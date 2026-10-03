package preflight

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
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
		if len(node.Content) == 0 || (len(node.Content) == 1 && node.Content[0].Tag == "!!null") {
			continue
		}
		if len(documents) >= 256 {
			return nil, "ProductLimit", number
		}
		if code := checkNode(&node, 0); code != "" {
			return nil, code, number
		}
		value, err := decodeYAMLValue(&node)
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

// Decode the already checked syntax tree without serializing and reparsing YAML.
// Date scalars retain their authored string; admission owns format validation.
func decodeYAMLValue(node *yaml.Node) (any, error) {
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) != 1 {
			return nil, errors.New("invalid document")
		}
		return decodeYAMLValue(node.Content[0])
	case yaml.MappingNode:
		object := make(map[string]any, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			value, err := decodeYAMLValue(node.Content[index+1])
			if err != nil {
				return nil, err
			}
			object[node.Content[index].Value] = value
		}
		return object, nil
	case yaml.SequenceNode:
		values := make([]any, len(node.Content))
		for index, child := range node.Content {
			value, err := decodeYAMLValue(child)
			if err != nil {
				return nil, err
			}
			values[index] = value
		}
		return values, nil
	case yaml.ScalarNode:
		if node.Tag == "!!timestamp" {
			return node.Value, nil
		}
		var value any
		if err := node.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	case yaml.AliasNode:
		return nil, errors.New("invalid document")
	default:
		return nil, errors.New("invalid document")
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
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
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
