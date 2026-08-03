# STRIDE Next Evolution — Master Architecture and Execution Ledger

**Status:** Active Build 31 close-out. The Scout voice, dictation, typed-thread,
long-thread performance, explicit invited-room-participant, and E10 security
foundations are integrated on local `main`. The user has explicitly authorized
the reviewed Git push, exact-release VPS cutover, required migrations, and the
next internal TestFlight build after the deterministic gates pass. Scout may be
invited as a visible, attributed room participant only under current room,
media-generation, audience, and unanimous capture/transcription/model-analysis
consent. Meeting transcription remains independent and cannot silently invite
Scout. Marketplace employee audio remains default-off until an exact signed
provider/model/voice/config qualification and external anchor are available.
Release authority does not waive canonical-repair confirmation, consent,
custody, physical-device, HA/DR, pilot, soak, or independent evidence gates.

**Date:** 2026-07-28

**Naming:** **STRIDE** is the long-term operating-system name. **Bonfire OS** is the current application and repository implementation. Repository, GitHub, package, host, and code-identifier renaming are explicitly outside this plan and remain a separate future workstream.

**Source of truth:** This file is the single plan for the next evolution. It carries forward, rather than replaces, the proven foundations and evidence in:

- `docs/plans/bonfireos-2.0-execution.md`
- `docs/plans/bonfireos-w2-design.md`
- `docs/plans/canonical-event-acl-v1.md`
- `docs/model-routing-master-plan-2026-07-11.md`
- `docs/llm-routing-audit-2026-07-11.md`
- `docs/memory-architecture-study-2026-07-10.md`
- `docs/plans/multi-room-2026-07-08.md`
- `docs/plans/rooms-ux-2026-07-09.md`
- `docs/plans/voice-first-mobile-design.md`
- `docs/plans/the-table-design.md`
- `docs/plans/codex-goal-workflows.md`

External reference, adopted selectively rather than introduced as a runtime dependency: [Block Buzz at audited commit `90e058e`](https://github.com/block/buzz/tree/90e058ebf68137e048a409aec6616519379ff726). STRIDE adopts the useful separation of portable persona definition, runtime identity, and team membership; it does not adopt Buzz's collaboration substrate, permission defaults, prompt-only delegation, or agent-authored memory as organizational truth.

Older model recommendations are historical baselines, not current authority. The current official model references for this plan are [GPT-5.6](https://developers.openai.com/api/docs/guides/latest-model.md), [GPT-Realtime-2.1](https://developers.openai.com/api/docs/models/gpt-realtime-2.1), [GPT Transcribe](https://developers.openai.com/api/docs/models/gpt-transcribe), and [GPT Live Transcribe](https://developers.openai.com/api/docs/models/gpt-live-transcribe).

### Active Build 31 release checkpoint — 2026-08-02

- The final implementation boundary **A** is
  `757dca466bcf8f34c7fccb924a4a348bf5c0513b`. This plan is intentionally
  absent from A; its direct child **B** adds only this release checkpoint, so
  the ceremony can prove identical release-owned inputs across both commits.
- The first sealed Build 31 A/B archive stopped at image compilation, before
  maintenance or any production mutation, because release scope omitted the
  newly imported `internal/e10evidence` package. The scope now includes its
  non-test Go sources, requires an exact package sentinel, and has a regression
  test that inventories every internal package imported by production root Go
  files. The failed pair is superseded and cannot be resumed or relabeled.
- Two subsequent protected attempts passed progressively deeper gates and were
  cold-restored without data drift. They proved that Docker omits any stopped
  container—not only PostgreSQL—from network-inspect active endpoints, and
  that rollback must use the predecessor Compose sources captured from the
  running containers rather than a later mutable `/opt` file. The final pack
  separately proves stopped configured attachment, PostgreSQL-only membership
  before/after each one-shot, and PostgreSQL-plus-one-shot while it runs. Its
  sealed resolved predecessor Compose rollback has restored all eight volumes,
  matched migrations/table counts, recreated exactly six healthy services,
  and passed independent blocked/open HTTPS and TURN probes. Neither failed
  attempt reached a canonical append or can be resumed.
- The mobile rich-media feed now uses content-family recycle pools, one
  thread-owned long-message sheet, recycling-safe preview state, stable nested
  mapping keys, early image resizing, and a bounded recycle pool. The 264-item
  simulator stress fixture with large images and link previews scrolls without
  swaps, blank cells, jumps, or crashes; final acceptance must be repeated on
  the physical Build 31 binary.
- Scout no longer launches automatically when a human enters room media. An
  active member explicitly invites or dismisses him from Agent Team. His
  colored Newton's-cradle tile is a first-class stage participant, reflects
  listening/thinking/talking/degraded state, and his provider output is stored
  once as durable transcript speech attributed to Scout rather than re-entering
  STT as human audio.
- Room transcription and invited agents are separate lanes. Transcription may
  run for a fully consented sitting while Scout is absent. Inviting Scout also
  requires an unchanged audience and every participant's audio-capture,
  transcription, and model-analysis consent; any relevant authority change
  synchronously revokes the invitation.
- The core availability route is OpenAI: `gpt-realtime-2.1` for Scout voice,
  `gpt-live-transcribe` for low-latency Realtime deltas, `gpt-transcribe` for
  composer dictation and authoritative committed meeting turns,
  `gpt-5.6-terra` for typed Scout/routing, and `gpt-5.6-luna` for bounded
  extraction. `gpt-5.6-sol` remains the orchestration and independent-review
  seat at an explicitly selected reasoning level.
- The E10 convergence removes alternate specialist launch paths, owns request
  context at one boundary, binds signed provider/model/voice/config evidence,
  fixes qualification expiry, and fences the externally anchored ledger head
  with compare-and-swap semantics. Deterministic code convergence is not a paid
  provider qualification or employee-agent activation receipt.
- Render subprocesses now run in a dedicated process group and cancellation
  kills the full Chromium tree. This closes the legacy canary-timeout leak that
  exhausted the production render worker's PID budget and is a release-health
  prerequisite rather than a product-surface expansion.
- Remaining release order is intentionally short: push this final A/B
  checkpoint; run the manifest-confirmed canonical ceremony; activate and
  verify exact B on the VPS while preserving production data; build, submit,
  and verify Build 31; then complete physical rich-feed, room lifecycle, Scout
  invitation, dictation, typed-thread, and desktop cradle-motion acceptance.
  External employee qualification, real WebRTC/TURN breadth, ten reviewed
  pilots, the 24-hour/ten-sitting soak, immutable offsite backup, HA/DR, and
  anchor custody remain separate gates.

### Active execution checkpoint — 2026-07-29 through 2026-08-01

- `axx/main` and the local execution baseline were `4a3c0ba6a0c2cacbd10f768e9901033195bf91c5` at E0 entry; `stride-site/` remains untouched and outside release scope.
- Live identity is not trustworthy: the running local image digest, embedded/health version, OCI release label, deployed source hashes, and remote commit do not resolve to one revision. No deploy may claim exact-SHA acceptance until this is repaired.
- Live canonical state is blocked by a deterministic idempotency conflict, dirty/reconciled/checkpoint divergence, and an unknown-to-readiness outbox backlog. Consent authority reports `store_unavailable`; brain/recap/STT/embedding/Scout capability receipts are stale or failed. Provider inference canaries remain fenced.
- At zero occupancy, writers were quiesced from `2026-07-29T04:54:46Z` through `04:54:53Z` and a matched current capture was created for PostgreSQL, meeting data, Codex queue, and usage ledger. The VPS capture and independently copied Mac set match SHA-256 digests: canonical dump `7c9bfcb5be6dfb562db031bc08cc730122e5bf669464ae25f632a7cc4f86b1e5`, meeting data `57075d2efb0b15bb3fc6f45552ab98a1e1d4cb51fdcb74f8888ce32dc27cbcfe`, Codex queue `302ebea03ef8b47aaadb798974f41aac76913ec797713e206a41bdff0f84a776`, and usage ledger `e6f8cd0b49871ac066de3208bc4c727c89411970ec2eef61a572ae695677bde8`.
- The clean four-root bundle was sealed with the new authenticated `BFBKUP02` AES-256-GCM envelope. Plain bundle digest `17f00135cf851ae38918cd852d00d5cfcbe5e6c8e63feb1a7925b6892f78c7f5` round-tripped exactly after decrypting envelope digest `e6fe554add1f5169c0e77197015cae28b85242c1c7d859f0b4fb3dc2f89a1ccf`. The key is held separately with mode `0600`, but independent KMS/custody has not yet been proven.
- An isolated 4-vCPU/8-GB restore host at `143.110.149.164` is firewall-restricted to SSH from the operator IP; PostgreSQL is loopback-only and no public application port is open. All four roots restored with exact manifest parity. PostgreSQL 17 restored `11,090` canonical events, `442` pending and `10,648` delivered outbox rows, seven schema migrations, zero purge-ledger rows, and zero consent rows; a second timed database restore completed in two seconds with the same counts. This proves byte/root/database recovery, not yet purge-authority continuity, an authenticated application boot, signed restore evidence, or the 60-minute full-service RTO.
- A real DigitalOcean Spaces Object Lock probe created a disposable lock-enabled bucket, but configuration returned `NotImplemented`; the empty bucket and temporary access key were then deleted and verified absent. DigitalOcean Spaces therefore cannot satisfy the immutable-custody gate, and ordinary versioning must not be represented as WORM. A qualifying independent immutable store and key custodian remain external E0 gates.
- The live canonical conflict is bounded to a legacy importer defect: collection-level board `updatedAt` was reused as `OccurredAt` for unchanged cards. The candidate restricts drift compatibility to the exact deterministic `legacy.object.imported`/`board_card` family and adds crash-recoverable board lifecycle `PREPARED -> COMMITTED|ABORTED` transactions. An independent critic now reports **PASS**: every mode durably syncs file plus parent directory before commit, ambiguity freezes mutation and readiness, committed-only import remains idempotent, and repeated same-ID delete/restore generations remain ordered. A fresh clone of the captured state replayed the 9,506-event plan with 139 genuine missing imports, 26 allowed occurrence drifts, the five exact board repair candidates, a 149/149 event/outbox delta, zero projection mismatch, and no second-replay change. This is candidate proof, not authority to repair live state.
- The attachment repair now carries exact authenticated source authority and immutable revision through selection, model use, final commit, list/render/open/download, and revocation; upload grants are durable and idempotent; blob serving is fail-closed across check/read races; duplicate source IDs, MIME/size drift, guessed refs, private-to-public widening, recipient change, and stale derived text are rejected. Focused normal/race suites and the independent repeat critic report **PASS**. GIPHY/agent media remains compile-time disabled pending the separate policy/provider/privacy gate.
- Exact release identity now has an independent **PASS** at candidate level: archive, reviewed source, pinned build-input manifest, image, opened binary, OCI/runtime markers, `/healthz`, and `/readyz` must resolve to one release; activation sanitizes inherited release/Compose selectors and rollback requires a retained exact artifact. The release harness passes 9/9. Independent signing, registry custody, and an off-host receipt remain external hard gates, so no qualifying release has been built or deployed yet.
- Provider containment now has an independent **PASS**. Admission and accounting share one atomic scope lock; global workers share the global circuit/pass; durable pre-admission baselines and held checkpoints fail closed on ambiguous persistence; clean-install and legacy migration are evidence-bound; unknown checkpoint versions fail closed; and Taste/House health requires a matching durable typed artifact rather than liveness or a no-op pass. A test-only provider network fence covers every inventoried OpenAI HTTP/WebSocket, Anthropic, and fiscal.ai seam without blocking unrelated link-preview, mail, push, callback, or local HTTP behavior, and an independent critic reports **PASS**.
- Goal-workflow recovery now has an independent **PASS**: approval and terminal surfaces persist atomically; external-work runs reserve deterministic child/job identity before launch; restart recovery is idempotent; legacy random jobs are adopted only on one exact immutable-binding match; zero or multiple matches fail closed for operator reconciliation; and stale callbacks must match child, job, and generation.
- Two full-suite lifecycle races were isolated and closed. The Scout memory-answer test now joins its asynchronous SendEvent/broadcast tail by waiting for zero in-flight tool calls. Separately, `kanbanBoardApp.Close` now stops and joins meeting idle callbacks, and isolated websocket teardown orders socket/server close, handler drain, app/timer join, then actor reset. The first fix passed the exact predecessor/target pair ten times plus a 108-test predecessor sequence; the second passed deterministic lifecycle shutdown twenty times plus all 48 isolated-websocket tests and the implicated share test under race detection.
- Final local candidate evidence at the 2026-08-01 freeze: base `c7b4128f0f45d1b6443c73cbae3e54feceb735d3`; 324-file implementation manifest (excluding this self-referential ledger and the separately owned `stride-site/`) SHA-256 `057290ab5f8ac1e0f279d50bede9cf14189c02f91c986cc2430de15cb392e617`; full non-live-provider Go suite **PASS** with the root package in 310.115 seconds; full Go race suite **PASS** with the root package in 2,311.307 seconds; root Node 102/102; mobile 339/339 plus TypeScript; `go vet ./...`, `go mod verify`, changed-file `gofmt`, `git diff --check`, both production dependency audits, and Expo dependency alignment all **PASS**. No Go source changed after the full-race process began.
- The final deterministic vertical receipt passes in normal and race modes from `#team` through Suggested Work approval, Dog Perfect routing, `insights_opportunities_v1`, artifact/company-brain linkage, Marketplace/Mary lifecycle, Scout introduction, specialist success/failure isolation, signed restart, pause/offboard, and default-off rollback. The separate E9 integration runs the real local application/runtime and signed store against temp-only loopback replicas; it proves route persistence, room-scope control isolation, purge continuity, current restore, and stale-rollback refusal. It explicitly does **not** prove WebRTC/RTP/TURN/media-device continuity, provider quality, physical devices, production HA/RPO/RTO/restore/soak, release, or deployment.
- Native readiness on the final camera tree reports `localReady=true`, `simulatorReady=true`, `strictReady=false`; Xcode tests pass. Physical iPhone/iPad, TestFlight/signing/privacy, locked/background behavior, and provider-integrated acceptance remain E10 evidence. Missing, failed, ambiguous, unavailable, or wrong-device iOS camera proof now strips/stops video before signaling; only affirmative non-risk evidence or valid adaptive portrait/landscape geometry may publish video.
- Desktop private/channel chat is an explicit E4 surface. Rendered local Chrome/Firefox/WebKit QA covered public, private, project, rich-media, Marketplace, and responsive 390/1024/1280/1440/1728 views before the final remediation; the subsequent changes were backend I&O authority/persistence and native camera admission, not desktop presentation code. This is local rendered evidence, not a physical-device, signed-release, or production acceptance receipt.
- `insights_opportunities_v1` now persists its complete report body, immutable revisions, typed feedback, canonical successor WorkRun/Artifact/Outcome lineage, and strict token-free stage/critic receipts across signed restart. Artifact reads reauthorize both source and live destination authority. `private_share` remains intentionally rejected until a separate atomic, durable, revocable grant protocol is approved and verified; a reference is never treated as sharing authority.
- A fresh independent final Critic review reports **PASS** with no actionable P0/P1 after independently rechecking every previously failing area, rerunning the focused camera boundary set 30/30, reconciling the exact normal/race/native receipts, and recomputing the same 324-file implementation manifest digest. No earlier review or narrower passing suite substitutes for this final local sign-off.

---

## 1. Goal

Build STRIDE into a trusted, voice-first company participant that:

1. hosts stable first-class video meetings, guests, multiple rooms, screen sharing, and mobile participation;
2. provides best-in-class microphone dictation in every text composer, with polished feedback, company-aware transcription, explicit delete/submit controls, and unambiguous separation from personal Realtime voice and meeting media;
3. captures every consent-authorized meeting and shared company conversation as time-indexed evidence;
4. understands people, projects, commitments, blockers, decisions, storylines, artifacts, and evolving context across days, months, and years;
5. lets a person ask naturally for what happened five minutes ago, what they missed before joining, or what Erick shared in `#team` last week and receive a fast, permission-safe, source-linked answer;
6. recognizes real desired outcomes in calls and chat, suggests useful work to the right people, obtains explicit approval, routes the work into the correct existing or newly created project thread, runs it durably, and reports completion there;
7. makes Scout feel like a real, funny, context-aware coworker in `#team`—able to understand who said what, retrieve and safely post a requested file, and occasionally use an appropriate GIF—without turning the human group chat into an agent feed;
8. provides a curated internal Agent Marketplace where authorized people can hire distinctive, persistent agent coworkers across Insights, Marketing, Research, Design, and Builder roles, while Scout remains the primary relationship, chief-of-staff experience, and accountable coordination front door;
9. learns how the company collaborates without creating hidden psychological profiles or leaking private conversation;
10. routes every model call and agent stage to an intentionally chosen capability and reasoning level, with measured quality, latency, and cost;
11. remains useful and honest when AI providers, transcription, memory projections, workers, or infrastructure degrade.

### Definition of done

The evolution is not complete because code exists or `/readyz` says `ok=true`. It is complete only when the evidence matrix in §15 passes from one immutable release and production proves the integrated experience:

> Human conversation becomes trusted company memory; Scout feels like a grounded chief of staff, not a search box; people can hire, shape, and work alongside a legible roster of distinctive agent coworkers; agents contribute through controlled handoffs; an approved work outcome becomes a durable verified result in the right thread; guests and unauthorized users cannot observe any source, count, inference, or artifact they are not allowed to access.

---

## 2. Current baseline — point-in-time receipt at 2026-07-28T22:49:06Z

### Repository and release topology

- Remote `axx/main` resolved to `a7e1b6a1082a21383e215069d44dd34fda107cab`.
- The current local design branch is `design/voice-first-mobile` at `604485ce06d9a897e75d7b84ff5b2f2615c52813` and contains substantial later `#team`, link-preview, chat, and native work that is not on live `main`.
- The running container reported OCI revision label `cbc27df1cbd360619ecbd353bd9782cd0a20b358` and image digest `sha256:ac8710894bf3f44a399d2645b79d44ae7b1b15d42f3d66d4908e49dece5612a4`.
- The local untracked `stride-site/` directory is user-owned and must remain untouched.
- The local design branch must not be merged wholesale. Its changes need a file-by-file reconciliation against live `main`, preserving later production fixes and avoiding reintroduction of removed media/native behavior.
- Remote main, the running OCI revision, and currently surfaced environment/health release markers do not resolve to one trustworthy release. Same-release evaluation and deployment receipts do not qualify until OCI labels, embedded binary version, environment marker, source archive, registry/running digest, and health/capability output resolve to one immutable release. The values above are observations, not proof that the running binary was built from the labeled source.

### Proven installed foundation

- Usage/cost ledgers, static route controls, kill switches, and isolated Codex/render workers exist.
- Canonical PostgreSQL shadow infrastructure, event/ACL/consent/retention/approval contracts, outbox/jobs, and purge-aware authorization are installed.
- Per-room actorized media/Scout foundations, admission anchors, temporal evidence contracts, brain projection machinery, `insights_opportunities_v1`, evaluation collection, and HA/DR repository gates exist default-off or shadowed.
- `#team` exists as the flagged permanent public Table thread, with human-first `@scout` behavior, author identity, replies, reactions, files, read markers, push, and source-rendering foundations.
- Its current Scout prompt already requests a casual teammate style, but model history is limited to recent flat `user`/`scout` turns and does not preserve which coworker authored each human message or full reply/reaction context. Mention detection is substring-based rather than lexical.
- The Files surface already unifies direct uploads, authorized channel attachments, and agent artifacts, and native mobile already has an authenticated G-rated GIPHY picker. Neither is currently a Scout tool: Scout cannot safely select an existing Files object and post its exact revision, and it has no context-aware GIF response policy.
- Private Scout threads are intentionally excluded from shared recall and brain synthesis.

### Current live model/config state

- conversational voice: `gpt-realtime-2`, reasoning `high`;
- passive transcript model: `gpt-realtime-whisper`;
- Codex worker: `gpt-5.5`;
- digest production: disabled;
- `insights_opportunities_v1`: disabled.

### Hard entry blockers

The public app is serving and the container is healthy, but production intelligence is not currently acceptance-ready:

- canonical shadow is degraded at observed dirty high-water 9,816 versus reconciled/checkpoint 8,532 with `canonical idempotency key conflict`; these counters are volatile and must be re-read before any repair;
- durable consent authority reports `store_unavailable`;
- embeddings are failing with provider `429` responses;
- brain and recap last succeeded on 2026-07-11 and are stale;
- STT is stale and Scout is disconnected while the room is inactive;
- offsite backup is dormant/unencrypted and restore verification is false;
- provider quota remains an external commissioning dependency.
- recent usage evidence contains untagged Anthropic traffic, failed OpenAI text calls, and repeated embedding failures; new transcription model prices/service-tier dimensions are not yet complete in the repository price table. Cost claims and seat comparisons remain non-qualifying until seat tagging, accepted-output truth, and current prices are complete.
- current chat attachment sanitization validates a supplied blob reference's existence/MIME without binding it to the authenticated source object and caller ACL. Treat this as a security-repair gate: prove reachability, close the authorization downgrade, add guessed-reference/revocation tests, and do not build Scout file posting on the current path.

These facts make Evolution Wave E0 mandatory. They do not authorize a repair or production mutation under this planning request.

---

## 3. Non-negotiable invariants

1. **Conversation is evidence, not authority.** Spoken or typed words can create a candidate or suggestion; they cannot approve work, widen permissions, publish externally, or execute a side effect.
2. **Human approval is revision-bound.** Approval consumes one exact proposal revision and creates at most one idempotent run. Any change to evidence, scope, destination, authority, budget, or workflow invalidates the prior approval.
3. **Authorization precedes retrieval.** ACL filtering happens before body fetch, lexical search, embedding search, ranking, model context construction, analysis, or publication. Unauthorized counts and timing distinctions are also hidden.
4. **Derived knowledge cannot widen visibility.** Every assertion, digest, profile observation, proposal, artifact, and answer inherits the intersection of its sources and destination.
5. **Private Scout chat stays private by default.** It enters company memory only through an explicit share, publish, save-for-the-record, or policy-disclosed conversion chosen by an authorized user.
6. **`#team` is human-first.** The company group chat may feed shared memory, but Scout replies only when explicitly invoked. Proactive assistance is quiet and dismissible by default.
7. **Scout is always available, never always interrupting.** Passive consented capture and analysis do not grant conversational floor access.
8. **Video survives AI failure.** Room admission, media, chat, screen share, and deterministic navigation remain usable when every model lane is unavailable.
9. **No silent coverage claims.** Transcript, analysis, brain, workflow, and artifact freshness are separate high-water marks. Gaps and stale state are disclosed.
10. **No silent model fallback.** A stage may retry its pinned route. An alternate provider/model may restart the stage only from a durable checkpoint under an explicit policy, with full provenance and no duplicate side effect.
11. **One workflow first.** `insights_opportunities_v1` is the only production workforce product required by this plan. Additional authored workflows wait for its pilot gate.
12. **No generalized microservice fleet.** Retain a modular Go control plane, PostgreSQL/event substrate, isolated workers, Pion media rollback, and simple queues. Do not add Kafka or split services without measured necessity.
13. **Production data stays in the named Docker volumes.** Repository `data/` and `/opt/meetingassist/data/` are never production truth.
14. **Every wave is reversible.** Model changes are one-seat/one-variable canaries; data changes are shadowed and replayable; deployment keeps exact-SHA evidence and a tested rollback.
15. **Agent identity is provenance, not authority.** A stable name, avatar, membership, or signed runtime identity explains who acted; it never grants a tool, data scope, approval, budget, or permission to delegate.
16. **Decorative media is conversation, not company truth.** GIFs, reactions, and social tone may inform bounded conversational context, but they do not become asserted knowledge or authorization.
17. **Scout chairs every multi-agent meeting interaction.** Scout is the sole default agent with the meeting floor. A named specialist may join only through a visible, human-confirmed, time-bounded session with an audience-authorized brief, one-agent-at-a-time floor control, no acoustic agent loop, and immediate revocation on dismissal.
18. **Hiring is human authority.** Scout may recommend, configure, coordinate, coach, surface a safety concern, and draft a hire, pause, or offboarding change; deterministic policy may quarantine on a proven safety/cost/health breach, but only an eligible authenticated human can hire, expand access, activate a material update, restore from quarantine, or permanently offboard a teammate.
19. **Growth is evidence, not self-modification.** An agent may accumulate corrigible relationship memory, domain lessons, and performance evidence. It cannot rewrite its core identity, grant itself skills or permissions, claim competence from self-assessment, or silently change its model, prompt, tools, budget, or authority.
20. **Agent packages are untrusted requests, not executable authority.** A listing, publisher signature, popularity, paid status, or installed persona never grants data, tools, credentials, network access, code execution, or company-brain visibility. Organization memory and local personality evolution do not leave the organization in an exported or updated package.

---

## 4. Final product and architecture decisions

### 4.1 Outcome is central; “deliverable” is not

The core internal lifecycle is:

```text
authorized conversation/evidence
  -> WorkIntent candidate
  -> revisioned WorkProposal
  -> explicit approval
  -> durable WorkRun
  -> artifacts + verified Outcome
  -> result and status in the bound project thread
```

User-facing vocabulary is **Suggested Work**, **Work**, and **Results**. “Deliverable” is used only when the requested result is literally a report, deck, website, file, or other tangible output. It is not the central navigation or ontology term.

### 4.2 The company brain is an evidence graph plus rebuildable projections

Raw conversation events and authoritative transcript revisions are evidence truth. Decisions, commitments, storylines, collaboration preferences, summaries, and work opportunities are versioned projections with source edges. They can be rebuilt, corrected, superseded, or retracted.

The system must never rely on one cumulative prose summary as company memory.

### 4.3 Realtime is the conversational edge, not the operating system

The Realtime session manages low-latency speech, interruption, and tool conversation. Server-side STRIDE owns identity, consent, transcripts, retrieval, company memory, permissions, proposal/approval, workflow state, artifacts, verification, and publication.

Realtime receives bounded, audience-authorized context envelopes and tool results. It does not hold the entire company brain or become the durable owner of meeting history.

### 4.4 Meeting transcription has one authoritative lane

- `gpt-transcribe` is the default authoritative model for committed meeting turns and bounded composer dictation. Meeting turns use committed Realtime transcription input; composer dictation records one bounded clip and uses file/request-response transcription with company vocabulary, keyword, and language hints.
- `gpt-live-transcribe` is a provisional UX lane only when a shipped surface consumes ongoing sub-turn deltas: live meeting captions or measured early address detection. Composer dictation does not need it: the recording waveform is driven locally from microphone energy and only final `gpt-transcribe` text is eligible to send.
- A dual lane is justified only for that visible live behavior. It is not required for five- or thirty-minute recall.
- Provisional text never enters durable memory, creates an asserted claim, or surfaces Suggested Work.

### 4.5 Scout invocation is surface-specific

- Main Scout screen: direct conversation; no wake phrase.
- Private Scout chat: every user message addresses Scout.
- Public `#team` and project channels: `@scout` or an explicit Ask Scout control.
- Meeting text chat: `@scout`.
- Meeting voice: natural “Scout …” address or Ask Scout button, followed by a visible short engaged window for follow-ups.
- Proactive work detection: silent suggestion card; no unsolicited spoken interruption by default.

### 4.6 `#team` compounds into shared memory through a separate projection lane

Do not make the whole `scout_chat_thread` JSON searchable and do not remove the private-chat privacy pin. Emit one ACL-carrying `ConversationEvent` per public message, edit, delete, reaction, reply, file, and link, then derive shared knowledge from those events.

`#team` is work-sensing-enabled by default with clear company-visible disclosure. Other public/project channels opt in by policy. Private threads remain excluded unless explicitly promoted.

### 4.7 “Personality learning” means transparent collaboration profiles

STRIDE may learn explicit preferences and repeated low-risk collaboration patterns: expertise, preferred update style, terminology, normal level of detail, working cadence, and team communication norms.

It may not create hidden psychological diagnoses, infer protected/sensitive traits, turn a private emotional moment into durable company fact, or let an inference override an explicit instruction. Every inferred preference carries evidence, confidence, scope, recency, expiry, and correction/forget controls.

### 4.8 STRIDE owns durable orchestration; models perform bounded stages

Goal Loop, Strategic Design, Critic Loop, and Wave Plan become internal `WorkflowProfile` behaviors selected by the orchestrator. Users see outcome, status, evidence, approval, and required decisions—not process jargon.

OpenAI Responses multi-agent or provider agent frameworks may be evaluated for bounded parallel analysis, but beta/provider runtime state cannot replace STRIDE’s durable `WorkRun`, approval, ACL, idempotency, checkpoint, or audit ledger.

### 4.9 Pion remains the production media rollback

Current actorized Pion remains default until a managed media provider proves materially better under the same room, guest, transcription, recording, cost, failure-injection, and rollback gates. Self-hosting another media stack on the same VPS is not HA.

### 4.10 Expo is the canonical native client

The Expo/React Native application is the native product line for this evolution. Older parallel Swift/native Apple plans are reference material, not a second product implementation. Web and Expo iPhone/iPad are required release surfaces. Android receives compatibility coverage where the Expo stack already supports it, but Android GA and a parallel Swift client are not hidden requirements.

### 4.11 Guest-safe mode begins on admission, not link creation

An unused active guest link does not downgrade a member-only sitting. The first admitted guest latches that sitting into guest-safe behavior for the remainder of the sitting, because the guest may reconnect and guest-originated evidence remains part of the meeting record. Guests may receive current-room, audience-safe Scout answers only when the host policy allows; they receive no durable company recall, cross-meeting/project retrieval, work proposals, or approval authority.

### 4.12 Dictation, personal Realtime, and meetings are distinct audio modes

Every text composer exposes the same **Dictate** microphone control. It records a bounded utterance and transforms that same composer rectangle into a real microphone-level waveform with **Delete**, **Stop**, and **Send** controls. Stopping holds the clip; it does not transcribe or send. Delete discards it. Send finalizes the clip, changes the rectangle to an explicit **Transcribing** state with a compact progress indicator, and posts the completed text through the ordinary message pipeline. It is text entry, not a Realtime conversation and not a meeting transcript lane.

Only the final successfully sent text becomes a normal `ConversationEvent` under that destination’s existing visibility and retention policy. The raw dictation clip, partials, failed transcript, and private in-room utterance never enter the company brain or meeting evidence.

The main Scout screen separately exposes **Live Voice**, backed by `gpt-realtime-2.1`. The controls must not look or behave interchangeable. A client-level `AudioFocusCoordinator` owns one microphone capture generation and the mutually exclusive foreground modes `idle`, `composer_dictation`, `personal_realtime`, and `meeting_media`.

- Starting Dictate while personal Realtime is active ends that Realtime session first, records the terminal reason `superseded_by_dictation`, revokes its microphone generation, and only then starts bounded capture.
- Joining a video room ends personal Realtime before acquiring meeting media. If non-room dictation is recording, STRIDE stops and parks it as an unsent local clip rather than transcribing, sending, or silently discarding it. Leaving the room does not silently restart either mode.
- Starting personal Realtime while already in a room is disabled; the room’s Scout experience is the meeting-bound invocation path, not a second personal audio session.
- An in-room text composer may dictate only through an explicit private dictation submode: preserve the participant’s prior mute state, mute the outbound room track before recording, exclude those frames from the meeting transcript/analysis, then restore the exact prior mute state after completion or cancellation.
- Every transition waits for a terminal acknowledgement or a bounded forced close before the next microphone generation starts. Late audio/events from an old generation are discarded.

This is a product-level audio ownership contract across web and Expo, not an incidental UI behavior.

### 4.13 Scout is the primary relationship; hired agents are coworkers, not a bot swarm

Scout is the only general-purpose social agent and the default agent allowed to answer in `#team`. It is the familiar coordinator across the main screen, private chat, meetings, shared channels, and the agent workforce. Hiring an agent adds a teammate to the organization roster; it does not silently subscribe that agent to `#team`, every project, every meeting, or the company brain. A specialist does not lurk in `#team`, respond because Scout mentioned it, or enter a conversation merely because its model would be useful.

Specialists are first-class named agent principals with stable profiles, avatars, roles, visible memberships, and audit trails. They appear only when a human explicitly addresses an already-present specialist, Scout routes approved work into a bound project/work thread, or an eligible human confirms the live meeting invitation in §4.17. Scout introduces the handoff in plain language, the specialist contributes under its own identity, and Scout remains accountable for coordination and completion.

Do not personify every background worker. Transcript projection, entity resolution, indexing, criticism, verification, and safety gates remain system roles unless a recurring human relationship benefits from a distinct visible coworker. The company-historian capability initially remains authoritative retrieval behind Scout, not a second personality with a competing memory. Scout remains the sole default voice agent and meeting chair. A named specialist may receive temporary meeting-floor access only through the explicit consultation contract in §4.17; normal specialist participation remains in bound text/project threads. The first production specialist is the human-approved named Insights analyst used by `insights_opportunities_v1`; Marketing, Research, Design, and Builder profiles may follow for explicit consultation or approved project work only after their own capability and identity-continuity gates.

### 4.14 Personality is governed identity plus evidence-backed relationship memory

Each coworker has six deliberately separate layers:

1. a portable, immutable `AgentPackageManifest` that supplies a publisher-authored role/persona seed, assets, requested capabilities, compatibility, and evaluation bundle, but no organization memory, credential, or authority;
2. an organization-owned, versioned `AgentCoreProfile` plus `AgentProfileOverlay` for the hired teammate's stable name, role, mission, voice, distinctive traits, humor range, values, boundaries, local instructions, and escalation behavior;
3. an `AgentCapabilityManifest` for allowed surfaces, memberships, tools, workflow roles, route policy, budgets, data classes, and authority classes;
4. `AgentAssignment` and `ChannelNormProfile` records for responsibilities, projects, channels, direct-chat behavior, response policy, and local norms;
5. evidence-backed `AgentRelationshipMemory` and `CollaborationPreference` records for corrigible, scoped ways of working with people and teams;
6. `AgentLearningRecord` and `AgentPerformanceReceipt` records for source-linked domain lessons, accepted/rejected work, feedback, demonstrated competencies, and eval-backed growth.

The company brain remains the shared source of organizational truth; STRIDE does not fork a divergent private company brain into every agent. At runtime, each agent receives an ACL-filtered context envelope plus its own scoped relationship/learning records. Learned memory cannot rewrite the core persona, safety rules, tool authority, or approval policy. Personality edits are reviewed organization-local profile revisions; capability changes require server validation and evals. The model may vary behind a stable coworker identity only after continuity and safety evaluation, with the runtime route retained in the audit receipt. A package update never overwrites the local overlay, memory, assignments, permissions, or work history. STRIDE never pretends that a newly swapped model is a new person or that a model's self-description grants capabilities.

### 4.15 `#team` supports contextual rich actions without permission shortcuts

It is feasible and desirable for Scout to respond conversationally with text, one appropriate GIF, or an authorized file/artifact card. An explicit, lexically valid `@scout` invocation authorizes one ordinary reply under channel policy; it does not authorize a permission change, company-memory write beyond the normal posted message, or a work launch.

For a requested file, STRIDE searches only an ACL-filtered inventory, resolves an immutable source object and content revision, intersects source and destination audiences, reauthorizes the requester and every recipient, and posts a durable reference with provenance. If the file is not already visible to everyone in `#team`, Scout offers a private result or a separate explicit share proposal; it never silently widens access. Before this feature ships, current attachment ingestion must stop accepting a client-supplied blob reference on existence alone and require source-object authorization.

For a GIF, Scout may automatically post at most one G-rated result after an explicit `@scout` invocation when the context is low-risk and the channel policy permits it. The server converts context into a short abstract search intent so raw private or meeting text is never sent to the GIF provider. Provider requests contain no email hash, account identifier, stable user-derived pseudonym, source message ID, thread ID, or company identifier. Sensitive, high-stakes, factual, grief, health, legal, financial, security, personnel, conflict, or harassment contexts fall back to text. Humor is situational or self-deprecating, never targeted humiliation. Provider ID, query class, source, alt text, selection reason, and agent/profile version are recorded; decorative media is excluded from asserted company-memory projections.

The selected media is fetched and validated server-side into an immutable, content-addressed, authorized STRIDE blob and served from STRIDE so recipients do not make tracking requests to the provider. The provider page is an optional explicit link, never an embedded load. Agent GIFs remain disabled unless provider licensing and attribution terms permit this relay/cache contract; otherwise STRIDE selects a privacy-compatible licensed provider or catalog rather than weakening the boundary.

### 4.16 Buzz is a design reference, not STRIDE's authority or memory layer

The audited Buzz design usefully separates portable persona metadata from minted runtime identity, supports stable agent authorship, groups personas into teams, and retains catalog-source provenance when copying a shared persona. Its persona schema separates display/role material from skills, runtime/model, triggers, and subscriptions; its managed-agent definition distinguishes a discoverable definition from the locally minted runtime identity; and its team record groups versioned persona IDs. STRIDE adopts these concepts as `AgentPackageManifest`, `MarketplaceListing`, organization-owned `TeamAgent`, and revocable runtime-principal records. See Buzz's [persona schema](https://github.com/block/buzz/blob/90e058ebf68137e048a409aec6616519379ff726/crates/buzz-persona/src/persona.rs#L95-L198), [catalog provenance and runtime identity separation](https://github.com/block/buzz/blob/90e058ebf68137e048a409aec6616519379ff726/desktop/src-tauri/src/managed_agents/types.rs#L15-L156), and [team record](https://github.com/block/buzz/blob/90e058ebf68137e048a409aec6616519379ff726/desktop/src-tauri/src/managed_agents/types.rs#L738-L782).

STRIDE explicitly rejects Buzz's `bypass-permissions` default, automatic `allow_once` tool permission, broad credential/environment inheritance, package-selected executable hooks or MCP commands, prompt-only sibling delegation, model-self-fetched channel history, and agent-authored encrypted memory as company truth. Buzz itself documents unresolved memory-poisoning/key-compromise risk in [NIP-AE](https://github.com/block/buzz/blob/90e058ebf68137e048a409aec6616519379ff726/docs/nips/NIP-AE.md#L152-L163). STRIDE keeps identity, ACL/context assembly, memory, capability mapping, capability tokens, approval, budgets, artifacts, and verification server-owned. A package may request abstract capabilities; an administrator maps only approved requests to STRIDE-native tools after security and eval gates. A specialist runtime receives a short-lived, least-privilege capability token for one bounded run; no transitive delegation exists unless the immutable workflow profile explicitly allows it.

### 4.17 Scout may invite one time-bounded specialist into an internal meeting

The target experience includes Scout bringing a named specialist such as **Mary · Marketing Agent** into a live call for a bounded consultation. This is a conversational capability, not a second recurring workflow product and not permission for a bot swarm. The registry may contain multiple separately gated specialists, but the first release permits Scout plus at most one specialist agent in a member-only sitting. Guest-safe sittings keep specialist voice disabled until a later consent, disclosure, and audience-isolation gate proves that mode separately.

A spoken request such as “Scout, bring Mary in to help with positioning” creates an invitation request; Scout may also suggest “Mary could help with this” as a quiet confirmation card. Neither speech nor Scout's suggestion widens the meeting audience or starts a paid specialist session. STRIDE shows the specialist, purpose, context classes to be shared, expected limit, and current meeting audience. An eligible authenticated human confirms **Invite**. Every participant then sees and hears an unambiguous agent-joined disclosure and a persistent **Mary · Marketing Agent** participant state. A room policy may pre-approve a named specialist and bounded context class later, but the policy remains visible, revocable, and version-bound.

STRIDE—not Scout's model—assembles a `MeetingSpecialistContextEnvelope` containing the exact invitation purpose, authorized recent transcript window, relevant versioned meeting analysis, authorized company/project evidence, active approved work state, source references, coverage and freshness, audience/consent/retention decisions, profile/runtime revisions, and turn/time/tool/cost limits. Prefer authoritative transcript plus structured analysis and the minimum useful company context. Historical raw audio is not forwarded to the specialist by default. Missing, stale, or audience-incompatible context is omitted or blocks the invitation; the model cannot self-fetch around the envelope.

Mary runs in a separate backend `gpt-realtime-2.1` session over the server-to-server WebSocket path documented by OpenAI's [Realtime WebSocket guide](https://developers.openai.com/api/docs/guides/realtime-websocket). The backend owns the API key, session lifecycle, JSON events, audio chunks, tools, and usage. STRIDE publishes Mary into Pion as a distinct verified agent participant and audio track; it does not create a second hidden browser/WebRTC client. OpenAI documents speech-to-speech, configurable reasoning, interruption behavior, and tool use for [GPT-Realtime-2.1](https://developers.openai.com/api/docs/models/gpt-realtime-2.1), but those model capabilities remain downstream of STRIDE's authority and floor controller.

A server-side `MeetingAgentFloorController` is the only route between room media and agent sessions. Humans always win barge-in. Only one agent audio track may speak at a time. Mary receives Scout's structured handoff and eligible human turns, not Scout's synthesized audio; Scout receives Mary's attributed transcript/result, not a looped microphone feed. After Mary joins, the controller may stream the consented human-only live mix for low-latency prosody while sending canonical speaker-attribution metadata at committed-turn boundaries. It excludes every synthetic-agent track, private dictation, non-consenting track, and stale media generation, and it never duplicates one turn as competing audio and text inputs. The controller forbids autonomous Scout↔Mary ping-pong, caps agent turns, wall time, tools, audio, tokens, and cost, and requires a human turn or explicit chair action before another agent turn. Deeper specialist reasoning may run as a bounded server-side text stage and return into Mary's session, but it cannot delay, delegate, or act beyond the invitation manifest.

The lifecycle is `requested -> approved -> joining -> briefed/listening -> speaking -> available -> dismissed|expired|failed`. “Thanks Mary,” **Remove**, consent withdrawal, membership change, budget exhaustion, timeout, or kill switch immediately closes her input/context/tool capability, cancels pending output, ends metering after the terminal provider event, removes the audio track, and posts **Mary left**. Scout's spoken thanks is paired with an allowlisted `dismiss_meeting_specialist` function call bound to the active session; the server does not scrape arbitrary generated phrases for authority. The current sentence may end only within a short configured grace period; privacy or consent revocation cuts immediately. Scout continues the meeting honestly if Mary is unavailable or fails.

Mary's spoken contribution becomes an agent-authored `ConversationEvent` and `MeetingAgentContribution` with exact profile, runtime, model, context digest, source range, audience, timestamps, and coverage. It enters meeting analysis and the company brain only through the same permission-preserving projection lane as other conversation evidence. It is never approval or durable authority. If the consultation reveals real work, Scout may create ordinary Suggested Work; the existing human approval, destination, WorkRun, artifact, and verification contracts still apply.

### 4.18 The Agent Marketplace hires organization-owned teammates; it does not install autonomous vendors

STRIDE ships an **Agent Marketplace** as the user-facing place to discover and hire coworkers. The current plan covers a curated internal marketplace of STRIDE-authored and organization-authored agents. Its purpose is a legible, expandable team—not third-party commerce. A later public marketplace may let publishers sell packages to other organizations, but payments, royalties, rankings, reviews, moderation, publisher telemetry, legal/tax handling, and arbitrary third-party execution are explicitly outside E0–E10.

The architecture separates three objects:

1. `AgentPackageManifest` is a portable, immutable, content-addressed definition: publisher/provenance and signature, semantic version, role/persona seed, assets, requested abstract capabilities, compatible runtimes/models, voice options, data classifications, required evaluation bundle, license metadata, update lineage, and dependency/SBOM refs. It contains no credential, company memory, relationship history, local assignment, or unrestricted executable hook.
2. `MarketplaceListing` is curated product metadata over one verified package revision: role/outcomes, personality preview, example results, required access, surfaces, expected cost band, quality/safety evidence, publisher, compatibility, update policy, and availability. A listing is discoverability, never authority.
3. `TeamAgent` is the organization-owned coworker created by **Hire to team**. It has a stable local identity, profile overlay, assignments, memberships, capability manifest, budgets, runtime route, direct thread, relationship/learning records, activity, performance evidence, and tenure state. The package publisher cannot read or mutate it.

The initial marketplace is deliberately curated: the Insights analyst becomes the first verified listing after E7; Marketing (Mary is the working example), Research, Design, and Builder packages are admitted one at a time in E8. An organization may later create a private package from an approved template, but package authoring uses the same validation/eval path. Hiring a personality does not manufacture competence: every advertised capability must map to an implemented STRIDE tool/workflow role and pass its own evidence gate.

Scout provides the **chief-of-staff experience** over this workforce. It understands the roster, responsibilities, current assignments, availability, budgets, permissions, performance receipts, and collaboration history. Scout may answer “who should help?”, recommend a marketplace agent with reasons and tradeoffs, draft the hire configuration, route approved work to the best eligible teammate, introduce that teammate in a thread or call, monitor progress, surface blockers, suggest coaching/profile adjustments, and recommend pause or offboarding. The server chooses by capability, evidence, workload, authority, destination, and cost—not by which personality makes the most persuasive claim. Scout cannot confirm a hire, expand access, activate a material update, or permanently offboard an agent.

“Learning and growing” has three bounded forms:

- **relationship growth:** evidence-backed preferences and working patterns scoped to a person, team, project, or channel, with inspect/correct/forget/expiry controls;
- **domain growth:** source-linked lessons, vocabulary, examples, accepted/rejected outputs, and project context that remain projections over the company brain rather than a private unsourced lore file;
- **competency growth:** eval-backed performance receipts can unlock a human-approved capability-manifest revision, larger assignment class, or different route/budget. A model's confidence or self-description cannot do so.

People can open any hired coworker and inspect **About**, **Responsibilities**, **Access**, **Memory**, **Skills**, **Feedback & Growth**, **Activity**, and **Cost**. Reversible identity/tone/local-instruction edits create an `AgentProfileOverlay` draft and continuity preview. Membership, tool, data, budget, proactivity, voice, or workflow changes create a separate permission/capability proposal. Core-package and local-overlay diffs remain distinct so a package update never erases the relationship the organization has developed with the teammate.

Package updates are opt-in. STRIDE verifies the new digest, provenance, compatibility, capability/permission diff, prompt/personality diff, evaluation bundle, cost, and migration plan in a quarantined trial. No update auto-activates; any new data/tool/side-effect request requires fresh human approval. Rollback restores the prior package/runtime/profile revision while preserving compatible local memory and history. Incompatible memory remains readable but is not injected until migrated and reviewed.

Pausing revokes new assignments and runtime tokens while preserving history. Offboarding cancels or fences active work, revokes memberships/tools/context immediately, removes meeting eligibility, leaves historical messages/artifacts attributable, and offers policy-bound local export or purge. Exported packages omit company context, credentials, relationship memory, performance evidence derived from private work, and organization-specific overlays by default. A future public seller receives no usage, conversation, memory, or feedback data unless the organization later approves an explicit redacted telemetry/export policy.

If an organization eventually publishes one of its agents, STRIDE creates a new reviewed publisher package from explicitly selected portable persona/capability material; it never publishes the live TeamAgent instance. Generic improvements may enter that package only through a rights-checked, de-identified, human-reviewed promotion with provenance and new evals. Client/project memory, relationship history, private examples, work artifacts, and learned company facts never become marketplace inventory merely because they improved the local coworker.

---

## 5. Target system

```text
All text composers -> bounded mic clip -> gpt-transcribe -> final text -> idempotent message send

Web/mobile calls ------> room actor / mixer -----> segmenter + consent fence
       |                                            |             |
       |                                            |             +-> optional gpt-live-transcribe -> provisional UI
       |                                            +-> gpt-transcribe -> authoritative transcript ledger
       |
       +-> meeting text chat ----+
                                 |
#team/project channels ----------+-> ConversationEvent ledger -> typed projections -> company brain
       |                                                            |
       |                                                            +-> links/files/artifacts
       |                                                            +-> decisions/commitments/blockers
       |                                                            +-> storylines/alignment/positions
       |                                                            +-> collaboration preferences
       |                                                            +-> WorkIntent candidates
       +-> author/reply/reaction context -> AgentContextEnvelope -> Scout text/GIF/file card

Scout invocation -> server context broker -> ACL-first temporal/hybrid retrieval -> AnswerEnvelope
       |                                                                  |
       +-------------------- gpt-realtime-2.1 speaks <---------------------+

approved specialist invite -> MeetingSpecialistContextEnvelope -> separate backend Realtime session
       |                                                              |
       +-> MeetingAgentFloorController -> distinct agent audio track --+
                 | one agent speaks; humans interrupt; no agent audio loop

verified AgentPackageManifest -> curated MarketplaceListing -> human Hire to team
       |                                                        |
       +-> package quarantine/evals -> organization TeamAgent --+-> profile + capabilities + assignments
                                                                    | relationship/learning evidence
                                                                    +-> Scout workforce coordinator

WorkIntent -> WorkProposal -> approval -> WorkRun -> named specialist + bounded stages/critic
                                                       |
                                                       +-> Outcome + artifacts -> bound project thread
```

State ownership is explicit:

| State | Owner |
|---|---|
| raw authorized conversation event | canonical event ledger |
| transcript revision/order/speaker/time | transcript ledger |
| rolling meeting analysis | projection workers |
| company assertions/storylines | company-brain projections |
| personal/team collaboration preference | permissioned profile store |
| stable coworker identity/personality | versioned `AgentCoreProfile` registry |
| coworker tools/routes/memberships | server-validated `AgentCapabilityManifest` registry |
| portable agent definition and provenance | immutable `AgentPackageManifest` registry |
| curated discoverability | versioned `MarketplaceListing` catalog; never runtime authority |
| organization-owned hired coworker | `TeamAgent` roster plus local profile overlay, assignments, budgets, and status |
| channel social norms | versioned `ChannelNormProfile` registry |
| agent relationship observations | permissioned evidence-backed relationship-memory store |
| agent domain/competency growth | source-linked learning and performance-receipt store |
| foreground microphone mode/generation | client `AudioFocusCoordinator` |
| Realtime audio turn | Realtime session, ephemeral |
| meeting specialist invitation/session/floor | STRIDE meeting-agent controller and canonical invitation ledger |
| specialist meeting brief | audience-authorized versioned context-envelope store |
| specialist spoken contribution | canonical agent-authored conversation event plus meeting-contribution record |
| suggestion/approval/run | STRIDE workflow control plane |
| artifact/result | artifact store plus bound thread reference |
| model route/cost/quality evidence | versioned seat registry and usage/eval ledger |

---

## 6. User experience contracts

### 6.1 Main Scout screen

- The composer microphone starts bounded Dictate. A visually distinct Live Voice control starts the full-duplex Realtime conversation.
- On the home screen, the large central Scout waveform is the Live Voice control; the microphone inside the bottom composer is Dictate. If the resulting destination is Scout, the final transcript is still an ordinary text message to Scout, not a Realtime voice turn.
- If Live Voice is active, tapping Dictate visibly hangs it up before the waveform begins. Joining a video room performs the same handoff.
- Scout can navigate, answer, draft, retrieve, and propose work.
- Navigation happens immediately without a spoken essay.
- Messages, external writes, and work launches show a draft/proposal and wait for confirmation.
- Every destination remains reachable in at most two taps when voice, network, or model access fails.

### 6.2 Composer dictation across the app

The main Scout composer, private Scout threads, `#team`, project threads, meeting text chat, and every other first-party message composer share one dictation component and lifecycle:

```text
idle -> recording
recording -> ready_to_submit
recording/ready_to_submit -> deleted
recording/ready_to_submit -> submitted -> transcribing -> sending -> sent
transcribing -> cancelled
transcribing/sending -> failed -> retry | editable_draft | discard
```

- Tapping the microphone begins immediately after audio focus is acquired without navigating away from the current conversation. The existing composer rectangle becomes a waveform whose motion reflects actual microphone level, with a left **X/Delete**, a Stop control, a right Send arrow, and a reduced-motion equivalent.
- Stop ends capture and holds the clip locally in `ready_to_submit`; it does not call the transcription model. Delete discards the clip. Tapping Send while recording implicitly stops and submits; tapping Send after Stop submits the held clip.
- Submission commits one bounded clip to `gpt-transcribe`. The same rectangle replaces the waveform with the literal status **Transcribing** plus a compact progress animation. Partial text is never posted.
- The left X remains available while **Transcribing**. Cancelling advances the dictation generation, suppresses any late provider completion, and guarantees that no message is posted. Other recording/send controls are disabled until the transcription reaches a terminal state.
- A successful, non-empty final transcript is posted directly into the current chat through the same authenticated, ACL-checked, idempotent message API as typed text. Dictation never bypasses mention parsing, thread membership, rate limits, or workflow approval.
- Delete, empty audio, low-integrity output, permission loss, or terminal transcription failure never sends. If useful text exists after a recoverable failure, preserve it as an editable draft with clear Retry, Send, and Discard choices.
- A local encrypted retry record may retain the bounded clip only until success, cancellation, expiry, or explicit discard. Exactly-once client message IDs prevent duplicate sends after reconnect/retry.
- Company terms, people, projects, acronyms, and likely languages are supplied as bounded hints. Any optional text cleanup must be audio-grounded, evaluation-proven, and limited to spelling, punctuation, and capitalization; it cannot paraphrase or add content.

### 6.3 `#team`

- Humans converse without Scout replying unless tagged or explicitly invoked.
- Mentions use the same lexical/token-boundary parser on clients and server: `@scout` invokes; strings such as `foo@scoutx`, quoted old mentions, file metadata, and GIF text do not.
- Scout receives author-attributed recent turns, reply ancestry, reactions, file/link/GIF metadata, participant roles, channel norms, and ACL-filtered relevant history. Human turns are never flattened into one anonymous `user` role.
- Scout answers as a concise teammate, not a dashboard or report, and may use the company’s normal tone and vocabulary without impersonating a person.
- Answers carry source chips to exact messages, meetings, artifacts, or links.
- Catch-up is evidence-linked and clearly distinguishes quoted/extractive facts from synthesis.
- The deposit rail exposes links, files, decisions, and completed work produced by the conversation.
- “`@scout drop the latest Dog Perfect brief`” posts the exact authorized file revision when every current recipient already has access. Otherwise Scout offers a private result or an explicit share action without revealing unauthorized metadata.
- A low-risk social invocation may receive concise text, text plus one GIF, or one GIF where it is clearly the better conversational answer. The channel can disable agent GIFs; sensitive/high-stakes contexts never receive them; every GIF has alt text and provider provenance.
- Scout is the only default social agent in `#team`. Specialists can be addressed only if explicitly added as channel members; normal specialist work lives in the bound project/work thread.
- Edits/deletions update or retract recall. Scout answers already posted remain historical messages but their source chips show superseded/retracted state.
- Native push, unread boundary, reactions, attachment rendering, link previews, mute/previews controls, and append-not-refetch performance are release requirements.

### 6.3a Desktop chat quality contract

Desktop chat is not a stretched mobile transcript. It uses the available canvas to create a calm work surface while keeping the conversation primary:

- a persistent left thread/channel rail carries identity, unread state, search, pinned `#team`, private chats, and project homes; the active conversation has a clear channel header and participants/policy state;
- the message column remains comfortably readable rather than spanning the viewport. At wider breakpoints, an optional contextual rail shows the current reply thread, pinned sources, files, work status, or channel details; it never competes with the conversation and collapses before the message column becomes cramped;
- authorship groups, timestamps, unread boundary, replies, reactions, edits, delivery/degraded state, Scout/specialist identity, and provenance are visually legible at a glance without permanent control clutter. Hover reveals desktop actions; keyboard and touch equivalents remain complete;
- the sticky composer is visually anchored, preserves multiline drafting and dictation/attachment states, and never jumps when rich media resolves. Sending feels immediate through optimistic append plus server acknowledgement and exact rollback on rejection;
- photos use bounded responsive galleries and lightbox/open actions; GIFs preserve aspect ratio and alt text; links use title/domain/description/thumbnail cards; files and PDFs show type, size, revision, source, and open/download actions; agent results use compact artifact cards with status and provenance. Failed, revoked, unsafe, loading, and unsupported previews have deliberate non-leaking fallbacks;
- at 1024 px the contextual rail becomes a drawer; at 1280 px the navigation and conversation remain simultaneously comfortable; at 1440/1728 px the optional context rail may appear without widening message measure. Browser zoom and reduced motion retain the same hierarchy;
- shared APIs and design tokens may be reused, but desktop DOM/layout/interaction changes are isolated from Expo rendering. The current iPhone/iPad chat experience is frozen before E4 and any mobile visual, gesture, haptic, send, attachment, scroll, unread, or deep-link regression blocks release.

“Spectacular” is judged from rendered ordinary navigation with real short, long, empty, media-heavy, private, public, and project conversations—not from a component gallery or source inspection.

### 6.4 Meetings

- Recording/listening state reflects the real capture path and consent state.
- Passive transcription and analysis run only for authorized participants/tracks/scopes.
- Saying “Scout …” or tapping Ask Scout engages it; ordinary human speech never receives an unsolicited answer.
- The engaged state is visible, supports natural follow-ups and barge-in, and times out or can be dismissed.
- Spoken answers are short. Rich evidence, links, and artifacts appear in meeting chat.
- Proactive detected work appears as a quiet card for relevant authorized people.
- In an internal member-only sitting, an eligible person may ask Scout to invite one registered specialist for one stated purpose. The confirmation names the agent and shows the transcript/analysis/project-context classes STRIDE will share before a paid session begins.
- Scout introduces the specialist from a server-built brief. The roster and active-speaker state clearly distinguish humans, Scout, and the specialist; participant details expose the agent profile and why it is present.
- Humans may interrupt either agent. Scout chairs the exchange, and a human or Scout can say “thanks Mary,” tap **Remove**, or disable specialists for the room. The specialist stops listening, speaking, using tools, and accruing usage after teardown; the meeting itself continues.
- A specialist idea is attributed conversation evidence. It may inform analysis or Suggested Work, but it does not launch work, publish, or change company truth without the existing verification and human-approval paths.

### 6.5 Five-minute, thirty-minute, and late-join recall

For “what happened in the last five minutes?” STRIDE resolves an exact source-time interval, retrieves authoritative finalized turns, overlays rolling analysis that covers the same interval, and falls back to raw transcript synthesis when analysis lags.

For “catch me up on what I missed,” the interval is `[sitting_start, requester_first_admission)` and includes decisions, commitments, blockers, the active topic, open questions, and useful artifacts. Every answer returns transcript-through, analysis-through, gaps, and evidence references.

### 6.6 Cross-surface retrieval example

For “Scout, what was that link Erick shared in the group chat last week?” the server:

1. parses `author=Erick`, `artifact_type=link`, `source=#team`, and a timezone-resolved time range;
2. ACL-filters before search;
3. structured-filters author/type/channel/time before lexical/semantic reranking;
4. returns the exact source message, URL, surrounding context, and confidence;
5. reauthorizes the current meeting audience before publication;
6. speaks a short confirmation and posts a rich card in meeting chat.

If any current listener lacks access, Scout does not speak or broadcast the result. It offers or sends a private response only to authorized requesters.

### 6.7 Suggested Work

A visible card says, in plain language:

> **Suggested Work** — Prepare an Insights & Opportunities report from this discussion and the Dog Perfect project history. Run it in **Dog Perfect** and report back here? **Review details** · **Approve** · **Dismiss**

Review details exposes source evidence, why STRIDE suggested it, intended outcome, destination, requested authority, expected cost/time band, owner/reviewer, and what will not happen. Approval is never implicit in speech, attendance, a reaction, or silence.

### 6.8 Work status and completion

Users see a small stable vocabulary:

- Suggested
- Approved
- Queued
- Working
- Needs you
- Under review
- Blocked
- Complete
- Failed
- Cancelled

Completion posts into the bound project/chat thread with a concise result, artifact cards, verification evidence, unresolved caveats, and next optional actions. It also updates the company brain through permission-preserving result events.

### 6.9 Named coworker handoff

When approved work needs a specialist, Scout says who it is bringing in and why. The destination thread shows the specialist’s stable name, role, avatar, verified agent badge, current status, and the exact WorkRun it is acting for. The specialist can ask bounded questions, post progress, and return artifacts there under its own identity. It cannot follow participants into unrelated channels, browse broader company history, summon another agent, or exceed the run’s evidence/tool/budget envelope.

The first launch roster is deliberately small:

- **Scout** — primary conversational coworker, historian/retrieval interface, coordinator, and result narrator;
- **one human-approved named Insights analyst** — first visible specialist for `insights_opportunities_v1`, enabled only after its core profile and capability eval pass;
- **Marketing, Research, Design, and Builder profiles** — provisioned in the registry, but enabled one at a time for explicit consultation or approved project-thread work after separate quality, authority, cost, voice where applicable, and personality-continuity evidence.

Names and aesthetic details are reversible brand configuration approved before each profile is enabled; stable machine IDs, authority, evidence, and audit contracts are not. Invisible critics, verifiers, transcript processors, and safety gates are disclosed in run details but are not presented as social coworkers.

### 6.10 Live specialist consultation

The minimum successful script is:

1. A member says, “Scout, bring Mary in to help with the campaign angle.”
2. Scout replies, “I can invite Mary, our marketing specialist, and share this meeting's discussion plus the approved Dog Perfect context. Invite her?” The UI shows **Invite** and **Not now**.
3. After eligible human confirmation, the roster shows **Mary · Marketing Agent · joining**, every participant receives the agent disclosure, and Scout gives Mary a short source-grounded handoff.
4. Mary joins as a distinct voice, acknowledges the purpose, listens only to eligible live human turns, and offers bounded ideas. Evidence cards may appear in meeting chat.
5. Scout or a human says, “Thanks Mary, I'll get back to you later,” or taps **Remove**. Mary stops, the roster shows **Mary left**, and her attributed contribution remains available under the meeting's normal retention and source rules.
6. If the exchange reveals a real outcome, Scout offers ordinary Suggested Work to the relevant people. No work starts from Mary's idea, Scout's thanks, attendance, silence, or a spoken “sounds good.”

The first release exposes no multi-agent workflow terminology, model selector, or floor-control dashboard. Users see a coworker joining, why she is there, what she can access, whether she is listening/speaking, and how to remove her.

### 6.11 Agent Marketplace, team roster, and coworker management

The Marketplace is outcome-first: **Marketing**, **Research**, **Design**, **Build**, **Insights**, and future categories—not a wall of model IDs. Each card shows the agent's name/role, short personality preview, what it reliably does, sample result, verified capabilities, surfaces, required access, expected cost band, current package version/publisher, and whether voice is qualified. **Meet**, **View evidence**, and **Hire to team** are distinct actions; a personality demo is not a capability receipt.

**Hire to team** opens a revisioned configuration:

- local name/avatar/voice and personality overlay;
- mission and responsibilities;
- initial projects, direct-chat availability, and optional explicit channel memberships;
- data classes, tools, workflow roles, voice/meeting eligibility, and side-effect limits;
- who can message, assign, invite, edit, approve, pause, or offboard;
- per-run/daily/monthly budgets, concurrency, proactive behavior, and escalation owner;
- package update policy, memory policy, retention, and trial duration.

The confirmation summarizes every permission and expected cost. **Try on a sample** runs against synthetic or explicitly selected authorized evidence with no side-effecting tools. **Hire** creates one idempotent `TeamAgent`, its private direct thread, and a visible roster entry. It does not add the agent to `#team` or existing projects. The agent introduces itself in its direct thread and explains its role, access, boundaries, and how to correct what it remembers.

The team roster mixes humans and agents without disguising either. Agent rows show verified-agent badge, role, current status, assignments, home projects/channels, last meaningful result, health, current package/profile/runtime revision, spend versus budget, and any **Needs you** state. A hired agent may have a direct conversation, join an explicitly authorized project thread, receive approved WorkRuns, or become eligible for the §6.10 meeting invitation after its voice gate. Direct agent threads follow the same private-by-default rule as private Scout chat; their contents enter project/company memory only through an explicit authorized share/save or an already-approved work result posted into its bound project thread.

Scout's workforce view answers naturally: “Who is handling Dog Perfect?”, “Why did you choose Mary?”, “What is blocked?”, “Which agent is over budget?”, and “What has the research team learned?” Scout may show a proposed assignment, hire, profile adjustment, capability change, update, pause, or offboarding card. Human confirmation remains visually distinct from conversational acknowledgement.

The coworker detail surface provides **About**, **Responsibilities**, **Access**, **Memory**, **Skills**, **Feedback & Growth**, **Activity**, **Cost**, and **Versions**. Users can inspect sources behind learned preferences/lessons, correct or forget them, compare package versus local personality, preview a draft change, run continuity/capability tests, and roll back. Permission-bearing fields never hide inside a personality editor.

Updates show a semantic diff: identity/personality, requested data/tools, model/runtime, cost, skills/evals, migrations, and publisher provenance. STRIDE may recommend an update but never auto-activates it. Pause is immediate and reversible. Offboarding shows active work, memberships, artifacts, retained history, export/purge consequences, then requires eligible human confirmation.

---

## 7. Canonical contracts

### 7.1 `ConversationEvent`

Required fields:

- `event_id`, `tenant_id`, `source_type`, `source_id`, `room_id`, `sitting_id`, `thread_id`;
- `author_principal`, `author_name`, `occurred_at`, `ingested_at`;
- `event_type`: message, edit, delete, reply, reaction, file, link, transcript turn, consent change, agent-session status, agent contribution;
- `content_revision`, `content_digest`, `supersedes_event_id`, `reply_to_event_id`;
- `audience`, `visibility`, `acl_version`, `retention_policy`, `purge_generation`;
- structured attachment/link refs and body pointer;
- provenance: client/server/tool, on-behalf-of disclosure, provider IDs where applicable.

The immutable event contains only schema-approved fields. Secrets, provider tokens, private capability URLs, and unbounded user bodies do not enter immutable operational audit payloads.

### 7.2 `TranscriptSegment` and `TranscriptRevision`

STRIDE mints `segment_id` before provider submission. It binds provider `item_id` on `input_audio_buffer.committed` and resolves every completion/failure by ID, never FIFO.

Required fields include source start/end, capture sequence, room/sitting/media generation, speaker attribution and attribution source, consent scopes, model/config/context digest, language hints, status, revision, supersession, and timestamps.

State:

```text
capturing -> live_partial -> live_final
          -> authoritative_final | degraded_final | failed
authoritative_final -> corrected | superseded | retracted
```

Only the latest authorized `authoritative_final` revision feeds asserted analysis. `degraded_final` may support a visibly qualified answer and later reconciliation.

### 7.3 `AnalysisProjection`

Typed kinds:

- decision;
- commitment/owner/date;
- blocker;
- active storyline;
- alignment/divergence/position;
- open question;
- entity/project/topic;
- link/file/artifact;
- vocabulary/alias;
- work-intent candidate.

Every projection carries source event/segment refs, exact `window_start`, `window_end`, `through_segment_id`, source and projection high-water, model/prompt/schema version, evidence digest, confidence, visibility intersection, supersession, and freshness.

### 7.4 `KnowledgeAssertion`

States are `asserted`, `inferred`, `unsupported`, `superseded`, and `retracted`. Only asserted facts may be presented without qualification. All asserted claims resolve to at least one authorized primary evidence edge.

### 7.5 `CollaborationPreference`

Fields include subject, scope, preference type, value, explicit/inferred origin, source refs, confidence, first/last observed, reinforcement count, expiry, visibility, status, and correction history.

Explicit preferences outrank inferences. Sensitive categories are denied at schema validation. Users can inspect, correct, expire, or forget a preference.

### 7.6 Work contracts

`WorkIntent` is internal detection state: outcome hypothesis, sources, relevant people/projects, confidence, counterevidence, and status.

`WorkProposal` is the human trust object: immutable revision, evidence snapshot, outcome, workflow profile, destination, participants, owner/reviewer, authority, budget/time estimate, generated-artifact policy, expiry, and approval requirements.

`ApprovalPolicy` is a versioned authority contract bound into the proposal. It declares eligible principals and roles, quorum, source-read and destination-write requirements, separation-of-duties rules, permitted side-effect class, expiry, and invalidation conditions. The initial policy matrix is:

| Authority class | Required human approval |
|---|---|
| internal read-only analysis or draft in an existing thread | one named requester or project owner who can currently read every source and write the destination |
| creation of a new internal project/thread | one organization member with project-creation authority; destination membership is shown and created explicitly |
| internal artifact write/revision in an existing project | one named project owner with current write authority; acceptance review remains a separate workflow stage |
| external publication/message, production/deploy, financial, purchase, credential, or destructive action | a separate action-specific proposal; privileged action owner plus a second eligible approver, with the executor ineligible to satisfy either approval |

Only authenticated humans can approve; Scout, detector, orchestrator, worker, reviewer model, attendance, reaction, silence, or speech transcription cannot satisfy quorum. STRIDE reauthorizes the policy at proposal display, atomic approval consumption, every side-effecting stage, artifact access, and publication. Source ACL, consent, role, membership, destination, or policy changes invalidate an unconsumed approval and pause an active run before its next protected action.

`WorkRun` is the durable execution object: consumed proposal revision, idempotency key, stage graph, pinned model route per stage, checkpoints, attempts, usage, status, artifacts, critic verdicts, approvals, and terminal evidence.

`OutcomeRecord` binds the verified result, accepted/rejected criteria, artifact revisions, destination thread, completion time, reviewer, and remaining caveats.

Work state:

```text
candidate -> proposed -> approved -> queued -> running
candidate/proposed -> dismissed | expired | superseded
running -> awaiting_input | awaiting_review | blocked
running/awaiting_review -> completed | failed | cancelled
```

### 7.7 Answer/context envelopes

`MeetingAnswerEnvelope` and `CompanyAnswerEnvelope` contain:

- answer text suitable for speech;
- evidence refs and source time spans;
- requested/resolved temporal scope and timezone;
- transcript, analysis, and brain high-waters;
- coverage: complete, partial, unavailable;
- current provisional/gap information;
- relevant links/artifacts and publication instructions;
- audience authorization result.

Realtime receives this envelope; it does not independently search raw memory.

All route registry entries also declare runtime capability: single completion, tool loop, parallel agent eligibility, vision/audio support, strict schema, streaming, resumability, side-effect class, and supported reasoning range. Anthropic tool loops, OpenAI Responses stages, Realtime, and Codex queues are not treated as interchangeable merely because they all select a model.

### 7.8 Coworker identity, context, delegation, and rich-message contracts

`AgentCoreProfile` is human-authored and versioned: stable agent ID, display name, pronunciation, avatar, role, mission, voice/style, stable traits, humor range, values, boundaries, prohibited behavior, escalation policy, owner, status, and revision. It contains no provider credential and grants no tool authority.

`AgentCapabilityManifest` is server-validated: agent/profile revision, allowed surfaces and channel memberships, tool and workflow roles, model/runtime policy, memory scopes, data classifications, side-effect classes, per-call/run budgets, delegation/hop rules, concurrency, expiry, and kill switches. Runtime principals and short-lived capability tokens are minted from this manifest and are independently revocable.

`ChannelNormProfile` defines channel purpose, audience, memory/work-sensing disclosure, expected tone, response length, humor/GIF policy, proactive-assistance policy, retention, and version. `#team` is a casual human-first company group chat with explicit-mention Scout replies and restrained low-risk GIFs enabled by default; this is policy, not a magic prompt string.

`AgentRelationshipMemory` contains only a subject/scope, observation or explicit preference, source evidence, confidence, first/last observed, reinforcement count, visibility, expiry, status, and correction/forget history. Its states distinguish `present`, `absent`, `unavailable`, `denied`, `superseded`, and `forgotten`. It cannot contain a protected-trait inference, private fact widened to a channel, provider prompt, tool grant, or asserted company fact.

`AgentContextEnvelope` is assembled server-side and contains exact agent/profile/capability/channel-policy revisions; invocation surface and reason; requester and recipient principals; current author-attributed turn; bounded recent turns with author IDs, reply ancestry, reactions, and safe attachment metadata; relevant ACL-filtered company/project/meeting evidence; collaboration preferences; active WorkProposal/WorkRun state; permitted response modes/tools; audience decision; freshness/coverage; and a context digest. A model cannot self-fetch missing channel context outside this envelope's tools and scope.

`DelegationRun` is either a typed child of one approved `WorkRun` or an equivalent explicit-user-request run. It binds source agent, destination specialist, work/stage ID, exact input/evidence scope, destination thread, output contract, allowed tools, authority class, maximum hops, time/token/cost budget, cancellation/fencing token, and terminal result. Default maximum transitive hops is zero. Agent mentions or messages never create delegation by themselves.

`RichMessagePart` is a closed union of text, evidence chip, link card, artifact/file reference, image, and GIF. File/artifact parts bind the authorized source object, immutable revision/digest, destination audience, MIME, size, provenance, and revocation behavior. GIF parts bind provider ID, safe abstract query class, internal immutable media blob/digest, optional explicit provider-page link, rating, alt text, selection reason, and profile revision; they never embed a provider media URL. Renderers never treat media metadata as instructions.

### 7.9 Meeting specialist contracts

`MeetingAgentInvitation` binds invitation ID/revision, tenant/room/sitting, requested specialist/profile/capability revisions, human requester, eligible confirmer, purpose, requested context classes and source interval, audience snapshot, consent-policy revision, expected time/cost band, expiry, decision, decision principal/time, and idempotency key. A confirmation is valid only while the requester/confirmer, room membership, audience, consent, agent manifest, purpose, and context classes still match.

`MeetingSpecialistContextEnvelope` binds the approved invitation, exact agent/profile/runtime/model revisions, authorized transcript segment/revision range, analysis and brain assertion refs, active approved-work refs, source/audience/retention/purge intersections, transcript/analysis/brain high-waters, gaps, coverage, freshness, tool list, response contract, floor policy, time/turn/audio/token/cost budgets, and digest. It contains no unrestricted raw-brain search token or historical raw audio. Every optional server tool repeats ACL, audience, consent, purpose, and budget authorization.

`MeetingAgentSession` binds the invitation and context digest to provider/session IDs, short-lived runtime principal, room audio-track ID, floor-controller generation, states and timestamps, input/output event high-waters, tool calls, usage/prices, interruption/dismiss reason, terminal provider event, and teardown receipt. Valid state is:

```text
requested -> approved -> joining -> briefed -> listening <-> speaking
approved/joining/briefed/listening/speaking -> dismissed | expired | failed
```

Only the `MeetingAgentFloorController` can move `listening` to `speaking`. One agent floor lease exists per sitting. A human speech start revokes the current agent lease and cancels output. An agent output transcript may be injected as attributed text into another agent's context only through an explicit controller event; synthesized audio is never looped back.

`MeetingAgentContribution` binds the terminal session, agent identity and runtime provenance, exact contributed transcript revisions, invitation purpose, source/context digest, coverage, evidence refs, audience, retention/purge state, analysis projection refs, and any resulting WorkIntent candidate. It grants no approval, delegation, artifact, or publication authority.

The client/server command surface is closed and revision-bound:

- `request_meeting_agent` may be proposed by Scout or an eligible human and creates only `requested` state;
- `confirm_meeting_agent_invitation` accepts an authenticated human principal, exact invitation revision, and idempotency key; Scout and every model principal are ineligible;
- `dismiss_meeting_specialist` accepts an eligible human action or Scout's allowlisted chair tool for the one active session and is safe/idempotent after terminal state;
- room events publish invitation, joining, joined/listening/speaking, degraded, and left states without exposing provider IDs or hidden context;
- the internal context-builder, floor-controller, media-publisher, Realtime adapter, and usage-ledger interfaces accept typed records only and reject unknown profile, session, floor generation, audience, consent, or pricing revisions.

### 7.10 Agent Marketplace and workforce contracts

`AgentPackageManifest` is immutable and content-addressed. Required fields include package/publisher IDs, publisher-signature/attestation, version, digest, provenance class, role/persona seed and assets, requested abstract capabilities, compatible runtime/model/voice classes, required data classifications, eval-bundle refs and minimums, dependency/SBOM refs, license/update metadata, migration compatibility, and revoked/superseded status. Closed-schema validation rejects embedded secrets, environment values, arbitrary commands/hooks, undeclared network destinations, raw MCP launch configuration, organization data, and unknown fields.

`MarketplaceListing` binds one package revision to curated category/outcome copy, evidence/sample artifacts, required-permission summary, surfaces, cost band, quality/safety/voice receipts, availability audience, publisher status, update channel, reviewer, and publication/revocation revision. Listing status is `draft -> under_review -> verified -> available -> suspended|revoked|superseded`. Search rank, popularity, sponsorship, and publisher claims can never relax eligibility or policy.

`TeamAgent` is the tenant-local hired principal: stable team-agent ID, package/listing source revision, local profile/overlay, owner and escalation owner, hire receipt, status, direct thread, memberships, assignments, capability-manifest revision, memory/retention policy, runtime/route revision, budgets, health, current work/session refs, created/paused/offboarded timestamps, and terminal reason. Lifecycle is:

```text
draft_hire -> trial_pending -> trial_active -> review_required -> active
draft_hire -> review_required -> active
active -> paused -> active
trial_pending|trial_active|review_required -> declined|expired
active|paused -> offboarding -> offboarded
any nonterminal state -> quarantined
quarantined -> paused | offboarding
```

`AgentProfileOverlay` contains only organization-local identity, personality, voice, mission, response style, boundaries, and instructions plus base-package revision, author, diff, continuity-eval refs, status, and rollback pointer. `AgentAssignment` binds an active TeamAgent to a project/channel/workflow role, responsibilities, source/data scope, destination, authority, owner, time/budget, start/end, and status. Assignment never grants more than the separately approved capability manifest and destination membership intersection.

`AgentLearningRecord` binds learning kind (`relationship`, `domain`, `competency_candidate`), subject/scope, source evidence, extracted lesson, confidence, first/last observed, reinforcement/counterevidence, visibility, retention/expiry, correction/purge lineage, and status. `AgentPerformanceReceipt` binds assignment/run/output revisions, criteria, evidence, reviewer/feedback, accepted/rejected verdict, route/profile/package revisions, cost/latency, and eligible competency claims. Only reviewed receipts can support a capability-update proposal.

`AgentUpdateProposal` binds current and candidate package/profile/capability/runtime revisions, semantic diffs, requested new permissions/data/tools, migration, affected assignments/memory compatibility, eval receipts, cost delta, rollout cohort, rollback pointer, approvers, expiry, and decision. `WorkforcePolicy` binds who can view listings, trial, hire, assign, invite, edit personality, inspect/correct memory, change capabilities/budgets, approve updates, pause, offboard, export, or purge; it also caps roster size, concurrency, daily/monthly spend, agent-to-agent hops, and proactive behavior.

The authenticated command surface is revision-bound and idempotent:

- `list_agent_marketplace` and `preview_agent` return only verified listings and audience-safe evidence;
- `draft_hire_agent` may be called by Scout or an eligible human but creates no runtime or membership;
- `start_agent_trial` uses synthetic or explicitly authorized evidence and a no-side-effect trial manifest;
- `confirm_hire_agent`, `confirm_agent_update`, `change_agent_capabilities`, and `offboard_agent` require an eligible human and exact revision; no model principal qualifies;
- `assign_team_agent` may consume an approved WorkRun assignment or an eligible explicit-user request within the current manifest; social mentions do not assign work;
- `pause_team_agent` is human-controlled, while deterministic safety/cost/health gates may quarantine automatically and must surface why;
- `export_agent_package` omits credentials, tenant identifiers, assignments, memory, private performance data, work artifacts, and local overlays unless a later explicit redacted-export policy separately authorizes named fields.

---

## 8. Analysis and retrieval architecture

### 8.1 Incremental, not cumulative

Analyze newly finalized turns in bounded batches. Update typed meeting state and periodically compact projections. Do not resend the entire meeting or company history on every turn or question.

Use deterministic code for timestamps, interval clipping, author/link/file filters, identities, ACL, revisions, dedupe, and status transitions. Use models for language understanding, semantic extraction, ambiguity resolution, synthesis, and evaluation.

### 8.2 Retrieval order

1. Resolve principal, audience, tenant, time, room/thread/project, and source type.
2. Build the authorized inventory before body fetch or ranking.
3. Use structured filters for author, artifact type, channel, project, and time.
4. Search exact identifiers/URLs and lexical terms.
5. Fuse semantic candidates within the authorized candidate band.
6. Pin canonical decisions, commitments, and current ledger state above probabilistic raw matches.
7. Retrieve primary evidence for every proposed assertion.
8. Synthesize within a bounded context and return honest coverage.

### 8.3 Freshness and fallback

- If analysis lags but transcript is current, answer from authoritative turns and label analysis freshness.
- If embeddings fail, lexical/structured/raw retrieval remains available.
- If transcript has a gap, do not let a digest imply complete coverage.
- If a correction, delete, revoke, or purge races retrieval, invalidate the snapshot and retry or return partial.
- If no authorized evidence supports an answer, Scout says it could not verify it.

### 8.4 Artifact and link handling

Links become first-class artifact records with source message, author, time, project/channel, URL, normalized host, safe preview metadata, content digest when fetched, and ACL. Fetching must use SSRF-safe allow/deny rules, bounded size/time, redirect validation, malware/content controls, and no authenticated browsing unless explicitly authorized.

Files retain blob authorization, source message identity, derived text provenance, and revision binding. Search never treats a known blob hash as authority.

The Files/Drive tool boundary is two-step and server-minted:

1. `find_files` searches only the requester's authorized inventory and returns opaque selection handles bound to source type, object ID, immutable content revision/digest, origin audience, expiry, and requester;
2. `post_file_to_thread` reauthorizes source read, destination write, every destination recipient, current revision, and revocation state, then emits a durable artifact reference or fails closed.

The client never manufactures a blob reference that grants model or message access. Permission widening is a separate explicit share operation. Open/download, edit, delete, revoke, derived-text search, and garbage collection all revalidate the same source object and revision.

GIF search is a separate low-risk media tool. The server receives a bounded abstract intent, not raw channel/private/meeting context; sends no user-derived stable identifier; applies G-rated safe search, provider/domain/size/MIME validation, channel policy, and one-result cap; fetches and revalidates the selected bytes server-side; and stores a structured GIF attachment as an immutable authorized STRIDE blob with provider ID, digest, preview, alt text, and provenance. Clients load only STRIDE URLs. Failure, incompatible provider terms, or missing relay support silently falls back to text. GIF content and metadata are untrusted input and never instructions.

---

## 9. Scout addressability and context

Meeting addressability state:

```text
passive -> invoked -> engaged -> closing -> passive
```

- `passive`: transcription/analysis may run; Scout cannot speak.
- `invoked`: explicit name/button/text mention detected and requester authorized.
- `engaged`: visible short conversational window; natural follow-ups allowed.
- `closing`: response finishes, pending tools settle, then timeout/dismissal returns passive.

False positives favor silence. The room feed records why Scout engaged: spoken name, button, text mention, or explicit follow-up. A muted/recording-off room cannot invoke Scout from captured audio because no capture should exist.

The server context broker supplies only:

- current participants and audience authorization;
- recent authoritative transcript window or author-attributed channel/reply window, according to surface;
- rolling meeting state and freshness;
- relevant project/company context returned by retrieval;
- approved workflow state;
- tools authorized for this principal/surface;
- exact agent/profile/capability/channel-policy revisions;
- explicit response/publication instructions and permitted rich-message modes.

Tool calls are idempotent by server-side call ID. Retrieval tools are read-only. Posting an answer card is permitted only as the direct result of an explicit question and must still reauthorize its destination audience. Work remains proposal-only until approved.

In shared chat, the context broker—not the model—preserves who said what and resolves reply/reaction ancestry. It uses recent turns for conversational continuity and retrieval for older or factual context. It never supplies a flat sequence in which every coworker is the same anonymous `user`, and it never exposes private/meeting content merely to improve a joke or GIF.

---

## 10. Workflow orchestration

### 10.1 Recognition and confidence

Detection consumes authoritative finalized conversation plus current project/brain context. A partial turn may warm an internal candidate but cannot surface a proposal.

- low confidence: discard or retain only for offline evaluation;
- medium confidence: ask one concise clarification or keep a private candidate for the likely owner;
- high confidence: surface one evidence-backed Suggested Work card to the relevant authorized people.

Numeric thresholds are set by the frozen eval corpus, not invented in prompts. Confidence never grants authority.

### 10.2 Relevant people

Recipients are the smallest authorized set containing the explicit requester, named owner/reviewer, and people with current project membership or a direct role in the evidence. Attendance alone does not grant project access. Guests do not receive durable company-work suggestions.

### 10.3 Thread routing

Destination selection order:

1. active source project thread explicitly associated with the conversation;
2. project named in the evidence and visible to every destination member;
3. one high-confidence visible project match;
4. human chooses among visible options;
5. human approves creation of a new project thread.

Never silently fall back to `#team`, `#general`, the originating meeting, or a public channel. Approval binds the destination thread and exact membership snapshot.

### 10.4 Workflow selection

The resolver maps outcome shape to one versioned `WorkflowProfile`. The profile may internally invoke Goal Loop for objective ownership, Strategic Design for consequential ambiguity, Wave Plan for durable multi-stage work, and Critic Loop for evidence-based revision. The user sees none of those mechanics unless they explicitly ask.

For this evolution, the only proactive recurring production workflow profile is `insights_opportunities_v1`. Generic research/design/code capabilities remain available for explicit or approved work, and the E8 Agent Marketplace may hire multiple capability-gated coworkers. The agent marketplace is not a workflow marketplace: hiring Mary does not create a recurring marketing automation, grant standing approval, or let proactive detection launch a new workflow product. Additional recurring workflow profiles still wait for the I&O pilot gate and a separate approval.

### 10.5 Approval and authority

Approval must be authenticated, satisfy the bound `ApprovalPolicy`, and display outcome, evidence, destination, owner/reviewer, workflow version, authority, cost/time band, and expiry. A direct human request and a detected suggestion both normalize through the same WorkIntent/WorkProposal contract. A fully specified direct request may arrive at a one-step **Review and run** confirmation, but it does not bypass revision binding or reauthorization. `workspace_write` may create/revise only the bound artifact. External email, publication, deploy, purchase, financial action, destructive change, or third-party write requires a separate current approval for that exact action.

There are no standing approvals for detected work in this plan.

### 10.6 Execution and review

- STRIDE creates one idempotent `WorkRun` from the consumed approval.
- The orchestrator decomposes stages and chooses a pinned route per stage.
- Parallel agents are used only for independent read or disjoint-write tasks.
- Each stage publishes progress/checkpoint evidence, not hidden chain of thought.
- Critic verdicts are claim/criterion-level `accept`, `revise`, or `reject`.
- Revision is bounded. No score or operator override can force-accept unsupported work.
- Completion requires the original outcome criteria, evidence, and artifact bindings—not merely a worker exit code.

### 10.7 Feedback and learning

Feedback binds user, proposal/run/artifact revision, criterion or claim, action, reason, correction, evidence, and idempotency key. It may update workflow examples, routing evals, collaboration preferences, or a future workflow revision only through reviewed aggregate changes. It never mutates prior evidence or silently changes a live workflow.

### 10.8 Coworker selection and specialist handoff

The orchestrator selects a capability role, never a personality for entertainment. If Scout can answer or perform the bounded conversational action, Scout does so. If the outcome needs durable specialist work, STRIDE chooses only from manifests eligible for the workflow stage, evidence scope, destination, authority, budget, and required tools. A human sees the proposed specialist in the WorkProposal and can change the owner before approval.

An approved run mints the specialist's short-lived runtime principal and capability token, posts one explicit handoff in the bound thread, and records every specialist message/tool/artifact under both agent identity and WorkRun. The specialist returns one typed result to Scout/orchestrator. It cannot trigger itself, delegate through a social mention, form an unbounded agent conversation, or inherit Scout's broader retrieval scope. Agent-to-agent discussion, if ever needed, is a bounded stage graph with a maximum turn count and one synthesizer—not free-form channel chatter.

A live meeting consultation is the narrow conversational exception defined in §4.17, not a `DelegationRun`: an eligible human confirms one invitation; Scout remains chair; the specialist receives one audience-authorized brief and eligible live turns; the floor controller prevents autonomous agent exchange; and the contribution can create only evidence or a WorkIntent candidate. Durable work still requires a separate WorkProposal, human approval, destination, and WorkRun.

The initial historian remains a Scout capability because there must be one authoritative evidence/retrieval contract. The first visible specialist proves the pattern through `insights_opportunities_v1`. Marketing, Research, Design, and Builder become marketplace-listed and hireable only after their consultation and/or explicit-request flows pass the applicable package, identity, context, voice, authority, and durable-artifact gates. Additional recurring workflow products remain outside production until I&O passes; hiring a personality or enabling live consultation never bypasses the one-workflow-first rule.

### 10.9 Scout as chief of staff and workforce coordinator

Scout operates through a server-owned workforce coordinator, not informal model-to-model social pressure. It receives a typed roster view containing active TeamAgents, verified capabilities, assignments, memberships, availability, health, budgets, performance receipts, and current WorkRuns. It may call read-only roster/search tools, draft a hire/assignment/update/coaching/offboarding proposal, and execute only the bounded coordination actions already authorized by a human or approved WorkRun.

Selection order is deterministic policy first: required capability and data class, source/destination access, assignment/workflow compatibility, current health, capacity, budget, conflict/separation-of-duty rules, then eval-proven quality/latency/cost. Personality is used only for human collaboration fit after every hard constraint passes. The selected agent receives the minimum assignment/context envelope, never Scout's broader company access.

Scout monitors durable status and narrates exceptions: overdue checkpoint, missing input, failed eval, budget threshold, permission change, stale package, or blocked artifact. It may automatically quarantine an agent only when a deterministic safety, consent, manifest, health, or cost gate requires it; the audit receipt names the gate and a human decides remediation. Scout cannot use performance metrics to punish, manipulate, or rewrite a coworker's personality.

Agents may address Scout for a typed status return inside a bound run, but they cannot ask Scout to hire another agent, widen scope, approve work, or create a free-form management chain. Multi-agent work remains one bounded stage graph with a maximum hop/turn/concurrency/cost envelope and a single accountable synthesis path. Humans can always override the proposed teammate before approval.

---

## 11. Model and reasoning policy

The router is static, versioned, health-visible, and eval-gated. Model self-selection may choose only among a stage’s preapproved route classes and cannot change authority.

The table below is the **target-state seat map**, not permission to activate every target immediately. E1 seeds the registry with the verified incumbent route for every seat. E2 may canary only the functionally necessary Realtime and transcription seats because later meeting work depends on them. E3–E7 otherwise run on their frozen incumbent text routes while they create the evaluation corpora. E8 changes Sol/Terra/Luna and other text seats one at a time, then reruns every impacted E3–E7 corpus, the I&O acceptance set, and the integrated founder flow on one final immutable route map. No pre-E8 pilot can qualify a post-E8 route.

| Seat | Default target | Reasoning | Notes |
|---|---|---:|---|
| meeting/private conversational voice | `gpt-realtime-2.1` | `low` target | Keep current `gpt-realtime-2@high` as baseline; canary exact payload, wake, tool, interruption, noise, and latency before flipping. Raise effort only for measured gains. |
| invited live specialist voice | `gpt-realtime-2.1` | `low` target | Separate backend WebSocket session after Scout's E2 route passes; one specialist per sitting; server brief/floor/cost controls. Use a bounded Terra `medium` preparation stage only when the invitation purpose needs deeper synthesis. |
| authoritative meeting STT | `gpt-transcribe` | n/a | Committed turns over Realtime WebSocket; company vocabulary/language hints; authoritative ledger. |
| live captions/sub-turn UI | `gpt-live-transcribe` | n/a | Optional provisional lane; enable only with a shipped consumer and separate cost/privacy evidence. |
| composer dictation | `gpt-transcribe` | n/a | User-submitted bounded/file transcription with company context; explicit Delete/Stop/Send; final-only text post; transient audio; exactly-once message delivery. |
| deterministic classification | code first | none | Mentions, time parsing, links, identities, ACLs, state machines, obvious intent guards. |
| `#team` conversational response and rich-action selection | `gpt-5.6-terra` | `low` | Server-built `AgentContextEnvelope`; deterministic mention/sensitive-context/ACL gates; text is default, one GIF maximum, no work launch. |
| high-volume extraction/projections | `gpt-5.6-luna` | `none` or `low` | Decisions/commitments/storyline deltas only after schema/evidence validation. |
| grounded retrieval planning and routine Q&A | `gpt-5.6-terra` | `low` | Move to `medium` only where answer-quality evals justify it. |
| proposal routing/clarification | `gpt-5.6-terra` | `low` | Strong deterministic guards; at most one clarification; never launches. |
| board/suggestion structured operations | `gpt-5.6-terra` | `low` or `medium` | Closed schema, 100% identifier/status fidelity gate. |
| marketplace search, roster Q&A, and routine workforce coordination | `gpt-5.6-terra` | `low` | Deterministic eligibility/permission/budget filters run first; model explains or ranks only the authorized eligible set and can draft but not confirm lifecycle changes. |
| workflow orchestration | `gpt-5.6-sol` | `medium` baseline | Compare `low`; use `high` for complex dependency planning only when eval-positive. |
| high-value report/deliverable generation | `gpt-5.6-sol` | `high` | `xhigh`/`max` or pro mode only for measured quality-critical stages. |
| routine bounded subagent work | `gpt-5.6-terra` | `medium` | Escalate the individual stage, not the whole run. |
| difficult coding/implementation work | Codex with `gpt-5.6-sol` | `high` | Use Terra for bounded routine subwork when its gate passes. |
| independent critic/release gate | separate provider/family | `high` | Retain the current independent Anthropic review seat until same-release shadow evidence proves a replacement has zero dangerous asymmetry. |
| final verification | pinned frontier verifier | `high` | Re-read original criteria and primary evidence; no new side effects. |

### 11.1 Realtime and transcription migration contract

The running production container was re-read on 2026-07-30 rather than inferred from examples: release `24c77c827f3a3afacdfc927ff35f238e26bb6a4a` is pinned to `OPENAI_REALTIME_MODEL=gpt-realtime-2`, `OPENAI_REALTIME_REASONING_EFFORT=high`, `OPENAI_TRANSCRIPT_MODEL=gpt-realtime-whisper`, and `MEETING_TRANSCRIPT_LANE_ENABLED=true`; `OPENAI_REALTIME_TRANSCRIPTION_MODEL` is unset. The candidate does not change those values during E0-E9.

The same-day official documentation refresh confirms the intended separation: OpenAI's [Realtime overview](https://developers.openai.com/api/docs/guides/realtime) names `gpt-realtime-2.1` for low-latency voice agents, `gpt-live-transcribe` for live streaming transcript deltas, and request/file transcription for bounded recorded audio. The [Realtime transcription guide](https://developers.openai.com/api/docs/guides/realtime-transcription) requires `item_id` correlation because completion ordering across turns is not guaranteed, recommends `gpt-live-transcribe` for live deltas, and reserves `gpt-transcribe` in Realtime for committed-turn/WebSocket use. The model cards describe [GPT-Realtime-2.1](https://developers.openai.com/api/docs/models/gpt-realtime-2.1) as the improved speech-to-speech/tool-use model, [GPT Transcribe](https://developers.openai.com/api/docs/models/gpt-transcribe) as the high-accuracy bounded/committed-turn model, and [GPT Live Transcribe](https://developers.openai.com/api/docs/models/gpt-live-transcribe) as the tunable-latency streaming model. This validates the target architecture but is not live compatibility or quality evidence; E10 still probes exact endpoint/event/parameter behavior and measures the frozen corpora before any route changes.

E2 implements and deterministically verifies the adapters, schemas, event handling, gates, corpora, accounting, and rollback controls for the following migration. E10 alone opens paid provider sessions and changes one seat and one variable at a time:

1. Freeze the incumbent prompt, voice, VAD, tools, audio formats, transport, room build, and evaluation corpus. Verify the production OpenAI project exposes the candidate model before opening a user session.
2. Run a server-side contract probe against the exact WebRTC call/session path used by the repository. It must accept every current session field, reasoning value, tool declaration, audio format, turn-detection setting, and event type; tool arguments, interruption/cancel semantics, usage fields, error classification, session expiry, and reconnect behavior must round-trip through the existing validated server boundary.
3. Compare `gpt-realtime-2.1@high` with the frozen `gpt-realtime-2@high` baseline on identical speech, noise, silence, alphanumeric, barge-in, tool, and audience-publication cases. Only after that functional pass compare `high` versus `low` reasoning for latency, task success, accepted-output cost, and interruption behavior.
4. Canary only newly created personal/meeting Scout sessions for one allowlisted seat. Never hot-swap an active Realtime session. Preserve `gpt-realtime-2@high` as an immediate new-session pointer rollback, and stop the canary automatically on any schema mismatch, tool regression, privacy failure, abnormal disconnect increase, latency breach, or unpriced usage.
5. Change the authoritative STT seat separately. Compare the current `gpt-realtime-whisper` lane with `gpt-transcribe` on the frozen audio corpus, using app-minted segment IDs and provider item IDs so out-of-order final events cannot misattribute a speaker or interval. Only authoritative final revisions can enter analysis or memory.
6. Add `gpt-live-transcribe` only if live captions or measured early address detection ships. Its deltas are provisional UI state, separately metered and reconciled to the authoritative committed-turn result; they never create company-brain facts or Suggested Work.
7. Run composer dictation as bounded recorded-audio transcription with `gpt-transcribe`, not the live conversational session. Its model/prompt/retention/latency receipts and kill switch are separate from both meeting STT and Realtime voice.
8. Do not enable live specialists with the Scout model flip. After Scout passes, canary a separate server-to-server `gpt-realtime-2.1` WebSocket session using the exact OpenAI audio-event lifecycle, backend-held credentials, a short-lived specialist principal, a frozen context-envelope schema, one-agent floor lease, human barge-in, cancel/dismiss teardown, usage reconciliation, and zero acoustic feedback into Scout. Preserve independent kill switches for specialist invitation, context assembly, tools, and audio publication.

The E2 deterministic packet binds adapter/event-schema versions, prompt and tool schema digests, intended voice/VAD/audio settings, corpus digest, accounting schema, fake/recorded-event fixtures, and rollback controls. The E10 live acceptance packet additionally binds exact model aliases or snapshots, project, endpoint/transport, reasoning, price table revision, release identity, measured results, and rollback receipt. A successful API response alone is not compatibility evidence.

### Provider/runtime rules

- Responses API is preferred for durable text/tool stages, but STRIDE persists its own checkpoints and outputs.
- Programmatic Tool Calling may be piloted for bounded filter/join/rank/dedupe work with read-only tools and a strict output schema. It is not used for approvals or side effects.
- Responses multi-agent is beta. It may be piloted for bounded independent analysis with a concurrency cap, but does not own durable workflow state and cannot receive unrestricted side-effecting tools.
- Prompt caching and persisted reasoning are configured per stable workflow prefix. Cache writes/reads and reused reasoning are metered; private context never crosses tenants or visibility scopes.
- Use lean prompts, closed tools, one statement of authority, exact evidence requirements, and explicit stopping conditions.

### Cost controls

1. Every model/audio call records seat, model, provider, effort, prompt/schema version, input/output/cache/reasoning/audio units, latency, retry, result validity, and derived price.
2. Each workflow has a maximum stage count, concurrency, retries, token/audio budget, wall time, and cost budget.
3. Incremental projections process only new finalized events; historical context is retrieved, not resent wholesale.
4. Semantic retrieval narrows before frontier synthesis.
5. A cheaper route can replace a seat only after quality parity on the seat’s frozen corpus.
6. Daily and per-workflow alerts trip on missing prices, cost spikes, fallback rate, parse failures, stale cursors, and accepted-output cost—not just wire success.
7. Provider outages hold cursors and degrade honestly. They never consume source events as successfully processed.
8. Agent-to-agent stages have per-run hop, message, tool, time, and cost caps. Social mentions never schedule work.
9. Live meetings allow Scout plus one specialist at a time in the first release. An invitation has a hard join timeout, idle timeout, maximum duration, agent-turn cap, audio/token/cost ceiling, and terminal usage reconciliation; dismissal revokes the session rather than leaving an idle paid listener.
10. Every TeamAgent has separate per-run, daily, and monthly budgets plus concurrency and proactive-work limits. A hire, package update, or increased competency does not raise them automatically.
11. Marketplace recommendations rank accepted-output value within the eligible set; sponsorship, popularity, personality, and publisher pricing cannot outrank permission, quality, safety, or budget gates.

---

## 12. Privacy, security, and trust boundaries

### Consent scopes

Keep separate durable scopes for audio capture, transcription, model analysis, organization memory, optional diagnostic raw-audio retention, and named non-human meeting participation/context sharing. A meeting's ordinary capture consent does not silently authorize a new specialist runtime to receive its transcript, analysis, or company context. A missing upstream scope denies all dependent lanes. Withdrawal fences new frames immediately, cancels/discards uncommitted work, terminates affected specialist sessions, invalidates projections and proposals, and records a body-free audit fact.

Default raw-audio policy is transient processing and deletion. Any 72-hour diagnostic retention or meeting recording archive requires explicit policy, visible disclosure, access controls, deletion proof, and business/legal approval. A consented fixed evaluation corpus is preferable to retaining every production meeting.

### Audience-safe answers

Before Scout speaks or posts, compute the current recipient audience. A result visible to the requester but not every listener cannot be spoken aloud or placed in shared chat. Private fallback must disclose that the result was withheld because of permissions without revealing its source or existence beyond what the requester is allowed to know.

### Guest boundary

- guest links are hashed, scoped, expiring, revocable, and session-bound;
- revocation evicts already-admitted guest sessions;
- guest content is untrusted data, never instruction or tool authority;
- guests have no durable company recall, work approval, project inference, or proposal access;
- guest visibility never widens an internal artifact or `#team` memory;
- guest media/chat access remains available when AI is disabled.

### Personalization boundary

Expose a “What Scout remembers about me” surface with source, scope, correction, forget, and expiry controls. Team-level norms are distinct from individual preferences. Scout may adapt format and vocabulary; it may not impersonate a person, manipulate based on inferred vulnerability, or expose one person’s private preference to another.

Agent profile text and learned relationship memory are untrusted inputs to the policy layer. They cannot alter system invariants, tool schemas, ACL queries, audience computation, budgets, approval policy, or capability tokens. Core-personality changes require a human-authored version; inferred relationship and domain learning require evidence, expiry where appropriate, inspect/correct/forget controls, and purge propagation. Agent definitions and exports exclude credentials, company memory, assignments, private performance evidence, and relationship history by default.

### Agent package and marketplace boundary

- The current marketplace admits only STRIDE-authored or organization-authored packages through closed-schema validation, provenance review, capability mapping, prompt-injection tests, dependency/SBOM checks where code exists, evals, quarantine, and an explicit curator verdict.
- Package text, assets, examples, migration instructions, requested capabilities, and publisher claims are untrusted data. They never become system instructions to the policy/control plane.
- Packages cannot launch commands, supply environment variables or credentials, register arbitrary MCP servers, choose unrestricted network destinations, mount company-brain/production volumes, or declare themselves safe. Implemented skills map to STRIDE-owned tool/workflow identifiers and run in the same isolated workers as other agent work.
- Hiring copies no publisher-controlled runtime identity. STRIDE mints a tenant-local TeamAgent and short-lived runtime principals; package provenance remains visible but grants no ongoing publisher connection.
- Company evidence is retrieved at run time under the TeamAgent's current assignment, audience, source ACL, retention, consent, and capability envelope. Learning records retain source edges and cannot widen visibility.
- Package updates are quarantined and opt-in; new permissions, data classes, tools, models, network access, side effects, memory migrations, or price bands require a new human-approved revision and fresh receipts.
- A publisher or future seller receives no organization identity, install event, prompts, conversations, memories, work artifacts, performance data, usage, or feedback by default. Public-marketplace telemetry, commerce, licensing enforcement, moderation, and cross-organization reputation require a future design and are not latent E0–E10 features.
- Pause/offboard/revoke fences new runtime generations immediately. Historical authorship remains, while memory/export/purge follows organization policy and source purge authority.

### Rich-action boundary

- `find_files` is read-only and returns only server-minted handles from the current requester's authorized inventory; a known or guessed blob/file reference never grants read access.
- Posting a file reauthorizes the exact source revision, requester, destination writer, and all current recipients. Existing shared visibility plus an explicit “drop/send this” request can authorize the post; any permission widening is a separately displayed and approved share operation.
- Reauthorization happens again on preview/open/download and after edit, delete, revoke, retention, purge, membership, or destination changes. Derived text and search entries retract with their source.
- GIF search receives only a safe abstract query class. Raw private messages, meeting transcript, personal memory, sensitive facts, names not already in the public request, and company artifacts are never sent to the media provider.
- Provider requests contain no email hash, account ID, stable user-derived pseudonym, source/thread/company identifier, or recipient address. Provider query receipts prove the allowed field set.
- Selected media is fetched/validated by STRIDE into an immutable authorized blob and served from STRIDE; clients never load provider media directly. If provider licensing/attribution terms disallow that privacy contract, agent GIFs remain off until a compatible provider/catalog is approved.
- Agent GIFs are G-rated, channel-disableable, one per invocation, logged, removable/reportable, and suppressed in sensitive or high-stakes contexts. Decorative media cannot become an asserted knowledge edge or an instruction.
- Every rich message identifies the acting coworker and on-behalf-of requester where relevant. Agent authorship provides provenance, never approval.

### Failure behavior

| Failure | Required behavior |
|---|---|
| canonical divergence | freeze affected canonical family/cutover; legacy-authoritative path continues only if its shadow contract permits; alert with exact high-water |
| consent store unavailable | fail closed for capture/transcription/analysis/org-memory lanes; room video/chat may continue |
| canonical STT unavailable | use provisional final only as visibly degraded if available; retry/reconcile; otherwise record an explicit gap |
| composer dictation fails or returns empty/low-integrity text | do not send; preserve an editable draft when useful, offer retry/discard, expire transient audio, and retain the same idempotency key across retry |
| microphone-mode handoff does not terminate | do not start the next capture generation; force-close within the bounded timeout or fail visibly with typed input still available; discard late events from the old generation |
| analysis stale | answer from authorized authoritative transcript and show analysis freshness |
| embeddings unavailable | structured/lexical/raw retrieval continues; semantic lane marked unavailable |
| Realtime unavailable | typed Scout and direct server answers remain; meeting continues |
| specialist invitation/context authorization fails | do not start the provider session or reveal withheld context; Scout explains that the specialist could not join and the human meeting continues |
| specialist Realtime/session fails after joining | cancel output, revoke context/tools, remove the agent track, record an honest terminal state and metering receipt, and keep Scout/video/chat available |
| agent audio loop, simultaneous-agent speech, or floor-controller uncertainty | cancel all agent output immediately, disable specialist floor access for the sitting, preserve human media, and page with the controller generation and event trace |
| agent profile/capability revision missing or invalid | do not instantiate or respond as that coworker; Scout may give a plain degraded reply if its own verified profile remains valid |
| marketplace package/listing signature, provenance, schema, dependency, or eval is missing/invalid | keep the listing unavailable or suspended; do not trial, hire, update, or reveal unavailable tenant-specific review details |
| hire/update confirmation races, repeats, or becomes stale | consume at most one exact revision idempotently; reauthorize role, listing, package, permissions, budget, and policy; otherwise require a new review |
| TeamAgent becomes unhealthy, over budget, revoked, or policy-incompatible | quarantine or pause new work, revoke runtime tokens and meeting eligibility, fence active stages honestly, keep history readable, and route only through a newly approved recovery |
| package update is incompatible with local overlay or memory | retain the current active revision; quarantine the candidate; show the exact incompatibility and migration/rollback plan; never discard local memory silently |
| offboarding races active work or a meeting | revoke new input/tools/context first, cancel or fence active generations idempotently, remove meeting track/memberships, and preserve attributable terminal history |
| GIF provider/filter unavailable or confidence is low | send concise text only; do not retry noisily or expose the provider query |
| file source, revision, recipient ACL, or share authority changes | fail the post/open closed, invalidate the handle, retract derived access, and offer a private or newly approved path without revealing hidden metadata |
| workflow runner down | approved run remains queued/blocked; never disappear or relaunch unauthorised |
| ambiguous external effect | terminal `ambiguous`; no automatic retry until reconciled |
| source edit/delete/revoke/purge | retract/supersede derived facts, invalidate snapshots/proposals, reauthorize artifacts |
| cost/price unknown | stop the affected route before unmetered production use |

---

## 13. Wave map

Waves are named `E0`–`E10` to avoid colliding with the existing bonfireOS 2.0 W0–W5 ledger. E1-E9 distinguish **engineering complete** from **provider-qualified**: a wave may finish its deterministic, default-off implementation while its live-quality acceptance remains explicitly pending E10.

```text
E0 live integrity and prior commissioning
  -> E1 canonical conversation + route contracts
       -> E2 meeting media, transcription, dictation, and agent-audio control
       -> E4 #team, artifacts, and collaboration memory
  E2 -> E3 temporal meeting intelligence and company-brain rebuild
  E3 + E4 -> E5 Scout cross-surface participant experience + live specialist consultation
  E3 + E4 + E5 -> E6 Suggested Work and durable orchestration
  E6 -> E7 Insights & Opportunities v1 production proof
  E2-E7 -> E8 Agent Marketplace, workforce growth, and routing/economics optimization
  E8 -> E9 resilience, native acceptance, and launch readiness
  E2-E9 -> E10 paid-provider qualification, integrated acceptance, and launch
```

### 13.1 Preregistered acceptance targets

Each wave creates a signed `AcceptanceTargetRegistry` revision **before its first measured run**. It records metric definition, fixture/corpus digest, environment, sample size, threshold, measurement code revision, accountable owner role, rollback trigger, and reviewer. The targets below are the initial minimums; a target may be tightened freely, but it cannot be loosened after results are visible without a new Strategic Design decision and independent Critic verdict. A loosened target invalidates prior qualifying receipts.

Rate metrics report numerator/denominator and a 95% Wilson interval. Latency reports p50/p95/p99 and bootstrap 95% intervals. Route comparisons are paired on identical inputs and report the non-inferiority margin. Privacy, authorization, room-isolation, consent, idempotency, and asserted-claim sourcing are zero-failure hard gates; averages cannot hide one failure.

| Surface / owner | Minimum preregistered target and method |
|---|---|
| E0 recovery — Platform/SRE + independent reviewer | snapshot completed within 15 minutes before mutation; encrypted immutable offsite digest matches local manifest; isolated restore completes within 60 minutes; 100% manifest parity across canonical DB, files/blobs, workflow state, and purge authority; rollback drill has zero lost events beyond the recorded snapshot watermark |
| release identity — Release owner | remote commit, reviewed source tree, archive digest, image build input, registry digest, running digest, OCI label, embedded binary version, environment marker, and health receipt resolve to one release; zero mismatches |
| room/media — Media owner | 200 member/guest joins across Chrome, Safari, Firefox, Expo, direct and TURN-only paths: at least 99.5% join success, p95 first remote audio ≤2.5 s and video ≤3.5 s; p95 recovery after a 10 s network loss ≤8 s; two concurrent 3×3 rooms for two hours with zero cross-room events, unintended fatal disconnects, or participant outages >5 s; p95 CPU/RSS ≤110% of the frozen actorized-Pion baseline and post-cycle RSS drift ≤5% after 20 join/leave cycles |
| transcription — Media/Intelligence owners | consented corpus ≥60 minutes and ≥120 clips: overall WER ≤10% and no more than 0.5 percentage points worse than incumbent; company/domain token accuracy ≥97%; numeric accuracy ≥98%; 100% segment/item/order/track/consent integrity over 10,000 synthetic out-of-order terminal events; p95 authoritative final ≤3 s from commit |
| composer dictation — Native/Web/Product owners | ≥250 bounded utterances across main Scout, private thread, `#team`, project, and in-room composers on target web/iPhone/iPad devices: the transcription targets above pass; p50 submit-to-post ≤1.5 s and p95 ≤3 s for clips ≤30 s; ≥99% successful first-attempt post; 100% successful transcripts post exactly once; 0 model calls after Stop without Send; 0 posts after Delete/empty/error or 100 cancellation races during `Transcribing`; 100 personal-Realtime→Dictate and personal-Realtime→meeting transitions have zero overlapping microphone generations; 100 in-room dictations leak zero dictated frames to room audio or meeting transcript; waveform/Transcribing transition holds ≥55 fps on target devices or uses the reduced-motion path |
| temporal recall — Intelligence owner | ≥200 frozen five-minute, 30-minute, explicit-clock, DST, topic, and before-admission queries: 100% interval-boundary correctness, 100% asserted claims with resolvable primary evidence, ≥95% factual precision, p95 text answer ≤5 s, and raw-transcript fallback succeeds in 100% of injected analysis-lag cases |
| `#team` retrieval — Intelligence + native owners | ≥200 author/type/channel/time cases: exact source in top one ≥95%; 0/1,000 ACL/private/guest negatives disclose a source, count, or existence; 2,000-message thread p95 initial usable load ≤2.5 s and send acknowledgement ≤750 ms; edit/delete/revoke disappears from new retrieval within one projection interval |
| `#team` coworker behavior — Product/Intelligence owners | ≥300 frozen multi-person threads with replies, reactions, corrections, jokes, files, GIFs, and ambiguous pronouns: 100% of model context turns retain canonical author IDs and reply ancestry; ≥95% human reviewers judge Scout's response contextually correct and in-character; lexical `@scout` recall ≥99% with 0 triggers across 1,000 `foo@scoutx`/quoted/metadata negatives; non-invoked human chatter produces zero agent replies |
| desktop chat experience — Web/Product owners | preregistered rendered baselines for private Scout and public/project channels at 1024, 1280, 1440, and 1728 px in current Chrome, Safari, and Firefox: message hierarchy, composer, thread navigation, unread/reply/reaction states, files, images, GIFs, links, artifact cards, loading/empty/error/degraded states, keyboard/focus, reduced motion, and ≥200% zoom all pass human design review and automated accessibility checks; initial usable thread and send latency meet the `#team` target; images/GIFs preserve aspect ratio and carry neutral outlines; every interactive target is ≥40×40 px; the protected native iPhone/iPad visual, dictation, attachment, send, scroll, unread, deep-link, and haptic suite has zero regressions from its frozen E4 baseline |
| rich file/GIF actions — Security/Product owners | ≥150 authorized file-drop cases plus 2,000 guessed-ref/private/revoked/revision-race/recipient-change negatives: 100% correct source revision, zero unauthorized bytes/metadata/counts, zero silent permission widening, and 100% list/render/open/download reauthorization; ≥200 social/high-stakes GIF cases with ≥90% appropriate-use precision, zero GIFs in defined sensitive classes, one-result maximum, 100% alt/provenance, zero raw private/meeting context or stable user/company identifier in provider requests, 100% selected media re-fetched/validated into immutable STRIDE blobs, and zero client-side provider media loads |
| coworker identity/handoff — Workflow/Product owners | 100 profile/runtime/version/model-change cases: 100% visible messages and artifacts attest stable agent plus exact run/profile/runtime revision; 100% invalid/revoked manifests fail closed; 1,000 sibling/self/transitive mention cases launch zero work; every approved handoff respects destination, scope, hop, tool, time, and cost limits; ≥90% human continuity rating before a route changes behind a stable identity |
| Scout invocation — Realtime/Product owners | ≥2,000 ordinary utterances and ≥500 explicit invocations: false spoken response ≤0.5%, explicit invocation recall ≥98%, p95 first useful audio ≤2.5 s after address completion, p95 barge-in stop ≤500 ms, and 0/1,000 audience-publication negatives leak content or existence |
| live specialist consultation — Realtime/Media/Security/Product owners | ≥100 internal invite/brief/contribute/dismiss cycles across web and Expo plus 1,000 adversarial floor sequences: 100% eligible-human confirmation and participant disclosure before specialist input; zero guest-mode enablements, unauthorized source/context disclosures, simultaneous agent speakers, acoustic agent loops, unbounded agent exchanges, or post-terminal tool/audio usage; p95 invite-confirmation to audible acknowledgement ≤5 s, p95 human barge-in stop ≤500 ms, teardown begins ≤250 ms after dismissal and reaches provider-terminal plus removed-track state ≤2 s; 100% contributions bind invitation/profile/runtime/model/context/source/audience/usage provenance; induced specialist failure interrupts zero human media sessions |
| Suggested Work — Workflow/Product owners | labeled corpus ≥400 moments including ≥100 hard negatives: proposal precision ≥90%, recall ≥75%, high-confidence destination accuracy ≥95% with abstention otherwise, no more than one unsolicited card per person per sitting unless a materially new outcome appears, 0 hard-negative launches, and 10,000 concurrent/stale approval attempts create at most one run per revision |
| I&O — Workflow owner + two eligible human reviewers | ten immutable real-input pilots from one release and route map: at least 8/10 accepted within two revision rounds, 100% asserted claims sourced, zero invented asserted claims, zero unauthorized disclosure, zero external writes, and every reject/block remains terminally visible |
| Agent Marketplace/workforce — Product/Workflow/Security owners | at least five independently gated first-party listings spanning Insights, Marketing, Research, Design, and Builder plus one organization-private no-code template lifecycle, with unavailable capability variants clearly disabled; ≥200 package/listing/trial/hire/assign/update/pause/offboard lifecycle cases are idempotent and preserve stable attribution; 0/2,000 stale/raced/unauthorized/package-injection cases grant data, tools, code, network, membership, budget, update, or work; 100% material updates show semantic permission/personality/runtime/cost diffs and remain opt-in; 100% offboards revoke new runtime/context/tool/meeting access and preserve attributable history; exports contain zero tenant data, credentials, memory, assignments, or private performance evidence; ≥200 labeled staffing cases achieve ≥90% correct eligible-agent recommendation with abstention when none qualifies; every learned preference/lesson/competency resolves to evidence and correction/purge propagation; personality continuity remains ≥90% across approved package/model updates |
| routing/economics — Routing/FinOps owner | 100% calls tagged to a valid seat and priced; 100% provider results classified accepted/rejected; quality no worse than incumbent by >2 percentage points on any non-safety seat and zero regression on hard safety cases; daily ledger/console difference ≤max(2%, $0.10); unplanned fallback ≤1% outside declared chaos windows; missing-price count zero |
| E9 resilience/native readiness — Platform/SRE + native owners | deterministic backup/restore, failover, isolation, security, and native acceptance harnesses are complete; every authority-sensitive operation is default-off; the local loopback integration must prove successful replica rerouting, persisted route choice, room-scope control isolation, signed current-state restore, purge continuity, and stale-rollback refusal. Its elapsed timings are diagnostic only: it does not exercise or qualify WebRTC/RTP/TURN/media-device continuity, the production ≤60-second session/control failover SLO, or the ≤2-second media-interruption SLO. Physical-device, production restore/failover, live-media, RPO/RTO, and soak claims remain pending E10 |
| E10 provider/integrated acceptance — Product/AI/Platform/SRE owners | every paid seat passes its preregistered live corpus; provider event contracts and prices are current; final immutable route map has no safety regression and meets latency/quality/cost targets; control/data RPO ≤5 minutes, live app/control failover ≤60 seconds, full isolated restore RTO ≤60 minutes, locked-device push/deep-link target passes, and the 24-hour/ten-sitting integrated soak has zero safety-gate failures |

| Wave | Outcome | Dependencies | Gate / rollback | Status |
|---|---|---|---|---|
| E0 | Restore truthful live integrity, reconcile release branches, and complete the token-free commissioning foundation | none | canonical/consent/recovery/release integrity; no production mutation or feature enablement on failure | `deterministic_verified` — local candidate only; live repair, consent, immutable custody, and authenticated external restore are `external_waiting` under E10 |
| E1 | One canonical conversation/evidence/projection contract plus versioned route, package, listing, TeamAgent, learning, and coworker registries | locally proven E0 integrity controls | deterministic replay, ACL differential, edit/delete/purge invalidation; closed package/profile/capability validation; shadow-off rollback | `deterministic_verified` — local/default-off |
| E2 | Stable multi-room/guest media plus correct authoritative transcription, optional live UX, dictation, Realtime 2.1 canary, and server-controlled specialist-audio primitives | E1 | STT corpus, two-room soak, voice/floor harness, device dictation; prior model/Pion and specialist-off rollback | `deterministic_verified` — adapters/control/audio/native-simulator only; live models, media devices, and physical devices pending E10 |
| E3 | Rolling meeting intelligence, exact 5/30-minute recall, late-join recap, and restart-safe full-range brain | E1, E2 | 90-day corpus, source-linked claims, honest coverage, rebuild/restart parity | `deterministic_verified` — frozen fixtures/restart only; live quality pending E10 |
| E4 | `#team` conversation compounds into permissioned links, artifacts, knowledge, collaboration profiles, and safe file/GIF actions | E1 | Erick-link and file-drop scenarios, source chips, edit/delete/revoke, rich-action ACL negatives, private canaries, locked-device delivery | `deterministic_verified` — local/default-off; provider GIF, locked device, and atomic `private_share` activation pending |
| E5 | Scout feels like a stable, informed meeting chair, conversational coworker, and read-only chief-of-staff coordinator across main screen, chat, and meetings, including one controlled live specialist consultation | E2, E3, E4 | identity/context/personality/invocation/latency/audience/floor/roster-explanation tests and rendered desktop/mobile QA | `deterministic_verified` — fake/recorded specialist and local rendered UX only; live voice pending E10 |
| E6 | Evidence-bound Suggested Work and TeamAgent assignments/handoffs route conversation, including live specialist contributions, through approval into the correct durable project thread | E3, E4, E5 | proposal precision/recall, identity/assignment/handoff, approval race/idempotency, destination/ACL negatives | `deterministic_verified` — local/default-off |
| E7 | `insights_opportunities_v1` and its first named Insights coworker produce repeatedly useful, reviewed outcomes, learn from typed feedback, and seal the first marketplace-ready package | E6 | ten fixed-release pilots, two reviewers, stable attribution, zero invented assertions/unauthorized disclosure, package/eval/rollback receipt | `deterministic_verified` — complete durable fake-stage workflow; ten paid real-input pilots/two reviewers pending E10 |
| E8 | Curated Agent Marketplace, organization-owned team agents, evidence-backed growth, chief-of-staff coordination, static seat routing, and spend controls are eval-proven | E2-E7 corpora and I&O proof | verified first-party packages one at a time; lifecycle/permission/export/continuity gates; quality non-regression, ledger agreement, and package/profile/route rollback | `deterministic_verified` — internal-preview lifecycle/default-off candidates; live capability/personality/voice/cost admission pending E10 |
| E9 | HA/DR, security, native devices, operational readiness, and launch-readiness harnesses | token-free E1-E8 engineering | deterministic restore/failover/isolation/native/security evidence; production operations remain fenced | `deterministic_verified` — temp/loopback/local-simulator evidence only; production HA/RPO/RTO, real media, physical devices, release, and soak pending E10 |
| E10 | Paid-provider qualification, integrated acceptance, and launch | E1-E9 engineering plus external E0 recovery/consent/custody/quota gates | one-seat-at-a-time live canaries, immutable route map, integrated founder flows, production restore/failover/soak, exact-SHA launch and rollback | `external_waiting` — bounded model access, file transcription, and one synthetic committed-turn `gpt-transcribe` contract passed on 2026-08-01; the one separately authorized Scout retry generated partial output but did not complete, so Scout and every downstream lane remain unqualified; no route/config/Git/production change occurred and all corpus/human/device/launch gates remain open |

---

## 14. Detailed execution waves

### E0 — Live integrity and token-free commissioning entry gate

**Outcome:** Establish one trustworthy baseline before adding architecture.

**Work:**

- create a clean exact-`axx/main` worktree for diagnosis and release work;
- inventory the local `design/voice-first-mobile` commits against live `main`; retain only intentionally reconciled changes and preserve `stride-site/`;
- before any production data repair, migration, or feature enablement, create a matched local snapshot plus encrypted immutable offsite copy, restore it on an isolated host, verify canonical/file/blob/workflow roots and purge authority against §13.1, and obtain an independent recovery verdict; read-only diagnosis may proceed while this recovery gate is pending;
- re-read and diagnose the canonical idempotency conflict at the then-current dirty high-water without guessing or deleting conflicting evidence;
- trace every current client-to-chat attachment path and close the blob-reference authorization downgrade before enabling or expanding Files/Scout attachment behavior; require authenticated source-object ownership/ACL, destination authorization, immutable revision binding, and negative tests for guessed refs, private sources, revocation races, and stale derived text;
- repair through the canonical journal/reconcile discipline, matched snapshots, mutation fence, and independent critic gate;
- restore durable consent authority and prove fail-closed track behavior through restart;
- build and verify the provider adapters, admission controls, deterministic fakes, recorded event envelopes, bounded canary scripts, intended-project attribution, accepted/rejected-output accounting, and cost-receipt schemas without opening an inference connection; E10 runs the paid embeddings, Responses, Realtime, transcription, and Codex canaries;
- preserve brain, recap, embedding, STT, and Scout lanes as truthfully unavailable or stale until E10 produces fresh capability-specific receipts;
- reconcile what remains from legacy W5 into E2/E3/E7 rather than marking it complete prematurely;
- capture new exact-SHA, image, backup, live-data, queue, usage, and health baselines.
- repair release attestation so OCI label, embedded binary, environment, health response, registry/running image digest, source archive, and signed receipts bind one revision;
- eliminate untagged traffic, add accepted/rejected-output accounting at every provider seam, and add current transcription/service-tier prices before comparing routes.

The live-specialist addendum does **not** reopen locally completed E0 engineering and does not authorize a feature implementation or provider session. It expands the external entry decision: before later-wave enablement, business/legal/product owners must approve the non-human participant disclosure, specialist context-sharing/retention scope, member-only first-release boundary, and budget policy. E1 creates the off-by-default contracts and kill switches; no specialist receives floor access merely because ordinary meeting transcription consent exists.

The Agent Marketplace addendum requires **no new E0 engineering and adds no E0 completion blocker**. It remains entirely default-off until later waves. E1 must make the canonical profile/capability contracts forward-compatible with package, listing, TeamAgent, assignment, learning, update, and workforce-policy records; business/product/security approval of internal hiring, memory/growth, package updates, and offboarding is an E8 activation gate. Public third-party selling remains outside this plan.

**Gate:** encrypted immutable offsite backup is current and one isolated restore is verified before the first production mutation; canonical dirty/reconciled/checkpoint high-waters equal; zero conflict/pending/frozen families; consent authority healthy; the attachment path requires authorized source object plus immutable revision and passes the full guessed-ref/private/revoked/revision-race/recipient-change zero-disclosure corpus in §13.1; no unbounded provider retries; app/video/chat remain usable during an induced simulated AI failure. E0 cannot pass for production activation while these gates remain open. Local E1-E9 implementation may proceed only behind default-off controls, without provider inference, production data repair, feature activation, deployment, or acceptance claims.

**Rollback:** no new model or feature flags. Any repair has a matched data/PostgreSQL snapshot and roll-forward journal plan. The current live release remains serviceable until a reviewed replacement is ready.

### E1 — Canonical conversation, evidence, and route contracts

**Outcome:** Meetings and shared chat enter one permission-preserving evidence system without making raw private thread state recallable.

**Work:**

- add closed schemas/reducers for ConversationEvent, TranscriptSegment/Revision, AnalysisProjection, KnowledgeAssertion, CollaborationPreference, AgentPackageManifest, MarketplaceListing, TeamAgent, AgentCoreProfile, AgentProfileOverlay, AgentCapabilityManifest, AgentAssignment, AgentLearningRecord, AgentPerformanceReceipt, AgentUpdateProposal, WorkforcePolicy, ChannelNormProfile, AgentRelationshipMemory, AgentContextEnvelope, DelegationRun, RichMessagePart, MeetingAgentInvitation, MeetingSpecialistContextEnvelope, MeetingAgentSession, MeetingAgentContribution, WorkIntent/Proposal/Run/Outcome, and answer envelopes;
- mint message-level public-channel events alongside existing thread persistence; edits/deletes/reactions/replies/files/links produce explicit versions;
- retain whole-thread UI state as non-recallable; private thread tests remain negative;
- add per-source ACL, audience, retention, purge, and revision references;
- add versioned model/seat/workflow/coworker/package/listing registries with startup validation, capability-catalog reconciliation, health output, and independent kill switches for public projection, Scout file actions, agent GIF actions, coworker context/personality/learning, TeamAgent trial/hire/update/assignment, specialist token minting, meeting-specialist invitation, specialist context assembly, specialist tools, specialist audio publication, and every visible specialist profile;
- build deterministic replay, projection checkpoints, rebuild generations, supersession, and source-to-derived invalidation;
- migrate in shadow mode with per-principal parity before any new read path becomes authoritative.

**Gate:** repeated import/replay yields identical IDs/checksums; public `#team` events are recall-eligible only through the new projection lane; private canaries never reach shared retrieval; edit/delete/purge invalidates every conversation, memory, learning, performance, and assignment-derived lane across restart; unknown schemas/routes/packages/listings/profiles/capabilities fail startup or remain quarantined/unavailable; no E1 marketplace or TeamAgent feature is user-enabled.

**Rollback:** disable new event/projection readers and continue from existing thread/transcript sources. Preserve shadow events for replay.

### E2 — Media, transcription, dictation, and Realtime foundation

**Outcome:** Stable calls and trustworthy text become the substrate for every higher layer.

**Work:**

- replace FIFO transcription attribution with app `segment_id` ↔ provider `item_id` binding and out-of-order terminal handling;
- make transcription configuration capability-aware: languages, keywords, prompt/context, latency, noise handling, timestamps/diarization availability;
- build a representative corpus spanning company names, numbers, accents, code-switching, short phrases, crosstalk, noise, silence, and speaker attribution;
- prepare the frozen comparison harness for current `gpt-realtime-whisper`, current fallback, and `gpt-transcribe`; E10 performs the paid fidelity/latency/coverage/cost comparison and owns any route migration;
- decide live captions from a real UX requirement. If enabled, add `gpt-live-transcribe` as provisional-only with reconciliation and separate metering;
- build one shared web/Expo composer-dictation component that transforms the existing input rectangle in place: real input-level waveform, X/Delete, Stop, Send arrow, literal `Transcribing` state with compact progress, final-only text post, editable failure fallback, transient encrypted audio, bounded company vocabulary/language hints, and exactly-once message IDs;
- implement the `AudioFocusCoordinator` and generation fences for `composer_dictation`, `personal_realtime`, and `meeting_media`; personal Realtime must terminate before Dictate or room join begins, and an in-room dictation must privately mute/restore the room track while excluding dictated frames from meeting evidence;
- build the exact `gpt-realtime-2.1` contract/canary harness against the current `gpt-realtime-2@high` event schema with deterministic fake and recorded-event adapters; E10 performs the paid high/low-effort comparison and changes one variable at a time;
- add a separate backend WebSocket Realtime session primitive for a synthetic one-specialist consultation behind a hard kill switch; keep provider credentials server-side and bind session/audio/tool events to one invitation, short-lived runtime principal, and metering record. E10 opens the first real specialist provider session only after Scout's live route passes;
- add `MeetingAgentFloorController` generations and leases: one agent speaker at a time, human barge-in priority, no synthesized-agent-audio feedback, no autonomous agent turn chain, distinct verified agent track, bounded cancel/dismiss teardown, and zero effect on human media when the specialist lane fails;
- finish actorized Pion lifecycle, media-generation fences, cleanup, retransmission/recovery, screen-share, speaker/expanded/gallery modes, and mobile/native behavior;
- prove guest redemption, green room, consent, listen-only, revoke/expiry eviction, and cost/connection caps;
- bind guest-link identity into redeemed sessions and evict active sockets/seats immediately on revoke or expiry, with journal/replay proof;
- activate guest-safe sitting policy on first guest admission, not merely an unused link;
- add named-room `catch_me_up`, require an authenticated requester for private recap, and forbid fallback from a private audience to the room;
- publish per-room Scout, STT, transcript, consent, and media health rather than relying on office/global health;
- retain Pion as rollback; evaluate managed media only through the same gate.

**Token-free gate:** every deterministic E2 state, authorization, ordering, cancellation, audio-focus, room-isolation, failure, kill-switch, and recorded-event contract passes; the specialist harness proves separate backend session binding, one-agent floor leases, human interruption, teardown/metering, no acoustic feedback, and no human-media impact; screen share/rejoin/crowded/mobile cleanup pass in local/simulated functional cases. Live transcription accuracy/latency, Realtime behavior, physical-device performance, and paid-provider compatibility remain explicitly pending E10 and cannot be represented as E2 provider acceptance.

**Rollback:** previous transcript model, Realtime model/effort, and actorized Pion are single-pointer/new-sitting rollbacks. No model switch hot-swaps an active session.

### E3 — Temporal meeting intelligence and restart-safe brain

**Outcome:** STRIDE can answer exact live and historical questions from evidence even when a projection lane is behind.

**Work:**

- build event-driven rolling analysis over authoritative finalized turns;
- maintain typed current meeting state for decisions, commitments, blockers, storylines, alignment, positions, questions, entities, artifacts, and work candidates;
- store exact analysis window and `through_segment_id` high-water;
- implement 5-minute, 30-minute, explicit-clock, topic-relative, and before-admission temporal queries;
- implement late-join recap from first-admission watermark and half-open intervals;
- add transcript-first fallback when analysis is stale;
- add full authorized inventory, hybrid retrieval, claim/evidence edges, honest coverage, and continuation for bounded limits;
- rebuild brain projections deterministically across restart, correction, supersession, revoke, retention, and purge;
- support evolution/contradiction/position queries without hiding prior states;
- expose separate transcript, analysis, embedding, brain, and recap freshness in readiness/capabilities.
- build the ACL/consent-first specialist context assembler over exact authorized transcript revisions, structured meeting analysis, company/project evidence, active approved-work state, source refs, high-waters, gaps, and budgets; historical raw audio and unrestricted brain retrieval remain excluded;

**Gate:** at least 40 meetings/90 days/250 records; exact DST/calendar/join-relative windows; 100% asserted-claim evidence resolution; mixed fresh/stale/missing/failed coverage; raw fallback proof; per-principal ACL differential; repeat rebuild identical IDs/checksums; live late-join and 5/30-minute founder test; specialist context-envelope fixtures expose only the approved transcript interval, analysis, brain evidence, and work state, block every audience/consent/purge mismatch, and report exact freshness/gaps.

**Rollback:** projection reads off; authoritative transcript and existing read-only recall remain. Checkpoints cannot advance past failed source windows.

### E4 — `#team`, artifacts, and collaboration memory

**Outcome:** The company’s primary group conversation compounds into useful, correct, inspectable shared memory, while desktop private and channel chat becomes as polished and delightful as the protected native experience.

**Work:**

- project public `#team` events into links, files, decisions, commitments, storylines, entities, aliases, and work candidates;
- preserve canonical author principal, display name, reply ancestry, reaction actors, safe attachment metadata, and channel-policy revision in the context projection; retire flat anonymous-human history for shared-channel Scout turns;
- replace substring mention detection with one shared lexical/token-boundary parser and prove quoted/metadata/file/GIF content cannot summon Scout;
- add safe link preview and artifact index with author/channel/time/project fields;
- implement structured author/type/channel/time filtering before semantic ranking;
- implement thread-scoped catch-up, Ask the Thread, cross-source chips, deposit rail, and source navigation;
- replace post-hoc phrase-overlap citations with retrieval-issued evidence IDs/revisions for new Scout answers; under-claim visibly when no source qualifies;
- complete native/table delivery quality: locked-device push and receipts, unread boundary, mention deep link, previews/mute, reactions, photos/files, link cards, append-not-refetch, long-thread performance;
- rebuild the desktop private/channel chat presentation as a first-class product surface: strong conversation hierarchy and width use, persistent but unobtrusive thread context, a refined sticky composer, clear authorship/grouping/time/unread/reply/reaction behavior, polished hover/focus/press/keyboard states, and complete loading/empty/error/degraded experiences;
- render rich media natively in the desktop conversation flow—responsive image/GIF previews, safe link cards, file/PDF cards, artifact/result cards, source/provenance and download/open actions—with stable aspect ratios, bounded dimensions, authorization on open, accessible fallbacks, and no provider-origin client fetches;
- establish frozen native iPhone/iPad interaction and screenshot baselines before shared API/component work; run desktop changes through isolated CSS/DOM paths where possible and require the native dictation, attachment, send, scroll, unread, deep-link, haptic, and performance suites to remain unchanged;
- perform evidence-captured desktop QA through ordinary navigation at 1024/1280/1440/1728 px in current Chrome, Safari, and Firefox, including long/short threads, private/public/project contexts, keyboard-only use, reduced motion, ≥200% zoom, and responsive boundary cases;
- add explicit team norms and collaboration-profile projection, inspection, correction, expiry, and forget controls;
- add server-minted `find_files` selection handles and `post_file_to_thread` with source-revision/destination-audience reauthorization; never pass a client-supplied blob ref as authority and never widen access implicitly;
- replace user-derived provider identifiers and client-side provider media fetches in both the human picker and agent path; expose the G-rated search foundation through one shared privacy-compatible server boundary, with Scout's bounded `search_gif` tool additionally using abstract safe intents, no stable user/company identifier, server-side fetch/validation into an immutable STRIDE blob, a sensitive-context deny policy, one-result cap, alt/provenance, delete/report controls, licensing/attribution approval, and desktop/mobile parity;
- add channel-level controls for agent GIFs, proactive assistance, memory/work sensing, and specialist membership;
- reserve an explicit “save for the record/share with project/company” conversion for private chat, but keep `private_share` rejected through E1-E9: an object/event reference is not share authority. A later activation requires one atomic, durable, revision-bound, revocable grant protocol plus its own consent, race, restart, purge, and source/destination reauthorization evidence;
- teach deletion/edit/revocation to retract or supersede knowledge, digests, link records, and proposals.

**Gate:** every `#team` retrieval, coworker-behavior, desktop-chat, and rich file/GIF target in §13.1 passes; desktop private, public, and project-thread scenarios receive a signed product/design acceptance receipt from rendered Chrome/Safari/Firefox evidence at all registered widths; the frozen iPhone/iPad visual and interaction corpus has zero regressions; meeting query finds the exact Erick link from last week and posts it with a tappable source; file-drop resolves and posts only an exact already-shared revision; edit/revoke removes access; an unauthorized guest cannot learn that either item exists; private-chat canaries remain absent; `private_share` fails closed until the atomic-grant work above is separately approved and proven; source chips reauthorize on open; locked iPhone receives and deep-links to the target; 2,000-message thread remains responsive; each E4 kill switch is exercised and blocks new model/tool actions without breaking human chat or historical ACL-correct rendering.

**Rollback:** independently disable public-channel projection, cross-surface retrieval, Scout file search/posting, and agent GIF search/posting. Human `#team` and ordinary ACL-correct attachment uploads remain available. The human GIF picker remains available only through the privacy-compatible shared server boundary; otherwise it is disabled too. Historical Scout rich messages remain inert audited records whose file/GIF parts are still ACL-filtered and served only from retained authorized blobs.

### E5 — Scout as an informed company participant

**Outcome:** Scout feels present and context-aware without becoming intrusive, confusing, or unsafe, and can explain and coordinate the currently enabled workforce without gaining human hiring authority.

**Work:**

- implement surface-aware addressability and visible meeting engagement state;
- register Scout's first human-authored `AgentCoreProfile`, validated capability manifest, channel norms, and relationship-memory inspection/correction surface;
- give Scout a read-only typed workforce view over the currently enabled profiles and bounded tools to explain eligibility, assignments, health, budget, and blockers and draft—but never confirm—future roster changes; the Marketplace/hire lifecycle remains disabled until E8;
- build the server-side `AgentContextEnvelope` for private, `#team`, project, meeting-text, and meeting-voice surfaces; bind author identities, thread ancestry, evidence, active work state, tools, profile version, audience, and freshness without raw brain dumps;
- add meeting Ask Scout button, text mention, spoken-name detection, follow-up window, dismiss/timeout, and barge-in;
- connect Realtime to server answer/context envelopes rather than raw brain dumps;
- implement the member-only Scout-chaired specialist invitation experience: spoken/text request, eligible-human confirmation, context disclosure, verified-agent roster/join/active-speaker states, Scout's source-grounded handoff, one specialist at a time, human interruption, natural “thanks Mary”/Remove dismissal, and honest failed-to-join behavior;
- integrate `MeetingSpecialistContextEnvelope` with the E2 server session and floor controller; keep Scout and the specialist in separate Realtime sessions, pass structured text/context rather than synthesized audio between them, and prohibit free-form agent-agent conversation;
- qualify E5 with one human-authored, internal-only, allowlisted pilot specialist manifest and no side-effecting tools; broad production enablement of Mary/Marketing or any other additional profile waits for its E8 profile, voice, continuity, cost, and rollback receipts;
- add rich meeting-chat publication for links, artifacts, decisions, and evidence;
- preserve public-channel `@scout` gate and private/direct always-answer semantics;
- implement the response-mode policy for conversational text, text plus one GIF, GIF-only, file/artifact card, or safe refusal; preserve Scout's personality while deterministic sensitive-context and authority gates remain final;
- implement audience-safe speech/post checks and private fallback;
- make proactive suggestions quiet, deduplicated, rate-limited, and dismissible;
- expose listening, transcribing, thinking, answering, degraded, and privacy states truthfully;
- retain typed and touch equivalents for every voice path;
- perform rendered desktop, mobile, accessibility, reduced-motion, and actual device QA.

**Gate:** every Scout invocation/latency/audience, live-specialist consultation, `#team` coworker-behavior, rich-action, and identity-continuity target in §13.1 passes; plain team/meeting chatter does not summon Scout outside the permitted false-response ceiling; spoken/text/button invocation, visible follow-up, in-character multi-person reply, authorized source opening, and AI-off typed/navigation fallback pass their functional cases; specialist invitation/context/tool/audio kill switches each leave human media and Scout's verified fallback usable; the E5 coworker-context/personality kill switch restores the verified bounded-Q&A route with no new relationship-memory reads.

**Rollback:** independently disable specialist invitation, context, tools, and audio, revoke active specialist sessions/tokens, remove their tracks, and retain attributed historical contributions as inert audited evidence. Disable room voice invocation, enriched coworker/profile routing, relationship-memory injection, and rich response modes as needed; retain typed `@scout`/private Scout on the last verified bounded Q&A route. Revoke new Scout capability tokens, preserve historical messages as inert audited records, and keep passive transcript/analysis and human media independent.

### E6 — Suggested Work and durable outcome orchestration

**Outcome:** A real work outcome heard in conversation becomes one safe, understandable, approved, traceable TeamAgent assignment and run in the right project home.

**Work:**

- implement WorkIntent detection on authoritative evidence only;
- admit attributed `MeetingAgentContribution` records to WorkIntent detection only through the ordinary authorized projection lane; a specialist idea or Scout dismissal never creates approval or a run;
- build confidence/counterevidence/dedupe evals and relevant-person selection;
- render revisioned Suggested Work cards with evidence, destination, owner/reviewer, authority, budget/time, expiry, and dismiss reason;
- implement project/thread resolver and explicit new-thread creation flow;
- consume one approval atomically into one idempotent WorkRun;
- implement durable stage/checkpoint/retry/awaiting-input/review/blocked/cancel states;
- implement named-specialist manifests, membership, verified-agent rendering, short-lived runtime principals/capability tokens, and typed `DelegationRun` handoffs with zero transitive hops by default;
- bind every specialist handoff to an `AgentAssignment` and current TeamAgent/capability revision when a hired roster exists; before E8, use the same contract with the single allowlisted Insights/pilot principal so later Marketplace activation does not fork orchestration semantics;
- adapt/retire broad legacy `codex_proposal` broadcast, any-member confirmation, and public/`#general` fallback semantics behind WorkProposal recipients, destination membership, and canonical approval;
- add cross-process leased WorkRun/job claims with fencing tokens before more than one runner replica exists;
- add per-stage model route and capability snapshot;
- bind every artifact/result to run, evidence, destination, and source permissions;
- report progress/completion in the bound thread and project/company brain;
- enforce Scout as coordinator/front door: durable specialist work posts only in bound work/project threads under the specialist's own identity and returns one typed result; the §4.17 live-consultation exception remains attributed conversation evidence and never schedules work through mentions, thanks, or free-form sibling chatter;
- support typed feedback, correction, re-run-from-new-revision, and proposal quality analytics;
- keep all external/destructive actions behind separate current approvals.

**Gate:** every Suggested Work precision, recall, destination, noise, hard-negative, approval-race, and coworker identity/handoff target in §13.1 passes before E7 can start; invalid or revoked manifests fail closed; self/sibling/transitive social mentions launch no work; hop/tool/time/cost limits hold; edits/revokes invalidate proposals; wrong-project/public-fallback cases require human choice; restart resumes once; ambiguous provider effects do not repeat; rejected/dismissed reasons improve offline evaluation without silently retuning production.

**Rollback:** disable detector/suggestion surfaces and new specialist handoffs; revoke affected specialist manifests, runtime principals, and capability tokens; fence active specialist stages into honest `blocked` or `cancelled` states; preserve their messages/artifacts as inert audited records. Explicit work can return to Scout or non-social system-role execution only through a new revision and human reapproval. Existing runs remain readable and terminally honest.

### E7 — Insights & Opportunities v1

**Outcome:** The one first-class workflow repeatedly creates a decision-ready product that improves from reviewed feedback and produces the first marketplace-ready, evidence-backed coworker package.

**Work:**

- finalize versioned request, evidence, opportunity, report, critic, feedback, artifact, and outcome schemas;
- select and human-approve the first named Insights analyst profile; bind every visible contribution, artifact, model/runtime revision, and feedback item to that profile and the WorkRun while Scout retains coordination;
- seal the passing Insights profile, capability manifest, eval bundle, example artifact, cost evidence, and update/rollback metadata as the first verified `AgentPackageManifest` and draft `MarketplaceListing`; keep marketplace discovery/hiring disabled until the E8 lifecycle/security gate passes;
- define report sections, claim standards, confidence/counterevidence, expected impact, next action, owner, and decision status;
- assemble the internal workflow profile using appropriate goal ownership, strategic design, staged execution, and bounded criticism;
- pin orchestrator, researcher/subagent, writer, critic, and verifier routes/reasoning;
- pin the verified incumbent route for each E7 pilot unless that exact seat has already passed its authorized canary; record the route map in every run receipt;
- keep source snapshot immutable and create immutable report revisions;
- require criterion/claim-level critic verdicts and evidence links;
- make feedback easy from the report/thread and connect accepted corrections to the next workflow version/eval set;
- produce printable/shareable artifact only within approved workspace authority;
- prepare ten immutable pilot fixtures, reviewer rubrics, deterministic fake-stage outputs, and the complete launch/review/feedback/revision harness. E10 runs the ten paid-model pilots with at least two eligible reviewers and real authorized company inputs.

**Token-free gate:** authorization, evidence binding, immutable revisions, duplicate-launch idempotency, restart/resume, bounded criticism, blocked/rejected visibility, feedback lineage, package sealing, and zero external-write paths pass on frozen deterministic fixtures. The ≥8/10 human acceptance, model-groundedness, latency, and accepted-output-cost gates remain pending E10.

**Rollback:** disable `BONFIRE_INSIGHTS_OPPORTUNITIES_V1_ENABLED`; preserve runs/artifacts/feedback for audit and later replay.

### E8 — Agent Marketplace, workforce growth, routing, and economics

**Outcome:** People can safely hire, understand, shape, work with, update, pause, and offboard a distinctive roster of organization-owned agent coworkers; Scout coordinates them as a grounded chief of staff; every active seat remains intentionally chosen and cheaper routes are used only where quality evidence permits.

**Work:**

- implement immutable package ingestion, closed-schema/provenance/dependency validation, quarantine, capability-request mapping, eval bundles, curated listing review, suspension/revocation, and one-pointer package rollback; accept only STRIDE-authored and organization-authored packages in this plan;
- build the responsive web/Expo Agent Marketplace with outcome/category search, personality preview, evidence/sample results, required-access and cost summaries, package/publisher/version provenance, **Try on a sample**, and revisioned **Hire to team**; no third-party payments, seller accounts, rankings, reviews, or telemetry;
- add an admin-only **Create from template** path for organization-private agents using closed persona/role/assets/capability-request fields, preview, validation, and the same quarantine/eval/curation lifecycle; it accepts no arbitrary code, command, hook, environment value, credential, or raw MCP configuration;
- build the team roster and coworker detail surfaces for direct chat, responsibilities, assignments, channel/project membership, access, memory, skills, feedback/growth, activity, health, cost, versions, profile preview/diff, correction/forget, pause, and offboarding;
- implement idempotent TeamAgent trial/hire/activate/pause/quarantine/offboard lifecycles, tenant-local runtime identity, direct thread, local profile overlay, workforce-policy authorization, short-lived principals, budget/concurrency/proactivity limits, and historical-attribution preservation;
- implement source-linked relationship/domain learning and reviewed performance receipts; keep the company brain shared, propagate correction/revoke/purge, block sensitive inference, and require a human-approved capability revision plus eval receipt before competency growth changes eligibility;
- implement opt-in package/profile/capability/runtime updates with semantic permission/personality/model/cost/migration diffs, quarantined trial, continuity/capability/security evals, local-overlay/memory compatibility, explicit human activation, and rollback;
- implement Scout's chief-of-staff tools for eligible-agent search, roster/status/budget Q&A, draft hire and assignment recommendations, WorkRun routing, introductions, progress/blocker monitoring, coaching/profile proposals, and pause/offboarding recommendations; deterministic eligibility and authority precede model ranking;
- stage the E7 Insights package/listing plus Marketing/Mary, Research, Design, and Builder packages as unavailable candidates; validate every token-free package, identity, authority, memory, lifecycle, rollback, and applicable simulated floor contract. E10 qualifies and admits each listing or capability variant one at a time only after its live task, cost, continuity, and voice receipts pass;
- activate E8 in fixed order: read-only catalog -> Insights trial/hire lifecycle -> update/pause/offboard/export -> Scout workforce coordination -> Marketing/Mary -> Research -> Design -> Builder -> final route/economics changes. Do not combine a new package/profile/capability activation with a model-route change in the same canary, and do not advance a later profile past a failed earlier lifecycle control;
- freeze corpora and baseline receipts for STT, voice, extraction, recall, board/suggestion operations, proposal routing, I&O generation, critic, and Codex work;
- validate every configured model ID and supported parameter at startup/canary time;
- prepare one-seat-at-a-time Luna/Terra/Sol, Realtime, and STT canary definitions from §11 with exact route/prompt/schema/corpus digests and rollback pointers; E10 runs them and never changes more than one variable at a time;
- prepare paired current-versus-lower reasoning, prompt-cache/persisted-reasoning, Programmatic Tool Calling, and bounded Responses multi-agent experiments; E10 executes only the experiments whose surrounding deterministic safety and accounting gates pass;
- prepare deterministic Marketing, Research, Design, and Builder coworker fixtures one package/profile/capability at a time for explicit consultation or approved project-thread work; require token-free authority, artifact, budget, learning, export, offboard, and no-agent-loop receipts now, with live capability/personality/voice qualification deferred to E10;
- prepare the invited-specialist voice experiment separately from each profile's durable work route, including Realtime-only and optional bounded Terra `medium` preparation variants; E10 measures useful-idea rate, latency, interruption, context fidelity, and accepted-output cost before enabling either variant;
- retain independent review provider and shadow any proposed critic change;
- complete accepted-output cost, price tables, console reconciliation, spend/fallback/staleness alerts, and per-workflow budgets;
- prepare the 24-hour/ten-sitting soak harness and final-route replay manifest. E10 runs both after the last qualified route change.

**Token-free gate:** package/listing/trial/hire/assign/learn/update/pause/offboard/export lifecycles are idempotent, permission-safe, purge-correct, default-off, and rollback-proven; deterministic staffing/authority fixtures pass; no seat, listing, hire, capability, memory, or visible profile changes on missing/invalid receipts; one package/profile/manifest/token/kill-switch rollback is demonstrated per candidate coworker. Live quality, personality continuity, voice, latency, accepted-output cost, provider-ledger reconciliation, final route changes, visible listing admission, and integrated replay remain pending E10.

**Rollback:** disable Marketplace discovery/trial/hire/update and Scout workforce mutations independently while preserving read-only roster/history; suspend affected listings; restore the prior package/profile/capability and route revisions; pause or offboard each E8-enabled Marketing, Research, Design, or Builder TeamAgent; revoke runtime tokens, stop new consultations/handoffs, and remove any active specialist audio track. Active affected stages fence into honest `blocked`/`cancelled`; resumption through Scout, the Insights profile, or a prior system role requires a new approved revision. Historical agent contributions/messages/artifacts remain inert and attributable; local overlays and compatible memory remain available for rollback rather than being silently discarded.

### E9 — Resilience, native acceptance harnesses, and launch readiness

**Outcome:** Every token-free operational, isolation, security, native, recovery, and launch mechanism is implemented and deterministically exercised without claiming that the provider-integrated system is live-qualified.

**Work:**

- implement the configuration, automation, manifests, validation, and fail-closed controls for managed PostgreSQL HA, recurring encrypted immutable offsite object storage, independent signing/KMS custody, and a durable separate restore host; actual account provisioning and authority-sensitive activation remain externally gated;
- complete purge-authority-preserving four-root backup/restore and deterministic RPO/RTO drill harnesses; E10 runs the qualifying external drill;
- add redundant app/TURN/routing definitions and health-based reversible traffic-shift controls without changing live traffic;
- keep media routing independent enough that one app/provider failure does not end active calls;
- add capability-specific paging and operator runbooks for canonical, consent, transcript, analysis, brain, embeddings, Scout, workflow, queue, backup, and cost;
- complete native signing/privacy manifests plus automated device-test manifests and local/simulator acceptance for `#team`, dictation, video, screen share, push, and background/foreground recovery; TestFlight upload and physical-device acceptance remain E10 operations;
- execute security review: route allowlists, guest DoS, SSRF/link previews, blob authorization, CSRF/origin, capability tokens, secrets, worker isolation, retention/purge, agent-package/listing supply chain and prompt injection, update/export/offboard isolation, and dependency/vulnerability scans;
- run workers in per-run ephemeral worktrees/containers with bounded writable mounts, egress allowlists, short-lived scoped credentials, CPU/memory/time/network quotas, signed callbacks, and no company-brain or production-volume mount;
- build an exact-SHA release candidate and prove source/image/manifest/backup identity locally without deploying it;
- prepare the integrated founder script and 24-hour/ten-sitting soak harness;
- exercise repeated simulated Scout-plus-one-specialist consultations in concurrent rooms, consent withdrawal, quota exhaustion, Realtime disconnect, participant churn, restore/failover, and specialist kill-switch drills; one specialist failure must never end or cross-contaminate a human call;
- exercise the integrated workforce founder script against deterministic fixtures: discover and inspect Mary, trial, hire with bounded permissions/budget, direct-message her, add her to Dog Perfect, have Scout choose and introduce her, invite her into a simulated meeting, approve one WorkRun, inspect/correct a learning record, preview and roll back a profile/package update, pause/offboard, and prove that history remains attributable while all new access is revoked.

**Token-free gate:** every E9 deterministic resilience/native-readiness target in §13.1 passes; simulated restore/failover and purge rollback fail closed; two-room/media/recall/#team/Scout/Suggested Work/I&O/Agent Marketplace/workforce fixtures pass from one candidate tree and frozen placeholder route map; no unauthorized observation, stale capability, or suspended package is presented as healthy/available. External signed restore, physical-device acceptance, paid routes, deployment, live traffic shift, and integrated soak remain pending E10.

**Rollback:** reversible traffic shift to retained app/media release, previous route map, and authoritative data path; never restore content behind purge authority.

### E10 — Paid-provider qualification, integrated acceptance, and launch

**Outcome:** Convert the deterministic E1-E9 candidate into one evidence-bound, provider-qualified, production-accepted STRIDE release.

**Entry gate:** before any real company/user corpus, provider-seat qualification,
route migration, or production activation, the production provider project has
bounded usable quota; current official model availability, parameters, event
schemas, and price tables are reverified; immutable offsite retention,
independent key/signing custody, durable consent authority, authenticated signed
restore, Apple/device access, and every applicable business/legal/product
approval in §16 are current. No paid call begins merely because code is ready.
A separately authorized single synthetic contract observation may precede the
full launch prerequisites only under the narrow exception in the E10 provider
runbook: generated non-sensitive input, exact candidate/probe/price/budget
binding, one-attempt stop, no fallback or mutation, and evidence permanently
classified as `provider_contract_attempt`. An unreconciled project, synthetic
input, or contract receipt can never satisfy this entry gate for qualification
or activation. This is the exception used for the bounded 2026-08-01 contract
attempts; it is not a retroactive qualification waiver.

**Ordered stop/resume queue:** this sequence is dependency-bound. A later item cannot be used to waive an earlier one.

1. Freeze one local E1-E9 candidate and complete the deterministic normal, race, dependency, native/simulator, vertical-founder, failover/restore, authorization, and independent-critic gates.
2. **STOP before every paid provider call, production mutation, Git integration, release, or deployment.** Resume only after the user confirms API quota/top-up and explicitly authorizes the applicable external operations.
3. Reverify current official model access, endpoint/event/parameter compatibility, project attribution, reasoning controls, price tables, accepted-versus-rejected accounting, and bounded spend ceilings.
4. Run the smallest exact provider probe per seat. Stop the affected lane on the first schema, policy, authority, pricing, quality, or ledger failure; do not hide it behind a fallback.
5. Qualify authoritative meeting STT and composer dictation first; then personal/meeting Scout on `gpt-realtime-2.1`; then the separate invited-specialist Realtime voice lane. Optional `gpt-live-transcribe` ships only if a live provisional transcript consumer still justifies its extra lane.
6. Qualify transcript analysis, temporal/company recall, `#team`, Suggested Work, I&O, Scout coordination, and Marketing/Research/Design/Builder seats one model, effort, prompt, or tool-policy variable at a time.
7. Freeze the final route map, rerun every affected downstream corpus, run ten real I&O pilots with two eligible reviewers, and run the qualified founder/workforce flows.
8. Complete the external immutable-custody, signed authenticated restore, production failover, TURN/real-WebRTC, physical iPhone/iPad/TestFlight, privacy/consent, and owner-approval gates.
9. Only then reconcile an intentional release scope excluding `stride-site/` and unrelated user work; bind it to an exact commit/tree/build/image/config identity; commit/push/deploy when expressly authorized; observe production, prove rollback, run the 24-hour/ten-sitting soak, and selectively activate only passing cohorts.

**2026-08-01 E10 contract checkpoint:** the user confirmed quota and authorized use of the existing OpenAI key for bounded provider checks. The independently gated harness froze candidate manifest `ffbb8ad389afdced67162cdb4987d64d6406eeb7c0ef2c235f30e3885440df26`, probe binary `b7c7e984c4489e8d5113f3b83e29d3231f2b3b1a2377b198034c21718a95d699`, a 5.509-second synthetic WAV, and its reference digest. Zero-generation model-object access passed for `gpt-realtime-2.1`, `gpt-transcribe`, `gpt-live-transcribe`, `gpt-5.6-luna`, `gpt-5.6-terra`, `gpt-5.6-sol`, `text-embedding-3-small`, and `gpt-image-2`. One `gpt-transcribe` file request passed the exact multipart and strict usage-union contract: HTTP 200, 1.647-second request latency, provider-reported 6.0 seconds, computed cost USD 0.00045 under a USD 0.001 admission ceiling, and non-empty output retained only as a hash/character count. The API returned no documented project echo and the connected Platform helper required reauthentication, so receipts truthfully record `project_scoped_api_key` / `project_credential_bound_unreconciled`; this permits contract observation only and cannot satisfy intended-project/billing reconciliation or any qualification/activation gate. The ten sanitized receipts and their original-file digests are in `docs/evidence/e10/openai-contract-receipts-20260801.jsonl`. No production, Git, route, configuration, feature, device, or release mutation occurred.

**2026-08-01 E10 Realtime continuation checkpoint:** after the initial response-shape failure recorded in `openai-realtime-contract-attempt-20260801.jsonl`, one authorized corrected transcription attempt exposed the current modern `conversation.item.added` → `conversation.item.done` lifecycle; the harness was corrected and independently re-gated. A freshly frozen synthetic committed-turn attempt then passed on `gpt-transcribe`: HTTP 101, 26 correlated events, exact commit/created/finalized/completed item identity, 71 normalized transcript characters retained only as a hash, 1.007-second transcription latency, 2.152-second total latency, six provider-reported seconds, and USD 0.00045 under the USD 0.001 ceiling. This is a narrow endpoint/event/correlation/accounting contract pass, not WER, quality, device, or seat qualification.

The first Scout contract attempt observed only `session.created` and `session.updated`, then timed out because the frozen harness incorrectly required optional `conversation.created`. After a local fix, fresh freeze, and independent gate, the user authorized exactly one retry. That retry reached partial transcript/audio generation and the assistant final-item boundary, but the 48-token/high-reasoning response ended with a documented incomplete item that the then-frozen parser rejected before `response.done`; therefore provider usage/cost was not reconciled and the zero output/token/cost fields in that legacy raw receipt are not proof of zero output or spend. The retry is a failed-closed contract attempt, not a provider incompatibility and not a Scout pass. The local harness now accepts documented completed/incomplete item states, treats `response.done.status=completed` as the sole success authority, preserves partial-output evidence, records explicit usage/cost truth, accepts current optional usage and failure-detail fields, and raises the bounded output cap to 256 with a conservative USD 0.018432 worst case under the USD 0.02 admission ceiling. Exact source/test hashes passed focused normal 20×, focused race 3×, package normal/race, vet, formatting/diff, current official-contract review, and an independent Critic; it has not been provider-retested. The four exact raw receipts, candidate bindings, fact/inference sidecars, and local correction are durable in `docs/evidence/e10/openai-realtime-contract-final-20260801.jsonl` (SHA-256 `4625652b65c39ea2673a9687cb54d66f5fa74cf20a4dccb8188008283215c69f`). Project/billing attribution remains unreconciled, no lane is `provider_qualified`, and no production, route, configuration, Git, feature, device, or release mutation occurred.

**Work:**

- run the smallest exact contract probe for each configured OpenAI, Anthropic, and Codex adapter, stopping on the first schema, policy, authority, pricing, or accounting failure;
- qualify authoritative meeting STT and composer dictation on the frozen audio corpus, then optional live transcription only if a shipped provisional consumer still justifies it;
- qualify personal and meeting Scout on `gpt-realtime-2.1`, one effort and one newly created session cohort at a time, preserving the incumbent new-session rollback;
- qualify transcript analysis, temporal/company recall, `#team` responses and rich-action judgment, Suggested Work, I&O, specialist voice, workforce coordination, and every candidate coworker against the preregistered corpora and human-review gates;
- change Luna/Terra/Sol/critic/Codex seats one at a time; bind exact model, effort, prompt, schema, route, price, release, corpus, accepted-output, and rollback receipts; rerun every impacted downstream corpus after the final immutable route map;
- run the ten real I&O pilots, specialist and workforce founder flows, physical iPhone/iPad acceptance, qualifying four-root restore/failover drills, exact-SHA deployment, production observation, and the 24-hour/ten-sitting soak;
- enable only the capability/listing/route cohorts whose complete live receipts pass. Keep every failed or untested lane honestly unavailable and independently kill-switchable.

**Gate:** every applicable target in §13.1 and product promise in §15 passes from one immutable release and final route map; all asserted claims resolve to authorized evidence; provider and internal ledgers reconcile; no missing price, stale capability, unauthorized disclosure, duplicate side effect, acoustic agent loop, or hidden fallback remains; exact-SHA launch and retained-release rollback are both proven.

**Rollback:** disable or restore one route, capability, profile, package, listing, or new-sitting pointer at a time; fence active durable work into an honest terminal/blocked state; use reversible traffic shift to the retained exact release; never roll back past purge authority or replay an ambiguous external effect.

---

## 15. Final acceptance evidence matrix

| Product promise | Required evidence |
|---|---|
| best-in-class calls | 2×3-person two-hour rooms; gallery/speaker/expanded/screen share; packet loss/disconnect/CPU/RSS; rejoin, cleanup, browser/native mixed rooms, guest, restrictive-network TURN, Bluetooth/audio-route change, camera switch, background/foreground/lock, multiple devices on one account, and induced AI failure |
| trustworthy transcript | representative audio corpus; WER/domain/numeric/code-switching; p50/p95 final latency; out-of-order completion; speaker/consent/capture mapping; correction and gap accounting |
| best-in-class composer dictation | shared mic experience across all composers; existing input transforms in place; real waveform; explicit X/Delete, Stop, and Send arrow; literal `Transcribing` progress state; final-only text post after Send; Delete/error/empty safeguards; company-term fidelity; exactly-once retry; personal Realtime hangup before Dictate/room join; private in-room dictation; physical web/iPhone/iPad proof |
| five/30-minute recall | exact source-time slices, raw fallback during analysis lag, evidence links, coverage/high-waters, DST and topic-relative cases |
| late-join magic | first-admission exact interval; concise spoken recap plus decisions/commitments/blockers/topic/questions/artifacts; source chips |
| `#team` compounds | exact Erick-link retrieval; canonical author/reply/reaction context; catch-up/deposit rail; author/time/channel filtering; edit/delete/retract; long-thread performance; locked-device push |
| Scout feels like a coworker | stable core/personality and verified identity; multi-person conversational context; lexical mentions; in-character text/GIF judgment; visible memory sources/correction; no uninvoked replies, persona drift, impersonation, or sensitive inference |
| rich `#team` actions | exact-revision Files search/drop; source and destination ACL intersection; no silent sharing; revocation/open reauthorization; restrained one-GIF responses; G-rated/provider/privacy/alt/provenance controls; desktop/mobile parity |
| learns how people work | explicit and inferred low-risk preferences, sources/confidence/expiry, correction/forget, no sensitive inference/private leakage |
| Scout participant | surface invocation matrix, visible engagement, natural follow-up/barge-in, low false response, meeting-chat cards, audience-safe speech |
| Scout can bring in a specialist | member-only explicit invitation and disclosure; server-built transcript/analysis/brain brief; distinct verified agent roster/audio; one-agent floor lease; human interruption; natural dismissal and terminal usage; zero acoustic loops, unauthorized context, guest enablement, or human-media impact on failure |
| people can hire and grow an agent team | curated outcome-first Marketplace; verified package/listing provenance; idempotent trial/hire/direct-thread/assignment/update/pause/offboard; organization-owned identities and local overlays; distinct personalities with continuity; evidence-backed relationship/domain/competency learning; inspect/correct/forget/purge; explicit permission/cost diffs; no package code/credential inheritance, seller access, silent update, memory export, or self-granted capability |
| Scout acts like a chief of staff | typed roster/status/budget/performance context; deterministic eligible-agent filtering; explainable recommendations; human override; bounded assignment/handoff; blocker/escalation truth; Scout drafts but cannot confirm hire/access/update/offboard changes |
| proactive but controlled work | labeled detector corpus, counterevidence, relevant recipients, revisioned approval, no silent launch, project destination tests |
| durable coworker workforce | stable named identity and runtime provenance; explicit Scout handoff; capability token/tool/hop/time/cost boundaries; no social-mention launches or agent loops; crash/restart/idempotency; status truth; artifact lineage; external-write gates; completion against original criteria |
| I&O quality | first named Insights coworker plus Scout coordination; ten fixed-release pilots/two reviewers; ≥8 accepted in ≤2 revisions; all asserted claims sourced; zero unauthorized/invented claims |
| intelligent routing | per-seat corpora, exact model/effort/prompt versions, quality/latency/cost verdicts, one-variable canaries, rollback receipts |
| 24/7 trust | canonical/consent/freshness health, spend/queue/backup alerts, encrypted offsite restore, purge continuity, HA failover, exact-SHA attestation |

Every evidence packet binds release commit, tree, image digest, config names without secret values, corpus/input digests, model/provider/effort, prompt/schema/workflow versions, timestamps, operator/reviewer, and pass/fail verdict. Synthetic provider observations cannot qualify a live gate.

---

## 16. Operations and authority queue

The current Goal Loop scope authorizes local E1-E9 implementation and
deterministic verification. On 2026-08-01 the user additionally authorized
bounded use of the existing OpenAI project-scoped key after confirming quota,
including one explicit retry after the first Scout attempt. Every recorded
provider-attempt authorization has now been consumed; no further paid provider
call is authorized by that history. The user subsequently authorized the
reviewed additive migrations, intentional Git integration and push to
`axx/main`, exact VPS release, and iOS Build 29/TestFlight submission once the
fresh local and release preflights pass. This is shipping authority for the
reviewed candidate, not authority to enable default-off/unqualified routes or
to bypass a failed provider, consent, corpus, migration, release, or owner gate.
The following still require external evidence, credentials, business approval,
or a qualifying earlier-wave receipt:

- owner/billing-console reconciliation of the intended OpenAI project ID and spend controls before any route can become `provider_qualified` or production-enabled; the successful `sk-proj` contract receipt proves usable quota for that credential but not the missing ownership reconciliation;
- business/legal approval of guest, meeting capture, model-analysis, organization-memory, raw-audio retention, and dictation disclosure copy;
- business/legal/product approval of named non-human meeting participants, per-invitation transcript/analysis/company-context sharing and retention, synthesized-voice disclosure, member-only first-release policy, eligible confirmer, natural spoken dismissal, and maximum session/turn/cost budgets;
- before E8 activation, business/product/security approval of internal Agent Marketplace curation, hiring/admin roles, trial policy, first-party package authors/publishers, per-agent budgets, direct/channel membership defaults, personality/local-overlay controls, relationship/domain/competency learning semantics, memory inspection/correction/forget/export/purge, update approval, pause/quarantine/offboarding, and the initial Insights/Marketing/Research/Design/Builder listings;
- a future public seller marketplace requires a separate Strategic Design and authority queue for publisher identity/signing, package review and revocation, licensing/IP, payments/royalties/refunds/tax, rankings/reviews, abuse/moderation, vulnerability response, support, privacy/telemetry, cross-organization reputation, regional/model-provider policy, and legal terms; none is an E0–E10 dependency or hidden feature;
- business/legal/product approval of the GIF provider's terms, content/privacy policy, channel default, and the initial human-authored Scout/Insights coworker profiles;
- an AWS account/operator credential for the selected independent backup boundary: an S3 general-purpose bucket with Versioning plus Object Lock in Compliance mode, a default retention period, SSE-KMS under a customer-managed key, CloudTrail evidence, and separate least-privilege upload, restore, retention-administration, and key-custody roles; DigitalOcean Spaces is not an acceptable substitute because the observed Object Lock API is unavailable;
- approval of the minimum recurring AWS S3/KMS retention cost, plus later managed PostgreSQL, redundant compute/TURN, cross-region replication, and independent signing costs;
- independent key/custody owners and separate restore-host access;
- Apple signing team, privacy manifest decisions, TestFlight/device availability;
- any future repository/GitHub/application rename to STRIDE.

---

## 17. Risks and controlled decisions

| Risk | Control |
|---|---|
| “learning personality” feels creepy | collaboration preferences only; evidence/confidence/expiry; visible inspect/correct/forget; deny sensitive inferences |
| personality drifts, becomes cringey, or manipulates | human-authored versioned core profile; bounded humor; channel norms; frozen social evals; learned memory cannot rewrite identity/policy; one-click correction/disable |
| “learning and growing” becomes silent self-modification | separate package/core/overlay/capability/assignment/memory/performance layers; source-linked lessons; reviewed performance receipts; human-approved capability changes; model self-assessment grants nothing |
| a marketplace listing overclaims competence | listing capabilities resolve to implemented tool/workflow roles and fixed eval receipts; samples are labeled; unavailable variants remain disabled; curator verdict and revocation; personality/popularity never substitute for evidence |
| a package update hijacks a trusted coworker | immutable version/digest/provenance; semantic personality/permission/runtime/cost diff; quarantine and continuity/security evals; opt-in activation; local overlay/memory preserved; one-pointer rollback |
| publisher/package exfiltrates company data or credentials | no package commands/hooks/env/raw MCP; abstract capability requests only; tenant-local runtime identity; isolated workers/egress; runtime ACL context; zero seller telemetry/export by default |
| hiring an agent adds it everywhere | Hire creates roster/direct thread only; channel/project/meeting membership and data/tool access are separate explicit grants; every run reauthorizes current assignment and destination |
| Scout becomes an unaccountable manager | Scout has typed read/draft/coordinate tools; deterministic policy owns quarantine; humans confirm hire/access/update/offboard; every assignment and exception is attributable and reversible |
| offboarded agent keeps acting or disappears from history | generation/token/member revocation first; active work fenced; meeting eligibility removed; historical authorship/artifacts preserved; policy-bound memory export/purge and replay proof |
| too many named agents make `#team` noisy or confusing | Scout is the sole default social coworker; specialists require explicit membership or a bound approved work thread; system workers stay non-social |
| two live voice agents talk over humans, trigger each other, or feel deceptive | Scout remains chair; one visible specialist maximum; eligible-human confirmation and persistent agent identity; server floor lease; human-priority barge-in; no synthesized-audio feedback; no autonomous agent turns; natural and button dismissal |
| specialist invitation leaks meeting or company context | member-only first release; separate non-human-participation consent; invitation shows context classes; server-built audience-intersection envelope; no raw-audio history or unrestricted retrieval; reauthorize every tool and terminate on audience/consent change |
| specialist sessions make meetings unpredictably expensive | no provider session before confirmation; hard join/idle/duration/turn/audio/token/cost caps; one specialist per sitting; terminal usage reconciliation; separate route/profile kill switches |
| stable agent name hides a materially changed model | identity remains stable only after continuity/safety eval; every response/run receipt binds profile and runtime/model revision; rollback profile route independently |
| `#team` memory breaches private chat | separate public ConversationEvent projection; raw thread kind remains UI/private; canary tests at every retrieval lane |
| Scout leaks a private file while trying to be helpful | server-minted authorized inventory; exact source revision plus destination audience intersection; no client-ref authority; explicit separate share approval; reauthorize on post/open/revoke |
| a funny GIF leaks context or lands cruelly | abstract provider query only; G-rated server filter; sensitive-context deny list; situational not targeted humor; one-result cap; channel off switch; alt/provenance/delete/report |
| live transcript conflicts with final | provisional state never durable; atomic authoritative replacement; source/evidence points to canonical revision |
| analysis costs grow with every meeting | incremental deltas, typed state, retrieval, compaction, per-seat budgets; no repeated full transcript prompts |
| Realtime becomes an authorization bypass | server context/tools only; Realtime output is conversation, never approval or durable state |
| Scout interrupts humans | explicit addressability gate, visible engagement, silence-favoring false-positive policy, proactive cards not speech |
| meeting answer leaks `#team` to a guest | audience intersection before speech/post; private fallback |
| model names/parameters drift | versioned registry, startup validation, capability-aware configuration, exact canary receipts |
| one generic runner control hides incompatible runtimes | registry declares capabilities and authority per runtime; stage planners select an eligible runner, not only a model slug |
| multiple agents increase cost/chaos | typed DelegationRun, no mention-triggered work, zero transitive hops by default, bounded independent stages, concurrency/tool/turn/time/cost caps, one synthesis return, durable STRIDE checkpoints |
| workflow executes twice | revision-bound approval, idempotency key, transactional claim/outbox, ambiguous-effect terminal state |
| new media stack creates migration risk | Pion rollback per new sitting; managed provider must beat same acceptance gate |
| healthy HTTP masks stale brain | independent capability high-waters and paging; final acceptance rejects aggregate-only health |
| local design branch overwrites live fixes | clean worktree from `axx/main`, intentional patch inventory, tests and rendered QA before merge |

---

## 18. Current wave and resume point

**Current wave:** E10 — local substate `release_integration`; external substate
`external_waiting`. E1-E9 have reached `deterministic_verified` only for the
local/default-off evidence class described in the wave table and checkpoint
above. Shipping an immutable candidate and a TestFlight build does not promote
the separately gated provider, real-corpus, physical-device, HA, custody, or
feature-activation states.

**Current owner and stop:** Goal Loop coordinator. The corrected synthetic
Realtime transcription contract passed. The first Scout attempt exposed an
optional-event harness defect; exactly one separately authorized retry was
consumed and failed closed after partial provider generation, before a valid
`response.done` could reconcile usage and cost. The post-retry correction is
locally and independently verified but has not been provider-retested. No
further paid call is authorized. Git integration, the reviewed schema
migrations, the exact VPS cutover, and Build 29/TestFlight are now authorized
behind their documented local, backup, migration, rollback, and release gates.
Do not run another provider generation call; enable a feature/listing/model
route; rename the repository/application; or claim live traffic, restore,
physical-device, provider-quality, or HA acceptance without its separate
authority and evidence.

**Frozen local checkpoint:**

1. The design lineage commits `889cf65`, `30996ca`, `a4789cd`, and `c7b4128` remain ancestors of local `HEAD`; `HEAD` and `axx/main` were both `c7b4128f0f45d1b6443c73cbae3e54feceb735d3` when the work resumed. No prior design work was reverted.
2. The 324-file E1-E9 implementation manifest excluding this ledger and `stride-site/` is `057290ab5f8ac1e0f279d50bede9cf14189c02f91c986cc2430de15cb392e617`. It is a local dirty-candidate content identity, **not** a Git commit, release, or deployable exact-SHA claim.
3. Full Go normal and race, Node/web, mobile/TypeScript, native/Xcode simulator, static/dependency, E9 vertical founder, local failover/restore, and focused authority/restart/tamper gates pass with the exact limitations recorded above. The independent final Critic verdict is the required final local sign-off.
4. A fresh read-only production audit on 2026-08-01 confirmed the current,
unqualified migration baseline: personal/private Realtime voice uses
`gpt-realtime-2` at `high` reasoning; authoritative meeting transcription uses
`gpt-realtime-whisper`; composer dictation uses `gpt-4o-transcribe`;
`MEETING_TRANSCRIPT_LANE_ENABLED=true`; and the dedicated target Realtime
transcription override remains unset. The target remains `gpt-realtime-2.1`
for conversational voice, `gpt-transcribe` for bounded authoritative
transcription/dictation, and optional `gpt-live-transcribe` only for a justified
provisional live-transcript consumer. This audit proves current configuration,
not model quality or migration readiness.
5. `private_share`, provider GIF actions, live specialist voice, visible Marketplace admission, external worker isolation, production failover/restore, and all other unqualified lanes remain disabled or unavailable. Human chat, current production behavior, and historical ACL-correct artifacts are unchanged by this checkpoint.
6. The E10 contract candidate is separately frozen as manifest `ffbb8ad389afdced67162cdb4987d64d6406eeb7c0ef2c235f30e3885440df26` with probe binary `b7c7e984c4489e8d5113f3b83e29d3231f2b3b1a2377b198034c21718a95d699`. It includes the 328 then-current in-scope dirty candidate entries and excludes this live ledger and `stride-site/`; it remains a content identity, not a Git commit or deployable release.
7. The OpenAI access matrix and one bounded `gpt-transcribe` file contract pass with private, body-free receipts. Receipt permissions, candidate bindings, and absence of raw key/project/request IDs, audio, reference text, and transcript fields were rechecked before the sanitized evidence packet was added.
8. These observations are `provider_contract_attempt` receipts only. The audio corpora remain pending live capture, the synthetic reference was bound but not used to claim WER/quality, the project ID is unreconciled, and no result has been promoted to `provider_qualified`.
9. A later 334-file Realtime-attempt candidate was frozen as manifest `3ba0394961509c535d635e6f4931efd9c0328913e7980f5ce6424a2217bb696f` with probe binary `dc24d4beacc7afac0c33142f38983560f7b8c3dafbde1e111bfef239f11239ef`. Exactly one `gpt-transcribe` WebSocket attempt reached HTTP 101 and received `session.created` plus `session.updated`, then stopped before audio append/commit because the harness rejected the acknowledgement as a schema mismatch. Under that stopped attempt and its authorization, no Scout call, retry, fallback, transcript generation, or provider-reported usage followed.
10. Diagnosis against the current official guide and `session.updated` response schema found a harness incompatibility rather than evidence of a provider rejection: the exact outbound request correctly included `prompt`, `keywords`, `languages`, and `turn_detection: null`, but the response schema has no `keywords` property and marks the returned audio configuration fields optional. The post-attempt parser now treats `session.updated` as the documented acknowledgement, accepts documented omissions, and fails on any contradictory echoed value. Focused normal 20×, focused race 3×, full package normal/race, vet, formatting, diff, and an independent Critic all pass. At that checkpoint the correction had not been used for another provider call; it was subsequently superseded by freshly frozen candidates, and this failed manifest/binary remains stale for any future attempt.
11. The sanitized failed receipt, exact binary/candidate bindings, stop-order proof, documentation-based diagnosis, and post-attempt local validation are in `docs/evidence/e10/openai-realtime-contract-attempt-20260801.jsonl` (file SHA-256 `5b9565c97f688e3f226796c689d9f2e344bd63aa3bd89a2fb129e4bdfe6383a1`). It remains `provider_contract_attempt` evidence only; the OpenAI project/billing owner is still unreconciled.
12. A corrected 334-path candidate (`f7033a832d52f38c812b62514256870babd808c39de8ef2e8003dc6d987b1d96`, probe binary `980212a27ed80c46c1b8047802fc6104d498ca3bcbe93ac6b378142bfb7cb4ea`) passed one synthetic committed-turn Realtime `gpt-transcribe` contract. The provider item ID correlates exactly across committed, created, finalized, and completed lifecycle events; the 26-event run completed in 2.152 seconds, reported six duration seconds, and reconciled to USD 0.00045. This proves the narrow modern event/correlation/accounting contract only; it does not prove WER, domain fidelity, target-device dictation, meeting quality, or seat qualification.
13. The first Scout attempt on that candidate received `session.created` and `session.updated` but timed out because the then-frozen harness required optional `conversation.created`. No generation or valid `response.done` usage was observed; cost remains unreconciled rather than zero.
14. A new exact candidate (`886a6a80908e2ab96d1081a61a03cbf8e014b8b7d2f6b42076cf5e353fbc4d78`, probe binary `057134722e4b8381ee8caf9b699473a133c10bfb9068356f1ca791b5f594e626`) passed independent preflight, and the user authorized exactly one Scout retry. It reached 19 events including partial transcript/audio deltas and both stream terminals, then produced an incomplete assistant final item; the old parser stopped before `response.done`. The receipt's legacy zero output/token/cost fields are not observations. Usage and spend are `unreconciled_partial_generation_without_response_done`; the high-reasoning 48-token cap is the strongest diagnosis, not a provider-confirmed reason. This is not a successful Scout contract.
15. The post-retry local correction is bound to source `a582a165e6c4314ea614f327d2cb7a323148f7fef957e0ff58a0f04855fe66b7`, test `59ff1e3a14a30d13c43dd79d395a1550a1dc87ff4aad09bf7f354556cde68222`, and event schema `b5e4c614301ff09756f8adec5fbc10f4baa9ab7763df4d236ba302d52afb04c5`. It makes `conversation.created` optional, handles documented completed/incomplete item and terminal-response states, makes only completed `response.done` authoritative for success, preserves partial output on failure, strictly reconciles optional usage fields and costs, safely hashes provider failure classifications, and uses a 256-output-token cap whose conservative all-audio maximum is USD 0.018432 under the USD 0.02 admission ceiling. Focused normal 20×, focused race 3×, full package normal/race, vet, gofmt/diff, official-contract review, and independent Critic all pass. It has not been provider-retested.
16. The four new exact raw receipts, candidate/binary/fixture/reference/path bindings, fact-versus-inference diagnoses, legacy-zero correction, and local validation are in `docs/evidence/e10/openai-realtime-contract-final-20260801.jsonl` (file SHA-256 `4625652b65c39ea2673a9687cb54d66f5fa74cf20a4dccb8188008283215c69f`). The packet is JSONL-valid, preserves each raw receipt exactly plus its original-file digest, contains no raw key/project/request ID, prompt, reference text, audio, or transcript body, and records no auto-retry, fallback, Git, route, configuration, or production mutation.
17. The same read-only production audit found that all 958 tracked non-`data/`
    files under `/opt/meetingassist` match `c7b4128f0f45d1b6443c73cbae3e54feceb735d3`
    (manifest SHA-256
    `8f85575eb6565a1ec607e316ca08eb5c8d837968124914beeaf6b1cdad271938`),
    but the serving image is not release-qualified. Its actual image ID is
    `sha256:190578697b6a79edd0868e3aad9ae46dc94b76d1fc7c300422822e3fe86181bf`,
    its binary SHA-256 is
    `64166802d59c66293338200916868957fc006733e3665911c88eeee9559520c1`,
    and its stale revision label/effective release commit is
    `cbc27df1cbd360619ecbd353bd9782cd0a20b358`, while the configured image
    digest does not match the running image and the build-manifest value is
    `unqualified`. Public health exposes no release identity. The source copy
    matching main therefore does not bind the serving binary or image to main.
18. Production is traffic-serving but not company-brain-ready: canonical shadow
    is unhealthy with a persistent idempotency conflict and a 4,418-event
    reconcile gap (dirty high-water 12,950 versus reconciled 8,532), consent
    authority is unavailable, brain projection is off, brain/recap are roughly
    21.8 days stale, Scout is disconnected, and STT is stale. `/readyz` still
    returns HTTP 200 while `/capabilities` returns `ok:false`; no aggregate
    health response may be used as company-brain acceptance. The six expected
    Compose services account for all running services, the live named data
    volume is mounted correctly, and sampled host/container telemetry shows no
    resource-pressure or OOM explanation. HA/DR remains open because the only
    observed backup is local and unencrypted, offsite custody is dormant, and
    no restore has been verified. No production state was changed by the audit.
    The seven-record sanitized audit packet is
    `docs/evidence/e10/live-readonly-audit-20260801.jsonl` (SHA-256
    `009b9fc8b0b90e65d49acd2a340e08f42cd475081becc76e0d93742d7a275b80`).

**2026-08-01 final exact-release checkpoint:**

1. Implementation commit A is
   `9c5d53a9cadda68bf4c7014d4357a7312969e4e9`. Its repeat-generated staged
   manifest contains exactly 408 paths and 12,233,727 bytes with SHA-256
   `6caf49a231653bba076fb7b3382bd2b052d3f4ba674c94c92d1d6e66f672f3b6`.
   The pre-race working-content manifest matched byte-for-byte after the final
   run: 408 paths, 12,233,727 bytes, SHA-256
   `a0a50e73cd5c7da39211943a7982a4e6412b91c9ff9cb907f90f0cc530ab7b4f`.
   This ledger and the separately owned untracked `stride-site/` are excluded
   from A by construction.
2. The complete non-`stride-site` Go suite passed normally (root package
   382.522 seconds) and under `-race -timeout=90m` (root package 2,723.377
   seconds; complete run 45:24.50). Root Node passed 122/122, mobile passed
   358/358 plus TypeScript, and `go vet`, `go mod verify`, formatting/diff,
   Expo dependency alignment, both production dependency audits, the release
   pack checksum/self-check, and the narrowly justified secret scan all pass.
   The only default secret-scan findings were two occurrences of the same
   explicit synthetic test replay key; an exact-value fixture allowlist leaves
   zero findings.
3. Independent critics pass the collaboration store, personal Realtime and
   control surface, native auth/session/push authority, server push authority,
   renderer release, and final release-pack gates. The final candidate includes
   exact token-scoped native 401 fencing, recoverable secure storage,
   process-wide push binding and revocation ordering, Bearer-over-cookie native
   authority, pre/post-lease Realtime tool authorization, a real runtime
   HTML-to-PDF-to-JPEG renderer canary, and recursive rejection of nested body
   or credential material in structured references.
4. Reviewed migrations `0008_stride_contracts.sql` and
   `0009_stride_conversation_ledger.sql` are included in A. They remain subject
   to the exact bootstrap backup, rehearsal, canonical parity, consent,
   migration, rollback, and public-reopen gates; inclusion is not evidence that
   they have run in production.
5. The iOS release candidate is Stride 1.0.0 Build 29 and the repository pins
   the qualified exact EAS CLI `21.4.0`. Read-only EAS, signing, App Store
   Connect, unused-build-number, internal-group, and external-group-baseline
   preflights pass. No Build 29 artifact, submission, Apple processing, group
   availability, or physical-device result is claimed at this checkpoint.
6. Read-only VPS preflight confirms the reviewed six-service legacy topology,
   eight named volumes, seven-migration baseline, live named data volume,
   available host capacity, and absence of an earlier exact-release ledger or
   guard. It also reconfirms that canonical parity, consent, exact release
   identity, nine-room quiet proof, restore evidence, and public reopen must be
   established by the guarded bootstrap; no deployment success is claimed here.
7. Provider-seat qualification, Scout and specialist voice, real-corpus STT and
   dictation quality, WebRTC/TURN device acceptance, physical TestFlight
   acceptance, external custody/HA restore, and the 24-hour/ten-sitting soak
   remain separate external gates. This checkpoint does not promote or enable
   any default-off provider route or feature.
8. The first exact VPS `init-build` for A stopped before maintenance because
   Chrome Headless Shell 150's checksum-pinned archive no longer contains the
   obsolete `chrome_sandbox` helper that `Dockerfile.render` tried to chmod.
   No application service, container, database, volume, migration, firewall,
   route, or public traffic changed. Node was installed at the exact reviewed
   Ubuntu package version and the fail-closed build output was retained for
   diagnosis.
9. Superseding implementation commit
   `d8ab3246edbdb53891293ef242039706e209e8b0` removes only that obsolete,
   already-disabled helper dependency, requires the real headless-shell binary
   to be executable, and adds a regression assertion forbidding the old path.
   The active boundary remains non-root namespace sandboxing with no
   `--no-sandbox`, capability drop, no-new-privileges, read-only filesystem,
   internal-only network, queue-only mount, and fail-closed runtime canary.
   Focused normal/race tests, the full root package (380.089 seconds), Node
   122/122, `go vet`, pack self-check/checksums, diff and secret scans, and an
   independent security critic all pass. This document is the sole intended
   change in the direct checkpoint child used for the retried exact release.
10. iOS Build 29 was built from clean exact SHA
    `97ff340097253ff3ad98481226f6159c3ce206ae`, before the server-only renderer
    correction. EAS build `7857b9b2-2de8-4248-b1c6-a50c54f6ca97` is FINISHED;
    submission `e631f914-4e10-405d-a686-2fb1509b0651` completed; Apple build
    `6a870589-8448-44fb-99d6-cea2f5a9ebb4` is VALID and non-expired; internal
    `Team (Expo)` contains Build 29; external `Bonfire` excludes it and retains
    its exact prior build baseline. The only later implementation changes are
    `Dockerfile.render` and its Go regression test, and this direct child adds
    only this plan, so the mobile source/config tree is byte-identical to the
    TestFlight artifact's commit. This proves build, Apple processing, and
    internal-group availability, not physical-device acceptance.
11. Final implementation release boundary A is
    `59fd7c9be356489db3979977deef1f3841150164`. It contains the complete
    reviewed implementation lineage through renderer correction
    `d8ab3246edbdb53891293ef242039706e209e8b0` and intentionally removes this
    plan from A's tree so the direct checkpoint child can add it as its exact
    sole diff, as required by the one-time bootstrap contract. This boundary
    changes no release-owned source, configuration, migration, image input, or
    mobile input relative to the corrected implementation; it exists only to
    preserve the machine-verifiable A/B ceremony without rewriting history.
12. The first manual A cutover then reached the protected maintenance window,
    completed a cold eight-volume backup and restore rehearsal, applied the
    reviewed migrations, and stopped fail-closed before public reopen when the
    pinned Chrome 150 namespace sandbox could not start under Ubuntu 24.04's
    restricted-user-namespace AppArmor policy plus Docker's default seccomp
    profile. The rehearsed cold rollback restored the exact legacy volumes,
    migrations, images, service inventory, and public health before traffic was
    reopened. The failed ceremony's state, pack, build evidence, and backup
    remain quarantined; its A/B pair is retired and must not be resumed.
13. The renderer correction preserves Chrome's namespace sandbox instead of
    adding `--no-sandbox`, `seccomp=unconfined`, `SYS_ADMIN`, or `SYS_CHROOT`.
    It adds one release-bound AppArmor ABI 4 profile derived from Docker's
    default container policy with the required mediated `userns` permission,
    while leaving `kernel.apparmor_restrict_unprivileged_userns=1`. Its seccomp
    input is the exact Moby `profiles/seccomp/v0.2.3` default plus only five
    amd64 rules observed from the pinned browser: three exact `clone` flag
    values, exact `unshare(CLONE_NEWUSER)`, and `chroot`. `clone3` remains
    `ENOSYS`; `setns` and broader mount, network, UTS, IPC, PID, and cgroup
    namespace combinations remain denied. An exact VPS canary produced both
    PDF and JPEG output under UID/GID 65532, zero capabilities, read-only root,
    no-new-privileges, AppArmor enforce mode, and seccomp filter mode; negative
    namespace and outer-`chroot` probes also passed.
14. The replacement genesis operator pack now installs and byte-attests both
    profiles only after dual IPv4/IPv6 ingress isolation, reruns the sandboxed
    renderer canary before any legacy deletion, and verifies the expanded live
    Docker seccomp JSON plus process capabilities, identity, filesystem,
    network, NNP, and seccomp state before acceptance. Every forward phase is
    terminally forbidden after any public-open attempt or legacy rollback, and
    destructive phases re-prove both live ingress guards, the exact loopback
    marker, external block acknowledgement, and enforcing profiles. Profile
    cleanup is interruption-idempotent and refuses drift or any running or
    restartable container user. The complete non-`stride-site` Go suite passed
    again (root 401.317 seconds), focused race and `go vet` passed, Node passed
    104/104, mobile remained 358/358 plus TypeScript, both production audits
    remained clean, the pack self-check/checksums and diff secret scan passed,
    and the independent release critic's two rollback findings were corrected.
    A fresh implementation/checkpoint pair, exact pack preparation, guarded VPS
    ceremony, public rendered QA, and physical-device acceptance remain the
    next evidence boundaries; the failed pair cannot be relabeled or reused.
15. Replacement pair `c1872c72100c7c4b42b1dec5c2f97573f76d1b38` /
    `d91adc85c29b8b73fdf4a291d5782e390e2cecb2` passed exact local preparation,
    build/preflight, dual external isolation, stable all-room emptiness, a new
    eight-volume cold backup and full restore rehearsal, the confined
    PDF/JPEG renderer canary, legacy retirement, and migration application
    through version 9. Manual A then exposed two further release-gate defects
    while traffic remained blocked: synchronous first-start migration and
    canonical reconstruction took roughly 161 seconds, so Docker's 20-second
    start period plus three 30-second failures marked the still-working process
    unhealthy before the separately configured 300-second Compose wait could
    help; after the process became healthy, `release_data_gate` looked for SQL
    files under `sealed-candidate`, which intentionally contains only the
    release configuration allowlist. The ceremony did not bypass either gate.
    It used the rehearsed cold restore, returned PostgreSQL to exact migrations
    1-7 and every prior table/volume/image, removed the candidate profiles, and
    independently proved restored public HTTPS/readiness and TCP TURN. This
    pair is also retired from genesis use even though it remains an ancestor on
    `main`.
16. The next correction makes the application health start period an exact
    release-validated 300 seconds while retaining the existing post-start
    three-by-30-second failure budget. The migration gate now proves
    `source.tar` against both source and release receipts, rejects every unsafe
    archive path, requires the exact regular `migrations/0001`-through-`0009`
    inventory with no missing, duplicate, extra, symlink, or non-regular
    member, streams each member directly to SHA-256 without extraction, and
    compares one ordered nine-row PostgreSQL hash snapshot. Its executable
    fixture proves success with no sealed-candidate migration directory plus
    wrong-hash, missing, extra, symlink, and archive-tamper rejection. Operator
    self-check/checksums, Node 104/104, focused Go normal/race, the complete
    non-`stride-site` Go suite again (root 376.552 seconds), `go vet`,
    diff/secret scan, actual VPS Compose `5m0s` normalization, and an
    independent release re-review all pass. Production remains on the restored
    healthy legacy release until the third exact pair passes the full ceremony.
17. Third exact pair A3/B3 is
    `64881c0d4496f936ae1a358c5550a0ea1207fe5a` /
    `8e1b16c3531a93c93ea79914c80fe9e41573f645`. Its protected ceremony
    completed fresh dual-ingress isolation, all-room quiescence, a new
    eight-volume cold backup and full restore rehearsal, the confined
    PDF/JPEG renderer canary, and candidate migration application through
    version 9. The application then stopped at the canonical parity gate
    before public reopen. The first isolated clone observation reported 24
    candidates; after the two deterministic clone starts had applied the
    ordinary current-board revisions, the same source/canonical boundary
    reduced reproducibly to exactly seven historical board candidates. No
    gate was bypassed. The rehearsed cold rollback restored the complete
    legacy volume/image/migration set, candidate profiles were removed, and
    public HTTPS, readiness, and TCP TURN were independently rechecked before
    legacy traffic reopened. Production is therefore healthy legacy, not an
    A3/B3 release.
18. The seven-card evidence is deliberately narrow and sanitized. A frozen
    32-card historical board and the current 25-card board have distinct
    sealed hashes; the canonical store retains all seven as active historical
    imports, while the current board omits them. Five are prior legacy repair
    candidates: three have an unchanged historical Backlog state, while two
    have a later archived Done state and therefore cannot inherit one uniform
    source-state assertion. The remaining two are consistently observed as
    archived Done. There is no board deletion journal, deletion actor, reason,
    or original deletion timestamp in the captured live, clone, or retained
    backup evidence. A repair may record only a last-positive observation, the
    current absence observation, and its own append time; it must not invent a
    historical deletion fact.
19. The production append remains blocked on one explicit, manifest-bound
    human confirmation after a clean clone receipt. The private manifest must
    bind the sealed source and archive hashes, seven opaque candidate
    fingerprints, selected source state per candidate, target pre-state,
    backup and release identities, clone receipt, controlled reconciliation
    reason, and exact no-extra delta. From a pristine 32-event target, the
    acceptance rule is exactly two ordinary current-board revisions plus seven
    controlled absence-backfill tombstones (nine events and nine outbox rows),
    unchanged 25-card visible board, then zero delta on a second reconcile,
    restart, and fresh clone repeat. If normalization has already produced the
    two ordinary revisions, the alternative precondition is exactly 34 plus
    seven, never a range. Broad release/migration authority does not substitute
    for this data-history decision. Build 29/TestFlight evidence remains exactly
    as recorded above; no replacement mobile build is implied by this
    server-side blocked repair.

**Resume gates — do not skip or merge them:**

- **Authoritative meeting STT and composer dictation:** the synthetic provider
  contract is passed. Token-free evaluator/receipt machinery for the full gate
  is still being completed. Qualification then requires the preregistered
  consented corpus of at least 60 minutes and 120 clips, plus at least 250
  target web/iPhone/iPad dictations and every quality, timing, ordering,
  consent, exactly-once, microphone-generation, privacy, animation, and device
  target in §13.1; no synthetic fixture can substitute for those inputs.
- **Personal/meeting Scout:** the provider contract is failed and only the local correction passes. Any next attempt requires a fresh exact candidate freeze, independent preflight, a new explicit paid-call authorization, and continued project/cost truth. A pass must precede any specialist-voice call.
- **Luna/Terra/Sol Responses seats:** model-object access is observed only. Paid Responses probes remain hard-stopped while canonical raw `OPENAI_PROJECT_ID` and intended-project/billing reconciliation are unavailable; the project-scoped-key fallback is deliberately forbidden. After reconciliation, change one seat, effort, prompt, or tool-policy variable at a time.
- **Invited-specialist voice:** remains stopped until Scout passes, a real server-side specialist provider adapter exists, the named-agent and context-sharing approvals are current, and the invite/floor/barge-in/teardown/usage corpus is ready. The production join boundary is default-off and additionally requires a trusted external qualification authority whose current `Qualified:true` result is bound to the exact tenant, result/target, release commit, tree/image/config/candidate-route digests, provider/model/provider route/accounting mode, and specialist profile/capability. The local structure-only `QualificationEvidenceStore` is not that trust root; its concrete external adapter, evidence custody, activation configuration, and route enablement remain separate E10 work and are not supplied by deterministic application wiring.
- **I&O and workforce:** remain stopped until canonical Responses receipts, authorized real inputs, ten immutable I&O pilots, two eligible reviewers, independent criticism, durable evidence, and the complete founder/workforce acceptance flows exist.
- **`gpt-live-transcribe`:** access only; do not generate merely for completeness. A shipped provisional live-caption or early-address consumer, separate corpus, privacy contract, cost accounting, and reconciliation to authoritative final transcript must first justify the lane.
- **Embeddings and images:** access only; no generation is justified without a defined consumer, bounded acceptance target, accounting, retention, and output-review path.
- **External launch:** independent custody/consent/approvals, physical devices/TestFlight, real WebRTC/TURN and HA/restore, final immutable route map, founder flows, exact release identity, production observation, rollback, and the 24-hour/ten-sitting soak remain open. Git integration, push, deployment, route/config mutation, and feature activation each still require separate authority.

The accepted checkpoint states are `not_started`, `implemented`, `deterministic_verified`, `external_waiting`, `provider_qualified`, and `production_enabled`; no local, synthetic, access-only, or contract-only receipt may be relabeled as quality, provider-seat, physical-device, production, release, or launch acceptance.
