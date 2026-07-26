import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  View,
  type ListRenderItemInfo,
} from 'react-native';
import { useFocusEffect } from '@react-navigation/native';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import { api, BonfireApiError } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Screen } from '../components/Screen';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';

type ActivityItem = {
  id: string;
  kind: 'info' | 'task' | 'agent' | 'chat' | 'alert';
  text: string;
  createdAt: string;
  read: boolean;
  tool?: string;
  redactedAt?: string;
};

type BusyAction = 'markAll' | 'clearRead' | 'clearAll' | null;
type ReloadMode = 'initial' | 'pull' | 'background';

const dateTimeFormatter = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
});

const timeFormatter = new Intl.DateTimeFormat(undefined, {
  hour: 'numeric',
  minute: '2-digit',
});

const kindPresentation: Record<
  ActivityItem['kind'],
  { label: string; symbol: SFSymbol; tone: 'neutral' | 'info' | 'ember' | 'danger' }
> = {
  info: { label: 'Update', symbol: 'bell.fill', tone: 'neutral' },
  task: { label: 'Task', symbol: 'checklist', tone: 'info' },
  agent: { label: 'Scout', symbol: 'sparkles', tone: 'ember' },
  chat: { label: 'Chat', symbol: 'bubble.left.fill', tone: 'info' },
  alert: { label: 'Alert', symbol: 'exclamationmark.triangle.fill', tone: 'danger' },
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function normalizeKind(value: unknown): ActivityItem['kind'] {
  return value === 'task' || value === 'agent' || value === 'chat' || value === 'alert'
    ? value
    : 'info';
}

function normalizeNotifications(value: unknown): ActivityItem[] {
  if (!isRecord(value) || !Array.isArray(value.notifications)) return [];

  return value.notifications.flatMap((entry) => {
    if (!isRecord(entry) || typeof entry.id !== 'string' || typeof entry.text !== 'string') {
      return [];
    }
    return [{
      id: entry.id,
      kind: normalizeKind(entry.kind),
      text: entry.text,
      createdAt: typeof entry.createdAt === 'string' ? entry.createdAt : '',
      read: entry.read === true,
      tool: typeof entry.tool === 'string' ? entry.tool : undefined,
      redactedAt: typeof entry.redactedAt === 'string' ? entry.redactedAt : undefined,
    }];
  });
}

function formatCreatedAt(value: string): string {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return 'Recently';

  const date = new Date(timestamp);
  const now = new Date();
  const today = date.getFullYear() === now.getFullYear()
    && date.getMonth() === now.getMonth()
    && date.getDate() === now.getDate();
  return today ? `Today at ${timeFormatter.format(date)}` : dateTimeFormatter.format(date);
}

function displayTool(tool?: string): string | null {
  const value = tool?.trim();
  if (!value) return null;
  return value
    .split(/[_-]+/)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');
}

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof BonfireApiError || error instanceof Error) return error.message;
  return fallback;
}

type ActivityRowProps = {
  item: ActivityItem;
  pending: boolean;
  onMarkRead: (id: string) => void;
};

const ActivityRow = memo(function ActivityRow({ item, pending, onMarkRead }: ActivityRowProps) {
  const presentation = kindPresentation[item.kind];
  const tool = displayTool(item.tool);
  const handlePress = useCallback(() => onMarkRead(item.id), [item.id, onMarkRead]);
  const body = item.redactedAt ? 'This private update has expired.' : item.text;
  const accessibilityLabel = `${item.read ? 'Read' : 'Unread'} ${presentation.label}. ${body}. ${formatCreatedAt(item.createdAt)}`;

  return (
    <Pressable
      accessibilityRole={item.read ? undefined : 'button'}
      accessibilityLabel={accessibilityLabel}
      accessibilityHint={item.read ? undefined : 'Marks this activity as read'}
      accessibilityState={{ disabled: item.read, busy: pending }}
      disabled={item.read || pending}
      onPress={handlePress}
      style={({ pressed }) => [
        styles.item,
        shadow[1],
        !item.read && styles.itemUnread,
        pressed && styles.itemPressed,
      ]}
    >
      {!item.read ? <View style={styles.unreadRail} /> : null}
      <View
        style={[
          styles.icon,
          presentation.tone === 'info' && styles.iconInfo,
          presentation.tone === 'ember' && styles.iconEmber,
          presentation.tone === 'danger' && styles.iconDanger,
        ]}
      >
        <SymbolView
          name={presentation.symbol}
          size={17}
          tintColor={
            presentation.tone === 'info'
              ? colors.info
              : presentation.tone === 'ember'
                ? colors.ember
                : presentation.tone === 'danger'
                  ? colors.danger
                  : colors.text2
          }
        />
      </View>
      <View style={styles.itemCopy}>
        <View style={styles.itemTopline}>
          <Text style={[styles.kind, !item.read && styles.kindUnread]}>{presentation.label}</Text>
          {!item.read ? <View accessibilityLabel="Unread" style={styles.unreadDot} /> : null}
        </View>
        <Text style={[styles.itemText, item.read && styles.itemTextRead]}>{body}</Text>
        <View style={styles.metaRow}>
          <Text style={styles.meta}>{formatCreatedAt(item.createdAt)}</Text>
          {tool ? (
            <>
              <Text accessibilityElementsHidden style={styles.metaSeparator}>·</Text>
              <Text style={styles.meta} numberOfLines={1}>{tool}</Text>
            </>
          ) : null}
        </View>
      </View>
      {pending ? <ActivityIndicator size="small" color={colors.accent} /> : null}
    </Pressable>
  );
});

type ActionButtonProps = {
  label: string;
  disabled: boolean;
  busy: boolean;
  destructive?: boolean;
  symbol: SFSymbol;
  onPress: () => void;
};

function ActionButton({ label, disabled, busy, destructive, symbol, onPress }: ActionButtonProps) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityState={{ disabled, busy }}
      disabled={disabled}
      onPress={onPress}
      style={({ pressed }) => [
        styles.actionButton,
        destructive && styles.actionButtonDestructive,
        disabled && styles.actionButtonDisabled,
        pressed && styles.actionButtonPressed,
      ]}
    >
      {busy ? (
        <ActivityIndicator size="small" color={destructive ? colors.danger : colors.accent} />
      ) : (
        <SymbolView
          name={symbol}
          size={15}
          tintColor={destructive ? colors.danger : colors.text1}
        />
      )}
      <Text style={[styles.actionLabel, destructive && styles.actionLabelDestructive]}>{label}</Text>
    </Pressable>
  );
}

export function AlertsScreen() {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const [items, setItems] = useState<ActivityItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<BusyAction>(null);
  const [pendingIds, setPendingIds] = useState<Set<string>>(() => new Set());
  const loaded = useRef(false);
  const loadVersion = useRef(0);

  const unreadIds = useMemo(() => items.filter((item) => !item.read).map((item) => item.id), [items]);
  const readIds = useMemo(() => items.filter((item) => item.read).map((item) => item.id), [items]);
  const mutationBusy = busyAction !== null || pendingIds.size > 0;

  const reload = useCallback(async (mode: ReloadMode = 'initial') => {
    if (!sessionToken) return;
    const version = ++loadVersion.current;
    if (mode === 'initial' && !loaded.current) setLoading(true);
    if (mode === 'pull') setRefreshing(true);
    setError(null);
    try {
      const response = await api.notifications(sessionToken);
      if (version !== loadVersion.current) return;
      setItems(normalizeNotifications(response));
      loaded.current = true;
    } catch (loadError) {
      if (version === loadVersion.current) {
        setError(errorMessage(loadError, 'Activity could not be loaded.'));
      }
    } finally {
      if (version === loadVersion.current) {
        setLoading(false);
        setRefreshing(false);
      }
    }
  }, [sessionToken]);

  useFocusEffect(
    useCallback(() => {
      void reload(loaded.current ? 'background' : 'initial');
    }, [reload]),
  );

  useEffect(() => {
    if (office.event === 'notification' || office.event === 'notification_backlog') {
      void reload('background');
    }
  }, [office.event, office.version, reload]);

  const markOneRead = useCallback(async (id: string) => {
    if (!sessionToken) return;
    ++loadVersion.current;
    setError(null);
    setPendingIds((current) => new Set(current).add(id));
    try {
      await api.markNotificationsRead(sessionToken, [id]);
      setItems((current) => current.map((item) => item.id === id ? { ...item, read: true } : item));
    } catch (markError) {
      setError(errorMessage(markError, 'That activity could not be marked as read.'));
    } finally {
      setPendingIds((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
    }
  }, [sessionToken]);

  const markAllRead = useCallback(async () => {
    if (!sessionToken || unreadIds.length === 0) return;
    ++loadVersion.current;
    setError(null);
    setBusyAction('markAll');
    try {
      await api.markNotificationsRead(sessionToken, unreadIds);
      setItems((current) => current.map((item) => ({ ...item, read: true })));
    } catch (markError) {
      setError(errorMessage(markError, 'Activity could not be marked as read.'));
    } finally {
      setBusyAction(null);
    }
  }, [sessionToken, unreadIds]);

  const clearItems = useCallback(async (mode: 'read' | 'all') => {
    if (!sessionToken) return;
    const ids = mode === 'read' ? readIds : [];
    ++loadVersion.current;
    setError(null);
    setBusyAction(mode === 'read' ? 'clearRead' : 'clearAll');
    try {
      await api.clearNotifications(sessionToken, ids);
      await reload('background');
    } catch (clearError) {
      setError(errorMessage(clearError, 'Activity could not be cleared.'));
    } finally {
      setBusyAction(null);
    }
  }, [readIds, reload, sessionToken]);

  const confirmClearRead = useCallback(() => {
    Alert.alert(
      'Clear read activity?',
      `This removes ${readIds.length === 1 ? '1 read update' : `${readIds.length} read updates`} from your activity view.`,
      [
        { text: 'Cancel', style: 'cancel' },
        { text: 'Clear read', style: 'destructive', onPress: () => void clearItems('read') },
      ],
    );
  }, [clearItems, readIds.length]);

  const confirmClearAll = useCallback(() => {
    Alert.alert(
      'Clear all activity?',
      'This dismisses every clearable update for your account. Pending approval requests may remain.',
      [
        { text: 'Cancel', style: 'cancel' },
        { text: 'Clear all', style: 'destructive', onPress: () => void clearItems('all') },
      ],
    );
  }, [clearItems]);

  const renderItem = useCallback(
    ({ item }: ListRenderItemInfo<ActivityItem>) => (
      <ActivityRow item={item} pending={pendingIds.has(item.id)} onMarkRead={markOneRead} />
    ),
    [markOneRead, pendingIds],
  );

  const listHeader = (
    <>
      <View style={[styles.summary, shadow[1]]}>
        <View style={styles.summaryIcon}>
          <SymbolView name="bell.badge.fill" size={20} tintColor={colors.info} />
        </View>
        <View style={styles.summaryCopy}>
          <Text style={styles.summaryValue}>{unreadIds.length}</Text>
          <Text style={styles.summaryLabel}>{unreadIds.length === 1 ? 'unread update' : 'unread updates'}</Text>
        </View>
        <View style={styles.liveStatus}>
          <View style={[styles.liveDot, !office.connected && styles.liveDotOffline]} />
          <Text style={styles.liveLabel}>{office.connected ? 'Live' : 'Reconnecting'}</Text>
        </View>
      </View>

      <View accessibilityRole="toolbar" accessibilityLabel="Activity actions" style={styles.actions}>
        <ActionButton
          label="Mark all read"
          symbol="checkmark.circle"
          disabled={mutationBusy || unreadIds.length === 0}
          busy={busyAction === 'markAll'}
          onPress={() => void markAllRead()}
        />
        <ActionButton
          label="Clear read"
          symbol="trash"
          destructive
          disabled={mutationBusy || readIds.length === 0}
          busy={busyAction === 'clearRead'}
          onPress={confirmClearRead}
        />
        <ActionButton
          label="Clear all"
          symbol="trash.slash"
          destructive
          disabled={mutationBusy || items.length === 0}
          busy={busyAction === 'clearAll'}
          onPress={confirmClearAll}
        />
      </View>

      {items.length > 0 ? <Text style={styles.sectionLabel}>RECENT</Text> : null}
    </>
  );

  const emptyState = !loading && !error ? (
    <View style={styles.empty}>
      <View style={styles.emptyIcon}>
        <SymbolView name="checkmark" size={24} tintColor={colors.success} />
      </View>
      <Text style={styles.emptyTitle}>You’re all caught up.</Text>
      <Text style={styles.emptyBody}>New agent work, mentions, decisions, and alerts will appear here.</Text>
    </View>
  ) : null;

  return (
    <Screen
      title="Activity"
      subtitle="Agent work, mentions, decisions, and alerts"
      loading={loading}
      error={error}
      onRetry={() => void reload(loaded.current ? 'pull' : 'initial')}
      scroll={false}
    >
      <FlatList
        accessibilityLabel="Activity updates"
        data={items}
        keyExtractor={(item) => item.id}
        renderItem={renderItem}
        ListHeaderComponent={listHeader}
        ListEmptyComponent={emptyState}
        contentContainerStyle={[styles.listContent, items.length === 0 && styles.listContentEmpty]}
        refreshControl={(
          <RefreshControl
            refreshing={refreshing}
            onRefresh={() => void reload('pull')}
            tintColor={colors.accent}
          />
        )}
        showsVerticalScrollIndicator={false}
      />
    </Screen>
  );
}

const styles = StyleSheet.create({
  listContent: {
    paddingBottom: space[10],
  },
  listContentEmpty: {
    flexGrow: 1,
  },
  summary: {
    minHeight: 82,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    padding: space[4],
    marginBottom: space[3],
    borderRadius: radius.xl,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
  },
  summaryIcon: {
    width: 42,
    height: 42,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.md,
    backgroundColor: colors.infoSoft,
  },
  summaryCopy: {
    flex: 1,
  },
  summaryValue: {
    ...type.title2,
    color: colors.text1,
    fontVariant: ['tabular-nums'],
  },
  summaryLabel: {
    ...type.caption,
    color: colors.text2,
  },
  liveStatus: {
    minHeight: hitMin,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
  },
  liveDot: {
    width: 7,
    height: 7,
    borderRadius: radius.full,
    backgroundColor: colors.live,
  },
  liveDotOffline: {
    backgroundColor: colors.text3,
  },
  liveLabel: {
    ...type.captionMedium,
    color: colors.text2,
  },
  actions: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: space[2],
    marginBottom: space[5],
  },
  actionButton: {
    minWidth: 126,
    minHeight: hitMin,
    flexGrow: 1,
    flexBasis: '30%',
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 7,
    paddingHorizontal: space[3],
    borderRadius: radius.md,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
  },
  actionButtonDestructive: {
    backgroundColor: colors.dangerSoft,
    borderColor: colors.dangerSoft,
  },
  actionButtonDisabled: {
    opacity: 0.4,
  },
  actionButtonPressed: {
    opacity: 0.78,
    transform: [{ scale: 0.96 }],
  },
  actionLabel: {
    ...type.button,
    color: colors.text1,
  },
  actionLabelDestructive: {
    color: colors.danger,
  },
  sectionLabel: {
    ...type.label,
    color: colors.text3,
    letterSpacing: 1.05,
    marginBottom: space[2],
    marginLeft: space[1],
  },
  item: {
    position: 'relative',
    minHeight: 94,
    flexDirection: 'row',
    alignItems: 'flex-start',
    gap: space[3],
    padding: space[4],
    paddingLeft: space[5],
    marginBottom: space[3],
    borderRadius: radius.lg,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    overflow: 'hidden',
  },
  itemUnread: {
    backgroundColor: colors.infoSoft,
    borderColor: colors.line2,
  },
  itemPressed: {
    opacity: 0.86,
    transform: [{ scale: 0.96 }],
  },
  unreadRail: {
    position: 'absolute',
    top: space[3],
    bottom: space[3],
    left: 0,
    width: 3,
    borderRadius: radius.full,
    backgroundColor: colors.info,
  },
  icon: {
    width: 38,
    height: 38,
    borderRadius: 11,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface3,
  },
  iconInfo: {
    backgroundColor: colors.infoSoft,
  },
  iconEmber: {
    backgroundColor: colors.emberSoft,
  },
  iconDanger: {
    backgroundColor: colors.dangerSoft,
  },
  itemCopy: {
    flex: 1,
  },
  itemTopline: {
    minHeight: 18,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 7,
    marginBottom: 4,
  },
  kind: {
    ...type.label,
    color: colors.text3,
    textTransform: 'uppercase',
  },
  kindUnread: {
    color: colors.info,
  },
  unreadDot: {
    width: 6,
    height: 6,
    borderRadius: radius.full,
    backgroundColor: colors.info,
  },
  itemText: {
    ...type.bodyMedium,
    color: colors.text1,
  },
  itemTextRead: {
    ...type.body,
    color: colors.text2,
  },
  metaRow: {
    flexDirection: 'row',
    alignItems: 'center',
    marginTop: space[2],
  },
  meta: {
    ...type.caption,
    color: colors.text3,
    flexShrink: 1,
  },
  metaSeparator: {
    ...type.caption,
    color: colors.text3,
    marginHorizontal: 6,
  },
  empty: {
    flex: 1,
    minHeight: 280,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: space[6],
    paddingBottom: space[8],
  },
  emptyIcon: {
    width: 54,
    height: 54,
    borderRadius: radius.lg,
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: space[4],
    backgroundColor: colors.liveSoft,
  },
  emptyTitle: {
    ...type.headline,
    color: colors.text1,
    textAlign: 'center',
  },
  emptyBody: {
    ...type.bodySm,
    color: colors.text2,
    textAlign: 'center',
    marginTop: space[2],
    maxWidth: 280,
  },
});
