# Model-Routing Master Plan — 2026-07-11

The complete, wave-sequenced plan to make Bonfire's model-selection/routing harness best-in-class
across OpenAI + Anthropic end to end, with zero intelligence drop-off on mission-critical lanes
(transcription fidelity, proposal→kickoff reliability), in service of the four product pillars:
(a) voice Scout anytime, (b) every meeting → company-brain artifacts, (c) every team workflow made
agentic and kickable from chat/channel/meeting-proposal with approval, (d) everyone aware of
everything when asked.

Produced by an 8-agent gated workflow (6 deep dives → synthesis → adversarial review).
**Gate: PASS at 8.8/10, round 1.** Reviewer verified 73/73 dive plan-items carried into the plan
(nothing silently deferred) and that every mission-critical eval precedes the model change it
protects. Companion document: `docs/llm-routing-audit-2026-07-11.md` (the underlying audit).

## Plan summary


Master plan merging all six dives into six dependency-sequenced waves. W0 builds the books (usage ledger, dated pricing, wire-seam instrumentation on all four provider surfaces, eval-event funnel, proposal/workflow provenance, rollup artifact + alerts) — nothing flips until baselines are frozen and console-verified. W1 ships the zero-eval fix pack and safe flips (router DisableThinking, voice-session prompt-gate defusal, realtime gate widen, base-URL seams, Codex→gpt-5.6-sol canary+flip, static vocab, fossil-doc kill). W2 builds and RUNS every mission-critical eval gate BEFORE the model changes they protect: the STT dual-model harness (transcription fidelity), the proposal-coverage + router golden-set harnesses (proposal→kickoff), brain-fleet parity (Luna), board-op fidelity (Terra), the realtime voice harness, recall-quality, review shadow, embeddings. W3 executes the gated flips one at a time, ledger-verified: transcription→gpt-4o-transcribe, Terra pins BEFORE the Luna flip, Luna, realtime-effort and orchestrator-effort pilots. W4 lands the config-not-code harness (runner registry, unified seat table with uniform cross-provider doctrine guards, Sonnet worker runner, Fable model_choice + cost-awareness prompt, per-seat fallback + breaker, shadow STT lane) plus proposal-chain hardening (idempotency, provenance, commitments sweep) and the workflow substrate (WorkflowDescriptor+RunnerPlan, toolTemplate proposal seam, scheduler). W5 makes the four pillars visible: channel/room-voice workflow kickoff, generalized meeting-heard suggestions, six authored processes, commitments engine, proactive morning brief, channel digests, proposal inbox, dynamic vocabulary, realtime 2.1 + private-voice mini pilots. Every dive plan_item lands in a wave or founder_decisions; duplicates merged and noted (router fix ×3, pricing table ×2, Terra pins ×2, Codex Sol ×2, channel parity ×2, voice-recall carve-out ×2, usage ledger ×2, standing approvals ×2, realtime gate widen ×2). Nothing on a mission-critical lane moves without its eval verdict; every flip has a one-env-var rollback.


## Metrics that prove it (no-drop-off + cost)

- TRANSCRIPTION FIDELITY GATE (runs BEFORE the flip): gpt-4o-transcribe must score WER ≤ gpt-realtime-whisper AND strictly higher domain-term recall AND p95 segment latency <3s on the ≥60-min/≥150-term corpus; reproducible ±0.5 WER points.
- TRANSCRIPTION LIVE (post-flip, from W0 instrumentation): correction-regex hit rate per sitting ≤ whisper baseline (each hit = a known term the model got wrong); .failed segments <2% per sitting (drop-off alert); attribution-confidence distribution stable; healthz shows vocab ON.
- TRANSCRIPTION COST: $0.017/min duration-billed → ~$0.006/min (~$0.66/room-hour, ~$160/mo heavy) — deterministic, ledger-verified before/after.
- PROPOSAL→KICKOFF RECALL GATE (runs BEFORE ambient flips): recall + precision on the human-labeled commitment corpus with per-stage loss attribution (died-in-brain / died-in-board / dropped-op); no regression across Terra, Luna, or the brain-contract change; commitments-sweep duplicate rate <20%.
- PROPOSAL FUNNEL (W0 taxonomy): time-to-proposal (minted − transcript ts), kickoff success rate (terminal-complete / launch), acceptance rate per source (board/suggestion/voice/router/sweep), median time-to-confirm and stale->24h count trending down after the inbox ships.
- ROUTER RELIABILITY: truncation (stop_reason=max_tokens) rate ~0 after DisableThinking; routing golden set per-workflow precision/recall with 100% hard-negative rejection; proposal-per-work-ask no regression.
- AMBIENT FLEET INTELLIGENCE (Luna gate): decision-extraction recall ≥98% of gold + Opus-judged coverage within the gpt-5.5 confidence band; live strict-JSON parse-failure delta ~0 fleet-wide for 1 week post-flip.
- BOARD FIDELITY (Terra gate): 100% card_id preservation, zero invented statuses, parse-failure delta ~0 in the ≥30-pass replay harness; live board-op error rate ≤ baseline (alert at >20%/24h).
- GATE-BY-RUNNER: Opus gate-failure rate per runner — anthropic_sonnet_worker ≤1.5x Fable over 2 weeks (model_choice adoption gate); shadow eval reports Opus-fail/Sonnet-pass asymmetry count (must be zero to ever move the gate).
- VOICE (gates effort-low, 2.1, and mini pilots): wake-gating precision/recall, tool-dispatch success, and false-response rate at/above the frozen gpt-realtime-2@high baseline on the ≥40-utterance harness; no malformed launch args in live A/B days.
- RECALL QUALITY (pillar d): per-lane groundedness / completeness / refusal-honesty scorecard at/above baseline for any memory change; semantic-lane recall@10 vs lexical-only measured.
- COST WINS (ledger-proven): ambient fleet ~80% cut (est $83-330/mo); orchestrator output tokens −10-20% at effort medium with re-run/needs_attention within noise; private-voice ~70% audio cut on the mini pilot; realtime output tokens down on effort-low days; Codex flip $0 delta (upgrade at same price).
- HARNESS HEALTH: per-seat fallback rate <5%/24h (alert above); price_missing entries = 0 (typo tripwire); ledger-vs-console spend agreement ±15%; boot seat map logged with zero unknown-id warnings; kickoff first-turn transport recoveries visible with retryUsed provenance and needs_attention rate down.

## Founder decisions (yours, separated out — nothing below assumes an answer)

1. AUDIO RETENTION (gates diarize cross-checks + real-meeting ground truth): store committed-segment audio in prod on a 72h rolling window? Storage is trivial (~170MB/room-hr); the question is purely privacy/comfort incl. guest-visible disclosure. Yes → W5 follow-on: segment store + nightly gpt-4o-transcribe-diarize QA over low-confidence/failed segments + real audio replacing the scripted corpus.
2. STANDING APPROVALS (card 069, merged from proposals + expansion dives): which workflow classes may auto_run, who may grant, does it expire? Proposed default: morning brief + channel digests auto (deterministic, no external writes); all model-written deliverables standard lane; external_write always heavy. Scheduler and suggestion agent ship propose-mode-only until ratified — nothing blocks on this.
3. SONNET SEP-1 PRICE STEP (+50% on every Sonnet seat, est +$13/mo light, +$53/mo heavy): RECOMMENDED eat it — every seat is product-voice or mission-critical, alternatives repeal never-Haiku (router, saves ~$5-19/mo) or break the Anthropic product-voice doctrine (Terra on human-visible seats). Decision packet with REAL W0 ledger numbers due 2026-08-15.
4. OPUS 4.8 STAYS ON THE REVIEW GATE — ratify as standing policy while any non-Anthropic runner writes gated artifacts (the Luna/Terra/Sol flips make this permanently true; the gate is what makes the cheap fleet AND model_choice routing safe). Revisit only if the W2 shadow eval shows verdict parity with ZERO Opus-fail/Sonnet-pass defects. Refusal fallback stays Opus forever (thinking-block replay is Anthropic-only).
5. VOICE-RECALL EFFORT-FLOOR CARVE-OUT (doctrine amendment): ratify 'spoken-turn recall may run effort=low for latency' (restores author intent, one seat, one flag, wired disabled in the seat table) — or strike it and fix the stale comment. Either answer is one boolean.
6. CHANNEL KICKOFF SEMANTICS (product behavior change for W5 channel parity): run the propose-confirm router on @scout channel mentions, and may ANY signed-in member confirm (recommended — matches the standard lane and room-card model), or mentioner-only? Also: retire or keep the legacy keyword instant-launch in channels?
7. SUGGESTION-AGENT TRUST CAP: may meeting-heard auto-proposals ever propose heavy-lane/external-write workflows? Recommended: cap at standard-lane (read_only/workspace_write) — passive listening proposing external writes is the wrong trust posture even behind the confirm gate.
8. GOOGLE CALENDAR: build the reserved OAuth integration (calendar.go seam, real build) so meeting_prep triggers from calendar sync, or trigger off scheduled rooms/key dates for now?
9. WEEKLY MEMO SEND CEREMONY: is a permanently-parked heavy gate per investor send acceptable, or is 'compile and park for admin send' the contract?
10. PRIVATE DASHBOARD VOICE METERING: ship the small browser usage beacon inside W0, or accept an explicit 'unmetered' rollup flag until W5? (The lane is structurally unmeterable server-side — browser owns the peer.)
11. TELEMETRY CALIBRATION: confirm SPEND_ALERT_DAILY_USD=75 (audit models heavy days ~$50) and rollup-artifact visibility (all roster users per the everyone-aware pillar, or admin-only).
12. LIVE CAPTIONS: does any planned surface want word-by-word live captions? One-line answer — if yes, the architecture is a DUAL lane (whisper-family display-only captions + gpt-4o-transcribe authoritative), never a return to whisper for persistence, and the STT harness adds a caption-latency metric.
13. DOCTRINE PARAMETERS FOR THE W4 HARNESS: (a) frontier never-list = strict allowlist on orchestrator/deliverable (recommended) vs refuse-known-cheap-tiers; (b) mixed-provenance runs acceptable (Opus answering a 429/529'd mid-run turn inside a Fable run, provenance-stamped — the refusal path already does this); (c) orchestrator effort-medium pilot scope: include /goal decompose/panel/report/verify (shared dial) or add a separate BONFIRE_GOAL_ENGINE_EFFORT dial; (d) ratify the workflow build order (followup_sweep + meeting_prep first) and RunnerPlan-as-registry-data seat doctrine.

## Complete change register (69 changes)

| # | Change | Type | Now | Target | Wave |
|---|--------|------|-----|--------|------|
| 1 | usage_ledger.go core (JSONL, seat tags, aggregates, kill switch) | code | No usage recording anywhere; Anthropic tokens parsed then dropped | Per-call ledger in docker volume, USAGE_LEDGER_DISABLED kill switch | W0 |
| 2 | models_pricing.go dated price table (merged: harness+telemetry duplicates) | code | No pricing data in code; prose docs only | Single dated source incl. Sonnet Sep-1 step-up row; warn on unknown ids | W0 |
| 3 | Anthropic seam instrumentation + cache_read/cache_creation token parsing | code | Usage structs miss cache fields — up to 10x cost error on Fable lane | Full token splits + seat tags recorded on every Anthropic call incl. errors | W0 |
| 4 | OpenAI Responses seam instrumentation (~15 ambient callers seat-tagged) | code | openAIResponsesBody parses no usage at all | Usage decoded + recorded per seat; Luna flip measurable per-seat | W0 |
| 5 | Realtime voice / transcription / images / embeddings / codex metering + transcription minutes+correction-hit ledger + healthz model surfacing | code | response.done usage unconsumed; no transcription telemetry; fossil pin silent | All lanes metered or explicitly flagged unmetered; healthz shows lane model + vocab on/off | W0 |
| 6 | Eval-event funnel: board-op fidelity, router outcomes+truncation, gate-by-runner, parse-failure counters, digest structural checks, transcript landing zone | code | Signals exist in-memory (meetingBoardRunResult, goalGate) but never aggregated | First-class eval series per lane feeding rollup + alerts | W0 |
| 7 | Proposal-chain event taxonomy + transcript-lineage stamps on proposals | code | Proposals carry no lineage; time-to-proposal uncomputable; dropped ops buried in generic error rail | Full minted/resolved/launch/terminal taxonomy with source + lineage | W0 |
| 8 | Workflow-run provenance ledger (trigger surface, approver, lane, seats, outcome) | code | Launches carry no trigger-surface provenance | Proposed→confirmed→launched→completed funnel countable via jq | W0 |
| 9 | Daily rollup worker + living Spend & Health artifact + /api/usage/rollup + alert engine | code | No spend visibility, no alerts | $0 deterministic rollup in company brain; 8 threshold alerts, 6h dedupe, separate kill switch | W0 |
| 10 | Baseline capture + console verification gate | eval | No baselines exist | 3-7 day frozen pre-flip baseline; ledger-vs-console ±15%; seat-coverage checklist | W0 |
| 11 | Scout router DisableThinking (merged: proposals+harness+seats duplicates) | code | scout_chat.go:737 omits it — thinking shares 700-token cap, silent proposal truncation | thinking:{type:disabled} + truncation rate ~0 | W1 |
| 12 | Voice-session transcription prompt gate + no-vocab alarm + startup config validation | code | sessionConfig sends prompt unconditionally (latent 2026-07-08-class bomb); fossil state silent | transcriptionModelAcceptsPrompt gated everywhere; degraded-fidelity warning event live | W1 |
| 13 | usesAdvancedCommandProfile widened (excl. -mini) (merged: harness+seats) | code | Exact-match 'gpt-realtime-2'; any bump silently drops effort pin | Family prefix match; -mini excluded until reasoning support verified | W1 |
| 14 | OPENAI_RESPONSES_BASE_URL / ANTHROPIC_BASE_URL override seams | code | Both wire URLs hardcoded consts | Env-readable funcs, unchanged defaults; gateway/Venice shelf-ready | W1 |
| 15 | BONFIRE_CODEX_MODEL flip (merged: harness+seats) | env | gpt-5.5 @ effort high ($5/$30) | gpt-5.6-sol @ high (same price, designated code tier) after sidecar canary | W1 |
| 16 | Codex sidecar CLI canary (id acceptance + 2-3 canary subtasks) | eval | Sidecar CLI support for 5.6 ids unverified | Canary verdict gating the Sol flip | W1 |
| 17 | meetingDomainVocabulary phase-1 gaps (StationTenn, fiscal.ai, Fable/Claude family, Codex, Resend, Luna/Terra/Sol, BonfireOS) | code | Static list missing a live client and the whole model vocabulary | Gaps closed; correction regexes only where W0 evidence shows mangling | W1 |
| 18 | Whisper fossil docs killed + live .env deliberate-vs-fossil annotation sweep | code | .env.example:8 + two READMEs re-seed the whisper pin on every fresh deploy; env lines silently detached from code defaults | Docs warn instead of reseed; annotated .env inventory in rollup artifact | W1 |
| 19 | codex_proposal broadcast scope | code | Office sockets only — named-room members miss live confirm cards | broadcastSignedInKanbanEvent (member rooms incl., guests excluded) | W1 |
| 20 | Ground-truth STT corpus + MEETING_TRANSCRIPT_CAPTURE tap | eval | No meeting audio retained anywhere; no reference corpus | ≥60min/≥150-term corpus through the real room path; tap inert unless enabled | W2 |
| 21 | Dual-model STT eval harness (transcription-fidelity gate) | eval | No STT measurement exists; flip would be blind | WER + domain-term-recall + latency verdict GATING the transcription flip; mini column for the record | W2 |
| 22 | meetingBrainInstructions commitments-verbatim contract | code | No commitments section; spoken work asks summarized away at effort=low | Mandated verbatim section both proposal engines read preferentially | W2 |
| 23 | Proposal-coverage eval harness (proposal→kickoff recall gate, meeting-heard path) | eval | No recall measurement; losses invisible | Recall/precision/per-stage-loss with CI + live modes; gates Terra, Luna, brain contract | W2 |
| 24 | Router golden set + confirm-funnel test (proposal→kickoff typed path) | eval | Only payload-shape tests exist | ~60-utterance precision/recall + one-launch-per-confirm assertion on all 4 paths; gates channel parity + generalized suggestions | W2 |
| 25 | Ask-anything recall quality eval | eval | recall_e2e tests plumbing, not answer quality | Per-lane groundedness/completeness/refusal scorecard; ship-checklist tripwire | W2 |
| 26 | Brain-fleet parity corpus (Luna gate) | eval | No ambient-quality measurement | ≥50-window Opus-judged 5.5-vs-Luna diff; ≥98% decision recall bar | W2 |
| 27 | Board-op fidelity replay harness (Terra gate) | eval | A2-documented sensitivity, no harness | ≥30-pass replay; 100% card_id / zero invented statuses bar | W2 |
| 28 | Realtime voice eval harness (gates all voice changes) | eval | No voice golden set; spoken kickoff fidelity unmeasured | ≥40-utterance wake/dispatch/false-response baseline on both session classes | W2 |
| 29 | Review-gate Sonnet shadow eval (log-only) | eval | Opus-vs-Sonnet gate question is doctrinal, not quantitative | ≥30 paired reviews: agreement, Opus-fail/Sonnet-pass asymmetry, $/review | W2 |
| 30 | Embeddings retrieval eval (recall@10 semantic lane) | eval | Semantic RRF lane's contribution unmeasured | Measured; 3-small kept (no successor exists); 3-large trial rule recorded | W2 |
| 31 | OPENAI_TRANSCRIPT_MODEL | env | gpt-realtime-whisper fossil ($0.017/min, no vocab prompt, deltas structurally unused; fallback lane transcribes BETTER than authoritative lane) | gpt-4o-transcribe + vocab prompt + NR (~$0.006/min, ~$160/mo saved) — only on STT-gate pass | W3 |
| 32 | OPENAI_BOARD_MODEL + OPENAI_SUGGESTION_MODEL pins (merged: proposals+seats) | env | Unset — both recall engines inherit OPENAI_BRAIN_MODEL | gpt-5.6-terra pinned BEFORE the Luna flip (never ride the cheapest tier, even transiently) | W3 |
| 33 | OPENAI_BRAIN_MODEL | env | Unset → gpt-5.5 across the 10-lane ambient fleet | gpt-5.6-luna (~80% fleet cut, est $83-330/mo) — only on parity-gate pass, after Terra pins verified | W3 |
| 34 | OPENAI_REALTIME_REASONING_EFFORT | env | high (live, applies to every turn incl. do_nothing) | low pilot — only on voice-harness parity | W3 |
| 35 | BONFIRE_ORCHESTRATOR_EFFORT | env | high (deliverables independently high) | medium 2-week pilot judged by gate/needs_attention/re-run metrics; scope per founder answer | W3 |
| 36 | Runner registry replacing the four selection switches | code | Closed enum; new provider = 4 switch edits | Init-time registration table; provider = 1 entry + 1 client file; silent-drop safety preserved | W4 |
| 37 | Unified seat-dial table (~25 seats) + cross-provider doctrine guards + boot validation | code | Doctrine guards Anthropic-only; OpenAI dials pass unvalidated; typo'd ids fail per-request in prod | One table, uniform never-lists, boot-logged resolved seat map, healthz-visible | W4 |
| 38 | Sonnet worker runner (anthropic_sonnet_worker) | code | Sanctioned in comments, NOT built | Registered parameterized runner; same loop, Opus gate unchanged; unreachable until stamped | W4 |
| 39 | Fable model_choice (frontier/standard) + generated cost-awareness prompt | code | Keyword heuristic only; Fable has zero price knowledge | Validated tier field with deliverable-forced-frontier + kill switch; ~200-token cached price block | W4 |
| 40 | Per-seat same-call fallback + circuit breaker + first-turn kickoff retry (merged: harness+proposals kickoff-resilience) | code | Single wire failure = permanent needs_attention; no per-seat fallback; chat refusals hard-error | One-replay fallback with provenance, per-provider breaker, first-orchestrator-turn transport retry; >5% alert | W4 |
| 41 | OPENAI_TRANSCRIPT_SHADOW_MODEL A/B lane | code | No in-prod STT comparison instrument | Inert-unless-set tee with divergence reports; memory-store leak impossible by construction | W4 |
| 42 | Private-voice server-side call_id dedupe | code | Browser retry/double-POST double-launches (room path protected, private not) | Per-user TTL ledger; call_id required for launch-class tools | W4 |
| 43 | Room-voice proposal provenance | code | proposedBy='' + no origin_room_id weakens listen-only gate + audit trail | origin_room_id + proposedBy=room_voice + optional arming speaker | W4 |
| 44 | Per-meeting commitments sweep (recall backstop) | code | No backstop; missed commitments unrecoverable | Meeting-close diff sweep, ≤5/meeting, dedupe vs proposals+cards+threads, kill flag | W4 |
| 45 | WorkflowDescriptor registry + per-workflow RunnerPlan | code | Two disjoint registries; runner seats keyword-heuristic only | Unified descriptor view; RunnerPlan resolves through the W4 seat vocabulary (no fork) | W4 |
| 46 | toolTemplate through the proposal seam | code | proposeCodexTask accepts 5 single-thread modes only; approved proposals never reach the goal engine | Any registered workflow proposable + confirmable, launched via launchGoalThread, feature-gated first deploy | W4 |
| 47 | Scheduled workflow lane | code | No recurrence mechanism exists | Registry-declared schedules → proposals (or 069-approved auto-run); restart-idempotent | W4 |
| 48 | Standing-approvals surface (ticker Case B activation — build half) | code | Fully gated, fully inert — nothing writes laneApprovedBy | Admin surface stamping ratified classes; grill/external_write stay hard-excluded | W4 |
| 49 | Channel @scout propose-confirm parity (merged: proposals+expansion) | product | Channels launch only on exact keyword/prefix; other work asks leave no trace | Router cards in channels, any-member confirm, origin delivery; gated on router golden set | W5 |
| 50 | Room-voice propose_workflow_run tool | product | Room voice cannot kick registered workflows | Confirm-first workflow proposal card by voice mid-meeting | W5 |
| 51 | Generalized suggestion agent (any-workflow meeting-heard proposals) | product | Research-only suggestions | Registry-driven autoProposeTriggers, standard-lane cap, all safety properties kept | W5 |
| 52 | Authored processes wave 1: followup_sweep + meeting_prep | product | Only packaging_studio uses the process runtime | Two daily-cadence reference processes registered + golden-eval'd | W5 |
| 53 | Authored processes wave 2: weekly_memo, fiscal_analysis, backlog_triage, diligence_pack | product | Manual/absent (40-card triage done by hand 2026-07-06) | Four registered processes; memo scheduled; sends park at heavy lane | W5 |
| 54 | Commitments engine (per-person records) | product | cross_meeting_briefing per-person hook documented NOT built | First-class commitment memory kind; 'what did I commit to' answerable | W5 |
| 55 | Proactive Morning Brief delivery | product | GET /assistant/brief exists; nobody is told | Scheduled per-user push/email, suppressed-when-empty, opt-out | W5 |
| 56 | Channel catch-up digests | product | No channel digest tier; brief shows counts only | Rolling per-channel digest + catch_me_up on chat and both voice surfaces | W5 |
| 57 | Proposal inbox + stale re-nudge | product | Proposals age silently after the bell scrolls | Dashboard rail + one daily >24h summary nudge | W5 |
| 58 | Dynamic per-room vocabulary (phase 2) | code | Static prompt; entity ledger + rosters don't feed it | Roster + entity-ledger + card-title terms, capped, session-refresh rebuilt, shadow-A/B'd cap | W5 |
| 59 | Attribution confidence + lost-speech surfacing | product | Confidence computed but never surfaced; .failed segments invisible | Sitting summary, UI low-confidence flags with human correction, >2% drop-off alert | W5 |
| 60 | OPENAI_REALTIME_MODEL | env | gpt-realtime-2 | gpt-realtime-2.1 (price-identical, better interruption/noise) — gate widened W1, harness-gated | W5 |
| 61 | OPENAI_PRIVATE_REALTIME_MODEL (new dial) + 2.1-mini pilot | code | Private voice shares the room dial (main.go:1893) | Separate dial; mini pilot on AJ's dashboard only (~70% audio cut) | W5 |
| 62 | Voice-recall effort-floor carve-out build (merged: harness+seats) | code | Authored 'low' silently clamped to medium at anthropic_text.go:83 | Seat-scoped exemption IF ratified; else comment fix | W5 |
| 63 | Audio retention decision (72h rolling segment audio) | decision | No meeting audio stored | Founder yes/no + window + guest exclusion; unlocks diarize cross-check + real-meeting ground truth + failed-segment re-transcription | founder |
| 64 | 069 standing-approvals policy (classes, granter, expiry) (merged: proposals+expansion) | decision | Mechanism built + inert; nothing writes laneApprovedBy | Ratified matrix (proposed: brief+channel digests auto; model-written standard; external_write always heavy) | founder |
| 65 | Sonnet Sep-1 intro-price expiry (+50%) | decision | Sonnet cluster at intro $2/$10 | Recommended: eat it (+$13-53/mo); packet with real ledger numbers by Aug 15 | founder |
| 66 | Opus 4.8 review-gate standing policy | decision | Reconciled-audit position, not ratified policy | Gate model coupled to runner provenance while any non-Anthropic runner writes gated artifacts; revisit only on shadow parity | founder |
| 67 | Voice-recall effort-floor doctrine carve-out | decision | Blanket no-below-medium doctrine contradicts authored intent on one seat | Ratify 'spoken-turn recall may run low' or strike + fix comment | founder |
| 68 | Channel kickoff semantics + suggestion-agent lane cap + never-list strictness + mixed-provenance fallback + orchestrator-pilot scope | decision | Undecided product/doctrine parameters | Any-member channel confirm; standard-lane cap on heard proposals; strict frontier allowlist; Opus-in-Fable-run fallback acceptable; pilot scope answered | founder |
| 69 | Google Calendar OAuth vs key-date triggers; weekly-memo send ceremony; live-captions requirement; private-voice beacon vs unmetered; SPEND_ALERT_DAILY_USD=75; workflow order + RunnerPlan seat doctrine ratification | decision | Open per dives | One-line founder answers packaged with the W0/W2 evidence | founder |

## Waves


### W0 — Telemetry: the ledger gates everything
**Objective:** Real books for every LLM seat plus the proposal/workflow provenance funnel, so every later flip has a frozen before-baseline and an automatic after-delta. No model change ships before this wave's gate.

#### usage_ledger.go — core ledger, seat tags, kill switch
Mutex-guarded O_APPEND daily-rotated JSONL (usage-YYYY-MM-DD.jsonl / eval-YYYY-MM-DD.jsonl) under filepath.Dir(meetingMemoryPath())/usage — docker volume, never the stale /opt/meetingassist/data trap. llmUsageEntry with token splits incl. cached, audio, duration, est_cost_usd, PriceMissing, FallbackUsed. withLLMCallTags(ctx) seat-tagging with zero signature changes; untagged calls record seat=untagged so gaps are visible. In-memory 24h/7d rolling aggregates; USAGE_LEDGER_DISABLED kill switch; 90-day retention sweep. MERGED: telemetry item 1 + seats dive W0 item (duplicate).
- **Files:** /Users/ajhart/meetingassist/usage_ledger.go
- **Acceptance:** Concurrent-write, rotation, disabled-mode, silent-drop-on-write-failure, and ctx round-trip tests pass; in-container smoke shows entries in the volume path.
- **Rollback:** USAGE_LEDGER_DISABLED=1 or revert the new file.
- **Effort:** M

#### models_pricing.go — single dated price table
Model id → {in, cachedIn, out, sourceDate} for every seat default and live dial: fable-5 $10/$50 (cache ~$1), opus-4-8 $5/$25, sonnet-5 $2/$10 with dated $3/$15 row EffectiveFrom 2026-09-01, gpt-5.5, gpt-5.6-sol/terra/luna, realtime-2/2.1 audio+text, 2.1-mini, gpt-4o-transcribe $0.006/min, gpt-realtime-whisper $0.017/min, gpt-image-2, text-embedding-3-small. priceFor() warns loudly on unknown ids — the typo'd-env-flip tripwire. Consumers: ledger est_cost, boot seat validation, Fable cost prompt. MERGED: harness item 1 + telemetry item 2 — built exactly once, owned here.
- **Files:** /Users/ajhart/meetingassist/models_pricing.go
- **Acceptance:** Test iterates every seat default + repo model-id constant and asserts a priced row; date-boundary math (Sonnet step-up), cached discount, duration billing, unknown-id warn-once all tested.
- **Rollback:** Inert data file; revert.
- **Effort:** S

#### Anthropic wire-seam instrumentation + cache-token parsing
Add cache_read_input_tokens/cache_creation_input_tokens to all three usage structs (agent_runner_anthropic.go:261-264, :505-507, :518-520) and the SSE fold — without this the Fable lane's books are up to 10x wrong. recordLLMUsage in createAnthropicMessagesResponseHTTP with latency/status, error entries for 429/529 storms. Seat-tag every caller: orchestrator/deliverable, refusal-fallback (FallbackUsed), goal decompose/panel/report/verify, review_gate, chat/memory_answer/followup/attachments/narrative/taste/house_style, router.
- **Files:** /Users/ajhart/meetingassist/agent_runner_anthropic.go, /Users/ajhart/meetingassist/anthropic_text.go, /Users/ajhart/meetingassist/goal_engine.go, /Users/ajhart/meetingassist/scout_chat.go, /Users/ajhart/meetingassist/memory_query.go
- **Acceptance:** Mock-responder tests: one entry per call with correct seat; cached tokens parsed from SSE fixture; fallback writes two entries; live Fable run shows CachedInputTokens >> InputTokens on later turns.
- **Rollback:** Kill switch no-ops recording; decode fields are inert.
- **Effort:** M

#### OpenAI Responses seam instrumentation — the ambient fleet
Add Usage (input/output/cached) to openAIResponsesBody (openai_responses.go:40-51), record in createOpenAITextResponseHTTP. Seat-tag all ~15 consumers: brain, board, suggestions, decision/entity ledger, meeting/company digest, mission_intel, slop, recall, thread_legacy, keyless-fallback twins. This makes the W3 Luna flip measurable per-seat automatically.
- **Files:** /Users/ajhart/meetingassist/openai_responses.go, /Users/ajhart/meetingassist/brain_worker.go, /Users/ajhart/meetingassist/board_worker.go, /Users/ajhart/meetingassist/suggestion_agent.go
- **Acceptance:** Mocked-responder test carries all three token fields + correct seat; each worker test emits its seat string; live brain entries ~5min cadence.
- **Rollback:** Kill switch; additive decode.
- **Effort:** M

#### Realtime voice + transcription + images + embeddings + codex metering (incl. transcription minutes ledger + correction-hit counter)
MERGED: telemetry item 5 + transcription dive item 1. (a) Room voice: decode response.done usage (kanban.go:161-168, handler :2691) with audio/text/cached splits — verify usage arrives over the WebRTC datachannel first, else wall-clock estimate flagged Estimated. (b) Transcription lane: per-segment record {room, model, source transcript_lane|scout_realtime, audio_seconds via transcriptionLaneAudioSamples/24000, completed|failed, attribution speaker+confidence, correction_hits}; instrument canonicalizeDomainTerms (memory.go:685) — every regex hit is a known-term mistranscription, the free live vocab-error proxy; count .failed + reconnects per sitting; surface effective lane model + vocabulary_prompt on/off in healthz and the lane-connected broadcast so the whisper fossil is LOUD (today: gpt-realtime-whisper / vocab OFF). (c) openai_images.go per-generation, embeddings.go usage.prompt_tokens, codex sidecar job-level entry or explicit unmetered flag. (d) Private dashboard voice: unmetered flag or browser beacon per founder decision.
- **Files:** /Users/ajhart/meetingassist/kanban.go, /Users/ajhart/meetingassist/transcription_lane.go, /Users/ajhart/meetingassist/memory.go, /Users/ajhart/meetingassist/openai_images.go, /Users/ajhart/meetingassist/embeddings.go
- **Acceptance:** Live meeting: room-voice entries carry nonzero audio tokens; transcript-lane duration within ~5% of wall-clock; healthz shows whisper/vocab-OFF; correction-hit rate per sitting queryable; unmetered lanes explicitly flagged in rollup.
- **Rollback:** Kill switch; decode fields inert.
- **Effort:** L

#### Quality eval events: board-op fidelity, router outcomes, gate-by-runner, parse-failure counters, digest checks, transcript landing zone
MERGED: telemetry items 6+7. Fold meetingBoardRunResult into eval events (op_count, error classes — the regression alarm for the Terra pin); router_turn outcomes incl. stop_reason=max_tokens as 'truncated' (validates the DisableThinking fix) + proposal confirmed/dismissed at settleProposalNotification; gate verdicts tagged by runner (goal_engine.go:95) — arms the per-runner gate-failure metric the model_choice adoption gate needs; parse-failure counters on every strict-JSON lane (the designated gate metric for any flip, Luna included); deterministic digest structural checks (LLM judge default-off); reserved lane=transcript metric names (transcript_wer, vocab_hit_rate, speaker_attribution_rate) that the W2 STT harness writes through.
- **Files:** /Users/ajhart/meetingassist/board_worker.go, /Users/ajhart/meetingassist/notifications.go, /Users/ajhart/meetingassist/goal_engine.go, /Users/ajhart/meetingassist/decision_ledger.go, /Users/ajhart/meetingassist/slop_classifier.go
- **Acceptance:** Unsupported board op → error_count=1 with right class; mint→confirm pair joined by proposalID; failed gate writes runner-tagged event; forced unparseable output increments seat's series; synthetic transcript_wer renders in rollup.
- **Rollback:** Kill switch; leaf hooks removable independently.
- **Effort:** M

#### Proposal-chain event taxonomy + transcript-lineage stamps
Proposals dive item 1 — the Dive-2 measurement contract: proposal_minted {source: board_worker|suggestion_worker|room_voice|private_voice|chat_router|deterministic_guard|commitments_sweep, fromBrainId, throughTranscriptId+CreatedAt}, proposal_resolved, proposal_launch {path}, kickoff_terminal, voice_tool_call, board_pass with droppedOpsMissingArgs split out of the generic error rail, suggestion_pass. Locks the derived metrics: proposal recall, time-to-proposal, kickoff success rate, acceptance rate per source, stale age. Keep new metadata out of Scout search context.
- **Files:** /Users/ajhart/meetingassist/codex_proposals.go, /Users/ajhart/meetingassist/board_worker.go, /Users/ajhart/meetingassist/scout_chat.go, /Users/ajhart/meetingassist/workflow_ticker.go
- **Acceptance:** Every new proposal carries source + lineage; a day of prod traffic yields computable time-to-proposal and kickoff-success from events alone; dropped propose-ops reported separately.
- **Rollback:** Stop emitting; all new fields optional to readers.
- **Effort:** M

#### Workflow-run provenance ledger
Expansion dive item 1: one durable record per workflow launch + terminal outcome — workflow id+version, trigger surface (palette/goal-door/chat-router/channel/room-voice/private-voice/suggestion-agent/scheduler), proposer+approver+lane, runner seats used, terminal status, duration. Rides run_log memory kind + JSONL twin beside usage_ledger so per-workflow cost = join(provenance, tokens). Grep-checklist of all launchAgentThreadWithSpec callers.
- **Files:** /Users/ajhart/meetingassist/agent_thread_runner.go, /Users/ajhart/meetingassist/goal_engine.go, /Users/ajhart/meetingassist/codex_proposals.go
- **Acceptance:** Every launch path stamps surface + workflow id; jq one-liner reproduces the proposed→confirmed→launched→completed funnel for 7 days; zero unattributed launches in full test run.
- **Rollback:** Metadata keys inert if unread.
- **Effort:** S

#### Daily rollup worker + living 'LLM Spend & Health' artifact + /api/usage/rollup + alert engine
MERGED: telemetry items 8+9. Deterministic $0 Go fold → daily/7d/MTD spend by seat×model×provider with cached split, fallback/parse-failure/board-error rates, router funnel, gate-by-runner, transcript scores, price_missing + unmetered callouts; filed as ONE living company-brain artifact (everyone-aware pillar) + JSON endpoint beside /healthz. Alert engine (15-min, env-tunable, 6h dedupe, separate USAGE_ALERTS_DISABLED): fallback>5%/24h, parse-failure>3x trailing, spend>2.5x or >$75/day, any price_missing, board-op errors>20%, router truncation>10% or confirm-rate −50%, gate-failure>1.5x by runner, transcript-fidelity floor breach — via existing createNotification/web push.
- **Files:** /Users/ajhart/meetingassist/usage_rollup.go, /Users/ajhart/meetingassist/usage_alerts.go, /Users/ajhart/meetingassist/notifications.go, /Users/ajhart/meetingassist/main.go
- **Acceptance:** Fixture fold matches hand-computed numbers; artifact updates in place; each threshold fires exactly once per 6h window in tests; no false positives during baseline soak.
- **Rollback:** Unregister worker/endpoint; USAGE_ALERTS_DISABLED=1.
- **Effort:** L

#### Baseline capture + verification gate (process)
Telemetry item 10: (1) seat-coverage checklist vs the audit call-site inventory — any absent seat is an instrumentation bug; (2) 3-7 days baseline with alarms armed; (3) ledger vs OpenAI+Anthropic console spend within ~±15%; (4) frozen pre-flip baseline section in the artifact; (5) flips then proceed ONE at a time with automatic before/after deltas.
- **Acceptance:** Checklist passes (all lanes metered or flagged unmetered); console verification recorded; baseline snapshot exists; first W1 flip's delta renders automatically.
- **Rollback:** N/A — observational.
- **Effort:** S

**Wave gate:** All expected seats appear in the ledger (or are explicitly flagged unmetered); 3-7 days of baseline captured with zero false-positive alerts; ledger-vs-console agreement ±15%; pre-flip baseline (per-seat cost, parse-failure, board-op error, gate rates, correction-hit rate, proposal funnel) frozen in the rollup artifact. Nothing in W1+ proceeds without this.


### W1 — Fix pack + no-eval env flips
*Depends on: W0 — Telemetry: the ledger gates everything*

**Objective:** Ship every change that needs no eval: latent-bomb defusals, the mission-critical router fix (a pure reliability improvement), the price-identical Codex upgrade, fossil kills. All one-line-class, all instantly reversible.

#### Scout router DisableThinking:true + truncation telemetry
MERGED (3 dives: proposals item 3, harness fix-pack part 2, seats item 11). Add DisableThinking to routeScoutChatTurn's anthropicMessagesRequest (scout_chat.go:737-749), copying anthropic_text.go:105. Today Sonnet-5 adaptive thinking shares the 700-token cap with the tool call → truncated tool_use → silent degrade to inline answer → a typed work ask produces NO proposal card. Fidelity improvement on the mission-critical proposal seat, not a cost tradeoff. Test mirrors anthropic_text_test.go:81-82.
- **Files:** /Users/ajhart/meetingassist/scout_chat.go
- **Acceptance:** Payload carries thinking:{type:disabled} (unit test); W0 router_turn truncated-rate falls to ~0 over a week; proposal-per-work-ask rate does not regress.
- **Rollback:** One-line revert.
- **Effort:** S

#### Voice-session transcription hardening: prompt gate + no-vocab alarm + startup validation
Transcription item 6, promoted from W4 because it defuses a live config bomb: sessionConfig (kanban.go:1293-1300) and privateRealtimeVoiceSessionConfig send the transcription prompt UNCONDITIONALLY — a whisper-family pin on OPENAI_REALTIME_TRANSCRIPTION_MODEL would break the entire voice session exactly like prod broke 2026-07-08. Gate through transcriptionModelAcceptsPrompt; startup-log effective transcription config for both lanes; persistent warning event whenever the AUTHORITATIVE lane runs without vocabulary biasing (the current fossil state should be screaming).
- **Files:** /Users/ajhart/meetingassist/kanban.go, /Users/ajhart/meetingassist/realtime_config_test.go
- **Acceptance:** Unit tests: whisper-family config omits prompt/NR, gpt-4o family includes them; unknown ids log a startup warning; the no-vocab warning fires today and clears after the W3 flip.
- **Rollback:** Revert; no behavior change on gpt-4o-family configs.
- **Effort:** S

#### Widen usesAdvancedCommandProfile (exclude -mini)
MERGED (harness fix-pack part 1 + seats item 8 step 1). kanban.go:1494-1497 exact-matches 'gpt-realtime-2'; any model bump silently drops the live effort=high reasoning block. Prefix-match the gpt-realtime-2 family EXCLUDING ids containing 'mini' until 2.1-mini's session.reasoning support is live-verified. Hard prerequisite to the W5 realtime flips; zero behavior change under the current pin.
- **Files:** /Users/ajhart/meetingassist/kanban.go, /Users/ajhart/meetingassist/realtime_config_test.go
- **Acceptance:** Table test: gpt-realtime-2 → true, 2.1 → true, 2.1-mini → false, junk → false.
- **Rollback:** Revert.
- **Effort:** S

#### Base-URL override seams (OPENAI_RESPONSES_BASE_URL / ANTHROPIC_BASE_URL)
Harness fix-pack part 3: convert both hardcoded wire consts (openai_responses.go:17, agent_runner_anthropic.go:42) to env-readable funcs with unchanged defaults. Keeps venice_chat.go and any future gateway shelf-ready without building a provider.
- **Files:** /Users/ajhart/meetingassist/openai_responses.go, /Users/ajhart/meetingassist/agent_runner_anthropic.go
- **Acceptance:** Wire tests: byte-identical defaults when unset; honored when set.
- **Rollback:** Revert.
- **Effort:** S

#### BONFIRE_CODEX_MODEL → gpt-5.6-sol (canary, then flip)
MERGED (harness item 8 + seats item 6). Same $5/$30 price, OpenAI's designated complex-code tier, string passes to the Codex CLI as --model (codex_runner.go:118,356). Pre-flight: sidecar smoke job confirms the installed CLI resolves the id, then 2-3 canary subtasks comparing gate outcomes vs gpt-5.5, then droplet .env flip. Terra ($2.50/$15) recorded as a founder option only if Sol parity holds; default recommendation is Sol (execution writes code).
- **Files:** droplet /opt/meetingassist/deploy/digitalocean/.env, /Users/ajhart/meetingassist/codex_runner.go
- **Acceptance:** Sidecar accepts the id; canaries complete with Opus gate-pass ≥ baseline; next 10 execution subtasks clean; ledger bills under the new id.
- **Rollback:** BONFIRE_CODEX_MODEL=gpt-5.5 + restart.
- **Effort:** S

#### Domain vocabulary phase 1 — close the static gaps
Transcription item 7 phase 1 (phase 2 lands W5): add StationTenn (live client, ABSENT today), fiscal.ai, Fable/Anthropic/Claude/Sonnet/Opus, Codex, Resend, Luna/Terra/Sol, BonfireOS to meetingDomainVocabulary (domain_terms.go:37-60). Correction regexes only where W0 correction-hit data shows mangling evidence. Immediately improves the fallback (voice-peer) lane which already carries the prompt.
- **Files:** /Users/ajhart/meetingassist/domain_terms.go
- **Acceptance:** Additive constants + tests; StationTenn transcribes correctly in a scripted spot check on the voice-peer lane.
- **Rollback:** Revert additive constants.
- **Effort:** S

#### Kill the self-reseeding whisper fossils in docs + live .env fossil sweep
Doc half of transcription item 4, safe now (the prod flip itself waits for the W3 gate): deploy/digitalocean/.env.example:8 → commented-out override with a WARNING that whisper disables domain-vocab biasing; README env block ~line 38 and README.md:133 corrected. Plus the flagged W0-adjacent sweep from the transcription open questions: diff live droplet .env against code defaults, annotate every line deliberate-override vs fossil (e.g. OPENAI_REALTIME_REASONING_EFFORT=high restates the default; OPENAI_REALTIME_MODEL=gpt-realtime-2 gates behavior) so no future default flip silently detaches.
- **Files:** /Users/ajhart/meetingassist/deploy/digitalocean/.env.example, /Users/ajhart/meetingassist/deploy/digitalocean/README.md, /Users/ajhart/meetingassist/README.md
- **Acceptance:** Fresh-deploy docs no longer instruct the whisper pin; annotated .env inventory recorded in the rollup artifact.
- **Rollback:** Docs revert.
- **Effort:** S

#### Live proposal visibility beyond office sockets
Proposals item 8, pulled forward (one-line broadcast-tier change, low risk, advances proposal→kickoff latency): switch codex_proposal broadcasts from broadcastOfficeKanbanEvent (kanban.go:6719) to broadcastSignedInKanbanEvent (:6745) so confirm cards render live in named rooms mid-sitting; guests excluded; id-deduped.
- **Files:** /Users/ajhart/meetingassist/codex_proposals.go
- **Acceptance:** Proposal minted mid-sitting renders live in a named room without rejoin; guests never receive it; office unchanged.
- **Rollback:** Revert to broadcastOfficeKanbanEvent.
- **Effort:** S

**Wave gate:** 48h clean: router truncation ~0 in the ledger, zero session-config errors, Codex canary green and flipped, no baseline drift on any seat. The no-vocab warning is now visibly screaming — motivating, not blocking, the W3 transcription flip.


### W2 — Mission-critical eval gates (built and RUN before the flips they protect)
*Depends on: W0 — Telemetry: the ledger gates everything*

**Objective:** Concrete measurement designs for the two mission-critical lanes — transcription fidelity and proposal→kickoff — plus the parity/fidelity harnesses gating every W3 model change. Standing instruments, not one-offs.

#### Ground-truth STT corpus: scripted reads through the real room path + env-gated capture tap
Transcription item 2: 15-20 scripts, 30-60s, dense in domain vocab (StationTenn, Boot Barn, fiscal.ai, WebRTC/HEVC/DTLS acronyms, all 7 names) + adversarial conditions, read INSIDE a real room so audio traverses mixer → 24kHz mono downmix (transcription_lane.go:586). MEETING_TRANSCRIPT_CAPTURE=1 (default off, test rooms only) writes segment PCM+transcript+attribution for human-corrected references.
- **Files:** /Users/ajhart/meetingassist/transcription_lane.go, /Users/ajhart/meetingassist/scripts/
- **Acceptance:** ≥60 min audio, ≥150 tagged term occurrences, ≥2 acoustic conditions, manifest maps every WAV to verified reference text; tap provably inert when unset (unit test), never enabled in prod without founder sign-off.
- **Rollback:** Delete data/transcript-eval/, unset env.
- **Effort:** M

#### Dual-model STT eval harness — THE gate for the transcription flip
Transcription item 3: replay identical WAVs through BOTH models over the exact prod wire path (wss intent=transcription, real session config incl. prompt gating), real-time paced. Scores normalized WER, domain-term recall per tagged occurrence, proper-noun error rate, per-condition breakdown, commit→completed p50/p95. GATE: gpt-4o-transcribe WER ≤ whisper AND strictly higher domain-term recall AND p95 <3s. gpt-4o-mini-transcribe as third column for the record (documents why the cheap option is refused on a mission-critical lane). Results write through the W0 transcript-fidelity landing zone.
- **Files:** /Users/ajhart/meetingassist/scripts/
- **Acceptance:** Machine-readable pass/fail verdict + report; reproducible within ±0.5 WER points across two runs.
- **Rollback:** N/A — read-only tooling.
- **Effort:** M

#### Brain write-up commitments contract
Proposals item 4: mandated 'Commitments and requested work' section in meetingBrainInstructions (brain_worker.go:157) with keep-verbatim discipline (who said it, exact deliverable phrasing, transcript id; 'None' when empty) — the single choke point both the board worker and suggestion agent read; stops summarization loss at the source. Board/suggestion prompts read it preferentially. Verified within brainMaxOutputTokens 2400. Gated by the proposal-coverage harness below.
- **Files:** /Users/ajhart/meetingassist/brain_worker.go, /Users/ajhart/meetingassist/board_worker.go
- **Acceptance:** Seeded commitment-bearing windows produce the section verbatim with transcript ids; proposal-coverage recall improves vs baseline; live artifacts show the section within a day.
- **Rollback:** Revert instruction strings.
- **Effort:** S

#### Proposal-coverage eval harness — the recall gate
Proposals item 5: human-labeled corpus (commitments that MUST propose, discussions that must NOT — precision protects confirm trust) driven through the real chain (brain → board → suggestion → proposeCodexTask capture) with recorded fixtures for CI plus a live-keyed mode for env-flip verification (the gpt-4o prompt-param lesson: mocks miss what prod catches). Reports recall, precision, per-stage loss attribution (died-in-brain / died-in-board / dropped-op). GATES: the Terra pins, the Luna flip, the commitments contract, every future ambient change.
- **Files:** /Users/ajhart/meetingassist/evals_test.go, /Users/ajhart/meetingassist/board_worker_test.go
- **Acceptance:** Runs in CI deterministic + live on demand; baseline recall/precision per stage recorded; new tests explicitly pinned into the gate list (TestFrontend lesson).
- **Rollback:** Test-only.
- **Effort:** M

#### Proposal→kickoff router golden set + funnel test
Expansion item 13 (complements the coverage harness — this covers TYPED asks, that covers meeting-heard): ~60 utterances across chat + channel phrasings per registered workflow + hard negatives, expected verdict {workflow|choices|none}, fixtures for CI + live mode; deterministic funnel test asserting every confirm path (HTTP, chat-accept, voice, ticker) lands exactly one launch with provenance. Ships as the ship-gate for W5 channel parity and the generalized suggestion agent.
- **Files:** /Users/ajhart/meetingassist/router_eval_test.go, /Users/ajhart/meetingassist/evals_test.go
- **Acceptance:** Per-workflow precision/recall reported; hard negatives route to none at 100%; funnel test green on all four confirm paths.
- **Rollback:** Test-only.
- **Effort:** M

#### Ask-anything recall quality eval (pillar-d gate)
Expansion item 14: seeded memory fixture + ~40 Q&A pairs across chat ask-anything, voice answer_memory_question, cross-meeting briefing, catch-me-up; scored on groundedness, completeness, refusal honesty; judge = review seat. The regression tripwire for any memory/recall change.
- **Files:** /Users/ajhart/meetingassist/recall_quality_eval_test.go
- **Acceptance:** One-command per-lane scorecard; sabotaged fixture fails it; baseline recorded in-repo; wired into the ship checklist for memory_query/digest/commitments changes.
- **Rollback:** Test-only.
- **Effort:** M

#### Brain-fleet parity corpus — the Luna gate
Seats item 2: ≥50 real transcript windows scored by an Opus-4.8 judge rubric (decisions/action-items/entities/facts) gpt-5.5 vs gpt-5.6-luna; decision-extraction recall vs hand-labeled gold; memory-recall QA pairs. Include the spikiest windows (48-transcript backfills, digest folds). Repeatable for every future ambient change.
- **Files:** /Users/ajhart/meetingassist/brain_worker.go, /Users/ajhart/meetingassist/decision_ledger.go
- **Acceptance:** Scored diff emitted; Luna gate = decision recall ≥98% of gold AND coverage within the 5.5 confidence band on all axes.
- **Rollback:** Offline harness.
- **Effort:** M

#### Board-op fidelity harness — the Terra gate
Seats item 4: replay ≥30 recorded brain write-ups + fixture board through produceMeetingBoardUpdate; assert real card_ids, legal statuses, tool-allowlist respect, strict parse; gpt-5.5@medium vs gpt-5.6-terra@medium side by side (the in-code A2 comment documents this lane degrading observably under weaker settings).
- **Files:** /Users/ajhart/meetingassist/board_worker.go
- **Acceptance:** Terra: 100% card_id preservation, zero invented statuses, parse-failure delta ~0 vs baseline.
- **Rollback:** Offline harness.
- **Effort:** M

#### Realtime voice eval harness — gates every voice change
Seats item 7: golden set ≥40 PCM-injected utterances over the keyless smoke rig — kickoff commands (assert launch_agent_thread args), memory questions, unaddressed chatter (no false wake), interruptions; asserts on oai-events datachannel + dispatch logs; drives both session classes. Freezes the baseline that the W3 effort pilot and W5 2.1/mini flips must meet — spoken proposal→kickoff dispatch is mission-critical.
- **Files:** /Users/ajhart/meetingassist/kanban.go, /Users/ajhart/meetingassist/realtime_config_test.go
- **Acceptance:** Repeatable wake precision/recall, tool-dispatch success, false-response rate reported as the frozen baseline on gpt-realtime-2@high.
- **Rollback:** Harness only.
- **Effort:** L

#### Review-gate shadow eval — Sonnet judges in the dark, Opus keeps the seat
Seats item 14: log-only Sonnet-5 shadow review beside goal_engine.callReviewModel for 2-3 weeks; measure verdict agreement, the dangerous Opus-fail/Sonnet-pass asymmetry, $/review from the ledger. Input to the founder's standing-policy packet; production behavior unchanged.
- **Files:** /Users/ajhart/meetingassist/goal_engine.go
- **Acceptance:** ≥30 paired reviews; agreement rate + asymmetry count with examples + measured cost delivered.
- **Rollback:** Delete the shadow call.
- **Effort:** M

#### Embeddings retrieval eval — keep 3-small, prove it
Seats item 15: recall@10 of the semantic RRF lane vs lexical-only on the memory QA corpus (no successor generation exists — verified). Decision rule recorded: 3-large trial only if semantic lane measurably trails (note: NOT env-only — embeddingDims=1536 hardcoded, full reindex).
- **Files:** /Users/ajhart/meetingassist/embeddings.go
- **Acceptance:** Recall@10 reported; decision rule recorded.
- **Rollback:** Eval only.
- **Effort:** S

**Wave gate:** Every W3 flip has its verdict IN HAND: STT gate (WER ≤ whisper AND domain-recall strictly higher AND p95<3s), Luna gate (≥98% decision recall + coverage band), Terra gate (100% card_id, zero invented statuses), voice baseline frozen, proposal recall/precision + recall-QA baselines recorded. A failing verdict BLOCKS its flip — no exceptions on mission-critical lanes.


### W3 — Gated model flips (one at a time, ledger-verified)
*Depends on: W1 — Fix pack + no-eval env flips; W2 — Mission-critical eval gates (built and RUN before the flips they protect)*

**Objective:** Execute the eval-gated env flips in protection order: recall-critical Terra pins land BEFORE the Luna flip so the proposal engines never ride the cheapest tier even transiently; each flip gets its automatic before/after delta and armed regression alarms.

#### Transcription flip: OPENAI_TRANSCRIPT_MODEL → gpt-4o-transcribe
Transcription item 4 (flip half; docs already fixed in W1), executes only on STT-gate pass: delete/replace the fossil pin on the droplet, restart. The authoritative company-brain input gains the domain-vocab prompt + near-field NR it currently forfeits to its own fallback lane; kills the absurd quality inversion. Deterministic ~$0.66/room-hr (~$160/mo heavy) saving at 2.8x price delta.
- **Acceptance:** Lane-connected broadcast shows the new model; healthz shows vocab ON, no-vocab warning cleared; zero 'prompt parameter' errors over 48h; W0 correction-hit rate ≤ whisper baseline over following meetings.
- **Rollback:** Single env var back to gpt-realtime-whisper + restart; whisper path preserved behind transcriptionModelAcceptsPrompt forever.
- **Effort:** S

#### Terra pins FIRST: OPENAI_BOARD_MODEL + OPENAI_SUGGESTION_MODEL = gpt-5.6-terra
MERGED (proposals item 2 + seats item 5). Both proposal-recall engines currently inherit OPENAI_BRAIN_MODEL; pinning them to Terra ($2.50/$15, OpenAI's stated 5.5 successor for tool work) BEFORE the Luna flip is the sequencing that protects the mission-critical proposal lane. Gated by the W2 board-op fidelity harness + proposal-coverage baseline.
- **Acceptance:** Board artifacts stamp model=gpt-5.6-terra; 24h+ of board_pass/suggestion_pass telemetry shows no drop in proposeOps, no rise in droppedOps; 2 weeks: proposal approval rate and proposals/meeting-hour ≥ baseline.
- **Rollback:** Unset pins (falls back to brain model) or pin gpt-5.5 + restart.
- **Effort:** S

#### Luna flip: OPENAI_BRAIN_MODEL → gpt-5.6-luna (after Terra pins verified)
Seats item 3, gated by the W2 parity corpus: 10-lane ambient fleet + keyless fallbacks + legacy openai_text runner inherit automatically. ~80% fleet cost cut (est $83-330/mo saved). Cursor-not-advanced self-heal bounds blast radius; parse-failure fleet alarm armed.
- **Acceptance:** Parity eval passed FIRST; then 1 week live: strict-JSON parse-failure delta ~0, board consumes brain output cleanly, no rise in slop false-quarantines, ledger confirms the cut.
- **Rollback:** Unset OPENAI_BRAIN_MODEL + restart.
- **Effort:** S

#### OPENAI_REALTIME_REASONING_EFFORT high→low pilot
Seats item 9, gated by the W2 voice harness: run the golden set at low vs high, then 3-5 day alternating live A/B reading ledger output-token deltas + dispatch logs. Spoken-kickoff argument fidelity is the metric that may not degrade.
- **Acceptance:** Harness at low within noise of high on dispatch success + wake gating; live days show no missed/misfired responses or malformed tool args; measurable audio-output-token reduction per meeting-hour.
- **Rollback:** Restore =high + restart.
- **Effort:** S

#### BONFIRE_ORCHESTRATOR_EFFORT=medium pilot (non-deliverable turns)
Seats item 13: independent dial verified (deliverables stay high on BONFIRE_DELIVERABLE_EFFORT). 2 weeks judged entirely by W0 gate metrics; note orchestratorEffort() also feeds goal decompose/panel/report/verify — scope per founder answer (separate dial if he wants decompose kept at high).
- **Acceptance:** ≥20 runs: Opus gate-failure, needs_attention, turns-per-run, re-run count within noise of the high baseline; deliverables unaffected; est 10-20% orchestrator output-token cut in ledger.
- **Rollback:** Unset (default high) + restart.
- **Effort:** S

**Wave gate:** 1-2 weeks live per flip: parse-failure delta ~0 fleet-wide, board-op errors ≤ baseline, correction-hit rate ≤ whisper baseline, voice dispatch + gate rates within noise, and the ledger confirms the cost deltas. Regressions revert via single env vars before W4 builds on the new seat map.


### W4 — Harness: config-not-code + proposal-chain hardening + workflow substrate
*Depends on: W3 — Gated model flips (one at a time, ledger-verified)*

**Objective:** The architecture that makes every FUTURE swap a config change with doctrine guards enforced uniformly across both providers, plus the idempotency/provenance/backstop hardening on the proposal→kickoff chain and the registry substrate the W5 product wave rides.

#### Runner registry — collapse the four switches
Harness item 3: runner_registry.go registration table replacing the closed enum's four switches (agent_runner_iface.go:159-268), preserving exact semantics incl. alias matrix, keyless degrade, and resolveAssignedRunnerName's silent-drop safety property; StaticCaps closes the never-called AgentCapabilities gap. New provider = one registration + one client file.
- **Files:** /Users/ajhart/meetingassist/runner_registry.go, /Users/ajhart/meetingassist/agent_runner_iface.go
- **Acceptance:** Golden matrix over every (env value × key presence × legacy worker) combination byte-identical to pre-refactor output (golden written FROM old code before deletion); full suite green.
- **Rollback:** Revert commit; no schema/env/data changes.
- **Effort:** M

#### Unified seat-dial table (~25 seats) + uniform doctrine guards + boot validation
Harness item 4: one declarative seat_config.go — provider, model dial+default, effort dial+floor, max-tokens, doctrine class with never-lists applied to BOTH providers (extends today's Anthropic-only doctrineModelOrDefault to OpenAI dials — kills the typo'd-id-fails-silently class), fallback target. Accessors become thin table reads, call sites unchanged. Boot validateSeatMap logs the full resolved map, warns on any id missing from pricing, surfaces on healthz. Voice-recall floor carve-out wired but DISABLED pending founder decision.
- **Files:** /Users/ajhart/meetingassist/seat_config.go, /Users/ajhart/meetingassist/agent_runner_anthropic.go, /Users/ajhart/meetingassist/openai_responses.go
- **Acceptance:** Empty-env golden: every accessor byte-identical to today; haiku-refusal parity on all Anthropic dials PLUS cheap-tier refusal on OpenAI reasoning seats; one boot log line per seat; live droplet values resolve identically.
- **Rollback:** Accessor signatures unchanged — revert restores old bodies.
- **Effort:** L

#### Sonnet worker runner (the sanctioned fan-out seam, built)
Harness item 5: parameterize newAnthropicFableRunner by seat — same tool loop, allowlist, Opus refusal fallback, budgets — model from BONFIRE_WORKER_MODEL (default claude-sonnet-5) through the seat table's doctrine guard; registered as anthropic_sonnet_worker; NOT selectable as default orchestrator (never-list forbids it there); reachable only via per-subtask assignedRunner.
- **Files:** /Users/ajhart/meetingassist/agent_runner_anthropic.go, /Users/ajhart/meetingassist/runner_registry.go
- **Acceptance:** Integration test: stamped subtask runs the full loop on sonnet-5, hits the Opus gate unchanged, stamps provenance; keyless degrades like anthropic_fable; haiku id refused to default with warning.
- **Rollback:** Remove registry entry — stamped values silently degrade to default.
- **Effort:** M

#### Fable-emitted model_choice (frontier|standard) + cost-awareness system prompt
Harness item 6: optional per-subtask tier vocabulary (never raw ids) in the decompose schema; mapping forces deliverables frontier in code, unknown values fall through to today's behavior (the assignedRunner degrade pattern); BONFIRE_GOAL_MODEL_CHOICE=off kill switch. costAwarenessPromptBlock() generated FROM the pricing table + seat map (~200 tokens, rides the cached system-prompt breakpoint). Adoption gate: keep enabled iff Sonnet-worker gate-failure ≤1.5x Fable's over 2 weeks (W0 metric).
- **Files:** /Users/ajhart/meetingassist/goal_engine.go, /Users/ajhart/meetingassist/agent_runner_anthropic.go
- **Acceptance:** Absent/junk/'haiku' values → default runner (golden); 'standard' on deliverable overridden to frontier with logged note; prompt block <250 tokens, tracks table changes; kill switch yields byte-identical plans.
- **Rollback:** BONFIRE_GOAL_MODEL_CHOICE=off + restart.
- **Effort:** M

#### Per-seat same-call fallback + per-provider circuit breaker + first-turn kickoff retry
MERGED (harness item 7 + proposals item 9 — the kickoff-resilience retry is the same mechanism scoped to the run's zero-side-effect first turn). Generalize the one sanctioned pattern (refusal replay-once with provenance): text seats gain tryPrimary-then-fallback on transport/429/5xx/refusal/empty (existing OpenAI twins promoted); orchestrator refusal branch extends to 429/529 (Anthropic-only forever — thinking-block replay constraint) INCLUDING the first orchestrator turn so a single wire failure no longer lands a fresh kickoff at needs_attention; NO mid-run retries (partial side effects); per-provider breaker (3 fails → 10-min cooldown → half-open); fallback provenance in ledger + artifact metadata; >5%/24h alert.
- **Files:** /Users/ajhart/meetingassist/seat_config.go, /Users/ajhart/meetingassist/anthropic_text.go, /Users/ajhart/meetingassist/agent_runner_anthropic.go, /Users/ajhart/meetingassist/memory_query.go
- **Acceptance:** Fault-injection: 429 → exactly one fallback with provenance; both-fail → unchanged error path; simulated 529 on first turn recovers transparently with retryUsed stamped, mid-run failure still lands needs_attention; breaker open/half-open/close tested; no path calls primary twice.
- **Rollback:** BONFIRE_SEAT_FALLBACK=off (+ BONFIRE_LAUNCH_RETRY_DISABLED).
- **Effort:** L

#### Shadow A/B transcription lane — the standing STT instrument
Transcription item 5: OPENAI_TRANSCRIPT_SHADOW_MODEL (unset = fully inert) tees byte-identical PCM segments to a second WS session; sidecar JSONL only (NEVER the memory store — enforced by construction), drop-on-backpressure, per-sitting divergence report. Post-flip monitoring now; the measured on-ramp for every future STT model (realtime diarization, new families).
- **Files:** /Users/ajhart/meetingassist/transcription_lane.go, /Users/ajhart/meetingassist/transcription_lane_test.go
- **Acceptance:** Paired per-segment transcripts + divergence report in a test room; primary metrics unchanged shadow-on; kill test — shadow WS down, primary unaffected; leak-impossibility unit-tested.
- **Rollback:** Unset the env var — path inert.
- **Effort:** M

#### Private-voice tool-call idempotency (server-side call_id dedupe)
Proposals item 10: browser forwards realtime call_id; server keeps per-user TTL ledger (mirror handledCalls) rejecting duplicates idempotently; call_id required for launch-class tools (launch_agent_thread, initiate_goal, propose_codex_task), tolerated-missing for read-only during rollout. Closes the double-launch hole the room path already guards (kanban.go:2992 vs :3301).
- **Files:** /Users/ajhart/meetingassist/main.go, /Users/ajhart/meetingassist/kanban.go
- **Acceptance:** Double-POST same call_id → exactly one thread, same payload twice; deduped count in voice_tool_call telemetry.
- **Rollback:** Drop enforcement — ledger becomes no-op.
- **Effort:** M

#### Room-voice proposal provenance (origin room + speaker)
Proposals item 11: stamp origin_room_id from the session's actual room + proposedBy=room_voice (fixes the empty-proposedBy listen-only-gate weakening at kanban.go:3080-3084); optionally attach the arming speaker via speakerForCommittedTranscriptForRoom.
- **Files:** /Users/ajhart/meetingassist/kanban.go, /Users/ajhart/meetingassist/codex_proposals.go
- **Acceptance:** Voice proposal carries originRoomId + room_voice provenance; listen-only sittings refuse voice mints (test); telemetry separates voice-minted acceptance rates.
- **Rollback:** Revert stamping.
- **Effort:** S

#### Per-meeting commitments sweep — the missed-commitment backstop
Proposals item 6, gated by the W2 proposal-coverage harness: meeting-close pass re-reads the full sitting's brain write-ups, enumerates commitments, Jaccard-diffs against proposals AND cards AND threads, mints misses via proposeCodexTask with source=commitments_sweep + heard-at reason; never auto-runs; ≤5/meeting; listen-only respected. The safety net under every upstream loss point (brain windows, ≤2/pass conservatism, baseline-advance, dropped ops, router truncation).
- **Files:** /Users/ajhart/meetingassist/meeting_digest.go, /Users/ajhart/meetingassist/codex_proposals.go, /Users/ajhart/meetingassist/linkage.go
- **Acceptance:** Deliberately starved commitment yields a sweep proposal within minutes of close; duplicate rate <20% on the corpus; acceptance-rate telemetry from day one.
- **Rollback:** BONFIRE_COMMITMENTS_SWEEP_DISABLED (ships with the feature).
- **Effort:** L

#### WorkflowDescriptor registry overlay + per-workflow RunnerPlan
Expansion item 2: unify packagingTools (13) + processDefinitions into one descriptor view — intake schema, trigger surfaces, approval lane, deliverable contract, filing rule, RunnerPlan {writerSeat, reviewSeat, effort} resolving through the W4 seat-table vocabulary (one seat vocabulary, no fork — the flagged convergence). Goal engine consults RunnerPlan before the keyword heuristic (heuristic stays as fallback). Additive on GET /assistant/tools.
- **Files:** /Users/ajhart/meetingassist/workflow_registry.go, /Users/ajhart/meetingassist/tool_registry.go, /Users/ajhart/meetingassist/process_definitions.go, /Users/ajhart/meetingassist/goal_engine.go
- **Acceptance:** Endpoint serves identical ids/groups (golden); RunnerPlan'd process runs its writer on the declared seat (provenance-asserted); no RunnerPlan → byte-identical fallback; existing payload gates pass.
- **Rollback:** Delete overlay; keyword heuristic resumes.
- **Effort:** M

#### toolTemplate through the proposal seam — highest-leverage unlock
Expansion item 3: proposeCodexTask accepts registry-validated toolTemplate + typed intake; launchApprovedProposal routes toolTemplate proposals through launchGoalThread; ticker crash-recovery follows; persist-before-launch + revert kept byte-for-byte; goal-cap hits park honestly. Unlocks channel, room-voice, meeting-heard, and scheduled kickoff of ANY registered workflow.
- **Files:** /Users/ajhart/meetingassist/codex_proposals.go, /Users/ajhart/meetingassist/workflow_ticker.go, /Users/ajhart/meetingassist/goal_engine.go
- **Acceptance:** Confirmed packaging_studio proposal produces an instantiated process (not a single thread); launch failure reverts to proposed; listen-only + origin delivery hold; non-toolTemplate proposals byte-identical; cap-hit path tested.
- **Rollback:** BONFIRE_PROPOSAL_TOOLTEMPLATE feature gate for first deploy.
- **Effort:** M

#### Scheduled workflow lane — registry-declared recurrence
Expansion item 7: Schedule field on descriptors; scheduler mints standard-lane proposals at the appointed time, or launches directly through the ticker's existing shape-B seam ONLY for workflows carrying ratified 069 standing approval (inert-by-default until the founder decision lands). Persisted last-fired high-water mark for restart idempotence. Never a new execution path.
- **Files:** /Users/ajhart/meetingassist/workflow_schedule.go, /Users/ajhart/meetingassist/workflow_ticker.go
- **Acceptance:** T+1min test schedule mints exactly one proposal across restarts; with laneApprovedBy it launches attributed 'standing approval: <who>'; without ratified 069 nothing auto-runs (proven in test).
- **Rollback:** Env flag; schedules go dormant, zero data impact.
- **Effort:** M

#### Standing-approvals surface (ticker Case B activation — build half)
Proposals item 12 build half (decision half in founder_decisions, merged with expansion ratification 1): after the founder ratifies the policy matrix, build the admin surface stamping lane=auto_run + laneApprovedBy onto approved workflow classes, riding the already-shipped, already-gated ticker seam (grill + external_write hard-excluded, maxPerPass=2).
- **Files:** /Users/ajhart/meetingassist/workflow_ticker.go, /Users/ajhart/meetingassist/approval_lanes.go
- **Acceptance:** Matching proposal launches on next ticker pass with full attribution; revoking stops future launches; everything else still requires fresh confirm.
- **Rollback:** Strip lane metadata or BONFIRE_WORKFLOW_TICKER_DISABLED.
- **Effort:** M

**Wave gate:** Golden selection tests byte-identical pre/post registry+seat-table refactor; boot seat map logged and healthz-visible; every kill switch exercised; Sonnet-worker gate-failure ≤1.5x Fable over 2 weeks (model_choice adoption gate); commitments-sweep duplicate rate <20%; shadow-lane kill test passes; scheduler restart-idempotent. Config-not-code is now proven: a model swap is one validated env dial.


### W5 — Product expansion: the four pillars made visible
*Depends on: W2 — Mission-critical eval gates (built and RUN before the flips they protect); W4 — Harness: config-not-code + proposal-chain hardening + workflow substrate*

**Objective:** Voice Scout kickable anytime/anywhere (a); every meeting compounding into brain artifacts with commitments (b); workflows agentic one-by-one, kickable from chat/channel/meeting-proposal with approval (c); everyone aware of everything — pull AND push (d). Plus the eval-gated voice model upgrades.

#### Channel @scout router parity — propose-confirm cards in public channels
MERGED (proposals item 7 + expansion item 4). Run the propose-confirm router on @scout channel mentions that miss the keyword/prefix lane (gate at scout_chat_threads.go:710); any-member confirm (per founder ratification); origin-channel delivery already works; keyword instant-launch preserved/demoted per ratification. Ships only on a passing W2 router golden-set run.
- **Files:** /Users/ajhart/meetingassist/scout_chat_threads.go, /Users/ajhart/meetingassist/scout_chat.go
- **Acceptance:** '@scout draft a brief on X' in a channel → proposal card; another member's confirm launches with channel origin; artifact posts back; nothing launches without a tap; private threads byte-identical; hard negatives stay Q&A.
- **Rollback:** Env flag re-tightens to private-only.
- **Effort:** M

#### Room-voice propose_workflow_run tool
Expansion item 5: Realtime function tool (enum = registry ids) minting a toolTemplate proposal card + everyone-bell instead of launching — confirm-first trust model in shared rooms; private voice keeps direct initiate_goal.
- **Files:** /Users/ajhart/meetingassist/kanban.go, /Users/ajhart/meetingassist/codex_proposals.go
- **Acceptance:** Spoken proposal in a live room creates the card + bell; confirm launches the process; listen-only suppresses; Scout speaks the confirmation.
- **Rollback:** Remove tool from sessionConfig.
- **Effort:** S

#### Generalized suggestion agent — meeting-heard proposals for any registered workflow
Expansion item 6: descriptors declare autoProposeTriggers compiled into the suggestion pass; mints via the toolTemplate seam; keeps confirm-first, listen-only exclusion, 2/pass cap, per-workflow Jaccard dedupe, baseline discipline; capped at standard-lane workflows per founder ratification. Ships behind default-off env until the router golden set + coverage harness pass.
- **Files:** /Users/ajhart/meetingassist/suggestion_agent.go, /Users/ajhart/meetingassist/workflow_registry.go
- **Acceptance:** 'We should pressure-test this pitch' window → grill_pressure_test card (fixture); already-proposed → nothing; failed pass leaves baseline untouched.
- **Rollback:** Existing disable env / default-off gate.
- **Effort:** M

#### Wave-1 authored processes: followup_sweep + meeting_prep
Expansion item 8: the two daily-cadence reference processes — followup_sweep (digest action items + ledger deltas → owner-attributed follow-ups → gate → human checkpoint → filing; draft-forced tickets) and meeting_prep (cross_meeting_briefing + board + positions → brief → gate → deliver). Digest-substrate-first, exercising every registry dimension.
- **Files:** /Users/ajhart/meetingassist/process_followup_sweep.go, /Users/ajhart/meetingassist/process_meeting_prep.go, /Users/ajhart/meetingassist/cross_meeting_briefing.go
- **Acceptance:** Registration validation + golden evals (no invented owners); '@scout run the follow-up sweep' proposes and launches end-to-end in keyed smoke; deliverables file via flywheel to origin.
- **Rollback:** Unregister definitions.
- **Effort:** L

#### Wave-2 authored processes: weekly_memo, fiscal_analysis, backlog_triage, diligence_pack
Expansion item 9: weekly_memo scheduled + park-for-admin-send (external send stays heavy per founder ceremony call); fiscal_analysis wrapping the shipped fiscal.ai grounding tools; backlog_triage (read-only sweep, draft-only moves behind checkpoint — evidence: the 2026-07-06 manual 40-card triage); diligence_pack as the first multi-tool chained process.
- **Files:** /Users/ajhart/meetingassist/process_weekly_memo.go, /Users/ajhart/meetingassist/process_fiscal_analysis.go, /Users/ajhart/meetingassist/process_backlog_triage.go, /Users/ajhart/meetingassist/process_diligence_pack.go
- **Acceptance:** Each registered + golden-eval'd; memo fires Monday 07:00 as standard-lane proposal, send parks at heavy; triage never mutates a non-draft ticket; diligence binder records every stage artifact id.
- **Rollback:** Per-process unregistration.
- **Effort:** L

#### Commitments engine — per-person follow-through records
Expansion item 10: first-class commitment memory kind folded deterministically from digests + decision ledger + followup_sweep ({owner, text, sourceMeetingId, due, status}, edit-as-records doctrine); powers per-person briefing filter, 'what did I commit to' chat/voice lane, brief personal section. Watch the new-memory-kind exclusion-list trap.
- **Files:** /Users/ajhart/meetingassist/commitments.go, /Users/ajhart/meetingassist/meeting_digest.go, /Users/ajhart/meetingassist/memory_query.go
- **Acceptance:** 3 fixture action items → 3 records with resolvable owners (never invented); chat answers verbatim with source links; done-marking updates next brief; no recall double-surfacing.
- **Rollback:** Additive kind; stop the fold.
- **Effort:** M

#### Proactive Morning Brief delivery per user
Expansion item 11: scheduled per-user composition (brief sections + commitments + yesterday's briefing) via existing web push + Resend, per-user time/opt-out, suppressed-when-empty, deterministic $0 default path; first standing-approval consumer of the scheduler.
- **Files:** /Users/ajhart/meetingassist/brief_delivery.go, /Users/ajhart/meetingassist/office_brief.go, /Users/ajhart/meetingassist/web_push.go
- **Acceptance:** Configured-hour push deep-links to the brief with personal commitments; opt-out stops next cycle; no double-send across restart.
- **Rollback:** Default-off per-user + global kill.
- **Effort:** M

#### Channel catch-up digests
Expansion item 12: per-channel rolling digest (company-digest pattern, upsert latest-only, delta-window gated, ambient seat) + catch_me_up tool on chat and both voice surfaces with drill-down; feeds the brief's unread section with substance.
- **Files:** /Users/ajhart/meetingassist/channel_digest.go, /Users/ajhart/meetingassist/scout_chat_threads.go
- **Acceptance:** 'Catch me up on #general' returns a digest with zero invented facts, every claim traceable to a message id; idle channels spend zero tokens.
- **Rollback:** Disable ambient env; tool degrades to read_thread_aloud.
- **Effort:** M

#### Proposal inbox + stale-proposal re-nudge
Proposals item 13: pending-proposals rail on the dashboard (codexProposalsSnapshot, age, one-tap confirm/dismiss) + one daily summary nudge for >24h pending. The actuator for the W0 time-to-confirm metric.
- **Files:** /Users/ajhart/meetingassist/codex_proposals.go, /Users/ajhart/meetingassist/main.go
- **Acceptance:** Pending proposals visible with age from dashboard; rail confirm launches identically; exactly one summary nudge/day; median time-to-confirm drops.
- **Rollback:** Hide rail; nudge env flag.
- **Effort:** M

#### Living per-room dynamic vocabulary (phase 2)
Transcription item 7 phase 2, after the shadow lane exists to A/B the term cap: realtimeTranscriptionPrompt becomes static seed + room roster + top-N entity-ledger nouns + recent card titles, deduped, token-budget-capped, rebuilt at session start + 55-min refresh. New client names transcribe correctly the meeting after they first hit the board — 'understood perfectly' compounding.
- **Files:** /Users/ajhart/meetingassist/domain_terms.go, /Users/ajhart/meetingassist/transcription_lane.go, /Users/ajhart/meetingassist/entity_ledger.go
- **Acceptance:** Seeded room's lane prompt contains ledger + roster names, capped; harness re-run shows domain-term recall ≥ previous; behind MEETING_TRANSCRIPT_DYNAMIC_VOCAB defaulting on only after a shadow-lane comparison window.
- **Rollback:** Env flag off.
- **Effort:** M

#### Attribution confidence + lost-speech accounting in the transcript surface
Transcription item 8: per-sitting attribution summary (attributed/low-confidence/unattributed + failed counts) in ledger + digest health line; low-confidence flags in transcript UI with human speaker-label correction (append-only discipline); 'transcription drop-off detected' alert at >2% failed segments — every .failed is speech the brain never heard.
- **Files:** /Users/ajhart/meetingassist/speaker_attribution.go, /Users/ajhart/meetingassist/meeting_digest.go, /Users/ajhart/meetingassist/index.html
- **Acceptance:** Sitting summary shows confidence bands + failed count; simulated .failed burst fires the alert; a human correction persists and displays.
- **Rollback:** Revert; read-side surfaces only.
- **Effort:** M

#### Shared room → gpt-realtime-2.1 flip
Seats item 8 step 2 (gate already widened in W1): verify 2.1 accepts session.reasoning.effort on the wire, then OPENAI_REALTIME_MODEL=gpt-realtime-2.1 — price-identical, better interruption/noise/alphanumeric. Gated by the W2 voice harness baseline.
- **Acceptance:** session.created echoes the reasoning block on 2.1; harness ≥ baseline on dispatch + false-response, interruptions equal or better; live office smoke sitting.
- **Rollback:** OPENAI_REALTIME_MODEL=gpt-realtime-2 (widened gate still matches).
- **Effort:** S

#### Private-voice dial + gpt-realtime-2.1-mini pilot
Seats item 10: new OPENAI_PRIVATE_REALTIME_MODEL (falls back to realtimeModel()) — required because both surfaces share one dial today (main.go:1893). Pilot mini ($10/$20, ~70% cheaper) on AJ's dashboard only; reasoning block emitted only for verified-accepting models (probe or allowlist).
- **Files:** /Users/ajhart/meetingassist/main.go, /Users/ajhart/meetingassist/kanban.go
- **Acceptance:** Harness private-session scenarios ≥ baseline on mini; 1-week live pilot clean; ledger shows ~70% audio-cost cut on the private class; room session provably unaffected.
- **Rollback:** Unset the new dial.
- **Effort:** M

#### Voice-recall effort-floor carve-out (build, if ratified)
MERGED (harness item 9 + seats item 12) — decision in founder_decisions; this is the one-flag build: seat-scoped LatencyCritical exemption so spoken recall runs effort=low as authored (memory_query.go:1538 vs the silent clamp at anthropic_text.go:83); every other seat keeps the floor. If declined: delete the flag and fix the stale comment.
- **Files:** /Users/ajhart/meetingassist/anthropic_text.go, /Users/ajhart/meetingassist/memory_query.go
- **Acceptance:** Recall QA eval at low: accuracy parity with medium; spoken-answer P50/P95 latency measurably improved; no other seat's wire effort changes (asserted).
- **Rollback:** Flip the flag back.
- **Effort:** S

**Wave gate:** Four-pillar demo passes end-to-end: (a) Scout kicks work from room voice, private voice, chat, and channel; (b) a live meeting produces brain write-up with commitments section, board updates, and a sweep-caught backstop proposal; (c) a heard-in-meeting workflow proposal confirms and launches a registered process delivered to origin; (d) morning brief pushes, catch-me-up answers, 'what did I commit to' answers from records. ALL mission-critical metrics (WER/term recall, proposal recall/precision, kickoff success, voice dispatch, recall QA) at or above their frozen baselines.


## Review gate record

Verdict: **pass** (score 8.8)

Verified against source: every load-bearing ground-truth claim checks out (router DisableThinking omission at the scout_chat.go request builder; usesAdvancedCommandProfile exact-match 'gpt-realtime-2'; whisper fossil live in .env.example:8 + both READMEs; zero cache_read_input_tokens parsing in agent_runner_anthropic.go; laneApprovedBy written by nothing but tests — Case B inert; channel router gate in scout_chat_threads.go). Full diff of all 73 dive plan_items against the synthesis: 100% coverage — transcription 9/9, proposals 13/13, harness 9/9, telemetry 10/10, seats 17/17, expansion 15/15 — with the nine cross-dive duplicates explicitly merged and annotated; nothing silently deferred. Mission-critical protection holds in wave order: the STT dual-model harness (W2, concrete WER≤whisper AND strictly-higher domain-term recall AND p95<3s gate) precedes the W3 transcription flip; proposal-coverage + router golden-set harnesses (W2) precede the Terra/Luna flips (W3) and gate W5 channel parity and generalized suggestions; Terra pins are sequenced before Luna so the recall engines never ride the cheapest tier transiently; the voice harness precedes every voice change. W1 contains only no-eval fixes (the Codex Sol flip embeds its own sidecar canary; execution is not a named mission-critical lane). Harness clause met (registry, ~25-seat table with uniform cross-provider doctrine guards, Sonnet worker, model_choice tier vocabulary with deliverable-forced-frontier + kill switch, pricing-table-generated cost prompt, fallback/breaker, shadow STT lane). All four pillars have named W5 items plus an end-to-end gate demo. Waves have declared acyclic dependencies, gates, and effort sizes; founder decisions are separated with conditional builds explicitly marked (W4 standing-approvals surface 'after ratification', W5 carve-out 'if ratified', scheduler inert-by-default). Deductions: the CHANGE REGISTER rows carry now/target/wave but not per-row acceptance/rollback — those exist for every change but only inside the wave items the register's wave column points to, and one register row lumps six founder decisions together, so the register does not stand alone against clause 1's letter; a few pure-env-flip wave items (W3.1/W3.4/W3.5/W5.12) omit files fields (cosmetic). Substance is complete and correct; this is a traceability/formatting shortfall only, hence pass at 8.8 with one optional polish gap.


## Appendix: dive summaries


### Dive: transcription

The prod gpt-realtime-whisper pin is a fossil, not a decision: it was seeded into deploy/digitalocean/.env.example on 2026-05-20 (commit 10c2de7) as a redundant restatement of the lane's original code default, survived the 2026-07-08 default flip to gpt-4o-transcribe (92626da, done specifically because whisper's lack of vocabulary biasing mangled proper nouns — 'Ball Dogs'), broke prod the same day (2cd2117 gated the prompt), and the commit-message-promised follow-up flip never happened. Whisper buys nothing today — the lane consumes only .completed events (transcription_lane.go:496), never deltas, so whisper's sole advantage (streaming deltas) is structurally unused while costing 2.8x ($0.017/min duration-billed vs ~$0.006/min) and forfeiting the domain-vocab prompt + near-field noise reduction on the company brain's authoritative input. Absurd inversion: the FALLBACK path (Scout voice peer, gpt-4o-transcribe + vocab prompt) currently has better transcription than the AUTHORITATIVE lane. July-2026 research confirms gpt-4o-transcribe is OpenAI's highest-accuracy STT (WER ~4.1 vs Whisper v3 5.3, strongest on technical vocab/accents); gpt-4o-transcribe-diarize (known-speaker references) exists but is /audio/transcriptions-only, and nothing newer exists on the realtime transcription intent. Plan: W0 telemetry (minutes ledger + correction-regex hit counter as a live vocab-error proxy), W2 ground-truth corpus + dual-model eval harness that GATES the swap, W1 env flip + kill the self-reseeding .env.example/README fossils, W3 shadow A/B lane as the standing instrument for all future STT swaps, W4 voice-session prompt-gate hardening, living per-room vocabulary (StationTenn is missing today), attribution-confidence surfacing, and a founder decision on audio retention that would unlock diarize cross-checks and real-meeting ground-truth harvesting. Rollback for the flip is a single env var.

**Open questions:** Does any planned product surface want LIVE word-by-word captions? Today no consumer uses transcript deltas (the 'hearing:' feed rides the voice peer's deltas, kanban.go:2649, office room only). If live captions become a requirement, the right architecture is a DUAL lane — gpt-realtime-whisper (or successor) on a display-only caption lane, gpt-4o-transcribe on the persisted authoritative lane — not a return to whisper for persistence. Worth a one-line founder answer so the eval harness does or doesn't add a captions-latency metric. | OpenAI has diarization only on /v1/audio/transcriptions ('not yet supported in the Realtime API' per docs/search). When realtime diarization ships, should Bonfire adopt it as a cross-check on the energy FIFO or keep attribution fully first-party? The shadow lane (W3) is the designed on-ramp either way. | The .env.example fossil mechanism bit once and will bite again: prod .env values that were seeded as 'restatements of defaults' (OPENAI_REALTIME_REASONING_EFFORT=high matches today's code default; OPENAI_REALTIME_MODEL=gpt-realtime-2 gates behavior via the exact-string check at kanban.go:1496) silently detach from code defaults on every default change. A small W0-adjacent sweep — diff live .env against code defaults and annotate each line as 'deliberate override' vs 'fossil' — would prevent the next one; flagging for the parent plan since it exceeds this dive's lane. | Scripted-read corpus realism: team reads through the real room path capture mic/codec/mixer effects but not true meeting prosody (interruptions, mumbles, distance shifts). If the founder approves the audio-retention decision quickly, real-meeting harvested segments should replace scripted reads as the primary corpus within a few weeks — the harness is built to accept both. | gpt-realtime-whisper supports a delay parameter (minimal→xhigh) trading latency for accuracy per the realtime-transcription docs. If the eval gate somehow FAILED for gpt-4o-transcribe (not expected given published WER and the vocab asymmetry), the fallback experiment is whisper at delay=xhigh + post-hoc canonicalization — but this remains prompt-less, so domain-term recall would still need the correction-regex crutch; noted only for completeness.

### Dive: proposals

Dive 2 traced the full proposal→kickoff chain in source across all three paths. Verdict: the DOWNSTREAM half (confirm→launch→run) is already crash-hardened and idempotent — persist-before-launch with revert (codex_proposals.go:318), proposalMu + confirm-grace + confirm-signal discriminator in the workflow ticker (workflow_ticker.go:198-284), claim-first replay-safe chat accepts (scout_chat_threads.go:832), and call_id dedupe + barge-in truncation classification on room voice (kanban.go:2771-3001). The UPSTREAM half (spoken/typed commitment → proposal card) is where recall silently leaks: (1) the confirmed router bug — scout_chat.go:737 omits DisableThinking so Sonnet-5 adaptive thinking shares the 700-token budget and truncated tool calls silently degrade to inline answers; (2) brain summarization loss — meetingBrainInstructions has no commitments contract, and the board worker drops propose ops missing args silently while the suggestion agent permanently consumes under-called windows; (3) channels only launch on exact keyword/prefix — '@scout do X' otherwise leaves no trace (router is private-thread-only); (4) live proposal cards reach office sockets only; (5) ticker Case B standing approvals are designed but fully inert (nothing writes laneApprovedBy); (6) the planned OPENAI_BRAIN_MODEL=luna flip will drag BOTH proposal-recall engines (board + suggestion workers inherit it) unless Terra pins land first; (7) post-launch, one transport failure = permanent needs_attention with no retry and no metric. Deliverables: a 5-metric measurement contract (proposal recall via eval corpus + sweep diff, time-to-proposal via new transcript-lineage stamps, kickoff success rate, router truncation rate, acceptance rate per source/lane) with the event taxonomy this dive owns, plus 13 plan items — telemetry taxonomy first, model-pin sequencing, the one-line router fix, brain commitments contract, a proposal-coverage eval gate, a per-meeting commitments sweep as the recall backstop, channel router parity, visibility/idempotency/retry hardening, and the standing-approvals founder decision.

**Open questions:** Is the workflow ticker actually enabled in prod? BONFIRE_WORKFLOW_TICKER_INTERVAL / BONFIRE_WORKFLOW_TICKER_DISABLED were not in the verified live .env list — /healthz readiness (main.go:883 readinessWorkflowTickerSnapshot) shows enabled/lastPassAt and should be checked before relying on Case-A recovery. | Channel parity is a product-behavior change: does the founder want the propose-confirm router (proposal cards) running on @scout channel mentions that miss the keyword/prefix lane, or should channels stay launch-only-on-explicit-syntax? Founder vision (c) says 'kickable via @scout in chat/channels' — current code only honors exact syntax. | Auto-run standing approvals (ticker Case B): which workflow classes qualify for a standing approval, who may grant one, and does it expire? Nothing writes laneApprovedBy today, so this is a founder policy decision before any build. | Room-voice launches stamp originMeetingId from officeRoomID only (kanban.go:3036-3039, audit open question): is binding named-room voice launches to the office meeting id acceptable, or should per-room Scout voice (when it exists) carry its room's meeting id? | Commitments-sweep duplicate tolerance: the sweep will re-propose some items the board worker already carded-but-not-proposed. Is a proposal card that duplicates an existing board CARD (not proposal) acceptable noise, or should the sweep also dedupe against card titles (matchBoardCard exists and can be reused)? | Should the suggestion agent's advance-baseline-on-empty-pass behavior be revisited directly (one retry window like shouldRetryBoardWindow), or is the meeting-close sweep a sufficient backstop? The sweep is the lower-risk answer; changing the baseline idiom touches the ambient runner's cursor discipline. | Sonnet 5 intro pricing expires 2026-08-31 (+50% on the router seat): after the DisableThinking fix lands and truncation telemetry reads ~0, does the founder want a router-model line item revisited (the never-Haiku doctrine's one recurring cost), or is the doctrine absolute?

### Dive: harness

Dive 3 build plan: replace the closed runner enum + four switches with an init-time registry; introduce a unified seat-dial table (~25 seats, one place: provider+model+effort+budget+never-list+fallback per seat) with doctrine guards applied uniformly to BOTH providers and boot-time seat-map validation; build the sanctioned Sonnet worker runner and let Fable emit a validated per-subtask model_choice (frontier|standard) that degrades silently on unknown values exactly like assignedRunner; give Fable the live pricing table + seat map in its (cached) system prompt; generalize the refusal-retry pattern into per-seat same-call fallback with provenance and a per-provider circuit breaker; and land the small fix pack (kanban.go:1496 gate widen, router DisableThinking, base-URL overrides that keep venice_chat.go shelf-ready). Everything is config-not-code after this wave: a new provider = one registry entry + one client file; a model swap = one seat-table env dial validated at boot. Sequenced by dependency: pricing table (W0, shared with the usage ledger) → fix pack → registry → seat table → Sonnet worker → model_choice + cost prompt → fallback/breaker; Codex 5.6 eval rides W2. All behavior-identical refactors are pinned by golden tests; every routed feature has an env kill switch.

**Open questions:** Does gpt-realtime-2.1-mini accept session.reasoning.effort? (Official model page silent per audit repair-2.) The widened kanban.go gate excludes '-mini' until confirmed — confirm with a live session before any private-voice mini pilot extends the gate. | Does the sidecar's installed Codex CLI version accept gpt-5.6-sol/terra model ids? Determines whether the W2 execution-seat eval is a pure env flip or needs a sidecar image bump first. | Which dive owns models_pricing.go + the usage ledger seams? Dive 3's seat map, boot validation, cost-awareness prompt, and fallback provenance all consume them — the pricing table must be built exactly once, and the gate-failure-by-runner metric for model_choice needs the ledger's seat/runner tagging to include assignedRunner. | Founder ratification: (a) the voice-recall effort-floor carve-out (wired disabled, one flag); (b) whether the frontier-seat never-list should be a strict allowlist (fable/opus families only on orchestrator+deliverable, sonnet also allowed on review) or the softer refuse-known-cheap-tiers rule — the plan defaults to strict allowlist on orchestrator/deliverable, Sonnet/Opus allowlist on review/chat/router/worker per the standing doctrine. | model_choice vocabulary: plan ships frontier|standard (Sonnet worker) only, per 'worker seats are Sonnet/Opus tier ONLY'. Should a third 'economy' tier (an OpenAI-runner draft lane for /goal subtasks) ever exist, it needs a fresh doctrine ruling — deliberately excluded here. | Orchestrator transport-fallback scope: extending the refusal branch to 429/529 means a mid-run turn can be answered by Opus within an otherwise-Fable run (provenance-stamped). Confirm this mixed-provenance run is acceptable vs failing the run honestly — the plan assumes acceptable because the refusal path already does exactly this. | Circuit-breaker granularity: per-provider (planned, simple) vs per-model — a Fable-pool 529 storm would also cool down Sonnet chat seats under per-provider. Acceptable for v1?

### Dive: telemetry

Dive 4 design complete: a single W0 wave (usage_ledger.go + dated pricing table + wire-seam instrumentation on all 4 provider surfaces + eval-event funnel + daily rollup artifact + alert engine) that gives Bonfire real books before any model flip. Every instrumentation point was verified in source: Anthropic usage is parsed but dropped (and misses cache_read/cache_creation fields entirely — up to 10x cost error on the cache-heavy Fable lane), OpenAI Responses parses no usage, the Realtime response.done event struct decodes no usage field, and the private dashboard voice is browser-owned so it is structurally unmeterable server-side without a beacon. Quality-eval hooks mostly already exist as in-memory structures (board worker's meetingBoardRunResult.ErrorCount, notifications.go settleProposalNotification for router outcomes, goalGate for gate-by-runner) — the harness aggregates them into the same JSONL ledger + alert thresholds surfaced via the existing notifications system and a living company-brain spend artifact. Ten plan items, one shippable wave, additive-only with a single kill switch.

**Open questions:** Does gpt-realtime-2's response.done event reliably include the usage object over the WebRTC datachannel (vs the WS transport it is documented on)? Verify on one live room session before trusting room-voice metering; fallback is wall-clock session estimation. | Private dashboard voice: ship the browser usage beacon inside W0 (small JS + reuse of the existing realtime-tool POST path) or accept an explicit 'unmetered' lane flag until W4? Founder scope call. | Does the Codex CLI sidecar emit parseable token usage in its job output JSON? If yes, the callback handler can ledger it; if no, execution-runner jobs carry an unmetered flag — verify against the sidecar container, not this repo. | Visibility policy: is the spend rollup artifact + /api/usage/rollup endpoint visible to all roster users (fits the 'everyone aware' pillar) or admin-only? Same question for ops_alert notification audience. | Default alert calibration: proposed SPEND_ALERT_DAILY_USD=75 (audit models heavy days at ~$50 typ) and fallback-rate>5%/24h per the audit — confirm the daily dollar cap with the founder. | JSONL retention (proposed 90 days, env-tunable) and whether the backup ring should include the usage dir (it will by default since backup.go tars the whole data dir — likely fine, files are small). | Ledger-vs-console verification method for the baseline gate: manual console read-off, or also wire the OpenAI organization usage API as an automated cross-check (optional, not W0-blocking)?

### Dive: seats

Dive 5 — seat-by-seat model assignment, best-in-class OpenAI+Anthropic end to end. Verdict shape: the Anthropic executive stack stays exactly as-is (Fable 5 high on orchestrator+deliverables, Opus 4.8 on review/gate and refusal fallback, Sonnet 5 on every human-visible worker seat) with two surgical fixes (router DisableThinking, voice-recall latency carve-out) and one eval-gated pilot (orchestrator effort medium for non-deliverable turns). The OpenAI side maps cleanly onto OpenAI's own 5.6 tiering doctrine: Sol=execution (codex sidecar gpt-5.5→gpt-5.6-sol, same price, strict upgrade), Terra=tool-op/proposal seats (board worker + suggestion agent pins), Luna=extraction fleet (OPENAI_BRAIN_MODEL→gpt-5.6-luna, ~80% cut on the 10-lane ambient fleet, eval-gated because the brain lane is the company-memory substrate and zero intelligence drop-off is tolerated). Voice: shared room upgrades gpt-realtime-2→2.1 (same price, better interruption/noise — but the kanban.go:1496 exact-string gate must be widened FIRST or reasoning.effort silently drops), live effort high→low only after a voice-harness eval proves proposal→kickoff dispatch reliability holds, and gpt-realtime-2.1-mini pilots on the private dashboard voice only (needs a new env dial — both surfaces share OPENAI_REALTIME_MODEL today, verified main.go:1893). Transcription stays deferred to Dive 1 (whisper pin confirmed live; fidelity bake-off owns that call). Embeddings (no successor exists — verified) and gpt-image-2 stay. Everything is sequenced W0 telemetry → harness/evals → flips, every flip has a one-env-var rollback, and the two genuine founder decisions (eat the Sep-1 Sonnet +50% [recommended, est +$13-53/mo]; keep Opus on the gate while the ambient fleet is non-Anthropic [recommended, revisit only on shadow-eval parity]) are packaged with real numbers.

**Open questions:** Does gpt-realtime-2.1 accept session.reasoning.effort byte-identically to 2 on the /v1/realtime/calls session config (docs say configurable reasoning is supported, but verify on a live session before the flip), and does 2.1-mini accept it at all (its model page is silent — decide probe-vs-allowlist in the widened gate)? | Dive 1's transcription verdict: does gpt-realtime-whisper's streaming-delta latency serve anything user-visible that gpt-4o-transcribe cannot, and does the domain-vocab prompt on gpt-4o-transcribe actually win the WER/domain-term bake-off on real Bonfire meeting audio? Every downstream lane in this dive inherits that fidelity call. | Is suggestion-proposal approval/dismissal currently recorded anywhere queryable (needed as the Terra-pin acceptance baseline), or does W0 need to add a proposal-outcome counter alongside the ledger? | Does the sidecar container's Codex CLI build resolve gpt-5.6-sol (GA was Jul 9; the pre-flight smoke decides whether the flip needs a sidecar image bump first)? | Orchestrator-effort pilot scope: is AJ comfortable with /goal decompose/panel/report/verify riding the medium pilot too (they share orchestratorEffort()), or should the pilot add a separate BONFIRE_GOAL_ENGINE_EFFORT dial so only agent-thread non-deliverable turns drop to medium? | For the review-gate shadow eval, do historical gate verdicts persisted in artifact metadata suffice to pre-seed the paired corpus (faster), or must the eval run purely forward over 2-3 weeks of new goal runs?

### Dive: expansion

Dive 6 (pillars c+d): The workflow registry the founder wants already ~60% exists — tool_registry.go's 13 gated goal-preset tools plus process_definitions.go's versioned ProcessDefinition runtime (stage roles, registration validation, checkpoint park/resume, additive registration seam) — but only packaging_studio uses it, and the trigger surfaces are badly asymmetric: registered workflows are kickable from palette/goal-door/private chat/private voice, yet NOT from public channels (router gated to private at scout_chat_threads.go:710), NOT from room voice, NOT from meeting-heard proposals (proposeCodexTask only accepts 5 single-thread modes; launchApprovedProposal never reaches the goal engine), and nothing schedules recurring runs. The plan: (W0) workflow-run provenance ledger; (W3) a WorkflowDescriptor overlay unifying both registries with per-workflow RunnerPlan seats, toolTemplate through the proposal seam (the single highest-leverage change — it unlocks channel, room-voice, heard-in-meeting, and scheduled kickoff of any workflow), and a registry-declared scheduler riding the inert-until-069 standing-approval lane; (W4) channel @scout router parity, room-voice propose_workflow_run, a generalized suggestion agent, six new authored processes sequenced followup_sweep + meeting_prep first (digest-substrate, daily recurrence) then weekly_memo/fiscal_analysis/backlog_triage/diligence_pack, plus three genuinely-new-product everyone-aware surfaces — commitments engine, proactive per-user morning brief delivery, and channel catch-up digests; (W2) two mission-critical eval gates (proposal→kickoff routing golden set, ask-anything recall quality). Approval governance needs no redesign (auto/standard/heavy lanes complete), brain filing and origin-channel delivery already work, and four founder ratifications (069, workflow order, seat plan, calendar build) gate sequencing only — everything ships propose-mode-first so nothing blocks on them.

**Open questions:** Channel proposal confirm semantics: when @scout proposes a workflow in a public channel, may ANY signed-in member confirm (consistent with standard lane + room proposal cards), or only the mentioner? Recommend any-member (the lane already says so) but it changes who can spend orchestrator budget from a channel. | Card 069 standing approvals are still AWAITING RATIFICATION (per project memory) — the scheduled-workflow auto-run lane and the workflow ticker's shape B are inert until laneApprovedBy semantics are ratified. Which scheduled workflows get standing approval vs always-propose? (Proposed default: morning brief + channel digests auto (deterministic composition, no external writes); weekly memo and anything model-written always standard-lane; anything external_write always heavy.) | Meeting-prep trigger source: build the reserved Google Calendar OAuth integration (calendar.go seam exists, real build), or trigger prep off scheduled rooms/board key dates only for now? The workflow itself is buildable either way; the trigger fidelity differs. | Weekly memo distribution: filing to Files/packages is workspace_write, but actually EMAILING investors is external_write → heavy lane (admin or 2-member). Is a permanently-parked heavy gate per weekly send acceptable ceremony, or should 'compile and park for admin send' be the contract? | Should the generalized suggestion agent be allowed to propose HEAVY-lane workflows it hears in meetings (e.g. 'we should push the release'), or is its proposal surface capped at standard-lane workflows? Recommend capping at read_only/workspace_write intake — heard speech proposing external writes feels like the wrong trust posture even with the confirm gate. | Per-workflow RunnerPlan vs audit's env-dial staging: the routing audit (Dive prerequisite) sequences env flips first (OPENAI_BRAIN_MODEL=gpt-5.6-luna etc., pending verification those model ids are callable). The registry RunnerPlan should consume the same resolved seats, not fork them — confirm the routing dive's seat names/env contract before W3 lands so the two dives converge on one seat vocabulary.