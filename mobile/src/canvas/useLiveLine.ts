import { useCallback, useEffect, useState } from 'react';
import { useFocusEffect } from '@react-navigation/native';
import { api } from '../api/client';
import type { ScoutMessage, ScoutThread } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { firstArray } from '../utils/records';
import { resolveLiveLine, type LiveLineInput, type LiveLineResult } from './liveLine';
import { useShowPreviews } from './previewPreference';

/**
 * The canvas live line — design §5.
 *
 * This hook fetches; `resolveLiveLine` decides. The split exists because mobile
 * tests run on plain node:test with no React renderer, so the five-rung ladder
 * is only testable outside a hook — and the arbitration is the part with real
 * rules in it (see canvas/liveLine.ts and __tests__/liveLine.test.ts).
 *
 * The mention-versus-volume distinction comes straight from the server: a
 * targeted notification carries `userEmail`, a broadcast channel post does not.
 * It is never re-derived from message text.
 */

export type LiveLine = LiveLineResult;

type NotificationRow = {
  read?: boolean;
  readBy?: string[];
  userEmail?: string;
  kind?: string;
  text?: string;
  threadId?: string;
  authorName?: string;
};

const EMPTY: LiveLine = {
  kind: 'none',
  author: null,
  text: null,
  mentioned: false,
  threadId: null,
};

function channelLabel(thread: ScoutThread | undefined): string {
  const title = String(thread?.title || 'team').trim();
  return `#${title.replace(/^#/, '')}`;
}

function lastFrom(thread: ScoutThread | undefined): LiveLineInput['tableLastMessage'] {
  const messages = (thread?.messages ?? []) as ScoutMessage[];
  const last = messages[messages.length - 1];
  if (!last) return null;
  return {
    authorName: String(last.authorName ?? '').trim(),
    authorEmail: String(last.authorEmail ?? '').trim(),
    text: String(last.text ?? last.content ?? '').trim(),
  };
}

export function useLiveLine(): LiveLine {
  const { sessionToken, user } = useAuth();
  const office = useOfficeEvents();
  const { showPreviews } = useShowPreviews();
  const [line, setLine] = useState<LiveLine>(EMPTY);

  const load = useCallback(async () => {
    if (!sessionToken) return;
    try {
      const [rooms, alerts, threadList] = await Promise.all([
        api.rooms(sessionToken),
        api.notifications(sessionToken),
        api.scoutThreads(sessionToken),
      ]);

      const email = user?.email?.trim().toLowerCase() ?? '';
      const threads = threadList.threads ?? [];
      const table = threads.find((thread) => thread.table === true);
      const tableId = table ? String(table.id) : null;

      const unread = firstArray(alerts, ['notifications']).filter((row) => {
        const item = row as NotificationRow;
        if (item.read) return false;
        if (email && item.readBy?.some((value) => value.toLowerCase() === email)) return false;
        return true;
      }) as NotificationRow[];

      // Targeted only — the server already drew this line for us.
      const mentions = unread
        .filter((item) => email && item.userEmail?.trim().toLowerCase() === email)
        .map((item) => {
          const mentionThread = threads.find((thread) => String(thread.id) === String(item.threadId));
          return {
            threadId: String(item.threadId ?? ''),
            threadName: channelLabel(mentionThread),
            text: String(item.text ?? '').trim(),
            authorName: String(item.authorName ?? '').trim(),
          };
        });

      // Ambient volume in every thread that is NOT the Table — the Table gets
      // its own rung with a real preview, so counting it here would double it.
      const otherUnreadThreads = threads.filter(
        (thread) => !thread.table && (thread.unreadCount ?? 0) > 0,
      );
      const otherUnreadCount = otherUnreadThreads.reduce(
        (total, thread) => total + (thread.unreadCount ?? 0),
        0,
      );

      setLine(
        resolveLiveLine({
          viewerEmail: email,
          tableThreadId: tableId,
          tableName: channelLabel(table),
          tableUnreadCount: table?.unreadCount ?? 0,
          tableLastMessage: lastFrom(table),
          mentions,
          liveRooms: (rooms.rooms ?? []).filter((room) => room.live).length,
          otherUnreadCount,
          otherUnreadThreads: otherUnreadThreads.length,
          showPreviews,
        }),
      );
    } catch {
      // A failed poll leaves the previous line in place rather than blanking
      // the canvas — a transient network blip should not look like "all clear".
    }
  }, [sessionToken, showPreviews, user?.email]);

  useFocusEffect(
    useCallback(() => {
      void load();
    }, [load]),
  );

  useEffect(() => {
    if (
      !['rooms', 'participants', 'notification', 'notification_backlog', 'chat_thread'].includes(
        office.event ?? '',
      )
    ) {
      return;
    }
    void load();
  }, [load, office.event, office.version]);

  return line;
}
