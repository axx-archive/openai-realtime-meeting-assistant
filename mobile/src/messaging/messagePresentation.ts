import { parseMentions, type MentionSegment } from './mentions';

const httpUrlPattern = /https?:\/\/[^\s<>"']+/giu;
const sentencePunctuation = new Set(['.', ',', ';', ':', '!', '?']);

export type HttpUrlMatch = {
  /** The exact safe http(s) URL text, excluding surrounding punctuation. */
  url: string;
  start: number;
  end: number;
};

export type MessageTextSegment =
  | MentionSegment
  | { kind: 'link'; text: string; url: string };

export type PersistedReactionRecord = {
  emoji: string;
  /** Server-stamped identity; clients must never choose this value. */
  actorEmail: string;
};

export type MessageReactionGroup = {
  emoji: string;
  count: number;
  reactedByViewer: boolean;
};

export type MessageOwnershipCandidate = {
  role?: unknown;
  authorEmail?: unknown;
};

export type MessageOwnershipContext = {
  viewerEmail?: unknown;
  threadVisibility?: unknown;
  threadOwnerEmail?: unknown;
};

export type ActiveMentionQuery = { start: number; query: string };

function normalizedString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

function normalizedEmail(value: unknown): string {
  return normalizedString(value).toLowerCase();
}

function unmatchedClosingBracket(value: string, open: string, close: string): boolean {
  let balance = 0;
  for (const character of value) {
    if (character === open) balance += 1;
    if (character === close) balance -= 1;
  }
  return balance < 0;
}

/**
 * Removes prose punctuation greedily captured after a URL. Balanced closing
 * brackets remain part of the URL, so a Wikipedia-style `Foo_(bar)` link is
 * not damaged while `(https://example.com).` still leaves `).` in the text.
 */
function trimUrlTail(candidate: string): string {
  let value = candidate;
  while (value) {
    const last = value[value.length - 1];
    if (sentencePunctuation.has(last)) {
      value = value.slice(0, -1);
      continue;
    }
    if (
      (last === ')' && unmatchedClosingBracket(value, '(', ')')) ||
      (last === ']' && unmatchedClosingBracket(value, '[', ']')) ||
      (last === '}' && unmatchedClosingBracket(value, '{', '}'))
    ) {
      value = value.slice(0, -1);
      continue;
    }
    break;
  }
  return value;
}

function isSafeHttpUrl(value: string): boolean {
  try {
    const parsed = new URL(value);
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && Boolean(parsed.hostname);
  } catch {
    return false;
  }
}

/** Finds only valid http(s) URLs and reports spans that exclude prose tails. */
export function extractHttpUrls(text: string): HttpUrlMatch[] {
  const matches: HttpUrlMatch[] = [];
  for (const match of text.matchAll(httpUrlPattern)) {
    const start = match.index;
    const url = trimUrlTail(match[0]);
    if (start === undefined || !url || !isSafeHttpUrl(url)) continue;
    matches.push({ url, start, end: start + url.length });
  }
  return matches;
}

/**
 * Splits message text for native rendering. URLs are isolated before mention
 * parsing so an `@name` inside a URL never becomes a mention chip.
 */
export function parseMessageTextSegments(text: string): MessageTextSegment[] {
  const segments: MessageTextSegment[] = [];
  let cursor = 0;

  const appendMentions = (plain: string) => {
    if (plain) segments.push(...parseMentions(plain));
  };

  for (const match of extractHttpUrls(text)) {
    appendMentions(text.slice(cursor, match.start));
    segments.push({ kind: 'link', text: match.url, url: match.url });
    cursor = match.end;
  }
  appendMentions(text.slice(cursor));
  return segments;
}

/**
 * Groups one persisted record per reactor and emoji. Duplicate records from
 * the same normalized account are ignored, preserving first-seen emoji order.
 */
export function groupMessageReactions(
  reactions: readonly PersistedReactionRecord[] | null | undefined,
  viewerEmail: unknown,
): MessageReactionGroup[] {
  const viewer = normalizedEmail(viewerEmail);
  const groups = new Map<string, { reactors: Set<string>; reactedByViewer: boolean }>();

  for (const reaction of reactions ?? []) {
    const emoji = normalizedString(reaction?.emoji);
    const reactor = normalizedEmail(reaction?.actorEmail);
    if (!emoji || !reactor) continue;

    let group = groups.get(emoji);
    if (!group) {
      group = { reactors: new Set<string>(), reactedByViewer: false };
      groups.set(emoji, group);
    }
    group.reactors.add(reactor);
    if (viewer && reactor === viewer) group.reactedByViewer = true;
  }

  return Array.from(groups, ([emoji, group]) => ({
    emoji,
    count: group.reactors.size,
    reactedByViewer: group.reactedByViewer,
  }));
}

/**
 * Matches the server's edit/delete ownership rule. A stamped user message is
 * owned only by that account. A legacy unstamped message is manageable only in
 * a non-public thread whose owner is the current viewer.
 */
export function isOwnMessageForViewer(
  message: MessageOwnershipCandidate,
  context: MessageOwnershipContext,
): boolean {
  if (normalizedString(message.role).toLowerCase() !== 'user') return false;

  const viewer = normalizedEmail(context.viewerEmail);
  if (!viewer) return false;

  const author = normalizedEmail(message.authorEmail);
  if (author) return author === viewer;

  if (normalizedString(context.threadVisibility).toLowerCase() === 'public') return false;
  return normalizedEmail(context.threadOwnerEmail) === viewer;
}

/** Returns the mention fragment currently being typed at the end of a draft. */
export function activeMentionQuery(text: string): ActiveMentionQuery | null {
  const match = /(?:^|\s)@([\p{L}\p{N}._-]*)$/u.exec(text);
  if (!match || match.index === undefined) return null;
  const at = match.index + (match[0].startsWith('@') ? 0 : 1);
  return { start: at, query: match[1] ?? '' };
}

/** Completes the active mention while keeping the canonical @Name wire token. */
export function completeMention(text: string, name: string): string {
  const active = activeMentionQuery(text);
  const cleanName = normalizedString(name).replace(/^@+/, '');
  if (!active || !cleanName) return text;
  return `${text.slice(0, active.start)}@${cleanName} `;
}
