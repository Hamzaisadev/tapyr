package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
)

const pkgProvider = `apiVersion: tapyr/v1alpha1
kind: Provider
metadata: { name: acme, version: 1.2.0 }
spec:
  baseURL: https://api.acme.invalid
`

const pkgTemplate = `apiVersion: tapyr/v1alpha1
kind: JobTemplate
metadata: { name: acme-devices }
spec:
  provider: acme
  defaultInterval: 5m
  request: { path: /devices }
`

const pkgSimulation = `apiVersion: tapyr/v1alpha1
kind: Simulation
metadata: { name: acme }
spec:
  basePath: /v1
  auth: { accept: { bearer: [sim-token] } }
  routes:
    - path: /devices
      pagination: offset
      response: { file: fixtures/devices.json }
`

func mapFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, content := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return fsys
}

func TestLoadPackage(t *testing.T) {
	l := newTestLoader(t)
	pkg, err := l.LoadPackage(mapFS(map[string]string{
		"provider.yaml":             pkgProvider,
		"templates/devices.yaml":    pkgTemplate,
		"templates/notes.txt":       "ignored",
		"sim/sim.yaml":              pkgSimulation,
		"sim/fixtures/devices.json": "[]",
	}), "acme", "builtin/acme", "the built-in package")
	if err != nil {
		t.Fatalf("LoadPackage() = %v", err)
	}
	if pkg.Provider.Metadata.Version != "1.2.0" || len(pkg.JobTemplates) != 1 || pkg.Simulation == nil {
		t.Fatalf("package = %+v", pkg)
	}
	if got := pkg.Simulation.Spec.Routes[0]; got.Pagination != PaginationOffset || got.Response.File != "fixtures/devices.json" {
		t.Errorf("route = %+v", got)
	}
	if got := pkg.Provider.Source.Position("metadata", "name").String(); got != "builtin/acme/provider.yaml:3:19" {
		t.Errorf("provider position = %s", got)
	}

	noSim, err := l.LoadPackage(mapFS(map[string]string{"provider.yaml": pkgProvider}), "acme", "acme", "acme")
	if err != nil || noSim.Simulation != nil || len(noSim.JobTemplates) != 0 {
		t.Fatalf("package without templates and simulator: %+v, %v", noSim, err)
	}
}

func TestLoadPackageInvalid(t *testing.T) {
	tests := []struct {
		name  string
		dir   string
		files map[string]string
		want  []string
	}{
		{
			name:  "missing provider.yaml",
			dir:   "acme",
			files: map[string]string{"templates/devices.yaml": pkgTemplate},
			want:  []string{"pkg: not a provider package: missing provider.yaml"},
		},
		{
			name:  "directory name mismatch",
			dir:   "other",
			files: map[string]string{"provider.yaml": pkgProvider},
			want:  []string{`pkg/provider.yaml:3:19: metadata.name: provider name "acme" must match the package directory "other"`},
		},
		{
			name:  "two providers",
			dir:   "acme",
			files: map[string]string{"provider.yaml": pkgProvider + "---\n" + pkgProvider},
			want:  []string{"pkg/provider.yaml:7:1: provider.yaml must contain exactly one Provider document"},
		},
		{
			name: "wrong kinds",
			dir:  "acme",
			files: map[string]string{
				"provider.yaml":          pkgProvider + "---\n" + pkgTemplate,
				"templates/devices.yaml": pkgProvider,
			},
			want: []string{
				"pkg/provider.yaml:8:7: kind: kind JobTemplate is not allowed here (want Provider)",
				"pkg/templates/devices.yaml:2:7: kind: kind Provider is not allowed here (want JobTemplate)",
			},
		},
		{
			name: "template of another provider",
			dir:  "acme",
			files: map[string]string{
				"provider.yaml":          pkgProvider,
				"templates/devices.yaml": strings.Replace(pkgTemplate, "provider: acme", "provider: other", 1),
			},
			want: []string{`pkg/templates/devices.yaml:5:13: spec.provider: job template of package "acme" must use provider "acme"`},
		},
		{
			name: "invalid simulation",
			dir:  "acme",
			files: map[string]string{
				"provider.yaml": pkgProvider,
				"sim/sim.yaml":  "apiVersion: tapyr/v1alpha1\nkind: Simulation\nmetadata: { name: acme }\nspec: { routes: [] }\n",
			},
			want: []string{"pkg/sim/sim.yaml:4:9: spec.routes: must have at least 1 item (got 0)"},
		},
	}
	l := newTestLoader(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := l.LoadPackage(mapFS(tt.files), tt.dir, "pkg", "test")
			var diags Diagnostics
			if !errors.As(err, &diags) {
				t.Fatalf("LoadPackage() error = %v, want Diagnostics", err)
			}
			if diff := cmp.Diff(strings.Join(tt.want, "\n"), diags.Error()); diff != "" {
				t.Errorf("diagnostics (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSimulationRejectedInUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.yaml")
	write(t, path, pkgSimulation)
	_, err := newTestLoader(t).Load([]string{path})
	if err == nil || !strings.Contains(err.Error(), "kind Simulation is only valid inside a provider package") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestAssemblePrecedence(t *testing.T) {
	l := newTestLoader(t)
	load := func(t *testing.T, display, version, template string) *Package {
		t.Helper()
		files := map[string]string{"provider.yaml": strings.Replace(pkgProvider, "1.2.0", version, 1)}
		if template != "" {
			files["templates/t.yaml"] = strings.Replace(pkgTemplate, "acme-devices", template, 1)
		}
		pkg, err := l.LoadPackage(mapFS(files), "acme", display, display)
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	}
	account := filepath.Join(t.TempDir(), "account.yaml")
	write(t, account, "apiVersion: tapyr/v1alpha1\nkind: Account\nmetadata: { name: a }\nspec: { provider: acme }\n")

	t.Run("first package wins", func(t *testing.T) {
		cfg, err := l.Parse([]string{account})
		if err != nil {
			t.Fatal(err)
		}
		cfg, err = Assemble(cfg, []*Package{load(t, "user", "2.0.0", "user-devices"), load(t, "builtin", "1.0.0", "builtin-devices")})
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Providers) != 1 || cfg.Providers[0].Metadata.Version != "2.0.0" {
			t.Fatalf("providers = %+v", cfg.Providers)
		}
		if len(cfg.JobTemplates) != 1 || cfg.JobTemplates[0].Metadata.Name != "user-devices" {
			t.Errorf("templates of the overridden package must be dropped: %+v", cfg.JobTemplates)
		}
		if got := cfg.Warnings.Error(); got != `builtin/provider.yaml:3:19: metadata.name: provider "acme" from builtin is overridden by user` {
			t.Errorf("warnings = %q", got)
		}
	})

	t.Run("inline provider wins over packages", func(t *testing.T) {
		inline := filepath.Join(filepath.Dir(account), "provider.yaml")
		write(t, inline, strings.Replace(pkgProvider, "1.2.0", "9.9.9", 1))
		cfg, err := l.Parse([]string{account, inline})
		if err != nil {
			t.Fatal(err)
		}
		cfg, err = Assemble(cfg, []*Package{load(t, "builtin", "1.0.0", "builtin-devices")})
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Providers) != 1 || cfg.Providers[0].Metadata.Version != "9.9.9" || len(cfg.JobTemplates) != 0 {
			t.Fatalf("providers = %+v, templates = %+v", cfg.Providers, cfg.JobTemplates)
		}
		if !strings.Contains(cfg.Warnings.Error(), "is overridden by the configuration ("+inline+":3:19)") {
			t.Errorf("warnings = %q", cfg.Warnings.Error())
		}
	})

	t.Run("references resolve against packages", func(t *testing.T) {
		cfg, err := l.Parse([]string{account})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Assemble(cfg, nil); err == nil || !strings.Contains(err.Error(), `unknown provider "acme"`) {
			t.Fatalf("Assemble() without packages = %v", err)
		}
	})
}
