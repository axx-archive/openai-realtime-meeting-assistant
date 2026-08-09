# STRIDE E10-PD0 primary-source research and design decisions

**Date:** 2026-08-09

**Status:** design research and bounded decisions; not PD0 acceptance, implementation evidence, release authority, or device acceptance

**Scope:** desktop web, native iPhone, and native iPad as three first-class expressions of one STRIDE product
**Sources:** official product, help, engineering, and platform documentation only; accessed 2026-08-09

## Decision

STRIDE will not look or behave like a social feed attached to a workplace. Its
ownable product thesis is:

> **STRIDE is a live work graph you can speak into. The Signal opens the floor,
> governed work leaves an attributable trail, and provenance—not popularity—
> determines what can travel.**

This creates one cross-platform loop:

`Signal or conversation -> governed action -> artifact/outcome -> reviewed proof -> person-controlled Work Record -> deliberately released Work Object -> relevant collaboration`

The loop is shared. Its composition is native to each platform. Desktop web is
a commandable multi-pane workbench; iPhone is a voice-first, one-handed
continuation surface; iPad is an adaptive workroom with persistent context. The
interfaces share semantics, trust language, tokens, and state—not a pixel grid.

## Research boundary

This brief answers which durable interaction principles STRIDE should adopt and
which familiar patterns it should refuse. It does not select external
participants, copy screenshots or trade dress, authorize instrumentation,
change product code, or satisfy physical-device, accessibility, privacy, or
release gates.

The current repository already contains strong but fragmented ingredients:

- desktop web has a top bar, tool rail, Scout rail, Board, Drive, artifact, chat,
  room, and W2 product surfaces in `index.html`; several surfaces have their own
  shell or sidebar, so rendered PD0 audit must distinguish purposeful context
  from duplicate navigation;
- native has a deliberately voice-first Canvas root and stack navigation with
  no generic tab navigator in `mobile/src/navigation/RootNavigator.tsx`; that is
  valuable product identity, but route presence is not yet a unified IA; and
- revision-2 Public Workspaces, Workstream, Explore, public profiles, and public
  trust controls remain design/default-off work, not current rendered proof.

## Primary-source findings

### Meta: feed control and platform adaptation, not social mechanics

Official Threads material shows two durable ideas. First, a user-selected feed
can be reverse chronological and bounded to explicitly followed sources.
Second, web can use available width for multiple independently chosen columns
while mobile stays simpler. Threads also makes federation an explicit opt-in
with privacy consequences instead of treating public reach as reversible local
state.

Primary sources:

- [Threads: dedicated reverse-chronological fediverse feed](https://about.fb.com/news/2025/06/its-now-easier-see-more-fediverse-content-threads/)
- [Threads web: single-column continuity plus optional multi-column composition](https://about.fb.com/news/2025/04/new-features-threads-web-experience/)
- [Threads: persistent feed choice and reply/quote controls](https://about.fb.com/news/2025/03/new-threads-features-more-personalized-experience-you-control/)
- [Meta: federation expands distribution and changes privacy/control](https://about.fb.com/news/2024/06/what-is-the-fediverse/)

Adopt:

- user-chosen feed modes with an obvious strict-chronology path;
- desktop columns only when each column represents a stable user purpose;
- explicit audience/distribution state before content leaves its governing
  boundary; and
- mute, block, reply, and source controls adjacent to the affected object.

Reject:

- the universal status composer;
- engagement ranking, likes, repost counts, follower counts, creator-growth
  prompts, trending-topic pressure, public community membership as identity,
  and activity-derived badges;
- copying Threads' sparse visual language, feed card proportions, icons, or
  column chrome; and
- federation as a PN1 requirement. Open-web or interoperable distribution is a
  later, separately governed boundary.

### LinkedIn: legible professional context, but not popularity-shaped identity

LinkedIn demonstrates the value of consistent global navigation, structured
professional profiles, explicit verification scope, persistent feed choice,
semantic design tokens, and accessibility infrastructure. Its own documentation
also makes visible why STRIDE must diverge: LinkedIn relevance uses profile,
activity, network, dwell, engagement, prominence, and follower signals. Those
signals are incompatible with STRIDE's evidence-not-popularity constitution.

Primary sources:

- [LinkedIn relevance and member feed controls](https://www.linkedin.com/help/linkedin/answer/a1339724)
- [LinkedIn persistent Recent versus Relevant feed choice](https://www.linkedin.com/help/linkedin/answer/a1480504/sort-feed-by-most-relevant-and-most-recent-posts?lang=en)
- [LinkedIn Page verification and its explicitly limited meaning](https://www.linkedin.com/help/linkedin/answer/a6275638)
- [LinkedIn engineering: semantic tokens and incremental design-system migration](https://www.linkedin.com/blog/engineering/product-design/updating-linkedins-ui)
- [LinkedIn engineering: automated accessibility testing](https://www.linkedin.com/blog/engineering/accessibility/automated-accessibility-testing)

Adopt:

- structured, scannable professional context rather than biography-first
  storytelling;
- verification labels that state exactly what was verified and explicitly do
  not imply endorsement or universal truth;
- one persistent feed preference rather than a transient hidden toggle;
- semantic token infrastructure and incremental migration that avoids a
  mixed-generation “Frankenstein” product; and
- accessibility checks as design-system and CI responsibilities, not a final
  visual review.

Reject:

- self-description, employer prestige, network size, profile activity, dwell,
  clicks, follower count, or engagement as reach, quality, reputation, search,
  or Work Record signals;
- collapsing people search, recruiter intent, content discovery, and public
  Work Objects into one search box or ranking system;
- generic blue professional-network styling, badge inflation, résumé hierarchy,
  connection-count theater, and premium eligibility as trust; and
- copying LinkedIn's top navigation or dense card anatomy.

### OpenAI Codex: thread-scoped work, reviewable evidence, and continuation

The Codex app treats long-running agent work as separately scoped threads
organized by project, provides explicit review of changes and evidence, and
supports continuation across desktop and mobile while keeping files,
credentials, and permissions on the trusted machine. Its Automations return
results to a review queue rather than silently converting background work into
accepted work.

Primary sources:

- [OpenAI: introducing the Codex app](https://openai.com/index/introducing-the-codex-app/)
- [OpenAI: Codex evidence, isolated tasks, and review](https://openai.com/index/introducing-codex/)
- [OpenAI: continuing active threads and approvals from mobile](https://openai.com/index/work-with-codex-from-anywhere/)

Adopt:

- one durable thread or work context per meaningful objective;
- visible current state, pending question, approval, output, and evidence;
- review queues for agent-created proposals and artifacts;
- context-preserving continuation across devices without copying underlying
  secrets or widening authority; and
- clear separation between live pairing, asynchronous delegation, and finished
  reviewable output.

Reject:

- exposing agent orchestration complexity merely because it exists;
- treating agents as peers with human identity, consent, governance, or social
  rights;
- terminal, diff, token, branch, worktree, and code-review metaphors for people
  who are doing general work; and
- making a task inbox, command palette, or multi-agent dashboard the product's
  emotional center. STRIDE's center remains the Signal and the work itself.

### Small comparison set: Slack, Notion, and Linear

#### Slack

Slack's durable contribution is the continuity between a conversation, a live
huddle, its notes/canvas, and the channel or DM that retains the context. Its
accessibility work treats focus restoration, screen-reader announcements,
captions, reduced motion, density, and simplified layout as product behavior.

Sources: [huddles and persistent canvas](https://slack.com/help/articles/4402059015315-Use-huddles-in-Slack), [accessibility capabilities](https://slack.com/help/articles/4455747966739-Accessibility-in-Slack), and [current accessibility changelog](https://slack.com/help/articles/50668520513939-Accessibility-changelog).

- **Adopt:** conversation-bound realtime work; notes and artifacts that survive
  the call; captions; explicit focus behavior; density and motion preferences.
- **Reject:** channel sprawl, unread-count anxiety, reaction totals as proof,
  app-directory clutter, and a universal message stream as the primary IA.

#### Notion

Notion demonstrates discoverable hierarchy, collapsible contextual navigation,
deep linking, keyboard jump, and desktop drag/drop.

Sources: [sidebar navigation](https://www.notion.com/help/navigate-with-the-sidebar) and [keyboard shortcuts](https://www.notion.com/help/keyboard-shortcuts).

- **Adopt:** a collapsible contextual sidebar on wide canvases; visible
  hierarchy; direct links; keyboard jump; reversible desktop organization.
- **Reject:** infinite nesting, “everything is a page,” block-editor identity,
  and carrying a desktop sidebar unchanged onto iPhone.

#### Linear

Linear demonstrates outcome-bound Projects, high-signal properties, compact
views, and fast keyboard navigation.

Sources: [Projects as outcome-bound work](https://linear.app/docs/projects), [team pages and durable context](https://linear.app/docs/default-team-pages), and [display controls](https://linear.app/docs/display-options).

- **Adopt:** clear outcomes, accountable leads, compact state, user-selectable
  density, filters, and keyboard paths.
- **Reject:** issue-tracker language as STRIDE's content model, velocity as a
  person metric, priority theater, and status transitions that hide the human
  conversation or evidence.

### Apple platform guidance: expression must follow the device

Apple's current guidance supports adaptive hierarchy rather than a stretched
phone layout: sidebars need space and should yield to more compact navigation
on iPhone; deep hierarchies can use split views on iPad; windows must adapt to
multitasking sizes; accessibility requires alternatives to gestures, scalable
type, VoiceOver/full-keyboard support, and reduced motion.

Primary sources:

- [Apple HIG: sidebars](https://developer.apple.com/design/human-interface-guidelines/sidebars)
- [Apple HIG: layout](https://developer.apple.com/design/human-interface-guidelines/layout)
- [Apple: multitasking and adaptive window sizes](https://developer.apple.com/documentation/uikit/multitasking-on-ipad-mac-and-apple-vision-pro)
- [Apple HIG: accessibility](https://developer.apple.com/design/human-interface-guidelines/accessibility)
- [Apple HIG: motion](https://developer.apple.com/design/human-interface-guidelines/motion)
- [Apple HIG: playing audio](https://developer.apple.com/design/human-interface-guidelines/playing-audio)

These sources are platform constraints, not a license to reproduce Apple's
materials, ornament, or current visual fashion.

## STRIDE product-language decisions

### 1. Information architecture

One semantic IA spans all platforms:

| Destination | Purpose | Includes | Excludes |
|---|---|---|---|
| **Home** | Begin or resume meaningful work | Stride Signal, active conversations, rooms, current work, approvals requiring the person | universal text post composer, engagement feed |
| **Work** | Private organizational execution | Chat, meetings, Board, Drive/artifacts, Scout work, organizations and current workspace context | public discovery, private mind exposure |
| **Network** | Deliberately released work and governed collaboration | Workstream, Public Workspaces, public profiles, provenance, collaboration; Explore only after its gate | people/recruiter intent, private source search, popularity ranking |
| **Work Search** | Purpose-bound people discovery | visible interpretation, current published projections, why/unknown, contact boundary | feed retrieval, browsing private members, generic Explore |
| **You** | Person-controlled identity and policy | Work Record, profile/public presence, organizations, settings, privacy, export/correction/delete | employer dossier, scalar score |

Organization/account context, global command/search, notifications, and current
authority are shell utilities, not content destinations. Work Search stays
visibly distinct from object discovery even if both are entered from Network.

### 2. Platform shells

#### Desktop web: the workbench

- Persistent global rail for Home, Work, Network, Work Search, and You.
- Resizable contextual sidebar for the current destination.
- Primary canvas for the selected conversation, room, board, artifact,
  workspace, Work Object, or profile.
- Optional inspector for Scout, provenance, approvals, participants, or object
  details. It never becomes a second global navigation.
- Multi-column Workstream is user-created and purpose-labeled, not default
  density. Deep links, keyboard traversal, hover disclosure, drag/drop, and
  simultaneous work are first-class.

#### Native iPhone: the voice-first continuation surface

- Preserve the Canvas and Stride Signal as Home's emotional and interaction
  center. Home has no universal typing field; typing belongs inside a selected
  thread, Work Object, request, or search flow.
- A bottom-reach Deck exposes the same semantic destinations without turning
  the Signal into a generic floating action button.
- Use stack navigation, sheets, explicit back state, thumb-reachable primary
  actions, interruption-safe drafts, and visible upload/run/approval recovery.
- Show one primary task per screen. Secondary provenance and controls use a
  sheet or disclosure view, never an invisible gesture-only path.

#### Native iPad: the adaptive workroom

- Use a sidebar plus content list/canvas plus optional inspector when the
  available width supports it; collapse progressively to two panes and then a
  stack rather than stretching or hiding phone cards.
- Preserve the Signal as a compact, available voice control and live-state cue,
  not a mostly empty full-screen composition when a work context is open.
- Support portrait and landscape, Split View, Stage Manager widths, pointer,
  hover, context menus, external keyboard, focus, drag/drop, native share/files,
  and reconnect/resume.
- Keep selection and draft state coherent as panes appear, disappear, resize,
  or move between windows.

### 3. Content and trust grammar

Every public or cross-boundary object uses one compact **provenance strip**:

1. principal class and visible human/agent identity;
2. accountable human when an agent acted;
3. source class and current revision;
4. audience and publication state;
5. exact verification label and its limited meaning;
6. correction/dispute/revocation state;
7. **Why this appeared** and **What is unknown**; and
8. View As, pause, mute/block, inspect, correct/dispute, export/delete controls
   when authorized.

The collapsed strip is readable, not a wall of badges. Expansion reveals exact
refs and history. Color never carries trust by itself. “Verified” never means
endorsed, accurate in every respect, popular, or high quality.

Human messages, agent contributions, system events, Work Objects, artifacts,
and evidence cards have distinct semantic components. They may share spacing,
type, and surface tokens, but cannot collapse authorship or authority.

### 4. Token and component implications

- Keep STRIDE Ember as the scarce accent for the live Signal, current focus,
  primary governed action, and meaningful state transition—not every link,
  badge, or decoration.
- Use semantic color roles (`canvas`, `surface`, `raised`, `ink`, `muted`,
  `focus`, `action`, `warning`, `danger`, `verified`, `revoked`) with contrast
  contracts; no trust meaning depends on hue.
- Use a shared type role system, spacing scale, control heights, radii,
  elevation levels, icon semantics, and density modes. Platform fonts and
  metrics may differ while roles stay equivalent.
- Reserve shadows/material elevation for real spatial hierarchy: overlay,
  inspector, sheet, media stage, or dragged object. Borders and spacing express
  ordinary grouping.
- Custom icons must belong to one STRIDE symbol family; use platform symbols
  where convention is stronger. Do not import another product's icons.
- Components require semantic contracts for empty, loading, offline, stale,
  unauthorized, revoked, pending approval, retry, and restored states before
  visual variants.

### 5. Motion and sound

- The Stride Signal may breathe while listening, thinking, or speaking, with a
  nonanimated equivalent and explicit textual/assistive state.
- Motion explains continuity: opening a work context, moving an approved item
  into Board, expanding provenance, or handing off between panes.
- No infinite decorative motion, engagement confetti, parallax, auto-advancing
  cards, or springy movement on authority/destructive actions.
- Reduced Motion replaces travel/scale/depth transitions with short fades or
  immediate state changes and suppresses the Signal's repeated pulse.
- Audio respects silent mode, output changes, interruption, captions, and
  visible alternatives. Sound never provides the only evidence that recording,
  approval, failure, or publication occurred.

### 6. Accessibility baseline

The unified system requires:

- complete keyboard operation and visible focus on web and iPad;
- VoiceOver labels, roles, values, rotor/heading structure, announcements, and
  reliable focus restoration on iPhone and iPad;
- Dynamic Type without clipped authority, provenance, or action labels;
- platform-appropriate minimum targets, with at least 44 pt touch targets on
  iPhone/iPad and at least 40 CSS px interactive targets on web;
- alternatives for every swipe, drag, hover, long-press, pointer, and voice
  action;
- captions/transcripts and speaker identity for realtime media where allowed;
- 200% web zoom, high contrast, non-color state cues, reduced motion, and
  content that remains understandable at compact density; and
- accessibility semantics derived from the same component state machine as the
  visual UI, not separately authored optimistic labels.

## Durable patterns versus superficial imitation

| Durable and adopted | Superficial or constitutionally incompatible |
|---|---|
| user-controlled chronology and source filters | For You ranking, trending pressure, social counts |
| adaptive wide-screen panes with compact collapse | copying another product's columns or sidebar geometry |
| structured identity with exact verification meaning | résumé-first profile, badges, prestige, follower graph |
| persistent thread/work context and reviewable output | agent spectacle, orchestration chrome, terminal metaphors |
| conversation-to-artifact continuity | channel sprawl and reaction-driven proof |
| visible hierarchy, deep links, keyboard jump | infinite nesting and page-as-everything |
| outcome-bound work with accountable owners | issue-tracker language and velocity-as-worth |
| semantic tokens and staged migration | color/radius/font reskin over duplicate shells |
| platform-native navigation and input | stretched iPhone UI on iPad or web fallback |

## PD0 follow-on decisions and evidence

This research settles the thesis, high-level IA, shell direction, trust grammar,
and adopted/rejected pattern set. PD0 still requires evidence before it can
PASS:

1. Rendered current-state audit at desktop widths 1024, 1280, 1440, and 1728;
   iPhone compact/large portrait plus landscape media; and iPad portrait,
   landscape, Split View, and Stage Manager widths.
2. A complete route/surface parity and deprecation matrix. Every surface gets
   `equivalent_capability`, `intentional_platform_expression`, or an explicit
   deferral with owner and exit gate.
3. High-fidelity interactive references for Home/Signal, private team work,
   publish Work Object, Workstream, Public Workspace, provenance, all profile
   types, collaborate, Work Search/contact, agent controls, settings/privacy,
   and failure/revocation.
4. A semantic component inventory and token contract with migration ownership,
   adapters/codemods where useful, and removal gates for duplicate shells.
5. Normal-interaction accessibility and performance proof: navigation,
   scrolling, keyboard/focus, hover, touch, dynamic type, reduced motion,
   VoiceOver, offline/retry/revocation, long lists, media, and continuation.

## External evidence that remains open

No desktop simulator or static reference can close these gates:

- real iPhone reachability, interruption/background, orientation, VoiceOver,
  Dynamic Type, reduced motion, media route change, and restrictive-network use;
- real iPad portrait/landscape, Split View and Stage Manager resizing, external
  keyboard, pointer/hover/context menus, drag/drop, native share/files, safe
  areas, disconnect/reconnect, VoiceOver, Dynamic Type, and sustained canvas use;
- TestFlight distribution for the exact final native commit/build and intended
  groups; and
- independent accessibility/privacy acceptance and restrictive TURN/WebRTC
  evidence for the exact release.

Until those exist, this brief is `primary_source_research_complete` only. It is
not `PD0_passed`, `PD1_implemented`, `device_accepted`, or `release_accepted`.
