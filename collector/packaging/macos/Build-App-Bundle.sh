#!/bin/bash
set -euo pipefail

usage() {
  echo "usage: $0 <release-binary> <output.app>" >&2
  exit 64
}

[[ $# -eq 2 ]] || usage
release_binary="$1"
app_bundle="$2"

[[ -f "$release_binary" ]] || { echo "BLOCKED: release binary not found: $release_binary" >&2; exit 66; }
[[ "$app_bundle" == *.app ]] || { echo "BLOCKED: output must be a .app bundle: $app_bundle" >&2; exit 64; }
[[ ! -e "$app_bundle" ]] || { echo "BLOCKED: output app already exists: $app_bundle" >&2; exit 73; }

executable_name="$(basename "$release_binary")"
contents="$app_bundle/Contents"
mkdir -p "$contents/MacOS" "$contents/Resources"
cp "$release_binary" "$contents/MacOS/$executable_name"
chmod 0755 "$contents/MacOS/$executable_name"
cat > "$contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>en</string>
  <key>CFBundleExecutable</key><string>$executable_name</string>
  <key>CFBundleIdentifier</key><string>io.tokendance.desktop</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>TokenDance</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
</dict>
</plist>
PLIST
plutil -lint "$contents/Info.plist" >/dev/null
printf '%s\n' "$app_bundle"
