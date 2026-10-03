// Package crd embeds the exact generated installation contract for offline publication checks.
package crd

import _ "embed"

//go:embed bases/data.devantler.tech_dataproducts.yaml
var dataProductSchema string

// DataProductSchema returns a private copy of the generated CRD.
func DataProductSchema() []byte { return []byte(dataProductSchema) }
