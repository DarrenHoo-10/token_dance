import React, { useEffect, useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";

export const OnboardingWizard: React.FC = () => {
  const {
    isOnboardingOpen,
    setIsOnboardingOpen,
    completeOnboarding,
    approveAdapterManifest,
    adapterManifests,
    user,
    activeLanguage,
  } = useTokenShow();
  const [step, setStep] = useState(2);
  const [nickname, setNickname] = useState("");
  const [handle, setHandle] = useState("");
  const [bio, setBio] = useState("");
  const [timezone, setTimezone] = useState("Asia/Shanghai");
  const [privacyChoice, setPrivacyChoice] = useState<"private" | "public">("private");
  const [approvingAdapter, setApprovingAdapter] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!isOnboardingOpen) return;
    setNickname(user.nickname || "");
    setHandle(user.handle || "");
    setBio(user.bio || "");
    setTimezone(user.timezone || "Asia/Shanghai");
  }, [isOnboardingOpen, user]);

  if (!isOnboardingOpen) return null;

  const allApproved = adapterManifests.length > 0 && adapterManifests.every((manifest) => manifest.approved);

  const handleApprove = async (adapterId: string) => {
    const manifest = adapterManifests.find((item) => item.adapterId === adapterId);
    if (!manifest) return;
    try {
      setError(null);
      setApprovingAdapter(adapterId);
      await approveAdapterManifest(
        adapterId,
        manifest.permissions.filter((permission) => permission.required).map((permission) => permission.id),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setApprovingAdapter(null);
    }
  };

  const handleFinish = async () => {
    try {
      setError(null);
      await completeOnboarding(
        {
          nickname,
          handle: handle.replace(/^@/, ""),
          bio,
          timezone,
          avatarText: (nickname.slice(0, 2) || "TD").toUpperCase(),
        },
        privacyChoice,
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <div className="modal-overlay" role="dialog" aria-modal="true" aria-label="Onboarding">
      <div className="modal-card modal-wide">
        <div className="modal-head">
          <div>
            <p className="eyebrow">{activeLanguage === "zh" ? "首次建档与逐 Adapter 授权" : "Onboarding & Per-adapter Authorization"}</p>
            <h2>{activeLanguage === "zh" ? "创建开发者身份并审批采集权限" : "Set Up Identity & Approve Collector Access"}</h2>
          </div>
          <button type="button" className="btn btn-sm" onClick={() => setIsOnboardingOpen(false)} aria-label="Close">✕</button>
        </div>

        <div className="modal-body">
          <div className="onboarding-steps">
            <div className="onboarding-step complete"><small>STEP 1</small><strong>{activeLanguage === "zh" ? "✓ 邮箱已验证" : "✓ Email Verified"}</strong></div>
            <div className={`onboarding-step ${step === 2 ? "active" : ""}`}><small>STEP 2</small><strong>{activeLanguage === "zh" ? "设置个人资料" : "Profile Setup"}</strong></div>
            <div className={`onboarding-step ${step === 3 ? "active" : ""}`}><small>STEP 3</small><strong>{activeLanguage === "zh" ? "逐 Adapter 审批" : "Adapter Approvals"}</strong></div>
          </div>

          {error && <div className="feedback-error" role="alert">{error}</div>}

          {step === 2 && (
            <div>
              <div className="form-row">
                <div className="form-group">
                  <label className="form-label">{activeLanguage === "zh" ? "公开昵称" : "Public Nickname"}</label>
                  <input type="text" className="form-input" value={nickname} onChange={(event) => setNickname(event.target.value)} />
                </div>
                <div className="form-group">
                  <label className="form-label">{activeLanguage === "zh" ? "唯一 Handle" : "Unique Handle"}</label>
                  <input type="text" className="form-input" value={handle} onChange={(event) => setHandle(event.target.value)} />
                </div>
              </div>
              <div className="form-group">
                <label className="form-label">{activeLanguage === "zh" ? "个人简介" : "Bio"}</label>
                <textarea className="form-input form-textarea" value={bio} onChange={(event) => setBio(event.target.value)} />
              </div>
              <div className="form-group">
                <label className="form-label">{activeLanguage === "zh" ? "统计时区" : "Timezone"}</label>
                <select className="form-input" value={timezone} onChange={(event) => setTimezone(event.target.value)}>
                  <option value="Asia/Shanghai">Asia / Shanghai (UTC+8)</option>
                  <option value="America/New_York">America / New York</option>
                  <option value="America/Los_Angeles">America / Los Angeles</option>
                  <option value="Europe/London">Europe / London</option>
                </select>
              </div>
            </div>
          )}

          {step === 3 && (
            <div>
              <h3 className="section-title">{activeLanguage === "zh" ? "1. 排行榜范围（默认私密）" : "1. Leaderboard Scope (Private by default)"}</h3>
              <div className="choice-grid">
                <button type="button" className={`choice-card ${privacyChoice === "private" ? "selected" : ""}`} onClick={() => setPrivacyChoice("private")}>
                  <strong>{activeLanguage === "zh" ? "仅自己可见（推荐）" : "Private (Recommended)"}</strong>
                  <span>{activeLanguage === "zh" ? "先私下核对真实遥测。" : "Review real telemetry privately first."}</span>
                </button>
                <button type="button" className={`choice-card ${privacyChoice === "public" ? "selected" : ""}`} onClick={() => setPrivacyChoice("public")}>
                  <strong>{activeLanguage === "zh" ? "公开摘要排行榜" : "Public Summary"}</strong>
                  <span>{activeLanguage === "zh" ? "仅公开选定聚合字段。" : "Only selected aggregates are public."}</span>
                </button>
              </div>

              <h3 className="section-title">{activeLanguage === "zh" ? "2. 逐 Adapter 审批 manifest 权限" : "2. Approve Each Adapter Manifest"}</h3>
              <div className="manifest-list">
                {adapterManifests.map((manifest) => (
                  <article key={manifest.adapterId} className="manifest-card">
                    <div className="manifest-head">
                      <div><strong>{manifest.adapterName}</strong><small>{manifest.adapterId} · v{manifest.version}</small></div>
                      <span className={`tag ${manifest.approved ? "tag-lime" : "tag-warning"}`}>{manifest.approved ? "APPROVED" : "REVIEW REQUIRED"}</span>
                    </div>
                    <ul>
                      {manifest.permissions.map((permission) => (
                        <li key={permission.id}><strong>{permission.label}</strong> — {permission.description}{permission.required ? " *" : ""}</li>
                      ))}
                    </ul>
                    <button
                      type="button"
                      className="btn btn-sm btn-dark"
                      disabled={manifest.approved || approvingAdapter !== null}
                      onClick={() => void handleApprove(manifest.adapterId)}
                    >
                      {manifest.approved
                        ? (activeLanguage === "zh" ? "已审批" : "Approved")
                        : approvingAdapter === manifest.adapterId
                          ? (activeLanguage === "zh" ? "审批中..." : "Approving...")
                          : (activeLanguage === "zh" ? `审批 ${manifest.adapterName}` : `Approve ${manifest.adapterName}`)}
                    </button>
                  </article>
                ))}
              </div>
            </div>
          )}
        </div>

        <div className="modal-foot">
          {step === 2 ? (
            <>
              <button type="button" className="btn" onClick={() => setIsOnboardingOpen(false)}>{activeLanguage === "zh" ? "取消" : "Cancel"}</button>
              <button type="button" className="btn btn-primary" onClick={() => setStep(3)} disabled={!nickname || !handle}>
                {activeLanguage === "zh" ? "下一步：逐项审批 →" : "Next: Review Permissions →"}
              </button>
            </>
          ) : (
            <>
              <button type="button" className="btn" onClick={() => setStep(2)}>{activeLanguage === "zh" ? "← 返回" : "← Back"}</button>
              <button type="button" className="btn btn-primary" onClick={() => void handleFinish()} disabled={!allApproved}>
                {allApproved
                  ? (activeLanguage === "zh" ? "完成建档" : "Complete Onboarding")
                  : (activeLanguage === "zh" ? "请先审批全部 Adapter" : "Approve every adapter first")}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
};
