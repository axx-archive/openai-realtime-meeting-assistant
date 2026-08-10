import React from 'react';
import { Pressable, StyleSheet, Text, View, useWindowDimensions } from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { colors, radius, space, type } from '../theme/tokens';
import {
  nativeShellDestinations,
  nativeShellLayout,
  type NativeShellDestination,
} from './nativeShellModel';

type Props = {
  active: NativeShellDestination;
  children: React.ReactNode;
  visible: boolean;
  onSelect: (destination: (typeof nativeShellDestinations)[number]) => void;
};

export function NativeUniversalShell({ active, children, visible, onSelect }: Props) {
  const { width } = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const layout = nativeShellLayout(width);
  const sidebar = visible && layout === 'sidebar';
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
          <Text accessibilityRole="header" style={styles.sidebarBrand}>STRIDE</Text>
          <Text style={styles.sidebarContext}>The network where work happens</Text>
          <View style={styles.sidebarItems}>
            {nativeShellDestinations.map((destination) => (
              <ShellItem key={destination.id} compact={false} destination={destination} selected={active === destination.id} onPress={() => onSelect(destination)} />
            ))}
          </View>
        </View>
      ) : null}
      {compact ? (
        <View
          accessibilityLabel="Primary"
          accessibilityRole="tablist"
          style={[styles.bottomRail, { paddingBottom: Math.max(insets.bottom, space[2]) }]}
        >
          {nativeShellDestinations.map((destination) => (
            <ShellItem key={destination.id} compact destination={destination} selected={active === destination.id} onPress={() => onSelect(destination)} />
          ))}
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
        size={compact ? 20 : 18}
        tintColor={selected ? colors.ember : colors.text2}
      />
      <Text
        maxFontSizeMultiplier={2}
        numberOfLines={compact ? 2 : 1}
        style={[
          compact ? styles.compactLabel : styles.sidebarLabel,
          selected && styles.selectedLabel,
        ]}
      >
        {destination.label}
      </Text>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: colors.bgApp },
  content: { flex: 1, minWidth: 0, minHeight: 0 },
  contentSidebar: { marginLeft: 248 },
  contentCompact: { marginBottom: 64 },
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
    right: 0,
    bottom: 0,
    left: 0,
    minHeight: 64,
    paddingTop: space[1],
    paddingHorizontal: space[1],
    flexDirection: 'row',
    alignItems: 'flex-start',
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: colors.border,
    backgroundColor: colors.surface1,
  },
  compactItem: {
    flex: 1,
    minWidth: 0,
    minHeight: 52,
    paddingHorizontal: 2,
    paddingVertical: space[1],
    alignItems: 'center',
    justifyContent: 'center',
    gap: 2,
    borderRadius: radius.sm,
  },
  compactItemSelected: { backgroundColor: colors.accentSoft },
  compactLabel: { ...type.label, color: colors.text2, textAlign: 'center', fontSize: 10, lineHeight: 12 },
  selectedLabel: { color: colors.emberText },
  pressed: { opacity: 0.82, transform: [{ scale: 0.96 }] },
});
