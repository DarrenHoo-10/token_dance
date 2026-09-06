"""Integration tests against a disposable loopback-only MySQL container.

Run with Docker available. No existing database or cloud service is accessed.
"""
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import unittest
from unittest.mock import patch

import pymysql
import release_registry as registry

ROOT = Path(__file__).resolve().parents[2]


class RegistryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.container = 'td-release-test-' + secrets.token_hex(6)
        cls.password = secrets.token_urlsafe(32)
        env = {**os.environ, 'MYSQL_ROOT_PASSWORD': cls.password}
        subprocess.run(['docker', 'run', '-d', '--rm', '--name', cls.container,
                        '-e', 'MYSQL_ROOT_PASSWORD', '-p', '127.0.0.1::3306', 'mysql:8.0.34'],
                       env=env, check=True, capture_output=True, timeout=120)
        cls.addClassCleanup(lambda: subprocess.run(['docker', 'rm', '-f', cls.container],
                                                   check=True, capture_output=True, timeout=30))
        result = subprocess.run(['docker', 'port', cls.container, '3306'], check=True,
                                capture_output=True, text=True, timeout=10)
        cls.port = int(result.stdout.strip().rsplit(':', 1)[1])
        deadline = time.monotonic() + 120
        while True:
            try:
                db = cls.connection(database=None)
                db.close()
                break
            except pymysql.Error:
                if time.monotonic() > deadline:
                    raise RuntimeError('Disposable MySQL did not become ready') from None
                time.sleep(1)

    @classmethod
    def connection(cls, database='td_release_test'):
        return pymysql.connect(host='127.0.0.1', port=cls.port, user='root',
                               password=cls.password, database=database, charset='utf8mb4',
                               autocommit=False, connect_timeout=2)

    def setUp(self):
        db = self.connection(database=None)
        with db.cursor() as cursor:
            cursor.execute('DROP DATABASE IF EXISTS td_release_test')
            cursor.execute('CREATE DATABASE td_release_test')
        db.close()
        self.db = self.connection()
        self.addCleanup(self.db.close)
        migration = ROOT / 'server/db/migrations/0008_desktop_releases.sql'
        self.assertEqual(migration.read_bytes(),
                         (ROOT / 'server/internal/migrate/migrations' / migration.name).read_bytes())
        with self.db.cursor() as cursor:
            for statement in migration.read_text().split(';'):
                if statement.strip():
                    cursor.execute(statement)
        self.db.commit()
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / 'stable.json'
        self.release = json.loads((ROOT / 'schemas/fixtures/desktop-release-manifest.json').read_text())['releases'][0]

    def build(self, release):
        return {'branch': 'main', 'version': release['version'], 'commit': 'a' * 40,
                'sha256': release['exe']['sha256']}

    def publish(self, release=None, verify=lambda _: None, db=None):
        release = release or self.release
        return registry.publish(db or self.db, self.path, release, self.build(release), verify)

    def query(self, sql):
        self.db.commit()
        with self.db.cursor() as cursor:
            cursor.execute(sql)
            return cursor.fetchall()

    def test_history_retained_and_public_projection_has_latest_only(self):
        self.publish()
        self.publish({**self.release, 'version': '0.3.0'})
        self.assertEqual(self.query('SELECT COUNT(*) FROM desktop_releases')[0][0], 2)
        self.assertEqual(json.loads(self.path.read_text())['releases'][0]['version'], '0.3.0')
        self.assertEqual(self.query('SELECT revision,rendered_revision FROM desktop_release_publication'), ((2, 2),))
        self.assertNotIn('source_commit', self.path.read_text())

    def test_failure_before_commit_does_not_publish_or_write_history(self):
        self.publish()
        old = self.path.read_bytes()
        with patch.object(registry, 'snapshot', side_effect=ValueError('invalid projection')):
            with self.assertRaises(ValueError):
                self.publish({**self.release, 'version': '0.3.0'})
        self.assertEqual(self.path.read_bytes(), old)
        self.assertEqual(self.query('SELECT COUNT(*) FROM desktop_releases')[0][0], 1)

    def test_failed_file_write_leaves_pending_revision_and_reconcile_repairs(self):
        self.publish()
        old = self.path.read_bytes()
        with patch.object(registry.os, 'replace', side_effect=OSError('disk unavailable')):
            with self.assertRaises(OSError):
                self.publish({**self.release, 'version': '0.3.0'})
        self.assertEqual(self.path.read_bytes(), old)
        self.assertEqual(self.query('SELECT revision,rendered_revision FROM desktop_release_publication'), ((2, 1),))
        registry.reconcile(self.db, self.path)
        self.assertEqual(json.loads(self.path.read_text())['releases'][0]['version'], '0.3.0')
        self.assertEqual(self.query('SELECT revision,rendered_revision FROM desktop_release_publication'), ((2, 2),))

    def test_retry_immutable_and_downgrade_rejected(self):
        self.publish()
        self.assertFalse(self.publish({**self.release, 'publishedAt': '2026-09-08T00:00:00Z'}))
        for changed in [{**self.release, 'version': '0.1.0'},
                        {**self.release, 'notes': 'changed'},
                        {**self.release, 'exe': {**self.release['exe'], 'sha256': 'b' * 64}}]:
            with self.assertRaises(ValueError):
                self.publish(changed)
        with self.assertRaises(ValueError):
            registry.publish(self.db, self.path, self.release,
                             {**self.build(self.release), 'commit': 'b' * 40}, lambda _: None)
        self.assertEqual(self.query('SELECT COUNT(*) FROM desktop_releases')[0][0], 1)

    def test_network_failure_preserves_database_and_manifest(self):
        self.publish()
        old = self.path.read_bytes()
        def fail(_):
            raise ValueError('Remote download failed')
        with self.assertRaises(ValueError):
            self.publish({**self.release, 'version': '0.3.0'}, verify=fail)
        self.assertEqual(self.path.read_bytes(), old)
        self.assertEqual(self.query('SELECT COUNT(*) FROM desktop_releases')[0][0], 1)

    def test_reconcile_recovers_deleted_file_and_preserves_other_platforms(self):
        self.publish()
        self.publish({**self.release, 'platform': 'macos-arm64'})
        self.path.unlink()
        registry.reconcile(self.db, self.path)
        self.assertEqual(len(json.loads(self.path.read_text())['releases']), 2)

    def test_concurrent_publishers_cannot_overwrite_newer_release(self):
        self.publish()
        def run(value):
            db = self.connection()
            try:
                return self.publish({**self.release, 'version': value}, db=db)
            except ValueError:
                return False  # Older version may lose the race and must be rejected.
            finally:
                db.close()
        with ThreadPoolExecutor(max_workers=2) as pool:
            list(pool.map(run, ['0.3.0', '0.4.0']))
        self.assertEqual(json.loads(self.path.read_text())['releases'][0]['version'], '0.4.0')
        self.assertEqual(self.query('SELECT revision=rendered_revision FROM desktop_release_publication'), ((1,),))

    def test_existing_migration_runner_applies_and_checks_new_tables(self):
        # Go migration tests reset this dedicated container database, never a shared DB.
        with self.db.cursor() as cursor:
            cursor.execute('CREATE DATABASE td_migration_test')
        env = {**os.environ, 'TOKENDANCE_TEST_MYSQL_DSN':
               f'root:{self.password}@tcp(127.0.0.1:{self.port})/td_migration_test?parseTime=true'}
        result = subprocess.run(['go', 'test', './internal/migrate', '-count=1'],
                                cwd=ROOT / 'server', env=env, capture_output=True,
                                text=True, timeout=240)
        self.assertEqual(result.returncode, 0,
                         (result.stdout + result.stderr).replace(self.password, '[REDACTED]'))


if __name__ == '__main__':
    unittest.main()
