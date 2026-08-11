export const STRIDE_SIDEBAR_WIDTH = 248;
export const THREAD_CONVERSATION_PANE_WIDTH = 300;
export const THREAD_CONVERSATION_PANE_MIN_WIDTH = 1024;
// At accessibility text sizes, even a 13-inch iPad in landscape needs the
// complete width for the selected conversation. The rail returns only on a
// genuinely expansive external-display canvas.
export const THREAD_CONVERSATION_PANE_LARGE_TEXT_MIN_WIDTH = 1600;
export const THREAD_LARGE_TEXT_FONT_SCALE = 1.35;

export type ThreadWorkspaceLayout = {
  conversationPane: boolean;
  detailWidth: number;
  stackedActivity: boolean;
};

export function threadWorkspaceLayout(
  width: number,
  fontScale: number,
  sidebarVisible: boolean,
): ThreadWorkspaceLayout {
  const safeWidth = Number.isFinite(width) ? Math.max(0, width) : 0;
  const safeFontScale = Number.isFinite(fontScale) && fontScale > 0 ? fontScale : 1;
  const largeText = safeFontScale >= THREAD_LARGE_TEXT_FONT_SCALE;
  const conversationPane = safeWidth >= (
    largeText
      ? THREAD_CONVERSATION_PANE_LARGE_TEXT_MIN_WIDTH
      : THREAD_CONVERSATION_PANE_MIN_WIDTH
  );
  const detailWidth = Math.max(
    0,
    safeWidth
      - (sidebarVisible ? STRIDE_SIDEBAR_WIDTH : 0)
      - (conversationPane ? THREAD_CONVERSATION_PANE_WIDTH : 0),
  );
  return {
    conversationPane,
    detailWidth,
    stackedActivity: largeText || detailWidth < 390,
  };
}
