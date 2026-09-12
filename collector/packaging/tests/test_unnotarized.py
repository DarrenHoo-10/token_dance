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

    def test_no_paid_credentials_or_gatekeeper_bypass_in_free_packaging(self):
        source = (ROOT / 'collector/packaging/macos/package-unnotarized.py').read_text()
        self.assertIn('build-source.mjs',source)
        self.assertIn("'notarized':False",source)
        self.assertIn("'codesign','--force','--sign','-'",source)
        for forbidden in ('notarytool', 'disable-library-validation', '--master-disable', 'xattr -d'):
            self.assertNotIn(forbidden,source)
        workflow = (ROOT / '.github/workflows/cross-platform-packaging.yml').read_text()
        self.assertIn('unnotarized_macos_release:',workflow)
        self.assertIn('inputs.sign_release || inputs.unnotarized_macos_release',workflow)
        self.assertIn('tokendance-desktop-macos-${{ matrix.architecture }}-unnotarized',workflow)


if __name__ == '__main__': unittest.main()
