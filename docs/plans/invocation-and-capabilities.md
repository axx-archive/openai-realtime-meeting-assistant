# Invocation & Capabilities — THE answer and the build plan

**Date:** 2026-07-06 · **Status:** DECIDED — synthesis of three invocation-architecture positions + four feature specs, against the 2026-07-05 live sim results. All code claims below re-verified in source before writing.

---

## Founder summary (6 lines)

1. **Keep "just ask" as the front door.** The sim proved it: every outcome-phrased ask routed on first try, and Scout's memory-grounded answers twice *prevented* redundant work (LONG LIGHT, the already-built deck) — something no /command can ever do.
2. **The real bug isn't routing — it's that Scout doesn't know its own house.** The answer brain is told only what it *can't* do (memory_query.go:1153) and nothing it *can*, so a router miss dead-ends in "that's a bigger ask than I can spin up." Fix #1: teach it the full registry so every miss self-heals into "yes — that's the Packaging Studio staged run, want me to set it up?" with the same confirmation card a pill arms.
3. **Add /packaging — but as a guarantee, not the front door.** Bare `/packaging` opens the studio intake; `/packaging <brief>` arms a pre-filled confirmation card. Plus a deterministic match so the literal words ("end to end," "the full run," "packaging studio") can never lose to thread-context gravity again. Commands cap at three: /packaging, /grill, /research.
4. **The "+" menu is the discovery catalog for people who don't know the words** — and it currently teaches the wrong company. Packaging Studio renders LAST, below all 12 instruments. It moves to first tile, group renamed "End-to-end."
5. **One-off asks are protected by architecture, not vocabulary:** nothing ever launches without a tap (the propose-confirm law), genuine ambiguity gets quick-reply pills (verified working), and the already-recorded acceptance signals get a read surface with the written <50% tighten-the-trigger tripwire.
6. **Build order:** every server fix starts now (self-knowledge, router repairs, chat→brain distillation, upload/vision plumbing); everything touching index.html render regions (slash aliases, hero tile, attachment viewers) queues behind the in-flight design-spec implementation.

---

## Part 1 — THE INVOCATION ANSWER

### The decision

**All three doors stay, each with a distinct job, and none of them is new machinery — they are four spellings of one door.** Every path (ask → router card, "+" tile, "/" command, voice initiate_goal) already converges on the same `runGoalPipeline` spec, gated by the same confirmation card (verified: scout_chat_threads.go:600-646 — the router can only ever ADD a card, never launch; index.html:33030-33040 — the palette, /goal, and voice are documented siblings on one pipeline). The founder's question is therefore not "which door" but "what is each door FOR, and why did one ask die tonight." The answers:

| Door | Job | Who it serves |
|---|---|---|
| **Just ask** (router + Tier-0) | The zero-thought default. Outcome phrasing routes; questions get memory-grounded answers that can *prevent* redundant work. | Everyone, every day. The primary door. |
| **"+" palette** | Discovery: the browsable catalog for people who don't know what can be asked or what the words are. Teaches by position and by the card's gate/kill-condition copy. | Talent/ops, new members, "what can this thing do?" moments. |
| **"/" lane** | Determinism: the guaranteed path for people who DO know the words. `/goal` exists; `/packaging` joins it as the flagship's reserved spelling. | Power users, recovery after any miss, muscle memory. |
| **Confirmation card** | The shared trust surface all doors converge on. The ONLY launch door. Unchanged. | The whole system's over-routing insurance. |

### Why routing stays the default (resolving Analyst C vs. A/B on "add a command at all")

Analyst C argued against /packaging: the sim user *did* ask, twice, and a command wouldn't have saved either turn. That's true — and it's why the **self-knowledge fixes rank above the command in the worklist**. But C's own remedy proves the command's necessity: the capabilities digest will end capability answers with "ask for it directly or type /packaging" — a redirect needs a deterministic landing spot that cannot itself misroute. Three additional facts settle it:

1. **Prompt tuning demonstrably cannot close the flagship collision.** The intent map already pins "package this end to end" → packaging_studio verbatim (verified at scout_chat.go:375) and the router still missed, because scoutRouterInput folds the last 6 turns into the routing turn (verified scout_chat.go:451-466) and thread context dragged the verdict to package_assembly. When the map covers the exact phrase and still loses, the fix must move into code.
2. **The cost is near zero.** "/" already opens the palette inline with Enter-selects-top-match (verified index.html:34544-34567); `/packaging` uniquely matches the studio tile today. Completing the alias is a parser sibling of parseGoalCommand feeding the existing pipeline.
3. **"Package" is this company's most-frequent verb and its most collision-prone token** (package_assembly vs. packaging_studio). Highest frequency × heaviest run (~5-15 min staged, multi-agent) × worst name collision = exactly where a deterministic path earns its keep, and nowhere else.

**Verdict: adopt A's asymmetry with B's mechanics.** Routing is the front door for everyone; the flagship — alone among heavy flows — gets a guaranteed deterministic path; commands cap at /packaging + /grill + /research (Delta 1's exact table) and grow only if door-attribution signals demand it.

### Exact behavior of /packaging

- **Bare `/packaging`** → deterministic pin-select of the Packaging Studio tile (`paletteSelectTool`, no fuzzy-rank drift): the conversational intake handoff arms the visible template chip and asks for the founder's words, audience, and assets. Identical to tapping the tile — zero new launch machinery.
- **`/packaging <brief>`** → echoes the command as a user message, then posts a **pre-filled packaging_studio PROPOSAL CARD** (via the existing `scoutRouterProposalForToolID` shape) — *not* an instant launch. This deliberately diverges from `/goal` (which launches directly): the studio run is the heaviest thing in the OS, and the card's Run tap stays the only launch door. The card shows the weight label ("multi-agent goal loop, ~5-15 min"), gate rubric, and kill condition — the command is fast, the commitment is still one tap.
- **Inline-palette Enter interceptor** checks the alias table FIRST (before top-fuzzy-match), so `/packaging` + Enter is deterministic even mid-palette.
- **`/grill <brief>`, `/research <brief>`** behave identically for grill_pressure_test / deep_research. Footer kbd hints document all three.
- All forms converge on the same `runGoalPipeline` spec every other door posts.

### Deterministic pre-router guard (the flagship's second guarantee, for people who ask instead of slash)

Before the Haiku turn in `routeScoutChatTurn` (scout_chat.go:474), a lexical check commits `scoutRouterProposalForToolID` directly when the message is work-shaped (no leading negation, not a question) and contains either:
- an **exact registry tool/process name** ("packaging studio," "deck outline," "one-pager"), or
- a **short, reviewed full-run phrase list**: "end to end," "the full run," "full packaging run," "0 to 100" → packaging_studio.

Constraints that keep this out of the retired keyword-sniffing class (the analysis doc's "only silent heavy invoke" failure): it may only ever emit a **proposal card, never a launch**; the phrase list is capped and code-reviewed; and its dismissal signal is watched specifically. This is A's move 2 + B's move 4, merged.

### How one-off asks are protected from over-routing into full packaging

Five layers, four of which already exist:
1. **The propose-confirm law** (architecture, not vocabulary): nothing launches without a tap. A wrong card costs one dismissal.
2. **The under-route bias stays dominant**: "When in doubt, answer inline" remains the router's closing instruction; Tier-0 memory answers remain the two best moments the system produces.
3. **Forced disambiguation for the collision pair**: new intent-map rule — package_assembly is ONLY "compile already-made artifacts into the send-ready binder"; end-to-end/full-run language → packaging_studio *even when the thread was discussing an existing package*; genuinely torn → offer_choices between exactly those two ("compile what we have" / "the full staged run").
4. **One-off vocabulary joins the map, not the command table**: "business model / projections / unit economics / does the deal work" → economics_waterfall (the words appear nowhere in its name/promise today, so both fuzzy search and the router miss them — verified tool_registry.go:322-346). A dedicated projections tool is a signals-gated follow-up, not a now-build.
5. **The measurement tripwire is already written**: router_proposal_accepted / dismissed / router_choice_selected are recorded on every card action and pill tap (constants verified at scout_chat.go:286-293) with the policy "below ~50% acceptance, tighten the trigger." What's missing is a read surface — build it, and let the 6 people's actual taps decide any further balance question. Tonight is n=1; the S-fixes are justified by the observed miss class, re-architecture is not.

### How Scout's self-knowledge gets fixed (the night's worst bug, ranked #1)

The knowledge asymmetry is structural: the Haiku router receives all 13 registry ids + promises via `scoutRouterTools`/`buildToolsPayload` (verified scout_chat.go:385-400), the palette reads the same payload — but the Sonnet answer model that actually talks to users carries exactly one affordance instruction, and it is purely negative (verified memory_query.go:1153: "Do not claim to run research, design, grill, Codex… from this chat answer"). Every router under-fire — including the *deliberate* Tier-0 bias and all degraded paths (nil verdict = keyless AND router error AND undecodable call) — lands on a brain whose only safe move is denial. Adopt Analyst C's fix wholesale, in two stages:

**Stage 1 (S, now):** `assistantCapabilitiesDigest()` in tool_registry.go, generated from `buildToolsPayload()` — the same single taxonomy source the router enum and palette read, so it cannot drift. One line per tool (group · name · id), Packaging Studio described as the full end-to-end staged run, plus the three doors ("describe the work, type /, or tap +"). Append to `assistantQueryInstructions()` and **replace** the line-1153 prohibition with the offer-never-deny protocol: *when the ask is work on this list or a capability question, say yes, name it, and offer to set it up; never say work on this list is beyond this chat; you still cannot launch anything yourself.* Gate on `currentAnthropicAPIKey() != ""` (keyless deploys can't run goal loops — don't overpromise). Golden test caps digest length. Measured cost ~300 net tokens/turn ≈ $1 per 1,000 chat turns.

**Stage 2 (M, after Stage 1):** the `[[offer:tool_id]]` sentinel — the digest instructs the model to end a capability answer with one marker on its own final line; the three `resolveAssistantQueryContext` commit sites strip it, validate via `routerToolByID` (unknown ids strip silently), and commit the same Kind-"proposal" message a quick-reply pill commits. Result: "can you run the full studio?" → a yes, one sentence, and the armed card. Bounds: one marker max, final line only, explicit work asks only, strip the pattern from quoted context blocks, and new answer_offer_armed/accepted/dismissed signals under the same <50% rule. Server-only — the client already renders proposal messages, so this does NOT touch index.html.

**Plus the correction rule** in the router prompt: *when the user corrects a prior proposal or answer by naming a different tool or process ("no, the full Packaging Studio staged run"), the correction IS the work ask — propose the named id confidently; a correction is never Tier 0.* This closes the second turn of the sim failure; the deterministic name guard closes it redundantly (the follow-up named the process verbatim).

### Conflict resolutions, on the record

| Conflict | Resolution |
|---|---|
| C: "no /packaging command" vs. A/B: add it | **Overruled C on the command, adopted C on priority.** The digest's own redirects need a deterministic landing; cost is near zero; but self-knowledge ships first because the command alone would not have saved either sim turn. |
| C: prompt-only fix for the collision vs. A/B: deterministic guard | **A/B win.** The map already covered the exact phrase and lost to context-folding; certainty moves into code. C's prompt patches ship too (~95 tokens) — belt and suspenders on the flagship. |
| B: /packaging `<brief>` launches like /goal vs. weight concerns | **Proposal card, not launch.** /goal stays as-is; the studio's 5-15 min weight keeps the card mandatory (B's own risk note, adopted). |
| B: hero tile now vs. index.html freeze | **Split.** Delta 2's server-side reorder (processes group first, label "End-to-end") ships now — it also reorders the router's injected enum, a free nudge. The hero-tile render + copy pass wait for the design-spec implementation to land. |
| F2 spec: image generation direct vs. propose-confirm | **Direct, with disclosure** ("generating image…" status, then the async result). The law exists for multi-agent misfire economics; one gpt-image-2 call is cents, sub-minute, zero side effects. The multi-shot imagery BOARD stays behind propose-confirm. |
| F1 vs. F2: two competing upload endpoints | **Merged.** One door — POST /assistant/chat-uploads, F1's general 32MB shape with artifactBlobHandler's guards. The vision lane enforces its own image whitelist + 5MB Anthropic per-image cap at model-plumbing time. The GC fix (sweepUnreferencedBlobs walking chat refs) is non-optional in the same wave — otherwise an admin sweep deletes chat attachments. |

---

## Part 2 — THE BUILD PLAN

One ranked, dependency-ordered worklist: the four features + all invocation deltas. **START NOW** = server seams, no index.html render-region contact. **WAIT** = touches index.html render regions and queues behind the in-flight design-spec implementation.

### START NOW — server seams

**Wave A — self-knowledge + routing repair (the invocation core; all S; ship together)**

| # | Size | Item | Files / seams | Depends on |
|---|---|---|---|---|
| 1 | S | **Capabilities digest + offer-never-deny.** `assistantCapabilitiesDigest()` from buildToolsPayload; replace the :1153 prohibition; keyless-gated; golden length-cap test + a test asserting every router-enum id appears in the digest. | tool_registry.go, memory_query.go:1142-1156 | — |
| 2 | S | **Router prompt patches**: package_assembly-vs-packaging_studio disambiguation + forced offer_choices pair when torn; correction rule ("a correction is never Tier 0"); economics vocab line (projections/business model → economics_waterfall). ~95 Haiku tokens. | scout_chat.go:371-376 | — |
| 3 | S | **Deterministic pre-router guard**: exact registry/process names + reviewed full-run phrase list → scoutRouterProposalForToolID before the Haiku turn; work-shaped, non-negated messages only; propose-only, never launch. | scout_chat.go:474 (routeScoutChatTurn), reusing :541-558 | — |
| 4 | S | **Flagship-first payload reorder**: processes group built FIRST in buildToolsPayload, label → "End-to-end". Pinned tests updated in the SAME commit (wave11_palette_test.go:39-47, process_definitions_test.go:341, scout_chat_choices_test.go:235/356/365/412). Free router-enum nudge; no consumer keys on order (verified). | tool_registry.go:46, :503-527 + 3 test files | — |
| 5 | S | **Regression fence**: eval rows for the exact two-turn sim failure ("package this end to end — the full run" → "no, the full Packaging Studio staged run"), "can you run the full packaging studio from here?", "what can you actually do?" — asserting zero denial phrasing + armed offer where expected. Sim-table rows 13-15 in docs/plans/conversational-intents.md. | evals_test.go, docs/plans/conversational-intents.md | 1-3 |

**Wave B — recovery + measurement**

| # | Size | Item | Files / seams | Depends on |
|---|---|---|---|---|
| 6 | M | **[[offer:tool_id]] sentinel**: answer path arms the validated proposal card at the three resolveAssistantQueryContext commit sites; one marker, final line only, unknown ids strip silently, pattern stripped from quoted context. Server-only (client already renders Kind-"proposal"). | scout_chat_threads.go (~:646, :821, :924), memory_query.go | 1 |
| 7 | S | **Signals read surface + new events**: answer_offer_armed/accepted/dismissed beside the router constants; door-attribution field on the goal spec (server side); a simple acceptance-rate query/dashboard over router_proposal_accepted/dismissed/choice_selected. Apply the written <50% tighten rule with data; all future menu-vs-router balance decisions gate on this. | scout_chat.go:286-293, signals.go, scout_chat_threads.go:747-749/:873 | — |

**Wave C — compounding + media server halves (parallelizable with A/B)**

| # | Size | Item | Files / seams | Depends on |
|---|---|---|---|---|
| 8 | M | **F3 chat distillation lane** (whole feature — pure server): chat_distiller.go + tests; boot registration beside the slop classifier; code-enforced eligibility (Role==user / PostedOnBehalfOf, ack filter); quiet-debounce + staleness-floor gating; forward-carried per-thread cursors in chat_distill_pass entries; public channels → kind=brain (grounds Tier-0 search AND flows to ledger/board/mission-intel via existing throughBrainId cursors, zero new plumbing); private 1:1s → user-scoped lane only. Directly fixes the "channel-stated facts invisible to Scout" class. | chat_distiller.go (new), main.go ~:510, memory.go consts | — |
| 9 | M | **Unified upload door + refs + GC fix** (merged F1/F2 server): POST /assistant/chat-uploads (32MB, artifactBlobHandler's origin+session guards); Ref+Mime on scoutChatFileAttachment; sanitize via validBlobRef; **sweepUnreferencedBlobs walks scout_chat_thread refs — non-optional, same wave**. | scout_chat_uploads.go (new), scout_chat_threads.go:46/:1828, blobs.go:371, main.go | — |
| 10 | M | **Vision plumbing** (F2-A server): resolveAssistantQueryWithImages variant; Images []json.RawMessage on anthropicTextRequest; image blocks before the text block; existing 12-image/20MB budget validation is free; 25s→45s timeout only when images present; "Attached image: name" in model text for history/router. | memory_query.go, anthropic_text.go, scout_chat_threads.go:331, agent_runner_anthropic.go (reuse :1038) | 9 |
| 11 | S | **Image generation direct lane** (F2-B server): reuse createOpenAIImage/gpt-image-2 verbatim; direct (not proposed) with an immediate "generating image…" status message then the async result via the existing deliverScoutChatThreadUpdate push; files one dismissible design artifact; imagery board stays propose-confirm. Keyless → honest message. | openai_images.go (reuse), scout_chat_threads.go | 9 |

### WAIT — index.html render regions (queue behind the in-flight design-spec implementation)

| # | Size | Item | Files / seams | Depends on |
|---|---|---|---|---|
| 12 | S | **/packaging, /grill, /research aliases** (Delta 1): parseToolAliasCommand beside parseGoalCommand; bare alias → paletteSelectTool pin-select; `<brief>` form → pre-filled packaging_studio **proposal card** (not launch — deliberate divergence from /goal); inline-palette Enter checks the alias table first; footer kbd hints; frontend test pinning the table + that literal "/goal" still wins. | index.html :32999-33019, :34559-34567, :33184 | design-spec landing; 4 |
| 13 | M | **Flagship-first palette render + copy pass**: hero tile for Packaging Studio atop paletteRenderList (modal + inline), "Package this" affordance on the package/binder view opening the pre-filled studio card; composer placeholder/tooltip/footer copy refresh (replace stale "ask Scout about any meeting" at :18683). Check frontend_* copy-grep tests first. | index.html :33310 region, :18672-18683 | design-spec landing; 4 |
| 14 | M | **F1 client — attachments become doors**: upload-at-attach in scoutChatFilePayload (>32MB and failure toasts, metadata-only fallback); chips → image thumbs / pdf "open" / download links (DOM-built, no innerHTML — frontend_chat_links_test invariants); openAttachmentStage sibling reusing the artifact-stage chrome (shared wireArtifactStageKeys factor). | index.html :32834, :37064, :23340 region | design-spec landing; 9 |
| 15 | M | **F2 client — image pending chips + generated-image render** in-thread; composer image branch (whitelist + 5MB, degrade with toast). | index.html :32834, :32865, :37064 | design-spec landing; 9, 10, 11 |
| 16 | S | **Door-attribution client stamping** (button / slash / slash-packaging / router-card / pill / voice) onto runGoalPipeline specs, feeding item 7's dashboard. | index.html runGoalPipeline call sites | design-spec landing; 7 |
| 17 | — | **Device-matrix pass** on palette footer, bare-alias arming, mobile bottom-sheet, attachment stage ≤860px — rides the OPS-3 live VPS deploy, which remains open. | live deploy | 12-15; OPS-3 |

### Sizing note
No L items. Waves A+B are ~1 focused day of server work and close 100% of the sim's observed failure surface. Wave C is independent and parallelizable. The WAIT queue is ~2-3 days once the design-spec implementation lands and should ship behind it in one pass so index.html is touched once.

### Risks carried forward (owned, not ignored)
- **Deterministic guard regression into keyword-sniffing**: bounded structurally — proposal-only, capped reviewed phrase list, dismissal signal watched. "Package" alone never triggers it; only full-run phrases and exact names do.
- **Salesy digest / over-offering**: digest stays compact; "when in doubt answer inline" stays dominant; offers only on explicit work asks; the <50% acceptance rule governs the new lane identically.
- **Marker injection via quoted memory**: registry validation + final-line-only + quote-stripping; worst case is one inert card.
- **Prompt growth on the 700-token Haiku turn**: the pre-router guard deliberately moves certainty into code instead of prompt lines; net prompt add is ~95 tokens.
- **n=1 evidence**: item 7's read surface ships before any balance decision beyond these fixes; re-decide at the ~50% threshold with real taps from the 6 people.
- **Flagship prominence → accidental heavy launches**: the confirmation card (weight label + Run tap) remains mandatory on every door, including /packaging `<brief>`.
