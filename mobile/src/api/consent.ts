import {
  consentDispositions,
  consentLanes,
  consentScopes,
  type ConsentDecisionRequest,
  type ConsentDecisionResponse,
  type ConsentDisposition,
  type ConsentLane,
  type ConsentLaneStatus,
  type ConsentScope,
  type ConsentStatus,
} from './types';

type UnknownRecord = Record<string, unknown>;

const scopeSet = new Set<string>(consentScopes);
const dispositionSet = new Set<string>(consentDispositions);
const laneSet = new Set<string>(consentLanes);

export class ConsentContractError extends Error {
  constructor(field: string) {
    super(`The server returned an invalid consent response (${field}).`);
    this.name = 'ConsentContractError';
  }
}

function record(value: unknown, field: string): UnknownRecord {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new ConsentContractError(field);
  }
  return value as UnknownRecord;
}

function nonEmptyString(value: unknown, field: string): string {
  if (typeof value !== 'string' || !value.trim()) throw new ConsentContractError(field);
  return value;
}

function boolean(value: unknown, field: string): boolean {
  if (typeof value !== 'boolean') throw new ConsentContractError(field);
  return value;
}

export function isConsentScope(value: unknown): value is ConsentScope {
  return typeof value === 'string' && scopeSet.has(value);
}

export function isConsentDisposition(value: unknown): value is ConsentDisposition {
  return typeof value === 'string' && dispositionSet.has(value);
}

function isConsentLane(value: unknown): value is ConsentLane {
  return typeof value === 'string' && laneSet.has(value);
}

function parseScopeList(value: unknown, field: string): ConsentScope[] | undefined {
  if (value === undefined) return undefined;
  if (!Array.isArray(value) || value.some((scope) => !isConsentScope(scope))) {
    throw new ConsentContractError(field);
  }
  return [...value] as ConsentScope[];
}

function parseRecordIds(value: unknown, field: string): Partial<Record<ConsentScope, string>> | undefined {
  if (value === undefined) return undefined;
  const raw = record(value, field);
  const parsed: Partial<Record<ConsentScope, string>> = {};
  for (const [scope, id] of Object.entries(raw)) {
    if (!isConsentScope(scope)) throw new ConsentContractError(`${field}.${scope}`);
    parsed[scope] = nonEmptyString(id, `${field}.${scope}`);
  }
  return parsed;
}

function parseLaneStatus(value: unknown, field: string): ConsentLaneStatus {
  const raw = record(value, field);
  const missingScopes = parseScopeList(raw.missingScopes, `${field}.missingScopes`);
  const recordIds = parseRecordIds(raw.recordIds, `${field}.recordIds`);
  return {
    allowed: boolean(raw.allowed, `${field}.allowed`),
    ...(missingScopes ? { missingScopes } : {}),
    ...(recordIds ? { recordIds } : {}),
  };
}

/** Fail closed on malformed or future authority values instead of inventing consent. */
export function parseConsentStatus(value: unknown): ConsentStatus {
  const raw = record(value, 'consent');
  const rawLanes = record(raw.lanes, 'consent.lanes');
  const lanes = {} as Record<ConsentLane, ConsentLaneStatus>;
  for (const [lane, status] of Object.entries(rawLanes)) {
    if (!isConsentLane(lane)) throw new ConsentContractError(`consent.lanes.${lane}`);
    lanes[lane] = parseLaneStatus(status, `consent.lanes.${lane}`);
  }
  for (const lane of consentLanes) {
    if (!lanes[lane]) throw new ConsentContractError(`consent.lanes.${lane}`);
  }

  const rawScopes = record(raw.scopes, 'consent.scopes');
  const scopes: Partial<Record<ConsentScope, ConsentDisposition>> = {};
  for (const [scope, disposition] of Object.entries(rawScopes)) {
    if (!isConsentScope(scope) || !isConsentDisposition(disposition)) {
      throw new ConsentContractError(`consent.scopes.${scope}`);
    }
    scopes[scope] = disposition;
  }

  const principalKind = nonEmptyString(raw.principalKind, 'consent.principalKind');
  if (principalKind !== 'user' && principalKind !== 'guest') {
    throw new ConsentContractError('consent.principalKind');
  }

  return {
    policyVersion: nonEmptyString(raw.policyVersion, 'consent.policyVersion'),
    principalKind,
    roomId: nonEmptyString(raw.roomId, 'consent.roomId'),
    sittingId: nonEmptyString(raw.sittingId, 'consent.sittingId'),
    guestPolicyListenOnly: boolean(raw.guestPolicyListenOnly, 'consent.guestPolicyListenOnly'),
    storeAvailable: boolean(raw.storeAvailable, 'consent.storeAvailable'),
    lanes,
    scopes,
  };
}

export function buildConsentDecision(
  scope: ConsentScope,
  disposition: ConsentDisposition,
): ConsentDecisionRequest {
  if (!isConsentScope(scope)) throw new ConsentContractError('decision.scope');
  if (!isConsentDisposition(disposition)) throw new ConsentContractError('decision.disposition');
  return { scope, disposition };
}

export function parseConsentDecisionResponse(value: unknown): ConsentDecisionResponse {
  const raw = record(value, 'decision');
  const sequence = raw.lastAcceptedCaptureSequence;
  if (sequence !== null && (!Number.isSafeInteger(sequence) || (sequence as number) < 0)) {
    throw new ConsentContractError('decision.lastAcceptedCaptureSequence');
  }
  return {
    recordId: nonEmptyString(raw.recordId, 'decision.recordId'),
    recordedAt: nonEmptyString(raw.recordedAt, 'decision.recordedAt'),
    lastAcceptedCaptureSequence: sequence as number | null,
    consent: parseConsentStatus(raw.consent),
  };
}
