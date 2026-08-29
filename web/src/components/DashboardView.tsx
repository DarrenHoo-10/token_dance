import React, { useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";

export const DashboardView: React.FC = () => {
  const {
    metrics,
    agents,
    skills,
    toggleAgent,
    globalPaused,
    toggleGlobalPause,
    triggerSyncNow,
    isOnline,
    outbox,
    privacy,
    activeLanguage,
    setActiveTab,
    setIsUploadPreviewOpen,
  } = useTokenShow();

  const [timeRange, setTimeRange] = useState<"today" | "7d" | "30d" | "all">("30d");
  const [agentFilter, setAgentFilter] = useState<string>("all");
  const [modelFilter, setModelFilter] = useState<string>("all");
  const [isSyncing, setIsSyncing] = useState(false);
  const [syncFeedback, setSyncFeedback] = useState<string | null>(null);

  const pendingCount = outbox.filter((item) => item.deliveryStatus !== "ACKED").length;

  const handleManualSync = async () => {
    try {
      setIsSyncing(true);
      setSyncFeedback(null);
      const ack = await triggerSyncNow();
      setSyncFeedback(
        activeLanguage === "zh"
          ? `同步成功！批次 ${ack.batchId.slice(0, 12)} 已确认 ${ack.accepted} 个事件`
          : `Sync success! Batch ${ack.batchId.slice(0, 12)} acked ${ack.accepted} events`
      );
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setSyncFeedback(msg);
    } finally {
      setIsSyncing(false);
    }
  };

  // Generate 70 heatmap squares
  const heatmapSquares = Array.from({ length: 70 }, (_, i) => {
    if (i % 11 === 0) return "level-4";
    if (i % 7 === 0) return "level-3";
    if (i % 4 === 0) return "level-2";
    if (i % 3 === 0) return "level-1";
    return "";
  });

  return (
    <div className="dashboard-view">
      {globalPaused && (
        <div className="pause-banner">
          <div className="pause-banner-content">
            <span style={{ fontSize: "18px" }}>⏸</span>
            <span>
              {activeLanguage === "zh"
                ? "全局采集与上报已暂停。所有 Agent 遥测已冻结，新事件暂不入队。"
                : "Global collection is paused. All agent telemetry is frozen."}
            </span>
          </div>
          <button
            type="button"
            className="btn btn-sm btn-dark"
            onClick={() => void toggleGlobalPause().catch(() => undefined)}
          >
            {activeLanguage === "zh" ? "一键恢复采集" : "Resume All"}
          </button>
        </div>
      )}

      <header className="page-head">
        <div>
          <p className="eyebrow">
            {activeLanguage === "zh" ? "个人数据驾驶舱" : "Personal Analytics"}
          </p>
          <h1>{activeLanguage === "zh" ? "你的 Token 正在起舞" : "Let Your Tokens Dance"}</h1>
          <p>
            {activeLanguage === "zh"
              ? "聚合 Codex、Claude Code、Grok Build、Cursor、ZCode、DeepSeek 多源遥测"
              : "Cross-Agent Telemetry from Codex, Claude, Grok, Cursor, ZCode, DeepSeek"}
          </p>
        </div>

        <div style={{ display: "flex", gap: "10px", alignItems: "center", flexWrap: "wrap" }}>
          <div className="segmented" style={{ display: "flex", background: "white", padding: "4px", borderRadius: "10px", border: "1px solid var(--line)" }}>
            {(["today", "7d", "30d", "all"] as const).map((r) => (
              <button
                key={r}
                type="button"
                style={{
                  padding: "6px 12px",
                  borderRadius: "6px",
                  fontSize: "12px",
                  fontWeight: timeRange === r ? 750 : 500,
                  background: timeRange === r ? "#111512" : "transparent",
                  color: timeRange === r ? "white" : "var(--muted)",
                }}
                onClick={() => setTimeRange(r)}
              >
                {r === "today" ? (activeLanguage === "zh" ? "今天" : "Today") : r === "7d" ? (activeLanguage === "zh" ? "7 天" : "7D") : r === "30d" ? (activeLanguage === "zh" ? "30 天" : "30D") : (activeLanguage === "zh" ? "全部" : "All")}
              </button>
            ))}
          </div>

          <button
            type="button"
            className="btn"
            onClick={() => setIsUploadPreviewOpen(true)}
          >
            {activeLanguage === "zh" ? "🔍 审计上传字段" : "🔍 Audit Upload"}
          </button>
        </div>
      </header>

      {/* Top Metrics Grid */}
      <section className="metric-grid" aria-label="Overview Metrics">
        <div className="metric-card">
          <small>{activeLanguage === "zh" ? "预估费用" : "Estimated Cost"}</small>
          <strong>${metrics.estimatedCost.toLocaleString(undefined, { minimumFractionDigits: 2 })}</strong>
          <em>{activeLanguage === "zh" ? "按公允费率" : "Standard Rate"}</em>
        </div>
        <div className="metric-card">
          <small>{activeLanguage === "zh" ? "总 Token" : "Total Tokens"}</small>
          <strong>{(metrics.totalTokens / 1000000).toFixed(1)}M</strong>
          <em>▲ 18.7%</em>
        </div>
        <div className="metric-card">
          <small>{activeLanguage === "zh" ? "生成代码行" : "AI Code Lines"}</small>
          <strong>{(metrics.codeLinesAdded / 1000).toFixed(1)}K</strong>
          <em>▲ 12.4%</em>
        </div>
        <div className="metric-card">
          <small>{activeLanguage === "zh" ? "单行 Token" : "Tokens / Line"}</small>
          <strong>{metrics.tokensPerLine}</strong>
          <em>{activeLanguage === "zh" ? "加权平均" : "Weighted Avg"}</em>
        </div>
        <div className="metric-card">
          <small>{activeLanguage === "zh" ? "输入上下文" : "Input Tokens"}</small>
          <strong>{(metrics.inputContextTokens / 1000000).toFixed(1)}M</strong>
          <span style={{ fontSize: "11px", color: "var(--muted)" }}>56.7%</span>
        </div>
        <div className="metric-card">
          <small>{activeLanguage === "zh" ? "输出 Token" : "Output Tokens"}</small>
          <strong>{(metrics.outputTokens / 1000000).toFixed(1)}M</strong>
          <span style={{ fontSize: "11px", color: "var(--muted)" }}>24.0%</span>
        </div>
        <div className="metric-card">
          <small>{activeLanguage === "zh" ? "缓存命中率" : "Cache Hit Rate"}</small>
          <strong>{metrics.cacheHitRate}%</strong>
          <em>▲ 4.2%</em>
        </div>
        <div className="metric-card">
          <small>{activeLanguage === "zh" ? "总活跃时长" : "Active Hours"}</small>
          <strong>{metrics.totalHours}h</strong>
          <span style={{ fontSize: "11px", color: "var(--muted)" }}>{metrics.totalSessions} sessions</span>
        </div>
        <div className="metric-card">
          <small>{activeLanguage === "zh" ? "总交互轮次" : "Total Turns"}</small>
          <strong>{(metrics.totalTurns / 1000).toFixed(1)}K</strong>
          <em>▲ 9.1%</em>
        </div>
        <div className="metric-card">
          <small>{activeLanguage === "zh" ? "排行榜状态" : "Rank Visibility"}</small>
          <strong style={{ fontSize: "15px" }}>
            {privacy.isPublicLeaderboard ? `#${metrics.globalRank}` : (activeLanguage === "zh" ? "仅自己可见" : "Private")}
          </strong>
          <em style={{ color: privacy.isPublicLeaderboard ? "var(--good)" : "var(--muted)" }}>
            {privacy.isPublicLeaderboard ? (activeLanguage === "zh" ? "已公开" : "Public") : (activeLanguage === "zh" ? "未公开" : "Private")}
          </em>
        </div>
      </section>

      {/* Main Grid: 30D Token Trend & Agent Breakdown */}
      <div className="dashboard-main-grid">
        <section className="panel" aria-label="Token Trend Chart">
          <div className="panel-head">
            <div>
              <h2>{activeLanguage === "zh" ? "Token 动态趋势" : "Token Dynamics Trend"}</h2>
              <p>{activeLanguage === "zh" ? "按模型与 Agent 筛选输入、输出和缓存 Token" : "Filter input, output, cache tokens by model & agent"}</p>
            </div>
            <div style={{ display: "flex", gap: "8px" }}>
              <select
                className="form-input"
                style={{ height: "32px", fontSize: "12px" }}
                value={modelFilter}
                onChange={(e) => setModelFilter(e.target.value)}
                aria-label="Model Filter"
              >
                <option value="all">{activeLanguage === "zh" ? "全部模型" : "All Models"}</option>
                <option value="claude-3-7-sonnet">Claude 3.7 Sonnet</option>
                <option value="gpt-5-codex">GPT-5 Codex</option>
                <option value="grok-3-code">Grok 3 Code</option>
              </select>

              <select
                className="form-input"
                style={{ height: "32px", fontSize: "12px" }}
                value={agentFilter}
                onChange={(e) => setAgentFilter(e.target.value)}
                aria-label="Agent Filter"
              >
                <option value="all">{activeLanguage === "zh" ? "全部 Agent (6)" : "All Agents (6)"}</option>
                <option value="claude-code">Claude Code</option>
                <option value="codex">Codex</option>
                <option value="grok-build">Grok Build</option>
                <option value="cursor">Cursor</option>
                <option value="zcode">ZCode</option>
                <option value="deepseek-harness">DeepSeek Harness</option>
              </select>
            </div>
          </div>

          <div style={{ height: "180px", position: "relative", borderBottom: "1px solid var(--line)", background: "repeating-linear-gradient(to bottom, transparent 0, transparent 44px, #edf0ed 45px)" }}>
            <svg viewBox="0 0 700 170" preserveAspectRatio="none" style={{ width: "100%", height: "100%" }}>
              <defs>
                <linearGradient id="areaGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#b9f600" stopOpacity="0.35" />
                  <stop offset="100%" stopColor="#b9f600" stopOpacity="0" />
                </linearGradient>
              </defs>
              <path
                d="M0 145 L60 132 L115 136 L170 105 L225 116 L280 78 L340 96 L395 55 L450 71 L510 36 L570 62 L630 28 L700 42 L700 170 L0 170 Z"
                fill="url(#areaGrad)"
              />
              <polyline
                points="0,145 60,132 115,136 170,105 225,116 280,78 340,96 395,55 450,71 510,36 570,62 630,28 700,42"
                fill="none"
                stroke="#72b900"
                strokeWidth="3"
                strokeLinecap="round"
              />
            </svg>
          </div>
          <div style={{ display: "flex", justifyContent: "space-between", marginTop: "8px", fontSize: "11px", color: "var(--muted)" }}>
            <span>Jul 31</span>
            <span>Aug 07</span>
            <span>Aug 14</span>
            <span>Aug 21</span>
            <span>Aug 29</span>
          </div>
        </section>

        {/* Six Agents Quick Cards */}
        <section className="panel" aria-label="Agents Quick Status">
          <div className="panel-head">
            <div>
              <h2>{activeLanguage === "zh" ? "六 Agent 采集状态" : "6 Agents Status"}</h2>
              <p>{activeLanguage === "zh" ? "点击可快速开启或停用单个 Agent" : "Quick toggle agent telemetry"}</p>
            </div>
            <button
              type="button"
              className="btn btn-sm"
              onClick={() => setActiveTab("agents")}
            >
              {activeLanguage === "zh" ? "矩阵管理 →" : "Matrix →"}
            </button>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: "12px" }}>
            {agents.map((agent) => (
              <div
                key={agent.id}
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  padding: "10px 12px",
                  background: agent.enabled ? "var(--soft)" : "#fafafa",
                  borderRadius: "10px",
                  border: "1px solid var(--line)",
                  opacity: agent.enabled ? 1 : 0.65,
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
                  <div
                    className="agent-avatar"
                    style={{
                      width: "30px",
                      height: "30px",
                      fontSize: "11px",
                      background: agent.enabled ? "#111512" : "#9aa19a",
                    }}
                  >
                    {agent.name.slice(0, 2).toUpperCase()}
                  </div>
                  <div>
                    <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
                      <strong style={{ fontSize: "13px" }}>{agent.name}</strong>
                      <span className={`tag ${agent.status === "ACTIVE" ? "tag-lime" : agent.status === "CONFIGURING" ? "tag-warning" : ""}`} style={{ fontSize: "10px", padding: "0 6px" }}>
                        {agent.status}
                      </span>
                    </div>
                    <small style={{ color: "var(--muted)", fontSize: "11px" }}>
                      {agent.todayTokens > 0
                        ? `${(agent.todayTokens / 1000).toFixed(0)}k tokens today · ${agent.accuracy}`
                        : `${agent.lastActive} · ${agent.accuracy}`}
                    </small>
                  </div>
                </div>

                <button
                  type="button"
                  className={`switch-toggle ${agent.enabled ? "on" : ""}`}
                  onClick={() => void toggleAgent(agent.id).catch(() => undefined)}
                  aria-label={`Toggle ${agent.name}`}
                >
                  <div className="switch-handle" />
                </button>
              </div>
            ))}
          </div>
        </section>
      </div>

      {/* Lower Grid: Heatmap, Top Skills, Sync Status */}
      <div className="dashboard-lower-grid">
        {/* Heatmap */}
        <section className="panel" aria-label="Activity Heatmap">
          <div className="panel-head">
            <div>
              <h2>{activeLanguage === "zh" ? "活跃日历" : "Activity Heatmap"}</h2>
              <p>{activeLanguage === "zh" ? "近 10 周跨 Agent 创造频率" : "Past 10 weeks token activity"}</p>
            </div>
            <span className="status-indicator">
              <span className="status-dot" />
              {activeLanguage === "zh" ? `${metrics.streakDays} 天连续活跃` : `${metrics.streakDays}d streak`}
            </span>
          </div>
          <div className="heatmap-grid">
            {heatmapSquares.map((cls, idx) => (
              <div key={idx} className={`heatmap-cell ${cls}`} title={`Day ${idx + 1}`} />
            ))}
          </div>
        </section>

        {/* Top Skills */}
        <section className="panel" aria-label="Top Skills">
          <div className="panel-head">
            <div>
              <h2>{activeLanguage === "zh" ? "Skill 排行榜" : "Top Skills"}</h2>
              <p>{activeLanguage === "zh" ? "个人高频能力调用" : "Most invoked skills"}</p>
            </div>
            <span className="tag tag-lime">Top 5</span>
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: "10px" }}>
            {skills.slice(0, 3).map((sk, idx) => (
              <div
                key={sk.id}
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  padding: "8px 10px",
                  background: "var(--soft)",
                  borderRadius: "8px",
                  fontSize: "12px",
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
                  <div className="rank-badge" style={{ width: "22px", height: "22px", fontSize: "10px" }}>
                    {idx + 1}
                  </div>
                  <div>
                    <strong>{sk.name}</strong>
                    <div style={{ color: "var(--muted)", fontSize: "10px" }}>
                      {sk.daysUsed} {activeLanguage === "zh" ? "天使用" : "days used"} · {sk.accuracy}
                    </div>
                  </div>
                </div>
                <div style={{ textAlign: "right" }}>
                  <strong>{sk.invokeCount}</strong>
                  <div style={{ color: "var(--good)", fontSize: "10px", fontWeight: 700 }}>{sk.trend}</div>
                </div>
              </div>
            ))}
          </div>
        </section>

        {/* Sync & Outbox Status */}
        <section className="panel" aria-label="Sync & Devices">
          <div className="panel-head">
            <div>
              <h2>{activeLanguage === "zh" ? "最近同步与队列" : "Sync & Spool"}</h2>
              <p>{activeLanguage === "zh" ? "Collector 本地 WAL 状态" : "Local WAL Spool Status"}</p>
            </div>
            <span className={`tag ${isOnline ? "tag-lime" : "tag-danger"}`}>
              {isOnline ? (activeLanguage === "zh" ? "网络正常" : "Online") : (activeLanguage === "zh" ? "离线缓存中" : "Offline Spool")}
            </span>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: "10px" }}>
            <div style={{ display: "flex", justifyContent: "space-between", fontSize: "12px", padding: "6px 0", borderBottom: "1px solid var(--line)" }}>
              <span>{activeLanguage === "zh" ? "待同步事件 (Outbox)" : "Pending Outbox Events"}</span>
              <strong>{pendingCount}</strong>
            </div>
            <div style={{ display: "flex", justifyContent: "space-between", fontSize: "12px", padding: "6px 0", borderBottom: "1px solid var(--line)" }}>
              <span>{activeLanguage === "zh" ? "主设备 (Windows Studio)" : "Primary Device"}</span>
              <span style={{ color: "var(--muted)" }}>{activeLanguage === "zh" ? "刚刚" : "Just now"}</span>
            </div>
            <div style={{ display: "flex", justifyContent: "space-between", fontSize: "12px", padding: "6px 0" }}>
              <span>{activeLanguage === "zh" ? "副设备 (MacBook Pro)" : "Secondary Device"}</span>
              <span style={{ color: "var(--muted)" }}>8 min ago</span>
            </div>

            {syncFeedback && (
              <div style={{ padding: "8px", borderRadius: "6px", fontSize: "11px", background: syncFeedback.includes("成功") || syncFeedback.includes("success") ? "var(--lime-soft)" : "#fde8e5", color: syncFeedback.includes("成功") || syncFeedback.includes("success") ? "var(--lime-dark)" : "var(--danger)" }}>
                {syncFeedback}
              </div>
            )}

            <div style={{ display: "flex", gap: "8px", marginTop: "4px" }}>
              <button
                type="button"
                className="btn btn-sm btn-dark"
                style={{ flex: 1 }}
                onClick={handleManualSync}
                disabled={isSyncing}
              >
                {isSyncing ? (activeLanguage === "zh" ? "同步中..." : "Syncing...") : (activeLanguage === "zh" ? "立即触发上报" : "Sync Now")}
              </button>
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => setActiveTab("queue")}
              >
                {activeLanguage === "zh" ? "队列详情" : "Outbox"}
              </button>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
};
