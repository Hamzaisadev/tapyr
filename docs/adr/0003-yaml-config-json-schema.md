# ADR 0003 - YAML configuration with a Kubernetes-like envelope and generated JSON Schemas

- Status: accepted
- Date: 2026-11-02

## Context

Configuration must be declarative, Git-friendly, splittable across files, validated precisely, and pleasant to edit with autocompletion. Provider packages and user configuration share the same format.

## Decision

- YAML, multi-document, with `apiVersion` / `kind` / `metadata` / `spec`.
- `apiVersion: tapyr/v1alpha1`, bumped on breaking changes.
- Go types are the source of truth; JSON Schemas are generated from them and committed in `schemas/`.
- Documents can live in any number of files and directories; references are by name.

## Alternatives considered

- **HCL or Alloy syntax**: expressive, but less universal tooling and a steeper learning curve for users.
- **TOML** (Telegraf style): awkward for deeply nested structures such as mappings.
- **Single monolithic file**: does not scale to many providers and accounts.

## Consequences

- Editors (VS Code YAML extension) get validation and autocompletion through the schemas.
- The documentation reference can be generated from the schemas.
- Versioned format from day one, a prerequisite for a community provider ecosystem.
