# bonfireOS 2.0 - Execution Ledger

Goal and source pointers: active `$goal-loop`; `docs/model-routing-master-plan-2026-07-11.md`; `docs/plans/multi-room-2026-07-08.md`; architecture audit in the current Codex task.

Current phase: pre-W5 release closure. W0 and W1 are live and verified; the W2 product/runtime slices and W4 secure-restore repository foundation have passed their independent code gates. The exact release is being pushed and installed default-off/shadow before quota-dependent commissioning begins.

## Invariants

- Production truth lives in Docker volume `digitalocean_meeting_data`; repository and `/opt/meetingassist/data/` content are never treated as live data.
- Preserve user-owned `README.md`, `stride-site/`, `design-system/`, and ignored native-Apple artifacts; none are part of this release.
- Private Scout threads remain owner-scoped; guest or retrieved content never authorizes tools.
- Video remains available when AI providers degrade; stale or partial AI output is always labeled.
- No force-accept critic path, no unattended external publish, and no silent authority escalation.
- JSONL, Pion, and prior per-seat model routes remain rollback paths until their replacements pass live gates.

## Wave Map

| Wave | Outcome | Dependencies | Gate / rollback | Status |
|---|---|---|---|---|
| W0 | Stop runaway spend; make output, authority, health, backup, and usage truth enforceable | None | Accepted digest advances cursor; code-level authority tests; offsite restore evidence; old env/code backup | Complete; model-output recovery canaries held for W5 |
| W1 | Canonical event/ACL substrate, object authorization, outbox/jobs, retention, consent, revision-bound approval | W0 | Dual-write replay/checksum and ACL-negative parity; JSONL reader rollback | Complete; live in PostgreSQL shadow mode with JSON/JSONL authoritative; expiry repair restart-proven |
| W2A | Per-room Scout, exact recap, guest policy, media backend pilot | W1 contracts | Two-room zero-leak live gate; Pion and feature-flag rollback | Repository-complete; actorized/scoped implementation independently passed; two-hour live soak held for W5 |
| W2B | Restart-safe brain, complete historical recall, claim/evidence lineage | W1 contracts | Recall corpus and restart/replay gates; shadow-reader rollback | Repository-complete; projection/backfill/retrieval gates passed; live 90-day replay held for W5 |
| W2C | `insights_opportunities_v1`, structured feedback, verdict critic, pilots | W1 + W0 route/authority | Ten reviewed pilots; process disable and route rollback | Repository-complete and default-off; durable executor/feedback/capability gates passed; human pilots held for W5 |
| W2D | Same-release evaluation, collector custody, cost derivation, and signed verdict receipts | W2A-W2C | Clean-commit and receipt-custody gate; no route mutation | Harness complete; live-provider corpora held for W5 |
| W3 | Static versioned model-route registry and measured canaries | W2 eval corpora | One seat at a time; prior route pointer rollback | Canary/rollback plan ready; route changes remain blocked on W5 receipts |
| W4 | HA/DR cutover and full operational release | W2 + W3 | Chaos, restore, live media/recall/workflow evidence; cutover rollback | Secure DR capture/manifest/restore gate repository-complete; managed HA, offsite immutable custody, and live restore drill remain external gates |
| W5 | Final AI commissioning after API quota is restored | Pre-W5 code installed; W4 infrastructure/custody and human reviewers available | Bounded provider canaries, same-commit live receipts, then one-seat-at-a-time enablement; all AI routes remain degraded/disabled on failure | Pending top-up and remaining external prerequisites |

## Current Wave

- Lead owns integration, Git, VPS configuration, restarts, migration cutovers, and release evidence.
- Subagents own disjoint code paths only; they do not stage, commit, push, deploy, or mutate production.
- W2 resumed on 2026-07-22. Independent audits found the existing W2A/W2B foundations, the absent W2C product, and the exact remaining gaps. Strategic design plus two critic rounds produced the approved W2 contract in `docs/plans/bonfireos-w2-design.md`, including W2D's complete pre-W3 evaluation wave.
- Resume inspection found W1 canonical shadow at four target-only `guest_link` objects. Cold W1 backup evidence proved the exact four rows expired on 2026-07-16/17; the current expiry sweep had removed them without a lifecycle journal. No target history gap, pending capture, outbox failure, or principal-parity defect was present.
- Commit `a0ae9c9` is pushed to `axx/main`. It journals future expiry before source removal, shares the importer digest projection, and recovers the journal-before-source-rewrite crash window before canonical boot. Focused normal/race tests, the full suite, and an independent adversarial re-gate passed.
- The historical backfill was executed under a full mutation fence after explicit interruption authority. Matched data-volume/PostgreSQL snapshot: `/opt/meetingassist-backups/20260722T153417Z-guest-link-parity-repair/matched-snapshot`. The operator repair refused expansion beyond the exact four cold-backup/canonical-candidate fingerprints, appended four `guest_link_expired` lifecycle records, and reconciled with zero divergence.
- Commit `a0ae9c9` was deployed with the Codex and render profiles. First live parity reached equal dirty/reconciled/checkpoint high-water 5,796; the independent PostgreSQL plus application restart proof reached equal high-water 5,804. Both had zero pending capture, outbox backlog/failures, or frozen families. App, PostgreSQL, Codex, render, Caddy, and coturn are running; both worker heartbeats are fresh.
- The live `rooms.json` SHA-256 remained byte-identical to the pre-repair matched snapshot, PostgreSQL contains exactly four deleted `guest_link` events, all 13 historical queue jobs remain, the room is empty, and the public host returns HTTP 200. The one-time repair test and image were removed.
- W2 shared foundation now has canonical evidence/revision/ACL/purge/trust references, half-open temporal and admission-relative queries, honest recall coverage, exhaustive terminal/count/manifest-proven inventory, local source-byte verification, boundary-safe byte-addressed clipping, and deterministic full-range prompt folding. Denied inventory cannot affect published counts or late-arrival state; guest/capability recall remains denied and service recall requires an explicit kind-bound ACL.
- W2 projection checkpoints bind tenant, projection, partition, version, generation, authoritative source range/manifest, derived high-water/digest, and an opaque fence token. Publication verifies the derived sink inside the same advisory-locked transaction; rebuild, race, crash/restart, tamper, and ambiguous-commit tests pass.
- W2A admission anchors now use a checksummed durable first-admission store and a separately durable monotonic capture sequence. Startup proves the exact atomic write path, runtime failure latches readiness, plaintext guest identifiers are refused, and live participant state is published only after the anchor persists under one visibility lock. Independent admission re-gate returned PASS.
- W2A now actorizes Pion and Scout by room, sitting, and generation; exact-scope fencing reaches transcription completion, attribution, durable commit, recap, chat, artifact fanout, and teardown/restart. The independent final critic returned PASS after focused normal/race, vet, and diff gates.
- W2B's production adapter, transactional projection queue, bounded admin backfill, exact as-of rebuild, catch-up publication, purge/ACL reauthorization, and restart readiness passed independent adversarial gates.
- W2C's default-off durable executor now implements the immutable two-revision Insights chain, one-use capability enforcement, typed feedback, crash recovery, provider boundaries, and fail-closed reviewer eligibility. Independent adversarial re-gate returned PASS; only the ten human-reviewed pilots remain.
- W2D's typed collector, local metric/cost replay, independent Ed25519/HMAC custody, clean-release binding, transitive input verification, and 48-hour receipt sets passed its repository gate. Synthetic or missing provider observations cannot qualify.
- W4's signed four-root capture, repeatable-read logical PostgreSQL digest, purge-authority continuity, isolated restore-only boot gate, and one-use release/environment/image-bound receipt passed independent normal/race/vet/diff gates. Managed HA, immutable offsite storage/KMS custody, and a real restore-host drill are not created by this VPS code deploy.
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

- Dedicated managed PostgreSQL HA, immutable private object storage, independent signing/KMS custody, a separate restore host, and redundant app/TURN/routing are not present. The W4 repository gate fails closed without them; provisioning and the real restore drill remain operational work outside this single-VPS deploy. Managed Valkey remains intentionally deferred.
- The OpenAI production project currently requires a user-reported balance top-up. Do not poll or run provider/model canaries until the user confirms it is ready; normal digest production remains disabled meanwhile.
- Guest consent/retention language needs an authorized business/legal owner.
- Native Apple GA requires signing team, privacy manifest decisions, and physical-device evidence; otherwise 2.0 must explicitly ship web-first with native labeled beta.

## W5 Final AI Commissioning Queue

Run this wave only after the OpenAI project used by the production key has usable API quota and the listed W4 infrastructure/custody prerequisites have owners. A balance change is not acceptance evidence; each check below must produce a fresh, same-release receipt. Keep media admission, video, deterministic workflows, and read-only company-brain access available while AI remains degraded.

1. Run one bounded provider canary for embeddings, Responses, Realtime voice, and transcription. Confirm successful usage attribution to the intended project/organization, model access, latency, and ledgered token/audio usage. Stop on `insufficient_quota`, permission, unknown-price, or model-access errors.
2. Re-run the frozen W2D live-provider corpora from the release-candidate commit. Verify collector custody, raw evidence, price derivation, baseline pass, and candidate verdict receipts; missing or synthetic samples remain non-qualifying.
3. Run the two-room 3x3 media soak from the same commit, including room-isolation canaries, head-of-line injection, Realtime/AI failure continuity, transcript/Scout fencing, resource limits, and recovery. Preserve the signed sanitized evidence packet.
4. Run the production-shaped 90-day recall replay and catch-up checks. Verify semantic retrieval, source/ACL/purge lineage, honest partial-coverage labels, exact-window late-join recap, and zero cross-room or guest leakage.
5. Execute the ten fixed-release `insights_opportunities_v1` pilots with at least two eligible human reviewers. Verify immutable evidence snapshots, bounded critic revisions, typed feedback, approval/capability enforcement, and no external write without current authority.
6. Only after the receipts above pass, exercise the static W3 route canaries one seat at a time. Compare quality, latency, failure, retry, and cost against the frozen baseline; retain the prior route pointer and automatically roll back any negative verdict.
7. Recheck `/readyz`, `/capabilities`, usage/price ledgers, provider console agreement, worker cursors, queue depth, and 24-hour/ten-sitting stability. Final commissioning fails closed if any AI lane is stale, unmetered, unattributed, or unable to reproduce its receipt.

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

Freeze and ship the intended W2-W4 repository changes to `axx/main`, install that exact commit on the VPS with all new AI/workflow/media-observer/restore paths default-off or shadow, and verify live readiness without touching `digitalocean_meeting_data`. Then stop. Resume at W5 item 1 only after the user confirms the production OpenAI project is topped up; do not infer provider recovery from a balance change alone.
