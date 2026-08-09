import React, {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { FlatList, Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp, NativeStackScreenProps } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import type { RootStackParamList } from '../navigation/types';
import { BonfireApiError } from '../api/client';
import {
  createStrideOperationKey,
  loadStrideSurface,
  mutateStrideSurface,
  type StrideSurfaceResourceSelector,
} from '../stride/api';
import {
  unavailableStrideSurface,
  parseStrideActionValues,
  type StrideProjectionItem,
  type StrideProjectionAction,
  type StrideProjectionActionType,
  type StrideProjectionDetail,
  type StrideActionValues,
  type StrideSurfaceName,
  type StrideSurfaceProjection,
} from '../stride/models';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import {
  sameStrideAuthority,
	StrideMutationAmbiguityError,
	StrideMutationPersistenceError,
	strideMutationLedgerForAccount,
  type StrideRequestAuthority,
} from '../stride/mutationAuthority';
import { strideMutationSecurePersistence } from '../stride/mutationPersistence';

type StrideNav = NativeStackNavigationProp<RootStackParamList>;

type Destination = {
  label: string;
  hint: string;
  route: keyof Pick<RootStackParamList,
    | 'Organizations'
    | 'OrganizationPeople'
    | 'OrganizationRequests'
    | 'OrganizationRecruiting'
    | 'ContributionApprovals'
    | 'NetworkDraft'
    | 'NetworkRecruiterView'
    | 'NetworkSearch'
    | 'ContactInbox'
    | 'NetworkBlocks'>;
};

type SurfaceRow =
  | ({ kind: 'destination'; key: string } & Destination)
  | ({ kind: 'projection'; key: string; resourceId: string } & Omit<StrideProjectionItem, 'id' | 'actions' | 'kind'>)
  | ({ kind: 'action'; key: string } & StrideProjectionAction);

const WORK_DESTINATIONS: Destination[] = [
  { route: 'Organizations', label: 'Organizations', hint: 'Your server-authorized organization view' },
  { route: 'OrganizationPeople', label: 'People', hint: 'Visible members in the active organization' },
  { route: 'OrganizationRequests', label: 'Requests', hint: 'Join requests you are allowed to review' },
  { route: 'ContributionApprovals', label: 'Contribution approvals', hint: 'Contribution claims awaiting your decision' },
  { route: 'OrganizationRecruiting', label: 'Organization recruiting', hint: 'Current grants, limits, receipts, and audit' },
  { route: 'NetworkDraft', label: 'Network draft', hint: 'Private draft fields before publication' },
];

const NETWORK_DESTINATIONS: Destination[] = [
  { route: 'NetworkRecruiterView', label: 'View as recruiter', hint: 'The exact published projection recruiters can see' },
  { route: 'NetworkSearch', label: 'Search', hint: 'Search only when a current grant is installed' },
  { route: 'ContactInbox', label: 'Contact inbox', hint: 'Purpose-bound contact requests' },
  { route: 'NetworkBlocks', label: 'Blocked people', hint: 'Manage network blocks without exposing private data' },
];

const ProjectionRow = memo(function ProjectionRow({
  title,
  summary,
  status,
  context,
  updatedAt,
  detail,
}: Omit<StrideProjectionItem, 'id' | 'kind'>) {
  const detailLines = useMemo(() => strideDetailLines(detail), [detail]);
  return (
    <View style={styles.card}>
      <View style={styles.rowHeading}>
        <Text style={styles.rowTitle}>{title}</Text>
        {status ? <Text style={styles.status}>{status}</Text> : null}
      </View>
      {summary ? <Text style={styles.summary}>{summary}</Text> : null}
      {context ? <Text style={styles.context}>{context}</Text> : null}
      {detailLines.map((line) => <Text key={line} style={styles.context}>{line}</Text>)}
      {updatedAt ? <Text style={styles.timestamp}>{updatedAt}</Text> : null}
    </View>
  );
});

const CoworkerProjectionRow = memo(function CoworkerProjectionRow({
  resourceId,
  onOpen,
  ...projection
}: Omit<StrideProjectionItem, 'id' | 'kind' | 'actions'> & {
  resourceId: string;
  onOpen: (person: string) => void;
}) {
  const open = useCallback(() => onOpen(resourceId), [onOpen, resourceId]);
  return (
    <Pressable accessibilityRole="button" accessibilityLabel={`Open ${projection.title}`} onPress={open} style={({ pressed }) => pressed ? styles.pressed : null}>
      <ProjectionRow {...projection} />
    </Pressable>
  );
});

function strideDetailLines(detail: StrideProjectionDetail | undefined): string[] {
  if (!detail) return [];
  switch (detail.kind) {
    case 'self-profile-detail': return [`${detail.displayName}${detail.pronouns ? ` · ${detail.pronouns}` : ''}`, ...(detail.bio ? [detail.bio] : []), `Work modes: ${detail.workModes.join(', ') || 'none'}`, `Open to: ${detail.openToEnabled ? detail.openTo.join(', ') || 'enabled' : 'off'}`, `Organizations: ${detail.organizationChoices.join(', ') || 'none'}`];
    case 'coworker-profile-detail': return [`${detail.displayName} · ${detail.role}`, ...(detail.title ? [`Title: ${detail.title}`] : []), ...(detail.team ? [`Team: ${detail.team}`] : []), `Joined: ${detail.joinedAt}`];
    case 'network-profile-detail': return [
      ...(detail.displayName ? [`Display name: ${detail.displayName}`] : []),
      ...(detail.pronouns ? [`Pronouns: ${detail.pronouns}`] : []),
      ...(detail.bio ? [`Bio: ${detail.bio}`] : []),
      ...(detail.visibleOrganizations ? [`Organizations: ${detail.visibleOrganizations.join(', ')}`] : []),
      ...(detail.workModes ? [`Work modes: ${detail.workModes.join(', ')}`] : []),
      ...(detail.openTo ? [`Open to: ${detail.openTo.join(', ')}`] : []),
    ];
    case 'work-record-section': return [`${detail.section}: ${detail.entries.join(' · ')}${detail.section === 'open-to' ? ` · ${detail.openToEnabled ? 'enabled' : 'off'}` : ''}`];
    case 'contribution-evidence': return [
      `Problem: ${detail.problem}`,
      `Outcome: ${detail.outcome}`,
      `Contribution: ${detail.contribution}`,
      `Verification: ${detail.verificationTier} · released ${detail.releasedFields.join(', ')}`,
      `Attestation ${detail.attestation.id} r${detail.attestation.revision} ${detail.attestation.digest} · published claim ${detail.publishedClaim.id} r${detail.publishedClaim.revision} ${detail.publishedClaim.digest}`,
      `Artifact: ${detail.artifactAccess}${detail.reviewedInfluence ? ` · reviewed influence: ${detail.reviewedInfluence}` : ''}`,
    ];
    case 'contribution-review': return [
      `Claim ${detail.claim.id} r${detail.claim.revision} ${detail.claim.digest} · source r${detail.sourceRevision} ${detail.sourceDigest}`,
      ...detail.fieldDiffs.map((entry) => `Change ${entry.field} (${entry.disclosureTier}): ${entry.before} → ${entry.after}`),
      ...detail.namedPartyStates.map((entry) => `Party ${entry.partyLabel}: ${entry.state}${entry.required ? ' · required' : ''}`),
      ...detail.auditEntries.map((entry) => `Audit r${entry.revision}: ${entry.action} by ${entry.actorRole} · ${entry.occurredAt}`),
    ];
    case 'network-state': return [`Network: ${detail.state}`, `Searchable fields: ${detail.searchableFields.length ? detail.searchableFields.join(', ') : 'none'}`];
    case 'recruiting-governance': return [
      `Grant: ${detail.grantState} · revision ${detail.grantRevision} · expires ${detail.expiresAt}`,
      `Capability: ${detail.capability}`,
      `Search limits: person ${detail.personSearchLimit.used}/${detail.personSearchLimit.limit} · organization ${detail.organizationSearchLimit.used}/${detail.organizationSearchLimit.limit} · global ${detail.globalSearchLimit.used}/${detail.globalSearchLimit.limit}`,
      `Contact limits: person ${detail.personContactLimit.used}/${detail.personContactLimit.limit} · organization ${detail.organizationContactLimit.used}/${detail.organizationContactLimit.limit} · global ${detail.globalContactLimit.used}/${detail.globalContactLimit.limit}`,
      ...detail.receiptSummaries.map((entry) => `Receipt r${entry.revision}: ${entry.kind} ${entry.verdict} · ${entry.occurredAt}`),
      ...detail.auditEntries.map((entry) => `Audit r${entry.revision}: ${entry.action} by ${entry.actorRole} · ${entry.occurredAt}`),
    ];
    case 'organization-summary': return [`Members: ${detail.activeCount}/${detail.capacity} · pending ${detail.pendingCount}`, `${detail.role}${detail.isCurrent ? ' · current organization' : ''}`];
    case 'membership-detail': return [`${detail.role} · ${detail.status}${detail.isFinalOwner ? ' · final owner' : ''}`];
    case 'join-request-detail': return [`${detail.status} · expires ${detail.expiresAt}`];
    case 'network-query-interpretation': return [`Verdict: ${detail.verdict}`, `Filters: ${detail.filters.join(', ') || 'none'}`];
    case 'network-search-result': return [...detail.why.map((value) => `Why: ${value}`), ...detail.unknown.map((value) => `Unknown: ${value}`), `Verification: ${detail.verificationLabels.join(', ') || 'none'}`, ...detail.publishedRefs.map((ref) => `Published ${ref.id} r${ref.revision} ${ref.digest}`)];
    case 'contact-request-detail': return [`${detail.purpose} · ${detail.collaborationType} · ${detail.state}`, `Contact channel: ${detail.channelRevealed ? 'revealed after acceptance' : 'hidden'}`];
    case 'block-detail': return [`${detail.state} · ${detail.targetKind}`];
    case 'export-receipt': return [`Export ${detail.status} · expires ${detail.expiresAt}`, `Package ${detail.packageDigest}`];
    case 'purge-receipt': return [`Purge ${detail.status} · receipt ${detail.receiptId}`, `Stores: ${detail.stores.join(', ') || 'none'}`];
  }
}

const DestinationRow = memo(function DestinationRow({
  label,
  hint,
  route,
  onOpen,
}: {
  label: string;
  hint: string;
  route: Destination['route'];
  onOpen: (route: Destination['route']) => void;
}) {
  const open = useCallback(() => onOpen(route), [route, onOpen]);
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityHint={hint}
      onPress={open}
      style={({ pressed }) => [styles.destination, pressed ? styles.pressed : null]}
    >
      <View style={styles.destinationCopy}>
        <Text style={styles.rowTitle}>{label}</Text>
        <Text style={styles.context}>{hint}</Text>
      </View>
      <Text style={styles.chevron}>›</Text>
    </Pressable>
  );
});

const ActionRow = memo(function ActionRow({
  id,
  actionType,
  label,
  expectedRevision,
  pending,
  retryPending,
	ambiguityFrozen,
	retryValues,
  onAction,
  onDiscard,
}: {
  id: string;
  actionType: StrideProjectionActionType;
  label: string;
  expectedRevision: number;
  pending: boolean;
  retryPending: boolean;
  ambiguityFrozen: boolean;
	retryValues?: StrideActionValues;
  onAction: (action: StrideProjectionAction, values: StrideActionValues) => void;
  onDiscard: () => void;
}) {
	const initialString = useCallback((key: string) => {
	  const value = retryValues?.[key];
	  return typeof value === 'string' ? value : '';
	}, [retryValues]);
	const initialList = useCallback((key: string) => {
	  const value = retryValues?.[key];
	  return Array.isArray(value) ? value.join(', ') : '';
	}, [retryValues]);
  const [displayName, setDisplayName] = useState(() => initialString('displayName'));
  const [pronouns, setPronouns] = useState(() => initialString('pronouns'));
  const [bio, setBio] = useState(() => initialString('bio'));
  const [name, setName] = useState(() => initialString('name'));
  const [slug, setSlug] = useState(() => initialString('slug'));
  const [joinCode, setJoinCode] = useState(() => initialString('joinCode'));
  const [intro, setIntro] = useState(() => initialString('intro'));
  const [workModes, setWorkModes] = useState(() => initialList('workModes'));
  const [openTo, setOpenTo] = useState(() => initialList('openTo'));
  const [query, setQuery] = useState(() => initialString('query'));
  const [purpose, setPurpose] = useState(() => initialString('purpose'));
  const [note, setNote] = useState(() => initialString('note'));
  const [collaborationType, setCollaborationType] = useState(() => initialString('collaborationType'));
  const [reason, setReason] = useState(() => initialString('reason'));
  const [decision, setDecision] = useState(() => initialString('decision'));
  const [role, setRole] = useState(() => initialString('role'));
  const [fields, setFields] = useState(() => initialList('fields'));

  const values = useMemo<StrideActionValues>(() => {
    switch (actionType) {
      case 'profile-update':
        return { displayName, pronouns, bio, workModes: splitList(workModes), openTo: splitList(openTo) } as StrideActionValues;
      case 'organization-create':
        return { name, slug } as StrideActionValues;
      case 'organization-join':
        return { joinCode } as StrideActionValues;
      case 'organization-member-role-change':
        return { role } as StrideActionValues;
      case 'network-searchable-fields-update':
        return { fields: splitList(fields) } as StrideActionValues;
      case 'network-draft-save':
        return { intro, workModes: splitList(workModes), openTo: splitList(openTo) } as StrideActionValues;
      case 'network-search-propose':
        return { query } as StrideActionValues;
      case 'contact-send':
      case 'exact-link-contact-send':
        return { purpose, note, collaborationType } as StrideActionValues;
      case 'contribution-named-party-decision':
        return reason.trim() ? { decision, reason } : { decision };
      default:
        return actionSupportsReason(actionType) && reason.trim() ? { reason } : {};
    }
  }, [
    actionType,
    bio,
    collaborationType,
    decision,
    displayName,
    fields,
    intro,
    joinCode,
    name,
    note,
    openTo,
    pronouns,
    purpose,
    query,
    reason,
    role,
    slug,
    workModes,
  ]);
  const press = useCallback(() => {
    onAction({ id, type: actionType, label, expectedRevision }, values);
  }, [actionType, expectedRevision, id, label, onAction, values]);
  return (
    <View style={styles.actionForm}>
      {actionType === 'profile-update' ? (
        <>
		  <FormField disabled={ambiguityFrozen} label="Display name" value={displayName} onChangeText={setDisplayName} maxLength={80} />
		  <FormField disabled={ambiguityFrozen} label="Pronouns" value={pronouns} onChangeText={setPronouns} maxLength={40} />
		  <FormField disabled={ambiguityFrozen} label="Bio" value={bio} onChangeText={setBio} maxLength={280} multiline />
		  <FormField disabled={ambiguityFrozen} label="Work modes" hint="Separate entries with commas" value={workModes} onChangeText={setWorkModes} maxLength={1299} />
		  <FormField disabled={ambiguityFrozen} label="Open to" hint="Separate entries with commas" value={openTo} onChangeText={setOpenTo} maxLength={1299} />
        </>
      ) : null}
      {actionType === 'organization-create' ? (
        <>
		  <FormField disabled={ambiguityFrozen} label="Organization name" value={name} onChangeText={setName} maxLength={120} />
		  <FormField disabled={ambiguityFrozen} label="Slug" value={slug} onChangeText={setSlug} maxLength={63} autoCapitalize="none" />
        </>
      ) : null}
      {actionType === 'organization-join' ? (
		<FormField disabled={ambiguityFrozen} label="Join code" value={joinCode} onChangeText={setJoinCode} maxLength={128} autoCapitalize="none" />
      ) : null}
      {actionType === 'organization-member-role-change' ? (
		<FormField disabled={ambiguityFrozen} label="Role" hint="member or admin" value={role} onChangeText={setRole} maxLength={16} autoCapitalize="none" />
      ) : null}
      {actionType === 'network-searchable-fields-update' ? (
		<FormField disabled={ambiguityFrozen} label="Searchable fields" hint="Use exact public field keys, separated with commas" value={fields} onChangeText={setFields} maxLength={1299} autoCapitalize="none" />
      ) : null}
      {actionType === 'network-draft-save' ? (
        <>
		  <FormField disabled={ambiguityFrozen} label="Introduction" value={intro} onChangeText={setIntro} maxLength={280} multiline />
		  <FormField disabled={ambiguityFrozen} label="Work modes" hint="Separate entries with commas" value={workModes} onChangeText={setWorkModes} maxLength={1299} />
		  <FormField disabled={ambiguityFrozen} label="Open to" hint="Separate entries with commas" value={openTo} onChangeText={setOpenTo} maxLength={1299} />
        </>
      ) : null}
      {actionType === 'network-search-propose' ? (
		<FormField disabled={ambiguityFrozen} label="What kind of work do you need help with?" value={query} onChangeText={setQuery} maxLength={240} multiline />
      ) : null}
      {actionType === 'contact-send' || actionType === 'exact-link-contact-send' ? (
        <>
		  <FormField disabled={ambiguityFrozen} label="Purpose" value={purpose} onChangeText={setPurpose} maxLength={80} />
		  <FormField disabled={ambiguityFrozen} label="Collaboration type" hint="collaboration, advisory, employment, recruiting, or organization_join" value={collaborationType} onChangeText={setCollaborationType} maxLength={32} autoCapitalize="none" />
		  <FormField disabled={ambiguityFrozen} label="Note" value={note} onChangeText={setNote} maxLength={1000} multiline />
        </>
      ) : null}
      {actionType === 'contribution-named-party-decision' ? (
        <FormField disabled={ambiguityFrozen} label="Decision" hint="approved or denied" value={decision} onChangeText={setDecision} maxLength={16} autoCapitalize="none" />
      ) : null}
      {actionSupportsReason(actionType) ? (
		<FormField disabled={ambiguityFrozen} label="Reason (optional)" value={reason} onChangeText={setReason} maxLength={500} multiline />
      ) : null}
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={label}
		accessibilityState={{ busy: pending, disabled: pending || (ambiguityFrozen && !retryPending) }}
		disabled={pending || (ambiguityFrozen && !retryPending)}
        onPress={press}
        style={({ pressed }) => [styles.action, pressed ? styles.pressed : null]}
      >
        <Text style={styles.actionLabel}>{pending ? 'Working…' : label}</Text>
      </Pressable>
      {retryPending ? (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Discard pending retry"
          onPress={onDiscard}
          style={({ pressed }) => [styles.discardAction, pressed ? styles.pressed : null]}
        >
          <Text style={styles.discardActionLabel}>Discard pending retry</Text>
        </Pressable>
      ) : null}
    </View>
  );
});

const FormField = memo(function FormField({
	disabled,
  label,
  hint,
  value,
  onChangeText,
  maxLength,
  multiline = false,
  autoCapitalize = 'sentences',
}: {
	disabled: boolean;
  label: string;
  hint?: string;
  value: string;
  onChangeText: (value: string) => void;
  maxLength: number;
  multiline?: boolean;
  autoCapitalize?: 'none' | 'sentences';
}) {
  return (
    <View style={styles.field}>
      <Text style={styles.fieldLabel}>{label}</Text>
      {hint ? <Text style={styles.fieldHint}>{hint}</Text> : null}
      <TextInput
		accessibilityLabel={label}
		editable={!disabled}
        value={value}
        onChangeText={onChangeText}
        maxLength={maxLength}
        multiline={multiline}
        autoCapitalize={autoCapitalize}
        autoCorrect={autoCapitalize !== 'none'}
		style={[styles.input, disabled ? styles.inputDisabled : null, multiline ? styles.inputMultiline : null]}
      />
    </View>
  );
});

function splitList(value: string): string[] {
  return value.split(',').map((item) => item.trim()).filter((item) => item !== '');
}

function actionSupportsReason(actionType: StrideProjectionActionType): boolean {
  return [
    'organization-request-approve',
    'organization-request-deny',
    'contribution-subject-approve',
    'contribution-subject-dispute',
    'contribution-organization-approve',
    'contribution-organization-deny',
    'contribution-correct',
    'contribution-revoke',
    'contribution-named-party-decision',
    'contribution-attestation-revoke',
    'organization-recruiting-grant-revoke',
    'contact-accept',
    'contact-decline',
    'contact-withdraw',
  ].includes(actionType);
}

function StrideSurfaceScreen({
  title,
  subtitle,
  surface,
  destinations = [],
  resourceSelector,
}: {
  title: string;
  subtitle: string;
  surface: StrideSurfaceName;
  destinations?: Destination[];
  resourceSelector?: StrideSurfaceResourceSelector;
}) {
  const navigation = useNavigation<StrideNav>();
  const { user, sessionToken } = useAuth();
  const accountKey = user?.email.trim().toLowerCase() ?? '';
  const currentAuthority = useMemo<StrideRequestAuthority | null>(() =>
    sessionToken && accountKey ? { sessionToken, accountKey } : null,
  [accountKey, sessionToken]);
  const authorityRef = useRef<StrideRequestAuthority | null>(currentAuthority);
	const mutationLedger = useMemo(() => strideMutationLedgerForAccount(accountKey), [accountKey]);
  const mutationAbortRef = useRef<AbortController | null>(null);
  const mutationAdmissionRef = useRef<symbol | null>(null);
  const allowPendingReturnRef = useRef(false);
  const [projection, setProjection] = useState<StrideSurfaceProjection>(() =>
    unavailableStrideSurface(surface, 'Checking availability…'));
  const [loading, setLoading] = useState(true);
  const [pendingActionId, setPendingActionId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
	const [ambiguousActionId, setAmbiguousActionId] = useState<string | null>(() => mutationLedger.pendingMutation()?.actionId ?? null);
	const [ledgerReady, setLedgerReady] = useState(false);
	const [ledgerBlocked, setLedgerBlocked] = useState(false);

	useEffect(() => {
	  let current = true;
	  if (!currentAuthority) {
		setLedgerReady(false);
		setLedgerBlocked(false);
		return () => { current = false; };
	  }
	  setLedgerReady(false);
	  void mutationLedger.hydrate(currentAuthority, strideMutationSecurePersistence).then(() => {
		if (!current) return;
		setLedgerBlocked(false);
		setAmbiguousActionId(mutationLedger.pendingMutation()?.actionId ?? null);
		setLedgerReady(true);
	  }).catch(() => {
		if (!current) return;
		setLedgerBlocked(true);
		setLedgerReady(true);
		setActionError('The stored unresolved operation could not be verified. Discard it before continuing.');
	  });
	  return () => { current = false; };
	}, [currentAuthority, mutationLedger]);

  useLayoutEffect(() => {
	const hydratedPending = mutationLedger.pendingMutation();
	if (sameStrideAuthority(authorityRef.current, currentAuthority)) {
	  setAmbiguousActionId(hydratedPending?.actionId ?? null);
	  return;
	}
    authorityRef.current = currentAuthority;
    mutationAbortRef.current?.abort();
    mutationAbortRef.current = null;
    mutationAdmissionRef.current = null;
    setPendingActionId(null);
	setAmbiguousActionId(hydratedPending?.actionId ?? null);
    setActionError(null);
	}, [currentAuthority, mutationLedger]);

	useEffect(() => navigation.addListener('beforeRemove', (event) => {
	  if (allowPendingReturnRef.current) {
		allowPendingReturnRef.current = false;
		return;
	  }
	  if (!ledgerBlocked && (!ambiguousActionId || !mutationLedger.hasPending())) return;
	  event.preventDefault();
	  setActionError('Retry the unresolved operation or discard it before leaving this screen.');
	}), [ambiguousActionId, ledgerBlocked, mutationLedger, navigation]);

  const openDestination = useCallback((route: Destination['route']) => {
	if (ledgerBlocked || (ambiguousActionId && mutationLedger.hasPending())) {
	  setActionError('Retry the unresolved operation or discard it before opening another screen.');
	  return;
	}
    navigation.navigate(route);
	}, [ambiguousActionId, ledgerBlocked, mutationLedger, navigation]);

	const openPendingSurface = useCallback(() => {
	  const pendingSurface = mutationLedger.pendingMutation()?.surface;
	  if (!pendingSurface || pendingSurface === surface) return;
	  allowPendingReturnRef.current = true;
	  switch (pendingSurface) {
		case 'profile': navigation.navigate('Profile'); break;
		case 'work-record': navigation.navigate('WorkRecord'); break;
		case 'organizations': navigation.navigate('Organizations'); break;
		case 'organization-people': navigation.navigate('OrganizationPeople'); break;
		case 'organization-requests': navigation.navigate('OrganizationRequests'); break;
		case 'organization-recruiting': navigation.navigate('OrganizationRecruiting'); break;
		case 'contribution-approvals': navigation.navigate('ContributionApprovals'); break;
		case 'network-draft': navigation.navigate('NetworkDraft'); break;
		case 'network-preview': navigation.navigate('NetworkPreview'); break;
		case 'network-recruiter-view': navigation.navigate('NetworkRecruiterView'); break;
		case 'network-search': navigation.navigate('NetworkSearch'); break;
		case 'contact-inbox': navigation.navigate('ContactInbox'); break;
		case 'network-blocks': navigation.navigate('NetworkBlocks'); break;
		default: allowPendingReturnRef.current = false; break;
	  }
	  queueMicrotask(() => { allowPendingReturnRef.current = false; });
	}, [mutationLedger, navigation, surface]);

  const load = useCallback(async (signal?: AbortSignal) => {
    const initiatingAuthority = currentAuthority;
    if (!initiatingAuthority) return;
    setLoading(true);
    try {
      const nextProjection = await loadStrideSurface(
        initiatingAuthority.sessionToken,
        surface,
        signal,
        resourceSelector,
      );
      if (!signal?.aborted && sameStrideAuthority(authorityRef.current, initiatingAuthority)) {
        setProjection(nextProjection);
      }
    } catch {
      if (!signal?.aborted && sameStrideAuthority(authorityRef.current, initiatingAuthority)) {
        setProjection(unavailableStrideSurface(surface, 'This surface could not be loaded safely.'));
      }
    } finally {
      if (!signal?.aborted && sameStrideAuthority(authorityRef.current, initiatingAuthority)) {
        setLoading(false);
      }
    }
  }, [currentAuthority, resourceSelector, surface]);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const runAction = useCallback(async (
    action: StrideProjectionAction,
    values: StrideActionValues,
  ) => {
    const initiatingAuthority = currentAuthority;
    if (mutationAdmissionRef.current || pendingActionId || !initiatingAuthority || !ledgerReady) return;
    const attempt = Symbol(action.id);
    mutationAdmissionRef.current = attempt;
    let closedValues: StrideActionValues;
    try {
      closedValues = parseStrideActionValues(action.type, values);
    } catch {
      setActionError('Check the highlighted action fields and try again.');
	  if (mutationAdmissionRef.current === attempt) mutationAdmissionRef.current = null;
      return;
    }
	let operationKey: string;
	try {
	  operationKey = mutationLedger.operationKey(
		initiatingAuthority,
		surface,
		action,
		closedValues,
		() => createStrideOperationKey(surface, action.id),
	  );
	  await mutationLedger.persist(initiatingAuthority, strideMutationSecurePersistence);
	} catch (error) {
	  if (error instanceof StrideMutationAmbiguityError) {
		setActionError('Discard the unresolved retry before changing its action or fields.');
		if (mutationAdmissionRef.current === attempt) mutationAdmissionRef.current = null;
		return;
	  }
	  if (!sameStrideAuthority(authorityRef.current, initiatingAuthority)) {
		if (mutationAdmissionRef.current === attempt) mutationAdmissionRef.current = null;
		return;
	  }
	  if (error instanceof StrideMutationPersistenceError || error instanceof Error) {
		setLedgerBlocked(true);
		setAmbiguousActionId(mutationLedger.pendingMutation()?.actionId ?? action.id);
		setActionError('The unresolved operation could not be saved safely. Retry storage or discard before continuing.');
		if (mutationAdmissionRef.current === attempt) mutationAdmissionRef.current = null;
		return;
	  }
	  if (mutationAdmissionRef.current === attempt) mutationAdmissionRef.current = null;
	  return;
	}
    const controller = new AbortController();
    mutationAbortRef.current = controller;
	if (!sameStrideAuthority(authorityRef.current, initiatingAuthority)) {
	  controller.abort();
	  if (mutationAbortRef.current === controller) mutationAbortRef.current = null;
	  if (mutationAdmissionRef.current === attempt) mutationAdmissionRef.current = null;
	  return;
	}
	setLedgerBlocked(false);
    setPendingActionId(action.id);
    setActionError(null);
    try {
      const nextProjection = await mutateStrideSurface(
        initiatingAuthority.sessionToken,
        surface,
        action,
        closedValues,
        operationKey,
        controller.signal,
      );
      if (controller.signal.aborted
          || !sameStrideAuthority(authorityRef.current, initiatingAuthority)) return;
	  if (nextProjection.availability === 'unavailable') {
		setProjection(nextProjection);
		setAmbiguousActionId(action.id);
		setActionError('The result was not confirmed. Retry to safely reuse the same operation.');
		return;
	  }
	  await mutationLedger.settlePersisted(operationKey, initiatingAuthority, strideMutationSecurePersistence);
      setAmbiguousActionId(null);
      setProjection(nextProjection);
    } catch (error) {
      if (controller.signal.aborted
          || !sameStrideAuthority(authorityRef.current, initiatingAuthority)) return;
      if (error instanceof BonfireApiError && error.status === 400) {
		try {
		  await mutationLedger.settlePersisted(operationKey, initiatingAuthority, strideMutationSecurePersistence);
		} catch {
		  setLedgerBlocked(true);
		  setAmbiguousActionId(action.id);
		  setActionError('The confirmed validation response could not be saved safely. Retry or discard the unresolved operation.');
		  return;
		}
		setAmbiguousActionId(null);
		setActionError('Check the action fields. The server rejected this form without changing the current view.');
	  } else if (error instanceof BonfireApiError && error.status === 409) {
		try {
		  await mutationLedger.settlePersisted(operationKey, initiatingAuthority, strideMutationSecurePersistence);
		} catch {
		  setLedgerBlocked(true);
		  setAmbiguousActionId(action.id);
		  setActionError('The conflict response could not be saved safely. Retry or discard the unresolved operation.');
		  return;
		}
        setAmbiguousActionId(null);
        setActionError('This changed elsewhere. The latest server state has been reloaded.');
        await load();
      } else {
        setAmbiguousActionId(action.id);
        setActionError('The result was not confirmed. Retry to safely reuse the same operation.');
      }
    } finally {
      if (mutationAbortRef.current === controller) mutationAbortRef.current = null;
	  if (mutationAdmissionRef.current === attempt) mutationAdmissionRef.current = null;
      if (sameStrideAuthority(authorityRef.current, initiatingAuthority)) {
        setPendingActionId(null);
      }
    }
	}, [currentAuthority, ledgerBlocked, ledgerReady, load, mutationLedger, pendingActionId, surface]);

  const discardPendingRetry = useCallback(() => {
	const authority = currentAuthority;
	if (!authority) return;
	void mutationLedger.discardPersisted(authority, strideMutationSecurePersistence).then(() => {
	  if (!sameStrideAuthority(authorityRef.current, authority)) return;
	  setAmbiguousActionId(null);
	  setLedgerBlocked(false);
	  setActionError(null);
	}).catch(() => {
	  if (!sameStrideAuthority(authorityRef.current, authority)) return;
	  setLedgerBlocked(true);
	  setActionError('The unresolved operation could not be discarded safely.');
	});
	}, [currentAuthority, mutationLedger]);

	const retryPersistedMutation = useCallback(() => {
	  const pending = mutationLedger.pendingMutation();
	  if (!pending || pending.surface !== surface) return;
	  void runAction({
		id: pending.actionId,
		type: pending.actionType,
		label: 'Retry unresolved operation',
		expectedRevision: pending.expectedRevision,
	  }, pending.values);
	}, [mutationLedger, runAction, surface]);

	const reconcilePendingMutation = useCallback(() => {
	  void load().then(() => {
		setActionError('The latest server state was reloaded. Retry the exact operation or discard it.');
	  });
	}, [load]);

  const rows = useMemo<SurfaceRow[]>(() => {
    const next: SurfaceRow[] = destinations.map((destination) => ({
      kind: 'destination',
      key: `destination:${destination.route}`,
      ...destination,
    }));
    if (projection.availability !== 'available') return next;
    for (const item of projection.items) {
      next.push({
        kind: 'projection',
		key: `projection:${accountKey}:${item.id}`,
        resourceId: item.id,
        title: item.title,
        summary: item.summary,
        status: item.status,
        context: item.context,
        updatedAt: item.updatedAt,
        detail: item.detail,
      });
      for (const action of item.actions ?? []) {
		next.push({ kind: 'action', key: `action:${accountKey}:${action.id}`, ...action });
      }
    }
    return next;
  }, [accountKey, destinations, projection]);

  const openCoworkerProfile = useCallback((person: string) => {
    if (ambiguousActionId && mutationLedger.hasPending()) {
      setActionError('Retry the unresolved operation or discard it before opening another screen.');
      return;
    }
    navigation.navigate('CoworkerProfile', { person });
  }, [ambiguousActionId, mutationLedger, navigation]);

  const renderRow = useCallback(({ item }: { item: SurfaceRow }) => {
    if (item.kind === 'destination') {
      return <DestinationRow label={item.label} hint={item.hint} route={item.route} onOpen={openDestination} />;
    }
    if (item.kind === 'action') {
      return (
        <ActionRow
          id={item.id}
          actionType={item.type}
          label={item.label}
          expectedRevision={item.expectedRevision}
          pending={pendingActionId === item.id}
          retryPending={ambiguousActionId === item.id}
		  ambiguityFrozen={!ledgerReady || ledgerBlocked || ambiguousActionId !== null}
		  retryValues={ambiguousActionId === item.id ? mutationLedger.pendingMutation()?.values : undefined}
          onAction={runAction}
          onDiscard={discardPendingRetry}
        />
      );
    }
    if (surface === 'organization-people') {
      return (
        <CoworkerProjectionRow
          resourceId={item.resourceId}
          title={item.title}
          summary={item.summary}
          status={item.status}
          context={item.context}
          updatedAt={item.updatedAt}
          detail={item.detail}
          onOpen={openCoworkerProfile}
        />
      );
    }
    return (
      <ProjectionRow
        title={item.title}
        summary={item.summary}
        status={item.status}
        context={item.context}
        updatedAt={item.updatedAt}
        detail={item.detail}
      />
    );
	}, [ambiguousActionId, discardPendingRetry, ledgerBlocked, ledgerReady, mutationLedger, openCoworkerProfile, openDestination, pendingActionId, runAction, surface]);

  const header = useMemo(() => (
    <View style={styles.header}>
      <Text style={styles.title}>{title}</Text>
      <Text style={styles.subtitle}>{subtitle}</Text>
      {loading ? <Text style={styles.notice}>Checking availability…</Text> : null}
      {actionError ? <Text style={styles.error}>{actionError}</Text> : null}
	  {ambiguousActionId || ledgerBlocked ? (
		<View style={styles.pendingRecovery}>
		  {ambiguousActionId && mutationLedger.pendingMutation()?.surface === surface ? (
			<>
			  <Pressable accessibilityRole="button" accessibilityLabel="Retry unresolved operation" onPress={retryPersistedMutation} style={({ pressed }) => [styles.discardAction, pressed ? styles.pressed : null]}>
				<Text style={styles.discardActionLabel}>Retry unresolved operation</Text>
			  </Pressable>
			  <Pressable accessibilityRole="button" accessibilityLabel="Reconcile unresolved operation" onPress={reconcilePendingMutation} style={({ pressed }) => [styles.discardAction, pressed ? styles.pressed : null]}>
				<Text style={styles.discardActionLabel}>Reload server state</Text>
			  </Pressable>
			</>
		  ) : null}
		  {mutationLedger.pendingMutation()?.surface !== surface ? (
			<Pressable accessibilityRole="button" accessibilityLabel="Return to unresolved action" onPress={openPendingSurface} style={({ pressed }) => [styles.discardAction, pressed ? styles.pressed : null]}>
			  <Text style={styles.discardActionLabel}>Return to unresolved action</Text>
			</Pressable>
		  ) : null}
		  <Pressable
			accessibilityRole="button"
			accessibilityLabel="Discard unresolved operation"
			onPress={discardPendingRetry}
			style={({ pressed }) => [styles.discardAction, pressed ? styles.pressed : null]}
		  >
			<Text style={styles.discardActionLabel}>Discard unresolved operation</Text>
		  </Pressable>
		</View>
	  ) : null}
      {!loading && projection.availability === 'unavailable' ? (
        <View style={styles.unavailable}>
          <Text style={styles.unavailableTitle}>Unavailable</Text>
          <Text style={styles.context}>{projection.reason}</Text>
        </View>
      ) : null}
    </View>
	), [actionError, ambiguousActionId, discardPendingRetry, ledgerBlocked, loading, mutationLedger, openPendingSurface, projection, reconcilePendingMutation, retryPersistedMutation, subtitle, surface, title]);

  const empty = !loading && projection.availability === 'available'
    ? <Text style={styles.notice}>Nothing is published here yet.</Text>
    : null;

  return (
    <FlatList
      style={styles.screen}
      contentContainerStyle={styles.content}
      contentInsetAdjustmentBehavior="automatic"
      showsVerticalScrollIndicator={false}
      data={rows}
      keyExtractor={keyExtractor}
      renderItem={renderRow}
      ListHeaderComponent={header}
      ListEmptyComponent={empty}
      ItemSeparatorComponent={RowSeparator}
    />
  );
}

function keyExtractor(item: SurfaceRow): string {
  return item.key;
}

function RowSeparator() {
  return <View style={styles.separator} />;
}

export const ProfileScreen = () => <StrideSurfaceScreen title="Profile" subtitle="Your public identity and current profile projection" surface="profile" />;
export const WorkRecordScreen = () => <StrideSurfaceScreen title="Work record" subtitle="Your verified contribution history" surface="work-record" destinations={WORK_DESTINATIONS} />;
export const OrganizationsScreen = () => <StrideSurfaceScreen title="Organizations" subtitle="Organizations the current session is allowed to see" surface="organizations" />;
export const OrganizationPeopleScreen = () => <StrideSurfaceScreen title="People" subtitle="Visible people in the active organization" surface="organization-people" />;
type CoworkerProfileProps = NativeStackScreenProps<RootStackParamList, 'CoworkerProfile'>;

export const CoworkerProfileScreen = ({ route }: CoworkerProfileProps) => {
  const resourceSelector = useMemo(() => ({ person: route.params.person }), [route.params.person]);
  return (
    <StrideSurfaceScreen
      title="Coworker profile"
      subtitle="Only the organization-authorized coworker detail projection"
      surface="coworker-profile"
      resourceSelector={resourceSelector}
    />
  );
};
export const OrganizationRequestsScreen = () => <StrideSurfaceScreen title="Requests" subtitle="Current organization join requests" surface="organization-requests" />;
export const OrganizationRecruitingScreen = () => <StrideSurfaceScreen title="Organization recruiting" subtitle="Current recruiting grants, limits, receipts, and audit" surface="organization-recruiting" />;
export const ContributionApprovalsScreen = () => <StrideSurfaceScreen title="Contribution approvals" subtitle="Claims the server says you may review" surface="contribution-approvals" />;
export const NetworkDraftScreen = () => <StrideSurfaceScreen title="Network draft" subtitle="Your private work-record draft, independent of publication and search" surface="network-draft" />;
export const NetworkPreviewScreen = () => <StrideSurfaceScreen title="Network preview" subtitle="Your published network presence" surface="network-preview" destinations={NETWORK_DESTINATIONS} />;
export const NetworkRecruiterViewScreen = () => <StrideSurfaceScreen title="View as recruiter" subtitle="Only the fields in your active published projection" surface="network-recruiter-view" />;
export const NetworkSearchScreen = () => <StrideSurfaceScreen title="Network search" subtitle="Available only with a current purpose-bound search grant" surface="network-search" />;
export const ContactInboxScreen = () => <StrideSurfaceScreen title="Contact inbox" subtitle="No contact channel is shown before recipient acceptance" surface="contact-inbox" />;
export const NetworkBlocksScreen = () => <StrideSurfaceScreen title="Blocked people" subtitle="Blocks immediately fence matching results and contacts" surface="network-blocks" />;

const styles = StyleSheet.create({
  screen: { flex: 1, backgroundColor: colors.bgApp },
  content: { padding: space[5], paddingBottom: space[12] },
  header: { gap: space[4], marginBottom: space[4] },
  title: { ...type.title1, color: colors.text1 },
  subtitle: { ...type.body, color: colors.text2 },
  section: { gap: space[3] },
  separator: { height: space[3] },
  destination: {
    minHeight: hitMin,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    padding: space[4],
    borderRadius: radius.lg,
    borderCurve: 'continuous',
    backgroundColor: colors.surface1,
  },
  destinationCopy: { flex: 1, gap: space[1] },
  pressed: { opacity: 0.72 },
  chevron: { ...type.title2, color: colors.text3 },
  card: {
    gap: space[2],
    padding: space[4],
    borderRadius: radius.lg,
    borderCurve: 'continuous',
    backgroundColor: colors.surface1,
  },
  rowHeading: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  rowTitle: { ...type.bodyMedium, flex: 1, color: colors.text1 },
  status: { ...type.label, color: colors.emberText, textTransform: 'uppercase' },
  summary: { ...type.body, color: colors.text1 },
  context: { ...type.caption, color: colors.text2 },
  timestamp: { ...type.label, color: colors.text3 },
  notice: { ...type.bodySm, color: colors.text2, textAlign: 'center', padding: space[5] },
  unavailable: {
    gap: space[2],
    padding: space[5],
    borderRadius: radius.lg,
    borderCurve: 'continuous',
    backgroundColor: colors.surface3,
  },
  unavailableTitle: { ...type.headline, color: colors.text1 },
  action: {
    minHeight: hitMin,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: space[4],
    borderRadius: radius.md,
    borderCurve: 'continuous',
    backgroundColor: colors.accent,
  },
  actionForm: {
    gap: space[3],
    padding: space[4],
    borderRadius: radius.lg,
    borderCurve: 'continuous',
    backgroundColor: colors.surface1,
  },
  field: { gap: space[1] },
  fieldLabel: { ...type.captionMedium, color: colors.text1 },
  fieldHint: { ...type.caption, color: colors.text2 },
  input: {
    minHeight: hitMin,
    paddingHorizontal: space[3],
    paddingVertical: space[2],
    borderWidth: 1,
    borderColor: colors.line1,
    borderRadius: radius.md,
    borderCurve: 'continuous',
    backgroundColor: colors.bgApp,
    color: colors.text1,
    ...type.body,
  },
	inputDisabled: { opacity: 0.55 },
	pendingRecovery: { gap: 8 },
  inputMultiline: { minHeight: 96, textAlignVertical: 'top' },
  actionLabel: { ...type.button, color: colors.onAccent },
  discardAction: {
    minHeight: hitMin,
    alignItems: 'center',
    justifyContent: 'center',
  },
  discardActionLabel: { ...type.button, color: colors.text2 },
  error: { ...type.bodySm, color: colors.danger },
});
