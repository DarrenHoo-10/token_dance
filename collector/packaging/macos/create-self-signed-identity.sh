#!/bin/bash
# One-time setup on the machine that builds the free macOS DMG.
#
# Creates a self-signed code-signing certificate in the login keychain. Signing
# every release with it gives the app the same code identity across updates, so
# a Keychain "Always Allow" given once keeps working. It is not a Developer ID,
# is not notarized, and does not change Gatekeeper behaviour for downloads.
#
# The private key never leaves this machine; do not export or commit it. Keep
# using the same certificate for every release: replacing it changes the app's
# identity and users are asked to approve Keychain access once more.
set -euo pipefail

name="${1:-TokenDance Self-Signed}"
keychain="${KEYCHAIN:-$HOME/Library/Keychains/login.keychain-db}"

if security find-identity -v -p codesigning "$keychain" | grep -F "\"$name\"" >/dev/null; then
  echo "Identity \"$name\" already exists; nothing to do."
  exit 0
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
chmod 700 "$work"

cat > "$work/openssl.cnf" <<CNF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = $name
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
CNF

openssl req -x509 -newkey rsa:2048 -nodes -days 7300 -sha256 \
  -config "$work/openssl.cnf" -keyout "$work/key.pem" -out "$work/cert.pem"

# Only codesign may use the private key without asking.
security import "$work/key.pem" -k "$keychain" -T /usr/bin/codesign
security import "$work/cert.pem" -k "$keychain"
# Trust it for code signing only, for this user; macOS asks for the login password.
security add-trusted-cert -r trustRoot -p codeSign -k "$keychain" "$work/cert.pem"

security find-identity -v -p codesigning "$keychain" | grep -F "\"$name\""
echo "Done. build:macos --unnotarized now signs with \"$name\"."
