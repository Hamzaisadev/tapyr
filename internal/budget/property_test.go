package budget

import (
	"context"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/config"
)

// TestNoLimitIsEverExceeded drives random request streams from concurrent
// workers through overlapping limits while a driver advances the fake clock,
// then checks every limit against the grant log.
func TestNoLimitIsEverExceeded(t *testing.T) {
	for seed := range uint64(3) {
		t.Run("", func(t *testing.T) { runProperty(t, seed) })
	}
}

func runProperty(t *testing.T, seed uint64) {
	clk := clock.NewFake(epoch)
	const (
		rate     = 5.0 // per second, per account
		burst    = 3
		maxConc  = 2 // instance
		hourly   = 600
		duration = 2 * time.Hour
	)
	e := newEngine(t, clk, `    - { name: account-rate, kind: rate, rate: 5/s, burst: 3, scope: { by: [account] } }
    - { name: endpoint-rate, kind: rate, rate: 8/s, burst: 4, scope: { by: [endpoint] } }
    - { name: conc, kind: concurrency, max: 2 }
    - { name: hourly, kind: quota, max: 600, window: { period: hour }, scope: { by: [account] } }`, "")
	end := epoch.Add(duration)

	type grant struct {
		account  string
		endpoint string
		at       time.Time
	}
	var (
		mu       sync.Mutex
		grants   []grant
		inFlight atomic.Int64
		maxSeen  atomic.Int64
		stop     atomic.Bool
		workers  sync.WaitGroup
	)
	priorities := []config.Priority{config.PriorityLow, config.PriorityNormal, config.PriorityHigh, config.PriorityCritical}
	for w := range 8 {
		workers.Go(func() {
			r := rand.New(rand.NewPCG(seed, uint64(w)))
			for !stop.Load() {
				d := Descriptor{
					Provider: "p", Account: []string{"a", "b"}[r.IntN(2)], Method: "GET",
					Endpoint: []string{"/devices", "/sites"}[r.IntN(2)],
					Priority: priorities[r.IntN(len(priorities))], Retry: r.IntN(5) == 0,
				}
				if r.IntN(3) == 0 {
					d.Deadline = clk.Now().Add(time.Duration(r.IntN(2000)) * time.Millisecond)
				}
				res, err := e.Reserve(context.Background(), d)
				if err != nil {
					if _, ok := IsRefusal(err); !ok {
						t.Errorf("unexpected error: %v", err)
						return
					}
					// Refused (quota exhausted until the next hour, deadline):
					// let fake time move on.
					time.Sleep(50 * time.Microsecond)
					continue
				}
				n := inFlight.Add(1)
				for {
					m := maxSeen.Load()
					if n <= m || maxSeen.CompareAndSwap(m, n) {
						break
					}
				}
				mu.Lock()
				grants = append(grants, grant{d.Account, d.Endpoint, res.(*reservation).at})
				mu.Unlock()
				if r.IntN(2) == 0 {
					time.Sleep(time.Duration(r.IntN(100)) * time.Microsecond)
				}
				inFlight.Add(-1)
				res.Done(Outcome{Status: 200})
			}
		})
	}
	driver := rand.New(rand.NewPCG(seed, 99))
	for clk.Now().Before(end) {
		clk.Advance(time.Duration(10+driver.IntN(300)) * time.Millisecond)
		time.Sleep(20 * time.Microsecond)
	}
	stop.Store(true)
	// Keep time moving until every worker has returned: a worker may start a
	// reservation that waits for a token right after the last advance.
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	timeout := time.After(30 * time.Second)
	for finished := false; !finished; {
		select {
		case <-done:
			finished = true
		case <-timeout:
			t.Fatal("workers did not finish: deadlock?")
		default:
			clk.Advance(time.Second)
			time.Sleep(time.Millisecond)
		}
	}

	if maxSeen.Load() > maxConc {
		t.Errorf("concurrency %d exceeded the limit %d", maxSeen.Load(), maxConc)
	}
	if len(grants) < 1000 {
		t.Fatalf("only %d grants: the test does not exercise the limits", len(grants))
	}
	t.Logf("%d grants, max concurrency %d", len(grants), maxSeen.Load())
	check := func(name string, key func(grant) string, rate, burst float64) {
		groups := map[string][]time.Time{}
		for _, g := range grants {
			groups[key(g)] = append(groups[key(g)], g.at)
		}
		for k, times := range groups {
			slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
			for i := range times {
				for j := i; j < len(times) && times[j].Sub(times[i]) <= 10*time.Second; j++ {
					allowed := burst + rate*times[j].Sub(times[i]).Seconds() + 1e-9
					if float64(j-i+1) > allowed {
						t.Fatalf("%s %s: %d grants in %v (allowed %.2f)", name, k, j-i+1, times[j].Sub(times[i]), allowed)
					}
				}
			}
		}
	}
	check("account-rate", func(g grant) string { return g.account }, rate, burst)
	check("endpoint-rate", func(g grant) string { return g.endpoint }, 8, 4)

	perHour := map[string]int{}
	for _, g := range grants {
		perHour[g.account+g.at.Truncate(time.Hour).String()]++
	}
	for k, n := range perHour {
		limit := hourly
		if k[0] == 'b' {
			limit = hourly / 2 // share 0.5
		}
		if n > limit {
			t.Errorf("hourly quota %s: %d grants, limit %d", k, n, limit)
		}
	}
}
