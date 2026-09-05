#!/bin/bash
set -u
DSN='tokendance:ucapp_KRWuK9ZyX7NFaXDPCkbnHJJY@tcp(127.0.0.1:3307)/tokendance_dev?parseTime=true'
docker exec usercenter-mysql mysql -h127.0.0.1 -uroot -p"$(cat /etc/usercenter/mysql-creds | grep MYSQL_ROOT_PASSWORD | cut -d= -f2)" -e "DROP DATABASE IF EXISTS tokendance_dev; CREATE DATABASE tokendance_dev CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci; GRANT ALL PRIVILEGES ON tokendance_dev.* TO 'tokendance'@'%'; FLUSH PRIVILEGES;" 2>/dev/null
echo "db recreated"
TOKENDANCE_MYSQL_DSN="$DSN" /opt/usercenter/tokendance-migrate 2>&1 | tail -2
echo "== tables =="
docker exec usercenter-mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" tokendance_dev -e "SELECT COUNT(*) AS tables_count FROM information_schema.tables WHERE table_schema=\"tokendance_dev\";"' 2>/dev/null | tail -1
