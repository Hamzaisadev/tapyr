package collect

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/auth"
	"github.com/kentrow/tapyr/internal/budget/budgettest"
	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/runtime"
)

// TestRunOnce runs a fan-out once: the configured instances in key order,
// each followed by the children of its inventory (filtered, capped by
// maxInstances, skipped when their variables fail), and a failing
// instance reported without stopping the others.
func TestRunOnce(t *testing.T) {
	var seen []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sites":
			fmt.Fprint(w, `[{"id": "e"}, {"id": "a", "zone": "eu"}, {"id": "c", "zone": "us"}, {"id": "b", "zone": "eu"}, {"id": "d", "zone": "eu"}]`)
		case "/broken":
			w.WriteHeader(http.StatusBadRequest)
		default:
			fmt.Fprint(w, `[{"n": 1}]`)
		}
	}))
	defer api.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`apiVersion: tapyr/v1alpha1
kind: Provider
metadata: { name: once, version: 0.1.0 }
spec: { baseURL: %q }
---
apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: acme }
spec: { provider: once }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: sites }
spec:
  account: acme
  interval: 1h
  role: inventory
  request: { path: /sites }
  extract: { inventory: { key: item.id, fields: { siteId: item.id, zone: "default(item.zone, '')" } } }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: devices }
spec:
  account: acme
  interval: 1h
  forEach:
    job: sites
    vars: { siteId: "parent.zone == '' ? parent.missing : parent.siteId" }
    filter: "parent.zone != 'us'"
    maxInstances: 2
  request: { path: "/sites/{siteId}/devices" }
  extract: { metrics: [{ name: device_up, value: "1.0", labels: { site: vars.siteId } }] }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: broken }
spec:
  account: acme
  interval: 1h
  request: { path: /broken }
  extract: { metrics: [{ name: x, value: "1.0" }] }
`, api.URL)), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := config.NewLoader()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := l.Parse([]string{path})
	if err == nil {
		cfg, err = config.Assemble(cfg, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.NewManager(auth.ManagerOptions{Clock: clock.NewFake(start)})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := runtime.Compile(cfg, runtime.Options{Auth: tokens})
	if err != nil {
		t.Fatal(err)
	}
	var visits []string
	var samples int
	New(Options{Budget: budgettest.Passthrough{}, Clock: clock.NewFake(start)}).RunOnce(context.Background(), graph.Jobs, func(v Visit) {
		line := v.Job.Instance
		if v.Err != nil {
			line += " failed"
		}
		if len(v.Skipped) > 0 {
			line += fmt.Sprintf(" skipped %d", len(v.Skipped))
		}
		if v.Output != nil {
			samples += len(v.Output.Samples)
		}
		visits = append(visits, line)
	})
	// e fails its variables, c is filtered out, d is over maxInstances.
	want := "broken failed|sites skipped 1|devices[siteId=a]|devices[siteId=b]"
	if got := strings.Join(visits, "|"); got != want {
		t.Errorf("visits = %s, want %s", got, want)
	}
	if samples != 2 {
		t.Errorf("%d samples, want 2", samples)
	}
	if got := strings.Join(seen, " "); got != "/broken /sites /sites/a/devices /sites/b/devices" {
		t.Errorf("requests = %s", got)
	}
}

type countingTransport struct{ n int }

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.n++
	return http.DefaultTransport.RoundTrip(req)
}

func TestClientFor(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `[]`) }))
	defer api.Close()
	own := &countingTransport{}
	e := New(Options{Budget: budgettest.Passthrough{}, Clock: clock.NewFake(start), ClientFor: func(account string) *http.Client {
		if account == "acme" {
			return &http.Client{Transport: own}
		}
		return nil
	}})
	job := newJob(t, api.URL, &config.Extract{Items: "response.body"})
	if _, err := e.Run(context.Background(), job, start, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	other := newJob(t, api.URL, &config.Extract{Items: "response.body"})
	other.Account = "other"
	if _, err := e.Run(context.Background(), other, start, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if own.n != 1 {
		t.Errorf("the account client sent %d requests, want 1", own.n)
	}
}
