import type { ScoutMessage, ScoutThread } from '../api/types';
import { channelDisplayName, isBonfireChat, pinBonfireChatFirst } from './channelPresentation';
import { workFamilyLabel, workPhaseLabel } from './workPresentation';

const activeWorkStatuses = new Set(['queued', 'running', 'approval_required', 'needs_input', 'parked']);

export type ChannelActiveWork = {
  message: ScoutMessage;
  work: NonNullable<ScoutMessage['thread']>;
};

export type ChannelListRow =
  | { kind: 'section'; id: string; label: string }
  | { kind: 'thread'; id: string; thread: ScoutThread };

export function channelActiveWork(thread: ScoutThread): ChannelActiveWork | null {
  const messages = Array.isArray(thread.messages) ? thread.messages : [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (!message.thread || !activeWorkStatuses.has(String(message.thread.status ?? '').toLowerCase())) continue;
    return { message, work: message.thread };
  }
  return null;
}

export function channelListRows(threads: readonly ScoutThread[]): ChannelListRow[] {
  const ordered = pinBonfireChatFirst([...threads]);
  const sections = [
    { label: 'CHANNELS', threads: ordered.filter((thread) => thread.visibility === 'public') },
    { label: 'PRIVATE', threads: ordered.filter((thread) => thread.visibility !== 'public') },
  ].filter((section) => section.threads.length > 0);
  return sections.flatMap((section) => [
    { kind: 'section' as const, id: `section-${section.label}`, label: section.label },
    ...section.threads.map((thread) => ({ kind: 'thread' as const, id: String(thread.id), thread })),
  ]);
}

export function channelThreadAccessibilityLabel(thread: ScoutThread, working: ChannelActiveWork | null): string {
  const parts = [channelDisplayName(thread)];
  if (isBonfireChat(thread)) parts.push('pinned channel');
  const unread = Math.max(0, Number(thread.unreadCount ?? 0));
  if (unread > 0) parts.push(`${unread > 99 ? '99 plus' : unread} unread`);
  if (working) {
    const agent = String(working.work.agentName ?? 'Scout');
    parts.push(`${agent} working on ${workFamilyLabel(working.work)}, ${workPhaseLabel(working.work)}`);
  }
  return parts.join(', ');
}
