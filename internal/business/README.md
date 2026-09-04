# Business store boundary

This package is the authoritative organization, membership, Business, employment,
mandate, allowance, and Work-intent store for the new Business namespace. It does
not create legacy goals, import legacy membership, invoke providers, start workers,
spend money, or publish anything. Setup returns a draft Business and no employees.

The HTTP adapter supplies an already-authenticated stable person identity in
`Actor`; never decode Actor or Scope from a request body. A requested organization
ID is a selector, and current SQL membership authorizes it. Agent actors must be
bound by the worker to an existing employment ID, never chosen by a browser. The
server selects the namespace before calling this package. W4 organizations and
legacy execution remain separate.

`SetupBusiness` atomically creates an organization, owner membership, Business,
and initial allowance, or adds a Business to an existing organization where the
actor is currently an owner. The same actor/key/body returns the same result after
a lost acknowledgement. A changed body conflicts. Existing-organization setup
requires that organization's ID; new-organization setup requires its name.

`UpdateBusiness` is an owner-only compare-and-swap of the complete policy/state
triple. Leadership is `human_ceo` or `agent_ceo`; presets are `advise`,
`execute_assigned`, `take_initiative`, and `full_autonomy`. These are ceilings:
only the latter two permit an employed agent to propose a Work intent itself.
Owners can explicitly request private Work in any preset. Every admission still
requires an active Business, active employment, exact current mandate, current
issuing owner membership revision, unexpired mandate, bounded attempts, an open
Work slot, and available allowance. Full autonomy does not grant funding or
mandate-management authority. This first operation supports only
`private_document_v1` and grants no external action capability.

The funded and cap amounts are **owner-authorized internal model allowances in
USD micros**, not a payment or evidence of a funded provider account. Funding
never silently raises the cap. Available allowance is `min(funded, cap)-reserved-settled`.
Attempt leases, issuance markers, private results, and cost settlement now share
the same transaction boundary. Available allowance is
`min(funded, cap)-reserved-settled`. Actual observed cost can exceed the reserved
ceiling: the store records that overage truthfully and blocks even zero-reservation
new admissions while overdrawn. A completed/recovered operation with unknown cost
blocks new admissions for its Business and retains its held reservation. An active,
unexpired operation still has its known maximum reservation; expiry makes its
unsettled liability reconciliation-only.

A provider worker still does not exist. Before calling a provider it must supply
authoritative adapter/route/price/request records, bind source and resource
versions, implement cancellation/issuance handoff, and prove provider-specific
idempotency or reconciliation. A caller-requested reservation is not a provider
quote. `PrepareOperation` records **may have issued**, not provider acceptance,
permission to call twice, or proof an external effect happened. A retry must use
the exact operation ID with an adapter qualified for that behavior, or reconcile.
There is no automatic dispatch endpoint or background loop in this package.

Mutations serialize on the organization row inside PostgreSQL, including current
authority checks, reservation changes, intent creation, and append-only receipts.
No database lock crosses a provider or network call. Work captures exact Business
and employment revisions plus its mandate revision. Claim and issuance revalidate
those records, current membership/issuer authority, expiry and the current ceiling.
Revocation/policy changes increment the current attempt fence immediately. They
release definitely unissued reservations; a possibly issued operation stays in
reconciliation with its liability reserved.

`ClaimAttempt` gives one worker a database-clock lease of at most 300 seconds.
Concurrent claimers cannot both win. Repeating the same active claim key returns
the same expiry, not an extension. `RenewAttempt` cannot revive an expired lease.
Reclaiming an expired unissued attempt retains its ID and increases its generation.
An expired possibly issued attempt returns `mode: reconcile` and the original
operation ID; it cannot be prepared or completed through the fresh-execution path.
Only explicit provider evidence of nonacceptance and known cost permits another
attempt, beneath the same Work root, within its original MaxAttempts and remaining
reservation. Failures and succeeded operations do not automatically retry.

Completion atomically commits a unique result per Work, its digest, current state,
and known cost settlement. Result bytes are immutable private markdown. Unknown
cost uses a SQL NULL and `costState: unknown`, never an invented zero; cost may be
reconciled later without creating another result. A stale lease cannot complete,
renew, or issue. Same-generation, unchanged terminal completion acknowledgements
return the saved receipt; differing content/cost conflicts. After expiry, read the
current Work/result rather than treating a failed acknowledgement as permission
to repeat execution.

Revoked authority may still reconcile already incurred cost or factual outcome,
but cannot convert it into an actionable success: Work remains cancelled and the
private result is ineligible. Result eligibility stored at creation is historical;
`GetResult` also rechecks current authority so a later takeover/revocation cannot
silently inherit an earlier eligibility claim. The result is not publishing
permission in any case. The next adapter must honor this distinction before any
external action. No provider bytes leave this package and no actual call is made.

Mandate expiry blocks new admission and issuance. This slice has no scheduler or
expiry sweeper; an unissued expired mandate may keep reserved allowance until an
owner cancels/revokes it. Recovery is explicitly invoked, never a model-call polling
loop. Idempotent command replies are immutable historical receipts; read APIs
expose current state after later cancellation or policy changes.

## Database roles and migration

Use a separate RLS-bypassing administrative connection for `Migrate`. It applies
checksummed embedded migrations in one transaction under an advisory lock and
uses its own ledger, independent of legacy canonical-store migrations. Run this
as an explicit release step, never through the HTTP request path. Migration has
no down/destructive path. Retain the schema and data on rollback; rollback builds must remain attempt-aware. A pre-attempt Business build must
have the entire Business namespace/DB connection disabled, because its old
cancellation path would incorrectly release potentially issued reservations.
Dispatch disabled alone is insufficient. Preserve SQL records for a compatible
recovery build, and never reinterpret Business records as legacy work.

The migration creates the NOLOGIN `business_runtime` group. Provision a separate
LOGIN, NOSUPERUSER, NOBYPASSRLS runtime role and grant that group to it. Runtime
must not own the schema/tables, inherit their owner, or be able to assume an
RLS-bypassing role. `New` checks those constraints and the exact embedded migration set/digests before serving requests. Never pass the admin pool to
`New`. Runtime has no delete privilege; operation receipts and events also have
no update privilege. All tenant tables use forced RLS. Every transaction sets its
organization scope locally, avoiding connection-pool scope leakage.

RLS protects against missing tenant filters and cross-tenant data mistakes. The
runtime SQL credential and `Actor` argument are trusted server-side capabilities:
custom PostgreSQL scope settings are not a defense against an attacker with that
credential or arbitrary SQL execution. Only the fixed SECURITY DEFINER directory
function crosses scope to list current memberships for an authenticated person;
it returns organization ID/name/revision and grants no execution authority.
Its migration owner must retain BYPASSRLS for this narrow query to work.

Business fields and typed entity bodies are validated through this package;
composite database foreign keys separately forbid references into another
organization. Any future writer must use the same authority transaction boundary.
Backups and restore rehearsals must include this schema and its migration ledger
before production is enabled. There are no production migrations in this change.

## Verification

`./internal/business/test-postgres.sh` creates its own disposable PostgreSQL
cluster, runs the tests under a genuinely restricted LOGIN role with the race
detector, and stops/removes only that cluster. It uses local PostgreSQL binaries
and makes no provider calls. Alternatively point `BUSINESS_TEST_DATABASE_URL` at
a disposable administrator database and run `go test -race ./internal/business`.
The DB test skips without that variable; a default Go suite pass alone is not
PostgreSQL execution evidence.
