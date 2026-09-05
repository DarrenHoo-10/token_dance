#!/bin/bash
set -u
sed -i '/^USERCENTER_TEST_AUTH_CODE=/d' /etc/usercenter/usercenter.env
systemctl restart usercenter-api && sleep 3
echo "usercenter-api: $(systemctl is-active usercenter-api) $(curl -s http://127.0.0.1:8100/healthz)"
curl -s -X POST http://127.0.0.1:8100/api/v1/auth/token -H "X-App-Id: app_1b0849c422b4a22f3b8e" -H "Content-Type: application/json" -d '{"email":"darrenhoomessi@gmail.com","password":"UCtest2026!"}' -o /dev/null -w "login: %{http_code}\n"
curl -s -o /dev/null -w "public healthz: %{http_code}\n" https://www.nexorai.com.cn/auth/healthz
