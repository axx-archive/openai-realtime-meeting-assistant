import { useCallback, useEffect, useState } from 'react';
import { useFocusEffect } from '@react-navigation/native';
import { api } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { firstArray } from '../utils/records';

/**
 * The canvas live line — design §14.5.
 *
 * Killing the tab bar removed the conventional home of the unread badge, so the
 * canvas's one line of prose becomes the unread surface. A sentence beats a dot
 * because it can say *who* and *where*:
 *
 *     "Three unread in #pricing. Dana mentioned you."
 *
 * The escalation rule is the important part: ambient channel volume never earns
 * pixels outside the Deck — only a DIRECT MENTION does. The server already draws
 * that line (a targeted notification carries `userEmail`; a broadcast channel
 * post does not), and the client must not flatten it back into one
 * undifferentiated red dot.
 */

export type LiveLine = {
  /** The sentence, or null when nothing is live — the line is ABSENT, not empty. */
  text: string | null;
  /** True when at least one unread notification is addressed to this user. */
  mentioned: boolean;
  unreadCount: number;
  liveRooms: number;
  /** Thread to open when the line is tapped, if the line names one. */
  threadId: string | null;
};

type NotificationRow = {
  read?: boolean;
  readBy?: string[];
  userEmail?: string;
  kind?: string;
  text?: string;
  threadId?: string;
};

const EMPTY: LiveLine = {
  text: null,
  mentioned: false,
  unreadCount: 0,
  liveRooms: 0,
  threadId: null,
};

function plural(count: number, one: string, many: string): string {
  return count === 1 ? one : many;
}

export function useLiveLine(): LiveLine {
  const { sessionToken, user } = useAuth();
  const office = useOfficeEvents();
  const [line, setLine] = useState<LiveLine>(EMPTY);

  const load = useCallback(async () => {
    if (!sessionToken) return;
    try {
      const [rooms, alerts] = await Promise.all([
        api.rooms(sessionToken),
        api.notifications(sessionToken),
      ]);

      const liveRooms = (rooms.rooms ?? []).filter((room) => room.live).length;
      const email = user?.email?.trim().toLowerCase() ?? '';
      const unread = firstArray(alerts, ['notifications']).filter((row) => {
        const item = row as NotificationRow;
        if (item.read) return false;
        if (email && item.readBy?.some((value) => value.toLowerCase() === email)) return false;
        return true;
      }) as NotificationRow[];

      // A targeted notification carries the recipient; a broadcast channel post
      // does not. That is the mention-versus-volume distinction, straight from
      // the server rather than re-derived from message text.
      const mentions = unread.filter(
        (item) => email && item.userEmail?.trim().toLowerCase() === email,
      );

      const parts: string[] = [];
      if (liveRooms > 0) {
        parts.push(`${liveRooms} ${plural(liveRooms, 'room is', 'rooms are')} live.`);
      }
      if (unread.length > 0) {
        parts.push(`${unread.length} unread ${plural(unread.length, 'message', 'messages')}.`);
      }
      if (mentions.length > 0) {
        const first = mentions[0]?.text?.trim();
        parts.push(first ? first.replace(/\s+/g, ' ') : 'Someone mentioned you.');
      }

      setLine({
        // Absent, not "Nothing live" — empty states that narrate their own
        // emptiness are noise, and the quiet page stays quiet (§9).
        text: parts.length ? parts.join(' ') : null,
        mentioned: mentions.length > 0,
        unreadCount: unread.length,
        liveRooms,
        threadId: mentions[0]?.threadId ?? null,
      });
    } catch {
      // A failed poll leaves the previous line in place rather than blanking
      // the canvas — a transient network blip should not look like "all clear".
    }
  }, [sessionToken, user?.email]);

  useFocusEffect(
    useCallback(() => {
      void load();
    }, [load]),
  );

  useEffect(() => {
    if (!['rooms', 'participants', 'notification', 'notification_backlog', 'chat_thread'].includes(office.event ?? '')) {
      return;
    }
    void load();
  }, [load, office.event, office.version]);

  return line;
}
