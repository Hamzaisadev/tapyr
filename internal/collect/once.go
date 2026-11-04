package collect

import (
	"context"
	"slices"
	"time"

	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/mapping"
	"github.com/kentrow/tapyr/internal/runtime"
)

// Visit is the outcome of one instance run by RunOnce.
type Visit struct {
	Job *runtime.Job
	// Result is nil when the requests failed.
	Result *Result
	// Output is nil when the requests or the mapping failed.
	Output *mapping.Result
	Err    error
	// Skipped lists the inventory items that gave no child, with the
	// reason (filters excluded).
	Skipped []error
}

// RunOnce runs every instance of jobs once, in a stable order: the
// configured instances sorted by instance key, each followed by the
// children its inventory creates (breadth first), as tapyr run would
// instantiate them. Instances run one after the other, without a
// deadline; visit is called after each one. It serves tapyr record and the
// contract tests of tapyr provider lint.
func (e *Executor) RunOnce(ctx context.Context, jobs []*runtime.Job, visit func(Visit)) {
	models := map[string][]*runtime.Job{}
	var queue []*runtime.Job
	for _, j := range jobs {
		if j.ForEach != nil {
			models[j.ForEach.ParentJob] = append(models[j.ForEach.ParentJob], j)
			continue
		}
		queue = append(queue, j)
	}
	slices.SortStableFunc(queue, func(a, b *runtime.Job) int {
		switch {
		case a.Instance < b.Instance:
			return -1
		case a.Instance > b.Instance:
			return 1
		}
		return 0
	})
	seen := map[string]bool{}
	for len(queue) > 0 && ctx.Err() == nil {
		job := queue[0]
		queue = queue[1:]
		if seen[job.Instance] {
			continue
		}
		seen[job.Instance] = true
		now := e.clock.Now()
		v := Visit{Job: job}
		res, err := e.Run(ctx, job, now, time.Time{})
		if v.Err = err; err == nil {
			v.Result = res
		}
		if v.Err == nil {
			v.Output, v.Err = job.Extract.Evaluate(&mapping.Input{
				Pages: v.Result.Pages, Vars: job.Vars, Account: job.AccountInfo, Target: job.TargetInfo, Now: now, Labels: job.Labels,
			}, job.Limits)
		}
		if v.Output != nil && job.Role != config.RoleMetrics {
			for _, m := range models[job.Name] {
				if m.Target != job.Target {
					continue
				}
				n := 0
				for _, it := range v.Output.Inventory {
					child, ok, err := m.Child(job, it, now)
					if err != nil {
						v.Skipped = append(v.Skipped, err)
						continue
					}
					if !ok {
						continue
					}
					if m.ForEach.MaxInstances > 0 && n >= m.ForEach.MaxInstances {
						break
					}
					n++
					queue = append(queue, child)
				}
			}
		}
		visit(v)
	}
}
