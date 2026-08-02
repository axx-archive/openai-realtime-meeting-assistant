# STRIDE E10 provider qualification runbook

This runbook governs the smallest provider-facing checks that may convert the
E1-E9 deterministic candidate into provider evidence. It does not authorize a
route change, production mutation, deployment, release, or feature activation.

## Authority and stop rules

- The operator must have explicit authority to use the credential and its
  bounded project quota. A top-up is not authority for production changes.
- Every invocation binds a new private receipt directory, the exact candidate
  manifest SHA-256, and an acknowledgement nonce. Prefer the SHA-256 of a raw
  `OPENAI_PROJECT_ID` supplied to the request. If the provider and connected
  Platform account do not expose that ID, a user-authorized `sk-proj` credential
  may run a contract attempt only under the explicit project-key flag.
- The probe never reads an env file. The operator supplies `OPENAI_API_KEY` and
  `OPENAI_PROJECT_ID` in the process environment without printing either one.
- Stop the affected lane on the first project, access, schema, price, budget,
  quality, or ledger failure. Do not retry through another model or provider.
- Receipts are contract observations. They do not qualify a route, corpus,
  physical device, production system, or product promise by themselves.

### Narrow prequalification exception

The complete E10 entry gate in the master plan applies before any call that
uses real company/user content, attempts route or seat qualification, or could
support production activation. It does not prohibit a separately authorized,
single-attempt synthetic contract observation whose sole purpose is to learn
the current endpoint/event/usage shape. Such an observation is permitted only
when all of the following are true:

- the user explicitly authorizes that exact paid call after its model, endpoint,
  spend ceiling, input class, and stop condition are stated;
- the input is generated, non-sensitive, bounded, and retained only by digest;
- the candidate, probe binary, request schema, price row, and private receipt
  destination are frozen before network I/O;
- no production data, route, configuration, feature flag, deployment, Git
  state, device distribution, or fallback is changed;
- credential/project attribution is recorded exactly as observed and an
  unreconciled project can produce only `provider_contract_attempt` evidence;
- the affected lane stops after one attempt, including partial generation or
  ambiguous terminal usage, unless the user separately authorizes a retry.

This exception records the authority boundary used by the 2026-08-01 synthetic
contract attempts. It does not waive the full entry gate, permit a real corpus,
or promote any seat to `provider_qualified`.

## Frozen evidence inputs

Before any provider request, create one private evidence directory and freeze:

1. a manifest containing the candidate base commit and every in-scope changed
   or untracked file with its state, byte length, and content SHA-256;
2. one synthetic, non-sensitive PCM RIFF/WAVE speech fixture of at most ten
   seconds and two MiB;
3. the exact UTF-8 reference text spoken in that fixture; and
4. the SHA-256 of each file above.

The probe rejects missing files, symlinks, non-regular files, digest mismatch,
incoherent PCM metadata, trailing RIFF data, duplicate format/data chunks, and
an existing receipt directory. The exact approved WAV bytes are parsed, hashed,
and uploaded; the exact reference digest is bound to the request shape.

## Ordered OpenAI checks

1. Confirm the intended project identity and spend ceiling outside the probe.
   A project-scoped-key exception may be used only for the narrow synthetic
   transcription/Realtime contract observation above. Responses, embeddings,
   images, real corpora, and every qualification run require the canonical raw
   `OPENAI_PROJECT_ID` plus intended-project and billing reconciliation.
2. Run one non-generative `GET /v1/models/{model}` access check per allowlisted
   seat. This confirms only credential-bound model-object access.
3. Run one bounded `gpt-transcribe` file request with a declared spend ceiling
   no lower than the local estimate and no higher than USD 0.05. For committed
   Realtime transcription, correlate the app-minted turn with the provider
   `item_id` across commit, item-created/finalized, and completion events; never
   assign finals by FIFO arrival order.
4. Inspect the body-free receipt. It may retain hashes, status, latency, usage,
   local/provider duration, calculated cost, and admission ceilings, but not the
   key, raw project/org/request IDs, audio, reference text, or transcript.
5. Only after this contract gate passes, run the preregistered authoritative
   meeting-STT and composer-dictation corpora. Each signed corpus must bind the
   corresponding immutable `qualification_evaluator` target and fixture in the
   independently anchored E10 registry. A real corpus result—not this synthetic
   probe—is required for provider qualification.
6. Observe the personal/meeting Scout contract on a new
   `gpt-realtime-2.1` server-owned session before its qualification corpus. The
   current harness treats `conversation.created` as optional, accepts documented
   completed or incomplete assistant-item terminals, preserves partial output
   on failure, and treats only `response.done.status=completed` with reconciled
   terminal usage as success. The current bounded response cap is 256 output
   tokens under a USD 0.02 admission ceiling. Missing `response.done`, an
   incomplete response, inconsistent usage, or a ceiling breach is a failed
   contract attempt—not zero usage and not a retry invitation.
7. Qualify Scout only with the preregistered corpus: at least 2,000 ordinary
   speech cases, 500 explicit invocations, and 1,000 audience-authorization
   negatives, including false-response, invocation-recall, latency, barge-in,
   audience, terminal-usage, and cost thresholds. The invited-specialist lane
   remains separate and later.
8. After Scout passes, qualify one default-off server-side specialist session
   with its own context-envelope, floor, barge-in, teardown, audience, usage,
   and acoustic-loop corpus. It must not inherit a Scout receipt.

## Current 2026-08-01 contract status

- Model-object access and one bounded file `gpt-transcribe` contract passed.
- One synthetic committed-turn Realtime `gpt-transcribe` contract passed with
  exact provider `item_id` correlation and reconciled duration/cost.
- The separately authorized Scout retry produced partial audio/transcript but
  did not yield a valid successful `response.done`; its usage and spend remain
  unreconciled. The post-attempt parser correction is locally verified but has
  not been provider-retested.
- Every paid-call authorization recorded for those attempts has been consumed.
  No additional provider call is implied by this runbook.
- No seat is `provider_qualified`; real corpora, physical devices, project and
  billing reconciliation, approvals, release identity, and production evidence
  remain open.

The access state `request_bound` means the caller supplied a raw project ID
whose digest matched the independently expected digest before network I/O.
`provider_verified` additionally means the provider returned a matching project
echo. Because that response header is not a documented contract, its absence
does not fail a request-bound probe and its presence does not replace owner or
billing-console confirmation. `project_credential_bound_unreconciled` means an
explicitly admitted `sk-proj` key selected one provider project but the raw ID
could not be independently reconciled. That state can support a bounded contract
attempt only; it cannot support route qualification, billing reconciliation, or
production activation.

## Receipt interpretation

- `classification=provider_contract_attempt`: a bounded provider call occurred.
- `success=true` and `outcome=pass`: the exact narrow response contract passed.
- any other outcome: retain the receipt, stop the lane, and diagnose without a
  fallback.
- `computedCostUsd` is reconciled from provider-reported duration when present,
  otherwise from the locally parsed WAV duration and the dated price row.

Receipts may be attached to the durable E10 provider-attempt ledger only after
their candidate, project, model, schema, price, fixture, and reference digests
have been rechecked. Promotion to `provider_qualified` requires the full frozen
corpus, quality/latency/cost thresholds, independent review where specified,
and the route-specific rollback evidence in the master plan.

After evaluation, retain the full evaluator output outside the application and
place its SHA-256 in a dual-signed
`stride.e10.qualification-result/v2` packet. The packet and its signed
`corpus/v4` or `pilot-packet/v5` source bind the tenant, qualification-subject
digest, registry, candidate tree/image/config/route digests, and exact
preregistered evaluator revision. `e10-evidence -mode qualification-result`
emits a canonical import bundle containing the registry, source, result, and
all detached signing material. The trusted store re-verifies that bundle
against separately configured trust-root bytes and their approved digest on
every import and reload; opaque in-process capabilities and local seed data are
ineligible. Import is durable and one-use but still does not authorize route
activation, deployment, release, or launch.

For `meeting_specialist`, the target fixture is the canonical digest of the
signed provider/model/voice, route, accounting profile, runtime profile, and
capability-policy binding. The same digest appears as the qualification subject
in the source and result. An adapter configured for any different field set
therefore cannot borrow the qualified result.
