# TokenDance 开发与构建

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

开发网站默认将 API 请求代理到 `http://127.0.0.1:8081`，后端地址可通过 `VITE_API_PROXY_TARGET` 配置。正式网站部署在 `/token-dance/`，构建前设置 `VITE_BASE_PATH=/token-dance/`；详见 [云端部署说明](../deploy/README.md)。

### 验证

```powershell
# 桌面前端用量、网站链接和同步状态测试
npm --prefix collector/apps/desktop run test:usage
npm --prefix collector/apps/desktop run test:updates

# 桌面原生测试
cargo test --locked --manifest-path collector/apps/desktop/src-tauri/Cargo.toml

# 跨平台发布配置检查
python collector/packaging/tests/test_release_wiring.py

# 网站测试
npm --prefix web test
```

## CI 与发布

GitHub Actions 在采集端代码变更和 PR 上执行 Windows/macOS 构建检查，普通 CI 产物明确标记为 `unsigned`。面向用户的预编译版本放在 [GitHub Releases](https://github.com/DarrenHoo-10/token_dance/releases)。

需要正式签名时，手动运行 `cross-platform-packaging` 并选择 `sign_release: true`。Windows 签名证书、Apple Developer ID 和 notarization 凭证需提前配置。缺少证书或验证失败会阻止签名产物发布。详见 [打包与签名说明](../collector/packaging/README.md)。

配置和密钥保存在本机环境或服务器私有配置目录中，不应提交到仓库或随安装包分发。
