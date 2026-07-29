export const compactComposerHeight = 40;
export const expandedComposerMaxHeight = 132;

export function composerHeight(value: string, measuredHeight: number): number {
  if (!value) return compactComposerHeight;
  if (!Number.isFinite(measuredHeight)) return compactComposerHeight;
  return Math.max(compactComposerHeight, Math.min(expandedComposerMaxHeight, Math.ceil(measuredHeight)));
}
