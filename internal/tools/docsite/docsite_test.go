package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// repo writes the files of a test repository and returns its root.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func markdown(files map[string]string) []string {
	var md []string
	for name := range maps.Keys(files) {
		if strings.HasSuffix(name, ".md") {
			md = append(md, name)
		}
	}
	slices.Sort(md)
	return md
}

const siteYML = `repository: https://github.com/example/tapyr
sources: [docs, providers]
nav:
  - Guides:
      - docs/install.md
  - Providers:
      - providers/acme/README.md
unpublished:
  - docs/releasing.md
`

func TestGithubSlug(t *testing.T) {
	tests := []struct{ heading, want string }{
		{"Install", "install"},
		{"Verify the signatures", "verify-the-signatures"},
		{"`remote_write`", "remote_write"},
		{"Out-of-order samples", "out-of-order-samples"},
		{"M0 - Bootstrap", "m0---bootstrap"},
		{"What's new? (v0.1)", "whats-new-v01"},
		{"[Links](https://example.com) and **bold**", "links-and-bold"},
		{"Été", "été"},
	}
	for _, tt := range tests {
		doc := parse("# " + tt.heading + "\n")
		if !doc.anchors[tt.want] {
			t.Errorf("%q: anchors %v, want %q", tt.heading, slices.Collect(maps.Keys(doc.anchors)), tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	src := strings.Join([]string{
		"# Title `x`",
		"See [the guide](guide.md#setup) and ![logo](assets/logo.png).",
		"Code `[not](a-link.md)` stays, [`code text`](code.md) does not.",
		"```yaml",
		"[inside](fence.md)",
		"```",
		"[ref]: <ref target.md>",
		`<img src="assets/picture.svg" alt=""> <a id="custom"></a>`,
		"## Setup",
		"## Setup",
	}, "\n")
	doc := parse(src)
	var dests []string
	for _, l := range doc.links {
		dests = append(dests, l.dest)
		if got := src[l.start:l.end]; got != l.dest {
			t.Errorf("offsets of %q point at %q", l.dest, got)
		}
	}
	slices.Sort(dests)
	want := []string{"assets/logo.png", "assets/picture.svg", "code.md", "guide.md#setup", "ref target.md"}
	if !slices.Equal(dests, want) {
		t.Errorf("links %v, want %v", dests, want)
	}
	if doc.title != "Title x" {
		t.Errorf("title %q", doc.title)
	}
	for _, a := range []string{"title-x", "setup", "setup-1", "custom"} {
		if !doc.anchors[a] {
			t.Errorf("anchor %q missing from %v", a, doc.anchors)
		}
	}
}

func TestCheck(t *testing.T) {
	files := map[string]string{
		"docs/site.yml":            siteYML,
		"README.md":                "# Tapyr\n[install](docs/install.md#binary) [scripts](scripts/) [mail](mailto:a@example.com) [web](https://example.com)\n",
		"docs/install.md":          "# Install\n## Binary\n[same page](#binary) [provider](../providers/acme/README.md#limits)\n",
		"docs/releasing.md":        "# Releasing\n[missing](missing.md) [bad anchor](install.md#nope) [up](../../outside.md) [root](/README.md) [self](#gone)\n",
		"providers/acme/README.md": "# Acme\n## Limits\n```\n[ignored](nowhere.md)\n```\n",
		"providers/AGENTS.md":      "# Agents\n",
		"scripts/check.sh":         "",
	}
	got := check(repo(t, files), markdown(files))
	want := []string{
		"docs/releasing.md:2: #gone: no heading with the anchor #gone",
		"docs/releasing.md:2: ../../outside.md: outside the repository",
		"docs/releasing.md:2: /README.md: use a relative link, it resolves differently on GitHub and on the site",
		"docs/releasing.md:2: install.md#nope: no heading with the anchor #nope",
		"docs/releasing.md:2: missing.md: no such file",
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestManifest(t *testing.T) {
	tests := []struct {
		name  string
		site  string
		extra map[string]string
		want  string
	}{
		{"valid", siteYML, nil, ""},
		{"unlisted page", siteYML, map[string]string{"docs/new.md": "# New\n"}, "docs/new.md: neither in nav nor in unpublished"},
		{"missing page", strings.Replace(siteYML, "docs/install.md", "docs/gone.md", 1), nil, "docs/gone.md: no such file"},
		{"listed twice", siteYML + "  - docs/install.md\n", nil, "docs/install.md: listed twice"},
		{"home page", strings.Replace(siteYML, "docs/install.md", "docs/README.md", 1), map[string]string{"docs/README.md": "# Home\n"}, "is the home page"},
		{"same URL", strings.Replace(siteYML, "      - providers/acme/README.md", "      - providers/acme/README.md\n      - providers/acme.md", 1), map[string]string{"providers/acme.md": "# Acme\n"}, "have the same URL /providers/acme/"},
		{"not markdown", strings.Replace(siteYML, "docs/releasing.md", "docs/site.yml", 1), nil, "docs/site.yml: not a Markdown file"},
		{"bad section", strings.Replace(siteYML, "  - Guides:\n      - docs/install.md", "  - Guides: docs/install.md", 1), nil, `section "Guides": not a list of pages`},
		{"unknown key", siteYML + "extra: true\n", nil, "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{
				"docs/site.yml":            tt.site,
				"docs/install.md":          "# Install\n",
				"docs/releasing.md":        "# Releasing\n",
				"providers/acme/README.md": "# Acme\n",
			}
			maps.Copy(files, tt.extra)
			_, err := loadManifest(repo(t, files))
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("error %v, want %q", err, tt.want)
			}
		})
	}
}

func TestExport(t *testing.T) {
	root := repo(t, map[string]string{
		"docs/site.yml": siteYML,
		"docs/install.md": "# Install\n" +
			"[acme](../providers/acme/README.md#limits) [release](releasing.md) [example](../examples/config.yaml#L3) [scripts](../scripts/)\n" +
			"![overview](assets/overview.svg) [web](https://example.com) [here](#install)\n" +
			"```\n[kept](releasing.md)\n```\n",
		"docs/releasing.md":            "# Releasing\n",
		"docs/assets/overview.svg":     "<svg/>",
		"providers/acme/README.md":     "# Acme provider\n## Limits\n[install](../../docs/install.md) ![dashboard](dashboard.png)\n",
		"providers/acme/dashboard.png": "png",
		"examples/config.yaml":         "",
		"scripts/check.sh":             "",
	})
	out := filepath.Join(t.TempDir(), "site")
	indexFile := filepath.Join(t.TempDir(), "site.json")
	n, err := export(root, "v0.1.1", out, indexFile)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("exported %d pages, want 2", n)
	}
	want := map[string]string{
		"install.md": "# Install\n" +
			"[acme](providers/acme/index.md#limits) [release](https://github.com/example/tapyr/blob/v0.1.1/docs/releasing.md) [example](https://github.com/example/tapyr/blob/v0.1.1/examples/config.yaml#L3) [scripts](https://github.com/example/tapyr/tree/v0.1.1/scripts)\n" +
			"![overview](assets/overview.svg) [web](https://example.com) [here](#install)\n" +
			"```\n[kept](releasing.md)\n```\n",
		"providers/acme/index.md":      "# Acme provider\n## Limits\n[install](../../install.md) ![dashboard](dashboard.png)\n",
		"assets/overview.svg":          "<svg/>",
		"providers/acme/dashboard.png": "png",
	}
	var written []string
	_ = filepath.WalkDir(out, func(file string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(out, file)
			written = append(written, filepath.ToSlash(rel))
		}
		return err
	})
	slices.Sort(written)
	if wantFiles := slices.Sorted(maps.Keys(want)); !slices.Equal(written, wantFiles) {
		t.Errorf("files %v, want %v", written, wantFiles)
	}
	for name, content := range want {
		b, _ := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		if string(b) != content {
			t.Errorf("%s:\n%s\nwant:\n%s", name, b, content)
		}
	}
	b, err := os.ReadFile(indexFile)
	if err != nil {
		t.Fatal(err)
	}
	var idx index
	if err := json.Unmarshal(b, &idx); err != nil {
		t.Fatal(err)
	}
	gotNav, _ := json.Marshal(idx.Nav)
	if wantNav := `[{"Guides":["install.md"]},{"Providers":["providers/acme/index.md"]}]`; string(gotNav) != wantNav {
		t.Errorf("nav %s, want %s", gotNav, wantNav)
	}
	if wantPages := []indexPage{
		{Path: "install.md", Source: "docs/install.md", Title: "Install"},
		{Path: "providers/acme/index.md", Source: "providers/acme/README.md", Title: "Acme provider"},
	}; !slices.Equal(idx.Pages, wantPages) || idx.Ref != "v0.1.1" {
		t.Errorf("index %+v", idx)
	}
	if _, err := export(root, "v0.1.1", out, ""); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("export to a non-empty directory: %v", err)
	}
}
