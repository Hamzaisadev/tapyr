<!-- Generated from the JSON Schemas by make schema (internal/tools/schemagen). Do not edit. -->

# Scenario

A simulator scenario of a provider package (sim/scenarios/\*.yaml).

Schema: [`scenario.json`](../../schemas/v1alpha1/scenario.json). Explanations and examples: [configuration model](../spec/03-configuration.md#provider-packages).

| Field | Type | Required | Description |
|---|---|---|---|
| `apiVersion` | `tapyr/v1alpha1` | yes | Version of the configuration format. |
| `kind` | `Scenario` | yes | Kind of the document. |
| `metadata` | [ObjectMeta](#objectmeta) | yes | Name and description of the document. |
| `spec` | [ScenarioSpec](#scenariospec) | yes | Specification of the document. |

## ObjectMeta

ObjectMeta identifies a document.

| Field | Type | Required | Description |
|---|---|---|---|
| `description` | string |  | Description is free text for humans. |
| `name` | [Name](#name) | yes | Name of the document, unique per kind. |

## ScenarioSpec

ScenarioSpec is the specification of a Scenario.

| Field | Type | Required | Description |
|---|---|---|---|
| `steps` | list of [ScenarioStep](#scenariostep) |  | Steps of the timeline. |

## Name

Type: string. Pattern `^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`.

## ScenarioStep

ScenarioStep applies from at until at + for (or until the end), in simulated time since the simulator start.

| Field | Type | Required | Description |
|---|---|---|---|
| `at` | [Duration](#duration) |  | At is the start of the step. |
| `errors` | [ScenarioErrors](#scenarioerrors) |  | Errors answers a ratio of the requests to matching routes with a status. |
| `externalConsumption` | [ExternalConsumption](#externalconsumption) |  | `externalConsumption` simulates another client consuming a quota. |
| `fixtures` | map of string to string |  | Fixtures replaces the fixture of routes, by route path, to simulate inventory changes. Files are relative to the sim/ directory and take the {var} placeholders of the route. |
| `for` | [Duration](#duration) |  | For is the duration of the step; empty means until the end. |
| `latency` | [ScenarioLatency](#scenariolatency) |  | Latency delays the responses (log-normal, in simulated time). |
| `limits` | map of string to [ScenarioLimit](#scenariolimit) |  | Limits overrides the max of named quota limits. |
| `outage` | boolean |  | Outage closes connections without a response. |
| `tokenExpiry` | boolean |  | `tokenExpiry` invalidates, when the step starts, the tokens issued before it. |

## Duration

Type: string. Pattern <code>^(&#91;0-9&#93;+(&#92;.&#91;0-9&#93;+)?(ns&#124;us&#124;µs&#124;ms&#124;s&#124;m&#124;h))+$</code>. Examples `30s`, `5m`, `1h30m`.

## ScenarioErrors

ScenarioErrors injects error responses.

| Field | Type | Required | Description |
|---|---|---|---|
| `ratio` | number | yes | Ratio of the matching requests that fail (0 to 1). Minimum 0. Maximum 1. |
| `routes` | list of string |  | Routes are globs on the route paths (\* within a segment, \*\* across segments). Empty means every route. |
| `status` | integer | yes | Status of the injected responses. Minimum 400. Maximum 599. |

## ExternalConsumption

ExternalConsumption consumes a quota at a steady rate.

| Field | Type | Required | Description |
|---|---|---|---|
| `limit` | [Name](#name) | yes | Limit is the name of a quota limit of the provider. |
| `ratePerHour` | number | yes | `ratePerHour` is the consumption rate. Minimum 0. |

## ScenarioLatency

ScenarioLatency is a log-normal response delay given by two quantiles.

| Field | Type | Required | Description |
|---|---|---|---|
| `p50` | [Duration](#duration) | yes | P50 is the median delay. |
| `p99` | [Duration](#duration) |  | P99 is the 99th percentile, at least P50. |

## ScenarioLimit

ScenarioLimit overrides a quota limit.

| Field | Type | Required | Description |
|---|---|---|---|
| `max` | integer | yes | Max of the quota per window. Minimum 0. |
