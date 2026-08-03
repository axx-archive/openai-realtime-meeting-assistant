import React, { useCallback, useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Keyboard, Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import * as Haptics from 'expo-haptics';
import { useFocusEffect, useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type { ScoutThread } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

/**
 * The Threads segment — design §14.
 *
 * Public threads ARE channels server-side: `#`-prefixed, broadcast-notifying,
 * @-mention-parsing. The old client rendered them as static cards above an "Ask"
 * textarea, which reads as a search tool. These are channel rows.
 */

type ChannelNav = NativeStackNavigationProp<RootStackParamList>;

function channelName(thread: ScoutThread): string {
  const title = String(thread.title || thread.preview || 'Thread').trim();
  return thread.visibility === 'public' ? `#${title.replace(/^#/, '')}` : title;
}

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

  return (
    <View>
      {renameError ? <Text accessibilityRole="alert" style={styles.renameError}>{renameError}</Text> : null}
      {threads.map((thread) => {
        const body = preview(thread);
		const unread = Math.max(0, Number(thread.unreadCount ?? 0));
        const threadID = String(thread.id);
        const editing = editingThreadID === threadID;
        return (
          <Pressable
            key={threadID}
            accessibilityRole="button"
            accessibilityLabel={channelName(thread)}
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
                title: channelName(thread),
              })
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
                <Text style={styles.name} numberOfLines={1}>
                  {channelName(thread)}
                </Text>
              )}
              {body ? (
                <Text style={styles.preview} numberOfLines={1}>
                  {body}
                </Text>
              ) : null}
            </View>
			<View style={styles.meta}>
				<Text style={styles.time}>{timeAgo(thread.updatedAt)}</Text>
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
  );
}

const styles = StyleSheet.create({
  loading: { paddingVertical: space[8] },
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
  name: {
    ...type.bodyMedium,
    color: colors.text1,
  },
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
