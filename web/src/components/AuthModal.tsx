import React, { useState } from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";

export const AuthModal: React.FC = () => {
  const { isAuthModalOpen, setIsAuthModalOpen, login, register, activeLanguage } = useTokenShow();
  const [tab, setTab] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("developer@tokendance.io");
  const [password, setPassword] = useState("••••••••••••");
  const [code, setCode] = useState("8848");
  const [codeSent, setCodeSent] = useState(false);
  const [errorMsg, setErrorMsg] = useState("");

  if (!isAuthModalOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email || !email.includes("@")) {
      setErrorMsg(activeLanguage === "zh" ? "请输入有效邮箱地址" : "Please enter a valid email");
      return;
    }
    setErrorMsg("");
    try {
      if (tab === "login") {
        await login(email, password);
      } else {
        await register(email, code, password);
      }
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <div className="modal-overlay" role="dialog" aria-modal="true" aria-label={activeLanguage === "zh" ? "账户接入 Account Access" : "Account Access"}>
      <div className="modal-card">
        <div className="modal-head">
          <div>
            <p className="eyebrow">{activeLanguage === "zh" ? "账户接入" : "Account Access"}</p>
            <h2>{tab === "login" ? (activeLanguage === "zh" ? "邮箱登录" : "Sign In with Email") : (activeLanguage === "zh" ? "创建新账户" : "Create Account")}</h2>
          </div>
          <button
            type="button"
            className="btn btn-sm"
            onClick={() => setIsAuthModalOpen(false)}
            aria-label="Close"
          >
            ✕
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="modal-body">
            <div style={{ display: "flex", gap: "16px", borderBottom: "1px solid var(--line)", marginBottom: "20px" }}>
              <button
                type="button"
                style={{
                  padding: "8px 4px",
                  borderBottom: tab === "login" ? "2px solid #111512" : "2px solid transparent",
                  fontWeight: tab === "login" ? 750 : 500,
                  color: tab === "login" ? "var(--ink)" : "var(--muted)",
                }}
                onClick={() => setTab("login")}
              >
                {activeLanguage === "zh" ? "登录" : "Sign In"}
              </button>
              <button
                type="button"
                style={{
                  padding: "8px 4px",
                  borderBottom: tab === "register" ? "2px solid #111512" : "2px solid transparent",
                  fontWeight: tab === "register" ? 750 : 500,
                  color: tab === "register" ? "var(--ink)" : "var(--muted)",
                }}
                onClick={() => setTab("register")}
              >
                {activeLanguage === "zh" ? "注册" : "Register"}
              </button>
            </div>

            {errorMsg && (
              <div style={{ padding: "10px", background: "#fde8e5", color: "#e9573f", borderRadius: "8px", fontSize: "12px", marginBottom: "16px" }}>
                {errorMsg}
              </div>
            )}

            <div className="form-group">
              <label className="form-label">{activeLanguage === "zh" ? "邮箱地址" : "Email Address"}</label>
              <input
                type="email"
                className="form-input"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="name@example.com"
                required
              />
            </div>

            {tab === "register" && (
              <div className="form-group">
                <label className="form-label">{activeLanguage === "zh" ? "邮箱验证码" : "Verification Code"}</label>
                <div style={{ display: "flex", gap: "8px" }}>
                  <input
                    type="text"
                    className="form-input"
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    placeholder="4-6 位数字"
                    style={{ flex: 1 }}
                    required
                  />
                  <button
                    type="button"
                    className="btn btn-sm"
                    onClick={() => setCodeSent(true)}
                  >
                    {codeSent ? (activeLanguage === "zh" ? "已发送 (59s)" : "Sent (59s)") : (activeLanguage === "zh" ? "获取验证码" : "Send Code")}
                  </button>
                </div>
              </div>
            )}

            <div className="form-group">
              <label className="form-label">{activeLanguage === "zh" ? "密码" : "Password"}</label>
              <input
                type="password"
                className="form-input"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••••••"
                required
              />
            </div>

            <div className="security-audit-box allowed" style={{ marginTop: "16px" }}>
              <strong>{activeLanguage === "zh" ? "隐私与安全保证：" : "Privacy & Safety Guarantee:"}</strong>
              <p style={{ marginTop: "4px" }}>
                {activeLanguage === "zh"
                  ? "TokenDance 仅接收去标识化的 Token、代码行数与工具调用聚合数据。绝不触碰 Prompt、模型回复、代码正文或系统凭据。"
                  : "TokenDance only collects de-identified aggregate telemetry. Never touches prompts, model completions, raw code or secrets."}
              </p>
            </div>
          </div>

          <div className="modal-foot">
            <button
              type="button"
              className="btn"
              onClick={() => setIsAuthModalOpen(false)}
            >
              {activeLanguage === "zh" ? "取消" : "Cancel"}
            </button>
            <button type="submit" className="btn btn-primary">
              {tab === "login"
                ? activeLanguage === "zh" ? "登录 TokenDance" : "Sign In"
                : activeLanguage === "zh" ? "完成注册并首次建档" : "Register & Onboard"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
