// The example reads a saved v2 publisher report using only the Go standard library.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	errUnsupported = errors.New("unsupported publisher report")
	knownFeatures  = strings.Fields(
		"provisioned-sources engine-providers connector-readiness composition contract-readiness ui-contract ui-appearance dcat-catalog",
	)
	knownCodes = strings.Fields(
		"NoProducts SourceLimit DocumentLimit ReadFailed InputLimit ProductLimit DepthLimit InvalidDocument UnsupportedResource UnknownField AdmissionInvalid NamespaceRequired InvalidNamespace IdentityConflict InvalidPublicMetadata InvalidUI ContractOutputNotFound ProducerUnresolved OutputNotFound ContractIncompatible CrossNamespaceInput CompositionCycle CompositionLimit ValidationLimit ValidationUnavailable PreviewLimit InvalidField RequiredField DuplicateListItem CrossFieldInvalid",
	)
	knownPath = regexp.MustCompile(
		"^(/(apiVersion|kind|metadata|spec)(/[A-Za-z][A-Za-z0-9]*|/[0-9]+)*)?$",
	)
)

// exact rejects missing, null, case-varied and unknown fields before any typed interpretation.
func exact(value any, required, optional string) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil, errUnsupported
	}
	requiredKeys, optionalKeys := strings.Fields(required), strings.Fields(optional)
	for _, key := range requiredKeys {
		if object[key] == nil {
			return nil, errUnsupported
		}
	}
	for key, value := range object {
		if value == nil ||
			!slices.Contains(requiredKeys, key) && !slices.Contains(optionalKeys, key) {
			return nil, errUnsupported
		}
	}
	return object, nil
}

// array requires a non-null array within the example's explicit collection bound.
func array(value any, limit int) ([]any, error) {
	result, ok := value.([]any)
	if !ok || result == nil || len(result) > limit {
		return nil, errUnsupported
	}
	return result, nil
}

// integer accepts only nonnegative integral JSON numbers within the named report bound.
func integer(value any, maximum int) (int, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, errUnsupported
	}
	n, err := number.Int64()
	if err != nil || n < 0 || n > int64(maximum) {
		return 0, errUnsupported
	}
	return int(n), nil
}

// text requires a nonempty public string within its encoded byte limit.
func text(value any, maximum int) (string, error) {
	result, ok := value.(string)
	if !ok || result == "" || len(result) > maximum {
		return "", errUnsupported
	}
	return result, nil
}

// boolean rejects coercions and requires an explicit JSON boolean.
func boolean(value any) (bool, error) {
	result, ok := value.(bool)
	if !ok {
		return false, errUnsupported
	}
	return result, nil
}

// featureSet checks the report's sorted, unique list of known deployment requirements.
func featureSet(value any) (map[string]bool, error) {
	values, err := array(value, 8)
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	previous := ""
	for _, value := range values {
		feature, err := text(value, 64)
		if err != nil || !slices.Contains(knownFeatures, feature) || result[feature] ||
			feature <= previous {
			return nil, errUnsupported
		}
		result[feature] = true
		previous = feature
	}
	return result, nil
}

// readValue rejects duplicate JSON keys recursively, rather than accepting last-value-wins decoding.
func readValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errUnsupported
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, errUnsupported
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	switch delimiter {
	case '{':
		result := map[string]any{}
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return nil, errUnsupported
			}
			if _, exists := result[key]; exists {
				return nil, errUnsupported
			}
			child, err := readValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = child
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errUnsupported
		}
		return result, nil
	case '[':
		result := []any{}
		for decoder.More() {
			if len(result) >= 1024 {
				return nil, errUnsupported
			}
			child, err := readValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, child)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errUnsupported
		}
		return result, nil
	default:
		return nil, errUnsupported
	}
}

type (
	sourceSummary struct{ documents, products int }
	publicProduct struct {
		key              string
		id               string
		inputs           []map[string]any
		outputs          map[string]bool
		requiredFeatures map[string]bool
	}
)

// reference validates the closed product-reference profile and identifier length bounds.
func reference(value any) (map[string]any, error) {
	ref, err := exact(value, "name output", "namespace")
	if err != nil {
		return nil, err
	}
	for key, value := range ref {
		maximum := 63
		if key == "name" {
			maximum = 253
		}
		if _, err := text(value, maximum); err != nil {
			return nil, err
		}
	}
	return ref, nil
}

// owner checks the preview's public owner fields without fetching an optional URL.
func owner(value any) error {
	object, err := exact(value, "name", "url")
	if err != nil {
		return err
	}
	for _, value := range object {
		if _, err := text(value, 16384); err != nil {
			return err
		}
	}
	return nil
}

// readiness requires the offline preview to describe readiness as unobserved.
func readiness(value any) error {
	object, err := exact(value, "reason message", "")
	if err != nil {
		return err
	}
	reason, err := text(object["reason"], 32)
	if err != nil || reason != "unobserved" {
		return errUnsupported
	}
	_, err = text(object["message"], 16384)
	return err
}

// descriptor checks the example's closed publication-preview profile.
// Schema validation and the publisher's full API/public-metadata checks remain separate.
func descriptor(value any) (publicProduct, error) {
	result := publicProduct{outputs: map[string]bool{}, requiredFeatures: map[string]bool{}}
	object, err := exact(
		value,
		"apiVersion kind namespace name id displayName description version owner outputs ready readiness generation observedGeneration health",
		"documentationUrl inputs ui composition lineage",
	)
	if err != nil || object["apiVersion"] != "data-product-descriptor/v1" ||
		object["kind"] != "DataProduct" {
		return result, errUnsupported
	}
	for _, key := range []string{"namespace", "name", "id", "displayName", "description", "version", "documentationUrl"} {
		if value, exists := object[key]; exists {
			if _, err := text(value, 16384); err != nil {
				return result, err
			}
		}
	}
	namespace, _ := text(object["namespace"], 63)
	name, _ := text(object["name"], 253)
	if namespace == "" || name == "" {
		return result, errUnsupported
	}
	result.key = namespace + "/" + name
	result.id, err = text(object["id"], 16384)
	if err != nil {
		return result, err
	}
	ready, err := boolean(object["ready"])
	if err != nil || ready {
		return result, errUnsupported
	}
	for _, key := range []string{"generation", "observedGeneration"} {
		if n, err := integer(object[key], 0); err != nil || n != 0 {
			return result, errUnsupported
		}
	}
	if err := owner(object["owner"]); err != nil {
		return result, err
	}
	if err := readiness(object["readiness"]); err != nil {
		return result, err
	}
	health, err := exact(object["health"], "source connector contracts composition", "")
	if err != nil {
		return result, err
	}
	for _, value := range health {
		dimension, err := exact(value, "state message generation observedGeneration", "")
		if err != nil {
			return result, err
		}
		if dimension["state"] != "unobserved" && dimension["state"] != "not-applicable" {
			return result, errUnsupported
		}
		if _, err := text(dimension["message"], 16384); err != nil {
			return result, err
		}
		for _, key := range []string{"generation", "observedGeneration"} {
			if _, err := integer(dimension[key], 0); err != nil {
				return result, err
			}
		}
	}
	outputs, err := array(object["outputs"], 1024)
	if err != nil || len(outputs) == 0 {
		return result, errUnsupported
	}
	protocols := strings.Fields("OpenAPI AsyncAPI GraphQL DCAT ArrowFlight")
	for _, value := range outputs {
		output, err := exact(value, "name protocol url contractUrl", "mediaType")
		if err != nil {
			return result, err
		}
		for _, value := range output {
			if _, err := text(value, 16384); err != nil {
				return result, err
			}
		}
		name, _ := text(output["name"], 63)
		protocol, _ := text(output["protocol"], 32)
		if name == "" || result.outputs[name] || !slices.Contains(protocols, protocol) {
			return result, errUnsupported
		}
		result.outputs[name] = true
	}
	if value, exists := object["inputs"]; exists {
		inputs, err := array(value, 1024)
		if err != nil {
			return result, err
		}
		seen := map[string]bool{}
		for _, value := range inputs {
			input, err := exact(value, "name productRef", "contract")
			if err != nil {
				return result, err
			}
			name, err := text(input["name"], 63)
			if err != nil || seen[name] {
				return result, errUnsupported
			}
			seen[name] = true
			if _, err := reference(input["productRef"]); err != nil {
				return result, err
			}
			if value, exists := input["contract"]; exists {
				contract, err := exact(value, "minimumVersion protocol", "")
				if err != nil {
					return result, err
				}
				if _, err := text(contract["minimumVersion"], 64); err != nil {
					return result, err
				}
				protocol, err := text(contract["protocol"], 32)
				if err != nil || !slices.Contains(protocols, protocol) {
					return result, errUnsupported
				}
			}
			result.inputs = append(result.inputs, input)
		}
		if len(result.inputs) != 0 {
			result.requiredFeatures["composition"] = true
		}
	}
	if value, exists := object["composition"]; exists {
		if err := readiness(value); err != nil {
			return result, err
		}
	}
	if value, exists := object["lineage"]; exists {
		values, err := array(value, 0)
		if err != nil || len(values) != 0 {
			return result, errUnsupported
		}
	}
	if value, exists := object["ui"]; exists {
		ui, err := exact(value, "url title", "contract")
		if err != nil {
			return result, err
		}
		if _, err := text(ui["url"], 2048); err != nil {
			return result, err
		}
		title, err := text(ui["title"], 800)
		if err != nil || len(utf16.Encode([]rune(title))) > 200 {
			return result, errUnsupported
		}
		if value, exists := ui["contract"]; exists {
			contract, err := exact(value, "apiVersion hostOrigins capabilities", "")
			if err != nil {
				return result, err
			}
			version := contract["apiVersion"]
			if version != "data-product-ui/v1" && version != "data-product-ui/v2" {
				return result, errUnsupported
			}
			result.requiredFeatures["ui-contract"] = true
			if version == "data-product-ui/v2" {
				result.requiredFeatures["ui-appearance"] = true
			}
			origins, err := array(contract["hostOrigins"], 16)
			if err != nil || len(origins) == 0 {
				return result, errUnsupported
			}
			seen := map[string]bool{}
			for _, value := range origins {
				origin, err := text(value, 253)
				if err != nil || seen[origin] {
					return result, errUnsupported
				}
				seen[origin] = true
			}
			capabilities, err := array(contract["capabilities"], 3)
			if err != nil {
				return result, err
			}
			seen = map[string]bool{}
			for _, value := range capabilities {
				capability, err := text(value, 32)
				if err != nil || seen[capability] ||
					!slices.Contains([]string{"status", "resize", "appearance"}, capability) ||
					version == "data-product-ui/v1" && capability == "appearance" {
					return result, errUnsupported
				}
				seen[capability] = true
			}
		}
	}
	encoded, err := json.Marshal(object)
	if err != nil || len(encoded) > 64<<10 {
		return result, errUnsupported
	}
	return result, nil
}

// validateReport checks report shape, counts, provenance and plan consistency without trusting validity claims.
func validateReport(value any) (map[string]any, error) {
	// plan is the sole nullable field and is checked separately.
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, errUnsupported
	}
	plan, exists := raw["plan"]
	if !exists {
		return nil, errUnsupported
	}
	delete(raw, "plan")
	report, err := exact(
		raw,
		"apiVersion valid complete products sources requiredFeatures productFeatures diagnostics diagnosticCounts descriptors",
		"",
	)
	raw["plan"] = plan
	if err != nil || report["apiVersion"] != "data-product-preflight/v2" {
		return nil, errUnsupported
	}
	valid, err := boolean(report["valid"])
	if err != nil {
		return nil, err
	}
	complete, err := boolean(report["complete"])
	if err != nil || complete && !valid {
		return nil, errUnsupported
	}
	products, err := integer(report["products"], 256)
	if err != nil {
		return nil, err
	}
	sourceValues, err := array(report["sources"], 32)
	if err != nil {
		return nil, err
	}
	sources := []sourceSummary{}
	totalProducts, totalDocuments := 0, 0
	for index, value := range sourceValues {
		source, err := exact(value, "source documents products", "")
		if err != nil {
			return nil, err
		}
		ordinal, err := integer(source["source"], 32)
		if err != nil || ordinal != index+1 {
			return nil, errUnsupported
		}
		documents, err := integer(source["documents"], 4097)
		if err != nil {
			return nil, err
		}
		count, err := integer(source["products"], 256)
		if err != nil || count > documents {
			return nil, errUnsupported
		}
		sources = append(sources, sourceSummary{documents: documents, products: count})
		totalProducts += count
		totalDocuments += documents
	}
	if totalProducts != products || totalDocuments > 4097 || valid && totalDocuments > 4096 {
		return nil, errUnsupported
	}
	position := func(source, document any, allowGlobal bool) (string, error) {
		s, err := integer(source, 32)
		if err != nil {
			return "", err
		}
		d, err := integer(document, 4097)
		if err != nil {
			return "", err
		}
		if s == 0 {
			if !allowGlobal || d != 0 {
				return "", errUnsupported
			}
		} else if s > len(sources) || d > sources[s-1].documents || !allowGlobal && d == 0 {
			return "", errUnsupported
		}
		return fmt.Sprintf("%d/%d", s, d), nil
	}
	required, err := featureSet(report["requiredFeatures"])
	if err != nil {
		return nil, err
	}
	features, err := array(report["productFeatures"], 256)
	if err != nil {
		return nil, err
	}
	featurePositions := map[string]map[string]bool{}
	union := map[string]bool{}
	featureCounts := map[int]int{}
	for _, value := range features {
		entry, err := exact(value, "source document requiredFeatures", "")
		if err != nil {
			return nil, err
		}
		key, err := position(entry["source"], entry["document"], false)
		if err != nil || featurePositions[key] != nil {
			return nil, errUnsupported
		}
		source, _ := integer(entry["source"], 32)
		featureCounts[source]++
		set, err := featureSet(entry["requiredFeatures"])
		if err != nil {
			return nil, err
		}
		featurePositions[key] = set
		for key := range set {
			union[key] = true
		}
	}
	for index, source := range sources {
		count := featureCounts[index+1]
		if count > source.products || complete && count != source.products {
			return nil, errUnsupported
		}
	}
	if len(union) != len(required) {
		return nil, errUnsupported
	}
	for key := range union {
		if !required[key] {
			return nil, errUnsupported
		}
	}
	counts, err := exact(report["diagnosticCounts"], "total errors warnings omitted", "")
	if err != nil {
		return nil, err
	}
	numbers := map[string]int{}
	for key, value := range counts {
		n, err := integer(value, 1<<20)
		if err != nil {
			return nil, err
		}
		numbers[key] = n
	}
	diagnostics, err := array(report["diagnostics"], 128)
	if err != nil {
		return nil, err
	}
	retainedErrors, retainedWarnings := 0, 0
	for _, value := range diagnostics {
		d, err := exact(
			value,
			"source document line column severity code path message witness witnessTruncated",
			"",
		)
		if err != nil {
			return nil, err
		}
		if _, err := position(d["source"], d["document"], true); err != nil {
			return nil, err
		}
		line, err := integer(d["line"], (2<<20)+1)
		if err != nil {
			return nil, err
		}
		column, err := integer(d["column"], (2<<20)+1)
		if err != nil || (line == 0) != (column == 0) {
			return nil, errUnsupported
		}
		code, err := text(d["code"], 64)
		if err != nil || !slices.Contains(knownCodes, code) {
			return nil, errUnsupported
		}
		path, ok := d["path"].(string)
		if !ok || len(path) > 4096 || !knownPath.MatchString(path) {
			return nil, errUnsupported
		}
		if _, err := text(d["message"], 256); err != nil {
			return nil, err
		}
		switch d["severity"] {
		case "error":
			retainedErrors++
		case "warning":
			retainedWarnings++
		default:
			return nil, errUnsupported
		}
		if _, err := boolean(d["witnessTruncated"]); err != nil {
			return nil, err
		}
		witness, err := array(d["witness"], 64)
		if err != nil {
			return nil, err
		}
		for _, value := range witness {
			step, err := exact(value, "source document path", "")
			if err != nil {
				return nil, err
			}
			if _, err := position(step["source"], step["document"], false); err != nil {
				return nil, err
			}
			path, ok := step["path"].(string)
			if !ok || len(path) > 4096 || !knownPath.MatchString(path) {
				return nil, errUnsupported
			}
		}
	}
	if numbers["total"] != numbers["errors"]+numbers["warnings"] ||
		numbers["total"]-len(diagnostics) != numbers["omitted"] ||
		retainedErrors > numbers["errors"] ||
		retainedWarnings > numbers["warnings"] ||
		numbers["omitted"] == 0 &&
			(retainedErrors != numbers["errors"] || retainedWarnings != numbers["warnings"]) {
		return nil, errUnsupported
	}
	if valid != (numbers["errors"] == 0) || complete != (numbers["total"] == 0) {
		return nil, errUnsupported
	}
	descriptors, err := array(report["descriptors"], 256)
	if err != nil {
		return nil, err
	}
	if !complete {
		if plan != nil || len(descriptors) != 0 {
			return nil, errUnsupported
		}
		return report, nil
	}
	if products == 0 || len(descriptors) != products || len(features) != products {
		return nil, errUnsupported
	}
	byKey := map[string]publicProduct{}
	ids := map[string]bool{}
	for _, value := range descriptors {
		product, err := descriptor(value)
		if err != nil {
			return nil, err
		}
		if _, exists := byKey[product.key]; exists || ids[product.id] {
			return nil, errUnsupported
		}
		byKey[product.key] = product
		ids[product.id] = true
	}
	planObject, err := exact(plan, "order edges", "")
	if err != nil {
		return nil, err
	}
	order, err := array(planObject["order"], 256)
	if err != nil || len(order) != products {
		return nil, errUnsupported
	}
	rank := map[string]int{}
	origins := map[string]string{}
	usedPositions := map[string]bool{}
	for index, value := range order {
		entry, err := exact(value, "key source document", "")
		if err != nil {
			return nil, err
		}
		key, err := text(entry["key"], 317)
		if err != nil {
			return nil, err
		}
		if _, exists := byKey[key]; !exists {
			return nil, errUnsupported
		}
		if _, exists := rank[key]; exists {
			return nil, errUnsupported
		}
		at, err := position(entry["source"], entry["document"], false)
		if err != nil || featurePositions[at] == nil || usedPositions[at] {
			return nil, errUnsupported
		}
		for feature := range byKey[key].requiredFeatures {
			if !featurePositions[at][feature] {
				return nil, errUnsupported
			}
		}
		usedPositions[at] = true
		rank[key] = index
		origins[key] = at
	}
	edges, err := array(planObject["edges"], 1024)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	expected := 0
	for _, product := range byKey {
		expected += len(product.inputs)
	}
	if len(edges) != expected {
		return nil, errUnsupported
	}
	for _, value := range edges {
		edge, err := exact(value, "consumer producer input inputIndex output source document", "")
		if err != nil {
			return nil, err
		}
		consumer, err := text(edge["consumer"], 317)
		if err != nil {
			return nil, err
		}
		producer, err := text(edge["producer"], 317)
		if err != nil {
			return nil, err
		}
		c, cExists := byKey[consumer]
		p, pExists := byKey[producer]
		if !cExists || !pExists || rank[producer] >= rank[consumer] {
			return nil, errUnsupported
		}
		index, err := integer(edge["inputIndex"], 1023)
		if err != nil || index >= len(c.inputs) {
			return nil, errUnsupported
		}
		input := c.inputs[index]
		ref, _ := reference(input["productRef"])
		consumerNamespace := strings.SplitN(consumer, "/", 2)[0]
		namespace := consumerNamespace
		if selected, ok := ref["namespace"]; ok {
			namespace, err = text(selected, 63)
			if err != nil {
				return nil, err
			}
		}
		if namespace != consumerNamespace ||
			strings.SplitN(producer, "/", 2)[0] != consumerNamespace {
			return nil, errUnsupported
		}
		name, err := text(ref["name"], 253)
		if err != nil {
			return nil, err
		}
		output, err := text(ref["output"], 63)
		if err != nil {
			return nil, err
		}
		if namespace+"/"+name != producer || edge["input"] != input["name"] ||
			edge["output"] != ref["output"] ||
			!p.outputs[output] {
			return nil, errUnsupported
		}
		at, err := position(edge["source"], edge["document"], false)
		if err != nil || at != origins[consumer] {
			return nil, errUnsupported
		}
		key := fmt.Sprintf("%s/%d", consumer, index)
		if seen[key] {
			return nil, errUnsupported
		}
		seen[key] = true
	}
	return report, nil
}

// readReportFile opens one selected regular file without blocking on a FIFO.
// This is a bounded profile reader, not a generic JSON Schema or Kubernetes validator.
func readReportFile(path string) (map[string]any, error) {
	// #nosec G304 G703 -- there is no privileged root: the caller explicitly selects this local report.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errUnsupported
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errUnsupported
	}
	data, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil || len(data) > 2<<20 || !utf8.Valid(data) || !validUnicodeEscapes(data) {
		return nil, errUnsupported
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errUnsupported
	}
	return validateReport(value)
}

// validUnicodeEscapes rejects decoding that would replace an unpaired escaped surrogate.
// This independent reader preserves deliberate U+FFFD, paired surrogates and escaped backslashes.
func validUnicodeEscapes(data []byte) bool {
	inString := false
	for index := 0; index < len(data); index++ {
		if data[index] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[index] != '\\' {
			continue
		}
		index++
		if index >= len(data) {
			return false
		}
		if data[index] != 'u' {
			continue
		}
		if index+4 >= len(data) {
			return false
		}
		code, err := strconv.ParseUint(string(data[index+1:index+5]), 16, 16)
		if err != nil {
			return false
		}
		index += 4
		if code >= 0xD800 && code <= 0xDBFF {
			if index+6 >= len(data) || data[index+1] != '\\' || data[index+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[index+3:index+7]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			index += 6
		} else if code >= 0xDC00 && code <= 0xDFFF {
			return false
		}
	}
	return true
}

// main prints bounded report claims and a static review order, or a fixed rejection message.
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run main.go report.json")
		os.Exit(2)
	}
	report, err := readReportFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not read a supported publisher report.")
		os.Exit(1)
	}
	fmt.Printf(
		"Declarations: valid=%t complete=%t products=%s; live readiness and access remain unverified.\n",
		report["valid"],
		report["complete"],
		report["products"],
	)
	diagnostics, _ := array(report["diagnostics"], 128)
	for _, value := range diagnostics {
		d, ok := value.(map[string]any)
		if !ok {
			return
		}
		fmt.Printf(
			"source %s document %s %q %q: %q\n",
			d["source"],
			d["document"],
			d["code"],
			d["path"],
			d["message"],
		)
	}
	if report["plan"] != nil {
		plan, ok := report["plan"].(map[string]any)
		if !ok {
			return
		}
		order, _ := array(plan["order"], 256)
		for _, value := range order {
			entry, ok := value.(map[string]any)
			if !ok {
				return
			}
			fmt.Printf("Review %q\n", entry["key"])
		}
	}
}
