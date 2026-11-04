package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/kentrow/tapyr/internal/clock"
)

// flakyPackage is a provider package with OAuth2 tokens whose simulator
// injects 503 and 429 responses.
var flakyPackage = map[string]string{
	"provider.yaml": `apiVersion: tapyr/v1alpha1
kind: Provider
metadata: { name: flaky-sim, version: 0.1.0 }
spec:
  baseURL: '{{ .params.baseURL | default "http://127.0.0.1:1/api" }}'
  params: [{ name: baseURL }]
  secrets: [{ name: clientSecret, required: true }]
  auth:
    type: oauth2_client_credentials
    oauth2ClientCredentials:
      tokenURL: '{{ .params.baseURL | default "http://127.0.0.1:1/api" }}/oauth/token'
      clientId: tapyr
      clientSecret: { secret: clientSecret }
  limits:
    - { name: daily, kind: quota, max: 5000, window: { period: day }, scope: { by: [account] } }
`,
	"templates/items.yaml": `apiVersion: tapyr/v1alpha1
kind: JobTemplate
metadata: { name: flaky-sim-items }
spec:
  provider: flaky-sim
  defaultInterval: 1m
  request: { path: /items }
  extract:
    items: response.body
    metrics: [{ name: flaky_sim_item_up, value: "1.0", labels: { id: item.id } }]
`,
	"templates/status.yaml": `apiVersion: tapyr/v1alpha1
kind: JobTemplate
metadata: { name: flaky-sim-status }
spec:
  provider: flaky-sim
  defaultInterval: 2m
  request: { path: /status }
  extract:
    items: "[response.body]"
    metrics: [{ name: flaky_sim_status_up, value: "1.0" }]
`,
	"sim/sim.yaml": `apiVersion: tapyr/v1alpha1
kind: Simulation
metadata: { name: flaky-sim }
spec:
  basePath: /api
  auth:
    accept: { clients: [{ clientId: tapyr, clientSecret: sim-secret }] }
    tokenLifetime: 10m
  routes:
    - { path: /items, response: { file: items.json } }
    - { path: /status, response: { file: status.json } }
`,
	"sim/items.json":  `[{"id": "a"}, {"id": "b"}]`,
	"sim/status.json": `{"ok": true}`,
	"sim/scenarios/flaky.yaml": `apiVersion: tapyr/v1alpha1
kind: Scenario
metadata: { name: flaky }
spec:
  steps:
    - { at: 0s, errors: { routes: ["/items"], status: 503, ratio: 0.3 } }
    - { at: 0s, errors: { routes: ["/status"], status: 429, ratio: 0.25 } }
`,
}

// TestSelfMetricsMatchSimulator is the self-metrics part of the M4
// acceptance: after 40 simulated minutes against a simulator injecting 503
// and 429 responses, the self-metrics on /metrics/self count exactly what
// the simulator counted.
func TestSelfMetricsMatchSimulator(t *testing.T) {
	dir := t.TempDir()
	writePackage(t, filepath.Join(dir, "providers", "flaky-sim"), flakyPackage)
	start := time.Date(2026, time.November, 2, 9, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	simAddr, stopSim := backgroundWith(t, nil, clk, "sim", "--provider", "flaky-sim", "--scenario", "flaky",
		"--provider-path", filepath.Join(dir, "providers"))

	config := fmt.Sprintf(`apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: acme }
spec:
  provider: flaky-sim
  params: { baseURL: "http://%s/api" }
  secrets: { clientSecret: { env: TAPYR_ACME_CLIENT_SECRET } }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: items }
spec: { account: acme, template: flaky-sim-items }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: status }
spec: { account: acme, template: flaky-sim-status }
---
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: prom }
spec: { type: prometheus }
---
apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: flaky }
spec:
  server: { listen: "127.0.0.1:0" }
  state: { path: %q }
  providerPaths: [%q]
  collection: { jitter: 0%% }
  retry: { initialBackoff: 1s, maxBackoff: 2s }
  log: { format: text }
`, simAddr, filepath.Join(dir, "tapyr.db"), filepath.Join(dir, "providers"))
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var inFlight, instances atomic.Int64
	addr, stopRun := backgroundHooks(t, map[string]string{"TAPYR_ACME_CLIENT_SECRET": "sim-secret"}, &env{
		clock:       clk,
		onRun:       func(d int) { inFlight.Add(int64(d)) },
		onInstances: func(n int) { instances.Store(int64(n)) },
	}, "run", "--config", filepath.Join(dir, "config.yaml"))
	for {
		// Idle: every instance loop and the flush and replan tickers wait on
		// the clock, and so does every run in flight: its deadline timer and
		// its sleep before a retry (or its wait for a penalty).
		deadline := time.Now().Add(10 * time.Second)
		for int64(clk.Waiters()) != instances.Load()+2+2*inFlight.Load() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Microsecond)
		}
		if time.Now().After(deadline) {
			t.Fatalf("collector not idle at %s: %d runs in flight, %d timers", clk.Now().Sub(start), inFlight.Load(), clk.Waiters())
		}
		if !clk.Now().Before(start.Add(40*time.Minute)) && inFlight.Load() == 0 {
			break
		}
		clk.Advance(time.Second)
	}

	for _, path := range []string{"/healthz", "/readyz"} {
		if code, body := httpGet(t, "http://"+addr+path); code != 200 {
			t.Errorf("%s = %d: %s", path, code, body)
		}
	}
	_, body := httpGet(t, "http://"+addr+"/metrics/self")
	self := parseExposition(t, body)
	_, body = httpGet(t, "http://"+simAddr+"/_sim/status")
	var status struct {
		Stats struct {
			Requests int64            `json:"requests"`
			Refused  map[string]int64 `json:"rate_limited"`
			Injected int64            `json:"injected_errors"`
			Tokens   int64            `json:"tokens_issued"`
			Routes   map[string]int64 `json:"routes"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatal(err)
	}
	st := status.Stats
	if st.Injected == 0 || st.Tokens == 0 {
		t.Fatalf("the scenario injected nothing: %+v", st)
	}
	refused := int64(0)
	for _, n := range st.Refused {
		refused += n
	}
	injected429 := self.sum("tapyr_http_requests_total", "code", "429")
	checks := []struct {
		name      string
		got, want float64
	}{
		{"requests", self.sum("tapyr_http_requests_total"), float64(st.Requests)},
		{"items requests", self.sum("tapyr_http_requests_total", "endpoint", "/items"), float64(st.Routes["GET /items"])},
		{"status requests", self.sum("tapyr_http_requests_total", "endpoint", "/status"), float64(st.Routes["GET /status"])},
		{"token requests", self.sum("tapyr_http_requests_total", "endpoint", "/api/oauth/token"), float64(st.Routes["POST /api/oauth/token"])},
		{"injected errors", self.sum("tapyr_http_requests_total", "code", "503") + injected429, float64(st.Injected)},
		{"simulator refusals", float64(refused), 0},
		{"rate limited", self.sum("tapyr_budget_rate_limited_total"), injected429},
		{"tokens", self.sum("tapyr_auth_token_requests_total", "result", "success"), float64(st.Tokens)},
		{"duration observations", self.sum("tapyr_http_request_duration_seconds_count"), float64(st.Requests)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: self-metrics %v, simulator %v", c.name, c.got, c.want)
		}
	}
	if injected429 == 0 {
		t.Error("no 429 injected")
	}

	// Every run of these one-page jobs sends one request plus its retries.
	for _, job := range []string{"items", "status"} {
		runs := self.sum("tapyr_job_runs_total", "job", job)
		requests := float64(st.Routes["GET /"+job])
		if got := self.sum("tapyr_http_retries_total", "job", job); got != requests-runs {
			t.Errorf("%s: %v retries, the simulator served %v requests for %v runs", job, got, requests, runs)
		}
	}

	res := stopRun()
	if res.code != exitOK {
		t.Fatalf("run exited with %d:\n%s", res.code, tail(res.stderr, 20))
	}
	finished, failed := strings.Count(res.stderr, `msg="run finished"`), strings.Count(res.stderr, `msg="run failed"`)
	if got := self.sum("tapyr_job_runs_total", "result", "success"); got != float64(finished) {
		t.Errorf("%v successful runs in the self-metrics, %d in the logs", got, finished)
	}
	if got := self.sum("tapyr_job_runs_total") - self.sum("tapyr_job_runs_total", "result", "success"); got != float64(failed) {
		t.Errorf("%v failed runs in the self-metrics, %d in the logs", got, failed)
	}
	if stopSim().code != exitOK {
		t.Error("sim did not stop cleanly")
	}
}

func writePackage(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// exposition is a parsed text exposition.
type exposition map[string]*dto.MetricFamily

func parseExposition(t *testing.T, body string) exposition {
	t.Helper()
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parsing the exposition: %v\n%s", err, body)
	}
	return families
}

// sum adds the values of the series of a metric matching label pairs. The
// _count and _sum of histograms are addressed by their suffixed names.
func (e exposition) sum(name string, labelPairs ...string) float64 {
	base, suffix := name, ""
	for _, s := range []string{"_count", "_sum"} {
		if b, ok := strings.CutSuffix(name, s); ok && e[b] != nil {
			base, suffix = b, s
		}
	}
	mf := e[base]
	if mf == nil {
		return 0
	}
	total := 0.0
next:
	for _, m := range mf.Metric {
		labels := map[string]string{}
		for _, l := range m.Label {
			labels[l.GetName()] = l.GetValue()
		}
		for i := 0; i+1 < len(labelPairs); i += 2 {
			if labels[labelPairs[i]] != labelPairs[i+1] {
				continue next
			}
		}
		switch {
		case suffix == "_count" && m.Histogram != nil:
			total += float64(m.Histogram.GetSampleCount())
		case suffix == "_sum" && m.Histogram != nil:
			total += m.Histogram.GetSampleSum()
		case m.Counter != nil:
			total += m.Counter.GetValue()
		case m.Gauge != nil:
			total += m.Gauge.GetValue()
		}
	}
	return total
}
