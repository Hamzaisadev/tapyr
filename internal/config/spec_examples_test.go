package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specExamples extracts the ```yaml blocks of a Markdown file.
func specExamples(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var blocks []string
	var cur *strings.Builder
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case cur == nil && strings.TrimSpace(line) == "```yaml":
			cur = &strings.Builder{}
		case cur != nil && strings.TrimSpace(line) == "```":
			blocks = append(blocks, cur.String())
			cur = nil
		case cur != nil:
			cur.WriteString(line + "\n")
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return blocks
}

// TestSpecExamples checks every YAML example of the
// configuration specification is valid, completed only by minimal stubs for
// the documents the examples reference but do not define.
func TestSpecExamples(t *testing.T) {
	dir := t.TempDir()
	blocks := specExamples(t, filepath.Join("..", "..", "docs", "spec", "03-configuration.md"))
	if len(blocks) < 8 {
		t.Fatalf("found %d YAML examples in 03-configuration.md, want at least 8", len(blocks))
	}
	for i, b := range blocks {
		name := filepath.Join(dir, fmt.Sprintf("example-%02d.yaml", i+1))
		if err := os.WriteFile(name, []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l := newTestLoader(t)
	cfg, err := l.Load([]string{dir, filepath.Join("testdata", "spec-stubs")})
	if err != nil {
		t.Fatalf("spec examples are invalid:\n%v", err)
	}
	counts := map[string]int{
		"providers": len(cfg.Providers), "templates": len(cfg.JobTemplates), "accounts": len(cfg.Accounts),
		"jobs": len(cfg.Jobs), "outputs": len(cfg.Outputs),
	}
	want := map[string]int{"providers": 3, "templates": 2, "accounts": 3, "jobs": 2, "outputs": 3}
	for k, n := range want {
		if counts[k] != n {
			t.Errorf("%s = %d, want %d", k, counts[k], n)
		}
	}
	if cfg.Settings == nil || cfg.Settings.Metadata.Name != "default" {
		t.Errorf("settings not loaded: %+v", cfg.Settings)
	}
}

func newTestLoader(t *testing.T) *Loader {
	t.Helper()
	l, err := NewLoader()
	if err != nil {
		t.Fatal(err)
	}
	return l
}
