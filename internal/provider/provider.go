// Package provider loads provider packages: the built-in packages embedded
// in the binary and the user packages found in providerPaths. It also
// assembles a configuration with its packages.
package provider

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/providers"
)

// BuiltinOrigin describes built-in packages in messages.
const BuiltinOrigin = "the built-in package"

// Builtin loads the packages embedded in the binary, sorted by name.
func Builtin(l *config.Loader) ([]*config.Package, error) {
	return builtinFrom(l, providers.FS)
}

func builtinFrom(l *config.Loader, fsys fs.FS) ([]*config.Package, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("reading built-in packages: %w", err)
	}
	var pkgs []*config.Package
	var diags config.Diagnostics
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.Sub(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		pkg, err := l.LoadPackage(sub, e.Name(), path.Join("builtin", e.Name()), BuiltinOrigin)
		if err != nil {
			diags = appendError(diags, config.Diagnostic{Pos: config.Position{File: path.Join("builtin", e.Name())}}, err)
			continue
		}
		pkgs = append(pkgs, pkg)
	}
	if len(diags) > 0 {
		return nil, diags.Sorted()
	}
	return pkgs, nil
}

// FromPath loads the packages of a providerPaths entry: dir is either a
// package (it contains provider.yaml) or a directory of packages.
func FromPath(l *config.Loader, dir string) ([]*config.Package, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("provider path %s: %w", dir, unwrapPathError(err))
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("provider path %s: not a directory", dir)
	}
	if isPackage(dir) {
		pkg, err := loadDir(l, dir)
		if err != nil {
			return nil, err
		}
		return []*config.Package{pkg}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("provider path %s: %w", dir, unwrapPathError(err))
	}
	var pkgs []*config.Package
	var diags config.Diagnostics
	for _, e := range entries {
		sub := filepath.Join(dir, e.Name())
		if !e.IsDir() || !isPackage(sub) {
			continue
		}
		pkg, err := loadDir(l, sub)
		if err != nil {
			diags = appendError(diags, config.Diagnostic{Pos: config.Position{File: sub}}, err)
			continue
		}
		pkgs = append(pkgs, pkg)
	}
	if len(diags) > 0 {
		return nil, diags.Sorted()
	}
	return pkgs, nil
}

func isPackage(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, config.PackageProviderFile))
	return err == nil
}

func loadDir(l *config.Loader, dir string) (*config.Package, error) {
	return l.LoadPackage(os.DirFS(dir), filepath.Base(dir), dir, "the package in "+dir)
}

// LoadConfig parses the configuration paths, loads the packages of its
// providerPaths and the built-in packages, and assembles everything.
// builtin is usually the result of Builtin.
func LoadConfig(l *config.Loader, paths []string, builtin []*config.Package) (*config.Config, error) {
	cfg, err := l.Parse(paths)
	if err != nil {
		return nil, err
	}
	var pkgs []*config.Package
	var diags config.Diagnostics
	if s := cfg.Settings; s != nil {
		for i, dir := range s.Spec.ProviderPaths {
			found, err := FromPath(l, dir)
			if err != nil {
				diags = appendError(diags, s.Source.Diagnostic("", "spec", "providerPaths", fmt.Sprint(i)), err)
				continue
			}
			pkgs = append(pkgs, found...)
		}
	}
	if len(diags) > 0 {
		return nil, diags.Sorted()
	}
	return config.Assemble(cfg, append(pkgs, builtin...))
}

// Find returns the package named name, or nil.
func Find(pkgs []*config.Package, name string) *config.Package {
	i := slices.IndexFunc(pkgs, func(p *config.Package) bool { return string(p.Provider.Metadata.Name) == name })
	if i < 0 {
		return nil
	}
	return pkgs[i]
}

// appendError appends err to diags: its own diagnostics when it has some,
// otherwise base with the error as message.
func appendError(diags config.Diagnostics, base config.Diagnostic, err error) config.Diagnostics {
	if d, ok := errors.AsType[config.Diagnostics](err); ok {
		return append(diags, d...)
	}
	base.Message = err.Error()
	return append(diags, base)
}

func unwrapPathError(err error) error {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err
	}
	return err
}
