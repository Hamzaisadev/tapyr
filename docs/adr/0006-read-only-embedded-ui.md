# ADR 0006 - Read-only web UI embedded in the binary

- Status: accepted
- Date: 2026-11-02

## Context

Users need to see what Tapyr is doing (providers, jobs, budgets, plan, errors) quickly, in the spirit of the Grafana Alloy UI. Configuration must stay declarative and versioned in Git.

## Decision

- A read-only web UI in the MVP, built with React + Vite + TypeScript, compiled and embedded with `go:embed`.
- The UI consumes only the public read-only status API (`/api/v1`) and SSE events.
- No editing capability in the UI.

## Alternatives considered

- **No UI (Grafana only)**: Grafana covers metrics, but not effective configuration, plan, fan-out instances or live mapping debug.
- **Server-rendered UI (Go templates + htmx)**: lighter build chain, but the UI will grow (graph, timelines, future mapping assistant and impact simulation) and benefits from a component ecosystem (`@xyflow/react`).
- **Editable UI**: conflicts with configuration-as-code and multiplies complexity.

## Consequences

- The status API is designed first and stays the single contract, which keeps other clients possible.
- The build requires Node in CI; end users still get one binary.
- Security: the UI shows infrastructure details; default listen on localhost, optional basic auth.
