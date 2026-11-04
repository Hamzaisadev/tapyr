# ADR 0005 - Embedded SQLite for persistent state

- Status: accepted
- Date: 2026-11-02

## Context

Daily and monthly quotas must survive restarts, rotating refresh tokens must never be lost, inventory caches avoid wasting quota, and the series registry drives staleness across restarts. The MVP is single-instance and must not require a database server.

## Decision

Use embedded SQLite through `modernc.org/sqlite` (pure Go), behind a `state.Store` interface. Default path `./data/tapyr.db`. Batched writes, flush every few seconds and on shutdown. Tokens encrypted at rest when `TAPYR_STATE_KEY` is set.

## Alternatives considered

- **bbolt / badger**: key-value only; SQLite makes run history and timelines easier to query.
- **Plain JSON files**: fragile under crashes and concurrency.
- **Redis**: requires a server; kept as a possible backend for the future distributed mode.

## Consequences

- Single static binary preserved.
- The `state.Store` interface keeps a future shared backend possible.
- A volume is required in containers for persistence (documented).
