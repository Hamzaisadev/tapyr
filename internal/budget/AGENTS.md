# AGENTS.md - budget engine and planner

Rules for `internal/budget` and `internal/budget/planner`, on top of the [root `AGENTS.md`](../../AGENTS.md). The specification is [`04-budget-engine.md`](../../docs/spec/04-budget-engine.md): read it before any change here, it is the heart of the project.

## Invariants

- **Nothing reaches a provider without a reservation.** Requests, retries, pages, auth and token requests, discovery calls and logouts all go through `Reserve`; a new kind of request is a new descriptor, never a bypass. `Check` is the enforcer of the simulator, with the full budget and no waiting.
- **The limits come from the provider definitions.** No limit, window, rate or penalty is hardcoded here; defaults live in the configuration types and the spec.
- **Time is the injected clock.** Windows, refills, penalties, waits and projections read `clock.Clock`; never `time.Now`, `time.Sleep` or `time.After`. Calendar windows are computed in the timezone of the limit and stay correct across DST changes.
- **Quota is never overspent.** Tapyr's consumption stays within its usable budget (its share of the effective max) and within the remaining reported by the provider; the retry reserve is kept for retries and critical jobs.
- **Critical jobs are never stretched** by the planner; infeasibility is reported, not hidden.
- **The planner is a pure function** of jobs, scopes and options (`planner.Plan`): the same code serves `tapyr run` (online, with the remaining budget and time) and `tapyr plan` (offline, over a full window).
- **State survives restarts**: the ledger is persisted and restored, expired windows discarded. A change of the persisted format needs a migration in `internal/state`.
- **The engine is concurrent**: every state change holds `e.mu`; waiters are woken in priority order. Run `go test -race`.

## Tests

- Table-driven tests with `clock.Fake`; daily and monthly windows are tested in milliseconds by advancing the fake clock.
- `property_test.go` checks the invariants under random workloads: extend it when you add a limit kind or a scope dimension.
- Coverage stays at 90% or more for both packages: `go test -cover ./internal/budget/...`.
- Behavior seen by users is also covered end to end: the 24-hour acceptance tests in [`cmd/tapyr/acceptance_test.go`](../../cmd/tapyr/acceptance_test.go) run Tapyr and a simulator on a shared fake clock (`go test -run TwentyFourHours ./cmd/tapyr`). A change of pacing or planning must keep them green, and the contract samples of the providers may move with it (`make providers-update`, then explain the diff).
- A bug found here gets a regression test at the lowest level that shows it, and an acceptance test when it only appears with a simulator.
