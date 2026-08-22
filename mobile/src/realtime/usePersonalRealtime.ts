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
import { currentAuthStorageGeneration, useAuth } from '../auth/AuthContext';
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
  safePersonalRealtimeErrorMessage,
  realtimeStatusForEvent,
  realtimeToolContinuationPolicy,
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
import {
  PersonalRealtimeTerminalLatch,
  personalRealtimeAppLifecycleAction,
} from './personalRealtimeAppLifecycle';
import {
  privateRealtimeVoiceIsQualified,
  type NativeClientConfig,
} from './personalRealtimeQualification';
import { nativeClientConfigCache } from './nativeClientConfig';
import {
  personalRealtimeStartAuthorityIsCurrent,
  runPersonalRealtimeGuardedStage,
} from './personalRealtimeStartAuthority';
import {
  PersonalRealtimeLeaseWatchdog,
  personalRealtimeLeaseTiming,
  type PersonalRealtimeLeaseWatchdogSnapshot,
} from './personalRealtimeLeaseWatchdog';

type DataChannel = ReturnType<RTCPeerConnection['createDataChannel']>;

export type PersonalRealtimeTurn = {
  question: string;
  answer: string;
};

const STATS_INTERVAL_MS = 100;
const NATIVE_MEDIA_OPERATION_TIMEOUT_MS = 2_500;
const PERSONAL_REALTIME_RECONNECT_DELAYS_MS = [500, 1_500] as const;
const PERSONAL_REALTIME_LEASE_RENEW_MS = 10_000;
const PERSONAL_REALTIME_QUALIFICATION_POLL_MS = 30_000;

type PersonalRealtimeQualification = 'checking' | 'qualified' | 'unqualified';

type PersonalRealtimeReconnectBinding = {
  voiceSessionId: string;
  threadId: string;
  transportRevision: number;
  attempt: number;
};

export type PersonalRealtimeStartBinding = {
  threadId: string;
};

function newVoiceSessionId(): string {
  const random = Math.random().toString(36).slice(2, 14);
  return `voice-${Date.now().toString(36)}-${random}`;
}

function userFacingRealtimeError(error: unknown): string {
  if (error instanceof NativeMediaOperationTimeoutError) {
    return 'Scout voice audio did not respond. Please try again.';
  }
  if (error instanceof BonfireApiError) return safePersonalRealtimeErrorMessage(error.message);
  if (error instanceof Error) return safePersonalRealtimeErrorMessage(error.message);
  return safePersonalRealtimeErrorMessage(null);
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
  const [tearingDown, setTearingDown] = useState(false);
  const [qualification, setQualification] = useState<PersonalRealtimeQualification>(
    NATIVE_REALTIME_VOICE_ENABLED && sessionToken ? 'checking' : 'unqualified',
  );
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
  const voiceAuthorityTokenRef = useRef('');
  const voiceTransportRevisionRef = useRef(0);
  const voiceLeaseTokenRef = useRef('');
  const voiceLeaseGenerationRef = useRef(0);
  const voiceLeaseExpiresAtRef = useRef('');
  const voiceLeaseRenewTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const voiceLeaseRenewAbortControllerRef = useRef<AbortController | null>(null);
  const voiceLeaseWatchdogRef = useRef<PersonalRealtimeLeaseWatchdog | null>(null);
  if (voiceLeaseWatchdogRef.current === null) {
    voiceLeaseWatchdogRef.current = new PersonalRealtimeLeaseWatchdog();
  }
  const milestoneOperationIdsRef = useRef(new Map<string, string>());
  const pendingMilestonesRef = useRef(new Set<string>());
  const activeTransportRevisionRef = useRef<{ generation: number; revision: number } | null>(null);
  const reconnectBindingRef = useRef<PersonalRealtimeReconnectBinding | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectIntentRef = useRef(0);
  const reconnectRunnerRef = useRef<(binding: PersonalRealtimeReconnectBinding) => void>(() => undefined);
  const authorityTokenRef = useRef(sessionToken);
  const qualifiedAuthorityTokenRef = useRef('');
  const qualificationEpochRef = useRef(0);
  const authorityTransitionsRef = useRef<Array<{ previousToken: string | null }>>([]);
  const tearingDownRef = useRef(false);
  const stopInFlightRef = useRef<Promise<void> | null>(null);
  const appStateRef = useRef(AppState.currentState);
  const lifecycleStopLatchRef = useRef(new PersonalRealtimeTerminalLatch());
  const toolAbortControllerRef = useRef<AbortController | null>(null);
  const statsTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const smoothedLevelRef = useRef(0);
  const durableToolAnswerRef = useRef('');
  const onActionsRef = useRef(options.onActions);

  // React effects run after commit. Authentication authority does not get that
  // grace period: a late qualification/control promise can settle between the
  // auth render and its passive cleanup. Advance the transport generation and
  // clear qualification synchronously as soon as this render observes a new
  // token. Explicit logout/401 additionally invalidates AudioFocusCoordinator
  // at AuthContext's own synchronous linearization point.
  if (authorityTokenRef.current !== sessionToken) {
    const previousToken = authorityTokenRef.current;
    authorityTokenRef.current = sessionToken;
    qualifiedAuthorityTokenRef.current = '';
    qualificationEpochRef.current += 1;
    generationRef.current += 1;
    terminalRequestRef.current += 1;
    authorityTransitionsRef.current.push({ previousToken });
  }
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
      voiceAuthorityTokenRef.current = '';
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
        const wasTearingDown = tearingDownRef.current;
        tearingDownRef.current = false;
        setTrace(emptyTrace());
        setTearingDown(false);
        if (statusRef.current !== 'error' || wasTearingDown) setLiveStatus('idle');
      }
      return;
    }
    generationRef.current += 1;
    if (mediaSessionGenerationRef.current === expectedMediaSessionGeneration) {
      mediaSessionGenerationRef.current = null;
    }
    if (statsTimerRef.current) clearInterval(statsTimerRef.current);
    statsTimerRef.current = null;
    if (voiceLeaseRenewTimerRef.current) clearInterval(voiceLeaseRenewTimerRef.current);
    voiceLeaseRenewTimerRef.current = null;
    voiceLeaseWatchdogRef.current?.clear();
    const leaseRenewAbortController = voiceLeaseRenewAbortControllerRef.current;
    voiceLeaseRenewAbortControllerRef.current = null;
    leaseRenewAbortController?.abort();
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
    const serverAuthorityToken = voiceAuthorityTokenRef.current;
    const serverLease = {
      voiceSessionId: voiceSessionIdRef.current,
      threadId: voiceThreadIdRef.current,
      leaseToken: voiceLeaseTokenRef.current,
      leaseGeneration: voiceLeaseGenerationRef.current,
      transportRevision: voiceTransportRevisionRef.current,
    };
    if (!preserveLogicalBinding) {
      voiceSessionIdRef.current = '';
      voiceThreadIdRef.current = '';
      voiceAuthorityTokenRef.current = '';
      voiceTransportRevisionRef.current = 0;
    }
    voiceLeaseTokenRef.current = '';
    voiceLeaseGenerationRef.current = 0;
    voiceLeaseExpiresAtRef.current = '';
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
    if (
      serverAuthorityToken
      && serverLease.voiceSessionId
      && serverLease.threadId
      && serverLease.leaseToken
      && serverLease.leaseGeneration > 0
      && serverLease.transportRevision > 0
    ) {
      void api.realtimeLeaseStop(serverAuthorityToken, {
        ...serverLease,
        operationId: createConversationOperationId(),
      }).catch(() => undefined);
    }
    if (publishIdle && mountedRef.current) {
      const wasTearingDown = tearingDownRef.current;
      tearingDownRef.current = false;
      setTrace(emptyTrace());
      setTearingDown(false);
      if (statusRef.current !== 'error' || wasTearingDown) setLiveStatus('idle');
    }
  }, [setLiveStatus]);

  const terminateTransportWithError = useCallback((
    connectionGeneration: number,
    message: string,
    reconnectEligible = true,
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
      reconnectEligible
      &&
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
    if (!reconnectEligible) {
      cancelReconnect(false);
      tearingDownRef.current = true;
      if (mountedRef.current) setTearingDown(true);
    }
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
        if (!reconnectEligible) {
          tearingDownRef.current = false;
          setTearingDown(false);
        }
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
    const leaseToken = voiceLeaseTokenRef.current;
    const leaseGeneration = voiceLeaseGenerationRef.current;
    if (!voiceSessionId || !threadId || !leaseToken || leaseGeneration <= 0 || transportRevision <= 0) {
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
      leaseToken,
      leaseGeneration,
      operationId,
      milestone,
    }).catch(() => undefined);
  }, [sessionToken]);

  const sendToolOutput = useCallback((callId: string, output: Record<string, unknown>): boolean => (
    sendEvent({
      type: 'conversation.item.create',
      item: {
        type: 'function_call_output',
        call_id: callId,
        output: JSON.stringify(output),
      },
    })
  ), [sendEvent]);

  const sendSpokenToolContinuation = useCallback((instructions: string): boolean => {
    if (!instructions.trim()) return false;
    return sendEvent({
      type: 'response.create',
      response: {
        output_modalities: ['audio'],
        tool_choice: 'none',
        instructions,
      },
    });
  }, [sendEvent]);

  const handleToolCall = useCallback(async (
    call: RealtimeFunctionCall,
    connectionGeneration: number,
  ): Promise<boolean> => {
    const lease = leaseRef.current;
    const toolAbortController = toolAbortControllerRef.current;
    if (
      !sessionToken
      || !lease?.isCurrent()
      || !toolAbortController
      || toolAbortController.signal.aborted
      || handledCallsRef.current.has(call.callId)
    ) return false;
    handledCallsRef.current.add(call.callId);
    setLiveStatus(
      call.name === 'do_nothing' || call.name === 'route_conversation_turn'
        ? 'thinking'
        : 'acting',
    );
    let argumentsValue: Record<string, unknown> = {};
    try {
      const parsed = call.argumentsText.trim() ? JSON.parse(call.argumentsText) : {};
      if (!isRecord(parsed)) throw new Error('arguments must be an object');
      argumentsValue = parsed;
    } catch {
      return sendToolOutput(call.callId, {
        ok: false,
        outcome: 'unavailable',
        message: "I couldn't safely read that voice turn. Please try again.",
        error: 'could not read tool arguments',
      });
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
        return sendToolOutput(call.callId, {
          ok: false,
          outcome: 'unavailable',
          message: "I couldn't safely complete that voice turn. Nothing else was launched.",
          error: 'Scout could not re-establish its authenticated control channel.',
        });
      }
      if (
        generationRef.current !== connectionGeneration
        || leaseRef.current !== lease
        || !lease.isCurrent()
        || toolAbortController.signal.aborted
      ) return false;
      const response = await api.realtimeTool(
        sessionToken,
        voiceSessionIdRef.current,
        voiceThreadIdRef.current,
        call.callId,
        call.name,
        argumentsValue,
        {
          leaseToken: voiceLeaseTokenRef.current,
          leaseGeneration: voiceLeaseGenerationRef.current,
          transportRevision: voiceTransportRevisionRef.current,
        },
        toolAbortController.signal,
      );
      if (
        generationRef.current !== connectionGeneration
        || leaseRef.current !== lease
        || !lease.isCurrent()
        || toolAbortController.signal.aborted
      ) return false;
      if (response.actions?.length) onActionsRef.current?.(response.actions);
      const result = { ...(response.result ?? { ok: response.ok !== false }) };
      if ((response.ok === false || result.ok === false) && !String(result.message ?? '').trim()) {
        result.outcome = 'unavailable';
        result.message = "I couldn't safely complete that voice turn. Nothing else was launched.";
      }
      const durableMessage = isRecord(result) ? String(result.message ?? '').trim() : '';
      if (durableMessage && mountedRef.current) {
        durableToolAnswerRef.current = durableMessage;
        setTurn((current) => ({ question: current?.question ?? '', answer: durableMessage }));
      }
      return sendToolOutput(call.callId, result);
    } catch (toolError) {
      if (
        generationRef.current !== connectionGeneration
        || leaseRef.current !== lease
        || !lease.isCurrent()
        || toolAbortController.signal.aborted
      ) return false;
      return sendToolOutput(call.callId, {
        ok: false,
        outcome: 'unavailable',
        message: "I couldn't safely complete that voice turn. Nothing else was launched.",
        error: userFacingRealtimeError(toolError),
      });
    }
  }, [sendToolOutput, sessionToken, setLiveStatus]);

  const continueToolCalls = useCallback(async (
    calls: RealtimeFunctionCall[],
    connectionGeneration: number,
  ) => {
    if (
      generationRef.current !== connectionGeneration
      || dataChannelRef.current?.readyState !== 'open'
    ) return;
    const continuation = realtimeToolContinuationPolicy(calls);
    if (!continuation.valid) {
      // A Realtime response is one accepted user turn and may resolve to one
      // server route or one no-effect decision—never a batch of effects. Settle
      // every provider call so the conversation stays coherent, but execute
      // none of them when that invariant is violated.
      for (const call of calls) {
        if (!call.callId || handledCallsRef.current.has(call.callId)) continue;
        handledCallsRef.current.add(call.callId);
        sendToolOutput(call.callId, {
          ok: false,
          outcome: 'unavailable',
          message: continuation.failureMessage,
        });
      }
      if (continuation.shouldRespond) {
        sendSpokenToolContinuation(continuation.instructions);
        setLiveStatus('error');
      } else {
        setLiveStatus('listening');
      }
      return;
    }
    for (const call of calls) {
      if (!await handleToolCall(call, connectionGeneration)) return;
    }
    if (generationRef.current !== connectionGeneration || dataChannelRef.current?.readyState !== 'open') return;
    if (!continuation.shouldRespond) {
      setLiveStatus('listening');
      return;
    }
    // The server-created session remains tool_choice=required for the next
    // completed user utterance. Override only this continuation so Scout can
    // speak the exact durable router result without recursively routing it.
    sendSpokenToolContinuation(continuation.instructions);
  }, [handleToolCall, sendSpokenToolContinuation, sendToolOutput, setLiveStatus]);

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
      if (transcript.role === 'user') {
        durableToolAnswerRef.current = '';
        setTurn({ question: transcript.text, answer: '' });
      } else {
        const answer = durableToolAnswerRef.current || transcript.text;
        setTurn((current) => ({ question: current?.question ?? '', answer }));
      }
    }
    const toolCalls = realtimeFunctionCalls(event);
    if (toolCalls.length) void continueToolCalls(toolCalls, connectionGeneration);
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
      const providerError = safePersonalRealtimeErrorMessage(
        isRecord(event.error) ? event.error.message : null,
        'Scout voice needs attention.',
      );
      publishMilestone('transport_error', connectionGeneration);
      terminateTransportWithError(connectionGeneration, providerError);
      return;
    }
    if (sessionToken && (type.includes('response.audio') || type.includes('output_audio_buffer.started'))) {
      publishMilestone('first_audio', connectionGeneration);
    }
    setLiveStatus(realtimeStatusForEvent(type, statusRef.current));
  }, [continueToolCalls, publishMilestone, sessionToken, setLiveStatus, terminateTransportWithError]);

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

  const armLeaseExpiryWatchdog = useCallback((
    snapshot: PersonalRealtimeLeaseWatchdogSnapshot,
  ) => voiceLeaseWatchdogRef.current?.arm(snapshot, (deadline) => {
    if (
      generationRef.current !== deadline.connectionGeneration
      || voiceLeaseGenerationRef.current !== deadline.leaseGeneration
      || voiceLeaseTokenRef.current !== deadline.leaseToken
      || voiceLeaseExpiresAtRef.current !== deadline.leaseExpiresAt
    ) return;
    terminateTransportWithError(
      deadline.connectionGeneration,
      'Scout voice\'s private session expired. Tap to retry.',
      false,
    );
  }) ?? null, [terminateTransportWithError]);

  const startLeaseRenewal = useCallback((connectionGeneration: number) => {
    if (voiceLeaseRenewTimerRef.current) clearInterval(voiceLeaseRenewTimerRef.current);
    voiceLeaseRenewTimerRef.current = setInterval(() => {
      const binding = {
        voiceSessionId: voiceSessionIdRef.current,
        threadId: voiceThreadIdRef.current,
        leaseToken: voiceLeaseTokenRef.current,
        leaseGeneration: voiceLeaseGenerationRef.current,
        transportRevision: voiceTransportRevisionRef.current,
      };
      const leaseExpiresAt = voiceLeaseExpiresAtRef.current;
      if (
        !sessionToken
        || generationRef.current !== connectionGeneration
        || !binding.voiceSessionId
        || !binding.threadId
        || !binding.leaseToken
        || binding.leaseGeneration <= 0
        || binding.transportRevision <= 0
      ) return;
      if (voiceLeaseRenewAbortControllerRef.current) return;
      const leaseTiming = personalRealtimeLeaseTiming(
        leaseExpiresAt,
        voiceLeaseWatchdogRef.current?.now() ?? Date.now(),
      );
      if (!leaseTiming) {
        terminateTransportWithError(
          connectionGeneration,
          'Scout voice\'s private session expired. Tap to retry.',
          false,
        );
        return;
      }
      const renewAbortController = new AbortController();
      voiceLeaseRenewAbortControllerRef.current = renewAbortController;
      void api.realtimeLeaseRenew(sessionToken, {
        ...binding,
        operationId: createConversationOperationId(),
      }, {
        signal: renewAbortController.signal,
        timeoutMs: leaseTiming.renewRequestTimeoutMs,
      }).then((result) => {
        if (voiceLeaseRenewAbortControllerRef.current === renewAbortController) {
          voiceLeaseRenewAbortControllerRef.current = null;
        }
        const renewedLeaseExpiresAt = String(result.leaseExpiresAt || '');
        if (
          generationRef.current !== connectionGeneration
          || voiceLeaseGenerationRef.current !== binding.leaseGeneration
          || voiceLeaseTokenRef.current !== binding.leaseToken
          || voiceLeaseExpiresAtRef.current !== leaseExpiresAt
        ) return;
        const renewedExpiryMs = Date.parse(renewedLeaseExpiresAt);
        if (
          !Number.isFinite(renewedExpiryMs)
          || renewedExpiryMs <= Date.parse(leaseExpiresAt)
        ) {
          terminateTransportWithError(
            connectionGeneration,
            'Scout voice could not renew its private session. Tap to retry.',
            false,
          );
          return;
        }
        voiceLeaseExpiresAtRef.current = renewedLeaseExpiresAt;
        const armed = armLeaseExpiryWatchdog({
          connectionGeneration,
          leaseGeneration: binding.leaseGeneration,
          leaseToken: binding.leaseToken,
          leaseExpiresAt: renewedLeaseExpiresAt,
        });
        if (!armed) {
          terminateTransportWithError(
            connectionGeneration,
            'Scout voice\'s private session expired. Tap to retry.',
            false,
          );
        }
      }).catch((renewError) => {
        if (voiceLeaseRenewAbortControllerRef.current === renewAbortController) {
          voiceLeaseRenewAbortControllerRef.current = null;
        }
        if (generationRef.current !== connectionGeneration) return;
        terminateTransportWithError(
          connectionGeneration,
          renewError instanceof BonfireApiError
            ? userFacingRealtimeError(renewError)
            : 'Scout voice could not renew its private session. Tap to retry.',
          false,
        );
      });
    }, PERSONAL_REALTIME_LEASE_RENEW_MS);
  }, [armLeaseExpiryWatchdog, sessionToken, terminateTransportWithError]);

  const loadQualifiedClientConfig = useCallback(async (
    force = false,
  ): Promise<NativeClientConfig | null> => {
    if (!NATIVE_REALTIME_VOICE_ENABLED || !sessionToken) {
      if (qualifiedAuthorityTokenRef.current) qualificationEpochRef.current += 1;
      qualifiedAuthorityTokenRef.current = '';
      if (mountedRef.current) setQualification('unqualified');
      return null;
    }
    const authorityToken = sessionToken;
    const clientConfig = await nativeClientConfigCache.load(authorityToken, { force });
    if (
      !mountedRef.current
      || authorityTokenRef.current !== authorityToken
      || sessionToken !== authorityToken
    ) return null;
    if (!privateRealtimeVoiceIsQualified(clientConfig)) {
      if (qualifiedAuthorityTokenRef.current === authorityToken) {
        qualificationEpochRef.current += 1;
      }
      qualifiedAuthorityTokenRef.current = '';
      setQualification('unqualified');
      return null;
    }
    qualifiedAuthorityTokenRef.current = authorityToken;
    setQualification('qualified');
    return clientConfig;
  }, [sessionToken]);

  const start = useCallback(async (binding?: PersonalRealtimeReconnectBinding | PersonalRealtimeStartBinding) => {
    if (
      !NATIVE_REALTIME_VOICE_ENABLED
      || !sessionToken
      || qualifiedAuthorityTokenRef.current !== sessionToken
    ) return;
    const reconnecting = Boolean(binding && 'transportRevision' in binding);
    const reconnectBinding = reconnecting ? binding as PersonalRealtimeReconnectBinding : undefined;
    const requestedThreadId = String(binding?.threadId ?? '').trim();
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
      durableToolAnswerRef.current = '';
      setThreadId(requestedThreadId || null);
    }
    setLiveStatus('connecting');
    const connectionGeneration = ++generationRef.current;
    const authStorageGeneration = currentAuthStorageGeneration();
    const startAuthority = {
      sessionToken,
      authStorageGeneration,
      connectionGeneration,
      qualificationEpoch: qualificationEpochRef.current,
    };
    const startAuthorityIsCurrent = () => personalRealtimeStartAuthorityIsCurrent(
      startAuthority,
      {
        mounted: mountedRef.current,
        liveSessionToken: authorityTokenRef.current,
        qualifiedAuthorityToken: qualifiedAuthorityTokenRef.current,
        authStorageGeneration: currentAuthStorageGeneration(),
        connectionGeneration: generationRef.current,
        qualificationEpoch: qualificationEpochRef.current,
      },
    );
    let qualifiedClientConfig: NativeClientConfig | null;
    try {
      // Reconfirm the server-owned release gate for every fresh transport. The
      // shared cache coalesces a simultaneous foreground/poll refresh, and no
      // microphone or native audio work begins before this returns true.
      const qualificationStage = await runPersonalRealtimeGuardedStage({
        isCurrent: startAuthorityIsCurrent,
        run: () => loadQualifiedClientConfig(true),
      });
      if (!qualificationStage.current) return;
      qualifiedClientConfig = qualificationStage.value;
    } catch (qualificationError) {
      if (
        startAuthorityIsCurrent()
      ) {
        setError(userFacingRealtimeError(qualificationError));
        setLiveStatus('error');
      }
      return;
    }
    if (!qualifiedClientConfig) {
      if (
        startAuthorityIsCurrent()
      ) setLiveStatus('idle');
      return;
    }
    const controlStage = await runPersonalRealtimeGuardedStage({
      isCurrent: startAuthorityIsCurrent,
      run: () => waitForOfficeControlChannel(
        sessionToken,
        () => (
          mountedRef.current
          && startAuthorityIsCurrent()
          && statusRef.current === 'connecting'
        ),
      ),
    });
    if (!controlStage.current) return;
    const controlReady = controlStage.value;
    if (!controlReady) {
      cancelReconnect();
      setError('Scout voice could not reach its control channel. Please try again.');
      setLiveStatus('error');
      return;
    }
    const voiceSessionId = reconnectBinding?.voiceSessionId ?? newVoiceSessionId();
    const expectedThreadId = requestedThreadId;
    const previousTransportRevision = reconnectBinding?.transportRevision ?? 0;
    voiceSessionIdRef.current = voiceSessionId;
    voiceThreadIdRef.current = expectedThreadId;
    voiceAuthorityTokenRef.current = sessionToken;
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
      const leaseStage = await runPersonalRealtimeGuardedStage({
        isCurrent: startAuthorityIsCurrent,
        run: () => audioFocusRuntime.acquire('personal_realtime', {
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
        }),
        retireStale: (staleLease) => releasePersonalRealtimeTerminalFocus(
          staleLease,
          cleanupSessionTransport,
          'cancelled',
        ),
      });
      if (!leaseStage.current) return;
      const lease = leaseStage.value;
      if (
        !startAuthorityIsCurrent()
        || !lease.isCurrent()
        || !officeControlChannelIsLive(sessionToken)
      ) {
        await releasePersonalRealtimeTerminalFocus(
          lease,
          cleanupSessionTransport,
          'cancelled',
        ).catch(() => undefined);
        return;
      }
      leaseRef.current = lease;
      toolAbortControllerRef.current = new AbortController();
      const capturePromise = Promise.resolve()
        .then(() => {
          if (!startAuthorityIsCurrent() || !lease.isCurrent()) {
            throw new Error('Scout voice start was cancelled.');
          }
          return mediaDevices.getUserMedia({ audio: true, video: false });
        })
        .then((capture) => {
        if (!startAuthorityIsCurrent() || !lease.isCurrent()) {
          capture.getTracks().forEach((track) => track.stop());
        } else {
          streamRef.current = capture;
        }
        return capture;
      });
      const startup = drainPersonalRealtimeStartup(
        Promise.resolve(qualifiedClientConfig),
        capturePromise,
        Promise.resolve().then(async () => {
          if (!startAuthorityIsCurrent() || !lease.isCurrent()) {
            throw new Error('Scout voice start was cancelled.');
          }
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
      const [startupClientConfig, stream] = await startup;
      startupDrain = null;
      if (
        !startAuthorityIsCurrent()
        || !lease.isCurrent()
        || !officeControlChannelIsLive(sessionToken)
      ) {
        stream.getTracks().forEach((track) => track.stop());
        // A token transition may have made this lease stale before its queued
        // coordinator acquisition settled. If release reports false, retire
        // only this attempt's exact native generation and detached resources;
        // cleanup scope prevents it from touching any replacement session.
        await releasePersonalRealtimeTerminalFocus(
          lease,
          cleanupSessionTransport,
          'cancelled',
        ).catch(() => undefined);
        return;
      }
      const peer = new RTCPeerConnection({
        iceServers: (startupClientConfig.rtcConfiguration?.iceServers ?? []) as never,
        bundlePolicy: 'max-bundle',
        rtcpMuxPolicy: 'require',
      });
      peerRef.current = peer;
      peer.ontrack = (event: unknown) => {
        if (peerRef.current !== peer || !startAuthorityIsCurrent()) return;
        const trackEvent = event as { streams?: MediaStream[]; track?: { enabled: boolean } };
        const streamValue = trackEvent.streams?.[0];
        if (streamValue) remoteStreamRef.current = streamValue;
        if (trackEvent.track) trackEvent.track.enabled = true;
        publishMilestone('remote_track', connectionGeneration);
      };
      peer.onconnectionstatechange = () => {
        if (peerRef.current !== peer || !startAuthorityIsCurrent()) return;
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
        if (startAuthorityIsCurrent() && dataChannelRef.current === dataChannel) {
          publishMilestone('data_channel_open', connectionGeneration);
          setLiveStatus('listening');
        }
      };
      dataChannel.onmessage = (event: unknown) => {
        if (!startAuthorityIsCurrent() || dataChannelRef.current !== dataChannel) return;
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
      if (!startAuthorityIsCurrent() || peerRef.current !== peer) return;
      await peer.setLocalDescription(offer);
      if (!startAuthorityIsCurrent() || peerRef.current !== peer) return;
      const localSDP = await waitForOfferSDP(peer, offer);
      if (
        !startAuthorityIsCurrent()
        || peerRef.current !== peer
        || !officeControlChannelIsLive(sessionToken)
      ) return;
      const answer = await api.realtimeOffer(
        sessionToken,
        localSDP,
        voiceSessionId,
        createConversationOperationId(),
        expectedThreadId,
      );
      if (
        !startAuthorityIsCurrent()
        || peerRef.current !== peer
        || !lease.isCurrent()
        || !officeControlChannelIsLive(sessionToken)
      ) return;
      if (
        answer.voiceSessionId !== voiceSessionId
        || !String(answer.threadId || '').trim()
        || !Number.isSafeInteger(answer.transportRevision)
        || answer.transportRevision <= previousTransportRevision
        || !String(answer.leaseToken || '').trim()
        || !Number.isSafeInteger(answer.leaseGeneration)
        || answer.leaseGeneration <= 0
        || !Number.isFinite(new Date(answer.leaseExpiresAt).getTime())
        || (reconnecting && answer.threadId !== expectedThreadId)
        || (!reconnecting && expectedThreadId && answer.threadId !== expectedThreadId)
      ) {
        throw new Error('Scout voice did not bind its private transcript.');
      }
      voiceThreadIdRef.current = answer.threadId;
      voiceTransportRevisionRef.current = answer.transportRevision;
      voiceLeaseTokenRef.current = answer.leaseToken;
      voiceLeaseGenerationRef.current = answer.leaseGeneration;
      voiceLeaseExpiresAtRef.current = answer.leaseExpiresAt;
      const initialLeaseWatchdog = armLeaseExpiryWatchdog({
        connectionGeneration,
        leaseGeneration: answer.leaseGeneration,
        leaseToken: answer.leaseToken,
        leaseExpiresAt: answer.leaseExpiresAt,
      });
      if (!initialLeaseWatchdog) {
        throw new Error('Scout voice received an expired private session.');
      }
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
        !startAuthorityIsCurrent()
        || peerRef.current !== peer
        || !lease.isCurrent()
      ) return;
      startLeaseRenewal(connectionGeneration);
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
  }, [armLeaseExpiryWatchdog, cancelReconnect, cleanupTransport, handleProviderEvent, loadQualifiedClientConfig, publishMilestone, queueReconnect, sessionToken, setLiveStatus, startLeaseRenewal, startStats, terminateTransportWithError]);

  reconnectRunnerRef.current = (binding) => {
    void start(binding);
  };

  const stop = useCallback((
    reason: AudioFocusTerminalReason = 'completed',
  ): Promise<void> => {
    if (stopInFlightRef.current) return stopInFlightRef.current;
    const representsLiveMedia = (
      tearingDownRef.current
      || statusRef.current !== 'idle'
      || leaseRef.current !== null
      || mediaSessionGenerationRef.current !== null
      || streamRef.current !== null
    );
    if (representsLiveMedia) {
      tearingDownRef.current = true;
      if (mountedRef.current) setTearingDown(true);
    }
    const operation = (async () => {
      terminalRequestRef.current += 1;
      // Keep the exact server binding until cleanup snapshots it for Stop.
      cancelReconnect(false);
      const lease = leaseRef.current;
      leaseRef.current = null;
      try {
        await releasePersonalRealtimeTerminalFocus(lease, cleanupTransport, reason);
      } catch (stopError) {
        if (mountedRef.current) {
          setError(userFacingRealtimeError(stopError));
          setLiveStatus('error');
        }
        // A failed or timed-out native terminal path is not yet safe to depict
        // as microphone-off. Keep the indicator and Stop affordance available
        // so the exact-generation finalizer can finish or the user can retry.
        return;
      }
      tearingDownRef.current = false;
      if (mountedRef.current) {
        setThreadId(null);
        setLiveStatus('idle');
        setTearingDown(false);
      }
    })();
    let trackedOperation!: Promise<void>;
    trackedOperation = operation.finally(() => {
      if (stopInFlightRef.current === trackedOperation) stopInFlightRef.current = null;
    });
    stopInFlightRef.current = trackedOperation;
    return trackedOperation;
  }, [cancelReconnect, cleanupTransport, setLiveStatus]);

  useEffect(() => {
    if (!authorityTransitionsRef.current.length) return;
    const transitions = authorityTransitionsRef.current.splice(0);
    setQualification(NATIVE_REALTIME_VOICE_ENABLED && sessionToken ? 'checking' : 'unqualified');
    for (const transition of transitions) {
      nativeClientConfigCache.clear(transition.previousToken ?? undefined);
    }
    setThreadId(null);
    // A private voice transcript is authorized to exactly one signed-in
    // account. Moving this hook above navigation must never let a transport or
    // its bound thread survive an account/session boundary.
    void stop('cancelled');
  }, [sessionToken, stop]);

  useEffect(() => {
    if (!NATIVE_REALTIME_VOICE_ENABLED || !sessionToken) {
      if (qualifiedAuthorityTokenRef.current) qualificationEpochRef.current += 1;
      qualifiedAuthorityTokenRef.current = '';
      setQualification('unqualified');
      return undefined;
    }
    let disposed = false;
    const refresh = async (force: boolean) => {
      try {
        await loadQualifiedClientConfig(force);
      } catch {
        if (
          !disposed
          && authorityTokenRef.current === sessionToken
          && qualifiedAuthorityTokenRef.current !== sessionToken
        ) setQualification('checking');
      }
    };
    void refresh(false);
    const timer = setInterval(() => {
      if (AppState.currentState === 'active') void refresh(true);
    }, PERSONAL_REALTIME_QUALIFICATION_POLL_MS);
    return () => {
      disposed = true;
      clearInterval(timer);
    };
  }, [loadQualifiedClientConfig, sessionToken]);

  useEffect(() => {
    if (qualification === 'unqualified' && statusRef.current !== 'idle') {
      void stop('cancelled');
    }
  }, [qualification, stop]);

  useEffect(() => () => {
    mountedRef.current = false;
    cancelReconnect(false);
    const lease = leaseRef.current;
    leaseRef.current = null;
    if (lease) void lease.release('cancelled').catch(() => undefined);
    else void cleanupTransport().catch(() => undefined);
  }, [cancelReconnect, cleanupTransport]);

  useEffect(() => {
    const subscription = AppState.addEventListener('change', (nextState) => {
      const previousState = appStateRef.current;
      appStateRef.current = nextState;
      if (nextState === 'active' && sessionToken) {
        lifecycleStopLatchRef.current.rearm();
        void loadQualifiedClientConfig(true).catch(() => undefined);
      }
      if (personalRealtimeAppLifecycleAction(previousState, nextState, statusRef.current) === 'stop') {
        void lifecycleStopLatchRef.current.run(() => stop('cancelled'));
      }
    });
    return () => subscription.remove();
  }, [loadQualifiedClientConfig, sessionToken, stop]);

  return {
    enabled: NATIVE_REALTIME_VOICE_ENABLED
      && qualification === 'qualified'
      && Boolean(sessionToken)
      && qualifiedAuthorityTokenRef.current === sessionToken,
    status,
    active: !['idle', 'error'].includes(status),
    tearingDown,
    turn,
    error,
    trace,
    threadId,
    start,
    stop,
  };
}
