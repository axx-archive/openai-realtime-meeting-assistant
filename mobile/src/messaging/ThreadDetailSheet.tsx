import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  Dimensions,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SymbolView } from 'expo-symbols';
import { SafeAreaView } from 'react-native-safe-area-context';

import { api } from '../api/client';
import type { ChatMentionCandidate, HomeProjectChoice, ScoutFileAttachment, ScoutMessage, ScoutResultAssetRef } from '../api/types';
import { Glass } from '../theme/glass';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import { isOwnMessageForViewer } from './messagePresentation';
import { MessageBubble } from './MessageBubble';
import { MentionComposerInput } from './MentionComposerInput';
import { completeDocumentReference } from '../drive/driveModels';
import { rebindOpaqueProjectChoice } from './projectChoice';
import {
	explicitProjectAttachmentEnabled,
  safeProjectContextFromResponse,
  shouldRequestReplyThreadProjectContext,
} from './projectContextPreflight';

type Props = {
  visible: boolean;
  title: string;
  threadId: string;
  root: ScoutMessage | null;
  replies: readonly ScoutMessage[];
  viewerEmail: string;
  threadVisibility: string;
  threadOwnerEmail: string;
  sessionToken: string;
  participantAvatars: ReadonlyMap<string, string>;
  mentionCandidates: ChatMentionCandidate[];
  sending: boolean;
  uploading: boolean;
  error?: string;
  pendingFiles: readonly ScoutFileAttachment[];
  stagingFiles: ReadonlyArray<{ id: string; name: string; mime: string }>;
  onClose: () => void;
  onSend: (text: string, files: readonly ScoutFileAttachment[], projectContextToken: string) => Promise<boolean>;
  onAddAttachment: () => void;
  onBrowseDrive: (query?: string) => void;
  documentSelection?: { key: number; name: string } | null;
  onDocumentSelectionApplied: () => void;
  onRemoveAttachment: (file: ScoutFileAttachment) => void;
  onOpenAttachment: (file: ScoutFileAttachment) => void;
  onLongPress: (message: ScoutMessage, own: boolean, attachment?: { file: ScoutFileAttachment; index: number }) => void;
  onToggleReaction: (message: ScoutMessage, emoji: string, active: boolean) => void;
  onRetryReply: (message: ScoutMessage) => void;
  onResolveProposal: (message: ScoutMessage, action: 'accepted' | 'dismissed', objective: string) => void;
  proposalObjectives: Readonly<Record<string, string>>;
  onChangeProposalObjective: (message: ScoutMessage, objective: string) => void;
  resolvingProposalID?: string | null;
  onOpenLongMessage: (text: string, authorName: string, scout: boolean) => void;
  onOpenWorkArtifact: (message: ScoutMessage) => void;
  onOpenStudioProject: (projectId: string) => void;
  onOpenWorkAsset: (asset: ScoutResultAssetRef) => void;
  onResolveWorkCheckpoint: (message: ScoutMessage, option: { id: string; label: string; action: string }) => void;
  onChangeWorkProject: (message: ScoutMessage, returnFocusHandle?: number) => void;
  onSaveWorkArtifact: (message: ScoutMessage) => void;
  onOpenSavedWorkArtifact: (message: ScoutMessage) => void;
  onRegenerateWorkArtifact: (message: ScoutMessage) => void;
  onRetryGoal: (message: ScoutMessage) => void;
  savingWorkID?: string | null;
  regeneratingWorkID?: string | null;
  retryingGoalID?: string | null;
  savedWorkIDs: ReadonlySet<string>;
  workDriveSaveAvailability: 'checking' | 'available' | 'unavailable';
  actionOverlay?: React.ReactNode;
};

export function ThreadDetailSheet({
  visible,
  title,
  threadId,
  root,
  replies,
  viewerEmail,
  threadVisibility,
  threadOwnerEmail,
  sessionToken,
  participantAvatars,
  mentionCandidates,
  sending,
  uploading,
  error,
  pendingFiles,
  stagingFiles,
  onClose,
  onSend,
  onAddAttachment,
  onBrowseDrive,
  documentSelection,
  onDocumentSelectionApplied,
  onRemoveAttachment,
  onOpenAttachment,
  onLongPress,
  onToggleReaction,
  onRetryReply,
  onResolveProposal,
  proposalObjectives,
  onChangeProposalObjective,
  resolvingProposalID,
  onOpenLongMessage,
  onOpenWorkArtifact,
  onOpenStudioProject,
  onOpenWorkAsset,
  onResolveWorkCheckpoint,
  onChangeWorkProject,
  onSaveWorkArtifact,
  onOpenSavedWorkArtifact,
  onRegenerateWorkArtifact,
  onRetryGoal,
  savingWorkID,
  regeneratingWorkID,
  retryingGoalID,
  savedWorkIDs,
  workDriveSaveAvailability,
  actionOverlay,
}: Props) {
  const [draft, setDraft] = useState('');
	const [projectContext, setProjectContext] = useState<{ available: boolean; scopeKey: string; choices: HomeProjectChoice[] }>({ available: false, scopeKey: '', choices: [] });
	const [selectedProject, setSelectedProject] = useState<(HomeProjectChoice & { text: string; sourceKey: string }) | null>(null);
	const [projectExplicitNone, setProjectExplicitNone] = useState(false);
	const [projectChooserOpen, setProjectChooserOpen] = useState(false);
	const [projectStatus, setProjectStatus] = useState('');
	const projectGenerationRef = useRef(0);
  const scrollRef = useRef<ScrollView>(null);
  const sheetRef = useRef<View>(null);
  const timestampReveal = useRef(new Animated.Value(0)).current;
  const previousReplyCountRef = useRef(0);
  const initialScrollCompleteRef = useRef(false);
  const lastDocumentSelectionRef = useRef(0);
  const [keyboardOffset, setKeyboardOffset] = useState(8);

  const conversation = useMemo(() => root ? [root, ...replies] : [], [replies, root]);
	const attachmentHandles = useMemo(() => pendingFiles.map((file) => ({ sourceId: String(file.sourceId ?? '').trim(), sourceRevision: String(file.sourceRevision ?? '').trim() })), [pendingFiles]);
	const projectSourceKey = useMemo(() => JSON.stringify({ attachmentHandles, replyToMessageId: String(root?.id ?? '') }), [attachmentHandles, root?.id]);

  const measureSheetKeyboardOffset = useCallback(() => {
    requestAnimationFrame(() => {
      sheetRef.current?.measureInWindow((_x, y, _width, height) => {
        // iOS page sheets report their layout relative to the sheet while
        // keyboard coordinates remain screen-relative. Account for the
        // sheet's top edge so the composer clears the keyboard completely.
        const screenHeight = Dimensions.get('screen').height;
        const sheetTop = Math.max(0, y, screenHeight - height);
        const nextOffset = Math.max(8, Math.round(sheetTop) + 8);
        setKeyboardOffset((current) => current === nextOffset ? current : nextOffset);
      });
    });
  }, []);

  useEffect(() => {
    if (!visible) {
      setDraft('');
      previousReplyCountRef.current = 0;
      initialScrollCompleteRef.current = false;
      return;
    }
    initialScrollCompleteRef.current = false;
    previousReplyCountRef.current = replies.length;
  }, [root?.id, visible]);

	useEffect(() => {
		const generation = ++projectGenerationRef.current;
		if (!visible || !sessionToken || !threadId || !root?.id) {
			setProjectContext({ available: false, scopeKey: '', choices: [] });
			setSelectedProject(null);
			setProjectExplicitNone(false);
			setProjectChooserOpen(false);
			return;
		}
		// A closed reply sheet (and an ordinary unlinked reply) performs no Project
		// preflight. The optional accessory becomes live only after an explicit
		// chooser press or while refreshing an existing exact selection.
		if (!shouldRequestReplyThreadProjectContext({
			visible,
			sessionToken,
			threadId,
			rootMessageId: String(root.id),
			chooserOpen: projectChooserOpen,
			hasSelectedProject: Boolean(selectedProject),
		})) return;
		const timer = setTimeout(() => {
			void api.projectContext(sessionToken, {
				text: draft.trim(), destination: { route: 'thread', threadId }, attachmentHandles, replyToMessageId: String(root.id),
			}).then((response) => {
				if (generation !== projectGenerationRef.current) return;
				const next = safeProjectContextFromResponse(response);
				if (!next) throw new Error('Project context is unavailable.');
				setProjectStatus('');
				setProjectContext((current) => {
					if (current.scopeKey && next.scopeKey && current.scopeKey !== next.scopeKey) {
						setSelectedProject(null);
						setProjectExplicitNone(false);
					}
					return { available: next.available, scopeKey: next.scopeKey ?? '', choices: next.choices ?? [] };
				});
				setSelectedProject((current) => {
					const refreshed = rebindOpaqueProjectChoice(current, next.suggested, next.choices, projectExplicitNone);
					return refreshed ? { ...refreshed, text: draft.trim(), sourceKey: projectSourceKey } : null;
				});
			}).catch(() => {
				if (generation !== projectGenerationRef.current) return;
				setProjectContext({ available: false, scopeKey: '', choices: [] });
				setSelectedProject(null);
				setProjectExplicitNone(false);
				setProjectChooserOpen(false);
				setProjectStatus('Project context is unavailable for these sources.');
			});
		}, 220);
		return () => clearTimeout(timer);
	}, [attachmentHandles, draft, projectChooserOpen, projectExplicitNone, projectSourceKey, root?.id, sessionToken, threadId, visible]);

  useEffect(() => {
    if (!visible) return;
    if (replies.length > previousReplyCountRef.current) {
      requestAnimationFrame(() => scrollRef.current?.scrollToEnd({ animated: true }));
    }
    previousReplyCountRef.current = replies.length;
  }, [replies.length, visible]);

  useEffect(() => {
    if (!visible || !documentSelection || lastDocumentSelectionRef.current === documentSelection.key) return;
    lastDocumentSelectionRef.current = documentSelection.key;
    setDraft((current) => completeDocumentReference(current, documentSelection.name));
    onDocumentSelectionApplied();
  }, [documentSelection, onDocumentSelectionApplied, visible]);

  const submit = async () => {
    const text = draft.trim();
    if ((!text && pendingFiles.length === 0) || sending || uploading) return;
	const projectContextToken = explicitProjectAttachmentEnabled && selectedProject?.text === text && selectedProject.sourceKey === projectSourceKey ? selectedProject.token : '';
	if (explicitProjectAttachmentEnabled && selectedProject?.token && !projectContextToken) {
	  setProjectStatus('Project context is refreshing for these sources. Try Send again in a moment.');
	  return;
	}
    if (await onSend(text, pendingFiles, projectContextToken)) {
      setDraft('');
	  setProjectStatus('');
	  setSelectedProject(null);
	  setProjectExplicitNone(false);
	  setProjectChooserOpen(false);
    }
  };

  return (
    <Modal
      visible={visible}
      animationType="slide"
      presentationStyle="pageSheet"
      onShow={measureSheetKeyboardOffset}
      onRequestClose={onClose}
    >
      <SafeAreaView
        ref={sheetRef}
        style={styles.safe}
        edges={['left', 'right', 'bottom']}
        onLayout={measureSheetKeyboardOffset}
      >
        <KeyboardAvoidingView
          style={styles.fill}
          behavior={Platform.OS === 'ios' ? 'padding' : undefined}
          keyboardVerticalOffset={keyboardOffset}
        >
          <View style={styles.handle} />
          <View style={styles.header}>
            <View style={styles.headerCopy}>
              <Text style={styles.eyebrow}>THREAD</Text>
              <Text numberOfLines={1} style={styles.title}>{title}</Text>
              <Text style={styles.meta}>
                {replies.length} {replies.length === 1 ? 'reply' : 'replies'}
              </Text>
            </View>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Close thread"
              hitSlop={8}
              onPress={onClose}
              style={({ pressed }) => [styles.close, pressed && styles.pressed]}
            >
              <SymbolView name="xmark" size={15} tintColor={colors.text2} />
            </Pressable>
          </View>

          <ScrollView
            ref={scrollRef}
            contentInsetAdjustmentBehavior="automatic"
            keyboardShouldPersistTaps="handled"
            contentContainerStyle={styles.conversation}
            onContentSizeChange={() => {
              if (!visible || initialScrollCompleteRef.current) return;
              initialScrollCompleteRef.current = true;
              requestAnimationFrame(() => scrollRef.current?.scrollToEnd({ animated: false }));
            }}
          >
            {conversation.map((message, index) => {
              const own = isOwnMessageForViewer(message, {
                viewerEmail,
                threadVisibility,
                threadOwnerEmail,
              });
              const email = String(message.authorEmail ?? '').trim().toLowerCase();
              const avatarDataURL = String(message.avatarDataURL ?? participantAvatars.get(email) ?? '') || undefined;
              return (
                <React.Fragment key={String(message.id)}>
                  {index === 1 ? (
                    <View accessibilityRole="header" style={styles.replyDivider}>
                      <View style={styles.rule} />
                      <Text style={styles.replyDividerText}>REPLIES</Text>
                      <View style={styles.rule} />
                    </View>
                  ) : null}
                  <View style={styles.messageRow}>
                    <MessageBubble
                      message={message}
                      own={own}
                      showAuthor
                      showAvatar
                      avatarDataURL={avatarDataURL}
                      sessionToken={sessionToken}
                      viewerEmail={viewerEmail}
                      timestampReveal={timestampReveal}
                      onOpenSource={() => scrollRef.current?.scrollTo({ y: 0, animated: true })}
                      onOpenReplySource={() => scrollRef.current?.scrollTo({ y: 0, animated: true })}
                      showReplyContext={false}
                      onOpenAttachment={onOpenAttachment}
                      onLongPress={onLongPress}
                      onToggleReaction={onToggleReaction}
                      onRetryReply={onRetryReply}
                      onResolveProposal={onResolveProposal}
                      proposalObjective={proposalObjectives[String(message.id)]}
                      onChangeProposalObjective={onChangeProposalObjective}
                      resolvingProposal={resolvingProposalID === String(message.id)}
                      onOpenLongMessage={onOpenLongMessage}
                      onOpenWorkArtifact={onOpenWorkArtifact}
                      onOpenStudioProject={onOpenStudioProject}
                      onOpenWorkAsset={onOpenWorkAsset}
                      onResolveWorkCheckpoint={onResolveWorkCheckpoint}
                      onChangeWorkProject={onChangeWorkProject}
                      onSaveWorkArtifact={onSaveWorkArtifact}
                      onOpenSavedWorkArtifact={onOpenSavedWorkArtifact}
                      onRegenerateWorkArtifact={onRegenerateWorkArtifact}
                      onRetryGoal={onRetryGoal}
                      savingWork={savingWorkID === String(message.id)}
                      regeneratingWork={regeneratingWorkID === String(message.id)}
                      retryingGoal={retryingGoalID === String(message.id)}
                      workSaved={savedWorkIDs.has(String(message.id))}
                      workDriveSaveAvailability={workDriveSaveAvailability}
                    />
                  </View>
                </React.Fragment>
              );
            })}
            {replies.length === 0 ? <View style={styles.emptyBreath} /> : null}
          </ScrollView>

		  {error || projectStatus ? <Text accessibilityLiveRegion="polite" style={styles.error}>{error || projectStatus}</Text> : null}
          <Glass radius={radius.xl} style={styles.composer}>
            {pendingFiles.length > 0 || stagingFiles.length > 0 ? (
              <View accessibilityLabel="Reply attachments" style={styles.pendingFiles}>
                {stagingFiles.map((file) => (
                  <View key={file.id} style={[styles.pendingFile, styles.stagingFile]}>
                    <Text numberOfLines={1} style={styles.pendingFileText}>{file.name}</Text>
                    <ActivityIndicator color={colors.text2} size="small" />
                  </View>
                ))}
                {pendingFiles.map((file) => (
                  <View key={`${file.ref}-${file.name}`} style={styles.pendingFile}>
                    <SymbolView name={file.mime.startsWith('image/') ? 'photo' : 'doc.richtext'} tintColor={colors.text2} size={14} />
                    <Text numberOfLines={1} style={styles.pendingFileText}>{file.name}</Text>
                    <Pressable
                      accessibilityRole="button"
                      accessibilityLabel={`Remove ${file.name}`}
                      onPress={() => onRemoveAttachment(file)}
                      style={({ pressed }) => [styles.pendingRemove, pressed && styles.pressed]}
                    >
                      <SymbolView name="xmark" tintColor={colors.text3} size={10} />
                    </Pressable>
                  </View>
                ))}
              </View>
            ) : null}
            <View style={styles.composerRow}>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Add attachment to reply"
                accessibilityState={{ disabled: sending || uploading }}
                disabled={sending || uploading}
                onPress={onAddAttachment}
                style={({ pressed }) => [styles.attachment, (pressed || sending || uploading) && styles.sendDim]}
              >
                <SymbolView name="plus" size={20} tintColor={colors.text2} />
              </Pressable>
              <View style={styles.input}>
                <MentionComposerInput
                  accessibilityLabel="Reply in thread"
                  candidates={mentionCandidates}
                  editable={!sending && !uploading}
                  onChangeText={setDraft}
                  onDocumentQuery={(query) => onBrowseDrive(query)}
                  placeholder="Reply in thread…"
                  value={draft}
                />
              </View>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Send threaded reply"
                accessibilityState={{ disabled: sending || uploading || (!draft.trim() && pendingFiles.length === 0) }}
                disabled={sending || uploading || (!draft.trim() && pendingFiles.length === 0)}
                onPress={() => { void submit(); }}
                style={({ pressed }) => [styles.send, (pressed || sending || uploading || (!draft.trim() && pendingFiles.length === 0)) && styles.sendDim]}
              >
                {sending ? (
                  <ActivityIndicator color={colors.onAccent} size="small" />
                ) : (
                  <SymbolView name="arrow.up" size={18} tintColor={colors.onAccent} />
                )}
              </Pressable>
            </View>
          </Glass>
		  <Modal animationType="slide" presentationStyle="pageSheet" visible={explicitProjectAttachmentEnabled && projectChooserOpen && projectContext.available} onRequestClose={() => setProjectChooserOpen(false)}>
			<SafeAreaView style={styles.projectSheet}>
			  <View style={styles.projectSheetHeader}>
				<Text accessibilityRole="header" style={styles.projectSheetTitle}>Choose a project</Text>
				<Pressable accessibilityRole="button" accessibilityLabel="Close project chooser" onPress={() => setProjectChooserOpen(false)} style={({ pressed }) => [styles.projectSheetClose, pressed && styles.pressed]}><SymbolView name="xmark" size={17} tintColor={colors.text1} /></Pressable>
			  </View>
			  <ScrollView contentContainerStyle={styles.projectChoices}>
				{[{ title: 'No project', token: '', choiceKey: '' }, ...projectContext.choices].map((choice) => {
				  const selected = choice.token ? (choice.choiceKey ? selectedProject?.choiceKey === choice.choiceKey : selectedProject?.token === choice.token) : projectExplicitNone;
				  return <Pressable key={choice.choiceKey || choice.token || 'none'} accessibilityRole="radio" accessibilityState={{ selected }} accessibilityLabel={choice.title} onPress={() => { setSelectedProject(choice.token ? { ...choice, text: draft.trim(), sourceKey: projectSourceKey } : null); setProjectExplicitNone(!choice.token); setProjectChooserOpen(false); }} style={({ pressed }) => [styles.projectChoice, selected && styles.projectChoiceSelected, pressed && styles.pressed]}><Text maxFontSizeMultiplier={1.8} style={styles.projectChoiceText}>{choice.title}</Text>{selected ? <SymbolView name="checkmark" size={16} tintColor={colors.ember} /> : null}</Pressable>;
				})}
			  </ScrollView>
			  <Text style={styles.projectSheetHint}>Nothing changes until you send.</Text>
			</SafeAreaView>
		  </Modal>
          {actionOverlay}
        </KeyboardAvoidingView>
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  fill: { flex: 1 },
  handle: { alignSelf: 'center', width: 36, height: 5, marginTop: space[2], borderRadius: radius.full, backgroundColor: colors.line2 },
  header: { minHeight: 82, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[5], paddingBottom: space[3], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  headerCopy: { flex: 1, minWidth: 0, gap: 1 },
  eyebrow: { ...type.label, color: colors.emberText },
  title: { ...type.headline, color: colors.text1 },
  meta: { ...type.caption, color: colors.text3, fontVariant: ['tabular-nums'] },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.md, backgroundColor: colors.surface3 },
  pressed: { opacity: 0.72, transform: [{ scale: 0.96 }] },
  conversation: { paddingTop: space[3], paddingBottom: space[8] },
  messageRow: { position: 'relative' },
  replyDivider: { flexDirection: 'row', alignItems: 'center', gap: space[3], marginHorizontal: space[4], marginTop: space[5], marginBottom: space[2] },
  rule: { flex: 1, height: StyleSheet.hairlineWidth, backgroundColor: colors.line1 },
  replyDividerText: { ...type.label, color: colors.text3 },
  emptyBreath: { height: space[10] },
  error: { ...type.caption, color: colors.danger, paddingHorizontal: space[5], paddingBottom: space[2] },
  composer: { minHeight: 58, maxHeight: 360, gap: space[2], marginHorizontal: space[4], marginTop: space[2], marginBottom: space[3], padding: 7 },
	projectChip: { minHeight: hitMin, maxWidth: '100%', alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 7, paddingHorizontal: 12, borderRadius: radius.full, backgroundColor: colors.surface3 },
	projectChipText: { ...type.captionMedium, minWidth: 0, flexShrink: 1, color: colors.text1 },
	projectSheet: { flex: 1, backgroundColor: colors.bgApp },
	projectSheetHeader: { minHeight: 72, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', paddingHorizontal: space[5], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
	projectSheetTitle: { ...type.title2, color: colors.text1 },
	projectSheetClose: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.md, backgroundColor: colors.surface3 },
	projectChoices: { padding: space[4], gap: space[2] },
	projectChoice: { minHeight: hitMin, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[3], paddingHorizontal: space[4], paddingVertical: space[3], borderRadius: radius.lg, backgroundColor: colors.surface2 },
	projectChoiceSelected: { borderWidth: 1, borderColor: colors.ember },
	projectChoiceText: { ...type.body, flex: 1, color: colors.text1 },
	projectSheetHint: { ...type.caption, color: colors.text3, paddingHorizontal: space[5], paddingBottom: space[5] },
  composerRow: { flexDirection: 'row', alignItems: 'flex-end', gap: space[2] },
  attachment: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  input: { flex: 1, minHeight: hitMin, maxHeight: 328, justifyContent: 'center', paddingTop: 10, paddingBottom: 4 },
  pendingFiles: { flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 6 },
  pendingFile: { minHeight: 34, maxWidth: '100%', flexDirection: 'row', alignItems: 'center', gap: 6, paddingLeft: 10, paddingRight: 4, borderRadius: radius.full, backgroundColor: colors.surface3 },
  stagingFile: { opacity: 0.72, paddingRight: 10 },
  pendingFileText: { ...type.captionMedium, minWidth: 0, maxWidth: 220, flexShrink: 1, color: colors.text1 },
  pendingRemove: { width: 34, height: 34, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  send: { width: hitMin, height: hitMin, flex: 0, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.accent },
  sendDim: { opacity: 0.46, transform: [{ scale: 0.96 }] },
});
