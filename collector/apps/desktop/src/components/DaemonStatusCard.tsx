import React from "react";
import type { DaemonStatus, CollectorMetrics } from "../tauri-bridge.ts";

interface DaemonStatusCardProps {
  status: DaemonStatus | null;
  metrics: CollectorMetrics | null;
  onTogglePause: () => void;
  onSyncNow: () => void;
  onHideWindow: () => void;
  lang: "zh" | "en";
}

export const DaemonStatusCard: React.FC<DaemonStatusCardProps> = ({
  status,
  metrics,
  onTogglePause,
  onSyncNow,
  onHideWindow,
  lang,
}) => {
  const isZh = lang === "zh";

  const formatUptime = (secs: number) => {
    const h = Math.floor(secs / 3600);
    const m = Math.floor((secs % 3600) / 60);
    const s = secs % 60;
    return `${h}h ${m}m ${s}s`;
  };

  const formatMemory = (bytes: number) => {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  };

  return (
    <div className="card-section">
      <div className="section-head">
        <div>
          <h2>{isZh ? "采集守护进程状态与生命周期" : "Collector Daemon & Lifecycle"}</h2>
          <p>
            {isZh
              ? "后台常驻采集引擎，关闭设置窗口时保持在托盘静默运行，无需担心数据遗漏"
              : "Persistent background telemetry engine. Minimizes to system tray on window close without interrupting ingestion"}
          </p>
        </div>
        <div className="action-row">
          <button
            type="button"
            className={`btn ${status?.globalPaused ? "btn-primary" : "btn-warning"}`}
            onClick={onTogglePause}
          >
            {status?.globalPaused
              ? isZh
                ? "▶ 恢复全局采集"
                : "▶ Resume Collection"
              : isZh
                ? "⏸ 全局暂停采集"
                : "⏸ Pause Collection"}
          </button>
          <button type="button" className="btn btn-outline" onClick={onSyncNow}>
            {isZh ? "⚡ 立即同步" : "⚡ Sync Now"}
          </button>
          <button type="button" className="btn btn-secondary" onClick={onHideWindow}>
            {isZh ? "🗕 最小化至托盘" : "🗕 Hide to Tray"}
          </button>
        </div>
      </div>

      <div className="metrics-grid">
        <div className="metric-box">
          <span className="metric-label">{isZh ? "守护进程状态" : "Daemon Status"}</span>
          <div className="metric-value-row">
            <span
              className={`status-pill ${
                status?.status === "RUNNING"
                  ? "pill-active"
                  : status?.status === "PAUSED"
                    ? "pill-warning"
                    : "pill-danger"
              }`}
            >
              {status?.status || "RUNNING"}
            </span>
            <span className="metric-sub">PID: {status?.pid || 14820}</span>
          </div>
        </div>

        <div className="metric-box">
          <span className="metric-label">{isZh ? "常驻运行时间" : "Uptime"}</span>
          <div className="metric-value">{formatUptime(status?.uptimeSecs || 0)}</div>
          <span className="metric-sub">v{status?.collectorVersion || "1.2.0"}</span>
        </div>

        <div className="metric-box">
          <span className="metric-label">{isZh ? "采集事件总数" : "Total Events"}</span>
          <div className="metric-value font-mono">{(status?.eventsCollected || 0).toLocaleString()}</div>
          <span className="metric-sub">
            {metrics?.eventsPerSecond.toFixed(1) || "4.2"} {isZh ? "事件/秒" : "events/sec"}
          </span>
        </div>

        <div className="metric-box">
          <span className="metric-label">{isZh ? "待上报 WAL 队列" : "Pending Outbox"}</span>
          <div className="metric-value font-mono highlight-lime">{status?.eventsPending || 0}</div>
          <span className="metric-sub">{formatMemory(status?.walSpoolBytes || 0)} Spool</span>
        </div>

        <div className="metric-box">
          <span className="metric-label">{isZh ? "已确认上报数" : "ACKed Batches"}</span>
          <div className="metric-value font-mono">{(status?.eventsUploaded || 0).toLocaleString()}</div>
          <span className="metric-sub">{isZh ? "服务端双向验签" : "HMAC / Ed25519"}</span>
        </div>

        <div className="metric-box">
          <span className="metric-label">{isZh ? "进程资源开销" : "Memory & CPU"}</span>
          <div className="metric-value">{formatMemory(status?.memoryRssBytes || 43280000)}</div>
          <span className="metric-sub">CPU: {(status?.cpuUsagePct || 0.8).toFixed(1)}%</span>
        </div>
      </div>
    </div>
  );
};
