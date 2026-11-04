package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/kentrow/tapyr/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestPipelineAgainstSimulator checks the pipeline end to end: tapyr run against
// tapyr sim --provider example-sim exposes the expected metrics.
func TestPipelineAgainstSimulator(t *testing.T) {
	simAddr, stopSim := background(t, nil, "sim", "--provider", "example-sim")

	dir := t.TempDir()
	config := fmt.Sprintf(`apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: e2e }
spec:
  provider: example-sim
  params: { baseURL: "http://%s/api/v1" }
  secrets: { apiKey: { env: TAPYR_E2E_API_KEY } }
  labels: { tenant: e2e }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: e2e-devices }
spec: { account: e2e, template: example-sim-devices, labels: { env: test } }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: e2e-sites }
spec: { account: e2e, template: example-sim-sites }
---
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: prom }
spec: { type: prometheus }
---
apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: e2e }
spec:
  server: { listen: "127.0.0.1:0" }
  state: { path: %q }
  collection: { jitter: 0%% }
`, simAddr, filepath.Join(dir, "tapyr.db"))
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, stopRun := background(t, map[string]string{"TAPYR_E2E_API_KEY": "sim-api-key"}, "run", "--config", dir, "--log-format", "text")

	// With no jitter, both jobs run at startup.
	var body string
	deadline := time.Now().Add(10 * time.Second)
	for {
		var code int
		code, body = httpGet(t, "http://"+addr+"/metrics")
		if code == http.StatusOK && strings.Contains(body, "example_devices") && strings.Contains(body, "example_site_info") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("metrics not exposed in time (status %d):\n%s", code, body)
		}
		time.Sleep(20 * time.Millisecond)
	}

	golden := filepath.Join("testdata", "pipeline-metrics.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("reading golden file (run go test -update): %v", err)
		}
		if diff := cmp.Diff(string(want), body); diff != "" {
			t.Errorf("/metrics (-want +got):\n%s", diff)
		}
	}

	res := stopRun()
	if res.code != exitOK {
		t.Errorf("run exited with %d:\n%s", res.code, res.stderr)
	}
	for _, want := range []string{
		`msg="collector started"`,
		`msg="run finished" component=run provider=example-sim account=e2e job=e2e-devices`,
		"requests=3 retries=0 pages=3 items=5 samples=16 deduplicated=0 mapping_errors=0",
		`msg="collector stopped"`,
	} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("logs do not contain %q:\n%s", want, res.stderr)
		}
	}
	if strings.Contains(res.stderr, "sim-api-key") {
		t.Error("the API key appears in the logs")
	}

	// The run history and the ledger are persisted.
	store, err := state.OpenSQLite(context.Background(), filepath.Join(dir, "tapyr.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runs, err := store.Runs(context.Background(), "e2e-devices", "e2e-devices", 10)
	if err != nil || len(runs) == 0 || runs[0].Result != "success" || runs[0].Pages != 3 || runs[0].Samples != 16 {
		t.Errorf("run history = %+v, %v", runs, err)
	}
	ledger, err := store.LoadLedger(context.Background())
	if err != nil || len(ledger) != 1 || ledger[0].Limit != "example-sim/daily" || ledger[0].Used != 5 || ledger[0].ObservedMax != 5000 {
		t.Errorf("ledger = %+v, %v", ledger, err)
	}
	if salt, ok, _ := store.Meta(context.Background(), "credential_salt"); !ok || len(salt) != 64 {
		t.Errorf("credential salt = %q", salt)
	}
	if res := stopSim(); res.code != exitOK {
		t.Errorf("sim exited with %d:\n%s", res.code, res.stderr)
	}
}
