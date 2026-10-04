from pathlib import Path
import importlib.util
import sys
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
        self.assertIsNone(package.select_signing({}, '     0 valid identities found\n'))
        self.assertIsNone(package.select_signing({'TOKENDANCE_SIGNING_IDENTITY': 'adhoc'}, listing))
        with self.assertRaises(ValueError):  # an explicitly requested identity must exist
            package.select_signing({'TOKENDANCE_SIGNING_IDENTITY': 'Missing'}, listing)
        with self.assertRaises(ValueError):
            package.select_signing({'TOKENDANCE_SIGNING_IDENTITY': 'Developer ID Application: X (ABCDEFGHIJ)'}, listing)
        with self.assertRaises(ValueError):  # ambiguous
            package.select_signing({}, listing + f'  3) {"EF" * 20} "{package.DEFAULT_IDENTITY}"\n')
        self.assertTrue(package.stable_requirement(f'designated => identifier "io.tokendance.desktop" and certificate leaf = H"{sha1.lower()}"', sha1))
        self.assertFalse(package.stable_requirement('designated => cdhash H"abcd"', sha1))

    def test_identity_helper_creates_a_local_codesigning_only_certificate(self):
        script = (ROOT / 'collector/packaging/macos/create-self-signed-identity.sh').read_text()
        self.assertIn('codeSigning', script)
        self.assertIn('security import', script)
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


if __name__ == '__main__': unittest.main()
