# Outputs

Tapyr sends what it collects through `Output` documents. A job sends to every output, or to the outputs listed in its `spec.outputs`. Each output has its own queue: a slow or unavailable backend never blocks collection or the other outputs.

| Type | Protocol | Typical backends |
|---|---|---|
| `prometheus` | Pull, served on `/metrics` | Prometheus, VictoriaMetrics vmagent, Grafana Alloy, any scraper |
| `remote_write` | Prometheus remote write 1.0 (protobuf, snappy) | Prometheus (remote write receiver enabled), VictoriaMetrics, Grafana Mimir, Thanos Receive, Cortex |
| `otlp` | OTLP metrics over HTTP (protobuf) or gRPC | OpenTelemetry Collector, any OTLP receiver |

## `prometheus`

```yaml
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: prom }
spec:
  type: prometheus
  prometheus:
    exposeTimestamps: false   # the default, as recommended by Prometheus
```

`/metrics` holds the samples of the last successful run of every job instance. A series that a run no longer produces leaves the exposition at once; the series of a failing instance stay until `staleAfter` (3x the effective interval by default). The exposition is served in the Prometheus text format or OpenMetrics, by content negotiation, and gzip-compressed for the scrapers that accept it (all the usual ones do). At most one `prometheus` output may be configured.

## `remote_write`

```yaml
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: victoria }
spec:
  type: remote_write
  remoteWrite:
    url: http://victoriametrics:8428/api/v1/write
    headers: { X-Scope-OrgID: tenant-1 }            # literals or { env: ... } / { file: ... } secrets
    basicAuth: { username: { env: VM_USER }, password: { env: VM_PASSWORD } }
    timeout: 10s
  queue:
    capacity: 50000        # samples; the oldest are dropped when full
    maxBatch: 5000         # samples per request
    flushInterval: 5s      # a partial batch leaves after this delay
  externalLabels:
    tapyr_instance: lab    # added to every sample that does not set the label
```

- Samples carry their timestamp: the source timestamp when the mapping sets one, else the time the response was received.
- A series that goes away (absent from the next successful run, its job instance removed by discovery or a reload, or withdrawn after `staleAfter`) receives one staleness marker, so that queries stop returning it at once instead of after the lookback delta.
- Samples whose source timestamp was already sent for the same series are not sent again (APIs often return overlapping windows); see `tapyr_samples_deduplicated_total`.
- Metric metadata (type, help, unit) is sent on the first request and every minute.
- Network errors, `5xx` and `429` are retried with exponential backoff (1 second doubling up to 1 minute), honoring `Retry-After`. Other `4xx` drop the batch.

### Out-of-order samples

Source timestamps can be older than samples already ingested for a series, for example after a restart, when a job interval changes or when an API reports late data. Prometheus rejects such samples unless out-of-order ingestion is enabled (`storage.tsdb.out_of_order_time_window`); Mimir and Thanos have equivalent settings; VictoriaMetrics accepts them. Rejected batches are counted in `tapyr_output_samples_dropped_total{reason="out_of_order"}`.

## `otlp`

```yaml
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: collector }
spec:
  type: otlp
  otlp:
    endpoint: http://otel-collector:4318   # /v1/metrics is added when the URL has no path
    protocol: http/protobuf                # or grpc (endpoint http://otel-collector:4317)
    headers: { api-key: { env: OTLP_API_KEY } }
    resourceAttributes: { deployment.environment: production }
    timeout: 10s
  queue: { capacity: 50000, maxBatch: 5000, flushInterval: 5s }
```

- Payloads are built with the OTLP data model directly, not with the OpenTelemetry metrics SDK, so that points keep their source timestamps.
- One resource per provider and account, with `service.name=tapyr`, `service.instance.id` (a random identifier kept in the state store, stable across restarts), `tapyr.provider`, `tapyr.account` and the configured `resourceAttributes`. The instrumentation scope is `github.com/kentrow/tapyr`.
- Gauges map to OTLP gauges; counters to cumulative monotonic sums whose start time is the first time the output saw the series (a restart starts them again, which receivers treat as a reset).
- A series that goes away receives one data point flagged `FLAG_NO_RECORDED_VALUE`.
- Partial success responses count the rejected points; `429`, `502`, `503`, `504` (HTTP) and `UNAVAILABLE` or `RESOURCE_EXHAUSTED` with `RetryInfo` (gRPC) are retried, honoring the delay asked by the receiver.
- `https://` endpoints use TLS with the system roots.

## Sizing the queue

The queue holds `capacity` samples per output. While a backend is down, the batch at the head of the queue is retried and new samples queue behind it; once full, the oldest samples are dropped and counted. Size `capacity` for the outage you want to ride out: samples per minute across your jobs, times the minutes. The batch being sent is never dropped while its request is in flight.

## Self-metrics to watch

| Metric | Meaning |
|---|---|
| `tapyr_output_queue_samples` | Samples waiting; grows while a backend is slow or down |
| `tapyr_output_samples_sent_total` | Samples accepted by the backend |
| `tapyr_output_samples_dropped_total{reason}` | `queue_full`, `rejected` or `out_of_order` |
| `tapyr_output_send_failures_total{class}` | Failed requests: `transient`, `throttled`, `rejected`, `out_of_order` |
| `tapyr_output_last_success_timestamp_seconds` | Last successful send |

`settings.selfMetrics.outputs` pushes Tapyr's own metrics through outputs too, every `settings.selfMetrics.interval` (30 seconds by default), for setups where nothing scrapes `/metrics/self`.

## Testing against real backends

`make integration` starts VictoriaMetrics and an OpenTelemetry Collector with Docker Compose (`test/integration/compose.yaml`) and checks that samples and staleness markers land in both, over remote write, OTLP/HTTP and OTLP/gRPC.
