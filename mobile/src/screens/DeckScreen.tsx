import React, { useCallback, useEffect, useState } from 'react';
import { Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type { Room } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { ChannelList } from '../messaging/ChannelList';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import type { DeckSegment, RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

type Props = NativeStackScreenProps<RootStackParamList, 'Deck'>;
type DeckNav = NativeStackNavigationProp<RootStackParamList>;

/**
 * The Deck — design §6.5.
 *
 * Not a fourth navigator: one sheet with a segmented header. The three segments
 * answer three different questions, and the segmentation is the product thesis
 * in miniature — talk → meet → result:
 *
 *   Threads  "What is being said?"
 *   Rooms    "Who is together right now?"
 *   Work     "What came out of it?"
 *
 * Detents are the real UISheetPresentationController ones, configured on the
 * route (see RootNavigator) rather than hand-rolled, so rubber-banding,
 * interactive dismissal, and VoiceOver behave the way iOS users expect.
 */

const SEGMENTS: Array<{ id: DeckSegment; label: string }> = [
  { id: 'threads', label: 'Threads' },
  { id: 'rooms', label: 'Rooms' },
  { id: 'work', label: 'Work' },
];

export function DeckScreen({ route }: Props) {
  const [segment, setSegment] = useState<DeckSegment>(route.params?.segment ?? 'threads');

  return (
    // `top` matters at the full detent, where an iOS sheet rises close enough to
    // the status bar that an un-inset header collides with the clock and the
    // Dynamic Island.
    <SafeAreaView style={styles.sheet} edges={['top', 'left', 'right']}>
      <View style={styles.segments} accessibilityRole="tablist">
        {SEGMENTS.map((item) => {
          const active = item.id === segment;
          return (
            <Pressable
              key={item.id}
              accessibilityRole="tab"
              accessibilityState={{ selected: active }}
              accessibilityLabel={item.label}
              onPress={() => setSegment(item.id)}
              style={({ pressed }) => [
                styles.segment,
                active && styles.segmentActive,
                pressed && styles.pressed,
              ]}
            >
              <Text style={[styles.segmentLabel, active && styles.segmentLabelActive]}>
                {item.label}
              </Text>
            </Pressable>
          );
        })}
      </View>

      {segment === 'threads' ? (
        <View style={styles.threadList}>
          <ChannelList />
        </View>
      ) : (
        <ScrollView
          contentContainerStyle={styles.body}
          keyboardShouldPersistTaps="handled"
          showsVerticalScrollIndicator={false}
        >
          {segment === 'rooms' ? <RoomsSegment /> : null}
          {segment === 'work' ? <WorkSegment /> : null}
        </ScrollView>
      )}
    </SafeAreaView>
  );
}

function RoomsSegment() {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const navigation = useNavigation<DeckNav>();
  const [rooms, setRooms] = useState<Room[]>([]);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!sessionToken) return;
    try {
      const response = await api.rooms(sessionToken);
      setRooms((response.rooms ?? []).filter((room) => !room.archived));
      setError(null);
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Could not load rooms.');
    }
  }, [sessionToken]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (['rooms', 'participants'].includes(office.event ?? '')) void load();
  }, [load, office.event, office.version]);

  if (error) return <Text style={styles.error}>{error}</Text>;
  if (!rooms.length) return <Text style={styles.empty}>No rooms yet.</Text>;

  return (
    <View>
      {rooms.map((room) => (
        <Pressable
          key={String(room.id)}
          accessibilityRole="button"
          accessibilityLabel={`${room.name}${room.live ? ', live' : ''}`}
          onPress={() =>
            navigation.navigate('Room', {
              roomId: String(room.id),
              title: String(room.name),
            })
          }
          style={({ pressed }) => [styles.row, pressed && styles.pressed]}
        >
          <View style={styles.rowText}>
            <Text style={styles.rowTitle} numberOfLines={1}>
              {String(room.name)}
            </Text>
            {room.live ? <Text style={styles.liveLabel}>Live now</Text> : null}
          </View>
          {room.live ? <View style={styles.liveDot} /> : null}
        </Pressable>
      ))}
    </View>
  );
}

/** The artifacts of conversation — what came out of the talking. */
function WorkSegment() {
  const navigation = useNavigation<DeckNav>();
  const destinations: Array<{
    route: 'Board' | 'Files' | 'AgentTeam' | 'Alerts' | 'Meetings' | 'Memory' | 'Intelligence' | 'Settings' | 'Profile' | 'WorkRecord' | 'NetworkPreview';
    label: string;
    hint: string;
    icon: SFSymbol;
  }> = [
    { route: 'Profile', label: 'Profile', hint: 'Identity and public profile', icon: 'person.crop.circle.fill' },
    { route: 'WorkRecord', label: 'Work record', hint: 'Verified contributions and organizations', icon: 'checkmark.seal.fill' },
    { route: 'NetworkPreview', label: 'Network', hint: 'Preview, search, contact, and blocks', icon: 'point.3.connected.trianglepath.dotted' },
    { route: 'Files', label: 'Files', hint: 'Documents and artifacts', icon: 'folder.fill' },
    { route: 'AgentTeam', label: 'Agent team', hint: 'Coworkers and Marketplace', icon: 'person.2.fill' },
    { route: 'Alerts', label: 'Alerts', hint: 'What needs you', icon: 'bell.fill' },
    { route: 'Meetings', label: 'Meetings', hint: 'Recaps and transcripts', icon: 'calendar' },
    { route: 'Memory', label: 'Memory', hint: 'What the company knows', icon: 'brain' },
    { route: 'Intelligence', label: 'Intelligence', hint: 'Themes and signals', icon: 'sparkles' },
    { route: 'Settings', label: 'Settings', hint: 'Account and voice', icon: 'gearshape.fill' },
  ];

  return (
    <View>
      {destinations.map((item) => (
        <Pressable
          key={item.route}
          accessibilityRole="button"
          accessibilityLabel={item.label}
          accessibilityHint={item.hint}
          onPress={() => navigation.navigate(item.route)}
          style={({ pressed }) => [styles.row, pressed && styles.pressed]}
        >
          <SymbolView name={item.icon} tintColor={colors.text2} size={20} />
          <View style={styles.rowText}>
            <Text style={styles.rowTitle}>{item.label}</Text>
            <Text style={styles.rowHint}>{item.hint}</Text>
          </View>
          <SymbolView name="chevron.right" tintColor={colors.text3} size={14} />
        </Pressable>
      ))}
    </View>
  );
}

const styles = StyleSheet.create({
  sheet: {
    flex: 1,
    backgroundColor: colors.bgApp,
  },
  segments: {
    flexDirection: 'row',
    gap: space[1],
    padding: space[2],
    marginHorizontal: space[4],
    marginTop: space[3],
    borderRadius: radius.md,
    backgroundColor: colors.surface3,
  },
  segment: {
    flex: 1,
    paddingVertical: space[2],
    borderRadius: radius.sm,
    alignItems: 'center',
  },
  segmentActive: {
    backgroundColor: colors.surface1,
  },
  segmentLabel: {
    ...type.captionMedium,
    color: colors.text2,
  },
  segmentLabelActive: {
    color: colors.text1,
  },
  body: {
    paddingVertical: space[3],
    paddingHorizontal: space[1],
    paddingBottom: space[10],
  },
  threadList: { flex: 1, minHeight: 0, paddingHorizontal: space[1] },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    paddingVertical: space[3],
    paddingHorizontal: space[4],
    borderRadius: radius.md,
  },
  pressed: { backgroundColor: colors.accentSoft, opacity: 0.9 },
  rowText: { flex: 1, gap: 2 },
  rowTitle: {
    ...type.bodyMedium,
    color: colors.text1,
  },
  rowHint: {
    ...type.caption,
    color: colors.text2,
  },
  liveLabel: {
    ...type.label,
    color: colors.live,
    textTransform: 'uppercase',
  },
  liveDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: colors.live,
  },
  empty: {
    ...type.bodySm,
    color: colors.text2,
    textAlign: 'center',
    paddingVertical: space[6],
  },
  error: {
    ...type.bodySm,
    color: colors.danger,
    paddingHorizontal: space[4],
  },
});
