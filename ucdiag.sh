#!/bin/bash
set -u
echo "== api failure reason =="
journalctl -u usercenter-api --no-pager -n 6 --output=cat | grep -iE "fatal|error" | tail -2
echo "== usercenter_prod tables =="
DSN=$(cat /etc/usercenter/secrets/mysql_dsn)
APP_PW=$(echo "$DSN" | sed 's/^usercenter:\([^@]*\)@.*/\1/')
docker exec usercenter-mysql mysql -h127.0.0.1 -uusercenter -p"$APP_PW" usercenter_prod -e "SHOW TABLES;" 2>/dev/null | head -6
echo "== api env check =="
grep -E "USERCENTER_ENVIRONMENT|MYSQL_DSN" /etc/usercenter/usercenter.env | sed 's/DSN_FILE=.*/DSN_FILE=***/'
