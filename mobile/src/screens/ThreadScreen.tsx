import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ActivityIndicator,
  Alert,
  Animated,
  findNodeHandle,
  Keyboard,
  KeyboardAvoidingView,
  Modal,
  PanResponder,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  useWindowDimensions,
  View,
} from "react-native";
import {
  SafeAreaView,
  useSafeAreaInsets,
} from "react-native-safe-area-context";
import * as Haptics from "expo-haptics";
import * as DocumentPicker from "expo-document-picker";
import * as ImagePicker from "expo-image-picker";
import { Image } from "expo-image";
import { SymbolView } from "expo-symbols";
import type { NativeStackScreenProps } from "@react-navigation/native-stack";
import { useFocusEffect } from "@react-navigation/native";
import * as Linking from "expo-linking";
import * as Clipboard from "expo-clipboard";
import { FlashList, type FlashListRef } from "@shopify/flash-list";
import { api, BonfireApiError } from "../api/client";
import type {
  ChatMentionCandidate,
  DriveFileRecord,
  GiphySearchResult,
	HomeProjectChoice,
  ProjectCorrectionProjection,
  ScoutFileAttachment,
  ScoutMessage,
  ThreadDigestResponse,
} from "../api/types";
import { useAuth } from "../auth/AuthContext";
import { syncNotificationBadge } from "../push/usePushRegistration";
import { MessageBubble } from "../messaging/MessageBubble";
import { ChannelList } from "../messaging/ChannelList";
import { firstUnreadIndex } from "../messaging/unreadBoundary";
import { buildTimelineMarkers } from "../messaging/timelineMarkers";
import { CatchUpSheet } from "../messaging/CatchUpSheet";
import { MessageActionSheet } from "../messaging/MessageActionSheet";
import { ProjectCorrectionSheet } from "../messaging/ProjectCorrectionSheet";
import { LongMessageSheet } from "../messaging/LongMessageSheet";
import { ThreadDetailSheet } from "../messaging/ThreadDetailSheet";
import { MentionComposerInput } from "../messaging/MentionComposerInput";
import { channelDisplayName } from "../messaging/channelPresentation";
import { AttachmentSourceSheet } from "../messaging/AttachmentSourceSheet";
import { GifPickerSheet } from "../messaging/GifPickerSheet";
import {
  attachmentBatchMessage,
  maxConcurrentAttachmentUploads,
  maxMessageAttachments,
  prepareAttachmentBatch,
  type AttachmentAssetInput,
} from "../messaging/attachmentSources";
import {
  ThreadNotificationMenu,
  type ThreadNotificationLevel,
} from "../messaging/ThreadNotificationMenu";
import {
  groupMessageReactions,
  isOwnMessageForViewer,
} from "../messaging/messagePresentation";
import {
  safeWorkProgressNote,
  workFamilyLabel,
  workPhaseLabel,
} from "../messaging/workPresentation";
import { threadWorkspaceLayout } from "../messaging/threadWorkspaceLayout";
import { nativeShellLayout } from "../navigation/nativeShellModel";
import { buildThreadReplyTopology } from "../messaging/threadReplyTopology";
import {
  applyChatThreadEvent,
  chatThreadEventJournalCovers,
  isMessageRunEnd,
  maxChatThreadEventJournal,
  resolveChatThreadSnapshot,
  type ChatThreadEventPayload,
  type ChatTypingEventPayload,
  type SequencedChatThreadEvent,
} from "../messaging/chatRealtime";
import {
  TypingIndicator,
  type TypingParticipant,
} from "../messaging/TypingIndicator";
import { FilePreviewModal } from "../components/FilePreviewModal";
import {
  shouldBeginTimestampReveal,
  timestampRevealProgress,
} from "../messaging/messageGestures";
import {
  nextThreadScrollInteraction,
  shouldFollowThreadTail,
  threadRowPresentationEqual,
  threadRowRecycleType,
  type ThreadListRow as ThreadRow,
} from "../messaging/threadListPerformance";
import { useOfficeEvents } from "../realtime/OfficeEventsContext";
import { Glass } from "../theme/glass";
import { Waveform } from "../components/Waveform";
import { useComposerDictation } from "../voice/useComposerDictation";
import type { RootStackParamList } from "../navigation/types";
import { colors, hitMin, radius, shadow, space, type } from "../theme/tokens";
import { useReduceMotion } from "../theme/motion";
import {
  ArtifactSaveSheet,
  DriveFilePickerSheet,
} from "../drive/DrivePickerSheet";
import {
  completeDocumentReference,
  mergeExactAttachmentGrants,
} from "../drive/driveModels";
import {
  createDispositionOperationId,
  validDispositionRef,
} from "../artifacts/artifactDisposition";
import { createConversationOperationId } from "../conversations/newConversation";
import { RegenerateWorkSheet } from "../artifacts/RegenerateWorkSheet";

type Props = NativeStackScreenProps<RootStackParamList, "Thread">;

type ProjectCorrectionTarget = {
  messageId: string;
  threadId: string;
  sessionToken: string;
  returnFocusHandle?: number;
};

function workThreadIsActive(message: ScoutMessage): boolean {
  if (String(message.kind ?? "").toLowerCase() !== "thread" || !message.thread)
    return false;
  return [
    "queued",
    "running",
    "approval_required",
    "needs_input",
    "parked",
  ].includes(String(message.thread.status ?? "").toLowerCase());
}

function workThreadPhase(message: ScoutMessage): string {
  return workPhaseLabel(message.thread);
}

const privateThreadStarters = [
  {
    label: "Pitch the idea",
    example: "Tell a compelling 10-slide story",
    prompt: "Create a polished 10-slide pitch deck for ",
  },
  {
    label: "Research the question",
    example: "Compare the market and cite sources",
    prompt: "Research ",
  },
  {
    label: "Model the business",
    example: "Turn assumptions into a working forecast",
    prompt: "Build a financial model for ",
  },
  {
    label: "Shape the visual direction",
    example: "Turn a brief into a coherent design",
    prompt: "Design ",
  },
] as const;

function sameDispositionRef(left: unknown, right: unknown): boolean {
  if (!validDispositionRef(left) || !validDispositionRef(right)) return false;
  return (
    left.tenantId === right.tenantId &&
    left.artifactId === right.artifactId &&
    left.contentRevision === right.contentRevision &&
    left.contentDigest === right.contentDigest &&
    left.aclVersion === right.aclVersion &&
    left.audienceDigest === right.audienceDigest
  );
}

type ThreadMessageRowProps = {
  item: ThreadRow;
  sessionToken: string;
  viewerEmail: string;
  timestampReveal: Animated.Value;
  retryingReply: boolean;
  resolvingProposal: boolean;
  savingImage: boolean;
  regeneratingImage: boolean;
  imageSaved: boolean;
  proposalObjective?: string;
  savingWork: boolean;
  regeneratingWork: boolean;
  workSaved: boolean;
  workDriveSaveAvailability: "checking" | "available" | "unavailable";
  onOpenSource: (messageId: string) => void;
  onOpenAttachment: (file: ScoutFileAttachment) => void;
  onLongPress: (
    message: ScoutMessage,
    own: boolean,
    attachment?: { file: ScoutFileAttachment; index: number },
  ) => void;
  onToggleReaction: (
    message: ScoutMessage,
    emoji: string,
    active: boolean,
  ) => void;
  onRetryReply: (message: ScoutMessage) => void;
  onResolveProposal: (
    message: ScoutMessage,
    action: "accepted" | "dismissed",
    objective: string,
  ) => void;
  onChangeProposalObjective: (message: ScoutMessage, objective: string) => void;
  onSaveWorkArtifact: (message: ScoutMessage) => void;
  onRegenerateWorkArtifact: (message: ScoutMessage) => void;
  onSaveImage: (message: ScoutMessage) => void;
  onRegenerateImage: (message: ScoutMessage) => void;
  onOpenCatchUp: () => void;
  onOpenLongMessage: (text: string, authorName: string, scout: boolean) => void;
  onOpenWorkArtifact: (message: ScoutMessage, returnFocusHandle?: number) => void;
  onOpenThread: (message: ScoutMessage) => void;
  onChangeProject: (message: ScoutMessage, returnFocusHandle?: number) => void;
};

const ThreadMessageRow = React.memo(
  function ThreadMessageRow({
    item,
    sessionToken,
    viewerEmail,
    timestampReveal,
    retryingReply,
    resolvingProposal,
    savingImage,
    regeneratingImage,
    imageSaved,
    proposalObjective,
    savingWork,
    regeneratingWork,
    workSaved,
    workDriveSaveAvailability,
    onOpenSource,
    onOpenAttachment,
    onLongPress,
    onToggleReaction,
    onRetryReply,
    onResolveProposal,
    onChangeProposalObjective,
    onSaveWorkArtifact,
    onRegenerateWorkArtifact,
    onSaveImage,
    onRegenerateImage,
    onOpenCatchUp,
    onOpenLongMessage,
    onOpenWorkArtifact,
    onOpenThread,
    onChangeProject,
  }: ThreadMessageRowProps) {
    return (
      <>
        {item.timelineLabel ? (
          <View style={styles.timelineMarker}>
            <Text accessibilityRole="header" style={styles.timelineMarkerLabel}>
              {item.timelineLabel}
            </Text>
          </View>
        ) : null}
        {item.boundaryLabel ? (
          <>
            <View style={styles.boundary}>
              <View style={styles.boundaryRule} />
              <Text style={styles.boundaryLabel}>{item.boundaryLabel}</Text>
              <View style={styles.boundaryRule} />
            </View>
            {item.showCatchUp ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Catch me up"
                onPress={onOpenCatchUp}
                style={({ pressed }) => [
                  styles.catchUp,
                  pressed && styles.pressedRow,
                ]}
              >
                <SymbolView
                  name="text.line.first.and.arrowtriangle.forward"
                  tintColor={colors.emberText}
                  size={14}
                />
                <Text style={styles.catchUpText}>Catch me up</Text>
              </Pressable>
            ) : null}
          </>
        ) : null}
        <MessageBubble
          message={item.message}
          own={item.own}
          showAuthor={item.showAuthor}
          showAvatar={item.showAvatar}
          avatarDataURL={item.avatarDataURL}
          sessionToken={sessionToken}
          viewerEmail={viewerEmail}
          timestampReveal={timestampReveal}
          onOpenSource={onOpenSource}
          onOpenReplySource={onOpenSource}
          threadReplies={item.threadReplies}
          onOpenThread={onOpenThread}
          onChangeProject={onChangeProject}
          onOpenAttachment={onOpenAttachment}
          onLongPress={onLongPress}
          onToggleReaction={onToggleReaction}
          onRetryReply={onRetryReply}
          onResolveProposal={onResolveProposal}
          proposalObjective={proposalObjective}
          onChangeProposalObjective={onChangeProposalObjective}
          onSaveWorkArtifact={onSaveWorkArtifact}
          onRegenerateWorkArtifact={onRegenerateWorkArtifact}
          onSaveImage={onSaveImage}
          onRegenerateImage={onRegenerateImage}
          resolvingProposal={resolvingProposal}
          onOpenLongMessage={onOpenLongMessage}
          onOpenWorkArtifact={onOpenWorkArtifact}
          retryingReply={retryingReply}
          savingImage={savingImage}
          regeneratingImage={regeneratingImage}
          imageSaved={imageSaved}
          savingWork={savingWork}
          regeneratingWork={regeneratingWork}
          workSaved={workSaved}
          workDriveSaveAvailability={workDriveSaveAvailability}
        />
      </>
    );
  },
  (previous, next) =>
    threadRowPresentationEqual(previous.item, next.item) &&
    previous.sessionToken === next.sessionToken &&
    previous.viewerEmail === next.viewerEmail &&
    previous.timestampReveal === next.timestampReveal &&
    previous.retryingReply === next.retryingReply &&
    previous.resolvingProposal === next.resolvingProposal &&
    previous.savingImage === next.savingImage &&
    previous.regeneratingImage === next.regeneratingImage &&
    previous.imageSaved === next.imageSaved &&
    previous.proposalObjective === next.proposalObjective &&
    previous.savingWork === next.savingWork &&
    previous.regeneratingWork === next.regeneratingWork &&
    previous.workSaved === next.workSaved &&
    previous.workDriveSaveAvailability === next.workDriveSaveAvailability &&
    previous.onOpenSource === next.onOpenSource &&
    previous.onOpenAttachment === next.onOpenAttachment &&
    previous.onLongPress === next.onLongPress &&
    previous.onToggleReaction === next.onToggleReaction &&
    previous.onRetryReply === next.onRetryReply &&
    previous.onResolveProposal === next.onResolveProposal &&
    previous.onChangeProposalObjective === next.onChangeProposalObjective &&
    previous.onSaveWorkArtifact === next.onSaveWorkArtifact &&
    previous.onRegenerateWorkArtifact === next.onRegenerateWorkArtifact &&
    previous.onSaveImage === next.onSaveImage &&
    previous.onRegenerateImage === next.onRegenerateImage &&
    previous.onOpenCatchUp === next.onOpenCatchUp &&
    previous.onOpenLongMessage === next.onOpenLongMessage &&
    previous.onOpenWorkArtifact === next.onOpenWorkArtifact &&
    previous.onOpenThread === next.onOpenThread &&
    previous.onChangeProject === next.onChangeProject,
);

const threadRowKey = (row: ThreadRow) => String(row.message.id);
const threadMomentumGraceMs = 200;
// FlashList's native scroll anchor must remain enabled. Older heterogeneous
// rows are measured as the viewer scrolls upward; disabling the anchor exposes
// those size corrections as visible jumps.
const threadListPosition = { startRenderingFromBottom: true } as const;

/**
 * A thread — design §14.
 *
 * Rebuilt from a card list plus an "Ask" box into genuine messaging. The
 * composer is glass (it floats above the conversation, §7); the bubbles are not
 * (they ARE the conversation).
 *
 * The mic is a peer of the keyboard, not an afterthought: holding it dictates
 * into the draft with company-vocabulary transcription, which is the whole
 * reason to type work messages here rather than in Slack (§10).
 */
export function ThreadScreen({ route, navigation }: Props) {
  const { sessionToken, user } = useAuth();
  const office = useOfficeEvents();
  const reduceMotion = useReduceMotion();
  const insets = useSafeAreaInsets();
  const window = useWindowDimensions();
  const workspaceLayout = threadWorkspaceLayout(
    window.width,
    window.fontScale,
    nativeShellLayout(
      window.width,
      Platform.OS !== "ios" || Platform.isPad,
      window.fontScale,
    ) === "sidebar",
  );
  const iPadWorkspace = workspaceLayout.conversationPane;
  const [messages, setMessages] = useState<ScoutMessage[]>([]);
  const [draft, setDraft] = useState("");
	const [projectContext, setProjectContext] = useState<{ available: boolean; scopeKey: string; choices: HomeProjectChoice[] }>({ available: false, scopeKey: "", choices: [] });
	const [selectedProject, setSelectedProject] = useState<(HomeProjectChoice & { text: string; threadId: string }) | null>(null);
	const [projectExplicitNone, setProjectExplicitNone] = useState(false);
	const [projectChooserOpen, setProjectChooserOpen] = useState(false);
	const projectContextGenerationRef = useRef(0);
  const [loading, setLoading] = useState(true);
  const [sending, setSending] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [pendingFiles, setPendingFiles] = useState<ScoutFileAttachment[]>([]);
  const [stagingFiles, setStagingFiles] = useState<
    Array<{ id: string; name: string; mime: string; uri?: string }>
  >([]);
  const [threadReplyFiles, setThreadReplyFiles] = useState<
    ScoutFileAttachment[]
  >([]);
  const [threadReplyStagingFiles, setThreadReplyStagingFiles] = useState<
    Array<{ id: string; name: string; mime: string; uri?: string }>
  >([]);
  const [editingMessage, setEditingMessage] = useState<ScoutMessage | null>(
    null,
  );
  const [actionMessage, setActionMessage] = useState<{
    message: ScoutMessage;
    own: boolean;
    attachment?: { file: ScoutFileAttachment; index: number };
  } | null>(null);
  const [projectCorrectionTarget, setProjectCorrectionTarget] = useState<ProjectCorrectionTarget | null>(null);
  const [projectCorrection, setProjectCorrection] = useState<ProjectCorrectionProjection | null>(null);
  const [projectCorrectionLoading, setProjectCorrectionLoading] = useState(false);
  const [projectCorrectionUpdating, setProjectCorrectionUpdating] = useState(false);
  const [projectCorrectionError, setProjectCorrectionError] = useState("");
  const [previewFile, setPreviewFile] = useState<ScoutFileAttachment | null>(
    null,
  );
  const [expandedMessage, setExpandedMessage] = useState<{
    text: string;
    authorName: string;
    scout: boolean;
    activity?: boolean;
    report?: { agentName: string; mode: string; status: string };
  } | null>(null);
  const expandedMessageReturnFocusHandleRef = useRef<number | null>(null);
  const activeWorkTriggerRef = useRef<View>(null);
  const [participants, setParticipants] = useState<ChatMentionCandidate[]>([
    { name: "Scout", kind: "scout" },
  ]);
  const [threadVisibility, setThreadVisibility] = useState("private");
  const [threadOwnerEmail, setThreadOwnerEmail] = useState("");
  const [threadTitle, setThreadTitle] = useState(route.params.title);
  const [editingThreadTitle, setEditingThreadTitle] = useState(false);
  const [threadTitleDraft, setThreadTitleDraft] = useState(route.params.title);
  const [notificationLevel, setNotificationLevel] =
    useState<ThreadNotificationLevel>("all");
  const [notificationMenuOpen, setNotificationMenuOpen] = useState(false);
  const [notificationBusy, setNotificationBusy] = useState(false);
  const [attachmentSourceOpen, setAttachmentSourceOpen] = useState(false);
  const [gifPickerOpen, setGifPickerOpen] = useState(false);
  const [attachmentTarget, setAttachmentTarget] = useState<"message" | "reply">(
    "message",
  );
  const [drivePicker, setDrivePicker] = useState<{
    target: "message" | "reply";
    query: string;
    fromHash: boolean;
  } | null>(null);
  const [threadReplyDocumentSelection, setThreadReplyDocumentSelection] =
    useState<{ key: number; name: string } | null>(null);
  const [proposalObjectives, setProposalObjectives] = useState<
    Record<string, string>
  >({});
  const [workSaveTarget, setWorkSaveTarget] = useState<ScoutMessage | null>(
    null,
  );
  const [savingWorkID, setSavingWorkID] = useState<string | null>(null);
  const [savedWorkIDs, setSavedWorkIDs] = useState<Set<string>>(
    () => new Set(),
  );
  const [workSaveError, setWorkSaveError] = useState("");
  const [workDriveSaveAvailability, setWorkDriveSaveAvailability] = useState<
    "checking" | "available" | "unavailable"
  >("checking");
  const [attachmentSaveTarget, setAttachmentSaveTarget] = useState<{
    message: ScoutMessage;
    file: ScoutFileAttachment;
    index: number;
  } | null>(null);
  const [savingAttachment, setSavingAttachment] = useState(false);
  const [attachmentSaveError, setAttachmentSaveError] = useState("");
  const [regenerateWorkTarget, setRegenerateWorkTarget] =
    useState<ScoutMessage | null>(null);
  const [regeneratingWorkID, setRegeneratingWorkID] = useState<string | null>(
    null,
  );
  const [regenerateWorkError, setRegenerateWorkError] = useState("");
  const [typingParticipants, setTypingParticipants] = useState<
    TypingParticipant[]
  >([]);
  const [error, setError] = useState<string | null>(null);
  const [retryingReplyID, setRetryingReplyID] = useState<string | null>(null);
  const [resolvingProposalID, setResolvingProposalID] = useState<string | null>(
    null,
  );
  const [savingImageID, setSavingImageID] = useState<string | null>(null);
  const [regeneratingImageID, setRegeneratingImageID] = useState<string | null>(
    null,
  );
  const [savedImageIDs, setSavedImageIDs] = useState<Set<string>>(
    () => new Set(),
  );
  const [threadContextRootID, setThreadContextRootID] = useState<string | null>(
    null,
  );
  const [threadReplySending, setThreadReplySending] = useState(false);
  const [threadReplyError, setThreadReplyError] = useState("");
  // null means "not yet loaded" — distinct from "" which means never read.
  const [readAt, setReadAt] = useState<string | null>(null);
  const listRef = useRef<FlashListRef<ThreadRow>>(null);
  const messagesRef = useRef<ScoutMessage[]>([]);
  messagesRef.current = messages;
  const replyTopology = useMemo(
    () => buildThreadReplyTopology(messages),
    [messages],
  );
  const replyTopologyRef = useRef(replyTopology);
  replyTopologyRef.current = replyTopology;
  const feedMessages = replyTopology.feedMessages;
  const feedMessagesRef = useRef<ScoutMessage[]>([]);
  feedMessagesRef.current = feedMessages;
  const threadContextRoot = threadContextRootID
    ? (replyTopology.rootFor(threadContextRootID) ?? null)
    : null;
  const threadContextReplies = threadContextRoot
    ? replyTopology.repliesFor(threadContextRoot)
    : [];
  // Starts false while the thread loads. A normal open flips it true once the
  // bottom-rendered list is on screen; a targeted message link stays false
  // until the viewer actually reaches the latest message.
  const atBottomRef = useRef(false);

  // Rotation and Dynamic Type can remeasure every heterogeneous row after the
  // list has already reached its tail. Follow the newly measured tail only
  // when the viewer was already there; a reader scrolled upward keeps control.
  useEffect(() => {
    if (!atBottomRef.current) return;
    const frame = requestAnimationFrame(() => {
      listRef.current?.scrollToEnd({ animated: false });
    });
    return () => cancelAnimationFrame(frame);
  }, [
    window.fontScale,
    window.height,
    window.width,
    workspaceLayout.conversationPane,
    workspaceLayout.stackedActivity,
  ]);
  const threadScrollInteractionRef = useRef(false);
  const threadMomentumGraceTimerRef = useRef<ReturnType<
    typeof setTimeout
  > | null>(null);
  const listHeightRef = useRef(0);
  const lastMarkedMessageIDRef = useRef<string | null>(null);
  const markingMessageIDRef = useRef<string | null>(null);
  const [digest, setDigest] = useState<ThreadDigestResponse | null>(null);
  const [catchUpOpen, setCatchUpOpen] = useState(false);
  const typingExpiryTimersRef = useRef<
    Map<string, ReturnType<typeof setTimeout>>
  >(new Map());
  const typingIdleTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const typingActiveRef = useRef(false);
  const typingLastSignalAtRef = useRef(0);
  const threadTitleRenameInFlightRef = useRef(false);
  const transcriptGenerationRef = useRef(0);
  const transcriptEventJournalRef = useRef<SequencedChatThreadEvent[]>([]);
  const workSaveAttemptRef = useRef<{
    artifactId: string;
    fileName: string;
    folderId: string;
    operationId: string;
  } | null>(null);
  const messageSendAttemptRef = useRef<{
    key: string;
    operationId: string;
  } | null>(null);
  const threadReplyAttemptRef = useRef<{
    key: string;
    operationId: string;
  } | null>(null);
  const projectCorrectionTargetRef = useRef<ProjectCorrectionTarget | null>(null);
  const projectCorrectionGenerationRef = useRef(0);
  const projectCorrectionAttemptRef = useRef<{
    key: string;
    operationId: string;
  } | null>(null);

  useEffect(() => {
    let current = true;
    if (!sessionToken) {
      setWorkDriveSaveAvailability("unavailable");
      return () => {
        current = false;
      };
    }
    setWorkDriveSaveAvailability("checking");
    void api
      .artifactDriveSaveCapability(sessionToken)
      .then((capability) => {
        if (!current) return;
        const available =
          capability?.available === true &&
          capability.action === "save" &&
          capability.receiptBacked === true;
        setWorkDriveSaveAvailability(available ? "available" : "unavailable");
      })
      .catch(() => {
        if (current) setWorkDriveSaveAvailability("unavailable");
      });
    return () => {
      current = false;
    };
  }, [sessionToken]);
  const applyTranscriptSnapshot = useCallback(
    (generationAtRequest: number, next: ScoutMessage[]) => {
      const currentGeneration = transcriptGenerationRef.current;
      const journal = [...transcriptEventJournalRef.current];
      const canResolve = chatThreadEventJournalCovers(
        generationAtRequest,
        currentGeneration,
        journal,
      );
      if (!canResolve) return false;
      setMessages(
        (current) =>
          resolveChatThreadSnapshot(
            current,
            next,
            route.params.threadId,
            generationAtRequest,
            currentGeneration,
            journal,
          ).messages,
      );
      return true;
    },
    [route.params.threadId],
  );
  const timestampReveal = useRef(new Animated.Value(0)).current;
  const timestampPan = useMemo(
    () =>
      PanResponder.create({
        onMoveShouldSetPanResponder: (_event, gesture) =>
          shouldBeginTimestampReveal(gesture.dx, gesture.dy),
        onPanResponderMove: (_event, gesture) => {
          timestampReveal.setValue(timestampRevealProgress(gesture.dx));
        },
        onPanResponderRelease: () => {
          if (reduceMotion) timestampReveal.setValue(0);
          else
            Animated.spring(timestampReveal, {
              toValue: 0,
              damping: 18,
              stiffness: 240,
              mass: 0.8,
              useNativeDriver: true,
            }).start();
        },
        onPanResponderTerminate: () => {
          if (reduceMotion) timestampReveal.setValue(0);
          else
            Animated.spring(timestampReveal, {
              toValue: 0,
              damping: 18,
              stiffness: 240,
              mass: 0.8,
              useNativeDriver: true,
            }).start();
        },
      }),
    [reduceMotion, timestampReveal],
  );

  // Scroll to a cited message. Both the catch-up and the deposit rail point at
  // real messages, so both need to be able to land on one.
  const scrollToMessage = useCallback((messageId: string) => {
    const message = messagesRef.current.find(
      (candidate) => String(candidate.id) === messageId,
    );
    if (!message) return;
    const root = replyTopologyRef.current.rootFor(message);
    if (root && String(root.id) !== String(message.id)) {
      setThreadReplyError("");
      setThreadContextRootID(String(root.id));
      return;
    }
    const index = feedMessagesRef.current.findIndex(
      (candidate) => String(candidate.id) === messageId,
    );
    if (index >= 0) listRef.current?.scrollToIndex({ index, animated: true });
  }, []);

  const openThreadContext = useCallback((message: ScoutMessage) => {
    const root = replyTopologyRef.current.rootFor(message);
    if (!root?.id) return;
    setActionMessage(null);
    setEditingMessage(null);
    setThreadReplyError("");
    setThreadContextRootID(String(root.id));
    void Haptics.selectionAsync();
  }, []);

  const closeThreadContext = useCallback(() => {
    setThreadContextRootID(null);
    setThreadReplyError("");
    setThreadReplyFiles([]);
    setThreadReplyStagingFiles([]);
  }, []);

  useEffect(() => {
    if (threadContextRootID && !threadContextRoot) closeThreadContext();
  }, [closeThreadContext, threadContextRoot, threadContextRootID]);

  const dictation = useComposerDictation({
    context: threadVisibility === "private" ? "scout" : "chat",
    threadId: route.params.threadId,
    // Dictation takes the same ordinary send path as typed text. The hook
    // generation-fences late results, so this callback runs once per Send.
    onTranscript: ({ text }) => {
      void send(text);
    },
  });

  const load = useCallback(async () => {
    if (!sessionToken) return;
    const generationAtRequest = transcriptGenerationRef.current;
    try {
      const response = await api.scoutThread(
        sessionToken,
        route.params.threadId,
      );
      const next = response.thread?.messages ?? response.messages ?? [];
      applyTranscriptSnapshot(generationAtRequest, next);
      if (response.thread) {
        const displayTitle = channelDisplayName(response.thread);
        setThreadTitle(displayTitle);
        setThreadTitleDraft(displayTitle);
        navigation.setParams({ title: displayTitle });
      }
      setThreadVisibility(String(response.thread?.visibility ?? "private"));
      setThreadOwnerEmail(String(response.thread?.ownerEmail ?? ""));
      const level = String(
        response.notificationLevel ?? (response.muted ? "mentions" : "all"),
      );
      setNotificationLevel(
        level === "mentions" || level === "none" ? level : "all",
      );
      // Captured once, on the FIRST load only. If it tracked every refresh the
      // divider would jump to the bottom the moment the marker advanced, and
      // the "80 new messages" line would vanish while you were still reading
      // through them.
      setReadAt((current) =>
        current === null ? String(response.readAt ?? "") : current,
      );
      setError(null);
    } catch (err) {
      setError(
        err instanceof BonfireApiError
          ? err.message
          : "Could not load this thread.",
      );
    } finally {
      setLoading(false);
    }
  }, [
    applyTranscriptSnapshot,
    navigation,
    route.params.threadId,
    sessionToken,
  ]);

  useFocusEffect(
    useCallback(() => {
      void load();
    }, [load]),
  );

  useEffect(() => {
    const messageId = route.params.messageId;
    if (!loading && messageId) {
      requestAnimationFrame(() => scrollToMessage(messageId));
    }
  }, [loading, route.params.messageId, scrollToMessage]);

  useEffect(() => {
    if (!sessionToken) return;
    let active = true;
    void api
      .chatParticipants(sessionToken)
      .then((response) => {
        if (active && response.participants.length > 0)
          setParticipants(response.participants);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [sessionToken]);

  // Catch-up arrives after first paint; it is not needed to read a message.
  useEffect(() => {
    if (!sessionToken) return;
    let cancelled = false;
    void api
      .threadDigest(sessionToken, route.params.threadId)
      .then((response) => {
        if (!cancelled) setDigest(response);
      })
      .catch(() => {
        // An absent digest simply means no catch-up affordance.
      });
    return () => {
      cancelled = true;
    };
  }, [route.params.threadId, sessionToken]);

  const reconcileThread = useCallback(async () => {
    if (!sessionToken) return;
    const generationAtStart = transcriptGenerationRef.current;
    try {
      const response = await api.scoutThread(
        sessionToken,
        route.params.threadId,
      );
      if (generationAtStart !== transcriptGenerationRef.current) return;
      const shouldFollow = shouldFollowThreadTail(
        atBottomRef.current,
        threadScrollInteractionRef.current,
      );
      const next = response.thread?.messages ?? response.messages ?? [];
      if (!applyTranscriptSnapshot(generationAtStart, next)) return;
      setThreadVisibility(String(response.thread?.visibility ?? "private"));
      setThreadOwnerEmail(String(response.thread?.ownerEmail ?? ""));
      if (shouldFollow)
        requestAnimationFrame(() =>
          listRef.current?.scrollToEnd({ animated: false }),
        );
    } catch {
      // Recovery is intentionally silent; the live transcript remains usable
      // and the next 12-second pass or socket frame can self-heal it.
    }
  }, [applyTranscriptSnapshot, route.params.threadId, sessionToken]);

  // The socket is the fast path: matching message additions, replacements,
  // and deletions land immediately without waiting on a network round trip.
  useEffect(() => {
    if (office.event !== "chat_thread" || !sessionToken) return;
    const payload = office.data as ChatThreadEventPayload | null;
    if (!payload || String(payload.id ?? "") !== route.params.threadId) return;
    const generation = transcriptGenerationRef.current + 1;
    transcriptGenerationRef.current = generation;
    transcriptEventJournalRef.current = [
      ...transcriptEventJournalRef.current,
      { generation, payload },
    ].slice(-maxChatThreadEventJournal);
    const shouldFollow = shouldFollowThreadTail(
      atBottomRef.current,
      threadScrollInteractionRef.current,
    );
    setMessages((current) =>
      applyChatThreadEvent(current, route.params.threadId, payload),
    );
    if (payload?.visibility) setThreadVisibility(String(payload.visibility));
    const authorEmail = String(payload?.message?.authorEmail ?? "")
      .trim()
      .toLowerCase();
    if (authorEmail) {
      const timer = typingExpiryTimersRef.current.get(authorEmail);
      if (timer) clearTimeout(timer);
      typingExpiryTimersRef.current.delete(authorEmail);
      setTypingParticipants((current) =>
        current.filter((participant) => participant.email !== authorEmail),
      );
    }
    if (shouldFollow)
      requestAnimationFrame(() =>
        listRef.current?.scrollToEnd({ animated: false }),
      );
  }, [
    office.data,
    office.event,
    office.version,
    route.params.threadId,
    sessionToken,
  ]);

  // Socket delivery can be missed during suspension or a half-open network
  // transition. A bounded authoritative pass repairs drift without making the
  // transcript flash or showing transient recovery errors.
  useFocusEffect(
    useCallback(() => {
      const timer = setInterval(() => void reconcileThread(), 12_000);
      return () => clearInterval(timer);
    }, [reconcileThread]),
  );

  const wasConnectedRef = useRef(office.connected);
  useEffect(() => {
    const reconnected = !wasConnectedRef.current && office.connected;
    wasConnectedRef.current = office.connected;
    if (reconnected) void load();
  }, [load, office.connected]);

  const email = user?.email?.trim().toLowerCase() ?? "";
  const participantByEmail = useMemo(
    () =>
      new Map(
        [
          ...participants,
          ...(user?.email
            ? [
                {
                  name: user.name,
                  email: user.email,
                  avatarDataURL: user.avatarDataURL,
                  kind: "person" as const,
                },
              ]
            : []),
        ]
          .filter((participant) => participant.email)
          .map((participant) => [
            String(participant.email).trim().toLowerCase(),
            participant,
          ]),
      ),
    [participants, user?.avatarDataURL, user?.email, user?.name],
  );
  const participantAvatars = useMemo(
    () =>
      new Map(
        Array.from(participantByEmail.values())
          .filter(
            (participant) => participant.email && participant.avatarDataURL,
          )
          .map((participant) => [
            String(participant.email).trim().toLowerCase(),
            String(participant.avatarDataURL),
          ]),
      ),
    [participantByEmail],
  );

  useEffect(() => {
    if (office.event !== "chat_typing") return;
    const payload = office.data as ChatTypingEventPayload | null;
    if (String(payload?.threadId ?? "") !== route.params.threadId) return;
    const actorEmail = String(payload?.email ?? "")
      .trim()
      .toLowerCase();
    if (!actorEmail || actorEmail === email) return;
    const priorTimer = typingExpiryTimersRef.current.get(actorEmail);
    if (priorTimer) clearTimeout(priorTimer);
    typingExpiryTimersRef.current.delete(actorEmail);
    if (payload?.typing === false) {
      setTypingParticipants((current) =>
        current.filter((participant) => participant.email !== actorEmail),
      );
      return;
    }
    const known = participantByEmail.get(actorEmail);
    const participant: TypingParticipant = {
      email: actorEmail,
      name: String(
        payload?.name ?? known?.name ?? actorEmail.split("@")[0] ?? "Someone",
      ),
      avatarDataURL:
        String(payload?.avatarDataURL ?? known?.avatarDataURL ?? "") ||
        undefined,
    };
    setTypingParticipants((current) => [
      ...current.filter((candidate) => candidate.email !== actorEmail),
      participant,
    ]);
    const timer = setTimeout(() => {
      typingExpiryTimersRef.current.delete(actorEmail);
      setTypingParticipants((current) =>
        current.filter((candidate) => candidate.email !== actorEmail),
      );
    }, 4_500);
    typingExpiryTimersRef.current.set(actorEmail, timer);
  }, [
    email,
    office.data,
    office.event,
    office.version,
    participantByEmail,
    route.params.threadId,
  ]);

  useEffect(
    () => () => {
      typingExpiryTimersRef.current.forEach((timer) => clearTimeout(timer));
      typingExpiryTimersRef.current.clear();
    },
    [],
  );

  useEffect(() => {
    if (office.connected) return;
    typingExpiryTimersRef.current.forEach((timer) => clearTimeout(timer));
    typingExpiryTimersRef.current.clear();
    setTypingParticipants([]);
  }, [office.connected]);

  const stopTyping = useCallback(
    (notify = true) => {
      if (typingIdleTimerRef.current) clearTimeout(typingIdleTimerRef.current);
      typingIdleTimerRef.current = null;
      const wasActive = typingActiveRef.current;
      typingActiveRef.current = false;
      typingLastSignalAtRef.current = 0;
      if (notify && wasActive) {
        office.send("chat_typing", {
          threadId: route.params.threadId,
          typing: false,
        });
      }
    },
    [office.send, route.params.threadId],
  );

  const changeDraft = useCallback(
    (value: string) => {
      setDraft(value);
      if (
        threadVisibility !== "public" ||
        editingMessage ||
        !sessionToken ||
        !value.trim()
      ) {
        stopTyping();
        return;
      }
      const now = Date.now();
      if (
        !typingActiveRef.current ||
        now - typingLastSignalAtRef.current >= 1_800
      ) {
        office.send("chat_typing", {
          threadId: route.params.threadId,
          typing: true,
        });
        typingLastSignalAtRef.current = now;
      }
      typingActiveRef.current = true;
      if (typingIdleTimerRef.current) clearTimeout(typingIdleTimerRef.current);
      typingIdleTimerRef.current = setTimeout(() => stopTyping(), 2_800);
    },
    [
      editingMessage,
      office.send,
      route.params.threadId,
      sessionToken,
      stopTyping,
      threadVisibility,
    ],
  );

	useEffect(() => {
		const generation = ++projectContextGenerationRef.current;
		setSelectedProject((current) => current && (current.text !== draft.trim() || current.threadId !== route.params.threadId) ? null : current);
		if (!sessionToken || editingMessage) {
			setProjectContext({ available: false, scopeKey: "", choices: [] });
			setSelectedProject(null);
			setProjectExplicitNone(false);
			setProjectChooserOpen(false);
			return;
		}
		const timer = setTimeout(() => {
			void api.projectContext(sessionToken, { text: draft.trim(), destination: { route: "thread", threadId: route.params.threadId } }).then((response) => {
				if (generation !== projectContextGenerationRef.current) return;
				const next = response.projectContext;
				setProjectContext((current) => {
					if (current.scopeKey && next.scopeKey && current.scopeKey !== next.scopeKey) {
						setSelectedProject(null);
						setProjectExplicitNone(false);
					}
					return { available: next.available, scopeKey: next.scopeKey ?? "", choices: Array.isArray(next.choices) ? next.choices : [] };
				});
				if (!projectExplicitNone && next.suggested?.token) setSelectedProject((current) => current ?? { ...next.suggested!, text: draft.trim(), threadId: route.params.threadId });
			}).catch(() => {
				if (generation !== projectContextGenerationRef.current) return;
				setProjectContext({ available: false, scopeKey: "", choices: [] });
				setSelectedProject(null);
				setProjectExplicitNone(false);
				setProjectChooserOpen(false);
			});
		}, 220);
		return () => clearTimeout(timer);
	}, [draft, editingMessage, projectExplicitNone, route.params.threadId, sessionToken]);

	useEffect(() => {
		setSelectedProject(null);
		setProjectExplicitNone(false);
		setProjectChooserOpen(false);
		projectCorrectionGenerationRef.current += 1;
		projectCorrectionTargetRef.current = null;
		projectCorrectionAttemptRef.current = null;
		setProjectCorrectionTarget(null);
		setProjectCorrection(null);
		setProjectCorrectionLoading(false);
		setProjectCorrectionUpdating(false);
		setProjectCorrectionError("");
	}, [route.params.threadId, sessionToken]);

  const applyScoutActions = useCallback(
    (raw: unknown) => {
      if (!Array.isArray(raw)) return;
      for (const candidate of raw) {
        if (!candidate || typeof candidate !== "object") continue;
        const action = candidate as Record<string, unknown>;
        if (String(action.type ?? "").trim() !== "open_tool") continue;
        const tool = String(action.tool ?? "").trim();
        if (tool === "chat")
          navigation.navigate("Deck", { segment: "threads" });
        else if (["workflow", "research", "design", "grill"].includes(tool))
          navigation.navigate("Deck", { segment: "work" });
        else if (tool === "board") navigation.navigate("Board");
        else if (tool === "artifacts" || tool === "files")
          navigation.navigate("Files");
        else if (tool === "memory") navigation.navigate("Memory");
        else if (tool === "notifications" || tool === "alerts")
          navigation.navigate("Alerts");
        else if (tool === "settings") navigation.navigate("Settings");
      }
    },
    [navigation],
  );

  useEffect(() => () => stopTyping(), [stopTyping]);

  useEffect(() => {
    if (!office.connected) stopTyping(false);
  }, [office.connected, stopTyping]);

  useEffect(() => {
    if (
      typingParticipants.length > 0 &&
      shouldFollowThreadTail(
        atBottomRef.current,
        threadScrollInteractionRef.current,
      )
    ) {
      requestAnimationFrame(() =>
        listRef.current?.scrollToEnd({ animated: false }),
      );
    }
  }, [typingParticipants.length]);

  // Where the "N new messages" divider goes. -1 means everything is read and
  // no divider renders.
  const boundary = useMemo(
    () => firstUnreadIndex(feedMessages, readAt ?? undefined, email),
    [email, feedMessages, readAt],
  );

  const timelineLabels = useMemo(
    () => buildTimelineMarkers(feedMessages),
    [feedMessages],
  );

  const activeWorkMessage = useMemo(
    () => [...messages].reverse().find(workThreadIsActive) ?? null,
    [messages],
  );

  const rows = useMemo(
    () =>
      feedMessages.map((message, index) => {
        const own = isOwnMessageForViewer(message, {
          viewerEmail: email,
          threadVisibility,
          threadOwnerEmail,
        });
        const previous = feedMessages[index - 1];
        const showAvatar =
          !own &&
          String(message.role ?? "").toLowerCase() === "user" &&
          isMessageRunEnd(feedMessages, index);
        const knownParticipant = participantByEmail.get(
          String(message.authorEmail ?? "")
            .trim()
            .toLowerCase(),
        );
        const showAuthor =
          !previous ||
          previous.role !== message.role ||
          previous.authorEmail !== message.authorEmail;
        const isBoundary = index === boundary;
        const unreadCount = isBoundary ? feedMessages.length - boundary : 0;
        return {
          message,
          threadReplies: replyTopology.repliesFor(message).map((reply) => {
            const replyParticipant = participantByEmail.get(
              String(reply.authorEmail ?? "")
                .trim()
                .toLowerCase(),
            );
            return reply.avatarDataURL || !replyParticipant?.avatarDataURL
              ? reply
              : { ...reply, avatarDataURL: replyParticipant.avatarDataURL };
          }),
          own,
          showAuthor,
          showAvatar,
          avatarDataURL:
            String(
              message.avatarDataURL ?? knownParticipant?.avatarDataURL ?? "",
            ) || undefined,
          timelineLabel: timelineLabels[index],
          // The divider is part of the row above the first unread message
          // rather than a separate list item, so it cannot desync from it.
          boundaryLabel: isBoundary
            ? `${unreadCount} new ${unreadCount === 1 ? "message" : "messages"}`
            : undefined,
          showCatchUp: isBoundary && Boolean(digest?.catchUp?.bullets?.length),
        };
      }),
    [
      boundary,
      digest?.catchUp?.bullets?.length,
      email,
      feedMessages,
      participantByEmail,
      replyTopology,
      threadOwnerEmail,
      threadVisibility,
      timelineLabels,
    ],
  );

  // Advance the marker only when the latest message is genuinely on screen.
  // Normal opens now land there; targeted links to older messages do not.
  const markRead = useCallback(() => {
    if (!sessionToken || messagesRef.current.length === 0) return;
    const last = messagesRef.current[messagesRef.current.length - 1];
    if (!last?.id) return;
    const messageID = String(last.id);
    if (
      lastMarkedMessageIDRef.current === messageID ||
      markingMessageIDRef.current === messageID
    )
      return;
    markingMessageIDRef.current = messageID;
    void api
      .markThreadRead(sessionToken, route.params.threadId, messageID)
      .then(() => {
        lastMarkedMessageIDRef.current = messageID;
        void syncNotificationBadge(sessionToken);
      })
      .catch(() => {
        // Best-effort: a failed mark just means the thread still shows unread,
        // which is the safe direction to fail in.
      })
      .finally(() => {
        if (markingMessageIDRef.current === messageID)
          markingMessageIDRef.current = null;
      });
  }, [route.params.threadId, sessionToken]);

  const clearThreadMomentumGrace = useCallback(() => {
    if (threadMomentumGraceTimerRef.current) {
      clearTimeout(threadMomentumGraceTimerRef.current);
      threadMomentumGraceTimerRef.current = null;
    }
  }, []);

  const settleThreadScroll = useCallback(
    (offsetY: number, contentHeight: number, viewportHeight: number) => {
      clearThreadMomentumGrace();
      threadScrollInteractionRef.current = false;
      atBottomRef.current = offsetY + viewportHeight >= contentHeight - 48;
      if (atBottomRef.current) markRead();
    },
    [clearThreadMomentumGrace, markRead],
  );

  useEffect(() => () => clearThreadMomentumGrace(), [clearThreadMomentumGrace]);

  // Leaving the thread while at the bottom counts as having read it.
  useEffect(() => {
    return () => {
      if (atBottomRef.current) markRead();
    };
  }, [markRead]);

  async function send(textOverride?: string) {
    const text = (textOverride ?? draft).trim();
    const messageFiles = textOverride === undefined ? pendingFiles : [];
	const projectContextToken = !editingMessage && selectedProject?.text === text && selectedProject.threadId === route.params.threadId ? selectedProject.token : "";
    if (
      !sessionToken ||
      (!text && messageFiles.length === 0) ||
      sending ||
      uploading
    )
      return;
	if (projectContextToken && messageFiles.length > 0) {
		setError("Send the Project-linked message first, then attach files in the next turn.");
		return;
	}
    stopTyping();
    setSending(true);
    setError(null);
    const generationAtRequest = transcriptGenerationRef.current;
    const operationKey = JSON.stringify({
      threadId: route.params.threadId,
      text,
      files: messageFiles,
	  projectContextToken,
    });
    const messageAttempt =
      messageSendAttemptRef.current?.key === operationKey
        ? messageSendAttemptRef.current
        : { key: operationKey, operationId: createConversationOperationId() };
    messageSendAttemptRef.current = messageAttempt;
    try {
      const response = editingMessage
        ? await api.updateScoutMessage(
            sessionToken,
            route.params.threadId,
            String(editingMessage.id),
            text,
            messageFiles,
          )
        : await api.sendScoutMessage(
            sessionToken,
            route.params.threadId,
            text,
            messageFiles,
            "",
            messageAttempt.operationId,
			projectContextToken,
          );
      if (!editingMessage) messageSendAttemptRef.current = null;
      if (textOverride === undefined) {
        setDraft("");
        setPendingFiles([]);
		setEditingMessage(null);
		setSelectedProject(null);
		setProjectExplicitNone(false);
		setProjectChooserOpen(false);
      }
      applyTranscriptSnapshot(
        generationAtRequest,
        response.thread?.messages ?? response.messages ?? [],
      );
      applyScoutActions(response.actions);
      Keyboard.dismiss();
      atBottomRef.current = true;
      requestAnimationFrame(() =>
        listRef.current?.scrollToEnd({ animated: false }),
      );
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    } catch (err) {
      setError(
        err instanceof BonfireApiError ? err.message : "Message did not send.",
      );
    } finally {
      setSending(false);
    }
  }

  const sendThreadReply = useCallback(
    async (
      text: string,
      files: readonly ScoutFileAttachment[],
    ): Promise<boolean> => {
      const rootID = String(threadContextRootID ?? "").trim();
      if (
        !sessionToken ||
        !rootID ||
        (!text.trim() && files.length === 0) ||
        threadReplySending ||
        uploading
      )
        return false;
      const generationAtRequest = transcriptGenerationRef.current;
      setThreadReplySending(true);
      setThreadReplyError("");
      const operationKey = JSON.stringify({
        threadId: route.params.threadId,
        rootID,
        text: text.trim(),
        files,
      });
      const replyAttempt =
        threadReplyAttemptRef.current?.key === operationKey
          ? threadReplyAttemptRef.current
          : { key: operationKey, operationId: createConversationOperationId() };
      threadReplyAttemptRef.current = replyAttempt;
      try {
        const response = await api.sendScoutMessage(
          sessionToken,
          route.params.threadId,
          text.trim(),
          [...files],
          rootID,
          replyAttempt.operationId,
        );
        threadReplyAttemptRef.current = null;
        applyTranscriptSnapshot(
          generationAtRequest,
          response.thread?.messages ?? response.messages ?? [],
        );
        applyScoutActions(response.actions);
        setThreadReplyFiles([]);
        setThreadReplyStagingFiles([]);
        void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
        return true;
      } catch (caught) {
        setThreadReplyError(
          caught instanceof BonfireApiError
            ? caught.message
            : "Reply did not send. Your text is still here.",
        );
        return false;
      } finally {
        setThreadReplySending(false);
      }
    },
    [
      applyScoutActions,
      applyTranscriptSnapshot,
      route.params.threadId,
      sessionToken,
      threadContextRootID,
      threadReplySending,
      uploading,
    ],
  );

  const retryScoutReply = useCallback(
    async (message: ScoutMessage) => {
      const replyID = String(message.id ?? "").trim();
      if (
        !sessionToken ||
        !replyID ||
        message.reply?.state !== "failed" ||
        message.reply.retryable !== true ||
        retryingReplyID
      )
        return;
      const generationAtRequest = transcriptGenerationRef.current;
      setRetryingReplyID(replyID);
      setError(null);
      try {
        const response = await api.retryScoutReply(
          sessionToken,
          route.params.threadId,
          replyID,
        );
        applyTranscriptSnapshot(
          generationAtRequest,
          response.thread?.messages ?? response.messages ?? [],
        );
        atBottomRef.current = true;
        requestAnimationFrame(() =>
          listRef.current?.scrollToEnd({ animated: false }),
        );
      } catch (caught) {
        setError(
          caught instanceof BonfireApiError
            ? caught.message
            : "Scout could not retry that reply.",
        );
      } finally {
        setRetryingReplyID(null);
      }
    },
    [
      applyTranscriptSnapshot,
      retryingReplyID,
      route.params.threadId,
      sessionToken,
    ],
  );

  const changeProposalObjective = useCallback(
    (message: ScoutMessage, objective: string) => {
      const messageID = String(message.id ?? "").trim();
      if (!messageID) return;
      setProposalObjectives((current) => ({
        ...current,
        [messageID]: objective,
      }));
    },
    [],
  );

  const resolveProposal = useCallback(
    async (
      message: ScoutMessage,
      action: "accepted" | "dismissed",
      editedObjective: string,
    ) => {
      const messageID = String(message.id ?? "").trim();
      if (!sessionToken || !messageID || resolvingProposalID) return;
      const objective = String(editedObjective ?? "").trim();
      if (action === "accepted" && !objective) {
        setError("Add a clear objective before running this work.");
        setThreadReplyError("Add a clear objective before running this work.");
        return;
      }
      const generationAtRequest = transcriptGenerationRef.current;
      setResolvingProposalID(messageID);
      setError(null);
      setThreadReplyError("");
      try {
        const response = await api.resolveScoutProposal(
          sessionToken,
          route.params.threadId,
          messageID,
          action,
          objective,
        );
        applyTranscriptSnapshot(
          generationAtRequest,
          response.thread?.messages ?? response.messages ?? [],
        );
        applyScoutActions(response.actions);
        setProposalObjectives((current) => {
          if (!(messageID in current)) return current;
          const next = { ...current };
          delete next[messageID];
          return next;
        });
        void Haptics.notificationAsync(
          Haptics.NotificationFeedbackType.Success,
        );
      } catch (caught) {
        const detail =
          caught instanceof BonfireApiError
            ? caught.message
            : "Could not update that proposed work.";
        setError(detail);
        setThreadReplyError(detail);
      } finally {
        setResolvingProposalID(null);
      }
    },
    [
      applyScoutActions,
      applyTranscriptSnapshot,
      resolvingProposalID,
      route.params.threadId,
      sessionToken,
    ],
  );

  const saveGeneratedImage = useCallback(
    async (message: ScoutMessage) => {
      const messageID = String(message.id ?? "").trim();
      const artifactID = String(message.image?.artifactId ?? "").trim();
      if (!sessionToken || !messageID || !artifactID || savingImageID) return;
      setSavingImageID(messageID);
      setError(null);
      try {
        await api.saveArtifactToFiles(sessionToken, artifactID);
        setSavedImageIDs((current) => {
          const next = new Set(current);
          next.add(messageID);
          return next;
        });
        void Haptics.notificationAsync(
          Haptics.NotificationFeedbackType.Success,
        );
      } catch (caught) {
        setError(
          caught instanceof BonfireApiError
            ? caught.message
            : "Could not save that image to Drive.",
        );
      } finally {
        setSavingImageID(null);
      }
    },
    [savingImageID, sessionToken],
  );

  const regenerateGeneratedImage = useCallback(
    (message: ScoutMessage) => {
      const messageID = String(message.id ?? "").trim();
      const originalPrompt = String(message.image?.prompt ?? "").trim();
      if (!sessionToken || !messageID || !originalPrompt || regeneratingImageID)
        return;

      const submit = async (value?: string) => {
        const prompt = String(value ?? "").trim();
        if (!prompt) {
          setError("Image prompt is required.");
          return;
        }
        const generationAtRequest = transcriptGenerationRef.current;
        setRegeneratingImageID(messageID);
        setError(null);
        try {
          const response = await api.regenerateScoutImage(
            sessionToken,
            route.params.threadId,
            messageID,
            prompt,
          );
          applyTranscriptSnapshot(
            generationAtRequest,
            response.thread?.messages ?? response.messages ?? [],
          );
          applyScoutActions(response.actions);
          setSavedImageIDs((current) => {
            if (!current.has(messageID)) return current;
            const next = new Set(current);
            next.delete(messageID);
            return next;
          });
          void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
        } catch (caught) {
          setError(
            caught instanceof BonfireApiError
              ? caught.message
              : "Image could not be regenerated.",
          );
        } finally {
          setRegeneratingImageID(null);
        }
      };

      if (Platform.OS === "ios") {
        Alert.prompt(
          "Regenerate image",
          "Edit the prompt, then generate a replacement.",
          [
            { text: "Cancel", style: "cancel" },
            {
              text: "Regenerate",
              onPress: (value?: string) => {
                void submit(value);
              },
            },
          ],
          "plain-text",
          originalPrompt,
        );
        return;
      }
      Alert.alert(
        "Regenerate image?",
        "This will generate a replacement using the same prompt.",
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Regenerate",
            onPress: () => {
              void submit(originalPrompt);
            },
          },
        ],
      );
    },
    [
      applyScoutActions,
      applyTranscriptSnapshot,
      regeneratingImageID,
      route.params.threadId,
      sessionToken,
    ],
  );

  function openAttachmentSource(target: "message" | "reply") {
    setAttachmentTarget(target);
    setAttachmentSourceOpen(true);
  }

  const openDrivePicker = useCallback(
    (target: "message" | "reply", query = "", fromHash = false) => {
      setAttachmentTarget(target);
      setDrivePicker({ target, query, fromHash });
    },
    [],
  );

  const chooseDriveFiles = useCallback(
    async (files: DriveFileRecord[]) => {
      const intent = drivePicker;
      if (!intent || !sessionToken || uploading || files.length === 0) return;
      const currentFiles =
        intent.target === "reply" ? threadReplyFiles : pendingFiles;
      const available = Math.max(
        0,
        maxMessageAttachments - currentFiles.length,
      );
      const selected = files.slice(0, intent.fromHash ? 1 : available);
      if (selected.length === 0) {
        setDrivePicker(null);
        return;
      }
      setDrivePicker(null);
      setUploading(true);
      setError(null);
      setThreadReplyError("");
      try {
        const grants: ScoutFileAttachment[] = [];
        for (const file of selected) {
          grants.push(
            await api.attachDriveFile(
              sessionToken,
              route.params.threadId,
              file.id,
            ),
          );
        }
        const setTargetFiles =
          intent.target === "reply" ? setThreadReplyFiles : setPendingFiles;
        setTargetFiles((current) =>
          mergeExactAttachmentGrants(current, grants, maxMessageAttachments),
        );
        if (intent.fromHash) {
          const fileName = selected[0]?.name ?? grants[0]?.name;
          if (fileName) {
            if (intent.target === "reply") {
              setThreadReplyDocumentSelection({
                key: Date.now(),
                name: fileName,
              });
            } else {
              setDraft((current) =>
                completeDocumentReference(current, fileName),
              );
            }
          }
        }
        void Haptics.notificationAsync(
          Haptics.NotificationFeedbackType.Success,
        );
      } catch (caught) {
        const detail =
          caught instanceof BonfireApiError
            ? caught.message
            : caught instanceof Error
              ? caught.message
              : "Could not attach that Drive file.";
        setError(detail);
        setThreadReplyError(detail);
      } finally {
        setUploading(false);
      }
    },
    [
      drivePicker,
      pendingFiles,
      route.params.threadId,
      sessionToken,
      threadReplyFiles,
      uploading,
    ],
  );

  async function uploadAttachmentAssets(
    assets: readonly AttachmentAssetInput[],
    target: "message" | "reply",
  ): Promise<boolean> {
    if (!sessionToken || uploading) return false;
    const targetFiles = target === "reply" ? threadReplyFiles : pendingFiles;
    const remaining = maxMessageAttachments - targetFiles.length;
    const batch = prepareAttachmentBatch(assets, remaining);
    const issues = attachmentBatchMessage(batch);
    if (batch.accepted.length === 0) {
      if (issues) setError(issues);
      return false;
    }

    setUploading(true);
    setError(issues || null);
    const staging = batch.accepted.map((asset, index) => ({
      id: `${Date.now()}-${index}-${asset.name}`,
      name: asset.name,
      mime: asset.mime,
      uri: asset.uri,
    }));
    const setTargetStaging =
      target === "reply" ? setThreadReplyStagingFiles : setStagingFiles;
    const setTargetFiles =
      target === "reply" ? setThreadReplyFiles : setPendingFiles;
    setTargetStaging((current) => [...current, ...staging]);
    try {
      const outcomes: Array<{
        file: ScoutFileAttachment | null;
        error: string;
      }> = [];
      for (
        let index = 0;
        index < batch.accepted.length;
        index += maxConcurrentAttachmentUploads
      ) {
        const chunk = batch.accepted.slice(
          index,
          index + maxConcurrentAttachmentUploads,
        );
        outcomes.push(
          ...(await Promise.all(
            chunk.map(async (asset) => {
              try {
                return {
                  file: await api.uploadScoutAttachment(
                    sessionToken,
                    route.params.threadId,
                    asset,
                  ),
                  error: "",
                };
              } catch (caught) {
                return {
                  file: null,
                  error:
                    caught instanceof Error
                      ? caught.message
                      : `${asset.name} could not be attached.`,
                };
              }
            }),
          )),
        );
      }
      const uploaded = outcomes.flatMap((outcome) =>
        outcome.file ? [outcome.file] : [],
      );
      const failures = outcomes.map((outcome) => outcome.error).filter(Boolean);
      if (uploaded.length > 0) {
        setTargetFiles((current) =>
          [...current, ...uploaded].slice(0, maxMessageAttachments),
        );
        void Haptics.notificationAsync(
          Haptics.NotificationFeedbackType.Success,
        );
      }
      const message = [issues, ...failures].filter(Boolean).join(" ");
      setError(message || null);
      return uploaded.length > 0 && failures.length === 0;
    } finally {
      const stagingIDs = new Set(staging.map((file) => file.id));
      setTargetStaging((current) =>
        current.filter((file) => !stagingIDs.has(file.id)),
      );
      setUploading(false);
    }
  }

  async function pickFiles(target: "message" | "reply") {
    const targetFiles = target === "reply" ? threadReplyFiles : pendingFiles;
    if (uploading || targetFiles.length >= maxMessageAttachments) return;
    try {
      const result = await DocumentPicker.getDocumentAsync({
        type: [
          "image/png",
          "image/jpeg",
          "image/webp",
          "image/gif",
          "application/pdf",
        ],
        multiple: true,
        copyToCacheDirectory: true,
      });
      if (result.canceled) return;
      await uploadAttachmentAssets(
        result.assets.map((asset) => ({
          uri: asset.uri,
          name: asset.name,
          mime: asset.mimeType,
          size: asset.size,
        })),
        target,
      );
    } catch {
      setError("Could not open Files. Please try again.");
    }
  }

  async function pickPhotos(target: "message" | "reply") {
    const targetFiles = target === "reply" ? threadReplyFiles : pendingFiles;
    if (uploading || targetFiles.length >= maxMessageAttachments) return;
    try {
      const result = await ImagePicker.launchImageLibraryAsync({
        mediaTypes: ["images"],
        allowsMultipleSelection: true,
        selectionLimit: maxMessageAttachments - targetFiles.length,
        // Photo-library images are message previews, not archival masters. A
        // modest JPEG compression keeps staging responsive while preserving
        // enough resolution for the full-screen viewer.
        quality: 0.82,
        preferredAssetRepresentationMode:
          ImagePicker.UIImagePickerPreferredAssetRepresentationMode.Compatible,
        shouldDownloadFromNetwork: true,
      });
      if (result.canceled) return;
      await uploadAttachmentAssets(
        result.assets.map((asset, index) => ({
          uri: asset.uri,
          name: asset.fileName || `Photo ${index + 1}.jpg`,
          mime: asset.mimeType,
          size: asset.fileSize,
        })),
        target,
      );
    } catch {
      setError("Could not open your photo library. Please try again.");
    }
  }

  async function addGiphyGif(
    gif: GiphySearchResult,
    target: "message" | "reply",
  ): Promise<boolean> {
    const targetFiles = target === "reply" ? threadReplyFiles : pendingFiles;
    if (
      !sessionToken ||
      uploading ||
      targetFiles.length >= maxMessageAttachments
    )
      return false;
    setUploading(true);
    setError(null);
    const stagingID = `giphy-${gif.id}-${Date.now()}`;
    const setTargetStaging =
      target === "reply" ? setThreadReplyStagingFiles : setStagingFiles;
    const setTargetFiles =
      target === "reply" ? setThreadReplyFiles : setPendingFiles;
    setTargetStaging((current) => [
      ...current,
      {
        id: stagingID,
        name: `${gif.title?.trim() || "GIPHY"}.gif`,
        mime: "image/gif",
        uri: gif.previewUrl,
      },
    ]);
    try {
      // Let the server fetch and validate its own trusted GIPHY URL. Avoiding
      // a device download followed by a second upload makes selection feel
      // immediate and avoids holding the animation twice on mobile data.
      const attachment = await api.importGiphy(
        sessionToken,
        route.params.threadId,
        gif,
      );
      setTargetFiles((current) =>
        [...current, attachment].slice(0, maxMessageAttachments),
      );
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      return true;
    } catch (caught) {
      const message =
        caught instanceof Error ? caught.message : "Could not import that GIF.";
      setError(message);
      throw new Error(message);
    } finally {
      setTargetStaging((current) =>
        current.filter((file) => file.id !== stagingID),
      );
      setUploading(false);
    }
  }

  function beginEdit(message: ScoutMessage) {
    stopTyping();
    setActionMessage(null);
    closeThreadContext();
    setEditingMessage(message);
    setDraft(String(message.text ?? message.content ?? ""));
    setPendingFiles(Array.isArray(message.files) ? message.files : []);
    void Haptics.selectionAsync();
  }

  function cancelEdit() {
    stopTyping();
    setEditingMessage(null);
    setDraft("");
    setPendingFiles([]);
  }

  function beginReply(message: ScoutMessage) {
    openThreadContext(message);
  }

  function copyMessage(message: ScoutMessage) {
    const text = String(message.text ?? message.content ?? "");
    setActionMessage(null);
    if (!text.trim()) return;
    void Clipboard.setStringAsync(text)
      .then(() => {
        void Haptics.notificationAsync(
          Haptics.NotificationFeedbackType.Success,
        );
      })
      .catch(() => setError("Message could not be copied."));
  }

  function confirmDelete(message: ScoutMessage) {
    setActionMessage(null);
    Alert.alert(
      "Delete message?",
      "This removes it for everyone in the channel.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: () => {
            if (!sessionToken) return;
            const generationAtRequest = transcriptGenerationRef.current;
            void api
              .deleteScoutMessage(
                sessionToken,
                route.params.threadId,
                String(message.id),
              )
              .then((response) => {
                applyTranscriptSnapshot(
                  generationAtRequest,
                  response.thread?.messages ?? response.messages ?? [],
                );
                if (String(editingMessage?.id) === String(message.id))
                  cancelEdit();
                void Haptics.notificationAsync(
                  Haptics.NotificationFeedbackType.Success,
                );
              })
              .catch((caught) =>
                setError(
                  caught instanceof BonfireApiError
                    ? caught.message
                    : "Message was not deleted.",
                ),
              );
          },
        },
      ],
    );
  }

  const toggleReaction = useCallback(
    async (message: ScoutMessage, emoji: string, active: boolean) => {
      if (!sessionToken) return;
      setActionMessage(null);
      const generationAtRequest = transcriptGenerationRef.current;
      try {
        const response = await api.setScoutMessageReaction(
          sessionToken,
          route.params.threadId,
          String(message.id),
          emoji,
          active,
        );
        applyTranscriptSnapshot(
          generationAtRequest,
          response.thread?.messages ?? response.messages ?? [],
        );
        void Haptics.selectionAsync();
      } catch (caught) {
        setError(
          caught instanceof BonfireApiError
            ? caught.message
            : "Reaction was not saved.",
        );
      }
    },
    [applyTranscriptSnapshot, route.params.threadId, sessionToken],
  );

  async function changeNotificationLevel(level: ThreadNotificationLevel) {
    if (!sessionToken || notificationBusy) return;
    setNotificationBusy(true);
    try {
      const response = await api.setThreadNotificationLevel(
        sessionToken,
        route.params.threadId,
        level,
      );
      setNotificationLevel(response.level);
      setNotificationMenuOpen(false);
      void Haptics.selectionAsync();
    } catch (caught) {
      setError(
        caught instanceof BonfireApiError
          ? caught.message
          : "Notification setting was not saved.",
      );
    } finally {
      setNotificationBusy(false);
    }
  }

  function beginThreadTitleRename() {
    if (loading || threadVisibility !== "private") return;
    setThreadTitleDraft(threadTitle);
    setEditingThreadTitle(true);
    void Haptics.selectionAsync();
  }

  async function commitThreadTitleRename() {
    if (
      !sessionToken ||
      !editingThreadTitle ||
      threadTitleRenameInFlightRef.current
    )
      return;
    const title = threadTitleDraft.replace(/\s+/g, " ").trim();
    if (!title) {
      setError("A thread name cannot be empty.");
      return;
    }
    if (title === threadTitle.trim()) {
      setEditingThreadTitle(false);
      return;
    }

    threadTitleRenameInFlightRef.current = true;
    try {
      await api.updateScoutThread(sessionToken, route.params.threadId, {
        title,
      });
      setThreadTitle(title);
      setEditingThreadTitle(false);
      navigation.setParams({ title });
      Keyboard.dismiss();
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (caught) {
      setError(
        caught instanceof BonfireApiError
          ? caught.message
          : "Could not rename that thread.",
      );
    } finally {
      threadTitleRenameInFlightRef.current = false;
    }
  }

  const closeProjectCorrection = useCallback(() => {
    projectCorrectionGenerationRef.current += 1;
    projectCorrectionTargetRef.current = null;
    projectCorrectionAttemptRef.current = null;
    setProjectCorrectionTarget(null);
    setProjectCorrection(null);
    setProjectCorrectionLoading(false);
    setProjectCorrectionUpdating(false);
    setProjectCorrectionError("");
  }, []);

  const loadProjectCorrection = useCallback(async (targetOverride?: ProjectCorrectionTarget) => {
    const target = targetOverride ?? projectCorrectionTargetRef.current;
    if (!target || !sessionToken || target.sessionToken !== sessionToken || target.threadId !== route.params.threadId) return;
    const generation = ++projectCorrectionGenerationRef.current;
    setProjectCorrectionLoading(true);
    setProjectCorrectionError("");
    try {
      const response = await api.projectCorrection(sessionToken, target.threadId, target.messageId);
      if (generation !== projectCorrectionGenerationRef.current || projectCorrectionTargetRef.current !== target) return;
      if (!response.projectCorrection) throw new Error("The server did not return current Project choices.");
      setProjectCorrection(response.projectCorrection);
    } catch (caught) {
      if (generation !== projectCorrectionGenerationRef.current || projectCorrectionTargetRef.current !== target) return;
      setProjectCorrection(null);
      setProjectCorrectionError(
        caught instanceof BonfireApiError
          ? caught.message
          : caught instanceof Error
            ? caught.message
            : "Project choices could not be loaded.",
      );
    } finally {
      if (generation === projectCorrectionGenerationRef.current && projectCorrectionTargetRef.current === target) {
        setProjectCorrectionLoading(false);
      }
    }
  }, [route.params.threadId, sessionToken]);

  const openProjectCorrection = useCallback((message: ScoutMessage, returnFocusHandle?: number) => {
    const status = message.project?.status;
    if (!sessionToken || !isOwnMessageForViewer(message, {
      viewerEmail: email,
      threadVisibility,
      threadOwnerEmail,
    }) || (status !== "confirmed" && status !== "unavailable")) return;
    const target: ProjectCorrectionTarget = {
      messageId: String(message.id),
      threadId: route.params.threadId,
      sessionToken,
      returnFocusHandle,
    };
    setActionMessage(null);
    projectCorrectionAttemptRef.current = null;
    projectCorrectionTargetRef.current = target;
    setProjectCorrectionTarget(target);
    setProjectCorrection(null);
    setProjectCorrectionError("");
    void loadProjectCorrection(target);
    void Haptics.selectionAsync();
  }, [email, loadProjectCorrection, route.params.threadId, sessionToken, threadOwnerEmail, threadVisibility]);

  const submitProjectCorrection = useCallback(async (selection: { kind: "project" | "remove"; token: string; title: string }) => {
    const target = projectCorrectionTargetRef.current;
    const projection = projectCorrection;
    if (!target || !projection || !sessionToken || target.sessionToken !== sessionToken || target.threadId !== route.params.threadId || projectCorrectionUpdating) return;
    const attemptKey = `${target.threadId}:${target.messageId}:${projection.scopeKey}:${selection.token}`;
    const attempt = projectCorrectionAttemptRef.current?.key === attemptKey
      ? projectCorrectionAttemptRef.current
      : { key: attemptKey, operationId: createConversationOperationId() };
    projectCorrectionAttemptRef.current = attempt;
    const generationAtRequest = transcriptGenerationRef.current;
    setProjectCorrectionUpdating(true);
    setProjectCorrectionError("");
    try {
      const response = await api.updateProjectCorrection(sessionToken, target.threadId, target.messageId, {
        operationId: attempt.operationId,
        correctionToken: selection.token,
      });
      if (projectCorrectionTargetRef.current !== target) return;
      const nextMessages = response.thread?.messages ?? response.messages;
      if (Array.isArray(nextMessages)) {
        applyTranscriptSnapshot(generationAtRequest, nextMessages);
      } else if (response.message) {
        setMessages((current) => applyChatThreadEvent(current, target.threadId, { id: target.threadId, message: response.message }));
      }
      closeProjectCorrection();
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (caught) {
      if (projectCorrectionTargetRef.current !== target) return;
      const refreshed = caught instanceof BonfireApiError
        && caught.status === 409
        && caught.data
        && typeof caught.data === "object"
        && "projectCorrection" in caught.data
        ? (caught.data as { projectCorrection?: ProjectCorrectionProjection }).projectCorrection
        : undefined;
      if (refreshed) {
        projectCorrectionAttemptRef.current = null;
        setProjectCorrection(refreshed);
        setProjectCorrectionError("The Project changed. Review the current choice and try again.");
      } else {
        setProjectCorrectionError(
          caught instanceof BonfireApiError ? caught.message : "The Project was not updated. Try again.",
        );
      }
    } finally {
      if (projectCorrectionTargetRef.current === target) setProjectCorrectionUpdating(false);
    }
  }, [applyTranscriptSnapshot, closeProjectCorrection, projectCorrection, projectCorrectionUpdating, route.params.threadId, sessionToken]);

  const openMessageActions = useCallback(
    (
      message: ScoutMessage,
      own: boolean,
      attachment?: { file: ScoutFileAttachment; index: number },
    ) => {
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium);
      setActionMessage({ message, own, attachment });
    },
    [],
  );
  const openAttachment = useCallback((file: ScoutFileAttachment) => {
    setPreviewFile(file);
  }, []);
  const openCatchUp = useCallback(() => {
    setCatchUpOpen(true);
  }, []);
  const openLongMessage = useCallback(
    (text: string, authorName: string, scout: boolean) => {
      expandedMessageReturnFocusHandleRef.current = null;
      setExpandedMessage({ text, authorName, scout });
    },
    [],
  );
  const openWorkArtifact = useCallback(
    async (message: ScoutMessage, returnFocusHandle?: number) => {
      expandedMessageReturnFocusHandleRef.current = returnFocusHandle ?? null;
      const governedWork = message.work;
      if (["work_result", "work_record"].includes(String(message.kind ?? "").toLowerCase()) && governedWork) {
        if (!sessionToken) return;
        try {
          const response = await api.strideWorkArtifact(
            sessionToken,
            String(governedWork.artifactHref ?? ""),
          );
          const artifact = response.artifact;
          const source = String(artifact.sourceSnippet ?? "").trim();
          const approved = String(artifact.approvedOutcome ?? "").trim();
          const summary = String(artifact.summary ?? governedWork.summary ?? "").trim();
          setExpandedMessage({
            text: [summary, approved ? `Approved outcome\n${approved}` : "", source ? `Verified source\n${source}` : ""]
              .filter(Boolean)
              .join("\n\n"),
            authorName: String(artifact.title ?? governedWork.title ?? "Completed work"),
            scout: true,
            report: {
              agentName: String(governedWork.workerName ?? "Scout"),
              mode: "completed work",
              status: "Completed",
            },
          });
        } catch (caught) {
          setError(
            caught instanceof BonfireApiError
              ? caught.message
              : caught instanceof Error
                ? caught.message
                : "Could not open that governed result.",
          );
        }
        return;
      }
      const artifactId = String(message.thread?.artifactId ?? "").trim();
      const agentName =
        String(message.thread?.agentName ?? "Scout").trim() || "Scout";
      const workFamily = workFamilyLabel(message.thread);
      const status = String(message.thread?.status ?? "running").toLowerCase();
      const terminal = status === "complete" || status === "published";
      if (!sessionToken) return;
      if (!artifactId) {
        const note = String(message.thread?.progressNote ?? "").trim();
        const phase = workThreadPhase(message);
        setExpandedMessage({
          text: safeWorkProgressNote(note, phase),
          authorName: `${agentName} · ${workFamily.toLowerCase()} activity`,
          scout: true,
          activity: true,
        });
        return;
      }
      try {
        const response = await api.artifact(sessionToken, artifactId);
        const artifact = response.artifacts[0];
        const text = String(artifact?.text ?? "").trim();
        if (terminal && text) {
          const title = String(
            artifact?.metadata?.title ??
              message.thread?.query ??
              `${agentName} deliverable`,
          ).trim();
          setExpandedMessage({
            text,
            authorName: title || `${agentName} deliverable`,
            scout: true,
            report: {
              agentName,
              mode:
                workFamily,
              status: "Delivered",
            },
          });
          return;
        }
        const note = String(
          artifact?.metadata?.progressNote ??
            message.thread?.progressNote ??
            "",
        ).trim();
        const phase = workPhaseLabel({
          status,
          currentStage:
            artifact?.metadata?.currentStage ?? message.thread?.currentStage,
        });
        setExpandedMessage({
          text: safeWorkProgressNote(note, phase),
          authorName: `${agentName} · ${workFamily.toLowerCase()} activity`,
          scout: true,
          activity: true,
        });
      } catch (caught) {
        setError(
          caught instanceof BonfireApiError
            ? caught.message
            : caught instanceof Error
              ? caught.message
              : "Could not open that deliverable.",
        );
      }
    },
    [sessionToken],
  );

  const beginSaveWorkArtifact = useCallback(
    (message: ScoutMessage) => {
      if (workDriveSaveAvailability !== "available") return;
      if (!String(message.thread?.artifactId ?? "").trim()) {
        setError("This deliverable is not available to save yet.");
        return;
      }
      setWorkSaveError("");
      workSaveAttemptRef.current = null;
      setWorkSaveTarget(message);
    },
    [workDriveSaveAvailability],
  );

  const beginSaveChatAttachment = useCallback(() => {
    const target = actionMessage?.attachment;
    if (!target) return;
    setAttachmentSaveError("");
    setAttachmentSaveTarget({
      message: actionMessage.message,
      file: target.file,
      index: target.index,
    });
    setActionMessage(null);
  }, [actionMessage]);

  const saveChatAttachment = useCallback(
    async (fileName: string, folderId: string) => {
      const target = attachmentSaveTarget;
      const messageID = String(target?.message.id ?? "").trim();
      if (!target || !sessionToken || !messageID || savingAttachment) return;
      setSavingAttachment(true);
      setAttachmentSaveError("");
      try {
        const sourceFileId = `${route.params.threadId}:${messageID}:${target.index}`;
        await api.saveChatAttachmentToFiles(
          sessionToken,
          sourceFileId,
          fileName.trim(),
          folderId,
        );
        setAttachmentSaveTarget(null);
        void Haptics.notificationAsync(
          Haptics.NotificationFeedbackType.Success,
        );
      } catch (caught) {
        setAttachmentSaveError(
          caught instanceof BonfireApiError
            ? caught.message
            : "Could not save that attachment to Drive.",
        );
      } finally {
        setSavingAttachment(false);
      }
    },
    [
      attachmentSaveTarget,
      route.params.threadId,
      savingAttachment,
      sessionToken,
    ],
  );

  const saveWorkArtifact = useCallback(
    async (fileName: string, folderId: string) => {
      const target = workSaveTarget;
      const messageID = String(target?.id ?? "").trim();
      const artifactId = String(target?.thread?.artifactId ?? "").trim();
      if (!target || !sessionToken || !messageID || !artifactId || savingWorkID)
        return;
      const normalizedName = fileName.trim();
      const prior = workSaveAttemptRef.current;
      const attempt =
        prior &&
        prior.artifactId === artifactId &&
        prior.fileName === normalizedName &&
        prior.folderId === folderId
          ? prior
          : {
              artifactId,
              fileName: normalizedName,
              folderId,
              operationId: createDispositionOperationId("save"),
            };
      workSaveAttemptRef.current = attempt;
      setSavingWorkID(messageID);
      setWorkSaveError("");
      try {
        const artifact = await api.artifact(sessionToken, artifactId);
        if (!validDispositionRef(artifact.dispositionRef))
          throw new Error(
            "The deliverable authority changed. Refresh and try again.",
          );
        const response = await api.saveArtifactToDrive(sessionToken, {
          operationId: attempt.operationId,
          artifact: artifact.dispositionRef,
          folderId,
          fileName: normalizedName,
        });
        const receipt = response.receipt;
        if (
          response.ok !== true ||
          receipt?.operationId !== attempt.operationId ||
          receipt.action !== "save" ||
          receipt.outcome !== "saved" ||
          !sameDispositionRef(receipt.artifact, artifact.dispositionRef) ||
          !receipt.drive ||
          receipt.drive.id !== artifactId ||
          receipt.drive.sourceArtifactId !== artifactId ||
          receipt.drive.name !== normalizedName ||
          (receipt.drive.folderId ?? "") !== folderId ||
          !sameDispositionRef(receipt.drive.artifact, artifact.dispositionRef)
        ) {
          throw new Error("Drive did not confirm the exact save.");
        }
        setSavedWorkIDs((current) => new Set(current).add(messageID));
        setWorkSaveTarget(null);
        workSaveAttemptRef.current = null;
        void Haptics.notificationAsync(
          Haptics.NotificationFeedbackType.Success,
        );
      } catch (caught) {
        if (caught instanceof BonfireApiError && caught.status === 409)
          workSaveAttemptRef.current = null;
        setWorkSaveError(
          caught instanceof BonfireApiError
            ? caught.message
            : caught instanceof Error
              ? caught.message
              : "Could not save that deliverable.",
        );
      } finally {
        setSavingWorkID(null);
      }
    },
    [savingWorkID, sessionToken, workSaveTarget],
  );

  const beginRegenerateWorkArtifact = useCallback((message: ScoutMessage) => {
    if (!String(message.thread?.artifactId ?? "").trim()) {
      setError("This deliverable is not available to regenerate yet.");
      return;
    }
    setRegenerateWorkError("");
    setRegenerateWorkTarget(message);
  }, []);

  const regenerateWorkArtifact = useCallback(
    async (prompt: string) => {
      const target = regenerateWorkTarget;
      const messageID = String(target?.id ?? "").trim();
      const artifactId = String(target?.thread?.artifactId ?? "").trim();
      if (
        !target ||
        !sessionToken ||
        !messageID ||
        !artifactId ||
        regeneratingWorkID
      )
        return;
      setRegeneratingWorkID(messageID);
      setRegenerateWorkError("");
      try {
        await api.followUpArtifact(sessionToken, artifactId, prompt.trim());
        setRegenerateWorkTarget(null);
        void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
      } catch (caught) {
        setRegenerateWorkError(
          caught instanceof BonfireApiError
            ? caught.message
            : caught instanceof Error
              ? caught.message
              : "Could not start that regeneration.",
        );
      } finally {
        setRegeneratingWorkID(null);
      }
    },
    [regenerateWorkTarget, regeneratingWorkID, sessionToken],
  );
  const typingFooter = useMemo(
    () =>
      typingParticipants.length > 0 ? (
        <TypingIndicator participants={typingParticipants} />
      ) : null,
    [typingParticipants],
  );
  const renderThreadRow = useCallback(
    ({ item }: { item: ThreadRow }) => (
      <ThreadMessageRow
        item={item}
        sessionToken={sessionToken ?? ""}
        viewerEmail={email}
        timestampReveal={timestampReveal}
        retryingReply={retryingReplyID === String(item.message.id)}
        resolvingProposal={resolvingProposalID === String(item.message.id)}
        savingImage={savingImageID === String(item.message.id)}
        regeneratingImage={regeneratingImageID === String(item.message.id)}
        imageSaved={
          item.message.image?.savedToFiles === true ||
          savedImageIDs.has(String(item.message.id))
        }
        proposalObjective={proposalObjectives[String(item.message.id)]}
        savingWork={savingWorkID === String(item.message.id)}
        regeneratingWork={regeneratingWorkID === String(item.message.id)}
        workSaved={savedWorkIDs.has(String(item.message.id))}
        workDriveSaveAvailability={workDriveSaveAvailability}
        onOpenSource={scrollToMessage}
        onOpenAttachment={openAttachment}
        onLongPress={openMessageActions}
        onToggleReaction={toggleReaction}
        onRetryReply={retryScoutReply}
        onResolveProposal={resolveProposal}
        onChangeProposalObjective={changeProposalObjective}
        onSaveWorkArtifact={beginSaveWorkArtifact}
        onRegenerateWorkArtifact={beginRegenerateWorkArtifact}
        onSaveImage={saveGeneratedImage}
        onRegenerateImage={regenerateGeneratedImage}
        onOpenCatchUp={openCatchUp}
        onOpenLongMessage={openLongMessage}
        onOpenWorkArtifact={openWorkArtifact}
        onOpenThread={openThreadContext}
        onChangeProject={openProjectCorrection}
      />
    ),
    [
      email,
      beginRegenerateWorkArtifact,
      beginSaveWorkArtifact,
      changeProposalObjective,
      openAttachment,
      openCatchUp,
      openMessageActions,
      openLongMessage,
      openWorkArtifact,
      openThreadContext,
      openProjectCorrection,
      regenerateGeneratedImage,
      regeneratingImageID,
      regeneratingWorkID,
      retryScoutReply,
      retryingReplyID,
      resolveProposal,
      resolvingProposalID,
      saveGeneratedImage,
      savedImageIDs,
      savingImageID,
      savingWorkID,
      savedWorkIDs,
      workDriveSaveAvailability,
      proposalObjectives,
      scrollToMessage,
      sessionToken,
      timestampReveal,
      toggleReaction,
    ],
  );

  const listening = dictation.state === "listening";
  const dictationActive = dictation.state !== "idle";
  const dictationCanCommit =
    listening || dictation.state === "held" || dictation.state === "error";
  const renderMessageActionSheet = (contained = false) => (
    <MessageActionSheet
      visible={Boolean(actionMessage)}
      contained={contained}
      own={Boolean(actionMessage?.own)}
      snippet={
        actionMessage?.attachment?.file.name ??
        String(
          actionMessage?.message.text ?? actionMessage?.message.content ?? "",
        )
      }
      reactions={actionMessage?.message.reactions ?? []}
      onClose={() => setActionMessage(null)}
      onReact={(emoji) => {
        if (!actionMessage) return;
        const current = groupMessageReactions(
          actionMessage.message.reactions,
          email,
        ).find((reaction) => reaction.emoji === emoji);
        void toggleReaction(
          actionMessage.message,
          emoji,
          !current?.reactedByViewer,
        );
      }}
      onCopy={() => {
        if (actionMessage) copyMessage(actionMessage.message);
      }}
      onSaveAttachment={
        actionMessage?.attachment ? beginSaveChatAttachment : undefined
      }
      onReply={() => {
        if (actionMessage) beginReply(actionMessage.message);
      }}
      onEdit={() => {
        if (actionMessage) beginEdit(actionMessage.message);
      }}
      onDelete={() => {
        if (actionMessage) confirmDelete(actionMessage.message);
      }}
      onChangeProject={
        actionMessage?.own && actionMessage.message.project && actionMessage.message.project.status !== "removed"
          ? () => openProjectCorrection(actionMessage.message)
          : undefined
      }
      projectChangePending={actionMessage?.message.project?.status === "pending"}
    />
  );
  const renderProjectCorrectionSheet = (contained = false) => (
    <ProjectCorrectionSheet
      visible={Boolean(projectCorrectionTarget)}
      contained={contained}
      projection={projectCorrection}
      loading={projectCorrectionLoading}
      updating={projectCorrectionUpdating}
      error={projectCorrectionError}
      returnFocusHandle={projectCorrectionTarget?.returnFocusHandle}
      onClose={closeProjectCorrection}
      onReload={() => { void loadProjectCorrection(); }}
      onSubmit={(selection) => { void submitProjectCorrection(selection); }}
    />
  );
  const renderLongMessageSheet = (contained = false) => (
    <LongMessageSheet
      visible={Boolean(expandedMessage)}
      contained={contained}
      text={expandedMessage?.text ?? ""}
      authorName={expandedMessage?.authorName ?? ""}
      scout={Boolean(expandedMessage?.scout)}
      activity={Boolean(expandedMessage?.activity)}
      report={expandedMessage?.report}
      returnFocusHandle={expandedMessageReturnFocusHandleRef.current}
      onClose={() => setExpandedMessage(null)}
    />
  );

  return (
    <View style={styles.workspace}>
      {iPadWorkspace ? (
        <SafeAreaView
          accessibilityLabel="Conversations"
          style={[styles.conversationPane, { paddingTop: insets.top }]}
          edges={["left", "bottom"]}
        >
          <View style={styles.conversationPaneHeader}>
            <View style={styles.conversationPaneCopy}>
              <Text accessibilityRole="header" style={styles.conversationPaneTitle}>
                Conversations
              </Text>
              <Text style={styles.conversationPaneSubtitle}>
                Channels and private work
              </Text>
            </View>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="New conversation"
              onPress={() => navigation.navigate("NewConversation")}
              style={({ pressed }) => [
                styles.conversationPaneAction,
                pressed && styles.headerActionPressed,
              ]}
            >
              <SymbolView
                name="square.and.pencil"
                tintColor={colors.text1}
                size={18}
              />
            </Pressable>
          </View>
          <View style={styles.conversationPaneList}>
            <ChannelList
              selectedThreadId={route.params.threadId}
              onOpenThread={(thread) => {
                const threadId = String(thread.id);
                if (threadId === route.params.threadId) return;
                navigation.replace("Thread", {
                  threadId,
                  title: channelDisplayName(thread),
                });
              }}
            />
          </View>
        </SafeAreaView>
      ) : null}
    <SafeAreaView
      style={[
        styles.safe,
        iPadWorkspace && styles.threadPane,
        { paddingTop: insets.top },
      ]}
      edges={["left", "right", "bottom"]}
    >
      <View style={styles.header}>
        {iPadWorkspace ? (
          <View accessibilityElementsHidden importantForAccessibility="no" style={styles.back} />
        ) : (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Back"
            hitSlop={10}
            onPress={() => navigation.goBack()}
            style={styles.back}
          >
            <SymbolView name="chevron.left" tintColor={colors.text1} size={19} />
          </Pressable>
        )}
        {editingThreadTitle ? (
          <TextInput
            maxFontSizeMultiplier={1.6}
            accessibilityLabel="Edit thread name"
            autoFocus
            editable={!threadTitleRenameInFlightRef.current}
            enterKeyHint="done"
            onBlur={() => {
              void commitThreadTitleRename();
            }}
            onChangeText={setThreadTitleDraft}
            onSubmitEditing={() => {
              void commitThreadTitleRename();
            }}
            returnKeyType="done"
            selectTextOnFocus
            selectionColor={colors.info}
            submitBehavior="blurAndSubmit"
            style={styles.titleInput}
            value={threadTitleDraft}
          />
        ) : (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={threadTitle}
            accessibilityHint={
              threadVisibility === "private" && !loading
                ? "Touch and hold to rename this thread"
                : undefined
            }
            accessibilityActions={
              threadVisibility === "private" && !loading
                ? [{ name: "longpress", label: "Rename thread" }]
                : undefined
            }
            disabled={loading || threadVisibility !== "private"}
            onAccessibilityAction={(event) => {
              if (event.nativeEvent.actionName === "longpress")
                beginThreadTitleRename();
            }}
            onLongPress={beginThreadTitleRename}
            style={styles.titlePressable}
          >
            <Text maxFontSizeMultiplier={1.6} style={styles.title} numberOfLines={1}>
              {threadTitle}
            </Text>
            <View style={styles.titleMetaRow}>
              <SymbolView
                name={threadVisibility === "private" ? "lock.fill" : "number"}
                tintColor={colors.text3}
                size={9}
              />
              <Text maxFontSizeMultiplier={1.6} style={styles.titleMeta} numberOfLines={1}>
                {threadVisibility === "private"
                  ? "Private · Hold to rename"
                  : "Channel"}
              </Text>
            </View>
          </Pressable>
        )}
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={`Notifications: ${notificationLevel}`}
          accessibilityHint="Choose all messages, mentions only, or none"
          onPress={() => setNotificationMenuOpen(true)}
          style={({ pressed }) => [
            styles.headerAction,
            pressed && styles.headerActionPressed,
          ]}
        >
          <SymbolView
            name={
              notificationLevel === "none"
                ? "bell.slash.fill"
                : notificationLevel === "mentions"
                  ? "bell.badge.fill"
                  : "bell.fill"
            }
            tintColor={colors.text2}
            size={18}
          />
        </Pressable>
      </View>

      <CatchUpSheet
        visible={catchUpOpen}
        catchUp={digest?.catchUp ?? null}
        onClose={() => setCatchUpOpen(false)}
        onOpenMessage={(messageId) => {
          setCatchUpOpen(false);
          scrollToMessage(messageId);
        }}
      />

      <ThreadNotificationMenu
        visible={notificationMenuOpen}
        level={notificationLevel}
        busy={notificationBusy}
        onClose={() => setNotificationMenuOpen(false)}
        onChange={(level) => void changeNotificationLevel(level)}
      />

      <FilePreviewModal
        file={previewFile}
        sessionToken={sessionToken ?? ""}
        onClose={() => setPreviewFile(null)}
      />

      {threadContextRoot ? null : renderLongMessageSheet()}

      <ThreadDetailSheet
        visible={Boolean(threadContextRoot)}
        title={threadTitle}
        root={threadContextRoot}
        replies={threadContextReplies}
        viewerEmail={email}
        threadVisibility={threadVisibility}
        threadOwnerEmail={threadOwnerEmail}
        sessionToken={sessionToken ?? ""}
        participantAvatars={participantAvatars}
        mentionCandidates={participants}
        sending={threadReplySending}
        uploading={uploading}
        error={threadReplyError}
        pendingFiles={threadReplyFiles}
        stagingFiles={threadReplyStagingFiles}
        onClose={closeThreadContext}
        onSend={sendThreadReply}
        onAddAttachment={() => openAttachmentSource("reply")}
        onBrowseDrive={(query = "") => openDrivePicker("reply", query, true)}
        documentSelection={threadReplyDocumentSelection}
        onDocumentSelectionApplied={() => setThreadReplyDocumentSelection(null)}
        onRemoveAttachment={(file) =>
          setThreadReplyFiles((current) =>
            current.filter((candidate) => candidate.ref !== file.ref),
          )
        }
        onOpenAttachment={openAttachment}
        onLongPress={openMessageActions}
        onToggleReaction={toggleReaction}
        onRetryReply={retryScoutReply}
        onResolveProposal={resolveProposal}
        proposalObjectives={proposalObjectives}
        onChangeProposalObjective={changeProposalObjective}
        resolvingProposalID={resolvingProposalID}
        onOpenLongMessage={openLongMessage}
        onOpenWorkArtifact={openWorkArtifact}
        onSaveWorkArtifact={beginSaveWorkArtifact}
        onRegenerateWorkArtifact={beginRegenerateWorkArtifact}
        savingWorkID={savingWorkID}
        regeneratingWorkID={regeneratingWorkID}
        savedWorkIDs={savedWorkIDs}
        workDriveSaveAvailability={workDriveSaveAvailability}
        actionOverlay={
          <>
            {renderLongMessageSheet(true)}
            {renderMessageActionSheet(true)}
            {renderProjectCorrectionSheet(true)}
          </>
        }
      />

      <AttachmentSourceSheet
        visible={attachmentSourceOpen}
        onClose={() => setAttachmentSourceOpen(false)}
        onPhotos={() => void pickPhotos(attachmentTarget)}
        onFiles={() => void pickFiles(attachmentTarget)}
        onGifs={() => setGifPickerOpen(true)}
        onDrive={() => openDrivePicker(attachmentTarget)}
      />

      <DriveFilePickerSheet
        visible={Boolean(drivePicker)}
        sessionToken={sessionToken ?? ""}
        initialQuery={drivePicker?.query ?? ""}
        selectionMode={drivePicker?.fromHash ? "single" : "multiple"}
        maxSelection={Math.max(
          0,
          maxMessageAttachments -
            (drivePicker?.target === "reply"
              ? threadReplyFiles.length
              : pendingFiles.length),
        )}
        onClose={() => setDrivePicker(null)}
        onChoose={(files) => {
          void chooseDriveFiles(files);
        }}
      />

      <ArtifactSaveSheet
        visible={Boolean(workSaveTarget)}
        sessionToken={sessionToken ?? ""}
        defaultName={
          String(
            workSaveTarget?.thread?.resultTitle ??
              workSaveTarget?.thread?.query ??
              "Scout deliverable",
          ).trim() || "Scout deliverable"
        }
        saving={Boolean(savingWorkID)}
        error={workSaveError}
        onClose={() => {
          if (!savingWorkID) setWorkSaveTarget(null);
        }}
        onSave={(fileName, folderId) => {
          void saveWorkArtifact(fileName, folderId);
        }}
      />

      <ArtifactSaveSheet
        visible={Boolean(attachmentSaveTarget)}
        sessionToken={sessionToken ?? ""}
        defaultName={attachmentSaveTarget?.file.name ?? "Attachment"}
        saving={savingAttachment}
        error={attachmentSaveError}
        onClose={() => {
          if (!savingAttachment) setAttachmentSaveTarget(null);
        }}
        onSave={(fileName, folderId) => {
          void saveChatAttachment(fileName, folderId);
        }}
      />

      <RegenerateWorkSheet
        visible={Boolean(regenerateWorkTarget)}
        agentName={String(regenerateWorkTarget?.thread?.agentName ?? "Scout")}
        initialPrompt={String(regenerateWorkTarget?.thread?.query ?? "")}
        busy={Boolean(regeneratingWorkID)}
        error={regenerateWorkError}
        onClose={() => {
          if (!regeneratingWorkID) setRegenerateWorkTarget(null);
        }}
        onSubmit={(prompt) => {
          void regenerateWorkArtifact(prompt);
        }}
      />

      <GifPickerSheet
        visible={gifPickerOpen}
        sessionToken={sessionToken ?? ""}
        onClose={() => setGifPickerOpen(false)}
        onSelect={(gif) => addGiphyGif(gif, attachmentTarget)}
      />

      {threadContextRoot ? null : renderMessageActionSheet()}
      {threadContextRoot ? null : renderProjectCorrectionSheet()}

      <KeyboardAvoidingView
        style={styles.fill}
        contentContainerStyle={
          Platform.OS === "ios" && window.width > window.height
            ? styles.fill
            : undefined
        }
        behavior={
          Platform.OS === "ios"
            ? window.width > window.height
              ? "position"
              : "padding"
            : undefined
        }
        // This screen owns its safe-area header inside the avoiding view; an
        // additional constant offset creates an overlap after rotation and in
        // Split View. Zero keeps the composer tied to the actual keyboard.
        keyboardVerticalOffset={0}
      >
        {editingMessage ? (
          <View style={styles.messageEditor}>
            <View style={styles.messageEditorHeader}>
              <View style={styles.editingCopy}>
                <Text style={styles.messageEditorEyebrow}>Edit message</Text>
                <Text style={styles.messageEditorTitle}>Make your changes</Text>
              </View>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Cancel editing"
                onPress={cancelEdit}
                style={({ pressed }) => [
                  styles.messageEditorClose,
                  pressed && styles.headerActionPressed,
                ]}
              >
                <SymbolView name="xmark" tintColor={colors.text2} size={15} />
              </Pressable>
            </View>
            <TextInput
              accessibilityLabel="Message text"
              autoFocus
              multiline
              onChangeText={changeDraft}
              scrollEnabled
              selectionColor={colors.info}
              style={styles.messageEditorInput}
              textAlignVertical="top"
              value={draft}
            />
            <View style={styles.messageEditorActions}>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Cancel editing"
                onPress={cancelEdit}
                style={({ pressed }) => [
                  styles.messageEditorSecondary,
                  pressed && styles.headerActionPressed,
                ]}
              >
                <Text style={styles.messageEditorSecondaryText}>Cancel</Text>
              </Pressable>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Save edited message"
                disabled={
                  sending || (!draft.trim() && pendingFiles.length === 0)
                }
                onPress={() => {
                  void send();
                }}
                style={({ pressed }) => [
                  styles.messageEditorPrimary,
                  (pressed ||
                    sending ||
                    (!draft.trim() && pendingFiles.length === 0)) &&
                    styles.sendDim,
                ]}
              >
                {sending ? (
                  <ActivityIndicator color={colors.onAccent} />
                ) : (
                  <Text style={styles.messageEditorPrimaryText}>Save</Text>
                )}
              </Pressable>
            </View>
          </View>
        ) : (
          <View style={styles.fill} {...timestampPan.panHandlers}>
            {loading ? (
              <ActivityIndicator color={colors.accent} style={styles.loading} />
            ) : (
              <FlashList
                ref={listRef}
                data={rows}
                // Stable identity on the message id. Index keys would recycle
                // bubbles onto the wrong messages as the list grows.
                keyExtractor={threadRowKey}
                getItemType={threadRowRecycleType}
                maxItemsInRecyclePool={32}
                contentContainerStyle={styles.list}
                ListEmptyComponent={
                  <View
                    style={[
                      styles.emptyThread,
                      {
                        minHeight: Math.max(
                          360,
                          window.height - insets.top - insets.bottom - 250,
                        ),
                      },
                    ]}
                  >
                    <Text style={styles.emptyThreadEyebrow}>
                      {threadVisibility === "private"
                        ? "PRIVATE WORK WITH SCOUT"
                        : "SHARED CHANNEL"}
                    </Text>
                    <Text
                      accessibilityRole="header"
                      style={styles.emptyThreadTitle}
                    >
                      {threadVisibility === "private"
                        ? "What do you want to accomplish?"
                        : "Start the conversation"}
                    </Text>
                    <Text style={styles.emptyThreadBody}>
                      {threadVisibility === "private"
                        ? "Ask anything, or describe the outcome you want. Scout can answer, start private work, or ask for approval when it actually matters."
                        : "Message the team. Mention @Scout when you want help."}
                    </Text>
                    {threadVisibility === "private" ? (
                      <>
                        <View style={styles.emptyThreadStarters}>
                          {privateThreadStarters.map((starter) => (
                            <Pressable
                              key={starter.label}
                              accessibilityRole="button"
                              accessibilityLabel={starter.label}
                              accessibilityHint={`Fills the composer with: ${starter.prompt.trim()}`}
                              onPress={() => {
                                setDraft(starter.prompt);
                                void Haptics.selectionAsync();
                              }}
                              style={({ pressed }) => [
                                styles.emptyThreadStarter,
                                window.width >= 700 &&
                                  styles.emptyThreadStarterWide,
                                pressed && styles.emptyThreadStarterPressed,
                              ]}
                            >
                              <View style={styles.emptyThreadStarterHead}>
                                <Text style={styles.emptyThreadStarterLabel}>
                                  {starter.label}
                                </Text>
                                <SymbolView
                                  name="arrow.up.right"
                                  tintColor={colors.text3}
                                  size={12}
                                />
                              </View>
                              <Text style={styles.emptyThreadStarterExample}>
                                {starter.example}
                              </Text>
                            </Pressable>
                          ))}
                        </View>
                        <Text style={styles.emptyThreadDraftNote}>
                          Starts as a draft. Nothing runs until you send.
                        </Text>
                      </>
                    ) : null}
                  </View>
                }
                keyboardShouldPersistTaps="handled"
                keyboardDismissMode={Platform.OS === "ios" ? "interactive" : "on-drag"}
                maintainVisibleContentPosition={threadListPosition}
                ListFooterComponent={typingFooter}
                // FlashList lays out from the latest message immediately; onLoad
                // makes a final non-animated correction after variable-height
                // bubbles have been measured. Explicit message links override
                // this in the focused-message effect above.
                onLoad={() => {
                  if (route.params.messageId) return;
                  atBottomRef.current = true;
                  listRef.current?.scrollToEnd({ animated: false });
                  markRead();
                }}
                onScroll={(event) => {
                  const { contentOffset, contentSize, layoutMeasurement } =
                    event.nativeEvent;
                  if (!threadScrollInteractionRef.current) {
                    atBottomRef.current =
                      contentOffset.y + layoutMeasurement.height >=
                      contentSize.height - 48;
                    if (atBottomRef.current) markRead();
                  }
                }}
                onScrollBeginDrag={() => {
                  // Human upward intent wins before any socket, typing, or
                  // reconciliation update can sample the previous tail state.
                  clearThreadMomentumGrace();
                  threadScrollInteractionRef.current =
                    nextThreadScrollInteraction(
                      threadScrollInteractionRef.current,
                      "drag-begin",
                    );
                  atBottomRef.current = false;
                }}
                onMomentumScrollBegin={() => {
                  clearThreadMomentumGrace();
                  threadScrollInteractionRef.current =
                    nextThreadScrollInteraction(
                      threadScrollInteractionRef.current,
                      "momentum-begin",
                    );
                }}
                onScrollEndDrag={(event) => {
                  const { contentOffset, contentSize, layoutMeasurement } =
                    event.nativeEvent;
                  const velocityY = event.nativeEvent.velocity?.y;
                  const remainsInteracting = nextThreadScrollInteraction(
                    threadScrollInteractionRef.current,
                    "drag-end",
                    velocityY,
                  );
                  threadScrollInteractionRef.current = remainsInteracting;
                  if (!remainsInteracting) {
                    settleThreadScroll(
                      contentOffset.y,
                      contentSize.height,
                      layoutMeasurement.height,
                    );
                    return;
                  }
                  // Native momentum begins immediately when it exists. The timer
                  // releases a low/no-momentum drag whose velocity was unavailable
                  // without exposing the drag-to-momentum callback gap.
                  clearThreadMomentumGrace();
                  threadMomentumGraceTimerRef.current = setTimeout(() => {
                    settleThreadScroll(
                      contentOffset.y,
                      contentSize.height,
                      layoutMeasurement.height,
                    );
                  }, threadMomentumGraceMs);
                }}
                onMomentumScrollEnd={(event) => {
                  const { contentOffset, contentSize, layoutMeasurement } =
                    event.nativeEvent;
                  threadScrollInteractionRef.current =
                    nextThreadScrollInteraction(
                      threadScrollInteractionRef.current,
                      "momentum-end",
                    );
                  settleThreadScroll(
                    contentOffset.y,
                    contentSize.height,
                    layoutMeasurement.height,
                  );
                }}
                scrollEventThrottle={200}
                onLayout={(event) => {
                  listHeightRef.current = event.nativeEvent.layout.height;
                }}
                // A thread short enough to fit on screen has no bottom to scroll
                // to, so onScroll never fires and it would stay unread forever.
                // Fitting entirely on screen IS having read it.
                onContentSizeChange={(_width, height) => {
                  if (
                    listHeightRef.current > 0 &&
                    height <= listHeightRef.current
                  ) {
                    atBottomRef.current = true;
                    markRead();
                  }
                }}
                renderItem={renderThreadRow}
              />
            )}
          </View>
        )}

        {!editingMessage && activeWorkMessage ? (
          <Pressable
            ref={activeWorkTriggerRef}
            accessibilityRole="button"
            accessibilityLabel={`${String(activeWorkMessage.thread?.agentName ?? "Scout")}, ${workFamilyLabel(activeWorkMessage.thread)} work, ${workThreadPhase(activeWorkMessage)}${Number(activeWorkMessage.thread?.progressPercent) > 0 ? `, ${Math.round(Number(activeWorkMessage.thread?.progressPercent))}% complete` : ""}`}
            accessibilityHint="Opens current work activity"
            focusable
            onPress={() => void openWorkArtifact(activeWorkMessage, findNodeHandle(activeWorkTriggerRef.current) ?? undefined)}
            style={({ pressed }) => [
              styles.activeWork,
              workspaceLayout.stackedActivity && styles.activeWorkStacked,
              pressed && styles.activeWorkPressed,
            ]}
          >
            <View style={styles.activeWorkSignal}>
              <View style={styles.activeWorkBarShort} />
              <View style={styles.activeWorkBarTall} />
              <View style={styles.activeWorkBarMid} />
            </View>
            <Text maxFontSizeMultiplier={1.8} style={[styles.activeWorkText, workspaceLayout.stackedActivity && styles.activeWorkTextStacked]}>
              {String(activeWorkMessage.thread?.agentName ?? "Scout")} ·{" "}
              {workFamilyLabel(activeWorkMessage.thread)} ·{" "}
              {workThreadPhase(activeWorkMessage)}
            </Text>
            <Text maxFontSizeMultiplier={1.8} style={[styles.activeWorkAction, workspaceLayout.stackedActivity && styles.activeWorkActionStacked]}>View activity</Text>
          </Pressable>
        ) : null}

        {error ? <Text style={styles.error}>{error}</Text> : null}
        {dictation.error ? (
          <View style={styles.dictationError}>
            <Text style={styles.error}>{dictation.error}</Text>
            <Pressable onPress={dictation.retry} accessibilityRole="button">
              <Text style={styles.retry}>Retry</Text>
            </Pressable>
          </View>
        ) : null}

		{!editingMessage && projectContext.available ? (
		  <Pressable
			accessibilityRole="button"
			accessibilityLabel={selectedProject ? `Project: ${selectedProject.title}. Change project` : projectExplicitNone ? "No project. Change project" : "Add project"}
			accessibilityHint="Opens the authorized Project chooser. Nothing changes until you send."
			onPress={() => setProjectChooserOpen(true)}
			style={({ pressed }) => [styles.projectChip, pressed && styles.headerActionPressed]}
		  >
			<SymbolView name="link" size={14} tintColor={colors.text2} />
			<Text numberOfLines={1} maxFontSizeMultiplier={1.8} style={styles.projectChipText}>{selectedProject ? `${selectedProject.suggested ? "Suggested" : "Project"} · ${selectedProject.title}` : projectExplicitNone ? "No project" : "Add project"}</Text>
		  </Pressable>
		) : null}

		{!editingMessage ? (
          <Glass radius={radius.xl} style={styles.composer}>
            {pendingFiles.length > 0 || stagingFiles.length > 0 || uploading ? (
              <View style={styles.pendingFiles}>
                {stagingFiles.map((file) => (
                  <View
                    key={file.id}
                    style={[styles.pendingFile, styles.stagingFile]}
                  >
                    {file.mime.startsWith("image/") && file.uri ? (
                      <Image
                        source={{ uri: file.uri }}
                        contentFit="cover"
                        cachePolicy="memory-disk"
                        style={styles.stagingThumb}
                      />
                    ) : null}
                    <Text style={styles.pendingFileText} numberOfLines={1}>
                      {file.name}
                    </Text>
                    <ActivityIndicator color={colors.text2} size="small" />
                  </View>
                ))}
                {pendingFiles.map((file) => (
                  <View
                    key={`${file.ref}-${file.name}`}
                    style={styles.pendingFile}
                  >
                    <SymbolView
                      name={
                        file.mime.startsWith("image/")
                          ? "photo"
                          : "doc.richtext"
                      }
                      tintColor={colors.text2}
                      size={14}
                    />
                    <Text style={styles.pendingFileText} numberOfLines={1}>
                      {file.name}
                    </Text>
                    <Pressable
                      accessibilityRole="button"
                      accessibilityLabel={`Remove ${file.name}`}
                      onPress={() =>
                        setPendingFiles((current) =>
                          current.filter(
                            (candidate) => candidate.ref !== file.ref,
                          ),
                        )
                      }
                      style={({ pressed }) => [
                        styles.pendingRemove,
                        pressed && styles.headerActionPressed,
                      ]}
                    >
                      <SymbolView
                        name="xmark"
                        tintColor={colors.text3}
                        size={10}
                      />
                    </Pressable>
                  </View>
                ))}
              </View>
            ) : null}
            {dictationActive ? (
              <View style={styles.listening}>
                <Waveform
                  trace={dictation.trace}
                  listening={listening}
                  height={30}
                  scale={0.7}
                />
                <Text style={styles.listeningHint}>
                  {listening
                    ? "Recording · send when finished"
                    : dictation.state === "held"
                      ? "Ready to send"
                      : dictation.state === "error"
                        ? "Recording saved · try send again"
                        : "Transcribing"}
                </Text>
              </View>
            ) : (
              <MentionComposerInput
                placeholder={
                  threadTitle.length > 22
                    ? `Message ${threadTitle.slice(0, 21).trimEnd()}…`
                    : `Message ${threadTitle}`
                }
                value={draft}
                onChangeText={changeDraft}
                onDocumentQuery={(query) =>
                  openDrivePicker("message", query, true)
                }
                onBlur={() => stopTyping()}
                candidates={participants}
                editable
              />
            )}

            <View style={styles.composerActions}>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Add attachment"
                accessibilityState={{
                  disabled:
                    dictationActive ||
                    uploading ||
                    pendingFiles.length >= maxMessageAttachments,
                }}
                disabled={
                  dictationActive ||
                  uploading ||
                  pendingFiles.length >= maxMessageAttachments
                }
                onPress={() => openAttachmentSource("message")}
                style={({ pressed }) => [
                  styles.mic,
                  pressed && styles.micPressed,
                  (dictationActive ||
                    uploading ||
                    pendingFiles.length >= maxMessageAttachments) &&
                    styles.sendDim,
                ]}
              >
                <SymbolView name="plus" tintColor={colors.text2} size={20} />
              </Pressable>
              {!dictationActive ? (
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel="Dictate a message"
                  accessibilityHint="Starts dictation. Press Send once when you are finished to transcribe and post it."
                  onPress={() => {
                    void dictation.start();
                  }}
                  style={({ pressed }) => [
                    styles.mic,
                    pressed && styles.micPressed,
                  ]}
                >
                  <SymbolView
                    name="mic.fill"
                    tintColor={colors.text2}
                    size={20}
                  />
                </Pressable>
              ) : null}

              {dictationCanCommit ? (
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel="Delete dictated clip"
                  onPress={() => {
                    void dictation.discard();
                  }}
                  style={({ pressed }) => [
                    styles.mic,
                    pressed && styles.micPressed,
                  ]}
                >
                  <SymbolView name="xmark" tintColor={colors.text2} size={18} />
                </Pressable>
              ) : null}

              <Pressable
                accessibilityRole="button"
                accessibilityLabel={
                  dictationCanCommit
                    ? "Transcribe and send dictated clip"
                    : "Send"
                }
                disabled={
                  dictation.state === "transcribing" ||
                  (!dictationCanCommit &&
                    ((!draft.trim() && pendingFiles.length === 0) ||
                      sending ||
                      uploading))
                }
                onPress={() => {
                  if (dictationCanCommit) void dictation.commit();
                  else void send();
                }}
                style={({ pressed }) => [
                  styles.send,
                  (dictation.state === "transcribing" ||
                    (!dictationCanCommit &&
                      ((!draft.trim() && pendingFiles.length === 0) ||
                        sending ||
                        uploading ||
                        pressed))) &&
                    styles.sendDim,
                ]}
              >
                {sending ? (
                  <ActivityIndicator color={colors.onAccent} />
                ) : (
                  <SymbolView
                    name="arrow.up"
                    tintColor={colors.onAccent}
                    size={18}
                  />
                )}
              </Pressable>
            </View>
          </Glass>
        ) : null}
		<Modal animationType="slide" presentationStyle="pageSheet" visible={projectChooserOpen && projectContext.available} onRequestClose={() => setProjectChooserOpen(false)}>
		  <SafeAreaView style={styles.projectSheet}>
			<View style={styles.projectSheetHeader}>
			  <Text accessibilityRole="header" style={styles.projectSheetTitle}>Choose a project</Text>
			  <Pressable accessibilityRole="button" accessibilityLabel="Close project chooser" onPress={() => setProjectChooserOpen(false)} style={({ pressed }) => [styles.projectSheetClose, pressed && styles.headerActionPressed]}><SymbolView name="xmark" size={17} tintColor={colors.text1} /></Pressable>
			</View>
			<ScrollView contentContainerStyle={styles.projectChoices}>
			  {[{ title: "No project", token: "" }, ...projectContext.choices].map((choice) => {
				const selected = choice.token ? String(selectedProject?.token ?? "") === choice.token : projectExplicitNone;
				return <Pressable key={choice.token || "none"} accessibilityRole="radio" accessibilityState={{ selected }} accessibilityLabel={choice.title} onPress={() => { setSelectedProject(choice.token ? { ...choice, text: draft.trim(), threadId: route.params.threadId } : null); setProjectExplicitNone(!choice.token); setProjectChooserOpen(false); }} style={({ pressed }) => [styles.projectChoice, selected && styles.projectChoiceSelected, pressed && styles.headerActionPressed]}><Text maxFontSizeMultiplier={1.8} style={styles.projectChoiceText}>{choice.title}</Text>{selected ? <SymbolView name="checkmark" size={16} tintColor={colors.ember} /> : null}</Pressable>;
			  })}
			</ScrollView>
			<Text style={styles.projectSheetHint}>Nothing changes until you send.</Text>
		  </SafeAreaView>
		</Modal>
      </KeyboardAvoidingView>
    </SafeAreaView>
    </View>
  );
}

const styles = StyleSheet.create({
  workspace: { flex: 1, flexDirection: "row", backgroundColor: colors.bgApp },
  safe: { flex: 1, backgroundColor: colors.bgApp },
  threadPane: { minWidth: 0 },
  conversationPane: {
    width: 300,
    borderRightWidth: StyleSheet.hairlineWidth,
    borderRightColor: colors.line1,
    backgroundColor: colors.surface1,
  },
  conversationPaneHeader: {
    minHeight: 72,
    flexDirection: "row",
    alignItems: "center",
    gap: space[3],
    paddingHorizontal: space[4],
    paddingBottom: space[3],
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line1,
  },
  conversationPaneCopy: { flex: 1, minWidth: 0 },
  conversationPaneTitle: { ...type.title2, color: colors.text1 },
  conversationPaneSubtitle: {
    ...type.caption,
    marginTop: 2,
    color: colors.text3,
  },
  conversationPaneAction: {
    width: hitMin,
    height: hitMin,
    alignItems: "center",
    justifyContent: "center",
    borderRadius: radius.md,
    backgroundColor: colors.surface3,
  },
  conversationPaneList: { flex: 1, minHeight: 0, paddingHorizontal: space[2] },
  fill: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    gap: space[2],
    minHeight: 58,
    paddingHorizontal: space[3],
    paddingBottom: space[2],
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line1,
  },
  back: {
    width: hitMin,
    height: hitMin,
    borderRadius: radius.full,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "transparent",
  },
  headerAction: {
    width: hitMin,
    height: hitMin,
    alignItems: "center",
    justifyContent: "center",
    borderRadius: radius.full,
    backgroundColor: "transparent",
  },
  headerActionPressed: { opacity: 0.72, transform: [{ scale: 0.96 }] },
  title: {
    ...type.headline,
    color: colors.text1,
    maxWidth: "100%",
    textAlign: "center",
  },
  titlePressable: {
    flex: 1,
    minHeight: hitMin,
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: space[1],
  },
  titleMetaRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 4,
    maxWidth: "100%",
  },
  titleMeta: {
    ...type.caption,
    color: colors.text3,
    fontSize: 11,
    lineHeight: 14,
  },
  titleInput: {
    ...type.headline,
    color: colors.text1,
    flex: 1,
    minHeight: hitMin,
    paddingHorizontal: space[2],
    paddingVertical: 0,
    borderRadius: radius.sm,
    borderCurve: "continuous",
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.info,
    textAlign: "center",
  },
  mute: {
    width: 42,
    height: 42,
    borderRadius: 15,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
  },
  loading: { paddingVertical: space[10] },
  timelineMarker: {
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: space[4],
    paddingTop: space[4],
    paddingBottom: space[2],
  },
  timelineMarkerLabel: {
    ...type.captionMedium,
    color: colors.text3,
    fontVariant: ["tabular-nums"],
    textAlign: "center",
  },
  boundary: {
    flexDirection: "row",
    alignItems: "center",
    gap: space[3],
    paddingHorizontal: space[4],
    paddingTop: space[4],
    paddingBottom: space[2],
  },
  boundaryRule: {
    flex: 1,
    height: StyleSheet.hairlineWidth,
    backgroundColor: colors.ember,
  },
  boundaryLabel: {
    fontSize: 11,
    fontFamily: "GoogleSansFlex_600SemiBold",
    fontWeight: "600",
    letterSpacing: 0.4,
    color: colors.emberText,
    textTransform: "uppercase",
  },
  list: {
    paddingTop: space[2],
    // Clears the glass composer floating above the list bottom, so the last
    // message never sits tucked under it.
    paddingBottom: space[5],
  },
  emptyThread: {
    width: "100%",
    maxWidth: 720,
    alignSelf: "center",
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: space[5],
    paddingVertical: space[8],
  },
  emptyThreadEyebrow: {
    ...type.label,
    marginBottom: space[3],
    color: colors.text3,
    textAlign: "center",
  },
  emptyThreadTitle: {
    ...type.title1,
    maxWidth: 430,
    color: colors.text1,
    textAlign: "center",
  },
  emptyThreadBody: {
    ...type.body,
    maxWidth: 500,
    marginTop: space[3],
    color: colors.text2,
    textAlign: "center",
  },
  emptyThreadStarters: {
    width: "100%",
    maxWidth: 560,
    flexDirection: "row",
    flexWrap: "wrap",
    gap: space[2],
    marginTop: space[6],
  },
  emptyThreadStarter: {
    minHeight: 66,
    flexBasis: "100%",
    flexGrow: 1,
    justifyContent: "center",
    gap: space[1],
    paddingHorizontal: space[4],
    paddingVertical: space[3],
    borderRadius: radius.lg,
    borderCurve: "continuous",
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    backgroundColor: colors.surface2,
  },
  emptyThreadStarterWide: { flexBasis: "48%" },
  emptyThreadStarterPressed: {
    opacity: 0.78,
    transform: [{ scale: 0.98 }],
  },
  emptyThreadStarterHead: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: space[2],
  },
  emptyThreadStarterLabel: {
    ...type.captionMedium,
    flex: 1,
    color: colors.text1,
  },
  emptyThreadStarterExample: { ...type.caption, color: colors.text3 },
  emptyThreadDraftNote: {
    ...type.caption,
    marginTop: space[3],
    color: colors.text3,
    textAlign: "center",
  },
  activeWork: {
    minHeight: 44,
    flexDirection: "row",
    alignItems: "center",
    gap: space[2],
    marginHorizontal: space[4],
    marginBottom: space[2],
    paddingHorizontal: space[3],
    borderRadius: radius.lg,
    borderCurve: "continuous",
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    backgroundColor: colors.surface2,
  },
  activeWorkPressed: { opacity: 0.76, transform: [{ scale: 0.96 }] },
  activeWorkStacked: {
    minHeight: 0,
    flexDirection: "column",
    alignItems: "flex-start",
    paddingVertical: space[3],
  },
  activeWorkSignal: {
    width: 26,
    height: 26,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 2,
    borderRadius: radius.sm,
    backgroundColor: colors.emberSoft,
  },
  activeWorkBarShort: {
    width: 2,
    height: 7,
    borderRadius: radius.full,
    backgroundColor: colors.emberText,
  },
  activeWorkBarTall: {
    width: 2,
    height: 13,
    borderRadius: radius.full,
    backgroundColor: colors.emberText,
  },
  activeWorkBarMid: {
    width: 2,
    height: 9,
    borderRadius: radius.full,
    backgroundColor: colors.emberText,
  },
  activeWorkText: { ...type.captionMedium, flex: 1, color: colors.text1 },
  activeWorkTextStacked: { flex: 0, alignSelf: "stretch" },
  activeWorkAction: { ...type.captionMedium, color: colors.emberText },
  activeWorkActionStacked: { alignSelf: "stretch", textAlign: "right" },
  composer: {
    marginHorizontal: space[4],
    marginBottom: space[2],
    paddingHorizontal: space[4],
    paddingTop: space[3],
    paddingBottom: space[2],
    gap: space[2],
  },
  messageEditor: {
    ...shadow[2],
    flex: 1,
    minHeight: 220,
    marginHorizontal: space[4],
    marginTop: space[3],
    marginBottom: space[2],
    padding: space[3],
    gap: space[3],
    borderRadius: radius.xl,
    borderCurve: "continuous",
    backgroundColor: colors.surface1,
  },
  messageEditorHeader: {
    minHeight: hitMin,
    flexDirection: "row",
    alignItems: "center",
    gap: space[3],
  },
  messageEditorEyebrow: {
    ...type.captionMedium,
    color: colors.info,
    textTransform: "uppercase",
    letterSpacing: 0.4,
  },
  messageEditorTitle: { ...type.headline, color: colors.text1 },
  messageEditorClose: {
    width: hitMin,
    height: hitMin,
    alignItems: "center",
    justifyContent: "center",
    borderRadius: radius.full,
    backgroundColor: colors.surface3,
  },
  messageEditorInput: {
    ...type.body,
    flex: 1,
    minHeight: 132,
    paddingHorizontal: space[3],
    paddingVertical: space[3],
    borderRadius: radius.md,
    borderCurve: "continuous",
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
    backgroundColor: colors.surface3,
    color: colors.text1,
  },
  messageEditorActions: {
    flexDirection: "row",
    justifyContent: "flex-end",
    gap: space[2],
  },
  messageEditorSecondary: {
    minWidth: 92,
    minHeight: hitMin,
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: space[4],
    borderRadius: radius.md,
    backgroundColor: colors.surface3,
  },
  messageEditorSecondaryText: { ...type.button, color: colors.text1 },
  messageEditorPrimary: {
    minWidth: 92,
    minHeight: hitMin,
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: space[4],
    borderRadius: radius.md,
    backgroundColor: colors.accent,
  },
  messageEditorPrimaryText: { ...type.button, color: colors.onAccent },
  editingCopy: { flex: 1 },
  editingHint: { ...type.caption, color: colors.text2 },
  editingCancel: {
    width: hitMin,
    height: hitMin,
    alignItems: "center",
    justifyContent: "center",
    borderRadius: radius.full,
  },
  replyingBar: {
    minHeight: 52,
    flexDirection: "row",
    alignItems: "center",
    gap: space[3],
    marginHorizontal: space[5],
    paddingHorizontal: space[3],
  },
  replyingMark: {
    width: 3,
    alignSelf: "stretch",
    marginVertical: 8,
    borderRadius: radius.full,
    backgroundColor: colors.info,
  },
  replyingTitle: { ...type.captionMedium, color: colors.info },
  pendingFiles: {
    flexDirection: "row",
    flexWrap: "wrap",
    alignItems: "center",
    gap: 6,
  },
  pendingFile: {
    maxWidth: "100%",
    minHeight: 32,
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    paddingLeft: 9,
    paddingRight: 3,
    borderRadius: radius.full,
    backgroundColor: colors.surface3,
  },
  stagingFile: { paddingLeft: 3, paddingRight: 8, opacity: 0.86 },
  stagingThumb: {
    width: 26,
    height: 26,
    borderRadius: radius.full,
    backgroundColor: colors.surface2,
  },
  pendingFileText: { ...type.caption, maxWidth: 190, color: colors.text1 },
  pendingRemove: {
    width: 28,
    height: 28,
    alignItems: "center",
    justifyContent: "center",
    borderRadius: radius.full,
  },
  listening: {
    minHeight: 40,
    alignItems: "center",
    justifyContent: "center",
    gap: space[2],
  },
  listeningHint: {
    ...type.caption,
    color: colors.emberText,
  },
  composerActions: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "flex-end",
    gap: space[2],
  },
	projectChip: {
		minHeight: hitMin,
		maxWidth: "88%",
		alignSelf: "flex-start",
		flexDirection: "row",
		alignItems: "center",
		gap: space[2],
		marginHorizontal: space[5],
		marginBottom: space[2],
		paddingHorizontal: space[3],
		borderRadius: radius.full,
		backgroundColor: colors.surface2,
	},
	projectChipText: { ...type.captionMedium, color: colors.text2, flexShrink: 1 },
	projectSheet: { flex: 1, backgroundColor: colors.surface1, paddingHorizontal: space[5] },
	projectSheetHeader: { minHeight: 64, flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
	projectSheetTitle: { ...type.title2, color: colors.text1 },
	projectSheetClose: { width: hitMin, height: hitMin, alignItems: "center", justifyContent: "center", borderRadius: radius.full, backgroundColor: colors.surface2 },
	projectChoices: { gap: space[2], paddingBottom: space[6] },
	projectChoice: { minHeight: 52, flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: space[3], paddingHorizontal: space[4], borderRadius: radius.lg },
	projectChoiceSelected: { backgroundColor: colors.surface2 },
	projectChoiceText: { ...type.body, color: colors.text1, flexShrink: 1 },
	projectSheetHint: { ...type.caption, color: colors.text3, textAlign: "center", paddingVertical: space[4] },
  mic: {
    width: hitMin,
    height: hitMin,
    alignItems: "center",
    justifyContent: "center",
    borderRadius: radius.full,
  },
  micPressed: { backgroundColor: colors.emberSoft },
  send: {
    width: hitMin,
    height: hitMin,
    borderRadius: radius.full,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: colors.accent,
  },
  sendDim: { opacity: 0.4 },
  error: {
    ...type.bodySm,
    color: colors.danger,
    paddingHorizontal: space[5],
    paddingBottom: space[2],
  },
  dictationError: {
    flexDirection: "row",
    alignItems: "center",
    gap: space[3],
    paddingHorizontal: space[5],
  },
  retry: { ...type.button, color: colors.ember },
  catchUp: {
    flexDirection: "row",
    alignItems: "center",
    alignSelf: "center",
    gap: 6,
    paddingHorizontal: space[4],
    paddingVertical: 7,
    marginBottom: space[3],
    borderRadius: radius.full,
    backgroundColor: colors.emberSoft,
  },
  catchUpText: {
    ...type.button,
    color: colors.emberText,
  },
  pressedRow: { opacity: 0.6 },
});
