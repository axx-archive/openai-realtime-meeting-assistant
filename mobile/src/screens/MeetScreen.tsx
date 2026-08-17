import React, { useCallback, useEffect, useState } from 'react';
import { Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type { Room } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

type MeetNav = NativeStackNavigationProp<RootStackParamList>;

/**
 * The Meet destination — a card surface for discovering and creating rooms.
 *
 * This is a proper destination, not a peek sheet. Selecting Meet fills the
 * content area on phone (with the rail visible) and shows sidebar + list on
 * tablet ≥744. The Work segment is deliberately absent; Meet is purely about
 * live video rooms, not the artifacts and tools that live in Work.
 */

export function MeetScreen() {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const navigation = useNavigation<MeetNav>();
  const [rooms, setRooms] = useState<Room[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!sessionToken) return;
    setLoading(true);
    try {
      const response = await api.rooms(sessionToken);
      setRooms((response.rooms ?? []).filter((room) => !room.archived));
      setError(null);
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Could not load rooms.');
    } finally {
      setLoading(false);
    }
  }, [sessionToken]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (['rooms', 'participants'].includes(office.event ?? '')) void load();
  }, [load, office.event, office.version]);

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right']}>
      <View style={styles.header}>
        <Text accessibilityRole="header" style={styles.title}>Meet</Text>
        <Pressable
          accessibilityLabel="Create room"
          accessibilityRole="button"
          onPress={() => navigation.navigate('CreateRoom')}
          style={({ pressed }) => [styles.createButton, pressed && styles.pressed]}
        >
          <SymbolView name="plus" size={16} tintColor={colors.onAccent} />
          <Text style={styles.createText}>Create room</Text>
        </Pressable>
      </View>

      <ScrollView
        contentContainerStyle={styles.body}
        keyboardDismissMode="on-drag"
        keyboardShouldPersistTaps="handled"
        showsVerticalScrollIndicator={false}
      >
        {loading ? (
          <View accessibilityLabel="Loading rooms" accessibilityRole="progressbar" style={styles.loadingContainer}>
            {Array.from({ length: 4 }, (_, index) => (
              <View key={`room-loading-${index}`} style={styles.loadingRow}>
                <View style={styles.loadingIcon} />
                <View style={styles.loadingCopy}>
                  <View style={[styles.loadingLine, index % 2 === 0 ? styles.loadingLineShort : null]} />
                </View>
              </View>
            ))}
          </View>
        ) : error ? (
          <Pressable
            accessibilityLabel={`${error}. Tap to retry`}
            accessibilityRole="button"
            onPress={() => void load()}
            style={styles.errorBox}
          >
            <SymbolView name="exclamationmark.triangle.fill" size={24} tintColor={colors.danger} />
            <Text style={styles.errorText}>{error}</Text>
            <Text style={styles.retryText}>Tap to retry</Text>
          </Pressable>
        ) : rooms.length === 0 ? (
          <View style={styles.emptyContainer}>
            <View style={styles.emptyIcon}>
              <SymbolView name="video.fill" size={32} tintColor={colors.text3} />
            </View>
            <Text style={styles.emptyTitle}>No rooms yet</Text>
            <Text style={styles.emptyBody}>
              Create a room to start a video call with your team. Rooms persist so
              people can drop in anytime.
            </Text>
            <Pressable
              accessibilityLabel="Create your first room"
              accessibilityRole="button"
              onPress={() => navigation.navigate('CreateRoom')}
              style={({ pressed }) => [styles.emptyAction, pressed && styles.pressed]}
            >
              <Text style={styles.emptyActionText}>Create your first room</Text>
            </Pressable>
          </View>
        ) : (
          <View style={styles.roomList}>
            {rooms.map((room) => (
              <Pressable
                key={String(room.id)}
                accessibilityRole="button"
                accessibilityLabel={`${room.name}${room.live ? ', live now' : ''}`}
                onPress={() =>
                  navigation.navigate('Room', {
                    roomId: String(room.id),
                    title: String(room.name),
                  })
                }
                style={({ pressed }) => [styles.row, pressed && styles.pressed]}
              >
                <View style={[styles.roomIcon, room.live && styles.roomIconLive]}>
                  <SymbolView
                    name={room.live ? 'video.fill' : 'video'}
                    size={18}
                    tintColor={room.live ? colors.onAccent : colors.text2}
                  />
                </View>
                <View style={styles.rowText}>
                  <Text style={styles.rowTitle} numberOfLines={1}>
                    {String(room.name)}
                  </Text>
                  {room.live ? <Text style={styles.liveLabel}>Live now</Text> : null}
                </View>
                {room.live ? <View style={styles.liveDot} /> : null}
                <SymbolView name="chevron.right" size={14} tintColor={colors.text3} />
              </Pressable>
            ))}
          </View>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  header: {
    minHeight: 60,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: space[5],
    paddingTop: space[2],
  },
  title: { ...type.title1, color: colors.text1 },
  createButton: {
    minHeight: 36,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    paddingHorizontal: space[4],
    borderRadius: radius.full,
    backgroundColor: colors.accent,
  },
  createText: { ...type.button, color: colors.onAccent, fontSize: 14 },
  body: {
    width: '100%',
    maxWidth: 820,
    alignSelf: 'center',
    padding: space[5],
    paddingBottom: space[10],
  },
  loadingContainer: { gap: space[2] },
  loadingRow: {
    minHeight: 64,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    padding: space[3],
    borderRadius: radius.lg,
    backgroundColor: colors.surface1,
  },
  loadingIcon: { width: 44, height: 44, borderRadius: radius.md, backgroundColor: colors.surface2 },
  loadingCopy: { flex: 1, gap: 8 },
  loadingLine: { width: '60%', height: 12, borderRadius: radius.full, backgroundColor: colors.surface2 },
  loadingLineShort: { width: '40%' },
  errorBox: {
    alignItems: 'center',
    gap: space[3],
    padding: space[6],
    borderRadius: radius.lg,
    backgroundColor: colors.dangerSoft,
  },
  errorText: { ...type.body, color: colors.danger, textAlign: 'center' },
  retryText: { ...type.button, color: colors.ember },
  emptyContainer: {
    alignItems: 'center',
    gap: space[3],
    padding: space[6],
    borderRadius: radius.xl,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.border,
  },
  emptyIcon: {
    width: 72,
    height: 72,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
    backgroundColor: colors.surface2,
    marginBottom: space[2],
  },
  emptyTitle: { ...type.title2, color: colors.text1 },
  emptyBody: {
    ...type.body,
    color: colors.text2,
    textAlign: 'center',
    maxWidth: 320,
  },
  emptyAction: {
    minHeight: 44,
    paddingHorizontal: space[5],
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
    backgroundColor: colors.accent,
    marginTop: space[2],
  },
  emptyActionText: { ...type.button, color: colors.onAccent },
  roomList: { gap: space[2] },
  row: {
    minHeight: 72,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    padding: space[3],
    borderRadius: radius.lg,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.border,
  },
  roomIcon: {
    width: 44,
    height: 44,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.md,
    backgroundColor: colors.surface3,
  },
  roomIconLive: { backgroundColor: colors.ember },
  rowText: { flex: 1, gap: 2 },
  rowTitle: { ...type.bodyMedium, color: colors.text1 },
  liveLabel: {
    ...type.label,
    color: colors.emberText,
    textTransform: 'uppercase',
  },
  liveDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: colors.ember,
  },
  pressed: { opacity: 0.82, transform: [{ scale: 0.98 }] },
});
