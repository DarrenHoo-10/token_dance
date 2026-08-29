import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { Capability, EventEnvelope, UploadAck, UploadBatch } from "../protocol/generated.ts";
import type {
  AccountStatus,
  AdapterManifest,
  AgentInfo,
  CollectorDevice,
  ConfigBackup,
  DeletionJob,
  LeaderboardEntry,
  OutboxItem,
  PersonalMetrics,
  PrivacyScopeSettings,
  SkillUsage,
  SyncLogEntry,
  UserProfile,
} from "../types.ts";
import {
  FetchControlPlaneClient,
  type AppTab,
  type ControlPlaneClient,
  type ControlPlaneState,
} from "../api/controlPlane.ts";

const EMPTY_PROFILE: UserProfile = {
  id: "",
  email: "",
  nickname: "",
  handle: "",
  bio: "",
  avatarText: "TD",
  timezone: "Asia/Shanghai",
  language: "zh",
  createdAt: "",
};

const EMPTY_PRIVACY: PrivacyScopeSettings = {
  isPublicLeaderboard: false,
  showTokenTotals: false,
  showTokenTrends: false,
  showAgentBreakdown: false,
  showActivityHeatmap: false,
  showTopSkills: false,
  showAICodeLines: false,
};

const EMPTY_METRICS: PersonalMetrics = {
  estimatedCost: 0,
  totalTokens: 0,
  inputContextTokens: 0,
  outputTokens: 0,
  cacheTokens: 0,
  cacheHitRate: 0,
  codeLinesAdded: 0,
  codeLinesDeleted: 0,
  tokensPerLine: 0,
  totalHours: 0,
  totalSessions: 0,
  totalTurns: 0,
  userMessages: 0,
  globalRank: 0,
  streakDays: 0,
};

const EMPTY_TOGGLES: Record<Capability, boolean> = {
  tokens: false,
  sessions: false,
  turns: false,
  tools: false,
  skills: false,
  code: false,
  cost: false,
  subagents: false,
};

const EMPTY_STATE: ControlPlaneState = {
  accountStatus: "unauthenticated",
  user: EMPTY_PROFILE,
  privacy: EMPTY_PRIVACY,
  agents: [],
  adapterManifests: [],
  metricToggles: EMPTY_TOGGLES,
  globalPaused: false,
  isOnline: true,
  devices: [],
  configBackups: [],
  outbox: [],
  syncLogs: [],
  metrics: EMPTY_METRICS,
  skills: [],
  leaderboard: [],
  recentBatch: null,
  deletionJob: null,
};

const TAB_PATHS: Record<AppTab, string> = {
  dashboard: "/dashboard",
  agents: "/agents",
  queue: "/queue",
  privacy: "/privacy",
  devices: "/devices",
  leaderboard: "/leaderboard",
};

const tabFromPath = (pathname: string): AppTab => {
  const entry = (Object.entries(TAB_PATHS) as [AppTab, string][]).find(([, path]) => path === pathname);
  return entry?.[0] ?? "dashboard";
};

export interface TokenShowContextValue {
  accountStatus: AccountStatus;
  user: UserProfile;
  privacy: PrivacyScopeSettings;
  agents: AgentInfo[];
  adapterManifests: AdapterManifest[];
  metricToggles: Record<Capability, boolean>;
  globalPaused: boolean;
  isOnline: boolean;
  devices: CollectorDevice[];
  configBackups: ConfigBackup[];
  outbox: OutboxItem[];
  syncLogs: SyncLogEntry[];
  metrics: PersonalMetrics;
  skills: SkillUsage[];
  leaderboard: LeaderboardEntry[];
  recentBatch: UploadBatch | null;
  deletionJob: DeletionJob | null;
  loading: boolean;
  error: string | null;
  isAuthModalOpen: boolean;
  isOnboardingOpen: boolean;
  isUploadPreviewOpen: boolean;
  activeLanguage: "zh" | "en";
  activeTab: AppTab;
  setActiveTab: (tab: AppTab) => void;
  setActiveLanguage: (lang: "zh" | "en") => void;
  setIsAuthModalOpen: (open: boolean) => void;
  setIsOnboardingOpen: (open: boolean) => void;
  setIsUploadPreviewOpen: (open: boolean) => void;
  login: (email: string, password?: string) => Promise<boolean>;
  register: (email: string, code: string, password?: string) => Promise<boolean>;
  approveAdapterManifest: (adapterId: string, permissionIds: string[]) => Promise<void>;
  completeOnboarding: (profile: Partial<UserProfile>, privacyChoice: "private" | "public") => Promise<void>;
  toggleGlobalPause: () => Promise<void>;
  toggleAgent: (agentId: string, enabled?: boolean) => Promise<void>;
  toggleMetric: (metric: Capability, enabled?: boolean) => Promise<void>;
  updatePrivacyScope: (updates: Partial<PrivacyScopeSettings>) => Promise<void>;
  triggerSyncNow: () => Promise<UploadAck>;
  refreshRecentBatch: () => Promise<void>;
  revokeDevice: (deviceId: string) => Promise<void>;
  createConfigBackup: (description?: string) => Promise<void>;
  restoreConfigBackup: (backupId: string) => Promise<void>;
  requestDataDeletion: () => Promise<void>;
  recentEnvelope: (eventType?: string) => EventEnvelope | null;
}

const TokenShowContext = createContext<TokenShowContextValue | null>(null);

export const TokenShowProvider: React.FC<{
  children: React.ReactNode;
  client?: ControlPlaneClient;
}> = ({ children, client: injectedClient }) => {
  const client = useMemo(() => injectedClient ?? new FetchControlPlaneClient(), [injectedClient]);
  const [state, setState] = useState<ControlPlaneState>(EMPTY_STATE);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isAuthModalOpen, setIsAuthModalOpen] = useState(false);
  const [isOnboardingOpen, setIsOnboardingOpen] = useState(false);
  const [isUploadPreviewOpen, setIsUploadPreviewOpen] = useState(false);
  const [activeLanguage, setActiveLanguageState] = useState<"zh" | "en">(
    () => (localStorage.getItem("tokendance.language") as "zh" | "en" | null) ?? "zh",
  );
  const [activeTab, setActiveTabState] = useState<AppTab>(() => tabFromPath(window.location.pathname));

  const refreshState = useCallback(async () => {
    const authoritative = await client.getState();
    setState(authoritative);
    setIsOnboardingOpen(authoritative.accountStatus === "new");
    return authoritative;
  }, [client]);

  useEffect(() => {
    let active = true;
    const bootstrap = async () => {
      try {
        const session = await client.getSession();
        if (!active) return;
        if (!session) {
          setState(EMPTY_STATE);
          setIsAuthModalOpen(true);
          return;
        }
        const authoritative = await client.getState();
        if (!active) return;
        setState(authoritative);
        setIsOnboardingOpen(authoritative.accountStatus === "new");
      } catch (err) {
        if (active) setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (active) setLoading(false);
      }
    };
    void bootstrap();
    return () => {
      active = false;
    };
  }, [client]);

  useEffect(() => {
    if (!Object.values(TAB_PATHS).includes(window.location.pathname)) {
      window.history.replaceState({}, "", TAB_PATHS.dashboard);
    }
    const handlePopState = () => setActiveTabState(tabFromPath(window.location.pathname));
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  const setActiveTab = useCallback((tab: AppTab) => {
    setActiveTabState(tab);
    const path = TAB_PATHS[tab];
    if (window.location.pathname !== path) window.history.pushState({}, "", path);
  }, []);

  const setActiveLanguage = useCallback((lang: "zh" | "en") => {
    localStorage.setItem("tokendance.language", lang);
    setActiveLanguageState(lang);
  }, []);

  const runAndRefresh = useCallback(
    async (command: () => Promise<unknown>) => {
      setError(null);
      try {
        await command();
        await refreshState();
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        setError(message);
        throw err;
      }
    },
    [refreshState],
  );

  const login = useCallback(async (email: string, password?: string) => {
    await client.login(email, password);
    const authoritative = await refreshState();
    setIsAuthModalOpen(false);
    setIsOnboardingOpen(authoritative.accountStatus === "new");
    return true;
  }, [client, refreshState]);

  const register = useCallback(async (email: string, code: string, password?: string) => {
    await client.register(email, code, password);
    await refreshState();
    setIsAuthModalOpen(false);
    setIsOnboardingOpen(true);
    return true;
  }, [client, refreshState]);

  const approveAdapterManifest = useCallback(async (adapterId: string, permissionIds: string[]) => {
    await runAndRefresh(() => client.approveAdapterManifest(adapterId, permissionIds));
  }, [client, runAndRefresh]);

  const completeOnboarding = useCallback(async (profile: Partial<UserProfile>, privacyChoice: "private" | "public") => {
    await runAndRefresh(() => client.completeOnboarding(profile, privacyChoice));
    setIsOnboardingOpen(false);
  }, [client, runAndRefresh]);

  const toggleGlobalPause = useCallback(async () => {
    await runAndRefresh(() => client.setGlobalPaused(!state.globalPaused));
  }, [client, runAndRefresh, state.globalPaused]);

  const toggleAgent = useCallback(async (agentId: string, enabled?: boolean) => {
    const agent = state.agents.find((item) => item.id === agentId);
    if (!agent) return;
    await runAndRefresh(() => client.setAgentEnabled(agentId, enabled ?? !agent.enabled));
  }, [client, runAndRefresh, state.agents]);

  const toggleMetric = useCallback(async (metric: Capability, enabled?: boolean) => {
    await runAndRefresh(() => client.setMetricEnabled(metric, enabled ?? !state.metricToggles[metric]));
  }, [client, runAndRefresh, state.metricToggles]);

  const updatePrivacyScope = useCallback(async (updates: Partial<PrivacyScopeSettings>) => {
    await runAndRefresh(() => client.updatePrivacyScope(updates));
  }, [client, runAndRefresh]);

  const triggerSyncNow = useCallback(async () => {
    if (!state.recentBatch) throw new Error("没有可上报的权威批次");
    const ack = await client.syncNow(state.recentBatch);
    await refreshState();
    return ack;
  }, [client, refreshState, state.recentBatch]);

  const refreshRecentBatch = useCallback(async () => {
    const recentBatch = await client.getRecentBatch();
    setState((current) => ({ ...current, recentBatch }));
  }, [client]);

  const revokeDevice = useCallback(async (deviceId: string) => {
    await runAndRefresh(() => client.runDangerousCommand({ type: "REVOKE_DEVICE", deviceId }));
  }, [client, runAndRefresh]);

  const createConfigBackup = useCallback(async (description?: string) => {
    await runAndRefresh(() => client.createConfigBackup(description));
  }, [client, runAndRefresh]);

  const restoreConfigBackup = useCallback(async (backupId: string) => {
    await runAndRefresh(() => client.runDangerousCommand({ type: "RESTORE_CONFIG", backupId }));
  }, [client, runAndRefresh]);

  const requestDataDeletion = useCallback(async () => {
    await runAndRefresh(() => client.runDangerousCommand({ type: "REQUEST_DATA_DELETION" }));
  }, [client, runAndRefresh]);

  const recentEnvelope = useCallback((eventType?: string) => {
    if (!state.recentBatch) return null;
    return state.recentBatch.events.find((event) => !eventType || event.payload.type === eventType) ?? null;
  }, [state.recentBatch]);

  const value = useMemo<TokenShowContextValue>(() => ({
    ...state,
    loading,
    error,
    isAuthModalOpen,
    isOnboardingOpen,
    isUploadPreviewOpen,
    activeLanguage,
    activeTab,
    setActiveTab,
    setActiveLanguage,
    setIsAuthModalOpen,
    setIsOnboardingOpen,
    setIsUploadPreviewOpen,
    login,
    register,
    approveAdapterManifest,
    completeOnboarding,
    toggleGlobalPause,
    toggleAgent,
    toggleMetric,
    updatePrivacyScope,
    triggerSyncNow,
    refreshRecentBatch,
    revokeDevice,
    createConfigBackup,
    restoreConfigBackup,
    requestDataDeletion,
    recentEnvelope,
  }), [
    state, loading, error, isAuthModalOpen, isOnboardingOpen, isUploadPreviewOpen, activeLanguage, activeTab,
    setActiveTab, setActiveLanguage, login, register, approveAdapterManifest, completeOnboarding, toggleGlobalPause,
    toggleAgent, toggleMetric, updatePrivacyScope, triggerSyncNow, refreshRecentBatch, revokeDevice, createConfigBackup,
    restoreConfigBackup, requestDataDeletion, recentEnvelope,
  ]);

  return <TokenShowContext.Provider value={value}>{children}</TokenShowContext.Provider>;
};

export const useTokenShow = (): TokenShowContextValue => {
  const context = useContext(TokenShowContext);
  if (!context) throw new Error("useTokenShow must be used within a TokenShowProvider");
  return context;
};
