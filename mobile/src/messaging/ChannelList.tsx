import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Keyboard, Pressable, StyleSheet, Text, TextInput, useWindowDimensions, View } from 'react-native';
import { FlashList } from '@shopify/flash-list';
import * as Haptics from 'expo-haptics';
import { useFocusEffect, useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type { ScoutThread } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import type { RootStackParamList } from '../navigation/types';
import { SymbolView } from 'expo-symbols';
import { colors, radius, space, type } from '../theme/tokens';
import { channelDisplayName, isBonfireChat } from './channelPresentation';
import { THREAD_LARGE_TEXT_FONT_SCALE } from './threadWorkspaceLayout';
import {
  channelActiveWork,
  channelListRows,
  channelThreadAccessibilityLabel,
  type ChannelActiveWork,
} from './channelListPerformance';

/**
 * The Threads segment — design §14.
 *
 * Public threads ARE channels server-side: `#`-prefixed, broadcast-notifying,
 * @-mention-parsing. The old client rendered them as static cards above an "Ask"
 * textarea, which reads as a search tool. These are channel rows.
 */

type ChannelNav = NativeStackNavigationProp<RootStackParamList>;

type ChannelListProps = {
  onOpenThread?: (thread: ScoutThread) => void;
  selectedThreadId?: string;
};

/* channel-terminal-preview-contract:start */
function terminalPreviewHasActiveWork(thread: ScoutThread): boolean {
  const activeStatuses = new Set(['queued', 'running', 'approval_required', 'needs_input', 'parked']);
  const messages = Array.isArray(thread.messages) ? thread.messages : thread.activeWork ? [thread.activeWork] : [];
  return messages.some((message) => (
    Boolean(message.thread)
      && activeStatuses.has(String(message.thread?.status ?? '').toLowerCase())
  ));
}

function ordinaryPreview(thread: ScoutThread): string {
  const last = thread.lastMessage?.text || thread.preview || '';
  return String(last).replace(/\s+/g, ' ').trim();
}

function chatIndexMetadata(thread: ScoutThread): Partial<ScoutThread> {
  return {
    id: thread.id,
    title: thread.title,
    visibility: thread.visibility,
    ownerEmail: thread.ownerEmail,
    memberEmails: thread.memberEmails,
    updatedAt: thread.updatedAt,
    createdAt: thread.createdAt,
    preview: thread.preview,
    table: thread.table,
    archived: thread.archived,
    activeWork: thread.activeWork,
  };
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

function boundedResearchSourceSummary(raw: unknown): string | null {
  const value = String(raw ?? '').replace(/\s+/g, ' ').trim();
  const match = /^Research delivered · ([1-9][0-9]{0,4}) cited source (link|links) · ([1-9][0-9]{0,4}) (domain|domains)$/u.exec(value);
  if (!match) return null;
  const citations = Number(match[1]);
  const domains = Number(match[3]);
  if (!Number.isInteger(citations) || citations < 1 || citations > 10_000 || !Number.isInteger(domains) || domains < 1 || domains > 10_000) return null;
  if ((citations === 1) !== (match[2] === 'link') || (domains === 1) !== (match[4] === 'domain')) return null;
  return value;
}

export function channelTerminalPreview(thread: ScoutThread): string {
  const fallback = ordinaryPreview(thread);
  // Concurrent work is real state. A completed historical card must never
  // replace the timer/copy for any work item the server still marks active.
  if (terminalPreviewHasActiveWork(thread)) return 'Scout is working';

  const messages = Array.isArray(thread.messages) ? thread.messages : [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    const work = message?.thread;
    if (!work) continue;
    const status = String(work.status ?? '').trim().toLowerCase();
    const exactBinding = String(message.kind ?? '').trim().toLowerCase() === 'thread'
      && Boolean(String(work.id ?? '').trim() && String(work.artifactId ?? '').trim());
    const research = String(work.mode ?? '').trim().toLowerCase() === 'research';
    if (!exactBinding || !research) return fallback;
    if (status === 'error') return 'Needs attention';
    if (status !== 'complete') return fallback;

    // The list response's preview is the server-projected, body-minimized
    // current artifact postimage. Source provenance is optional and is shown
    // only when that closed copy is exact; message/report bodies are never
    // parsed for counts. A stale launch/lastMessage preview cannot win.
    const sourceSummary = boundedResearchSourceSummary(thread.preview);
    const terminalCardCopy = String(message.text ?? '').replace(/\s+/g, ' ').trim();
    return sourceSummary && terminalCardCopy === sourceSummary ? sourceSummary : 'Research delivered';
  }
  return fallback;
}
/* channel-terminal-preview-contract:end */

const ActiveWorkTimer = React.memo(function ActiveWorkTimer({ active, clock }: { active: ChannelActiveWork; clock: number }) {
  return (
    <View accessible={false} style={styles.workTimer}>
      <View style={styles.workDot} />
      <Text maxFontSizeMultiplier={2} style={styles.workTime}>{workElapsed(active.work.startedAt ?? active.message.createdAt, clock)}</Text>
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

export function ChannelList({ onOpenThread, selectedThreadId }: ChannelListProps = {}) {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const navigation = useNavigation<ChannelNav>();
  const [threads, setThreads] = useState<ScoutThread[]>([]);
  const [threadsSessionToken, setThreadsSessionToken] = useState<string | null>(null);
  const [attemptedSessionToken, setAttemptedSessionToken] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editingThreadID, setEditingThreadID] = useState<string | null>(null);
  const [titleDraft, setTitleDraft] = useState('');
  const [renameError, setRenameError] = useState<string | null>(null);
  const [clock, setClock] = useState(() => Date.now());
  const renameInFlightRef = useRef<string | null>(null);
  const longPressedThreadRef = useRef<string | null>(null);
  const loadRequestRef = useRef<Promise<void> | null>(null);
  const loadQueuedRef = useRef(false);
  const loadGenerationRef = useRef(0);
  const sessionTokenRef = useRef(sessionToken);
  const threadsRef = useRef(threads);
  sessionTokenRef.current = sessionToken;
  threadsRef.current = threads;
  const { fontScale } = useWindowDimensions();
  const largeText = fontScale >= THREAD_LARGE_TEXT_FONT_SCALE;
  const scopedThreads = threadsSessionToken === sessionToken ? threads : [];
  const scopedEditingThreadID = threadsSessionToken === sessionToken ? editingThreadID : null;
  const rows = useMemo(() => channelListRows(scopedThreads), [scopedThreads]);
  const hasActiveWork = useMemo(() => scopedThreads.some((thread) => Boolean(channelActiveWork(thread))), [scopedThreads]);

  const load = useCallback(async () => {
    if (!sessionToken) return;
    if (loadRequestRef.current) {
      loadQueuedRef.current = true;
      return loadRequestRef.current;
    }
    const generation = loadGenerationRef.current;
    const token = sessionToken;
    setAttemptedSessionToken(token);
    setError(null);
    let request!: Promise<void>;
    request = (async () => {
      try {
        const response = await api.scoutThreadIndex(token);
        if (generation !== loadGenerationRef.current || token !== sessionTokenRef.current) return;
        setThreads(response.threads ?? []);
        setThreadsSessionToken(token);
      } catch (err) {
        if (generation !== loadGenerationRef.current || token !== sessionTokenRef.current) return;
        if (threadsRef.current.length === 0) setError(err instanceof BonfireApiError ? err.message : 'Could not load threads.');
      } finally {
        if (generation === loadGenerationRef.current && token === sessionTokenRef.current) setLoading(false);
        if (loadRequestRef.current === request) loadRequestRef.current = null;
        if (loadQueuedRef.current && generation === loadGenerationRef.current && token === sessionTokenRef.current) {
          loadQueuedRef.current = false;
          setTimeout(() => {
            if (generation === loadGenerationRef.current && token === sessionTokenRef.current) void load();
          }, 0);
        }
      }
    })();
    loadRequestRef.current = request;
    return request;
  }, [sessionToken]);

  useEffect(() => {
    loadGenerationRef.current += 1;
    loadRequestRef.current = null;
    loadQueuedRef.current = false;
    setThreads([]);
    setThreadsSessionToken(null);
    setAttemptedSessionToken(null);
    setError(null);
    setLoading(Boolean(sessionToken));
	setEditingThreadID(null);
	setTitleDraft('');
	setRenameError(null);
	renameInFlightRef.current = null;
	longPressedThreadRef.current = null;
  }, [sessionToken]);

  useFocusEffect(
    useCallback(() => {
      void load();
    }, [load]),
  );

  useEffect(() => {
    if (office.event === 'chat_thread') void load();
  }, [load, office.event, office.version]);

  // One shared clock updates only the virtualized visible rows. A timer per
  // active thread scales linearly with the company and wakes offscreen work.
  useEffect(() => {
    if (!hasActiveWork) return undefined;
    const timer = setInterval(() => setClock(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [hasActiveWork]);

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
    const generation = loadGenerationRef.current;
    const token = sessionToken;
    try {
      const response = await api.updateScoutThread(sessionToken, threadID, { title });
      if (generation !== loadGenerationRef.current || token !== sessionTokenRef.current) return;
      setThreads((current) => current.map((candidate) => (
        String(candidate.id) === threadID
          ? { ...candidate, ...chatIndexMetadata(response.thread ?? { id: threadID }), title, messages: undefined }
          : candidate
      )));
      setEditingThreadID(null);
      setTitleDraft('');
      Keyboard.dismiss();
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (err) {
      if (generation !== loadGenerationRef.current || token !== sessionTokenRef.current) return;
      setRenameError(err instanceof BonfireApiError ? err.message : 'Could not rename that thread.');
    } finally {
      if (renameInFlightRef.current === threadID) renameInFlightRef.current = null;
    }
  }, [editingThreadID, sessionToken, titleDraft]);

  if (loading || (threadsSessionToken !== sessionToken && attemptedSessionToken !== sessionToken)) {
    return (
      <View accessibilityLabel="Loading channels and private chats" accessibilityRole="progressbar" style={styles.loadingSkeleton}>
        {Array.from({ length: 6 }, (_, index) => (
          <View key={`thread-loading-${index}`} style={styles.loadingRow}>
            <View style={styles.loadingIcon} />
            <View style={styles.loadingCopy}>
              <View style={[styles.loadingLine, index % 2 === 0 ? styles.loadingLineShort : null]} />
              <View style={[styles.loadingLine, styles.loadingLineMuted]} />
            </View>
          </View>
        ))}
      </View>
    );
  }
  if (error && !scopedThreads.length) {
    return (
      <Pressable
        accessibilityRole="button"
        onPress={() => void load()} style={styles.errorBox}>
        <Text style={styles.error}>{error}</Text>
        <Text style={styles.retry}>Tap to retry</Text>
      </Pressable>
    );
  }
  if (!scopedThreads.length) {
    return <Text style={styles.empty}>No threads yet. Hold the mic and say something.</Text>;
  }

  return (
    <View style={styles.listRoot}>
      {renameError ? <Text accessibilityRole="alert" style={styles.renameError}>{renameError}</Text> : null}
      <FlashList
        data={rows}
        extraData={{ clock, editingThreadID: scopedEditingThreadID, largeText, selectedThreadId, titleDraft }}
        keyExtractor={(row) => row.id}
        getItemType={(row) => row.kind}
        maxItemsInRecyclePool={32}
        keyboardDismissMode="on-drag"
        keyboardShouldPersistTaps="handled"
        contentContainerStyle={styles.listContent}
        renderItem={({ item }) => {
          if (item.kind === 'section') {
            return <Text accessibilityRole="header" maxFontSizeMultiplier={2} style={styles.sectionLabel}>{item.label}</Text>;
          }
          const thread = item.thread;
            const body = channelTerminalPreview(thread);
            const unread = Math.max(0, Number(thread.unreadCount ?? 0));
            const threadID = String(thread.id);
            const editing = scopedEditingThreadID === threadID;
            const working = channelActiveWork(thread);
            const bonfire = isBonfireChat(thread);
            return (
              <Pressable
                key={threadID}
                accessibilityRole="button"
                accessibilityState={{ selected: selectedThreadId === threadID }}
                accessibilityLabel={channelThreadAccessibilityLabel(thread, working)}
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
                  if (onOpenThread) onOpenThread(thread);
                  else {
                    navigation.replace('Thread', {
                      threadId: threadID,
                      title: channelDisplayName(thread),
                    });
                  }
                }}
                style={({ pressed }) => [
                  styles.row,
                  selectedThreadId === threadID && styles.selected,
                  pressed && styles.pressed,
                ]}
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
                      <Text maxFontSizeMultiplier={2} style={[styles.name, bonfire && styles.nameBonfire]} numberOfLines={largeText ? 2 : 1}>
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
                    <Text maxFontSizeMultiplier={2} style={styles.preview} numberOfLines={largeText ? 2 : 1}>
                      {body}
                    </Text>
                  ) : null}
                </View>
                <View style={styles.meta}>
                  {working ? <ActiveWorkTimer active={working} clock={clock} /> : <Text maxFontSizeMultiplier={2} style={styles.time}>{timeAgo(thread.updatedAt)}</Text>}
                  {unread > 0 ? (
                    <View style={[styles.unreadBadge, largeText && styles.unreadBadgeLarge]}>
                      <Text maxFontSizeMultiplier={2} style={styles.unreadText}>{unread > 99 ? '99+' : unread}</Text>
                    </View>
                  ) : null}
                </View>
              </Pressable>
            );
        }}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  listRoot: { flex: 1, minHeight: 0 },
  listContent: { paddingBottom: space[8] },
  loadingSkeleton: { paddingHorizontal: space[2], paddingVertical: space[3], gap: space[2] },
  loadingRow: { minHeight: 58, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[3] },
  loadingIcon: { width: 30, height: 30, borderRadius: radius.md, backgroundColor: colors.surface2 },
  loadingCopy: { flex: 1, gap: 8 },
  loadingLine: { width: '82%', height: 8, borderRadius: radius.full, backgroundColor: colors.surface2 },
  loadingLineShort: { width: '56%' },
  loadingLineMuted: { opacity: 0.62 },
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
  selected: { backgroundColor: colors.accentSoft },
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
		minHeight: 20,
		paddingVertical: 2,
		paddingHorizontal: 6,
		borderRadius: radius.full,
		backgroundColor: colors.ember,
		alignItems: 'center',
		justifyContent: 'center',
	},
	unreadBadgeLarge: { minWidth: 28, minHeight: 28, paddingVertical: 4 },
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
