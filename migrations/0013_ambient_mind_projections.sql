BEGIN;

-- E10-R2 is shadow-only. This migration establishes durable typed graph
-- authority but cannot activate a reader or replace a current product path.
INSERT INTO stride_feature_switches(feature_key,enabled,revision)
VALUES ('ambient_mind_projection_shadow',false,1)
ON CONFLICT (feature_key) DO NOTHING;

CREATE FUNCTION stride_ambient_audience_principals_are_valid(principals jsonb)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT jsonb_typeof(principals) = 'array'
       AND jsonb_array_length(CASE WHEN jsonb_typeof(principals) = 'array' THEN principals ELSE '[]'::jsonb END) > 0
       AND NOT EXISTS (
           SELECT 1
             FROM jsonb_array_elements(CASE WHEN jsonb_typeof(principals) = 'array' THEN principals ELSE '[]'::jsonb END) AS entry(value)
            WHERE jsonb_typeof(value) <> 'string'
               OR entry.value #>> '{}' !~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$'
       )
       AND jsonb_array_length(CASE WHEN jsonb_typeof(principals) = 'array' THEN principals ELSE '[]'::jsonb END) = (
           SELECT count(DISTINCT entry.value)
             FROM jsonb_array_elements(CASE WHEN jsonb_typeof(principals) = 'array' THEN principals ELSE '[]'::jsonb END) AS entry(value)
       );
$$;

CREATE TABLE stride_ambient_projection_events (
    tenant_id text NOT NULL,
    event_id text NOT NULL,
    idempotency_key text NOT NULL,
    sequence bigint NOT NULL CHECK (sequence > 0),
    source_high_water bigint NOT NULL CHECK (source_high_water > 0),
    operation text NOT NULL CHECK (operation IN ('upsert','revoke','retract')),
    event_digest bytea NOT NULL CHECK (octet_length(event_digest) = 32),
    event_document jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,event_id),
    UNIQUE (tenant_id,idempotency_key),
    UNIQUE (tenant_id,sequence),
    CHECK (tenant_id=btrim(tenant_id) AND tenant_id<>''),
    CHECK (event_id=btrim(event_id) AND event_id<>''),
    CHECK (idempotency_key=btrim(idempotency_key) AND idempotency_key<>''),
    CHECK (jsonb_typeof(event_document)='object'),
    CHECK (NOT stride_jsonb_has_forbidden_key(event_document, ARRAY['body','text','audio','secret','token','api_key','authorization','credential','credentials','password','cookie']))
);

CREATE INDEX stride_ambient_projection_event_watermark
    ON stride_ambient_projection_events(tenant_id,source_high_water,sequence);

CREATE TABLE stride_ambient_projection_sources (
    tenant_id text NOT NULL,
    source_contract_type text NOT NULL,
    source_contract_id text NOT NULL,
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    source_digest bytea NOT NULL CHECK (octet_length(source_digest)=32),
    visibility text NOT NULL CHECK (visibility IN ('project','channel','organization','meeting')),
    audience_principals jsonb NOT NULL,
    audience_digest bytea NOT NULL CHECK (octet_length(audience_digest)=32),
    acl_version bigint NOT NULL CHECK (acl_version > 0),
    source_high_water bigint NOT NULL CHECK (source_high_water > 0),
    fresh_through timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,source_contract_type,source_contract_id,source_revision,source_digest),
    CHECK (tenant_id=btrim(tenant_id) AND tenant_id<>''),
    CHECK (stride_ambient_audience_principals_are_valid(audience_principals)),
    CHECK (NOT stride_jsonb_has_forbidden_key(audience_principals, ARRAY['body','text','audio','secret','token','api_key','authorization','credential','credentials','password','cookie']))
);

CREATE TABLE stride_ambient_projection_nodes (
    tenant_id text NOT NULL,
    node_contract_type text NOT NULL CHECK (node_contract_type IN ('analysis_projection','knowledge_assertion')),
    node_id text NOT NULL,
    node_revision bigint NOT NULL CHECK (node_revision > 0),
    node_digest bytea NOT NULL CHECK (octet_length(node_digest)=32),
    logical_id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('decision','commitment','blocker','alignment','storyline','entity','artifact','work_receipt','known_gap')),
    visibility text NOT NULL CHECK (visibility IN ('project','channel','organization','meeting')),
    audience_principals jsonb NOT NULL,
    audience_digest bytea NOT NULL CHECK (octet_length(audience_digest)=32),
    acl_version bigint NOT NULL CHECK (acl_version > 0),
    source_high_water bigint NOT NULL CHECK (source_high_water > 0),
    fresh_through timestamptz NOT NULL,
    supersedes_contract_type text,
    supersedes_id text,
    supersedes_revision bigint,
    supersedes_digest bytea,
    PRIMARY KEY (tenant_id,node_contract_type,node_id,node_revision,node_digest),
    CHECK (tenant_id=btrim(tenant_id) AND tenant_id<>''),
    CHECK (node_id=btrim(node_id) AND node_id<>''),
    CHECK (logical_id=btrim(logical_id) AND logical_id<>''),
    CHECK (stride_ambient_audience_principals_are_valid(audience_principals)),
    CHECK ((supersedes_contract_type IS NULL AND supersedes_id IS NULL AND supersedes_revision IS NULL AND supersedes_digest IS NULL) OR
           (supersedes_contract_type IN ('analysis_projection','knowledge_assertion') AND supersedes_id IS NOT NULL AND supersedes_revision > 0 AND octet_length(supersedes_digest)=32))
);

CREATE INDEX stride_ambient_projection_node_logical
    ON stride_ambient_projection_nodes(tenant_id,logical_id,node_revision DESC);

CREATE TABLE stride_ambient_projection_source_edges (
    tenant_id text NOT NULL,
    source_contract_type text NOT NULL,
    source_contract_id text NOT NULL,
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    source_digest bytea NOT NULL CHECK (octet_length(source_digest)=32),
    node_contract_type text NOT NULL,
    node_id text NOT NULL,
    node_revision bigint NOT NULL CHECK (node_revision > 0),
    node_digest bytea NOT NULL CHECK (octet_length(node_digest)=32),
    PRIMARY KEY (tenant_id,source_contract_type,source_contract_id,source_revision,source_digest,node_contract_type,node_id,node_revision,node_digest),
    FOREIGN KEY (tenant_id,source_contract_type,source_contract_id,source_revision,source_digest)
        REFERENCES stride_ambient_projection_sources(tenant_id,source_contract_type,source_contract_id,source_revision,source_digest),
    FOREIGN KEY (tenant_id,node_contract_type,node_id,node_revision,node_digest)
        REFERENCES stride_ambient_projection_nodes(tenant_id,node_contract_type,node_id,node_revision,node_digest)
);

CREATE INDEX stride_ambient_projection_source_fanout
    ON stride_ambient_projection_source_edges(tenant_id,source_contract_type,source_contract_id,source_revision);

CREATE TABLE stride_ambient_projection_node_edges (
    tenant_id text NOT NULL,
    parent_contract_type text NOT NULL,
    parent_id text NOT NULL,
    parent_revision bigint NOT NULL CHECK (parent_revision > 0),
    parent_digest bytea NOT NULL CHECK (octet_length(parent_digest)=32),
    child_contract_type text NOT NULL,
    child_id text NOT NULL,
    child_revision bigint NOT NULL CHECK (child_revision > 0),
    child_digest bytea NOT NULL CHECK (octet_length(child_digest)=32),
    PRIMARY KEY (tenant_id,parent_contract_type,parent_id,parent_revision,parent_digest,child_contract_type,child_id,child_revision,child_digest),
    FOREIGN KEY (tenant_id,parent_contract_type,parent_id,parent_revision,parent_digest)
        REFERENCES stride_ambient_projection_nodes(tenant_id,node_contract_type,node_id,node_revision,node_digest),
    FOREIGN KEY (tenant_id,child_contract_type,child_id,child_revision,child_digest)
        REFERENCES stride_ambient_projection_nodes(tenant_id,node_contract_type,node_id,node_revision,node_digest)
);

CREATE INDEX stride_ambient_projection_node_fanout
    ON stride_ambient_projection_node_edges(tenant_id,parent_contract_type,parent_id,parent_revision);

CREATE TABLE stride_ambient_projection_node_states (
    tenant_id text NOT NULL,
    node_contract_type text NOT NULL,
    node_id text NOT NULL,
    node_revision bigint NOT NULL CHECK (node_revision > 0),
    node_digest bytea NOT NULL CHECK (octet_length(node_digest)=32),
    logical_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('current','superseded','retracted')),
    reason text,
    projection_high_water bigint NOT NULL CHECK (projection_high_water >= 0),
    PRIMARY KEY (tenant_id,node_contract_type,node_id,node_revision,node_digest),
    FOREIGN KEY (tenant_id,node_contract_type,node_id,node_revision,node_digest)
        REFERENCES stride_ambient_projection_nodes(tenant_id,node_contract_type,node_id,node_revision,node_digest),
    CHECK ((status='current' AND reason IS NULL) OR (status<>'current' AND reason IS NOT NULL AND reason=btrim(reason) AND reason<>''))
);

CREATE UNIQUE INDEX stride_ambient_projection_one_current
    ON stride_ambient_projection_node_states(tenant_id,logical_id) WHERE status='current';

CREATE TABLE stride_ambient_projection_checkpoints (
    tenant_id text PRIMARY KEY,
    generation bigint NOT NULL CHECK (generation >= 0),
    through_sequence bigint NOT NULL CHECK (through_sequence >= 0),
    source_high_water bigint NOT NULL CHECK (source_high_water >= 0),
    projection_high_water bigint NOT NULL CHECK (projection_high_water >= 0),
    fresh_through timestamptz,
    source_manifest_digest bytea NOT NULL CHECK (octet_length(source_manifest_digest)=32),
    projection_digest bytea NOT NULL CHECK (octet_length(projection_digest)=32),
    rebuilt_at timestamptz NOT NULL,
    CHECK (tenant_id=btrim(tenant_id) AND tenant_id<>''),
    CHECK (through_sequence=projection_high_water)
);

COMMIT;
