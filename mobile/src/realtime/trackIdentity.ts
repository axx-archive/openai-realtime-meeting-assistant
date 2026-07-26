export type ParticipantTrackMetadata = {
  name?: string;
  kind?: string;
  endpointId?: string;
  trackId?: string;
  sourceTrackId?: string;
};

export type IdentifiedRemoteVideoFeed = {
  trackId: string;
  participant?: string;
  endpointId?: string;
};

type EndpointMediaIndex = Readonly<Record<string, Readonly<Record<string, unknown>>>>;

function normalizedParticipantName(value: unknown): string {
  return String(value ?? '').trim().toLowerCase();
}

function remoteTrackIdentityKeys(trackId: string): string[] {
  const value = String(trackId ?? '').trim();
  if (!value) return [];
  const keys = new Set([value]);
  value.split(':').forEach((segment) => {
    const normalized = segment.trim();
    if (normalized && normalized !== '-' && !/^\d+$/.test(normalized)) keys.add(normalized);
  });
  return [...keys];
}

/** Keep participant identity attached to media instead of relying on arrival order. */
export function indexParticipantTrack(
  current: ReadonlyMap<string, string>,
  metadata: ParticipantTrackMetadata,
): Map<string, string> {
  const next = new Map(current);
  const name = String(metadata.name ?? '').trim();
  if (!name) return next;
  for (const key of [metadata.trackId, metadata.sourceTrackId]) {
    const normalized = String(key ?? '').trim();
    if (normalized) next.set(normalized, name);
  }
  return next;
}

/** Attach the publishing endpoint to both forwarded and source track ids. */
export function indexParticipantTrackEndpoint(
  current: ReadonlyMap<string, string>,
  metadata: ParticipantTrackMetadata,
): Map<string, string> {
  const next = new Map(current);
  const endpointId = String(metadata.endpointId ?? '').trim();
  if (!endpointId) return next;
  for (const key of [metadata.trackId, metadata.sourceTrackId]) {
    const normalized = String(key ?? '').trim();
    if (normalized) next.set(normalized, endpointId);
  }
  return next;
}

export function participantForTrack(
  trackId: string,
  participantsByTrack: ReadonlyMap<string, string>,
): string | undefined {
  const exact = participantsByTrack.get(trackId);
  if (exact) return exact;

  // Forwarded SFU ids are stream:sourceTrack:ssrc. Native WebRTC builds may
  // surface either that full id or just the publisher's source-track UUID.
  const segments = String(trackId).split(':').filter(Boolean);
  for (const segment of segments) {
    const match = participantsByTrack.get(segment);
    if (match) return match;
  }
  return undefined;
}

export function endpointForTrack(
  trackId: string,
  endpointsByTrack: ReadonlyMap<string, string>,
): string | undefined {
  return participantForTrack(trackId, endpointsByTrack);
}

function participantForFeed(
  feed: IdentifiedRemoteVideoFeed,
  participantsByTrack: ReadonlyMap<string, string>,
): string | undefined {
  return String(feed.participant ?? '').trim() || participantForTrack(feed.trackId, participantsByTrack);
}

/** Remove every media identity and visible feed owned by a departed person. */
export function removeRemoteParticipantMedia<T extends IdentifiedRemoteVideoFeed>(
  feeds: readonly T[],
  participantsByTrack: ReadonlyMap<string, string>,
  participant: string,
): { feeds: T[]; participantsByTrack: Map<string, string> } {
  const departed = normalizedParticipantName(participant);
  if (!departed) return { feeds: [...feeds], participantsByTrack: new Map(participantsByTrack) };
  return {
    feeds: feeds.filter((feed) => normalizedParticipantName(participantForFeed(feed, participantsByTrack)) !== departed),
    participantsByTrack: new Map(
      [...participantsByTrack].filter(([, name]) => normalizedParticipantName(name) !== departed),
    ),
  };
}

/**
 * Treat an authoritative roster snapshot as a self-heal boundary. Named media
 * outside the roster is stale; unlabeled tracks stay until metadata arrives.
 */
export function reconcileRemoteParticipantRoster<T extends IdentifiedRemoteVideoFeed>(
  feeds: readonly T[],
  participantsByTrack: ReadonlyMap<string, string>,
  participants: readonly string[],
): { feeds: T[]; participantsByTrack: Map<string, string> } {
  const present = new Set(participants.map(normalizedParticipantName).filter(Boolean));
  return {
    feeds: feeds.filter((feed) => {
      const participant = normalizedParticipantName(participantForFeed(feed, participantsByTrack));
      return !participant || present.has(participant);
    }),
    participantsByTrack: new Map(
      [...participantsByTrack].filter(([, name]) => present.has(normalizedParticipantName(name))),
    ),
  };
}

/**
 * Retire media for a device removed by an authoritative endpoint snapshot even
 * when another device keeps the participant's name in the room roster. Null
 * means the endpoint snapshot was missing/malformed, so no endpoint media is
 * eligible for removal. Unlabeled feeds likewise wait for track metadata.
 */
export function reconcileRemoteParticipantEndpoints<T extends IdentifiedRemoteVideoFeed>(
  feeds: readonly T[],
  participantsByTrack: ReadonlyMap<string, string>,
  endpointsByTrack: ReadonlyMap<string, string>,
  endpointMediaStates: EndpointMediaIndex | null,
): {
  feeds: T[];
  participantsByTrack: Map<string, string>;
  endpointsByTrack: Map<string, string>;
} {
  if (endpointMediaStates === null) {
    return {
      feeds: [...feeds],
      participantsByTrack: new Map(participantsByTrack),
      endpointsByTrack: new Map(endpointsByTrack),
    };
  }

  const retainedFeeds = feeds.filter((feed) => {
    const participant = normalizedParticipantName(participantForFeed(feed, participantsByTrack));
    const endpointId = String(feed.endpointId ?? endpointForTrack(feed.trackId, endpointsByTrack) ?? '').trim();
    if (!participant || !endpointId) return true;
    return Object.prototype.hasOwnProperty.call(endpointMediaStates[participant] ?? {}, endpointId);
  });
  return {
    feeds: retainedFeeds,
    participantsByTrack: retainRemoteTrackIndexForFeeds(participantsByTrack, retainedFeeds),
    endpointsByTrack: retainRemoteTrackIndexForFeeds(endpointsByTrack, retainedFeeds),
  };
}

/**
 * Reconcile native tiles with the server's current sendonly video m-lines.
 * react-native-webrtc can leave a receiver track `live` after its m-line turns
 * inactive, so track.onended alone is not a reliable departure signal.
 */
export function reconcileRemoteVideoOffer<T extends IdentifiedRemoteVideoFeed>(
  feeds: readonly T[],
  participantsByTrack: ReadonlyMap<string, string>,
  offeredTrackIds: readonly string[],
): { feeds: T[]; participantsByTrack: Map<string, string> } {
  const activeKeys = new Set(offeredTrackIds.flatMap(remoteTrackIdentityKeys));
  const retainedFeeds = feeds.filter((feed) => (
    remoteTrackIdentityKeys(feed.trackId).some((key) => activeKeys.has(key))
  ));
  const retainedFeedKeys = new Set(retainedFeeds.flatMap((feed) => remoteTrackIdentityKeys(feed.trackId)));
  return {
    feeds: retainedFeeds,
    participantsByTrack: new Map(
      [...participantsByTrack].filter(([trackId]) => retainedFeedKeys.has(trackId) || activeKeys.has(trackId)),
    ),
  };
}

/** Drop identity keys for a receiver that actually ended. */
export function removeRemoteTrackIdentity(
  participantsByTrack: ReadonlyMap<string, string>,
  trackId: string,
): Map<string, string> {
  const removedKeys = new Set(remoteTrackIdentityKeys(trackId));
  return new Map([...participantsByTrack].filter(([key]) => !removedKeys.has(key)));
}

/** Retain arbitrary track metadata only for receiver feeds that remain live. */
export function retainRemoteTrackIndexForFeeds<T extends IdentifiedRemoteVideoFeed>(
  index: ReadonlyMap<string, string>,
  feeds: readonly T[],
): Map<string, string> {
  const retainedKeys = new Set(feeds.flatMap((feed) => remoteTrackIdentityKeys(feed.trackId)));
  return new Map([...index].filter(([key]) => retainedKeys.has(key)));
}
