<!-- Generated from the JSON Schemas by make schema (internal/tools/schemagen). Do not edit. -->

# Job

What to collect for an account, from a template or inline.

Schema: [`job.json`](../../schemas/v1alpha1/job.json). Explanations and examples: [configuration model](../spec/03-configuration.md#job).

| Field | Type | Required | Description |
|---|---|---|---|
| `apiVersion` | `tapyr/v1alpha1` | yes | Version of the configuration format. |
| `kind` | `Job` | yes | Kind of the document. |
| `metadata` | [ObjectMeta](#objectmeta) | yes | Name and description of the document. |
| `spec` | [JobSpec](#jobspec) | yes | Specification of the document. |

## ObjectMeta

ObjectMeta identifies a document.

| Field | Type | Required | Description |
|---|---|---|---|
| `description` | string |  | Description is free text for humans. |
| `name` | [Name](#name) | yes | Name of the document, unique per kind. |

## JobSpec

JobSpec is the specification of a Job.

| Field | Type | Required | Description |
|---|---|---|---|
| `account` | [Name](#name) | yes | Account the job collects for. |
| `estimate` | [Estimate](#estimate) |  | Estimate gives hints to the planner before run history exists. |
| `extract` | [Extract](#extract) |  | Extract maps responses to samples and inventory items. |
| `forEach` | [ForEach](#foreach) |  | `forEach` fans the job out over the inventory items of a parent. |
| `interval` | [Duration](#duration) |  | Interval is the desired interval; the planner may stretch it. Defaults to the template's defaultInterval. |
| `labels` | map of string to string |  | Labels added to every sample of the job. |
| `outputs` | list of [Name](#name) |  | Outputs receiving the job's samples. Default: all outputs. |
| `pagination` | [Pagination](#pagination) |  | Pagination overrides the provider default. |
| `priority` | [Priority](#priority) |  | Priority of the job. Defaults to the template's defaultPriority, else normal. |
| `request` | [Request](#request) |  | Request to send. |
| `role` | [JobRole](#jobrole) |  | Role of the job: inventory items, metrics, or both. |
| `staleAfter` | [Duration](#duration) |  | `staleAfter` removes series of failing runs after this delay. Default: 3x the effective interval. |
| `template` | [Name](#name) |  | Template the job instantiates. |

## Name

Type: string. Pattern `^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`.

## Estimate

Estimate gives hints to the planner.

| Field | Type | Required | Description |
|---|---|---|---|
| `pagesPerRun` | integer |  | `pagesPerRun` is the expected number of pages per run. Minimum 1. |

## Extract

Extract maps responses to samples and inventory items.

| Field | Type | Required | Description |
|---|---|---|---|
| `filter` | [Expr](#expr) |  | Filter is a CEL expression on item; false skips the item. |
| `info` | list of [InfoMetric](#infometric) |  | Info metrics (value 1, descriptive labels) produced per item. |
| `inventory` | [Inventory](#inventory) |  | Inventory items produced per item (role inventory or both). |
| `items` | [Expr](#expr) |  | Items is a CEL expression returning the list of items of a page. Default: response.body when it is a list. |
| `metrics` | list of [Metric](#metric) |  | Metrics produced per item. |
| `partial` | boolean |  | Partial emits the samples of a run even when a later page fails. |
| `post` | list of [Metric](#metric) |  | Post metrics computed once per run over all pages (responses). |

## ForEach

ForEach fans a job out over the inventory items of a parent job.

| Field | Type | Required | Description |
|---|---|---|---|
| `filter` | [Expr](#expr) |  | Filter is a CEL expression on parent selecting a subset of items. |
| `job` | [Name](#name) |  | Job is the parent Job (same account), for inline jobs. |
| `maxInstances` | integer |  | `maxInstances` caps the fan-out, against runaway discovery. Default `10000`. Minimum 1. |
| `template` | [Name](#name) |  | Template is the parent JobTemplate, instantiated implicitly for the account when no job references it. |
| `vars` | map of string to [Expr](#expr) |  | Vars maps request variables to CEL expressions on parent. Keys match `^[A-Za-z_][A-Za-z0-9_]*$`. |

## Duration

Type: string. Pattern <code>^(&#91;0-9&#93;+(&#92;.&#91;0-9&#93;+)?(ns&#124;us&#124;µs&#124;ms&#124;s&#124;m&#124;h))+$</code>. Examples `30s`, `5m`, `1h30m`.

## Pagination

Pagination configures how successive pages are requested.

| Field | Type | Required | Description |
|---|---|---|---|
| `cursor` | [CursorPagination](#cursorpagination) |  | Cursor pagination (type cursor). |
| `expression` | [ExpressionPagination](#expressionpagination) |  | Expression pagination computed by CEL (type expression). |
| `linkHeader` | [LinkHeaderPagination](#linkheaderpagination) |  | `linkHeader` pagination following RFC 8288 Link headers (type link\_header). |
| `maxPages` | integer |  | `maxPages` is a hard safety cap on pages per run. Default `100`. Minimum 1. |
| `offset` | [OffsetPagination](#offsetpagination) |  | Offset pagination (type offset). |
| `page` | [PagePagination](#pagepagination) |  | Page pagination (type page). |
| `type` | string | yes | Type of pagination. One of `none`, `offset`, `page`, `link_header`, `cursor`, `expression`. |

- When `type` is `offset`: `offset` is required.
- When `type` is `page`: `page` is required.
- When `type` is `cursor`: `cursor` is required.
- When `type` is `expression`: `expression` is required.

## Priority

Type: string. One of `critical`, `high`, `normal`, `low`.

## Request

Request describes the HTTP request of a job.

| Field | Type | Required | Description |
|---|---|---|---|
| `body` | map of string to [Value](#value) |  | Body is a JSON object for POST queries; values are literals, secrets or CEL expressions. |
| `headers` | map of string to string |  | Headers merged over the provider defaults (the job wins). |
| `idempotent` | boolean |  | Idempotent allows retrying POST requests. |
| `method` | string |  | Method of the request. POST is allowed only for read-only queries. Default `GET`. One of `GET`, `HEAD`, `POST`. |
| `path` | string | yes | Path relative to the base URL, with {var} placeholders resolved from discovery variables, target params and account params. Pattern `^/`. |
| `query` | map of string to [Value](#value) |  | Query parameters; values are literals, secrets or CEL expressions. |
| `readOnly` | boolean |  | `readOnly` must be true for POST requests: Tapyr never writes to providers. |
| `timeout` | [Duration](#duration) |  | Timeout of a single request. Defaults to the provider default. |

## JobRole

Type: string. Default `metrics`. One of `inventory`, `metrics`, `both`.

## Expr

CEL expression.

Type: string. Minimum length 1.

## InfoMetric

InfoMetric produces an \_info metric: value 1 and descriptive labels.

| Field | Type | Required | Description |
|---|---|---|---|
| `help` | string |  | Help text of the metric. |
| `labels` | map of string to [Expr](#expr) | yes | Labels maps label names to CEL expressions returning strings. Keys match `^[a-zA-Z_][a-zA-Z0-9_]*$`. |
| `name` | string | yes | Name of the metric, ending with \_info. Pattern `^[a-zA-Z_:][a-zA-Z0-9_:]*_info$`. |
| `skipIf` | [Expr](#expr) |  | `skipIf` is a CEL expression; true skips the sample. |

## Inventory

Inventory produces inventory items for child jobs.

| Field | Type | Required | Description |
|---|---|---|---|
| `fields` | map of string to [Expr](#expr) |  | Fields maps field names to CEL expressions, available as parent.&lt;field&gt;. Keys match `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `key` | [Expr](#expr) | yes | Key is a CEL expression returning the unique key of an item. |
| `ttl` | [Duration](#duration) |  | `ttl` of cached items. Default: 3x the job's effective interval. |

## Metric

Metric maps items to samples of one metric.

| Field | Type | Required | Description |
|---|---|---|---|
| `accumulate` | boolean |  | Accumulate sums successive window values into a persisted counter (type counter only). |
| `help` | string |  | Help text of the metric. |
| `labels` | map of string to [Expr](#expr) |  | Labels maps label names to CEL expressions returning strings. Keys match `^[a-zA-Z_][a-zA-Z0-9_]*$`. |
| `name` | string | yes | Name of the metric (Prometheus naming rules). Pattern `^[a-zA-Z_:][a-zA-Z0-9_:]*$`. |
| `skipIf` | [Expr](#expr) |  | `skipIf` is a CEL expression; true skips the sample. |
| `timestamp` | [Expr](#expr) |  | Timestamp is a CEL expression returning the source timestamp. |
| `type` | string |  | Type of the metric. Default `gauge`. One of `gauge`, `counter`. |
| `unit` | string |  | Unit of the metric, preferably a Prometheus base unit. |
| `value` | [Expr](#expr) | yes | Value is a CEL expression returning a number or a bool. |
| `windowField` | [Expr](#expr) |  | `windowField` is a CEL expression identifying the window of a value, used by accumulate to avoid counting a window twice. |

## CursorPagination

CursorPagination passes a cursor extracted from each response.

| Field | Type | Required | Description |
|---|---|---|---|
| `cursor` | [Expr](#expr) | yes | Cursor is a CEL expression extracting the next cursor; empty or null stops. |
| `header` | string |  | Header carrying the cursor, instead of a query parameter. |
| `pageSize` | integer |  | `pageSize` is the number of items per page. Minimum 1. |
| `pageSizeParam` | string |  | `pageSizeParam` is the query parameter carrying the page size. |
| `param` | string |  | Param is the query parameter carrying the cursor. |

## ExpressionPagination

ExpressionPagination computes the next request with CEL.

| Field | Type | Required | Description |
|---|---|---|---|
| `next` | [Expr](#expr) | yes | Next is a CEL expression returning {query: {...}} for the next page, or null. |

## LinkHeaderPagination

LinkHeaderPagination follows RFC 8288 Link headers.

| Field | Type | Required | Description |
|---|---|---|---|
| `pageSize` | integer |  | `pageSize` is the number of items per page. Minimum 1. |
| `pageSizeParam` | string |  | `pageSizeParam` is the query parameter carrying the page size. |
| `rel` | string |  | Rel is the link relation of the next page. Default `next`. |

## OffsetPagination

OffsetPagination requests pages by item offset.

| Field | Type | Required | Description |
|---|---|---|---|
| `limitParam` | string | yes | `limitParam` is the query parameter carrying the page size. Minimum length 1. |
| `offsetParam` | string | yes | `offsetParam` is the query parameter carrying the offset. Minimum length 1. |
| `pageSize` | integer | yes | `pageSize` is the number of items per page. Minimum 1. |
| `start` | integer |  | Start is the first offset. Default `0`. Minimum 0. |
| `stop` | [Expr](#expr) |  | Stop is a CEL expression; pagination stops when it returns true. |
| `total` | [Expr](#expr) |  | Total is a CEL expression returning the total number of items. |

## PagePagination

PagePagination requests pages by page number.

| Field | Type | Required | Description |
|---|---|---|---|
| `pageParam` | string | yes | `pageParam` is the query parameter carrying the page number. Minimum length 1. |
| `pageSize` | integer |  | `pageSize` is the number of items per page. Minimum 1. |
| `sizeParam` | string |  | `sizeParam` is the query parameter carrying the page size. |
| `start` | integer |  | Start is the first page number. Default `1`. Minimum 0. |
| `stop` | [Expr](#expr) |  | Stop is a CEL expression; pagination stops when it returns true. |
| `total` | [Expr](#expr) |  | Total is a CEL expression returning the total number of items. |

## Value

A literal string, {secret: name} or {expr: cel}.

Either a string, or an object:

| Field | Type | Required | Description |
|---|---|---|---|
| `expr` | string |  | CEL expression evaluated at runtime. Minimum length 1. |
| `secret` | string |  | Name of a secret declared by the provider and supplied by the account. Pattern `^[A-Za-z_][A-Za-z0-9_]*$`. |

- Exactly one field is set.
