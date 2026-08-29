#!/bin/bash
set -euo pipefail

usage() {
  echo "usage: $0 <unsigned.app> <output.zip>" >&2
  exit 64
}

[[ $# -eq 2 ]] || usage
sign_target="$1"
release_artifact="$2"
script_dir="$(cd "$(dirname "$0")" && pwd)"
entitlements="$script_dir/collector.entitlements"
verify_archive="$script_dir/Verify-NotarizationArchive.sh"

: "${DEVELOPER_ID_APPLICATION:?BLOCKED: DEVELOPER_ID_APPLICATION signing identity is missing}"
: "${APPLE_NOTARY_PROFILE:?BLOCKED: APPLE_NOTARY_PROFILE keychain profile is missing}"
notary_credentials=(--keychain-profile "$APPLE_NOTARY_PROFILE")
if [[ -n "${APPLE_NOTARY_KEYCHAIN:-}" ]]; then
  [[ -f "$APPLE_NOTARY_KEYCHAIN" ]] || { echo "BLOCKED: APPLE_NOTARY_KEYCHAIN not found: $APPLE_NOTARY_KEYCHAIN" >&2; exit 66; }
  notary_credentials+=(--keychain "$APPLE_NOTARY_KEYCHAIN")
fi
[[ -d "$sign_target" && "$sign_target" == *.app ]] || { echo "BLOCKED: signing target must be an existing .app bundle: $sign_target" >&2; exit 66; }
[[ "$release_artifact" == *.zip ]] || { echo "BLOCKED: release artifact must be a zip: $release_artifact" >&2; exit 64; }
[[ ! -e "$release_artifact" ]] || { echo "BLOCKED: release artifact already exists: $release_artifact" >&2; exit 73; }
[[ -f "$verify_archive" ]] || { echo "BLOCKED: archive verifier not found: $verify_archive" >&2; exit 69; }
security find-identity -v -p codesigning | grep -F "\"$DEVELOPER_ID_APPLICATION\"" >/dev/null || {
  echo "BLOCKED: exact Developer ID certificate evidence not present in keychain: $DEVELOPER_ID_APPLICATION" >&2
  exit 78
}

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
submission_artifact="$work_dir/notarization-submission.zip"
notary_result="$work_dir/notary-result.json"

codesign --force --deep --timestamp --options runtime \
  --entitlements "$entitlements" \
  --sign "$DEVELOPER_ID_APPLICATION" "$sign_target"
codesign --verify --deep --strict --verbose=2 "$sign_target"
codesign -d --verbose=4 "$sign_target" 2>&1 | grep -F 'flags=0x10000(runtime)' >/dev/null || {
  echo "BLOCKED: hardened runtime evidence missing after codesign" >&2
  exit 1
}

ditto -c -k --keepParent "$sign_target" "$submission_artifact"
bash "$verify_archive" "$sign_target" "$submission_artifact"
xcrun notarytool submit "$submission_artifact" \
  "${notary_credentials[@]}" \
  --wait --output-format json > "$notary_result"
python3 - "$notary_result" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    result = json.load(handle)
status = result.get("status")
if status != "Accepted":
    raise SystemExit(f"BLOCKED: Apple notarization status is {status!r}, expected 'Accepted'")
print(f"Notarization accepted: id={result.get('id', 'unknown')}")
PY

xcrun stapler staple "$sign_target"
xcrun stapler validate "$sign_target"
spctl --assess --type execute --verbose=4 "$sign_target"
ditto -c -k --keepParent "$sign_target" "$release_artifact"
bash "$verify_archive" "$sign_target" "$release_artifact" --require-staple
printf 'Signed, notarized, stapled, and bound release artifact: %s\n' "$release_artifact"
