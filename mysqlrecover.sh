#!/bin/bash
# Recovery: reset MySQL passwords via --skip-grant-tables (data preserved).
set -u
NEW_ROOT="tdroot_$(head -c 18 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 28)"
NEW_APP="ucapp_$(head -c 18 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 28)"

docker rm -f usercenter-mysql 2>/dev/null
# keep ONLY localhost binding during recovery (grant tables are open)
docker run -d --name mysql-recover --restart no \
  -p 127.0.0.1:3308:3306 -v usercenter-mysql-data:/var/lib/mysql \
  mysql:8.0 --skip-grant-tables
sleep 8
for i in $(seq 1 12); do docker exec mysql-recover mysqladmin ping -h localhost --silent 2>/dev/null && break; sleep 3; done

docker exec mysql-recover mysql -uroot << 'EOSQL'
FLUSH PRIVILEGES;
ALTER USER 'root'@'%' IDENTIFIED BY '__NEWROOT__';
CREATE USER IF NOT EXISTS 'usercenter'@'%' IDENTIFIED BY '__NEWAPP__';
ALTER USER 'usercenter'@'%' IDENTIFIED BY '__NEWAPP__';
CREATE USER IF NOT EXISTS 'tokendance'@'%' IDENTIFIED BY '__NEWAPP__';
ALTER USER 'tokendance'@'%' IDENTIFIED BY '__NEWAPP__';
FLUSH PRIVILEGES;
EOSQL
# substitute passwords (avoid exposing in process list via heredoc substitution)
docker exec mysql-recover sh -c "mysql -uroot -e \"
FLUSH PRIVILEGES;
ALTER USER 'root'@'%' IDENTIFIED BY '$NEW_ROOT';
ALTER USER 'usercenter'@'%' IDENTIFIED BY '$NEW_APP';
CREATE USER IF NOT EXISTS 'tokendance'@'%';
ALTER USER 'tokendance'@'%' IDENTIFIED BY '$NEW_APP';
FLUSH PRIVILEGES;\"" 2>&1 | grep -v Warning | head -3
echo "passwords reset"

docker rm -f mysql-recover
# persist credentials server-side for future operations
cat > /etc/usercenter/mysql-creds << EOF
MYSQL_ROOT_PASSWORD=$NEW_ROOT
MYSQL_USER_PASSWORD=$NEW_APP
EOF
chmod 600 /etc/usercenter/mysql-creds && chown root:root /etc/usercenter/mysql-creds
echo "creds saved to /etc/usercenter/mysql-creds (root-only)"

# restart the real container with the new credentials + public port
docker run -d --name usercenter-mysql --restart unless-stopped \
  -e MYSQL_ROOT_PASSWORD="$NEW_ROOT" -e MYSQL_USER_PASSWORD="$NEW_APP" \
  -e MYSQL_USER=usercenter -e MYSQL_PASSWORD="$NEW_APP" \
  -p 3307:3306 -v usercenter-mysql-data:/var/lib/mysql mysql:8.0
sleep 5
for i in $(seq 1 12); do docker exec usercenter-mysql mysqladmin ping -h localhost --silent 2>/dev/null && echo "mysql ready" && break; sleep 3; done

echo "== data check =="
docker exec usercenter-mysql sh -c 'mysql -uusercenter -p"$MYSQL_PASSWORD" -e "SELECT user_id, app_id FROM usercenter.users;"' 2>&1 | grep -v Warning | head -6

echo "== create env databases + grants =="
docker exec usercenter-mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e "
CREATE DATABASE IF NOT EXISTS usercenter_prod CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE DATABASE IF NOT EXISTS usercenter_dev CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE DATABASE IF NOT EXISTS tokendance_prod CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE DATABASE IF NOT EXISTS tokendance_dev CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
GRANT ALL PRIVILEGES ON usercenter_dev.* TO \"usercenter\"@\"%\";
GRANT ALL PRIVILEGES ON usercenter_prod.* TO \"usercenter\"@\"%\";
GRANT ALL PRIVILEGES ON tokendance_dev.* TO \"tokendance\"@\"%\";
GRANT ALL PRIVILEGES ON tokendance_prod.* TO \"tokendance\"@\"%\";
FLUSH PRIVILEGES;"' 2>&1 | grep -v Warning | head -3

echo "== copy usercenter -> usercenter_prod =="
docker exec usercenter-mysql sh -c 'mysqldump -uusercenter -p"$MYSQL_PASSWORD" --databases usercenter' > /tmp/uc_dump.sql 2>/dev/null
echo "dump: $(wc -c < /tmp/uc_dump.sql) bytes"
docker exec -i usercenter-mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" usercenter_prod' < /tmp/uc_dump.sql
docker exec usercenter-mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -N -e "SELECT COUNT(*) FROM usercenter_prod.users;"' 2>/dev/null | head -1

echo "== update usercenter api dsn + restart =="
cat > /etc/usercenter/secrets/mysql_dsn << EOF
usercenter:$NEW_APP@tcp(127.0.0.1:3307)/usercenter_prod?parseTime=true
EOF
chmod 640 /etc/usercenter/secrets/mysql_dsn && chown root:usercenter /etc/usercenter/secrets/mysql_dsn
systemctl restart usercenter-api && sleep 3
echo "usercenter-api: $(systemctl is-active usercenter-api) $(curl -s http://127.0.0.1:8100/healthz)"
echo "NEW_ROOT=$NEW_ROOT"
echo "NEW_APP=$NEW_APP"
