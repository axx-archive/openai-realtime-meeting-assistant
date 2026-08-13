import type { ScoutThreadDetailResponse } from '../api/types';

export type HomeScoutOpeningAttempt = {
  text: string;
  projectContextToken?: string;
  idempotencyKey: string;
};

export type HomeScoutOpeningAcceptance = {
  threadId: string;
  title: string;
};

export type HomeScoutOpeningResult =
  | { accepted: true; attempt: HomeScoutOpeningAttempt; thread: HomeScoutOpeningAcceptance }
  | { accepted: false; attempt: HomeScoutOpeningAttempt; error: unknown };

let fallbackKeySequence = 0;

export function createHomeScoutIdempotencyKey(): string {
  const randomUUID = globalThis.crypto?.randomUUID?.bind(globalThis.crypto);
  if (randomUUID) return `home-scout-${randomUUID()}`;
  fallbackKeySequence += 1;
  return `home-scout-${Date.now().toString(36)}-${fallbackKeySequence.toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

export function homeScoutOpeningAttempt(
  current: HomeScoutOpeningAttempt | null,
  value: string,
  createKey: () => string = createHomeScoutIdempotencyKey,
  projectContextToken = '',
): HomeScoutOpeningAttempt | null {
  const text = value.trim();
  if (!text) return null;
  if (current?.text === text && (current.projectContextToken ?? '') === projectContextToken) return current;
  return { text, ...(projectContextToken ? { projectContextToken } : {}), idempotencyKey: createKey() };
}

export function homeScoutOpeningBody(attempt: HomeScoutOpeningAttempt) {
  return {
    openingMessage: { text: attempt.text, ...(attempt.projectContextToken ? { projectContextToken: attempt.projectContextToken } : {}) },
  } as const;
}

function fallbackTitle(text: string): string {
  return text.length > 54 ? `${text.slice(0, 51).trimEnd()}…` : text;
}

export async function submitHomeScoutOpening(
  attempt: HomeScoutOpeningAttempt,
  dependencies: {
    createThread: (
      body: ReturnType<typeof homeScoutOpeningBody>,
      idempotencyKey: string,
    ) => Promise<ScoutThreadDetailResponse>;
  },
): Promise<HomeScoutOpeningResult> {
  try {
    const response = await dependencies.createThread(
      homeScoutOpeningBody(attempt),
      attempt.idempotencyKey,
    );
    const threadId = String(response.thread?.id ?? '').trim();
    if (!threadId) throw new Error('Scout did not open a thread.');
    const title = String(response.thread?.title ?? '').trim() || fallbackTitle(attempt.text);
    return { accepted: true, attempt, thread: { threadId, title } };
  } catch (error) {
    return { accepted: false, attempt, error };
  }
}
