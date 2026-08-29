import React from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";
import type { Capability, AdapterRuntimeStatus } from "../protocol/generated.ts";

export const AgentsMatrixView: React.FC = () => {
  const {
    agents,
    toggleAgent,
    setAgentRuntimeStatus,
    metricToggles,
    toggleMetric,
    globalPaused,
    toggleGlobalPause,
    activeLanguage,
    setIsUploadPreviewOpen,
  } = useTokenShow();

  const metricLabels: Record<Capability, { nameZh: string; nameEn: string; descZh: string; descEn: string }> = {
    tokens: {
      nameZh: "Token 统计与分项",
      nameEn: "Tokens & Breakdown",
      descZh: "输入上下文、输出 Token、缓存读取/写入量统计",
      descEn: "Input context, output tokens, cache read/write stats",
    },
    code: {
      nameZh: "AI 代码变更量",
      nameEn: "Code Lines Changed",
      descZh: "新增行数、删除行数与文件计数（绝不上传代码正文）",
      descEn: "Added/deleted lines & file count (no raw code content)",
    },
    skills: {
      nameZh: "Skill 调用分析",
      nameEn: "Skill Invocations",
      descZh: "Skill 标识、公开名称与调用频次",
      descEn: "Skill keys, public names and invocation counts",
    },
    tools: {
      nameZh: "工具类别统计",
      nameEn: "Tool Categories",
      descZh: "终端执行、文件读写、网络等安全类别归属",
      descEn: "Safe tool category telemetry without parameters",
    },
    cost: {
      nameZh: "预估费用与币种",
      nameEn: "Estimated Cost",
      descZh: "根据公允模型定价推导的 USD 预估花费",
      descEn: "Estimated USD spend derived from model usage",
    },
    turns: {
      nameZh: "交互轮次与耗时",
      nameEn: "Turns & Duration",
      descZh: "请求响应轮次、耗时与成功状态",
      descEn: "Turn hashes, execution duration and status",
    },
    sessions: {
      nameZh: "会话生命周期",
      nameEn: "Session Lifecycle",
      descZh: "去标识化会话哈希、开始与结束标记",
      descEn: "De-identified session hashes & lifecycle markers",
    },
    subagents: {
      nameZh: "子 Agent 衍生追踪",
      nameEn: "Spawned Subagents",
      descZh: "父子会话衍生关系与多 Agent 协作深度",
      descEn: "Parent-child session relations and spawned agents",
    },
  };

  const statusList: AdapterRuntimeStatus[] = [
    "ACTIVE",
    "CONFIGURING",
    "NEEDS_PERMISSION",
    "DEGRADED",
    "DISABLED",
    "ERROR",
  ];

  return (
    <div className="agents-matrix-view">
      <header className="page-head">
        <div>
          <p className="eyebrow">{activeLanguage === "zh" ? "采集器插件与能力管理" : "Collector Adapters & Capabilities"}</p>
          <h1>{activeLanguage === "zh" ? "六 Agent 采集矩阵与指标开关" : "6-Agent Matrix & Metric Controls"}</h1>
          <p>
            {activeLanguage === "zh"
              ? "本地自动发现、标准化、去重并上报；支持单 Agent 开关、全局暂停与指标级白名单过滤"
              : "Local auto-discovery, normalization, de-duplication; per-agent toggles, global pause, metric filters"}
          </p>
        </div>

        <div style={{ display: "flex", gap: "10px", alignItems: "center" }}>
          {/* Global Pause Switch */}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "10px",
              padding: "8px 14px",
              background: globalPaused ? "#fff3d6" : "white",
              border: globalPaused ? "1px solid #fbd38d" : "1px solid var(--line)",
              borderRadius: "12px",
            }}
          >
            <div>
              <div style={{ fontSize: "12px", fontWeight: 750 }}>
                {globalPaused
                  ? activeLanguage === "zh" ? "⏸ 全局已暂停" : "⏸ Paused"
                  : activeLanguage === "zh" ? "▶ 采集运行中" : "▶ Running"}
              </div>
              <small style={{ color: "var(--muted)", fontSize: "10px" }}>
                {activeLanguage === "zh" ? "一键暂停所有采集与上报" : "Global pause all ingestion"}
              </small>
            </div>
            <button
              type="button"
              className={`switch-toggle ${!globalPaused ? "on" : ""}`}
              onClick={toggleGlobalPause}
              aria-label="Toggle Global Pause"
            >
              <div className="switch-handle" />
            </button>
          </div>

          <button
            type="button"
            className="btn btn-primary"
            onClick={() => setIsUploadPreviewOpen(true)}
          >
            {activeLanguage === "zh" ? "审计上传白名单" : "Audit Whitelist"}
          </button>
        </div>
      </header>

      {/* Six Agents Cards Grid */}
      <section style={{ marginBottom: "28px" }} aria-label="Six Agents Matrix">
        <h2 style={{ fontSize: "18px", marginBottom: "14px" }}>
          {activeLanguage === "zh" ? "首期六 Agent 采集适配器 (Adapter Host)" : "Six Core Agent Adapters"}
        </h2>

        <div className="agent-cards-grid">
          {agents.map((agent) => (
            <div
              key={agent.id}
              className={`agent-matrix-card ${!agent.enabled ? "disabled" : ""}`}
              style={{
                borderTop: agent.enabled ? "4px solid var(--lime)" : "4px solid #cbd5cb",
              }}
            >
              <div className="agent-header">
                <div className="agent-title-row">
                  <div
                    className="agent-avatar"
                    style={{
                      background: agent.enabled ? "#111512" : "#9aa19a",
                    }}
                  >
                    {agent.name.slice(0, 2).toUpperCase()}
                  </div>
                  <div>
                    <h3>{agent.name}</h3>
                    <p>{agent.adapterId} (v{agent.adapterVersion})</p>
                  </div>
                </div>

                <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
                  <button
                    type="button"
                    className={`switch-toggle ${agent.enabled ? "on" : ""}`}
                    onClick={() => toggleAgent(agent.id)}
                    aria-label={`Toggle agent ${agent.name}`}
                  >
                    <div className="switch-handle" />
                  </button>
                </div>
              </div>

              {/* Status & Accuracy */}
              <div style={{ display: "flex", gap: "6px", flexWrap: "wrap", alignItems: "center" }}>
                <span
                  className={`tag ${
                    agent.status === "ACTIVE"
                      ? "tag-lime"
                      : agent.status === "CONFIGURING"
                      ? "tag-warning"
                      : agent.status === "NEEDS_PERMISSION"
                      ? "tag-warning"
                      : "tag-danger"
                  }`}
                >
                  ● {agent.status}
                </span>

                <span className="tag" style={{ background: "#e8f0fe", color: "#1a73e8" }}>
                  Accuracy: {agent.accuracy}
                </span>

                <span className="tag" style={{ background: "#f0f4ef" }}>
                  Plan: {agent.setupPlanStatus}
                </span>
                <span className="tag" style={{ background: "#f0f4ef" }}>
                  Checkpoint: {agent.checkpointStatus}
                </span>
              </div>

              {/* Capabilities */}
              <div>
                <small style={{ color: "var(--muted)", fontWeight: 700, display: "block", marginBottom: "4px" }}>
                  {activeLanguage === "zh" ? "支持的能力声明 (Capabilities)：" : "Capabilities:"}
                </small>
                <div className="agent-capabilities">
                  {agent.capabilities.map((cap) => (
                    <span
                      key={cap}
                      className="tag"
                      style={{
                        background: metricToggles[cap] ? "var(--lime-soft)" : "#f0f4ef",
                        color: metricToggles[cap] ? "var(--lime-dark)" : "#888",
                        border: metricToggles[cap] ? "1px solid #c9f564" : "1px solid transparent",
                      }}
                    >
                      {cap}
                    </span>
                  ))}
                </div>
              </div>

              {/* Data Sources */}
              <div>
                <small style={{ color: "var(--muted)", fontWeight: 700, display: "block", marginBottom: "4px" }}>
                  {activeLanguage === "zh" ? "接入数据源 (Sources)：" : "Sources:"}
                </small>
                <div style={{ display: "flex", gap: "6px", flexWrap: "wrap" }}>
                  {agent.sources.map((src) => (
                    <span key={src} className="tag" style={{ background: "#eef2ee", fontSize: "10px" }}>
                      {src}
                    </span>
                  ))}
                </div>
              </div>

              {/* Meta stats */}
              <div className="agent-meta-row">
                <div className="agent-meta-item">
                  <small>{activeLanguage === "zh" ? "今日 Token" : "Today Tokens"}</small>
                  <strong>{(agent.todayTokens / 1000).toFixed(0)}k</strong>
                </div>
                <div className="agent-meta-item">
                  <small>{activeLanguage === "zh" ? "历史累计" : "Total Tokens"}</small>
                  <strong>{(agent.totalTokens / 1000000).toFixed(1)}M</strong>
                </div>
                <div className="agent-meta-item">
                  <small>{activeLanguage === "zh" ? "最后活跃" : "Last Active"}</small>
                  <strong>{agent.lastActive}</strong>
                </div>
              </div>

              {/* Runtime status test selector */}
              <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", paddingTop: "8px", borderTop: "1px solid var(--line)", fontSize: "11px" }}>
                <span style={{ color: "var(--muted)" }}>
                  {activeLanguage === "zh" ? "驱动状态转换：" : "Change Status:"}
                </span>
                <select
                  className="form-input"
                  style={{ height: "26px", fontSize: "11px", padding: "0 6px" }}
                  value={agent.status}
                  onChange={(e) => setAgentRuntimeStatus(agent.id, e.target.value as AdapterRuntimeStatus)}
                  aria-label={`Change runtime status for ${agent.name}`}
                >
                  {statusList.map((st) => (
                    <option key={st} value={st}>
                      {st}
                    </option>
                  ))}
                </select>
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* Metric Switches Section */}
      <section className="panel" aria-label="Metric Capability Switches">
        <div className="panel-head">
          <div>
            <h2>{activeLanguage === "zh" ? "指标级采集与脱敏开关" : "Metric & Capability Granular Switches"}</h2>
            <p>
              {activeLanguage === "zh"
                ? "在本地标准化层（Normalization）根据用户选择阻断未开启的指标，绝不打包入批次"
                : "Disabled metrics are dropped at the local normalization layer before batching"}
            </p>
          </div>
          <span className="tag tag-lime">
            {Object.values(metricToggles).filter(Boolean).length} / 8 {activeLanguage === "zh" ? "项已开启" : "Active"}
          </span>
        </div>

        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(280px, 1fr))", gap: "16px" }}>
          {(Object.keys(metricLabels) as Capability[]).map((cap) => {
            const meta = metricLabels[cap];
            const isEnabled = metricToggles[cap];
            return (
              <div
                key={cap}
                style={{
                  padding: "16px",
                  borderRadius: "12px",
                  border: isEnabled ? "1px solid #ccd5cc" : "1px solid var(--line)",
                  background: isEnabled ? "white" : "#fafafa",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: "12px",
                }}
              >
                <div>
                  <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
                    <strong style={{ fontSize: "13px" }}>
                      {activeLanguage === "zh" ? meta.nameZh : meta.nameEn}
                    </strong>
                    <span className="tag" style={{ fontSize: "10px", padding: "0 4px" }}>
                      {cap}
                    </span>
                  </div>
                  <p style={{ fontSize: "11px", color: "var(--muted)", marginTop: "4px" }}>
                    {activeLanguage === "zh" ? meta.descZh : meta.descEn}
                  </p>
                </div>

                <button
                  type="button"
                  className={`switch-toggle ${isEnabled ? "on" : ""}`}
                  onClick={() => toggleMetric(cap)}
                  aria-label={`Toggle metric ${cap}`}
                >
                  <div className="switch-handle" />
                </button>
              </div>
            );
          })}
        </div>
      </section>
    </div>
  );
};
