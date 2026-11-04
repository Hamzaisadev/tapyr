package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
)

// Package is a provider package: one Provider, its job templates and an
// optional simulator definition (see docs/spec/03-configuration.md,
// "Provider packages").
type Package struct {
	// Origin describes where the package comes from, for messages: "the
	// built-in package" or the package directory.
	Origin string
	// FS is the package directory (fixtures are read from it).
	FS fs.FS
	// Provider is the content of provider.yaml.
	Provider *Provider
	// JobTemplates are the documents of templates/*.yaml.
	JobTemplates []*JobTemplate
	// Simulation is the content of sim/sim.yaml, or nil.
	Simulation *Simulation
	// Scenarios of the simulator, by name.
	Scenarios map[string]*Scenario
}

// Package file layout.
const (
	PackageProviderFile   = "provider.yaml"
	PackageTemplatesGlob  = "templates/*.yaml"
	PackageSimulationFile = "sim/sim.yaml"
	PackageScenariosGlob  = "sim/scenarios/*.yaml"
)

// LoadPackage loads the provider package rooted at fsys. dir is the name of
// the package directory, which must match the provider name; display
// prefixes file names in diagnostics; origin describes the package in
// messages. Errors are returned as Diagnostics.
func (l *Loader) LoadPackage(fsys fs.FS, dir, display, origin string) (*Package, error) {
	pkg := &Package{Origin: origin, FS: fsys}
	var diags Diagnostics
	read := func(name string, allowed Kind) ([]envelope, bool) {
		file := path.Join(display, name)
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, false
			}
			diags = append(diags, Diagnostic{Pos: Position{File: file}, Message: fileError(err)})
			return nil, true
		}
		docs, d := l.parseDocuments(file, data, map[Kind]bool{allowed: true})
		diags = append(diags, d...)
		return docs, true
	}

	docs, found := read(PackageProviderFile, KindProvider)
	switch {
	case !found:
		return nil, Diagnostics{{Pos: Position{File: display}, Message: "not a provider package: missing " + PackageProviderFile}}
	case len(docs) == 1:
		pkg.Provider = docs[0].(*Provider)
		if dir != "" && pkg.Provider.name() != dir {
			diags = append(diags, pkg.Provider.Source.Diagnostic(
				fmt.Sprintf("provider name %q must match the package directory %q", pkg.Provider.name(), dir), "metadata", "name"))
		}
	case len(docs) > 1:
		diags = append(diags, docs[1].source().Diagnostic(PackageProviderFile+" must contain exactly one Provider document"))
	case len(diags) == 0:
		diags = append(diags, Diagnostic{Pos: Position{File: path.Join(display, PackageProviderFile)}, Message: "missing Provider document"})
	}

	templates, err := fs.Glob(fsys, PackageTemplatesGlob)
	if err != nil {
		return nil, err
	}
	slices.Sort(templates)
	for _, name := range templates {
		docs, _ := read(name, KindJobTemplate)
		for _, d := range docs {
			t := d.(*JobTemplate)
			if pkg.Provider != nil && t.Spec.Provider != pkg.Provider.Metadata.Name {
				diags = append(diags, t.Source.Diagnostic(
					fmt.Sprintf("job template of package %q must use provider %q", pkg.Provider.Metadata.Name, pkg.Provider.Metadata.Name), "spec", "provider"))
			}
			pkg.JobTemplates = append(pkg.JobTemplates, t)
		}
	}

	if docs, found := read(PackageSimulationFile, KindSimulation); found {
		switch len(docs) {
		case 1:
			pkg.Simulation = docs[0].(*Simulation)
		case 0:
		default:
			diags = append(diags, docs[1].source().Diagnostic(PackageSimulationFile+" must contain exactly one Simulation document"))
		}
	}
	scenarios, err := fs.Glob(fsys, PackageScenariosGlob)
	if err != nil {
		return nil, err
	}
	slices.Sort(scenarios)
	pkg.Scenarios = map[string]*Scenario{}
	for _, name := range scenarios {
		docs, _ := read(name, KindScenario)
		for _, d := range docs {
			sc := d.(*Scenario)
			if prev, dup := pkg.Scenarios[sc.name()]; dup {
				diags = append(diags, sc.Source.Diagnostic(fmt.Sprintf("duplicate scenario name %q (first defined at %s)",
					sc.name(), prev.Source.Position("metadata", "name")), "metadata", "name"))
				continue
			}
			pkg.Scenarios[sc.name()] = sc
		}
	}
	if len(diags) > 0 {
		return nil, diags.Sorted()
	}
	return pkg, nil
}
