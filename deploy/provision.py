"""Run as root on the target host; provider settings arrive as JSON on stdin.

Only initializes TokenDance's database login and secret files. Existing app.env
is preserved on subsequent releases. Never prints supplied or generated secrets.
"""
import grp
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys

settings = json.load(sys.stdin)
root = Path('/etc/token-dance')
if subprocess.run(['id', 'tokendance'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
    subprocess.run(['useradd', '--system', '--home', '/nonexistent', '--shell', '/usr/sbin/nologin', 'tokendance'], check=True)
if (root / 'app.env').exists():
    print('Existing TokenDance configuration preserved.')
    sys.exit(0)
gid = grp.getgrnam('tokendance').gr_gid
root.mkdir(mode=0o750, exist_ok=True)
(root / 'secrets').mkdir(mode=0o750, exist_ok=True)
for folder in [root, root / 'secrets']:
    os.chown(folder, 0, gid)
    folder.chmod(0o750)

def secret_file(name, value):
    path = root / 'secrets' / name
    path.write_text(value + '\n')
    path.chmod(0o640)
    os.chown(path, 0, gid)
    return str(path)

def ensure_redis(name, publish, password, volume):
    inspect = subprocess.run(['docker', 'inspect', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if inspect.returncode != 0:
        subprocess.run([
            'docker', 'run', '-d', '--name', name, '--restart', 'unless-stopped',
            '-p', publish, '-v', volume + ':/data', 'redis:7-alpine',
            'redis-server', '--requirepass', password, '--appendonly', 'yes',
            '--maxmemory-policy', 'noeviction',
        ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

password = secrets.token_hex(32)
redis_password = secrets.token_hex(32)
redis_dev_password = secrets.token_hex(32)
sql = f"""CREATE DATABASE IF NOT EXISTS tokendance_prod CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER IF NOT EXISTS 'tokendance_app'@'%' IDENTIFIED BY '{password}';
ALTER USER 'tokendance_app'@'%' IDENTIFIED BY '{password}';
GRANT ALL PRIVILEGES ON tokendance_prod.* TO 'tokendance_app'@'%';
"""
subprocess.run(['docker', 'exec', '-i', 'usercenter-mysql', 'sh', '-c',
    'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot'], input=sql, text=True, check=True,
    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
ensure_redis('tokendance-redis', '127.0.0.1:6379:6379', redis_password, 'tokendance-redis-data')
ensure_redis('redis_dev', '6380:6379', redis_dev_password, 'redis-dev-data')
env = {
    'TOKENDANCE_ENVIRONMENT': 'production',
    'TOKENDANCE_HTTP_ADDR': '127.0.0.1:8130',
    'TOKENDANCE_TRUSTED_PROXY_CIDRS': '127.0.0.1/32,::1/128',
    'TOKENDANCE_MYSQL_DSN_FILE': secret_file('mysql_dsn',
        f'tokendance_app:{password}@tcp(127.0.0.1:3307)/tokendance_prod?parseTime=true&loc=UTC'),
    'TOKENDANCE_REDIS_URL_FILE': secret_file('redis_url',
        f'redis://:{redis_password}@127.0.0.1:6379/0'),
    'TOKENDANCE_OBJECT_PROVIDER': 's3',
    'TOKENDANCE_OBJECT_PREFIX': 'token-dance',
    'TOKENDANCE_OBJECT_USE_PATH_STYLE': 'false',
    'GOMEMLIMIT': '192MiB',
}
secret_file('redis_dev_url', f'redis://:{redis_dev_password}@www.nexorai.com.cn:6380/0')
for purpose in ['EMAIL_LOOKUP', 'AUTH_SUBJECT', 'SESSION', 'CSRF', 'VERIFICATION_CODE', 'BINDING_CODE', 'GRANT', 'IDEMPOTENCY', 'AEAD']:
    name = purpose + ('_HMAC' if purpose != 'AEAD' else '')
    env[f'TOKENDANCE_{name}_KEYRING_FILE'] = secret_file(purpose.lower()+'.json',
        json.dumps({'currentVersion': 1, 'keys': {'1': secrets.token_hex(32)}}))
allowed = ['EMAIL_PROVIDER', 'SMTP_HOST', 'SMTP_PORT', 'SMTP_TLS_MODE', 'SMTP_USERNAME', 'SMTP_FROM',
    'OBJECT_ENDPOINT', 'OBJECT_REGION', 'OBJECT_BUCKET']
for name in allowed:
    value = settings['TOKENDANCE_'+name]
    if '\n' in value or '\r' in value:
        raise ValueError('Invalid multiline provider setting')
    env['TOKENDANCE_'+name] = value
for name in ['SMTP_PASSWORD', 'OBJECT_ACCESS_KEY', 'OBJECT_SECRET_KEY']:
    env['TOKENDANCE_'+name+'_FILE'] = secret_file(name.lower(), settings['TOKENDANCE_'+name])
path = root / 'app.env'
path.write_text(''.join(k+'='+json.dumps(v)+'\n' for k,v in env.items()))
path.chmod(0o640)
os.chown(path,0,gid)
print('TokenDance production configuration created; credentials remain in /etc/token-dance/secrets.')
