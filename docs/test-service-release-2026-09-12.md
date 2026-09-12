# 测试服务发布记录 · 2026-09-12

- 入口：https://nexorai.com.cn/token-dance-test/
- 构建分支：`release`，已推送 `origin/release`。
- 完整提交：`5f8cdef2c27d047f2695927fb23aec6142952703`。
- 构建时间：2026-09-12 06:22:47 UTC；切换时间：2026-09-12 06:24:01 UTC。
- 源码：从 `codex/team-init` 的团队功能、CR-015 / CR-016 修复及测试发布配置创建独立 release worktree，干净构建。
- 版本目录：`/opt/token-dance-test/releases/20260912-062247-5f8cdef2`。
- 构建包 SHA-256：`2de049c9a579890f4f4254d46d015830142d58ca286d353f621eb4508f91075a`。
- 备份：`/var/backups/token-dance-test/20260912-062401/database.sql`，仅服务器保存；同目录 `release.json` 保存发布元数据。

## 环境与数据

独立的 `token-dance-test-api` / `token-dance-test-worker` 已启用并运行，API 仅监听 `127.0.0.1:8131`。配置位于 `/etc/token-dance-test`，使用专用系统账号和仅有 `tokendance_dev.*` 权限的数据库账号。

测试库从 7 个迁移推进至 13 个，checksum、dirty 状态和 schema compatibility 检查全部通过；保留现有 11 个账号。迁移 0009 按设计清理可重建的旧日统计和排行榜投影，不删除原始用量和用户/团队数据；新版团队分析读取 v2 上传事实。灰度同步在备份/迁移期间短暂停止，发布后恢复 active。

Redis 使用 `redis_dev`，对象存储使用独立 `token-dance-test/` 前缀。邮件 sink 开启，固定测试验证码关闭。复用原测试库 email lookup、auth subject、AEAD 兼容密钥以保留账号登录，其他操作密钥独立生成。凭据和测试账号密码没有进入 Git 或发布包。

测试服务使用 apex 主机名，生产保持 `www.nexorai.com.cn`。TLS 复用现有覆盖两者的证书。测试 Cookie 保持 Path=/，由 Nginx 设置 Secure / HttpOnly / SameSite=Lax。Nginx 原有其他站点重复 server_name 警告仍存在，配置检测及本次测试/生产入口验证成功。

## 实际验收

- 前端从 release worktree 使用 `/token-dance-test/` base 构建成功，Linux amd64 API / Worker / migrate / checkproviders 构建成功。
- 两个测试服务 active、NRestarts=0；生产 API / Worker 和灰度同步 active。
- 测试及生产 HTTPS readyz 均为 200；测试 `build-info.json` 返回实际 release 分支与上述完整 SHA。
- 浏览器使用本地私有账号文档中的测试账号登录成功；确认会话只属于测试主机名且 Secure、HttpOnly。
- 已验证注册深链接刷新、未登录创建团队的登录保护、团队入口、创建临时团队、成员 API、概览页面及异步分析快照 ready。
- 冒烟临时团队已解散，测试账号恢复未加入团队；没有修改既有团队。
- 修正冒烟脚本误读响应 `status` 为 `state` 后重跑通过；这是本地测试脚本断言修正，未变更发布的应用。
- 浏览器没有运行时异常或 5xx 响应。
- 对象存储写入、HEAD、签名下载和删除通过。通用 checkproviders 随后因测试环境未配置 SMTP 返回非零，符合本环境 sink 配置；不计为 SMTP 验证通过，也没有发送邮件。

截图与本地脚本位于忽略目录 `build/test-release/`。部署规则及后续操作见 [测试发布说明](../deploy/test/README.md)。生产版本和生产数据库未发布或迁移。
