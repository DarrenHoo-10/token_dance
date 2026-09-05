#!/bin/bash
set -u
echo "== keyring files mtimes =="
ls -la /etc/usercenter/secrets/
echo "== env keyring lines =="
grep -E "KEYRING_FILE" /etc/usercenter/usercenter.env
echo "== current email_lookup.json =="
cat /etc/usercenter/secrets/email_lookup.json | head -3
