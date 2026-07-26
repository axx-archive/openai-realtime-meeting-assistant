import React, { useCallback, useEffect, useState } from 'react';
import { Pressable, Text, StyleSheet, View } from 'react-native';
import { SymbolView } from 'expo-symbols';
import {
  useFocusEffect,
  useNavigation,
  type CompositeNavigationProp,
} from '@react-navigation/native';
import type { BottomTabNavigationProp } from '@react-navigation/bottom-tabs';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type { Room } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { Card } from '../components/Card';
import { Screen } from '../components/Screen';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import type { MainTabParamList, RootStackParamList } from '../navigation/types';
import { colors, type } from '../theme/tokens';

type RoomsNav = CompositeNavigationProp<
  BottomTabNavigationProp<MainTabParamList, 'Rooms'>,
  NativeStackNavigationProp<RootStackParamList>
>;

export function RoomsScreen() {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const navigation = useNavigation<RoomsNav>();
  const [rooms, setRooms] = useState<Room[]>([]);
  const [showArchived, setShowArchived] = useState(false);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    async (mode: 'initial' | 'refresh' = 'initial') => {
      if (!sessionToken) return;
      if (mode === 'refresh') setRefreshing(true);
      else setLoading(true);
      setError(null);
      try {
        const res = await api.rooms(sessionToken);
        setRooms(res.rooms ?? []);
      } catch (err) {
        setError(err instanceof BonfireApiError ? err.message : 'Could not load rooms');
      } finally {
        setLoading(false);
        setRefreshing(false);
      }
    },
    [sessionToken],
  );

  useFocusEffect(
    useCallback(() => {
      void load('initial');
    }, [load]),
  );

  useEffect(() => {
    if (office.event === 'rooms' || office.event === 'participants') void load('refresh');
  }, [load, office.event, office.version]);

  return (
    <Screen
      title="Rooms"
      subtitle="Live spaces shared with the web OS"
      loading={loading}
      error={error}
      onRetry={() => void load('initial')}
      refreshing={refreshing}
      onRefresh={() => void load('refresh')}
      right={
        <View style={styles.actions}>
          <Pressable accessibilityRole="button" accessibilityLabel={showArchived ? 'Show active rooms' : 'Show archived rooms'} onPress={() => setShowArchived((value) => !value)} style={styles.archiveToggle}>
            <SymbolView name={showArchived ? 'person.2.fill' : 'archivebox.fill'} tintColor={colors.text1} size={18} />
          </Pressable>
          <Pressable accessibilityRole="button" accessibilityLabel="Create room" onPress={() => navigation.navigate('CreateRoom')} style={styles.add}>
            <SymbolView name="plus" tintColor={colors.onAccent} size={18} />
          </Pressable>
        </View>
      }
    >
      {rooms.filter((room) => room.archived === showArchived).length === 0 && !loading ? (
        <Text style={styles.empty}>{showArchived ? 'No archived rooms.' : 'No active rooms yet.'}</Text>
      ) : (
        rooms.filter((room) => room.archived === showArchived).map((room) => (
          <Card
            key={room.id}
            title={room.name}
            subtitle={
              room.live
                ? `${room.participantCount} in room`
                : room.passcodeRequired
                  ? 'Passcode required'
                  : 'Quiet'
            }
            meta={room.createdBy ? `Created by ${room.createdBy}` : room.id}
            badge={room.live ? 'Live' : room.archived ? 'Archived' : undefined}
            badgeTone={room.live ? 'live' : 'muted'}
            onPress={() =>
              navigation.navigate('Room', { roomId: room.id, title: room.name })
            }
          />
        ))
      )}
    </Screen>
  );
}

const styles = StyleSheet.create({
  empty: {
    ...type.bodySm,
    color: colors.text2,
  },
  add: {
    width: 44,
    height: 44,
    borderRadius: 15,
    backgroundColor: colors.accent,
    alignItems: 'center',
    justifyContent: 'center',
  },
  actions: { flexDirection: 'row', gap: 8 },
  archiveToggle: {
    width: 44,
    height: 44,
    borderRadius: 15,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    alignItems: 'center',
    justifyContent: 'center',
  },
});
