# BonfireOS 2.0 — Execution Ledger

**Canonical design:** [STRIDE Next Evolution Master Plan](./stride-next-evolution-master-plan.md)

**Checkpoint date:** 2026-08-02

**Current wave:** E10, with Scout availability as the P0 safe frontier

**Overall state:** `external_waiting`; production remains on the retained legacy release

This is the durable resume ledger for the approved master plan. The master plan
owns product and architecture decisions; this file owns current execution truth,
dependencies, authority boundaries, rollback posture, and the next safe action.

## 1. Current verified checkpoint

| Surface | Exact state | Meaning |
|---|---|---|
| Local `main` | `9217dbf7a9f1a96ceed20f174fd2a9894daa29af` | Clean except separately owned untracked `stride-site/`. |
| Remote `axx/main` | `9217dbf7a9f1a96ceed20f174fd2a9894daa29af` | Read-only remote refresh matches local `main`. No E10 or P0 branch is published. |
| Production | `30eb8891dd74edda` | Live `/healthz` identity on 2026-08-02. This is the retained older release; the sealed canonical repair release has not been activated. |
| Production health | Traffic-ready, capability-degraded | `/capabilities` reports `trafficReady:true`, `ok:false`; Scout is disconnected/degraded and STT is disconnected/stale since `2026-07-31T18:28:56Z`. Aggregate readiness is not acceptance. |
| P0 branch | `/tmp/meetingassist-scout-p0`, `codex/scout-p0`, candidate `13a3ef2ea7bd8031a0717616d11373482a598dcd` on base `9217dbf` | Isolated, committed deterministic candidate. It is not published, provider-qualified, physical-device accepted, merge-approved, or deployable yet. |
| E10 integration | `/tmp/meetingassist-e10-integration`, `codex/e10-integration`, `f808a0e41361f6a98ca5d1ef6db64a6c464fac2a` | Clean follow-up foundation: worker isolation, qualification registry, and specialist-agent join work. Not ready for `main`. |
| Specialist repair | `/tmp/meetingassist-e10-specialist`, `codex/e10-specialist`, base `0aa1b090687d7a5d9ed3c10c9d792ee9044a545c` | Nine tracked files contain preserved uncommitted removal of the legacy alternate join path and request-context leakage. |
| Registry repair | `/tmp/meetingassist-e10-registry`, `codex/e10-registry`, base `1d2f55cfa86e1f155b7b14d06c5928379e2b1ea3` | Seven tracked files plus untracked `meeting_specialist_qualification_bridge.go` preserve signed-evidence binding, expiry, and external ledger-head/CAS work. |
| Independent E10 verdict | `REVISE` | Neither interrupted repair is approved for integration or `main`. |
| TestFlight | Stride 1.0.0 Build 29 | Built from `97ff340097253ff3ad98481226f6159c3ce206ae`; EAS `7857b9b2-2de8-4248-b1c6-a50c54f6ca97` finished, Apple build `6a870589-8448-44fb-99d6-cea2f5a9ebb4` was last verified `VALID`, non-expired, and in internal `Team (Expo)`. External `Bonfire` excludes it. Physical-device acceptance is still open. |

No new migration, push, deployment, provider qualification, feature activation,
or TestFlight upload is established by this checkpoint.

## 2. Non-negotiable invariants

- Conversation is evidence, not authority. Every approval remains explicit,
  revision-bound, attributable, and revocable.
- Private Scout threads remain owner-only. Retrieval, attachments, citations,
  artifacts, search/history, and company-brain projection must preserve the
  same ACL and consent intersection at write, model call, projection, and open.
- Human chat, video, and meeting media remain usable when any AI lane fails.
- No silent provider, model, effort, tool-policy, or coverage fallback is
  allowed. A failed or unqualified lane stays visibly degraded or unavailable.
- One model, effort, prompt, schema, or tool-policy variable changes per canary.
- `stride-site/` and unrelated user work stay outside every commit, manifest,
  sync, migration, and release boundary unless separately authorized.
- Local or synthetic receipts may be `implemented` or
  `deterministic_verified`; they are never relabeled `provider_qualified`,
  physical-device accepted, production-enabled, or launch-accepted.

## 3. P0 incident truth

The incident has three separately gated lanes; one shared cause is not claimed.

1. **Realtime voice to Scout:** production is enabled but disconnected/degraded
   on `gpt-realtime-2` at `high` reasoning. The target is
   `gpt-realtime-2.1`, but only after an exact candidate passes the authorized
   provider contract, quality, cost, lifecycle, and physical-device gates.
2. **Composer dictation and meeting STT:** production still exposes the legacy
   `gpt-realtime-whisper` meeting lane and `gpt-4o-transcribe` voice
   transcription configuration. Meeting STT is disconnected and stale. The
   target authoritative model is `gpt-transcribe`; dictation and meeting STT
   remain distinct health and acceptance lanes.
3. **Typed Scout:** production logged concrete Anthropic HTTP 400
   insufficient-credit failures. Ordinary Scout routing and answers must not
   depend on Anthropic credit or silently fall back across providers.

These observations prove degraded production behavior and one concrete typed
routing failure. They do not by themselves prove voice quality, dictation
quality, one common root cause, or qualification of a replacement route.

## 4. Frozen model-seat contract

| Seat | Target route | Activation boundary |
|---|---|---|
| Personal and meeting Scout voice | `gpt-realtime-2.1` | New-session cohort only after exact contract, audio, interruption, usage/cost, ACL, and physical-device qualification. |
| Meeting STT | `gpt-transcribe` | Authoritative final transcript gate over the consented corpus; exact item correlation, ordering, fidelity, latency, accounting, and privacy required. |
| Composer dictation | `gpt-transcribe` | Separate web/iPhone/iPad corpus and exactly-once, recoverable-recording, lifecycle, latency, and UI acceptance required. |
| Typed Scout router | `gpt-5.6-terra`, `low` | Strict structured route; proposals only, never implicit launch. No Anthropic fallback. |
| Typed Scout answer and routine `#team` chat | `gpt-5.6-terra`, `low` | Grounded, ACL-safe Responses path with explicit degraded state. |
| Extraction and projections | `gpt-5.6-luna`, `low` | Bounded structured extraction with evidence and freshness checks. |
| Goal/work orchestration | `gpt-5.6-sol`, `medium` | Durable approved-work stages; high-value generation may use Sol `high` only under its own receipt. |
| Independent review | Separate provider/model family when available | Optional independent Anthropic review may remain, but it cannot own Scout availability or become an automatic fallback. |

Model-object access and synthetic contract receipts are not seat qualification.
Every live route still needs exact project/billing, pricing, accepted-output,
corpus, rollback, and downstream-replay evidence before activation.

## 5. Isolated P0 implementation state

The isolated `codex/scout-p0` candidate `13a3ef2e` implements:

- OpenAI Responses ownership of ordinary Scout routing and answers: Terra
  `low` for router/chat, Luna `low` for attachment extraction, strict router
  schema, Responses-native image/PDF input, and no automatic Anthropic route for
  core Scout availability.
- Separate capability rows and safe milestones for room voice, private voice,
  meeting STT, dictation, typed Scout router, and typed Scout answer while
  retaining backward-compatible aggregate fields.
- Dictation and transcription telemetry/accounting hooks, plus native Realtime
  transport milestones and explicit failure reporting.
- The server contract for atomic home opening: one private thread, one opening
  user message, one durable Scout reply lifecycle placeholder, idempotency and
  restart-safe background completion, safe failure/retry, lease recovery, and
  owner-only live delivery.
- Native home submission stops Realtime, retains the exact draft/idempotency
  key on failure, clears only after durable acceptance, navigates immediately
  to the bottom-first Thread route without a historical message target, and no
  longer renders a question/answer transcript on home.
- Web home now uses the same atomic private-opening contract and stable retry
  key. It preserves the opening draft on failure, stops personal Realtime,
  navigates immediately into the private thread, renders the durable
  queued/running/failed/canceled lifecycle with accessible live status and
  retry, and labels ordinary replies only as Scout rather than asserting an
  unattested provider/model.
- Web Realtime and composer dictation now share the same audio-focus
  coordinator in both directions; starting either surface stops/parks the
  other without discarding a held recording.

The independent backend and UX code-artifact critics now report `PASS`; all
raised lifecycle, causal-history, telemetry-authority, provider-label,
touch-target, and mobile-containment findings were repaired. The P0 worktree
passes the targeted root Go suite, targeted atomic-opening race suite,
`go vet ./...`, official Responses image/PDF contract tests, web dictation
coordinator suite (20/20), mobile TypeScript check, and full mobile suite
(368/368). The first repository-wide run exposed seven real regressions in the
keyed offer-never-deny prompt and coworker GIF/file exact-once paths. The fixes
now gate capability offers on OpenAI core availability rather than Anthropic,
and separate upload/render-safe GIF storage from the narrower model-forwarding
MIME set. All seven regressions pass focused normal and race coverage. A clean
`go test ./... -count=1 -timeout 20m` rerun passed every package, including the
root package in 385.575 seconds; the post-fix `go vet ./...` and
`git diff --check 9217dbf` gates also pass.

Rendered local acceptance now covers desktop web at 1280x720, exact mobile web
at 390x844, and the current native branch in the installed iPhone 17 Pro
simulator development shell. Web and native both proved transcript-free home,
atomic navigation, auto-title, a visible running placeholder, and replacement
by one completed Scout answer against an isolated local fake Responses server.
The 390px pass also found and repaired a clipped home composer and wrapped phone
placeholder. These are synthetic/rendering receipts only: the physical iPhone
is currently visible but offline, live Realtime/dictation/provider quality is
not accepted, and the fake local provider response does not qualify any seat.
No P0 change is on `main` or production, and no target model is
provider-qualified by this work.

The approved home behavior remains:

- large waveform starts live Realtime Scout;
- composer mic is dictation only;
- typed or dictated send atomically creates a private Scout thread, persists
  the first message, navigates there, streams the answer, and auto-titles from
  the opening message;
- home never becomes a transcript-like chat surface;
- dictation stops active personal Realtime; personal Realtime or video join
  stops dictation;
- dictation exposes recording controls and `Transcribing`; failure preserves a
  recoverable recording/draft rather than silently losing it.

## 6. E10 convergence state

The integration branch already contains patch-equivalent versions of the clean
worker, specialist, and first registry commits; do not cherry-pick those commits
again. When P0 is stable, reconcile from `f808a0e` in this order:

1. Preserve and review both dirty worktrees before rebasing or applying patches.
2. Apply the specialist dirty repair first; it previously applied cleanly to
   the integration base.
3. Integrate the registry repair semantically. Its tracked patch conflicts in
   `meeting_specialist_realtime_adapter.go` and its snapshot was not a complete
   compiling unit, so mechanical patch application is not acceptance.
4. Remove every alternate provider-launch path and finish request-context
   ownership.
5. Bind signed evidence to the exact provider, model, voice, config, candidate,
   and fixed freshness window.
6. Finish the externally anchored ledger head with compare-and-swap semantics.
7. Run an independent red-team/critic gate. The current verdict stays
   `REVISE` until that evidence passes.

## 7. Remaining dependency order

1. Independently review the isolated Scout P0 branch. Complete the full
   normal/race/vet and rendered browser/simulator matrix, then reproduce and
   qualify web/physical-iPhone voice, dictation, and typed Scout as separate
   lanes before activation.
2. Reconcile the two interrupted E10 repairs onto `f808a0e`; complete the
   security convergence and independent red-team pass.
3. Freeze one candidate and run the complete non-`stride-site` normal, race,
   vet, dependency, mobile/TypeScript, simulator, rendered desktop/mobile,
   authority, restart, migration, release-pack, and founder matrix.
4. Execute the manifest-confirmed canonical repair ceremony. Stop at each human
   and parity gate; do not reuse any retired release pair or failed ceremony.
5. Merge the verified scope, commit and push `main`, run only the reviewed
   migrations, deploy the exact artifact, and prove public commit/tree/image,
   health, capabilities, Scout, STT, canonical convergence, rollback, and data
   integrity.
6. Build the resulting mobile release (expected Build 30), submit it to internal
   TestFlight, verify Apple `VALID` and `Team (Expo)`, then complete physical
   device acceptance. Submission alone is not acceptance.
7. Finish external E10 evidence: bounded paid-provider qualification, real
   WebRTC/TURN device matrix, ten immutable I&O pilots with two eligible
   reviewers, 24-hour/ten-sitting soak, encrypted immutable offsite backup,
   authenticated restore, HA/DR, and independent anchor custody.

Do not merge later gates into earlier ones. A release build cannot waive
provider, canonical, data, device, restore, or owner evidence.

## 8. Production data and release provenance

- Live production state exists only in Docker volume
  `digitalocean_meeting_data`, mounted at `/app/data`; on the VPS it is
  `/var/lib/docker/volumes/digitalocean_meeting_data/_data/`.
- `/opt/meetingassist/data/` and the repo `data/` are stale seed/dev artifacts,
  not production truth. Every rsync must exclude `data/`.
- Before replacing VPS files, create a timestamped backup under
  `/opt/meetingassist-backups`. Before migrations or canonical repair, take and
  attest the required cold named-volume backup and restore rehearsal.
- Bind the reviewed commit/tree, source archive, build manifest, image digest,
  runtime binary, configuration names without secret values, migration hashes,
  and public release identity. Source-file parity alone is not serving-artifact
  proof.
- Preserve the retained rollback release and never restore behind purge or
  consent authority. Public reopen requires exact canonical/data parity and
  production observation, not merely HTTP 200.

## 9. Operations and authority

Existing shipping authority remains bounded by every documented gate. It does
not authorize another paid provider call, activation of an unqualified route,
a hidden fallback, or bypass of canonical, consent, data, device, or release
evidence. Reconfirm current quota/project/billing and obtain explicit authority
before any new paid provider attempt.

The canonical ceremony requires the user at the shared terminal twice:

1. enter the hidden roster password without recording it in Git, logs, this
   ledger, or an agent message;
2. after the clean clone receipt and private repair manifest are displayed,
   provide the exact manifest-bound confirmation. Broad migration/deployment
   approval does not substitute for this data-history decision.

If either input is unavailable or any manifest, clone, count, source state,
backup identity, or no-extra-delta check differs, stop fail-closed and preserve
the retained production release. Physical iPhone acceptance also requires the
user/device owner; it cannot be inferred from simulator or Apple processing.

## 10. Resume here

Resume in `/tmp/meetingassist-scout-p0`. The code, rendered, focused race/vet,
mobile, and full repository gates are green; freeze this exact isolated
candidate without changing production routes. The remaining P0 frontier is
live-provider and physical-device evidence: verify voice, dictation, and typed
Scout as three separate lanes, keep target routes default-off until
qualification, and do not substitute simulator/fake-server receipts. Code-level
P0 convergence may now move to semantic E10 reconciliation on `f808a0e` while
those external acceptance gates remain explicit.

No merge, push, migration, VPS mutation, provider call, TestFlight upload, or
canonical append belongs before that frontier passes and its separate authority
is current.
