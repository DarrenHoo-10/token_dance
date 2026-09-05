#!/bin/bash
set -u
NEW_ROOT="tdroot_zhlG3u0TUDv2hP4ymyODtJ6m"

echo "== dump single database (no USE statement) =="
docker exec usercenter-mysql mysqldump -h127.0.0.1 -uroot -p"$NEW_ROOT" usercenter > /tmp/uc_dump2.sql 2>/dev/null
echo "dump2 size: $(wc -c < /tmp/uc_dump2.sql) bytes, USE statements: $(grep -c 'USE ' /tmp/uc_dump2.sql || true)"

echo "== import into usercenter_prod =="
docker exec -i usercenter-mysql mysql -h127.0.0.1 -uroot -p"$NEW_ROOT" usercenter_prod < /tmp/uc_dump2.sql 2>&1 | grep -v Warning | head -5

echo "== verify =="
docker exec usercenter-mysql mysql -h127.0.0.1 -uroot -p"$NEW_ROOT" usercenter_prod -e "SELECT COUNT(*) AS tables_created FROM information_schema.tables WHERE table_schema='usercenter_prod'; SELECT user_id, app_id FROM users;" 2>&1 | grep -v Warning | head -8

systemctl restart usercenter-api && sleep 3
echo "usercenter-api: $(systemctl is-active usercenter-api) $(curl -s http://127.0.0.1:8100/healthz)"

echo "== login check (existing account) =="
curl -s -X POST http://127.0.0.1:8100/api/v1/auth/token -H "X-App-Id: app_1b0849c422b4a22f3b8e" -H "Content-Type: application/json" -d '{"email":"darrenhoomessi@gmail.com","password":"UCtest2026!"}' -o /dev/null -w "login: %{http_code}\n"
