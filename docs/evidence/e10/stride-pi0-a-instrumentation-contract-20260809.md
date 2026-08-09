# STRIDE PI0-A body-minimized lifecycle instrumentation contract

Status: `draft_revised_waiting_independent_recritic`

Observed: 2026-08-09

This document freezes the PI0-A instrumentation contract and inventories the
current implementation. It does not authorize collection, a migration, a
provider, a runtime install, feature activation, Git action, release, or
production mutation. It does not change the canonical E10 plan.

## 1. Purpose and boundary

PI0-A may measure whether authorized work moves from an authorized source to a
human-visible, reviewed outcome. It must not measure whether a person is busy,
productive, compliant, engaged, loyal, talented, promotable, or psychologically
suited to work.

PI0-A records no passive observation and no “no work happened” fact. A source
enters this ledger only when a current authorized operation binds that exact
source revision into a newly admitted lifecycle trace. Merely opening, reading,
speaking, receiving, or storing a conversation or transcript creates no PI0-A
event.

The lifecycle in scope is:

1. an authorized conversation, chat, transcript, or explicit user instruction;
2. a Suggested Work candidate and human-visible card;
3. card revision, endorsement, approval, dismissal, or expiry;
4. an authorized run, including requested human intervention;
5. an artifact and its revision lineage;
6. review and deterministic or human verification;
7. an explicitly approved Work Record contribution;
8. an explicitly approved publication or collaboration transition.

The event ledger is operational metadata, not product truth. Product contracts
remain authoritative for current state. An event may point to an exact product
revision; it may never recreate or widen that product revision.

## 2. Non-negotiable prohibitions

PI0-A must never collect or derive:

- raw message, transcript, prompt, response, artifact, file, note, feedback, or
  MyMind bodies;
- email addresses, names, raw session tokens, passkeys, IP addresses, precise
  device identifiers, cursor paths, keystrokes, mouse movement, window focus,
  camera/microphone presence, location, or continuous activity duration;
- sentiment, personality, emotion, psychographics, political or protected-class
  inference, health inference, or relationship-strength inference;
- person, worker, agent, team, or organization productivity scores, rankings,
  quotas, leaderboards, comparative performance, or promotion/termination
  recommendations;
- hidden joins between usage/cost telemetry and a person or Work Record;
- evidence created by timestamp proximity, matching display text, matching
  names, or a model's unsupported inference;
- public trace identifiers or commitments that enable enumeration or
  dictionary recovery of private sources.

Unknown is a first-class result. Missing evidence is never success, zero, or
negative evidence about a person.

## 3. Canonical event envelope

Every PI0-A event uses schema `stride.pi0.lifecycle-event.v1`. Unknown fields,
unknown event types, missing required bindings, duplicate array entries, invalid
RFC3339Nano timestamps, or non-canonical JSON fail closed.

| Field | Contract |
|---|---|
| `eventId` | Opaque server-minted identifier. Stable on idempotent replay. |
| `eventType` | One exact closed value from section 5. |
| `tenantId` | Opaque current organization tenant. Never inferred from email or a singleton. |
| `aggregate` | `{type,id,revision,digest}`; exact current or historical product contract revision. |
| `traceId` | Opaque private lifecycle trace. Server-minted at the first admitted source-to-work edge. |
| `parentEventId` | Exact immediate predecessor when one exists. No timestamp-derived parentage. |
| `causationRefs` | One to 16 exact `{type,id,revision,digest}` references. Sorted and unique. |
| `principal` | `{personId,organizationId,membershipId,membershipRevision,sessionSubjectDigest,sessionRevision}` from current canonical authority. All fields required for a human action; body-free service events instead carry exact service/controller revision. |
| `subjectRefs` | Zero to 16 exact opaque person, organization, work, artifact, contribution, publication, or contact references. No names or contact data. |
| `audience` | Exact closed STRIDE audience plus `aclVersion`; copied from the authorized destination, never caller-expanded. |
| `consentRefs` | Exact consent snapshot/revision/digest references required by the source or disclosure. Empty only for event types explicitly marked not applicable. |
| `policyRefs` | Exact feature, retention, disclosure, approval, and execution policy revision/digest references used for the decision. |
| `provenance` | Closed `direct_human`, `deterministic_system`, `model_assisted`, `tool_result`, `provider_import`, or `legacy_import`; model-assisted events also bind model, prompt, configuration, and evidence-manifest digests without bodies. |
| `idempotencyDigest` | Managed HMAC commitment to the closed operation tuple under domain `pi0/idempotency/v1`. Raw or plain SHA commitments to low-entropy text are forbidden. |
| `sourceDigest` | Managed HMAC commitment to the canonical ordered source-reference manifest under domain `pi0/source-manifest/v1`, not source bodies. |
| `outputDigest` | Managed HMAC commitment to the exact output revision or closed state transition under domain `pi0/output/v1`, not output bodies. |
| `occurredAt` | When the source action occurred. |
| `effectiveAt` | When the state/authority change became effective. Required even when equal to `occurredAt`. |
| `recordedAt` | Append time. It may not precede `occurredAt`; latency is data quality, not human performance. |
| `quality` | Closed object from section 8. |
| `revocation` | `{generation,refs}` when authority, consent, evidence, publication, grant, or membership has changed; otherwise explicit `null`. |
| `purge` | `{generation,receiptRefs}` when derived state is fenced or deleted; otherwise explicit `null`. |
| `retention` | `{class,policyRef,retainUntil}` using section 9. |

No event contains a free-form `body`, `text`, `reason`, `summary`, `title`,
`query`, `prompt`, `response`, `note`, `label`, or arbitrary metadata map.
Human explanations remain in their private authorized product object; the event
stores only that object's exact revision and digest.

Every commitment over a private body, private body-derived value, low-entropy
value, label, closed reason code, source/output manifest, or idempotency tuple is
produced by a managed keyring with an explicit key ID/version and a distinct
domain string. Domains are non-interchangeable. Public projection commitments
use independently managed public keys and cannot verify, correlate, or enumerate
private values. Plain SHA, unkeyed deterministic digests, and reuse of state-MAC,
encryption, trace, export, or purge keys are forbidden.

### 3.1 Principal and service rules

- Human events on the initial path must be minted inside the current session
  and organization membership callback held through the product mutation and
  event append. A restart repair may append only an event envelope already
  sealed in the pre-effect journal under the recovery-only contract below; it
  is not a new human admission.
- Service events require an action-bound managed authority envelope and exact
  controller generation. A worker name or model name is not authority.
- If product mutation succeeds but event append does not, the operation must be
  recoverable from one durable operation journal and the same idempotency
  digest. It may not repeat the product effect.
- If the event append succeeds but the product effect does not, the event is an
  explicit `*.failed` or recovery state, never a success event.

### 3.2 Authenticated compound-operation journal

Every operation that can change product state and append one or more PI0-A
events has one durable, managed-MAC journal written before the first effect.
The closed phases are:

`prepared -> effect_requested -> effect_approved -> effect_applied ->`
`events_written -> postimages_verified -> committed`

Failure/recovery branches are:

`effect_failed -> reconciled -> committed` or
`recovery_required -> effect_reconciled -> events_reconciled ->`
`postimages_verified -> committed`.

The journal binds exactly:

- opaque `operationId`, schema version, tenant, trace, aggregate and initiating
  current principal/controller tuple;
- `operationFingerprint`: managed commitment under
  `pi0/compound-operation/v1` to the exact action, prior revisions, closed
  values, policy/consent/ACL revisions and idempotency key;
- ordered requested event IDs/types and their canonical envelope commitments;
- exact preimages and expected product/event postimages as typed
  `{store,type,id,revision,digest,highWater}` entries;
- effect provider/adapter identity, its journal-bound idempotent operation/
  receipt identity, managed evidence key ID/version, and current authority-
  envelope digest, all persisted before `effect_requested`;
- a managed-MAC `committedEffectPostimage` written as part of the same
  compare-and-swap transition that records `effect_applied`; it binds the exact
  authoritative effect bytes/digests/high-water, initiating authority image,
  ordered precommitted event envelopes, and recovery capability purpose;
- current phase, phase generation, key ID/version and phase receipt digest;
- actual independently read product/event postimages and high-waters; and
- terminal reconciliation class `applied`, `not_applied`, `compensated`, or
  `quarantined`, never inferred from a timeout.

Each phase transition is compare-and-swap, append-receipted, and MAC verified.
Recovery reads the authoritative destination before deciding whether to retry,
append missing events, compensate, or quarantine. Lost response after an effect
must reconcile the exact expected postimage and must never repeat the effect.
Changed fingerprint or postimage is conflict, not replay. Recovery emits the
closed events `effect.reconciled` and/or `lifecycle.reconciled` and verifies the
same final product and event postimages before `committed`. A process restart at
every phase resumes deterministically. There is no file-ahead or event-ahead
success state outside this journal.

The `effect_requested`/`effect_approved` crash window has an exact recovery
protocol. Before invoking the effect, the prepared journal already contains the
expected authoritative postimage, original authority image, ordered event
envelopes, and the effect adapter's exact operation/receipt identity. On
restart, the recovery worker reads both the authoritative destination and the
adapter's independently authenticated idempotency receipt:

1. If both prove the exact expected postimage and matching operation identity,
   one journal CAS creates `committedEffectPostimage` from those independently
   read bytes and advances to `effect_applied`; the effect is not invoked again.
2. If both prove `not_applied`, the effect may be retried only through the
   original idempotent operation and only after current controller/session/
   membership authority is newly resolved. Revoked authority denies the retry
   and records a body-free terminal denial; it never manufactures an applied
   event.
3. If destination and receipt are missing, partial, contradictory, unavailable,
   or do not exactly match the expected postimage, recovery fences any derived
   visibility and moves to `quarantined`. It does not append success events,
   compensate, guess, or invoke the effect.

The CAS in case 1 is the only recovery path allowed to establish a committed
seal after a crash between external effect commit and the ordinary
`effect_applied` transition. Its proof inputs and resulting seal are retained
in the journal receipt and independently reverified before event repair.

The recovery worker has one server-owned, purpose-bound
`pi0/repair-precommitted-events/v1` capability. It may do exactly three things:
verify the journal MAC and `committedEffectPostimage`; verify that the product
destination equals that exact committed postimage; and append the exact ordered
precommitted event envelopes whose IDs/digests are missing. It may not resolve
or impersonate the initiating human, change an envelope, mint a new event,
rerun/compensate the product effect, broaden audience/retention, or return
private data. A revoked controller/session after effect commit therefore does
not strand ledger consistency: repair completes the already committed compound
postimage exactly once, installs any required authority fence/purge first, and
commits the journal. Only after repair does the request/replay/read path
independently resolve current human authority; stale or revoked callers receive
the normal opaque denial and no event body or result projection.

Required crash negative: product effect commits and its postimage is sealed;
event append is lost; the controller and active session are revoked; the
process restarts. Recovery must install the current fence/purge, append each
precommitted event exactly once without repeating the effect, verify both
postimages, and commit. An exact caller replay is then denied under current
authority. Changed journal/event bytes, missing recovery purpose, stale product
postimage, or a second append attempt fails closed with no new effect/event.

Required pre-seal crash negative: product effect commits, but the process dies
before the `effect_applied` CAS and before `committedEffectPostimage` exists;
then the controller and active session are revoked. Restart must use the exact
pre-effect operation identity, authenticated adapter receipt, and independently
read destination to prove the expected postimage, atomically create the seal,
install the current fence/purge, append the precommitted events once, and commit
without invoking the effect. Caller replay is denied. A missing/mismatched
receipt, partial destination, changed expected bytes, or second seal attempt
must quarantine with zero new effect or event.

## 4. Trace graph and edge rules

One `traceId` describes one admitted work lifecycle. It is private to the
tenant. A source can cause multiple work traces; each trace must cite the same
exact source revision rather than share an inferred edge.

The only valid forward edge order is:

`source -> intent -> suggestion -> approval -> run -> artifact -> review ->`
`verification -> Work Record -> authorized publication/collaboration`

Optional branches are dismissal/expiry before approval, intervention during a
run, failure/cancellation before artifact completion, correction/revocation
after Work Record creation, and withdrawal/pause/off/delete after publication.

Rules:

- `parentEventId` is single-parent process order. `causationRefs` carries the
  many-source evidence graph.
- Every transition binds the prior aggregate revision and the resulting
  aggregate revision. Revision gaps are invalid except a separately receipted
  `legacy_import` baseline.
- An artifact edit creates a new revision and invalidates prior approval unless
  the product contract explicitly re-approves that exact revision.
- Review and verification are distinct. Human approval does not claim factual
  verification; deterministic verification does not grant publication.
- Work Record, contribution attestation, field release, publication, network
  profile, search grant, contact, and block remain separate authorities.
- A public event uses a new `publicTraceId`. The private ledger may retain the
  private-to-public binding; the public projection must not expose the private
  `traceId`, source manifest, private principal tuple, private consent, or
  unreleased field commitments.
- Revocation, correction, withdrawal, pause/off, block, membership/session
  invalidation, and purge create explicit reverse/fence edges. Derived readers
  must revalidate before the final copy or effect.

## 5. Closed event taxonomy

All types below are body-minimized. “Required binding” is in addition to the
common envelope.

| Event type | Required binding |
|---|---|
| `source.bound_to_trace` | Exact ConversationEvent/TranscriptRevision and newly admitted trace, current audience/ACL/consent/purge generation. Emitted only in the atomic source-to-work admission; never for passive observation or a source that produces no admitted work. |
| `source.corrected` | Prior and corrected source revisions. |
| `source.retracted` | Prior source revision plus current revocation/purge binding. |
| `intent.admitted` | Authorized source manifest and admitted WorkIntent revision. |
| `intent.rejected` | Candidate digest and closed rejection code; no candidate body. |
| `suggestion.created` | WorkProposal/Suggested Work revision, destination ACL, owner/reviewer opaque refs, approval-policy ref. |
| `suggestion.revised` | Prior/new revisions and exact revising principal. |
| `suggestion.endorsed` | Exact card revision, principal, endorsement/quorum revision. |
| `suggestion.approved` | Quorum-complete card revision and deterministically created run reference. |
| `suggestion.dismissed` | Exact card revision and private dismissal-object reference/digest. |
| `suggestion.expired` | Exact policy expiry and terminal card revision. |
| `run.created` | Approved card revision, idempotency digest, source manifest, destination and budget policy. |
| `run.queued` | Durable job/claim generation and action-bound authority-envelope digest. |
| `run.started` | Current claim, route snapshot, source-currentness result. |
| `run.state_changed` | Exact prior/new run revisions and closed states. |
| `run.source_invalidated` | Invalid source refs and revocation/purge generation. |
| `run.intervention_requested` | Closed intervention kind `input`, `review`, `effect_approval`, `source_refresh`, or `recovery`; private request-object ref. |
| `run.intervention_resolved` | Exact request revision, resolver authority and closed `approved`, `denied`, `supplied`, `expired`, or `cancelled`. |
| `effect.requested` | Exact run/stage revision, closed effect kind, destination controller, policy and expected postimage; no effect body. |
| `effect.approved` | Exact request revision, current approving principal/controller and approval-policy revision. |
| `effect.applied` | Exact request/approval, provider receipt commitment and independently read effect postimage. |
| `effect.failed` | Exact request revision, closed failure class and independently read `not_applied`, `partial_unknown`, or `compensated` result; no provider error body. |
| `effect.reconciled` | Exact journal operation/fingerprint, prior ambiguous phase and authoritative applied/not-applied/compensated postimage. |
| `run.cancelled` | Current run/claim revision and controller. |
| `run.failed` | Closed failure class and terminal run revision; no provider error body. |
| `run.completed` | Completion receipt digest and exact output refs. |
| `artifact.created` | Artifact revision, destination audience/ACL and run/stage refs. |
| `artifact.revised` | Prior/new artifact revisions and output digest. |
| `artifact.review_requested` | Exact artifact revision and review-policy ref. |
| `artifact.review_decided` | Exact artifact revision, reviewer authority and closed `approved`, `changes_requested`, or `rejected`. |
| `artifact.verification_recorded` | Exact artifact revision, verifier/controller, verification policy and body-free receipt digest; closed `passed`, `failed`, `partial`, or `unknown`. |
| `artifact.adopted` | Exact artifact revision and exact authorized destination revision that incorporated it; adoption is a product-state edge, never a view/download proxy. |
| `artifact.rejected` | Exact artifact/review revision and closed decision-object reference; no free-form reason or inference from non-use. |
| `artifact.withdrawn` | Exact adopted artifact and destination revisions plus current withdrawal/revocation/purge generation. |
| `artifact.publication_changed` | Exact approved artifact revision and closed `private`, `organization`, `exact_link`, `revoked`, or `expired`; exact-link capability remains separately revocable. |
| `outcome.recorded` | Exact authorized outcome object/revision, contributing run/artifact refs and owner/reviewer authority; no outcome body or inferred success. |
| `outcome.corrected` | Prior/new exact outcome revisions and current correcting authority. |
| `outcome.rejected` | Exact candidate outcome revision and closed decision-object reference; missing verification is `unknown`, not rejection. |
| `outcome.withdrawn` | Exact current outcome revision plus withdrawal/revocation/purge generation. |
| `work_record.claim_created` | ContributionClaim revision, subject controller and exact run/artifact/evidence refs. |
| `work_record.subject_decided` | Exact claim revision and closed subject approval/dispute state. |
| `work_record.named_party_decided` | Exact claim revision and body-free named-party decision ref; no contact identifier. |
| `work_record.organization_decided` | Exact claim revision and current organization controller revision. |
| `work_record.attested` | ContributionAttestation revision, claim revision/digest and verification tier. |
| `work_record.corrected` | Superseded and corrected claim revisions. |
| `work_record.revoked` | Revoked claim/attestation revision and purge generation. |
| `publication.contribution_published` | PublishedContributionClaim and FieldReleaseApproval refs; only released-field digests. |
| `publication.contribution_withdrawn` | Publication revision and exact 13-store purge receipt. |
| `publication.network_state_changed` | NetworkProfileProjection prior/new revision and closed `off`, `draft`, `live`, `paused`, or `deleted`. |
| `collaboration.search_admitted` | Current TalentSearchGrant, policy verdict and body-free NetworkSearchReceipt; no query body or scores. |
| `collaboration.contact_requested` | Purpose-bound ContactRequest revision; no channel/contact detail. |
| `collaboration.contact_decided` | Exact request revision and closed `accepted`, `declined`, `withdrawn`, or `expired`; channel revelation remains in the authorized product object only after acceptance. |
| `collaboration.block_changed` | NetworkBlock revision and closed active/revoked state; no free-form reason. |
| `lifecycle.corrected` | Exact invalid event plus replacement event; permitted only for metadata error, never to rewrite history. |
| `lifecycle.reconciled` | Exact compound-operation journal/fingerprint and authoritative product/event postimages after restart or ambiguous response. |
| `lifecycle.revoked` | Exact affected refs and current revocation generation. |
| `lifecycle.purged` | Body-free aggregate purge receipt with store/status counts, no enumerable person/source commitments. |

## 6. Authority, audience, consent, policy and provenance

An event is admissible only when all of the following are resolved from current
server authority and held through the final append:

1. active person, organization membership and session revision;
2. action-specific controller or grant revision;
3. destination audience and ACL revision;
4. source consent snapshot and current consent lanes;
5. active feature and policy revisions;
6. source/output revision and digest;
7. tenant purge and revocation generations.

Shadow observations, caches, usage ledgers, display names, emails, singleton
organizations, fixed rosters, model claims and client-supplied authority IDs may
never authorize an event or fill a missing binding.

`model_assisted` describes provenance only. It neither lowers the evidence
standard nor permits a model to name a person, infer an audience, choose a
retention class, approve work, verify its own output, create a Work Record, or
publish anything.

## 7. Private and public separation

The private lifecycle ledger is tenant-scoped and readable only through current
membership plus object ACL. Private-person events require the subject or an
exact legally approved administrative capability; ordinary organization admin
status is insufficient for private MyMind or private-source inspection.

The public projection is allow-list-only and may contain:

- public event type and effective time bucket;
- public contract ID/revision/digest already visible in the product;
- approved verification tier and released field names;
- aggregate body-free purge state.

It may not contain private trace/parent IDs, private source refs, subject/session
authority, private audience membership, contact identities, search query,
unknown/private fields, score, raw reason, or a digest of a low-entropy private
value. Public and private stores must use separate MAC domains and keys.

### 7.1 Audit access is not analytics access

Private audit access resolves one current subject or one exact case trace at a
time. A person may inspect their own authorized trace. A case reviewer needs a
current purpose-bound review capability naming the exact trace, audience,
fields, expiry and policy revision. Organization-admin status alone grants no
person history, trace search, export, or actor drilldown. Every audit read is
itself body-minimized and receipted.

Analytics access is a different service, store, policy, key domain, API and UI.
It consumes only approved metric facts after current consent/opt-out and purge
fences. It returns cohort aggregates with numerator, denominator, unknown count,
definition revision and coverage; never raw events, trace IDs, person IDs,
membership IDs, session digests, small-cell members, or actor drilldown. Cohorts
below the approved suppression threshold are `suppressed`, not zero. Repeated or
overlapping queries that could reconstruct a person are denied and receipted.
Self-inspection and exact case review never pass through the analytics service,
and analytics cannot be used as an employee dossier, leaderboard, search index,
or performance investigation.

## 8. Data quality and unknown handling

Every event carries:

```text
quality.status = known | partial | unknown | not_applicable
quality.reason = none | legacy_gap | source_unavailable | authority_unavailable |
                 consent_unavailable | policy_unavailable | digest_unavailable |
                 late_arrival | clock_uncertain | recovery_pending | unsupported
quality.observedSourceCount
quality.expectedSourceCount
```

Counts are non-negative integers. `known` requires equal observed and expected
counts and `reason=none`. `partial` requires both counts and observed less than
expected. `unknown` must not invent an expected count. `not_applicable` is
allowed only where the taxonomy declares no source set.

Operational rules:

- no divide-by-zero; a metric with no eligible denominator is `unknown`;
- no backfilling by inference from current state;
- late events retain original `occurredAt` and receive `late_arrival`;
- clock skew is bounded and reported, never attributed to a person;
- duplicate exact events collapse by idempotency; changed replay is conflict;
- malformed or unauthorized events enter a body-free quarantine counter, not
  the lifecycle graph;
- dropped best-effort telemetry is reported as coverage unknown, not zero.

## 9. Retention, export, correction and deletion

Closed retention classes are:

| Class | Exact default | Applies to |
|---|---:|---|
| `source_link_short` | 30 days after `recordedAt` | `source.*` linkage not otherwise required by a current authorized object. |
| `private_work_lifecycle` | 365 days after terminal `effectiveAt` | intent, suggestion, run, artifact and review events. |
| `authorized_disclosure_audit` | 730 days after withdrawal/revocation/expiry | Work Record, publication, exact-link and collaboration authority events. |
| `purge_receipt_body_free` | 730 days after purge completion | aggregate purge proof only. |

An exact approved retention-policy revision may shorten these periods. It may
lengthen one only through a separately reviewed policy revision and legal basis;
the event binds that revision. Legal hold is a separate current capability and
must never restore an erased body or destroyed key.

Private traces and subject-linked events use per-subject keys, and source-linked
edges additionally use per-trace data keys. Shared events are stored once under
a shared-event key and carry independently wrapped subject/trace link keys; a
subject deletion destroys only that subject's links and cannot delete another
subject's authorized audit history. Every wrapper binds tenant, subject, trace,
event, audience, revision and purge generation. Backups contain ciphertext and
wrapped keys only and are subject to the same external tombstone/high-water.

Legal hold is a purpose-bound, expiring capability with exact event/trace scope,
legal-policy revision and reviewer. It may delay row expiry but cannot preserve
or reconstruct a key already destroyed, widen audience, expose a held record to
analytics, or defeat a subject/source consent fence. Hold creation, renewal,
release and denial are auditable; an expired hold resumes the original deletion
deadline rather than creating a new retention period.

Export is generated at request time under current authority:

- a person receives their private lifecycle events and exact product refs;
- an organization export contains only organization-audience events the
  requester can currently read;
- public export contains only the public projection;
- exports never include another person's private events, raw session digest,
  provider error, hidden membership, unreleased value digest, or internal
  idempotency material.

An export is encrypted to a request-specific key, has an exact manifest and
expiry of at most seven days, and is independently revocable. Expiry,
revocation, subject deletion, consent withdrawal, or authority loss destroys
the export key and advances its external tombstone. Download capabilities are
single-purpose and never become a new retention authority.

Correction appends `lifecycle.corrected`; it never overwrites the original.
Product corrections cite new exact revisions. A subject delete destroys private
subject/trace/body-derived commitment and export keys, removes private trace
links and indexes, advances external anti-rollback high-water, and leaves only
a non-enumerable aggregate purge receipt. Shared events survive only for other
currently authorized subjects with fresh independent wrappers. Backups, caches,
indexes, exports, journals and derived metrics are included in purge receipts.
Restore verifies managed MACs and rejects any record, wrapper, export or key
older than the applicable subject/trace/source tombstone and external
high-water; it must prove deleted material cannot be decrypted or re-linked.

## 10. Pre-migration baseline

No PI0-A migration may begin until one signed private baseline manifest is
captured atomically or at mutually fenced high-waters. The manifest contains no
bodies and binds:

- release commit and schema/policy/feature-switch digests;
- tenant and store identities;
- source high-water, projection high-water and purge generation for conversation,
  Suggested Work, run queue, artifacts, review/verification, Work Record,
  publication and collaboration stores;
- exact counts by closed aggregate type and current state;
- counts of records with complete, partial, unknown or invalid linkage;
- current retention classes and oldest/newest effective times;
- source and target manifest digests, expected migration delta, backup identity,
  rollback identity and operator/reviewer signatures.

Definitions:

- `eligible`: a current, authorized, non-purged record at the frozen high-water;
- `linked`: every required PI0-A edge is exact and digest-bound;
- `partial`: at least one exact edge exists and at least one required edge is
  provably missing;
- `unknown`: eligibility or a required edge cannot be established;
- `invalid`: conflicting tenant, revision, digest, authority, audience, consent,
  policy, time, idempotency, revocation or purge binding;
- `legacy_import`: an exact baseline object converted without fabricating
  history, with `quality=partial|unknown` as applicable.

The baseline reports coverage as `linked / eligible` with the numerator,
denominator and unknown count. It never reports a rate when eligible is zero or
unknown. Existing best-effort telemetry cannot establish eligibility.

Migration must be idempotent, independently read back the target, preserve the
baseline source byte-exact, and prove rollback/restore. Unlinked legacy rows are
quarantined from metrics; they are not deleted or silently repaired.

## 11. Metric-definition manifest

No founder metric may be queried until a signed
`stride.pi0.metric-definition-manifest.v1` exists before results are visible.
Each entry binds `metricId`, definition revision/digest, eligible event types and
states, numerator, denominator, unit, time origin/terminal, exclusion rules,
unknown rules, censoring/window, cohort policy, suppression threshold, consent
and purpose, source schema/high-waters, measurement code release, retention,
owner role and independent reviewer. Changing a definition creates a new
revision and does not rewrite prior results.

The initial closed manifest is:

| Metric ID | Exact definition |
|---|---|
| `time_to_useful_outcome` | Distribution from `source.bound_to_trace.effectiveAt` to the first current `outcome.recorded.effectiveAt` whose exact outcome is human-reviewed and not rejected, withdrawn, revoked or purged. No outcome, incomplete linkage, or an open trace is right-censored/unknown, never a zero or failure. Report p50/p95/p99 and eligible/observed/unknown counts, never person ranking. |
| `completion_correction` | Separately report terminal completed traces, completed traces later corrected, and withdrawn/revoked outcomes. Denominator is eligible admitted traces whose observation window closed. `run.completed` alone is not a useful outcome. No scalar “quality score.” |
| `human_intervention` | Counts and rate of traces with at least one exact `run.intervention_requested`, broken out only by the five closed intervention kinds. Denominator is eligible started runs; unavailable logging is unknown. It measures workflow demand, not human or agent competence. |
| `artifact_adoption` | Count/rate of exact `artifact.adopted` edges among eligible reviewed artifacts. A view, download, copy, publication, or absence of rejection is not adoption. Rejection and withdrawal are reported separately; unresolved is unknown. |
| `repeat_use` | Suppressed cohort count/rate of opted-in subjects who start a second eligible trace within a predeclared 7-, 30-, or 90-day window after a first completed trace. Subject identity is used only inside the private metric executor and is never returned. |
| `retention` | Suppressed cohort survival at the same predeclared windows, with censoring and opt-out/deletion applied. It is a product-return measure, not employee retention, attendance, or activity. |
| `reliability` | Counts/rates by closed system failure class for admitted runs/effects: completed, failed, cancelled, recovery-required, reconciled-applied, reconciled-not-applied and quarantined. Permission denials and user rejection are not system failures. |
| `provider_model_cost` | Aggregate authorized provider/tool units and currency from exact metering receipts joined to eligible run/effect IDs. No prompt/body, person/worker breakdown, hidden join to Work Record, or estimated cost without an explicit `unknown` source class. |
| `permission_failure` | Count/rate of operations denied or revoked by current audience, ACL, consent, session, membership, grant or policy checks. Denominator is admitted requests at the same boundary; malformed/unknown authority is separate. No actor drilldown. |
| `attribution_failure` | Count/rate of otherwise eligible artifacts/outcomes lacking an exact current principal/source/output/provenance binding. Missing binding is `unknown` or failed admission and cannot be repaired by text/name/time inference. |
| `qualitative_trust_pull` | Consented research-case counts by a separately approved closed interview/task codebook: understood provenance, understood unknown, trusted boundary, correction used, withdrawal used, return requested. Quotations, notes and recordings stay in the research store and are never PI0 events or metric dimensions. No sentiment or psychographic inference. |

### 11.1 Prior-workflow comparison

Comparison with the prior workflow is a separately signed paired-study manifest,
not a query option. It binds the frozen prior/current release identities, task
fixture/case manifest, eligibility and exclusion rules, consent, assignment or
matching method, observation window, metric-definition revisions, missing-data
handling, non-inferiority/superiority hypothesis, sample-size rule and reviewer.
Only the same metric revision and equivalent source/outcome contract may be
compared. Legacy best-effort usage telemetry cannot serve as the prior arm.
Historical inference from present state is forbidden. If no prospective or
frozen authoritative baseline exists, comparison is `unknown`.

Results report paired aggregate deltas and intervals plus eligible/observed/
unknown/suppressed counts. They never expose person, worker, team, organization
admin drilldown or convert a workflow comparison into a productivity score.

## 12. What current evidence proves

| Area | Current evidence | Proven today | Not proven for PI0-A |
|---|---|---|---|
| Canonical facts | `canonical_store.go`, `canonical_normalization.go`, `canonical_normalization_test.go` | Append-only event IDs, aggregate revisions, correlation/causation, actor, ACL, consent snapshot, idempotency, payload digest, content ref and retention fields; strict payload normalization. | The PI0-A envelope/taxonomy, complete lifecycle emitters, private/public domain separation and lifecycle migration. |
| Conversation sources | `stride_contracts.go`, `stride_conversation_ledger.go`, `stride_conversation_ledger_test.go`, `stride_product_source_authority.go`, `stride_product_source_authority_test.go` | Body-free exact conversation/transcript refs, audience/ACL/consent/purge-aware currentness and source manifests. | A complete source-to-trace event and baseline coverage across every chat/transcript path. |
| Existing proposal telemetry | `usage_ledger.go`, `usage_ledger_test.go`, `usage_rollup.go`, `usage_rollup_test.go` | Best-effort minted/resolved/launched/terminal proposal joins and workflow outcomes. | Authority: recording can be disabled or dropped, fields are open maps, and tenant/principal/ACL/consent/policy/revision bindings are incomplete. It must not be migrated as canonical PI0-A truth. |
| Legacy proposal UI | `codex_proposals.go`, `codex_proposals_test.go`, `codex_proposals_stride_authority_test.go` | Human confirm/dismiss, launch claim/recovery and legacy retirement while STRIDE owns Suggested Work. | One unified PI0-A event path across legacy and STRIDE records. |
| STRIDE Suggested Work | `stride_work_orchestration.go`, `stride_work_orchestration_test.go`, `stride_runtime_adapters.go`, `stride_meeting_suggested_work_http_test.go` | Revision/CAS, current source checks, approval policy/quorum, dismiss, deterministic run identity, route/claim/checkpoint/effect approval, intervention states, artifacts/outcomes/feedback and snapshot validation. | Canonical PI0-A append transaction and durable production event repository for every transition. |
| Run/provider | `agent_thread_runner.go`, `agent_thread_runner_test.go`, `codex_runner_queue.go`, queue tests | Proposal/run joins, terminal events, durable claims, current tenant authority fences, artifact writes and quarantine behavior. | One exact trace held through every legacy and STRIDE runner/provider/final callback seam. |
| Artifacts | `memory.go`, `artifact_object_authorizer.go`, `artifact_object_authorizer_test.go`, `share_links.go`, `share_links_test.go`, `office_brief.go`, `office_brief_test.go` | Revision lineage/digests, ACL-bound writes, human approval stamps, review gates, exact-link expiry/revocation. | Closed PI0-A artifact/review/verification events and consistent retention/deletion across superseded blobs/backups. |
| Work Record | `stride_contribution_network_contracts.go`, its tests, `stride_contribution_authority.go`, `stride_contribution_authority_test.go`, `stride_e10_product_live.go` | Claims, subject/named-party/organization review, attestation tiers, correction/revoke, field release and private/public projections with revisions/digests. | Lifecycle instrumentation append and pre-migration linkage coverage from every run/artifact. |
| Publication/collaboration | `stride_network_authority.go`, `stride_network_authority_test.go`, `stride_network_shadow.go`, `stride_network_shadow_test.go` | Draft/live/pause/off/delete, grants, published-only search, purpose-bound contact, block, current-authority fencing and exact 13-store purge. | PI0-A private/public event stores and externally approved retention/export policy. |
| Product integration | `stride_product_lifecycle.go`, `stride_product_lifecycle_test.go`, `stride_e10_product_http.go`, `stride_e10_product_http_test.go`, `stride_e10_product_live.go`, `stride_e10_product_live_test.go` | Concrete Suggested Work-to-run-to-artifact/outcome paths and W1-backed product actions. | Exhaustive emitter coverage and atomic event/product operation journal across all routes. |

## 13. Exact future implementation and test inventory

This section is an ownership proposal, not authorization to edit.

The slices below are disjoint. Shared integration files are owned only by the
named integration slice during a wave; emitter owners consume exported APIs and
do not duplicate stores, metric queries, purge logic, or journals.

| Slice / accountable owner | Exact proposed files | Required focused proof |
|---|---|---|
| Contract and private/public store / Instrumentation Core | `stride_pi0_instrumentation_contracts.go`, `_test.go`; `stride_pi0_instrumentation_store.go`, `_test.go` | Closed schema/taxonomy, canonical JSON, current principal/audience/ACL/consent/policy, domain-separated commitments, MAC rotation, public/private non-correlation, forbidden recursive keys, CAS/idempotency/restart. |
| Compound journal / Transaction Reliability | `stride_pi0_compound_journal.go`, `_test.go` | Every phase and abrupt restart; malformed/changed fingerprint; effect applied with lost response; event-ahead/product-ahead recovery; independently read postimages/high-waters; no repeated effect; compensation/quarantine; managed-MAC tamper/rotation. |
| Metric definitions and executor / Measurement | `stride_pi0_metric_manifest.go`, `_test.go`; `stride_pi0_metric_query.go`, `_test.go` | All section 11 definitions, pre-result manifest freeze, numerator/denominator/unknown/censoring, suppression, opt-out, no actor drilldown, overlapping-query reconstruction denial, prior-workflow manifest mismatch and legacy-telemetry rejection. |
| Audit query / Privacy and Trust | `stride_pi0_audit_query.go`, `_test.go` | Self-only access, exact purpose-bound case review, expiry/revoke, org-admin denial, one-trace scope, audit read receipt, analytics API cannot return raw events. |
| Crypto lifecycle, purge and tombstones / Data Custody | `stride_pi0_crypto_lifecycle.go`, `_test.go`; `stride_pi0_purge.go`, `_test.go` | Per-subject/per-trace/shared-event key separation; subject/source/trace deletion; nonimpact to other subjects; external high-water; backups/caches/indexes/metrics; wrong-key/tamper/rotation; restore cannot resurrect/re-link; legal-hold bounds. |
| Export / Data Rights | `stride_pi0_export.go`, `_test.go` | Current self/org/public scope, manifest, request key, seven-day maximum expiry, single-purpose download, revoke/delete/authority-loss crypto-erasure, another-subject and hidden-field denial, backup/restore behavior. |
| Baseline / Migration | `stride_pi0_baseline.go`, `_test.go`; `stride_pi0_migration.go`, `_test.go`; `stride_pi0_disposable_target.go`, `_test.go` | Atomic fenced high-waters, exact counts/unknown, no inferred legacy edges, signed source/target/backup/rollback, exact apply/readback, lost response/restart, idempotency, rollback/restore, symlink/hardlink/tamper/wrong-key/rotation. |
| Canonical SQL and DR registry / Storage | next unallocated canonical migration; `canonical_migrations.go`, `canonical_migrations_test.go`; `canonical_postgres_test.go`; `internal/dr/postgres.go`, `_test.go` | SQL/API validation parity, immutable receipts, journal phases and exact foreign bindings, nonce/key uniqueness, row-level tenant/audience separation, external high-water, backup inventory, forward/future version rejection, disposable PostgreSQL apply/rollback/restore and DR table parity. |
| Read-only preflight and receipt / Operator | `scripts/stride-pi0-preflight.mjs`, `.test.mjs`; body-minimized receipt and SHA sidecar | Exact release/schema/policy/store/key identities, no mutation, missing external owner/key/high-water fails closed, deterministic receipt, digest/sidecar verification, no production caller or activation. |

### New shared contract/store slice

- `stride_pi0_instrumentation_contracts.go`
- `stride_pi0_instrumentation_contracts_test.go`
- `stride_pi0_instrumentation_store.go`
- `stride_pi0_instrumentation_store_test.go`
- the next unallocated canonical migration and its canonical/DR registry tests

Tests: closed-schema rejection; canonical JSON; exact tenant/principal/session;
ACL/consent/policy currentness held through append; idempotent replay/conflict;
revision continuity; source/output manifests; effective/recorded time; unknown
handling; private/public MAC-domain separation; no forbidden fields recursively;
retention/export/correction/delete; high-water anti-rollback; crash recovery;
backup/restore/purge.

### Source and Suggested Work emitters

- `stride_conversation_ledger.go` and tests
- `stride_product_source_authority.go` and tests
- `stride_work_orchestration.go` and tests
- `stride_runtime_adapters.go` and tests
- `stride_meeting_suggested_work_http_test.go`
- `codex_proposals.go` only for explicit legacy bridge/retirement evidence

Tests: source correction/retraction; meeting/chat/private audience separation;
guest consent; revision edit; quorum; dismiss/expire; stale source; wrong tenant;
lost response; no launch before approval; legacy telemetry cannot authorize;
passive source read/speech/storage emits nothing; only an atomic admitted
source-to-trace edge emits `source.bound_to_trace`.

### Run, intervention, artifact, review and verification emitters

- `codex_runner_queue.go` and tests
- `agent_thread_runner.go` and tests
- `agent_thread_followup.go` and tests
- `memory.go` and tests
- `artifact_object_authorizer.go` and tests
- `share_links.go` and tests
- `office_brief.go` and tests

Tests: current authority held through provider/final writes/callback; claim
restart; intervention request/decision; stale ACL; artifact revision invalidates
approval; reviewer separation; verification partial/unknown; exact-link
independence; effect requested/approved/applied/failed/reconciled; outcome and
adoption/rejection/withdrawal; correction/delete/purge; zero provider callback
on stale authority; compound-journal lost-response recovery without repeat.

### Work Record, publication and collaboration emitters

- `stride_contribution_authority.go` and tests
- `stride_network_authority.go` and tests
- `stride_network_shadow.go` and tests
- `stride_e10_product_live.go` and tests
- `stride_e10_product_http.go` and tests

Tests: subject/named-party/organization separation; current controllers;
attestation/publication refs; correction/revoke/withdraw; off is non-destructive
but synchronously fenced; exact 13-store purge; grant/session currentness;
published-only search; accepted-contact channel rule; block; public projection
contains no private trace or low-entropy commitment.

### Baseline/migration/operator slice

- a new PI0 baseline/manifest module and tests
- a new disposable target adapter and tests
- a read-only preflight/operator command and tests
- body-minimized deterministic receipt plus SHA sidecar

Tests: atomic high-waters; exact counts; unknown denominators; valid legacy
import without invented edges; full manifest apply/readback; lost response;
restart; signed backup; rollback; wrong key; tamper; key rotation; symlink/hardlink
rejection; no production caller or default activation.

## 14. Acceptance gate

PI0-A remains `draft_revised_waiting_independent_recritic` until all of the following are independently
verified:

1. every event type has a strict validator and event-specific required refs;
2. all real lifecycle roots have atomic or journaled emitters, with every
   compound phase and lost-response postimage independently verified;
3. the pre-migration baseline is signed, body-free, exact and reproducible;
4. private/public and audit/analytics separation, commitment-domain separation,
   suppression and recursive forbidden-field tests pass;
5. per-subject/trace/shared-event retention, export expiry/revoke, correction,
   deletion, legal hold, backup and anti-resurrection restore pass;
6. no person/worker score, surveillance signal, psychographic field or hidden
   join exists;
7. normal, race, disposable PostgreSQL, restart/restore, migration, vet and
   diff checks pass;
8. the metric manifest covers every section 11 metric and prior-workflow
   comparison before any result is visible, and an independent critic returns
   no blocker or major;
9. any collection, migration, provider, external custody, Git, release and
   production activation receive separate explicit authority.

Current verdict: the repository supplies reusable authority, provenance,
revision, artifact, review, Work Record and purge foundations, but does not yet
supply an authoritative end-to-end PI0-A instrumentation ledger. Existing
best-effort usage/proposal telemetry is informative operational evidence only.
