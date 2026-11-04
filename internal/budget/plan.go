package budget

import (
	"slices"
	"strings"
	"time"

	"github.com/kentrow/tapyr/internal/config"
)

// PlanScope describes a limit scope a job contributes to, for the planner.
type PlanScope struct {
	// ID identifies the scope: limit id and scope key.
	ID    string
	Limit string
	Key   string
	Kind  config.LimitKind
	// PerInstance is true when the scope is partitioned by a variable of
	// the fan-out: every instance of the job has its own scope key.
	PerInstance bool

	// Quota scopes.
	Usable  int64
	Reserve int64
	Used    int64
	// Remaining is what Tapyr may still spend in the window: its usable
	// budget less its consumption, and no more than the remaining reported
	// by the provider, which includes the consumption of other clients.
	Remaining int64
	// External estimates the consumption of other clients in the window,
	// from the remaining reported by the provider.
	External    int64
	WindowStart time.Time
	WindowEnd   time.Time
	// WindowLength is the length of a full window (the current one for
	// calendar windows, the duration for rolling ones).
	WindowLength time.Duration
	Rolling      bool

	// Rate scopes: tokens per second.
	Rate float64
}

// PlanScopes returns the quota and rate scopes a request of d contributes
// to. Variables in fanoutVars come from discovery: their values are not
// known, and scopes partitioned by them are per instance.
func (e *Engine) PlanScopes(d Descriptor, fanoutVars []string) []PlanScope {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock.Now()
	vars := map[string]string{}
	for k, v := range d.Vars {
		vars[k] = v
	}
	for _, v := range fanoutVars {
		vars[v] = "*"
	}
	d.Vars = vars
	var out []PlanScope
	for _, l := range e.limits[d.Provider] {
		if l.kind == config.LimitConcurrency || !l.applies(&d) {
			continue
		}
		s := e.state(l, &d, now)
		ps := PlanScope{ID: l.id + "|" + s.key, Limit: l.id, Key: s.key, Kind: l.kind}
		for _, dim := range l.scope {
			if name, ok := strings.CutPrefix(dim, "var:"); ok && slices.Contains(fanoutVars, name) {
				ps.PerInstance = true
			}
		}
		switch l.kind {
		case config.LimitRate:
			ps.Rate = e.rate(s)
		case config.LimitQuota:
			q := e.quotaState(s, now)
			ps.Usable, ps.Reserve, ps.Used, ps.Remaining, ps.External = q.Usable, q.Reserve, q.Used, q.Remaining, q.ExternalUsage
			if s.rolling != nil {
				ps.Rolling, ps.WindowLength = true, l.rolling
				ps.WindowStart, ps.WindowEnd = now.Add(-l.rolling), now
			} else {
				ps.WindowStart, ps.WindowEnd = s.windowStart, s.windowEnd
				ps.WindowLength = s.windowEnd.Sub(s.windowStart)
			}
		}
		out = append(out, ps)
	}
	return out
}
