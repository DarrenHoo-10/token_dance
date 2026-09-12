import { api } from '@/api/client';

export type TeamRole = 'owner' | 'admin' | 'member';
export type TeamStatus = 'active' | 'dissolved';
export type MetricValueState = 'available' | 'empty' | 'unsupported' | 'not_shared';
export type InviteLinkState = 'active' | 'revoked' | 'expired' | 'exhausted';
export type InvitationStatus = 'pending' | 'accepted' | 'revoked' | 'expired';
export type DeliveryState = 'pending' | 'sent' | 'failed' | 'cancelled';
export type AnalysisState = 'ready' | 'updating';
export type ExportKind = 'daily' | 'agents' | 'models' | 'members';
export type TeamExportStatus = 'queued' | 'running' | 'completed' | 'revoked' | 'failed' | 'expired';
export type ComparisonReason = 'membership_changed' | 'insufficient_history' | 'no_baseline';
export type AnalysisCollection = 'agents' | 'models' | 'contributions';

export interface SharingFlags {
  base: boolean;
  named: boolean;
  classification: boolean;
  cost: boolean;
}

export const EMPTY_SHARING: SharingFlags = {
  base: false,
  named: false,
  classification: false,
  cost: false,
};

export const TEAM_JOIN_SHARING: SharingFlags = {
  base: true,
  named: true,
  classification: false,
  cost: false,
};

export interface TeamPermissions {
  inviteMembers: boolean;
  assignAdmins: boolean;
  editProfile: boolean;
  exportAnalytics: boolean;
  transferOwnership: boolean;
  dissolve: boolean;
  leave: boolean;
}

export interface Team {
  id: string;
  name: string;
  description: string;
  timezone: string;
  visibility: 'private';
  status: TeamStatus;
  profileVersion: string;
  authRevision: string;
  avatarUrl?: string | null;
  memberCount?: string;
  createdAt?: string;
}

export interface Membership {
  id: string;
  role: TeamRole;
  sharingVersion: string;
  joinedAt: string;
}

export interface TeamScope {
  team: Team;
  membership: Membership;
  permissions: TeamPermissions;
}

export interface AcceptTeamResult extends TeamScope {
  alreadyMember?: boolean;
}

export type MeTeamResponse = { team: null } | TeamScope;

export interface CreateTeamRequest {
  name: string;
  description?: string;
  timezone: string;
  sharing?: SharingFlags;
}

export interface UpdateTeamRequest {
  name?: string;
  description?: string;
  expectedProfileVersion: string;
}

export interface TeamInboxInvitation {
  id: string;
  team: { id: string; name: string; description?: string };
  inviterDisplayName: string;
  invitedRole: Exclude<TeamRole, 'owner'>;
  createdAt: string;
  expiresAt: string;
  version: string;
  status: InvitationStatus;
}

export interface InvitationPreview {
  id: string;
  team: { id: string; name: string; description?: string };
  inviterDisplayName: string;
  invitedRole: Exclude<TeamRole, 'owner'>;
  createdAt?: string;
  expiresAt: string;
  version: string;
  status: InvitationStatus;
}

export interface ManagedInvitation {
  id: string;
  recipientMasked: string;
  invitedRole: Exclude<TeamRole, 'owner'>;
  createdAt: string;
  expiresAt: string;
  version: string;
  status: InvitationStatus;
  deliveryState: DeliveryState;
}

export interface InviteLink {
  id: string;
  version: string;
  role: 'member';
  expiresAt: string;
  maxUses: string;
  usedCount: string;
  effectiveState: InviteLinkState;
  creatorDisplayName?: string;
  createdAt?: string;
  shareUrl?: string;
}

export interface InviteLinkPreview {
  teamName: string;
  inviterDisplayName: string;
  role: 'member';
  expiresAt: string;
  linkVersion: string;
  effectiveState: InviteLinkState;
  alreadyMember?: boolean;
  removedPreviously?: boolean;
  currentOtherTeam?: { id: string; name: string } | null;
}

export interface SharingDimensionState {
  enabled: boolean;
  effectiveFrom?: string | null;
}

export interface MySharingResponse {
  membershipId: string;
  sharingVersion: string;
  authRevision: string;
  sharing: SharingFlags;
  effectiveFrom?: {
    base?: string | null;
    named?: string | null;
    classification?: string | null;
    cost?: string | null;
  };
}

export interface MetricValue {
  value: string | null;
  state: MetricValueState;
}

export interface CostAmount {
  currency: string;
  amount: string;
}

export interface CostCoverage {
  reportedUsageEvents: string;
  eligibleUsageEvents: string;
  estimatedUsageEvents?: string;
}

export interface AnalysisSnapshot {
  id: string;
  authRevision: string;
  sourceRevision: string;
  ruleVersion: string;
  asOf: string;
  refreshing: boolean;
}

export interface AnalysisRange {
  timezone: string;
  from: string;
  toExclusive: string;
  dataToExclusive: string;
}

export interface AnalysisFilters {
  agent: string | null;
  provider: string | null;
  model: string | null;
}

export interface AnalysisSummary {
  tokens: MetricValue;
  activeMembers: string;
  currentMembers: string;
  currentSharingMembers: string;
  comparison: { tokensDelta?: string; tokensDeltaPct?: string } | null;
  comparisonReason?: ComparisonReason | null;
}

export interface AnalysisCosts {
  reported: CostAmount[];
  estimatedUncovered: CostAmount[];
  coverage: CostCoverage;
  unattributedCostCount: string;
}

export interface AnalysisTrendPoint {
  date: string;
  tokens: MetricValue;
}

export interface AnalysisBucketItem {
  id: string;
  label: string;
  bucketType?: 'agent' | 'model' | 'unshared_classification' | 'unknown' | 'other';
  tokens: MetricValue;
  share?: string | null;
  usageEvents?: string | null;
  costType?: 'reported' | 'estimated' | 'none';
  coverage?: string | null;
}

export interface ContributionItem {
  membershipId: string;
  displayName: string;
  handle: string | null;
  rank: string;
  tokens: MetricValue;
  namedShare?: boolean;
}

export interface CursorPage<T> {
  items: T[];
  nextCursor: string | null;
}

export interface AnalysisQuality {
  hasLegacyAggregates?: boolean;
  unsupportedEvents: string;
  estimatedEvents: string;
}

export interface TeamAnalysisReady {
  state: 'ready';
  snapshot: AnalysisSnapshot;
  range: AnalysisRange;
  filters: AnalysisFilters;
  summary: AnalysisSummary;
  costs: AnalysisCosts;
  trend: AnalysisTrendPoint[];
  agents: CursorPage<AnalysisBucketItem>;
  models: CursorPage<AnalysisBucketItem>;
  contributions: CursorPage<ContributionItem>;
  quality: AnalysisQuality;
  filtersHash?: string;
}

export interface TeamAnalysisUpdating {
  state: 'updating';
  authRevision: string;
  retryAfterMs?: number;
  messageKey?: string;
}

export type TeamAnalysisResponse = TeamAnalysisReady | TeamAnalysisUpdating;

export interface TeamMember {
  membershipId: string;
  userId: string;
  displayName: string;
  handle: string | null;
  role: TeamRole;
  joinedAt: string;
  sharing: SharingFlags;
  syncStatus: 'not_shared' | 'waiting' | 'healthy' | 'delayed';
  lastReceivedAt?: string | null;
  periodTokens?: MetricValue | null;
  canOpenDetail: boolean;
}

export interface MemberDetail {
  membershipId: string;
  displayName: string;
  handle: string | null;
  role: TeamRole;
  joinedAt: string;
  range: AnalysisRange;
  sharing: SharingFlags;
  tokens: MetricValue;
  costs: AnalysisCosts;
  agents: AnalysisBucketItem[];
  models: AnalysisBucketItem[];
  dimensions: {
    named: MetricValueState | 'available';
    classification: MetricValueState | 'available';
    cost: MetricValueState | 'available';
  };
}

export interface TeamExportJob {
  id: string;
  kind: ExportKind;
  status: TeamExportStatus;
  snapshotId: string;
  createdAt: string;
  expiresAt?: string | null;
  errorCode?: string | null;
}

export interface AuditEvent {
  id: string;
  action: string;
  actorDisplayName?: string | null;
  targetType?: string | null;
  targetId?: string | null;
  createdAt: string;
}

export interface FilterOption {
  id: string;
  label: string;
}

export interface TeamFilterOptions {
  agents: FilterOption[];
  providers: FilterOption[];
  models: FilterOption[];
}

export interface PageQuery {
  cursor?: string;
  limit?: number;
  q?: string;
}

export interface AnalysisQuery {
  range?: string;
  from?: string;
  to?: string;
  agent?: string;
  provider?: string;
  model?: string;
  snapshotId?: string;
  collection?: AnalysisCollection;
  cursor?: string;
}

export interface RequestOptions {
  signal?: AbortSignal;
  idempotencyKey?: string;
}

function headers(options?: RequestOptions): HeadersInit | undefined {
  if (!options?.idempotencyKey) return undefined;
  return { 'Idempotency-Key': options.idempotencyKey };
}

function queryString(params: Record<string, string | number | undefined | null>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue;
    search.set(key, String(value));
  }
  const encoded = search.toString();
  return encoded ? `?${encoded}` : '';
}

function teamPath(teamId: string, suffix = ''): string {
  return `/teams/${encodeURIComponent(teamId)}${suffix}`;
}

export const teamsApi = {
  getMyTeam(signal?: AbortSignal): Promise<MeTeamResponse> {
    return api.requestJson<MeTeamResponse>('/me/team', { method: 'GET', signal });
  },

  getMyInvitations(query: PageQuery = {}, signal?: AbortSignal): Promise<{ invitations: TeamInboxInvitation[]; nextCursor: string | null }> {
    return api.requestJson(`/me/team-invitations${queryString({ cursor: query.cursor, limit: query.limit })}`, {
      method: 'GET',
      signal,
    });
  },

  createTeam(body: CreateTeamRequest, options?: RequestOptions): Promise<TeamScope> {
    return api.requestJson('/teams', {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  getTeam(teamId: string, signal?: AbortSignal): Promise<TeamScope> {
    return api.requestJson(teamPath(teamId), { method: 'GET', signal });
  },

  updateTeam(teamId: string, body: UpdateTeamRequest, options?: RequestOptions): Promise<TeamScope> {
    return api.requestJson(teamPath(teamId), {
      method: 'PATCH',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  getMembers(
    teamId: string,
    query: PageQuery & { snapshotId?: string } = {},
    signal?: AbortSignal
  ): Promise<{ members: TeamMember[]; nextCursor: string | null }> {
    return api.requestJson(
      teamPath(teamId, `/members${queryString({ q: query.q, cursor: query.cursor, limit: query.limit, snapshotId: query.snapshotId })}`),
      { method: 'GET', signal }
    );
  },

  getMemberDetail(teamId: string, membershipId: string, snapshotId: string, signal?: AbortSignal): Promise<MemberDetail> {
    return api.requestJson(
      teamPath(teamId, `/members/${encodeURIComponent(membershipId)}${queryString({ snapshotId })}`),
      { method: 'GET', signal }
    );
  },

  changeMemberRole(
    teamId: string,
    membershipId: string,
    body: { role: Exclude<TeamRole, 'owner'>; expectedAuthRevision: string },
    options?: RequestOptions
  ): Promise<TeamScope> {
    return api.requestJson(teamPath(teamId, `/members/${encodeURIComponent(membershipId)}/role`), {
      method: 'PATCH',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  removeMember(
    teamId: string,
    membershipId: string,
    body: { expectedAuthRevision: string },
    options?: RequestOptions
  ): Promise<void> {
    return api.requestJson(teamPath(teamId, `/members/${encodeURIComponent(membershipId)}/remove`), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  getInvitations(teamId: string, query: PageQuery = {}, signal?: AbortSignal): Promise<{ invitations: ManagedInvitation[]; nextCursor: string | null }> {
    return api.requestJson(
      teamPath(teamId, `/invitations${queryString({ cursor: query.cursor, limit: query.limit })}`),
      { method: 'GET', signal }
    );
  },

  createInvitation(
    teamId: string,
    body: { email: string; role: Exclude<TeamRole, 'owner'> },
    options?: RequestOptions
  ): Promise<{ invitation: ManagedInvitation; deliveryState: DeliveryState }> {
    return api.requestJson(teamPath(teamId, '/invitations'), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  revokeInvitation(
    teamId: string,
    invitationId: string,
    body: { expectedInvitationVersion: string },
    options?: RequestOptions
  ): Promise<void> {
    return api.requestJson(teamPath(teamId, `/invitations/${encodeURIComponent(invitationId)}/revoke`), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  resendInvitation(
    teamId: string,
    invitationId: string,
    body: { expectedInvitationVersion: string },
    options?: RequestOptions
  ): Promise<{ invitation: ManagedInvitation; deliveryState: DeliveryState }> {
    return api.requestJson(teamPath(teamId, `/invitations/${encodeURIComponent(invitationId)}/resend`), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  getInvitation(invitationId: string, signal?: AbortSignal): Promise<InvitationPreview> {
    return api.requestJson(`/team-invitations/${encodeURIComponent(invitationId)}`, { method: 'GET', signal });
  },

  acceptInvitation(
    invitationId: string,
    body: { expectedInvitationVersion: string; sharing: SharingFlags },
    options?: RequestOptions
  ): Promise<AcceptTeamResult> {
    return api.requestJson(`/team-invitations/${encodeURIComponent(invitationId)}/accept`, {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  getInviteLinks(teamId: string, query: PageQuery = {}, signal?: AbortSignal): Promise<{ links: InviteLink[]; nextCursor: string | null }> {
    return api.requestJson(
      teamPath(teamId, `/invite-links${queryString({ cursor: query.cursor, limit: query.limit })}`),
      { method: 'GET', signal }
    );
  },

  createInviteLink(
    teamId: string,
    body: { expiresInDays: number; maxUses: number },
    options?: RequestOptions
  ): Promise<InviteLink> {
    return api.requestJson(teamPath(teamId, '/invite-links'), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  getInviteLinkShareUrl(teamId: string, linkId: string, options?: RequestOptions): Promise<{ shareUrl: string }> {
    return api.requestJson(teamPath(teamId, `/invite-links/${encodeURIComponent(linkId)}/share-url`), {
      method: 'POST',
      body: JSON.stringify({}),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  revokeInviteLink(
    teamId: string,
    linkId: string,
    body: { expectedLinkVersion: string },
    options?: RequestOptions
  ): Promise<void> {
    return api.requestJson(teamPath(teamId, `/invite-links/${encodeURIComponent(linkId)}/revoke`), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  regenerateInviteLink(
    teamId: string,
    linkId: string,
    body: { expectedLinkVersion: string; expiresInDays: number; maxUses: number },
    options?: RequestOptions
  ): Promise<InviteLink> {
    return api.requestJson(teamPath(teamId, `/invite-links/${encodeURIComponent(linkId)}/regenerate`), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  previewInviteLink(linkId: string, token: string, options?: RequestOptions): Promise<InviteLinkPreview> {
    return api.requestJson(`/team-invite-links/${encodeURIComponent(linkId)}/preview`, {
      method: 'POST',
      body: JSON.stringify({ token }),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  acceptInviteLink(
    linkId: string,
    body: { token: string; expectedLinkVersion: string; sharing: SharingFlags },
    options?: RequestOptions
  ): Promise<AcceptTeamResult> {
    return api.requestJson(`/team-invite-links/${encodeURIComponent(linkId)}/accept`, {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  getMySharing(teamId: string, signal?: AbortSignal): Promise<MySharingResponse> {
    return api.requestJson(teamPath(teamId, '/my-sharing'), { method: 'GET', signal });
  },

  updateMySharing(
    teamId: string,
    body: { expectedSharingVersion: string; sharing: SharingFlags },
    options?: RequestOptions
  ): Promise<MySharingResponse> {
    return api.requestJson(teamPath(teamId, '/my-sharing'), {
      method: 'PATCH',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  getAnalysis(teamId: string, query: AnalysisQuery = {}, signal?: AbortSignal): Promise<TeamAnalysisResponse> {
    return api.requestJson(
      teamPath(
        teamId,
        `/analysis${queryString({
          range: query.range,
          from: query.from,
          to: query.to,
          agent: query.agent,
          provider: query.provider,
          model: query.model,
          snapshotId: query.snapshotId,
          collection: query.collection,
          cursor: query.cursor,
        })}`
      ),
      { method: 'GET', signal }
    );
  },

  getFilterOptions(teamId: string, snapshotId: string, signal?: AbortSignal): Promise<TeamFilterOptions> {
    return api.requestJson(teamPath(teamId, `/filter-options${queryString({ snapshotId })}`), { method: 'GET', signal });
  },

  createExport(
    teamId: string,
    body: { snapshotId: string; kind: ExportKind; agent?: string; provider?: string; model?: string; filtersHash?: string },
    options?: RequestOptions
  ): Promise<TeamExportJob> {
    return api.requestJson(teamPath(teamId, '/exports'), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  getExports(teamId: string, signal?: AbortSignal): Promise<{ exports: TeamExportJob[] }> {
    return api.requestJson(teamPath(teamId, '/exports'), { method: 'GET', signal });
  },

  getExport(teamId: string, exportId: string, signal?: AbortSignal): Promise<TeamExportJob> {
    return api.requestJson(teamPath(teamId, `/exports/${encodeURIComponent(exportId)}`), { method: 'GET', signal });
  },

  downloadExportContent(
    teamId: string,
    exportId: string,
    signal?: AbortSignal
  ): Promise<{ blob: Blob; filename: string | null }> {
    return api.requestBinary(teamPath(teamId, `/exports/${encodeURIComponent(exportId)}/content`), {
      method: 'GET',
      signal,
    });
  },

  leaveTeam(teamId: string, body: { expectedAuthRevision: string }, options?: RequestOptions): Promise<void> {
    return api.requestJson(teamPath(teamId, '/leave'), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  transferOwnership(
    teamId: string,
    body: { targetMembershipId: string; confirmTeamName: string; expectedAuthRevision: string },
    options?: RequestOptions
  ): Promise<TeamScope> {
    return api.requestJson(teamPath(teamId, '/transfer-ownership'), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  dissolveTeam(
    teamId: string,
    body: { confirmTeamName: string; expectedAuthRevision: string },
    options?: RequestOptions
  ): Promise<void> {
    return api.requestJson(teamPath(teamId, '/dissolve'), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  getAuditEvents(teamId: string, query: PageQuery = {}, signal?: AbortSignal): Promise<{ events: AuditEvent[]; nextCursor: string | null }> {
    return api.requestJson(
      teamPath(teamId, `/audit-events${queryString({ cursor: query.cursor, limit: query.limit })}`),
      { method: 'GET', signal }
    );
  },

  createAvatarUploadIntent(
    teamId: string,
    body: { contentType: string; byteSize: number; sha256: string },
    options?: RequestOptions
  ): Promise<{ objectId: string }> {
    return api.requestJson(teamPath(teamId, '/avatar-upload-intents'), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  uploadAvatarContent(teamId: string, objectId: string, file: File, signal?: AbortSignal): Promise<void> {
    return api.requestJson(teamPath(teamId, `/avatar-upload-intents/${encodeURIComponent(objectId)}/content`), {
      method: 'PUT',
      body: file,
      headers: { 'Content-Type': file.type },
      signal,
    });
  },

  completeAvatarUpload(
    teamId: string,
    objectId: string,
    body: { expectedProfileVersion: string },
    options?: RequestOptions
  ): Promise<TeamScope> {
    return api.requestJson(teamPath(teamId, `/avatar-upload-intents/${encodeURIComponent(objectId)}/complete`), {
      method: 'POST',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },

  deleteAvatar(teamId: string, body: { expectedProfileVersion: string }, options?: RequestOptions): Promise<TeamScope> {
    return api.requestJson(teamPath(teamId, '/avatar'), {
      method: 'DELETE',
      body: JSON.stringify(body),
      headers: headers(options),
      signal: options?.signal,
    });
  },
};

export function isTeamScope(value: MeTeamResponse | TeamScope | null | undefined): value is TeamScope {
  return Boolean(value && value.team);
}

export function fieldErrorsFromApi(details?: Record<string, unknown>): Record<string, string> {
  if (!details) return {};
  const raw = details.fieldErrors;
  const result: Record<string, string> = {};
  if (raw && typeof raw === 'object' && !Array.isArray(raw)) {
    for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
      if (typeof value === 'string') result[key] = value;
      else if (Array.isArray(value) && typeof value[0] === 'string') result[key] = value[0];
    }
  }
  return result;
}
