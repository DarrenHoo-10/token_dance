import React, { useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";

export const OnboardingWizard: React.FC = () => {
  const { isOnboardingOpen, setIsOnboardingOpen, completeOnboarding, user, activeLanguage } = useTokenShow();
  const [step, setStep] = useState<number>(2); // 1: Email Verified, 2: Profile Setup, 3: First-time Auth & Consent
  const [nickname, setNickname] = useState(user.nickname || "Hoo Darren");
  const [handle, setHandle] = useState(user.handle || "darrenhoo");
  const [bio, setBio] = useState(user.bio || "Building intelligent agents with full telemetry & token dance.");
  const [timezone, setTimezone] = useState(user.timezone || "Asia/Shanghai");
  const [privacyChoice, setPrivacyChoice] = useState<"private" | "public">("private"); // DEFAULT IS PRIVATE

  if (!isOnboardingOpen) return null;

  const handleFinish = () => {
    completeOnboarding(
      {
        nickname,
        handle: handle.replace(/^@/, ""),
        bio,
        timezone,
        avatarText: (nickname.slice(0, 2) || "TD").toUpperCase(),
      },
      privacyChoice
    );
  };

  return (
    <div className="modal-overlay" role="dialog" aria-modal="true">
      <div className="modal-card modal-wide">
        <div className="modal-head">
          <div>
            <p className="eyebrow">{activeLanguage === "zh" ? "首次建档与跨 Agent 授权" : "Onboarding & Multi-Agent Authorization"}</p>
            <h2>{activeLanguage === "zh" ? "创建你的开发者公开身份与采集授权" : "Set Up Developer Identity & Collector"}</h2>
          </div>
          <button
            type="button"
            className="btn btn-sm"
            onClick={() => setIsOnboardingOpen(false)}
            aria-label="Close"
          >
            ✕
          </button>
        </div>

        <div className="modal-body">
          {/* Steps Progress */}
          <div style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: "12px", marginBottom: "28px" }}>
            <div style={{ padding: "12px", border: "1px solid var(--line)", borderRadius: "10px", background: "#f0f4ef" }}>
              <small style={{ color: "var(--muted)", fontWeight: 700 }}>STEP 1</small>
              <div style={{ fontWeight: 750, marginTop: "2px" }}>{activeLanguage === "zh" ? "✓ 邮箱已验证" : "✓ Email Verified"}</div>
            </div>
            <div style={{ padding: "12px", border: step === 2 ? "2px solid #111512" : "1px solid var(--line)", borderRadius: "10px", background: step === 2 ? "var(--lime-soft)" : "white" }}>
              <small style={{ color: "var(--muted)", fontWeight: 700 }}>STEP 2</small>
              <div style={{ fontWeight: 750, marginTop: "2px" }}>{activeLanguage === "zh" ? "设置个人资料" : "Profile Setup"}</div>
            </div>
            <div style={{ padding: "12px", border: step === 3 ? "2px solid #111512" : "1px solid var(--line)", borderRadius: "10px", background: step === 3 ? "var(--lime-soft)" : "white" }}>
              <small style={{ color: "var(--muted)", fontWeight: 700 }}>STEP 3</small>
              <div style={{ fontWeight: 750, marginTop: "2px" }}>{activeLanguage === "zh" ? "六 Agent 授权与公开范围" : "Agents & Privacy Scope"}</div>
            </div>
          </div>

          {step === 2 && (
            <div className="profile-step">
              <div className="form-row">
                <div className="form-group">
                  <label className="form-label">{activeLanguage === "zh" ? "公开昵称" : "Public Nickname"}</label>
                  <input
                    type="text"
                    className="form-input"
                    value={nickname}
                    onChange={(e) => setNickname(e.target.value)}
                    placeholder="e.g. Hoo Darren"
                  />
                </div>
                <div className="form-group">
                  <label className="form-label">{activeLanguage === "zh" ? "唯一 Handle" : "Unique Handle"}</label>
                  <div style={{ display: "flex", alignItems: "center" }}>
                    <span style={{ padding: "0 12px", background: "#f0f4ef", border: "1px solid var(--line)", borderRight: "none", height: "44px", display: "flex", alignItems: "center", borderTopLeftRadius: "10px", borderBottomLeftRadius: "10px", fontWeight: 700 }}>@</span>
                    <input
                      type="text"
                      className="form-input"
                      style={{ borderTopLeftRadius: 0, borderBottomLeftRadius: 0 }}
                      value={handle}
                      onChange={(e) => setHandle(e.target.value)}
                      placeholder="darrenhoo"
                    />
                  </div>
                </div>
              </div>

              <div className="form-group">
                <label className="form-label">{activeLanguage === "zh" ? "个人简介" : "Bio"}</label>
                <textarea
                  className="form-input"
                  style={{ height: "72px", paddingTop: "10px" }}
                  value={bio}
                  onChange={(e) => setBio(e.target.value)}
                  placeholder="Tell the community what you are building with AI..."
                />
              </div>

              <div className="form-row">
                <div className="form-group">
                  <label className="form-label">{activeLanguage === "zh" ? "统计时区" : "Timezone"}</label>
                  <select
                    className="form-input"
                    value={timezone}
                    onChange={(e) => setTimezone(e.target.value)}
                  >
                    <option value="Asia/Shanghai">Asia / Shanghai (UTC+8)</option>
                    <option value="America/New_York">America / New_York (UTC-5)</option>
                    <option value="America/Los_Angeles">America / Los_Angeles (UTC-8)</option>
                    <option value="Europe/London">Europe / London (UTC+0)</option>
                    <option value="Europe/Berlin">Europe / Berlin (UTC+1)</option>
                  </select>
                </div>
                <div className="form-group">
                  <label className="form-label">{activeLanguage === "zh" ? "头像占位" : "Avatar Preview"}</label>
                  <div style={{ display: "flex", alignItems: "center", gap: "12px", height: "44px" }}>
                    <div className="avatar-circle" style={{ width: "42px", height: "42px", fontSize: "14px" }}>
                      {(nickname.slice(0, 2) || "TD").toUpperCase()}
                    </div>
                    <span style={{ fontSize: "12px", color: "var(--muted)" }}>
                      {activeLanguage === "zh" ? "自动根据昵称生成品牌色头像" : "Generated brand avatar"}
                    </span>
                  </div>
                </div>
              </div>
            </div>
          )}

          {step === 3 && (
            <div className="consent-step">
              <h3 style={{ fontSize: "15px", marginBottom: "12px" }}>
                {activeLanguage === "zh" ? "1. 排行榜参与与公开范围（默认仅自己可见）" : "1. Leaderboard Scope (Default: Private)"}
              </h3>
              <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "14px", marginBottom: "24px" }}>
                <div
                  onClick={() => setPrivacyChoice("private")}
                  style={{
                    padding: "16px",
                    border: privacyChoice === "private" ? "2px solid #111512" : "1px solid var(--line)",
                    borderRadius: "12px",
                    background: privacyChoice === "private" ? "var(--lime-soft)" : "white",
                    cursor: "pointer",
                  }}
                >
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                    <strong>{activeLanguage === "zh" ? "仅自己可见 (默认 / 推荐)" : "Private (Default / Recommended)"}</strong>
                    {privacyChoice === "private" && <span className="tag tag-lime">✓ 当前选中</span>}
                  </div>
                  <p style={{ fontSize: "12px", color: "var(--muted)", marginTop: "6px" }}>
                    {activeLanguage === "zh"
                      ? "完成六 Agent 采集连接并在个人总览核对数据后，再自主选择是否加入公开排行榜。"
                      : "Review your multi-agent telemetry privately before choosing to join public leaderboards."}
                  </p>
                </div>

                <div
                  onClick={() => setPrivacyChoice("public")}
                  style={{
                    padding: "16px",
                    border: privacyChoice === "public" ? "2px solid #111512" : "1px solid var(--line)",
                    borderRadius: "12px",
                    background: privacyChoice === "public" ? "var(--lime-soft)" : "white",
                    cursor: "pointer",
                  }}
                >
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                    <strong>{activeLanguage === "zh" ? "公开摘要排行榜" : "Public Leaderboard Summary"}</strong>
                    {privacyChoice === "public" && <span className="tag tag-lime">✓ 当前选中</span>}
                  </div>
                  <p style={{ fontSize: "12px", color: "var(--muted)", marginTop: "6px" }}>
                    {activeLanguage === "zh"
                      ? "加入全球与社区 Token 排行榜，展示个人公开主页和 Agent 百分比。"
                      : "Join global leaderboard and show public summary profile."}
                  </p>
                </div>
              </div>

              <h3 style={{ fontSize: "15px", marginBottom: "12px" }}>
                {activeLanguage === "zh" ? "2. 六 Agent 采集配置变更计划 (Setup Plans Approval)" : "2. Six Agents Setup Plan Approval"}
              </h3>
              <div style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: "10px", marginBottom: "20px" }}>
                {["Codex (OTLP)", "Claude Code (OTLP)", "Grok Build (OTLP)", "Cursor (SQLite)", "ZCode (JSONL)", "DeepSeek (OTLP)"].map((agent, idx) => (
                  <div key={idx} style={{ padding: "10px 12px", background: "var(--soft)", borderRadius: "8px", fontSize: "12px", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                    <span style={{ fontWeight: 700 }}>{agent}</span>
                    <span className="tag tag-lime">{activeLanguage === "zh" ? "计划就绪" : "Plan Ready"}</span>
                  </div>
                ))}
              </div>

              <div className="security-audit-box allowed">
                <strong>{activeLanguage === "zh" ? "数据采集白名单与严格阻断范围：" : "Data Whitelist & Strict Redaction Guarantee:"}</strong>
                <ul style={{ paddingLeft: "20px", marginTop: "6px", fontSize: "12px" }}>
                  <li>{activeLanguage === "zh" ? "✓ 允许：Token 数量、输入/输出比例、缓存命中率、代码增加/删除行数、Skill/工具调用频次" : "✓ Allowed: Token counts, input/output ratio, cache hits, code lines, skill invoke frequency"}</li>
                  <li>{activeLanguage === "zh" ? "✗ 严格阻断：禁止采集 Prompt、模型回复正文、源代码正文、本地绝对路径、API Key 或任何系统凭据" : "✗ Blocked: Prompts, completions, source code contents, local file paths, API credentials"}</li>
                </ul>
              </div>
            </div>
          )}
        </div>

        <div className="modal-foot">
          {step === 2 ? (
            <>
              <button
                type="button"
                className="btn"
                onClick={() => setIsOnboardingOpen(false)}
              >
                {activeLanguage === "zh" ? "取消" : "Cancel"}
              </button>
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => setStep(3)}
              >
                {activeLanguage === "zh" ? "下一步：授权与公开范围 →" : "Next: Authorization & Scope →"}
              </button>
            </>
          ) : (
            <>
              <button
                type="button"
                className="btn"
                onClick={() => setStep(2)}
              >
                {activeLanguage === "zh" ? "← 返回上一步" : "← Back"}
              </button>
              <button
                type="button"
                className="btn btn-primary"
                onClick={handleFinish}
              >
                {activeLanguage === "zh" ? "完成建档并激活采集器" : "Complete & Activate Collector"}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
};
