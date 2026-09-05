# First live Business document adapter

Status: decision-complete implementation proposal, 2026-09-04. No provider call or production activation is represented by this document. The current SQL Business authority and fake-adapter Worker tests are foundations; they do not establish a working autonomous business. This slice produces one useful private document, survives an observed worker restart, records the real resource cost, and captures the next observation under the same Work lineage.

## Decision and useful result

Implement a small server-owned OpenAI Responses background adapter for `private_document_v1`. Use the documented `gpt-5.6-terra` route with `high` reasoning for a bounded writer. Keep Astra available as a separate future operator or consequential judgment route; this operation does not require a new supervisor runtime. This choice follows the existing bounded-writer intent, rather than selecting solely on price. The route starts as `experimental`, with `qualification: not_evaluated`. A successful call establishes one observed route execution, not measured general qualification or Full Dissent.

The first STRIDE Builders assignment is a private, actionable first-customer experiment brief: a specific customer, their problem, a proposed offer, evidence that would falsify it, and a concrete next experiment. Its inputs are the owner-authored mission/customer/first-outcome fields and explicitly selected, version-bound private sources. It cannot send messages, browse, purchase, deploy, or publish. Its result must distinguish supplied facts, assumptions, and proposed actions. No invented interviews, market validation, revenue, or customer outcomes.

The existing model documentation supports Terra, Responses, and high reasoning. Actual credential access, wire compatibility, latency, and output quality remain first-call observations. A documented model is not proof that this account can call it. [Terra model documentation](https://developers.openai.com/api/docs/models/gpt-5.6-terra)

## Authority includes the provider resource

`SetupBusiness` currently records an owner-authorized internal allowance. It is not evidence that the owner funded STRIDE's provider account. A logged-in creator must never manufacture platform credits by setting JSON allowance fields.

Add a narrow host-issued provider-resource grant, within the same PostgreSQL transaction domain as Work. Its immutable identity binds organization, Business, credential reference, allowed adapter/route revisions, retention choice, expiry, maximum generations, and USD-micro allowance. The host operator issues or raises it through an administrative path unavailable to ordinary HTTP creators or employed agents. The Business owner can impose a lower ceiling or revoke their business's use; they cannot raise the host ceiling. The effective allowance is the intersection of host resource grant, Business allowance, employment mandate, and the exact operation reservation. No actual payment or provider balance is implied by either allowance.

For local QA, the root operator can issue an explicit ephemeral grant for at most two generations and $1 total, further bounded to $0.10 per generation. A restart and GET polling do not count as another generation. The second generation is not automatically required for this proof. Production Builders requires a durable host grant before dispatch, even if the owner has already entered a larger Business budget.

Reserve the host grant and Business allowance atomically for admitted Work; settle both against the same immutable operation cost receipt. Outstanding unknown provider liability blocks further use of that grant, including through another Business if grants share its credited pool. Never create independent credit pools against the same promised host credit without a host-authorized allocation. Record actual overage rather than clamping it to the reservation, then block further admission. No database lock crosses network I/O.

`Scope` now reaches Plan, Execute, and Reconcile from the actual `Worker.Step` caller (commit `7998a262`). Use it directly. `Work` has no organization ID, and prompt text, response metadata, caller request bodies, or globally unique Work IDs are not substitutes for current SQL tenant authority. The adapter must verify the full scope/Work/attempt/operation/grant binding on every journal action.

## Request contract

Freeze these server-owned fields in a private request record before generation:

| Field | First route |
| --- | --- |
| Adapter / route | `openai_private_document_v1` / immutable version |
| Model / reasoning | `gpt-5.6-terra` / `high` |
| Background / stream | `true` / `false` |
| Retention | explicit `store:false` for bounded first proof |
| Tier | requested `default`; actual returned tier retained and priced |
| Output limit | 4,096 tokens, including reasoning consumption |
| Tools | empty tools, tool choice none, no parallel tools |
| Input handling | truncation disabled; exact frozen instructions and input |
| Cache | `prompt_cache_options:{"mode":"explicit"}`, no breakpoints |
| Output | private Markdown, no hidden publication action |
| Metadata | opaque operation correlation only; no customer/private organization names |

The endpoint is fixed to the official HTTPS Responses host. Disable redirects and automatic POST retries. Bound response bodies and transport deadlines. Never accept a model, provider URL, credential, tool set, or tier from model output. A client request ID is useful correlation; an idempotency header is not assumed to guarantee create deduplication.

The official prompt-caching contract says explicit mode without breakpoints performs no caching or cache writes. Still capture and validate actual cache counters. Unexpected counters are priced truthfully and mark the route observation as a mismatch; they do not disappear from accounting. [Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)

Plan remains local and effect-free. It reads a frozen prepared request and returns exactly those bytes plus operation route/price references. Do not put a token-count HTTP request inside Plan.

For a hard cost bound, prepare the exact input through the documented `/v1/responses/input_tokens` endpoint before admission and accept at most 8,192 input tokens. The count receipt binds model, instructions, input, and all token-affecting fields; generation must use those same fields. This includes message framing that character estimates miss. The preparation endpoint is a distinct bounded provider egress action, not a model generation. Do not assert it is free without a documented account contract. [Counting tokens](https://developers.openai.com/api/docs/guides/token-counting)

The minimal preparation lifecycle is a host-grant-authorized, privately stored preparation record with a $0.10 hold, one allowed counting request, frozen source versions, and a short expiry. Successful admission transfers that hold to its one Work atomically and creates the matching Business reservation; acknowledgement retries reuse the preparation ID. Abandoned, definitely non-generating preparations release their generation hold. Any separately billed or uncertain preflight liability remains explicit. This avoids giving an unfunded user a provider-egress endpoint or counting against a mutable request. If account evidence establishes a simpler nonbillable count contract, retain the authority and input binding while simplifying only its cost settlement.

For the first isolated proof, the operator may prepare the fixed request through this same service before exposing a general preparation endpoint. Do not build a generic quoting system or add another runtime just for this step.

## Retained price and cost truth

Retain a new immutable Business tariff revision such as `openai-terra-standard-usd-2026-09-04-v1`, including retrieval date, source URL and digest, currency, context band, tier, and integer rates. Official standard short-context rates per million tokens are $2 input, $0.20 cached input, $2.50 cache writes, and $12 output. Do not silently read a mutable global pricing table when settling a historical operation. [API pricing](https://developers.openai.com/api/docs/pricing)

Record actual wire model, requested and observed reasoning when present, requested and observed service tier, provider response ID, input tokens, cached tokens, cache-write tokens, output tokens, reasoning tokens, and total tokens. Missing counters are distinct from zero. Reasoning is a subset of output and is not billed twice. Validate nonnegative counts and coherent subsets/totals.

Calculate ordinary input as input minus cached minus cache-write tokens. Sum each category times its retained tariff, then round upward once to USD micros using integer/rational arithmetic. At 8,192 input and 4,096 output, even pricing all input at the cache-write rate is $0.069632, beneath the proposed $0.10 ceiling. This is a generation bound for the specified standard route, not permission for regional uplifts, tools, other tiers, or a different model. Terra's long-context multiplier is irrelevant only because the enforced input ceiling is far below its documented threshold. [Terra limits and pricing conditions](https://developers.openai.com/api/docs/models/gpt-5.6-terra)

The UI should say “cost calculated from provider usage,” with the price revision available in details. It is not an invoice reconciliation claim. Missing usage, unsupported observed tier/model, or an incoherent envelope means unknown cost and retained liability. If output is usable but cost is unknown, keep the private factual output and unresolved cost state; do not label the operation fully settled.

## Durable journal and crash boundary

Add a private SQL provider journal, with tenant RLS, restricted runtime privileges, and immutable fact receipts. A next migration after `003_overview.sql` contains:

- Host provider grants and reservation/settlement records, with administrative grant issuance separated from runtime usage.
- Prepared request records: preparation ID, current tenant/actor/source bindings, exact private bytes and digest, route/price/credential references, token-count evidence, hold and expiry.
- An operation journal bound to organization, Business, Work, attempt, operation, exact request digest, host grant, credential binding, route, price, and retention policy.
- An append-only observation stream with sequence, observed time, event type, provider response ID when available, provider request/project identifiers when returned, bounded response evidence, parsed usage, and evidence digest. The mutable head may cache phase and next poll time but does not replace receipts.

Credential references identify a configured server secret and provider account/project binding; never store secret values. Response IDs are retained privately, not merely hashed, because recovery needs the exact ID. Enforce uniqueness of a response ID within its provider account/project binding and prevent attaching it to a second operation or tenant. A response mismatch is quarantined rather than adopted.

`PrepareOperation` remains the conservative durable marker that generation might have happened. Journal preparation and this marker must be ordered so a process can never send before both the exact request and possible-issuance record are durable. Immediately before POST, check current Work/source/resource authority and record the create boundary. Once that boundary is crossed, a lost acknowledgement is unknown; absence of a response ID is not nonacceptance.

On receiving any valid response ID, persist acceptance immediately, even when status is queued and there is no usage or output. Parsing a later field must not discard that recovery handle. Persist terminal provider evidence before invoking `CompleteAttempt` or `ReconcileAttempt`; a crash between those transactions can replay the saved factual observation without another generation or a still-retained provider object.

A superseded worker may need to record a late acknowledgement or cost fact for the exact operation it already issued. Provide a narrow append-only receipt method that accepts a previously established operation-bound receipt capability, verifies the exact immutable operation/credential tuple, and cannot issue, change authority, replace content, or terminalize Work. This capability is server-held, never a browser token. Current attempt fencing still exclusively controls result completion and settlement. Losing the lease must not throw away the only response ID or turn late evidence into renewed execution authority.

Provider facts are untrusted input. Store bounded envelopes and needed output/usage, not arbitrary headers, credentials, or unnecessary reasoning internals. Private source reads and result delivery continue through current tenant/source authorization; an inaccessible input does not become readable through a receipt.

## Execution, recovery, and cancellation

The existing Worker makes at most one Execute or Reconcile call per Step. Preserve that bound. Execute performs one create. Reconcile uses a retained terminal receipt or GET of the exact accepted response ID; it never reissues create. Background responses are polled while queued/in progress. The cancel endpoint is idempotent, including repeated cancellation of the same response. [Background mode](https://developers.openai.com/api/docs/guides/background)

| Observed boundary | Required behavior |
| --- | --- |
| No durable possible issuance | Retry local preparation under current authority; no generation claim |
| Possible issuance, no response ID | Retain unknown liability; no blind POST retry |
| Accepted ID, queued/in progress | GET only, preserve exact account and operation binding |
| Terminal response durably saved | Complete/reconcile from that receipt; exactly one Work result |
| Failed, incomplete, or cancelled | Preserve actual cost and factual output/status; no invented successful deliverable or automatic retry |
| Missing/incoherent usage | Unknown cost; retain reservation and block new grant use |
| Revoked Work or source authority | No fresh actionable success; reconcile incurred facts and cost privately |
| GET 404 after retention expiry | Unavailable evidence, not proof of nonacceptance or zero cost |
| Duplicate or conflicting observation | Identical receipt is idempotent; conflict is explicit and cannot rewrite a settled result |

Cancellation is an external control operation. Do not hide it in the current read-only Reconcile contract. Add a narrow optional `CancelExisting` adapter capability or a separate controlled cancellation driver: it can only cancel the journal's exact response under a recorded host/Work cancellation intent, never generate. Record request and observed outcome separately; “cancel requested” does not mean the provider stopped or cost is final. Reconcile then retrieves/settles final facts. Revocation must retain the ability to reconcile this existing liability through a host service principal even when the originating person loses membership.

For the first bounded runner, choose a 30-second lease and a shorter per-Step deadline; wait for lease expiry before a new claimant. Persist a next observation time and poll at most once per eligible Step. This works with current fencing without a new early-release API. A stopped runner resumes from the journal. A production scheduler can later use bounded due-work claims; no parallel legacy goal record owns the same Work.

Use explicit `store:false` for the first proof and observe recovery within the documented roughly ten-minute window. Set an eight-minute operational observation deadline; near the deadline, request cancellation when an exact ID exists and continue recording the final status if available. A longer outage may become irrecoverable unknown liability, which the product must show honestly. Local durable evidence survives after provider retention only if it was captured.

`store:true` can support longer retrieval where the account permits it, but it is a separate retention policy choice; ZDR can override it. It is not required to prove a short bounded local restart. Likewise, `store:false` is not a promise that no provider logs or temporary state exist. Application-state retention and abuse-monitoring retention differ. [Background retention](https://developers.openai.com/api/docs/guides/background), [Your data](https://developers.openai.com/api/docs/guides/your-data)

There is no verified existing OpenAI webhook registration or signing-secret configuration in this repository. Do not make webhooks a prerequisite or claim lost-create-ack recovery through them. A later project-configured signed webhook can supply a response ID; retrieve the response and verify its opaque operation correlation and account binding before adoption. Event signatures, duplicates, delayed delivery, retention, and cross-tenant mapping all need tests. [Webhooks](https://developers.openai.com/api/docs/guides/webhooks)

## Reuse and implementation ownership

Reuse the SQL Work, attempt, budget, result, fencing, and settlement contracts. Preserve explicit Scope propagation. Reuse strict parsing and endpoint-validation ideas from `internal/e10probe/responses.go`; it already distinguishes missing token counters and includes cache writes. Its CLI, synthetic probe authority, project-proof policy, and hashes-only receipt are not a Business execution adapter.

Do not call through `openai_responses.go`'s legacy text helper: it hardcodes synchronous `store:false`, returns text, has resilience/retry wrappers, and captures a response receipt only when usage exists. Its usage shape omits cache-write tokens. Those properties lose queued acceptance and can make uncertain issuance unsafe. Existing `models_pricing.go` does contain the current Terra rate in its history; that does not supply immutable per-operation Business settlement authority.

`dissent_internal_document_policy.go` is an experimental host-owned bounded routing pattern. It depends on legacy thread/request types and does not prove independent review. Extract a small pure policy contract later if useful; do not fabricate a Scout thread or import legacy memory/tenant authority to use it. This first adapter reports exact execution evidence and explicitly unavailable independent review.

Suggested independent implementation units:

| Owner / files | Contract |
| --- | --- |
| SQL owner: new `internal/business/provider_journal.go`, tests, next migration | Resource grant, prepared input, operation-bound evidence and idempotent durable facts; same SQL authority transaction |
| Transport owner: new `internal/business/openai_document_adapter.go` and tests | One background create, acceptance callback, retrieve, explicit cancellation capability, strict usage and price calculation; injectable HTTP transport |
| Root: `internal/business/worker.go` narrow integration and server runtime wiring | Scope already landed; preserve fencing and pure Plan, install adapter explicitly, run bounded due observations, keep dispatch disabled by default |
| Root: one private result/observation endpoint and UI projection | Actual document, assumptions, execution/cost/recovery status; source-safe next observation and Work lineage |

A versioned tariff can be embedded with the adapter and copied into the immutable journal. The journal package must not import the main app or legacy executor. Migration/runtime compatibility follows the existing checksummed Business migrations; retained old binaries may reject a newer schema and must not be described as an executable rollback without validation. Disable dispatch first on rollback, retain journal data and reconciliation ability, and never roll back by deleting possibly issued operation evidence.

## Acceptance and next observation

Before a paid call, fake HTTP plus real PostgreSQL tests must cover two tenants sharing one adapter; wrong credential/response bindings; competing claimers; crash before POST and after acceptance; late response receipt after lease loss; restart from queued ID; terminal receipt before completion; lost create acknowledgement; malformed/missing/cache-write usage; actual overage; cancellation/revocation; duplicate terminalization; and restart after provider retention expiry. Verify one create across every recovery scenario. Test that an ordinary creator's self-authored allowance cannot use the host key, and that two Businesses cannot oversubscribe one grant.

The real proof uses an explicitly host-granted local Builders Work and the frozen private experiment brief. Persist the accepted response ID, stop only its local worker at an observed boundary, restart, retrieve, settle actual usage, and render the immutable private result. Preserve the exact request/route/price/result receipts. A provider dashboard or returned project header may help verify credential binding, but key presence alone does not establish project access.

Read-only inspection found existing OpenAI key configuration in the original app's local/deployment environment files; the isolated worktree has no copied secrets. This is evidence of available configuration, not proof of live validity. Reuse the root's existing secret injection after verifying the intended credential binding; do not ask the founder for a new account or key merely because the new worktree is clean. No webhook secret/project-ID configuration was verified in that inspection.

The next observation is a durable, source-bound statement about this exact result: an owner review if actually supplied, or an explicitly labeled agent check against the five requested document criteria. An agent check cannot impersonate human acceptance or customer impact. Persist the observation and a proposed next Work derived from it, such as turning the chosen experiment into a test script. The next generation proceeds only within current mandate and remaining host/Business allowance. The first release can leave it proposed while still proving remembered feedback and resumption.

Success means a useful privately rendered document, a verified restart with no duplicate generation, truthful settled or explicitly unresolved cost, and a real retained observation that informs the next step. It does not mean an autonomous business has customers, a general agent marketplace exists, or multi-provider Dissent has been qualified.
