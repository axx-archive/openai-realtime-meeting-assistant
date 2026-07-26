export type ParticipantMediaState = {
  micMuted: boolean;
  cameraOff: boolean;
  screenSharing: boolean;
  suspended?: boolean;
  updatedAt?: string;
};

export type ParticipantMediaStates = Record<string, ParticipantMediaState>;
export type ParticipantEndpointMediaStates = Record<string, Record<string, ParticipantMediaState>>;

export function normalizedParticipantName(value: unknown): string {
  return String(value ?? '').trim().toLocaleLowerCase();
}

/** Decode a complete, authoritative participants.mediaStates snapshot. */
export function parseParticipantMediaStates(value: unknown): ParticipantMediaStates {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};

  const states: ParticipantMediaStates = {};
  Object.entries(value).forEach(([rawName, rawState]) => {
    const name = normalizedParticipantName(rawName);
    if (!name || !rawState || typeof rawState !== 'object' || Array.isArray(rawState)) return;
    const state = rawState as Record<string, unknown>;
    const updatedAt = typeof state.updatedAt === 'string' && state.updatedAt.trim()
      ? state.updatedAt
      : undefined;
    states[name] = {
      micMuted: state.micMuted === true,
      cameraOff: state.cameraOff === true,
      screenSharing: state.screenSharing === true,
      ...(state.suspended === true ? { suspended: true } : {}),
      ...(updatedAt ? { updatedAt } : {}),
    };
  });
  return states;
}

/**
 * Valid objects are full authoritative replacements, including an empty map.
 * Missing or malformed fields preserve the last valid state for older servers.
 */
export function participantMediaStatesFromSnapshot(
  value: unknown,
  current: ParticipantMediaStates,
): ParticipantMediaStates {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return current;
  const entries = Object.entries(value);
  const valid = entries.every(([name, state]) => (
    Boolean(normalizedParticipantName(name))
    && Boolean(state)
    && typeof state === 'object'
    && !Array.isArray(state)
  ));
  if (!valid) return current;
  return parseParticipantMediaStates(value);
}

export function parseParticipantEndpointMediaStates(value: unknown): ParticipantEndpointMediaStates {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};

  const states: ParticipantEndpointMediaStates = {};
  Object.entries(value).forEach(([rawName, rawEndpoints]) => {
    const name = normalizedParticipantName(rawName);
    if (!name || !rawEndpoints || typeof rawEndpoints !== 'object' || Array.isArray(rawEndpoints)) return;
    const endpoints: Record<string, ParticipantMediaState> = {};
    Object.entries(rawEndpoints).forEach(([rawEndpointId, rawState]) => {
      const endpointId = rawEndpointId.trim();
      if (!endpointId || !rawState || typeof rawState !== 'object' || Array.isArray(rawState)) return;
      const parsed = parseParticipantMediaStates({ [name]: rawState });
      if (parsed[name]) endpoints[endpointId] = parsed[name];
    });
    states[name] = endpoints;
  });
  return states;
}

export function participantEndpointMediaStatesFromSnapshot(
  value: unknown,
  current: ParticipantEndpointMediaStates,
): ParticipantEndpointMediaStates {
  if (!participantEndpointMediaStatesSnapshotIsAuthoritative(value)) return current;
  return parseParticipantEndpointMediaStates(value);
}

/**
 * Distinguish an explicit, complete endpoint snapshot from an older server (or
 * malformed frame) that must not be allowed to retire otherwise healthy media.
 */
export function participantEndpointMediaStatesSnapshotIsAuthoritative(
  value: unknown,
): value is Record<string, Record<string, unknown>> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  return Object.entries(value).every(([name, endpoints]) => (
    Boolean(normalizedParticipantName(name))
    && Boolean(endpoints)
    && typeof endpoints === 'object'
    && !Array.isArray(endpoints)
    && Object.entries(endpoints).every(([, state]) => (
      Boolean(state)
      && typeof state === 'object'
      && !Array.isArray(state)
    ))
  ));
}

export function participantMediaStateFor(
  states: Readonly<ParticipantMediaStates>,
  participant: string | undefined,
): ParticipantMediaState | undefined {
  const name = normalizedParticipantName(participant);
  return name ? states[name] : undefined;
}

export function participantMediaStateForEndpoint(
  states: Readonly<ParticipantMediaStates>,
  endpointStates: Readonly<ParticipantEndpointMediaStates>,
  participant: string | undefined,
  endpointId: string | undefined,
): ParticipantMediaState | undefined {
  const name = normalizedParticipantName(participant);
  const endpoint = String(endpointId ?? '').trim();
  return (name && endpoint ? endpointStates[name]?.[endpoint] : undefined)
    ?? participantMediaStateFor(states, participant);
}

/** A live screen share remains frame-capable even when the camera is off. */
export function participantVideoIsOff(
  states: Readonly<ParticipantMediaStates>,
  participant: string | undefined,
  endpointStates: Readonly<ParticipantEndpointMediaStates> = {},
  endpointId?: string,
): boolean {
  const state = participantMediaStateForEndpoint(states, endpointStates, participant, endpointId);
  return Boolean(state && (state.cameraOff || state.suspended) && !state.screenSharing);
}
