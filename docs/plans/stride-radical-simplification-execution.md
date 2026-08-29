# STRIDE Radical Simplification - Execution Ledger

Goal and source pointer: active `$goal-loop` objective for thread `01a03482-f258-78a2-87e0-d7a93b96ee01`. This ledger records the local implementation and its remaining promotion gates. The governing product contract is four destinations, three agents, one permissioned organizational brain, one durable WorkRun, and a live-media-first meeting path.

Current phase: exact production release authorized on 2026-08-28; qualification-lineage bridge and final candidate preparation in progress. Push remains separately unauthorized and is not required by the exact-release tool.

## Product Contract

- Customer navigation is exactly Home, Conversations, Work, and Drive.
- Video meetings, public/private channel conversations, and Realtime voice are conversation modes, not separate products.
- Research and presentations are Work deliverables, with a common lifecycle designed to admit later artifact types.
- Scout is the lead agent. Researcher and Presenter are the only separately addressable specialists, and appear in activity only when backed by a durable assignment.
- Company files and completed work live in Drive. Search and retrieval respect source authority before ranking.
- One durable `WorkRun` owns assignments, sparse activity, approvals, retries, artifact lineage, recovery, and terminal delivery truth.
- `Needs you` is reserved for missing authority or consequential human judgment.

## Safety And Memory Invariants

- Live RTP forwarding outranks transcription, analysis, memory, parity, and indexing. Optional analysis sheds load before media.
- During an active, closing, finalizing, or retrying meeting, work is limited to the current meeting ID and exact transcript delta. Full-history parity and global-memory inventory are forbidden.
- Capture appends to a durable spool in O(delta). Raw transcripts are cold evidence, not the default query surface.
- After close, STRIDE publishes a revisioned meeting analysis/source episode, then coalesces parity when idle.
- Publication is atomic against transcript highwater/digest, ACL revision, consent fence, purge generation, and source authority. Failed persistence leaves no retrievable in-memory evidence.
- Authorization precedes ranking. Private evidence cannot promote itself to company memory.
- Learning candidates remain inactive until evaluated and human-ratified under a held, current source-authority lease. Revocation, consent withdrawal, correction, or purge immediately excludes the candidate.
- STRIDE owns source identity, ACL/consent/purge, retention, correction, provenance, and publication. External memory systems may only be replaceable projections downstream of authorized `SourceEpisode` publication.

## Wave Map

| Wave | Local outcome | Status | Promotion or rollback gate |
|---|---|---|---|
| 0 | Meeting-safe delta capture, idle post-close publication, canonical safety contract | Complete locally | Real-device media/load validation; additive compatibility remains |
| 1 | Four-destination shell and three-agent customer contract | Complete locally | Rendered desktop/mobile journeys passed; legacy aliases retained |
| 2 | Six-source `SourceEpisode` inventory for meetings, channels, private conversations, Realtime, Drive, and completed work | Complete locally | PostgreSQL migration and real-corpus replay require separate authority |
| 3 | Permissioned company/project/person projections and source-aware retrieval | First production-shaped slice complete | Semantic/ranking alternatives remain shadow-only pending held-out evaluation |
| 4 | One durable WorkRun with truthful assignments, activity, lineage, recovery, and terminal artifact reconciliation | Complete locally | Provider/device acceptance and real-corpus recovery remain promotion gates |
| 5 | Responses-backed Scout lead harness inside STRIDE authority/recovery envelope | Shadow harness complete, not promoted | Native render/open/editability receipts and blinded benchmark must pass |
| 6 | Evaluated, ratified, correctable, forgettable learning | Complete locally | Human policy/retention approval and real-corpus privacy replay required |
| 7 | Retire customer bloat and rigid stage theater | Customer surface complete locally | Stored legacy data and compatibility code are deliberately retained |

## Completed Evidence

- The meeting path uses maintained sitting indexes and exact transcript segments; a 5,000-unrelated-sitting regression observes only the selected sitting.
- Canonical startup primes the rolling meeting-memory digest before WebRTC admission, so the first append does not cold-hash lifetime history.
- Meeting source episodes have normal and race coverage for six source kinds, exact-segment raw access, supersession, ACL/consent/purge drift, PostgreSQL authority leases, atomic save failure, and absence of ghost evidence.
- A new PostgreSQL schema entry point exists at `migrations/0025_source_episode_ledger.sql`; it has not been applied to production.
- WorkRun recovery reconciles terminal artifacts and exposes only durable activity. Missing provider-local history is shown as repairing, never fabricated.
- Governed learning re-resolves exact source references and evaluates/ratifies under the same held authority boundary. Concurrent revocation serializes against ratification, then removes the learned context immediately.
- The default-off Responses lead harness persists provider state and retry receipts, obeys spend/tool/authority fences, runs only at media-safe idle, and refuses to claim native artifact success without native validation receipts.
- A 36-case blinded three-lane benchmark fixture covers research, presentations, and visual work. Promotion thresholds remain: 90% open/render/editable, 85% reviewable without added user input, false `Needs you` below 5%, zero ACL/lineage violations, no retry duplicates, and a clear contextual/handoff advantage.
- The customer shell enforces four destinations and three agents. Marketplace, network, hiring/team theater, standalone Design/Grill/Tool Palette, and Work Search have been removed or retired; compatibility routes converge on the retained destinations.
- Rendered isolated QA passed at 1280x800 and 390x844 after the final measured UX patch: four destinations, canonical aliases, no forbidden customer labels, corrected light-theme semantic tokens, full-width Drive search, no console errors, and no mobile horizontal overflow.
- The measured UX audit fixes from `25e45068fe66c464afdfb8fe789038b512cf7f35` were manually integrated without importing unrelated branch state: message-local hover actions, readable light-theme semantic tokens, active-width search, wider Drive cards, unclipped studio rows, and usable presenter notes. Audit and handoff documents from `03ba2fd1` are preserved in this tree.
- The clean baseline at `56afdc116f8dd4069df0d280a768287a7fe38077` was already not fully green: `go test ./...` timed out after ten minutes in `TestStudioDeepLinksOpenAuthenticatedSurfacesAndReturnToFiles` after existing Private Riff and project-bound research failures. Focused evidence is compared against that inherited baseline rather than presented as a full-suite pass.

## Brain Build/Buy Decision

- Keep going with STRIDE's canonical memory authority layer. Do not place Mem0, Zep, Memco, or provider memory in the live meeting path or make one the sole copy of organizational memory.
- The replaceable seam is downstream of authorized `SourceEpisode` publication: candidate extraction, embeddings, graph traversal, and ranking may be evaluated behind existing inventory/body interfaces.
- Adopt a vendor only if a shadow evaluation beats the native projection on held-out retrieval quality or operating cost with no regression in permission filtering, provenance, correction/forget, restart/replay, latency, or purge behavior. Adding one before that would add a second truth system without proving the core promise.

## Harness Decision

- STRIDE owns context/authority, approval and spend boundaries, durable execution and retries, artifact revisions, citations, native validation, external actions, and delivery.
- Scout owns the request and final delivery. Researcher and Presenter receive only real, visible assignments.
- Models own story, synthesis, copy, visual judgment, ordinary revision, and tool choice within those boundaries. The legacy 19-stage presentation and 12-stage report graphs are frozen compatibility paths, not the future cognitive architecture.
- The current native shadow adapter intentionally returns validation pending when it cannot prove render/open/editability. Legacy delivery remains available until the benchmark and artifact validators pass.

## UX Audit And Operations Boundaries

- Production remains at `56afdc11`; the UX fixes on `axx/main` at `25e45068` are not live from this task.
- Do not hand-edit `PRIVATE_REALTIME_VOICE_QUALIFIED`. The deploy qualification lineage must be repaired or handled through a receipted de-qualify/activate/re-qualify operation under separate authorization.
- In-call video tiles, the control island, and meeting tabs remain unaudited on a real camera/microphone device. Browser permission-error coverage does not close this gap.
- Backup `.tmp-tar-*` leakage and pruning of images, build cache, and `source.tar` are separate retention/operations work. The ember rail icon contrast is a product-taste decision, not an accessibility blocker.

## Production Release Wave

| Checkpoint | Required outcome | Status |
|---|---|---|
| Baseline | Generation 225, active `56afdc11`, previous `28b92428`, exact images and retained directories present | Verified |
| Qualification bridge | Same active SHA is transactionally changed from canonical `true` to canonical `false`, with exact-byte backup, crash-recovery journal, receipt, idle-room proof, and unchanged ledger | Ready; not yet executed |
| Unqualified bootstrap | Already-built `25e45068` becomes active through the retained `56afdc11` tool and verifies unqualified | Pending bridge |
| Final successor | Clean reviewed STRIDE commit is built and activated from `25e45068` with the retained false-to-true qualification transaction | Pending candidate commit/build |
| Acceptance | Ledger, images, `/healthz`, `/readyz`, retained verifier, authenticated product shell, desktop/mobile rendering, and bounded observation agree | Pending activation |

The bridge is implemented as `scripts/private-realtime-dequalification-bridge.mjs`, is included in the sealed release configuration inventory, and may run only from the built exact candidate. It never activates candidate code. A failed run restores the exact qualified base env and same active image; an interruption retains the global lock for explicit recovery.

## Verification Record

- Focused critical normal tests: passed in 18.951s.
- Focused critical race tests: passed in 35.201s, including source authority, publication, WorkRun recovery, learning drift/concurrency, and lead-harness recovery/fail-closed behavior.
- Repository compile-only gate, `go test ./... -run '^$'`: passed for all packages.
- Changed frontend contract suite, including the measured UX audit assertion: passed in 14.943s.
- Final combined source-authority, publication, WorkRun, learning, and lead-harness replay: passed in 12.364s.
- Final post-UX rendered pass: desktop Work and Drive plus mobile Drive and Conversations passed; `/video` selected Conversations, the console had no warnings/errors, and both viewports had zero horizontal overflow.
- `git diff --check`: passed. No deploy workflow, release-ledger, Docker activation, or realtime qualification changes are present in the diff.

## Authority Queue

| Operation | State |
|---|---|
| Commit or stage | Authorized as a required exact-release input |
| Push | Not separately authorized and not required |
| Production deploy/restart | Authorized for this exact release wave |
| Transactional qualification configuration | Authorized only through the receipted bridge and release tool |
| PostgreSQL migration | Authorized only when applied by the exact candidate startup and verified; no manual SQL |
| Legacy data/code deletion | Not authorized; prohibited before promotion gates |

## Resume Here

Prepare one clean reviewed commit, build its exact archive, execute the receipted dequalification bridge while all rooms remain idle, activate and verify `25e45068` unqualified, then activate the final successor through the existing false-to-true qualification transaction. Preserve the bridge and qualification receipts plus both rollback releases and images. Stop on any identity, lock, env, ledger, media-idle, migration, health, readiness, or rendered-product mismatch.
