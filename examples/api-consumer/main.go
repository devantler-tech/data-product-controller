// api-consumer demonstrates caller-owned access to the Harbour observations output.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type observation struct {
	Station     string    `json:"station"`
	Temperature float64   `json:"temperatureCelsius"`
	Salinity    float64   `json:"salinityPsu"`
	ObservedAt  time.Time `json:"observedAt"`
}

var (
	errDescriptor = errors.New("select a bounded public descriptor with one supported output")
	errEndpoint   = errors.New(
		"the selected output must match an explicitly approved HTTPS endpoint",
	)
	errQuery    = errors.New("the observations request could not complete")
	errResponse = errors.New(
		"the observations response is incomplete, invalid or exceeds the example's bounds",
	)
	portName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

type output struct {
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	URL         string `json:"url"`
	ContractURL string `json:"contractUrl"`
	MediaType   string `json:"mediaType"`
}

// selectOutput reads the interface projection, not readiness or a general descriptor schema.
func selectOutput(input io.Reader, name, approved string) (string, error) {
	data, err := boundedJSON(input, 64<<10)
	if err != nil || len(name) > 63 || !portName.MatchString(name) {
		return "", errDescriptor
	}
	var descriptor map[string]json.RawMessage
	if json.Unmarshal(data, &descriptor) != nil {
		return "", errDescriptor
	}
	var version, kind string
	var outputs []json.RawMessage
	if json.Unmarshal(descriptor["apiVersion"], &version) != nil ||
		version != "data-product-descriptor/v1" ||
		json.Unmarshal(descriptor["kind"], &kind) != nil ||
		kind != "DataProduct" ||
		json.Unmarshal(descriptor["outputs"], &outputs) != nil ||
		len(outputs) == 0 ||
		len(outputs) > 1024 {
		return "", errDescriptor
	}
	seen := make(map[string]bool)
	var selected *output
	for _, raw := range outputs {
		if !exactFields(raw, []string{"name", "protocol", "url", "contractUrl"}, "mediaType") {
			return "", errDescriptor
		}
		candidate := &output{}
		if json.Unmarshal(raw, candidate) != nil {
			return "", errDescriptor
		}
		if len(candidate.Name) > 63 || !portName.MatchString(candidate.Name) ||
			seen[candidate.Name] {
			return "", errDescriptor
		}
		seen[candidate.Name] = true
		if candidate.Name == name {
			selected = candidate
		}
	}
	if selected == nil || selected.Protocol != "OpenAPI" ||
		selected.MediaType != "application/json" ||
		!literalHTTPS(selected.ContractURL) {
		return "", errDescriptor
	}
	if !literalHTTPS(approved) || selected.URL != approved {
		return "", errEndpoint
	}
	return approved, nil
}

// literalHTTPS refuses alternate spellings and embedded authentication before any request.
func literalHTTPS(value string) bool {
	if len(value) == 0 || len(value) > 1024 || strings.ContainsAny(value, "\\%") {
		return false
	}
	for _, char := range value {
		if char <= ' ' || char >= 127 {
			return false
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" ||
		parsed.String() != value || strings.HasSuffix(parsed.Host, ":") {
		return false
	}
	host := parsed.Hostname()
	if address, err := netip.ParseAddr(host); err == nil {
		if address.Zone() != "" || address.String() != host {
			return false
		}
	} else {
		labels := strings.Split(host, ".")
		if len(host) > 253 || strings.Trim(labels[len(labels)-1], "0123456789") == "" ||
			strings.HasPrefix(labels[len(labels)-1], "0x") {
			return false
		}
		for _, label := range labels {
			if len(label) > 63 || !portName.MatchString(label) {
				return false
			}
		}
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return false
		}
	}
	return true
}

// consume owns no controller connection and uses only the caller-approved data endpoint.
func consume(
	ctx context.Context,
	descriptor io.Reader,
	name, approved, station string,
	client *http.Client,
) ([]observation, error) {
	endpoint, err := selectOutput(descriptor, name, approved)
	if err != nil {
		return nil, err
	}
	if client == nil || (station != "" && (len(station) > 63 || !portName.MatchString(station))) {
		return nil, errQuery
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	queryURL, _ := url.Parse(endpoint)
	if queryURL.RawQuery != "" || queryURL.ForceQuery {
		return nil, errEndpoint
	}
	if station != "" {
		query := url.Values{}
		query.Set("station", station)
		queryURL.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL.String(), nil)
	if err != nil {
		return nil, errQuery
	}
	request.Header.Set("Accept", "application/json")
	owned := *client
	owned.Jar = nil
	owned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// #nosec G704 -- The caller independently approves this exact literal HTTPS endpoint;
	// selectOutput rejects any descriptor mismatch before access, and redirects and cookies are denied.
	response, err := owned.Do(request)
	if err != nil {
		return nil, errQuery
	}
	defer func() { _ = response.Body.Close() }()
	mediaType, parameters, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || err != nil || mediaType != "application/json" ||
		(parameters["charset"] != "" && !strings.EqualFold(parameters["charset"], "utf-8")) {
		return nil, errResponse
	}
	data, err := boundedJSON(response.Body, 1<<20)
	if err != nil || ctx.Err() != nil {
		return nil, errResponse
	}
	var envelope map[string][]json.RawMessage
	if !exactFields(data, []string{"items"}) || json.Unmarshal(data, &envelope) != nil ||
		envelope["items"] == nil || len(envelope["items"]) > 64 {
		return nil, errResponse
	}
	items := make([]observation, 0, len(envelope["items"]))
	for _, raw := range envelope["items"] {
		if !exactFields(
			raw,
			[]string{"station", "temperatureCelsius", "salinityPsu", "observedAt"},
		) {
			return nil, errResponse
		}
		var item struct {
			Station     *string    `json:"station"`
			Temperature *float64   `json:"temperatureCelsius"`
			Salinity    *float64   `json:"salinityPsu"`
			ObservedAt  *time.Time `json:"observedAt"`
		}
		if json.Unmarshal(raw, &item) != nil {
			return nil, errResponse
		}
		if item.Station == nil || len(*item.Station) > 63 || !portName.MatchString(*item.Station) ||
			(station != "" && *item.Station != station) || item.Temperature == nil ||
			item.Salinity == nil || item.ObservedAt == nil || item.ObservedAt.IsZero() {
			return nil, errResponse
		}
		items = append(items, observation{
			Station: *item.Station, Temperature: *item.Temperature,
			Salinity: *item.Salinity, ObservedAt: *item.ObservedAt,
		})
	}
	return items, nil
}

// exactFields preserves case-sensitive public shapes without importing a controller library.
func exactFields(data []byte, required []string, optional ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return false
	}
	allowed := make(map[string]bool)
	for _, name := range required {
		if _, exists := fields[name]; !exists {
			return false
		}
		allowed[name] = true
	}
	for _, name := range optional {
		allowed[name] = true
	}
	for name := range fields {
		if !allowed[name] {
			return false
		}
	}
	return true
}

// boundedJSON refuses partial reads and ambiguous keys before publishing any result.
func boundedJSON(input io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil || int64(len(data)) > limit || !utf8.Valid(data) {
		return nil, errResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nodes := 0
	if uniqueValue(decoder, 0, &nodes) != nil {
		return nil, errResponse
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errResponse
	}
	return data, nil
}

func uniqueValue(decoder *json.Decoder, depth int, nodes *int) error {
	*nodes++
	if depth > 32 || *nodes > 16384 {
		return errResponse
	}
	token, err := decoder.Token()
	if err != nil {
		return errResponse
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return errResponse
	}
	keys := make(map[string]bool)
	for decoder.More() {
		if delimiter == '{' {
			key, keyErr := decoder.Token()
			name, valid := key.(string)
			if keyErr != nil || !valid || keys[name] {
				return errResponse
			}
			keys[name] = true
		}
		if uniqueValue(decoder, depth+1, nodes) != nil {
			return errResponse
		}
	}
	closing, err := decoder.Token()
	if err != nil || (delimiter == '{' && closing != json.Delim('}')) ||
		(delimiter == '[' && closing != json.Delim(']')) {
		return errResponse
	}
	return nil
}

func main() {
	os.Exit(command())
}

func command() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		_, _ = fmt.Fprintln(os.Stderr, errQuery)
		return 1
	}
	transport := base.Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	if err := run(ctx, os.Args[1:], os.Stdout, &http.Client{Transport: transport}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// run is an opt-in executable example; publisher metadata cannot choose its local file.
func run(ctx context.Context, args []string, writer io.Writer, client *http.Client) error {
	flags := flag.NewFlagSet("api-consumer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("descriptor", "", "Exported public descriptor file")
	name := flags.String("output", "", "Named observations output")
	approved := flags.String("approved-endpoint", "", "Exact independently approved HTTPS URL")
	station := flags.String("station", "", "Optional station filter")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *path == "" || *name == "" ||
		*approved == "" {
		return errors.New(
			"usage: api-consumer --descriptor FILE --output NAME --approved-endpoint HTTPS_URL [--station STATION]",
		)
	}
	// #nosec G304 -- the caller explicitly selects this regular local descriptor.
	file, err := os.OpenFile(*path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errDescriptor
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errDescriptor
	}
	items, err := consume(ctx, file, *name, *approved, *station, client)
	if err != nil {
		return err
	}
	if json.NewEncoder(writer).Encode(struct {
		Items []observation `json:"items"`
	}{Items: items}) != nil {
		return errors.New("could not write validated observations")
	}
	return nil
}
