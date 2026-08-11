# STRIDE E10 OpenAI function-tool loop — decision contract

Status: `design_frozen_implementation_and_activation_pending`

Date: 2026-08-10

## Purpose

Restore ordinary multiplayer agent work on OpenAI without silently weakening
the authority-filtered tool loop that the retired Anthropic runner provided.
This contract does not enable a provider, tool, route, feature, or production
configuration. Until every gate below passes, ordinary tool-dependent work
stays explicitly unavailable.

## Provider and privacy decision

- OpenAI Responses is the only generative provider for this path.
- A request defines custom `function` tools and sets
  `parallel_tool_calls:false`; effects remain serialized.
- The model returns one `function_call` with a nonempty `call_id`, registered
  name, and JSON-object `arguments`.
- The next request returns exactly one `function_call_output` with the same
  `call_id` and the bounded recorded result.
- Requests use `store:false`. STRIDE manually replays the prior response output
  items plus the matching function output. It does not use
  `previous_response_id`, remote Conversations, or hidden server-side state.
- Research keeps its separate OpenAI hosted `web_search` contract. STRIDE does
  not fabricate hosted-search results through a local function.
- Fiscal remains a governed non-generative source/data egress lane. It is not a
  second generation provider and remains keyless/fail-closed when unconfigured.

Official contract references:

- <https://developers.openai.com/api/docs/guides/function-calling>
- <https://developers.openai.com/api/docs/guides/conversation-state>
- <https://platform.openai.com/docs/api-reference/responses>

## Closed runner identities

1. `openai_text` — research with hosted web search, or an exact pre-grounded
   process writer carrying all four durable fields: `goalParentId`,
   `goalSubtaskId`, `goalDeliverable=true`, and nonempty `outputContract`.
2. `openai_tools` — ordinary server-minted agent work admitted only after the
   authority and journal gates below pass.
3. `stub` — missing capability, missing authority, unknown/stale assignment,
   retired Anthropic assignment, malformed tool schema, unavailable journal,
   or any ambiguous prior effect.

No environment key, persisted runner string, model output, or client field may
upgrade `stub` to either OpenAI runner.

## Exact tool exposure

The tool list is derived only from the existing server-owned catalog:

- `kanbanTools()`;
- `privateScoutNativeToolDefinitions()`;
- `coworkerDelegationToolDefinition()`;
- `report_goal_state`.

`orchestratorToolPolicies` is the sole product-effect authority map. A recursive schema
compiler produces Responses strict JSON Schema:

- every object has `additionalProperties:false`;
- every property is listed in `required`;
- previously optional fields become nullable unions;
- unsupported schema keywords/types fail closed;
- schema and tool-set digests are durable inputs to every operation receipt.

The compiler also freezes an argument-normalization map alongside each schema.
For a field that was optional before strict conversion, an explicit JSON
`null` is removed before defaulting, canonical serialization, argument digest,
semantic-effect identity and dispatch. A field that was originally required
rejects `null`. Missing required properties, unknown properties, noncanonical
numbers, duplicate object keys and a null/default ambiguity fail closed. Thus
strict-wire compatibility cannot silently change the existing dispatcher's
absence/default semantics.

`report_goal_state` is control-only. It cannot grant authority, mutate product
state, or count as a board effect. It updates only the bounded sticky stage,
status, review gate, progress and note projection. It is the one explicit
exception to `orchestratorToolPolicies`: admission is governed by a separate
closed control policy and it never enters the product-effect dispatcher.

### Closed admission manifest

Catalog membership alone never exposes a tool. A checked-in, versioned and
digest-bound admission manifest is the intersection of the catalog, the
authority policy and the OpenAI runner. A newly registered catalog tool remains
unavailable until its manifest row and tests are independently accepted.

The first manifest admits exactly four tools. The compact identifiers in this
table are closed implementation interfaces, not descriptive placeholders:

| Tool | Authority / effect | Canonical arguments | Preimage / postimage / reconciliation | Final use, fan-out, minimized result, required suites |
|---|---|---|---|---|
| `report_goal_state` | `goal-control-v1` / `control` | exact catalog fields `goal_status`, `review_gate`, `stage`, `progress_percent`, `note`, all present on the strict wire and nullable-normalized from the current optional catalog | sticky goal revision / exact monotonic goal projection / compare-and-merge same operation | held goal+thread capability; one authorized progress event; bounded state+receipt only; `ToolGoalStateNormalRaceRestart` |
| `answer_memory_question` | policy `read_only:read` / `read` | exact required catalog field `query`; no limit or adapter-added model field | exact tenant memory high-water+source window / same source receipt / reread receipt, never cache stale body | held tenant/person/thread read capability; no product fan-out; bounded answer+source refs; `ToolMemoryReadNormalRaceRestart` |
| `create_artifact` | policy `workspace_write:artifact_write` / `write` | exact catalog fields: required `mode`, required `query`, optional `content` represented as nullable-required only on the strict wire and removed when null before dispatch | exact server-resolved thread+artifact collection generation / created artifact authorization header+content digest / locate by semantic operation ID and verify exact owner/thread/private postimage | held requester/thread write through artifact CAS and card/event delivery; one authorized projection event; artifact ID/title/type/status receipt, never body; `ToolCreateArtifactNormalRaceRestart` |
| `update_artifact` | policy `workspace_write:artifact_write` / `write` | exact catalog fields: required `artifact_id`, optional `title` and `content` represented as nullable-required only on the strict wire and removed when null before dispatch; artifact revision/content expectations are server-resolved and never model arguments | exact current authorization header+full postimage / next header+full postimage digest / reread expected prior or exact committed successor | held requester/thread/artifact write through CAS and delivery; one authorized projection event; artifact ID/revision/status receipt, never body; `ToolUpdateArtifactNormalRaceRestart` |

All other currently known catalog names are explicitly unadmitted in manifest
version 1, including policy-bearing names: `create_ticket`, `move_ticket`,
`add_tags`, `add_key_date`, `remove_key_dates`, `update_ticket`,
`note_for_the_record`, `meeting_interval_recall`, `cross_meeting_briefing`,
`get_meeting_detail`, `create_package`, `attach_to_package`,
`advance_package_stage`, `send_notification`, `archive_channel`,
`rename_channel`, `create_file_folder`, `rename_file_folder`,
`delete_file_folder`, `delete_file`, `request_coworker_help`, `do_nothing`,
`company_financial_snapshot`, `financial_comps`, `fiscal_api_docs`, and
`fiscal_data_query`; and catalog names without a current policy row:
`delete_ticket`, `undo_delete_ticket`, `control_app`, `set_voice_control`,
`set_recording`, `archive_meeting`, `publish_artifact`, `launch_agent_thread`,
`portfolio_health`, `propose_codex_task`, `organize_files`, `save_to_files`,
`post_to_channel`, `create_channel`, `meeting_recap`, `catch_me_up`,
`start_grill_session`, `end_grill_session`, `start_private_grill`,
`end_private_grill`, `read_thread_aloud`, `start_chat_as_user`, and
`initiate_goal`. Unknown future names are also unadmitted. These tools stay
absent from the provider request and dispatch returns stub even if model output,
an environment value or a persisted assignment names one.

The executable manifest repeats every table field as typed data. Its validator
rejects a missing field, duplicate tool, catalog drift, policy drift, unknown
effect class, or admitted tool without its named normal/race/restart suite. A
future manifest revision must add an exact row and independent tests before any
currently unadmitted tool becomes visible. There is no family/default policy.

## Current-authority capability

Model output is an untrusted proposal, never authority. Before any local tool
effect, the server resolves an immutable expectation containing:

- tenant ID;
- person ID and normalized requester account;
- exact session hash and active-organization-session ID;
- membership ID/revision and organization ID/revision;
- thread ID, artifact ID/revision and source-window digest;
- granted job authority and exact tool-policy revision;
- tool name, schema digest and arguments digest.

The current session/membership/organization resolver holds its locks/capability
callback through the tool's final durable effect and journal commit. Revocation,
session switch, organization switch, artifact/source revision change, coworker
pause/offboard, or policy drift returns a bounded unavailable result and writes
no product effect.

All tool dispatch receives the held context. No new path may use
`context.Background()` to escape the caller's cancellation or authority.

## Durable operation journal

Every proposed function call has a provider-independent semantic-effect ID:

`HMAC-SHA-256(effect-key, domain || tenant || person || thread || artifact
revision || source-window digest || tool || schema digest || canonical
arguments digest || policy revision)`.

Provider response IDs and call IDs are separately recorded correlation values;
they are never part of effect identity. A retry that produces a new response or
call ID but the same authority, source and canonical effect resolves to the
same semantic-effect record. Reuse of a call ID for a different semantic effect
is a collision and quarantines both correlations. Each semantic tuple binds to
one immutable random operation ID; a keyed semantic digest is an index alias,
not the durable operation identity.

The separate default-off journal contains two authenticated stores:

1. a body-minimized operation index with states `reserved`,
   `effect_committed`, `continuation_sent`, `completed`, or `quarantined`; and
2. an encrypted replay envelope containing the exact bounded Responses output
   items needed for manual continuation—including function and reasoning
   items—and the exact bounded function result.

Replay envelopes use a dedicated managed AEAD key. Journal records use a
different managed MAC key, and semantic-effect IDs use a third key; key IDs and
versions are explicit and pairwise key-ID/secret reuse is rejected. Every
generation MAC covers its domain, tenant, journal ID, monotonic generation,
previous-generation digest, state, semantic-effect ID, authority expectation,
all provider correlation digests, schema/arguments/policy/source digests,
effect postimage, bounded-result digest, encrypted-envelope digest, timestamps,
attempt count and key versions. The generation is committed by locked
temp-file write, file fsync, atomic rename, directory fsync and reread.

Open and every transition verify the entire generation/CAS chain plus exact
file identity and private custody: absolute canonical parent, owner-only
directory/file modes, regular file, one link, no symlink/hardlink/path swap,
stable dev/inode/uid/mode/size/content through read, no truncation, rollback or
trailing data. Wrong, unknown, reused or retired keys; altered ciphertext,
record, link or generation; missing envelope; stale backup; and any parseable
substitution fail closed without a provider or product effect. Rotation accepts
authenticated historical records only through their exact historical key
versions. Effect-key rotation is a journal-wide transaction: while old and new
versions are both authorized, decrypt every live semantic tuple, compute its
new keyed alias, bind old and new aliases to the same immutable operation ID,
write and reread the next authenticated generation, and only then advance the
current key authority. The old alias remains authenticated until its operation
is purged; an effect key cannot retire until a signed migration receipt proves
every live operation has a current-version alias. Admission looks up all
retained authorized aliases and rejects multiple operation IDs for one tuple.
Crash/restart resumes the rotation generation, and stale pre-rotation backup
restore is denied by the journal high-water. Therefore the same semantic effect
before, during or after rotation always resolves to the original operation.
Retention, subject/tenant purge, backup and restore operate on both stores and
every alias together; deleting one without the others is corruption, not
absence.

Required transaction order:

1. hold current authority;
2. reserve exact operation with monotonic CAS and durable fsync/rename receipt;
3. durably encrypt and bind the exact response output items before an effect,
   and reject collisions or changed arguments;
4. execute the effect once;
5. reread the exact product postimage;
6. atomically mark `effect_committed` with the postimage and bounded result;
7. reconstruct and return the exact recorded `function_call_output`;
8. durably mark `continuation_sent` before sending the manual continuation;
9. only after the provider response is validated and its exact output is
   encrypted for the next turn, mark the prior operation `completed`.

Restart rules:

- `completed` replays the exact encrypted transcript/result without another
  effect;
- `effect_committed` revalidates the exact postimage and reconstructs the exact
  prior output items plus function result before resending the continuation;
- `continuation_sent` uses the same exact transcript/result reconstruction; a
  new provider response/call ID maps through semantic-effect identity and
  cannot duplicate an effect;
- ambiguous/missing postimage quarantines and requires operator reconciliation;
- `reserved` may execute only if the effect's preimage is still exact;
- an unavailable/corrupt journal prevents all function-tool provider admission.

Tests must kill and reopen the process after response receipt, after reserve,
after product effect, after effect commit, after continuation send and before
completion. Each restart must either reconstruct byte-identical continuation
material and converge once or quarantine with zero additional effect.

## Tool-specific invariants

- only the four manifest-version-1 rows can be exposed or dispatched;
- artifact creates and updates remain private to the exact requester/thread;
  publication is not admitted by this runner;
- memory reads bind their exact high-water/source window and cannot replay as
  current after source revision;
- `report_goal_state` has no product-effect path and cannot satisfy an artifact
  deliverable by itself;
- all tool results use the existing 24,000-character cap; errors are bounded
  structured results and never expand authority.

## Progress and terminal truth

- emit progress only after an accepted function call is durably journaled;
- preserve sticky `report_goal_state` and monotonic progress high-water;
- terminal success requires a completed Responses envelope, zero pending tool
  calls and a contract-valid artifact body. The same current
  session/membership/organization/source capability is held—not merely
  rechecked—through terminal artifact CAS, its journal linkage, durable chat/work
  projection save and authorized socket/event fan-out. Revocation or source
  drift at any point leaves the prior artifact/projection bytes unchanged and
  emits only the bounded unavailable state to the still-authorized requester;
- incomplete, refused, malformed, multi-call, unknown-tool, max-token,
  max-turn, cancellation or journal ambiguity yields
  `needs_attention/blocked` and never a verified artifact;
- evidence names `openai_tools`, exact model/reasoning, turns, tool-set digest,
  journal receipt digest and zero Anthropic fallback.

## Acceptance matrix

Implementation cannot be accepted until normal and race tests prove:

1. exact function-call wire shape, `store:false`, manual history replay and
   `parallel_tool_calls:false`;
2. strict schema closure, optional-null stripping/default normalization,
   required-null rejection and deterministic tool-set/schema/arguments digests;
3. authorized read and write calls plus read-only mutation denial;
4. session/membership/org/source/profile revocation before provider, between
   turns and during final effect, all with byte-no-change negatives;
5. distinct managed semantic/MAC/AEAD keys; historical rotation plus wrong,
   retired, reused-key, tamper, truncation, rollback, stale-backup, link/path and
   encrypted-envelope negatives with zero effect;
6. durable reserve/effect/continuation/complete restart at every crash boundary,
   new provider/call IDs for the same semantic effect, concurrent duplicate,
   changed-arguments collision, same-effect replay across effect-key rotation,
   retirement and process restart, and ambiguous-postimage quarantine;
7. byte-exact transcript/result reconstruction with zero duplicate
   provider-independent effect;
8. bounded tool result, max turns, cancellation, incomplete/refusal/malformed,
   unknown tool and duplicate/multiple-call rejection;
9. the exact closed admission manifest covers every catalog tool; every admitted
   row proves authority/effect/arguments/preimage/postimage/reconciliation/
   final-use/broadcast/result contracts and every unadmitted tool remains stub;
10. monotonic progress, sticky approval gate, one board broadcast per committed
   turn and none for read-only/error/replay;
11. file ACL, coworker pause/offboard/recursion, Fiscal keyless/bounds and
   `report_goal_state` control-only hard negatives;
12. terminal authority held through artifact CAS, journal linkage, projection
   save and fan-out with revoke/switch interleavings at each boundary;
13. no Anthropic request, key admission, configuration fallback, retry or
    provenance across every case;
14. process-writer and research paths remain unchanged; deterministic workbook
    and package compile/render remain providerless;
15. focused normal/race, full Go, vet, formatting/diff and independent critic
    PASS on one frozen source boundary.

## Rollback and activation boundary

The implementation is a new default-off runner and separate journal. Rollback
removes its registration/config while leaving existing artifacts, Work Records,
W4 private authority and deterministic pipelines untouched. No production
enablement occurs until exact OpenAI project/billing/cost authority, managed
journal keys/storage/backup/purge, reviewed release, AJA work matrix and
physical-device acceptance are separately receipted.
