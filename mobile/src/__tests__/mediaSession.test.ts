import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  createMediaSessionClient,
  nextMediaSessionGeneration,
  type NativeMediaSessionModule,
} from '../../modules/bonfire-media-session/src/BonfireMediaSession';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');

test('media-session generations are positive safe integers and strictly monotonic', () => {
  const first = nextMediaSessionGeneration(1);
  const second = nextMediaSessionGeneration(1);
  assert.equal(Number.isSafeInteger(first), true);
  assert.equal(second, first + 1);
});

test('meeting audio routing fails closed when its native module is unavailable', async () => {
  const client = createMediaSessionClient(undefined);

  assert.equal(await client.activateVideoMeeting(41), null);
  assert.equal(await client.deactivateVideoMeeting(41), false);
});

test('meeting audio routing preserves native route evidence and contains failures', async () => {
  const generation = 42;
  const expected = {
    generation,
    category: 'AVAudioSessionCategoryPlayAndRecord',
    mode: 'AVAudioSessionModeVideoChat',
    outputs: [{ name: 'Speaker', type: 'Speaker' }],
  };
  const calls: string[] = [];
  const native: NativeMediaSessionModule = {
    activateVideoMeeting: async (value) => {
      calls.push(`activate:${value}`);
      return expected;
    },
    deactivateVideoMeeting: async (value) => {
      calls.push(`deactivate:${value}`);
      return true;
    },
  };
  const client = createMediaSessionClient(native);

  assert.deepEqual(await client.activateVideoMeeting(generation), expected);
  assert.equal(await client.deactivateVideoMeeting(generation), true);
  assert.deepEqual(calls, ['activate:42', 'deactivate:42']);

  const failing = createMediaSessionClient({
    activateVideoMeeting: async () => { throw new Error('route unavailable'); },
    deactivateVideoMeeting: async () => { throw new Error('route unavailable'); },
  });
  assert.equal(await failing.activateVideoMeeting(generation), null);
  assert.equal(await failing.deactivateVideoMeeting(generation), false);
});

test('meeting audio routing rejects malformed, empty, and mismatched route evidence', async () => {
  const malformed = createMediaSessionClient({
    activateVideoMeeting: async () => ({
      generation: 8,
      category: '',
      mode: 'AVAudioSessionModeVideoChat',
      outputs: [],
    }),
    deactivateVideoMeeting: async () => true,
  });
  assert.equal(await malformed.activateVideoMeeting(8), null);

  const mismatched = createMediaSessionClient({
    activateVideoMeeting: async () => ({
      generation: 8,
      category: 'AVAudioSessionCategoryPlayAndRecord',
      mode: 'AVAudioSessionModeVideoChat',
      outputs: [{ name: 'Speaker', type: 'Speaker' }],
    }),
    deactivateVideoMeeting: async () => true,
  });
  assert.equal(await mismatched.activateVideoMeeting(9), null);
  assert.equal(await mismatched.activateVideoMeeting(0), null);
  assert.equal(await mismatched.deactivateVideoMeeting(Number.NaN), false);
});

test('iOS room audio forces built-in speaker while preserving external routes', () => {
  const swift = fs.readFileSync(
    path.join(mobileRoot, 'modules', 'bonfire-media-session', 'ios', 'BonfireMediaSessionModule.swift'),
    'utf8',
  );
  const room = fs.readFileSync(
    path.join(mobileRoot, 'src', 'realtime', 'useNativeRoom.ts'),
    'utf8',
  );

  assert.match(swift, /import WebRTC/);
  assert.match(swift, /\.defaultToSpeaker/);
  assert.match(swift, /\.allowBluetoothHFP/);
  assert.match(swift, /\.allowAirPlay/);
  assert.match(swift, /RTCAudioSessionConfiguration\.setWebRTC\(configuration\)/);
  assert.match(swift, /rtcSession\.lockForConfiguration\(\)/);
  assert.match(swift, /try rtcSession\.setConfiguration\(configuration, active: true\)/);
  assert.match(
    swift,
    /activationSucceeded = true[\s\S]*catch \{[\s\S]*if activationSucceeded \{[\s\S]*try\? rtcSession\.setActive\(false\)[\s\S]*throw error/,
  );
  assert.match(swift, /audioSessionDidStartPlayOrRecord/);
  assert.match(swift, /audioSessionDidChangeRoute/);
  assert.match(swift, /scheduleRouteReassertion\(\)/);
  assert.match(swift, /activeGeneration: Int64\?/);
  assert.match(swift, /latestGeneration: Int64 = 0/);
  assert.match(swift, /retiredThroughGeneration: Int64 = 0/);
  assert.match(swift, /generation > self\.retiredThroughGeneration[\s\S]*generation >= self\.latestGeneration/);
  assert.match(swift, /activeGeneration == generation[\s\S]*self\.latestGeneration == generation/);
  assert.match(swift, /guard activeGeneration <= generation else \{ return false \}/);
  assert.match(swift, /OnDestroy \{[\s\S]*deactivateVideoMeetingRoute\(retiringThrough: nil\)/);
  assert.match(swift, /AsyncFunction\("deactivateVideoMeeting"\)[\s\S]*deactivateVideoMeetingRoute\(retiringThrough: generation\)/);
  assert.match(swift, /try rtcSession\.setActive\(false\)[\s\S]*ownsWebRTCActivation = false/);
  assert.match(swift, /let builtInOutputs: Set<AVAudioSession\.Port> = \[\.builtInReceiver, \.builtInSpeaker\]/);
  assert.match(swift, /alreadyOnSpeaker/);
  assert.match(swift, /activeGeneration > self\.retiredThroughGeneration/);
  assert.doesNotMatch(swift, /session\.setCategory\(/);
  assert.doesNotMatch(swift, /session\.setActive\(true/);

  const podspec = fs.readFileSync(
    path.join(mobileRoot, 'modules', 'bonfire-media-session', 'ios', 'BonfireMediaSession.podspec'),
    'utf8',
  );
  assert.match(podspec, /s\.dependency 'JitsiWebRTC', '~> 124\.0\.0'/);
  assert.match(room, /BonfireMediaSession\.activateVideoMeeting\(session\.mediaSessionGeneration\)/);
  assert.match(room, /Platform\.OS !== 'ios' \|\| snapshot !== null/);
  assert.match(room, /await activateMeetingMedia\(\)[\s\S]*waitForBoundedNativeOperation\([\s\S]*refreshCameraFramingInternal\(true\)/);
  assert.match(room, /outcome === 'installed'[\s\S]*await activateMeetingMedia\(\)/);
  assert.match(
    room,
    /nextState !== 'active'[\s\S]*if \(previousAppState === 'active' \|\| intentionallyLeaving\.current\) return;[\s\S]*if \(!localRef\.current\) return;[\s\S]*reassertMeetingMedia\(\)/,
  );
  assert.match(room, /track\.kind === 'audio'[\s\S]*reassertMeetingMedia\(\)/);
  assert.match(room, /requestedAudio\.current = true;[\s\S]*reassertMeetingMedia\(\)/);
  assert.match(room, /Room audio routing needs attention\. Rejoin if you cannot hear the call\./);
  assert.match(room, /drainNativeRoomMediaTeardown\([\s\S]*deactivateVideoMeeting\(generation\)/);
});

test('multi-participant room composition renders one equal-sized self view', () => {
  const room = fs.readFileSync(
    path.join(mobileRoot, 'src', 'screens', 'RoomScreen.tsx'),
    'utf8',
  );

  assert.match(room, /callParticipants\.length === 1 \? \([\s\S]*<LocalPreview/);
  assert.doesNotMatch(room, /callParticipants\.length > 0 && callParticipants\.length < 4/);
  assert.match(room, /style=\{\[styles\.participantStripTile, landscape && styles\.participantStripTileLandscape\]\}/);
  assert.match(room, /fit=\{stageIsScreenShare \? 'contain' : 'cover'\}/);
  assert.match(room, /largePrimaryCameraTile: \{ flex: 1, alignSelf: 'stretch' \}/);
});
