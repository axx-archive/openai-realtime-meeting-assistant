import React from 'react';
import { Platform, Pressable, StyleSheet, View, useWindowDimensions } from 'react-native';
import Svg, { Path, Rect, Circle } from 'react-native-svg';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { Glass } from '../theme/glass';
import { colors, radius, space } from '../theme/tokens';
import { PersonalRealtimeFloatingControl } from '../realtime/PersonalRealtimeFloatingControl';
import { useOptionalPersonalRealtimeContext } from '../realtime/PersonalRealtimeContext';
import {
  nativeShellDestinations,
  nativeShellDestinationsForAccess,
  type NativeShellAccess,
  nativeShellLayout,
  type NativeShellDestination,
} from './nativeShellModel';

type Props = {
  active: NativeShellDestination;
  access?: NativeShellAccess;
  children: React.ReactNode;
  keepSidebarForFocusedRoute?: boolean;
  personalRealtimeVisible?: boolean;
  visible: boolean;
  /** Notification badges: which dests have unread activity */
  unreadBadges?: Partial<Record<NativeShellDestination, boolean>>;
  onOpenPersonalRealtimeThread?: (threadId: string) => void;
  onSelect: (destination: (typeof nativeShellDestinations)[number]) => void;
};

/**
 * Custom destination marks matching the live web stroke family (45e2f7c1).
 *   • home-mark: house glyph
 *   • meet-mark: video camera
 *   • chat-bubble: one speech bubble (rounded rect + lower-left tail)
 *   • stacked-sheets: two overlapping rectangles
 * NOT SF bubble.left.and.bubble.right.fill, NOT folder.fill, NOT the old lens/wifi.
 */
function DestMark({ icon, size, color }: { icon: string; size: number; color: string }) {
  const strokeWidth = 1.5;
  switch (icon) {
    case 'home-mark':
      return (
        <Svg width={size} height={size} viewBox="0 0 24 24" fill="none">
          <Path
            d="M3 10.5L12 3L21 10.5V20C21 20.5523 20.5523 21 20 21H15V15C15 14.4477 14.5523 14 14 14H10C9.44772 14 9 14.4477 9 15V21H4C3.44772 21 3 20.5523 3 20V10.5Z"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </Svg>
      );
    case 'meet-mark':
      return (
        <Svg width={size} height={size} viewBox="0 0 24 24" fill="none">
          <Rect
            x="2"
            y="6"
            width="13"
            height="12"
            rx="2"
            stroke={color}
            strokeWidth={strokeWidth}
          />
          <Path
            d="M15 10L21 7V17L15 14"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </Svg>
      );
    case 'chat-bubble':
      return (
        <Svg width={size} height={size} viewBox="0 0 24 24" fill="none">
          <Path
            d="M4 5C4 4.44772 4.44772 4 5 4H19C19.5523 4 20 4.44772 20 5V15C20 15.5523 19.5523 16 19 16H8L4 20V5Z"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </Svg>
      );
    case 'stacked-sheets':
      return (
        <Svg width={size} height={size} viewBox="0 0 24 24" fill="none">
          <Rect
            x="6"
            y="2"
            width="14"
            height="16"
            rx="2"
            stroke={color}
            strokeWidth={strokeWidth}
          />
          <Path
            d="M4 6V20C4 21.1046 4.89543 22 6 22H16"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
          />
        </Svg>
      );
    default:
      return (
        <Svg width={size} height={size} viewBox="0 0 24 24" fill="none">
          <Circle cx="12" cy="12" r="8" stroke={color} strokeWidth={strokeWidth} />
        </Svg>
      );
  }
}

export function NativeUniversalShell({
  active,
  access,
  children,
  keepSidebarForFocusedRoute = false,
  personalRealtimeVisible = false,
  visible,
  unreadBadges = {},
  onOpenPersonalRealtimeThread,
  onSelect,
}: Props) {
  const { width, fontScale } = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const realtime = useOptionalPersonalRealtimeContext();
  const visibleDestinations = nativeShellDestinationsForAccess(access);
  // An iPhone in landscape is wide, but it is not an iPad workspace. Keep the
  // focused conversation full-width instead of turning 874pt into a 248pt
  // desktop rail. Real iPads still adapt by width for Split View.
  const layout = nativeShellLayout(width, Platform.OS !== 'ios' || Platform.isPad, fontScale);
  const sidebar = (visible || keepSidebarForFocusedRoute) && layout === 'sidebar';
  const compact = visible && layout === 'compact';
  const compactItemWidth = Math.max(40, Math.min(48, Math.floor((width - 38) / visibleDestinations.length)));
  return (
    <View style={styles.root}>
      {/* The navigator always occupies this exact child slot. Route, width,
          orientation, and chrome visibility may change styles or siblings but
          never replace/reparent the navigation subtree. */}
      <View style={[styles.content, sidebar && styles.contentSidebar, compact && styles.contentCompact]}>
        {children}
      </View>
      {sidebar ? (
        <View
          accessibilityLabel="Primary"
          accessibilityRole="tablist"
          style={[styles.sidebar, { paddingTop: Math.max(insets.top, space[4]), paddingBottom: Math.max(insets.bottom, space[4]) }]}
        >
          {/* No wordmark or tagline — mark only. Slim rail like web. */}
          <View style={styles.sidebarItems}>
            {visibleDestinations.map((destination) => (
              <ShellItem
                key={destination.id}
                compact={false}
                destination={destination}
                selected={active === destination.id}
                hasUnread={Boolean(unreadBadges[destination.id])}
                onPress={() => onSelect(destination)}
              />
            ))}
          </View>
        </View>
      ) : null}
      {compact ? (
        <Glass
          accessibilityLabel="Primary"
          accessibilityRole="tablist"
          interactive
          radius={radius.full}
          style={[styles.bottomRail, { bottom: Math.max(insets.bottom, space[2]) }]}
        >
          {visibleDestinations.map((destination) => (
            <ShellItem
              key={destination.id}
              compact
              compactWidth={compactItemWidth}
              destination={destination}
              selected={active === destination.id}
              hasUnread={Boolean(unreadBadges[destination.id])}
              onPress={() => onSelect(destination)}
            />
          ))}
        </Glass>
      ) : null}
      {(personalRealtimeVisible || realtime?.active) && realtime ? (
        <View
          pointerEvents="box-none"
          style={[
            styles.personalRealtime,
            {
              top: Math.max(insets.top, space[2]),
              right: Math.max(insets.right, space[3]),
            },
          ]}
        >
          <PersonalRealtimeFloatingControl
            realtime={realtime}
            onOpenThread={onOpenPersonalRealtimeThread}
          />
        </View>
      ) : null}
    </View>
  );
}

function ShellItem({
  compact,
  compactWidth,
  destination,
  selected,
  hasUnread = false,
  onPress,
}: {
  compact: boolean;
  compactWidth?: number;
  destination: (typeof nativeShellDestinations)[number];
  selected: boolean;
  hasUnread?: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      accessibilityLabel={`${destination.label}${hasUnread ? ', unread notifications' : ''}`}
      accessibilityRole="tab"
      accessibilityState={{ selected }}
      hitSlop={4}
      onPress={onPress}
      style={({ pressed }) => [
        compact ? styles.compactItem : styles.sidebarItem,
        compact && { width: compactWidth },
        selected && (compact ? styles.compactItemSelected : styles.sidebarItemSelected),
        pressed && styles.pressed,
      ]}
    >
      <View style={styles.iconWrap}>
        <DestMark
          icon={destination.icon}
          size={compact ? 20 : 20}
          color={String(selected ? colors.ember : colors.text2)}
        />
        {hasUnread ? <View style={styles.unreadDot} /> : null}
      </View>
    </Pressable>
  );
}

/**
 * Slim rail width for iPad sidebar — matches live web narrow rail.
 * No wordmark, no tagline, just the four dest marks.
 */
const SIDEBAR_WIDTH = 68;

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: colors.bgApp },
  content: { flex: 1, minWidth: 0, minHeight: 0 },
  contentSidebar: { marginLeft: SIDEBAR_WIDTH },
  contentCompact: { marginBottom: 84 },
  sidebar: {
    position: 'absolute',
    top: 0,
    bottom: 0,
    left: 0,
    width: SIDEBAR_WIDTH,
    paddingHorizontal: space[2],
    borderRightWidth: StyleSheet.hairlineWidth,
    borderRightColor: colors.border,
    backgroundColor: colors.surface1,
    alignItems: 'center',
  },
  sidebarItems: { gap: space[2], marginTop: space[4], alignItems: 'center' },
  sidebarItem: {
    width: 48,
    height: 48,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.md,
  },
  sidebarItemSelected: { backgroundColor: colors.accentSoft },
  bottomRail: {
    position: 'absolute',
    alignSelf: 'center',
    minHeight: 58,
    padding: 5,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 2,
  },
  personalRealtime: {
    position: 'absolute',
    zIndex: 10,
  },
  compactItem: {
    width: 48,
    minHeight: 48,
    paddingHorizontal: space[2],
    alignItems: 'center',
    justifyContent: 'center',
    flexDirection: 'row',
    gap: 6,
    borderRadius: radius.full,
  },
  compactItemSelected: { backgroundColor: colors.accentSoft },
  iconWrap: { position: 'relative' },
  unreadDot: {
    position: 'absolute',
    top: -2,
    right: -2,
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: colors.ember,
  },
  pressed: { opacity: 0.82, transform: [{ scale: 0.96 }] },
});
