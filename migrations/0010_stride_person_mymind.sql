BEGIN;

-- E10-R3 adds a global person root above revocable workspace memberships. No
-- route reads these tables in this migration, and the feature switch at the
-- end remains permanently false under the existing E1 activation fence.
CREATE TABLE stride_person_principals (
    person_id text PRIMARY KEY,
    revision bigint NOT NULL CHECK (revision > 0),
    account_subject_digest bytea NOT NULL UNIQUE CHECK (octet_length(account_subject_digest) = 32),
    status text NOT NULL CHECK (status IN ('active', 'recovery_pending', 'deletion_pending', 'deleted')),
    recovery_revision bigint NOT NULL CHECK (recovery_revision > 0),
    custody_revision bigint NOT NULL CHECK (custody_revision > 0),
    created_at timestamptz NOT NULL,
    custody_deletion_receipt_id text,
    deleted_at timestamptz,
    CHECK (person_id = btrim(person_id) AND person_id <> ''),
    CHECK ((status = 'deleted' AND deleted_at IS NOT NULL AND custody_deletion_receipt_id IS NOT NULL) OR
           (status <> 'deleted' AND deleted_at IS NULL AND custody_deletion_receipt_id IS NULL))
);

CREATE TABLE stride_workspace_memberships (
    membership_id text NOT NULL,
    workspace_id text NOT NULL,
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    revision bigint NOT NULL CHECK (revision > 0),
    role text NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'contractor', 'freelance')),
    status text NOT NULL CHECK (status IN ('active', 'revoked', 'departed')),
    granted_at timestamptz NOT NULL,
    revoked_at timestamptz,
    PRIMARY KEY (membership_id, revision),
    UNIQUE (membership_id, person_id, workspace_id, revision),
    CHECK (membership_id = btrim(membership_id) AND membership_id <> ''),
    CHECK (workspace_id = btrim(workspace_id) AND workspace_id <> ''),
    CHECK ((status = 'active') = (revoked_at IS NULL)),
    CHECK (revoked_at IS NULL OR revoked_at >= granted_at)
);
CREATE INDEX stride_workspace_membership_person
    ON stride_workspace_memberships (person_id, status, workspace_id);
CREATE INDEX stride_workspace_membership_workspace
    ON stride_workspace_memberships (workspace_id, status, membership_id);
CREATE UNIQUE INDEX stride_workspace_membership_one_active
    ON stride_workspace_memberships (workspace_id, person_id)
    WHERE status = 'active';

-- MyMind bodies remain in encrypted revision custody. This table stores only
-- source identity, digests, consent revision, purpose, and workspace binding.
CREATE TABLE stride_mymind_sources (
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    source_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest) = 32),
    source_kind text NOT NULL CHECK (source_kind IN ('private_import', 'preference', 'collaboration_pattern', 'correction', 'reflection', 'portable_receipt', 'public_work')),
    bound_workspace_id text,
    confidentiality text NOT NULL CHECK (confidentiality IN ('private', 'workspace_confidential', 'portable', 'public')),
    custody_ref text NOT NULL,
    allowed_purposes text[] NOT NULL,
    consent_revision bigint NOT NULL CHECK (consent_revision > 0),
    consent_status text NOT NULL CHECK (consent_status IN ('granted', 'withdrawn', 'deleted')),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (person_id, source_id, revision),
    UNIQUE (person_id, source_id, revision, consent_revision),
    UNIQUE (person_id, source_id, revision, consent_revision, content_digest),
    UNIQUE (person_id, source_id, revision, consent_revision, content_digest, custody_ref),
    CHECK (source_id = btrim(source_id) AND source_id <> ''),
    CHECK (bound_workspace_id IS NULL OR (bound_workspace_id = btrim(bound_workspace_id) AND bound_workspace_id <> '')),
    CHECK (custody_ref = btrim(custody_ref) AND custody_ref <> ''),
    CHECK (cardinality(allowed_purposes) > 0),
    CHECK (allowed_purposes <@ ARRAY['private_answer', 'collaboration', 'shared_answer', 'meeting_support', 'portable_export', 'account_custody']::text[]),
    CHECK (confidentiality <> 'workspace_confidential' OR bound_workspace_id IS NOT NULL),
    CHECK (confidentiality NOT IN ('portable', 'public') OR source_kind IN ('portable_receipt', 'public_work'))
);
CREATE INDEX stride_mymind_source_current
    ON stride_mymind_sources (person_id, source_id, revision DESC, consent_revision DESC);
CREATE INDEX stride_mymind_source_workspace
    ON stride_mymind_sources (person_id, bound_workspace_id, consent_status);

-- Destination kinds are deliberately exact. There is no organization-wide
-- audience value and therefore no organization-wide MyMind grant.
CREATE TABLE stride_mymind_disclosure_grants (
    grant_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL CHECK (membership_revision > 0),
    source_id text NOT NULL,
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    source_consent_revision bigint NOT NULL CHECK (source_consent_revision > 0),
    source_digest bytea NOT NULL CHECK (octet_length(source_digest) = 32),
    workspace_id text NOT NULL,
    destination_kind text NOT NULL CHECK (destination_kind IN ('private_person', 'workspace_thread', 'workspace_channel', 'workspace_meeting', 'public_export')),
    destination_audience_id text NOT NULL,
    purpose text NOT NULL CHECK (purpose IN ('private_answer', 'collaboration', 'shared_answer', 'meeting_support', 'portable_export', 'account_custody')),
    modes text[] NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'revoked', 'expired')),
    granted_at timestamptz NOT NULL,
    expires_at timestamptz,
    revoked_at timestamptz,
    PRIMARY KEY (grant_id, revision),
    FOREIGN KEY (membership_id, person_id, workspace_id, membership_revision)
        REFERENCES stride_workspace_memberships (membership_id, person_id, workspace_id, revision),
    FOREIGN KEY (person_id, source_id, source_revision, source_consent_revision, source_digest)
        REFERENCES stride_mymind_sources (person_id, source_id, revision, consent_revision, content_digest),
    CHECK (grant_id = btrim(grant_id) AND grant_id <> ''),
    CHECK (workspace_id = btrim(workspace_id) AND workspace_id <> ''),
    CHECK (destination_audience_id = btrim(destination_audience_id) AND destination_audience_id NOT IN ('', 'organization', 'workspace')),
    CHECK (cardinality(modes) > 0),
    CHECK (modes <@ ARRAY['personalize', 'cite', 'quote', 'assert_basis', 'export']::text[]),
    CHECK ((status = 'active') = (revoked_at IS NULL)),
    CHECK (expires_at IS NULL OR expires_at > granted_at)
);
CREATE INDEX stride_mymind_disclosure_intersection
    ON stride_mymind_disclosure_grants
       (person_id, membership_id, membership_revision, workspace_id, destination_kind, destination_audience_id, purpose, source_id, source_revision, source_consent_revision, status);

-- These authorities are intentionally separate records with non-overlapping
-- scopes; holding one never implies any other lifecycle power.
CREATE TABLE stride_mymind_authority_grants (
    authority_grant_id text PRIMARY KEY,
    authority text NOT NULL CHECK (authority IN ('account_recovery', 'workspace_membership', 'mymind_custody', 'organization_export', 'departure', 'global_delete')),
    controller_id text NOT NULL,
    person_id text,
    workspace_id text,
    scope_kind text GENERATED ALWAYS AS (
        CASE WHEN person_id IS NOT NULL THEN 'person' ELSE 'workspace' END
    ) STORED,
    scope_id text GENERATED ALWAYS AS (COALESCE(person_id, workspace_id)) STORED,
    granted_at timestamptz NOT NULL,
    revoked_at timestamptz,
    UNIQUE (authority_grant_id, authority, scope_kind, scope_id),
    CHECK (authority_grant_id = btrim(authority_grant_id) AND authority_grant_id <> ''),
    CHECK (controller_id = btrim(controller_id) AND controller_id <> ''),
    CHECK ((authority IN ('account_recovery', 'mymind_custody', 'global_delete') AND person_id IS NOT NULL AND workspace_id IS NULL) OR
           (authority IN ('workspace_membership', 'organization_export', 'departure') AND person_id IS NULL AND workspace_id IS NOT NULL)),
    CHECK (revoked_at IS NULL OR revoked_at >= granted_at)
);
CREATE INDEX stride_mymind_authority_scope
    ON stride_mymind_authority_grants (authority, person_id, workspace_id, controller_id)
    WHERE revoked_at IS NULL;

-- A deletion receipt is external evidence, not an instruction to a KMS. Its
-- item manifest must cover every exact current custody source revision before
-- a person may transition to deleted.
CREATE TABLE stride_mymind_custody_deletion_receipts (
    deletion_receipt_id text PRIMARY KEY,
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    custody_authority_grant_id text NOT NULL,
    custody_authority text NOT NULL DEFAULT 'mymind_custody' CHECK (custody_authority = 'mymind_custody'),
    custody_scope_kind text NOT NULL DEFAULT 'person' CHECK (custody_scope_kind = 'person'),
    source_count integer NOT NULL CHECK (source_count >= 0),
    source_manifest_digest bytea NOT NULL CHECK (octet_length(source_manifest_digest) = 32),
    external_evidence_digest bytea NOT NULL CHECK (octet_length(external_evidence_digest) = 32),
    recorded_at timestamptz NOT NULL,
    UNIQUE (deletion_receipt_id, person_id),
    FOREIGN KEY (custody_authority_grant_id, custody_authority, custody_scope_kind, person_id)
        REFERENCES stride_mymind_authority_grants (authority_grant_id, authority, scope_kind, scope_id),
    CHECK (deletion_receipt_id = btrim(deletion_receipt_id) AND deletion_receipt_id <> '')
);

CREATE TABLE stride_mymind_custody_deletion_items (
    deletion_receipt_id text NOT NULL,
    person_id text NOT NULL,
    source_id text NOT NULL,
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    source_consent_revision bigint NOT NULL CHECK (source_consent_revision > 0),
    source_digest bytea NOT NULL CHECK (octet_length(source_digest) = 32),
    custody_ref text NOT NULL,
    effect text NOT NULL CHECK (effect = 'body_destroyed'),
    deleted_at timestamptz NOT NULL,
    PRIMARY KEY (deletion_receipt_id, source_id),
    FOREIGN KEY (deletion_receipt_id, person_id)
        REFERENCES stride_mymind_custody_deletion_receipts (deletion_receipt_id, person_id) ON DELETE CASCADE,
    FOREIGN KEY (person_id, source_id, source_revision, source_consent_revision, source_digest, custody_ref)
        REFERENCES stride_mymind_sources (person_id, source_id, revision, consent_revision, content_digest, custody_ref),
    CHECK (source_id = btrim(source_id) AND source_id <> ''),
    CHECK (custody_ref = btrim(custody_ref) AND custody_ref <> '')
);

ALTER TABLE stride_person_principals
    ADD CONSTRAINT stride_person_custody_deletion_receipt
    FOREIGN KEY (custody_deletion_receipt_id, person_id)
    REFERENCES stride_mymind_custody_deletion_receipts (deletion_receipt_id, person_id);

CREATE FUNCTION stride_person_delete_has_exact_custody_receipt()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    expected_count integer;
    recorded_count integer;
BEGIN
    IF NEW.status = 'deleted' AND (TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM 'deleted') THEN
        SELECT receipt.source_count, count(item.source_id)::integer
          INTO expected_count, recorded_count
          FROM stride_mymind_custody_deletion_receipts receipt
          LEFT JOIN stride_mymind_custody_deletion_items item
            ON item.deletion_receipt_id = receipt.deletion_receipt_id
           AND item.person_id = receipt.person_id
         WHERE receipt.deletion_receipt_id = NEW.custody_deletion_receipt_id
           AND receipt.person_id = NEW.person_id
         GROUP BY receipt.source_count;
        IF expected_count IS NULL OR expected_count <> recorded_count THEN
            RAISE EXCEPTION 'exact MyMind custody deletion receipt is required';
        END IF;
        IF expected_count <> (
            SELECT count(*) FROM (
                SELECT DISTINCT ON (source_id) source_id
                  FROM stride_mymind_sources
                 WHERE person_id = NEW.person_id
                 ORDER BY source_id, revision DESC
            ) current_sources
        ) OR EXISTS (
            SELECT 1
              FROM (
                  SELECT DISTINCT ON (source_id)
                         source_id, revision, consent_revision, content_digest, custody_ref
                    FROM stride_mymind_sources
                   WHERE person_id = NEW.person_id
                   ORDER BY source_id, revision DESC
              ) current_source
             WHERE NOT EXISTS (
                 SELECT 1
                   FROM stride_mymind_custody_deletion_items item
                  WHERE item.deletion_receipt_id = NEW.custody_deletion_receipt_id
                    AND item.person_id = NEW.person_id
                    AND item.source_id = current_source.source_id
                    AND item.source_revision = current_source.revision
                    AND item.source_consent_revision = current_source.consent_revision
                    AND item.source_digest = current_source.content_digest
                    AND item.custody_ref = current_source.custody_ref
                    AND item.effect = 'body_destroyed'
             )
        ) THEN
            RAISE EXCEPTION 'custody deletion receipt does not cover current MyMind sources';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER stride_person_delete_custody_gate
BEFORE INSERT OR UPDATE OF status, custody_deletion_receipt_id ON stride_person_principals
FOR EACH ROW EXECUTE FUNCTION stride_person_delete_has_exact_custody_receipt();

CREATE FUNCTION stride_mymind_source_requires_active_person()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    person_status text;
BEGIN
    SELECT status INTO person_status
      FROM stride_person_principals
     WHERE person_id = NEW.person_id
     FOR UPDATE;
    IF person_status IS DISTINCT FROM 'active' THEN
        RAISE EXCEPTION 'MyMind custody source requires an active person';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER stride_mymind_source_active_person_gate
BEFORE INSERT OR UPDATE ON stride_mymind_sources
FOR EACH ROW EXECUTE FUNCTION stride_mymind_source_requires_active_person();

CREATE FUNCTION stride_mymind_source_refs_are_valid(refs jsonb)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT jsonb_typeof(refs) = 'array'
       AND jsonb_array_length(CASE WHEN jsonb_typeof(refs) = 'array' THEN refs ELSE '[]'::jsonb END) > 0
       AND NOT stride_jsonb_has_forbidden_key(refs, ARRAY['body', 'text', 'audio', 'secret', 'token', 'api_key', 'authorization', 'credential', 'credentials', 'password', 'cookie'])
       AND NOT EXISTS (
           SELECT 1
           FROM jsonb_array_elements(CASE WHEN jsonb_typeof(refs) = 'array' THEN refs ELSE '[]'::jsonb END) AS entry(value)
           WHERE jsonb_typeof(value) <> 'object'
              OR (SELECT count(*) FROM jsonb_object_keys(CASE WHEN jsonb_typeof(value) = 'object' THEN value ELSE '{}'::jsonb END)) <> 5
              OR NOT value ?& ARRAY['personId', 'sourceId', 'revision', 'consentRevision', 'digest']
              OR jsonb_typeof(value->'personId') <> 'string'
              OR value->>'personId' !~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$'
              OR jsonb_typeof(value->'sourceId') <> 'string'
              OR value->>'sourceId' !~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$'
              OR jsonb_typeof(value->'revision') <> 'number'
              OR value->>'revision' !~ '^[1-9][0-9]*$'
              OR jsonb_typeof(value->'consentRevision') <> 'number'
              OR value->>'consentRevision' !~ '^[1-9][0-9]*$'
              OR jsonb_typeof(value->'digest') <> 'string'
              OR value->>'digest' !~ '^[0-9A-Fa-f]{64}$'
       );
$$;

CREATE TABLE stride_mymind_export_receipts (
    export_receipt_id text PRIMARY KEY,
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    workspace_id text NOT NULL,
    organization_authority_grant_id text NOT NULL,
    organization_authority text NOT NULL DEFAULT 'organization_export' CHECK (organization_authority = 'organization_export'),
    organization_scope_kind text NOT NULL DEFAULT 'workspace' CHECK (organization_scope_kind = 'workspace'),
    custody_authority_grant_id text NOT NULL,
    custody_authority text NOT NULL DEFAULT 'mymind_custody' CHECK (custody_authority = 'mymind_custody'),
    custody_scope_kind text NOT NULL DEFAULT 'person' CHECK (custody_scope_kind = 'person'),
    source_refs jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    CHECK (export_receipt_id = btrim(export_receipt_id) AND export_receipt_id <> ''),
    CHECK (workspace_id = btrim(workspace_id) AND workspace_id <> ''),
    CHECK (organization_authority_grant_id <> custody_authority_grant_id),
    CHECK (stride_mymind_source_refs_are_valid(source_refs)),
    FOREIGN KEY (organization_authority_grant_id, organization_authority, organization_scope_kind, workspace_id)
        REFERENCES stride_mymind_authority_grants (authority_grant_id, authority, scope_kind, scope_id),
    FOREIGN KEY (custody_authority_grant_id, custody_authority, custody_scope_kind, person_id)
        REFERENCES stride_mymind_authority_grants (authority_grant_id, authority, scope_kind, scope_id)
);

-- Allow body-free lineage to name the new contracts without making any of
-- them retrievable by an existing consumer.
CREATE OR REPLACE FUNCTION stride_structured_refs_are_valid(refs jsonb)
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
                   'agent_learning_record', 'agent_performance_receipt', 'agent_update_proposal', 'workforce_policy',
                   'person_principal', 'workspace_membership', 'mymind_source', 'mymind_disclosure_grant',
                   'mymind_custody_deletion_receipt'
              ])
              OR jsonb_typeof(value->'id') <> 'string'
              OR value->>'id' !~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$'
              OR jsonb_typeof(value->'revision') <> 'number'
              OR value->>'revision' !~ '^[1-9][0-9]*$'
              OR jsonb_typeof(value->'digest') <> 'string'
              OR value->>'digest' !~ '^[0-9A-Fa-f]{64}$'
       );
$$;

INSERT INTO stride_feature_switches (feature_key, enabled, revision)
VALUES ('person_mymind_context', false, 1)
ON CONFLICT (feature_key) DO NOTHING;

COMMIT;
