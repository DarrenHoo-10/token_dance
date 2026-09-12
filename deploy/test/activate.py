"""Activate a clean release-branch build against the isolated test service."""
import datetime
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time
import urllib.request

release = Path(sys.argv[1]).resolve()
assert release.parent == Path('/opt/token-dance-test/releases')
build = json.loads((release / 'build-info.json').read_text())
assert build['branch'] == 'release' and re.fullmatch(r'[0-9a-f]{40}', build['commit'])
assert build['clean'] is True
config = Path('/etc/token-dance-test/app.env')
env = dict(line.split('=', 1) for line in config.read_text().splitlines() if line and not line.startswith('#'))
dsn = Path(env['TOKENDANCE_MYSQL_DSN_FILE'].strip('"')).read_text().strip()
assert dsn.split('/')[-1].split('?')[0] == 'tokendance_dev', 'Refusing non-test database'
stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%d-%H%M%S')
backup = Path('/var/backups/token-dance-test') / stamp
backup.mkdir(parents=True, mode=0o700)
services = ['token-dance-test-api', 'token-dance-test-worker']
grayscale = subprocess.run(['systemctl', 'is-active', '--quiet', 'token-dance-grayscale-sync']).returncode == 0
current = Path('/opt/token-dance-test/current')
previous = os.readlink(current) if current.is_symlink() else None
assert not current.exists() or current.is_symlink()

def migrate(*args):
    subprocess.run(['systemd-run', '--quiet', '--wait', '--pipe', '--collect',
                    '--uid=tokendance-test', '--gid=tokendance-test',
                    '-p', 'EnvironmentFile=' + str(config),
                    str(release / 'bin/token-dance-migrate'), *args], check=True)

try:
    if grayscale:
        subprocess.run(['systemctl', 'stop', 'token-dance-grayscale-sync'], check=True)
    if previous:
        subprocess.run(['systemctl', 'stop', *services], check=True)
    with (backup / 'database.sql').open('wb') as stream:
        os.chmod(stream.name, 0o600)
        subprocess.run(['docker', 'exec', 'usercenter-mysql', 'sh', '-c',
                        'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysqldump -uroot --single-transaction --set-gtid-purged=OFF --no-tablespaces tokendance_dev'],
                       stdout=stream, check=True)
    migrate()
    migrate('-check')
    subprocess.run(['nginx', '-t'], check=True)
    next_link = current.with_name('next-' + stamp)
    next_link.symlink_to(release)
    os.replace(next_link, current)
    web = Path('/var/www/token-dance-test')
    assert not web.exists() or web.is_symlink()
    if not web.is_symlink():
        web.symlink_to(current / 'web')
    for service in services:
        shutil.copyfile(release / 'deploy/test' / (service + '.service'), '/etc/systemd/system/' + service + '.service')
    subprocess.run(['systemctl', 'daemon-reload'], check=True)
    subprocess.run(['systemctl', 'enable', *services], check=True)
    subprocess.run(['systemctl', 'restart', *services], check=True)
    for attempt in range(20):
        try:
            with urllib.request.urlopen('http://127.0.0.1:8131/readyz', timeout=2) as response:
                assert response.status == 200
            break
        except Exception:
            if attempt == 19:
                raise
            time.sleep(1)
    subprocess.run(['systemctl', 'is-active', '--quiet', *services], check=True)
    subprocess.run(['systemctl', 'reload', 'nginx'], check=True)
except Exception:
    if previous:
        rollback = current.with_name('rollback-' + stamp)
        rollback.symlink_to(previous)
        os.replace(rollback, current)
        subprocess.run(['systemctl', 'restart', *services], check=False)
    else:
        subprocess.run(['systemctl', 'stop', *services], check=False)
    raise
finally:
    if grayscale:
        subprocess.run(['systemctl', 'start', 'token-dance-grayscale-sync'], check=True)

record = {**build, 'activatedAt': stamp, 'backup': str(backup), 'previous': previous,
          'url': 'https://nexorai.com.cn/token-dance-test/'}
(backup / 'release.json').write_text(json.dumps(record, indent=2) + '\n')
print(json.dumps(record))
