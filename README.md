# TokenDance

记录 AI 编程工具的 Token 用量，在 Windows 托盘查看统计，在网站查看个人数据和排行榜。

**官网：<https://www.nexorai.com.cn/token-dance/>**

## 下载使用

普通用户不需要编译，也不需要安装 Node.js、Rust 或 Go。

| 下载 | 说明 |
| --- | --- |
| [TokenDance.exe](https://github.com/DarrenHoo-10/token_dance/releases/download/v0.1.3/TokenDance.exe) | Windows x64 便携版，下载后双击运行 |
| [Windows ZIP 压缩包](https://github.com/DarrenHoo-10/token_dance/releases/download/v0.1.3/TokenDance-windows-x64.zip) | 包含程序、使用说明和构建信息；解压后运行 |
| [版本说明与 SHA-256 校验文件](https://github.com/DarrenHoo-10/token_dance/releases/tag/v0.1.3) | 查看版本详情与下载校验值 |

1. 将程序放在固定目录，双击 `TokenDance.exe`。
2. 在 Windows 右下角托盘点击 TokenDance 图标。看不到时展开隐藏图标区域。
3. 在设置页面登录账号，登录后自动同步用量；注册会跳转到网站。
4. 点击“网站主页 · 看排名”进入官网。

更新时先退出托盘中的旧版本，再启动新版。前端页面已经打包进 exe，运行时无需启动本地开发服务器。程序使用 Microsoft Edge WebView2 Runtime；系统提示缺失时可从 [Microsoft 官方页面](https://developer.microsoft.com/microsoft-edge/webview2/) 安装。

当前 Windows 预览版尚未进行 Authenticode 代码签名，系统可能显示发布者未验证提示。macOS 的预编译下载会在可用后提供。

## 功能

- 托盘用量面板：今日、近 7 日、All Time，以及近 7 日趋势。
- 展示 Agent 用量构成、已有费用记录、可获取的订阅额度和年度活跃度。
- 桌面设置提供登录、开机启动、采集开关和采集来源选择。
- 登录后自动同步，界面显示采集与同步状态。
- 网站提供个人统计、公开排行榜、账号和设备管理。

各工具能够提供的数据不同，缺失的额度、费用或历史数据不会当作真实统计值展示。

同步状态与本机采集相互独立。“同步未完成，稍后自动重试”表示后台会自动补传；“部分记录校验未通过，已保留在本机”表示服务器拒收了部分记录，原始待同步记录仍保存在本机，无需删除数据或重新安装。v0.1.1 修复了部分上传结果的解析；服务端同时兼容 GLM 等模型名称的大写字母和提供商命名空间。

## 项目结构

| 目录 | 内容 |
| --- | --- |
| `collector/apps/desktop` | Tauri 2、Rust、React、TypeScript 桌面客户端 |
| `collector/apps/service` | 本地采集服务 |
| `collector/adapters` | 各 AI 编程工具的用量采集适配器 |
| `collector/crates` | 本地数据处理、队列、上传及平台支持 |
| `web` | React、TypeScript、Vite 网站 |
| `server` | Go API、后台任务和数据库迁移 |
| `deploy` | Nginx、systemd 和云端部署说明 |
| `collector/packaging` | Windows/macOS 构建、签名与发布流程 |

## 开发者：本地构建

以下步骤仅面向开发者。Windows 桌面构建需要 Node.js 22.18+、Rust stable、MSVC C++ 构建工具及 Windows SDK；后端开发需要 Go 1.25+。

### Windows exe

在 PowerShell 中执行：

```powershell
cd collector/apps/desktop
npm ci
npm run build:windows
```

输出为 `collector/apps/desktop/release/TokenDance.exe`，同目录的 `build-info.json` 记录构建时间和校验值。覆盖文件前先退出正在运行的旧版。

构建会使用 `--locked --features custom-protocol`，确保依赖固定、前端嵌入。桌面端是独立的 Cargo workspace：修改相关依赖后，应同时更新它的 `src-tauri/Cargo.lock`，不要删除 `--locked` 来绕过检查。

### 网站开发

```powershell
cd web
npm ci
npm run dev
```

开发网站默认将 API 请求代理到 `http://127.0.0.1:8081`，后端地址可通过 `VITE_API_PROXY_TARGET` 配置。正式网站部署在 `/token-dance/`，构建前设置 `VITE_BASE_PATH=/token-dance/`；详见 [云端部署说明](deploy/README.md)。

### 验证

```powershell
# 桌面前端用量、网站链接和同步状态测试
npm --prefix collector/apps/desktop run test:usage

# 桌面原生测试
cargo test --locked --manifest-path collector/apps/desktop/src-tauri/Cargo.toml

# 跨平台发布配置检查
python collector/packaging/tests/test_release_wiring.py

# 网站测试
npm --prefix web test
```

## CI 与发布

GitHub Actions 在采集端代码变更和 PR 上执行 Windows/macOS 构建检查，普通 CI 产物明确标记为 `unsigned`。面向用户的预编译版本放在 [GitHub Releases](https://github.com/DarrenHoo-10/token_dance/releases)。

需要正式签名时，手动运行 `cross-platform-packaging` 并选择 `sign_release: true`。Windows 签名证书、Apple Developer ID 和 notarization 凭证需提前配置。缺少证书或验证失败会阻止签名产物发布。详见 [打包与签名说明](collector/packaging/README.md)。

配置和密钥保存在本机环境或服务器私有配置目录中，不应提交到仓库或随安装包分发。
