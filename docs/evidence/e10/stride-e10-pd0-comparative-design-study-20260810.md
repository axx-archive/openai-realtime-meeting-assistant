# STRIDE PD0 Comparative Product-Design Study — 2026-08-10

Status: `research_complete_design_thesis_ready_independent_critic_pending`

This is a read-only evidence and synthesis artifact inside the existing E10
PD0/PD1 wave. It does not authorize code changes, provider spend, production
mutation, publication, cohort activation, or collection. One Product Design
implementation owner remains responsible for the target system and serialized
cross-platform integration.

## Evidence boundary

The desktop study inspected real rendered authenticated new-chat/workspace
shells for ChatGPT web, Claude web plus the installed macOS app, and Slack. It
did not open or reproduce conversation/message bodies and performed no writes.
Notion and Linear product interiors were not authenticated, so only their
public positioning and depicted shells were considered.

Local screenshot packet:

`/Users/ajhart/.codex/visualizations/2026/08/08/019fe1fc-ee80-7e92-a889-1e14e91f693e/stride-product-study-20260810/`

Manifest SHA-256:

`8b041753d0d3a43c72bd92363999d36232c15e8745f778dd88b95bd6f8ddc2a8`

Selected evidence:

- `chatgpt-new-chat-main-crop.png` — calm primary canvas and composer;
- `chatgpt-900-responsive.png` — narrower expanded-sidebar clipping risk;
- `claude-macos-app.png` and `claude-new-chat-main-crop.png` — explicit work
  affordances, richer result/tool posture, and a blocking promotion contrast;
- `slack-authenticated-shell-header-crop.png` and
  `slack-900-responsive-header.png` — persistent workspace/person hierarchy,
  global search, Activity, and Files;
- `notion-public-home.png` and `linear-public-home.png` — public-only evidence,
  never treated as authenticated interior behavior.

Mobile/tablet evidence was limited to current public App Store galleries,
official product documentation, Apple HIG, current Buzz source positioning, and
current STRIDE native source. Authenticated/local ChatGPT, Claude, Slack,
Discord, and Buzz iOS/iPad interiors were unavailable; no native behavior was
inferred from inaccessible screens.

Primary mobile/tablet references:

- ChatGPT iPad: https://apps.apple.com/us/app/chatgpt/id6448311069?platform=ipad
- Claude iPad: https://apps.apple.com/us/app/claude-by-anthropic/id6473753684?platform=ipad
- Slack iPad: https://slack.com/blog/news/ipad-app-new-look-improved-functionality
- Discord iPad: https://apps.apple.com/us/app/discord-talk-play-hang-out/id985746746?platform=ipad
- Linear mobile: https://linear.app/docs/get-the-app
- Buzz: https://github.com/block/buzz/blob/main/README.md
- Apple tab bars: https://developer.apple.com/design/human-interface-guidelines/tab-bars
- Apple sidebars: https://developer.apple.com/design/human-interface-guidelines/sidebars

## Comparative teardown

| Product | Strongest observed pattern | Adopt / adapt | Reject or boundary |
|---|---|---|---|
| ChatGPT | quiet single-task canvas, explicit new conversation, thumb-ready multimodal composer, low visual noise | **Adopt** calm focus and latest-thread continuity; **adapt** the composer into a scope-aware STRIDE SignalControl | reject unbounded history sprawl, hamburger-only primary IA, and centered phone-like iPad composition; never let an expanded sidebar clip the canvas/composer |
| Claude | calm conversation canvas with richer result/tool affordances and clearer work-output posture | **Adopt** deliverables as first-class conversational results; **adapt** tool/model choices into governed capability and provenance disclosure | reject promotional overlays that block core work and a separate “AI work universe” |
| Slack | explicit workspace/person context, global search, persistent Activity/Files, intentional iPad list-detail hierarchy | **Adopt** contextual hierarchy and iPad continuity; **adapt** Activity into typed audit/events and Files into governed artifacts | reject channel sprawl as the entire work model, duplicate workspace shells, and implicit identity switching |
| Discord | immediate spatial orientation across place/channel/conversation and strong presence/joinability | **Adapt** legible place and participant context | reject icon-heavy server rail, entertainment density, mixed private/public identity, and social presence as authority |
| Linear | compact action-oriented navigation, structured issue/detail/activity, explicit create/retry state, mobile offline awareness | **Adapt** typed Work Object -> Run -> Artifact -> Outcome -> Evidence and durable retry state | reject issue-tracker ontology as STRIDE's shell and a global center action tab |
| Notion | multiple views over one system of record; capture/find/automate framing | **Adapt** Work and Network as governed projections over one verified contribution authority | reject generic blank-canvas/database complexity and user-authored structure as an authority shortcut |
| Buzz | humans and agents in rooms, signed event-log thesis, portable persona/runtime identity separation | **Adopt conceptually** accountable agent identity and room-as-record boundaries already reflected in STRIDE contracts | reject unverified mobile layout claims, agent/human authority collapse, prompt-only delegation, and agent-authored memory as organizational truth |

## Distinctive STRIDE design thesis

**One verified work graph, five lenses.** STRIDE is not a chat product beside a
professional network. Home, Work, Network, Work Search, and You are five stable
lenses over one permissioned object graph. The object identity persists while
its audience and projection change only through explicit governed transitions.

- **Home** is voice-first Signal: orientation, attention, and a path into the
  latest relevant work. It is not a second text-composer surface.
- **Work** owns private conversations, rooms, Suggested Work, runs, artifacts,
  review, Work Record candidates, and creation of chats/channels/work objects.
- **Network** shows only deliberate server-published Public Workspaces, typed
  Work Objects, Workstream entries, and opted-in profiles backed by disclosed
  evidence. It has no generic post box.
- **Work Search** is purpose-bound discovery over authorized evidence and
  published projections, with abstention, reason, and contact controls.
- **You** owns private identity, organization context, Work Record review,
  publication settings, consent, privacy, and agent sponsorship.

The visible bridge is not “switch apps” or “copy to feed.” It is:

`Private Work -> Reviewed Evidence -> Work Record -> Selective Network Projection`

Every boundary change names the object, audience, controller, provenance,
status, and resulting receipt. Human, agent, and system authorship remain
structurally and visually distinct at every stage.

## Unified IA and navigation grammar

| Platform | Global navigation | Context and detail | Creation and transition |
|---|---|---|---|
| Desktop web | stable five-destination rail; it may compact but never becomes an unrelated product shell | collapsible contextual list/sidebar, main canvas, optional evidence/provenance inspector; context collapses before the canvas becomes unusable | New lives inside Work; sheets/panels own bounded creation; deep links preserve object, audience and selection |
| iPhone | five bottom destinations, no overlapping secondary shortcut band; destination stacks persist | one primary task at a time; thread/room/detail is immersive full-screen; progressive disclosure in native sheets | Home remains voice-first; Work exposes New Conversation; audience and organization are named before submit; composer owns only thread-safe actions |
| iPad | adaptive leading sidebar for the same five destinations | true list-detail for Work, Work Search and Network; optional third provenance inspector only when a selected object warrants it; selection survives resize/rotation | form sheets/popovers are size-class correct; no stretched 820px phone gateway; hardware keyboard, pointer, context menu and drag/drop are additive, never exclusive |

Destination switching does not reset the current stack. List-to-detail uses
native selection/push. Creation and review use sheets. Thread/room work may be
immersive. Private-to-published is an explicit review transition with a result
receipt, never an optimistic decorative animation.

## Shared system

The target design-system handoff must freeze:

- one typography, color, spacing, density, radius, elevation, icon and motion
  vocabulary with semantic—not raw—tokens;
- one identity system for human, agent, organization, workspace and system
  actors, including sponsor/delegation and provenance;
- one surface taxonomy: global shell, contextual navigation, work canvas,
  inspector/sheet/popover, immersive work, trust disclosure, transient status,
  and recovery;
- closed components for messages, Suggested Work, runs/interventions,
  artifacts, outcomes, evidence, Work Record, Public Work Object, profiles,
  search/contact, moderation and unavailable/revoked states;
- platform-native layout adapters without divergent semantics or authority;
- motion only for voice state, continuity, pending effect and recovery, with
  reduced-motion parity and no popularity/performance theater.

## Representative end-to-end direction

### Work loop

Home voice signal -> latest/private conversation -> Suggested Work proposal ->
explicit accept/edit/dismiss -> durable run with stage/progress/intervention ->
rich artifact preview -> review/correction/verification -> Work Record candidate.

Acceptance requires private authority, exact participant/agent identity,
current status and progress, honest failure/retry, offline/restart recovery,
artifact/open/save rules, and zero publication side effects.

### Network loop

Reviewed Work Record -> field/evidence selection -> private publication preview
-> explicit release -> typed Public Work Object or Workspace projection ->
Workstream/provenance/profile inspection -> offer help or purpose-bound contact
-> governed private work context.

Acceptance remains fixture-only/default-off until PN/W6 authority is qualified.
Moderation and publication parents must mount zero external child behavior while
off. Network never exposes the private source, MyMind, hidden organization data,
or generic composer authority.

## Cohesion and rendered acceptance handoff

The implementation cannot claim acceptance until exact side-by-side evidence
shows both loops across web 1024/1280/1440/1728, representative compact/large
iPhones, and iPad compact/medium/expanded portrait/landscape/Split View/Stage
Manager. Required states: normal, loading, empty, offline, stale, unavailable,
revoked, pending, failed, and restored.

Required checks:

1. zero duplicate global shells, shortcut bands, or competing navigation;
2. no composer, send, attachment, tab, or critical action overlaps keyboard,
   home indicator, safe areas, or other navigation;
3. exact location/audience comprehension and stable destination vocabulary;
4. users can move Work -> evidence -> Network -> purpose-bound collaboration
   without a universe switch or hidden route;
5. persistent route, draft, selection, source, authority, and pending-operation
   state through resize/orientation/background and allowed continuation;
6. human/agent/system authorship and private/org/public projection are not
   visually or structurally interchangeable;
7. iPhone one-handed reach and progressive disclosure; intentional iPad
   list-detail/inspector use rather than widened phone cards;
8. keyboard/focus, pointer/hover, touch, Dynamic Type, VoiceOver, reduced
   motion, offline/retry and performance budgets;
9. all unavailable/parent-off routes mount zero child projection/provider
   behavior; and
10. independent design critic PASS on the exact comparative packet, thesis,
    prototypes, rendered audit and usability evidence before broad migration or
    legacy-shell removal.

## Current verdict

The existing five-destination STRIDE IA and privacy posture are directionally
sound. The uploaded iPhone overlap proves that source/static tests did not close
visual acceptance, and current iPad gateway cards do not yet earn the tablet
canvas. The next accepted design slice is not a hamburger redesign. It is:

1. voice-only Home without a competing text composer or shortcut band;
2. Work-owned creation for private chat/channel/work objects;
3. persistent iPad Work list-detail plus optional provenance inspector;
4. one governed Work Record bridge into Network; and
5. exact rendered cross-platform evidence and an independent critic before
   implementation acceptance.

No code, account, message, provider, production, or user-data mutation was part
of this study.
