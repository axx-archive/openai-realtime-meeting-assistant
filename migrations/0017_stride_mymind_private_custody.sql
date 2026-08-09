BEGIN;

-- W5 private custody remains default-off. These tables store authenticated
-- ciphertext and body-free operation evidence only; no plaintext, shared
-- disclosure, automatic-import, or provider-consumer column is representable.
CREATE TABLE stride_mymind_private_custody_envelopes (
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    source_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    source_kind text NOT NULL CHECK (source_kind IN ('preference','reflection','correction')),
    consent_revision bigint NOT NULL CHECK (consent_revision > 0),
    body_digest bytea NOT NULL CHECK (octet_length(body_digest) = 32),
    key_id text NOT NULL,
    key_version bigint NOT NULL CHECK (key_version > 0),
    nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) > 16),
    organization_id text NOT NULL,
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL CHECK (membership_revision > 0),
    session_subject_digest bytea NOT NULL REFERENCES stride_active_organization_sessions(session_subject_digest),
    session_revision bigint NOT NULL CHECK (session_revision > 0),
    authority_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (person_id, source_id),
    CHECK (source_id = btrim(source_id) AND source_id <> ''),
    CHECK (key_id = btrim(key_id) AND key_id <> ''),
    UNIQUE (key_id, key_version, nonce),
    FOREIGN KEY (membership_id, membership_revision)
        REFERENCES stride_organization_membership_revisions (membership_id, revision)
);
CREATE INDEX stride_mymind_private_custody_key
    ON stride_mymind_private_custody_envelopes (person_id, key_id, key_version);

CREATE TABLE stride_mymind_private_operation_receipts (
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    idempotency_key text NOT NULL,
    operation_kind text NOT NULL CHECK (operation_kind IN ('put','forget','rotate','delete')),
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint) = 32),
    source_id text,
    source_revision bigint CHECK (source_revision IS NULL OR source_revision > 0),
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL CHECK (membership_revision > 0),
    session_subject_digest bytea NOT NULL REFERENCES stride_active_organization_sessions(session_subject_digest),
    session_revision bigint NOT NULL CHECK (session_revision > 0),
    recorded_at timestamptz NOT NULL,
    organization_id text NOT NULL,
    authority_at timestamptz NOT NULL,
    PRIMARY KEY (person_id, idempotency_key),
    FOREIGN KEY (membership_id, membership_revision)
        REFERENCES stride_organization_membership_revisions (membership_id, revision),
    CHECK (idempotency_key = btrim(idempotency_key) AND idempotency_key <> ''),
    CHECK ((source_id IS NULL) = (source_revision IS NULL))
);

CREATE TABLE stride_mymind_private_deletion_journals (
    person_id text PRIMARY KEY REFERENCES stride_person_principals(person_id),
    idempotency_key text NOT NULL,
    destruction_operation_id text NOT NULL UNIQUE,
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint) = 32),
    phase text NOT NULL CHECK (phase IN ('prepared','records_removed','keys_destroyed','completed')),
    source_manifest_digest bytea NOT NULL CHECK (octet_length(source_manifest_digest) = 32),
    key_refs jsonb NOT NULL,
    key_refs_digest bytea NOT NULL CHECK (octet_length(key_refs_digest) = 32),
    key_destruction_receipt_id text,
    organization_id text NOT NULL,
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL CHECK (membership_revision > 0),
    session_subject_digest bytea NOT NULL REFERENCES stride_active_organization_sessions(session_subject_digest),
    session_revision bigint NOT NULL CHECK (session_revision > 0),
    authority_at timestamptz NOT NULL,
    started_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
	CHECK (destruction_operation_id = btrim(destruction_operation_id) AND destruction_operation_id <> ''),
    CHECK (updated_at >= started_at),
    CHECK (jsonb_typeof(key_refs) = 'array'),
    CHECK (key_refs_digest = sha256(convert_to(key_refs::text,'UTF8'))),
    CHECK (NOT stride_jsonb_has_forbidden_key(key_refs, ARRAY['body','text','ciphertext','nonce','secret','material','email','token','authorization'])),
    CHECK ((phase IN ('prepared','records_removed') AND key_destruction_receipt_id IS NULL) OR
           (phase IN ('keys_destroyed','completed') AND key_destruction_receipt_id IS NOT NULL)),
    FOREIGN KEY (membership_id, membership_revision)
        REFERENCES stride_organization_membership_revisions (membership_id, revision)
);

CREATE TABLE stride_mymind_private_key_destruction_receipts (
    receipt_id text PRIMARY KEY,
    destruction_operation_id text NOT NULL UNIQUE,
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    scope text NOT NULL CHECK (scope IN ('source','person')),
    source_id text,
    source_revision bigint,
    source_envelope_digest bytea,
    key_refs_digest bytea NOT NULL CHECK (octet_length(key_refs_digest) = 32),
    evidence_key_id text NOT NULL,
    evidence_key_version bigint NOT NULL CHECK (evidence_key_version > 0),
    destroyed_at timestamptz NOT NULL,
    evidence_mac bytea NOT NULL CHECK (octet_length(evidence_mac) = 32),
    verification_contract text NOT NULL CHECK (verification_contract = 'managed_keyring_v1'),
    verification_receipt_digest bytea NOT NULL CHECK (octet_length(verification_receipt_digest) = 32),
    verified boolean NOT NULL CHECK (verified),
    organization_id text NOT NULL,
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL CHECK (membership_revision > 0),
    session_subject_digest bytea NOT NULL REFERENCES stride_active_organization_sessions(session_subject_digest),
    session_revision bigint NOT NULL CHECK (session_revision > 0),
    recorded_at timestamptz NOT NULL,
    authority_at timestamptz NOT NULL,
	CHECK (destruction_operation_id = btrim(destruction_operation_id) AND destruction_operation_id <> ''),
    CHECK ((scope = 'source' AND source_id IS NOT NULL AND source_revision > 0 AND octet_length(source_envelope_digest)=32) OR (scope = 'person' AND source_id IS NULL AND source_revision IS NULL AND source_envelope_digest IS NULL)),
    FOREIGN KEY (membership_id, membership_revision)
        REFERENCES stride_organization_membership_revisions (membership_id, revision)
);

ALTER TABLE stride_mymind_private_deletion_journals
    ADD FOREIGN KEY (key_destruction_receipt_id)
        REFERENCES stride_mymind_private_key_destruction_receipts(receipt_id);

CREATE TABLE stride_mymind_private_source_tombstones (
    person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    source_id text NOT NULL,
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    source_envelope_digest bytea NOT NULL CHECK (octet_length(source_envelope_digest)=32),
    key_refs jsonb NOT NULL CHECK (jsonb_typeof(key_refs)='array'),
    key_refs_digest bytea NOT NULL CHECK (octet_length(key_refs_digest)=32),
    deletion_high_water bigint NOT NULL CHECK (deletion_high_water > 0),
    destruction_operation_id text NOT NULL UNIQUE,
    key_destruction_receipt_id text NOT NULL REFERENCES stride_mymind_private_key_destruction_receipts(receipt_id),
    organization_id text NOT NULL,
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL CHECK (membership_revision > 0),
    session_subject_digest bytea NOT NULL REFERENCES stride_active_organization_sessions(session_subject_digest),
    session_revision bigint NOT NULL CHECK (session_revision > 0),
    authority_at timestamptz NOT NULL,
    forgotten_at timestamptz NOT NULL,
	CHECK (destruction_operation_id = btrim(destruction_operation_id) AND destruction_operation_id <> ''),
    PRIMARY KEY (person_id, source_id),
    CHECK (key_refs_digest = sha256(convert_to(key_refs::text,'UTF8'))),
    FOREIGN KEY (membership_id, membership_revision)
        REFERENCES stride_organization_membership_revisions (membership_id, revision)
);

CREATE TABLE stride_mymind_private_state_high_water (
    store_id text PRIMARY KEY,
    generation bigint NOT NULL CHECK (generation >= 0),
    payload_digest bytea NOT NULL CHECK (octet_length(payload_digest) = 32),
    state_key_id text NOT NULL,
    state_key_version bigint NOT NULL CHECK (state_key_version > 0),
    updated_at timestamptz NOT NULL
);

CREATE FUNCTION stride_mymind_private_mutation_requires_current_authority()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    membership stride_organization_memberships_current%ROWTYPE;
    session_binding stride_active_organization_sessions%ROWTYPE;
	person_value text;
	organization_value text;
	membership_value text;
	membership_revision_value bigint;
	session_digest_value bytea;
	session_revision_value bigint;
	recorded_value timestamptz;
BEGIN
	IF TG_OP = 'DELETE' THEN
		person_value := OLD.person_id; organization_value := OLD.organization_id;
		membership_value := OLD.membership_id; membership_revision_value := OLD.membership_revision;
		session_digest_value := OLD.session_subject_digest; session_revision_value := OLD.session_revision;
		recorded_value := GREATEST(OLD.authority_at, clock_timestamp());
	ELSE
		person_value := NEW.person_id; organization_value := NEW.organization_id;
		membership_value := NEW.membership_id; membership_revision_value := NEW.membership_revision;
		session_digest_value := NEW.session_subject_digest; session_revision_value := NEW.session_revision;
		recorded_value := GREATEST(NEW.authority_at, clock_timestamp());
	END IF;
    SELECT * INTO membership FROM stride_organization_memberships_current
     WHERE membership_id = membership_value FOR SHARE;
    SELECT * INTO session_binding FROM stride_active_organization_sessions
     WHERE session_subject_digest = session_digest_value FOR SHARE;
    IF membership.membership_id IS NULL OR membership.status <> 'active' OR
       membership.person_id <> person_value OR membership.organization_id <> organization_value OR membership.revision <> membership_revision_value OR
       session_binding.session_subject_digest IS NULL OR session_binding.status <> 'active' OR
       session_binding.expires_at <= recorded_value OR session_binding.person_id <> person_value OR
       session_binding.organization_id <> organization_value OR session_binding.membership_id <> membership_value OR
       session_binding.membership_revision <> membership_revision_value OR
       session_binding.session_revision <> session_revision_value THEN
        RAISE EXCEPTION 'private MyMind operation requires current organization session authority';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER stride_mymind_private_operation_authority_gate
BEFORE INSERT ON stride_mymind_private_operation_receipts
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_mutation_requires_current_authority();

CREATE TRIGGER stride_mymind_private_envelope_authority_gate
BEFORE INSERT OR UPDATE OR DELETE ON stride_mymind_private_custody_envelopes
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_mutation_requires_current_authority();
CREATE FUNCTION stride_mymind_private_envelope_rejects_resurrection()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM stride_mymind_private_source_tombstones
                WHERE person_id = NEW.person_id AND source_id = NEW.source_id) THEN
        RAISE EXCEPTION 'forgotten private MyMind source cannot be resurrected';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_mymind_private_envelope_resurrection_gate
BEFORE INSERT OR UPDATE ON stride_mymind_private_custody_envelopes
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_envelope_rejects_resurrection();
CREATE TRIGGER stride_mymind_private_journal_authority_gate
BEFORE INSERT OR UPDATE OR DELETE ON stride_mymind_private_deletion_journals
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_mutation_requires_current_authority();
CREATE TRIGGER stride_mymind_private_destruction_authority_gate
BEFORE INSERT ON stride_mymind_private_key_destruction_receipts
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_mutation_requires_current_authority();
CREATE TRIGGER stride_mymind_private_tombstone_authority_gate
BEFORE INSERT ON stride_mymind_private_source_tombstones
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_mutation_requires_current_authority();

CREATE FUNCTION stride_mymind_private_receipt_immutable()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'private MyMind custody receipts are immutable'; END;
$$;
CREATE TRIGGER stride_mymind_private_operation_receipt_immutable
BEFORE UPDATE OR DELETE ON stride_mymind_private_operation_receipts
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_receipt_immutable();
CREATE TRIGGER stride_mymind_private_destruction_receipt_immutable
BEFORE UPDATE OR DELETE ON stride_mymind_private_key_destruction_receipts
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_receipt_immutable();
CREATE TRIGGER stride_mymind_private_tombstone_immutable
BEFORE UPDATE OR DELETE ON stride_mymind_private_source_tombstones
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_receipt_immutable();

CREATE FUNCTION stride_mymind_private_source_envelope_digest(e stride_mymind_private_custody_envelopes)
RETURNS bytea LANGUAGE sql IMMUTABLE STRICT AS $$
SELECT sha256(convert_to(concat_ws(E'\x1f',e.person_id,e.source_id,e.revision::text,e.source_kind,e.consent_revision::text,encode(e.body_digest,'hex'),e.key_id,e.key_version::text,encode(e.nonce,'hex'),encode(sha256(e.ciphertext),'hex')),'UTF8'));
$$;

CREATE FUNCTION stride_mymind_private_destruction_receipt_verified()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE envelope stride_mymind_private_custody_envelopes%ROWTYPE;
DECLARE expected_refs jsonb;
DECLARE expected_receipt_digest bytea;
BEGIN
	    expected_receipt_digest := sha256(convert_to(concat_ws(E'\x1f',NEW.receipt_id,NEW.destruction_operation_id,NEW.person_id,NEW.scope,COALESCE(NEW.source_id,''),COALESCE(NEW.source_revision::text,''),COALESCE(encode(NEW.source_envelope_digest,'hex'),''),encode(NEW.key_refs_digest,'hex'),NEW.evidence_key_id,NEW.evidence_key_version::text,NEW.destroyed_at::text,encode(NEW.evidence_mac,'hex'),NEW.verification_contract),'UTF8'));
    IF NOT NEW.verified OR NEW.verification_contract <> 'managed_keyring_v1' OR NEW.verification_receipt_digest <> expected_receipt_digest THEN
        RAISE EXCEPTION 'managed cryptographic key-destruction verification required';
    END IF;
    IF NEW.scope='source' THEN
        SELECT * INTO envelope FROM stride_mymind_private_custody_envelopes WHERE person_id=NEW.person_id AND source_id=NEW.source_id FOR SHARE;
        expected_refs := jsonb_build_array(jsonb_build_object('id',envelope.key_id,'version',envelope.key_version));
        IF envelope.person_id IS NULL OR NEW.source_revision<>envelope.revision OR NEW.source_envelope_digest<>stride_mymind_private_source_envelope_digest(envelope) OR NEW.key_refs_digest<>sha256(convert_to(expected_refs::text,'UTF8')) THEN
            RAISE EXCEPTION 'destruction receipt does not bind exact custody envelope';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_mymind_private_destruction_receipt_verified_gate
BEFORE INSERT ON stride_mymind_private_key_destruction_receipts
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_destruction_receipt_verified();

CREATE FUNCTION stride_mymind_private_exact_destruction_receipt()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE receipt stride_mymind_private_key_destruction_receipts%ROWTYPE;
BEGIN
    SELECT * INTO receipt FROM stride_mymind_private_key_destruction_receipts
     WHERE receipt_id = NEW.key_destruction_receipt_id FOR SHARE;
	    IF receipt.receipt_id IS NULL OR NOT receipt.verified OR receipt.person_id <> NEW.person_id OR receipt.destruction_operation_id <> NEW.destruction_operation_id OR
       (TG_TABLE_NAME = 'stride_mymind_private_deletion_journals' AND receipt.scope <> 'person') OR
       (TG_TABLE_NAME = 'stride_mymind_private_deletion_journals' AND receipt.key_refs_digest <> NEW.key_refs_digest) OR
       (TG_TABLE_NAME = 'stride_mymind_private_source_tombstones' AND
        (receipt.scope <> 'source' OR receipt.source_id <> NEW.source_id OR receipt.source_revision<>NEW.source_revision OR receipt.source_envelope_digest<>NEW.source_envelope_digest OR receipt.key_refs_digest<>NEW.key_refs_digest)) THEN
        RAISE EXCEPTION 'exact authenticated MyMind key-destruction receipt required';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_mymind_private_journal_exact_destruction
BEFORE INSERT OR UPDATE ON stride_mymind_private_deletion_journals
FOR EACH ROW WHEN (NEW.key_destruction_receipt_id IS NOT NULL)
EXECUTE FUNCTION stride_mymind_private_exact_destruction_receipt();
CREATE TRIGGER stride_mymind_private_tombstone_exact_destruction
BEFORE INSERT ON stride_mymind_private_source_tombstones
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_exact_destruction_receipt();

CREATE FUNCTION stride_mymind_private_high_water_monotonic()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND
       (NEW.store_id <> OLD.store_id OR NEW.generation <> OLD.generation + 1 OR
        NEW.updated_at <= OLD.updated_at OR NEW.payload_digest = OLD.payload_digest)) THEN
        RAISE EXCEPTION 'private MyMind custody high-water cannot roll back';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_mymind_private_high_water_gate
BEFORE UPDATE OR DELETE ON stride_mymind_private_state_high_water
FOR EACH ROW EXECUTE FUNCTION stride_mymind_private_high_water_monotonic();

INSERT INTO stride_feature_switches(feature_key, enabled, revision)
VALUES ('person_mymind_context', false, 1)
ON CONFLICT (feature_key) DO NOTHING;

COMMIT;
