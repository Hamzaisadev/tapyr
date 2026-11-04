// Package plugins builds the registry of the built-in auth plugins.
package plugins

import (
	"github.com/kentrow/tapyr/internal/auth"
	"github.com/kentrow/tapyr/internal/auth/plugins/arubacentral"
)

// Builtin returns a new registry of the built-in auth plugins.
func Builtin() auth.Plugins {
	return auth.NewPlugins(arubacentral.New())
}
