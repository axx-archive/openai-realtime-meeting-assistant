BEGIN;

-- JSON audit metadata is intentionally free-form, but the no-body/no-secret
-- boundary is recursive. PostgreSQL's `?` operator checks only the outermost
-- object and would allow {"metadata":{"api_key":"..."}}. Keep one immutable
-- recursive predicate in canonical authority so every future writer receives
-- the same fail-closed rule even if an application validator is bypassed.
CREATE FUNCTION stride_jsonb_has_forbidden_key(document jsonb, forbidden_keys text[])
RETURNS boolean
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
AS $$
DECLARE
    item_key text;
    item_value jsonb;
BEGIN
    IF jsonb_typeof(document) = 'object' THEN
        FOR item_key, item_value IN SELECT lower(entry.key), entry.value FROM jsonb_each(document) AS entry LOOP
            IF item_key = ANY(forbidden_keys) OR stride_jsonb_has_forbidden_key(item_value, forbidden_keys) THEN
                RETURN true;
            END IF;
        END LOOP;
    ELSIF jsonb_typeof(document) = 'array' THEN
        FOR item_value IN SELECT entry.value FROM jsonb_array_elements(document) AS entry LOOP
            IF stride_jsonb_has_forbidden_key(item_value, forbidden_keys) THEN
                RETURN true;
            END IF;
        END LOOP;
    END IF;
    RETURN false;
END;
$$;

-- E1 stores immutable, body-free contract revisions. Text/audio/media bodies
-- stay in revision_bodies/blobs and are represented here only by a digest and
-- optional content reference; audit payloads cannot carry credentials or a
-- provider/capability URL.
CREATE TABLE stride_contract_revisions (
    tenant_id text NOT NULL,
    contract_type text NOT NULL,
    contract_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest) = 32),
    acl_version bigint NOT NULL CHECK (acl_version >= 0),
    audience_digest bytea NOT NULL CHECK (octet_length(audience_digest) = 32),
    retention_policy text NOT NULL,
    purge_generation bigint NOT NULL DEFAULT 0 CHECK (purge_generation >= 0),
    status text NOT NULL,
    body_ref text,
    audit jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL,
    supersedes_revision bigint,
    PRIMARY KEY (tenant_id, contract_type, contract_id, revision),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (contract_type = btrim(contract_type) AND contract_type <> ''),
    CHECK (contract_id = btrim(contract_id) AND contract_id <> ''),
    CHECK (retention_policy = btrim(retention_policy) AND retention_policy <> ''),
    CHECK (status = btrim(status) AND status <> ''),
    CHECK (body_ref IS NULL OR (body_ref = btrim(body_ref) AND body_ref <> '')),
    CHECK (supersedes_revision IS NULL OR (supersedes_revision > 0 AND supersedes_revision < revision)),
    CHECK (jsonb_typeof(audit) = 'object'),
    CHECK (NOT stride_jsonb_has_forbidden_key(audit, ARRAY['body', 'text', 'audio', 'secret', 'token', 'api_key', 'authorization', 'capability_url', 'provider_url', 'credential', 'credentials', 'password', 'cookie']))
);

CREATE INDEX stride_contract_current_lookup
    ON stride_contract_revisions (tenant_id, contract_type, contract_id, revision DESC);
CREATE INDEX stride_contract_purge_lookup
    ON stride_contract_revisions (tenant_id, purge_generation, contract_type, contract_id);
CREATE INDEX stride_contract_audience_lookup
    ON stride_contract_revisions (tenant_id, audience_digest, acl_version);

-- A registry revision describes an otherwise inert route/workflow/coworker or
-- package capability. Nothing in this table is executable configuration; all
-- entries are unavailable or quarantined until a later reviewed activation.
CREATE TABLE stride_registry_revisions (
    tenant_id text NOT NULL,
    registry_kind text NOT NULL,
    registry_key text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    contract_type text NOT NULL,
    contract_id text NOT NULL,
    contract_revision bigint NOT NULL CHECK (contract_revision > 0),
    contract_digest bytea NOT NULL CHECK (octet_length(contract_digest) = 32),
    feature_key text NOT NULL,
    status text NOT NULL CHECK (status IN ('draft', 'quarantined', 'unavailable', 'revoked')),
    schema_digest bytea NOT NULL CHECK (octet_length(schema_digest) = 32),
    capability_audit jsonb NOT NULL DEFAULT '{}'::jsonb,
    quarantine_reason text,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, registry_kind, registry_key, revision),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (registry_kind = btrim(registry_kind) AND registry_kind <> ''),
    CHECK (registry_key = btrim(registry_key) AND registry_key <> ''),
    CHECK (contract_type = btrim(contract_type) AND contract_type <> ''),
    CHECK (contract_id = btrim(contract_id) AND contract_id <> ''),
    CHECK (feature_key = btrim(feature_key) AND feature_key <> ''),
    CHECK (jsonb_typeof(capability_audit) = 'object'),
    CHECK (NOT stride_jsonb_has_forbidden_key(capability_audit, ARRAY['body', 'text', 'audio', 'secret', 'token', 'api_key', 'authorization', 'endpoint', 'url', 'command', 'hook', 'mcp', 'credential', 'credentials', 'password', 'cookie'])),
    CHECK ((status = 'quarantined' AND quarantine_reason IS NOT NULL AND quarantine_reason = btrim(quarantine_reason) AND quarantine_reason <> '') OR
           (status <> 'quarantined' AND quarantine_reason IS NULL))
);

CREATE INDEX stride_registry_current_lookup
    ON stride_registry_revisions (tenant_id, registry_kind, registry_key, revision DESC);
CREATE INDEX stride_registry_feature_status
    ON stride_registry_revisions (tenant_id, feature_key, status, revision DESC);

-- Every feature starts disabled.  E1 may add a new switch or keep one disabled
-- but it must never turn one on; a later migration/review must establish any
-- activation mechanism deliberately.
CREATE TABLE stride_feature_switches (
    feature_key text PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT false CHECK (enabled = false),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (feature_key = btrim(feature_key) AND feature_key <> '')
);

-- This immutable dependency edge is the purge/replay fan-out index. Both
-- sides identify a closed contract revision; no content body is duplicated.
CREATE TABLE stride_source_derived_edges (
    tenant_id text NOT NULL,
    source_contract_type text NOT NULL,
    source_contract_id text NOT NULL,
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    derived_contract_type text NOT NULL,
    derived_contract_id text NOT NULL,
    derived_revision bigint NOT NULL CHECK (derived_revision > 0),
    edge_kind text NOT NULL CHECK (edge_kind IN ('projection', 'evidence', 'preference', 'learning', 'performance', 'assignment', 'answer', 'work')),
    source_digest bytea NOT NULL CHECK (octet_length(source_digest) = 32),
    derived_digest bytea NOT NULL CHECK (octet_length(derived_digest) = 32),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, source_contract_type, source_contract_id, source_revision,
                 derived_contract_type, derived_contract_id, derived_revision, edge_kind),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (source_contract_type = btrim(source_contract_type) AND source_contract_type <> ''),
    CHECK (source_contract_id = btrim(source_contract_id) AND source_contract_id <> ''),
    CHECK (derived_contract_type = btrim(derived_contract_type) AND derived_contract_type <> ''),
    CHECK (derived_contract_id = btrim(derived_contract_id) AND derived_contract_id <> '')
);

CREATE INDEX stride_source_derived_invalidation
    ON stride_source_derived_edges (tenant_id, source_contract_type, source_contract_id, source_revision);
CREATE INDEX stride_derived_source_lineage
    ON stride_source_derived_edges (tenant_id, derived_contract_type, derived_contract_id, derived_revision);

COMMIT;
