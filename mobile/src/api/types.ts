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
  createdAt: string;
  authorName?: string;
  authorEmail?: string;
  [key: string]: unknown;
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
  column?: string;
  status?: string;
  owner?: string;
  labels?: string[];
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

export type ApiError = {
  status: number;
  message: string;
};
