export type Identity = {
  email: string;
  name: string;
  avatarDataURL?: string;
  passkeys?: number;
  hasPasskeys?: boolean;
  themePref?: 'light' | 'dark' | 'system' | string;
  /** Only present on native login responses (X-Bonfire-Client: expo). */
  sessionToken?: string;
};

export type Room = {
  id: string;
  name: string;
  live: boolean;
  participantCount: number;
  passcodeRequired: boolean;
  guestEnabled: boolean;
  guestLinkActive: boolean;
  createdBy: string;
  archived: boolean;
};

export type RoomsResponse = {
  ok: boolean;
  rooms: Room[];
};

export type ScoutThread = {
  id: string;
  title?: string;
  visibility?: string;
  ownerEmail?: string;
  memberEmails?: string[];
  updatedAt?: string;
  createdAt?: string;
  messageCount?: number;
  preview?: string;
  /** True for the deployment's single permanent team thread — the Table. */
  table?: boolean;
  /** Per-viewer, computed server-side from the read marker. Never persisted. */
  unreadCount?: number;
  /** Anchors the client's "new messages" divider. */
  lastReadMessageId?: string;
  messages?: ScoutMessage[];
  lastMessage?: {
    text?: string;
    role?: string;
    createdAt?: string;
  };
  archived?: boolean;
  [key: string]: unknown;
};

export type ThreadCatchUpBullet = {
  text: string;
  author: string;
  messageId: string;
  createdAt: string;
};

export type ThreadDeposit = {
  messageId: string;
  author?: string;
};

export type ThreadDigestResponse = {
  ok: boolean;
  catchUp: {
    headline: string;
    bullets: ThreadCatchUpBullet[];
    totalUnread: number;
  };
  deposits: {
    files: Array<ThreadDeposit & { name: string; mime?: string; ref?: string }>;
    links: Array<ThreadDeposit & { url: string; host: string }>;
  };
};

export type ScoutThreadsResponse = {
  ok: boolean;
  threads: ScoutThread[];
};

export type ScoutReplyState = 'queued' | 'running' | 'completed' | 'failed' | 'canceled';

/**
 * Durable lifecycle for the server-owned Scout reply placeholder paired with
 * one user message. Provider/lease internals never cross this public contract.
 */
export type ScoutReplyLifecycle = {
  operationId: string;
  inReplyTo: string;
  state: ScoutReplyState;
  attempt: number;
  queuedAt?: string;
  startedAt?: string;
  finishedAt?: string;
  retryable?: boolean;
  errorCode?: string;
};

export type ScoutMessage = {
  id: string;
  kind?: string;
  role: string;
  text?: string;
  content?: string;
  createdAt: string;
  editedAt?: string;
  authorName?: string;
  authorEmail?: string;
  files?: ScoutFileAttachment[];
  reactions?: ScoutMessageReaction[];
  replyTo?: ScoutMessageReplyRef;
  sources?: ScoutAnswerSource[];
  proposal?: Record<string, unknown>;
  choices?: Array<Record<string, unknown>>;
  reply?: ScoutReplyLifecycle;
  [key: string]: unknown;
};

export type ScoutMessageReplyRef = {
  messageId: string;
  authorName: string;
  authorEmail?: string;
  text: string;
};

export type ScoutMessageReaction = {
  emoji: string;
  actorEmail: string;
  actorName?: string;
  createdAt?: string;
};

export type ScoutAnswerSource = {
  messageId: string;
  author?: string;
  /** The phrase the answer provably quotes — why this was cited. */
  quote: string;
};

export type ScoutFileAttachment = {
  name: string;
  kind?: string;
  size?: number;
  ref: string;
  mime: string;
  sourceId?: string;
  sourceRevision?: string;
};

export type ScoutThreadDetailResponse = {
  ok?: boolean;
  thread?: ScoutThread & { messages?: ScoutMessage[] };
  messages?: ScoutMessage[];
  /** Per-viewer read state — beside the record, never on it. */
  readAt?: string;
  lastReadMessageId?: string;
  muted?: boolean;
  notificationLevel?: 'all' | 'mentions' | 'none' | string;
  [key: string]: unknown;
};

export type LinkPreview = {
  url: string;
  kind?: string;
  title?: string;
  description?: string;
  siteName?: string;
  imageUrl?: string;
  mediaType?: string;
  authorName?: string;
  authorHandle?: string;
  publishedAt?: string;
};

export type GiphySearchResult = {
  id: string;
  title: string;
  previewUrl: string;
  stillUrl?: string;
  mediaUrl: string;
  width: number;
  height: number;
};

export type ChatMentionCandidate = {
  name: string;
  email?: string;
  avatarDataURL?: string;
  kind: 'person' | 'scout';
};

export type BoardCard = {
  id: string;
  title?: string;
  body?: string;
  notes?: string;
  column?: string;
  status?: string;
  owner?: string;
  labels?: string[];
  tags?: string[];
  dueDate?: string;
  keyDates?: string[];
  draft?: boolean;
  updatedAt?: string;
  [key: string]: unknown;
};

export type BoardState = {
  cards?: BoardCard[];
  updatedAt?: string;
  columns?: Array<{ id: string; title?: string; name?: string }>;
  [key: string]: unknown;
};

export type BoardResponse = {
  ok: boolean;
  board: BoardState;
};

export type StrideRelationshipReference = {
  contractType: string;
  id: string;
  revision: number;
  digest: string;
};

export type StrideRelationshipConsent = {
  subjectPrincipal?: string;
  revision?: number;
  enabled?: boolean;
  allowInferred?: boolean;
  allowShared?: boolean;
  updatedAt?: string;
  updatedBy?: string;
};

export type StrideRelationshipPreference = {
  reference: StrideRelationshipReference;
  preferenceType: string;
  value: string;
  scope: 'private' | 'shared' | string;
  origin: 'explicit' | 'inferred' | string;
  sourceEventId: string;
  evidence: StrideRelationshipReference[];
  confidence: number;
  expiresAt: string;
  consentRevision: number;
  projectionRevision: number;
  source?: {
    kind: 'settings' | 'conversation' | 'company_context' | string;
    label: string;
    available: boolean;
    threadId?: string;
    threadTitle?: string;
    messageId?: string;
    occurredAt?: string;
  };
  relationship: {
    firstObserved: string;
    lastObserved: string;
    audience: { visibility: string; principals: string[] };
    reinforcementCount: number;
    status: string;
  };
};

export type StrideRelationshipMemoryResponse = {
  ok: boolean;
  providerExecutionFenced: true;
  mode: 'deterministic_local';
  revision: number;
  consent?: StrideRelationshipConsent;
  preferences?: StrideRelationshipPreference[];
};

export type StrideRuntimeCapability = {
  capability: string;
  state: string;
  durableState?: boolean;
  featureEnabled?: boolean;
  activationFenced?: boolean;
};

export type StrideRuntimeFeature = {
  feature: string;
  enabled: boolean;
};

export type StrideRuntimeStatusResponse = {
  ok: boolean;
  runtime: {
    state: string;
    configured: boolean;
    restored?: boolean;
    generation?: number;
    activationFenced: boolean;
    capabilities?: StrideRuntimeCapability[];
    features?: StrideRuntimeFeature[];
  };
};

export type StrideMeetingSpecialistCandidate = {
  agentId: string;
  displayName: string;
};

export type StrideMeetingSpecialistInvitation = {
  id: string;
  revision: number;
  agentId: string;
  displayName: string;
  purposeSummary: string;
  contextClasses: string[];
  audience: {
    visibility: string;
    principals: string[];
  };
  expectedTimeSeconds: number;
  expectedCostCents: number;
  hardLimits: {
    timeBudgetSeconds: number;
    turnBudget: number;
    maxFloorLeaseSeconds: number;
    audioBudgetSeconds: number;
    tokenBudget: number;
    costBudgetCents: number;
  };
  decision: 'requested' | 'approved' | 'declined' | 'dismissed' | string;
  status: string;
  providerSessionStarted: boolean;
  expiresAt: string;
  updatedAt: string;
};

export type StrideMeetingSpecialistStatus = {
  available: boolean;
  canInvite: boolean;
  reason?: string;
  roomId?: string;
  sittingId?: string;
  candidates: StrideMeetingSpecialistCandidate[];
  invitations: StrideMeetingSpecialistInvitation[];
};

export type StrideMeetingSpecialistStatusResponse = {
  ok: boolean;
  specialists: StrideMeetingSpecialistStatus;
};

export type StrideTeamSeat = {

  id: string;
  listingId: string;
  displayName: string;
  category: string;
  status: string;
  ownerId: string;
  directThreadId?: string;
  revision: number;
  config: {
    personalityNotes?: string;
    memberships: string[];
    perRunBudgetCents: number;
    dailyBudgetCents: number;
    proactivity: string;
  };
  assignments: Array<{
    id: string;
    projectOrChannel: string;
    role: string;
    responsibility: string;
    destination: string;
    status: string;
    createdAt: string;
  }>;
  updates: Array<{
    id: string;
    revision: number;
    status: string;
    summary: string;
    semanticDiff?: {
      personalityChanged: boolean;
      membershipsAdded: string[];
      membershipsRemoved: string[];
      permissionChanged: boolean;
      perRunBudgetDeltaCents: number;
      dailyBudgetDeltaCents: number;
      costChanged: boolean;
      proactivityChanged: boolean;
      runtimeChanged: boolean;
      runtimeSummary: string;
      migrationSummary: string;
      digest: string;
    };
  }>;
  learning: Array<{
    id: string;
    subject: string;
    scope: string;
    summary: string;
    status: string;
    revision: number;
    createdAt: string;
    updatedAt: string;
  }>;
  lifecycle: string[];
  providerExecutionFenced: boolean;
  accessRevoked: boolean;
  [key: string]: unknown;
};

export type StrideMarketplaceListing = {

  id: string;
  packageId: string;
  displayName: string;
  category: string;
  outcomeSummary: string;
  personalitySummary: string;
  sampleOutputs: string[];
  capabilities: string[];
  requiredAccess: string[];
  accessSummary: string;
  costBand: string;
  publisher: string;
  version: string;
  provenance: string;
  visibility: string;
  updatePolicy: string;
  memoryPolicy: string;
  packageDigest: string;
  receiptStatus: Record<string, boolean>;
  availability: string;
  liveAvailable: boolean;
  providerExecutionFenced: boolean;
  [key: string]: unknown;
};

export type StrideWorkSuggestion = {
  id: string;
  title: string;
  outcome: string;
  sourceThreadId: string;
  sourceMessageId: string;
  sourceSnippet: string;
  recipientIds: string[];
  revision: number;
  status: string;
  destinationMode?: string;
  destinationThreadId?: string;
  destinationTitle?: string;
  runId?: string;
  artifactHref?: string;
  brainHref?: string;
  completionSummary?: string;
  providerExecutionFenced: boolean;
  lifecycle: string[];
};

export type StrideRosterResponse = {
  ok: boolean;
  available: boolean;
  reason?: string;
  seats: StrideTeamSeat[];
  recommendations?: unknown[];
};

export type StrideMarketplaceResponse = {
  ok: boolean;
  available: boolean;
  reason?: string;
  canManage?: boolean;
  listings: StrideMarketplaceListing[];
};

export type StridePrivateAgentTemplateInput = {
  templateId: string;
  displayName: string;
  category: string;
  outcomeSummary: string;
  personalitySummary: string;
  sampleOutputs: string[];
  requestedCapabilities: string[];
  requiredAccess: string[];
  costBand: string;
  memberships: string[];
  perRunBudgetCents: number;
  dailyBudgetCents: number;
  monthlyBudgetCents: number;
  concurrency: number;
  proactivity: 'disabled' | 'quiet';
};

export type StrideWorkResponse = {
  ok: boolean;
  available: boolean;
  reason?: string;
  providerExecutionFenced: boolean;
  suggestions: StrideWorkSuggestion[];
  runs: Array<Record<string, unknown>>;
};

export type StrideSeatMutationResponse = {
  ok: boolean;
  seat: StrideTeamSeat;
  replayed?: boolean;
  scoutIntroductionPosted?: boolean;
  providerSessionStarted: false;
};

export type StrideWorkMutationResponse = {
  ok: boolean;
  suggestion: StrideWorkSuggestion;
  providerCalls?: 0;
  inputTokens?: 0;
  outputTokens?: 0;
};

export type BoardCardInput = {
  title: string;
  status: string;
  owner: string;
  notes: string;
  tags: string[];
  dueDate?: string;
  keyDates?: Array<{ label: string; date: string }>;
};

export const consentScopes = [
  'audio_capture',
  'transcription',
  'model_analysis',
  'org_memory',
] as const;

export type ConsentScope = (typeof consentScopes)[number];

export const consentDispositions = ['granted', 'denied', 'withdrawn'] as const;

export type ConsentDisposition = (typeof consentDispositions)[number];

export const consentLanes = [
  'audio_transport',
  'audio_capture',
  'transcription',
  'model_analysis',
  'org_memory',
] as const;

export type ConsentLane = (typeof consentLanes)[number];

export type ConsentLaneStatus = {
  allowed: boolean;
  missingScopes?: ConsentScope[];
  recordIds?: Partial<Record<ConsentScope, string>>;
};

export type ConsentStatus = {
  policyVersion: string;
  principalKind: 'user' | 'guest';
  roomId: string;
  sittingId: string;
  guestPolicyListenOnly: boolean;
  storeAvailable: boolean;
  lanes: Record<ConsentLane, ConsentLaneStatus>;
  scopes: Partial<Record<ConsentScope, ConsentDisposition>>;
};

export type ConsentDecisionRequest = {
  scope: ConsentScope;
  disposition: ConsentDisposition;
};

export type ConsentDecisionResponse = {
  recordId: string;
  recordedAt: string;
  lastAcceptedCaptureSequence: number | null;
  consent: ConsentStatus;
};

export type ApiError = {
  status: number;
  message: string;
};
