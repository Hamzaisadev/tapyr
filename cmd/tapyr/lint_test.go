package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// examplePackage copies the example-sim package into a temporary
// directory, to be modified by a test.
func examplePackage(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "example-sim")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "providers", "example-sim"))); err != nil {
		t.Fatal(err)
	}
	return dir
}

func edit(t *testing.T, path, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, replacement, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProviderLint(t *testing.T) {
	// Every built-in package passes.
	res := execute(t, nil, "provider", "lint", filepath.Join("..", "..", "providers"))
	if res.code != 0 || !strings.Contains(res.stdout, "example-sim: ok") {
		t.Fatalf("built-in packages: exit %d\n%s%s", res.code, res.stdout, res.stderr)
	}

	tests := []struct {
		name   string
		change func(t *testing.T, dir string)
		want   []string
	}{
		{
			name: "changed samples",
			change: func(t *testing.T, dir string) {
				edit(t, filepath.Join(dir, "tests", "expected.prom"), `example_devices 5`, `example_devices 4`)
			},
			want: []string{"the samples differ from tests/expected.prom", "- example_devices 4", "+ example_devices 5"},
		},
		{
			name: "unknown metric in a dashboard",
			change: func(t *testing.T, dir string) {
				edit(t, filepath.Join(dir, "dashboards", "example-sim.json"), `sum(example_devices)`, `sum(example_devices_missing)`)
			},
			want: []string{"dashboards/example-sim.json: example_devices_missing is not a metric of the package"},
		},
		{
			name: "no datasource variable",
			change: func(t *testing.T, dir string) {
				edit(t, filepath.Join(dir, "dashboards", "example-sim.json"), `"type": "datasource"`, `"type": "custom"`)
			},
			want: []string{"no datasource variable"},
		},
		{
			name:   "no dashboard",
			change: func(_ *testing.T, dir string) { _ = os.RemoveAll(filepath.Join(dir, "dashboards")) },
			want:   []string{"no dashboard (dashboards/*.json)"},
		},
		{
			name:   "no example",
			change: func(_ *testing.T, dir string) { _ = os.RemoveAll(filepath.Join(dir, "examples")) },
			want:   []string{"no example (examples/*.yaml)"},
		},
		{
			name: "invalid example",
			change: func(t *testing.T, dir string) {
				edit(t, filepath.Join(dir, "examples", "example-sim.yaml"), "template: example-sim-devices", "template: nope")
			},
			want: []string{"examples/example-sim.yaml:"},
		},
		{
			name:   "missing scenario",
			change: func(_ *testing.T, dir string) { _ = os.Remove(filepath.Join(dir, "sim", "scenarios", "bad-day.yaml")) },
			want:   []string{"no bad-day scenario (sim/scenarios/bad-day.yaml)"},
		},
		{
			name: "invalid scenario",
			change: func(t *testing.T, dir string) {
				edit(t, filepath.Join(dir, "sim", "scenarios", "inventory-change.yaml"), "/sites/{siteId}/networks", "/nope")
			},
			want: []string{"scenario inventory-change: "},
		},
		{
			name:   "no contract test",
			change: func(_ *testing.T, dir string) { _ = os.RemoveAll(filepath.Join(dir, "tests")) },
			want:   []string{"no contract test (tests/contract.yaml)"},
		},
		{
			name: "template not exercised",
			change: func(t *testing.T, dir string) {
				edit(t, filepath.Join(dir, "tests", "contract.yaml"), "template: example-sim-devices", "template: example-sim-sites")
			},
			want: []string{"template example-sim-devices is not exercised by tests/contract.yaml"},
		},
		{
			name: "wrong credentials",
			change: func(t *testing.T, dir string) {
				edit(t, filepath.Join(dir, "tests", "contract.env"), "=sim-api-key", "=wrong-key")
			},
			want: []string{"devices: ", "401"},
		},
		{
			name: "invalid contract",
			change: func(t *testing.T, dir string) {
				edit(t, filepath.Join(dir, "tests", "contract.yaml"), "template: example-sim-devices", "template: nope")
			},
			want: []string{"tests/contract.yaml:"},
		},
		{
			name: "invalid template",
			change: func(t *testing.T, dir string) {
				edit(t, filepath.Join(dir, "templates", "devices.yaml"), `value: item.clients`, `value: item.clients +`)
			},
			want: []string{"templates/devices.yaml:"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := examplePackage(t)
			tt.change(t, dir)
			res := execute(t, nil, "provider", "lint", dir)
			if res.code != exitInvalidConfig {
				t.Errorf("exit %d, want %d", res.code, exitInvalidConfig)
			}
			for _, w := range tt.want {
				if !strings.Contains(res.stderr, w) {
					t.Errorf("output does not contain %q:\n%s", w, res.stderr)
				}
			}
		})
	}

	t.Run("update", func(t *testing.T) {
		dir := examplePackage(t)
		path := filepath.Join(dir, "tests", "expected.prom")
		want, _ := os.ReadFile(path)
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if res := execute(t, nil, "provider", "lint", "--update", dir); res.code != 0 {
			t.Fatalf("exit %d\n%s", res.code, res.stderr)
		}
		if got, _ := os.ReadFile(path); string(got) != string(want) {
			t.Errorf("--update wrote:\n%s\nwant:\n%s", got, want)
		}
	})
	t.Run("not a package", func(t *testing.T) {
		if res := execute(t, nil, "provider", "lint", t.TempDir()); res.code != exitInvalidConfig || !strings.Contains(res.stderr, "holds no provider package") {
			t.Errorf("exit %d\n%s", res.code, res.stderr)
		}
	})
}
