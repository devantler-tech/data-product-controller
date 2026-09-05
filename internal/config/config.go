// Package config reads process configuration with fail-closed defaults.
package config

import (
	"fmt"
	"strconv"
)

// RegistryUIEnabled parses the registry UI release flag.
func RegistryUIEnabled(value string) (bool, error) {
	return featureEnabled("REGISTRY_UI_ENABLED", value)
}

// ProvisionedSourcesEnabled parses the provisioner observation release flag.
func ProvisionedSourcesEnabled(value string) (bool, error) {
	return featureEnabled("PROVISIONED_SOURCES_ENABLED", value)
}

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
