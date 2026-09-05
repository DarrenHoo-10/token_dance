#!/usr/bin/env python3
"""Re-hash existing users' email/auth-subject hashes from the override-era
derivation to the file-keyring derivation, so logins survive the
production switch."""
import hmac, hashlib, base64, json, subprocess

cfg = json.load(open('/etc/usercenter/secrets/email_lookup.json'))
lookup_key = base64.b64decode(cfg['keys'][str(cfg['currentVersion'])])
subj_cfg = json.load(open('/etc/usercenter/secrets/auth_subject.json'))
subj_key = base64.b64decode(subj_cfg['keys'][str(subj_cfg['currentVersion'])])

# legacy override key (test era)
hmac_secret = open('/etc/usercenter/secrets/hmac_secret').read().strip()
legacy_lookup = hmac.new(hmac_secret.encode(), b'email_lookup', hashlib.sha256).digest()

app = 'app_1b0849c422b4a22f3b8e'
# user email known: decrypt not needed, we know both accounts
accounts = {
    'usr_7be7204bc1ddb8f8e1ae936ba0': 'darrenhoomessi@gmail.com',
    'usr_dee4a00b306a19062b1ad3c255': 'test2@x.com',
}

dsn = open('/etc/usercenter/secrets/mysql_dsn').read()
app_pw = dsn.split(':', 1)[1].split('@', 1)[0]

for uid, email in accounts.items():
    new_lookup = hmac.new(lookup_key, (app + chr(0) + email).encode(), hashlib.sha256).digest()
    new_subject = hmac.new(subj_key, ('email:' + email).encode(), hashlib.sha256).digest()
    sql = ("UPDATE users SET email_lookup_hash = UNHEX('%s'), auth_subject_hash = UNHEX('%s') "
           "WHERE user_id = '%s' AND app_id = '%s';" % (new_lookup.hex(), new_subject.hex(), uid, app))
    out = subprocess.run(['docker', 'exec', 'usercenter-mysql', 'mysql', '-h127.0.0.1',
                          '-uusercenter', '-p' + app_pw, 'usercenter_prod', '-e', sql],
                         capture_output=True, text=True)
    print(uid, 're-hashed:', 'OK' if out.returncode == 0 else out.stderr[:200])

print('done')
