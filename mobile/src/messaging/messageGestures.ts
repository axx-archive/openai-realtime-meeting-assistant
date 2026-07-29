export const messageLongPressDelayMs = 430;
export const messageReactionButtonSize = 44;
export const messageReactionTrayPadding = 5;
export const messageReactionTrayHeight = messageReactionButtonSize + (messageReactionTrayPadding * 2);
export const timestampRevealDistance = 68;
export const timestampRevealStartDistance = 8;
export const timestampRevealHorizontalBias = 1.35;

export const messageReactionChoices = ['❤️', '👍', '👎', '😂', '‼️', '❓', '🔥'] as const;

/**
 * A timestamp reveal must be an intentional leftward gesture. The horizontal
 * bias keeps normal vertical transcript scrolling from being captured.
 */
export function shouldBeginTimestampReveal(dx: number, dy: number): boolean {
  return dx < -timestampRevealStartDistance
    && Math.abs(dx) > Math.abs(dy) * timestampRevealHorizontalBias;
}

/** Maps a leftward drag to the shared 0…1 reveal progress. */
export function timestampRevealProgress(dx: number): number {
  if (!Number.isFinite(dx)) return 0;
  return Math.max(0, Math.min(1, -dx / timestampRevealDistance));
}
