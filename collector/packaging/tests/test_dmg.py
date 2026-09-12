from pathlib import Path
import importlib.util
import plistlib
import shutil
import tempfile
import unittest
from unittest.mock import patch

MODULE = Path(__file__).resolve().parents[1] / 'macos/dmg.py'
spec = importlib.util.spec_from_file_location('dmg', MODULE)
dmg = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dmg)


class DmgTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.app = self.root / 'TokenDance.app'
        (self.app / 'Contents/MacOS').mkdir(parents=True)
        (self.app / 'Contents/MacOS/app').write_bytes(b'fixture')
        (self.app / 'Contents/Info.plist').write_bytes(plistlib.dumps({'CFBundleExecutable': 'app'}))
        self.mount = self.root / 'mounted'
        self.mount.mkdir()
        shutil.copytree(self.app, self.mount / self.app.name)
        (self.mount / 'Applications').symlink_to('/Applications')

    def test_matching_bundle_and_applications_link(self):
        self.assertEqual(dmg.verify_contents(self.app, self.mount), self.mount / self.app.name)

    def test_versioned_source_has_a_stable_app_name_inside_the_dmg(self):
        renamed = self.root / 'TokenDance-1.2.3-macos-arm64.app'
        self.app.rename(renamed)
        info = {'CFBundleExecutable':'app','CFBundleIdentifier':'io.tokendance.desktop'}
        (renamed / 'Contents/Info.plist').write_bytes(plistlib.dumps(info))
        (self.mount / 'TokenDance.app/Contents/Info.plist').write_bytes(plistlib.dumps(info))
        self.assertEqual(dmg.packaged_name(renamed), 'TokenDance.app')
        dmg.verify_contents(renamed,self.mount)

    def test_tampered_binary_is_not_the_verified_app(self):
        (self.mount / self.app.name / 'Contents/MacOS/app').write_bytes(b'changed')
        with self.assertRaisesRegex(ValueError, 'differs'):
            dmg.verify_contents(self.app, self.mount)

    def test_link_cannot_point_to_a_different_destination(self):
        (self.mount / 'Applications').unlink()
        (self.mount / 'Applications').symlink_to('/tmp')
        with self.assertRaisesRegex(ValueError, 'Applications'):
            dmg.verify_contents(self.app, self.mount)

    def test_unexpected_second_app_is_rejected(self):
        (self.mount / 'Other.app').mkdir()
        with self.assertRaisesRegex(ValueError, 'Unexpected'):
            dmg.verify_contents(self.app, self.mount)

    def test_mount_is_detached_even_when_verification_fails(self):
        with patch.object(dmg, 'run') as run:
            with self.assertRaisesRegex(RuntimeError, 'validation'):
                with dmg.mounted(self.root / 'test.dmg'):
                    raise RuntimeError('validation failed')
        self.assertEqual(run.call_args_list[-1].args[:2], ('hdiutil', 'detach'))

    def test_existing_output_is_never_overwritten(self):
        output = self.root / 'existing.dmg'
        output.write_bytes(b'old')
        with self.assertRaises(ValueError):
            dmg.create(self.app, output)
        self.assertEqual(output.read_bytes(), b'old')


if __name__ == '__main__':
    unittest.main()
