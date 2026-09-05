#!/bin/bash
set -u
TD_PW=$(grep '^MYSQL_USER_PASSWORD=' /etc/usercenter/mysql-creds | cut -d= -f2)
cat >> /srv/secrets/credentials.env << EOF

[tokendance-local-dev]  # 本地 TokenDance API (127.0.0.1:8081/3000)
TOKENDANCE_DEV_MYSQL_DSN=tokendance:$TD_PW@tcp(127.0.0.1:3307)/tokendance_dev?parseTime=true
# 需要先开 SSH 隧道: python D:/ProgrammingProjects/TokenDance/mysql-tunnel.py
TOKENDANCE_TEST_AUTH_CODE=123456
EOF
echo "credentials file updated"
sudo cat /srv/secrets/credentials.env | grep -cE "^\[|="
