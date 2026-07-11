// Package meta is the module root. It embeds the authoritative JSON Schema
// files so the validation tooling is self-contained while the files under
// schema/ remain the public contract.
package meta

import "embed"

// SchemaFS holds the JSON Schema files (schema/*.json) that define every
// entity's on-disk shape. metacheck validates against these embedded copies.
//
//go:embed schema/*.json
var SchemaFS embed.FS
