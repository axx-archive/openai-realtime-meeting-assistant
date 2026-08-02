/**
 * Push deep links — design §8.
 *
 * A notification is an untrusted receipt for ONE specific server record.
 * Navigation fields are accepted only from the current account's authenticated
 * notification projection, never directly from an APNs/Expo payload.
 */

export type PushCandidate = {
  notificationId: string;
};

export type PushTarget = {
  notificationId: string;
  accountKey: string;
  threadId: string;
  messageId: string | null;
  threadName: string | null;
};

function stringOrNull(value: unknown): string | null {
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : null;
}

/**
 * Parses a notification's `data` payload into an untrusted receipt.
 *
 * Everything is validated rather than coerced: this data crosses a process
 * boundary from a push service, so a non-string id must be rejected, not
 * String()-ed into a route parameter that then fails to resolve.
 */
export function parsePushTarget(data: unknown): PushCandidate | null {
  if (typeof data !== 'object' || data === null) return null;
  const record = data as Record<string, unknown>;

  const notificationId = stringOrNull(record.notificationId);
  if (!notificationId) return null;

  return { notificationId };
}

/**
 * Converts an untrusted push receipt into a route only after finding that exact
 * notification in the current account's authenticated server projection.
 * Thread/message fields are derived from that projection, never from APNs.
 */
export function resolveAuthorizedPushTarget(
  candidate: PushCandidate,
  notifications: unknown[],
  accountKey: string,
): PushTarget | null {
  const normalizedAccount = accountKey.trim().toLowerCase();
  if (!normalizedAccount) return null;
  const notification = notifications.find((value) => (
    typeof value === 'object'
    && value !== null
    && stringOrNull((value as Record<string, unknown>).id) === candidate.notificationId
  ));
  if (typeof notification !== 'object' || notification === null) return null;
  const record = notification as Record<string, unknown>;
  const threadId = stringOrNull(record.threadId);
  if (!threadId) return null;

  return {
    notificationId: candidate.notificationId,
    accountKey: normalizedAccount,
    threadId,
    messageId: stringOrNull(record.messageId),
    threadName: stringOrNull(record.threadName),
  };
}
