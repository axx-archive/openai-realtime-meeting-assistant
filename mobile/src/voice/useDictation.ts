import { useCallback, useEffect, useRef, useState } from 'react';
import * as Haptics from 'expo-haptics';
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
import { normalizeMetering, smoothAmplitude } from './amplitude';

/**
 * Hold-to-dictate — design §11.
 *
 *   idle ──hold──▶ listening ──release──▶ transcribing ──▶ landed
 *     ▲               │                        │             │
 *     └──slide-away───┘                        └──error──────┘
 *        (cancel)                                 (audio RETAINED)
 *
 * The invariant that matters: a failure never discards the recording. Losing a
 * user's spoken paragraph to a flaky network is unforgivable, so the audio stays
 * on disk and the error path hands back a retry that re-uploads the same file.
 */

export type DictationState = 'idle' | 'listening' | 'transcribing' | 'error';

/** Mirrors the server's cap so a stuck recorder can't bill a surprise. */
export const MAX_DICTATION_MS = 600_000;

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

export type UseDictationOptions = {
  context?: 'chat' | 'board' | 'search';
  threadId?: string;
  onTranscript: (result: DictationResult) => void;
};

export function useDictation({ context = 'chat', threadId, onTranscript }: UseDictationOptions) {
  const { sessionToken } = useAuth();
  const recorder = useAudioRecorder(DICTATION_RECORDING);
  const recorderState = useAudioRecorderState(recorder, meteringIntervalMs);

  const [state, setState] = useState<DictationState>('idle');
  const [amplitude, setAmplitude] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [permissionDenied, setPermissionDenied] = useState(false);

  // Survives re-renders and the async gap between release and upload.
  const cancelledRef = useRef(false);
  const pendingRef = useRef<{ uri: string; durationMs: number } | null>(null);
  const onTranscriptRef = useRef(onTranscript);
  onTranscriptRef.current = onTranscript;

  const listening = state === 'listening';

  // Amplitude tracking. Only runs while listening — at rest the bars are static,
  // which is the breathe-only-while-listening law (design §8) enforced at the
  // data source rather than left to the view to remember.
  useEffect(() => {
    if (!listening) {
      setAmplitude(0);
      return;
    }
    setAmplitude((previous) => smoothAmplitude(previous, normalizeMetering(recorderState.metering)));
  }, [listening, recorderState.metering]);

  const upload = useCallback(
    async (recording: { uri: string; durationMs: number }) => {
      if (!sessionToken) {
        setState('error');
        setError('Sign in to dictate.');
        return;
      }
      setState('transcribing');
      setError(null);
      try {
        const result = await api.transcribeDictation(sessionToken, recording, { context, threadId });
        pendingRef.current = null;
        setState('idle');
        if (result.text.trim()) {
          void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
          onTranscriptRef.current({ text: result.text, biased: result.biased });
        }
      } catch (err) {
        // The recording stays in pendingRef so retry() can re-send the exact
        // same audio. Nothing is deleted on failure.
        setState('error');
        setError(err instanceof BonfireApiError ? err.message : 'Could not transcribe that.');
      }
    },
    [context, sessionToken, threadId],
  );

  const start = useCallback(async () => {
    if (state === 'listening' || state === 'transcribing') return;
    cancelledRef.current = false;
    setError(null);

    const permission = await AudioModule.requestRecordingPermissionsAsync();
    if (!permission.granted) {
      setPermissionDenied(true);
      setState('idle');
      return;
    }
    setPermissionDenied(false);

    // allowsRecording routes the session to the mic; playsInSilentMode keeps a
    // spoken answer audible when the ringer switch is off, which is the common
    // case for a phone on a desk.
    await setAudioModeAsync({ allowsRecording: true, playsInSilentMode: true });
    await recorder.prepareToRecordAsync();
    recorder.record();
    setState('listening');
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
  }, [recorder, state]);

  /** Release: stop, then upload — unless the finger slid away first. */
  const stop = useCallback(async () => {
    if (state !== 'listening') return;
    const durationMs = recorderState.durationMillis;
    await recorder.stop();
    // Release the mic so other audio (a room, a spoken answer) is not ducked
    // behind an idle recording session.
    await setAudioModeAsync({ allowsRecording: false, playsInSilentMode: true });

    if (cancelledRef.current) {
      cancelledRef.current = false;
      setState('idle');
      return;
    }
    const uri = recorder.uri;
    if (!uri || durationMs < 400) {
      // Below ~400ms this is a mistap, not speech. Silently returning to idle
      // beats surfacing an error for something the user did not intend to do.
      setState('idle');
      return;
    }
    const recording = { uri, durationMs: Math.min(durationMs, MAX_DICTATION_MS) };
    pendingRef.current = recording;
    await upload(recording);
  }, [recorder, recorderState.durationMillis, state, upload]);

  /** Slide-away while still holding. Marks the intent; `stop` honors it. */
  const cancel = useCallback(() => {
    if (state !== 'listening') return;
    cancelledRef.current = true;
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Rigid);
  }, [state]);

  /** Re-uploads the retained audio after a failure. */
  const retry = useCallback(() => {
    const recording = pendingRef.current;
    if (!recording) {
      setState('idle');
      setError(null);
      return;
    }
    void upload(recording);
  }, [upload]);

  const dismissError = useCallback(() => {
    pendingRef.current = null;
    setState('idle');
    setError(null);
  }, []);

  // Auto-stop at the cap so a button held by a pocket cannot record forever.
  useEffect(() => {
    if (state !== 'listening') return;
    if (recorderState.durationMillis < MAX_DICTATION_MS) return;
    void stop();
  }, [recorderState.durationMillis, state, stop]);

  return {
    state,
    /** 0..1, smoothed. Drives the waveform. */
    amplitude,
    durationMs: listening ? recorderState.durationMillis : 0,
    error,
    permissionDenied,
    start,
    stop,
    cancel,
    retry,
    dismissError,
  };
}
