import type { ScoutMessage } from '../api/types';

export type ChatThreadEventPayload = {
  id?: string;
  title?: string;
  preview?: string;
  visibility?: string;
  memberEmails?: string[];
  updatedAt?: string;
  message?: ScoutMessage;
  deletedMessageId?: string;
};

export type ChatTypingEventPayload = {
  threadId?: string;
  email?: string;
  name?: string;
  avatarDataURL?: string;
  typing?: boolean;
};

export type SequencedChatThreadEvent = {
  generation: number;
  payload: ChatThreadEventPayload;
};

export const maxChatThreadEventJournal = 256;

export function chatThreadEventJournalCovers(
  generationAtRequest: number,
  currentGeneration: number,
  journal: readonly SequencedChatThreadEvent[],
): boolean {
  if (generationAtRequest === currentGeneration) return true;
  if (generationAtRequest > currentGeneration) return false;
  const generations = journal
    .map((entry) => entry.generation)
    .filter((generation) => generation > generationAtRequest && generation <= currentGeneration)
    .sort((left, right) => left - right);
  const expectedCount = currentGeneration - generationAtRequest;
  return generations.length === expectedCount
    && generations.every((generation, index) => generation === generationAtRequest + index + 1);
}

export function applyChatThreadEvent(
  messages: readonly ScoutMessage[],
  threadId: string,
  payload: ChatThreadEventPayload | null | undefined,
): ScoutMessage[] {
  if (!payload || String(payload.id ?? '') !== threadId) return messages as ScoutMessage[];
  const deletedID = String(payload.deletedMessageId ?? '').trim();
  if (deletedID) {
    const next = messages.filter((message) => String(message.id) !== deletedID);
    return next.length === messages.length ? messages as ScoutMessage[] : next;
  }
  const incoming = payload.message;
  if (!incoming?.id) return messages as ScoutMessage[];
  const index = messages.findIndex((message) => String(message.id) === String(incoming.id));
  if (index < 0) return [...messages, incoming];
  const next = [...messages];
  next[index] = incoming;
  return next;
}

/** Preserve the current array when a fallback fetch has no new information. */
export function reconcileChatThreadSnapshot(
  current: readonly ScoutMessage[],
  fetched: readonly ScoutMessage[],
): ScoutMessage[] {
  if (current === fetched) return current as ScoutMessage[];
  if (current.length !== fetched.length) return [...fetched];
  for (let index = 0; index < current.length; index += 1) {
    if (JSON.stringify(current[index]) !== JSON.stringify(fetched[index])) return [...fetched];
  }
  return current as ScoutMessage[];
}

/**
 * Resolve an HTTP snapshot without allowing it to erase live events that
 * arrived while the request was in flight. Replaying the complete socket
 * journal over the fetched baseline preserves both older history (critical on
 * initial load) and newer append/edit/delete events. If the bounded journal no
 * longer covers the request, preserve the visible transcript and let the next
 * authoritative reconciliation repair it instead of guessing.
 */
export function resolveChatThreadSnapshot(
  current: readonly ScoutMessage[],
  fetched: readonly ScoutMessage[],
  threadId: string,
  generationAtRequest: number,
  currentGeneration: number,
  journal: readonly SequencedChatThreadEvent[],
): { accepted: boolean; replayed: boolean; messages: ScoutMessage[] } {
  if (generationAtRequest === currentGeneration) {
    return {
      accepted: true,
      replayed: false,
      messages: reconcileChatThreadSnapshot(current, fetched),
    };
  }
  if (generationAtRequest > currentGeneration) {
    return { accepted: false, replayed: false, messages: current as ScoutMessage[] };
  }
  if (!chatThreadEventJournalCovers(generationAtRequest, currentGeneration, journal)) {
    return { accepted: false, replayed: false, messages: current as ScoutMessage[] };
  }
  const events = journal
    .filter((entry) => entry.generation > generationAtRequest && entry.generation <= currentGeneration)
    .sort((left, right) => left.generation - right.generation);
  const replayed = events.reduce<ScoutMessage[]>(
    (messages, entry) => applyChatThreadEvent(messages, threadId, entry.payload),
    [...fetched],
  );
  return { accepted: true, replayed: true, messages: reconcileChatThreadSnapshot(current, replayed) };
}

export function isMessageRunEnd(messages: readonly ScoutMessage[], index: number): boolean {
  const current = messages[index];
  const next = messages[index + 1];
  if (!current || !next) return Boolean(current);
  return String(current.role ?? '') !== String(next.role ?? '')
    || String(current.authorEmail ?? '').trim().toLowerCase() !== String(next.authorEmail ?? '').trim().toLowerCase();
}

export function typingIndicatorLabel(names: readonly string[]): string {
  const clean = [...new Set(names.map((name) => name.trim()).filter(Boolean))];
  if (clean.length === 0) return '';
  if (clean.length === 1) return `${clean[0]} is typing`;
  if (clean.length === 2) return `${clean[0]} and ${clean[1]} are typing`;
  return `${clean[0]} and ${clean.length - 1} others are typing`;
}
