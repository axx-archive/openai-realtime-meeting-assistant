# STRIDE autonomous business contract

Status: decision-complete design for the next wave; no runtime activation or new spending authorization. Written 2026-09-04 against the current shared working tree. This document extends `stride-3-execution.md` following the founder's explicit direction that agents should collaborate to operate businesses, with human involvement optional under delegated policy. Existing release evidence and implementation status remain in that execution ledger.

## Decision

Build **a business that can keep operating**, with a mission, resources, customers, accountable agents and observed results. The founder sets a mandate once; a supervisor decides what to do next, delegates bounded work, examines results and changes course. People can participate as owners, colleagues, customers or reviewers. A human confirmation is not a universal runtime requirement.

Business is a durable policy and resource aggregate anchored to an existing organization and Project. It does not introduce another task scheduler, conversation system, artifact store or generic workflow language. Every concrete episode executes through an existing goal root, child agent threads and the Work projection. Business is shown as the operating view of its Project; Work remains the unit of an inspectable outcome. A business may eventually own several Projects, but v1 has exactly one.

Autonomy means the system can choose and complete successive work within a standing mandate, including an explicitly permitted external action, without a person driving each transition. It does not mean unlimited credentials, self-granted budgets, fabricated customer success or the removal of ownership. Policy determines when human judgment is required; a model cannot decide that its own permissions have expanded.

## What exists and what must change

| Existing authority | Reuse | Verified limitation |
|---|---|---|
| `goal_engine.go`: persisted `goalPlan`, subtasks, process identity, cancellation, checkpoints, commit generation | One execution lifecycle per episode | Existing checkpoints and external-write paths often require human/admin approval; do not relabel them automatic |
| `process_definitions.go`: authored stages, writer/panel/gate/render, bounded process budgets | A small authored business process; supervisor chooses among admitted processes | Token/wall-clock stage bounds are not a durable pooled money reservation |
| `studio_projects.go`, `work_result_ledger.go` | Work status and exact artifact version/digest; no second Work state | Studio's “project” DTO is a goal projection, distinct from the canonical company Project |
| `project_work_binding.go`, `home_project_context.go` | Company Project continuity and exact-source authority | Current Project-linked launches rely on an admitted human conversation; autonomous origin needs a separate explicit binding |
| `coworker_delegation.go`, `stride_workforce_runtime.go` | Named seats, capability/route references, accountable assignment | Live delegation requires an active member requester and one hop. Workforce types alone do not prove service-principal execution |
| `dissent_internal_document_policy.go`, `dissent_work_receipt.go`, `document_report_quality.go` | Existing provider adapter, frozen route and execution/result receipts | Experimental admission is not measured qualification; rendered same-provider critique is not independent dissent |
| `usage_ledger.go`, `studio_work_usage.go`, `stride_routing_economics.go` | Observed calls, costs, source Work IDs, price and route vocabulary | Usage append is best effort; E8 budget circuit is in memory and per attempt. Neither can enforce a business account across restart |
| `studio_work_feedback*.go`, source-bound memory | Exact human review, reported outcome and revoked/corrected feedback handling | A human review event must remain human. Agent evaluations and measured outcomes need distinct event types |
| `stride_organization_authority.go`, `auth_http.go`, `organization_execution_scope.go` | Central W4 identity and current-member checks; singleton-engine compatibility fence | Shadow conversion does not isolate another organization's legacy data. JSON stores have process-local locks, not multiprocess authority |

These limits are implementation dependencies, not reasons to build a parallel agent engine. Legacy Bonfire work remains unchanged until an explicitly bound business launch uses the new contract.

## Records and ownership

All new records use `schemaVersion:1`, stable IDs, integer revisions and canonical digests. IDs supplied in prompts are references only. Monetary amounts are integer USD micros with a frozen price revision; other currencies require a later explicit contract.

| Record | Required fields and meaning |
|---|---|
| `Business` | `id, organizationId, projectId, revision, mission, customerDefinition, outcomeDefinition, ownerPrincipalId, supervisorSeatId, policyRef, budgetAccountId, status, createdAt`. Status: `draft`, `active`, `paused`, `closed`. Business is not a legal entity |
| `BusinessPolicy` | `id, revision, digest, grantorPrincipalId, authorizedBy, validFrom, expiresAt, allowedProcessRefs[], allowedSourceScopes[], allowedDestinations[], decisionMandates[], limits, reviewRules, outcomeRules`. Exact process references include version/digest; no wildcard external actions in v1 |
| `DecisionMandate` | `decisionClass, actorSeatIds[], permittedActions[], resourceScopes[], maxCostMicros, maxAttempts, maxConcurrent, maxDelegationDepth, reviewRule`. Supervisor may prioritize, select an admitted process, delegate and stop; it cannot change owner, policy or its own grant |
| `BusinessPrincipalBinding` | `principalId, principalKind:"agent", seatId, businessId, organizationId, policyRef, grantorPrincipalId, grantId, grantRevision, expiresAt, sourceScopeDigest, destinationScopeDigest`. Server-minted; kept distinct from a human's account/session |
| `BusinessEpisodeBinding` | `businessId, businessRevision, policyRef, episodeId, workRootId, attemptId, decisionId, principalBindingRef, reservationId, sourceManifestDigest, outputContract`. Stored on the existing goal root and frozen provider request |
| `BusinessDecision` | `id, businessId, policyRef, actorPrincipalId, class, evidenceRefs[], alternatives[], choice, rationale, proposedActions[], status, occurredAt`. Status: `proposed`, `admitted`, `rejected`, `superseded`. Model text proposes; deterministic host admission decides |
| `BusinessResource` | `id, businessId, kind, externalHandleRef, grantRef, observedState, observedAt, authorityRevision, status`. Credentials stay in host vault/proxy; models see scoped handles. Includes owned repository/environment, connector, data collection and provider account allocations |
| `BusinessBudgetEvent` | `id, accountId, episodeId, attemptId, operationId, type, amountMicros, priceRevision, providerReceiptRef?, occurredAt`. Types: `funded`, `reserved`, `settled`, `released`, `reconciled`; an unknown billable result retains its reservation |
| `BusinessOutcomeEvent` | `id, businessId, episodeId, result:{artifactId,version,digest}, metric, baselineRef?, observationWindow, value, unit, sourceRefs[], reportedByPrincipalId, evidenceClass, confidenceLimit, supersedesId?, occurredAt`. Evidence class: `measured`, `customer_reported`, `agent_assessment`. No automatic causal claim |

The owning engine appends Business commands and budget events to one durable, single-writer journal under its own data directory. A command carries `expectedBusinessRevision`, `idempotencyKey`, actor/grant revision and request digest. Replay produces Business state; a mismatched retry is a conflict. New policy/resource authority is necessary control state, not another Work execution store.

Human feedback remains in its current append-only `work_review` lane. Automated evaluation records never set `ActorID` to AJ, `reviewState:"accepted"`, or publication approval. Business may continue under its own machine-review policy while the human-review field remains unreviewed. Sources retain their original audience; Business membership never promotes private notes.

### Atomic admission and budgets

1. Revalidate organization, Business status, policy/grant, exact source revisions, destination and process identity. Effective authority is their intersection. An agent's role name or high model score grants nothing.
2. Under the Business journal lock, admit the decision and reserve worst-case billable exposure for the operation before dispatch. Remaining funds are funded minus settled minus outstanding reservations. Include retries, reviewer calls and tool fees. Unknown price or missing funding prevents autonomous dispatch.
3. Append a durable launch intent with deterministic `workRootId` and `operationId`; then use the existing goal launch/claim seam. A restart reconciles the intent against that same root. Never mint a fresh root merely because acknowledgement was lost.
4. Before every provider/tool call, recheck the grant and claim the exact operation. An ambiguous provider acceptance is `reconciliation_required`; reserve remains held and the same effect is not blindly replayed. Existing provider idempotency/receipt reconciliation must establish whether it ran.
5. Settle actual observed cost durably, then release only the unused confirmed remainder. Keep ordinary usage telemetry as a secondary report. If settlement persistence fails, pause new calls; do not silently credit the budget.

Funding changes are owner-authorized journal commands. Supervisor budget reallocation may divide an existing allowance but cannot mint funds. First implementation has one business currency, one active episode, a capped provider-call count and no autonomous purchases. A low token count is efficiency evidence, not contribution value.

## Supervisor, specialists and routing

Use three functional seats initially: supervisor, implementer/researcher, reviewer. These are role/capability assignments on existing coworkers, not new personality services. The supervisor owns the business queue and decision rationale; specialists own an exact output contract and cannot expand the objective. The reviewer challenges the result and source fit; its report is a referenced artifact. Technical independence is recorded separately from its role name.

Keep one-hop supervisor-to-specialist delegation in v1. Specialists return new needs to the supervisor. A supervisor may launch the next episode only after the previous episode has a terminal result or a recorded stop and its required observation condition has been resolved. Backoff and a `nextWakeAt` persisted with the Business prevent busy loops. The existing `workflow_ticker.go` wakes eligible businesses and submits work to the existing engine; it does not execute an independent agent loop.

Routing input is `{taskClass, outputContract, contextRequirements, toolRequirements, riskClass, deadline, maxCostMicros, reviewRequirement, allowedProviderPolicies}`. STRIDE resolves these against its server-owned, currently available adapter registry and route/price revisions. Model and reasoning-effort names are route outputs, not persisted business identity. Freeze requested route, actual wire identity, fallback, usage and exact result binding. An unavailable route produces an explicit hold; another admitted route can be selected only through a new recorded decision/reservation. Do not invent future models or activate credentials from a prompt.

Two qualification labels remain separate: `experimental_internal` for a supported but unevaluated route, and measured qualification backed by current evaluation receipts. A review record states provider/family independence, scope, exact result and availability. More agents using one family may offer useful criticism; they do not become cross-family DISSENT.

Review policy is specific to the action. Private draft generation can proceed with same-provider review if policy permits. Consequential action requires the reviewer/quorum configured in the exact mandate. If independent review is mandatory and unavailable, the action waits; the draft remains usable. A human can amend the mandate or act through an existing explicit approval path. The supervisor cannot silently waive the rule.

## One real STRIDE Builders proof

Mission: **make it easier for an invited STRIDE user to turn a conversation into their first useful Work result, then improve the experience from observed use**. Customer: AJ as an explicitly identified first internal user, followed by an invited external pilot. Internal use is not outside customer demand or revenue.

The complete proof is a two-episode business cycle:

1. Builders reads only its authorized product brief, repository snapshot and recorded onboarding observations. The supervisor identifies one measurable friction, chooses an admitted improvement and records why another candidate lost.
2. Specialists produce a bounded change and test evidence; a reviewer challenges the exact patch and user flow. The supervisor either revises, stops or authorizes a permitted delivery using policy. All work appears in STRIDE, with its actual executor and budget.
3. A release adapter deploys that exact checked patch through the existing receipted release/rollback path once its standing release mandate has passed its own gate. Until that adapter exists, an owner performs the release and the run is labeled **supervised delivery**, not autonomous operation.
4. AJ performs the actual onboarding task. Record the result version/release identity, time to useful result, success/failure, assistance required and any usability observation. Missing observation remains unknown; a generated artifact or “looks good” gate is not successful onboarding.
5. With no new step-by-step instruction, the supervisor uses that current outcome to choose a second bounded improvement, or records why no further work is warranted. Show source citations and a counterfactual run with the feedback withheld. Correct or revoke the first observation and demonstrate that future decisions change or lose the evidence.

Initial proof envelope is a proposed policy, activated only through the owner command: seven-day expiry, USD 5 maximum model exposure, one concurrent episode, three functional seats, at most two repair rounds, twenty total billable calls, no outbound marketing/customer messages, no purchases, no production-data edits. Resource scope is the Builders Project and explicit STRIDE repository/QA environment. Public release is a separate exact resource/action mandate, not implied by write access to a working tree.

The acceptance record includes every autonomous decision, intervention, restart, failed attempt and observed spend. Report the fraction of episodes completed without intervention alongside outcome quality and elapsed time; never optimize for fewer interventions at the expense of failed customers. A single successful cycle is feasibility proof, not proof of a self-sustaining company.

## Next implementable slice and dependency gates

**Next coding slice after the current release: a Business-funded private improvement brief that autonomously chooses its next episode after an observation.** It uses an authored private-document process and existing Work, not a new external execution adapter. This exercises mission → decision → reserved execution → review → result → observation → next decision, with no required human confirmation between admitted internal steps. Observation can arrive from a measured event or a person; the supervisor must wait when its contract requires evidence that has not arrived.

The first source patch can be implemented and fake-provider tested before infrastructure is ready. Its live Builders activation depends on G1 below; no shared Bonfire destination is an acceptable substitute.

| Gate | Deliverable, files and ownership | Acceptance / stop condition |
|---|---|---|
| G0 current release | Lead finishes exact-source feedback CAS, sparse recovery/guard integration, reviewed SHA and retained verification | Do not mix the new autonomous patch into a release still repairing evidence integrity |
| G1 real Builders isolation | Platform owner: extend `stride_e10_tenant_main_runtime.go`, `stride_e10_product_live.go`, `stride_e10_w4_network_activation.go`, `auth_http.go`, and exact-release infrastructure under `deploy/digitalocean/`; new purpose-bound authority client/server file | One authoritative W4/session writer. Optional same-SHA Builders engine service with its own volume and both tenant IDs set to the real organization ID. Central authority issues/revalidates audience/operation/expiry-bound grants. No shared or cloned writable JSON identity stores. AJ can enter Builders and cannot read Bonfire through relabeling. Verifier and rollback cover both services |
| G2 Business admission | Backend owner, new `business_contract.go`, `business_journal.go`, `business_authority.go`, `business_http.go`; minimal `kanban.go` initialization and registered route wiring in `main.go` | CAS/idempotent create/policy/fund/pause; current organization membership and grant revocation; restart replay; no runtime dispatch if journal unavailable. Ship dispatch-off binding/version recognition first and retain that image as the compatible rollback baseline before G3 activation |
| G3 existing-engine episode | Same backend owner: new `business_episode.go`, `business_process.go`; hooks in `goal_engine.go`, `agent_thread_runner.go`, `workflow_ticker.go`, `dissent_internal_document_policy.go`; additive `studio_projects.go` DTO | Durable launch intent → one existing Work root. `BusinessEpisodeBinding` and budget claim rechecked before dispatch and terminal write. Implementer/reviewer calls use supported adapters; zero paid calls in test suite. Existing unbound goals unaffected |
| G4 private loop proof | Product owner: new `business_outcome.go`; extend Project detail in `index.html` and Expo Work/Project screens only after server DTO stabilizes | Mission, active Work, budget, decisions, result and observation visible on phone/tablet/web; second episode changes from current outcome without a new launch instruction. Corrections, pause, exhausted budget, restart and no-route paths proven |
| G5 external delivery mandate | Execution owner: extend `codex_runner_queue.go`, existing external outbox/callback checks and retained release tooling; new `business_action_admission.go` | Scope exact repository/ref/environment/action, patch/review/release digests, max exposure and expiry. Existing global external-write flag or human “accepted” feedback cannot substitute for mandate. Exact checked SHA deploy + rollback + post-release observation; unknown external state stops replay |
| G6 customer business evidence | Founder/product owner activates an invited pilot and explicit customer-facing policy | Repeated useful outcomes, operating cost and support/intervention history; no outbound activation by this design document |

Proposed authenticated endpoints: `POST /api/businesses/v1` creates a Business against an existing Project; `GET /api/businesses/v1/{id}` projects authorized state; `POST .../{id}/commands` accepts `{expectedRevision,idempotencyKey,type,payload}` for policy, funding, pause/resume and episode request; `POST .../{id}/outcomes` records a source-bound observation. Server mints actor and organization facts. New endpoints require an explicit Business authority dispatch path in the organization guard; they must not acquire a broad prefix exemption to the legacy engine.

Provider/source authorization cannot be implemented by inventing an AJ session. The delegated grant identifies the agent actor and grantor. Until source/tool readers support that actor directly, an adapter may use the grantor's current ACL solely as an additional upper bound and must record `onBehalfOf`; it must also enforce the narrower business grant at every read/write. No ambient widening or unrestricted service user. If the existing seam cannot preserve that intersection, it is unavailable for the first process.

A private process uses its own explicit machine-review policy and has no `human_checkpoint` stage. Existing human checkpoints remain unchanged. Do not make a general “auto-approve all” switch. A Business result can be `machine_reviewed`, `delivered`, `outcome_pending`, `observed` or `stopped`; these project episode evidence and never rewrite goal execution or human review truth.

## Failure, rollback and validation contract

- Revoked owner/agent grant, private source, consent or destination: stop dispatch; suppress inaccessible result/evidence projections; retain body-free audit and outstanding cost reconciliation. Viewer access is checked independently of the business's execution grant.
- Changed policy/process/source/result: exact references invalidate admission. Replan with a new decision; no replay under stale authority. One held episode does not silently consume or skip other recorded work.
- Supervisor loops or fails: bounded decisions/attempts/wakeups; retry does not refill budget. After the bound, pause with a concrete reason. Stop/pause is deterministic host control, not a prompt request.
- Crash between admission and root creation, provider acceptance and result, or settlement and feedback: deterministic reconciliation proves at most one admitted effect. Tests must inject each boundary and distinguish absent from ambiguous completion.
- Customer report conflicts with telemetry: preserve both source-linked observations, label disagreement and require the authored outcome rule to resolve or hold. Model assessments cannot overwrite measured or human evidence.
- Agent-generated instruction in a meeting, website or artifact is untrusted evidence, never a mandate, resource grant or policy revision.
- Release rollback disables Business dispatch first, preserves journals and goals, retains unknown reservations, and returns to the prior exact bundle. A dispatch-off compatibility release must recognize and reject business-tagged launches before any such roots are created; retain it as the minimum rollback image. Older pre-compatibility binaries are not a valid rollback target once Business roots exist because unknown process IDs may degrade into legacy work. Test compatible-image restart and dispatch rejection before G3 activation; keep existing unbound goals functional.

Acceptance requires real API/engine integration tests, not DTO round trips: concurrent reservations cannot overspend; replay cannot double-launch; no-provider/unknown-price stays idle; outcome changes next actual frozen request; stale outcome during generation prevents terminal evidence claims; cross-organization and private-source fixtures fail closed; human review remains untouched; rollback cannot resume autonomous business roots through an old unrestricted path. Rendered verification covers the full loop at phone, tablet and desktop sizes. Live proof records exact release, provider request/result receipts, budget reconciliation and observed use.

## Market and investment position

Proposed buyer entry: small teams and founders who want an ongoing operation run, not another place to configure isolated agents. Sell a private operated Project with a mission, a pooled execution allowance and inspectable customer outcomes. Meetings and rich conversations are valuable inputs and collaboration surfaces within that operation. Avoid making customers replace every tool before the first successful episode.

“Agents, business context and learning” alone is already overlapping incumbent territory: OpenAI Frontier publicly describes enterprise context, agent execution, evaluation and governed actions. STRIDE must demonstrate a more specific usable product and evidence advantage; generic orchestration or a provider router is insufficient differentiation. [OpenAI Frontier, inspected 2026-09-04](https://openai.com/business/frontier/)

Keep runtime interfaces replaceable. Anthropic describes separate durable sessions, harnesses and execution environments; this supports retaining STRIDE-owned business identity/outcomes while adopting better executors when useful. It does not establish that the managed service is already integrated here. [Anthropic, Scaling Managed Agents](https://www.anthropic.com/engineering/managed-agents)

The investment/acquihire thesis is a hypothesis: a small team that can demonstrate reliable, permission-respecting businesses choosing and completing successive work, with source-linked customer outcomes, reusable mandates and evidence that outcome memory improves later decisions. The asset is the product and operational evidence, plus a team that can build it—not a claim of AGI, an invented valuation, or privileged access to foundation-model buyers. Private customer data is not an acquisition asset to disclose without its own rights and consent.

Commercial default: paid private business workspaces plus transparent metered execution. Do not use token totals as compensation, resumes or contribution rankings. Test willingness to pay and repeat use before publishing prices. DISSENT remains an internal differentiator; standalone routing sales, public talent networks, an app store and white-label licensing are options after the operating loop has customers. Maintain exportable mission, Work, outcomes and receipts so customer continuity does not depend on one model vendor or on inability to leave.

Decision-complete next action: implement G2–G3 with fake providers and the exact contracts above after G0 is stable; build G1 as its independent deployment/authority prerequisite; activate one private Builders cycle only after both gates pass. G5 is the explicit threshold between useful autonomous internal work and autonomous business delivery. No remaining design question blocks the first implementation patch; live identity/resource grants, funding and measured customer observations are execution inputs, not facts this plan invents.
