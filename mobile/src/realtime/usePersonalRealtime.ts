import { useCallback, useEffect, useRef, useState } from 'react';
import { AppState, Platform } from 'react-native';
import {
  MediaStream,
  RTCPeerConnection,
  RTCSessionDescription,
  mediaDevices,
} from 'react-native-webrtc';
import BonfireMediaSession, {
  nextMediaSessionGeneration,
} from '../../modules/bonfire-media-session';
import { api, BonfireApiError } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { NATIVE_REALTIME_VOICE_ENABLED } from '../config';
import { emptyTrace, pushTrace, smoothAmplitude } from '../voice/amplitude';
import type { AudioFocusLease, AudioFocusTerminalReason } from '../voice/AudioFocusCoordinator';
import { audioFocusRuntime } from './audioFocusRuntime';
import {
  officeControlChannelIsLive,
  officeControlChannelSnapshot,
  waitForOfficeControlChannel,
} from './OfficeEventsContext';
import { createConversationOperationId } from '../conversations/newConversation';
import {
  audioLevelFromStats,
  isRecord,
  normalizeRealtimeSDP,
  realtimeFunctionCalls,
  realtimeStatusForEvent,
  transcriptFromRealtimeEvent,
  type PersonalRealtimeStatus,
  type RealtimeFunctionCall,
} from './personalRealtimeProtocol';
import {
  closePersonalRealtimeStartup,
  closePersonalRealtimeTransportResources,
  drainPersonalRealtimeStartup,
  personalRealtimeCleanupScope,
  releasePersonalRealtimeTerminalFocus,
} from './personalRealtimeTerminal';
import {
  NativeMediaOperationTimeoutError,
  waitForBoundedNativeOperation,
} from './nativeRoomTerminal';

type DataChannel = ReturnType<RTCPeerConnection['createDataChannel']>;

export type PersonalRealtimeTurn = {
  question: string;
  answer: string;
};

const STATS_INTERVAL_MS = 100;
const NATIVE_MEDIA_OPERATION_TIMEOUT_MS = 2_500;
const PERSONAL_REALTIME_RECONNECT_DELAYS_MS = [500, 1_500] as const;

type PersonalRealtimeReconnectBinding = {
  voiceSessionId: string;
  threadId: string;
  transportRevision: number;
  attempt: number;
};

function newVoiceSessionId(): string {
  const random = Math.random().toString(36).slice(2, 14);
  return `voice-${Date.now().toString(36)}-${random}`;
}

function userFacingRealtimeError(error: unknown): string {
  if (error instanceof NativeMediaOperationTimeoutError) {
    return 'Scout voice audio did not respond. Please try again.';
  }
  if (error instanceof BonfireApiError) return error.message;
  if (error instanceof Error && error.message.trim()) return error.message;
  return 'Scout voice could not connect.';
}

async function waitForOfferSDP(peer: RTCPeerConnection, offer: { sdp?: string }): Promise<string> {
  const immediate = normalizeRealtimeSDP(peer.localDescription?.sdp || offer.sdp);
  if (immediate) return immediate;
  return new Promise((resolve, reject) => {
    let settled = false;
    const previousHandler = peer.onicegatheringstatechange;
    const restoreHandler = () => { peer.onicegatheringstatechange = previousHandler; };
    const finish = () => {
      if (settled) return;
      const value = normalizeRealtimeSDP(peer.localDescription?.sdp);
      if (!value) return;
      settled = true;
      clearTimeout(timeout);
      restoreHandler();
      resolve(value);
    };
    const timeout = setTimeout(() => {
      if (settled) return;
      settled = true;
      restoreHandler();
      reject(new Error('Scout voice did not create an offer.'));
    }, 1_500);
    peer.onicegatheringstatechange = finish;
    finish();
  });
}

/**
 * Native full-duplex private Scout transport. The server remains the authority
 * for session configuration, tools, ACLs, usage, and model routing; this hook
 * owns only the ephemeral WebRTC peer and its user-visible lifecycle.
 */
export function usePersonalRealtime(options: {
  onActions?: (actions: Array<Record<string, unknown>>) => void;
} = {}) {
  const { sessionToken } = useAuth();
  const [status, setStatus] = useState<PersonalRealtimeStatus>('idle');
  const [turn, setTurn] = useState<PersonalRealtimeTurn | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [trace, setTrace] = useState<number[]>(emptyTrace);
  const [threadId, setThreadId] = useState<string | null>(null);
  const mountedRef = useRef(true);
  const generationRef = useRef(0);
  const terminalRequestRef = useRef(0);
  const statusRef = useRef<PersonalRealtimeStatus>('idle');
  const peerRef = useRef<RTCPeerConnection | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const remoteStreamRef = useRef<MediaStream | null>(null);
  const dataChannelRef = useRef<DataChannel | null>(null);
  const leaseRef = useRef<AudioFocusLease | null>(null);
  const mediaSessionGenerationRef = useRef<number | null>(null);
  const handledCallsRef = useRef(new Set<string>());
  const voiceSessionIdRef = useRef('');
  const voiceThreadIdRef = useRef('');
  const voiceTransportRevisionRef = useRef(0);
  const milestoneOperationIdsRef = useRef(new Map<string, string>());
  const pendingMilestonesRef = useRef(new Set<string>());
  const activeTransportRevisionRef = useRef<{ generation: number; revision: number } | null>(null);
  const reconnectBindingRef = useRef<PersonalRealtimeReconnectBinding | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectIntentRef = useRef(0);
  const reconnectRunnerRef = useRef<(binding: PersonalRealtimeReconnectBinding) => void>(() => undefined);
  const authorityTokenRef = useRef(sessionToken);
  const toolAbortControllerRef = useRef<AbortController | null>(null);
  const statsTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const smoothedLevelRef = useRef(0);
  const onActionsRef = useRef(options.onActions);
  onActionsRef.current = options.onActions;

  const setLiveStatus = useCallback((next: PersonalRealtimeStatus) => {
    statusRef.current = next;
    if (mountedRef.current) setStatus(next);
  }, []);

  const cancelReconnect = useCallback((clearLogicalBinding = true) => {
    reconnectIntentRef.current += 1;
    if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
    reconnectTimerRef.current = null;
    reconnectBindingRef.current = null;
    if (clearLogicalBinding) {
      voiceSessionIdRef.current = '';
      voiceThreadIdRef.current = '';
      voiceTransportRevisionRef.current = 0;
      milestoneOperationIdsRef.current = new Map();
      pendingMilestonesRef.current = new Set();
    }
  }, []);

  const queueReconnect = useCallback((binding: PersonalRealtimeReconnectBinding) => {
    if (
      binding.attempt < 1
      || binding.attempt > PERSONAL_REALTIME_RECONNECT_DELAYS_MS.length
      || !mountedRef.current
      || authorityTokenRef.current !== sessionToken
      || AppState.currentState !== 'active'
    ) return false;
    const intent = ++reconnectIntentRef.current;
    reconnectBindingRef.current = binding;
    setError(null);
    setLiveStatus('connecting');
    reconnectTimerRef.current = setTimeout(() => {
      reconnectTimerRef.current = null;
      if (
        reconnectIntentRef.current !== intent
        || reconnectBindingRef.current !== binding
        || authorityTokenRef.current !== sessionToken
        || AppState.currentState !== 'active'
      ) return;
      reconnectRunnerRef.current(binding);
    }, PERSONAL_REALTIME_RECONNECT_DELAYS_MS[binding.attempt - 1]);
    return true;
  }, [sessionToken, setLiveStatus]);

  const cleanupTransport = useCallback(async (
    publishIdle = true,
    expectedMediaSessionGeneration = mediaSessionGenerationRef.current,
    preserveLogicalBinding = false,
  ) => {
    const cleanupScope = personalRealtimeCleanupScope(
      expectedMediaSessionGeneration,
      mediaSessionGenerationRef.current,
    );
    if (cleanupScope !== 'owned') {
      // The first terminal pass already detached this session's JS resources,
      // or a replacement has since installed newer ones. A late finalizer is
      // authorized only to retire the old native generation.
      if (expectedMediaSessionGeneration !== null) {
        await BonfireMediaSession.deactivateVideoMeeting(expectedMediaSessionGeneration);
      }
      if (
        cleanupScope === 'detached'
        && publishIdle
        && mediaSessionGenerationRef.current === null
        && mountedRef.current
      ) {
        setTrace(emptyTrace());
        if (statusRef.current !== 'error') setLiveStatus('idle');
      }
      return;
    }
    generationRef.current += 1;
    if (mediaSessionGenerationRef.current === expectedMediaSessionGeneration) {
      mediaSessionGenerationRef.current = null;
    }
    if (statsTimerRef.current) clearInterval(statsTimerRef.current);
    statsTimerRef.current = null;
    const toolAbortController = toolAbortControllerRef.current;
    toolAbortControllerRef.current = null;
    toolAbortController?.abort();
    const dataChannel = dataChannelRef.current;
    dataChannelRef.current = null;
    const peer = peerRef.current;
    peerRef.current = null;
    const stream = streamRef.current;
    streamRef.current = null;
    remoteStreamRef.current = null;
    handledCallsRef.current = new Set();
    if (!preserveLogicalBinding) {
      voiceSessionIdRef.current = '';
      voiceThreadIdRef.current = '';
      voiceTransportRevisionRef.current = 0;
    }
    milestoneOperationIdsRef.current = new Map();
    pendingMilestonesRef.current = new Set();
    activeTransportRevisionRef.current = null;
    smoothedLevelRef.current = 0;
    await closePersonalRealtimeTransportResources({
      dataChannel,
      peer,
      stream,
      deactivateMediaSession: () => expectedMediaSessionGeneration === null
        ? Promise.resolve(false)
        : BonfireMediaSession.deactivateVideoMeeting(expectedMediaSessionGeneration),
    });
    if (publishIdle && mountedRef.current) {
      setTrace(emptyTrace());
      if (statusRef.current !== 'error') setLiveStatus('idle');
    }
  }, [setLiveStatus]);

  const terminateTransportWithError = useCallback((
    connectionGeneration: number,
    message: string,
  ) => {
    if (generationRef.current !== connectionGeneration) return;
    const reconnectBinding = reconnectBindingRef.current;
    const logicalVoiceSessionId = voiceSessionIdRef.current || reconnectBinding?.voiceSessionId || '';
    const logicalThreadId = voiceThreadIdRef.current || reconnectBinding?.threadId || '';
    const logicalTransportRevision = voiceTransportRevisionRef.current || reconnectBinding?.transportRevision || 0;
    const currentAttempt = reconnectBinding?.attempt ?? 0;
    const binding = logicalVoiceSessionId && logicalThreadId && logicalTransportRevision > 0
      ? {
        voiceSessionId: logicalVoiceSessionId,
        threadId: logicalThreadId,
        transportRevision: logicalTransportRevision,
        attempt: currentAttempt + 1,
      }
      : null;
    if (
      binding
      && binding.attempt <= PERSONAL_REALTIME_RECONNECT_DELAYS_MS.length
      && authorityTokenRef.current === sessionToken
      && AppState.currentState === 'active'
    ) {
      generationRef.current += 1;
      const terminalRequest = ++terminalRequestRef.current;
      const lease = leaseRef.current;
      const expectedMediaSessionGeneration = mediaSessionGenerationRef.current;
      leaseRef.current = null;
      reconnectBindingRef.current = binding;
      setError(null);
      setLiveStatus('connecting');
      void releasePersonalRealtimeTerminalFocus(
        lease,
        () => cleanupTransport(false, expectedMediaSessionGeneration, true),
      ).catch(() => undefined).finally(() => {
        if (
          terminalRequestRef.current !== terminalRequest
          || reconnectBindingRef.current !== binding
          || !queueReconnect(binding)
        ) {
          if (reconnectBindingRef.current === binding && mountedRef.current) {
            cancelReconnect();
            setThreadId(null);
            setError(message);
            setLiveStatus('error');
          }
        }
      });
      return;
    }
    // Fence all peer/channel/provider callbacks synchronously. The exact lease
    // is detached before release so its force-close hook cannot recursively
    // release itself behind the coordinator transition.
    generationRef.current += 1;
    const terminalRequest = ++terminalRequestRef.current;
    const lease = leaseRef.current;
    leaseRef.current = null;
    setError(message);
    // Keep the existing active state until close has synchronously stopped the
    // tracks/peer and finished the native media-session deactivation. Only then
    // may the UI truthfully become inactive/error.
    void releasePersonalRealtimeTerminalFocus(lease, cleanupTransport)
      .catch(() => undefined)
      .finally(() => {
        if (terminalRequestRef.current !== terminalRequest || !mountedRef.current) return;
        setError(message);
        setLiveStatus('error');
      });
  }, [cancelReconnect, cleanupTransport, queueReconnect, sessionToken, setLiveStatus]);

  const sendEvent = useCallback((event: Record<string, unknown>): boolean => {
    const channel = dataChannelRef.current;
    if (!channel || channel.readyState !== 'open') return false;
    channel.send(JSON.stringify(event));
    return true;
  }, []);

  const publishMilestone = useCallback((milestone:
    | 'peer_connected'
    | 'data_channel_open'
    | 'remote_track'
    | 'first_audio'
    | 'response_done'
    | 'transport_error', connectionGeneration?: number,
  ) => {
    if (!sessionToken) return;
    const activeTransport = activeTransportRevisionRef.current;
    if (
      connectionGeneration !== undefined
      && activeTransport
      && activeTransport.generation !== connectionGeneration
    ) return;
    const voiceSessionId = voiceSessionIdRef.current;
    const threadId = voiceThreadIdRef.current;
    const transportRevision = voiceTransportRevisionRef.current;
    if (!voiceSessionId || !threadId || transportRevision <= 0) {
      // Provider callbacks can beat the HTTP offer response. Retain their
      // names until the exact durable binding is known instead of downgrading
      // them to an unbound legacy receipt.
      pendingMilestonesRef.current.add(milestone);
      return;
    }
    let operationId = milestoneOperationIdsRef.current.get(milestone);
    if (!operationId) {
      operationId = createConversationOperationId();
      milestoneOperationIdsRef.current.set(milestone, operationId);
    }
    void api.realtimeMilestone(sessionToken, {
      voiceSessionId,
      threadId,
      transportRevision,
      operationId,
      milestone,
    }).catch(() => undefined);
  }, [sessionToken]);

  const sendToolOutput = useCallback((callId: string, output: Record<string, unknown>) => {
    sendEvent({
      type: 'conversation.item.create',
      item: {
        type: 'function_call_output',
        call_id: callId,
        output: JSON.stringify(output),
      },
    });
    sendEvent({
      type: 'response.create',
      response: { output_modalities: ['audio'], tool_choice: 'none' },
    });
  }, [sendEvent]);

  const handleToolCall = useCallback(async (
    call: RealtimeFunctionCall,
    connectionGeneration: number,
  ) => {
    const lease = leaseRef.current;
    const toolAbortController = toolAbortControllerRef.current;
    if (
      !sessionToken
      || !lease?.isCurrent()
      || !toolAbortController
      || toolAbortController.signal.aborted
      || handledCallsRef.current.has(call.callId)
    ) return;
    handledCallsRef.current.add(call.callId);
    setLiveStatus(call.name === 'do_nothing' ? 'thinking' : 'acting');
    let argumentsValue: Record<string, unknown> = {};
    try {
      const parsed = call.argumentsText.trim() ? JSON.parse(call.argumentsText) : {};
      if (!isRecord(parsed)) throw new Error('arguments must be an object');
      argumentsValue = parsed;
    } catch {
      sendToolOutput(call.callId, { ok: false, error: 'could not read tool arguments' });
      return;
    }
    try {
      const controlReady = await waitForOfficeControlChannel(
        sessionToken,
        () => (
          generationRef.current === connectionGeneration
          && leaseRef.current === lease
          && lease.isCurrent()
          && !toolAbortController.signal.aborted
        ),
      );
      if (!controlReady) {
        sendToolOutput(call.callId, { ok: false, error: 'Scout could not re-establish its authenticated control channel.' });
        return;
      }
      if (
        generationRef.current !== connectionGeneration
        || leaseRef.current !== lease
        || !lease.isCurrent()
        || toolAbortController.signal.aborted
      ) return;
      const response = await api.realtimeTool(
        sessionToken,
        voiceSessionIdRef.current,
        voiceThreadIdRef.current,
        call.callId,
        call.name,
        argumentsValue,
        toolAbortController.signal,
      );
      if (
        generationRef.current !== connectionGeneration
        || leaseRef.current !== lease
        || !lease.isCurrent()
        || toolAbortController.signal.aborted
      ) return;
      if (response.actions?.length) onActionsRef.current?.(response.actions);
      sendToolOutput(call.callId, response.result ?? { ok: response.ok !== false });
    } catch (toolError) {
      if (
        generationRef.current !== connectionGeneration
        || leaseRef.current !== lease
        || !lease.isCurrent()
        || toolAbortController.signal.aborted
      ) return;
      sendToolOutput(call.callId, { ok: false, error: userFacingRealtimeError(toolError) });
    }
  }, [sendToolOutput, sessionToken, setLiveStatus]);

  const handleProviderEvent = useCallback((raw: unknown, connectionGeneration: number) => {
    let event: Record<string, unknown>;
    try {
      const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw;
      if (!isRecord(parsed)) return;
      event = parsed;
    } catch {
      return;
    }
    if (generationRef.current !== connectionGeneration) return;
    const type = String(event.type ?? '');
    if (!type) return;

    const transcript = transcriptFromRealtimeEvent(event);
    if (transcript && mountedRef.current) {
      setTurn((current) => transcript.role === 'user'
        ? { question: transcript.text, answer: '' }
        : { question: current?.question ?? '', answer: transcript.text });
    }
    for (const call of realtimeFunctionCalls(event)) {
      void handleToolCall(call, connectionGeneration);
    }
    if (type === 'response.done' && sessionToken && isRecord(event.response)) {
      publishMilestone('response_done', connectionGeneration);
      const usage = event.response.usage;
      if (isRecord(usage)) {
        void api.realtimeUsage(sessionToken, {
          callId: String(event.response.id ?? ''),
          model: String(event.response.model ?? ''),
          usage,
        }).catch(() => undefined);
      }
    }
    if (type === 'error') {
      const providerError = isRecord(event.error) ? String(event.error.message ?? '') : '';
      publishMilestone('transport_error', connectionGeneration);
      terminateTransportWithError(connectionGeneration, providerError || 'Scout voice needs attention.');
      return;
    }
    if (sessionToken && (type.includes('response.audio') || type.includes('output_audio_buffer.started'))) {
      publishMilestone('first_audio', connectionGeneration);
    }
    setLiveStatus(realtimeStatusForEvent(type, statusRef.current));
  }, [handleToolCall, publishMilestone, sessionToken, setLiveStatus, terminateTransportWithError]);

  const startStats = useCallback((peer: RTCPeerConnection, connectionGeneration: number) => {
    if (generationRef.current !== connectionGeneration || peerRef.current !== peer) return;
    if (statsTimerRef.current) clearInterval(statsTimerRef.current);
    statsTimerRef.current = setInterval(() => {
      void peer.getStats().then((reports) => {
        if (generationRef.current !== connectionGeneration || !mountedRef.current) return;
        const sample = audioLevelFromStats(reports);
        smoothedLevelRef.current = smoothAmplitude(smoothedLevelRef.current, sample);
        setTrace((history) => pushTrace(history, smoothedLevelRef.current));
      }).catch(() => undefined);
    }, STATS_INTERVAL_MS);
  }, []);

  const start = useCallback(async (reconnectBinding?: PersonalRealtimeReconnectBinding) => {
    if (!NATIVE_REALTIME_VOICE_ENABLED || !sessionToken) return;
    const reconnecting = Boolean(reconnectBinding);
    if (reconnecting) {
      if (
        statusRef.current !== 'connecting'
        || reconnectBindingRef.current !== reconnectBinding
        || reconnectBinding?.voiceSessionId !== voiceSessionIdRef.current
        || reconnectBinding.threadId !== voiceThreadIdRef.current
      ) return;
    } else {
      if (statusRef.current !== 'idle') return;
      cancelReconnect();
    }
    terminalRequestRef.current += 1;
    setError(null);
    if (!reconnecting) {
      setTurn(null);
      setThreadId(null);
    }
    setLiveStatus('connecting');
    const connectionGeneration = ++generationRef.current;
    const controlReady = await waitForOfficeControlChannel(
      sessionToken,
      () => (
        mountedRef.current
        && generationRef.current === connectionGeneration
        && authorityTokenRef.current === sessionToken
        && statusRef.current === 'connecting'
      ),
    );
    if (generationRef.current !== connectionGeneration || authorityTokenRef.current !== sessionToken) return;
    if (!controlReady) {
      cancelReconnect();
      setError('Scout voice could not reach its control channel. Please try again.');
      setLiveStatus('error');
      return;
    }
    const voiceSessionId = reconnectBinding?.voiceSessionId ?? newVoiceSessionId();
    const expectedThreadId = reconnectBinding?.threadId ?? '';
    const previousTransportRevision = reconnectBinding?.transportRevision ?? 0;
    voiceSessionIdRef.current = voiceSessionId;
    voiceThreadIdRef.current = expectedThreadId;
    voiceTransportRevisionRef.current = 0;
    activeTransportRevisionRef.current = null;
    milestoneOperationIdsRef.current = new Map();
    pendingMilestonesRef.current = new Set();
    const mediaSessionGeneration = nextMediaSessionGeneration();
    mediaSessionGenerationRef.current = mediaSessionGeneration;
    const cleanupSessionTransport = (publishIdle = true) => (
      cleanupTransport(publishIdle, mediaSessionGeneration)
    );
    let startupFailureTerminalRequest: number | null = null;
    let startupDrain: Promise<unknown> | null = null;
    try {
      const lease = await audioFocusRuntime.acquire('personal_realtime', {
        forceClose: async (reason: AudioFocusTerminalReason) => {
          leaseRef.current = null;
          let controlReconnect: PersonalRealtimeReconnectBinding | null = null;
          let controlReconnectExhausted = false;
          const control = officeControlChannelSnapshot(sessionToken);
          if (
            reason === 'forced_close'
            && control.reconnectEligible
            && AppState.currentState === 'active'
            && authorityTokenRef.current === sessionToken
            && voiceSessionIdRef.current
            && voiceThreadIdRef.current
          ) {
            const priorAttempt = reconnectBindingRef.current?.attempt ?? 0;
            if (priorAttempt < PERSONAL_REALTIME_RECONNECT_DELAYS_MS.length) {
              controlReconnect = {
                voiceSessionId: voiceSessionIdRef.current,
                threadId: voiceThreadIdRef.current,
                transportRevision: voiceTransportRevisionRef.current || previousTransportRevision,
                attempt: priorAttempt + 1,
              };
              reconnectBindingRef.current = controlReconnect;
              setError(null);
              setLiveStatus('connecting');
            } else controlReconnectExhausted = true;
          }
          const preservingReconnect = Boolean(
            controlReconnect
            || (reason === 'error' && reconnectBindingRef.current),
          );
          await closePersonalRealtimeStartup(
            startupDrain,
            (publishIdle = true) => cleanupTransport(
              preservingReconnect ? false : publishIdle,
              mediaSessionGeneration,
              preservingReconnect,
            ),
          );
          if (!preservingReconnect && mountedRef.current) setThreadId(null);
          if (controlReconnect && !queueReconnect(controlReconnect)) {
            cancelReconnect();
            if (mountedRef.current) {
              setThreadId(null);
              setError('Scout voice could not reconnect. Please try again.');
              setLiveStatus('error');
            }
          } else if (controlReconnectExhausted) {
            cancelReconnect();
            if (mountedRef.current) {
              setThreadId(null);
              setError('Scout voice could not reconnect. Please try again.');
              setLiveStatus('error');
            }
          }
        },
      });
      if (
        generationRef.current !== connectionGeneration
        || !lease.isCurrent()
        || !officeControlChannelIsLive(sessionToken)
      ) {
        await lease.release('cancelled');
        return;
      }
      leaseRef.current = lease;
      toolAbortControllerRef.current = new AbortController();
      const capturePromise = Promise.resolve()
        .then(() => mediaDevices.getUserMedia({ audio: true, video: false }))
        .then((capture) => {
        if (generationRef.current !== connectionGeneration || !lease.isCurrent()) {
          capture.getTracks().forEach((track) => track.stop());
        } else {
          streamRef.current = capture;
        }
        return capture;
      });
      const startup = drainPersonalRealtimeStartup(
        Promise.resolve().then(() => api.clientConfig(sessionToken)),
        capturePromise,
        Promise.resolve().then(async () => {
          const snapshot = await waitForBoundedNativeOperation(
            BonfireMediaSession.activateVideoMeeting(mediaSessionGeneration),
            NATIVE_MEDIA_OPERATION_TIMEOUT_MS,
            'Scout voice audio routing',
          );
          if (Platform.OS === 'ios' && snapshot === null) {
            throw new Error('Scout voice audio routing could not be activated.');
          }
          return snapshot;
        }),
        () => {
          if (generationRef.current !== connectionGeneration) return;
          // Stop late capture/provider callbacks immediately, while the drain
          // keeps waiting for native activation and capture to settle.
          generationRef.current += 1;
          startupFailureTerminalRequest = ++terminalRequestRef.current;
        },
      );
      startupDrain = startup;
      const [clientConfig, stream] = await startup;
      startupDrain = null;
      if (
        generationRef.current !== connectionGeneration
        || !lease.isCurrent()
        || !officeControlChannelIsLive(sessionToken)
      ) {
        stream.getTracks().forEach((track) => track.stop());
        // A stale lease is already being closed by the coordinator transition
        // that fenced it. Never fall back to global cleanup from this sibling
        // continuation: that cleanup could outlive the owning close and touch
        // the replacement session after focus has moved on.
        await lease.release('cancelled').catch(() => undefined);
        return;
      }
      const peer = new RTCPeerConnection({
        iceServers: (clientConfig.rtcConfiguration?.iceServers ?? []) as never,
        bundlePolicy: 'max-bundle',
        rtcpMuxPolicy: 'require',
      });
      peerRef.current = peer;
      peer.ontrack = (event: unknown) => {
        if (peerRef.current !== peer || generationRef.current !== connectionGeneration) return;
        const trackEvent = event as { streams?: MediaStream[]; track?: { enabled: boolean } };
        const streamValue = trackEvent.streams?.[0];
        if (streamValue) remoteStreamRef.current = streamValue;
        if (trackEvent.track) trackEvent.track.enabled = true;
        publishMilestone('remote_track', connectionGeneration);
      };
      peer.onconnectionstatechange = () => {
        if (peerRef.current !== peer || generationRef.current !== connectionGeneration) return;
        if (peer.connectionState === 'connected') {
          publishMilestone('peer_connected', connectionGeneration);
          setLiveStatus('listening');
        } else if (peer.connectionState === 'failed' || peer.connectionState === 'disconnected') {
          publishMilestone('transport_error', connectionGeneration);
          terminateTransportWithError(connectionGeneration, 'Scout voice connection was interrupted.');
        } else if (peer.connectionState === 'closed') {
          publishMilestone('transport_error', connectionGeneration);
          terminateTransportWithError(connectionGeneration, 'Scout voice connection ended.');
        }
      };
      const dataChannel = peer.createDataChannel('oai-events');
      dataChannelRef.current = dataChannel;
      dataChannel.onopen = () => {
        if (generationRef.current === connectionGeneration && dataChannelRef.current === dataChannel) {
          publishMilestone('data_channel_open', connectionGeneration);
          setLiveStatus('listening');
        }
      };
      dataChannel.onmessage = (event: unknown) => {
        if (generationRef.current !== connectionGeneration || dataChannelRef.current !== dataChannel) return;
        handleProviderEvent((event as { data?: unknown }).data, connectionGeneration);
      };
      dataChannel.onerror = () => {
        if (dataChannelRef.current !== dataChannel) return;
        publishMilestone('transport_error', connectionGeneration);
        terminateTransportWithError(connectionGeneration, 'Scout voice needs attention.');
      };
      dataChannel.onclose = () => {
        if (dataChannelRef.current !== dataChannel) return;
        publishMilestone('transport_error', connectionGeneration);
        terminateTransportWithError(connectionGeneration, 'Scout voice connection ended.');
      };
      stream.getTracks().forEach((track) => peer.addTrack(track, stream));
      const offer = await peer.createOffer();
      if (generationRef.current !== connectionGeneration || peerRef.current !== peer) return;
      await peer.setLocalDescription(offer);
      if (generationRef.current !== connectionGeneration || peerRef.current !== peer) return;
      const localSDP = await waitForOfferSDP(peer, offer);
      if (
        generationRef.current !== connectionGeneration
        || peerRef.current !== peer
        || !officeControlChannelIsLive(sessionToken)
      ) return;
      const answer = await api.realtimeOffer(sessionToken, localSDP, voiceSessionId);
      if (
        generationRef.current !== connectionGeneration
        || peerRef.current !== peer
        || !lease.isCurrent()
        || !officeControlChannelIsLive(sessionToken)
      ) return;
      if (
        answer.voiceSessionId !== voiceSessionId
        || !String(answer.threadId || '').trim()
        || !Number.isSafeInteger(answer.transportRevision)
        || answer.transportRevision <= previousTransportRevision
        || (reconnecting && answer.threadId !== expectedThreadId)
      ) {
        throw new Error('Scout voice did not bind its private transcript.');
      }
      voiceThreadIdRef.current = answer.threadId;
      voiceTransportRevisionRef.current = answer.transportRevision;
      activeTransportRevisionRef.current = {
        generation: connectionGeneration,
        revision: answer.transportRevision,
      };
      reconnectBindingRef.current = null;
      setThreadId(answer.threadId);
      const pendingMilestones = [...pendingMilestonesRef.current];
      pendingMilestonesRef.current.clear();
      pendingMilestones.forEach((milestone) => publishMilestone(milestone as
        | 'peer_connected'
        | 'data_channel_open'
        | 'remote_track'
        | 'first_audio'
        | 'response_done'
        | 'transport_error', connectionGeneration));
      const answerSDP = normalizeRealtimeSDP(answer.sdp);
      if (!answerSDP) throw new Error('Scout voice returned an empty answer.');
      await peer.setRemoteDescription(new RTCSessionDescription({ type: 'answer', sdp: answerSDP }));
      if (
        generationRef.current !== connectionGeneration
        || peerRef.current !== peer
        || !lease.isCurrent()
      ) return;
      startStats(peer, connectionGeneration);
    } catch (startError) {
      if (startupFailureTerminalRequest === null && generationRef.current !== connectionGeneration) return;
      if (
        startupFailureTerminalRequest !== null
        && terminalRequestRef.current !== startupFailureTerminalRequest
      ) return;
      const message = userFacingRealtimeError(startError);
      // Fence callbacks synchronously, but keep the visible state connecting
      // until exact focus release has closed transport, tracks, and the native
      // media session. "Inactive" must mean the microphone is actually down.
      if (startupFailureTerminalRequest === null) generationRef.current += 1;
      const terminalRequest = startupFailureTerminalRequest ?? ++terminalRequestRef.current;
      const lease = leaseRef.current;
      leaseRef.current = null;
      const nextReconnect = reconnectBinding && reconnectBinding.attempt < PERSONAL_REALTIME_RECONNECT_DELAYS_MS.length
        ? { ...reconnectBinding, attempt: reconnectBinding.attempt + 1 }
        : null;
      if (nextReconnect) {
        voiceSessionIdRef.current = nextReconnect.voiceSessionId;
        voiceThreadIdRef.current = nextReconnect.threadId;
        voiceTransportRevisionRef.current = nextReconnect.transportRevision;
        reconnectBindingRef.current = nextReconnect;
      }
      await releasePersonalRealtimeTerminalFocus(
        lease,
        nextReconnect
          ? () => cleanupTransport(false, mediaSessionGeneration, true)
          : cleanupSessionTransport,
      ).catch(() => undefined);
      if (terminalRequestRef.current !== terminalRequest || !mountedRef.current) return;
      if (nextReconnect && queueReconnect(nextReconnect)) return;
      cancelReconnect();
      setThreadId(null);
      setError(message);
      setLiveStatus('error');
    }
  }, [cancelReconnect, cleanupTransport, handleProviderEvent, publishMilestone, queueReconnect, sessionToken, setLiveStatus, startStats, terminateTransportWithError]);

  reconnectRunnerRef.current = (binding) => {
    void start(binding);
  };

  const stop = useCallback(async (
    reason: AudioFocusTerminalReason = 'completed',
  ) => {
    terminalRequestRef.current += 1;
    cancelReconnect();
    const lease = leaseRef.current;
    leaseRef.current = null;
    try {
      if (lease) await lease.release(reason);
      else await cleanupTransport();
    } catch (stopError) {
      if (mountedRef.current) {
        setError(userFacingRealtimeError(stopError));
        setLiveStatus('error');
      }
      return;
    }
    if (mountedRef.current) {
      setThreadId(null);
      setLiveStatus('idle');
    }
  }, [cancelReconnect, cleanupTransport, setLiveStatus]);

  useEffect(() => {
    if (authorityTokenRef.current === sessionToken) return;
    authorityTokenRef.current = sessionToken;
    setThreadId(null);
    // A private voice transcript is authorized to exactly one signed-in
    // account. Moving this hook above navigation must never let a transport or
    // its bound thread survive an account/session boundary.
    void stop('cancelled');
  }, [sessionToken, stop]);

  useEffect(() => () => {
    mountedRef.current = false;
    cancelReconnect();
    const lease = leaseRef.current;
    leaseRef.current = null;
    if (lease) void lease.release('cancelled').catch(() => undefined);
    else void cleanupTransport().catch(() => undefined);
  }, [cancelReconnect, cleanupTransport]);

  return {
    enabled: NATIVE_REALTIME_VOICE_ENABLED,
    status,
    active: !['idle', 'error'].includes(status),
    turn,
    error,
    trace,
    threadId,
    start,
    stop,
  };
}
