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
  updatedAt?: string;
  createdAt?: string;
  messageCount?: number;
  preview?: string;
  lastMessage?: {
    text?: string;
    role?: string;
    createdAt?: string;
  };
  archived?: boolean;
  [key: string]: unknown;
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
  authorName?: string;
  authorEmail?: string;
  files?: ScoutFileAttachment[];
  proposal?: Record<string, unknown>;
  choices?: Array<Record<string, unknown>>;
  [key: string]: unknown;
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
  [key: string]: unknown;
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
