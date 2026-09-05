#!/bin/bash
set -u
KEY=$(python3 -c "import json;print(json.load(open('/etc/usercenter/secrets/email_lookup.json'))['keys']['1'])")
APP="app_1b0849c422b4a22f3b8e"
python3 - "$KEY" "$APP" << 'PYEOF'
import hmac, hashlib, sys, json
key = sys.argv[1].encode()
app = sys.argv[2]
import base64
key_bytes = base64.b64decode(key)
for email in ["darrenhoomessi@gmail.com"]:
    h = hmac.new(key_bytes, (app + "\x00" + email).encode(), hashlib.sha256).hex()
    print("computed:", h)
PYEOF
DSN=$(cat /etc/usercenter/secrets/mysql_dsn)
APP_PW=$(echo "$DSN" | sed 's/^usercenter:\([^@]*\)@.*/\1/')
docker exec usercenter-mysql mysql -h127.0.0.1 -uusercenter -p"$APP_PW" usercenter_prod -e "SELECT user_id, HEX(email_lookup_hash) FROM users;" 2>/dev/null
