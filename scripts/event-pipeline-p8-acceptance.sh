#!/usr/bin/env bash
# P8 acceptance smoke: inventory, flags, client rollout unit tests.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CARGO=(cargo)
if command -v rustup >/dev/null 2>&1 && rustup toolchain list | grep -q '1\.88'; then
  CARGO=(cargo +1.88.0)
fi

echo "== P8 server rollout unit tests =="
(
  cd "$ROOT/server"
  go test ./internal/rollout/ -count=1
  go test ./internal/httpapi/ -count=1 -run 'TestTelemetryV2Paused|TestTelemetryV1PathsRequireUpgrade|TestTelemetryCapabilitiesShape'
)

echo "== P8 client rollout / schema tests =="
(
  cd "$ROOT/collector/apps/desktop/src-tauri"
  "${CARGO[@]}" test --lib local_store::pipeline::rollout -- --nocapture
  "${CARGO[@]}" test --lib local_store::pipeline::schema -- --nocapture
)

echo "== privacy canary (static) =="
# Canary strings must not appear in upload wire helpers / transport bodies.
if rg -n 'CANARY_PROMPT_DO_NOT_UPLOAD|CANARY_PATH_DO_NOT_UPLOAD|/Users/canary/secret' \
  "$ROOT/collector/crates/uploader" \
  "$ROOT/collector/apps/desktop/src-tauri/src/upload_pipeline.rs" \
  "$ROOT/server/internal/httpapi/telemetry_v2.go"; then
  echo "privacy canary leak detected" >&2
  exit 1
fi
echo "privacy canary scan clean"

echo "P8 acceptance smoke OK"
