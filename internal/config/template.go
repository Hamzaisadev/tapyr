package config

import (
	"reflect"
	"regexp"
	"strings"
	"text/template"
)

// templateFuncs is the restricted function set of Go template fields.
var templateFuncs = template.FuncMap{
	// default returns fallback when v is empty: {{ .params.x | default "y" }}.
	"default": func(fallback, v any) any {
		if v == nil {
			return fallback
		}
		rv := reflect.ValueOf(v)
		if rv.IsZero() {
			return fallback
		}
		return v
	},
	// trimSuffix removes suffix from s: {{ .params.url | trimSuffix "/" }}.
	"trimSuffix": func(suffix, s string) string { return strings.TrimSuffix(s, suffix) },
	"lower":      strings.ToLower,
	"upper":      strings.ToUpper,
}

// parseTemplate parses a template field with the restricted function set.
func parseTemplate(name, text string) (*template.Template, error) {
	return template.New(name).Funcs(templateFuncs).Option("missingkey=zero").Parse(text)
}

// RenderTemplate evaluates a template field with the given params (the
// account params overridden by the target params), available as .params.
func RenderTemplate(name, text string, params map[string]string) (string, error) {
	t, err := parseTemplate(name, text)
	if err != nil {
		return "", err
	}
	if params == nil {
		params = map[string]string{}
	}
	var b strings.Builder
	if err := t.Execute(&b, map[string]any{"params": params}); err != nil {
		return "", err
	}
	return b.String(), nil
}

// PathVars returns the {name} placeholders of a request path.
func PathVars(path string) []string { return pathVars(path) }

// pathVarRe matches {name} placeholders in request paths.
var pathVarRe = regexp.MustCompile(`\{([^{}]*)\}`)

// pathVars returns the placeholders of a path template.
func pathVars(path string) []string {
	matches := pathVarRe.FindAllStringSubmatch(path, -1)
	vars := make([]string, 0, len(matches))
	for _, m := range matches {
		vars = append(vars, m[1])
	}
	return vars
}
