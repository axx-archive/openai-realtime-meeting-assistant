# Bonfire OS × the Packaging Superpower — architecture analysis & direction (2026-07-05)

---

## Executive summary

**1. Model stack — your mental model is half right, and the wrong half matters.** Voice is OpenAI Realtime (`gpt-realtime-2`, kanban.go:27) and should stay — nobody else has a production speech-to-speech API. But text chat is *not* Realtime; it already runs on `gpt-5.5` via the Responses API (openai_responses.go:18). And Fable-as-orchestrator is *designed but dormant*: the local `.env.local` (gitignored, .gitignore:3) has no `ANTHROPIC_API_KEY`, so every agentic run — goals, grill reports, packaging deliverables — falls back to the Codex sidecar on an **unpinned model** (agent_runner_iface.go:143-168, .env.local:29-30). The single highest-leverage action in this document is closing OPS-3. Target matrix: Realtime for voice, **Sonnet 5** for text chat, **Haiku 4.5** for routing, **Fable 5 at effort high** (not today's low, agent_runner_anthropic.go:48-55) for orchestration + deliverables, Opus 4.8 for reviews, gpt-5.5 stays on the ambient workers.

**2. Menu vs intelligent routing — hybrid, decisively, and cut the pills.** Keep the palette: it is not a menu, it is a registry of goal presets with contracts, rubrics, and kill conditions (tool_registry.go:104-403) — the single taxonomy every door converges on. Give the typed Scout the routing brain voice already has (`initiate_goal`, kanban.go:2253-2272): a **propose-confirm card** — Scout detects deliverable-shaped intent, proposes the matching tool with pre-filled fields, and one tap runs the identical `runGoalPipeline` spec. Never silent launch. Cut the four composer pills and retire the keyword auto-invoke (scout_chat.go:272-287) — it is the system's only silent heavy-process trigger and its phrase list ("market", "brief") guarantees false positives.

**3. Skills — yes, and the runtime already exists in embryo.** The 12 tools are proto-skills: contracted prompts with a gate, but no stages, no rivals, no closed critic loops. The goal engine (10-state machine, rubric gates, bounded revisions — goal_engine.go:564-677, :1009-1159) is a skill runtime missing two primitives: **panel** (N parallel personas + synthesis) and **gate-with-thresholds**. Add those plus a versioned **ProcessDefinition** (authored stages as Go structs beside tool_registry.go), then port `/packaging` as the flagship process. Do not build a workflow DSL — the moat is 5-6 authored, opinionated pipelines, not a platform.

**4. Artifacts — the biggest gap between the superpower and the code.** An artifact today is a text field in a JSONL store rendered through an escape-everything DOM renderer (memory.go:21, index.html:22111). An HTML deck displays as escaped source; a PDF cannot even be stored. The Deal Room — the one page an investor sees — flattens everything to escaped bullets (deal_room.go:676). Build first-class Artifacts: a sandboxed render route (highest fidelity-per-line-of-code in this plan), a content-addressed blob store, typed provenance/versioning, tokened share links, and a Deal Room artifact gallery. Rendering quality *is* the product the investor sees.

**5. Compounding brain — 80% of the plumbing exists, 0% of the signal is captured.** The typed memory store, five cursor-driven ambient workers, and the decision ledger are the distill-and-inject layers already built (memory.go:57, agent_runner.go:31-129). What's missing is capture: human edits, reject reasons, follow-up instructions, and grill deltas all flow through existing server seams today and are discarded (main.go:1305, office_brief.go:433). Log them as a `signal` kind — zero model tokens — then add a per-user Taste Analyst worker and a House-Style Distiller, and **pin** their outputs into prompts the way the 12 decisions already ride into every answer (memory_query.go:962-975). Surveys are one-tap chips on existing completion cards, nothing more. Capture ships first: every week without it is unrecoverable training data.

### The unifying thesis

Bonfire already built the right spine — one tool registry, one goal pipeline, one package binder, one memory store. The five answers are one move viewed from five angles: **put the best models where humans see the output** (Fable-high on deliverables, Sonnet on the front door, pennies on the ambient lanes); **make every door — palette, chat, voice — converge on that one spine** via propose-confirm routing so the human's only chat cost is a judgment tap; **make the spine's processes the IP** by encoding /packaging's adversarial choreography (rivals, panels, closed critic loops) as authored ProcessDefinitions the engine executes; **make the output presentation-grade and shareable** so what the engine produces is literally what the investor opens; and **make every run inherit the taste of every previous run** by capturing the team's edits and verdicts and pinning the distilled profile into every future prompt. The end state: a packaging run touches a human at exactly four moments — the brief, the narrative pick, the founder pass, the ship approval — all judgment and taste, zero production labor. That is "the OS pushes inspiration to 100," made mechanical.

---

## 1. Model stack

### What is actually wired (three corrections)

**Text chat is not Realtime.** Voice — the shared room Scout and the private dashboard voice — is `gpt-realtime-2` end to end over WebRTC (kanban.go:27, :514, :706, :995), reasoning effort high, voice "marin", with per-utterance transcription on `gpt-4o-transcribe` (domain_terms.go:9) and a durable transcript lane on `gpt-realtime-whisper` (transcription_lane.go:21). Every *text* surface — Scout chat Q&A (memory_query.go:204-211), agent-thread follow-ups (agent_thread_followup.go:223-230), the brain/board/ledger/mission/slop workers — funnels through `createOpenAITextResponse` → POST /v1/responses on `gpt-5.5` (openai_responses.go:18, :57-63). The split already exists; the question is whether gpt-5.5-at-effort-low is the right text brain. It isn't (below).

**Fable is dormant under the deployed config.** The code defaults the orchestrator to `claude-fable-5` iff `ANTHROPIC_API_KEY` is set (agent_runner_iface.go:143-168, agent_runner_anthropic.go:20). The local `.env.local` (gitignored — only deploy/digitalocean/.env.example is tracked; note it also holds a live Resend key, so keep it that way) has no Anthropic key, no `BONFIRE_AGENT_RUNNER`, and routes agentic work to the Codex sidecar (`BONFIRE_AGENT_THREAD_WORKER=codex_exec`, `BONFIRE_CODEX_RUNNER_MODE=sidecar_queue`, lines 29-30). `BONFIRE_CODEX_MODEL` is also unset, so the sidecar runs whatever the Codex CLI defaults to on the host. Per project memory, OPS-3 (live VPS keys) is still open — meaning production thebonfire.xyz has plausibly **never run Fable**, and your grill readiness scores are being generated by an unpinned fallback. `launchGoalThread` 503s outright when keyless (goal_engine.go:406-408).

**Grill is two stacks.** The live persona is the room Realtime session with swapped instructions (grill.go:65-118); the graded report rides the agent-runner stack — today, the Codex fallback.

### The role matrix

| Role | Model | Config | Why |
|---|---|---|---|
| Voice (room + private) | gpt-realtime-2 | as-is (reasoning high, marin) | Only real speech-to-speech option; contract pinned in realtime_config_test.go |
| Transcription (both lanes) | gpt-4o-transcribe / gpt-realtime-whisper | as-is | Working |
| Text chat (Scout threads, follow-ups) | **claude-sonnet-5** | effort low-medium, adaptive thinking | Near-Opus quality at $3/$15 (intro $2/$10 through 2026-08-31); the taste surface; vendor-coherent with the orchestrator — one caching regime, one tool-schema dialect, chat hands context to /goal without a vendor seam |
| Routing / intent | **claude-haiku-4-5** | strict tool use, enum over registry | $1/$5, classification-shaped; the enabling primitive for §2 |
| Orchestration (decompose, gate, verify) | **claude-fable-5** | effort medium→high, timeout ≥15m, streaming | Long-horizon agentic is Fable's design center; these stages ARE the quality bar |
| Deliverable workers (packaging artifacts, grill reports, deep research) | **claude-fable-5** | effort **high**, max_tokens 32-64K streamed, refusal fallback → opus-4-8 | This is the product; don't starve it at effort medium / 8192 tokens |
| Per-subtask review + coordination | claude-opus-4-8 (or sonnet-5) | effort high, **full artifact body** | Half Fable's price; reviews need the whole artifact, not the Fable ceiling |
| Ambient workers (brain, board, ledger, mission) | gpt-5.5 (keep) | as-is | Batch-shaped, cheap, one seam; migrate opportunistically, not now |
| Slop classifier | gpt-5.5 now, haiku-4-5 eventually | — | Pure classification |
| Shell/repo execution | Codex sidecar, **BONFIRE_CODEX_MODEL=gpt-5.5 pinned** | sandbox/authority as-is | Only sandboxed runner; your own docs recommend the pin (docs/plans/first-class-chat-design.md:171) |

### Fable operational requirements the code doesn't handle yet

- **Effort is the dial and it's set to "low"** (agent_runner_anthropic.go:48-55; deliverables bump only to medium/8192). Paying $10/$50 for shallow depth is the worst of both worlds. Raise deliverables to effort high, 32K+ tokens.
- **The 5-minute timeout** (agent_runner_anthropic.go:57-59) will convert slow-but-good high-effort runs into `needs_attention` failures. Raise it and/or stream in the same wave as the effort change — not separately.
- **Refusal handling**: Fable can return HTTP 200 with `stop_reason: "refusal"`; the tool loop must branch on it and pass server-side fallbacks to `claude-opus-4-8` (beta `server-side-fallback-2026-06-01`), or a classifier false-positive on a rights/chain-of-title prompt kills a goal run mid-pipeline.
- **Data retention**: Fable requires 30-day retention; a ZDR org 400s every request. Check org config before debugging code.
- **Prompt caching**: the 24-turn loop resends the full conversation each turn; `cache_control` breakpoints are a ~10x cut on repeated-input spend.

Cost reality: at 6 users, a full packaging goal at Fable-high is single-digit dollars against the value of one investible package. Spend on quality wherever a human sees the output; spend on cost only in the ambient lanes and Realtime minutes.

### Contradiction resolved: what model runs the router?

The routing analysis proposed running the new chat router "on the existing meetingBrainModel (gpt-5.5) seam"; the model analysis proposed Haiku 4.5. **Decision: Haiku 4.5, shipped in the same wave chat moves to Sonnet 5.** Once the conversational surface is Anthropic, keeping the router on OpenAI reintroduces the exact vendor seam the migration removes — two tool-schema dialects on one code path. Haiku is cheaper than gpt-5.5, faster, and its strict tool use is purpose-built for enum-over-registry classification. If sequencing forces the router to ship before the chat migration, launch it on the gpt-5.5 seam and swap the model constant when Sonnet lands — the propose-confirm architecture is identical either way.

---

## 2. Menu vs intelligent routing

### The asymmetry nobody noticed

Voice Scout already routes intelligently: the `initiate_goal` Realtime tool takes an objective plus an optional tool preset id and authority hint (kanban.go:2253-2272, dispatched via kanban.go:2870-2911). The *typed* Scout has no function-calling loop at all — its reply path is memory Q&A (scout_chat_threads.go:425, memory_query.go:95), and even `/goal` is parsed client-side (index.html:30990-31021). From typed conversation, Scout literally cannot invoke any of the 12 tools. Meanwhile the routing text chat *does* have is the worst kind: `scoutChatThreadModeForText` silently keyword-sniffs messages ("market", "brief", "screen", "grill") and launches a generic single-shot agent thread with no confirmation, no rubric, no tool template (scout_chat.go:272-287, scout_chat_threads.go:377-389). "What did we decide about the market for this?" launches a workstream instead of answering. That is intelligent routing without legibility — the failure mode to design out.

### The verdict: hybrid — palette as registry, router as verb, card as the trust surface

The palette is genuinely good infrastructure: 12 goal presets, each with an output contract, a rubric with a kill condition, an authority class, and 1-3 form fields, all converging on `runGoalPipeline` → POST /assistant/goal (tool_registry.go:3-8, index.html:31049-31092, main.go:986-1065), server-registered so a router builds *on* it (GET /assistant/tools, tool_registry.go:454-471). The trust surface exists *after* launch (10-node goalcard, gate outcome, "N marked ASSUMED" line — index.html:31711-31769, pinned by wave11_palette_test.go:198-216). What's missing is the trust surface *before* launch.

**Three-tier response policy for typed Scout, replacing keyword sniffing entirely:**

- **Tier 0 — answer inline (heavily-biased default):** anything answerable from memory stays the existing Q&A path. An agent that under-routes is trusted; one that over-launches is muted.
- **Tier 1 — single-shot workstream, proposed:** bounded "go do one thing" asks map to `launchAgentThreadWithOrigin`, but as a proposal card, never silently.
- **Tier 2 — goal pipeline, proposed:** asks matching a registry contract ("one-pager for the Sundance buyer", "is the chain of title clean?") map to a toolTemplate'd `runGoalPipeline` spec.

Mechanically: one function-calling turn in `appendScoutChatThreadMessage` exposing `propose_tool_run(tool_id, objective, package, fields)` and `propose_workstream(mode, query)`, with names/descriptions injected from tool_registry.go so the registry stays the single taxonomy source. This is the typed twin of voice `initiate_goal` — proven schema shape.

**The confirmation card is load-bearing.** Scout answers with one legible sentence — "This is a Comps & Precedent run — I'll pull precedent titles against your thesis and file a research brief to the Neon Signal package. Gate: rubric-scored, kill condition = any invented source." — plus a card: tool name + group, pre-filled *editable* form fields (reuse the palette form-card morphing, index.html:31559-31577), target package, authority class, weight label ("multi-agent goal loop, ~5-15 min" vs "quick single pass"). One tap posts the identical spec the palette Run button posts; a "just answer instead" escape forces Tier 0. The card is simultaneously the trust surface, the cost gate (nothing multi-agent starts without a tap — critical while concurrency limits are global, goal_engine.go:297-300), and the in-context tutorial for the 12 tools.

**Explicitly rejected: fully silent routing.** A /goal launch is a Fable decompose + up to 6 child threads + review/gate calls with zero per-user rate limits. Auto-invoking that off conversational keywords is a cost hazard and a trust hazard. The tap stays mandatory for Tier 2 until per-user quotas exist.

### What happens to each existing door

- **Pills: cut them** (index.html:17117-17120). They only prefill starter text (index.html:21673-21685) and ride the keyword-sniff path to a generic thread. Their quick-lane survives as Tier 1 proposals — sequence the router into the same release so the lane never disappears.
- **Keyword sniffing: retire it.** The only silent heavy invoke in the system.
- **Fidelity bug, fix regardless of everything else:** the palette's own conversational tiles (deep_research, grill_pressure_test) reuse the pill machinery and *drop their toolTemplate* (paletteConversationalHandoff, index.html:31431-31437) — so the same tool name produces contract-gated output from one door and generic output from another. Make the handoff carry `tool.id`. If this doesn't land before the router, two "talk it out" paths will produce visibly different quality for the same tool, which reads as flakiness.
- **Palette, /goal, voice: unchanged.** They already converge on `runGoalPipeline`; the router is the fourth convergent door. Menu = noun catalog, router = verb.
- **Misfire economics:** log card dismissals (Q5 fuel); suppress a twice-dismissed mapping per session; add a user-facing cancel for running goals (persist `needs_attention`, halt `dispatchReady`) so a wrong launch costs one tap, not six subtasks.

Measure proposal-acceptance rate from day one; below ~50%, tighten the trigger.

---

## 3. Skills direction

### Are the palette tools skills? Skills in contract, not in choreography.

Each of the 12 tools carries an output contract, a 3-5 dimension rubric with a kill condition (tool_registry.go:66-80, :104-403), and a master-wrapper prompt with evidence discipline (tool_prompts.go:24-84). The engine reviews against the rubric with strict-JSON verdicts and bounded revisions (goal_engine.go:1009-1103) — structurally, that is /packaging's critic loop. What they lack versus /packaging:

1. **No stage structure** — the model free-decomposes into ≤6 subtasks (goal_engine.go:71) and only the sink subtask gets the contract (goal_engine.go:853-884). In /packaging, the 10 authored stages (SKILL.md:22-33) *are* the intellectual property; today Fable re-invents the pipeline shape every run.
2. **No adversarial structure** — no rival teams, no judge panels, no persona red-teams.
3. **Open-loop critique** — grill has personas (grill.go:65-260) but nothing forces the same critics to verify their round-1 objections were answered (the /packaging gate schema: objections_answered / objections_remaining, workflows.md:38).
4. **Single artifact, no interlocks** — /packaging ships 5 interlocking artifacts with no-contradiction rules (SKILL.md:12); package_assembly today is a field change, not a compiler.

And one defect that poisons everything above: **the reviewer reads a one-line flattened thumbnail of the artifact** (`compactAssistantLine`, goal_engine.go:1065-1069). Adding panels and judges on top of a blind reviewer multiplies cost, not quality. Fix the reviewer's eyes first.

### The abstraction: ProcessDefinition on the existing engine

Do not build a new engine; the substrate is there (AgentProgress streaming with Stage/ReviewGate, agent_runner_iface.go:60-81; per-transition persistence + boot reconciler, goal_engine.go:1604-1617; strict-JSON parallel-call pattern, goal_engine.go:1070-1102; package binders as the multi-artifact container, packages.go:247-711). Add a thin layer — versioned Go structs beside tool_registry.go, served through the same GET /assistant/tools payload so palette, /goal, voice, and the §2 router all reach processes via `runGoalPipeline` unchanged:

- **Stages**: authored, ordered; each declares roles (writer | panel-of-N | judges-of-N | synthesizer), input contracts, output contract, parallel vs sequential. Decompose becomes "instantiate the definition." Free-form /goal remains the fallback.
- **Panel primitive**: N parallel Anthropic calls with per-persona system prompts + shared strict-JSON schema + one synthesis call — implemented as goroutine fan-out *inside one engine step*, not as engine subtasks, so the DAG stays coarse and goalMaxSubtasks stays sane. One primitive covers red-team quartets, judge trios, slide juries, and the typographer/story-editor pair (workflows.md:11-34, :78-86).
- **Gate primitive**: threshold + per-dimension floor + max rounds + force-accept-with-disclosed-gaps (9.0 internal / 9.5 client-facing, floor 7.0, max 2 rounds — SKILL.md:80). Today's toolRubric becomes the degenerate 1-stage case, so the 12 tools migrate for free.
- **Budgets**: per-process subtask/token/wall-clock envelope overriding goalMaxSubtasks=6 and the 5m timeout, with per-stage checkpointing on the existing persist path so long runs resume rather than die.

### Porting /packaging: flagship, with scope cuts

**Phase 1 — mechanisms as primitives (before any new deliverable type):** fix the reviewer's input; protect lists (`strengths_to_keep`/`do_not_touch` fed into the requeue prompt — tens of lines, improves all 12 tools immediately); deterministic law sweeps (heading presence, em-dash grep on client-facing contracts, READINESS parse — parser exists at agent_thread_followup.go:497-513; zero model cost); **close the grill loop** as the primitives' first consumer — red-team panel → `objection_ledger_v1` artifact → gate re-presents each persona its own objections, readiness dial moves on *verified* fixes. Cheapest visible proof of the whole direction.

**Phase 2 — `packaging_studio` as the flagship ProcessDefinition:** INTAKE (form: sources, founder's verbatim words, audience — the palette form-card is exactly this surface) → RED-TEAM → COMPETE (3 rival narrative architects × 3 judges scoring excitement/coherence/credibility/distinctiveness, mandatory `best_beats_to_steal`; final = winner + grafted steals) → WRITE → GATE (personas re-review with round-1 objections in hand) → VOICE (speechwriter + copy-chief, interlock rule) → SHIP (HTML deck from deck-template.html shipped verbatim as a static asset + rigor companion + findings record, all attached to the venturePackage — "clients trust a document more when they can see it was attacked").

**Cut from v1:** IMAGERY (Midjourney has no API; the harvest requires the user's interactive browser session) and PDF flattening (chromium + poppler + PIL don't exist in the Docker image). Ship the interactive HTML deck in-app — which depends on §4's viewer, the better place to spend that effort. Vision-based slide juries wait for image-input plumbing (the raw-content seam at agent_runner_anthropic.go:96-99 can carry image blocks; nothing sends them yet). Set the expectation explicitly: the in-app flagship will look less cinematic than the skill until an imagery path lands.

### The design-identity gap (an asymmetry inside /packaging itself)

/packaging runs three rival narrative spines through a judge panel for the *story*, but design gets a single fixed answer: the house system in deck-template.html (locked type scale, hairline frame, duotone plates) parameterized by four CSS tokens (`--red`/`--ink`/`--dust`/`--bone` + the `--heat` dial). When client brand assets exist, workflows.md §6 extracts the logo and the tokens get recolored; when nothing exists, there is no identity-development stage at all — output is "Bonfire house style, recolored," never an identity born from the project.

The fix reuses two things that already exist: the **rival-competition primitive** (§3 above) and the **brand_design_brief palette tool** (design_brief_v1 contract, tool_registry.go:231), which today floats unconnected to the flagship. When packaging_studio's INTAKE finds no brand assets, insert an identity-competition stage between COMPETE and WRITE: 2-3 rival visual directions (token set + type pairing + duotone treatment, each applied to the same 2-3 sample slides), judged by a design panel, winner's tokens fed into the template chassis — the layout/craft system stays fixed and reliable; only the aesthetic layer competes. brand_design_brief becomes that stage's standalone door, so the tool finally has a home inside the pipeline it was always meant to serve.

**What generalizes where:** rival-competition-with-beat-stealing → market_map, deck_outline, brand_design_brief; closed persona loop → grill first, then every Package-group gate; protect lists + law sweeps → engine-wide; candor as a scored rubric dimension → one_pager and investor_update_memo (data-only change); the interlock gate → package_assembly becomes the real "assembled-stage compiler" the roadmap promised (pairwise consistency across everything attached to a package); effort-classified fix taxonomy (one-line | one-slide | structural | needs-company-data) → the standard gaps schema, with needs-company-data becoming a founder notification.

### Where humans sit

Four judgment touchpoints per packaging run, all mapped to existing machinery: **INTAKE** (form card — sources, verbatim words, audience); **COMPETE verdict** (the three angles + judge scores rendered as a choice card; human can overrule before WRITE spends tokens); **post-GATE founder pass** (read the gated draft, mark do_not_touch — the single highest-leverage taste moment); **ship approval** (external_write is structurally never grantable to children, goal_engine.go:896-908; ships only via resumeApprovedGoal, goal_engine.go:1363-1393 — inherited unchanged). Force-accept below threshold is always human (salvageBlockedDeliverable already rescues the best draft, goal_engine.go:1183-1247).

### Contradiction resolved: how are processes invoked?

The routing analysis proposes propose-confirm cards; the skills analysis warns chat-launched processes must "never route through keyword sniffing." These agree once stated precisely: **all process invocations — palette tile, /goal, voice, router card — post the same `runGoalPipeline` spec with the process id.** The router card is not a second invocation model; it is a fourth door to the single existing one. The fidelity fix (§2) is the precondition that keeps this true.

---

## 4. Artifacts

### Today: presentation-grade output cannot be displayed, period

An artifact is a `meetingMemoryEntry` of kind `os_artifact`: one plain-text body plus a string metadata map in `data/meeting-memory.jsonl` (memory.go:21, :388, :428). The viewer, `renderArtifactRead` (index.html:22111), is a deliberately injection-safe DOM renderer — headings, lists, blockquotes, pipe tables, everything through `textContent`. Consequences, verified:

- HTML decks display as **escaped source code** — no iframe, no srcdoc, no innerHTML of artifact content anywhere in index.html.
- PDFs are **unstorable** — no binary storage, no MIME concept, no ingestion path. Chat file attachments are inputs only (index.html:30856-30866).
- Presenter mode doesn't exist, even though the packaging deck template has it built in (deck-template.html:233-278).
- The **Deal Room** — the one investor-facing surface, GET /deal-room/{token} (deal_room.go:569) — escapes every span and supports only headings/lists/paragraphs (deal_room.go:676). An attached deck shows an investor its escaped source.
- Structural ceilings: PATCH capped at 256KB (main.go:1332), list capped at 100 entries (main.go:1369), every body inline in one JSONL rewritten whole per update.

For a company whose superpower is packaging, this is the single highest-leverage build in the OS.

### The build: first-class Artifact

**Data model** (a formalization — goal_engine.go:440-487 already writes most of this into the string map): `type` (markdown | html_deck | pdf | image | bundle), `contract` (exists as artifactContract), `assets[]` (content-addressed blob refs in `data/blobs/<sha256>` — JSONL stores refs only, killing the 256KB/rewrite ceiling), `version` lineage (every gate revision = new version), `provenance` (goalId, toolTemplate, model, gateOutcome, rubric scores, ASSUMED count, findingsArtifactId — the engine computes all of it today and flattens it), `status` extending today's vocabulary with `gated` and `approved` (riding the ExternalWriteGated approval seam, goal_engine.go:627-637), `interlocks[]` for the no-contradiction pairs.

**Viewer strategy:**
1. **Sandboxed iframe for HTML decks, first.** GET /artifacts/{id}/render with `text/html` + strict CSP (the deck template is fully self-contained), rendered in `<iframe sandbox="allow-scripts">` — no `allow-same-origin`, served from a token path that never carries session-cookie authority. Presenter mode comes free; the OS just needs a "Present" button opening the route fullscreen. Highest fidelity-per-line-of-code in the entire plan and touches no storage internals.
2. **Native PDF embed** once blobs exist — browser-native viewer, no PDF.js for v1.
3. **Tokened per-artifact share links** (GET /a/{token}) reusing the Deal Room/HMAC precedents (deal_room.go:569, main.go:752-784), gated **server-side at the route** to status=final + human approval, with expiry, revocation, and open/dwell logging — "investor opened the deck, reached slide 12" feeds straight into §5's brain.
4. **Renderer selection by type** in renderArtifactDetail (index.html:37351): markdown → today's safe renderer *unchanged* (do NOT loosen the escaping — fidelity comes from the separate sandboxed route), html_deck → iframe + Present, pdf → embed, bundle → primary + file list.

**Deal Room becomes an artifact gallery**: binder narrative stays as the escaped cover page; the package's `final` artifacts link to their full-fidelity render routes behind it. Binder tuples (packages.go:603) gain type/version/gateOutcome so the binder reads "Deck v3 — gated 9.2, presenter-ready," and package_assembly's interlock gate (§3) runs across the same set.

**PDF export — pulled forward by founder decision (2026-07-05).** The original plan deferred the render toolchain to Wave 5; the founder requires that a user who wants a PDF export of the deck + talk-track can get one from the OS, matching the /packaging skill's package spec. The cheap path the original estimate missed: the codex-runner sidecar pattern (same Go binary, a mode flag, file-per-job queue, authenticated callback) is exactly the right chassis. Wave 3 adds a **render-runner sidecar**: its own image layers chromium + poppler-utils over the base; an `export_pdf` job takes an html_deck artifact → headless-chromium print-to-pdf (`--no-pdf-header-footer --virtual-time-budget=15000`) → `pdftoppm -jpeg -r 144` → pure-Go JPEG→PDF reassembly (the skill's PIL step needs no Python — DCTDecode page imposition is ~200 lines of Go) → blob store → attached as a `pdf` asset on the same artifact, with an "Export PDF" button in the viewer. The talk-track/paper kit ("The Talk", "The Wall") renders from the paper-kit template as **text-native** PDFs (no blends, no flatten — direct print-to-pdf). The skill's production trap is law server-side too: never ship the layered print; the flattened ~5MB raster is the deliverable. This also pre-builds the rendered-page images Wave 5's vision slide juries need. What remains genuinely deferred: the in-OS imagery pipeline (Midjourney has no API) and vision juries themselves.

---

## 5. Compounding brain

### The 80/20: distill-and-inject exists, capture doesn't

Exists and maps directly: the typed append-only store (memory.go:57,124-142); the generic ambient-worker pattern — cursor advance, min-batch gating, run locks, archive-time flush (agent_runner.go:31-129) — of which five instances already run; the decision ledger with a reserved-but-unbuilt `superseded` status (decision_ledger.go:28-30); and a pinning precedent — the 12 newest decisions ride into every Scout answer unconditionally (memory_query.go:207, :962-975).

Missing: **any feedback capture at all** (grep confirms zero), anything per-user (one shared office store; hardcoded 6-person roster, participants.go:21), any interval analyst, and a real `save_what_worked` (goal_engine.go:1166-1173 just attaches the artifact — the stage name is the vision, the body is a no-op).

### Capture: free appends at existing seams, ranked by signal density

New kind `signal` — `{actor, event, valence, artifactId, packageId, payload}` — marked UI-state (memory.go:859-861) so raw signals never pollute recall. **No model calls at capture time**; tokens are spent only at distillation. That is "no token wasted," literally.

1. **Human edits to agent copy** (strongest signal in the system): PATCH /artifacts (main.go:1305-1372) has both the prior and replacement text in hand — store a section-level diff. A deleted section or a trimmed comps-table row *is* the taste data.
2. **Accepted vs regenerated**: a follow-up re-run = mild dissatisfaction, and the follow-up instruction text says exactly what was wrong (agent_thread_followup.go:22-35, :472-493); publish = strong accept; attachToPackage = kept; salvage = agent failure worth studying.
3. **Approve/reject reasons** (office_brief.go:433-471): a free, human-authored one-line critique currently thrown away. Log verbatim.
4. **Grill objections that landed**: at endGrillSession/endPrivateGrill (grill.go:120-176, :335-395), join the readiness delta vs the package's prior scorecard with what changed in between; recurring objections without score movement are unresolved truths.
5. **Open/ignore**: deliverArtifactToOrigin stamps deliveredAt; add openedAt on select_artifact. A never-opened deliverable is a negative signal on that tool for that user.
6. Quarantine restores, proposal confirm/dismiss, and **router-card dismissals from §2** — lower-frequency, pure signals.

### Surveys: garnish, not a surface

Rule zero: never ask what implicit signals already answered. Two inline chips — "landed" / "off" (+ one free-text line on "off") — on the existing completion cards and the goal-verified notification (goal finish() already notifies exactly once, goal_engine.go:1547-1573). Grill-end variant: render the scorecard's top objections as chips, ask "which one stung?" — converting model critique into human-validated critique. Server-enforced: max 1/user/day, never the same package stage twice, suppressed when implicit volume is already high.

### Distill and inject

**Taste Analyst** (sixth instance of agent_runner.go; per-user; weekly or ≥15 signals): reads the user's signal window, writes ONE living `user_profile` os_artifact — voice & style from edit diffs, recurring objections, comp taste, do/don't list — every bullet **evidence-cited** to signal ids, human-editable in the Artifacts pane (correcting your own profile is itself a signal). Proposes decision-ledger candidates and supersessions — which requires finally building the `superseded` path.

**House-Style Distiller** (per-office, monthly or on package-assembled): reads all profiles + published artifacts + rising-readiness grills + settled decisions; writes ONE living `house_style` entry — structures that survive grills, claims investors bought, banned patterns.

**Injection is pinning, not search** — recall is lexical (substring/token scoring, memory.go:863-935) and cannot be trusted to find profiles. Copy the decisions precedent: pin requester profile + house_style into buildAssistantQueryInput (beside memory_query.go:962-975) and into goalGroundingSlots (goal_engine.go:346-379) for deliverable subtasks; ground the private-grill question bank (grill.go:266-418) in house_style so the grill attacks the way this office's real investors do.

**The flywheel closing**: raw signals → distilled into living profiles (stamped `distilledInto`, then slop-eligible for compaction, slop_classifier.go:288) → hardest rules graduate to the decision ledger, which already rides into every answer. An edit made in March becomes a profile bullet in April, a house rule in June, and a constraint on every deck the OS generates in July. For §3 specifically: house_style is the standing brief for packaging_studio — judge panels gain a "house judge" persona distilled from real grill outcomes, rival teams inherit the banned-patterns list. This is how OS output stops looking like generic AI decks and starts looking like **Bonfire** decks.

**Guardrails**: bias the analyst to under-claim (six people = thin data; mirror the ledger's explicit-only discipline); decide deliberately whether profiles are self-visible-only; periodically generate a no-profile variant (or grill the house style itself) to prevent taste lock-in; and watch the JSONL store — signals are the highest-volume kind ever added to a file held in RAM and rewritten whole on metadata updates (memory.go:630-668). Compaction ships with the analyst, not after.

**Sequencing is the whole game here: capture ships first and alone.** Signals are worthless in week one and priceless in month three; every week without capture is unrecoverable training data.

---

## Build roadmap

Dependencies honored: OPS-3 gates everything agentic (launchGoalThread 503s keyless); the reviewer-input fix gates all adversarial structure; the viewer gates the packaging flagship's SHIP stage; capture gates the analysts; the fidelity fix gates the router.

### Wave 1 — Unblock and stop the bleeding (all S, ~days)
1. **Close OPS-3**: ANTHROPIC_API_KEY + BONFIRE_AGENT_RUNNER=anthropic_fable in the VPS env (verify /opt/meetingassist live env before and after); pin BONFIRE_CODEX_MODEL=gpt-5.5. Until this ships, Fable is dead code and grill scores run on an unpinned fallback. Confirm the Anthropic org is on 30-day retention.
2. **Raise the Fable dials together**: BONFIRE_DELIVERABLE_EFFORT=high, max_tokens 32K streamed, orchestrator timeout 5m → 15m+ (agent_runner_anthropic.go:57-59) — the timeout ships in the same change as the effort bump or high-effort runs manufacture failures.
3. **Signal capture** (kind=signal appends at the seams in §5) — unrecoverable data; ships before anything that consumes it exists.
4. **Fix the reviewer's eyes**: full artifact body to reviewOneSubtask/gate instead of compactAssistantLine (goal_engine.go:1065-1069).
5. **Fidelity fix**: paletteConversationalHandoff carries tool.id (index.html:31431-31437).
6. **Per-user in-flight goal cap** (limits are global today, goal_engine.go:297-300) — precondition for both the router and the flagship.

### Wave 2 — The front door and the window (S/M, ~1-2 weeks)
7. (M) Chat migrates to **Sonnet 5** (memory_query.go:204-211, agent_thread_followup.go:223-230 onto the existing Anthropic client); re-baseline token budgets for the tokenizer difference.
8. (M) **Propose-confirm router** on Haiku 4.5 + confirmation card (registry-injected schema; card reuses the palette form-card; "just answer instead" escape; dismissal logging). (S) Cut the pills and retire keyword sniffing **in the same release** so the quick lane never disappears. (M) User-facing goal cancel.
9. (S) **Sandboxed HTML render route + iframe viewer + Present button** — decks become viewable in-app immediately.
10. (S) Protect lists in the revision loop; deterministic law sweeps; candor rubric dimensions (data-only). (M) Refusal handling + server-side fallback + prompt caching in the Fable loop.
11. (S) Grill-delta signal; micro-survey chips; decision `superseded` path.

### Wave 3 — Primitives and the artifact spine (M, ~2-4 weeks)
12. (M) **Panel + gate primitives** as engine steps (goroutine fan-out, strict-JSON pattern); **close the grill loop** as first consumer (objection_ledger_v1, persona re-review, verified-fix readiness dial).
13. (M) **Blob store** (data/blobs/<sha256>) + native PDF embed; formalize the Artifact model (type/version/provenance); artifact-list pagination + generic download.
14. (M) **Tokened share links** with expiry/revocation/open-logging, server-side final+approved gating.
14b. (M) **PDF export pipeline** (pulled forward from Wave 5 by founder decision): render-runner sidecar on the codex-runner chassis (own image: chromium + poppler-utils), `export_pdf` job = print → rasterize 144dpi → pure-Go JPEG→PDF flatten → blob store → `pdf` asset on the artifact + "Export PDF" button; paper-kit (talk-track) renders text-native. Depends on item 13 (blob store).
15. (M) **Taste Analyst worker** + profile/house-style pinning into chat and goal grounding; signal compaction; make save_what_worked emit real lessons.
16. (M) Review-model split: BONFIRE_REVIEW_MODEL=claude-opus-4-8 via the assignedRunner pattern (goal_engine.go:737-754).

### Wave 4 — The flagship (L, ~4-6 weeks)
17. (M) **ProcessDefinition** (versioned Go structs, per-process budgets, per-stage checkpointing, served through GET /assistant/tools).
18. (L) **packaging_studio**: INTAKE → RED-TEAM → COMPETE → WRITE → GATE → VOICE → SHIP (HTML deck + **flattened deck PDF + The Talk via the Wave-3 render-runner** + rigor companion + findings record, all attached to the package). Imagery = client-supplied. Four human touchpoints wired to existing card/approval UI.
19. (M) **Deal Room artifact gallery** + package_assembly as the real interlock compiler.
20. (M) **House-Style Distiller**; house judge persona + banned-patterns inheritance into packaging_studio and grill.

### Wave 5 — Production polish (L, when the flagship earns it)
21. (L) Vision slide juries via the raw-content image seam (agent_runner_anthropic.go:96-99), consuming the render-runner's page images (toolchain itself moved to Wave 3 item 14b). (Later) API image provider for the imagery stage; memory-store split if signal volume demands it.

## What we are explicitly NOT doing

- **No generic workflow DSL.** 5-6 authored Go-struct processes, versioned in git, tested like wave11_palette_test.go. The moat is the pipelines, not a platform.
- **No silent intelligent routing.** The confirmation tap is mandatory for goal launches until per-user quotas exist — and probably after.
- **No ambient-worker migration off gpt-5.5.** Working, cheap, one seam. Opportunistic later, not now.
- **No single-vendor purity.** Voice stays OpenAI permanently; chase coherence on the text/agentic side only.
- **No in-app Midjourney.** Interactive browser sessions can't run server-side; imagery stays client-supplied until an API image provider lands. (The PDF-flatten pipeline was originally on this list; the founder pulled it into Wave 3 — item 14b — because the send-anywhere diligence packet is a real deliverable and the codex-runner sidecar pattern makes it cheap.)
- **No loosening of the markdown renderer's escaping.** Fidelity comes from the separate sandboxed route; the injection-safe renderer is correct for text.
- **No embeddings/vector search.** Pinning delivers the learned profiles reliably; lexical recall is fine for a 6-person office this year.
- **No new survey surface.** Two chips on existing cards, rate-limited server-side, or nothing.
