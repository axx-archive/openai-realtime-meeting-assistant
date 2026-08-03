export const compactComposerHeight = 40;
export const expandedComposerMaxHeight = 132;
export const editingComposerMaxHeight = 220;

export function composerHeight(
  value: string,
  measuredHeight: number,
  maxHeight = expandedComposerMaxHeight,
): number {
  if (!value) return compactComposerHeight;
  if (!Number.isFinite(measuredHeight)) return compactComposerHeight;
  return Math.max(compactComposerHeight, Math.min(maxHeight, Math.ceil(measuredHeight)));
}
