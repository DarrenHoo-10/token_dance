from pathlib import Path
import importlib.util
import os
import subprocess
import sys
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / 'collector/packaging/macos'))
spec = importlib.util.spec_from_file_location('unnotarized', ROOT / 'collector/packaging/macos/package-unnotarized.py')
package = importlib.util.module_from_spec(spec)
spec.loader.exec_module(package)


class UnnotarizedTests(unittest.TestCase):
    def test_only_explicit_free_builds_can_be_packaged(self):
        valid = {'CFBundleIdentifier':'io.tokendance.desktop','TokenDanceCredentialStore':'login-keychain','TokenDanceDistribution':'unnotarized'}
        package.validate_app(valid)
        for change in [{'TokenDanceCredentialStore':'data-protection'}, {'TokenDanceDistribution':'local-test'}, {'CFBundleIdentifier':'io.tokendance.desktop.local-test'}]:
            with self.assertRaises(ValueError): package.validate_app({**valid,**change})

    def test_signing_identity_selection_is_stable_and_never_apple_issued(self):
        sha1 = 'AB' * 20
        listing = f'  1) {sha1} "{package.DEFAULT_IDENTITY}"\n  2) {"CD" * 20} "Other"\n     2 valid identities found\n'
        self.assertEqual(package.select_signing({}, listing), (package.DEFAULT_IDENTITY, sha1))
        self.assertEqual(package.select_signing({'CI': 'true'}, listing), (package.DEFAULT_IDENTITY, sha1))
        self.assertIsNone(package.select_signing({}, '     0 valid identities found\n'))
        self.assertIsNone(package.select_signing({'TOKENDANCE_SIGNING_IDENTITY': 'adhoc'}, listing))
        self.assertIsNone(package.select_signing({'CI': 'true', 'BUILD_PROFILE': 'release', 'TOKENDANCE_SIGNING_IDENTITY': 'adhoc'}, listing))
        for environ in ({'CI': 'true'}, {'GITHUB_ACTIONS': 'true'}, {'BUILD_PROFILE': 'release'}, {'CI': '1'}):
            with self.assertRaises(ValueError) as blocked:
                package.select_signing(environ, '     0 valid identities found\n')
            self.assertIn('BLOCKED', str(blocked.exception))
        with self.assertRaises(ValueError):  # an explicitly requested identity must exist
            package.select_signing({'TOKENDANCE_SIGNING_IDENTITY': 'Missing'}, listing)
        with self.assertRaises(ValueError) as blocked:
            package.select_signing({'CI': 'true', 'TOKENDANCE_SIGNING_IDENTITY': 'Missing'}, listing)
        self.assertIn('BLOCKED', str(blocked.exception))
        with self.assertRaises(ValueError):
            package.select_signing({'TOKENDANCE_SIGNING_IDENTITY': 'Developer ID Application: X (ABCDEFGHIJ)'}, listing)
        with self.assertRaises(ValueError):  # ambiguous
            package.select_signing({}, listing + f'  3) {"EF" * 20} "{package.DEFAULT_IDENTITY}"\n')
        self.assertTrue(package.stable_requirement(f'designated => identifier "io.tokendance.desktop" and certificate leaf = H"{sha1.lower()}"', sha1))
        self.assertFalse(package.stable_requirement('designated => cdhash H"abcd"', sha1))

    def test_duplicate_find_identity_lines_are_the_same_certificate(self):
        sha1 = 'AB' * 20
        other = 'EF' * 20
        duplicated = (
            f'  1) {sha1} "{package.DEFAULT_IDENTITY}"\n'
            f'  2) {sha1.lower()} "{package.DEFAULT_IDENTITY}"\n'
            '     2 valid identities found\n'
        )
        self.assertEqual(package.find_identity(package.DEFAULT_IDENTITY, duplicated), sha1)
        self.assertEqual(package.require_identity(package.DEFAULT_IDENTITY, duplicated, sha1), sha1)
        self.assertEqual(package.require_identity(package.DEFAULT_IDENTITY, duplicated, sha1.lower()), sha1)
        self.assertEqual(
            package.require_identity(package.DEFAULT_IDENTITY, duplicated, ':'.join(sha1[i:i + 2] for i in range(0, 40, 2))),
            sha1,
        )
        self.assertEqual(
            package.select_signing({'CI': 'true', 'GITHUB_ACTIONS': 'true'}, duplicated),
            (package.DEFAULT_IDENTITY, sha1),
        )
        self.assertIsNone(package.find_identity(package.DEFAULT_IDENTITY, '     0 valid identities found\n'))
        with self.assertRaises(ValueError) as missing:
            package.require_identity(package.DEFAULT_IDENTITY, '     0 valid identities found\n', sha1)
        self.assertIn('BLOCKED', str(missing.exception))
        wrong = f'  1) {other} "{package.DEFAULT_IDENTITY}"\n  2) {other} "{package.DEFAULT_IDENTITY}"\n'
        self.assertEqual(package.find_identity(package.DEFAULT_IDENTITY, wrong), other)
        with self.assertRaises(ValueError) as mismatch:
            package.require_identity(package.DEFAULT_IDENTITY, wrong, sha1)
        self.assertIn('expected SHA1', str(mismatch.exception))
        mixed = duplicated + f'  3) {other} "{package.DEFAULT_IDENTITY}"\n'
        with self.assertRaises(ValueError) as ambiguous:
            package.require_identity(package.DEFAULT_IDENTITY, mixed, sha1)
        self.assertIn('More than one', str(ambiguous.exception))
        with self.assertRaises(ValueError) as malformed:
            package.require_identity(package.DEFAULT_IDENTITY, duplicated, 'abc')
        self.assertIn('40 hex', str(malformed.exception))

    def test_identity_helper_creates_a_local_codesigning_only_certificate(self):
        script = (ROOT / 'collector/packaging/macos/create-self-signed-identity.sh').read_text()
        self.assertIn('codeSigning', script)
        self.assertIn('security import', script)
        self.assertIn('MACOS_SELFSIGNED_P12_BASE64', script)
        self.assertNotIn('never leaves this machine', script)
        for forbidden in ('--master-disable', 'xattr -d', 'spctl --global', 'sudo'):
            self.assertNotIn(forbidden, script)

    def test_no_paid_credentials_or_gatekeeper_bypass_in_free_packaging(self):
        source = (ROOT / 'collector/packaging/macos/package-unnotarized.py').read_text()
        self.assertIn('build-source.mjs',source)
        self.assertIn("'notarized':False",source)
        self.assertIn("'codesign','--force','--sign',identity",source)
        for forbidden in ('notarytool', 'disable-library-validation', '--master-disable', 'xattr -d'):
            self.assertNotIn(forbidden,source)
        workflow = (ROOT / '.github/workflows/cross-platform-packaging.yml').read_text()
        mac_build = (ROOT / 'collector/apps/desktop/scripts/build-macos.mjs').read_text()
        self.assertIn('unnotarized_macos_release:',workflow)
        self.assertIn('inputs.sign_release || inputs.unnotarized_macos_release',workflow)
        self.assertIn('tokendance-desktop-macos-${{ matrix.architecture }}-unnotarized',workflow)
        self.assertIn('TokenDance-${version}-macos-${outArch}.dmg', mac_build)
        self.assertNotIn('TokenDance-${version}-macos-${outArch}-unnotarized.dmg', mac_build)
        self.assertIn('*-macos-${{ matrix.architecture }}.dmg collector/packaging/macos/release/', workflow)
        self.assertIn('MACOS_SELFSIGNED_P12_BASE64', workflow)
        self.assertIn('MACOS_SELFSIGNED_P12_PASSWORD', workflow)
        self.assertIn('MACOS_SELFSIGNED_CERT_SHA1', workflow)
        self.assertIn('TOKENDANCE_SIGNING_IDENTITY=TokenDance Self-Signed', workflow)
        self.assertIn('sudo security add-trusted-cert -d -r trustRoot -p codeSign -k /Library/Keychains/System.keychain', workflow)
        self.assertIn('package.require_identity(', workflow)
        self.assertIn('sys.path.insert(0, root)', workflow)
        self.assertIn('imported certificate SHA1', workflow)
        self.assertNotIn('found != [expected]', workflow)
        self.assertIn('signingAuthority") != "self-signed"', workflow)

    def test_install_step_accepts_only_the_expected_certificate(self):
        workflow = (ROOT / '.github/workflows/cross-platform-packaging.yml').read_text()
        body = workflow.split("LISTING=\"$listing\" python3 - <<'PY'\n", 1)[1].split("\n          PY\n", 1)[0]
        script = textwrap.dedent(body)
        sha1 = 'AB' * 20
        other = 'EF' * 20
        duplicated = (
            f'  1) {sha1} "TokenDance Self-Signed"\n'
            f'  2) {sha1} "TokenDance Self-Signed"\n'
            '     2 valid identities found\n'
        )
        cases = [
            (duplicated, sha1, 0),
            ('     0 valid identities found\n', sha1, 1),
            (f'  1) {other} "TokenDance Self-Signed"\n  2) {other} "TokenDance Self-Signed"\n', sha1, 1),
            (duplicated + f'  3) {other} "TokenDance Self-Signed"\n', sha1, 1),
            (duplicated, 'abc', 1),
        ]
        for listing, expected, status in cases:
            env = os.environ.copy()
            env['LISTING'] = listing
            env['MACOS_SELFSIGNED_CERT_SHA1'] = expected
            result = subprocess.run([sys.executable, '-c', script], cwd=ROOT, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, status, result.stderr or result.stdout)
            if status == 0:
                self.assertIn(sha1, result.stdout)
            else:
                self.assertIn('BLOCKED', result.stderr)
        sign_release = workflow.split('Sign, notarize, staple, and verify bound macOS release artifact', 1)[1]
        self.assertNotIn('MACOS_SELFSIGNED', sign_release.split('Record unsigned build status', 1)[0])


if __name__ == '__main__': unittest.main()
