import React from "react";
import { useTokenShow } from "../context/TokenShowContext.tsx";

export const Navbar: React.FC = () => {
  const {
    user,
    accountStatus,
    activeTab,
    setActiveTab,
    activeLanguage,
    setActiveLanguage,
    globalPaused,
    isOnline,
    setIsAuthModalOpen,
    setIsOnboardingOpen,
  } = useTokenShow();

  return (
    <header className="navbar">
      <div className="brand" onClick={() => setActiveTab("dashboard")}>
        <div className="brand-icon">TD</div>
        <span>TokenDance</span>
      </div>

      <nav className="nav-tabs">
        <button
          type="button"
          className={`nav-tab ${activeTab === "dashboard" ? "active" : ""}`}
          onClick={() => setActiveTab("dashboard")}
        >
          {activeLanguage === "zh" ? "个人总览" : "TokenBoard"}
        </button>
        <button
          type="button"
          className={`nav-tab ${activeTab === "agents" ? "active" : ""}`}
          onClick={() => setActiveTab("agents")}
        >
          {activeLanguage === "zh" ? "六 Agent 状态" : "Agents Matrix"}
        </button>
        <button
          type="button"
          className={`nav-tab ${activeTab === "queue" ? "active" : ""}`}
          onClick={() => setActiveTab("queue")}
        >
          {activeLanguage === "zh" ? "离线队列与上报" : "Offline Outbox"}
        </button>
        <button
          type="button"
          className={`nav-tab ${activeTab === "privacy" ? "active" : ""}`}
          onClick={() => setActiveTab("privacy")}
        >
          {activeLanguage === "zh" ? "隐私与公开范围" : "Privacy & Scope"}
        </button>
        <button
          type="button"
          className={`nav-tab ${activeTab === "devices" ? "active" : ""}`}
          onClick={() => setActiveTab("devices")}
        >
          {activeLanguage === "zh" ? "设备与备份" : "Devices & Backup"}
        </button>
        <button
          type="button"
          className={`nav-tab ${activeTab === "leaderboard" ? "active" : ""}`}
          onClick={() => setActiveTab("leaderboard")}
        >
          {activeLanguage === "zh" ? "社区排行榜" : "Leaderboard"}
        </button>
      </nav>

      <div className="nav-actions">
        {globalPaused && (
          <span className="tag tag-warning" title="全局数据采集已暂停">
            {activeLanguage === "zh" ? "已全局暂停" : "Paused"}
          </span>
        )}

        {!isOnline && (
          <span className="tag tag-danger" title="离线模式：事件写入本地 WAL Spool">
            {activeLanguage === "zh" ? "离线缓存" : "Offline"}
          </span>
        )}

        <div className="lang-switch">
          <button
            type="button"
            className={`lang-btn ${activeLanguage === "zh" ? "active" : ""}`}
            onClick={() => setActiveLanguage("zh")}
          >
            中文
          </button>
          <button
            type="button"
            className={`lang-btn ${activeLanguage === "en" ? "active" : ""}`}
            onClick={() => setActiveLanguage("en")}
          >
            EN
          </button>
        </div>

        {accountStatus === "unauthenticated" ? (
          <button
            type="button"
            className="btn btn-dark btn-sm"
            onClick={() => setIsAuthModalOpen(true)}
          >
            {activeLanguage === "zh" ? "登录 / 注册" : "Sign In"}
          </button>
        ) : (
          <button
            type="button"
            className="user-avatar-btn"
            onClick={() => {
              if (accountStatus === "new") {
                setIsOnboardingOpen(true);
              } else {
                setActiveTab("privacy");
              }
            }}
          >
            <div className="avatar-circle">{user.avatarText || "TD"}</div>
            <span className="avatar-name">@{user.handle}</span>
          </button>
        )}
      </div>
    </header>
  );
};
