# Spectacular OS — Product Lead Deliverable

**Date:** 2026-07-03 · **Author:** Product Lead (cross-pillar architecture, journeys, orchestration, idea triage) · **Wave:** Spectacular OS
**Companion briefs:** `spectacular-os-context.md` (mission + system map), `wow-roadmap.md` (product thesis + backlog), `codex-goal-workflows.md` (the /goal loop + control/execution boundary), `bi-os-design.md` + `first-class-chat-design.md` (prior design language).

> This document owns the *system composition* — how the four mandated pillars interlock into one product, the user journeys that prove it, and which extra ideas ride the wave. The other four specialists own the depth of their pillars (RTC stability, the tool-suite content, the runner engine internals, the visual/interaction design). Where I name a mechanism they own, I name it as a *dependency and a contract*, not a final spec — I am asserting the seam, they are asserting the implementation.

---

## 0. The one thing to get right

Bonfire already has every organ of an intelligent company: a transcript funnel, a brain, a board, a decision ledger, packages, agent threads, a codex sidecar with approval gates, notifications, channels, two Scout voices. **The organs work. They are not wired into one nervous system.** The 2026-07-02 simulation proved this exactly: "the intelligence half genuinely works — the delivery half was broken," value landed for one person out of four, because the server finished in 35 seconds and three of four employees never saw it.

So the spectacular-OS wave is **not a feature wave. It is a wiring wave.** The mandate ("push from very good to spectacular end-to-end") is a demand that *the same work be reachable, visible, and returnable from every door* — voice, text, menu — and that *the intelligence the company already produces stop leaking out the last mile*. Everything below serves that.

The single sentence I want every specialist to design against:

> **Any request, spoken or typed or clicked, becomes staffed work that returns — visibly, to everyone who needs it, in the place it was born — and the company's memory keeps only what mattered.**

That sentence contains all four pillars. WebRTC is *"spoken"* being trustworthy. Scout Realtime-2 is *"any request, spoken"*. The tool suite + /goal is *"becomes staffed work that returns"*. Design excellence + slop quarantine is *"visibly… in the place it was born… keeps only what mattered."* If a journey below can't be traced back to that sentence, it doesn't ship this wave.

---

## 1. Product thesis for this wave

**"Spectacular end-to-end" means:** a request never dies in a channel, a finished result never hides on the server, a door never leads to a different pipeline than the door next to it, and the knowledge base never fills with slop. The company *feels like it is thinking* because work you started by talking finishes as an artifact you can see, that Scout can then read back to you, that advanced a package on a board you're looking at — a closed loop with no dead ends.

The one-sentence promise per pillar:

| Pillar | The promise (what the user feels) | The proof (the moment nobody's seen a meeting tool do) |
|---|---|---|
| **1. First-class WebRTC** | *"I join from anywhere, it just works, and I look and sound like the best version of me."* | Two people join the same room simultaneously — one desktop, one phone — while a third is already screen-sharing, and nothing flickers, drops, or double-negotiates. Settings honestly says "RNNoise suppression: on, this device." |
| **2. Scout Realtime-2 — she can do it all** | *"I ask out loud and it happens — she opens the thing, tells the person, posts the note, runs the drill, and reads me back the answer."* | "Scout, tell Tyler I moved Nimbus to grill, then grill me on the unit economics." Scout posts as you (disclosed), fires the notification, flips into a hostile-VC persona, and starts asking — all from one spoken sentence. |
| **3. Tool suite + /goal pipeline** | *"Every tool is the same loop: I name a goal, the company staffs it, reviews it, gates it, and hands me back exactly what I asked for."* | You click "Comp Set" from a thread's quick-select, and a multi-agent goal loop decomposes → assigns → executes → reviews-against-goal → gates → **returns the artifact into the very thread you clicked from**, with a readable card, not a raw prompt string. |
| **4. Design excellence + slop quarantine** | *"It's the most beautiful tool I use, on my laptop and my phone, and it never gets cluttered with junk."* | The tab says **BonfireOS**. Every state transition has intention. And in the morning, a "Quarantine" tray shows you the three things the OS decided weren't worth remembering — reviewable, restorable, auto-expiring — so the memory stays sharp instead of snowballing. |

**The through-line:** pillars 2 and 3 are the *same pipeline seen through two doors* (voice and menu/text). Pillar 1 makes the voice door trustworthy enough to be the *primary* door. Pillar 4 makes the return trip *legible* (a beautiful, honest surface) and *sustainable* (slop doesn't accumulate). None of the four is independent; the wave's risk is treating them as four projects instead of one nervous system.

---

## 2. System composition

### 2.1 The spine: one pipeline, three doors

The central architectural claim of this wave is that **/goal is the spine, and voice + text + menu are three doors into it — never three pipelines.** Today the doors exist but fork into different backends:

- **Voice door** (Scout Realtime-2): `launch_agent_thread` → `agent_thread_runner.go:47`, writing `os_artifact` with `agentLoop=realtime_controlled_workforce`, `workflowStages`, `goalStatus`, `reviewGate`. This *already scaffolds the 10-step loop* (`agent_thread_runner.go:56–79`) but does not *execute* it as a real orchestrator.
- **Text door**: `/assistant/query` with `mode=workflow` (per `codex-goal-workflows.md`) — same artifact scaffold, no execution.
- **Menu door**: **does not exist.** The context brief is explicit: "No slash-command/quick-action system exists" (`spectacular-os-context.md` on `scout_chat_threads.go`). `openOfficeTool` (`index.html:19295`) is the closest — it routes composers, it does not name a goal.

The wave unifies these onto **one orchestrator** that takes a *goal spec* (objective + optional tool template + context refs + originating surface) and runs the loop. The three doors become three ways to *produce a goal spec*:

```
   VOICE                 TEXT                    MENU
Scout Realtime-2     "/goal <objective>"    Quick-select tool tile
  tool call            in a thread            on thread/chat screen
      │                    │                       │
      └──────────┬─────────┴───────────┬───────────┘
                 ▼                      ▼
          ┌───────────────────────────────────┐
          │        GOAL SPEC (one schema)      │
          │  objective · toolTemplate? ·       │
          │  contextRefs · originSurface ·     │
          │  requestedBy · visibility          │
          └───────────────┬───────────────────┘
                          ▼
          ┌───────────────────────────────────┐
          │   /goal ORCHESTRATOR (Fable 5 dflt)│  ← control plane
          │  set→decompose→assign→coordinate→  │     (Technical Analyst owns
          │  execute→review→GATE→save→report→  │      the engine internals)
          │  verify→(commit/push for code)     │
          └───────────────┬───────────────────┘
                          ▼ dispatches steps to
          ┌───────────────────────────────────┐
          │  EXECUTION PLANE (model-agnostic)  │
          │  Fable-5 agent-runner (default) ·  │
          │  codex sidecar (swappable) ·       │  ← respects codex-goal-workflows.md:
          │  research/design/grill/artifacts   │     realtime = control, workers = exec
          └───────────────┬───────────────────┘
                          ▼ writes
          ┌───────────────────────────────────┐
          │  os_artifact (first-class)         │  ← visible to Realtime-2 recall
          │  + RETURN to originSurface         │     + slop classifier sees it
          └───────────────────────────────────┘
```

**Non-negotiable boundary (from `codex-goal-workflows.md`, respected):** Realtime-2 is the *control plane* — it produces goal specs, opens surfaces, answers from memory, reads results aloud. It **never** directly runs shell/SSH/browser-automation/long-research. The orchestrator dispatches those to the *execution plane* (Fable-5 runner or codex sidecar), which writes status/output back through artifacts and reviewed app actions — never mutating the live board or listening to room audio directly. This is the load-bearing safety invariant; the Technical Analyst's engine must enforce it structurally, not by prompt.

### 2.2 The four cross-cutting seams

Beyond the spine, four seams cut across all pillars. These are the integration surfaces where the wave lives or dies:

**Seam A — `originSurface` and the return trip.** Every goal spec carries where it was born (channel id / thread id / room / package stage / private-voice). When the orchestrator's *report* step (step 9) completes, the artifact does not merely land in the library — it **returns a readable card to `originSurface`**. This is the simulation's #1 independently-surfaced idea ("close the loop where it started") promoted to a first-class field. It is the single highest-leverage wiring change in the wave: it converts the pipeline's biggest failure (work finishes invisibly) into its signature move. *Owned by: Technical Analyst (schema) + UX (the returned card) + me (the contract that every door sets it).*

**Seam B — the unified push channel.** The simulation's structural item #5: "Office-mode sessions don't consume room-scoped events," which is one coupling behind three symptoms (missing completion events, board/memory requiring a room join, no room-chat unread outside the room). Today the quick fixes "paper over this with polling + HTTP snapshot reads." For the return trip (Seam A) to feel instant on everyone's screen, **every authenticated session — in room or not — must consume one push channel** carrying artifact-completion, proposal, notification, and channel-post events. Without this, "returns visibly to everyone" is a lie for the 3-of-4 who aren't in the room. *Owned by: Technical Analyst (transport) — but I flag it as the riskiest single integration; see §5.*

**Seam C — artifact intelligence + slop quarantine.** The intelligence loop has two known holes (simulation items #3 and the context brief's memory section): (1) **Scout retrieval cannot read completed artifact bodies** — grade A on chat grounding, grade D reconciling a channel number against an artifact, because it returns board-card metadata instead; (2) **no slop-discard mechanism exists** and there are no embeddings. This wave makes artifact bodies first-class in recall *and* introduces the quarantine-then-expire slop policy. These are two ends of the same organ: **decide what's worth remembering, then remember it well.** *Owned by: Domain Strategist (slop classification criteria) + Technical Analyst (data model, recall ranking).*

**Seam D — Scout tool parity + disclosure.** The private-voice allowlist is 14 tools (`kanban.go:1041–1066`); the master registry is 29 (`kanban.go:1678`). "She can do it all" means growing the allowlist toward parity — open tabs, notify, post to threads/channels, post-as-user, read responses aloud, initiate packaging /goals, self-run grill — **while keeping the external-write human gate intact** (`main.go:1187`, admin-gated to aj@shareability.com). The one genuinely new safety primitive is **post-as-user disclosure**: when Scout posts on your behalf, the record must show "via Scout" so it's never a spoofed human message. *Owned by: Technical Analyst (plumbing) + Domain/UX (disclosure UX).*

### 2.3 Component inventory (new vs upgraded)

| Component | Status | What changes | Owner |
|---|---|---|---|
| **/goal orchestrator engine** | **NEW** | Real executor of the 10-step loop; model-agnostic; Fable-5 default. Today the loop is only *scaffolded* in artifact metadata, not run. | Technical |
| **Goal spec schema** | **NEW** | One schema all three doors emit; carries `originSurface`, `toolTemplate`, `contextRefs`, `visibility`, `requestedBy`. | Technical |
| **Quick-select tool menu** | **NEW** | The menu door. Curated packaging tools on thread/chat screens. No slash/quick-action system exists today. | UX + Domain |
| **`/goal <objective>` command** | **NEW** | Text door. A parser on the thread composer that emits a goal spec. | UX + Technical |
| **Packaging tool suite + A++ prompts** | **NEW content, existing rails** | ~8–12 curated tools, each an A++ prompt template feeding the orchestrator. Rides existing agent modes. | Domain |
| **Fable-5 agent-runner backend** | **NEW** | Anthropic-API execution backend behind a model-agnostic interface; codex kept swappable. | Technical |
| **Return-to-origin card** | **NEW** | The readable card the report step pushes back to `originSurface`. | UX |
| **Unified push channel** | **UPGRADE (critical)** | One event stream every authed session consumes; replaces polling/snapshot papering-over. | Technical |
| **Scout private-voice allowlist** | **UPGRADE** | 14 → ~26 tools (toward parity), external-write gate intact, post-as-user disclosure added. | Technical + Domain |
| **Scout self-run grill** | **UPGRADE** | Grill currently room-only (`grill.go` enforces). Add a private-voice grill so Scout grills you 1:1. | Domain + Technical |
| **Artifact-body recall** | **UPGRADE** | Index artifact bodies; rank above card metadata for reconciliation questions. | Technical |
| **Slop quarantine** | **NEW** | Quarantine-then-auto-expire archive (visible, restorable, ~30-day expiry). No silent delete. | Domain + Technical |
| **WebRTC simul-join stability** | **UPGRADE** | Desktop+mobile simultaneous join without double-negotiation/flicker; the connection matrix. | RTC |
| **Noise suppression honesty** | **UPGRADE** | RNNoise already ships (`public/voice-focus/`); make settings honestly report state/device. | RTC |
| **Video "looks" / touch-up** | **NEW (lightweight)** | Zoom-like tone/touch-up only — no ML segmentation/blur (user-confirmed). | RTC |
| **BonfireOS rename + shell polish** | **UPGRADE** | "Office" → "BonfireOS" desktop + mobile; surprise-and-delight where earned. | UX |
| **Notifications / channels / packages / ledger / grill / recap** | **REUSE (shipped)** | Load-bearing plumbing the above wires together. Do not rebuild. | — |

**The ratio that matters:** roughly two-thirds of this inventory is *upgrade/reuse*, one-third *new*. That is the correct ratio for a wiring wave and honors design principle #4 ("prefer wiring existing plumbing over new infrastructure"). The genuinely new pieces — orchestrator engine, quick-select menu, Fable-5 backend, slop quarantine — are the *connective tissue*, not new organs.

---

## 3. User journeys

Each journey names the actual UI surfaces and traces back to the §0 sentence. These are the demo test (principle #5): each contains a moment nobody's seen a meeting tool do.

### Journey 1 — Voice-initiated packaging task, end to end
*Door: voice. Proves: the spine + return trip + recall.*

Maya is in the room mid-conversation about the "Nimbus" creator-IP package. She says: **"Scout, pull a comp set for Nimbus — recent creator-led IP exits, and put it in #nimbus when it's done."**

1. Room Scout (server peer, `kanban.go:508`) recognizes a packaging request and calls a tool that emits a **goal spec**: `objective="comp set: recent creator-led IP exits for Nimbus"`, `toolTemplate="comp-set"`, `contextRefs=[nimbus package artifacts]`, `originSurface=#nimbus channel`, `requestedBy=Maya`.
2. Because Scout is the *control plane*, she does not research — she confirms out loud ("On it — comp set for Nimbus, I'll drop it in #nimbus") and the orchestrator takes over. The voice island shows a small "working" state; Maya keeps talking.
3. The **orchestrator** runs the loop: sets the goal, decomposes (find comps → extract terms → rank by relevance → format), assigns a research agent (Fable-5 backend, or codex if configured), coordinates, executes, **reviews the draft against the original goal** (is this actually *recent, creator-led, exits*?), passes the **gate** (read-only research → no external-write approval needed), saves the reusable pattern, and reports.
4. The **report step returns a card into #nimbus** (Seam A) — a readable "Nimbus Comp Set" artifact card with titles and a real reader, not a raw prompt string. The **unified push channel** (Seam B) lights the bell for everyone watching #nimbus, in-room or not.
5. Eight minutes later Maya asks: **"Scout, what did that comp set say about median deal size?"** Scout reads the **artifact body** (Seam C — the D→A recall fix) and answers with the actual number, aloud.

**The moment:** she asked by talking, kept talking, and the answer came back into the exact channel she named — then she interrogated the result by voice and got a real number, not board metadata. The whole loop happened around the conversation, not instead of it.

### Journey 2 — Late joiner on mobile while desktop is in the room
*Door: none (presence). Proves: pillar 1 + catch-me-up.*

Tyler is already in the room on his **desktop**, screen-sharing a deck. Maya joins ten minutes late from her **phone**, and simultaneously Joel joins from his **desktop**.

1. The **simul-join** case (RTC's connection matrix) handles two peers negotiating at once against an existing screen-share without double-negotiation or flicker — the exact "zero weirdness when users join simultaneously on desktop and mobile" mandate. Maya's phone gets the mobile layout (no aspect-ratio pin, `index.html:16850`); Tyler's share stays stable.
2. Maya, seeing she's behind, says: **"Scout, catch me up."** `catch_me_up` scoped to her join time (`wow-roadmap.md` NOW #2) forces a brain pass and delivers a 30-second private summary to *her* alone via the private-voice path / `audience: me` notification — Tyler and Joel don't hear it.
3. Maya opens Settings → "audio & video" (`index.html:16029`) and it **honestly** says: "Noise suppression: RNNoise worklet, active, this device (iPhone)." Not a lie, not a placeholder toggle.

**The moment:** three simultaneous joins across two form factors and a live screen-share, and nobody sees a flicker — then the late joiner catches up privately in one sentence without derailing the room.

### Journey 3 — A user types /goal in a thread
*Door: text. Proves: three doors → one pipeline.*

Joel is in a **Scout chat thread** (`scout_chat_threads.go`), not the room. He types: **`/goal one-pager for the Ember anthology package, investor tone`**.

1. The composer parses `/goal` and emits the **same goal spec schema** as Journey 1's voice call — `originSurface=this thread`, `toolTemplate` inferred as "one-pager", `contextRefs=[Ember package]`.
2. The thread renders a **staged progress card** (the pattern from `codex-goal-workflows.md`: "chat renders the launched work as a staged progress card and syncs terminal state from `/artifacts`") — decompose → draft → review-against-goal → gate → done, with a live progress bar.
3. On completion the artifact **returns into this thread** as a readable card he can open, edit, copy, publish. Because he typed it in a private thread, `visibility=private` until he publishes.

**The moment:** the *identical* pipeline that Maya reached by voice, Joel reached by typing — same schema, same loop, same return card, different door. This is principle #2 made literally true, and it's the demo that proves the architecture rather than a feature.

### Journey 4 — Scout grills a founder privately
*Door: voice, private. Proves: Scout parity (self-run grill) + safety scoping.*

Before a real pitch, Maya opens the **private Realtime-2 voice** from the office home page and says: **"Scout, grill me on Nimbus — be a skeptical Series A investor."**

1. Today `start_grill_session` is **room-only** (`grill.go` enforces; private allowlist excludes it — `kanban.go:1041–1066`). This wave adds a **private self-run grill**: Scout flips her own private session instructions into the skeptical-investor persona and starts asking questions *out loud, one at a time*, listening to Maya's spoken answers — the pillar-2 mandate "run grill mode herself."
2. The exchange is captured; on `end_grill_session` Scout files a **grill artifact** (score + weak-answer flags) via the execution plane, and — because a real pitch is coming — offers: "Want me to tell the team you did a practice round and scored 7.1?" (post-as-user, disclosed, Seam D).
3. Crucially this is **private** — the grill artifact defaults to Maya's private visibility (per `first-class-chat-design.md` privacy rules); it does not leak into org intelligence unless she publishes it.

**The moment:** a solo founder, no scheduling, gets a hostile-investor dress rehearsal by voice — from the ambient assistant, in private — and it remembers the score. Nobody's seen a meeting tool grill *one person alone*.

### Journey 5 — Morning artifact review with slop quarantine
*Door: menu/dashboard. Proves: pillar 4 + the intelligence flywheel.*

Tyler opens **BonfireOS** (the renamed tab) with coffee. The office home surface shows a **Morning Brief** card: overnight results awaiting review, proposals pending approval, board deltas — *and* a new **Quarantine** tray.

1. The quarantine tray shows **three items the OS decided weren't worth remembering**: a duplicate transcript fragment, an off-topic tangent about lunch, a stale board card from an ex-participant. Each is **visible, labeled with why, restorable** — and stamped "auto-expires in 30 days" (the confirmed quarantine-then-expire policy, principle #3: "magic must never mean unsupervised side effects" — including deletion).
2. Tyler glances, agrees with two, and **restores one** (the "tangent" was actually a real idea) with one click — it re-enters recall.
3. Below, the artifacts surface lists real **titles** (not raw prompt text, the simulation's structural item #1) with a read-only reader anyone can open.

**The moment:** the company shows Tyler *what it chose to forget* and lets him overrule it — the knowledge base curates itself in the open, so it never snowballs into "years of useless information" (the mission's explicit fear), and it never silently deletes.

### Journey 6 — A package advances a stage via completed /goal work
*Door: the board reacts. Proves: the whole nervous system closing.*

The Nimbus comp set from Journey 1 lands as a published artifact.

1. **Linkage** (`linkage.go`, Jaccard title match) attaches the artifact to the Nimbus "research" stage card, and the package **auto-advances** research → design (the simulation's structural item #4: "board cards auto-advance from artifact events," so "the board stops actively lying about package state").
2. The **unified push channel** (Seam B) fans the stage-advance to everyone's screen. On the **package binder** surface (shipped: `create_package`/`advance_package_stage`), the Nimbus stage-rail visibly ticks forward.
3. Later Maya says: **"Scout, advance Nimbus to grill,"** and Scout — via the packaging tools now in her private allowlist (Seam D) — calls `advance_package_stage`, which itself can launch a grill /goal. The loop feeds itself.

**The moment:** real work you started by voice *moved the company's state machine* without anyone dragging a card — and then a spoken command advanced it again. The board is a live readout of the company thinking, not a manual to-do list.

### Journey coverage check

| Journey | Door | Pillar(s) | New "nobody's seen this" moment | Key seam |
|---|---|---|---|---|
| 1. Voice comp set | Voice | 2,3,4 | Answer returns to the named channel; voice-interrogate the result | A, B, C |
| 2. Simul-join + catch-up | Presence | 1,2 | 3 simul joins, 2 form factors, live share, no flicker | (RTC) |
| 3. /goal in thread | Text | 3 | Identical pipeline reached by typing | A |
| 4. Private grill | Voice | 2 | Ambient assistant grills one founder, privately | D |
| 5. Slop quarantine | Menu | 4 | The OS shows you what it forgot, you overrule it | C |
| 6. Package auto-advance | Reactive | 3,4 | Voice-started work moves the board itself | A, B |

All four pillars appear in ≥2 journeys; all four seams are exercised. No journey is a single-pillar demo — which is the point.

---

## 4. Idea triage

Design principle: **look for delight that's cheap because the plumbing exists** (principle #4). I rank each candidate by *wow-per-hour given what this wave already builds*, then rule it IN or DEFER with reasoning. The bias is aggressive inclusion of anything that's mostly wiring *and* closes a loop, hard deferral of anything whose risk is judgment quality rather than plumbing.

### Rides this wave (IN)

**IN-1. Close-the-loop return card (Seam A).** *Not optional — it IS the wave.* The simulation surfaced it independently in all three scenes. Promoting `originSurface` to a first-class goal-spec field is the cheapest transformation of the pipeline's biggest weakness into its signature move. Already justified in §2.2/§3.

**IN-2. Voice-proposed codex work exposed to Scout** — *already shipped* (`wow-roadmap.md` NOW #5) but the private allowlist parity work (Seam D) makes it reachable from the private voice too, not just the room. Near-zero marginal cost; big parity win.

**IN-3. Catch-me-up for late joiners (`wow-roadmap.md` NOW #2).** Journey 2 depends on it; the summarizer + memory-query + private delivery all exist. ~1 day, and it's the payoff moment of the simul-join demo. IN.

**IN-4. Auto-title / titled artifacts (simulation top idea #1 + structural #1).** The library listing raw prompt text with empty titles is actively embarrassing in Journey 5. Titles + a read-only reader for everyone is table-stakes for "design excellence." IN — it's a prerequisite, not an extra.

**IN-5. Re-grill readiness dial (simulation top idea #3).** Feed the team's/founder's answers back into a rerun and track the score delta (6.2 → 7.1). Journey 4's private grill makes this nearly free — the grill artifact + rerun-with-context is the same mechanism. Turns a one-shot report into a *pitch-readiness metric* the company watches over time. High wow, rides existing rails. IN.

**IN-6. Wake-word presence (`wow-roadmap.md` NOW #3).** "Say 'Scout' and the whole shell takes a breath." Pure surprise-and-delight, ~1 day, the `.is-listening` breathe animation already exists. This is exactly the "delight that earns its place" the design pillar asks for, and it makes the voice door (now the *primary* door) feel alive. IN — but gated on RTC/voice stability landing first so it doesn't false-fire.

**IN-7. Morning Brief (`wow-roadmap.md` NEXT #5).** Journey 5's frame. Every input has a snapshot function already; it's composition logic. It's also the natural *home for the quarantine tray* — so the slop policy (mandated) gets a beautiful surface for free. IN, scoped to compose-from-existing-snapshots (no new ambient workers).

### Deferred (with reasoning)

**DEFER-1. Scout interjects — the participant-with-a-conscience moonshot (`wow-roadmap.md` LATER #1).** This is *the* named-after-it demo, and it's tempting. **Defer anyway.** Its risk is *judgment* (contradiction confidence + interruption etiquette), not plumbing — precisely the wrong risk profile for a wiring wave. Shipping a Scout that interrupts wrongly would *lower* the quality bar, not raise it. It also depends on the Decision Ledger being battle-tested, which it isn't yet. Revisit once this wave's recall (Seam C) proves artifact/ledger grounding is A-grade.

**DEFER-2. Ambient consensus detection (LATER #2).** Same reasoning — stance-inference quality is the risk. Defer.

**DEFER-3. The Panel — multi-persona investor room (LATER #6).** Journey 4 gives us *single-persona* private grill this wave. Multi-voice orchestration on one realtime session is "the frontier" (roadmap's own words). The single-persona version delivers 80% of the wow at 20% of the risk. Defer the panel; ship the solo grill.

**DEFER-4. Market twin / overnight staff (LATER #4, #5).** Both need scheduled/standing ambient workers and network-access hygiene — new infrastructure, against principle #4 on a 4GB VPS. The wave already adds an orchestrator engine and a push channel; adding standing autonomous watchers on top is too much new surface at once. Defer until the orchestrator is proven with human-in-the-loop /goals before it runs unattended.

**DEFER-5. Embeddings for recall.** Tempting to "do recall right" with vectors. But the recall *fix that matters this wave* (Seam C) is narrow: **index artifact bodies and rank them above card metadata for reconciliation questions.** That closes the demonstrated D-grade gap with text-match ranking, no vector store, no new infra. Embeddings are a larger bet with real ops cost on one VPS; defer to a dedicated pass. *(Flag for Technical Analyst: confirm the narrow fix is sufficient for the reconciliation journey; if not, escalate.)*

**DEFER-6. Codex app-server broker (`first-class-chat-design.md` Phase B+).** The existing `codex exec` sidecar queue is live and sufficient as the *swappable backend*. Adding the richer app-server broker is orthogonal to this wave's model-agnostic mandate. Keep the sidecar; defer the broker.

### The triage principle, stated

**IN if:** the risk is wiring (bounded, testable) AND it closes a loop AND the plumbing exists. **DEFER if:** the risk is judgment quality (unbounded, needs eval), OR it needs standing autonomous infrastructure, OR a cheaper narrow version delivers most of the wow. Every deferral above is a *risk-profile* decision, not a value decision — the deferred ideas are excellent; they're just the *wrong risk* for a wave whose promise is "works flawlessly."

---

## 5. Sequencing & dependency logic

### What must land first (the foundation)

**F1. The goal spec schema + orchestrator skeleton (Technical).** Nothing else in the spine can be built until the one schema and the loop-executor interface exist — even as a stub that runs the existing agent modes. This is the keystone; it blocks all three doors.

**F2. The unified push channel (Technical) — the riskiest integration, land it early.** Seam B is the load-bearing dependency for *every* return-trip journey (1, 3, 6) feeling instant on non-room sessions. It is also the single hardest change because it touches the room/office coupling the simulation flagged (item #5) and the quick fixes only papered over. **If this slips, the wave's signature moment ("returns visibly to everyone") silently degrades to polling latency — the exact 35-seconds-vs-never failure the simulation caught.** Land it early, test it hard, and treat its acceptance as a wave gate.

**F3. WebRTC simul-join stability (RTC), in parallel.** Pillar 1 is *independent* of the spine — it can and should build in parallel from day one. It has its own test harness (the media smoke harness, `frontend_latency_test.go`). It gates Journey 2 and the wake-word delight (IN-6), and it's the reason voice can be the primary door. No dependency on F1/F2.

### What can parallel (after the foundation)

Once F1 (schema) exists, the three doors parallelize:
- **Voice door** (Scout parity, Seam D) — Technical + Domain.
- **Text door** (`/goal` parser) — UX + Technical. Cheapest door; good early proof of the schema.
- **Menu door** (quick-select + tool suite content) — UX + Domain. The tool *content* (A++ prompts) parallelizes independently of the menu *chrome*.

Also parallel after foundation:
- **Slop quarantine + titled artifacts + artifact-body recall** (Seam C) — Domain + Technical. Independent of the doors; gated only on the memory store, which exists.
- **BonfireOS rename + shell polish + delight inventory** (UX) — independent of everything; can start immediately (it's the cheapest visible win and sets the quality bar for all other surfaces).

### The critical path

```
F1 schema ──┬── voice door ──┐
            ├── text door ────┼── return-card (needs Seam A field in F1) ──┐
            └── menu door ────┘                                            │
F2 push channel ───────────────────────────────────────────────────────── ┴── Journeys 1,3,6 "instant & visible"
F3 RTC (parallel) ───────────────────────────────────────────────── Journey 2 + IN-6 wake-word
Seam C (parallel) ───────────────────────────────────────────────── Journey 5 + Journey 1's recall answer
UX shell/rename (parallel from day 1) ───────────────────────────── quality bar for all surfaces
```

**The riskiest integration, ranked:**
1. **F2 unified push channel** — highest risk, highest blast radius. Touches known-fragile room/office coupling. *Mitigation: build and gate it before any return-trip journey is called done; keep the polling fallback until it's proven under two-session test.*
2. **Orchestrator gate-before-shipping under real external writes** — the loop's step 7 must keep the `external_write` approval gate (`codex_runner_queue.go`, `main.go:1187`) structurally intact when the *new* Fable-5 backend runs it, not just the codex path. A model-agnostic runner that loses the gate on one backend is a safety regression. *Mitigation: the gate lives in the orchestrator/queue, not the backend; test both backends against it.*
3. **Scout post-as-user without disclosure** — a parity feature that, done wrong, spoofs humans. *Mitigation: disclosure ("via Scout") is a schema field on the post record, tested, before the tool ships.*
4. **Working inside one 34.9k-line `index.html`** — every UI surface (menu, cards, quarantine tray, rename) edits one file with no build step. *Mitigation: UX owns a convention for where new surfaces slot in; sequence UI edits to avoid two waves editing overlapping line ranges simultaneously.*

### Sequencing verdict

Ship in this dependency order, parallelizing aggressively after the foundation:
**Foundation:** F1 schema + F2 push channel + F3 RTC (F3 fully parallel). →
**Doors + intelligence:** text door (proves schema cheapest) + Seam C recall/slop + Scout parity, in parallel. →
**Menu + tool content + return card + delight:** the visible payoff, once the spine carries work. →
**Polish gate:** BonfireOS rename and shell polish run *throughout* but the final delight pass (wake-word IN-6, animations) lands last, after stability, so nothing delightful false-fires on a shaky foundation.

---

## 6. Success criteria — the "it feels like magic" acceptance moments

Each is *observable* — a specific thing a person does and sees — not a metric. If the wave can't produce the left column live, it isn't done.

### Pillar 1 — First-class WebRTC
- **The simul-join test:** two people join one room at the same instant, one desktop + one phone, while a third is already screen-sharing. Observed: no flicker, no dropped share, no double-negotiation, both new tiles stable within seconds. *(The exact mandate.)*
- **The honesty test:** open Settings → audio & video on three different devices. Each *correctly* reports its own suppression state, device, and whether anything needs re-enabling. No placeholder toggle, no lie.
- **The looks test:** enable video touch-up; the tone lift is visible and flattering, applied with no perceptible lag, and it's clearly *lightweight* (no segmentation artifacts, because there's no segmentation).

### Pillar 2 — Scout Realtime-2 (she can do it all)
- **The one-sentence chain:** "Scout, tell Tyler I moved Nimbus to grill, then grill me on the unit economics" produces, from one utterance: a disclosed post-as-Maya message to Tyler, a bell notification, and Scout flipping into investor persona and asking the first question aloud — all without touching a keyboard.
- **The parity test:** every capability the mandate lists (open tabs, notify users, post to threads/channels, post-as-user with disclosure, read responses aloud, recall meetings, initiate a packaging /goal, self-run grill) is reachable from the **private** voice, not just the room — and every external write still hits the human gate.
- **The read-aloud test:** "Scout, what did Joel say in #dealflow?" — she reads the actual response aloud, and it's the real message, not a summary of board metadata.

### Pillar 3 — Tool suite + /goal pipeline
- **The three-doors test:** the *same* comp-set goal, reached by (a) voice, (b) typing `/goal`, (c) clicking the quick-select tile, produces the *same* staged loop and the *same* return card. Demonstrably one pipeline, three doors.
- **The return-trip test:** work requested in #nimbus comes back *into #nimbus* as a readable titled card, and the bell lights for someone who never entered the room. *(The simulation's exact failure, now the signature move.)*
- **The gate test:** a /goal that would write externally (e.g., email an LP the memo) *stops* at an approval card before the side effect — on both the Fable-5 and codex backends.
- **The recall test:** "Scout, does the comp set's median match what Joel said in #dealflow?" — she reconciles the *artifact body* against the *channel message* correctly (the D→A fix), not board metadata.

### Pillar 4 — Design excellence + slop quarantine
- **The rename test:** the tab reads **BonfireOS** on desktop and mobile. (Trivial, mandated, do it first.)
- **The delight test:** say "Scout" mid-conversation and the shell visibly takes a breath. At least three state transitions in the wave have intention nobody asked for but everyone smiles at — and none of them false-fire.
- **The mobile-parity test:** every journey above is completable on a phone with no horizontal overflow, no clipped panes, and layout that reads as *designed for* mobile, not shrunk.
- **The quarantine test:** the morning tray shows what the OS chose to forget, *why*, with restore, and a visible expiry — and restoring an item puts it back in recall. Nothing was ever silently hard-deleted.

### The whole-wave acceptance moment
One continuous demo, no cuts: *Maya joins late on her phone while Tyler shares on desktop and Joel joins simultaneously (P1); she says "Scout, catch me up" and gets a private summary (P2); she says "Scout, pull a comp set for Nimbus and put it in #nimbus" and keeps talking (P3, voice door); the card returns to #nimbus and Joel's bell lights though he's not in the room (Seam A+B); Joel types `/goal one-pager for Nimbus` and watches the same staged card (P3, text door); Maya asks Scout to grill her privately and scores 7.1 (P2); the Nimbus board stage auto-advances on-screen (Seam A); and next morning Tyler sees the quarantine tray show three forgotten things and restores one (P4).* If that runs end-to-end with no dead ends and no invisible finishes, the wave is spectacular.

---

## Appendix — Contracts I'm asserting for the other specialists

These are the seams where my composition depends on their depth. Each is a *request for a contract*, flagged so the lead's synthesis can reconcile:

- **To Technical Analyst:** (1) one goal-spec schema with `originSurface` as a first-class field; (2) the external-write gate must live in the orchestrator/queue, backend-agnostic, so both Fable-5 and codex paths inherit it; (3) the unified push channel is the wave's riskiest dependency — please treat its two-session acceptance as a gate; (4) confirm the *narrow* artifact-body recall fix (index bodies, rank above card metadata) closes the reconciliation gap without embeddings, or escalate.
- **To Domain Strategist:** (1) the ~8–12 tool suite must each emit a goal spec with a `toolTemplate` id the menu and `/goal` parser can both reference; (2) slop-classification criteria need to produce *human-readable "why"* strings for the quarantine tray (Journey 5 shows the reason); (3) define which tools are read-only vs external-write so the gate wiring is correct per tool.
- **To RTC Engineer:** pillar 1 is independent and should ship in parallel from day one; the only cross-dependency is that wake-word delight (IN-6) must not false-fire, so voice-capture stability gates that one delight item.
- **To UX Designer:** (1) the return-to-origin card is a new first-class component appearing in channels, threads, and the room — one design, three contexts; (2) the quarantine tray lives in the Morning Brief; (3) own the convention for slotting new surfaces into the 34.9k-line file so parallel waves don't collide; (4) post-as-user needs a visible "via Scout" disclosure treatment.
- **To the Lead (synthesis):** my strongest opinion is that **F2 (unified push channel) is the wave's true keystone, not the orchestrator** — the orchestrator makes work happen, but F2 is why anyone sees it. If a cut has to be made, cut a deferred idea, never F2.
