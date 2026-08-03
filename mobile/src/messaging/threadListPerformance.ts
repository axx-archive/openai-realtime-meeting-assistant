import type { ScoutMessage } from '../api/types';

export type ThreadListRow = {
  message: ScoutMessage;
  own: boolean;
  showAuthor: boolean;
  showAvatar: boolean;
  avatarDataURL?: string;
  timelineLabel?: string;
  boundaryLabel?: string;
  showCatchUp: boolean;
};

/**
 * FlashList reuses native cells most efficiently when materially different
 * message shapes do not share the same recycle pool. Markers add their own
 * vertical layout, while Scout, own, and teammate bubbles each have distinct
 * content and alignment.
 */
export function threadRowRecycleType(row: ThreadListRow): string {
  const role = String(row.message.role ?? '').toLowerCase();
  const family = row.own ? 'own' : role === 'assistant' || role === 'scout' ? 'scout' : 'teammate';
  return row.timelineLabel || row.boundaryLabel ? `marker-${family}` : family;
}

/**
 * Appending a message rebuilds lightweight row metadata, but historical rows
 * keep the same message object. Compare the presentation fields explicitly so
 * an append only rerenders the prior run-end and the new message—not every
 * visible rich bubble.
 */
export function threadRowPresentationEqual(
  left: ThreadListRow,
  right: ThreadListRow,
): boolean {
  return left.message === right.message
    && left.own === right.own
    && left.showAuthor === right.showAuthor
    && left.showAvatar === right.showAvatar
    && left.avatarDataURL === right.avatarDataURL
    && left.timelineLabel === right.timelineLabel
    && left.boundaryLabel === right.boundaryLabel
    && left.showCatchUp === right.showCatchUp;
}
