/**
 * Push deep links — design §8.
 *
 * A notification is a request to see ONE specific thing. Landing on the canvas
 * would make the user navigate twice to reach what they were just told about,
 * so a payload that does not name a thread yields null rather than falling back
 * to a home screen (shell §14.5).
 */

export type PushTarget = {
  threadId: string;
  messageId: string | null;
  threadName: string | null;
};

function stringOrNull(value: unknown): string | null {
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : null;
}

/**
 * Parses a notification's `data` payload into a navigation target.
 *
 * Everything is validated rather than coerced: this data crosses a process
 * boundary from a push service, so a non-string id must be rejected, not
 * String()-ed into a route parameter that then fails to resolve.
 */
export function parsePushTarget(data: unknown): PushTarget | null {
  if (typeof data !== 'object' || data === null) return null;
  const record = data as Record<string, unknown>;

  const threadId = stringOrNull(record.threadId);
  if (!threadId) return null;

  return {
    threadId,
    messageId: stringOrNull(record.messageId),
    threadName: stringOrNull(record.threadName),
  };
}
