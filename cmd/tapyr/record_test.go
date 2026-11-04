package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kentrow/tapyr/internal/clock"
)

// TestRecord records a fan-out job of the example simulator: its parents
// run too, every page is written anonymized, with the quota headers only,
// and the report names the replaced paths.
func TestRecord(t *testing.T) {
	simAddr, stopSim := background(t, nil, "sim", "--provider", "example-sim")
	defer stopSim()
	dir := t.TempDir()
	config := fmt.Sprintf(`apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: lab }
spec:
  provider: example-sim
  params: { baseURL: "http://%s/api/v1" }
  secrets: { apiKey: { env: TAPYR_LAB_API_KEY } }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: network-devices }
spec: { account: lab, template: example-sim-network-devices }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: devices }
spec: { account: lab, template: example-sim-devices }
---
apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: record }
spec: { log: { format: text } }
`, simAddr)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(t.TempDir(), "anonymize.yaml") // outside the configuration
	if err := os.WriteFile(rules, []byte("keep: [\"[*].name\"]\nreplace: [{ path: \"[*].status\", as: redact }]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "recording")
	vars := map[string]string{"TAPYR_LAB_API_KEY": "sim-api-key"}
	res := executeWith(t, vars, clock.Real(), "record", "--config", dir, "--job", "network-devices", "--out", out, "--anonymize", rules)
	if res.code != 0 {
		t.Fatalf("exit %d\n%s%s", res.code, res.stdout, res.stderr)
	}
	index, err := os.ReadFile(filepath.Join(out, "index.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"job: lab.example-sim-sites", "job: lab.example-sim-networks", "job: network-devices",
		"path: /sites/{siteId}/networks/{networkId}/devices", "X-Ratelimit-Remaining-Day", "file: network-devices/001.json",
	} {
		if !strings.Contains(string(index), want) {
			t.Errorf("index.yaml does not contain %q:\n%s", want, index)
		}
	}
	if strings.Contains(string(index), "job: devices") {
		t.Error("an unselected job was recorded")
	}
	var all strings.Builder
	_ = filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(p)
			all.Write(data)
		}
		return nil
	})
	// Serials, identifiers and the API key are gone; kept paths stay.
	for _, original := range []string{"SIM-0101", "net-a1", "site-a", "sim-api-key", "Bearer"} {
		if strings.Contains(all.String(), original) {
			t.Errorf("%q is in the recording", original)
		}
	}
	page, _ := os.ReadFile(filepath.Join(out, "lab.example-sim-sites", "001.json"))
	if !strings.Contains(string(page), `"name": "`) || strings.Contains(string(page), `"name": "name-`) {
		t.Errorf("kept names were replaced:\n%s", page)
	}
	report, _ := os.ReadFile(filepath.Join(out, "report.txt"))
	for _, want := range []string{"[*].serial", "vars.networkId", "[*].status"} {
		if !strings.Contains(string(report), want) {
			t.Errorf("report.txt does not contain %q:\n%s", want, report)
		}
	}

	// Errors: unknown job, missing flags, unknown rule.
	if res := executeWith(t, vars, clock.Real(), "record", "--config", dir, "--job", "nope", "--out", out); res.code != exitFailure || !strings.Contains(res.stderr, `no job "nope"`) {
		t.Errorf("unknown job: exit %d\n%s", res.code, res.stderr)
	}
	if res := executeWith(t, vars, clock.Real(), "record", "--config", dir); res.code != exitFailure || !strings.Contains(res.stderr, "--job and --out are required") {
		t.Errorf("missing flags: exit %d\n%s", res.code, res.stderr)
	}
	if err := os.WriteFile(rules, []byte("replace: [{ path: x, as: nope }]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := executeWith(t, vars, clock.Real(), "record", "--config", dir, "--job", "devices", "--out", out, "--anonymize", rules); res.code != exitFailure || !strings.Contains(res.stderr, "unknown rule") {
		t.Errorf("unknown rule: exit %d\n%s", res.code, res.stderr)
	}
}
