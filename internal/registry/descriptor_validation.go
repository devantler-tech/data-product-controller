package registry

import (
	"encoding/json"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/config"
	"k8s.io/apimachinery/pkg/util/validation"
)

const maxPortableGeneration int64 = 9007199254740991

var portableMinimumVersion = regexp.MustCompile(
	`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`,
)

// validPortableMetadata validates the public schema without loading external documents or data.
func validPortableMetadata(descriptor portableDescriptor) bool {
	if !validProductName(descriptor.Name) || !validPublicLabel(descriptor.Namespace) ||
		descriptor.ID == "" || descriptor.DisplayName == "" || descriptor.Description == "" || descriptor.Version == "" ||
		!validPublicOwner(descriptor.Owner) || len(descriptor.Outputs) == 0 ||
		!validGeneration(
			descriptor.Generation,
		) || !validGeneration(descriptor.ObservedGeneration) ||
		(descriptor.DocumentationURL != "" && !validPublicHTTPS(descriptor.DocumentationURL)) {
		return false
	}
	for _, dimension := range descriptor.Health {
		if !validGeneration(dimension.Generation) ||
			!validGeneration(dimension.ObservedGeneration) {
			return false
		}
	}
	for _, output := range descriptor.Outputs {
		if !validPublicOutput(output) {
			return false
		}
	}
	for _, input := range descriptor.Inputs {
		if !validPublicLabel(input.Name) || !validPublicReference(input.ProductRef) {
			return false
		}
		if input.Contract != nil &&
			(len(input.Contract.MinimumVersion) > 64 || !portableMinimumVersion.MatchString(input.Contract.MinimumVersion) || !validPublicProtocol(input.Contract.Protocol)) {
			return false
		}
	}
	for _, edge := range descriptor.Lineage {
		if !validPublicLabel(edge.Name) || !validPublicReference(edge.ProductRef) ||
			!validGeneration(edge.ObservedGeneration) ||
			(edge.Owner != nil && !validPublicOwner(*edge.Owner)) ||
			(edge.Output != nil && !validPublicOutput(*edge.Output)) {
			return false
		}
	}
	return descriptor.UI == nil || validPublicUI(*descriptor.UI)
}

// validGeneration bounds integers to values independent JavaScript clients can compare exactly.
func validGeneration(value int64) bool { return value >= 0 && value <= maxPortableGeneration }

// validPublicLabel enforces a single DNS label for namespaces and port names.
func validPublicLabel(value string) bool { return len(validation.IsDNS1123Label(value)) == 0 }

// validProductName permits product names with multiple canonical DNS labels.
func validProductName(value string) bool {
	if len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if !validPublicLabel(label) {
			return false
		}
	}
	return true
}

// validPublicReference validates identity without reading a producer or widening namespace permissions.
func validPublicReference(reference datav1alpha1.ProductReference) bool {
	return validProductName(reference.Name) && validPublicLabel(reference.Output) &&
		(reference.Namespace == "" || validPublicLabel(reference.Namespace))
}

// validPublicOwner requires a public identity and validates optional support links.
func validPublicOwner(owner datav1alpha1.ProductOwner) bool {
	return owner.Name != "" && (owner.URL == "" || validPublicHTTPS(owner.URL))
}

// validPublicOutput checks declared interface metadata without contacting its endpoint.
func validPublicOutput(output datav1alpha1.OutputPort) bool {
	return validPublicLabel(output.Name) && validPublicProtocol(output.Protocol) &&
		validPublicHTTPS(output.URL) &&
		validPublicHTTPS(output.ContractURL)
}

// validPublicProtocol keeps the portable vocabulary aligned with the published API enum.
func validPublicProtocol(protocol datav1alpha1.OutputProtocol) bool {
	switch protocol {
	case datav1alpha1.ProtocolOpenAPI,
		datav1alpha1.ProtocolAsyncAPI,
		datav1alpha1.ProtocolGraphQL,
		datav1alpha1.ProtocolDCAT,
		datav1alpha1.ProtocolArrowFlight:
		return true
	}
	return false
}

// validPublicHTTPS rejects credentials and URL forms independent consumers cannot safely interpret.
func validPublicHTTPS(value string) bool {
	if !strings.HasPrefix(value, "https://") || strings.ContainsAny(value, "\\#") ||
		strings.IndexFunc(value, func(char rune) bool {
			return char > unicode.MaxASCII || unicode.IsControl(char) || unicode.IsSpace(char)
		}) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" ||
		parsed.User != nil ||
		parsed.Opaque != "" {
		return false
	}
	address, addressError := netip.ParseAddr(parsed.Hostname())
	if addressError == nil && address.Zone() != "" {
		return false
	}
	if addressError != nil && !validProductName(parsed.Hostname()) {
		return false
	}
	if addressError != nil {
		labels := strings.Split(parsed.Hostname(), ".")
		last := labels[len(labels)-1]
		// Browser URL parsers treat numeric final labels as IPv4 candidates, not DNS names.
		if strings.Trim(last, "0123456789") == "" || strings.HasPrefix(last, "0x") {
			return false
		}
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return false
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return false
		}
	}
	return true
}

// validPublicUI retains publisher origin and protocol limits without granting host capabilities.
func validPublicUI(ui datav1alpha1.ProductUI) bool {
	title := strings.TrimFunc(ui.Title, func(char rune) bool {
		return unicode.IsSpace(char) || char == '\ufeff'
	})
	if title == "" ||
		len(utf16.Encode([]rune(ui.Title))) > 200 ||
		len(ui.URL) > 2048 ||
		!validPublicHTTPS(ui.URL) {
		return false
	}
	manifest, err := json.Marshal(ui)
	if err != nil || len(manifest) > 16<<10 {
		return false
	}
	if ui.Contract == nil {
		return true
	}
	contract := ui.Contract
	if (contract.APIVersion != "data-product-ui/v1" && contract.APIVersion != "data-product-ui/v2") ||
		len(contract.HostOrigins) == 0 ||
		len(contract.HostOrigins) > 16 ||
		contract.Capabilities == nil ||
		len(contract.Capabilities) > 3 {
		return false
	}
	origins := make([]string, 0, len(contract.HostOrigins))
	for _, origin := range contract.HostOrigins {
		origins = append(origins, string(origin))
	}
	if _, err := config.UIHostOrigins(strings.Join(origins, ",")); err != nil {
		return false
	}
	seen := make(map[datav1alpha1.UICapability]bool)
	for _, capability := range contract.Capabilities {
		if seen[capability] ||
			(capability != "status" && capability != "resize" && (capability != "appearance" || contract.APIVersion != "data-product-ui/v2")) {
			return false
		}
		seen[capability] = true
	}
	return true
}
