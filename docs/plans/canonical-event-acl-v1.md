# Canonical Event and ACL v1

Status: implementation candidate under independent critic review for the active bonfireOS 2.0 goal. This becomes the W1 source of truth only after the gate passes; the execution ledger remains `docs/plans/bonfireos-2.0-execution.md`.

## Outcome and evidence

W1 creates one durable company-brain substrate without putting the current room, board, or memory behind an unproven database.

Complete means:

- every authoritative legacy object can be imported into deterministic canonical events and replayed into matching projections;
- runtime mutations are captured through an append-only local shadow spool and delivered idempotently to PostgreSQL;
- one deny-by-default authorization seam governs object reads, content reads, writes, sharing, approvals, execution, search, recall, websocket fan-out, archives, and blobs;
- approvals bind an immutable content revision and exact action plan;
- policy-versioned consent and retention records exist, including the ratified 72-hour raw-audio TTL;
- a live shadow soak produces zero unexplained count, revision, ACL, or checksum divergence;
- JSON remains the production reader and rollback source until a later per-family cutover gate.

W1 does not switch media backends, make PostgreSQL the production reader, add Valkey, migrate password/passkey material into company events, or build more than `insights_opportunities_v1`.

## Decisions

1. **PostgreSQL 17 plus `pgx/v5` is the transactional authority target.** JSON/JSONL remains authoritative during W1 shadowing. PostgreSQL owns canonical events, projections, ACLs, approvals, consent, retention, jobs, and the transactional outbox after cutover.
2. **No Valkey in W1.** Leased jobs use PostgreSQL `FOR UPDATE SKIP LOCKED`; an in-memory cache is never a source of truth. Add managed Valkey only after measured load justifies it.
3. **Large or erasable bodies do not live in immutable events.** Events carry normalized state deltas, classifications, hashes, and content references. Revisions/blobs hold bodies so retention and deletion remain possible.
4. **No global event hash chain.** Per-event SHA-256, aggregate-version uniqueness, deterministic replay order, and projection checksums provide integrity without serializing unrelated writers.
5. **Shadow capture is JSON-first and fenced.** Capture is enabled under bounded legacy mutation locks before the import snapshot. Every mutation durably appends its fact or a recoverable preimage/tombstone to a `0600` framed local spool before prior state can disappear. A reconciler provides defense-in-depth. PostgreSQL downtime never blocks production in shadow mode.
6. **Authorization defaults to deny.** Ownership creates explicit grants; it is not an implicit bypass. Admins can manage lifecycle and ACLs but cannot silently read private content. Break-glass access is a separate expiring, reasoned, audited grant.
7. **Current visibility is preserved only as an explicit migration default.** Private Scout threads stay owner-only; public channels and current team-wide artifacts stay organization-visible; guests stay bound to one room and gain no durable-memory or tool authority. New objects use deny-by-default templates.
8. **Approvals are revision-bound and exactly-once.** Content revision, content digest, action-plan digest, policy version, target, recipient, destination, command, and ACL are part of the approval. Any change invalidates it.
9. **Production infrastructure baseline is managed PostgreSQL HA plus private Spaces.** Recommended cost is approximately $53/month: two-node managed PostgreSQL ($48) plus Spaces ($5). Provisioning is blocked on explicit recurring-cost approval. The current 2 GB, 81%-disk VPS must not host production PostgreSQL or be called HA.

## State ownership

| Domain | W1 authority | Target authority | Notes |
|---|---|---|---|
| Meeting memory, artifacts, chat, board, rooms, meetings | Existing JSON/JSONL | PostgreSQL events + projections | W1 shadow only; JSON mirror retained for rollback |
| Passwords, passkeys, member sessions | Existing isolated files | Separate auth tables in a later cutover | Never canonical event payloads |
| Guest sessions and room capabilities | Existing files | Expiring principal/capability records | Only token hashes persist |
| Blob bytes | Local content-addressed files | `BlobStore` local or private Spaces | Metadata/hash/ACL stay transactional in PostgreSQL |
| Embeddings | Rebuildable JSONL sidecar | Rebuildable projection | Never authoritative |
| Usage/eval telemetry | Isolated daily JSONL | Observability store | Workflow terminal facts also emit canonical events |
| Codex/render jobs | Current file queues | PostgreSQL leased jobs | Cut over only after two-runner race and reclaim tests |
| Backups | Local file ring | PG backup/PITR + encrypted Spaces inventory | Restore proof is required; same-region HA is not DR |

## Canonical contracts

### Event envelope

```go
type CanonicalEvent struct {
    EventID         uuid.UUID
    TenantID        string
    AggregateType   string
    AggregateID     string
    AggregateVersion int64
    EventType       string
    SchemaVersion   int
    OccurredAt      time.Time
    RecordedAt      time.Time
    Actor            PrincipalRef
    RoomID          string
    MeetingID       string
    CorrelationID   string
    CausationID     *uuid.UUID
    IdempotencyKey  string
    Classification  string
    ConsentSnapshotID *uuid.UUID
    ACLVersion      int64
    Payload         json.RawMessage
    ContentRef      string
    PayloadSHA256   [32]byte
    RetainUntil     *time.Time
}
```

Live events use UUIDv7. Imports use a deterministic UUIDv5 over `{source system, source object id, event type, source revision}`. `PayloadSHA256` is computed from canonical JSON encoding. Blank legacy `roomId` always normalizes to `office`.

Initial event families:

- `memory.entry.created|updated|deleted|superseded`
- `board.card.created|updated|deleted|restored`
- `meeting.started|participant_added|listen_only_latched|ended|archived`
- `room.created|updated|archived|restored`
- `artifact.created|revised|published|asset_attached`
- `chat.thread_created|message_added|message_deleted`
- `package.created|updated|reference_attached|reference_detached`
- `notification.created|read|cleared|delivered`
- `workflow.proposed|approved|launched|completed|failed`
- `job.enqueued|claimed|approval_required|completed|failed`
- `acl.granted|revoked`, `consent.granted|denied|withdrawn`, `approval.*`, `retention.*`

Imports are specified by a versioned normalization registry, not by ad hoc decoder behavior. Each legacy family records: source path and record key, tenant and aggregate identity, lifecycle classification, source revision algorithm, canonical-field allowlist, timestamp fallback, stable sort tuple, UUIDv5 input, and tombstone rule. JSON is encoded with RFC 8785 JCS before hashing. Golden vectors cover every family and include blank `roomId`, absent legacy member kind, in-place digest replacement, artifact revision journals, duplicate global memory IDs, hard deletes, capped-record eviction, and queue status transitions.

Identity and revision never depend on unrelated record order. A stable legacy object key comes from the family's intrinsic ID fields; its normalized state digest identifies a state. A checksummed, fsynced per-object version map persists `{family, object_key, state_digest -> aggregate_version}` and allocates the next version under the same family mutation lock. Initial import assigns version 1 to the current state unless an embedded revision journal provides its ordered history; later unseen state digests receive the next version, while known digests reuse the recorded version and event ID. The UUIDv5 input is `{normalization_registry_version, family, object_key, lifecycle_event, state_digest}`. Exact duplicate records with the same key and digest collapse according to existing global-memory-ID behavior. Same-key conflicting duplicates without a family-defined lifecycle order fail the import instead of using array position as identity. A golden test inserts, removes, and reorders an unrelated earlier-sorting record and proves unchanged event IDs and aggregate versions remain byte-identical.

Canonical event payloads use a closed schema registry keyed by `{event_type, schema_version}`. Unknown fields or an unregistered schema fail closed. Event payloads may contain only identifiers, enums, booleans, timestamps, numeric counters, digests, classifications, revision numbers, and content references. Transcript text, chat/file/artifact bodies, filenames, prompts, email/recipient data, provider tokens, credentials, capability tokens, raw provider responses, and user-supplied excerpts are prohibited. Tests walk serialized events and reject fields outside the allowlist.

### PostgreSQL schema

`migrations/0001_canonical.sql` creates:

- `canonical_events`: append-only sequence, unique event ID, unique aggregate version, optional tenant-scoped idempotency key, hashes/classification/content reference/retention fields;
- `objects`: current state and content revision, visibility, owner, room/meeting, lifecycle, ACL version, last event sequence;
- `object_revisions`: immutable revision headers and digests only; a nullable `body_id` references separately erasable revision content;
- `revision_bodies`: encrypted or blob-referenced erasable content, with per-body data-encryption-key reference, purge state, and destruction evidence;
- `principals` and `org_memberships`: user, guest, and service identities plus owner/admin/member roles;
- `object_grants`: explicit subject/action grants with conditions, expiry, and revocation;
- `approvals` and `approval_endorsements`: exact revision/action bindings and monotonic status;
- `consent_records` and `retention_state`;
- `outbox`: leased delivery rows inserted in the same transaction as events/projections;
- `jobs`: durable leased work with authority, attempts, idempotency, and terminal immutability;
- `blobs`: content hash, backend key, size/type, verification, lifecycle;
- `projection_checkpoints`: replay high-water sequence and checksum.

Migration SQL is embedded and applied under a PostgreSQL advisory lock. Every migration records version and SHA-256. Down SQL is supplied only where it cannot destroy accepted canonical data; otherwise rollback is an application reader/mirror flip.

### Authorization kernel

```go
Authorize(ctx, principal, action, objectRef, revisionRef) Decision
```

Actions are `read_metadata`, `read_content`, `create_child`, `write`, `delete`, `share`, `approve`, `execute`, `export`, and `manage_acl`.

The decision carries allow/deny, a public-safe denial code, policy reason, matched grant, ACL version, and obligations such as `redact`, `consent_required`, or `audit`. Missing principal, object, tenant match, grant, capability, or consent denies. Object denials return HTTP 404 to prevent enumeration.

All list/search/recall queries filter authorized object IDs before bodies are fetched. Blob access resolves `hash -> owning object/revision` and authorizes the object; knowledge of a hash is never authority. Canonical event reads inherit the subject object's current access rules and preserve the event-time ACL version for audit.

Migration grant templates:

- legacy private thread: owner only;
- public channel: organization read/write;
- legacy room: organization read/join; creator plus owner/admin manage for new rooms;
- legacy artifact/direct file: organization read, creator plus owner/admin write;
- meeting/transcript/brain: attach to room, preserve organization read initially, deny guest durable access;
- targeted notification: recipient only; broadcast: organization;
- guest: live room-participant grant for one room/sitting only;
- existing approvals: `legacy_unbound`, unable to authorize a new share or external write.

### Consent and retention

Consent records bind principal, sitting, policy version, evidence, and scopes: `audio_capture`, `transcription`, `model_analysis`, and `org_memory`.

- capture is off for a track until required consent exists;
- a declined participant may join listen-only/chat, but the server rejects their mic track from mixers, transcription, and models;
- late-join audio is dropped before consent;
- withdrawal immediately excludes the track and emits an event;
- raw committed-segment audio has the ratified 72-hour TTL and guest-visible disclosure;
- transcript/artifact/file content remains indefinite until a legal/business owner sets a destructive policy;
- soft delete immediately denies read and recall; purge removes bodies/blobs and derived recall while keeping a minimal non-sensitive tombstone;
- legal hold blocks purge, never access revocation.

Revision headers are immutable, but revision bodies are not. A purge transaction nulls the header's `body_id`, marks the body destroyed, deletes local/Spaces replicas and generated renders/exports, and destroys the per-body encryption key where immediate physical deletion cannot be proven. It enqueues invalidation for embeddings, excerpts, digests, search indexes, caches, model-context projections, and backup manifests. Restore tooling applies the durable purge ledger before a restored service becomes readable, so old backups cannot resurrect purged content. The surviving tombstone is limited to tenant/object/revision IDs, non-user-supplied event type, timestamps, actor pseudonymous ID, content digest, policy ID, purge status, and destruction evidence; it contains no title, filename, prompt, recipient, excerpt, or body.

Exact user-facing policy copy remains a named business/legal blocker, not an engineering assumption.

### Revision-bound approval

Content/body/assets changes increment `content_revision` and digest. Operational/read metadata increments `state_revision` only.

An approval binds:

```text
object + action + content_revision + content_sha256 + action_input_sha256 + policy_version
```

Execution locks the approval row, recomputes every binding, verifies current active approvers and expiry, marks it consumed, and inserts the job/outbox row in one transaction. One owner/admin or two distinct active members satisfy the existing heavy gate. Guests, services, and duplicate identities never count.

Public share capabilities bind one approved revision and store only a token hash. An edit never changes the bytes served by an old link; policy may keep that approved revision live or revoke it, but may never serve the edit silently.

The database transaction provides exactly-once authorization and dispatch, not magical exactly-once delivery to every external system. Every execution receives a stable `execution_key = SHA256(approval_id || action_input_sha256)` which is persisted before dispatch and passed as the provider idempotency key whenever supported. A worker that loses its lease after send must reconcile by that key or a provider receipt before retrying. Providers without idempotency lookup use a provider-specific `prepared -> sending -> confirmed|failed|ambiguous` receipt state machine; `ambiguous` fails closed and requires reconciliation or a fresh approval. No automatic resend is allowed. Tests cover crash after remote acceptance/before local commit, concurrent lease reclaim, receipt lookup, and an ambiguous provider.

## Authorization and migration coverage matrix

The implementation keeps a checked-in machine-readable registry whose rows are the release inventory. Adding a persisted kind, route, query, fan-out, capability, export, or worker without a row fails CI. At minimum it covers:

| Object family | Legacy authority | Required surfaces | Minimum action |
|---|---|---|---|
| meetings, transcripts, memory, digests, decisions, signals | `meeting-memory.jsonl`, `meetings.json` | meeting/memory APIs, recap/search/recall, embeddings/digest workers, archive/export, websocket memory/snapshot | `read_metadata`, `read_content`, `write`, `export` |
| artifacts, revisions, assets, files, folders | memory JSONL, blobs, folder JSON | artifact/file/blob/render routes, render tokens, share links, search/recall, websocket artifact snapshots, renderer | `read_content`, `write`, `share`, `export` |
| private Scout threads, public channels, room chat | memory JSONL | chat/thread APIs, room bootstrap, websocket replay/fan-out, Scout context, search | `read_content`, `create_child`, `write` |
| packages, proposals, workflows, goals, approvals | memory JSONL, job queues | package/proposal/workflow APIs, agent context, approval evidence, callbacks, websocket updates | `read_content`, `approve`, `execute` |
| rooms, memberships, guest links/sessions | rooms/users/sessions JSON | join/bootstrap/participants, room websocket, guest capability open/revoke, media/Scout context | `read_metadata`, `create_child`, `manage_acl` |
| board/cards | board JSON | board APIs, room board snapshot/fan-out, workflow mutations, recap context | `read_content`, `write` |
| notifications | notifications JSON | notification APIs, per-user websocket fan-out, delivery workers | `read_content`, `write` |
| deal rooms and grants | memory JSONL | deal-room APIs, gallery/PDF/export and capability routes | `read_content`, `share`, `export` |
| archives | archives JSON plus blobs | archive key/open/download/email routes | `read_content`, `share`, `export` |
| blobs | local CAS plus metadata | blob/file/artifact/deal-room/archive reads, thumbnails/renders | inherited owning-object action |
| entity ledger | memory JSONL | ledger APIs, search/recall, agent context | `read_content`, `write` |
| jobs and outputs | Codex/render queues | enqueue/claim/callback/output routes, artifact binding | `execute`, `read_content` |

All current artifact render tokens, artifact shares, archive keys, deal-room gallery/PDF links, guest links, and future public capabilities store only a token hash, bind tenant/object/revision/action, expire and revoke, then resolve through `Authorize`. The negative corpus enumerates every HTTP route, websocket bootstrap/replay/fan-out message, list/search/recall query, export, blob/render path, and worker-context builder in the registry. It asserts no unauthorized ID, metadata, body, event, count, timing distinction, or derived excerpt escapes.

The first enforcement slice closes the proven current leaks before broad migration work: named-room chat/delete must stop using global signed-in websocket fan-out; meeting records must stop broadcasting across rooms; memory HTTP/websocket/search must accept a principal and filter candidates before lexical/semantic fusion; artifact GET/PATCH/render-token and known-hash blob reads must authorize the exact parent revision; room/package/share-link administration must stop treating every member as an administrator; and any artifact edit must mint a revision that makes prior approval ineligible. The registry cites the exact handler and fan-out function for each seam. Parity compares per-principal visible IDs, counts, and hashes—not only global object counts—so a missing ACL cannot appear complete.

## Shadow capture, replay, and rollback

Environment contract:

```text
BONFIRE_CANONICAL_MODE=off|shadow|required
BONFIRE_CANONICAL_READS=json|postgres
DATABASE_URL=...
BONFIRE_BLOB_BACKEND=local|s3
```

`shadow` is the only W1 production mode. `required` and PostgreSQL reads are rejected unless a persisted parity gate for that object family is current.

1. **Bootstrap capture fence:** under the existing bounded per-store mutation locks, create and fsync a common migration epoch plus a generation/high-water record for every authority, enable spool capture, and only then release writers. Spool records are length-delimited, checksummed frames with sequence numbers; startup truncates only an incomplete final frame and rejects any interior corruption.
2. **Import:** snapshot each fenced generation after capture is active; import deterministic events through its recorded high-water; record normalized counts and checksums. Spool facts after each source high-water are replayed in sequence, so writes and deletes on either side of the boundary have one ordering.
3. **Capture:** every legacy mutation uses a recoverable two-phase shadow protocol while holding its family mutation lock:
   1. append and fsync a `prepared` frame containing mutation ID, family/object, before-state digest, after-state digest, previous family-chain digest, normalized fact, and any destructive preimage/tombstone;
   2. persist the legacy mutation through a hardened durability contract: whole-file stores write a same-directory temp file, fsync the temp, rename, then fsync the parent directory; append-only stores sync appended data before commit; multi-file mutations either publish one fsynced commit record after every member is durable or remain under one recoverable journal transaction;
   3. append and fsync a `committed` marker for the mutation ID and new family-chain digest;
   4. only committed facts are deliverable.

   Startup resolves orphan prepares in sequence against committed marker chains and the fenced legacy source: an exact after-state or a later prepare whose before-chain proves this after-state synthesizes the missing commit; an exact before-state records `aborted`; neither provable state freezes that family in degraded read-only mode for explicit reconciliation. It never guesses. PostgreSQL downtime does not block legacy commits in `shadow`, but local prepare/commit durability does; inability to fsync leaves the legacy mutation unattempted or the family frozen, never silently uncaptured. Failpoints cover before legacy write, after write before file sync, after rename before directory sync, after directory/data sync before commit marker, and after commit marker before delivery. Restart tests inspect actual filesystem state rather than only mocks.
4. **Repair:** periodic reconciliation scans normalized legacy objects, tombstones, capped-store eviction journals, and queue transition journals against canonical projections and appends missing deterministic repair events. Reconciliation is defense in depth, not the only record of destructive state.
5. **Parity:** replay from an empty database and compare object counts, tombstones, latest revisions, access decisions, queue transitions, blob references, and checksums. Repeated zero-diff live soaks are required.
6. **Future cutover:** PostgreSQL transaction becomes authoritative and emits JSON compatibility writes through its outbox. Readers flip one family at a time.
7. **Rollback:** enter a bounded maintenance fence that rejects new mutations and external dispatch, lets already-sent executions reconcile to terminal or `ambiguous`, expires/returns unclaimed leases, drains the JSON mirror through a signed canonical high-water, and proves per-family normalized checksums plus spool/outbox emptiness. Only then flip that family to JSON and reopen writers. An undrained mirror, an ambiguous side effect, or an unaccounted sequence blocks rollback.

## Implementation boundaries

Keep `package main` for W1 to avoid a repo-wide reorganization:

- `canonical_store.go`: interfaces, event/object/job/blob contracts;
- `canonical_postgres.go`: pgx pool, migrations, transactional append;
- `canonical_projection.go`: deterministic reducers and checksums;
- `canonical_import.go`: kind-aware legacy import and deterministic IDs;
- `canonical_capture.go`: local spool, delivery, checkpoints;
- `canonical_reconcile.go`: high-water and divergence report;
- `canonical_acl.go`: principals, grants, default-deny decisions, legacy resolver;
- `canonical_approval.go`: revision-bound requests/endorsements/consumption;
- `canonical_consent.go`: policy scopes and effective sitting consent;
- `canonical_retention.go`: expiry, purge, tombstones, derived invalidation;
- `canonical_jobs.go`: leased job queue;
- `canonical_blob_store.go`: local/S3 implementations plus authorized links;
- `migrations/*.sql`: embedded schema;
- `deploy/dev/docker-compose.canonical.yml`: disposable Postgres and MinIO only, explicitly non-HA.

Primary mutation seams are `memory.go`, `kanban.go`, `meetings.go`, `rooms.go`, `notifications.go`, `packages.go`, `codex_runner_queue.go`, `render_runner.go`, `blobs.go`, and `share_links.go`. Accounts, sessions, push credentials, and provider secrets migrate through separate auth/operational paths and emit only redacted audit facts.

## Delivery sequence and gates

### W1A - contracts and deterministic replay

- harden every covered legacy writer to fsync file data and parent-directory rename durability, with a commit record or journal transaction for multi-file mutations;
- migrations, interfaces, in-memory reducer, PostgreSQL implementation, local spool, importer, reconciler;
- checked-in normalization schema and authorization-surface registries, golden vectors, migration-epoch fencing, destructive tombstone/eviction journals, and framed-spool recovery;
- production-shaped fixtures for missing room IDs, legacy member sessions, memory cursors, archives, queues, blobs, and capped stores;
- gate: two imports and empty-database replay produce identical events and projection checksums; unrelated record insertion/removal/reorder preserves existing IDs and versions; concurrent create/update/delete/eviction/queue transitions on both sides of the snapshot boundary are neither lost, duplicated, nor invented.

### W1B - authorization and urgent trust fixes

- legacy object resolver plus one authorization kernel;
- enforce every row in the object/surface registry, including private threads, channels/chat, artifacts, files, blobs/renders, shares, archives, deal rooms, packages/proposals, notifications, search/recall, all websocket snapshots/fan-out, exports, and worker context;
- hash capability tokens and bind them to revisions;
- gate: the negative ACL corpus passes with indistinguishable 404s and no unauthorized IDs/bodies in lists or search.

### W1C - approval, consent, retention, and jobs

- revision-bound approvals; consent records and track exclusion; retention scheduler/tombstones; PostgreSQL job leases;
- gate: edit invalidates approval; action change invalidates approval; absent/withdrawn consent reaches no mixer/transcript/model; two runners cannot claim one job; crash-after-provider-acceptance reconciles without a duplicate effect and ambiguous completion fails closed.

### W1D - live shadow soak

- provision dedicated managed PostgreSQL and private Spaces after cost approval;
- encrypted connection, private VPC/trusted source, bounded pool/timeouts;
- import production snapshot, enable shadow capture, reconcile continuously;
- gate: repeated zero-diff replay/checksum and ACL-negative parity, bounded outbox lag, database failover evidence, object checksum verification, encrypted offsite snapshot, and real restore drill;
- rollback: disable shadow and continue from untouched JSON readers.

## Required evidence

- deterministic import twice and replay-from-empty checksums;
- same idempotency key returns the original result; concurrent aggregate versions cannot duplicate;
- failpoint after legacy write/before spool is repaired; failpoint after event/before projection rolls back the DB transaction;
- migration fence tests create/update/delete/evict and transition queues immediately before, during, and after snapshot high-water; failpoints exercise every prepare/legacy/commit/delivery boundary and prove no phantom or lost mutation; torn final spool frames recover and interior corruption fails closed;
- JSON mirror boots the current binary and preserves legacy member sessions;
- Bob cannot list/read/edit Alice's private thread, attachment, blob, artifact, event, or approval evidence;
- a learned blob hash grants nothing; a guest cannot access another room, board, memory, artifacts, tools, ACLs, or events;
- a room-A member cannot receive/search/recap room-B private content without a grant;
- edit or target/recipient/command change invalidates approval; racing endorsements consume once;
- late/declined/withdrawn audio never reaches mixer, transcript, digest, embedding, or model;
- raw audio expires after 72 hours; delete removes source and derived recall while leaving only a safe tombstone;
- event-schema tests prove sensitive bodies, titles, filenames, prompts, recipients, excerpts, and secrets cannot enter immutable payloads; restore applies the purge ledger before reads;
- job lease expiry/reclaim and two-runner claim race pass;
- crash-after-send, provider idempotency lookup, receipt reconciliation, concurrent reclaim, and ambiguous-effect tests pass;
- blob corruption fails closed;
- production shadow reports truthful health, lag, backlog, divergence, and restore evidence.

## Named blockers

- Explicit approval for approximately $53/month recurring managed PostgreSQL HA plus Spaces.
- Exact guest/member consent and retention copy approved by a business/legal owner.
- PostgreSQL HA is same-region availability, not cross-region disaster recovery; W4 still needs app/media redundancy and independent restore evidence.
