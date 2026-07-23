BEGIN;

CREATE SEQUENCE brain_projection_work_request_generation_seq AS bigint;

CREATE TABLE brain_projection_work (
    tenant_id text NOT NULL,
    projector_version text NOT NULL,
    room_id text NOT NULL,
    sitting_id text NOT NULL,
    source_family text NOT NULL,
    first_requested_at timestamptz NOT NULL DEFAULT now(),
    requested_at timestamptz NOT NULL DEFAULT now(),
    available_at timestamptz NOT NULL DEFAULT now(),
    request_generation bigint NOT NULL DEFAULT nextval('brain_projection_work_request_generation_seq') CHECK (request_generation > 0),
    lease_token bytea CHECK (lease_token IS NULL OR octet_length(lease_token) = 32),
    lease_expires_at timestamptz,
    attempts bigint NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error text NOT NULL DEFAULT '',
    failure_since timestamptz,
    PRIMARY KEY (tenant_id, projector_version, room_id, sitting_id, source_family),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (projector_version = btrim(projector_version) AND projector_version <> ''),
    CHECK (room_id = btrim(room_id) AND room_id <> ''),
    CHECK (sitting_id = btrim(sitting_id) AND sitting_id <> ''),
    CHECK (source_family = btrim(source_family) AND source_family <> ''),
    CHECK (num_nonnulls(lease_token, lease_expires_at) IN (0, 2)),
    CHECK ((last_error = '' AND failure_since IS NULL) OR (last_error <> '' AND failure_since IS NOT NULL))
);

CREATE INDEX brain_projection_work_available
    ON brain_projection_work (available_at, requested_at);

-- Historical projection work is never inferred from canonical history. An
-- authenticated operator plane must submit one exact scope and bounded source
-- interval. This ledger makes that authority and the minted rebuild fence
-- durable across retries and process restarts.
CREATE TABLE brain_projection_backfill_requests (
    request_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    projector_version text NOT NULL,
    room_id text NOT NULL,
    sitting_id text NOT NULL,
    source_family text NOT NULL,
    expected_generation bigint NOT NULL CHECK (expected_generation >= 0),
    start_source_high_water bigint NOT NULL CHECK (start_source_high_water >= 0),
    end_source_high_water bigint NOT NULL CHECK (end_source_high_water > start_source_high_water),
    authorized_by text NOT NULL,
    approval_reference text NOT NULL UNIQUE,
    authorization_expires_at timestamptz NOT NULL,
    accepted_at timestamptz NOT NULL DEFAULT now(),
    fence_generation bigint CHECK (fence_generation > 0),
    fence_token bytea CHECK (fence_token IS NULL OR octet_length(fence_token) = 32),
    rebuild_started_at timestamptz,
    source_manifest jsonb,
    CHECK (request_id = btrim(request_id) AND request_id <> ''),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (projector_version = btrim(projector_version) AND projector_version <> ''),
    CHECK (room_id = btrim(room_id) AND room_id <> ''),
    CHECK (sitting_id = btrim(sitting_id) AND sitting_id <> ''),
    CHECK (source_family = btrim(source_family) AND source_family <> ''),
    CHECK (authorized_by = btrim(authorized_by) AND authorized_by <> ''),
    CHECK (approval_reference = btrim(approval_reference) AND approval_reference <> ''),
    CHECK (authorization_expires_at > accepted_at AND authorization_expires_at <= accepted_at + interval '24 hours'),
    CHECK (num_nonnulls(fence_generation, fence_token, rebuild_started_at, source_manifest) IN (0, 4))
);

COMMIT;
