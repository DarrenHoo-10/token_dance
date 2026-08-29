import React, { useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";
import type { EventType } from "../protocol/generated.ts";

export const OfflineQueueView: React.FC = () => {
  const {
    outbox,
    syncLogs,
    isOnline,
    toggleNetworkSimulation,
    triggerSyncNow,
    generateSampleEnvelope,
    generateSampleBatch,
    globalPaused,
    activeLanguage,
  } = useTokenShow();

  const [isSyncing, setIsSyncing] = useState(false);
  const [syncFeedback, setSyncFeedback] = useState<string | null>(null);
  const [selectedEventType, setSelectedEventType] = useState<EventType>("model_usage_recorded");
  const [previewMode, setPreviewMode] = useState<"envelope" | "batch">("envelope");

  const pendingItems = outbox.filter((item) => item.deliveryStatus !== "ACKED");

  const handleSync = async () => {
    try {
      setIsSyncing(true);
      setSyncFeedback(null);
      const ack = await triggerSyncNow();
      setSyncFeedback(
        activeLanguage === "zh"
          ? `✓ 批次 ${ack.batchId} 上报成功，服务端已 ACK ${ack.accepted} 条事件`
          : `✓ Batch ${ack.batchId} uploaded, server ACKed ${ack.accepted} events`
      );
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setSyncFeedback(`✗ ${msg}`);
    } finally {
      setIsSyncing(false);
    }
  };

  const currentEnvelope = generateSampleEnvelope(selectedEventType);
  const currentBatch = generateSampleBatch();

  return (
    <div className="offline-queue-view">
      <header className="page-head">
        <div>
          <p className="eyebrow">{activeLanguage === "zh" ? "可靠上报与离线持久化" : "Reliable Delivery & Offline Outbox"}</p>
          <h1>{activeLanguage === "zh" ? "离线 WAL 队列与上传字段审计" : "WAL Spool Queue & Upload Inspector"}</h1>
          <p>
            {activeLanguage === "zh"
              ? "本地 append-only WAL 文件缓冲；网络中断不丢数据，网络恢复后幂等重放批量上报"
              : "Append-only local WAL spool queue; zero data loss on network drops, idempotent replay"}
          </p>
        </div>

        <div style={{ display: "flex", gap: "10px", alignItems: "center", flexWrap: "wrap" }}>
          {/* Network Simulation Toggle */}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              padding: "8px 14px",
              borderRadius: "10px",
              border: "1px solid var(--line)",
              background: "white",
            }}
          >
            <span className={`status-dot ${isOnline ? "" : "danger"}`} />
            <span style={{ fontSize: "12px", fontWeight: 700 }}>
              {isOnline
                ? activeLanguage === "zh" ? "网络在线 (Online)" : "Online"
                : activeLanguage === "zh" ? "模拟断网 (Offline)" : "Offline Sim"}
            </span>
            <button
              type="button"
              className="btn btn-sm"
              onClick={toggleNetworkSimulation}
            >
              {isOnline ? (activeLanguage === "zh" ? "切换离线" : "Set Offline") : (activeLanguage === "zh" ? "恢复在线" : "Set Online")}
            </button>
          </div>

          <button
            type="button"
            className="btn btn-primary"
            onClick={handleSync}
            disabled={isSyncing || !isOnline || globalPaused}
          >
            {isSyncing
              ? activeLanguage === "zh" ? "正在批量上传..." : "Uploading..."
              : activeLanguage === "zh" ? "立即清空队列并上报" : "Drain & Sync Outbox"}
          </button>
        </div>
      </header>

      {syncFeedback && (
        <div
          style={{
            padding: "12px 18px",
            borderRadius: "12px",
            fontSize: "13px",
            fontWeight: 600,
            marginBottom: "20px",
            background: syncFeedback.startsWith("✓") ? "var(--lime-soft)" : "#fde8e5",
            color: syncFeedback.startsWith("✓") ? "var(--lime-dark)" : "var(--danger)",
            border: syncFeedback.startsWith("✓") ? "1px solid #c9f564" : "1px solid #fbc9c2",
          }}
        >
          {syncFeedback}
        </div>
      )}

      {/* Queue & Sync Status Cards */}
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "16px", marginBottom: "24px" }}>
        {/* Outbox Status Card */}
        <section className="panel" aria-label="Outbox Queue Status">
          <div className="panel-head">
            <div>
              <h2>{activeLanguage === "zh" ? "本地 Outbox Spool 队列" : "Local Outbox Spool Queue"}</h2>
              <p>{activeLanguage === "zh" ? "待上报标准化事件" : "Pending normalized envelopes"}</p>
            </div>
            <span className="tag tag-lime">{pendingItems.length} {activeLanguage === "zh" ? "条待处理" : "pending"}</span>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: "10px", maxHeight: "280px", overflowY: "auto" }}>
            {outbox.length === 0 ? (
              <p style={{ color: "var(--muted)", fontSize: "13px", textAlign: "center", padding: "20px 0" }}>
                {activeLanguage === "zh" ? "队列为空，无待上报事件" : "Outbox queue is empty"}
              </p>
            ) : (
              outbox.map((item) => (
                <div
                  key={item.id}
                  style={{
                    padding: "10px 12px",
                    background: "var(--soft)",
                    borderRadius: "8px",
                    border: "1px solid var(--line)",
                    fontSize: "12px",
                  }}
                >
                  <div style={{ display: "flex", justifyContent: "space-between", marginBottom: "4px" }}>
                    <strong style={{ fontFamily: "var(--font-mono)", fontSize: "11px" }}>{item.envelope.eventId}</strong>
                    <span
                      className={`tag ${
                        item.deliveryStatus === "ACKED"
                          ? "tag-lime"
                          : item.deliveryStatus === "IN_FLIGHT"
                          ? "tag-warning"
                          : item.deliveryStatus === "DEAD_LETTER"
                          ? "tag-danger"
                          : ""
                      }`}
                      style={{ fontSize: "10px" }}
                    >
                      {item.deliveryStatus}
                    </span>
                  </div>
                  <div style={{ display: "flex", justifyContent: "space-between", color: "var(--muted)", fontSize: "11px" }}>
                    <span>Agent: {item.envelope.agentId} ({item.envelope.payload.type})</span>
                    <span>Source: {item.envelope.source.kind}</span>
                  </div>
                </div>
              ))
            )}
          </div>
        </section>

        {/* Sync History Logs */}
        <section className="panel" aria-label="Recent Sync Logs">
          <div className="panel-head">
            <div>
              <h2>{activeLanguage === "zh" ? "最近同步历史 (Recent Sync Acks)" : "Recent Sync History"}</h2>
              <p>{activeLanguage === "zh" ? "服务端批次入库记录" : "Server batch ingestion logs"}</p>
            </div>
            <span className="tag">MySQL 8.0</span>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: "10px", maxHeight: "280px", overflowY: "auto" }}>
            {syncLogs.map((log) => (
              <div
                key={log.id}
                style={{
                  padding: "10px 12px",
                  background: "white",
                  borderRadius: "8px",
                  border: "1px solid var(--line)",
                  fontSize: "12px",
                }}
              >
                <div style={{ display: "flex", justifyContent: "space-between", marginBottom: "4px" }}>
                  <strong style={{ fontFamily: "var(--font-mono)", fontSize: "11px" }}>{log.batchId}</strong>
                  <span className="tag tag-lime" style={{ fontSize: "10px" }}>{log.status}</span>
                </div>
                <div style={{ display: "flex", justifyContent: "space-between", color: "var(--muted)", fontSize: "11px" }}>
                  <span>{log.timestamp}</span>
                  <span>{log.acceptedCount} events · 0 dups</span>
                </div>
              </div>
            ))}
          </div>
        </section>
      </div>

      {/* Embedded Live Upload Field Preview Inspector */}
      <section className="panel" aria-label="Upload Payload Whitelist Preview">
        <div className="panel-head">
          <div>
            <h2>{activeLanguage === "zh" ? "上传字段实时预览与脱敏审计" : "Live Upload Payload & Whitelist Inspector"}</h2>
            <p>
              {activeLanguage === "zh"
                ? "根据当前生效的指标开关与适配器设置生成的实际上报数据包"
                : "Exact schema-compliant JSON payload dispatched to TokenShow Server"}
            </p>
          </div>

          <div style={{ display: "flex", gap: "8px", alignItems: "center" }}>
            <button
              type="button"
              className={`btn btn-sm ${previewMode === "envelope" ? "btn-dark" : ""}`}
              onClick={() => setPreviewMode("envelope")}
            >
              {activeLanguage === "zh" ? "单事件 (EventEnvelope)" : "Event Envelope"}
            </button>
            <button
              type="button"
              className={`btn btn-sm ${previewMode === "batch" ? "btn-dark" : ""}`}
              onClick={() => setPreviewMode("batch")}
            >
              {activeLanguage === "zh" ? "批次 (UploadBatch)" : "Upload Batch"}
            </button>

            {previewMode === "envelope" && (
              <select
                className="form-input"
                style={{ height: "32px", fontSize: "12px" }}
                value={selectedEventType}
                onChange={(e) => setSelectedEventType(e.target.value as EventType)}
                aria-label="Preview Event Type"
              >
                <option value="model_usage_recorded">model_usage_recorded</option>
                <option value="code_changed">code_changed</option>
                <option value="skill_invoked">skill_invoked</option>
                <option value="tool_invoked">tool_invoked</option>
                <option value="turn_completed">turn_completed</option>
                <option value="agent_spawned">agent_spawned</option>
              </select>
            )}
          </div>
        </div>

        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "12px", marginBottom: "14px" }}>
          <div className="security-audit-box allowed">
            <strong>{activeLanguage === "zh" ? "✓ 白名单包含字段：" : "✓ Whitelisted Envelope Fields:"}</strong>
            <p style={{ fontSize: "11px", marginTop: "4px" }}>
              eventId, adapterId, agentId, occurredAt, sessionHash, turnHash, tokens, codeAddedLines, skillKey, costAmount, accuracy
            </p>
          </div>
          <div className="security-audit-box blocked">
            <strong>{activeLanguage === "zh" ? "✗ 本地阻断字段：" : "✗ Strictly Blocked Fields:"}</strong>
            <p style={{ fontSize: "11px", marginTop: "4px" }}>
              Prompts, Completions, Raw Source Code, Local File Paths, Secrets, API Keys
            </p>
          </div>
        </div>

        <div className="code-inspector">
          <pre>
            {JSON.stringify(previewMode === "envelope" ? currentEnvelope : currentBatch, null, 2)}
          </pre>
        </div>
      </section>
    </div>
  );
};
