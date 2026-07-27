import { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { AppState } from 'react-native';
import {
  MediaStream,
  type MediaStreamTrack,
  RTCPeerConnection,
  RTCRtpSender,
  type RTCRtpTransceiver,
  RTCSessionDescription,
  mediaDevices,
} from 'react-native-webrtc';
import BonfireCameraFraming, {
  type CameraFramingCapabilities,
} from '../../modules/bonfire-camera-framing';
import { api } from '../api/client';
import { API_BASE_URL, NATIVE_CLIENT_HEADER } from '../config';
import {
  nextZeroOutboundVideoIntervalCount,
  summarizeNativeRoomStats,
  type NativeRoomQuality,
  type NativeRoomStatsSnapshot,
} from './quality';
import { createDisconnectedIceRestartController } from './iceRecovery';
import {
  attachLocalAudioTrackAfterPublicationCommit,
  createSerializedLocalMediaRecovery,
  detachStalledLocalVideoTracks,
  installRecoveredLocalAudioTrack,
  installRecoveredLocalVideoTrack,
  localAudioPublicationPendingState,
  setLocalAudioTracksEnabled,
} from './localAudioRecovery';
import {
  createSerializedVideoSenderMutations,
  installScreenShareTrack,
  restoreAfterScreenShare,
  screenShareMadeProgress,
  screenShareProgress,
  screenShareStopIsCurrent,
  screenShareStopShouldBegin,
} from './localScreenShare';
import { createRemoteVideoMuteController } from './remoteTrackMute';
import {
  participantEndpointMediaStatesFromSnapshot,
  participantEndpointMediaStatesSnapshotIsAuthoritative,
  participantMediaStateForEndpoint,
  participantMediaStatesFromSnapshot,
  participantVideoIsOff,
  type ParticipantEndpointMediaStates,
  type ParticipantMediaStates,
} from './participantMedia';
import {
  createRemoteVideoRecoveryState,
  nextRemoteVideoRecoveryDecision,
  nextRemoteVideoProgressState,
  remoteVideoProgressSample,
  type RemoteVideoRecoveryState,
  type RemoteVideoProgressState,
} from './remoteTrackProgress';
import {
  isServerUplinkSection,
  nativeUplinkTransceiverForSender,
  nativeUplinkAnswerDirection,
  nativeVideoUplinkCodecViolation,
  offeredRemoteVideoTrackIds,
  remoteMediaSections,
  unexpectedNativeUplinkDirectionKinds,
} from './sdp';
import { nativeH264UplinkCodecPreferences } from './nativeVideoCodec';
import {
  confirmNativeRoomAccessGranted,
  nativeRoomParticipantHello,
  type NativeRoomAdmissionContext,
} from './roomAdmission';
import {
  createNativeRoomConnectionGenerationGuard,
  createNativeRoomJoinAttemptGuard,
  settleGenerationOperation,
  settleGenerationResource,
  type PeerGeneration,
  type SocketGeneration,
} from './connectionGeneration';
import {
  endpointForTrack,
  indexParticipantTrack,
  indexParticipantTrackEndpoint,
  participantForTrack,
  reconcileRemoteParticipantEndpoints,
  reconcileRemoteParticipantRoster,
  reconcileRemoteVideoOffer,
  removeRemoteParticipantMedia,
  removeRemoteTrackIdentity,
  retainRemoteTrackIndexForFeeds,
  type ParticipantTrackMetadata,
} from './trackIdentity';
import {
  createRoomConversationState,
  roomConversationReducer,
  type RoomConversationViewer,
} from './roomConversation';
import {
  cameraFramingStateFromCapabilities,
  cameraFramingTelemetryFromCapabilities,
  createCameraFramingGenerationGuard,
  createCameraFramingOperationQueue,
  cooperativeCenterStageIntentAfterRefresh,
  emptyCameraFramingState,
  explicitFramingIntentAfterResult,
  readLiveCameraTrackIdentity,
  wideFramingRestoreDeviceId,
  wideUprightFramingNeedsUpdate,
  wideUprightIntentAfterTransition,
  type CameraFramingOperation,
  type CameraFramingState,
  type CameraFramingTrackIdentity,
} from './cameraFramingLifecycle';
import { applyNativeCameraSenderPolicy } from './videoSenderPolicy';

type RoomLifecycle = 'idle' | 'joining' | 'admitted' | 'connected' | 'reconnecting';
type ScreenShareStopReason = 'user' | 'ended' | 'cancelled' | 'start-failed' | 'stalled';

export type NativeRoomState = {
  lifecycle: RoomLifecycle;
  localStream: MediaStream | null;
  remoteVideoFeeds: Array<{ trackId: string; stream: MediaStream; participant?: string; endpointId?: string; stalled?: boolean }>;
  participants: string[];
  participantMediaStates: ParticipantMediaStates;
  participantEndpointMediaStates: ParticipantEndpointMediaStates;
  recording: boolean;
  muted: boolean;
  microphoneStarting: boolean;
  cameraOff: boolean;
  cameraStarting: boolean;
  screenSharing: boolean;
  screenShareStarting: boolean;
  screenShareStream: MediaStream | null;
  videoSuspended: boolean;
  cameraFraming: CameraFramingState;
  activeSpeaker?: string;
  quality: NativeRoomQuality | null;
  error: string | null;
};

type NativeWebSocketConstructor = new (
  uri: string,
  protocols?: string | string[] | null,
  options?: { headers: Record<string, string> },
) => WebSocket;

type SignalEnvelope = { event: string; data: string; offerId?: string; revision?: number };
type NestedEnvelope = { event?: string; data?: unknown };
type IceCandidateEventShape = { candidate: { toJSON(): unknown } | null };
type TrackEventShape = { streams: MediaStream[]; track: MediaStreamTrack | null };
type ReconnectContext = NativeRoomAdmissionContext & {
  iceServers: Array<Record<string, unknown>>;
};
type RemoteVideoTrackEntry = {
  trackId: string;
  track: MediaStreamTrack;
  stream: MediaStream;
  participant?: string;
  endpointId?: string;
};
type NativeRoomSocketContext = {
  socket: WebSocket;
  generation: SocketGeneration;
  pendingCandidates: unknown[];
  signalQueue: Promise<void>;
};
type NativeRoomPeerContext = {
  peer: RTCPeerConnection;
  generation: PeerGeneration;
  socketContext: NativeRoomSocketContext;
  pendingCandidates: unknown[];
  remoteDescriptionReady: boolean;
};
type PendingMicrophonePublicationCommit = {
  isCurrent: () => boolean;
  local: MediaStream;
  publish: () => boolean;
  resolve: (committed: boolean) => void;
  version: number;
};

const NATIVE_ROOM_CLIENT_VERSION = 'expo-native-9';
const reconnectDelaysMs = [500, 1_000, 2_000, 4_000, 8_000, 12_000];
const cameraRecoveryCooldownMs = 8_000;
const cameraFramingRestoreTimeoutMs = 750;
const screenShareStartTimeoutMs = 20_000;
const screenShareProgressPollMs = 500;
const nativeCameraConstraints = { facingMode: 'user', width: 1280, height: 720, frameRate: 30 } as const;

type MutableNativeVideoEncoding = {
  maxBitrate: number | null;
  minBitrate: number | null;
  maxFramerate: number | null;
  scaleResolutionDownBy: number | null;
};

async function configureNativeVideoSender(sender: RTCRtpSender): Promise<void> {
  const parameters = sender.getParameters();
  applyNativeCameraSenderPolicy(parameters);
  await sender.setParameters(parameters);
}

async function configureNativeScreenShareSender(sender: RTCRtpSender): Promise<void> {
  const parameters = sender.getParameters();
  parameters.encodings.forEach((rawEncoding) => {
    const encoding = rawEncoding as unknown as MutableNativeVideoEncoding;
    encoding.maxBitrate = 2_500_000;
    encoding.minBitrate = null;
    encoding.maxFramerate = 20;
    encoding.scaleResolutionDownBy = 1;
  });
  parameters.degradationPreference = 'maintain-resolution';
  await sender.setParameters(parameters);
}

function releaseNativeMediaStream(stream: MediaStream): void {
  stream.getTracks().forEach((track) => track.stop());
  stream.release();
}

function localMediaTrackIsPublishing(stream: MediaStream | null, kind: 'audio' | 'video'): boolean {
  return stream?.getTracks().some((track) => (
    track.kind === kind
    && track.readyState === 'live'
    && track.enabled
  )) ?? false;
}

function websocketURL(roomId: string): string {
  const url = new URL('/websocket', API_BASE_URL);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.searchParams.set('room', roomId);
  return url.toString();
}

function parseNestedData<T>(data: unknown, fallback: T): T {
  if (data == null) return fallback;
  if (typeof data !== 'string') return data as T;
  try {
    return JSON.parse(data) as T;
  } catch {
    return data as T;
  }
}

function normalizedParticipantName(value: unknown): string {
  return String(value ?? '').trim().toLowerCase();
}

function participantNameFromPayload(data: unknown): string {
  const payload = parseNestedData<unknown>(data, '');
  if (typeof payload === 'string') return payload.trim();
  if (!payload || typeof payload !== 'object') return '';
  return String((payload as { name?: unknown }).name ?? '').trim();
}

function trackIdentityWasRetired(trackId: string, retiredTrackIds: ReadonlySet<string>): boolean {
  if (retiredTrackIds.has(trackId)) return true;
  return String(trackId).split(':').some((segment) => retiredTrackIds.has(segment));
}

const initialState: NativeRoomState = {
  lifecycle: 'idle',
  localStream: null,
  remoteVideoFeeds: [],
  participants: [],
  participantMediaStates: {},
  participantEndpointMediaStates: {},
  recording: true,
  muted: false,
  microphoneStarting: false,
  cameraOff: false,
  cameraStarting: false,
  screenSharing: false,
  screenShareStarting: false,
  screenShareStream: null,
  videoSuspended: false,
  cameraFraming: emptyCameraFramingState(),
  quality: null,
  error: null,
};

/** Native SFU session using the same server-offer protocol as the web app. */
export function useNativeRoom(
  sessionToken: string | null,
  roomId: string,
  viewer: RoomConversationViewer = {},
) {
  const [state, setState] = useState<NativeRoomState>(initialState);
  const [microphonePublicationCommitVersion, setMicrophonePublicationCommitVersion] = useState(0);
  const [conversation, dispatchConversation] = useReducer(
    roomConversationReducer,
    roomId,
    createRoomConversationState,
  );
  const socketRef = useRef<WebSocket | null>(null);
  const socketContextRef = useRef<NativeRoomSocketContext | null>(null);
  const peerRef = useRef<RTCPeerConnection | null>(null);
  const peerContextRef = useRef<NativeRoomPeerContext | null>(null);
  const audioSenderRef = useRef<RTCRtpSender | null>(null);
  const videoSenderRef = useRef<RTCRtpSender | null>(null);
  const localRef = useRef<MediaStream | null>(null);
  const screenShareRef = useRef<MediaStream | null>(null);
  const screenShareOperationRef = useRef(0);
  const screenShareRequestedRef = useRef(false);
  const screenShareAnnouncedRef = useRef(false);
  const stopScreenShareRef = useRef<(reason?: ScreenShareStopReason) => void>(() => undefined);
  const videoSenderMutationsRef = useRef<ReturnType<typeof createSerializedVideoSenderMutations> | null>(null);
  if (!videoSenderMutationsRef.current) {
    videoSenderMutationsRef.current = createSerializedVideoSenderMutations();
  }
  const requestedAudio = useRef(true);
  const microphonePublicationOperationRef = useRef(0);
  const microphonePublicationCommitSequenceRef = useRef(0);
  const pendingMicrophonePublicationCommitRef = useRef<PendingMicrophonePublicationCommit | null>(null);
  const requestedVideo = useRef(false);
  const roomChatOpenRef = useRef(false);
  const conversationViewerRef = useRef<RoomConversationViewer>(viewer);
  conversationViewerRef.current = viewer;
  const intentionallyLeaving = useRef(false);
  const heartbeatRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const qualityTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttemptRef = useRef(0);
  const reconnectContextRef = useRef<ReconnectContext | null>(null);
  const connectSocketRef = useRef<() => void>(() => undefined);
  const recoverNativeMicrophoneRef = useRef<(reason: string) => boolean>(() => false);
  const recoverNativeCameraRef = useRef<(reason: string, bypassCooldown?: boolean) => boolean>(() => false);
  const previousQualityRef = useRef<NativeRoomStatsSnapshot | null>(null);
  const qualityStatsEpochRef = useRef(0);
  const zeroOutboundVideoIntervalsRef = useRef(0);
  const systemVideoSuspendedRef = useRef(false);
  const cameraRecoveryGuardRef = useRef<ReturnType<typeof createSerializedLocalMediaRecovery> | null>(null);
  if (!cameraRecoveryGuardRef.current) {
    cameraRecoveryGuardRef.current = createSerializedLocalMediaRecovery();
  }
  const cameraRecoveryLastAttemptAtRef = useRef(0);
  const microphoneRecoveryGuardRef = useRef<ReturnType<typeof createSerializedLocalMediaRecovery> | null>(null);
  if (!microphoneRecoveryGuardRef.current) {
    microphoneRecoveryGuardRef.current = createSerializedLocalMediaRecovery();
  }
  const appStateRef = useRef(AppState.currentState);
  const cameraFramingGenerationGuardRef = useRef<ReturnType<typeof createCameraFramingGenerationGuard> | null>(null);
  if (!cameraFramingGenerationGuardRef.current) {
    cameraFramingGenerationGuardRef.current = createCameraFramingGenerationGuard();
  }
  const cameraFramingRefreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const cameraFramingRefreshTimerEpochRef = useRef(0);
  const cameraSwitchOperationRef = useRef(0);
  const cameraFramingNativeQueueRef = useRef<ReturnType<typeof createCameraFramingOperationQueue> | null>(null);
  if (!cameraFramingNativeQueueRef.current) {
    cameraFramingNativeQueueRef.current = createCameraFramingOperationQueue();
  }
  const requestedCenterStageRef = useRef<boolean | null>(null);
  const requestedWideUprightFramingRef = useRef<boolean | null>(null);
  const wideUprightAppliedDeviceIdRef = useRef<string | null>(null);
  const cameraFramingCapabilitiesRef = useRef<{
    identity: CameraFramingTrackIdentity;
    capabilities: CameraFramingCapabilities;
  } | null>(null);
  const participantsByTrackRef = useRef<Map<string, string>>(new Map());
  const endpointsByTrackRef = useRef<Map<string, string>>(new Map());
  const participantsRef = useRef<string[]>([]);
  const participantMediaStatesRef = useRef<ParticipantMediaStates>({});
  const participantEndpointMediaStatesRef = useRef<ParticipantEndpointMediaStates>({});
  const hasParticipantSnapshotRef = useRef(false);
  const departedParticipantsRef = useRef<Set<string>>(new Set());
  const retiredRemoteTrackIdsRef = useRef<Set<string>>(new Set());
  const remoteVideoTracksRef = useRef<Map<string, RemoteVideoTrackEntry>>(new Map());
  const remoteVideoProgressRef = useRef<Map<string, RemoteVideoProgressState>>(new Map());
  const remoteVideoRecoveryRef = useRef<RemoteVideoRecoveryState>(createRemoteVideoRecoveryState());
  const remoteVideoProgressPollRef = useRef(0);
  const endpointId = useRef(`ios-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`);
  const connectionGenerationGuardRef = useRef<ReturnType<typeof createNativeRoomConnectionGenerationGuard> | null>(null);
  if (!connectionGenerationGuardRef.current) {
    connectionGenerationGuardRef.current = createNativeRoomConnectionGenerationGuard();
  }
  const joinAttemptGuardRef = useRef<ReturnType<typeof createNativeRoomJoinAttemptGuard> | null>(null);
  if (!joinAttemptGuardRef.current) {
    joinAttemptGuardRef.current = createNativeRoomJoinAttemptGuard();
  }
  const disconnectedIceRestartControllerRef = useRef<ReturnType<typeof createDisconnectedIceRestartController> | null>(null);
  if (!disconnectedIceRestartControllerRef.current) {
    disconnectedIceRestartControllerRef.current = createDisconnectedIceRestartController();
  }
  const remoteVideoMuteControllerRef = useRef<ReturnType<typeof createRemoteVideoMuteController> | null>(null);
  if (!remoteVideoMuteControllerRef.current) {
    remoteVideoMuteControllerRef.current = createRemoteVideoMuteController();
  }

  const sendOnSocket = useCallback((
    socketContext: NativeRoomSocketContext,
    event: string,
    payload: unknown = {},
    metadata: { offerId?: string; revision?: number } = {},
  ): boolean => {
    if (
      socketContextRef.current !== socketContext
      || socketRef.current !== socketContext.socket
      || !connectionGenerationGuardRef.current?.isCurrentSocket(socketContext.generation)
      || socketContext.socket.readyState !== WebSocket.OPEN
    ) {
      return false;
    }
    socketContext.socket.send(JSON.stringify({ event, data: JSON.stringify(payload), ...metadata }));
    return true;
  }, []);

  const send = useCallback((event: string, payload: unknown = {}, metadata: { offerId?: string; revision?: number } = {}): boolean => {
    const socketContext = socketContextRef.current;
    if (!socketContext) return false;
    return sendOnSocket(socketContext, event, payload, metadata);
  }, [sendOnSocket]);

  const localParticipantMediaState = useCallback((overrides: Partial<{
    micMuted: boolean;
    cameraOff: boolean;
    screenSharing: boolean;
    suspended: boolean;
    reason: string;
  }> = {}) => {
    const screenSharing = screenShareAnnouncedRef.current;
    const suspended = screenSharing ? false : systemVideoSuspendedRef.current;
    return {
      micMuted: !requestedAudio.current || !localMediaTrackIsPublishing(localRef.current, 'audio'),
      cameraOff: screenSharing
        || !requestedVideo.current
        || !localMediaTrackIsPublishing(localRef.current, 'video')
        || suspended,
      screenSharing,
      suspended,
      ...overrides,
    };
  }, []);

  const cancelMicrophonePublicationCommit = useCallback(() => {
    const pending = pendingMicrophonePublicationCommitRef.current;
    if (!pending) return false;
    pendingMicrophonePublicationCommitRef.current = null;
    pending.resolve(false);
    return true;
  }, []);

  const requestMicrophonePublicationCommit = useCallback((options: {
    isCurrent: () => boolean;
    local: MediaStream;
    publish: () => boolean;
  }): Promise<boolean> => {
    cancelMicrophonePublicationCommit();
    if (!options.isCurrent()) return Promise.resolve(false);
    const version = ++microphonePublicationCommitSequenceRef.current;
    return new Promise((resolve) => {
      pendingMicrophonePublicationCommitRef.current = {
        ...options,
        resolve,
        version,
      };
      // This update is intentionally unconditional. React may defer functional
      // updaters, so an ephemeral recovery guard must not be consulted inside it.
      setState((current) => ({
        ...current,
        localStream: options.local,
        muted: false,
        microphoneStarting: false,
        error: null,
      }));
      setMicrophonePublicationCommitVersion(version);
    });
  }, [cancelMicrophonePublicationCommit]);

  useEffect(() => {
    const pending = pendingMicrophonePublicationCommitRef.current;
    if (
      !pending
      || pending.version !== microphonePublicationCommitVersion
      || state.localStream !== pending.local
      || state.muted
      || state.microphoneStarting
    ) return;
    pendingMicrophonePublicationCommitRef.current = null;
    let committed = false;
    try {
      // Effects run only after React has committed the unmuted local state.
      // Enqueue the participant-state frame before resolving the sender barrier.
      committed = pending.isCurrent() && pending.publish();
    } catch {
      committed = false;
    }
    pending.resolve(committed);
  }, [
    microphonePublicationCommitVersion,
    state.localStream,
    state.microphoneStarting,
    state.muted,
  ]);

  const setRoomChatOpen = useCallback((open: boolean) => {
    roomChatOpenRef.current = open;
    if (open) dispatchConversation({ type: 'mark_read' });
  }, []);

  const sendRoomChat = useCallback((text: string): boolean => {
    const normalized = Array.from(String(text ?? '').trim()).slice(0, 4_000).join('').trim();
    const socketContext = socketContextRef.current;
    if (!normalized || !socketContext) return false;
    return sendOnSocket(socketContext, 'room_chat', { text: normalized });
  }, [sendOnSocket]);

  const deleteRoomChat = useCallback((id: string): boolean => {
    const messageId = String(id ?? '').trim();
    const socketContext = socketContextRef.current;
    if (!messageId || !socketContext) return false;
    return sendOnSocket(socketContext, 'room_chat_delete', { id: messageId });
  }, [sendOnSocket]);

  const isCurrentPeerContext = useCallback((peerContext: NativeRoomPeerContext): boolean => (
    peerContextRef.current === peerContext
    && peerRef.current === peerContext.peer
    && socketContextRef.current === peerContext.socketContext
    && connectionGenerationGuardRef.current?.isCurrentPeer(peerContext.generation) === true
  ), []);

  const resetQualityBaseline = useCallback(() => {
    qualityStatsEpochRef.current += 1;
    previousQualityRef.current = null;
    zeroOutboundVideoIntervalsRef.current = 0;
  }, []);

  const participantCanPublishVideo = useCallback((participant: string | undefined): boolean => {
    const normalized = normalizedParticipantName(participant);
    if (!normalized) return true;
    if (departedParticipantsRef.current.has(normalized)) return false;
    if (!hasParticipantSnapshotRef.current) return true;
    return participantsRef.current.some((name) => normalizedParticipantName(name) === normalized);
  }, []);

  const restoreRemoteVideoEntry = useCallback((
    entry: RemoteVideoTrackEntry,
    generation = peerContextRef.current?.generation,
  ) => {
    const entryIsCurrent = () => {
      const participant = participantForTrack(entry.trackId, participantsByTrackRef.current) ?? entry.participant;
      const endpointId = endpointForTrack(entry.trackId, endpointsByTrackRef.current) ?? entry.endpointId;
      const videoOff = participantVideoIsOff(
        participantMediaStatesRef.current,
        participant,
        participantEndpointMediaStatesRef.current,
        endpointId,
      );
      return Boolean(generation)
        && connectionGenerationGuardRef.current?.isCurrentPeer(generation) === true
        && remoteVideoTracksRef.current.get(entry.trackId) === entry
        && entry.track.readyState !== 'ended'
        && (videoOff || remoteVideoProgressRef.current.get(entry.trackId)?.stalled !== true)
        && (!entry.track.muted || videoOff);
    };
    if (!entryIsCurrent()) return;
    setState((current) => {
      if (!entryIsCurrent()) return current;
      const participant = participantForTrack(entry.trackId, participantsByTrackRef.current) ?? entry.participant;
      const endpointId = endpointForTrack(entry.trackId, endpointsByTrackRef.current) ?? entry.endpointId;
      if (!participantCanPublishVideo(participant)) return current;
      entry.participant = participant;
      entry.endpointId = endpointId;
      return {
        ...current,
        remoteVideoFeeds: current.remoteVideoFeeds.some((item) => item.trackId === entry.trackId)
          ? current.remoteVideoFeeds.map((item) => item.trackId === entry.trackId
            ? {
              trackId: entry.trackId,
              stream: entry.stream,
              participant: participant ?? item.participant,
              endpointId: endpointId ?? item.endpointId,
              stalled: false,
            }
            : item)
          : [...current.remoteVideoFeeds, {
            trackId: entry.trackId,
            stream: entry.stream,
            participant,
            endpointId,
            stalled: false,
          }],
      };
    });
  }, [participantCanPublishVideo]);

  const setSystemVideoSuspended = useCallback((suspended: boolean, reason: string) => {
    // ReplayKit is a separate capture path and continues while the app moves
    // between foreground/background. Camera suspension must never overwrite it.
    if (screenShareRequestedRef.current || screenShareAnnouncedRef.current) return;
    if (systemVideoSuspendedRef.current === suspended) return;
    systemVideoSuspendedRef.current = suspended;
    setState((current) => ({ ...current, videoSuspended: suspended }));
    send('participant_media_state', localParticipantMediaState({ suspended, reason }));
  }, [localParticipantMediaState, send]);

  const liveFrontCameraContext = useCallback((): {
    track: MediaStreamTrack;
    identity: CameraFramingTrackIdentity;
  } | null => {
    const track = localRef.current?.getVideoTracks()
      .find((candidate) => candidate.readyState === 'live' && candidate.enabled) ?? null;
    const identity = readLiveCameraTrackIdentity(track);
    return track && identity ? { track, identity } : null;
  }, []);

  const currentCameraFramingContext = useCallback((): {
    track: MediaStreamTrack;
    identity: CameraFramingTrackIdentity;
  } | null => {
    if (
      intentionallyLeaving.current
      || appStateRef.current !== 'active'
      || !requestedVideo.current
      || systemVideoSuspendedRef.current
      || screenShareRequestedRef.current
      || screenShareAnnouncedRef.current
    ) return null;
    return liveFrontCameraContext();
  }, [liveFrontCameraContext]);

  const runCameraFramingNativeOperation = useCallback(<T,>(operation: () => Promise<T>): Promise<T> => {
    return cameraFramingNativeQueueRef.current!.run(operation);
  }, []);

  const recordWideUprightCapability = useCallback((
    deviceId: string,
    capabilities: CameraFramingCapabilities,
  ) => {
    if (capabilities.activeDeviceId !== deviceId) return;
    if (capabilities.wideUprightFramingEnabled) {
      wideUprightAppliedDeviceIdRef.current = deviceId;
    } else if (wideUprightAppliedDeviceIdRef.current === deviceId) {
      wideUprightAppliedDeviceIdRef.current = null;
    }
  }, []);

  const restoreWideUprightFraming = useCallback((): Promise<void> | null => {
    const liveIdentity = liveFrontCameraContext()?.identity ?? null;
    // Prefer the exact device on which Bonfire most recently observed wide
    // framing. Falling back to the current exact front device makes cleanup
    // idempotent even if suspension wins before the capability result arrives.
    const targetDeviceId = wideFramingRestoreDeviceId(
      wideUprightAppliedDeviceIdRef.current,
      liveIdentity,
    );
    if (!targetDeviceId) return null;

    return runCameraFramingNativeOperation(() => (
      BonfireCameraFraming.setWideUprightFramingEnabled(false, targetDeviceId)
    )).then((result) => {
      recordWideUprightCapability(targetDeviceId, result.capabilities);
    });
  }, [liveFrontCameraContext, recordWideUprightCapability, runCameraFramingNativeOperation]);

  const resetCameraFraming = useCallback((forgetExplicitRequests = false) => {
    cameraFramingRefreshTimerEpochRef.current += 1;
    cameraSwitchOperationRef.current += 1;
    if (cameraFramingRefreshTimerRef.current) clearTimeout(cameraFramingRefreshTimerRef.current);
    cameraFramingRefreshTimerRef.current = null;
    cameraFramingGenerationGuardRef.current?.retire();
    cameraFramingCapabilitiesRef.current = null;
    if (forgetExplicitRequests) {
      requestedCenterStageRef.current = null;
      requestedWideUprightFramingRef.current = wideUprightIntentAfterTransition(
        requestedWideUprightFramingRef.current,
        'call-end',
      );
    } else {
      requestedWideUprightFramingRef.current = wideUprightIntentAfterTransition(
        requestedWideUprightFramingRef.current,
        'camera-reset',
      );
    }
    setState((current) => ({ ...current, cameraFraming: emptyCameraFramingState() }));
  }, []);

  const cameraFramingOperationIsCurrent = useCallback((operation: CameraFramingOperation): boolean => {
    const currentIdentity = currentCameraFramingContext()?.identity ?? null;
    return cameraFramingGenerationGuardRef.current?.isCurrent(operation, currentIdentity) === true;
  }, [currentCameraFramingContext]);

  const refreshCameraFramingInternal = useCallback(async (reapplyExplicitRequests: boolean): Promise<void> => {
    const context = currentCameraFramingContext();
    if (!context) {
      resetCameraFraming();
      return;
    }
    const operation = cameraFramingGenerationGuardRef.current!.begin(context.identity);
    cameraFramingCapabilitiesRef.current = null;
    setState((current) => ({
      ...current,
      // A capability query does not replace the capture track or its geometry.
      // Preserve the last confirmed dimensions so routine foreground/Center
      // Stage reconciliation cannot remount and blink the local RTC renderer.
      cameraFraming: {
        ...current.cameraFraming,
        checking: true,
        applying: false,
        pendingControl: null,
        message: null,
      },
    }));

    let capabilities = await runCameraFramingNativeOperation(() => (
      BonfireCameraFraming.getCapabilities(operation.deviceId)
    ));
    recordWideUprightCapability(operation.deviceId, capabilities);
    if (!cameraFramingOperationIsCurrent(operation)) return;

    let framingState = cameraFramingStateFromCapabilities(capabilities, operation.deviceId);
    if (!reapplyExplicitRequests && requestedCenterStageRef.current !== null) {
      // Center Stage is cooperative with Control Center. Once this call has
      // an explicit app preference, a query-only foreground refresh adopts
      // the exact current system choice instead of resurrecting stale intent
      // on a later recovery or camera switch.
      requestedCenterStageRef.current = cooperativeCenterStageIntentAfterRefresh(
        requestedCenterStageRef.current,
        framingState.centerStageSupported,
        framingState.centerStageEnabled,
      );
    }
    cameraFramingCapabilitiesRef.current = { identity: context.identity, capabilities };
    setState((current) => ({ ...current, cameraFraming: framingState }));
    if (!reapplyExplicitRequests) return;

    const requestedCenterStage = requestedCenterStageRef.current;
    const requestedWideUpright = requestedWideUprightFramingRef.current;
    const centerStageNeedsUpdate = requestedCenterStage !== null
      && framingState.centerStageSupported
      && framingState.centerStageEnabled !== requestedCenterStage;
    const wideUprightNeedsUpdate = wideUprightFramingNeedsUpdate(
      requestedWideUpright,
      framingState,
    );
    if (!centerStageNeedsUpdate && !wideUprightNeedsUpdate) return;

    setState((current) => (
      cameraFramingOperationIsCurrent(operation)
        ? {
            ...current,
            cameraFraming: {
              ...current.cameraFraming,
              applying: true,
              pendingControl: centerStageNeedsUpdate ? 'centerStage' : 'wideUpright',
              message: null,
            },
          }
        : current
    ));
    if (centerStageNeedsUpdate) {
      const result = await runCameraFramingNativeOperation(() => (
        BonfireCameraFraming.setCenterStageEnabled(requestedCenterStage, operation.deviceId)
      ));
      recordWideUprightCapability(operation.deviceId, result.capabilities);
      if (!cameraFramingOperationIsCurrent(operation)) return;
      capabilities = result.capabilities;
      framingState = cameraFramingStateFromCapabilities(capabilities, operation.deviceId);
      if (wideUprightNeedsUpdate) {
        setState((current) => (
          cameraFramingOperationIsCurrent(operation)
            ? {
                ...current,
                cameraFraming: {
                  ...current.cameraFraming,
                  pendingControl: 'wideUpright',
                },
              }
            : current
        ));
      }
    }

    const refreshedWideState = cameraFramingStateFromCapabilities(capabilities, operation.deviceId);
    if (
      requestedWideUpright !== null
      && wideUprightFramingNeedsUpdate(requestedWideUpright, refreshedWideState)
    ) {
      let result = await runCameraFramingNativeOperation(() => (
        BonfireCameraFraming.setWideUprightFramingEnabled(requestedWideUpright, operation.deviceId)
      ));
      recordWideUprightCapability(operation.deviceId, result.capabilities);
      if (!cameraFramingOperationIsCurrent(operation)) return;
      // A failed default-wide transition must not leave a cold adaptive
      // camera at its invalid 1:1 ratio. Explicit OFF establishes the
      // validated 9:16 fallback before this track is offered to the SFU.
      if (!result.ok && requestedWideUpright) {
        result = await runCameraFramingNativeOperation(() => (
          BonfireCameraFraming.setWideUprightFramingEnabled(false, operation.deviceId)
        ));
        recordWideUprightCapability(operation.deviceId, result.capabilities);
        if (!cameraFramingOperationIsCurrent(operation)) return;
      }
      capabilities = result.capabilities;
      framingState = cameraFramingStateFromCapabilities(capabilities, operation.deviceId);
    }

    if (!cameraFramingOperationIsCurrent(operation)) return;
    cameraFramingCapabilitiesRef.current = { identity: context.identity, capabilities };
    setState((current) => ({
      ...current,
      cameraFraming: {
        ...framingState,
        applying: false,
        pendingControl: null,
        message: null,
      },
    }));
  }, [
    cameraFramingOperationIsCurrent,
    currentCameraFramingContext,
    recordWideUprightCapability,
    resetCameraFraming,
    runCameraFramingNativeOperation,
  ]);

  const scheduleCameraFramingRefresh = useCallback((
    delayMs = 0,
    reapplyExplicitRequests = false,
  ) => {
    const timerEpoch = ++cameraFramingRefreshTimerEpochRef.current;
    if (cameraFramingRefreshTimerRef.current) clearTimeout(cameraFramingRefreshTimerRef.current);
    cameraFramingRefreshTimerRef.current = setTimeout(() => {
      if (cameraFramingRefreshTimerEpochRef.current !== timerEpoch) return;
      cameraFramingRefreshTimerRef.current = null;
      void refreshCameraFramingInternal(reapplyExplicitRequests);
    }, delayMs);
  }, [refreshCameraFramingInternal]);

  const refreshCameraFraming = useCallback(() => {
    void refreshCameraFramingInternal(false);
  }, [refreshCameraFramingInternal]);

  const setCenterStageEnabled = useCallback((enabled: boolean) => {
    const context = currentCameraFramingContext();
    const record = cameraFramingCapabilitiesRef.current;
    if (
      !context
      || !record
      || record.identity.trackId !== context.identity.trackId
      || record.identity.deviceId !== context.identity.deviceId
      || !cameraFramingStateFromCapabilities(record.capabilities, context.identity.deviceId)
        .centerStageSupported
    ) {
      refreshCameraFramingInternal(false);
      return;
    }

    requestedCenterStageRef.current = enabled;
    const operation = cameraFramingGenerationGuardRef.current!.begin(context.identity);
    setState((current) => ({
      ...current,
      cameraFraming: {
        ...current.cameraFraming,
        checking: false,
        applying: true,
        pendingControl: 'centerStage',
        message: null,
      },
    }));
    void (async () => {
      const result = await runCameraFramingNativeOperation(() => (
        BonfireCameraFraming.setCenterStageEnabled(enabled, operation.deviceId)
      ));
      recordWideUprightCapability(operation.deviceId, result.capabilities);
      if (!cameraFramingOperationIsCurrent(operation)) return;
      requestedCenterStageRef.current = explicitFramingIntentAfterResult(enabled, result.ok);
      const framingState = cameraFramingStateFromCapabilities(result.capabilities, operation.deviceId);
      cameraFramingCapabilitiesRef.current = {
        identity: context.identity,
        capabilities: result.capabilities,
      };
      setState((current) => ({
        ...current,
        cameraFraming: {
          ...framingState,
          applying: false,
          pendingControl: null,
          message: result.ok ? null : result.message,
        },
      }));
      if (
        result.ok
        && enabled
        && framingState.centerStageSupported
        && framingState.centerStageEnabled
        && !framingState.centerStageActive
      ) {
        // AVFoundation can report the global choice before the active capture
        // device begins producing Center Stage frames. Reconcile once after a
        // short, identity-guarded delay so the UI never calls that transient
        // state "On" indefinitely.
        scheduleCameraFramingRefresh(650, false);
      }
    })();
  }, [
    cameraFramingOperationIsCurrent,
    currentCameraFramingContext,
    recordWideUprightCapability,
    refreshCameraFramingInternal,
    runCameraFramingNativeOperation,
    scheduleCameraFramingRefresh,
  ]);

  const setWideUprightFramingEnabled = useCallback((enabled: boolean) => {
    const context = currentCameraFramingContext();
    const record = cameraFramingCapabilitiesRef.current;
    if (
      !context
      || !record
      || record.identity.trackId !== context.identity.trackId
      || record.identity.deviceId !== context.identity.deviceId
      || !cameraFramingStateFromCapabilities(record.capabilities, context.identity.deviceId)
        .wideUprightSupported
    ) {
      refreshCameraFramingInternal(false);
      return;
    }

    requestedWideUprightFramingRef.current = enabled;
    const operation = cameraFramingGenerationGuardRef.current!.begin(context.identity);
    setState((current) => ({
      ...current,
      cameraFraming: {
        ...current.cameraFraming,
        checking: false,
        applying: true,
        pendingControl: 'wideUpright',
        message: null,
      },
    }));
    void (async () => {
      const result = await runCameraFramingNativeOperation(() => (
        BonfireCameraFraming.setWideUprightFramingEnabled(enabled, operation.deviceId)
      ));
      recordWideUprightCapability(operation.deviceId, result.capabilities);
      if (!cameraFramingOperationIsCurrent(operation)) return;
      requestedWideUprightFramingRef.current = explicitFramingIntentAfterResult(enabled, result.ok);
      const framingState = cameraFramingStateFromCapabilities(result.capabilities, operation.deviceId);
      cameraFramingCapabilitiesRef.current = {
        identity: context.identity,
        capabilities: result.capabilities,
      };
      setState((current) => ({
        ...current,
        cameraFraming: {
          ...framingState,
          applying: false,
          pendingControl: null,
          message: result.ok ? null : result.message,
        },
      }));
    })();
  }, [
    cameraFramingOperationIsCurrent,
    currentCameraFramingContext,
    recordWideUprightCapability,
    refreshCameraFramingInternal,
    runCameraFramingNativeOperation,
  ]);

  const recoverNativeMicrophone = useCallback((reason: string): boolean => {
    if (
      intentionallyLeaving.current
      || appStateRef.current !== 'active'
      || !requestedAudio.current
    ) {
      return false;
    }
    const peerContext = peerContextRef.current;
    const peer = peerContext?.peer ?? null;
    // The fixed audio uplink is established from the server's recvonly m-line
    // and retained here. Never guess it from receiver.kind: every remote audio
    // downlink has the same receiver kind and can otherwise steal unmute.
    const sender = audioSenderRef.current;
    const local = localRef.current;
    if (
      !peerContext
      || !isCurrentPeerContext(peerContext)
      || !peer
      || peer.connectionState === 'closed'
      || !sender
      || !local
    ) return false;
    const { socketContext } = peerContext;
    audioSenderRef.current = sender;
    const recoveryGuard = microphoneRecoveryGuardRef.current;
    const attempt = recoveryGuard?.begin() ?? null;
    if (attempt === null) return false;
    const publicationOperation = ++microphonePublicationOperationRef.current;
    const attemptIsCurrent = () => (
      recoveryGuard?.isActive(attempt) === true
      && microphonePublicationOperationRef.current === publicationOperation
      && requestedAudio.current
      && appStateRef.current === 'active'
      && localRef.current === local
      && audioSenderRef.current === sender
      && isCurrentPeerContext(peerContext)
      && peer.connectionState !== 'closed'
      && socketContext.socket.readyState === WebSocket.OPEN
    );
    cancelMicrophonePublicationCommit();
    setLocalAudioTracksEnabled([
      ...local.getAudioTracks(),
      sender.track,
    ], false);
    if (attemptIsCurrent()) {
      setState((current) => ({ ...current, muted: true, microphoneStarting: true, error: null }));
    }

    void (async () => {
      try {
        const commitPublication = () => requestMicrophonePublicationCommit({
          isCurrent: attemptIsCurrent,
          local,
          publish: () => sendOnSocket(
            socketContext,
            'participant_media_state',
            localParticipantMediaState({ micMuted: false }),
          ),
        });
        const existingTrack = local.getAudioTracks()
          .find((track) => track.readyState === 'live');
        const outcome = existingTrack
          ? await attachLocalAudioTrackAfterPublicationCommit({
            commitPublication,
            isCurrent: attemptIsCurrent,
            local,
            sender,
            track: existingTrack,
          })
          : await (async () => {
            const capture = await mediaDevices.getUserMedia({ audio: true, video: false });
            return installRecoveredLocalAudioTrack({
              capture,
              commitPublication,
              isCurrent: attemptIsCurrent,
              local,
              sender,
            });
          })();
        if (outcome === 'installed' && attemptIsCurrent()) return;

        setLocalAudioTracksEnabled([
          ...local.getAudioTracks(),
          sender.track,
        ], false);
        if (isCurrentPeerContext(peerContext)) {
          const retrying = requestedAudio.current
            && appStateRef.current === 'active'
            && socketContext.socket.readyState === WebSocket.OPEN;
          setState((current) => ({
            ...current,
            muted: true,
            microphoneStarting: retrying,
          }));
          sendOnSocket(
            socketContext,
            'participant_media_state',
            localParticipantMediaState({ micMuted: true }),
          );
        }
      } catch (error) {
        const failureIsCurrent = attemptIsCurrent();
        cancelMicrophonePublicationCommit();
        setLocalAudioTracksEnabled([
          ...local.getAudioTracks(),
          sender.track,
        ], false);
        if (!isCurrentPeerContext(peerContext)) return;
        if (failureIsCurrent) requestedAudio.current = false;
        const technicalMessage = error instanceof Error
          ? error.message
          : 'The microphone recovery request failed.';
        const message = 'Could not start the microphone. Check iOS permissions and try again.';
        setState((current) => ({
          ...current,
          muted: true,
          microphoneStarting: false,
          error: failureIsCurrent ? message : current.error,
        }));
        sendOnSocket(
          socketContext,
          'participant_media_state',
          localParticipantMediaState({ micMuted: true }),
        );
        if (failureIsCurrent) {
          sendOnSocket(socketContext, 'media_error', {
            kind: 'microphone_recovery',
            reason,
            message: technicalMessage,
            client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION, appState: appStateRef.current },
          });
        }
      } finally {
        const retryLatestIntent = requestedAudio.current
          && appStateRef.current === 'active'
          && isCurrentPeerContext(peerContext)
          && socketContext.socket.readyState === WebSocket.OPEN
          && !localMediaTrackIsPublishing(localRef.current, 'audio');
        const disposition = recoveryGuard?.settle(attempt, retryLatestIntent) ?? 'stale';
        if (disposition !== 'stale') {
          if (disposition === 'retry') {
            setState((current) => ({ ...current, muted: true, microphoneStarting: true }));
            setTimeout(() => {
              recoverNativeMicrophoneRef.current('microphone re-enabled while its previous start was settling');
            }, 0);
          } else {
            setState((current) => (
              isCurrentPeerContext(peerContext) && current.microphoneStarting
                ? { ...current, microphoneStarting: false }
                : current
            ));
          }
        }
      }
    })();
    return true;
  }, [
    cancelMicrophonePublicationCommit,
    isCurrentPeerContext,
    localParticipantMediaState,
    requestMicrophonePublicationCommit,
    sendOnSocket,
  ]);

  const recoverNativeCamera = useCallback((reason: string, bypassCooldown = false): boolean => {
    if (
      intentionallyLeaving.current
      || appStateRef.current !== 'active'
      || !requestedVideo.current
      || screenShareRequestedRef.current
      || screenShareAnnouncedRef.current
    ) {
      return false;
    }
    const now = Date.now();
    if (!bypassCooldown && now - cameraRecoveryLastAttemptAtRef.current < cameraRecoveryCooldownMs) return false;
    const peerContext = peerContextRef.current;
    const peer = peerContext?.peer ?? null;
    const sender = videoSenderRef.current
      ?? peer?.getTransceivers().find((candidate) => candidate.receiver.track?.kind === 'video')?.sender
      ?? peer?.getSenders().find((candidate) => candidate.track?.kind === 'video')
      ?? null;
    const local = localRef.current;
    if (
      !peerContext
      || !isCurrentPeerContext(peerContext)
      || !peer
      || peer.connectionState === 'closed'
      || !sender
      || !local
    ) return false;
    const { socketContext } = peerContext;
    videoSenderRef.current = sender;
    const recoveryGuard = cameraRecoveryGuardRef.current;
    const attempt = recoveryGuard?.begin() ?? null;
    if (attempt === null) return false;
    const wideFramingRestore = restoreWideUprightFraming();
    resetCameraFraming();
    const attemptIsCurrent = () => (
      recoveryGuard?.isActive(attempt) === true
      && requestedVideo.current
      && !screenShareRequestedRef.current
      && !screenShareAnnouncedRef.current
      && appStateRef.current === 'active'
      && localRef.current === local
      && videoSenderRef.current === sender
      && isCurrentPeerContext(peerContext)
      && peer.connectionState !== 'closed'
    );

    setState((current) => (
      attemptIsCurrent()
        ? { ...current, cameraStarting: true, error: null }
        : current
    ));
    void (async () => {
      let capture: MediaStream | null = null;
      let attempted = false;
      try {
        if (wideFramingRestore) await wideFramingRestore;
        if (!attemptIsCurrent()) return;

        if (local.getVideoTracks().some((track) => track.readyState === 'live')) {
          attempted = true;
          const detached = await videoSenderMutationsRef.current!.run(sender, () => (
            detachStalledLocalVideoTracks({
              isCurrent: attemptIsCurrent,
              local,
              sender,
            })
          ));
          if (detached !== 'detached' || !attemptIsCurrent()) return;
        }

        attempted = true;
        capture = await mediaDevices.getUserMedia({ audio: false, video: nativeCameraConstraints });
        const ownedCapture = capture;
        capture = null;
        let senderConfigurationError: unknown = null;
        const outcome = await videoSenderMutationsRef.current!.run(sender, async () => {
          const installed = await installRecoveredLocalVideoTrack({
            capture: ownedCapture,
            isCurrent: attemptIsCurrent,
            local,
            sender,
          });
          if (installed !== 'installed' || !attemptIsCurrent()) return installed;
          try {
            await configureNativeVideoSender(sender);
          } catch (error) {
            senderConfigurationError = error;
          }
          return installed;
        });
        if (outcome !== 'installed' || !attemptIsCurrent()) return;
        if (senderConfigurationError) {
          sendOnSocket(socketContext, 'media_error', {
            kind: 'camera_sender_parameters',
            reason,
            message: senderConfigurationError instanceof Error
              ? senderConfigurationError.message
              : 'Could not restore camera sender settings.',
            client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION },
          });
        }
        if (!attemptIsCurrent()) return;
        systemVideoSuspendedRef.current = false;
        resetQualityBaseline();
        setState((current) => (
          attemptIsCurrent()
            ? {
              ...current,
              localStream: local,
              cameraOff: false,
              cameraStarting: false,
              videoSuspended: false,
              error: null,
            }
            : current
        ));
        sendOnSocket(
          socketContext,
          'participant_media_state',
          localParticipantMediaState({ cameraOff: false, screenSharing: false, suspended: false }),
        );
        scheduleCameraFramingRefresh(0, true);
      } catch (error) {
        if (!attemptIsCurrent()) return;
        const technicalMessage = error instanceof Error
          ? error.message
          : 'The camera recovery request failed.';
        const message = 'Could not start the camera. Check iOS permissions and try again.';
        const cameraTrackUnavailable = !local.getVideoTracks()
          .some((track) => track.readyState === 'live');
        if (cameraTrackUnavailable && requestedVideo.current) {
          requestedVideo.current = false;
          systemVideoSuspendedRef.current = false;
          setState((current) => ({
            ...current,
            cameraOff: true,
            cameraStarting: false,
            videoSuspended: false,
            error: message,
          }));
          sendOnSocket(
            socketContext,
            'participant_media_state',
            localParticipantMediaState({ cameraOff: true, screenSharing: false, suspended: false }),
          );
        } else {
          setState((current) => ({ ...current, cameraStarting: false, error: message }));
        }
        sendOnSocket(socketContext, 'media_error', {
          kind: 'camera_recovery',
          reason,
          message: technicalMessage,
          client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION, appState: appStateRef.current },
        });
      } finally {
        capture?.getTracks().forEach((track) => track.stop());
        capture?.release();
        if (attempted) cameraRecoveryLastAttemptAtRef.current = Date.now();
        const retryLatestIntent = requestedVideo.current
          && appStateRef.current === 'active'
          && !screenShareRequestedRef.current
          && !screenShareAnnouncedRef.current
          && isCurrentPeerContext(peerContext)
          && !localMediaTrackIsPublishing(localRef.current, 'video');
        const disposition = recoveryGuard?.settle(attempt, retryLatestIntent) ?? 'stale';
        if (disposition !== 'stale') {
          if (disposition === 'retry') {
            setState((current) => ({ ...current, cameraOff: false, cameraStarting: true }));
            setTimeout(() => {
              recoverNativeCameraRef.current(
                'camera re-enabled while its previous start was settling',
                true,
              );
            }, 0);
          } else {
            setState((current) => (
              isCurrentPeerContext(peerContext) && current.cameraStarting
                ? { ...current, cameraStarting: false }
                : current
            ));
          }
        }
      }
    })();
    return true;
  }, [
    isCurrentPeerContext,
    localParticipantMediaState,
    resetCameraFraming,
    resetQualityBaseline,
    restoreWideUprightFraming,
    scheduleCameraFramingRefresh,
    sendOnSocket,
  ]);

  recoverNativeMicrophoneRef.current = recoverNativeMicrophone;
  recoverNativeCameraRef.current = recoverNativeCamera;

  const disposePeer = useCallback(() => {
    // Permission prompts can outlive a peer. Retire their attempts immediately
    // so a replacement peer is free to honor the same mic/camera intent while
    // the stale capture result is safely released by its currentness guard.
    microphonePublicationOperationRef.current += 1;
    cancelMicrophonePublicationCommit();
    cameraRecoveryGuardRef.current?.retire();
    microphoneRecoveryGuardRef.current?.retire();
    disconnectedIceRestartControllerRef.current?.cancel();
    remoteVideoMuteControllerRef.current?.cancelAll();
    remoteVideoTracksRef.current = new Map();
    remoteVideoProgressRef.current = new Map();
    remoteVideoRecoveryRef.current = createRemoteVideoRecoveryState();
    remoteVideoProgressPollRef.current += 1;
    if (heartbeatRef.current) clearInterval(heartbeatRef.current);
    heartbeatRef.current = null;
    if (qualityTimerRef.current) clearInterval(qualityTimerRef.current);
    qualityTimerRef.current = null;
    resetQualityBaseline();
    participantsByTrackRef.current = new Map();
    endpointsByTrackRef.current = new Map();
    const peerContext = peerContextRef.current;
    const peer = peerContext?.peer ?? peerRef.current;
    peerContextRef.current = null;
    peerRef.current = null;
    connectionGenerationGuardRef.current?.retirePeer(peerContext?.generation);
    audioSenderRef.current = null;
    videoSenderRef.current = null;
    peer?.close();
  }, [cancelMicrophonePublicationCommit, resetQualityBaseline]);

  const disposeMedia = useCallback(() => {
    const wideFramingRestore = restoreWideUprightFraming();
    resetCameraFraming(true);
    joinAttemptGuardRef.current?.cancel();
    screenShareOperationRef.current += 1;
    screenShareRequestedRef.current = false;
    screenShareAnnouncedRef.current = false;
    const screenShare = screenShareRef.current;
    screenShareRef.current = null;
    screenShare?.getVideoTracks().forEach((track) => { track.onended = null; });
    if (screenShare) releaseNativeMediaStream(screenShare);
    if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
    reconnectTimerRef.current = null;
    reconnectAttemptRef.current = 0;
    reconnectContextRef.current = null;
    cameraRecoveryLastAttemptAtRef.current = 0;
    cameraRecoveryGuardRef.current?.retire();
    microphoneRecoveryGuardRef.current?.retire();
    systemVideoSuspendedRef.current = false;
    participantsRef.current = [];
    participantMediaStatesRef.current = {};
    participantEndpointMediaStatesRef.current = {};
    hasParticipantSnapshotRef.current = false;
    departedParticipantsRef.current = new Set();
    retiredRemoteTrackIdsRef.current = new Set();
    disposePeer();
    const local = localRef.current;
    localRef.current = null;
    if (local && !wideFramingRestore) {
      releaseNativeMediaStream(local);
    } else if (local && wideFramingRestore) {
      let released = false;
      const release = () => {
        if (released) return;
        released = true;
        releaseNativeMediaStream(local);
      };
      const timeout = setTimeout(release, cameraFramingRestoreTimeoutMs);
      void wideFramingRestore.finally(() => {
        clearTimeout(timeout);
        release();
      });
    }
  }, [disposePeer, resetCameraFraming, restoreWideUprightFraming]);

  const leave = useCallback(() => {
    intentionallyLeaving.current = true;
    const socketContext = socketContextRef.current;
    const socket = socketContext?.socket ?? socketRef.current;
    socketContextRef.current = null;
    socketRef.current = null;
    connectionGenerationGuardRef.current?.retireSocket(socketContext?.generation);
    if (socket && socket.readyState < WebSocket.CLOSING) socket.close(1000, 'left room');
    disposeMedia();
    roomChatOpenRef.current = false;
    dispatchConversation({ type: 'reset', roomId });
    setState(initialState);
  }, [disposeMedia, roomId]);

  const fail = useCallback((message: string) => {
    intentionallyLeaving.current = true;
    const socketContext = socketContextRef.current;
    const socket = socketContext?.socket ?? socketRef.current;
    socketContextRef.current = null;
    socketRef.current = null;
    connectionGenerationGuardRef.current?.retireSocket(socketContext?.generation);
    if (socket && socket.readyState < WebSocket.CLOSING) socket.close(1011, 'room error');
    disposeMedia();
    roomChatOpenRef.current = false;
    dispatchConversation({ type: 'reset', roomId });
    setState({ ...initialState, error: message });
  }, [disposeMedia, roomId]);

  const installPeer = useCallback((
    iceServers: Array<Record<string, unknown>>,
    stream: MediaStream,
    socketContext: NativeRoomSocketContext,
  ) => {
    if (
      socketContextRef.current !== socketContext
      || !connectionGenerationGuardRef.current?.isCurrentSocket(socketContext.generation)
    ) {
      return;
    }
    if (peerContextRef.current) disposePeer();
    const generation = connectionGenerationGuardRef.current?.activatePeer(socketContext.generation);
    if (!generation) return;
    disconnectedIceRestartControllerRef.current?.cancel();
    const peer = new RTCPeerConnection({
      iceServers: iceServers as never,
      bundlePolicy: 'max-bundle',
      rtcpMuxPolicy: 'require',
    });
    remoteVideoRecoveryRef.current = createRemoteVideoRecoveryState();
    const peerContext: NativeRoomPeerContext = {
      peer,
      generation,
      socketContext,
      pendingCandidates: socketContext.pendingCandidates.splice(0),
      remoteDescriptionReady: false,
    };
    peerContextRef.current = peerContext;
    peerRef.current = peer;

    peer.onicecandidate = (rawEvent: unknown) => {
      if (!isCurrentPeerContext(peerContext)) return;
      const event = rawEvent as unknown as IceCandidateEventShape;
      if (event.candidate) sendOnSocket(socketContext, 'candidate', event.candidate.toJSON());
    };
    peer.onconnectionstatechange = () => {
      if (!isCurrentPeerContext(peerContext)) return;
      const lifecycle = peer.connectionState === 'connected'
        ? 'connected'
        : peer.connectionState === 'disconnected' || peer.connectionState === 'failed'
          ? 'reconnecting'
          : null;
      if (lifecycle) {
        setState((current) => (
          isCurrentPeerContext(peerContext) ? { ...current, lifecycle } : current
        ));
      }
      disconnectedIceRestartControllerRef.current?.handleConnectionStateChange(
        peer,
        () => (isCurrentPeerContext(peerContext) ? peerRef.current : null),
        () => {
          if (isCurrentPeerContext(peerContext)) sendOnSocket(socketContext, 'restart_ice');
        },
      );
    };
    peer.ontrack = (rawEvent: unknown) => {
      if (!isCurrentPeerContext(peerContext)) return;
      const event = rawEvent as unknown as TrackEventShape;
      const track = event.track;
      if (!track || track.kind !== 'video') return;
      if (trackIdentityWasRetired(track.id, retiredRemoteTrackIdsRef.current)) return;
      // Publishers commonly use the same stream id ("-") for every SFU
      // transceiver. Keying tiles by MediaStream.id merges unrelated people;
      // a one-track stream keyed by the actual track keeps feeds independent.
      const remoteStream = new MediaStream([track]);
      const participant = participantForTrack(track.id, participantsByTrackRef.current);
      const trackEndpointId = endpointForTrack(track.id, endpointsByTrackRef.current);
      const previousEntry = remoteVideoTracksRef.current.get(track.id);
      if (previousEntry && previousEntry.track !== track) {
        remoteVideoMuteControllerRef.current?.cancel(previousEntry.track);
        remoteVideoProgressRef.current.delete(track.id);
      }
      const entry: RemoteVideoTrackEntry = {
        trackId: track.id,
        track,
        stream: remoteStream,
        participant,
        endpointId: trackEndpointId,
      };
      remoteVideoTracksRef.current.set(track.id, entry);
      const isCurrentTrack = () => (
        isCurrentPeerContext(peerContext)
        && remoteVideoTracksRef.current.get(track.id) === entry
      );
      track.onmute = () => {
        remoteVideoMuteControllerRef.current?.handleMute(track, isCurrentTrack, () => {
          if (!isCurrentTrack()) return;
          const owner = participantForTrack(track.id, participantsByTrackRef.current) ?? entry.participant;
          const ownerEndpointId = endpointForTrack(track.id, endpointsByTrackRef.current) ?? entry.endpointId;
          if (participantVideoIsOff(
            participantMediaStatesRef.current,
            owner,
            participantEndpointMediaStatesRef.current,
            ownerEndpointId,
          )) {
            restoreRemoteVideoEntry(entry, peerContext.generation);
            return;
          }
          setState((current) => (
            isCurrentTrack()
              ? {
              ...current,
              remoteVideoFeeds: current.remoteVideoFeeds.map((item) => item.trackId === track.id
                ? { ...item, stalled: true }
                : item),
              }
              : current
          ));
        });
      };
      track.onunmute = () => {
        remoteVideoMuteControllerRef.current?.handleUnmute(
          track,
          isCurrentTrack,
          () => restoreRemoteVideoEntry(entry),
        );
      };
      track.onended = () => {
        if (!isCurrentTrack()) return;
        remoteVideoMuteControllerRef.current?.cancel(track);
        remoteVideoTracksRef.current.delete(track.id);
        remoteVideoProgressRef.current.delete(track.id);
        participantsByTrackRef.current = removeRemoteTrackIdentity(participantsByTrackRef.current, track.id);
        endpointsByTrackRef.current = removeRemoteTrackIdentity(endpointsByTrackRef.current, track.id);
        setState((current) => (
          isCurrentPeerContext(peerContext)
            ? {
              ...current,
              remoteVideoFeeds: current.remoteVideoFeeds.filter((item) => item.trackId !== track.id),
            }
            : current
        ));
      };
      if (isCurrentPeerContext(peerContext)) restoreRemoteVideoEntry(entry);
    };

    qualityTimerRef.current = setInterval(() => {
      if (!isCurrentPeerContext(peerContext) || peer.connectionState === 'closed') return;
      const statsEpoch = qualityStatsEpochRef.current;
      const progressPoll = ++remoteVideoProgressPollRef.current;
      const progressEntries = [...remoteVideoTracksRef.current.values()];
      void Promise.all(progressEntries.map(async (entry) => {
        try {
          const report = await peer.getStats(entry.track) as Map<string, Record<string, unknown>>;
          return { entry, sample: remoteVideoProgressSample(report) };
        } catch {
          return null;
        }
      })).then((results) => {
        if (
          !isCurrentPeerContext(peerContext)
          || peer.connectionState === 'closed'
          || progressPoll !== remoteVideoProgressPollRef.current
        ) return;

        let requestRepair = false;
        const monitoredProgress: RemoteVideoProgressState[] = [];
        const currentTrackIds = new Set(remoteVideoTracksRef.current.keys());
        for (const trackId of remoteVideoProgressRef.current.keys()) {
          if (!currentTrackIds.has(trackId)) remoteVideoProgressRef.current.delete(trackId);
        }
        results.forEach((result) => {
          if (!result?.sample || remoteVideoTracksRef.current.get(result.entry.trackId) !== result.entry) return;
          const participant = participantForTrack(
            result.entry.trackId,
            participantsByTrackRef.current,
          ) ?? result.entry.participant;
          const trackEndpointId = endpointForTrack(
            result.entry.trackId,
            endpointsByTrackRef.current,
          ) ?? result.entry.endpointId;
          const mediaState = participantMediaStateForEndpoint(
            participantMediaStatesRef.current,
            participantEndpointMediaStatesRef.current,
            participant,
            trackEndpointId,
          );
          const shouldMonitor = peer.connectionState === 'connected'
            && Boolean(participant)
            && Boolean(mediaState)
            && !participantVideoIsOff(
              participantMediaStatesRef.current,
              participant,
              participantEndpointMediaStatesRef.current,
              trackEndpointId,
            )
            && participantCanPublishVideo(participant);
          const previousProgress = remoteVideoProgressRef.current.get(result.entry.trackId);
          const progress = nextRemoteVideoProgressState(
            previousProgress,
            result.sample,
            shouldMonitor,
          );
          remoteVideoProgressRef.current.set(result.entry.trackId, progress.state);
          if (shouldMonitor) monitoredProgress.push(progress.state);
          if (progress.becameStalled) {
            requestRepair = true;
            setState((current) => (
              isCurrentPeerContext(peerContext)
                ? {
                  ...current,
                  remoteVideoFeeds: current.remoteVideoFeeds.map((feed) => feed.trackId === result.entry.trackId
                    ? { ...feed, stalled: true }
                    : feed),
                }
                : current
            ));
            return;
          }
          if (
            shouldMonitor
            && previousProgress
            && result.sample.framesDecoded > previousProgress.framesDecoded
          ) {
            restoreRemoteVideoEntry(result.entry, peerContext.generation);
          }
        });
        if (requestRepair && isCurrentPeerContext(peerContext)) {
          sendOnSocket(socketContext, 'request_participant_tracks', {
            reason: 'native receiver stopped decoding video',
          });
        }
        const progressSamplesComplete = results.every((result) => Boolean(result?.sample));
        if (isCurrentPeerContext(peerContext) && progressSamplesComplete) {
          const recovery = nextRemoteVideoRecoveryDecision(
            remoteVideoRecoveryRef.current,
            monitoredProgress,
            Date.now(),
          );
          if (
            recovery.shouldRestartIce
            && sendOnSocket(socketContext, 'restart_ice', {
              reason: 'native receiver remained stalled after track refresh',
            })
          ) {
            remoteVideoRecoveryRef.current = recovery.state;
            try {
              peer.restartIce();
            } catch {
              // The server-side restart offer is authoritative; a missing
              // native restartIce implementation must not strand recovery.
            }
          }
        }
      }).catch(() => {
        // Receiver progress is a visual self-heal signal, never call-critical.
      });
      void peer.getStats().then((report: Map<string, Record<string, unknown>>) => {
        if (
          !isCurrentPeerContext(peerContext)
          || peer.connectionState === 'closed'
          || statsEpoch !== qualityStatsEpochRef.current
        ) return;
        const previous = previousQualityRef.current;
        const snapshot = summarizeNativeRoomStats(report, previous);
        previousQualityRef.current = snapshot;
        const screenSharePublishing = screenShareAnnouncedRef.current
          && (screenShareRef.current?.getVideoTracks()
            .some((track) => track.readyState === 'live') ?? false);
        const shouldMonitorOutboundVideo = !intentionallyLeaving.current
          && (requestedVideo.current || screenSharePublishing)
          && peer.connectionState === 'connected';
        zeroOutboundVideoIntervalsRef.current = nextZeroOutboundVideoIntervalCount(
          zeroOutboundVideoIntervalsRef.current,
          shouldMonitorOutboundVideo,
          previous !== null,
          snapshot.outboundVideoBytesDelta,
        );
        setState((current) => (
          isCurrentPeerContext(peerContext) ? { ...current, quality: snapshot } : current
        ));
        sendOnSocket(socketContext, 'media_quality', {
          sentAt: new Date(snapshot.at).toISOString(),
          laggy: snapshot.label !== 'Live',
          client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION, appState: appStateRef.current },
          cameraFraming: cameraFramingTelemetryFromCapabilities(
            cameraFramingCapabilitiesRef.current?.capabilities,
            cameraFramingCapabilitiesRef.current?.identity.deviceId,
          ),
          stats: {
            at: snapshot.at,
            inboundVideoLost: snapshot.inboundVideoPacketsLost,
            inboundVideoPacketsReceived: snapshot.inboundVideoPacketsReceived,
            inboundVideoDrops: snapshot.inboundVideoFramesDropped,
            inboundVideoDecoded: snapshot.inboundVideoFramesDecoded,
            inboundVideoJitter: snapshot.inboundVideoJitter,
            inboundVideoJitterBufferMs: snapshot.jitterBufferMs,
            outboundVideoBytesSent: snapshot.outboundVideoBytesSent,
            outboundVideoFramesEncoded: snapshot.outboundVideoFramesEncoded,
            outboundVideoFramesSent: snapshot.outboundVideoFramesSent,
            outboundVideoFrameWidth: snapshot.outboundVideoFrameWidth,
            outboundVideoFrameHeight: snapshot.outboundVideoFrameHeight,
            outboundVideoFramesPerSecond: snapshot.outboundVideoFramesPerSecond,
            outboundVideoTargetBitrate: snapshot.outboundVideoTargetBitrate,
            outboundVideoQualityLimitationReason: snapshot.outboundVideoQualityLimitationReason,
            availableOutgoingBitrate: snapshot.availableOutgoingBitrate,
            outboundRtt: snapshot.roundTripTimeMs / 1000,
            candidatePair: snapshot.candidatePair ?? {},
          },
          deltas: previous ? {
            outboundVideoBytesSent: snapshot.outboundVideoBytesDelta,
            outboundVideoFramesSent: snapshot.outboundVideoFramesSentDelta,
            inboundVideoPacketsLost: snapshot.inboundVideoPacketsLost - previous.inboundVideoPacketsLost,
            inboundVideoPacketsReceived: snapshot.inboundVideoPacketsReceived - previous.inboundVideoPacketsReceived,
            inboundVideoDecoded: snapshot.inboundVideoFramesDecoded - previous.inboundVideoFramesDecoded,
            inboundVideoDrops: snapshot.inboundVideoFramesDropped - previous.inboundVideoFramesDropped,
            inboundAudioPacketsLost: snapshot.inboundAudioPacketsLost - previous.inboundAudioPacketsLost,
            inboundAudioPacketsReceived: snapshot.inboundAudioPacketsReceived - previous.inboundAudioPacketsReceived,
            elapsedMs: snapshot.at - previous.at,
          } : {},
        });
        if (screenSharePublishing && previous && snapshot.outboundVideoBytesDelta > 0) {
          // ReplayKit is healthy. Its capture lifecycle is independent from the
          // foreground camera suspension signal.
        } else if (screenSharePublishing && zeroOutboundVideoIntervalsRef.current >= 3) {
          if (zeroOutboundVideoIntervalsRef.current === 3) {
            sendOnSocket(socketContext, 'media_error', {
              kind: 'screen_share_stalled',
              reason: 'three connected intervals without outbound screen data',
              client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION, appState: appStateRef.current },
            });
            stopScreenShareRef.current('stalled');
          }
        } else if (shouldMonitorOutboundVideo && previous && snapshot.outboundVideoBytesDelta > 0) {
          setSystemVideoSuspended(false, 'outbound camera bytes resumed');
        } else if (zeroOutboundVideoIntervalsRef.current >= 2) {
          const stalledInBackground = appStateRef.current !== 'active';
          setSystemVideoSuspended(
            true,
            stalledInBackground
              ? 'camera stopped publishing while the app was not active'
              : 'camera stopped publishing while connected',
          );
          if (!stalledInBackground) {
            recoverNativeCamera('two connected intervals without outbound camera bytes');
          }
        }
      }).catch(() => {
        // Stats are diagnostic only; never disturb live media for telemetry.
      });
    }, 4_000);

    setState((current) => (
      isCurrentPeerContext(peerContext)
        ? {
          ...current,
          lifecycle: 'admitted',
          localStream: stream,
          // installPeer never owns microphone publication. Initial join and
          // reconnect stay visibly muted until the post-offer commit barrier
          // announces and attaches the disabled local track.
          ...localAudioPublicationPendingState(requestedAudio.current),
          cameraOff: screenShareRequestedRef.current
            || screenShareAnnouncedRef.current
            || !requestedVideo.current
            || !stream.getVideoTracks().some((track) => track.readyState === 'live'),
          cameraStarting: !screenShareRequestedRef.current
            && !screenShareAnnouncedRef.current
            && requestedVideo.current
            && !stream.getVideoTracks().some((track) => track.readyState === 'live'),
          videoSuspended: systemVideoSuspendedRef.current,
        }
        : current
    ));
  }, [
    disposePeer,
    isCurrentPeerContext,
    participantCanPublishVideo,
    recoverNativeCamera,
    restoreRemoteVideoEntry,
    sendOnSocket,
    setSystemVideoSuspended,
  ]);

  const handleSignal = useCallback(async (
    envelope: SignalEnvelope,
    config: { iceServers: Array<Record<string, unknown>> },
    socketContext: NativeRoomSocketContext,
  ) => {
    const socketIsCurrent = () => (
      socketContextRef.current === socketContext
      && socketRef.current === socketContext.socket
      && connectionGenerationGuardRef.current?.isCurrentSocket(socketContext.generation) === true
    );
    const updateStateForSocket = (update: (current: NativeRoomState) => NativeRoomState) => {
      setState((current) => (socketIsCurrent() ? update(current) : current));
    };
    if (!socketIsCurrent()) return;
    if (envelope.event === 'candidate') {
      const candidate = JSON.parse(envelope.data) as unknown;
      const peerContext = peerContextRef.current;
      if (!peerContext || peerContext.socketContext !== socketContext) {
        socketContext.pendingCandidates.push(candidate);
      } else if (peerContext.remoteDescriptionReady) {
        const added = await settleGenerationOperation(
          peerContext.peer.addIceCandidate(candidate),
          () => isCurrentPeerContext(peerContext),
        );
        if (!added.current) return;
      } else {
        peerContext.pendingCandidates.push(candidate);
      }
      return;
    }
    if (envelope.event === 'offer') {
      const offer = JSON.parse(envelope.data) as { type: 'offer'; sdp: string };
      const peerContext = peerContextRef.current;
      if (!peerContext || peerContext.socketContext !== socketContext || !isCurrentPeerContext(peerContext)) return;
      const peer = peerContext.peer;
      const remoteDescription = await settleGenerationOperation(
        peer.setRemoteDescription(new RTCSessionDescription(offer)),
        () => isCurrentPeerContext(peerContext),
      );
      if (!remoteDescription.current) return;
      const sections = remoteMediaSections(offer.sdp);
      const offeredVideoTrackIds = offeredRemoteVideoTrackIds(sections);
      if (offeredVideoTrackIds) {
        const trackIndex = participantsByTrackRef.current;
        const currentEntries = [...remoteVideoTracksRef.current.values()];
        const retainedEntries = reconcileRemoteVideoOffer(
          currentEntries,
          trackIndex,
          offeredVideoTrackIds,
        ).feeds;
        const retainedEntrySet = new Set(retainedEntries);
        currentEntries.forEach((entry) => {
          if (!retainedEntrySet.has(entry)) {
            retiredRemoteTrackIdsRef.current.add(entry.trackId);
            remoteVideoMuteControllerRef.current?.cancel(entry.track);
            remoteVideoProgressRef.current.delete(entry.trackId);
          }
        });
        remoteVideoTracksRef.current = new Map(retainedEntries.map((entry) => [entry.trackId, entry]));
        const retainedTrackIndex = reconcileRemoteVideoOffer([], trackIndex, offeredVideoTrackIds).participantsByTrack;
        trackIndex.forEach((_, trackId) => {
          if (!retainedTrackIndex.has(trackId)) retiredRemoteTrackIdsRef.current.add(trackId);
        });
        participantsByTrackRef.current = retainedTrackIndex;
        endpointsByTrackRef.current = retainRemoteTrackIndexForFeeds(
          endpointsByTrackRef.current,
          retainedEntries,
        );
        setState((current) => {
          if (!isCurrentPeerContext(peerContext)) return current;
          const reconciled = reconcileRemoteVideoOffer(current.remoteVideoFeeds, trackIndex, offeredVideoTrackIds);
          if (reconciled.feeds.length === current.remoteVideoFeeds.length) return current;
          return { ...current, remoteVideoFeeds: reconciled.feeds };
        });
        if (!isCurrentPeerContext(peerContext)) return;
        retainedEntries.forEach((entry) => {
          if (isCurrentPeerContext(peerContext)) restoreRemoteVideoEntry(entry);
        });
      }
      const local = localRef.current;
      const microphoneRequestedForOffer = requestedAudio.current;
      const uplinkTransceivers = new Map<'audio' | 'video', RTCRtpTransceiver>();
      for (const transceiver of peer.getTransceivers()) {
        if (!isCurrentPeerContext(peerContext)) return;
        const section = sections.get(transceiver.mid ?? '');
        if (!isServerUplinkSection(section) || !local) continue;
        // Lock the fixed publication slot before applying the camera-uplink
        // codec envelope. The selected H.264 capabilities are shared by the
        // installed iOS sender/receiver factories, so the synchronous native
        // preference call remains valid while the direction mutation settles.
        transceiver.direction = nativeUplinkAnswerDirection(
          section.kind as 'audio' | 'video',
          microphoneRequestedForOffer,
        );
        if (section.kind === 'audio') {
          audioSenderRef.current = transceiver.sender;
          uplinkTransceivers.set('audio', transceiver);
          // Offers only negotiate the slot. Publication is a separate phase
          // after the answer: keep every local/sender track disabled and do not
          // attach one while the UI or participant state can still say muted.
          setLocalAudioTracksEnabled([
            ...local.getAudioTracks(),
            transceiver.sender.track,
          ], false);
          continue;
        }
        if (section.kind === 'video') {
          const codecPreferences = nativeH264UplinkCodecPreferences(
            RTCRtpSender.getCapabilities('video').codecs,
          );
          transceiver.setCodecPreferences(codecPreferences);
          videoSenderRef.current = transceiver.sender;
          uplinkTransceivers.set('video', transceiver);
        }
        // Negotiate both publication slots even when a quiet join has no
        // capture tracks yet. replaceTrack can then start mic/camera without
        // tearing down the call or asking the server for a new offer.
        const screenTrack = section.kind === 'video'
          ? screenShareRef.current?.getVideoTracks().find((candidate) => candidate.readyState === 'live')
          : undefined;
        const track = screenTrack
          ?? local.getTracks().find((candidate) => candidate.kind === section.kind);
        if (!track) continue;
        if (section.kind === 'video') {
          const mutationIsCurrent = await videoSenderMutationsRef.current!.run(transceiver.sender, async () => {
            if (!isCurrentPeerContext(peerContext)) return false;
            if (transceiver.sender.track !== track) {
              try {
                await transceiver.sender.replaceTrack(track);
              } catch (error) {
                if (!isCurrentPeerContext(peerContext)) return false;
                throw error;
              }
              if (!isCurrentPeerContext(peerContext)) return false;
              if (transceiver.sender.track !== track) {
                throw new Error(screenTrack
                  ? 'The screen share could not reconnect.'
                  : 'The video track could not be attached.');
              }
            }
            try {
              await (screenTrack
                ? configureNativeScreenShareSender(transceiver.sender)
                : configureNativeVideoSender(transceiver.sender));
            } catch (error) {
              if (isCurrentPeerContext(peerContext)) {
                sendOnSocket(socketContext, 'media_error', {
                  kind: screenTrack ? 'screen_share_sender_parameters' : 'camera_sender_parameters',
                  reason: 'server offer',
                  message: error instanceof Error
                    ? error.message
                    : `Could not configure ${screenTrack ? 'screen-share' : 'camera'} sender settings.`,
                  client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION },
                });
              }
            }
            return isCurrentPeerContext(peerContext);
          });
          if (!mutationIsCurrent) return;
          continue;
        }
      }
      if (!isCurrentPeerContext(peerContext)) return;
      peerContext.remoteDescriptionReady = true;
      for (const candidate of peerContext.pendingCandidates.splice(0)) {
        const added = await settleGenerationOperation(
          peer.addIceCandidate(candidate),
          () => isCurrentPeerContext(peerContext),
        );
        if (!added.current) return;
      }
      const answerResult = await settleGenerationOperation(
        peer.createAnswer(),
        () => isCurrentPeerContext(peerContext),
      );
      if (!answerResult.current) return;
      const answer = answerResult.value;
      const localDescription = await settleGenerationOperation(
        peer.setLocalDescription(answer),
        () => isCurrentPeerContext(peerContext),
      );
      if (!localDescription.current) return;
      const uplinkMids = new Map<'audio' | 'video', string>();
      uplinkTransceivers.forEach((transceiver, kind) => {
        if (transceiver.mid) uplinkMids.set(kind, transceiver.mid);
      });
      const negotiatedAnswerSdp = peer.localDescription?.sdp ?? answer.sdp;
      const invalidAnswerKinds = new Set(unexpectedNativeUplinkDirectionKinds(
        negotiatedAnswerSdp,
        uplinkMids,
        microphoneRequestedForOffer,
      ));
      const videoUplinkMid = uplinkMids.get('video') ?? '';
      const videoCodecViolation = videoUplinkMid
        ? nativeVideoUplinkCodecViolation(negotiatedAnswerSdp, videoUplinkMid)
        : 'video uplink MID is missing';
      const invalidUplinkKinds = (['audio', 'video'] as const).filter((kind) => (
        invalidAnswerKinds.has(kind)
        || uplinkTransceivers.get(kind)?.currentDirection !== nativeUplinkAnswerDirection(
          kind,
          microphoneRequestedForOffer,
        )
      ));
      if (
        uplinkTransceivers.size !== 2
        || invalidUplinkKinds.length > 0
        || videoCodecViolation !== null
        || [...uplinkTransceivers.values()].some((transceiver) => transceiver.stopped)
      ) {
        const invalidKinds = invalidUplinkKinds.length ? invalidUplinkKinds.join(', ') : 'audio/video';
        sendOnSocket(socketContext, 'media_error', {
          kind: 'native_uplink_negotiation',
          reason: videoCodecViolation
            ? `Native video codec rejected: ${videoCodecViolation}`
            : `Uplink directions did not match publication intent: ${invalidKinds}`,
          client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION },
        });
        throw new Error('The call could not prepare your microphone and camera. Reconnecting…');
      }
      sendOnSocket(socketContext, 'answer', { type: 'answer', sdp: negotiatedAnswerSdp }, {
        offerId: envelope.offerId,
        revision: envelope.revision,
      });
      const currentLocal = localRef.current;
      const hasLiveLocalVideo = currentLocal?.getVideoTracks()
        .some((track) => track.readyState === 'live') ?? false;
      const hasLiveScreenShare = screenShareRef.current?.getVideoTracks()
        .some((track) => track.readyState === 'live') ?? false;
      sendOnSocket(
        socketContext,
        'participant_media_state',
        localParticipantMediaState({
          // The negotiated audio sender is deliberately still detached/silent.
          // Recovery publishes false only after React commits and before enable.
          micMuted: true,
          cameraOff: screenShareAnnouncedRef.current
            || !requestedVideo.current
            || !hasLiveLocalVideo
            || systemVideoSuspendedRef.current,
          screenSharing: screenShareAnnouncedRef.current && hasLiveScreenShare,
          suspended: screenShareAnnouncedRef.current ? false : systemVideoSuspendedRef.current,
        }),
      );
      if (screenShareAnnouncedRef.current && hasLiveScreenShare) {
        sendOnSocket(socketContext, 'screen_share_started');
      }
      if (
        requestedAudio.current
      ) {
        recoverNativeMicrophone('microphone publication requested after the room connection became ready');
      }
      if (
        requestedVideo.current
        && appStateRef.current === 'active'
        && !hasLiveLocalVideo
        && !screenShareRequestedRef.current
        && !screenShareAnnouncedRef.current
      ) {
        recoverNativeCamera('camera requested while the room connection became ready');
      }
      return;
    }
    if (envelope.event !== 'kanban') return;
    const nested = JSON.parse(envelope.data) as NestedEnvelope;
    switch (nested.event) {
      case 'access_granted': {
        const stream = localRef.current;
        if (!stream) throw new Error('Camera and microphone were not ready.');
        if (reconnectContextRef.current) confirmNativeRoomAccessGranted(reconnectContextRef.current);
        installPeer(config.iceServers, stream, socketContext);
        const hasLiveScreenShare = screenShareRef.current?.getVideoTracks()
          .some((track) => track.readyState === 'live') ?? false;
        sendOnSocket(socketContext, 'media_ready', {
          client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION, appState: appStateRef.current },
          media: { audio: requestedAudio.current, video: requestedVideo.current || hasLiveScreenShare },
        });
        break;
      }
      case 'participants': {
        const snapshot = parseNestedData<{
          participants?: string[];
          mediaStates?: unknown;
          endpointMediaStates?: unknown;
          recording?: { enabled?: boolean };
        }>(nested.data, {});
        const nextParticipants = Array.isArray(snapshot.participants)
          ? snapshot.participants.map((participant) => String(participant).trim()).filter(Boolean)
          : null;
        const nextParticipantMediaStates = participantMediaStatesFromSnapshot(
          snapshot.mediaStates,
          participantMediaStatesRef.current,
        );
        participantMediaStatesRef.current = nextParticipantMediaStates;
        const nextParticipantEndpointMediaStates = participantEndpointMediaStatesFromSnapshot(
          snapshot.endpointMediaStates,
          participantEndpointMediaStatesRef.current,
        );
        const endpointSnapshotIsAuthoritative = participantEndpointMediaStatesSnapshotIsAuthoritative(
          snapshot.endpointMediaStates,
        );
        participantEndpointMediaStatesRef.current = nextParticipantEndpointMediaStates;
        remoteVideoTracksRef.current.forEach((entry) => {
          const owner = participantForTrack(entry.trackId, participantsByTrackRef.current) ?? entry.participant;
          const ownerEndpointId = endpointForTrack(entry.trackId, endpointsByTrackRef.current) ?? entry.endpointId;
          if (participantVideoIsOff(
            nextParticipantMediaStates,
            owner,
            nextParticipantEndpointMediaStates,
            ownerEndpointId,
          )) {
            remoteVideoProgressRef.current.delete(entry.trackId);
          }
        });
        const trackIndex = participantsByTrackRef.current;
        const endpointIndex = endpointsByTrackRef.current;
        if (nextParticipants) {
          participantsRef.current = nextParticipants;
          hasParticipantSnapshotRef.current = true;
          nextParticipants.forEach((participant) => {
            departedParticipantsRef.current.delete(normalizedParticipantName(participant));
          });
          const currentEntries = [...remoteVideoTracksRef.current.values()];
          const rosterReconciliation = reconcileRemoteParticipantRoster(
            currentEntries,
            trackIndex,
            nextParticipants,
          );
          const endpointReconciliation = reconcileRemoteParticipantEndpoints(
            rosterReconciliation.feeds,
            rosterReconciliation.participantsByTrack,
            endpointIndex,
            endpointSnapshotIsAuthoritative ? nextParticipantEndpointMediaStates : null,
          );
          const retainedEntries = endpointReconciliation.feeds;
          const retainedEntrySet = new Set(retainedEntries);
          currentEntries.forEach((entry) => {
            if (!retainedEntrySet.has(entry)) {
              retiredRemoteTrackIdsRef.current.add(entry.trackId);
              remoteVideoMuteControllerRef.current?.cancel(entry.track);
              remoteVideoProgressRef.current.delete(entry.trackId);
            }
          });
          remoteVideoTracksRef.current = new Map(retainedEntries.map((entry) => [entry.trackId, entry]));
          const retainedTrackIndex = endpointReconciliation.participantsByTrack;
          trackIndex.forEach((_, trackId) => {
            if (!retainedTrackIndex.has(trackId)) retiredRemoteTrackIdsRef.current.add(trackId);
          });
          participantsByTrackRef.current = retainedTrackIndex;
          endpointsByTrackRef.current = endpointReconciliation.endpointsByTrack;
          retainedEntries.forEach((entry) => restoreRemoteVideoEntry(entry));
        }
        updateStateForSocket((current) => ({
          ...current,
          participants: nextParticipants ?? current.participants,
          participantMediaStates: nextParticipantMediaStates,
          participantEndpointMediaStates: nextParticipantEndpointMediaStates,
          recording: snapshot.recording?.enabled ?? current.recording,
          remoteVideoFeeds: nextParticipants
            ? reconcileRemoteParticipantEndpoints(
              reconcileRemoteParticipantRoster(current.remoteVideoFeeds, trackIndex, nextParticipants).feeds,
              trackIndex,
              endpointIndex,
              endpointSnapshotIsAuthoritative ? nextParticipantEndpointMediaStates : null,
            ).feeds
            : current.remoteVideoFeeds,
          activeSpeaker: nextParticipants && current.activeSpeaker
            && !nextParticipants.some((participant) => normalizedParticipantName(participant) === normalizedParticipantName(current.activeSpeaker))
            ? undefined
            : current.activeSpeaker,
        }));
        break;
      }
      case 'room_chat_history': {
        dispatchConversation({
          type: 'room_chat_history',
          payload: parseNestedData<unknown>(nested.data, null),
          chatOpen: roomChatOpenRef.current,
          viewer: conversationViewerRef.current,
        });
        break;
      }
      case 'room_chat': {
        dispatchConversation({
          type: 'room_chat',
          payload: parseNestedData<unknown>(nested.data, null),
          chatOpen: roomChatOpenRef.current,
          viewer: conversationViewerRef.current,
        });
        break;
      }
      case 'room_chat_delete': {
        dispatchConversation({
          type: 'room_chat_delete',
          payload: parseNestedData<unknown>(nested.data, null),
        });
        break;
      }
      case 'memory_transcript': {
        dispatchConversation({
          type: 'memory_transcript',
          payload: parseNestedData<unknown>(nested.data, null),
        });
        break;
      }
      case 'participant_left': {
        const participant = participantNameFromPayload(nested.data);
        if (!participant) break;
        const departedName = normalizedParticipantName(participant);
        const nextParticipantMediaStates = { ...participantMediaStatesRef.current };
        delete nextParticipantMediaStates[departedName];
        participantMediaStatesRef.current = nextParticipantMediaStates;
        const nextParticipantEndpointMediaStates = { ...participantEndpointMediaStatesRef.current };
        delete nextParticipantEndpointMediaStates[departedName];
        participantEndpointMediaStatesRef.current = nextParticipantEndpointMediaStates;
        departedParticipantsRef.current.add(departedName);
        participantsRef.current = participantsRef.current.filter((name) => (
          normalizedParticipantName(name) !== departedName
        ));
        const trackIndex = participantsByTrackRef.current;
        trackIndex.forEach((name, trackId) => {
          if (normalizedParticipantName(name) === departedName) {
            retiredRemoteTrackIdsRef.current.add(trackId);
          }
        });
        const currentEntries = [...remoteVideoTracksRef.current.values()];
        const retainedEntries = removeRemoteParticipantMedia(currentEntries, trackIndex, participant).feeds;
        const retainedEntrySet = new Set(retainedEntries);
        currentEntries.forEach((entry) => {
          if (!retainedEntrySet.has(entry)) {
            retiredRemoteTrackIdsRef.current.add(entry.trackId);
            remoteVideoMuteControllerRef.current?.cancel(entry.track);
            remoteVideoProgressRef.current.delete(entry.trackId);
          }
        });
        remoteVideoTracksRef.current = new Map(retainedEntries.map((entry) => [entry.trackId, entry]));
        participantsByTrackRef.current = removeRemoteParticipantMedia([], trackIndex, participant).participantsByTrack;
        endpointsByTrackRef.current = retainRemoteTrackIndexForFeeds(
          endpointsByTrackRef.current,
          retainedEntries,
        );
        updateStateForSocket((current) => ({
          ...current,
          participants: current.participants.filter((name) => (
            normalizedParticipantName(name) !== normalizedParticipantName(participant)
          )),
          participantMediaStates: nextParticipantMediaStates,
          participantEndpointMediaStates: nextParticipantEndpointMediaStates,
          remoteVideoFeeds: removeRemoteParticipantMedia(
            current.remoteVideoFeeds,
            trackIndex,
            participant,
          ).feeds,
          activeSpeaker: normalizedParticipantName(current.activeSpeaker) === normalizedParticipantName(participant)
            ? undefined
            : current.activeSpeaker,
        }));
        break;
      }
      case 'participant_track': {
        const metadata = parseNestedData<ParticipantTrackMetadata>(nested.data, {});
        const participant = String(metadata.name ?? '').trim();
        if (!participantCanPublishVideo(participant)) break;
        for (const trackId of [metadata.trackId, metadata.sourceTrackId]) {
          const normalizedTrackId = String(trackId ?? '').trim();
          if (normalizedTrackId) retiredRemoteTrackIdsRef.current.delete(normalizedTrackId);
        }
        participantsByTrackRef.current = indexParticipantTrack(participantsByTrackRef.current, metadata);
        endpointsByTrackRef.current = indexParticipantTrackEndpoint(endpointsByTrackRef.current, metadata);
        const entriesToRestore: RemoteVideoTrackEntry[] = [];
        remoteVideoTracksRef.current.forEach((entry) => {
          const resolvedParticipant = participantForTrack(entry.trackId, participantsByTrackRef.current);
          if (!resolvedParticipant) return;
          entry.participant = resolvedParticipant;
          entry.endpointId = endpointForTrack(entry.trackId, endpointsByTrackRef.current) ?? entry.endpointId;
          entriesToRestore.push(entry);
        });
        updateStateForSocket((current) => ({
          ...current,
          remoteVideoFeeds: current.remoteVideoFeeds.map((feed) => ({
            ...feed,
            participant: participantForTrack(feed.trackId, participantsByTrackRef.current) ?? feed.participant,
            endpointId: endpointForTrack(feed.trackId, endpointsByTrackRef.current) ?? feed.endpointId,
          })),
        }));
        entriesToRestore.forEach((entry) => restoreRemoteVideoEntry(entry));
        break;
      }
      case 'active_speaker': {
        const speaker = parseNestedData<unknown>(nested.data, '');
        let activeSpeaker: string | undefined;
        if (typeof speaker === 'string') {
          activeSpeaker = speaker.trim() || undefined;
        } else if (speaker && typeof speaker === 'object') {
          const payload = speaker as { name?: unknown; participant?: unknown };
          const value = typeof payload.name === 'string'
            ? payload.name
            : typeof payload.participant === 'string'
              ? payload.participant
              : '';
          activeSpeaker = value.trim() || undefined;
        }
        updateStateForSocket((current) => ({ ...current, activeSpeaker }));
        break;
      }
      case 'media_disconnected': {
        const message = parseNestedData<unknown>(nested.data, 'The media connection ended.');
        updateStateForSocket((current) => ({
          ...current,
          lifecycle: 'reconnecting',
          error: typeof message === 'string' ? message : 'Reconnecting media…',
        }));
        if (socketIsCurrent() && socketContext.socket.readyState < WebSocket.CLOSING) {
          socketContext.socket.close(1012, 'reconnect media');
        }
        break;
      }
      case 'room_closed':
      case 'session_replaced':
      case 'access_denied': {
        const message = parseNestedData<unknown>(nested.data, 'The room disconnected.');
        fail(typeof message === 'string' ? message : 'The room disconnected.');
        break;
      }
      default:
        break;
    }
  }, [
    fail,
    installPeer,
    isCurrentPeerContext,
    localParticipantMediaState,
    participantCanPublishVideo,
    recoverNativeCamera,
    recoverNativeMicrophone,
    restoreRemoteVideoEntry,
    sendOnSocket,
  ]);

  const scheduleReconnect = useCallback((reason: string) => {
    if (intentionallyLeaving.current || !localRef.current || !reconnectContextRef.current) return;
    if (reconnectTimerRef.current) return;
    disposePeer();
    const attempt = reconnectAttemptRef.current;
    const delay = reconnectDelaysMs[Math.min(attempt, reconnectDelaysMs.length - 1)];
    reconnectAttemptRef.current += 1;
    setState((current) => ({
      ...current,
      lifecycle: 'reconnecting',
      remoteVideoFeeds: [],
      quality: null,
      error: reason || 'Reconnecting to the room…',
    }));
    reconnectTimerRef.current = setTimeout(() => {
      reconnectTimerRef.current = null;
      connectSocketRef.current();
    }, delay);
  }, [disposePeer]);

  const connectSocket = useCallback(() => {
    const context = reconnectContextRef.current;
    if (!sessionToken || !context || !localRef.current || intentionallyLeaving.current) return;
    const NativeWebSocket = WebSocket as unknown as NativeWebSocketConstructor;
    const socket = new NativeWebSocket(websocketURL(roomId), [], {
      headers: {
        Authorization: `Bearer ${sessionToken}`,
        'X-Bonfire-Client': NATIVE_CLIENT_HEADER,
      },
    });
    const generation = connectionGenerationGuardRef.current?.activateSocket();
    if (!generation) {
      socket.close(1011, 'signaling generation unavailable');
      return;
    }
    const socketContext: NativeRoomSocketContext = {
      socket,
      generation,
      pendingCandidates: [],
      signalQueue: Promise.resolve(),
    };
    socketContextRef.current = socketContext;
    socketRef.current = socket;
    const socketIsCurrent = () => (
      socketContextRef.current === socketContext
      && socketRef.current === socket
      && connectionGenerationGuardRef.current?.isCurrentSocket(generation) === true
    );
    socket.onopen = () => {
      if (!socketIsCurrent()) return;
      reconnectAttemptRef.current = 0;
      setState((current) => (socketIsCurrent() ? { ...current, error: null } : current));
      sendOnSocket(socketContext, 'participant', {
        ...nativeRoomParticipantHello(endpointId.current, context),
        client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION },
      });
      if (heartbeatRef.current) clearInterval(heartbeatRef.current);
      heartbeatRef.current = setInterval(() => {
        sendOnSocket(socketContext, 'room_ping');
      }, 15_000);
    };
    socket.onmessage = (message) => {
      socketContext.signalQueue = socketContext.signalQueue
        .then(async () => {
          if (!socketIsCurrent()) return;
          const envelope = JSON.parse(String(message.data)) as SignalEnvelope;
          await handleSignal(envelope, { iceServers: context.iceServers }, socketContext);
        })
        .catch((err) => {
          if (!socketIsCurrent()) return;
          if (socket.readyState < WebSocket.CLOSING) {
            socket.close(1012, 'signaling retry');
          }
          setState((current) => (
            socketIsCurrent()
              ? { ...current, error: err instanceof Error ? err.message : 'Room signaling interrupted.' }
              : current
          ));
        });
    };
    socket.onerror = () => {
      if (socketIsCurrent()) {
        setState((current) => (
          socketIsCurrent()
            ? { ...current, lifecycle: 'reconnecting', error: 'Reconnecting to the room…' }
            : current
        ));
      }
    };
    socket.onclose = () => {
      if (!socketIsCurrent()) return;
      socketContextRef.current = null;
      socketRef.current = null;
      connectionGenerationGuardRef.current?.retireSocket(generation);
      if (!intentionallyLeaving.current) scheduleReconnect('Reconnecting to the room…');
    };
  }, [handleSignal, roomId, scheduleReconnect, sendOnSocket, sessionToken]);

  connectSocketRef.current = connectSocket;

  const join = useCallback(async (
    withVideo: boolean,
    passcode = '',
    withAudio = true,
    transferExisting = false,
  ) => {
    if (!sessionToken || state.lifecycle !== 'idle') return;
    const joinAttempt = joinAttemptGuardRef.current?.begin();
    if (!joinAttempt) return;
    const joinIsCurrent = () => joinAttemptGuardRef.current?.isCurrent(joinAttempt) === true;
    resetCameraFraming(true);
    // Each call starts from the premium composition the user validated:
    // Center Stage off and explicit 9:16 portrait capture. Both controls stay
    // available in-call as deliberate power-user choices.
    requestedCenterStageRef.current = false;
    intentionallyLeaving.current = false;
    requestedAudio.current = withAudio;
    requestedVideo.current = withVideo;
    requestedWideUprightFramingRef.current = wideUprightIntentAfterTransition(
      requestedWideUprightFramingRef.current,
      'call-start',
    );
    roomChatOpenRef.current = false;
    dispatchConversation({ type: 'reset', roomId });
    setState((current) => (joinIsCurrent() ? { ...current, lifecycle: 'joining', error: null } : current));
    try {
      const clientConfigResult = await settleGenerationOperation(
        api.clientConfig(sessionToken),
        joinIsCurrent,
      );
      if (!clientConfigResult.current) return;
      const clientConfig = clientConfigResult.value;
      const iceServers = clientConfig.rtcConfiguration?.iceServers ?? [];
      const captureRequestedMedia = async (): Promise<MediaStream> => {
        if (!withAudio && !withVideo) return new MediaStream();
        try {
          return await mediaDevices.getUserMedia({
            audio: withAudio,
            video: withVideo ? nativeCameraConstraints : false,
          });
        } catch (error) {
          if (!withVideo) throw error;
          // Camera permission or hardware failure should not lock someone out
          // of the room. Join quietly without video; they can retry the camera
          // control after fixing permission or choosing another device.
          requestedVideo.current = false;
          return withAudio
            ? mediaDevices.getUserMedia({ audio: true, video: false })
            : new MediaStream();
        }
      };
      const streamResult = await settleGenerationResource(
        captureRequestedMedia(),
        joinIsCurrent,
        releaseNativeMediaStream,
      );
      if (!streamResult.current) return;
      const stream = streamResult.value;
      // getUserMedia returns live microphone tracks enabled by default. Keep
      // them silent until the server offer is ready and React has visibly
      // committed the unmuted state through the publication barrier.
      setLocalAudioTracksEnabled(stream.getAudioTracks(), false);
      localRef.current = stream;
      setState((current) => (
        joinIsCurrent()
          ? {
            ...current,
            localStream: stream,
            ...localAudioPublicationPendingState(requestedAudio.current),
            cameraOff: !requestedVideo.current,
            cameraStarting: false,
            videoSuspended: false,
          }
          : current
      ));
      if (!joinIsCurrent()) {
        if (localRef.current === stream) localRef.current = null;
        releaseNativeMediaStream(stream);
        return;
      }
      // Establish a valid 16:9 wide or 9:16 portrait capture before signaling.
      // Unsupported cameras complete capability discovery without mutation.
      await refreshCameraFramingInternal(true);
      if (!joinIsCurrent()) return;
      reconnectContextRef.current = { iceServers, passcode, transferExisting };
      reconnectAttemptRef.current = 0;
      connectSocket();
    } catch (err) {
      if (!joinIsCurrent()) return;
      fail(err instanceof Error ? err.message : 'Could not join this room.');
    }
  }, [
    connectSocket,
    fail,
    resetCameraFraming,
    roomId,
    refreshCameraFramingInternal,
    sessionToken,
    state.lifecycle,
  ]);

  const setMuted = useCallback((muted: boolean) => {
    const liveTracks = localRef.current?.getAudioTracks()
      .filter((track) => track.readyState === 'live') ?? [];
    if (muted) {
      requestedAudio.current = false;
      microphonePublicationOperationRef.current += 1;
      cancelMicrophonePublicationCommit();
      // Keep an in-flight permission/replacement operation serialized. Its
      // currentness predicate now reads false and will release or roll back;
      // if the user immediately unmutes again, that same operation may safely
      // satisfy the latest on intent without racing a second sender mutation.
      setLocalAudioTracksEnabled([
        ...liveTracks,
        audioSenderRef.current?.track,
      ], false);
      setState((current) => ({ ...current, muted: true, microphoneStarting: false }));
      send('participant_media_state', localParticipantMediaState({ micMuted: true }));
      return;
    }

    requestedAudio.current = true;
    setLocalAudioTracksEnabled([
      ...liveTracks,
      audioSenderRef.current?.track,
    ], false);
    // Existing and newly captured tracks use the same serialized publication
    // barrier. Never turn a live track on directly from the button callback.
    setState((current) => ({ ...current, muted: true, microphoneStarting: true, error: null }));
    const failMicrophoneStart = (reason: string) => {
      requestedAudio.current = false;
      setState((current) => ({
        ...current,
        muted: true,
        microphoneStarting: false,
        error: 'The microphone is not ready yet. Rejoin the room and try again.',
      }));
      send('participant_media_state', localParticipantMediaState({ micMuted: true }));
      send('media_error', {
        kind: 'microphone_uplink_unavailable',
        reason,
        client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION, appState: appStateRef.current },
      });
    };
    const peer = peerContextRef.current?.peer ?? peerRef.current;
    const audioTransceiver = nativeUplinkTransceiverForSender(
      peer?.getTransceivers() ?? [],
      audioSenderRef.current,
    );
    if (audioTransceiver && audioTransceiver.currentDirection !== 'sendonly') {
      audioTransceiver.direction = 'sendonly';
      if (send('request_participant_tracks', {
        reason: 'microphone enabled after quiet join',
        renegotiateUplink: true,
      })) return;
      audioTransceiver.direction = 'inactive';
      failMicrophoneStart('unmute could not request a server offer');
      return;
    }
    if (
      !microphoneRecoveryGuardRef.current?.isRunning()
      && recoverNativeMicrophone('microphone enabled after joining muted')
    ) {
      return;
    }
    failMicrophoneStart('unmute could not resolve the fixed audio publication sender');
  }, [cancelMicrophonePublicationCommit, localParticipantMediaState, recoverNativeMicrophone, send]);

  const setCameraOff = useCallback((cameraOff: boolean) => {
    const liveTracks = localRef.current?.getVideoTracks()
      .filter((track) => track.readyState === 'live') ?? [];
    requestedVideo.current = !cameraOff;
    zeroOutboundVideoIntervalsRef.current = 0;
    if (cameraOff) {
      void restoreWideUprightFraming();
      resetCameraFraming();
    }
    if (screenShareRequestedRef.current || screenShareAnnouncedRef.current) {
      // Camera intent is remembered for restoration, but ReplayKit exclusively
      // owns the negotiated video sender until sharing stops.
      liveTracks.forEach((track) => { track.enabled = false; });
      setState((current) => ({
        ...current,
        cameraOff: true,
        cameraStarting: false,
        videoSuspended: false,
      }));
      send('participant_media_state', localParticipantMediaState({ cameraOff: true }));
      return;
    }
    if (cameraOff) {
      // As with microphone recovery, do not launch an overlapping replaceTrack
      // against this sender. The pending operation observes requestedVideo and
      // either rolls back while off or completes if the latest intent returns on.
      systemVideoSuspendedRef.current = false;
      liveTracks.forEach((track) => { track.enabled = false; });
      setState((current) => ({
        ...current,
        cameraOff: true,
        cameraStarting: false,
        videoSuspended: false,
      }));
      send('participant_media_state', localParticipantMediaState({
        cameraOff: true,
        screenSharing: false,
        suspended: false,
      }));
      return;
    }

    if (liveTracks.length) {
      liveTracks.forEach((track) => { track.enabled = true; });
      setState((current) => ({
        ...current,
        cameraOff: false,
        cameraStarting: false,
        videoSuspended: false,
        error: null,
      }));
      send('participant_media_state', localParticipantMediaState({
        cameraOff: false,
        screenSharing: false,
        suspended: false,
      }));
      scheduleCameraFramingRefresh(0, true);
      return;
    }

    setState((current) => ({
      ...current,
      cameraOff: false,
      cameraStarting: true,
      videoSuspended: false,
      error: null,
    }));
    send('participant_media_state', localParticipantMediaState({
      cameraOff: true,
      screenSharing: false,
      suspended: false,
    }));
    if (appStateRef.current === 'active') {
      recoverNativeCamera('camera enabled after joining with video off', true);
    }
  }, [
    localParticipantMediaState,
    recoverNativeCamera,
    resetCameraFraming,
    restoreWideUprightFraming,
    scheduleCameraFramingRefresh,
    send,
  ]);

  const finishScreenShare = useCallback(async (
    reason: ScreenShareStopReason = 'user',
    userError?: string,
  ) => {
    if (!screenShareStopShouldBegin(
      screenShareRequestedRef.current,
      screenShareAnnouncedRef.current,
      Boolean(screenShareRef.current),
    )) return;
    const stopOperation = ++screenShareOperationRef.current;
    const logicalLocalStream = localRef.current;
    screenShareRequestedRef.current = false;
    const wasAnnounced = screenShareAnnouncedRef.current;
    screenShareAnnouncedRef.current = false;
    const displayStream = screenShareRef.current;
    screenShareRef.current = null;
    const screenTrack = displayStream?.getVideoTracks()[0] ?? null;
    if (screenTrack) screenTrack.onended = null;

    const peerContext = peerContextRef.current;
    const sender = videoSenderRef.current;
    const cameraTrack = requestedVideo.current
      ? localRef.current?.getVideoTracks().find((track) => track.readyState === 'live') ?? null
      : null;
    if (cameraTrack) cameraTrack.enabled = true;

    let restoreError: unknown = null;
    try {
      if (sender) await videoSenderMutationsRef.current!.run(sender, async () => {
        if (
          !screenTrack
          || !sender
          || !peerContext
          || videoSenderRef.current !== sender
          || !isCurrentPeerContext(peerContext)
        ) return;
        await restoreAfterScreenShare({
          sender,
          screenTrack,
          restoreTrack: cameraTrack,
        });
        if (cameraTrack && sender.track === cameraTrack) {
          await configureNativeVideoSender(sender);
        }
      });
    } catch (error) {
      restoreError = error;
    }

    if (displayStream) releaseNativeMediaStream(displayStream);
    if (!screenShareStopIsCurrent(
      stopOperation,
      screenShareOperationRef.current,
      logicalLocalStream,
      localRef.current,
    )) return;
    if (wasAnnounced) send('screen_share_stopped');
    systemVideoSuspendedRef.current = false;
    resetQualityBaseline();

    const activeSender = videoSenderRef.current;
    const cameraReady = Boolean(
      requestedVideo.current
      && cameraTrack
      && activeSender
      && activeSender.track === cameraTrack,
    );
    const finalError = userError
      ?? (restoreError
          ? 'Your screen stopped sharing. Reconnecting the camera…'
        : reason === 'stalled'
          ? 'Screen sharing stopped because no screen data was being sent.'
          : null);
    setState((current) => ({
      ...current,
      screenSharing: false,
      screenShareStarting: false,
      screenShareStream: null,
      cameraOff: !cameraReady,
      cameraStarting: requestedVideo.current && !cameraReady,
      videoSuspended: false,
      error: finalError ?? current.error,
    }));
    send('participant_media_state', localParticipantMediaState({
      cameraOff: !cameraReady,
      screenSharing: false,
      suspended: false,
    }));
    if (cameraReady && !restoreError) scheduleCameraFramingRefresh(0, true);

    if (restoreError) {
      send('media_error', {
        kind: 'screen_share_restore_camera',
        reason,
        message: restoreError instanceof Error ? restoreError.message : 'Camera restoration failed.',
        client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION, appState: appStateRef.current },
      });
      const socket = socketRef.current;
      if (socket && socket.readyState < WebSocket.CLOSING) socket.close(1012, 'restore camera after screen share');
      return;
    }
    if (requestedVideo.current && !cameraReady && appStateRef.current === 'active') {
      recoverNativeCameraRef.current('camera restore after screen sharing', true);
    }
  }, [
    isCurrentPeerContext,
    localParticipantMediaState,
    resetQualityBaseline,
    scheduleCameraFramingRefresh,
    send,
  ]);

  const stopScreenShare = useCallback((reason: ScreenShareStopReason = 'user') => {
    void finishScreenShare(reason);
  }, [finishScreenShare]);
  stopScreenShareRef.current = stopScreenShare;

  const startScreenShare = useCallback((showSystemPicker: () => void): boolean => {
    const peerContext = peerContextRef.current;
    const sender = videoSenderRef.current;
    if (
      intentionallyLeaving.current
      || screenShareRequestedRef.current
      || screenShareRef.current
      || !peerContext
      || !isCurrentPeerContext(peerContext)
      || !sender
      || peerContext.peer.connectionState === 'closed'
    ) return false;

    const operation = ++screenShareOperationRef.current;
    screenShareRequestedRef.current = true;
    screenShareAnnouncedRef.current = false;
    void restoreWideUprightFraming();
    resetCameraFraming();
    cameraRecoveryGuardRef.current?.retire();
    zeroOutboundVideoIntervalsRef.current = 0;
    setState((current) => ({
      ...current,
      screenSharing: false,
      screenShareStarting: true,
      screenShareStream: null,
      error: null,
    }));

    void (async () => {
      let displayStream: MediaStream | null = null;
      const operationIsCurrent = () => (
        screenShareOperationRef.current === operation
        && screenShareRequestedRef.current
        && peerContextRef.current === peerContext
        && videoSenderRef.current === sender
        && isCurrentPeerContext(peerContext)
        && peerContext.peer.connectionState !== 'closed'
      );
      try {
        displayStream = await mediaDevices.getDisplayMedia({});
        const screenTrack = displayStream.getVideoTracks()[0];
        if (!screenTrack) throw new Error('iOS did not provide a screen-share track.');
        if (!operationIsCurrent()) {
          releaseNativeMediaStream(displayStream);
          if (screenShareOperationRef.current === operation) {
            throw new Error('The call reconnected while screen sharing was starting.');
          }
          return;
        }

        screenShareRef.current = displayStream;
        screenTrack.onended = () => {
          if (screenShareRef.current === displayStream) stopScreenShareRef.current('ended');
        };
        let senderConfigurationError: unknown = null;
        const installed = await videoSenderMutationsRef.current!.run(sender, async () => {
          const result = await installScreenShareTrack({
            sender,
            track: screenTrack,
            isCurrent: operationIsCurrent,
          });
          if (result.outcome !== 'installed' || !operationIsCurrent()) return result;
          try {
            await configureNativeScreenShareSender(sender);
          } catch (error) {
            senderConfigurationError = error;
          }
          return result;
        });
        if (installed.outcome !== 'installed' || !operationIsCurrent()) {
          if (screenShareRef.current === displayStream) {
            throw new Error('The call reconnected while screen sharing was starting.');
          }
          return;
        }
        if (senderConfigurationError) {
          sendOnSocket(peerContext.socketContext, 'media_error', {
            kind: 'screen_share_sender_parameters',
            reason: 'start screen share',
            message: senderConfigurationError instanceof Error
              ? senderConfigurationError.message
              : 'Could not optimize screen-share sender settings.',
            client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION },
          });
        }
        if (!operationIsCurrent()) throw new Error('The call reconnected while screen sharing was starting.');

        localRef.current?.getVideoTracks().forEach((track) => { track.enabled = false; });
        systemVideoSuspendedRef.current = false;
        resetQualityBaseline();
        setState((current) => ({
          ...current,
          cameraOff: true,
          cameraStarting: false,
          screenShareStarting: true,
          screenShareStream: displayStream,
          videoSuspended: false,
          error: null,
        }));
        sendOnSocket(
          peerContext.socketContext,
          'participant_media_state',
          localParticipantMediaState({ cameraOff: true, screenSharing: false, suspended: false }),
        );

        const baseline = screenShareProgress(await sender.getStats());
        showSystemPicker();
        const deadline = Date.now() + screenShareStartTimeoutMs;
        let publishing = false;
        while (operationIsCurrent()) {
          await new Promise((resolve) => setTimeout(resolve, screenShareProgressPollMs));
          if (!operationIsCurrent()) {
            if (screenShareOperationRef.current === operation) {
              throw new Error('The call reconnected while screen sharing was starting.');
            }
            return;
          }
          if (screenTrack.readyState === 'ended') throw new Error('Screen sharing was cancelled.');
          const progress = screenShareProgress(await sender.getStats());
          if (screenShareMadeProgress(baseline, progress)) {
            publishing = true;
            break;
          }
          if (Date.now() >= deadline) break;
        }
        if (!publishing) {
          throw new Error('Screen sharing did not start. Choose BonfireOS and tap Start Broadcast.');
        }
        if (!operationIsCurrent()) {
          if (screenShareOperationRef.current === operation) {
            throw new Error('The call reconnected while screen sharing was starting.');
          }
          return;
        }

        screenShareAnnouncedRef.current = true;
        setState((current) => ({
          ...current,
          screenSharing: true,
          screenShareStarting: false,
          screenShareStream: displayStream,
          cameraOff: true,
          cameraStarting: false,
          videoSuspended: false,
          error: null,
        }));
        sendOnSocket(
          peerContext.socketContext,
          'participant_media_state',
          localParticipantMediaState({ cameraOff: true, screenSharing: true, suspended: false }),
        );
        sendOnSocket(peerContext.socketContext, 'screen_share_started');
      } catch (error) {
        if (screenShareOperationRef.current !== operation) return;
        const technicalMessage = error instanceof Error ? error.message : 'Screen sharing failed.';
        sendOnSocket(peerContext.socketContext, 'media_error', {
          kind: 'screen_share_start',
          reason: 'replaykit',
          message: technicalMessage,
          client: { platform: 'ios', version: NATIVE_ROOM_CLIENT_VERSION, appState: appStateRef.current },
        });
        await finishScreenShare('start-failed', technicalMessage);
      }
    })();
    return true;
  }, [
    finishScreenShare,
    isCurrentPeerContext,
    localParticipantMediaState,
    resetCameraFraming,
    resetQualityBaseline,
    restoreWideUprightFraming,
    sendOnSocket,
  ]);

  const setRecording = useCallback((enabled: boolean) => send('set_recording', { enabled }), [send]);

  const switchCamera = useCallback(() => {
    if (
      !requestedVideo.current
      || systemVideoSuspendedRef.current
      || screenShareRequestedRef.current
      || screenShareAnnouncedRef.current
    ) return;
    const track = localRef.current?.getVideoTracks().find((candidate) => candidate.readyState === 'live');
    if (!track) return;
    let facingMode: string;
    try {
      facingMode = String(track.getSettings().facingMode ?? '');
    } catch {
      resetCameraFraming();
      return;
    }
    if (facingMode !== 'user' && facingMode !== 'environment') {
      resetCameraFraming();
      return;
    }

    // applyConstraints is the awaitable successor to _switchCamera. Waiting
    // for its native settings result prevents a framing query from binding to
    // the old device while WebRTC is still switching capture inputs.
    const wideFramingRestore = restoreWideUprightFraming();
    resetCameraFraming();
    const switchOperation = ++cameraSwitchOperationRef.current;
    const targetFacingMode = facingMode === 'user' ? 'environment' : 'user';
    const switchIsCurrent = () => (
      cameraSwitchOperationRef.current === switchOperation
      && requestedVideo.current
      && !screenShareRequestedRef.current
      && !screenShareAnnouncedRef.current
      && localRef.current?.getVideoTracks().includes(track) === true
      && track.readyState === 'live'
    );
    void (async () => {
      try {
        if (wideFramingRestore) await wideFramingRestore;
        if (!switchIsCurrent()) return;
        await track.applyConstraints({
          ...nativeCameraConstraints,
          facingMode: targetFacingMode,
        });
      } catch {
        // The renderer remains on the last confirmed camera. The guarded
        // refresh below reconciles its exact effective framing state.
      } finally {
        if (!switchIsCurrent()) return;
        // The promise updates the JS track settings; a short currentness-guarded
        // delay also lets the native capture registry settle before exact-device
        // capability discovery.
        scheduleCameraFramingRefresh(150, true);
      }
    })();
  }, [resetCameraFraming, restoreWideUprightFraming, scheduleCameraFramingRefresh]);

  useEffect(() => {
    const subscription = AppState.addEventListener('change', (nextState) => {
      const previousAppState = appStateRef.current;
      appStateRef.current = nextState;
      if (nextState !== 'active') {
        // Permanently invalidate a start that crossed the background boundary;
        // returning active cannot make that old permission result current again.
        microphonePublicationOperationRef.current += 1;
        const cancelledPublication = cancelMicrophonePublicationCommit();
        if (cancelledPublication) {
          setState((current) => ({ ...current, muted: true, microphoneStarting: false }));
        }
        return;
      }
      // iOS multitasking camera access is enabled natively. AppState alone is
      // not evidence that capture stopped, so keep tracks and signaling live.
      if (previousAppState === 'active' || intentionallyLeaving.current) return;
      if (
        requestedAudio.current
        && !localMediaTrackIsPublishing(localRef.current, 'audio')
      ) {
        setState((current) => ({ ...current, muted: true, microphoneStarting: true }));
        recoverNativeMicrophone('app returned to foreground before microphone publication completed');
      }
      if (screenShareRequestedRef.current || screenShareAnnouncedRef.current) {
        resetCameraFraming();
        return;
      }
      if (!requestedVideo.current) {
        resetCameraFraming();
        return;
      }
      const hasLiveVideoTrack = localRef.current?.getVideoTracks().some((track) => track.readyState === 'live') ?? false;
      const stalled = systemVideoSuspendedRef.current || zeroOutboundVideoIntervalsRef.current >= 2 || !hasLiveVideoTrack;
      if (stalled) {
        setSystemVideoSuspended(true, 'detected camera suspension before foreground recovery');
        recoverNativeCamera('app returned to foreground after a detected camera suspension');
        return;
      }
      scheduleCameraFramingRefresh(100, false);
    });
    return () => subscription.remove();
  }, [
    cancelMicrophonePublicationCommit,
    recoverNativeCamera,
    recoverNativeMicrophone,
    resetCameraFraming,
    scheduleCameraFramingRefresh,
    setSystemVideoSuspended,
  ]);

  useEffect(() => () => leave(), [leave]);

  return {
    state,
    conversation,
    join,
    leave,
    setMuted,
    setCameraOff,
    startScreenShare,
    stopScreenShare,
    switchCamera,
    refreshCameraFraming,
    setCenterStageEnabled,
    setWideUprightFramingEnabled,
    setRecording,
    setRoomChatOpen,
    sendRoomChat,
    deleteRoomChat,
  };
}
