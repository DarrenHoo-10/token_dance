# TokenDance 桌面托盘

启动后常驻 Windows 通知区域，主窗口和设置窗口默认隐藏，不占任务栏。

- 左键点击 TokenDance 图标：在所在显示器工作区右下角打开 480 × 780 逻辑像素的用量面板，小屏幕自动限制在工作区内。内容滚动，状态栏和底部入口固定。
- 点击面板外部、按 Escape 或点击右上角 −：收起面板到托盘，后台采集继续。
- 托盘面板标题栏仅保留语言和 −，退出应用使用托盘右键菜单。界面采用白底、石墨灰文字、蓝灰图表和细边框。
- 左下角「设置」：打开桌面设置窗口；− 或 Escape 收起到托盘，× 退出应用。
- 右下角「网站主页 · 看排名」：打开官网首页（默认 `https://www.nexorai.com.cn/token-dance/`）。未登录会转到登录页；登录成功后本机保存 Session（默认一个月），再次点击直接进入官网首页。设置左下角显示当前网站地址，点击可打开官网。
- 托盘右键菜单：设置、暂停/恢复、退出；不再提供手动同步入口。

面板并排展示今日、近 7 日、全部时间（All time）的本机 Token 和已记录费用；选择周期同步切换 Agent 明细。7 日折线始终展示，包含今天及前六个本地日历日期。年度热力图展示过去 12 个月，未记录的日期与真实零用量分开显示。数据每 3 秒刷新，隐藏时停止前端轮询，后台采集和自动同步继续运行。浏览器预览明确标记为示例数据。

原生 usage-ledger.json 持久化所有已记录日期及去重 ID，All time 不再等于旧版 8 天窗口。旧格式自动兼容，升级前已经清理并确认上传的历史无法从本地恢复；界面的 All time 指本机仍有记录的全部历史。IPC 每次返回最近 366 天以及全部历史累计。费用来自 CostRecorded 事件，按币种分别汇总，以亿分之一货币单位整数持久化；未接入或缺少记录显示 —，不把部分费用当成完整账单，也不根据总 Token 随意套单价。

Codex 额度从 CODEX_HOME（默认用户目录 .codex）的近期 sessions 日志中读取 token_count 的 primary/secondary rate_limits。只扫描有上限的文件尾部、每分钟缓存，不读取登录凭据，也不把对话内容传给前端。额度显示已用比例和重置倒计时，记录超过 30 分钟或已过重置时间显示待更新。其他 Agent 尚无额度来源时显示“套餐额度暂不支持查询”；不影响 Token 采集。

`npm run test:usage` 在 Node 22.6+ 检查七日汇总、跨年日期、缺失历史和零用量。

## 开发与检查

在本目录执行 `npm ci`、`npm run dev`，然后在另一个终端执行 `cargo run --manifest-path src-tauri/Cargo.toml`。浏览器预览为本地 1420 端口，`?view=settings` 可预览设置页。

`npm run build` 检查 TypeScript 并构建前端；`npm test` 检查桌面配置、IPC 对齐并运行 Rust 测试。Windows 发布统一使用 `npm run build:windows`：先构建前端，再使用 `--features custom-protocol` 嵌入到原生程序，输出独立的 `release/TokenDance.exe` 和包含文件哈希的 `release/build-info.json`。桌面快捷方式应指向该发布文件，不再指向可能被其他 Cargo 构建覆盖的 target 目录。未启用 custom-protocol 的 release 构建会明确报错，避免生成依赖开发服务器的程序。

## 精简设置与桌面登录

设置采用网站的浅色、深绿和青柠色，单页显示账号、开机启动和采集开关，语言只通过右上角切换。Agent 来源默认折叠；原运维仪表盘、上传预览、配置快照和数据删除不再作为日常桌面入口。移除“更多设置”说明和快捷入口；左下角保留可点击的网站地址，底部不再显示“网站连接”和“完成”。

账号卡片的“登录”直接打开默认浏览器中的网站登录页。已有网页登录状态时自动完成，无需重复输入密码；未登录时完成网页登录后返回本机。原生端仅监听随机的 `127.0.0.1` 端口，使用随机 state 和 SHA-256 校验的一次性授权码交换独立桌面会话，随后读取 `/api/v1/auth/session` 确认用户；授权码两分钟内有效且只能使用一次，浏览器等待上限五分钟。网站需同时部署 `/desktop-login` 回传页和 `/api/v1/auth/desktop/authorize`、`/api/v1/auth/desktop/exchange` 接口。临时授权码只保存在 API 进程内存，API 重启后重新点击登录即可。退出客户端只撤销桌面会话；按网站 origin 隔离的本机会话恢复及设备签名同步保持有效。

浏览器预览不接受真实登录，只展示交互；原生 HTTP 测试使用本地模拟服务，覆盖登录读回、Cookie/CSRF、会话失效、失败提示和禁止重定向。历史界面截图位于 `docs/ui-prototypes/16-desktop-settings-redesign.png`、`17-desktop-settings-login.png` 和 `18-desktop-settings-signed-in.png`（最后一张为测试用户）。

人工验收：托盘图标展开、弹窗外部点击收起、Escape 收起、左下设置及关闭回托盘、右下默认浏览器跳转；另外检查多显示器与 125%/150% 缩放。Windows 可将新托盘图标放在折叠区，显示位置由系统管理。

## 自动同步

原生后台每 10 秒检查一次，即使托盘面板和设置窗口隐藏也继续运行。每次只上传最多 100 条、128 KiB 的已有隐私过滤记录；使用登录网站的同一 origin，通过 /api/v1/me/device-grants、/v1/installations/register、/v1/telemetry/batches 完成授权、设备注册和签名上传，不使用开发环境的占位 session。

服务器 ACK 必须匹配批次 ID、事件数量和拒收事件集合；只将明确接受或去重的记录写入本机 ACK。请求失败时保留队列，按 20–320 秒退避自动重试。永久拒收、设备停用或设备归属冲突显示同步受阻，不尝试绕过设备限制。退出登录和会话过期停止后续上传；采集暂停时也暂停同步。

验证：本地模拟 HTTP 服务覆盖设备 grant、设备签名请求、确认后清队列、503 保留并自动重试、会话过期禁止上传；纯函数测试覆盖错误 ACK 和部分拒收。真实网站尚需使用已完成资料的真实账号验收。

Grok Build 用量来自本地已完成轮次日志。主列表按所选周期用量降序展示前三个有已知数据的来源，其余收进“其他来源”；今日为 0 时仍可查看历史累计；未接入的其他能力不会被提示为整个用量来源需要配置。

额度查询已接入 Codex 本地限额日志及 ZCode 个人 Coding Plan。ZCode 从本机 `.zcode/v2/config.json` 中读取已启用的智谱或 Z.ai 官方 Coding Plan 配置，使用现有 API Key 向对应官方 HTTPS 额度接口发出只读 GET 请求；不读取或解密 credentials.json，不刷新或创建密钥，不上传凭据到 TokenDance 网站。只接受官方提供商和对应主机，禁止重定向，超时 8 秒，响应最多 256 KiB。

ZCode 每 5 分钟查询 5 小时及 7 日已用比例和重置时间，界面独立加载额度，不阻塞本机用量和同步状态。网络失败保留原读数与原记录时间并标记待更新；登录失效提示回到 ZCode 重新登录。账号或密钥变化、取消启用时清除旧账号额度缓存。仅支持个人 Coding Plan，暂不查询团队项目、Start Plan 余额或月度工具调用额度；不将这些额度混为 Token 限额。

验证：`cargo test --manifest-path src-tauri/Cargo.toml --lib commands::quotas` 覆盖官方主机绑定、响应校验、毫秒重置时间、失败缓存及拒绝重定向。开发者可显式设置 `TOKENDANCE_VERIFY_ZCODE_QUOTA=1` 后运行忽略的 `live_zcode_quota` 测试验证本机已登录账号的只读查询；输出仅包含展示字段，不包含凭据。

窗口首次打开会等待前端首屏数据（或错误页面）、布局及图片准备就绪，再由原生层显示。隐藏状态仍允许一次初始数据读取，避免等待显示与等待数据互相阻塞；之后隐藏时停止轮询。开机自启动继续仅驻留托盘。重复点击托盘不会重复设置相同位置和大小，失焦后短暂延后检查实际焦点，避免处理过时失焦通知导致窗口闪现后消失。

## Grok Build 与 Cursor 额度

Grok Build 复用本机 `.grok/auth.json`（支持 `GROK_HOME`）中官方登录的有效访问令牌，查询 `https://cli-chat-proxy.grok.com/v1/billing?format=credits`，显示 **Grok 各产品共享的周额度**及重置时间。它不是 Build 独占的 Token 额度，也不把按量付费余额当成套餐额度。

Cursor 优先复用 CLI 登录：Windows 的 `%APPDATA%/Cursor/auth.json`、macOS 的 `~/Library/Application Support/Cursor/auth.json`、Linux 的 `$XDG_CONFIG_HOME/Cursor/auth.json`（默认 `~/.config`）。没有 CLI 登录文件时，只读对应 Cursor 目录下 `User/globalStorage/state.vscdb` 的 `cursorAuth/accessToken`。已有 CLI 会话失效时提示重新登录，不悄悄切换到另一个编辑器账号。通过 Cursor 客户端的 `https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage` 与 `GetPlanInfo` 只读 RPC 查询 Auto、API 两个额度池与账单周期；缺少分池数据时展示返回的套餐额度或个人上限，不将未知额度显示为零。

两个来源每 5 分钟刷新，使用 Windows/macOS 系统代理设置；请求仅发送至固定官方 HTTPS 地址，禁止重定向，超时 12 秒，响应上限 256 KiB。只使用访问令牌，不刷新或改写客户端登录，不上传凭据到 TokenDance 网站。切换账号或失效时清除旧额度；网络失败保留原观测时间并标记待更新。排名仍按 Token 用量取前三，其他来源的额度可在展开列表后查看。

这些是客户端所用接口，可能随上游版本变化。产品口径参考 [Grok 官方 FAQ](https://docs.x.ai/grok/faq) 和 [Cursor 用量说明](https://prod.cursor.com/help/models-and-usage/usage-limits)；协议核对参考 [CodexBar Grok 实现](https://github.com/steipete/CodexBar/tree/main/Sources/CodexBarCore/Providers/Grok) 与 [Cursor 实现](https://github.com/steipete/CodexBar/tree/main/Sources/CodexBarCore/Providers/Cursor)，并以本机账号只读查询验证。

开发验证：设置 `TOKENDANCE_VERIFY_CONNECTED_QUOTAS=1` 后运行 `cargo test --manifest-path src-tauri/Cargo.toml --lib live_connected_quotas -- --ignored --nocapture`，仅输出规范化额度字段。常规测试覆盖分池百分比、未知/无限额度、账号变化、访问令牌校验、只读 SQLite、重定向和响应大小限制。
