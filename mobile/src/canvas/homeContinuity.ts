import type { Room, ScoutMessage, ScoutThread } from '../api/types';
import { safeWorkProgressNote, workFamilyLabel, workPhaseLabel } from '../messaging/workPresentation';

export type HomeContinuityDestination =
  | { route: 'Alerts' }
  | { route: 'Room'; roomId: string; title: string }
  | { route: 'Thread'; threadId: string; title: string; messageId?: string };

export type HomeContinuityItem = {
  id: string;
  kind: 'needs-you' | 'live-meeting' | 'active-work' | 'recent-thread';
  eyebrow: string;
  title: string;
  detail: string;
  destination: HomeContinuityDestination;
};

export type HomeNotification = {
  id?: unknown;
  read?: unknown;
  readBy?: unknown;
  userEmail?: unknown;
  kind?: unknown;
  text?: unknown;
  createdAt?: unknown;
};

function oneLine(value: unknown, limit = 140): string {
  const collapsed = String(value ?? '').replace(/\s+/gu, ' ').trim();
  if (collapsed.length <= limit) return collapsed;
  return `${collapsed.slice(0, Math.max(0, limit - 1)).trimEnd()}…`;
}

function normalizedEmail(value: unknown): string {
  return String(value ?? '').trim().toLowerCase();
}

function timestamp(value: unknown): number {
  const parsed = Date.parse(String(value ?? ''));
  return Number.isFinite(parsed) ? parsed : 0;
}

function latestMeaningfulThread(threads: ScoutThread[]): ScoutThread | undefined {
  return [...threads]
    .filter((thread) => !thread.archived && !thread.table && String(thread.id ?? '').trim())
    .sort((left, right) => (
      timestamp(right.updatedAt ?? right.lastMessage?.createdAt ?? right.createdAt)
      - timestamp(left.updatedAt ?? left.lastMessage?.createdAt ?? left.createdAt)
    ))[0];
}

function workMessages(threads: ScoutThread[]): Array<{ thread: ScoutThread; message: ScoutMessage }> {
  return threads.flatMap((thread) => (
    (thread.messages ?? []).flatMap((message) => message.thread ? [{ thread, message }] : [])
  ));
}

function activeWorkItem(threads: ScoutThread[]): HomeContinuityItem | null {
  const activeStatuses = new Set([
    'approval_required', 'needs_input', 'parked', 'needs_attention', 'error', 'failed',
    'queued', 'running', 'in_progress', 'working',
  ]);
  const attentionStatuses = new Set([
    'approval_required', 'needs_input', 'parked', 'needs_attention', 'error', 'failed',
  ]);
  const candidates = workMessages(threads)
    .filter(({ message }) => activeStatuses.has(String(message.thread?.status ?? '').toLowerCase()))
    .sort((left, right) => {
      const leftAttention = attentionStatuses.has(String(left.message.thread?.status ?? '').toLowerCase());
      const rightAttention = attentionStatuses.has(String(right.message.thread?.status ?? '').toLowerCase());
      if (leftAttention !== rightAttention) return leftAttention ? -1 : 1;
      return timestamp(right.message.createdAt) - timestamp(left.message.createdAt);
    });
  const candidate = candidates[0];
  const work = candidate?.message.thread;
  if (!candidate || !work) return null;

  const phase = workPhaseLabel(work);
  const family = workFamilyLabel(work);
  const threadTitle = oneLine(candidate.thread.title, 80) || 'Work';
  const title = oneLine(work.resultTitle ?? work.query, 100) || threadTitle;
  return {
    id: `work:${String(work.id ?? candidate.message.id)}`,
    kind: 'active-work',
    eyebrow: `${family} · ${phase}`,
    title,
    detail: safeWorkProgressNote(work.progressNote, phase),
    destination: {
      route: 'Thread',
      threadId: String(candidate.thread.id),
      title: threadTitle,
      messageId: String(candidate.message.id),
    },
  };
}

function needsYouItem(notifications: HomeNotification[], viewerEmail: string): HomeContinuityItem | null {
  const viewer = normalizedEmail(viewerEmail);
  const unread = notifications.filter((item) => {
    if (item.read === true) return false;
    const readBy = Array.isArray(item.readBy) ? item.readBy.map(normalizedEmail) : [];
    if (viewer && readBy.includes(viewer)) return false;
    const addressed = viewer !== '' && normalizedEmail(item.userEmail) === viewer;
    const kind = String(item.kind ?? '').toLowerCase();
    return addressed || kind === 'alert' || kind === 'task';
  }).sort((left, right) => timestamp(right.createdAt) - timestamp(left.createdAt));
  if (!unread.length) return null;
  const first = unread[0];
  const detail = oneLine(first.text, 120)
    || `${unread.length} ${unread.length === 1 ? 'update needs' : 'updates need'} your attention`;
  return {
    id: `needs:${String(first.id ?? unread.length)}`,
    kind: 'needs-you',
    eyebrow: 'Needs you',
    title: detail,
    detail: unread.length > 1 ? `${unread.length} updates waiting` : 'Open to respond',
    destination: { route: 'Alerts' },
  };
}

function liveMeetingItem(rooms: Room[]): HomeContinuityItem | null {
  const room = [...rooms]
    .filter((candidate) => candidate.live && !candidate.archived)
    .sort((left, right) => right.participantCount - left.participantCount)[0];
  if (!room) return null;
  const count = Math.max(0, Number(room.participantCount) || 0);
  return {
    id: `room:${String(room.id)}`,
    kind: 'live-meeting',
    eyebrow: 'Live meeting',
    title: oneLine(room.name, 80) || 'Meeting',
    detail: count > 0 ? `${count} ${count === 1 ? 'person is' : 'people are'} here` : 'Join the conversation',
    destination: { route: 'Room', roomId: String(room.id), title: String(room.name || 'Meeting') },
  };
}

function recentThreadItem(threads: ScoutThread[]): HomeContinuityItem | null {
  const thread = latestMeaningfulThread(threads);
  if (!thread) return null;
  const title = oneLine(thread.title, 80) || 'Conversation';
  const detail = oneLine(thread.lastMessage?.text ?? thread.preview, 120) || 'Pick up where you left off';
  return {
    id: `thread:${String(thread.id)}`,
    kind: 'recent-thread',
    eyebrow: 'Continue',
    title,
    detail,
    destination: { route: 'Thread', threadId: String(thread.id), title },
  };
}

/** Home is a re-entry surface, not a dashboard; at most three decisions show. */
export function buildHomeContinuity(input: {
  viewerEmail: string;
  notifications: HomeNotification[];
  rooms: Room[];
  threads: ScoutThread[];
}): HomeContinuityItem[] {
  const ordered = [
    needsYouItem(input.notifications, input.viewerEmail),
    liveMeetingItem(input.rooms),
    activeWorkItem(input.threads),
    recentThreadItem(input.threads),
  ].filter((item): item is HomeContinuityItem => Boolean(item));
  const seen = new Set<string>();
  return ordered.filter((item) => {
    const target = item.destination.route === 'Thread' ? `thread:${item.destination.threadId}` : item.id;
    if (seen.has(target)) return false;
    seen.add(target);
    return true;
  }).slice(0, 3);
}
