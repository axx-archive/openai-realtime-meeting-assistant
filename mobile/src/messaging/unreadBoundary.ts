/**
 * The unread boundary — design §9 of docs/plans/the-table-design.md.
 *
 * Opening a channel with 80 unread is the everything-channel's defining moment,
 * so it is designed rather than defaulted. iMessage lands you at the bottom,
 * which is right for a five-message thread and wrong for an eighty-message one.
 * Slack's boundary behaviour is correct for volume: land where you stopped
 * reading, show a divider, and hide nothing.
 */

export type BoundaryMessage = {
  id?: string;
  createdAt?: string;
  authorEmail?: string;
};

function parse(value: string | undefined): number {
  if (!value) return Number.NaN;
  const at = Date.parse(value);
  return Number.isNaN(at) ? Number.NaN : at;
}

/**
 * Index of the first message the viewer has not read, or -1 when everything is
 * read (no divider renders).
 *
 * The viewer's own messages never open the unread run. Sending from another
 * device would otherwise draw a "new messages" line directly above your own
 * text, which reads as a bug every single time.
 *
 * A message with an unparseable timestamp is treated as read, matching the
 * server's `threadUnreadCount`: a message that cannot be placed in time cannot
 * honestly be called new, and the two sides disagreeing would put a divider
 * where the count says there is nothing.
 */
export function firstUnreadIndex(
  messages: BoundaryMessage[],
  readAt: string | undefined,
  viewerEmail: string | undefined,
): number {
  const since = parse(readAt);
  const viewer = String(viewerEmail ?? '').trim().toLowerCase();

  for (let index = 0; index < messages.length; index += 1) {
    const message = messages[index];
    const author = String(message.authorEmail ?? '').trim().toLowerCase();
    if (viewer !== '' && author === viewer) continue;

    const created = parse(message.createdAt);
    if (Number.isNaN(created)) continue;

    // No marker at all means nothing has been read — the first message from
    // someone else opens the run.
    if (Number.isNaN(since) || created > since) return index;
  }
  return -1;
}
