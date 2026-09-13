from pathlib import Path
import json
import re
import unittest


ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = (ROOT / ".github/workflows/cross-platform-packaging.yml").read_text(encoding="utf-8")
MAC_SIGN = (ROOT / "collector/packaging/macos/sign-notarize.sh").read_text(encoding="utf-8")
MAC_VERIFY = (ROOT / "collector/packaging/macos/Verify-NotarizationArchive.sh").read_text(encoding="utf-8")
WINDOWS_SIGN = (ROOT / "collector/packaging/windows/Sign-Authenticode.ps1").read_text(encoding="utf-8")
WINDOWS_VERIFY = (ROOT / "collector/packaging/windows/Verify-Authenticode.ps1").read_text(encoding="utf-8")
MAC_BUILD = (ROOT / "collector/apps/desktop/scripts/build-macos.mjs").read_text(encoding="utf-8")
WINDOWS_BUILD = (ROOT / "collector/apps/desktop/scripts/build-windows.ps1").read_text(encoding="utf-8")
DESKTOP_MAIN = (ROOT / "collector/apps/desktop/src-tauri/src/main.rs").read_text(encoding="utf-8")
PE_GUI = (ROOT / "collector/packaging/windows/check_pe_gui.py").read_text(encoding="utf-8")
PUBLISH = (ROOT / "tools/releases/publish_manifest.py").read_text(encoding="utf-8")


class ReleaseWiringTests(unittest.TestCase):
    def test_windows_ci_parallelizes_without_bypassing_release_tests(self):
        tests = WORKFLOW.split("  windows-tests:\n", 1)[1].split("  windows:\n", 1)[0]
        build = WORKFLOW.split("  windows:\n", 1)[1].split("  macos:\n", 1)[0]
        self.assertNotIn("    needs:", tests)
        self.assertNotIn("    needs:", build)
        self.assertIn("if: github.event_name != 'workflow_dispatch' || !inputs.sign_release", tests)
        for name in (
            "Release wiring static tests", "Windows crate format", "Windows runnable tests",
            "Adapter Pack tests", "Desktop update manifest and replacement tests",
            "PowerShell syntax and fail-closed Authenticode tests",
        ):
            self.assertIn(f"- name: {name}\n", tests)
            self.assertIn(
                f"- name: {name}\n        if: github.event_name == 'workflow_dispatch' && inputs.sign_release",
                build,
            )
            self.assertLess(build.index(f"- name: {name}\n"), build.index("- name: Sign and verify"))
        self.assertIn("cargo build --locked --release", build)
        self.assertIn("Reject non-GUI Windows portable", build)

    def test_both_platforms_embed_frontend_with_a_locked_build(self):
        command = "cargo build --locked --release --manifest-path collector/apps/desktop/src-tauri/Cargo.toml --features custom-protocol"
        self.assertIn(command, WORKFLOW)
        self.assertIn('custom-protocol,unnotarized-distribution', MAC_BUILD)
        self.assertIn('"--",\n  "--locked"', MAC_BUILD)
        self.assertLess(WORKFLOW.index("npm --prefix collector/apps/desktop run build"), WORKFLOW.index("Desktop update manifest and replacement tests"))

    def test_signing_and_signed_uploads_require_explicit_release_mode(self):
        self.assertIn('sign_release:', WORKFLOW)
        self.assertIn('default: false', WORKFLOW)
        gate = "if: github.event_name == 'workflow_dispatch' && inputs.sign_release"
        self.assertGreaterEqual(WORKFLOW.count(gate), 4)
        self.assertIn('tokendance-desktop-windows-unsigned', WORKFLOW)
        self.assertIn('tokendance-desktop-macos-${{ matrix.architecture }}-unsigned', WORKFLOW)
        self.assertNotIn("if: github.event_name != 'pull_request'", WORKFLOW)

    def test_windows_release_artifact_is_built_signed_verified_and_uploaded(self):
        artifact = "collector/apps/desktop/src-tauri/target/release/tokendance-desktop.exe"
        self.assertIn("cargo build --locked --release --manifest-path collector/apps/desktop/src-tauri/Cargo.toml --features custom-protocol", WORKFLOW)
        self.assertGreaterEqual(WORKFLOW.count(artifact), 2)
        self.assertIn("Sign-Authenticode.ps1 -Path $artifact", WORKFLOW)
        self.assertIn("Verify-Authenticode.ps1 -Path $artifact", WORKFLOW)
        self.assertRegex(WORKFLOW, rf"path:\s*\|\s*{re.escape(artifact)}")
        self.assertNotIn("target/debug/tokendance-desktop.exe", WORKFLOW)

    def test_windows_portable_is_forced_gui_and_rejected_if_console(self):
        self.assertIn('#![windows_subsystem = "windows"]', DESKTOP_MAIN)
        self.assertNotIn("cfg_attr(not(debug_assertions), windows_subsystem", DESKTOP_MAIN)
        self.assertIn("check_pe_gui.py", WORKFLOW)
        self.assertIn("collector/packaging/tests/test_pe_gui.py", WORKFLOW)
        self.assertIn("Reject non-GUI Windows portable", WORKFLOW)
        self.assertIn("BLOCKED: TokenDance.exe must use the Windows GUI subsystem", PE_GUI)
        self.assertIn("BLOCKED: TokenDance.exe must use the Windows GUI subsystem", WINDOWS_BUILD)
        self.assertIn("Assert-WindowsGuiSubsystem", WINDOWS_BUILD)
        self.assertIn("check_pe_gui.py", PUBLISH)
        self.assertIn("IMAGE_SUBSYSTEM_WINDOWS_GUI", PE_GUI)

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

    def test_local_windows_release_checks_source_before_build_and_records_commit(self):
        guard = WINDOWS_BUILD.index("$sourceJson = & node (Join-Path $PSScriptRoot 'build-source.mjs')")
        checked = WINDOWS_BUILD.index("if ($LASTEXITCODE -ne 0) { throw 'BLOCKED: release source verification failed' }")
        frontend = WINDOWS_BUILD.index("& npm.cmd run build")
        native = WINDOWS_BUILD.index("& cargo build --locked --release")
        self.assertLess(guard, checked)
        self.assertLess(checked, frontend)
        self.assertLess(frontend, native)
        self.assertIn("$source.branch -ne 'main'", WINDOWS_BUILD)
        self.assertIn("$source.commitSha -notmatch '^[a-f0-9]{40}$'", WINDOWS_BUILD)
        self.assertIn("$source.dirty -ne $false", WINDOWS_BUILD)
        self.assertIn("branch = $source.branch", WINDOWS_BUILD)
        self.assertIn("commitSha = $source.commitSha", WINDOWS_BUILD)
        self.assertIn("'collector/apps/desktop/scripts/build-windows.ps1',", WORKFLOW)

    def test_app_and_dmg_are_both_notarized_before_final_verification(self):
        sign = MAC_SIGN.index('codesign --force --timestamp --options runtime')
        submit = MAC_SIGN.index('xcrun notarytool submit "$submission_artifact"')
        app_staple = MAC_SIGN.index('xcrun stapler staple "$sign_target"')
        create = MAC_SIGN.index('python3 "$script_dir/dmg.py" create')
        dmg_sign = MAC_SIGN.index('codesign --force --timestamp --sign "$DEVELOPER_ID_APPLICATION" "$release_artifact"')
        dmg_submit = MAC_SIGN.index('xcrun notarytool submit "$release_artifact"')
        dmg_staple = MAC_SIGN.index('xcrun stapler staple "$release_artifact"')
        verify = MAC_SIGN.index('python3 "$script_dir/dmg.py" verify')
        self.assertEqual(sorted([sign, submit, app_staple, create, dmg_sign, dmg_submit, dmg_staple, verify]), [sign, submit, app_staple, create, dmg_sign, dmg_submit, dmg_staple, verify])
        self.assertIn('--require-staple', MAC_SIGN[verify:])
        self.assertLess(verify, MAC_SIGN.index("'sha256': digest(dmg)"))

    def test_macos_archive_verifier_binds_identity_and_staple_to_archive_contents(self):
        self.assertIn('[[ ${#entries[@]} -eq 1 ]]', MAC_VERIFY)
        self.assertIn('source_cdhash=', MAC_VERIFY)
        self.assertIn('archive_cdhash=', MAC_VERIFY)
        self.assertIn('"$source_cdhash" == "$archive_cdhash"', MAC_VERIFY)
        self.assertIn('xcrun stapler validate "$extracted_app"', MAC_VERIFY)

    def test_macos_release_build_credentials_and_upload_are_wired_fail_closed(self):
        app = "collector/packaging/macos/release/TokenDance.app"
        archive = "collector/packaging/macos/release/*.dmg"
        self.assertIn('npm --prefix collector/apps/desktop run build:macos -- "${build_args[@]}"', WORKFLOW)
        self.assertGreaterEqual(WORKFLOW.count(app), 2)
        self.assertIn(archive, WORKFLOW)
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

    def test_release_builds_verify_main_and_publish_source_metadata(self):
        self.assertIn("inputs.sign_release && github.ref != 'refs/heads/main'", WORKFLOW)
        self.assertIn('node collector/apps/desktop/scripts/build-source.mjs', WORKFLOW)
        self.assertIn('verifyReleaseSource(desktopRoot)', MAC_BUILD)
        self.assertIn('release-source.json', WORKFLOW)
        self.assertIn('build-info.json', WORKFLOW)
        self.assertIn('...source,', MAC_BUILD)
        self.assertIn('build_args+=(--debug)', WORKFLOW)

    def test_macos_artifacts_are_bound_to_each_built_architecture(self):
        for target in ("aarch64-apple-darwin", "x86_64-apple-darwin"):
            self.assertIn(f"target: {target}", WORKFLOW)
        self.assertIn('target/${{ matrix.target }}/$BUILD_PROFILE/bundle/macos/TokenDance.app', WORKFLOW)
        self.assertNotIn('find collector/apps/desktop/src-tauri/target', WORKFLOW)
        self.assertIn('tokendance-desktop-macos-${{ matrix.architecture }}-notarized', WORKFLOW)
        self.assertIn('run("lipo", [binary, "-verify_arch", outArch])', MAC_BUILD)
        self.assertIn('src-tauri/tauri.conf.json', MAC_BUILD)

    def test_macos_keychain_signing_requires_profile_and_explicit_team(self):
        for variable in ('APPLE_TEAM_ID', 'MACOS_PROVISIONING_PROFILE_BASE64'):
            self.assertIn(f'BLOCKED: {variable} is missing', WORKFLOW)
        self.assertIn('export MACOS_PROVISIONING_PROFILE="$provisioning_path"', WORKFLOW)
        self.assertIn('prepare-keychain-signing.py', MAC_SIGN)
        self.assertIn('sign-notarize.sh', MAC_BUILD)
        self.assertLess(MAC_SIGN.index('python3 "$script_dir/prepare-keychain-signing.py"'), MAC_SIGN.index('codesign --force --timestamp --options runtime'))
        for key in ('com.apple.application-identifier', 'com.apple.developer.team-identifier', 'keychain-access-groups'):
            self.assertIn(key, MAC_SIGN)
        self.assertIn('tokendance-build-source.json', MAC_SIGN)
        self.assertIn('notarizedInfo', MAC_BUILD)

    def test_macos_settings_window_keeps_overlay_chrome_after_config_merge(self):
        # RFC 7396 replaces the whole windows array, so the macOS overlay must
        # carry a complete settings window or Overlay/size/center are dropped.
        macos = json.loads(
            (ROOT / "collector/apps/desktop/src-tauri/tauri.macos.conf.json").read_text(
                encoding="utf-8"
            )
        )
        windows = {window["label"]: window for window in macos["app"]["windows"]}
        settings = windows["settings"]
        self.assertTrue(settings["decorations"])
        self.assertEqual(settings["titleBarStyle"], "Overlay")
        self.assertEqual(settings["trafficLightPosition"], {"x": 16, "y": 20})
        self.assertTrue(settings["hiddenTitle"])
        self.assertTrue(settings["acceptFirstMouse"])
        self.assertTrue(settings["center"])
        self.assertEqual(settings["width"], 680)
        self.assertEqual(settings["height"], 600)
        main = windows["main"]
        self.assertTrue(main["decorations"])
        self.assertEqual(main["titleBarStyle"], "Overlay")
        self.assertEqual(main["trafficLightPosition"], {"x": 16, "y": 20})
        self.assertTrue(main["hiddenTitle"])
        self.assertTrue(main["acceptFirstMouse"])
        self.assertEqual(main["width"], 480)
        self.assertEqual(main["height"], 780)


if __name__ == "__main__":
    unittest.main()
