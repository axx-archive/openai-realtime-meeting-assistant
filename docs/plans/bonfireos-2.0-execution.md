# bonfireOS 2.0 - Execution Ledger

Goal and source pointers: active `$goal-loop`; `docs/model-routing-master-plan-2026-07-11.md`; `docs/plans/multi-room-2026-07-08.md`; architecture audit in the current Codex task.

Current phase: W0 is live and verified. W1 canonical event/ACL implementation is in progress; its first slice hardens legacy file durability before shadow capture can be trusted.

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
| W1 | Canonical event/ACL substrate, object authorization, outbox/jobs, retention, consent, revision-bound approval | W0 | Dual-write replay/checksum and ACL-negative parity; JSONL reader rollback | In progress: W1A durability/contracts |
| W2A | Per-room Scout, exact recap, guest policy, media backend pilot | W1 contracts | Two-room zero-leak live gate; Pion and feature-flag rollback | Pending |
| W2B | Restart-safe brain, complete historical recall, claim/evidence lineage | W1 contracts | Recall corpus and restart/replay gates; shadow-reader rollback | Pending |
| W2C | `insights_opportunities_v1`, structured feedback, verdict critic, pilots | W1 + W0 route/authority | Ten reviewed pilots; process disable and route rollback | Pending |
| W3 | Static versioned model-route registry and measured canaries | W2 eval corpora | One seat at a time; prior route pointer rollback | Pending |
| W4 | HA/DR cutover and full operational release | W2 + W3 | Chaos, restore, live media/recall/workflow evidence; cutover rollback | Pending |

## Current Wave

- Lead owns integration, Git, VPS configuration, restarts, migration cutovers, and release evidence.
- Subagents own disjoint code paths only; they do not stage, commit, push, deploy, or mutate production.
- Live containment applied at `2026-07-12T19:11Z`: `MEETING_DIGEST_DISABLED=true`; env backup at `/opt/meetingassist-backups/20260712T191102Z-digest-containment/env.before`.
- Digest usage baseline after containment: 573 calls, last call `2026-07-12T19:07:49Z`, app-estimated cost `$61.80095` for 2026-07-12.
- Digest count remained exactly 573 through `2026-07-12T21:30Z`; persisted digest count remained 5 across 2 meetings. No post-containment spend occurred.
- Combined W0 implementation now includes strict Responses JSON Schema, 4,000-token reasoning/output headroom, accepted-vs-wire usage truth, poison-output circuit, provider-outage cursor hold, positive-evidence capability health, principal-aware tool policy, exact callback binding, monotonic queue status, and hardened runner isolation.
- Codex production queue has 13 completed jobs and zero nonterminal jobs. Cutover must preserve those records, migrate usage books, and recreate the app plus profiled runner together.

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

## Pending Dependencies

- Dedicated managed PostgreSQL HA and private object storage are not present. The recommended W1 minimum is approximately $53/month ($48 PostgreSQL plus $5 Spaces) and remains blocked on explicit recurring-cost approval. Managed Valkey is intentionally deferred; PostgreSQL leases/outbox are the W1 design.
- OpenAI quota is again exhausted after the digest storm; Realtime/transcription cannot be called operational until billing is restored and the repaired lanes are canaried.
- Guest consent/retention language needs an authorized business/legal owner.
- Native Apple GA requires signing team, privacy manifest decisions, and physical-device evidence; otherwise 2.0 must explicitly ship web-first with native labeled beta.

## Operations And Authority Queue

- Authorized by current objective: scoped code edits, tests, commit/push to `axx/main`, VPS deploy/restart, and production configuration needed to make 2.0 fully live.
- Before irreversible data cutover: encrypted offsite restore drill, cold volume snapshot, dual-write high-water marks, empty-room window, and explicit rollback command.
- External resource provisioning is allowed only where credentials and account authority are already available; missing credentials remain a reported dependency rather than a local-only substitute.

## Risks And Decisions

- Current digest failures were systemic truncation/parse failures, not provider transport failures. Identical rejected model output uses a bounded circuit; provider/quota/transport failures use capped probes while holding the cursor forever and can never consume the poison-input dead-letter budget.
- Cost/eval books move to a dedicated `usage_ledger` volume nested at `/app/data/usage` for the app and mounted alone at `/app/usage-ledger` for the runner. Migrate the existing production usage directory before recreation; never restore runner access to the whole company-brain volume.
- The HMAC callback contract requires app and runner to cut over together with `docker compose --profile codex ...`; a standard profile-less restart is a no-go.
- LiveKit is a gated production-default candidate, not a foregone cutover. Pion remains per-sitting rollback; if LiveKit cannot pass the gate, Pion must be actorized per room before a 2.0 availability claim.
- Use a modular Go control plane with durable shared state; do not split into a microservice fleet or add Kafka.
- Implement only `insights_opportunities_v1`; no scheduler, provider marketplace, dynamic model chooser, or additional workflow before its pilot gate.

## Resume Here

Implement W1A in this order: harden legacy file/append durability; land canonical contracts, deterministic normalization/replay, and recoverable framed shadow capture; then prove migration-fence failpoints and PostgreSQL integration against disposable infrastructure. Commit and push scoped slices without staging user-owned Superdesign or mobile-room work. Provision managed PostgreSQL/Spaces only after explicit cost approval. Run a successful read-only job and one-meeting digest recovery canary after OpenAI quota is restored; normal digest concurrency stays disabled until both pass.
