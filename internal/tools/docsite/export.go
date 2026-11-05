package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// index is the file the site repository builds its navigation and its "edit
// this page" links from.
type index struct {
	Ref string `json:"ref"`
	// Nav uses the nav format of mkdocs.yml: a page is its site path, a
	// section a single key object of its title to its entries.
	Nav   []any       `json:"nav"`
	Pages []indexPage `json:"pages"`
}

type indexPage struct {
	Path   string `json:"path"`   // site path
	Source string `json:"source"` // repository path
	Title  string `json:"title"`
}

// export writes the published pages to out at their site paths, with their
// links rewritten, copies the images they show, and writes the index file
// when given. It returns the number of pages written.
func export(root, ref, out, indexFile string) (int, error) {
	m, err := loadManifest(root)
	if err != nil {
		return 0, err
	}
	if entries, err := os.ReadDir(out); err == nil && len(entries) > 0 {
		return 0, fmt.Errorf("%s is not empty", out)
	}
	idx := index{Ref: ref, Nav: navIndex(m, m.nav), Pages: []indexPage{}}
	for _, p := range m.pages {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			return 0, err
		}
		src := string(b)
		doc := parse(src)
		var copies []string
		for i := len(doc.links) - 1; i >= 0; i-- {
			l := doc.links[i]
			dest, copied, err := m.rewrite(root, ref, p, l)
			if err != nil {
				return 0, fmt.Errorf("%s:%d: %w", p, l.line, err)
			}
			if copied != "" {
				copies = append(copies, copied)
			}
			src = src[:l.start] + dest + src[l.end:]
		}
		if err := write(filepath.Join(out, m.site[p]), []byte(src)); err != nil {
			return 0, err
		}
		for _, c := range copies {
			b, err := os.ReadFile(filepath.Join(root, c))
			if err != nil {
				return 0, err
			}
			if err := write(filepath.Join(out, sitePath(c)), b); err != nil {
				return 0, err
			}
		}
		idx.Pages = append(idx.Pages, indexPage{Path: m.site[p], Source: p, Title: doc.title})
	}
	if indexFile != "" {
		b, err := json.MarshalIndent(idx, "", "  ")
		if err != nil {
			return 0, err
		}
		if err := write(indexFile, append(b, '\n')); err != nil {
			return 0, err
		}
	}
	return len(m.pages), nil
}

// rewrite returns the destination of a link of the page p on the site: the
// site page when the target is published, the copied file for an image, the
// file on GitHub at ref otherwise. Absolute URLs and anchors of the page are
// kept. copied is the repository path of an image to copy.
func (m *manifest) rewrite(root, ref, p string, l link) (dest, copied string, err error) {
	t, relative, err := resolve(p, l.dest)
	if err != nil || !relative || t.path == "" {
		return l.dest, "", err
	}
	anchor := ""
	if t.anchor != "" {
		anchor = "#" + t.anchor
	}
	from := path.Dir(m.site[p])
	if site, ok := m.site[t.path]; ok {
		return relPath(from, site) + anchor, "", nil
	}
	info, err := os.Stat(filepath.Join(root, t.path))
	if err != nil {
		return "", "", fmt.Errorf("%s: no such file", l.dest)
	}
	if l.image && !info.IsDir() {
		return relPath(from, sitePath(t.path)), t.path, nil
	}
	kind := "blob"
	if info.IsDir() {
		kind = "tree"
	}
	return fmt.Sprintf("%s/%s/%s/%s%s", m.repository, kind, ref, t.path, anchor), "", nil
}

// relPath is the relative path from the directory dir to the file target,
// both site paths.
func relPath(dir, target string) string {
	rel, err := filepath.Rel(filepath.FromSlash(dir), filepath.FromSlash(target))
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}

func navIndex(m *manifest, entries []entry) []any {
	nav := make([]any, 0, len(entries))
	for _, e := range entries {
		if e.page != "" {
			nav = append(nav, m.site[e.page])
			continue
		}
		nav = append(nav, map[string]any{e.section: navIndex(m, e.entries)})
	}
	return nav
}

func write(file string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o600) //nolint:gosec // G703: paths come from the manifest, resolve keeps them in the repository and out.
}
