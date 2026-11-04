package budget

import (
	"maps"
	"slices"
	"time"

	"github.com/kentrow/tapyr/internal/config"
	store "github.com/kentrow/tapyr/internal/state"
)

// Export returns the ledger entries of every quota scope, for the state
// store.
func (e *Engine) Export() []store.LedgerEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock.Now()
	var out []store.LedgerEntry
	for _, s := range e.states {
		if s.l.kind != config.LimitQuota {
			continue
		}
		entry := store.LedgerEntry{
			Limit: s.l.id, Scope: s.key, WindowStart: s.windowStart, Used: s.used,
			Observed: s.observed, ObservedRemaining: s.observedRemaining, ObservedMax: s.observedMax,
			UsedAtObservation: s.usedAtObservation, ExhaustedUntil: s.exhaustedUntil,
			Accounts: slices.Sorted(maps.Keys(s.accounts)), UpdatedAt: now,
		}
		if s.rolling != nil {
			s.rolling.prune(now)
			entry.Minutes = maps.Clone(s.rolling.buckets)
		} else if len(s.history) > 0 {
			entry.Minutes = maps.Clone(s.history)
		}
		out = append(out, entry)
	}
	slices.SortFunc(out, func(a, b store.LedgerEntry) int {
		if a.Limit != b.Limit {
			if a.Limit < b.Limit {
				return -1
			}
			return 1
		}
		if a.Scope < b.Scope {
			return -1
		}
		if a.Scope > b.Scope {
			return 1
		}
		return 0
	})
	return out
}

// Restore loads ledger entries saved by a previous process. Entries of
// fixed windows that have ended are discarded; rolling windows keep their
// recent minutes. Entries of limits that no longer exist are ignored.
// It returns the number of entries restored.
func (e *Engine) Restore(entries []store.LedgerEntry) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock.Now()
	byID := map[string]*limit{}
	for _, limits := range e.limits {
		for _, l := range limits {
			byID[l.id] = l
		}
	}
	restored := 0
	for _, entry := range entries {
		l, ok := byID[entry.Limit]
		if !ok || l.kind != config.LimitQuota {
			continue
		}
		s := &state{l: l, key: entry.Scope, accounts: map[string]bool{}, refilled: now}
		for _, a := range entry.Accounts {
			s.accounts[a] = true
		}
		switch {
		case l.fixed != nil:
			start, end := l.fixed.bounds(now)
			if !entry.WindowStart.Equal(start) {
				continue // the window has ended
			}
			s.windowStart, s.windowEnd = start, end
			s.used = entry.Used
			s.history = maps.Clone(entry.Minutes)
		case l.rolling > 0:
			s.rolling = newRollingWindow(l.rolling)
			maps.Copy(s.rolling.buckets, entry.Minutes)
			s.rolling.prune(now)
		}
		s.observed, s.observedRemaining, s.observedMax = entry.Observed, entry.ObservedRemaining, entry.ObservedMax
		s.usedAtObservation = entry.UsedAtObservation
		if entry.ExhaustedUntil.After(now) {
			s.exhaustedUntil = entry.ExhaustedUntil
		}
		e.states[l.id+"|"+entry.Scope] = s
		restored++
	}
	return restored
}

// FlushInterval is the cadence at which the ledger is persisted.
const FlushInterval = 5 * time.Second
