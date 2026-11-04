package selfobs

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/kentrow/tapyr/internal/clock"
)

type stringer string

func (s stringer) String() string { return string(s) }

func TestRedactor(t *testing.T) {
	r := NewRedactor()
	if r.Redact("nothing registered") != "nothing registered" {
		t.Error("redacted without values")
	}
	if r.Add("abc") {
		t.Error("a 3-character value was accepted")
	}
	for _, v := range []string{"s3cr3t", "s3cr3t-and-more", "s3cr3t"} {
		if !r.Add(v) {
			t.Errorf("Add(%q) refused", v)
		}
	}
	tests := map[string]string{
		"key=s3cr3t":           "key=[REDACTED]",
		"key=s3cr3t-and-more!": "key=[REDACTED]!",
		"s3cr3ts3cr3t":         "[REDACTED][REDACTED]",
		"abc is not redacted":  "abc is not redacted",
		"":                     "",
	}
	for in, want := range tests {
		if got := r.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
	for name, want := range map[string]bool{
		"Authorization": true, "cookie": true, "Set-Cookie": true, "X-Cisco-Meraki-API-Key": true, "x-auth-token": true,
		"Proxy-Authorization": true, "Content-Type": false, "X-Request-Id": false, "key": false,
	} {
		if got := RedactedHeader(name); got != want {
			t.Errorf("RedactedHeader(%q) = %v", name, got)
		}
	}
}

func TestRedactingLogger(t *testing.T) {
	r := NewRedactor()
	r.Add("hunter22")
	var buf bytes.Buffer
	logger := NewLogger(&buf, LogOptions{Clock: clock.NewFake(at), Redactor: r})
	logger.With("preset", "pre-hunter22").WithGroup("g").Info("login with hunter22",
		"plain", "hunter22",
		"err", fmt.Errorf("wrapped: %w", errors.New("token hunter22 rejected")),
		"header", http.Header{"Authorization": {"Bearer x"}, "Accept": {"hunter22"}},
		"Authorization", "Bearer anything",
		"x-api-key", 42,
		"stringer", stringer("s=hunter22"),
		"map", map[string]string{"k": "hunter22"},
		"clean", map[string]int{"n": 1},
		slog.Group("nested", "inner", "hunter22", "count", 3),
	)
	out := buf.String()
	if strings.Contains(out, "hunter22") || strings.Contains(out, "Bearer") {
		t.Errorf("a secret leaked:\n%s", out)
	}
	for _, s := range []string{
		`"msg":"login with [REDACTED]"`, `"preset":"pre-[REDACTED]"`, `"plain":"[REDACTED]"`,
		`"err":"wrapped: token [REDACTED] rejected"`, `"Authorization":["[REDACTED]"]`, `"Accept":["[REDACTED]"]`,
		`"Authorization":"[REDACTED]"`, `"x-api-key":"[REDACTED]"`, `"stringer":"s=[REDACTED]"`,
		`"map":"map[k:[REDACTED]]"`, `"clean":{"n":1}`, `"nested":{"inner":"[REDACTED]","count":3}`,
	} {
		if !strings.Contains(out, s) {
			t.Errorf("%s missing from:\n%s", s, out)
		}
	}
}

func TestRedactorConcurrency(t *testing.T) {
	r := NewRedactor()
	logger := NewLogger(&syncWriter{}, LogOptions{Redactor: r})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 50 {
				r.Add(fmt.Sprintf("token-%d-%d", i, j))
				logger.Info("using token-0-0", "value", fmt.Sprintf("token-%d-%d", i, j))
			}
		})
	}
	wg.Wait()
	if got := r.Redact("token-7-49"); got != Redacted {
		t.Errorf("Redact = %q after concurrent adds", got)
	}
}

type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}
