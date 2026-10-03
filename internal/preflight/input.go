package preflight

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	yamlv2 "go.yaml.in/yaml/v2"
	"go.yaml.in/yaml/v3"
	kubejson "k8s.io/apimachinery/pkg/util/json"
)

const maxInputBytes = 2 << 20

type document struct {
	product data.DataProduct
	object  map[string]any
}

// readDocuments rejects ambiguous input before decoding values or evaluating the schema.
func readDocuments(in io.Reader) ([]document, string, int) {
	input, err := io.ReadAll(io.LimitReader(in, maxInputBytes+1))
	if err != nil {
		return nil, "ReadFailed", 0
	}
	if len(input) > maxInputBytes {
		return nil, "InputLimit", 0
	}
	decoder := yaml.NewDecoder(bytes.NewReader(input))
	values := yamlv2.NewDecoder(bytes.NewReader(input))
	values.SetStrict(true)
	var documents []document
	for {
		var node yaml.Node
		err := decoder.Decode(&node)
		if errors.Is(err, io.EOF) {
			break
		}
		number := len(documents) + 1
		if err != nil {
			return nil, "InvalidDocument", number
		}
		if code := checkNode(&node, 0); code != "" {
			return nil, code, number
		}
		var value any
		if err := values.Decode(&value); err != nil {
			return nil, "InvalidDocument", number
		}
		if value == nil {
			continue
		}
		if len(documents) >= 256 {
			return nil, "ProductLimit", number
		}
		value, err = jsonValue(value)
		if err != nil {
			return nil, "InvalidDocument", number
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, "InvalidDocument", number
		}
		var object map[string]any
		if err := kubejson.Unmarshal(encoded, &object); err != nil || object == nil {
			return nil, "InvalidDocument", number
		}
		if object["apiVersion"] != "data.devantler.tech/v1alpha1" ||
			object["kind"] != "DataProduct" {
			return nil, "UnsupportedResource", number
		}
		var product data.DataProduct
		typed := json.NewDecoder(bytes.NewReader(encoded))
		typed.DisallowUnknownFields()
		if err := typed.Decode(&product); err != nil {
			// Do not emit parser errors: both field names and values are untrusted.
			var typeError *json.UnmarshalTypeError
			if errors.As(err, &typeError) {
				return nil, "AdmissionInvalid", number
			}
			return nil, "UnknownField", number
		}
		documents = append(documents, document{product: product, object: object})
	}
	if len(documents) == 0 {
		return nil, "NoProducts", 0
	}
	return documents, "", 0
}

// Decode original scalars with Kubernetes's YAML 1.1 decoder, after the syntax
// guards. A v3 Node loses bare ! tags, so it cannot supply deployment semantics.
// Normalize maps without another YAML serialization or parsing round trip.
func jsonValue(value any) (any, error) {
	switch value := value.(type) {
	case map[any]any:
		object := make(map[string]any, len(value))
		for key, child := range value {
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("non-string key")
			}
			converted, err := jsonValue(child)
			if err != nil {
				return nil, err
			}
			object[name] = converted
		}
		return object, nil
	case map[string]any:
		object := make(map[string]any, len(value))
		for key, child := range value {
			converted, err := jsonValue(child)
			if err != nil {
				return nil, err
			}
			object[key] = converted
		}
		return object, nil
	case []any:
		values := make([]any, len(value))
		for index, child := range value {
			converted, err := jsonValue(child)
			if err != nil {
				return nil, err
			}
			values[index] = converted
		}
		return values, nil
	default:
		return value, nil
	}
}

func checkNode(node *yaml.Node, depth int) string {
	if depth > 64 {
		return "DepthLimit"
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return "InvalidDocument"
	}
	if node.Tag != "" && node.Tag != "!!map" && node.Tag != "!!seq" && node.Tag != "!!str" &&
		node.Tag != "!!int" && node.Tag != "!!float" && node.Tag != "!!bool" && node.Tag != "!!null" && node.Tag != "!!timestamp" {
		return "InvalidDocument"
	}
	switch node.Kind {
	case yaml.DocumentNode, yaml.MappingNode, yaml.SequenceNode, yaml.ScalarNode:
	case yaml.AliasNode:
		return "InvalidDocument"
	default:
		return "InvalidDocument"
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || seen[key.Value] {
				return "InvalidDocument"
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if code := checkNode(child, depth+1); code != "" {
			return code
		}
	}
	return ""
}
