package api

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Validator checks responses against the OpenAPI description, so that the
// description and the API cannot drift apart. It is test support (the
// tests of this package and the end-to-end tests of cmd/tapyr), exported
// because those live in another package; production code never calls it.
type Validator struct {
	doc      map[string]any
	compiler *jsonschema.Compiler
	schemas  map[string]*jsonschema.Schema
}

const openAPIURL = "https://tapyr.eu/api/v1/openapi.json"

// NewValidator compiles the OpenAPI description.
func NewValidator() (*Validator, error) {
	data, err := yaml.YAMLToJSON(OpenAPI)
	if err != nil {
		return nil, fmt.Errorf("openapi.yaml: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("openapi.yaml: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	if err := c.AddResource(openAPIURL, doc); err != nil {
		return nil, err
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("openapi.yaml: not an object")
	}
	return &Validator{doc: m, compiler: c, schemas: map[string]*jsonschema.Schema{}}, nil
}

// Paths returns the documented operations, as "GET /api/v1/status".
func (v *Validator) Paths() []string {
	var out []string
	paths, _ := v.doc["paths"].(map[string]any)
	for path, item := range paths {
		ops, _ := item.(map[string]any)
		for method := range ops {
			out = append(out, strings.ToUpper(method)+" "+path)
		}
	}
	return out
}

// Validate checks a JSON response of an operation ("GET /api/v1/jobs/{name}")
// against the schema documented for its status.
func (v *Validator) Validate(operation string, status int, body []byte) error {
	key := operation + " " + strconv.Itoa(status)
	sch, ok := v.schemas[key]
	if !ok {
		method, path, _ := strings.Cut(operation, " ")
		ptr, err := v.schemaPointer(strings.ToLower(method), path, strconv.Itoa(status))
		if err != nil {
			return err
		}
		if sch, err = v.compiler.Compile(openAPIURL + "#" + ptr); err != nil {
			return fmt.Errorf("%s: compiling %s: %w", key, ptr, err)
		}
		v.schemas[key] = sch
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: invalid JSON: %w", key, err)
	}
	if err := sch.Validate(inst); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

// schemaPointer returns the JSON pointer of the response schema of an
// operation, following a $ref to components.responses.
func (v *Validator) schemaPointer(method, path, status string) (string, error) {
	ptr := "/paths/" + escape(path) + "/" + method + "/responses/" + status
	resp, ok := v.lookup(ptr).(map[string]any)
	if !ok {
		return "", fmt.Errorf("%s %s: status %s is not documented", strings.ToUpper(method), path, status)
	}
	if ref, ok := resp["$ref"].(string); ok {
		ptr = strings.TrimPrefix(ref, "#")
		if _, ok := v.lookup(ptr).(map[string]any); !ok {
			return "", fmt.Errorf("unresolved %s", ref)
		}
	}
	if v.lookup(ptr+"/content/application~1json/schema") == nil {
		return "", fmt.Errorf("%s %s %s: no JSON schema", strings.ToUpper(method), path, status)
	}
	return ptr + "/content/application~1json/schema", nil
}

func (v *Validator) lookup(ptr string) any {
	var cur any = v.doc
	for _, tok := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")]
	}
	return cur
}

func escape(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
