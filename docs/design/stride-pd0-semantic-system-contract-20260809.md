# STRIDE PD0 governed semantic system and migration contract

Status: `decision_complete_supporting_design_contract`

Observed: 2026-08-09

Scope: desktop web, native iPhone, native iPad

Authority: supporting PD0 design contract; it does not activate product behavior or replace the canonical master plan

## 1. Product thesis and non-negotiable boundary

STRIDE makes work legible through verified contribution, provenance, consent, and current authority. Its interface must make state, audience, evidence, and consequence understandable without turning work into social performance. The design system therefore optimizes for calm comprehension, trustworthy action, and private-by-default collaboration—not engagement theater, worker scoring, decorative dashboards, or profile performance.

There is one semantic system and three first-class platform expressions. Web, iPhone, and iPad share meaning, content contracts, state machines, accessibility obligations, and trust language. They do not need pixel-identical layouts. A platform may adapt navigation, density, material, input behavior, and pane structure only through the mappings in this contract.

This document governs future implementation. It creates no visual mockups, collects no telemetry, changes no runtime, and authorizes no publication, search, contact, or production release.

## 2. Source of truth and governance

The target source of truth is a platform-neutral, versioned semantic schema. Generated artifacts consume it:

- Web: static CSS custom properties plus typed component contracts.
- Native: typed React Native tokens plus iOS semantic mappings.
- Tests and codemods: the same closed schema and component-state vocabulary.

Raw palette values are implementation inputs. Feature code may consume only semantic roles or governed component props. A platform adapter may map a semantic role to an explicitly approved platform value; it may not rename the role, weaken its meaning, or introduce a private one-off token.

Ownership is divided as follows:

| Contract | Accountable owner | Allowed change |
|---|---|---|
| Semantic roles, scales, and component state machines | Design Systems | Versioned schema change |
| Trust/provenance semantics and audience language | Privacy and Trust | Versioned schema and copy change |
| Web adapter and primitives | Web Platform | Conforming adapter change |
| iPhone/iPad adapter and primitives | Native Platform | Conforming adapter change |
| Accessibility gates | Accessibility owner | May block any release |
| Feature composition | Feature team | Governed components and semantic props only |
| Legacy inventory and removal proof | Design Systems with platform owner | Receipted migration entry |

The system uses semantic versioning:

- Patch: a value correction that preserves role and component meaning.
- Minor: an additive token, state, component variant, or platform mapping.
- Major: removal, rename, or change of semantic meaning or state behavior.

Deprecations remain available for two minor releases and carry an owner, replacement, introduced version, removal version, and link to a legacy-inventory entry. New use of a deprecated alias fails lint immediately. Removal requires the gates in section 13; elapsed time alone never permits removal.

## 3. Exact token contract

### 3.1 Private raw scales

The palette is not a feature API. These values make current web/native drift explicit and give adapters a complete common source.

| Scale | Values |
|---|---|
| `warm` | `0 #FFFDF8`, `50 #F2EDE4`, `100 #EDE8DF`, `200 #DDD4C6`, `300 #CFC5B7`, `400 #C2B7A7`, `500 #B2A695` |
| `ink` | `950 #09090B`, `900 #101013`, `850 #141418`, `800 #1B1B21`, `700 #26262E`, `600 #34343E`, `500 #4D4D59`, `400 #6E6E7A`, `300 #9A9AA4` |
| `signal` | `400 #5CE08A`, `500 #30D158`, `600 #23A847` |
| `ember` | `300 #FFA07A`, `400 #FF7F4D`, `500 #FF5A19`, `600 #E64100`, `text #86290F` |
| `status` | `danger #FF453A`, `warning #FF9F0A`, `info #0A84FF` |
| `fixed` | `white #FFFFFF`, `black #000000`, `transparent transparent` |

### 3.2 Semantic color roles

| Role | Light | Dark | Meaning |
|---|---|---|---|
| `color.canvas` | web `warm.200`; native `warm.300` | `ink.950` | Platform ground. The difference is an approved material expression, not a feature override. |
| `color.surface.primary` | `warm.50` | `ink.850` | Primary readable surface |
| `color.surface.secondary` | `warm.100` | `ink.900` | Recessed or grouped surface |
| `color.surface.tertiary` | `warm.300` | `ink.800` | Well, selection, or bounded secondary region |
| `color.text.primary` | `#26231E` | `#F7F3EC` | Required for primary content |
| `color.text.secondary` | `rgba(38,35,30,.87)` | `rgba(247,243,236,.78)` | Supporting content |
| `color.text.tertiary` | `rgba(38,35,30,.75)` | `rgba(247,243,236,.66)` | Nonessential metadata only |
| `color.text.disabled` | `rgba(38,35,30,.43)` | `rgba(247,243,236,.40)` | Disabled label; never the only state signal |
| `color.line.subtle` | `rgba(38,35,30,.12)` | `rgba(247,243,236,.12)` | Group boundary |
| `color.line.strong` | `rgba(38,35,30,.22)` | `rgba(247,243,236,.22)` | Interactive or high-emphasis boundary |
| `color.action.primary` | `ink.700` | `warm.200` | Primary action; foreground uses the inverse text role |
| `color.action.agent` | `ember.500` | `ember.400` | Agent-originated activity only, never generic promotion |
| `color.state.live` | `signal.600` | `signal.400` | Currently live or listening |
| `color.state.success` | `signal.600` | `signal.400` | Completed successfully; must use a distinct icon/label from live |
| `color.state.warning` | `status.warning` | `status.warning` | Caution or degraded behavior |
| `color.state.danger` | `status.danger` | `status.danger` | Destructive, failed, or revoked |
| `color.state.info` | `status.info` | `status.info` | Neutral system information |
| `color.focus` | `status.info` | `#64D2FF` | Keyboard/accessibility focus ring |
| `color.scrim` | `rgba(9,9,11,.38)` | `rgba(0,0,0,.62)` | Modal separation only |

Text and icon contrast must be calculated against the actual composited background. Normal text requires 4.5:1; large text requires 3:1; essential icons, focus indicators, and control boundaries require 3:1. `text.tertiary` is forbidden for instructions, actions, evidence state, authority, or any content whose loss changes understanding. Color never acts alone: live, success, warning, revoked, and selected states require a label, icon, shape, or position distinction.

### 3.3 Typography

Primary family is Google Sans Flex (`400`, `500`, `600`, `700`). Structured labels and numeric evidence use Geist Mono (`400`, `500`, `600`). Native may use SF Pro/SF Mono only when the governed fonts are unavailable at first paint; layout metrics must remain within one point and may not cause content truncation.

| Role | Size/line | Weight | Tracking | Use |
|---|---:|---:|---:|---|
| `type.displayXL` | `56/58` | 600 | `-1.20` | Web/iPad landmark only; iPhone maps to `display` |
| `type.display` | `40/43` | 600 | `-0.80` | Major page or outcome statement |
| `type.title1` | `28/32` | 600 | `-0.60` | Screen title |
| `type.title2` | `21/26` | 600 | `-0.34` | Section title |
| `type.headline` | `17/22` | 600 | `-0.10` | Row/card heading |
| `type.body` | `15/22` | 400 | `0` | Default prose and control label |
| `type.bodyMedium` | `15/22` | 500 | `0` | Emphasized body |
| `type.bodySmall` | `14/22` | 400 | `0` | Dense supporting copy |
| `type.bodyLongform` | `14/23` | 400 | `0` | Long-form artifact text |
| `type.caption` | `13/19` | 400 | `0` | Metadata with full contrast |
| `type.captionMedium` | `13/19` | 500 | `0` | Emphasized metadata |
| `type.label` | `11/13` | 500 mono | `0.66` | Short structured label, maximum 24 characters |
| `type.labelLarge` | `13/17` | 500 mono | `0.26` | Structured control/evidence label |
| `type.numeric` | `13/16` | 500 mono | `0` | Dates, revisions, counts; tabular figures |
| `type.button` | `14/18` | 600 | `0` | Button text |
| `type.wordmark` | `22/22` | 600 | `-0.30` | STRIDE wordmark only |

Native mappings use the corresponding Dynamic Type text style and reflow through accessibility sizes, including AX5. Critical text is never capped. Large display roles may step down to the next semantic role when space is constrained; they may not truncate body content. All numbers that compare across rows use tabular figures. Uppercase is limited to short structured labels; sentences and actions use sentence case.

### 3.4 Spacing and density

The spacing scale is `0, 2, 4, 8, 12, 16, 20, 24, 32, 40, 48, 64, 80` points/CSS pixels. No other layout gap is permitted outside an approved optical offset of `1` or `-1` on an icon or baseline.

| Density | Row/control visual height | Minimum hit target | Use |
|---|---:|---:|---|
| `touch` | 44–56 | `44×44` | iPhone default; iPad touch targets |
| `comfortable` | 44 | `44×44` | Web default, spacious iPad regions |
| `compact` | 36 | web `40×40`; iPad hit area still `44×44` | Pointer/keyboard data regions only |

Prose width is at most `680`; form width `560`; inspector width `320–420`; contextual popover width `280–420`. Components use `gap` rather than child margins. A feature may select a governed density for a whole region; it may not mix arbitrary row heights inside one list.

### 3.5 Radius, elevation, and material

Radius scale: `0, 4, 8, 12, 16, 22, 28, 999`. Roles are `control 12`, `card 16`, `panel 22`, `modal 28`, `chip 999`, `media 16`, and `focus 4`. Native iOS uses continuous corners where supported.

Elevation roles are:

- `elevation.0`: no shadow; use normal surface and boundary roles.
- `elevation.1`: `0 1px 2px rgba(9,9,11,.10)`; native shadow opacity `.10`, radius `2`, y `1`.
- `elevation.2`: `0 8px 24px rgba(9,9,11,.14)`; native opacity `.14`, radius `12`, y `8`.
- `elevation.3`: `0 24px 64px rgba(9,9,11,.18)`; native opacity `.18`, radius `28`, y `18`.
- `material.glass`: semantic background plus blur `20` and saturation `1.15`; it is a material, not an extra elevation level.

Nested shadows are forbidden. Light surfaces retain a visible semantic boundary. Reduced-transparency mode replaces glass with an opaque `surface.primary` or `surface.secondary` and preserves the same boundary and hierarchy.

### 3.6 Icon semantics

iOS uses curated SF Symbols. Web uses one curated outline family with `1.75` stroke and round caps/joins. Sizes are `16`, `20`, `24`, and `28`; `20` is the default control icon. Filled symbols are limited to current selection, an active live state, or a critical warning.

The closed semantic set for PD0 primitives is: `add`, `back`, `close`, `more`, `search`, `filter`, `compose`, `edit`, `approve`, `deny`, `retry`, `restore`, `offline`, `stale`, `unauthorized`, `revoked`, `pending`, `verified`, `provenance`, `private`, `organization`, `public`, `warning`, `destructive`, `person`, `agent`, `work`, `artifact`, `voice`, `play`, `pause`, and `stop`. Adapters own the platform glyph mapping. Feature code asks for the semantic name, not a glyph. Emoji, Unicode approximations, and unlabeled custom drawings are forbidden. Authority, trust, and destructive actions always pair icon and text.

### 3.7 Motion

| Role | Duration | Curve | Use |
|---|---:|---|---|
| `motion.fast` | `120ms` | `cubic-bezier(.32,.72,0,1)` | Press, focus, small disclosure |
| `motion.medium` | `220ms` | `cubic-bezier(.32,.72,0,1)` | Sheet, pane, selection transition |
| `motion.slow` | `360ms` | `cubic-bezier(.32,.72,0,1)` | Major spatial transition |
| `motion.spring` | `220ms` | `cubic-bezier(.34,1.25,.5,1)` | Bounded direct manipulation only |
| `motion.breathe` | `2400ms` | ease-in-out | A genuinely live/listening indicator only |

Default animation uses opacity and transform; layout animation requires measured proof that it does not block input or cause content shift. Continuous decorative motion is forbidden. A waveform may reflect real input amplitude but may not simulate activity.

With reduced motion, decorative and breathing animation is removed, spatial movement becomes an instant state change, and essential transition feedback is a nonspatial opacity change of at most `80ms`. Information formerly communicated by amplitude or direction is represented by a static level, label, or numeric state. Reduced transparency is independent and must also be honored.

## 4. Governed component vocabulary

The primitive catalog is: `Surface`, `Stack`, `Inline`, `Text`, `Icon`, `Button`, `IconButton`, `Field`, `SearchField`, `Select`, `Toggle`, `Chip`, `Badge`, `Avatar`, `Divider`, `Card`, `Row`, `VirtualizedList`, `Skeleton`, `Spinner`, `StatusBanner`, `EmptyState`, `Toast`, `Dialog`, `Sheet`, `Popover`, `Menu`, `Tabs`, `SegmentedControl`, `TrustProvenanceStrip`, `PermissionGate`, and `MutationCoordinator`.

Compound components may compose primitives but must expose the universal state contract rather than invent a screen-local loader, empty card, permission message, or retry ledger. Lists with more than one repeated data row use virtualization; mapped repeated rows inside a root scroll container are forbidden. Every control has an accessible name, semantic role, visible focus, and deterministic disabled reason.

### 4.1 Closed domain-component catalog

Feature code may compose only the following governed domain components during
PD0/PD1. Each receives typed server projections; none accepts a free-form
`actor`, `authority`, `audience`, `status`, `provenance`, or action payload.

| Component | Required semantic inputs | Required distinction and bound journey |
|---|---|---|
| `SignalControl` | listening state, input mode, source authority, interruption state, server action | Voice/typed entry is a source control, never a universal public composer; J2 |
| `HumanMessage` | human principal projection, audience, revision, effective time, delivery state | A human communication; cannot render agent/system attribution; J2-J3 |
| `AgentContribution` | agent principal, accountable human/sponsor, package/runtime/delegation, audience, revision, verification limit | Visibly machine-authored and never styled or announced as a human message; J2-J4/J11 |
| `SystemEvent` | closed event type, effective time, related object ref, bounded state | Non-social service fact; no avatar, opinion, vote, author voice, or reply affordance; J3-J4/J13 |
| `SuggestedWorkCard` | suggestion revision, source class/ref, audience, scope, approval state, exact server actions | Proposal is not approved work or a Work Object; J2 |
| `RunTimeline` | run/intervention revisions, current actor, accountable human, provider state, recovery state | Separates requested/approved/applied/reconciled and never implies provider success; J4 |
| `InterventionRequest` | bounded schema, controller, deadline, revision, consequence, server action | Human decision point, not chat text or generic form; J4 |
| `ArtifactRevisionView` | artifact/ref revision, audience, source/output provenance, review and verification states | Artifact, review, and verification remain separate claims; J3-J4 |
| `OutcomeRecordView` | outcome revision, evidence refs, accountable human, verification state, corrections | Outcome is evidence-backed work state, not an engagement/result score; J4-J5 |
| `WorkRecordSection` | one of six section types, current eligible evidence refs, field controller, release state | Person-controlled record; evidence cannot collapse into profile biography; J5 |
| `EvidenceCard` | exact claim/attestation/approval refs, revision, audience, freshness, Why/Unknown, server actions | Distinguishes self-asserted, attested, verified, disputed, revoked, and unknown; J5/J9 |
| `PublicWorkspaceView` | workspace projection/revision, owner/moderator roles, participation/retention, moderation and publication states | Public projection is never a private organization/team ACL or generic card; J6/J8 |
| `PublicWorkObjectView` | one closed object type, workspace/ref revision, human/agent authorship, provenance, moderation/publication state | Typed public work, never a status post, feed card, or artifact without its object type; J7-J8 |
| `WorkstreamRow` | chronological eligible object projection, mode, Why/Unknown, private Observe/Save state | No engagement rank/count/social recommendation styling; J8 |
| `PersonProfileView` | released person projection, current audience, evidence, controls | Human identity/consent/governance semantics; never agent/system profile; J9 |
| `OrganizationProfileView` | released organization projection, current roles/controls, evidence | Organization authority and work are distinct from a person or workspace; J9 |
| `WorkspaceProfileView` | public workspace projection, participation/purpose, current objects and controls | Workspace public purpose is not organization membership; J9 |
| `AgentProfileView` | agent projection, sponsor, accountable human, package/runtime/delegation, visibility/participation states | Machine identity has no human rights, employment authority, social edge, or consensus action; J9/J11 |
| `WorkSearchInterpretation` | opaque proposal ID/revision, visible interpretation, policy/cohort/session binding, confirm action | Interpretation precedes retrieval and cannot display synthetic results; J10 |
| `WorkSearchResult` | exact recorded disclosure, publication/attestation refs, Why/Unknown, current contact capability | Result is governed search disclosure, not profile-map reread or Explore row; J10 |
| `ContactRequestView` | purpose-bound request revision, sender/recipient projections admitted to each viewer, terminal state, server actions | Never reveals contact channel before acceptance or behaves as a DM; J10 |
| `ModerationCaseView` | policy/case revision, reporter-safe state, quarantine/notice/deadline, conflict-separated reviewer/appeal roles | Case is not public content and cannot expose reporter/private evidence; J13 |
| `ConsensusDisplay` | eligibility-manifest/proposal revisions, population/rule/unknowns, current aggregate | Human-only governed decision; no agent/system votes, voter list, social poll, or engagement count; J13 |

Every domain component requires the universal `dataState`, exact projection
revision, audience, current `TrustProvenanceStrip` model where applicable, and
only server-minted closed actions. Authorship components additionally require a
closed principal kind (`human`, `agent`, `system`) and reject missing or
contradictory identity. Public components additionally require current parent-
switch and moderation state; `feature_off`, `unavailable`, or
`blocked_dependency` cannot mount a ready child projection.

Migration must never collapse `HumanMessage`, `AgentContribution`, and
`SystemEvent` into one visually ambiguous author row; `SuggestedWorkCard`,
`PublicWorkObjectView`, `ArtifactRevisionView`, `OutcomeRecordView`, and
`EvidenceCard` into one generic card; the four profile components into one
authority-blind template; Work Search into Workstream/Explore; contact into
chat; or moderation/consensus into public engagement. The legacy inventory
records each occurrence and its exact destination component.

## 5. Universal component and data state machine

Every data-bearing component uses the following closed states:

`idle → loading → ready | empty | feature_off | unavailable | blocked_dependency | offline | stale | unauthorized | revoked | error`

An action from `ready` or `stale` may enter `pending`, then `restored`, `ready`, `retry`, `unauthorized`, `revoked`, or `error`. `retry` is a permitted transition only for a transport failure, lost response, or retryable server failure. The same action fingerprint must reuse the same idempotency key until authoritative success, conflict reconciliation, or explicit discard.

Precedence is `revoked > unauthorized > feature_off > blocked_dependency > unavailable > offline > stale > pending > loading > empty > ready`. A higher-precedence state governs sensitive rendering and action availability even if lower state data exists. Public or cross-boundary components use the most opaque copy permitted by the server contract; precedence never authorizes disclosing which parent gate failed.

| State | Required rendering and behavior |
|---|---|
| `loading` | Preserve expected geometry with a skeleton. Use a spinner only for a bounded local action. Do not show fake content or infer an empty result. |
| `empty` | Only after an authoritative successful response confirms no items. Name what is absent and offer only a server-authorized next action. |
| `feature_off` | A known server-owned switch or mandatory parent is off. Render the bounded product-level state and its authorized next gate; do not mount fixture data, stale public data, or a legacy fallback. |
| `unavailable` | The capability cannot currently prove its dependency or authority contract. Use opaque, non-diagnostic copy appropriate to the audience and expose no inferred content or identity. |
| `blocked_dependency` | A named user-visible prerequisite is safely discloseable and incomplete, such as consent or an owner-controlled setup step. It may link only to that governed step and never permit the blocked action. Internal policy, cohort, or authority failures remain `unavailable`. |
| `offline` | Show an offline banner and last safe cache only when its audience remains current. Disable mutations unless a server contract explicitly supports durable offline admission. Never describe cached authority as current. |
| `stale` | Mark the projection stale with its last effective time. Disable irreversible or authority-changing actions. Reload through the current server authority. |
| `unauthorized` | Remove private content and actions. Private resource selectors remain opaque, normally `404`; do not reveal whether the resource exists. |
| `revoked` | Immediately clear sensitive content, cache, previews, clipboard affordances, and channels. Explain the loss at a bounded semantic level and route to a safe landing. No cached rendering. |
| `pending` | Freeze the exact action, revision, closed values, account, and idempotency key. No optimistic authority or projection. Only exact retry, reconcile, or explicit discard is available. |
| `retry` | Reuse the recorded key and exact fingerprint. A changed action/body is rejected until discard. A `400` is authoritative: clear pending and preserve the current projection with a bounded validation message. A `409` settles pending and reloads. Transport and `5xx` retain ambiguity. |
| `restored` | The server or restart recovery has reconciled the operation. Show a bounded confirmation, replace data with the authoritative projection, then transition to `ready` or `empty`. |
| `error` | Bounded non-authority failure. Preserve only content proven safe under current audience; offer retry only when idempotency and authority contracts permit it. |

No state may be encoded by color alone. Copy must distinguish “nothing exists,” “not available to you,” “not available now,” “out of date,” and “access removed.” A component never turns a `403`, `404`, `501`, or `503` into apparent availability.

## 6. Trust and provenance strip

`TrustProvenanceStrip` is the canonical compact explanation of why a work object, contribution, profile field, artifact, search result, or agent output is visible. It has compact and expanded modes and is body-minimized.

The strip may contain only:

- audience: `private`, `organization`, `named_parties`, or `public`;
- source class: `self_asserted`, `organization_attested`, `system_observed`, `agent_derived`, or `published_claim`;
- verification tier: the contract-approved closed tier;
- current revision and effective timestamp;
- freshness state: `current`, `stale`, `superseded`, `withdrawn`, or `revoked`;
- controller role, never a hidden controller identifier;
- safe released-field labels;
- body-free source/output digests or public references already authorized for this audience;
- correction, withdrawal, or revocation status; and
- a “Why can I see this?” disclosure that explains the current audience/policy path without exposing membership, contact, or source bodies.

Unknown provenance is rendered as `unverified` or `unknown`, never silently upgraded. A revoked or withdrawn source synchronously removes derived visible claims before the strip can describe them as current. Private source text, prompts, hidden memberships, email, session data, scores, unreleased fields, and internal reasoning never enter the strip. Public and private projections use separately admitted data; the public strip is not a redacted private component tree.

## 7. Platform semantic mapping

| Concern | Desktop web | Native iPhone | Native iPad |
|---|---|---|---|
| Navigation | Persistent app rail/sidebar where width permits; URL-addressable detail; browser back | Native stack, bottom-reach primary actions, sheets for focused tasks | Available-width adaptive sidebar/detail/inspector; native stack inside pane where needed |
| Layout | Governed widths `1024`, `1280`, `1440`, `1728`; pointer and keyboard first-class | Single-column portrait default; safe areas and automatic insets; voice and one-handed reach | Compact `<600`: iPhone expression; medium `600–899`: sidebar + detail; expanded `≥900`: sidebar + detail + persistent inspector |
| Overlay | Native dialog/popover semantics; anchored menu | Sheet for tasks, alert for critical confirmation | Popover for contextual actions, sheet for bounded tasks, full-screen only for immersive room/work |
| Input | Keyboard, pointer, hover, focus, drag where appropriate | Touch, voice, system share/picker, haptics | Touch + keyboard shortcuts + focus system + pointer/hover/context menu + drag/drop |
| Density | Comfortable default; compact for governed data regions | Touch only | Touch default; compact visual rows allowed with `44×44` hit regions |
| Scrolling | One primary scroll owner per pane; virtualized collections | Root automatic insets or virtualized list | Independent pane scroll owners; virtualized collections; no stretched phone feed |
| System semantics | Semantic HTML, real button/link/dialog/list/table roles | React Native accessibility roles, native stack, Dynamic Type, VoiceOver | Same native contract plus size-class, Split View, Stage Manager, focus, pointer, and multi-pane semantics |

iPad adaptation is based on available content width, not device model. Portrait orientation locks are incompatible with the target iPad expression. A stretched iPhone card column does not count as iPad support.

## 8. Accessibility release budgets

The release floor is WCAG 2.2 AA on web and the equivalent Apple accessibility behavior on native:

- Contrast thresholds in section 3.2 pass in light, dark, increased-contrast, and reduced-transparency modes.
- All native touch targets are at least `44×44`; every web control hit target is at least `40×40` CSS pixels and retains an unambiguous focus ring.
- Full keyboard operation includes logical tab order, visible focus, skip navigation, Escape dismissal, arrow-key composite navigation, and focus restoration to the invoker.
- VoiceOver/screen-reader output announces label, role, value, state, error, audience, and consequence without reading decorative content.
- Dynamic Type through AX5 reflows without clipping, overlapping, or hiding an action. Landscape and Split View remain operable.
- Text and controls support 200% browser zoom and reflow at 320 CSS pixels without two-dimensional scrolling except an inherently two-dimensional artifact.
- Reduced motion and reduced transparency follow section 3.7 and section 3.5.
- Captions/transcripts accompany meaningful recorded audio where the product has authority to retain them; voice is never the only input path for a required action.
- Destructive actions name the object and consequence, require confirmation when not immediately reversible, and return focus predictably.

Any failure blocks component or reference-flow acceptance; an exception requires an owner, affected audience, expiry, and replacement—not a blanket waiver.

## 9. Performance budgets

Budgets are measured on production-like builds with representative content:

- Generated semantic tokens add at most `12 KB` gzip to initial web CSS and require no runtime token parser.
- Web Core Web Vitals at p75: LCP `≤2.5s`, INP `≤200ms`, CLS `≤0.10` at the four governed widths.
- A press or keyboard action shows local feedback within `100ms`; no main-thread task caused by a design-system interaction exceeds `50ms`.
- Web/native transitions sustain 60 fps on supported devices; animation uses compositor-safe properties unless measured otherwise.
- Native first meaningful screen is `≤2.5s` cold and `≤1.0s` warm on the oldest supported iPhone/iPad with a representative authenticated projection.
- Virtualized list row render is `≤4ms` median, the initial batch contains no more than two viewports, and off-screen media is not eagerly decoded.
- Images declare dimensions, use bounded variants, and do not shift layout. A component does not fetch merely to render decoration.
- State recovery and provenance expansion are local UI operations after the authoritative response; they must not trigger an unbounded profile or source-body reread.

The exact device/browser classes, reference fixture cardinalities, cold/cached
definitions, shaped network, timing boundaries, sample count, percentile rule,
frame/jank threshold, owners, and output receipts are frozen in section 7.1 of
[the PD0 interactive reference-flow contract](./stride-pd0-reference-flow-contract-20260809.md).
Measurements that omit any required field are not comparable release evidence.

Performance regressions require the same owner/expiry/replacement discipline as accessibility exceptions.

## 10. Adapter and implementation rules

1. Generated adapters are deterministic and checked in or reproducibly built; hand-edited generated output fails verification.
2. Web consumes semantic CSS variables and governed component props. New direct hex/rgb values, ad hoc shadows, arbitrary radii, and pixel gaps outside the scale fail lint.
3. Native consumes typed tokens/components. New inline palette literals, platform glyph strings, arbitrary animation timings, or screen-local state cards fail lint.
4. A platform-only capability is wrapped by an adapter with an explicit semantic fallback. Absence never widens authority or hides an unavailable state.
5. Trust, audience, and mutation state are server-derived inputs. Adapters format them; they do not join authority, infer provenance, or mint actions.
6. Legacy aliases point only toward the governed primitive. A governed component never imports a legacy feature component.
7. Component props use closed unions for variant, density, state, intent, and icon. Free-form `color`, `shadow`, `radius`, `status`, or `authority` props are prohibited.
8. Private/public projection trees remain distinct through rendering. A display adapter cannot “sanitize” a private model into a public model.

## 11. Codemod contract

Codemods are narrow, deterministic, idempotent, and reviewable. Each transformation has positive, negative, and already-migrated fixtures.

- CSS literal replacement maps only exact inventoried values to semantic roles. Ambiguous values produce a review marker and fail the migration gate; they are never guessed.
- React/HTML codemods replace known legacy controls with governed primitives while preserving event order, form semantics, accessible names, test IDs where still contractual, and server action payloads.
- React Native codemods replace token imports, repeated Pressable/Text shells, state cards, and icon glyphs only when prop equivalence is proven.
- State codemods cannot collapse `empty`, `offline`, `stale`, `unauthorized`, `revoked`, and `error` into one fallback.
- Codemods do not alter route names, network requests, authorization, persistence, analytics, or content bodies.
- A second run produces a byte-identical result. Unknown constructs stop with an exact file/line report.

## 12. Legacy inventory linkage

The authoritative starting inventory is [STRIDE PD0 current product parity and removal audit](./stride-pd0-current-product-parity-audit-20260809.md). Every migrated surface and removed component must cite its audit row and disposition. The inventory must additionally record exact occurrences of:

- duplicate web shells, navigation rails, headers, composer/voice controls, cards, status treatments, and settings paths;
- mobile screen-local loading/error/empty presentations and repeated Pressable/Text shells;
- raw web/native palette, spacing, radius, elevation, motion, and icon literals;
- phone-only assumptions in the native navigator, portrait configuration, sheets, scroll ownership, and fixed widths;
- stale/unauthorized/revoked states that currently share generic error rendering; and
- legacy pages or routes whose capability has moved into the surviving shell.

An entry is `mapped`, `adapted`, `migrated`, `verified`, or `removed`. “Hidden,” “unused in the happy path,” and “covered by the new component” are not removal proof.

## 13. Migration sequence and removal gate

1. **Freeze and inventory.** Generate literal/component/route counts and bind them to the parity audit. New legacy usage is blocked.
2. **Establish the neutral schema.** Generate web/native adapters and parity snapshots without changing feature behavior.
3. **Land primitives and state machines.** Verify accessibility, performance, contrast, state transitions, and platform semantics in isolation.
4. **Build reference flows.** Produce the artifacts in section 15 and approve them before broad surface migration.
5. **Migrate by complete user flow.** Move one vertical journey across its states and governed widths/devices. Preserve server contracts and default-off truth.
6. **Run coexistence checks.** Legacy aliases remain rollback-only; no new feature imports are allowed.
7. **Remove.** Delete a legacy component or route only when all references, runtime reachability, CSS, tests, docs, and navigation entries are zero and the replacement passes the same capability/state matrix.

Removal proof requires:

- AST/CSS/import scans showing zero live use and no unclassified literal;
- codemod fixtures and second-run idempotency;
- normal, dark, increased-contrast, Dynamic Type, VoiceOver/screen-reader, keyboard/pointer, reduced-motion, and reduced-transparency tests;
- rendered reference-flow comparison at all governed web widths and native device classes;
- state-machine tests for every state in section 5;
- server-contract and action-body parity tests proving no authority or payload drift;
- performance budgets in section 9;
- route/deep-link/back-stack preservation or an explicit approved successor;
- exact inventory entry updated to `removed`; and
- a bounded rollback path that does not restore a duplicated product shell.

## 14. Required verification matrix

| Gate | Required proof |
|---|---|
| Schema | Token/component schema validation; generated web/native snapshot parity; unknown role rejection |
| Color | Automated contrast matrix for every foreground/background/state pair in all appearance modes |
| Type | Font-load fallback, Dynamic Type AX5, 200% zoom, localization expansion, truncation negatives |
| Layout | Web `1024/1280/1440/1728`; iPhone compact portrait/landscape where supported; iPad compact/medium/expanded, portrait/landscape, Split View and Stage Manager |
| Input | Touch, keyboard-only, pointer/hover, screen reader/VoiceOver, voice alternative, drag/drop where offered |
| Components | Every primitive state and transition, focus restoration, disabled reason, destructive confirmation |
| Data state | Loading/empty/offline/stale/unauthorized/revoked/pending/retry/restored precedence and hard negatives |
| Trust | Audience/provenance fixtures, unknown and withdrawn sources, public/private tree separation, no hidden-field leakage |
| Mutation | Stable idempotency retry, `400` clear, `409` reconcile, transport/`5xx` persistence, account/session isolation |
| Migration | Codemod positive/negative/idempotency fixtures; legacy usage counts; no route/action/network diff |
| Performance | Web vitals, frame timing, list virtualization, cold/warm native start, bundle/token size |
| Restoration | App/browser restart in offline, ambiguous mutation, stale, revoked, and restored states |

Tests must assert behavior and semantics, not only snapshots or source strings. Visual regression is supporting evidence, not a substitute for interaction, accessibility, authority, or state tests.

The component gate includes a rendered DOM hit-target audit at every governed
web width. A fixture whose interactive bounding box is below `40×40` CSS pixels
must fail even when its visual row height is 36; native fixtures below `44×44`
points likewise fail. Catalog tests also reject every prohibited collapse in
section 4.1 and every missing principal/audience/provenance/state/action input.

## 15. Reference-flow artifacts still required

PD0 is not visually or interactionally accepted until the following artifacts exist. This contract intentionally does not generate them:

1. A token specimen and governed component catalog showing all appearances, densities, input modes, Dynamic Type/zoom behavior, and every state in section 5.
2. A single end-to-end reference journey for join/onboarding, home voice/typed entry, private team work, Suggested Work review, run/intervention, artifact review, verification, and Work Record update.
3. Reference flows for publishing a Work Object, browsing a Workstream, entering a Public Workspace, inspecting provenance, and correcting/withdrawing/revoking evidence.
4. Self, coworker, and network profile boundary flows with exact private/current-org/opted-in distinctions.
5. Work Search proposal/confirmation/results, exact-link contact, contact acceptance, blocking, grant revocation, and purge/unavailable outcomes—without enabling those features.
6. Agent attribution and intervention flows that distinguish human work, agent-derived output, reviewed influence, and unknown provenance.
7. Settings/privacy flows for inspect, correct, export, forget, consent, audience, accessibility, appearance, offline state, and account/session change.
8. A failure-state storyboard covering loading, empty, offline, stale, unauthorized, revoked, pending, retry, restored, destructive failure, and restart recovery.
9. Platform layouts for every journey at web `1024/1280/1440/1728`, representative iPhone compact width, and iPad compact/medium/expanded widths in portrait and landscape.
10. iPad keyboard map, focus order, pointer/hover/context-menu annotations, Split View/Stage Manager behavior, and drag/drop source/destination rules.
11. An annotated accessibility tree and spoken-order transcript for the key reference screens.
12. A content inventory for trust/provenance, unavailable, destructive, recovery, privacy, and consent language with owners and localization expansion proof.
13. Motion storyboards for voice/listening, pane transitions, mutation pending/recovery, and reduced-motion equivalents.
14. The updated legacy/removal inventory with exact component, route, literal, owner, replacement, codemod, test, and removal status.

Each artifact must show ordinary interaction, not isolated beauty frames. It must use realistic bounded data, include hard states, and distinguish what is implemented, default-off, unavailable, or still conceptual. Approval of a desktop frame does not approve iPhone or iPad behavior, and simulator screenshots do not replace real-device accessibility and interaction proof.

## 16. Completion boundary

This supporting contract is decision-complete for semantic roles, scales, component states, platform adaptation, governance, migration, and verification. PD0 remains incomplete until the reference-flow artifacts in section 15 are produced, reviewed across all three platform expressions, and linked to the legacy inventory and required evidence. No component implementation, broad migration, legacy deletion, or feature activation should cite this document alone as completion proof.
