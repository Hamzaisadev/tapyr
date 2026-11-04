package budget

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kentrow/tapyr/internal/config"
)

// maxMinuteBuckets is the longest window counted per minute; longer windows
// are counted per hour.
const maxMinuteBuckets = 24 * time.Hour

// bucketWidth returns the timeline bucket of a window length.
func bucketWidth(window time.Duration) time.Duration {
	if window > maxMinuteBuckets {
		return time.Hour
	}
	return time.Minute
}

// record counts one unit in the history of a fixed window, keyed by the
// minute index of the bucket start.
func (s *state) record(now time.Time) {
	if s.history == nil {
		s.history = map[int64]int64{}
	}
	width := int64(bucketWidth(s.windowEnd.Sub(s.windowStart)) / time.Second)
	s.history[now.Unix()/width*width/60]++
}

// TimelineWindow is the consumption of one window, per bucket.
type TimelineWindow struct {
	Start, End time.Time
	Buckets    []Bucket
}

// Bucket counts the units consumed from Start for the bucket width.
type Bucket struct {
	Start time.Time
	Count int64
}

// Timeline is the consumption of a quota scope over its current and
// previous windows.
type Timeline struct {
	Provider, Limit, Scope string
	// Bucket is the width of the buckets: 1 minute for windows up to a
	// day, 1 hour beyond.
	Bucket   time.Duration
	Current  TimelineWindow
	Previous *TimelineWindow
}

// Timeline returns the consumption of a quota scope (limit name and scope
// key); ok is false for an unknown scope or a limit that is not a quota.
func (e *Engine) Timeline(provider, limit, scope string) (Timeline, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.states[provider+"/"+limit+"|"+scope]
	if !ok || s.l.kind != config.LimitQuota {
		return Timeline{}, false
	}
	now := e.clock.Now()
	t := Timeline{Provider: provider, Limit: limit, Scope: scope}
	if s.rolling != nil {
		s.rolling.prune(now)
		t.Bucket = time.Minute
		t.Current = TimelineWindow{Start: now.Add(-s.l.rolling), End: now, Buckets: buckets(s.rolling.buckets, now.Add(-s.l.rolling))}
		return t, true
	}
	e.roll(s, now)
	t.Bucket = bucketWidth(s.windowEnd.Sub(s.windowStart))
	t.Current = TimelineWindow{Start: s.windowStart, End: s.windowEnd, Buckets: buckets(s.history, s.windowStart)}
	if !s.prevEnd.IsZero() {
		t.Previous = &TimelineWindow{Start: s.prevStart, End: s.prevEnd, Buckets: buckets(s.prevHistory, s.prevStart)}
	}
	return t, true
}

// buckets returns the non-empty buckets in time order, from start.
func buckets(m map[int64]int64, start time.Time) []Bucket {
	out := []Bucket{}
	for _, minute := range slices.Sorted(maps.Keys(m)) {
		at := time.Unix(minute*60, 0).UTC()
		if m[minute] == 0 || at.Before(start.Truncate(time.Minute)) {
			continue
		}
		out = append(out, Bucket{Start: at, Count: m[minute]})
	}
	return out
}

// LimitState describes a rate or concurrency limit for one scope key.
type LimitState struct {
	Provider, Limit, Scope string
	Kind                   config.LimitKind
	// Rate limits: tokens per second, burst and tokens available.
	Rate, Burst, Tokens float64
	// Concurrency limits: maximum and requests in flight.
	Max, InUse int64
	// PenaltyUntil is the end of a 429 penalty, zero when none.
	PenaltyUntil time.Time
}

// Limits returns the state of every rate and concurrency scope seen so far,
// sorted by limit and scope.
func (e *Engine) Limits() []LimitState {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock.Now()
	var out []LimitState
	for _, s := range e.states {
		ls := LimitState{Provider: s.l.provider, Limit: s.l.name, Scope: s.key, Kind: s.l.kind}
		switch s.l.kind {
		case config.LimitRate:
			e.refill(s, now)
			ls.Rate, ls.Burst, ls.Tokens = e.rate(s), e.burst(s), s.tokens
		case config.LimitConcurrency:
			ls.Max, ls.InUse = e.concurrency(s), s.inUse
		default:
			continue
		}
		if now.Before(s.penaltyUntil) {
			ls.PenaltyUntil = s.penaltyUntil
		}
		out = append(out, ls)
	}
	slices.SortFunc(out, func(a, b LimitState) int {
		if c := strings.Compare(a.Provider+"/"+a.Limit, b.Provider+"/"+b.Limit); c != 0 {
			return c
		}
		return strings.Compare(a.Scope, b.Scope)
	})
	return out
}
