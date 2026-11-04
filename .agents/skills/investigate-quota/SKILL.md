---
name: investigate-quota
description: Explain why Tapyr received a 429, ran out of a quota, stretched jobs or reports an infeasible plan, from tapyr plan, the status API, the self-metrics, the logs and the simulator, and propose a fix in the configuration or the provider definition. Use for "why 429", "quota exhausted", "jobs stretched", "plan infeasible" or "Tapyr polls too little" questions. Not for auth failures (401, 403) or mapping errors.
---

# Investigate a quota problem

The budget engine is supposed to make quota problems impossible: every request goes through a reservation, limits come from the provider definition, the planner stretches low-priority jobs to fit. When something goes wrong anyway, one of its inputs is wrong. Find which one, with evidence, before changing anything. Read [`04-budget-engine.md`](../../../docs/spec/04-budget-engine.md) for the model.

## 1. Gather the facts

From a running `tapyr run` (default `http://127.0.0.1:9790`; add basic auth if configured):

| Question | Where |
|---|---|
| Which limit, which scope, how much is used and left, by Tapyr and by others? | `GET /api/v1/budgets` (`used`, `remaining`, `remaining_observed`, `external_usage`, `reserve`, `window_start`, `window_end`, `exhaustion_eta_seconds`) |
| When was it consumed? | `GET /api/v1/budgets/{provider}%2F{limit}/timeline?scope=...` (per-minute buckets, current and previous window) |
| What does the planner think? | `GET /api/v1/plan`, or `tapyr plan --config <paths> --output json` offline |
| Which jobs run, how often, with how many requests and retries? | `GET /api/v1/jobs`, `GET /api/v1/jobs/{name}/runs` |
| What did the provider answer? | `GET /api/v1/debug/jobs/{name}/last` (status, quota headers; with `settings.server.debug.enabled`) |
| Counters over time | `/metrics/self`: `tapyr_budget_rate_limited_total`, `tapyr_budget_unexpected_429_total`, `tapyr_budget_refusals_total{reason}`, `tapyr_budget_external_usage`, `tapyr_budget_limit_mismatch`, `tapyr_http_retries_total{reason}`, `tapyr_job_effective_interval_seconds` |
| Decisions | logs: `msg="rate limited"` (with `unexpected`), `msg="effective interval changed"` (with `reason`), `quota_exhausted` refusals |

The UI shows the same data (Budgets, Plan, Jobs, Live debug). Ask the person for these outputs rather than for credentials; never call the real API yourself to "check".

## 2. Match a cause

| Symptom | Likely cause | Evidence |
|---|---|---|
| `unexpected_429` grows, quota not exhausted for Tapyr | The provider enforces a limit the definition does not declare, or declares too high, or scopes differently (per IP or per organization instead of per account) | Vendor documentation; the debug headers; whether several accounts or tools share a token or an IP |
| `external_usage` high, quota runs out early | Another client uses the same quota (the vendor UI, a script, another integration) | `external_usage` growing while Tapyr is idle; the timeline |
| `limit_mismatch` is 1 | The headers announce another maximum than the definition (plan tier, account size) | `tapyr_budget_limit` vs the definition; use `max: auto` with `initialMax` |
| Consumption above the plan | Fan-out larger than estimated, `pagesPerRun` too low, many retries | `tapyr plan` jobs (`calls_per_run`, `fanout`) vs the runs (`requests`, `pages`, `retries`) |
| Low-priority jobs stretched to `maxStretch` | The quota is genuinely too small for the desired intervals | `tapyr plan` scope `budget` vs `projected` |
| Plan infeasible | Critical jobs alone exceed the quota | `tapyr plan` exit code 3, the critical jobs of the scope |
| `quota_exhausted` refusals right after a restart | The state store was lost (no volume), so consumption restarted at 0 while the provider remembered | `ledger_entries=0` in the startup log; the observed remaining |

## 3. Reproduce with the simulator

When the cause is not obvious, reproduce it without spending real quota: `tapyr sim --provider <name> --scenario quota-tight` (or a scenario written for the case: `limits`, `externalConsumption`, `errors`), a configuration pointing to it, and `--time-scale` to run a day in minutes. The acceptance tests in `cmd/tapyr/acceptance_test.go` show how to drive 24 simulated hours on a fake clock; a confirmed bug of the engine gets such a test.

## 4. Propose a fix

In order of preference, and say which evidence supports it:

1. **Configuration**: longer intervals, lower priorities for what can wait, fewer fan-out instances (filters, `forEach.maxInstances`), a `budget.share` when the quota is shared on purpose, a volume for the state store.
2. **Provider definition** (with a source for the new value): a missing limit, a corrected maximum or scope, `max: auto`, the quota headers in `sync`, a better `estimate.pagesPerRun`. Follow [`providers/AGENTS.md`](../../../providers/AGENTS.md).
3. **Engine** (only when the inputs were right and the engine still spent too much): an issue with the evidence and a reproduction in the simulator, then a fix with its regression test (see [`internal/budget/AGENTS.md`](../../../internal/budget/AGENTS.md)).
