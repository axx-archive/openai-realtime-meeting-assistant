import React, { useCallback, useMemo, useState } from 'react';
import { StyleSheet, Text, View } from 'react-native';
import { useFocusEffect } from '@react-navigation/native';
import { api, BonfireApiError } from '../api/client';
import type { BoardCard } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { Card } from '../components/Card';
import { Screen } from '../components/Screen';
import { colors, space, type } from '../theme/tokens';

function cardColumn(card: BoardCard): string {
  return String(card.column || card.status || 'backlog').toLowerCase();
}

function cardTitle(card: BoardCard): string {
  return String(card.title || card.body || card.id || 'Untitled');
}

export function BoardScreen() {
  const { sessionToken } = useAuth();
  const [cards, setCards] = useState<BoardCard[]>([]);
  const [updatedAt, setUpdatedAt] = useState<string | undefined>();
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
        const res = await api.board(sessionToken);
        setCards(res.board?.cards ?? []);
        setUpdatedAt(res.board?.updatedAt);
      } catch (err) {
        setError(err instanceof BonfireApiError ? err.message : 'Could not load board');
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

  const grouped = useMemo(() => {
    const map = new Map<string, BoardCard[]>();
    for (const card of cards) {
      const col = cardColumn(card);
      const list = map.get(col) ?? [];
      list.push(card);
      map.set(col, list);
    }
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b));
  }, [cards]);

  return (
    <Screen
      title="Board"
      subtitle={
        updatedAt
          ? `Shared kanban · updated ${new Date(updatedAt).toLocaleString()}`
          : 'Same cards as the web board'
      }
      loading={loading}
      error={error}
      onRetry={() => void load('initial')}
      refreshing={refreshing}
      onRefresh={() => void load('refresh')}
    >
      {cards.length === 0 && !loading ? (
        <Text style={styles.empty}>No board cards yet.</Text>
      ) : (
        grouped.map(([column, colCards]) => (
          <View key={column} style={styles.section}>
            <Text style={styles.sectionTitle}>
              {column} · {colCards.length}
            </Text>
            {colCards.map((card) => (
              <Card
                key={String(card.id)}
                title={cardTitle(card)}
                subtitle={
                  typeof card.body === 'string' && card.body !== cardTitle(card)
                    ? card.body
                    : undefined
                }
                meta={
                  [card.owner, Array.isArray(card.labels) ? card.labels.join(' · ') : '']
                    .filter(Boolean)
                    .join(' · ') || undefined
                }
              />
            ))}
          </View>
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
  section: {
    marginBottom: space[2],
  },
  sectionTitle: {
    ...type.label,
    color: colors.text3,
    marginBottom: space[2],
    marginTop: space[2],
    textTransform: 'uppercase',
  },
});
