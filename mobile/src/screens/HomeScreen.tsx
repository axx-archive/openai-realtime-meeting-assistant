import React, { useCallback, useEffect, useState } from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { useFocusEffect, useNavigation } from '@react-navigation/native';
import { SymbolView } from 'expo-symbols';
import type { BottomTabNavigationProp } from '@react-navigation/bottom-tabs';
import type { CompositeNavigationProp } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { BrandMark } from '../components/BrandMark';
import { Card } from '../components/Card';
import { Screen } from '../components/Screen';
import { API_BASE_URL } from '../config';
import { api } from '../api/client';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { firstArray } from '../utils/records';
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
  const { user, sessionToken } = useAuth();
  const office = useOfficeEvents();
  const navigation = useNavigation<HomeNav>();
  const [roomCount, setRoomCount] = useState(0);
  const [liveCount, setLiveCount] = useState(0);
  const [alertCount, setAlertCount] = useState(0);

  useFocusEffect(
    useCallback(() => {
      if (!sessionToken) return;
      let cancelled = false;
      void Promise.all([api.rooms(sessionToken), api.notifications(sessionToken)]).then(([rooms, alerts]) => {
        if (cancelled) return;
        setRoomCount((rooms.rooms ?? []).filter((room) => !room.archived).length);
        setLiveCount((rooms.rooms ?? []).filter((room) => room.live).length);
        const rows = firstArray(alerts, ['notifications']);
        setAlertCount(rows.filter((item) => !(item as { read?: boolean })?.read).length);
      }).catch(() => {});
      return () => { cancelled = true; };
    }, [sessionToken]),
  );

  useEffect(() => {
    if (!sessionToken || !['rooms', 'participants', 'notification', 'notification_backlog'].includes(office.event ?? '')) return;
    void Promise.all([api.rooms(sessionToken), api.notifications(sessionToken)]).then(([rooms, alerts]) => {
      setRoomCount((rooms.rooms ?? []).filter((room) => !room.archived).length);
      setLiveCount((rooms.rooms ?? []).filter((room) => room.live).length);
      setAlertCount(firstArray(alerts, ['notifications']).filter((item) => !(item as { read?: boolean })?.read).length);
    }).catch(() => {});
  }, [office.event, office.version, sessionToken]);

  return (
    <Screen
      title={product.name}
      subtitle={user ? user.name : undefined}
      right={<BrandMark size={40} />}
    >
      <View style={styles.hero}>
        <View style={styles.heroCopy}>
          <Text style={styles.eyebrow}>YOUR OFFICE</Text>
          <Text style={styles.heroTitle}>Good {new Date().getHours() < 12 ? 'morning' : new Date().getHours() < 18 ? 'afternoon' : 'evening'}, {user?.name?.split(' ')[0]}.</Text>
          <Text style={styles.heroBody}>
            Everything here is live with {API_BASE_URL.replace(/^https?:\/\//, '')}.
          </Text>
        </View>
        <View style={styles.metrics}>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`${liveCount} live ${liveCount === 1 ? 'room' : 'rooms'}`}
            accessibilityHint="Opens rooms."
            style={({ pressed }) => [styles.metric, pressed && styles.metricPressed]}
            onPress={() => navigation.navigate('Rooms')}
          >
            <Text style={styles.metricValue}>{liveCount}</Text><Text style={styles.metricLabel}>live</Text>
          </Pressable>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`${roomCount} active ${roomCount === 1 ? 'room' : 'rooms'}`}
            accessibilityHint="Opens rooms."
            style={({ pressed }) => [styles.metric, pressed && styles.metricPressed]}
            onPress={() => navigation.navigate('Rooms')}
          >
            <Text style={styles.metricValue}>{roomCount}</Text><Text style={styles.metricLabel}>rooms</Text>
          </Pressable>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`${alertCount} unread ${alertCount === 1 ? 'alert' : 'alerts'}`}
            accessibilityHint="Opens alerts."
            style={({ pressed }) => [styles.metric, pressed && styles.metricPressed]}
            onPress={() => navigation.navigate('Alerts')}
          >
            <Text style={styles.metricValue}>{alertCount}</Text><Text style={styles.metricLabel}>unread</Text>
          </Pressable>
        </View>
      </View>

      <Card
        title="Ask Scout"
        subtitle="Turn company memory into a clear next move."
        badge="AI"
        onPress={() => navigation.navigate('Chat')}
      />
      <Card
        title="Intelligence"
        subtitle="Themes, signals, and opportunities distilled from the work."
        onPress={() => navigation.navigate('Intelligence')}
      />
      <Card
        title="Meeting memory"
        subtitle="Catch up on decisions without replaying the entire room."
        onPress={() => navigation.navigate('Meetings')}
      />
    </Screen>
  );
}

const styles = StyleSheet.create({
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
  eyebrow: {
    ...type.label,
    color: colors.ember,
    letterSpacing: 1.1,
  },
  heroTitle: {
    ...type.title2,
    color: colors.text1,
  },
  heroBody: {
    ...type.bodySm,
    color: colors.text2,
  },
  metrics: { flexDirection: 'row', gap: space[2], marginTop: space[2] },
  metric: { flex: 1, minHeight: 66, borderRadius: radius.md, backgroundColor: colors.surface3, alignItems: 'center', justifyContent: 'center' },
  metricPressed: { opacity: 0.92, transform: [{ scale: 0.96 }] },
  metricValue: { ...type.title2, color: colors.text1, fontVariant: ['tabular-nums'] },
  metricLabel: { ...type.label, color: colors.text3, marginTop: 2, textTransform: 'uppercase' },
});
