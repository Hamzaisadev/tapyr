# ADR 0009 - Window values exposed as gauges by default

- Status: accepted
- Date: 2026-11-02

## Context

Many vendor APIs return values aggregated over a time window ("bytes over the last 5 minutes", "average over the last hour") instead of cumulative counters.

## Decision

Expose window values as gauges by default, with names stating the window (`..._5m`). Offer optional counter reconstruction (`type: counter`, `accumulate: true`) that sums non-overlapping windows into a persisted monotonic counter.

## Alternatives considered

- **Always reconstruct counters**: elegant for PromQL `rate()`, but fragile with overlapping or irregular windows and misleading after gaps.
- **Expose raw without naming conventions**: ambiguous for users.

## Consequences

- Honest default semantics; users query gauges directly.
- Reconstruction available where windows are reliable.
- Provider package reviews must check window naming.
