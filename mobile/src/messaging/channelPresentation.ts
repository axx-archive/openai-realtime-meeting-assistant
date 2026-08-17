import type { ScoutThread } from '../api/types';

export function isBonfireChat(thread: Pick<ScoutThread, 'table' | 'title' | 'visibility'> | null | undefined): boolean {
  if (!thread || String(thread.visibility ?? '').toLowerCase() !== 'public') return false;
  const title = String(thread.title ?? '').trim().toLowerCase();
  return thread.table === true || title === 'team' || title === 'general' || title === 'bonfire chat';
}

/**
 * Heading-only title lift — matches web `desktopWorkTitle`.
 *
 * Locked plan:
 * - Lift markdown # / ## / ### heading
 * - Accept em/en-dash structured titles ("Strategy — Q3 Review")
 * - If title === query (the prompt), reject it
 * - Never rely on verb patterns as the guard
 * - Fall back to "Conversation"
 *
 * Pass `query` (the user's original prompt) to enable the title===query check.
 * If title equals query, it's the prompt being echoed back — reject it.
 */
export function threadHeadingTitle(
  thread: Pick<ScoutThread, 'title'>,
  query?: string | null
): string {
  const raw = String(thread.title ?? '').trim();
  if (!raw) return 'Conversation';
  
  // Title === query check: if the title IS the prompt, reject it
  const normalizedTitle = raw.toLowerCase().replace(/\s+/g, ' ').trim();
  const normalizedQuery = String(query ?? '').toLowerCase().replace(/\s+/g, ' ').trim();
  if (normalizedQuery && normalizedTitle === normalizedQuery) return 'Conversation';
  
  // Markdown heading: # Title, ## Title, ### Title — extract and use
  const mdMatch = raw.match(/^#{1,3}\s+(.+)$/u);
  if (mdMatch) {
    const heading = mdMatch[1].trim();
    if (heading) return heading;
  }
  
  // Em/en-dash structured title ("Strategy — Q3 Review") — accept
  if (/\s[—–]\s/.test(raw)) return raw;
  
  // Everything else is assumed to be a prompt — reject it
  // This catches: "make a 5-slide deck", "Please make a deck", "A deck about Q3", etc.
  return 'Conversation';
}

/**
 * Extract the query (user's prompt) from a thread if available.
 *
 * The query might be in activeWork.thread.query or in thread messages.
 */
function extractThreadQuery(thread: Pick<ScoutThread, 'activeWork' | 'messages'>): string | null {
  // Try activeWork first
  const activeQuery = (thread.activeWork as { thread?: { query?: string } } | undefined)?.thread?.query;
  if (activeQuery) return activeQuery;
  
  // Try first user message as fallback
  const messages = thread.messages ?? [];
  const firstUserMessage = messages.find((m) => m.role === 'user');
  if (firstUserMessage?.text) return firstUserMessage.text;
  
  return null;
}

/**
 * Channel display name — heading-only lift (locked plan).
 *
 * Public channels: use the title directly (set by owner, not from query).
 * Private conversations: use threadHeadingTitle with title===query check.
 */
export function channelDisplayName(
  thread: Pick<ScoutThread, 'table' | 'title' | 'visibility' | 'preview' | 'activeWork' | 'messages'>
): string {
  if (isBonfireChat(thread)) return 'Bonfire Chat';
  
  // Public channels have intentionally-set titles, not prompts
  if (thread.visibility === 'public') {
    const title = String(thread.title ?? '').trim() || 'Conversation';
    return `#${title.replace(/^#/, '')}`;
  }
  
  // Private conversations: extract query for title===query check
  const query = extractThreadQuery(thread);
  const heading = threadHeadingTitle(thread, query);
  return heading;
}

export function pinBonfireChatFirst<T extends Pick<ScoutThread, 'table' | 'title' | 'visibility'>>(threads: readonly T[]): T[] {
  return threads
    .map((thread, index) => ({ thread, index }))
    .sort((left, right) => Number(isBonfireChat(right.thread)) - Number(isBonfireChat(left.thread)) || left.index - right.index)
    .map(({ thread }) => thread);
}
