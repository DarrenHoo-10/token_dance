# 团队分支本地运行记录

日期：2026-09-06。

本次按用户明确要求，将 main 合入 codex/team-init 后在本地运行团队版本。合入的 main 为 `5fb84e7427c8b5ca2ca8b0b7621c933d08eb78b4`；没有将团队功能合入 main，也没有推送远端或发布生产。

## 入口与依赖

- 页面：http://localhost:3000
- API：http://127.0.0.1:8083
- 就绪检查：http://localhost:3000/readyz
- 数据库：**云端 tokendance_dev**；已核对服务器实例，7 个迁移及 checksum 检查通过。本次没有执行云端迁移或重置数据。
- Redis：同一云端服务器的 redis_dev。
- 本机到云端的 MySQL 3307 / Redis 6380 直连超时，本次用独立 SSH 转发接入，分别监听本机 13307 / 16380。转发具备重连和 TCP_NODELAY，未替换其他任务的转发。
- API 和 Worker 共享本机 MinIO 对象存储，容器 tokendance-team-objects，仅监听 127.0.0.1:19000；独立 volume 保存本地导出和上传对象。云端测试库里既有对象可能来自其他存储，不能据此保证其历史头像或旧导出可读。
- 邮件使用本地 sink；开发验证码 123456，不向真实收件人发送邮件。
- 所有团队开关已启用。
- 准备过程中创建的空本地数据库和专用账户已移除，本地服务仅使用云端测试库。

## 启动与日志

本机运行目录：`C:/Users/Administrator/AppData/Local/TokenDance/local-teams`。

```powershell
& 'C:/Users/Administrator/AppData/Local/TokenDance/local-teams/start.ps1'
```

脚本可重复执行，会复用本任务已运行的 API、Worker、页面和转发进程。先启动 Docker Desktop。SSH 凭据从 Windows 用户级环境变量读取；数据库、对象存储配置保存在运行目录的 server-env.json，禁止提交或分享该文件。processes.json 记录进程，api.err.log / worker.err.log / cloud-forward.err.log 保存日志。

发布构建 SHA 记录在运行目录 release.json。静态产物、二进制和启动脚本均在仓库外。不要将仅部署到本机的服务误认为云端服务已更新。

## 验证结果

- 合并的 5 处冲突已解决，保留团队能力、排行榜缓存及 0001–0007 迁移。
- Go 全量测试通过；MySQL 自动化集成用例未配置专用测试 DSN，未对共享开发库运行重置型测试。
- 前端 typecheck、22 个文件 / 115 项测试及构建通过。
- 已连接云端实例，对现有库运行 migrate -check，通过。
- 3 个并发 readyz 请求返回 200。
- 本地浏览器验证登录、注册深链接刷新、未登录团队创建页保护、All Time 排行榜请求及运行时异常检查通过。
- 原有 deployment-smoke.cjs 使用英文 All Time 定位，但 main 已将中文界面标签改为“全部时间”；本次仓库外 smoke.cjs 兼容两个标签。既有测试库的一张历史头像返回 404，未计为团队页面故障；未宣称所有历史媒体已迁移。
- [CR 报告](tokendance-teams-cr-report.md) 的 CR-011–CR-014 仍待修复，CR-010 的数据库回归测试仍待复验。本次部署没有关闭这些问题。

