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
| Production | `672df6b5f97778c2` | Live `/healthz` identity after the isolated Scout P0 hotfix. Production now advertises `gpt-realtime-2.1`, `gpt-live-transcribe`, and `gpt-transcribe`; the sealed canonical repair release has still not been activated. |
| Production health | Traffic-ready, capability-degraded | `/capabilities` reports `trafficReady:true`, `ok:false`. Physical on-demand Scout voice and composer dictation succeeded, but the aggregate Scout/STT telemetry remains disconnected/degraded and STT retains the stale `2026-07-31T18:28:56Z` success marker; backup, brain, and recap evidence also remain degraded. Aggregate readiness is not acceptance. |
| P0 branch | `/tmp/meetingassist-scout-p0`, `codex/scout-p0`, checkpoint `78b4d1cae287cc9bab9ab1a1e2ffa6022f8e2ee3` on base `9217dbf` | Isolated, committed deterministic candidate. It is not published, provider-qualified, physical-device accepted, merge-approved, or deployable yet. |
| E10 integration | `/tmp/meetingassist-e10-integration`, `codex/e10-integration`, candidate `0783a1cf9c45c1e06e95e3b010c7eae055ef723d` on base `9217dbf` | Clean, committed security candidate. Focused normal/race, full repository, vet, diff, and independent critic gates pass. It is not published, externally provisioned, merge-approved, or deployable yet. |
| Combined release candidate | `/tmp/meetingassist-stride-rc`, `codex/stride-release-candidate`, code head `294049af92c1458fc7fcd760c8c3d0dde294876c` | Frozen isolated composition of the complete E10 chain, Scout P0, the iOS 26 WebRTC camera crash guard, exact file-dictation routing and Scout vocative correction, private home-thread flow, mobile thread editing/renaming, and long-thread rendering hardening. Full normal/race/vet, 377 mobile tests, TypeScript, Expo Doctor 20/20, release-simulator rendering, and the 240-message `#team` stress pass are green. `main` has not moved. |
| Specialist repair | `/tmp/meetingassist-e10-specialist`, `codex/e10-specialist`, base `0aa1b090687d7a5d9ed3c10c9d792ee9044a545c` | Ten tracked uncommitted files remain untouched as a superseded partial draft. Their reconciled authority contracts are already present in clean integration and the frozen candidate. |
| Registry repair | `/tmp/meetingassist-e10-registry`, `codex/e10-registry`, base `1d2f55cfa86e1f155b7b14d06c5928379e2b1ea3` | Eight tracked files plus untracked `meeting_specialist_qualification_bridge.go` remain untouched as a superseded partial draft. Their signed-evidence, expiry, and external ledger-head/CAS contracts are already present in clean integration and the frozen candidate. |
| Independent E10 verdict | `PASS` | No blocker, major, or minor finding remains in the committed E10 code checkpoint. External authority custody and activation remain separate default-off gates. |
| TestFlight | Stride 1.0.0 Build 29 | A fresh read-only EAS list confirms Build 29 remains the newest EAS build: `7857b9b2-2de8-4248-b1c6-a50c54f6ca97`, `FINISHED`, from `97ff340097253ff3ad98481226f6159c3ce206ae`. Apple build `6a870589-8448-44fb-99d6-cea2f5a9ebb4` was last verified `VALID`, non-expired, and in internal `Team (Expo)`; external `Bonfire` excludes it. The connected handset now carries a locally signed release candidate over the same bundle ID, not the TestFlight binary. No Apple/TestFlight state changed and physical-device acceptance is still open. |

No canonical repair, `main` push, migration, canonical release deployment,
provider qualification, E10 activation, or TestFlight upload is established by
this checkpoint.

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
   on `gpt-realtime-2` at `high` reasoning. The locally signed handset build
   initially had `EXPO_PUBLIC_NATIVE_REALTIME_VOICE_ENABLED` unset, so the
   cradle used its legacy record-and-upload loop instead of opening native
   Realtime. A second locally signed diagnostic build with that gate explicitly
   enabled is installed and open; a physical spoken-turn result is still
   required. The target is `gpt-realtime-2.1`, but only after an exact candidate
   passes the authorized provider contract, quality, cost, lifecycle, and
   physical-device gates.
2. **Composer dictation and meeting STT:** the physical iPhone successfully
   uploaded a server-derived `7.975`-second recording, after which the
   production usage ledger recorded model `gpt-realtime-whisper` and an exact
   provider `404 Invalid URL (POST /v1/audio/transcriptions)`. This proves the
   recorder and upload reached the server and that dictation incorrectly
   inherited a Realtime-only meeting-lane override. Commit `b358aa7` makes an
   unset dictation dial use the independently file-compatible
   `gpt-4o-transcribe` default; the explicit qualified target remains
   `gpt-transcribe`. The repair passes ten repeated normal runs and three
   race-enabled runs, but is not deployed. Meeting STT remains a distinct,
   disconnected/stale health and acceptance lane.
3. **Typed Scout:** production logged concrete Anthropic HTTP 400
   insufficient-credit failures. Ordinary Scout routing and answers must not
   depend on Anthropic credit or silently fall back across providers.

These observations prove degraded production behavior, one concrete typed
routing failure, a distinct handset feature-gate cause for the missing native
Realtime attempt, and a distinct endpoint/model mismatch for dictation. They
do not prove voice quality, dictation quality, one common root cause, or
qualification of a replacement route.

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

The intermittent physical-iPhone room crash is now independently localized to
five recent Build 27 `SIGABRT` reports on WebRTC's
`org.webrtc.RTCDispatcherCaptureSession` queue. The exact bundled
`JitsiWebRTC 124.0.2` dSYM resolves the aborting frame to M124's
`-[RTCCameraVideoCapturer updateVideoDataOutputPixelFormat:]`, where iOS 26's
adaptive front-camera format rejects the fixed output dimensions. Commit
`4c56ca5` pins that exact WebRTC binary and installs an iOS 26-only native
guard before capture: it omits the unsafe fixed-dimension rewrite only for the
front adaptive 16:9 format, contains any unexpected Objective-C settings
exception, and emits privacy-safe intervention telemetry. The exact generated
Expo/CocoaPods graph, simulator workspace, and signed physical-device Release
build pass; the candidate installs, launches, and remains alive on the
connected iPhone. Mobile passes 369/369 plus TypeScript, full Go and vet pass,
focused normal/race telemetry coverage passes, and strict Objective-C
compilation passes with warnings as errors. One physical room pass joined over
cellular, negotiated srflx/UDP, selected the front ultra-wide camera at
720x1280 and about 31 fps, emitted 118 outbound frames in the sampled interval,
left cleanly, kept the app process alive, and produced no new crash report.
Camera-toggle and rotation gestures were not independently observed, so full
physical room acceptance remains open.

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

The interrupted worktrees remain byte-for-byte preserved. Their repairs were
reconciled semantically onto the integration branch and sealed as `dad0070`
and `0783a1c`; the originals were not edited, reset, committed, or deleted.

The committed candidate now has one sealed provider-launch path, request-owned
startup cancellation without request-value leakage, exact signed provider,
model, voice, full endpoint, accounting, runtime, capability-policy, candidate,
release/tree/image/config, evaluator, and result binding, plus a fixed seven-day
expiry that is checked after authority I/O, provider creation, and briefing and
also bounds the live runtime. The full signed result is carried through runtime,
product, terminal evidence, signed snapshot, and restart. Snapshot format v2
migrates valid signed v1 history as explicitly qualification-unbound rather than
silently trusting it.

Qualification custody atomically supplies the approved trust-root pin and exact
ledger head; CAS requires that same root revision. Rejected CAS rolls back,
lost replies reconcile by reread, stale processes fail before append, root
rotation fences stale verification, third-state ambiguity poisons the process,
and all custody calls are bounded. No local/file production authority exists:
external custody provisioning remains a default-off release prerequisite.

The final E10 checkpoint passed focused normal and race coverage, `go vet
./...`, `git diff --check`, and a clean `go test ./... -count=1 -timeout 20m`
with the root package at 392.773 seconds. The independent critic returned
`PASS`.

The frozen combined code head `294049a` passes clean repository-wide normal
and race runs: the root package completed in 391.942 seconds normally and
2846.138 seconds under the race detector, with every command and internal
package green. `go vet ./...` and `git diff --check` also pass. Mobile has 377
passing tests, clean TypeScript, Expo Doctor 20/20, a successful release-mode
iOS simulator build, and a rendered 240-message `#team` stress pass with ten
consecutive upward-scroll actions and an unchanged visible message set across
background HTTP reconciliation. The thread uses FlashList recycling types,
stable row identities/callbacks, row-local unread markers, a memoized typing
footer, and one timestamp computation per bubble so unrelated composer,
typing, or reconciliation state does not rerender visible rich messages.

Physical Scout voice, dictation, message creation/answering, copy, deep edit,
and both thread-title rename paths passed on the connected iPhone during the
P0 session. Final TestFlight Build 30 and physical room-crash acceptance remain
separate post-deployment gates. The independent E10/composition critic remains
`PASS`; the later exact qualification-expiry terminal-cause assertion passes
repeated normal and race coverage.

## 7. Remaining dependency order

1. **Complete:** freeze the isolated combined candidate and run the complete
   non-`stride-site` normal/race/vet, mobile/TypeScript/Expo, and rendered
   release-simulator matrix.
2. **Next:** execute the manifest-confirmed canonical repair ceremony. Stop at each human
   and parity gate; do not reuse any retired release pair or failed ceremony.
3. Merge the verified scope, commit and push `main`, run only the reviewed
   migrations, deploy the exact artifact, and prove public commit/tree/image,
   health, capabilities, Scout, STT, canonical convergence, rollback, and data
   integrity.
4. Build the resulting mobile release (expected Build 30), submit it to internal
   TestFlight, verify Apple `VALID` and `Team (Expo)`, then complete physical
   device acceptance. Submission alone is not acceptance.
5. Finish external P0/E10 evidence: web and physical-iPhone Realtime/dictation/
   typed-Scout acceptance, bounded paid-provider qualification, real
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

Resume in `/tmp/meetingassist-stride-rc` at verified code head `4c56ca5`. The
remaining pre-ceremony P0 frontier is live-provider and physical-device
evidence: verify voice, dictation, and typed Scout as three separate lanes,
keep target routes default-off until qualification, and do not substitute
simulator/fake-server receipts. Refresh the physical iPhone connection and
obtain explicit authority before any paid provider attempt. If those gates
pass, freeze the exact release manifest and begin the user-assisted canonical
repair; otherwise remain default-off and preserve this candidate. External
custody, HA/DR, pilot, soak, and broader device gates remain open even after the
deterministic matrix passes.

No merge, push, migration, VPS mutation, provider call, TestFlight upload, or
canonical append belongs before that frontier passes and its separate authority
is current.
