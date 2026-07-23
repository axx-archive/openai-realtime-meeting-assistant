BEGIN;

CREATE TABLE catch_up_publications (
    publication_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    recipient_email text NOT NULL,
    room_id text NOT NULL,
    sitting_id text NOT NULL,
    snapshot_id text NOT NULL,
    sources_sha256 bytea NOT NULL CHECK (octet_length(sources_sha256) = 32),
    authority jsonb,
    authority_sha256 bytea NOT NULL CHECK (octet_length(authority_sha256) = 32),
    payload jsonb,
    payload_sha256 bytea NOT NULL CHECK (octet_length(payload_sha256) = 32),
    notification_id text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'pending',
    committed_at timestamptz NOT NULL DEFAULT now(),
    retain_until timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    notification_persisted_at timestamptz,
    push_dispatched_at timestamptz,
    delivered_at timestamptz,
    cancelled_at timestamptz,
    cancellation_reason text,
    redacted_at timestamptz,
    CHECK (publication_id = btrim(publication_id) AND publication_id <> ''),
    CHECK (tenant_id = btrim(tenant_id) AND tenant_id <> ''),
    CHECK (recipient_email = btrim(recipient_email) AND recipient_email <> ''),
    CHECK (room_id = btrim(room_id) AND room_id <> ''),
    CHECK (sitting_id = btrim(sitting_id) AND sitting_id <> ''),
    CHECK (snapshot_id = btrim(snapshot_id) AND snapshot_id <> ''),
    CHECK (notification_id = btrim(notification_id) AND notification_id <> ''),
    CHECK (status IN ('pending','delivered','cancelled')),
    CHECK (authority IS NULL OR jsonb_typeof(authority) = 'object'),
    CHECK (payload IS NULL OR jsonb_typeof(payload) = 'object'),
    CHECK (
        (status = 'pending' AND authority IS NOT NULL AND payload IS NOT NULL AND redacted_at IS NULL)
        OR
        (status = 'delivered' AND delivered_at IS NOT NULL AND authority IS NULL AND payload IS NULL AND redacted_at IS NOT NULL)
        OR
        (status = 'cancelled' AND cancelled_at IS NOT NULL AND cancellation_reason IS NOT NULL AND authority IS NULL AND payload IS NULL AND redacted_at IS NOT NULL)
    )
);

CREATE INDEX catch_up_publications_pending
    ON catch_up_publications (committed_at, publication_id)
    WHERE status = 'pending';

CREATE INDEX catch_up_publications_retention
    ON catch_up_publications (retain_until, publication_id)
    WHERE status = 'pending';

COMMIT;
