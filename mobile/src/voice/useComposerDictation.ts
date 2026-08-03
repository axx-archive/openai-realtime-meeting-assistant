import { useCallback, useEffect, useRef } from 'react';
import { Alert } from 'react-native';
import * as SecureStore from 'expo-secure-store';
import { audioFocusRuntime } from '../realtime/audioFocusRuntime';
import type { AudioFocusLease } from './AudioFocusCoordinator';
import { useDictation, type DictationResult } from './useDictation';

const DICTATION_DISCLOSURE_KEY = 'bonfire.dictation.serverDisclosure.v1';

type Options = {
  context?: 'scout' | 'chat';
  threadId?: string;
  onTranscript: (result: DictationResult) => void;
};

/**
 * Shared tap-to-record composer controller for Canvas and in-room chat.
 * Acquiring composer focus hangs up personal Realtime or temporarily parks the
 * meeting microphone. Releasing it restores the room's exact prior mute state.
 */
export function useComposerDictation({ context = 'chat', threadId, onTranscript }: Options) {
  const leaseRef = useRef<AudioFocusLease | null>(null);
  const dictation = useDictation({ context, threadId, onTranscript });
  const mountedRef = useRef(true);
  const captureRequestGenerationRef = useRef(0);
  const startingRef = useRef(false);
  const disclosureAcceptedRef = useRef(false);
  const disclosureOpenRef = useRef(false);
  const lifecycleRef = useRef({ cancel: dictation.cancel, stop: dictation.stop });
  lifecycleRef.current = { cancel: dictation.cancel, stop: dictation.stop };

  const releaseExact = useCallback(async (
    exactLease: AudioFocusLease | null,
    reason: 'completed' | 'cancelled' | 'error',
  ) => {
    if (!exactLease) return;
    if (leaseRef.current === exactLease) leaseRef.current = null;
    // useDictation normally releases immediately after native stop. This is a
    // fenced fallback for cancellation before start or an unexpected hook
    // exception; isCurrent prevents a stale completion touching a newer owner.
    if (!exactLease.isCurrent()) return;
    dictation.fenceFocusLease(exactLease);
    await exactLease.release(reason);
  }, [dictation.fenceFocusLease]);

  const disclosureAllowsCapture = useCallback(async (): Promise<boolean> => {
    if (disclosureAcceptedRef.current) return true;
    const stored = await SecureStore.getItemAsync(DICTATION_DISCLOSURE_KEY).catch(() => null);
    if (stored === 'accepted') {
      disclosureAcceptedRef.current = true;
      return true;
    }
    if (disclosureOpenRef.current) return false;
    disclosureOpenRef.current = true;
    return new Promise<boolean>((resolve) => {
      let settled = false;
      const settle = (accepted: boolean) => {
        if (settled) return;
        settled = true;
        disclosureOpenRef.current = false;
        if (accepted) {
          disclosureAcceptedRef.current = true;
          void SecureStore.setItemAsync(DICTATION_DISCLOSURE_KEY, 'accepted');
        }
        resolve(accepted);
      };
      Alert.alert(
        'Voice transcription',
        'Your voice is sent to STRIDE to transcribe with your company vocabulary, then the audio is deleted. Only the text stays.',
        [
          { text: 'Not now', style: 'cancel', onPress: () => settle(false) },
          { text: 'I understand', onPress: () => settle(true) },
        ],
        { cancelable: true, onDismiss: () => settle(false) },
      );
    });
  }, []);

  const start = useCallback(async () => {
    if (!mountedRef.current || dictation.state !== 'idle' || startingRef.current) return;
    const requestGeneration = ++captureRequestGenerationRef.current;
    startingRef.current = true;
    let exactLease: AudioFocusLease | null = null;
    try {
      const disclosureAccepted = await disclosureAllowsCapture();
      if (
        !disclosureAccepted
        || !mountedRef.current
        || captureRequestGenerationRef.current !== requestGeneration
      ) return;

      const lease = await audioFocusRuntime.acquire('composer_dictation', {
        forceClose: async () => {
          // AudioFocusCoordinator owns this forced lease close. Fence this
          // exact generation first so recorder teardown cannot enqueue a
          // recursive release behind the coordinator's in-flight close.
          if (exactLease) {
            if (leaseRef.current === exactLease) leaseRef.current = null;
            dictation.fenceFocusLease(exactLease);
          }
          dictation.cancel();
          await dictation.stop();
        },
      });
      exactLease = lease;
      // A newer focus intent can supersede this request while acquire() waits
      // for the previous owner's async close. Its promise still resolves with
      // a deliberately stale lease; never let that lease reach native capture.
      if (
        !mountedRef.current
        || captureRequestGenerationRef.current !== requestGeneration
        || !lease.isCurrent()
      ) {
        await lease.release('cancelled');
        return;
      }
      leaseRef.current = lease;
      const started = await dictation.start(lease);
      if (!started && leaseRef.current === lease) leaseRef.current = null;
    } catch {
      await releaseExact(exactLease, 'error');
    } finally {
      if (captureRequestGenerationRef.current === requestGeneration) startingRef.current = false;
    }
  }, [dictation, disclosureAllowsCapture, releaseExact]);

  const stop = useCallback(async () => {
    const exactLease = leaseRef.current;
    try {
      await dictation.stop();
    } finally {
      await releaseExact(exactLease, 'completed');
    }
  }, [dictation, releaseExact]);

  const commit = useCallback(async () => {
    const exactLease = leaseRef.current;
    try {
      await dictation.commit();
    } finally {
      await releaseExact(exactLease, 'completed');
    }
  }, [dictation, releaseExact]);

  const discard = useCallback(async () => {
    // Invalidate before awaiting teardown: a hidden composer may have a
    // disclosure lookup or focus acquisition in flight while still idle.
    captureRequestGenerationRef.current += 1;
    startingRef.current = false;
    const exactLease = leaseRef.current;
    try {
      if (dictation.state === 'listening') {
        dictation.cancel();
        await dictation.stop();
      } else {
        dictation.delete();
      }
    } finally {
      await releaseExact(exactLease, 'cancelled');
    }
  }, [dictation, releaseExact]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      captureRequestGenerationRef.current += 1;
      startingRef.current = false;
      lifecycleRef.current.cancel();
      const lease = leaseRef.current;
      leaseRef.current = null;
      void lifecycleRef.current.stop().finally(async () => {
        if (!lease?.isCurrent()) return;
        await lease.release('cancelled');
      });
    };
  }, []);

  return { ...dictation, start, stop, commit, discard };
}
