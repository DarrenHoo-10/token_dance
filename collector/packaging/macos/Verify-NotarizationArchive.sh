#!/bin/bash
set -euo pipefail

usage() {
  echo "usage: $0 <signed.app> <artifact.zip> [--require-staple]" >&2
  exit 64
}

[[ $# -eq 2 || $# -eq 3 ]] || usage
signed_app="$1"
artifact="$2"
require_staple="${3:-}"
[[ -z "$require_staple" || "$require_staple" == "--require-staple" ]] || usage

[[ -d "$signed_app" && "$signed_app" == *.app ]] || { echo "BLOCKED: signed app bundle not found: $signed_app" >&2; exit 66; }
[[ -f "$artifact" && "$artifact" == *.zip ]] || { echo "BLOCKED: notarization archive not found or not a zip: $artifact" >&2; exit 66; }

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
ditto -x -k "$artifact" "$work_dir"
app_name="$(basename "$signed_app")"
extracted_app="$work_dir/$app_name"
[[ -d "$extracted_app" ]] || { echo "BLOCKED: archive does not contain expected app: $app_name" >&2; exit 65; }

shopt -s nullglob dotglob
entries=("$work_dir"/*)
shopt -u nullglob dotglob
[[ ${#entries[@]} -eq 1 ]] || { echo "BLOCKED: archive must contain exactly one top-level app" >&2; exit 65; }

codesign --verify --deep --strict --verbose=2 "$signed_app"
codesign --verify --deep --strict --verbose=2 "$extracted_app"
source_cdhash="$(codesign -d --verbose=4 "$signed_app" 2>&1 | sed -n 's/^CDHash=//p')"
archive_cdhash="$(codesign -d --verbose=4 "$extracted_app" 2>&1 | sed -n 's/^CDHash=//p')"
[[ -n "$source_cdhash" && "$source_cdhash" == "$archive_cdhash" ]] || {
  echo "BLOCKED: archive app CDHash does not match signed app" >&2
  exit 65
}
if [[ "$require_staple" == "--require-staple" ]]; then
  xcrun stapler validate "$signed_app"
  xcrun stapler validate "$extracted_app"
fi

printf 'Archive binding verified: app=%s cdhash=%s artifact=%s\n' "$app_name" "$source_cdhash" "$artifact"
