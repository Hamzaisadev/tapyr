package budget

import (
	"net/http"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/clock"
)

func TestExportRestore(t *testing.T) {
	limits := `    - { name: daily, kind: quota, max: 10, window: { period: day }, scope: { by: [account] } }
    - { name: hourly, kind: quota, max: 10, window: { rolling: 1h }, scope: { by: [account] } }
    - name: synced
      kind: quota
      max: auto
      initialMax: 100
      window: { period: day }
      sync: { limitHeader: X-Limit, remainingHeader: X-Remaining }`
	clk := clock.NewFake(epoch)
	before := newEngine(t, clk, limits, "")
	for range 5 {
		reserve(t, before, desc("b")).Done(Outcome{Status: 200, Header: http.Header{"X-Limit": {"500"}, "X-Remaining": {"300"}}})
	}
	entries := before.Export()
	if len(entries) != 3 {
		t.Fatalf("exported %d entries", len(entries))
	}

	// A restart in the same window keeps the consumption: b (share 0.5) has
	// 5 usable (reserve floor(0.5) = 0) and already used them all.
	clk.Advance(30 * time.Minute)
	after := newEngine(t, clk, limits, "")
	if n := after.Restore(entries); n != 3 {
		t.Fatalf("restored %d entries", n)
	}
	refusal(t, after, desc("b"), ReasonQuotaExhausted)
	q := after.Quotas()
	if q[2].Limit != "synced" || q[2].Max != 500 || q[2].ObservedRemaining != 300 {
		t.Errorf("synced quota after restore = %+v", q[2])
	}

	// After the day ends the fixed windows are discarded, and the rolling
	// minutes have expired.
	clk.Set(epoch.Add(13 * time.Hour))
	fresh := newEngine(t, clk, limits, "")
	if n := fresh.Restore(entries); n != 1 {
		t.Fatalf("restored %d entries after the window, want only the rolling one", n)
	}
	reserve(t, fresh, desc("b")).Done(Outcome{})
	if n := fresh.Restore(append(entries, entries[0])); n < 1 {
		t.Error("restore of known entries failed")
	}
}
