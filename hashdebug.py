#!/usr/bin/env python3
import hmac, hashlib, base64, json, subprocess, sys

cfg = json.load(open('/etc/usercenter/secrets/email_lookup.json'))
key = base64.b64decode(cfg['keys'][str(cfg['currentVersion'])])
app = 'app_1b0849c422b4a22f3b8e'
email = 'darrenhoomessi@gmail.com'
computed = hmac.new(key, (app + chr(0) + email).encode(), hashlib.sha256).hexdigest().upper()
print('computed:', computed)

dsn = open('/etc/usercenter/secrets/mysql_dsn').read()
app_pw = dsn.split(':', 1)[1].split('@', 1)[0]
out = subprocess.run(
    ['docker', 'exec', 'usercenter-mysql', 'mysql', '-h127.0.0.1',
     '-uusercenter', '-p' + app_pw, 'usercenter_prod',
     '-e', 'SELECT user_id, HEX(email_lookup_hash) AS h FROM users;'],
    capture_output=True, text=True)
for line in out.stdout.splitlines():
    if line.startswith('usr_'):
        uid, stored = line.split('\t')
        match = 'MATCH' if stored.upper() == computed else 'MISMATCH'
        print(uid, 'stored:', stored[:16] + '...', match)
