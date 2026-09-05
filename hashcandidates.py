#!/usr/bin/env python3
import hmac, hashlib, base64, json, subprocess

cfg = json.load(open('/etc/usercenter/secrets/email_lookup.json'))
key = base64.b64decode(cfg['keys'][str(cfg['currentVersion'])])
app = 'app_1b0849c422b4a22f3b8e'
email = 'darrenhoomessi@gmail.com'

candidates = {
    'app+NUL+email': (app + chr(0) + email).encode(),
    'NUL+email': (chr(0) + email).encode(),
    'email only': email.encode(),
    'app+email (no NUL)': (app + email).encode(),
    'email+app (no NUL)': (email + app).encode(),
}

dsn = open('/etc/usercenter/secrets/mysql_dsn').read()
app_pw = dsn.split(':', 1)[1].split('@', 1)[0]
out = subprocess.run(
    ['docker', 'exec', 'usercenter-mysql', 'mysql', '-h127.0.0.1', '-N',
     '-uusercenter', '-p' + app_pw, 'usercenter_prod',
     '-e', "SELECT user_id, HEX(email_lookup_hash) FROM users WHERE email_lookup_hash IS NOT NULL;"],
    capture_output=True, text=True)
stored = {}
for line in out.stdout.splitlines():
    parts = line.split('\t')
    if len(parts) == 2 and len(parts[1]) == 64:
        stored[parts[0]] = parts[1].upper()

for uid, stored_hash in stored.items():
    print(uid)
    matched = False
    for name, material in candidates.items():
        h = hmac.new(key, material, hashlib.sha256).hexdigest().upper()
        tag = ' <== MATCH' if h == stored_hash else ''
        print(f'  {name}: {h[:16]}...{tag}')
        if h == stored_hash:
            matched = True
    if not matched:
        print('  NONE of the candidates match; stored =', stored_hash)
