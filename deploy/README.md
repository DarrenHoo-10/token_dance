# TokenDance 云端部署

正式入口：**https://www.nexorai.com.cn/token-dance/**

部署目标是现有 Tencent Seoul 01 服务器。凭证由本机用户环境变量读取；不得放进代码、构建包或日志。

**仅允许部署 `main` 分支。** 改动先合入 `origin/main`，再从干净的 `main` 工作区构建；构建前拉取远端并确认 `HEAD` 与 `origin/main` 一致。禁止直接部署功能分支。发布记录必须注明分支 `main` 和完整提交 SHA。此规则同样适用于开发环境部署及桌面端发布。

## 运行结构

| 项目 | 位置 / 配置 |
| --- | --- |
| 版本目录 | `/opt/token-dance/releases/<UTC timestamp>/` |
| 当前版本 | `/opt/token-dance/current`，指向版本目录 |
| 静态文件 | `/var/www/token-dance` → `/opt/token-dance/current/web` |
| API | `token-dance-api.service`，仅监听 `127.0.0.1:8130` |
| 后台任务 | `token-dance-worker.service` |
| MySQL | 现有 `usercenter-mysql` 容器，主机端口 `3307` 直连，库 `tokendance_prod` |
| 数据库账号 | `tokendance_app`，仅授权 `tokendance_prod.*` |
| Redis 生产 | `tokendance-redis` 容器，仅本机 `127.0.0.1:6379`，对应库 `tokendance_prod` |
| Redis 测试 | `redis_dev` 容器，主机端口 `6380`，经 `www.nexorai.com.cn:6380` 直连，对应库 `tokendance_dev` |
| Redis 连接 | 生产读 `/etc/token-dance/secrets/redis_url`；测试/开发读 `redis_dev_url` 或设置 `TOKENDANCE_REDIS_URL`，不走 SSH 隧道、不写公网 IP |
| 环境配置 | `/etc/token-dance/app.env` |
| 独立密钥 | `/etc/token-dance/secrets/`，root:tokendance，目录 0750、文件 0640 |
| 对象存储 | S3 兼容 OSS，所有对象使用 `token-dance/` 前缀 |
| Nginx | `/etc/nginx/snippets/token-dance.conf`，由现有 `www.nexorai.com.cn` HTTPS server 引入 |
| 发布前备份 | `/var/backups/token-dance/<UTC timestamp>/` |

生产配置启用 SMTP、MySQL、S3 和九组独立加密密钥。禁止测试验证码、内存存储和邮件 sink。`__Host-tokendance_session` 保持 Secure、HttpOnly 和 Path=/；不得通过 Nginx 改写其 Cookie Path。

## 构建

Node、npm、Go 需在 PATH 中。在项目根目录的 PowerShell 执行：

```powershell
$env:VITE_BASE_PATH = '/token-dance/'
Push-Location web
npm ci
npm run build
Pop-Location

$env:CGO_ENABLED = '0'
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
Push-Location server
foreach ($app in @('api', 'worker', 'migrate', 'checkproviders')) {
    go build -trimpath -ldflags='-s -w' -o "../build/cloud-release/bin/token-dance-$app" "./cmd/$app"
    if ($LASTEXITCODE -ne 0) { throw "Build failed: $app" }
}
Pop-Location
```

将 `web/dist/client/` 内容放入版本目录的 `web/`，四个二进制放入 `bin/`，本目录放入 `deploy/`。版本包仅包含这些文件，不包含 `.env.local`、数据库或密钥。

上传到新的 `/opt/token-dance/releases/<timestamp>/`，赋予 `bin/*` 0755 权限。后续发布直接复用 `/etc/token-dance` 中的正式配置。

首次配置可将 provider JSON 经 SSH 标准输入传给 `sudo python3 <release>/deploy/provision.py`。JSON 字段为 `TOKENDANCE_EMAIL_PROVIDER`、`TOKENDANCE_SMTP_HOST/PORT/TLS_MODE/USERNAME/PASSWORD/FROM` 和 `TOKENDANCE_OBJECT_ENDPOINT/REGION/BUCKET/ACCESS_KEY/SECRET_KEY`。已有 `app.env` 时不会重新生成密钥。已有环境补 Redis 时执行 `sudo python3 <release>/deploy/provision_redis.py`：创建本机 `tokendance-redis` 和测试实例 `redis_dev`。生产 URL 写入 `/etc/token-dance/secrets/redis_url`；测试 URL 写入 `redis_dev_url`，本机开发经 `www.nexorai.com.cn:6380` 直连。

## 验证和切换

在服务器执行（将 `<release>` 替换为已上传的绝对目录）：

```bash
sudo systemd-run --quiet --wait --pipe --collect --uid=tokendance --gid=tokendance \
  -p EnvironmentFile=/etc/token-dance/app.env <release>/bin/token-dance-checkproviders
sudo python3 <release>/deploy/activate.py <release>
sudo systemctl is-active token-dance-api token-dance-worker
curl --fail https://www.nexorai.com.cn/token-dance/readyz
```

`checkproviders` 检查对象写入、HEAD、签名下载、删除以及 SMTP TLS/认证，**不会发送邮件**。`activate.py` 先备份数据库和 Nginx，再执行迁移、检查 Nginx、切换版本，待 readiness 通过后重载 Nginx。

本机浏览器冒烟测试：

```powershell
Push-Location web
node e2e/deployment-smoke.cjs
Pop-Location
```

默认用 Edge 无头浏览器；可通过 `TOKENDANCE_E2E_CHROME` 指定浏览器路径，通过 `TOKENDANCE_E2E_BASE_URL` 指定其他环境。测试覆盖子路径跳转、登录表单、Logo、注册深链接刷新、All Time 排行榜和请求错误。

## 回滚

保留上一版本目录。将 `/opt/token-dance/current` 原子切换回旧目录后，重启两个服务；若 Nginx 有变更，从该次发布备份恢复配置，`nginx -t` 通过后 reload。数据库迁移不能仅靠回退二进制撤销：需先确认旧版本兼容性，必要时另行审核数据库备份恢复。备份保存在 Web 根目录之外。

首次上线验证（2026-09-05）：生产迁移成功，两个服务 active，HTTPS readiness 200，网页浏览器冒烟通过，对象存储和 SMTP 认证通过；未向用户发送测试邮件。

## 桌面安装包和更新清单

网站部署与桌面发版分开。首次部署需配置独立的公共清单路径，后续发版更新 OSS 包和清单即可，不修改前端版本常量。详见 [桌面版本发布](../docs/desktop-release-publishing.md)。
