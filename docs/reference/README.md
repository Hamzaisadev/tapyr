<!-- Generated from the JSON Schemas by make schema (internal/tools/schemagen). Do not edit. -->

# Configuration reference

Every field of every document kind of `tapyr/v1alpha1`, generated from the [JSON Schemas](../../schemas/v1alpha1/) that Tapyr validates configuration files with. The [configuration model](../spec/03-configuration.md) explains the kinds and shows examples. Editors validate and complete configuration files with the combined schema `tapyr.json`, for example with the YAML language server:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/kentrow/tapyr/main/schemas/v1alpha1/tapyr.json
```

## Configuration

The documents of a Tapyr configuration.

| Kind | Description |
|---|---|
| [Account](account.md) | An instance of a provider with credentials, params, targets and labels. |
| [Job](job.md) | What to collect for an account, from a template or inline. |
| [Output](output.md) | Where collected samples are sent. |
| [Settings](settings.md) | Global settings (at most one document). |

## Provider packages

The documents of a provider package (`providers/<name>/`).

| Kind | Description |
|---|---|
| [Provider](provider.md) | An API family: base URL, auth, pagination, error semantics and rate limits. |
| [JobTemplate](jobtemplate.md) | A reusable job definition shipped by a provider package. |
| [Simulation](simulation.md) | The simulator of a provider package (sim/sim.yaml). |
| [Scenario](scenario.md) | A simulator scenario of a provider package (sim/scenarios/\*.yaml). |

## Command line

Every command and flag of `tapyr`: [command line reference](cli.md).
