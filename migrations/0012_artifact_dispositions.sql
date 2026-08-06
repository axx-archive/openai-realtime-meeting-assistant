BEGIN;

-- Body-free authority state for completion actions. Artifact content remains
-- in its existing object store; these rows bind every action to the exact
-- revision, ACL, and audience that was reauthorized at execution time.
CREATE TABLE stride_artifact_disposition_states (
    tenant_id text NOT NULL,
    artifact_id text NOT NULL,
    content_revision bigint NOT NULL CHECK (content_revision > 0),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest) = 32),
    acl_version bigint NOT NULL CHECK (acl_version > 0),
    audience_digest bytea NOT NULL CHECK (octet_length(audience_digest) = 32),
    drive_reference_id text,
    drive_content_revision bigint,
    drive_content_digest bytea,
    drive_acl_version bigint,
    drive_audience_digest bytea,
    drive_created_at timestamptz,
    drive_created_by text,
    pending_operation_id text,
    tombstoned_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, artifact_id),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (artifact_id = btrim(artifact_id) AND artifact_id <> ''),
    CHECK (pending_operation_id IS NULL OR (pending_operation_id = btrim(pending_operation_id) AND pending_operation_id <> '')),
    CHECK (
        (drive_reference_id IS NULL AND drive_content_revision IS NULL AND drive_content_digest IS NULL AND
         drive_acl_version IS NULL AND drive_audience_digest IS NULL AND drive_created_at IS NULL AND drive_created_by IS NULL)
        OR
        (drive_reference_id = btrim(drive_reference_id) AND drive_reference_id <> '' AND
         drive_content_revision > 0 AND octet_length(drive_content_digest) = 32 AND drive_acl_version > 0 AND
         octet_length(drive_audience_digest) = 32 AND drive_created_at IS NOT NULL AND
         drive_created_by = btrim(drive_created_by) AND drive_created_by <> '')
    )
);

CREATE TABLE stride_artifact_discard_confirmations (
    tenant_id text NOT NULL,
    artifact_id text NOT NULL,
    confirmation_id text NOT NULL,
    operation_id text NOT NULL,
    actor_principal text NOT NULL,
    content_revision bigint NOT NULL CHECK (content_revision > 0),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest) = 32),
    acl_version bigint NOT NULL CHECK (acl_version > 0),
    audience_digest bytea NOT NULL CHECK (octet_length(audience_digest) = 32),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    consumed_at timestamptz,
    PRIMARY KEY (tenant_id, artifact_id, confirmation_id),
    UNIQUE (tenant_id, operation_id),
    FOREIGN KEY (tenant_id, artifact_id)
        REFERENCES stride_artifact_disposition_states (tenant_id, artifact_id)
        ON DELETE CASCADE,
    CHECK (confirmation_id = btrim(confirmation_id) AND confirmation_id <> ''),
    CHECK (operation_id = btrim(operation_id) AND operation_id <> ''),
    CHECK (actor_principal = btrim(actor_principal) AND actor_principal <> '')
);

CREATE UNIQUE INDEX stride_artifact_discard_one_live_confirmation
    ON stride_artifact_discard_confirmations (tenant_id, artifact_id)
    WHERE consumed_at IS NULL;

CREATE TABLE stride_artifact_disposition_receipts (
    tenant_id text NOT NULL,
    operation_id text NOT NULL,
    artifact_id text NOT NULL,
    action text NOT NULL CHECK (action IN ('open', 'save', 'discard')),
    actor_principal text NOT NULL,
    content_revision bigint NOT NULL CHECK (content_revision > 0),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest) = 32),
    acl_version bigint NOT NULL CHECK (acl_version > 0),
    audience_digest bytea NOT NULL CHECK (octet_length(audience_digest) = 32),
    outcome text NOT NULL CHECK (outcome IN ('opened', 'save_pending', 'saved', 'save_conflicted', 'confirmation_required', 'discard_pending', 'discarded', 'discard_conflicted', 'chat_retracted_drive_preserved')),
    confirmation_id text,
    prior_confirmation_id text,
    drive_reference_id text,
    retracted_references integer NOT NULL DEFAULT 0 CHECK (retracted_references >= 0),
    receipt_digest bytea NOT NULL CHECK (octet_length(receipt_digest) = 32),
    occurred_at timestamptz NOT NULL,
    receipt jsonb NOT NULL,
    PRIMARY KEY (tenant_id, operation_id),
    UNIQUE (tenant_id, operation_id, artifact_id),
    FOREIGN KEY (tenant_id, artifact_id)
        REFERENCES stride_artifact_disposition_states (tenant_id, artifact_id),
    CHECK (operation_id = btrim(operation_id) AND operation_id <> ''),
    CHECK (artifact_id = btrim(artifact_id) AND artifact_id <> ''),
    CHECK (actor_principal = btrim(actor_principal) AND actor_principal <> ''),
    CHECK (confirmation_id IS NULL OR (confirmation_id = btrim(confirmation_id) AND confirmation_id <> '')),
    CHECK (prior_confirmation_id IS NULL OR (prior_confirmation_id = btrim(prior_confirmation_id) AND prior_confirmation_id <> '')),
    CHECK (drive_reference_id IS NULL OR (drive_reference_id = btrim(drive_reference_id) AND drive_reference_id <> ''))
);

CREATE INDEX stride_artifact_disposition_receipts_artifact
    ON stride_artifact_disposition_receipts (tenant_id, artifact_id, occurred_at DESC);

ALTER TABLE stride_artifact_disposition_states
    ADD CONSTRAINT stride_artifact_disposition_pending_receipt
    FOREIGN KEY (tenant_id, pending_operation_id, artifact_id)
    REFERENCES stride_artifact_disposition_receipts (tenant_id, operation_id, artifact_id);

COMMIT;
