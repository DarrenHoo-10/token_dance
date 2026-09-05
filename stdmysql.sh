#!/bin/bash
# Standardize the single MySQL instance to per-environment databases.
# Convention: database name = {service}_{env}; one MySQL user per service
# with rights on its own {service}_dev and {service}_prod databases.
set -u
UCPW=$(docker inspect usercenter-mysql --format '{{range .Config.Env}}{{println .}}{{end}}' | grep '^MYSQL_PASSWORD=' | cut -d= -f2)
UCROOT=$(docker inspect usercenter-mysql --format '{{range .Config.Env}}{{println .}}{{end}}' | grep '^MYSQL_ROOT_PASSWORD=' | cut -d= -f2)
echo "creds loaded: ${#UCPW}/${#UCROOT} chars"

echo "== current container binding =="
docker port usercenter-mysql 3306 2>/dev/null | head -2 || echo none

echo "== recreate with public binding 0.0.0.0:3307 (volume preserved) =="
docker rm -f usercenter-mysql
docker run -d --name usercenter-mysql --restart unless-stopped \
  -e MYSQL_ROOT_PASSWORD="$UCROOT" -e MYSQL_DATABASE=usercenter \
  -e MYSQL_USER=usercenter -e MYSQL_PASSWORD="$UCPW" \
  -p 3307:3306 -v usercenter-mysql-data:/var/lib/mysql mysql:8.0
sleep 5
for i in $(seq 1 24); do docker exec usercenter-mysql mysqladmin ping -h localhost --silent 2>/dev/null && echo "mysql ready" && break; sleep 5; done

echo "== per-env databases =="
# Dump existing usercenter data -> usercenter_prod, then create the env DBs.
docker exec usercenter-mysql sh -c 'mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --databases usercenter' > /tmp/usercenter_dump.sql 2>/dev/null
echo "dump size: $(wc -c < /tmp/usercenter_dump.sql) bytes"

docker exec -i usercenter-mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD"' << 'EOSQL'
CREATE DATABASE IF NOT EXISTS usercenter_prod CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE DATABASE IF NOT EXISTS usercenter_dev CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE DATABASE IF NOT EXISTS tokendance_prod CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE DATABASE IF NOT EXISTS tokendance_dev CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
EOSQL

docker exec -i usercenter-mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" usercenter_prod' < /tmp/usercenter_dump.sql
echo "usercenter data copied to usercenter_prod: $(docker exec usercenter-mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -N -e "SELECT COUNT(*) FROM usercenter_prod.users;"' 2>/dev/null) users"

# Grants: usercenter service user on both envs; tokendance service user likewise.
docker exec usercenter-mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e "
CREATE USER IF NOT EXISTS \"tokendance\"@\"%\" IDENTIFIED BY \"$UCPW\";
GRANT ALL PRIVILEGES ON tokendance_dev.* TO \"tokendance\"@\"%\";
GRANT ALL PRIVILEGES ON tokendance_prod.* TO \"tokendance\"@\"%\";
GRANT ALL PRIVILEGES ON usercenter_dev.* TO \"usercenter\"@\"%\";
GRANT ALL PRIVILEGES ON usercenter_prod.* TO \"usercenter\"@\"%\";
FLUSH PRIVILEGES;"' 2>&1 | grep -v Warning | head -3

echo "== databases now =="
docker exec usercenter-mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e "SHOW DATABASES;"' 2>/dev/null | grep -vE "information_schema|performance_schema|mysql|sys"

# usercenter-prod api DSN: point at usercenter_prod
cat > /etc/usercenter/secrets/mysql_dsn << EOF
usercenter:$UCPW@tcp(127.0.0.1:3307)/usercenter_prod?parseTime=true
EOF
chmod 640 /etc/usercenter/secrets/mysql_dsn && chown root:usercenter /etc/usercenter/secrets/mysql_dsn
systemctl restart usercenter-api && sleep 3
echo "usercenter-api: $(systemctl is-active usercenter-api) $(curl -s http://127.0.0.1:8100/healthz)"
