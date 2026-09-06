"""Internal MySQL release ledger. Public clients read only its JSON projection."""

from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import tempfile

import pymysql

from publish_manifest import MAX_MANIFEST, validate_build, validate_manifest, version


def connect():
    # Configuration is a restricted file, never a command-line DSN or public asset.
    config = json.loads(Path(os.environ['TOKENDANCE_RELEASE_DB_CONFIG_FILE']).read_text(encoding='utf-8'))
    allowed = {'host', 'port', 'user', 'password', 'database', 'unix_socket', 'ssl_ca'}
    if not isinstance(config, dict) or set(config) - allowed or not config.get('database'):
        raise ValueError('Invalid release database configuration')
    if config.get('host', 'localhost') not in ('localhost', '127.0.0.1', '::1') and not config.get('ssl_ca'):
        raise ValueError('Remote database requires verified TLS')
    tls = {'ssl_verify_cert': True, 'ssl_verify_identity': True} if config.get('ssl_ca') else {}
    return pymysql.connect(**config, **tls, charset='utf8mb4', autocommit=False,
                           connect_timeout=10, read_timeout=30, write_timeout=30)


@contextmanager
def locked(db):
    # Session advisory lock survives COMMIT, serializing DB + filesystem publication.
    with db.cursor() as cursor:
        cursor.execute('SELECT DATABASE()')
        name = 'td-release-' + hashlib.sha256(cursor.fetchone()[0].encode()).hexdigest()[:40]
        cursor.execute('SELECT GET_LOCK(%s, 30)', (name,))
        if cursor.fetchone()[0] != 1:
            raise ValueError('Another publisher is running')
    try:
        yield
    finally:
        db.rollback()
        with db.cursor() as cursor:
            cursor.execute('SELECT RELEASE_LOCK(%s)', (name,))


def snapshot(db):
    with db.cursor() as cursor:
        cursor.execute('SELECT revision FROM desktop_release_publication WHERE id=1')
        state = cursor.fetchone()
        if state is None:
            raise ValueError('No release publication state; publish a release first')
        cursor.execute('SELECT r.manifest_json FROM desktop_release_channels c '
                       'JOIN desktop_releases r ON r.id=c.release_id ORDER BY c.platform')
        manifest = {'schemaVersion': 1, 'releases': [json.loads(row[0]) for row in cursor.fetchall()]}
    validate_manifest(manifest)
    data = (json.dumps(manifest, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
    if len(data) > MAX_MANIFEST:
        raise ValueError('Manifest exceeds limit')
    return state[0], data


def render(db, path):
    revision, data = snapshot(db)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as stream:
            temporary = Path(stream.name)
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        temporary.chmod(0o644)
        os.replace(temporary, path)
        if os.name != 'nt':
            fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
    finally:
        if temporary is not None and temporary.exists():
            temporary.unlink()
    with db.cursor() as cursor:
        cursor.execute('UPDATE desktop_release_publication SET rendered_revision=%s, '
                       'rendered_at=UTC_TIMESTAMP(3) WHERE id=1', (revision,))
    db.commit()


def reconcile(db, path):
    with locked(db):
        # Always rewrite, even when revisions match: recover a lost/corrupt file.
        render(db, path)


def publish(db, path, release, build, verify):
    validate_manifest({'schemaVersion': 1, 'releases': [release]})
    validate_build(build, release['version'], release['exe'])
    if len(release['platform']) > 32 or len(release['version']) > 64:
        raise ValueError('Release identifier exceeds database limit')
    # Network checks happen before taking the publication lock or touching the DB.
    verify(release['exe'])
    if release.get('zip'):
        verify(release['zip'])
    with locked(db):
        with db.cursor() as cursor:
            cursor.execute('SELECT r.manifest_json FROM desktop_release_channels c '
                           'JOIN desktop_releases r ON r.id=c.release_id WHERE c.platform=%s',
                           (release['platform'],))
            current = cursor.fetchone()
            if current and version(json.loads(current[0])['version']) > version(release['version']):
                raise ValueError('Refusing to publish an older version')
            cursor.execute('SELECT id, manifest_json, source_commit FROM desktop_releases '
                           'WHERE platform=%s AND version=%s', (release['platform'], release['version']))
            previous = cursor.fetchone()
            changed = previous is None
            if previous:
                stored = json.loads(previous[1])
                # Retry-generated timestamp is ignored; original metadata remains immutable.
                candidate = {**release, 'publishedAt': stored['publishedAt']}
                if candidate != stored or build['commit'] != previous[2]:
                    raise ValueError('A published version is immutable')
                release_id = previous[0]
            else:
                cursor.execute('INSERT INTO desktop_releases '
                               '(platform, version, source_branch, source_commit, manifest_json) '
                               'VALUES (%s,%s,%s,%s,%s)', (release['platform'], release['version'],
                               'main', build['commit'], json.dumps(release, ensure_ascii=False)))
                release_id = cursor.lastrowid
                cursor.execute('INSERT INTO desktop_release_channels (platform,release_id) VALUES (%s,%s) '
                               'ON DUPLICATE KEY UPDATE release_id=VALUES(release_id)',
                               (release['platform'], release_id))
                cursor.execute('INSERT INTO desktop_release_publication (id,revision) VALUES (1,1) '
                               'ON DUPLICATE KEY UPDATE revision=revision+1')
        # Validate the complete projection before committing. A failed render leaves a
        # durable revision gap and is recoverable without re-uploading any package.
        snapshot(db)
        db.commit()
        render(db, path)
        return changed
