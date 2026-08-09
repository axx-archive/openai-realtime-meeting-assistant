export type StrideSurfaceAvailability = 'available' | 'unavailable';

export type StrideSurfaceName =
  | 'profile'
  | 'work-record'
  | 'organizations'
  | 'organization-people'
  | 'organization-requests'
  | 'contribution-approvals'
  | 'network-draft'
  | 'network-preview'
  | 'network-recruiter-view'
  | 'network-search'
  | 'contact-inbox'
  | 'network-blocks'
  | 'organization-recruiting'
  | 'coworker-profile';

export type StridePublicReference = {
  id: string;
  revision: number;
  digest: string;
};

export type StrideProjectionDetail =
  | { kind: 'self-profile-detail'; displayName: string; pronouns?: string; bio?: string; workModes: string[]; openTo: string[]; openToEnabled: boolean; organizationChoices: string[] }
  | { kind: 'coworker-profile-detail'; displayName: string; role: string; title?: string; team?: string; joinedAt: string }
  | { kind: 'network-profile-detail'; displayName?: string; pronouns?: string; bio?: string; visibleOrganizations?: string[]; workModes?: string[]; openTo?: string[] }
  | { kind: 'work-record-section'; section: 'problems-outcomes' | 'how-i-contribute' | 'organizations-roles' | 'work-evidence' | 'people-agents-helped'; entries: string[] }
  | { kind: 'work-record-section'; section: 'open-to'; entries: string[]; openToEnabled: boolean }
  | { kind: 'contribution-evidence'; problem: string; outcome: string; contribution: string; verificationTier: string; releasedFields: string[]; attestation: StridePublicReference; publishedClaim: StridePublicReference; artifactAccess: 'authorized' | 'redacted'; reviewedInfluence?: string }
  | { kind: 'contribution-review'; claim: StridePublicReference; sourceRevision: number; sourceDigest: string; fieldDiffs: Array<{ field: string; before: string; after: string; disclosureTier: string }>; namedPartyStates: Array<{ partyLabel: string; state: string; required: boolean }>; auditEntries: Array<{ action: string; actorRole: string; revision: number; occurredAt: string }> }
  | { kind: 'network-state'; state: 'off' | 'draft' | 'live' | 'paused'; searchableFields: string[] }
  | { kind: 'recruiting-governance'; grantState: 'active' | 'revoked' | 'expired'; grantRevision: number; expiresAt: string; capability: 'talent_searcher'; personSearchLimit: StrideLimitSummary; organizationSearchLimit: StrideLimitSummary; globalSearchLimit: StrideLimitSummary; personContactLimit: StrideLimitSummary; organizationContactLimit: StrideLimitSummary; globalContactLimit: StrideLimitSummary; receiptSummaries: StrideReceiptSummary[]; auditEntries: StrideAuditSummary[] }
  | { kind: 'organization-summary'; activeCount: number; capacity: 3; pendingCount: number; isCurrent: boolean; role: string }
  | { kind: 'membership-detail'; role: string; status: string; isFinalOwner: boolean }
  | { kind: 'join-request-detail'; status: string; expiresAt: string }
  | { kind: 'network-query-interpretation'; verdict: string; filters: string[] }
  | { kind: 'network-search-result'; why: string[]; unknown: string[]; verificationLabels: string[]; publishedRefs: StridePublicReference[] }
  | { kind: 'contact-request-detail'; purpose: string; collaborationType: string; state: string; channelRevealed: boolean }
  | { kind: 'block-detail'; state: 'active' | 'withdrawn'; targetKind: 'person' | 'organization' }
  | { kind: 'export-receipt'; status: string; packageDigest: string; expiresAt: string }
  | { kind: 'purge-receipt'; status: string; receiptId: string; stores: string[] };

export type StrideLimitSummary = { used: number; limit: number; windowEndsAt: string };
export type StrideReceiptSummary = { kind: 'search' | 'contact'; verdict: 'admitted' | 'denied'; revision: number; occurredAt: string };
export type StrideAuditSummary = { action: string; actorRole: string; revision: number; occurredAt: string };

/** A server-authored display projection. It deliberately carries no authority inputs. */
export type StrideProjectionItem = {
  id: string;
  title: string;
  summary?: string;
  status?: string;
  context?: string;
  updatedAt?: string;
  kind?: StrideProjectionDetail['kind'];
  detail?: StrideProjectionDetail;
  actions?: StrideProjectionAction[];
};

export type StrideProjectionActionType =
  | 'profile-update'
  | 'organization-create'
  | 'organization-join'
  | 'organization-request-approve'
  | 'organization-request-deny'
  | 'organization-switch'
  | 'organization-leave'
  | 'organization-member-role-change'
  | 'organization-member-revoke'
  | 'organization-ownership-transfer'
  | 'organization-recruiting-grant-create'
  | 'organization-recruiting-grant-revoke'
  | 'network-draft-save'
  | 'network-publish'
  | 'network-pause'
  | 'network-profile-off'
  | 'network-search-propose'
  | 'network-search-confirm'
  | 'contact-send'
  | 'exact-link-contact-send'
  | 'contribution-subject-approve'
  | 'contribution-subject-dispute'
  | 'contribution-organization-approve'
  | 'contribution-organization-deny'
  | 'contribution-publish'
  | 'contribution-withdraw'
  | 'contribution-correct'
  | 'contribution-revoke'
  | 'contribution-named-party-decision'
  | 'contribution-attestation-revoke'
  | 'work-record-export'
  | 'work-record-delete'
  | 'network-profile-export'
  | 'network-profile-delete'
  | 'network-searchable-fields-update'
  | 'contact-accept'
  | 'contact-decline'
  | 'contact-withdraw'
  | 'network-block'
  | 'network-unblock';

export type StrideProjectionAction = {
  id: string;
  type: StrideProjectionActionType;
  label: string;
  expectedRevision: number;
};

export type StrideSurfaceProjection =
  | {
      availability: 'available';
      surface: StrideSurfaceName;
      revision: number;
      items: StrideProjectionItem[];
    }
  | {
      availability: 'unavailable';
      surface: StrideSurfaceName;
      reason: string;
    };

const FORBIDDEN_PROJECTION_KEYS = new Set([
  'email',
  'email_address',
  'mymind',
  'my_mind',
  'hidden_membership',
  'hidden_memberships',
  'source_body',
  'source_bodies',
  'score',
  'scores',
]);
const ENVELOPE_KEYS = new Set(['availability', 'surface', 'revision', 'items', 'reason']);
const ITEM_KEYS = new Set(['id', 'title', 'summary', 'status', 'context', 'updatedAt', 'kind', 'detail', 'actions']);
const ACTION_KEYS = new Set(['id', 'type', 'label', 'expectedRevision']);
const ACTION_TYPES = new Set<StrideProjectionActionType>([
  'profile-update',
  'organization-create',
  'organization-join',
  'organization-request-approve',
  'organization-request-deny',
  'organization-switch',
  'organization-leave',
  'organization-member-role-change',
  'organization-member-revoke',
  'organization-ownership-transfer',
  'organization-recruiting-grant-create',
  'organization-recruiting-grant-revoke',
  'network-draft-save',
  'network-publish',
  'network-pause',
  'network-profile-off',
  'network-search-propose',
  'network-search-confirm',
  'contact-send',
  'exact-link-contact-send',
  'contribution-subject-approve',
  'contribution-subject-dispute',
  'contribution-organization-approve',
  'contribution-organization-deny',
  'contribution-publish',
  'contribution-withdraw',
  'contribution-correct',
  'contribution-revoke',
  'contribution-named-party-decision',
  'contribution-attestation-revoke',
  'work-record-export',
  'work-record-delete',
  'network-profile-export',
  'network-profile-delete',
  'network-searchable-fields-update',
  'contact-accept',
  'contact-decline',
  'contact-withdraw',
  'network-block',
  'network-unblock',
]);
const ACTION_SURFACES: Record<StrideProjectionActionType, StrideSurfaceName> = {
  'profile-update': 'profile',
  'organization-create': 'organizations',
  'organization-join': 'organizations',
  'organization-request-approve': 'organization-requests',
  'organization-request-deny': 'organization-requests',
  'organization-switch': 'organizations',
  'organization-leave': 'organizations',
  'organization-member-role-change': 'organization-people',
  'organization-member-revoke': 'organization-people',
  'organization-ownership-transfer': 'organization-people',
  'organization-recruiting-grant-create': 'organization-recruiting',
  'organization-recruiting-grant-revoke': 'organization-recruiting',
  'network-draft-save': 'network-draft',
  'network-publish': 'network-preview',
  'network-pause': 'network-preview',
  'network-profile-off': 'network-preview',
  'network-search-propose': 'network-search',
  'network-search-confirm': 'network-search',
  'contact-send': 'network-search',
  'exact-link-contact-send': 'network-recruiter-view',
  'contribution-subject-approve': 'work-record',
  'contribution-subject-dispute': 'work-record',
  'contribution-organization-approve': 'contribution-approvals',
  'contribution-organization-deny': 'contribution-approvals',
  'contribution-publish': 'work-record',
  'contribution-withdraw': 'work-record',
  'contribution-correct': 'contribution-approvals',
  'contribution-revoke': 'contribution-approvals',
  'contribution-named-party-decision': 'contribution-approvals',
  'contribution-attestation-revoke': 'contribution-approvals',
  'work-record-export': 'work-record',
  'work-record-delete': 'work-record',
  'network-profile-export': 'network-preview',
  'network-profile-delete': 'network-preview',
  'network-searchable-fields-update': 'network-preview',
  'contact-accept': 'contact-inbox',
  'contact-decline': 'contact-inbox',
  'contact-withdraw': 'contact-inbox',
  'network-block': 'network-blocks',
  'network-unblock': 'network-blocks',
};

export function isStrideProjectionActionType(value: unknown): value is StrideProjectionActionType {
  return typeof value === 'string' && ACTION_TYPES.has(value as StrideProjectionActionType);
}

export function strideActionSurface(actionType: StrideProjectionActionType): StrideSurfaceName {
  return ACTION_SURFACES[actionType];
}

export type StrideActionValues = Record<string, string | string[]>;

const ACTION_VALUE_KEYS: Partial<Record<StrideProjectionActionType, ReadonlySet<string>>> = {
  'profile-update': new Set(['displayName', 'pronouns', 'bio', 'workModes', 'openTo']),
  'organization-create': new Set(['name', 'slug']),
  'organization-join': new Set(['joinCode']),
  'organization-member-role-change': new Set(['role']),
  'organization-recruiting-grant-revoke': new Set(['reason']),
  'network-draft-save': new Set(['intro', 'workModes', 'openTo']),
  'network-search-propose': new Set(['query']),
  'contact-send': new Set(['purpose', 'note', 'collaborationType']),
  'exact-link-contact-send': new Set(['purpose', 'note', 'collaborationType']),
  'organization-request-approve': new Set(['reason']),
  'organization-request-deny': new Set(['reason']),
  'contribution-subject-approve': new Set(['reason']),
  'contribution-subject-dispute': new Set(['reason']),
  'contribution-organization-approve': new Set(['reason']),
  'contribution-organization-deny': new Set(['reason']),
  'contribution-correct': new Set(['reason']),
  'contribution-revoke': new Set(['reason']),
  'contribution-named-party-decision': new Set(['decision', 'reason']),
  'contribution-attestation-revoke': new Set(['reason']),
  'network-searchable-fields-update': new Set(['fields']),
  'contact-accept': new Set(['reason']),
  'contact-decline': new Set(['reason']),
  'contact-withdraw': new Set(['reason']),
};

function closedString(
  value: unknown,
  field: string,
  maxLength: number,
  required = false,
): string | undefined {
  if (value === undefined && !required) return undefined;
  if (typeof value !== 'string') throw new Error(`Invalid STRIDE action value: ${field}`);
  const normalized = value.trim();
  if ((required && normalized === '') || normalized.length > maxLength) {
    throw new Error(`Invalid STRIDE action value: ${field}`);
  }
  return normalized;
}

function closedStringList(value: unknown, field: string): string[] | undefined {
  if (value === undefined) return undefined;
  if (!Array.isArray(value) || value.length > 20) {
    throw new Error(`Invalid STRIDE action value: ${field}`);
  }
  const normalized = value.map((item) => closedString(item, field, 64, true) as string);
  if (new Set(normalized).size !== normalized.length) {
    throw new Error(`Invalid STRIDE action value: ${field}`);
  }
  return normalized;
}

export function parseStrideActionValues(
  actionType: StrideProjectionActionType,
  value: unknown,
): StrideActionValues {
  const values = record(value);
  if (!values) throw new Error('Invalid STRIDE action values');
  const allowed = ACTION_VALUE_KEYS[actionType] ?? new Set<string>();
  for (const key of Object.keys(values)) {
    if (!allowed.has(key)) throw new Error(`Unknown STRIDE action value: ${key}`);
  }
  const result: StrideActionValues = {};
  const add = (key: string, parsed: string | string[] | undefined) => {
    if (parsed !== undefined) result[key] = parsed;
  };
  switch (actionType) {
    case 'profile-update':
      if (Object.keys(values).length === 0) throw new Error('Invalid STRIDE action values');
      add('displayName', closedString(values.displayName, 'displayName', 80));
      add('pronouns', closedString(values.pronouns, 'pronouns', 40));
      add('bio', closedString(values.bio, 'bio', 280));
      add('workModes', closedStringList(values.workModes, 'workModes'));
      add('openTo', closedStringList(values.openTo, 'openTo'));
      break;
    case 'organization-create':
      add('name', closedString(values.name, 'name', 120, true));
      add('slug', closedString(values.slug, 'slug', 63, true));
		if (!/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(result.slug as string)) {
        throw new Error('Invalid STRIDE action value: slug');
      }
      break;
    case 'organization-join':
      add('joinCode', closedString(values.joinCode, 'joinCode', 128, true));
      break;
    case 'organization-member-role-change':
      add('role', closedString(values.role, 'role', 16, true));
      if (!['member', 'admin'].includes(result.role as string)) {
        throw new Error('Invalid STRIDE action value: role');
      }
      break;
    case 'network-searchable-fields-update': {
      const fields = closedStringList(values.fields, 'fields') ?? [];
      const allowedFields = new Set(['display_name', 'pronouns', 'bio', 'work_modes', 'open_to', 'visible_organizations', 'contribution_problem_classes', 'contribution_roles', 'verified_contributions']);
      if (fields.length > 9 || fields.some((field) => !allowedFields.has(field))) throw new Error('Invalid STRIDE action value: fields');
      result.fields = fields;
      break;
    }
    case 'network-draft-save':
      if (Object.keys(values).length === 0) throw new Error('Invalid STRIDE action values');
      add('intro', closedString(values.intro, 'intro', 280));
      add('workModes', closedStringList(values.workModes, 'workModes'));
      add('openTo', closedStringList(values.openTo, 'openTo'));
      break;
    case 'network-search-propose':
      add('query', closedString(values.query, 'query', 240, true));
      break;
    case 'contact-send':
    case 'exact-link-contact-send':
      add('purpose', closedString(values.purpose, 'purpose', 80, true));
      add('note', closedString(values.note, 'note', 1_000));
      add('collaborationType', closedString(values.collaborationType, 'collaborationType', 32, true));
      if (!['collaboration', 'advisory', 'employment', 'recruiting', 'organization_join']
        .includes(result.collaborationType as string)) {
        throw new Error('Invalid STRIDE action value: collaborationType');
      }
      break;
    case 'contribution-named-party-decision':
      add('decision', closedString(values.decision, 'decision', 16, true));
      if (!['approved', 'denied'].includes(result.decision as string)) {
        throw new Error('Invalid STRIDE action value: decision');
      }
      add('reason', closedString(values.reason, 'reason', 500));
      break;
    default:
      if (allowed.has('reason')) add('reason', closedString(values.reason, 'reason', 500));
  }
  return result;
}

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function assertProjectionSafe(value: unknown): void {
  if (Array.isArray(value)) {
    value.forEach(assertProjectionSafe);
    return;
  }
  const candidate = record(value);
  if (!candidate) return;
  for (const [key, child] of Object.entries(candidate)) {
    if (FORBIDDEN_PROJECTION_KEYS.has(key.toLowerCase())) {
      throw new Error(`Unsafe STRIDE projection field: ${key}`);
    }
    assertProjectionSafe(child);
  }
}

function requiredString(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.trim() === '' || value.trim().length > 500) {
    throw new Error(`Invalid STRIDE projection ${field}`);
  }
  return value.trim();
}

function exactEnum<T extends string>(value: unknown, field: string, allowed: readonly T[]): T {
  if (typeof value !== 'string' || !allowed.includes(value as T)) throw new Error(`Invalid STRIDE projection ${field}`);
  return value as T;
}

function rfc3339(value: unknown, field: string): string {
  const text = requiredString(value, field);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(text) || Number.isNaN(Date.parse(text))) {
    throw new Error(`Invalid STRIDE projection ${field}`);
  }
  return text;
}

function boundedInteger(value: unknown, field: string, maximum = 1_000_000_000): number {
  if (!Number.isSafeInteger(value) || (value as number) < 0 || (value as number) > maximum) throw new Error(`Invalid STRIDE projection ${field}`);
  return value as number;
}

function optionalString(value: unknown, field: string): string | undefined {
  if (value === undefined || value === null || value === '') return undefined;
  return requiredString(value, field);
}

function closedObject(value: unknown, keys: readonly string[], field: string): Record<string, unknown> {
  const object = record(value);
  if (!object || Object.keys(object).some((key) => !keys.includes(key))) {
    throw new Error(`Invalid STRIDE projection ${field}`);
  }
  return object;
}

function closedStrings(value: unknown, field: string, maximum = 50): string[] {
  if (!Array.isArray(value) || value.length > maximum) throw new Error(`Invalid STRIDE projection ${field}`);
  const strings = value.map((entry) => requiredString(entry, field));
  if (strings.some((entry) => entry.length > 500) || new Set(strings).size !== strings.length) {
    throw new Error(`Invalid STRIDE projection ${field}`);
  }
  return strings;
}

function publicReference(value: unknown, field: string): StridePublicReference {
  const reference = closedObject(value, ['id', 'revision', 'digest'], field);
  if (!Number.isSafeInteger(reference.revision) || (reference.revision as number) < 1
      || typeof reference.digest !== 'string' || !/^[a-f0-9]{64}$/.test(reference.digest)) {
    throw new Error(`Invalid STRIDE projection ${field}`);
  }
  return { id: requiredString(reference.id, `${field} id`), revision: reference.revision as number, digest: reference.digest };
}

function parseProjectionDetail(surface: StrideSurfaceName, kind: unknown, value: unknown): StrideProjectionDetail {
  if (typeof kind !== 'string') throw new Error('Invalid STRIDE projection detail kind');
  const detail = record(value);
  if (!detail || detail.kind !== kind) throw new Error('STRIDE projection detail kind mismatch');
  switch (kind) {
    case 'self-profile-detail': {
      if (surface !== 'profile') throw new Error('Self profile detail surface mismatch');
      const item = closedObject(detail, ['kind', 'displayName', 'pronouns', 'bio', 'workModes', 'openTo', 'openToEnabled', 'organizationChoices'], 'self profile detail');
      if (typeof item.openToEnabled !== 'boolean') throw new Error('Invalid self profile open-to state');
      const parsed = { kind, displayName: requiredString(item.displayName, 'display name'), workModes: closedStrings(item.workModes, 'work modes', 20), openTo: closedStrings(item.openTo, 'open to', 20), openToEnabled: item.openToEnabled, organizationChoices: closedStrings(item.organizationChoices, 'organization choices', 3) } satisfies StrideProjectionDetail;
      const pronouns = optionalString(item.pronouns, 'pronouns');
      const bio = optionalString(item.bio, 'bio');
      return { ...parsed, ...(pronouns ? { pronouns } : {}), ...(bio ? { bio } : {}) };
    }
    case 'coworker-profile-detail': {
      if (surface !== 'coworker-profile') throw new Error('Coworker profile detail surface mismatch');
      const item = closedObject(detail, ['kind', 'displayName', 'role', 'title', 'team', 'joinedAt'], 'coworker profile detail');
      const parsed = { kind, displayName: requiredString(item.displayName, 'display name'), role: exactEnum(item.role, 'coworker role', ['owner', 'admin', 'member'] as const), joinedAt: rfc3339(item.joinedAt, 'joined at') } satisfies StrideProjectionDetail;
      const title = optionalString(item.title, 'title');
      const team = optionalString(item.team, 'team');
      return { ...parsed, ...(title ? { title } : {}), ...(team ? { team } : {}) };
    }
    case 'network-profile-detail': {
      if (!['network-preview', 'network-recruiter-view', 'network-search'].includes(surface)) throw new Error('Network profile detail surface mismatch');
      const item = closedObject(detail, ['kind', 'displayName', 'pronouns', 'bio', 'visibleOrganizations', 'workModes', 'openTo'], 'network profile detail');
      const parsed: Extract<StrideProjectionDetail, { kind: 'network-profile-detail' }> = { kind };
      const displayName = optionalString(item.displayName, 'network display name');
      const pronouns = optionalString(item.pronouns, 'network pronouns');
      const bio = optionalString(item.bio, 'network bio');
      if (displayName) parsed.displayName = displayName;
      if (pronouns) parsed.pronouns = pronouns;
      if (bio) parsed.bio = bio;
      if (item.visibleOrganizations !== undefined) parsed.visibleOrganizations = closedStrings(item.visibleOrganizations, 'visible organizations', 3);
      if (item.workModes !== undefined) parsed.workModes = closedStrings(item.workModes, 'network work modes', 20);
      if (item.openTo !== undefined) parsed.openTo = closedStrings(item.openTo, 'network open to', 20);
      if (Object.keys(parsed).length === 1) throw new Error('Empty network profile detail');
      return parsed;
    }
    case 'work-record-section': {
      const item = closedObject(detail, ['kind', 'section', 'entries', 'openToEnabled'], 'work record detail');
      const sections = ['problems-outcomes', 'how-i-contribute', 'organizations-roles', 'work-evidence', 'people-agents-helped', 'open-to'] as const;
      if (!sections.includes(item.section as typeof sections[number])) throw new Error('Invalid work record section');
      const entries = closedStrings(item.entries, 'work record entries');
      if (item.section === 'open-to') {
        if (typeof item.openToEnabled !== 'boolean') throw new Error('Invalid open-to state');
        return { kind, section: 'open-to', entries, openToEnabled: item.openToEnabled };
      }
      if (item.openToEnabled !== undefined) throw new Error('Open-to state on wrong section');
      return { kind, section: item.section as Exclude<typeof sections[number], 'open-to'>, entries };
    }
    case 'contribution-evidence': {
      const item = closedObject(detail, ['kind', 'problem', 'outcome', 'contribution', 'verificationTier', 'releasedFields', 'attestation', 'publishedClaim', 'artifactAccess', 'reviewedInfluence'], 'contribution evidence');
      if (!['authorized', 'redacted'].includes(item.artifactAccess as string)) throw new Error('Invalid artifact access');
      const verificationTier = exactEnum(item.verificationTier, 'verification tier', ['self_described', 'organization_verified_opaque', 'organization_verified_redacted', 'public_source_verified'] as const);
      const parsed: StrideProjectionDetail = { kind, problem: requiredString(item.problem, 'problem'), outcome: requiredString(item.outcome, 'outcome'), contribution: requiredString(item.contribution, 'contribution'), verificationTier, releasedFields: closedStrings(item.releasedFields, 'released fields', 30), attestation: publicReference(item.attestation, 'attestation'), publishedClaim: publicReference(item.publishedClaim, 'published claim'), artifactAccess: item.artifactAccess as 'authorized' | 'redacted' };
      const reviewedInfluence = optionalString(item.reviewedInfluence, 'reviewed influence');
      return reviewedInfluence === undefined ? parsed : { ...parsed, reviewedInfluence };
    }
    case 'contribution-review': {
      const item = closedObject(detail, ['kind', 'claim', 'sourceRevision', 'sourceDigest', 'fieldDiffs', 'namedPartyStates', 'auditEntries'], 'contribution review');
      if (!Number.isSafeInteger(item.sourceRevision) || (item.sourceRevision as number) < 1 || typeof item.sourceDigest !== 'string' || !/^[a-f0-9]{64}$/.test(item.sourceDigest)) throw new Error('Invalid contribution source binding');
      if (!Array.isArray(item.fieldDiffs) || !Array.isArray(item.namedPartyStates) || !Array.isArray(item.auditEntries) || item.fieldDiffs.length > 50 || item.namedPartyStates.length > 50 || item.auditEntries.length > 100) throw new Error('Invalid contribution review collections');
      const fieldDiffs = item.fieldDiffs.map((raw) => { const value = closedObject(raw, ['field', 'before', 'after', 'disclosureTier'], 'field diff'); return { field: requiredString(value.field, 'diff field'), before: requiredString(value.before, 'diff before'), after: requiredString(value.after, 'diff after'), disclosureTier: exactEnum(value.disclosureTier, 'disclosure tier', ['public', 'redacted', 'opaque'] as const) }; });
      const namedPartyStates = item.namedPartyStates.map((raw) => { const value = closedObject(raw, ['partyLabel', 'state', 'required'], 'named party state'); if (typeof value.required !== 'boolean') throw new Error('Invalid named party required state'); return { partyLabel: requiredString(value.partyLabel, 'party label'), state: requiredString(value.state, 'party state'), required: value.required }; });
      const auditEntries = item.auditEntries.map((raw) => { const value = closedObject(raw, ['action', 'actorRole', 'revision', 'occurredAt'], 'audit entry'); const revision = boundedInteger(value.revision, 'audit revision'); if (revision < 1) throw new Error('Invalid audit revision'); return { action: requiredString(value.action, 'audit action'), actorRole: requiredString(value.actorRole, 'audit actor role'), revision, occurredAt: rfc3339(value.occurredAt, 'audit occurred at') }; });
      return { kind, claim: publicReference(item.claim, 'claim'), sourceRevision: item.sourceRevision as number, sourceDigest: item.sourceDigest, fieldDiffs, namedPartyStates, auditEntries };
    }
    case 'network-state': {
      const item = closedObject(detail, ['kind', 'state', 'searchableFields'], 'network state');
      if (!['off', 'draft', 'live', 'paused'].includes(item.state as string)) throw new Error('Invalid network state');
      const searchableFields = closedStrings(item.searchableFields, 'searchable fields', 9);
      const allowed = new Set(['display_name', 'pronouns', 'bio', 'work_modes', 'open_to', 'visible_organizations', 'contribution_problem_classes', 'contribution_roles', 'verified_contributions']);
      if (searchableFields.some((field) => !allowed.has(field))) throw new Error('Invalid searchable field');
      return { kind, state: item.state as 'off' | 'draft' | 'live' | 'paused', searchableFields };
    }
    case 'recruiting-governance': {
      const item = closedObject(detail, ['kind', 'grantState', 'grantRevision', 'expiresAt', 'capability', 'personSearchLimit', 'organizationSearchLimit', 'globalSearchLimit', 'personContactLimit', 'organizationContactLimit', 'globalContactLimit', 'receiptSummaries', 'auditEntries'], 'recruiting governance');
      const grantRevision = boundedInteger(item.grantRevision, 'grant revision');
      if (grantRevision < 1) throw new Error('Invalid recruiting grant revision');
      const limit = (raw: unknown, field: string): StrideLimitSummary => { const value = closedObject(raw, ['used', 'limit', 'windowEndsAt'], field); const used = boundedInteger(value.used, `${field} used`); const maximum = boundedInteger(value.limit, `${field} limit`); if (maximum < 1 || used > maximum) throw new Error(`Invalid ${field}`); return { used, limit: maximum, windowEndsAt: rfc3339(value.windowEndsAt, `${field} window end`) }; };
      if (!Array.isArray(item.receiptSummaries) || item.receiptSummaries.length > 100 || !Array.isArray(item.auditEntries) || item.auditEntries.length > 100) throw new Error('Invalid recruiting history');
      const receiptSummaries = item.receiptSummaries.map((raw): StrideReceiptSummary => { const value = closedObject(raw, ['kind', 'verdict', 'revision', 'occurredAt'], 'receipt summary'); const revision = boundedInteger(value.revision, 'receipt revision'); if (revision < 1) throw new Error('Invalid receipt revision'); return { kind: exactEnum(value.kind, 'receipt kind', ['search', 'contact'] as const), verdict: exactEnum(value.verdict, 'receipt verdict', ['admitted', 'denied'] as const), revision, occurredAt: rfc3339(value.occurredAt, 'receipt occurred at') }; });
      const auditEntries = item.auditEntries.map((raw): StrideAuditSummary => { const value = closedObject(raw, ['action', 'actorRole', 'revision', 'occurredAt'], 'recruiting audit'); const revision = boundedInteger(value.revision, 'audit revision'); if (revision < 1) throw new Error('Invalid audit revision'); return { action: requiredString(value.action, 'audit action'), actorRole: requiredString(value.actorRole, 'audit actor role'), revision, occurredAt: rfc3339(value.occurredAt, 'audit occurred at') }; });
      return { kind, grantState: exactEnum(item.grantState, 'grant state', ['active', 'revoked', 'expired'] as const), grantRevision, expiresAt: rfc3339(item.expiresAt, 'grant expiry'), capability: exactEnum(item.capability, 'grant capability', ['talent_searcher'] as const), personSearchLimit: limit(item.personSearchLimit, 'person search limit'), organizationSearchLimit: limit(item.organizationSearchLimit, 'organization search limit'), globalSearchLimit: limit(item.globalSearchLimit, 'global search limit'), personContactLimit: limit(item.personContactLimit, 'person contact limit'), organizationContactLimit: limit(item.organizationContactLimit, 'organization contact limit'), globalContactLimit: limit(item.globalContactLimit, 'global contact limit'), receiptSummaries, auditEntries };
    }
    case 'organization-summary': {
      if (surface !== 'organizations') throw new Error('Organization detail surface mismatch');
      const item = closedObject(detail, ['kind', 'activeCount', 'capacity', 'pendingCount', 'isCurrent', 'role'], 'organization summary');
      const activeCount = boundedInteger(item.activeCount, 'active organization count', 3);
      const pendingCount = boundedInteger(item.pendingCount, 'pending organization count', 3);
      if (item.capacity !== 3 || typeof item.isCurrent !== 'boolean') throw new Error('Invalid organization summary');
      return { kind, activeCount, capacity: 3, pendingCount, isCurrent: item.isCurrent, role: exactEnum(item.role, 'organization role', ['owner', 'admin', 'member'] as const) };
    }
    case 'membership-detail': {
      if (surface !== 'organization-people') throw new Error('Membership detail surface mismatch');
      const item = closedObject(detail, ['kind', 'role', 'status', 'isFinalOwner'], 'membership detail');
      if (typeof item.isFinalOwner !== 'boolean') throw new Error('Invalid final owner state');
      return { kind, role: exactEnum(item.role, 'membership role', ['owner', 'admin', 'member'] as const), status: exactEnum(item.status, 'membership status', ['active', 'departed', 'revoked'] as const), isFinalOwner: item.isFinalOwner };
    }
    case 'join-request-detail': {
      if (surface !== 'organization-requests') throw new Error('Join request detail surface mismatch');
      const item = closedObject(detail, ['kind', 'status', 'expiresAt'], 'join request detail');
      return { kind, status: exactEnum(item.status, 'join request status', ['pending', 'approved', 'denied', 'cancelled', 'expired'] as const), expiresAt: rfc3339(item.expiresAt, 'join request expiry') };
    }
    case 'network-query-interpretation': {
      if (surface !== 'network-search') throw new Error('Query interpretation surface mismatch');
      const item = closedObject(detail, ['kind', 'verdict', 'filters'], 'query interpretation');
      return { kind, verdict: exactEnum(item.verdict, 'query verdict', ['admitted', 'denied'] as const), filters: closedStrings(item.filters, 'query filters', 30) };
    }
    case 'network-search-result': {
      if (surface !== 'network-search') throw new Error('Search result surface mismatch');
      const item = closedObject(detail, ['kind', 'why', 'unknown', 'verificationLabels', 'publishedRefs'], 'network search result');
      if (!Array.isArray(item.publishedRefs) || item.publishedRefs.length > 50) throw new Error('Invalid published references');
      return { kind, why: closedStrings(item.why, 'why', 30), unknown: closedStrings(item.unknown, 'unknown', 30), verificationLabels: closedStrings(item.verificationLabels, 'verification labels', 30), publishedRefs: item.publishedRefs.map((value) => publicReference(value, 'published reference')) };
    }
    case 'contact-request-detail': {
      if (surface !== 'contact-inbox') throw new Error('Contact request detail surface mismatch');
      const item = closedObject(detail, ['kind', 'purpose', 'collaborationType', 'state', 'channelRevealed'], 'contact request detail');
      if (typeof item.channelRevealed !== 'boolean') throw new Error('Invalid contact channel state');
      const state = exactEnum(item.state, 'contact state', ['pending', 'accepted', 'declined', 'withdrawn', 'expired'] as const);
      if (item.channelRevealed && state !== 'accepted') throw new Error('Contact channel revealed before acceptance');
      return { kind, purpose: requiredString(item.purpose, 'contact purpose'), collaborationType: exactEnum(item.collaborationType, 'collaboration type', ['collaboration', 'advisory', 'employment', 'recruiting', 'organization_join'] as const), state, channelRevealed: item.channelRevealed };
    }
    case 'block-detail': {
      if (surface !== 'network-blocks') throw new Error('Block detail surface mismatch');
      const item = closedObject(detail, ['kind', 'state', 'targetKind'], 'block detail');
      return { kind, state: exactEnum(item.state, 'block state', ['active', 'withdrawn'] as const), targetKind: exactEnum(item.targetKind, 'block target kind', ['person', 'organization'] as const) };
    }
    case 'export-receipt': {
      if (!['work-record', 'network-preview'].includes(surface)) throw new Error('Export receipt surface mismatch');
      const item = closedObject(detail, ['kind', 'status', 'packageDigest', 'expiresAt'], 'export receipt');
      if (typeof item.packageDigest !== 'string' || !/^[a-f0-9]{64}$/.test(item.packageDigest)) throw new Error('Invalid export receipt digest');
      return { kind, status: exactEnum(item.status, 'export status', ['pending', 'ready', 'expired', 'failed'] as const), packageDigest: item.packageDigest, expiresAt: rfc3339(item.expiresAt, 'export expiry') };
    }
    case 'purge-receipt': {
      if (!['work-record', 'network-preview'].includes(surface)) throw new Error('Purge receipt surface mismatch');
      const item = closedObject(detail, ['kind', 'status', 'receiptId', 'stores'], 'purge receipt');
      const stores = closedStrings(item.stores, 'purged stores', 13);
      const allowedStores = new Set(['projection', 'lexical_index', 'vector_index', 'reranker_cache', 'application_cache', 'cdn', 'push_queue', 'job_queue', 'analytics', 'audit_log', 'test_fixture', 'export', 'backup_manifest']);
      if (stores.some((store) => !allowedStores.has(store))) throw new Error('Invalid purge store');
      return { kind, status: exactEnum(item.status, 'purge status', ['queued', 'completed', 'failed_escalated'] as const), receiptId: requiredString(item.receiptId, 'purge receipt id'), stores };
    }
    default:
      throw new Error('Unknown STRIDE projection detail kind');
  }
}

function parseAction(value: unknown, surface: StrideSurfaceName): StrideProjectionAction {
  const action = record(value);
  if (!action) throw new Error('Invalid STRIDE projection action');
  for (const key of Object.keys(action)) {
    if (!ACTION_KEYS.has(key)) throw new Error(`Unknown STRIDE projection action field: ${key}`);
  }
  if (!ACTION_TYPES.has(action.type as StrideProjectionActionType)
      || !Number.isSafeInteger(action.expectedRevision)
      || (action.expectedRevision as number) < 1) {
    throw new Error('Invalid STRIDE projection action authority');
  }
  const actionType = action.type as StrideProjectionActionType;
  if (ACTION_SURFACES[actionType] !== surface) {
    throw new Error('STRIDE projection action surface mismatch');
  }
  return {
    id: requiredString(action.id, 'action id'),
    type: actionType,
    label: requiredString(action.label, 'action label'),
    expectedRevision: action.expectedRevision as number,
  };
}

export function unavailableStrideSurface(
  surface: StrideSurfaceName,
  reason = 'This feature is not available for this account.',
): StrideSurfaceProjection {
  return { availability: 'unavailable', surface, reason };
}

export function parseStrideSurfaceProjection(
  expectedSurface: StrideSurfaceName,
  value: unknown,
): StrideSurfaceProjection {
  assertProjectionSafe(value);
  const envelope = record(value);
  if (!envelope || envelope.surface !== expectedSurface) {
    throw new Error('STRIDE projection surface mismatch');
  }
  const envelopeKeys = Object.keys(envelope);
  for (const key of envelopeKeys) {
    if (!ENVELOPE_KEYS.has(key)) throw new Error(`Unknown STRIDE projection field: ${key}`);
  }
  if (envelope.availability === 'unavailable') {
    if (envelopeKeys.length !== 3
        || !envelopeKeys.includes('availability')
        || !envelopeKeys.includes('surface')
        || !envelopeKeys.includes('reason')) {
      throw new Error('Invalid unavailable STRIDE projection envelope');
    }
    return unavailableStrideSurface(expectedSurface, requiredString(envelope.reason, 'reason'));
  }
  if (envelopeKeys.length !== 4
      || !envelopeKeys.includes('availability')
      || !envelopeKeys.includes('surface')
      || !envelopeKeys.includes('revision')
      || !envelopeKeys.includes('items')
      || envelope.availability !== 'available'
      || !Number.isSafeInteger(envelope.revision)
      || (envelope.revision as number) < 1
      || !Array.isArray(envelope.items)) {
    throw new Error('Invalid STRIDE projection envelope');
  }
  const items = envelope.items.map((value): StrideProjectionItem => {
    const item = record(value);
    if (!item) throw new Error('Invalid STRIDE projection item');
    for (const key of Object.keys(item)) {
      if (!ITEM_KEYS.has(key)) throw new Error(`Unknown STRIDE projection item field: ${key}`);
    }
    if ((item.kind === undefined) !== (item.detail === undefined)) {
      throw new Error('Incomplete STRIDE projection detail');
    }
    const detail = item.kind === undefined ? undefined : parseProjectionDetail(expectedSurface, item.kind, item.detail);
    return {
      id: requiredString(item.id, 'item id'),
      title: requiredString(item.title, 'item title'),
      summary: optionalString(item.summary, 'item summary'),
      status: optionalString(item.status, 'item status'),
      context: optionalString(item.context, 'item context'),
      updatedAt: optionalString(item.updatedAt, 'item updatedAt'),
      kind: detail?.kind,
      detail,
      actions: item.actions === undefined
        ? undefined
        : Array.isArray(item.actions)
          ? item.actions.map((action) => parseAction(action, expectedSurface))
          : (() => { throw new Error('Invalid STRIDE projection actions'); })(),
    };
  });
  return {
    availability: 'available',
    surface: expectedSurface,
    revision: envelope.revision as number,
    items,
  };
}
