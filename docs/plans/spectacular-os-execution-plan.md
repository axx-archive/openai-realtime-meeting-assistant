# Spectacular OS — Execution Plan

Date created: 2026-07-03
Primary plan: `docs/plans/spectacular-os-design.md` (accepted by dual-critic loop at 9.5/10)
Execution log: `docs/plans/spectacular-os-execution-log.md`
Branch: `main` (house convention: single branch, rsync-based deploy decouples push from deploy; each wave commits with a descriptive message)

---

## How This Works

1. Execute exactly one wave per fresh context window.
2. Each wave has a ready-to-paste prompt at the bottom of its section in the execution log.
3. Do not begin the next wave until the log marks the current wave as `completed`.
4. Every wave updates the execution log with: checklist, files changed, validation, risks, next-wave prompt.

---

## Reference Documents

| Doc | Path | Purpose |
|---|---|---|
| Unified design (source of truth) | `docs/plans/spectacular-os-design.md` | Architecture, contracts, conflict resolutions, acceptance |
| Context brief | `docs/plans/spectacular-os-context.md` | Mission, current-system map with file:line refs |
| Technical spec | `docs/plans/spectacular-os-technical.md` | AgentRunner interface, /goal state machine, schemas, parity plumbing, quarantine model, test strategy |
| Domain spec | `docs/plans/spectacular-os-domain.md` | 12 tools, master wrapper, exemplar prompts, rubrics, slop criteria, grill contract |
| RTC spec | `docs/plans/spectacular-os-rtc.md` | Multi-endpoint design, suppression strategy, looks pipeline, device matrix |
| UX spec | `docs/plans/spectacular-os-ux.md` | Palette, voice island, rename table, delight inventory, monolith checklist |
| Product spec | `docs/plans/spectacular-os-product.md` | Journeys, seams, triage, acceptance moments |
| House rules | `AGENTS.md` | go test mandate, deploy shape |

---

## Critical Rules (Apply to ALL Waves)

1. **One pipeline, three doors.** Voice, `/goal` text, and menu all emit the same goal-spec schema. Never fork into parallel backends.
2. **Control/execution plane boundary.** Realtime/orchestrator never runs shell/SSH/browser/long research in-process; execution-plane workers never listen to room audio or mutate the live board directly.
3. **Safety gates are backend-agnostic and live in the orchestrator/queue layer.** `read_only/workspace_write/external_write` ladder + `approval_required` + admin gate (`isArtifactApprovalAdmin`, `main.go:1187`). No backend self-approves. `external_write` is never settable by a voice/tool argument — it is earned at the gate.
4. **Disclosure is server-stamped unconditionally.** Every on-behalf post carries `postedOnBehalfOf` + a rendered "via Scout" chip regardless of model args.
5. **Additive metadata only — no schema migrations.** Absent field = today's behavior. Existing artifacts/threads/packages/tests keep working. Env aliases preserve deploy configs.
6. **Keyless-local keeps working** (`go run .` on :3000; agentic features degrade to the stub/503 cleanly). The native Apple `/native/config` contract is untouched (new fields additive/optional).
7. **`go test ./...` green is the gate for every wave.** The VPS has no Go — tests run locally before any deploy. Frontend assertions live in Go tests (`frontend_latency_test.go` pattern).
8. **index.html discipline (34.9k lines, no build step):** namespaced component classes (`.palette__*`, `.goalcard__*`, `.grillstage__*`), token-only values, no `transition: all`, every animation in the `prefers-reduced-motion` block (state kept, motion dropped), `tabular-nums` for dynamic numbers, ≥44px hit areas, a phone spec ships with every new surface, z-index slots documented. Rename **labels only, never the `office` data-tool key**.
9. **Color law:** green (`--signal`) = a human is live/speaking; ember (`--agent`) = a machine is working. They never co-occur; no other new hue.
10. **Never silently hard-delete.** Quarantine deny-list (decisions, archives, packages/UI-state, published, package-attached, human-pinned, <7 days) is enforced in the candidate builder (code), not the prompt. Expiry leaves an audit stub.
11. **Push-channel semantics:** light consumers render from the event payload; rich consumers fetch-by-ref on receipt. Polling fallback stays until the two-session acceptance test passes.
12. **Deploy ops:** rsync committed tree to `root@146.190.171.224:/opt/meetingassist` (preserve `.env`), `docker compose up -d --build` in `deploy/digitalocean/`, verify `https://thebonfire.xyz` + container health. Ops only at checkpoints, never mid-group.

---

## Wave Map

| Wave | Phase | Scope summary | Status |
|---|---|---|---|
| 1 | Foundations | AgentRunner interface + anthropic_fable/stub providers + goal-spec schema + env selection | `pending` |
| 2 | Foundations | /goal engine: state machine, goalPlan, review/gate/verify model calls, boot reconciler | `pending` |
| 3 | Foundations | Unified push channel: broadcastOSEvent + producers + client consumer + two-session test + RSS baseline | `pending` |
| 4 | Foundations | Multi-endpoint WebRTC sessions + handoff UX + smoke harness (+ OPS-1) | `pending` |
| 5 | Shell | BonfireOS rename + `--agent` ember token + shell polish (9× transition:all, pressable, focus-on-glass) | `pending` |
| 6 | Doors | Scout Realtime-2 parity: allowlist growth, 5 new tools, disclosure, `/goal` text parser | `pending` |
| 7 | Intelligence | Artifact titles/reader + body recall + query expansion + slop lifecycle/classifier/expiry | `pending` |
| 8 | Intelligence | Morning Brief + Portfolio Health + quarantine tray + approval round-trip loop (+ OPS-2) | `pending` |
| 9 | AV | Noise suppression overhaul: per-browser strategy, gate demotion, default-on, honest chip, v8 settings | `pending` |
| 10 | Suite | Tool registry + 12 prompt bodies + rubrics + golden/checklist evals | `pending` |
| 11 | Suite | Quick-select palette (desktop+mobile) + running-state stage-rail card + return-to-origin card | `pending` |
| 12 | Suite | Private grill: tools + client session.update swap + 3-act ritual + readiness dial | `pending` |
| 13 | AV | Video looks: pipeline, 4 looks, thermal governor, settings picker, smoke script | `pending` |
| 14 | Polish | Delight pass + wake-word + device recovery + Deal Room (cuttable) + acceptance demo (+ OPS-3 final) | `pending` |

Dependencies: 2←1; 3 independent (lands early, highest risk); 4 independent; 5 independent; 6←1,2; 7 independent of doors; 8←3,7; 9 independent; 10←2; 11←3,5,6,10; 12←6; 13←9(settings section); 14←all.

---

## Wave Details

### Wave 1: AgentRunner foundation
**Source:** design §2, technical §1. **Scope:** ~5 new/modified Go files, ~800 LOC + tests.
**Deliverables:**
- [ ] `agent_runner_iface.go`: `AgentJob`, `AgentCapabilities`, `AgentProgress`, `AgentRunner` (async channel contract per technical §1.1)
- [ ] Goal-spec fields threaded onto thread launch (objective, toolTemplate, contextRefs, originSurface, requestedBy, authority, visibility, packageId)
- [ ] `anthropic_fable` runner: raw Messages API tool-loop calling `app.applyToolCallArgs` (`kanban.go:2543`); `currentAnthropicAPIKey()` mirroring the OpenAI accessor; maxTurns cap; status-only error surfacing
- [ ] `codex_sidecar`/`codex_local`/`openai_text` wrapped as `AgentRunner` implementations at the `produceAgentThreadArtifactWithWorker` seam (`agent_thread_runner.go:427`); sidecar returns queued-progress channel, callback feeds terminal
- [ ] Keyless **stub runner** + env selection `selectAgentRunner` (`BONFIRE_AGENT_RUNNER`, `BONFIRE_EXECUTION_RUNNER`, `BONFIRE_ORCHESTRATOR_MODEL`) + back-compat aliases for `BONFIRE_AGENT_THREAD_WORKER`/`BONFIRE_CODEX_AGENT_THREADS`
- [ ] Tests: selection matrix incl. aliases + keyless fallback; fake-runner progress→artifact-metadata mapping; anthropic loop against a mock endpoint (tool_use→dispatch→tool_result round trip)

### Wave 2: /goal engine
**Source:** design §1, technical §2. **Scope:** ~2 new Go files + seams, ~900 LOC + tests.
**Deliverables:**
- [ ] Goal state enum (superset of existing stage strings) + `goalPlan` JSON schema (≤6 subtasks, deps, per-subtask review, gate/report/verification blocks) persisted in artifact metadata
- [ ] Transition engine: decompose (validated JSON, 2 attempts), capability-match assignment, topological coordinate, concurrency cap 2, subtasks as `launchAgentThreadWithOrigin` children
- [ ] Review-against-goal + gate + verify as model calls; bounded retries (2/subtask); external-write detection forces `approval_required` and stops (reuses `codexApprovalRequiredResult` path)
- [ ] save_what_worked (`mode=goal` artifact + idempotent `attachToPackage`) + four-line report (Changed/Headline/Gap/Next) + gate outcome & ASSUMED-count fields for the return card
- [ ] Boot reconciler: resume every non-terminal `mode=goal` artifact from its plan; idempotent re-dispatch
- [ ] Tests: full state-machine table; plan validation (malformed/oversized); resumability; **safety regression: no external_write launch without an approval record**

### Wave 3: Unified push channel
**Source:** design §Integration. **Scope:** Go seam + index.html consumer, ~600 LOC + tests.
**Deliverables:**
- [ ] `broadcastOSEvent(event)` seam (`{kind, ref, title, originSurface, actor, at}`) wired into artifact terminal path, proposal store, notification store, channel post path, package linkage
- [ ] Office websocket carries the events for every authenticated session (in room or not)
- [ ] Client consumer: light consumers render from payload (bell, brief counters, return-card arrival); rich consumers fetch-by-ref (board, package rail); idempotent by `(kind, ref, at)`
- [ ] Polling fallback retained + documented as removable only after acceptance
- [ ] **Two-session acceptance test** (change in session A visible in session B ≤2s, no room join) + RSS before/after measurement recorded in the log
- [ ] Tests: event fan-out per producer; office-socket auth guard; frontend-marker test for the consumer

### Wave 4: Multi-endpoint WebRTC (+ OPS-1)
**Source:** design §7, rtc §1.3/§5.4/§6. **Scope:** kanban.go/main.go session model + index.html, ~700 LOC + tests + smoke.
**Deliverables:**
- [ ] Client mints stable `bonfire.endpoint.id.v1` (localStorage) sent in the participant hello (additive/optional)
- [ ] Server sessions keyed `map[name]map[endpointId]sessionMeta`; same-endpoint replacement keeps refresh semantics; capacity counts distinct names; endpoints/account capped at 2
- [ ] Roster: one identity + "· 2 devices"; same-account second endpoint defaults to muted playback of the first endpoint's tracks + "this is my other device" chip; calm "joined from another device" handoff chip (no alarming eviction)
- [ ] `endpoint_session_test.go` (both admitted; same-endpoint replaced; one seat; all existing `participants_test.go` assertions green) + two-endpoint renegotiation case
- [ ] `scripts/multi-endpoint-smoke.mjs` (two Playwright contexts, same account, both in roster >30s, no `session_replaced`)
- [ ] **OPS-1:** commit, rsync, compose rebuild, `.env` gains `ANTHROPIC_API_KEY`/`BONFIRE_AGENT_RUNNER`/`BONFIRE_EXECUTION_RUNNER`, verify thebonfire.xyz + RSS on the real box

### Wave 5: BonfireOS rename + design-system foundation
**Source:** design §8, ux §3/§7. **Scope:** index.html only, ~300 LOC edits.
**Deliverables:**
- [ ] Rename all 8 label locations (ux §3.1 table incl. phone `'bonfire'` special-case) — labels only, `office` key untouched
- [ ] `--agent`/`--agent-soft`/`--glow-agent` tokens in both theme blocks; ember hairline active-tool mark
- [ ] Remove all 9 `transition: all` (named properties); `.pressable` utility applied to primary buttons; label-flyout hover delay vs instant focus; focus-visible-on-glass fallback; bell warmth one-breath pulse
- [ ] Reduced-motion block updated; monolith review checklist (ux §7.4) added as a comment banner near the token table
- [ ] Frontend-marker Go test pins the rename strings + `--agent` token

### Wave 6: Scout Realtime-2 parity + /goal doors
**Source:** design §5, technical §3. **Scope:** kanban.go/main.go + index.html composer, ~800 LOC + tests.
**Deliverables:**
- [ ] Allowlist growth (13 real → ~24): add update/publish artifact + board mutations; instruction boundary rewritten ("on the user's behalf; not the room's shared voice"); room-only set unchanged
- [ ] New tools + dispatch: `read_thread_aloud`, `start_chat_as_user` (server-stamped `postedOnBehalfOf` + "via Scout" chip render), `initiate_goal` (cannot request external_write), `control_app.also_open`
- [ ] `/goal <objective>` composer parser (first-token match, emits the same goal spec, `originSurface=this thread`)
- [ ] Voice island `acting` + `hand-raised` states + announce→act→confirm narration line + durable toast receipts + "what Scout did" session ledger
- [ ] Tests: allowlist include/exclude; dispatch routing; **disclosure stamped regardless of args**; parser marker test

### Wave 7: Memory intelligence (recall + slop)
**Source:** design §6, technical §4–5, domain §4. **Scope:** memory.go/memory_query.go/new worker + reader UI, ~800 LOC + tests.
**Deliverables:**
- [ ] Artifact titles everywhere + read-only reader for all users (kills raw-prompt listings)
- [ ] Artifact-body recall indexing ranked above card metadata for reconciliation questions; query expansion (synonyms OR'd, rides `canonicalizeDomainTerms`)
- [ ] `relevance` lifecycle (`active/archived/quarantined/expired`) + search guard (skip quarantined/expired; down-rank archived)
- [ ] Slop classifier ambient worker (6h default, minBatch 8, cursor `slop_pass`), Domain criteria as system prompt, thresholds 0.85/0.70, **deny-list in the candidate builder** (rule 10), candidates = transcript segments + unpublished/unattached artifacts only
- [ ] 30-day expiry job + audit stubs; `/assistant/quarantine` GET/restore endpoints (restore all users; delete-now admin-only)
- [ ] Tests: search exclusion/restore; deny-list; expiry + stub; idempotence; expansion hit; reader access

### Wave 8: Morning Brief + Portfolio Health + approval loop (+ OPS-2)
**Source:** design §9, domain §6.2. **Scope:** Go snapshot composition + index.html surfaces, ~700 LOC.
**Deliverables:**
- [ ] Morning Brief card on BonfireOS home: pending approvals, overnight results, board deltas, unread channels, quarantine tray (10-second decision kit, batch confirm, permissions per rule)
- [ ] Portfolio Health surface: stage, readiness dial + trend, freshness, gaps per package; "Scout, how's the portfolio?" summary tool
- [ ] Approval round-trip loop: requester waiting state + subscription; admin one-tap approve/reject from bell + Brief; approved asset returns to requester's origin surface ("approved · sent") or rejection with reason — rides push-channel `proposal` events
- [ ] Mobile specs for all three (sheet/accordion patterns per ux §5)
- [ ] Tests: brief composition; portfolio payload; **approval round-trip** (non-admin request → admin approve → origin-surface return + notification)
- [ ] **OPS-2:** deploy + verify; `.env` gains `SLOP_CLASSIFIER_INTERVAL`

### Wave 9: Noise suppression, first-class
**Source:** design §7, rtc §2/§4. **Scope:** index.html audio graph + `public/voice-focus/rnnoise-processor.js`, ~500 LOC.
**Deliverables:**
- [ ] Per-browser strategy: Safari/iOS platform voiceIsolation (no WASM stacking); Chrome/Firefox RNNoise-as-denoiser with browser NS disabled when worklet active; Android platform-NS default + labeled opt-in; AEC browser-native everywhere
- [ ] Worklet gate demotion: `targetGate = 0.6 + 0.4·smoothedVAD` clamped [0.5,1.0]; remove `noiseBias` subtraction; heuristic kept only as soft −12dB comfort ducker
- [ ] Default-on (desktop) + relabeled modes (voice focus (intelligent) / standard cleanup / raw mic)
- [ ] Honest status chip from live diagnostics (active/fallback/loading/unavailable/off + mechanism) + suppression-dB meter; v8 unified AV settings record (tolerant v7 migration) + "✓ saved for this device"
- [ ] `voice-focus-benchmark.mjs` extended: suppression-dB + speech-onset preservation assertions
- [ ] Frontend-marker tests: chip strings + v8 migration branch

### Wave 10: Tool suite + evals
**Source:** design §3, domain §1–2. **Scope:** Go registry + prompt content + eval harness, ~900 LOC.
**Deliverables:**
- [ ] Tool registry (12 entries: id, group Ideate/Package/Market/Portfolio, promise, inputMode, stage map, authority, rubric ref) consumed by palette, parser, `initiate_goal`, engine
- [ ] Master /goal wrapper implemented as the orchestrator prompt scaffold (immutable goal, memory grounding slots, evidence discipline, 10-step instructions, 4-line report)
- [ ] 12 tool bodies (3 exemplars verbatim from domain §2.3; 9 following the identical shape) + gate rubrics with kill conditions; existing contracts (`research_brief_v2`, `grill_scorecard_v2`, `READINESS:`) preserved exactly
- [ ] Golden-output evals (Deep Research, One-Pager, Grill: fixed inputs, checklists, seeded-flaw kill checks) + checklist evals for the other 9 — runnable as a Go test tag or script; ship gate
- [ ] Flywheel wiring: completion fires attach-offer/decision-log/context-index; readiness delta tracked

### Wave 11: Quick-select palette + /goal cards
**Source:** design §8, ux §1. **Scope:** index.html, ~900 LOC.
**Deliverables:**
- [ ] Palette: `+ Tools` button + `/` first-char trigger → `openToolPalette`; glass sheet grid grouped by lifecycle; fuzzy search; keyboard model (roving focus, Enter/⌘Enter/Esc, focus trap); poetic empty state → Scout handoff
- [ ] Input collection: inline form (morphing sheet) + conversational prefill via existing `promptScoutForWork`; both call `runGoalPipeline`
- [ ] Running-state card: 10-node stage rail (done/active-ember-breathing/pending), advance animation, show-working disclosure, tabular numbers, pause/cancel
- [ ] Terminal states: complete (ember-settle + single spark burst + **gate outcome + ASSUMED count** + open-artifact), gate/approval (amber stop, admin buttons vs "waiting on AJ"), error (freeze + retry-from-here)
- [ ] Return-to-origin card rendering in channels/threads/room (one component, three contexts)
- [ ] Mobile: bottom sheet (no keyboard auto-open, grab handle, compressed 5-node rail)
- [ ] Frontend-marker tests + reduced-motion entries

### Wave 12: Private grill
**Source:** design §5, technical §3.3, domain §5, ux §2.6. **Scope:** Go tools + index.html client swap + ritual UI, ~700 LOC.
**Deliverables:**
- [ ] `start_private_grill`/`end_private_grill` tools (private-allowlisted); dispatch returns sanitized persona instruction block
- [ ] Client-driven `session.update` swap on the browser-owned data channel + revert on end + 15-min safety timer; **restart rule: session survives server restart (browser-owned); only report-filing retries**
- [ ] The 3-act ritual: pitch capture (timer ring, held questions), grill phase (persona from package artifacts + contradicting decisions cited, pressure meter, escalation budget), scorecard reveal (staggered rows, count-up, serif verdict)
- [ ] `grill_scorecard_v2` filing + package attach + readiness dial delta spoken and shown (trend on the binder)
- [ ] Tests: allowlist; dispatch returns instructions (no server session mutation); frontend marker for the client swap handler; READINESS parse intact

### Wave 13: Video looks
**Source:** design §7, rtc §3. **Scope:** index.html + `public/video-looks/`, ~600 LOC.
**Deliverables:**
- [ ] Pipeline: `MediaStreamTrackProcessor → OffscreenCanvas WebGL shader → MediaStreamTrackGenerator` at the `createLocalMediaStream` seam; canvas-capture fallback (Safari desktop); CSS-preview-only honest fallback ("preview only")
- [ ] One parameterized shader; 4 looks (Bonfire warm/Studio/Mono/Low-light) + none (full teardown); looks **off by default** everywhere; iOS off + labeled opt-in
- [ ] Thermal governor (intensity → look → worklet shed, chip-honest, auto-restore) shared with audio
- [ ] Settings picker with live self-preview + sliders-as-presets + per-device v8 persistence
- [ ] `scripts/video-look-smoke.mjs` (loopback PC asserts far-end frame signatures)
- [ ] Never-black-tile guard: any pipeline exception tears down to raw track

### Wave 14: Polish + capstones + acceptance (+ OPS-3 final)
**Source:** design §9, ux §4, rtc §5, product §6. **Scope:** index.html + small Go, ~600 LOC + verification.
**Deliverables:**
- [ ] Delight pass: remaining inventory items (stage-advance sweep, quarantine slide, theme icon cross-fade, copy confirm, empty-state serif) — all reduced-motion gated; wake-word presence (transcript watch → shell breathe + private-voice arm), **gated on voice stability**
- [ ] Device recovery: `track.onended` auto-switch, `devicechange` recovery + offer chip, mobile visibility-restore (AudioContext resume + track rebuild), reconnect choreography polish
- [ ] Catch-me-up + deferred "after meeting" reminders verified wired (roadmap NOW items riding this wave's plumbing)
- [ ] Deal Room (**cuttable**): read-only shareable binder export behind external_write approval
- [ ] Acceptance: whole-wave demo + quiet-Tuesday journey + three-doors/gate/approval-round-trip/honesty-chip/rename/mobile-parity tests + device matrix (rtc §6.3) all green
- [ ] **OPS-3:** final deploy, live verification on thebonfire.xyz (desktop + real phone), memory-file update
