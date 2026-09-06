# Windows 便携版更新

从 v0.1.12 起支持 Windows x64 的应用内更新。其他平台明确显示暂不支持，不下载 Windows 文件。

## 用户体验

- 原生后台启动 10 秒后检查，之后每 4 小时检查。手动检查在设置中触发。
- 新版存在时，在 TokenDance 文字右上角显示红色 NEW；打开说明不会启动安装。说明以文本呈现，不执行发布说明里的 HTML。
- 自动更新默认开启，仅后台下载。已下载的包在下次启动、创建窗口和采集服务之前安装。关闭自动更新后不自动安装已下载的包。
- “立即更新”下载、校验、替换并重新启动；下载完成时显示“重启并更新”。运行中的后台检查不会强制重启。
- 设置保存在应用数据目录 `updates/preferences.json`，与网站账号无关。两个窗口读取同一份原生状态。下载、安装和检查互斥。

## 发布要求

发布到 `DarrenHoo-10/token_dance` 的 GitHub Releases：

1. 同步更新 `src-tauri/Cargo.toml` 和 `src-tauri/tauri.conf.json` 的版本，提交对应 Cargo.lock。
2. 使用 `npm run build:windows` 构建，上传名为 **TokenDance.exe** 的 Windows x64 二进制文件。ZIP 可同时提供，但更新器使用单独的 exe。
3. 使用 `v0.1.12` 这样的数字版本标签，填写用户可读的发布说明；确认 exe 附件上传完成后发布。
4. GitHub API 必须返回附件 `digest: sha256:...` 和准确 `size`。缺失时客户端拒绝安装。构建输出的 `SHA256SUMS.txt` 可用于人工复核，不替代 API 校验信息。

当前产品版本在 GitHub 标为 prerelease，因此更新器读取最近 100 条发布记录，选择数字语义版本最高的一条；允许 GitHub 的 prerelease 标记，排除草稿和 `-alpha`、`-beta` 等语义版本后缀。不会降级或安装相同版本。最新版本缺附件时显示未就绪，不回退到更旧附件。

## 下载和替换

仅访问固定仓库的公开 HTTPS 发布 API，不使用账号令牌。附件 URL 必须匹配该仓库及对应标签，HTTPS 重定向仅允许 GitHub 的发行附件域名。网络遵循系统代理设置，没有固定代理地址。

元数据最多 2 MiB，包最多 150 MiB，下载有连接和总超时。客户端校验长度、PE 标记和 SHA-256，再保存到独立缓存。安装前重新获取可信发布元数据并重新校验缓存，缓存不可信时不会执行。这里的校验保证与 GitHub 提供的附件一致，并非 Authenticode 发布者签名。

使用 `self-replace` 替换当前 exe，另保留原文件备份，在替换失败且目标缺失时恢复原程序；不修改账号、WAL、采集配置或快捷方式。更新缓存与下载临时文件使用固定的应用数据子目录。目录权限或磁盘空间不足会展示错误，支持重试。

## 验证

```powershell
npm run test:updates
npm run test:usage
npm run build
cargo test --locked --manifest-path src-tauri/Cargo.toml updates::tests
# 只读取公开 GitHub API，不安装或覆盖程序
cargo test --locked --manifest-path src-tauri/Cargo.toml updates::tests::live_release_feed -- --ignored
```

真实跨版本端到端验收需要先发布一个更高版本的 exe；验证“检查 → NEW → 说明 → 立即更新 → 重启版本变化”，以及自动下载后关闭应用、再次启动时安装。不得用伪造的在线版本或把旧文件标记为新版本替代此项验收。
