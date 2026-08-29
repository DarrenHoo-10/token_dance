import React, { useState, useEffect, useCallback } from "react";
import {
  getDaemonStatus,
  toggleGlobalPause,
  getCollectorMetrics,
  getAgentConfigs,
  toggleAgent,
  previewUploadBatch,
  triggerSyncNow,
  getPendingEnvelopes,
  createConfigBackup,
  restoreConfigBackup,
  listConfigBackups,
  listDevices,
  revokeDevice,
  requestDataDeletion,
  purgeLocalCache,
  getAutostartStatus,
  setAutostart,
  hideWindow,
  quitApp,
} from "./tauri-bridge.ts";
import type {
  DaemonStatus,
  CollectorMetrics,
  AgentConfig,
  UploadBatchPreview,
  OutboxEnvelope,
  ConfigBackup,
  CollectorDevice,
  AutostartInfo,
} from "./tauri-bridge.ts";
import { DaemonStatusCard } from "./components/DaemonStatusCard.tsx";
import { AgentsMatrixCard } from "./components/AgentsMatrixCard.tsx";
import { UploadPreviewCard } from "./components/UploadPreviewCard.tsx";
import { AutostartLifecycleCard } from "./components/AutostartLifecycleCard.tsx";
import { DevicesRevokeCard } from "./components/DevicesRevokeCard.tsx";
import { ConfigBackupRestoreCard } from "./components/ConfigBackupRestoreCard.tsx";
import { DataDeletionCard } from "./components/DataDeletionCard.tsx";
import "./styles/desktop.css";

export const App: React.FC = () => {
  const [lang, setLang] = useState<"zh" | "en">("zh");
  const [activeTab, setActiveTab] = useState<
    "daemon" | "agents" | "upload" | "autostart" | "devices" | "backups" | "deletion"
  >("daemon");

  // State
  const [daemonStatus, setDaemonStatus] = useState<DaemonStatus | null>(null);
  const [metrics, setMetrics] = useState<CollectorMetrics | null>(null);
  const [agents, setAgents] = useState<AgentConfig[]>([]);
  const [preview, setPreview] = useState<UploadBatchPreview | null>(null);
  const [outbox, setOutbox] = useState<OutboxEnvelope[]>([]);
  const [backups, setBackups] = useState<ConfigBackup[]>([]);
  const [devices, setDevices] = useState<CollectorDevice[]>([]);
  const [autostartInfo, setAutostartInfo] = useState<AutostartInfo | null>(null);
  const [toastMessage, setToastMessage] = useState<string | null>(null);

  const showToast = (msg: string) => {
    setToastMessage(msg);
    setTimeout(() => setToastMessage(null), 3500);
  };

  const refreshAllState = useCallback(async () => {
    try {
      const [ds, met, ags, prv, ob, bks, devs, auto] = await Promise.all([
        getDaemonStatus(),
        getCollectorMetrics(),
        getAgentConfigs(),
        previewUploadBatch(),
        getPendingEnvelopes(),
        listConfigBackups(),
        listDevices(),
        getAutostartStatus(),
      ]);
      setDaemonStatus(ds);
      setMetrics(met);
      setAgents(ags);
      setPreview(prv);
      setOutbox(ob);
      setBackups(bks);
      setDevices(devs);
      setAutostartInfo(auto);
    } catch (err) {
      console.error("Failed to load desktop state:", err);
    }
  }, []);

  useEffect(() => {
    refreshAllState();
    // Poll status every 3 seconds
    const timer = setInterval(() => {
      getDaemonStatus().then(setDaemonStatus).catch(() => {});
      getCollectorMetrics().then(setMetrics).catch(() => {});
    }, 3000);
    return () => clearInterval(timer);
  }, [refreshAllState]);

  // Actions
  const handleTogglePause = async () => {
    try {
      const paused = await toggleGlobalPause();
      showToast(
        lang === "zh"
          ? paused
            ? "⏸ 全局采集已暂停"
            : "▶ 全局采集已恢复运行"
          : paused
            ? "⏸ Collection paused"
            : "▶ Collection resumed"
      );
      await refreshAllState();
    } catch (e: unknown) {
      showToast(String(e));
    }
  };

  const handleToggleAgent = async (agentId: string) => {
    try {
      const updated = await toggleAgent(agentId);
      showToast(
        lang === "zh"
          ? `${updated.name} 适配器已${updated.enabled ? "启用" : "禁用"}`
          : `${updated.name} adapter ${updated.enabled ? "enabled" : "disabled"}`
      );
      await refreshAllState();
    } catch (e: unknown) {
      showToast(String(e));
    }
  };

  const handleSyncNow = async () => {
    try {
      const ack = await triggerSyncNow();
      showToast(
        lang === "zh"
          ? `✓ 批次上报成功 (已接收 ${ack.accepted} 条事件)`
          : `✓ Batch synced (${ack.accepted} events acked)`
      );
      await refreshAllState();
    } catch (e: unknown) {
      showToast(String(e));
    }
  };

  const handleToggleAutostart = async (enabled: boolean) => {
    try {
      const res = await setAutostart(enabled);
      setAutostartInfo(res);
      showToast(
        lang === "zh"
          ? res.enabled
            ? "✓ 开机自启动已启用"
            : "✓ 开机自启动已关闭"
          : res.enabled
            ? "✓ Autostart enabled"
            : "✓ Autostart disabled"
      );
    } catch (e: unknown) {
      showToast(String(e));
    }
  };

  const handleRevokeDevice = async (deviceId: string) => {
    try {
      await revokeDevice(deviceId);
      showToast(lang === "zh" ? "✓ 终端设备凭证已撤销作废" : "✓ Device revoked");
      await refreshAllState();
    } catch (e: unknown) {
      showToast(String(e));
    }
  };

  const handleCreateBackup = async (description: string) => {
    try {
      const backup = await createConfigBackup(description);
      showToast(
        lang === "zh"
          ? `✓ 配置快照 ${backup.versionTag} 创建成功`
          : `✓ Snapshot ${backup.versionTag} created`
      );
      await refreshAllState();
    } catch (e: unknown) {
      showToast(String(e));
    }
  };

  const handleRestoreBackup = async (backupId: string) => {
    try {
      await restoreConfigBackup(backupId);
      showToast(lang === "zh" ? "✓ 已成功恢复并回滚至选定配置" : "✓ Configuration restored");
      await refreshAllState();
    } catch (e: unknown) {
      showToast(String(e));
    }
  };

  const handlePurgeLocalCache = async () => {
    try {
      const count = await purgeLocalCache();
      showToast(
        lang === "zh"
          ? `✓ 本地缓存已清空 (${count} 条待发记录)`
          : `✓ Local cache purged (${count} records)`
      );
      await refreshAllState();
    } catch (e: unknown) {
      showToast(String(e));
    }
  };

  const handleRequestDataDeletion = async () => {
    try {
      const res = await requestDataDeletion();
      showToast(res.message);
      await refreshAllState();
    } catch (e: unknown) {
      showToast(String(e));
    }
  };

  const isZh = lang === "zh";

  return (
    <div className="desktop-app">
      {/* Header */}
      <header className="desktop-header">
        <div className="brand-section">
          <div className="brand-icon">TD</div>
          <div>
            <span className="brand-title">TokenDance Desktop</span>
            <span className="brand-badge" style={{ marginLeft: "8px" }}>
              {daemonStatus?.status === "RUNNING"
                ? isZh ? "采集中" : "ACTIVE"
                : daemonStatus?.status === "PAUSED"
                  ? isZh ? "已暂停" : "PAUSED"
                  : "STANDBY"}
            </span>
          </div>
        </div>

        <div className="header-actions">
          {toastMessage && <div className="alert-box alert-success">{toastMessage}</div>}

          <div style={{ display: "flex", gap: "4px" }}>
            <button
              type="button"
              className={`lang-btn ${lang === "zh" ? "active" : ""}`}
              onClick={() => setLang("zh")}
            >
              中
            </button>
            <button
              type="button"
              className={`lang-btn ${lang === "en" ? "active" : ""}`}
              onClick={() => setLang("en")}
            >
              EN
            </button>
          </div>

          <button
            type="button"
            className="header-btn"
            onClick={hideWindow}
            title={isZh ? "隐藏窗口至托盘（后台继续采集）" : "Hide to tray"}
          >
            {isZh ? "🗕 托盘后台" : "🗕 Hide"}
          </button>
          <button
            type="button"
            className="header-btn danger-hover"
            onClick={quitApp}
            title={isZh ? "退出守护进程与全部服务" : "Quit Collector"}
          >
            {isZh ? "✕ 退出" : "✕ Quit"}
          </button>
        </div>
      </header>

      {/* Main App Body */}
      <div className="desktop-body">
        {/* Navigation Sidebar */}
        <aside className="desktop-sidebar">
          <button
            type="button"
            className={`nav-item ${activeTab === "daemon" ? "active" : ""}`}
            onClick={() => setActiveTab("daemon")}
          >
            <span className="nav-icon">📊</span>
            <span>{isZh ? "采集状态与控制" : "Daemon & Health"}</span>
          </button>
          <button
            type="button"
            className={`nav-item ${activeTab === "agents" ? "active" : ""}`}
            onClick={() => setActiveTab("agents")}
          >
            <span className="nav-icon">🤖</span>
            <span>{isZh ? "六 Agent 开关" : "Agents Matrix"}</span>
          </button>
          <button
            type="button"
            className={`nav-item ${activeTab === "upload" ? "active" : ""}`}
            onClick={() => setActiveTab("upload")}
          >
            <span className="nav-icon">📦</span>
            <span>{isZh ? "上传预览与脱敏" : "Upload Preview"}</span>
          </button>
          <button
            type="button"
            className={`nav-item ${activeTab === "autostart" ? "active" : ""}`}
            onClick={() => setActiveTab("autostart")}
          >
            <span className="nav-icon">⚙️</span>
            <span>{isZh ? "自启动与常驻" : "Autostart Lifecycle"}</span>
          </button>
          <button
            type="button"
            className={`nav-item ${activeTab === "devices" ? "active" : ""}`}
            onClick={() => setActiveTab("devices")}
          >
            <span className="nav-icon">🔐</span>
            <span>{isZh ? "设备与密钥撤销" : "Devices & Keys"}</span>
          </button>
          <button
            type="button"
            className={`nav-item ${activeTab === "backups" ? "active" : ""}`}
            onClick={() => setActiveTab("backups")}
          >
            <span className="nav-icon">↺</span>
            <span>{isZh ? "配置快照与回滚" : "Config Snapshots"}</span>
          </button>
          <button
            type="button"
            className={`nav-item ${activeTab === "deletion" ? "active" : ""}`}
            onClick={() => setActiveTab("deletion")}
          >
            <span className="nav-icon">🗑</span>
            <span>{isZh ? "数据擦除与注销" : "Data Erasure"}</span>
          </button>
        </aside>

        {/* Dynamic Content View */}
        <main className="desktop-content">
          {activeTab === "daemon" && (
            <DaemonStatusCard
              status={daemonStatus}
              metrics={metrics}
              onTogglePause={handleTogglePause}
              onSyncNow={handleSyncNow}
              onHideWindow={hideWindow}
              lang={lang}
            />
          )}

          {activeTab === "agents" && (
            <AgentsMatrixCard
              agents={agents}
              onToggleAgent={handleToggleAgent}
              lang={lang}
            />
          )}

          {activeTab === "upload" && (
            <UploadPreviewCard
              preview={preview}
              outbox={outbox}
              onRefreshPreview={refreshAllState}
              onSyncNow={handleSyncNow}
              isPaused={daemonStatus?.globalPaused ?? false}
              lang={lang}
            />
          )}

          {activeTab === "autostart" && (
            <AutostartLifecycleCard
              autostartInfo={autostartInfo}
              onToggleAutostart={handleToggleAutostart}
              lang={lang}
            />
          )}

          {activeTab === "devices" && (
            <DevicesRevokeCard
              devices={devices}
              onRevokeDevice={handleRevokeDevice}
              lang={lang}
            />
          )}

          {activeTab === "backups" && (
            <ConfigBackupRestoreCard
              backups={backups}
              onCreateBackup={handleCreateBackup}
              onRestoreBackup={handleRestoreBackup}
              lang={lang}
            />
          )}

          {activeTab === "deletion" && (
            <DataDeletionCard
              onPurgeLocalCache={handlePurgeLocalCache}
              onRequestDataDeletion={handleRequestDataDeletion}
              lang={lang}
            />
          )}
        </main>
      </div>
    </div>
  );
};

export default App;
