# Bonfire — the OS for a venture-packaging group

**A ranked roadmap for maximum "woah" — grounded in the plumbing that already exists.**

Bonfire today: one always-on room, a transcript pipeline (audio → transcript → brain → board), Scout in two voices (shared room presence with a full tool belt, private per-user sessions), agent threads in five modes (research / design / grill / workflow / artifacts) backed by a codex sidecar with human approval gates, room chat that flows into the transcript stream, public channels, a persistent notification bell, and codex proposal cards. The thesis of this roadmap: **stop adding features and start closing loops** — every item below turns existing one-way plumbing into a round trip that feels like the company is thinking.

---

## NOW — a day each

Ranked by wow-per-hour. Each of these is mostly wiring, not building.

### 1. "Scout, grill us" — live pressure-test mode in the room
**What:** A new room voice tool `start_grill_session {topic, persona}`. Scout flips its own realtime session instructions (the server-side room peer already supports `session.update` — the exact mechanism `refreshRealtimeBoardContext` uses) into a skeptical-investor persona and starts asking questions *out loud*, one at a time, listening to the team's spoken answers. `end_grill_session` restores normal instructions and files the full exchange as a grill artifact.
**The wow:** Someone says "Scout, grill us on the Nimbus package" and the room's ambient assistant becomes a hostile VC — voice, timing, follow-up questions. Nobody has seen a meeting tool do this.
**Reuses:** server-side room peer + `session.update`, transcript lane (captures Q&A for free), `launch_agent_thread(grill)` for the post-session written report, `create_artifact`.
**Effort:** ~1 day. New tool schema in `kanbanTools()`, instruction-swap state on `kanbanBoardApp`, restore-on-close.

### 2. Meeting recap on demand — "Scout, where are we?"
**What:** A `meeting_recap` tool that forces an immediate brain pass over the current meeting window (the `flushAmbientAgentsForArchive` machinery already does exactly this with `minBatch=1`), then speaks a 30-second summary and posts it to room chat. Variant: `catch_me_up` scoped to a late joiner's join time, delivered only to them via the private-voice path or a `me`-audience notification.
**The wow:** Walk in ten minutes late, say one sentence, and the room tells you what you missed — decisions, open questions, who's carrying what.
**Reuses:** `runAmbientAgentOnce` / archive-flush pattern, `answerMemoryQuestion`, room chat broadcast, `send_notification(audience: me)`.
**Effort:** ~1 day. The summarizer, the memory query, and both delivery channels all exist.

### 3. Wake-word presence — Scout visibly *hears* its name
**What:** The transcription lane already sees every utterance. Watch completed transcripts for "Scout" / "hey Scout": pulse the topbar mark and voice island (the `.is-listening` breathe switch is already in the CSS), and when the room voice session is idle, auto-arm it so the next sentence is treated as addressed to Scout.
**The wow:** Say "Scout" mid-conversation and the whole shell takes a breath. The assistant stops being a button and becomes a presence.
**Reuses:** the `rememberTranscript` funnel (a single choke point for every transcript), `broadcastAssistantEvent`, voice island states, the existing breathe animation token.
**Effort:** ~1 day. A string match at one seam plus a client cue; the auto-arm is the only careful part (respect `set_voice_control`).

### 4. Voice → channels: "Scout, tell the team…"
**What:** Two tools: `post_to_channel {channel, text}` and `create_channel {name}`. Scout posts on your behalf (author = you, via Scout) or as itself into the public channels, and drops a `chat`-kind notification with a deep link so the bell lights up for everyone.
**The wow:** Mid-meeting: "Scout, put that in #dealflow and flag Tyler." Done before the sentence ends — channel post, notification, deep link.
**Reuses:** public-visibility chat threads + `saveScoutChatThread`, the chat_thread websocket fanout, `send_notification` with `tool: chat`.
**Effort:** ~1 day. Both storage and fanout exist; this is tool schema + dispatch + allowlist.

### 5. Voice-proposed codex work — expose `propose_codex_task` to Scout *(shipped)*
**What:** `propose_codex_task` was already dispatched in `applyToolCallArgs` and used by the board worker — but not exposed in `kanbanTools()`, so Scout couldn't call it by voice. The schema, both private-voice allowlists, and both instruction builders now expose it, so Scout can queue real agent work that lands as an approval card.
**The wow:** "Scout, have someone research comparable exits for creator-led IP" → a proposal card appears on everyone's screen with approve/reject. Voice → staffed work in one breath, human gate intact.
**Reuses:** literally everything — proposal store, cards, approve/reject endpoint, codex queue, notifications on completion (`notifyAgentThreadCreator`).
**Effort:** ~half a day. The cheapest wow in the codebase.

### 6. "Remind me after the meeting" — deferred notifications
**What:** Extend `send_notification` with `deliver: "now" | "after_meeting"`. Deferred records queue in the notification store and flush inside `archiveMeeting` before `rotateMeetingID`.
**The wow:** "Scout, after this call remind me to send Joel the deck" — and the bell rings the moment the meeting archives, not while you're mid-conversation.
**Reuses:** notification store + persistence, the `archiveMeeting` hook, existing bell UI.
**Effort:** ~half a day.

---

## NEXT — a week each

### 1. The Package — venture packages as the first-class object
**What:** Today the artifact library is a flat list and the pipeline lives in people's heads. Make `package` a real record (new memory kind, same JSONL store): title, IP, stage (`thesis → research → design → pitch → grill → assembly`), and attached artifacts per stage. A new `packages` tool surface renders each package as a glass stage-rail; each stage launches the matching agent-thread mode; the final stage compiles every published artifact into one investor-ready document via an `artifacts`-mode codex job. Scout tools: `create_package`, `advance_package`, `attach_to_package`.
**The wow:** The whole business — every IP, every deal, exactly where it stands — on one screen, and saying "Scout, advance Nimbus to grill" moves real work. This is the moment Bonfire stops being a meeting tool and becomes the company.
**Reuses:** the five agent-thread modes map 1:1 onto pipeline stages; `os_artifact` metadata + `workflowStages`; codex queue + approval gates for assembly; kanban tags for linkage; the tool-shell nav pattern (`data-tool`).
**Effort:** ~1 week. Record type + CRUD + one new surface + 3 tools; assembly is a prompt over existing artifacts.

### 2. Investor-Q&A grill mode with live scoring
**What:** The deep version of NOW #1. During a pitch rehearsal, Scout runs a structured Q&A round: a question bank generated from the package's artifacts, questions asked by voice, answers captured by the transcript lane and attributed by speaker, each answer scored live (evidence / clarity / confidence) on a glass scorecard in the room, weak answers flagged with the exact past-meeting context that contradicts or supports them. Ends with a graded report artifact and per-person notifications.
**The wow:** A live score ticking on the wall while the founder answers a hostile question — then "your answer on CAC contradicts what Tim reported on June 12th." Rehearsal becomes a sport.
**Reuses:** transcript lane + speaker attribution, the grill mode contract, `answer_memory_question` for contradiction lookup, `active_speaker` events for UI, artifact + notification delivery.
**Effort:** ~1 week. The scoring model call and the scorecard surface are the new work; capture and voice are free.

### 3. The Decision Ledger
**What:** Decisions are currently keyword-scanned at archive time (`extractDecisionItems`) and then buried in archive JSON. Promote them: a `decision` memory kind with structured metadata (what, who committed, meeting, date, package), extracted by the brain worker each pass, rendered as a browsable ledger surface, and injected into Scout's answer context so "what did we decide about X?" is grounded in a real record — and revisable ("Scout, supersede that decision").
**The wow:** Six months of "wait, didn't we already decide this?" ends. The company has a memory with receipts — every decision links to the meeting it was made in.
**Reuses:** brain worker + ambient-agent recipe (one `startAmbientAgent` call), memory store, archives for provenance links, the memory-query context builder.
**Effort:** ~1 week. Mostly a prompt contract + one surface. This is also the load-bearing prerequisite for the LATER interjection moonshot.

### 4. Per-IP war rooms
**What:** Not new media rooms — lenses. A war room is a saved filter across the whole OS keyed to a package/IP: its channel, its board cards (by tag), its artifacts, its decisions, its grill scores, its memory search scope. One click (or "Scout, open the Nimbus war room") and every surface narrows to that IP.
**The wow:** Context switching between three live deals feels like changing channels, not digging through lists.
**Reuses:** kanban tags, channel records, artifact/package metadata, `control_app` for voice navigation, the `data-tool` shell state machine.
**Effort:** ~1 week. It's a filter predicate threaded through existing renderers plus a switcher UI.

### 5. The Morning Brief
**What:** A per-user daily brief composed at first sign-in (or a fixed hour): overnight codex results awaiting review, proposals pending your approval, board deltas, key dates in the next 72h, unread channel activity — rendered as an office-home card and optionally read aloud by the private voice session on request ("Scout, brief me").
**The wow:** You open Bonfire with coffee and your company reports to *you*.
**Reuses:** notification store (unread), artifact statuses, board state + key dates, channel previews, the office home surface, private voice.
**Effort:** ~1 week, mostly composition logic; every input already has a snapshot function.

### 6. Research autopilot — Scout settles debates before the meeting ends
**What:** A fourth ambient agent (one more `startAmbientAgent` call, same recipe as brain/board) that watches transcript windows for *open factual questions the room couldn't answer* ("what did that format sell for?", "who reps that showrunner?"), quietly launches a research agent thread, and posts the sourced answer to room chat with a bell notification when it lands — often mid-meeting. Strictly gated: max two concurrent, a confidence threshold, suppressed when someone says "don't look that up."
**The wow:** The team argues about a comp for five minutes, moves on — and eight minutes later the bell rings with the number and a source. The debate is over before anyone remembered to google it.
**Reuses:** the `ambientAgentConfig` runner + cursor semantics, transcript entries, `launchAgentThread("research", …)`, codex queue for deeper digs, room chat posting, `send_notification`.
**Effort:** ~1 week; the detection prompt and suppression heuristics are the work, not the plumbing.

### 7. Commitments engine — attributed follow-through
**What:** At archive time (and on brain cadence), extract *commitments* — who said they'd do what, by when — using speaker attribution, and post each as a proposal card (`reviewGate: approval_required`) so a human confirms before it becomes a kanban card with owner + due date. Confirmed commitments notify the owner (`audience: me`), quote their own words back ("you said: 'I'll have the lookbook by Friday'"), and nudge as the date approaches. A kept/slipped tally per person feeds the Weekly Memo.
**The wow:** Nobody takes notes, nobody assigns action items — and Tuesday morning everyone has their own confirmed to-dos with due dates, in their own words.
**Reuses:** the archive flow + `flushAmbientAgentsForArchive`, speaker-attributed transcripts, the proposal/reviewGate pattern, `create_ticket`, notification persistence for nudges.
**Effort:** ~1 week (extraction + confirm flow ~3d, nudge scheduling ~2d).

---

## LATER — the moonshots

### 1. Scout interjects — a participant with a conscience
**What:** Scout continuously cross-checks the live transcript against the Decision Ledger and package artifacts. On a high-confidence contradiction ("let's price it at $50k" when the ledger says $75k was committed three weeks ago), Scout *speaks*: a short, cited interjection through the room peer's existing audio-out, with a strict politeness budget (max N interjections per meeting, backs off when overruled, a visible "raise hand" state on the voice island before speaking).
**The wow:** The company's memory has a voice and a seat at the table. This is the demo that makes people say the sentence this roadmap is named after.
**Reuses:** the room peer already speaks; the transcript funnel; the Decision Ledger (NEXT #3); voice island states for the "hand raised" cue.
**Effort:** 3–4 weeks. The hard part is judgment (contradiction confidence + interruption etiquette), not plumbing.

### 2. Ambient consensus detection
**What:** A live read of where each person stands on the question currently being discussed — inferred per-speaker from attributed transcripts — rendered as a quiet consensus meter in the room. When alignment crosses the threshold, Scout asks "sounds like a decision — should I log it?" and one word crystallizes it into the ledger and the board.
**The wow:** Decisions stop evaporating. The room *watches itself agree* and captures the moment it happens.
**Reuses:** speaker attribution + active speaker, brain worker cadence, the Decision Ledger write path, room chat for the confirmation prompt.
**Effort:** 3–4 weeks; stance inference quality is the risk, the pipeline is not.

### 3. The Weekly Memo — an investor-ready company report, untouched by human hands
**What:** A weekly ambient agent composes the company memo: per-package movement (stage advances, grill scores), decisions made with links, agent output shipped, pipeline risks — written in the artifacts-mode contract, published to the artifact library, and emailed via the existing SMTP/Resend path. The founder reviews via the same approve gate as codex external writes before it leaves the building.
**The wow:** Sunday night, a memo that reads like a chief of staff wrote it — sourced entirely from meetings the team already had. Forward it to LPs as-is.
**Reuses:** archives + memory snapshots, package records, codex queue with the `external_write` approval gate, meeting-notes email plumbing.
**Effort:** 2–3 weeks once Packages and the Ledger exist.

### 4. Market twin — every package watches its own market
**What:** Each package gets a standing ambient watcher: scheduled research threads monitor comps, competing announcements, talent moves, and buyer appetite relevant to that IP's thesis. A material move produces a "thesis check" notification — "a competing anthology was just announced; here's how it changes the comp set" — and stamps a freshness score on the package's thesis stage.
**The wow:** The packages defend themselves. A six-person shop gets the ambient market awareness of a studio's development department.
**Reuses:** package records (NEXT #1), the ambient-agent recipe, research threads + codex queue (network access rides the existing authority classes and approval gates), notifications, the package stage-rail UI.
**Effort:** 3+ weeks; source quality and noise suppression are the battle.

### 5. The overnight staff
**What:** Board cards tagged `@scout` become an overnight work queue: the codex sidecar picks them up after hours under authority gates, produces artifacts or proposals, and the Morning Brief opens with "while you were out, I finished three things and need two approvals."
**The wow:** A six-person company with a night shift.
**Reuses:** kanban cards + tags, codex queue + authority classes + approval cards, notifications, the Morning Brief (NEXT #5) as the delivery surface.
**Effort:** 3+ weeks; scheduling and failure hygiene are the real work.

### 6. The Panel — a simulated investor room
**What:** Grill mode's final form: Scout runs a multi-persona panel (the skeptic, the operator, the believer) with distinct voices taking turns, cross-examining a live pitch end-to-end, scored per persona, with a term-sheet-style verdict artifact.
**The wow:** A full pitch dress-rehearsal against a partnership meeting, any hour, no scheduling.
**Reuses:** grill session machinery (NOW #1 / NEXT #2), realtime voice, package artifacts as the panel's diligence material.
**Effort:** 4+ weeks; multi-voice orchestration on one realtime session is the frontier.

---

## Sequencing note

The horizons compound deliberately: NOW #1/#2/#5 make Scout feel alive this week; NEXT #1 (Packages) and #3 (Decision Ledger) are the two structural investments everything in LATER stands on — the ledger unlocks interjections and consensus capture, packages unlock war rooms, the memo, and the market twin; the commitments engine and research autopilot ride speaker attribution and the ambient-agent recipe as they exist today. If only two NEXT items ship, ship Packages and the Ledger.

---

## Learned from simulation (2026-07-02)

A three-scene live simulation of real team usage (kickoff meeting → async work → package sprint) produced a split verdict: **the intelligence half genuinely works — the delivery half was broken**, so end-to-end value landed for exactly one person (the admin). The upstream loop closed unprompted with verifiable quality (accurate themes in ~3 min, board cards with the exact spoken owner assignments, proposals matching the called-out action items, artifacts quoting room chat verbatim, an A-grade hallucination refusal), but three stacked last-mile failures made 3 of 4 employees experience "agents that never finish" while the server had finished in 35 seconds. The 14 quick fixes from that synthesis shipped alongside this note; the structural items below did not.

### Structural items (not quick fixes)

1. **Artifact delivery/permissions model.** The library is admin-gated to one hardcoded email, artifacts list by raw prompt text with empty titles, and the only non-admin surface is the thread card. Needs a first-class shared artifact surface: titles, a read-only reader for everyone, and a publish action that pushes a readable card into the originating channel/room.
2. **Agent threads are one-shot with no memory of their own outputs.** No in-thread reply composer; answers to a scorecard's questions land as unattached channel messages and every follow-up spawns a fresh run. Needs rerun-with-context (prior artifact body + user replies feed the next run).
3. **Scout retrieval cannot read completed artifact bodies.** Grade A on chat-history grounding, grade D when asked to reconcile channel numbers with an artifact — it returns board-card metadata instead. Index artifact bodies into retrieval and rank them above card metadata for comparison questions.
4. **No event linkage between proposals, threads, artifacts, and board cards.** The proposal→thread→artifact chain exists; the artifact→card hop (attach + advance column) is missing, so the board actively lies about package state.
5. **Office-mode sessions don't consume room-scoped events.** One coupling behind three symptoms: missing completion events, board/memory requiring a room join, and no room-chat unread outside the room. The always-on office needs a unified push channel every authenticated session consumes. (The quick fixes paper over this with polling + HTTP snapshot reads.)
6. **IA/naming: two different "chat"s** (rail chat vs the room-chat dock) and team-shared threads under a tab labeled "private". The word-to-surface mapping and the private/team ownership model need a rethink, not a copy tweak.
7. **Contribution lens measures message count**, and stale kanban cards put ex-participants on the leaderboard. Weight fuel by synthesized contribution (whose lines became themes/consensus) or people learn to spam short messages.
8. **Meetings lack stable identity:** hardcoded name, join-relative per-participant clocks, no meeting ID memory/search can key on. First-class meeting objects are the prerequisite for "return the artifact to the meeting it came from". (The label now derives from the brain's dominant theme; the shared meeting clock is deferred on this item.)

### Top ideas surfaced by the simulation

1. **Auto-title meetings from brain themes** (near-zero effort, feels sentient) — the OS knew the meeting was "EMBERS format development" while its own label said "platform standup". *Shipped in the quick-fix pass.*
2. **Close the loop where it started:** post finished agent work back into the channel/room where the need was born — this emerged independently in all three scenes. A "share to #channel" action (or Scout auto-offering it) converts the pipeline's biggest weakness into its signature move.
3. **Re-grill readiness dial:** feed the team's answers back into a rerun and track the score delta (6.2 → 7.1) — turns a one-shot report into the team's pitch-readiness metric.
4. **Board cards auto-advance from artifact events:** attach the completed artifact to its card and move Backlog→Done, making the board self-updating.
5. **The "Package" binder:** a mission-scoped surface pinning the one-pager, grill scorecard, rights map, economics scan, and open gaps — the artifact the studio actually sells. Highest effort of the five.
