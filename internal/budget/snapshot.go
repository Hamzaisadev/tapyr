package budget

import (
	"slices"
	"strings"
	"time"

	"github.com/kentrow/tapyr/internal/config"
)

// QuotaState describes one quota limit for one scope key.
type QuotaState struct {
	Provider string
	Limit    string
	Scope    string
	// Max is the effective maximum M of the window (declared, learned from
	// headers, or initialMax).
	Max int64
	// Usable is U = floor(M x share); Reserve is R = floor(U x retryReserve).
	Usable  int64
	Reserve int64
	// Used is Tapyr's consumption in the current window.
	Used int64
	// Remaining is Tapyr's remaining budget: min(U - used, observed
	// remaining).
	Remaining int64
	// ObservedRemaining is the remaining reported by the provider, adjusted
	// for Tapyr's own use since the observation; -1 when never observed.
	ObservedRemaining int64
	// ExternalUsage estimates the consumption of other clients.
	ExternalUsage int64
	// WindowStart and WindowEnd bound the current window (for rolling
	// windows: now minus the duration, and now).
	WindowStart time.Time
	WindowEnd   time.Time
	Rolling     bool
	// Mismatch reports a limit header disagreeing with the declared max.
	Mismatch bool
}

// Quotas returns the state of every quota scope seen so far, sorted by
// limit and scope.
func (e *Engine) Quotas() []QuotaState {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock.Now()
	var out []QuotaState
	for _, s := range e.states {
		if s.l.kind != config.LimitQuota {
			continue
		}
		out = append(out, e.quotaState(s, now))
	}
	slices.SortFunc(out, func(a, b QuotaState) int {
		if c := strings.Compare(a.Provider+"/"+a.Limit, b.Provider+"/"+b.Limit); c != 0 {
			return c
		}
		return strings.Compare(a.Scope, b.Scope)
	})
	return out
}

func (e *Engine) quotaState(s *state, now time.Time) QuotaState {
	if s.l.fixed != nil {
		e.roll(s, now)
	}
	m, u, r := e.quota(s)
	used := e.quotaUsed(s, now)
	q := QuotaState{
		Provider: s.l.provider, Limit: s.l.name, Scope: s.key, Max: m, Usable: u, Reserve: r,
		Used: used, Remaining: max(0, u-used), ObservedRemaining: -1,
		WindowStart: s.windowStart, WindowEnd: s.windowEnd,
		Mismatch: !s.l.auto && s.observedMax > 0 && s.observedMax != s.l.max,
	}
	if s.rolling != nil {
		q.Rolling, q.WindowStart, q.WindowEnd = true, now.Add(-s.l.rolling), now
	}
	if s.observed {
		obs := max(0, s.observedRemaining-(used-s.usedAtObservation))
		q.ObservedRemaining = obs
		q.Remaining = min(q.Remaining, obs)
		q.ExternalUsage = max(0, m-s.observedRemaining-s.usedAtObservation)
	}
	if now.Before(s.exhaustedUntil) {
		q.Remaining = 0
	}
	return q
}
