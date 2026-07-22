# bonfireOS 2.0 - Execution Ledger

Goal and source pointers: active `$goal-loop`; `docs/model-routing-master-plan-2026-07-11.md`; `docs/plans/multi-room-2026-07-08.md`; architecture audit in the current Codex task.

Current phase: execution resumed by explicit user signal on 2026-07-22. W0 is live; W1 is held at a bounded canonical guest-link parity repair before any W2 implementation. The decision-complete W2 design is approved at `docs/plans/bonfireos-w2-design.md`.

## Invariants

- Production truth lives in Docker volume `digitalocean_meeting_data`; repository and `/opt/meetingassist/data/` content are never treated as live data.
- Preserve the user-owned untracked `design-system/` directory.
- Private Scout threads remain owner-scoped; guest or retrieved content never authorizes tools.
- Video remains available when AI providers degrade; stale or partial AI output is always labeled.
- No force-accept critic path, no unattended external publish, and no silent authority escalation.
- JSONL, Pion, and prior per-seat model routes remain rollback paths until their replacements pass live gates.

## Wave Map

| Wave | Outcome | Dependencies | Gate / rollback | Status |
|---|---|---|---|---|
| W0 | Stop runaway spend; make output, authority, health, backup, and usage truth enforceable | None | Accepted digest advances cursor; code-level authority tests; offsite restore evidence; old env/code backup | Complete; model-output recovery canaries held for W4 quota gate |
| W1 | Canonical event/ACL substrate, object authorization, outbox/jobs, retention, consent, revision-bound approval | W0 | Dual-write replay/checksum and ACL-negative parity; JSONL reader rollback | Repair gate: future expiry fix pushed; historical four-object parity repair awaiting empty-room maintenance window |
| W2A | Per-room Scout, exact recap, guest policy, media backend pilot | W1 contracts | Two-room zero-leak live gate; Pion and feature-flag rollback | Pending |
| W2B | Restart-safe brain, complete historical recall, claim/evidence lineage | W1 contracts | Recall corpus and restart/replay gates; shadow-reader rollback | Pending |
| W2C | `insights_opportunities_v1`, structured feedback, verdict critic, pilots | W1 + W0 route/authority | Ten reviewed pilots; process disable and route rollback | Pending |
| W3 | Static versioned model-route registry and measured canaries | W2 eval corpora | One seat at a time; prior route pointer rollback | Pending |
| W4 | HA/DR cutover and full operational release | W2 + W3 | Chaos, restore, live media/recall/workflow evidence; cutover rollback | Pending |

## Current Wave

- Lead owns integration, Git, VPS configuration, restarts, migration cutovers, and release evidence.
- Subagents own disjoint code paths only; they do not stage, commit, push, deploy, or mutate production.
- W2 resumed on 2026-07-22. Independent audits found the existing W2A/W2B foundations, the absent W2C product, and the exact remaining gaps. Strategic design plus two critic rounds produced the approved W2 contract in `docs/plans/bonfireos-w2-design.md`, including W2D's complete pre-W3 evaluation wave.
- Resume inspection found W1 canonical shadow at four target-only `guest_link` objects. Cold W1 backup evidence proved the exact four rows expired on 2026-07-16/17; the current expiry sweep had removed them without a lifecycle journal. No target history gap, pending capture, outbox failure, or principal-parity defect was present.
- Commit `a0ae9c9` is pushed to `axx/main`. It journals future expiry before source removal, shares the importer digest projection, and recovers the journal-before-source-rewrite crash window before canonical boot. Focused normal/race tests, the full suite, and an independent adversarial re-gate passed.
- The historical backfill is roll-forward-only after first append and is bounded to the four cold-backup/canonical-candidate fingerprints. Production currently reports occupied seats, so the matched data-volume/PostgreSQL snapshot, journal repair, deploy, restart, and second-reconcile proof are waiting for a genuinely empty-room maintenance window.
- Live containment applied at `2026-07-12T19:11Z`: `MEETING_DIGEST_DISABLED=true`; env backup at `/opt/meetingassist-backups/20260712T191102Z-digest-containment/env.before`.
- Digest usage baseline after containment: 573 calls, last call `2026-07-12T19:07:49Z`, app-estimated cost `$61.80095` for 2026-07-12.
- Digest count remained exactly 573 through `2026-07-12T21:30Z`; persisted digest count remained 5 across 2 meetings. No post-containment spend occurred.
- Combined W0 implementation now includes strict Responses JSON Schema, 4,000-token reasoning/output headroom, accepted-vs-wire usage truth, poison-output circuit, provider-outage cursor hold, positive-evidence capability health, principal-aware tool policy, exact callback binding, monotonic queue status, and hardened runner isolation.
- Codex production queue has 13 completed jobs and zero nonterminal jobs. Cutover must preserve those records, migrate usage books, and recreate the app plus profiled runner together.
- W1 runs a private PostgreSQL 17 shadow service on the VPS with the protected external volume `digitalocean_canonical_postgres`. JSON/JSONL remains authoritative; `required` is not enabled in W1.
- `meetingassist-demo` was permanently resized from 2 vCPU / 2 GB / 60 GB to `s-4vcpu-8gb` (4 vCPU / 8 GB / 160 GB, $48/month).

## Completed Evidence

- Baseline `cd78b8e` is synchronized with `axx/main`; key live file hashes match local HEAD.
- `go test -count=1 ./...` passed in 101.151s.
- Media verification passed 21/21; voice-focus benchmark passed.
- Live `healthz` and `readyz` returned HTTP success after containment.
- Root integration `go test -count=1 ./...` passed after all W0 revisions in 114.299s; the focused provider/principal/health gate passed in 0.394s and the isolated HOME/TMPDIR runner regression passed in 0.633s.
- Read-only production compatibility gate confirmed Docker 29.4.3, Compose v5.1.3, empty rooms, healthy app/runner, fresh runner heartbeat, no nonterminal jobs, and successful parsing of both current and proposed Compose against the live environment.
- Independent adversarial re-gate passed after provider holds were centralized across every ambient producer and authenticated principals were propagated through chat-launched process goals.
- Exact-Compose VPS preflight built both images and started Codex CLI 0.144.1 under dropped capabilities, read-only root, no-new-privileges, isolated writable mounts, and read-only sandbox. Queue heartbeat and usage writes passed. The first job exposed and drove a HOME/TMPDIR isolation fix; the rerun reached OpenAI and stopped only on the current quota error.
- Live W0 cutover completed from commit `7dbac83` with backup `/opt/meetingassist-backups/20260712T220050Z-w0-control-plane`. Historical usage books were prefix-verified in `digitalocean_usage_ledger`; all 13 completed jobs were checksum-verified in `digitalocean_codex_queue`; app and runner were recreated together and both have zero restarts.
- External-volume deletion protection shipped in follow-up commit `cc780f1`; the live queue and usage volumes are explicit external resources.
- Live `/livez`, `/readyz`, `/capabilities`, and `/participants` passed their contracts. Traffic is ready while AI capabilities truthfully report degraded; the runner heartbeat reports the new queue paths and a Git workspace; the sidecar has no company-brain mount. Digest remains disabled and the live call count remains exactly 573.
- W1A durability/contracts slice passed an independent code gate after adversarial revisions: mode-gated fsync/rename/directory durability, RFC 8785 deterministic tenant-scoped imports, closed immutable payload schemas, deterministic replay, default-deny ACLs, revision/action-bound approval and ambiguous-effect handling, and a PostgreSQL 17 schema applied successfully to a disposable local database. Focused race tests passed. The repository-wide suite still has one reproducible-only-under-full-load async TempDir cleanup flake to resolve before this slice can deploy.
- The async TempDir flake was traced to a test-owned revision goroutine chain and fixed by draining it before cleanup; 50 normal and 10 race repetitions passed. W1A now also has recoverable prepared/committed/aborted shadow frames, torn-tail recovery and ambiguity freeze, cross-process locked durable object versions, advisory-locked checksum migrations with future-version refusal, transactional event/projection/outbox append, revisioned PostgreSQL ACL grants, and state-only content-binding preservation. After two independent revision rounds the code gate passed; the final integrated repository suite passed in 112.739s and focused race gates passed.
- W1 canonical migration/consent/retention/approval slice passed three adversarial rounds: deterministic secret-free legacy import, tenant-scoped non-mutating reconciliation with target ACL proof and current aggregate folding, exact consent withdrawal semantics, strict privacy-safe tombstones, PostgreSQL purge evidence across process restart, and row-locked atomic approval receipt/job/outbox dispatch. Focused normal and race gates passed; the final repository suite passed in 128.969s. Full-database rollback protection remains a W4 dependency because the purge authority must be retained separately from content snapshots.
- W1 artifact/capability enforcement passed its independent critic after bounded adversarial revisions: body-free tenant-bound authorization headers; exact atomic header/body snapshots; owner-only private legacy migration; principal-filtered artifact, Files, follow-up, chat-drop, and client snapshot paths; revision/asset-bound share, Deal Room, render, and archive capabilities; approval-time reauthorization; hash-only token state; archive revocation and same-buffer serving; parent-goal authorization; and conditional/compensated Files mutations. Integrated focused normal/race gates passed, and the final post-fix repository suite passed in 128.435s.
- W1 management, recall, worker, and runtime integration passed independent adversarial gates after bounded revisions: metadata-first package/folder/file authorization; organization-visible historical recall with owner/private and explicit room-only isolation; zero guest durable recall; per-principal HTTP/websocket/meeting/Brief projections; delegated service/agent context; scope-preserving ambient derivation; revision-bound imported PostgreSQL grants with member/owner/guest/service ACL parity; recoverable prepare/write/commit capture; lifecycle deletion advancement; autonomous retry; drained verified outbox; source-bound restart checkpoints; and blob two-file crash recovery.
- Final integrated `go test -count=1 ./...` passed in 182.922s. The consolidated high-risk race gate for runtime, capture/import/reconcile, ACL, management, recall, ambient workers, and packages passed in 40.194s. Final focused registry/decision/package verification passed after the last registry correction. `gofmt` and `git diff --check` are clean.
- W1 shipped through commits `8883ffb`, `f148096`, and `7c68c04` on `axx/main`. The 6,220-object initial import was made restart-practical with one batch version-map publication, and reconciliation was independently re-gated after moving its expensive scan/apply work outside the user-facing runtime lock while retaining single-flight ordering.
- The repository-wide post-availability suite passed in 178.349s; focused normal and all `CanonicalRuntime` race tests passed; the independent availability critic returned PASS.
- The W1 cutover used cold snapshot `/opt/meetingassist-backups/20260713T023028Z-w1-canonical-shadow` plus the scoped follow-up backup `/opt/meetingassist-backups/20260713-031023-w1-reconcile-availability`. Production was empty before recreation and remained empty afterward.
- Live restart/checkpoint verification passed with canonical shadow healthy at equal dirty/reconciled/checkpoint high-water 10, zero pending capture, zero outbox backlog/failures, and no frozen families. PostgreSQL held 6,226 canonical events, 6,224 current objects, and 11,791 grants; imported guest/service/capability durable grants were exactly zero.
- Production memory remained 5,455 lines; board, user, and room hashes exactly matched the pre-cutover baseline. All 13 queue jobs remained present, usage evidence advanced from 5 to 6 files, and app/PostgreSQL/Codex/render services were running with zero restarts and fresh worker heartbeats.

## Pending Dependencies

- Dedicated managed PostgreSQL HA and private object storage are not present. W1 uses a local private PostgreSQL shadow on the resized VPS without a separate managed-resource charge; managed HA/object storage and their recurring-cost decision remain W4 work. Managed Valkey remains intentionally deferred.
- The PostgreSQL purge ledger survives process restarts, but a full database rollback could restore both content and its purge authority to the same older point. W4 restore design must keep a separately retained append-only purge manifest/authority and invoke the restore gate before readiness; process-restart evidence alone is not a database-rollback proof.
- The user reports the OpenAI API balance was topped up, but the bounded live embedding canary still returned HTTP 429 `insufficient_quota` at `2026-07-13T03:13:08Z`. Normal digest production remains disabled; this external provider gate must be rechecked before later AI recovery claims.
- Guest consent/retention language needs an authorized business/legal owner.
- Native Apple GA requires signing team, privacy manifest decisions, and physical-device evidence; otherwise 2.0 must explicitly ship web-first with native labeled beta.

## Operations And Authority Queue

- Authorized by current objective: scoped code edits, tests, commit/push to `axx/main`, VPS deploy/restart, and production configuration needed to make 2.0 fully live.
- Before irreversible data cutover: encrypted offsite restore drill, cold volume snapshot, dual-write high-water marks, empty-room window, and explicit rollback command.
- External resource provisioning is allowed only where credentials and account authority are already available; missing credentials remain a reported dependency rather than a local-only substitute.
- Authorized W1 maintenance action: cold live-data snapshot, reversible service stop, permanent disk-expanding droplet resize to `s-4vcpu-8gb`, creation of external `digitalocean_canonical_postgres`, env/Compose cutover, and app plus profiled runner recreation.

## Risks And Decisions

- Current digest failures were systemic truncation/parse failures, not provider transport failures. Identical rejected model output uses a bounded circuit; provider/quota/transport failures use capped probes while holding the cursor forever and can never consume the poison-input dead-letter budget.
- Cost/eval books move to a dedicated `usage_ledger` volume nested at `/app/data/usage` for the app and mounted alone at `/app/usage-ledger` for the runner. Migrate the existing production usage directory before recreation; never restore runner access to the whole company-brain volume.
- The HMAC callback contract requires app and runner to cut over together with `docker compose --profile codex ...`; a standard profile-less restart is a no-go.
- LiveKit is a gated production-default candidate, not a foregone cutover. Pion remains per-sitting rollback; if LiveKit cannot pass the gate, Pion must be actorized per room before a 2.0 availability claim.
- Use a modular Go control plane with durable shared state; do not split into a microservice fleet or add Kafka.
- Implement only `insights_opportunities_v1`; no scheduler, provider marketplace, dynamic model chooser, or additional workflow before its pilot gate.

## Execution Frontier

Wait for `occupiedSeats=0`, then enter the full maintenance fence and take one matched data-volume/PostgreSQL snapshot. Revalidate the exact four-candidate manifest, append lifecycle records from the cold W1 backup, reconcile roll-forward to zero diff, deploy `a0ae9c9` with the Codex and render profiles, restart, and prove a second zero-diff reconciliation. Only then begin W2 shared evidence/temporal contracts and the dependency order in `docs/plans/bonfireos-w2-design.md`. The OpenAI quota canary remains an external recovery dependency, not authorization to weaken containment controls.
