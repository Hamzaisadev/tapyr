<!-- Generated from the JSON Schemas by make schema (internal/tools/schemagen). Do not edit. -->

# Account

An instance of a provider with credentials, params, targets and labels.

Schema: [`account.json`](../../schemas/v1alpha1/account.json). Explanations and examples: [configuration model](../spec/03-configuration.md#account).

| Field | Type | Required | Description |
|---|---|---|---|
| `apiVersion` | `tapyr/v1alpha1` | yes | Version of the configuration format. |
| `kind` | `Account` | yes | Kind of the document. |
| `metadata` | [ObjectMeta](#objectmeta) | yes | Name and description of the document. |
| `spec` | [AccountSpec](#accountspec) | yes | Specification of the document. |

## ObjectMeta

ObjectMeta identifies a document.

| Field | Type | Required | Description |
|---|---|---|---|
| `description` | string |  | Description is free text for humans. |
| `name` | [Name](#name) | yes | Name of the document, unique per kind. |

## AccountSpec

AccountSpec is the specification of an Account.

| Field | Type | Required | Description |
|---|---|---|---|
| `budget` | [AccountBudget](#accountbudget) |  | Budget share of this account. |
| `labels` | map of string to string |  | Labels added to every sample from this account. Keys match `^[a-zA-Z_][a-zA-Z0-9_]*$`. |
| `params` | map of string to string |  | Params declared by the provider. Keys match `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `provider` | [Name](#name) | yes | Provider instantiated by the account. |
| `secrets` | map of string to [SecretRef](#secretref) |  | Secrets declared by the provider, as references to environment variables or files. Keys match `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `targets` | list of [Target](#target) |  | Targets of a multi-target account (for example one entry per device). Jobs run once per target. |
| `tls` | [TLSClient](#tlsclient) |  | `tls` settings for connections to the provider. |

## Name

Type: string. Pattern `^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`.

## AccountBudget

AccountBudget configures the share of quotas the account may use.

| Field | Type | Required | Description |
|---|---|---|---|
| `scaleRates` | boolean |  | `scaleRates` also scales rate and concurrency limits by the share. |
| `share` | number |  | Share of every quota limit Tapyr may use, when other consumers share the same credentials. In (0, 1]. Default `1`. Greater than 0. Maximum 1. |

## SecretRef

A secret read from an environment variable ({env: NAME}) or a file ({file: /path}).

| Field | Type | Required | Description |
|---|---|---|---|
| `env` | string |  | Name of an environment variable holding the secret. Minimum length 1. |
| `file` | string |  | Path of a file holding the secret, relative to the declaring file. Minimum length 1. |

- Exactly one field is set.

## Target

Target is one endpoint of a multi-target account.

| Field | Type | Required | Description |
|---|---|---|---|
| `labels` | map of string to string |  | Labels added to every sample from this target. Keys match `^[a-zA-Z_][a-zA-Z0-9_]*$`. |
| `name` | [Name](#name) | yes | Name of the target, unique within the account. |
| `params` | map of string to string |  | Params overriding the account params. Keys match `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `secrets` | map of string to [SecretRef](#secretref) |  | Secrets overriding the account secrets. Keys match `^[A-Za-z_][A-Za-z0-9_]*$`. |

## TLSClient

TLSClient configures TLS towards the provider.

| Field | Type | Required | Description |
|---|---|---|---|
| `caFile` | string |  | `caFile` is a PEM bundle of trusted CAs, relative to the declaring file. |
| `insecureSkipVerify` | boolean |  | `insecureSkipVerify` disables certificate verification. Logged loudly. |
