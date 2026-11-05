package mapping_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/mapping"
	"github.com/kentrow/tapyr/internal/provider"
)

// FuzzExtract evaluates the mappings of every built-in job template on an
// arbitrary JSON response. Whatever the API returns, an evaluation never
// panics and stays within its limits, and every sample it produces is
// well formed: a name, and labels with a name and a value.
func FuzzExtract(f *testing.F) {
	l, err := config.NewLoader()
	if err != nil {
		f.Fatal(err)
	}
	pkgs, err := provider.Builtin(l)
	if err != nil {
		f.Fatal(err)
	}
	env, err := mapping.NewEnv(1_000_000)
	if err != nil {
		f.Fatal(err)
	}
	type template struct {
		name    string
		extract *mapping.Extract
	}
	var templates []template
	for _, p := range pkgs {
		for _, jt := range p.JobTemplates {
			if jt.Spec.Extract == nil {
				continue
			}
			x, errs := env.CompileExtract(jt.Spec.Extract)
			if len(errs) > 0 {
				f.Fatalf("%s: %v", jt.Metadata.Name, errs)
			}
			templates = append(templates, template{string(jt.Metadata.Name), x})
		}
	}
	if len(templates) < 10 {
		f.Fatalf("%d templates with a mapping, the built-in packages have more", len(templates))
	}

	// The fixtures of the simulators are realistic responses (the .tmpl
	// ones are Go templates, still worth mutating).
	fixtures, _ := filepath.Glob("../../providers/*/sim/fixtures/*")
	nested, _ := filepath.Glob("../../providers/*/sim/fixtures/*/*")
	for _, file := range append(fixtures, nested...) {
		if b, err := os.ReadFile(file); err == nil && len(b) < 64<<10 {
			f.Add(b)
		}
	}
	for _, s := range []string{
		`[]`, `{}`, `null`, `[null, 1, "x", true, [], {}]`, `{"data": [{"serial": null, "status": 1e308}]}`,
		`[{"id": -9223372036854775808, "lastSeen": "not a date", "usage": {"sent": "NaN"}}]`, `"just a string"`,
	} {
		f.Add([]byte(s))
	}

	lim := mapping.Limits{MaxItems: 1000, MaxSamples: 5000, LabelConflict: "override"}
	now := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, data []byte) {
		body, err := mapping.DecodeJSON(data)
		if err != nil {
			return
		}
		in := &mapping.Input{
			Pages:   []mapping.Page{{Status: http.StatusOK, Headers: http.Header{}, Body: body, ReceivedAt: now}},
			Vars:    map[string]string{"organizationId": "1", "networkId": "N_1", "siteId": "site-a", "serial": "S1", "deviceId": "1"},
			Account: map[string]any{"name": "acme", "labels": map[string]any{}, "params": map[string]any{}},
			Target:  map[string]any{"name": "", "labels": map[string]any{}, "params": map[string]any{}},
			Now:     now,
		}
		for _, tpl := range templates {
			res, err := tpl.extract.Evaluate(in, lim)
			if err != nil {
				continue
			}
			if len(res.Samples) > lim.MaxSamples {
				t.Errorf("%s: %d samples, the limit is %d", tpl.name, len(res.Samples), lim.MaxSamples)
			}
			for _, s := range res.Samples {
				if s.Name == "" {
					t.Errorf("%s: a sample without a name", tpl.name)
				}
				for _, l := range s.Labels {
					if l.Name == "" || l.Value == "" {
						t.Errorf("%s: sample %s has the label %q=%q", tpl.name, s.Name, l.Name, l.Value)
					}
				}
			}
		}
	})
}
