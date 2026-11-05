package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// manifestPath is the manifest of the site, relative to the repository root.
const manifestPath = "docs/site.yml"

// entry is a page or a section of the navigation.
type entry struct {
	page    string // repository path of a page
	section string // title of a section
	entries []entry
}

type manifest struct {
	repository  string
	nav         []entry
	pages       []string          // published pages, in navigation order
	site        map[string]string // repository path of a published page to its site path
	unpublished map[string]bool
}

// loadManifest reads and validates docs/site.yml.
func loadManifest(root string) (*manifest, error) {
	b, err := os.ReadFile(filepath.Join(root, manifestPath))
	if err != nil {
		return nil, err
	}
	var raw struct {
		Repository  string   `yaml:"repository"`
		Sources     []string `yaml:"sources"`
		Nav         []any    `yaml:"nav"`
		Unpublished []string `yaml:"unpublished"`
	}
	if err := yaml.UnmarshalWithOptions(b, &raw, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	m := &manifest{
		repository:  strings.TrimSuffix(raw.Repository, "/"),
		site:        map[string]string{},
		unpublished: map[string]bool{},
	}
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{manifestPath}, args...)...))
	}
	if !strings.HasPrefix(m.repository, "https://") {
		fail("repository must be an https URL")
	}
	m.nav, err = parseNav(raw.Nav)
	if err != nil {
		fail("nav: %v", err)
	}
	seen := map[string]bool{}
	page := func(p string) bool {
		switch {
		case p != path.Clean(p) || path.IsAbs(p) || strings.HasPrefix(p, "../"):
			fail("%s: not a clean path from the repository root", p)
		case path.Ext(p) != ".md":
			fail("%s: not a Markdown file", p)
		case seen[p]:
			fail("%s: listed twice", p)
		default:
			seen[p] = true
			if _, err := os.Stat(filepath.Join(root, p)); err != nil {
				fail("%s: no such file", p)
			}
			return true
		}
		return false
	}
	urls := map[string]string{}
	walkPages(m.nav, func(p string) {
		if !page(p) {
			return
		}
		s := sitePath(p)
		u := siteURL(s)
		switch {
		case u == "":
			fail("%s: its site path %s is the home page, which the site repository owns", p, s)
		case urls[u] != "":
			fail("%s and %s have the same URL /%s", urls[u], p, u)
		}
		urls[u] = p
		m.site[p] = s
		m.pages = append(m.pages, p)
	})
	for _, p := range raw.Unpublished {
		if page(p) {
			m.unpublished[p] = true
		}
	}
	for _, src := range raw.Sources {
		err := filepath.WalkDir(filepath.Join(root, src), func(file string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(file) != ".md" || agentFile(d.Name()) {
				return err
			}
			rel, err := filepath.Rel(root, file)
			if err != nil {
				return err
			}
			if rel = filepath.ToSlash(rel); !seen[rel] {
				fail("%s: neither in nav nor in unpublished", rel)
			}
			return nil
		})
		if err != nil {
			fail("source %s: %v", src, err)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return m, nil
}

// agentFile reports instructions for coding agents, never published.
func agentFile(name string) bool { return name == "AGENTS.md" || name == "CLAUDE.md" }

func parseNav(items []any) ([]entry, error) {
	entries := make([]entry, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case string:
			entries = append(entries, entry{page: v})
		case map[string]any:
			if len(v) != 1 {
				return nil, fmt.Errorf("a section is a single title: list of pages, got %d keys", len(v))
			}
			for title, sub := range v {
				list, ok := sub.([]any)
				if !ok || len(list) == 0 {
					return nil, fmt.Errorf("section %q: not a list of pages", title)
				}
				children, err := parseNav(list)
				if err != nil {
					return nil, fmt.Errorf("section %q: %w", title, err)
				}
				entries = append(entries, entry{section: title, entries: children})
			}
		default:
			return nil, fmt.Errorf("unexpected entry %v", item)
		}
	}
	return entries, nil
}

func walkPages(entries []entry, fn func(string)) {
	for _, e := range entries {
		if e.page != "" {
			fn(e.page)
		}
		walkPages(e.entries, fn)
	}
}

// sitePath maps a repository path to its path on the site: without the docs/
// prefix, a README.md becoming the index.md of its directory.
func sitePath(p string) string {
	p = strings.TrimPrefix(p, "docs/")
	if path.Base(p) == "README.md" {
		p = path.Join(path.Dir(p), "index.md")
	}
	return p
}

// siteURL is the URL of a page relative to the site root, with directory URLs:
// providers.md and providers/index.md are both providers/.
func siteURL(site string) string {
	u := strings.TrimSuffix(site, ".md")
	if u == "index" {
		return ""
	}
	return strings.TrimSuffix(u, "/index") + "/"
}
