package budget

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/config"
)

var epoch = time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)

const waitTimeout = 5 * time.Second

// newEngine builds an engine from a provider limits block (YAML) and
// accounts.
func newEngine(t *testing.T, clk clock.Clock, limits string, extra string) *Engine {
	t.Helper()
	doc := `apiVersion: tapyr/v1alpha1
kind: Provider
metadata: { name: p, version: 0.1.0 }
spec:
  baseURL: https://api.example.invalid
  limits:
` + limits + `
---
apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: a }
spec: { provider: p }
---
apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: b }
spec: { provider: p, budget: { share: 0.5 } }
` + extra
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := config.NewLoader()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := l.Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(Config{
		Providers: cfg.Providers, Accounts: cfg.Accounts, RetryReserve: 0.1,
		Breaker: cfg.Settings.Spec.CircuitBreaker,
	}, clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func desc(account string) Descriptor {
	return Descriptor{Provider: "p", Account: account, Endpoint: "/devices", Method: http.MethodGet, Priority: config.PriorityNormal}
}

func reserve(t *testing.T, e *Engine, d Descriptor) Reservation {
	t.Helper()
	r, err := e.Reserve(context.Background(), d)
	if err != nil {
		t.Fatalf("Reserve(%+v) = %v", d, err)
	}
	return r
}

// refusal asserts a refusal and returns it (as a value, so callers need not
// check it).
func refusal(t *testing.T, e *Engine, d Descriptor, reason string) RefusalError {
	t.Helper()
	_, err := e.Reserve(context.Background(), d)
	re, ok := errors.AsType[*RefusalError](err)
	if !ok || re.Reason != reason {
		t.Fatalf("Reserve() error = %v, want refusal %s", err, reason)
	}
	return *re
}

// async reserves in a goroutine; the channel receives once granted.
func async(ctx context.Context, e *Engine, d Descriptor) <-chan error {
	ch := make(chan error, 1)
	go func() {
		r, err := e.Reserve(ctx, d)
		if err == nil {
			r.Done(Outcome{Status: 200})
		}
		ch <- err
	}()
	return ch
}

func pending(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("reservation completed early: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
}

func completed(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(waitTimeout):
		t.Fatal("reservation did not complete")
		return nil
	}
}

func TestApplicabilityAndScopes(t *testing.T) {
	l := &limit{
		scope:     []string{"account", "var:orgId", "endpoint"},
		endpoints: []string{"/orgs/**"}, methods: []string{"GET"},
		requestsWithVar: "orgId", authRequests: "exclude",
	}
	d := Descriptor{Account: "acme", Endpoint: "/orgs/{orgId}/devices", Method: "GET", Vars: map[string]string{"orgId": "42"}}
	if !l.applies(&d) {
		t.Fatal("limit does not apply")
	}
	if got := l.scopeKey(&d); got != "account=acme,orgId=42,endpoint=/orgs/{orgId}/devices" {
		t.Errorf("scope key = %q", got)
	}
	for name, mutate := range map[string]func(*Descriptor){
		"other endpoint": func(d *Descriptor) { d.Endpoint = "/networks" },
		"other method":   func(d *Descriptor) { d.Method = "POST" },
		"missing var":    func(d *Descriptor) { d.Vars = nil },
		"auth request":   func(d *Descriptor) { d.AuthRequest = true },
	} {
		d2 := d
		mutate(&d2)
		if l.applies(&d2) {
			t.Errorf("%s: limit applies", name)
		}
	}
	only := &limit{scope: []string{"instance"}, authRequests: "only"}
	if only.applies(&Descriptor{}) || !only.applies(&Descriptor{AuthRequest: true}) {
		t.Error("authRequests: only")
	}
	if (&limit{scope: []string{"credential"}}).scopeKey(&Descriptor{Credential: "abc"}) != "credential=abc" {
		t.Error("credential scope key")
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern, path string
		want          bool
	}{
		{"/organizations/*", "/organizations/{organizationId}", true},
		{"/organizations/*", "/organizations/{organizationId}/networks", false},
		{"/organizations/**", "/organizations/{organizationId}/networks", true},
		{"/organizations/**", "/organizations", true},
		{"/**/devices", "/orgs/{id}/devices", true},
		{"/v*/devices", "/v1/devices", true},
		{"/devices", "/devices/", true},
		{"/devices", "/networks", false},
	}
	for _, tt := range tests {
		if got := globMatch(tt.pattern, tt.path); got != tt.want {
			t.Errorf("globMatch(%q, %q) = %v", tt.pattern, tt.path, got)
		}
	}
}

func TestRateLimit(t *testing.T) {
	clk := clock.NewFake(epoch)
	// A burst of 3: two requests at once, the third token kept in hand.
	e := newEngine(t, clk, "    - { name: rate, kind: rate, rate: 2/s, burst: 3, scope: { by: [account] } }", "")
	reserve(t, e, desc("a")).Done(Outcome{})
	reserve(t, e, desc("a")).Done(Outcome{})
	// Another account has its own bucket.
	reserve(t, e, desc("b")).Done(Outcome{})

	ch := async(context.Background(), e, desc("a"))
	pending(t, ch)
	clk.Advance(400 * time.Millisecond)
	pending(t, ch)
	clk.Advance(100 * time.Millisecond)
	if err := completed(t, ch); err != nil {
		t.Fatal(err)
	}

	// A token that comes after the deadline is refused at once.
	d := desc("a")
	d.Deadline = clk.Now().Add(100 * time.Millisecond)
	re := refusal(t, e, d, ReasonDeadline)
	if re.Limit != "p/rate" || re.Scope != "account=a" {
		t.Errorf("refusal = %+v", re)
	}
}

// TestRateHeadroom: with a burst of 2 or more, requests keep a token in the
// bucket, so that the bucket of the provider, of the same size, absorbs
// arrivals made early by the latency; the enforcer applies the full burst.
func TestRateHeadroom(t *testing.T) {
	for _, tc := range []struct {
		burst int
		now   int // requests admitted at once
	}{{1, 1}, {2, 1}, {4, 3}} {
		t.Run(fmt.Sprintf("burst %d", tc.burst), func(t *testing.T) {
			limits := fmt.Sprintf("    - { name: rate, kind: rate, rate: 2/s, burst: %d }", tc.burst)
			clk := clock.NewFake(epoch)
			e := newEngine(t, clk, limits, "")
			for range tc.now {
				reserve(t, e, desc("a")).Done(Outcome{})
			}
			ch := async(context.Background(), e, desc("a"))
			pending(t, ch)
			clk.Advance(500 * time.Millisecond)
			if err := completed(t, ch); err != nil {
				t.Fatal(err)
			}

			enforcer := newEngine(t, clk, limits, "")
			for i := range tc.burst {
				if release, refused := enforcer.Check(desc("a")); refused != nil {
					t.Fatalf("Check %d refused: %v", i, refused)
				} else {
					release()
				}
			}
			if _, refused := enforcer.Check(desc("a")); refused == nil {
				t.Error("Check beyond the burst allowed")
			}
		})
	}
}

func TestConcurrencyLimit(t *testing.T) {
	clk := clock.NewFake(epoch)
	e := newEngine(t, clk, "    - { name: conc, kind: concurrency, max: 2 }", "")
	r1 := reserve(t, e, desc("a"))
	r2 := reserve(t, e, desc("b"))
	ch := async(context.Background(), e, desc("a"))
	pending(t, ch)
	r1.Done(Outcome{Status: 200})
	r1.Done(Outcome{Status: 200}) // idempotent
	if err := completed(t, ch); err != nil {
		t.Fatal(err)
	}
	r2.Done(Outcome{})

	// A waiter whose context ends is refused with deadline.
	r3, r4 := reserve(t, e, desc("a")), reserve(t, e, desc("a"))
	ctx, cancel := context.WithCancel(context.Background())
	ch = async(ctx, e, desc("a"))
	pending(t, ch)
	cancel()
	err := completed(t, ch)
	if reason, ok := IsRefusal(err); !ok || reason != ReasonDeadline || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v", err)
	}
	r3.Done(Outcome{})
	r4.Done(Outcome{})
}

func TestQuotaWindowShareAndReserve(t *testing.T) {
	clk := clock.NewFake(epoch)
	// max 100: account a (share 1) may use 90 normal requests (10% reserve)
	// and 100 with retries or critical; account b (share 0.5) has its own
	// scope: 50 usable, 5 reserved.
	e := newEngine(t, clk, "    - { name: daily, kind: quota, max: 100, window: { period: day }, scope: { by: [account] } }", "")
	for range 90 {
		reserve(t, e, desc("a")).Done(Outcome{})
	}
	re := refusal(t, e, desc("a"), ReasonQuotaExhausted)
	if !re.Until.Equal(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("refusal until %v, want the window end", re.Until)
	}
	retry := desc("a")
	retry.Retry = true
	for range 5 {
		reserve(t, e, retry).Done(Outcome{})
	}
	critical := desc("a")
	critical.Priority = config.PriorityCritical
	for range 5 {
		reserve(t, e, critical).Done(Outcome{})
	}
	refusal(t, e, retry, ReasonQuotaExhausted)
	refusal(t, e, critical, ReasonQuotaExhausted)

	for range 45 {
		reserve(t, e, desc("b")).Done(Outcome{})
	}
	refusal(t, e, desc("b"), ReasonQuotaExhausted)

	// The next window starts afresh.
	clk.Set(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	reserve(t, e, desc("a")).Done(Outcome{})
	q := e.Quotas()
	if len(q) != 2 || q[0].Used != 1 || q[0].Usable != 100 || q[0].Reserve != 10 || q[1].Usable != 50 || q[1].Used != 0 {
		t.Errorf("quotas = %+v", q)
	}
}

func TestSharedScopeUsesSmallestShare(t *testing.T) {
	clk := clock.NewFake(epoch)
	e := newEngine(t, clk, "    - { name: shared, kind: quota, max: 20, window: { period: day }, scope: { by: [instance] } }", "")
	reserve(t, e, desc("a")).Done(Outcome{})
	reserve(t, e, desc("b")).Done(Outcome{})
	// share 0.5 -> usable 10, reserve 1: 9 normal requests in total.
	for range 7 {
		reserve(t, e, desc("a")).Done(Outcome{})
	}
	refusal(t, e, desc("a"), ReasonQuotaExhausted)
}

func TestRollingQuota(t *testing.T) {
	clk := clock.NewFake(epoch)
	e := newEngine(t, clk, "    - { name: hourly, kind: quota, max: 10, window: { rolling: 1h } }", "")
	for range 9 {
		reserve(t, e, desc("a")).Done(Outcome{})
	}
	re := refusal(t, e, desc("a"), ReasonQuotaExhausted)
	if !re.Until.Equal(epoch.Add(time.Hour)) {
		t.Errorf("until = %v", re.Until)
	}
	clk.Advance(time.Hour)
	reserve(t, e, desc("a")).Done(Outcome{})
}

func TestHeaderSync(t *testing.T) {
	clk := clock.NewFake(epoch)
	e := newEngine(t, clk, `    - name: daily
      kind: quota
      max: auto
      initialMax: 50
      window: { period: day }
      scope: { by: [account] }
      sync: { limitHeader: X-Limit, remainingHeader: X-Remaining, resetHeader: X-Reset }`, "")
	if q := e.Quotas(); len(q) != 0 {
		t.Fatalf("quotas before any request: %+v", q)
	}
	r := reserve(t, e, desc("a"))
	r.Done(Outcome{Status: 200, Header: http.Header{"X-Limit": {"1000"}, "X-Remaining": {"400"}}})
	q := e.Quotas()[0]
	// 1 used by Tapyr, 600 consumed in total: 599 by other clients.
	if q.Max != 1000 || q.Usable != 1000 || q.Used != 1 || q.Remaining != 400 || q.ExternalUsage != 599 {
		t.Fatalf("after sync: %+v", q)
	}
	// Remaining decreases with Tapyr's own use until the next observation.
	for range 10 {
		reserve(t, e, desc("a")).Done(Outcome{})
	}
	if q := e.Quotas()[0]; q.Remaining != 390 || q.ExternalUsage != 599 {
		t.Fatalf("after 10 more: %+v", q)
	}
	// remaining = 0 exhausts the quota until the reset header.
	reserve(t, e, desc("a")).Done(Outcome{Status: 200, Header: http.Header{"X-Remaining": {"0"}, "X-Reset": {"3600"}}})
	re := refusal(t, e, desc("a"), ReasonQuotaExhausted)
	if !re.Until.Equal(epoch.Add(time.Hour)) {
		t.Errorf("until = %v, want the reset header time", re.Until)
	}
	retry := desc("a")
	retry.Retry = true
	refusal(t, e, retry, ReasonQuotaExhausted)
	clk.Advance(time.Hour)
	reserve(t, e, desc("a")).Done(Outcome{})
}

func TestRateLimitedPenalties(t *testing.T) {
	clk := clock.NewFake(epoch)
	e := newEngine(t, clk, `    - { name: global, kind: rate, rate: 100/s, burst: 100 }
    - { name: per-account, kind: rate, rate: 100/s, burst: 100, scope: { by: [account] } }`, "")
	rec := &recorder{}
	e.observer = rec

	// Retry-After in seconds penalizes the narrowest scope only.
	reserve(t, e, desc("a")).Done(Outcome{Status: 429, Header: http.Header{"Retry-After": {"5"}}})
	d := desc("a")
	d.Deadline = clk.Now().Add(time.Second)
	re := refusal(t, e, d, ReasonPenalized)
	if re.Limit != "p/per-account" || !re.Until.Equal(epoch.Add(5*time.Second)) {
		t.Errorf("refusal = %+v", re)
	}
	reserve(t, e, desc("b")).Done(Outcome{Status: 200})
	ch := async(context.Background(), e, desc("a"))
	pending(t, ch)
	clk.Advance(5 * time.Second)
	if err := completed(t, ch); err != nil {
		t.Fatal(err)
	}

	// Without Retry-After, the default penalty (2s) grows on consecutive 429s.
	reserve(t, e, desc("b")).Done(Outcome{Status: 429})
	clk.Advance(2 * time.Second)
	reserve(t, e, desc("b")).Done(Outcome{Status: 429})
	re = refusal(t, e, Descriptor{Provider: "p", Account: "b", Endpoint: "/devices", Deadline: clk.Now().Add(3 * time.Second)}, ReasonPenalized)
	if !re.Until.Equal(clk.Now().Add(4 * time.Second)) {
		t.Errorf("second penalty until %v, want 4s", re.Until)
	}

	// An HTTP date works too.
	clk.Advance(time.Minute)
	reserve(t, e, desc("a")).Done(Outcome{Status: 429, Header: http.Header{"Retry-After": {clk.Now().Add(30 * time.Second).Format(http.TimeFormat)}}})
	re = refusal(t, e, Descriptor{Provider: "p", Account: "a", Endpoint: "/devices", Deadline: clk.Now().Add(time.Second)}, ReasonPenalized)
	if !re.Until.Equal(clk.Now().Add(30 * time.Second)) {
		t.Errorf("date penalty until %v", re.Until)
	}
	// Every 429 is counted, and none was predicted by a quota.
	limited, unexpected := 0, 0
	for _, ev := range rec.events {
		if strings.HasPrefix(ev, "rate_limited ") {
			limited++
			if strings.HasSuffix(ev, " true") {
				unexpected++
			}
		}
	}
	if limited != 4 || unexpected != 4 {
		t.Errorf("%d rate-limited responses, %d unexpected, want 4 and 4: %v", limited, unexpected, rec.events)
	}
}

func TestPenalizeAll(t *testing.T) {
	clk := clock.NewFake(epoch)
	e := newEngine(t, clk, `    - { name: global, kind: rate, rate: 100/s, burst: 100 }
    - { name: per-account, kind: rate, rate: 100/s, burst: 100, scope: { by: [account] } }
  errors: { rateLimited: { statuses: [429], penalize: all } }`, "")
	reserve(t, e, desc("a")).Done(Outcome{Status: 429, Header: http.Header{"Retry-After": {"10"}}})
	d := desc("b")
	d.Deadline = clk.Now().Add(time.Second)
	if re := refusal(t, e, d, ReasonPenalized); re.Limit != "p/global" {
		t.Errorf("refusal = %+v", re)
	}
}

func TestCircuitBreaker(t *testing.T) {
	clk := clock.NewFake(epoch)
	e := newEngine(t, clk, "    - { name: rate, kind: rate, rate: 100/s }", `---
apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: s }
spec: { circuitBreaker: { failureThreshold: 3, cooldown: 1m, maxCooldown: 3m } }
`)
	fail := Outcome{Transient: true}
	for range 2 {
		reserve(t, e, desc("a")).Done(fail)
	}
	reserve(t, e, desc("a")).Done(Outcome{Status: 404}) // not transient: resets the streak
	for range 3 {
		reserve(t, e, desc("a")).Done(fail)
	}
	if e.CircuitState("a", "") != CircuitOpen {
		t.Fatal("circuit not open")
	}
	refusal(t, e, desc("a"), ReasonCircuitOpen)
	reserve(t, e, desc("b")).Done(Outcome{})

	clk.Advance(time.Minute)
	if e.CircuitState("a", "") != CircuitHalfOpen {
		t.Fatal("circuit not half-open after the cooldown")
	}
	probe := reserve(t, e, desc("a"))
	refusal(t, e, desc("a"), ReasonCircuitOpen) // one probe at a time
	probe.Done(fail)
	// Reopened with a doubled cooldown.
	clk.Advance(time.Minute)
	refusal(t, e, desc("a"), ReasonCircuitOpen)
	clk.Advance(time.Minute)
	reserve(t, e, desc("a")).Done(Outcome{Status: 200})
	if e.CircuitState("a", "") != CircuitClosed {
		t.Fatal("circuit not closed after a successful probe")
	}
}

func TestPriorityOrder(t *testing.T) {
	clk := clock.NewFake(epoch)
	e := newEngine(t, clk, "    - { name: rate, kind: rate, rate: 1/s, burst: 1 }", "")
	reserve(t, e, desc("a")).Done(Outcome{})

	order := make(chan string, 3)
	start := func(name string, p config.Priority) {
		d := desc("a")
		d.Priority = p
		go func() {
			r, err := e.Reserve(context.Background(), d)
			if err != nil {
				order <- "error: " + err.Error()
				return
			}
			order <- name
			r.Done(Outcome{})
		}()
	}
	start("low", config.PriorityLow)
	waitWaiters(t, e, 1)
	start("normal", config.PriorityNormal)
	waitWaiters(t, e, 2)
	start("critical", config.PriorityCritical)
	waitWaiters(t, e, 3)

	// Burst 1: each second of fake time refills one token, so grants come
	// one at a time; keep advancing until the next one (a waiter may arm its
	// timer just after an advance).
	var got []string
	deadline := time.Now().Add(waitTimeout)
	for len(got) < 3 {
		clk.Advance(time.Second)
		select {
		case name := <-order:
			got = append(got, name)
		case <-time.After(10 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatalf("no grant after %v", got)
			}
		}
	}
	if strings.Join(got, ",") != "critical,normal,low" {
		t.Fatalf("grant order = %v, want critical,normal,low", got)
	}
}

func waitWaiters(t *testing.T, e *Engine, n int) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		e.mu.Lock()
		got := len(e.waiters)
		e.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiters = %d, want %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := epoch
	tests := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"5", 5 * time.Second, true},
		{"0.5", 500 * time.Millisecond, true},
		{"99999", maxPenalty, true},
		{now.Add(time.Minute).Format(http.TimeFormat), time.Minute, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, true},
		{"soon", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseRetryAfter(http.Header{"Retry-After": {tt.value}}, "Retry-After", now)
		if got != tt.want || ok != tt.ok {
			t.Errorf("parseRetryAfter(%q) = %v, %v", tt.value, got, ok)
		}
	}
}

// recorder is an Observer recording the events of an engine.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf(format, args...))
}

func (r *recorder) BudgetGranted(provider, limit string, wait time.Duration) {
	r.add("granted %s %s %s", provider, limit, wait)
}

func (r *recorder) BudgetRefused(provider, account, job, reason string) {
	r.add("refused %s %s %s %s", provider, account, job, reason)
}

func (r *recorder) RateLimited(provider, account, limit string, unexpected bool) {
	r.add("rate_limited %s %s %s %v", provider, account, limit, unexpected)
}

func TestObserver(t *testing.T) {
	clk := clock.NewFake(epoch)
	e := newEngine(t, clk, `    - { name: conc, kind: concurrency, max: 1 }
    - { name: rate, kind: rate, rate: 1/s, burst: 1, scope: { by: [account] } }
    - { name: daily, kind: quota, max: 3, window: { period: day }, scope: { by: [account] } }`, "")
	rec := &recorder{}
	e.observer = rec

	r1 := reserve(t, e, desc("a")) // granted at once
	ch := async(context.Background(), e, desc("b"))
	pending(t, ch)
	clk.Advance(3 * time.Second)
	r1.Done(Outcome{Status: 429}) // unexpected: the quota of a is not exhausted
	if err := completed(t, ch); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	d := desc("a")
	d.Job = "devices"
	d.Deadline = clk.Now().Add(500 * time.Millisecond) // the penalty ends 1s from now
	refusal(t, e, d, ReasonPenalized)
	want := []string{
		"granted p none 0s",
		"rate_limited p a rate true",
		"granted p conc 3s",
		"refused p a devices penalized",
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if diff := cmp.Diff(want, rec.events); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}
