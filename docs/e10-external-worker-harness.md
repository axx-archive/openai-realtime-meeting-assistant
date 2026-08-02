# E10 external worker boundary harness

## Status and evidence boundary

The repository now contains an executable, token-free model of the external
worker boundary:

```bash
go run ./cmd/e10-worker-harness
```

The command validates `deploy/e9/worker-isolation-policy.json` and executes one
synthetic `insights_opportunities_v1` run through the boundary contracts in
`internal/e10worker`. It prints a body-free
`stride.e10.worker-harness-receipt/v1` receipt. A valid receipt says:

- `state:"local_contract_passed"`;
- `evidenceClass:"local_token_free_boundary_harness"`;
- `providerCalls:false`, `networkConnections:0`, and
  `productionMutation:false`;
- `productionReady:false` and `activationDefault:"off"`.

This is an executable contract harness, not an external runner. It does not add
a Compose service, worker image, binary mode in the application, queue consumer,
provider adapter, production callback handler, credential endpoint, or network
dialer. Production execution remains compile-time/default-off under the E9
policy.

## Trust boundary

The harness admits a run only when all of the following hold:

1. The run and workflow IDs are explicit and the workflow is pre-registered.
2. The sandbox is ephemeral, its root is read-only, its only writable/mounted
   paths are `/tmp` and `/workspace/run`, and its network mode is the synthetic
   gateway.
3. The environment contains only the non-secret run/workflow identity keys.
   Database URLs, provider keys, DR encryption/signing keys, and every
   unreviewed environment key fail closed.
4. Gateway targets are HTTPS names under the reserved non-resolving
   `.test.invalid` suffix. IP literals, localhost/private/metadata names,
   credentials in URLs, ports, queries, fragments, redirects, and unlisted
   hosts fail closed. The gateway is an authorization reducer only; it has no
   dial method.
5. The in-memory broker issues an opaque HMAC-bound credential for one run,
   workflow, generation, audience, and explicit scope set. TTL cannot exceed
   the E9 maximum, and a workflow cannot request scopes or a byte budget above
   policy.
6. Per-workflow gateway call and byte quotas are claimed atomically. Concurrent
   requests cannot over-admit.
7. Completion callbacks bind run, workflow, audience, nonce, idempotency key,
   generation, fencing token, terminal status, result digest, and timestamp.
   Signatures are checked before commit; nonce replay is rejected; one logical
   terminal is allowed per run; an exact retry with a fresh nonce is a no-op;
   and a different terminal key or binding fails closed. The local replay set
   is capped at 64 nonces per run, after which further retries fail closed. A
   production callback path still requires the external durable replay store
   listed below.
8. Run, workflow, and global kill switches revoke future credential, gateway,
   and callback authority. Fencing generation changes make old leases stale.

The receipt includes only digests and named controls. It contains no prompt,
source body, result body, endpoint, bearer value, signing key, or provider
identifier.

## Verification

Run the focused normal and race suites:

```bash
go test ./internal/e10worker ./cmd/e10-worker-harness
go test -race ./internal/e10worker
```

The tests cover the happy path, policy and receipt tamper, forbidden mounts and
environment, credential signature/scope/audience/expiry, gateway IP/metadata/
redirect/query/host denial, quota exhaustion, callback signature/skew/fence/
replay/idempotency, one-terminal and nonce caps, kill switches, and concurrent
quota/callback decisions.

## Installation gates that remain external

Do not relabel a harness receipt as installed or production evidence. Before an
external worker can be enabled, the operator still needs every receipt listed
in `worker-isolation-policy.json`, including:

- an independently identified external per-run orchestrator and create/destroy
  receipt for a fresh container and worktree;
- runtime inspection proving read-only root, exact mounts, dropped authority,
  and no production/company-brain volume;
- an independently enforced default-deny gateway with DNS resolution and
  rebinding checks, redirect re-authorization, IP/private/metadata blocking,
  and an exact production allowlist;
- a real short-lived credential broker backed by approved secret custody and
  immediate run/workflow/global revocation;
- externally enforced CPU, memory, PID, wall-time, and network-byte quotas;
- a durable callback replay/idempotency store integrated with the authoritative
  WorkRun state machine;
- provider/account/project reconciliation, per-seat qualification, operational
  paging, incident response, and an approved activation/rollback ceremony.

Those changes require a separately reviewed E10 installation contract. They
must not be added to the live DigitalOcean Compose deployment by extending this
local harness.
