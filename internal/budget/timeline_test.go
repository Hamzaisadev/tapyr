package budget

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/config"
)

func TestTimeline(t *testing.T) {
	clk := clock.NewFake(epoch) // 2026-11-02 12:00 UTC
	e := newEngine(t, clk, `    - { name: daily, kind: quota, max: 1000, window: { period: day }, scope: { by: [account] } }
    - { name: monthly, kind: quota, max: 100000, window: { period: month }, scope: { by: [account] } }
    - { name: hourly, kind: quota, max: 100, window: { rolling: 1h }, scope: { by: [account] } }`, "")
	use := func(n int) {
		for range n {
			reserve(t, e, desc("a")).Done(Outcome{Status: 200})
		}
	}
	use(2)
	clk.Advance(90 * time.Second)
	use(1)
	clk.Advance(time.Hour)
	use(3)

	day, ok := e.Timeline("p", "daily", "account=a")
	if !ok {
		t.Fatal("no daily timeline")
	}
	dayStart := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	want := Timeline{
		Provider: "p", Limit: "daily", Scope: "account=a", Bucket: time.Minute,
		Current: TimelineWindow{Start: dayStart, End: dayStart.Add(24 * time.Hour), Buckets: []Bucket{
			{Start: epoch, Count: 2}, {Start: epoch.Add(time.Minute), Count: 1}, {Start: epoch.Add(61 * time.Minute), Count: 3},
		}},
	}
	if diff := cmp.Diff(want, day); diff != "" {
		t.Errorf("daily (-want +got):\n%s", diff)
	}
	month, _ := e.Timeline("p", "monthly", "account=a")
	if month.Bucket != time.Hour || !cmp.Equal(month.Current.Buckets, []Bucket{{Start: epoch, Count: 3}, {Start: epoch.Add(time.Hour), Count: 3}}) {
		t.Errorf("monthly = %+v", month)
	}
	hour, _ := e.Timeline("p", "hourly", "account=a")
	if hour.Bucket != time.Minute || hour.Previous != nil || !cmp.Equal(hour.Current.Buckets, []Bucket{{Start: epoch.Add(61 * time.Minute), Count: 3}}) {
		t.Errorf("rolling = %+v", hour)
	}
	if _, ok := e.Timeline("p", "daily", "account=nope"); ok {
		t.Error("timeline of an unknown scope")
	}

	// The ledger carries the current window over a restart.
	restored := newEngine(t, clk, `    - { name: daily, kind: quota, max: 1000, window: { period: day }, scope: { by: [account] } }`, "")
	restored.Restore(e.Export())
	if got, _ := restored.Timeline("p", "daily", "account=a"); !cmp.Equal(got.Current.Buckets, want.Current.Buckets) {
		t.Errorf("restored = %+v", got.Current.Buckets)
	}

	// The next day, yesterday is the previous window.
	clk.Advance(12 * time.Hour)
	use(1)
	day, _ = e.Timeline("p", "daily", "account=a")
	if day.Previous == nil || !day.Previous.Start.Equal(dayStart) || !cmp.Equal(day.Previous.Buckets, want.Current.Buckets) ||
		len(day.Current.Buckets) != 1 || day.Current.Buckets[0].Count != 1 {
		t.Errorf("next day = %+v", day)
	}
}

func TestLimits(t *testing.T) {
	clk := clock.NewFake(epoch)
	e := newEngine(t, clk, `    - { name: conc, kind: concurrency, max: 2 }
    - { name: rate, kind: rate, rate: 10/s, burst: 5, scope: { by: [account] } }
    - { name: daily, kind: quota, max: 10, window: { period: day } }`, "")
	r := reserve(t, e, desc("a"))
	reserve(t, e, desc("a")).Done(Outcome{Status: 429})
	got := e.Limits()
	want := []LimitState{
		{Provider: "p", Limit: "conc", Scope: "instance", Kind: config.LimitConcurrency, Max: 2, InUse: 1},
		{
			Provider: "p", Limit: "rate", Scope: "account=a", Kind: config.LimitRate, Rate: 10, Burst: 5, Tokens: 3,
			PenaltyUntil: epoch.Add(2 * time.Second),
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("limits (-want +got):\n%s", diff)
	}
	r.Done(Outcome{Status: 200})
}
