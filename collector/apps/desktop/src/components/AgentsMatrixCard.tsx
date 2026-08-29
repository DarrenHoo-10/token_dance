import React from "react";
import type { AgentConfig } from "../tauri-bridge.ts";

interface AgentsMatrixCardProps {
  agents: AgentConfig[];
  onToggleAgent: (agentId: string) => void;
  lang: "zh" | "en";
}

export const AgentsMatrixCard: React.FC<AgentsMatrixCardProps> = ({
  agents,
  onToggleAgent,
  lang,
}) => {
  const isZh = lang === "zh";

  const getStatusBadgeClass = (status: string) => {
    switch (status) {
      case "ACTIVE":
        return "pill-active";
      case "CONFIGURING":
        return "pill-warning";
      case "NEEDS_PERMISSION":
        return "pill-danger";
      case "DISABLED":
      default:
        return "pill-muted";
    }
  };

  const getAccuracyBadge = (accuracy: string) => {
    switch (accuracy) {
      case "exact":
        return { label: isZh ? "精准" : "Exact", class: "tag-lime" };
      case "derived":
        return { label: isZh ? "推导" : "Derived", class: "tag-blue" };
      case "correlated":
        return { label: isZh ? "关联" : "Correlated", class: "tag-purple" };
      case "estimated":
      default:
        return { label: isZh ? "估算" : "Estimated", class: "tag-orange" };
    }
  };

  return (
    <div className="card-section">
      <div className="section-head">
        <div>
          <h2>{isZh ? "六 Agent 采集矩阵与适配器开关" : "Six Agents Matrix & Adapters"}</h2>
          <p>
            {isZh
              ? "精细化控制各 Agent 的遥测探针启用状态；各适配器独立运行在本地沙箱隔离层中"
              : "Granular control over telemetry adapters. Each adapter runs in an isolated local sandbox"}
          </p>
        </div>
        <div className="status-counter">
          <span className="count-active">{agents.filter((a) => a.enabled).length}</span>
          <span className="count-total">/ {agents.length} {isZh ? "已启用" : "Active"}</span>
        </div>
      </div>

      <div className="agents-grid">
        {agents.map((agent) => {
          const acc = getAccuracyBadge(agent.accuracy);
          return (
            <div key={agent.id} className={`agent-card ${agent.enabled ? "agent-enabled" : "agent-disabled"}`}>
              <div className="agent-card-header">
                <div className="agent-title-row">
                  <h3>{agent.name}</h3>
                  <span className={`tag ${acc.class}`}>{acc.label}</span>
                </div>
                <label className="switch">
                  <input
                    type="checkbox"
                    checked={agent.enabled}
                    onChange={() => onToggleAgent(agent.id)}
                  />
                  <span className="slider round"></span>
                </label>
              </div>

              <div className="agent-meta-row">
                <span className={`status-pill ${getStatusBadgeClass(agent.status)}`}>
                  {agent.status}
                </span>
                <span className="agent-version">
                  {agent.adapterId}@{agent.adapterVersion}
                </span>
              </div>

              <div className="agent-stats">
                <div className="agent-stat">
                  <span className="stat-label">{isZh ? "今日 Token" : "Today Tokens"}</span>
                  <span className="stat-val font-mono">
                    {agent.todayTokens > 0 ? (agent.todayTokens / 1000).toFixed(0) + "k" : "-"}
                  </span>
                </div>
                <div className="agent-stat">
                  <span className="stat-label">{isZh ? "累计 Token" : "Total Tokens"}</span>
                  <span className="stat-val font-mono">
                    {agent.totalTokens > 0 ? (agent.totalTokens / 1000000).toFixed(1) + "M" : "-"}
                  </span>
                </div>
                <div className="agent-stat">
                  <span className="stat-label">{isZh ? "最近活跃" : "Last Active"}</span>
                  <span className="stat-val">{agent.lastActive}</span>
                </div>
              </div>

              <div className="agent-sources-row">
                <span className="sources-label">{isZh ? "数据源:" : "Sources:"}</span>
                {agent.sources.map((src) => (
                  <span key={src} className="source-tag">
                    {src}
                  </span>
                ))}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};
