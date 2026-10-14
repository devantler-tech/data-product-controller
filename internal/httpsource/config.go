package httpsource

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/devantler-tech/data-product-controller/internal/jsoninput"
)

var (
	errInvalidConfig = errors.New("invalid source configuration")
	bearerPattern    = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)
)

type sourceConfig struct {
	endpointURL string
	bearerToken string
}

// readConfig reopens one projected file so credentials and endpoint rotate as an atomic pair.
func readConfig(path string) (sourceConfig, error) {
	invalid := sourceConfig{}
	// #nosec G304 G703 -- The path is operator-supplied process configuration, never an HTTP input.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return invalid, errInvalidConfig
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return invalid, errInvalidConfig
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil || len(data) > 16<<10 || !utf8.Valid(data) ||
		!jsoninput.ValidUnicodeEscapes(data) {
		return invalid, errInvalidConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return invalid, errInvalidConfig
	}
	// Inspect decoded keys before retaining values; duplicate and escaped aliases are ambiguous.
	fields := make(map[string]string, 2)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, valid := keyToken.(string)
		if err != nil || !valid || (key != "endpointURL" && key != "bearerToken") {
			return invalid, errInvalidConfig
		}
		if _, duplicate := fields[key]; duplicate {
			return invalid, errInvalidConfig
		}
		valueToken, err := decoder.Token()
		value, valid := valueToken.(string)
		if err != nil || !valid {
			return invalid, errInvalidConfig
		}
		fields[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(fields) != 2 {
		return invalid, errInvalidConfig
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return invalid, errInvalidConfig
	}
	config := sourceConfig{endpointURL: fields["endpointURL"], bearerToken: fields["bearerToken"]}
	endpoint, err := url.Parse(config.endpointURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" ||
		endpoint.User != nil ||
		strings.Contains(config.endpointURL, "#") ||
		endpoint.RawQuery != "" ||
		endpoint.ForceQuery ||
		endpoint.Opaque != "" {
		return invalid, errInvalidConfig
	}
	if !bearerPattern.MatchString(config.bearerToken) {
		return invalid, errInvalidConfig
	}
	return config, nil
}
