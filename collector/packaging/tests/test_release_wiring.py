from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = (ROOT / ".github/workflows/cross-platform-packaging.yml").read_text(encoding="utf-8")
MAC_SIGN = (ROOT / "collector/packaging/macos/sign-notarize.sh").read_text(encoding="utf-8")
MAC_VERIFY = (ROOT / "collector/packaging/macos/Verify-NotarizationArchive.sh").read_text(encoding="utf-8")
WINDOWS_SIGN = (ROOT / "collector/packaging/windows/Sign-Authenticode.ps1").read_text(encoding="utf-8")
WINDOWS_VERIFY = (ROOT / "collector/packaging/windows/Verify-Authenticode.ps1").read_text(encoding="utf-8")


class ReleaseWiringTests(unittest.TestCase):
    def test_windows_release_artifact_is_built_signed_verified_and_uploaded(self):
        artifact = "collector/apps/desktop/src-tauri/target/release/tokendance-desktop.exe"
        self.assertIn("cargo build --locked --release --manifest-path collector/apps/desktop/src-tauri/Cargo.toml", WORKFLOW)
        self.assertGreaterEqual(WORKFLOW.count(artifact), 3)
        self.assertIn("Sign-Authenticode.ps1 -Path $artifact", WORKFLOW)
        self.assertIn("Verify-Authenticode.ps1 -Path $artifact", WORKFLOW)
        self.assertRegex(WORKFLOW, rf"path:\s*{re.escape(artifact)}")

    def test_windows_signing_is_fail_closed_and_requires_timestamp_evidence(self):
        for secret in (
            "WINDOWS_SIGNING_CERT_PFX_BASE64",
            "WINDOWS_SIGNING_CERT_PASSWORD",
            "WINDOWS_SIGNING_CERT_THUMBPRINT",
        ):
            self.assertIn(f"BLOCKED: {secret} is missing", WORKFLOW)
        self.assertIn("/tr $TimestampUrl /td SHA256", WINDOWS_SIGN)
        self.assertIn("trusted Authenticode timestamp evidence is missing", WINDOWS_VERIFY)
        self.assertNotIn("continue-on-error", WORKFLOW)

    def test_macos_submission_and_published_archive_are_created_from_signed_app(self):
        sign_pos = MAC_SIGN.index('codesign --force --deep --timestamp --options runtime')
        submission_pos = MAC_SIGN.index('ditto -c -k --keepParent "$sign_target" "$submission_artifact"')
        submit_pos = MAC_SIGN.index('xcrun notarytool submit "$submission_artifact"')
        staple_pos = MAC_SIGN.index('xcrun stapler staple "$sign_target"')
        release_pos = MAC_SIGN.index('ditto -c -k --keepParent "$sign_target" "$release_artifact"')
        final_verify_pos = MAC_SIGN.index('bash "$verify_archive" "$sign_target" "$release_artifact" --require-staple')
        self.assertLess(sign_pos, submission_pos)
        self.assertLess(submission_pos, submit_pos)
        self.assertLess(submit_pos, staple_pos)
        self.assertLess(staple_pos, release_pos)
        self.assertLess(release_pos, final_verify_pos)
        self.assertIn('bash "$verify_archive" "$sign_target" "$submission_artifact"', MAC_SIGN)

    def test_macos_archive_verifier_binds_identity_and_staple_to_archive_contents(self):
        self.assertIn('[[ ${#entries[@]} -eq 1 ]]', MAC_VERIFY)
        self.assertIn('source_cdhash=', MAC_VERIFY)
        self.assertIn('archive_cdhash=', MAC_VERIFY)
        self.assertIn('"$source_cdhash" == "$archive_cdhash"', MAC_VERIFY)
        self.assertIn('xcrun stapler validate "$extracted_app"', MAC_VERIFY)

    def test_macos_release_build_credentials_and_upload_are_wired_fail_closed(self):
        app = "collector/packaging/macos/release/TokenDance.app"
        archive = "collector/packaging/macos/release/tokendance-desktop-macos.zip"
        self.assertIn("cargo build --locked --release --manifest-path collector/apps/desktop/src-tauri/Cargo.toml", WORKFLOW)
        self.assertGreaterEqual(WORKFLOW.count(app), 2)
        self.assertGreaterEqual(WORKFLOW.count(archive), 2)
        for secret in (
            "MACOS_CERTIFICATE_P12_BASE64",
            "MACOS_CERTIFICATE_PASSWORD",
            "DEVELOPER_ID_APPLICATION",
            "APPLE_NOTARY_KEY_ID",
            "APPLE_NOTARY_ISSUER_ID",
            "APPLE_NOTARY_PRIVATE_KEY_BASE64",
        ):
            self.assertIn(f"BLOCKED: {secret} is missing", WORKFLOW)
        self.assertIn('grep -F "\\\"$DEVELOPER_ID_APPLICATION\\\""', WORKFLOW)
        self.assertIn('grep -F "\\\"$DEVELOPER_ID_APPLICATION\\\""', MAC_SIGN)
        self.assertIn('export APPLE_NOTARY_KEYCHAIN="$keychain"', WORKFLOW)
        self.assertIn('notary_credentials+=(--keychain "$APPLE_NOTARY_KEYCHAIN")', MAC_SIGN)


if __name__ == "__main__":
    unittest.main()
