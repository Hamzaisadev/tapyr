package main

import (
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAccountTLS runs tapyr run against an HTTPS API with a certificate the
// system does not trust: the account trusts it with caFile, or skips the
// verification with insecureSkipVerify (logged); without either, the runs
// fail on the certificate.
func TestAccountTLS(t *testing.T) {
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id": "a"}]`)
	}))
	defer api.Close()
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tls string
		ok        bool
		log       string
	}{
		{name: "caFile", tls: fmt.Sprintf("tls: { caFile: %q }", caFile), ok: true},
		{name: "insecure", tls: "tls: { insecureSkipVerify: true }", ok: true, log: "TLS certificate verification is disabled"},
		{name: "untrusted", ok: false, log: "certificate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub := t.TempDir()
			config := fmt.Sprintf(`apiVersion: tapyr/v1alpha1
kind: Provider
metadata: { name: secure, version: 0.1.0 }
spec:
  baseURL: %q
---
apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: secure }
spec:
  provider: secure
  %s
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: items }
spec:
  account: secure
  interval: 1m
  request: { path: /items }
  extract:
    items: response.body
    metrics: [{ name: secure_item_up, value: "1.0", labels: { id: item.id } }]
---
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: prom }
spec: { type: prometheus }
---
apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: secure }
spec:
  server: { listen: "127.0.0.1:0" }
  state: { path: %q }
  collection: { jitter: 0%% }
  log: { format: text }
`, api.URL, tc.tls, filepath.Join(sub, "tapyr.db"))
			if err := os.WriteFile(filepath.Join(sub, "config.yaml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			addr, stop := background(t, nil, "run", "--config", sub)
			wait := 10 * time.Second
			if !tc.ok {
				wait = 2 * time.Second // the first run fails at once
			}
			deadline := time.Now().Add(wait)
			var body string
			for time.Now().Before(deadline) {
				_, body = httpGet(t, "http://"+addr+"/metrics")
				if strings.Contains(body, `secure_item_up{id="a"} 1`) {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			res := stop()
			if got := strings.Contains(body, `secure_item_up{id="a"} 1`); got != tc.ok {
				t.Errorf("collected = %t, want %t\n%s", got, tc.ok, tail(res.stderr, 10))
			}
			if tc.log != "" && !strings.Contains(res.stderr, tc.log) {
				t.Errorf("the logs do not mention %q:\n%s", tc.log, tail(res.stderr, 10))
			}
		})
	}
}
