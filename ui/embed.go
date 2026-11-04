// Package ui embeds the built web UI (dist/app, from make ui). Only
// dist/README.md is committed: without a build, the binary serves a page
// explaining how to build the UI.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the dist/ directory: the built UI is under app/.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded: it always exists
	}
	return sub
}
