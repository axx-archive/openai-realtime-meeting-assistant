# Scout Team Runs And Memory Fabric

Status: decision complete for staged implementation; no autonomous durable-memory writes are authorized by this document.

## Product decision

Scout is the accountable chief of staff for one durable team run. Researcher,
Designer, Critic, and future specialist roles become real child assignments only
when the runtime actually invokes a separately identifiable worker with its own
brief, capability fence, context lease, lifecycle, and output contract.

The product must never fabricate a team conversation from prompt stages. Current
panels are shown as independent perspectives, compilers and renderers are shown as
technical work, and an agent is described as hired only when a durable workforce
or built-in child assignment exists.

The channel contains one root work surface, genuine human decisions, and the final
deliverable. The desktop sidecar and mobile activity sheet are projections of one
canonical team-run event stream. They expose useful operational summaries and
references, never chain-of-thought, hidden reasoning, prompts, credentials, token
streams, or raw tool output.

## Existing foundations and the seam

STRIDE already has bounded coworker delegation, resumable goal subtasks, process
panels, AgentMind judgments, reviewed agent learnings, workforce profiles, strong
ACL/freshness/purge context contracts, canonical projections, and broad retention
machinery. The missing seam is a single identity- and event-bound relationship
between those systems.

The goal plan remains the execution state machine. A new append-only Team Run Graph
becomes the replay, UI, audit, and evaluation truth. A shared Memory Fabric unifies
logical namespaces over canonical knowledge; it does not create a separate copy of
company history for every specialist.

## Canonical Team Run Graph

### WorkRun

- `root_run_id`, `goal_id`, `tenant_id`
- `accountable_agent_id` (Scout for this product surface)
- exact request, source-selection, route, process, and authority references
- current state and aggregate version
- creation, terminal, and cancellation times

### AgentAssignment

- `assignment_id`, `root_run_id`, `parent_run_id`, `child_run_id`, `stage_id`
- `agent_id`, stable `role_key`, and `identity_kind`
- profile and capability revisions
- input and output contracts
- context lease, tool grant, authority grant, memory grant, and budget
- maximum delegation depth and terminal state

Identity kinds are explicit:

- `built_in`: Scout, Researcher, Designer, Critic
- `workforce`: an approved durable coworker profile
- `ephemeral_review_seat`: an actual independent panel call, described as a perspective
- `deterministic_worker`: renderer/compiler, never presented as a teammate

Nested delegation begins disabled. The first implementation supports Scout plus
one child level. A later depth increase requires bounded fan-out, authority
intersection, cycle rejection, budget reservation, and replay tests.

### AgentRunEvent

Every durable event includes:

- schema version, tenant, event ID, idempotency key, aggregate version
- root, parent, run, assignment, and stage IDs
- event type and status
- actor agent/role/identity plus profile and capability revisions
- visibility class, audience, ACL version, and purge generation
- structured payload reference, source references, artifact reference
- input-context digest and retention resource key
- optional superseded event ID
- occurred/recorded timestamps and event digest

Supported customer-relevant types are:

- `run.created`
- `agent.assigned`, `agent.started`, `agent.update`
- `agent.question`, `agent.evidence_added`
- `agent.handoff`, `agent.disagreement`, `agent.deliverable`
- `agent.failed`, `agent.cancelled`, `agent.completed`
- `run.checkpoint`, `run.completed`

Event IDs are deterministic over root run, assignment, stage, attempt, type, and
sequence. An idempotent duplicate is accepted only when its digest matches. A
conflicting body for the same identity fails closed.

Visible payloads are bounded, structured operational summaries. For example:

> Researcher -> Scout: Three defensible market proof points found; one unsupported market-size estimate excluded.

That sentence is emitted only from a persisted handoff event with the referenced
evidence and exclusion record. It is not a generated simulation of private thought.

## Memory Fabric

Memory namespaces are logical authority boundaries over shared canonical records.

| Namespace | Contents | Write policy |
|---|---|---|
| `tenant/{t}/run/{r}/agent/{a}` | scratch, checkpoints, drafts, tool-result refs | active assignee writes within its lease; Scout reads; terminal TTL |
| `tenant/{t}/agent/{a}/learning` | role procedures and source-linked preferences | agent proposes; policy or human review activates |
| `tenant/{t}/company/{domain}` | facts, decisions, positions, dossiers, canonical artifacts | agents read; canonical ingestion or ratification promotes |
| `tenant/{t}/person/{p}/agent/{a}` | consented relationship preferences | subject-authorized, private, correctable, forgettable |
| project/channel views | authorized projections | derived only; never independent authority |

AgentMind, reviewed product-agent learnings, workforce learning records, and Brain
references converge behind this revision contract rather than becoming a fourth
memory store.

### MemoryCandidate

All model-originated durable writes begin as a candidate containing:

- candidate ID, namespace, memory type, and subject key
- proposed summary and exact source references
- proposing agent, assignment, and run
- confidence, sensitivity, audience, observed time, expiry
- input-context digest

Promotion creates an append-only MemoryRevision in `pending`, `active`,
`corrected`, `superseded`, `forgotten`, or `purged` state. Company facts, personal
claims, and private-to-company promotion require deterministic validation and,
by default, human ratification. Critic and checker roles receive read-only durable
memory grants.

An approved Designer learning may say the company prefers evidence-led cover
slides. A worker may not promote a company strategy from one private conversation.

## Context leases, retrieval, and compaction

Every assignment receives a signed/bound context lease with:

- exact source refs and the current task/output contract
- audience, ACL version, purge generation, and expiry
- transcript, analysis, Brain, and artifact highwaters
- freshness and coverage gaps
- tool and memory grants
- one context digest

Provider context is progressively disclosed in this order:

1. current brief, constraints, and output contract;
2. latest verified run checkpoint;
3. top ask-conditioned authorized company references;
4. approved role learning;
5. consented relationship preferences when the surface permits them.

Retrieval filters authorization before ranking, then combines canonical lookups,
exact/lexical search, semantic search, temporal relevance, and ask-conditioned
lanes. Full company context means strong authorized retrieval coverage, not a dump
of years of transcripts into a prompt.

Compaction occurs at stable milestones or bounded context thresholds. The atomic
checkpoint preserves the objective, phase, completed actions, decisions,
constraints, source/artifact/run/message IDs, blockers, next action, highwaters,
ACL/purge versions, and pending memory candidates. Active context is replaced only
after the checkpoint append and checksum verification. Raw sources remain
canonical. Resume reauthorizes every reference and reports revoked or stale
material as a coverage gap.

## Supersession, purge, and years-long durability

Memory identity is `(tenant, namespace, memory_type, subject_key)`. New knowledge
adds a revision that explicitly supersedes the prior revision. Silence never
deletes or weakens an existing fact.

Each retrievable revision binds source revision/digest, audience, ACL version,
purge generation, validity interval, observed/verified/expiry times, coverage
highwaters, and current/superseded state. Corrections append; they do not rewrite
history.

All bodies, blobs, excerpts, embeddings, indexes, caches, exports, and backups
register with the existing retention machinery. Forget/purge removes every
derivative, leaves only a non-content tombstone, and prevents restore from
resurrecting the material.

JSONL and in-memory indexes may remain compatibility projections during rollout,
but they are not the years-long target. Durable authority moves to PostgreSQL
events/revisions plus an outbox; source bodies and artifacts move to object storage;
full-text/vector indexes remain rebuildable; encrypted offsite snapshots and restore
drills prove the selected RPO/RTO.

## Customer projection

### Main channel

- one persistent root work pill/card;
- a decision card only when a real material judgment is required;
- one final deliverable;
- no routine stage receipts or child-agent cards.

### Desktop activity sidecar

- header: Scout state, customer phase, truthful progress, elapsed time, cancel;
- team strip: only real assignments, stable role, current state, and brief;
- event timeline grouped by the existing five customer phases;
- meaningful evidence, question, handoff, disagreement, deliverable, retry, and failure events;
- linked sources/artifacts;
- technical details drawer for process stages, attempts, provider receipts, and diagnostics.

### Mobile activity sheet

- one compact persistent pill opens a slide-up sheet;
- horizontal real-team strip and the same grouped event projection;
- no raw JSON, code, or repeated work cards;
- preview/present/download remain native; unsupported editing is explicitly desktop-only.

Web and mobile consume the same projection and cannot independently invent status.

## Failure truth

- Assigned but never invoked is `failed_to_start`; no fake progress event.
- Partial panels report exact returned/required seats and continue only under quorum policy.
- Missing handoff output leaves the child `needs_attention`.
- ACL or purge changes revoke the context lease, record `context_changed`, and restart or ask when material.
- Failed compaction retains the last verified checkpoint.
- Projection delay shows `Syncing activity`; goal state cannot regress.
- Every model-written operational summary passes schema and source-reference validation.

## Release gates

1. Every visible agent and exchange maps to an actual assignment/event.
2. Private/shared and cross-tenant authority isolation.
3. Memory-poisoning resistance for messages, documents, files, and tool output.
4. Ten compaction cycles preserve all identifiers, constraints, decisions, blockers, and next actions.
5. Current/as-of supersession and silence preservation.
6. Complete purge through embeddings, caches, exports, and backups.
7. Ask-conditioned retrieval beats recency-tail retrieval on a fixed corpus.
8. Failure, retry, quorum, and handoff truth.
9. Rendered desktop/mobile accessibility and layout QA.
10. Replay against a synthetic one-year company corpus.

Operational measures include plan-to-first-agent latency, fan-out/depth, child
success/retry/quorum/handoff latency, event conflicts, context rejections, retrieval
coverage, memory proposal/correction/forget rates, compaction invariant retention,
projection lag, and cost by role/run.

## Staged rollout

### Current release: truthful projection only

- retain one root card and five customer phases;
- keep routine stages internal;
- show a coworker only for an actual bounded delegation;
- show current panels as `N perspectives`, not durable teammates;
- derive bounded assignment/start/handoff/complete/failure summaries from current durable goal state;
- keep current human-reviewed AgentMind/learning writes unchanged;
- place any team projection behind a flag or shadow replay.

### Wave A: Team Run Graph

- canonical AgentAssignment and AgentRunEvent ledger/outbox;
- stable built-in specialist identities and real child-run lifecycle;
- shared web sidecar/mobile sheet projection;
- idempotent crash, replay, retry, cancellation, and authority-change tests.

### Wave B: Memory Fabric

- unified MemoryCandidate/MemoryRevision contract;
- durable compaction checkpoints;
- ask-conditioned hybrid retrieval and explicit remember/forget surfaces;
- complete provenance, ACL, staleness, correction, and retention integration.

### Wave C: durable scale

- PostgreSQL/object-storage authority and rebuildable search indexes;
- encrypted offsite snapshots, restore drills, and tenant-isolation hardening.

Migration pressure is reviewed when p95 memory retrieval exceeds 300 ms,
projection latency exceeds 100 ms, startup replay exceeds 30 seconds, index lag
exceeds five minutes, event volume reaches millions, compaction invariant retention
falls below 99.9%, or the selected offsite RPO/RTO cannot be met.

## Reference patterns

- xAI confirms a parallel multi-agent mode, but does not establish a durable specialist-memory contract: https://docs.x.ai/developers/model-capabilities/text/multi-agent
- OpenAI distinguishes conversational sessions from progressively disclosed long-term memory and supports structured handoffs/tracing: https://openai.github.io/openai-agents-python/sandbox/memory/
- Anthropic documents client-owned persistent memory, scoped stores, context editing, and memory-poisoning concerns: https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool
- Cloudflare documents explicit remember/recall/forget, isolated profiles, supersession, idempotency, and asynchronous indexing: https://blog.cloudflare.com/introducing-agent-memory/
- LangGraph separates thread state from namespaced long-term memory: https://docs.langchain.com/oss/python/concepts/memory

These are implementation patterns, not sources of truth. STRIDE's ACL, provenance,
retention, company-memory, and private-context contracts remain authoritative.
