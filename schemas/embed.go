// Package schemas embeds the JSON Schemas generated from the configuration Go
// types (make schema). They are used for validation at runtime and printed by
// tapyr schema.
package schemas

import "embed"

// V1alpha1 holds the tapyr/v1alpha1 schemas under v1alpha1/.
//
//go:embed v1alpha1/*.json
var V1alpha1 embed.FS
