import { useCallback, useEffect, useRef, useState } from "react";
import { brandLogo } from "./brand";
import { DesktopAccountCard } from "./DesktopAccountCard";
import { getAgentConfigs, getAutostartStatus, getDaemonStatus, getWebsiteUrl, hideWindow, isTauriEnvironment, openWebsite, quitApp, saveWebsiteUrl, setAgentStatus, setAutostart, setGlobalPause } from "./tauri-bridge";
import type { AgentConfig, AutostartInfo, DaemonStatus } from "./tauri-bridge";
import "./styles/settings.css";

function Toggle({ checked, disabled, label, onChange }: { checked: boolean; disabled: boolean; label: string; onChange: (value: boolean) => void }) {
  return <button type="button" className="settings-toggle" role="switch" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)}><span /></button>;
}

export function SettingsPage() {
  const [lang, setLang] = useState<"zh" | "en">(() => localStorage.getItem("tokendance.language") === "en" ? "en" : "zh");
  const [data, setData] = useState<{ agents: AgentConfig[]; status: DaemonStatus; autostart: AutostartInfo } | null>(null);
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [sourcesOpen, setSourcesOpen] = useState(false);
  const [connectionOpen, setConnectionOpen] = useState(false);
  const [website, setWebsite] = useState(getWebsiteUrl);
  const [websiteRevision, setWebsiteRevision] = useState(0);
  const mounted = useRef(false);
  const loading = useRef<Promise<boolean> | null>(null);
  const mutating = useRef(false);
  const zh = lang === "zh";
  const t = (cn: string, en: string) => zh ? cn : en;
  const changeLanguage = (next: "zh" | "en") => { localStorage.setItem("tokendance.language", next); setLang(next); document.documentElement.lang = next === "zh" ? "zh-CN" : "en"; };

  const refresh = useCallback(async (force = false): Promise<boolean> => {
    if (document.hidden || (mutating.current && !force)) return false;
    if (loading.current) return loading.current;
    loading.current = (async () => {
      try {
        const [agents, status, autostart] = await Promise.all([getAgentConfigs(), getDaemonStatus(), getAutostartStatus()]);
        if (mounted.current) { setData({ agents, status, autostart }); setError(false); }
        return true;
      } catch { if (mounted.current) setError(true); return false; }
      finally { loading.current = null; }
    })();
    return loading.current;
  }, []);

  useEffect(() => {
    mounted.current = true;
    const onFocus = () => { setLang(localStorage.getItem("tokendance.language") === "en" ? "en" : "zh"); void refresh(); };
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") void hideWindow().catch(err => setNotice(String(err))); };
    void refresh();
    const timer = window.setInterval(refresh, 5000);
    window.addEventListener("focus", onFocus);
    window.addEventListener("keydown", onKey);
    document.addEventListener("visibilitychange", onFocus);
    return () => { mounted.current = false; clearInterval(timer); window.removeEventListener("focus", onFocus); window.removeEventListener("keydown", onKey); document.removeEventListener("visibilitychange", onFocus); };
  }, [refresh]);

  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(""), 4500);
    return () => clearTimeout(timer);
  }, [notice]);

  const perform = async (action: () => Promise<unknown>, readback = false) => {
    if (mutating.current) return;
    mutating.current = true;
    setBusy(true);
    try {
      await loading.current;
      await action();
      if (readback) setNotice(await refresh(true) ? t("已保存", "Saved") : t("修改已提交，请重试刷新状态。", "Change submitted. Retry to refresh its status."));
    }
    catch (err) { setNotice(String(err)); }
    finally { mutating.current = false; setBusy(false); }
  };
  const goToWebsite = (path: "/settings/profile" | "/settings/privacy" | "/settings/devices") => {
    void perform(() => openWebsite(path));
  };
  const disabled = busy || !data || error;
  const collecting = data?.status.status === "RUNNING" && !data.status.globalPaused;
  const statusLabel = error ? t("连接中断", "Disconnected") : !data ? t("连接中", "Connecting") : data.status.globalPaused ? t("已暂停", "Paused") : collecting ? t("采集中", "Collecting") : t("需要检查", "Needs attention");

  return <div className="settings-page">
    <header className="settings-header" data-tauri-drag-region>
      <div className="settings-brand" data-tauri-drag-region><img src={brandLogo} alt="" draggable={false} data-tauri-drag-region /><strong data-tauri-drag-region>TokenDance</strong><span data-tauri-drag-region>{t("桌面端", "Desktop")}</span></div>
      <div className="usage-controls"><div className="usage-window-controls" role="group" aria-label={t("语言与窗口控制", "Language and window controls")}>
        <button className="usage-language" onClick={() => changeLanguage(zh ? "en" : "zh")} aria-label={t("切换到英文", "Switch to Chinese")}>{zh ? "EN" : "中"}</button>
        <button className="usage-minimize" onClick={() => void perform(hideWindow)} aria-label={t("最小化到托盘", "Minimize to tray")} title={t("最小化到托盘", "Minimize to tray")}><span aria-hidden="true">−</span></button>
        <button className="usage-close" onClick={() => void perform(quitApp)} aria-label={t("退出 TokenDance", "Quit TokenDance")} title={t("退出 TokenDance", "Quit TokenDance")}><span aria-hidden="true">×</span></button>
      </div></div>
    </header>
    <main className="settings-main">
      <div className="settings-intro"><div><h1>{t("桌面设置", "Desktop settings")}</h1><p>{t("仅设置这台设备的采集与运行方式。", "Collection and preferences for this device.")}</p></div><span className={`settings-status ${collecting && !error ? "active" : ""}`}><i />{statusLabel}</span></div>
      {error && <div className="settings-error" role="alert">{t("无法读取本机设置，已保留上次状态。", "Unable to refresh settings. Showing the last known state.")}<button onClick={() => void refresh()}>{t("重试", "Retry")}</button></div>}
      <DesktopAccountCard zh={zh} websiteRevision={websiteRevision} />
      <section className="settings-section" aria-labelledby="preferences-heading">
        <div className="settings-section-heading"><h2 id="preferences-heading">{t("运行偏好", "Preferences")}</h2><span>{t("更改自动保存", "Changes save automatically")}</span></div>
        <div className="settings-sheet">
          <div className="settings-row"><div><h3>{t("开机启动", "Launch at login")}</h3><p>{t("登录电脑后，自动在托盘中运行", "Start quietly in the tray when you sign in")}</p></div><Toggle label={t("开机启动", "Launch at login")} checked={data?.autostart.enabled ?? false} disabled={disabled} onChange={enabled => void perform(async () => { const autostart = await setAutostart(enabled); setData(current => current ? { ...current, autostart } : current); }, true)} /></div>
          <div className="settings-row"><div><h3>{t("采集用量", "Collect usage")}</h3><p>{t("记录本机 Agent 用量，可随时暂停", "Record agent usage on this device. Pause anytime.")}</p></div><Toggle label={t("采集用量", "Collect usage")} checked={data ? !data.status.globalPaused : false} disabled={disabled} onChange={enabled => void perform(() => setGlobalPause(!enabled), true)} /></div>
          <div className="settings-row"><div><h3>{t("界面语言", "App language")}</h3><p>{t("仅影响桌面端的显示语言", "Applies to the desktop app only")}</p></div><div className="settings-languages" role="group" aria-label={t("界面语言", "App language")}><button aria-pressed={zh} onClick={() => changeLanguage("zh")}>中文</button><button aria-pressed={!zh} onClick={() => changeLanguage("en")}>English</button></div></div>
        </div>
      </section>
      <section className="settings-sources">
        <button className="settings-disclosure" aria-expanded={sourcesOpen} aria-controls="settings-source-list" onClick={() => setSourcesOpen(!sourcesOpen)}><div><h2>{t("采集来源", "Collection sources")}</h2><p>{t("选择这台设备上的 Agent", "Choose which agents to collect from")}</p></div><span>{data ? t(`${data.agents.filter(agent => agent.enabled && agent.status !== "UNDETECTED").length} 个已启用来源`, `${data.agents.filter(agent => agent.enabled && agent.status !== "UNDETECTED").length} sources enabled`) : "—"}<b aria-hidden="true">{sourcesOpen ? "−" : "+"}</b></span></button>
        <div id="settings-source-list" hidden={!sourcesOpen} className="settings-source-list">
          {data?.agents.map(agent => <div className="settings-source" key={agent.id}><span className="settings-source-initial" aria-hidden="true">{agent.name.slice(0, 1)}</span><div><h3>{agent.name}</h3><p>{agent.status === "UNDETECTED" ? t("尚未检测到", "Not detected") : !agent.enabled ? t("已关闭", "Disabled") : ["ERROR", "DEGRADED", "NEEDS_PERMISSION"].includes(agent.status) ? t("暂不可用，请检查本机 Agent", "Unavailable. Check this agent on your device.") : agent.status === "CONFIGURING" ? t("连接中", "Connecting") : data.status.globalPaused ? t("已随全局采集暂停", "Collection is paused") : t("已启用", "Enabled")}</p></div><Toggle checked={agent.enabled} disabled={disabled || agent.status === "UNDETECTED"} label={`${agent.name} ${t("采集", "collection")}`} onChange={enabled => void perform(() => setAgentStatus(agent.id, enabled), true)} /></div>)}
          {data?.agents.length === 0 && <p className="settings-no-sources">{t("尚未发现可用的采集来源。", "No collection sources found yet.")}</p>}
        </div>
      </section>
      <section className="settings-web" aria-labelledby="web-settings-heading"><div className="settings-web-heading"><span className="settings-web-mark" aria-hidden="true">↗</span><div><h2 id="web-settings-heading">{t("更多设置，在网站完成", "More settings on the web")}</h2><p>{t("统一管理账号、公开范围和所有设备。", "Manage your account, visibility and devices in one place.")}</p></div></div><div className="settings-web-links">
        <button onClick={() => goToWebsite("/settings/profile")}>{t("账号与资料", "Account & profile")}<span aria-hidden="true">↗</span></button>
        <button onClick={() => goToWebsite("/settings/privacy")}>{t("隐私与公开范围", "Privacy & visibility")}<span aria-hidden="true">↗</span></button>
        <button onClick={() => goToWebsite("/settings/devices")}>{t("设备管理", "Devices")}<span aria-hidden="true">↗</span></button>
      </div></section>
      {connectionOpen && <form className="settings-connection" onSubmit={event => { event.preventDefault(); try { saveWebsiteUrl(website.trim()); setWebsiteRevision(value => value + 1); setConnectionOpen(false); setNotice(website.trim() ? t("网站地址已保存。未登录会先打开登录页，已登录则进入官网首页。", "Website saved. Sign-in opens if needed; otherwise the official homepage.") : t("已使用默认网站地址。未登录去登录页，已登录去官网首页。", "Using the default website. Sign-in if needed, otherwise the official homepage.")); } catch (err) { setNotice(String(err)); } }}><label htmlFor="website-address">{t("TokenDance 网站地址（可留空使用默认）", "TokenDance website URL (optional)")}</label><div><input autoFocus id="website-address" type="url" placeholder="http://127.0.0.1:3000" value={website} onChange={event => setWebsite(event.target.value)} /><button type="submit">{t("保存", "Save")}</button><button type="button" onClick={() => setConnectionOpen(false)}>{t("取消", "Cancel")}</button></div></form>}
    </main>
    <footer className="settings-footer"><span>{!isTauriEnvironment() ? t("界面预览 · 示例状态", "Preview · Sample state") : `TokenDance ${data?.status.collectorVersion ?? ""}`}</span><div><button className="settings-connection-link" onClick={() => { setWebsite(getWebsiteUrl()); setConnectionOpen(!connectionOpen); }}>{t("网站连接", "Website connection")}</button><button className="settings-done" onClick={() => void perform(hideWindow)}>{t("完成", "Done")}</button></div></footer>
    {notice && <div role="status" className="settings-notice">{notice}</div>}
  </div>;
}
