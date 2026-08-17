import type { ScoutThread } from '../api/types';

export function isBonfireChat(thread: Pick<ScoutThread, 'table' | 'title' | 'visibility'> | null | undefined): boolean {
  if (!thread || String(thread.visibility ?? '').toLowerCase() !== 'public') return false;
  const title = String(thread.title ?? '').trim().toLowerCase();
  return thread.table === true || title === 'team' || title === 'general' || title === 'bonfire chat';
}

/**
 * Forbidden thread title patterns — work status that leaks into titles.
 */
const FORBIDDEN_STATUS_PATTERNS = [
  /^scout is (working|thinking|listening)/iu,
  /^(research|work|presentation|document) delivered/iu,
  /^(generating|creating|building|preparing)/iu,
  /^(needs attention|deliverable ready)/iu,
];

/**
 * Prompt-like patterns — if title matches, it's the user's query, not a heading.
 *
 * Prompts typically start with an imperative verb or are questions.
 * "make a 5-slide deck" is a prompt. "Q3 Strategy Review" is a heading.
 */
const PROMPT_PATTERNS = [
  // Imperative verbs at start (common prompt starters)
  /^(make|create|build|write|draft|design|research|analyze|summarize|help|generate|prepare|develop|explain|describe|compare|find|search|look|get|give|tell|show|list|outline|review|revise|update|edit|translate|convert|turn|transform)\b/iu,
  // Questions
  /^(what|how|why|when|where|who|which|can|could|would|should|is|are|do|does|will)\b.+\??\s*$/iu,
  // "I want/need" patterns
  /^i (want|need|would like|'d like)\b/iu,
  // Sentences ending with period (prompts are often full sentences)
  /^[a-z].*\.$/iu,
];

/**
 * Extract a real heading from markdown or em/en-dash format.
 *
 * - `# Heading` → `Heading`
 * - `## Subheading` → `Subheading`
 * - `Title — Subtitle` keeps the structure
 */
function extractMarkdownHeading(text: string): string | null {
  // Markdown heading: # Title, ## Title, ### Title
  const mdMatch = text.match(/^#{1,3}\s+(.+)$/u);
  if (mdMatch) return mdMatch[1].trim();
  return null;
}

/**
 * Check if text looks like a real title (not a prompt).
 *
 * Real titles:
 * - Short (under ~60 chars typically)
 * - Don't start with imperative verbs
 * - Aren't questions
 * - May have em/en-dash structure ("Strategy — Q3")
 */
function looksLikeRealTitle(text: string): boolean {
  // Too long to be a real title
  if (text.length > 80) return false;
  // Matches prompt patterns
  if (PROMPT_PATTERNS.some((pattern) => pattern.test(text))) return false;
  // Has em/en-dash structure (real titles often do)
  if (/\s[—–-]\s/.test(text)) return true;
  // Title case or all caps (real titles often are)
  if (/^[A-Z][a-z]+(\s+[A-Z][a-z]+)*$/.test(text)) return true;
  if (/^[A-Z\s]+$/.test(text) && text.length < 40) return true;
  // Short and doesn't match prompt patterns — accept it
  if (text.length < 40) return true;
  return false;
}

/**
 * Heading-only title lift — matches web `desktopWorkTitle`.
 *
 * Locked plan:
 * - Lift markdown # / ## / ### or a real em/en-dash title
 * - Never thread.title if it equals the prompt
 * - Never resultTitle||query, never last spoken line
 * - If title === query (looks like a prompt), reject it
 * - Fall back to "Conversation"
 */
export function threadHeadingTitle(thread: Pick<ScoutThread, 'title'>): string {
  const raw = String(thread.title ?? '').trim();
  if (!raw) return 'Conversation';
  
  // Reject forbidden status patterns
  if (FORBIDDEN_STATUS_PATTERNS.some((pattern) => pattern.test(raw))) return 'Conversation';
  
  // Try to extract markdown heading first
  const mdHeading = extractMarkdownHeading(raw);
  if (mdHeading && looksLikeRealTitle(mdHeading)) return mdHeading;
  
  // Check if raw title looks like a real title (not a prompt)
  if (looksLikeRealTitle(raw)) return raw;
  
  // Title looks like a prompt — reject it
  return 'Conversation';
}

/**
 * Channel display name — heading-only lift (locked plan).
 *
 * Never fall back to raw title (the prompt), preview, or last spoken line.
 * Uses threadHeadingTitle which filters forbidden patterns.
 */
export function channelDisplayName(thread: Pick<ScoutThread, 'table' | 'title' | 'visibility' | 'preview'>): string {
  if (isBonfireChat(thread)) return 'Bonfire Chat';
  // Heading-only: never fall back to raw title (the prompt)
  const heading = threadHeadingTitle(thread);
  return thread.visibility === 'public' ? `#${heading.replace(/^#/, '')}` : heading;
}

export function pinBonfireChatFirst<T extends Pick<ScoutThread, 'table' | 'title' | 'visibility'>>(threads: readonly T[]): T[] {
  return threads
    .map((thread, index) => ({ thread, index }))
    .sort((left, right) => Number(isBonfireChat(right.thread)) - Number(isBonfireChat(left.thread)) || left.index - right.index)
    .map(({ thread }) => thread);
}
