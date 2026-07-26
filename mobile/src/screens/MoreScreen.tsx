import React from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { Screen } from '../components/Screen';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, shadow, space, type } from '../theme/tokens';

const destinations: Array<{
  route: keyof RootStackParamList;
  title: string;
  subtitle: string;
  icon: SFSymbol;
}> = [
  { route: 'Intelligence', title: 'Intelligence', subtitle: 'Themes and signals', icon: 'sparkles' },
  { route: 'Memory', title: 'Memory', subtitle: 'Shared company context', icon: 'brain.head.profile' },
  { route: 'Meetings', title: 'Meetings', subtitle: 'Recaps and decisions', icon: 'person.2.wave.2' },
  { route: 'Files', title: 'Files', subtitle: 'Artifacts and uploads', icon: 'folder.fill' },
  { route: 'Alerts', title: 'Activity', subtitle: 'What needs attention', icon: 'bell.fill' },
  { route: 'Settings', title: 'Settings', subtitle: 'Profile, passkeys, appearance', icon: 'gearshape.fill' },
];

export function MoreScreen() {
  const navigation = useNavigation<NativeStackNavigationProp<RootStackParamList>>();
  return (
    <Screen title="More" subtitle="Everything in your Bonfire office">
      <View style={styles.grid}>
        {destinations.map((item) => (
          <Pressable
            key={item.route}
            accessibilityRole="button"
            accessibilityLabel={item.title}
            accessibilityHint={`Opens ${item.subtitle.toLowerCase()}.`}
            onPress={() => navigation.navigate(item.route as never)}
            style={({ pressed }) => [styles.tile, shadow[1], pressed && styles.pressed]}
          >
            <View style={styles.iconWrap}>
              <SymbolView name={item.icon} tintColor={colors.text1} size={22} />
            </View>
            <Text style={styles.title}>{item.title}</Text>
            <Text style={styles.subtitle}>{item.subtitle}</Text>
          </Pressable>
        ))}
      </View>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel="Open web workspace"
        accessibilityHint="Opens the authenticated advanced workspace inside BonfireOS."
        onPress={() => navigation.navigate('OSWeb', { path: '/', title: 'Web workspace' })}
        style={({ pressed }) => [styles.webRow, pressed && styles.webPressed]}
      >
        <SymbolView name="safari.fill" tintColor={colors.text2} size={18} />
        <View style={styles.webCopy}>
          <Text style={styles.webTitle}>Web workspace</Text>
          <Text style={styles.webSubtitle}>Open an advanced surface when you need the desktop canvas.</Text>
        </View>
        <SymbolView name="chevron.right" tintColor={colors.text3} size={13} />
      </Pressable>
    </Screen>
  );
}

const styles = StyleSheet.create({
  grid: { flexDirection: 'row', flexWrap: 'wrap', gap: space[3] },
  tile: {
    width: '48%',
    minHeight: 150,
    borderRadius: radius.xl,
    backgroundColor: colors.surface1,
    padding: space[4],
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
  },
  pressed: { transform: [{ scale: 0.96 }], opacity: 0.9 },
  iconWrap: {
    width: 42,
    height: 42,
    borderRadius: 13,
    backgroundColor: colors.surface3,
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: space[4],
  },
  title: { ...type.headline, color: colors.text1 },
  subtitle: { ...type.caption, color: colors.text2, marginTop: 4 },
  webRow: {
    marginTop: space[5],
    minHeight: 72,
    borderRadius: radius.lg,
    paddingHorizontal: space[4],
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    backgroundColor: colors.surface3,
  },
  webPressed: { opacity: 0.7 },
  webCopy: { flex: 1 },
  webTitle: { ...type.bodyMedium, color: colors.text1 },
  webSubtitle: { ...type.caption, color: colors.text2, marginTop: 2 },
});
