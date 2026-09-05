# TokenDance 桌面托盘

启动后常驻 Windows 通知区域，主窗口和设置窗口默认隐藏，不占任务栏。

- 左键点击 TokenDance 图标：在所在显示器工作区右下角打开 420 × 560 逻辑像素的用量面板。
- 按 Escape 或点击右上角 −：收起面板到托盘，后台采集继续。
- 点击右上角 ×：保存状态并退出 TokenDance，停止后台采集。窗口控件有操作提示；最小化悬停为浅绿，退出悬停为红色。
- 左下角「设置」：打开桌面设置窗口；− 或「完成」收起到托盘，× 退出应用。
- 右下角「网站主页 · 看排名」：打开官网首页（默认 `http://127.0.0.1:3000/`）。未登录会转到登录页；登录成功后本机保存 Session（默认一个月），再次点击直接进入官网首页。设置里的网站地址可留空。
- 托盘右键菜单：设置、暂停/恢复、立即同步、退出。

面板展示本机今日/近7日 Token、Agent 构成、采集状态和待同步条数。「7日」包含今天及前六个本地日历日期，展示每日趋势线，节点支持悬停和键盘查看精确值；总量、折线和 Agent 明细使用同一份七日日聚合。数据通过已有 Tauri IPC 读取，每 3 秒刷新，隐藏时停止前端轮询。浏览器预览明确标记为示例数据。当前原生 `get_agents` 尚未计算 Token 聚合，返回 `accuracy: unknown`，且没有 `dailyUsage` 历史数据，面板因此显示待接入，不会用累计值或补零伪造七日趋势。接入时 `dailyUsage` 应包含七个本地日期及每日 Token 值（真实无用量的日期明确返回 0）。

`npm run test:usage` 在 Node 22.6+ 检查七日汇总、跨年日期、缺失历史和零用量。

## 开发与检查

在本目录执行 `npm ci`、`npm run dev`，然后在另一个终端执行 `cargo run --manifest-path src-tauri/Cargo.toml`。浏览器预览为本地 1420 端口，`?view=settings` 可预览设置页。

`npm run build` 检查 TypeScript 并构建前端；`npm test` 检查桌面配置、IPC 对齐并运行 Rust 测试。`cargo build --release --manifest-path src-tauri/Cargo.toml --features tauri/custom-protocol` 生成包含前端产物的 Windows 程序，构建前必须运行 `npm run build`。直接用 Cargo 发布时必须启用此 feature，否则仍会访问开发服务器。

## 精简设置与桌面登录

设置采用网站的浅色、深绿和青柠色，单页显示账号、开机启动、采集开关、界面语言。Agent 来源默认折叠；原运维仪表盘、上传预览、配置快照和数据删除不再作为日常桌面入口。账号资料、公开范围和设备管理打开对应网站页面。

账号卡片可展开邮箱/密码登录，注册与找回密码直接打开网站 `/register` 和 `/forgot-password`。登录通过原生 HTTP 客户端调用现有 `/api/v1/auth/login`，再读取 `/api/v1/auth/session` 确认用户；退出登录调用 `/api/v1/auth/logout`。密码不落盘，Cookie 和 CSRF 仅保留在原生进程内存，按网站 origin 隔离；退出应用后需重新登录。桌面登录和默认浏览器登录是独立会话，桌面登录不会自动绑定采集设备或启用云端同步。

浏览器预览不接受真实登录，只展示交互；原生 HTTP 测试使用本地模拟服务，覆盖登录读回、Cookie/CSRF、会话失效、失败提示和禁止重定向。截图位于 `docs/ui-prototypes/16-desktop-settings-redesign.png`、`17-desktop-settings-login.png` 和 `18-desktop-settings-signed-in.png`（最后一张为测试用户）。

人工验收：托盘图标展开、弹窗外部点击收起、Escape 收起、左下设置及关闭回托盘、右下默认浏览器跳转；另外检查多显示器与 125%/150% 缩放。Windows 可将新托盘图标放在折叠区，显示位置由系统管理。
