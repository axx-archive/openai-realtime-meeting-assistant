import { useCallback, useEffect, useRef, useState } from 'react';
import * as Haptics from 'expo-haptics';
import { File } from 'expo-file-system';
import {
  AudioModule,
  RecordingPresets,
  setAudioModeAsync,
  useAudioRecorder,
  useAudioRecorderState,
  type RecordingOptions,
} from 'expo-audio';
import { api, BonfireApiError } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { meteringIntervalMs } from '../theme/motion';
import { emptyTrace, normalizeMetering, pushTrace, smoothAmplitude } from './amplitude';
import type { AudioFocusLease } from './AudioFocusCoordinator';
import {
  beginDictationCapture,
  finishDictationCapture,
  settleDictationFocusLease,
  type DictationFinishResult,
} from './dictationAudioLifecycle';
import { canSendDictation, type DictationLifecycleState } from './dictationLifecycle';

/**
 * Recorded composer dictation — design §11.
 *
 *   idle ──record──▶ listening ──stop──▶ held ──send──▶ transcribing ──▶ landed
 *     ▲               │             │              │
 *     └──slide-away───┴──delete─────┴──delete──────┴──error
 *        (cancel)                                 (audio RETAINED)
 *
 * The invariant that matters: a failure never discards the recording. Losing a
 * user's spoken paragraph to a flaky network is unforgivable, so the audio stays
 * on disk and the error path hands back a retry that re-uploads the same file.
 */

export type DictationState = DictationLifecycleState;

/** Mirrors the server's cap so a stuck recorder can't bill a surprise. */
export const MAX_DICTATION_MS = 600_000;

function deleteDictationFile(uri: string | null | undefined): void {
	if (!uri) return;
	try {
		const file = new File(uri);
		if (file.exists) file.delete();
	} catch {
		// Privacy cleanup is best-effort at the filesystem boundary. The file is
		// never intentionally retained after success, cancel, or discard.
	}
}

/**
 * Speech, not music: mono at a modest bitrate is indistinguishable to a
 * transcription model and roughly a quarter the upload of the stereo preset,
 * which is the difference between a snappy and a sluggish release on cellular.
 *
 * `directory: 'document'` is load-bearing — the cache directory can be evicted
 * by iOS under storage pressure, and an evicted pending dictation is exactly the
 * lost paragraph this design promises never to lose (§12.5).
 */
const DICTATION_RECORDING: RecordingOptions = {
  ...RecordingPresets.HIGH_QUALITY,
  isMeteringEnabled: true,
  directory: 'document',
  numberOfChannels: 1,
  bitRate: 64_000,
};

export type DictationResult = {
  text: string;
  /** False when the server fell back to un-biased transcription (whisper pin). */
  biased: boolean;
};

export type DictationUploadAttempt = {
  generation: number;
  uri: string;
};

/** Synchronous admission closes the same-render double-tap window. */
export function admitDictationUploadAttempt(
  activeAttempt: DictationUploadAttempt | null,
  uri: string,
  generation: number,
) {
  if (activeAttempt) return { admitted: false as const, attempt: activeAttempt };
  return { admitted: true as const, attempt: { generation, uri } };
}

/** A stale provider completion must never clear a newer admitted attempt. */
export function clearDictationUploadAttempt(
  activeAttempt: DictationUploadAttempt | null,
  exactAttempt: DictationUploadAttempt,
): DictationUploadAttempt | null {
  return activeAttempt === exactAttempt ? null : activeAttempt;
}

/** HTTP success without speech is retryable, not a successful transcription. */
export function hasUsableDictationTranscript(text: string): boolean {
  return text.trim().length > 0;
}

export type UseDictationOptions = {
  context?: 'chat' | 'board' | 'search';
  threadId?: string;
  onTranscript: (result: DictationResult) => void;
  /**
   * Temporary migration adapter for older surfaces that still upload on touch
   * release. It is intentionally opt-in and defaults off; new composers must
   * use Stop → held → Send.
  */
  legacyUploadOnStop?: boolean;
};

export function useDictation({ context = 'chat', threadId, onTranscript, legacyUploadOnStop = false }: UseDictationOptions) {
  const { sessionToken } = useAuth();
  const recorder = useAudioRecorder(DICTATION_RECORDING);
  const recorderState = useAudioRecorderState(recorder, meteringIntervalMs);

  const [state, setState] = useState<DictationState>('idle');
  const [amplitude, setAmplitude] = useState(0);
  /** Rolling history of the last ~3s of speech — drives the scrolling trace. */
  const [trace, setTrace] = useState<number[]>(emptyTrace);
  const [error, setError] = useState<string | null>(null);
  const [permissionDenied, setPermissionDenied] = useState(false);

  // Survives re-renders and the async gap between release and upload.
  const cancelledRef = useRef(false);
	const heldRef = useRef(false);
	const startingRef = useRef(false);
	const listeningRef = useRef(false);
  const startSettlementRef = useRef<Promise<void> | null>(null);
  const captureFocusLeaseRef = useRef<AudioFocusLease | null>(null);
  const pendingRef = useRef<{ uri: string; durationMs: number } | null>(null);
  const requestGenerationRef = useRef(0);
  const deliveredGenerationRef = useRef(0);
  const activeUploadAttemptRef = useRef<DictationUploadAttempt | null>(null);
  const onTranscriptRef = useRef(onTranscript);
  onTranscriptRef.current = onTranscript;

  const listening = state === 'listening';

  // Amplitude tracking. Only runs while listening — at rest the bars are static,
  // which is the breathe-only-while-listening law (design §8) enforced at the
  // data source rather than left to the view to remember.
  useEffect(() => {
    if (!listening) {
      setAmplitude(0);
      // A stopped clip keeps its final waveform while the user decides whether
      // to delete or send it. Clear only after the lifecycle returns to idle so
      // the next dictation still starts from silence.
      if (state === 'idle') setTrace(emptyTrace);
      return;
    }
    const sample = normalizeMetering(recorderState.metering);
    setAmplitude((previous) => {
      const smoothed = smoothAmplitude(previous, sample);
      setTrace((history) => pushTrace(history, smoothed));
      return smoothed;
    });
  }, [listening, recorderState.metering, state]);

  const upload = useCallback(
    async (recording: { uri: string; durationMs: number }): Promise<boolean> => {
      if (!sessionToken) {
        setState('error');
        setError('Sign in to dictate.');
        return false;
      }
      const generation = requestGenerationRef.current + 1;
      const admission = admitDictationUploadAttempt(
        activeUploadAttemptRef.current,
        recording.uri,
        generation,
      );
      if (!admission.admitted) return false;
      const exactAttempt = admission.attempt;
      activeUploadAttemptRef.current = exactAttempt;
      requestGenerationRef.current = generation;
      setState('transcribing');
      setError(null);
      try {
		const result = await api.transcribeDictation(sessionToken, recording, { context, threadId });
		if (
          activeUploadAttemptRef.current !== exactAttempt
          || generation !== requestGenerationRef.current
          || pendingRef.current?.uri !== recording.uri
        ) return false;
		if (!hasUsableDictationTranscript(result.text)) {
          // A 200 response with no text did not fulfill the user's request.
          // Keep the exact held file so Retry can submit it again.
          setState('error');
          setError('No speech was detected. Your recording is saved — retry or delete it.');
          return false;
        }
		deleteDictationFile(recording.uri);
		pendingRef.current = null;
        setState('idle');
        if (deliveredGenerationRef.current !== generation) {
          deliveredGenerationRef.current = generation;
          void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
          onTranscriptRef.current({ text: result.text, biased: result.biased });
        }
        return true;
      } catch (err) {
		if (
          activeUploadAttemptRef.current !== exactAttempt
          || generation !== requestGenerationRef.current
          || pendingRef.current?.uri !== recording.uri
        ) return false;
        // The recording stays in pendingRef so retry() can re-send the exact
        // same audio. Nothing is deleted on failure.
        setState('error');
        setError(err instanceof BonfireApiError ? err.message : 'Could not transcribe that.');
        return false;
      } finally {
        activeUploadAttemptRef.current = clearDictationUploadAttempt(
          activeUploadAttemptRef.current,
          exactAttempt,
        );
      }
    },
    [context, sessionToken, threadId],
  );

  const fenceFocusLease = useCallback((exactLease: AudioFocusLease) => {
    if (captureFocusLeaseRef.current === exactLease) captureFocusLeaseRef.current = null;
  }, []);

  const releaseFocusLease = useCallback(async (
    exactLease: AudioFocusLease | null | undefined,
    reason: 'completed' | 'cancelled' | 'error',
  ) => {
    if (exactLease && captureFocusLeaseRef.current !== exactLease) return null;
    return settleDictationFocusLease(exactLease, reason, fenceFocusLease);
  }, [fenceFocusLease]);

  const start = useCallback(async (captureFocusLease?: AudioFocusLease | null): Promise<boolean> => {
	// A held/error clip is deliberately valuable user data. A new recording
	// cannot silently overwrite it; the caller must Send or Delete first.
	if (startingRef.current || listeningRef.current || state !== 'idle') {
      await settleDictationFocusLease(captureFocusLease, 'cancelled', () => {});
      return false;
    }
	if (captureFocusLease) captureFocusLeaseRef.current = captureFocusLease;
	heldRef.current = true;
	startingRef.current = true;
    let settleStart!: () => void;
    const startSettlement = new Promise<void>((resolve) => { settleStart = resolve; });
    startSettlementRef.current = startSettlement;
    cancelledRef.current = false;
    setError(null);

	let started = false;
    let terminalReason: 'cancelled' | 'error' = 'error';
	try {
		const result = await beginDictationCapture({
        requestPermission: async () => (await AudioModule.requestRecordingPermissionsAsync()).granted,
        // allowsRecording routes the session to the mic; playsInSilentMode
        // keeps a spoken answer audible when the ringer switch is off.
        enableRecordingMode: () => setAudioModeAsync({ allowsRecording: true, playsInSilentMode: true }),
        prepare: () => recorder.prepareToRecordAsync(),
        record: () => recorder.record(),
        stillRequested: () => heldRef.current,
        stopPartialCapture: () => recorder.stop(),
        restoreAudioMode: () => setAudioModeAsync({ allowsRecording: false, playsInSilentMode: true }),
        discardPartialFile: () => deleteDictationFile(recorder.uri),
      });

      setPermissionDenied(result.status === 'permission_denied');
      if (result.status === 'started') {
        listeningRef.current = true;
        setState('listening');
        started = true;
        void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
      } else {
        terminalReason = result.status === 'failed' ? 'error' : 'cancelled';
        heldRef.current = false;
        cancelledRef.current = false;
        setState('idle');
        if (result.status === 'failed') {
          const cleanupIncomplete = result.cleanupFailures.length > 0;
          setError(cleanupIncomplete
            ? 'Could not start recording cleanly. Tap the mic to try again.'
            : 'Could not start recording. Tap the mic to try again.');
        } else if (result.status === 'cancelled' && result.cleanupFailures.length > 0) {
          setError('Recording cancelled, but the microphone did not close cleanly.');
        }
      }
	} catch {
      heldRef.current = false;
      cancelledRef.current = false;
      setPermissionDenied(false);
      setState('idle');
      setError('Could not start recording cleanly. Tap the mic to try again.');
    } finally {
		startingRef.current = false;
      settleStart();
      if (startSettlementRef.current === startSettlement) startSettlementRef.current = null;
      if (!started) {
        const releaseFailure = await releaseFocusLease(captureFocusLease, terminalReason);
        if (releaseFailure) setError('Could not restore meeting audio. Try the mic again.');
      }
	}
    return started;
  }, [recorder, releaseFocusLease, state]);

  /** Finish capture either as a held local clip or as an explicit Send. */
  const finishRecording = useCallback(async (sendAfterStop: boolean) => {
	heldRef.current = false;
	if (!listeningRef.current && startSettlementRef.current) await startSettlementRef.current;
	if (!listeningRef.current) return;
	listeningRef.current = false;
    const durationMs = recorderState.durationMillis;
    const exactLease = captureFocusLeaseRef.current;
    let finishResult: DictationFinishResult = { stopFailure: null, audioModeFailure: null };
    let releaseFailure: unknown | null = null;
    try {
      finishResult = await finishDictationCapture({
        stopAndUnload: () => recorder.stop(),
        restoreAudioMode: () => setAudioModeAsync({ allowsRecording: false, playsInSilentMode: true }),
      });
    } finally {
      // Release the exact capture generation before any upload. A parked room
      // regains its prior mute state even when stop/unload or audio reset fails.
      const reason = cancelledRef.current
        ? 'cancelled'
        : finishResult.stopFailure || finishResult.audioModeFailure
          ? 'error'
          : 'completed';
      releaseFailure = await releaseFocusLease(exactLease, reason);
    }

    if (finishResult.stopFailure) {
      deleteDictationFile(recorder.uri);
      cancelledRef.current = false;
      setState('idle');
      setError('Could not finish recording. Tap the mic to try again.');
      return;
    }

	if (cancelledRef.current) {
		deleteDictationFile(recorder.uri);
      cancelledRef.current = false;
      setState('idle');
      if (finishResult.audioModeFailure || releaseFailure) {
        setError('Recording cancelled, but meeting audio could not be restored cleanly.');
      }
      return;
    }
    const uri = recorder.uri;
	if (!uri || durationMs < 400) {
		deleteDictationFile(uri);
      // Below ~400ms this is a mistap, not speech. Silently returning to idle
      // beats surfacing an error for something the user did not intend to do.
      setState('idle');
      if (finishResult.audioModeFailure || releaseFailure) {
        setError('Could not restore meeting audio. Tap the mic to try again.');
      }
      return;
    }
    const recording = { uri, durationMs: Math.min(durationMs, MAX_DICTATION_MS) };
    pendingRef.current = recording;
    if (finishResult.audioModeFailure || releaseFailure) {
      setState('error');
      setError('Recording saved, but meeting audio could not be restored cleanly.');
      return;
    }
    setState('held');
    if (legacyUploadOnStop || sendAfterStop) await upload(recording);
  }, [legacyUploadOnStop, recorder, recorderState.durationMillis, releaseFocusLease, upload]);

  /** Stop records a local held clip. Only Send may start a provider request. */
  const stop = useCallback(async () => {
	await finishRecording(false);
  }, [finishRecording]);

  /** Slide-away while still holding. Marks the intent; `stop` honors it. */
  const cancel = useCallback(() => {
	if (!startingRef.current && !listeningRef.current) return;
    cancelledRef.current = true;
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Rigid);
	}, []);

  /** Send is explicit: it transcribes only the currently held retained audio. */
  const send = useCallback(() => {
    const recording = pendingRef.current;
	if (!canSendDictation(state, Boolean(recording)) || !recording) return;
    void upload(recording);
  }, [state, upload]);

  /**
   * Composer send-arrow behavior: while recording it atomically stops and
   * starts transcription; while held/error it sends the retained clip. This
   * avoids a render-gap in which a second tap could upload a stale recording.
   */
  const commit = useCallback(async () => {
	if (listeningRef.current) {
		await finishRecording(true);
		return;
	}
	const recording = pendingRef.current;
	if (!canSendDictation(state, Boolean(recording)) || !recording) return;
	await upload(recording);
  }, [finishRecording, state, upload]);

  /** Compatibility name for existing error UI; it preserves explicit Send semantics. */
  const retry = send;

  /** Discard a held/transcribing/failed clip and fence any late provider completion. */
  const discard = useCallback(() => {
	requestGenerationRef.current += 1;
	activeUploadAttemptRef.current = null;
	deleteDictationFile(pendingRef.current?.uri);
	pendingRef.current = null;
	setState('idle');
	setError(null);
  }, []);

  const dismissError = useCallback(() => {
	discard();
  }, [discard]);

  // Auto-stop at the cap so a button held by a pocket cannot record forever.
  useEffect(() => {
    if (state !== 'listening') return;
    if (recorderState.durationMillis < MAX_DICTATION_MS) return;
    void stop();
  }, [recorderState.durationMillis, state, stop]);

  return {
    state,
    /** 0..1, smoothed. The current level. */
    amplitude,
    /** Rolling amplitude history, oldest first. Drives the waveform. */
    trace,
    durationMs: listening ? recorderState.durationMillis : 0,
    error,
    permissionDenied,
    start,
    stop,
    send,
	commit,
    /** Delete explicitly discards a held or in-flight clip and ignores late text. */
    delete: discard,
    cancel,
    retry,
    dismissError,
    /** Used only by an AudioFocusCoordinator forced-close callback. */
    fenceFocusLease,
  };
}
