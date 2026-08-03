import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { AudioFocusCoordinator } from '../voice/AudioFocusCoordinator';
import { createNativeRoomJoinAttemptGuard } from '../realtime/connectionGeneration';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('thread dictation holds locally, then uses the normal message send path once', () => {
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  assert.match(thread, /onTranscript: \(\{ text \}\) => \{ void send\(text\); \}/);
  assert.match(thread, /useComposerDictation/);
  assert.match(thread, /accessibilityLabel=\{listening \? 'Stop dictation' : 'Dictate a message'\}/);
  assert.match(thread, /if \(listening\) void dictation\.stop\(\);\s*else void dictation\.start\(\);/);
  assert.doesNotMatch(thread, /onPressIn|dictationTouchActiveRef/);
  assert.match(thread, /Ready to transcribe/);
  assert.match(thread, /Transcribe and send dictated clip/);
  assert.match(thread, /dictation\.delete/);
  assert.match(thread, /dictation\.send\(\)/);
});

test('Canvas keeps live Scout and recorded composer dictation distinct', () => {
  const canvas = source('src', 'screens', 'CanvasScreen.tsx');
  assert.match(canvas, /usePersonalRealtime/);
  assert.match(canvas, /realtime\.enabled/);
  assert.match(canvas, /legacyUploadOnStop: true/);
  assert.match(canvas, /const requestGeneration = \+\+fallbackVoiceRequestGenerationRef\.current/);
  assert.match(
    canvas,
    /requestGeneration !== fallbackVoiceRequestGenerationRef\.current[\s\S]*\|\| !lease\.isCurrent\(\)[\s\S]*\|\| !officeControlChannelIsLive\(sessionToken\)/,
  );
  assert.match(canvas, /void startFallbackVoiceCapture\(requestGeneration, lease\)/);
  assert.doesNotMatch(canvas, /void voiceDictation\.start\(\)/);
  const closeFallback = canvas.slice(
    canvas.indexOf('if (conversation.open) {'),
    canvas.indexOf("let exactLease: AudioFocusLease | null = null;"),
  );
  assert.ok(closeFallback.indexOf('voiceDictation.cancel()') < closeFallback.indexOf('await voiceDictation.stop()'));
  assert.ok(closeFallback.indexOf('await voiceDictation.stop()') < closeFallback.indexOf("lease?.release('completed')"));
  assert.match(canvas, /useEffect\(\(\) => \(\) => \{\s*fallbackVoiceRequestGenerationRef\.current \+= 1/);
  const guardedFallbackStart = canvas.slice(
    canvas.indexOf('const startFallbackVoiceCapture = useCallback'),
    canvas.indexOf('const handleTap = useCallback'),
  );
  assert.ok(
    guardedFallbackStart.indexOf('requestGeneration !== fallbackVoiceRequestGenerationRef.current')
      < guardedFallbackStart.indexOf('voiceDictation.start()'),
    'fallback Scout voice must reject a stale generation before native capture',
  );
  assert.ok(
    guardedFallbackStart.indexOf('fallbackVoiceLeaseRef.current !== lease')
      < guardedFallbackStart.indexOf('voiceDictation.start()'),
    'fallback Scout voice must own the exact lease before native capture',
  );
  const realtime = source('src', 'realtime', 'usePersonalRealtime.ts');
  assert.match(realtime, /audioFocusRuntime\.acquire\('personal_realtime'/);
  assert.match(realtime, /api\.realtimeOffer\(sessionToken, localSDP\)/);
  assert.match(realtime, /api\.realtimeTool\([\s\S]*sessionToken,[\s\S]*call\.name/);
  assert.match(realtime, /api\.realtimeUsage\(sessionToken/);
  assert.match(realtime, /onActionsRef\.current\?\.\(response\.actions\)/);
  assert.match(realtime, /terminateTransportWithError\(connectionGeneration, 'Scout voice connection was interrupted\.'\)/);
  assert.match(realtime, /terminateTransportWithError\(connectionGeneration, 'Scout voice needs attention\.'\)/);
  assert.match(realtime, /terminateTransportWithError\(connectionGeneration, 'Scout voice connection ended\.'\)/);
  assert.match(realtime, /terminateTransportWithError\(connectionGeneration, providerError \|\| 'Scout voice needs attention\.'\)/);
  assert.match(realtime, /generationRef\.current \+= 1;[\s\S]*leaseRef\.current = null;[\s\S]*releasePersonalRealtimeTerminalFocus\(lease, cleanupTransport\)/);
  const terminalCleanup = realtime.slice(
    realtime.indexOf('const terminateTransportWithError = useCallback'),
    realtime.indexOf('const sendEvent = useCallback'),
  );
  assert.ok(
    terminalCleanup.indexOf('releasePersonalRealtimeTerminalFocus(lease, cleanupTransport)')
      < terminalCleanup.indexOf("setLiveStatus('error')"),
    'native Realtime must finish transport/focus cleanup before rendering inactive error status',
  );
  const realtimeStart = realtime.slice(
    realtime.indexOf('const start = useCallback'),
    realtime.indexOf('const stop = useCallback'),
  );
  assert.match(realtimeStart, /const startup = drainPersonalRealtimeStartup\(/);
  assert.match(realtimeStart, /const mediaSessionGeneration = nextMediaSessionGeneration\(\)/);
  assert.match(realtimeStart, /waitForBoundedNativeOperation\([\s\S]*activateVideoMeeting\(mediaSessionGeneration\)/);
  assert.match(realtimeStart, /startupDrain = startup;\s*const \[clientConfig, stream\] = await startup;/);
  assert.match(realtimeStart, /closePersonalRealtimeStartup\(startupDrain, cleanupSessionTransport\)/);
  assert.match(realtimeStart, /generationRef\.current \+= 1;\s*startupFailureTerminalRequest = \+\+terminalRequestRef\.current;/);
  assert.ok(
    realtimeStart.indexOf('const startup = drainPersonalRealtimeStartup(')
      < realtimeStart.indexOf('releasePersonalRealtimeTerminalFocus(lease, cleanupSessionTransport'),
    'startup siblings drain before any terminal media/focus cleanup',
  );
  assert.match(realtimeStart, /await lease\.release\('cancelled'\)\.catch\(\(\) => undefined\);/);
  const startupFailure = realtimeStart.slice(realtimeStart.indexOf('} catch (startError)'));
  const startupCleanup = startupFailure.indexOf('await releasePersonalRealtimeTerminalFocus(lease, cleanupSessionTransport)');
  assert.ok(startupFailure.indexOf('generationRef.current += 1') < startupCleanup);
  assert.ok(startupCleanup < startupFailure.indexOf('setError(message)'));
  assert.ok(startupCleanup < startupFailure.indexOf("setLiveStatus('error')"));
  assert.match(realtime, /dataChannelRef\.current !== dataChannel/);
  assert.match(realtime, /dataChannel\.onclose = \(\) =>/);
  assert.match(realtime, /peerRef\.current !== peer \|\| generationRef\.current !== connectionGeneration/);
  assert.match(realtime, /personalRealtimeCleanupScope\(/);
  assert.match(realtime, /cleanupScope !== 'owned'[\s\S]*deactivateVideoMeeting\(expectedMediaSessionGeneration\)/);
  assert.match(realtime, /cleanupScope === 'detached'[\s\S]*mediaSessionGenerationRef\.current === null/);
  assert.match(canvas, /usePersonalRealtime\(\{ onActions: handleRealtimeActions \}\)/);
  assert.match(canvas, /useComposerDictation/);
  assert.match(canvas, /accessibilityLabel="Message Scout"/);
  assert.match(canvas, /accessibilityLabel="Dictate a message"/);
  assert.match(canvas, /composerDictation\.commit\(\)/);
  assert.match(canvas, /submitComposerText\(text\)/);
  const config = source('src', 'config.ts');
  assert.match(config, /EXPO_PUBLIC_NATIVE_REALTIME_VOICE_ENABLED === 'true'/);
});

test('room chat has the same explicit record, delete, transcribe and send lifecycle', () => {
  const sheet = source('src', 'components', 'RoomConversationSheet.tsx');
  assert.match(sheet, /useComposerDictation/);
  assert.match(sheet, /accessibilityLabel="Dictate a room message"/);
  assert.match(sheet, /accessibilityLabel="Delete dictated message"/);
  assert.match(sheet, /accessibilityLabel="Stop recording"/);
  assert.match(sheet, /accessibilityLabel="Transcribe and send"/);
  assert.match(sheet, /composerDictation\.commit\(\)/);
  assert.match(sheet, /sendComposerText\(text\)/);
  assert.match(sheet, /if \(visible && mode === 'chat'\) return;/);
  assert.match(sheet, /discardDictationRef\.current\(\)/);
  const controller = source('src', 'voice', 'useComposerDictation.ts');
  assert.match(controller, /audioFocusRuntime\.acquire\('composer_dictation'/);
  assert.match(controller, /const started = await dictation\.start\(lease\)/);
  assert.match(controller, /dictation\.fenceFocusLease\(exactLease\)/);
  assert.match(controller, /finally \{\s*await releaseExact\(exactLease, 'completed'\)/);
  assert.match(controller, /await dictation\.commit\(\)/);
  assert.match(controller, /return new Promise<boolean>\(\(resolve\) =>/);
  assert.match(controller, /'I understand', onPress: \(\) => settle\(true\)/);

  const composerStart = controller.slice(
    controller.indexOf('const start = useCallback'),
    controller.indexOf('const stop = useCallback'),
  );
  const disclosureAwait = composerStart.indexOf('await disclosureAllowsCapture()');
  const focusAwait = composerStart.indexOf("await audioFocusRuntime.acquire('composer_dictation'");
  const generationChecks = [...composerStart.matchAll(/captureRequestGenerationRef\.current !== requestGeneration/g)]
    .map((match) => match.index ?? -1);
  assert.equal(generationChecks.length, 2);
  assert.ok(disclosureAwait < generationChecks[0], 'surface generation is checked after disclosure lookup');
  assert.ok(focusAwait < generationChecks[1], 'surface generation is checked after focus acquisition');
  assert.ok(generationChecks[1] < composerStart.indexOf('dictation.start(lease)'));
  const staleLeaseCheck = composerStart.indexOf('|| !lease.isCurrent()');
  assert.ok(focusAwait < staleLeaseCheck && staleLeaseCheck < composerStart.indexOf('dictation.start(lease)'));
  assert.match(composerStart, /await lease\.release\('cancelled'\);\s*return;/);
  assert.match(controller, /const discard = useCallback[\s\S]*captureRequestGenerationRef\.current \+= 1;/);
  assert.match(controller, /mountedRef\.current = false;\s*captureRequestGenerationRef\.current \+= 1;/);

  const capture = source('src', 'voice', 'useDictation.ts');
  assert.match(capture, /finishResult = await finishDictationCapture/);
  assert.match(capture, /finally \{[\s\S]*releaseFocusLease\(exactLease, reason\)/);
  assert.match(capture, /Recording saved, but meeting audio could not be restored cleanly/);

  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  assert.match(thread, /useComposerDictation/);
  assert.doesNotMatch(thread, /onPressIn|dictationTouchActiveRef/);
});

test('thread unmount during deferred focus acquisition releases the lease without starting capture', async () => {
  const focus = new AudioFocusCoordinator();
  let allowClose!: () => void;
  let closeEntered!: () => void;
  const entered = new Promise<void>((resolve) => { closeEntered = resolve; });
  await focus.acquire('personal_realtime', {
    forceClose: () => new Promise<void>((resolve) => {
      allowClose = resolve;
      closeEntered();
    }),
  });

  let touchActive = true;
  let captureStarts = 0;
  const pendingCapture = (async () => {
    const lease = await focus.acquire('composer_dictation');
    if (!lease.isCurrent() || !touchActive) {
      await lease.release('cancelled');
      return;
    }
    captureStarts += 1;
  })();

  await entered;
  touchActive = false;
  allowClose();
  await pendingCapture;

  assert.equal(captureStarts, 0);
  assert.equal(focus.mode, 'idle');
});

test('Canvas unmount generation cancels a deferred focus acquisition before capture', async () => {
  const focus = new AudioFocusCoordinator();
  let allowClose!: () => void;
  let closeEntered!: () => void;
  const entered = new Promise<void>((resolve) => { closeEntered = resolve; });
  await focus.acquire('meeting_media', {
    forceClose: () => new Promise<void>((resolve) => {
      allowClose = resolve;
      closeEntered();
    }),
  });

  let generation = 1;
  let captureStarts = 0;
  const requestGeneration = generation;
  const pendingCapture = (async () => {
    const lease = await focus.acquire('personal_realtime');
    if (requestGeneration !== generation || !lease.isCurrent()) {
      await lease.release('cancelled');
      return;
    }
    captureStarts += 1;
  })();

  await entered;
  generation += 1; // Canvas unmount/stop intent.
  allowClose();
  await pendingCapture;
  assert.equal(captureStarts, 0);
  assert.equal(focus.mode, 'idle');
});

test('room leave cancels its join generation before deferred focus can begin media work', async () => {
  const focus = new AudioFocusCoordinator();
  const joins = createNativeRoomJoinAttemptGuard();
  let allowClose!: () => void;
  let closeEntered!: () => void;
  const entered = new Promise<void>((resolve) => { closeEntered = resolve; });
  await focus.acquire('personal_realtime', {
    forceClose: () => new Promise<void>((resolve) => {
      allowClose = resolve;
      closeEntered();
    }),
  });

  const joinAttempt = joins.begin();
  let mediaStarts = 0;
  const pendingJoin = (async () => {
    const lease = await focus.acquire('meeting_media');
    if (!joins.isCurrent(joinAttempt) || !lease.isCurrent()) {
      await lease.release('cancelled');
      return;
    }
    mediaStarts += 1;
  })();

  await entered;
  joins.cancel(joinAttempt); // leave/unmount
  allowClose();
  await pendingJoin;
  assert.equal(mediaStarts, 0);
  assert.equal(focus.mode, 'idle');
});

test('room media owns foreground focus and exposes exact mute parking hooks', () => {
  const room = source('src', 'realtime', 'useNativeRoom.ts');
  assert.match(room, /audioFocusRuntime\.acquire\('meeting_media'/);
  assert.match(room, /const authority = createNativeRoomTerminalAuthority\(/);
  assert.match(room, /authority\.bindFocusAdmission\(focusAdmission\)/);
  assert.match(room, /authority\.bindFocusLease\(meetingLease\)/);
  assert.match(room, /if \(!joinIsCurrent\(\) \|\| !meetingLease\.isCurrent\(\)\) \{\s*await terminateRoomSession\(roomSession, 'cancelled', 'leave', null\)/);
  assert.match(room, /forceClose: \(reason\) => terminateRoomSession\([\s\S]*'focus_coordinator'/);
  assert.match(room, /drainNativeRoomMediaTeardown\(/);
  assert.match(room, /BonfireMediaSession\.activateVideoMeeting\(session\.mediaSessionGeneration\)/);
  assert.match(room, /waitForBoundedNativeOperation\([\s\S]*nativeRoomMediaOperationTimeoutMs/);
  assert.match(room, /waitForNativeRoomTerminalPresentation\(/);
  assert.match(room, /mergeNativeRoomTerminalPresentation\(/);
  assert.doesNotMatch(room, /void meetingFocusLeaseRef\.current\?\.release/);
  assert.ok(
    room.indexOf('joinAttemptGuardRef.current?.begin()') < room.indexOf("audioFocusRuntime.acquire('meeting_media'"),
    'room join must establish a cancellable intent before awaiting focus',
  );
  const roomJoin = room.slice(
    room.indexOf('const join = useCallback'),
    room.indexOf('const setMuted = useCallback'),
  );
  assert.ok(
    roomJoin.indexOf("lifecycle: 'joining'") < roomJoin.indexOf("audioFocusRuntime.acquire('meeting_media'"),
    'room join must publish joining before deferred focus admission',
  );
  assert.match(room, /reason === 'error' \? 'failure' : 'leave'/);
  assert.match(room, /parkRoomMute: \(\) => roomDictationFocusRef\.current\.park\(\)/);
  assert.match(room, /restoreRoomMute: \(wasMuted\) => roomDictationFocusRef\.current\.restore\(wasMuted\)/);
  assert.match(room, /const parkPrivateDictation = useCallback\(\(\): boolean =>/);
  assert.match(room, /const wasMuted = state\.muted \|\| state\.microphoneStarting/);
  assert.match(room, /setMuted\(true\)/);
  assert.match(room, /const restorePrivateDictation = useCallback\(\(wasMuted: boolean\) =>/);
  assert.match(room, /setMuted\(wasMuted\)/);
});
