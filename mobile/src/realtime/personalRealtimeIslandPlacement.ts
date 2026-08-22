import type { NativeShellLayout } from '../navigation/nativeShellModel';
import type { RootStackParamList } from '../navigation/types';

const COMPACT_NAV_CLEARANCE = 70;
// The compact active pill is 48pt high. A 64pt lane leaves 16pt between it and
// the focused screen header. The error card is bounded to three lines at 1.6x
// (92pt) plus its 60pt action/padding stack, so 168pt remains collision-free.
export const FOCUSED_ACTIVE_LANE_HEIGHT = 64;
export const FOCUSED_ERROR_LANE_HEIGHT = 168;

export type PersonalRealtimeIslandSurface =
  | 'shell'
  | 'conversation'
  | 'room'
  | 'focused-workspace';

export type PersonalRealtimeIslandPlacement = {
  bottom?: number;
  contentTopInset: number;
  docked: boolean;
  right?: number;
  top?: number;
};

/**
 * Focused routes do not share one generic bottom-control height. Thread has a
 * 40–132pt input plus attachments/suggestions and Room has a 60pt call dock,
 * so guessing a bottom offset can cover an actionable control. Classifying the
 * route lets Shell reserve a real top lane above the entire focused navigator;
 * headers and every variable-height bottom control remain inside content.
 */
export function personalRealtimeIslandSurface(
  route: keyof RootStackParamList | undefined,
): PersonalRealtimeIslandSurface {
  if (route === 'Thread' || route === 'ChannelRiff') return 'conversation';
  if (route === 'Room') return 'room';
  if (
    route === 'Login'
    || route === 'CreateRoom'
    || route === 'NewConversation'
    || route === 'OSWeb'
    || route === 'DeckViewer'
  ) return 'focused-workspace';
  return 'shell';
}

export function personalRealtimeIslandPlacement(input: {
  bottomInset: number;
  expanded: boolean;
  rightInset: number;
  layout: NativeShellLayout;
  smallSpace: number;
  largeSpace: number;
  surface: PersonalRealtimeIslandSurface;
  topInset: number;
}): PersonalRealtimeIslandPlacement {
  const compact = input.layout === 'compact';
  if (input.surface !== 'shell') {
    return {
      contentTopInset: input.expanded
        ? FOCUSED_ERROR_LANE_HEIGHT
        : FOCUSED_ACTIVE_LANE_HEIGHT,
      docked: compact,
      right: compact ? undefined : Math.max(input.rightInset, input.largeSpace),
      top: Math.max(input.topInset, input.smallSpace),
    };
  }
  if (compact) {
    return {
      bottom: Math.max(input.bottomInset, input.smallSpace) + COMPACT_NAV_CLEARANCE,
      contentTopInset: 0,
      docked: true,
    };
  }
  return {
    bottom: Math.max(input.bottomInset, input.largeSpace),
    contentTopInset: 0,
    docked: false,
    right: Math.max(input.rightInset, input.largeSpace),
  };
}
