export const compactComposerHeight = 40;
export const expandedComposerMaxHeight = 132;
export const editingComposerMaxHeight = 220;
const composerLineHeight = 22;
const averageComposerGlyphWidth = 7.2;

function estimatedWrappedHeight(value: string, width: number): number {
  if (!Number.isFinite(width) || width <= 0) return compactComposerHeight;
  const visualLines = value.split('\n').reduce((total, line) => {
    const estimatedWidth = Math.max(1, Array.from(line).length) * averageComposerGlyphWidth;
    return total + Math.max(1, Math.ceil(estimatedWidth / width));
  }, 0);
  return visualLines * composerLineHeight;
}

export function composerHeight(
  value: string,
  measuredHeight: number,
  maxHeight = expandedComposerMaxHeight,
  width = 0,
): number {
  if (!value) return compactComposerHeight;
  const nativeHeight = Number.isFinite(measuredHeight) ? measuredHeight : compactComposerHeight;
  const contentHeight = Math.max(nativeHeight, estimatedWrappedHeight(value, width));
  return Math.max(compactComposerHeight, Math.min(maxHeight, Math.ceil(contentHeight)));
}
