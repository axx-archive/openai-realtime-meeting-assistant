# bonfireOS 2.0 - W2 Decision-Complete Design

Status: approved execution design under the active goal loop; independent critic PASS on 2026-07-22. This document narrows W2 to the smallest architecture that can prove the product promise without pre-building W3 routing or W4 infrastructure.

Source contracts: `docs/plans/canonical-event-acl-v1.md`, `docs/plans/multi-room-2026-07-08.md`, `docs/model-routing-master-plan-2026-07-11.md`, and `docs/plans/bonfireos-2.0-execution.md`.

## Outcome

W2 is complete only when bonfireOS can demonstrate all three outcomes together:

1. Two simultaneous rooms can run without media, identity, Scout, chat, transcript, recap, or artifact leakage; a participant who joins late can request an exact, evidence-linked recap of the material before their first admission.
2. The company brain can restart and rebuild deterministically, answer across the entire authorized time range without silent truncation, label partial or stale answers, and resolve every asserted claim to authorized primary evidence.
3. `insights_opportunities_v1` can turn one authorized evidence snapshot into a decision-ready report, survive a bounded critic/revision loop without force-accept, accept typed human feedback, and produce ten human-reviewed pilot records.

W2 does not add a generalized workflow marketplace, scheduler, dynamic model chooser, managed HA cutover, or additional authored workflows. It does include the model-routing master plan's complete pre-W3 evaluation wave as W2D; no W3 route canary may begin without those verdicts.

## Dependency order

```text
W1 parity repair and restart proof
  -> shared W2 evidence + temporal contracts
      -> W2B projection/retrieval correctness
      -> W2A exact join-relative recap
      -> W2C evidence-bound report and critic
      -> W2D model and product eval corpus
  -> Pion room-actor gate
  -> optional managed LiveKit pilot
  -> integrated two-room + recall + workflow live gate
```

W2B supplies claim/evidence and coverage contracts to W2A and W2C. W2A supplies authoritative first-admission anchors to the temporal planner. W2C contracts may be implemented in parallel, but no pilot counts until the W2B evidence resolver is active. W3 model-route work may consume W2 eval results only after this wave passes.

### W1 entry gate

The four historical target-only `guest_link` objects are a hard stop before W2 implementation. The repair procedure is bounded to the exact candidate manifest recovered from the cold W1 backup: four object-ID SHA-256 prefixes, prior state digests, creation/expiry times, and `guest_link` family. It must:

1. enter a full mutation maintenance fence, drain or freeze delivery, confirm the room is empty, and create one matched-high-water cold snapshot of the live data volume and PostgreSQL;
2. refuse unless current reconciliation reports exactly those four unjournaled `tombstone_required` candidates and no other divergence;
3. append idempotent `guest_link_expired` lifecycle records computed from the backed-up source rows, never from guessed state;
4. deploy the journal-before-delete crash recovery and future expiry fix;
5. reconcile until dirty, reconciled, and checkpoint high-waters are equal, with zero candidates, pending captures, outbox failures, or frozen families;
6. restart the app and PostgreSQL-facing runtime, reconcile a second time, and require the same zero-diff result.

Before any journal append, a manifest mismatch, nonempty room, unrelated candidate, backup failure, or fence failure aborts with no mutation. After the first lifecycle record is appended, recovery is roll-forward-only: retain the truthful journal, keep mutations fenced, and retry idempotent reconciliation. Never restore scoped files independently after canonical delivery may have occurred. A true restore is allowed only from the matched data-volume and PostgreSQL snapshot at one proven high-water, before writers reopen.

## Shared contracts

### Evidence and claims

An asserted claim must have a stable `claimId` and at least one resolvable primary evidence edge containing tenant, source family, object ID, revision, meeting/sitting, occurrence interval or text span, and content digest. Generated artifacts additionally stamp model, route seat, reasoning effort, prompt version, generation time, and retrieval snapshot ID.

Claim states are `asserted`, `inferred`, `unsupported`, and `superseded`. Only `asserted` claims may appear without a visible qualifier. Missing, revoked, purged, cross-tenant, cross-room, or digest-mismatched evidence makes the claim unavailable; it is never silently replaced by model prose.

Retrieval produces an immutable snapshot bound to source revisions/digests, ACL versions, principal, query, temporal bounds, and source/projection high-waters. Authorization is revalidated before prompt construction, critic acceptance, artifact publication, and every later content read. A derived output's ACL is the intersection of its evidence visibility and the workflow destination; it may never widen source visibility. Revocation or purge makes affected asserted content unreadable/unassertable while preserving a body-free audit record. Guest/listen-only evidence is explicitly marked untrusted-origin, structurally delimited as data, and excluded from instructions/tool authority.

### Coverage

Every recall or recap result carries one coverage record:

- requested and resolved UTC bounds plus the interpreting timezone;
- authorized source inventory count and IDs/digests, never unauthorized counts;
- source and projection high-water marks;
- fresh, partial, stale, missing, failed, and deliberately omitted counts;
- lexical, semantic, digest, and raw-source lane status;
- `complete`, `partial`, or `unavailable`, with a concise user-facing reason when not complete.

No answer may claim to be comprehensive merely because one digest or the newest bounded window returned content.

### Temporal query

`TemporalQuery` is the one interpretation for historical and live-window requests. It includes absolute UTC start/end, local timezone, optional room/sitting, optional participant first-admission anchor, source capture-sequence cutoff, capture watermark, and an interpretation string. It supports explicit clock ranges, first/last N minutes, and N minutes before admission. Source occurrence time is distinct from ingestion, projection-generation, and claim-validity time.

### Authorization

Authorization occurs before body fetch, lexical search, semantic ranking, digest folding, prompt construction, or workflow input construction. Legacy metadata and canonical PostgreSQL authorizers run in shadow parity during W2. Guests have no durable recall. Grant withdrawal, retention deletion, and purge invalidate every retrieval lane across restart.

## W2A - Rooms, Scout, recap, and media

### Room-owned runtime

Each active full-mode sitting owns one lazy `roomRealtimeBundle`. It contains all room-shared Scout mutable state, Realtime connection/input/output, status, tool-call dedupe, restart generation, and cancellation. A guest-policy listen-only sitting never constructs the bundle. Private dashboard Scout remains a separate owner-scoped session.

The bundle is created after admission and consent gates, fenced by room+sitting+media generation, and destroyed after the existing idle close/flush chain. Every callback checks those fences before publishing tracks, events, tools, or output.

### First-admission watermark and exact recap

Persist an admission record before `access_granted`, uniquely keyed by tenant, room, sitting, and principal, using atomic `MIN(admitted_at)` semantics. It also stores the room capture-sequence cutoff and capture watermark observed at admission. A reconnect or second device cannot move it; a new sitting creates a new anchor. Guest identities may receive an anchor for audit and consent, but W2 `catch_me_up` is an authenticated organization-member surface and guest durable-recall policy remains deny. Any future ephemeral guest recap requires a separate room-local, non-durable authorization contract.

Default `catch_me_up` for a member admitted after the sitting started covers the half-open interval `[sitting_start, admission_cutoff)`. “Last N minutes” is a separate explicit range and may include post-admission content. Segments crossing the cutoff are clipped or claim-split at source timestamps; they are never included wholesale. Late-arriving segments with capture sequence at or before the admission cutoff enter a bounded settle window, after which unresolved capture gaps keep coverage `partial`. Results use the shared temporal planner, raw-source inventory, structured evidence edges, and coverage record; cumulative brain output and ambient worker cursors are never recap boundaries.

### Consent and guest revocation

PostgreSQL `consent_records` is the authoritative durable store; an in-memory adapter remains test-only. Consent-store unavailability fails closed for affected tracks while room admission/chat/video viewing continue. The server persists consent before publishing track eligibility and binds the admitted socket/track to the exact principal and sitting.

The lane matrix is explicit: `audio_capture` gates mixer/audio retention; `transcription` gates STT input and transcript persistence; `model_analysis` gates any model input or derived artifact; `org_memory` gates company rollups and organization-wide recall. A missing earlier scope also denies all dependent later lanes. Reconnect reuses only the same sitting/principal/policy's still-effective records. Withdrawal immediately fences new frames, cancels or discards in-flight uncommitted segments, invalidates dependent queued model work, and records the last accepted capture sequence. Per-participant consent state is separate from the guest-policy sitting-wide `ListenOnly` latch.

Exact human-facing disclosure copy remains owned by the authorized business/legal owner. Engineering ships neutral policy-version plumbing and fail-closed behavior; it does not invent legal assurances.

Guest sessions retain the link ID used at redemption. Revoking a link invalidates every non-expired session minted from it and immediately evicts its admitted sockets/seats. Link expiry and sweep follow the same durable lifecycle-journal-before-delete rule.

### Media backend

First place the current Pion implementation behind a per-sitting media-backend interface and move peer pool, track registry, signaling debounce, watchdogs, restart state, and room Scout track under one room actor. The actor has a bounded mailbox with typed admit/leave/track/signal/restart/close commands; signaling bursts coalesce before enqueue, nonessential refresh work sheds first, and admission/leave/close commands cannot be dropped. Callbacks carry room+sitting+media generation and publish only by re-entering their owning actor. Shutdown closes admission, cancels producers, drains required leave/state work, closes peers/tracks, then releases the actor. No room may block or mutate another room's state.

Pion remains the production default until it passes the two-room soak. A managed LiveKit pilot is optional within W2 only if credentials and account authority are already available. The backend is pinned for the life of a sitting; a route change affects new sittings only. LiveKit becomes default only after browser compatibility, guest access, recording/transcription fan-out, failure injection, cost, and rollback gates beat or match actorized Pion. Self-hosting LiveKit on the same VPS does not count as HA.

### W2A gates

- two simultaneous rooms, at least three publishers and subscribers each, for a two-hour soak;
- zero cross-room track, identity, chat, Scout, transcript, recap, or artifact events;
- room-local signaling/restart stress under `go test -race` with no cross-room head-of-line stall;
- exact pre-join and explicit-clock recaps with source evidence and honest coverage;
- reconnect does not move the first-admission watermark;
- consent deny/withdrawal prevents track ingestion at every downstream seam;
- guest-link revoke evicts existing sessions and expiry deletion is journaled/replay-safe;
- AI provider failure does not disable video or room admission;
- one-switch new-sitting rollback to the prior media backend.

The head-of-line fault gate blocks room A's offer path for 10 seconds while room B admits and renegotiates; room B p95 admission remains under two seconds and p95 renegotiation under three seconds with zero failures. The soak additionally requires packet loss below 2%, unexpected disconnects below 1% of participant-minutes, process CPU below 80% sustained, and RSS below 75% of the container limit. LiveKit rollback always returns new sittings to actorized Pion, never the old global owner.

## W2B - Restart-safe company brain

### Projection checkpoints and rebuild

Add a new migration for `brain_projection_checkpoints`; do not overload the W1 global `projection_checkpoints` primary key. Identity is tenant, projector version, room, sitting, and source family; mutable fields include source high-water, derived high-water/digest, rebuild generation, and publication time. A projector publishes its checkpoint only after its idempotent derived output is durable. “Derived durable, checkpoint absent” replays to the same derived ID and deduplicates. Controlled backfill/rebuild requires explicit start/end watermarks, a fenced rebuild generation, and the same derived IDs/checksums on repeated execution.

The canonical event sequence orders work while W2 continues to resolve bodies from the authoritative JSON/JSONL reader. PostgreSQL unavailability holds checkpoint advancement and labels projections stale; it never causes a worker to baseline past history. PostgreSQL reads remain a per-family shadow decision, not a W2 blanket cutover. Retrieval snapshots fence source revisions and purge generation so a concurrent mutation forces revalidation/retry rather than mixed-revision context. Crash failpoints cover source append, derived append, checkpoint publication, supersession, purge, rebuild fencing, and restart.

### Full-range retrieval

Inventory every authorized meeting/source object in the requested range before answering. Each inventory row is classified fresh, partial, stale, missing, failed, or omitted. Missing/stale portions are paged through raw sources; one fresh digest never suppresses uncovered meetings or transcript windows. Hard computational limits yield continuation or a `partial` response, never silent oldest-drop.

Stale vectors are excluded by default. Query-embedding or model-provider failure leaves lexical/raw lanes usable and appears in coverage. Projection lag and stale/failed counts appear in health and eval evidence.

### W2B gates

- deterministic corpus of at least 40 meetings over 90 days, exceeding 250 records, 12 meetings, and four chunks in one meeting;
- exact windows, DST boundaries, join-relative queries, current state, evolution, contradictions, supersession, and source drill-down;
- per-principal ACL differential across organization, owner/private, room-only, guest, service, grant, revoke, retention, and purge;
- crash/replay matrix at every source/derived/checkpoint boundary, replayed twice with identical IDs/checksums and no lost or duplicated claims;
- mixed fresh/missing/stale/failed coverage with every omission visible;
- every asserted answer claim resolves to authorized primary evidence;
- stale embeddings and unavailable semantic/model lanes cannot silently influence an answer;
- sanitized read-only production snapshot replay is a required release artifact.

## W2C - Insights & Opportunities v1

### Closed workflow contract

Register one versioned process, `insights_opportunities_v1`, with closed request, evidence snapshot, claim, opportunity, report, critic verdict, and feedback schemas. Each opportunity binds source claim/evidence IDs, confidence, counterevidence, expected impact, recommended next action, proposed owner, and decision status.

The only launch surface in W2 is an authenticated, direct, human-approved invocation. Approval binds request revision/digest, evidence-snapshot digest, process/prompt version, artifact destination, and `workspace_write` action digest. The workflow may create or revise that workspace artifact; it may not email, share, publish, deploy, call an external write API, or gain standing approval. `ForceAccept` is permanently false for this process.

### Critic and feedback

The critic returns claim/opportunity-level `accept`, `revise`, or `reject` verdicts with evidence IDs, missing-evidence and counterevidence findings, and required actions. Revision is bounded and checkpointed. A rejected or exhausted run is terminally honest; it cannot be promoted by an aggregate score.

Typed feedback binds workflow/version, run, report, opportunity/claim, action/reason, optional corrected fields, actor, time, evidence, and idempotency key. The actor must be an active organization member authorized to write the destination report; human-review qualification additionally requires a named pilot-reviewer role. Feedback can request a new revision but never mutates the evidence snapshot or prior report in place.

W2 pins the known-safe static seats: Fable 5/high for orchestration and report generation and Opus 4.8 for review, subject only to existing same-seat provider fallback rules. It records the actual model, provider, effort, prompt version, retries, usage, and critic outcome. Dynamic model selection belongs to W3. The process ships default-off behind `BONFIRE_INSIGHTS_OPPORTUNITIES_V1_ENABLED`; disabling it is the workflow rollback.

### W2C gates

- at least ten deterministic source-bound fixtures including conflicts, missing evidence, invented source, stale/revoked evidence, unauthorized feedback, critic rejection, and human revision;
- ten unique completed pilot runs by at least two eligible reviewers, each from the same release-candidate commit/process/schema/prompt versions, with an immutable input/evidence manifest and authenticated review; any behavior-affecting change resets the qualifying set;
- aggregate evaluator reports accepted, revised, rejected, blocked, unsupported-claim, and evidence-resolution counts without collapsing failures into success;
- restart/resume and duplicate-launch tests produce one logical run and immutable revisions;
- process disable switch and prior static route rollback are proven;
- no force-accept or external-write path is reachable.

The qualifying ten require zero unauthorized evidence disclosures, zero invented asserted claims, 100% asserted-claim evidence resolution, and at least eight accepted after no more than two revision rounds. Rejected/blocked runs remain visible but cannot satisfy the acceptance-rate gate. Authenticated reviewers and working provider quota are named external dependencies for this live gate.

## W2D - Pre-W3 model and product evaluations

W2D implements and runs every mandatory verdict from `docs/model-routing-master-plan-2026-07-11.md` before W3:

- ground-truth STT corpus and dual-model transcription fidelity/latency/cost gate;
- brain write-up commitments contract and proposal-coverage evaluation;
- proposal-to-kickoff router golden set and funnel test;
- ask-anything recall groundedness/completeness/refusal-honesty evaluation;
- brain-fleet parity and board-operation fidelity corpora;
- Realtime voice wake/tool/false-response baseline;
- review-gate shadow comparison;
- embedding retrieval and stale/degraded-lane evaluation.

Each verdict freezes its baseline, candidate, corpus digest, metrics, cost, and pass/fail result. W2D changes no production model route; it produces the evidence W3 consumes. Every evaluation must execute successfully and produce a valid receipt. Product-correctness baselines must pass; a valid negative candidate verdict is an acceptable W2D outcome and blocks only its corresponding W3 canary. Missing, invalid, or inconclusive receipts block W2.

## Integrated release gate

Before W2 ships, the repository-wide normal suite, consolidated high-risk race suite, migration/replay corpus, two-room soak, production-shaped recall replay, and workflow pilot evaluator must pass from the same release-candidate commit. Every W2D evaluation must execute successfully with a valid receipt from that commit; product baselines must pass, while a candidate regression records a negative verdict and blocks only its W3 canary. The critic loop receives the final diff and evidence packet and must return pass with no unresolved P0/P1 finding.

`testdata/w2/gates.json` is the checked-in executable manifest. Every gate row contains ID, exact command, fixture/snapshot digest, timeout, numeric thresholds, receipt output path/schema, release commit, feature flag, stop condition, and rollback command. Receipts live under `artifacts/w2-gates/<release-commit>/` and are body-sanitized; the production replay manifest records custody, source high-water, hashes, and deletion time without committing production content. Final commands include `go test -count=1 ./...`, a checked-in consolidated `go test -race` regex, and named binaries/tests for the two-room soak, replay, pilots, and W2D evaluations. `P0` means data loss, authority/privacy breach, cross-room leak, capability resurrection, or unsafe side effect; `P1` means a core promised flow is incorrect, incomplete without labeling, non-restart-safe, or lacks a proven rollback. Either blocks release.

Deployment uses the existing cold-backup and profiled Compose procedure. Production starts in shadow/disabled posture for new projectors, workflow, and optional media backend. Enable one bounded canary at a time for at least 24 hours or ten complete sittings, whichever is longer, verify health plus user-visible evidence, and retain JSON/JSONL, actorized Pion, process-disable state, and prior model seats as independent rollback paths.

Provider quota recovery is required for live model acceptance, but never for video continuity, deterministic fixtures, replay correctness, authorization, or lifecycle durability. A provider error remains a degraded gate, not permission to weaken evidence, cost, or authority controls.

## Named external boundaries

- Authorized business/legal owner must approve the exact consent and retention disclosure copy.
- Managed LiveKit, managed PostgreSQL HA, and private object storage require usable account credentials; absent credentials remain explicit dependencies.
- Native Apple GA requires signing-team, privacy-manifest, and physical-device evidence; otherwise 2.0 remains web-first with native labeled beta.
