BEGIN;

-- Immutable authority receipt for moving one validated replay result from the
-- shadow executor into the current Meeting Record. The derived body remains in
-- the legacy meeting-memory journal; PostgreSQL stores only exact digests and
-- authority/release identity, so canonical authority stays body-free.
CREATE TABLE ambient_intelligence_replay_promotions (
    manifest_digest bytea PRIMARY KEY REFERENCES ambient_intelligence_replay_manifests(manifest_digest) ON DELETE RESTRICT,
    execution_id uuid NOT NULL,
    tenant_id text NOT NULL,
    room_id text NOT NULL,
    sitting_id text NOT NULL,
    source_manifest_digest bytea NOT NULL CHECK (octet_length(source_manifest_digest) = 32),
    meeting_digest_stage_output_digest bytea NOT NULL CHECK (octet_length(meeting_digest_stage_output_digest) = 32),
    canonical_meeting_digest_body_digest bytea NOT NULL CHECK (octet_length(canonical_meeting_digest_body_digest) = 32),
    approval_reference text NOT NULL,
    rollback_floor text NOT NULL,
    release_commit text NOT NULL,
    recorded_at timestamptz NOT NULL,
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (room_id = btrim(room_id) AND room_id <> ''),
    CHECK (sitting_id = btrim(sitting_id) AND sitting_id <> ''),
    CHECK (approval_reference = btrim(approval_reference) AND approval_reference <> ''),
    CHECK (rollback_floor = btrim(rollback_floor) AND rollback_floor <> ''),
    CHECK (release_commit = btrim(release_commit) AND release_commit <> '')
);

CREATE FUNCTION ambient_intelligence_replay_promotion_exact()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    replay ambient_intelligence_replay_manifests%ROWTYPE;
    stage_output bytea;
BEGIN
    SELECT * INTO replay FROM ambient_intelligence_replay_manifests
      WHERE manifest_digest = NEW.manifest_digest FOR UPDATE;
    IF NOT FOUND OR replay.status <> 'running' OR replay.execution_id IS DISTINCT FROM NEW.execution_id OR
       replay.tenant_id IS DISTINCT FROM NEW.tenant_id OR replay.room_id IS DISTINCT FROM NEW.room_id OR
       replay.sitting_id IS DISTINCT FROM NEW.sitting_id OR replay.approval_reference IS DISTINCT FROM NEW.approval_reference OR
       replay.rollback_floor IS DISTINCT FROM NEW.rollback_floor OR replay.release_commit IS DISTINCT FROM NEW.release_commit OR
       decode(replay.manifest->>'sourceManifestDigest', 'hex') IS DISTINCT FROM NEW.source_manifest_digest THEN
        RAISE EXCEPTION 'ambient replay promotion requires exact running manifest authority';
    END IF;
    SELECT output_artifact_digest INTO stage_output
      FROM ambient_intelligence_replay_stage_receipts
      WHERE manifest_digest = NEW.manifest_digest AND execution_id = NEW.execution_id
        AND stage = 'meeting_digest' AND status = 'completed';
    IF stage_output IS NULL OR stage_output IS DISTINCT FROM NEW.meeting_digest_stage_output_digest THEN
        RAISE EXCEPTION 'ambient replay promotion requires exact completed meeting digest stage';
    END IF;
    IF NEW.recorded_at < replay.started_at OR NEW.recorded_at > clock_timestamp() + interval '1 minute' THEN
        RAISE EXCEPTION 'ambient replay promotion recorded_at is outside execution window';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER ambient_intelligence_replay_promotion_exact
BEFORE INSERT ON ambient_intelligence_replay_promotions
FOR EACH ROW EXECUTE FUNCTION ambient_intelligence_replay_promotion_exact();

CREATE FUNCTION ambient_intelligence_replay_promotion_immutable()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ambient replay promotion receipts are immutable';
END;
$$;

CREATE TRIGGER ambient_intelligence_replay_promotion_immutable
BEFORE UPDATE OR DELETE ON ambient_intelligence_replay_promotions
FOR EACH ROW EXECUTE FUNCTION ambient_intelligence_replay_promotion_immutable();

COMMIT;
