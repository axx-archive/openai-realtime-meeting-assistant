/**
 * @-mention rendering — design §14.
 *
 * Mirrors `chat_mentions.go` so the client highlights exactly what the server
 * would notify on. The rules that matter, both taken from the Go implementation:
 *
 *   - A mention needs a word boundary before the "@", so an email address
 *     (aj@shareability.com) is never a mention.
 *   - The handle runs through letters, digits, dots, underscores, and dashes,
 *     so canonical multiword handles such as "@Insights-Analyst" stay whole.
 *     Other trailing punctuation still ends the mention.
 *
 * `@scout` is deliberately styled apart: server-side it is not a notification
 * target at all, it gates the answer path — the agent is a participant, not a
 * pager.
 */

export type MentionSegment =
  | { kind: 'text'; text: string }
  | { kind: 'mention'; text: string; name: string; scout: boolean };

function isNameChar(char: string): boolean {
  return /[\p{L}\p{N}._-]/u.test(char);
}

/**
 * Splits message text into renderable segments. Pure and cheap, but call sites
 * must still memoize per message — re-parsing every message on every render is
 * the obvious performance trap in a chat list (§15).
 */
export function parseMentions(text: string): MentionSegment[] {
  const segments: MentionSegment[] = [];
  let buffer = '';

  for (let index = 0; index < text.length; index += 1) {
    if (text[index] !== '@') {
      buffer += text[index];
      continue;
    }
    // Word-boundary rule: a name character before the "@" means this is inside
    // a token (an email), not a mention.
    if (index > 0 && isNameChar(text[index - 1])) {
      buffer += text[index];
      continue;
    }
    let end = index + 1;
    while (end < text.length && isNameChar(text[end])) end += 1;
    let mentionEnd = end;
    while (mentionEnd > index + 1 && text[mentionEnd - 1] === '.') mentionEnd -= 1;
    if (mentionEnd === index + 1) {
      buffer += text[index];
      continue;
    }
    const name = text.slice(index + 1, mentionEnd);
    if (buffer) {
      segments.push({ kind: 'text', text: buffer });
      buffer = '';
    }
    segments.push({
      kind: 'mention',
      text: text.slice(index, mentionEnd),
      name,
      scout: name.toLowerCase() === 'scout',
    });
    index = mentionEnd - 1;
  }

  if (buffer) segments.push({ kind: 'text', text: buffer });
  return segments;
}
