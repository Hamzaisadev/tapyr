package collect

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/runtime"
)

// keyAuth sends a custom credentials header, the kind Go keeps on
// redirects to other hosts.
type keyAuth struct{}

func (keyAuth) Decorate(_ context.Context, req *http.Request) error {
	req.Header.Set("X-Key", "key-1234")
	return nil
}

func (keyAuth) OnAuthFailure(context.Context, *http.Response) (bool, error) { return false, nil }

// seen records the requests of a server.
type seen struct {
	mu   sync.Mutex
	reqs []string // method path key body
}

func (s *seen) add(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+r.Header.Get("X-Key")+" "+string(body)))
}

func (s *seen) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

// TestRedirects: a redirect is followed within the origin, or to a host
// the provider allows, as a request of its own (a reservation and the
// credentials each); a redirect to another host is refused, and that host
// receives nothing, credentials included.
func TestRedirects(t *testing.T) {
	var other seen
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other.add(r)
		_, _ = w.Write([]byte(`[{"serial": "B"}]`))
	}))
	defer b.Close()
	// The other server under another host name than the API (127.0.0.1).
	otherURL := strings.Replace(b.URL, "127.0.0.1", "localhost", 1)

	var api seen
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.add(r)
		switch r.URL.Path {
		case "/devices":
			http.Redirect(w, r, "/v2/devices", http.StatusMovedPermanently)
		case "/v2/devices":
			_, _ = w.Write([]byte(`[{"serial": "A"}]`))
		case "/away":
			http.Redirect(w, r, otherURL+"/devices", http.StatusFound)
		case "/post":
			http.Redirect(w, r, otherURL+"/created", http.StatusTemporaryRedirect)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/insecure":
			http.Redirect(w, r, "ftp://localhost/devices", http.StatusFound)
		}
	}))
	defer a.Close()
	extract := &config.Extract{Items: "response.body", Metrics: []config.Metric{{Name: "device_up", Value: "1.0", Labels: map[string]config.Expr{"serial": "item.serial"}}}}

	tests := []struct {
		name      string
		path      string
		method    string
		allowed   []string
		wantErr   string
		wantAPI   int
		wantOther []string
		reserved  int
	}{
		{name: "within the origin", path: "/devices", wantAPI: 2, reserved: 2},
		{name: "to another host", path: "/away", wantErr: "redirected to localhost:", wantAPI: 1, reserved: 1},
		{name: "to an allowed host", path: "/away", allowed: []string{"localhost"}, wantAPI: 1, wantOther: []string{"GET /devices key-1234"}, reserved: 2},
		{name: "to a host allowed by a pattern", path: "/away", allowed: []string{"*.example.com", "localhost"}, wantAPI: 1, wantOther: []string{"GET /devices key-1234"}, reserved: 2},
		{name: "307 keeps the method and the body", path: "/post", method: http.MethodPost, allowed: []string{"localhost"}, wantAPI: 1, wantOther: []string{`POST /created key-1234 {"q":"1"}`}, reserved: 2},
		{name: "loop", path: "/loop", wantErr: "more than 5 redirects", wantAPI: 6, reserved: 6},
		{name: "to another scheme", path: "/insecure", allowed: []string{"localhost"}, wantErr: "refusing a redirect from http to ftp", wantAPI: 1, reserved: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api.reqs, other.reqs = nil, nil
			job := newJob(t, a.URL, extract)
			job.Request.Path = tt.path
			job.Auth = keyAuth{}
			job.RedirectHosts = tt.allowed
			if tt.method != "" {
				job.Request.Method, job.Request.Idempotent = tt.method, true
				job.Request.Body = []runtime.Param{{Name: "q", Literal: "1"}}
			}
			rec := &recorder{}
			_, err := newExecutor(rec).Run(context.Background(), job, start, time.Time{})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Run() = %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Run() = %v, want %q", err, tt.wantErr)
			}
			if tt.wantErr != "" {
				if ce, ok := errors.AsType[*Error](err); !ok || ce.Class != ClassClient {
					t.Errorf("error %v is not a client error", err)
				}
			}
			if n := len(api.list()); n != tt.wantAPI {
				t.Errorf("the API received %d requests, want %d: %v", n, tt.wantAPI, api.list())
			}
			if got := other.list(); strings.Join(got, "|") != strings.Join(tt.wantOther, "|") {
				t.Errorf("the other host received %q, want %q", got, tt.wantOther)
			}
			if len(rec.reserved) != tt.reserved {
				t.Errorf("%d reservations, want %d", len(rec.reserved), tt.reserved)
			}
		})
	}
}

// TestHostAllowed matches host names and "*." patterns, case-insensitively.
func TestHostAllowed(t *testing.T) {
	for _, tt := range []struct {
		host string
		want bool
	}{
		{"n123.meraki.com", true},
		{"N123.Meraki.com", true},
		{"meraki.com", false},
		{"evil-meraki.com", false},
		{"api.example.org", true},
		{"x.api.example.org", false},
	} {
		if got := hostAllowed(tt.host, []string{"*.meraki.com", "api.example.org"}); got != tt.want {
			t.Errorf("hostAllowed(%s) = %v", tt.host, got)
		}
	}
}
