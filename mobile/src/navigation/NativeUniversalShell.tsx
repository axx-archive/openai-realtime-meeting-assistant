import React from 'react';
import { Platform, Pressable, StyleSheet, Text, View, useWindowDimensions, type ColorValue } from 'react-native';
import Svg, { Path, Circle } from 'react-native-svg';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { Glass } from '../theme/glass';
import { colors, radius, space, type } from '../theme/tokens';
import { PersonalRealtimeFloatingControl } from '../realtime/PersonalRealtimeFloatingControl';
import { useOptionalPersonalRealtimeContext } from '../realtime/PersonalRealtimeContext';
import {
  personalRealtimeIslandPlacement,
  type PersonalRealtimeIslandSurface,
} from '../realtime/personalRealtimeIslandPlacement';
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
  personalRealtimeSurface?: PersonalRealtimeIslandSurface;
  personalRealtimeStartAllowed?: boolean;
  personalRealtimeVisible?: boolean;
  visible: boolean;
  /** Notification badges: which dests have unread activity */
  unreadBadges?: Partial<Record<NativeShellDestination, boolean>>;
  onOpenPersonalRealtimeThread?: (threadId: string) => void;
  onSelect: (destination: (typeof nativeShellDestinations)[number]) => void;
};

/**
 * Destination marks use one coherent 1.8pt stroke family in a 24pt view box.
 *   • home-mark: hearth (flame shape with inner circle)
 *   • meet-camera: video camera
 *   • chat-bubble: one speech bubble (rounded rect + lower-left tail)
 *   • work-pencil: authoring pencil
 *   • stacked-sheets: three stacked rectangles with depth
 */
function DestMark({ icon, size, color }: { icon: string; size: number; color: ColorValue }) {
  const strokeWidth = 1.8;
  switch (icon) {
    case 'home-mark':
      // Hearth: flame shape with inner circle (live web line 30958)
      return (
        <Svg style={styles.destMark} width={size} height={size} viewBox="0 0 24 24" fill="none">
          <Path
            d="M12 4c-5.5 3-8 6.5-8 10a8 8 0 0 0 16 0c0-3.5-2.5-7-8-10Z"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          <Circle
            cx="12"
            cy="14"
            r="2.5"
            stroke={color}
            strokeWidth={strokeWidth}
          />
        </Svg>
      );
    case 'meet-camera':
      return (
        <Svg style={styles.destMark} width={size} height={size} viewBox="0 0 24 24" fill="none">
          <Path
            d="M4 8a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8Z"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          <Path
            d="m16 10 4-2v8l-4-2"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </Svg>
      );
    case 'chat-bubble':
      // One speech bubble with lower-left tail (live web line 30966)
      return (
        <Svg style={styles.destMark} width={size} height={size} viewBox="0 0 24 24" fill="none">
          <Path
            d="M4 6c0-1.1.9-2 2-2h12c1.1 0 2 .9 2 2v8c0 1.1-.9 2-2 2h-5l-4 4v-4H6c-1.1 0-2-.9-2-2V6Z"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </Svg>
      );
    case 'work-pencil':
      return (
        <Svg style={styles.destMark} width={size} height={size} viewBox="0 0 24 24" fill="none">
          <Path
            d="m5 16.5-.8 3.3 3.3-.8L18.7 7.8a2.1 2.1 0 0 0-3-3L5 15.5v1Z"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          <Path
            d="m13.9 6.6 3.5 3.5M4.2 19.8h15.6"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </Svg>
      );
    case 'stacked-sheets':
      // Three stacked rectangles with depth (live web line 30970)
      return (
        <Svg style={styles.destMark} width={size} height={size} viewBox="0 0 24 24" fill="none">
          <Path
            d="M7 3h8c1.1 0 2 .9 2 2v1"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          <Path
            d="M5 6h10c1.1 0 2 .9 2 2v1"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          <Path
            d="M3 9h10c1.1 0 2 .9 2 2v8c0 1.1-.9 2-2 2H5c-1.1 0-2-.9-2-2v-8c0-1.1.9-2 2-2Z"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </Svg>
      );
    default:
      return (
        <Svg style={styles.destMark} width={size} height={size} viewBox="0 0 24 24" fill="none">
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
  personalRealtimeSurface = 'shell',
  personalRealtimeStartAllowed = false,
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
  const personalRealtimeRendered = Boolean(
    realtime
    && (
      (realtime.enabled && personalRealtimeVisible)
      || realtime.active
      || realtime.tearingDown
      || (realtime.enabled && realtime.status === 'error')
    ),
  );
  const personalRealtimePlacement = personalRealtimeIslandPlacement({
    bottomInset: insets.bottom,
    expanded: realtime?.status === 'error',
    rightInset: insets.right,
    layout,
    smallSpace: space[2],
    largeSpace: space[4],
    surface: personalRealtimeSurface,
    topInset: insets.top,
  });
  const personalRealtimePosition = personalRealtimePlacement.top !== undefined
    ? {
        top: personalRealtimePlacement.top,
        ...(personalRealtimePlacement.docked ? {} : { right: personalRealtimePlacement.right }),
      }
    : {
        bottom: personalRealtimePlacement.bottom,
        ...(personalRealtimePlacement.docked ? {} : { right: personalRealtimePlacement.right }),
      };
  const compactItemWidth = Math.max(40, Math.min(48, Math.floor((width - 38) / visibleDestinations.length)));
  return (
    <View style={styles.root}>
      {/* The navigator always occupies this exact child slot. Route, width,
          orientation, and chrome visibility may change styles or siblings but
          never replace/reparent the navigation subtree. */}
      <View
        testID="native-shell-content"
        style={[
          styles.content,
          sidebar && styles.contentSidebar,
          compact && styles.contentCompact,
          personalRealtimeRendered && personalRealtimePlacement.contentTopInset > 0
            ? { paddingTop: personalRealtimePlacement.contentTopInset }
            : null,
        ]}
      >
        {children}
      </View>
      {sidebar ? (
        <View
          accessibilityLabel="Primary"
          accessibilityRole="tablist"
          style={[styles.sidebar, { paddingTop: Math.max(insets.top, space[4]), paddingBottom: Math.max(insets.bottom, space[4]) }]}
        >
          {/* iPad wordmark at top of rail, above dest icons — matches web. */}
          <Text accessibilityRole="header" style={styles.sidebarWordmark}>stride</Text>
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
      {personalRealtimeRendered && realtime ? (
        <View
          pointerEvents="box-none"
          testID="personal-realtime-island"
          style={[
            styles.personalRealtime,
            personalRealtimePlacement.docked && styles.personalRealtimeDocked,
            personalRealtimePosition,
          ]}
        >
          <PersonalRealtimeFloatingControl
            realtime={realtime}
            onOpenThread={onOpenPersonalRealtimeThread}
            startAllowed={personalRealtimeStartAllowed}
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
          color={selected ? colors.ember : colors.text1}
        />
        {hasUnread ? <View style={styles.unreadDot} /> : null}
      </View>
    </Pressable>
  );
}

/**
 * Slim rail width for iPad sidebar — matches live web narrow rail.
 * No tagline or labels, just the five destination marks.
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
  sidebarWordmark: {
    ...type.wordmark,
    color: colors.wordmark,
    textAlign: 'center',
    textTransform: 'lowercase',
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
  personalRealtimeDocked: {
    left: 0,
    right: 0,
    alignItems: 'center',
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
    zIndex: 1,
  },
  compactItemSelected: { backgroundColor: colors.accentSoft },
  iconWrap: { position: 'relative', opacity: 1, zIndex: 2 },
  destMark: { opacity: 1, zIndex: 2 },
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
