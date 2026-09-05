#!/bin/bash
set -u
echo "== tencent sg / iptables =="
iptables -L INPUT -n 2>/dev/null | head -8
echo "== container port binding now =="
docker port usercenter-mysql 3306
ss -tlnp | grep 3307 | head -2
echo "== local (on-server) connect test =="
mysql -h127.0.0.1 -P3307 -uusercenter -p"$(grep MYSQL_USER_PASSWORD /etc/usercenter/mysql-creds 2>/dev/null | cut -d= -f2 || echo x)" -e "SELECT 1" 2>&1 | head -2
