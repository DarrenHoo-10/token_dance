"""Idempotent Redis setup for an existing TokenDance host. Run as root.

Production uses localhost tokendance-redis. Test/dev uses a separate
redis_dev instance on www.nexorai.com.cn:6380, analogous to tokendance_dev.
Never prints the password or URL.
"""
import grp
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys

root = Path('/etc/token-dance')
secrets_dir = root / 'secrets'
app_env = root / 'app.env'
if not app_env.exists():
    print('TokenDance app.env is missing; run provision.py first.', file=sys.stderr)
    sys.exit(1)

gid = grp.getgrnam('tokendance').gr_gid
secrets_dir.mkdir(mode=0o750, exist_ok=True)
os.chown(secrets_dir, 0, gid)
secrets_dir.chmod(0o750)


def read_or_write_url(name, url_without_password, password=None):
    path = secrets_dir / name
    marker = 'redis://:'
    if path.exists():
        raw = path.read_text().strip()
        if not raw.startswith(marker) or '@' not in raw:
            print('Existing ' + name + ' secret is invalid.', file=sys.stderr)
            sys.exit(1)
        return raw[len(marker):].split('@', 1)[0], path
    if not password:
        password = secrets.token_hex(32)
    path.write_text('redis://:' + password + '@' + url_without_password + '\n')
    path.chmod(0o640)
    os.chown(path, 0, gid)
    return password, path


def ensure_redis(name, publish, password, volume):
    inspect = subprocess.run(['docker', 'inspect', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if inspect.returncode != 0:
        subprocess.run([
            'docker', 'run', '-d', '--name', name, '--restart', 'unless-stopped',
            '-p', publish, '-v', volume + ':/data', 'redis:7-alpine',
            'redis-server', '--requirepass', password, '--appendonly', 'yes',
            '--maxmemory-policy', 'noeviction',
        ], check=True)


prod_password, url_path = read_or_write_url('redis_url', '127.0.0.1:6379/0')
dev_password, _ = read_or_write_url('redis_dev_url', 'www.nexorai.com.cn:6380/0')
ensure_redis('tokendance-redis', '127.0.0.1:6379:6379', prod_password, 'tokendance-redis-data')
ensure_redis('redis_dev', '6380:6379', dev_password, 'redis-dev-data')

lines = app_env.read_text().splitlines()
keys = {line.split('=', 1)[0] for line in lines if '=' in line and not line.startswith('#')}
if 'TOKENDANCE_REDIS_URL_FILE' not in keys:
    lines.append('TOKENDANCE_REDIS_URL_FILE=' + json.dumps(str(url_path)))
    app_env.write_text('\n'.join(lines) + '\n')
    app_env.chmod(0o640)
    os.chown(app_env, 0, gid)

print('Production Redis is tokendance-redis at 127.0.0.1:6379; URL in /etc/token-dance/secrets/redis_url')
print('Test Redis is redis_dev at www.nexorai.com.cn:6380; URL in /etc/token-dance/secrets/redis_dev_url')
