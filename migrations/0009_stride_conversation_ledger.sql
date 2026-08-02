BEGIN;

-- Structured references are a closed, body-free value type rather than an
-- arbitrary JSON escape hatch. Match STRIDEReference's four-field contract at
-- canonical authority so nested bodies, credentials, and unknown fields are
-- rejected even when a future writer bypasses the Go validator.
CREATE FUNCTION stride_structured_refs_are_valid(refs jsonb)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT jsonb_typeof(refs) = 'array'
       AND NOT stride_jsonb_has_forbidden_key(refs, ARRAY['body', 'text', 'audio', 'secret', 'token', 'api_key', 'authorization', 'credential', 'credentials', 'password', 'cookie'])
       AND NOT EXISTS (
           SELECT 1
           FROM jsonb_array_elements(CASE WHEN jsonb_typeof(refs) = 'array' THEN refs ELSE '[]'::jsonb END) AS entry(value)
           WHERE jsonb_typeof(value) <> 'object'
              OR (SELECT count(*) FROM jsonb_object_keys(CASE WHEN jsonb_typeof(value) = 'object' THEN value ELSE '{}'::jsonb END)) <> 4
              OR NOT value ?& ARRAY['contractType', 'id', 'revision', 'digest']
              OR jsonb_typeof(value->'contractType') <> 'string'
              OR value->>'contractType' <> ALL(ARRAY[
                   'conversation_event', 'transcript_segment', 'transcript_revision', 'analysis_projection',
                   'knowledge_assertion', 'collaboration_preference', 'work_intent', 'work_proposal', 'work_run',
                   'outcome', 'meeting_answer', 'company_answer', 'agent_core_profile', 'agent_profile_overlay',
                   'agent_capability_manifest', 'channel_norm_profile', 'agent_relationship_memory',
                   'agent_context_envelope', 'delegation_run', 'rich_message_part', 'meeting_agent_invitation',
                   'meeting_specialist_context', 'meeting_agent_session', 'meeting_agent_contribution',
                   'agent_package_manifest', 'marketplace_listing', 'team_agent', 'agent_assignment',
                   'agent_learning_record', 'agent_performance_receipt', 'agent_update_proposal', 'workforce_policy'
              ])
              OR jsonb_typeof(value->'id') <> 'string'
              OR value->>'id' !~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$'
              OR jsonb_typeof(value->'revision') <> 'number'
              OR value->>'revision' !~ '^[1-9][0-9]*$'
              OR jsonb_typeof(value->'digest') <> 'string'
              OR value->>'digest' !~ '^[0-9A-Fa-f]{64}$'
       );
$$;

-- Default-off E1 canonical conversation input. No handler reads this table in
-- this migration; it is the durable counterpart to the deterministic local
-- reducer and deliberately stores only body-free event/audience provenance.
CREATE TABLE stride_conversation_events (
    tenant_id text NOT NULL,
    event_id text NOT NULL,
    event_revision bigint NOT NULL CHECK (event_revision > 0),
    sequence bigint NOT NULL CHECK (sequence > 0),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    idempotency_key text NOT NULL,
    source_type text NOT NULL,
    source_id text NOT NULL,
    room_id text,
    sitting_id text,
    thread_id text,
    author_principal text NOT NULL,
    author_name text NOT NULL,
    occurred_at timestamptz NOT NULL,
    ingested_at timestamptz NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('message', 'edit', 'delete', 'reply', 'reaction', 'file', 'link', 'transcript_turn', 'consent_change', 'agent_session_status', 'agent_contribution')),
    content_revision bigint NOT NULL CHECK (content_revision > 0),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest) = 32),
    supersedes_event_id text,
    reply_to_event_id text,
    audience_digest bytea NOT NULL CHECK (octet_length(audience_digest) = 32),
    visibility text NOT NULL CHECK (visibility IN ('private', 'project', 'channel', 'organization', 'meeting')),
    acl_version bigint NOT NULL CHECK (acl_version > 0),
    retention_policy text NOT NULL,
    purge_generation bigint NOT NULL DEFAULT 0 CHECK (purge_generation >= 0),
    provenance text NOT NULL CHECK (provenance IN ('client', 'server', 'tool', 'provider')),
    on_behalf_of text,
    provider_item_id_hash bytea CHECK (provider_item_id_hash IS NULL OR octet_length(provider_item_id_hash) = 32),
    body_ref text,
    structured_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
    private_share_source_type text,
    private_share_source_id text,
    private_share_source_revision bigint,
    private_share_authorization_type text,
    private_share_authorization_id text,
    private_share_authorization_revision bigint,
    recall_eligible boolean NOT NULL DEFAULT false CHECK (recall_eligible = false),
    invalidated_at timestamptz,
    invalidation_reason text,
    PRIMARY KEY (tenant_id, event_id),
    UNIQUE (tenant_id, idempotency_key),
    UNIQUE (tenant_id, sequence),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (event_id = btrim(event_id) AND event_id <> ''),
    CHECK (idempotency_key = btrim(idempotency_key) AND idempotency_key <> ''),
    CHECK (source_type = btrim(source_type) AND source_type <> ''),
    CHECK (source_id = btrim(source_id) AND source_id <> ''),
    CHECK (author_principal = btrim(author_principal) AND author_principal <> ''),
    CHECK (author_name = btrim(author_name) AND author_name <> ''),
    CHECK (retention_policy = btrim(retention_policy) AND retention_policy <> ''),
    CHECK (stride_structured_refs_are_valid(structured_refs)),
    CHECK ((private_share_source_type IS NULL AND private_share_source_id IS NULL AND private_share_source_revision IS NULL AND private_share_authorization_type IS NULL AND private_share_authorization_id IS NULL AND private_share_authorization_revision IS NULL) OR
           (source_type = 'private_share' AND visibility <> 'private' AND private_share_source_type IS NOT NULL AND private_share_source_id IS NOT NULL AND private_share_source_revision > 0 AND private_share_authorization_type IS NOT NULL AND private_share_authorization_id IS NOT NULL AND private_share_authorization_revision > 0)),
    CHECK ((invalidated_at IS NULL AND invalidation_reason IS NULL) OR (invalidated_at IS NOT NULL AND invalidation_reason = btrim(invalidation_reason) AND invalidation_reason <> ''))
);

CREATE INDEX stride_conversation_thread_sequence
    ON stride_conversation_events (tenant_id, thread_id, sequence);
CREATE INDEX stride_conversation_source_revision
    ON stride_conversation_events (tenant_id, source_type, source_id, content_revision);
CREATE INDEX stride_conversation_invalidation
    ON stride_conversation_events (tenant_id, invalidated_at, purge_generation);

-- Projection checkpoints and rebuild generations make replay/restart state
-- auditable without persisting a second copy of message bodies.
CREATE TABLE stride_conversation_projection_checkpoints (
    tenant_id text NOT NULL,
    projection_name text NOT NULL,
    generation bigint NOT NULL CHECK (generation >= 0),
    through_sequence bigint NOT NULL CHECK (through_sequence >= 0),
    checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
    source_manifest_digest bytea NOT NULL CHECK (octet_length(source_manifest_digest) = 32),
    rebuilt_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, projection_name, generation),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (projection_name = btrim(projection_name) AND projection_name <> '')
);

-- The fan-out index is separate from body storage. A retract/revoke/purge can
-- invalidate every knowledge/preference/learning/performance/assignment/answer
-- or work derivative without searching model output text.
CREATE TABLE stride_conversation_derived_edges (
    tenant_id text NOT NULL,
    source_event_id text NOT NULL,
    source_event_revision bigint NOT NULL CHECK (source_event_revision > 0),
    source_digest bytea NOT NULL CHECK (octet_length(source_digest) = 32),
    derived_contract_type text NOT NULL,
    derived_contract_id text NOT NULL,
    derived_revision bigint NOT NULL CHECK (derived_revision > 0),
    derived_digest bytea NOT NULL CHECK (octet_length(derived_digest) = 32),
    lane text NOT NULL CHECK (lane IN ('knowledge', 'preference', 'learning', 'performance', 'assignment', 'answer', 'work')),
    invalidated_at timestamptz,
    invalidation_reason text,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, source_event_id, source_event_revision, derived_contract_type, derived_contract_id, derived_revision, lane),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (source_event_id = btrim(source_event_id) AND source_event_id <> ''),
    CHECK (derived_contract_type = btrim(derived_contract_type) AND derived_contract_type <> ''),
    CHECK (derived_contract_id = btrim(derived_contract_id) AND derived_contract_id <> ''),
    CHECK ((invalidated_at IS NULL AND invalidation_reason IS NULL) OR (invalidated_at IS NOT NULL AND invalidation_reason = btrim(invalidation_reason) AND invalidation_reason <> ''))
);

CREATE INDEX stride_conversation_edge_source
    ON stride_conversation_derived_edges (tenant_id, source_event_id, source_event_revision, invalidated_at);
CREATE INDEX stride_conversation_edge_derived
    ON stride_conversation_derived_edges (tenant_id, derived_contract_type, derived_contract_id, derived_revision, invalidated_at);

COMMIT;
