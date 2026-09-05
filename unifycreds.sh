#!/bin/bash
set -u
# 1) remove legacy override trigger so ALL environments derive keys from the keyring files
sed -i '/^USERCENTER_HMAC_SECRET_FILE=/d; /^USERCENTER_HMAC_SECRET=/d' /etc/usercenter/usercenter.env
systemctl restart usercenter-api && sleep 3
echo "usercenter-api: $(systemctl is-active usercenter-api)"
curl -s -X POST http://127.0.0.1:8100/api/v1/auth/token -H "X-App-Id: app_1b0849c422b4a22f3b8e" -H "Content-Type: application/json" -d '{"email":"darrenhoomessi@gmail.com","password":"UCtest2026!"}' -o /dev/null -w "login darrenhoomessi: %{http_code}\n"

# 2) unified credentials file (single source of truth, root-only)
OSS_KEY=$(grep '^OSS_API_KEYS=' /etc/oss-service/oss-service.env | cut -d= -f2)
SMTP_CODE=$(grep '^USERCENTER_SMTP_PASSWORD=' /etc/usercenter/usercenter.env | cut -d= -f2)
ADMIN_TOKEN=$(grep '^USERCENTER_ADMIN_TOKEN=' /etc/usercenter/usercenter.env | cut -d= -f2)
mkdir -p /srv/secrets
cat > /srv/secrets/credentials.env << EOF
# ============================================================
# 统一密码/凭据存放处（唯一来源）— 查看: sudo cat /srv/secrets/credentials.env
# 服务器: 43.131.242.110 (ubuntu)
# ============================================================

[server]
SSH_USER=ubuntu
SSH_PASSWORD=Laoyumi0248

[mysql]  # 单一 MySQL 实例: docker 容器 usercenter-mysql, 外部端口 3307
MYSQL_ROOT_PASSWORD=__SEE_MYSQL_CREDS__
MYSQL_APP_USER_PASSWORD=__SEE_MYSQL_CREDS__
EOF
# append real mysql passwords from the existing file
cat /etc/usercenter/mysql-creds >> /dev/null 2>/dev/null && {
  ROOT_PW=$(grep '^MYSQL_ROOT_PASSWORD=' /etc/usercenter/mysql-creds | cut -d= -f2)
  APP_PW=$(grep '^MYSQL_USER_PASSWORD=' /etc/usercenter/mysql-creds | cut -d= -f2)
  sed -i "s|__SEE_MYSQL_CREDS__|$ROOT_PW|; s|__SEE_MYSQL_CREDS__|$APP_PW|" /srv/secrets/credentials.env
}
cat >> /srv/secrets/credentials.env << EOF

[usercenter-api]  # https://www.nexorai.com.cn/auth
ADMIN_TOKEN=$ADMIN_TOKEN
FIRST_APP_ID=app_1b0849c422b4a22f3b8e
SMTP_AUTH_CODE=$SMTP_CODE

[oss-service]  # https://www.nexorai.com.cn/oss
API_KEY=$OSS_KEY
EOF
chmod 600 /srv/secrets/credentials.env && chown root:root /srv/secrets/credentials.env
rm -f /etc/usercenter/mysql-creds
echo "unified credentials file: /srv/secrets/credentials.env (600 root)"
