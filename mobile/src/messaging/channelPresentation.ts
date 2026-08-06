import type { ScoutThread } from '../api/types';

export function isBonfireChat(thread: Pick<ScoutThread, 'table' | 'title' | 'visibility'> | null | undefined): boolean {
  if (!thread || String(thread.visibility ?? '').toLowerCase() !== 'public') return false;
  const title = String(thread.title ?? '').trim().toLowerCase();
  return thread.table === true || title === 'team' || title === 'general' || title === 'bonfire chat';
}

export function channelDisplayName(thread: Pick<ScoutThread, 'table' | 'title' | 'visibility' | 'preview'>): string {
  if (isBonfireChat(thread)) return 'Bonfire Chat';
  const title = String(thread.title || thread.preview || 'Thread').trim();
  return thread.visibility === 'public' ? `#${title.replace(/^#/, '')}` : title;
}

export function pinBonfireChatFirst<T extends Pick<ScoutThread, 'table' | 'title' | 'visibility'>>(threads: readonly T[]): T[] {
  return threads
    .map((thread, index) => ({ thread, index }))
    .sort((left, right) => Number(isBonfireChat(right.thread)) - Number(isBonfireChat(left.thread)) || left.index - right.index)
    .map(({ thread }) => thread);
}
