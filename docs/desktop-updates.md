# Windows 便携版更新

从 v0.1.12 起支持 Windows x64 的应用内更新。其他平台明确显示暂不支持，不下载 Windows 文件。

## 用户体验

- 原生后台启动 10 秒后检查，之后每 4 小时检查。手动检查在设置中触发。
- 新版存在时，在 TokenDance 文字右上角显示红色 NEW；打开说明不会启动安装。说明以文本呈现，不执行发布说明里的 HTML。
- 自动更新默认开启，仅后台下载。已下载的包在下次启动、创建窗口和采集服务之前安装。关闭自动更新后不自动安装已下载的包。
- “立即更新”下载、校验、替换并重新启动；下载完成时显示“重启并更新”。运行中的后台检查不会强制重启。
- 设置保存在应用数据目录 `updates/preferences.json`，与网站账号无关。两个窗口读取同一份原生状态。下载、安装和检查互斥。

## 发布要求

发布流程见 [桌面版本发布](desktop-release-publishing.md)。下载页与客户端读取同一份清单：

`https://www.nexorai.com.cn/token-dance/releases/stable.json`

1. 从干净的 `main` 构建，Rust 包、Cargo.lock、Tauri 配置中的版本保持一致。
2. 将 EXE 和可选 ZIP 上传到 OSS/CDN 的独立版本目录，保留可信的构建来源记录。
3. 运行发布工具核对本地包、构建信息、远端完整文件；通过后原子更新清单。
4. 清单中的 `version` 使用三段数字，不带 `v`、`-beta` 或构建后缀。`prerelease` 只控制网页预览标记；当前客户端接受数字版本，不分通道。

清单包含平台、版本、发布日期、说明和每个安装包的永久 HTTPS 地址、原始 SHA-256、准确字节数。客户端选取最高 Windows x64 数字版本，不降级、不安装相同版本；最新记录不完整则报错，不回退到旧记录。

## 下载和替换

不再读取 GitHub Releases 或使用 GitHub 令牌。元数据来自固定的第一方 HTTPS 地址；安装包地址来自这份可信清单。只接受无凭据、无查询参数的永久 HTTPS 包地址，不跟随重定向。网络遵循系统代理设置。

元数据最多 2 MiB，包最多 150 MiB，下载有连接和总超时。客户端校验长度、PE 标记和 SHA-256，再保存到独立缓存。安装前重新获取可信清单并重新校验缓存，缓存不可信时不会执行。校验用于确认与可信清单记录一致，并非 Authenticode 发布者签名。

使用 `self-replace` 替换当前 exe，另保留原文件备份，在替换失败且目标缺失时恢复原程序；不修改账号、WAL、采集配置或快捷方式。更新缓存与下载临时文件使用固定的应用数据子目录。目录权限或磁盘空间不足会展示错误，支持重试。

## 验证

```powershell
npm run test:updates
npm run test:usage
npm run build
cargo test --locked --manifest-path src-tauri/Cargo.toml updates::tests
# 清单上线后只读检查，不安装或覆盖程序
cargo test --locked --manifest-path src-tauri/Cargo.toml updates::tests::live_release_feed -- --ignored
```

真实跨版本端到端验收需要先发布一个更高版本的 exe；验证“检查 → NEW → 说明 → 立即更新 → 重启版本变化”，以及自动下载后关闭应用、再次启动时安装。不得用伪造的在线版本或把旧文件标记为新版本替代此项验收。

首次从 GitHub 更新源切换时，旧客户端需要从下载页手动安装一次新版本；之后由自有清单提供更新。
