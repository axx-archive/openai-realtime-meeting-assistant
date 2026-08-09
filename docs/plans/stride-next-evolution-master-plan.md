# STRIDE Next Evolution — Master Architecture and Execution Ledger

**Status:** E10-W0 through E10-W3 are independently
`deterministic_verified` for their named evidence classes. E10-W4's private
person/organization/session, Contribution Review, Work Record, and private
network-draft product is now `production_private_live`. The authenticated W4
activation binds all seven current people to Bonfire, with seven active
memberships, AJ as sole owner, all 98 current sessions bound to their canonical
person and membership, seven private unlisted network drafts, and zero guest
sessions. The production snapshot is schema v2 generation 48 and descends from
activation `275d8c25b06c2947ff0e85257f14090ce1fd75a8c045eb1d73c6b96b907719a6`
and receipt digest
`fafe4431517126f0807b159b919354ca75c27d8ecd5e7fe7a3514743a83729af`.

The exact serving release at 2026-08-09 02:38 PT is ledger generation 37,
commit `cd9566ba32b9c967e59e4c6f8fa40ee2ec10b0d4`, bundle
`89b1f109129695a7c2a3176d04eaad6993c48c7ec5fb4a1c406525cb81e6a64c`,
application image
`sha256:005359bb6f43fb774933f750b59c762415f485c6d0fbe2eab34a0bf45062023e`,
and renderer image
`sha256:685bb0692af2ff2967ec0ad9b70c88b80dca93d8ce5caffe084e8e5b2b597735`.
The retained verifier, running images, Caddy, `/healthz`, and `/readyz` agree
with `verified-local-unsigned`; exact prior release
`c34118fdcc55635eac073745e35b017fc97392cc` remains intact for rollback.
The carrier remains `qualified=false` and `externallyAttested=false` because
independent signing/off-host attestation is still a separate gate.

Native Build 49, not Build 48, is the newest exact mobile carrier. EAS build
`d76f442c-29ee-4f55-b30d-bac0c7cfeeb3` and submission
`b8d58ae6-c183-4072-8ee2-a7ef36190344` are `FINISHED` from exact commit
`d4c827c...`; Apple build `9c63cf4a-ef1e-49bd-9110-ea5abbc56b9d` was freshly
observed `VALID`, unexpired, and `IN_BETA_TESTING` at 2026-08-08 08:34 PT.
Unexpectedly, both internal `Team (Expo)` and external `Bonfire` contain Build
49, contradicting the prior “external testers unchanged” report and requiring
provenance review before any further TestFlight action. Build 48 remains valid
historical evidence only. No physical iPhone/iPad acceptance is proven.
The integrated Go suite, required ConversationContinuity/AgentMind/proactivity/
web-scroll race gate, `go vet`, native readiness contract checks, client tests,
`git diff --check`, and independent critic all passed before release. The later
exact-release-only queue migration was additionally proven by the 34 release
contract checks and an actual confined-container ownership/mode test.

The current serving release remains unqualified externally. Production
`/readyz` truthfully reports the separate canonical shadow unhealthy at
dirty/high-water 29,094 versus reconciled/checkpoint 8,532 with 119 repair
candidates and governed replay off. W4 does not read that shadow as product
authority. Exactly these W4 features are active: person profile, organization
read/write, active-organization session, Contribution Review, and private Work
Record. Public network publication, discovery/search, contact, semantic
reranking, MyMind context, and global tenant cutover remain deliberately off
pending W5-W8 qualification rather than being presented as completed. Offsite backup remains dormant. No production replay, MyMind
body custody/context assembly, AmbientMind compiler/worker or reader, artifact
disposition action, provider qualification, physical-device acceptance,
multi-room/TURN acceptance, HA/DR custody ceremony, pilot packet, or soak has
been completed. Those are named unresolved gates, not implied by this candidate.
Scout may be
invited as a visible, attributed room participant only under current room,
media-generation, audience, and unanimous capture/transcription/model-analysis
consent. Meeting transcription remains independent and cannot silently invite
Scout. Marketplace employee audio remains default-off until an exact signed
provider/model/voice/config qualification and external anchor are available.
Release authority does not waive canonical-repair confirmation, consent,
custody, physical-device, HA/DR, pilot, soak, or independent evidence gates.

**Date:** 2026-08-09

**Naming:** **STRIDE** is the long-term operating-system name. **Bonfire OS** is
the current application and code-identifier implementation. After the exact
release is verified, the reviewed history will be published to a new primary
`STRIDE` GitHub repository; the existing repository remains intact until the
new remote and default branch are independently verified.

**Source of truth:** This file is the single plan for the next evolution. It carries forward, rather than replaces, the proven foundations and evidence in:

- `docs/plans/bonfireos-2.0-execution.md`
- `docs/plans/bonfireos-w2-design.md`
- `docs/plans/canonical-event-acl-v1.md`
- `docs/model-routing-master-plan-2026-07-11.md`
- `docs/llm-routing-audit-2026-07-11.md`
- `docs/memory-architecture-study-2026-07-10.md`
- `docs/plans/multi-room-2026-07-08.md`
- `docs/plans/rooms-ux-2026-07-09.md`
- `docs/plans/voice-first-mobile-design.md`
- `docs/plans/the-table-design.md`
- `docs/plans/codex-goal-workflows.md`

External reference, adopted selectively rather than introduced as a runtime dependency: [Block Buzz at audited commit `90e058e`](https://github.com/block/buzz/tree/90e058ebf68137e048a409aec6616519379ff726). STRIDE adopts the useful separation of portable persona definition, runtime identity, and team membership; it does not adopt Buzz's collaboration substrate, permission defaults, prompt-only delegation, or agent-authored memory as organizational truth.

Older model recommendations are historical baselines, not current authority. The current official model references for this plan are [GPT-5.6](https://developers.openai.com/api/docs/guides/latest-model.md), [GPT-Realtime-2.1](https://developers.openai.com/api/docs/models/gpt-realtime-2.1), [GPT Transcribe](https://developers.openai.com/api/docs/models/gpt-transcribe), and [GPT Live Transcribe](https://developers.openai.com/api/docs/models/gpt-live-transcribe).

## Active completion checkpoint — 2026-08-08 10:00 PT

This is the compact-safe resume ledger for the current goal. A checked item is
complete only at the scope named; local implementation is not release proof.

### Investigation and decisions

- [x] Proved the Country Golf report itself completed with 14 sources.
- [x] Proved that report used the legacy Anthropic Fable worker, despite the UI
  expectation that research was OpenAI-only.
- [x] Separated the later red `max_output_truncation` Scout-answer failure from
  report completion and sharing.
- [x] Proved the missing PDF is blocked by a stale render-queue job owned with
  unreadable permissions; the newer report remains `renderStatus=queued` behind
  that head-of-line job.
- [x] Decided canonical Scout research routing: OpenAI Sol, high reasoning;
  ordinary Scout chat/routing/extraction remain Luna, maximum reasoning.
- [x] Decided that analysis, critique, description, or prompt reconstruction
  from an already-authorized image/document is ordinary multimodal conversation,
  not a research workstream. Explicit reports, audits, comparison/recommendation
  passes, and external research remain governed durable work.
- [x] Decided `main channel` resolves to the permanent pinned Bonfire Chat, but
  posting there remains an explicit user-authorized action.

### Current candidate implementation

- [x] ConversationContinuity exact-revision, body-free rebuild/restart/delete/
  edit corrections are released; frozen normal and race gates passed.
- [x] AgentMind exact source-linked correction/supersession/forgetting and
  signed-in inspection authorization are released; frozen normal and race
  gates passed.
- [x] Proactive reply/react/no-action, quiet/active behavior, dedupe,
  revalidation, non-launching Colton consultation, and shutdown corrections
  are released; frozen normal and race gates passed.
- [x] Research runner and research follow-up are forced to OpenAI Sol/high with
  actual model/effort provenance; the route regression matrix passed.
- [x] Scout-answer truncation receives one bounded larger-output retry and still
  fails closed without a fuzzy fallback; receipt tests passed.
- [x] Render jobs are written atomically as `0660`; exact-release init repairs
  existing queue JSON ownership/mode before the runner starts. The confined
  repair is live and the exact Country Golf PDF completed.
- [x] Drive file rename and named Save-to-Drive server seams implemented.
- [x] Hierarchical Drive folders (`parentId`, depth/cycle validation) are live
  additively over existing root folders; focused tests and rendered navigation
  passed.
- [x] Destination-bound `POST /assistant/attachments/from-file` implemented with
  exact Drive-source revision revalidation and no body-bearing response.
- [x] Native thread-chat proposal editing, rich terminal actions, Browse Drive,
  `#document`, exact source grants, file rename, and subfolder navigation
  implemented; native typecheck and 423 tests passed on the current mobile diff.
- [x] Replied-to generated images now enter the authorized OpenAI multimodal
  answer turn, and prompt reconstruction bypasses workstream routing; focused
  regression passes.
- [x] Web proposal/result/Drive/document-tag UX is live: the
  proposal is Scout's editable execution prompt, terminal work is a compact
  in-feed result with actual preview/provenance and Open/PDF/named Save/
  editable Regenerate actions, the persistent terminal rail and invented stage
  log are gone, Drive has hierarchical browsing and Rename, and both the
  paperclip and `+` palette can attach exact authorized Drive sources. Signed-in
  production observation proved report PDF download, folder traversal, the
  per-file Rename menu, and inline generated-image controls.
- [x] Web `#document` autocomplete is implemented for the main and desktop
  reply composers; selection mints and attaches an exact destination-bound
  source grant instead of resolving by display name. Signed-in production
  observation found the exact Country Golf report via `#positioning` without
  selecting or sending it.
- [ ] Canvas opening-turn and in-room Drive attachments need separate authority
  contracts because neither currently has an existing Scout-thread destination
  ID. They are not being faked through the thread-only grant endpoint.

### Frozen-tree and release gates

- [x] Added focused server regressions for Drive attachment auth/ACL/destination/
  source revision, named-save idempotency, render-job `0660`, and folder
  hierarchy/depth. Focused normal and race runs pass; intended files remain to
  be staged with the final candidate.
- [x] `go test ./... -count=1` passed on the post-critic staged candidate
  (root package `424.495s`).
- [x] Required ConversationContinuity/AgentMind/ScoutProactive/web-scroll race
  command passed with `-count=1` on the integrated local candidate.
- [x] `go vet ./...`, `git diff --check`, inline frontend JavaScript syntax,
  frontend truth contracts, native typecheck, all 423 native tests, and the 34
  native-readiness/exact-release harness tests passed on the integrated local
  candidate and committed archive.
- [x] Independent critic PASS obtained on the staged candidate after closing
  private/project Drive audience widening, post-commit source revalidation,
  reaction-only proactive visibility, route-documentation truth, and staged
  mobile-source completeness; no blocker or major finding remains.
- [x] Committed the intended combined tree and pushed without force to
  `axx/main`; `stride-site/` remained untracked and untouched.
- [x] Built native Build 48 from exact commit `7689bea`, uploaded it through
  EAS, and submitted it successfully to Apple. EAS now reports both the build
  and submission `FINISHED`, with no submission error.
- [x] Verified through an authenticated App Store Connect API observation that
  Build 48 is `VALID`, unexpired through 2026-11-05, and available to internal
  `Team (Expo)`; the exact Apple build ID is
  `2ce3d1b3-a21a-4700-9c20-fce1d5377450`.
- [x] Freshly observed Build 49 as the newer exact `d4c827c...` native carrier:
  EAS build `d76f442c-29ee-4f55-b30d-bac0c7cfeeb3`, EAS submission
  `b8d58ae6-c183-4072-8ee2-a7ef36190344`, and Apple build
  `9c63cf4a-ef1e-49bd-9110-ea5abbc56b9d` are complete/`VALID`.
- [ ] Investigate, without mutating distribution, why Build 49 is currently in
  both internal `Team (Expo)` and external `Bonfire`; this contradicts the prior
  external-group baseline and is not accepted as intentional cohort activation.
- [ ] Neither Build 48 nor Build 49 was physically accepted for this program.
  Preserve their processing receipts as historical evidence, but run the
  complete physical matrix on the final exact native build containing the new
  organization/Work Record/network behavior, including generated-image Open,
  Save to Drive, and editable Regenerate.
- [x] Activated the exact reviewed `axx/main` HEAD through
  `/opt/meetingassist-releases/<sha>`; ledger, receipt, all Compose image IDs,
  Caddy, `/healthz`, `/readyz`, rollback retention, and
  `verified-local-unsigned` agree. The 13:45 PT observation was commit
  `b95794f`, generation 26.
- [x] Refreshed the private exact-release proof at 2026-08-08 08:31 PT: ledger
  generation 30, exact active `d4c827c...`, bundle `2226f1d3...`, application
  image `de0533e1...`, renderer `73f0323d...`, all expected service images,
  Caddy, public/container health/readiness, retained rollback `cffe16b...`, and
  the official verifier agree on `verified-local-unsigned`.
- [x] The existing Country Golf report render job was repaired from root-owned
  `0600` to `65532:65532 0660`, completed as a 9-page 141,120-byte PDF, stored
  a durable PDF blob plus page images, and emitted `pdf_exported`.
- [x] Verified an authenticated user can download the exact Country Golf PDF in
  the rendered product. The live report exposes the 141,120-byte PDF, all nine
  rendered pages, and the 14-source research body.
- [x] Verified signed-in production rendering for inline generated images,
  Open, named Save-to-Drive, editable Regenerate, per-file Rename, hierarchical
  Drive attachment browsing, and `#document` autocomplete.
- [ ] Perform live send-path acceptance for direct image analysis and exact-
  source Bonfire Chat TL;DR sharing. Code regressions pass, but this checkpoint
  intentionally did not create production chat messages under the standing
  no-production-data-mutation constraint.
- [ ] Create and verify the new primary STRIDE repository. Discovery found an
  existing, unrelated private `axx-archive/stride` repository, so no repository
  was overwritten, renamed, deleted, or made primary without a user decision.
- [ ] Reconcile the rest of this master ledger. Provider qualification,
  physical-device acceptance, canonical replay/promotion, HA/DR custody,
  real-workforce pilots, and soak remain external acceptance gates unless their
  full evidence is actually produced.
- [x] Completed read-only discovery for people/MyMind and multi-organization
  productization. Basic name/avatar account profiles are live, but canonical
  `PersonPrincipal`/MyMind and multi-organization membership are not user-ready.
- [x] Froze the MVP identity, profile, organization, approval, maximum-three,
  active-session, departure, migration, ACL, and acceptance contracts in the
  strategic-design checkpoint below.
- [x] Completed E10-W1 canonical authority locally: migrations 14-16, exact
  person/organization/session/contribution/network contracts, serialized
  capacity and owner safety, immutable histories/current pointers, exact
  controller and named-party approvals, synchronous stale-evidence fencing,
  bounded search/contact, and full derived-store purge receipts. Focused normal,
  race, disposable-PostgreSQL, `go vet`, diff, and iterative independent critic
  gates pass; all 13 new switches remain false and no HTTP route is active.
- [x] Completed E10-W2 product surfaces locally/default-off: registered HTTP
  routing and all 38 closed actions use concrete W1 authorities with durable
  restart reconciliation; web and native render the exact self/coworker/network,
  Work Record, organization-governance, recruiting, contact, block, export, and
  deletion projections. Focused normal/race, disposable PostgreSQL, `go vet`,
  457/457 native tests, native typecheck, web VM compilation, client recovery
  tests, final independent code critic, and authenticated registered-live W1-
  backed browser QA pass. Earlier static fixture screenshots remain labeled
  static; the canonical receipt binds the later registered-live captures.

**Exact resume point:** request and, only if separately granted, execute
**E10-W4** against the independently verified W0 freeze and W1-W3 receipts
below. Before any mutation, regenerate the exact read-only production repair
manifest; the W0 count of 119 candidates is an observation, not a current
row-level mutation manifest.
The private release proof is current for generation 30 and exact `d4c827c...`.
Build 48 is historical; Build 49 is the newer exact carrier, but its unexpected
external `Bonfire` group inclusion requires provenance review and physical-device
acceptance is still open. Do not mutate production
chat merely to prove image analysis or TL;DR sharing; do not begin paid calls,
canonical repair/promotion, production data/config changes, deployment, or the
repository rename without the separately named authority. Resolve the existing
`axx-archive/stride` naming collision before creating or changing a primary
remote.

## 2026-08-07 People, Work Record, network, and multi-organization strategic-design checkpoint

This section is the decision-complete MVP contract for turning the existing
person/MyMind authority proof into real individuals and allowing one person to
belong to at most three organizations. It is part of E10, not a second roadmap.
It does not weaken the canonical, consent, custody, provider, device, HA/DR,
pilot, or soak gates later in this ledger.

### Readiness verdict

**Basic profiles are ready; canonical individuals/MyMind are not.** The current
signed-in account can view and edit a display name and avatar on web and native,
and passkey/theme state follows that fixed account. The app is still a seeded
seven-person Bonfire roster: boot removes non-seeded accounts, sessions contain
an email but no person, organization, or membership revision, `/auth/me` returns
no organization context, and runtime callers use one process-global Bonfire
tenant. AJ's current special-case approval authority is email-based and is not
an organization-admin membership.

The E10-R3 foundation is valuable but deliberately dormant. It defines and
tests body-free `PersonPrincipal`, `WorkspaceMembership`, exact-purpose MyMind
source/disclosure, departure, export, recovery, and custody-deletion authority.
Its own source says no HTTP route, worker, provider, or fixed-user path consumes
it; the compatibility adapter returns `ErrMyMindFeatureDisabled`; migration 10
installs `person_mymind_context=false`. There is no production PostgreSQL
reader/writer adapter, encrypted MyMind body custody, MyMind context consumer,
profile/MyMind product page, organization creation/join flow, maximum-three
constraint, or active-organization session. The separate **What STRIDE remembers
about me** relationship-memory UI is not canonical MyMind and must not be
presented as if it were.

### Product thesis — the professional network where work actually happens

AmbientMind, AgentMind, and MyMind are internal architecture, not the value
proposition or primary navigation. Users should experience one coherent promise:
**STRIDE is where work happens, so it is where the most credible professional
identity is formed and where future coworkers are found.** The three minds make
that experience intelligent behind the scenes; product copy speaks about the
work, people, evidence, and controls rather than exposing memory-system names.

LinkedIn's core object is a self-authored profile or feed post. STRIDE's core
network object is a **source-bound contribution to real work**. The profile is
not a static resume or an activity leaderboard: it is a living, portable work
record that shows what problems a person helped solve, how they contributed,
what outcomes and artifacts exist, which organizations verified the claim, and
what they chose to make visible. STRIDE does not ship a performative public feed
as the network MVP. Work creates the record; posting about work is optional.

The compounding loop is:

1. people and agents do work inside an authorized organization;
2. STRIDE identifies a candidate contribution from exact work/outcome sources;
3. the person and an eligible organization reviewer correct or confirm a concise
   contribution statement;
4. the organization issues, and the person accepts, a redacted portable
   attestation without exporting confidential source material;
5. the person chooses private, signed-in network, or direct-share visibility;
   open-web publication is a later, separate choice rather than an MVP default;
6. their work record becomes more useful across their career;
7. teams and recruiters ask STRIDE for people who have actually contributed to
   comparable problems, receive explainable evidence-backed matches, and invite
   a conversation or organization join request;
8. the next body of work creates new evidence rather than a new static resume.

This is the network moat: STRIDE observes the collaboration and outcomes at the
point of work, while a conventional professional network must ask users to
reconstruct and advertise them later. It must never turn that advantage into
surveillance, hidden employability scoring, or an employer-owned human memory.

### User-facing professional identity

The working product name is **Work Record**; implementation types use
`ContributionGraph` and `ContributionAttestation`. The Work Record replaces a
resume-first profile hierarchy with:

- **Problems and outcomes:** selected verified contribution cards grouped by
  problem/outcome class rather than chronological job-description prose;
- **How I contribute:** person-approved work-style and contribution roles such
  as builder, domain guide, decision owner, connector, reviewer, or facilitator;
- **Organizations and roles:** only memberships the person has chosen to reveal,
  with current membership never inferred from a private history;
- **Work evidence:** authorized public artifacts, redacted outcome summaries,
  and exact verification status, never confidential source links a viewer cannot
  open;
- **People and agents I helped:** optional, reviewed attestations describing how
  a person's input improved a team or an organization's agent, without exposing
  private AgentMind content or letting an agent self-certify the claim;
- **Open to:** explicit collaboration, advisory, employment, or recruiting
  preferences, off by default.

The identity header remains simple—name, avatar, optional pronouns, short bio,
and current visible organization context—but the Work Record is the core value.
Traditional title, education, certifications, and resume import can be supported
as user-authored context; they never outrank verified contribution evidence by
default.

### Canonical ownership and privacy decisions

1. **Person is global; organization access is revocable.** `PersonPrincipal` is
   the stable opaque root above credentials and memberships. Email is login data,
   not identity or a tenant key. MyMind belongs to the person and survives an
   organization departure subject to the person's custody/deletion choices.
2. **One product noun.** User-facing copy and new APIs say `organization`.
   Existing `workspace_id` remains a migration compatibility field only until
   one canonical organization authority replaces the dual legacy
   `org_memberships`/`stride_workspace_memberships` paths.
3. **Profiles are work records, not dossiers.** `PersonProfile` owns self-edited
   global identity and visibility preferences. `OrganizationMemberProfile` owns
   title/team for one membership. `ContributionAttestation` supplies only
   person-accepted, organization-verified portable work claims. Credentials,
   hidden memberships, activity/productivity telemetry, unapproved personality
   inference, MyMind values, private source graphs, account digests, and custody
   references never enter the shared projection.
4. **Visibility has separate projections.** Self can read/edit the global
   profile. A person sharing the currently active organization can read only the
   safe coworker projection plus that organization's role/title/team/joined
   date. An authorized signed-in network user can read only an independently
   opted-in `NetworkProfileProjection`. Owner/admin can edit only organization-
   scoped member fields and governed roles. A caller with neither shared active
   membership nor published-network authority, including a guest, departed, or
   revoked caller, receives an opaque, indistinguishable 404. Admin never implies
   MyMind custody, recruiter capability, or network-profile control.
5. **The minds remain backstage.** No public or coworker profile exposes a
   MyMind/AmbientMind/AgentMind card, source count, or graph. Private Settings may
   offer plain-language **Personal context and memory** inspect/correct/forget/
   export controls. An organization sees only exact revision-bound disclosures
   or accepted contribution attestations. It cannot enumerate another
   membership, private source, employment history, or hidden graph.
6. **Avatar/media ACL follows profile ACL.** Store new avatars as authorized
   blob references rather than duplicating data URLs in canonical records;
   validate type/size/content and revoke reads with membership loss.

### Organization and membership state machine

- `Organization`: immutable opaque ID; mutable name/slug; `active | archived`;
  creator; revision; timestamps; `private | listed` discoverability. Private is
  the default. Exact links/codes can reach a private organization's request
  page; only listed organizations appear in signed-in search.
- `OrganizationMembership`: person + organization + `owner | admin | member`;
  `active | departed | revoked`; revision and grant/end timestamps. Creating an
  organization atomically creates the creator's owner membership or creates
  nothing.
- `OrganizationJoinRequest`: a separate object with
  `pending -> approved | denied | cancelled | expired`. Pending grants zero
  organization discovery, object, chat, Drive, room, brain, agent, push, or
  profile authority. Replays are idempotent and stale decisions fail with 409.
- **Three-organization maximum:** count active organization memberships, not
  pending requests. Enforce it in the approval/create transaction under a
  locked person membership root (or equivalently three unique active slots),
  not in UI or with an unlocked count. A fourth create/approval fails without
  an orphan organization or partial audit event. Leaving durably frees a slot.
- **Approval:** the current revision of an active owner/admin membership may
  approve or deny for that exact organization. Approval rechecks request,
  administrator, organization, duplicate membership, and capacity in one
  transaction. AJ is bootstrapped as Bonfire owner during migration; subsequent
  authority comes from membership, never a permanent email exception.
- **Departure and ownership safety:** leaving/revoking immediately revokes
  sessions, sockets, push bindings, cached tenant views, and organization-bound
  MyMind disclosures while retaining attributable organization history. Refuse
  any leave/revoke/demotion that would leave no active owner; require an explicit
  ownership transfer first. An organization may additionally require an admin,
  but an admin never substitutes for its final owner. Concurrent departures
  cannot create an ownerless organization.
- **Audit:** create, request, approve, deny, cancel, expire, switch, role change,
  transfer, leave, and revoke append body-free actor/org/person/prior/new
  revision, reason, correlation, and idempotency evidence.

### Contribution graph, portable proof, and network discovery

The three memory systems may suggest a candidate, but they never become profile
or recruiter APIs. Network truth is produced only through these explicit,
revisioned contracts:

- `ContributionClaim` is organization-private and source-bound: subject person,
  `originated | shaped | reviewed | decided | delivered`, problem/outcome class,
  bounded dates, exact source/artifact/outcome/agent-run revisions and digests,
  ACL/purge generation, attribution method, and
  `candidate | subject_review | disputed | verified | revalidation_required |
  revoked`. It contains no portable body by default.
  The exact repair path is `verified -> revalidation_required -> verified |
  revoked | superseded`; re-verification always creates or binds a current
  source/consent/field-approval revision and stale approval cannot revive an old
  projection.
- `ContributionAttestation` is an immutable organization-signed revision of a
  verified claim with issuer, subject, evidence-manifest digest, policy revision,
  confidentiality, exact released-field manifest, signature, and supersession/
  revocation pointers. Default portable fields are coarse dates, category,
  contribution role/verbs, verification tier, and issuer only when approved.
  Customer, collaborator, project, excerpt, metric, and exact outcome fields
  require field-specific rights and named-party approval.
- `PublishedContributionClaim` is the person's concise narrative bound to one
  or more active attestations. Its lifecycle is
  `draft -> approval_required -> published -> superseded | withdrawn`; denial
  returns exact fields to draft. The organization controls factual verification
  and revocation; the person controls publication and may unpublish immediately.
- `AgentInfluenceReceipt` may support “helped this agent/team improve” only when
  it binds exact agent profile/runtime/model, human interaction, agent output,
  human adoption/review, and resulting work/outcome. An agent suggestion,
  self-report, prompt, token count, or interaction volume is not human impact
  and cannot reduce human credit or produce an AI-dependence score.
- `NetworkProfileProjection` is a separately opted-in global projection of
  person-authored fields and currently published claims. It is never assembled
  client-side from private person, organization, or memory records. Its states
  are `draft -> published <-> paused -> deleted`; discoverability is
  `unlisted | signed_in_network | exact_link`, with `exact_link` deferred from
  MVP activation.
- `TalentSearchGrant` gives an exact organization member a revocable, expiring
  `talent_searcher` capability. Owner/admin does not imply recruiter access.
  `NetworkSearchReceipt` records the human query, prohibited-criteria verdict,
  transparent structured interpretation, published candidate IDs/reasons,
  model/route, and audit metadata without copying hidden candidate data.
- `ContactRequest` is purpose + note + requested collaboration type with
  `pending -> accepted | declined | withdrawn | expired`. Contact information
  remains hidden until the person accepts the exact channel. People can pause
  discovery, decline, block a person/organization, and inspect their search/
  contact receipts.

Any source edit, deletion, ACL, purge-generation, source-consent revision/change,
or named-party field-approval withdrawal moves the dependent claim to
`revalidation_required` and synchronously fences its profile/search projection.
Corrections create immutable superseding revisions. Revocation
leaves a verifiable tombstone while removing discovery. Departure immediately
ends source drill-down and new claims under the old membership; the organization
retains its governed record, while the person retains already-issued signatures
without gaining confidential source access. Pending, disputed, stale, denied,
or revoked evidence grants zero network visibility.

Recruiter discovery is a signed-in, opt-in network capability, not an employer
dashboard. Natural language is compiled through prohibited-criteria policy into
visible structured filters over `NetworkProfileProjection`; optional semantic
reranking may use published text only. Results say **why surfaced** and **what is
unknown**, distinguish `organization_verified_opaque`,
`organization_verified_redacted`, `public_source_verified`, and
`self_described`, and never call someone “best” or emit a quality/personality/
fit score. Requests for “personality” may match only person-published work-mode
preferences; STRIDE never infers psychographics, culture fit, loyalty,
promotion readiness, compensation, availability, health, politics, protected
traits, or proxies from work/memory/activity. Ordering may use declared-query
match, evidence coverage, and freshness only—never engagement, network size,
contact response, employer prestige, or hidden model judgment.

The search index contains only the active published projection—never MyMind,
AmbientMind, AgentMind, raw artifacts/messages, hidden organizations, private
embeddings, or confidential evidence. Pause, withdrawal, revocation, and delete
fence search synchronously and purge derived indexes asynchronously with a
receipt. MVP forbids anonymous search, public SEO, bulk export/scraping, CRM
sync, automated outreach, feed/posts/comments/likes/follows, follower or
engagement counts, employee availability dashboards, productivity comparisons,
global influence scores, and the legacy “contribution fuel/share” ranking and
renderer. Scout may draft a claim or structured query, but cannot publish,
approve, change visibility, contact a candidate, or use private context for
ranking without a new explicit human action.

### Session, tenant, and ACL contract

Every member session selects exactly one active organization and binds
`person_id`, `organization_id`, exact membership ID/revision, and session
revision. The server derives tenant, teams, and role from that record; a client
header, query, body, route ID, or cached selector cannot widen it. Switching
validates the new active membership, bumps session revision, and tears down then
rebinds organization-scoped WebSockets, push authority, caches, workers, and
subscriptions. Revocation or departure invalidates the binding immediately and
returns the user to another valid organization or the organization chooser.

Before activation, replace unconditional `TeamIDs:[organization]`, fixed-roster
participant discovery, and direct `canonicalTenantID()` authorization at every
HTTP, WebSocket, chat, Drive, artifact, room, Board, brain, Scout, notification,
push, Marketplace, work-queue, and worker boundary with one centralized
membership-derived principal. Reconcile the two tenant environment sources and
the two membership models into one authority. A cosmetic organization switcher
before that conversion is forbidden because it would create cross-tenant leak
risk.

### MVP surfaces and APIs

- Top-bar organization switcher: current organization/role, up to three active
  memberships, pending badges, **Create organization**, **Join organization**,
  and an honest `3 of 3` state. Zero-membership accounts enter this chooser.
- Settings > Organizations: switch, leave, pending requests, capacity, and role.
  Organization > People/Requests is owner/admin-only, with approve/deny,
  role/removal, ownership transfer, conflict explanations, and an audit trail.
- `/me` is the editable self identity plus Work Record. `/people/:personId` is
  the safe current-organization coworker profile. `/network/:personId` is the
  separately published signed-in network projection. Web and native use the
  same server projections; no client independently joins global, organization,
  contribution, or memory records.
- Network settings provide Off/Draft/Live/Paused state, searchable-field preview,
  **View as recruiter**, contribution-card review/publication, contact inbox,
  blocks, and export/delete. Organization > Contribution approvals shows the
  exact field diff, evidence digest, named-party approvals, decision, revocation,
  and audit. Organization > Recruiting separately grants/revokes
  `talent_searcher`, search/contact limits, and audit; it never shows which
  current employees are open to work or who viewed/searched them.
- `GET/PATCH /api/identity/v1/me/profile`; `GET
  /api/identity/v1/people/{personId}`; and `GET/PATCH
  /api/organizations/{orgId}/members/{membershipId}/profile` use revisions/CAS.
- `GET/POST /api/organizations`; `POST/DELETE
  /api/organizations/{orgId}/join-requests`; admin `GET` requests and revision-
  bound `POST .../{requestId}/decision`; membership role/revoke; leave/transfer;
  and `POST /api/session/active-organization` are idempotent and opaque across
  tenant boundaries.
- Revision-bound network APIs cover self draft/preview/publish/pause, candidate
  claim review, exact field-release approval/revocation, signed receipt status,
  authorized search with visible structured interpretation, contact request/
  decision/block, and search/contact audit. Recruiter responses contain only the
  published projection and evidence-safe explanation; no source or memory body.

### Migration and activation

Keep the existing credential store during the first migration, but stop deleting
non-seeded accounts before enabling account creation. Idempotently mint stable
random person IDs for the seven current accounts, preserve password/passkey/
profile/avatar/session truth, create Bonfire, make AJ owner, and make the six
other seeded users members from a reviewed mapping manifest. Do not activate a
dual-write authority: shadow and compare legacy/canonical membership projections,
then cut readers/writers together with rollback evidence. Keep
`person_mymind_context=false` while profiles/organizations ship; encrypted body
custody and private MyMind retrieval activate only after their own custody,
consent, correction/forget/export, restart, and zero-leakage gates.

### E10-W0 normative authority freeze — revision 1

This subsection is the canonical W0 contract. Revision 1 is frozen for local
implementation and deterministic verification on 2026-08-08; changing a
normative rule below requires a new numbered revision, an exact diff, impacted-
test inventory, and re-review. This design freeze does not constitute the later
product/legal/privacy approval, production migration authority, signing-key
custody, provider authorization, activation, or shipping authority.

#### Contract registry and ownership

| Contract | Revision | Governing rule | Controller | W0 verification |
|---|---:|---|---|---|
| `PersonPrincipal` and safe `PersonProfile` | 1 | global opaque person above credentials; email never acts as identity or tenant; self controls global profile | person; recovery and deletion remain separate exact authorities | validation, account-linking/takeover, recovery, deletion, and opaque-reader tests |
| `Organization`, `OrganizationMembership`, `OrganizationJoinRequest`, active-organization session, and organization audit | 1 | private-by-default organization; transactional three-active-membership limit; owner/admin exact-revision decisions; no ownerless organization; server-derived active membership | current active membership, never an email exception | normal/race transaction, stale decision, cross-org, final-owner, session-rebind, restart/restore tests |
| `ContributionClaim` | 1 | organization-private, exact-source-bound claim; no portable body; stale source/ACL/consent/field approval enters `revalidation_required` | subject review plus eligible organization reviewer | lifecycle, source-drift, dispute, supersession, correction, revoke, and body-leak tests |
| `ContributionAttestation` | 1 | immutable signed organization revision bound to evidence-manifest and exact released-field manifest | eligible organization issuer under current policy/signing revision | signature, key rotation/revoke, field-minimization, forgery, and source-revalidation tests |
| `PublishedContributionClaim` | 1 | person-authored narrative bound only to active attestations; person can withdraw immediately; denial returns exact fields to draft | person controls publication; organization controls verification/revocation | concurrent publish/revoke, withdrawal, stale attestation, and index-fence tests |
| `AgentInfluenceReceipt` | 1 | exact agent run/output plus human adoption/review plus resulting outcome; no self-assessment or usage-volume credit | human subject and eligible outcome reviewer | missing-link, forged-output, non-adoption, revocation, and private-AgentMind exclusion tests |
| `NetworkProfileProjection` | 1 | independently opted-in server projection containing only currently published fields | person | off/draft/live/paused/delete, field-preview, source-drift, and projection-minimization tests |
| `TalentSearchGrant` | 1 | revocable, expiring, organization-member-specific `talent_searcher`; owner/admin grants no implied search access | current organization capability administrator under separate policy approval | expiry, revoke, role-change, session-switch, opaque denial, and rate-limit tests |
| `NetworkSearchReceipt` | 1 | body-minimized audit of original-query digest, safely redacted policy decision, visible structured interpretation, surfaced projection revisions/reasons, route, and cost | server policy; recruiter cannot edit | prohibited-query, abstention, deterministic-filter, audit-redaction, replay, and extraction tests |
| `ContactRequest` and block | 1 | purpose-bound request; no contact channel before exact acceptance; person/org block fences search and contact | recipient person; sender may withdraw only its own pending request | accept/decline/withdraw/expire/block, spam-limit, race, and channel-nondisclosure tests |

The canonical implementation may split storage tables or service objects, but it
must not merge controllers, infer one authority from another, or let a client
assemble a broader projection. Every contract records schema/policy revision,
actor, correlation, idempotency, prior/new revision, created/effective time, and
body-free audit evidence where applicable.

Revision 1 uses one narrowly privileged application-owned PostgreSQL writer and
serializable transactions as the primary mutation boundary. Database roles must
deny direct client, worker, renderer, and provider writes. Row-level security is
required before any independently credentialed tenant writer is admitted; its
absence in the single-writer phase is an explicit defense-in-depth limitation,
not permission to trust caller-supplied tenant IDs. Cross-tenant transaction and
connection-pool reuse tests remain hard gates in both phases.

#### Disclosure and verification tiers

Lifecycle state and evidence origin are independent. `draft`, `published`,
`paused`, `revalidation_required`, `withdrawn`, `revoked`, and `superseded` never
become verification labels. A viewer sees one of these evidence labels:

| Label | Minimum evidence and issuer | Permitted released fields | Viewer copy and downgrade |
|---|---|---|---|
| `self_described` | person-authored statement; no organization signature | person-approved narrative, work modes, coarse dates, open-to preference | visibly “Self-described”; never “verified”; removed on person withdrawal |
| `organization_verified_opaque` | active signed attestation whose evidence manifest remains current | category, contribution verbs/role, coarse date, verification status; issuer only with approval | “Organization verified; underlying evidence is private”; source drift fences immediately to unavailable, not self-described |
| `organization_verified_redacted` | opaque requirements plus approved redacted outcome/artifact fields | only exact released-field manifest; every named detail satisfies the aggregation rule below | “Organization verified; details redacted”; any affected approval withdrawal fences the exact field and recomputes the card |
| `public_source_verified` | exact current public source revision/digest and policy-approved verifier | only fields observable in that public source and person-approved narrative | “Verified from a public source”; source removal/drift fences immediately |

Labels bind issuer/signing-policy revision, evidence-manifest digest, released-
field manifest, approval-set revision, issued time, optional expiry, and current
source/ACL/consent/purge generation. Expired or unverifiable evidence is not
silently downgraded. Unknowns remain explicit, and no tier implies quality,
seniority, productivity, employability, personality, or comparative rank.

#### Named-party and field-release approval

A named party is any identifiable customer, collaborator, partner, project,
artifact owner, metric owner, outcome owner, quoted speaker, or person other
than the subject whose identity, confidential work, words, or attributable
result would be released. Each candidate field records its field key, normalized
value digest, source revision, subject, organization, named-party set, required-
approver rule, policy revision, requested expiry, and intended visibility.

The approval lifecycle is `pending -> approved | denied | withdrawn | expired | superseded`.
An approval is valid only for the exact field value digest, source/consent/purge
generation, attestation revision, visibility, policy revision, approver identity
and authority revision, and time window. Stale or concurrent decisions fail;
approval of one field never approves a sibling field or later value.

Release requires all of: subject approval; current eligible organization-
reviewer approval; and every required named-party approval. Policy may allow an
organization privacy owner to approve a coarse non-identifying replacement, but
never to impersonate a named party's approval for an identifying field. Denial
or missing approval returns the exact field to draft. Withdrawal, expiry, source
drift, consent drift, ACL/purge drift, or approver-authority loss synchronously
fences the exact field from profile and search before acknowledgement, then
queues projection/index purge. Already issued receipts remain as body-free
historical tombstones, but direct shares and rendered/exported copies are marked
stale or revoked wherever STRIDE controls them; STRIDE never claims to recall an
uncontrolled recipient copy.

#### Retention, deletion, and derived-index purge policy

The following are maximum defaults pending the named W6 policy approval; legal
hold may retain governed organization evidence but can never keep a person
searchable or disclose a withdrawn field. Production activation requires exact
configured values no broader than this table.

| Data class | Default retention | Synchronous action | Derived purge target | Durable receipt |
|---|---|---|---|---|
| private claim source links and manifests | governed source policy; no copied body | fence on source/ACL/consent/purge drift before mutation returns | caches and candidate projections queued immediately; p95 5 min, hard 30 min | source-fence receipt plus per-store purge attempts |
| active attestation and published card | while active and current | withdraw/revoke/expiry removes card and search field before acknowledgement | projection/index/cache p95 60 s, hard 5 min | revision, affected fields/stores, completion or escalation |
| network profile/search document and semantic material | only while profile is Live and source-current | Off/Paused/Delete/block makes it ineligible before acknowledgement | all lexical/vector/reranker caches p95 60 s, hard 5 min | purge-generation receipt; zero stale-hit replay |
| contact request | pending until terminal or 90 days, whichever is earlier; terminal metadata 1 year | block/withdraw/revoke prevents new delivery before acknowledgement | notification queues and caches p95 60 s, hard 5 min | terminal/contact-channel nondisclosure receipt |
| search receipt | 90 days for abuse/audit, then body-free aggregate only | store query digest and policy-safe redaction; never copy hidden candidate fields | operational caches p95 24 h | retention/purge receipt |
| approval, dispute, correction, revocation, audit, and signature tombstone | 7 years or approved governed-record policy | never searchable; field bodies minimized or separately purgeable | no discovery material; detached bodies follow their source policy | immutable body-free lineage receipt |
| person export | one-time encrypted package, 7-day server expiry | revoke download immediately on delete/correction where controllable | package/blob/cache p95 24 h, hard 72 h | export-expiry/deletion receipt |
| backups | approved immutable-retention schedule; no activation before custody approval | live authority fences immediately despite backup retention | purge manifests applied on restore before serving; aged copies expire by schedule | restore-time purge-generation parity receipt |

Client caches, CDN objects, push payloads, queued jobs, analytics/logs, test
fixtures, exports, backups, lexical indexes, embeddings, and semantic/reranker
caches are explicit stores, never “out of scope.” Purge is idempotent and
restart-safe. Missing a hard target pages the privacy owner, keeps affected
features fail-closed, records the failing store without private body material,
and blocks cohort expansion. Legal hold preserves only the minimum governed
record and cannot restore contact, profile, source drill-down, or search access.

#### Prohibited-search policy — revision 1

Policy runs deterministically before retrieval. It evaluates explicit terms,
semantic/composite requests, exclusions, geographic or employer proxies, and
attempts to reconstruct a prohibited criterion across multiple queries. The
parser emits `allow`, `transform_with_confirmation`, `abstain`, or `reject` plus
the visible structured interpretation. Any uncertainty that could alter the
protected/sensitive verdict abstains before retrieval; the user must confirm a
safe interpretation before search.

Reject protected traits and proxies; health/disability; family/pregnancy;
religion/politics; sexual orientation/gender identity; race/ethnicity/national
origin/citizenship proxies; age or graduation-year proxies except a separately
lawful explicit age requirement; compensation history; inferred availability;
personality/psychographic/culture-fit/loyalty; promotion/termination risk;
productivity/activity/meeting/message/token/response volume; social/network
size; employer prestige; contact response; and any hidden quality or fit score.
“Personality” may transform only to exact person-published work-mode preferences
after confirmation. Free-text query bodies are not copied to candidate records;
audit retains a digest and policy-safe classification unless an approved abuse-
investigation policy requires encrypted restricted text.

Allowed ordering inputs have closed definitions: declared-query match is the
deterministic overlap between confirmed structured fields and published fields;
evidence coverage is the fraction of matched fields with current visible
evidence labels, never quantity of work/activity; freshness is age of the exact
published claim revision within policy-bounded buckets, never tenure, age, or
recent message/activity volume. Ties use a stable privacy-preserving shuffle.
No model may add an unlisted factor. Adversarial acceptance requires zero
retrieval on the frozen prohibited corpus and zero hidden-field influence on
the allowed corpus.

#### Network and organization feature switches — revision 1

All switches are server-authoritative, default false, tenant/cohort scoped,
audited, observable without private values, and fail closed when missing or
stale. Disabling a parent synchronously disables every dependent reader,
writer, job admission, provider call, queue consumer, projection, and index.

| Switch | Depends on | Disabled behavior |
|---|---|---|
| `organization_authority_write` | canonical person mapping and migration parity | no create/join/approve/role/transfer/leave mutations |
| `organization_authority_read` | canonical membership reader parity | no organization/member/coworker projection reads |
| `active_organization_session` | organization authority read and session-revision invalidation | sessions remain on legacy single-tenant path; no client-selected tenant authority |
| `contribution_candidate_detection` | active membership principal and exact-source authority | no detector/job admission or candidate creation |
| `contribution_review` | candidate authority and approved policy revision | no subject/org/named-party review mutations |
| `work_record_private` | active attestation resolver | no private draft/preview reader or writer |
| `network_profile_publication` | approved disclosure policy and purge worker healthy | no publish/live mutation; existing material fenced when false |
| `network_projection_shadow` | `network_profile_publication`; published fields only | no projection/index build, including background jobs |
| `network_search` | Live projection, current `TalentSearchGrant`, policy engine, rate limiter, purge health | no parser/reranker/provider call or retrieval |
| `network_contact` | search independent; current published recipient and limits | no contact admission or notification delivery |
| `network_query_parser_provider` | network search plus separately qualified/authorized route | deterministic structured filters only; no paid call |
| `network_semantic_reranker` | network search plus separately qualified/authorized route | no embeddings/vector/reranker generation or query |
| `person_mymind_context` | separate W5 custody/consent approval | remains false and cannot gate organization, Work Record, or network functions |

Publication, search, and contact remain independently kill-switchable. Turning
off search does not unpublish a profile; turning off publication or a person's
Live state fences that profile from search; turning off contact never reveals a
channel. No switch may be enabled merely by database migration or registry
presence.

#### Seven-person Bonfire migration manifest — revision 1

The migration input set is exactly the existing AJ, Joel, Caitlyn, Tyler, Tim,
Erick, and Tom account records read from the authoritative credential store at
rehearsal time. The private generated manifest must bind, per account, the
normalized credential subject digest, newly minted stable random person ID,
profile/avatar/passkey digests, prior session hashes and expiries, target Bonfire
membership ID/revision/role, and no-extra-account proof. AJ receives `owner`;
the other six receive `member`. It must also bind the Bonfire organization ID,
slug/name, policy/schema/migration digests, source/target high-waters, feature-
switch snapshot, backup identity, rollback identity, and operator/reviewer.

The manifest contains no password, passkey secret/private material, raw session
token, avatar body, or email in evidence packets; a private access-controlled
mapping may retain the minimum normalized account identifier needed for exact
execution. Rehearsal must prove idempotent second run; unchanged credential,
passkey, profile, avatar, and session behavior; exactly seven person roots and
memberships; AJ as sole initial owner; zero extra account deletion; shadow
legacy/canonical parity; restart/restore; and rollback to the untouched input.
Production execution requires AJ's approval of the exact generated manifest,
not merely this transformation contract.

#### Threat model and verification obligations — revision 1

| Threat | Asset/boundary | Preventive control | Detection/response and required proof |
|---|---|---|---|
| account linking takeover or recovery confusion | credential -> global person | random opaque person ID; exact credential-subject digest; separate revisioned recovery authority; no email-as-identity | conflicting-link/recovery race tests; alert and fence both mappings |
| tenant-switch race or confused deputy | session/socket/push/cache/worker -> organization | server-derived membership principal and revision; teardown-before-rebind; client tenant inputs non-authoritative | concurrent switch/revoke race tests across every enumerated surface; stale generation rejected |
| forged attestation or signing-key compromise | organization evidence -> portable proof | signed exact manifest, issuer/policy/key revision, external custody, rotation and revocation | tamper/key-revoke/restart tests; fence all affected claims and publish key incident receipt |
| malicious or complicit reviewer | field and claim approval | separation of subject/org/named-party controllers; exact field digest; immutable audit; dispute/correction | two-actor and stale-authority tests; revoke/supersede without erasing governed history |
| re-identification from coarse fields | redacted portable projection | minimum fields, bucketed dates/metrics, preview/View-as-recruiter, named-party aggregation | k-anonymity/manual privacy review for pilot; remove exact field on risk finding |
| enumeration or timing side channel | profile/org/search endpoints | opaque 404, normalized response shape/timing, signed-in capability, bounded pagination | cross-org/anonymous differential tests and audit alerts |
| scraping, Sybil, or rate-limit evasion | signed-in network/search/contact | person/org/global budgets, velocity and breadth limits, no bulk export, grant revocation | extraction corpus, distributed-rate tests, quarantine and grant revoke |
| prompt/query injection or index poisoning | query parser, public fields, semantic route | deterministic policy before retrieval; published-field schema; no hidden source/model tool; output schema | adversarial parser/index corpus; abstain/reject; quarantine poisoned revision |
| stale cache/index resurrection | source/consent/purge -> derived stores | synchronous eligibility fence plus monotonic purge generation and rebuild filter | restart/restore/replay tests; stale-hit canary; keep search fail-closed on lag |
| harassment, spam, or channel disclosure | contact | purpose/channel-bound acceptance, block, limits, no channel before acceptance | spam/race/blocked-contact tests; immediate sender/org quarantine |
| defamation or dispute abuse | contribution narrative and verification label | source-bound claim, visible origin tier, correction/dispute/supersession, no “best” claims | dispute SLA and reviewer audit; fence contested fields when required |
| deletion versus immutable signature | portable proof and governed record | separate searchable projection, purgeable bodies, and body-free signed tombstone | delete/export/restore tests prove no rediscovery while signature lineage remains |

Pilot privacy `k`, normalized-response timing tolerance, person/organization/
global search and contact budgets, and dispute-response SLA are required W6
policy parameters. Unset, zero, expired, or revision-mismatched values fail
closed: no network search/contact cohort may activate and contested fields stay
fenced where policy requires. Qualification receipts bind the exact parameter
revision and test the limits rather than relying on prose defaults.

#### External approval ledger

| Gate | Accountable decision | Required artifact | Current state |
|---|---|---|---|
| W0 local design freeze | Goal Loop coordinator; independent read-only critic | exact revision-1 section digest, W0 receipt, critic verdict | pending critic; no external product approval implied |
| W4 Bonfire migration and private product activation | AJ as production owner | exact generated seven-person manifest, private backup/rollback receipts, authenticated activation journal/receipt, exact release/rollback, deterministic suite, critic verdict | authorized and completed for the private W4 scope on 2026-08-09; canonical repair/promotion remains separately blocked and was not inferred |
| W5 MyMind custody/consent | AJ plus named privacy/security and independent custody owners | encryption/key/data-flow policy, user copy, deletion/recovery ceremony, custody and restore receipts | not authorized; may be explicitly deferred by AJ only for overall completion |
| W6 Work Record/network qualification | named product, legal, and privacy approvers | signed policy revision covering attribution/export, tiers/copy, named parties, dispute/revoke, recruiter limits, prohibited proxies, retention/purge, pilot cohort | not approved; qualification receipts must bind the approved revision |
| W6 paid provider routes | AJ and intended OpenAI project/billing owner | exact project identity, spend limits, per-route paid-call authorization and usage receipt | not authorized |
| W7 native/privacy/resilience | Apple/TestFlight owner, product/privacy/legal approvers, physical-device participants, custody/DR owners | final-build Apple/group evidence, iPhone/iPad matrix, accessibility/privacy sign-off, restrictive-network and restore/failover receipts | external waiting |
| W8 Git/release/cohorts | AJ | separately explicit commit, push, deploy, and cohort decisions; exact release/rollback; per-cohort kill-switch receipt | W4 commit/push/deploy explicitly authorized and completed; W5-W8 publication/search/contact/MyMind cohorts remain unactivated |

No deadline is invented for an external approver. A missing decision keeps the
dependent gate `external_waiting` or `blocked`; it cannot be inferred from a
local design freeze, older provider attempt, migration inclusion, release,
TestFlight distribution, administrator role, or prior cohort state.

The body-minimized live/local/native W0 receipt is
`docs/evidence/e10/stride-e10-w0-freeze-20260808.json`; its status remains
`deterministic_verified` after a two-round independent critic gate closed every
blocker and major finding.

W0 exits only when the live release/switch/high-water receipts and this revision
are independently reviewed, the generated section/evidence digests are recorded,
the exact local dirty-state boundary is frozen, and every unresolved later-wave
decision is explicitly external rather than silently assumed.

### E10 execution waves and status

| Wave | Deliverable | Dependency and acceptance | Current state |
|---|---|---|---|
| E10-W0 | Reconcile ledger and freeze identity/network authority | refresh exact serving ledger/images/rollback/feature switches/high-waters; reconcile stale claims; freeze person/org plus contribution/attestation/network/search/contact contracts, disclosure tiers, prohibited-search policy, retention/deletion, threat model, and migration manifest | `deterministic_verified` — revision 1, receipt, exact digests, and independent critic PASS |
| E10-W1 | Canonical person, organization, contribution, network, session, and audit authority | transactional max-three/final-owner proofs; revision/CAS/idempotency; exact org/named-party field approval; user publication/pause/revoke; recruiter grant/query/contact audit; purge propagation; every route default-off | `deterministic_verified` — migrations 14-16, receipt, targeted normal/race/PostgreSQL/vet, independent critic PASS; all switches false and no route active |
| E10-W2 | Organization, Work Record, and network product surfaces | web/native self/coworker/network projections; create/join/approve/switch/leave; evidence cards/tiers; organization contribution approvals; network preview/View-as-recruiter/contact/block; no discovery activation | `deterministic_verified` — receipt-bound local/default-off backend, web, native, persistent recovery, critic PASS, and registered-live W1-backed rendered QA; earlier static fixture evidence remains explicitly non-live |
| E10-W3 | Tenant conversion, network shadow, and migration rehearsal | replace singleton/unconditional org ACL paths; build shadow network projection/index from published fields only; revalidation/revoke/purge parity; idempotent offline seven-person/Bonfire rehearsal with AJ owner; no production mutation | `deterministic_verified` — local/offline migration, shadow, durable purge, restart/rollback, and converted authority-path evidence plus three independent critic PASS verdicts; production cutover remains deliberately disabled and singleton-dependent surfaces are explicitly unavailable/pending |
| E10-W4 | Authority-gated Bonfire migration and private org/Work Record/network activation; canonical repair remains separately gated | explicit production authority; exact backup/rollback; authenticated activation journal/receipt; seven people and memberships with AJ sole owner; all current sessions bound; cohesive web/native org/profile/private draft/preview while discovery and MyMind remain off | `production_private_live` — exact migration evidence retained without rerun; W4 activation and successor lineage verified; seven people/seven memberships/98 sessions live; registered signed-in desktop/mobile QA PASS; canonical repair/promotion deliberately not performed |
| E10-W5 | Private MyMind custody and activation | independent encrypted custody/consent; inspect/correct/forget/export; restart/restore; exact disclosure and zero leakage; profiles/orgs do not depend on this wave | `external_waiting` |
| E10-W6 | Network, provider, and real-work qualification | privacy/legal/product approval; at least five consenting profile participants and two search reviewers; labeled/adversarial recruiter corpus; explanation/unknown/prohibited-criteria/contact/exfiltration gates; qualify any parser/reranker plus STT, voice, Luna/Terra/Sol, I&O/workforce one variable at a time | `external_waiting` |
| E10-W7 | Physical, privacy, and resilience acceptance | final exact native build containing org/Work Record/network changes on iPhone/iPad; publish/pause/search/evidence/contact/block/revoke rendered flows; restrictive-network TURN/WebRTC; signed restore; index purge; HA failover; accessibility and privacy/legal sign-off | `external_waiting` |
| E10-W8 | Final activation | final route freeze and exact release/rollback; independently kill-switchable opt-in cohort order `draft/publish -> evidence search -> contact`; 24-hour/ten-sitting soak includes revoke/purge latency and prohibited-leakage audit; W5 must be complete or explicitly deferred by AJ, never silently pending | `blocked` on W0-W7 |

The body-minimized W1 authority receipt is
`docs/evidence/e10/stride-e10-w1-authority-20260808.json` (SHA-256
`2b64d45b42ac7f4b61b991b0698588e7b4976043f5c14206a0c3b750fd1baaad`).
It binds the 20-file worktree checkpoint, migrations 14-16, all 13 new switches
false, focused normal/race/disposable-PostgreSQL and `go vet` passes, and a final
independent critic PASS with zero blocker or major finding. It is local
`deterministic_verified` authority evidence, not a commit, release, production
migration, route activation, provider qualification, or cohort approval.

The W2 product receipt is
`docs/evidence/e10/stride-e10-w2-product-20260808.json`. It distinguishes the
earlier static fake-backend schema-admission captures from the canonical
authenticated `registered_live_runtime_w1_backed` browser run, binds all five
registered-live screenshot digests, and records the final normal/race,
disposable-PostgreSQL, vet, web VM, 457/457 native-test, native-typecheck,
persistent-recovery, and independent-critic passes. It remains local/default-
off evidence: no Git, provider, production data/configuration, deployment,
release, physical-device, or cohort action is claimed.

The W3 tenant/shadow/migration receipt is
`docs/evidence/e10/stride-e10-w3-tenant-shadow-migration-20260808.json`. It
binds the exact seven-person/AJ-owner and 15-row disposable migration proof,
separate public/private signed receipts, crash-journal restore, published-only
visible-field shadow parity, current-authority search admission, exact 13-store
durable purge, default-off/shadow runtime bootstrap, converted active paths,
explicitly unavailable pending paths, the full repository suite, and three
independent critic PASS verdicts. This is a local/offline safety proof, not
production cutover authority or proof that pending singleton-dependent paths are
functional in cutover.

The W4 intervention packet is
`docs/evidence/e10/stride-e10-w4-intervention-20260808.json`. It records the
required authority, backup, delta, canary, and rollback contract and explicitly
refuses to treat the W0 count of 119 repair candidates as a fresh exact mutation
manifest.

### Required evidence and human interventions

The implementation is incomplete unless: two concurrent capacity claims produce
at most three active memberships; pending/denied requests grant zero access;
create is atomic; stale/cross-org/non-admin approvals fail; concurrent owner
transfer/departure cannot remove the final active owner; switch/revoke/leave invalidate old socket,
push, cache, Drive, room, brain, Scout, and worker authority; profiles expose no
private email/MyMind/hidden-membership data; restart/restore/replay reproduce the
same authority; and web/native rendered flows pass at zero, one, and three
organizations. Every Work Record card must resolve to an active signed
attestation and exact released-field manifest; source/ACL/delete/purge drift must
retract stale projections; any source-consent revision/change or named-party
field-approval withdrawal must synchronously fence the exact card/search fields;
opted-out/paused/departed/revoked people must not
surface; concurrent publish/revoke must leave no stale searchable revision;
blocked recruiters cannot search/contact; cross-org and anonymous requests remain
opaque; protected criteria and proxies are rejected; bulk extraction is bounded;
deletion purges derived indexes; agent influence requires exact adopted output;
and contract/UI regressions ban rank, productivity, engagement, “contribution
fuel/share,” fit, and personality-score fields.

AJ intervention is required only at the named external gates: confirm the exact
OpenAI billing project and spend controls before paid qualification; provide or
approve AWS S3 Object Lock/KMS, recurring cost, and independent custody owners;
approve the consent/privacy/product policies in §16; participate in physical
iPhone/iPad and restrictive-network tests; identify two eligible reviewers and
real meeting participants for pilots/soak; authorize the exact Bonfire migration,
canonical production repair/promotion, production data/config changes, paid
calls, Git shipping/deployment, and cohort activation when their preflights pass;
and resolve the existing `axx-archive/stride` repository-name collision before
any new primary remote is created. Before network qualification, AJ/product/
legal/privacy must also approve contribution/export and named-party consent,
dispute/revocation, recruiter capability/limits, prohibited search criteria,
discovery/contact copy, retention/deletion, and the opt-in pilot cohort, with at
least five consenting profile participants and two realistic recruiter/search
reviewers.

## 2026-08-06 AmbientMind / AgentMind / MyMind strategic-design delta

This is the canonical design and execution delta for the Ball Dogs Scout
failure and the wider company-intelligence/coworker objective. It extends the
E3-E8 architecture below; it does not create a competing brain or workforce
plan.

### Decision 1: three minds, assembled but never collapsed

- **AmbientMind** is the company brain: audience-authorized organizational
  evidence plus rebuildable projections for conversations, transcripts,
  decisions, commitments, blockers, alignments, storylines, entities,
  artifacts, work receipts, freshness, coverage, and known gaps. It has no
  personality and no single cumulative prose document.
- **AgentMind** is one agent coworker's governed continuity projection:
  independent positions, current-work state, relationship learning, reviewed
  domain learning, and continuity checkpoints. It carries read-only revision
  references to its canonical `TeamAgent`, `AgentCoreProfile`,
  `AgentProfileOverlay`, `AgentAssignment`, and `AgentCapabilityManifest`;
  role, eligibility, access, tools, budget, and assignments are not memory and
  cannot be mutated by learning or conversation. It retrieves company truth
  from AmbientMind and never forks its own divergent version of company facts.
- **MyMind** is one human's evolving brain: private imports, explicit
  preferences, collaboration patterns, corrections, personal continuity, and
  user-controlled disclosures. Every human has a distinct MyMind. Private
  MyMind evidence never becomes AmbientMind merely because an agent used it in
  a private answer.

MyMind belongs to the person, not the employer. The product's durable root is a
global `PersonPrincipal` with one user account and one private MyMind; companies,
clients, projects, and freelance practices are revocable `WorkspaceMemberships`
layered onto that person. Joining, leaving, or switching workspaces changes the
active AmbientMind, role, audience, and policy without resetting identity,
private continuity, relationships, or the person's own contribution history.
There is no organization-wide MyMind grant and no implicit cross-company
retrieval. The person may keep portable preferences, skills, public work,
self-authored reflections, and non-confidential career continuity while
organization-confidential evidence remains bound to that organization's
AmbientMind.

This makes STRIDE the person's long-lived work identity: it should represent
how they think, what they have learned, and what they have contributed across
every company, client, and independent engagement they choose to connect. That
continuity is user-controlled and evidence-backed, not a universal employer
profile. A company can see only the workspace-scoped person projection and
explicitly disclosed portable receipts; it cannot inspect the rest of MyMind,
enumerate other memberships, or infer private work history from hidden graph
edges.

Each organization maintains an append-only, source-linked
`ContributionAttestation` ledger for who originated, shaped, reviewed,
decided, or completed work. The organization retains authorized company
evidence and attribution after departure. By default the person's portable
contribution receipt is opaque: organization signature, source-ledger digest,
date range, and a non-confidential role/category code. Outcome text, project
names, collaborator identities, customer identity, and source excerpts remain
absent unless the organization explicitly approves those exact redacted fields
and every named third party is public or separately authorized. Private source
text, files, prompts, customer data, and internal conclusions never become
portable implicitly. Freelance/client spaces follow the same contract. Public
portfolio promotion is a deliberate field-level export, never a side effect of
changing companies.

At answer time every candidate MyMind source must pass the intersection of
**subject principal × active workspace membership × destination audience ×
declared purpose × source-level consent/revision**. Private MyMind may silently
personalize phrasing in a private answer, but it cannot be quoted, cited,
disclosed, or used as the asserted basis of a shared/public answer without a
separate destination-specific disclosure authorization from the person. The
organization can narrow that authorization but cannot widen it. STRIDE then
assembles an ACL- and freshness-bound context envelope from the current
conversation, relevant AmbientMind projections, read-only AgentMind continuity,
and only the MyMind sources that passed that exact turn's intersection. The
envelope is ephemeral and source-linked; assembly is not copying or widening
memory.

Account recovery, tenant membership, MyMind custody, organization export,
departure, and deletion are separate authorities. Removing a membership revokes
future organization access immediately, preserves attributable company history,
and leaves the user's portable MyMind intact. Deleting a global account requires
an explicit custody/export flow and cannot silently erase another
organization's lawful shared record or leave orphaned authorship.

The product shell is therefore person-first and organization-scoped: signing in
opens the same human account and private continuity, while switching company,
client, or personal/freelance space changes the active organization policy and
audience. A user who is between companies still retains their account, private
context, authorized portable receipts, and ability to do independent work. The
user-facing **Work Record** shows the contribution graph they chose to publish;
plain-language Personal context and memory controls in Settings govern the
behind-the-scenes private history. Every item must show one of three portability
states: **private to me**, **shared inside this organization**, or **portable
receipt/network-visible work**. This is an evidence-led record of a person's
thinking and impact, not an employee productivity score; private reflection, message
volume, and surveillance-derived activity are never converted into employer
performance rankings.

### Decision 2: continuity is a projection, not a larger prompt

Scout is not currently equivalent to a long-running Codex conversation. Raw
chat history is tail-bounded, and recent live meeting analysis is stale. Before
older turns leave the raw window, STRIDE maintains a revisioned
`ConversationContinuity` projection containing current intent, resolved and
unresolved references, established positions, disagreements, corrections,
open loops, active work, and source/high-water references. A correction,
deletion, source revocation, or audience change invalidates and rebuilds the
affected checkpoint.

Typed workers maintain separate authoritative projections rather than one
omniscient "need to knows" writer: meeting state, decisions, commitments,
actions, blockers, alignments, storylines, entity relationships, conversation
continuity, and company-state change. A thin company briefing may be generated
from those projections, but it is disposable and never the authority.

Every conversational agent turn receives, within a measured token budget:

1. agent identity and independent-judgment constitution;
2. recent verbatim turns and reply ancestry;
3. the current `ConversationContinuity` checkpoint;
4. targeted AmbientMind evidence and active work;
5. relevant AgentMind and authorized MyMind relationship context;
6. source revisions, ACL/audience, freshness, coverage, high-water marks, and
   known gaps.

### Decision 3: the Marketplace includes real onboarding

Hiring does not create a second lifecycle beside `TeamAgent`. Marketplace
`available` is a listing state outside TeamAgent; selecting it creates the
existing authoritative TeamAgent lifecycle and uses its exact states:

```text
MarketplaceListing.available -> TeamAgent.draft_hire
draft_hire -> trial_pending -> trial_active -> review_required -> active
draft_hire ---------------------------------> review_required -> active
active -> paused -> active
active|paused -> offboarding -> offboarded
any nonterminal -> quarantined
```

`onboarding | needs_correction | ready` are readiness results attached to the
canonical `review_required` TeamAgent revision, not another seat state, and
`ready` is required for `review_required -> active`. Decline, expiry, quarantine,
and recovery retain the exact canonical transitions in §7.10. Only the canonical
lifecycle service may transition TeamAgent states; AgentMind stores their signed
revision references and cannot activate itself. During onboarding the agent
receives only the approved AmbientMind scopes and
builds a role-specific `AgentOnboardingPack`: company/role map, relevant active
storylines, current decisions and blockers, vocabulary, important people and
projects, initial hypotheses, explicit unknowns, and source references. It does
not ingest the whole company into every prompt. The understanding review tests
factual grounding, access boundaries, role judgment, contradiction handling,
and disclosure of gaps. Humans may correct the pack; activation requires a
passing current revision. Material access, role, or company changes can trigger
a bounded refresh without erasing stable AgentMind identity or MyMind
relationships.

Marketplace cards and coworker detail show onboarding scope, progress,
freshness, unresolved questions, last refresh, and readiness evidence. Hiring
does not subscribe the coworker to every channel or meeting.

### Decision 4: engagement follows conversation topology

- Private Scout: every human turn is eligible.
- Public top-level messages: `@Scout`, Ask Scout, or explicit assignment remains
  the default written-response door.
- A direct reply to Scout is automatically read without another tag and may
  produce `reply`, `reaction`, or `no_action`.
- AmbientMind ingests authorized public/project events continuously, but visible
  proactive participation is a separate event-driven policy with channel mode
  `off | quiet | active`, relevance, confidence, deduplication, interruption
  cost, per-agent assignment scope, and ordinary provider-usage accounting. Do
  not simulate blind interval polling, impose arbitrary per-agent hourly caps,
  or let every hired agent lurk everywhere.

Start with reactions/silence and quiet response suggestions. Autonomous written
interjections remain gated until context correctness and interruption-cost
evals pass.

### Decision 5: useful disagreement is part of AgentMind

Scout and every opinion-bearing coworker must treat truth and usefulness as
higher priority than agreement; distinguish fact, inference, personal judgment,
and ratified company decision; state the strongest counterargument; challenge
the current framing when evidence warrants it; and change position when the
evidence changes. This is not performative contrarianism. Candor/sycophancy
fixtures are a seat-qualification gate.

### Decision 6: work recedes; results arrive in chat

After approval, one compact activity rail above the composer shows the agent,
human-readable current action, real phase, elapsed time, and expansion control.
Do not also post a permanent full run card. Do not show invented percentages;
only measured stage progress may use a number.

Completion posts a normal coworker message with outcome, preview, provenance,
and **Open**, **Save to Drive**, and **Discard**. Save opens Recent, My Drive,
Shared, folder selection, and New Folder, then creates one stable ACL-bound
reference for everyone in the destination audience. Discard reauthorizes the
artifact revision and audience, requires two confirmations for delete-for-all,
tombstones the unsaved result, and retracts derived chat/search projections. A
Drive-saved object is distinct: removing its chat result never silently deletes
the saved Drive copy.

Capability routing is provider-neutral: modality, required tools, evidence,
privacy, output contract, budget, measured quality, and fallback choose the
adapter. Web/X search, research, images, video, slides, documents, and code are
capabilities, not model names exposed to coworkers. Grok/X and video remain
separately qualified adapters, not assumptions.

### Decision 7: memory stays backstage; Work Search and attention are the product

The earlier user-facing name **Ask AmbientMind** is superseded. **Work Search**
is the natural-language surface across authorized meetings, public/project
threads, decisions, artifacts, Drive, storylines, and work receipts. AmbientMind
remains an internal implementation boundary. Answers lead with synthesis,
citations, freshness, coverage, and known gaps. Browse sources remains
secondary. Results can be continued privately, added to a new public/project
thread, or added to an existing channel only after previewing and reauthorizing
the evidence bundle for the destination audience.

**Intelligence** answers "what requires attention and why": evolving
storylines, blockers and alignments, decisions and commitments, contribution
maps, active work/results, value/spend/latency by project/person/category, and a
personalized "what changed since" view. Ingestion counters and worker health
move to an admin/system-health surface.

### Verified baseline and immediate defect

- The live Ball Dogs reply was a deterministic Board misroute. The phrase
  "form your own take" activated the broad ownership marker and "trying to do"
  activated the raw `to do -> Backlog` classifier, producing the exact Backlog
  card answer before the conversational model ran.
- Public-channel retrieval also ranked the serialized channel envelope rather
  than only the human-authored message, polluting recall with generic metadata.
- Direct replies to Scout already auto-engage and can choose a no-response
  marker; top-level untagged human conversation remains human-first.
- Production raw transcription has recent speaker-attributed material, but
  `meetingDigest` is disabled with dead letters and the meeting/day/company,
  decision/entity, narrative, mission, brain, and recap projections are stale
  or degraded. Recent meetings therefore do not currently have trustworthy
  complete analysis.
- Work activity and Drive save primitives exist, but duplicated work cards,
  folder choice, and governed delete-for-all are not yet one coherent flow.

### E10 nested change set — no second wave ledger

The canonical wave remains **E10** in §18. These are dependency-ordered E10
remediation/workstream checkpoints, not A-waves and not a competing release
plan. Every live activation inherits E0/E9 integrity, exact-release,
receipt/ledger agreement, rollback-bundle retention, and E10 acceptance gates.

**Activation mandate:** default-off is only a fail-closed construction and
rollout state; it is not the intended product destination and does not satisfy a
checkpoint outcome. Every approved capability in this plan must finish fully
implemented, provider- and authority-qualified where applicable, activated in
the real product, observable, rollback-proven, and accepted on its actual web,
mobile, meeting, and coworker surfaces. A capability may remain off only while a
named prerequisite is genuinely unresolved or when AJ explicitly defers it.
Code presence, migrations, shadow tables, hidden routes, disabled workers, and
passing isolated tests must never be reported as “done” while the user-facing
behavior is dormant. The active workstream owns removing those temporary fences
in dependency order and must record the exact blocker and activation proof for
anything that cannot yet be turned on.

| Checkpoint | Outcome | Dependencies | Acceptance gate | Explicit rollback | Status |
|---|---|---|---|---|---|
| E10-R0 | Repair Ball Dogs routing, causal deletion, cleanup, and independent answer | current exact serving receipt and E10 release authority | focused/full tests; both public mention and direct reply call the model once; authored-only retrieval; caused answer retracts from chat and canonical projections; useful live answer | reactivate retained prior exact release; do not rewrite named-volume data | `completed` — exact release `ba9bca4a0392085df93f5dcd0607cba547260289`, ledger generation 16, durable cleanup, accepted independent answer, and causal lineage verified live |
| E10-R1 | Restore meeting-intelligence truth and replay recent authorized meetings | R0 plus E10 provider/quota and source-custody authority | fresh source-anchored transcript/decision/action/blocker/storyline receipts; honest gap/coverage state | disable unhealthy worker, retain raw transcript and prior projections, replay from last verified high-water | `in_progress` — the deployed runtime is `mode=off`; the admin-only, digest-bound planner/executor foundation and non-mutating reviewed stage-runner adapter pass focused normal/race/PostgreSQL/DR gates, but production planning remains fail-closed until authenticated approval/rollback fence authority is installed; no provider replay or canonical projection promotion has run |
| E10-R2 | Run canonical AmbientMind projections in shadow | R1 and E1 canonical contracts | rebuild/revoke/restart parity, high-water/freshness agreement, zero ACL leakage | turn off shadow consumers and rebuild from immutable evidence | `blocked_on_canonical_typed_source` — immutable typed event/source/node/edge/state/checkpoint persistence and deterministic revoke/supersede/rebuild/restart/ACL tests pass in memory and disposable PostgreSQL; historical-source revocation now preserves the newer logical revision. Production activation remains blocked because no globally ordered, authorized typed projection input provides stable logical ID, ACL snapshot, kind, freshness, supersession, and a cursor checksum for standalone invalidations. Raw chat and R1 execution-local output cannot safely fill that contract; migration 13 remains false and no reader/writer is active. |
| E10-R3 | Prove person/membership/MyMind metadata authority | E1 authority contracts plus E0 custody proof | principal × workspace × audience × purpose × consent tests; recovery/departure/export/delete; zero cross-company/private leakage | keep every consumer fenced and preserve the legacy account path | `contract_verified_default_off` — body-free in-memory policy, schema, and tests exist, but no route/worker/provider/current account path consumes them and encrypted body custody is absent; this is not a user-ready individual or MyMind |
| E10-R3a | Productize global people and safe profiles | R3 and W0 schema/migration freeze | stable person mapping; self/member/admin field ownership; no hidden-membership/MyMind leakage; web/native rendered proof | keep legacy profile projection; disable canonical profile readers | `not_started` |
| E10-R3b | Productize governed multi-organization membership | R3a | create/join/approve/deny/switch/leave/transfer; transactional maximum three; final-owner-transfer and cross-tenant corpus | keep organization feature off; retain Bonfire legacy authority until one atomic cutover | `not_started` |
| E10-R3c | Activate private MyMind custody and controls | R3a, R3b, independent custody/consent | encrypted body custody; inspect/correct/forget/export; restart/restore; exact disclosure and zero leakage | revoke MyMind consumer tokens; keep body-free metadata and person profile | `not_started` |
| E10-R4 | Add `ConversationContinuity` and the three-mind context envelope | R2, R3c for activation | long-thread correction/delete/revoke/rebuild tests; read-only authority refs; freshness/gap disclosure | disable continuity/context-envelope consumers and fall back to bounded raw turns plus authorized AmbientMind retrieval | `split_state` — ConversationContinuity corrections are released; body-free context integration is verified, but the real three-mind envelope cannot activate before R2/R3c authority and real-surface acceptance |
| E10-R5 | Add AgentMind candor and reply/react/no-action participation | R4 | sycophancy, context, confidence, interruption, audience, and channel-policy corpus | restore explicit-invocation-only policy and prior signed profile revision | `split_state` — source-linked AgentMind positions, correction/supersession/forgetting, coworker constitution, Luna/max routing, and quiet-mode proactive classification/revalidation/dedupe/lifecycle/usage are released. Provider qualification, the policy corpus, and rendered active-mode acceptance remain external gates. |
| E10-R6 | Add Marketplace onboarding packs to canonical TeamAgent lifecycle | R3, R4 and E8 package/lifecycle receipts | scoped access, factual grounding, gap disclosure, correction, refresh, pause/quarantine/offboard gates | pause seat, revoke runtime principal/pack, restore prior package/profile/capability revisions | `in_progress` — the canonical workforce runtime now stops route activation at `review_required`, keeps access revoked, and requires a separate idempotent human review receipt before `active`; replay/restore and internal-preview receipt chains preserve that state. The product preview's simplified lifecycle mirror, onboarding-pack compiler, readiness refresh, and real UI/provider activation remain pending. |
| E10-R7 | Collapse work UI to one result flow and complete Open/Save/Discard | R0 and E4/E6 artifact/Drive authority | desktop/mobile rendered QA; stable Drive reference; revision-bound, double-confirmed discard and projection retraction | restore prior renderer; keep artifacts/Drive copies intact and hide new discard action | `split_state` — web proposal/result/Drive/document-tag UX, Open/PDF, named Save, editable Regenerate, folder navigation, and file rename are live and signed-in verified. Backend disposition authority is verified. Remaining scope is Discard end-to-end activation, final native physical-device acceptance, and separate destination contracts for Canvas opening turns and in-room attachments. |
| E10-R8 | Ship Work Search, attention-led Intelligence, and scoped contribution views | R2-R5, especially R3 disclosure authority | cited authorized answers, destination reauthorization, freshness/gaps, non-surveillance contribution truth | disable new views/queries and retain source systems plus admin health | `in_progress` — the surveillance-shaped activity-volume “contribution fuel” ranking is removed from the live member payload and UI; cited Work Search, attention views, and evidence-backed scoped contribution attestations remain pending R4/R5 authority |
| E10-R9 | Ship the opt-in Work Record and evidence-backed people network | R3a-R3b, R8 contribution authority, and explicit organization/person/named-party disclosure policy; private MyMind activation is not a dependency | signed/redacted contribution attestations; source/consent/field-approval drift retraction; separate coworker/network projections; prohibited-query corpus; explainable search; consentful contact; departure/revoke/purge; zero confidential or mind content; no scoring/feed | independently disable publication, search, and contact; purge derived index; retain org evidence, private MyMind, and signed tombstones separately | `not_started` — no ContributionAttestation/NetworkProfile/search/contact contract exists yet; legacy contribution-fuel ranking code/renderer must be removed and regression-banned before launch |

### Historical E10 substate and authority queue — superseded 2026-08-08

This 2026-08-07 substate is retained as historical evidence. It is superseded by
the 2026-08-08 generation-30 W0 receipt and §18 resume point: exact live commit
`d4c827c...`, current images, retained rollback, and canonical high-waters are
now refreshed. Canonical shadow remains unhealthy at 28,626 versus 8,532 with
119 repair candidates; replay remains off, STRIDE runtime/MyMind activation
remains fenced, and offsite custody remains dormant. R1 remains the truth-restoration gate
because no governed production replay has executed. R2's reducer/store is
verified but its production compiler is blocked on a canonical typed source
contract. R3 is a metadata-only authority proof; R3a-R3c are the productization
work defined above. ConversationContinuity and AgentMind repairs are released,
while the three-mind envelope, active proactive acceptance, remaining artifact
actions, R6/R8/R9, and the external provider/device/media/HA/custody/pilot/soak
gates remain pending at their exact scopes.

The live repair proved two independent failures. First, routing and retrieval
were classifying the structured public-channel envelope rather than only the
human-authored turn, which allowed ordinary strategic language to trigger the
legacy Board shortcut. Second, the corrected strategic answer hit the former
800-token Responses cap; public coworker chat then suppressed the
`max_output_truncation` error and rendered fuzzy Memory hits as though they
were Scout's answer. Conversational Scout now requires a real model result,
never the raw-memory fallback, and has a 2,400-token shared reasoning/output
budget for strategic answers.

Seven failed messages were removed through exact, admin-only, durable
moderation receipts: the original five-message failed exchange plus the first
post-release retry and its memory fallback. No named-volume record was edited
directly. The accepted AJ prompt is
`scout-chat-message-1786042346580793902`; Scout's grounded independent answer
is `scout-chat-message-1786042364030812344` and durably carries the former as
`CausedByMessageID`. Scout treated the pitches as two different companies,
separated near-term attainability from owned-IP upside, mapped distinct capital
and talent paths, and challenged licensing, altcast, Fanatics, and proof-point
assumptions. Unrelated production records and `stride-site/` remain untouched.

E10-R1's 2026-08-06 live read-only audit proves transcription is current but
analysis is not. The authoritative volume contains fresh consent-bound
transcripts through August 6, while the latest brain/decision/narrative outputs
stop around August 4 and meeting/day/company digest continuity stops around July
11. The pre-release environment had `MEETING_DIGEST_DISABLED=true` together
with `MEETING_DIGEST_BACKFILL=1`; the exact release disarmed that broad backfill
flag before activation while leaving the worker disabled. The 51 meeting-digest dead
letters are 17 repeated failed passes over each of three older meetings, not 51
distinct recent meetings. Raw transcripts and prior projections remain intact;
only the unsafe broad-backfill runtime flags were disarmed.

The deployed E10-R1 safety repair makes the missing proof explicit and closes a
restart loss-risk. Ambient checkpoints carry their input/artifact/cursor
contract; baseline and held-window IDs must resolve to the correct input kind,
room, and order; legacy cursorless artifacts normalize to a real input cursor;
an invalid held window fails closed; and an invalid non-held checkpoint repairs
only from an unambiguous durable worker cursor. Readiness now distinguishes
configuration from a running supervisor, records real poll liveness, reports
continuity scope health without raw message IDs, exposes `analysisReady`, and
degrades the exact disabled-with-backfill-armed state. The continuity candidate
passed focused, broader ambient, focused race, and full repository tests before
its release. Live readiness now honestly reports `analysisReady=false`: brain continuity is structurally
healthy but stale, meeting digest remains disabled, and ambiguous legacy
cursors keep the affected Board/Narrative scopes fail-closed instead of
silently skipping evidence.

Replay remains a separate governed operation. Before any provider call, produce
one dry-run manifest containing the exact authorized meeting/source IDs, start
and end high-water marks, projected calls/tokens/cost, current artifact cursors,
consent/revision checks, excluded sources, and rollback floor. Activate one
exact reviewed release with broad backfill flags off, prove each supervisor and
checkpoint healthy, then replay a bounded oldest-first slice and require fresh
source-linked brain, decision, action, blocker, narrative, and digest receipts
before widening. A stale downstream artifact may continue to consume previously
durable upstream work, but it cannot claim the newest meeting is incorporated
while an upstream continuity gap remains.

The post-release replay audit confirms that no safe production execution door
exists yet. `/api/admin/brain-projection/backfill` rebuilds only the canonical
sanitized projection; it does not regenerate brain, decision, narrative, or
digest artifacts. Every `*_BACKFILL` flag resets that worker to unbounded
history, `runAmbientAgentOnceLimited` limits batch size but cannot bind an
explicit start/end fence, and the close-flush chain follows live cursors and
includes Board mutation. The reviewed replay stage runner now supplies the
missing bounded, digest-bound, non-mutating provider/deterministic stage seam
and source-linked output digest/usage receipts, but it cannot be reached by
production configuration while authenticated approval/rollback authority is
absent, and its execution-local output bodies are not yet canonical projection
state. None of this is permission to run a provider replay.

Implement E10-R1 as an admin-only, two-phase
`ambient-intelligence-replay/v1` planner/executor. The provider-free planner
freezes one digest-bound manifest with release identity, tenant/room/sitting,
oldest-first source IDs, start-after/end-at high-waters, content revisions and
digests, ACL/consent/purge state, exclusions, artifact/sidecar cursor digests,
selected DAG stages, prompt/model/output ceilings, call/token/cost ceilings,
authorization TTL, and rollback floor. The executor accepts only that approved
manifest digest, uses an isolated replay cursor namespace, revalidates every
source and authority fence before each provider call, rejects drift, and lets
downstream stages consume only artifacts produced by that manifest. Board is
excluded by default. The first production slice is one oldest stale authorized
sitting with brain windows of at most 48, followed by decision, mission,
narrative, meeting digest, deterministic day fold, entity ledger, and company
digest; every stage must leave a source-linked receipt before widening. If a
distinct blocker receipt remains an acceptance requirement, add a typed
blocker/status projection instead of treating an inferred narrative sentence
as proof.

### Country+Golf release carrier — 2026-08-05

- The implementation baseline is
  `65c3948bed19c3e469c62c762aa653e96bc76027`, descended from the requested live
  baseline `374150b0bf6651dac1a3c717957fc2f8da463187`. Build 38 is the finished
  predecessor at `22b9c6e30723ca5b2a329d2632d5bc5966c678df`; Build 39 is the first
  native carrier for this complete tree. Git, VPS, Apple processing, intended
  `Team (Expo)` availability, and physical-device acceptance remain separately
  evidenced release gates. `stride-site/` is excluded.
- Desktop and mobile now share the finished product contract: polished
  STRIDE/Bonfire shell, compact room lobby, responsive/resizable channels,
  rich media and GIPHY recovery, bottom-first smooth feeds, adaptive composer,
  durable reply threads with edit/delete, liquid-glass reactions, anchored
  notification controls, dual-use reply/activity context, three-state Board,
  ACL-bound Drive, visible agent work, and compact completed-deliverable
  View/Save to Drive actions. Completed areas are verification-only; this
  checkpoint does not reopen them for another redesign pass.
- User memory is not a one-time import. A person may import repeatedly from
  another assistant; identical imports are idempotent, changed exports merge,
  and there is no per-line or per-entry character ceiling. Bounded aggregate
  request/storage and runtime-context limits prevent bloat. Imported/private
  memories remain user-scoped and corrigible, while public chats and recorded
  meetings contribute speaker-attributed audience-bound evidence. Scout,
  Colton, and future agents receive one permission-filtered evolving view of
  the human acting now plus the current company context; they do not flatten
  coworkers together or copy private imports into shared work.
- Scout is the chief-of-staff front door. He may recommend Colton for research
  or prepare an invitation, and a human must approve the join. Colton's durable
  identity, first-person personality, research role, and distinct voice route
  are integrated. Live specialist audio remains honestly unavailable until the
  external provider/session, transcript transport, SFU publication, accounting,
  and E10 qualification receipts pass; application wiring is not provider
  qualification.
- AnyDoc is closed for this release: it is not adopted as the default parser or
  an agent dependency. A future opt-in, pinned, isolated server-side extraction
  canary may be evaluated only behind tenant/object ACLs and immutable revision
  digests; scanned-PDF OCR remains a separate capability.
- Integrated evidence is green: full Go (`399.402s`), focused Go race
  (`96.838s`), all 402 native tests, native typecheck, 33 media-harness checks,
  23 brand checks, inline parsing, patch checks, and rendered desktop/mobile
  acceptance for home, chat, replies, notifications, rooms, Board, Drive, and
  light/dark shell. Rendered QA used a disposable local data root. It did not
  read, write, migrate, delete, or repair any record in the live production
  named volume.
- E1-E9 remain `deterministic_verified` only in their existing local/default-off
  evidence classes. E10 is `product_release; external_acceptance_waiting`:
  physical device/room/transcript/File acceptance, live Colton voice/provider
  qualification, HA/DR, immutable custody, and soak remain open. Canonical Board
  repair and unrelated `stride-site/` work remain explicitly out of scope.

### Historical Country+Golf checkpoint — 2026-08-04

- The reviewed implementation commit is
  `9d73be4957e8005e7ebbf950b1d956b8e427943f`, based on the exact requested live
  baseline `374150b0bf6651dac1a3c717957fc2f8da463187`. It is live from a sealed
  committed-tree bundle on the DigitalOcean VPS; the separately owned
  `stride-site/` tree was excluded and remains untouched. The live canonical
  board file is byte-identical before and after the product cutover
  (`4a499809adda2719169b711cf482744a8bd10cdf1576caeaf743078cb576eceb`,
  20,518 bytes). No Git push is part of this checkpoint.
- Native distribution truth is separate: Build 34 predated this work; Build 35
  is the exact `6e6e714119e976d895f10c34c78426ba717c9e43` room/voice predecessor. Build
  36 is the intended first binary for `9d73be49...` and remains pending until
  EAS completion, Apple `VALID`, intended `Team (Expo)` availability, and a
  fresh physical-device pass are each independently observed.
- Focused physical acceptance on Build 35 records: the home cradle listens and
  talks; one-send dictation works (several seconds of processing remains an
  observed latency, not a failure claim); Scout can be invited; long mobile
  feeds are materially smoother; and a room was stable in the observed sitting.
  Multi-human name invocation, full speaker-attributed recording, Files access,
  and extended room soak remain explicit Build 36 retests. Files/Drive is a
  user-controlled deliverable surface, not a synonym for company memory.
- Desktop and mobile chat now share one first-class contract: responsive shell
  and resizable channel rail, STRIDE/Bonfire organization identity, dual-use
  activity/comments context rail, adaptive mobile composer that grows per line
  and collapses after send, rich link/media previews, visible agent work with
  stages/progress/timer, durable deliverables, and honest terminal states.
  Desktop thread replies omit redundant empty-state copy and preserve the draft
  with an explicit recovery instruction when a connection or cutover interrupts
  confirmation. Pending attachment chips constrain both filename and provenance
  metadata with ellipsis while keeping the remove action fixed and the full text
  available on hover/focus, so long names cannot escape the composer.
  A plain nested reply in a direct-agent thread remains a side conversation:
  it persists immutable reply ancestry but cannot be reclassified as a new
  research objective. Work starts only through an explicit root request,
  artifact follow-up, or selected tool/process control.
- Scout is the default coordinating coworker. Colton is the first approved
  research hire and speaks in first person; Marvin remains a research
  methodologist candidate, not a silently hired employee. Named-agent prompts
  receive one identity, never a Scout-plus-specialist blend. Completed-work
  learnings remain pending and provenance-bound until a human approves,
  corrects, expires, or forgets them.
- Relationship memory has three non-interchangeable lanes. Private imports and
  1:1 agent chat remain bound to that exact human and private thread. Public
  chat and recorded meetings contribute speaker-attributed shared work only to
  their exact audience. Company context evolves from authorized shared work,
  without flattening people into one profile or leaking private imports. Every
  agent run receives the authenticated requester, destination audience,
  source revisions, and current permissions; destination delivery is rejected
  if any recipient cannot read every source. A follow-up by human B uses B's
  current relationship lane and B's File permissions even when human A created
  the original artifact, while the durable v1 authorship remains A. Room runs
  receive the current human's meeting identity and speaker-attributed shared
  evidence, never private Settings imports or private agent-chat memory.
- Missing-input prompts, one approved work launch, Scout-to-Colton delegation,
  live work visualization, proactive follow-up, and durable terminal delivery
  are production acceptance scenarios for this checkpoint. A promise to act
  must either schedule durable work or state that it cannot; conversational
  text alone is never evidence that work exists.
- E1-E9 remain `deterministic_verified` within their prior local/default-off
  evidence classes. E10 now includes an exact shipped product candidate and
  production workflow proof, but it remains open for Build 36 device acceptance,
  multi-human room/Scout invocation, real speaker-attribution corpus review,
  Files/Drive acceptance, provider-seat qualification, soak, HA/DR, and external
  custody. This checkpoint does not reopen or authorize canonical Board repair.

### Active final release checkpoint — 2026-08-02

- The executable implementation boundary **A** is
  `f815edea87525a195cd3f2e2f62fe07f43647a70`. This plan is intentionally
  absent from A; its direct child **B** adds only this release checkpoint, so
  the ceremony can prove identical release-owned inputs across both commits.
- The latest protected qualification completed an exact seven-record repair on
  a disposable cold clone and sealed the expected `+7/+7/+7`, unchanged-board,
  unchanged-spool, seven-to-zero, parity, and idempotent-replay evidence. The
  outer shell gate correctly stopped before production repair because it had
  conflated the full authorized target-set digest with the narrower terminal
  candidate-projection digest. The predecessor was cold-restored and public
  HTTPS/TURN independently re-opened. A now binds each receipt digest to its
  corresponding manifest field, and its self-check uses deliberately distinct
  digests so that category error cannot regress silently.
- The next two-run qualification fully passed run 1 and reached run 2 manifest
  generation before safely stopping. A cold-clone timestamp happened to end in
  trailing fractional zeros: the shell self-digest retained all nine digits,
  while Go's typed UTC timestamp correctly emitted its canonical shortened
  spelling. Production repair was still untouched and the predecessor again
  cold-restored with independent HTTPS/TURN proof. A now canonicalizes every
  fractional UTC timestamp before sealing and propagates the actual one-shot
  exit code immediately, preventing both cross-language digest drift and a
  misleading secondary missing-output error.
- The subsequent protected ceremony passed both full disposable-clone repair,
  restart, and replay qualifications and accepted the exact seven-record
  manifest confirmation. Production append still never began: the stable
  pre-repair fingerprint stopped on a schema mismatch because the operator
  pack read projected status field `highWater` from the persisted reconcile
  checkpoint, whose canonical data field is `high_water`. The exact cold
  restore returned every retired legacy container/volume and all protected
  data to the verified predecessor before public HTTPS/TURN reopened. A now
  parses only the persisted snake-case contract and its executable self-check
  accepts that shape while rejecting the camel-case status projection.
- The next corrected ceremony passed the new exact backup/rehearsal,
  normalization, both full clone repair/restart/replay qualifications, and the
  operator-confirmed production manifest
  `c5a4ec8f517aa52fe5b68bdbafa991f73b0dc06f0922b7d45a88d6e43e20058f`.
  Production append still did not begin: after the irreversible execution
  marker but before the one-shot started, a repair-specific creation check
  incorrectly required the stopped container to appear as an active Docker
  network endpoint. Direct host proof showed that both `docker create
  --network` and an additional `docker network connect` preserve the sole
  configured attachment in container inspect while exposing zero active
  network endpoints until start. The required cold restore returned every
  protected volume, migration, image, and six-service predecessor before
  independent public HTTPS/readiness/TCP TURN proof. A now applies the same
  lifecycle contract already used by normalization and both successful clone
  qualifications: container inspect proves the stopped attachment,
  network-inspect proves PostgreSQL-only membership before start,
  PostgreSQL-plus-one-shot while running, and PostgreSQL-only after exit. The
  pack self-check explicitly rejects the impossible stopped-active-endpoint
  assertion on the production repair path.
- EAS Build 31 finished successfully from superseded checkpoint
  `32d306b1f722cbed6efea4ca6ea0c25f7985f8c6` but was intentionally not
  submitted. Its product/mobile source is unchanged by the operator-only
  correction, but exact-release proof requires a successor build from the new
  checkpoint rather than relabeling Build 31.
- The first sealed Build 31 A/B archive stopped at image compilation, before
  maintenance or any production mutation, because release scope omitted the
  newly imported `internal/e10evidence` package. The scope now includes its
  non-test Go sources, requires an exact package sentinel, and has a regression
  test that inventories every internal package imported by production root Go
  files. The failed pair is superseded and cannot be resumed or relabeled.
- Two subsequent protected attempts passed progressively deeper gates and were
  cold-restored without data drift. They proved that Docker omits any stopped
  container—not only PostgreSQL—from network-inspect active endpoints, and
  that rollback must use the predecessor Compose sources captured from the
  running containers rather than a later mutable `/opt` file. The final pack
  separately proves stopped configured attachment, PostgreSQL-only membership
  before/after each one-shot, and PostgreSQL-plus-one-shot while it runs. Its
  sealed resolved predecessor Compose rollback has restored all eight volumes,
  matched migrations/table counts, recreated exactly six healthy services,
  and passed independent blocked/open HTTPS and TURN probes. Neither failed
  attempt reached a canonical append or can be resumed. A later pre-shutdown
  stop proved restored containers identify their sealed recovery Compose and
  environment as provenance; the pack now reseals that chain only when the
  root-private source environment byte-matches the current live base file.
  Exact-A observation and its scratch-backed read-only plan now preserve both
  the bytes and the presence of an optional lifecycle journal that has never
  been created: safe absence remains absent in the sealed input fingerprint,
  normalization leaves it absent, and only the authorized repair append creates
  it. A disposable clone reproduced the former observation/normalization
  mismatch exactly before this fix and the corrected path passed the full Go
  suite. Symlink or other read failures still stop the ceremony.
  The next disposable clone exposed 46 source-authorized missing/state events
  plus 17 target-only queue jobs. Queue directories are current work indices,
  so file absence cannot authorize deletion of durable job history; current
  queue entries still require exact state and ACL parity. Normalization now
  derives its exact event/outbox delta from the unique sealed missing/state
  candidates and records the exact bounded version-entry delta, while the
  terminal gate still requires only the seven classified board tombstones.
  The canonical database fingerprint also streams ordered outbox rows into a
  constant-memory digest instead of asking PostgreSQL to aggregate all payloads
  into one JSONB value inside its 256 MB cgroup.
  Strict-mode receipt helpers now initialize output variables before deriving
  sibling paths, and the operator-pack self-check rejects a same-declaration
  dependency anywhere in the production shell sources.
  A superseded A/B pair that stopped after build and preflight can now roll over
  directly only when those are its sole phase markers and the unchanged public
  predecessor still proves the exact healthy six-service topology. This archives
  the pre-maintenance state without fabricating a restore or taking downtime.
- The mobile rich-media feed now uses content-family recycle pools, one
  thread-owned long-message sheet, recycling-safe preview state, stable nested
  mapping keys, early image resizing, and a bounded recycle pool. The 264-item
  simulator stress fixture with large images and link previews scrolls without
  swaps, blank cells, jumps, or crashes; final acceptance must be repeated on
  the resulting exact physical-device binary.
- Scout no longer launches automatically when a human enters room media. An
  active member explicitly invites or dismisses him from Agent Team. His
  colored Newton's-cradle tile is a first-class stage participant, reflects
  listening/thinking/talking/degraded state, and his provider output is stored
  once as durable transcript speech attributed to Scout rather than re-entering
  STT as human audio.
- Room transcription and invited agents are separate lanes. Transcription may
  run for a fully consented sitting while Scout is absent. Inviting Scout also
  requires an unchanged audience and every participant's audio-capture,
  transcription, and model-analysis consent; any relevant authority change
  synchronously revokes the invitation.
- The core availability route is OpenAI: `gpt-realtime-2.1` for Scout voice,
  `gpt-live-transcribe` for low-latency Realtime deltas, `gpt-transcribe` for
  composer dictation and authoritative committed meeting turns,
  `gpt-5.6-terra` for typed Scout/routing, and `gpt-5.6-luna` for bounded
  extraction. `gpt-5.6-sol` remains the orchestration and independent-review
  seat at an explicitly selected reasoning level.
- The E10 convergence removes alternate specialist launch paths, owns request
  context at one boundary, binds signed provider/model/voice/config evidence,
  fixes qualification expiry, and fences the externally anchored ledger head
  with compare-and-swap semantics. Deterministic code convergence is not a paid
  provider qualification or employee-agent activation receipt.
- Render subprocesses now run in a dedicated process group and cancellation
  kills the full Chromium tree. This closes the legacy canary-timeout leak that
  exhausted the production render worker's PID budget and is a release-health
  prerequisite rather than a product-surface expansion.
- Remaining release order is intentionally short: push this final A/B
  checkpoint; run the manifest-confirmed canonical ceremony; activate and
  verify exact B on the VPS while preserving production data; build, submit,
  and verify the resulting exact mobile build; then complete physical rich-feed, room lifecycle, Scout
  invitation, dictation, typed-thread, and desktop cradle-motion acceptance.
  External employee qualification, real WebRTC/TURN breadth, ten reviewed
  pilots, the 24-hour/ten-sitting soak, immutable offsite backup, HA/DR, and
  anchor custody remain separate gates.

### Active execution checkpoint — 2026-07-29 through 2026-08-01

- `axx/main` and the local execution baseline were `4a3c0ba6a0c2cacbd10f768e9901033195bf91c5` at E0 entry; `stride-site/` remains untouched and outside release scope.
- Live identity is not trustworthy: the running local image digest, embedded/health version, OCI release label, deployed source hashes, and remote commit do not resolve to one revision. No deploy may claim exact-SHA acceptance until this is repaired.
- Live canonical state is blocked by a deterministic idempotency conflict, dirty/reconciled/checkpoint divergence, and an unknown-to-readiness outbox backlog. Consent authority reports `store_unavailable`; brain/recap/STT/embedding/Scout capability receipts are stale or failed. Provider inference canaries remain fenced.
- At zero occupancy, writers were quiesced from `2026-07-29T04:54:46Z` through `04:54:53Z` and a matched current capture was created for PostgreSQL, meeting data, Codex queue, and usage ledger. The VPS capture and independently copied Mac set match SHA-256 digests: canonical dump `7c9bfcb5be6dfb562db031bc08cc730122e5bf669464ae25f632a7cc4f86b1e5`, meeting data `57075d2efb0b15bb3fc6f45552ab98a1e1d4cb51fdcb74f8888ce32dc27cbcfe`, Codex queue `302ebea03ef8b47aaadb798974f41aac76913ec797713e206a41bdff0f84a776`, and usage ledger `e6f8cd0b49871ac066de3208bc4c727c89411970ec2eef61a572ae695677bde8`.
- The clean four-root bundle was sealed with the new authenticated `BFBKUP02` AES-256-GCM envelope. Plain bundle digest `17f00135cf851ae38918cd852d00d5cfcbe5e6c8e63feb1a7925b6892f78c7f5` round-tripped exactly after decrypting envelope digest `e6fe554add1f5169c0e77197015cae28b85242c1c7d859f0b4fb3dc2f89a1ccf`. The key is held separately with mode `0600`, but independent KMS/custody has not yet been proven.
- An isolated 4-vCPU/8-GB restore host at `143.110.149.164` is firewall-restricted to SSH from the operator IP; PostgreSQL is loopback-only and no public application port is open. All four roots restored with exact manifest parity. PostgreSQL 17 restored `11,090` canonical events, `442` pending and `10,648` delivered outbox rows, seven schema migrations, zero purge-ledger rows, and zero consent rows; a second timed database restore completed in two seconds with the same counts. This proves byte/root/database recovery, not yet purge-authority continuity, an authenticated application boot, signed restore evidence, or the 60-minute full-service RTO.
- A real DigitalOcean Spaces Object Lock probe created a disposable lock-enabled bucket, but configuration returned `NotImplemented`; the empty bucket and temporary access key were then deleted and verified absent. DigitalOcean Spaces therefore cannot satisfy the immutable-custody gate, and ordinary versioning must not be represented as WORM. A qualifying independent immutable store and key custodian remain external E0 gates.
- The live canonical conflict is bounded to a legacy importer defect: collection-level board `updatedAt` was reused as `OccurredAt` for unchanged cards. The candidate restricts drift compatibility to the exact deterministic `legacy.object.imported`/`board_card` family and adds crash-recoverable board lifecycle `PREPARED -> COMMITTED|ABORTED` transactions. An independent critic now reports **PASS**: every mode durably syncs file plus parent directory before commit, ambiguity freezes mutation and readiness, committed-only import remains idempotent, and repeated same-ID delete/restore generations remain ordered. A fresh clone of the captured state replayed the 9,506-event plan with 139 genuine missing imports, 26 allowed occurrence drifts, the five exact board repair candidates, a 149/149 event/outbox delta, zero projection mismatch, and no second-replay change. This is candidate proof, not authority to repair live state.
- The attachment repair now carries exact authenticated source authority and immutable revision through selection, model use, final commit, list/render/open/download, and revocation; upload grants are durable and idempotent; blob serving is fail-closed across check/read races; duplicate source IDs, MIME/size drift, guessed refs, private-to-public widening, recipient change, and stale derived text are rejected. Focused normal/race suites and the independent repeat critic report **PASS**. GIPHY/agent media remains compile-time disabled pending the separate policy/provider/privacy gate.
- Exact release identity now has an independent **PASS** at candidate level: archive, reviewed source, pinned build-input manifest, image, opened binary, OCI/runtime markers, `/healthz`, and `/readyz` must resolve to one release; activation sanitizes inherited release/Compose selectors and rollback requires a retained exact artifact. The release harness passes 9/9. Independent signing, registry custody, and an off-host receipt remain external hard gates, so no qualifying release has been built or deployed yet.
- Provider containment now has an independent **PASS**. Admission and accounting share one atomic scope lock; global workers share the global circuit/pass; durable pre-admission baselines and held checkpoints fail closed on ambiguous persistence; clean-install and legacy migration are evidence-bound; unknown checkpoint versions fail closed; and Taste/House health requires a matching durable typed artifact rather than liveness or a no-op pass. A test-only provider network fence covers every inventoried OpenAI HTTP/WebSocket, Anthropic, and fiscal.ai seam without blocking unrelated link-preview, mail, push, callback, or local HTTP behavior, and an independent critic reports **PASS**.
- Goal-workflow recovery now has an independent **PASS**: approval and terminal surfaces persist atomically; external-work runs reserve deterministic child/job identity before launch; restart recovery is idempotent; legacy random jobs are adopted only on one exact immutable-binding match; zero or multiple matches fail closed for operator reconciliation; and stale callbacks must match child, job, and generation.
- Two full-suite lifecycle races were isolated and closed. The Scout memory-answer test now joins its asynchronous SendEvent/broadcast tail by waiting for zero in-flight tool calls. Separately, `kanbanBoardApp.Close` now stops and joins meeting idle callbacks, and isolated websocket teardown orders socket/server close, handler drain, app/timer join, then actor reset. The first fix passed the exact predecessor/target pair ten times plus a 108-test predecessor sequence; the second passed deterministic lifecycle shutdown twenty times plus all 48 isolated-websocket tests and the implicated share test under race detection.
- Final local candidate evidence at the 2026-08-01 freeze: base `c7b4128f0f45d1b6443c73cbae3e54feceb735d3`; 324-file implementation manifest (excluding this self-referential ledger and the separately owned `stride-site/`) SHA-256 `057290ab5f8ac1e0f279d50bede9cf14189c02f91c986cc2430de15cb392e617`; full non-live-provider Go suite **PASS** with the root package in 310.115 seconds; full Go race suite **PASS** with the root package in 2,311.307 seconds; root Node 102/102; mobile 339/339 plus TypeScript; `go vet ./...`, `go mod verify`, changed-file `gofmt`, `git diff --check`, both production dependency audits, and Expo dependency alignment all **PASS**. No Go source changed after the full-race process began.
- The final deterministic vertical receipt passes in normal and race modes from `#team` through Suggested Work approval, Dog Perfect routing, `insights_opportunities_v1`, artifact/company-brain linkage, Marketplace/Mary lifecycle, Scout introduction, specialist success/failure isolation, signed restart, pause/offboard, and default-off rollback. The separate E9 integration runs the real local application/runtime and signed store against temp-only loopback replicas; it proves route persistence, room-scope control isolation, purge continuity, current restore, and stale-rollback refusal. It explicitly does **not** prove WebRTC/RTP/TURN/media-device continuity, provider quality, physical devices, production HA/RPO/RTO/restore/soak, release, or deployment.
- Native readiness on the final camera tree reports `localReady=true`, `simulatorReady=true`, `strictReady=false`; Xcode tests pass. Physical iPhone/iPad, TestFlight/signing/privacy, locked/background behavior, and provider-integrated acceptance remain E10 evidence. Missing, failed, ambiguous, unavailable, or wrong-device iOS camera proof now strips/stops video before signaling; only affirmative non-risk evidence or valid adaptive portrait/landscape geometry may publish video.
- Desktop private/channel chat is an explicit E4 surface. Rendered local Chrome/Firefox/WebKit QA covered public, private, project, rich-media, Marketplace, and responsive 390/1024/1280/1440/1728 views before the final remediation; the subsequent changes were backend I&O authority/persistence and native camera admission, not desktop presentation code. This is local rendered evidence, not a physical-device, signed-release, or production acceptance receipt.
- `insights_opportunities_v1` now persists its complete report body, immutable revisions, typed feedback, canonical successor WorkRun/Artifact/Outcome lineage, and strict token-free stage/critic receipts across signed restart. Artifact reads reauthorize both source and live destination authority. `private_share` remains intentionally rejected until a separate atomic, durable, revocable grant protocol is approved and verified; a reference is never treated as sharing authority.
- A fresh independent final Critic review reports **PASS** with no actionable P0/P1 after independently rechecking every previously failing area, rerunning the focused camera boundary set 30/30, reconciling the exact normal/race/native receipts, and recomputing the same 324-file implementation manifest digest. No earlier review or narrower passing suite substitutes for this final local sign-off.

---

## 1. Goal

Build STRIDE into a trusted, voice-first company participant that:

1. hosts stable first-class video meetings, guests, multiple rooms, screen sharing, and mobile participation;
2. provides best-in-class microphone dictation in every text composer, with polished feedback, company-aware transcription, explicit delete/submit controls, and unambiguous separation from personal Realtime voice and meeting media;
3. captures every consent-authorized meeting and shared company conversation as time-indexed evidence;
4. understands people, projects, commitments, blockers, decisions, storylines, artifacts, and evolving context across days, months, and years;
5. lets a person ask naturally for what happened five minutes ago, what they missed before joining, or what Erick shared in `#team` last week and receive a fast, permission-safe, source-linked answer;
6. recognizes real desired outcomes in calls and chat, suggests useful work to the right people, obtains explicit approval, routes the work into the correct existing or newly created project thread, runs it durably, and reports completion there;
7. makes Scout feel like a real, funny, context-aware coworker in `#team`—able to understand who said what, retrieve and safely post a requested file, and occasionally use an appropriate GIF—without turning the human group chat into an agent feed;
8. provides a curated internal Agent Marketplace where authorized people can hire distinctive, persistent agent coworkers across Insights, Marketing, Research, Design, and Builder roles, while Scout remains the primary relationship, chief-of-staff experience, and accountable coordination front door;
9. learns how the company collaborates without creating hidden psychological profiles or leaking private conversation;
10. routes every model call and agent stage to an intentionally chosen capability and reasoning level, with measured quality, latency, and cost;
11. remains useful and honest when AI providers, transcription, memory projections, workers, or infrastructure degrade.

### Definition of done

The evolution is not complete because code exists or `/readyz` says `ok=true`. It is complete only when the evidence matrix in §15 passes from one immutable release and production proves the integrated experience:

> Human conversation becomes trusted company memory; Scout feels like a grounded chief of staff, not a search box; people can hire, shape, and work alongside a legible roster of distinctive agent coworkers; agents contribute through controlled handoffs; an approved work outcome becomes a durable verified result in the right thread; guests and unauthorized users cannot observe any source, count, inference, or artifact they are not allowed to access.

---

## 2. Current baseline — point-in-time receipt at 2026-07-28T22:49:06Z

### Repository and release topology

- Remote `axx/main` resolved to `a7e1b6a1082a21383e215069d44dd34fda107cab`.
- The current local design branch is `design/voice-first-mobile` at `604485ce06d9a897e75d7b84ff5b2f2615c52813` and contains substantial later `#team`, link-preview, chat, and native work that is not on live `main`.
- The running container reported OCI revision label `cbc27df1cbd360619ecbd353bd9782cd0a20b358` and image digest `sha256:ac8710894bf3f44a399d2645b79d44ae7b1b15d42f3d66d4908e49dece5612a4`.
- The local untracked `stride-site/` directory is user-owned and must remain untouched.
- The local design branch must not be merged wholesale. Its changes need a file-by-file reconciliation against live `main`, preserving later production fixes and avoiding reintroduction of removed media/native behavior.
- Remote main, the running OCI revision, and currently surfaced environment/health release markers do not resolve to one trustworthy release. Same-release evaluation and deployment receipts do not qualify until OCI labels, embedded binary version, environment marker, source archive, registry/running digest, and health/capability output resolve to one immutable release. The values above are observations, not proof that the running binary was built from the labeled source.

### Proven installed foundation

- Usage/cost ledgers, static route controls, kill switches, and isolated Codex/render workers exist.
- Canonical PostgreSQL shadow infrastructure, event/ACL/consent/retention/approval contracts, outbox/jobs, and purge-aware authorization are installed.
- Per-room actorized media/Scout foundations, admission anchors, temporal evidence contracts, brain projection machinery, `insights_opportunities_v1`, evaluation collection, and HA/DR repository gates exist default-off or shadowed.
- `#team` exists as the flagged permanent public Table thread, with human-first `@scout` behavior, author identity, replies, reactions, files, read markers, push, and source-rendering foundations.
- Its current Scout prompt already requests a casual teammate style, but model history is limited to recent flat `user`/`scout` turns and does not preserve which coworker authored each human message or full reply/reaction context. Mention detection is substring-based rather than lexical.
- The Files surface already unifies direct uploads, authorized channel attachments, and agent artifacts, and native mobile already has an authenticated G-rated GIPHY picker. Neither is currently a Scout tool: Scout cannot safely select an existing Files object and post its exact revision, and it has no context-aware GIF response policy.
- Private Scout threads are intentionally excluded from shared recall and brain synthesis.

### Current live model/config state

- conversational voice: `gpt-realtime-2`, reasoning `high`;
- passive transcript model: `gpt-realtime-whisper`;
- Codex worker: `gpt-5.5`;
- digest production: disabled;
- `insights_opportunities_v1`: disabled.

### Hard entry blockers

The public app is serving and the container is healthy, but production intelligence is not currently acceptance-ready:

- canonical shadow is degraded at observed dirty high-water 9,816 versus reconciled/checkpoint 8,532 with `canonical idempotency key conflict`; these counters are volatile and must be re-read before any repair;
- durable consent authority reports `store_unavailable`;
- embeddings are failing with provider `429` responses;
- brain and recap last succeeded on 2026-07-11 and are stale;
- STT is stale and Scout is disconnected while the room is inactive;
- offsite backup is dormant/unencrypted and restore verification is false;
- provider quota remains an external commissioning dependency.
- recent usage evidence contains untagged Anthropic traffic, failed OpenAI text calls, and repeated embedding failures; new transcription model prices/service-tier dimensions are not yet complete in the repository price table. Cost claims and seat comparisons remain non-qualifying until seat tagging, accepted-output truth, and current prices are complete.
- current chat attachment sanitization validates a supplied blob reference's existence/MIME without binding it to the authenticated source object and caller ACL. Treat this as a security-repair gate: prove reachability, close the authorization downgrade, add guessed-reference/revocation tests, and do not build Scout file posting on the current path.

These facts make Evolution Wave E0 mandatory. They do not authorize a repair or production mutation under this planning request.

---

## 3. Non-negotiable invariants

1. **Conversation is evidence, not authority.** Spoken or typed words can create a candidate or suggestion; they cannot approve work, widen permissions, publish externally, or execute a side effect.
2. **Human approval is revision-bound.** Approval consumes one exact proposal revision and creates at most one idempotent run. Any change to evidence, scope, destination, authority, budget, or workflow invalidates the prior approval.
3. **Authorization precedes retrieval.** ACL filtering happens before body fetch, lexical search, embedding search, ranking, model context construction, analysis, or publication. Unauthorized counts and timing distinctions are also hidden.
4. **Derived knowledge cannot widen visibility.** Every assertion, digest, profile observation, proposal, artifact, and answer inherits the intersection of its sources and destination.
5. **Private Scout chat stays private by default.** It enters company memory only through an explicit share, publish, save-for-the-record, or policy-disclosed conversion chosen by an authorized user.
6. **`#team` is human-first.** The company group chat may feed shared memory, but Scout replies only when explicitly invoked. Proactive assistance is quiet and dismissible by default.
7. **Scout is always available, never always interrupting.** Passive consented capture and analysis do not grant conversational floor access.
8. **Video survives AI failure.** Room admission, media, chat, screen share, and deterministic navigation remain usable when every model lane is unavailable.
9. **No silent coverage claims.** Transcript, analysis, brain, workflow, and artifact freshness are separate high-water marks. Gaps and stale state are disclosed.
10. **No silent model fallback.** A stage may retry its pinned route. An alternate provider/model may restart the stage only from a durable checkpoint under an explicit policy, with full provenance and no duplicate side effect.
11. **One workflow first.** `insights_opportunities_v1` is the only production workforce product required by this plan. Additional authored workflows wait for its pilot gate.
12. **No generalized microservice fleet.** Retain a modular Go control plane, PostgreSQL/event substrate, isolated workers, Pion media rollback, and simple queues. Do not add Kafka or split services without measured necessity.
13. **Production data stays in the named Docker volumes.** Repository `data/` and `/opt/meetingassist/data/` are never production truth.
14. **Every wave is reversible.** Model changes are one-seat/one-variable canaries; data changes are shadowed and replayable; deployment keeps exact-SHA evidence and a tested rollback.
15. **Agent identity is provenance, not authority.** A stable name, avatar, membership, or signed runtime identity explains who acted; it never grants a tool, data scope, approval, budget, or permission to delegate.
16. **Decorative media is conversation, not company truth.** GIFs, reactions, and social tone may inform bounded conversational context, but they do not become asserted knowledge or authorization.
17. **Scout chairs every multi-agent meeting interaction.** Scout is the sole default agent with the meeting floor. A named specialist may join only through a visible, human-confirmed, time-bounded session with an audience-authorized brief, one-agent-at-a-time floor control, no acoustic agent loop, and immediate revocation on dismissal.
18. **Hiring is human authority.** Scout may recommend, configure, coordinate, coach, surface a safety concern, and draft a hire, pause, or offboarding change; deterministic policy may quarantine on a proven safety/cost/health breach, but only an eligible authenticated human can hire, expand access, activate a material update, restore from quarantine, or permanently offboard a teammate.
19. **Growth is evidence, not self-modification.** An agent may accumulate corrigible relationship memory, domain lessons, and performance evidence. It cannot rewrite its core identity, grant itself skills or permissions, claim competence from self-assessment, or silently change its model, prompt, tools, budget, or authority.
20. **Agent packages are untrusted requests, not executable authority.** A listing, publisher signature, popularity, paid status, or installed persona never grants data, tools, credentials, network access, code execution, or company-brain visibility. Organization memory and local personality evolution do not leave the organization in an exported or updated package.

---

## 4. Final product and architecture decisions

### 4.1 Outcome is central; “deliverable” is not

The core internal lifecycle is:

```text
authorized conversation/evidence
  -> WorkIntent candidate
  -> revisioned WorkProposal
  -> explicit approval
  -> durable WorkRun
  -> artifacts + verified Outcome
  -> result and status in the bound project thread
```

User-facing vocabulary is **Suggested Work**, **Work**, and **Results**. “Deliverable” is used only when the requested result is literally a report, deck, website, file, or other tangible output. It is not the central navigation or ontology term.

### 4.2 The company brain is an evidence graph plus rebuildable projections

Raw conversation events and authoritative transcript revisions are evidence truth. Decisions, commitments, storylines, collaboration preferences, summaries, and work opportunities are versioned projections with source edges. They can be rebuilt, corrected, superseded, or retracted.

The system must never rely on one cumulative prose summary as company memory.

### 4.3 Realtime is the conversational edge, not the operating system

The Realtime session manages low-latency speech, interruption, and tool conversation. Server-side STRIDE owns identity, consent, transcripts, retrieval, company memory, permissions, proposal/approval, workflow state, artifacts, verification, and publication.

Realtime receives bounded, audience-authorized context envelopes and tool results. It does not hold the entire company brain or become the durable owner of meeting history.

### 4.4 Meeting transcription has one authoritative lane

- `gpt-transcribe` is the default authoritative model for committed meeting turns and bounded composer dictation. Meeting turns use committed Realtime transcription input; composer dictation records one bounded clip and uses file/request-response transcription with company vocabulary, keyword, and language hints.
- `gpt-live-transcribe` is a provisional UX lane only when a shipped surface consumes ongoing sub-turn deltas: live meeting captions or measured early address detection. Composer dictation does not need it: the recording waveform is driven locally from microphone energy and only final `gpt-transcribe` text is eligible to send.
- A dual lane is justified only for that visible live behavior. It is not required for five- or thirty-minute recall.
- Provisional text never enters durable memory, creates an asserted claim, or surfaces Suggested Work.

### 4.5 Scout invocation is surface-specific

- Main Scout screen: direct conversation; no wake phrase.
- Private Scout chat: every user message addresses Scout.
- Public `#team` and project channels: `@scout` or an explicit Ask Scout control.
- Meeting text chat: `@scout`.
- Meeting voice: natural “Scout …” address or Ask Scout button, followed by a visible short engaged window for follow-ups.
- Proactive work detection: silent suggestion card; no unsolicited spoken interruption by default.

### 4.6 `#team` compounds into shared memory through a separate projection lane

Do not make the whole `scout_chat_thread` JSON searchable and do not remove the private-chat privacy pin. Emit one ACL-carrying `ConversationEvent` per public message, edit, delete, reaction, reply, file, and link, then derive shared knowledge from those events.

`#team` is work-sensing-enabled by default with clear company-visible disclosure. Other public/project channels opt in by policy. Private threads remain excluded unless explicitly promoted.

### 4.7 “Personality learning” means transparent collaboration profiles

STRIDE may learn explicit preferences and repeated low-risk collaboration patterns: expertise, preferred update style, terminology, normal level of detail, working cadence, and team communication norms.

It may not create hidden psychological diagnoses, infer protected/sensitive traits, turn a private emotional moment into durable company fact, or let an inference override an explicit instruction. Every inferred preference carries evidence, confidence, scope, recency, expiry, and correction/forget controls.

### 4.8 STRIDE owns durable orchestration; models perform bounded stages

Goal Loop, Strategic Design, Critic Loop, and Wave Plan become internal `WorkflowProfile` behaviors selected by the orchestrator. Users see outcome, status, evidence, approval, and required decisions—not process jargon.

OpenAI Responses multi-agent or provider agent frameworks may be evaluated for bounded parallel analysis, but beta/provider runtime state cannot replace STRIDE’s durable `WorkRun`, approval, ACL, idempotency, checkpoint, or audit ledger.

### 4.9 Pion remains the production media rollback

Current actorized Pion remains default until a managed media provider proves materially better under the same room, guest, transcription, recording, cost, failure-injection, and rollback gates. Self-hosting another media stack on the same VPS is not HA.

### 4.10 Expo is the canonical native client

The Expo/React Native application is the native product line for this evolution. Older parallel Swift/native Apple plans are reference material, not a second product implementation. Web and Expo iPhone/iPad are required release surfaces. Android receives compatibility coverage where the Expo stack already supports it, but Android GA and a parallel Swift client are not hidden requirements.

### 4.11 Guest-safe mode begins on admission, not link creation

An unused active guest link does not downgrade a member-only sitting. The first admitted guest latches that sitting into guest-safe behavior for the remainder of the sitting, because the guest may reconnect and guest-originated evidence remains part of the meeting record. Guests may receive current-room, audience-safe Scout answers only when the host policy allows; they receive no durable company recall, cross-meeting/project retrieval, work proposals, or approval authority.

### 4.12 Dictation, personal Realtime, and meetings are distinct audio modes

Every text composer exposes the same **Dictate** microphone control. It records a bounded utterance and transforms that same composer rectangle into a real microphone-level waveform with **Delete**, **Stop**, and **Send** controls. Stopping holds the clip; it does not transcribe or send. Delete discards it. Send finalizes the clip, changes the rectangle to an explicit **Transcribing** state with a compact progress indicator, and posts the completed text through the ordinary message pipeline. It is text entry, not a Realtime conversation and not a meeting transcript lane.

Only the final successfully sent text becomes a normal `ConversationEvent` under that destination’s existing visibility and retention policy. The raw dictation clip, partials, failed transcript, and private in-room utterance never enter the company brain or meeting evidence.

The main Scout screen separately exposes **Live Voice**, backed by `gpt-realtime-2.1`. The controls must not look or behave interchangeable. A client-level `AudioFocusCoordinator` owns one microphone capture generation and the mutually exclusive foreground modes `idle`, `composer_dictation`, `personal_realtime`, and `meeting_media`.

- Starting Dictate while personal Realtime is active ends that Realtime session first, records the terminal reason `superseded_by_dictation`, revokes its microphone generation, and only then starts bounded capture.
- Joining a video room ends personal Realtime before acquiring meeting media. If non-room dictation is recording, STRIDE stops and parks it as an unsent local clip rather than transcribing, sending, or silently discarding it. Leaving the room does not silently restart either mode.
- Starting personal Realtime while already in a room is disabled; the room’s Scout experience is the meeting-bound invocation path, not a second personal audio session.
- An in-room text composer may dictate only through an explicit private dictation submode: preserve the participant’s prior mute state, mute the outbound room track before recording, exclude those frames from the meeting transcript/analysis, then restore the exact prior mute state after completion or cancellation.
- Every transition waits for a terminal acknowledgement or a bounded forced close before the next microphone generation starts. Late audio/events from an old generation are discarded.

This is a product-level audio ownership contract across web and Expo, not an incidental UI behavior.

### 4.13 Scout is the primary relationship; hired agents are coworkers, not a bot swarm

Scout is the only general-purpose social agent and the default agent allowed to answer in `#team`. It is the familiar coordinator across the main screen, private chat, meetings, shared channels, and the agent workforce. Hiring an agent adds a teammate to the organization roster; it does not silently subscribe that agent to `#team`, every project, every meeting, or the company brain. A specialist does not lurk in `#team`, respond because Scout mentioned it, or enter a conversation merely because its model would be useful.

Specialists are first-class named agent principals with stable profiles, avatars, roles, visible memberships, and audit trails. They appear only when a human explicitly addresses an already-present specialist, Scout routes approved work into a bound project/work thread, or an eligible human confirms the live meeting invitation in §4.17. Scout introduces the handoff in plain language, the specialist contributes under its own identity, and Scout remains accountable for coordination and completion.

Do not personify every background worker. Transcript projection, entity resolution, indexing, criticism, verification, and safety gates remain system roles unless a recurring human relationship benefits from a distinct visible coworker. The company-historian capability initially remains authoritative retrieval behind Scout, not a second personality with a competing memory. Scout remains the sole default voice agent and meeting chair. A named specialist may receive temporary meeting-floor access only through the explicit consultation contract in §4.17; normal specialist participation remains in bound text/project threads. The first production specialist is the human-approved named Insights analyst used by `insights_opportunities_v1`; Marketing, Research, Design, and Builder profiles may follow for explicit consultation or approved project work only after their own capability and identity-continuity gates.

### 4.14 Personality is governed identity plus evidence-backed relationship memory

Each coworker has six deliberately separate layers:

1. a portable, immutable `AgentPackageManifest` that supplies a publisher-authored role/persona seed, assets, requested capabilities, compatibility, and evaluation bundle, but no organization memory, credential, or authority;
2. an organization-owned, versioned `AgentCoreProfile` plus `AgentProfileOverlay` for the hired teammate's stable name, role, mission, voice, distinctive traits, humor range, values, boundaries, local instructions, and escalation behavior;
3. an `AgentCapabilityManifest` for allowed surfaces, memberships, tools, workflow roles, route policy, budgets, data classes, and authority classes;
4. `AgentAssignment` and `ChannelNormProfile` records for responsibilities, projects, channels, direct-chat behavior, response policy, and local norms;
5. evidence-backed `AgentRelationshipMemory` and `CollaborationPreference` records for corrigible, scoped ways of working with people and teams;
6. `AgentLearningRecord` and `AgentPerformanceReceipt` records for source-linked domain lessons, accepted/rejected work, feedback, demonstrated competencies, and eval-backed growth.

The company brain remains the shared source of organizational truth; STRIDE does not fork a divergent private company brain into every agent. At runtime, each agent receives an ACL-filtered context envelope plus its own scoped relationship/learning records. Learned memory cannot rewrite the core persona, safety rules, tool authority, or approval policy. Personality edits are reviewed organization-local profile revisions; capability changes require server validation and evals. The model may vary behind a stable coworker identity only after continuity and safety evaluation, with the runtime route retained in the audit receipt. A package update never overwrites the local overlay, memory, assignments, permissions, or work history. STRIDE never pretends that a newly swapped model is a new person or that a model's self-description grants capabilities.

### 4.15 `#team` supports contextual rich actions without permission shortcuts

It is feasible and desirable for Scout to respond conversationally with text, one appropriate GIF, or an authorized file/artifact card. An explicit, lexically valid `@scout` invocation authorizes one ordinary reply under channel policy; it does not authorize a permission change, company-memory write beyond the normal posted message, or a work launch.

For a requested file, STRIDE searches only an ACL-filtered inventory, resolves an immutable source object and content revision, intersects source and destination audiences, reauthorizes the requester and every recipient, and posts a durable reference with provenance. If the file is not already visible to everyone in `#team`, Scout offers a private result or a separate explicit share proposal; it never silently widens access. Before this feature ships, current attachment ingestion must stop accepting a client-supplied blob reference on existence alone and require source-object authorization.

For a GIF, Scout may automatically post at most one G-rated result after an explicit `@scout` invocation when the context is low-risk and the channel policy permits it. The server converts context into a short abstract search intent so raw private or meeting text is never sent to the GIF provider. Provider requests contain no email hash, account identifier, stable user-derived pseudonym, source message ID, thread ID, or company identifier. Sensitive, high-stakes, factual, grief, health, legal, financial, security, personnel, conflict, or harassment contexts fall back to text. Humor is situational or self-deprecating, never targeted humiliation. Provider ID, query class, source, alt text, selection reason, and agent/profile version are recorded; decorative media is excluded from asserted company-memory projections.

The selected media is fetched and validated server-side into an immutable, content-addressed, authorized STRIDE blob and served from STRIDE so recipients do not make tracking requests to the provider. The provider page is an optional explicit link, never an embedded load. Agent GIFs remain disabled unless provider licensing and attribution terms permit this relay/cache contract; otherwise STRIDE selects a privacy-compatible licensed provider or catalog rather than weakening the boundary.

### 4.16 Buzz is a design reference, not STRIDE's authority or memory layer

The audited Buzz design usefully separates portable persona metadata from minted runtime identity, supports stable agent authorship, groups personas into teams, and retains catalog-source provenance when copying a shared persona. Its persona schema separates display/role material from skills, runtime/model, triggers, and subscriptions; its managed-agent definition distinguishes a discoverable definition from the locally minted runtime identity; and its team record groups versioned persona IDs. STRIDE adopts these concepts as `AgentPackageManifest`, `MarketplaceListing`, organization-owned `TeamAgent`, and revocable runtime-principal records. See Buzz's [persona schema](https://github.com/block/buzz/blob/90e058ebf68137e048a409aec6616519379ff726/crates/buzz-persona/src/persona.rs#L95-L198), [catalog provenance and runtime identity separation](https://github.com/block/buzz/blob/90e058ebf68137e048a409aec6616519379ff726/desktop/src-tauri/src/managed_agents/types.rs#L15-L156), and [team record](https://github.com/block/buzz/blob/90e058ebf68137e048a409aec6616519379ff726/desktop/src-tauri/src/managed_agents/types.rs#L738-L782).

STRIDE explicitly rejects Buzz's `bypass-permissions` default, automatic `allow_once` tool permission, broad credential/environment inheritance, package-selected executable hooks or MCP commands, prompt-only sibling delegation, model-self-fetched channel history, and agent-authored encrypted memory as company truth. Buzz itself documents unresolved memory-poisoning/key-compromise risk in [NIP-AE](https://github.com/block/buzz/blob/90e058ebf68137e048a409aec6616519379ff726/docs/nips/NIP-AE.md#L152-L163). STRIDE keeps identity, ACL/context assembly, memory, capability mapping, capability tokens, approval, budgets, artifacts, and verification server-owned. A package may request abstract capabilities; an administrator maps only approved requests to STRIDE-native tools after security and eval gates. A specialist runtime receives a short-lived, least-privilege capability token for one bounded run; no transitive delegation exists unless the immutable workflow profile explicitly allows it.

### 4.17 Scout may invite one time-bounded specialist into an internal meeting

The target experience includes Scout bringing a named specialist such as **Mary · Marketing Agent** into a live call for a bounded consultation. This is a conversational capability, not a second recurring workflow product and not permission for a bot swarm. The registry may contain multiple separately gated specialists, but the first release permits Scout plus at most one specialist agent in a member-only sitting. Guest-safe sittings keep specialist voice disabled until a later consent, disclosure, and audience-isolation gate proves that mode separately.

A spoken request such as “Scout, bring Mary in to help with positioning” creates an invitation request; Scout may also suggest “Mary could help with this” as a quiet confirmation card. Neither speech nor Scout's suggestion widens the meeting audience or starts a paid specialist session. STRIDE shows the specialist, purpose, context classes to be shared, expected limit, and current meeting audience. An eligible authenticated human confirms **Invite**. Every participant then sees and hears an unambiguous agent-joined disclosure and a persistent **Mary · Marketing Agent** participant state. A room policy may pre-approve a named specialist and bounded context class later, but the policy remains visible, revocable, and version-bound.

STRIDE—not Scout's model—assembles a `MeetingSpecialistContextEnvelope` containing the exact invitation purpose, authorized recent transcript window, relevant versioned meeting analysis, authorized company/project evidence, active approved work state, source references, coverage and freshness, audience/consent/retention decisions, profile/runtime revisions, and turn/time/tool/cost limits. Prefer authoritative transcript plus structured analysis and the minimum useful company context. Historical raw audio is not forwarded to the specialist by default. Missing, stale, or audience-incompatible context is omitted or blocks the invitation; the model cannot self-fetch around the envelope.

Mary runs in a separate backend `gpt-realtime-2.1` session over the server-to-server WebSocket path documented by OpenAI's [Realtime WebSocket guide](https://developers.openai.com/api/docs/guides/realtime-websocket). The backend owns the API key, session lifecycle, JSON events, audio chunks, tools, and usage. STRIDE publishes Mary into Pion as a distinct verified agent participant and audio track; it does not create a second hidden browser/WebRTC client. OpenAI documents speech-to-speech, configurable reasoning, interruption behavior, and tool use for [GPT-Realtime-2.1](https://developers.openai.com/api/docs/models/gpt-realtime-2.1), but those model capabilities remain downstream of STRIDE's authority and floor controller.

A server-side `MeetingAgentFloorController` is the only route between room media and agent sessions. Humans always win barge-in. Only one agent audio track may speak at a time. Mary receives Scout's structured handoff and eligible human turns, not Scout's synthesized audio; Scout receives Mary's attributed transcript/result, not a looped microphone feed. After Mary joins, the controller may stream the consented human-only live mix for low-latency prosody while sending canonical speaker-attribution metadata at committed-turn boundaries. It excludes every synthetic-agent track, private dictation, non-consenting track, and stale media generation, and it never duplicates one turn as competing audio and text inputs. The controller forbids autonomous Scout↔Mary ping-pong, caps agent turns, wall time, tools, audio, tokens, and cost, and requires a human turn or explicit chair action before another agent turn. Deeper specialist reasoning may run as a bounded server-side text stage and return into Mary's session, but it cannot delay, delegate, or act beyond the invitation manifest.

The lifecycle is `requested -> approved -> joining -> briefed/listening -> speaking -> available -> dismissed|expired|failed`. “Thanks Mary,” **Remove**, consent withdrawal, membership change, budget exhaustion, timeout, or kill switch immediately closes her input/context/tool capability, cancels pending output, ends metering after the terminal provider event, removes the audio track, and posts **Mary left**. Scout's spoken thanks is paired with an allowlisted `dismiss_meeting_specialist` function call bound to the active session; the server does not scrape arbitrary generated phrases for authority. The current sentence may end only within a short configured grace period; privacy or consent revocation cuts immediately. Scout continues the meeting honestly if Mary is unavailable or fails.

Mary's spoken contribution becomes an agent-authored `ConversationEvent` and `MeetingAgentContribution` with exact profile, runtime, model, context digest, source range, audience, timestamps, and coverage. It enters meeting analysis and the company brain only through the same permission-preserving projection lane as other conversation evidence. It is never approval or durable authority. If the consultation reveals real work, Scout may create ordinary Suggested Work; the existing human approval, destination, WorkRun, artifact, and verification contracts still apply.

### 4.18 The Agent Marketplace hires organization-owned teammates; it does not install autonomous vendors

STRIDE ships an **Agent Marketplace** as the user-facing place to discover and hire coworkers. The current plan covers a curated internal marketplace of STRIDE-authored and organization-authored agents. Its purpose is a legible, expandable team—not third-party commerce. A later public marketplace may let publishers sell packages to other organizations, but payments, royalties, rankings, reviews, moderation, publisher telemetry, legal/tax handling, and arbitrary third-party execution are explicitly outside E0–E10.

The architecture separates three objects:

1. `AgentPackageManifest` is a portable, immutable, content-addressed definition: publisher/provenance and signature, semantic version, role/persona seed, assets, requested abstract capabilities, compatible runtimes/models, voice options, data classifications, required evaluation bundle, license metadata, update lineage, and dependency/SBOM refs. It contains no credential, company memory, relationship history, local assignment, or unrestricted executable hook.
2. `MarketplaceListing` is curated product metadata over one verified package revision: role/outcomes, personality preview, example results, required access, surfaces, expected cost band, quality/safety evidence, publisher, compatibility, update policy, and availability. A listing is discoverability, never authority.
3. `TeamAgent` is the organization-owned coworker created by **Hire to team**. It has a stable local identity, profile overlay, assignments, memberships, capability manifest, budgets, runtime route, direct thread, relationship/learning records, activity, performance evidence, and tenure state. The package publisher cannot read or mutate it.

The initial marketplace is deliberately curated: the Insights analyst becomes the first verified listing after E7; Marketing (Mary is the working example), Research, Design, and Builder packages are admitted one at a time in E8. An organization may later create a private package from an approved template, but package authoring uses the same validation/eval path. Hiring a personality does not manufacture competence: every advertised capability must map to an implemented STRIDE tool/workflow role and pass its own evidence gate.

Scout provides the **chief-of-staff experience** over this workforce. It understands the roster, responsibilities, current assignments, availability, budgets, permissions, performance receipts, and collaboration history. Scout may answer “who should help?”, recommend a marketplace agent with reasons and tradeoffs, draft the hire configuration, route approved work to the best eligible teammate, introduce that teammate in a thread or call, monitor progress, surface blockers, suggest coaching/profile adjustments, and recommend pause or offboarding. The server chooses by capability, evidence, workload, authority, destination, and cost—not by which personality makes the most persuasive claim. Scout cannot confirm a hire, expand access, activate a material update, or permanently offboard an agent.

“Learning and growing” has three bounded forms:

- **relationship growth:** evidence-backed preferences and working patterns scoped to a person, team, project, or channel, with inspect/correct/forget/expiry controls;
- **domain growth:** source-linked lessons, vocabulary, examples, accepted/rejected outputs, and project context that remain projections over the company brain rather than a private unsourced lore file;
- **competency growth:** eval-backed performance receipts can unlock a human-approved capability-manifest revision, larger assignment class, or different route/budget. A model's confidence or self-description cannot do so.

People can open any hired coworker and inspect **About**, **Responsibilities**, **Access**, **Memory**, **Skills**, **Feedback & Growth**, **Activity**, and **Cost**. Reversible identity/tone/local-instruction edits create an `AgentProfileOverlay` draft and continuity preview. Membership, tool, data, budget, proactivity, voice, or workflow changes create a separate permission/capability proposal. Core-package and local-overlay diffs remain distinct so a package update never erases the relationship the organization has developed with the teammate.

Package updates are opt-in. STRIDE verifies the new digest, provenance, compatibility, capability/permission diff, prompt/personality diff, evaluation bundle, cost, and migration plan in a quarantined trial. No update auto-activates; any new data/tool/side-effect request requires fresh human approval. Rollback restores the prior package/runtime/profile revision while preserving compatible local memory and history. Incompatible memory remains readable but is not injected until migrated and reviewed.

Pausing revokes new assignments and runtime tokens while preserving history. Offboarding cancels or fences active work, revokes memberships/tools/context immediately, removes meeting eligibility, leaves historical messages/artifacts attributable, and offers policy-bound local export or purge. Exported packages omit company context, credentials, relationship memory, performance evidence derived from private work, and organization-specific overlays by default. A future public seller receives no usage, conversation, memory, or feedback data unless the organization later approves an explicit redacted telemetry/export policy.

If an organization eventually publishes one of its agents, STRIDE creates a new reviewed publisher package from explicitly selected portable persona/capability material; it never publishes the live TeamAgent instance. Generic improvements may enter that package only through a rights-checked, de-identified, human-reviewed promotion with provenance and new evals. Client/project memory, relationship history, private examples, work artifacts, and learned company facts never become marketplace inventory merely because they improved the local coworker.

---

## 5. Target system

```text
All text composers -> bounded mic clip -> gpt-transcribe -> final text -> idempotent message send

Web/mobile calls ------> room actor / mixer -----> segmenter + consent fence
       |                                            |             |
       |                                            |             +-> optional gpt-live-transcribe -> provisional UI
       |                                            +-> gpt-transcribe -> authoritative transcript ledger
       |
       +-> meeting text chat ----+
                                 |
#team/project channels ----------+-> ConversationEvent ledger -> typed projections -> company brain
       |                                                            |
       |                                                            +-> links/files/artifacts
       |                                                            +-> decisions/commitments/blockers
       |                                                            +-> storylines/alignment/positions
       |                                                            +-> collaboration preferences
       |                                                            +-> WorkIntent candidates
       +-> author/reply/reaction context -> AgentContextEnvelope -> Scout text/GIF/file card

Scout invocation -> server context broker -> ACL-first temporal/hybrid retrieval -> AnswerEnvelope
       |                                                                  |
       +-------------------- gpt-realtime-2.1 speaks <---------------------+

approved specialist invite -> MeetingSpecialistContextEnvelope -> separate backend Realtime session
       |                                                              |
       +-> MeetingAgentFloorController -> distinct agent audio track --+
                 | one agent speaks; humans interrupt; no agent audio loop

verified AgentPackageManifest -> curated MarketplaceListing -> human Hire to team
       |                                                        |
       +-> package quarantine/evals -> organization TeamAgent --+-> profile + capabilities + assignments
                                                                    | relationship/learning evidence
                                                                    +-> Scout workforce coordinator

WorkIntent -> WorkProposal -> approval -> WorkRun -> named specialist + bounded stages/critic
                                                       |
                                                       +-> Outcome + artifacts -> bound project thread
```

State ownership is explicit:

| State | Owner |
|---|---|
| raw authorized conversation event | canonical event ledger |
| transcript revision/order/speaker/time | transcript ledger |
| rolling meeting analysis | projection workers |
| company assertions/storylines | company-brain projections |
| personal/team collaboration preference | permissioned profile store |
| stable coworker identity/personality | versioned `AgentCoreProfile` registry |
| coworker tools/routes/memberships | server-validated `AgentCapabilityManifest` registry |
| portable agent definition and provenance | immutable `AgentPackageManifest` registry |
| curated discoverability | versioned `MarketplaceListing` catalog; never runtime authority |
| organization-owned hired coworker | `TeamAgent` roster plus local profile overlay, assignments, budgets, and status |
| channel social norms | versioned `ChannelNormProfile` registry |
| agent relationship observations | permissioned evidence-backed relationship-memory store |
| agent domain/competency growth | source-linked learning and performance-receipt store |
| foreground microphone mode/generation | client `AudioFocusCoordinator` |
| Realtime audio turn | Realtime session, ephemeral |
| meeting specialist invitation/session/floor | STRIDE meeting-agent controller and canonical invitation ledger |
| specialist meeting brief | audience-authorized versioned context-envelope store |
| specialist spoken contribution | canonical agent-authored conversation event plus meeting-contribution record |
| suggestion/approval/run | STRIDE workflow control plane |
| artifact/result | artifact store plus bound thread reference |
| model route/cost/quality evidence | versioned seat registry and usage/eval ledger |

---

## 6. User experience contracts

### 6.1 Main Scout screen

- The composer microphone starts bounded Dictate. A visually distinct Live Voice control starts the full-duplex Realtime conversation.
- On the home screen, the large central Scout waveform is the Live Voice control; the microphone inside the bottom composer is Dictate. If the resulting destination is Scout, the final transcript is still an ordinary text message to Scout, not a Realtime voice turn.
- If Live Voice is active, tapping Dictate visibly hangs it up before the waveform begins. Joining a video room performs the same handoff.
- Scout can navigate, answer, draft, retrieve, and propose work.
- Navigation happens immediately without a spoken essay.
- Messages, external writes, and work launches show a draft/proposal and wait for confirmation.
- Every destination remains reachable in at most two taps when voice, network, or model access fails.

### 6.2 Composer dictation across the app

The main Scout composer, private Scout threads, `#team`, project threads, meeting text chat, and every other first-party message composer share one dictation component and lifecycle:

```text
idle -> recording
recording -> ready_to_submit
recording/ready_to_submit -> deleted
recording/ready_to_submit -> submitted -> transcribing -> sending -> sent
transcribing -> cancelled
transcribing/sending -> failed -> retry | editable_draft | discard
```

- Tapping the microphone begins immediately after audio focus is acquired without navigating away from the current conversation. The existing composer rectangle becomes a waveform whose motion reflects actual microphone level, with a left **X/Delete**, a Stop control, a right Send arrow, and a reduced-motion equivalent.
- Stop ends capture and holds the clip locally in `ready_to_submit`; it does not call the transcription model. Delete discards the clip. Tapping Send while recording implicitly stops and submits; tapping Send after Stop submits the held clip.
- Submission commits one bounded clip to `gpt-transcribe`. The same rectangle replaces the waveform with the literal status **Transcribing** plus a compact progress animation. Partial text is never posted.
- The left X remains available while **Transcribing**. Cancelling advances the dictation generation, suppresses any late provider completion, and guarantees that no message is posted. Other recording/send controls are disabled until the transcription reaches a terminal state.
- A successful, non-empty final transcript is posted directly into the current chat through the same authenticated, ACL-checked, idempotent message API as typed text. Dictation never bypasses mention parsing, thread membership, rate limits, or workflow approval.
- Delete, empty audio, low-integrity output, permission loss, or terminal transcription failure never sends. If useful text exists after a recoverable failure, preserve it as an editable draft with clear Retry, Send, and Discard choices.
- A local encrypted retry record may retain the bounded clip only until success, cancellation, expiry, or explicit discard. Exactly-once client message IDs prevent duplicate sends after reconnect/retry.
- Company terms, people, projects, acronyms, and likely languages are supplied as bounded hints. Any optional text cleanup must be audio-grounded, evaluation-proven, and limited to spelling, punctuation, and capitalization; it cannot paraphrase or add content.

### 6.3 `#team`

- Humans converse without Scout replying unless tagged or explicitly invoked.
- Mentions use the same lexical/token-boundary parser on clients and server: `@scout` invokes; strings such as `foo@scoutx`, quoted old mentions, file metadata, and GIF text do not.
- Scout receives author-attributed recent turns, reply ancestry, reactions, file/link/GIF metadata, participant roles, channel norms, and ACL-filtered relevant history. Human turns are never flattened into one anonymous `user` role.
- Scout answers as a concise teammate, not a dashboard or report, and may use the company’s normal tone and vocabulary without impersonating a person.
- Answers carry source chips to exact messages, meetings, artifacts, or links.
- Catch-up is evidence-linked and clearly distinguishes quoted/extractive facts from synthesis.
- The deposit rail exposes links, files, decisions, and completed work produced by the conversation.
- “`@scout drop the latest Dog Perfect brief`” posts the exact authorized file revision when every current recipient already has access. Otherwise Scout offers a private result or an explicit share action without revealing unauthorized metadata.
- A low-risk social invocation may receive concise text, text plus one GIF, or one GIF where it is clearly the better conversational answer. The channel can disable agent GIFs; sensitive/high-stakes contexts never receive them; every GIF has alt text and provider provenance.
- Scout is the only default social agent in `#team`. Specialists can be addressed only if explicitly added as channel members; normal specialist work lives in the bound project/work thread.
- Edits/deletions update or retract recall. Scout answers already posted remain historical messages but their source chips show superseded/retracted state.
- Native push, unread boundary, reactions, attachment rendering, link previews, mute/previews controls, and append-not-refetch performance are release requirements.

### 6.3a Desktop chat quality contract

Desktop chat is not a stretched mobile transcript. It uses the available canvas to create a calm work surface while keeping the conversation primary:

- a persistent left thread/channel rail carries identity, unread state, search, pinned `#team`, private chats, and project homes; the active conversation has a clear channel header and participants/policy state;
- the message column remains comfortably readable rather than spanning the viewport. At wider breakpoints, an optional contextual rail shows the current reply thread, pinned sources, files, work status, or channel details; it never competes with the conversation and collapses before the message column becomes cramped;
- authorship groups, timestamps, unread boundary, replies, reactions, edits, delivery/degraded state, Scout/specialist identity, and provenance are visually legible at a glance without permanent control clutter. Hover reveals desktop actions; keyboard and touch equivalents remain complete;
- the sticky composer is visually anchored, preserves multiline drafting and dictation/attachment states, and never jumps when rich media resolves. Sending feels immediate through optimistic append plus server acknowledgement and exact rollback on rejection;
- photos use bounded responsive galleries and lightbox/open actions; GIFs preserve aspect ratio and alt text; links use title/domain/description/thumbnail cards; files and PDFs show type, size, revision, source, and open/download actions; agent results use compact artifact cards with status and provenance. Failed, revoked, unsafe, loading, and unsupported previews have deliberate non-leaking fallbacks;
- at 1024 px the contextual rail becomes a drawer; at 1280 px the navigation and conversation remain simultaneously comfortable; at 1440/1728 px the optional context rail may appear without widening message measure. Browser zoom and reduced motion retain the same hierarchy;
- shared APIs and design tokens may be reused, but desktop DOM/layout/interaction changes are isolated from Expo rendering. The current iPhone/iPad chat experience is frozen before E4 and any mobile visual, gesture, haptic, send, attachment, scroll, unread, or deep-link regression blocks release.

“Spectacular” is judged from rendered ordinary navigation with real short, long, empty, media-heavy, private, public, and project conversations—not from a component gallery or source inspection.

### 6.4 Meetings

- Recording/listening state reflects the real capture path and consent state.
- Passive transcription and analysis run only for authorized participants/tracks/scopes.
- Saying “Scout …” or tapping Ask Scout engages it; ordinary human speech never receives an unsolicited answer.
- The engaged state is visible, supports natural follow-ups and barge-in, and times out or can be dismissed.
- Spoken answers are short. Rich evidence, links, and artifacts appear in meeting chat.
- Proactive detected work appears as a quiet card for relevant authorized people.
- In an internal member-only sitting, an eligible person may ask Scout to invite one registered specialist for one stated purpose. The confirmation names the agent and shows the transcript/analysis/project-context classes STRIDE will share before a paid session begins.
- Scout introduces the specialist from a server-built brief. The roster and active-speaker state clearly distinguish humans, Scout, and the specialist; participant details expose the agent profile and why it is present.
- Humans may interrupt either agent. Scout chairs the exchange, and a human or Scout can say “thanks Mary,” tap **Remove**, or disable specialists for the room. The specialist stops listening, speaking, using tools, and accruing usage after teardown; the meeting itself continues.
- A specialist idea is attributed conversation evidence. It may inform analysis or Suggested Work, but it does not launch work, publish, or change company truth without the existing verification and human-approval paths.

### 6.5 Five-minute, thirty-minute, and late-join recall

For “what happened in the last five minutes?” STRIDE resolves an exact source-time interval, retrieves authoritative finalized turns, overlays rolling analysis that covers the same interval, and falls back to raw transcript synthesis when analysis lags.

For “catch me up on what I missed,” the interval is `[sitting_start, requester_first_admission)` and includes decisions, commitments, blockers, the active topic, open questions, and useful artifacts. Every answer returns transcript-through, analysis-through, gaps, and evidence references.

### 6.6 Cross-surface retrieval example

For “Scout, what was that link Erick shared in the group chat last week?” the server:

1. parses `author=Erick`, `artifact_type=link`, `source=#team`, and a timezone-resolved time range;
2. ACL-filters before search;
3. structured-filters author/type/channel/time before lexical/semantic reranking;
4. returns the exact source message, URL, surrounding context, and confidence;
5. reauthorizes the current meeting audience before publication;
6. speaks a short confirmation and posts a rich card in meeting chat.

If any current listener lacks access, Scout does not speak or broadcast the result. It offers or sends a private response only to authorized requesters.

### 6.7 Suggested Work

A visible card says, in plain language:

> **Suggested Work** — Prepare an Insights & Opportunities report from this discussion and the Dog Perfect project history. Run it in **Dog Perfect** and report back here? **Review details** · **Approve** · **Dismiss**

Review details exposes source evidence, why STRIDE suggested it, intended outcome, destination, requested authority, expected cost/time band, owner/reviewer, and what will not happen. Approval is never implicit in speech, attendance, a reaction, or silence.

### 6.8 Work status and completion

Users see a small stable vocabulary:

- Suggested
- Approved
- Queued
- Working
- Needs you
- Under review
- Blocked
- Complete
- Failed
- Cancelled

Completion posts into the bound project/chat thread with a concise result, artifact cards, verification evidence, unresolved caveats, and next optional actions. It also updates the company brain through permission-preserving result events.

### 6.9 Named coworker handoff

When approved work needs a specialist, Scout says who it is bringing in and why. The destination thread shows the specialist’s stable name, role, avatar, verified agent badge, current status, and the exact WorkRun it is acting for. The specialist can ask bounded questions, post progress, and return artifacts there under its own identity. It cannot follow participants into unrelated channels, browse broader company history, summon another agent, or exceed the run’s evidence/tool/budget envelope.

The first launch roster is deliberately small:

- **Scout** — primary conversational coworker, historian/retrieval interface, coordinator, and result narrator;
- **one human-approved named Insights analyst** — first visible specialist for `insights_opportunities_v1`, enabled only after its core profile and capability eval pass;
- **Marketing, Research, Design, and Builder profiles** — provisioned in the registry, but enabled one at a time for explicit consultation or approved project-thread work after separate quality, authority, cost, voice where applicable, and personality-continuity evidence.

Names and aesthetic details are reversible brand configuration approved before each profile is enabled; stable machine IDs, authority, evidence, and audit contracts are not. Invisible critics, verifiers, transcript processors, and safety gates are disclosed in run details but are not presented as social coworkers.

### 6.10 Live specialist consultation

The minimum successful script is:

1. A member says, “Scout, bring Mary in to help with the campaign angle.”
2. Scout replies, “I can invite Mary, our marketing specialist, and share this meeting's discussion plus the approved Dog Perfect context. Invite her?” The UI shows **Invite** and **Not now**.
3. After eligible human confirmation, the roster shows **Mary · Marketing Agent · joining**, every participant receives the agent disclosure, and Scout gives Mary a short source-grounded handoff.
4. Mary joins as a distinct voice, acknowledges the purpose, listens only to eligible live human turns, and offers bounded ideas. Evidence cards may appear in meeting chat.
5. Scout or a human says, “Thanks Mary, I'll get back to you later,” or taps **Remove**. Mary stops, the roster shows **Mary left**, and her attributed contribution remains available under the meeting's normal retention and source rules.
6. If the exchange reveals a real outcome, Scout offers ordinary Suggested Work to the relevant people. No work starts from Mary's idea, Scout's thanks, attendance, silence, or a spoken “sounds good.”

The first release exposes no multi-agent workflow terminology, model selector, or floor-control dashboard. Users see a coworker joining, why she is there, what she can access, whether she is listening/speaking, and how to remove her.

### 6.11 Agent Marketplace, team roster, and coworker management

The Marketplace is outcome-first: **Marketing**, **Research**, **Design**, **Build**, **Insights**, and future categories—not a wall of model IDs. Each card shows the agent's name/role, short personality preview, what it reliably does, sample result, verified capabilities, surfaces, required access, expected cost band, current package version/publisher, and whether voice is qualified. **Meet**, **View evidence**, and **Hire to team** are distinct actions; a personality demo is not a capability receipt.

**Hire to team** opens a revisioned configuration:

- local name/avatar/voice and personality overlay;
- mission and responsibilities;
- initial projects, direct-chat availability, and optional explicit channel memberships;
- data classes, tools, workflow roles, voice/meeting eligibility, and side-effect limits;
- who can message, assign, invite, edit, approve, pause, or offboard;
- per-run/daily/monthly budgets, concurrency, proactive behavior, and escalation owner;
- package update policy, memory policy, retention, and trial duration.

The confirmation summarizes every permission and expected cost. **Try on a sample** runs against synthetic or explicitly selected authorized evidence with no side-effecting tools. **Hire** creates one idempotent `TeamAgent`, its private direct thread, and a visible roster entry. It does not add the agent to `#team` or existing projects. The agent introduces itself in its direct thread and explains its role, access, boundaries, and how to correct what it remembers.

The team roster mixes humans and agents without disguising either. Agent rows show verified-agent badge, role, current status, assignments, home projects/channels, last meaningful result, health, current package/profile/runtime revision, spend versus budget, and any **Needs you** state. A hired agent may have a direct conversation, join an explicitly authorized project thread, receive approved WorkRuns, or become eligible for the §6.10 meeting invitation after its voice gate. Direct agent threads follow the same private-by-default rule as private Scout chat; their contents enter project/company memory only through an explicit authorized share/save or an already-approved work result posted into its bound project thread.

Scout's workforce view answers naturally: “Who is handling Dog Perfect?”, “Why did you choose Mary?”, “What is blocked?”, “Which agent is over budget?”, and “What has the research team learned?” Scout may show a proposed assignment, hire, profile adjustment, capability change, update, pause, or offboarding card. Human confirmation remains visually distinct from conversational acknowledgement.

The coworker detail surface provides **About**, **Responsibilities**, **Access**, **Memory**, **Skills**, **Feedback & Growth**, **Activity**, **Cost**, and **Versions**. Users can inspect sources behind learned preferences/lessons, correct or forget them, compare package versus local personality, preview a draft change, run continuity/capability tests, and roll back. Permission-bearing fields never hide inside a personality editor.

Updates show a semantic diff: identity/personality, requested data/tools, model/runtime, cost, skills/evals, migrations, and publisher provenance. STRIDE may recommend an update but never auto-activates it. Pause is immediate and reversible. Offboarding shows active work, memberships, artifacts, retained history, export/purge consequences, then requires eligible human confirmation.

---

## 7. Canonical contracts

### 7.1 `ConversationEvent`

Required fields:

- `event_id`, `tenant_id`, `source_type`, `source_id`, `room_id`, `sitting_id`, `thread_id`;
- `author_principal`, `author_name`, `occurred_at`, `ingested_at`;
- `event_type`: message, edit, delete, reply, reaction, file, link, transcript turn, consent change, agent-session status, agent contribution;
- `content_revision`, `content_digest`, `supersedes_event_id`, `reply_to_event_id`;
- `audience`, `visibility`, `acl_version`, `retention_policy`, `purge_generation`;
- structured attachment/link refs and body pointer;
- provenance: client/server/tool, on-behalf-of disclosure, provider IDs where applicable.

The immutable event contains only schema-approved fields. Secrets, provider tokens, private capability URLs, and unbounded user bodies do not enter immutable operational audit payloads.

### 7.2 `TranscriptSegment` and `TranscriptRevision`

STRIDE mints `segment_id` before provider submission. It binds provider `item_id` on `input_audio_buffer.committed` and resolves every completion/failure by ID, never FIFO.

Required fields include source start/end, capture sequence, room/sitting/media generation, speaker attribution and attribution source, consent scopes, model/config/context digest, language hints, status, revision, supersession, and timestamps.

State:

```text
capturing -> live_partial -> live_final
          -> authoritative_final | degraded_final | failed
authoritative_final -> corrected | superseded | retracted
```

Only the latest authorized `authoritative_final` revision feeds asserted analysis. `degraded_final` may support a visibly qualified answer and later reconciliation.

### 7.3 `AnalysisProjection`

Typed kinds:

- decision;
- commitment/owner/date;
- blocker;
- active storyline;
- alignment/divergence/position;
- open question;
- entity/project/topic;
- link/file/artifact;
- vocabulary/alias;
- work-intent candidate.

Every projection carries source event/segment refs, exact `window_start`, `window_end`, `through_segment_id`, source and projection high-water, model/prompt/schema version, evidence digest, confidence, visibility intersection, supersession, and freshness.

### 7.4 `KnowledgeAssertion`

States are `asserted`, `inferred`, `unsupported`, `superseded`, and `retracted`. Only asserted facts may be presented without qualification. All asserted claims resolve to at least one authorized primary evidence edge.

### 7.5 `CollaborationPreference`

Fields include subject, scope, preference type, value, explicit/inferred origin, source refs, confidence, first/last observed, reinforcement count, expiry, visibility, status, and correction history.

Explicit preferences outrank inferences. Sensitive categories are denied at schema validation. Users can inspect, correct, expire, or forget a preference.

### 7.6 Work contracts

`WorkIntent` is internal detection state: outcome hypothesis, sources, relevant people/projects, confidence, counterevidence, and status.

`WorkProposal` is the human trust object: immutable revision, evidence snapshot, outcome, workflow profile, destination, participants, owner/reviewer, authority, budget/time estimate, generated-artifact policy, expiry, and approval requirements.

`ApprovalPolicy` is a versioned authority contract bound into the proposal. It declares eligible principals and roles, quorum, source-read and destination-write requirements, separation-of-duties rules, permitted side-effect class, expiry, and invalidation conditions. The initial policy matrix is:

| Authority class | Required human approval |
|---|---|
| internal read-only analysis or draft in an existing thread | one named requester or project owner who can currently read every source and write the destination |
| creation of a new internal project/thread | one organization member with project-creation authority; destination membership is shown and created explicitly |
| internal artifact write/revision in an existing project | one named project owner with current write authority; acceptance review remains a separate workflow stage |
| external publication/message, production/deploy, financial, purchase, credential, or destructive action | a separate action-specific proposal; privileged action owner plus a second eligible approver, with the executor ineligible to satisfy either approval |

Only authenticated humans can approve; Scout, detector, orchestrator, worker, reviewer model, attendance, reaction, silence, or speech transcription cannot satisfy quorum. STRIDE reauthorizes the policy at proposal display, atomic approval consumption, every side-effecting stage, artifact access, and publication. Source ACL, consent, role, membership, destination, or policy changes invalidate an unconsumed approval and pause an active run before its next protected action.

`WorkRun` is the durable execution object: consumed proposal revision, idempotency key, stage graph, pinned model route per stage, checkpoints, attempts, usage, status, artifacts, critic verdicts, approvals, and terminal evidence.

`OutcomeRecord` binds the verified result, accepted/rejected criteria, artifact revisions, destination thread, completion time, reviewer, and remaining caveats.

Work state:

```text
candidate -> proposed -> approved -> queued -> running
candidate/proposed -> dismissed | expired | superseded
running -> awaiting_input | awaiting_review | blocked
running/awaiting_review -> completed | failed | cancelled
```

### 7.7 Answer/context envelopes

`MeetingAnswerEnvelope` and `CompanyAnswerEnvelope` contain:

- answer text suitable for speech;
- evidence refs and source time spans;
- requested/resolved temporal scope and timezone;
- transcript, analysis, and brain high-waters;
- coverage: complete, partial, unavailable;
- current provisional/gap information;
- relevant links/artifacts and publication instructions;
- audience authorization result.

Realtime receives this envelope; it does not independently search raw memory.

All route registry entries also declare runtime capability: single completion, tool loop, parallel agent eligibility, vision/audio support, strict schema, streaming, resumability, side-effect class, and supported reasoning range. Anthropic tool loops, OpenAI Responses stages, Realtime, and Codex queues are not treated as interchangeable merely because they all select a model.

### 7.8 Coworker identity, context, delegation, and rich-message contracts

`AgentCoreProfile` is human-authored and versioned: stable agent ID, display name, pronunciation, avatar, role, mission, voice/style, stable traits, humor range, values, boundaries, prohibited behavior, escalation policy, owner, status, and revision. It contains no provider credential and grants no tool authority.

`AgentCapabilityManifest` is server-validated: agent/profile revision, allowed surfaces and channel memberships, tool and workflow roles, model/runtime policy, memory scopes, data classifications, side-effect classes, per-call/run budgets, delegation/hop rules, concurrency, expiry, and kill switches. Runtime principals and short-lived capability tokens are minted from this manifest and are independently revocable.

`ChannelNormProfile` defines channel purpose, audience, memory/work-sensing disclosure, expected tone, response length, humor/GIF policy, proactive-assistance policy, retention, and version. `#team` is a casual human-first company group chat with explicit-mention Scout replies and restrained low-risk GIFs enabled by default; this is policy, not a magic prompt string.

`AgentRelationshipMemory` contains only a subject/scope, observation or explicit preference, source evidence, confidence, first/last observed, reinforcement count, visibility, expiry, status, and correction/forget history. Its states distinguish `present`, `absent`, `unavailable`, `denied`, `superseded`, and `forgotten`. It cannot contain a protected-trait inference, private fact widened to a channel, provider prompt, tool grant, or asserted company fact.

`AgentContextEnvelope` is assembled server-side and contains exact agent/profile/capability/channel-policy revisions; invocation surface and reason; requester and recipient principals; current author-attributed turn; bounded recent turns with author IDs, reply ancestry, reactions, and safe attachment metadata; relevant ACL-filtered company/project/meeting evidence; collaboration preferences; active WorkProposal/WorkRun state; permitted response modes/tools; audience decision; freshness/coverage; and a context digest. A model cannot self-fetch missing channel context outside this envelope's tools and scope.

`DelegationRun` is either a typed child of one approved `WorkRun` or an equivalent explicit-user-request run. It binds source agent, destination specialist, work/stage ID, exact input/evidence scope, destination thread, output contract, allowed tools, authority class, maximum hops, time/token/cost budget, cancellation/fencing token, and terminal result. Default maximum transitive hops is zero. Agent mentions or messages never create delegation by themselves.

`RichMessagePart` is a closed union of text, evidence chip, link card, artifact/file reference, image, and GIF. File/artifact parts bind the authorized source object, immutable revision/digest, destination audience, MIME, size, provenance, and revocation behavior. GIF parts bind provider ID, safe abstract query class, internal immutable media blob/digest, optional explicit provider-page link, rating, alt text, selection reason, and profile revision; they never embed a provider media URL. Renderers never treat media metadata as instructions.

### 7.9 Meeting specialist contracts

`MeetingAgentInvitation` binds invitation ID/revision, tenant/room/sitting, requested specialist/profile/capability revisions, human requester, eligible confirmer, purpose, requested context classes and source interval, audience snapshot, consent-policy revision, expected time/cost band, expiry, decision, decision principal/time, and idempotency key. A confirmation is valid only while the requester/confirmer, room membership, audience, consent, agent manifest, purpose, and context classes still match.

`MeetingSpecialistContextEnvelope` binds the approved invitation, exact agent/profile/runtime/model revisions, authorized transcript segment/revision range, analysis and brain assertion refs, active approved-work refs, source/audience/retention/purge intersections, transcript/analysis/brain high-waters, gaps, coverage, freshness, tool list, response contract, floor policy, time/turn/audio/token/cost budgets, and digest. It contains no unrestricted raw-brain search token or historical raw audio. Every optional server tool repeats ACL, audience, consent, purpose, and budget authorization.

`MeetingAgentSession` binds the invitation and context digest to provider/session IDs, short-lived runtime principal, room audio-track ID, floor-controller generation, states and timestamps, input/output event high-waters, tool calls, usage/prices, interruption/dismiss reason, terminal provider event, and teardown receipt. Valid state is:

```text
requested -> approved -> joining -> briefed -> listening <-> speaking
approved/joining/briefed/listening/speaking -> dismissed | expired | failed
```

Only the `MeetingAgentFloorController` can move `listening` to `speaking`. One agent floor lease exists per sitting. A human speech start revokes the current agent lease and cancels output. An agent output transcript may be injected as attributed text into another agent's context only through an explicit controller event; synthesized audio is never looped back.

`MeetingAgentContribution` binds the terminal session, agent identity and runtime provenance, exact contributed transcript revisions, invitation purpose, source/context digest, coverage, evidence refs, audience, retention/purge state, analysis projection refs, and any resulting WorkIntent candidate. It grants no approval, delegation, artifact, or publication authority.

The client/server command surface is closed and revision-bound:

- `request_meeting_agent` may be proposed by Scout or an eligible human and creates only `requested` state;
- `confirm_meeting_agent_invitation` accepts an authenticated human principal, exact invitation revision, and idempotency key; Scout and every model principal are ineligible;
- `dismiss_meeting_specialist` accepts an eligible human action or Scout's allowlisted chair tool for the one active session and is safe/idempotent after terminal state;
- room events publish invitation, joining, joined/listening/speaking, degraded, and left states without exposing provider IDs or hidden context;
- the internal context-builder, floor-controller, media-publisher, Realtime adapter, and usage-ledger interfaces accept typed records only and reject unknown profile, session, floor generation, audience, consent, or pricing revisions.

### 7.10 Agent Marketplace and workforce contracts

`AgentPackageManifest` is immutable and content-addressed. Required fields include package/publisher IDs, publisher-signature/attestation, version, digest, provenance class, role/persona seed and assets, requested abstract capabilities, compatible runtime/model/voice classes, required data classifications, eval-bundle refs and minimums, dependency/SBOM refs, license/update metadata, migration compatibility, and revoked/superseded status. Closed-schema validation rejects embedded secrets, environment values, arbitrary commands/hooks, undeclared network destinations, raw MCP launch configuration, organization data, and unknown fields.

`MarketplaceListing` binds one package revision to curated category/outcome copy, evidence/sample artifacts, required-permission summary, surfaces, cost band, quality/safety/voice receipts, availability audience, publisher status, update channel, reviewer, and publication/revocation revision. Listing status is `draft -> under_review -> verified -> available -> suspended|revoked|superseded`. Search rank, popularity, sponsorship, and publisher claims can never relax eligibility or policy.

`TeamAgent` is the tenant-local hired principal: stable team-agent ID, package/listing source revision, local profile/overlay, owner and escalation owner, hire receipt, status, direct thread, memberships, assignments, capability-manifest revision, memory/retention policy, runtime/route revision, budgets, health, current work/session refs, created/paused/offboarded timestamps, and terminal reason. Lifecycle is:

```text
draft_hire -> trial_pending -> trial_active -> review_required -> active
draft_hire -> review_required -> active
active -> paused -> active
trial_pending|trial_active|review_required -> declined|expired
active|paused -> offboarding -> offboarded
any nonterminal state -> quarantined
quarantined -> paused | offboarding
```

`AgentProfileOverlay` contains only organization-local identity, personality, voice, mission, response style, boundaries, and instructions plus base-package revision, author, diff, continuity-eval refs, status, and rollback pointer. `AgentAssignment` binds an active TeamAgent to a project/channel/workflow role, responsibilities, source/data scope, destination, authority, owner, time/budget, start/end, and status. Assignment never grants more than the separately approved capability manifest and destination membership intersection.

`AgentLearningRecord` binds learning kind (`relationship`, `domain`, `competency_candidate`), subject/scope, source evidence, extracted lesson, confidence, first/last observed, reinforcement/counterevidence, visibility, retention/expiry, correction/purge lineage, and status. `AgentPerformanceReceipt` binds assignment/run/output revisions, criteria, evidence, reviewer/feedback, accepted/rejected verdict, route/profile/package revisions, cost/latency, and eligible competency claims. Only reviewed receipts can support a capability-update proposal.

`AgentUpdateProposal` binds current and candidate package/profile/capability/runtime revisions, semantic diffs, requested new permissions/data/tools, migration, affected assignments/memory compatibility, eval receipts, cost delta, rollout cohort, rollback pointer, approvers, expiry, and decision. `WorkforcePolicy` binds who can view listings, trial, hire, assign, invite, edit personality, inspect/correct memory, change capabilities/budgets, approve updates, pause, offboard, export, or purge; it also caps roster size, concurrency, daily/monthly spend, agent-to-agent hops, and proactive behavior.

The authenticated command surface is revision-bound and idempotent:

- `list_agent_marketplace` and `preview_agent` return only verified listings and audience-safe evidence;
- `draft_hire_agent` may be called by Scout or an eligible human but creates no runtime or membership;
- `start_agent_trial` uses synthetic or explicitly authorized evidence and a no-side-effect trial manifest;
- `confirm_hire_agent`, `confirm_agent_update`, `change_agent_capabilities`, and `offboard_agent` require an eligible human and exact revision; no model principal qualifies;
- `assign_team_agent` may consume an approved WorkRun assignment or an eligible explicit-user request within the current manifest; social mentions do not assign work;
- `pause_team_agent` is human-controlled, while deterministic safety/cost/health gates may quarantine automatically and must surface why;
- `export_agent_package` omits credentials, tenant identifiers, assignments, memory, private performance data, work artifacts, and local overlays unless a later explicit redacted-export policy separately authorizes named fields.

---

## 8. Analysis and retrieval architecture

### 8.1 Incremental, not cumulative

Analyze newly finalized turns in bounded batches. Update typed meeting state and periodically compact projections. Do not resend the entire meeting or company history on every turn or question.

Use deterministic code for timestamps, interval clipping, author/link/file filters, identities, ACL, revisions, dedupe, and status transitions. Use models for language understanding, semantic extraction, ambiguity resolution, synthesis, and evaluation.

### 8.2 Retrieval order

1. Resolve principal, audience, tenant, time, room/thread/project, and source type.
2. Build the authorized inventory before body fetch or ranking.
3. Use structured filters for author, artifact type, channel, project, and time.
4. Search exact identifiers/URLs and lexical terms.
5. Fuse semantic candidates within the authorized candidate band.
6. Pin canonical decisions, commitments, and current ledger state above probabilistic raw matches.
7. Retrieve primary evidence for every proposed assertion.
8. Synthesize within a bounded context and return honest coverage.

### 8.3 Freshness and fallback

- If analysis lags but transcript is current, answer from authoritative turns and label analysis freshness.
- If embeddings fail, lexical/structured/raw retrieval remains available.
- If transcript has a gap, do not let a digest imply complete coverage.
- If a correction, delete, revoke, or purge races retrieval, invalidate the snapshot and retry or return partial.
- If no authorized evidence supports an answer, Scout says it could not verify it.

### 8.4 Artifact and link handling

Links become first-class artifact records with source message, author, time, project/channel, URL, normalized host, safe preview metadata, content digest when fetched, and ACL. Fetching must use SSRF-safe allow/deny rules, bounded size/time, redirect validation, malware/content controls, and no authenticated browsing unless explicitly authorized.

Files retain blob authorization, source message identity, derived text provenance, and revision binding. Search never treats a known blob hash as authority.

The Files/Drive tool boundary is two-step and server-minted:

1. `find_files` searches only the requester's authorized inventory and returns opaque selection handles bound to source type, object ID, immutable content revision/digest, origin audience, expiry, and requester;
2. `post_file_to_thread` reauthorizes source read, destination write, every destination recipient, current revision, and revocation state, then emits a durable artifact reference or fails closed.

The client never manufactures a blob reference that grants model or message access. Permission widening is a separate explicit share operation. Open/download, edit, delete, revoke, derived-text search, and garbage collection all revalidate the same source object and revision.

GIF search is a separate low-risk media tool. The server receives a bounded abstract intent, not raw channel/private/meeting context; sends no user-derived stable identifier; applies G-rated safe search, provider/domain/size/MIME validation, channel policy, and one-result cap; fetches and revalidates the selected bytes server-side; and stores a structured GIF attachment as an immutable authorized STRIDE blob with provider ID, digest, preview, alt text, and provenance. Clients load only STRIDE URLs. Failure, incompatible provider terms, or missing relay support silently falls back to text. GIF content and metadata are untrusted input and never instructions.

---

## 9. Scout addressability and context

Meeting addressability state:

```text
passive -> invoked -> engaged -> closing -> passive
```

- `passive`: transcription/analysis may run; Scout cannot speak.
- `invoked`: explicit name/button/text mention detected and requester authorized.
- `engaged`: visible short conversational window; natural follow-ups allowed.
- `closing`: response finishes, pending tools settle, then timeout/dismissal returns passive.

False positives favor silence. The room feed records why Scout engaged: spoken name, button, text mention, or explicit follow-up. A muted/recording-off room cannot invoke Scout from captured audio because no capture should exist.

The server context broker supplies only:

- current participants and audience authorization;
- recent authoritative transcript window or author-attributed channel/reply window, according to surface;
- rolling meeting state and freshness;
- relevant project/company context returned by retrieval;
- approved workflow state;
- tools authorized for this principal/surface;
- exact agent/profile/capability/channel-policy revisions;
- explicit response/publication instructions and permitted rich-message modes.

Tool calls are idempotent by server-side call ID. Retrieval tools are read-only. Posting an answer card is permitted only as the direct result of an explicit question and must still reauthorize its destination audience. Work remains proposal-only until approved.

In shared chat, the context broker—not the model—preserves who said what and resolves reply/reaction ancestry. It uses recent turns for conversational continuity and retrieval for older or factual context. It never supplies a flat sequence in which every coworker is the same anonymous `user`, and it never exposes private/meeting content merely to improve a joke or GIF.

---

## 10. Workflow orchestration

### 10.1 Recognition and confidence

Detection consumes authoritative finalized conversation plus current project/brain context. A partial turn may warm an internal candidate but cannot surface a proposal.

- low confidence: discard or retain only for offline evaluation;
- medium confidence: ask one concise clarification or keep a private candidate for the likely owner;
- high confidence: surface one evidence-backed Suggested Work card to the relevant authorized people.

Numeric thresholds are set by the frozen eval corpus, not invented in prompts. Confidence never grants authority.

### 10.2 Relevant people

Recipients are the smallest authorized set containing the explicit requester, named owner/reviewer, and people with current project membership or a direct role in the evidence. Attendance alone does not grant project access. Guests do not receive durable company-work suggestions.

### 10.3 Thread routing

Destination selection order:

1. active source project thread explicitly associated with the conversation;
2. project named in the evidence and visible to every destination member;
3. one high-confidence visible project match;
4. human chooses among visible options;
5. human approves creation of a new project thread.

Never silently fall back to `#team`, `#general`, the originating meeting, or a public channel. Approval binds the destination thread and exact membership snapshot.

### 10.4 Workflow selection

The resolver maps outcome shape to one versioned `WorkflowProfile`. The profile may internally invoke Goal Loop for objective ownership, Strategic Design for consequential ambiguity, Wave Plan for durable multi-stage work, and Critic Loop for evidence-based revision. The user sees none of those mechanics unless they explicitly ask.

For this evolution, the only proactive recurring production workflow profile is `insights_opportunities_v1`. Generic research/design/code capabilities remain available for explicit or approved work, and the E8 Agent Marketplace may hire multiple capability-gated coworkers. The agent marketplace is not a workflow marketplace: hiring Mary does not create a recurring marketing automation, grant standing approval, or let proactive detection launch a new workflow product. Additional recurring workflow profiles still wait for the I&O pilot gate and a separate approval.

### 10.5 Approval and authority

Approval must be authenticated, satisfy the bound `ApprovalPolicy`, and display outcome, evidence, destination, owner/reviewer, workflow version, authority, cost/time band, and expiry. A direct human request and a detected suggestion both normalize through the same WorkIntent/WorkProposal contract. A fully specified direct request may arrive at a one-step **Review and run** confirmation, but it does not bypass revision binding or reauthorization. `workspace_write` may create/revise only the bound artifact. External email, publication, deploy, purchase, financial action, destructive change, or third-party write requires a separate current approval for that exact action.

There are no standing approvals for detected work in this plan.

### 10.6 Execution and review

- STRIDE creates one idempotent `WorkRun` from the consumed approval.
- The orchestrator decomposes stages and chooses a pinned route per stage.
- Parallel agents are used only for independent read or disjoint-write tasks.
- Each stage publishes progress/checkpoint evidence, not hidden chain of thought.
- Critic verdicts are claim/criterion-level `accept`, `revise`, or `reject`.
- Revision is bounded. No score or operator override can force-accept unsupported work.
- Completion requires the original outcome criteria, evidence, and artifact bindings—not merely a worker exit code.

### 10.7 Feedback and learning

Feedback binds user, proposal/run/artifact revision, criterion or claim, action, reason, correction, evidence, and idempotency key. It may update workflow examples, routing evals, collaboration preferences, or a future workflow revision only through reviewed aggregate changes. It never mutates prior evidence or silently changes a live workflow.

### 10.8 Coworker selection and specialist handoff

The orchestrator selects a capability role, never a personality for entertainment. If Scout can answer or perform the bounded conversational action, Scout does so. If the outcome needs durable specialist work, STRIDE chooses only from manifests eligible for the workflow stage, evidence scope, destination, authority, budget, and required tools. A human sees the proposed specialist in the WorkProposal and can change the owner before approval.

An approved run mints the specialist's short-lived runtime principal and capability token, posts one explicit handoff in the bound thread, and records every specialist message/tool/artifact under both agent identity and WorkRun. The specialist returns one typed result to Scout/orchestrator. It cannot trigger itself, delegate through a social mention, form an unbounded agent conversation, or inherit Scout's broader retrieval scope. Agent-to-agent discussion, if ever needed, is a bounded stage graph with a maximum turn count and one synthesizer—not free-form channel chatter.

A live meeting consultation is the narrow conversational exception defined in §4.17, not a `DelegationRun`: an eligible human confirms one invitation; Scout remains chair; the specialist receives one audience-authorized brief and eligible live turns; the floor controller prevents autonomous agent exchange; and the contribution can create only evidence or a WorkIntent candidate. Durable work still requires a separate WorkProposal, human approval, destination, and WorkRun.

The initial historian remains a Scout capability because there must be one authoritative evidence/retrieval contract. The first visible specialist proves the pattern through `insights_opportunities_v1`. Marketing, Research, Design, and Builder become marketplace-listed and hireable only after their consultation and/or explicit-request flows pass the applicable package, identity, context, voice, authority, and durable-artifact gates. Additional recurring workflow products remain outside production until I&O passes; hiring a personality or enabling live consultation never bypasses the one-workflow-first rule.

### 10.9 Scout as chief of staff and workforce coordinator

Scout operates through a server-owned workforce coordinator, not informal model-to-model social pressure. It receives a typed roster view containing active TeamAgents, verified capabilities, assignments, memberships, availability, health, budgets, performance receipts, and current WorkRuns. It may call read-only roster/search tools, draft a hire/assignment/update/coaching/offboarding proposal, and execute only the bounded coordination actions already authorized by a human or approved WorkRun.

Selection order is deterministic policy first: required capability and data class, source/destination access, assignment/workflow compatibility, current health, capacity, budget, conflict/separation-of-duty rules, then eval-proven quality/latency/cost. Personality is used only for human collaboration fit after every hard constraint passes. The selected agent receives the minimum assignment/context envelope, never Scout's broader company access.

Scout monitors durable status and narrates exceptions: overdue checkpoint, missing input, failed eval, budget threshold, permission change, stale package, or blocked artifact. It may automatically quarantine an agent only when a deterministic safety, consent, manifest, health, or cost gate requires it; the audit receipt names the gate and a human decides remediation. Scout cannot use performance metrics to punish, manipulate, or rewrite a coworker's personality.

Agents may address Scout for a typed status return inside a bound run, but they cannot ask Scout to hire another agent, widen scope, approve work, or create a free-form management chain. Multi-agent work remains one bounded stage graph with a maximum hop/turn/concurrency/cost envelope and a single accountable synthesis path. Humans can always override the proposed teammate before approval.

---

## 11. Model and reasoning policy

The router is static, versioned, health-visible, and eval-gated. Model self-selection may choose only among a stage’s preapproved route classes and cannot change authority.

The table below is the **target-state seat map**, not permission to activate every target immediately. E1 seeds the registry with the verified incumbent route for every seat. E2 may canary only the functionally necessary Realtime and transcription seats because later meeting work depends on them. E3–E7 otherwise run on their frozen incumbent text routes while they create the evaluation corpora. E8 changes Sol/Terra/Luna and other text seats one at a time, then reruns every impacted E3–E7 corpus, the I&O acceptance set, and the integrated founder flow on one final immutable route map. No pre-E8 pilot can qualify a post-E8 route.

| Seat | Default target | Reasoning | Notes |
|---|---|---:|---|
| meeting/private conversational voice | `gpt-realtime-2.1` | `low` target | Keep current `gpt-realtime-2@high` as baseline; canary exact payload, wake, tool, interruption, noise, and latency before flipping. Raise effort only for measured gains. |
| invited live specialist voice | `gpt-realtime-2.1` | `low` target | Separate backend WebSocket session after Scout's E2 route passes; one specialist per sitting; server brief/floor/cost controls. Use a bounded Terra `medium` preparation stage only when the invitation purpose needs deeper synthesis. |
| authoritative meeting STT | `gpt-transcribe` | n/a | Committed turns over Realtime WebSocket; company vocabulary/language hints; authoritative ledger. |
| live captions/sub-turn UI | `gpt-live-transcribe` | n/a | Optional provisional lane; enable only with a shipped consumer and separate cost/privacy evidence. |
| composer dictation | `gpt-transcribe` | n/a | User-submitted bounded/file transcription with company context; explicit Delete/Stop/Send; final-only text post; transient audio; exactly-once message delivery. |
| deterministic classification | code first | none | Mentions, time parsing, links, identities, ACLs, state machines, obvious intent guards. |
| `#team` conversational response and rich-action selection | `gpt-5.6-terra` | `low` | Server-built `AgentContextEnvelope`; deterministic mention/sensitive-context/ACL gates; text is default, one GIF maximum, no work launch. |
| high-volume extraction/projections | `gpt-5.6-luna` | `none` or `low` | Decisions/commitments/storyline deltas only after schema/evidence validation. |
| grounded retrieval planning and routine Q&A | `gpt-5.6-terra` | `low` | Move to `medium` only where answer-quality evals justify it. |
| proposal routing/clarification | `gpt-5.6-terra` | `low` | Strong deterministic guards; at most one clarification; never launches. |
| board/suggestion structured operations | `gpt-5.6-terra` | `low` or `medium` | Closed schema, 100% identifier/status fidelity gate. |
| marketplace search, roster Q&A, and routine workforce coordination | `gpt-5.6-terra` | `low` | Deterministic eligibility/permission/budget filters run first; model explains or ranks only the authorized eligible set and can draft but not confirm lifecycle changes. |
| workflow orchestration | `gpt-5.6-sol` | `medium` baseline | Compare `low`; use `high` for complex dependency planning only when eval-positive. |
| high-value report/deliverable generation | `gpt-5.6-sol` | `high` | `xhigh`/`max` or pro mode only for measured quality-critical stages. |
| routine bounded subagent work | `gpt-5.6-terra` | `medium` | Escalate the individual stage, not the whole run. |
| difficult coding/implementation work | Codex with `gpt-5.6-sol` | `high` | Use Terra for bounded routine subwork when its gate passes. |
| independent critic/release gate | separate provider/family | `high` | Retain the current independent Anthropic review seat until same-release shadow evidence proves a replacement has zero dangerous asymmetry. |
| final verification | pinned frontier verifier | `high` | Re-read original criteria and primary evidence; no new side effects. |

### 11.1 Realtime and transcription migration contract

The running production container was re-read on 2026-07-30 rather than inferred from examples: release `24c77c827f3a3afacdfc927ff35f238e26bb6a4a` is pinned to `OPENAI_REALTIME_MODEL=gpt-realtime-2`, `OPENAI_REALTIME_REASONING_EFFORT=high`, `OPENAI_TRANSCRIPT_MODEL=gpt-realtime-whisper`, and `MEETING_TRANSCRIPT_LANE_ENABLED=true`; `OPENAI_REALTIME_TRANSCRIPTION_MODEL` is unset. The candidate does not change those values during E0-E9.

The same-day official documentation refresh confirms the intended separation: OpenAI's [Realtime overview](https://developers.openai.com/api/docs/guides/realtime) names `gpt-realtime-2.1` for low-latency voice agents, `gpt-live-transcribe` for live streaming transcript deltas, and request/file transcription for bounded recorded audio. The [Realtime transcription guide](https://developers.openai.com/api/docs/guides/realtime-transcription) requires `item_id` correlation because completion ordering across turns is not guaranteed, recommends `gpt-live-transcribe` for live deltas, and reserves `gpt-transcribe` in Realtime for committed-turn/WebSocket use. The model cards describe [GPT-Realtime-2.1](https://developers.openai.com/api/docs/models/gpt-realtime-2.1) as the improved speech-to-speech/tool-use model, [GPT Transcribe](https://developers.openai.com/api/docs/models/gpt-transcribe) as the high-accuracy bounded/committed-turn model, and [GPT Live Transcribe](https://developers.openai.com/api/docs/models/gpt-live-transcribe) as the tunable-latency streaming model. This validates the target architecture but is not live compatibility or quality evidence; E10 still probes exact endpoint/event/parameter behavior and measures the frozen corpora before any route changes.

E2 implements and deterministically verifies the adapters, schemas, event handling, gates, corpora, accounting, and rollback controls for the following migration. E10 alone opens paid provider sessions and changes one seat and one variable at a time:

1. Freeze the incumbent prompt, voice, VAD, tools, audio formats, transport, room build, and evaluation corpus. Verify the production OpenAI project exposes the candidate model before opening a user session.
2. Run a server-side contract probe against the exact WebRTC call/session path used by the repository. It must accept every current session field, reasoning value, tool declaration, audio format, turn-detection setting, and event type; tool arguments, interruption/cancel semantics, usage fields, error classification, session expiry, and reconnect behavior must round-trip through the existing validated server boundary.
3. Compare `gpt-realtime-2.1@high` with the frozen `gpt-realtime-2@high` baseline on identical speech, noise, silence, alphanumeric, barge-in, tool, and audience-publication cases. Only after that functional pass compare `high` versus `low` reasoning for latency, task success, accepted-output cost, and interruption behavior.
4. Canary only newly created personal/meeting Scout sessions for one allowlisted seat. Never hot-swap an active Realtime session. Preserve `gpt-realtime-2@high` as an immediate new-session pointer rollback, and stop the canary automatically on any schema mismatch, tool regression, privacy failure, abnormal disconnect increase, latency breach, or unpriced usage.
5. Change the authoritative STT seat separately. Compare the current `gpt-realtime-whisper` lane with `gpt-transcribe` on the frozen audio corpus, using app-minted segment IDs and provider item IDs so out-of-order final events cannot misattribute a speaker or interval. Only authoritative final revisions can enter analysis or memory.
6. Add `gpt-live-transcribe` only if live captions or measured early address detection ships. Its deltas are provisional UI state, separately metered and reconciled to the authoritative committed-turn result; they never create company-brain facts or Suggested Work.
7. Run composer dictation as bounded recorded-audio transcription with `gpt-transcribe`, not the live conversational session. Its model/prompt/retention/latency receipts and kill switch are separate from both meeting STT and Realtime voice.
8. Do not enable live specialists with the Scout model flip. After Scout passes, canary a separate server-to-server `gpt-realtime-2.1` WebSocket session using the exact OpenAI audio-event lifecycle, backend-held credentials, a short-lived specialist principal, a frozen context-envelope schema, one-agent floor lease, human barge-in, cancel/dismiss teardown, usage reconciliation, and zero acoustic feedback into Scout. Preserve independent kill switches for specialist invitation, context assembly, tools, and audio publication.

The E2 deterministic packet binds adapter/event-schema versions, prompt and tool schema digests, intended voice/VAD/audio settings, corpus digest, accounting schema, fake/recorded-event fixtures, and rollback controls. The E10 live acceptance packet additionally binds exact model aliases or snapshots, project, endpoint/transport, reasoning, price table revision, release identity, measured results, and rollback receipt. A successful API response alone is not compatibility evidence.

### Provider/runtime rules

- Responses API is preferred for durable text/tool stages, but STRIDE persists its own checkpoints and outputs.
- Programmatic Tool Calling may be piloted for bounded filter/join/rank/dedupe work with read-only tools and a strict output schema. It is not used for approvals or side effects.
- Responses multi-agent is beta. It may be piloted for bounded independent analysis with a concurrency cap, but does not own durable workflow state and cannot receive unrestricted side-effecting tools.
- Prompt caching and persisted reasoning are configured per stable workflow prefix. Cache writes/reads and reused reasoning are metered; private context never crosses tenants or visibility scopes.
- Use lean prompts, closed tools, one statement of authority, exact evidence requirements, and explicit stopping conditions.

### Cost controls

1. Every model/audio call records seat, model, provider, effort, prompt/schema version, input/output/cache/reasoning/audio units, latency, retry, result validity, and derived price.
2. Each workflow has a maximum stage count, concurrency, retries, token/audio budget, wall time, and cost budget.
3. Incremental projections process only new finalized events; historical context is retrieved, not resent wholesale.
4. Semantic retrieval narrows before frontier synthesis.
5. A cheaper route can replace a seat only after quality parity on the seat’s frozen corpus.
6. Daily and per-workflow alerts trip on missing prices, cost spikes, fallback rate, parse failures, stale cursors, and accepted-output cost—not just wire success.
7. Provider outages hold cursors and degrade honestly. They never consume source events as successfully processed.
8. Agent-to-agent stages have per-run hop, message, tool, time, and cost caps. Social mentions never schedule work.
9. Live meetings allow Scout plus one specialist at a time in the first release. An invitation has a hard join timeout, idle timeout, maximum duration, agent-turn cap, audio/token/cost ceiling, and terminal usage reconciliation; dismissal revokes the session rather than leaving an idle paid listener.
10. Every TeamAgent has separate per-run, daily, and monthly budgets plus concurrency and proactive-work limits. A hire, package update, or increased competency does not raise them automatically.
11. Marketplace recommendations rank accepted-output value within the eligible set; sponsorship, popularity, personality, and publisher pricing cannot outrank permission, quality, safety, or budget gates.

---

## 12. Privacy, security, and trust boundaries

### Consent scopes

Keep separate durable scopes for audio capture, transcription, model analysis, organization memory, optional diagnostic raw-audio retention, and named non-human meeting participation/context sharing. A meeting's ordinary capture consent does not silently authorize a new specialist runtime to receive its transcript, analysis, or company context. A missing upstream scope denies all dependent lanes. Withdrawal fences new frames immediately, cancels/discards uncommitted work, terminates affected specialist sessions, invalidates projections and proposals, and records a body-free audit fact.

Default raw-audio policy is transient processing and deletion. Any 72-hour diagnostic retention or meeting recording archive requires explicit policy, visible disclosure, access controls, deletion proof, and business/legal approval. A consented fixed evaluation corpus is preferable to retaining every production meeting.

### Audience-safe answers

Before Scout speaks or posts, compute the current recipient audience. A result visible to the requester but not every listener cannot be spoken aloud or placed in shared chat. Private fallback must disclose that the result was withheld because of permissions without revealing its source or existence beyond what the requester is allowed to know.

### Guest boundary

- guest links are hashed, scoped, expiring, revocable, and session-bound;
- revocation evicts already-admitted guest sessions;
- guest content is untrusted data, never instruction or tool authority;
- guests have no durable company recall, work approval, project inference, or proposal access;
- guest visibility never widens an internal artifact or `#team` memory;
- guest media/chat access remains available when AI is disabled.

### Personalization boundary

Expose a “What Scout remembers about me” surface with source, scope, correction, forget, and expiry controls. Team-level norms are distinct from individual preferences. Scout may adapt format and vocabulary; it may not impersonate a person, manipulate based on inferred vulnerability, or expose one person’s private preference to another.

Agent profile text and learned relationship memory are untrusted inputs to the policy layer. They cannot alter system invariants, tool schemas, ACL queries, audience computation, budgets, approval policy, or capability tokens. Core-personality changes require a human-authored version; inferred relationship and domain learning require evidence, expiry where appropriate, inspect/correct/forget controls, and purge propagation. Agent definitions and exports exclude credentials, company memory, assignments, private performance evidence, and relationship history by default.

### Agent package and marketplace boundary

- The current marketplace admits only STRIDE-authored or organization-authored packages through closed-schema validation, provenance review, capability mapping, prompt-injection tests, dependency/SBOM checks where code exists, evals, quarantine, and an explicit curator verdict.
- Package text, assets, examples, migration instructions, requested capabilities, and publisher claims are untrusted data. They never become system instructions to the policy/control plane.
- Packages cannot launch commands, supply environment variables or credentials, register arbitrary MCP servers, choose unrestricted network destinations, mount company-brain/production volumes, or declare themselves safe. Implemented skills map to STRIDE-owned tool/workflow identifiers and run in the same isolated workers as other agent work.
- Hiring copies no publisher-controlled runtime identity. STRIDE mints a tenant-local TeamAgent and short-lived runtime principals; package provenance remains visible but grants no ongoing publisher connection.
- Company evidence is retrieved at run time under the TeamAgent's current assignment, audience, source ACL, retention, consent, and capability envelope. Learning records retain source edges and cannot widen visibility.
- Package updates are quarantined and opt-in; new permissions, data classes, tools, models, network access, side effects, memory migrations, or price bands require a new human-approved revision and fresh receipts.
- A publisher or future seller receives no organization identity, install event, prompts, conversations, memories, work artifacts, performance data, usage, or feedback by default. Public-marketplace telemetry, commerce, licensing enforcement, moderation, and cross-organization reputation require a future design and are not latent E0–E10 features.
- Pause/offboard/revoke fences new runtime generations immediately. Historical authorship remains, while memory/export/purge follows organization policy and source purge authority.

### Rich-action boundary

- `find_files` is read-only and returns only server-minted handles from the current requester's authorized inventory; a known or guessed blob/file reference never grants read access.
- Posting a file reauthorizes the exact source revision, requester, destination writer, and all current recipients. Existing shared visibility plus an explicit “drop/send this” request can authorize the post; any permission widening is a separately displayed and approved share operation.
- Reauthorization happens again on preview/open/download and after edit, delete, revoke, retention, purge, membership, or destination changes. Derived text and search entries retract with their source.
- GIF search receives only a safe abstract query class. Raw private messages, meeting transcript, personal memory, sensitive facts, names not already in the public request, and company artifacts are never sent to the media provider.
- Provider requests contain no email hash, account ID, stable user-derived pseudonym, source/thread/company identifier, or recipient address. Provider query receipts prove the allowed field set.
- Selected media is fetched/validated by STRIDE into an immutable authorized blob and served from STRIDE; clients never load provider media directly. If provider licensing/attribution terms disallow that privacy contract, agent GIFs remain off until a compatible provider/catalog is approved.
- Agent GIFs are G-rated, channel-disableable, one per invocation, logged, removable/reportable, and suppressed in sensitive or high-stakes contexts. Decorative media cannot become an asserted knowledge edge or an instruction.
- Every rich message identifies the acting coworker and on-behalf-of requester where relevant. Agent authorship provides provenance, never approval.

### Failure behavior

| Failure | Required behavior |
|---|---|
| canonical divergence | freeze affected canonical family/cutover; legacy-authoritative path continues only if its shadow contract permits; alert with exact high-water |
| consent store unavailable | fail closed for capture/transcription/analysis/org-memory lanes; room video/chat may continue |
| canonical STT unavailable | use provisional final only as visibly degraded if available; retry/reconcile; otherwise record an explicit gap |
| composer dictation fails or returns empty/low-integrity text | do not send; preserve an editable draft when useful, offer retry/discard, expire transient audio, and retain the same idempotency key across retry |
| microphone-mode handoff does not terminate | do not start the next capture generation; force-close within the bounded timeout or fail visibly with typed input still available; discard late events from the old generation |
| analysis stale | answer from authorized authoritative transcript and show analysis freshness |
| embeddings unavailable | structured/lexical/raw retrieval continues; semantic lane marked unavailable |
| Realtime unavailable | typed Scout and direct server answers remain; meeting continues |
| specialist invitation/context authorization fails | do not start the provider session or reveal withheld context; Scout explains that the specialist could not join and the human meeting continues |
| specialist Realtime/session fails after joining | cancel output, revoke context/tools, remove the agent track, record an honest terminal state and metering receipt, and keep Scout/video/chat available |
| agent audio loop, simultaneous-agent speech, or floor-controller uncertainty | cancel all agent output immediately, disable specialist floor access for the sitting, preserve human media, and page with the controller generation and event trace |
| agent profile/capability revision missing or invalid | do not instantiate or respond as that coworker; Scout may give a plain degraded reply if its own verified profile remains valid |
| marketplace package/listing signature, provenance, schema, dependency, or eval is missing/invalid | keep the listing unavailable or suspended; do not trial, hire, update, or reveal unavailable tenant-specific review details |
| hire/update confirmation races, repeats, or becomes stale | consume at most one exact revision idempotently; reauthorize role, listing, package, permissions, budget, and policy; otherwise require a new review |
| TeamAgent becomes unhealthy, over budget, revoked, or policy-incompatible | quarantine or pause new work, revoke runtime tokens and meeting eligibility, fence active stages honestly, keep history readable, and route only through a newly approved recovery |
| package update is incompatible with local overlay or memory | retain the current active revision; quarantine the candidate; show the exact incompatibility and migration/rollback plan; never discard local memory silently |
| offboarding races active work or a meeting | revoke new input/tools/context first, cancel or fence active generations idempotently, remove meeting track/memberships, and preserve attributable terminal history |
| GIF provider/filter unavailable or confidence is low | send concise text only; do not retry noisily or expose the provider query |
| file source, revision, recipient ACL, or share authority changes | fail the post/open closed, invalidate the handle, retract derived access, and offer a private or newly approved path without revealing hidden metadata |
| workflow runner down | approved run remains queued/blocked; never disappear or relaunch unauthorised |
| ambiguous external effect | terminal `ambiguous`; no automatic retry until reconciled |
| source edit/delete/revoke/purge | retract/supersede derived facts, invalidate snapshots/proposals, reauthorize artifacts |
| cost/price unknown | stop the affected route before unmetered production use |

---

## 13. Wave map

Waves are named `E0`–`E10` to avoid colliding with the existing bonfireOS 2.0 W0–W5 ledger. E1-E9 distinguish **engineering complete** from **provider-qualified**: a wave may finish its deterministic, default-off implementation while its live-quality acceptance remains explicitly pending E10.

```text
E0 live integrity and prior commissioning
  -> E1 canonical conversation + route contracts
       -> E2 meeting media, transcription, dictation, and agent-audio control
       -> E4 #team, artifacts, and collaboration memory
  E2 -> E3 temporal meeting intelligence and company-brain rebuild
  E3 + E4 -> E5 Scout cross-surface participant experience + live specialist consultation
  E3 + E4 + E5 -> E6 Suggested Work and durable orchestration
  E6 -> E7 Insights & Opportunities v1 production proof
  E2-E7 -> E8 Agent Marketplace, workforce growth, and routing/economics optimization
  E8 -> E9 resilience, native acceptance, and launch readiness
  E2-E9 -> E10 paid-provider qualification, integrated acceptance, and launch
```

### 13.1 Preregistered acceptance targets

Each wave creates a signed `AcceptanceTargetRegistry` revision **before its first measured run**. It records metric definition, fixture/corpus digest, environment, sample size, threshold, measurement code revision, accountable owner role, rollback trigger, and reviewer. The targets below are the initial minimums; a target may be tightened freely, but it cannot be loosened after results are visible without a new Strategic Design decision and independent Critic verdict. A loosened target invalidates prior qualifying receipts.

Rate metrics report numerator/denominator and a 95% Wilson interval. Latency reports p50/p95/p99 and bootstrap 95% intervals. Route comparisons are paired on identical inputs and report the non-inferiority margin. Privacy, authorization, room-isolation, consent, idempotency, and asserted-claim sourcing are zero-failure hard gates; averages cannot hide one failure.

| Surface / owner | Minimum preregistered target and method |
|---|---|
| E0 recovery — Platform/SRE + independent reviewer | snapshot completed within 15 minutes before mutation; encrypted immutable offsite digest matches local manifest; isolated restore completes within 60 minutes; 100% manifest parity across canonical DB, files/blobs, workflow state, and purge authority; rollback drill has zero lost events beyond the recorded snapshot watermark |
| release identity — Release owner | remote commit, reviewed source tree, archive digest, image build input, registry digest, running digest, OCI label, embedded binary version, environment marker, and health receipt resolve to one release; zero mismatches |
| room/media — Media owner | 200 member/guest joins across Chrome, Safari, Firefox, Expo, direct and TURN-only paths: at least 99.5% join success, p95 first remote audio ≤2.5 s and video ≤3.5 s; p95 recovery after a 10 s network loss ≤8 s; two concurrent 3×3 rooms for two hours with zero cross-room events, unintended fatal disconnects, or participant outages >5 s; p95 CPU/RSS ≤110% of the frozen actorized-Pion baseline and post-cycle RSS drift ≤5% after 20 join/leave cycles |
| transcription — Media/Intelligence owners | consented corpus ≥60 minutes and ≥120 clips: overall WER ≤10% and no more than 0.5 percentage points worse than incumbent; company/domain token accuracy ≥97%; numeric accuracy ≥98%; 100% segment/item/order/track/consent integrity over 10,000 synthetic out-of-order terminal events; p95 authoritative final ≤3 s from commit |
| composer dictation — Native/Web/Product owners | ≥250 bounded utterances across main Scout, private thread, `#team`, project, and in-room composers on target web/iPhone/iPad devices: the transcription targets above pass; p50 submit-to-post ≤1.5 s and p95 ≤3 s for clips ≤30 s; ≥99% successful first-attempt post; 100% successful transcripts post exactly once; 0 model calls after Stop without Send; 0 posts after Delete/empty/error or 100 cancellation races during `Transcribing`; 100 personal-Realtime→Dictate and personal-Realtime→meeting transitions have zero overlapping microphone generations; 100 in-room dictations leak zero dictated frames to room audio or meeting transcript; waveform/Transcribing transition holds ≥55 fps on target devices or uses the reduced-motion path |
| temporal recall — Intelligence owner | ≥200 frozen five-minute, 30-minute, explicit-clock, DST, topic, and before-admission queries: 100% interval-boundary correctness, 100% asserted claims with resolvable primary evidence, ≥95% factual precision, p95 text answer ≤5 s, and raw-transcript fallback succeeds in 100% of injected analysis-lag cases |
| `#team` retrieval — Intelligence + native owners | ≥200 author/type/channel/time cases: exact source in top one ≥95%; 0/1,000 ACL/private/guest negatives disclose a source, count, or existence; 2,000-message thread p95 initial usable load ≤2.5 s and send acknowledgement ≤750 ms; edit/delete/revoke disappears from new retrieval within one projection interval |
| `#team` coworker behavior — Product/Intelligence owners | ≥300 frozen multi-person threads with replies, reactions, corrections, jokes, files, GIFs, and ambiguous pronouns: 100% of model context turns retain canonical author IDs and reply ancestry; ≥95% human reviewers judge Scout's response contextually correct and in-character; lexical `@scout` recall ≥99% with 0 triggers across 1,000 `foo@scoutx`/quoted/metadata negatives; non-invoked human chatter produces zero agent replies |
| desktop chat experience — Web/Product owners | preregistered rendered baselines for private Scout and public/project channels at 1024, 1280, 1440, and 1728 px in current Chrome, Safari, and Firefox: message hierarchy, composer, thread navigation, unread/reply/reaction states, files, images, GIFs, links, artifact cards, loading/empty/error/degraded states, keyboard/focus, reduced motion, and ≥200% zoom all pass human design review and automated accessibility checks; initial usable thread and send latency meet the `#team` target; images/GIFs preserve aspect ratio and carry neutral outlines; every interactive target is ≥40×40 px; the protected native iPhone/iPad visual, dictation, attachment, send, scroll, unread, deep-link, and haptic suite has zero regressions from its frozen E4 baseline |
| rich file/GIF actions — Security/Product owners | ≥150 authorized file-drop cases plus 2,000 guessed-ref/private/revoked/revision-race/recipient-change negatives: 100% correct source revision, zero unauthorized bytes/metadata/counts, zero silent permission widening, and 100% list/render/open/download reauthorization; ≥200 social/high-stakes GIF cases with ≥90% appropriate-use precision, zero GIFs in defined sensitive classes, one-result maximum, 100% alt/provenance, zero raw private/meeting context or stable user/company identifier in provider requests, 100% selected media re-fetched/validated into immutable STRIDE blobs, and zero client-side provider media loads |
| coworker identity/handoff — Workflow/Product owners | 100 profile/runtime/version/model-change cases: 100% visible messages and artifacts attest stable agent plus exact run/profile/runtime revision; 100% invalid/revoked manifests fail closed; 1,000 sibling/self/transitive mention cases launch zero work; every approved handoff respects destination, scope, hop, tool, time, and cost limits; ≥90% human continuity rating before a route changes behind a stable identity |
| Scout invocation — Realtime/Product owners | ≥2,000 ordinary utterances and ≥500 explicit invocations: false spoken response ≤0.5%, explicit invocation recall ≥98%, p95 first useful audio ≤2.5 s after address completion, p95 barge-in stop ≤500 ms, and 0/1,000 audience-publication negatives leak content or existence |
| live specialist consultation — Realtime/Media/Security/Product owners | ≥100 internal invite/brief/contribute/dismiss cycles across web and Expo plus 1,000 adversarial floor sequences: 100% eligible-human confirmation and participant disclosure before specialist input; zero guest-mode enablements, unauthorized source/context disclosures, simultaneous agent speakers, acoustic agent loops, unbounded agent exchanges, or post-terminal tool/audio usage; p95 invite-confirmation to audible acknowledgement ≤5 s, p95 human barge-in stop ≤500 ms, teardown begins ≤250 ms after dismissal and reaches provider-terminal plus removed-track state ≤2 s; 100% contributions bind invitation/profile/runtime/model/context/source/audience/usage provenance; induced specialist failure interrupts zero human media sessions |
| Suggested Work — Workflow/Product owners | labeled corpus ≥400 moments including ≥100 hard negatives: proposal precision ≥90%, recall ≥75%, high-confidence destination accuracy ≥95% with abstention otherwise, no more than one unsolicited card per person per sitting unless a materially new outcome appears, 0 hard-negative launches, and 10,000 concurrent/stale approval attempts create at most one run per revision |
| I&O — Workflow owner + two eligible human reviewers | ten immutable real-input pilots from one release and route map: at least 8/10 accepted within two revision rounds, 100% asserted claims sourced, zero invented asserted claims, zero unauthorized disclosure, zero external writes, and every reject/block remains terminally visible |
| Agent Marketplace/workforce — Product/Workflow/Security owners | at least five independently gated first-party listings spanning Insights, Marketing, Research, Design, and Builder plus one organization-private no-code template lifecycle, with unavailable capability variants clearly disabled; ≥200 package/listing/trial/hire/assign/update/pause/offboard lifecycle cases are idempotent and preserve stable attribution; 0/2,000 stale/raced/unauthorized/package-injection cases grant data, tools, code, network, membership, budget, update, or work; 100% material updates show semantic permission/personality/runtime/cost diffs and remain opt-in; 100% offboards revoke new runtime/context/tool/meeting access and preserve attributable history; exports contain zero tenant data, credentials, memory, assignments, or private performance evidence; ≥200 labeled staffing cases achieve ≥90% correct eligible-agent recommendation with abstention when none qualifies; every learned preference/lesson/competency resolves to evidence and correction/purge propagation; personality continuity remains ≥90% across approved package/model updates |
| routing/economics — Routing/FinOps owner | 100% calls tagged to a valid seat and priced; 100% provider results classified accepted/rejected; quality no worse than incumbent by >2 percentage points on any non-safety seat and zero regression on hard safety cases; daily ledger/console difference ≤max(2%, $0.10); unplanned fallback ≤1% outside declared chaos windows; missing-price count zero |
| E9 resilience/native readiness — Platform/SRE + native owners | deterministic backup/restore, failover, isolation, security, and native acceptance harnesses are complete; every authority-sensitive operation is default-off; the local loopback integration must prove successful replica rerouting, persisted route choice, room-scope control isolation, signed current-state restore, purge continuity, and stale-rollback refusal. Its elapsed timings are diagnostic only: it does not exercise or qualify WebRTC/RTP/TURN/media-device continuity, the production ≤60-second session/control failover SLO, or the ≤2-second media-interruption SLO. Physical-device, production restore/failover, live-media, RPO/RTO, and soak claims remain pending E10 |
| E10 provider/integrated acceptance — Product/AI/Platform/SRE owners | every paid seat passes its preregistered live corpus; provider event contracts and prices are current; final immutable route map has no safety regression and meets latency/quality/cost targets; control/data RPO ≤5 minutes, live app/control failover ≤60 seconds, full isolated restore RTO ≤60 minutes, locked-device push/deep-link target passes, and the 24-hour/ten-sitting integrated soak has zero safety-gate failures |

| Wave | Outcome | Dependencies | Gate / rollback | Status |
|---|---|---|---|---|
| E0 | Restore truthful live integrity, reconcile release branches, and complete the token-free commissioning foundation | none | canonical/consent/recovery/release integrity; no production mutation or feature enablement on failure | `deterministic_verified` — local candidate only; live repair, consent, immutable custody, and authenticated external restore are `external_waiting` under E10 |
| E1 | One canonical conversation/evidence/projection contract plus versioned route, package, listing, TeamAgent, learning, and coworker registries | locally proven E0 integrity controls | deterministic replay, ACL differential, edit/delete/purge invalidation; closed package/profile/capability validation; shadow-off rollback | `deterministic_verified` — local/default-off |
| E2 | Stable multi-room/guest media plus correct authoritative transcription, optional live UX, dictation, Realtime 2.1 canary, and server-controlled specialist-audio primitives | E1 | STT corpus, two-room soak, voice/floor harness, device dictation; prior model/Pion and specialist-off rollback | `deterministic_verified` — adapters/control/audio/native-simulator only; live models, media devices, and physical devices pending E10 |
| E3 | Rolling meeting intelligence, exact 5/30-minute recall, late-join recap, and restart-safe full-range brain | E1, E2 | 90-day corpus, source-linked claims, honest coverage, rebuild/restart parity | `deterministic_verified` — frozen fixtures/restart only; live quality pending E10 |
| E4 | `#team` conversation compounds into permissioned links, artifacts, knowledge, collaboration profiles, and safe file/GIF actions | E1 | Erick-link and file-drop scenarios, source chips, edit/delete/revoke, rich-action ACL negatives, private canaries, locked-device delivery | `deterministic_verified` — local/default-off; provider GIF, locked device, and atomic `private_share` activation pending |
| E5 | Scout feels like a stable, informed meeting chair, conversational coworker, and read-only chief-of-staff coordinator across main screen, chat, and meetings, including one controlled live specialist consultation | E2, E3, E4 | identity/context/personality/invocation/latency/audience/floor/roster-explanation tests and rendered desktop/mobile QA | `deterministic_verified` — fake/recorded specialist and local rendered UX only; live voice pending E10 |
| E6 | Evidence-bound Suggested Work and TeamAgent assignments/handoffs route conversation, including live specialist contributions, through approval into the correct durable project thread | E3, E4, E5 | proposal precision/recall, identity/assignment/handoff, approval race/idempotency, destination/ACL negatives | `deterministic_verified` — local/default-off |
| E7 | `insights_opportunities_v1` and its first named Insights coworker produce repeatedly useful, reviewed outcomes, learn from typed feedback, and seal the first marketplace-ready package | E6 | ten fixed-release pilots, two reviewers, stable attribution, zero invented assertions/unauthorized disclosure, package/eval/rollback receipt | `deterministic_verified` — complete durable fake-stage workflow; ten paid real-input pilots/two reviewers pending E10 |
| E8 | Curated Agent Marketplace, organization-owned team agents, evidence-backed growth, chief-of-staff coordination, static seat routing, and spend controls are eval-proven | E2-E7 corpora and I&O proof | verified first-party packages one at a time; lifecycle/permission/export/continuity gates; quality non-regression, ledger agreement, and package/profile/route rollback | `deterministic_verified` — internal-preview lifecycle/default-off candidates; live capability/personality/voice/cost admission pending E10 |
| E9 | HA/DR, security, native devices, operational readiness, and launch-readiness harnesses | token-free E1-E8 engineering | deterministic restore/failover/isolation/native/security evidence; production operations remain fenced | `deterministic_verified` — temp/loopback/local-simulator evidence only; production HA/RPO/RTO, real media, physical devices, release, and soak pending E10 |
| E10 | Identity/organization/Work Record network productization, paid-provider qualification, integrated acceptance, and launch | E1-E9 engineering plus external E0 recovery/consent/custody/quota gates | W0-W8; cross-tenant identity/org/MyMind and contribution/search corpus; one-seat-at-a-time live canaries; immutable route map; integrated founder flows; production restore/failover/soak; exact-SHA launch and rollback | `production_private_live; external_acceptance_waiting` — exact carrier `cd9566b...` serves W4 person/org/session, Contribution Review, Work Record, and private network drafts for all seven current users. Publication/search/contact/MyMind, canonical promotion, physical acceptance, real-corpus qualification, HA/DR, custody, pilots, and soak remain open. |

---

## 14. Detailed execution waves

### E0 — Live integrity and token-free commissioning entry gate

**Outcome:** Establish one trustworthy baseline before adding architecture.

**Work:**

- create a clean exact-`axx/main` worktree for diagnosis and release work;
- inventory the local `design/voice-first-mobile` commits against live `main`; retain only intentionally reconciled changes and preserve `stride-site/`;
- before any production data repair, migration, or feature enablement, create a matched local snapshot plus encrypted immutable offsite copy, restore it on an isolated host, verify canonical/file/blob/workflow roots and purge authority against §13.1, and obtain an independent recovery verdict; read-only diagnosis may proceed while this recovery gate is pending;
- re-read and diagnose the canonical idempotency conflict at the then-current dirty high-water without guessing or deleting conflicting evidence;
- trace every current client-to-chat attachment path and close the blob-reference authorization downgrade before enabling or expanding Files/Scout attachment behavior; require authenticated source-object ownership/ACL, destination authorization, immutable revision binding, and negative tests for guessed refs, private sources, revocation races, and stale derived text;
- repair through the canonical journal/reconcile discipline, matched snapshots, mutation fence, and independent critic gate;
- restore durable consent authority and prove fail-closed track behavior through restart;
- build and verify the provider adapters, admission controls, deterministic fakes, recorded event envelopes, bounded canary scripts, intended-project attribution, accepted/rejected-output accounting, and cost-receipt schemas without opening an inference connection; E10 runs the paid embeddings, Responses, Realtime, transcription, and Codex canaries;
- preserve brain, recap, embedding, STT, and Scout lanes as truthfully unavailable or stale until E10 produces fresh capability-specific receipts;
- reconcile what remains from legacy W5 into E2/E3/E7 rather than marking it complete prematurely;
- capture new exact-SHA, image, backup, live-data, queue, usage, and health baselines.
- repair release attestation so OCI label, embedded binary, environment, health response, registry/running image digest, source archive, and signed receipts bind one revision;
- eliminate untagged traffic, add accepted/rejected-output accounting at every provider seam, and add current transcription/service-tier prices before comparing routes.

The live-specialist addendum does **not** reopen locally completed E0 engineering and does not authorize a feature implementation or provider session. It expands the external entry decision: before later-wave enablement, business/legal/product owners must approve the non-human participant disclosure, specialist context-sharing/retention scope, member-only first-release boundary, and budget policy. E1 creates the off-by-default contracts and kill switches; no specialist receives floor access merely because ordinary meeting transcription consent exists.

The Agent Marketplace addendum requires **no new E0 engineering and adds no E0 completion blocker**. It remains entirely default-off until later waves. E1 must make the canonical profile/capability contracts forward-compatible with package, listing, TeamAgent, assignment, learning, update, and workforce-policy records; business/product/security approval of internal hiring, memory/growth, package updates, and offboarding is an E8 activation gate. Public third-party selling remains outside this plan.

**Gate:** encrypted immutable offsite backup is current and one isolated restore is verified before the first production mutation; canonical dirty/reconciled/checkpoint high-waters equal; zero conflict/pending/frozen families; consent authority healthy; the attachment path requires authorized source object plus immutable revision and passes the full guessed-ref/private/revoked/revision-race/recipient-change zero-disclosure corpus in §13.1; no unbounded provider retries; app/video/chat remain usable during an induced simulated AI failure. E0 cannot pass for production activation while these gates remain open. Local E1-E9 implementation may proceed only behind default-off controls, without provider inference, production data repair, feature activation, deployment, or acceptance claims.

**Rollback:** no new model or feature flags. Any repair has a matched data/PostgreSQL snapshot and roll-forward journal plan. The current live release remains serviceable until a reviewed replacement is ready.

### E1 — Canonical conversation, evidence, and route contracts

**Outcome:** Meetings and shared chat enter one permission-preserving evidence system without making raw private thread state recallable.

**Work:**

- add closed schemas/reducers for ConversationEvent, TranscriptSegment/Revision, AnalysisProjection, KnowledgeAssertion, CollaborationPreference, AgentPackageManifest, MarketplaceListing, TeamAgent, AgentCoreProfile, AgentProfileOverlay, AgentCapabilityManifest, AgentAssignment, AgentLearningRecord, AgentPerformanceReceipt, AgentUpdateProposal, WorkforcePolicy, ChannelNormProfile, AgentRelationshipMemory, AgentContextEnvelope, DelegationRun, RichMessagePart, MeetingAgentInvitation, MeetingSpecialistContextEnvelope, MeetingAgentSession, MeetingAgentContribution, WorkIntent/Proposal/Run/Outcome, and answer envelopes;
- mint message-level public-channel events alongside existing thread persistence; edits/deletes/reactions/replies/files/links produce explicit versions;
- retain whole-thread UI state as non-recallable; private thread tests remain negative;
- add per-source ACL, audience, retention, purge, and revision references;
- add versioned model/seat/workflow/coworker/package/listing registries with startup validation, capability-catalog reconciliation, health output, and independent kill switches for public projection, Scout file actions, agent GIF actions, coworker context/personality/learning, TeamAgent trial/hire/update/assignment, specialist token minting, meeting-specialist invitation, specialist context assembly, specialist tools, specialist audio publication, and every visible specialist profile;
- build deterministic replay, projection checkpoints, rebuild generations, supersession, and source-to-derived invalidation;
- migrate in shadow mode with per-principal parity before any new read path becomes authoritative.

**Gate:** repeated import/replay yields identical IDs/checksums; public `#team` events are recall-eligible only through the new projection lane; private canaries never reach shared retrieval; edit/delete/purge invalidates every conversation, memory, learning, performance, and assignment-derived lane across restart; unknown schemas/routes/packages/listings/profiles/capabilities fail startup or remain quarantined/unavailable; no E1 marketplace or TeamAgent feature is user-enabled.

**Rollback:** disable new event/projection readers and continue from existing thread/transcript sources. Preserve shadow events for replay.

### E2 — Media, transcription, dictation, and Realtime foundation

**Outcome:** Stable calls and trustworthy text become the substrate for every higher layer.

**Work:**

- replace FIFO transcription attribution with app `segment_id` ↔ provider `item_id` binding and out-of-order terminal handling;
- make transcription configuration capability-aware: languages, keywords, prompt/context, latency, noise handling, timestamps/diarization availability;
- build a representative corpus spanning company names, numbers, accents, code-switching, short phrases, crosstalk, noise, silence, and speaker attribution;
- prepare the frozen comparison harness for current `gpt-realtime-whisper`, current fallback, and `gpt-transcribe`; E10 performs the paid fidelity/latency/coverage/cost comparison and owns any route migration;
- decide live captions from a real UX requirement. If enabled, add `gpt-live-transcribe` as provisional-only with reconciliation and separate metering;
- build one shared web/Expo composer-dictation component that transforms the existing input rectangle in place: real input-level waveform, X/Delete, Stop, Send arrow, literal `Transcribing` state with compact progress, final-only text post, editable failure fallback, transient encrypted audio, bounded company vocabulary/language hints, and exactly-once message IDs;
- implement the `AudioFocusCoordinator` and generation fences for `composer_dictation`, `personal_realtime`, and `meeting_media`; personal Realtime must terminate before Dictate or room join begins, and an in-room dictation must privately mute/restore the room track while excluding dictated frames from meeting evidence;
- build the exact `gpt-realtime-2.1` contract/canary harness against the current `gpt-realtime-2@high` event schema with deterministic fake and recorded-event adapters; E10 performs the paid high/low-effort comparison and changes one variable at a time;
- add a separate backend WebSocket Realtime session primitive for a synthetic one-specialist consultation behind a hard kill switch; keep provider credentials server-side and bind session/audio/tool events to one invitation, short-lived runtime principal, and metering record. E10 opens the first real specialist provider session only after Scout's live route passes;
- add `MeetingAgentFloorController` generations and leases: one agent speaker at a time, human barge-in priority, no synthesized-agent-audio feedback, no autonomous agent turn chain, distinct verified agent track, bounded cancel/dismiss teardown, and zero effect on human media when the specialist lane fails;
- finish actorized Pion lifecycle, media-generation fences, cleanup, retransmission/recovery, screen-share, speaker/expanded/gallery modes, and mobile/native behavior;
- prove guest redemption, green room, consent, listen-only, revoke/expiry eviction, and cost/connection caps;
- bind guest-link identity into redeemed sessions and evict active sockets/seats immediately on revoke or expiry, with journal/replay proof;
- activate guest-safe sitting policy on first guest admission, not merely an unused link;
- add named-room `catch_me_up`, require an authenticated requester for private recap, and forbid fallback from a private audience to the room;
- publish per-room Scout, STT, transcript, consent, and media health rather than relying on office/global health;
- retain Pion as rollback; evaluate managed media only through the same gate.

**Token-free gate:** every deterministic E2 state, authorization, ordering, cancellation, audio-focus, room-isolation, failure, kill-switch, and recorded-event contract passes; the specialist harness proves separate backend session binding, one-agent floor leases, human interruption, teardown/metering, no acoustic feedback, and no human-media impact; screen share/rejoin/crowded/mobile cleanup pass in local/simulated functional cases. Live transcription accuracy/latency, Realtime behavior, physical-device performance, and paid-provider compatibility remain explicitly pending E10 and cannot be represented as E2 provider acceptance.

**Rollback:** previous transcript model, Realtime model/effort, and actorized Pion are single-pointer/new-sitting rollbacks. No model switch hot-swaps an active session.

### E3 — Temporal meeting intelligence and restart-safe brain

**Outcome:** STRIDE can answer exact live and historical questions from evidence even when a projection lane is behind.

**Work:**

- build event-driven rolling analysis over authoritative finalized turns;
- maintain typed current meeting state for decisions, commitments, blockers, storylines, alignment, positions, questions, entities, artifacts, and work candidates;
- store exact analysis window and `through_segment_id` high-water;
- implement 5-minute, 30-minute, explicit-clock, topic-relative, and before-admission temporal queries;
- implement late-join recap from first-admission watermark and half-open intervals;
- add transcript-first fallback when analysis is stale;
- add full authorized inventory, hybrid retrieval, claim/evidence edges, honest coverage, and continuation for bounded limits;
- rebuild brain projections deterministically across restart, correction, supersession, revoke, retention, and purge;
- support evolution/contradiction/position queries without hiding prior states;
- expose separate transcript, analysis, embedding, brain, and recap freshness in readiness/capabilities.
- build the ACL/consent-first specialist context assembler over exact authorized transcript revisions, structured meeting analysis, company/project evidence, active approved-work state, source refs, high-waters, gaps, and budgets; historical raw audio and unrestricted brain retrieval remain excluded;

**Gate:** at least 40 meetings/90 days/250 records; exact DST/calendar/join-relative windows; 100% asserted-claim evidence resolution; mixed fresh/stale/missing/failed coverage; raw fallback proof; per-principal ACL differential; repeat rebuild identical IDs/checksums; live late-join and 5/30-minute founder test; specialist context-envelope fixtures expose only the approved transcript interval, analysis, brain evidence, and work state, block every audience/consent/purge mismatch, and report exact freshness/gaps.

**Rollback:** projection reads off; authoritative transcript and existing read-only recall remain. Checkpoints cannot advance past failed source windows.

### E4 — `#team`, artifacts, and collaboration memory

**Outcome:** The company’s primary group conversation compounds into useful, correct, inspectable shared memory, while desktop private and channel chat becomes as polished and delightful as the protected native experience.

**Work:**

- project public `#team` events into links, files, decisions, commitments, storylines, entities, aliases, and work candidates;
- preserve canonical author principal, display name, reply ancestry, reaction actors, safe attachment metadata, and channel-policy revision in the context projection; retire flat anonymous-human history for shared-channel Scout turns;
- replace substring mention detection with one shared lexical/token-boundary parser and prove quoted/metadata/file/GIF content cannot summon Scout;
- add safe link preview and artifact index with author/channel/time/project fields;
- implement structured author/type/channel/time filtering before semantic ranking;
- implement thread-scoped catch-up, Ask the Thread, cross-source chips, deposit rail, and source navigation;
- replace post-hoc phrase-overlap citations with retrieval-issued evidence IDs/revisions for new Scout answers; under-claim visibly when no source qualifies;
- complete native/table delivery quality: locked-device push and receipts, unread boundary, mention deep link, previews/mute, reactions, photos/files, link cards, append-not-refetch, long-thread performance;
- rebuild the desktop private/channel chat presentation as a first-class product surface: strong conversation hierarchy and width use, persistent but unobtrusive thread context, a refined sticky composer, clear authorship/grouping/time/unread/reply/reaction behavior, polished hover/focus/press/keyboard states, and complete loading/empty/error/degraded experiences;
- render rich media natively in the desktop conversation flow—responsive image/GIF previews, safe link cards, file/PDF cards, artifact/result cards, source/provenance and download/open actions—with stable aspect ratios, bounded dimensions, authorization on open, accessible fallbacks, and no provider-origin client fetches;
- establish frozen native iPhone/iPad interaction and screenshot baselines before shared API/component work; run desktop changes through isolated CSS/DOM paths where possible and require the native dictation, attachment, send, scroll, unread, deep-link, haptic, and performance suites to remain unchanged;
- perform evidence-captured desktop QA through ordinary navigation at 1024/1280/1440/1728 px in current Chrome, Safari, and Firefox, including long/short threads, private/public/project contexts, keyboard-only use, reduced motion, ≥200% zoom, and responsive boundary cases;
- add explicit team norms and collaboration-profile projection, inspection, correction, expiry, and forget controls;
- add server-minted `find_files` selection handles and `post_file_to_thread` with source-revision/destination-audience reauthorization; never pass a client-supplied blob ref as authority and never widen access implicitly;
- replace user-derived provider identifiers and client-side provider media fetches in both the human picker and agent path; expose the G-rated search foundation through one shared privacy-compatible server boundary, with Scout's bounded `search_gif` tool additionally using abstract safe intents, no stable user/company identifier, server-side fetch/validation into an immutable STRIDE blob, a sensitive-context deny policy, one-result cap, alt/provenance, delete/report controls, licensing/attribution approval, and desktop/mobile parity;
- add channel-level controls for agent GIFs, proactive assistance, memory/work sensing, and specialist membership;
- reserve an explicit “save for the record/share with project/company” conversion for private chat, but keep `private_share` rejected through E1-E9: an object/event reference is not share authority. A later activation requires one atomic, durable, revision-bound, revocable grant protocol plus its own consent, race, restart, purge, and source/destination reauthorization evidence;
- teach deletion/edit/revocation to retract or supersede knowledge, digests, link records, and proposals.

**Gate:** every `#team` retrieval, coworker-behavior, desktop-chat, and rich file/GIF target in §13.1 passes; desktop private, public, and project-thread scenarios receive a signed product/design acceptance receipt from rendered Chrome/Safari/Firefox evidence at all registered widths; the frozen iPhone/iPad visual and interaction corpus has zero regressions; meeting query finds the exact Erick link from last week and posts it with a tappable source; file-drop resolves and posts only an exact already-shared revision; edit/revoke removes access; an unauthorized guest cannot learn that either item exists; private-chat canaries remain absent; `private_share` fails closed until the atomic-grant work above is separately approved and proven; source chips reauthorize on open; locked iPhone receives and deep-links to the target; 2,000-message thread remains responsive; each E4 kill switch is exercised and blocks new model/tool actions without breaking human chat or historical ACL-correct rendering.

**Rollback:** independently disable public-channel projection, cross-surface retrieval, Scout file search/posting, and agent GIF search/posting. Human `#team` and ordinary ACL-correct attachment uploads remain available. The human GIF picker remains available only through the privacy-compatible shared server boundary; otherwise it is disabled too. Historical Scout rich messages remain inert audited records whose file/GIF parts are still ACL-filtered and served only from retained authorized blobs.

### E5 — Scout as an informed company participant

**Outcome:** Scout feels present and context-aware without becoming intrusive, confusing, or unsafe, and can explain and coordinate the currently enabled workforce without gaining human hiring authority.

**Work:**

- implement surface-aware addressability and visible meeting engagement state;
- register Scout's first human-authored `AgentCoreProfile`, validated capability manifest, channel norms, and relationship-memory inspection/correction surface;
- give Scout a read-only typed workforce view over the currently enabled profiles and bounded tools to explain eligibility, assignments, health, budget, and blockers and draft—but never confirm—future roster changes; the Marketplace/hire lifecycle remains disabled until E8;
- build the server-side `AgentContextEnvelope` for private, `#team`, project, meeting-text, and meeting-voice surfaces; bind author identities, thread ancestry, evidence, active work state, tools, profile version, audience, and freshness without raw brain dumps;
- add meeting Ask Scout button, text mention, spoken-name detection, follow-up window, dismiss/timeout, and barge-in;
- connect Realtime to server answer/context envelopes rather than raw brain dumps;
- implement the member-only Scout-chaired specialist invitation experience: spoken/text request, eligible-human confirmation, context disclosure, verified-agent roster/join/active-speaker states, Scout's source-grounded handoff, one specialist at a time, human interruption, natural “thanks Mary”/Remove dismissal, and honest failed-to-join behavior;
- integrate `MeetingSpecialistContextEnvelope` with the E2 server session and floor controller; keep Scout and the specialist in separate Realtime sessions, pass structured text/context rather than synthesized audio between them, and prohibit free-form agent-agent conversation;
- qualify E5 with one human-authored, internal-only, allowlisted pilot specialist manifest and no side-effecting tools; broad production enablement of Mary/Marketing or any other additional profile waits for its E8 profile, voice, continuity, cost, and rollback receipts;
- add rich meeting-chat publication for links, artifacts, decisions, and evidence;
- preserve public-channel `@scout` gate and private/direct always-answer semantics;
- implement the response-mode policy for conversational text, text plus one GIF, GIF-only, file/artifact card, or safe refusal; preserve Scout's personality while deterministic sensitive-context and authority gates remain final;
- implement audience-safe speech/post checks and private fallback;
- make proactive suggestions quiet, deduplicated, confidence-bound, usage-accounted, and dismissible;
- expose listening, transcribing, thinking, answering, degraded, and privacy states truthfully;
- retain typed and touch equivalents for every voice path;
- perform rendered desktop, mobile, accessibility, reduced-motion, and actual device QA.

**Gate:** every Scout invocation/latency/audience, live-specialist consultation, `#team` coworker-behavior, rich-action, and identity-continuity target in §13.1 passes; plain team/meeting chatter does not summon Scout outside the permitted false-response ceiling; spoken/text/button invocation, visible follow-up, in-character multi-person reply, authorized source opening, and AI-off typed/navigation fallback pass their functional cases; specialist invitation/context/tool/audio kill switches each leave human media and Scout's verified fallback usable; the E5 coworker-context/personality kill switch restores the verified bounded-Q&A route with no new relationship-memory reads.

**Rollback:** independently disable specialist invitation, context, tools, and audio, revoke active specialist sessions/tokens, remove their tracks, and retain attributed historical contributions as inert audited evidence. Disable room voice invocation, enriched coworker/profile routing, relationship-memory injection, and rich response modes as needed; retain typed `@scout`/private Scout on the last verified bounded Q&A route. Revoke new Scout capability tokens, preserve historical messages as inert audited records, and keep passive transcript/analysis and human media independent.

### E6 — Suggested Work and durable outcome orchestration

**Outcome:** A real work outcome heard in conversation becomes one safe, understandable, approved, traceable TeamAgent assignment and run in the right project home.

**Work:**

- implement WorkIntent detection on authoritative evidence only;
- admit attributed `MeetingAgentContribution` records to WorkIntent detection only through the ordinary authorized projection lane; a specialist idea or Scout dismissal never creates approval or a run;
- build confidence/counterevidence/dedupe evals and relevant-person selection;
- render revisioned Suggested Work cards with evidence, destination, owner/reviewer, authority, budget/time, expiry, and dismiss reason;
- implement project/thread resolver and explicit new-thread creation flow;
- consume one approval atomically into one idempotent WorkRun;
- implement durable stage/checkpoint/retry/awaiting-input/review/blocked/cancel states;
- implement named-specialist manifests, membership, verified-agent rendering, short-lived runtime principals/capability tokens, and typed `DelegationRun` handoffs with zero transitive hops by default;
- bind every specialist handoff to an `AgentAssignment` and current TeamAgent/capability revision when a hired roster exists; before E8, use the same contract with the single allowlisted Insights/pilot principal so later Marketplace activation does not fork orchestration semantics;
- adapt/retire broad legacy `codex_proposal` broadcast, any-member confirmation, and public/`#general` fallback semantics behind WorkProposal recipients, destination membership, and canonical approval;
- add cross-process leased WorkRun/job claims with fencing tokens before more than one runner replica exists;
- add per-stage model route and capability snapshot;
- bind every artifact/result to run, evidence, destination, and source permissions;
- report progress/completion in the bound thread and project/company brain;
- enforce Scout as coordinator/front door: durable specialist work posts only in bound work/project threads under the specialist's own identity and returns one typed result; the §4.17 live-consultation exception remains attributed conversation evidence and never schedules work through mentions, thanks, or free-form sibling chatter;
- support typed feedback, correction, re-run-from-new-revision, and proposal quality analytics;
- keep all external/destructive actions behind separate current approvals.

**Gate:** every Suggested Work precision, recall, destination, noise, hard-negative, approval-race, and coworker identity/handoff target in §13.1 passes before E7 can start; invalid or revoked manifests fail closed; self/sibling/transitive social mentions launch no work; hop/tool/time/cost limits hold; edits/revokes invalidate proposals; wrong-project/public-fallback cases require human choice; restart resumes once; ambiguous provider effects do not repeat; rejected/dismissed reasons improve offline evaluation without silently retuning production.

**Rollback:** disable detector/suggestion surfaces and new specialist handoffs; revoke affected specialist manifests, runtime principals, and capability tokens; fence active specialist stages into honest `blocked` or `cancelled` states; preserve their messages/artifacts as inert audited records. Explicit work can return to Scout or non-social system-role execution only through a new revision and human reapproval. Existing runs remain readable and terminally honest.

### E7 — Insights & Opportunities v1

**Outcome:** The one first-class workflow repeatedly creates a decision-ready product that improves from reviewed feedback and produces the first marketplace-ready, evidence-backed coworker package.

**Work:**

- finalize versioned request, evidence, opportunity, report, critic, feedback, artifact, and outcome schemas;
- select and human-approve the first named Insights analyst profile; bind every visible contribution, artifact, model/runtime revision, and feedback item to that profile and the WorkRun while Scout retains coordination;
- seal the passing Insights profile, capability manifest, eval bundle, example artifact, cost evidence, and update/rollback metadata as the first verified `AgentPackageManifest` and draft `MarketplaceListing`; keep marketplace discovery/hiring disabled until the E8 lifecycle/security gate passes;
- define report sections, claim standards, confidence/counterevidence, expected impact, next action, owner, and decision status;
- assemble the internal workflow profile using appropriate goal ownership, strategic design, staged execution, and bounded criticism;
- pin orchestrator, researcher/subagent, writer, critic, and verifier routes/reasoning;
- pin the verified incumbent route for each E7 pilot unless that exact seat has already passed its authorized canary; record the route map in every run receipt;
- keep source snapshot immutable and create immutable report revisions;
- require criterion/claim-level critic verdicts and evidence links;
- make feedback easy from the report/thread and connect accepted corrections to the next workflow version/eval set;
- produce printable/shareable artifact only within approved workspace authority;
- prepare ten immutable pilot fixtures, reviewer rubrics, deterministic fake-stage outputs, and the complete launch/review/feedback/revision harness. E10 runs the ten paid-model pilots with at least two eligible reviewers and real authorized company inputs.

**Token-free gate:** authorization, evidence binding, immutable revisions, duplicate-launch idempotency, restart/resume, bounded criticism, blocked/rejected visibility, feedback lineage, package sealing, and zero external-write paths pass on frozen deterministic fixtures. The ≥8/10 human acceptance, model-groundedness, latency, and accepted-output-cost gates remain pending E10.

**Rollback:** disable `BONFIRE_INSIGHTS_OPPORTUNITIES_V1_ENABLED`; preserve runs/artifacts/feedback for audit and later replay.

### E8 — Agent Marketplace, workforce growth, routing, and economics

**Outcome:** People can safely hire, understand, shape, work with, update, pause, and offboard a distinctive roster of organization-owned agent coworkers; Scout coordinates them as a grounded chief of staff; every active seat remains intentionally chosen and cheaper routes are used only where quality evidence permits.

**Work:**

- implement immutable package ingestion, closed-schema/provenance/dependency validation, quarantine, capability-request mapping, eval bundles, curated listing review, suspension/revocation, and one-pointer package rollback; accept only STRIDE-authored and organization-authored packages in this plan;
- build the responsive web/Expo Agent Marketplace with outcome/category search, personality preview, evidence/sample results, required-access and cost summaries, package/publisher/version provenance, **Try on a sample**, and revisioned **Hire to team**; no third-party payments, seller accounts, rankings, reviews, or telemetry;
- add an admin-only **Create from template** path for organization-private agents using closed persona/role/assets/capability-request fields, preview, validation, and the same quarantine/eval/curation lifecycle; it accepts no arbitrary code, command, hook, environment value, credential, or raw MCP configuration;
- build the team roster and coworker detail surfaces for direct chat, responsibilities, assignments, channel/project membership, access, memory, skills, feedback/growth, activity, health, cost, versions, profile preview/diff, correction/forget, pause, and offboarding;
- implement idempotent TeamAgent trial/hire/activate/pause/quarantine/offboard lifecycles, tenant-local runtime identity, direct thread, local profile overlay, workforce-policy authorization, short-lived principals, budget/concurrency/proactivity limits, and historical-attribution preservation;
- implement source-linked relationship/domain learning and reviewed performance receipts; keep the company brain shared, propagate correction/revoke/purge, block sensitive inference, and require a human-approved capability revision plus eval receipt before competency growth changes eligibility;
- implement opt-in package/profile/capability/runtime updates with semantic permission/personality/model/cost/migration diffs, quarantined trial, continuity/capability/security evals, local-overlay/memory compatibility, explicit human activation, and rollback;
- implement Scout's chief-of-staff tools for eligible-agent search, roster/status/budget Q&A, draft hire and assignment recommendations, WorkRun routing, introductions, progress/blocker monitoring, coaching/profile proposals, and pause/offboarding recommendations; deterministic eligibility and authority precede model ranking;
- stage the E7 Insights package/listing plus Marketing/Mary, Research, Design, and Builder packages as unavailable candidates; validate every token-free package, identity, authority, memory, lifecycle, rollback, and applicable simulated floor contract. E10 qualifies and admits each listing or capability variant one at a time only after its live task, cost, continuity, and voice receipts pass;
- activate E8 in fixed order: read-only catalog -> Insights trial/hire lifecycle -> update/pause/offboard/export -> Scout workforce coordination -> Marketing/Mary -> Research -> Design -> Builder -> final route/economics changes. Do not combine a new package/profile/capability activation with a model-route change in the same canary, and do not advance a later profile past a failed earlier lifecycle control;
- freeze corpora and baseline receipts for STT, voice, extraction, recall, board/suggestion operations, proposal routing, I&O generation, critic, and Codex work;
- validate every configured model ID and supported parameter at startup/canary time;
- prepare one-seat-at-a-time Luna/Terra/Sol, Realtime, and STT canary definitions from §11 with exact route/prompt/schema/corpus digests and rollback pointers; E10 runs them and never changes more than one variable at a time;
- prepare paired current-versus-lower reasoning, prompt-cache/persisted-reasoning, Programmatic Tool Calling, and bounded Responses multi-agent experiments; E10 executes only the experiments whose surrounding deterministic safety and accounting gates pass;
- prepare deterministic Marketing, Research, Design, and Builder coworker fixtures one package/profile/capability at a time for explicit consultation or approved project-thread work; require token-free authority, artifact, budget, learning, export, offboard, and no-agent-loop receipts now, with live capability/personality/voice qualification deferred to E10;
- prepare the invited-specialist voice experiment separately from each profile's durable work route, including Realtime-only and optional bounded Terra `medium` preparation variants; E10 measures useful-idea rate, latency, interruption, context fidelity, and accepted-output cost before enabling either variant;
- retain independent review provider and shadow any proposed critic change;
- complete accepted-output cost, price tables, console reconciliation, spend/fallback/staleness alerts, and per-workflow budgets;
- prepare the 24-hour/ten-sitting soak harness and final-route replay manifest. E10 runs both after the last qualified route change.

**Token-free gate:** package/listing/trial/hire/assign/learn/update/pause/offboard/export lifecycles are idempotent, permission-safe, purge-correct, default-off, and rollback-proven; deterministic staffing/authority fixtures pass; no seat, listing, hire, capability, memory, or visible profile changes on missing/invalid receipts; one package/profile/manifest/token/kill-switch rollback is demonstrated per candidate coworker. Live quality, personality continuity, voice, latency, accepted-output cost, provider-ledger reconciliation, final route changes, visible listing admission, and integrated replay remain pending E10.

**Rollback:** disable Marketplace discovery/trial/hire/update and Scout workforce mutations independently while preserving read-only roster/history; suspend affected listings; restore the prior package/profile/capability and route revisions; pause or offboard each E8-enabled Marketing, Research, Design, or Builder TeamAgent; revoke runtime tokens, stop new consultations/handoffs, and remove any active specialist audio track. Active affected stages fence into honest `blocked`/`cancelled`; resumption through Scout, the Insights profile, or a prior system role requires a new approved revision. Historical agent contributions/messages/artifacts remain inert and attributable; local overlays and compatible memory remain available for rollback rather than being silently discarded.

### E9 — Resilience, native acceptance harnesses, and launch readiness

**Outcome:** Every token-free operational, isolation, security, native, recovery, and launch mechanism is implemented and deterministically exercised without claiming that the provider-integrated system is live-qualified.

**Work:**

- implement the configuration, automation, manifests, validation, and fail-closed controls for managed PostgreSQL HA, recurring encrypted immutable offsite object storage, independent signing/KMS custody, and a durable separate restore host; actual account provisioning and authority-sensitive activation remain externally gated;
- complete purge-authority-preserving four-root backup/restore and deterministic RPO/RTO drill harnesses; E10 runs the qualifying external drill;
- add redundant app/TURN/routing definitions and health-based reversible traffic-shift controls without changing live traffic;
- keep media routing independent enough that one app/provider failure does not end active calls;
- add capability-specific paging and operator runbooks for canonical, consent, transcript, analysis, brain, embeddings, Scout, workflow, queue, backup, and cost;
- complete native signing/privacy manifests plus automated device-test manifests and local/simulator acceptance for `#team`, dictation, video, screen share, push, and background/foreground recovery; TestFlight upload and physical-device acceptance remain E10 operations;
- execute security review: route allowlists, guest DoS, SSRF/link previews, blob authorization, CSRF/origin, capability tokens, secrets, worker isolation, retention/purge, agent-package/listing supply chain and prompt injection, update/export/offboard isolation, and dependency/vulnerability scans;
- run workers in per-run ephemeral worktrees/containers with bounded writable mounts, egress allowlists, short-lived scoped credentials, CPU/memory/time/network quotas, signed callbacks, and no company-brain or production-volume mount;
- build an exact-SHA release candidate and prove source/image/manifest/backup identity locally without deploying it;
- prepare the integrated founder script and 24-hour/ten-sitting soak harness;
- exercise repeated simulated Scout-plus-one-specialist consultations in concurrent rooms, consent withdrawal, quota exhaustion, Realtime disconnect, participant churn, restore/failover, and specialist kill-switch drills; one specialist failure must never end or cross-contaminate a human call;
- exercise the integrated workforce founder script against deterministic fixtures: discover and inspect Mary, trial, hire with bounded permissions/budget, direct-message her, add her to Dog Perfect, have Scout choose and introduce her, invite her into a simulated meeting, approve one WorkRun, inspect/correct a learning record, preview and roll back a profile/package update, pause/offboard, and prove that history remains attributable while all new access is revoked.

**Token-free gate:** every E9 deterministic resilience/native-readiness target in §13.1 passes; simulated restore/failover and purge rollback fail closed; two-room/media/recall/#team/Scout/Suggested Work/I&O/Agent Marketplace/workforce fixtures pass from one candidate tree and frozen placeholder route map; no unauthorized observation, stale capability, or suspended package is presented as healthy/available. External signed restore, physical-device acceptance, paid routes, deployment, live traffic shift, and integrated soak remain pending E10.

**Rollback:** reversible traffic shift to retained app/media release, previous route map, and authoritative data path; never restore content behind purge authority.

### E10 — Paid-provider qualification, integrated acceptance, and launch

**Outcome:** Convert the deterministic E1-E9 candidate into one evidence-bound, provider-qualified, production-accepted STRIDE release.

**Entry gate:** before any real company/user corpus, provider-seat qualification,
route migration, or production activation, the production provider project has
bounded usable quota; current official model availability, parameters, event
schemas, and price tables are reverified; immutable offsite retention,
independent key/signing custody, durable consent authority, authenticated signed
restore, Apple/device access, and every applicable business/legal/product
approval in §16 are current. No paid call begins merely because code is ready.
A separately authorized single synthetic contract observation may precede the
full launch prerequisites only under the narrow exception in the E10 provider
runbook: generated non-sensitive input, exact candidate/probe/price/budget
binding, one-attempt stop, no fallback or mutation, and evidence permanently
classified as `provider_contract_attempt`. An unreconciled project, synthetic
input, or contract receipt can never satisfy this entry gate for qualification
or activation. This is the exception used for the bounded 2026-08-01 contract
attempts; it is not a retroactive qualification waiver.

**Ordered stop/resume queue:** this sequence is dependency-bound. A later item cannot be used to waive an earlier one.

1. Execute E10-W0: freeze one local candidate, refresh the exact serving ledger/
   images/rollback and canonical high-waters, reconcile stale ledger claims, and
   complete deterministic normal, race, dependency, authorization, native/
   simulator, founder, restore, and independent-critic gates.
2. Complete E10-W1-W3 without production mutation: identity/organization/network
   authority, product surfaces, tenant-principal conversion, cross-tenant
   negatives, and an offline reviewed Bonfire migration rehearsal. Keep every
   new authority and reader default-off.
3. **STOP before every paid provider call, production data/config mutation,
   canonical repair/promotion, Git integration, release, deployment, or cohort
   activation.** Resume only with the exact applicable user authority.
4. Before any real company/user corpus or provider qualification, complete the
   §16 business/privacy/consent approvals, intended OpenAI project/billing/spend
   reconciliation, immutable offsite retention, independent key/signing custody,
   authenticated signed restore, and Work Record/network attribution/search/
   contact/privacy approvals required by the entry gate. The narrow
   synthetic contract exception above does not waive this item.
5. With separate exact authority for each operation, execute E10-W4's Bonfire
   migration/organization/Work Record private-draft canary and canonical repair/
   promotion. Organization and private contribution preview may pass while
   `person_mymind_context=false`; network discovery/contact remain off until W6,
   and E10-W5 MyMind remains separately gated by encrypted custody and consent.
6. Execute E10-W5 only after its independent custody/consent gate. Work Record
   and network implementation must not read or wait on MyMind. W5 must either
   complete with its own receipts or be explicitly deferred by AJ; it cannot
   remain silently pending when W8 or the full plan is called complete.
7. Reverify current official model access, endpoint/event/parameter compatibility,
   reasoning controls, price tables, accepted-versus-rejected accounting, and
   bounded spend; then run the smallest authorized exact probe per seat. Stop
   that lane on the first schema, policy, authority, price, quality, or ledger
   failure; do not hide it behind a fallback.
8. Qualify authoritative meeting STT and composer dictation; personal/meeting
   Scout on `gpt-realtime-2.1`; the separate invited-specialist voice lane; then
   transcript analysis, recall, `#team`, Suggested Work, I&O, coordination, and
   Marketing/Research/Design/Builder one model/effort/prompt/tool-policy variable
   at a time. Separately qualify Work Record/search/contact on at least five
   consenting profiles, two realistic reviewers, and the labeled/adversarial
   prohibited-query, explanation, revocation, purge, exfiltration, and contact
   corpus. `gpt-live-transcribe` ships only for a qualified consumer.
9. Freeze the final route map, rerun every affected corpus, run ten real I&O
   pilots with two eligible reviewers, complete the final exact native build's
   physical iPhone/iPad/TestFlight organization/Work Record/publish/pause/search/
   evidence/contact/block/revoke matrix,
   restrictive-network TURN/real-WebRTC, production failover, and qualified
   founder/workforce flows.
10. Only then reconcile an intentional release scope excluding `stride-site/`
   and unrelated user work; bind an exact commit/tree/build/image/config;
   commit/push/deploy only when expressly authorized; observe production, prove
   rollback, run the 24-hour/ten-sitting soak, and activate only passing cohorts
   in order: organization/profile, contribution draft/publish, evidence search,
   then contact—each independently kill-switchable.

**2026-08-01 E10 contract checkpoint:** the user confirmed quota and authorized use of the existing OpenAI key for bounded provider checks. The independently gated harness froze candidate manifest `ffbb8ad389afdced67162cdb4987d64d6406eeb7c0ef2c235f30e3885440df26`, probe binary `b7c7e984c4489e8d5113f3b83e29d3231f2b3b1a2377b198034c21718a95d699`, a 5.509-second synthetic WAV, and its reference digest. Zero-generation model-object access passed for `gpt-realtime-2.1`, `gpt-transcribe`, `gpt-live-transcribe`, `gpt-5.6-luna`, `gpt-5.6-terra`, `gpt-5.6-sol`, `text-embedding-3-small`, and `gpt-image-2`. One `gpt-transcribe` file request passed the exact multipart and strict usage-union contract: HTTP 200, 1.647-second request latency, provider-reported 6.0 seconds, computed cost USD 0.00045 under a USD 0.001 admission ceiling, and non-empty output retained only as a hash/character count. The API returned no documented project echo and the connected Platform helper required reauthentication, so receipts truthfully record `project_scoped_api_key` / `project_credential_bound_unreconciled`; this permits contract observation only and cannot satisfy intended-project/billing reconciliation or any qualification/activation gate. The ten sanitized receipts and their original-file digests are in `docs/evidence/e10/openai-contract-receipts-20260801.jsonl`. No production, Git, route, configuration, feature, device, or release mutation occurred.

**2026-08-01 E10 Realtime continuation checkpoint:** after the initial response-shape failure recorded in `openai-realtime-contract-attempt-20260801.jsonl`, one authorized corrected transcription attempt exposed the current modern `conversation.item.added` → `conversation.item.done` lifecycle; the harness was corrected and independently re-gated. A freshly frozen synthetic committed-turn attempt then passed on `gpt-transcribe`: HTTP 101, 26 correlated events, exact commit/created/finalized/completed item identity, 71 normalized transcript characters retained only as a hash, 1.007-second transcription latency, 2.152-second total latency, six provider-reported seconds, and USD 0.00045 under the USD 0.001 ceiling. This is a narrow endpoint/event/correlation/accounting contract pass, not WER, quality, device, or seat qualification.

The first Scout contract attempt observed only `session.created` and `session.updated`, then timed out because the frozen harness incorrectly required optional `conversation.created`. After a local fix, fresh freeze, and independent gate, the user authorized exactly one retry. That retry reached partial transcript/audio generation and the assistant final-item boundary, but the 48-token/high-reasoning response ended with a documented incomplete item that the then-frozen parser rejected before `response.done`; therefore provider usage/cost was not reconciled and the zero output/token/cost fields in that legacy raw receipt are not proof of zero output or spend. The retry is a failed-closed contract attempt, not a provider incompatibility and not a Scout pass. The local harness now accepts documented completed/incomplete item states, treats `response.done.status=completed` as the sole success authority, preserves partial-output evidence, records explicit usage/cost truth, accepts current optional usage and failure-detail fields, and raises the bounded output cap to 256 with a conservative USD 0.018432 worst case under the USD 0.02 admission ceiling. Exact source/test hashes passed focused normal 20×, focused race 3×, package normal/race, vet, formatting/diff, current official-contract review, and an independent Critic; it has not been provider-retested. The four exact raw receipts, candidate bindings, fact/inference sidecars, and local correction are durable in `docs/evidence/e10/openai-realtime-contract-final-20260801.jsonl` (SHA-256 `4625652b65c39ea2673a9687cb54d66f5fa74cf20a4dccb8188008283215c69f`). Project/billing attribution remains unreconciled, no lane is `provider_qualified`, and no production, route, configuration, Git, feature, device, or release mutation occurred.

**Work:**

- run the smallest exact contract probe for each configured OpenAI, Anthropic, and Codex adapter, stopping on the first schema, policy, authority, pricing, or accounting failure;
- qualify authoritative meeting STT and composer dictation on the frozen audio corpus, then optional live transcription only if a shipped provisional consumer still justifies it;
- qualify personal and meeting Scout on `gpt-realtime-2.1`, one effort and one newly created session cohort at a time, preserving the incumbent new-session rollback;
- qualify transcript analysis, temporal/company recall, `#team` responses and rich-action judgment, Suggested Work, I&O, specialist voice, workforce coordination, and every candidate coworker against the preregistered corpora and human-review gates;
- change Luna/Terra/Sol/critic/Codex seats one at a time; bind exact model, effort, prompt, schema, route, price, release, corpus, accepted-output, and rollback receipts; rerun every impacted downstream corpus after the final immutable route map;
- run the ten real I&O pilots, specialist and workforce founder flows, physical iPhone/iPad acceptance, qualifying four-root restore/failover drills, exact-SHA deployment, production observation, and the 24-hour/ten-sitting soak;
- enable only the capability/listing/route cohorts whose complete live receipts pass. Keep every failed or untested lane honestly unavailable and independently kill-switchable.

**Gate:** every applicable target in §13.1 and product promise in §15 passes from one immutable release and final route map; all asserted claims resolve to authorized evidence; provider and internal ledgers reconcile; no missing price, stale capability, unauthorized disclosure, duplicate side effect, acoustic agent loop, or hidden fallback remains; exact-SHA launch and retained-release rollback are both proven.

**Rollback:** disable or restore one route, capability, profile, package, listing, or new-sitting pointer at a time; fence active durable work into an honest terminal/blocked state; use reversible traffic shift to the retained exact release; never roll back past purge authority or replay an ambiguous external effect.

---

## 15. Final acceptance evidence matrix

| Product promise | Required evidence |
|---|---|
| best-in-class calls | 2×3-person two-hour rooms; gallery/speaker/expanded/screen share; packet loss/disconnect/CPU/RSS; rejoin, cleanup, browser/native mixed rooms, guest, restrictive-network TURN, Bluetooth/audio-route change, camera switch, background/foreground/lock, multiple devices on one account, and induced AI failure |
| trustworthy transcript | representative audio corpus; WER/domain/numeric/code-switching; p50/p95 final latency; out-of-order completion; speaker/consent/capture mapping; correction and gap accounting |
| best-in-class composer dictation | shared mic experience across all composers; existing input transforms in place; real waveform; explicit X/Delete, Stop, and Send arrow; literal `Transcribing` progress state; final-only text post after Send; Delete/error/empty safeguards; company-term fidelity; exactly-once retry; personal Realtime hangup before Dictate/room join; private in-room dictation; physical web/iPhone/iPad proof |
| five/30-minute recall | exact source-time slices, raw fallback during analysis lag, evidence links, coverage/high-waters, DST and topic-relative cases |
| late-join magic | first-admission exact interval; concise spoken recap plus decisions/commitments/blockers/topic/questions/artifacts; source chips |
| `#team` compounds | exact Erick-link retrieval; canonical author/reply/reaction context; catch-up/deposit rail; author/time/channel filtering; edit/delete/retract; long-thread performance; locked-device push |
| Scout feels like a coworker | stable core/personality and verified identity; multi-person conversational context; lexical mentions; in-character text/GIF judgment; visible memory sources/correction; no uninvoked replies, persona drift, impersonation, or sensitive inference |
| rich `#team` actions | exact-revision Files search/drop; source and destination ACL intersection; no silent sharing; revocation/open reauthorization; restrained one-GIF responses; G-rated/provider/privacy/alt/provenance controls; desktop/mobile parity |
| learns how people work | explicit and inferred low-risk preferences, sources/confidence/expiry, correction/forget, no sensitive inference/private leakage |
| work builds a portable professional identity | every visible Work Record card resolves to an active signed attestation and exact released-field manifest; verification tier and unknowns are explicit; source/ACL/delete/purge drift retracts stale cards; person can unpublish; org can correct/revoke; departure preserves signatures but not source access; zero mind/confidential/hidden-membership leakage or productivity scoring |
| future coworkers are discoverable from real work | opt-in signed-in network only; natural-language query becomes visible policy-checked filters over published projection; evidence-backed why/unknown explanations; no protected-trait/proxy/personality/fit ranking; pause/block/revoke/delete fence search/contact and purge indexes; no bulk export, automated outreach, public feed, or employer availability dashboard |
| Scout participant | surface invocation matrix, visible engagement, natural follow-up/barge-in, low false response, meeting-chat cards, audience-safe speech |
| Scout can bring in a specialist | member-only explicit invitation and disclosure; server-built transcript/analysis/brain brief; distinct verified agent roster/audio; one-agent floor lease; human interruption; natural dismissal and terminal usage; zero acoustic loops, unauthorized context, guest enablement, or human-media impact on failure |
| people can hire and grow an agent team | curated outcome-first Marketplace; verified package/listing provenance; idempotent trial/hire/direct-thread/assignment/update/pause/offboard; organization-owned identities and local overlays; distinct personalities with continuity; evidence-backed relationship/domain/competency learning; inspect/correct/forget/purge; explicit permission/cost diffs; no package code/credential inheritance, seller access, silent update, memory export, or self-granted capability |
| Scout acts like a chief of staff | typed roster/status/budget/performance context; deterministic eligible-agent filtering; explainable recommendations; human override; bounded assignment/handoff; blocker/escalation truth; Scout drafts but cannot confirm hire/access/update/offboard changes |
| proactive but controlled work | labeled detector corpus, counterevidence, relevant recipients, revisioned approval, no silent launch, project destination tests |
| durable coworker workforce | stable named identity and runtime provenance; explicit Scout handoff; capability token/tool/hop/time/cost boundaries; no social-mention launches or agent loops; crash/restart/idempotency; status truth; artifact lineage; external-write gates; completion against original criteria |
| I&O quality | first named Insights coworker plus Scout coordination; ten fixed-release pilots/two reviewers; ≥8 accepted in ≤2 revisions; all asserted claims sourced; zero unauthorized/invented claims |
| intelligent routing | per-seat corpora, exact model/effort/prompt versions, quality/latency/cost verdicts, one-variable canaries, rollback receipts |
| 24/7 trust | canonical/consent/freshness health, spend/queue/backup alerts, encrypted offsite restore, purge continuity, HA failover, exact-SHA attestation |

Every evidence packet binds release commit, tree, image digest, config names without secret values, corpus/input digests, model/provider/effort, prompt/schema/workflow versions, timestamps, operator/reviewer, and pass/fail verdict. Synthetic provider observations cannot qualify a live gate.

---

## 16. Operations and authority queue

The current Goal Loop scope authorizes local E1-E9 implementation and
deterministic verification. On 2026-08-01 the user additionally authorized
bounded use of the existing OpenAI project-scoped key after confirming quota,
including one explicit retry after the first Scout attempt. Every recorded
provider-attempt authorization has now been consumed; no further paid provider
call is authorized by that history. The user subsequently authorized the
reviewed additive migrations, intentional Git integration and push to
`axx/main`, exact VPS release, and iOS Build 29/TestFlight submission once the
fresh local and release preflights pass. This is shipping authority for the
reviewed candidate, not authority to enable default-off/unqualified routes or
to bypass a failed provider, consent, corpus, migration, release, or owner gate.
The following still require external evidence, credentials, business approval,
or a qualifying earlier-wave receipt:

- owner/billing-console reconciliation of the intended OpenAI project ID and spend controls before any route can become `provider_qualified` or production-enabled; the successful `sk-proj` contract receipt proves usable quota for that credential but not the missing ownership reconciliation;
- business/legal approval of guest, meeting capture, model-analysis, organization-memory, raw-audio retention, and dictation disclosure copy;
- business/legal/product approval of named non-human meeting participants, per-invitation transcript/analysis/company-context sharing and retention, synthesized-voice disclosure, member-only first-release policy, eligible confirmer, natural spoken dismissal, and maximum session/turn/cost budgets;
- before E8 activation, business/product/security approval of internal Agent Marketplace curation, hiring/admin roles, trial policy, first-party package authors/publishers, per-agent budgets, direct/channel membership defaults, personality/local-overlay controls, relationship/domain/competency learning semantics, memory inspection/correction/forget/export/purge, update approval, pause/quarantine/offboarding, and the initial Insights/Marketing/Research/Design/Builder listings;
- a future public seller marketplace requires a separate Strategic Design and authority queue for publisher identity/signing, package review and revocation, licensing/IP, payments/royalties/refunds/tax, rankings/reviews, abuse/moderation, vulnerability response, support, privacy/telemetry, cross-organization reputation, regional/model-provider policy, and legal terms; none is an E0–E10 dependency or hidden feature;
- business/legal/product approval of the GIF provider's terms, content/privacy policy, channel default, and the initial human-authored Scout/Insights coworker profiles;
- before Work Record/network qualification, product/legal/privacy approval of contribution attribution and export policy, attestation language and verification tiers, named-party/customer/outcome disclosure, correction/dispute/revocation, recruiter capability and limits, prohibited criteria/proxies, search/contact copy, retention/deletion/index purge, and the opt-in pilot cohort; the pilot needs at least five consenting profiles and two realistic recruiter/search reviewers;
- an AWS account/operator credential for the selected independent backup boundary: an S3 general-purpose bucket with Versioning plus Object Lock in Compliance mode, a default retention period, SSE-KMS under a customer-managed key, CloudTrail evidence, and separate least-privilege upload, restore, retention-administration, and key-custody roles; DigitalOcean Spaces is not an acceptable substitute because the observed Object Lock API is unavailable;
- approval of the minimum recurring AWS S3/KMS retention cost, plus later managed PostgreSQL, redundant compute/TURN, cross-region replication, and independent signing costs;
- independent key/custody owners and separate restore-host access;
- Apple signing team, privacy manifest decisions, TestFlight/device availability;
- any future repository/GitHub/application rename to STRIDE.

---

## 17. Risks and controlled decisions

| Risk | Control |
|---|---|
| “learning personality” feels creepy | collaboration preferences only; evidence/confidence/expiry; visible inspect/correct/forget; deny sensitive inferences |
| Work Record becomes employer surveillance or a productivity score | claims describe bounded contribution to exact work; no activity-volume, responsiveness, meeting, message, token, “fuel/share,” influence, leaderboards, employee comparisons, or hidden performance rankings; candidate detection stays private until reviewed |
| portable proof leaks organization or collaborator secrets | opaque signed receipt by default; exact field-release manifest; org, subject, and named-party approval; coarsen/redact customers, dates, metrics, and outcomes; source drift retracts projection |
| recruiter AI recreates discriminatory “culture fit” ranking | signed-in revocable talent-search capability; prohibited-criteria/proxy policy before retrieval; only person-published work modes; transparent filters and why/unknown; no protected/sensitive/psychographic/compensation inference or quality score; audited, rate-limited, no bulk export |
| agents take credit from humans or expose AgentMind | agent-influence claim requires exact run/output plus human adoption/review and outcome; agent self-report/usage volume grants nothing; public receipt contains no prompt, private memory, model judgment, or AI-dependence score |
| personality drifts, becomes cringey, or manipulates | human-authored versioned core profile; bounded humor; channel norms; frozen social evals; learned memory cannot rewrite identity/policy; one-click correction/disable |
| “learning and growing” becomes silent self-modification | separate package/core/overlay/capability/assignment/memory/performance layers; source-linked lessons; reviewed performance receipts; human-approved capability changes; model self-assessment grants nothing |
| a marketplace listing overclaims competence | listing capabilities resolve to implemented tool/workflow roles and fixed eval receipts; samples are labeled; unavailable variants remain disabled; curator verdict and revocation; personality/popularity never substitute for evidence |
| a package update hijacks a trusted coworker | immutable version/digest/provenance; semantic personality/permission/runtime/cost diff; quarantine and continuity/security evals; opt-in activation; local overlay/memory preserved; one-pointer rollback |
| publisher/package exfiltrates company data or credentials | no package commands/hooks/env/raw MCP; abstract capability requests only; tenant-local runtime identity; isolated workers/egress; runtime ACL context; zero seller telemetry/export by default |
| hiring an agent adds it everywhere | Hire creates roster/direct thread only; channel/project/meeting membership and data/tool access are separate explicit grants; every run reauthorizes current assignment and destination |
| Scout becomes an unaccountable manager | Scout has typed read/draft/coordinate tools; deterministic policy owns quarantine; humans confirm hire/access/update/offboard; every assignment and exception is attributable and reversible |
| offboarded agent keeps acting or disappears from history | generation/token/member revocation first; active work fenced; meeting eligibility removed; historical authorship/artifacts preserved; policy-bound memory export/purge and replay proof |
| too many named agents make `#team` noisy or confusing | Scout is the sole default social coworker; specialists require explicit membership or a bound approved work thread; system workers stay non-social |
| two live voice agents talk over humans, trigger each other, or feel deceptive | Scout remains chair; one visible specialist maximum; eligible-human confirmation and persistent agent identity; server floor lease; human-priority barge-in; no synthesized-audio feedback; no autonomous agent turns; natural and button dismissal |
| specialist invitation leaks meeting or company context | member-only first release; separate non-human-participation consent; invitation shows context classes; server-built audience-intersection envelope; no raw-audio history or unrestricted retrieval; reauthorize every tool and terminate on audience/consent change |
| specialist sessions make meetings unpredictably expensive | no provider session before confirmation; hard join/idle/duration/turn/audio/token/cost caps; one specialist per sitting; terminal usage reconciliation; separate route/profile kill switches |
| stable agent name hides a materially changed model | identity remains stable only after continuity/safety eval; every response/run receipt binds profile and runtime/model revision; rollback profile route independently |
| `#team` memory breaches private chat | separate public ConversationEvent projection; raw thread kind remains UI/private; canary tests at every retrieval lane |
| Scout leaks a private file while trying to be helpful | server-minted authorized inventory; exact source revision plus destination audience intersection; no client-ref authority; explicit separate share approval; reauthorize on post/open/revoke |
| a funny GIF leaks context or lands cruelly | abstract provider query only; G-rated server filter; sensitive-context deny list; situational not targeted humor; one-result cap; channel off switch; alt/provenance/delete/report |
| live transcript conflicts with final | provisional state never durable; atomic authoritative replacement; source/evidence points to canonical revision |
| analysis costs grow with every meeting | incremental deltas, typed state, retrieval, compaction, per-seat budgets; no repeated full transcript prompts |
| Realtime becomes an authorization bypass | server context/tools only; Realtime output is conversation, never approval or durable state |
| Scout interrupts humans | explicit addressability gate, visible engagement, silence-favoring false-positive policy, proactive cards not speech |
| meeting answer leaks `#team` to a guest | audience intersection before speech/post; private fallback |
| model names/parameters drift | versioned registry, startup validation, capability-aware configuration, exact canary receipts |
| one generic runner control hides incompatible runtimes | registry declares capabilities and authority per runtime; stage planners select an eligible runner, not only a model slug |
| multiple agents increase cost/chaos | typed DelegationRun, no mention-triggered work, zero transitive hops by default, bounded independent stages, concurrency/tool/turn/time/cost caps, one synthesis return, durable STRIDE checkpoints |
| workflow executes twice | revision-bound approval, idempotency key, transactional claim/outbox, ambiguous-effect terminal state |
| new media stack creates migration risk | Pion rollback per new sitting; managed provider must beat same acceptance gate |
| healthy HTTP masks stale brain | independent capability high-waters and paging; final acceptance rejects aggregate-only health |
| local design branch overwrites live fixes | clean worktree from `axx/main`, intentional patch inventory, tests and rendered QA before merge |

---

## 18. Current wave and resume point

**Current wave:** E10-W5/W6 qualification after W4 private production
activation. E10-W0 through E10-W3 are `deterministic_verified`; W4 is
`production_private_live`. External substate `external_acceptance_waiting`.
E1-E9 remain
`deterministic_verified` only for their named local/default-off evidence classes.
The live product carrier promotes only the receipted W4 person/organization/
session, Contribution Review, Work Record, and private network-draft states. It
does not promote publication/search/contact/MyMind, provider, real-corpus,
physical-device, HA, custody, pilot, soak, or later cohort-activation states.

**Current owner and stop:** AJ authorized and completed the W4 implementation,
migration acceptance, commit, push, and exact VPS activation. The 2026-08-09
receipt proves ledger generation 37, exact live `cd9566b...`, retained rollback
`c34118f...`, authenticated activation lineage, seven current Bonfire members,
and 98 bound sessions. The canonical shadow's 119 repair candidates remain a
separate read-only finding and are not authority to repair or promote it.
Continue MyMind, publication/search/contact, and final cohorts in W5-W8 order
only with their separately named product/privacy/custody authority. No further paid
qualification retry, canonical production repair/promotion, production data or
configuration mutation, Git shipping/deployment, active specialist route,
repository rename, or cohort activation is authorized by this plan alone.
Build 49 is the current exact historical native carrier and is unexpectedly in
external `Bonfire`; its provenance review plus physical iPhone and iPad
acceptance remain open. Build 48 and older Build 39/47 notes are historical. Do
not claim provider-quality, restore, physical-device, HA, MyMind,
or multi-organization acceptance without its separate evidence.

**Historical E1-E9 frozen checkpoint — retained as evidence, not current resume
truth:**

1. The design lineage commits `889cf65`, `30996ca`, `a4789cd`, and `c7b4128` remain ancestors of local `HEAD`; `HEAD` and `axx/main` were both `c7b4128f0f45d1b6443c73cbae3e54feceb735d3` when the work resumed. No prior design work was reverted.
2. The 324-file E1-E9 implementation manifest excluding this ledger and `stride-site/` is `057290ab5f8ac1e0f279d50bede9cf14189c02f91c986cc2430de15cb392e617`. It is a local dirty-candidate content identity, **not** a Git commit, release, or deployable exact-SHA claim.
3. Full Go normal and race, Node/web, mobile/TypeScript, native/Xcode simulator, static/dependency, E9 vertical founder, local failover/restore, and focused authority/restart/tamper gates pass with the exact limitations recorded above. The independent final Critic verdict is the required final local sign-off.
4. A read-only production audit on 2026-08-01 confirmed the then-current,
unqualified migration baseline: personal/private Realtime voice uses
`gpt-realtime-2` at `high` reasoning; authoritative meeting transcription uses
`gpt-realtime-whisper`; composer dictation uses `gpt-4o-transcribe`;
`MEETING_TRANSCRIPT_LANE_ENABLED=true`; and the dedicated target Realtime
transcription override remains unset. The target remains `gpt-realtime-2.1`
for conversational voice, `gpt-transcribe` for bounded authoritative
transcription/dictation, and optional `gpt-live-transcribe` only for a justified
provisional live-transcript consumer. This audit proved that date's
configuration, not present configuration, model quality, or migration readiness.
5. `private_share`, provider GIF actions, live specialist voice, visible Marketplace admission, external worker isolation, production failover/restore, and all other unqualified lanes remain disabled or unavailable. Human chat, current production behavior, and historical ACL-correct artifacts are unchanged by this checkpoint.
6. The E10 contract candidate is separately frozen as manifest `ffbb8ad389afdced67162cdb4987d64d6406eeb7c0ef2c235f30e3885440df26` with probe binary `b7c7e984c4489e8d5113f3b83e29d3231f2b3b1a2377b198034c21718a95d699`. It includes the 328 then-current in-scope dirty candidate entries and excludes this live ledger and `stride-site/`; it remains a content identity, not a Git commit or deployable release.
7. The OpenAI access matrix and one bounded `gpt-transcribe` file contract pass with private, body-free receipts. Receipt permissions, candidate bindings, and absence of raw key/project/request IDs, audio, reference text, and transcript fields were rechecked before the sanitized evidence packet was added.
8. These observations are `provider_contract_attempt` receipts only. The audio corpora remain pending live capture, the synthetic reference was bound but not used to claim WER/quality, the project ID is unreconciled, and no result has been promoted to `provider_qualified`.
9. A later 334-file Realtime-attempt candidate was frozen as manifest `3ba0394961509c535d635e6f4931efd9c0328913e7980f5ce6424a2217bb696f` with probe binary `dc24d4beacc7afac0c33142f38983560f7b8c3dafbde1e111bfef239f11239ef`. Exactly one `gpt-transcribe` WebSocket attempt reached HTTP 101 and received `session.created` plus `session.updated`, then stopped before audio append/commit because the harness rejected the acknowledgement as a schema mismatch. Under that stopped attempt and its authorization, no Scout call, retry, fallback, transcript generation, or provider-reported usage followed.
10. Diagnosis against the current official guide and `session.updated` response schema found a harness incompatibility rather than evidence of a provider rejection: the exact outbound request correctly included `prompt`, `keywords`, `languages`, and `turn_detection: null`, but the response schema has no `keywords` property and marks the returned audio configuration fields optional. The post-attempt parser now treats `session.updated` as the documented acknowledgement, accepts documented omissions, and fails on any contradictory echoed value. Focused normal 20×, focused race 3×, full package normal/race, vet, formatting, diff, and an independent Critic all pass. At that checkpoint the correction had not been used for another provider call; it was subsequently superseded by freshly frozen candidates, and this failed manifest/binary remains stale for any future attempt.
11. The sanitized failed receipt, exact binary/candidate bindings, stop-order proof, documentation-based diagnosis, and post-attempt local validation are in `docs/evidence/e10/openai-realtime-contract-attempt-20260801.jsonl` (file SHA-256 `5b9565c97f688e3f226796c689d9f2e344bd63aa3bd89a2fb129e4bdfe6383a1`). It remains `provider_contract_attempt` evidence only; the OpenAI project/billing owner is still unreconciled.
12. A corrected 334-path candidate (`f7033a832d52f38c812b62514256870babd808c39de8ef2e8003dc6d987b1d96`, probe binary `980212a27ed80c46c1b8047802fc6104d498ca3bcbe93ac6b378142bfb7cb4ea`) passed one synthetic committed-turn Realtime `gpt-transcribe` contract. The provider item ID correlates exactly across committed, created, finalized, and completed lifecycle events; the 26-event run completed in 2.152 seconds, reported six duration seconds, and reconciled to USD 0.00045. This proves the narrow modern event/correlation/accounting contract only; it does not prove WER, domain fidelity, target-device dictation, meeting quality, or seat qualification.
13. The first Scout attempt on that candidate received `session.created` and `session.updated` but timed out because the then-frozen harness required optional `conversation.created`. No generation or valid `response.done` usage was observed; cost remains unreconciled rather than zero.
14. A new exact candidate (`886a6a80908e2ab96d1081a61a03cbf8e014b8b7d2f6b42076cf5e353fbc4d78`, probe binary `057134722e4b8381ee8caf9b699473a133c10bfb9068356f1ca791b5f594e626`) passed independent preflight, and the user authorized exactly one Scout retry. It reached 19 events including partial transcript/audio deltas and both stream terminals, then produced an incomplete assistant final item; the old parser stopped before `response.done`. The receipt's legacy zero output/token/cost fields are not observations. Usage and spend are `unreconciled_partial_generation_without_response_done`; the high-reasoning 48-token cap is the strongest diagnosis, not a provider-confirmed reason. This is not a successful Scout contract.
15. The post-retry local correction is bound to source `a582a165e6c4314ea614f327d2cb7a323148f7fef957e0ff58a0f04855fe66b7`, test `59ff1e3a14a30d13c43dd79d395a1550a1dc87ff4aad09bf7f354556cde68222`, and event schema `b5e4c614301ff09756f8adec5fbc10f4baa9ab7763df4d236ba302d52afb04c5`. It makes `conversation.created` optional, handles documented completed/incomplete item and terminal-response states, makes only completed `response.done` authoritative for success, preserves partial output on failure, strictly reconciles optional usage fields and costs, safely hashes provider failure classifications, and uses a 256-output-token cap whose conservative all-audio maximum is USD 0.018432 under the USD 0.02 admission ceiling. Focused normal 20×, focused race 3×, full package normal/race, vet, gofmt/diff, official-contract review, and independent Critic all pass. It has not been provider-retested.
16. The four new exact raw receipts, candidate/binary/fixture/reference/path bindings, fact-versus-inference diagnoses, legacy-zero correction, and local validation are in `docs/evidence/e10/openai-realtime-contract-final-20260801.jsonl` (file SHA-256 `4625652b65c39ea2673a9687cb54d66f5fa74cf20a4dccb8188008283215c69f`). The packet is JSONL-valid, preserves each raw receipt exactly plus its original-file digest, contains no raw key/project/request ID, prompt, reference text, audio, or transcript body, and records no auto-retry, fallback, Git, route, configuration, or production mutation.
17. The same read-only production audit found that all 958 tracked non-`data/`
    files under `/opt/meetingassist` match `c7b4128f0f45d1b6443c73cbae3e54feceb735d3`
    (manifest SHA-256
    `8f85575eb6565a1ec607e316ca08eb5c8d837968124914beeaf6b1cdad271938`),
    but the serving image is not release-qualified. Its actual image ID is
    `sha256:190578697b6a79edd0868e3aad9ae46dc94b76d1fc7c300422822e3fe86181bf`,
    its binary SHA-256 is
    `64166802d59c66293338200916868957fc006733e3665911c88eeee9559520c1`,
    and its stale revision label/effective release commit is
    `cbc27df1cbd360619ecbd353bd9782cd0a20b358`, while the configured image
    digest does not match the running image and the build-manifest value is
    `unqualified`. Public health exposes no release identity. The source copy
    matching main therefore does not bind the serving binary or image to main.
18. Production is traffic-serving but not company-brain-ready: canonical shadow
    is unhealthy with a persistent idempotency conflict and a 4,418-event
    reconcile gap (dirty high-water 12,950 versus reconciled 8,532), consent
    authority is unavailable, brain projection is off, brain/recap are roughly
    21.8 days stale, Scout is disconnected, and STT is stale. `/readyz` still
    returns HTTP 200 while `/capabilities` returns `ok:false`; no aggregate
    health response may be used as company-brain acceptance. The six expected
    Compose services account for all running services, the live named data
    volume is mounted correctly, and sampled host/container telemetry shows no
    resource-pressure or OOM explanation. HA/DR remains open because the only
    observed backup is local and unencrypted, offsite custody is dormant, and
    no restore has been verified. No production state was changed by the audit.
    The seven-record sanitized audit packet is
    `docs/evidence/e10/live-readonly-audit-20260801.jsonl` (SHA-256
    `009b9fc8b0b90e65d49acd2a340e08f42cd475081becc76e0d93742d7a275b80`).

**2026-08-01 final exact-release checkpoint:**

1. Implementation commit A is
   `9c5d53a9cadda68bf4c7014d4357a7312969e4e9`. Its repeat-generated staged
   manifest contains exactly 408 paths and 12,233,727 bytes with SHA-256
   `6caf49a231653bba076fb7b3382bd2b052d3f4ba674c94c92d1d6e66f672f3b6`.
   The pre-race working-content manifest matched byte-for-byte after the final
   run: 408 paths, 12,233,727 bytes, SHA-256
   `a0a50e73cd5c7da39211943a7982a4e6412b91c9ff9cb907f90f0cc530ab7b4f`.
   This ledger and the separately owned untracked `stride-site/` are excluded
   from A by construction.
2. The complete non-`stride-site` Go suite passed normally (root package
   382.522 seconds) and under `-race -timeout=90m` (root package 2,723.377
   seconds; complete run 45:24.50). Root Node passed 122/122, mobile passed
   358/358 plus TypeScript, and `go vet`, `go mod verify`, formatting/diff,
   Expo dependency alignment, both production dependency audits, the release
   pack checksum/self-check, and the narrowly justified secret scan all pass.
   The only default secret-scan findings were two occurrences of the same
   explicit synthetic test replay key; an exact-value fixture allowlist leaves
   zero findings.
3. Independent critics pass the collaboration store, personal Realtime and
   control surface, native auth/session/push authority, server push authority,
   renderer release, and final release-pack gates. The final candidate includes
   exact token-scoped native 401 fencing, recoverable secure storage,
   process-wide push binding and revocation ordering, Bearer-over-cookie native
   authority, pre/post-lease Realtime tool authorization, a real runtime
   HTML-to-PDF-to-JPEG renderer canary, and recursive rejection of nested body
   or credential material in structured references.
4. Reviewed migrations `0008_stride_contracts.sql` and
   `0009_stride_conversation_ledger.sql` are included in A. They remain subject
   to the exact bootstrap backup, rehearsal, canonical parity, consent,
   migration, rollback, and public-reopen gates; inclusion is not evidence that
   they have run in production.
5. The iOS release candidate is Stride 1.0.0 Build 29 and the repository pins
   the qualified exact EAS CLI `21.4.0`. Read-only EAS, signing, App Store
   Connect, unused-build-number, internal-group, and external-group-baseline
   preflights pass. No Build 29 artifact, submission, Apple processing, group
   availability, or physical-device result is claimed at this checkpoint.
6. Read-only VPS preflight confirms the reviewed six-service legacy topology,
   eight named volumes, seven-migration baseline, live named data volume,
   available host capacity, and absence of an earlier exact-release ledger or
   guard. It also reconfirms that canonical parity, consent, exact release
   identity, nine-room quiet proof, restore evidence, and public reopen must be
   established by the guarded bootstrap; no deployment success is claimed here.
7. Provider-seat qualification, Scout and specialist voice, real-corpus STT and
   dictation quality, WebRTC/TURN device acceptance, physical TestFlight
   acceptance, external custody/HA restore, and the 24-hour/ten-sitting soak
   remain separate external gates. This checkpoint does not promote or enable
   any default-off provider route or feature.
8. The first exact VPS `init-build` for A stopped before maintenance because
   Chrome Headless Shell 150's checksum-pinned archive no longer contains the
   obsolete `chrome_sandbox` helper that `Dockerfile.render` tried to chmod.
   No application service, container, database, volume, migration, firewall,
   route, or public traffic changed. Node was installed at the exact reviewed
   Ubuntu package version and the fail-closed build output was retained for
   diagnosis.
9. Superseding implementation commit
   `d8ab3246edbdb53891293ef242039706e209e8b0` removes only that obsolete,
   already-disabled helper dependency, requires the real headless-shell binary
   to be executable, and adds a regression assertion forbidding the old path.
   The active boundary remains non-root namespace sandboxing with no
   `--no-sandbox`, capability drop, no-new-privileges, read-only filesystem,
   internal-only network, queue-only mount, and fail-closed runtime canary.
   Focused normal/race tests, the full root package (380.089 seconds), Node
   122/122, `go vet`, pack self-check/checksums, diff and secret scans, and an
   independent security critic all pass. This document is the sole intended
   change in the direct checkpoint child used for the retried exact release.
10. iOS Build 29 was built from clean exact SHA
    `97ff340097253ff3ad98481226f6159c3ce206ae`, before the server-only renderer
    correction. EAS build `7857b9b2-2de8-4248-b1c6-a50c54f6ca97` is FINISHED;
    submission `e631f914-4e10-405d-a686-2fb1509b0651` completed; Apple build
    `6a870589-8448-44fb-99d6-cea2f5a9ebb4` is VALID and non-expired; internal
    `Team (Expo)` contains Build 29; external `Bonfire` excludes it and retains
    its exact prior build baseline. The only later implementation changes are
    `Dockerfile.render` and its Go regression test, and this direct child adds
    only this plan, so the mobile source/config tree is byte-identical to the
    TestFlight artifact's commit. This proves build, Apple processing, and
    internal-group availability, not physical-device acceptance.
11. Final implementation release boundary A is
    `59fd7c9be356489db3979977deef1f3841150164`. It contains the complete
    reviewed implementation lineage through renderer correction
    `d8ab3246edbdb53891293ef242039706e209e8b0` and intentionally removes this
    plan from A's tree so the direct checkpoint child can add it as its exact
    sole diff, as required by the one-time bootstrap contract. This boundary
    changes no release-owned source, configuration, migration, image input, or
    mobile input relative to the corrected implementation; it exists only to
    preserve the machine-verifiable A/B ceremony without rewriting history.
12. The first manual A cutover then reached the protected maintenance window,
    completed a cold eight-volume backup and restore rehearsal, applied the
    reviewed migrations, and stopped fail-closed before public reopen when the
    pinned Chrome 150 namespace sandbox could not start under Ubuntu 24.04's
    restricted-user-namespace AppArmor policy plus Docker's default seccomp
    profile. The rehearsed cold rollback restored the exact legacy volumes,
    migrations, images, service inventory, and public health before traffic was
    reopened. The failed ceremony's state, pack, build evidence, and backup
    remain quarantined; its A/B pair is retired and must not be resumed.
13. The renderer correction preserves Chrome's namespace sandbox instead of
    adding `--no-sandbox`, `seccomp=unconfined`, `SYS_ADMIN`, or `SYS_CHROOT`.
    It adds one release-bound AppArmor ABI 4 profile derived from Docker's
    default container policy with the required mediated `userns` permission,
    while leaving `kernel.apparmor_restrict_unprivileged_userns=1`. Its seccomp
    input is the exact Moby `profiles/seccomp/v0.2.3` default plus only five
    amd64 rules observed from the pinned browser: three exact `clone` flag
    values, exact `unshare(CLONE_NEWUSER)`, and `chroot`. `clone3` remains
    `ENOSYS`; `setns` and broader mount, network, UTS, IPC, PID, and cgroup
    namespace combinations remain denied. An exact VPS canary produced both
    PDF and JPEG output under UID/GID 65532, zero capabilities, read-only root,
    no-new-privileges, AppArmor enforce mode, and seccomp filter mode; negative
    namespace and outer-`chroot` probes also passed.
14. The replacement genesis operator pack now installs and byte-attests both
    profiles only after dual IPv4/IPv6 ingress isolation, reruns the sandboxed
    renderer canary before any legacy deletion, and verifies the expanded live
    Docker seccomp JSON plus process capabilities, identity, filesystem,
    network, NNP, and seccomp state before acceptance. Every forward phase is
    terminally forbidden after any public-open attempt or legacy rollback, and
    destructive phases re-prove both live ingress guards, the exact loopback
    marker, external block acknowledgement, and enforcing profiles. Profile
    cleanup is interruption-idempotent and refuses drift or any running or
    restartable container user. The complete non-`stride-site` Go suite passed
    again (root 401.317 seconds), focused race and `go vet` passed, Node passed
    104/104, mobile remained 358/358 plus TypeScript, both production audits
    remained clean, the pack self-check/checksums and diff secret scan passed,
    and the independent release critic's two rollback findings were corrected.
    A fresh implementation/checkpoint pair, exact pack preparation, guarded VPS
    ceremony, public rendered QA, and physical-device acceptance remain the
    next evidence boundaries; the failed pair cannot be relabeled or reused.
15. Replacement pair `c1872c72100c7c4b42b1dec5c2f97573f76d1b38` /
    `d91adc85c29b8b73fdf4a291d5782e390e2cecb2` passed exact local preparation,
    build/preflight, dual external isolation, stable all-room emptiness, a new
    eight-volume cold backup and full restore rehearsal, the confined
    PDF/JPEG renderer canary, legacy retirement, and migration application
    through version 9. Manual A then exposed two further release-gate defects
    while traffic remained blocked: synchronous first-start migration and
    canonical reconstruction took roughly 161 seconds, so Docker's 20-second
    start period plus three 30-second failures marked the still-working process
    unhealthy before the separately configured 300-second Compose wait could
    help; after the process became healthy, `release_data_gate` looked for SQL
    files under `sealed-candidate`, which intentionally contains only the
    release configuration allowlist. The ceremony did not bypass either gate.
    It used the rehearsed cold restore, returned PostgreSQL to exact migrations
    1-7 and every prior table/volume/image, removed the candidate profiles, and
    independently proved restored public HTTPS/readiness and TCP TURN. This
    pair is also retired from genesis use even though it remains an ancestor on
    `main`.
16. The next correction makes the application health start period an exact
    release-validated 300 seconds while retaining the existing post-start
    three-by-30-second failure budget. The migration gate now proves
    `source.tar` against both source and release receipts, rejects every unsafe
    archive path, requires the exact regular `migrations/0001`-through-`0009`
    inventory with no missing, duplicate, extra, symlink, or non-regular
    member, streams each member directly to SHA-256 without extraction, and
    compares one ordered nine-row PostgreSQL hash snapshot. Its executable
    fixture proves success with no sealed-candidate migration directory plus
    wrong-hash, missing, extra, symlink, and archive-tamper rejection. Operator
    self-check/checksums, Node 104/104, focused Go normal/race, the complete
    non-`stride-site` Go suite again (root 376.552 seconds), `go vet`,
    diff/secret scan, actual VPS Compose `5m0s` normalization, and an
    independent release re-review all pass. Production remains on the restored
    healthy legacy release until the third exact pair passes the full ceremony.
17. Third exact pair A3/B3 is
    `64881c0d4496f936ae1a358c5550a0ea1207fe5a` /
    `8e1b16c3531a93c93ea79914c80fe9e41573f645`. Its protected ceremony
    completed fresh dual-ingress isolation, all-room quiescence, a new
    eight-volume cold backup and full restore rehearsal, the confined
    PDF/JPEG renderer canary, and candidate migration application through
    version 9. The application then stopped at the canonical parity gate
    before public reopen. The first isolated clone observation reported 24
    candidates; after the two deterministic clone starts had applied the
    ordinary current-board revisions, the same source/canonical boundary
    reduced reproducibly to exactly seven historical board candidates. No
    gate was bypassed. The rehearsed cold rollback restored the complete
    legacy volume/image/migration set, candidate profiles were removed, and
    public HTTPS, readiness, and TCP TURN were independently rechecked before
    legacy traffic reopened. Production is therefore healthy legacy, not an
    A3/B3 release.
18. The seven-card evidence is deliberately narrow and sanitized. A frozen
    32-card historical board and the current 25-card board have distinct
    sealed hashes; the canonical store retains all seven as active historical
    imports, while the current board omits them. Five are prior legacy repair
    candidates: three have an unchanged historical Backlog state, while two
    have a later archived Done state and therefore cannot inherit one uniform
    source-state assertion. The remaining two are consistently observed as
    archived Done. There is no board deletion journal, deletion actor, reason,
    or original deletion timestamp in the captured live, clone, or retained
    backup evidence. A repair may record only a last-positive observation, the
    current absence observation, and its own append time; it must not invent a
    historical deletion fact.
19. The production append remains blocked on one explicit, manifest-bound
    human confirmation after a clean clone receipt. The private manifest must
    bind the sealed source and archive hashes, seven opaque candidate
    fingerprints, selected source state per candidate, target pre-state,
    backup and release identities, clone receipt, controlled reconciliation
    reason, and exact no-extra delta. From a pristine 32-event target, the
    acceptance rule is exactly two ordinary current-board revisions plus seven
    controlled absence-backfill tombstones (nine events and nine outbox rows),
    unchanged 25-card visible board, then zero delta on a second reconcile,
    restart, and fresh clone repeat. If normalization has already produced the
    two ordinary revisions, the alternative precondition is exactly 34 plus
    seven, never a range. Broad release/migration authority does not substitute
    for this data-history decision. Build 29/TestFlight evidence remains exactly
    as recorded above; no replacement mobile build is implied by this
    server-side blocked repair.

**Resume gates — do not skip or merge them:**

- **Authoritative meeting STT and composer dictation:** the synthetic provider
  contract is passed. Token-free evaluator/receipt machinery for the full gate
  is still being completed. Qualification then requires the preregistered
  consented corpus of at least 60 minutes and 120 clips, plus at least 250
  target web/iPhone/iPad dictations and every quality, timing, ordering,
  consent, exactly-once, microphone-generation, privacy, animation, and device
  target in §13.1; no synthetic fixture can substitute for those inputs.
- **Personal/meeting Scout:** the provider contract is failed and only the local correction passes. Any next attempt requires a fresh exact candidate freeze, independent preflight, a new explicit paid-call authorization, and continued project/cost truth. A pass must precede any specialist-voice call.
- **Luna/Terra/Sol Responses seats:** model-object access is observed only. Paid Responses probes remain hard-stopped while canonical raw `OPENAI_PROJECT_ID` and intended-project/billing reconciliation are unavailable; the project-scoped-key fallback is deliberately forbidden. After reconciliation, change one seat, effort, prompt, or tool-policy variable at a time.
- **Invited-specialist voice:** remains stopped until Scout passes, a real server-side specialist provider adapter exists, the named-agent and context-sharing approvals are current, and the invite/floor/barge-in/teardown/usage corpus is ready. The production join boundary is default-off and additionally requires a trusted external qualification authority whose current `Qualified:true` result is bound to the exact tenant, result/target, release commit, tree/image/config/candidate-route digests, provider/model/provider route/accounting mode, and specialist profile/capability. The local structure-only `QualificationEvidenceStore` is not that trust root; its concrete external adapter, evidence custody, activation configuration, and route enablement remain separate E10 work and are not supplied by deterministic application wiring.
- **I&O and workforce:** remain stopped until canonical Responses receipts, authorized real inputs, ten immutable I&O pilots, two eligible reviewers, independent criticism, durable evidence, and the complete founder/workforce acceptance flows exist.
- **`gpt-live-transcribe`:** access only; do not generate merely for completeness. A shipped provisional live-caption or early-address consumer, separate corpus, privacy contract, cost accounting, and reconciliation to authoritative final transcript must first justify the lane.
- **Embeddings and images:** access only; no generation is justified without a defined consumer, bounded acceptance target, accounting, retention, and output-review path.
- **External launch:** independent custody/consent/approvals, physical devices/TestFlight, real WebRTC/TURN and HA/restore, final immutable route map, founder flows, exact release identity, production observation, rollback, and the 24-hour/ten-sitting soak remain open. Git integration, push, deployment, route/config mutation, and feature activation each still require separate authority.

The accepted checkpoint states are `not_started`, `implemented`, `deterministic_verified`, `external_waiting`, `provider_qualified`, and `production_enabled`; no local, synthetic, access-only, or contract-only receipt may be relabeled as quality, provider-seat, physical-device, production, release, or launch acceptance.
