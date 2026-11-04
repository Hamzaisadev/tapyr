package planner

import (
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/budget"
	"github.com/kentrow/tapyr/internal/config"
)

var now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func daily(id string, usable, reserve int64) budget.PlanScope {
	start := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	return budget.PlanScope{
		ID: id, Limit: "p/" + id, Key: "account=a", Kind: config.LimitQuota, Usable: usable, Reserve: reserve, Remaining: usable,
		WindowStart: start, WindowEnd: start.Add(24 * time.Hour), WindowLength: 24 * time.Hour,
	}
}

func job(name string, p config.Priority, desired time.Duration, pages float64, scopes ...budget.PlanScope) Job {
	return Job{Name: name, Provider: "p", Account: "a", Priority: p, Desired: desired, PagesPerRun: pages, Fanout: 1, Scopes: scopes}
}

func TestPlan(t *testing.T) {
	q := daily("daily", 5000, 500) // budget 4500 per day
	tests := []struct {
		name         string
		jobs         []Job
		opts         Options
		wantEff      map[string]time.Duration
		wantFeasible bool
	}{
		{
			name: "fits",
			// 288 runs x 3 pages + 288 x 1 = 1152 per day.
			jobs:    []Job{job("aps", config.PriorityHigh, 5*time.Minute, 3, q), job("switches", config.PriorityNormal, 5*time.Minute, 1, q)},
			wantEff: map[string]time.Duration{"aps": 5 * time.Minute, "switches": 5 * time.Minute}, wantFeasible: true,
		},
		{
			name: "low class stretched",
			// aps 864 + switches 288 = 1152 fixed; clients 288 x 30 = 8640 must
			// fit in 3348: stretch 2.58.
			jobs: []Job{
				job("aps", config.PriorityHigh, 5*time.Minute, 3, q),
				job("switches", config.PriorityNormal, 5*time.Minute, 1, q),
				job("clients", config.PriorityLow, 5*time.Minute, 30, q),
			},
			wantEff:      map[string]time.Duration{"aps": 5 * time.Minute, "switches": 5 * time.Minute, "clients": 775 * time.Second},
			wantFeasible: true,
		},
		{
			name: "low capped, normal stretched next",
			// low: 288 x 100 = 28800 at k=1, 2400 at k=12 (max); critical 2880;
			// normal must fit in 4500 - 2880 - 2400 = -780: impossible ->
			// infeasible, everything stretchable at max.
			jobs: []Job{
				job("critical", config.PriorityCritical, 5*time.Minute, 10, q),
				job("normal", config.PriorityNormal, 5*time.Minute, 10, q),
				job("low", config.PriorityLow, 5*time.Minute, 100, q),
			},
			opts:         Options{MaxStretch: 12},
			wantEff:      map[string]time.Duration{"critical": 5 * time.Minute, "normal": time.Hour, "low": time.Hour},
			wantFeasible: false,
		},
		{
			name: "normal stretched after low reaches its cap",
			// critical 1440; low 28800 -> 2400 at max 12; normal 2880 must fit
			// 4500 - 1440 - 2400 = 660: stretch 4.36.
			jobs: []Job{
				job("critical", config.PriorityCritical, 5*time.Minute, 5, q),
				job("normal", config.PriorityNormal, 5*time.Minute, 10, q),
				job("low", config.PriorityLow, 5*time.Minute, 100, q),
			},
			opts:         Options{MaxStretch: 12},
			wantEff:      map[string]time.Duration{"critical": 5 * time.Minute, "normal": 1310 * time.Second, "low": time.Hour},
			wantFeasible: true,
		},
		{
			name:         "critical alone does not fit",
			jobs:         []Job{job("critical", config.PriorityCritical, time.Minute, 5, q)},
			wantEff:      map[string]time.Duration{"critical": time.Minute},
			wantFeasible: false,
		},
		{
			name: "largest stretch across scopes",
			// small: budget 90; low job 24 x 10 = 240 on both: needs 2.67 on
			// small, 1 on big.
			jobs: []Job{job("both", config.PriorityLow, time.Hour, 10, daily("small", 100, 10), daily("big", 10000, 1000))},
			opts: Options{MaxStretch: 12}, wantEff: map[string]time.Duration{"both": 9600 * time.Second}, wantFeasible: true,
		},
		{
			name: "fan-out multiplies, per-instance scopes do not",
			jobs: []Job{func() Job {
				perOrg := daily("per-org", 300, 0)
				perOrg.PerInstance = true
				j := job("orgs", config.PriorityLow, time.Hour, 10, daily("account", 10000, 0), perOrg)
				j.Fanout = 50
				return j
			}()},
			// account: 50 x 240 = 12000 > 10000 -> 1.2; per-org: 240 fits.
			opts: Options{MaxStretch: 12}, wantEff: map[string]time.Duration{"orgs": 72 * time.Minute}, wantFeasible: true,
		},
		{
			name: "online uses the remaining budget and time",
			// Half the day left, 4000 used: 500 left for 144 runs x 10.
			jobs: []Job{func() Job {
				s := daily("daily", 5000, 500)
				s.Used, s.Remaining = 4000, 1000
				return job("devices", config.PriorityLow, 5*time.Minute, 10, s)
			}()},
			opts:    Options{MaxStretch: 12, Online: true, Now: now},
			wantEff: map[string]time.Duration{"devices": 864 * time.Second}, wantFeasible: true,
		},
		{
			name: "online counts the consumption of other clients",
			// Tapyr used 1000, but the provider reports 1000 left: another
			// client spent 3000. The same 500 are left for half a day.
			jobs: []Job{func() Job {
				s := daily("daily", 5000, 500)
				s.Used, s.Remaining = 1000, 1000
				return job("devices", config.PriorityLow, 5*time.Minute, 10, s)
			}()},
			opts:    Options{MaxStretch: 12, Online: true, Now: now},
			wantEff: map[string]time.Duration{"devices": 864 * time.Second}, wantFeasible: true,
		},
		{
			name: "online projects the consumption of other clients",
			// Noon: another client spent 1000 in 12 hours and will spend
			// 1000 more. Of the 2500 left, 2500 - 500 - 1000 = 1000 remain
			// for 144 runs x 10.
			jobs: []Job{func() Job {
				s := daily("daily", 5000, 500)
				s.Used, s.Remaining, s.External = 1500, 2500, 1000
				return job("devices", config.PriorityLow, 5*time.Minute, 10, s)
			}()},
			opts:    Options{MaxStretch: 12, Online: true, Now: now},
			wantEff: map[string]time.Duration{"devices": 432 * time.Second}, wantFeasible: true,
		},
		{
			name: "rate feasibility",
			// 600 pages at 1/s need 10 minutes.
			jobs: []Job{job("big", config.PriorityNormal, time.Minute, 600, budget.PlanScope{
				ID: "rate", Limit: "p/rate", Key: "account=a", Kind: config.LimitRate, Rate: 1,
			})},
			wantEff: map[string]time.Duration{"big": 10 * time.Minute}, wantFeasible: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.opts.MaxStretch == 0 {
				tt.opts.MaxStretch = 12
			}
			res := Plan(tt.jobs, tt.opts)
			if res.Feasible != tt.wantFeasible {
				t.Errorf("feasible = %v, want %v (%+v)", res.Feasible, tt.wantFeasible, res.Scopes)
			}
			for _, j := range res.Jobs {
				if want := tt.wantEff[j.Name]; j.Effective != want {
					t.Errorf("job %s: effective %v, want %v (reason %q)", j.Name, j.Effective, want, j.Reason)
				}
			}
			for _, s := range res.Scopes {
				if s.Feasible && s.Projected > s.Budget+1 {
					t.Errorf("scope %s marked feasible with %.1f projected for %.1f", s.ID, s.Projected, s.Budget)
				}
			}
		})
	}
}

func TestPlanDetails(t *testing.T) {
	q := daily("daily", 5000, 500)
	j := job("clients", config.PriorityLow, 5*time.Minute, 30, q)
	j.RetryOverhead = DefaultRetryOverhead
	res := Plan([]Job{j}, Options{MaxStretch: 12, Now: now})
	p := res.Jobs[0]
	if p.CallsPerRun != 31.5 || p.Reason != "quota p/daily account=a" || p.Describe() == "" {
		t.Errorf("plan = %+v", p)
	}
	s := res.Scopes[0]
	if s.Budget != 4500 || s.Reserve != 500 || s.Period != 24*time.Hour || s.Projected > 4500 || s.Jobs[0] != "clients" {
		t.Errorf("scope = %+v", s)
	}
	infeasible := Plan([]Job{job("c", config.PriorityCritical, time.Minute, 10, q)}, Options{MaxStretch: 12, Now: now})
	if sc := infeasible.Scopes[0]; sc.Feasible || sc.ExhaustionAt.IsZero() || !sc.ExhaustionAt.Before(now.Add(24*time.Hour)) {
		t.Errorf("infeasible scope = %+v", sc)
	}
}
