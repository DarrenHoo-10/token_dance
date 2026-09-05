#!/bin/bash
set -u
NEW_ROOT="tdroot_zhlG3u0TUDv2hP4ymyODtJ6m"

echo "== import dump (visible output) =="
docker exec -i usercenter-mysql mysql -h127.0.0.1 -uroot -p"$NEW_ROOT" usercenter_prod < /tmp/uc_dump.sql 2>&1 | grep -v Warning | head -5
echo "import exit: $?"

echo "== tables + users =="
docker exec usercenter-mysql mysql -h127.0.0.1 -uroot -p"$NEW_ROOT" usercenter_prod -e "SHOW TABLES; SELECT user_id, app_id FROM users;" 2>&1 | grep -v Warning | head -10

echo "== clean env file =="
sed -i '/^USERCENTER_ENVIRONMENT=/d' /etc/usercenter/usercenter.env
echo "USERCENTER_ENVIRONMENT=production" >> /etc/usercenter/usercenter.env
systemctl restart usercenter-api && sleep 3
echo "usercenter-api: $(systemctl is-active usercenter-api) $(curl -s http://127.0.0.1:8100/healthz)"
