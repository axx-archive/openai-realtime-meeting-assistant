# STRIDE 2.0 / BonfireOS Closeout Ledger

Last reconciled: 2026-08-03

Canonical design source: `docs/plans/stride-next-evolution-master-plan.md`

Current objective: ship the latest coherent E0-E10 code to the VPS and the corresponding iOS build to internal TestFlight, while preserving production data and keeping unfinished capabilities safely gated.

This file is the canonical execution and resume ledger. The master plan remains the authority for architecture and acceptance intent. Older checkpoints are available in Git history and are not repeated here.

## Current phase

E10 final incident repair and exact release closeout.

The current production artifact is healthy enough to serve traffic, but it is not the final intended release. The frozen local Build 32 candidate contains the cross-client Scout participant/audio repair, room-chat `@Scout`, direct Reply-to-Scout behavior, the Country Golf file/work lifecycle repair, and the desktop/mobile cradle startup/render repairs. AJ approved the reconciled ledger on 2026-08-03. The complete deterministic/race/mobile/media matrix and rendered desktop/iOS qualification are green; creation of the exact release SHA, VPS activation, and corresponding TestFlight artifact remain.

## State vocabulary

- `implemented`: the code and contracts exist.
- `deterministic_verified`: the implementation passed the applicable deterministic/local suites at its recorded checkpoint.
- `deployed`: the code is present in the serving artifact.
- `enabled`: the production feature flag or runtime domain is active.
- `qualified`: the required provider, evidence, or operational gate has been satisfied.
- `accepted`: the intended rendered or physical-device behavior has been observed.

These states are not interchangeable. In particular, most E1-E9 contracts are deployed but deliberately default-off; that is preserved safety, not missing code.

## Non-negotiable invariants

1. Preserve the Docker volume `digitalocean_meeting_data`; never deploy local or VPS `/opt/meetingassist/data/` as production truth.
2. Preserve unrelated untracked `stride-site/` work and all unrelated dirty-tree changes.
3. Do not execute the historical canonical Board repair. AJ has retired it from this release path. Preserve Board data and history; any full Board removal is a separate, explicit migration.
4. A known Board-only canonical mismatch must not be represented as a Scout, STT, or human-media failure.
5. Human room audio, video, chat, and transcription must continue if an AI participant or provider fails.
6. Transcript recording and an explicitly invited Scout model-analysis lane are separate consent-controlled states.
7. Specialist employee agents remain unavailable by default until their exact provider/model/voice/config evidence is qualified.
8. No production deploy while a live room is occupied. Drain or wait; do not interrupt a meeting.
9. Bind every release claim to an exact Git SHA, source archive/image identity, public response, and—on iOS—Apple processing and intended-group evidence.
10. A local build, EAS `FINISHED`, upload start, deploy start, `READY`, or aggregate health response alone is not release completion.

## Current truth

| Surface | Verified state | Consequence |
|---|---|---|
| Local `main` | `13b0797f18c495f3e8daa4c2df872ea1548f5926` | Current committed baseline. |
| Remote `axx/main` | Exact match to local `main` | No committed divergence. |
| Local working tree | A frozen uncommitted Build 32 candidate contains the room Scout incident repair, web/mobile room `@Scout`, direct Reply-to-Scout engagement, room-scoped chat recall hardening, ACL-bound Files retrieval, missing-file admission, durable work status, and desktop/mobile cradle repairs; unrelated `stride-site/` is untracked | Deterministic and rendered qualification is green. The final SHA does not exist yet. |
| Public VPS | Serves exact `13b0797f18c495f3e8daa4c2df872ea1548f5926`; `/healthz` and `/readyz` pass; traffic-ready | Current committed baseline is live, but not the final candidate. |
| Public capabilities | Aggregate degraded; room Realtime and typed Scout lanes have degraded observations; meeting STT remains disconnected/stale; Board canonical shadow has seven historical conflicts | Final verification must refresh each required lane after activation, not hide failures behind aggregate status. |
| Production models | Realtime `gpt-realtime-2.1`; committed meeting/dictation `gpt-transcribe`; live caption `gpt-live-transcribe`; Scout text on OpenAI Terra with brain/extraction on Luna | The intended core OpenAI availability architecture is configured. Runtime behavior still needs final acceptance. |
| Runtime activation | STRIDE conversation/brain/registry/marketplace/workforce domains and advanced cohorts remain default-off | Code is deployed; broad production activation is not claimed. |
| Latest EAS artifact | iOS Build 31, EAS ID `f5403d3d-5167-40d2-a738-f6d95a32d3e9`, `FINISHED`, exact baseline SHA `13b0797...` | Useful checkpoint only; it does not contain the pending fixes. |
| Apple/TestFlight | Build 31 submission was initiated for `Team (Expo)`; current Apple `VALID` and group availability have not been independently re-established | Do not count Build 31 as the final mobile milestone. |

## E0-E10 reconciliation

| Wave | What is present and previously proven | What remains open | Final-release impact |
|---|---|---|---|
| E0 — safety foundation | Release identity, production-data separation, consent lanes, attachment/provider containment, deterministic recovery contracts, and operator controls are implemented. The committed line contains the prior deterministic checkpoint. | Immutable encrypted offsite custody, independent restore proof, external KMS/custody, and final business-policy approvals remain external. Board repair is removed from this release. | Revalidate touched consent/media behavior. External resilience gates remain truthfully unqualified. |
| E1 — canonical contracts | Conversation/evidence/route/profile/package/listing/team/learning schemas, replay, ACL, purge, and dual-write/shadow infrastructure are implemented and default-off. | Production cohort activation and live convergence evidence remain gated. Seven legacy Board-only shadow conflicts are preserved, not repaired. | Code may ship default-off. Do not claim production activation or canonical convergence for retired Board data. |
| E2 — media and voice | Realtime, transcription, dictation, floor control, consent-aware media lanes, interruption handling, and model routing are implemented; narrow physical voice/dictation paths have worked during this release cycle. | Current invited-Scout audio lane repair must be finalized. Final iPhone/desktop cross-client, recording-off, interruption, TURN, and longer-session acceptance remain. | Direct blocker for final candidate acceptance. |
| E3 — temporal brain | Temporal meeting intelligence, recall, provenance, restart fixtures, and ACL-aware retrieval are implemented behind gates. | Live corpus quality, completeness, retention, and production cohort evidence remain. | Ship default-off; run only bounded release smokes. Broader qualification remains external. |
| E4 — team and rich collaboration | `#team`, rich media, ACL/private-share foundations, collaboration memory, mobile bottom-first threads, long-thread optimization, and desktop/mobile shells are implemented. | Final Apple-tier long-feed physical acceptance, rendered desktop regression check, locked-device/push and provider-specific rich-media gates remain. | `#team` smoothness and desktop cradle rendering are in the final acceptance matrix. |
| E5 — Scout and specialists | Scout exists across home, private threads, rooms, text, dictation, and explicit Realtime participant flows. Specialist worker/isolation, registry, signed evidence, and agent-join foundations were merged. The local candidate adds the cross-client tile/audio repair, server-owned room-chat `@Scout`, direct Reply-to-Scout engagement, and exact ACL-bound Files/work context. | Verify the candidate: Scout must remain visible to every participant, respond while invited, answer authorized room mentions, suppress acknowledgement-only thread noise, ask for unreadable dependencies, and return approved work through durable status cards. Employee specialists remain default-off pending qualification. | Direct qualification blocker; implementation is locally prepared and focused regressions pass. |
| E6 — Suggested Work | Durable orchestration, approvals, receipts, budgets, replay, and default-off execution contracts are implemented and deterministically checked. | Live production activation and operating evidence remain. | Ship default-off; no new release work unless regression is found. |
| E7 — I&O | Immutable workflow/evidence contracts and deterministic fake-stage paths are implemented. | Ten real pilots with two independent reviewers and bounded provider evidence remain. | External qualification, not a blocker to shipping the gated code. |
| E8 — marketplace/workforce | Registry, listings, qualification, workforce, budgets, isolation, and internal-preview contracts are implemented; specialist launch paths were converged. | Five fully qualified live listings, exact economic/provider evidence, marketplace admission, and employee-agent activation remain. | Ship default-off. Do not advertise Mary or other persistent employee agents as production-ready. |
| E9 — recovery, security, native | Recovery/HA/security/native harnesses, simulator/loopback matrices, and deterministic operational checks are implemented. | Production HA, measured RPO/RTO, immutable offsite backup, full physical-device/TURN matrix, 24-hour/ten-sitting soak, and external custody remain. | No silent claim of HA/DR or full mobile acceptance. Final release still requires a bounded real-device pass. |
| E10 — integrate, qualify, release | E0-E9 branches were converged into `main`; the current exact baseline is deployed; Build 31 exists; security convergence and operator-pack repairs are in the committed lineage; the final incident candidate is deterministic-verified and rendered-accepted locally. | Create the exact final commit, deploy and prove it, create Build 32, verify Apple `VALID` plus `Team (Expo)`, and perform the exact-build physical acceptance. Broader qualification items remain in the external queue. | This is the active closeout wave. |

## What the audit found

No E0-E10 implementation branch was lost: the verified integration/candidate work and the interrupted E10 security repairs are ancestors of current `main`. The old worktrees are historical/superseded evidence, not alternate release roots.

Four distinctions had become blurred and are now explicit:

1. The current public VPS contains the committed E0-E10 baseline, but not the pending room Scout incident repair.
2. Deployed advanced STRIDE contracts are mostly default-off; they are not broadly production-enabled or externally qualified.
3. Build 31 is an artifact checkpoint, not the final mobile release and not proof of current Apple availability.
4. The historical Board canonical repair is not part of closeout. Board data stays intact while Board retirement is designed separately.

The newly identified product gaps in the requested release scope—server-owned room-chat `@Scout`, implicit engagement when replying directly to Scout, filename-only false promises, Drive/Files visibility, and durable work status—are now implemented in the local candidate alongside the cross-client visibility/audio and desktop cradle repair. Focused regressions pass; the full qualification matrix remains. All other unresolved master-plan items are either final regression/acceptance work or explicitly external qualification/operations work.

## Minimal closeout wave map

### W0 — Freeze and complete the final product candidate

Status: implementation complete locally; AJ approved the ledger; focused compile/regressions pass and the full matrix is next.

1. Finish the existing consent-safe room Scout audio repair.
2. Finish desktop/mobile cross-client Scout participant projection and obvious invite/dismiss controls.
3. Implement authenticated room-chat `@Scout` as a server-owned, room-scoped response:
   - ordinary human room chat is unchanged;
   - the answer uses shared-room ACL context only;
   - the answer is attributed to Scout and persisted/broadcast like a room message;
   - text mention does not invite, speak through, or duplicate the Realtime participant;
   - guest mentions do not spend provider capacity or gain company-brain access;
   - failure produces a clear transient error without affecting human media.
4. Add discoverable `@Scout` completion on web and mobile.
5. Treat a long-press Reply to a Scout-authored channel message as an explicit Scout turn without requiring another `@Scout`; Scout reads the threaded context and suppresses acknowledgement-only noise.
6. Make Files a first-class Scout source:
   - resolve only ACL-authorized Drive/chat-file identities;
   - pin exact readable content into Q&A and an approved worker instead of relying on fuzzy recall;
   - expose an authorized Files catalog for direct Files questions;
   - never expose the catalog or file bodies to guests.
7. For file-dependent work, distinguish a visible filename from readable contents. If the PDF/deck is missing, filename-only, oversized, or stored without ingestion, Scout asks for the exact dependency and states that nothing is running.
8. A readable, explicit review creates a source-bound proposal. Once approved, one durable thread card shows queued/running/ready and the completed artifact is delivered back into the same thread; explicit save-to-Files policy remains intact.
9. Freeze scope. Do not add Board retirement, marketplace activation, or broader employee-agent behavior to this release.

Exit: one reviewable local candidate diff exists; Go compile, mobile typecheck, exact Files/admission/proposal/running-card regressions, and existing artifact-delivery regressions pass. Full W1 qualification remains.

### W1 — Deterministic and rendered qualification

Status: complete for the frozen Build 32 candidate.

Run the current-candidate normal/race/vet/mobile/rendered matrix, including:

- Go unit/integration/race suites and consent-lane regressions;
- frontend static/contract suites;
- mobile typecheck/tests;
- room-media failure containment;
- server-scoped `@Scout` mention ACL, stale-sitting, guest, duplicate, provider-failure, and ordinary-chat regressions;
- direct Reply-to-Scout engagement, reply ancestry, acknowledgement suppression, and ordinary Reply-to-person regressions;
- filename-only/oversized/stored-only dependency refusal, exact ACL-bound Files retrieval, source freshness at approval, visible worker status, and single completion delivery;
- desktop/mobile room rendering, obvious Scout control, colored cradle, reduced motion, and no cradle regression;
- long `#team` rich-feed scroll and edit/copy/title interactions;
- home Realtime, composer dictation, private Scout-thread creation, and typed Scout reply path;
- release-tree, migration, data-exclusion, and artifact-integrity checks.

Exit: exact candidate SHA is deterministic-verified and rendered QA has no release-blocking finding.

Evidence: the complete normal suite, the full 2,807-test race inventory in four shards, `go vet`, 381/381 mobile tests, TypeScript, Expo Doctor 20/20, the 33-case media harness, the 23-case desktop/brand harness, current-release iOS Simulator rendering, and authenticated Playwright desktop rendering passed. Rendered QA found and repaired two blockers before this status was granted: an adaptive iOS color had been stringified so idle cradle balls vanished, and a desktop bootstrap temporal-dead-zone aborted the authenticated app before cradle/voice startup. The rebuilt iOS candidate shows all five resting balls; authenticated desktop rendering shows all five resting balls plus measured moving orange energy with no page error.

### W2 — Bounded physical/live acceptance

Status: bounded pre-release acceptance complete for the available local/current surfaces; exact Build 32 physical acceptance remains in W4 after the matching VPS is live.

On physical iPhone plus a separate desktop participant, verify:

1. Both clients see Scout join and leave the same sitting.
2. Scout hears consented room audio and responds when transcript recording is off.
3. Pausing transcription stops persistence but does not silently disable an invited Scout.
4. `@Scout` in room chat yields one shared, attributed text answer without audio duplication.
5. Human audio/video/chat survives Scout/provider failure.
6. Home Realtime 2.1, composer dictation, private-thread creation, typed response, edit/copy/rename, and auto-title work.
7. `#team` rich-feed scrolling remains smooth through a long history.
8. Desktop and mobile Newton cradles render and animate correctly.

Use bounded live provider calls only. Broad corpora, marketplace, and endurance qualification stay in W5.

Exit: candidate is accepted for release. A fresh stable all-rooms-empty proof is still mandatory immediately before deployment.

### W3 — Exact Git and VPS release

Status: pending W2.

1. Review the final diff; preserve `stride-site/` and production data boundaries.
2. Commit and push the exact final candidate to `axx/main`.
3. Determine migration necessity from the final diff and live migration ledger; run only required migrations.
4. Back up every replaced VPS file.
5. Deploy the exact committed source artifact to `/opt/meetingassist`, excluding all `data/` paths.
6. Rebuild/restart the exact artifact after confirming no live meeting is occupied.
7. Verify from the public host:
   - exact release SHA and artifact identity;
   - `/healthz`, `/readyz`, and traffic readiness;
   - Realtime 2.1, `gpt-transcribe`, dictation, typed Scout, room Scout, and `@Scout` observations;
   - consent authority, migration state, data-volume integrity, and no production record loss;
   - Board-only historical state is reported separately and does not masquerade as a core Scout/STT failure.

Exit: the exact intended final build is demonstrably live on the VPS. Report this milestone only then.

### W4 — Corresponding iOS release

Status: pending W3.

1. Set the next unused build number (expected Build 32) on the exact deployed SHA.
2. Build the production iOS artifact through EAS.
3. Submit that exact artifact to App Store Connect for internal TestFlight.
4. Verify Apple reports `VALID`, the build is not expired, and it is available to `Team (Expo)`.
5. Install/update on the physical iPhone and repeat the bounded release acceptance against the live VPS.

Exit: exact corresponding build is Apple-valid, in the intended internal group, and physically accepted. Report this milestone only then.

### W5 — External E10 qualification and operations

Status: external queue; not hidden, not conflated with W0-W4.

- provider qualification over the bounded paid corpus;
- full physical WebRTC/TURN/device/network matrix;
- ten I&O pilots with two independent reviewers;
- five fully admitted specialist listings and employee-agent qualification;
- 24-hour and ten-sitting soak;
- encrypted immutable offsite backup and restore proof;
- HA/DR with measured RPO/RTO;
- external ledger-anchor custody and operational ownership;
- deliberate, reversible production cohort activation for E1-E9 domains.

These items block the claim that all of E10 is broadly production-qualified. They do not block shipping the stable, default-off implementation after W0-W4 pass.

## Current Wave checklist

- [x] Re-read the complete E0-E10 master plan.
- [x] Reconcile master-plan intent against current Git, public VPS, runtime capability, and EAS truth.
- [x] Confirm interrupted E10 work is present in current `main` lineage.
- [x] Remove the canonical Board repair ceremony from the release path without deleting Board data.
- [x] Reduce the closeout to the minimal W0-W4 path and separate external W5 work.
- [x] Complete W0 implementation and freeze the candidate scope.
- [x] AJ confirms this ledger is coherent and authorizes the held test/release sequence to resume.
- [x] Pass focused Go compile, mobile typecheck, Country Golf file admission/Files binding, durable launch-card, and existing origin-delivery regressions.
- [x] Execute W1 deterministic/race/mobile/media/rendered qualification.
- [x] Complete bounded pre-release acceptance on current Simulator, authenticated desktop, and the earlier physical incident loop.
- [ ] Complete exact Build 32 physical/live acceptance after the matching VPS release.
- [ ] Execute W3 exact VPS release and send verified milestone update.
- [ ] Execute W4 exact TestFlight release and send verified milestone update.

## Completed evidence retained

- Current `main` and `axx/main` match at `13b0797f18c495f3e8daa4c2df872ea1548f5926`.
- The public VPS reports that exact baseline SHA and passes health/readiness/traffic-ready checks.
- The E10 integration, specialist-isolation, signed-registry, provider-binding, qualification-expiry, external ledger/CAS, operator-pack, and release checkpoint commits are in current `main` ancestry.
- The frozen candidate passed the complete normal suite, the full 2,807-test race inventory in four shards, `go vet`, 381/381 mobile tests, TypeScript, Expo Doctor 20/20, the 33-case media harness, the 23-case desktop/brand harness, authenticated desktop rendering, and current-release iOS Simulator rendering.
- EAS produced Build 31 at the exact baseline SHA; it is a superseded checkpoint for this final release.

## Pending dependencies and gates

- A live occupied room is a temporary deploy gate, not a blocker; wait for it to drain.
- Apple processing and intended-group visibility are external states that must be polled to proof.
- Physical-device acceptance needs the connected iPhone and a second client, but no additional product decision.
- Broad paid-provider qualification and long-running external operations remain W5 and are not silently spent or activated during bounded release smokes.

## Authority and operations

- Existing authority covers the bounded implementation, tests, commit/push, required migration, exact VPS deploy, EAS build/submission, and internal TestFlight verification described in W0-W4.
- Do not request repeated permission for ordinary in-scope steps after that approval.
- Stop only for a genuinely new consequential product decision, unavailable external credential/state, unexpected production-data risk, or a failing release gate that cannot be safely repaired in scope.

## Risks and decisions

- **Board:** preserve history; no canonical repair; no destructive retirement in this release. A later Board retirement plan may archive UI/routes/data consumers and adjust health classification deliberately.
- **Capabilities:** aggregate degraded status is diagnostic. Final acceptance is capability-specific and must not relabel a real failure as healthy.
- **Activation:** default-off STRIDE domains stay default-off until qualified. Shipping code is not permission to activate them.
- **Scout separation:** transcription persistence, Realtime invited participation, and text `@Scout` are three independent lanes with shared consent/ACL rules and no duplicate output.
- **Build numbering:** Build 31 is already used. The post-fix final artifact is expected to be Build 32, subject to a fresh read before creation.
- **Release identity:** the current public SHA is real but will be superseded. Only the post-fix exact SHA and corresponding iOS artifact count as the requested final milestones.

## Resume here

Proceed through W3-W4 against the frozen Build 32 candidate without reopening historical Board repair or unrelated E0-E10 design work. Immediate next action: create and push the exact release SHA, prove every room empty, preserve the live volumes, activate and publicly verify the exact VPS artifact, then build/submit/verify the corresponding internal TestFlight artifact and complete exact-build physical acceptance.
