# ADR 0004 - Declarative provider packages shared by collector and simulator

- Status: accepted
- Date: 2026-11-02

## Context

Supporting a new API should not require Go code, and providers must be testable without vendor access. Rate limits declared in one place must drive both the collector (to respect them) and the simulator (to enforce them).

## Decision

- Vendor knowledge lives in provider packages: `provider.yaml` (auth, limits, pagination, errors), job templates, simulator definition and fixtures, dashboards, contract tests, README.
- The simulator reads the same `Provider` document and reuses the budget package server-side to enforce limits.
- Go code is limited to generic mechanisms and auth plugins for flows that cannot be expressed declaratively.
- Packages are versioned (semver); built-in packages are embedded in the binary.

## Alternatives considered

- **Go plugins per vendor** (Terraform-style): maximum flexibility, but high contribution barrier and compiled distribution.
- **Separate mock servers** (e.g. generic mocking tools): limits would be declared twice and drift.

## Consequences

- Community contributions are accessible to engineers who know an API but not Go.
- A provider package is self-contained and testable in CI with no network.
- The declarative model must be rich enough; when it is not, we extend the generic mechanisms rather than adding vendor code.
- Opens the door to OCI distribution and a registry later.
