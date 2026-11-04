package series

import (
	"maps"
	"slices"
	"sync"

	"github.com/kentrow/tapyr/internal/model"
	"github.com/kentrow/tapyr/internal/state"
)

// Accumulators reconstruct counters from window values (accumulate: true,
// see "Implementation notes" in 06-mapping-and-outputs.md): per job
// instance and series, the running total and the last window counted. It is
// safe for concurrent use.
type Accumulators struct {
	mu        sync.Mutex
	instances map[string]map[string]*accumulated
	dirty     map[string]bool
	removed   map[string]bool
}

type accumulated struct {
	total  float64
	window string
}

// NewAccumulators returns empty Accumulators.
func NewAccumulators() *Accumulators {
	return &Accumulators{instances: map[string]map[string]*accumulated{}, dirty: map[string]bool{}, removed: map[string]bool{}}
}

// Apply replaces, in place, the value of every sample marked Accumulate with
// the running total of its series: a window not counted yet adds its value,
// the last window counted adds nothing. Windows are expected in order. The
// counters of the instance absent from samples (stale series) are dropped.
func (a *Accumulators) Apply(instance string, samples []model.Sample) []model.Sample {
	a.mu.Lock()
	defer a.mu.Unlock()
	prev := a.instances[instance]
	cur := map[string]*accumulated{}
	for i := range samples {
		s := &samples[i]
		if !s.Accumulate {
			continue
		}
		key := s.SeriesKey()
		acc, ok := cur[key]
		if !ok {
			if acc, ok = prev[key]; !ok {
				acc = &accumulated{}
			}
			cur[key] = acc
		}
		// A new counter has no window: the first one always counts.
		if acc.window != s.Window {
			acc.total += s.Value
			acc.window = s.Window
		}
		s.Value = acc.total
	}
	if len(cur) == 0 && len(prev) == 0 {
		return samples
	}
	if len(cur) == 0 {
		delete(a.instances, instance)
		delete(a.dirty, instance)
		a.removed[instance] = true
		return samples
	}
	a.instances[instance] = cur
	a.dirty[instance] = true
	delete(a.removed, instance)
	return samples
}

// Remove drops the counters of an instance (removed, or withdrawn after
// staleAfter).
func (a *Accumulators) Remove(instance string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.instances[instance]; !ok {
		return
	}
	delete(a.instances, instance)
	delete(a.dirty, instance)
	a.removed[instance] = true
}

// Snapshot returns the instances changed and removed since the last
// snapshot.
func (a *Accumulators) Snapshot() (changed []state.AccumulatorSnapshot, removed []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, instance := range slices.Sorted(maps.Keys(a.dirty)) {
		snap := state.AccumulatorSnapshot{Instance: instance}
		accs := a.instances[instance]
		for _, key := range slices.Sorted(maps.Keys(accs)) {
			snap.Series = append(snap.Series, state.Accumulated{Series: key, Total: accs[key].total, Window: accs[key].window})
		}
		changed = append(changed, snap)
	}
	removed = slices.Sorted(maps.Keys(a.removed))
	a.dirty, a.removed = map[string]bool{}, map[string]bool{}
	return changed, removed
}

// Restore loads snapshots, after a restart.
func (a *Accumulators) Restore(snaps []state.AccumulatorSnapshot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, snap := range snaps {
		accs := map[string]*accumulated{}
		for _, s := range snap.Series {
			accs[s.Series] = &accumulated{total: s.Total, window: s.Window}
		}
		a.instances[snap.Instance] = accs
	}
}
