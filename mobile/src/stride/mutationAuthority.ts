import {
  isStrideProjectionActionType,
  parseStrideActionValues,
  strideActionSurface,
  type StrideActionValues,
  type StrideProjectionAction,
  type StrideProjectionActionType,
  type StrideSurfaceName,
} from './models';

export type StrideRequestAuthority = {
  sessionToken: string;
  accountKey: string;
};

export type StrideMutationPersistence = {
  read: (key: string) => Promise<string | null>;
  write: (key: string, value: string) => Promise<void>;
  remove: (key: string) => Promise<void>;
};

type PendingMutation = {
  fingerprint: string;
  operationKey: string;
  actionId: string;
  actionType: StrideProjectionActionType;
  expectedRevision: number;
  surface: StrideSurfaceName;
  origin: StrideSurfaceName;
  values: StrideActionValues;
};

export type StridePendingMutation = Readonly<{
  operationKey: string;
  actionId: string;
  actionType: StrideProjectionActionType;
  expectedRevision: number;
  surface: StrideSurfaceName;
  origin: StrideSurfaceName;
  values: StrideActionValues;
}>;

type StoredMutation = {
  schema: 1;
  accountFingerprint: string;
  actionId: string;
  actionType: StrideProjectionActionType;
  surface: StrideSurfaceName;
  origin: StrideSurfaceName;
  revision: number;
  values: StrideActionValues;
  idempotencyKey: string;
};

const STORAGE_PREFIX = 'bonfire.strideMutation.v1.';
const SURFACES = new Set<StrideSurfaceName>([
  'profile', 'work-record', 'organizations', 'organization-people',
  'organization-requests', 'contribution-approvals', 'network-draft',
  'network-preview', 'network-recruiter-view', 'network-search',
  'contact-inbox', 'network-blocks', 'organization-recruiting',
  'coworker-profile',
]);

export class StrideMutationAmbiguityError extends Error {
  constructor() {
    super('Discard the unresolved mutation before changing its action or body.');
    this.name = 'StrideMutationAmbiguityError';
  }
}

export class StrideMutationPersistenceError extends Error {
  constructor(message = 'The unresolved mutation record could not be verified.') {
    super(message);
    this.name = 'StrideMutationPersistenceError';
  }
}

export function sameStrideAuthority(
  left: StrideRequestAuthority | null,
  right: StrideRequestAuthority | null,
): boolean {
  return left !== null
    && right !== null
    && left.sessionToken === right.sessionToken
    && left.accountKey === right.accountKey;
}

function cloneValues(values: StrideActionValues): StrideActionValues {
  return Object.fromEntries(Object.entries(values).map(([key, value]) => [
    key,
    Array.isArray(value) ? [...value] : value,
  ]));
}

function mutationFingerprint(
  authority: Pick<StrideRequestAuthority, 'accountKey'>,
  surface: StrideSurfaceName,
  action: StrideProjectionAction,
  values: StrideActionValues,
): string {
  const entries = Object.entries(values)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => [key, Array.isArray(value) ? [...value] : value]);
  return JSON.stringify([
    authority.accountKey.trim().toLowerCase(),
    surface,
    action.id,
    action.type,
    action.expectedRevision,
    entries,
  ]);
}

// This fingerprint is an opaque storage partition, not authentication. The
// envelope itself is encrypted by SecureStore and never contains the account
// name, email, session token, person ID, membership ID, or organization ID.
export function strideMutationAccountFingerprint(accountKey: string): string {
  const input = `stride-mutation-account-v1\0${accountKey.trim().toLowerCase()}`;
  let left = 0x811c9dc5;
  let right = 0x9e3779b9;
  for (let index = 0; index < input.length; index += 1) {
    const code = input.charCodeAt(index);
    left = Math.imul(left ^ code, 0x01000193) >>> 0;
    right = Math.imul(right ^ (code + index), 0x85ebca6b) >>> 0;
  }
  return `${left.toString(16).padStart(8, '0')}${right.toString(16).padStart(8, '0')}`;
}

function storageKey(accountKey: string): string {
  return `${STORAGE_PREFIX}${strideMutationAccountFingerprint(accountKey)}`;
}

function exactObject(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new StrideMutationPersistenceError();
  }
  const record = value as Record<string, unknown>;
  if (Object.keys(record).length !== keys.length || keys.some((key) => !(key in record))) {
    throw new StrideMutationPersistenceError();
  }
  return record;
}

function boundedString(value: unknown): string {
  if (typeof value !== 'string' || value.trim() === '' || value.length > 500) {
    throw new StrideMutationPersistenceError();
  }
  return value;
}

function parseStoredMutation(raw: string, authority: StrideRequestAuthority): PendingMutation {
  let decoded: unknown;
  try {
    decoded = JSON.parse(raw);
  } catch {
    throw new StrideMutationPersistenceError();
  }
  const record = exactObject(decoded, [
    'schema', 'accountFingerprint', 'actionId', 'actionType', 'surface',
    'origin', 'revision', 'values', 'idempotencyKey',
  ]);
  if (record.schema !== 1
      || record.accountFingerprint !== strideMutationAccountFingerprint(authority.accountKey)
      || !isStrideProjectionActionType(record.actionType)
      || typeof record.surface !== 'string'
      || !SURFACES.has(record.surface as StrideSurfaceName)
      || typeof record.origin !== 'string'
      || !SURFACES.has(record.origin as StrideSurfaceName)
      || record.origin !== record.surface
      || strideActionSurface(record.actionType) !== record.surface
      || !Number.isSafeInteger(record.revision)
      || (record.revision as number) < 0
      || (record.revision as number) > 1_000_000_000) {
    throw new StrideMutationPersistenceError();
  }
  const action: StrideProjectionAction = {
    id: boundedString(record.actionId),
    type: record.actionType,
    label: record.actionType,
    expectedRevision: record.revision as number,
  };
  const values = parseStrideActionValues(action.type, record.values);
  const surface = record.surface as StrideSurfaceName;
  return {
    fingerprint: mutationFingerprint(authority, surface, action, values),
    operationKey: boundedString(record.idempotencyKey),
    actionId: action.id,
    actionType: action.type,
    expectedRevision: action.expectedRevision,
    surface,
    origin: record.origin as StrideSurfaceName,
    values,
  };
}

function serializePending(pending: PendingMutation, accountKey: string): string {
  const stored: StoredMutation = {
    schema: 1,
    accountFingerprint: strideMutationAccountFingerprint(accountKey),
    actionId: pending.actionId,
    actionType: pending.actionType,
    surface: pending.surface,
    origin: pending.origin,
    revision: pending.expectedRevision,
    values: cloneValues(pending.values),
    idempotencyKey: pending.operationKey,
  };
  return JSON.stringify(stored);
}

export class StrideMutationLedger {
  private pending: PendingMutation | null = null;

  hasPending(): boolean {
    return this.pending !== null;
  }

  pendingMutation(): StridePendingMutation | null {
    if (!this.pending) return null;
    return {
      operationKey: this.pending.operationKey,
      actionId: this.pending.actionId,
      actionType: this.pending.actionType,
      expectedRevision: this.pending.expectedRevision,
      surface: this.pending.surface,
      origin: this.pending.origin,
      values: cloneValues(this.pending.values),
    };
  }

  operationKey(
    authority: StrideRequestAuthority,
    surface: StrideSurfaceName,
    action: StrideProjectionAction,
    values: StrideActionValues,
    create: () => string,
  ): string {
    const fingerprint = mutationFingerprint(authority, surface, action, values);
    if (this.pending?.fingerprint === fingerprint) return this.pending.operationKey;
    if (this.pending) throw new StrideMutationAmbiguityError();
    const operationKey = create();
    this.pending = {
      fingerprint,
      operationKey,
      actionId: action.id,
      actionType: action.type,
      expectedRevision: action.expectedRevision,
      surface,
      origin: surface,
      values: cloneValues(values),
    };
    return operationKey;
  }

  async hydrate(authority: StrideRequestAuthority, persistence: StrideMutationPersistence): Promise<void> {
    const raw = await persistence.read(storageKey(authority.accountKey));
    if (raw === null) {
      if (this.pending) await persistence.write(storageKey(authority.accountKey), serializePending(this.pending, authority.accountKey));
      return;
    }
    const restored = parseStoredMutation(raw, authority);
    if (this.pending && (this.pending.operationKey !== restored.operationKey || this.pending.fingerprint !== restored.fingerprint)) {
      throw new StrideMutationPersistenceError();
    }
    this.pending = restored;
  }

  async persist(authority: StrideRequestAuthority, persistence: StrideMutationPersistence): Promise<void> {
    if (!this.pending) throw new StrideMutationPersistenceError();
    await persistence.write(storageKey(authority.accountKey), serializePending(this.pending, authority.accountKey));
  }

  async settlePersisted(operationKey: string, authority: StrideRequestAuthority, persistence: StrideMutationPersistence): Promise<void> {
    if (this.pending?.operationKey !== operationKey) return;
    await persistence.remove(storageKey(authority.accountKey));
    this.pending = null;
  }

  async discardPersisted(authority: StrideRequestAuthority, persistence: StrideMutationPersistence): Promise<void> {
    await persistence.remove(storageKey(authority.accountKey));
    this.pending = null;
  }

  settle(operationKey: string): void {
    if (this.pending?.operationKey === operationKey) this.pending = null;
  }

  discard(): void {
    this.pending = null;
  }
}

const accountMutationLedgers = new Map<string, StrideMutationLedger>();

export function strideMutationLedgerForAccount(accountKey: string): StrideMutationLedger {
  const normalized = accountKey.trim().toLowerCase();
  if (!normalized) return new StrideMutationLedger();
  let ledger = accountMutationLedgers.get(normalized);
  if (!ledger) {
    ledger = new StrideMutationLedger();
    accountMutationLedgers.set(normalized, ledger);
  }
  return ledger;
}
