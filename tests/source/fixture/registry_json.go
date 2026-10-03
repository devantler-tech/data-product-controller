package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// registryJSON rejects duplicate fields before struct decoding could replace them.
func registryJSON(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := registryJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("registry JSON contains trailing input")
	}
	return nil
}

func registryJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("registry JSON exceeds nesting bound")
	}
	token, err := decoder.Token()
	if err != nil {
		return errors.New("registry JSON unavailable")
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			key, keyErr := decoder.Token()
			field, valid := key.(string)
			if keyErr != nil || !valid {
				return errors.New("registry JSON field unavailable")
			}
			// Struct decoding treats these names case-insensitively.
			field = strings.ToLower(field)
			if _, duplicate := seen[field]; duplicate {
				return errors.New("registry JSON contains ambiguous fields")
			}
			seen[field] = struct{}{}
			if err := registryJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := registryJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("registry JSON has unexpected delimiter")
	}
	_, err = decoder.Token()
	return err
}
