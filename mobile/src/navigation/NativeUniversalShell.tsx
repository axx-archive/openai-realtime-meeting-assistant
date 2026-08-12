import React from 'react';
import { Platform, Pressable, StyleSheet, Text, View, useWindowDimensions } from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { Glass } from '../theme/glass';
import { colors, radius, space, type } from '../theme/tokens';
import { PersonalRealtimeFloatingControl } from '../realtime/PersonalRealtimeFloatingControl';
import { useOptionalPersonalRealtimeContext } from '../realtime/PersonalRealtimeContext';
import {
  nativeShellDestinations,
  nativeShellLayout,
  type NativeShellDestination,
} from './nativeShellModel';

type Props = {
  active: NativeShellDestination;
  children: React.ReactNode;
  keepSidebarForFocusedRoute?: boolean;
  personalRealtimeVisible?: boolean;
  visible: boolean;
  onOpenPersonalRealtimeThread?: (threadId: string) => void;
  onSelect: (destination: (typeof nativeShellDestinations)[number]) => void;
};

export function NativeUniversalShell({
  active,
  children,
  keepSidebarForFocusedRoute = false,
  personalRealtimeVisible = false,
  visible,
  onOpenPersonalRealtimeThread,
  onSelect,
}: Props) {
  const { width, fontScale } = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const realtime = useOptionalPersonalRealtimeContext();
  // An iPhone in landscape is wide, but it is not an iPad workspace. Keep the
  // focused conversation full-width instead of turning 874pt into a 248pt
  // desktop rail. Real iPads still adapt by width for Split View.
  const layout = nativeShellLayout(width, Platform.OS !== 'ios' || Platform.isPad, fontScale);
  const sidebar = (visible || keepSidebarForFocusedRoute) && layout === 'sidebar';
  const compact = visible && layout === 'compact';
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
          <Text accessibilityRole="header" maxFontSizeMultiplier={2} style={styles.sidebarBrand}>STRIDE</Text>
          <Text maxFontSizeMultiplier={2} style={styles.sidebarContext}>The network where work happens</Text>
          <View style={styles.sidebarItems}>
            {nativeShellDestinations.map((destination) => (
              <ShellItem key={destination.id} compact={false} destination={destination} selected={active === destination.id} onPress={() => onSelect(destination)} />
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
          {nativeShellDestinations.map((destination) => (
            <ShellItem key={destination.id} compact destination={destination} selected={active === destination.id} onPress={() => onSelect(destination)} />
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
  destination,
  selected,
  onPress,
}: {
  compact: boolean;
  destination: (typeof nativeShellDestinations)[number];
  selected: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      accessibilityLabel={destination.label}
      accessibilityRole="tab"
      accessibilityState={{ selected }}
      hitSlop={4}
      onPress={onPress}
      style={({ pressed }) => [
        compact ? styles.compactItem : styles.sidebarItem,
        selected && (compact ? styles.compactItemSelected : styles.sidebarItemSelected),
        pressed && styles.pressed,
      ]}
    >
      <SymbolView
        name={destination.icon as SFSymbol}
        size={compact ? 19 : 18}
        tintColor={selected ? colors.ember : colors.text2}
      />
      {!compact ? (
        <Text
          maxFontSizeMultiplier={2}
          numberOfLines={1}
          style={[styles.sidebarLabel, selected && styles.selectedLabel]}
        >
          {destination.label}
        </Text>
      ) : null}
    </Pressable>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: colors.bgApp },
  content: { flex: 1, minWidth: 0, minHeight: 0 },
  contentSidebar: { marginLeft: 248 },
  contentCompact: { marginBottom: 84 },
  sidebar: {
    position: 'absolute',
    top: 0,
    bottom: 0,
    left: 0,
    width: 248,
    paddingHorizontal: space[4],
    borderRightWidth: StyleSheet.hairlineWidth,
    borderRightColor: colors.border,
    backgroundColor: colors.surface1,
  },
  sidebarBrand: { ...type.title2, color: colors.text1, letterSpacing: -0.4 },
  sidebarContext: { ...type.caption, color: colors.text2, marginTop: space[1], maxWidth: 190 },
  sidebarItems: { gap: space[1], marginTop: space[6] },
  sidebarItem: {
    minHeight: 48,
    paddingHorizontal: space[3],
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    borderRadius: radius.md,
  },
  sidebarItemSelected: { backgroundColor: colors.accentSoft },
  sidebarLabel: { ...type.bodyMedium, color: colors.text2, flexShrink: 1 },
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
  selectedLabel: { color: colors.emberText },
  pressed: { opacity: 0.82, transform: [{ scale: 0.96 }] },
});
