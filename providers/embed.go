// Package providers embeds the built-in provider packages, one directory
// per package (see docs/spec/03-configuration.md, "Provider packages").
package providers

import "embed"

// FS holds the built-in packages. A new package directory must be added to
// the embed pattern below.
//
//go:embed aruba-central-classic example-sim meraki-dashboard teltonika-rms teltonika-rutos
var FS embed.FS
