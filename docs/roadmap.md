# Roadmap

v0.1.0 (milestones M0 to M7) is complete.

## After v0.1.0

Candidates, not committed. Each one starts with an issue and an agreement on its design (an ADR when it changes the architecture); the items listed as out of scope in [`01-vision-and-scope.md`](spec/01-vision-and-scope.md) need that agreement before any code.

- Validation of `meraki-dashboard` and `aruba-central-classic` against real platforms (reports welcome).
- Move the collector orchestration out of `cmd/tapyr` into an internal package (see [`02-architecture.md`](spec/02-architecture.md#repository-layout)).
- Documentation site docs.tapyr.eu built from `docs/` and the JSON Schemas ([ADR 0010](adr/0010-documentation-site.md), [#7](https://github.com/Kentrow/tapyr/issues/7)).
- Aruba Central "new" (GreenLake) provider.
- UI: budget impact simulation, mapping authoring assistant.
- Webhook and streaming sources.
- Persistent output buffering (WAL), Prometheus remote write 2.0.
- Distributed mode (workers, shared state backend, leader election).
- Provider distribution as OCI artifacts and a community registry.
- Provider skeleton generation from OpenAPI specs.

## Delivered in v0.1.0

| Milestone | Delivered |
|---|---|
| M0 - Bootstrap | Go module, Makefile, CI, injected clock, CLI skeleton, configuration types and loader, JSON Schemas with `file:line:column` errors |
| M1 - Minimal pipeline | Provider packages, collection executor, CEL mapping, `prometheus` output, scheduler, minimal simulator, `example-sim` |
| M2 - Budget engine | Rate, concurrency and quota limits with scopes, state store and ledger, header sync, 429 penalties, retries and circuit breakers, planner and `tapyr plan`, simulator limit enforcement |
| M3 - Discovery and series lifecycle | `forEach` fan-out and inventory cache, multi-target accounts, staleness and deduplication, every pagination type, `oauth2_client_credentials` and `session_login`, hot reload |
| M4 - Outputs and self-observability | `remote_write` and `otlp` outputs, self-metrics, `/healthz` and `/readyz`, status API with its OpenAPI description, log redaction |
| M5 - Providers | `teltonika-rms`, `teltonika-rutos`, `meraki-dashboard`, `aruba-central-classic` with simulators, scenarios, dashboards and contract tests; `tapyr record`; `tapyr provider lint`; Teltonika e2e tests |
| M6 - Read-only web UI | React UI embedded in the binary, debug endpoint and Server-Sent Events |
| M7 - Packaging and release | Multi-arch distroless image, GoReleaser archives with checksums, SBOMs and cosign signatures, demo compose, example alerts, documentation |
