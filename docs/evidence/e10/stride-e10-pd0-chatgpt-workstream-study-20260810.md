# STRIDE PD0 ChatGPT Workstream Study — 2026-08-10

Status: `research_complete_multiplayer_workstream_handoff_independent_critic_pass`

This is a read-only extension of the existing E10 PD0 comparative-design
study. It does not create a new design wave or implementation owner. It
deepens one question: how should STRIDE turn a conversation into durable,
reviewable, multiplayer work without becoming either a ChatGPT clone or a
Slack/LinkedIn collage?

## Evidence boundary

The study inspected the founder's authenticated ChatGPT web shell in ChatGPT
Work, Library, Scheduled, and Plugins. It opened no conversation or project
body, submitted no prompt, uploaded no file, connected no service, changed no
task, and retained no private chat, project, file, or task name in this
artifact. The four body-minimized interaction observations, exact surface,
access date, interaction job, controls, mutation/privacy exclusions, and
limitations are bound in
`stride-e10-pd0-chatgpt-workstream-interaction-manifest-20260810.json`, SHA-256
`98027ae638ee1f0742725754cbdddfc0e7e8642cf990bd43958208bed82048fe`.
The observed controls and layouts were cross-checked against current official
OpenAI documentation:

- [Get started with ChatGPT Work](https://learn.chatgpt.com/docs/get-started-with-work)
- [Projects and chats](https://learn.chatgpt.com/docs/projects)
- [Long-running work](https://learn.chatgpt.com/docs/long-running-work)
- [Scheduled tasks](https://learn.chatgpt.com/docs/automations)
- [Create or revise a slide deck](https://learn.chatgpt.com/use-cases/generate-slide-decks)
- [ChatGPT and Codex use cases](https://learn.chatgpt.com/use-cases)

The prior cross-product/mobile evidence and design thesis remain in
`stride-e10-pd0-comparative-design-study-20260810.md`. No native ChatGPT
iPhone/iPad account interior was available for this extension, so native
behavior is not inferred from the web shell.

## What the real work system does well

### 1. It separates conversation from delegated work

The Chat/Work surface switch makes a useful product promise. Chat is for an
answer, explanation, brainstorm, or short draft. Work is for a clear outcome:
a brief, analysis, recurring update, workflow, presentation, spreadsheet, or
other reviewable file. The Work landing surface reinforces that distinction
with one outcome-oriented composer, a model/effort control, project selection,
plugins, source attachment, and a separate usage meter.

**STRIDE implication:** do not make every message look like a job and do not
make every job look like a long message. A conversation may produce Suggested
Work; only explicit acceptance creates a durable Work Object and Run.

### 2. It binds substantial work to context before launch

The Work launcher permits a project choice before the task starts. Its source
menu combines local upload, the cross-chat Library, web search, connected
Drive, and other approved plugins. Official project guidance recommends a
project when work spans time, produces multiple outcomes, or reuses files and
sources; related chats share project instructions and sources while each
distinct outcome stays in a focused chat.

**STRIDE implication:** every accepted job must show its governed context at
launch: organization, private conversation or room, participants, accountable
human, sponsored agents, approved source set, audience, retention, and expected
deliverable. Context must be server-bound, not an implicit client selection.

### 3. It makes long-running work steerable

Official long-running-work guidance centers an outcome, constraints, and
verification criteria. In the desktop app, Goal mode exposes a progress row
that can pause, resume, edit, or clear the goal. Hosted ChatGPT Work on the web
is steered by continuing the same chat with added context, changed constraints,
questions, or status requests. Independent tasks use separate chats, and
overlapping write authority is explicitly discouraged. The desktop Goal
controls were not observed in this authenticated web inspection and are cited
only from the official platform-specific documentation.

**STRIDE implication:** a work card needs an honest state machine and an
intervention surface, not a spinner:

`Proposed -> Confirmed -> Queued -> Working -> Needs input -> Review -> Delivered`

Failure, cancellation, supersession, retry, and recovery are sibling terminal
or recovery states. The card must expose the current stage, completed stages,
next expected action, latest body-minimized progress, responsible actor, and
whether work continues when the app closes. Pause, change direction, answer,
retry, cancel, and review are explicit, separately accessible actions.

### 4. It preserves outputs outside the transcript

The rendered Library is a searchable cross-chat file surface with All, Images,
and Documents views, basic file metadata, upload, folders, and notes. It solves
an important problem: an output is not trapped in the message that created it.

**STRIDE implication:** the transcript is a provenance trail and collaboration
surface; Drive is the durable artifact home. Every supported deliverable needs
an inline rich preview, a stable artifact detail view, an exact source/run
relationship, and an authorized Save to Drive path. A file should be
discoverable from its chat, Work Object, project/workspace, owner, and Drive
record without being duplicated into incompatible copies.

### 5. It treats recurring work as a managed object

Scheduled has a dedicated management surface rather than hiding recurrence in
chat prose. The observed cards expose cadence, next run, active/paused state,
edit, pause, and additional actions. Official guidance distinguishes a
standalone task, which starts from its saved prompt, from a task in an existing
chat, which resumes that chat's context; it also recommends testing the prompt
and reviewing early runs.

**STRIDE implication:** recurrence is a typed schedule attached to a Work
Object or conversation, with owner, context, source freshness, destination,
approval rule, cost ceiling, next run, last result, and pause/revoke controls.
No background task may silently gain broader audience, publication, contact,
or provider authority than its accepted parent.

### 6. It makes capabilities discoverable at the point of work

The Work source menu searches plugins, files, folders, and skills. The Plugins
directory groups installed and available capabilities by job domain, including
documents, presentations, spreadsheets, design, data, communication, and
developer tools. Skills are described as reusable instructions for repeatable
tasks.

**STRIDE implication:** Scout and specialist agents should not be a separate
app directory or a magical @mention grammar. The composer and Suggested Work
card should reveal only capabilities allowed for the current person,
organization, conversation, source set, and effect class. Reusable Processes
are versioned, reviewable instructions with inputs, outputs, checkpoints,
provider route, cost policy, and human authority—not hidden prompts.

## Adopt, adapt, and reject

| Pattern | STRIDE decision | Reason |
|---|---|---|
| Chat vs outcome-bound Work | **Adopt** | preserves conversational ease while making substantial work explicit and reviewable |
| Project before launch | **Adapt** | project becomes governed workspace/object context with organization, audience, people, agents, sources, retention, and authority shown on the card |
| One central Work composer | **Adapt** | keep a calm launcher inside Work, but use a bounded New/Suggested Work flow rather than a second universal chat box on Home or Network |
| Model and plugin chooser | **Adapt** | users choose capability and quality/cost posture when useful; exact model/provider remains policy-bound and receipt-backed |
| Goal/progress controls | **Adopt** | pause, steer, answer, cancel, review, resume, and recovery belong on the durable run card |
| Cross-chat Library | **Adapt** | STRIDE Drive is the governed artifact home with provenance, ACL, preview, version, source, and Work Record relationships |
| Scheduled management | **Adopt** | recurrence is a visible managed object, never invisible prompt state |
| Plugins and skills | **Adapt** | capabilities and Processes are scoped, versioned, permissioned, costed, and auditable |
| One-person project model | **Reject as sufficient** | STRIDE must make multiplayer audience, roles, agent sponsorship, decisions, approvals, conflict, and consent first-class |
| Chat history as the primary information architecture | **Reject** | conversations are one Work surface; artifacts, runs, outcomes, evidence, people, and Network projections remain first-class objects |
| Generic composer with publication/contact authority | **Reject** | Network publishing, search, contact, join, approval, and external send require explicit server-minted actions and receipts |
| Provider brand as product identity | **Reject** | the visible product is STRIDE's governed work lifecycle; OpenAI is the controlling provider, not the information architecture |

## The multiplayer STRIDE synthesis

ChatGPT Work is fundamentally a person delegating a task to an assistant.
STRIDE's ownable extension is a governed group turning conversation into
attributable work with humans and agents collaborating under visible authority.

### Object grammar

1. **Conversation or room** — private human/agent discussion with exact
   participants and organization context.
2. **Suggested Work** — a reviewable proposal naming objective, deliverable,
   sources, owner, agents, checkpoints, audience, cost posture, and effects.
3. **Work Object** — the durable accepted scope and decision record.
4. **Run** — one provider or deterministic execution attempt with stage,
   progress, intervention, usage, idempotency, and recovery.
5. **Artifact** — versioned deliverable with rich preview, exact source/run
   lineage, Drive state, and review status.
6. **Outcome and Evidence** — the human-reviewed result and what supports it.
7. **Work Record candidate** — person-controlled contribution evidence.
8. **Network projection** — deliberately released, body-minimized public
   representation with revocation and provenance; never the private object.

### Card anatomy

Every work card on web, iPhone, and iPad must answer without opening a detail
sheet:

- What is being done and what will be delivered?
- Who requested it, who owns it, and which humans/agents are participating?
- Where is it happening and who can see it?
- Which sources, capability, quality posture, and cost policy are bound?
- What stage is it in, what just completed, and what happens next?
- Does anyone need to answer, approve, retry, pause, or review?
- Will it continue in the background, and when was the last durable update?
- Where is the current artifact, and is it saved to Drive?

Compact list cards show objective, owner, status, current stage, next action,
latest update, and artifact badge. The full detail exposes the stage timeline,
participants, sources, interventions, usage receipt, versions, and governed
actions. Active state copy replaces stale delivered copy everywhere.

### Platform expression

- **Desktop web:** Work uses a contextual list, main conversation/object canvas,
  and optional artifact/evidence inspector. The active run remains visible while
  a deliverable is reviewed. Library/Drive is one click from the artifact, not a
  detached file universe.
- **iPhone:** Work owns New. A conversation is full screen; the composer stays
  clear of the tab bar, keyboard, and home indicator. Active work collapses to
  an honest stage card with one primary action. Artifact preview is full-screen
  and returns to the exact message/run.
- **iPad:** Work earns the canvas through persistent list/detail and an optional
  preview/provenance inspector. Running work, conversation, and artifact can be
  understood simultaneously without three competing navigation systems.

## Deliverable acceptance under the OpenAI-only decision

The founder's controlling provider decision is OpenAI for every provider-backed
product lane. Existing Anthropic presentation failures are historical and must
not be retried. OpenAI's current official Work guidance explicitly treats
presentations, spreadsheets, briefs, recurring updates, workflows, and files as
reviewable outputs; its slide-deck guidance requires editable Google Slides or
PowerPoint, source/template preservation, visual/layout checks, and a completed
file returned for review.

That reference raises—not lowers—STRIDE's acceptance bar. A presentation cell
is complete only after an OpenAI-routed real run produces an editable deck and
the same exact artifact proves:

1. accurate, source-bound narrative and disclosed unknowns;
2. coherent slide system and useful visual hierarchy rather than text dumps;
3. editable PPTX or native Slides plus rendered PDF/preview where required;
4. zero clipping, overlap, broken fonts, accidental placeholders, or unsafe
   links across every slide;
5. rich web preview, native iPhone open/review, intentional iPad preview, and
   exact Save to Drive;
6. honest card stages, intervention/retry, background/restart, and terminal
   state across all list and detail surfaces; and
7. provider/project/model/cost, source, version, and Drive receipts with no
   publication or external send unless separately authorized.

The same matrix applies to research, documents, images, workbooks, and mixed
packages with deliverable-specific visual and semantic checks.

## Prototype and implementation handoff

The single Product Design/implementation owner should now freeze these
representative prototypes before broad migration:

1. private conversation -> Suggested Work -> accepted research run -> rich
   report -> review -> Drive;
2. private channel -> presentation request -> outline/checkpoint -> OpenAI deck
   run -> slide preview -> correction -> Drive;
3. scheduled project update -> active run -> needs-input intervention ->
   reviewed result;
4. iPad Work list/detail/inspector with an active run and artifact preview;
5. reviewed evidence -> Work Record candidate -> private Network preview ->
   explicit publication receipt, still fixture-only/default-off until PN/W6.

Acceptance requires rendered side-by-side web/iPhone/iPad evidence, normal and
adversarial state coverage, five-user comprehension checks for place/audience/
status/next action, accessibility and performance evidence, and an independent
design critic on the exact prototype/system packet. Only then should PD1
serialize the broader product migration.

## Current verdict

The existing STRIDE direction is right to keep a stable five-destination shell
and to make Work and Network projections of one verified work graph. The deeper
ChatGPT study sharpens the missing product layer: Work must be a durable
operating system for scoped outcomes, not simply private chat with agent cards.

The next accepted design slice is therefore the card lifecycle and
cross-platform Work object system—context at launch, honest stage/next action,
steering and recovery, first-class artifact/Drive, recurring work, and the
governed Work Record bridge. That is the multiplayer advantage; the public
Network is the selective evidence-backed consequence of work, not a separate
feed bolted onto it.

## Independent critic

`/root/w0_plan_audit` reviewed exact study SHA-256
`5bd071dbc68252bebfeb81dead39c411a95abc68e0f59e5daac26e0a1fa93687`
and interaction-manifest SHA-256
`98027ae638ee1f0742725754cbdddfc0e7e8642cf990bd43958208bed82048fe`.
Verdict: **PASS**, with no blocker or major. The critic independently verified
the sidecars/JSON/plan references, body-minimized four-surface interaction
evidence, platform-specific desktop Goal versus web Work behavior, official
OpenAI source alignment, multiplayer synthesis, single ownership,
OpenAI-only/no-Anthropic-fallback sequencing, default-off boundaries, and
completion honesty. This paragraph is a one-way record of the verdict against
the prior reviewed SHA; it does not self-attest the current receipt bytes.
