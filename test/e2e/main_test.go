//go:build e2e

// Package e2e runs tapyr against real provider platforms. Every test is
// skipped unless its TAPYR_E2E_* credentials are set, and spends at most a
// few dozen requests. Run them with make e2e.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tapyr-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "tapyr")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", binary, "../../cmd/tapyr")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building tapyr:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// credentials returns the values of environment variables, or skips the
// test when one is missing.
func credentials(t *testing.T, names ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, n := range names {
		v := os.Getenv(n)
		if v == "" {
			t.Skipf("%s is not set", n)
		}
		out[n] = v
	}
	return out
}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// run starts tapyr run with a configuration until the test ends, and
// returns the address of its server. The logs must never hold a secret.
func run(t *testing.T, config string, secrets map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	config += fmt.Sprintf(`---
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: prom }
spec: { type: prometheus }
---
apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: e2e }
spec:
  server: { listen: %q }
  state: { path: %q }
  collection: { jitter: 0%% }
  log: { format: text }
`, addr, filepath.Join(dir, "tapyr.db"))
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &logBuffer{}
	cmd := exec.CommandContext(context.Background(), binary, "run", "--config", filepath.Join(dir, "config.yaml"))
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		out := logs.String()
		for name, v := range secrets {
			if strings.Contains(out, v) {
				t.Errorf("the logs hold the value of %s", name)
			}
		}
		if t.Failed() {
			t.Logf("tapyr run:\n%s", out)
		}
	})
	return addr
}

func get(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}

// waitFor polls an exposition until every pattern matches.
func waitFor(t *testing.T, url string, timeout time.Duration, patterns ...string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		body := get(t, url)
		missing := ""
		for _, p := range patterns {
			if !regexp.MustCompile(p).MatchString(body) {
				missing = p
				break
			}
		}
		if missing == "" {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s does not match %s:\n%s", url, missing, body)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
