# STRIDE Business: the first useful run

Date: 2026-09-04. Design proposal for the next bounded lifecycle slice. Baseline inspected: `97cac9a2`, after the root rebase. This specifies product behavior; it does not claim an executing agent company or authorize a release. Root owns HTTP/integration; Dissent owns the worker; the product agent owns this document. Concurrent root read-model and Dissent worker changes are called out separately below.

## Decision

Make one business move from a saved mission to an inspectable private result, then use one source-linked observation to choose its next move. The first supported output is `private_document_v1`. This is a deliberately narrow operating foundation. A generated document alone is not evidence that STRIDE runs a business, that a customer benefited, or that an agent CEO exists.

The first screen should answer: **What is this business trying to achieve, what is happening now, and what happens next?** Preserve the ink navigation, cobalt operating state and editorial typography of the current shell. Replace the default wall of unavailable sections with one substantial “Next move” area and progressively disclosed operating evidence. Keep public profiles out of this slice. Exact private work evidence can later support a selectively published case study; it is not published by completing work.

## What exists, and where the user journey breaks

| Step            | Current evidence                                                                                                                                                                                                                                                                                                      | Consequence for the experience                                                                                                                         |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Set up          | `SetupBusiness` atomically stores organization, membership, Business and an internal model allowance. Current HTTP and rendered UI cover create, read and policy update.                                                                                                                                              | Useful saved draft. Choosing agent CEO does not hire an operator, activate the business or start work.                                                 |
| Hire            | `CreateEmployment` stores an exact offering ID/version/digest; `GrantMandate` stores limits and expiry. The current method validates strings, not a qualified offering registry. Employment has no role/reporting-line or designated CEO field.                                                                       | Do not expose arbitrary offering fields or call an employment a capable operator. A safe curated hire and explicit responsibility binding are missing. |
| Connect tools   | Current Work admission permits only `private_document_v1`. No Business tool/resource grant contract is present in this kernel.                                                                                                                                                                                        | Do not show OAuth integrations as connected or let a mission imply access to chats, calls, repositories or external accounts.                          |
| Run             | `AdmitWork` requires active Business, current employment/mandate/issuer, limits and allowance. HTTP actions are update policy, pause and resume; there is no draft-to-active first-run path.                                                                                                                          | “Full autonomy” is a saved ceiling, not readiness. A launch path must join readiness, exact source input, qualified route and admission.               |
| Execute/recover | Attempt leases, preparation, result persistence and cost reconciliation exist. Dissent reports a new injectable, provider-free `Worker.Step` with a fake adapter; no production provider, scheduler or autonomous team.                                                                                               | Do not offer an active Start/Retry button merely because a step driver exists. “Prepared” means may have issued, not provider accepted.                |
| Inspect result  | Store result is immutable private markdown with digest, Work/attempt/operation identity and current eligibility recheck. Baseline HTTP hides team/work and exposes no result route. Root has implemented an atomic overview and exact Work read during this review; integration tests and the matching reader follow. | Immediate next UI slice is real Work/result inspection, with current authorization and honest uncertainty.                                             |
| Continue        | Kernel `Outcome=succeeded/failed/not_accepted` describes execution. No source-bound business observation or initiative-selection domain is present here.                                                                                                                                                              | Never present a successful call as customer success, or a repeated prompt as learning. The next move needs its own evidence and authority.             |

Source anchors: [product contract](stride-agent-business-product-contract.md), [kernel contracts](../../internal/business/contracts.go), [admission and employment](../../internal/business/store.go), [attempt lifecycle](../../internal/business/attempt_store.go), [kernel boundary](../../internal/business/README.md), [HTTP projection](../../business_http.go), [current UI](../../public/business/business.js). These are local implementation facts; inherited legacy Work/memory/media remain separate until explicitly connected.

## Prioritized slices

### P0 — Show the real work before enabling more actions

Build on the root-owned overview and `GET /api/business/v1/businesses/{businessId}/work/{workId}` projection. A Work row opens a durable route in the existing shell: `/business?business={businessId}&view=work&work={workId}`. Refresh and sharing an authorized internal URL must restore the same Work, not search by title or redirect to a legacy goal.

Show the objective, responsible employment, current execution state, exact result, and cost qualification. No hire, start, retry, connect or publish action is enabled unless a server capability explicitly permits it. A populated test record is labeled synthetic in the fixture, never shipped as a sample company pretending to operate.

Required projection distinctions:

- **Work state:** admitted, reconciling, completed, failed or cancelled, plus factual attempt state. “Working” requires actual current execution evidence; a lease alone may only mean claimed.
- **Result state:** no result, saved private result, or saved result that is no longer eligible for further use. Viewing an authorized historical result and acting on it are separate permissions.
- **Cost state:** known, unknown, reserved or overdrawn. Unknown cost may hold further business work even when readable output exists. An internal model spending limit is not a payment or purchased credits.
- **Source state:** supplied input, exact version/digest and current availability, when the worker actually records it. Until then, do not invent a citations panel from the objective text.
- **Business outcome:** not observed. Keep this separate from technical completion.

The Work reader must render markdown safely, treat all result content as untrusted, and avoid automatic remote image loads or executable HTML. Fetch the exact selected result through the Business authority path. Clear content, headers, draft state and announcements on denial; abort/ignore stale responses after business or Work navigation. Never open a result solely because the client holds an old ID.

### P1 — One admitted private-document run, end to end

This is the first execution slice after the provider adapter is qualified. Use one curated private-document capability and one business-owned employment. Do not require three decorative hires, a marketplace, an external integration or a mandatory human reviewer to produce the first private result.

1. **Choose the first outcome.** Prefill from `firstOutcome`, but make the actual requested objective and output explicit. Example: “Turn these founder-supplied customer notes into a brief identifying the strongest onboarding problem, supporting evidence and one next experiment.” Without supplied evidence, say “Draft hypotheses from the business brief,” not “Research customers.”
2. **Add the capability.** Show the exact offering publisher/version, what it can do, data destination, supported private output, cost basis and current availability. The browser chooses an offering reference; the server resolves and validates the package. Bind an isolated employment to this business. Public offering knowledge is not shared private employment memory.
3. **Give it only the needed inputs.** First launch may use the exact saved business brief plus explicit owner-supplied source records. Store those records and their source audience/version before admission. If source-record support is not ready, narrow the operation to the saved brief and label the evidence limit. Do not pass source blobs hidden inside a freeform objective as if they had durable citation/correction semantics.
4. **Set the operating boundary.** Present the per-run maximum estimate/ceiling, remaining internal allowance, expiry, retries and concurrency in plain language. Defaults for this first qualification: one open Work, one attempt, one private document. No arbitrary dollar promise: the adapter must produce an authoritative bounded quote for the actual request. Extra retry permission is enabled only after its evidence-based nonacceptance path is proven.
5. **Start the private run.** The initial owner action is a concrete delegation, not an approval gate for every subsequent agent step. The server atomically rechecks the selected business revision, exact employment/mandate, sources, route and quote before activation/admission. Store the durable Work root and reservation before dispatch. A lost acknowledgement returns/reconciles the same Work rather than repeating the action.
6. **Read the result and its limits.** The result opens on the same Work. Show who produced it, when, what source set it used, what is known about cost and what remains unobserved. An optional person or agent review may critique the result; “private result saved” does not depend on routine human acceptance.

Activation is a new explicit domain command, not a cosmetic client flag or a reuse of “resume” on a draft. A prepared source/quote may become stale; launch returns a precise changed prerequisite and retains the user's draft. Business status can become active only when the qualified operation is admitted. Execution readiness remains a separate server projection: an active business can be idle, missing a route, paused for reconciliation or waiting on a source.

**Proposed command boundary, for root/kernel implementation:** a server preparation request returns an expiring operation proposal with `businessId`, business revision, exact offering/employment/mandate references, source snapshot, output contract, qualified adapter/route/price revision, maximum model cost and missing requirements. A launch request supplies that proposal ID/digest, expected revision and idempotency key. It must not supply actor identity, raw credentials, a client-authored “qualified” route or a trusted cost quote. Preparation does not create permission to execute. The exact endpoint names can follow root's HTTP conventions; these fields and rechecks are the contract.

Hiring and resource setup can persist independently of launch. The final activation/admission boundary must be atomic. If an owner stops midway, show “Capability added; no work started” rather than a partially running company. Do not automatically elevate a private-document specialist into an agent CEO: designate an operator only when its decision/initiation capabilities are actually implemented and granted.

### P2 — The observation changes the next move

A second private run from a human click is useful, but it does not prove agent initiative. Add a bounded observation-to-next-move loop after P1:

- Store an append-only observation bound to the exact Work/result digest, source reference/version, observation time, observer identity, audience and correction/supersession. Separate “a person judged this useful” from “a customer used the brief to make a decision.” A self-authored agent reflection is an inference, not an observed business outcome.
- The authorized operator reads only current source evidence. It records one candidate next action and its reason, expected usefulness, uncertainty, rough effort, duplicate check and destination. Normal outcomes are discard, defer, investigate or act. Discard does not fill an attention queue.
- Under `take_initiative` or `full_autonomy`, the operator may admit the next private run within its current resources and limit without a routine human click. Under `advise`, show a concrete suggestion. Under assigned-work mode, continue only work already covered by that delegation. The scheduler/worker must bind the real employed actor; the client cannot choose an agent principal.
- Link the new Work to the causal observation and previous result. Duplicate delivery or restart must select the same intent. Revoked/corrected evidence invalidates any unissued proposal; already issued work shows the changed source and follows existing cancellation/reconciliation behavior.
- Idle businesses perform no model calls merely to animate a dashboard. The next wake is a durable event or observation deadline, with a visible purpose and bounded budget. Do not add a free-running polling agent.

There are two separate acceptance gates: a controlled two-episode product test over explicit source records, then the product contract's disclosed real-call unsolicited-initiative proof. The controlled test does not replace the call proof, external customer outcome, device media reliability, or source-correction evidence.

## The interface: a business with one clear next move

Use the existing shell, not another dashboard or chat client. The memorable visual element is the work itself: a large, beautifully readable result or an exact next-outcome statement occupying the primary canvas.

**Before launch:** mission at the top; a single “Your first move” composition beneath it. Left: the specific outcome and a short source summary. Right: the real responsible capability, required access and spending limit as three compact readiness rows. One primary action leads to the first unresolved prerequisite. Show factual labels such as “Not hired,” “Only the business brief,” “Route unavailable,” or “Ready for a private run.” Do not use percent-complete onboarding or fabricate workers. Unavailable advanced views collapse into a quiet “Not connected yet” area rather than six repeated blank panels.

**During work:** replace the launch composition with the same objective and a short factual operating trail. A line is added only for a durable event: admitted, provider acceptance if known, result saved, cost confirmed. No fake progress percentage, simulated live typing, endlessly orbiting agents or token feed. A timestamp and “Last checked” distinguish quiet work from a stale browser. A visible pause control stops new permitted effects when the API supports it; it does not promise cancellation of already accepted work.

**After work:** the private document becomes the main canvas. A narrow evidence column holds source/version, employment, route qualification and spending state. Technical identifiers are under “Inspect record.” “What happened next?” is visually separate and initially says “No outcome observed.” Optional review is a secondary action; if operator continuation is authorized, the next move can appear and proceed without a human acceptance button.

**When uncertain:** preserve the readable result if authorized, but put a precise statement beside it: “The result is saved. Cost is still being checked; further work is held.” If provider outcome is uncertain, show “Checking whether the operation completed. It will not be repeated while its outcome is unknown.” Do not present a generic Retry button that would dispatch again. Any “Check status” action must perform the adapter's read-only reconciliation and have a bounded retry interval; otherwise it is unavailable with an honest explanation.

**Phone:** one vertical narrative—objective, current state/next action, result, evidence. The evidence column becomes a disclosure, not a tiny two-column table. Preserve the existing bottom navigation and keyboard-safe forms. **Tablet/desktop:** a generous result canvas with the operating evidence alongside it. Motion is limited to actual state changes and disabled for reduced-motion users. Result reading, selection and status refresh must remain fast with large output and bounded history.

This vocabulary can later become an exceptional public work portfolio: artifact first, collaborator context and observed outcome second, evidence qualification third. No feed, follower count, universal assessment score or public profile editor belongs in this first-run slice.

## Required state and recovery copy

| Actual condition                    | User-visible meaning                                                                | Allowed next action                                                            |
| ----------------------------------- | ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| Draft; no qualified adapter         | “Setup saved. Private runs are not available yet.”                                  | Edit setup; no active Start action.                                            |
| Missing capability, source or grant | Name the exact missing prerequisite and what this job needs.                        | Complete only that prerequisite; retain the objective.                         |
| Waiting for worker/current lease    | “Queued” or “Claimed,” with last checked time.                                      | Refresh; stop only through a real supported cancellation path.                 |
| Provider acceptance unknown         | “Checking whether the operation completed.”                                         | Read-only reconciliation; no redispatch.                                       |
| Result saved; cost unknown          | “Result saved. Further work is held while cost is checked.”                         | Read result if authorized; inspect evidence.                                   |
| Policy changed/ineligible result    | “Historical result. Current authority no longer permits using it for further work.” | Authorized viewing; no reuse/publish action inferred from eligibility.         |
| Budget overdrawn or exhausted       | Show known spend, held amount, limit and qualification separately.                  | Owner changes a concrete internal limit if allowed; no implied payment.        |
| Known failure/not accepted          | State which operation failed and whether any charge is known.                       | Only a server-qualified next attempt under the same Work.                      |
| Access revoked/session changed      | Remove all private result and cached identity text immediately.                     | Reauthenticate or return to authorized business list.                          |
| No business outcome                 | “No outcome observed.”                                                              | Attach a permitted observation or await its event; no invented success metric. |

## Acceptance and handoff

Ship P0 before enabling P1 actions. P1 is not ready until the real adapter and every exposed mutation are qualified. P2 depends on durable observation/intention semantics, not merely more frontend controls.

Required evidence for P0: current Business/Work scoping; readable exact markdown result; nullable cost; result eligibility changes; two tenants with the same offering remain isolated; stale navigation and revoked-session clearing; phone/tablet result reading; bounded list and result payloads. Exact Work detail must not need an unbounded business or attempt scan.

Required evidence for P1: actual authenticated setup → validated hire → explicit source/limit → durable admission → one real private result → reload/restart of the same Work; no invented operator or external tool; changed quote/source/policy cannot issue; acknowledgement loss creates no duplicate Work/cost; worker loss after a possibly issued operation reconciles; known nonacceptance is the only qualified retry route; unknown cost and overage block further admission. Test interrupted setup and creator takeover. Show the observed provider call separately from fake-adapter fixtures and paid production activity.

Required evidence for P2: observation one materially changes episode two; duplicate event, restart, correction and revocation preserve causal identity; full autonomy proceeds without a routine human click within its mandate; advise does not initiate; quiet periods incur no decorative model calls. A real customer observation remains distinguishable from synthetic QA and evaluator opinion.

No implementation edits, production changes or public publication are included in this review. Immediate owner actions: root verifies the exact read projection; product implements the Work reader against that payload; Dissent completes the provider-free driver and then qualifies a real adapter; root/kernel add the missing catalog/source/readiness/admission command before any active hire/start UI.

## P0 implementation evidence from this pass

The Work reader is now implemented against the root-owned real HTTP/PostgreSQL projection, with no active hire/run/retry/publish controls. The disposable preview used canonical session/person authentication and the restricted database role; its completed and unresolved-cost results were explicitly synthetic fixture content, with no provider execution or customer outcome.

Rendered Chrome checks: real Work list to exact result, result/source/digest inspection, reload, completed versus unresolved cost, 390px phone/834px tablet with no horizontal overflow, and reduced motion. A dark system preference retained the intentional light product theme; this is not a separate dark-theme implementation. A cross-business Work URL returned 404 and removed private result nodes, prior business text, document title and live announcement. Historical ineligible results are covered in the API contract tests; this pass did not change the shared preview's policy merely to alter that fixture.

Ten API tests pass, including exact business/employment/attempt/operation/result identity, private and complete response coverage, immutable-result metadata, malformed content limits, historical eligibility, unknown cost, same-origin transport and denial/retry behavior. Markdown presentation uses text DOM nodes for headings, lists, paragraphs and code; HTML, links and images are never interpreted. Exact source remains inspectable. The full app shell plus reader is about 26 KB compressed before reused fonts. Whole-product acceptance and an executing private-document adapter remain subsequent gates.
