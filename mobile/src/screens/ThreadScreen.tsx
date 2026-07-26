import React, { useCallback, useEffect, useState } from 'react';
import {
  ActionSheetIOS,
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import * as Haptics from 'expo-haptics';
import * as DocumentPicker from 'expo-document-picker';
import { SymbolView } from 'expo-symbols';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type { ScoutFileAttachment, ScoutMessage } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { FilePreviewModal } from '../components/FilePreviewModal';
import { Screen } from '../components/Screen';
import { isInlinePreviewable, shareOrSaveRemoteFile } from '../files/fileActions';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import type { RootStackParamList } from '../navigation/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';

type Props = NativeStackScreenProps<RootStackParamList, 'Thread'>;

export function ThreadScreen({ route, navigation }: Props) {
  const { sessionToken, user } = useAuth();
  const office = useOfficeEvents();
  const [messages, setMessages] = useState<ScoutMessage[]>([]);
  const [title, setTitle] = useState(route.params.title);
  const [draft, setDraft] = useState('');
  const [attachment, setAttachment] = useState<ScoutFileAttachment & { uri: string } | null>(null);
  const [loading, setLoading] = useState(true);
  const [sending, setSending] = useState(false);
  const [fileAction, setFileAction] = useState<string | null>(null);
  const [preview, setPreview] = useState<ScoutFileAttachment | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!sessionToken) return;
    setError(null);
    try {
      const response = await api.scoutThread(sessionToken, route.params.threadId);
      setMessages(response.thread?.messages ?? response.messages ?? []);
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Could not load this thread.');
    } finally {
      setLoading(false);
    }
  }, [route.params.threadId, sessionToken]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (office.event === 'chat_thread') void load();
  }, [load, office.event, office.version]);

  async function send() {
    const text = draft.trim();
    if (!sessionToken || (!text && !attachment) || sending) return;
    setSending(true);
    setError(null);
    try {
      const uploaded = attachment
        ? await api.uploadScoutAttachment(sessionToken, attachment)
        : null;
      const response = await api.sendScoutMessage(
        sessionToken,
        route.params.threadId,
        text || `Shared ${attachment?.name}`,
        uploaded ? [uploaded] : [],
      );
      setDraft('');
      setAttachment(null);
      setMessages(response.thread?.messages ?? response.messages ?? []);
      await Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
      if (!(response.thread?.messages ?? response.messages)?.length) await load();
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Message did not send.');
    } finally {
      setSending(false);
    }
  }

  async function chooseAttachment() {
    const result = await DocumentPicker.getDocumentAsync({
      type: ['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'application/pdf'],
      copyToCacheDirectory: true,
      multiple: false,
    });
    if (result.canceled) return;
    const file = result.assets[0];
    if (!file) return;
    setAttachment({
      uri: file.uri,
      name: file.name,
      ref: '',
      mime: file.mimeType || 'application/octet-stream',
      size: file.size,
    });
  }

  function showThreadActions() {
    ActionSheetIOS.showActionSheetWithOptions(
      {
        options: ['Cancel', 'Rename thread', 'Archive thread'],
        cancelButtonIndex: 0,
        destructiveButtonIndex: 2,
        title: title,
      },
      (index) => {
        if (index === 1) {
          Alert.prompt('Rename thread', undefined, async (value) => {
            const next = value.trim();
            if (!sessionToken || !next) return;
            try {
              await api.updateScoutThread(sessionToken, route.params.threadId, { title: next });
              setTitle(next);
            } catch (err) {
              setError(err instanceof BonfireApiError ? err.message : 'Could not rename this thread.');
            }
          }, 'plain-text', title);
        }
        if (index === 2) {
          Alert.alert('Archive this thread?', 'It stays available from the web archive.', [
            { text: 'Cancel', style: 'cancel' },
            {
              text: 'Archive',
              style: 'destructive',
              onPress: () => {
                if (!sessionToken) return;
                void api.updateScoutThread(sessionToken, route.params.threadId, { archived: true })
                  .then(() => navigation.goBack())
                  .catch((err) => setError(err instanceof BonfireApiError ? err.message : 'Could not archive this thread.'));
              },
            },
          ]);
        }
      },
    );
  }

  function confirmDeleteMessage(message: ScoutMessage) {
    if (!sessionToken || !message.id) return;
    Alert.alert('Delete this message?', 'This removes your message from the shared thread.', [
      { text: 'Cancel', style: 'cancel' },
      {
        text: 'Delete',
        style: 'destructive',
        onPress: () => {
          void api.deleteScoutMessage(sessionToken, route.params.threadId, message.id)
            .then((response) => setMessages(response.thread?.messages ?? response.messages ?? []))
            .catch((err) => setError(err instanceof BonfireApiError ? err.message : 'Could not delete the message.'));
        },
      },
    ]);
  }

  function openFile(file: ScoutFileAttachment) {
    if (!sessionToken || fileAction) return;
    if (!file.ref) {
      setError('This older attachment does not include downloadable file data.');
      return;
    }
    if (isInlinePreviewable(file)) {
      setPreview(file);
      return;
    }
    void shareFile(file);
  }

  async function shareFile(file: ScoutFileAttachment) {
    if (!sessionToken || fileAction || !file.ref) return;
    const key = file.ref || file.name;
    setFileAction(key);
    setError(null);
    try {
      await shareOrSaveRemoteFile(sessionToken, file);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not share the attachment.');
    } finally {
      setFileAction(null);
    }
  }

  return (
    <KeyboardAvoidingView style={styles.flex} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
      <Screen
        title={title}
        subtitle="Scout thread · shared with the web"
        loading={loading}
        error={error}
        onRetry={() => void load()}
        right={
          <Pressable accessibilityRole="button" accessibilityLabel="Thread actions" onPress={showThreadActions} style={styles.iconButton}>
            <SymbolView name="ellipsis" tintColor={colors.text1} size={20} />
          </Pressable>
        }
      >
        {messages.map((message, index) => {
          const mine = message.role === 'user' || message.authorEmail === user?.email;
          const text = message.text || String(message.content ?? '');
          return (
            <Pressable
              key={message.id || `${message.createdAt}-${index}`}
              onLongPress={mine ? () => confirmDeleteMessage(message) : undefined}
              style={[styles.message, mine ? styles.mine : styles.scout]}
            >
              <Text style={[styles.messageLabel, mine && styles.mineText]}>{mine ? 'You' : message.authorName || 'Scout'}</Text>
              <Text style={[styles.messageText, mine && styles.mineText]}>{text}</Text>
              {message.files?.map((file) => {
                const key = file.ref || file.name;
                const unavailable = !file.ref;
                return (
                  <View
                    key={key}
                    style={[styles.fileChip, mine ? styles.mineFileChip : styles.scoutFileChip]}
                  >
                    <Pressable
                      accessibilityRole="button"
                      accessibilityLabel={`Open ${file.name}`}
                      accessibilityHint={unavailable ? 'File data is unavailable' : 'Previews or opens the attachment'}
                      accessibilityState={{ disabled: unavailable || Boolean(fileAction) }}
                      disabled={unavailable || Boolean(fileAction)}
                      onPress={() => openFile(file)}
                      style={({ pressed }) => [styles.fileOpen, pressed && styles.filePressed]}
                    >
                      <SymbolView
                        name={file.mime === 'application/pdf' ? 'doc.richtext.fill' : 'photo.fill'}
                        tintColor={mine ? colors.onAccent : colors.text2}
                        size={16}
                      />
                      <Text
                        numberOfLines={1}
                        style={[styles.fileLabel, mine && styles.mineText]}
                      >
                        {file.name}
                      </Text>
                    </Pressable>
                    <Pressable
                      accessibilityRole="button"
                      accessibilityLabel={`Share or save ${file.name}`}
                      accessibilityHint="Downloads securely and opens the system share sheet"
                      accessibilityState={{ disabled: unavailable || Boolean(fileAction) }}
                      disabled={unavailable || Boolean(fileAction)}
                      hitSlop={4}
                      onPress={() => void shareFile(file)}
                      style={({ pressed }) => [styles.fileShare, pressed && styles.filePressed]}
                    >
                      {fileAction === key ? (
                        <ActivityIndicator size="small" color={mine ? colors.onAccent : colors.text2} />
                      ) : (
                        <SymbolView
                          name="square.and.arrow.up"
                          tintColor={mine ? colors.onAccent : colors.text2}
                          size={16}
                        />
                      )}
                    </Pressable>
                  </View>
                );
              })}
            </Pressable>
          );
        })}
        {!messages.length && !loading ? <Text style={styles.empty}>Start the conversation below.</Text> : null}
        <View style={styles.composer}>
          {attachment ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={`Remove attachment ${attachment.name}`}
              accessibilityHint="Removes this file from the message."
              accessibilityState={{ selected: true }}
              onPress={() => setAttachment(null)}
              style={styles.attachmentChip}
            >
              <Text numberOfLines={1} style={styles.attachmentText}>📎 {attachment.name}</Text>
              <SymbolView name="xmark.circle.fill" tintColor={colors.text2} size={17} />
            </Pressable>
          ) : null}
          <TextInput
            accessibilityLabel="Message Scout"
            accessibilityHint="Enter the message you want to send."
            value={draft}
            onChangeText={setDraft}
            placeholder="Message Scout…"
            placeholderTextColor={colors.text3}
            multiline
            style={styles.input}
          />
          <View style={styles.composerActions}>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={attachment ? 'Replace attached file' : 'Attach a file'}
              accessibilityHint={attachment ? `Currently attached: ${attachment.name}. Opens the file picker.` : 'Opens the file picker.'}
              accessibilityState={{ disabled: sending, selected: Boolean(attachment) }}
              onPress={() => void chooseAttachment()}
              disabled={sending}
              style={styles.attachButton}
            >
              <SymbolView name="paperclip" tintColor={colors.text1} size={20} />
            </Pressable>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Send message"
              accessibilityHint="Sends this message to Scout."
              accessibilityState={{ disabled: (!draft.trim() && !attachment) || sending, busy: sending }}
              onPress={send}
              disabled={(!draft.trim() && !attachment) || sending}
              style={({ pressed }) => [styles.send, pressed && styles.pressed, ((!draft.trim() && !attachment) || sending) && styles.disabled]}
            >
              {sending ? <ActivityIndicator color={colors.onAccent} /> : <Text style={styles.sendText}>Send</Text>}
            </Pressable>
          </View>
        </View>
        <FilePreviewModal
          file={preview}
          sessionToken={sessionToken ?? ''}
          onClose={() => setPreview(null)}
        />
      </Screen>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  flex: { flex: 1, backgroundColor: colors.bgApp },
  message: { maxWidth: '88%', paddingHorizontal: space[4], paddingVertical: space[3], borderRadius: radius.lg, marginBottom: space[3] },
  scout: { alignSelf: 'flex-start', backgroundColor: colors.surface1, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  mine: { alignSelf: 'flex-end', backgroundColor: colors.accent },
  messageLabel: { ...type.label, color: colors.ember, marginBottom: 5, textTransform: 'uppercase' },
  messageText: { ...type.body, color: colors.text1 },
  fileChip: { minHeight: hitMin, flexDirection: 'row', alignItems: 'stretch', borderRadius: radius.md, marginTop: space[2], overflow: 'hidden' },
  scoutFileChip: { backgroundColor: colors.surface3 },
  mineFileChip: { backgroundColor: 'rgba(255,255,255,0.16)' },
  fileOpen: { minHeight: hitMin, flex: 1, flexDirection: 'row', alignItems: 'center', gap: space[2], paddingLeft: space[3] },
  fileLabel: { ...type.captionMedium, flex: 1, color: colors.text2 },
  fileShare: { width: hitMin, minHeight: hitMin, alignItems: 'center', justifyContent: 'center' },
  filePressed: { opacity: 0.7 },
  mineText: { color: colors.onAccent },
  empty: { ...type.bodySm, color: colors.text2 },
  composer: { marginTop: space[4], backgroundColor: colors.surface1, borderRadius: radius.xl, padding: space[3], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  input: { minHeight: 72, color: colors.text1, fontSize: 15, textAlignVertical: 'top' },
  composerActions: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', marginTop: space[2] },
  attachButton: { width: hitMin, height: hitMin, borderRadius: radius.md, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.surface3 },
  attachmentChip: { minHeight: hitMin, flexDirection: 'row', alignItems: 'center', gap: 8, paddingHorizontal: space[3], borderRadius: radius.md, backgroundColor: colors.surface3, marginBottom: space[2] },
  attachmentText: { ...type.caption, flex: 1, color: colors.text1 },
  send: { alignSelf: 'flex-end', minWidth: 74, minHeight: hitMin, borderRadius: radius.md, backgroundColor: colors.accent, alignItems: 'center', justifyContent: 'center' },
  sendText: { ...type.button, color: colors.onAccent },
  pressed: { transform: [{ scale: 0.96 }] },
  disabled: { opacity: 0.45 },
  iconButton: { width: hitMin, height: hitMin, borderRadius: radius.md, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.surface1 },
});
