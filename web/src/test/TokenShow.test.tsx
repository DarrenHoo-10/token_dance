import { describe, it, expect } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import React from "react";
import { TokenShowProvider, useTokenShow } from "../context/TokenShowContext.tsx";
import { App } from "../App.tsx";

// Helper component for testing raw context state transitions directly
const TestStateDriver: React.FC = () => {
  const {
    accountStatus,
    user,
    privacy,
    agents,
    metricToggles,
    globalPaused,
    isOnline,
    devices,
    configBackups,
    outbox,
    syncLogs,
    metrics,
    leaderboard,
    login,
    register,
    completeOnboarding,
    toggleGlobalPause,
    toggleAgent,
    setAgentRuntimeStatus,
    toggleMetric,
    updatePrivacyScope,
    triggerSyncNow,
    toggleNetworkSimulation,
    revokeDevice,
    createConfigBackup,
    restoreConfigBackup,
    requestDataDeletion,
    generateSampleEnvelope,
    generateSampleBatch,
  } = useTokenShow();

  return (
    <div>
      <div data-testid="account-status">{accountStatus}</div>
      <div data-testid="user-handle">{user.handle}</div>
      <div data-testid="user-nickname">{user.nickname}</div>
      <div data-testid="privacy-public">{privacy.isPublicLeaderboard ? "public" : "private"}</div>
      <div data-testid="global-paused">{globalPaused ? "paused" : "running"}</div>
      <div data-testid="network-online">{isOnline ? "online" : "offline"}</div>
      <div data-testid="metrics-tokens">{metrics.totalTokens}</div>
      <div data-testid="leaderboard-count">{leaderboard.length}</div>
      <div data-testid="current-user-ranked">{leaderboard.some((l) => l.isCurrentUser) ? "yes" : "no"}</div>
      <div data-testid="outbox-pending-count">{outbox.filter((o) => o.deliveryStatus !== "ACKED").length}</div>
      <div data-testid="synclogs-count">{syncLogs.length}</div>
      <div data-testid="devices-active-count">{devices.filter((d) => d.status === "ACTIVE").length}</div>
      <div data-testid="backups-count">{configBackups.length}</div>

      {/* Agents status list */}
      <div data-testid="agents-list">
        {agents.map((a) => (
          <div key={a.id} data-testid={`agent-${a.id}`}>
            {a.name}:{a.status}:{a.enabled ? "enabled" : "disabled"}:{a.accuracy}:{a.setupPlanStatus}
          </div>
        ))}
      </div>

      {/* Metric toggles list */}
      <div data-testid="metric-toggles">
        {Object.entries(metricToggles).map(([k, v]) => (
          <span key={k} data-testid={`metric-${k}`}>
            {k}:{v ? "on" : "off"}
          </span>
        ))}
      </div>

      {/* Action triggers */}
      <button data-testid="btn-login" onClick={() => login("test@example.com", "pass")}>
        Login
      </button>
      <button data-testid="btn-register" onClick={() => register("newuser@example.com", "1234", "pass")}>
        Register
      </button>
      <button
        data-testid="btn-onboard-private"
        onClick={() => completeOnboarding({ nickname: "Alice Agent", handle: "alice" }, "private")}
      >
        Onboard Private
      </button>
      <button
        data-testid="btn-onboard-public"
        onClick={() => completeOnboarding({ nickname: "Bob Builder", handle: "bob" }, "public")}
      >
        Onboard Public
      </button>
      <button data-testid="btn-toggle-global-pause" onClick={toggleGlobalPause}>
        Toggle Global Pause
      </button>
      <button data-testid="btn-toggle-codex" onClick={() => toggleAgent("codex")}>
        Toggle Codex
      </button>
      <button data-testid="btn-status-zcode-active" onClick={() => setAgentRuntimeStatus("zcode", "ACTIVE")}>
        Set ZCode Active
      </button>
      <button data-testid="btn-toggle-metric-code" onClick={() => toggleMetric("code")}>
        Toggle Code Metric
      </button>
      <button data-testid="btn-toggle-metric-cost" onClick={() => toggleMetric("cost")}>
        Toggle Cost Metric
      </button>
      <button data-testid="btn-set-public-scope" onClick={() => updatePrivacyScope({ isPublicLeaderboard: true })}>
        Set Public Scope
      </button>
      <button data-testid="btn-set-private-scope" onClick={() => updatePrivacyScope({ isPublicLeaderboard: false })}>
        Set Private Scope
      </button>
      <button data-testid="btn-sync-now" onClick={() => triggerSyncNow()}>
        Sync Now
      </button>
      <button data-testid="btn-toggle-network" onClick={toggleNetworkSimulation}>
        Toggle Network
      </button>
      <button data-testid="btn-revoke-win" onClick={() => revokeDevice("dev-win-01")}>
        Revoke Win Device
      </button>
      <button data-testid="btn-create-backup" onClick={() => createConfigBackup("test-snap")}>
        Create Backup
      </button>
      <button
        data-testid="btn-restore-backup"
        onClick={() => restoreConfigBackup(configBackups[0]?.id || "")}
      >
        Restore Backup
      </button>
      <button data-testid="btn-delete-data" onClick={requestDataDeletion}>
        Delete Data
      </button>
      <button
        data-testid="btn-test-sample-envelope"
        onClick={() => {
          const env = generateSampleEnvelope("model_usage_recorded");
          const elem = document.getElementById("sample-envelope-holder");
          if (elem) elem.textContent = JSON.stringify(env);
        }}
      >
        Gen Sample Envelope
      </button>
      <button
        data-testid="btn-test-sample-batch"
        onClick={() => {
          const batch = generateSampleBatch();
          const elem = document.getElementById("sample-batch-holder");
          if (elem) elem.textContent = JSON.stringify(batch);
        }}
      >
        Gen Sample Batch
      </button>
      <div id="sample-envelope-holder" data-testid="sample-envelope-holder"></div>
      <div id="sample-batch-holder" data-testid="sample-batch-holder"></div>
    </div>
  );
};

describe("TokenShow Cross-Agent State Transition Tests", () => {
  it("initializes with default private scope, 6 agents, and standard protocol setup", () => {
    render(
      <TokenShowProvider>
        <TestStateDriver />
      </TokenShowProvider>
    );

    // Default privacy is STRICTLY PRIVATE
    expect(screen.getByTestId("account-status").textContent).toBe("active_private");
    expect(screen.getByTestId("privacy-public").textContent).toBe("private");
    expect(screen.getByTestId("current-user-ranked").textContent).toBe("no");
    expect(screen.getByTestId("global-paused").textContent).toBe("running");
    expect(screen.getByTestId("network-online").textContent).toBe("online");

    // Check all 6 Agents exist with expected capabilities and statuses
    expect(screen.getByTestId("agent-codex").textContent).toContain("Codex:ACTIVE:enabled:exact:APPLIED");
    expect(screen.getByTestId("agent-claude-code").textContent).toContain("Claude Code:ACTIVE:enabled:exact:APPLIED");
    expect(screen.getByTestId("agent-grok-build").textContent).toContain("Grok Build:ACTIVE:enabled:derived:APPLIED");
    expect(screen.getByTestId("agent-cursor").textContent).toContain("Cursor:ACTIVE:enabled:correlated:APPLIED");
    expect(screen.getByTestId("agent-zcode").textContent).toContain("ZCode:CONFIGURING:disabled:estimated:PROPOSED");
    expect(screen.getByTestId("agent-deepseek-harness").textContent).toContain("DeepSeek Harness:NEEDS_PERMISSION:disabled:derived:PROPOSED");
  });

  it("drives registration and onboarding state transitions to private and public scopes", () => {
    render(
      <TokenShowProvider>
        <TestStateDriver />
      </TokenShowProvider>
    );

    // 1. Register transitions to new
    fireEvent.click(screen.getByTestId("btn-register"));
    expect(screen.getByTestId("account-status").textContent).toBe("new");

    // 2. Complete onboarding with private choice -> active_private
    fireEvent.click(screen.getByTestId("btn-onboard-private"));
    expect(screen.getByTestId("account-status").textContent).toBe("active_private");
    expect(screen.getByTestId("user-nickname").textContent).toBe("Alice Agent");
    expect(screen.getByTestId("user-handle").textContent).toBe("alice");
    expect(screen.getByTestId("privacy-public").textContent).toBe("private");
    expect(screen.getByTestId("current-user-ranked").textContent).toBe("no");

    // 3. Complete onboarding with public choice -> active_public
    fireEvent.click(screen.getByTestId("btn-onboard-public"));
    expect(screen.getByTestId("account-status").textContent).toBe("active_public");
    expect(screen.getByTestId("privacy-public").textContent).toBe("public");
    expect(screen.getByTestId("current-user-ranked").textContent).toBe("yes");
  });

  it("drives global pause and individual agent switches", () => {
    render(
      <TokenShowProvider>
        <TestStateDriver />
      </TokenShowProvider>
    );

    // Toggle global pause -> pauses all agents and marks degraded
    fireEvent.click(screen.getByTestId("btn-toggle-global-pause"));
    expect(screen.getByTestId("global-paused").textContent).toBe("paused");
    expect(screen.getByTestId("agent-claude-code").textContent).toContain("DEGRADED");

    // Toggle global pause back -> running and active
    fireEvent.click(screen.getByTestId("btn-toggle-global-pause"));
    expect(screen.getByTestId("global-paused").textContent).toBe("running");
    expect(screen.getByTestId("agent-claude-code").textContent).toContain("ACTIVE");

    // Toggle individual agent (Codex)
    fireEvent.click(screen.getByTestId("btn-toggle-codex"));
    expect(screen.getByTestId("agent-codex").textContent).toContain("Codex:DISABLED:disabled");

    // Set ZCode runtime status directly
    fireEvent.click(screen.getByTestId("btn-status-zcode-active"));
    expect(screen.getByTestId("agent-zcode").textContent).toContain("ZCode:ACTIVE:enabled");
  });

  it("drives granular metric switches and affects upload payload generation", () => {
    render(
      <TokenShowProvider>
        <TestStateDriver />
      </TokenShowProvider>
    );

    // Initial state: code is on
    expect(screen.getByTestId("metric-code").textContent).toBe("code:on");

    // Toggle code metric off
    fireEvent.click(screen.getByTestId("btn-toggle-metric-code"));
    expect(screen.getByTestId("metric-code").textContent).toBe("code:off");

    // The shipped model-usage path uses precision-safe strings and safe source HMACs.
    fireEvent.click(screen.getByTestId("btn-test-sample-envelope"));
    const envelopeJson = JSON.parse(screen.getByTestId("sample-envelope-holder").textContent || "{}");
    expect(envelopeJson.schemaVersion).toBe("1.0");
    expect(envelopeJson.payload.type).toBe("model_usage_recorded");
    expect(envelopeJson.payload.tokens.totalTokens).toBe("8700");
    expect(envelopeJson.source.cursorHmac).toMatch(/^hmac-sha256:/);
    expect(envelopeJson.source.cursor).toBeUndefined();
    expect(JSON.stringify(envelopeJson)).not.toContain("prompt");

    // Disabling code removes the entire code event from the real batch builder.
    fireEvent.click(screen.getByTestId("btn-test-sample-batch"));
    let batchJson = JSON.parse(screen.getByTestId("sample-batch-holder").textContent || "{}");
    expect(batchJson.events.some((event: { payload: { type: string } }) => event.payload.type === "code_changed")).toBe(false);

    // Re-enabling code restores a schema-valid code event with string-safe line counts.
    fireEvent.click(screen.getByTestId("btn-toggle-metric-code"));
    fireEvent.click(screen.getByTestId("btn-test-sample-batch"));
    batchJson = JSON.parse(screen.getByTestId("sample-batch-holder").textContent || "{}");
    const codeEvent = batchJson.events.find((event: { payload: { type: string } }) => event.payload.type === "code_changed");
    expect(codeEvent.payload.addedLines).toBe("64");
  });

  it("drives offline queue accumulation and drain/sync state transitions", async () => {
    render(
      <TokenShowProvider>
        <TestStateDriver />
      </TokenShowProvider>
    );

    // 3 pending outbox items initially
    expect(screen.getByTestId("outbox-pending-count").textContent).toBe("3");
    const initialSyncLogsCount = parseInt(screen.getByTestId("synclogs-count").textContent || "0", 10);

    // Sync now -> marks outbox items ACKED and adds sync log
    fireEvent.click(screen.getByTestId("btn-sync-now"));

    await waitFor(() => {
      expect(screen.getByTestId("outbox-pending-count").textContent).toBe("0");
    });
    expect(parseInt(screen.getByTestId("synclogs-count").textContent || "0", 10)).toBe(initialSyncLogsCount + 1);
  });

  it("drives network offline simulation and blocking sync", async () => {
    render(
      <TokenShowProvider>
        <TestStateDriver />
      </TokenShowProvider>
    );

    // Toggle offline
    fireEvent.click(screen.getByTestId("btn-toggle-network"));
    expect(screen.getByTestId("network-online").textContent).toBe("offline");

    // Toggle back online
    fireEvent.click(screen.getByTestId("btn-toggle-network"));
    expect(screen.getByTestId("network-online").textContent).toBe("online");
  });

  it("drives device revocation state transitions", () => {
    render(
      <TokenShowProvider>
        <TestStateDriver />
      </TokenShowProvider>
    );

    expect(screen.getByTestId("devices-active-count").textContent).toBe("2");
    fireEvent.click(screen.getByTestId("btn-revoke-win"));
    expect(screen.getByTestId("devices-active-count").textContent).toBe("1");
  });

  it("drives config backup snapshot creation and rollback restoration", () => {
    render(
      <TokenShowProvider>
        <TestStateDriver />
      </TokenShowProvider>
    );

    const initialBackupsCount = parseInt(screen.getByTestId("backups-count").textContent || "0", 10);

    // Create a new backup
    fireEvent.click(screen.getByTestId("btn-create-backup"));
    expect(parseInt(screen.getByTestId("backups-count").textContent || "0", 10)).toBe(initialBackupsCount + 1);

    // Modify some settings
    fireEvent.click(screen.getByTestId("btn-toggle-codex"));
    fireEvent.click(screen.getByTestId("btn-set-public-scope"));
    expect(screen.getByTestId("privacy-public").textContent).toBe("public");

    // Restore baseline backup snapshot
    fireEvent.click(screen.getByTestId("btn-restore-backup"));
    expect(screen.getByTestId("privacy-public").textContent).toBe("private");
    expect(screen.getByTestId("agent-codex").textContent).toContain("Codex:ACTIVE:enabled");
  });

  it("drives GDPR data deletion state transitions and zeroing metrics", () => {
    render(
      <TokenShowProvider>
        <TestStateDriver />
      </TokenShowProvider>
    );

    expect(parseInt(screen.getByTestId("metrics-tokens").textContent || "0", 10)).toBeGreaterThan(0);
    fireEvent.click(screen.getByTestId("btn-delete-data"));

    expect(screen.getByTestId("account-status").textContent).toBe("deletion_pending");
    expect(screen.getByTestId("metrics-tokens").textContent).toBe("0");
    expect(screen.getByTestId("privacy-public").textContent).toBe("private");
    expect(screen.getByTestId("current-user-ranked").textContent).toBe("no");
  });

  it("renders full UI views, performs live interactions and modal triggers", async () => {
    render(<App />);

    // Brand and title
    expect(screen.getByText("TokenDance")).toBeInTheDocument();
    expect(screen.getByText("你的 Token 正在起舞")).toBeInTheDocument();

    // 1. Click Audit Upload button to open UploadPreviewModal
    const auditBtn = screen.getByText("🔍 审计上传字段");
    fireEvent.click(auditBtn);
    expect(screen.getByText("上传字段实时预览与白名单校验")).toBeInTheDocument();
    expect(screen.getByText("✓ 协议白名单允许上传字段：")).toBeInTheDocument();

    // Close preview modal
    fireEvent.click(screen.getByText("完成查看"));

    // 2. Navigate to Six Agents Matrix
    fireEvent.click(screen.getByText("六 Agent 状态"));
    expect(screen.getByText("六 Agent 采集矩阵与指标开关")).toBeInTheDocument();
    // Test single agent toggle inside UI
    const toggleCursor = screen.getByLabelText("Toggle agent Cursor");
    fireEvent.click(toggleCursor);

    // Test metric toggle inside UI
    const toggleCode = screen.getByLabelText("Toggle metric code");
    fireEvent.click(toggleCode);

    // 3. Navigate to Offline Queue
    fireEvent.click(screen.getByText("离线队列与上报"));
    expect(screen.getByText("离线 WAL 队列与上传字段审计")).toBeInTheDocument();
    // Click Drain & Sync Outbox button
    const drainBtn = screen.getByText("立即清空队列并上报");
    fireEvent.click(drainBtn);

    // 4. Navigate to Privacy Settings
    fireEvent.click(screen.getByText("隐私与公开范围"));
    expect(screen.getByText("排行榜公开范围与数据擦除")).toBeInTheDocument();
    expect(screen.getByText("默认仅自己可见 (Private by default)")).toBeInTheDocument();

    // Toggle public leaderboard -> triggers confirmation modal
    const leaderboardToggle = screen.getByLabelText("Toggle Public Leaderboard");
    fireEvent.click(leaderboardToggle);
    expect(screen.getByText("确认将数据加入公开排行榜？")).toBeInTheDocument();
    fireEvent.click(screen.getByText("确认公开"));

    // 5. Navigate to Devices & Backup
    fireEvent.click(screen.getByText("设备与备份"));
    expect(screen.getByText("设备绑定、撤销与配置恢复")).toBeInTheDocument();

    // Test device revocation modal
    const revokeBtn = screen.getAllByText("撤销此设备")[0];
    if (revokeBtn) {
      fireEvent.click(revokeBtn);
      expect(screen.getByText("确认撤销该设备？")).toBeInTheDocument();
      fireEvent.click(screen.getByText("确认撤销"));
    }

    // 6. Navigate to Leaderboard
    fireEvent.click(screen.getByText("社区排行榜"));
    expect(screen.getByText("TokenDance 开发者排行榜与发现")).toBeInTheDocument();
  });
});
