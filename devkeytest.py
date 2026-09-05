#!/usr/bin/env python3
import hmac, hashlib, base64, json, subprocess

DEV_SECRET = b"tokendance-dev-hmac-secret-at-least-32-bytes-long"
dev_key = hmac.new(DEV_SECRET, b"email_lookup", hashlib.sha256).digest()

app = 'app_1b0849c422b4a22f3b8e'
email = 'darrenhoomessi@gmail.com'
h = hmac.new(dev_key, (app + chr(0) + email).encode(), hashlib.sha256).hexdigest().upper()
print('DEV key + app+NUL+email:', h)

dsn = open('/etc/usercenter/secrets/mysql_dsn').read()
app_pw = dsn.split(':', 1)[1].split('@', 1)[0]
out = subprocess.run(
    ['docker', 'exec', 'usercenter-mysql', 'mysql', '-h127.0.0.1', '-N',
     '-uusercenter', '-p' + app_pw, 'usercenter_prod',
     '-e', "SELECT user_id, HEX(email_lookup_hash) FROM users WHERE user_id='usr_7be7204bc1ddb8f8e1ae936ba0';"],
    capture_output=True, text=True)
for line in out.stdout.splitlines():
    parts = line.split('\t')
    if len(parts) == 2:
        stored = parts[1].upper()
        print('stored:                ', stored)
        print('MATCH' if stored == h else 'no match')
