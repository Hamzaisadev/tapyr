package main

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// target is the destination of a relative link, resolved in the repository.
type target struct {
	path   string // repository path, empty for a link to an anchor of the same file
	anchor string
}

// resolve resolves the destination of a link of the file from. It returns
// false for an absolute URL (with a scheme, or protocol-relative), which is
// not checked.
func resolve(from, dest string) (target, bool, error) {
	if strings.HasPrefix(dest, "//") {
		return target{}, false, nil
	}
	if u, err := url.Parse(dest); err == nil && u.Scheme != "" {
		return target{}, false, nil
	}
	p, anchor, _ := strings.Cut(dest, "#")
	p, _, _ = strings.Cut(p, "?")
	if p == "" {
		return target{anchor: anchor}, true, nil
	}
	p, err := url.PathUnescape(p)
	if err != nil {
		return target{}, true, fmt.Errorf("%s: %w", dest, err)
	}
	if strings.HasPrefix(p, "/") {
		return target{}, true, fmt.Errorf("%s: use a relative link, it resolves differently on GitHub and on the site", dest)
	}
	p = path.Join(path.Dir(from), p)
	if p == ".." || strings.HasPrefix(p, "../") {
		return target{}, true, fmt.Errorf("%s: outside the repository", dest)
	}
	return target{path: p, anchor: anchor}, true, nil
}

// check validates the manifest and the relative links of the Markdown files,
// and returns the problems found as "file:line: message".
func check(root string, files []string) []string {
	var problems []string
	if _, err := loadManifest(root); err != nil {
		problems = append(problems, strings.Split(err.Error(), "\n")...)
	}
	docs := map[string]document{}
	read := func(p string) (document, error) {
		if d, ok := docs[p]; ok {
			return d, nil
		}
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			return document{}, err
		}
		docs[p] = parse(string(b))
		return docs[p], nil
	}
	for _, file := range files {
		doc, err := read(file)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		for _, l := range doc.links {
			report := func(format string, args ...any) {
				problems = append(problems, fmt.Sprintf("%s:%d: ", file, l.line)+fmt.Sprintf(format, args...))
			}
			t, relative, err := resolve(file, l.dest)
			if err != nil {
				report("%v", err)
				continue
			}
			if !relative {
				continue
			}
			anchors := doc.anchors
			if t.path != "" {
				info, err := os.Stat(filepath.Join(root, t.path))
				if err != nil {
					report("%s: no such file", l.dest)
					continue
				}
				if t.anchor == "" || info.IsDir() || path.Ext(t.path) != ".md" {
					continue
				}
				d, err := read(t.path)
				if err != nil {
					report("%s: %v", l.dest, err)
					continue
				}
				anchors = d.anchors
			}
			if t.anchor != "" && !anchors[t.anchor] {
				report("%s: no heading with the anchor #%s", l.dest, t.anchor)
			}
		}
	}
	slices.Sort(problems)
	return problems
}
