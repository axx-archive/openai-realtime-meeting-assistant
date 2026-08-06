import React, { useCallback, useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Keyboard, Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import * as Haptics from 'expo-haptics';
import { useFocusEffect, useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type { ScoutMessage, ScoutThread } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import type { RootStackParamList } from '../navigation/types';
import { SymbolView } from 'expo-symbols';
import { colors, radius, space, type } from '../theme/tokens';
import { channelDisplayName, isBonfireChat, pinBonfireChatFirst } from './channelPresentation';

/**
 * The Threads segment — design §14.
 *
 * Public threads ARE channels server-side: `#`-prefixed, broadcast-notifying,
 * @-mention-parsing. The old client rendered them as static cards above an "Ask"
 * textarea, which reads as a search tool. These are channel rows.
 */

type ChannelNav = NativeStackNavigationProp<RootStackParamList>;

function preview(thread: ScoutThread): string {
  const last = thread.lastMessage?.text || thread.preview || '';
  return String(last).replace(/\s+/g, ' ').trim();
}

function timeAgo(raw: unknown): string {
  if (!raw) return '';
  const at = new Date(String(raw));
  if (Number.isNaN(at.getTime())) return '';
  const minutes = Math.round((Date.now() - at.getTime()) / 60000);
  if (minutes < 1) return 'now';
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}

const activeWorkStatuses = new Set(['queued', 'running', 'approval_required', 'needs_input', 'parked']);

type ActiveWork = { message: ScoutMessage; work: NonNullable<ScoutMessage['thread']> };

function activeWork(thread: ScoutThread): ActiveWork | null {
  const messages = Array.isArray(thread.messages) ? thread.messages : [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (!message.thread || !activeWorkStatuses.has(String(message.thread.status ?? '').toLowerCase())) continue;
    return { message, work: message.thread };
  }
  return null;
}

const ActiveWorkTimer = React.memo(function ActiveWorkTimer({ active }: { active: ActiveWork }) {
  const [clock, setClock] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setClock(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  return (
    <View accessibilityLabel={`${String(active.work.agentName ?? 'Scout')} is working`} style={styles.workTimer}>
      <View style={styles.workDot} />
      <Text style={styles.workTime}>{workElapsed(active.work.startedAt ?? active.message.createdAt, clock)}</Text>
    </View>
  );
});

function workElapsed(raw: unknown, now: number): string {
  const started = new Date(String(raw ?? '')).getTime();
  if (!Number.isFinite(started)) return 'live';
  const seconds = Math.max(0, Math.floor((now - started) / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${String(seconds % 60).padStart(2, '0')}s`;
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, '0')}m`;
}

export function ChannelList() {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const navigation = useNavigation<ChannelNav>();
  const [threads, setThreads] = useState<ScoutThread[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editingThreadID, setEditingThreadID] = useState<string | null>(null);
  const [titleDraft, setTitleDraft] = useState('');
  const [renameError, setRenameError] = useState<string | null>(null);
  const renameInFlightRef = useRef<string | null>(null);
  const longPressedThreadRef = useRef<string | null>(null);

  const load = useCallback(async () => {
    if (!sessionToken) return;
    setError(null);
    try {
      const response = await api.scoutThreads(sessionToken);
      setThreads(response.threads ?? []);
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Could not load threads.');
    } finally {
      setLoading(false);
    }
  }, [sessionToken]);

  useFocusEffect(
    useCallback(() => {
      void load();
    }, [load]),
  );

  useEffect(() => {
    if (office.event === 'chat_thread') void load();
  }, [load, office.event, office.version]);

  const beginRename = useCallback((thread: ScoutThread) => {
    if (thread.visibility === 'public') return;
    longPressedThreadRef.current = String(thread.id);
    setRenameError(null);
    setEditingThreadID(String(thread.id));
    setTitleDraft(String(thread.title || thread.preview || 'Thread').trim());
    void Haptics.selectionAsync();
  }, []);

  const commitRename = useCallback(async (thread: ScoutThread) => {
    const threadID = String(thread.id);
    if (!sessionToken || editingThreadID !== threadID || renameInFlightRef.current === threadID) return;
    const title = titleDraft.replace(/\s+/g, ' ').trim();
    if (!title) {
      setRenameError('A thread name cannot be empty.');
      return;
    }
    if (title === String(thread.title || '').trim()) {
      setEditingThreadID(null);
      setTitleDraft('');
      return;
    }

    renameInFlightRef.current = threadID;
    setRenameError(null);
    try {
      const response = await api.updateScoutThread(sessionToken, threadID, { title });
      setThreads((current) => current.map((candidate) => (
        String(candidate.id) === threadID
          ? { ...candidate, ...(response.thread ?? {}), title }
          : candidate
      )));
      setEditingThreadID(null);
      setTitleDraft('');
      Keyboard.dismiss();
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (err) {
      setRenameError(err instanceof BonfireApiError ? err.message : 'Could not rename that thread.');
    } finally {
      if (renameInFlightRef.current === threadID) renameInFlightRef.current = null;
    }
  }, [editingThreadID, sessionToken, titleDraft]);

  if (loading) {
    return <ActivityIndicator color={colors.accent} style={styles.loading} />;
  }
  if (error) {
    return (
      <Pressable
        accessibilityRole="button"
        onPress={() => void load()} style={styles.errorBox}>
        <Text style={styles.error}>{error}</Text>
        <Text style={styles.retry}>Tap to retry</Text>
      </Pressable>
    );
  }
  if (!threads.length) {
    return <Text style={styles.empty}>No threads yet. Hold the mic and say something.</Text>;
  }

  const orderedThreads = pinBonfireChatFirst(threads);
  const sections = [
    { label: 'CHANNELS', threads: orderedThreads.filter((thread) => thread.visibility === 'public') },
    { label: 'PRIVATE', threads: orderedThreads.filter((thread) => thread.visibility !== 'public') },
  ].filter((section) => section.threads.length > 0);

  return (
    <View>
      {renameError ? <Text accessibilityRole="alert" style={styles.renameError}>{renameError}</Text> : null}
      {sections.map((section) => (
        <View key={section.label} style={styles.section}>
          <Text accessibilityRole="header" style={styles.sectionLabel}>{section.label}</Text>
          {section.threads.map((thread) => {
            const body = preview(thread);
            const unread = Math.max(0, Number(thread.unreadCount ?? 0));
            const threadID = String(thread.id);
            const editing = editingThreadID === threadID;
            const working = activeWork(thread);
            const bonfire = isBonfireChat(thread);
            return (
              <Pressable
                key={threadID}
                accessibilityRole="button"
                accessibilityLabel={`${channelDisplayName(thread)}${bonfire ? ', pinned channel' : ''}`}
                accessibilityHint={thread.visibility === 'public' ? undefined : 'Touch and hold to rename this thread'}
                onLongPress={thread.visibility === 'public' ? undefined : () => beginRename(thread)}
                onPress={() => {
                  if (longPressedThreadRef.current === threadID) {
                    longPressedThreadRef.current = null;
                    return;
                  }
                  if (editing) {
                    Keyboard.dismiss();
                    return;
                  }
                  navigation.navigate('Thread', {
                    threadId: threadID,
                    title: channelDisplayName(thread),
                  });
                }}
                style={({ pressed }) => [styles.row, pressed && styles.pressed]}
              >
                <View style={styles.rowText}>
                  {editing ? (
                    <TextInput
                      accessibilityLabel="Edit thread name"
                      autoFocus
                      editable={renameInFlightRef.current !== threadID}
                      enterKeyHint="done"
                      onBlur={() => { void commitRename(thread); }}
                      onChangeText={setTitleDraft}
                      onSubmitEditing={() => { void commitRename(thread); }}
                      returnKeyType="done"
                      selectTextOnFocus
                      selectionColor={colors.info}
                      submitBehavior="blurAndSubmit"
                      style={styles.nameInput}
                      value={titleDraft}
                    />
                  ) : (
                    <View style={styles.nameRow}>
                      <Text style={[styles.name, bonfire && styles.nameBonfire]} numberOfLines={1}>
                        {channelDisplayName(thread)}
                      </Text>
                      {bonfire ? (
                        <View accessibilityLabel="Pinned channel" style={styles.bonfireTag}>
                          <SymbolView name="pin.fill" tintColor={colors.emberText} size={9} />
                        </View>
                      ) : null}
                    </View>
                  )}
                  {body ? (
                    <Text style={styles.preview} numberOfLines={1}>
                      {body}
                    </Text>
                  ) : null}
                </View>
                <View style={styles.meta}>
                  {working ? <ActiveWorkTimer active={working} /> : <Text style={styles.time}>{timeAgo(thread.updatedAt)}</Text>}
                  {unread > 0 ? (
                    <View style={styles.unreadBadge}>
                      <Text style={styles.unreadText}>{unread > 99 ? '99+' : unread}</Text>
                    </View>
                  ) : null}
                </View>
              </Pressable>
            );
          })}
        </View>
      ))}
    </View>
  );
}

const styles = StyleSheet.create({
  loading: { paddingVertical: space[8] },
  section: { gap: 1 },
  sectionLabel: {
    ...type.label,
    color: colors.text3,
    paddingHorizontal: space[4],
    paddingTop: space[4],
    paddingBottom: space[1],
    letterSpacing: 0.8,
  },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    paddingVertical: space[3],
    paddingHorizontal: space[4],
    borderRadius: radius.md,
  },
  pressed: { backgroundColor: colors.accentSoft },
  rowText: { flex: 1, gap: 2 },
  nameRow: { minWidth: 0, flexDirection: 'row', alignItems: 'center', gap: 7 },
  name: {
    ...type.bodyMedium,
    flexShrink: 1,
    color: colors.text1,
  },
  nameBonfire: { color: colors.emberText },
  bonfireTag: { width: 20, height: 20, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.emberSoft },
  nameInput: {
    ...type.bodyMedium,
    minHeight: 34,
    paddingHorizontal: space[2],
    paddingVertical: 4,
    borderRadius: radius.sm,
    borderCurve: 'continuous',
    color: colors.text1,
    backgroundColor: colors.surface1,
  },
  preview: {
    ...type.caption,
    color: colors.text2,
  },
	time: {
    ...type.label,
    color: colors.text3,
  },
	workTimer: {
    minHeight: 24,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 5,
    paddingHorizontal: 7,
    borderRadius: radius.full,
    backgroundColor: colors.emberSoft,
  },
  workDot: { width: 6, height: 6, borderRadius: 3, backgroundColor: colors.ember },
  workTime: { ...type.label, color: colors.text2, fontVariant: ['tabular-nums'] },
	meta: { alignItems: 'flex-end', gap: space[1] },
	unreadBadge: {
		minWidth: 20,
		height: 20,
		paddingHorizontal: 6,
		borderRadius: radius.full,
		backgroundColor: colors.ember,
		alignItems: 'center',
		justifyContent: 'center',
	},
	unreadText: { ...type.label, color: colors.onAccent, fontSize: 10, lineHeight: 12 },
  empty: {
    ...type.bodySm,
    color: colors.text2,
    paddingHorizontal: space[4],
    paddingVertical: space[6],
    textAlign: 'center',
  },
  errorBox: { padding: space[4], gap: space[2] },
  error: { ...type.bodySm, color: colors.danger },
  retry: { ...type.button, color: colors.ember },
  renameError: { ...type.caption, color: colors.danger, paddingHorizontal: space[4], paddingBottom: space[2] },
});
