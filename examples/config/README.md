# Example configuration

A small configuration for the built-in `example-sim` provider (`providers/example-sim/`), used by `make run` and by the CLI tests.

Run it against the built-in simulator, in two terminals:

```sh
go run ./cmd/tapyr sim --provider example-sim
TAPYR_EXAMPLE_API_KEY=sim-api-key go run ./cmd/tapyr run --config ./examples/config
curl -s http://127.0.0.1:9790/metrics
```

`go run ./cmd/tapyr validate --config ./examples/config --check-secrets` reports the API key when `TAPYR_EXAMPLE_API_KEY` is not set.

`demo-network-devices` discovers its instances: the sites (`demo-sites`) list the networks of each site (`demo.example-sim-networks`, run implicitly), and every network gets its own instance. `tapyr plan` needs the expected number of instances of a fan-out before the first runs:

```sh
TAPYR_EXAMPLE_API_KEY=sim-api-key go run ./cmd/tapyr plan --config ./examples/config \
  --fanout example-sim-sites=3 --fanout example-sim-networks=5
```

Send `SIGHUP` to `tapyr run` after editing the file to reload it without a restart.

While it runs, Tapyr describes itself:

```sh
curl -s http://127.0.0.1:9790/metrics/self | grep '^tapyr_budget'   # quotas, used and remaining
curl -s http://127.0.0.1:9790/api/v1/jobs                            # jobs, intervals, last runs
curl -s http://127.0.0.1:9790/api/v1/budgets                         # every limit and scope
curl -s http://127.0.0.1:9790/readyz
```

The status API is described in `/api/v1/openapi.yaml`. To push the samples to VictoriaMetrics or an OpenTelemetry Collector instead of (or as well as) serving `/metrics`, add a `remote_write` or `otlp` output: see [`docs/outputs.md`](../../docs/outputs.md).
