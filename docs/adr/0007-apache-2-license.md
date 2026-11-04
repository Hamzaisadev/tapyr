# ADR 0007 - Apache License 2.0

- Status: accepted
- Date: 2026-11-02

## Context

Tapyr aims at broad adoption, including in companies, and at a community of provider contributors.

## Decision

License the project under Apache License 2.0.

## Alternatives considered

- **MIT**: similar permissiveness, but no explicit patent grant.
- **AGPL**: protects against closed SaaS reuse but discourages corporate adoption and contributions.

## Consequences

- Same license as Prometheus and OpenTelemetry, familiar to the target audience.
- Explicit patent grant reassures companies.
- Contributions are accepted under the same license (DCO sign-off recommended in CONTRIBUTING).

## Notes

- 2026-11-04: DCO sign-off is not required. The inbound=outbound terms of [`CONTRIBUTING.md`](../../CONTRIBUTING.md#license) apply: contributions are licensed under the Apache License 2.0, like the project.
