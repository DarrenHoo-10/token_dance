#!/usr/bin/env python3
import hmac, hashlib, base64, json, subprocess, sys, urllib.request

# 1) create a fresh challenge via the API for a throwaway email
req = urllib.request.Request(
    'http://127.0.0.1:8100/api/v1/auth/register/code',
    data=json.dumps({'email': 'hashprobe@x.com', 'locale': 'zh-CN'}).encode(),
    headers={'Content-Type': 'application/json'}, method='POST')
print('register/code:', urllib.request.urlopen(req).status)

cfg = json.load(open('/etc/usercenter/secrets/email_lookup.json'))
key = base64.b64decode(cfg['keys'][str(cfg['currentVersion'])])
app = 'app_1b0849c422b4a22f3b8e'
email = 'hashprobe@x.com'
computed = hmac.new(key, (app + chr(0) + email).encode(), hashlib.sha256).hexdigest().upper()
computed_noapp = hmac.new(key, email.encode(), hashlib.sha256).hexdigest().upper()
print('computed (app-prefixed):', computed)
print('computed (no app):      ', computed_noapp)

# 2) read the challenge hash from db
dsn = open('/etc/usercenter/secrets/mysql_dsn').read()
app_pw = dsn.split(':', 1)[1].split('@', 1)[0]
out = subprocess.run(
    ['docker', 'exec', 'usercenter-mysql', 'mysql', '-h127.0.0.1',
     '-uusercenter', '-p' + app_pw, 'usercenter_prod',
     '-e', "SELECT HEX(email_lookup_hash) AS h FROM usercenter_prod.email_challenges WHERE email_lookup_hash IS NOT NULL ORDER BY created_at DESC LIMIT 1;"],
    capture_output=True, text=True)
for line in out.stdout.splitlines():
    if len(line) == 64 and all(c in '0123456789ABCDEFabcdef' for c in line):
        print('stored:  ', line.upper())
        if line.upper() == computed:
            print('=> MATCHES app-prefixed derivation')
        elif line.upper() == computed_noapp:
            print('=> MATCHES no-app derivation (binary computes WITHOUT app prefix!)')
