// Package series is the series registry (see
// docs/spec/06-mapping-and-outputs.md, "Series registry"): per job instance,
// the series of the last successful run and the last source timestamp sent
// per series and output. It drives staleness and deduplication.
package series

import (
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/kentrow/tapyr/internal/model"
	"github.com/kentrow/tapyr/internal/state"
)

// Output is an output receiving the samples of a run.
type Output struct {
	Name string
	// Dedup drops samples whose source timestamp is not after the last one
	// sent (push outputs). The pull output exposes the latest value and
	// receives every sample.
	Dedup bool
}

// Delivery is what an output receives for a successful run.
type Delivery struct {
	Samples []model.Sample
	// Stale are the series of the previous successful run absent from this one.
	Stale []model.Series
	// Deduplicated counts the samples dropped by deduplication.
	Deduplicated int
}

// Registry tracks the series of every job instance. It is safe for
// concurrent use.
type Registry struct {
	mu        sync.Mutex
	instances map[string]*instance
	// dirty and removed are the instances changed and removed since the
	// last snapshot.
	dirty   map[string]bool
	removed map[string]bool
}

type instance struct {
	lastSuccess time.Time
	series      map[string]model.Series
	sent        map[string]map[string]time.Time // output -> series key -> source timestamp
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{instances: map[string]*instance{}, dirty: map[string]bool{}, removed: map[string]bool{}}
}

// Success records the samples of a successful run of an instance and
// returns, per output, the samples to send and the series now stale.
func (r *Registry) Success(name string, samples []model.Sample, outputs []Output, at time.Time) map[string]Delivery {
	current := map[string]model.Series{}
	for i := range samples {
		s := samples[i].Series()
		if key := s.Key(); !hasKey(current, key) {
			current[key] = s
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	inst := r.instance(name)
	var stale []model.Series
	for _, key := range slices.Sorted(maps.Keys(inst.series)) {
		if _, ok := current[key]; !ok {
			stale = append(stale, inst.series[key])
		}
	}
	out := make(map[string]Delivery, len(outputs))
	for _, o := range outputs {
		d := Delivery{Samples: samples, Stale: stale}
		if o.Dedup {
			d.Samples, d.Deduplicated = inst.dedup(o.Name, samples)
		}
		out[o.Name] = d
	}
	// The timestamps of stale series are forgotten, which bounds the
	// registry to the series alive.
	for _, sent := range inst.sent {
		for key := range sent {
			if _, ok := current[key]; !ok {
				delete(sent, key)
			}
		}
	}
	inst.series = current
	inst.lastSuccess = at
	r.dirty[name] = true
	return out
}

// dedup drops the samples with a source timestamp not after the last one
// sent to the output, and records the timestamps sent.
func (inst *instance) dedup(output string, samples []model.Sample) ([]model.Sample, int) {
	sent := inst.sent[output]
	if sent == nil {
		sent = map[string]time.Time{}
		inst.sent[output] = sent
	}
	kept := make([]model.Sample, 0, len(samples))
	dropped := 0
	for i := range samples {
		s := &samples[i]
		if !s.SourceTimestamp {
			kept = append(kept, *s)
			continue
		}
		key := s.SeriesKey()
		if last, ok := sent[key]; ok && !s.Timestamp.After(last) {
			dropped++
			continue
		}
		sent[key] = s.Timestamp
		kept = append(kept, *s)
	}
	return kept, dropped
}

// Expired returns the instances to withdraw at now, sorted: those with
// series whose last success is older than their staleAfter, and those
// unknown to staleAfter (ok false: removed from the configuration).
func (r *Registry) Expired(now time.Time, staleAfter func(instance string) (time.Duration, bool)) []string {
	r.mu.Lock()
	names := slices.Sorted(maps.Keys(r.instances))
	last := make([]time.Time, len(names))
	empty := make([]bool, len(names))
	for i, name := range names {
		last[i], empty[i] = r.instances[name].lastSuccess, len(r.instances[name].series) == 0
	}
	r.mu.Unlock()
	var out []string
	for i, name := range names {
		d, ok := staleAfter(name)
		if !ok || (!empty[i] && now.Sub(last[i]) > d) {
			out = append(out, name)
		}
	}
	return out
}

// Expire withdraws the series of an instance whose runs keep failing and
// returns them. The timestamps sent are kept for when the runs recover.
func (r *Registry) Expire(name string) []model.Series {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.instances[name]
	if !ok {
		return nil
	}
	stale := sortedSeries(inst.series)
	inst.series = map[string]model.Series{}
	r.dirty[name] = true
	return stale
}

// Remove forgets an instance (removed by discovery or the configuration)
// and returns its series, now stale.
func (r *Registry) Remove(name string) []model.Series {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.instances[name]
	if !ok {
		return nil
	}
	delete(r.instances, name)
	delete(r.dirty, name)
	r.removed[name] = true
	return sortedSeries(inst.series)
}

// Snapshot returns the state of the instances changed since the last
// snapshot, and the instances removed.
func (r *Registry) Snapshot() (changed []state.SeriesSnapshot, removed []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range slices.Sorted(maps.Keys(r.dirty)) {
		inst := r.instances[name]
		snap := state.SeriesSnapshot{
			Instance: name, LastSuccess: inst.lastSuccess, Series: slices.Sorted(maps.Keys(inst.series)),
			Sent: make(map[string]map[string]time.Time, len(inst.sent)),
		}
		for output, sent := range inst.sent {
			snap.Sent[output] = maps.Clone(sent)
		}
		changed = append(changed, snap)
	}
	removed = slices.Sorted(maps.Keys(r.removed))
	clear(r.dirty)
	clear(r.removed)
	return changed, removed
}

// Restore loads snapshots, for example after a restart. Series keys that
// cannot be parsed are skipped and counted.
func (r *Registry) Restore(snaps []state.SeriesSnapshot) (skipped int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, snap := range snaps {
		inst := r.instance(snap.Instance)
		inst.lastSuccess = snap.LastSuccess
		for _, key := range snap.Series {
			s, err := model.ParseSeriesKey(key)
			if err != nil {
				skipped++
				continue
			}
			inst.series[key] = s
		}
		for output, sent := range snap.Sent {
			inst.sent[output] = maps.Clone(sent)
		}
	}
	return skipped
}

// Series returns the series of the last successful run of an instance.
func (r *Registry) Series(name string) []model.Series {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inst, ok := r.instances[name]; ok {
		return sortedSeries(inst.series)
	}
	return nil
}

// LastSuccess returns the time of the last successful run of an instance;
// ok is false when it never succeeded.
func (r *Registry) LastSuccess(name string) (at time.Time, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inst, found := r.instances[name]; found && !inst.lastSuccess.IsZero() {
		return inst.lastSuccess, true
	}
	return time.Time{}, false
}

// instance returns the state of an instance, created if needed; r.mu must
// be held.
func (r *Registry) instance(name string) *instance {
	inst, ok := r.instances[name]
	if !ok {
		inst = &instance{series: map[string]model.Series{}, sent: map[string]map[string]time.Time{}}
		r.instances[name] = inst
		delete(r.removed, name)
	}
	return inst
}

func hasKey(m map[string]model.Series, key string) bool {
	_, ok := m[key]
	return ok
}

func sortedSeries(m map[string]model.Series) []model.Series {
	out := make([]model.Series, 0, len(m))
	for _, key := range slices.Sorted(maps.Keys(m)) {
		out = append(out, m[key])
	}
	return out
}
