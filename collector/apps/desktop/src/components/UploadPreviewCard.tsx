import React, { useState } from "react";
import type { UploadBatchPreview, OutboxEnvelope } from "../tauri-bridge.ts";

interface UploadPreviewCardProps {
  preview: UploadBatchPreview | null;
  outbox: OutboxEnvelope[];
  onRefreshPreview: () => void;
  onSyncNow: () => void;
  isPaused: boolean;
  lang: "zh" | "en";
}

export const UploadPreviewCard: React.FC<UploadPreviewCardProps> = ({
  preview,
  outbox,
  onRefreshPreview,
  onSyncNow,
  isPaused,
  lang,
}) => {
  const isZh = lang === "zh";
  const [selectedTab, setSelectedTab] = useState<"json" | "envelopes">("json");

  return (
    <div className="card-section">
      <div className="section-head">
        <div>
          <h2>{isZh ? "上传批次预览与本地脱敏审计" : "Upload Batch Preview & Privacy Audit"}</h2>
          <p>
            {isZh
              ? "实时审查上报数据包：验证敏感信息（代码详情、会话路径、原始 prompt）是否已完全在本地脱敏"
              : "Inspect payload batches before network upload. Verify all prompt texts, raw code, and file paths are sanitized"}
          </p>
        </div>
        <div className="action-row">
          <button type="button" className="btn btn-outline" onClick={onRefreshPreview}>
            {isZh ? "🔄 刷新预览" : "🔄 Refresh Preview"}
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={onSyncNow}
            disabled={isPaused}
          >
            {isZh ? "⚡ 立即上报当前批次" : "⚡ Sync Batch Now"}
          </button>
        </div>
      </div>

      <div className="tab-pills">
        <button
          type="button"
          className={`tab-pill ${selectedTab === "json" ? "active" : ""}`}
          onClick={() => setSelectedTab("json")}
        >
          {isZh ? "JSON 批次载荷 (Schema v1.0)" : "JSON Payload (Schema v1.0)"}
        </button>
        <button
          type="button"
          className={`tab-pill ${selectedTab === "envelopes" ? "active" : ""}`}
          onClick={() => setSelectedTab("envelopes")}
        >
          {isZh ? "待上报事件信封清单" : "Queued Envelopes List"} ({outbox.length})
        </button>
      </div>

      {selectedTab === "json" ? (
        <div className="json-viewer-container">
          <div className="json-header">
            <div className="json-badges">
              <span className="tag tag-lime">{isZh ? "✓ 协议版本 1.0" : "✓ Protocol v1.0"}</span>
              <span className="tag tag-blue">{isZh ? "✓ 终端 Ed25519 签名就绪" : "✓ Ed25519 Ready"}</span>
              <span className="tag tag-purple">
                {preview?.redactionApplied
                  ? isZh
                    ? "✓ 本地脱敏与哈希已生效"
                    : "✓ Local Redaction Applied"
                  : "Raw"}
              </span>
            </div>
            <span className="json-meta">
              Batch ID: {preview?.batchId || "batch_preview_auto"} | {preview?.eventCount || 0} {isZh ? "条事件" : "events"}
            </span>
          </div>
          <pre className="json-code-block font-mono">
            {JSON.stringify(preview || {}, null, 2)}
          </pre>
        </div>
      ) : (
        <div className="envelopes-table-container">
          <table className="custom-table">
            <thead>
              <tr>
                <th>{isZh ? "事件 ID" : "Event ID"}</th>
                <th>{isZh ? "Agent" : "Agent"}</th>
                <th>{isZh ? "类型" : "Type"}</th>
                <th>{isZh ? "摘要载荷" : "Summary"}</th>
                <th>{isZh ? "精度" : "Accuracy"}</th>
                <th>{isZh ? "状态" : "Status"}</th>
              </tr>
            </thead>
            <tbody>
              {outbox.map((env) => (
                <tr key={env.id}>
                  <td className="font-mono">{env.eventId.slice(0, 16)}...</td>
                  <td>
                    <strong>{env.agentId}</strong>
                  </td>
                  <td>
                    <span className="type-tag">{env.eventType}</span>
                  </td>
                  <td>{env.payloadSummary}</td>
                  <td>
                    <span className="tag tag-lime">{env.accuracy}</span>
                  </td>
                  <td>
                    <span
                      className={`status-pill ${
                        env.deliveryStatus === "ACKED" ? "pill-active" : "pill-warning"
                      }`}
                    >
                      {env.deliveryStatus}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
};
