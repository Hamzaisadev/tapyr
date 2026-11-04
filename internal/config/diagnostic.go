package config

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Position locates a diagnostic in a configuration file. Line and Column are
// 1-based; zero means unknown.
type Position struct {
	File   string
	Line   int
	Column int
}

// String returns "file:line:column", omitting unknown parts.
func (p Position) String() string {
	switch {
	case p.Line == 0:
		return p.File
	case p.Column == 0:
		return fmt.Sprintf("%s:%d", p.File, p.Line)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Column)
}

// Diagnostic is a configuration error with its location.
type Diagnostic struct {
	Pos Position
	// Path is the location of the offending value within the document, in
	// dotted form (spec.limits[0].rate). Empty for file-level errors.
	Path    string
	Message string
}

// String formats the diagnostic as "file:line:column: path: message".
func (d Diagnostic) String() string {
	var b strings.Builder
	b.WriteString(d.Pos.String())
	b.WriteString(": ")
	if d.Path != "" {
		b.WriteString(d.Path)
		b.WriteString(": ")
	}
	b.WriteString(d.Message)
	return b.String()
}

// Diagnostics is a list of configuration errors. It implements error.
type Diagnostics []Diagnostic

// Error returns one diagnostic per line.
func (ds Diagnostics) Error() string {
	lines := make([]string, len(ds))
	for i, d := range ds {
		lines[i] = d.String()
	}
	return strings.Join(lines, "\n")
}

// Sorted returns the diagnostics ordered by file, line, column and message,
// without duplicates.
func (ds Diagnostics) Sorted() Diagnostics {
	out := slices.Clone(ds)
	slices.SortStableFunc(out, func(a, b Diagnostic) int {
		return cmp.Or(
			cmp.Compare(a.Pos.File, b.Pos.File),
			cmp.Compare(a.Pos.Line, b.Pos.Line),
			cmp.Compare(a.Pos.Column, b.Pos.Column),
			cmp.Compare(a.Path, b.Path),
			cmp.Compare(a.Message, b.Message),
		)
	})
	return slices.Compact(out)
}
