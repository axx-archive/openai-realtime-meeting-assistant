export type BonfireStatusLanePlacement = {
  compact: boolean;
  height: number;
  left: number;
  top: number;
  width: number;
};

export type ScreenRectangle = {
  height: number;
  left: number;
  top: number;
  width: number;
};

const STATUS_LANE_TOP = 52;
const STATUS_LANE_HEIGHT = 56;
const STATUS_LANE_LEFT = 20;
const STATUS_LANE_REGULAR_WIDTH = 280;
const STATUS_LANE_LANDSCAPE_PHONE_WIDTH = 96;

/**
 * Bonfire lookup errors own a fixed top lane, never the space above the
 * bottom-left shortcut. On short landscape phones the lane becomes a compact
 * left notice so even the centered 300pt Realtime error island stays disjoint.
 */
export function bonfireStatusLanePlacement(
  screenWidth: number,
  screenHeight: number,
): BonfireStatusLanePlacement {
  const width = Math.max(0, Number.isFinite(screenWidth) ? screenWidth : 0);
  const height = Math.max(0, Number.isFinite(screenHeight) ? screenHeight : 0);
  const compact = width > height && height < 450;
  return {
    compact,
    height: STATUS_LANE_HEIGHT,
    left: STATUS_LANE_LEFT,
    top: STATUS_LANE_TOP,
    width: Math.max(0, Math.min(
      compact ? STATUS_LANE_LANDSCAPE_PHONE_WIDTH : STATUS_LANE_REGULAR_WIDTH,
      width - (STATUS_LANE_LEFT * 2),
    )),
  };
}

export function screenRectanglesOverlap(left: ScreenRectangle, right: ScreenRectangle): boolean {
  return left.left < right.left + right.width
    && left.left + left.width > right.left
    && left.top < right.top + right.height
    && left.top + left.height > right.top;
}
