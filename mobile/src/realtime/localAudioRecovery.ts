export type RecoverableLocalTrack = {
  enabled: boolean;
  kind: string;
  readyState?: string;
  stop: () => void;
  release?: () => void;
};

export type RecoverableLocalStream<TTrack extends RecoverableLocalTrack = RecoverableLocalTrack> = {
  addTrack: (track: TTrack) => void;
  getAudioTracks: () => TTrack[];
  getVideoTracks: () => TTrack[];
  removeTrack: (track: TTrack) => void;
};

export type RecoverableCaptureStream<TTrack extends RecoverableLocalTrack = RecoverableLocalTrack> = RecoverableLocalStream<TTrack> & {
  getTracks: () => TTrack[];
  release: (releaseTracks?: boolean) => void;
};

export type RecoverableLocalSender<TTrack extends RecoverableLocalTrack = RecoverableLocalTrack> = {
  readonly track: TTrack | null;
  replaceTrack: (track: TTrack | null) => Promise<void>;
};

export type SerializedLocalMediaRecovery = {
  begin: () => number | null;
  isActive: (attempt: number) => boolean;
  isRunning: () => boolean;
  retire: () => void;
  settle: (attempt: number, retryLatestIntent: boolean) => 'stale' | 'settled' | 'retry';
};

export type LocalAudioPublicationCommit = () => Promise<boolean>;

export function localAudioPublicationPendingState(requested: boolean): {
  microphoneStarting: boolean;
  muted: true;
} {
  return { microphoneStarting: requested, muted: true };
}

type LocalAudioIntentTrack = {
  enabled: boolean;
  kind: string;
  readyState?: string;
};

/**
 * Apply the latest microphone intent to every known local/sender track.
 * The sender can briefly own a recovery track before that track is added to
 * the logical local stream, so callers must include both sources.
 */
export function setLocalAudioTracksEnabled<TTrack extends LocalAudioIntentTrack>(
  tracks: ReadonlyArray<TTrack | null | undefined>,
  enabled: boolean,
): void {
  const uniqueTracks = new Set(tracks.filter((track): track is TTrack => Boolean(track)));
  uniqueTracks.forEach((track) => {
    if (track.kind !== 'audio' || track.readyState === 'ended') return;
    track.enabled = enabled;
  });
}

/**
 * Serializes sender mutations while still preserving the latest on/off intent.
 * A same-peer cancel does not retire the active operation: this prevents an old
 * rollback from detaching a newer track. If the user turns the device back on
 * while rollback settles, `settle(..., true)` explicitly queues one fresh run.
 */
export function createSerializedLocalMediaRecovery(): SerializedLocalMediaRecovery {
  let sequence = 0;
  let activeAttempt: number | null = null;
  return {
    begin() {
      if (activeAttempt !== null) return null;
      activeAttempt = ++sequence;
      return activeAttempt;
    },
    isActive(attempt) {
      return activeAttempt === attempt;
    },
    isRunning() {
      return activeAttempt !== null;
    },
    retire() {
      sequence += 1;
      activeAttempt = null;
    },
    settle(attempt, retryLatestIntent) {
      if (activeAttempt !== attempt) return 'stale';
      activeAttempt = null;
      return retryLatestIntent ? 'retry' : 'settled';
    },
  };
}

function releaseCapture<TTrack extends RecoverableLocalTrack>(capture: RecoverableCaptureStream<TTrack>): void {
  capture.getTracks().forEach((track) => track.stop());
  capture.release();
}

function senderTrack<TTrack extends RecoverableLocalTrack>(
  sender: RecoverableLocalSender<TTrack>,
): TTrack | null {
  try {
    return sender.track;
  } catch {
    return null;
  }
}

function audioTracks<TTrack extends RecoverableLocalTrack>(
  getTracks: () => TTrack[],
): TTrack[] {
  try {
    return getTracks().filter((track) => track.kind === 'audio');
  } catch {
    return [];
  }
}

function disableStopAndReleaseAudioTrack<TTrack extends RecoverableLocalTrack>(track: TTrack): void {
  if (track.kind !== 'audio') return;
  try {
    track.enabled = false;
  } catch {
    // Continue through stop/release: privacy cleanup must be best-effort even
    // when one native bridge mutation throws.
  }
  try {
    track.stop();
  } catch {
    // release can still tear down a capture source after stop fails.
  }
  try {
    track.release?.();
  } catch {
    // The track is already disabled/stopped as far as the JS object permits.
  }
}

/**
 * Fail closed before awaiting sender detachment. Every audio track that can be
 * reached through capture, the logical local stream, or the sender is disabled
 * and stopped synchronously. A swallowed/rejected native replaceTrack(null)
 * can therefore leave only an inert track behind.
 */
export async function failClosedLocalAudioTracks<TTrack extends RecoverableLocalTrack>(options: {
  candidateTracks?: ReadonlyArray<TTrack | null | undefined>;
  capture?: RecoverableCaptureStream<TTrack> | null;
  local: RecoverableLocalStream<TTrack>;
  sender: RecoverableLocalSender<TTrack>;
}): Promise<void> {
  const { candidateTracks = [], capture, local, sender } = options;
  const knownTracks = new Set<TTrack>();
  const remember = (track: TTrack | null | undefined) => {
    if (track?.kind === 'audio') knownTracks.add(track);
  };
  candidateTracks.forEach(remember);
  audioTracks(() => local.getAudioTracks()).forEach(remember);
  if (capture) audioTracks(() => capture.getAudioTracks()).forEach(remember);
  remember(senderTrack(sender));

  // No await may move above this shutdown pass. It is the hard privacy fence
  // for cleanup and rollback failures.
  knownTracks.forEach(disableStopAndReleaseAudioTrack);
  knownTracks.forEach((track) => {
    try {
      local.removeTrack(track);
    } catch {
      // A stopped track is safe even if a native stream refuses removal.
    }
    if (capture) {
      try {
        capture.removeTrack(track);
      } catch {
        // release below remains a second cleanup attempt.
      }
    }
  });
  if (capture) {
    try {
      capture.release();
    } catch {
      // All discoverable audio tracks were already disabled and stopped.
    }
  }

  // react-native-webrtc can reject or silently ignore replaceTrack. Retry once,
  // and re-silence whatever its authoritative sender getter still exposes.
  for (let detachAttempt = 0; detachAttempt < 2; detachAttempt += 1) {
    const attachedTrack = senderTrack(sender);
    if (!attachedTrack) break;
    disableStopAndReleaseAudioTrack(attachedTrack);
    try {
      await sender.replaceTrack(null);
    } catch {
      // The next iteration verifies the authoritative sender getter.
    }
  }
  const residualTrack = senderTrack(sender);
  if (residualTrack) disableStopAndReleaseAudioTrack(residualTrack);
}

/**
 * Publish a prepared microphone only after both the local UI commit and the
 * participant-state signal have completed. The track remains disabled and is
 * not attached to the sender while that external publication barrier waits.
 */
export async function attachLocalAudioTrackAfterPublicationCommit<
  TTrack extends RecoverableLocalTrack,
>(options: {
  commitPublication: LocalAudioPublicationCommit;
  isCurrent: () => boolean;
  local: RecoverableLocalStream<TTrack>;
  sender: RecoverableLocalSender<TTrack>;
  track: TTrack;
}): Promise<'installed' | 'cancelled'> {
  const { commitPublication, isCurrent, local, sender, track } = options;
  const publicationIsCurrent = () => isCurrent() && track.readyState !== 'ended';
  try {
    setLocalAudioTracksEnabled([
      ...audioTracks(() => local.getAudioTracks()),
      senderTrack(sender),
      track,
    ], false);
    if (!publicationIsCurrent()) {
      await failClosedLocalAudioTracks({ candidateTracks: [track], local, sender });
      return 'cancelled';
    }

    const publicationCommitted = await commitPublication();
    if (!publicationCommitted || !publicationIsCurrent()) {
      await failClosedLocalAudioTracks({ candidateTracks: [track], local, sender });
      return 'cancelled';
    }

    // Reassert silence immediately before the native sender mutation in case a
    // platform callback changed the track while React was committing the UI.
    setLocalAudioTracksEnabled([
      ...audioTracks(() => local.getAudioTracks()),
      senderTrack(sender),
      track,
    ], false);
    if (senderTrack(sender) !== track) await sender.replaceTrack(track);
    if (senderTrack(sender) !== track) {
      throw new Error('The microphone track could not be attached.');
    }
    if (!publicationIsCurrent()) {
      await failClosedLocalAudioTracks({ candidateTracks: [track], local, sender });
      return 'cancelled';
    }

    // The UI is committed unmuted and its participant-state frame was accepted.
    // No await may appear between the final currentness check and this enable.
    track.enabled = true;
    return 'installed';
  } catch (error) {
    await failClosedLocalAudioTracks({ candidateTracks: [track], local, sender });
    throw error;
  }
}

async function installRecoveredLocalVideoTrackInternal<TTrack extends RecoverableLocalTrack>(options: {
  capture: RecoverableCaptureStream<TTrack>;
  isCurrent: () => boolean;
  local: RecoverableLocalStream<TTrack>;
  sender: RecoverableLocalSender<TTrack>;
}): Promise<'installed' | 'cancelled'> {
  const { capture, isCurrent, local, sender } = options;
  const captureTracks = capture.getVideoTracks();
  const freshTrack = captureTracks[0];
  if (!freshTrack) {
    releaseCapture(capture);
    throw new Error('The camera did not return a video track.');
  }
  const previousSenderTrack = sender.track;
  const rollbackSender = async () => {
    if (sender.track !== freshTrack) return;
    const rollbackTrack = previousSenderTrack?.readyState === 'live' ? previousSenderTrack : null;
    await sender.replaceTrack(rollbackTrack);
    if (sender.track !== rollbackTrack) {
      throw new Error('The previous video track could not be restored.');
    }
  };

  if (!isCurrent()) {
    releaseCapture(capture);
    return 'cancelled';
  }

  try {
    freshTrack.enabled = true;
    await sender.replaceTrack(freshTrack);
    // react-native-webrtc's replaceTrack resolves even when its native call is
    // rejected. The sender getter is therefore the authoritative success bit.
    if (sender.track !== freshTrack) {
      throw new Error('The camera track could not be attached.');
    }
    if (!isCurrent()) {
      await rollbackSender();
      releaseCapture(capture);
      return 'cancelled';
    }

    const oldTracks = local.getVideoTracks()
      .filter((track) => track !== freshTrack);
    oldTracks.forEach((track) => local.removeTrack(track));
    local.addTrack(freshTrack);
    capture.removeTrack(freshTrack);
    capture.release(false);
    oldTracks.forEach((track) => {
      track.stop();
      track.release?.();
    });
    return 'installed';
  } catch (error) {
    await rollbackSender().catch(() => undefined);
    releaseCapture(capture);
    throw error;
  }
}

/**
 * Installs a freshly captured microphone track into an already-negotiated
 * audio transceiver. The currentness predicate covers both peer generation
 * and the user's latest mute intent, so a permission result that arrives
 * after mute/leave/reconnect is detached and released instead of publishing.
 */
export function installRecoveredLocalAudioTrack<TTrack extends RecoverableLocalTrack>(options: {
  capture: RecoverableCaptureStream<TTrack>;
  commitPublication: LocalAudioPublicationCommit;
  isCurrent: () => boolean;
  local: RecoverableLocalStream<TTrack>;
  sender: RecoverableLocalSender<TTrack>;
}): Promise<'installed' | 'cancelled'> {
  const { capture, commitPublication, isCurrent, local, sender } = options;
  let freshTrack: TTrack | undefined;
  let recoveryTracks: TTrack[] = [];

  return (async () => {
    try {
      freshTrack = capture.getAudioTracks()[0];
      if (!freshTrack) {
        releaseCapture(capture);
        throw new Error('The microphone did not return an audio track.');
      }
      freshTrack.enabled = false;
      if (!isCurrent()) {
        await failClosedLocalAudioTracks({ candidateTracks: [freshTrack], capture, local, sender });
        return 'cancelled';
      }

      const oldTracks = local.getAudioTracks().filter((track) => track !== freshTrack);
      recoveryTracks = [freshTrack, ...oldTracks];
      setLocalAudioTracksEnabled([...oldTracks, senderTrack(sender), freshTrack], false);
      oldTracks.forEach((track) => local.removeTrack(track));
      local.addTrack(freshTrack);
      capture.removeTrack(freshTrack);
      capture.release(false);
      oldTracks.forEach((track) => {
        track.stop();
        track.release?.();
      });

      return await attachLocalAudioTrackAfterPublicationCommit({
        commitPublication,
        isCurrent,
        local,
        sender,
        track: freshTrack,
      });
    } catch (error) {
      await failClosedLocalAudioTracks({
        candidateTracks: recoveryTracks.length ? recoveryTracks : [freshTrack],
        capture,
        local,
        sender,
      });
      throw error;
    }
  })();
}

/** Same lifecycle guarantees as microphone recovery, for an off-at-join camera. */
export function installRecoveredLocalVideoTrack<TTrack extends RecoverableLocalTrack>(options: {
  capture: RecoverableCaptureStream<TTrack>;
  isCurrent: () => boolean;
  local: RecoverableLocalStream<TTrack>;
  sender: RecoverableLocalSender<TTrack>;
}): Promise<'installed' | 'cancelled'> {
  return installRecoveredLocalVideoTrackInternal(options);
}
