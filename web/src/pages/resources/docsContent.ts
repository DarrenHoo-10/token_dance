export interface DocSection { id: string; title: string; paragraphs?: string[]; note?: string; rows?: string[][]; columns?: string[]; action?: { to: string; label: string }; }
export interface DocArticle { slug: string; label: string; group: string; title: string; lead: string; sections: DocSection[]; }

export function getArticles(zh: boolean): DocArticle[] {
  return zh ? [
    { slug: 'quickstart', label: '快速开始', group: '开始使用', title: '把你的第一笔用量，接入 TokenDance。', lead: '安装 Windows 桌面客户端，登录同一个账号，让本机用量自动同步到网站。日常使用时，客户端会安静地留在系统托盘中。', sections: [
      { id: 'prepare', title: '开始之前', note: '准备一个 TokenDance 账号，并在 Windows 电脑上使用至少一种受支持的 AI 工具。当前仅支持 Windows x64。' },
      { id: 'install', title: '1. 获取并运行客户端', paragraphs: ['从下载页获取 Windows 版 TokenDance.exe。运行后，点击 Windows 通知区域中的 TokenDance 图标打开用量面板。'], action: { to: '/download', label: '前往客户端下载' } },
      { id: 'login', title: '2. 登录你的账号', paragraphs: ['打开客户端的「设置」，点击「登录」。在浏览器中完成网站登录，再回到客户端确认账号已连接。', '已有网页登录状态时，可以直接完成授权。登录失效或授权未完成时，从客户端设置重新发起登录。'] },
      { id: 'collect', title: '3. 确认正在采集', paragraphs: ['在支持的 AI 工具里完成一次使用，再打开托盘面板查看 Token 用量。在设置中确认需要的采集来源已开启。', '保持采集开启、账号登录和网络连接，客户端会自动同步。收起面板不影响后台运行。'] },
      { id: 'ranking', title: '4. 回到排行榜，找到自己', paragraphs: ['使用同一账号打开 TokenBoard，查看对应周期的排名。榜单按 UTC 统计，本机面板按本地日历日展示；跨时区时，两边的「今日」可能不同。'], note: '排行榜展示头像、昵称、Token 和排名。关闭详细资料页的公开开关，不会隐藏这些榜单信息。', action: { to: '/leaderboard', label: '打开排行榜' } },
    ] },
    { slug: 'install', label: '安装与运行', group: '开始使用', title: '安装与运行', lead: 'TokenDance 是常驻 Windows 托盘的桌面客户端。下载便携版后直接运行，无需为每个 AI 工具分别安装扩展。', sections: [
      { id: 'windows', title: '下载 Windows 版', paragraphs: ['当前支持 Windows x64。下载 TokenDance.exe，放在固定文件夹中运行；也可以下载 ZIP 压缩包，解压后运行其中的程序。', '版本号、文件大小和预览版标记均以下载页为准。需要 WebView2 Runtime；签名状态和其他运行要求以对应版本的发布说明为准。'], action: { to: '/download', label: '下载桌面客户端' } },
      { id: 'tray', title: '找到托盘图标', paragraphs: ['如果未看到图标，展开 Windows 右下角通知区域的隐藏图标。左键点击 TokenDance 打开面板，点击面板外部或按 Escape 可收起。', '设置入口位于面板左下角。暂停、恢复和退出可通过托盘右键菜单操作。'] },
      { id: 'update', title: '更新客户端', paragraphs: ['先从托盘右键菜单退出旧程序，再运行下载的新版本。不要删除应用数据目录。也可以在设置中检查更新或开启自动更新。', '下载页自动读取最新的公开 Windows 发行版本，包括标记为预览版的发行包。'], action: { to: '/docs/releases', label: '查看发布说明' } },
    ] },
    { slug: 'sources', label: '支持的工具', group: '了解 TokenDance', title: '支持的工具', lead: '采集用量与查询套餐额度是不同能力。能显示 Token，不代表同时支持额度、费用或全部历史数据。', sections: [
      { id: 'tools', title: '主要来源', columns: ['工具', '使用说明'], rows: [ ['Codex', '从本地会话记录读取用量；有近期额度记录时展示额度。'], ['Claude Code', '查看采集到的 Token；没有对应能力时不显示为零。'], ['Cursor', '用量与套餐额度独立展示，额度需有效的本机登录状态。'], ['Grok Build', '从本地已完成轮次日志采集用量；周额度为 Grok 产品共享额度。'], ['ZCode', '额度查询支持个人 Coding Plan；其他套餐不据此推算。'], ['Pi / DeepSeek Harness', '支持范围和可用指标以客户端来源状态与实际记录为准。'] ] },
      { id: 'missing', title: '没有看到数据', paragraphs: ['先确认本机有该工具的使用记录，再在设置中检查采集开关和来源状态。额度待更新、费用缺失与采集失败需要分别判断。'] },
    ] },
    { slug: 'sync', label: '采集与同步', group: '了解 TokenDance', title: '采集与同步', lead: '本机面板记录设备上的用量，网站汇总已成功同步的记录。网络、账号和统计时区都会影响两边的展示。', sections: [
      { id: 'flow', title: '数据如何到达榜单', paragraphs: ['本机工具记录 → 客户端采集与隐私过滤 → 登录后自动同步 → 网站汇总。隐藏面板后，后台采集与同步仍会继续。'] },
      { id: 'states', title: '读懂同步状态', columns: ['状态', '你可以做什么'], rows: [ ['等待同步', '保持登录与网络连接，等待自动处理。'], ['网络异常', '确认网络可用；待同步记录保留，客户端自动重试。'], ['登录已失效', '打开设置重新登录，确认账号连接成功。'], ['采集已暂停', '从托盘菜单恢复采集；暂停时同步也会暂停。'], ['同步受阻', '检查设备是否停用、归属是否冲突，再处理对应提示。'] ] },
      { id: 'time', title: '统计时间与历史', paragraphs: ['排行榜按 UTC 划分周期，本机面板使用本地日历日期。「全部时间」指本机仍有记录的全部历史，不保证能恢复升级前已清理的数据。', '不要将未知或不支持的数据当成零，也不要将未同步的本机记录当成网站已收录数据。'] },
    ] },
    { slug: 'privacy', label: '数据与隐私', group: '了解 TokenDance', title: '数据与隐私', lead: '了解什么留在本机、什么会同步，以及其他人能在排行榜上看到什么。', sections: [
      { id: 'data', title: '同步什么', paragraphs: ['客户端将经过隐私过滤的用量记录发送到 TokenDance，用于汇总 Token、关联工具来源和计算排名。只有登录并连接设备后才进行账号同步。'] },
      { id: 'content', title: '不上传什么', paragraphs: ['不上传提示词、模型回复、源代码正文、diff 正文或工具输出；不会将 Agent 登录凭据复制到 TokenDance 网站。'] },
      { id: 'public', title: '谁能看到你的数据', note: '排行榜与详细资料页分别控制。排行榜展示头像、昵称、Token 和排名。关闭详细资料页公开开关，不会将账号从排行榜隐藏。', paragraphs: ['使用前请确认你的昵称和头像适合公开展示。详细资料页的公开范围可在网站账号设置中管理。'] },
      { id: 'quota', title: '额度查询', paragraphs: ['部分工具的额度从本机日志读取，部分工具复用本机有效登录状态向对应官方服务发起只读查询。凭据不上传到 TokenDance 网站。', '查询失败时，额度显示待更新，不作为真实零额度。'] },
    ] },
    { slug: 'faq', label: '常见问题', group: '帮助与更新', title: '常见问题', lead: '先确认账号、采集状态和统计周期，通常就能定位用量显示问题。', sections: [
      { id: 'no-ranking', title: '客户端有数据，排行榜为什么还没有？', paragraphs: ['确认客户端和网站登录同一账号、采集没有暂停，并等待自动同步。检查同步提示；若登录失效需重新登录。网站按 UTC、本机按本地日期统计，请确认所选周期。'] },
      { id: 'background', title: '关闭面板，还会继续采集吗？', paragraphs: ['会。收起面板后后台继续运行；退出应用会停止。暂停采集时，自动同步也会暂停。'] },
      { id: 'quota', title: '为什么有 Token，却看不到额度或费用？', paragraphs: ['Token 采集、额度查询和费用记录是独立能力。未支持、缺少记录或查询待更新时显示为空或待更新，不应解释为零。'] },
      { id: 'offline', title: '断网后，用量会丢失吗？', paragraphs: ['待同步记录会保留在本机队列中，网络恢复后自动重试。不要在等待同步时删除应用数据。'] },
      { id: 'visibility', title: '关闭公开资料，会从榜单消失吗？', paragraphs: ['不会。公开开关只控制详细资料页；排行榜仍展示头像、昵称、Token 和排名。'] },
      { id: 'platform', title: '支持哪些操作系统？', paragraphs: ['当前仅支持 Windows x64。使用一个桌面客户端连接多个受支持的 AI 工具，各来源的采集方式和前置条件可在来源设置中检查。'] },
    ] },
    { slug: 'releases', label: '发布说明', group: '帮助与更新', title: '发布说明', lead: '下载页自动获取最新公开 Windows 版本，包含预览版。下载文件、版本号和发布说明始终来自同一份发行记录。', sections: [
      { id: 'download', title: '获取最新 Windows 版本', paragraphs: ['前往下载页查看当前版本、发布日期、大小和预览版标记。可直接下载 EXE，也可选用同版本 ZIP 压缩包。', '若网络异常或 GitHub 暂时限制请求，可重试或前往 GitHub 发布页查看。不会将旧版本显示为最新版本。'], action: { to: '/download', label: '查看最新 Windows 版本' } },
      { id: 'verify', title: '核对下载文件', paragraphs: ['发行包的 SHA-256 校验文件和完整变更说明均可从下载页打开。校验值用于确认文件一致性，不等同于 Windows 签名。', '更新前退出托盘中的旧程序，保留应用数据。系统要求、签名状态和已知问题以对应发行说明为准。'] },
    ] },
  ] : [
    { slug: 'quickstart', label: 'Quick start', group: 'Getting started', title: 'Bring your first usage record to TokenDance.', lead: 'Install the Windows desktop app and sign in to sync local usage to your account. The app stays quietly in your system tray.', sections: [
      { id: 'prepare', title: 'Before you begin', note: 'Prepare a TokenDance account and use at least one supported AI tool on your Windows computer. Windows x64 is currently the only supported platform.' },
      { id: 'install', title: '1. Get and run the app', paragraphs: ['Download TokenDance.exe for Windows. Run it, then click the TokenDance icon in the notification area to open your usage panel.'], action: { to: '/download', label: 'Download the desktop app' } },
      { id: 'login', title: '2. Sign in to your account', paragraphs: ['Open Settings in the desktop app and choose Sign in. Complete sign-in in your browser, then return to the app and confirm your account is connected.', 'An existing website session can complete authorization directly. If sign-in expires or authorization is incomplete, start again from the desktop settings.'] },
      { id: 'collect', title: '3. Check collection', paragraphs: ['Use a supported AI tool, then check tokens in the tray panel. Enable the sources you want to collect in Settings.', 'Keep collection enabled, stay signed in and connected to the internet for automatic sync. Hiding the panel does not stop background collection.'] },
      { id: 'ranking', title: '4. Find yourself on the board', paragraphs: ['Open TokenBoard with the same account and select the matching period. The board uses UTC while the desktop panel uses local calendar days, so “today” can differ.'], note: 'The leaderboard displays your avatar, name, tokens and rank. Turning off your detailed public profile does not hide these leaderboard fields.', action: { to: '/leaderboard', label: 'Open the leaderboard' } },
    ] },
    { slug: 'install', label: 'Install & run', group: 'Getting started', title: 'Install & run', lead: 'TokenDance runs in the Windows system tray. Run the portable app without installing separate extensions for each AI tool.', sections: [
      { id: 'windows', title: 'Download for Windows', paragraphs: ['Windows x64 is currently supported. Download TokenDance.exe to a permanent folder and run it, or extract the ZIP package and run the executable inside.', 'See the download page for the version, file size and preview status. WebView2 Runtime is required. Refer to the release notes for signing status and other requirements.'], action: { to: '/download', label: 'Download the desktop app' } },
      { id: 'tray', title: 'Find the tray icon', paragraphs: ['Expand the hidden icons in the Windows notification area if needed. Click TokenDance to open the panel. Click outside the panel or press Escape to hide it.', 'Settings is at the bottom left. Pause, resume and quit are available in the tray’s right-click menu.'] },
      { id: 'update', title: 'Update the app', paragraphs: ['Quit the old app from the tray menu before running the new version. Keep the application data folder. You can also check for updates or enable automatic updates in Settings.', 'The download page checks the newest public Windows release, including releases marked as previews.'], action: { to: '/docs/releases', label: 'Read release notes' } },
    ] },
    { slug: 'sources', label: 'Supported tools', group: 'About TokenDance', title: 'Supported tools', lead: 'Token collection and plan quota lookup are separate capabilities. Token usage does not imply support for quotas, costs or all historical records.', sections: [
      { id: 'tools', title: 'Main sources', columns: ['Tool', 'What to expect'], rows: [ ['Codex', 'Reads local session usage; quotas appear when recent quota records exist.'], ['Claude Code', 'Shows collected tokens. Unsupported capabilities are not shown as zero.'], ['Cursor', 'Usage and plan quotas are separate. Quotas require a valid local sign-in.'], ['Grok Build', 'Reads completed local turns. The weekly quota is shared across Grok products.'], ['ZCode', 'Quota lookup supports personal Coding Plans; other plans are not inferred.'], ['Pi / DeepSeek Harness', 'Available metrics depend on the source status and records shown in the app.'] ] },
      { id: 'missing', title: 'Missing data', paragraphs: ['Confirm that the tool has local usage records, then check its source status and collection switch. A stale quota, missing cost and collection failure are different conditions.'] },
    ] },
    { slug: 'sync', label: 'Collection & sync', group: 'About TokenDance', title: 'Collection & sync', lead: 'The desktop shows local device usage. The website aggregates records that have synced successfully. Network, account state and time zones can affect the totals.', sections: [
      { id: 'flow', title: 'From local usage to the board', paragraphs: ['Local tool records → collection and privacy filtering → automatic sync after sign-in → website aggregation. Hiding the panel leaves collection and sync running.'] },
      { id: 'states', title: 'Understand sync status', columns: ['Status', 'What you can do'], rows: [ ['Pending sync', 'Stay signed in and online while the app processes records.'], ['Network error', 'Check your connection. Pending records stay queued and retry automatically.'], ['Session expired', 'Sign in again from Settings and confirm the account is connected.'], ['Collection paused', 'Resume from the tray menu. Pausing collection also pauses sync.'], ['Sync blocked', 'Check whether the device is disabled or assigned to a different account.'] ] },
      { id: 'time', title: 'Time windows and history', paragraphs: ['The leaderboard uses UTC and the desktop uses local calendar days. All time covers history still recorded on this device, including no promise to recover data cleared before an upgrade.', 'Unknown or unsupported values are not zero. Local records that are pending sync are not yet included in website totals.'] },
    ] },
    { slug: 'privacy', label: 'Data & privacy', group: 'About TokenDance', title: 'Data & privacy', lead: 'Understand what stays local, what syncs and what other people can see on the leaderboard.', sections: [
      { id: 'data', title: 'What syncs', paragraphs: ['Privacy-filtered usage records are sent to TokenDance to aggregate tokens, associate tool sources and calculate ranks. Account sync requires sign-in and a connected device.'] },
      { id: 'content', title: 'What is not uploaded', paragraphs: ['Prompts, model responses, source code, diff contents and tool output are not uploaded. Agent sign-in credentials are not copied to the TokenDance website.'] },
      { id: 'public', title: 'Who can see your data', note: 'The leaderboard and detailed profile are separate. Your avatar, name, tokens and rank remain on the leaderboard when your detailed public profile is turned off.', paragraphs: ['Choose a name and avatar suitable for public display. Manage detailed profile visibility in your website account settings.'] },
      { id: 'quota', title: 'Quota lookups', paragraphs: ['Some quotas come from local logs. Others reuse a valid local sign-in to make read-only requests to the tool’s official service. Credentials are not uploaded to the TokenDance website.', 'Failed lookups are marked as needing an update, not as a real zero quota.'] },
    ] },
    { slug: 'faq', label: 'FAQ', group: 'Help & updates', title: 'Frequently asked questions', lead: 'Start by checking your account, collection status and reporting period.', sections: [
      { id: 'no-ranking', title: 'Why is local usage missing from the board?', paragraphs: ['Use the same account on desktop and web, resume collection and wait for automatic sync. Check sync errors and sign in again if needed. The board uses UTC and the desktop uses local dates.'] },
      { id: 'background', title: 'Does hiding the panel stop collection?', paragraphs: ['No. The app continues in the background. Quitting stops it; pausing collection also pauses sync.'] },
      { id: 'quota', title: 'Why are quotas or costs missing when tokens are visible?', paragraphs: ['Tokens, quotas and costs are independent capabilities. Unsupported or missing records stay unavailable, and stale lookups are marked as needing an update rather than zero.'] },
      { id: 'offline', title: 'What happens when I go offline?', paragraphs: ['Pending records remain in the local queue and retry when your connection returns. Keep application data while records are pending.'] },
      { id: 'visibility', title: 'Does a private profile hide me from the leaderboard?', paragraphs: ['No. The switch controls only your detailed profile. Your avatar, name, tokens and rank remain on the leaderboard.'] },
      { id: 'platform', title: 'Which operating systems are supported?', paragraphs: ['Currently Windows x64 only. One desktop app connects multiple supported tools. Check source settings for each tool’s requirements.'] },
    ] },
    { slug: 'releases', label: 'Release notes', group: 'Help & updates', title: 'Release notes', lead: 'The download page checks the newest public Windows release, including previews. The file, version and release notes all come from the same release record.', sections: [
      { id: 'download', title: 'Get the newest Windows release', paragraphs: ['See the download page for the current version, date, size and preview label. Download the EXE directly or choose the ZIP from the same release.', 'If the network fails or GitHub limits requests, retry or visit GitHub releases. An old version will never be presented as the latest.'], action: { to: '/download', label: 'Check the latest Windows version' } },
      { id: 'verify', title: 'Verify your download', paragraphs: ['Open the SHA-256 checksum file and full release notes from the download page. Checksums confirm file consistency; they do not imply Windows code signing.', 'Quit the old app from the tray before updating and keep application data. Refer to the specific release notes for requirements, signing status and known issues.'] },
    ] },
  ];
}
