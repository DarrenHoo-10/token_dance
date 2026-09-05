import React, { useState } from "react";
import type { ConfigBackup } from "../tauri-bridge.ts";

interface ConfigBackupRestoreCardProps {
  backups: ConfigBackup[];
  onCreateBackup: (desc: string) => void;
  onRestoreBackup: (backupId: string) => void;
  lang: "zh" | "en";
}

export const ConfigBackupRestoreCard: React.FC<ConfigBackupRestoreCardProps> = ({
  backups,
  onCreateBackup,
  onRestoreBackup,
  lang,
}) => {
  const isZh = lang === "zh";
  const [description, setDescription] = useState("");
  const [restoringId, setRestoringId] = useState<string | null>(null);

  const handleCreate = (e: React.FormEvent) => {
    e.preventDefault();
    onCreateBackup(description);
    setDescription("");
  };

  const handleRestore = (id: string) => {
    onRestoreBackup(id);
    setRestoringId(null);
  };

  return (
    <div className="card-section">
      <div className="section-head">
        <div>
          <h2>{isZh ? "配置版本快照与一键回滚恢复" : "Configuration Snapshots & Rollback"}</h2>
          <p>
            {isZh
              ? "自动捕获当前 6 Agent 开关、指标上报白名单、暂停状态及自启动配置；支持随时回滚"
              : "Capture configuration state across agents, metric toggles, pause state, and autostart with instant rollback"}
          </p>
        </div>
      </div>

      <form className="backup-create-form" onSubmit={handleCreate}>
        <input
          type="text"
          placeholder={
            isZh
              ? "输入配置快照描述（例如：升级前基准配置 / 调试模式）..."
              : "Enter snapshot description (e.g. baseline backup / debug mode)..."
          }
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="custom-input"
        />
        <button type="submit" className="btn btn-primary">
          {isZh ? "➕ 新建配置快照" : "➕ Create Snapshot"}
        </button>
      </form>

      <div className="backups-list">
        {backups.map((b) => (
          <div key={b.id} className="backup-item">
            <div className="backup-header">
              <div className="backup-title">
                <span className="tag tag-blue">{b.versionTag}</span>
                <span className="backup-desc">{b.description}</span>
              </div>
              <span className="backup-date font-mono">
                {new Date(b.createdAt).toLocaleString(isZh ? "zh-CN" : "en-US")}
              </span>
            </div>

            <div className="backup-meta">
              <div className="meta-pill">
                {isZh ? "Agent 状态:" : "Agents:"}
                <span className="font-mono">
                  {Object.entries(b.snapshot.agentToggles)
                    .filter(([, v]) => v)
                    .map(([k]) => k)
                    .join(", ")}
                </span>
              </div>
              <div className="meta-pill">
                {isZh ? "自启动:" : "Autostart:"}
                <span>{b.snapshot.autostartEnabled ? (isZh ? "已启用" : "Enabled") : (isZh ? "已禁用" : "Disabled")}</span>
              </div>
            </div>

            <div className="backup-actions">
              {restoringId === b.id ? (
                <div className="confirm-row">
                  <span className="confirm-tip">{isZh ? "确认回滚为此配置?" : "Confirm Rollback?"}</span>
                  <button
                    type="button"
                    className="btn btn-primary-sm"
                    onClick={() => handleRestore(b.id)}
                  >
                    {isZh ? "确认恢复" : "Confirm"}
                  </button>
                  <button
                    type="button"
                    className="btn btn-muted-sm"
                    onClick={() => setRestoringId(null)}
                  >
                    {isZh ? "取消" : "Cancel"}
                  </button>
                </div>
              ) : (
                <button
                  type="button"
                  className="btn btn-outline"
                  onClick={() => setRestoringId(b.id)}
                >
                  {isZh ? "↺ 恢复此配置快照" : "↺ Restore Backup"}
                </button>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
};
