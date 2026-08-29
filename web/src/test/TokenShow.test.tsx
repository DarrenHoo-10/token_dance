import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App } from "../App.tsx";
import { FetchControlPlaneClient, ControlPlaneError } from "../api/controlPlane.ts";
import { MockControlPlaneClient, createMockState } from "./MockControlPlaneClient.ts";

beforeEach(() => {
  localStorage.clear();
  window.history.replaceState({}, "", "/dashboard");
});

describe("FetchControlPlaneClient HTTP contract", () => {
  it("sends dangerous commands as JSON and returns the server ACK", async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ commandId: "cmd-42", status: "ACK" }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    const client = new FetchControlPlaneClient("https://control.example", fetcher as typeof fetch);

    await expect(client.runDangerousCommand({ type: "REVOKE_DEVICE", deviceId: "dev-win-01" })).resolves.toEqual({ commandId: "cmd-42", status: "ACK" });
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher).toHaveBeenCalledWith("https://control.example/api/control-plane/commands", expect.objectContaining({
      method: "POST",
      credentials: "include",
      body: JSON.stringify({ type: "REVOKE_DEVICE", deviceId: "dev-win-01" }),
    }));
  });

  it("surfaces non-2xx JSON failures instead of mutating local state", async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ message: "collector rejected command" }), {
      status: 409,
      headers: { "Content-Type": "application/json" },
    }));
    const client = new FetchControlPlaneClient("", fetcher as typeof fetch);

    await expect(client.setGlobalPaused(true)).rejects.toEqual(expect.objectContaining<Partial<ControlPlaneError>>({
      name: "ControlPlaneError",
      status: 409,
      message: "collector rejected command",
    }));
  });

  it("treats an unauthenticated session response as no session", async () => {
    const fetcher = vi.fn(async () => new Response(null, { status: 401 }));
    const client = new FetchControlPlaneClient("", fetcher as typeof fetch);
    await expect(client.getSession()).resolves.toBeNull();
  });
});

describe("Web control-plane flows through real UI buttons", () => {
  it("starts without a session as unauthenticated and opens account access", async () => {
    const client = new MockControlPlaneClient(createMockState(), null);
    render(<App client={client} />);

    expect(await screen.findByRole("dialog", { name: /Account Access|账户接入/i })).toBeInTheDocument();
    expect(client.calls.map((call) => call.method)).toEqual(["getSession"]);
  });

  it("registers, approves each adapter manifest separately, then completes onboarding", async () => {
    const user = userEvent.setup();
    const client = new MockControlPlaneClient(createMockState(), null);
    render(<App client={client} />);

    const authDialog = await screen.findByRole("dialog");
    await user.click(within(authDialog).getByRole("button", { name: "注册" }));
    await user.click(within(authDialog).getByRole("button", { name: "完成注册并首次建档" }));

    const onboarding = await screen.findByRole("dialog", { name: "Onboarding" });
    await user.click(within(onboarding).getByRole("button", { name: /下一步：逐项审批/ }));
    const finish = within(onboarding).getByRole("button", { name: /请先审批全部 Adapter/ });
    expect(finish).toBeDisabled();

    for (const name of ["Codex", "Claude Code", "Grok Build", "Cursor", "ZCode", "DeepSeek Harness"]) {
      await user.click(within(onboarding).getByRole("button", { name: `审批 ${name}` }));
    }
    await user.click(within(onboarding).getByRole("button", { name: "完成建档" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Onboarding" })).not.toBeInTheDocument());
    const approvals = client.calls.filter((call) => call.method === "approveAdapterManifest");
    expect(approvals).toHaveLength(6);
    expect(approvals[0].payload).toEqual({ adapterId: "adapter-codex", permissionIds: ["read-telemetry"] });
    expect(client.calls.some((call) => call.method === "completeOnboarding")).toBe(true);
  });

  it("keeps globalPaused independent from adapter runtime health and refreshes after ACK", async () => {
    const user = userEvent.setup();
    const client = new MockControlPlaneClient();
    const originalStatuses = client.state.agents.map((agent) => agent.status);
    render(<App client={client} />);

    await screen.findByText("你的 Token 正在起舞");
    await user.click(screen.getByRole("button", { name: "六 Agent 状态" }));
    await user.click(await screen.findByRole("button", { name: "Toggle Global Pause" }));

    await waitFor(() => expect(screen.getByText("⏸ 全局已暂停")).toBeInTheDocument());
    expect(client.state.globalPaused).toBe(true);
    expect(client.state.agents.map((agent) => agent.status)).toEqual(originalStatuses);
    expect(client.calls.slice(-2).map((call) => call.method)).toEqual(["setGlobalPaused", "getState"]);
  });

  it("runs device revocation as command → ACK → authoritative refresh and clears selection", async () => {
    const user = userEvent.setup();
    const client = new MockControlPlaneClient();
    render(<App client={client} />);

    await screen.findByText("你的 Token 正在起舞");
    await user.click(screen.getByRole("button", { name: "设备与备份" }));
    const buttons = await screen.findAllByRole("button", { name: "撤销此设备" });
    await user.click(buttons[0]);
    await user.click(screen.getByRole("button", { name: "确认撤销" }));

    await waitFor(() => expect(screen.queryByText("确认撤销该设备？")).not.toBeInTheDocument());
    expect(client.calls.slice(-2)).toEqual([
      { method: "runDangerousCommand", payload: { type: "REVOKE_DEVICE", deviceId: "dev-win-01" } },
      { method: "getState", payload: undefined },
    ]);
    expect(client.state.devices.find((device) => device.id === "dev-win-01")?.status).toBe("REVOKED");
  });

  it("shows deletion jobs with REQUESTED status after the dangerous command ACK", async () => {
    const user = userEvent.setup();
    const client = new MockControlPlaneClient();
    render(<App client={client} />);

    await screen.findByText("你的 Token 正在起舞");
    await user.click(screen.getByRole("button", { name: "隐私与公开范围" }));
    await user.click(await screen.findByRole("button", { name: "请求删除" }));
    await user.type(screen.getByPlaceholderText("delete"), "delete");
    await user.click(screen.getByRole("button", { name: "确认永久删除" }));

    expect(await screen.findByText(/删除任务状态: REQUESTED/)).toBeInTheDocument();
    expect(client.state.deletionJob?.status).toBe("REQUESTED");
  });

  it("previews the recent authoritative batch instead of generating random samples", async () => {
    const user = userEvent.setup();
    const client = new MockControlPlaneClient();
    render(<App client={client} />);

    await screen.findByText("你的 Token 正在起舞");
    await user.click(screen.getByRole("button", { name: "🔍 审计上传字段" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/最近真实上传批次/)).toBeInTheDocument();
    expect(within(dialog).getByText(new RegExp(client.state.recentBatch!.batchId))).toBeInTheDocument();
    expect(client.calls.some((call) => call.method === "getRecentBatch")).toBe(true);
  });

  it("uses URL routes and reloads persistent state from the control plane", async () => {
    const user = userEvent.setup();
    const client = new MockControlPlaneClient();
    const first = render(<App client={client} />);

    await screen.findByText("你的 Token 正在起舞");
    await user.click(screen.getByRole("button", { name: "离线队列与上报" }));
    expect(window.location.pathname).toBe("/queue");
    expect(await screen.findByText("离线 WAL 队列与真实批次审计")).toBeInTheDocument();
    first.unmount();

    render(<App client={client} />);
    expect(await screen.findByText("离线 WAL 队列与真实批次审计")).toBeInTheDocument();
    expect(client.calls.filter((call) => call.method === "getState").length).toBeGreaterThanOrEqual(2);
  });

  it("renders a failed command response without claiming success", async () => {
    const user = userEvent.setup();
    const client = new MockControlPlaneClient();
    client.failNext("runDangerousCommand", new Error("revocation denied"));
    render(<App client={client} />);

    await screen.findByText("你的 Token 正在起舞");
    await user.click(screen.getByRole("button", { name: "设备与备份" }));
    await user.click((await screen.findAllByRole("button", { name: "撤销此设备" }))[0]);
    await user.click(screen.getByRole("button", { name: "确认撤销" }));

    expect((await screen.findAllByText(/revocation denied/)).length).toBeGreaterThan(0);
    expect(client.state.devices[0].status).toBe("ACTIVE");
  });
});
