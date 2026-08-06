BEGIN;

CREATE TABLE ambient_intelligence_replay_manifests (
    manifest_digest bytea PRIMARY KEY CHECK (octet_length(manifest_digest) = 32),
    schema_version text NOT NULL CHECK (schema_version = 'ambient-intelligence-replay/v1'),
	idempotency_key bytea NOT NULL CHECK (octet_length(idempotency_key) = 32),
    tenant_id text NOT NULL,
    room_id text NOT NULL,
    sitting_id text NOT NULL,
    start_after bigint NOT NULL CHECK (start_after >= 0),
    end_at bigint NOT NULL CHECK (end_at > start_after),
    source_count integer NOT NULL CHECK (source_count BETWEEN 1 AND 48),
    purge_generation bigint NOT NULL CHECK (purge_generation >= 0),
    authorized_by text NOT NULL,
    approval_reference text NOT NULL,
    generated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > generated_at),
    release_commit text NOT NULL,
    rollback_floor text NOT NULL,
    manifest jsonb NOT NULL,
    status text NOT NULL DEFAULT 'planned'
        CHECK (status IN ('planned', 'running', 'completed', 'failed', 'drifted', 'expired')),
    execution_id uuid,
	lease_expires_at timestamptz,
    started_at timestamptz,
    completed_at timestamptz,
    last_error_code text,
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (room_id = btrim(room_id) AND room_id <> ''),
    CHECK (sitting_id = btrim(sitting_id) AND sitting_id <> ''),
    CHECK (authorized_by = btrim(authorized_by) AND authorized_by <> ''),
    CHECK (approval_reference = btrim(approval_reference) AND approval_reference <> ''),
    CHECK (release_commit = btrim(release_commit) AND release_commit <> ''),
    CHECK (rollback_floor = btrim(rollback_floor) AND rollback_floor <> ''),
	CHECK ((status = 'planned' AND execution_id IS NULL AND lease_expires_at IS NULL AND started_at IS NULL AND completed_at IS NULL) OR
	       (status = 'running' AND execution_id IS NOT NULL AND lease_expires_at IS NOT NULL AND started_at IS NOT NULL AND completed_at IS NULL) OR
	       (status IN ('completed', 'failed', 'drifted') AND execution_id IS NOT NULL AND lease_expires_at IS NULL AND started_at IS NOT NULL AND completed_at IS NOT NULL) OR
	       (status = 'expired' AND execution_id IS NULL AND lease_expires_at IS NULL AND started_at IS NULL AND completed_at IS NOT NULL))
);

CREATE UNIQUE INDEX ambient_intelligence_replay_one_active_sitting
    ON ambient_intelligence_replay_manifests (tenant_id, room_id, sitting_id)
    WHERE status IN ('planned', 'running');

CREATE UNIQUE INDEX ambient_intelligence_replay_one_plan_key
	ON ambient_intelligence_replay_manifests (tenant_id, idempotency_key);

CREATE INDEX ambient_intelligence_replay_reclaimable
    ON ambient_intelligence_replay_manifests (expires_at, lease_expires_at)
    WHERE status IN ('planned', 'running');

CREATE TABLE ambient_intelligence_replay_sources (
    manifest_digest bytea NOT NULL REFERENCES ambient_intelligence_replay_manifests(manifest_digest) ON DELETE CASCADE,
    ordinal integer NOT NULL CHECK (ordinal >= 0 AND ordinal < 48),
    object_id text NOT NULL,
    capture_sequence bigint NOT NULL CHECK (capture_sequence > 0),
    content_revision bigint NOT NULL CHECK (content_revision > 0),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest) = 32),
    acl_version bigint NOT NULL CHECK (acl_version > 0),
    purge_generation bigint NOT NULL CHECK (purge_generation >= 0),
    occurred_start timestamptz NOT NULL,
    occurred_end timestamptz NOT NULL CHECK (occurred_end > occurred_start),
    consent_fence_digest bytea NOT NULL CHECK (octet_length(consent_fence_digest) = 32),
    PRIMARY KEY (manifest_digest, ordinal),
    UNIQUE (manifest_digest, object_id),
    UNIQUE (manifest_digest, capture_sequence)
);

CREATE TABLE ambient_intelligence_replay_cursors (
    manifest_digest bytea NOT NULL REFERENCES ambient_intelligence_replay_manifests(manifest_digest) ON DELETE CASCADE,
    stage text NOT NULL,
    cursor_namespace text NOT NULL,
    through_source_sequence bigint NOT NULL CHECK (through_source_sequence >= 0),
    input_digest bytea NOT NULL CHECK (octet_length(input_digest) = 32),
    output_digest bytea CHECK (output_digest IS NULL OR octet_length(output_digest) = 32),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (manifest_digest, stage),
    CHECK (stage <> 'board'),
    CHECK (cursor_namespace = 'replay:' || encode(manifest_digest, 'hex'))
);

CREATE TABLE ambient_intelligence_replay_stage_receipts (
    manifest_digest bytea NOT NULL REFERENCES ambient_intelligence_replay_manifests(manifest_digest) ON DELETE CASCADE,
    execution_id uuid NOT NULL,
    stage text NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    status text NOT NULL CHECK (status IN ('prepared', 'running', 'completed', 'failed', 'drifted')),
    input_artifact_digest bytea NOT NULL CHECK (octet_length(input_artifact_digest) = 32),
    output_artifact_digest bytea CHECK (output_artifact_digest IS NULL OR octet_length(output_artifact_digest) = 32),
    source_manifest_digest bytea NOT NULL CHECK (octet_length(source_manifest_digest) = 32),
    calls integer NOT NULL DEFAULT 0 CHECK (calls >= 0),
    input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cost_micros bigint NOT NULL DEFAULT 0 CHECK (cost_micros >= 0),
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    error_code text,
    PRIMARY KEY (manifest_digest, execution_id, stage),
    CHECK (stage <> 'board'),
    CHECK ((status IN ('prepared', 'running') AND completed_at IS NULL) OR
           (status IN ('completed', 'failed', 'drifted') AND completed_at IS NOT NULL))
);

CREATE INDEX ambient_intelligence_replay_receipts_execution
    ON ambient_intelligence_replay_stage_receipts (execution_id, ordinal);

COMMIT;
