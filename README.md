<p align="center">
  <img src="logo-tokendance-v2.png" alt="TokenDance" width="80" />
</p>

<h1 align="center">TokenDance</h1>

<p align="center">Let Token Dance · 看见你与 AI 一起创造的每一天</p>

<p align="center">
  <a href="https://www.nexorai.com.cn/token-dance/">访问官网</a> ·
  <a href="https://www.nexorai.com.cn/token-dance/download">下载最新版本</a>
</p>

TokenDance 是一款 AI 编程用量统计工具。它把不同 Agent 的 Token 消耗、费用和额度汇总到桌面，让你随时查看用量，也能在网站回顾个人数据和排行榜。

## 用量，一眼看清

- **按周期查看**：今日、近 7 日和全部时间的 Token 用量与费用。
- **看见使用趋势**：每日折线图和年度活动热力图，记录持续创作的轨迹。
- **了解 Agent 构成**：分别查看不同编程工具的用量，知道 Token 花在了哪里。
- **关注订阅额度**：查看已接入工具的额度使用情况与重置时间。

<p align="center">
  <img src="docs/images/desktop-usage.jpg" alt="TokenDance 桌面用量面板：周期统计、用量趋势和 Agent 额度" width="420" />
</p>

## 留在桌面，随手可看

TokenDance 常驻 Windows 托盘，点击图标即可展开用量面板。也可以开启悬浮球，在工作时查看今日 Token 和所选来源的额度。

开机启动、采集暂停、Agent 来源和自动更新都能在设置中管理。

<p align="center">
  <img src="docs/images/desktop-settings.jpg" alt="TokenDance 桌面设置：账号、开机启动、采集开关和悬浮球" width="680" />
</p>

<p align="center"><sub>以上为产品界面预览，数据与状态为示例。</sub></p>

## 本地使用，也能同步到网站

**不登录也可以使用。** 在本机查看已采集的用量与历史统计，离线时继续记录。

登录后自动同步到网站，查看个人用量、趋势、活跃日历、Agent 构成和已记录的 Skill 使用情况。你可以设置昵称与头像，通过开关选择是否公开个人数据页。

**TokenBoard 排行榜**支持按不同时间周期查看排名、总参与人数和相较昨日的变化。注册账号即可参与；个人数据页是否公开，不影响参与排行榜。

## 支持的编程工具

**Codex · Claude Code · Grok Build · ZCode · Cursor · Pi · DeepSeek Harness**

不同工具支持的用量、费用和额度信息有所不同，具体以应用内显示为准。

## 开始使用

1. 前往 [下载页面](https://www.nexorai.com.cn/token-dance/download)，下载 `TokenDance.exe` 或 Windows ZIP 压缩包。
2. 运行程序，在 Windows 右下角托盘打开用量面板。
3. 在设置中选择需要采集的工具；如需网站同步，点击登录或注册。

已有用户可在设置中点击 **检查更新**，或开启自动更新。

## 项目目录

| 目录 | 用途 |
| --- | --- |
| `collector/` | Rust 采集端：桌面应用、本地采集服务、工具适配器和共享库 |
| `collector/apps/desktop/` | Tauri 桌面应用；`src/` 为 React 界面，`src-tauri/` 为 Rust 原生逻辑 |
| `collector/apps/service/` | 本地采集服务、来源发现及运行时集成 |
| `collector/adapters/` | 各 harness 工具的采集策略及测试样本 |
| `collector/crates/` | 采集、协议、隐私处理、持久化、上传和平台能力等共享 Rust 库 |
| `collector/packaging/` | Windows/macOS 安装包构建、签名及打包验证 |
| `collector/schemas/` | 采集端适配器清单和事件的 JSON Schema |
| `server/` | Go 服务端；`cmd/` 为 API/worker 入口，`internal/` 为业务实现，`api/` 为接口说明，`db/` 为数据库迁移，`scripts/` 为 SQL 代码生成等验证脚本 |
| `web/` | React 网站：个人统计、排行榜、账号设置和下载文档页；`src/` 为源码，`public/` 为静态资源，`e2e/` 为端到端测试 |
| `schemas/` | 跨端事件协议、协议样本和桌面发布清单的 JSON Schema |
| `tools/` | 协议代码生成与校验、发布清单管理、安全检查等可复用工具 |
| `scripts/` | 跨模块集成验收脚本 |
| `deploy/` | 云端部署脚本、Nginx 配置、systemd 服务配置及部署说明 |
| `docs/` | 文档入口；过程文档按需求归档，项目事实记录模块现状，spec 维护正式规范 |
| `.github/workflows/` | CI、跨平台打包、发布清单及敏感信息扫描工作流 |
| `.githooks/` | 本地 Git 钩子，包括提交前敏感信息扫描 |
| `build/` | 本地临时构建、验证产物；已被 Git 忽略，可能包含其他工作区，清理前先检查内容 |

根目录 `package.json` 提供跨端协议生成与校验命令；桌面端和网站在各自目录维护前端依赖。`AGENTS.md` 记录协作与部署约束，`SECURITY.md` 记录安全规则，`.gitignore` 和 `.gitattributes` 管理忽略项及文本属性。

构建、运行和测试方法见 [开发指南](docs/development.md)，云端服务部署见 [部署说明](deploy/README.md)。

## 项目文档

[文档目录与维护规则](docs/README.md)：过程文档按需求归档，项目事实按模块记录现状，spec 维护正式行为与契约。

## 开源协议

[MIT License](LICENSE)
