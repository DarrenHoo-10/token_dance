"""Activate a staged release on the configured Tencent host (run as root)."""
import datetime
import os
from pathlib import Path
import shutil
import subprocess
import sys
import urllib.request

release = Path(sys.argv[1]).resolve()
assert release.parent == Path('/opt/token-dance/releases') and (release/'bin/token-dance-api').is_file()
stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%d-%H%M%S')
backup = Path('/var/backups/token-dance') / stamp
backup.mkdir(parents=True, mode=0o700)
with (backup/'database.sql').open('wb') as stream:
    os.chmod(stream.name, 0o600)
    subprocess.run(['docker','exec','usercenter-mysql','sh','-c',
        'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysqldump -uroot --single-transaction --set-gtid-purged=OFF --no-tablespaces tokendance_prod'],
        stdout=stream,check=True)
subprocess.run(['systemd-run','--quiet','--wait','--pipe','--collect','--uid=tokendance','--gid=tokendance',
    '-p','EnvironmentFile=/etc/token-dance/app.env', str(release/'bin/token-dance-migrate')],check=True)

nginx = Path('/etc/nginx/sites-enabled/nexorai-fluxport').resolve()
snippet = Path('/etc/nginx/snippets/token-dance.conf')
old_config = nginx.read_bytes()
old_snippet = snippet.read_bytes() if snippet.exists() else None
shutil.copy2(nginx,backup/'nginx.conf')
if snippet.exists(): shutil.copy2(snippet,backup/'token-dance.conf')
include = '    include /etc/nginx/snippets/token-dance.conf;'
config = old_config.decode()
if include not in config:
    anchor = '    include /etc/nginx/snippets/nginx-flux-port-api.conf;'
    assert config.count(anchor) == 1, 'Expected canonical HTTPS server block'
    config = config.replace(anchor,include+'\n'+anchor)
snippet.write_bytes((release/'deploy/nginx-token-dance.conf').read_bytes())
nginx.write_text(config)
try:
    subprocess.run(['nginx','-t'],check=True)
except Exception:
    nginx.write_bytes(old_config)
    if old_snippet is None: snippet.unlink()
    else: snippet.write_bytes(old_snippet)
    raise

current = Path('/opt/token-dance/current')
previous = os.readlink(current) if current.is_symlink() else None
assert not current.exists() or current.is_symlink(), 'Current release must be a symlink'
next_link = current.with_name('next')
next_link.symlink_to(release)
os.replace(next_link,current)
web = Path('/var/www/token-dance')
assert not web.exists() or web.is_symlink(), 'Web target must be a symlink'
if not web.is_symlink(): web.symlink_to(current/'web')
for name in ['api','worker']:
    shutil.copyfile(release/f'deploy/token-dance-{name}.service',f'/etc/systemd/system/token-dance-{name}.service')
subprocess.run(['systemctl','daemon-reload'],check=True)
subprocess.run(['systemctl','enable','token-dance-api','token-dance-worker'],check=True)
subprocess.run(['systemctl','restart','token-dance-api','token-dance-worker'],check=True)
try:
    import time
    for attempt in range(15):
        try:
            with urllib.request.urlopen('http://127.0.0.1:8130/readyz',timeout=3) as response:
                assert response.status == 200
            break
        except Exception:
            if attempt == 14: raise
            time.sleep(1)
    subprocess.run(['systemctl','reload','nginx'],check=True)
except Exception:
    nginx.write_bytes(old_config)
    if old_snippet is None: snippet.unlink()
    else: snippet.write_bytes(old_snippet)
    if previous:
        next_link.symlink_to(previous)
        os.replace(next_link,current)
        subprocess.run(['systemctl','restart','token-dance-api','token-dance-worker'],check=True)
    raise
print('Activated',release,'; backup:',backup)
