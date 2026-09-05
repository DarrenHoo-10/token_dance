#!/bin/bash
set -u
UCPW=$(docker inspect usercenter-mysql --format '{{range .Config.Env}}{{println .}}{{end}}' | grep '^MYSQL_PASSWORD=' | cut -d= -f2)
UCROOT=$(docker inspect usercenter-mysql --format '{{range .Config.Env}}{{println .}}{{end}}' | grep '^MYSQL_ROOT_PASSWORD=' | cut -d= -f2)
echo "creds loaded: ${#UCPW} / ${#UCROOT} chars"

if docker inspect usercenter-mysql >/dev/null 2>&1; then
  BIND=$(docker port usercenter-mysql 3306 2>/dev/null | head -1)
  echo "current binding: $BIND"
  if echo "$BIND" | grep -q "127.0.0.1:3307"; then
    echo "recreating container with public binding (volume keeps data)..."
    docker rm -f usercenter-mysql
    docker run -d --name usercenter-mysql --restart unless-stopped \
      -e MYSQL_ROOT_PASSWORD="$UCROOT" -e MYSQL_DATABASE=usercenter \
      -e MYSQL_USER=usercenter -e MYSQL_PASSWORD="$UCPW" \
      -p 3307:3306 -v usercenter-mysql-data:/var/lib/mysql mysql:8.0
  fi
fi
sleep 5
for i in $(seq 1 24); do docker exec usercenter-mysql mysqladmin ping -h localhost --silent 2>/dev/null && echo "mysql ready" && break; sleep 5; done

docker exec usercenter-mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e "
CREATE DATABASE IF NOT EXISTS tokendance CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER IF NOT EXISTS \"tokendance\"@\"%\" IDENTIFIED BY \"$UCPW\";
ALTER USER \"tokendance\"@\"%\" IDENTIFIED BY \"$UCPW\";
GRANT ALL PRIVILEGES ON tokendance.* TO \"tokendance\"@\"%\";
FLUSH PRIVILEGES;"' 2>&1 | grep -v Warning | head -3
echo "db + user ready"

docker exec usercenter-mysql sh -c 'mysql -uusercenter -p"$MYSQL_PASSWORD" -e "SHOW DATABASES;"' 2>&1 | grep -v Warning | tail -5
