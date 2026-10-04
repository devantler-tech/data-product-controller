package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	yamlv2 "go.yaml.in/yaml/v2"
	"go.yaml.in/yaml/v3"
	structural "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	kubejson "k8s.io/apimachinery/pkg/util/json"
	strictjson "sigs.k8s.io/json"
)

const maxInputBytes = 2 << 20

type document struct {
	product data.DataProduct
	object  map[string]any
	origin  Position
	fields  map[string]Position
}

// readDocuments rejects ambiguous input before decoding values or evaluating the schema.
func readDocuments(in io.Reader) ([]document, string, int) {
	documents, _, code, number, _ := readSelected(context.Background(), []io.Reader{in})
	return documents, code, number
}

type cancelReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read checks cancellation before each bounded read from the caller's selected source.
func (r cancelReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}

// readSelected never joins file boundaries or multiplies the aggregate limits.
func readSelected(
	ctx context.Context,
	sources []io.Reader,
) ([]document, []SourceSummary, string, int, Position) {
	if len(sources) > 32 {
		return nil, nil, "SourceLimit", 0, Position{}
	}
	var documents []document
	summaries := make([]SourceSummary, len(sources))
	for index := range summaries {
		summaries[index].Source = index + 1
	}
	remaining := maxInputBytes
	physical := 0
	for index, source := range sources {
		origin := Position{Source: index + 1}
		if ctx.Err() != nil {
			return nil, summaries, "ValidationLimit", len(documents) + 1, origin
		}
		if source == nil {
			return nil, summaries, "ReadFailed", 0, origin
		}
		input, err := io.ReadAll(
			io.LimitReader(cancelReader{ctx: ctx, reader: source}, int64(remaining)+1),
		)
		if err != nil {
			code := "ReadFailed"
			if ctx.Err() != nil {
				code = "ValidationLimit"
			}
			return nil, summaries, code, 0, origin
		}
		if len(input) > remaining {
			return nil, summaries, "InputLimit", 0, origin
		}
		remaining -= len(input)
		parsed, summary, code, number, failure := parseSource(
			ctx,
			input,
			index+1,
			len(documents),
			&physical,
		)
		summaries[index] = summary
		if code != "" {
			return nil, summaries, code, number, failure
		}
		documents = append(documents, parsed...)
	}
	if len(documents) == 0 {
		return nil, summaries, "NoProducts", 0, Position{}
	}
	return documents, summaries, "", 0, Position{}
}

// parseSource preserves one file's physical document positions under the aggregate document budget.
func parseSource(
	ctx context.Context,
	input []byte,
	source, prior int,
	physical *int,
) ([]document, SourceSummary, string, int, Position) {
	summary := SourceSummary{Source: source}
	origin := Position{Source: source}
	fail := func(code string, number int) ([]document, SourceSummary, string, int, Position) {
		return nil, summary, code, number, origin
	}
	decoder := yaml.NewDecoder(bytes.NewReader(input))
	values := yamlv2.NewDecoder(bytes.NewReader(input))
	values.SetStrict(true)
	var documents []document
	for {
		if ctx.Err() != nil {
			return fail("ValidationLimit", prior+len(documents)+1)
		}
		var node yaml.Node
		err := decoder.Decode(&node)
		if errors.Is(err, io.EOF) {
			break
		}
		number := prior + len(documents) + 1
		summary.Documents++
		*physical++
		origin = Position{Source: source, Document: summary.Documents}
		if *physical > 4096 {
			return fail("DocumentLimit", number)
		}
		if err != nil {
			return fail("InvalidDocument", number)
		}
		if len(node.Content) > 0 {
			origin.Line = node.Content[0].Line
			origin.Column = node.Content[0].Column
		}
		if code := checkNode(&node, 0); code != "" {
			return fail(code, number)
		}
		var value any
		if err := values.Decode(&value); err != nil {
			return fail("InvalidDocument", number)
		}
		if value == nil {
			continue
		}
		if prior+len(documents) >= 256 {
			return fail("ProductLimit", number)
		}
		value, err = jsonValue(value)
		if err != nil {
			return fail("InvalidDocument", number)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fail("InvalidDocument", number)
		}
		var object map[string]any
		if err := kubejson.Unmarshal(encoded, &object); err != nil || object == nil {
			return fail("InvalidDocument", number)
		}
		if object["apiVersion"] != "data.devantler.tech/v1alpha1" ||
			object["kind"] != "DataProduct" {
			return fail("UnsupportedResource", number)
		}
		var product data.DataProduct
		// Kubernetes treats JSON field names as case-sensitive, including metadata.
		// Keep strict errors private: their field names and values are untrusted.
		strictErrors, err := strictjson.UnmarshalStrict(encoded, &product)
		if err != nil {
			return fail("AdmissionInvalid", number)
		}
		if len(strictErrors) != 0 {
			return fail("UnknownField", number)
		}
		fields := map[string]Position{}
		if validator, err := loadAdmission(); err == nil && len(node.Content) > 0 {
			collectPositions(node.Content[0], validator.schema, "", origin, fields)
		}
		documents = append(
			documents,
			document{product: product, object: object, origin: origin, fields: fields},
		)
		summary.Products++
	}
	return documents, summary, "", 0, Position{}
}

// Positions retain only schema-known names and numeric array indexes.
func collectPositions(
	node *yaml.Node,
	shape *structural.Structural,
	path string,
	origin Position,
	fields map[string]Position,
) {
	if shape == nil {
		return
	}
	origin.Line = node.Line
	origin.Column = node.Column
	fields[path] = origin
	if node.Kind == yaml.MappingNode {
		for index := 0; index < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			child, ok := shape.Properties[key.Value]
			if !ok {
				continue
			}
			childPath := path + "/" + key.Value
			collectPositions(value, &child, childPath, origin, fields)
			at := origin
			at.Line = key.Line
			at.Column = key.Column
			fields[childPath] = at
		}
	} else if node.Kind == yaml.SequenceNode && shape.Items != nil {
		for index, child := range node.Content {
			collectPositions(child, shape.Items, path+"/"+strconv.Itoa(index), origin, fields)
		}
	}
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

// checkNode rejects ambiguous YAML syntax and excessive nesting before decoding submitted values.
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
