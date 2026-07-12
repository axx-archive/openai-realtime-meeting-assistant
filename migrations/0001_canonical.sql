BEGIN;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE canonical_events (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id uuid NOT NULL UNIQUE,
    tenant_id text NOT NULL,
    aggregate_type text NOT NULL,
    aggregate_id text NOT NULL,
    aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
    event_type text NOT NULL,
    schema_version integer NOT NULL CHECK (schema_version > 0),
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    room_id text,
    meeting_id text,
    correlation_id text,
    causation_id uuid,
    idempotency_key text,
    classification text NOT NULL,
    consent_snapshot_id uuid,
    acl_version bigint NOT NULL DEFAULT 0 CHECK (acl_version >= 0),
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    content_ref text,
    payload_sha256 bytea NOT NULL CHECK (octet_length(payload_sha256) = 32),
    retain_until timestamptz,
    UNIQUE (tenant_id, aggregate_type, aggregate_id, aggregate_version)
);

CREATE UNIQUE INDEX canonical_events_tenant_idempotency
    ON canonical_events (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX canonical_events_aggregate
    ON canonical_events (tenant_id, aggregate_type, aggregate_id, sequence);
CREATE INDEX canonical_events_room_meeting
    ON canonical_events (tenant_id, room_id, meeting_id, sequence);

CREATE TABLE revision_bodies (
    body_id uuid PRIMARY KEY,
    tenant_id text NOT NULL,
    inline_ciphertext bytea,
    blob_ref text,
    encryption_key_ref text,
    created_at timestamptz NOT NULL DEFAULT now(),
    retain_until timestamptz,
    purge_status text NOT NULL DEFAULT 'active'
        CHECK (purge_status IN ('active', 'scheduled', 'destroyed', 'failed')),
    destroyed_at timestamptz,
    destruction_evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
    CHECK (inline_ciphertext IS NULL OR blob_ref IS NULL),
    CHECK (purge_status <> 'destroyed' OR (inline_ciphertext IS NULL AND blob_ref IS NULL))
);

CREATE TABLE objects (
    tenant_id text NOT NULL,
    object_type text NOT NULL,
    object_id text NOT NULL,
    state_revision bigint NOT NULL DEFAULT 0 CHECK (state_revision >= 0),
    content_revision bigint NOT NULL DEFAULT 0 CHECK (content_revision >= 0),
    owner_principal_type text,
    owner_principal_id text,
    room_id text,
    meeting_id text,
    classification text NOT NULL,
    state jsonb NOT NULL DEFAULT '{}'::jsonb,
    content_sha256 bytea,
    acl_version bigint NOT NULL DEFAULT 0 CHECK (acl_version >= 0),
    last_event_sequence bigint NOT NULL REFERENCES canonical_events(sequence),
    deleted_at timestamptz,
    retain_until timestamptz,
    legal_hold boolean NOT NULL DEFAULT false,
    PRIMARY KEY (tenant_id, object_type, object_id),
    CHECK (content_sha256 IS NULL OR octet_length(content_sha256) = 32)
);

CREATE INDEX objects_room ON objects (tenant_id, room_id, object_type);
CREATE INDEX objects_meeting ON objects (tenant_id, meeting_id, object_type);

CREATE TABLE object_revisions (
    tenant_id text NOT NULL,
    object_type text NOT NULL,
    object_id text NOT NULL,
    content_revision bigint NOT NULL CHECK (content_revision > 0),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256) = 32),
    body_id uuid REFERENCES revision_bodies(body_id),
    created_by_type text NOT NULL,
    created_by_id text NOT NULL,
    created_at timestamptz NOT NULL,
    retain_until timestamptz,
    purged_at timestamptz,
    PRIMARY KEY (tenant_id, object_type, object_id, content_revision),
    FOREIGN KEY (tenant_id, object_type, object_id)
        REFERENCES objects(tenant_id, object_type, object_id)
);

CREATE TABLE principals (
    tenant_id text NOT NULL,
    principal_type text NOT NULL,
    principal_id text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    PRIMARY KEY (tenant_id, principal_type, principal_id)
);

CREATE TABLE org_memberships (
    tenant_id text NOT NULL,
    principal_type text NOT NULL,
    principal_id text NOT NULL,
    role text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    active boolean NOT NULL DEFAULT true,
    granted_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    PRIMARY KEY (tenant_id, principal_type, principal_id),
    FOREIGN KEY (tenant_id, principal_type, principal_id)
        REFERENCES principals(tenant_id, principal_type, principal_id)
);

CREATE TABLE object_grants (
    grant_id uuid PRIMARY KEY,
    tenant_id text NOT NULL,
    object_type text NOT NULL,
    object_id text NOT NULL,
    acl_version bigint NOT NULL CHECK (acl_version > 0),
    revision bigint,
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    action text NOT NULL,
    room_id text,
    sitting_id text,
    granted_by_type text NOT NULL,
    granted_by_id text NOT NULL,
    granted_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    revoked_at timestamptz,
    conditions jsonb NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (tenant_id, object_type, object_id, acl_version, revision, subject_type, subject_id, action)
);

CREATE INDEX object_grants_lookup
    ON object_grants (tenant_id, object_type, object_id, subject_type, subject_id, action)
    WHERE revoked_at IS NULL;

CREATE TABLE approvals (
    approval_id uuid PRIMARY KEY,
    tenant_id text NOT NULL,
    object_type text NOT NULL,
    object_id text NOT NULL,
    action text NOT NULL,
    content_revision bigint NOT NULL CHECK (content_revision > 0),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256) = 32),
    action_input_sha256 bytea NOT NULL CHECK (octet_length(action_input_sha256) = 32),
    policy_version text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'revoked', 'consumed', 'expired')),
    required_endorsements integer NOT NULL CHECK (required_endorsements > 0),
    requested_by_type text NOT NULL,
    requested_by_id text NOT NULL,
    requested_at timestamptz NOT NULL,
    expires_at timestamptz,
    consumed_at timestamptz,
    execution_key bytea NOT NULL UNIQUE CHECK (octet_length(execution_key) = 32),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0)
);

CREATE TABLE approval_endorsements (
    approval_id uuid NOT NULL REFERENCES approvals(approval_id) ON DELETE CASCADE,
    principal_type text NOT NULL,
    principal_id text NOT NULL,
    decision text NOT NULL CHECK (decision IN ('approve', 'reject', 'withdraw')),
    decided_at timestamptz NOT NULL,
    approval_revision bigint NOT NULL CHECK (approval_revision > 0),
    PRIMARY KEY (approval_id, principal_type, principal_id)
);

CREATE TABLE execution_receipts (
    execution_key bytea PRIMARY KEY CHECK (octet_length(execution_key) = 32),
    approval_id uuid NOT NULL UNIQUE REFERENCES approvals(approval_id),
    provider text NOT NULL,
    provider_receipt text,
    status text NOT NULL CHECK (status IN ('prepared', 'sending', 'confirmed', 'failed', 'ambiguous')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    updated_at timestamptz NOT NULL,
    last_error_code text
);

CREATE TABLE consent_records (
    consent_id uuid PRIMARY KEY,
    tenant_id text NOT NULL,
    principal_type text NOT NULL,
    principal_id text NOT NULL,
    room_id text NOT NULL,
    sitting_id text NOT NULL,
    policy_version text NOT NULL,
    scopes text[] NOT NULL,
    status text NOT NULL CHECK (status IN ('granted', 'denied', 'withdrawn')),
    evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
    effective_at timestamptz NOT NULL,
    expires_at timestamptz,
    withdrawn_at timestamptz
);

CREATE INDEX consent_records_effective
    ON consent_records (tenant_id, principal_type, principal_id, room_id, sitting_id, effective_at DESC);

CREATE TABLE retention_state (
    tenant_id text NOT NULL,
    object_type text NOT NULL,
    object_id text NOT NULL,
    policy_id text NOT NULL,
    retain_until timestamptz,
    legal_hold boolean NOT NULL DEFAULT false,
    purge_status text NOT NULL DEFAULT 'active'
        CHECK (purge_status IN ('active', 'scheduled', 'purging', 'purged', 'failed')),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, object_type, object_id)
);

CREATE TABLE outbox (
    outbox_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id uuid NOT NULL UNIQUE REFERENCES canonical_events(event_id),
    topic text NOT NULL,
    payload jsonb NOT NULL,
    available_at timestamptz NOT NULL DEFAULT now(),
    leased_until timestamptz,
    leased_by text,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    delivered_at timestamptz,
    last_error_code text
);

CREATE INDEX outbox_available ON outbox (available_at, outbox_id)
    WHERE delivered_at IS NULL;

CREATE TABLE jobs (
    job_id uuid PRIMARY KEY,
    tenant_id text NOT NULL,
    kind text NOT NULL,
    status text NOT NULL CHECK (status IN ('queued', 'claimed', 'approval_required', 'completed', 'failed', 'ambiguous', 'cancelled')),
    authority text NOT NULL,
    idempotency_key text NOT NULL,
    execution_key bytea,
    payload jsonb NOT NULL,
    result jsonb,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    leased_until timestamptz,
    runner_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    UNIQUE (tenant_id, idempotency_key),
    CHECK (execution_key IS NULL OR octet_length(execution_key) = 32)
);

CREATE INDEX jobs_claimable ON jobs (available_at, created_at)
    WHERE status = 'queued';

CREATE TABLE blobs (
    ref text PRIMARY KEY,
    sha256 bytea NOT NULL UNIQUE CHECK (octet_length(sha256) = 32),
    storage_backend text NOT NULL,
    storage_key text NOT NULL,
    mime_type text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    verified_at timestamptz,
    deleted_at timestamptz
);

CREATE TABLE projection_checkpoints (
    projection_name text PRIMARY KEY,
    through_sequence bigint NOT NULL DEFAULT 0 CHECK (through_sequence >= 0),
    checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE migration_epochs (
    epoch_id uuid PRIMARY KEY,
    mode text NOT NULL CHECK (mode IN ('shadow', 'required')),
    status text NOT NULL CHECK (status IN ('preparing', 'capturing', 'frozen', 'closed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    closed_at timestamptz,
    source_high_waters jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE legacy_object_versions (
    registry_version integer NOT NULL,
    family text NOT NULL,
    object_key text NOT NULL,
    state_sha256 bytea NOT NULL CHECK (octet_length(state_sha256) = 32),
    aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
    event_id uuid NOT NULL,
    PRIMARY KEY (registry_version, family, object_key, state_sha256),
    UNIQUE (registry_version, family, object_key, aggregate_version),
    UNIQUE (event_id)
);

COMMIT;
