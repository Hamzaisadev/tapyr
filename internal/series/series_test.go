package series

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/kentrow/tapyr/internal/model"
)

var t0 = time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)

func sample(name, serial string, ts time.Time, source bool) model.Sample {
	return model.Sample{
		Name: name, Type: model.Gauge, Labels: model.LabelsFromMap(map[string]string{"serial": serial}),
		Value: 1, Timestamp: ts, SourceTimestamp: source,
	}
}

func keys(samples []model.Sample) []string {
	var out []string
	for i := range samples {
		out = append(out, samples[i].Labels.Get("serial")+"@"+samples[i].Timestamp.Format("15:04"))
	}
	return out
}

func serials(series []model.Series) []string {
	var out []string
	for _, s := range series {
		out = append(out, s.Name+"/"+s.Labels.Get("serial"))
	}
	return out
}

var outputs = []Output{{Name: "prom"}, {Name: "remote", Dedup: true}}

func TestSuccessStalenessAndDedup(t *testing.T) {
	r := New()
	// First run: everything is new.
	d := r.Success("devices", []model.Sample{
		sample("up", "A", t0, true), sample("up", "B", t0, true), sample("clients", "A", t0.Add(time.Minute), false),
	}, outputs, t0)
	if got := keys(d["remote"].Samples); len(got) != 3 || d["remote"].Deduplicated != 0 || len(d["remote"].Stale) != 0 {
		t.Fatalf("first run = %+v", d["remote"])
	}

	// Second run: A has no newer source timestamp (dropped for push, kept
	// for pull), B is gone, C appears; samples stamped with the collection
	// time are never deduplicated.
	d = r.Success("devices", []model.Sample{
		sample("up", "A", t0, true), sample("up", "C", t0, true), sample("clients", "A", t0.Add(time.Minute), false),
	}, outputs, t0.Add(5*time.Minute))
	if diff := cmp.Diff([]string{"C@12:00", "A@12:01"}, keys(d["remote"].Samples)); diff != "" {
		t.Errorf("remote samples (-want +got):\n%s", diff)
	}
	if d["remote"].Deduplicated != 1 || len(d["prom"].Samples) != 3 || d["prom"].Deduplicated != 0 {
		t.Errorf("dedup: remote %d, prom %d samples", d["remote"].Deduplicated, len(d["prom"].Samples))
	}
	for _, o := range []string{"prom", "remote"} {
		if diff := cmp.Diff([]string{"up/B"}, serials(d[o].Stale)); diff != "" {
			t.Errorf("%s stale (-want +got):\n%s", o, diff)
		}
	}

	// A newer source timestamp goes through; B comes back as a new series
	// (its timestamps were forgotten when it went stale).
	d = r.Success("devices", []model.Sample{
		sample("up", "A", t0.Add(time.Hour), true), sample("up", "B", t0, true),
	}, outputs, t0.Add(10*time.Minute))
	if diff := cmp.Diff([]string{"A@13:00", "B@12:00"}, keys(d["remote"].Samples)); diff != "" {
		t.Errorf("remote samples (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"clients/A", "up/C"}, serials(d["remote"].Stale)); diff != "" {
		t.Errorf("stale (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"up/A", "up/B"}, serials(r.Series("devices"))); diff != "" {
		t.Errorf("series (-want +got):\n%s", diff)
	}
}

func TestExpireRemoveAndSnapshot(t *testing.T) {
	r := New()
	r.Success("devices", []model.Sample{sample("up", "A", t0, true)}, outputs, t0)
	r.Success("sites", []model.Sample{sample("site", "S", t0, false)}, outputs, t0)
	r.Success("gone", []model.Sample{sample("x", "G", t0, false)}, outputs, t0)

	staleAfter := func(name string) (time.Duration, bool) {
		switch name {
		case "devices":
			return 15 * time.Minute, true
		case "sites":
			return 3 * time.Hour, true
		}
		return 0, false
	}
	if diff := cmp.Diff([]string{"gone"}, r.Expired(t0.Add(15*time.Minute), staleAfter)); diff != "" {
		t.Errorf("expired at 15m (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"devices", "gone"}, r.Expired(t0.Add(16*time.Minute), staleAfter)); diff != "" {
		t.Errorf("expired at 16m (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"up/A"}, serials(r.Expire("devices"))); diff != "" {
		t.Errorf("expire (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"x/G"}, serials(r.Remove("gone"))); diff != "" {
		t.Errorf("remove (-want +got):\n%s", diff)
	}
	if r.Remove("gone") != nil || r.Expire("gone") != nil {
		t.Error("removing twice returned series")
	}
	// An expired instance is not expired again until it has series.
	if diff := cmp.Diff([]string(nil), r.Expired(t0.Add(time.Hour), staleAfter)); diff != "" {
		t.Errorf("expired at 1h (-want +got):\n%s", diff)
	}

	changed, removed := r.Snapshot()
	if len(changed) != 2 || changed[0].Instance != "devices" || len(changed[0].Series) != 0 || changed[1].Instance != "sites" {
		t.Errorf("snapshot changed = %+v", changed)
	}
	if diff := cmp.Diff([]string{"gone"}, removed); diff != "" {
		t.Errorf("snapshot removed (-want +got):\n%s", diff)
	}
	if changed, removed := r.Snapshot(); len(changed)+len(removed) != 0 {
		t.Errorf("second snapshot = %v %v", changed, removed)
	}
	// The dedup timestamps of an expired instance survive: a recovering run
	// with the same source timestamp is deduplicated.
	d := r.Success("devices", []model.Sample{sample("up", "A", t0, true)}, outputs, t0.Add(time.Hour))
	if d["remote"].Deduplicated != 1 || len(d["remote"].Stale) != 0 {
		t.Errorf("recovering run = %+v", d["remote"])
	}

	// Restore rebuilds the registry from snapshots.
	changed, _ = r.Snapshot()
	r2 := New()
	if skipped := r2.Restore(append(changed, changed[0])); skipped != 0 {
		t.Errorf("skipped %d", skipped)
	}
	d = r2.Success("devices", []model.Sample{sample("up", "A", t0, true)}, outputs, t0.Add(2*time.Hour))
	if d["remote"].Deduplicated != 1 {
		t.Errorf("dedup after restore = %+v", d["remote"])
	}
	d = r2.Success("devices", nil, outputs, t0.Add(3*time.Hour))
	if diff := cmp.Diff([]string{"up/A"}, serials(d["prom"].Stale)); diff != "" {
		t.Errorf("stale after restore (-want +got):\n%s", diff)
	}
	changed[0].Series = []string{"invalid"}
	if skipped := New().Restore(changed); skipped != 1 {
		t.Errorf("skipped %d invalid keys, want 1", skipped)
	}
}
