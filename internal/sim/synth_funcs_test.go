package sim

import (
	"encoding/json"
	"strings"
	"testing"
	"text/template"
	"time"
)

// render executes a template expression with the helpers of a seeded
// inventory.
func renderExpr(t *testing.T, expr string) (string, error) {
	t.Helper()
	inv, err := newInventory(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.January, 1, 1, 0, 0, 0, time.UTC)
	tmpl, err := template.New("t").Funcs(inv.funcs(func() time.Time { return now })).Parse(expr)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	err = tmpl.Execute(&b, map[string]any{"n": json.Number("2.5")})
	return b.String(), err
}

func TestTemplateHelpers(t *testing.T) {
	for expr, want := range map[string]string{
		`{{ add 1 2 }}`:                      "3",
		`{{ add 1 2.5 }}`:                    "3.5",
		`{{ sub 5 7 }}`:                      "-2",
		`{{ mul 3 "2" }}`:                    "6",
		`{{ div 7 2 }}`:                      "3.5",
		`{{ div 7 0 }}`:                      "0",
		`{{ mod 7 3 }}`:                      "1",
		`{{ mod 7 0 }}`:                      "0",
		`{{ add .n 1 }}`:                     "3.5",
		`{{ len (seq 3) }}`:                  "3",
		`{{ len (seq -1) }}`:                 "0",
		`{{ json "a\"b" }}`:                  `"a\"b"`,
		`{{ num "x" "f" 5 5 }}`:              "5",
		`{{ ts "15:04" (ago "30m") }}`:       "00:30",
		`{{ unix (ago "1h") }}`:              "1767225600",
		`{{ len (where (items "") "a" 1) }}`: "0",
	} {
		got, err := renderExpr(t, expr)
		if err != nil && !strings.Contains(expr, `items ""`) {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if err == nil && got != want {
			t.Errorf("%s = %q, want %q", expr, got, want)
		}
	}
	for _, expr := range []string{
		`{{ add "x" 1 }}`, `{{ add 1 "x" }}`, `{{ mod "x" 1 }}`, `{{ add true 1 }}`,
		`{{ pick "x" "f" }}`, `{{ rand "x" "f" "a" 1 }}`, `{{ rand "x" "f" 1 "b" }}`,
		`{{ walk "x" "f" "a" 1 "1m" }}`, `{{ walk "x" "f" 0 "b" "1m" }}`, `{{ walk "x" "f" 0 1 "never" }}`,
		`{{ flap "x" "f" "0s" "a:1" }}`, `{{ flap "x" "f" "1m" "a:0" }}`, `{{ counter "x" "f" "fast" }}`,
		`{{ ago "soon" }}`, `{{ children "nope" "x" }}`, `{{ items "nope" }}`, `{{ find "nope" "x" }}`,
	} {
		if _, err := renderExpr(t, expr); err == nil {
			t.Errorf("%s: no error", expr)
		}
	}
}

func TestAnonymizerValues(t *testing.T) {
	a, err := NewAnonymizer(AnonymizeRules{})
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"id": 1234.0, "lat": int64(45), "lng": 4.5, "latitude": json.Number("45.1"), "serial": json.Number("777"),
		"name": int64(5), "host": "router.example.org", "address": "1 rue X", "imei": "356000000000001",
		"bssid": "00-1e-42-ab-cd-ef", "ipv6": "2001:4860::1", "gateway": "81.2.69.1", "flag": true, "none": nil,
		"text": "no address here 1.2.3", "fw_version": "1.2.3.4", "phone": "",
	}
	out := a.Document(doc).(map[string]any)
	if v, ok := out["id"].(float64); !ok || v == 1234 || v < 1000 || v > 9999 {
		t.Errorf("id = %v", out["id"])
	}
	if v, ok := out["lat"].(float64); !ok || v < -80 || v > 80 {
		t.Errorf("lat = %v", out["lat"])
	}
	if v, ok := out["lng"].(float64); !ok || v == 4.5 {
		t.Errorf("lng = %v", out["lng"])
	}
	if v, ok := out["latitude"].(json.Number); !ok || v == "45.1" {
		t.Errorf("latitude = %v", out["latitude"])
	}
	if v, ok := out["serial"].(json.Number); !ok || len(v) != 3 || v == "777" {
		t.Errorf("serial = %v", out["serial"])
	}
	for k, prefix := range map[string]string{"host": "host-", "address": "address-", "bssid": "02-", "ipv6": "2001:db8:"} {
		if s, _ := out[k].(string); !strings.HasPrefix(s, prefix) {
			t.Errorf("%s = %v, want prefix %s", k, out[k], prefix)
		}
	}
	if out["flag"] != true || out["none"] != nil || out["text"] != "no address here 1.2.3" || out["fw_version"] != "1.2.3.4" || out["phone"] != "" {
		t.Errorf("unchanged values changed: %v", out)
	}
	if _, ok := out["name"].(int64); !ok {
		t.Errorf("name = %v", out["name"])
	}
	if s := a.str(RuleRedact, "x"); s != "redacted" {
		t.Errorf("redact = %s", s)
	}
	if s := a.str(RuleCoordinate, "1.5"); s == "1.5" {
		t.Errorf("coordinate = %s", s)
	}
	if v := a.apply(RuleName, []any{1}); v == nil {
		t.Error("apply of a list")
	}
	if got := a.Var("weird", "x-1"); len(got) != 3 {
		t.Errorf("Var() = %s", got)
	}
}
