import React, { useCallback, useState } from 'react';
import { Text, StyleSheet } from 'react-native';
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
import type { MainTabParamList, RootStackParamList } from '../navigation/types';
import { colors, type } from '../theme/tokens';

type RoomsNav = CompositeNavigationProp<
  BottomTabNavigationProp<MainTabParamList, 'Rooms'>,
  NativeStackNavigationProp<RootStackParamList>
>;

export function RoomsScreen() {
  const { sessionToken } = useAuth();
  const navigation = useNavigation<RoomsNav>();
  const [rooms, setRooms] = useState<Room[]>([]);
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
        setRooms((res.rooms ?? []).filter((r) => !r.archived));
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

  return (
    <Screen
      title="Rooms"
      subtitle="Live spaces shared with the web OS"
      loading={loading}
      error={error}
      onRetry={() => void load('initial')}
      refreshing={refreshing}
      onRefresh={() => void load('refresh')}
    >
      {rooms.length === 0 && !loading ? (
        <Text style={styles.empty}>No active rooms yet.</Text>
      ) : (
        rooms.map((room) => (
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
            badge={room.live ? 'Live' : undefined}
            badgeTone={room.live ? 'live' : 'muted'}
            onPress={() =>
              navigation.navigate('OSWeb', {
                path: `/?room=${encodeURIComponent(room.id)}`,
                title: room.name,
              })
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
});
