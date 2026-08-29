import React, { useEffect, useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";

export const UploadPreviewModal: React.FC = () => {
  const {
    isUploadPreviewOpen,
    setIsUploadPreviewOpen,
    recentBatch,
    refreshRecentBatch,
    metricToggles,
    activeLanguage,
  } = useTokenShow();
  const [viewMode, setViewMode] = useState<"envelope" | "batch">("batch");
  const [selectedEventId, setSelectedEventId] = useState("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!isUploadPreviewOpen) return;
    setError(null);
    void refreshRecentBatch().catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, [isUploadPreviewOpen, refreshRecentBatch]);

  useEffect(() => {
    if (recentBatch?.events.length && !recentBatch.events.some((event) => event.eventId === selectedEventId)) {
      setSelectedEventId(recentBatch.events[0].eventId);
    }
  }, [recentBatch, selectedEventId]);

  if (!isUploadPreviewOpen) return null;
  const selectedEnvelope = recentBatch?.events.find((event) => event.eventId === selectedEventId) ?? null;

  return (
    <div className="modal-overlay" role="dialog" aria-modal="true">
      <div className="modal-card modal-wide">
        <div className="modal-head">
          <div><p className="eyebrow">{activeLanguage === "zh" ? "数据安全与上报审计" : "Security & Upload Inspector"}</p><h2>{activeLanguage === "zh" ? "最近真实上传批次与白名单校验" : "Recent Real Batch & Whitelist Audit"}</h2></div>
          <button type="button" className="btn btn-sm" onClick={() => setIsUploadPreviewOpen(false)} aria-label="Close">✕</button>
        </div>
        <div className="modal-body">
          {error && <div className="feedback-error" role="alert">{error}</div>}
          <div className="audit-grid">
            <div className="security-audit-box allowed"><strong>✓ {activeLanguage === "zh" ? "协议白名单允许上传字段：" : "Whitelisted Upload Fields:"}</strong><p>schemaVersion, eventId, adapterId, agentId, occurredAt, hashes, aggregate metrics, accuracy</p></div>
            <div className="security-audit-box blocked"><strong>✗ {activeLanguage === "zh" ? "严格阻断的私有字段：" : "Blocked Private Fields:"}</strong><p>Prompts, completions, raw source code, absolute paths, credentials, API keys</p></div>
          </div>
          <div className="preview-toolbar">
            <div className="page-actions">
              <button type="button" className={`btn btn-sm ${viewMode === "batch" ? "btn-dark" : ""}`} onClick={() => setViewMode("batch")}>UploadBatch</button>
              <button type="button" className={`btn btn-sm ${viewMode === "envelope" ? "btn-dark" : ""}`} onClick={() => setViewMode("envelope")} disabled={!recentBatch?.events.length}>EventEnvelope</button>
            </div>
            {viewMode === "envelope" && <select className="form-input compact-input" value={selectedEventId} onChange={(event) => setSelectedEventId(event.target.value)}>{recentBatch?.events.map((event) => <option key={event.eventId} value={event.eventId}>{event.payload.type}</option>)}</select>}
          </div>
          {!recentBatch ? <p className="empty-state">{activeLanguage === "zh" ? "Collector 尚未构建任何批次。" : "Collector has not built a batch yet."}</p> : <div className="code-inspector"><pre>{JSON.stringify(viewMode === "batch" ? recentBatch : selectedEnvelope, null, 2)}</pre></div>}
          <div className="filter-tags"><strong>{activeLanguage === "zh" ? "当前权威指标开关：" : "Authoritative metric switches:"}</strong>{Object.entries(metricToggles).map(([key, enabled]) => <span key={key} className={`tag ${enabled ? "tag-lime" : ""}`}>{key}: {enabled ? "ON" : "OFF"}</span>)}</div>
        </div>
        <div className="modal-foot"><button type="button" className="btn btn-primary" onClick={() => setIsUploadPreviewOpen(false)}>{activeLanguage === "zh" ? "完成查看" : "Done"}</button></div>
      </div>
    </div>
  );
};
