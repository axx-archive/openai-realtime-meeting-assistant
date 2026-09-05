# Private local document proof

Operator-only qualification tool. It is not a server, scheduler, product UI, or
claim of a qualified autonomous company. Root must authorize and execute live
provider calls. No provider call is part of this directory's tests.

Provision an isolated local PostgreSQL database named `stride_business_proof_*`
and a restricted login role with no superuser or RLS bypass. Supply separate
administrator and runtime DSNs for that same endpoint/database in
`STRIDE_PROOF_ADMIN_DATABASE_URL` and `STRIDE_PROOF_DATABASE_URL`.
Prepare applies the embedded Business migrations and grants `business_runtime`
to the exact runtime username. Store always uses the restricted connection.
Only literal loopback hosts or a Unix socket are accepted, including fallbacks.
A local endpoint is an operator trust boundary: do not point it at a tunnel to
production.

Set `OPENAI_API_KEY` and `OPENAI_PROJECT_ID` privately in the process environment.
Do not place credentials on a command line or in the state directory. The
retained fingerprint binds the exact key without retaining the key itself.

```
go build -o /tmp/stride-business-proof ./cmd/stride-business-proof
/tmp/stride-business-proof prepare --state-dir /absolute/new/private-proof --allow-live-model
/tmp/stride-business-proof step --state-dir /absolute/new/private-proof --allow-live-model
```

Prepare creates a new 0700 directory exclusively, creates the private STRIDE
Business setup, employment, mandate and host allocation, then calls the official
token-count endpoint once before admission. It admits one generation with a
$0.10 maximum. `prepared.json` and `counted.json` preserve partial progress;
`state.json` indicates admission finished. Files are 0600 and never overwritten.
If preparation fails, inspect the private checkpoint and database; do not rerun
against another directory as an automatic retry.

To diagnose counting against an existing `prepared.json`, the operator may run
`count --state-dir /absolute/existing/private-proof --allow-live-model`. This
checks the exact database, credential and current stored Business source before
one count request, writes a new private `count-diagnostic-*.json` receipt, and
prints only the token count or a static failure category. It creates no new
Business, grant, Work, or generation and does not convert the diagnostic receipt
into admission state.

Each step starts a new process, validates the exact persisted request, count,
source, database and credential binding, then calls Worker.Step exactly once.
The first step may create one provider response. A subsequent process reconciles
that exact response with GET, never another generation. Wait at least 35 seconds
after the prior lease renewal before restarting; an active lease returns busy.
There is no polling or retry loop. A missing response acknowledgement remains
unresolved and does not authorize another POST. A response can finish during the
first step; that proves completion but is not evidence of restart recovery.

Private `step-*.json` files retain actual state and evidence; a delivered result
also writes `result-*.json` and `result-*.md`. Stdout contains only bounded state
and IDs. Error output deliberately omits raw provider and database errors to
avoid exposing credentials or document content. Keep this directory private.
