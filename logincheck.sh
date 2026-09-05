#!/bin/bash
set -u
curl -s -X POST http://127.0.0.1:8100/api/v1/auth/token -H "X-App-Id: app_1b0849c422b4a22f3b8e" -H "Content-Type: application/json" -d '{"email":"darrenhoomessi@gmail.com","password":"UCtest2026!"}' | head -c 300
echo
journalctl -u usercenter-api --no-pager -n 4 --output=cat | tail -2
DSN=$(cat /etc/usercenter/secrets/mysql_dsn)
APP_PW=$(echo "$DSN" | sed 's/^usercenter:\([^@]*\)@.*/\1/')
docker exec usercenter-mysql mysql -h127.0.0.1 -uusercenter -p"$APP_PW" usercenter_prod -e "SELECT u.user_id, u.app_id, u.account_status, c.failed_login_count, c.locked_until FROM users u LEFT JOIN user_password_credentials c ON u.user_id = c.user_id;" 2>/dev/null
