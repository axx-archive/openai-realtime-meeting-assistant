import React from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { useNavigation } from '@react-navigation/native';
import type { BottomTabNavigationProp } from '@react-navigation/bottom-tabs';
import type { CompositeNavigationProp } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { BrandMark } from '../components/BrandMark';
import { Card } from '../components/Card';
import { Screen } from '../components/Screen';
import { API_BASE_URL } from '../config';
import type { MainTabParamList, RootStackParamList } from '../navigation/types';
import { colors, product, radius, space, type } from '../theme/tokens';

type HomeNav = CompositeNavigationProp<
  BottomTabNavigationProp<MainTabParamList, 'Home'>,
  NativeStackNavigationProp<RootStackParamList>
>;

/**
 * Office home — live tool title "BonfireOS". Surfaces mirror the web tool set
 * (Rooms, Chat/Scout, Board) against the same backend.
 */
export function HomeScreen() {
  const { user, signOut } = useAuth();
  const navigation = useNavigation<HomeNav>();

  return (
    <Screen
      title={product.name}
      subtitle={user ? user.name : undefined}
      right={
        <Pressable onPress={() => void signOut()} hitSlop={12} style={styles.signOutHit}>
          <Text style={styles.signOut}>sign out</Text>
        </Pressable>
      }
    >
      <View style={styles.hero}>
        <BrandMark size={44} />
        <View style={styles.heroCopy}>
          <Text style={styles.heroTitle}>Office</Text>
          <Text style={styles.heroBody}>
            Same session, rooms, Scout, and board as the web OS at{' '}
            {API_BASE_URL.replace(/^https?:\/\//, '')}.
          </Text>
        </View>
        <Pressable
          style={({ pressed }) => [styles.primary, pressed && styles.pressed]}
          onPress={() => navigation.navigate('OSWeb', { path: '/', title: product.name })}
        >
          <Text style={styles.primaryText}>Open full OS</Text>
        </Pressable>
      </View>

      <Card
        title="Rooms"
        subtitle="Live spaces — same roster and presence as desktop."
        onPress={() => navigation.navigate('Rooms')}
      />
      <Card
        title="Chat"
        subtitle="Scout threads and queries against organizational memory."
        onPress={() => navigation.navigate('Chat')}
      />
      <Card
        title="Board"
        subtitle="Kanban shared with agents and the web board surface."
        onPress={() => navigation.navigate('Board')}
      />
    </Screen>
  );
}

const styles = StyleSheet.create({
  signOutHit: {
    minHeight: 44,
    justifyContent: 'center',
  },
  signOut: {
    ...type.label,
    color: colors.text3,
    textDecorationLine: 'underline',
    textTransform: 'none',
  },
  hero: {
    backgroundColor: colors.surface1,
    borderRadius: radius.xl,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    padding: space[5],
    marginBottom: space[4],
    gap: space[3],
  },
  heroCopy: {
    gap: 6,
  },
  heroTitle: {
    ...type.title2,
    color: colors.text1,
  },
  heroBody: {
    ...type.bodySm,
    color: colors.text2,
  },
  primary: {
    alignSelf: 'flex-start',
    backgroundColor: colors.accent,
    borderRadius: radius.md,
    paddingHorizontal: space[4],
    height: 44,
    alignItems: 'center',
    justifyContent: 'center',
    marginTop: space[1],
  },
  pressed: {
    transform: [{ scale: 0.97 }],
  },
  primaryText: {
    ...type.button,
    color: colors.onAccent,
  },
});
