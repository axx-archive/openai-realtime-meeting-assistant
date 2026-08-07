import type { ScoutMessage } from '../api/types';

export type ThreadListRow = {
  message: ScoutMessage;
  threadReplies: readonly ScoutMessage[];
  own: boolean;
  showAuthor: boolean;
  showAvatar: boolean;
  avatarDataURL?: string;
  timelineLabel?: string;
  boundaryLabel?: string;
  showCatchUp: boolean;
};

function messageBody(message: ScoutMessage): string {
  return String(message.text ?? message.content ?? '');
}

/**
 * Keep materially different native subtrees out of the same recycle pool.
 * Image and preview cells retain native image state, while long/rich cells
 * have substantially different measurement costs from ordinary text.
 */
export function threadMessageContentFamily(message: ScoutMessage): string {
  const kind = String(message.kind ?? '').toLowerCase();
  if (kind === 'thread' && message.thread) return 'work-thread';
  if (kind === 'image' && message.image) return 'generated-image';
  if (kind === 'image_pending') return 'image-pending';

  const files = Array.isArray(message.files) ? message.files : [];
  if (files.some((file) => String(file.mime ?? '').toLowerCase().startsWith('image/'))) return 'image';
  if (files.length > 0) return 'file';

  const body = messageBody(message);
  if (/https?:\/\/[^\s]+/iu.test(body)) return 'link';

  const role = String(message.role ?? '').toLowerCase();
  const long = body.length > 700 || body.split('\n').length > 12;
  if (role === 'assistant' || role === 'scout') return long ? 'rich-long' : 'rich';
  return long ? 'long' : 'text';
}

/** Live updates follow the tail only while the viewer has not taken control. */
export function shouldFollowThreadTail(atBottom: boolean, userInteracting: boolean): boolean {
  return atBottom && !userInteracting;
}

export type ThreadScrollInteractionEvent =
  | 'drag-begin'
  | 'drag-end'
  | 'momentum-begin'
  | 'momentum-end';

/**
 * Keep live tail-follow fenced across the small native gap between a drag
 * ending and momentum beginning. A missing velocity is treated conservatively
 * and released by the caller's short fallback timer if momentum never starts.
 */
export function nextThreadScrollInteraction(
  _current: boolean,
  event: ThreadScrollInteractionEvent,
  velocityY?: number,
): boolean {
  if (event === 'drag-begin' || event === 'momentum-begin') return true;
  if (event === 'momentum-end') return false;
  if (!Number.isFinite(velocityY)) return true;
  return Math.abs(velocityY ?? 0) > 0.01 ? true : false;
}

/**
 * FlashList reuses native cells most efficiently when materially different
 * message shapes do not share the same recycle pool. Markers add their own
 * vertical layout, while Scout, own, and teammate bubbles each have distinct
 * content and alignment.
 */
export function threadRowRecycleType(row: ThreadListRow): string {
  const role = String(row.message.role ?? '').toLowerCase();
  const family = row.own ? 'own' : role === 'assistant' || role === 'scout' ? 'scout' : 'teammate';
  const marker = row.timelineLabel || row.boundaryLabel ? 'marker-' : '';
  return `${marker}${family}-${threadMessageContentFamily(row.message)}`;
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
    && left.threadReplies === right.threadReplies
    && left.own === right.own
    && left.showAuthor === right.showAuthor
    && left.showAvatar === right.showAvatar
    && left.avatarDataURL === right.avatarDataURL
    && left.timelineLabel === right.timelineLabel
    && left.boundaryLabel === right.boundaryLabel
    && left.showCatchUp === right.showCatchUp;
}
