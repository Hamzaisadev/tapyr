# ADR 0002 - CEL as the mapping and expression language

- Status: accepted
- Date: 2026-11-02

## Context

Provider packages must transform arbitrary JSON responses into metrics, compute pagination conditions, extract tokens, and filter discovery items. Provider packages are meant to be written by the community and executed by every Tapyr user, so the language must be safe, predictable and checkable before runtime.

## Decision

Use CEL (Common Expression Language, `github.com/google/cel-go`) for every runtime expression, wrapped in a declarative structure (`items`, `metrics`, `labels`, `value`...) so most mappings stay trivial.

## Alternatives considered

| Option | Why not |
|---|---|
| jq (gojq) | Powerful and well known, but untyped, termination not guaranteed, harder to bound in cost; risky for community packages |
| JSONPath (RFC 9535) | Standardized, but selection only, no computation |
| JMESPath | Selection-oriented, limited computation, less active ecosystem |
| OTTL | Designed to transform existing OpenTelemetry data inside the Collector, not arbitrary JSON |
| expr-lang | Pleasant, but library-specific rather than a specification |
| Lua / JavaScript | Turing-complete, sandboxing burden, too much power for mappings |

## Consequences

- Non Turing-complete: expressions always terminate; cost can be estimated and capped.
- Type-checked at compile time: `tapyr validate` catches errors before deployment.
- Proven in Kubernetes (validation rules, admission policies), Envoy and Google IAM: a known, specified language with a future.
- Extensible with custom functions (timestamps, sizes, durations, MAC normalization).
- Slightly less familiar to network engineers than jq: documentation must include a mapping cookbook.
