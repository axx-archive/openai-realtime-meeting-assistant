import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  focusedVideoParticipant,
  participantVideoAccessibilityStatus,
  pictureInPictureParticipant,
  pinnedVideoParticipantIsStale,
  presentRemoteParticipantDevices,
  presentRemoteVideoParticipants,
  remoteVideoPresentationKey,
  videoStageParticipants,
} from '../realtime/callPresentation';
import {
  parseParticipantMediaStates,
  participantEndpointMediaStatesFromSnapshot,
  participantEndpointMediaStatesSnapshotIsAuthoritative,
  participantMediaStatesFromSnapshot,
} from '../realtime/participantMedia';

describe('native participant presentation', () => {
  it('uses authoritative media state to hide and restore video without replacing the participant or pin', () => {
    const feed = { trackId: 'desktop-video', participant: 'AJ', streamURL: 'stream://desktop' };
    const videoOn = presentRemoteVideoParticipants({
      feeds: [feed],
      localNames: ['Tom'],
      mediaStates: parseParticipantMediaStates({
        AJ: { micMuted: false, cameraOff: false, screenSharing: false },
      }),
      roster: ['Tom', 'AJ'],
    });
    const pinnedKey = videoOn[0]?.key ?? null;
    assert.equal(pinnedKey, 'feed:desktop-video');
    assert.equal(videoOn[0]?.streamURL, 'stream://desktop');
    assert.equal(videoOn[0]?.videoOff, false);

    const offMediaStates = parseParticipantMediaStates({
      aj: { micMuted: true, cameraOff: true, screenSharing: false },
    });
    const videoOff = presentRemoteVideoParticipants({
      feeds: [feed],
      localNames: ['Tom'],
      mediaStates: offMediaStates,
      roster: ['Tom', 'AJ'],
    });
    assert.equal(videoOff.length, videoOn.length);
    assert.equal(videoOff[0]?.key, pinnedKey);
    assert.equal(videoOff[0]?.streamURL, undefined);
    assert.equal(videoOff[0]?.videoOff, true);
    assert.equal(participantVideoAccessibilityStatus(videoOff[0]!), 'video off');
    assert.equal(pinnedVideoParticipantIsStale(pinnedKey, videoOff), false);

    const restored = presentRemoteVideoParticipants({
      feeds: [feed],
      localNames: ['Tom'],
      mediaStates: participantMediaStatesFromSnapshot({
        AJ: { micMuted: false, cameraOff: false, screenSharing: false },
      }, offMediaStates),
      roster: ['Tom', 'AJ'],
    });
    assert.equal(restored[0]?.key, pinnedKey);
    assert.equal(restored[0]?.streamURL, 'stream://desktop');
    assert.equal(restored[0]?.videoOff, false);
    assert.equal(participantVideoAccessibilityStatus(restored[0]!), 'video on');
  });

  it('keeps endpoint B keyed to its track while same-name endpoint A arrives and leaves', () => {
    const endpointA = { trackId: 'aj-endpoint-a', participant: 'AJ', streamURL: 'stream://a' };
    const endpointB = { trackId: 'aj-endpoint-b', participant: 'AJ', streamURL: 'stream://b' };
    const input = {
      localNames: ['Tom'],
      mediaStates: parseParticipantMediaStates({
        AJ: { micMuted: false, cameraOff: false, screenSharing: false },
      }),
      roster: ['Tom', 'AJ'],
    };

    const before = presentRemoteVideoParticipants({ ...input, feeds: [endpointB] });
    const pinnedKey = before[0]?.key ?? null;
    assert.equal(pinnedKey, 'feed:aj-endpoint-b');

    const during = presentRemoteVideoParticipants({ ...input, feeds: [endpointA, endpointB] });
    assert.equal(during.find((participant) => participant.streamURL === 'stream://b')?.key, pinnedKey);
    assert.equal(pinnedVideoParticipantIsStale(pinnedKey, during), false);
    const duringReverseArrival = presentRemoteVideoParticipants({ ...input, feeds: [endpointB, endpointA] });
    assert.equal(duringReverseArrival.find((participant) => participant.streamURL === 'stream://b')?.key, pinnedKey);
    assert.equal(pinnedVideoParticipantIsStale(pinnedKey, duringReverseArrival), false);

    const after = presentRemoteVideoParticipants({ ...input, feeds: [endpointB] });
    assert.equal(after[0]?.key, pinnedKey);
    assert.equal(pinnedVideoParticipantIsStale(pinnedKey, after), false);
    assert.equal(
      remoteVideoPresentationKey('stream:aj-endpoint-b:222'),
      remoteVideoPresentationKey('stream:aj-endpoint-b:333'),
    );
  });

  it('keeps same-account desktop sharing while the phone camera is independently off', () => {
    const legacyStates = parseParticipantMediaStates({
      AJ: { micMuted: false, cameraOff: true, screenSharing: false },
    });
    const endpointMediaStates = participantEndpointMediaStatesFromSnapshot({
      AJ: {
        '': { micMuted: false, cameraOff: false, screenSharing: false },
        'aj-desktop': { micMuted: false, cameraOff: true, screenSharing: true },
        'aj-phone': { micMuted: true, cameraOff: true, screenSharing: false },
      },
    }, {});
    assert.equal(Object.hasOwn(endpointMediaStates.aj ?? {}, ''), false);
    assert.equal(participantEndpointMediaStatesFromSnapshot({ AJ: { phone: null } }, endpointMediaStates), endpointMediaStates);
    assert.equal(participantEndpointMediaStatesSnapshotIsAuthoritative({ AJ: { phone: null } }), false);
    assert.equal(participantEndpointMediaStatesSnapshotIsAuthoritative(undefined), false);
    assert.equal(participantEndpointMediaStatesSnapshotIsAuthoritative({ AJ: endpointMediaStates.aj }), true);
    const desktop = {
      endpointId: 'aj-desktop',
      participant: 'AJ',
      streamURL: 'stream://desktop-share',
      trackId: 'desktop-share-track',
    };
    const phone = {
      endpointId: 'aj-phone',
      participant: 'AJ',
      streamURL: 'stream://phone-camera',
      trackId: 'phone-camera-track',
    };
    const input = {
      endpointMediaStates,
      localNames: ['Tom'],
      mediaStates: legacyStates,
      roster: ['Tom', 'AJ'],
    };

    const together = presentRemoteVideoParticipants({ ...input, feeds: [desktop, phone] });
    const desktopPresentation = together.find((participant) => participant.key === 'endpoint:aj:aj-desktop');
    const phonePresentation = together.find((participant) => participant.key === 'endpoint:aj:aj-phone');
    assert.equal(desktopPresentation?.streamURL, 'stream://desktop-share');
    assert.equal(desktopPresentation?.videoOff, false);
    assert.equal(desktopPresentation?.screenSharing, true);
    assert.equal(desktopPresentation?.endpointId, 'aj-desktop');
    assert.equal(phonePresentation?.streamURL, undefined);
    assert.equal(phonePresentation?.videoOff, true);
    assert.equal(phonePresentation?.micMuted, true);
    assert.equal(phonePresentation?.screenSharing, false);

    const pinnedDesktopKey = desktopPresentation?.key ?? null;
    const phoneLeft = presentRemoteVideoParticipants({ ...input, feeds: [desktop] });
    assert.equal(pinnedVideoParticipantIsStale(pinnedDesktopKey, phoneLeft), false);

    const phoneReconnected = presentRemoteVideoParticipants({
      ...input,
      feeds: [desktop, { ...phone, trackId: 'phone-camera-track-reconnected' }],
    });
    assert.equal(
      phoneReconnected.find((participant) => participant.name === 'AJ' && participant.videoOff)?.key,
      'endpoint:aj:aj-phone',
    );
    assert.equal(pinnedVideoParticipantIsStale(pinnedDesktopKey, phoneReconnected), false);
  });

  it('normalizes media-state names and treats each snapshot as a complete replacement', () => {
    const current = parseParticipantMediaStates({
      ' AJ ': {
        micMuted: true,
        cameraOff: true,
        screenSharing: false,
        updatedAt: '2026-07-25T10:00:00Z',
      },
      invalid: null,
    });
    assert.deepEqual(current, {
      aj: {
        micMuted: true,
        cameraOff: true,
        screenSharing: false,
        updatedAt: '2026-07-25T10:00:00Z',
      },
    });
    assert.equal(participantMediaStatesFromSnapshot(undefined, current), current);
    assert.equal(participantMediaStatesFromSnapshot('malformed', current), current);
    assert.equal(participantMediaStatesFromSnapshot({ AJ: null }, current), current);
    assert.deepEqual(participantMediaStatesFromSnapshot({}, current), {});
  });

  it('keeps an active screen share visible when that participant camera is off', () => {
    const participants = presentRemoteVideoParticipants({
      feeds: [{ trackId: 'shared-screen', participant: 'AJ', streamURL: 'stream://screen' }],
      localNames: ['Tom'],
      mediaStates: parseParticipantMediaStates({
        AJ: { cameraOff: true, micMuted: false, screenSharing: true, suspended: true },
      }),
      roster: ['Tom', 'AJ'],
    });

    assert.equal(participants[0]?.streamURL, 'stream://screen');
    assert.equal(participants[0]?.videoOff, false);
    assert.equal(participants[0]?.screenSharing, true);
    assert.equal(participantVideoAccessibilityStatus(participants[0]!), 'screen sharing');
  });

  it('promotes a screen share over the active speaker unless the viewer pins someone', () => {
    const participants = presentRemoteVideoParticipants({
      activeSpeaker: 'Erick',
      feeds: [
        { trackId: 'erick-camera', participant: 'Erick', streamURL: 'stream://erick' },
        { trackId: 'aj-screen', participant: 'AJ', streamURL: 'stream://screen' },
      ],
      localNames: ['Tom'],
      mediaStates: parseParticipantMediaStates({
        Erick: { cameraOff: false, micMuted: false, screenSharing: false },
        AJ: { cameraOff: true, micMuted: true, screenSharing: true },
      }),
      roster: ['Tom', 'Erick', 'AJ'],
    });
    const erick = participants.find((participant) => participant.name === 'Erick');
    const aj = participants.find((participant) => participant.name === 'AJ');

    assert.equal(focusedVideoParticipant(participants, null)?.key, aj?.key);
    assert.equal(focusedVideoParticipant(participants, erick?.key ?? null)?.key, erick?.key);
  });

  it('does not promote an unavailable active feed over healthy video', () => {
    const unavailableActive = {
      key: 'aj', name: 'AJ', active: true, micMuted: false,
      screenSharing: false, videoOff: false,
    };
    const healthy = {
      key: 'erick', name: 'Erick', streamURL: 'stream://erick', active: false,
      micMuted: false, screenSharing: false, videoOff: false,
    };

    assert.equal(focusedVideoParticipant([unavailableActive, healthy], null)?.key, 'erick');
    assert.equal(focusedVideoParticipant([unavailableActive, healthy], 'aj')?.key, 'aj');
  });

  it('keeps PiP available for silent group calls and follows deliberate focus', () => {
    const erick = {
      key: 'erick', name: 'Erick', streamURL: 'stream://erick', active: false,
      micMuted: false, screenSharing: false, videoOff: false,
    };
    const aj = {
      key: 'aj', name: 'AJ', streamURL: 'stream://aj', active: false,
      micMuted: false, screenSharing: false, videoOff: false,
    };

    assert.equal(pictureInPictureParticipant([erick, aj], null)?.key, 'erick');
    assert.equal(pictureInPictureParticipant([erick, { ...aj, active: true }], null)?.key, 'aj');
    assert.equal(pictureInPictureParticipant([erick, aj], 'erick')?.key, 'erick');
    assert.equal(pictureInPictureParticipant([erick], null)?.key, 'erick');
  });

  it('includes camera-off endpoints in the People device roster without adding blank stage tiles', () => {
    const endpointMediaStates = {
      aj: {
        'desktop-1': { micMuted: false, cameraOff: false, screenSharing: false },
        'ios-quiet-2': { micMuted: true, cameraOff: true, screenSharing: false },
      },
    };
    const stage = presentRemoteVideoParticipants({
      feeds: [{
        trackId: 'desktop-track',
        participant: 'AJ',
        endpointId: 'desktop-1',
        streamURL: 'desktop-stream',
      }],
      roster: ['Tom', 'AJ'],
      localNames: ['Tom'],
      mediaStates: {
        aj: { micMuted: false, cameraOff: false, screenSharing: false },
      },
      endpointMediaStates,
    });

    assert.deepEqual(stage.map((participant) => participant.endpointId), ['desktop-1']);

    const devices = presentRemoteParticipantDevices({
      participants: stage,
      endpointMediaStates,
      localNames: ['Tom'],
    });
    assert.deepEqual(devices.map((participant) => participant.endpointId), ['desktop-1', 'ios-quiet-2']);
    assert.equal(devices[1]?.micMuted, true);
    assert.equal(devices[1]?.videoOff, true);
    assert.equal(devices[1]?.streamURL, undefined);
  });

  it('keeps camera-off identities and pins without assigning them empty video-stage slots', () => {
    const participants = presentRemoteVideoParticipants({
      activeSpeaker: 'Caitlyn',
      feeds: [
        {
          trackId: 'tyler-screen',
          participant: 'Tyler',
          endpointId: 'tyler-desktop',
          streamURL: 'stream://screen',
        },
        {
          trackId: 'caitlyn-camera',
          participant: 'Caitlyn',
          endpointId: 'caitlyn-phone',
          streamURL: 'stream://camera',
        },
      ],
      localNames: ['AJ'],
      mediaStates: {
        tyler: { micMuted: false, cameraOff: true, screenSharing: true },
        caitlyn: { micMuted: true, cameraOff: true, screenSharing: false },
      },
      endpointMediaStates: {
        tyler: {
          'tyler-desktop': { micMuted: false, cameraOff: true, screenSharing: true },
        },
        caitlyn: {
          'caitlyn-phone': { micMuted: true, cameraOff: true, screenSharing: false },
        },
      },
      roster: ['AJ', 'Tyler', 'Caitlyn'],
    });
    const caitlyn = participants.find((participant) => participant.name === 'Caitlyn');
    const pinnedKey = caitlyn?.key ?? null;

    assert.equal(participants.length, 2, 'the authoritative roster keeps both people');
    assert.equal(caitlyn?.videoOff, true);
    assert.equal(pinnedVideoParticipantIsStale(pinnedKey, participants), false);
    assert.deepEqual(
      videoStageParticipants(participants).map((participant) => participant.name),
      ['Tyler'],
      'only the live screen share receives a video tile',
    );

    const restored = participants.map((participant) => participant.key === pinnedKey
      ? { ...participant, streamURL: 'stream://restored-camera', videoOff: false }
      : participant);
    assert.equal(videoStageParticipants(restored).some((participant) => participant.key === pinnedKey), true);
    assert.equal(focusedVideoParticipant(restored, pinnedKey)?.key, pinnedKey);
  });
});
