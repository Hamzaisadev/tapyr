# ADR 0008 - Build OTLP payloads directly, not through the OpenTelemetry metrics SDK

- Status: accepted
- Date: 2026-11-02

## Context

Collected samples often carry source timestamps from the vendor API, may arrive late or out of order, and must sometimes be marked stale. The OpenTelemetry metrics SDK is designed for instrumenting applications: it aggregates measurements and assigns timestamps at collection time.

## Decision

Build OTLP metric payloads directly with `go.opentelemetry.io/collector/pdata` (`pmetric`) and send them with OTLP/HTTP or OTLP/gRPC clients. The OpenTelemetry SDK may still be used for Tapyr's own telemetry if needed.

## Alternatives considered

- **OpenTelemetry metrics SDK for collected data**: cannot carry source timestamps, aggregates values, no staleness flag control.

## Consequences

- Full control over timestamps, start times of cumulative sums, and `FLAG_NO_RECORDED_VALUE` for staleness.
- We own batching, retries and partial-success handling for the OTLP output.
