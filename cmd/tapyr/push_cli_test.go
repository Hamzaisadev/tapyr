package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/snappy"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
)

// TestRunPushOutputs runs tapyr run against the simulator with a remote
// write and an OTLP output: the samples reach both receivers, with the
// credentials of the Output documents, and the OTLP service.instance.id is
// kept across restarts.
func TestRunPushOutputs(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var headers []http.Header
	received := make(chan struct{}, 100)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		raw, err := snappy.Decode(nil, data)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		bodies, headers = append(bodies, raw), append(headers, r.Header.Clone())
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		received <- struct{}{}
	}))
	defer receiver.Close()
	var instanceIDs []string
	otlpReceived := make(chan struct{}, 100)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		req := pmetricotlp.NewExportRequest()
		if err := req.UnmarshalProto(data); err != nil || r.Header.Get("api-key") != "otel-key" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rms := req.Metrics().ResourceMetrics()
		mu.Lock()
		for i := range rms.Len() {
			if id, ok := rms.At(i).Resource().Attributes().Get("service.instance.id"); ok {
				instanceIDs = append(instanceIDs, id.AsString())
			}
		}
		mu.Unlock()
		otlpReceived <- struct{}{}
	}))
	defer collector.Close()
	simAddr, stopSim := background(t, nil, "sim", "--provider", "example-sim")
	defer stopSim()

	dir := t.TempDir()
	config := fmt.Sprintf(`apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: fleet }
spec:
  provider: example-sim
  params: { baseURL: "http://%s/api/v1" }
  secrets: { apiKey: { env: TAPYR_FLEET_API_KEY } }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: devices }
spec: { account: fleet, template: example-sim-devices }
---
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: vm }
spec:
  type: remote_write
  remoteWrite:
    url: %q
    basicAuth: { username: { env: TAPYR_VM_USER }, password: { env: TAPYR_VM_PASSWORD } }
  queue: { flushInterval: 100ms }
  externalLabels: { tapyr_instance: e2e }
---
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: otel }
spec:
  type: otlp
  otlp: { endpoint: %q, headers: { api-key: { env: TAPYR_OTEL_KEY } } }
  queue: { flushInterval: 100ms }
---
apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: rw }
spec:
  server: { listen: "127.0.0.1:0" }
  collection: { jitter: 0%% }
  selfMetrics: { outputs: [vm], interval: 200ms }
  state: { path: %q }
  log: { format: text }
`, simAddr, receiver.URL, collector.URL, filepath.Join(dir, "tapyr.db"))
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{
		"TAPYR_FLEET_API_KEY": "sim-api-key", "TAPYR_VM_USER": "tapyr", "TAPYR_VM_PASSWORD": "vm-password", "TAPYR_OTEL_KEY": "otel-key",
	}
	_, stop := background(t, vars, "run", "--config", dir)
	deadline := time.After(10 * time.Second)
	// Wait for the collected samples and for the pushed self-metrics.
	for samples, self := false, false; !samples || !self; {
		select {
		case <-received:
		case <-deadline:
			t.Fatalf("missing in remote write: samples %v, self-metrics %v", samples, self)
		}
		mu.Lock()
		for _, b := range bodies {
			samples = samples || (bytes.Contains(b, []byte("example_device_up")) && bytes.Contains(b, []byte("tapyr_instance")))
			self = self || bytes.Contains(b, []byte("tapyr_build_info"))
		}
		mu.Unlock()
	}
	select {
	case <-otlpReceived:
	case <-time.After(10 * time.Second):
		t.Fatal("no OTLP export received")
	}
	if res := stop(); res.code != exitOK {
		t.Fatalf("run exited with %d:\n%s", res.code, tail(res.stderr, 20))
	}
	// After a restart, the instance keeps its identifier.
	_, stop = background(t, vars, "run", "--config", dir)
	select {
	case <-otlpReceived:
	case <-time.After(10 * time.Second):
		t.Fatal("no OTLP export received after the restart")
	}
	if res := stop(); res.code != exitOK {
		t.Fatalf("run exited with %d:\n%s", res.code, tail(res.stderr, 20))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(instanceIDs) < 2 || len(instanceIDs[0]) != 32 || instanceIDs[0] != instanceIDs[len(instanceIDs)-1] {
		t.Errorf("service.instance.id = %v", instanceIDs)
	}
	user, pass, ok := (&http.Request{Header: headers[0]}).BasicAuth()
	if !ok || user != "tapyr" || pass != "vm-password" || headers[0].Get("User-Agent") == "" {
		t.Errorf("headers = %v", headers[0])
	}
}
