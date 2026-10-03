// Package config reads process configuration with fail-closed defaults.
package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ProvisionedSourcesEnabled parses the provisioner observation release flag.
func ProvisionedSourcesEnabled(value string) (bool, error) {
	return featureEnabled("PROVISIONED_SOURCES_ENABLED", value)
}

// EngineProvidersEnabled parses the default-off typed engine observation release flag.
func EngineProvidersEnabled(value string) (bool, error) {
	return featureEnabled("ENGINE_PROVIDERS_ENABLED", value)
}

// ConnectorReadinessEnabled parses the default-off workload observation release flag.
func ConnectorReadinessEnabled(value string) (bool, error) {
	return featureEnabled("CONNECTOR_READINESS_ENABLED", value)
}

// ContractReadinessEnabled parses the default-off contract observation and execution flag.
func ContractReadinessEnabled(value string) (bool, error) {
	return featureEnabled("CONTRACT_READINESS_ENABLED", value)
}

// CompositionEnabled parses the default-off graph and declared compatibility release flag.
func CompositionEnabled(value string) (bool, error) {
	return featureEnabled("COMPOSITION_ENABLED", value)
}

// UIContractEnabled parses the default-off portable UI host release flag.
func UIContractEnabled(value string) (bool, error) {
	return featureEnabled("UI_CONTRACT_ENABLED", value)
}

// UIAppearanceEnabled parses the default-off v2 presentation grant gate.
func UIAppearanceEnabled(value string) (bool, error) {
	return featureEnabled("UI_APPEARANCE_ENABLED", value)
}

// RegistryDiscoveryEnabled parses the default-off portable inventory and descriptor release gate.
func RegistryDiscoveryEnabled(value string) (bool, error) {
	return featureEnabled("REGISTRY_DISCOVERY_ENABLED", value)
}

// UIHostOrigins validates publisher-owned HTTPS origins without accepting wildcards or URL components.
func UIHostOrigins(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	origins := strings.Split(value, ",")
	if len(origins) > 16 {
		return nil, fmt.Errorf("UI_HOST_ORIGINS permits at most 16 origins")
	}
	seen := make(map[string]bool)
	for _, origin := range origins {
		parsed, err := url.Parse(origin)
		if err != nil || len(origin) > 253 || strings.ContainsAny(origin, "\\ \t\r\n*#?") ||
			parsed.Scheme != "https" ||
			parsed.User != nil ||
			parsed.Host == "" ||
			parsed.Path != "" ||
			parsed.RawQuery != "" ||
			parsed.Fragment != "" ||
			parsed.Opaque != "" ||
			origin != "https://"+parsed.Host ||
			seen[origin] {
			return nil, fmt.Errorf("UI_HOST_ORIGINS requires distinct exact HTTPS origins")
		}
		host := parsed.Hostname()
		if !validUIHostname(host) {
			return nil, fmt.Errorf(
				"UI_HOST_ORIGINS requires lowercase DNS names or canonical IPv4 addresses",
			)
		}
		if strings.Contains(parsed.Host, ":") {
			port, portErr := strconv.Atoi(parsed.Port())
			if portErr != nil || port < 1 || port > 65535 || port == 443 ||
				strconv.Itoa(port) != parsed.Port() {
				return nil, fmt.Errorf(
					"UI_HOST_ORIGINS requires canonical non-default ports from 1 to 65535",
				)
			}
		}
		seen[origin] = true
	}
	return origins, nil
}

// validUIHostname keeps deployment configuration within the manifest's DNS/IPv4 origin subset.
func validUIHostname(host string) bool {
	if host == "" || host != strings.ToLower(host) || strings.HasSuffix(host, ".") {
		return false
	}
	labels := strings.Split(host, ".")
	last := labels[len(labels)-1]
	if strings.Trim(last, "0123456789") == "" || strings.HasPrefix(last, "0x") {
		address, err := netip.ParseAddr(host)
		return err == nil && address.Is4() && address.String() == host
	}
	labelPattern := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	for _, label := range labels {
		if !labelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

// featureEnabled defaults an unset flag to false and names invalid settings in parsing errors.
func featureEnabled(name, value string) (bool, error) {
	if value == "" {
		return false, nil
	}

	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", name, err)
	}

	return enabled, nil
}
