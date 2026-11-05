package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/goccy/go-yaml/parser"
	"github.com/google/go-cmp/cmp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kentrow/tapyr/schemas"
)

// TestSchemasUpToDate fails when the committed (and embedded) schemas drift
// from the Go types. Fix it with: make schema.
func TestSchemasUpToDate(t *testing.T) {
	generated, err := GenerateSchemas("github.com/kentrow/tapyr/internal/config", ".")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := fs.Glob(schemas.V1alpha1, "v1alpha1/*.json")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for name := range generated {
		names = append(names, "v1alpha1/"+name)
	}
	slices.Sort(names)
	if diff := cmp.Diff(names, embedded); diff != "" {
		t.Fatalf("schema files differ (-generated +committed), run make schema:\n%s", diff)
	}
	for name, want := range generated {
		got, err := fs.ReadFile(schemas.V1alpha1, "v1alpha1/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("schemas/v1alpha1/%s is out of date, run make schema", name)
		}
	}
}

// TestCombinedSchema checks that the editor schema accepts the specification
// examples and rejects a broken document, like the per-kind schemas.
// TestSchemaDefaults checks that every default= of a jsonschema tag reaches
// the generated schema, on fields of a basic type and on fields referencing
// a definition alike.
func TestSchemaDefaults(t *testing.T) {
	generated, err := GenerateSchemas("github.com/kentrow/tapyr/internal/config", ".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, k := range allKinds {
		var root map[string]any
		dec := json.NewDecoder(bytes.NewReader(generated[SchemaFile(k)]))
		dec.UseNumber()
		if err := dec.Decode(&root); err != nil {
			t.Fatal(err)
		}
		defs, _ := root["$defs"].(map[string]any)
		seen := map[reflect.Type]bool{}
		var walk func(s map[string]any, typ reflect.Type)
		walk = func(s map[string]any, typ reflect.Type) {
			for f := range typ.Fields() {
				ft := fieldType(f.Type)
				if f.Anonymous {
					walk(s, ft)
					continue
				}
				if want, ok := tagDefault(f); ok {
					props, _ := s["properties"].(map[string]any)
					p, _ := props[jsonName(f)].(map[string]any)
					if got, ok := p["default"]; !ok || fmt.Sprint(got) != want {
						t.Errorf("%s: %s.%s: default %v, want %s", SchemaFile(k), typ.Name(), f.Name, got, want)
					}
					checked++
				}
				if ft.Kind() == reflect.Struct && !seen[ft] {
					seen[ft] = true
					def, _ := defs[ft.Name()].(map[string]any)
					walk(def, ft)
				}
			}
		}
		walk(root, documentTypes[k])
	}
	if checked < 50 {
		t.Errorf("checked %d defaults, the walk misses fields", checked)
	}
}

func TestCombinedSchema(t *testing.T) {
	b, err := fs.ReadFile(schemas.V1alpha1, "v1alpha1/"+CombinedSchemaFile)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(SchemaBaseURL+CombinedSchemaFile, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(SchemaBaseURL + CombinedSchemaFile)
	if err != nil {
		t.Fatal(err)
	}

	validate := func(t *testing.T, src string) error {
		t.Helper()
		f, err := parser.ParseBytes([]byte(src), 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Docs {
			if d.Body == nil {
				continue
			}
			v, err := newTreeBuilder("example").convert(d.Body, nil, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := sch.Validate(v); err != nil {
				return err
			}
		}
		return nil
	}
	for i, block := range specExamples(t, filepath.Join("..", "..", "docs", "spec", "03-configuration.md")) {
		if err := validate(t, block); err != nil {
			t.Errorf("example %d rejected by %s: %v", i+1, CombinedSchemaFile, err)
		}
	}
	broken, err := os.ReadFile(filepath.Join("testdata", "invalid", "unions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(t, string(broken)); err == nil {
		t.Errorf("%s accepted testdata/invalid/unions.yaml", CombinedSchemaFile)
	}
}
