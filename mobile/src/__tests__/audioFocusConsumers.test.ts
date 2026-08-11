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
  assert.match(thread, /onTranscript: \(\{ text \}\) => \{\s*void send\(text\);\s*\}/u);
  assert.match(thread, /useComposerDictation/);
  assert.match(thread, /accessibilityLabel="Dictate a message"/);
  assert.match(thread, /if \(dictationCanCommit\) void dictation\.commit\(\);/);
  assert.doesNotMatch(thread, /onPressIn|dictationTouchActiveRef/);
  assert.match(thread, /Recording · send when finished/);
  assert.match(thread, /Transcribe and send dictated clip/);
  assert.match(thread, /dictation\.discard\(\)/);
  assert.doesNotMatch(thread, /Stop dictation/);
});

test('Canvas keeps live Scout singular and leaves dictation to conversation screens', () => {
  const canvas = source('src', 'screens', 'CanvasScreen.tsx');
  assert.match(canvas, /usePersonalRealtime/);
  assert.match(canvas, /realtime\.enabled/);
  assert.match(canvas, /The cradle has one stable meaning: a full-duplex Realtime Scout call/);
  assert.match(canvas, /if \(!realtime\.enabled\) return;/);
  assert.doesNotMatch(canvas, /legacyUploadOnStop|fallbackVoice|voiceDictation/);
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
  assert.doesNotMatch(canvas, /useComposerDictation|Message Scout|composerDictation|submitComposerText/);
  const config = source('src', 'config.ts');
  assert.match(config, /EXPO_PUBLIC_NATIVE_REALTIME_VOICE_ENABLED === 'true'/);
  const eas = JSON.parse(source('eas.json')) as {
    build?: { production?: { env?: Record<string, string> } };
  };
  assert.equal(
    eas.build?.production?.env?.EXPO_PUBLIC_NATIVE_REALTIME_VOICE_ENABLED,
    'true',
    'the production release profile must compile the core Scout cradle on',
  );
});

test('room chat has the same explicit record, delete, transcribe and send lifecycle', () => {
  const sheet = source('src', 'components', 'RoomConversationSheet.tsx');
  assert.match(sheet, /useComposerDictation/);
  assert.match(sheet, /accessibilityLabel="Dictate a room message"/);
  assert.match(sheet, /accessibilityLabel="Delete dictated message"/);
  assert.match(sheet, /accessibilityLabel="Transcribe and send"/);
  assert.match(sheet, /composerDictation\.commit\(\)/);
  assert.doesNotMatch(sheet, /accessibilityLabel="Stop recording"/);
  assert.match(sheet, /sendComposerText\(text\)/);
  assert.match(sheet, /if \(visible && mode === 'chat'\) return;/);
  assert.match(sheet, /discardDictationRef\.current\(\)/);
  const controller = source('src', 'voice', 'useComposerDictation.ts');
  assert.match(controller, /audioFocusRuntime\.acquire\('composer_dictation'/);
  assert.match(controller, /const started = await dictation\.start\(lease\)/);
  assert.match(controller, /dictation\.fenceFocusLease\(exactLease\)/);
  assert.match(controller, /finally \{\s*await releaseExact\(exactLease, 'completed'\)/);
  assert.match(controller, /await dictation\.commit\(\)/);
  assert.doesNotMatch(controller, /Alert|SecureStore|disclosureAllowsCapture/);

  const composerStart = controller.slice(
    controller.indexOf('const start = useCallback'),
    controller.indexOf('const stop = useCallback'),
  );
  const focusAwait = composerStart.indexOf("await audioFocusRuntime.acquire('composer_dictation'");
  const generationChecks = [...composerStart.matchAll(/captureRequestGenerationRef\.current !== requestGeneration/g)]
    .map((match) => match.index ?? -1);
  assert.equal(generationChecks.length, 1);
  assert.ok(focusAwait < generationChecks[0], 'surface generation is checked after focus acquisition');
  assert.ok(generationChecks[0] < composerStart.indexOf('dictation.start(lease)'));
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
