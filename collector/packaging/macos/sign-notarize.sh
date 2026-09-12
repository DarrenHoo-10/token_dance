#!/bin/bash
set -euo pipefail

usage() {
  echo "usage: $0 <unsigned.app> <output.dmg>" >&2
  exit 64
}

[[ $# -eq 2 ]] || usage
sign_target="$1"
release_artifact="$2"
script_dir="$(cd "$(dirname "$0")" && pwd)"
entitlements_template="$script_dir/collector.entitlements"
verify_archive="$script_dir/Verify-NotarizationArchive.sh"

: "${DEVELOPER_ID_APPLICATION:?BLOCKED: DEVELOPER_ID_APPLICATION signing identity is missing}"
: "${APPLE_NOTARY_PROFILE:?BLOCKED: APPLE_NOTARY_PROFILE keychain profile is missing}"
: "${APPLE_TEAM_ID:?BLOCKED: APPLE_TEAM_ID is missing}"
: "${MACOS_PROVISIONING_PROFILE:?BLOCKED: MACOS_PROVISIONING_PROFILE is missing}"
[[ "$DEVELOPER_ID_APPLICATION" == "Developer ID Application:"* ]] || { echo 'BLOCKED: a Developer ID Application identity is required' >&2; exit 78; }
[[ -f "$MACOS_PROVISIONING_PROFILE" ]] || { echo 'BLOCKED: MACOS_PROVISIONING_PROFILE does not exist' >&2; exit 66; }
notary_credentials=(--keychain-profile "$APPLE_NOTARY_PROFILE")
if [[ -n "${APPLE_NOTARY_KEYCHAIN:-}" ]]; then
  [[ -f "$APPLE_NOTARY_KEYCHAIN" ]] || { echo "BLOCKED: APPLE_NOTARY_KEYCHAIN not found: $APPLE_NOTARY_KEYCHAIN" >&2; exit 66; }
  notary_credentials+=(--keychain "$APPLE_NOTARY_KEYCHAIN")
fi
[[ -d "$sign_target" && "$sign_target" == *.app ]] || { echo "BLOCKED: signing target must be an existing .app bundle: $sign_target" >&2; exit 66; }
[[ "$release_artifact" == *.dmg ]] || { echo "BLOCKED: release artifact must be a dmg: $release_artifact" >&2; exit 64; }
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
entitlements="$work_dir/keychain.entitlements"
(cd "$script_dir/../../.." && node "$script_dir/../../apps/desktop/scripts/build-source.mjs" "$work_dir/release-source.json")
mkdir -p "$sign_target/Contents/Resources"
cp "$work_dir/release-source.json" "$sign_target/Contents/Resources/tokendance-build-source.json"
python3 "$script_dir/prepare-keychain-signing.py" \
  "$sign_target" "$entitlements_template" "$MACOS_PROVISIONING_PROFILE" "$entitlements" \
  --team-id "$APPLE_TEAM_ID" --signing-identity "$DEVELOPER_ID_APPLICATION"

# Sign nested Mach-O files innermost-first, then the bundle. Avoid --deep.
while IFS= read -r nested; do
  codesign --force --timestamp --options runtime \
    --sign "$DEVELOPER_ID_APPLICATION" "$nested"
done < <(find "$sign_target/Contents" -type f \( -name '*.dylib' -o -name '*.so' -o -perm +111 \) ! -path '*/MacOS/*' | sort -r)
codesign --force --timestamp --options runtime \
  --entitlements "$entitlements" \
  --sign "$DEVELOPER_ID_APPLICATION" "$sign_target"
codesign --verify --deep --strict --verbose=2 "$sign_target"
codesign -d --verbose=4 "$sign_target" 2>&1 | grep -Fx "TeamIdentifier=$APPLE_TEAM_ID" >/dev/null || {
  echo 'BLOCKED: signed app team does not match APPLE_TEAM_ID' >&2
  exit 1
}
codesign -d --entitlements :- "$sign_target" > "$work_dir/signed-entitlements.plist" 2>/dev/null
python3 - "$entitlements" "$work_dir/signed-entitlements.plist" <<'PY'
import plistlib
import sys
expected, actual = [plistlib.load(open(file, 'rb')) for file in sys.argv[1:]]
for key in ('com.apple.application-identifier', 'com.apple.developer.team-identifier', 'keychain-access-groups'):
    if actual.get(key) != expected[key]:
        raise SystemExit(f'BLOCKED: signed app is missing expected {key}')
PY
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

# Keep the app's ticket inside the image, then separately notarize the DMG.
python3 "$script_dir/dmg.py" create "$sign_target" "$release_artifact"
codesign --force --timestamp --sign "$DEVELOPER_ID_APPLICATION" "$release_artifact"
codesign --verify --strict --verbose=2 "$release_artifact"
dmg_notary_result="$work_dir/dmg-notary-result.json"
xcrun notarytool submit "$release_artifact" "${notary_credentials[@]}" \
  --wait --output-format json > "$dmg_notary_result"
python3 - "$dmg_notary_result" <<'PY'
import json, sys
result = json.load(open(sys.argv[1], encoding='utf-8'))
if result.get('status') != 'Accepted':
    raise SystemExit('BLOCKED: disk image notarization was not accepted')
PY
xcrun stapler staple "$release_artifact"
python3 "$script_dir/dmg.py" verify "$sign_target" "$release_artifact" --require-staple

# Hash only final, stapled bytes; these are the hashes used by the download page.
python3 - "$sign_target" "$release_artifact" "$work_dir/release-source.json" "$notary_result" "$dmg_notary_result" <<'PY'
import hashlib, json, os, pathlib, plistlib, subprocess, sys
app, dmg, source_file, app_notary, dmg_notary = map(pathlib.Path, sys.argv[1:])
info = plistlib.load(open(app / 'Contents/Info.plist', 'rb'))
binary = app / 'Contents/MacOS' / info['CFBundleExecutable']
architectures = subprocess.check_output(['lipo', '-archs', str(binary)], text=True).split()
if len(architectures) != 1 or architectures[0] not in ('arm64', 'x86_64'):
    raise SystemExit('BLOCKED: expected one supported macOS architecture')
def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()
source = json.load(source_file.open())
metadata = {**source, 'commit': source['commitSha'], 'profile': 'release',
    'version': info['CFBundleShortVersionString'], 'bundleId': info['CFBundleIdentifier'],
    'minimumSystemVersion': info.get('LSMinimumSystemVersion', '13.0'),
    'architecture': architectures[0], 'executable': binary.name, 'sha256': digest(binary),
    'notarized': True, 'teamIdentifier': os.environ['APPLE_TEAM_ID'],
    'signingAuthority': os.environ['DEVELOPER_ID_APPLICATION'],
    'appNotaryId': json.load(app_notary.open())['id'], 'dmgNotaryId': json.load(dmg_notary.open())['id'],
    'dmg': {'file': dmg.name, 'size': dmg.stat().st_size, 'sha256': digest(dmg)}}
dmg.with_suffix('.build-info.json').write_text(json.dumps(metadata, indent=2) + '\n')
dmg.with_suffix('.dmg.sha256').write_text(f"{metadata['dmg']['sha256']}  {dmg.name}\n")
PY
printf 'Signed and notarized DMG ready: %s\n' "$release_artifact"
