import React, { useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";

export const DevicesConfigView: React.FC = () => {
  const {
    devices,
    revokeDevice,
    configBackups,
    createConfigBackup,
    restoreConfigBackup,
    activeLanguage,
  } = useTokenShow();

  const [backupDescription, setBackupDescription] = useState("");
  const [feedback, setFeedback] = useState<string | null>(null);
  const [deviceToRevoke, setDeviceToRevoke] = useState<string | null>(null);

  const handleCreateBackup = () => {
    const backup = createConfigBackup(backupDescription || undefined);
    setBackupDescription("");
    setFeedback(
      activeLanguage === "zh"
        ? `✓ 已成功创建配置快照 ${backup.versionTag}`
        : `✓ Config snapshot ${backup.versionTag} created`
    );
  };

  const handleRestore = (id: string) => {
    const success = restoreConfigBackup(id);
    if (success) {
      setFeedback(
        activeLanguage === "zh"
          ? "✓ 已成功恢复并回滚至选中的配置快照！"
          : "✓ Configuration restored to selected backup successfully!"
      );
    }
  };

  const handleConfirmRevoke = () => {
    if (deviceToRevoke) {
      revokeDevice(deviceToRevoke);
      setDeviceToRevoke(null);
      setFeedback(
        activeLanguage === "zh"
          ? "✓ 该设备已被成功撤销，服务端已拒绝接收其新上报批次。"
          : "✓ Device revoked. Server will reject future batches from it."
      );
    }
  };

  return (
    <div className="devices-config-view">
      <header className="page-head">
        <div>
          <p className="eyebrow">{activeLanguage === "zh" ? "硬件终端与配置快照" : "Collector Devices & Config Snapshots"}</p>
          <h1>{activeLanguage === "zh" ? "设备绑定、撤销与配置恢复" : "Device Revocation & Config Rollback"}</h1>
          <p>
            {activeLanguage === "zh"
              ? "管理已绑定的 Collector 终端实例；支持一键撤销设备权限与配置版本回滚"
              : "Manage authenticated devices, revoke Ed25519 keys, and rollback configuration backups"}
          </p>
        </div>
      </header>

      {feedback && (
        <div
          style={{
            padding: "12px 18px",
            background: "var(--lime-soft)",
            border: "1px solid #c9f564",
            color: "var(--lime-dark)",
            borderRadius: "12px",
            marginBottom: "20px",
            fontSize: "13px",
            fontWeight: 700,
          }}
        >
          {feedback}
        </div>
      )}

      {/* Devices Section */}
      <section className="panel" style={{ marginBottom: "24px" }} aria-label="Connected Devices">
        <div className="panel-head">
          <div>
            <h2>{activeLanguage === "zh" ? "已连接 Collector 设备" : "Connected Collector Devices"}</h2>
            <p>{activeLanguage === "zh" ? "已通过 Ed25519 签名绑定的终端采集器" : "Authenticated Ed25519 hardware installations"}</p>
          </div>
          <span className="tag tag-lime">
            {devices.filter((d) => d.status === "ACTIVE").length} {activeLanguage === "zh" ? "台在线" : "active"}
          </span>
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: "12px" }}>
          {devices.map((dev) => (
            <div
              key={dev.id}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                padding: "14px 18px",
                borderRadius: "12px",
                background: dev.status === "REVOKED" ? "#fafafa" : "white",
                border: "1px solid var(--line)",
                opacity: dev.status === "REVOKED" ? 0.6 : 1,
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: "14px" }}>
                <div
                  className="agent-avatar"
                  style={{
                    width: "42px",
                    height: "42px",
                    background: dev.status === "REVOKED" ? "#9aa19a" : "#111512",
                  }}
                >
                  {dev.platform === "windows" ? "WIN" : dev.platform === "macos" ? "MAC" : "LIN"}
                </div>
                <div>
                  <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
                    <strong style={{ fontSize: "14px" }}>{dev.name}</strong>
                    <span
                      className={`tag ${
                        dev.status === "ACTIVE"
                          ? "tag-lime"
                          : dev.status === "REVOKED"
                          ? "tag-danger"
                          : "tag-warning"
                      }`}
                    >
                      {dev.status}
                    </span>
                  </div>
                  <div style={{ fontSize: "12px", color: "var(--muted)", marginTop: "4px" }}>
                    {dev.osVersion} · Collector v{dev.collectorVersion} · {dev.lastSyncAt}
                  </div>
                  <div style={{ fontSize: "11px", color: "var(--muted)", fontFamily: "var(--font-mono)", marginTop: "2px" }}>
                    Key: {dev.keyFingerprint}
                  </div>
                </div>
              </div>

              <div>
                {dev.status === "ACTIVE" && (
                  <button
                    type="button"
                    className="btn btn-danger btn-sm"
                    onClick={() => setDeviceToRevoke(dev.id)}
                  >
                    {activeLanguage === "zh" ? "撤销此设备" : "Revoke Device"}
                  </button>
                )}
                {dev.status === "REVOKED" && (
                  <span style={{ fontSize: "12px", color: "var(--danger)", fontWeight: 700 }}>
                    {activeLanguage === "zh" ? "已注销" : "Revoked"}
                  </span>
                )}
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* Config Restore / Backup Snapshots Section */}
      <section className="panel" aria-label="Configuration Backup & Restore">
        <div className="panel-head">
          <div>
            <h2>{activeLanguage === "zh" ? "配置备份与版本恢复 (Config Restore / Rollback)" : "Config Snapshots & Restore"}</h2>
            <p>
              {activeLanguage === "zh"
                ? "保存当前 Agent 启用状态、指标开关与公开设置；支持一键恢复"
                : "Snapshot current agent & metric toggle state and restore at any time"}
            </p>
          </div>
        </div>

        {/* Create Backup Input */}
        <div style={{ display: "flex", gap: "10px", marginBottom: "20px" }}>
          <input
            type="text"
            className="form-input"
            style={{ flex: 1 }}
            value={backupDescription}
            onChange={(e) => setBackupDescription(e.target.value)}
            placeholder={activeLanguage === "zh" ? "输入备份描述，如：联调前基线配置" : "Backup description..."}
          />
          <button
            type="button"
            className="btn btn-dark"
            onClick={handleCreateBackup}
          >
            {activeLanguage === "zh" ? "保存当前配置快照" : "Create Snapshot"}
          </button>
        </div>

        {/* Backups List */}
        <div style={{ display: "flex", flexDirection: "column", gap: "10px" }}>
          {configBackups.map((b) => (
            <div
              key={b.id}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                padding: "14px 18px",
                borderRadius: "12px",
                background: "var(--soft)",
                border: "1px solid var(--line)",
              }}
            >
              <div>
                <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
                  <strong style={{ fontSize: "14px" }}>{b.versionTag}</strong>
                  <span className="tag">{b.createdAt.slice(0, 10)}</span>
                </div>
                <p style={{ fontSize: "12px", color: "var(--muted)", marginTop: "4px" }}>
                  {b.description}
                </p>
                <div style={{ display: "flex", gap: "6px", marginTop: "6px" }}>
                  <span className="tag" style={{ fontSize: "10px" }}>
                    Agents: {Object.values(b.snapshot.agentToggles).filter(Boolean).length}
                  </span>
                  <span className="tag" style={{ fontSize: "10px" }}>
                    Metrics: {Object.values(b.snapshot.metricToggles).filter(Boolean).length}
                  </span>
                  <span className="tag" style={{ fontSize: "10px" }}>
                    Scope: {b.snapshot.privacy.isPublicLeaderboard ? "Public" : "Private"}
                  </span>
                </div>
              </div>

              <button
                type="button"
                className="btn btn-sm btn-primary"
                onClick={() => handleRestore(b.id)}
              >
                {activeLanguage === "zh" ? "恢复此配置" : "Restore Config"}
              </button>
            </div>
          ))}
        </div>
      </section>

      {/* Revocation Confirmation Dialog */}
      {deviceToRevoke && (
        <div className="modal-overlay" role="dialog" aria-modal="true">
          <div className="modal-card">
            <div className="modal-head">
              <h2 style={{ color: "var(--danger)" }}>
                {activeLanguage === "zh" ? "确认撤销该设备？" : "Revoke Collector Device?"}
              </h2>
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => setDeviceToRevoke(null)}
              >
                ✕
              </button>
            </div>
            <div className="modal-body">
              <p style={{ fontSize: "13px", color: "var(--muted)" }}>
                {activeLanguage === "zh"
                  ? "撤销后，该设备的 Ed25519 签名密钥将永久失效，服务端会立即阻断该设备的所有后续上报批次。"
                  : "Once revoked, this device's Ed25519 key is invalidated and future upload batches are rejected."}
              </p>
            </div>
            <div className="modal-foot">
              <button
                type="button"
                className="btn"
                onClick={() => setDeviceToRevoke(null)}
              >
                {activeLanguage === "zh" ? "取消" : "Cancel"}
              </button>
              <button
                type="button"
                className="btn btn-danger"
                onClick={handleConfirmRevoke}
              >
                {activeLanguage === "zh" ? "确认撤销" : "Confirm Revoke"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
