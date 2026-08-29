import React, { useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";

export const LeaderboardExploreView: React.FC = () => {
  const {
    leaderboard,
    privacy,
    activeLanguage,
    setActiveTab,
  } = useTokenShow();

  const [searchQuery, setSearchQuery] = useState("");
  const [selectedAgent, setSelectedAgent] = useState("all");
  const [selectedAccuracy, setSelectedAccuracy] = useState("all");

  const filteredEntries = leaderboard.filter((item) => {
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      const match = item.nickname.toLowerCase().includes(q) || item.handle.toLowerCase().includes(q);
      if (!match) return false;
    }
    if (selectedAgent !== "all" && item.topAgent !== selectedAgent) {
      return false;
    }
    if (selectedAccuracy !== "all" && item.accuracy !== selectedAccuracy) {
      return false;
    }
    return true;
  });

  return (
    <div className="leaderboard-explore-view">
      <header className="page-head">
        <div>
          <p className="eyebrow">{activeLanguage === "zh" ? "全球开发者社区" : "Global Developer Community"}</p>
          <h1>{activeLanguage === "zh" ? "TokenDance 开发者排行榜与发现" : "TokenDance Leaderboard & Explore"}</h1>
          <p>
            {activeLanguage === "zh"
              ? "基于真实 Agent 遥测聚合的公开排行榜；默认私密，开启后实时上榜"
              : "Cross-Agent telemetry leaderboard; strictly private by default, instant public appearance on opt-in"}
          </p>
        </div>

        {!privacy.isPublicLeaderboard && (
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => setActiveTab("privacy")}
          >
            {activeLanguage === "zh" ? "⚡ 加入公开排行榜" : "⚡ Join Public Leaderboard"}
          </button>
        )}
      </header>

      {!privacy.isPublicLeaderboard && (
        <div
          style={{
            padding: "14px 18px",
            background: "#f0f4ef",
            border: "1px solid #d0dad0",
            borderRadius: "var(--radius)",
            marginBottom: "20px",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            fontSize: "13px",
          }}
        >
          <div>
            <strong>{activeLanguage === "zh" ? "🔒 你当前处于私密状态" : "🔒 Your Profile is Private"}</strong>
            <p style={{ color: "var(--muted)", fontSize: "12px", marginTop: "2px" }}>
              {activeLanguage === "zh"
                ? "你的数据仅在个人总览可见，未进入此公开榜单。可前往隐私设置一键开启。"
                : "Your telemetry is only visible to you in your dashboard. You can opt-in from Privacy Settings."}
            </p>
          </div>
          <button
            type="button"
            className="btn btn-sm btn-dark"
            onClick={() => setActiveTab("privacy")}
          >
            {activeLanguage === "zh" ? "前往设置公开范围" : "Configure Scope"}
          </button>
        </div>
      )}

      {/* Filter Row */}
      <div style={{ display: "flex", gap: "12px", marginBottom: "20px", flexWrap: "wrap", alignItems: "center" }}>
        <input
          type="text"
          className="form-input"
          style={{ width: "260px" }}
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          placeholder={activeLanguage === "zh" ? "搜索用户、Handle..." : "Search user, handle..."}
        />

        <select
          className="form-input"
          style={{ width: "160px" }}
          value={selectedAgent}
          onChange={(e) => setSelectedAgent(e.target.value)}
          aria-label="Filter by Agent"
        >
          <option value="all">{activeLanguage === "zh" ? "全部 Agent" : "All Agents"}</option>
          <option value="Claude Code">Claude Code</option>
          <option value="Codex">Codex</option>
          <option value="Grok Build">Grok Build</option>
          <option value="Cursor">Cursor</option>
        </select>

        <select
          className="form-input"
          style={{ width: "160px" }}
          value={selectedAccuracy}
          onChange={(e) => setSelectedAccuracy(e.target.value)}
          aria-label="Filter by Accuracy"
        >
          <option value="all">{activeLanguage === "zh" ? "全部精度" : "All Accuracy"}</option>
          <option value="exact">Exact (原生精确)</option>
          <option value="derived">Derived (派生)</option>
          <option value="correlated">Correlated (关联)</option>
        </select>
      </div>

      {/* Leaderboard Table */}
      <section className="panel" aria-label="Leaderboard Table">
        <div className="table-responsive">
          <table className="custom-table">
            <thead>
              <tr>
                <th style={{ width: "60px" }}>{activeLanguage === "zh" ? "排名" : "Rank"}</th>
                <th>{activeLanguage === "zh" ? "开发者" : "Developer"}</th>
                <th>{activeLanguage === "zh" ? "总 Token" : "Total Tokens"}</th>
                <th>{activeLanguage === "zh" ? "AI 代码行" : "AI Code Lines"}</th>
                <th>{activeLanguage === "zh" ? "主导 Agent" : "Top Agent"}</th>
                <th>{activeLanguage === "zh" ? "数据精度" : "Accuracy"}</th>
              </tr>
            </thead>
            <tbody>
              {filteredEntries.map((entry) => (
                <tr
                  key={entry.handle}
                  className={entry.isCurrentUser ? "highlight-row" : ""}
                >
                  <td>
                    <div className={`rank-badge ${entry.rank === 1 ? "top-1" : entry.rank === 2 ? "top-2" : entry.rank === 3 ? "top-3" : ""}`}>
                      {entry.rank}
                    </div>
                  </td>
                  <td>
                    <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
                      <div className="avatar-circle" style={{ width: "32px", height: "32px", fontSize: "11px" }}>
                        {entry.avatarText}
                      </div>
                      <div>
                        <div style={{ fontWeight: 750, display: "flex", alignItems: "center", gap: "6px" }}>
                          {entry.nickname}
                          {entry.isCurrentUser && (
                            <span className="tag tag-lime" style={{ fontSize: "10px", padding: "0 4px" }}>
                              {activeLanguage === "zh" ? "你" : "You"}
                            </span>
                          )}
                        </div>
                        <div style={{ fontSize: "11px", color: "var(--muted)" }}>@{entry.handle}</div>
                      </div>
                    </div>
                  </td>
                  <td>
                    <strong style={{ fontSize: "14px" }}>
                      {(entry.totalTokens / 1000000).toFixed(1)}M
                    </strong>
                  </td>
                  <td>
                    {entry.codeLines > 0 ? (
                      <span>{(entry.codeLines / 1000).toFixed(1)}k lines</span>
                    ) : (
                      <span style={{ color: "var(--muted)" }}>-</span>
                    )}
                  </td>
                  <td>
                    <span>
                      {entry.topAgent} ({entry.topAgentShare}%)
                    </span>
                  </td>
                  <td>
                    <span className="tag" style={{ fontSize: "11px" }}>
                      {entry.accuracy}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
};
