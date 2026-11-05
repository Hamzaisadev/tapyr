package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kentrow/tapyr/internal/config"
)

func decode(t *testing.T, s string) schema {
	t.Helper()
	var m schema
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTypeOf(t *testing.T) {
	tests := []struct{ schema, want string }{
		{`{"type": "string"}`, "string"},
		{`{"type": ["integer", "string"]}`, "integer or string"},
		{`{"$ref": "#/$defs/SecretRef"}`, "[SecretRef](#secretref)"},
		{`{"type": "array", "items": {"$ref": "#/$defs/Limit"}}`, "list of [Limit](#limit)"},
		{`{"type": "object", "additionalProperties": {"type": "string"}}`, "map of string to string"},
		{`{"const": "tapyr/v1alpha1"}`, "`tapyr/v1alpha1`"},
		{`{"type": "object"}`, "object"},
		{`{}`, "any"},
	}
	for _, tt := range tests {
		if got := typeOf(decode(t, tt.schema)); got != tt.want {
			t.Errorf("typeOf(%s) = %q, want %q", tt.schema, got, tt.want)
		}
	}
}

func TestConstraints(t *testing.T) {
	tests := []struct{ schema, want string }{
		{`{"type": "number", "default": 1, "exclusiveMinimum": 0, "maximum": 1}`, "Default `1`. Greater than 0. Maximum 1."},
		{`{"type": "string", "enum": ["json", "text"], "pattern": "ignored"}`, "One of `json`, `text`."},
		{`{"type": "string", "pattern": "^[a-z]+$", "examples": ["30s", "5m"]}`, "Pattern `^[a-z]+$`. Examples `30s`, `5m`."},
		{`{"type": "array", "minItems": 1, "uniqueItems": true, "items": {"type": "integer", "minimum": 100, "maximum": 599}}`, "Minimum items 1. Unique items. Each item: minimum 100, maximum 599."},
		{`{"type": "object", "propertyNames": {"pattern": "^[a-z]+$"}, "additionalProperties": {"type": "string", "minLength": 1}}`, "Keys match `^[a-z]+$`. Each value: minimum length 1."},
		{`{"type": "array", "items": {"$ref": "#/$defs/Limit"}}`, ""},
		{`{"type": "string", "format": "uri", "default": {"a": [1]}}`, "Default `{\"a\":[1]}`. Format uri."},
	}
	for _, tt := range tests {
		if got := constraints(decode(t, tt.schema)); got != tt.want {
			t.Errorf("constraints(%s) =\n%q, want\n%q", tt.schema, got, tt.want)
		}
	}
}

func TestCode(t *testing.T) {
	tests := []struct{ in, want string }{
		{"^[a-z]+$", "`^[a-z]+$`"},
		{`^(s|m)\.[A-Z_]*$`, "<code>^(s&#124;m)&#92;.&#91;A-Z&#95;&#93;&#42;$</code>"},
		{"a|<b>&c", "<code>a&#124;&#60;b&#62;&#38;c</code>"},
	}
	for _, tt := range tests {
		if got := code(tt.in); got != tt.want {
			t.Errorf("code(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestText(t *testing.T) {
	tests := []struct{ name, desc, want string }{
		{"scaleRates", "ScaleRates also scales rate and\nconcurrency limits.", "`scaleRates` also scales rate and concurrency limits."},
		{"caFile", "CAFile is a PEM bundle.", "`caFile` is a PEM bundle."},
		{"labels", "Labels added to every sample.", "Labels added to every sample."},
		{"by", "By lists var:<name>, * and remote_write | x.", `By lists var:&lt;name&gt;, \* and remote\_write \| x.`},
		{"share", "Part of the quota.", "Part of the quota."},
	}
	for _, tt := range tests {
		if got := fieldText(tt.name, tt.desc); got != tt.want {
			t.Errorf("fieldText(%q, %q) = %q, want %q", tt.name, tt.desc, got, tt.want)
		}
	}
}

func TestObject(t *testing.T) {
	s := decode(t, `{
		"type": "object",
		"properties": {
			"type": {"type": "string", "description": "Type of auth.", "enum": ["basic", "oauth"]},
			"basic": {"$ref": "#/$defs/Basic"},
			"oauth": {"$ref": "#/$defs/OAuth"},
			"kind": {"const": "Provider"}
		},
		"required": ["type", "kind"],
		"allOf": [
			{"if": {"properties": {"type": {"const": "basic"}}, "required": ["type"]}, "then": {"required": ["basic"]}},
			{"if": {"properties": {"type": {"const": "oauth"}}, "required": ["type"]}, "then": {"required": ["oauth", "basic"]}}
		],
		"oneOf": [{"required": ["basic"]}, {"required": ["oauth"]}]
	}`)
	var b strings.Builder
	if err := object(&b, s); err != nil {
		t.Fatal(err)
	}
	want := "| Field | Type | Required | Description |\n|---|---|---|---|\n" +
		"| `basic` | [Basic](#basic) |  |  |\n" +
		"| `kind` | `Provider` | yes | Kind of the document. |\n" +
		"| `oauth` | [OAuth](#oauth) |  |  |\n" +
		"| `type` | string | yes | Type of auth. One of `basic`, `oauth`. |\n" +
		"\n- When `type` is `basic`: `basic` is required.\n" +
		"- When `type` is `oauth`: `oauth` and `basic` are required.\n" +
		"- Exactly one of `basic`, `oauth` is set.\n"
	if b.String() != want {
		t.Errorf("object:\n%s\nwant:\n%s", b.String(), want)
	}
	for _, unsupported := range []string{
		`{"anyOf": [{"type": "string"}]}`,
		`{"allOf": [{"required": ["a"]}]}`,
		`{"oneOf": [{"required": ["a", "b"]}]}`,
	} {
		if err := object(&strings.Builder{}, decode(t, unsupported)); err == nil {
			t.Errorf("object(%s): no error for a construct the reference does not render", unsupported)
		}
	}
}

func TestReference(t *testing.T) {
	files, err := config.GenerateSchemas("github.com/kentrow/tapyr/internal/tools/schemagen", "../../config")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := writeReference(files, dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "account.md", "provider.md", "scenario.md"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(b), generatedNotice) {
			t.Errorf("%s does not start with the generated notice", name)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "account.md"))
	for _, want := range []string{"# Account\n", "## AccountSpec\n", "AccountSpec is the specification of an Account.", "| `provider` | [Name](#name) | yes |", "- Exactly one field is set.\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("account.md lacks %q", want)
		}
	}
	files["extra.json"] = []byte(`{}`)
	if err := writeReference(files, t.TempDir()); err == nil || !strings.Contains(err.Error(), "extra.json") {
		t.Errorf("a schema in no group: %v", err)
	}
}
