package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// link is the destination of a link, an image or an HTML attribute in a
// Markdown file, outside code.
type link struct {
	start, end int // byte offsets of the destination
	line       int
	dest       string
	image      bool
}

// document is what the checks and the export need from a Markdown file.
type document struct {
	links   []link
	anchors map[string]bool
	title   string // text of the first level 1 heading
}

var (
	fenceRe   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	headingRe = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$`)
	// An inline link or image; the text may hold one level of brackets.
	inlineRe = regexp.MustCompile(`(!?)\[(?:[^\[\]]|\[[^\[\]]*\])*\]\(\s*(<[^>]*>|[^\s()]+)(?:\s+(?:"[^"]*"|'[^']*'))?\s*\)`)
	refDefRe = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:[ \t]*(<[^>]*>|\S+)`)
	attrRe   = regexp.MustCompile(`\b(src|href|srcset)="([^"\s]+)"`)
	idRe     = regexp.MustCompile(`<a\s+(?:id|name)="([^"]+)"`)
	inlineMd = regexp.MustCompile("!?\\[([^\\]]*)\\]\\([^)]*\\)|<[^>]+>|[`*]")
)

// parse finds the links, the heading anchors and the title of a Markdown file.
// It skips fenced code blocks and code spans, and leaves the rest of the
// syntax to the site generator: links are checked, not rendered.
func parse(src string) document {
	doc := document{anchors: map[string]bool{}}
	counts := map[string]int{}
	var fence string
	offset := 0
	for n, line := range strings.SplitAfter(src, "\n") {
		start := offset
		offset += len(line)
		line = strings.TrimRight(line, "\r\n")
		if m := fenceRe.FindStringSubmatch(line); m != nil {
			marker := m[1]
			switch {
			case fence == "":
				fence = marker
				continue
			case marker[0] == fence[0] && len(marker) >= len(fence) && strings.TrimSpace(line) == marker:
				fence = ""
				continue
			}
		}
		if fence != "" {
			continue
		}
		code := codeSpans(line)
		outside := func(i int) bool {
			for _, c := range code {
				if i >= c[0] && i < c[1] {
					return false
				}
			}
			return true
		}
		if m := headingRe.FindStringSubmatch(line); m != nil {
			text := inlineMd.ReplaceAllString(m[2], "$1")
			if m[1] == "#" && doc.title == "" {
				doc.title = strings.TrimSpace(text)
			}
			slug := githubSlug(text)
			if c := counts[slug]; c > 0 {
				doc.anchors[fmt.Sprintf("%s-%d", slug, c)] = true
			} else {
				doc.anchors[slug] = true
			}
			counts[slug]++
		}
		for _, m := range idRe.FindAllStringSubmatchIndex(line, -1) {
			if outside(m[0]) {
				doc.anchors[line[m[2]:m[3]]] = true
			}
		}
		add := func(s, e int, image bool) {
			if line[s] == '<' {
				s, e = s+1, e-1
			}
			doc.links = append(doc.links, link{start: start + s, end: start + e, line: n + 1, dest: line[s:e], image: image})
		}
		if m := refDefRe.FindStringSubmatchIndex(line); m != nil {
			add(m[2], m[3], false)
		}
		for _, m := range inlineRe.FindAllStringSubmatchIndex(line, -1) {
			if outside(m[0]) {
				add(m[4], m[5], m[3] > m[2])
			}
		}
		for _, m := range attrRe.FindAllStringSubmatchIndex(line, -1) {
			if outside(m[0]) {
				add(m[4], m[5], line[m[2]:m[3]] != "href")
			}
		}
	}
	return doc
}

// codeSpans returns the byte ranges of the code spans of a line: a run of
// backticks up to the next run of the same length.
func codeSpans(line string) [][2]int {
	var spans [][2]int
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(line) && line[j] == '`' {
			j++
		}
		run := line[i:j]
		end := -1
		for k := j; k < len(line); {
			if line[k] != '`' {
				k++
				continue
			}
			l := k
			for l < len(line) && line[l] == '`' {
				l++
			}
			if l-k == len(run) {
				end = l
				break
			}
			k = l
		}
		if end < 0 {
			i = j
			continue
		}
		spans = append(spans, [2]int{i, end})
		i = end
	}
	return spans
}

// githubSlug is the anchor GitHub gives a heading: lowercase, letters,
// digits, hyphens and underscores kept, spaces turned into hyphens, the rest
// removed.
func githubSlug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r), r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}
