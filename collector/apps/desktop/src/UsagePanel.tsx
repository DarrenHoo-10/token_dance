import { useEffect, useRef, useState } from "react";
import { getAgentConfigs, getDaemonStatus, hideWindow, isTauriEnvironment, openSettings, openWebsite, quitApp, toggleGlobalPause } from "./tauri-bridge";
import { syncStatusText } from "./sync-status";
import type { AgentConfig, DaemonStatus } from "./tauri-bridge";
import { weeklyUsage } from "./weekly-usage";
import { WeeklyTrend } from "./components/WeeklyTrend";
import { brandLogo } from "./brand";
import "./styles/usage-panel.css";

const colors = ["#b5ed3c", "#526a49", "#a0b593", "#d2dcbb", "#e9bf78", "#9ba6b2"];

export function UsagePanel() {
  const [lang, setLang] = useState<"zh" | "en">(() => localStorage.getItem("tokendance.language") === "en" ? "en" : "zh");
  const [range, setRange] = useState<"today" | "week">("today");
  const [data, setData] = useState<{ agents: AgentConfig[]; status: DaemonStatus } | null>(null);
  const [error, setError] = useState(false);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const refresh = useRef<() => Promise<void>>(async () => {});
  const zh = lang === "zh";
  const text = (cn: string, en: string) => zh ? cn : en;

  useEffect(() => {
    let disposed = false;
    let loading = false;
    const load = async () => {
      if (loading || document.hidden) return;
      loading = true;
      setLang(localStorage.getItem("tokendance.language") === "en" ? "en" : "zh");
      try {
        const [agents, status] = await Promise.all([getAgentConfigs(), getDaemonStatus()]);
        if (!disposed) { setData({ agents, status }); setError(false); }
      } catch { if (!disposed) setError(true); }
      finally { loading = false; }
    };
    refresh.current = load;
    void load();
    const timer = window.setInterval(load, 3000);
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") void hideWindow(); };
    window.addEventListener("keydown", onKey);
    window.addEventListener("focus", load);
    document.addEventListener("visibilitychange", load);
    return () => {
      disposed = true;
      window.clearInterval(timer);
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("focus", load);
      document.removeEventListener("visibilitychange", load);
    };
  }, []);

  useEffect(() => {
    if (!message) return;
    const timer = window.setTimeout(() => setMessage(""), 4500);
    return () => window.clearTimeout(timer);
  }, [message]);

  const act = async (action: () => Promise<unknown>, success?: string) => {
    if (busy) return;
    setBusy(true);
    try { await action(); if (success) setMessage(success); await refresh.current(); }
    catch (err) { setMessage(String(err)); }
    finally { setBusy(false); }
  };
  const week = weeklyUsage(data?.agents ?? []);
  const tokensFor = (agent: AgentConfig) => range === "week" ? week.totals.get(agent.id) ?? null : agent.accuracy === "unknown" ? null : agent.todayTokens;
  const agents = [...(data?.agents ?? [])].sort((a, b) => (tokensFor(b) ?? -1) - (tokensFor(a) ?? -1));
  const known = agents.filter(agent => tokensFor(agent) !== null);
  const total = known.reduce((sum, agent) => sum + (tokensFor(agent) ?? 0), 0);
  const available = data !== null && known.length > 0;
  const complete = known.length === agents.length;
  const format = (value: number) => new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 2 }).format(value);
  const status = data?.status;
  const paused = status?.globalPaused;
  const healthy = status?.status === "RUNNING" && !error;

  return (
    <div className={`usage-panel ${range === "week" ? "usage-week" : ""}`}>
      <header className="usage-header">
        <div className="usage-brand"><img src={brandLogo} alt="" /><strong>TokenDance</strong></div>
        <div className="usage-controls">
          <div className="usage-window-controls" role="group" aria-label={text("语言与窗口控制", "Language and window controls")}>
            <button className="usage-language" onClick={() => { const next = zh ? "en" : "zh"; setLang(next); localStorage.setItem("tokendance.language", next); }} aria-label={text("切换到英文", "Switch to Chinese")}>{zh ? "EN" : "中"}</button>
            <button className="usage-minimize" disabled={busy} onClick={() => void act(hideWindow)} aria-label={text("最小化到托盘", "Minimize to tray")} title={text("最小化到托盘 · 后台继续采集（Esc）", "Minimize to tray · Keep collecting (Esc)")}><span aria-hidden="true">−</span></button>
            <button className="usage-close" disabled={busy} onClick={() => void act(quitApp)} aria-label={text("退出 TokenDance", "Quit TokenDance")} title={text("退出 TokenDance · 停止后台采集", "Quit TokenDance · Stop collecting")}><span aria-hidden="true">×</span></button>
          </div>
        </div>
      </header>

      <main className="usage-content">
        <div className="usage-heading"><h1>{text("我的用量", "My usage")}</h1><span className="usage-private">{text("本机数据", "This device")}</span></div>
        <div className="usage-period" aria-label={text("统计周期", "Usage period")}>
          <div className="usage-tabs">
            <button aria-pressed={range === "today"} onClick={() => setRange("today")}>{text("今日", "Today")}</button>
            <button aria-pressed={range === "week"} onClick={() => setRange("week")}>{text("7日", "7 days")}</button>
          </div>
          <span>{range === "week" ? `${week.dates[0].slice(5).replace("-", "/")} – ${week.dates[6].slice(5).replace("-", "/")}` : new Date().toLocaleDateString(zh ? "zh-CN" : "en-US", { month: "short", day: "numeric" })}</span>
        </div>

        <section className="usage-total" aria-label={text("Token 总用量", "Total token usage")}>
          <span>{range === "today" ? text("今日 Token 用量", "Tokens used today") : text("近7日 Token 用量", "Tokens over 7 days")}</span>
          <div><strong title={available ? total.toLocaleString() : undefined}>{available ? format(total) : "—"}</strong><span>tokens</span></div>
          <p>{!data ? text("正在读取采集数据…", "Loading collector data…") : !available ? text("用量统计待接入", "Usage statistics not connected yet") : !complete ? text("部分 Agent 暂无用量统计", "Usage is unavailable for some agents") : total === 0 ? text("暂无用量，开始使用 Agent 后在此查看", "No usage yet. Start using an agent to see activity.") : text("记录你的每一次 AI 创作", "Every token, a little more creation.")}</p>
        </section>

        {range === "week" && <WeeklyTrend points={week.points} lang={lang} />}

        <section className="usage-agents" aria-label={text("Agent 用量", "Usage by agent")}>
          <div className="usage-section-title"><h2>{text("Agent 构成", "By agent")}</h2><span>{text("Token 用量", "Tokens")}</span></div>
          {range === "today" && available && total > 0 && <div className="usage-composition" aria-hidden="true">{agents.map((agent, index) => <span key={agent.id} style={{ background: colors[index % colors.length], flexGrow: tokensFor(agent) ?? 0 }} />)}</div>}
          <div className="usage-agent-list">
            {agents.map((agent, index) => <div className="usage-agent" key={agent.id}>
              <span className="usage-agent-dot" style={{ background: colors[index % colors.length] }} />
              <span className="usage-agent-name">{agent.name}</span>
              <span className="usage-agent-state">{agent.status === "UNDETECTED" ? text("未检测到", "Not detected") : !agent.enabled ? text("已关闭", "Disabled") : ["ERROR", "DEGRADED", "NEEDS_PERMISSION"].includes(agent.status) ? text("需处理", "Needs attention") : ""}</span>
              <strong>{tokensFor(agent) === null ? "—" : format(tokensFor(agent)!)}</strong>
            </div>)}
            {data && agents.length === 0 && <p className="usage-empty">{text("尚未检测到 Agent，在设置中连接。", "No agents found. Connect one in Settings.")}</p>}
          </div>
        </section>
      </main>

      <div className="usage-status-bar">
        <div><span className={`usage-status-dot ${healthy ? "running" : ""}`} /><span>{error ? text("连接中断", "Disconnected") : !status ? text("连接中", "Connecting") : paused ? text("已暂停", "Paused") : healthy ? text("采集中", "Collecting") : text("需检查", "Check collector")}</span><span className="usage-queue" role="status" title={status ? text(`${status.eventsPending} 条记录保留在本机，等待服务器确认`, `${status.eventsPending} records stored locally, awaiting server confirmation`) : undefined}>{status && !error ? syncStatusText(status.syncStatus, status.eventsPending, zh) : ""}</span></div>
        <button disabled={!status || busy || error} onClick={() => void act(() => toggleGlobalPause())}>{paused ? text("继续", "Resume") : text("暂停", "Pause")}</button>
      </div>
      <footer className="usage-footer">
        <button onClick={() => void act(openSettings)}><span aria-hidden="true">⚙</span>{text("设置", "Settings")}</button>
        <button className="usage-website" onClick={() => void act(openWebsite)}>{text("网站主页 · 看排名", "Website · Rankings")}<span aria-hidden="true">↗</span></button>
      </footer>
      {!isTauriEnvironment() && <div className="usage-preview-label">{text("浏览器预览 · 示例数据", "Browser preview · Sample data")}</div>}
      {error && <div className="usage-notice" role="alert">{text("无法读取最新数据", "Could not read latest data")} <button onClick={() => void refresh.current()}>{text("重试", "Retry")}</button></div>}
      {message && <div className="usage-notice" role="status">{message}</div>}
    </div>
  );
}
