export type Identity = {
  email: string;
  name: string;
  avatarDataURL?: string;
  passkeys?: number;
  hasPasskeys?: boolean;
  themePref?: 'light' | 'dark' | 'system' | string;
  /** Server-owned top-level shell projection. Missing/unknown fails closed to core. */
  shellAccess?: 'core' | 'full';
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

export type HomeDestination =
  | { route: 'alerts' }
  | { route: 'room'; roomId: string; title?: string }
  | { route: 'thread'; threadId: string; messageId?: string; title?: string };

export type HomeStarterDestination =
  | { route: 'new-private' }
  | { route: 'thread'; threadId: string; messageId?: string; title?: string };

export type HomeItem = {
  id: string;
  kind: 'recent-thread' | 'needs-you' | 'active-work' | 'live-meeting';
  eyebrow: string;
  title: string;
  detail: string;
  sourceRevision?: string;
  workId?: string;
  destination: HomeDestination;
};

export type HomeStarterSuggestion = {
  id: string;
  text: string;
  destination: HomeStarterDestination;
  whyThis: string;
  sourceCoverage?: Array<{
    kind: string;
    id: string;
    revision: string;
  }>;
};

export type HomeStarter = {
  id: 'continue' | 'explore' | 'create' | 'challenge';
  label: string;
  detail: string;
  suggestions: HomeStarterSuggestion[];
};

export type HomeSnapshot = {
  version: 'home-v2';
  generatedAt: string;
  items: HomeItem[];
  starters: HomeStarter[];
  allClear: boolean;
};

export type HomeResponse = { ok: boolean; home: HomeSnapshot };
export type HomeProjectChoice = { title: string; token: string; choiceKey?: string; suggested?: boolean };
export type ProjectChatAttachmentHandle = { sourceId: string; sourceRevision: string };
export type HomeProjectContextResponse = {
  ok: true;
  projectContext: {
    available: boolean;
    scopeKey?: string;
    status: 'unlinked' | 'suggested' | 'clarify' | 'selected' | 'bound';
    suggested?: HomeProjectChoice;
    choices?: HomeProjectChoice[];
  };
};

export type ProjectCorrectionChoice = {
  title: string;
  token: string;
};

export type ProjectCorrectionProjection = {
  available: boolean;
  scopeKey: string;
  current: {
    title: string;
    status: 'confirmed' | 'unavailable' | 'removed';
    contextRevision: number;
  };
  choices: ProjectCorrectionChoice[];
  remove?: ProjectCorrectionChoice;
};

export type ProjectCorrectionResponse = {
  ok?: boolean;
  projectCorrection?: ProjectCorrectionProjection;
  thread?: ScoutThread & { messages?: ScoutMessage[] };
  messages?: ScoutMessage[];
  message?: ScoutMessage;
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
  /** Body-free current work projection used by the lightweight Chat index. */
  activeWork?: Pick<ScoutMessage, 'createdAt' | 'thread'>;
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

export type ScoutReplyState = 'project_pending' | 'queued' | 'running' | 'completed' | 'failed' | 'canceled';

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

export type ScoutWorkThreadRef = {
  id: string;
  mode: string;
  query: string;
  status: string;
  artifactId?: string;
  agentId?: string;
  agentName?: string;
  delegatedBy?: string;
  currentStage?: string;
  progressPercent?: number;
  progressNote?: string;
  followUpStatus?: string;
  attentionReason?: 'output_truncated' | 'quality_gate_failed' | 'provider_unavailable' | 'work_failed';
  startedAt?: string;
  resultTitle?: string;
  resultPreview?: string;
  provenance?: string;
};

export type ScoutWorkRecordRef = {
  id: string;
  runId: string;
  title: string;
  status: string;
  workerName: string;
  currentStage: string;
  summary: string;
  progressPercent: number;
  artifactId: string;
  artifactHref: string;
  evidenceHref: string;
  providerExecutionFenced: boolean;
};

export type ArtifactDispositionRef = {
  tenantId: string;
  artifactId: string;
  contentRevision: number;
  contentDigest: string;
  aclVersion: number;
  audienceDigest: string;
};

export type ArtifactDriveReference = {
  id: string;
  name?: string;
  artifact: ArtifactDispositionRef;
  createdAt: string;
  createdBy: string;
  folderId?: string;
  sourceArtifactId: string;
};

export type ArtifactDriveSaveCapability = {
  available: boolean;
  action: 'save';
  receiptBacked: boolean;
};

export type ArtifactDispositionReceipt = {
  operationId: string;
  action: 'open' | 'save' | 'discard';
  artifact: ArtifactDispositionRef;
  outcome: string;
  drive?: ArtifactDriveReference;
};

export type ArtifactResponse = {
  ok: boolean;
  artifacts: Array<{ id: string; text?: string; metadata?: Record<string, string> }>;
  dispositionRef?: ArtifactDispositionRef;
};

export type DriveFileRecord = {
  id: string;
  name: string;
  mime?: string;
  size?: number;
  folderId?: string;
  artifactId?: string;
  origin?: string;
  downloadUrl?: string;
  canRename?: boolean;
};

export type DriveFolderRecord = {
  id: string;
  name: string;
  count?: number;
  parentId?: string;
};

export type ScoutImageRef = {
  ref: string;
  mime?: string;
  name?: string;
  artifactId?: string;
  prompt?: string;
  generationId?: string;
  replacesMessageId?: string;
  savedToFiles?: boolean;
};

export type ScoutImageGeneration = {
  status: string;
  replacesMessageId?: string;
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
  proposal?: ScoutProposal;
  choices?: Array<Record<string, unknown>>;
  reply?: ScoutReplyLifecycle;
  project?: {
    status: 'pending' | 'confirmed' | 'unavailable' | 'removed';
    projectId?: string;
    projectRevision?: number;
    contextRevision?: number;
    associationId?: string;
    associationRevision?: number;
    title: string;
    basis?: string;
  };
  thread?: ScoutWorkThreadRef;
  work?: ScoutWorkRecordRef;
  image?: ScoutImageRef;
  imageGeneration?: ScoutImageGeneration;
  [key: string]: unknown;
};

export type ScoutProposal = {
  kind?: string;
  mode?: string;
  agentId?: string;
  agentName?: string;
  objective?: string;
  summary?: string;
  lane?: string;
  weightLabel?: string;
  status?: string;
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
  handle?: string;
  email?: string;
  agentId?: string;
  roleTitle?: string;
  avatarDataURL?: string;
  kind: 'person' | 'scout' | 'agent';
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
  projection?: {
    cards?: Array<{
      cardId: string;
      deliveryStage: 'requested' | 'delivered' | 'drive';
      projectId: string;
      projectTitle: string;
      projectResolution: 'linked' | 'tag' | 'missing';
      artifactId?: string;
    }>;
    projects?: Array<{ id: string; title: string }>;
  };
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
	importedCount?: number;
	alreadyPresentCount?: number;
};

export type StridePersonalContextSource = {
  personId: string;
  sourceId: string;
  revision: number;
  kind: 'preference' | 'reflection' | 'correction';
  body: string;
  bodyDigest: string;
  consentRevision: number;
  updatedAt: string;
};

export type StridePersonalContextExport = {
  personId: string;
  exportedAt: string;
  sources: StridePersonalContextSource[];
  manifestDigest: string;
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

export type RoomAgentParticipant = {
  id: string;
  name: string;
  kind: 'scout' | 'employee' | string;
  color: string;
  status: 'starting' | 'ready' | 'degraded' | 'closed' | string;
  voiceState: 'starting' | 'listening' | 'hearing' | 'thinking' | 'talking' | 'degraded' | string;
  invitationId: string;
  invitedAt: string;
  invitedBy?: string;
  model?: string;
  providerSessionStarted: boolean;
};

export type RoomAgentsResponse = {
  ok: boolean;
  agents: RoomAgentParticipant[];
};

export type StrideTeamSeat = {

  id: string;
  listingId: string;
  displayName: string;
  category: string;
  roleTitle?: string;
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
    origin?: string;
    runId?: string;
    artifactId?: string;
    sourceThreadId?: string;
    sourceRefs?: string[];
    confidence?: number;
    expiresAt?: string;
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
  roleTitle?: string;
  usageGuidance?: string;
  workingStyle?: string;
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

export type StrideWorkArtifactResponse = {
  ok: boolean;
  artifact: {
    id: string;
    title: string;
    summary: string;
    approvedOutcome: string;
    sourceSnippet: string;
    sourceHref: string;
    destinationThreadId: string;
    providerExecutionFenced: boolean;
    reportAvailable?: boolean;
    [key: string]: unknown;
  };
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
  choicesMutable: boolean;
  policyManaged: boolean;
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
