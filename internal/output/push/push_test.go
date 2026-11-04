package push

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/model"
)

var start = time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)

// fakeSender records the batches and answers with the queued results.
type fakeSender struct {
	mu      sync.Mutex
	batches [][]float64
	results []error
	partial Result
	sent    chan struct{}
	block   chan struct{}
}

func newSender() *fakeSender { return &fakeSender{sent: make(chan struct{}, 100)} }

func (s *fakeSender) Send(_ context.Context, points []Point) (Result, error) {
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = p.Value
	}
	s.batches = append(s.batches, values)
	var err error
	if len(s.results) > 0 {
		err, s.results = s.results[0], s.results[1:]
	}
	res := s.partial
	s.mu.Unlock()
	s.sent <- struct{}{}
	return res, err
}

func (s *fakeSender) got() [][]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]float64(nil), s.batches...)
}

type counts struct {
	mu       sync.Mutex
	sent     int
	dropped  map[string]int
	failures map[string]int
	sends    int
}

func (c *counts) OutputSent(_ string, n int) { c.mu.Lock(); c.sent += n; c.mu.Unlock() }
func (c *counts) OutputDropped(_, reason string, n int) {
	c.mu.Lock()
	c.dropped[reason] += n
	c.mu.Unlock()
}

func (c *counts) OutputSendFailed(_, class string) {
	c.mu.Lock()
	c.failures[class]++
	c.mu.Unlock()
}

func (c *counts) OutputSendDuration(string, time.Duration) { c.mu.Lock(); c.sends++; c.mu.Unlock() }

func points(values ...float64) []Point {
	out := make([]Point, len(values))
	for i, v := range values {
		out[i] = Point{Sample: model.Sample{Name: "m", Value: v, Type: model.Gauge}}
	}
	return out
}

type harness struct {
	t      *testing.T
	clk    *clock.Fake
	q      *Queue
	sender *fakeSender
	obs    *counts
	cancel context.CancelFunc
	done   chan struct{}
}

func newHarness(t *testing.T, capacity, maxBatch int) *harness {
	t.Helper()
	h := &harness{t: t, clk: clock.NewFake(start), sender: newSender(), obs: &counts{dropped: map[string]int{}, failures: map[string]int{}}}
	h.q = New(Options{
		Name: "out", Capacity: capacity, MaxBatch: maxBatch, FlushInterval: 5 * time.Second,
		Sender: h.sender, Clock: h.clk, Observer: h.obs, Rand: rand.New(rand.NewPCG(1, 2)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan struct{})
	go func() {
		defer close(h.done)
		if err := h.q.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	<-h.done
}

// waitSent waits for n more sends.
func (h *harness) waitSent(n int) {
	h.t.Helper()
	for range n {
		select {
		case <-h.sender.sent:
		case <-time.After(5 * time.Second):
			h.t.Fatalf("send did not happen; batches %v", h.sender.got())
		}
	}
}

// waitIdle waits until the sender loop is idle: no send in flight, and
// either nothing queued or a timer on the clock (flush interval, backoff).
func (h *harness) waitIdle() {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.q.mu.Lock()
		idle := h.q.sending == 0 && (len(h.q.points) == 0 || h.clk.Waiters() > 0)
		h.q.mu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatal("the sender loop is not idle")
		}
		time.Sleep(100 * time.Microsecond)
	}
}

func TestBatching(t *testing.T) {
	h := newHarness(t, 100, 3)
	// A full batch leaves at once; the rest waits for the flush interval.
	h.q.Add(points(1, 2, 3, 4))
	h.waitSent(1)
	h.waitIdle()
	if got := h.sender.got(); !cmp.Equal(got, [][]float64{{1, 2, 3}}) {
		t.Fatalf("batches = %v", got)
	}
	h.clk.Advance(5 * time.Second)
	h.waitSent(1)
	if got := h.sender.got(); !cmp.Equal(got, [][]float64{{1, 2, 3}, {4}}) {
		t.Errorf("batches = %v", got)
	}
	h.waitIdle()
	st := h.q.State()
	if st.Queue != 0 || st.Sent != 4 || st.Capacity != 100 || !st.LastSuccess.Equal(start.Add(5*time.Second)) {
		t.Errorf("state = %+v", st)
	}
	h.obs.mu.Lock()
	defer h.obs.mu.Unlock()
	if h.obs.sent != 4 || h.obs.sends != 2 {
		t.Errorf("observer: %d sent in %d sends", h.obs.sent, h.obs.sends)
	}
}

func TestQueueFullDropsOldest(t *testing.T) {
	h := newHarness(t, 3, 10)
	h.q.Add(points(1, 2))
	h.q.Add(points(3, 4, 5))
	if st := h.q.State(); st.Queue != 3 || st.Dropped != 2 {
		t.Errorf("state = %+v", st)
	}
	h.waitIdle()
	h.clk.Advance(5 * time.Second)
	h.waitSent(1)
	if got := h.sender.got(); !cmp.Equal(got, [][]float64{{3, 4, 5}}) {
		t.Errorf("batches = %v", got)
	}
	h.obs.mu.Lock()
	defer h.obs.mu.Unlock()
	if h.obs.dropped["queue_full"] != 2 {
		t.Errorf("dropped = %v", h.obs.dropped)
	}
}

func TestBatchInFlightIsNotDropped(t *testing.T) {
	h := newHarness(t, 3, 2)
	h.sender.block = make(chan struct{})
	h.q.Add(points(1, 2)) // a full batch: sent at once, the send blocks
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.q.mu.Lock()
		sending := h.q.sending
		h.q.mu.Unlock()
		if sending != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the batch was not sent")
		}
		time.Sleep(100 * time.Microsecond)
	}
	h.q.Add(points(3, 4, 5)) // 5 points for 3: 3 and 4 go, the batch in flight stays
	close(h.sender.block)
	h.waitSent(1)
	h.waitIdle()
	h.clk.Advance(5 * time.Second)
	h.waitSent(1)
	if got := h.sender.got(); !cmp.Equal(got, [][]float64{{1, 2}, {5}}) {
		t.Errorf("batches = %v", got)
	}
}

func TestRetries(t *testing.T) {
	h := newHarness(t, 100, 10)
	h.sender.results = []error{
		&Error{Class: ClassTransient, Err: errors.New("connection refused")},
		&Error{Class: ClassThrottled, Status: 429, RetryAfter: 30 * time.Second, Err: errors.New("slow down")},
		errors.New("unclassified"),
	}
	h.q.Add(points(1, 2))
	h.waitIdle()
	h.clk.Advance(5 * time.Second)
	h.waitSent(1) // transient: backoff within 1s
	h.waitIdle()
	h.clk.Advance(time.Second)
	h.waitSent(1) // throttled: Retry-After 30s
	h.waitIdle()
	h.clk.Advance(29 * time.Second)
	select {
	case <-h.sender.sent:
		t.Fatal("retried before Retry-After")
	case <-time.After(20 * time.Millisecond):
	}
	h.clk.Advance(time.Second)
	h.waitSent(1) // unclassified errors are transient: backoff within 2s
	h.waitIdle()
	h.clk.Advance(2 * time.Second)
	h.waitSent(1)
	if got := h.sender.got(); len(got) != 4 || !cmp.Equal(got[3], []float64{1, 2}) {
		t.Errorf("batches = %v", got)
	}
	h.waitIdle()
	st := h.q.State()
	if st.Sent != 2 || st.Queue != 0 || st.LastError == "" {
		t.Errorf("state = %+v", st)
	}
	h.obs.mu.Lock()
	defer h.obs.mu.Unlock()
	if !cmp.Equal(h.obs.failures, map[string]int{ClassTransient: 2, ClassThrottled: 1}) {
		t.Errorf("failures = %v", h.obs.failures)
	}
}

func TestRejections(t *testing.T) {
	h := newHarness(t, 100, 2)
	h.sender.results = []error{&Error{Class: ClassOutOfOrder, Status: 400, Err: errors.New("out of order sample")}}
	h.q.Add(points(1, 2, 3))
	h.waitSent(1) // the full batch is dropped, not retried
	h.sender.mu.Lock()
	h.sender.partial = Result{Rejected: 1, Message: "one bad point"}
	h.sender.mu.Unlock()
	h.waitIdle()
	h.clk.Advance(5 * time.Second)
	h.waitSent(1)
	h.waitIdle()
	if got := h.sender.got(); !cmp.Equal(got, [][]float64{{1, 2}, {3}}) {
		t.Errorf("batches = %v", got)
	}
	if st := h.q.State(); st.Sent != 0 || st.Dropped != 3 {
		t.Errorf("state = %+v", st)
	}
	h.obs.mu.Lock()
	defer h.obs.mu.Unlock()
	if !cmp.Equal(h.obs.dropped, map[string]int{ClassOutOfOrder: 2, ClassRejected: 1}) {
		t.Errorf("dropped = %v", h.obs.dropped)
	}
}

func TestFlush(t *testing.T) {
	h := newHarness(t, 100, 10)
	if err := h.q.Flush(context.Background()); err != nil {
		t.Fatalf("Flush of an empty queue: %v", err)
	}
	h.q.Add(points(1, 2))
	// Flush sends without waiting for the flush interval.
	if err := h.q.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.sender.got(); !cmp.Equal(got, [][]float64{{1, 2}}) {
		t.Errorf("batches = %v", got)
	}
	// A backend down: Flush gives up at the deadline.
	h.waitSent(1)
	h.sender.mu.Lock()
	for range 10 {
		h.sender.results = append(h.sender.results, &Error{Class: ClassTransient, Err: errors.New("down")})
	}
	h.sender.mu.Unlock()
	h.q.Add(points(3))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := h.q.Flush(ctx); err == nil || !errors.Is(err, context.DeadlineExceeded) || err.Error() != "1 samples not sent: context deadline exceeded" {
		t.Errorf("Flush to a backend down = %v", err)
	}
}

func TestHTTPError(t *testing.T) {
	remoteWrite := func(status int) bool { return status >= 500 }
	now := start
	tests := []struct {
		status     int
		header     string
		body       string
		wantClass  string
		wantRetry  time.Duration
		wantNilErr bool
	}{
		{status: 200, wantNilErr: true},
		{status: 204, wantNilErr: true},
		{status: 429, header: "7", wantClass: ClassThrottled, wantRetry: 7 * time.Second},
		{status: 429, header: "3600", wantClass: ClassThrottled, wantRetry: maxRetryAfter},
		{status: 503, header: now.Add(10 * time.Second).Format(http.TimeFormat), wantClass: ClassTransient, wantRetry: 10 * time.Second},
		{status: 500, header: "soon", wantClass: ClassTransient},
		{status: 400, body: "out of order sample", wantClass: ClassOutOfOrder},
		{status: 400, body: "sample timestamp out of bounds", wantClass: ClassOutOfOrder},
		{status: 400, body: "TOO OLD sample", wantClass: ClassOutOfOrder},
		{status: 401, body: "unauthorized", wantClass: ClassRejected},
	}
	for _, tt := range tests {
		resp := &http.Response{StatusCode: tt.status, Header: http.Header{}}
		if tt.header != "" {
			resp.Header.Set("Retry-After", tt.header)
		}
		e := HTTPError(resp, []byte(tt.body), now, remoteWrite)
		if tt.wantNilErr {
			if e != nil {
				t.Errorf("%d: %v", tt.status, e)
			}
			continue
		}
		if e == nil || e.Class != tt.wantClass || e.RetryAfter != tt.wantRetry || e.Status != tt.status {
			t.Errorf("%d %q: %+v", tt.status, tt.body, e)
		}
	}
	long := make([]byte, 1000)
	for i := range long {
		long[i] = 'x'
	}
	e := HTTPError(&http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}}, long, now, remoteWrite)
	if len(e.Err.Error()) != 259 || e.Error() != fmt.Sprintf("rejected: HTTP 400: %s...", long[:256]) {
		t.Errorf("long body: %d characters", len(e.Err.Error()))
	}
	if (&Error{Class: ClassTransient, Err: errors.New("x")}).Error() != "transient: x" {
		t.Error("Error() without status")
	}
}
