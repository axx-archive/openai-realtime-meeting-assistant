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
