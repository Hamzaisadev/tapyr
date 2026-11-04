package series

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/kentrow/tapyr/internal/model"
	"github.com/kentrow/tapyr/internal/state"
)

func window(name, w string, v float64) model.Sample {
	return model.Sample{Name: name, Type: model.Counter, Value: v, Accumulate: true, Window: w}
}

func values(samples []model.Sample) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = s.Value
	}
	return out
}

func TestAccumulators(t *testing.T) {
	a := NewAccumulators()
	gauge := model.Sample{Name: "up", Type: model.Gauge, Value: 1}
	steps := []struct {
		samples []model.Sample
		want    []float64
	}{
		{[]model.Sample{window("bytes_total", "w1", 10), gauge}, []float64{10, 1}},
		// The same window again adds nothing; a new one adds its value.
		{[]model.Sample{window("bytes_total", "w1", 10)}, []float64{10}},
		{[]model.Sample{window("bytes_total", "w2", 5), window("bytes_total", "w3", 7)}, []float64{15, 22}},
		{[]model.Sample{window("bytes_total", "w3", 7), window("bytes_total", "w4", 0)}, []float64{22, 22}},
	}
	for i, st := range steps {
		if got := values(a.Apply("devices", st.samples)); !cmp.Equal(got, st.want) {
			t.Errorf("step %d: %v, want %v", i, got, st.want)
		}
	}
	// Snapshot and restore carry the totals over a restart.
	changed, removed := a.Snapshot()
	key := model.Series{Name: "bytes_total"}.Key()
	want := []state.AccumulatorSnapshot{{Instance: "devices", Series: []state.Accumulated{{Series: key, Total: 22, Window: "w4"}}}}
	if diff := cmp.Diff(want, changed); diff != "" || len(removed) != 0 {
		t.Errorf("snapshot (-want +got):\n%s removed %v", diff, removed)
	}
	if changed, removed := a.Snapshot(); len(changed)+len(removed) != 0 {
		t.Error("second snapshot not empty")
	}
	b := NewAccumulators()
	b.Restore(changed)
	if got := values(b.Apply("devices", []model.Sample{window("bytes_total", "w5", 3)})); !cmp.Equal(got, []float64{25}) {
		t.Errorf("after restore: %v", got)
	}
	// A stale series starts again from zero; an instance without counters
	// left is removed.
	if got := values(b.Apply("devices", []model.Sample{gauge})); !cmp.Equal(got, []float64{1}) {
		t.Errorf("gauge only: %v", got)
	}
	if _, removed := b.Snapshot(); !cmp.Equal(removed, []string{"devices"}) {
		t.Errorf("removed = %v", removed)
	}
	if got := values(b.Apply("devices", []model.Sample{window("bytes_total", "w6", 4)})); !cmp.Equal(got, []float64{4}) {
		t.Errorf("after staleness: %v", got)
	}
	b.Remove("devices")
	b.Remove("unknown")
	if changed, removed := b.Snapshot(); len(changed) != 0 || !cmp.Equal(removed, []string{"devices"}) {
		t.Errorf("after Remove: %v %v", changed, removed)
	}
	if got := b.Apply("other", []model.Sample{gauge}); len(got) != 1 {
		t.Error("samples lost")
	}
}
