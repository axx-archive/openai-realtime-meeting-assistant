import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  createNativeRoomConnectionGenerationGuard,
  createNativeRoomJoinAttemptGuard,
  settleGenerationOperation,
  settleGenerationResource,
  type PeerGeneration,
} from '../realtime/connectionGeneration';
import {
  createDisconnectedIceRestartController,
  disconnectedIceRestartGraceMs,
  type RecoverableIcePeer,
} from '../realtime/iceRecovery';
import {
  createSerializedLocalMediaRecovery,
  detachStalledLocalVideoTracks,
  installRecoveredLocalAudioTrack,
  installRecoveredLocalVideoTrack,
  localAudioPublicationPendingState,
  setLocalAudioTracksEnabled,
} from '../realtime/localAudioRecovery';
import {
  createSerializedVideoSenderMutations,
  installScreenShareTrack,
  restoreAfterScreenShare,
  screenShareMadeProgress,
  screenShareProgress,
  screenShareStopIsCurrent,
  screenShareStopShouldBegin,
} from '../realtime/localScreenShare';
import { pinnedVideoParticipantIsStale } from '../realtime/callPresentation';
import { nextZeroOutboundVideoIntervalCount, summarizeNativeRoomStats } from '../realtime/quality';
import {
  createRemoteVideoMuteController,
  remoteVideoMuteGraceMs,
  type MutableRemoteVideoTrack,
} from '../realtime/remoteTrackMute';
import {
  createRemoteVideoRecoveryState,
  nextRemoteVideoRecoveryDecision,
  nextRemoteVideoProgressState,
  remoteVideoIceRestartCooldownMs,
  remoteVideoIceRestartIntervals,
  remoteVideoMaxIceRestartsPerConnection,
  remoteVideoProgressSample,
  remoteVideoStallIntervals,
} from '../realtime/remoteTrackProgress';
import {
  indexParticipantTrack,
  participantForTrack,
  reconcileRemoteParticipantRoster,
  reconcileRemoteVideoOffer,
  removeRemoteParticipantMedia,
} from '../realtime/trackIdentity';
import {
  containedVideoLabelPosition,
  fittedVideoDimensions,
  nativeVideoRenderIdentity,
} from '../utils/videoLayout';
import {
  applyNativeCameraSenderPolicy,
  nativeCameraMaxBitrate,
} from '../realtime/videoSenderPolicy';

describe('native room media', () => {
  it('keeps initial and reconnected peers visibly muted until publication commits', () => {
    assert.deepEqual(localAudioPublicationPendingState(true), {
      microphoneStarting: true,
      muted: true,
    });
    assert.deepEqual(localAudioPublicationPendingState(false), {
      microphoneStarting: false,
      muted: true,
    });
  });

  it('gives iPhone camera video enough headroom while preserving congestion control', () => {
    const parameters = {
      encodings: [{
        maxBitrate: 1_200_000,
        minBitrate: 900_000,
        maxFramerate: 15,
        scaleResolutionDownBy: 2,
      }],
      degradationPreference: null,
    } as unknown as Parameters<typeof applyNativeCameraSenderPolicy>[0];

    applyNativeCameraSenderPolicy(parameters);

    assert.equal(nativeCameraMaxBitrate, 2_500_000);
    assert.deepEqual(parameters.encodings, [{
      maxBitrate: 2_500_000,
      minBitrate: null,
      maxFramerate: 30,
      scaleResolutionDownBy: 1,
    }]);
    assert.equal(parameters.degradationPreference, 'maintain-framerate');
  });

  it('serializes off-on recovery and queues the latest on intent after rollback', () => {
    const recovery = createSerializedLocalMediaRecovery();
    const first = recovery.begin();
    assert.ok(first);
    assert.equal(recovery.isRunning(), true);

    // The user turns the device off while the first replaceTrack is settling,
    // then back on before its awaited rollback completes. A second sender
    // mutation must not overlap the first one.
    assert.equal(recovery.begin(), null);
    assert.equal(recovery.settle(first, true), 'retry');

    const retry = recovery.begin();
    assert.ok(retry);
    assert.notEqual(retry, first);
    recovery.retire();
    assert.equal(recovery.isRunning(), false);
    assert.equal(recovery.isActive(retry), false);
    assert.equal(recovery.settle(retry, true), 'stale');
  });

  it('waits for a deferred visible-state commit before attaching or enabling a recovered microphone', async () => {
    const oldTrack = {
      enabled: false,
      kind: 'audio',
      readyState: 'ended',
      stopped: false,
      released: false,
      stop() { this.stopped = true; },
      release() { this.released = true; },
    };
    const freshTrack = {
      enabled: false,
      kind: 'audio',
      readyState: 'live',
      stopped: false,
      stop() { this.stopped = true; },
    };
    let localTracks = [oldTrack, freshTrack].slice(0, 1) as Array<typeof oldTrack | typeof freshTrack>;
    const local = {
      addTrack: (track: typeof oldTrack | typeof freshTrack) => { localTracks.push(track); },
      getAudioTracks: () => localTracks,
      getVideoTracks: () => [],
      removeTrack: (track: typeof oldTrack | typeof freshTrack) => {
        localTracks = localTracks.filter((candidate) => candidate !== track);
      },
    };
    let captureTracks = [freshTrack];
    let captureReleasedWithTracks: boolean | undefined;
    const capture = {
      addTrack: (track: typeof freshTrack) => { captureTracks.push(track); },
      getAudioTracks: () => captureTracks,
      getVideoTracks: () => [],
      getTracks: () => captureTracks,
      removeTrack: (track: typeof freshTrack) => {
        captureTracks = captureTracks.filter((candidate) => candidate !== track);
      },
      release: (releaseTracks?: boolean) => { captureReleasedWithTracks = releaseTracks; },
    };
    let senderTrack: typeof freshTrack | null = null;
    const enabledAtAttachment: boolean[] = [];
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof freshTrack | null) => {
        if (track) enabledAtAttachment.push(track.enabled);
        senderTrack = track;
      },
    };

    let publicationRequested!: () => void;
    const publicationWasRequested = new Promise<void>((resolve) => { publicationRequested = resolve; });
    let resolvePublication!: (committed: boolean) => void;
    const publicationCommit = new Promise<boolean>((resolve) => { resolvePublication = resolve; });
    let visibleMuted = true;

    const installation = installRecoveredLocalAudioTrack({
      capture,
      commitPublication: () => {
        publicationRequested();
        return publicationCommit;
      },
      isCurrent: () => true,
      local,
      sender,
    });

    await publicationWasRequested;
    assert.equal(visibleMuted, true);
    assert.equal(sender.track, null);
    assert.deepEqual(local.getAudioTracks(), [freshTrack]);
    assert.equal(freshTrack.enabled, false);
    assert.deepEqual(enabledAtAttachment, []);

    // Model React's deferred commit: the rendered state advances first, then
    // the publication barrier resolves and the native sender may be attached.
    visibleMuted = false;
    resolvePublication(true);
    const result = await installation;

    assert.equal(result, 'installed');
    assert.equal(visibleMuted, false);
    assert.deepEqual(enabledAtAttachment, [false]);
    assert.equal(sender.track, freshTrack);
    assert.deepEqual(local.getAudioTracks(), [freshTrack]);
    assert.equal(freshTrack.enabled, true);
    assert.equal(freshTrack.stopped, false);
    assert.equal(oldTrack.stopped, true);
    assert.equal(oldTrack.released, true);
    assert.equal(captureReleasedWithTracks, false);
  });

  it('releases a late microphone permission result after mute, leave, or reconnect', async () => {
    const freshTrack = {
      enabled: false,
      kind: 'audio',
      readyState: 'live',
      stopped: false,
      stop() { this.stopped = true; },
    };
    let captureReleased = false;
    const capture = {
      addTrack: () => undefined,
      getAudioTracks: () => [freshTrack],
      getVideoTracks: () => [],
      getTracks: () => [freshTrack],
      removeTrack: () => undefined,
      release: () => { captureReleased = true; },
    };
    let replaceCalls = 0;
    const sender = {
      track: null,
      replaceTrack: async () => { replaceCalls += 1; },
    };
    const local = {
      addTrack: () => undefined,
      getAudioTracks: () => [],
      getVideoTracks: () => [],
      removeTrack: () => undefined,
    };

    const result = await installRecoveredLocalAudioTrack({
      capture,
      commitPublication: async () => true,
      isCurrent: () => false,
      local,
      sender,
    });

    assert.equal(result, 'cancelled');
    assert.equal(replaceCalls, 0);
    assert.equal(freshTrack.stopped, true);
    assert.equal(captureReleased, true);
  });

  it('never attaches or enables a recovered microphone after background invalidates its commit', async () => {
    const freshTrack = {
      enabled: true,
      kind: 'audio',
      readyState: 'live',
      stopped: false,
      stop() { this.stopped = true; },
    };
    let captureTracks = [freshTrack];
    const capture = {
      addTrack: (track: typeof freshTrack) => { captureTracks.push(track); },
      getAudioTracks: () => captureTracks,
      getVideoTracks: () => [],
      getTracks: () => captureTracks,
      removeTrack: (track: typeof freshTrack) => {
        captureTracks = captureTracks.filter((candidate) => candidate !== track);
      },
      release: () => undefined,
    };
    let localTracks: typeof freshTrack[] = [];
    const local = {
      addTrack: (track: typeof freshTrack) => { localTracks.push(track); },
      getAudioTracks: () => localTracks,
      getVideoTracks: () => [],
      removeTrack: (track: typeof freshTrack) => {
        localTracks = localTracks.filter((candidate) => candidate !== track);
      },
    };
    let senderTrack: typeof freshTrack | null = null;
    let attachmentCalls = 0;
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof freshTrack | null) => {
        attachmentCalls += 1;
        senderTrack = track;
      },
    };
    let publicationRequested!: () => void;
    const publicationWasRequested = new Promise<void>((resolve) => { publicationRequested = resolve; });
    let resolvePublication!: (committed: boolean) => void;
    const publicationCommit = new Promise<boolean>((resolve) => { resolvePublication = resolve; });
    let appActive = true;

    const installation = installRecoveredLocalAudioTrack({
      capture,
      commitPublication: () => {
        publicationRequested();
        return publicationCommit;
      },
      isCurrent: () => appActive,
      local,
      sender,
    });

    await publicationWasRequested;
    appActive = false;
    resolvePublication(true);
    const result = await installation;

    assert.equal(result, 'cancelled');
    assert.equal(attachmentCalls, 0);
    assert.equal(sender.track, null);
    assert.equal(freshTrack.enabled, false);
    assert.equal(freshTrack.stopped, true);
    assert.deepEqual(local.getAudioTracks(), []);
  });

  it('rolls back a microphone sender when mute wins during replaceTrack', async () => {
    const freshTrack = {
      enabled: false,
      kind: 'audio',
      readyState: 'live',
      stopped: false,
      stop() { this.stopped = true; },
    };
    let captureReleased = false;
    const capture = {
      addTrack: () => undefined,
      getAudioTracks: () => [freshTrack],
      getVideoTracks: () => [],
      getTracks: () => [freshTrack],
      removeTrack: () => undefined,
      release: () => { captureReleased = true; },
    };
    let senderTrack: typeof freshTrack | null = null;
    const replacements: Array<typeof freshTrack | null> = [];
    const enabledAtAttachment: boolean[] = [];
    let replacementStarted!: () => void;
    const replacementWasStarted = new Promise<void>((resolve) => { replacementStarted = resolve; });
    let finishReplacement!: () => void;
    const replacementCanFinish = new Promise<void>((resolve) => { finishReplacement = resolve; });
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof freshTrack | null) => {
        replacements.push(track);
        if (track) enabledAtAttachment.push(track.enabled);
        senderTrack = track;
        if (track) {
          replacementStarted();
          await replacementCanFinish;
        }
      },
    };
    const local = {
      addTrack: () => undefined,
      getAudioTracks: () => [],
      getVideoTracks: () => [],
      removeTrack: () => undefined,
    };
    let current = true;

    const installation = installRecoveredLocalAudioTrack({
      capture,
      commitPublication: async () => true,
      isCurrent: () => current,
      local,
      sender,
    });

    await replacementWasStarted;
    current = false;
    finishReplacement();
    const result = await installation;

    assert.equal(result, 'cancelled');
    assert.deepEqual(replacements, [freshTrack, null]);
    assert.deepEqual(enabledAtAttachment, [false]);
    assert.equal(sender.track, null);
    assert.equal(freshTrack.stopped, true);
    assert.equal(captureReleased, true);
  });

  it('synchronously disables and stops every audio source when recovery cleanup throws', async () => {
    const makeTrack = (label: string) => ({
      enabled: true,
      kind: 'audio',
      label,
      readyState: 'live',
      stopped: false,
      released: false,
      stop() { this.stopped = true; },
      release() { this.released = true; },
    });
    const oldTrack = makeTrack('old-local');
    const freshTrack = makeTrack('fresh-capture');
    const senderOnlyTrack = makeTrack('sender-only');
    let localTracks = [oldTrack];
    const local = {
      addTrack: (track: typeof oldTrack) => { localTracks.push(track); },
      getAudioTracks: () => localTracks,
      getVideoTracks: () => [],
      removeTrack: (track: typeof oldTrack) => {
        localTracks = localTracks.filter((candidate) => candidate !== track);
      },
    };
    let captureTracks = [freshTrack];
    const capture = {
      addTrack: (track: typeof freshTrack) => { captureTracks.push(track); },
      getAudioTracks: () => captureTracks,
      getVideoTracks: () => [],
      getTracks: () => captureTracks,
      removeTrack: (track: typeof freshTrack) => {
        captureTracks = captureTracks.filter((candidate) => candidate !== track);
      },
      release: (releaseTracks?: boolean) => {
        if (releaseTracks === false) throw new Error('injected capture cleanup failure');
      },
    };
    let senderTrack: typeof senderOnlyTrack | null = senderOnlyTrack;
    let detachStarted!: () => void;
    const detachmentWasStarted = new Promise<void>((resolve) => { detachStarted = resolve; });
    let finishDetachment!: () => void;
    const detachmentCanFinish = new Promise<void>((resolve) => { finishDetachment = resolve; });
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof senderOnlyTrack | null) => {
        detachStarted();
        await detachmentCanFinish;
        senderTrack = track;
      },
    };
    let publicationRequested = false;

    const rejectedInstallation = assert.rejects(
      installRecoveredLocalAudioTrack({
        capture,
        commitPublication: async () => {
          publicationRequested = true;
          return true;
        },
        isCurrent: () => true,
        local,
        sender,
      }),
      /injected capture cleanup failure/,
    );

    await detachmentWasStarted;
    // The sender bridge is still awaiting detachment, but every reachable
    // source must already be inert on the same synchronous cleanup turn.
    [oldTrack, freshTrack, senderOnlyTrack].forEach((track) => {
      assert.equal(track.enabled, false, `${track.label} stayed enabled before detach`);
      assert.equal(track.stopped, true, `${track.label} was not stopped before detach`);
      assert.equal(track.released, true, `${track.label} was not released before detach`);
    });
    finishDetachment();
    await rejectedInstallation;

    assert.equal(publicationRequested, false);
    assert.equal(sender.track, null);
    assert.deepEqual(local.getAudioTracks(), []);
    [oldTrack, freshTrack, senderOnlyTrack].forEach((track) => {
      assert.equal(track.enabled, false, `${track.label} stayed enabled`);
      assert.equal(track.stopped, true, `${track.label} was not stopped`);
      assert.equal(track.released, true, `${track.label} was not released`);
    });
  });

  it('leaves a native sender inert when attachment mutates then rollback repeatedly fails', async () => {
    const freshTrack = {
      enabled: true,
      kind: 'audio',
      readyState: 'live',
      stopped: false,
      released: false,
      stop() { this.stopped = true; },
      release() { this.released = true; },
    };
    let localTracks: typeof freshTrack[] = [];
    const local = {
      addTrack: (track: typeof freshTrack) => { localTracks.push(track); },
      getAudioTracks: () => localTracks,
      getVideoTracks: () => [],
      removeTrack: (track: typeof freshTrack) => {
        localTracks = localTracks.filter((candidate) => candidate !== track);
      },
    };
    let captureTracks = [freshTrack];
    const capture = {
      addTrack: (track: typeof freshTrack) => { captureTracks.push(track); },
      getAudioTracks: () => captureTracks,
      getVideoTracks: () => [],
      getTracks: () => captureTracks,
      removeTrack: (track: typeof freshTrack) => {
        captureTracks = captureTracks.filter((candidate) => candidate !== track);
      },
      release: () => undefined,
    };
    let senderTrack: typeof freshTrack | null = null;
    let detachAttempts = 0;
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof freshTrack | null) => {
        if (track) {
          senderTrack = track;
          throw new Error('injected attachment failure after native mutation');
        }
        detachAttempts += 1;
        throw new Error('injected rollback failure');
      },
    };

    await assert.rejects(
      installRecoveredLocalAudioTrack({
        capture,
        commitPublication: async () => true,
        isCurrent: () => true,
        local,
        sender,
      }),
      /injected attachment failure/,
    );

    assert.ok(detachAttempts >= 2);
    assert.equal(sender.track, freshTrack);
    assert.equal(freshTrack.enabled, false);
    assert.equal(freshTrack.stopped, true);
    assert.equal(freshTrack.released, true);
    assert.deepEqual(local.getAudioTracks(), []);
  });

  it('mutes local and sender-only microphone tracks without touching video', () => {
    const localAudio = {
      enabled: true,
      kind: 'audio',
      readyState: 'live',
    };
    const senderOnlyAudio = {
      enabled: true,
      kind: 'audio',
      readyState: 'live',
    };
    const localVideo = {
      enabled: true,
      kind: 'video',
      readyState: 'live',
    };

    setLocalAudioTracksEnabled([
      localAudio,
      senderOnlyAudio,
      localAudio,
      localVideo,
    ], false);

    assert.equal(localAudio.enabled, false);
    assert.equal(senderOnlyAudio.enabled, false);
    assert.equal(localVideo.enabled, true);
  });

  it('reapplies the latest microphone intent before reconnect attachment', () => {
    const track = {
      enabled: true,
      kind: 'audio',
      readyState: 'live',
    };

    setLocalAudioTracksEnabled([track], false);
    assert.equal(track.enabled, false);

    setLocalAudioTracksEnabled([track], true);
    assert.equal(track.enabled, true);
  });

  it('rolls back and releases a camera permission result when video is cancelled during attachment', async () => {
    const freshTrack = {
      enabled: false,
      kind: 'video',
      readyState: 'live',
      stopped: false,
      stop() { this.stopped = true; },
    };
    let captureReleased = false;
    const capture = {
      addTrack: () => undefined,
      getAudioTracks: () => [],
      getVideoTracks: () => [freshTrack],
      getTracks: () => [freshTrack],
      removeTrack: () => undefined,
      release: () => { captureReleased = true; },
    };
    let senderTrack: typeof freshTrack | null = null;
    const replacements: Array<typeof freshTrack | null> = [];
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof freshTrack | null) => {
        replacements.push(track);
        senderTrack = track;
      },
    };
    const local = {
      addTrack: () => undefined,
      getAudioTracks: () => [],
      getVideoTracks: () => [],
      removeTrack: () => undefined,
    };
    let currentChecks = 0;

    const result = await installRecoveredLocalVideoTrack({
      capture,
      isCurrent: () => ++currentChecks === 1,
      local,
      sender,
    });

    assert.equal(result, 'cancelled');
    assert.deepEqual(replacements, [freshTrack, null]);
    assert.equal(sender.track, null);
    assert.equal(freshTrack.stopped, true);
    assert.equal(captureReleased, true);
  });

  it('attaches a camera acquired after a quiet join without renegotiating the call', async () => {
    const freshTrack = {
      enabled: false,
      kind: 'video',
      readyState: 'live',
      stopped: false,
      stop() { this.stopped = true; },
    };
    let captureTracks = [freshTrack];
    let captureReleasedWithTracks: boolean | undefined;
    const capture = {
      addTrack: (track: typeof freshTrack) => { captureTracks.push(track); },
      getAudioTracks: () => [],
      getVideoTracks: () => captureTracks,
      getTracks: () => captureTracks,
      removeTrack: (track: typeof freshTrack) => {
        captureTracks = captureTracks.filter((candidate) => candidate !== track);
      },
      release: (releaseTracks?: boolean) => { captureReleasedWithTracks = releaseTracks; },
    };
    let localTracks: typeof freshTrack[] = [];
    const local = {
      addTrack: (track: typeof freshTrack) => { localTracks.push(track); },
      getAudioTracks: () => [],
      getVideoTracks: () => localTracks,
      removeTrack: (track: typeof freshTrack) => {
        localTracks = localTracks.filter((candidate) => candidate !== track);
      },
    };
    let senderTrack: typeof freshTrack | null = null;
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof freshTrack | null) => { senderTrack = track; },
    };

    const result = await installRecoveredLocalVideoTrack({
      capture,
      isCurrent: () => true,
      local,
      sender,
    });

    assert.equal(result, 'installed');
    assert.equal(sender.track, freshTrack);
    assert.deepEqual(local.getVideoTracks(), [freshTrack]);
    assert.equal(freshTrack.enabled, true);
    assert.equal(freshTrack.stopped, false);
    assert.equal(captureReleasedWithTracks, false);
  });

  it('detaches and releases a stalled camera before replacement capture begins', async () => {
    const stalledTrack = {
      enabled: true,
      kind: 'video',
      readyState: 'live',
      stopped: false,
      released: false,
      stop() { this.stopped = true; },
      release() { this.released = true; },
    };
    let localTracks = [stalledTrack];
    const local = {
      addTrack: (track: typeof stalledTrack) => { localTracks.push(track); },
      getAudioTracks: () => [],
      getVideoTracks: () => localTracks,
      removeTrack: (track: typeof stalledTrack) => {
        localTracks = localTracks.filter((candidate) => candidate !== track);
      },
    };
    let senderTrack: typeof stalledTrack | null = stalledTrack;
    const replacements: Array<typeof stalledTrack | null> = [];
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof stalledTrack | null) => {
        replacements.push(track);
        senderTrack = track;
      },
    };

    const result = await detachStalledLocalVideoTracks({
      isCurrent: () => true,
      local,
      sender,
    });

    assert.equal(result, 'detached');
    assert.deepEqual(replacements, [null]);
    assert.equal(sender.track, null);
    assert.deepEqual(local.getVideoTracks(), []);
    assert.equal(stalledTrack.enabled, false);
    assert.equal(stalledTrack.stopped, true);
    assert.equal(stalledTrack.released, true);
  });

  it('requires both outbound bytes and frames before announcing a screen share', () => {
    const baseline = screenShareProgress(new Map([
      ['video', { type: 'outbound-rtp', kind: 'video', bytesSent: 100, framesSent: 4 }],
      ['audio', { type: 'outbound-rtp', kind: 'audio', bytesSent: 9_000, framesSent: 9_000 }],
    ]));
    assert.deepEqual(baseline, { bytesSent: 100, framesSent: 4 });
    assert.equal(screenShareMadeProgress(baseline, { bytesSent: 140, framesSent: 4 }), false);
    assert.equal(screenShareMadeProgress(baseline, { bytesSent: 140, framesSent: 5 }), true);
  });

  it('verifies native sender attachment and restores the camera before release', async () => {
    const camera = {
      id: 'camera', kind: 'video', enabled: true, readyState: 'live', stop() {},
    };
    const screen = {
      id: 'screen', kind: 'video', enabled: false, readyState: 'live', stop() {},
    };
    let senderTrack: typeof camera | typeof screen | null = camera;
    const replacements: Array<typeof camera | typeof screen | null> = [];
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof senderTrack) => {
        replacements.push(track);
        senderTrack = track;
      },
      getStats: async () => new Map<string, Record<string, unknown>>(),
    };

    const installed = await installScreenShareTrack({ sender, track: screen, isCurrent: () => true });
    assert.equal(installed.outcome, 'installed');
    assert.equal(installed.previousTrack, camera);
    assert.equal(screen.enabled, true);
    assert.equal(sender.track, screen);

    await restoreAfterScreenShare({ sender, screenTrack: screen, restoreTrack: camera });
    assert.equal(sender.track, camera);
    assert.deepEqual(replacements, [screen, camera]);
  });

  it('serializes stale camera rollback, ReplayKit start, and camera restoration', async () => {
    const stableCamera = {
      id: 'stable-camera', kind: 'video', enabled: true, readyState: 'live', stopped: false,
      stop() { this.stopped = true; },
    };
    const recoveredCamera = {
      id: 'recovered-camera', kind: 'video', enabled: false, readyState: 'live', stopped: false,
      stop() { this.stopped = true; },
    };
    const screen = {
      id: 'screen', kind: 'video', enabled: false, readyState: 'live', stopped: false,
      stop() { this.stopped = true; },
    };
    let senderTrack: typeof stableCamera | typeof recoveredCamera | typeof screen | null = stableCamera;
    let activeMutations = 0;
    let maximumConcurrentMutations = 0;
    const replacements: string[] = [];
    let releaseFirstReplacement!: () => void;
    let markFirstReplacementStarted!: () => void;
    const firstReplacementStarted = new Promise<void>((resolve) => { markFirstReplacementStarted = resolve; });
    const firstReplacementGate = new Promise<void>((resolve) => { releaseFirstReplacement = resolve; });
    let firstReplacement = true;
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof senderTrack) => {
        activeMutations += 1;
        maximumConcurrentMutations = Math.max(maximumConcurrentMutations, activeMutations);
        replacements.push(track?.id ?? 'null');
        try {
          if (firstReplacement) {
            firstReplacement = false;
            markFirstReplacementStarted();
            await firstReplacementGate;
          }
          senderTrack = track;
        } finally {
          activeMutations -= 1;
        }
      },
      getStats: async () => new Map<string, Record<string, unknown>>(),
    };
    let localTracks = [stableCamera] as Array<typeof stableCamera | typeof recoveredCamera>;
    const local = {
      addTrack: (track: typeof stableCamera | typeof recoveredCamera) => { localTracks.push(track); },
      getAudioTracks: () => [],
      getVideoTracks: () => localTracks,
      removeTrack: (track: typeof stableCamera | typeof recoveredCamera) => {
        localTracks = localTracks.filter((candidate) => candidate !== track);
      },
    };
    const capture = {
      addTrack: () => undefined,
      getAudioTracks: () => [],
      getVideoTracks: () => [recoveredCamera],
      getTracks: () => [recoveredCamera],
      removeTrack: () => undefined,
      release: () => undefined,
    };
    const mutations = createSerializedVideoSenderMutations();
    let cameraRecoveryCurrent = true;
    const recoveringCamera = mutations.run(sender, () => installRecoveredLocalVideoTrack({
      capture,
      isCurrent: () => cameraRecoveryCurrent,
      local,
      sender,
    }));
    await firstReplacementStarted;

    // ReplayKit becomes the latest intent while camera replacement is still
    // in native code. Its mutation must wait for the stale camera rollback.
    cameraRecoveryCurrent = false;
    const startingShare = mutations.run(sender, () => installScreenShareTrack({
      sender,
      track: screen,
      isCurrent: () => true,
    }));
    releaseFirstReplacement();

    assert.equal(await recoveringCamera, 'cancelled');
    assert.equal((await startingShare).outcome, 'installed');
    assert.equal(sender.track, screen);
    assert.equal(recoveredCamera.stopped, true);

    await mutations.run(sender, () => restoreAfterScreenShare({
      sender,
      screenTrack: screen,
      restoreTrack: stableCamera,
    }));
    assert.equal(maximumConcurrentMutations, 1);
    assert.deepEqual(replacements, ['recovered-camera', 'stable-camera', 'screen', 'stable-camera']);
    assert.equal(sender.track, stableCamera);
  });

  it('detects a native stale-screen rollback that resolves without changing the sender', async () => {
    const camera = {
      id: 'camera', kind: 'video', enabled: true, readyState: 'live', stop() {},
    };
    const screen = {
      id: 'screen', kind: 'video', enabled: false, readyState: 'live', stop() {},
    };
    let senderTrack: typeof camera | typeof screen | null = camera;
    let replacements = 0;
    const sender = {
      get track() { return senderTrack; },
      replaceTrack: async (track: typeof senderTrack) => {
        replacements += 1;
        if (replacements === 1) senderTrack = track;
        // The second call models react-native-webrtc swallowing native failure.
      },
      getStats: async () => new Map<string, Record<string, unknown>>(),
    };
    let currentChecks = 0;

    await assert.rejects(
      installScreenShareTrack({
        sender,
        track: screen,
        isCurrent: () => ++currentChecks === 1,
      }),
      /previous video track could not be restored/,
    );
    assert.equal(sender.track, screen);
  });

  it('isolates video mutation queues across reconnect senders', async () => {
    const mutations = createSerializedVideoSenderMutations();
    const oldSender = {};
    const newSender = {};
    let releaseOldMutation!: () => void;
    let markOldMutationStarted!: () => void;
    const oldMutationStarted = new Promise<void>((resolve) => { markOldMutationStarted = resolve; });
    const oldMutationGate = new Promise<void>((resolve) => { releaseOldMutation = resolve; });
    const oldMutation = mutations.run(oldSender, async () => {
      markOldMutationStarted();
      await oldMutationGate;
      return 'old';
    });
    await oldMutationStarted;

    // A stuck old peer must not head-of-line block the replacement peer.
    const replacementResult = await mutations.run(newSender, async () => 'new');
    assert.equal(replacementResult, 'new');
    releaseOldMutation();
    assert.equal(await oldMutation, 'old');
  });

  it('rejects a screen-stop completion from an earlier leave/rejoin session', () => {
    const oldSession = {};
    const newSession = {};
    assert.equal(screenShareStopIsCurrent(8, 8, oldSession, oldSession), true);
    assert.equal(screenShareStopIsCurrent(8, 9, oldSession, oldSession), false);
    assert.equal(screenShareStopIsCurrent(8, 8, oldSession, newSession), false);
  });

  it('keeps duplicate Stop calls from stealing the one stop notification', () => {
    let requested = true;
    let announced = true;
    let hasDisplayStream = true;
    let operation = 4;
    let stopNotifications = 0;
    const beginStop = () => {
      if (!screenShareStopShouldBegin(requested, announced, hasDisplayStream)) return null;
      const ownedOperation = ++operation;
      requested = false;
      const ownedAnnouncement = announced;
      announced = false;
      hasDisplayStream = false;
      return { ownedOperation, ownedAnnouncement };
    };

    const first = beginStop();
    const duplicate = beginStop();
    assert.ok(first);
    assert.equal(duplicate, null);
    if (first.ownedAnnouncement && first.ownedOperation === operation) stopNotifications += 1;
    assert.equal(stopNotifications, 1);
  });

  it('fails closed when react-native-webrtc swallows a native screen replacement', async () => {
    const screen = {
      id: 'screen', kind: 'video', enabled: false, readyState: 'live', stop() {},
    };
    const sender = {
      track: null,
      replaceTrack: async () => undefined,
      getStats: async () => new Map<string, Record<string, unknown>>(),
    };

    await assert.rejects(
      installScreenShareTrack({ sender, track: screen, isCurrent: () => true }),
      /could not be attached/,
    );
  });

  it('does not request media when leave cancels a join during client config', async () => {
    const joins = createNativeRoomJoinAttemptGuard();
    const attempt = joins.begin();
    let finishConfig!: () => void;
    const config = new Promise<void>((resolve) => { finishConfig = resolve; });
    let mediaRequests = 0;
    const runJoin = async () => {
      const settled = await settleGenerationOperation(config, () => joins.isCurrent(attempt));
      if (!settled.current) return;
      mediaRequests += 1;
    };

    const pendingJoin = runJoin();
    joins.cancel(attempt);
    finishConfig();
    await pendingJoin;

    assert.equal(mediaRequests, 0);
  });

  it('stops and releases a permission stream that arrives after leave or a newer join', async () => {
    const joins = createNativeRoomJoinAttemptGuard();
    const firstAttempt = joins.begin();
    let finishPermission!: (stream: { tracks: Array<{ stop: () => void }>; release: () => void }) => void;
    const permission = new Promise<{ tracks: Array<{ stop: () => void }>; release: () => void }>((resolve) => {
      finishPermission = resolve;
    });
    let stopped = 0;
    let released = 0;
    const pendingStream = settleGenerationResource(
      permission,
      () => joins.isCurrent(firstAttempt),
      (stream) => {
        stream.tracks.forEach((track) => track.stop());
        stream.release();
      },
    );

    joins.cancel(firstAttempt);
    const secondAttempt = joins.begin();
    joins.cancel(firstAttempt);
    finishPermission({
      tracks: [{ stop: () => { stopped += 1; } }, { stop: () => { stopped += 1; } }],
      release: () => { released += 1; },
    });
    const oldResult = await pendingStream;

    assert.deepEqual(oldResult, { current: false });
    assert.equal(stopped, 2);
    assert.equal(released, 1);
    assert.equal(joins.isCurrent(secondAttempt), true);
  });

  it('rejects late track, ended, ICE, and connection-state callbacks from a replaced peer', () => {
    const generations = createNativeRoomConnectionGenerationGuard();
    const firstSocket = generations.activateSocket();
    const firstPeer = generations.activatePeer(firstSocket);
    assert.ok(firstPeer);

    const feeds = new Map<string, PeerGeneration>();
    const sentCandidates: string[] = [];
    let reconnectingTransitions = 0;
    const callbacksFor = (peer: PeerGeneration) => ({
      ontrack: (trackId: string) => {
        if (generations.isCurrentPeer(peer)) feeds.set(trackId, peer);
      },
      onended: (trackId: string) => {
        if (generations.isCurrentPeer(peer) && feeds.get(trackId) === peer) feeds.delete(trackId);
      },
      onicecandidate: (candidate: string) => {
        if (generations.isCurrentPeer(peer) && generations.isCurrentSocket(peer.socket)) {
          sentCandidates.push(candidate);
        }
      },
      onconnectionstatechange: () => {
        if (generations.isCurrentPeer(peer)) reconnectingTransitions += 1;
      },
    });
    const firstCallbacks = callbacksFor(firstPeer);
    firstCallbacks.ontrack('shared-track');

    const secondSocket = generations.activateSocket();
    const secondPeer = generations.activatePeer(secondSocket);
    assert.ok(secondPeer);
    feeds.clear();
    const secondCallbacks = callbacksFor(secondPeer);
    secondCallbacks.ontrack('shared-track');

    firstCallbacks.ontrack('ghost-track');
    firstCallbacks.onended('shared-track');
    firstCallbacks.onicecandidate('old-candidate');
    firstCallbacks.onconnectionstatechange();
    assert.deepEqual([...feeds.keys()], ['shared-track']);
    assert.equal(feeds.get('shared-track'), secondPeer);
    assert.deepEqual(sentCandidates, []);
    assert.equal(reconnectingTransitions, 0);

    secondCallbacks.onicecandidate('current-candidate');
    secondCallbacks.onconnectionstatechange();
    assert.deepEqual(sentCandidates, ['current-candidate']);
    assert.equal(reconnectingTransitions, 1);
  });

  it('drops an old offer completion after reconnect instead of reconciling or answering on the new socket', async () => {
    const generations = createNativeRoomConnectionGenerationGuard();
    const firstSocket = generations.activateSocket();
    const firstPeer = generations.activatePeer(firstSocket);
    assert.ok(firstPeer);

    let completeOldRemoteDescription!: () => void;
    const oldRemoteDescription = new Promise<void>((resolve) => {
      completeOldRemoteDescription = resolve;
    });
    let reconciliations = 0;
    const answers: number[] = [];
    const finishOffer = async (peer: PeerGeneration, remoteDescription: Promise<void>) => {
      const settled = await settleGenerationOperation(
        remoteDescription,
        () => generations.isCurrentPeer(peer),
      );
      if (!settled.current) return;
      reconciliations += 1;
      if (generations.isCurrentSocket(peer.socket)) answers.push(peer.socket.id);
    };

    const oldOffer = finishOffer(firstPeer, oldRemoteDescription);
    const secondSocket = generations.activateSocket();
    const secondPeer = generations.activatePeer(secondSocket);
    assert.ok(secondPeer);
    completeOldRemoteDescription();
    await oldOffer;

    assert.equal(reconciliations, 0);
    assert.deepEqual(answers, []);

    await finishOffer(secondPeer, Promise.resolve());
    assert.equal(reconciliations, 1);
    assert.deepEqual(answers, [secondSocket.id]);
  });

  it('keeps a contained-video name badge attached to the rendered picture', () => {
    const landscapeInPortrait = containedVideoLabelPosition(
      { width: 390, height: 844 },
      { width: 1920, height: 1080 },
      12,
    );
    assert.ok(landscapeInPortrait);
    assert.equal(landscapeInPortrait.left, 12);
    assert.ok(Math.abs(landscapeInPortrait.bottom - 324.3125) < 0.001);
    assert.equal(landscapeInPortrait.maxWidth, 366);

    const portraitInLandscape = containedVideoLabelPosition(
      { width: 844, height: 390 },
      { width: 1080, height: 1920 },
      12,
    );
    assert.ok(portraitInLandscape);
    assert.ok(Math.abs(portraitInLandscape.left - 324.3125) < 0.001);
    assert.equal(portraitInLandscape.bottom, 12);
    assert.ok(Math.abs(portraitInLandscape.maxWidth - 195.375) < 0.001);

    assert.equal(containedVideoLabelPosition(
      { width: 0, height: 390 },
      { width: 1920, height: 1080 },
      12,
    ), null);
  });

  it('sizes the speaker stage to the video pixels without crop or letterbox', () => {
    assert.deepEqual(
      fittedVideoDimensions({ width: 360, height: 500 }, { width: 1920, height: 1080 }),
      { width: 360, height: 202.5 },
    );
    const portrait = fittedVideoDimensions({ width: 360, height: 500 }, { width: 1080, height: 1920 });
    assert.ok(portrait);
    assert.equal(portrait.width, 281.25);
    assert.ok(Math.abs(portrait.height - 500) < 0.001);
    assert.equal(fittedVideoDimensions({ width: 0, height: 500 }, { width: 1920, height: 1080 }), null);
  });

  it('remounts a native renderer when iOS replaces a track inside the same stream', () => {
    const first = nativeVideoRenderIdentity('stream://local', 'camera-a', 'camera');
    const replacement = nativeVideoRenderIdentity('stream://local', 'camera-b', 'camera');
    assert.notEqual(first, replacement);
    assert.notEqual(replacement, nativeVideoRenderIdentity('stream://local', 'camera-b', 'screen'));
    assert.notEqual(
      replacement,
      nativeVideoRenderIdentity('stream://local', 'camera-b', 'camera', 'wide:1920x1080'),
    );
  });

  it('waits through transient disconnects and only restarts the same sustained disconnected peer', () => {
    let nextTimerId = 0;
    const timers = new Map<number, () => void>();
    const scheduledDelays: number[] = [];
    const runNextTimer = () => {
      const next = timers.entries().next().value as [number, () => void] | undefined;
      if (!next) return;
      timers.delete(next[0]);
      next[1]();
    };
    const controller = createDisconnectedIceRestartController({
      timer: {
        schedule: (callback, delayMs) => {
          const timerId = ++nextTimerId;
          scheduledDelays.push(delayMs);
          timers.set(timerId, callback);
          return timerId;
        },
        cancel: (handle) => { timers.delete(handle as number); },
      },
    });
    let currentPeer: RecoverableIcePeer | null = null;
    let restartCount = 0;
    let signalCount = 0;
    const peer: RecoverableIcePeer = {
      connectionState: 'disconnected',
      restartIce: () => { restartCount += 1; },
    };
    currentPeer = peer;

    controller.handleConnectionStateChange(peer, () => currentPeer, () => { signalCount += 1; });
    assert.deepEqual(scheduledDelays, [disconnectedIceRestartGraceMs]);
    assert.equal(restartCount, 0);

    peer.connectionState = 'connected';
    controller.handleConnectionStateChange(peer, () => currentPeer, () => { signalCount += 1; });
    assert.equal(timers.size, 0);
    assert.equal(restartCount, 0);

    peer.connectionState = 'disconnected';
    controller.handleConnectionStateChange(peer, () => currentPeer, () => { signalCount += 1; });
    peer.connectionState = 'new';
    controller.handleConnectionStateChange(peer, () => currentPeer, () => { signalCount += 1; });
    assert.equal(timers.size, 0);

    peer.connectionState = 'disconnected';
    controller.handleConnectionStateChange(peer, () => currentPeer, () => { signalCount += 1; });
    controller.cancel();
    assert.equal(timers.size, 0);

    peer.connectionState = 'disconnected';
    controller.handleConnectionStateChange(peer, () => currentPeer, () => { signalCount += 1; });
    currentPeer = { connectionState: 'new', restartIce: () => undefined };
    runNextTimer();
    assert.equal(restartCount, 0);
    assert.equal(signalCount, 0);

    currentPeer = peer;
    controller.handleConnectionStateChange(peer, () => currentPeer, () => { signalCount += 1; });
    runNextTimer();
    assert.equal(restartCount, 1);
    assert.equal(signalCount, 1);
  });

  it('keeps failed ICE recovery immediate and cancels a pending disconnect grace timer', () => {
    let pendingTimer: (() => void) | null = null;
    const controller = createDisconnectedIceRestartController({
      timer: {
        schedule: (callback) => {
          pendingTimer = callback;
          return 1;
        },
        cancel: () => { pendingTimer = null; },
      },
    });
    let restartCount = 0;
    let signalCount = 0;
    const peer: RecoverableIcePeer = {
      connectionState: 'disconnected',
      restartIce: () => { restartCount += 1; },
    };

    controller.handleConnectionStateChange(peer, () => peer, () => { signalCount += 1; });
    assert.ok(pendingTimer);
    peer.connectionState = 'failed';
    controller.handleConnectionStateChange(peer, () => peer, () => { signalCount += 1; });
    assert.equal(pendingTimer, null);
    assert.equal(restartCount, 1);
    assert.equal(signalCount, 1);
  });

  it('debounces remote mute blips, removes a sustained frozen feed, and restores on unmute', () => {
    let nextTimerId = 0;
    const timers = new Map<number, () => void>();
    const scheduledDelays: number[] = [];
    const runNextTimer = () => {
      const next = timers.entries().next().value as [number, () => void] | undefined;
      if (!next) return;
      timers.delete(next[0]);
      next[1]();
    };
    const controller = createRemoteVideoMuteController({
      timer: {
        schedule: (callback, delayMs) => {
          const timerId = ++nextTimerId;
          scheduledDelays.push(delayMs);
          timers.set(timerId, callback);
          return timerId;
        },
        cancel: (handle) => { timers.delete(handle as number); },
      },
    });
    const track: MutableRemoteVideoTrack = { muted: true, readyState: 'live' };
    let current = true;
    let removed = 0;
    let restored = 0;

    controller.handleMute(track, () => current, () => { removed += 1; });
    assert.deepEqual(scheduledDelays, [remoteVideoMuteGraceMs]);
    assert.equal(removed, 0);
    track.muted = false;
    controller.handleUnmute(track, () => current, () => { restored += 1; });
    assert.equal(timers.size, 0);
    assert.equal(removed, 0);
    assert.equal(restored, 1);

    track.muted = true;
    controller.handleMute(track, () => current, () => { removed += 1; });
    runNextTimer();
    assert.equal(removed, 1);
    track.muted = false;
    controller.handleUnmute(track, () => current, () => { restored += 1; });
    assert.equal(restored, 2);

    track.muted = true;
    controller.handleMute(track, () => current, () => { removed += 1; });
    current = false;
    runNextTimer();
    assert.equal(removed, 1);
    track.muted = false;
    controller.handleUnmute(track, () => current, () => { restored += 1; });
    assert.equal(restored, 2);
  });

  it('covers a live remote receiver that stops decoding without a mute event', () => {
    const firstSample = remoteVideoProgressSample(new Map([
      ['inbound', { type: 'inbound-rtp', kind: 'video', framesDecoded: 120, bytesReceived: 80_000 }],
    ]));
    assert.deepEqual(firstSample, { framesDecoded: 120, bytesReceived: 80_000 });
    assert.equal(remoteVideoProgressSample(new Map([
      ['audio', { type: 'inbound-rtp', kind: 'audio', bytesReceived: 2_000 }],
    ])), null);
    if (!firstSample) throw new Error('expected a video progress sample');

    let result = nextRemoteVideoProgressState(undefined, firstSample, true);
    for (let interval = 1; interval <= remoteVideoStallIntervals; interval += 1) {
      result = nextRemoteVideoProgressState(result.state, {
        framesDecoded: 120,
        // Bytes can keep arriving even though the decoder is frozen.
        bytesReceived: 80_000 + interval * 5_000,
      }, true);
    }
    assert.equal(result.state.stalled, true);
    assert.equal(result.becameStalled, true);

    result = nextRemoteVideoProgressState(result.state, {
      framesDecoded: 121,
      bytesReceived: 101_000,
    }, true);
    assert.equal(result.state.stalled, false);
    assert.equal(result.becameHealthy, true);
  });

  it('rebases the frozen-frame watch while the roster says camera off', () => {
    const stalled = {
      framesDecoded: 20,
      bytesReceived: 10_000,
      stagnantIntervals: remoteVideoStallIntervals,
      stalled: true,
    };
    const cameraOff = nextRemoteVideoProgressState(stalled, {
      framesDecoded: 20,
      bytesReceived: 10_000,
    }, false);
    assert.equal(cameraOff.state.stalled, false);
    assert.equal(cameraOff.state.stagnantIntervals, 0);

    const cameraOnBaseline = nextRemoteVideoProgressState(undefined, {
      framesDecoded: 20,
      bytesReceived: 10_000,
    }, true);
    assert.equal(cameraOnBaseline.state.stalled, false);
    assert.equal(cameraOnBaseline.becameStalled, false);
  });

  it('escalates one persistent dead binding only when the rest of the transport is healthy', () => {
    const healthy = {
      framesDecoded: 240,
      bytesReceived: 180_000,
      stagnantIntervals: 0,
      stalled: false,
    };
    const trackRefreshOnly = {
      framesDecoded: 120,
      bytesReceived: 90_000,
      stagnantIntervals: remoteVideoStallIntervals,
      stalled: true,
    };
    const persistentStall = {
      ...trackRefreshOnly,
      stagnantIntervals: remoteVideoIceRestartIntervals,
    };
    const initial = createRemoteVideoRecoveryState();

    assert.equal(
      nextRemoteVideoRecoveryDecision(initial, [healthy, trackRefreshOnly], 12_000).shouldRestartIce,
      false,
    );
    const isolatedDecision = nextRemoteVideoRecoveryDecision(
      initial,
      [healthy, persistentStall],
      24_000,
    );
    assert.equal(isolatedDecision.shouldRestartIce, true);
    assert.deepEqual(isolatedDecision.state, {
      iceRestartCount: 1,
      lastIceRestartAt: 24_000,
    });

    // Two sick feeds indicate room/transport congestion, not two independent
    // dead bindings. They must never produce one restart per feed.
    assert.equal(
      nextRemoteVideoRecoveryDecision(initial, [persistentStall, persistentStall], 24_000)
        .shouldRestartIce,
      false,
    );
    assert.equal(
      nextRemoteVideoRecoveryDecision(initial, [persistentStall], 24_000).shouldRestartIce,
      true,
    );
  });

  it('cools and caps transport-wide stale-video restarts per peer, then resets for a fresh peer', () => {
    const healthy = {
      framesDecoded: 240,
      bytesReceived: 180_000,
      stagnantIntervals: 0,
      stalled: false,
    };
    const persistentStall = {
      framesDecoded: 120,
      bytesReceived: 90_000,
      stagnantIntervals: remoteVideoIceRestartIntervals,
      stalled: true,
    };
    const monitored = [healthy, persistentStall];
    const firstAt = 24_000;
    const first = nextRemoteVideoRecoveryDecision(
      createRemoteVideoRecoveryState(),
      monitored,
      firstAt,
    );
    assert.equal(first.shouldRestartIce, true);

    const cooling = nextRemoteVideoRecoveryDecision(
      first.state,
      monitored,
      firstAt + remoteVideoIceRestartCooldownMs - 1,
    );
    assert.equal(cooling.shouldRestartIce, false);
    assert.equal(cooling.state, first.state);

    const second = nextRemoteVideoRecoveryDecision(
      first.state,
      monitored,
      firstAt + remoteVideoIceRestartCooldownMs,
    );
    assert.equal(second.shouldRestartIce, true);
    assert.equal(second.state.iceRestartCount, remoteVideoMaxIceRestartsPerConnection);

    const exhausted = nextRemoteVideoRecoveryDecision(
      second.state,
      monitored,
      firstAt + remoteVideoIceRestartCooldownMs * 2,
    );
    assert.equal(exhausted.shouldRestartIce, false);
    assert.equal(exhausted.state, second.state);

    const freshPeer = nextRemoteVideoRecoveryDecision(
      createRemoteVideoRecoveryState(),
      monitored,
      firstAt + remoteVideoIceRestartCooldownMs * 2,
    );
    assert.equal(freshPeer.shouldRestartIce, true);
    assert.equal(freshPeer.state.iceRestartCount, 1);
  });

  it('keeps participant names attached to track identity rather than arrival order', () => {
    const index = indexParticipantTrack(new Map(), {
      name: 'Erick',
      trackId: '-:desktop-track:1234',
      sourceTrackId: 'desktop-track',
    });
    assert.equal(participantForTrack('-:desktop-track:1234', index), 'Erick');
    assert.equal(participantForTrack('desktop-track', index), 'Erick');
    assert.equal(participantForTrack('mobile-track', index), undefined);
  });

  it('removes departed participant media and admits a clean later reconnect', () => {
    let index = indexParticipantTrack(new Map(), {
      name: 'AJ',
      trackId: '-:desktop-track:1234',
      sourceTrackId: 'desktop-track',
    });
    index = indexParticipantTrack(index, {
      name: 'Erick',
      trackId: '-:mobile-track:5678',
      sourceTrackId: 'mobile-track',
    });
    const initialFeeds = [
      { trackId: '-:desktop-track:1234', participant: 'AJ' },
      { trackId: '-:mobile-track:5678', participant: 'Erick' },
    ];

    const departed = removeRemoteParticipantMedia(initialFeeds, index, 'aj');
    assert.deepEqual(departed.feeds.map((feed) => feed.participant), ['Erick']);
    assert.equal(participantForTrack('desktop-track', departed.participantsByTrack), undefined);

    const freshIndex = indexParticipantTrack(departed.participantsByTrack, {
      name: 'AJ',
      trackId: '-:desktop-track-new:9012',
      sourceTrackId: 'desktop-track-new',
    });
    const reconnected = reconcileRemoteParticipantRoster([
      ...departed.feeds,
      { trackId: '-:desktop-track-new:9012', participant: 'AJ' },
    ], freshIndex, ['AJ', 'Erick', 'Tom']);
    assert.deepEqual(reconnected.feeds.map((feed) => feed.participant), ['Erick', 'AJ']);
    assert.equal(participantForTrack('desktop-track-new', reconnected.participantsByTrack), 'AJ');
  });

  it('uses roster and offer reconciliation to remove missed or inactive ghost feeds', () => {
    let index = indexParticipantTrack(new Map(), {
      name: 'AJ',
      trackId: '-:desktop-track:1234',
      sourceTrackId: 'desktop-track',
    });
    index = indexParticipantTrack(index, {
      name: 'Erick',
      trackId: '-:mobile-track:5678',
      sourceTrackId: 'mobile-track',
    });
    const feeds = [
      { trackId: '-:desktop-track:1234', participant: 'AJ' },
      { trackId: '-:mobile-track:5678', participant: 'Erick' },
    ];

    const rosterReconciled = reconcileRemoteParticipantRoster(feeds, index, ['Erick', 'Tom']);
    assert.deepEqual(rosterReconciled.feeds.map((feed) => feed.participant), ['Erick']);

    const offerReconciled = reconcileRemoteVideoOffer(feeds, index, ['-:mobile-track:5678']);
    assert.deepEqual(offerReconciled.feeds.map((feed) => feed.participant), ['Erick']);
    assert.equal(participantForTrack('desktop-track', offerReconciled.participantsByTrack), undefined);
  });

  it('releases a departed pin, preserves camera-off focus, and admits a fresh reconnect', () => {
    const key = 'participant:aj';
    assert.equal(pinnedVideoParticipantIsStale(key, [{ key, streamURL: 'fresh-stream' }]), false);
    assert.equal(pinnedVideoParticipantIsStale(key, [{ key }]), false);
    assert.equal(pinnedVideoParticipantIsStale(key, []), true);
    assert.equal(pinnedVideoParticipantIsStale(null, []), false);
  });

  it('reports delayed receive buffering without confusing it with frame order', () => {
    const first = summarizeNativeRoomStats(new Map([
      ['video', { type: 'inbound-rtp', kind: 'video', packetsReceived: 100, packetsLost: 0, framesDecoded: 60, jitterBufferDelay: 30, jitterBufferEmittedCount: 60 }],
    ]), null, 1_000);
    const second = summarizeNativeRoomStats(new Map([
      ['video', { type: 'inbound-rtp', kind: 'video', packetsReceived: 200, packetsLost: 1, framesDecoded: 120, jitterBufferDelay: 90, jitterBufferEmittedCount: 120 }],
      ['pair', { type: 'candidate-pair', nominated: true, state: 'succeeded', currentRoundTripTime: 0.5 }],
    ]), first, 5_000);

    assert.equal(Math.round(second.receivedFramesPerSecond), 15);
    assert.equal(Math.round(second.jitterBufferMs), 1_000);
    assert.equal(second.label, 'Connection weak');
  });

  it('reports audio-only packet loss at low RTT and recovers on the next healthy interval', () => {
    const report = (packetsReceived: number, packetsLost: number) => new Map([
      ['audio', { type: 'inbound-rtp', kind: 'audio', packetsReceived, packetsLost }],
      ['pair', { type: 'candidate-pair', selected: true, currentRoundTripTime: 0.02 }],
    ]);
    const first = summarizeNativeRoomStats(report(100, 0), null, 1_000);
    const degraded = summarizeNativeRoomStats(report(190, 10), first, 5_000);
    assert.equal(degraded.packetLossPercent, 10);
    assert.equal(degraded.label, 'Connection weak');
    const recovering = summarizeNativeRoomStats(report(287, 13), degraded, 9_000);
    assert.equal(recovering.label, 'Catching up');
    const healthy = summarizeNativeRoomStats(report(387, 13), recovering, 13_000);
    assert.equal(healthy.packetLossPercent, 0);
    assert.equal(healthy.label, 'Live');
  });

  it('does not dilute audio loss with healthy high-volume video', () => {
    const report = (audioReceived: number, audioLost: number, videoReceived: number) => new Map([
      ['audio', { type: 'inbound-rtp', kind: 'audio', packetsReceived: audioReceived, packetsLost: audioLost }],
      ['video', { type: 'inbound-rtp', kind: 'video', packetsReceived: videoReceived, packetsLost: 0 }],
    ]);
    const first = summarizeNativeRoomStats(report(100, 0, 1_000), null, 1_000);
    const second = summarizeNativeRoomStats(report(190, 10, 11_000), first, 5_000);
    assert.equal(second.packetLossPercent, 10);
    assert.equal(second.label, 'Connection weak');
  });

  it('reports audio buffering independently of video and does not mark silent intervals weak', () => {
    const report = (delay: number, count: number) => new Map([
      ['audio', { type: 'inbound-rtp', kind: 'audio', packetsReceived: count, packetsLost: 0, jitterBufferDelay: delay, jitterBufferEmittedCount: count }],
    ]);
    const first = summarizeNativeRoomStats(report(0, 100), null, 1_000);
    const buffered = summarizeNativeRoomStats(report(80, 200), first, 5_000);
    assert.equal(buffered.jitterBufferMs, 800);
    assert.equal(buffered.label, 'Connection weak');
    const silent = summarizeNativeRoomStats(report(80, 200), buffered, 9_000);
    assert.equal(silent.jitterBufferMs, 0);
    assert.equal(silent.packetLossPercent, 0);
    assert.equal(silent.label, 'Live');
    const reset = summarizeNativeRoomStats(report(0, 0), silent, 13_000);
    assert.equal(reset.label, 'Live');
  });

  it('uses the transport-selected candidate pair instead of a stale succeeded pair', () => {
    const snapshot = summarizeNativeRoomStats(new Map([
      ['stale-pair', {
        type: 'candidate-pair',
        nominated: true,
        state: 'succeeded',
        currentRoundTripTime: 4.676,
        availableOutgoingBitrate: 90_000,
      }],
      ['selected-pair', {
        type: 'candidate-pair',
        state: 'succeeded',
        currentRoundTripTime: 0.019,
        availableOutgoingBitrate: 2_550_000,
        localCandidateId: 'local',
        remoteCandidateId: 'remote',
      }],
      ['transport', { type: 'transport', selectedCandidatePairId: 'selected-pair' }],
      ['local', { type: 'local-candidate', candidateType: 'host', protocol: 'udp', networkType: 'wifi' }],
      ['remote', { type: 'remote-candidate', candidateType: 'srflx' }],
    ]), null, 1_000);

    assert.equal(snapshot.roundTripTimeMs, 19);
    assert.equal(snapshot.availableOutgoingBitrate, 2_550_000);
    assert.deepEqual(snapshot.candidatePair, {
      protocol: 'udp',
      networkType: 'wifi',
      localCandidateType: 'host',
      remoteCandidateType: 'srflx',
      availableOutgoingBitrate: 2_550_000,
      currentRoundTripTime: 0.019,
    });
  });

  it('prefers a selected fallback over a nominated succeeded fallback', () => {
    const snapshot = summarizeNativeRoomStats(new Map([
      ['nominated', { type: 'candidate-pair', nominated: true, state: 'succeeded', currentRoundTripTime: 0.8 }],
      ['selected', { type: 'candidate-pair', selected: true, state: 'succeeded', currentRoundTripTime: 0.04 }],
    ]), null, 1_000);

    assert.equal(snapshot.roundTripTimeMs, 40);
  });

  it('reports interval outbound bytes and frames with encoder quality fields', () => {
    const first = summarizeNativeRoomStats(new Map([
      ['outbound', {
        type: 'outbound-rtp',
        kind: 'video',
        bytesSent: 1_000,
        framesEncoded: 10,
        framesSent: 10,
      }],
    ]), null, 1_000);
    const second = summarizeNativeRoomStats(new Map([
      ['outbound', {
        type: 'outbound-rtp',
        kind: 'video',
        bytesSent: 7_000,
        framesEncoded: 70,
        framesSent: 68,
        frameWidth: 1280,
        frameHeight: 720,
        framesPerSecond: 30,
        targetBitrate: 1_200_000,
        qualityLimitationReason: 'bandwidth',
      }],
    ]), first, 5_000);

    assert.equal(second.outboundVideoBytesDelta, 6_000);
    assert.equal(second.outboundVideoFramesSentDelta, 58);
    assert.equal(second.outboundVideoFrameWidth, 1280);
    assert.equal(second.outboundVideoFrameHeight, 720);
    assert.equal(second.outboundVideoFramesPerSecond, 30);
    assert.equal(second.outboundVideoTargetBitrate, 1_200_000);
    assert.equal(second.outboundVideoQualityLimitationReason, 'bandwidth');
  });

  it('detects two connected zero-byte intervals regardless of foreground state', () => {
    const connectedWithCameraIntendedOn = true;
    let intervals = nextZeroOutboundVideoIntervalCount(0, connectedWithCameraIntendedOn, false, 0);
    assert.equal(intervals, 0);
    // The same monitoring input stays true while iOS is multitasking; AppState
    // decides whether to defer recovery, not whether outbound stalls count.
    intervals = nextZeroOutboundVideoIntervalCount(intervals, connectedWithCameraIntendedOn, true, 0);
    assert.equal(intervals, 1);
    intervals = nextZeroOutboundVideoIntervalCount(intervals, connectedWithCameraIntendedOn, true, 0);
    assert.equal(intervals, 2);
    assert.equal(nextZeroOutboundVideoIntervalCount(intervals, connectedWithCameraIntendedOn, true, 2_048), 0);
    assert.equal(nextZeroOutboundVideoIntervalCount(intervals, false, true, 0), 0);
  });
});
