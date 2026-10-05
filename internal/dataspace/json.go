package dataspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/devantler-tech/data-product-controller/internal/jsoninput"
)

// checkJSON rejects ambiguity that encoding/json normally accepts, including
// repeated keys, case-insensitive field aliases and lossy Unicode replacement.
func checkJSON(b []byte) error {
	if !utf8.Valid(b) {
		return errors.New("input must be UTF-8")
	}
	if !jsoninput.ValidUnicodeEscapes(b) {
		return errors.New("input contains an unpaired Unicode escape")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := value(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return errors.New("input must contain exactly one JSON value")
	}
	return nil
}

// value checks one bounded JSON subtree before typed decoding can discard ambiguity.
func value(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("input exceeds 32 nesting levels")
	}
	tok, err := d.Token()
	if err != nil {
		return errors.New("invalid JSON")
	}
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return errors.New("invalid object key")
				}
				s, ok := key.(string)
				if !ok || seen[s] || !knownKey(s) {
					return errors.New("duplicate or unsupported JSON field")
				}
				if s == "@context" && depth != 0 {
					return errors.New("nested contexts are unsupported")
				}
				seen[s] = true
				if err := value(d, depth+1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(d, depth+1); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
		if _, err := d.Token(); err != nil {
			return errors.New("invalid JSON delimiter")
		}
	case string:
		if len(v) > 16<<10 {
			return errors.New("string exceeds 16 KiB")
		}
	case nil:
		return errors.New("null values are unsupported")
	}
	return nil
}

// knownKey forbids case aliases; typed decoding then checks each key's exact location.
func knownKey(s string) bool {
	switch s {
	case "@id", "@type", "@context", "dcat", "dcterms", "foaf",
		"dcat:dataset", "dcat:service", "dcterms:identifier", "dcterms:title", "dcterms:description", "dcat:version", "dcterms:publisher", "dcat:landingPage", "dcat:distribution", "foaf:name", "foaf:page", "dcat:accessURL", "dcat:accessService", "dcterms:format", "dcat:endpointURL", "dcat:endpointDescription", "dcat:servesDataset",
		"version", "catalogId", "participantId", "service", "id", "endpointURL", "datasets", "distributions", "sourceId", "format", "offers", "assigner", "permission", "prohibition", "obligation", "action", "constraint", "leftOperand", "operator", "rightOperand":
		return true
	default:
		return false
	}
}
