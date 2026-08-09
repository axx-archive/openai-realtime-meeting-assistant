BEGIN;

-- E10-W1 establishes additive identity and organization authority only. No
-- existing route reads or writes these tables, and every activation switch at
-- the end remains false. Account subjects are represented only by one-way
-- digests; login email addresses never enter canonical person/org authority.
CREATE TABLE stride_account_person_mappings (
    mapping_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    account_subject_digest bytea NOT NULL CHECK (octet_length(account_subject_digest) = 32),
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    status text NOT NULL CHECK (status IN ('active', 'superseded', 'revoked')),
    created_at timestamptz NOT NULL,
    ended_at timestamptz,
    PRIMARY KEY (mapping_id, revision),
    UNIQUE (mapping_id, revision, account_subject_digest, person_id, status),
    CHECK (mapping_id = btrim(mapping_id) AND mapping_id <> ''),
    CHECK ((status = 'active') = (ended_at IS NULL)),
    CHECK (ended_at IS NULL OR ended_at >= created_at)
);

CREATE TABLE stride_account_person_mappings_current (
    mapping_id text PRIMARY KEY,
    revision bigint NOT NULL CHECK (revision > 0),
    account_subject_digest bytea NOT NULL CHECK (octet_length(account_subject_digest) = 32),
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    status text NOT NULL CHECK (status IN ('active', 'superseded', 'revoked')),
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (mapping_id, revision, account_subject_digest, person_id, status)
        REFERENCES stride_account_person_mappings
        (mapping_id, revision, account_subject_digest, person_id, status),
    CHECK (mapping_id = btrim(mapping_id) AND mapping_id <> '')
);
CREATE UNIQUE INDEX stride_account_person_one_active_subject
    ON stride_account_person_mappings_current (account_subject_digest)
    WHERE status = 'active';
CREATE UNIQUE INDEX stride_account_person_one_active_person
    ON stride_account_person_mappings_current (person_id)
    WHERE status = 'active';
CREATE INDEX stride_account_person_current
    ON stride_account_person_mappings_current (person_id, status, revision DESC);

-- Self-owned profile fields are explicit so account digests, credentials,
-- hidden memberships, inferred traits, and memory bodies cannot be smuggled
-- through a generic profile document.
CREATE TABLE stride_person_profile_revisions (
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    revision bigint NOT NULL CHECK (revision > 0),
    display_name text NOT NULL,
    avatar_blob_ref text,
    pronouns text,
    short_bio text,
    status text NOT NULL CHECK (status IN ('active', 'deleted')),
    created_at timestamptz NOT NULL,
    created_by_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    supersedes_revision bigint,
    PRIMARY KEY (person_id, revision),
    UNIQUE (person_id, revision, status),
    CHECK (display_name = btrim(display_name) AND display_name <> '' AND char_length(display_name) <= 160),
    CHECK (avatar_blob_ref IS NULL OR (avatar_blob_ref = btrim(avatar_blob_ref) AND avatar_blob_ref <> '' AND char_length(avatar_blob_ref) <= 512)),
    CHECK (pronouns IS NULL OR (pronouns = btrim(pronouns) AND pronouns <> '' AND char_length(pronouns) <= 80)),
    CHECK (short_bio IS NULL OR char_length(short_bio) <= 1200),
    CHECK (created_by_person_id = person_id),
    CHECK (supersedes_revision IS NULL OR (supersedes_revision > 0 AND supersedes_revision < revision))
);

CREATE TABLE stride_person_profiles_current (
    person_id text PRIMARY KEY,
    revision bigint NOT NULL CHECK (revision > 0),
    status text NOT NULL CHECK (status IN ('active', 'deleted')),
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (person_id, revision, status)
        REFERENCES stride_person_profile_revisions (person_id, revision, status)
);

CREATE FUNCTION stride_revision_projection_must_advance()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.revision <= OLD.revision THEN
            RAISE EXCEPTION 'current revision must advance';
        END IF;
        IF TG_TABLE_NAME = 'stride_account_person_mappings_current' AND
           (NEW.mapping_id <> OLD.mapping_id OR
            NEW.account_subject_digest <> OLD.account_subject_digest OR
            NEW.person_id <> OLD.person_id) THEN
            RAISE EXCEPTION 'account-person mapping identity is immutable';
        ELSIF TG_TABLE_NAME = 'stride_person_profiles_current' AND
              NEW.person_id <> OLD.person_id THEN
            RAISE EXCEPTION 'person profile identity is immutable';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_account_person_mapping_current_revision_gate
BEFORE UPDATE ON stride_account_person_mappings_current
FOR EACH ROW EXECUTE FUNCTION stride_revision_projection_must_advance();
CREATE TRIGGER stride_person_profile_current_revision_gate
BEFORE UPDATE ON stride_person_profiles_current
FOR EACH ROW EXECUTE FUNCTION stride_revision_projection_must_advance();

CREATE TABLE stride_organizations (
    organization_id text PRIMARY KEY,
    revision bigint NOT NULL CHECK (revision > 0),
    name text NOT NULL,
    slug text NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'archived')),
    discoverability text NOT NULL DEFAULT 'private' CHECK (discoverability IN ('private', 'listed')),
    creator_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    CHECK (organization_id = btrim(organization_id) AND organization_id <> ''),
    CHECK (name = btrim(name) AND name <> '' AND char_length(name) <= 160),
    CHECK (slug = lower(btrim(slug)) AND slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    CHECK (updated_at >= created_at),
    CHECK ((status = 'archived') = (archived_at IS NOT NULL)),
    UNIQUE (slug)
);

CREATE FUNCTION stride_organization_revision_must_advance()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.organization_id <> OLD.organization_id OR
       NEW.creator_person_id <> OLD.creator_person_id OR
       NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'organization root identity is immutable';
    END IF;
    IF NEW.revision <= OLD.revision THEN
        RAISE EXCEPTION 'organization revision must advance';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_organization_revision_gate
BEFORE UPDATE ON stride_organizations
FOR EACH ROW EXECUTE FUNCTION stride_organization_revision_must_advance();

-- One locked root per person serializes active membership allocation. The
-- current membership projection additionally carries one of exactly three
-- unique active slots, so even a buggy or concurrent writer cannot grant a
-- fourth active organization.
CREATE TABLE stride_person_organization_capacity_locks (
    person_id text PRIMARY KEY REFERENCES stride_person_principals(person_id),
    capacity_limit smallint NOT NULL DEFAULT 3 CHECK (capacity_limit = 3),
    lock_revision bigint NOT NULL DEFAULT 1 CHECK (lock_revision > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE stride_organization_membership_revisions (
    membership_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    role text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    status text NOT NULL CHECK (status IN ('active', 'departed', 'revoked')),
    granted_at timestamptz NOT NULL,
    ended_at timestamptz,
    created_at timestamptz NOT NULL,
    created_by_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    supersedes_revision bigint,
    reason_code text,
    PRIMARY KEY (membership_id, revision),
    UNIQUE (membership_id, revision, organization_id, person_id, role, status),
    CHECK (membership_id = btrim(membership_id) AND membership_id <> ''),
    CHECK ((status = 'active') = (ended_at IS NULL)),
    CHECK (ended_at IS NULL OR ended_at >= granted_at),
    CHECK (supersedes_revision IS NULL OR (supersedes_revision > 0 AND supersedes_revision < revision)),
    CHECK (reason_code IS NULL OR (reason_code = btrim(reason_code) AND reason_code <> '' AND char_length(reason_code) <= 96))
);
CREATE INDEX stride_organization_membership_history
    ON stride_organization_membership_revisions (organization_id, person_id, revision DESC);

CREATE TABLE stride_organization_memberships_current (
    membership_id text PRIMARY KEY,
    revision bigint NOT NULL CHECK (revision > 0),
    organization_id text NOT NULL,
    person_id text NOT NULL,
    role text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    status text NOT NULL CHECK (status IN ('active', 'departed', 'revoked')),
    active_slot smallint,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (membership_id, revision, organization_id, person_id, role, status)
        REFERENCES stride_organization_membership_revisions
        (membership_id, revision, organization_id, person_id, role, status),
    CHECK ((status = 'active' AND active_slot BETWEEN 1 AND 3) OR
           (status <> 'active' AND active_slot IS NULL))
);
CREATE UNIQUE INDEX stride_organization_membership_one_active_org
    ON stride_organization_memberships_current (organization_id, person_id)
    WHERE status = 'active';
CREATE UNIQUE INDEX stride_organization_membership_one_active_slot
    ON stride_organization_memberships_current (person_id, active_slot)
    WHERE status = 'active';
CREATE INDEX stride_organization_membership_active_org_role
    ON stride_organization_memberships_current (organization_id, role, person_id)
    WHERE status = 'active';

CREATE FUNCTION stride_lock_and_assign_organization_slot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    selected_slot smallint;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.membership_id <> OLD.membership_id OR
           NEW.organization_id <> OLD.organization_id OR
           NEW.person_id <> OLD.person_id THEN
            RAISE EXCEPTION 'current organization membership identity is immutable';
        END IF;
        IF NEW.revision <= OLD.revision THEN
            RAISE EXCEPTION 'current organization membership revision must advance';
        END IF;
        IF NOT EXISTS (
            SELECT 1
              FROM stride_organization_membership_revisions next_revision
             WHERE next_revision.membership_id = NEW.membership_id
               AND next_revision.revision = NEW.revision
               AND next_revision.supersedes_revision = OLD.revision
        ) THEN
            RAISE EXCEPTION 'current organization membership must bind a revision that supersedes the exact prior revision';
        END IF;
    END IF;

    INSERT INTO stride_person_organization_capacity_locks (person_id)
    VALUES (NEW.person_id)
    ON CONFLICT (person_id) DO NOTHING;
    PERFORM 1 FROM stride_person_organization_capacity_locks
     WHERE person_id = NEW.person_id
     FOR UPDATE;

    IF NEW.status = 'active' THEN
        selected_slot := NEW.active_slot;
        IF selected_slot IS NULL THEN
            SELECT candidate.slot
              INTO selected_slot
              FROM generate_series(1, 3) AS candidate(slot)
             WHERE NOT EXISTS (
                 SELECT 1
                   FROM stride_organization_memberships_current current_membership
                  WHERE current_membership.person_id = NEW.person_id
                    AND current_membership.status = 'active'
                    AND current_membership.active_slot = candidate.slot
                    AND current_membership.membership_id <> NEW.membership_id
             )
             ORDER BY candidate.slot
             LIMIT 1;
        END IF;
        IF selected_slot IS NULL OR selected_slot NOT BETWEEN 1 AND 3 THEN
            RAISE EXCEPTION 'person already has three active organizations';
        END IF;
        IF EXISTS (
            SELECT 1 FROM stride_organization_memberships_current current_membership
             WHERE current_membership.person_id = NEW.person_id
               AND current_membership.status = 'active'
               AND current_membership.active_slot = selected_slot
               AND current_membership.membership_id <> NEW.membership_id
        ) THEN
            RAISE EXCEPTION 'active organization slot is already occupied';
        END IF;
        NEW.active_slot := selected_slot;
    ELSE
        NEW.active_slot := NULL;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER stride_organization_membership_capacity_gate
BEFORE INSERT OR UPDATE ON stride_organization_memberships_current
FOR EACH ROW EXECUTE FUNCTION stride_lock_and_assign_organization_slot();

-- Deferred owner checks let create+owner and owner-transfer transactions use
-- either statement order while still making an ownerless commit impossible.
-- Locking the organization row serializes concurrent final-owner departures.
CREATE FUNCTION stride_require_active_organization_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    target_organization_id text;
    target_status text;
BEGIN
    target_organization_id := CASE
        WHEN TG_TABLE_NAME = 'stride_organizations' THEN NEW.organization_id
        ELSE COALESCE(NEW.organization_id, OLD.organization_id)
    END;
    SELECT status INTO target_status
      FROM stride_organizations
     WHERE organization_id = target_organization_id
     FOR UPDATE;
    IF target_status = 'active' AND NOT EXISTS (
        SELECT 1
          FROM stride_organization_memberships_current current_membership
         WHERE current_membership.organization_id = target_organization_id
           AND current_membership.status = 'active'
           AND current_membership.role = 'owner'
    ) THEN
        RAISE EXCEPTION 'active organization requires at least one active owner';
    END IF;
    IF TG_TABLE_NAME = 'stride_organizations' THEN
        IF TG_OP = 'INSERT' AND NOT EXISTS (
            SELECT 1
              FROM stride_organization_memberships_current current_membership
             WHERE current_membership.organization_id = target_organization_id
               AND current_membership.person_id = NEW.creator_person_id
               AND current_membership.status = 'active'
               AND current_membership.role = 'owner'
        ) THEN
            RAISE EXCEPTION 'organization creation requires the creator owner membership atomically';
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER stride_organization_owner_on_organization
AFTER INSERT OR UPDATE ON stride_organizations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION stride_require_active_organization_owner();

CREATE CONSTRAINT TRIGGER stride_organization_owner_on_membership
AFTER INSERT OR UPDATE OR DELETE ON stride_organization_memberships_current
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION stride_require_active_organization_owner();

CREATE TABLE stride_organization_join_requests (
    request_id text PRIMARY KEY,
    revision bigint NOT NULL CHECK (revision > 0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    status text NOT NULL CHECK (status IN ('pending', 'approved', 'denied', 'cancelled', 'expired')),
    requested_at timestamptz NOT NULL,
    expires_at timestamptz,
    decided_at timestamptz,
    decided_by_membership_id text,
    decided_by_membership_revision bigint,
    approved_membership_id text,
    approved_membership_revision bigint,
    reason_code text,
    updated_at timestamptz NOT NULL,
    CHECK (request_id = btrim(request_id) AND request_id <> ''),
    CHECK (expires_at IS NULL OR expires_at > requested_at),
    CHECK ((status = 'pending' AND decided_at IS NULL AND decided_by_membership_id IS NULL AND decided_by_membership_revision IS NULL AND approved_membership_id IS NULL AND approved_membership_revision IS NULL) OR
           (status IN ('approved', 'denied') AND decided_at IS NOT NULL AND decided_by_membership_id IS NOT NULL AND decided_by_membership_revision IS NOT NULL) OR
           (status IN ('cancelled', 'expired') AND decided_at IS NOT NULL AND approved_membership_id IS NULL AND approved_membership_revision IS NULL)),
    CHECK ((status = 'approved') = (approved_membership_id IS NOT NULL AND approved_membership_revision IS NOT NULL)),
    CHECK (reason_code IS NULL OR (reason_code = btrim(reason_code) AND reason_code <> '' AND char_length(reason_code) <= 96)),
    FOREIGN KEY (decided_by_membership_id, decided_by_membership_revision)
        REFERENCES stride_organization_membership_revisions (membership_id, revision)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (approved_membership_id, approved_membership_revision)
        REFERENCES stride_organization_membership_revisions (membership_id, revision)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE UNIQUE INDEX stride_organization_join_request_one_pending
    ON stride_organization_join_requests (organization_id, person_id)
    WHERE status = 'pending';

CREATE FUNCTION stride_validate_organization_join_decision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    decider stride_organization_memberships_current%ROWTYPE;
    approved_membership stride_organization_memberships_current%ROWTYPE;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.revision <= OLD.revision THEN
        RAISE EXCEPTION 'join request revision must advance';
    END IF;
    IF NEW.status IN ('approved', 'denied') THEN
        SELECT * INTO decider
          FROM stride_organization_memberships_current
         WHERE membership_id = NEW.decided_by_membership_id;
        IF decider.membership_id IS NULL OR decider.revision <> NEW.decided_by_membership_revision OR
           decider.organization_id <> NEW.organization_id OR decider.status <> 'active' OR
           decider.role NOT IN ('owner', 'admin') THEN
            RAISE EXCEPTION 'join decision requires the exact current owner/admin membership';
        END IF;
    END IF;
    IF NEW.status = 'approved' THEN
        SELECT * INTO approved_membership
          FROM stride_organization_memberships_current
         WHERE membership_id = NEW.approved_membership_id;
        IF approved_membership.membership_id IS NULL OR
           approved_membership.revision <> NEW.approved_membership_revision OR
           approved_membership.organization_id <> NEW.organization_id OR
           approved_membership.person_id <> NEW.person_id OR
           approved_membership.status <> 'active' THEN
            RAISE EXCEPTION 'approved join request requires the exact active membership';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER stride_organization_join_decision_gate
AFTER INSERT OR UPDATE ON stride_organization_join_requests
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION stride_validate_organization_join_decision();

-- Session subjects are opaque digests. Exact membership revision and session
-- revision bind tenant authority; stale membership pointers cannot be selected.
CREATE TABLE stride_active_organization_sessions (
    session_subject_digest bytea PRIMARY KEY CHECK (octet_length(session_subject_digest) = 32),
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL CHECK (membership_revision > 0),
    session_revision bigint NOT NULL CHECK (session_revision > 0),
    status text NOT NULL CHECK (status IN ('active', 'invalidated', 'expired')),
    bound_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    invalidated_at timestamptz,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (membership_id, membership_revision)
        REFERENCES stride_organization_membership_revisions (membership_id, revision),
    CHECK (expires_at > bound_at),
    CHECK ((status = 'active' AND invalidated_at IS NULL) OR
           (status = 'invalidated' AND invalidated_at IS NOT NULL AND invalidated_at >= bound_at) OR
           (status = 'expired' AND invalidated_at IS NOT NULL AND invalidated_at >= expires_at))
);
CREATE INDEX stride_active_organization_session_membership
    ON stride_active_organization_sessions (membership_id, membership_revision, status);

CREATE FUNCTION stride_validate_active_organization_session()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    membership stride_organization_memberships_current%ROWTYPE;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.session_revision <= OLD.session_revision THEN
        RAISE EXCEPTION 'active organization session revision must advance';
    END IF;
    IF NEW.status = 'active' THEN
        SELECT * INTO membership
          FROM stride_organization_memberships_current
         WHERE membership_id = NEW.membership_id;
        IF membership.membership_id IS NULL OR membership.revision <> NEW.membership_revision OR
           membership.person_id <> NEW.person_id OR membership.organization_id <> NEW.organization_id OR
           membership.status <> 'active' THEN
            RAISE EXCEPTION 'active organization session requires the exact current membership';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_active_organization_session_gate
BEFORE INSERT OR UPDATE ON stride_active_organization_sessions
FOR EACH ROW EXECUTE FUNCTION stride_validate_active_organization_session();

CREATE TABLE stride_organization_idempotency_keys (
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    operation text NOT NULL CHECK (operation IN ('create', 'request_join', 'approve_join', 'deny_join', 'cancel_join', 'expire_join', 'switch', 'role_change', 'transfer_owner', 'leave', 'revoke')),
    idempotency_digest bytea NOT NULL CHECK (octet_length(idempotency_digest) = 32),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    result_kind text NOT NULL CHECK (result_kind IN ('organization', 'join_request', 'membership', 'session')),
    result_id text NOT NULL,
    result_revision bigint NOT NULL CHECK (result_revision > 0),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (actor_person_id, operation, idempotency_digest),
    CHECK (result_id = btrim(result_id) AND result_id <> ''),
    CHECK (expires_at > created_at)
);

CREATE TABLE stride_organization_audit_events (
    event_id text PRIMARY KEY,
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    event_type text NOT NULL CHECK (event_type IN ('create', 'request', 'approve', 'deny', 'cancel', 'expire', 'switch', 'role_change', 'transfer', 'leave', 'revoke')),
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    subject_person_id text REFERENCES stride_person_principals(person_id),
    membership_id text,
    request_id text,
    prior_revision bigint,
    new_revision bigint NOT NULL CHECK (new_revision > 0),
    reason_code text,
    correlation_digest bytea NOT NULL CHECK (octet_length(correlation_digest) = 32),
    idempotency_digest bytea NOT NULL CHECK (octet_length(idempotency_digest) = 32),
    audit jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL,
    CHECK (event_id = btrim(event_id) AND event_id <> ''),
    CHECK (membership_id IS NULL OR (membership_id = btrim(membership_id) AND membership_id <> '')),
    CHECK (request_id IS NULL OR (request_id = btrim(request_id) AND request_id <> '')),
    CHECK (prior_revision IS NULL OR (prior_revision > 0 AND prior_revision < new_revision)),
    CHECK (reason_code IS NULL OR (reason_code = btrim(reason_code) AND reason_code <> '' AND char_length(reason_code) <= 96)),
    CHECK (jsonb_typeof(audit) = 'object'),
    CHECK (NOT stride_jsonb_has_forbidden_key(audit, ARRAY['body', 'text', 'audio', 'email', 'secret', 'token', 'api_key', 'authorization', 'credential', 'credentials', 'password', 'cookie']))
);
CREATE INDEX stride_organization_audit_timeline
    ON stride_organization_audit_events (organization_id, created_at, event_id);

CREATE FUNCTION stride_organization_audit_is_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'organization audit events are immutable';
END;
$$;
CREATE TRIGGER stride_organization_audit_immutable_gate
BEFORE UPDATE OR DELETE ON stride_organization_audit_events
FOR EACH ROW EXECUTE FUNCTION stride_organization_audit_is_immutable();

-- Organization-visible coworker fields are a separate, revision-bound safe
-- projection. They cannot carry global profile biography, contact data, mind
-- context, contribution bodies, or arbitrary JSON.
CREATE TABLE stride_organization_member_profile_revisions (
    profile_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL CHECK (membership_revision > 0),
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    title text,
    team text,
    joined_at timestamptz NOT NULL,
    updater_membership_id text NOT NULL,
    updater_membership_revision bigint NOT NULL CHECK (updater_membership_revision > 0),
    supersedes_revision bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (profile_id, revision),
    UNIQUE (profile_id, revision, organization_id, membership_id, membership_revision, person_id),
    FOREIGN KEY (membership_id, membership_revision)
        REFERENCES stride_organization_membership_revisions(membership_id, revision),
    FOREIGN KEY (updater_membership_id, updater_membership_revision)
        REFERENCES stride_organization_membership_revisions(membership_id, revision),
    CHECK (title IS NULL OR (title=btrim(title) AND title<>'' AND char_length(title)<=160)),
    CHECK (team IS NULL OR (team=btrim(team) AND team<>'' AND char_length(team)<=160)),
    CHECK (supersedes_revision IS NULL OR (supersedes_revision>0 AND supersedes_revision<revision))
);
CREATE TABLE stride_organization_member_profiles_current (
    profile_id text PRIMARY KEY,
    revision bigint NOT NULL,
    organization_id text NOT NULL,
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL,
    person_id text NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (profile_id, revision, organization_id, membership_id, membership_revision, person_id)
        REFERENCES stride_organization_member_profile_revisions
        (profile_id, revision, organization_id, membership_id, membership_revision, person_id)
);
CREATE UNIQUE INDEX stride_organization_member_profile_one_current_membership
    ON stride_organization_member_profiles_current(organization_id, membership_id);

CREATE FUNCTION stride_validate_organization_member_profile_current()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE revision_record stride_organization_member_profile_revisions%ROWTYPE;
BEGIN
    IF TG_OP='UPDATE' AND (NEW.profile_id<>OLD.profile_id OR NEW.organization_id<>OLD.organization_id OR NEW.membership_id<>OLD.membership_id OR NEW.person_id<>OLD.person_id OR NEW.revision<=OLD.revision) THEN
        RAISE EXCEPTION 'organization member profile identity is immutable and revision must advance';
    END IF;
    SELECT * INTO revision_record FROM stride_organization_member_profile_revisions
      WHERE profile_id=NEW.profile_id AND revision=NEW.revision;
    IF NOT EXISTS (SELECT 1 FROM stride_organization_memberships_current m
        WHERE m.membership_id=NEW.membership_id AND m.revision=NEW.membership_revision
          AND m.organization_id=NEW.organization_id AND m.person_id=NEW.person_id AND m.status='active') THEN
        RAISE EXCEPTION 'organization member profile requires exact current active membership';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM stride_organization_memberships_current updater
        WHERE updater.membership_id=revision_record.updater_membership_id
          AND updater.revision=revision_record.updater_membership_revision
          AND updater.organization_id=NEW.organization_id AND updater.status='active'
          AND updater.role IN ('owner','admin')) THEN
        RAISE EXCEPTION 'organization member profile update requires exact current owner/admin membership';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_organization_member_profile_current_gate
BEFORE INSERT OR UPDATE ON stride_organization_member_profiles_current
FOR EACH ROW EXECUTE FUNCTION stride_validate_organization_member_profile_current();

CREATE TRIGGER stride_organization_member_profile_history_immutable
BEFORE UPDATE OR DELETE ON stride_organization_member_profile_revisions
FOR EACH ROW EXECUTE FUNCTION stride_organization_audit_is_immutable();

INSERT INTO stride_feature_switches (feature_key, enabled, revision)
VALUES
    ('person_profile_authority', false, 1),
    ('organization_authority_write', false, 1),
    ('organization_authority_read', false, 1),
    ('active_organization_session', false, 1)
ON CONFLICT (feature_key) DO NOTHING;

COMMIT;
