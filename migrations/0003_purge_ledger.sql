BEGIN;

CREATE TABLE purge_ledger (
    tenant_id text NOT NULL,
    object_id text NOT NULL,
    revision_id text NOT NULL,
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256) = 32),
    policy_id text NOT NULL,
    purged_at timestamptz NOT NULL,
    destruction_evidence jsonb NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, object_id, revision_id),
    CHECK (jsonb_typeof(destruction_evidence) = 'object')
);

COMMIT;
