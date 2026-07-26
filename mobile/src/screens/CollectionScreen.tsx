import React, { useCallback, useEffect, useState } from 'react';
import { Pressable, Text, StyleSheet } from 'react-native';
import { useFocusEffect } from '@react-navigation/native';
import { BonfireApiError } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Card } from '../components/Card';
import { Screen } from '../components/Screen';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { colors, type } from '../theme/tokens';
import { displayMeta, displaySubtitle, displayTitle, firstArray } from '../utils/records';

type Props = {
  title: string;
  subtitle: string;
  empty: string;
  keys: string[];
  load: (sessionToken: string) => Promise<unknown>;
  events?: string[];
  actionLabel?: string;
  actionHint?: string;
  action?: (sessionToken: string) => Promise<unknown>;
};

export function CollectionScreen({
  title,
  subtitle,
  empty,
  keys,
  load,
  events = [],
  actionLabel,
  actionHint,
  action,
}: Props) {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const [items, setItems] = useState<unknown[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [acting, setActing] = useState(false);

  const reload = useCallback(
    async (refresh = false) => {
      if (!sessionToken) return;
      refresh ? setRefreshing(true) : setLoading(true);
      setError(null);
      try {
        const response = await load(sessionToken);
        setItems(firstArray(response, keys));
      } catch (err) {
        setError(err instanceof BonfireApiError ? err.message : `Could not load ${title.toLowerCase()}`);
      } finally {
        setLoading(false);
        setRefreshing(false);
      }
    },
    [keys, load, sessionToken, title],
  );

  useFocusEffect(
    useCallback(() => {
      void reload();
    }, [reload]),
  );

  useEffect(() => {
    if (office.event && events.includes(office.event)) void reload(true);
  }, [events, office.event, office.version, reload]);

  const runAction = useCallback(async () => {
    if (!sessionToken || !action || acting) return;
    setActing(true);
    setError(null);
    try {
      await action(sessionToken);
      await reload(true);
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : `Could not refresh ${title.toLowerCase()}`);
    } finally {
      setActing(false);
    }
  }, [action, acting, reload, sessionToken, title]);

  return (
    <Screen
      title={title}
      subtitle={subtitle}
      loading={loading}
      error={error}
      onRetry={() => void reload()}
      refreshing={refreshing}
      onRefresh={() => void reload(true)}
    >
      {action && actionLabel ? (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={actionLabel}
          accessibilityHint={actionHint}
          accessibilityState={{ busy: acting, disabled: acting }}
          disabled={acting}
          onPress={() => void runAction()}
          style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}
        >
          <Text style={styles.actionText}>{acting ? 'Refreshing…' : actionLabel}</Text>
        </Pressable>
      ) : null}
      {!items.length && !loading ? <Text style={styles.empty}>{empty}</Text> : null}
      {items.map((item, index) => (
        <Card
          key={`${displayTitle(item)}-${index}`}
          title={displayTitle(item)}
          subtitle={displaySubtitle(item) || undefined}
          meta={displayMeta(item) || undefined}
        />
      ))}
    </Screen>
  );
}

const styles = StyleSheet.create({
  empty: {
    ...type.bodySm,
    color: colors.text2,
  },
  action: {
    minHeight: 44,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: 12,
    backgroundColor: colors.accent,
    marginBottom: 16,
  },
  actionPressed: { opacity: 0.78, transform: [{ scale: 0.98 }] },
  actionText: { ...type.button, color: colors.onAccent },
});
