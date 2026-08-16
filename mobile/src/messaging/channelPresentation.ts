import type { ScoutThread } from '../api/types';

export function isBonfireChat(thread: Pick<ScoutThread, 'table' | 'title' | 'visibility'> | null | undefined): boolean {
  if (!thread || String(thread.visibility ?? '').toLowerCase() !== 'public') return false;
  const title = String(thread.title ?? '').trim().toLowerCase();
  return thread.table === true || title === 'team' || title === 'general' || title === 'bonfire chat';
}

/**
 * Forbidden thread title patterns — never show these as the display title.
 * Matches work status, prompts, and last spoken lines that leak into titles.
 */
const FORBIDDEN_TITLE_PATTERNS = [
  /^scout is (working|thinking|listening)/iu,
  /^(research|work|presentation|document) delivered/iu,
  /^(generating|creating|building|preparing)/iu,
  /^(needs attention|deliverable ready)/iu,
];

/**
 * Heading-only title lift — matches web `desktopWorkTitle`.
 *
 * Never use `thread.title || preview`. Never show the prompt, last spoken line,
 * or work status (like "Scout is working" or "Research delivered") as the title.
 * Returns a clean heading or "Conversation" as fallback.
 */
export function threadHeadingTitle(thread: Pick<ScoutThread, 'title'>): string {
  const raw = String(thread.title ?? '').trim();
  if (!raw) return 'Conversation';
  if (FORBIDDEN_TITLE_PATTERNS.some((pattern) => pattern.test(raw))) return 'Conversation';
  return raw;
}

export function channelDisplayName(thread: Pick<ScoutThread, 'table' | 'title' | 'visibility' | 'preview'>): string {
  if (isBonfireChat(thread)) return 'Bonfire Chat';
  // Heading-only: use proper title, not preview or work status
  const heading = threadHeadingTitle(thread);
  const title = heading === 'Conversation'
    ? String(thread.title || '').trim() || 'Conversation'
    : heading;
  return thread.visibility === 'public' ? `#${title.replace(/^#/, '')}` : title;
}

export function pinBonfireChatFirst<T extends Pick<ScoutThread, 'table' | 'title' | 'visibility'>>(threads: readonly T[]): T[] {
  return threads
    .map((thread, index) => ({ thread, index }))
    .sort((left, right) => Number(isBonfireChat(right.thread)) - Number(isBonfireChat(left.thread)) || left.index - right.index)
    .map(({ thread }) => thread);
}
