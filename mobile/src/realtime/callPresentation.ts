import {
  normalizedParticipantName,
  participantMediaStateFor,
  participantMediaStateForEndpoint,
  participantVideoIsOff,
  type ParticipantEndpointMediaStates,
  type ParticipantMediaStates,
} from './participantMedia';

export type PinnableVideoParticipant = {
  key: string;
  streamURL?: string;
};

export type RemoteVideoPresentationFeed = {
  trackId: string;
  participant?: string;
  endpointId?: string;
  streamURL: string;
};

export type PresentedVideoParticipant = PinnableVideoParticipant & {
  name: string;
  endpointId?: string;
  active: boolean;
  micMuted: boolean;
  screenSharing: boolean;
  videoOff: boolean;
};

export function participantVideoAccessibilityStatus(
  participant: Pick<PresentedVideoParticipant, 'screenSharing' | 'streamURL' | 'videoOff'>,
): 'screen sharing' | 'video off' | 'video on' | 'video unavailable' {
  if (participant.screenSharing && participant.streamURL) return 'screen sharing';
  if (participant.videoOff) return 'video off';
  return participant.streamURL ? 'video on' : 'video unavailable';
}

/** A pin survives camera-off and endpoint handoff, but not a departed person. */
export function pinnedVideoParticipantIsStale<T extends PinnableVideoParticipant>(
  pinnedParticipantKey: string | null,
  participants: readonly T[],
): boolean {
  if (!pinnedParticipantKey) return false;
  return !participants.some((participant) => participant.key === pinnedParticipantKey);
}

/**
 * Manual intent wins; otherwise presentation outranks speaker activity.
 * A receiver-stalled feed has no stream while `videoOff` remains false, so it
 * must not displace healthy video merely because it was the last active speaker.
 */
export function focusedVideoParticipant(
  participants: readonly PresentedVideoParticipant[],
  pinnedParticipantKey: string | null,
): PresentedVideoParticipant | undefined {
  return participants.find((participant) => participant.key === pinnedParticipantKey)
    ?? participants.find((participant) => participant.screenSharing && participant.streamURL)
    ?? participants.find((participant) => participant.active && (participant.streamURL || participant.videoOff))
    ?? participants.find((participant) => participant.streamURL)
    ?? participants[0];
}

/**
 * Track identity is endpoint-scoped. Unlike a name/count-derived key, it does
 * not change when another device using the same display name joins or leaves.
 */
export function remoteVideoPresentationKey(
  trackId: string,
  participant?: string,
  endpointId?: string,
): string {
  const endpoint = String(endpointId ?? '').trim();
  if (endpoint) return `endpoint:${normalizedParticipantName(participant)}:${endpoint}`;
  const value = String(trackId ?? '').trim();
  const segments = value.split(':');
  const forwardedSourceTrackId = segments.length >= 3 && /^\d+$/.test(segments.at(-1) ?? '')
    ? segments.at(-2)?.trim()
    : '';
  return `feed:${forwardedSourceTrackId || value}`;
}

export function presentRemoteVideoParticipants({
  activeSpeaker,
  endpointMediaStates = {},
  feeds,
  localNames,
  mediaStates,
  roster,
}: {
  activeSpeaker?: string;
  endpointMediaStates?: Readonly<ParticipantEndpointMediaStates>;
  feeds: readonly RemoteVideoPresentationFeed[];
  localNames: readonly string[];
  mediaStates: Readonly<ParticipantMediaStates>;
  roster: readonly string[];
}): PresentedVideoParticipant[] {
  const active = normalizedParticipantName(activeSpeaker);
  const local = new Set(localNames.map(normalizedParticipantName).filter(Boolean));
  const rosterNames = new Set<string>();
  const remoteRoster: string[] = [];
  roster.forEach((rawName) => {
    const name = String(rawName ?? '').trim();
    const normalized = normalizedParticipantName(name);
    if (!normalized || local.has(normalized) || rosterNames.has(normalized)) return;
    rosterNames.add(normalized);
    remoteRoster.push(name);
  });

  // Reserve signaled names first so an unlabeled feed cannot claim a named
  // participant's placeholder before participant_track metadata arrives.
  const claimedRosterNames = new Set(
    feeds
      .map((feed) => normalizedParticipantName(feed.participant))
      .filter((name) => name && name !== 'room member' && rosterNames.has(name)),
  );
  const feedParticipants = feeds.map((feed, index): PresentedVideoParticipant => {
    const providedName = String(feed.participant ?? '').trim();
    const providedNormalized = normalizedParticipantName(providedName);
    const providedIsGeneric = !providedNormalized || providedNormalized === 'room member';
    const name = providedIsGeneric
      ? remoteRoster.find((candidate) => !claimedRosterNames.has(normalizedParticipantName(candidate)))
        ?? `Participant ${index + 1}`
      : providedName;
    const normalized = normalizedParticipantName(name);
    if (rosterNames.has(normalized)) claimedRosterNames.add(normalized);
    const mediaState = participantMediaStateForEndpoint(
      mediaStates,
      endpointMediaStates,
      name,
      feed.endpointId,
    );
    const videoOff = participantVideoIsOff(mediaStates, name, endpointMediaStates, feed.endpointId);
    return {
      key: remoteVideoPresentationKey(feed.trackId, name, feed.endpointId),
      name,
      endpointId: feed.endpointId,
      streamURL: videoOff ? undefined : feed.streamURL,
      active: Boolean(active && normalized === active),
      micMuted: mediaState?.micMuted ?? false,
      screenSharing: mediaState?.screenSharing ?? false,
      videoOff,
    };
  });

  const placeholders = remoteRoster
    .filter((name) => !claimedRosterNames.has(normalizedParticipantName(name)))
    .map((name): PresentedVideoParticipant => {
      const mediaState = participantMediaStateFor(mediaStates, name);
      return {
        key: `participant:${normalizedParticipantName(name)}`,
        name,
        active: Boolean(active && normalizedParticipantName(name) === active),
        micMuted: mediaState?.micMuted ?? false,
        screenSharing: mediaState?.screenSharing ?? false,
        videoOff: participantVideoIsOff(mediaStates, name, endpointMediaStates),
      };
    });
  return [...feedParticipants, ...placeholders];
}

/**
 * Expand the video-stage projection into an endpoint-complete device roster for
 * the People sheet. Camera-off endpoints do not publish a video track, so they
 * must be materialized from the authoritative endpoint media snapshot without
 * becoming blank tiles on the call stage.
 */
export function presentRemoteParticipantDevices({
  endpointMediaStates,
  localNames,
  participants,
}: {
  endpointMediaStates: Readonly<ParticipantEndpointMediaStates>;
  localNames: readonly string[];
  participants: readonly PresentedVideoParticipant[];
}): PresentedVideoParticipant[] {
  const local = new Set(localNames.map(normalizedParticipantName).filter(Boolean));
  const names = new Map<string, string>();
  const activeNames = new Set<string>();
  participants.forEach((participant) => {
    const normalized = normalizedParticipantName(participant.name);
    if (!normalized) return;
    if (!names.has(normalized)) names.set(normalized, participant.name);
    if (participant.active) activeNames.add(normalized);
  });

  const coveredEndpoints = new Set<string>();
  const devices: PresentedVideoParticipant[] = [];
  participants.forEach((participant) => {
    const normalized = normalizedParticipantName(participant.name);
    const endpoints = normalized ? endpointMediaStates[normalized] : undefined;
    if (local.has(normalized) || !endpoints || Object.keys(endpoints).length === 0) {
      devices.push(participant);
      return;
    }

    const endpoint = String(participant.endpointId ?? '').trim();
    const endpointState = endpoint ? endpoints[endpoint] : undefined;
    if (!endpointState) {
      // An endpointless roster placeholder is superseded by the authoritative
      // device rows below. Preserve a real feed whose metadata simply has not
      // arrived yet so the sheet never hides known live media.
      if (participant.streamURL || endpoint) devices.push(participant);
      return;
    }

    const endpointKey = `${normalized}\u0000${endpoint}`;
    if (coveredEndpoints.has(endpointKey)) return;
    coveredEndpoints.add(endpointKey);
    devices.push({
      ...participant,
      key: remoteVideoPresentationKey(participant.key, participant.name, endpoint),
      endpointId: endpoint,
      active: activeNames.has(normalized),
      micMuted: endpointState.micMuted,
      screenSharing: endpointState.screenSharing,
      videoOff: Boolean((endpointState.cameraOff || endpointState.suspended) && !endpointState.screenSharing),
    });
  });

  names.forEach((name, normalized) => {
    if (local.has(normalized)) return;
    const endpoints = endpointMediaStates[normalized];
    if (!endpoints) return;
    Object.entries(endpoints).forEach(([endpointId, endpointState]) => {
      const endpoint = endpointId.trim();
      const endpointKey = `${normalized}\u0000${endpoint}`;
      if (!endpoint || coveredEndpoints.has(endpointKey)) return;
      coveredEndpoints.add(endpointKey);
      devices.push({
        key: remoteVideoPresentationKey('', name, endpoint),
        name,
        endpointId: endpoint,
        active: activeNames.has(normalized),
        micMuted: endpointState.micMuted,
        screenSharing: endpointState.screenSharing,
        videoOff: Boolean((endpointState.cameraOff || endpointState.suspended) && !endpointState.screenSharing),
      });
    });
  });

  return devices;
}
