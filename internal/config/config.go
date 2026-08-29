// Package config reads process configuration with fail-closed defaults.
package config

import (
	"fmt"
	"strconv"
)

// RegistryUIEnabled parses the registry UI release flag.
func RegistryUIEnabled(value string) (bool, error) {
	if value == "" {
		return false, nil
	}

	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse REGISTRY_UI_ENABLED: %w", err)
	}

	return enabled, nil
}
