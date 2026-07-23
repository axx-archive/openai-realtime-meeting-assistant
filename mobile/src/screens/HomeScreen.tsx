import React from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { useNavigation } from '@react-navigation/native';
import type { BottomTabNavigationProp } from '@react-navigation/bottom-tabs';
import type { CompositeNavigationProp } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { Card } from '../components/Card';
import { Screen } from '../components/Screen';
import { API_BASE_URL } from '../config';
import type { MainTabParamList, RootStackParamList } from '../navigation/types';
import { colors } from '../theme/colors';

type HomeNav = CompositeNavigationProp<
  BottomTabNavigationProp<MainTabParamList, 'Home'>,
  NativeStackNavigationProp<RootStackParamList>
>;

export function HomeScreen() {
  const { user, signOut } = useAuth();
  const navigation = useNavigation<HomeNav>();

  return (
    <Screen
      title="Office"
      subtitle={user ? `Signed in as ${user.name}` : undefined}
      right={
        <Pressable onPress={() => void signOut()} hitSlop={12}>
          <Text style={styles.signOut}>Sign out</Text>
        </Pressable>
      }
    >
      <View style={styles.hero}>
        <Text style={styles.heroTitle}>Same OS. Mobile shell.</Text>
        <Text style={styles.heroBody}>
          This app talks to the live BonfireOS backend at {API_BASE_URL.replace(/^https?:\/\//, '')}.
          Rooms, Scout, and board are the same data the web app reads and writes.
        </Text>
        <Pressable
          style={({ pressed }) => [styles.primary, pressed && styles.pressed]}
          onPress={() => navigation.navigate('OSWeb', { path: '/', title: 'BonfireOS' })}
        >
          <Text style={styles.primaryText}>Open full OS</Text>
        </Pressable>
      </View>

      <Card
        title="Rooms"
        subtitle="Live WebRTC spaces with the same roster and presence as desktop."
        onPress={() => navigation.navigate('Rooms')}
      />
      <Card
        title="Scout"
        subtitle="Private threads and queries against organizational memory."
        onPress={() => navigation.navigate('Scout')}
      />
      <Card
        title="Board"
        subtitle="Kanban cards shared with agents and the web board surface."
        onPress={() => navigation.navigate('Board')}
      />
    </Screen>
  );
}

const styles = StyleSheet.create({
  signOut: {
    color: colors.ember,
    fontWeight: '600',
    fontSize: 15,
    marginTop: 8,
  },
  hero: {
    backgroundColor: colors.accent,
    borderRadius: 20,
    padding: 20,
    marginBottom: 16,
  },
  heroTitle: {
    color: colors.onAccent,
    fontSize: 22,
    fontWeight: '700',
    letterSpacing: -0.4,
  },
  heroBody: {
    color: 'rgba(255,255,255,0.72)',
    fontSize: 14,
    lineHeight: 20,
    marginTop: 8,
  },
  primary: {
    marginTop: 16,
    alignSelf: 'flex-start',
    backgroundColor: colors.ember,
    borderRadius: 12,
    paddingHorizontal: 16,
    paddingVertical: 12,
  },
  pressed: {
    opacity: 0.9,
  },
  primaryText: {
    color: colors.onEmber,
    fontWeight: '700',
    fontSize: 15,
  },
});
