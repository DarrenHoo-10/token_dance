import copy
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('publisher', Path(__file__).with_name('publish_manifest.py'))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)
CONTRACT = json.loads((ROOT / 'schemas/fixtures/desktop-release-manifest.json').read_text())


class PublishTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.path = self.root / 'stable.json'
        self.release = copy.deepcopy(CONTRACT['releases'][0])

    def test_public_manifest_is_written_only_after_remote_verification(self):
        def verify(asset):
            self.assertFalse(self.path.exists())
            self.assertEqual(asset, self.release['exe'])
        self.assertTrue(publisher.update_manifest(self.path, self.release, verify))
        self.assertEqual(json.loads(self.path.read_text()), CONTRACT)
        self.assertFalse(self.path.with_name('stable.json.lock').exists())

    def test_failed_remote_verification_preserves_current_manifest(self):
        self.path.write_text(json.dumps({'schemaVersion': 1, 'releases': []}))
        previous = self.path.read_bytes()
        def fail(_):
            raise ValueError('Remote package incomplete')
        with self.assertRaises(ValueError):
            publisher.update_manifest(self.path, self.release, fail)
        self.assertEqual(self.path.read_bytes(), previous)
        self.assertFalse(self.path.with_name('stable.json.lock').exists())

    def test_republish_is_idempotent_and_existing_version_cannot_change(self):
        publisher.update_manifest(self.path, self.release, lambda _: None)
        previous = self.path.read_bytes()
        self.assertFalse(publisher.update_manifest(self.path, self.release, lambda _: None))
        self.release['exe']['sha256'] = 'f' * 64
        with self.assertRaises(ValueError):
            publisher.update_manifest(self.path, self.release, lambda _: None)
        self.assertEqual(self.path.read_bytes(), previous)

    def test_no_downgrade_and_other_platform_is_preserved(self):
        other = {**self.release, 'platform': 'macos-arm64'}
        self.path.write_text(json.dumps({'schemaVersion': 1, 'releases': [other]}))
        publisher.update_manifest(self.path, self.release, lambda _: None)
        self.assertIn(other, json.loads(self.path.read_text())['releases'])
        self.release['version'] = '0.1.9'
        with self.assertRaises(ValueError):
            publisher.update_manifest(self.path, self.release, lambda _: None)

    def test_exclusive_lock_and_atomic_replacement_failure(self):
        lock = self.path.with_name('stable.json.lock')
        lock.touch()
        with self.assertRaises(FileExistsError):
            publisher.update_manifest(self.path, self.release, lambda _: None)
        lock.unlink()
        self.path.write_text(json.dumps({'schemaVersion': 1, 'releases': []}))
        previous = self.path.read_bytes()
        with patch.object(publisher.os, 'replace', side_effect=OSError('disk unavailable')):
            with self.assertRaises(OSError):
                publisher.update_manifest(self.path, self.release, lambda _: None)
        self.assertEqual(self.path.read_bytes(), previous)
        self.assertEqual(list(self.root.iterdir()), [self.path])

    def test_invalid_existing_manifest_is_not_overwritten(self):
        for current in [{'schemaVersion': 2, 'releases': []},
                        {'schemaVersion': 1, 'releases': [self.release, self.release]},
                        {'schemaVersion': 1, 'releases': [{**self.release, 'exe': None}]}]:
            self.path.write_text(json.dumps(current))
            original = self.path.read_bytes()
            with self.assertRaises(ValueError):
                publisher.update_manifest(self.path, self.release, lambda _: None)
            self.assertEqual(self.path.read_bytes(), original)

    def test_build_provenance_must_match_main_version_and_bytes(self):
        build = {'branch': 'main', 'version': self.release['version'], 'commit': 'a' * 40,
                 'sha256': self.release['exe']['sha256']}
        publisher.validate_build(build, self.release['version'], self.release['exe'])
        for field, value in [('branch', 'feature'), ('version', '0.1.0'), ('sha256', 'b' * 64), ('commit', 'abc')]:
            with self.assertRaises(ValueError):
                publisher.validate_build({**build, field: value}, self.release['version'], self.release['exe'])

    def test_permanent_https_urls_and_numeric_versions(self):
        self.assertTrue(publisher.valid_url(self.release['exe']['url']))
        for url in ['http://cdn.example.com/a.exe', 'https://u:p@cdn.example.com/a.exe',
                    'https://127.0.0.1/a.exe', 'https://0x7f.0.0.1/a.exe', 'https://host.local/a.exe',
                    'https://cdn.example.com/a.exe?signature=expired', 'https://cdn.example.com/a.exe#fragment']:
            self.assertFalse(publisher.valid_url(url), url)
        for value in ['01.0.0', 'v1.0.0', '1.0.0-beta.1', '1.0.0+metadata']:
            with self.assertRaises(ValueError):
                publisher.version(value)

    def test_remote_bytes_are_checked_and_redirects_rejected(self):
        class Response(io.BytesIO):
            status = 200
        for data, valid in [(b'MZ', True), (b'MZextra', False), (b'M', False), (b'ZZ', False)]:
            with patch.object(publisher.urllib.request, 'build_opener') as opener:
                opener.return_value.open.return_value = Response(data)
                if valid:
                    publisher.verify_remote(self.release['exe'])
                else:
                    with self.assertRaises(ValueError):
                        publisher.verify_remote(self.release['exe'])
        with self.assertRaises(ValueError):
            publisher.NoRedirect().redirect_request(None, None, 302, '', {}, 'https://other.example.com/file')

    def test_executable_architecture_and_zip_must_match(self):
        data = bytearray(128)
        data[:2] = b'MZ'
        data[60:64] = (64).to_bytes(4, 'little')
        data[64:70] = b'PE\0\0\x64\x86'
        exe = self.root / 'TokenDance.exe'
        exe.write_bytes(data)
        publisher.check_windows_exe(exe)
        asset = publisher.describe(exe, self.release['exe']['url'])
        archive = self.root / 'package.zip'
        with zipfile.ZipFile(archive, 'w') as stream:
            stream.writestr('TokenDance.exe', data)
        publisher.check_zip(archive, asset)
        with zipfile.ZipFile(archive, 'w') as stream:
            stream.writestr('TokenDance.exe', b'wrong')
        with self.assertRaises(ValueError):
            publisher.check_zip(archive, asset)
        data[68:70] = b'\x4c\x01'
        exe.write_bytes(data)
        with self.assertRaises(ValueError):
            publisher.check_windows_exe(exe)


if __name__ == '__main__':
    unittest.main()
