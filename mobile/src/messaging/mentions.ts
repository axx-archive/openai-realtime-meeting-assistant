/**
 * @-mention rendering — design §14.
 *
 * Mirrors `chat_mentions.go` so the client highlights exactly what the server
 * would notify on. The rules that matter, both taken from the Go implementation:
 *
 *   - A mention needs a word boundary before the "@", so an email address
 *     (aj@shareability.com) is never a mention.
 *   - The name runs while the characters are letters or digits, so trailing
 *     punctuation ends it ("@tyler," hits) but a longer word does not
 *     ("@tylerish" is its own name).
 *
 * `@scout` is deliberately styled apart: server-side it is not a notification
 * target at all, it gates the answer path — the agent is a participant, not a
 * pager.
 */

export type MentionSegment =
  | { kind: 'text'; text: string }
  | { kind: 'mention'; text: string; name: string; scout: boolean };

function isNameChar(char: string): boolean {
  return /[\p{L}\p{N}]/u.test(char);
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
    if (end === index + 1) {
      buffer += text[index];
      continue;
    }
    const name = text.slice(index + 1, end);
    if (buffer) {
      segments.push({ kind: 'text', text: buffer });
      buffer = '';
    }
    segments.push({
      kind: 'mention',
      text: text.slice(index, end),
      name,
      scout: name.toLowerCase() === 'scout',
    });
    index = end - 1;
  }

  if (buffer) segments.push({ kind: 'text', text: buffer });
  return segments;
}
