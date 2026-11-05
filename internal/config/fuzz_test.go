package config

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParseDocuments decodes arbitrary bytes as a configuration file, every
// kind allowed. Whatever the file holds, decoding never panics, and every
// problem is a diagnostic of that file with a message, the way tapyr
// validate reports it.
func FuzzParseDocuments(f *testing.F) {
	l, err := NewLoader()
	if err != nil {
		f.Fatal(err)
	}
	// Real documents: the examples and the built-in provider packages.
	for _, pattern := range []string{
		"../../examples/*/*.yaml", "../../examples/*/*/*.yaml",
		"../../providers/*/*.yaml", "../../providers/*/*/*.yaml", "../../providers/*/sim/scenarios/*.yaml",
	} {
		files, _ := filepath.Glob(pattern)
		for _, file := range files {
			if b, err := os.ReadFile(file); err == nil && len(b) < 64<<10 {
				f.Add(b)
			}
		}
	}
	for _, s := range []string{
		"", "---\n---\n", "a: [", "- 1\n- 2\n", "apiVersion: tapyr/v1alpha1\nkind: Job\n",
		"apiVersion: tapyr/v1alpha1\nkind: Account\nmetadata: {name: a}\nspec: {provider: x, targets: [{name: t}, {name: t}]}\n",
		"&a [*a, *a]\n", "x: !!binary AAAA\n", "kind: Settings\nspec: {collection: {jitter: 200%}}\n",
	} {
		f.Add([]byte(s))
	}
	all := map[Kind]bool{}
	for _, k := range allKinds {
		all[k] = true
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, diags := l.parseDocuments("fuzz.yaml", data, all)
		for _, d := range diags {
			if d.Pos.File != "fuzz.yaml" {
				t.Errorf("diagnostic of another file %q: %s", d.Pos.File, d)
			}
			if d.Message == "" {
				t.Errorf("diagnostic without a message at %s", d.Pos)
			}
		}
	})
}
