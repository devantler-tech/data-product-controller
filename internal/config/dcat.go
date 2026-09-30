package config

// DCATCatalogEnabled parses the default-off catalog publication release flag.
func DCATCatalogEnabled(value string) (bool, error) {
	return featureEnabled("DCAT_CATALOG_ENABLED", value)
}
