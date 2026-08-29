import React, { useEffect, useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";

export const OfflineQueueView: React.FC = () => {
  const {
    outbox,
    syncLogs,
    isOnline,
    triggerSyncNow,
    recentBatch,
    refreshRecentBatch,
    globalPaused,
    activeLanguage,
  } = useTokenShow();
  const [isSyncing, setIsSyncing] = useState(false);
  const [syncFeedback, setSyncFeedback] = useState<string | null>(null);
  const [previewMode, setPreviewMode] = useState<"envelope" | "batch">("batch");
  const [selectedEventId, setSelectedEventId] = useState("");

  useEffect(() => {
    void refreshRecentBatch().catch((err) => setSyncFeedback(`✗ ${err instanceof Error ? err.message : String(err)}`));
  }, [refreshRecentBatch]);

  useEffect(() => {
    if (recentBatch?.events.length && !recentBatch.events.some((event) => event.eventId === selectedEventId)) {
      setSelectedEventId(recentBatch.events[0].eventId);
    }
  }, [recentBatch, selectedEventId]);

  const pendingItems = outbox.filter((item) => item.deliveryStatus !== "ACKED");
  const selectedEnvelope = recentBatch?.events.find((event) => event.eventId === selectedEventId) ?? null;

  const handleSync = async () => {
    try {
      setIsSyncing(true);
      setSyncFeedback(null);
      const ack = await triggerSyncNow();
      setSyncFeedback(activeLanguage === "zh"
        ? `✓ 批次 ${ack.batchId} 上报成功，服务端 ACK ${ack.accepted} 条事件`
        : `✓ Batch ${ack.batchId} uploaded; server ACKed ${ack.accepted} events`);
    } catch (err) {
      setSyncFeedback(`✗ ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setIsSyncing(false);
    }
  };

  return (
    <div className="offline-queue-view">
      <header className="page-head">
        <div>
          <p className="eyebrow">{activeLanguage === "zh" ? "可靠上报与离线持久化" : "Reliable Delivery & Offline Outbox"}</p>
          <h1>{activeLanguage === "zh" ? "离线 WAL 队列与真实批次审计" : "WAL Queue & Real Batch Inspector"}</h1>
          <p>{activeLanguage === "zh" ? "预览 Collector 最近实际构建的批次，不生成随机样本。" : "Preview the most recent batch actually built by Collector; no random samples."}</p>
        </div>
        <div className="page-actions">
          <span className={`tag ${isOnline ? "tag-lime" : "tag-danger"}`}>{isOnline ? "ONLINE" : "OFFLINE"}</span>
          <button type="button" className="btn btn-primary" onClick={() => void handleSync()} disabled={isSyncing || !isOnline || globalPaused || !recentBatch}>
            {isSyncing ? (activeLanguage === "zh" ? "正在上传..." : "Uploading...") : (activeLanguage === "zh" ? "上报最近权威批次" : "Upload Recent Batch")}
          </button>
        </div>
      </header>

      {syncFeedback && <div className={syncFeedback.startsWith("✓") ? "feedback-success" : "feedback-error"}>{syncFeedback}</div>}

      <div className="two-column-grid">
        <section className="panel" aria-label="Outbox Queue Status">
          <div className="panel-head"><div><h2>{activeLanguage === "zh" ? "本地 Outbox" : "Local Outbox"}</h2><p>{activeLanguage === "zh" ? "权威队列状态" : "Authoritative queue state"}</p></div><span className="tag tag-lime">{pendingItems.length} pending</span></div>
          <div className="scroll-list">
            {outbox.length === 0 ? <p className="empty-state">{activeLanguage === "zh" ? "队列为空" : "Queue is empty"}</p> : outbox.map((item) => (
              <div className="list-card" key={item.id}>
                <div className="list-card-head"><strong className="mono">{item.envelope.eventId}</strong><span className={`tag ${item.deliveryStatus === "ACKED" ? "tag-lime" : ""}`}>{item.deliveryStatus}</span></div>
                <small>{item.envelope.agentId} · {item.envelope.payload.type} · {item.envelope.source.kind}</small>
              </div>
            ))}
          </div>
        </section>

        <section className="panel" aria-label="Recent Sync Logs">
          <div className="panel-head"><div><h2>{activeLanguage === "zh" ? "最近同步 ACK" : "Recent Sync ACKs"}</h2><p>{activeLanguage === "zh" ? "服务端入库结果" : "Server ingestion results"}</p></div></div>
          <div className="scroll-list">
            {syncLogs.map((log) => (
              <div className="list-card" key={log.id}>
                <div className="list-card-head"><strong className="mono">{log.batchId}</strong><span className={`tag ${log.status === "ACKED" ? "tag-lime" : "tag-danger"}`}>{log.status}</span></div>
                <small>{log.timestamp} · {log.acceptedCount}/{log.eventCount} accepted</small>
              </div>
            ))}
          </div>
        </section>
      </div>

      <section className="panel" aria-label="Upload Payload Whitelist Preview">
        <div className="panel-head">
          <div><h2>{activeLanguage === "zh" ? "最近真实批次预览" : "Recent Real Batch Preview"}</h2><p>{recentBatch ? `${recentBatch.batchId} · ${recentBatch.events.length} events` : (activeLanguage === "zh" ? "Collector 尚无批次" : "No Collector batch yet")}</p></div>
          <div className="page-actions">
            <button type="button" className={`btn btn-sm ${previewMode === "batch" ? "btn-dark" : ""}`} onClick={() => setPreviewMode("batch")}>UploadBatch</button>
            <button type="button" className={`btn btn-sm ${previewMode === "envelope" ? "btn-dark" : ""}`} onClick={() => setPreviewMode("envelope")} disabled={!recentBatch?.events.length}>EventEnvelope</button>
            {previewMode === "envelope" && (
              <select className="form-input compact-input" value={selectedEventId} onChange={(event) => setSelectedEventId(event.target.value)} aria-label="Recent batch event">
                {recentBatch?.events.map((event) => <option key={event.eventId} value={event.eventId}>{event.payload.type} · {event.eventId.slice(0, 10)}</option>)}
              </select>
            )}
          </div>
        </div>
        <div className="audit-grid">
          <div className="security-audit-box allowed"><strong>✓ {activeLanguage === "zh" ? "协议白名单字段" : "Protocol whitelist"}</strong><p>eventId, adapterId, agentId, occurredAt, hashes, aggregate metrics, accuracy</p></div>
          <div className="security-audit-box blocked"><strong>✗ {activeLanguage === "zh" ? "本地阻断字段" : "Locally blocked"}</strong><p>Prompts, completions, raw code, file paths, secrets, API keys</p></div>
        </div>
        <div className="code-inspector"><pre>{JSON.stringify(previewMode === "batch" ? recentBatch : selectedEnvelope, null, 2)}</pre></div>
      </section>
    </div>
  );
};
