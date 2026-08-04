import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { describe, it } from 'node:test';
import {
  isServerUplinkSection,
  nativeUplinkTransceiverForSender,
  nativeUplinkAnswerDirection,
  nativeVideoUplinkCodecViolation,
  offeredRemoteVideoTrackIds,
  remoteMediaSections,
  unexpectedNativeUplinkDirectionKinds,
} from '../realtime/sdp';
import {
  NativeH264UnavailableError,
  nativeH264UplinkCodecPreferences,
} from '../realtime/nativeVideoCodec';

describe('native room offer planning', () => {
  it('selects constrained-baseline H264 and only its matching RTX', () => {
    const codecs = [
      { mimeType: 'video/VP8', payloadType: 96, sdpFmtpLine: '' },
      { mimeType: 'video/rtx', payloadType: 97, sdpFmtpLine: 'apt=96' },
      { mimeType: 'video/H264', payloadType: 104, sdpFmtpLine: 'profile-level-id=640c1f;packetization-mode=1' },
      { mimeType: 'video/rtx', payloadType: 105, sdpFmtpLine: 'apt=104' },
      { mimeType: 'video/H264', payloadType: 102, sdpFmtpLine: 'level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f' },
      { mimeType: 'video/rtx', payloadType: 103, sdpFmtpLine: 'apt=102' },
      { mimeType: 'video/H264', payloadType: 106, sdpFmtpLine: 'profile-level-id=42e01f;packetization-mode=0' },
    ];

    assert.deepEqual(nativeH264UplinkCodecPreferences(codecs), [
      codecs[4],
      codecs[2],
      codecs[3],
      codecs[5],
    ]);
    assert.throws(
      () => nativeH264UplinkCodecPreferences(codecs.slice(0, 2)),
      NativeH264UnavailableError,
    );
  });

  it('requires an H264-only native uplink answer with matching RTX', () => {
    const h264Answer = [
      'v=0',
      'm=video 9 UDP/TLS/RTP/SAVPF 102 103',
      'a=mid:uplink-video',
      'a=sendonly',
      'a=rtpmap:102 H264/90000',
      'a=fmtp:102 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f',
      'a=rtpmap:103 rtx/90000',
      'a=fmtp:103 apt=102',
    ].join('\r\n');
    assert.equal(nativeVideoUplinkCodecViolation(h264Answer, 'uplink-video'), null);

    const mixed = h264Answer
      .replace('102 103', '102 103 96')
      .concat('\r\na=rtpmap:96 VP8/90000');
    assert.match(nativeVideoUplinkCodecViolation(mixed, 'uplink-video') ?? '', /VP8/i);

    const wrongRtx = h264Answer.replace('a=fmtp:103 apt=102', 'a=fmtp:103 apt=96');
    assert.match(nativeVideoUplinkCodecViolation(wrongRtx, 'uplink-video') ?? '', /does not repair H\.264/);

    const packetizationZero = h264Answer.replace('packetization-mode=1', 'packetization-mode=0');
    assert.match(nativeVideoUplinkCodecViolation(packetizationZero, 'uplink-video') ?? '', /packetization-mode 1/);
  });

  it('applies H264 only to the native video uplink before creating its answer', () => {
    const roomSource = fs.readFileSync(
      path.resolve(import.meta.dirname, '..', 'realtime', 'useNativeRoom.ts'),
      'utf8',
    );
    const preferenceCall = roomSource.indexOf('nativeH264UplinkCodecPreferences(');
    const createAnswerCall = roomSource.indexOf('peer.createAnswer()');
    assert.ok(preferenceCall >= 0, 'native H264 preference call is missing');
    assert.ok(createAnswerCall > preferenceCall, 'codec preference must precede createAnswer');
    assert.equal(
      roomSource.match(/nativeH264UplinkCodecPreferences\(/g)?.length,
      1,
      'codec preference must only be applied at the video-uplink seam',
    );
    assert.match(
      roomSource,
      /transceiver\.direction = nativeUplinkAnswerDirection\([\s\S]*if \(section\.kind === 'video'\) \{[\s\S]*nativeH264UplinkCodecPreferences\([\s\S]*transceiver\.setCodecPreferences\(codecPreferences\);/,
    );
  });

  it('binds only server-recvonly uplinks and preserves same-kind downlinks', () => {
    const offer = [
      'v=0',
      'm=video 9 UDP/TLS/RTP/SAVPF 96',
      'a=mid:uplink-video',
      'a=recvonly',
      'm=audio 9 UDP/TLS/RTP/SAVPF 111',
      'a=mid:uplink-audio',
      'a=recvonly',
      'm=video 9 UDP/TLS/RTP/SAVPF 96',
      'a=mid:remote-video',
      'a=sendonly',
      'a=msid:remote-stream remote-track',
    ].join('\r\n');

    const sections = remoteMediaSections(offer);
    assert.equal(isServerUplinkSection(sections.get('uplink-video')), true);
    assert.equal(isServerUplinkSection(sections.get('uplink-audio')), true);
    assert.equal(isServerUplinkSection(sections.get('remote-video')), false);
    assert.deepEqual(sections.get('remote-video'), {
      kind: 'video',
      direction: 'sendonly',
      trackId: 'remote-track',
    });
    assert.deepEqual(offeredRemoteVideoTrackIds(sections), ['remote-track']);
  });

  it('resolves the native uplink by sender identity when remote audio downlinks coexist', () => {
    const remoteSender = { id: 'remote-downlink-sender' };
    const uplinkSender = { id: 'fixed-native-uplink-sender' };
    const transceivers = [
      { sender: remoteSender, receiver: { track: { kind: 'audio' } } },
      { sender: uplinkSender, receiver: { track: { kind: 'audio' } } },
    ];

    assert.equal(
      nativeUplinkTransceiverForSender(transceivers, uplinkSender),
      transceivers[1],
    );
    assert.equal(nativeUplinkTransceiverForSender(transceivers, null), null);
  });

  it('requests an explicit server offer before enabling a quiet-join microphone', () => {
    const roomSource = fs.readFileSync(
      path.join(process.cwd(), 'src/realtime/useNativeRoom.ts'),
      'utf8',
    );
    assert.match(
      roomSource,
      /nativeUplinkTransceiverForSender\([\s\S]*reason: 'microphone enabled after quiet join',[\s\S]*renegotiateUplink: true/,
    );
    assert.doesNotMatch(
      roomSource,
      /getTransceivers\(\)\.find\(\(candidate\) => candidate\.receiver\.track\?\.kind === 'audio'/,
    );
  });

  it('treats inactive video m-lines as removed and fails safe without track identity', () => {
    const removed = remoteMediaSections([
      'v=0',
      'm=video 9 UDP/TLS/RTP/SAVPF 96',
      'a=mid:old-remote-video',
      'a=inactive',
      'a=msid:remote-stream old-track',
    ].join('\r\n'));
    assert.deepEqual(offeredRemoteVideoTrackIds(removed), []);

    const ambiguous = remoteMediaSections([
      'v=0',
      'm=video 9 UDP/TLS/RTP/SAVPF 96',
      'a=mid:remote-video',
      'a=sendonly',
    ].join('\r\n'));
    assert.equal(offeredRemoteVideoTrackIds(ambiguous), null);
  });

  it('keeps quiet-join audio inactive while video remains ready to publish', () => {
    const mids = new Map<'audio' | 'video', string>([
      ['audio', 'uplink-audio'],
      ['video', 'uplink-video'],
    ]);
    const sendonlyAnswer = [
      'v=0',
      'm=audio 9 UDP/TLS/RTP/SAVPF 111',
      'a=mid:uplink-audio',
      'a=sendonly',
      'm=video 9 UDP/TLS/RTP/SAVPF 96',
      'a=mid:uplink-video',
      'a=sendonly',
    ].join('\r\n');
    assert.equal(nativeUplinkAnswerDirection('audio', false), 'inactive');
    assert.equal(nativeUplinkAnswerDirection('audio', true), 'sendonly');
    assert.equal(nativeUplinkAnswerDirection('video', false), 'sendonly');
    assert.deepEqual(unexpectedNativeUplinkDirectionKinds(sendonlyAnswer, mids, true), []);

    const quietAnswer = sendonlyAnswer.replace(
      'a=mid:uplink-audio\r\na=sendonly',
      'a=mid:uplink-audio\r\na=inactive',
    );
    assert.deepEqual(unexpectedNativeUplinkDirectionKinds(quietAnswer, mids, false), []);
    assert.deepEqual(unexpectedNativeUplinkDirectionKinds(sendonlyAnswer, mids, false), ['audio']);

    const inactiveVideo = sendonlyAnswer.replace(
      'a=mid:uplink-video\r\na=sendonly',
      'a=mid:uplink-video\r\na=inactive',
    );
    assert.deepEqual(unexpectedNativeUplinkDirectionKinds(inactiveVideo, mids, true), ['video']);
    assert.deepEqual(
      unexpectedNativeUplinkDirectionKinds(
        sendonlyAnswer,
        new Map([['audio', 'uplink-audio']]),
        true,
      ),
      ['video'],
    );
  });

  it('renegotiates before publishing a microphone after a quiet join', () => {
    const roomSource = fs.readFileSync(
      path.resolve(import.meta.dirname, '..', 'realtime', 'useNativeRoom.ts'),
      'utf8',
    );
    assert.match(
      roomSource,
      /audioTransceiver\.currentDirection !== 'sendonly'[\s\S]*audioTransceiver\.direction = 'sendonly';[\s\S]*send\('request_participant_tracks'/,
    );
  });

  it('retains and accepts audio-only agent receivers on iOS', () => {
    const roomSource = fs.readFileSync(
      path.resolve(import.meta.dirname, '..', 'realtime', 'useNativeRoom.ts'),
      'utf8',
    );
    assert.match(roomSource, /remoteAudioStreamsRef = useRef<Map<string, MediaStream>>/);
    assert.match(roomSource, /const remoteAudioStream = new MediaStream\(\[track\]\);[\s\S]*remoteAudioStreamsRef\.current\.set\(track\.id, remoteAudioStream\)/);
    assert.match(roomSource, /metadata\.kind[\s\S]*=== 'video'[\s\S]*!participantCanPublishVideo\(participant\)/);
    assert.doesNotMatch(roomSource, /if \(!participantCanPublishVideo\(participant\)\) break;/);
  });
});
