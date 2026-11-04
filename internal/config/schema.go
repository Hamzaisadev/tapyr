package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"github.com/invopop/jsonschema"
)

// SchemaBaseURL is the base of the $id of every generated schema.
const SchemaBaseURL = "https://raw.githubusercontent.com/kentrow/tapyr/main/schemas/v1alpha1/"

// CombinedSchemaFile is the schema accepting any document kind, meant for
// editors validating multi-document files.
const CombinedSchemaFile = "tapyr.json"

// SchemaFile returns the schema file name of a kind ("provider.json").
func SchemaFile(k Kind) string { return strings.ToLower(string(k)) + ".json" }

// documentTypes maps each kind to its Go document type.
var documentTypes = map[Kind]reflect.Type{
	KindProvider:    reflect.TypeFor[Provider](),
	KindJobTemplate: reflect.TypeFor[JobTemplate](),
	KindAccount:     reflect.TypeFor[Account](),
	KindJob:         reflect.TypeFor[Job](),
	KindOutput:      reflect.TypeFor[Output](),
	KindSettings:    reflect.TypeFor[Settings](),
	KindSimulation:  reflect.TypeFor[Simulation](),
	KindScenario:    reflect.TypeFor[Scenario](),
}

// kindDescriptions are the titles of the per-kind schemas.
var kindDescriptions = map[Kind]string{
	KindProvider:    "An API family: base URL, auth, pagination, error semantics and rate limits.",
	KindJobTemplate: "A reusable job definition shipped by a provider package.",
	KindAccount:     "An instance of a provider with credentials, params, targets and labels.",
	KindJob:         "What to collect for an account, from a template or inline.",
	KindOutput:      "Where collected samples are sent.",
	KindSettings:    "Global settings (at most one document).",
	KindSimulation:  "The simulator of a provider package (sim/sim.yaml).",
	KindScenario:    "A simulator scenario of a provider package (sim/scenarios/*.yaml).",
}

// GenerateSchemas returns the JSON Schema of every kind, keyed by file name,
// plus the combined schema. Descriptions come from the Go doc comments found
// in commentDir, the directory of this package; commentBase is the import
// path matching commentDir as seen from the working directory (see
// jsonschema.Reflector.AddGoComments).
func GenerateSchemas(commentBase, commentDir string) (map[string][]byte, error) {
	r := &jsonschema.Reflector{
		RequiredFromJSONSchemaTags: true,
		ExpandedStruct:             true,
	}
	if err := r.AddGoComments(commentBase, commentDir); err != nil {
		return nil, fmt.Errorf("extracting Go comments: %w", err)
	}

	files := make(map[string][]byte, len(documentTypes)+1)
	roots := make(map[Kind]map[string]any, len(documentTypes))
	defs := make(map[string]any)
	for _, k := range allKinds {
		s := r.ReflectFromType(documentTypes[k])
		s.ID = jsonschema.ID(SchemaBaseURL + SchemaFile(k))
		s.Title = string(k)
		s.Description = kindDescriptions[k]
		b, err := marshalSchema(s)
		if err != nil {
			return nil, fmt.Errorf("marshaling %s schema: %w", k, err)
		}
		files[SchemaFile(k)] = b

		var root map[string]any
		if err := json.Unmarshal(b, &root); err != nil {
			return nil, err
		}
		if d, ok := root["$defs"].(map[string]any); ok {
			for name, def := range d {
				if prev, dup := defs[name]; dup && !reflect.DeepEqual(prev, def) {
					return nil, fmt.Errorf("schema definition %q differs between kinds", name)
				}
				defs[name] = def
			}
		}
		for _, key := range []string{"$schema", "$id", "$defs"} {
			delete(root, key)
		}
		roots[k] = root
	}

	combined, err := combinedSchema(roots, defs)
	if err != nil {
		return nil, err
	}
	files[CombinedSchemaFile] = combined
	return files, nil
}

// combinedSchema dispatches on kind to the per-kind root schemas, all
// definitions inlined so the file is self-contained.
func combinedSchema(roots map[Kind]map[string]any, defs map[string]any) ([]byte, error) {
	kinds := make([]any, 0, len(allKinds))
	allOf := make([]any, 0, len(allKinds))
	merged := maps.Clone(defs)
	for _, k := range allKinds {
		kinds = append(kinds, string(k))
		name := "Document" + string(k)
		merged[name] = roots[k]
		allOf = append(allOf, map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"kind": map[string]any{"const": string(k)}},
				"required":   []any{"kind"},
			},
			"then": map[string]any{"$ref": "#/$defs/" + name},
		})
	}
	s := map[string]any{
		"$schema":     jsonschema.Version,
		"$id":         SchemaBaseURL + CombinedSchemaFile,
		"title":       "Tapyr configuration document",
		"description": "Any Tapyr " + APIVersion + " configuration document, dispatched on kind.",
		"type":        "object",
		"required":    []any{"apiVersion", "kind"},
		"properties": map[string]any{
			"apiVersion": map[string]any{"const": APIVersion},
			"kind":       map[string]any{"enum": kinds},
		},
		"allOf": allOf,
		"$defs": merged,
	}
	return marshalJSON(s)
}

func marshalSchema(s *jsonschema.Schema) ([]byte, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	// Re-encode through a generic value for stable, indented output.
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return marshalJSON(v)
}

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
