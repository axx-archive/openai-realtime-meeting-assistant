import React, { useCallback, useState } from 'react';
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
import type { MainTabParamList, RootStackParamList } from '../navigation/types';
import { colors } from '../theme/colors';

type ScoutNav = CompositeNavigationProp<
  BottomTabNavigationProp<MainTabParamList, 'Scout'>,
  NativeStackNavigationProp<RootStackParamList>
>;

function threadTitle(thread: ScoutThread): string {
  return String(thread.title || thread.preview || 'Scout thread');
}

function threadSubtitle(thread: ScoutThread): string | undefined {
  const last = thread.lastMessage?.text || thread.preview;
  return last ? String(last) : undefined;
}

export function ScoutScreen() {
  const { sessionToken } = useAuth();
  const navigation = useNavigation<ScoutNav>();
  const [threads, setThreads] = useState<ScoutThread[]>([]);
  const [query, setQuery] = useState('');
  const [answer, setAnswer] = useState<string | null>(null);
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
        setError(err instanceof BonfireApiError ? err.message : 'Could not load Scout');
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

  async function askScout() {
    if (!sessionToken || !query.trim()) return;
    setAsking(true);
    setAnswer(null);
    setError(null);
    try {
      const res = await api.scoutQuery(sessionToken, query.trim());
      const text =
        (typeof res.answer === 'string' && res.answer) ||
        (typeof res.text === 'string' && res.text) ||
        JSON.stringify(res, null, 2);
      setAnswer(text);
      await load('refresh');
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Scout query failed');
    } finally {
      setAsking(false);
    }
  }

  return (
    <Screen
      title="Scout"
      subtitle="Private thinking space — same threads as the web"
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
          placeholderTextColor={colors.textTertiary}
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

      {answer ? (
        <View style={styles.answerBox}>
          <Text style={styles.answerLabel}>Scout</Text>
          <Text style={styles.answerText}>{answer}</Text>
        </View>
      ) : null}

      <Text style={styles.sectionTitle}>Threads</Text>
      {threads.length === 0 && !loading ? (
        <Text style={styles.empty}>No Scout threads yet. Ask something above.</Text>
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
              navigation.navigate('OSWeb', {
                path: `/?tool=scout&thread=${encodeURIComponent(String(thread.id))}`,
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
    backgroundColor: colors.bgElevated,
    borderRadius: 16,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.border,
    padding: 12,
    marginBottom: 16,
  },
  input: {
    minHeight: 72,
    fontSize: 16,
    color: colors.text,
    textAlignVertical: 'top',
  },
  askBtn: {
    alignSelf: 'flex-end',
    backgroundColor: colors.accent,
    borderRadius: 12,
    paddingHorizontal: 18,
    paddingVertical: 10,
    marginTop: 8,
  },
  askBtnDisabled: {
    opacity: 0.55,
  },
  askBtnText: {
    color: colors.onAccent,
    fontWeight: '600',
    fontSize: 15,
  },
  answerBox: {
    backgroundColor: colors.bgMuted,
    borderRadius: 14,
    padding: 14,
    marginBottom: 16,
  },
  answerLabel: {
    fontSize: 12,
    fontWeight: '700',
    letterSpacing: 0.5,
    textTransform: 'uppercase',
    color: colors.ember,
    marginBottom: 6,
  },
  answerText: {
    fontSize: 15,
    lineHeight: 22,
    color: colors.text,
  },
  sectionTitle: {
    fontSize: 13,
    fontWeight: '700',
    letterSpacing: 0.6,
    textTransform: 'uppercase',
    color: colors.textTertiary,
    marginBottom: 8,
  },
  empty: {
    color: colors.textSecondary,
    fontSize: 15,
  },
});
