# 测试服务发布

测试服务从独立 `release` 分支的干净 worktree 构建，发布包内 `build-info.json` 记录分支、完整提交 SHA、UTC 构建时间。生产与桌面正式发布继续使用 main。

| 项目 | 配置 |
| --- | --- |
| 测试入口 | `https://nexorai.com.cn/token-dance-test/` |
| API | `127.0.0.1:8131` |
| 服务 | `token-dance-test-api` / `token-dance-test-worker` |
| 配置 | `/etc/token-dance-test/app.env`、`secrets/` |
| 版本目录 | `/opt/token-dance-test/releases/<version>/` |
| 当前版本 | `/opt/token-dance-test/current` |
| 静态目录 | `/var/www/token-dance-test` → current/web |
| 数据库 | `tokendance_dev`，发布前备份，不执行测试清库 |
| Redis | `redis_dev` / `127.0.0.1:6380` |
| 对象前缀 | `token-dance-test/` |
| Nginx | 独立 `token-dance-test` vhost，复用现有覆盖 apex 域名的 TLS 证书 |

测试和生产使用不同主机名，避免会话 Cookie 相互覆盖。Nginx 为测试 Cookie 设置 Secure、HttpOnly、SameSite=Lax；保持 `__Host-` Cookie 的 Path=/。测试服务启用团队开关；邮件使用 sink，固定测试验证码关闭，不发送真实邮件。

构建前设置 `VITE_BASE_PATH=/token-dance-test/`，执行前端构建和 Linux amd64 的 API、Worker、migrate、checkproviders 构建。上传白名单仅为 `bin/`、`web/`、`deploy/test/`、`build-info.json`，不上传仓库环境文件、密钥或本地账号文档。

首次配置在服务器本地生成独立运行账号、数据库受限账号、密钥文件和 Nginx vhost；现有测试账号的 email lookup、auth subject 和 AEAD 兼容密钥保持原测试库语义。会话与操作密钥使用独立随机值。已有本地 MinIO 对象不自动迁移到云端测试对象前缀。

后续发布由 `activate.py <release>` 执行：校验 release 分支记录，确认目标是 tokendance_dev，备份数据库，运行受控迁移及 schema 检查，切换 current、重启两个测试服务并验证 readiness。仅在检查通过后 reload Nginx。失败时回退 current；数据库恢复需按备份单独处理。

发布后检查 HTTPS readyz、前端深链接、匿名接口保护、实际测试账号登录和团队列表；保存发布记录。测试环境的历史 main 版本限制见团队 CR 报告。
