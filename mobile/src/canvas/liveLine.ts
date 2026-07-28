/**
 * The canvas live line — design §5 of docs/plans/the-table-design.md.
 *
 * Killing the tab bar removed the conventional home of the unread badge, so the
 * canvas's one line of prose became the unread surface. This wave promotes it
 * one step further: from *describing* activity to *being* it.
 *
 *     before   "3 unread in #pricing. Dana mentioned you."     a signpost
 *     after    "Dana · Pushed the pricing memo"                the thing itself
 *
 * A signpost tells you to go look. A rendered message can be read and acted on
 * without navigating at all, which is strictly more useful in the same pixels.
 * It also makes the app feel *inhabited* — the biggest emotional risk in a
 * minimalist voice-first home screen is that it reads as a dead tool.
 *
 * This is a pure function on purpose. Mobile tests run on plain node:test with
 * no React renderer, so extracting the ladder is the only way any of it can be
 * tested — and five priority rungs plus a privacy switch is real logic that
 * should not live inside a component.
 */

export type LiveLineMention = {
  threadId: string;
	messageId?: string;
  threadName: string;
  text: string;
  authorName: string;
};

export type LiveLineInput = {
  viewerEmail: string;
  tableThreadId: string | null;
  /** Display name of the Table, already '#'-prefixed. */
  tableName: string;
  tableUnreadCount: number;
  tableLastMessage: { authorName: string; authorEmail: string; text: string } | null;
  mentions: LiveLineMention[];
  liveRooms: number;
  otherUnreadCount: number;
  otherUnreadThreads: number;
  /** Settings → "Show message previews". Off degrades previews to counts. */
  showPreviews: boolean;
};

export type LiveLineKind =
  | 'mention-table'
  | 'mention-elsewhere'
  | 'table'
  | 'rooms'
  | 'other'
  | 'none';

export type LiveLineResult = {
  kind: LiveLineKind;
  /** Rendered in text1/500 ahead of the body. Null when there is no preview. */
  author: string | null;
  /** The sentence, or null when nothing is live — the line is ABSENT, not empty. */
  text: string | null;
  /** True when at least one unread notification is addressed to this viewer. */
  mentioned: boolean;
  /** Thread to open when THE LINE is tapped, if the line names one. */
  threadId: string | null;
	/** Exact message and display title for the live-line navigation target. */
	messageId?: string | null;
	threadTitle?: string | null;
  /**
   * The Table, always — regardless of what the line happens to be showing.
   *
   * Kept separate from `threadId` because the chat circle and the line are
   * different controls: the line follows its content (a mention elsewhere opens
   * THAT channel), while a control labelled "Team" must always open the team
   * thread. Sharing one field sent the circle to whichever channel had most
   * recently mentioned you.
   */
  tableThreadId: string | null;
};

const ABSENT: LiveLineResult = {
  kind: 'none',
  author: null,
  text: null,
  mentioned: false,
  threadId: null,
  tableThreadId: null,
};

function normalizeEmail(value: string | null | undefined): string {
  return String(value ?? '').trim().toLowerCase();
}

/**
 * Collapses a message to one line. The canvas gives this two lines at most, and
 * a message containing newlines would otherwise blow the greeting's vertical
 * rhythm apart before `numberOfLines` ever clamps it.
 */
function oneLine(value: string | null | undefined): string {
  return String(value ?? '').replace(/\s+/g, ' ').trim();
}

function plural(count: number, one: string, many: string): string {
  return count === 1 ? one : many;
}

export function resolveLiveLine(input: LiveLineInput): LiveLineResult {
  const viewer = normalizeEmail(input.viewerEmail);
  const tableId = String(input.tableThreadId ?? '').trim();
  const tableName = oneLine(input.tableName) || '#team';

  // ── Rungs 1 & 2: a direct mention outranks everything. ──────────────────
  //
  // The server already draws the mention-versus-volume line for us: a targeted
  // notification carries the recipient's email, a broadcast channel post does
  // not. The caller passes only targeted ones in `mentions`, so this never
  // re-derives that distinction from message text.
  const mention = input.mentions.find((candidate) => oneLine(candidate.text).length > 0)
    ?? input.mentions[0];

  if (mention) {
    // Matched by THREAD ID, not by name — a renamed Table must still take the
    // Table rung, or the line would say "in #old-name" while the canvas's chat
    // button goes to exactly that thread.
    const inTable = tableId !== '' && String(mention.threadId).trim() === tableId;
    const body = oneLine(mention.text);

    if (!input.showPreviews) {
      return {
        kind: inTable ? 'mention-table' : 'mention-elsewhere',
        author: null,
        text: inTable
          ? `You were mentioned in ${tableName}.`
          : `You were mentioned in ${oneLine(mention.threadName) || 'a thread'}.`,
        mentioned: true,
		threadId: String(mention.threadId).trim() || null,
		messageId: String(mention.messageId ?? '').trim() || null,
		threadTitle: oneLine(mention.threadName) || null,
        tableThreadId: tableId || null,
      };
    }

    return {
      kind: inTable ? 'mention-table' : 'mention-elsewhere',
      author: oneLine(mention.authorName) || null,
      // Away from the Table the channel has to be named, or you know that
      // someone wants you but not where to go.
      text: inTable
        ? body
        : `${body} — in ${oneLine(mention.threadName) || 'another thread'}`,
      mentioned: true,
	  threadId: String(mention.threadId).trim() || null,
	  messageId: String(mention.messageId ?? '').trim() || null,
	  threadTitle: oneLine(mention.threadName) || null,
      tableThreadId: tableId || null,
    };
  }

  // ── Rung 3: the Table has unread messages. The common case. ─────────────
  if (input.tableUnreadCount > 0 && tableId !== '') {
    const last = input.tableLastMessage;
    const body = oneLine(last?.text);
    const ownLast = last !== null && viewer !== '' && normalizeEmail(last.authorEmail) === viewer;

    // Fall through entirely when the only unread message is your own. You know
    // what you said, and reflecting it back reads as a broken feed.
    if (!ownLast) {
      // No body to show — no preview, no author, but still say something
      // happened. A count is a weaker signal than a message, not no signal.
      if (!input.showPreviews || body === '') {
        return {
          kind: 'table',
          author: null,
          text: `${input.tableUnreadCount} new in ${tableName}`,
          mentioned: false,
          threadId: tableId,
          tableThreadId: tableId,
        };
      }
      return {
        kind: 'table',
        author: oneLine(last?.authorName) || null,
        text: body,
        mentioned: false,
        threadId: tableId,
        tableThreadId: tableId,
      };
    }
  }

  // ── Rung 4: rooms are live. ─────────────────────────────────────────────
  if (input.liveRooms > 0) {
    return {
      kind: 'rooms',
      author: null,
      text: `${input.liveRooms} ${plural(input.liveRooms, 'room is', 'rooms are')} live.`,
      mentioned: false,
      threadId: null,
      tableThreadId: tableId || null,
    };
  }

  // ── Rung 5: ambient volume anywhere else. A count, deliberately. ────────
  //
  // Ambient traffic in other channels never earns a preview outside the Deck —
  // only the Table and a direct mention do. Otherwise the canvas becomes a
  // firehose and the line stops meaning anything.
  if (input.otherUnreadCount > 0) {
    const threads = Math.max(1, input.otherUnreadThreads);
    return {
      kind: 'other',
      author: null,
      text:
        `${input.otherUnreadCount} unread in ${threads} ` +
        `${plural(threads, 'thread', 'threads')}.`,
      mentioned: false,
      threadId: null,
      tableThreadId: tableId || null,
    };
  }

  // Absent, not "Nothing live" — empty states that narrate their own emptiness
  // are noise, and the quiet page stays quiet (shell §9). The Table id still
  // rides along: the chat circle needs a destination even on a silent canvas,
  // which is exactly when the line is gone and the circle is the only way in.
  return { ...ABSENT, tableThreadId: tableId || null };
}
