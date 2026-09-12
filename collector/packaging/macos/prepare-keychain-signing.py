#!/usr/bin/env python3
"""Prepare profile-authorized macOS Keychain entitlements before codesigning.

Apple TN3137 requires a provisioning profile for data-protection Keychain
access groups. Templates contain no team-specific entitlements; this tool
derives them from an explicit team ID and checks the supplied profile.
"""

import argparse
import datetime
import hashlib
import plistlib
import re
import shutil
import subprocess
from pathlib import Path


def authorized(pattern, value):
    return isinstance(pattern, str) and (
        pattern == value
        or (pattern.endswith(".*") and "*" not in pattern[:-1] and value.startswith(pattern[:-1]))
    )


def build_entitlements(template, profile, team_id, bundle_id, certificate_sha1, *, allow_development=False, now=None):
    if not re.fullmatch(r"[A-Z0-9]{10}", team_id):
        raise ValueError("APPLE_TEAM_ID must be the explicit 10-character Apple developer team ID")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.-]+", bundle_id):
        raise ValueError("invalid CFBundleIdentifier")
    application_id = f"{team_id}.{bundle_id}"
    entitlements = profile.get("Entitlements", {})
    if team_id not in profile.get("TeamIdentifier", []):
        raise ValueError("provisioning profile belongs to a different Apple team")
    if entitlements.get("com.apple.developer.team-identifier") != team_id:
        raise ValueError("profile does not authorize the configured team identifier")
    if not authorized(entitlements.get("com.apple.application-identifier"), application_id):
        raise ValueError("profile does not authorize this macOS application identifier")
    if not any(authorized(group, application_id) for group in entitlements.get("keychain-access-groups", [])):
        raise ValueError("profile does not authorize the application's Keychain access group")
    if not {"OSX", "macOS"}.intersection(profile.get("Platform", [])):
        raise ValueError("profile is not for macOS")
    expiration = profile.get("ExpirationDate")
    now = now or datetime.datetime.now(datetime.timezone.utc)
    if not isinstance(expiration, datetime.datetime):
        raise ValueError("profile is missing its expiration date")
    if expiration.tzinfo is None:
        expiration = expiration.replace(tzinfo=datetime.timezone.utc)
    if expiration <= now:
        raise ValueError("provisioning profile has expired")
    if not allow_development and (
        not profile.get("ProvisionsAllDevices") or entitlements.get("get-task-allow", False)
        or entitlements.get("com.apple.security.get-task-allow", False)
    ):
        raise ValueError("release requires a Developer ID distribution profile")
    certificate_hashes = {hashlib.sha1(cert).hexdigest().upper() for cert in profile.get("DeveloperCertificates", []) if isinstance(cert, bytes)}
    if not re.fullmatch(r"[0-9A-Fa-f]{40}", certificate_sha1) or certificate_sha1.upper() not in certificate_hashes:
        raise ValueError("profile does not authorize the selected signing certificate")
    result = dict(template)
    result["com.apple.application-identifier"] = application_id
    result["com.apple.developer.team-identifier"] = team_id
    result["keychain-access-groups"] = [application_id]
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("app")
    parser.add_argument("template")
    parser.add_argument("profile")
    parser.add_argument("output")
    parser.add_argument("--team-id", required=True)
    parser.add_argument("--signing-identity", required=True)
    parser.add_argument("--allow-development", action="store_true")
    args = parser.parse_args()
    app = Path(args.app)
    try:
        info = plistlib.loads((app / "Contents/Info.plist").read_bytes())
        template = plistlib.loads(Path(args.template).read_bytes())
        identities = subprocess.run(["security", "find-identity", "-v", "-p", "codesigning"], check=True, capture_output=True, text=True)
        hashes = [match.group(1) for line in identities.stdout.splitlines()
                  if (match := re.fullmatch(r'\s*\d+\)\s+([A-Fa-f0-9]{40})\s+"([^"]+)"\s*', line))
                  and match.group(2) == args.signing_identity]
        if len(hashes) != 1 or not args.signing_identity.endswith(f"({args.team_id})"):
            raise ValueError("selected signing identity must be unique and belong to APPLE_TEAM_ID")
        # Decode only the explicitly supplied provisioning profile, never a
        # credential from the user's login Keychain.
        decoded = subprocess.run(["security", "cms", "-D", "-i", args.profile], check=True, capture_output=True)
        profile = plistlib.loads(decoded.stdout)
        result = build_entitlements(template, profile, args.team_id, info["CFBundleIdentifier"], hashes[0], allow_development=args.allow_development)
        Path(args.output).write_bytes(plistlib.dumps(result))
        shutil.copyfile(args.profile, app / "Contents/embedded.provisionprofile")
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        raise SystemExit(f"BLOCKED: invalid macOS Keychain signing configuration: {error}")


if __name__ == "__main__":
    main()
