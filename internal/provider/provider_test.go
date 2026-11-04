package provider

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kentrow/tapyr/internal/config"
)

func newLoader(t *testing.T) *config.Loader {
	t.Helper()
	l, err := config.NewLoader()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestBuiltin(t *testing.T) {
	pkgs, err := Builtin(newLoader(t))
	if err != nil {
		t.Fatalf("built-in packages are invalid:\n%v", err)
	}
	pkg := Find(pkgs, "example-sim")
	if pkg == nil {
		t.Fatal("example-sim is not built in")
	}
	if pkg.Origin != BuiltinOrigin || pkg.Simulation == nil || len(pkg.JobTemplates) != 4 {
		t.Errorf("example-sim = origin %q, simulation %v, %d templates", pkg.Origin, pkg.Simulation != nil, len(pkg.JobTemplates))
	}
	if Find(pkgs, "nope") != nil {
		t.Error("Find returned a package for an unknown name")
	}
}

func TestBuiltinReportsBrokenPackages(t *testing.T) {
	fsys := fstest.MapFS{
		"broken/provider.yaml": &fstest.MapFile{Data: []byte("kind: [")},
		"README.md":            &fstest.MapFile{Data: []byte("not a package")},
	}
	_, err := builtinFrom(newLoader(t), fsys)
	if err == nil || !strings.Contains(err.Error(), "builtin/broken/provider.yaml:1:7: invalid YAML") {
		t.Fatalf("builtinFrom() error = %v", err)
	}
}

const providerDoc = `apiVersion: tapyr/v1alpha1
kind: Provider
metadata: { name: %s, version: 0.1.0 }
spec: { baseURL: https://api.example.invalid }
`

func writePackage(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o750); err != nil {
		t.Fatal(err)
	}
	doc := strings.Replace(providerDoc, "%s", name, 1)
	if err := os.WriteFile(filepath.Join(dir, name, "provider.yaml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFromPath(t *testing.T) {
	l := newLoader(t)
	root := t.TempDir()
	writePackage(t, root, "alpha")
	writePackage(t, root, "beta")
	if err := os.MkdirAll(filepath.Join(root, "not-a-package"), 0o750); err != nil {
		t.Fatal(err)
	}

	pkgs, err := FromPath(l, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 || Find(pkgs, "alpha") == nil || Find(pkgs, "beta") == nil {
		t.Fatalf("directory of packages: %d packages", len(pkgs))
	}
	if got := Find(pkgs, "alpha").Origin; got != "the package in "+filepath.Join(root, "alpha") {
		t.Errorf("origin = %q", got)
	}

	single, err := FromPath(l, filepath.Join(root, "alpha"))
	if err != nil || len(single) != 1 {
		t.Fatalf("package directory: %v, %v", single, err)
	}

	if _, err := FromPath(l, filepath.Join(root, "missing")); err == nil || !strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("missing path error = %v", err)
	}
}

func TestLoadConfig(t *testing.T) {
	l := newLoader(t)
	builtin, err := Builtin(l)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	user := filepath.Join(dir, "providers")
	writePackage(t, user, "example-sim")
	cfgFile := filepath.Join(dir, "config.yaml")
	write := func(content string) {
		if err := os.WriteFile(cfgFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(`apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: demo }
spec:
  provider: example-sim
  secrets: { apiKey: { env: KEY } }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: devices }
spec: { account: demo, template: example-sim-devices }
`)
	cfg, err := LoadConfig(l, []string{cfgFile}, builtin)
	if err != nil {
		t.Fatalf("built-in references: %v", err)
	}
	if len(cfg.Providers) != len(builtin) || len(cfg.Warnings) != 0 || Find(builtin, "example-sim") == nil {
		t.Fatalf("providers %d, templates %d, warnings %v", len(cfg.Providers), len(cfg.JobTemplates), cfg.Warnings)
	}

	// A user package overrides the built-in one, templates included.
	write(`apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: default }
spec: { providerPaths: [providers, missing] }
`)
	_, err = LoadConfig(l, []string{cfgFile}, builtin)
	if err == nil || !strings.Contains(err.Error(), "spec.providerPaths[1]: provider path "+filepath.Join(dir, "missing")+": no such file or directory") {
		t.Fatalf("missing provider path error = %v", err)
	}
	write(`apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: default }
spec: { providerPaths: [providers] }
`)
	cfg, err = LoadConfig(l, []string{cfgFile}, builtin)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(cfg.JobTemplates, func(jt *config.JobTemplate) bool { return jt.Spec.Provider == "example-sim" }) || len(cfg.Warnings) != 1 ||
		!strings.Contains(cfg.Warnings[0].Message, `provider "example-sim" from the built-in package is overridden by the package in `) {
		t.Fatalf("templates %d, warnings %v", len(cfg.JobTemplates), cfg.Warnings)
	}
}
