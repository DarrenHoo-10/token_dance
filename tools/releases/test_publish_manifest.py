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

    def test_build_provenance_must_match_main_version_and_bytes(self):
        build = {'branch': 'main', 'version': self.release['version'], 'commit': 'a' * 40,
                 'sha256': self.release['exe']['sha256']}
        publisher.validate_build(build, self.release['version'], self.release['exe'])
        for field, value in [('branch', 'feature'), ('version', '0.1.0'), ('sha256', 'b' * 64), ('commit', 'abc')]:
            with self.assertRaises(ValueError):
                publisher.validate_build({**build, field: value}, self.release['version'], self.release['exe'])

    def test_macos_dmg_requires_bound_notarization_and_main_provenance(self):
        image = self.root / 'TokenDance.dmg'
        image.write_bytes(b'fixture-dmg' + b'koly' + bytes(508))
        publisher.check_dmg(image)
        asset = publisher.describe(image, 'https://downloads.example.com/TokenDance.dmg', publisher.MAX_MAC_DOWNLOAD)
        release = {k: v for k, v in self.release.items() if k != 'exe'}
        release.update(platform='macos-arm64', minimumSystemVersion='13.0', notarized=True, dmg=asset)
        build = {'branch':'main','commit':'a'*40,'dirty':False,'profile':'release',
                 'version':release['version'],'architecture':'arm64','bundleId':'io.tokendance.desktop',
                 'minimumSystemVersion':'13.0','notarized':True,'teamIdentifier':'ABCDEFGHIJ',
                 'signingAuthority':'Developer ID Application: Example (ABCDEFGHIJ)',
                 'appNotaryId':'a'*8+'-'+ 'b'*4+'-'+ 'c'*4+'-'+ 'd'*4+'-'+ 'e'*12,
                 'dmgNotaryId':'f'*8+'-'+ 'b'*4+'-'+ 'c'*4+'-'+ 'd'*4+'-'+ 'e'*12,
                 'dmg': {'sha256':asset['sha256'],'size':asset['size']}}
        publisher.validate_manifest({'schemaVersion':1,'releases':[self.release, release]})
        publisher.validate_macos_build(build, release)
        for field, value in [('dirty',True),('profile','debug'),('notarized',False),('architecture','x86_64'),('branch','feature/test'),('dmg',{'size':1,'sha256':'b'*64}),('dmgNotaryId','')]:
            with self.assertRaises(ValueError):
                publisher.validate_macos_build({**build,field:value}, release)
        self.assertEqual(publisher.release_assets(release), [asset])
        with self.assertRaises(ValueError):
            publisher.validate_manifest({'schemaVersion':1,'releases':[{**release,'exe':self.release['exe']}]})
        image.write_bytes(b'<html>not a disk image</html>')
        with self.assertRaises(ValueError):
            publisher.check_dmg(image)

    def test_macos_has_its_own_asset_limit_and_does_not_accept_zip_urls(self):
        release = {k:v for k,v in self.release.items() if k != 'exe'}
        release.update(platform='macos-x64', minimumSystemVersion='13.0', notarized=True, dmg={
            'url':'https://downloads.example.com/mac.dmg','sha256':'a'*64,'size':300*1024*1024})
        publisher.validate_manifest({'schemaVersion':1,'releases':[release]})
        for patch in [{'size':513*1024*1024},{'url':'https://downloads.example.com/mac.zip'}]:
            with self.assertRaises(ValueError):
                publisher.validate_manifest({'schemaVersion':1,'releases':[{**release,'dmg':{**release['dmg'],**patch}}]})

    def test_free_release_is_explicit_and_cannot_claim_notarization(self):
        release = {key:value for key,value in self.release.items() if key != 'exe'}
        asset = {'url':'https://downloads.example.com/free.dmg','sha256':'a'*64,'size':512}
        release.update(platform='macos-arm64', minimumSystemVersion='13.0', notarized=False, dmg=asset)
        build = {'branch':'main','commit':'a'*40,'dirty':False,'profile':'release','version':release['version'],
                 'architecture':'arm64','bundleId':'io.tokendance.desktop','minimumSystemVersion':'13.0',
                 'notarized':False,'distribution':'unnotarized','signingAuthority':'adhoc',
                 'teamIdentifier':None,'credentialStore':'login-keychain','dmg':asset}
        publisher.validate_manifest({'schemaVersion':1,'releases':[release]})
        publisher.validate_macos_build(build,release)
        for change in [{'notarized':True},{'profile':'debug'},{'credentialStore':'plaintext'},{'branch':'feature/test'},{'dmgNotaryId':'fabricated'}]:
            with self.assertRaises(ValueError):
                publisher.validate_macos_build({**build,**change},release)

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
        data = self.gui_pe()
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

    def test_windows_console_subsystem_is_rejected(self):
        exe = self.root / 'TokenDance.exe'
        exe.write_bytes(self.gui_pe(subsystem=3))
        with self.assertRaises(ValueError) as error:
            publisher.check_windows_exe(exe)
        self.assertIn('Windows GUI subsystem', str(error.exception))
        exe.write_bytes(self.gui_pe(subsystem=2))
        publisher.check_windows_exe(exe)

    @staticmethod
    def gui_pe(subsystem=2):
        data = bytearray(160)
        data[:2] = b'MZ'
        data[60:64] = (64).to_bytes(4, 'little')
        data[64:68] = b'PE\0\0'
        data[68:70] = (0x8664).to_bytes(2, 'little')
        data[88:90] = (0x20B).to_bytes(2, 'little')
        data[156:158] = subsystem.to_bytes(2, 'little')
        return data


if __name__ == '__main__':
    unittest.main()
