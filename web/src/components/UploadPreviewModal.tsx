import React, { useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";
import type { EventType } from "../protocol/generated.ts";

export const UploadPreviewModal: React.FC = () => {
  const {
    isUploadPreviewOpen,
    setIsUploadPreviewOpen,
    generateSampleEnvelope,
    generateSampleBatch,
    metricToggles,
    activeLanguage,
  } = useTokenShow();

  const [selectedEventType, setSelectedEventType] = useState<EventType>("model_usage_recorded");
  const [viewMode, setViewMode] = useState<"envelope" | "batch">("envelope");

  if (!isUploadPreviewOpen) return null;

  const currentEnvelope = generateSampleEnvelope(selectedEventType);
  const currentBatch = generateSampleBatch();

  return (
    <div className="modal-overlay" role="dialog" aria-modal="true">
      <div className="modal-card modal-wide">
        <div className="modal-head">
          <div>
            <p className="eyebrow">{activeLanguage === "zh" ? "数据安全与上报审计" : "Security & Upload Inspector"}</p>
            <h2>{activeLanguage === "zh" ? "上传字段实时预览与白名单校验" : "Upload Payload Preview & Whitelist Audit"}</h2>
          </div>
          <button
            type="button"
            className="btn btn-sm"
            onClick={() => setIsUploadPreviewOpen(false)}
            aria-label="Close"
          >
            ✕
          </button>
        </div>

        <div className="modal-body">
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "12px", marginBottom: "16px" }}>
            <div className="security-audit-box allowed">
              <strong>{activeLanguage === "zh" ? "✓ 协议白名单允许上传字段：" : "✓ Whitelisted Upload Fields:"}</strong>
              <div style={{ marginTop: "6px", fontSize: "11px" }}>
                schemaVersion, eventId, adapterId, agentId, occurredAt, sessionHash, turnHash, tokens, codeAddedLines, codeDeletedLines, skillKey, toolCategory, costAmount, accuracy
              </div>
            </div>
            <div className="security-audit-box blocked">
              <strong>{activeLanguage === "zh" ? "✗ 严格阻断的私有敏感字段：" : "✗ Blocked Private Fields:"}</strong>
              <div style={{ marginTop: "6px", fontSize: "11px" }}>
                Prompts, Model Completions, Raw Source Code Diffs, Local Absolute Paths, System Credentials, API Keys, Repository URLs
              </div>
            </div>
          </div>

          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "12px" }}>
            <div style={{ display: "flex", gap: "8px" }}>
              <button
                type="button"
                className={`btn btn-sm ${viewMode === "envelope" ? "btn-dark" : ""}`}
                onClick={() => setViewMode("envelope")}
              >
                {activeLanguage === "zh" ? "单事件信封 (EventEnvelope)" : "Single Event Envelope"}
              </button>
              <button
                type="button"
                className={`btn btn-sm ${viewMode === "batch" ? "btn-dark" : ""}`}
                onClick={() => setViewMode("batch")}
              >
                {activeLanguage === "zh" ? "上报批次 (UploadBatch)" : "Upload Batch"}
              </button>
            </div>

            {viewMode === "envelope" && (
              <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
                <span style={{ fontSize: "12px", color: "var(--muted)", fontWeight: 600 }}>
                  {activeLanguage === "zh" ? "事件类型：" : "Event Type:"}
                </span>
                <select
                  className="form-input"
                  style={{ height: "32px", fontSize: "12px" }}
                  value={selectedEventType}
                  onChange={(e) => setSelectedEventType(e.target.value as EventType)}
                >
                  <option value="model_usage_recorded">model_usage_recorded (Tokens / Duration)</option>
                  <option value="code_changed">code_changed (Lines Added / Deleted)</option>
                  <option value="skill_invoked">skill_invoked (Skill Key / Type)</option>
                  <option value="tool_invoked">tool_invoked (Tool Category)</option>
                  <option value="turn_completed">turn_completed (Turn Hash / Metrics)</option>
                  <option value="agent_spawned">agent_spawned (Subagents)</option>
                </select>
              </div>
            )}
          </div>

          <div className="code-inspector">
            <pre>
              {JSON.stringify(viewMode === "envelope" ? currentEnvelope : currentBatch, null, 2)}
            </pre>
          </div>

          <div style={{ marginTop: "14px", display: "flex", flexWrap: "wrap", gap: "8px", alignItems: "center" }}>
            <span style={{ fontSize: "12px", color: "var(--muted)", fontWeight: 700 }}>
              {activeLanguage === "zh" ? "当前生效指标过滤：" : "Active Metric Filters:"}
            </span>
            {Object.entries(metricToggles).map(([k, enabled]) => (
              <span
                key={k}
                className={`tag ${enabled ? "tag-lime" : ""}`}
                style={{ opacity: enabled ? 1 : 0.4 }}
              >
                {k}: {enabled ? "ON" : "OFF"}
              </span>
            ))}
          </div>
        </div>

        <div className="modal-foot">
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => setIsUploadPreviewOpen(false)}
          >
            {activeLanguage === "zh" ? "完成查看" : "Done"}
          </button>
        </div>
      </div>
    </div>
  );
};
