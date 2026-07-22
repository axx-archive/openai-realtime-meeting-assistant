BEGIN;

CREATE TABLE brain_projection_checkpoints (
    tenant_id text NOT NULL,
    projector_version text NOT NULL,
    room_id text NOT NULL,
    sitting_id text NOT NULL,
    source_family text NOT NULL,
    source_high_water bigint CHECK (source_high_water >= 0),
    source_manifest_sha256 bytea CHECK (source_manifest_sha256 IS NULL OR octet_length(source_manifest_sha256) = 32),
    derived_high_water bigint CHECK (derived_high_water >= 0),
    derived_id text,
    derived_sha256 bytea CHECK (derived_sha256 IS NULL OR octet_length(derived_sha256) = 32),
    published_generation bigint CHECK (published_generation >= 0),
    published_rebuild_fence_token bytea CHECK (published_rebuild_fence_token IS NULL OR octet_length(published_rebuild_fence_token) = 32),
    rebuild_generation bigint NOT NULL DEFAULT 0 CHECK (rebuild_generation >= 0),
    rebuild_start_high_water bigint CHECK (rebuild_start_high_water >= 0),
    rebuild_end_high_water bigint CHECK (rebuild_end_high_water >= 0),
    rebuild_source_manifest_sha256 bytea CHECK (rebuild_source_manifest_sha256 IS NULL OR octet_length(rebuild_source_manifest_sha256) = 32),
    rebuild_fence_token bytea CHECK (rebuild_fence_token IS NULL OR octet_length(rebuild_fence_token) = 32),
    rebuild_started_at timestamptz,
    published_at timestamptz,
    PRIMARY KEY (tenant_id, projector_version, room_id, sitting_id, source_family),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (projector_version = btrim(projector_version) AND projector_version <> ''),
    CHECK (room_id = btrim(room_id) AND room_id <> ''),
    CHECK (sitting_id = btrim(sitting_id) AND sitting_id <> ''),
    CHECK (source_family = btrim(source_family) AND source_family <> ''),
    CHECK (derived_id IS NULL OR derived_id <> ''),
    CHECK (num_nonnulls(
        source_high_water, source_manifest_sha256, derived_high_water, derived_id,
        derived_sha256, published_generation, published_rebuild_fence_token, published_at
    ) IN (0, 8)),
    CHECK (num_nonnulls(
        rebuild_start_high_water, rebuild_end_high_water, rebuild_source_manifest_sha256,
        rebuild_fence_token, rebuild_started_at
    ) IN (0, 5)),
    CHECK (rebuild_end_high_water IS NULL OR rebuild_end_high_water >= rebuild_start_high_water),
    CHECK (published_generation IS NULL OR published_generation <= rebuild_generation),
    CHECK (rebuild_started_at IS NOT NULL OR published_generation IS NULL OR published_generation = rebuild_generation),
    CHECK (rebuild_started_at IS NULL OR published_generation IS NULL OR published_generation < rebuild_generation)
);

CREATE INDEX brain_projection_checkpoints_stale_rebuilds
    ON brain_projection_checkpoints (tenant_id, rebuild_started_at)
    WHERE rebuild_started_at IS NOT NULL;

COMMIT;
