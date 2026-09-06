"""Publish metadata only after uploaded public OSS packages match the build.

Standard library only. No OSS credentials are needed: CI uploads packages first.
This command writes the manifest on the host where Nginx serves it.
"""

import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile

MAX_DOWNLOAD = 150 * 1024 * 1024
MAX_MANIFEST = 2 * 1024 * 1024


def version(value):
    if not isinstance(value, str) or not re.fullmatch(r'(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)', value):
        raise ValueError('Version must be numeric major.minor.patch')
    parts = tuple(map(int, value.split('.')))
    if any(part > 2**64 - 1 for part in parts):
        raise ValueError('Version component exceeds uint64')
    return parts


def valid_url(value):
    if not isinstance(value, str) or re.search(r'\s|\\', value):
        return False
    try:
        url = urllib.parse.urlsplit(value)
        host = url.hostname or ''
        if (url.scheme != 'https' or url.username is not None or url.password is not None
                or '?' in value or '#' in value or url.port not in (None, 443)
                or not re.fullmatch(r'[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z][a-z0-9-]*', host) or host.endswith(('.', '.localhost', '.local', '.internal'))):
            return False
        try:
            ipaddress.ip_address(host)
            return False
        except ValueError:
            return not re.fullmatch(r'[\d.]+', host) and ':' not in host
    except ValueError:
        return False


def file_digest(path):
    digest = hashlib.sha256()
    size = 0
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(64 * 1024), b''):
            size += len(block)
            if size > MAX_DOWNLOAD:
                raise ValueError('Package exceeds download limit')
            digest.update(block)
    if not size:
        raise ValueError('Empty package')
    return size, digest.hexdigest()


def describe(path, url):
    if not valid_url(url):
        raise ValueError('Use a permanent public HTTPS package URL without credentials or a query')
    size, digest = file_digest(path)
    return {'url': url, 'sha256': digest, 'size': size}


def validate_build(build, release_version, executable):
    if (not isinstance(build, dict) or build.get('branch') != 'main' or build.get('version') != release_version
            or not isinstance(build.get('commit'), str) or not re.fullmatch(r'[0-9a-f]{40}', build['commit'])
            or build.get('sha256') != executable['sha256']):
        raise ValueError('Build provenance must match the executable, version and main branch')


def validate_manifest(manifest):
    if (not isinstance(manifest, dict) or type(manifest.get('schemaVersion')) is not int
            or manifest['schemaVersion'] != 1 or not isinstance(manifest.get('releases'), list)
            or len(manifest['releases']) > 100):
        raise ValueError('Invalid manifest')
    seen = set()
    for release in manifest['releases']:
        if (not isinstance(release, dict) or not isinstance(release.get('platform'), str)
                or not release['platform'] or not isinstance(release.get('notes'), str)
                or type(release.get('prerelease', False)) is not bool):
            raise ValueError('Invalid release')
        key = (release['platform'], version(release.get('version')))
        if key in seen:
            raise ValueError('Duplicate release')
        seen.add(key)
        date = release.get('publishedAt')
        if not isinstance(date, str) or not re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})', date):
            raise ValueError('Invalid release date')
        datetime.fromisoformat(date.replace('Z', '+00:00'))
        assets = [release.get('exe')]
        if release.get('zip') is not None:
            assets.append(release['zip'])
        for asset in assets:
            if (not isinstance(asset, dict) or not valid_url(asset.get('url'))
                    or type(asset.get('size')) is not int or not 0 < asset['size'] <= MAX_DOWNLOAD
                    or not isinstance(asset.get('sha256'), str) or not re.fullmatch(r'[a-fA-F0-9]{64}', asset['sha256'])):
                raise ValueError('Invalid package metadata')


def check_windows_exe(path):
    with path.open('rb') as stream:
        header = stream.read(64)
        if len(header) != 64 or header[:2] != b'MZ':
            raise ValueError('Expected a Windows executable')
        offset = int.from_bytes(header[60:64], 'little')
        if offset < 64 or offset > MAX_DOWNLOAD - 6:
            raise ValueError('Invalid PE header')
        stream.seek(offset)
        if stream.read(6) != b'PE\0\0\x64\x86':
            raise ValueError('Expected a Windows x64 executable')


def check_zip(path, executable):
    with zipfile.ZipFile(path) as archive:
        entries = archive.infolist()
        if len(entries) > 100 or sum(entry.file_size for entry in entries) > MAX_DOWNLOAD * 2:
            raise ValueError('ZIP is too large')
        for entry in entries:
            if '\\' in entry.filename or entry.filename.startswith('/') or '..' in entry.filename.split('/'):
                raise ValueError('Unsafe ZIP path')
        candidates = [entry for entry in entries if entry.filename == 'TokenDance.exe']
        if len(candidates) != 1 or candidates[0].file_size != executable['size']:
            raise ValueError('ZIP must contain the same TokenDance.exe exactly once')
        with archive.open(candidates[0]) as stream:
            digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        if digest != executable['sha256']:
            raise ValueError('ZIP executable differs from standalone executable')


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError('Package URL must not redirect')


def verify_remote(asset):
    request = urllib.request.Request(asset['url'], headers={'Accept-Encoding': 'identity', 'User-Agent': 'TokenDance-ReleasePublisher/1'})
    opener = urllib.request.build_opener(NoRedirect())
    digest = hashlib.sha256()
    size = 0
    deadline = time.monotonic() + 300
    try:
        with opener.open(request, timeout=20) as response:
            if response.status != 200:
                raise ValueError('Package is not publicly downloadable')
            for block in iter(lambda: response.read(64 * 1024), b''):
                size += len(block)
                if size > asset['size'] or time.monotonic() > deadline:
                    raise ValueError('Remote package size or time limit exceeded')
                digest.update(block)
    except (OSError, urllib.error.HTTPError) as error:
        raise ValueError('Remote package verification failed') from error
    if size != asset['size'] or digest.hexdigest() != asset['sha256']:
        raise ValueError('Remote package differs from the verified local build')


@contextmanager
def manifest_lock(path):
    path.parent.mkdir(parents=True, exist_ok=True)
    lock = path.with_name(path.name + '.lock')
    fd = os.open(lock, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write(str(os.getpid()))
        yield
    finally:
        lock.unlink()


def update_manifest(path, release, verify=verify_remote):
    validate_manifest({'schemaVersion': 1, 'releases': [release]})
    with manifest_lock(path):
        current = {'schemaVersion': 1, 'releases': []}
        if path.exists():
            if path.stat().st_size > MAX_MANIFEST:
                raise ValueError('Existing manifest too large')
            current = json.loads(path.read_text(encoding='utf-8'))
        validate_manifest(current)
        target = version(release['version'])
        matching = [r for r in current['releases'] if r['platform'] == release['platform']]
        for previous in matching:
            prior = version(previous['version'])
            if prior > target:
                raise ValueError('Refusing to publish an older version')
            if prior == target:
                if previous['exe'] != release['exe'] or previous.get('zip') != release.get('zip'):
                    raise ValueError('A published version cannot change its packages')
                return False
        # Check the complete remote bytes without credentials before exposing links.
        verify(release['exe'])
        if release.get('zip'):
            verify(release['zip'])
        # The public feed keeps one current release per platform; OSS keeps history.
        current['releases'] = [r for r in current['releases'] if r['platform'] != release['platform']] + [release]
        data = (json.dumps(current, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
        if len(data) > MAX_MANIFEST or len(current['releases']) > 100:
            raise ValueError('Manifest exceeds limit')
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as stream:
                temporary = Path(stream.name)
                stream.write(data)
                stream.flush()
                os.fsync(stream.fileno())
            temporary.chmod(0o644)
            os.replace(temporary, path)
        finally:
            if temporary is not None and temporary.exists():
                temporary.unlink()
        return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--exe', type=Path, required=True)
    parser.add_argument('--exe-url', required=True)
    parser.add_argument('--zip', type=Path)
    parser.add_argument('--zip-url')
    parser.add_argument('--build-info', type=Path, required=True)
    parser.add_argument('--notes-file', type=Path, required=True)
    parser.add_argument('--manifest', type=Path, required=True)
    parser.add_argument('--prerelease', action='store_true')
    args = parser.parse_args()
    version(args.version)
    if bool(args.zip) != bool(args.zip_url):
        raise ValueError('ZIP path and URL must be supplied together')
    check_windows_exe(args.exe)
    executable = describe(args.exe, args.exe_url)
    build = json.loads(args.build_info.read_text(encoding='utf-8-sig'))
    validate_build(build, args.version, executable)
    release = {'version': args.version, 'platform': 'windows-x64',
               'publishedAt': datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z'),
               'notes': args.notes_file.read_text(encoding='utf-8-sig'),
               'prerelease': args.prerelease, 'exe': executable}
    if args.zip:
        check_zip(args.zip, executable)
        release['zip'] = describe(args.zip, args.zip_url)
    changed = update_manifest(args.manifest, release)
    print('Release manifest published.' if changed else 'Release already published; manifest unchanged.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, zipfile.BadZipFile):
        raise SystemExit('Release not published: check package URLs, files, provenance and manifest. No secrets are logged.')
