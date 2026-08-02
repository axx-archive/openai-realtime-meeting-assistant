# E9 token-free local resilience and launch-readiness runbook

This runbook covers repository plans, a manifest-only contract exercise, and one real deterministic local integration drill. It does not provision managed PostgreSQL, create an offsite bucket, assign signing or KMS custody, prepare a restore host, shift live traffic, call a model provider, exercise WebRTC/TURN or a physical device, deploy a release, or qualify production. Those operations remain E10 gates.

The executable contracts are:

- `deploy/e9/readiness-plan.json` — closed infrastructure, availability, and capability-page plan.
- `deploy/e9/worker-isolation-policy.json` — disabled current worker state plus the externally pending per-run sandbox target.
- `deploy/e9/contract-drill.json` — a manifest-only two-room founder, failure, and workforce contract exercise.
- `internal/e9readiness` — strict decoders, validators, fail-closed readiness report, contract state machine, and narrow local-integration receipt validator.
- `stride_e9_failover_restore_integration_test.go` — the temp-only application/control/persistence integration drill invoked by the command.

Run the token-free check from the repository root:

```bash
go run ./cmd/e9-readiness
```

A successful command means that the closed manifests validate, no reusable Codex executor exists in the production-style Compose candidate, declared contract state transitions pass, and the local integration test produced a valid observed receipt. Its readiness and worker outputs must still say `activationReady:false` and `state:"external_pending"`; the contract drill must say `state:"contract_only"` and `productionReady:false`; the separate local receipt must say `evidenceClass:"local_deterministic_integration"`, `state:"passed"`, `tempResourcesOnly:true`, and `networkScope:"loopback_only"`. An unknown JSON field, missing capability, enabled activation, reintroduced Compose executor, incomplete worker target, missing safety assertion, invalid workforce transition, failed local observation, or missing measured timing exits nonzero.

The command creates a fresh directory under the operating system temp root, blanks every supported model-provider credential in the child test process, uses only loopback HTTP servers, and removes the directory after reading the receipt. It neither invokes Docker nor reads the repository's local `data/`, `/opt/meetingassist/data`, or the production volume. The local child boots `kanbanBoardApp` with canonical PostgreSQL off and STRIDE enabled over files beneath that temp directory.

## Infrastructure and traffic boundary

The HA plan requires two planned managed PostgreSQL nodes in distinct zones, provider failover evidence, and a purge authority outside the cluster. Recovery requires the four roots already sealed by `internal/dr`, encrypted immutable cross-region storage, separate KMS/signing custody, and a separate restore host that receives public verifiers only. None is represented as installed.

The availability plan names two app and two TURN replicas across two regions. Health routing, session drain, previous-release retention, media/app independence, and a rollback command are mandatory, but activation and traffic shift remain off. The definitions are intentionally provider-neutral; concrete endpoints, credentials, DNS, load-balancer rules, and provider resource IDs belong in an authorized E10 change and its signed evidence, never this repository plan.

Do not edit the manifest to say `active` or enable traffic. The E9 validator accepts only `external_pending`, `plan_only`, `activationDefault:"off"`, and `productionMutation:false`. E10 must introduce a separately reviewed activation contract instead of weakening this one.

## Worker isolation boundary

The production-style Compose candidate currently has no launchable Codex worker. The former long-lived `codex-runner` Compose service was removed because it held reusable provider credentials, mounted the broker queue and broad host workspace, and could not deliver the per-run isolation described by E9. The app may retain its durable queue as control-plane state, but no worker in this Compose file may mount or execute from it. `go run ./cmd/e9-readiness` audits this candidate and fails if the reviewed service set changes or if the named service, Codex runner build target, provider-key executor markers, or host-workspace executor markers return. Adding any Compose service therefore requires an explicit E9 manifest review instead of silently widening the deployment.

This is a deliberate availability trade: Codex-style execution remains unavailable until an external orchestrator is separately installed and qualified. The candidate removes the old Compose profile, Docker image target, and binary runner mode, and runner selection has a compile-time-closed production gate. Do not recreate those paths or interpret a queued job/heartbeat as worker-isolation evidence. Removing repository launch paths does not stop an already-running orphan container on any host; an authorized E10 cutover must inventory and remove legacy/orphan executors and capture that receipt.

The required external boundary is still strict: every run gets a fresh container and worktree, a read-only root, and only `/workspace/run` and `/tmp` writable. Production data, broker queue, Docker, PostgreSQL, DR evidence, and company-brain mounts are forbidden. Credentials must be run-bound, scope-limited, and expire within ten minutes. CPU, memory, PID, wall-time, and network-byte ceilings are mandatory. Completion callbacks must be signed and bind the run ID, audience, nonce, and short timestamp window; a durable replay cache must reject nonce reuse before accepting state. Reusable provider keys, canonical database URLs, and DR encryption/signing keys must never be injected.

`go run ./cmd/e10-worker-harness` now exercises those interfaces as a local,
token-free, no-network contract model. See
`docs/e10-external-worker-harness.md`. Its receipt proves reducer, fencing,
quota, kill-switch, and tamper behavior only; it does not change this policy's
`plan_only`/`external_pending` state or prove an orchestrator, container,
gateway, broker, durable callback store, or infrastructure quota enforcer is
installed.

Compose is not an egress allowlist. E9 therefore records `composeEnforced:false`, keeps the token-free allowlist empty, and keeps credential issuance disabled. Default-deny egress, DNS/IP/private-network filtering, an exact provider/source allowlist, and the network-byte ceiling require an external gateway or equivalent independently evidenced control. The repository manifest cannot become activation evidence for those controls.

Before any worker activation, E10 must provide all receipts named in `externalEvidenceRequired`: external orchestrator identity; create/destroy evidence for one container and worktree per run; read-only-root and mount inspection; default-deny gateway tests including IP literals, DNS rebinding/private networks, redirects, and metadata endpoints; short-lived run-bound credential issuance/revocation; CPU/memory/PID/wall/network quota enforcement; signed callback nonce/replay rejection; and proof that neither production nor company-brain volumes are mounted. The activation change must be a separately reviewed contract; do not flip this plan-only manifest to claim success.

## Capability-specific pages

An aggregate `/readyz` response cannot clear any capability page. Each page needs the named capability receipt, freshness, and owner-specific recovery proof.

### Canonical

Page Platform on write-fence, high-water, reconcile, or checkpoint divergence. Deny mutation, preserve journals and bytes, and diagnose without deleting evidence. Resume only after the canonical invariants reconcile at one captured high-water.

### Consent

Page Privacy when durable consent authority is missing, stale, or inconsistent with an active observation. Stop observation and specialist context immediately; do not infer consent from transcript text, presence, or aggregate health. Resume only on a fresh authoritative record.

### Transcript

Page Media on segment gaps, revision-order faults, or freshness breach. Mark transcription unavailable and preserve the call. Do not let stale or partial text become authoritative evidence.

### Analysis

Page Intelligence on projection lag, failed source coverage, or revision mismatch. Mark the analysis stale, expose its gaps, and keep source data unchanged. Rebuild only from authorized canonical sources.

### Brain

Page Intelligence on checkpoint divergence, rebuild non-parity, ACL differential, or purge propagation failure. Disable recall rather than returning mixed-authority results. Preserve lineage for diagnosis.

### Embeddings

Page Intelligence when coverage, source revision, index generation, or model revision is unknown. Disable the semantic lane and keep deterministic authorized fallback visibly distinct; never present missing semantic coverage as healthy.

### Scout

Page Product when the room-specific heartbeat, context envelope, roster, consent, or capability receipt is stale. Present Scout as unavailable, keep the human room working, revoke specialist floor/context, and never substitute aggregate service health.

### Workflow

Page Workflow on approval ambiguity, duplicated dispatch, expired authority, or non-terminal execution uncertainty. Block new work, preserve the visible blocked/ambiguous record, and never replay an external side effect speculatively.

### Queue

Page Platform on oldest-age, depth, lease, retry, or callback-replay breach. Stop admission while preserving existing jobs and receipts. A runner heartbeat alone does not clear the page.

### Backup

Page SRE on capture-barrier, manifest, authority pin, independent custody, object-lock, release identity, decrypt, or restore-receipt failure. Block release and restore boot. Follow `docs/bonfire-dr-restore-runbook.md`; never lower purge authority to make an old backup pass.

### Cost

Page FinOps on a missing price, untagged seat, accepted/rejected accounting gap, unexpected fallback, or ledger/console delta. Disable the affected seat and retain its receipts; do not estimate readiness from aggregate spend.

## Contract-drill interpretation

The contract drill uses a virtual clock, deterministic actors, two declared fixture tenants/rooms, Scout, and Mary. It covers declared consent withdrawal, quota exhaustion, Realtime disconnect, participant churn, app/control failover, restore tamper, and specialist kill switch. Each fault has mandatory safety assertions; the state machine also enforces the ordered Mary discover-through-offboard lifecycle. `declaredCoverage` is a checklist for a future integrated harness, not evidence that media, recall, `#team`, Scout, Suggested Work, I&O, Marketplace, or workforce code ran.

The contract reducer deliberately has no threshold-shaped timing assertions. `route_failover_expected` and `media_continuity_expected` are plan vocabulary only. The 24 hours and ten sittings are a proposed replay shape, not a completed live soak. The receipt must remain `contract_only`, list the two executed e9readiness components, explicitly exclude product integration claims, and list paid providers, physical devices, production restore, live traffic shift, and live soak as pending.

## Local integration interpretation

The separate local integration drill executes these concrete seams rather than reducing a manifest:

- Boots app replica A through the real `newKanbanBoardApp` and app-owned STRIDE runtime, applies a transcript plus decision, and persists an authenticated snapshot and monotonic generation ledger.
- Sends a real loopback HTTP session request through a small control router, stops replica A, boots replica B over the same durable files, observes the failed A route, and routes the request to B.
- Persists the selected route to a temp control-state file, replaces the control server, reloads that file, and observes routing to B after control restart.
- Creates two real app room-media generations and probes a separate loopback room-scope control server: the room's own lane returns 200, the same lane presented to the other room returns 403, and the room-A probe remains available while no app replica is running.
- Applies an actual `TemporalMeetingBrain` purge, persists it through the signed STRIDE snapshot, proves the derived decision is gone, and boots replica C from the current files with the purge still present.
- Pairs the old pre-purge signed snapshot with the current generation ledger and proves `NewSTRIDERuntime` refuses that stale rollback before it can become readable.

Every duration in `localIntegration.timings` is measured with the process monotonic clock around the executed operation. The command does not compare those values to a service-level target and does not turn them into production RTO/RPO, media-interruption, or availability claims. The room-scope probe proves only local control-routing isolation and control availability during the injected app gap; it does not send RTP, negotiate WebRTC, contact TURN, inspect a camera/microphone, or prove human-call continuity. The signed restore is local STRIDE file-state recovery, not the four-root/offsite/PostgreSQL production restore described by `docs/bonfire-dr-restore-runbook.md`.

## E10 handoff evidence

Before any launch claim, require current provider topology/failover receipts, immutable object version and independent custody receipts, authenticated restore on the separate host, measured RPO/RTO, reversible app/TURN/routing traffic shift with retained rollback, physical iPhone/iPad results, exact release identity, paid-route qualification, and the real 24-hour/ten-sitting soak. Preserve the production volume boundary documented in `AGENTS.md`: local and `/opt/meetingassist/data` files are not the live board.
