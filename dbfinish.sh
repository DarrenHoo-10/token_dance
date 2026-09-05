#!/bin/bash
# Finish env-DB setup: fix root@localhost password, create env DBs, populate usercenter_prod.
set -u
NEW_ROOT="tdroot_zhlG3u0TUDv2hP4ymyODtJ6m"
NEW_APP="ucapp_KRWuK9ZyX7NFaXDPCkbnHJJY"

# root via TCP matches root@% (password we set); socket matches root@localhost (old/unknown)
echo "== fix root@localhost via TCP =="
docker exec usercenter-mysql mysql -h127.0.0.1 -uroot -p"$NEW_ROOT" -e "
FLUSH PRIVILEGES;
ALTER USER 'root'@'localhost' IDENTIFIED BY '$NEW_ROOT';
FLUSH PRIVILEGES;" 2>&1 | grep -v Warning | head -3

echo "== create env databases + grants =="
docker exec usercenter-mysql mysql -h127.0.0.1 -uroot -p"$NEW_ROOT" -e "
CREATE DATABASE IF NOT EXISTS usercenter_prod CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE DATABASE IF NOT EXISTS usercenter_dev CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE DATABASE IF NOT EXISTS tokendance_prod CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE DATABASE IF NOT EXISTS tokendance_dev CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
GRANT ALL PRIVILEGES ON usercenter_dev.* TO 'usercenter'@'%';
GRANT ALL PRIVILEGES ON usercenter_prod.* TO 'usercenter'@'%';
GRANT ALL PRIVILEGES ON tokendance_dev.* TO 'tokendance'@'%';
GRANT ALL PRIVILEGES ON tokendance_prod.* TO 'tokendance'@'%';
FLUSH PRIVILEGES;" 2>&1 | grep -v Warning | head -3
echo "databases ready"

echo "== populate usercenter_prod from dump =="
docker exec -i usercenter-mysql mysql -h127.0.0.1 -uroot -p"$NEW_ROOT" usercenter_prod < /tmp/uc_dump.sql 2>&1 | grep -v Warning | head -2
docker exec usercenter-mysql mysql -h127.0.0.1 -uroot -p"$NEW_ROOT" -N -e "SELECT COUNT(*) AS users FROM usercenter_prod.users;" 2>/dev/null

echo "== restart usercenter api =="
systemctl restart usercenter-api && sleep 3
echo "usercenter-api: $(systemctl is-active usercenter-api) $(curl -s http://127.0.0.1:8100/healthz)"

echo "== databases =="
docker exec usercenter-mysql mysql -h127.0.0.1 -uroot -p"$NEW_ROOT" -e "SHOW DATABASES;" 2>/dev/null | grep -vE "information_schema|performance_schema|^mysql|sys$|Database"
