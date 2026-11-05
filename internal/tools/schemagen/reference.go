package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/kentrow/tapyr/internal/config"
)

type schema = map[string]any

// referenceGroup is a section of the index of the configuration reference.
type referenceGroup struct {
	title, intro string
	kinds        []referenceKind
}

type referenceKind struct {
	kind config.Kind
	spec string // anchor of the kind in docs/spec/03-configuration.md
}

var referenceGroups = []referenceGroup{
	{"Configuration", "The documents of a Tapyr configuration.", []referenceKind{
		{config.KindAccount, "account"},
		{config.KindJob, "job"},
		{config.KindOutput, "output"},
		{config.KindSettings, "settings"},
	}},
	{"Provider packages", "The documents of a provider package (`providers/<name>/`).", []referenceKind{
		{config.KindProvider, "provider"},
		{config.KindJobTemplate, "jobtemplate"},
		{config.KindSimulation, "provider-packages"},
		{config.KindScenario, "provider-packages"},
	}},
}

// envelope describes the fields every document has, which carry no doc
// comment in the schemas.
var envelope = map[string]string{
	"apiVersion": "Version of the configuration format.",
	"kind":       "Kind of the document.",
	"metadata":   "Name and description of the document.",
	"spec":       "Specification of the document.",
}

const generatedNotice = "<!-- Generated from the JSON Schemas by make schema (internal/tools/schemagen). Do not edit. -->\n\n"

// referencePage is the file name of the reference page of a kind.
func referencePage(k config.Kind) string { return strings.ToLower(string(k)) + ".md" }

// writeReference writes the configuration reference generated from the
// per-kind schemas into dir: a page per kind and an index (ADR 0010).
func writeReference(files map[string][]byte, dir string) error {
	pages := map[string]string{}
	covered := map[string]bool{config.CombinedSchemaFile: true}
	var index strings.Builder
	index.WriteString(generatedNotice)
	index.WriteString("# Configuration reference\n\n")
	index.WriteString("Every field of every document kind of `tapyr/v1alpha1`, generated from the [JSON Schemas](../../schemas/v1alpha1/) that Tapyr validates configuration files with. The [configuration model](../spec/03-configuration.md) explains the kinds and shows examples. Editors validate and complete configuration files with the combined schema `tapyr.json`, for example with the YAML language server:\n\n")
	index.WriteString("```yaml\n# yaml-language-server: $schema=" + config.SchemaBaseURL + config.CombinedSchemaFile + "\n```\n")
	for _, g := range referenceGroups {
		fmt.Fprintf(&index, "\n## %s\n\n%s\n\n| Kind | Description |\n|---|---|\n", g.title, g.intro)
		for _, rk := range g.kinds {
			file := config.SchemaFile(rk.kind)
			b, ok := files[file]
			if !ok {
				return fmt.Errorf("no schema %s", file)
			}
			covered[file] = true
			var root schema
			if err := json.Unmarshal(b, &root); err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			page, err := renderKind(rk, file, root)
			if err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			pages[referencePage(rk.kind)] = page
			fmt.Fprintf(&index, "| [%s](%s) | %s |\n", rk.kind, referencePage(rk.kind), text(str(root, "description")))
		}
	}
	for file := range files {
		if !covered[file] {
			return fmt.Errorf("schema %s is in no group of the reference", file)
		}
	}
	index.WriteString("\n## Command line\n\nEvery command and flag of `tapyr`: [command line reference](cli.md).\n")
	pages["README.md"] = index.String()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(pages)) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(pages[name]), 0o600); err != nil {
			return err
		}
		fmt.Println("wrote", filepath.Join(dir, name))
	}
	return nil
}

func renderKind(rk referenceKind, file string, root schema) (string, error) {
	defs, _ := root["$defs"].(map[string]any)
	title := string(rk.kind)
	if _, clash := defs[title]; clash {
		return "", fmt.Errorf("definition %s has the anchor of the page title", title)
	}
	var b strings.Builder
	b.WriteString(generatedNotice)
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", title, text(str(root, "description")))
	fmt.Fprintf(&b, "Schema: [`%s`](../../schemas/v1alpha1/%s). Explanations and examples: [configuration model](../spec/03-configuration.md#%s).\n\n", file, file, rk.spec)
	if err := object(&b, root); err != nil {
		return "", err
	}
	for _, name := range reachable(root, defs) {
		def, _ := defs[name].(map[string]any)
		fmt.Fprintf(&b, "\n## %s\n\n", name)
		if d := str(def, "description"); d != "" {
			fmt.Fprintf(&b, "%s\n\n", text(d))
		}
		var err error
		if _, ok := def["properties"]; ok {
			if t := types(def); len(t) > 1 {
				var others []string
				for _, o := range slices.DeleteFunc(t, func(s string) bool { return s == "object" }) {
					others = append(others, article(o)+" "+o)
				}
				fmt.Fprintf(&b, "Either %s, or an object:\n\n", strings.Join(others, " or "))
			}
			err = object(&b, def)
		} else {
			err = scalar(&b, def)
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
	}
	return b.String(), nil
}

// reachable lists the definitions used from the root, breadth first, so
// that a type comes soon after the first type using it.
func reachable(root schema, defs map[string]any) []string {
	var order []string
	seen := map[string]bool{}
	queue := []any{root}
	for len(queue) > 0 {
		var refs []string
		collectRefs(queue[0], &refs)
		queue = queue[1:]
		for _, r := range refs {
			if !seen[r] {
				seen[r] = true
				order = append(order, r)
				queue = append(queue, defs[r])
			}
		}
	}
	return order
}

func collectRefs(v any, refs *[]string) {
	switch v := v.(type) {
	case map[string]any:
		if r, ok := v["$ref"].(string); ok {
			*refs = append(*refs, strings.TrimPrefix(r, "#/$defs/"))
		}
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if k != "$defs" {
				collectRefs(v[k], refs)
			}
		}
	case []any:
		for _, e := range v {
			collectRefs(e, refs)
		}
	}
}

// object writes the field table of an object schema and its conditions.
func object(b *strings.Builder, s schema) error {
	props, _ := s["properties"].(map[string]any)
	required := map[string]bool{}
	for _, r := range list(s, "required") {
		required[fmt.Sprint(r)] = true
	}
	b.WriteString("| Field | Type | Required | Description |\n|---|---|---|---|\n")
	for _, name := range slices.Sorted(maps.Keys(props)) {
		p, _ := props[name].(map[string]any)
		desc := fieldText(name, str(p, "description"))
		if desc == "" {
			desc = envelope[name]
		}
		if c := constraints(p); c != "" {
			desc = strings.TrimSpace(desc + " " + c)
		}
		req := ""
		if required[name] {
			req = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", name, typeOf(p), req, desc)
	}
	var conds []string
	for _, c := range list(s, "allOf") {
		cond, err := condition(c)
		if err != nil {
			return err
		}
		conds = append(conds, cond)
	}
	if alts := list(s, "oneOf"); len(alts) > 0 {
		var names []string
		for _, a := range alts {
			r := list(a.(map[string]any), "required")
			if len(r) != 1 {
				return fmt.Errorf("oneOf: only single required fields are rendered")
			}
			names = append(names, code(fmt.Sprint(r[0])))
		}
		conds = append(conds, "Exactly one of "+strings.Join(names, ", ")+" is set.")
	}
	if num(s, "minProperties") == 1 && num(s, "maxProperties") == 1 {
		conds = append(conds, "Exactly one field is set.")
	}
	if len(conds) > 0 {
		b.WriteString("\n")
		for _, c := range conds {
			fmt.Fprintf(b, "- %s\n", c)
		}
	}
	for _, k := range []string{"anyOf", "not", "patternProperties"} {
		if _, ok := s[k]; ok {
			return fmt.Errorf("%s is not rendered in the reference", k)
		}
	}
	return nil
}

// condition renders {if: {properties: {f: {const: v}}}, then: {required: [...]}}.
func condition(c any) (string, error) {
	m, _ := c.(map[string]any)
	ifs, _ := m["if"].(map[string]any)
	then, _ := m["then"].(map[string]any)
	props, _ := ifs["properties"].(map[string]any)
	if len(props) != 1 || then == nil {
		return "", fmt.Errorf("allOf: only if/then on one constant field are rendered")
	}
	field := slices.Collect(maps.Keys(props))[0]
	value, ok := props[field].(map[string]any)["const"]
	if !ok {
		return "", fmt.Errorf("allOf: only if/then on one constant field are rendered")
	}
	var req []string
	for _, r := range list(then, "required") {
		req = append(req, code(fmt.Sprint(r)))
	}
	verb := "is"
	if len(req) > 1 {
		verb = "are"
	}
	return fmt.Sprintf("When `%s` is %s: %s %s required.", field, code(literal(value)), strings.Join(req, " and "), verb), nil
}

// scalar writes a definition that is not an object.
func scalar(b *strings.Builder, s schema) error {
	t := types(s)
	if len(t) == 0 {
		return fmt.Errorf("no type")
	}
	fmt.Fprintf(b, "Type: %s.", strings.Join(t, " or "))
	if c := constraints(s); c != "" {
		b.WriteString(" " + c)
	}
	b.WriteString("\n")
	return nil
}

func typeOf(s schema) string {
	if r, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(r, "#/$defs/")
		return fmt.Sprintf("[%s](#%s)", name, strings.ToLower(name))
	}
	if c, ok := s["const"]; ok {
		return code(literal(c))
	}
	t := types(s)
	switch {
	case len(t) == 0:
		return "any"
	case len(t) == 1 && t[0] == "array":
		items, _ := s["items"].(map[string]any)
		return "list of " + typeOf(items)
	case len(t) == 1 && t[0] == "object":
		if ap, ok := s["additionalProperties"].(map[string]any); ok {
			return "map of string to " + typeOf(ap)
		}
		return "object"
	}
	return strings.Join(t, " or ")
}

func types(s schema) []string {
	switch t := s["type"].(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, fmt.Sprint(e))
		}
		return out
	}
	return nil
}

// constraints renders the validation keywords of a schema as sentences.
func constraints(s schema) string {
	c := constraintList(s)
	for _, sub := range []struct{ key, label string }{{"items", "Each item"}, {"additionalProperties", "Each value"}} {
		if m, ok := s[sub.key].(map[string]any); ok && m["$ref"] == nil {
			if nested := constraintList(m); len(nested) > 0 {
				for i, n := range nested {
					nested[i] = strings.ToLower(n[:1]) + n[1:]
				}
				c = append(c, sub.label+": "+strings.Join(nested, ", "))
			}
		}
	}
	if len(c) == 0 {
		return ""
	}
	return strings.Join(c, ". ") + "."
}

// constraintList renders the validation keywords of a schema itself, one
// fragment each.
func constraintList(s schema) []string {
	var c []string
	values := func(key string) string {
		var vals []string
		for _, v := range list(s, key) {
			vals = append(vals, code(literal(v)))
		}
		return strings.Join(vals, ", ")
	}
	if d, ok := s["default"]; ok {
		c = append(c, "Default "+code(literal(d)))
	}
	if e := values("enum"); e != "" {
		c = append(c, "One of "+e)
	} else if p := str(s, "pattern"); p != "" {
		c = append(c, "Pattern "+code(p))
	}
	for _, b := range []struct{ key, label string }{
		{"minimum", "Minimum"},
		{"exclusiveMinimum", "Greater than"},
		{"maximum", "Maximum"},
		{"exclusiveMaximum", "Less than"},
		{"minLength", "Minimum length"},
		{"maxLength", "Maximum length"},
		{"minItems", "Minimum items"},
		{"maxItems", "Maximum items"},
	} {
		if v, ok := s[b.key]; ok {
			c = append(c, b.label+" "+literal(v))
		}
	}
	if u, _ := s["uniqueItems"].(bool); u {
		c = append(c, "Unique items")
	}
	if f := str(s, "format"); f != "" {
		c = append(c, "Format "+f)
	}
	if ex := values("examples"); ex != "" {
		c = append(c, "Examples "+ex)
	}
	if pn, ok := s["propertyNames"].(map[string]any); ok {
		if p := str(pn, "pattern"); p != "" {
			c = append(c, "Keys match "+code(p))
		}
	}
	return c
}

// fieldText is the description of a field. A doc comment starting with a
// compound Go field name (ScaleRates, CAFile) starts with the YAML name
// instead; a single word (Labels, Budget) reads as English and stays.
func fieldText(name, desc string) string {
	t := text(desc)
	first, rest, _ := strings.Cut(t, " ")
	if strings.EqualFold(first, name) && strings.IndexFunc(first[1:], unicode.IsUpper) >= 0 {
		return "`" + name + "` " + rest
	}
	return t
}

// text turns a doc comment into a table cell: one line, Markdown characters
// escaped.
func text(desc string) string {
	return strings.NewReplacer("|", `\|`, "*", `\*`, "_", `\_`, "<", "&lt;", ">", "&gt;").Replace(strings.Join(strings.Fields(desc), " "))
}

// code renders a code span. A value holding a pipe uses an HTML code element,
// since GitHub and Python-Markdown disagree on pipes in code spans of tables;
// the characters Markdown or HTML would take for syntax are written as
// character references.
func code(v string) string {
	if !strings.Contains(v, "|") {
		return "`" + v + "`"
	}
	var b strings.Builder
	b.WriteString("<code>")
	for _, r := range v {
		if strings.ContainsRune("\\*_`|<>&[]", r) {
			fmt.Fprintf(&b, "&#%d;", r)
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteString("</code>")
	return b.String()
}

// literal renders a JSON value as written in YAML: strings unquoted.
func literal(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func article(word string) string {
	if strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

func str(s schema, key string) string { v, _ := s[key].(string); return v }

func list(s schema, key string) []any { v, _ := s[key].([]any); return v }

func num(s schema, key string) float64 { v, _ := s[key].(float64); return v }
