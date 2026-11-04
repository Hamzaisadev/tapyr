#!/usr/bin/env bash
# Smoke test of a built binary: starts tapyr sim and tapyr run as processes,
# checks the collected metrics on /metrics, then checks both shut down
# cleanly on SIGTERM.
#
# Usage: scripts/smoke.sh [path/to/tapyr]
set -euo pipefail

bin="${1:-./bin/tapyr}"
sim_addr="127.0.0.1:18081"
run_addr="127.0.0.1:19790"
tmp="$(mktemp -d)"
sim_pid=""
run_pid=""

cleanup() {
  [[ -n "$run_pid" ]] && kill "$run_pid" 2>/dev/null || true
  [[ -n "$sim_pid" ]] && kill "$sim_pid" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT

fail() {
  echo "smoke test failed: $*" >&2
  echo "--- sim log" >&2; cat "$tmp/sim.log" >&2 || true
  echo "--- run log" >&2; cat "$tmp/run.log" >&2 || true
  exit 1
}

cat > "$tmp/config.yaml" <<YAML
apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: smoke }
spec:
  provider: example-sim
  params: { baseURL: "http://${sim_addr}/api/v1" }
  secrets: { apiKey: { env: TAPYR_SMOKE_API_KEY } }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: smoke-devices }
spec: { account: smoke, template: example-sim-devices }
---
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: prom }
spec: { type: prometheus }
---
apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: smoke }
spec:
  server: { listen: "${run_addr}" }
  state: { path: "${tmp}/tapyr.db" }
  collection: { jitter: 0% }
YAML

"$bin" version
"$bin" validate --config "$tmp/config.yaml"

"$bin" sim --provider example-sim --listen "$sim_addr" >"$tmp/sim.log" 2>&1 &
sim_pid=$!
# Wait for the simulator: the first run starts immediately (no jitter), and
# its failure would only be retried after a backoff.
for _ in $(seq 1 100); do
  curl -fsS "http://${sim_addr}/_sim/status" >/dev/null 2>&1 && break
  sleep 0.1
done
curl -fsS "http://${sim_addr}/_sim/status" >/dev/null || fail "simulator not ready"
TAPYR_SMOKE_API_KEY=sim-api-key "$bin" run --config "$tmp/config.yaml" --log-format text >"$tmp/run.log" 2>&1 &
run_pid=$!

metrics=""
for _ in $(seq 1 100); do
  if metrics="$(curl -fsS "http://${run_addr}/metrics" 2>/dev/null)" && grep -q '^example_devices[ {]' <<<"$metrics"; then
    break
  fi
  metrics=""
  sleep 0.1
done
[[ -n "$metrics" ]] || fail "metrics not exposed"
grep -q 'example_device_up{serial="SIM-0001",site_id="site-a"} 1' <<<"$metrics" || fail "unexpected metrics: $metrics"

kill -TERM "$run_pid"
wait "$run_pid" || fail "tapyr run exited with $?"
run_pid=""
kill -TERM "$sim_pid"
wait "$sim_pid" || fail "tapyr sim exited with $?"
sim_pid=""
grep -q 'sim-api-key' "$tmp/run.log" && fail "the API key appears in the logs"
echo "smoke test passed"
