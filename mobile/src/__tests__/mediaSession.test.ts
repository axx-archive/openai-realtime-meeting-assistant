import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  createMediaSessionClient,
  type NativeMediaSessionModule,
} from '../../modules/bonfire-media-session/src/BonfireMediaSession';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');

test('meeting audio routing fails closed when its native module is unavailable', async () => {
  const client = createMediaSessionClient(undefined);

  assert.equal(await client.activateVideoMeeting(), null);
  assert.equal(await client.deactivateVideoMeeting(), false);
});

test('meeting audio routing preserves native route evidence and contains failures', async () => {
  const expected = {
    category: 'AVAudioSessionCategoryPlayAndRecord',
    mode: 'AVAudioSessionModeVideoChat',
    outputs: [{ name: 'Speaker', type: 'Speaker' }],
  };
  const native: NativeMediaSessionModule = {
    activateVideoMeeting: async () => expected,
    deactivateVideoMeeting: async () => true,
  };
  const client = createMediaSessionClient(native);

  assert.deepEqual(await client.activateVideoMeeting(), expected);
  assert.equal(await client.deactivateVideoMeeting(), true);

  const failing = createMediaSessionClient({
    activateVideoMeeting: async () => { throw new Error('route unavailable'); },
    deactivateVideoMeeting: async () => { throw new Error('route unavailable'); },
  });
  assert.equal(await failing.activateVideoMeeting(), null);
  assert.equal(await failing.deactivateVideoMeeting(), false);
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
  assert.match(swift, /OnDestroy \{[\s\S]*deactivateVideoMeetingRoute\(\)/);
  assert.match(swift, /AsyncFunction\("deactivateVideoMeeting"\)[\s\S]*deactivateVideoMeetingRoute\(\)/);
  assert.match(swift, /try rtcSession\.setActive\(false\)[\s\S]*ownsWebRTCActivation = false/);
  assert.match(swift, /let builtInOutputs: Set<AVAudioSession\.Port> = \[\.builtInReceiver, \.builtInSpeaker\]/);
  assert.match(swift, /alreadyOnSpeaker/);
  assert.match(swift, /meetingActive == true/);
  assert.doesNotMatch(swift, /session\.setCategory\(/);
  assert.doesNotMatch(swift, /session\.setActive\(true/);

  const podspec = fs.readFileSync(
    path.join(mobileRoot, 'modules', 'bonfire-media-session', 'ios', 'BonfireMediaSession.podspec'),
    'utf8',
  );
  assert.match(podspec, /s\.dependency 'JitsiWebRTC', '~> 124\.0\.0'/);
  assert.match(room, /await BonfireMediaSession\.activateVideoMeeting\(\);[\s\S]*await refreshCameraFramingInternal\(true\)/);
  assert.match(room, /outcome === 'installed'[\s\S]*await BonfireMediaSession\.activateVideoMeeting\(\)/);
  assert.match(
    room,
    /nextState !== 'active'[\s\S]*if \(previousAppState === 'active' \|\| intentionallyLeaving\.current\) return;[\s\S]*if \(!localRef\.current\) return;[\s\S]*void BonfireMediaSession\.activateVideoMeeting\(\)/,
  );
  assert.match(room, /track\.kind === 'audio'[\s\S]*void BonfireMediaSession\.activateVideoMeeting\(\)/);
  assert.match(room, /requestedAudio\.current = true;[\s\S]*void BonfireMediaSession\.activateVideoMeeting\(\)/);
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
