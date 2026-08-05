import type { ScoutMessage } from '../api/types';

export type ThreadReplyTopology = {
  feedMessages: ScoutMessage[];
  rootFor: (message: ScoutMessage | string) => ScoutMessage | undefined;
  repliesFor: (message: ScoutMessage | string) => ScoutMessage[];
};

/**
 * Replies are durable messages, but they are not second copies of the channel
 * conversation. This projection keeps roots in the feed and resolves every
 * direct or nested reply back to the root-owned thread surface.
 *
 * An orphaned reply stays in the feed. Hiding a message whose referenced root
 * is absent would make server drift look like data loss to the reader.
 */
export function buildThreadReplyTopology(messages: readonly ScoutMessage[]): ThreadReplyTopology {
  const list = [...messages];
  const byID = new Map(
    list
      .map((message) => [String(message.id ?? '').trim(), message] as const)
      .filter(([id]) => Boolean(id)),
  );
  const rootIDByID = new Map<string, string>();

  const rootIDFor = (message: ScoutMessage): string => {
    const messageID = String(message.id ?? '').trim();
    if (!messageID) return '';
    const cached = rootIDByID.get(messageID);
    if (cached) return cached;

    let current = message;
    const seen = new Set([messageID]);
    while (current.replyTo?.messageId) {
      const parentID = String(current.replyTo.messageId).trim();
      const parent = byID.get(parentID);
      if (!parent || seen.has(parentID)) {
        rootIDByID.set(messageID, messageID);
        return messageID;
      }
      seen.add(parentID);
      current = parent;
    }

    const rootID = String(current.id ?? messageID).trim() || messageID;
    rootIDByID.set(messageID, rootID);
    return rootID;
  };

  const repliesByRootID = new Map<string, ScoutMessage[]>();
  for (const message of list) {
    const messageID = String(message.id ?? '').trim();
    if (!messageID || !message.replyTo?.messageId) continue;
    const rootID = rootIDFor(message);
    if (!rootID || rootID === messageID) continue;
    const replies = repliesByRootID.get(rootID) ?? [];
    replies.push(message);
    repliesByRootID.set(rootID, replies);
  }

  const resolveMessage = (message: ScoutMessage | string): ScoutMessage | undefined => (
    typeof message === 'string' ? byID.get(message) : message
  );
  const rootFor = (message: ScoutMessage | string): ScoutMessage | undefined => {
    const resolved = resolveMessage(message);
    if (!resolved) return undefined;
    return byID.get(rootIDFor(resolved)) ?? resolved;
  };

  return {
    feedMessages: list.filter((message) => rootIDFor(message) === String(message.id ?? '').trim()),
    rootFor,
    repliesFor(message) {
      const root = rootFor(message);
      return root ? [...(repliesByRootID.get(String(root.id ?? '').trim()) ?? [])] : [];
    },
  };
}
