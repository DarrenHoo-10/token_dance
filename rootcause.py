#!/usr/bin/env python3
import hmac, hashlib, base64, json, subprocess

# legacy override derivation (test mode): key = HMAC(hmac_secret, purpose)
hmac_secret = open('/etc/usercenter/secrets/hmac_secret').read().strip()
purpose_key = hmac.new(hmac_secret.encode(), b'email_lookup', hashlib.sha256).digest()

app = 'app_1b0849c422b4a22f3b8e'
email = 'darrenhoomessi@gmail.com'
h = hmac.new(purpose_key, (app + chr(0) + email).encode(), hashlib.sha256).hexdigest().upper()
print('override-key hash:', h)

dsn = open('/etc/usercenter/secrets/mysql_dsn').read()
app_pw = dsn.split(':', 1)[1].split('@', 1)[0]
out = subprocess.run(
    ['docker', 'exec', 'usercenter-mysql', 'mysql', '-h127.0.0.1', '-N',
     '-uusercenter', '-p' + app_pw, 'usercenter_prod',
     '-e', "SELECT HEX(email_lookup_hash) FROM users WHERE user_id='usr_7be7204bc1ddb8f8e1ae936ba0';"],
    capture_output=True, text=True)
stored = out.stdout.strip().splitlines()[-1].strip().upper() if out.stdout.strip() else ''
print('stored:          ', stored)
print('ROOT CAUSE CONFIRMED' if stored == h else 'no match — keep digging')
