import React, { useCallback, useEffect, useState } from 'react';
import {
  ActivityIndicator,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import {
  useFocusEffect,
  useNavigation,
  type CompositeNavigationProp,
} from '@react-navigation/native';
import type { BottomTabNavigationProp } from '@react-navigation/bottom-tabs';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type { ScoutThread } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { Card } from '../components/Card';
import { Screen } from '../components/Screen';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import type { MainTabParamList, RootStackParamList } from '../navigation/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';

type ChatNav = CompositeNavigationProp<
  BottomTabNavigationProp<MainTabParamList, 'Chat'>,
  NativeStackNavigationProp<RootStackParamList>
>;

function threadTitle(thread: ScoutThread): string {
  return String(thread.title || thread.preview || 'Thread');
}

function threadSubtitle(thread: ScoutThread): string | undefined {
  const last = thread.lastMessage?.text || thread.preview;
  return last ? String(last) : undefined;
}

/** Chat tab — same `/assistant/chat-threads` + Scout query as the web OS. */
export function ScoutScreen() {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const navigation = useNavigation<ChatNav>();
  const [threads, setThreads] = useState<ScoutThread[]>([]);
  const [query, setQuery] = useState('');
  const [asking, setAsking] = useState(false);
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
        const res = await api.scoutThreads(sessionToken);
        setThreads(res.threads ?? []);
      } catch (err) {
        setError(err instanceof BonfireApiError ? err.message : 'Could not load chat');
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
    if (office.event === 'chat_thread') void load('refresh');
  }, [load, office.event, office.version]);

  async function askScout() {
    if (!sessionToken || !query.trim()) return;
    setAsking(true);
    setError(null);
    try {
      const text = query.trim();
      const title = text.length > 54 ? `${text.slice(0, 51).trimEnd()}…` : text;
      const created = await api.createScoutThread(sessionToken, { title, visibility: 'private' });
      const threadId = String(created.thread?.id ?? '');
      if (!threadId) throw new Error('Scout did not return a thread.');
      setQuery('');
      await api.sendScoutMessage(sessionToken, threadId, text);
      await load('refresh');
      navigation.navigate('Thread', { threadId, title });
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Scout query failed');
    } finally {
      setAsking(false);
    }
  }

  return (
    <Screen
      title="Chat"
      subtitle="Scout threads — same data as the web"
      loading={loading}
      error={error}
      onRetry={() => void load('initial')}
      refreshing={refreshing}
      onRefresh={() => void load('refresh')}
    >
      <View style={styles.composer}>
        <TextInput
          style={styles.input}
          placeholder="Ask Scout…"
          placeholderTextColor={colors.text3}
          value={query}
          onChangeText={setQuery}
          multiline
          editable={!asking}
        />
        <Pressable
          onPress={() => void askScout()}
          disabled={asking || !query.trim()}
          style={({ pressed }) => [
            styles.askBtn,
            (pressed || asking || !query.trim()) && styles.askBtnDisabled,
          ]}
        >
          {asking ? (
            <ActivityIndicator color={colors.onAccent} />
          ) : (
            <Text style={styles.askBtnText}>Ask</Text>
          )}
        </Pressable>
      </View>

      <Text style={styles.sectionTitle}>Threads</Text>
      {threads.length === 0 && !loading ? (
        <Text style={styles.empty}>No threads yet. Ask something above.</Text>
      ) : (
        threads.map((thread) => (
          <Card
            key={String(thread.id)}
            title={threadTitle(thread)}
            subtitle={threadSubtitle(thread)}
            meta={[
              thread.visibility,
              thread.updatedAt ? new Date(String(thread.updatedAt)).toLocaleString() : '',
            ]
              .filter(Boolean)
              .join(' · ')}
            badge={thread.visibility === 'public' ? 'public' : 'private'}
            onPress={() =>
              navigation.navigate('Thread', {
                threadId: String(thread.id),
                title: threadTitle(thread),
              })
            }
          />
        ))
      )}
    </Screen>
  );
}

const styles = StyleSheet.create({
  composer: {
    backgroundColor: colors.surface1,
    borderRadius: radius.lg,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    padding: space[3],
    marginBottom: space[4],
  },
  input: {
    minHeight: 72,
    fontSize: 15,
    letterSpacing: -0.08,
    color: colors.text1,
    textAlignVertical: 'top',
  },
  askBtn: {
    alignSelf: 'flex-end',
    backgroundColor: colors.accent,
    borderRadius: radius.md,
    minHeight: hitMin,
    paddingHorizontal: space[4],
    alignItems: 'center',
    justifyContent: 'center',
    marginTop: space[2],
  },
  askBtnDisabled: {
    opacity: 0.55,
  },
  askBtnText: {
    ...type.button,
    color: colors.onAccent,
  },
  sectionTitle: {
    ...type.label,
    color: colors.text3,
    marginBottom: space[2],
    textTransform: 'uppercase',
  },
  empty: {
    ...type.bodySm,
    color: colors.text2,
  },
});
