import React, { useState } from "react";

interface DataDeletionCardProps {
  onPurgeLocalCache: () => void;
  onRequestDataDeletion: () => void;
  lang: "zh" | "en";
}

export const DataDeletionCard: React.FC<DataDeletionCardProps> = ({
  onPurgeLocalCache,
  onRequestDataDeletion,
  lang,
}) => {
  const isZh = lang === "zh";
  const [deleteConfirmText, setDeleteConfirmText] = useState("");
  const [showConfirmModal, setShowConfirmModal] = useState(false);
  const [purgeFeedback, setPurgeFeedback] = useState<string | null>(null);

  const handlePurgeLocal = () => {
    onPurgeLocalCache();
    setPurgeFeedback(
      isZh
        ? "✓ 本地 WAL 离线队列与 SQLite 缓存已全部清空！"
        : "✓ Local WAL outbox and SQLite cache successfully purged!"
    );
    setTimeout(() => setPurgeFeedback(null), 4000);
  };

  const handleExecuteDeletion = () => {
    if (deleteConfirmText.toLowerCase() === "delete" || deleteConfirmText === "删除") {
      onRequestDataDeletion();
      setShowConfirmModal(false);
      setDeleteConfirmText("");
      setPurgeFeedback(
        isZh
          ? "✓ 数据擦除指令已下达，服务端与本地数据均已标记永久清除"
          : "✓ Data deletion requested. Server & local state purged"
      );
    }
  };

  return (
    <div className="card-section">
      <div className="section-head">
        <div>
          <h2>{isZh ? "数据主权、缓存擦除与注销删除" : "Data Sovereignty & Permanent Deletion"}</h2>
          <p>
            {isZh
              ? "符合 GDPR / CCPA 严格标准：随时清空本地待发队列或向云端发起彻底注销擦除"
              : "GDPR / CCPA compliant. Purge local staging outbox or issue server-side data erasure"}
          </p>
        </div>
      </div>

      {purgeFeedback && (
        <div className="alert-box alert-success">
          {purgeFeedback}
        </div>
      )}

      <div className="deletion-options-grid">
        <div className="danger-box">
          <div className="danger-header">
            <h3>{isZh ? "清空本地 WAL 离线缓存" : "Purge Local WAL Cache"}</h3>
            <span className="tag tag-orange">{isZh ? "仅本地" : "Local Only"}</span>
          </div>
          <p className="danger-desc">
            {isZh
              ? "立即丢弃本地暂存但尚未上报的事件信封；不影响云端已确认的历史聚合数据。"
              : "Discard pending outbox envelopes stored in local WAL spool without affecting cloud aggregates."}
          </p>
          <button type="button" className="btn btn-outline-warning" onClick={handlePurgeLocal}>
            {isZh ? "🗑 清空本地待发缓存" : "🗑 Clear Staged Events"}
          </button>
        </div>

        <div className="danger-box danger-heavy">
          <div className="danger-header">
            <h3>{isZh ? "全量擦除所有数据 (GDPR/注销)" : "Erase All Data & Account"}</h3>
            <span className="tag tag-danger">{isZh ? "不可逆" : "Irreversible"}</span>
          </div>
          <p className="danger-desc">
            {isZh
              ? "清空本地全部缓存并向下游集群广播数据擦除指令，从所有公共排行榜与聚合数据库中彻底抹除。"
              : "Permanently wipe local data and trigger server-side deletion across all databases and leaderboards."}
          </p>
          <button
            type="button"
            className="btn btn-danger"
            onClick={() => setShowConfirmModal(true)}
          >
            {isZh ? "⚠️ 发起全量数据删除" : "⚠️ Request Complete Deletion"}
          </button>
        </div>
      </div>

      {showConfirmModal && (
        <div className="modal-overlay">
          <div className="modal-content">
            <h3 className="modal-title text-danger">
              {isZh ? "⚠️ 危险操作：确认彻底擦除所有数据？" : "⚠️ Confirm Complete Data Erasure?"}
            </h3>
            <p className="modal-desc">
              {isZh
                ? "此操作将永久清空本地采集缓存，并在云端注销所有终端绑定记录与历史指标。请在下方输入 DELETE 确认："
                : "This will permanently purge local collector outbox and trigger server-side wipe. Type DELETE below:"}
            </p>
            <input
              type="text"
              placeholder={isZh ? "输入 DELETE 或 删除" : "Type DELETE"}
              value={deleteConfirmText}
              onChange={(e) => setDeleteConfirmText(e.target.value)}
              className="custom-input danger-input"
              autoFocus
            />
            <div className="modal-actions">
              <button
                type="button"
                className="btn btn-danger"
                onClick={handleExecuteDeletion}
                disabled={
                  deleteConfirmText.toLowerCase() !== "delete" && deleteConfirmText !== "删除"
                }
              >
                {isZh ? "确认彻底擦除" : "Execute Erasure"}
              </button>
              <button
                type="button"
                className="btn btn-secondary"
                onClick={() => {
                  setShowConfirmModal(false);
                  setDeleteConfirmText("");
                }}
              >
                {isZh ? "取消" : "Cancel"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
