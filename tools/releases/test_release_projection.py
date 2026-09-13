"""Offline projection checks; no database is opened."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import MagicMock, patch
import release_registry as registry


class ProjectionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / 'stable.json'
        self.db = MagicMock()
        self.windows = {'platform':'windows-x64','version':'1.0.0','exe':{'url':'https://example.com/app.exe'}}
        self.mac = {'platform':'macos-arm64','version':'1.0.0','dmg':{'url':'https://example.com/app.dmg'}}
        self.payload = json.dumps({'schemaVersion':1,'releases':[self.windows,self.mac]}).encode()

    def test_legacy_windows_feed_never_contains_mac_entries(self):
        with patch.object(registry, 'snapshot', return_value=(7,self.payload)):
            registry.render(self.db, self.path)
        self.assertEqual(json.loads(self.path.read_text())['releases'], [self.windows])
        self.assertEqual(json.loads(self.path.with_name('macos.json').read_text())['releases'], [self.mac])
        self.db.commit.assert_called_once()

    def test_rendered_revision_is_not_advanced_if_second_feed_fails(self):
        with patch.object(registry, 'snapshot', return_value=(7,self.payload)), patch.object(registry, 'write_projection', side_effect=[None,OSError('disk')]):
            with self.assertRaises(OSError):
                registry.render(self.db,self.path)
        self.db.commit.assert_not_called()
        self.db.cursor.assert_not_called()

    def test_windows_only_releases_generate_an_explicit_empty_mac_feed(self):
        payload = json.dumps({'schemaVersion':1,'releases':[self.windows]}).encode()
        with patch.object(registry, 'snapshot', return_value=(1,payload)):
            registry.render(self.db,self.path)
        self.assertEqual(json.loads(self.path.with_name('macos.json').read_text())['releases'], [])

    def test_mac_feed_cannot_be_used_as_the_primary_manifest_path(self):
        with self.assertRaises(ValueError):
            registry.render(self.db, self.path.with_name('macos.json'))
        self.db.cursor.assert_not_called()


if __name__ == '__main__':
    unittest.main()
