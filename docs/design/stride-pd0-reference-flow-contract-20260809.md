# STRIDE PD0 interactive reference-flow contract

**Status:** `decision_complete_reference_contract_only`

**Date:** 2026-08-09

**Platforms:** desktop web, native iPhone, native iPad
**Authority:** safe local design only; no product implementation, collection,
provider, Git, release, activation, publication, or external acceptance

This contract turns the PD0 thesis, primary-source research, and parity audit
into one exact set of interactive reference artifacts. It does not claim those
artifacts are rendered or accepted yet.

## 1. Product loop and reference-data boundary

Every reference flow demonstrates the same STRIDE loop:

`Signal or conversation -> governed action -> artifact/outcome -> reviewed proof -> person-controlled Work Record -> deliberately released Work Object -> relevant collaboration`

Reference data is deterministic, fictional, body-safe, and labeled. It contains
no copied production message, transcript, person, organization, artifact,
session, source digest, consent, or authority. Server-owned states are simulated
only through a closed fixture contract; a prototype cannot mint authority or
serve as backend acceptance.

## 2. Shared shell and navigation contract

The semantic destinations are exactly **Home**, **Work**, **Network**, **Work
Search**, and **You**. Organization context, notifications, global command,
account, and current authority are shell utilities. A person never has to infer
that Work Search is hidden inside Explore or that public work is hidden inside
private team tools.

| Expression | Composition | Required interaction |
|---|---|---|
| Desktop web | persistent global rail; resizable contextual sidebar; primary canvas; optional inspector | keyboard-complete traversal, visible focus, hover disclosure, deep links, drag/drop, multi-pane resize, state preserved at 1024/1280/1440/1728 CSS px |
| Native iPhone | voice-first Home/Signal; thumb-reachable Deck for the five destinations; one primary task per screen; native sheets/disclosures | touch/VoiceOver/Dynamic Type; interruption-safe draft; background/resume; portrait-first, landscape media; no gesture-only action |
| Native iPad | adaptive sidebar plus list/canvas plus optional inspector; progressive three-pane -> two-pane -> stack collapse | portrait/landscape, 1/3-1/2-2/3 Split View, Stage Manager/freeform resize, external keyboard/focus, pointer/hover/context menu, drag/drop, share/files, state-preserving pane collapse |

Responsive web on iPad and a stretched iPhone stack are explicit failure
states, not fallbacks.

## 3. Reference fixture

One fixture follows a fictional signed-in human, **Alex**, in a fictional
organization, **Northstar**, through private and public work. It contains:

- one current human session and Northstar membership;
- one private conversation and meeting with exact consent/ACL labels;
- one Suggested Work proposal with editable scope and a dismiss branch;
- one approved run, one requested intervention, one artifact revision, one
  human review, one verification receipt, and one corrected outcome;
- one private Work Record candidate and one named-party approval boundary;
- one Public Workspace draft, one reviewed `EvidenceNote`, and one later
  `ArtifactRelease`;
- one visibly disclosed sponsored agent with no public authorship authority;
- one current published person profile, one purpose-bound Work Search result,
  one contact request, one block, and one withdrawn/revoked revision; and
- explicit unknowns, offline/retry, stale authority, moderation quarantine,
  appeal, purge, and restored states.

The fixture never includes a score, engagement count, follower count, raw
private body, private source commitment, hidden person ranking, or agent vote.

## 4. Required end-to-end reference journeys

Every journey is rendered and interacted with on all three expressions. A
different composition is expected; semantic outcome and authority language are
the same.

The following composition grammar is mandatory wherever a journey does not
repeat it verbatim. Desktop uses a resizable workbench with persistent global
navigation, primary canvas, and an optional contextual inspector; keyboard,
focus, hover, deep links, and drag/drop are first-class. iPhone uses a compact
navigation stack with bottom-reach primary actions, native sheets, safe
interruption/resume, and one principal task at a time. iPad uses an adaptive
sidebar plus one-, two-, or three-column composition chosen from available
space—not device name—with popovers or inspectors for secondary context,
external-keyboard/pointer support, and continuity across rotation, Split View,
and Stage Manager. A compact iPad width may collapse columns but may not fall
back to the web app or lose capability.

### J1 — Join, authenticate, and enter Bonfire/Northstar

- **Outcome:** create/sign in with passkey, see current organization, enter Home,
  and recover from expired/revoked auth without losing an authorized draft.
- **Desktop:** centered auth -> organization chooser -> workbench.
- **iPhone:** native passkey sheet -> compact chooser -> Signal Home.
- **iPad:** correctly anchored passkey/popover -> sidebar shell in portrait,
  landscape, and Split View.
- **Hard states:** zero organizations, 3-of-3 capacity, final-owner conflict,
  wrong account, expired link, offline, session revoked during transition.

### J2 — Speak into the Signal and review Suggested Work

- **Outcome:** begin a voice or authorized chat source, see listening/thinking/
  speaking state, inspect the generated work proposal, edit scope, approve or
  dismiss, and understand what will run.
- **Desktop:** Home canvas plus proposal inspector and keyboard approval.
- **iPhone:** Signal -> proposal sheet -> explicit edit/approve/dismiss.
- **iPad:** compact Signal control beside persistent proposal/work context.
- **Hard states:** denied microphone, provider unavailable, interruption,
  source authority changed, proposal stale, reduced motion, captions/alternative.

### J3 — Continue private team work

- **Outcome:** move from conversation to meeting, Board, Drive, and artifact
  without losing audience, source, or current work context.
- **Desktop:** chat/meeting canvas with contextual Board/Drive/inspector.
- **iPhone:** thread/room stack with native attachment and work sheets.
- **iPad:** channel/list, primary work canvas, optional artifact inspector;
  pointer/keyboard and cross-region file drag/drop.
- **Hard states:** offline draft, interrupted upload, revoked share, reconnect,
  changed organization, unavailable private source.

### J4 — Run, intervene, review, and verify

- **Outcome:** observe run state, answer a bounded intervention, inspect artifact
  revisions, request changes, and distinguish human review from verification.
- **Trust:** show current actor, accountable human, source class, revision,
  audience, approval, verification, unknown, and recovery state.
- **Desktop:** run timeline and artifact canvas remain visible while the
  intervention/review inspector opens; keyboard actions never bypass the
  bounded intervention schema.
- **iPhone:** run status remains resumable beneath a focused intervention or
  revision sheet; background/foreground recovery returns to the exact pending
  operation rather than replaying it.
- **iPad:** run timeline, artifact revision, and contextual review inspector
  share the canvas when width permits; compact widths preserve the same task as
  a drill-in stack with pointer and keyboard continuity.
- **Hard states:** lost response after effect, ambiguous recovery, cancelled run,
  failed provider, stale artifact, verification partial/unknown, correction.

### J5 — Build and control the Work Record

- **Outcome:** review all six sections, inspect exact evidence, approve/dispute,
  correct/revoke, export, delete, and keep Open to visibly off unless chosen.
- **Desktop/iPad:** evidence list plus provenance/diff inspector.
- **iPhone:** readable evidence stack with progressive disclosure and no clipped
  authority labels under Dynamic Type.
- **Hard states:** named-party denied/withdrawn, organization approval stale,
  source revalidation required, purge in progress/failed/completed.

### J6 — Create and review a Public Workspace

- **Outcome:** create a private draft projection, set purpose/types/retention/
  participation, review exact released fields, obtain approvals, preview View
  As, publish, pause, archive, or withdraw.
- **Authority:** current human owner/moderator; named-party/field controllers;
  moderation parent required. Public never grants private membership.
- **Desktop:** workspace outline and released-field preview sit beside a
  provenance/moderation inspector; publish remains a distinct reviewed action.
- **iPhone:** purpose, participation, retention, View As, and approval are
  sequential native steps with a final release summary and resumable draft.
- **iPad:** workspace structure, preview, and approval context use adaptive
  columns; popovers are used for bounded choices and full sheets for release.
- **Hard states:** final owner loss, stale moderator, private-label shortcut,
  missing moderation policy, source drift, purge backlog.

### J7 — Publish a typed Public Work Object

- **Outcome:** choose one of the nine closed types, bind workspace/audience/
  provenance, review exact revision, publish, correct/supersede/withdraw, and
  inspect the public-presence dashboard.
- **Composition:** desktop/iPad rich authoring plus inspector; iPhone focused
  form steps and review sheet; all preserve one semantic state machine.
- **Hard states:** unknown type, engagement-only body, client target, named-party
  withdrawal, agent without delegation, restart/restore stale resurrection.

### J8 — Browse Workstream and enter a Public Workspace

- **Outcome:** choose Following or Workspaces, use strict chronology, understand
  Why this appeared/What is unknown, Observe/Save privately, mute/block, enter
  a workspace, ask a typed question, or offer help.
- **Desktop:** optional purpose-labeled columns, never default engagement grid.
- **iPhone:** one stream with persistent user-chosen mode.
- **iPad:** stream plus workspace canvas/inspector at regular widths.
- **Hard states:** moderation off, object revoked, observer blocked, restored
  stale index, no agent/human signal collapse, zero public counts.

### J9 — Inspect four profile types and provenance

- **Outcome:** inspect person, organization, Public Workspace, and disclosed
  agent projections; expand one shared provenance strip; use View As; see exact
  verification limits and correction/revocation history.
- **Agent rule:** label, sponsor, package/runtime/delegation and accountable
  human remain visible; no human rights, vote, social edge, or hidden memory.
- **Desktop:** profile summary, governed work evidence, and provenance/history
  inspector can be compared without losing the selected object.
- **iPhone:** the summary leads, with evidence and provenance progressively
  disclosed in native drill-ins that preserve View As and back-stack context.
- **iPad:** master profile navigation and a rich profile/evidence canvas use an
  optional provenance inspector; rotation and multitasking preserve selection.
- **Hard states:** sponsor/package loss, hidden private field, stale approval,
  verification unknown, blocked viewer, public pause.

### J10 — Use Work Search and contact safely

- **Outcome:** enter a people-finding intent through Work Search—not Explore—see
  visible interpretation, confirm it, inspect why/unknown, and send or decide a
  purpose-bound request without channel disclosure before acceptance.
- **Hard states:** prohibited/proxy query, grant expired, rate limit, exact
  session changed, publication withdrawn, block, contact declined/expired.
- **Gate:** reference only until W6 real cohort/legal/privacy qualification and
  activation; the prototype is not a search provider or result authority.
- **Desktop:** intent and visible interpretation remain above the bounded
  result canvas; confirmation and contact open in the contextual inspector.
- **iPhone:** intent -> interpretation -> confirmation -> result -> contact is
  a clear stack with exact-session resume and no result preview before confirm.
- **iPad:** query context, bounded results, and selected result/contact can use
  two or three columns without exposing additional fields or identities.

### J11 — Understand agent attribution and control

- **Outcome:** view a safe agent profile, inspect sponsor/runtime/delegation,
  invite or pause in private work, and see that PN2 has no agent-authored public
  content. A PN4 reference permits one explicit human invocation in one thread.
- **Hard states:** autonomous post, agent-agent reply, vote/react/follow/contact,
  expired delegation, budget exhausted, sponsor fleet, quarantine/offboard.
- **Desktop:** agent profile and delegation history sit beside the exact private
  thread control; sponsor and accountable human never scroll out of context.
- **iPhone:** attribution precedes the invite/pause control in one focused
  flow, with a native confirmation sheet for every state-changing action.
- **iPad:** agent evidence, delegation, and the selected private thread may be
  inspected simultaneously, while the action remains thread- and sponsor-bound.

### J12 — Settings, privacy, export, deletion, and public presence

- **Outcome:** inspect current account/organization, notification permissions,
  MyMind/default-off state, public-presence inventory, consent/retention, export,
  correction, deletion, and one-action public pause.
- **Hard states:** W5 uninstalled, export expired/revoked, deletion recovery,
  legal hold limited to governed record, restored backup with purge generation.
- **Desktop:** settings navigation, selected policy/control, and explanatory
  audit context use a stable three-region hierarchy; destructive actions do not
  share placement with routine preferences.
- **iPhone:** grouped native settings use progressive disclosure, explicit
  destructive confirmations, background-safe export, and a persistent public-
  presence pause control.
- **iPad:** settings sidebar and detail canvas remain visible at regular widths;
  export/share uses native files/share affordances and deletion uses a sheet.

### J13 — Moderation, consensus, failure, and revocation

- **Outcome:** report content, see quarantine/notice, receive human decision,
  appeal with a separate reviewer, and understand SLA; inspect an eligible-human
  consensus display that states population/rule/unknowns without private IDs.
- **Hard states:** conflicted moderator, SLA missed, unfence without authority,
  duplicate/linked/recovered person, stale proposal, sponsor fleet, agent vote,
  indeterminate eligibility, open-web/cache purge failure.
- **Desktop:** case queue, selected evidence, and decision/appeal history form a
  governed workbench; consensus display is separate and never reveals voters.
- **iPhone:** report, notice, response, decision, and appeal are discrete,
  resumable steps with the current deadline and reviewer role always visible.
- **iPad:** queue/sidebar, evidence canvas, and decision inspector use adaptive
  columns; pointer/keyboard shortcuts cannot submit a decision without review.

### 4.1 Public-network dependency and default-off contract

All public-network controls in these references are non-authoritative design
fixtures today. Every corresponding production switch is known false unless a
later wave's signed activation explicitly proves otherwise. `pn_moderation` is
a mandatory parent of every externally visible PN read or write; parent-off,
unknown, or unhealthy state renders `feature_off`, `unavailable`, or
`blocked_dependency` and offers only an honest next gate—never fixture data.

| Journey | Earliest authoritative wave | Required parent contract while enabled | Parent-off reference behavior |
|---|---|---|---|
| J6 Public Workspace | PN1 for private draft/projection; PN2 for bounded public pilot | PN1 authority plus `pn_moderation`; PN2 adds signed pilot cohort/rollback gate | draft remains private; publish/read controls unavailable with exact dependency copy |
| J7 Public Work Object | PN1 typed private object/projection; PN2 bounded publication | workspace/revision/provenance authority plus `pn_moderation` and PN2 pilot gate | authoring may remain private; public preview/publish unavailable |
| J8 Workstream/workspace entry | PN2 bounded chronological read; PN3 adds Explore only for eligible Public Work Objects | PN2 read cohort plus `pn_moderation`; Explore additionally requires PN3 eligibility | empty-safe unavailable panel; no substitute private/network fixture results |
| J9 public profiles/provenance | PN1 projections; PN2 bounded public read | exact current projection/publication authority plus `pn_moderation` and PN2 read cohort | private owner preview only where separately authorized; external view unavailable |
| J10 Work Search/contact | existing W6 external qualification, not PN Explore | W6 legal/privacy/cohort/session/publication/contact gates; people-finding never routes through Explore | visible qualification dependency; no cached or synthetic results/contact |
| J11 agent public participation | PN4 only; PN2 expressly human-authored | PN4 sponsor/delegation/provenance/anti-synthetic-consensus gate plus `pn_moderation` | private governed agent controls may remain; public agent authorship unavailable |
| J12 public-presence controls | PN1 inventory/projection; PN2 public effects | exact owner/controller authority plus `pn_moderation` and current purge/rollback health | inventory shows only authoritative current state; public actions unavailable |
| J13 moderation/consensus/open web | moderation begins before PN2; consensus only PN2 eligible humans; open web PN5 last | `pn_moderation` for all; consensus eligibility/current-person gate; PN5 cache/purge/abuse gate | fail closed with deadline/state explanation; no public render, vote, or unfence |

## 5. Interaction-state matrix

Every semantic component and journey must render and behave in:

`idle | loading | empty | ready | feature_off | unavailable | blocked_dependency | pending_approval | offline | retrying | stale | unauthorized | revoked | quarantined | corrected | purge_pending | purge_failed | restored | terminal`

The state comes from one product contract, not platform-specific optimistic
copy. Transitions preserve focus/selection/draft where safe, announce changes
accessibly, expose an exact next action, and never reveal a hidden object's
existence through timing, counts, or differentiated errors.

## 6. Reference artifact set

PD0 reference acceptance requires:

1. one executable fixture manifest with closed schemas and no production data;
2. desktop interactive references at 1024, 1280, 1440, and 1728 CSS px;
3. native iPhone compact and large portrait references plus landscape media;
4. native iPad portrait/landscape, 1/3-1/2-2/3 Split View, and Stage Manager
   width references;
5. a state-switch harness for every interaction state above;
6. keyboard/focus, pointer/hover/context-menu, touch/gesture, drag/drop,
   Dynamic Type, VoiceOver, reduced-motion, offline/reconnect, and resize tests;
7. screenshots/video only as supporting evidence—normal interaction logs and
   assertions are controlling; and
8. independent design Critic Loop evidence linked to the exact artifact digest.

## 7. Acceptance measures and budgets

| Measure | Reference gate |
|---|---|
| Cohesion | one semantic IA, state model, trust grammar, icon meaning, and token contract; zero duplicate global shells in the target design |
| Task completion | every J1-J13 outcome completes without a hidden route, authority shortcut, or platform fallback |
| Learnability | destination and action labels remain stable; Why/Unknown and next authority are visible without training |
| Accessibility | 200% web zoom; complete keyboard; VoiceOver; Dynamic Type; non-color state; reduced motion; 44 pt native and 40 CSS px web targets |
| Performance | shell input response p95 <=100 ms; navigation feedback <=100 ms; cached route paint <=500 ms; first useful uncached content target <=1.5 s; 60 fps interaction target with measured long-list virtualization; no authority claim depends on latency |
| Continuity | selection, draft, source, authority, and pending operation survive permitted resize/background/reconnect; stale authority fences instead of replaying |
| Canvas use | iPad and desktop show useful simultaneous context at regular widths; no stretched cards or gratuitous empty regions |
| Trust | every cross-boundary object exposes principal, accountable human if agent, audience, revision, verification limit, correction/revocation, Why/Unknown, and controls |

Performance failures do not weaken authority or privacy. A slow authorized path
remains slow or unavailable; it never falls back to a private, stale, client-
selected, or unqualified source.

### 7.1 Reproducible performance protocol

- **Owners/output:** Web Platform owns a versioned JSON trace plus Web Vitals and
  frame capture; Native Platform owns a versioned Maestro/XCTest interaction
  trace plus React Native performance/frame capture. Product Design signs the
  fixture/version mapping. Raw traces and a body-minimized aggregate receipt are
  bound to the reviewed commit, fixture digest, device class, and run timestamp.
- **Reference classes:** desktop uses current stable Chrome on macOS at 1440 x
  900 CSS px on an Apple-silicon machine with 8 GB or more; iPhone uses the
  oldest supported physical iPhone OS/device class and the current large iPhone
  simulator; iPad uses the oldest supported physical iPad OS/device class plus
  the current 11-inch simulator at full screen and 1/2 Split View. Exact model,
  OS, browser, scale, thermal state, power mode, and build identifier are recorded.
- **Fixture:** the closed reference manifest contains 200 Workstream objects,
  100 Work Search candidates with a server-limited visible result, 100 Work
  Record evidence cards across six sections, 50 chat messages, 25 artifacts,
  10 workspace members, and the complete J1-J13 hard-state set. No production
  or randomly generated data is permitted.
- **Network:** cached runs use an already installed local bundle and primed
  immutable assets but a fresh route process/state. Uncached content runs use a
  cleared application cache and a shaped 20 Mbps down / 5 Mbps up / 40 ms RTT /
  0% loss profile. Offline/reconnect is a separate deterministic test and is
  excluded from latency percentiles.
- **Timing boundaries:** input response starts at hardware/event dispatch and
  ends at the next presented visual acknowledgement; navigation starts at the
  accepted activation event and ends at committed route shell feedback; cached
  paint ends when named above-the-fold semantic content is presented; uncached
  useful content ends when the first authorized non-skeleton object plus its
  trust state is presented. Server/provider time is reported separately.
- **Sampling:** one warm-up is discarded, then 30 cold-process samples per
  journey/class and 50 interaction samples per high-frequency control are
  recorded. Budgets use p95 from the raw samples; failures may not be deleted.
  Median and worst sample are also reported. Clock source and profiler version
  are fixed in the receipt.
- **Frames/jank:** scrolling, resize/orientation, sheet/inspector transition,
  long-list insertion, and drag/drop each run for at least 10 seconds. Target is
  display-refresh cadence with p95 frame time <=16.7 ms on 60 Hz classes and no
  task with >1% frames above 50 ms; 120 Hz devices are still judged against the
  60 Hz release floor and separately report native cadence. List virtualization
  must keep rendered-row count bounded to the visible window plus documented
  overscan.

## 8. Ownership and exit gate

- Product Design owns the thesis, IA, reference fixture, journey composition,
  and design critic packet.
- Web Platform owns desktop shell, focus/keyboard/resize/deep-link/drag-drop and
  performance evidence.
- Native Platform owns the universal Expo semantic system and shared state.
- iPhone owner owns reach, voice, sheets, interruption/background and real
  iPhone acceptance.
- iPad owner owns size classes, multi-column shell, Stage Manager/Split View,
  keyboard/pointer/drag-drop and real iPad acceptance.
- Trust/Privacy owns provenance, View As, revocation, moderation, deletion and
  accessibility semantics.
- PN/W5-W8 owners retain all authority, cohort, provider, activation, restore,
  and soak gates; a visual prototype cannot waive them.

This contract can pass a design review before implementation. PD0 itself passes
only after the exact interactive artifacts, parity/deprecation/component
packages, rendered interaction evidence, and independent design Critic PASS
exist. Real iPhone and real iPad acceptance remain release gates even after PD0
reference acceptance.
