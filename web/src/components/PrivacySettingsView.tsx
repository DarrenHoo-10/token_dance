import React, { useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";

export const PrivacySettingsView: React.FC = () => {
  const {
    privacy,
    updatePrivacyScope,
    accountStatus,
    deletionJob,
    requestDataDeletion,
    activeLanguage,
    setActiveTab,
  } = useTokenShow();

  const [isDeleteDialogOpen, setIsDeleteDialogOpen] = useState(false);
  const [deleteConfirmText, setDeleteConfirmText] = useState("");
  const [showScopeConfirmation, setShowScopeConfirmation] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const applyPrivacyUpdate = (updates: Parameters<typeof updatePrivacyScope>[0]) => {
    void updatePrivacyScope(updates).catch((err) => setActionError(err instanceof Error ? err.message : String(err)));
  };

  const handleTogglePublicLeaderboard = () => {
    if (!privacy.isPublicLeaderboard) {
      // Switching to public requires confirmation checklist
      setShowScopeConfirmation(true);
    } else {
      // Switching back to private immediately removes from leaderboard
      applyPrivacyUpdate({ isPublicLeaderboard: false });
    }
  };

  const handleConfirmMakePublic = async () => {
    try {
      await updatePrivacyScope({ isPublicLeaderboard: true });
      setShowScopeConfirmation(false);
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    }
  };

  const handleExecuteDataDeletion = async () => {
    if (deleteConfirmText.toLowerCase() !== "delete" && deleteConfirmText !== "删除") return;
    try {
      setActionError(null);
      await requestDataDeletion();
      setIsDeleteDialogOpen(false);
      setDeleteConfirmText("");
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <div className="privacy-settings-view">
      <header className="page-head">
        <div>
          <p className="eyebrow">{activeLanguage === "zh" ? "数据主权与可见性控制" : "Data Sovereignty & Privacy Controls"}</p>
          <h1>{activeLanguage === "zh" ? "排行榜公开范围与数据擦除" : "Leaderboard Scope & Data Erasure"}</h1>
          <p>
            {activeLanguage === "zh"
              ? "默认严格私密；你拥有完整的公开范围控制权，可随时一键退出排行榜或申请永久删除数据"
              : "Strictly private by default; full control over public fields, instant leaderboard opt-out, data deletion"}
          </p>
        </div>
      </header>

      {actionError && <div className="feedback-error" role="alert">{actionError}</div>}

      {(deletionJob || accountStatus === "deletion_pending" || accountStatus === "purged") && (
        <div className={deletionJob?.status === "FAILED" ? "feedback-error" : "deletion-status"}>
          <strong>{activeLanguage === "zh" ? "删除任务状态" : "Deletion job"}: {deletionJob?.status ?? (accountStatus === "purged" ? "PURGED" : "REQUESTED")}</strong>
          {deletionJob?.status === "REQUESTED" && <span>{activeLanguage === "zh" ? "命令已 ACK，等待 worker 执行。" : "Command ACKed; waiting for worker."}</span>}
          {deletionJob?.status === "RUNNING" && <span>{activeLanguage === "zh" ? "正在清除服务端聚合、批次和设备绑定。" : "Purging server aggregates, batches, and devices."}</span>}
          {deletionJob?.status === "FAILED" && <span>{deletionJob.failureReason ?? (activeLanguage === "zh" ? "删除失败，请重试。" : "Deletion failed; retry.")}</span>}
          {deletionJob?.status === "PURGED" && <span>{activeLanguage === "zh" ? "服务端权威状态已确认清除完成。" : "Authoritative server state confirms purge completion."}</span>}
        </div>
      )}

      <div className="settings-grid">
        <aside className="settings-nav-panel panel">
          <div style={{ fontSize: "12px", fontWeight: 800, color: "var(--muted)", marginBottom: "10px", paddingLeft: "14px" }}>
            {activeLanguage === "zh" ? "隐私与安全中心" : "PRIVACY CENTER"}
          </div>
          <button type="button" className="settings-nav-item active">
            {activeLanguage === "zh" ? "排行榜与公开范围" : "Leaderboard Scope"}
          </button>
          <button
            type="button"
            className="settings-nav-item"
            onClick={() => setActiveTab("devices")}
          >
            {activeLanguage === "zh" ? "已连接设备管理" : "Connected Devices"}
          </button>
          <button
            type="button"
            className="settings-nav-item"
            onClick={() => setActiveTab("queue")}
          >
            {activeLanguage === "zh" ? "上传白名单审计" : "Upload Whitelist"}
          </button>
        </aside>

        <main style={{ display: "flex", flexDirection: "column", gap: "20px" }}>
          {/* Leaderboard Scope Master Card */}
          <section className="panel" aria-label="Leaderboard Participation Scope">
            <div className="panel-head">
              <div>
                <h2>{activeLanguage === "zh" ? "排行榜与公开主页设置" : "Leaderboard & Public Profile"}</h2>
                <p>
                  {activeLanguage === "zh"
                    ? "默认仅自己可见 (Private by default)"
                    : "Private by default; explicit consent required for public leaderboard"}
                </p>
              </div>
              <span className={`tag ${privacy.isPublicLeaderboard ? "tag-lime" : ""}`}>
                {privacy.isPublicLeaderboard
                  ? activeLanguage === "zh" ? "公开 (Public)" : "Public"
                  : activeLanguage === "zh" ? "仅自己可见 (Private)" : "Private (Default)"}
              </span>
            </div>

            <div className="setting-item-row">
              <div className="setting-item-text">
                <h3>{activeLanguage === "zh" ? "参加公开排行榜" : "Join Public Leaderboard"}</h3>
                <p>
                  {activeLanguage === "zh"
                    ? "开启后，公开昵称、排名与下方勾选的聚合指标会进入社区排行榜。关闭则立即下榜且全站不可查。"
                    : "When enabled, your public nickname and selected aggregate metrics appear on leaderboard."}
                </p>
              </div>
              <button
                type="button"
                className={`switch-toggle ${privacy.isPublicLeaderboard ? "on" : ""}`}
                onClick={handleTogglePublicLeaderboard}
                aria-label="Toggle Public Leaderboard"
              >
                <div className="switch-handle" />
              </button>
            </div>

            {/* Granular Public Fields */}
            <div style={{ marginTop: "16px", paddingTop: "16px", borderTop: "1px solid var(--line)" }}>
              <h3 style={{ fontSize: "14px", marginBottom: "12px" }}>
                {activeLanguage === "zh" ? "细粒度公开字段选择（仅在开启排行榜时生效）" : "Granular Public Field Visibility"}
              </h3>

              <div className="responsive-form-grid">
                <label style={{ display: "flex", alignItems: "center", gap: "10px", fontSize: "13px", cursor: "pointer" }}>
                  <input
                    type="checkbox"
                    checked={privacy.showTokenTotals}
                    onChange={(e) => applyPrivacyUpdate({ showTokenTotals: e.target.checked })}
                  />
                  <span>{activeLanguage === "zh" ? "公开 Token 总量与趋势" : "Show Token Totals & Trends"}</span>
                </label>

                <label style={{ display: "flex", alignItems: "center", gap: "10px", fontSize: "13px", cursor: "pointer" }}>
                  <input
                    type="checkbox"
                    checked={privacy.showAgentBreakdown}
                    onChange={(e) => applyPrivacyUpdate({ showAgentBreakdown: e.target.checked })}
                  />
                  <span>{activeLanguage === "zh" ? "公开 Agent 占比构成" : "Show Agent Breakdown"}</span>
                </label>

                <label style={{ display: "flex", alignItems: "center", gap: "10px", fontSize: "13px", cursor: "pointer" }}>
                  <input
                    type="checkbox"
                    checked={privacy.showActivityHeatmap}
                    onChange={(e) => applyPrivacyUpdate({ showActivityHeatmap: e.target.checked })}
                  />
                  <span>{activeLanguage === "zh" ? "公开活跃热力图" : "Show Activity Heatmap"}</span>
                </label>

                <label style={{ display: "flex", alignItems: "center", gap: "10px", fontSize: "13px", cursor: "pointer" }}>
                  <input
                    type="checkbox"
                    checked={privacy.showTopSkills}
                    onChange={(e) => applyPrivacyUpdate({ showTopSkills: e.target.checked })}
                  />
                  <span>{activeLanguage === "zh" ? "公开 Top Skill 排行" : "Show Top Skills"}</span>
                </label>

                <label style={{ display: "flex", alignItems: "center", gap: "10px", fontSize: "13px", cursor: "pointer" }}>
                  <input
                    type="checkbox"
                    checked={privacy.showAICodeLines}
                    onChange={(e) => applyPrivacyUpdate({ showAICodeLines: e.target.checked })}
                  />
                  <span>{activeLanguage === "zh" ? "公开 AI 生成代码行数" : "Show AI Code Lines"}</span>
                </label>
              </div>
            </div>
          </section>

          {/* Data Deletion / GDPR Purge Card */}
          <section className="panel" aria-label="Data Purge Action">
            <div className="panel-head">
              <div>
                <h2>{activeLanguage === "zh" ? "数据注销与彻底删除" : "Data Erasure & Account Purge"}</h2>
                <p>
                  {activeLanguage === "zh"
                    ? "永久删除 TokenShow 服务端保存的所有聚合指标与设备绑定"
                    : "Permanently delete all server aggregates and device bindings"}
                </p>
              </div>
            </div>

            <div className="setting-item-row">
              <div className="setting-item-text">
                <h3 style={{ color: "var(--danger)" }}>
                  {activeLanguage === "zh" ? "请求删除服务端数据" : "Request Server Data Deletion"}
                </h3>
                <p>
                  {activeLanguage === "zh"
                    ? "创建数据删除任务，立即从公开查询与排行榜永久移除，并擦除云端存储的历史数据。"
                    : "Immediately removes you from all leaderboards and purges historical cloud telemetry."}
                </p>
              </div>
              <button
                type="button"
                className="btn btn-danger"
                onClick={() => setIsDeleteDialogOpen(true)}
              >
                {activeLanguage === "zh" ? "请求删除" : "Delete Data"}
              </button>
            </div>
          </section>
        </main>
      </div>

      {/* Scope Confirmation Dialog */}
      {showScopeConfirmation && (
        <div className="modal-overlay" role="dialog" aria-modal="true">
          <div className="modal-card">
            <div className="modal-head">
              <h2>{activeLanguage === "zh" ? "确认将数据加入公开排行榜？" : "Confirm Public Leaderboard Scope"}</h2>
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => setShowScopeConfirmation(false)}
              >
                ✕
              </button>
            </div>
            <div className="modal-body">
              <p style={{ fontSize: "13px", color: "var(--muted)", marginBottom: "14px" }}>
                {activeLanguage === "zh"
                  ? "从私密变更为公开时，以下清单中的字段将向社区展示："
                  : "The following fields will be visible to the community:"}
              </p>
              <div className="security-audit-box allowed" style={{ marginBottom: "16px" }}>
                <ul style={{ paddingLeft: "20px", fontSize: "12px" }}>
                  <li>{activeLanguage === "zh" ? "公开昵称与 Handle" : "Nickname & Handle"}</li>
                  <li>{activeLanguage === "zh" ? "总 Token 数量与排名" : "Total Tokens & Rank"}</li>
                  <li>{activeLanguage === "zh" ? "六 Agent 使用占比构成" : "6-Agent Usage Share"}</li>
                  <li>{activeLanguage === "zh" ? "不含任何邮箱、设备标识、Prompt 或代码正文" : "NO emails, device IDs, prompts or code"}</li>
                </ul>
              </div>
            </div>
            <div className="modal-foot">
              <button
                type="button"
                className="btn"
                onClick={() => setShowScopeConfirmation(false)}
              >
                {activeLanguage === "zh" ? "取消并保持私密" : "Keep Private"}
              </button>
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => void handleConfirmMakePublic()}
              >
                {activeLanguage === "zh" ? "确认公开" : "Confirm Public"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Data Deletion Confirmation Modal */}
      {isDeleteDialogOpen && (
        <div className="modal-overlay" role="dialog" aria-modal="true">
          <div className="modal-card">
            <div className="modal-head">
              <h2 style={{ color: "var(--danger)" }}>
                {activeLanguage === "zh" ? "⚠️ 危险操作：确认删除数据？" : "⚠️ Confirm Data Deletion"}
              </h2>
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => setIsDeleteDialogOpen(false)}
              >
                ✕
              </button>
            </div>
            <div className="modal-body">
              <p style={{ fontSize: "13px", color: "var(--muted)", marginBottom: "14px" }}>
                {activeLanguage === "zh"
                  ? "此操作不可逆！将立即从 MySQL 8.0 服务端擦除所有历史上报批次、会话哈希和排行榜数据。"
                  : "This action is irreversible. All server batches and rankings will be purged."}
              </p>
              <div className="form-group">
                <label className="form-label">
                  {activeLanguage === "zh" ? '请输入 "delete" 或 "删除" 以确认：' : 'Type "delete" to confirm:'}
                </label>
                <input
                  type="text"
                  className="form-input"
                  value={deleteConfirmText}
                  onChange={(e) => setDeleteConfirmText(e.target.value)}
                  placeholder="delete"
                />
              </div>
            </div>
            <div className="modal-foot">
              <button
                type="button"
                className="btn"
                onClick={() => setIsDeleteDialogOpen(false)}
              >
                {activeLanguage === "zh" ? "取消" : "Cancel"}
              </button>
              <button
                type="button"
                className="btn btn-danger"
                onClick={() => void handleExecuteDataDeletion()}
                disabled={deleteConfirmText.toLowerCase() !== "delete" && deleteConfirmText !== "删除"}
              >
                {activeLanguage === "zh" ? "确认永久删除" : "Permanently Delete"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
