# Spectacular OS — Agent Team Context Brief

**Date:** 2026-07-03 · **Lead:** Claude (strategic-design process) · **Requested by:** AJ Hart

## Mission

Push Bonfire OS from "very good" to **spectacular end-to-end** — an OS that feels like magic and works flawlessly. Bonfire is the operating system for a venture-packaging group: a six-person team that ideates, packages IP, takes packages to market to attract talent and capital, and then supports a portfolio of ventures through continued creative ideation and growth-capital rounds. The thesis is **"business as intelligence"**: the OS tracks everything everyone does, intelligently analyzes and summarizes contributed artifacts, and discards the slop (material irrelevant to the company) so the knowledge base never snowballs into years of useless information.

This wave has four mandated pillars plus an open invitation for ideas that take the concept to another level:

1. **First-class WebRTC.** Flawless video and audio, stable connections, zero weirdness when users join simultaneously on desktop and mobile, first-class noise suppression, lightweight video enhancement ("looks"/touch-up, Zoom-like tone), and per-device persisted AV settings with the settings surface honestly reporting suppression state.
2. **Scout Realtime-2 as a "she can do it all" agent.** The private voice/chat Scout reachable from the office/home page must be able to: open tabs, send other users notifications, post in threads, start chats and post on your behalf (and read responses aloud), recall meeting information, initiate any packaging-side request (research, design, etc.), and run grill mode herself (have you pitch her, then grill you).
3. **A packaging-company tool suite + the /goal pipeline.** A robust, A++-prompted set of tools that make sense for a venture-packaging company, surfaced as an intelligent quick-select menu on thread/chat screens. Every tool feeds one multi-agent loop: **identify & set goal → decompose the work → assign the right agent → coordinate dependencies → execute in order → review against the original goal → gate before shipping → save what worked → report only what matters → verify goal as completed → commit/push (for code tasks)**. Architecture must be **model-agnostic** with **Fable 5 (Claude) as the default orchestrator** and the existing codex path kept as a swappable backend.
4. **Design excellence everywhere.** Rename the main tab from "Office" to **BonfireOS** (desktop and mobile), surprise-and-delight animations wherever they earn their place, A++ best-in-the-world design on both desktop and mobile screen sizes.

## Current System

Stack: Go 1.25 backend (Pion WebRTC v4, Opus via cgo) + one monolithic vanilla-JS `index.html` (34,900 lines — all CSS/JS/tabs, no build step). Docker Compose on a DO VPS (`thebonfire.xyz`) behind Caddy + coturn. Six fixed accounts, no public signup. Full subsystem map below (from a fresh code exploration, 2026-07-03):

### WebRTC
- Room PC created at `index.html:22575`; RTC config from `/client-config`. Private Scout voice PC at `index.html:20628` (`beginPrivateRealtimeVoiceSession`).
- Audio constraints ladder: `mediaConstraints` `index.html:16866–16884` (echoCancellation/noiseSuppression/AGC/voiceIsolation + goog* legacy), fallback ladder `23313–23414`.
- **Noise suppression already ships**: RNNoise WASM AudioWorklet in `public/voice-focus/` (`rnnoise-processor.js`, `rnnoise.wasm`), graph built in `createOutboundAudioForSource` (`index.html:23427–23490`), three modes off/worklet/voice-focus (radios at `16096`), health diagnostics `22896–22979`, benchmark `scripts/voice-focus-benchmark.mjs`. It is a hand-rolled gate (noiseFloor/speechFloor/strength), not a trained/adaptive integration.
- Mobile: `isMobileDevice` `16830`, aspect-ratio pin dropped on mobile (`16850`), phone layout query `16533`. Native Apple clients exist in `apple/` (parallel track — out of scope this wave except not breaking `/native/config`).
- Reconnect: client ICE restart w/ backoff `16741–16743`, `22648–22720`; server renegotiation + stale-answer suppression `main.go:141–303, 2103–2282`; ICE credential rotation `main.go:2720`.
- Persistence: localStorage `bonfire.audio.settings.v1` per-account (`16815`, `17367–17449`), preferred mic (`17854`), video tile order (`21740`). Settings dialog "audio & video" section at `16029`.

### Scout / Realtime voice
- Model `gpt-realtime-2` (`kanban.go:26`). Two surfaces: shared **room Scout** (server-owned peer, `startRealtimePeer` `kanban.go:508`, wake-phrase gated) and **private Realtime-2 voice** (per-user WebRTC: `main.go:1076` → `createPrivateRealtimeVoiceCall` `kanban.go:981`; instructions `1028`).
- Master tool registry `kanbanTools()` `kanban.go:1678` — 29 tools incl. `control_app`, `create_artifact`, `launch_agent_thread`, `answer_memory_question`, `propose_codex_task`, `create_package`/`attach_to_package`/`advance_package_stage`, `send_notification`, `post_to_channel`, `create_channel`, `meeting_recap`, `catch_me_up`, `start_grill_session`/`end_grill_session`.
- **Private-voice allowlist** `privateRealtimeVoiceTools()` `kanban.go:1041–1066` — only 14 tools; grill + recording + board mutations are room-only (`grill.go` enforces). Tool bridge: `main.go:1132` → `applyPrivateRealtimeVoiceTool` `kanban.go:2643`.
- Office chat: `sendScoutChatViaOffice` `index.html:26701`; two backends — ephemeral ws chat (`scout_chat.go`) and persistent threads/channels (`scout_chat_threads.go`, 1,174 lines; `@scout` mention gate; attachments).

### Agentic / codex pipeline
- `launch_agent_thread` → `agent_thread_runner.go:47` (modes research/design/grill/workflow, contracts at `581`); threads write running `os_artifact`s with `agentLoop`, `workflowStages`, `goalStatus`, `reviewGate`, `progressPercent`.
- Worker modes (`codex_runner.go:50`): default `openai_text_response`; `codex_exec` for real Codex. Sidecar queue `codex_runner_queue.go` (1,084 lines): authorities read_only/workspace_write/external_write, statuses incl. `approval_required`; result callback `/internal/codex/jobs/result` (`687`); external-write approvals admin-gated to aj@shareability.com (`main.go:1187`).
- Human gate: `propose_codex_task` → proposal cards (`codex_proposals.go:33`), confirm/dismiss endpoint (`140`). Board linkage auto-advance in `linkage.go` (Jaccard title match).
- **Prior art:** `docs/plans/codex-goal-workflows.md` already specifies the exact 10-step goal loop and the realtime-as-control-plane / workers-as-execution-plane boundary. `mode=workflow` artifacts already scaffold the loop. What's missing: a real orchestrator executing the loop, model-agnosticism, quick-select surface, and Fable/Claude backend.

### Threads/chat, notifications
- Thread records `scout_chat_threads.go:20–36` (kind `scout_chat_thread`, visibility private|public). **No slash-command/quick-action system exists** — closest is `openOfficeTool` (`index.html:19295`) routing research/design/grill composers. Notifications durable + deferred (`notifications.go`; bell UI `index.html:14847`).

### Memory / intelligence
- Single JSONL store `data/meeting-memory.jsonl` (`memory.go`, kinds: transcript, brain, board_update, archive, os_artifact, scout_chat_thread, codex_proposal, mission_insight, decision, decision_pass, package).
- Ambient workers on a shared recipe (`agent_runner.go:31`): brain (5m, `gpt-5.5`), board (2m), decision ledger (5m), mission intelligence (15m).
- Recall: `answerAssistantQuery` `memory_query.go:50`; text-match search only, **no embeddings**. Relevance filtering is "visibility asymmetry" (`memory.go:709`) — UI-state kinds hidden from Scout. **No slop-discard/quarantine mechanism exists.**

### Nav shell / settings
- "Office" label: rail markup `index.html:14791–14794`, `toolTitles` `19217–19225` (phone office title renders 'bonfire'), `syncToolTopbar` `19212`. Tab routing `setActiveTool` `19280`; `appShell.dataset.tool` drives everything. Settings dialog sections at `16026–16030`.

### Build/test/deploy
- Local keyless: `go run .` on :3000 — whole shell works without `OPENAI_API_KEY` (Scout features 503 cleanly). Tests: `go test ./...` (mandatory pre-deploy; VPS has no Go); frontend asserted from Go tests (`frontend_latency_test.go`, `assistant_http_test.go`). Deploy: rsync to `/opt/meetingassist`, compose rebuild, verify `thebonfire.xyz` (memory: bonfire-vps-deploy-ops).

## Design Requirements

1. **Quality bar:** "Feels like magic, works flawlessly." A++ best-in-the-world design; surprise-and-delight where it earns its place. Critic gate at 9.5/10.
2. **WebRTC:** stable desktop+mobile simultaneous join; noise suppression on by default and honest in settings (state, device, whether re-train/re-enable needed — prefer intelligent zero-training suppression); AV settings persist per device+account; lightweight video "looks"/touch-up only this wave (**no ML segmentation/blur** — user-confirmed).
3. **Scout Realtime-2 scope:** the private-voice allowlist must grow to (or near) tool parity — open tabs, notify users, post to threads/channels, start chats + post-as-user (with disclosure), read responses aloud, recall meetings, initiate packaging requests, self-run grill mode privately. Safety gates (human approval for external writes) stay intact.
4. **/goal pipeline:** one orchestration loop (the 10 steps above) behind every tool; **model-agnostic agent-runner abstraction with Fable 5 (Anthropic API) default and codex as swappable backend** (user-confirmed). Voice (Scout), text command (`/goal <objective>`), and quick-select menu all converge on the same pipeline. Artifacts produced must land as first-class `os_artifact`s visible to Realtime-2 recall.
5. **Tool suite:** curated packaging-company tools (research, design, deck, one-pager, market map, comps, rights, etc. — Domain Strategist to define) each with A++ prompt templates; quick-select menu on thread/chat screens (user-confirmed: menu + `/goal` + voice).
6. **Slop policy:** quarantine-then-auto-expire (user-confirmed): irrelevant material moves to a visible "discarded" archive excluded from recall, reviewable/restorable in settings, auto-deletes after ~30 days. Never silent hard-delete.
7. **Rename:** main tab "Office" → "BonfireOS" on desktop and mobile.
8. **Scale/constraints:** 6 users, one VPS (4GB-class), one always-on room; frontend is a single 34.9k-line file (no build step) — design must specify how to work inside that constraint; `go test ./...` must pass; keyless-local must keep working; don't break the native Apple `/native/config` contract.
9. **Output of the process:** a unified design specific enough for `/wave-plan` to convert into implementation waves.

**Assumptions (user confirmed after initial AFK):** Fable-5-default model-agnostic runner; quick-select + /goal + voice; quarantine slop policy; lightweight video looks only.

## What The Team Needs To Produce

| Specialist | Deliverable File | Key Questions |
|---|---|---|
| Product Lead | `docs/plans/spectacular-os-product.md` | System architecture across the 4 pillars; how the /goal orchestrator, tool suite, Scout parity, and intelligence loop compose; user journeys; sequencing logic; which "next-level" ideas to include |
| Media/RTC Engineer | `docs/plans/spectacular-os-rtc.md` | Concrete plan to make AV flawless: connection stability matrix (desktop+mobile simul-join), noise-suppression upgrade path, video looks pipeline, settings persistence + honest status UX, failure/recovery modes, test/verification harness |
| Domain Strategist | `docs/plans/spectacular-os-domain.md` | The packaging-company tool suite: which tools, the A++ prompt template per tool, how tools map to package stages, the "business as intelligence" flywheel, slop-classification criteria, next-level venture-group ideas |
| Technical Analyst | `docs/plans/spectacular-os-technical.md` | Model-agnostic agent-runner design (Fable 5 default, codex swappable); /goal loop execution engine (schemas, state machine, gates); Realtime-2 tool-parity plumbing; slop quarantine data model; embeddings/recall question; risk register |
| UX Designer | `docs/plans/spectacular-os-ux.md` | Quick-select menu design; Scout do-it-all voice UX; BonfireOS rename + shell polish; surprise-and-delight inventory; mobile parity; settings honesty UX; how to achieve A++ inside one 34.9k-line file |

## Team Roles

Default team adapted for this topic (UI/UX + infra heavy):
- **Product Lead** — owns cross-pillar architecture, orchestration story, journey coherence, idea triage.
- **Media/RTC Engineer** (5th specialist, added for pillar 1) — owns WebRTC stability, audio pipeline, video looks.
- **Domain Strategist** — owns the venture-packaging framework, tool suite content, prompt architecture (absorbs Editorial Lead's prompt-quality mandate).
- **Data/Technical Analyst** — owns agent-runner abstraction, /goal engine, schemas, memory/slop data model.
- **UX Designer** (swapped in for Editorial Lead) — owns visual/interaction design, delight, mobile.

Critic adaptation (Phase 4): Quality Hawk adds **UX Coherence (1-10)** and **Operability (1-10)**; User Advocate unchanged.
