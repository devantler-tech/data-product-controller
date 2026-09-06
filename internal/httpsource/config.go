package httpsource

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"regexp"
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
	file, err := os.Open(path)
	if err != nil {
		return invalid, errInvalidConfig
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil || len(data) > 16<<10 {
		return invalid, errInvalidConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	// Decode into a local map so the retained credential has no exported, serializable field.
	var fields map[string]string
	if err := decoder.Decode(&fields); err != nil || len(fields) != 2 {
		return invalid, errInvalidConfig
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return invalid, errInvalidConfig
	}
	config := sourceConfig{endpointURL: fields["endpointURL"], bearerToken: fields["bearerToken"]}
	endpoint, err := url.Parse(config.endpointURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" ||
		endpoint.User != nil ||
		endpoint.Fragment != "" ||
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
