BEGIN;

ALTER TABLE approvals
    ADD COLUMN binding jsonb,
    ADD COLUMN requested_by_tenant text,
    ADD COLUMN reason text NOT NULL DEFAULT '',
    ADD COLUMN rejected_by text NOT NULL DEFAULT '',
    ADD COLUMN revoked_by text NOT NULL DEFAULT '';

ALTER TABLE approval_endorsements
    ADD COLUMN principal_tenant text,
    ADD COLUMN role text CHECK (role IN ('member', 'owner', 'admin'));

ALTER TABLE execution_receipts
    ADD COLUMN action_plan_sha256 bytea CHECK (action_plan_sha256 IS NULL OR octet_length(action_plan_sha256) = 32),
    ADD COLUMN created_at timestamptz,
    ADD COLUMN error_text text NOT NULL DEFAULT '';

ALTER TABLE outbox
    ALTER COLUMN event_id DROP NOT NULL,
    ADD COLUMN approval_id uuid UNIQUE REFERENCES approvals(approval_id),
    ADD CONSTRAINT outbox_exact_subject CHECK (num_nonnulls(event_id, approval_id) = 1);

COMMIT;
