import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import {
  parseStrideActionValues,
  parseStrideSurfaceProjection,
  unavailableStrideSurface,
} from '../stride/models';
import { bindStrideSurfaceResource, buildStrideSurfacePath } from '../stride/surfaceSelector';
import {
  sameStrideAuthority,
	strideMutationLedgerForAccount,
	StrideMutationAmbiguityError,
	StrideMutationPersistenceError,
  StrideMutationLedger,
	strideMutationAccountFingerprint,
	type StrideMutationPersistence,
  type StrideRequestAuthority,
} from '../stride/mutationAuthority';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('native stack registers every authenticated STRIDE product route', () => {
  const types = source('src', 'navigation', 'types.ts');
  const navigator = source('src', 'navigation', 'RootNavigator.tsx');
  const routes = [
    'Profile',
    'WorkRecord',
    'Organizations',
    'OrganizationPeople',
    'CoworkerProfile',
    'OrganizationRequests',
    'OrganizationRecruiting',
    'ContributionApprovals',
    'NetworkDraft',
    'NetworkPreview',
    'NetworkRecruiterView',
    'NetworkSearch',
    'ContactInbox',
    'NetworkBlocks',
  ];
  for (const route of routes) {
    if (route === 'CoworkerProfile') {
      assert.match(types, /CoworkerProfile: \{ person: string \}/);
    } else {
      assert.match(types, new RegExp(`${route}: undefined`));
    }
    assert.match(navigator, new RegExp(`<Stack\\.Screen name="${route}"`));
  }
  assert.ok(
    navigator.indexOf('<Stack.Screen name="Profile"') < navigator.indexOf('</>\n        ) : ('),
    'product routes remain inside the authenticated branch',
  );
  assert.match(navigator, /createNativeStackNavigator<RootStackParamList>/);
  assert.doesNotMatch(navigator, /createBottomTabNavigator/);
});

test('deck preserves existing destinations and adds product entry points', () => {
  const deck = source('src', 'screens', 'DeckScreen.tsx');
  for (const existing of ['Board', 'Files', 'AgentTeam', 'Alerts', 'Meetings', 'Memory', 'Intelligence', 'Settings']) {
    assert.match(deck, new RegExp(`route: '${existing}'`));
  }
  for (const added of ['Profile', 'WorkRecord', 'NetworkPreview']) {
    assert.match(deck, new RegExp(`route: '${added}'`));
  }
});

test('projection parser accepts only the closed server-authored display model', () => {
  assert.deepEqual(parseStrideSurfaceProjection('profile', {
    availability: 'available',
    surface: 'profile',
    revision: 2,
    items: [{
      id: 'person-1',
      title: 'Ada',
      summary: 'Systems lead',
      status: 'published',
      actions: [{ id: 'update-1', type: 'profile-update', label: 'Update', expectedRevision: 2 }],
    }],
  }), {
    availability: 'available',
    surface: 'profile',
    revision: 2,
    items: [{
      id: 'person-1',
      title: 'Ada',
      summary: 'Systems lead',
      status: 'published',
      context: undefined,
      updatedAt: undefined,
      kind: undefined,
      detail: undefined,
      actions: [{ id: 'update-1', type: 'profile-update', label: 'Update', expectedRevision: 2 }],
    }],
  });
  assert.deepEqual(
    unavailableStrideSurface('network-search'),
    {
      availability: 'unavailable',
      surface: 'network-search',
      reason: 'This feature is not available for this account.',
    },
  );
});

test('projection parser rejects private, authority, and ranking material', () => {
  const forbidden = [
    { email: 'private@example.com' },
    { mymind: { memory: 'private' } },
    { hidden_memberships: ['org-private'] },
    { source_body: 'raw evidence' },
    { score: 0.99 },
    { controllerPersonId: 'person-controller' },
  ];
  for (const extra of forbidden) {
    assert.throws(() => parseStrideSurfaceProjection('profile', {
      availability: 'available',
      surface: 'profile',
      revision: 1,
      items: [{ id: 'person-1', title: 'Ada', ...extra }],
    }));
  }
  assert.throws(() => parseStrideSurfaceProjection('profile', {
    availability: 'available',
    surface: 'network-search',
    revision: 1,
    items: [],
  }));
});

test('typed work, evidence, review, network, and recruiting details are closed and body-free', () => {
  const digest = 'a'.repeat(64);
  const details = [
    { surface: 'work-record' as const, kind: 'work-record-section', detail: { kind: 'work-record-section', section: 'problems-outcomes', entries: ['Reduced incident recovery time'] } },
    { surface: 'work-record' as const, kind: 'contribution-evidence', detail: { kind: 'contribution-evidence', problem: 'Slow recovery', outcome: 'Faster recovery', contribution: 'Built rollback tooling', verificationTier: 'organization_verified_redacted', releasedFields: ['outcome'], attestation: { id: 'att-1', revision: 2, digest }, publishedClaim: { id: 'pub-1', revision: 3, digest }, artifactAccess: 'redacted' } },
    { surface: 'contribution-approvals' as const, kind: 'contribution-review', detail: { kind: 'contribution-review', claim: { id: 'claim-1', revision: 4, digest }, sourceRevision: 7, sourceDigest: digest, fieldDiffs: [{ field: 'outcome', before: 'before', after: 'after', disclosureTier: 'public' }], namedPartyStates: [{ partyLabel: 'Customer', state: 'approved', required: true }], auditEntries: [{ action: 'review-opened', actorRole: 'admin', revision: 1, occurredAt: '2026-08-08T00:00:00Z' }] } },
    { surface: 'network-preview' as const, kind: 'network-state', detail: { kind: 'network-state', state: 'paused', searchableFields: ['display_name', 'verified_contributions'] } },
    { surface: 'organization-recruiting' as const, kind: 'recruiting-governance', detail: { kind: 'recruiting-governance', grantState: 'active', grantRevision: 2, expiresAt: '2026-08-09T00:00:00Z', capability: 'talent_searcher', personSearchLimit: { used: 1, limit: 10, windowEndsAt: '2026-08-09T00:00:00Z' }, organizationSearchLimit: { used: 2, limit: 100, windowEndsAt: '2026-08-09T00:00:00Z' }, globalSearchLimit: { used: 3, limit: 1000, windowEndsAt: '2026-08-09T00:00:00Z' }, personContactLimit: { used: 1, limit: 5, windowEndsAt: '2026-08-09T00:00:00Z' }, organizationContactLimit: { used: 2, limit: 50, windowEndsAt: '2026-08-09T00:00:00Z' }, globalContactLimit: { used: 3, limit: 500, windowEndsAt: '2026-08-09T00:00:00Z' }, receiptSummaries: [{ kind: 'search', verdict: 'admitted', revision: 1, occurredAt: '2026-08-08T00:00:00Z' }], auditEntries: [{ action: 'grant-issued', actorRole: 'admin', revision: 1, occurredAt: '2026-08-08T00:00:00Z' }] } },
  ];
  for (const [index, candidate] of details.entries()) {
    const parsed = parseStrideSurfaceProjection(candidate.surface, { availability: 'available', surface: candidate.surface, revision: 1, items: [{ id: `item-${index}`, title: 'Typed detail', kind: candidate.kind, detail: candidate.detail }] });
    assert.equal(parsed.availability, 'available');
    if (parsed.availability === 'available') assert.deepEqual(parsed.items[0].detail, candidate.detail);
  }
  assert.throws(() => parseStrideSurfaceProjection('work-record', { availability: 'available', surface: 'work-record', revision: 1, items: [{ id: 'bad', title: 'Bad', kind: 'contribution-evidence', detail: { ...details[1].detail, sourceBody: 'private' } }] }));
  assert.throws(() => parseStrideSurfaceProjection('network-preview', { availability: 'available', surface: 'network-preview', revision: 1, items: [{ id: 'bad', title: 'Bad', kind: 'network-state', detail: { kind: 'network-state', state: 'deleted', searchableFields: [] } }] }));
});

test('recruiting governance requires six distinct search and contact limit lanes', () => {
  const limit = { used: 1, limit: 10, windowEndsAt: '2026-08-09T00:00:00Z' };
  const wrap = (detail: object) => parseStrideSurfaceProjection('organization-recruiting', {
    availability: 'available', surface: 'organization-recruiting', revision: 1,
    items: [{ id: 'grant-1', title: 'Grant', kind: 'recruiting-governance', detail }],
  });
  const canonical = {
    kind: 'recruiting-governance', grantState: 'active', grantRevision: 1,
    expiresAt: '2026-08-09T00:00:00Z', capability: 'talent_searcher',
    personSearchLimit: limit, organizationSearchLimit: limit, globalSearchLimit: limit,
    personContactLimit: limit, organizationContactLimit: limit, globalContactLimit: limit,
    receiptSummaries: [], auditEntries: [],
  };
  assert.doesNotThrow(() => wrap(canonical));
  const { globalContactLimit: _missing, ...missingLane } = canonical;
  assert.throws(() => wrap(missingLane));
  assert.throws(() => wrap({
    kind: 'recruiting-governance', grantState: 'active', grantRevision: 1,
    expiresAt: '2026-08-09T00:00:00Z', capability: 'talent_searcher',
    personLimit: limit, organizationLimit: limit, globalLimit: limit,
    receiptSummaries: [], auditEntries: [],
  }));
  assert.throws(() => wrap({ ...canonical, personLimit: limit }));

  const screens = source('src', 'screens', 'StrideProductScreens.tsx');
  for (const field of [
    'personSearchLimit', 'organizationSearchLimit', 'globalSearchLimit',
    'personContactLimit', 'organizationContactLimit', 'globalContactLimit',
  ]) assert.match(screens, new RegExp(`detail\\.${field}`));
});

test('self, coworker, and network profile details cannot cross projection boundaries', () => {
  const projection = (surface: string, kind: string, detail: object) => ({ availability: 'available', surface, revision: 1, items: [{ id: 'profile-1', title: 'Profile', kind, detail }] });
  const self = { kind: 'self-profile-detail', displayName: 'Ada', workModes: ['remote'], openTo: [], openToEnabled: false, organizationChoices: ['Bonfire'] };
  const coworker = { kind: 'coworker-profile-detail', displayName: 'Grace', role: 'member', title: 'Engineer', team: 'Systems', joinedAt: '2026-08-08T00:00:00Z' };
  const network = { kind: 'network-profile-detail', displayName: 'Ada', openTo: ['collaboration'] };
  assert.doesNotThrow(() => parseStrideSurfaceProjection('profile', projection('profile', self.kind, self)));
  assert.doesNotThrow(() => parseStrideSurfaceProjection('coworker-profile', projection('coworker-profile', coworker.kind, coworker)));
  assert.doesNotThrow(() => parseStrideSurfaceProjection('network-preview', projection('network-preview', network.kind, network)));
  assert.throws(() => parseStrideSurfaceProjection('profile', projection('profile', coworker.kind, coworker)));
  assert.throws(() => parseStrideSurfaceProjection('coworker-profile', projection('coworker-profile', self.kind, self)));
  assert.throws(() => parseStrideSurfaceProjection('network-preview', projection('network-preview', self.kind, self)));
  assert.throws(() => parseStrideSurfaceProjection('network-preview', projection('network-preview', network.kind, { ...network, email: 'private@example.com' })));
  assert.throws(() => parseStrideSurfaceProjection('network-preview', projection('network-preview', network.kind, { ...network, openTo: [{ sourceBody: 'private' }] })));
});

test('typed details reject open enums, malformed timestamps, duplicate arrays, and unsafe state combinations', () => {
  const digest = 'b'.repeat(64);
  const wrap = (surface: string, kind: string, detail: object) => parseStrideSurfaceProjection(surface as Parameters<typeof parseStrideSurfaceProjection>[0], { availability: 'available', surface, revision: 1, items: [{ id: 'item-1', title: 'Item', kind, detail }] });
  assert.throws(() => wrap('work-record', 'contribution-evidence', { kind: 'contribution-evidence', problem: 'P', outcome: 'O', contribution: 'C', verificationTier: 'top-secret', releasedFields: ['outcome'], attestation: { id: 'a', revision: 1, digest }, publishedClaim: { id: 'p', revision: 1, digest }, artifactAccess: 'redacted' }));
  assert.throws(() => wrap('contribution-approvals', 'contribution-review', { kind: 'contribution-review', claim: { id: 'c', revision: 1, digest }, sourceRevision: 1, sourceDigest: digest, fieldDiffs: [{ field: 'outcome', before: 'a', after: 'b', disclosureTier: 'secret' }], namedPartyStates: [], auditEntries: [] }));
  assert.throws(() => wrap('organization-people', 'membership-detail', { kind: 'membership-detail', role: 'superadmin', status: 'active', isFinalOwner: false }));
  assert.throws(() => wrap('organization-requests', 'join-request-detail', { kind: 'join-request-detail', status: 'pending', expiresAt: 'tomorrow' }));
  assert.throws(() => wrap('contact-inbox', 'contact-request-detail', { kind: 'contact-request-detail', purpose: 'Discuss work', collaborationType: 'collaboration', state: 'pending', channelRevealed: true }));
  assert.doesNotThrow(() => wrap('network-blocks', 'block-detail', { kind: 'block-detail', state: 'active', targetKind: 'person' }));
  assert.doesNotThrow(() => wrap('network-blocks', 'block-detail', { kind: 'block-detail', state: 'active', targetKind: 'organization' }));
  assert.throws(() => wrap('network-blocks', 'block-detail', { kind: 'block-detail', state: 'active', targetKind: 'team' }));
  assert.throws(() => wrap('network-preview', 'purge-receipt', { kind: 'purge-receipt', status: 'completed', receiptId: 'purge-1', stores: ['projection', 'projection'] }));
  assert.throws(() => wrap('organization-recruiting', 'recruiting-governance', { kind: 'recruiting-governance', grantState: 'active', grantRevision: 1, expiresAt: '2026-08-09T00:00:00Z', capability: 'admin', personSearchLimit: { used: 1, limit: 1, windowEndsAt: '2026-08-09T00:00:00Z' }, organizationSearchLimit: { used: 1, limit: 1, windowEndsAt: '2026-08-09T00:00:00Z' }, globalSearchLimit: { used: 1, limit: 1, windowEndsAt: '2026-08-09T00:00:00Z' }, personContactLimit: { used: 1, limit: 1, windowEndsAt: '2026-08-09T00:00:00Z' }, organizationContactLimit: { used: 1, limit: 1, windowEndsAt: '2026-08-09T00:00:00Z' }, globalContactLimit: { used: 1, limit: 1, windowEndsAt: '2026-08-09T00:00:00Z' }, receiptSummaries: [], auditEntries: [] }));
});

test('API requires auth and sends no client-side authority inputs', () => {
  const api = source('src', 'stride', 'api.ts');
  assert.match(api, /if \(!token\) throw new Error\('An authenticated session is required\.'\)/);
  assert.match(api, /\{ sessionToken: token, signal \}/);
  assert.doesNotMatch(api, /membership|controller|organizationId|grantId/);
  assert.match(api, /\[403, 404, 501, 503\][\s\S]*unavailableStrideSurface/);
});

test('coworker navigation uses one selected opaque person resource and rejects cross-target selectors', () => {
  assert.equal(
    buildStrideSurfacePath('coworker-profile', { person: 'person_opaque-7' }),
    '/api/stride/v1/mobile/surfaces/coworker-profile?person=person_opaque-7',
  );
  assert.throws(() => buildStrideSurfacePath('coworker-profile'));
  assert.throws(() => buildStrideSurfacePath('coworker-profile', { person: '' }));
  assert.equal(
    buildStrideSurfacePath('coworker-profile', { person: 'person/other' }),
    '/api/stride/v1/mobile/surfaces/coworker-profile?person=person%2Fother',
  );
  assert.throws(() => buildStrideSurfacePath('coworker-profile', { person: 'person?other' }));
  assert.throws(() => buildStrideSurfacePath('profile', { person: 'person_opaque-7' }));
  assert.throws(() => buildStrideSurfacePath('coworker-profile', { person: 'person_opaque-7', organization: 'org-private' } as never));

  const selected = parseStrideSurfaceProjection('coworker-profile', {
    availability: 'available',
    surface: 'coworker-profile',
    revision: 1,
    items: [{ id: 'person_opaque-7', title: 'Selected coworker' }],
  });
  assert.equal(bindStrideSurfaceResource('coworker-profile', { person: 'person_opaque-7' }, selected), selected);
  for (const items of [
    [],
    [{ id: 'person_other', title: 'Different coworker' }],
    [{ id: 'person_opaque-7', title: 'Selected coworker' }, { id: 'person_other', title: 'Different coworker' }],
  ]) {
    const crossTarget = parseStrideSurfaceProjection('coworker-profile', {
      availability: 'available',
      surface: 'coworker-profile',
      revision: 1,
      items,
    });
    assert.throws(() => bindStrideSurfaceResource('coworker-profile', { person: 'person_opaque-7' }, crossTarget));
  }
  const opaqueUnavailable = unavailableStrideSurface('coworker-profile');
  assert.equal(bindStrideSurfaceResource('coworker-profile', { person: 'person_opaque-7' }, opaqueUnavailable), opaqueUnavailable);

  const screens = source('src', 'screens', 'StrideProductScreens.tsx');
  assert.match(screens, /navigation\.navigate\('CoworkerProfile', \{ person \}\)/);
  assert.match(screens, /useMemo\(\(\) => \(\{ person: route\.params\.person \}\), \[route\.params\.person\]\)/);
  assert.match(screens, /resourceSelector=\{resourceSelector\}/);
  assert.doesNotMatch(screens, /navigation\.navigate\('CoworkerProfile'\)/);
});

test('mutations carry exact revision and idempotency without optimistic authority', () => {
  const api = source('src', 'stride', 'api.ts');
  const screens = source('src', 'screens', 'StrideProductScreens.tsx');
  assert.match(api, /buildIdempotencyHeaders\(operationKey\)/);
  assert.match(api, /expectedRevision: action\.expectedRevision/);
  assert.match(api, /action: action\.type/);
  assert.match(api, /values: closedValues/);
  assert.match(api, /parseStrideActionValues\(action\.type, values\)/);
  assert.match(api, /if \(!operationKey\) throw new Error\('An idempotency key is required\.'\)/);
  assert.match(screens, /const nextProjection = await mutateStrideSurface/);
  assert.match(screens, /sameStrideAuthority\(authorityRef\.current, initiatingAuthority\)[\s\S]*setProjection\(nextProjection\)/);
  assert.doesNotMatch(screens, /setProjection\([^\n]*action/);
  assert.match(screens, /error instanceof BonfireApiError && error\.status === 409[\s\S]*await load\(\)/);
  assert.match(screens, /This changed elsewhere\. The latest server state has been reloaded\./);
});

test('lost-response retry reuses one operation key until settlement or discard', () => {
  const ledger = new StrideMutationLedger();
  const authority: StrideRequestAuthority = {
    sessionToken: 'session-a',
    accountKey: 'account-a',
  };
  const action = {
    id: 'action-1',
    type: 'network-search-propose' as const,
    label: 'Search',
    expectedRevision: 4,
  };
  let sequence = 0;
  const create = () => `operation-${++sequence}`;

  const first = ledger.operationKey(authority, 'network-search', action, { query: 'systems' }, create);
  const lostResponseRetry = ledger.operationKey(authority, 'network-search', action, { query: 'systems' }, create);
  assert.equal(lostResponseRetry, first);
  assert.equal(sequence, 1);

	assert.throws(
	  () => ledger.operationKey(authority, 'network-search', action, { query: 'security' }, create),
	  StrideMutationAmbiguityError,
	);
	const otherAction = { ...action, id: 'action-2' };
	assert.throws(
	  () => ledger.operationKey(authority, 'network-search', otherAction, { query: 'systems' }, create),
	  StrideMutationAmbiguityError,
	);
	assert.equal(sequence, 1);
	const retryAfterRejectedChanges = ledger.operationKey(authority, 'network-search', action, { query: 'systems' }, create);
	assert.equal(retryAfterRejectedChanges, first);

	ledger.discard();
	const afterDiscard = ledger.operationKey(authority, 'network-search', action, { query: 'security' }, create);
	assert.notEqual(afterDiscard, first);
	ledger.settle(afterDiscard);
	const afterSuccess = ledger.operationKey(authority, 'network-search', action, { query: 'security' }, create);
	assert.notEqual(afterSuccess, afterDiscard);

	const screens = source('src', 'screens', 'StrideProductScreens.tsx');
	assert.match(screens, /ambiguityFrozen=\{!ledgerReady \|\| ledgerBlocked \|\| ambiguousActionId !== null\}/);
	assert.match(screens, /editable=\{!disabled\}/);
	assert.match(screens, /ambiguityFrozen && !retryPending/);
});

test('unresolved mutation survives app restart without persisting account or session authority', async () => {
  const rows = new Map<string, string>();
  const persistence: StrideMutationPersistence = {
    read: async (key) => rows.get(key) ?? null,
    write: async (key, value) => { rows.set(key, value); },
    remove: async (key) => { rows.delete(key); },
  };
  const authority = { sessionToken: 'secret-session-token', accountKey: 'Ada@Example.com' };
  const action = {
    id: 'opaque-action-1',
    type: 'network-search-propose' as const,
    label: 'Search',
    expectedRevision: 7,
  };
  const first = new StrideMutationLedger();
  const operationKey = first.operationKey(
    authority,
    'network-search',
    action,
    { query: 'distributed systems' },
    () => 'opaque-idempotency-key',
  );
  await first.persist(authority, persistence);
  const serialized = [...rows.values()][0] ?? '';
  assert.doesNotMatch(serialized, /Ada@Example\.com|ada@example\.com|secret-session-token/);
  assert.match(serialized, new RegExp(strideMutationAccountFingerprint(authority.accountKey)));

  const restarted = new StrideMutationLedger();
  await restarted.hydrate(
    { sessionToken: 'refreshed-session-token', accountKey: 'ada@example.com' },
    persistence,
  );
  assert.deepEqual(restarted.pendingMutation(), {
    operationKey,
    actionId: 'opaque-action-1',
    actionType: 'network-search-propose',
    expectedRevision: 7,
    surface: 'network-search',
    origin: 'network-search',
    values: { query: 'distributed systems' },
  });
  const retry = restarted.operationKey(
    { sessionToken: 'another-token', accountKey: 'ada@example.com' },
    'network-search',
    action,
    { query: 'distributed systems' },
    () => 'must-not-be-used',
  );
  assert.equal(retry, operationKey);
});

test('persisted unresolved mutation isolates accounts and corrupt or unknown schemas fail closed', async () => {
  const rows = new Map<string, string>();
  const persistence: StrideMutationPersistence = {
    read: async (key) => rows.get(key) ?? null,
    write: async (key, value) => { rows.set(key, value); },
    remove: async (key) => { rows.delete(key); },
  };
  const accountA = { sessionToken: 'a', accountKey: 'a@example.com' };
  const accountB = { sessionToken: 'b', accountKey: 'b@example.com' };
  const action = { id: 'action-a', type: 'network-search-propose' as const, label: 'Search', expectedRevision: 1 };
  const ledgerA = new StrideMutationLedger();
  ledgerA.operationKey(accountA, 'network-search', action, { query: 'systems' }, () => 'key-a');
  await ledgerA.persist(accountA, persistence);
  const ledgerB = new StrideMutationLedger();
  await ledgerB.hydrate(accountB, persistence);
  assert.equal(ledgerB.hasPending(), false);

  const storedKey = [...rows.keys()][0];
  assert.ok(storedKey);
  rows.set(storedKey, JSON.stringify({ schema: 2 }));
  await assert.rejects(() => new StrideMutationLedger().hydrate(accountA, persistence), StrideMutationPersistenceError);
  rows.set(storedKey, '{not-json');
  await assert.rejects(() => new StrideMutationLedger().hydrate(accountA, persistence), StrideMutationPersistenceError);
});

test('authoritative 400 and 409 settle across restart while a lost response remains retryable', async () => {
  const makePersistence = () => {
    const rows = new Map<string, string>();
    const persistence: StrideMutationPersistence = {
      read: async (key) => rows.get(key) ?? null,
      write: async (key, value) => { rows.set(key, value); },
      remove: async (key) => { rows.delete(key); },
    };
    return { rows, persistence };
  };
  const authority = { sessionToken: 'session', accountKey: 'account@example.com' };
  const action = { id: 'action', type: 'network-search-propose' as const, label: 'Search', expectedRevision: 4 };

  for (const status of [400, 409]) {
    const { persistence } = makePersistence();
    const ledger = new StrideMutationLedger();
    const key = ledger.operationKey(authority, 'network-search', action, { query: 'systems' }, () => `key-${status}`);
    await ledger.persist(authority, persistence);
    await ledger.settlePersisted(key, authority, persistence);
    const restarted = new StrideMutationLedger();
    await restarted.hydrate(authority, persistence);
    assert.equal(restarted.hasPending(), false, `${status} must be terminal across restart`);
  }

  const { persistence } = makePersistence();
  const ambiguous = new StrideMutationLedger();
  const key = ambiguous.operationKey(authority, 'network-search', action, { query: 'systems' }, () => 'lost-response-key');
  await ambiguous.persist(authority, persistence);
  const restarted = new StrideMutationLedger();
  await restarted.hydrate({ ...authority, sessionToken: 'refreshed' }, persistence);
  assert.equal(restarted.pendingMutation()?.operationKey, key);
});

test('screen persists before mutation and exposes only exact retry, reconcile, or discard recovery', () => {
  const screens = source('src', 'screens', 'StrideProductScreens.tsx');
  const persistence = source('src', 'stride', 'mutationPersistence.ts');
  assert.match(screens, /await mutationLedger\.persist\([\s\S]*await mutateStrideSurface/);
	assert.match(screens, /await mutationLedger\.persist[\s\S]*sameStrideAuthority\(authorityRef\.current, initiatingAuthority\)[\s\S]*mutateStrideSurface/);
	assert.match(screens, /mutationAdmissionRef\.current \|\| pendingActionId/);
  assert.match(screens, /mutationLedger\.hydrate\(currentAuthority, strideMutationSecurePersistence\)/);
  assert.match(screens, /Retry unresolved operation/);
  assert.match(screens, /Reload server state/);
  assert.match(screens, /Discard unresolved operation/);
  assert.match(screens, /nextProjection\.availability === 'unavailable'[\s\S]*setAmbiguousActionId\(action\.id\)/);
	const validationBranch = screens.slice(
	  screens.indexOf('error.status === 400'),
	  screens.indexOf('error.status === 409'),
	);
	assert.match(validationBranch, /settlePersisted/);
	assert.match(validationBranch, /without changing the current view/);
	assert.doesNotMatch(validationBranch, /setProjection|await load/);
	const conflictBranch = screens.slice(
	  screens.indexOf('error.status === 409'),
	  screens.indexOf('\n      } else {', screens.indexOf('error.status === 409')),
	);
	assert.match(conflictBranch, /settlePersisted[\s\S]*await load\(\)/);
  assert.match(persistence, /SecureStore\.getItemAsync/);
  assert.match(persistence, /SecureStore\.setItemAsync/);
  assert.match(persistence, /SecureStore\.deleteItemAsync/);
  assert.doesNotMatch(persistence, /sessionToken|email|personId|membershipId|organizationId/);
});

test('account switch fences and aborts an in-flight mutation projection', () => {
  const accountA: StrideRequestAuthority = {
    sessionToken: 'session-a',
    accountKey: 'account-a',
  };
  const replacementSession: StrideRequestAuthority = {
    sessionToken: 'session-b',
    accountKey: 'account-a',
  };
  const accountB: StrideRequestAuthority = {
    sessionToken: 'session-c',
    accountKey: 'account-b',
  };
  assert.equal(sameStrideAuthority(accountA, accountA), true);
  assert.equal(sameStrideAuthority(accountA, replacementSession), false);
  assert.equal(sameStrideAuthority(accountA, accountB), false);
  assert.equal(sameStrideAuthority(accountA, null), false);

  const screens = source('src', 'screens', 'StrideProductScreens.tsx');
  const authorityChange = screens.slice(
    screens.indexOf('useLayoutEffect(() => {'),
    screens.indexOf('const openDestination'),
  );
  assert.match(authorityChange, /mutationAbortRef\.current\?\.abort\(\)/);
	assert.match(authorityChange, /const hydratedPending = mutationLedger\.pendingMutation\(\)[\s\S]*setAmbiguousActionId\(hydratedPending\?\.actionId \?\? null\)/);
	assert.doesNotMatch(authorityChange, /mutationLedger\.discard\(\)/);
  assert.match(screens, /controller\.signal\.aborted[\s\S]*!sameStrideAuthority\(authorityRef\.current, initiatingAuthority\)[\s\S]*return/);
});

test('ambiguous mutation survives back and destination navigation attempts', () => {
	const ledger = new StrideMutationLedger();
	const authority: StrideRequestAuthority = { sessionToken: 'session-a', accountKey: 'account-a' };
	const action = { id: 'action-a', type: 'network-search-propose' as const, label: 'Search', expectedRevision: 8 };
	let sequence = 0;
	const first = ledger.operationKey(authority, 'network-search', action, { query: 'systems' }, () => `operation-${++sequence}`);
	assert.equal(ledger.hasPending(), true);

	const screens = source('src', 'screens', 'StrideProductScreens.tsx');
	assert.match(screens, /navigation\.addListener\('beforeRemove',[\s\S]*mutationLedger\.hasPending\(\)[\s\S]*event\.preventDefault\(\)/);
	assert.match(screens, /const openDestination[\s\S]*ambiguousActionId && mutationLedger\.hasPending\(\)[\s\S]*return;[\s\S]*navigation\.navigate/);

	const afterBlockedNavigation = ledger.operationKey(authority, 'network-search', action, { query: 'systems' }, () => `operation-${++sequence}`);
	assert.equal(afterBlockedNavigation, first);
	assert.equal(sequence, 1);
});

test('return-to-origin is the only one-shot navigation escape while ambiguity remains', () => {
	const screens = source('src', 'screens', 'StrideProductScreens.tsx');
	assert.match(screens, /const allowPendingReturnRef = useRef\(false\)/);
	assert.match(screens, /beforeRemove[\s\S]*allowPendingReturnRef\.current[\s\S]*allowPendingReturnRef\.current = false[\s\S]*return/);
	assert.match(screens, /const openPendingSurface[\s\S]*pendingSurface === surface\) return[\s\S]*allowPendingReturnRef\.current = true[\s\S]*navigation\.navigate/);
	assert.match(screens, /queueMicrotask\(\(\) => \{ allowPendingReturnRef\.current = false; \}\)/);
});

test('account-scoped coordinator retains ambiguity through token refresh and A to B to A', () => {
	const accountA = `account-a-${Date.now()}`;
	const accountB = `account-b-${Date.now()}`;
	const ledgerA = strideMutationLedgerForAccount(accountA);
	const actionA = { id: 'action-a', type: 'network-search-propose' as const, label: 'Search', expectedRevision: 3 };
	let sequence = 0;
	const original = ledgerA.operationKey(
	  { sessionToken: 'old-session', accountKey: accountA },
	  'network-search',
	  actionA,
	  { query: 'systems' },
	  () => `operation-${++sequence}`,
	);
	const refreshed = strideMutationLedgerForAccount(accountA).operationKey(
	  { sessionToken: 'refreshed-session', accountKey: accountA },
	  'network-search',
	  actionA,
	  { query: 'systems' },
	  () => `operation-${++sequence}`,
	);
	assert.equal(refreshed, original);
	assert.deepEqual(ledgerA.pendingMutation()?.values, { query: 'systems' });

	const ledgerB = strideMutationLedgerForAccount(accountB);
	const bKey = ledgerB.operationKey(
	  { sessionToken: 'session-b', accountKey: accountB },
	  'network-search',
	  { ...actionA, id: 'action-b' },
	  { query: 'security' },
	  () => `operation-${++sequence}`,
	);
	assert.notEqual(bKey, original);
	assert.equal(strideMutationLedgerForAccount(accountA).pendingMutation()?.actionId, 'action-a');
	const returnedA = strideMutationLedgerForAccount(accountA).operationKey(
	  { sessionToken: 'newer-session', accountKey: accountA },
	  'network-search',
	  actionA,
	  { query: 'systems' },
	  () => `operation-${++sequence}`,
	);
	assert.equal(returnedA, original);
	assert.equal(sequence, 2);
	const screens = source('src', 'screens', 'StrideProductScreens.tsx');
	assert.match(screens, /useState<string \| null>\(\(\) => mutationLedger\.pendingMutation\(\)\?\.actionId \?\? null\)/);
	assert.match(screens, /const hydratedPending = mutationLedger\.pendingMutation\(\)[\s\S]*sameStrideAuthority[\s\S]*setAmbiguousActionId\(hydratedPending\?\.actionId \?\? null\)/);
	assert.match(screens, /mutationLedger\.pendingMutation\(\)\?\.surface !== surface[\s\S]*Return to unresolved action[\s\S]*Discard unresolved operation/);
	assert.match(screens, /case 'network-search': navigation\.navigate\('NetworkSearch'\)/);
	ledgerA.discard();
	ledgerB.discard();
});

test('availability envelopes are an exact discriminated union', () => {
  assert.deepEqual(parseStrideSurfaceProjection('profile', {
    availability: 'unavailable',
    surface: 'profile',
    reason: 'Default off',
  }), {
    availability: 'unavailable',
    surface: 'profile',
    reason: 'Default off',
  });
  assert.throws(() => parseStrideSurfaceProjection('profile', {
    availability: 'unavailable',
    surface: 'profile',
    reason: 'Default off',
    revision: 1,
  }));
  assert.throws(() => parseStrideSurfaceProjection('profile', {
    availability: 'unavailable',
    surface: 'profile',
    reason: 'Default off',
    items: [],
  }));
  assert.throws(() => parseStrideSurfaceProjection('profile', {
    availability: 'available',
    surface: 'profile',
    revision: 1,
    items: [],
    reason: 'Contradictory',
  }));
});

test('closed mutation values accept only the exact functional fields', () => {
  assert.deepEqual(parseStrideActionValues('profile-update', {
    displayName: 'Ada Lovelace',
    pronouns: 'she/her',
    bio: 'Systems lead',
    workModes: ['remote', 'async'],
    openTo: ['advising'],
  }), {
    displayName: 'Ada Lovelace',
    pronouns: 'she/her',
    bio: 'Systems lead',
    workModes: ['remote', 'async'],
    openTo: ['advising'],
  });
  assert.deepEqual(parseStrideActionValues('organization-create', {
    name: 'Stride Labs', slug: 'stride-labs',
  }), { name: 'Stride Labs', slug: 'stride-labs' });
	assert.deepEqual(parseStrideActionValues('organization-create', {
	  name: 'A', slug: 'a',
	}), { name: 'A', slug: 'a' });
	assert.deepEqual(parseStrideActionValues('organization-create', {
	  name: 'Consecutive', slug: 'a--b',
	}), { name: 'Consecutive', slug: 'a--b' });
	const longestSlug = `a${'-'.repeat(61)}b`;
	assert.equal(longestSlug.length, 63);
	assert.deepEqual(parseStrideActionValues('organization-create', {
	  name: 'Longest', slug: longestSlug,
	}), { name: 'Longest', slug: longestSlug });
  assert.deepEqual(parseStrideActionValues('organization-join', {
    joinCode: 'invite-123',
  }), { joinCode: 'invite-123' });
  assert.deepEqual(parseStrideActionValues('network-draft-save', {
    intro: 'I build reliable systems.', workModes: ['hybrid'], openTo: ['collaboration'],
  }), { intro: 'I build reliable systems.', workModes: ['hybrid'], openTo: ['collaboration'] });
  assert.deepEqual(parseStrideActionValues('network-search-propose', {
    query: 'distributed systems',
  }), { query: 'distributed systems' });
  assert.deepEqual(parseStrideActionValues('network-search-confirm', {}), {});
  assert.deepEqual(parseStrideActionValues('contact-send', {
    purpose: 'Discuss a project', note: 'Would you be open to talking?', collaborationType: 'collaboration',
  }), { purpose: 'Discuss a project', note: 'Would you be open to talking?', collaborationType: 'collaboration' });
  assert.deepEqual(parseStrideActionValues('exact-link-contact-send', {
    purpose: 'Discuss this exact profile', collaborationType: 'advisory',
  }), { purpose: 'Discuss this exact profile', collaborationType: 'advisory' });
  assert.deepEqual(parseStrideActionValues('contribution-subject-dispute', {
    reason: 'The dates are incorrect.',
  }), { reason: 'The dates are incorrect.' });
  assert.deepEqual(parseStrideActionValues('organization-member-role-change', { role: 'admin' }), { role: 'admin' });
	assert.deepEqual(parseStrideActionValues('organization-member-revoke', {}), {});
  assert.deepEqual(parseStrideActionValues('organization-ownership-transfer', {}), {});
  assert.deepEqual(parseStrideActionValues('contribution-correct', { reason: 'Correct the outcome.' }), { reason: 'Correct the outcome.' });
  assert.deepEqual(parseStrideActionValues('contribution-named-party-decision', { decision: 'approved', reason: 'Confirmed.' }), { decision: 'approved', reason: 'Confirmed.' });
  assert.deepEqual(parseStrideActionValues('contribution-named-party-decision', { decision: 'denied' }), { decision: 'denied' });
  assert.deepEqual(parseStrideActionValues('contribution-attestation-revoke', { reason: 'Issuer revoked the attestation.' }), { reason: 'Issuer revoked the attestation.' });
  assert.deepEqual(parseStrideActionValues('contribution-attestation-revoke', {}), {});
  assert.deepEqual(parseStrideActionValues('network-searchable-fields-update', { fields: ['display_name', 'verified_contributions'] }), { fields: ['display_name', 'verified_contributions'] });
  assert.deepEqual(parseStrideActionValues('network-block', {}), {});
	assert.deepEqual(parseStrideActionValues('network-profile-off', {}), {});
});

test('mutation values reject unknown, private, authority, malformed, and oversized input', () => {
  for (const forbidden of [
    { displayName: 'Ada', email: 'private@example.com' },
    { displayName: 'Ada', controllerPersonId: 'person-1' },
    { displayName: 'Ada', membershipId: 'member-1' },
    { displayName: 'Ada', sourceBody: 'private evidence' },
    { displayName: 'Ada', values: { bio: 'nested' } },
  ]) {
    assert.throws(() => parseStrideActionValues('profile-update', forbidden));
  }
  assert.throws(() => parseStrideActionValues('profile-update', {}));
  assert.throws(() => parseStrideActionValues('profile-update', { displayName: 'x'.repeat(81) }));
  assert.throws(() => parseStrideActionValues('profile-update', { displayName: 'Ada', workModes: 'remote' }));
  assert.throws(() => parseStrideActionValues('profile-update', {
    displayName: 'Ada', openTo: Array.from({ length: 21 }, (_, index) => `item-${index}`),
  }));
  assert.throws(() => parseStrideActionValues('organization-create', { name: 'Stride', slug: 'Not Valid' }));
	assert.throws(() => parseStrideActionValues('organization-create', { name: 'Stride', slug: '-stride' }));
	assert.throws(() => parseStrideActionValues('organization-create', { name: 'Stride', slug: 'stride-' }));
	assert.throws(() => parseStrideActionValues('organization-create', { name: 'Stride', slug: `a${'-'.repeat(62)}b` }));
  assert.throws(() => parseStrideActionValues('network-search-propose', { query: '' }));
  assert.throws(() => parseStrideActionValues('network-search-propose', { query: 'x'.repeat(241) }));
  assert.throws(() => parseStrideActionValues('contact-send', {
    purpose: 'Hello', collaborationType: 42,
  }));
  assert.throws(() => parseStrideActionValues('contact-send', {
    purpose: 'Hello', collaborationType: 'contract',
  }));
  assert.throws(() => parseStrideActionValues('network-search-confirm', { query: 'client-controlled' }));
  assert.throws(() => parseStrideActionValues('exact-link-contact-send', {
    purpose: 'Hello', collaborationType: 'advisory', profileId: 'profile-1',
  }));
  assert.throws(() => parseStrideActionValues('network-block', { reason: 'not allowed' }));
  assert.throws(() => parseStrideActionValues('organization-member-role-change', { role: 'owner' }));
  assert.deepEqual(parseStrideActionValues('contribution-revoke', { reason: '' }), { reason: '' });
  assert.throws(() => parseStrideActionValues('contribution-named-party-decision', {}));
  assert.throws(() => parseStrideActionValues('contribution-named-party-decision', { decision: 'withdrawn' }));
  assert.throws(() => parseStrideActionValues('contribution-named-party-decision', { decision: 'approved', reason: 'x'.repeat(501) }));
  assert.throws(() => parseStrideActionValues('contribution-named-party-decision', { decision: 'approved', partyId: 'party-1' }));
  assert.throws(() => parseStrideActionValues('contribution-attestation-revoke', { attestationId: 'attestation-1' }));
  assert.throws(() => parseStrideActionValues('contribution-attestation-revoke', { reason: 'x'.repeat(501) }));
  assert.throws(() => parseStrideActionValues('network-searchable-fields-update', { fields: ['email'] }));
  assert.throws(() => parseStrideActionValues('organization-ownership-transfer', { membershipId: 'member-1' }));
});

test('closed action vocabulary covers organization, publication, contact, and block decisions', () => {
  const models = source('src', 'stride', 'models.ts');
  for (const action of [
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
  ]) {
    assert.match(models, new RegExp(`'${action}'`));
  }
  assert.throws(() => parseStrideSurfaceProjection('profile', {
    availability: 'available',
    surface: 'profile',
    revision: 1,
    items: [{
      id: 'person-1',
      title: 'Ada',
      actions: [{ id: 'bad', type: 'make-admin', label: 'Escalate', expectedRevision: 1 }],
    }],
  }));
  assert.throws(() => parseStrideSurfaceProjection('profile', {
    availability: 'available',
    surface: 'profile',
    revision: 1,
    items: [{
      id: 'person-1',
      title: 'Ada',
      actions: [{ id: 'stale', type: 'profile-update', label: 'Update', expectedRevision: 0 }],
    }],
  }));
});

test('named-party and attestation actions are server-minted only on contribution approvals', () => {
  const item = (type: 'contribution-named-party-decision' | 'contribution-attestation-revoke') => ({
    id: `item-${type}`,
    title: 'Governed contribution',
    actions: [{ id: `action-${type}`, type, label: 'Review', expectedRevision: 2 }],
  });
  for (const type of ['contribution-named-party-decision', 'contribution-attestation-revoke'] as const) {
    const projection = parseStrideSurfaceProjection('contribution-approvals', {
      availability: 'available', surface: 'contribution-approvals', revision: 2, items: [item(type)],
    });
    assert.equal(projection.availability, 'available');
    assert.throws(() => parseStrideSurfaceProjection('work-record', {
      availability: 'available', surface: 'work-record', revision: 2, items: [item(type)],
    }));
  }
  const screens = source('src', 'screens', 'StrideProductScreens.tsx');
  assert.match(screens, /contribution-named-party-decision[\s\S]*label="Decision"[\s\S]*approved or denied/);
  assert.match(screens, /contribution-attestation-revoke/);
});

test('profile off and member revoke are server-minted one-tap actions on exact surfaces only', () => {
  assert.deepEqual(parseStrideActionValues('network-profile-off', {}), {});
  assert.deepEqual(parseStrideActionValues('organization-member-revoke', {}), {});
  assert.throws(() => parseStrideActionValues('network-profile-off', { reason: 'private' }));
  assert.throws(() => parseStrideActionValues('organization-member-revoke', { membershipId: 'member-1' }));

  const available = (surface: 'network-preview' | 'organization-people', action: string) => ({
    availability: 'available',
    surface,
    revision: 2,
    items: [{
      id: 'opaque-resource',
      title: 'Authorized row',
      actions: [{ id: 'opaque-action', type: action, label: 'Apply', expectedRevision: 2 }],
    }],
  });
  assert.doesNotThrow(() => parseStrideSurfaceProjection('network-preview', available('network-preview', 'network-profile-off')));
  assert.doesNotThrow(() => parseStrideSurfaceProjection('organization-people', available('organization-people', 'organization-member-revoke')));
  assert.throws(() => parseStrideSurfaceProjection('organization-people', {
    ...available('network-preview', 'network-profile-off'), surface: 'organization-people',
  }));
  assert.throws(() => parseStrideSurfaceProjection('network-preview', {
    ...available('organization-people', 'organization-member-revoke'), surface: 'network-preview',
  }));
});

test('subject and organization contribution decisions are fenced to distinct surfaces', () => {
  const action = (type: string) => ({
    id: `action-${type}`,
    type,
    label: 'Decide',
    expectedRevision: 3,
  });
  const projection = (surface: string, type: string) => ({
    availability: 'available',
    surface,
    revision: 3,
    items: [{ id: 'claim-1', title: 'Contribution', actions: [action(type)] }],
  });

  assert.doesNotThrow(() => parseStrideSurfaceProjection(
    'work-record',
    projection('work-record', 'contribution-subject-approve'),
  ));
  assert.doesNotThrow(() => parseStrideSurfaceProjection(
    'work-record',
    projection('work-record', 'contribution-subject-dispute'),
  ));
  assert.throws(() => parseStrideSurfaceProjection(
    'contribution-approvals',
    projection('contribution-approvals', 'contribution-subject-approve'),
  ));
  assert.throws(() => parseStrideSurfaceProjection(
    'contribution-approvals',
    projection('contribution-approvals', 'contribution-subject-dispute'),
  ));

  assert.doesNotThrow(() => parseStrideSurfaceProjection(
    'contribution-approvals',
    projection('contribution-approvals', 'contribution-organization-approve'),
  ));
  assert.doesNotThrow(() => parseStrideSurfaceProjection(
    'contribution-approvals',
    projection('contribution-approvals', 'contribution-organization-deny'),
  ));
  assert.throws(() => parseStrideSurfaceProjection(
    'work-record',
    projection('work-record', 'contribution-organization-approve'),
  ));
});

test('private network draft is distinct from publication and search surfaces', () => {
  const types = source('src', 'navigation', 'types.ts');
  const navigator = source('src', 'navigation', 'RootNavigator.tsx');
  const screens = source('src', 'screens', 'StrideProductScreens.tsx');
  const api = source('src', 'stride', 'api.ts');
  const selector = source('src', 'stride', 'surfaceSelector.ts');
  assert.match(types, /NetworkDraft: undefined/);
  assert.match(navigator, /<Stack\.Screen name="NetworkDraft" component=\{NetworkDraftScreen\}/);
  assert.match(screens, /NetworkDraftScreen[\s\S]*surface="network-draft"/);
  assert.match(screens, /NetworkPreviewScreen[\s\S]*surface="network-preview"/);
  assert.match(screens, /NetworkRecruiterViewScreen[\s\S]*surface="network-recruiter-view"/);
  assert.match(api, /buildStrideSurfacePath\(surface, selector\)/);
  assert.match(selector, /surfaces\/\$\{encodeURIComponent\(surface\)\}/);
  assert.doesNotMatch(api, /network-draft[\s\S]*(network-publish|network-search)/);
  assert.ok(
    screens.indexOf("surface=\"network-draft\"") < screens.indexOf("surface=\"network-preview\""),
    'draft remains its own work-record surface before published preview',
  );
});

test('screens use native-safe scrolling, Pressable, and guarded text rendering', () => {
  const screens = source('src', 'screens', 'StrideProductScreens.tsx');
  assert.match(screens, /contentInsetAdjustmentBehavior="automatic"/);
  assert.match(screens, /<FlatList/);
  assert.doesNotMatch(screens, /<ScrollView/);
  assert.doesNotMatch(screens, /projection\.items\.map/);
  assert.match(screens, /<Pressable/);
  assert.match(screens, /<TextInput/);
  assert.match(screens, /actionType === 'profile-update'/);
  assert.match(screens, /actionType === 'organization-create'/);
  assert.match(screens, /actionType === 'organization-join'/);
  assert.match(screens, /actionType === 'network-draft-save'/);
  assert.match(screens, /actionType === 'network-search-propose'/);
  assert.match(screens, /actionType === 'contact-send' \|\| actionType === 'exact-link-contact-send'/);
  assert.match(screens, /actionSupportsReason\(actionType\)/);
  assert.ok(
    screens.indexOf("for (const action of item.actions ?? [])") < screens.indexOf('<ActionRow'),
    'forms render only from actions carried by the server projection',
  );
  assert.match(screens, /borderCurve: 'continuous'/);
  assert.match(screens, /const ProjectionRow = memo/);
  assert.match(screens, /const DestinationRow = memo/);
  assert.match(screens, /const ActionRow = memo/);
  assert.match(screens, /const openDestination = useCallback/);
  assert.doesNotMatch(screens, /\{[^}\n]+ && </);
});
