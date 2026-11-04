package sim

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/kentrow/tapyr/internal/clock"
)

const synthProvider = `apiVersion: tapyr/v1alpha1
kind: Provider
metadata: { name: acme, version: 0.1.0 }
spec:
  baseURL: https://api.acme.invalid
  pagination: { type: offset, offset: { offsetParam: offset, limitParam: limit, pageSize: 2 } }
`

const synthSim = `apiVersion: tapyr/v1alpha1
kind: Simulation
metadata: { name: acme }
spec:
  inventory:
    seed: 7
    sets:
      - { name: orgs, count: 2 }
      - { name: devices, parent: orgs, count: 3 }
  routes:
    - path: /orgs
      response: { template: orgs.json.tmpl }
    - path: /orgs/{orgId}/devices
      pagination: offset
      response: { template: devices.json.tmpl, itemsField: data.items }
`

const devicesTemplate = `{{- $org := find "orgs" .vars.orgId -}}
{{- $devices := children "devices" $org -}}
{"success": true, "data": {"total": {{ len $devices }}, "items": [
{{- range $i, $d := $devices }}{{ if $i }},{{ end }}
  {"id": {{ json (id $d "d-") }}, "serial": {{ json (serial $d "XXXX-9999") }}, "mac": {{ json (mac $d) }},
   "ip": {{ json (ipv4 $d) }}, "model": {{ json (pick $d "model" "MX1" "MX2") }}, "n": {{ $d.n }},
   "status": {{ json (flap $d "status" "10m" "online:0.5" "offline:0.5") }}, "cpu": {{ walk $d "cpu" 0 100 "5m" }},
   "temp": {{ rand $d "temp" 20 40 }}, "rx": {{ counter $d "rx" 1000 }}, "port": {{ num $d "port" 1 48 }},
   "seen": {{ json (ts "2006-01-02T15:04:05Z" (ago "1m")) }}, "at": {{ unix $.now }}, "next": {{ add $d.n 1 }}}
{{- end }}]}}
`

func synthServer(t *testing.T, clk clock.Clock, sim string, files map[string]string) (*Server, error) {
	t.Helper()
	fsys := fstest.MapFS{
		"provider.yaml": &fstest.MapFile{Data: []byte(synthProvider)},
		"sim/sim.yaml":  &fstest.MapFile{Data: []byte(sim)},
	}
	for name, content := range files {
		fsys["sim/"+name] = &fstest.MapFile{Data: []byte(content)}
	}
	p, err := loader(t).LoadPackage(fsys, "acme", "acme", "test")
	if err != nil {
		t.Fatal(err)
	}
	return New(p, Options{Clock: clk})
}

func synthFiles() map[string]string {
	return map[string]string{
		"orgs.json.tmpl":    `[{{ range $i, $o := items "orgs" }}{{ if $i }},{{ end }}{"key": {{ json $o.key }}, "devices": {{ len (children "devices" $o) }}}{{ end }}]`,
		"devices.json.tmpl": devicesTemplate,
	}
}

func TestSyntheticInventory(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
	s, err := synthServer(t, clk, synthSim, synthFiles())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	status, _, body := get(t, srv.URL+"/orgs", "")
	want := []any{
		map[string]any{"key": "orgs-0", "devices": float64(3)},
		map[string]any{"key": "orgs-1", "devices": float64(3)},
	}
	if status != http.StatusOK || !cmp.Equal(body, want) {
		t.Fatalf("/orgs = %d %v", status, body)
	}

	// The envelope is kept, the list at itemsField is paginated.
	page := func(offset string) map[string]any {
		t.Helper()
		status, _, body := get(t, srv.URL+"/orgs/orgs-1/devices?limit=2&offset="+offset, "")
		if status != http.StatusOK {
			t.Fatalf("status %d: %v", status, body)
		}
		return body.(map[string]any)
	}
	first := page("0")
	data := first["data"].(map[string]any)
	if data["total"] != float64(3) || len(data["items"].([]any)) != 2 || first["success"] != true {
		t.Fatalf("first page = %v", first)
	}
	if items := page("2")["data"].(map[string]any)["items"].([]any); len(items) != 1 {
		t.Fatalf("second page = %v", items)
	}
	d0 := data["items"].([]any)[0].(map[string]any)
	if d0["n"] != float64(0) || d0["next"] != float64(1) || !strings.HasPrefix(d0["id"].(string), "d-") ||
		!strings.HasPrefix(d0["mac"].(string), "02:") || !strings.HasPrefix(d0["ip"].(string), "10.") ||
		len(d0["serial"].(string)) != len("XXXX-9999") || d0["seen"] != "2025-12-31T23:59:00Z" || d0["at"] != float64(1767225600) {
		t.Errorf("device = %v", d0)
	}
	if p := d0["port"].(float64); p < 1 || p > 48 {
		t.Errorf("port = %v", p)
	}
	if temp := d0["temp"].(float64); temp < 20 || temp > 40 {
		t.Errorf("temp = %v", temp)
	}

	// Same seed and time, same values; dynamics move with time, values
	// fixed per item do not, counters grow.
	if again := page("0"); !cmp.Equal(again, first) {
		t.Errorf("not deterministic:\n%v\n%v", again, first)
	}
	statuses := map[any]bool{}
	var cpus []any
	rx := d0["rx"].(float64)
	for range 20 {
		clk.Advance(10 * time.Minute)
		d := page("0")["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
		statuses[d["status"]] = true
		cpus = append(cpus, d["cpu"])
		if d["temp"] != d0["temp"] || d["model"] != d0["model"] || d["serial"] != d0["serial"] {
			t.Fatalf("fixed values changed: %v", d)
		}
		if d["rx"].(float64) <= rx {
			t.Fatalf("counter did not grow: %v then %v", rx, d["rx"])
		}
		rx = d["rx"].(float64)
	}
	if len(statuses) != 2 {
		t.Errorf("flap states seen: %v", statuses)
	}
	if cmp.Equal(cpus[0], cpus[1]) && cmp.Equal(cpus[1], cpus[2]) {
		t.Errorf("walk does not move: %v", cpus)
	}

	// Another seed gives other values.
	other, err := synthServer(t, clk, strings.Replace(synthSim, "seed: 7", "seed: 8", 1), synthFiles())
	if err != nil {
		t.Fatal(err)
	}
	if a, b := s.inv.hash("x"), other.inv.hash("x"); a == b {
		t.Error("the seed is not used")
	}
}

func TestSyntheticErrors(t *testing.T) {
	route := func(r string) string {
		head, _, _ := strings.Cut(synthSim, "  routes:")
		return head + "  routes:\n" + r
	}
	tests := []struct {
		name, sim string
		files     map[string]string
		err       string
	}{
		{
			name: "duplicate set",
			sim:  strings.Replace(synthSim, "{ name: devices, parent: orgs, count: 3 }", "{ name: orgs, count: 3 }", 1),
			err:  "set orgs is declared twice",
		},
		{
			name: "parent after",
			sim: strings.Replace(synthSim, "      - { name: orgs, count: 2 }\n      - { name: devices, parent: orgs, count: 3 }",
				"      - { name: devices, parent: orgs, count: 3 }\n      - { name: orgs, count: 2 }", 1),
			err: "parent orgs is not declared before it",
		},
		{
			name:  "template syntax",
			sim:   route("    - { path: /x, response: { template: x.tmpl } }\n"),
			files: map[string]string{"x.tmpl": "{{ range }}"},
			err:   "fixture x.tmpl",
		},
		{
			name: "items without pagination",
			sim:  route("    - { path: /x, response: { file: x.json, itemsField: data } }\n"),
			err:  "itemsField needs a pagination",
		},
		{
			name:  "no list at itemsField",
			sim:   route("    - { path: /x, pagination: offset, response: { file: x.json, itemsField: data } }\n"),
			files: map[string]string{"x.json": `{"data": {}}`},
			err:   "has no list at data",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := synthServer(t, nil, tt.sim, tt.files)
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("New() = %v, want %q", err, tt.err)
			}
		})
	}
}

func TestTemplateRuntimeErrors(t *testing.T) {
	files := synthFiles()
	files["bad.tmpl"] = `{"x": {{ find "orgs" "nope" }}}`
	files["notjson.tmpl"] = `{not json`
	files["obj-1.json"] = `{"id": 1}`
	sim := strings.Replace(synthSim, "  routes:\n", "  routes:\n    - { path: /bad, response: { template: bad.tmpl } }\n    - { path: /notjson, response: { template: notjson.tmpl } }\n    - { path: \"/objects/{id}\", response: { file: \"obj-{id}.json\" } }\n", 1)
	s, err := synthServer(t, nil, sim, files)
	if err != nil {
		t.Fatal(err)
	}
	// A fixture that does not render or parse answers 500 with its cause,
	// so that a contract test shows why instead of a bare "not found".
	for p, cause := range map[string]string{"/bad": `error calling find`, "/notjson": "notjson.tmpl"} {
		rec := call(s, p)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), cause) {
			t.Errorf("%s: %d %s, want 500 with %q", p, rec.Code, rec.Body, cause)
		}
	}
	// A missing fixture is an unknown object.
	if rec := call(s, "/objects/1"); rec.Code != http.StatusOK {
		t.Errorf("/objects/1: %d %s", rec.Code, rec.Body)
	}
	if rec := call(s, "/objects/2"); rec.Code != http.StatusNotFound {
		t.Errorf("/objects/2: %d %s, want 404", rec.Code, rec.Body)
	}
	if _, err := s.inv.funcs(time.Now)["flap"].(func(any, string, string, ...string) (string, error))(nil, "f", "1m", "bad"); err == nil {
		t.Error("flap accepted a state without weight")
	}
}
