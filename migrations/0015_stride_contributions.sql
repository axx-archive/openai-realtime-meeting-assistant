BEGIN;

-- Revision-1 contribution authority is additive and body-minimized. It does
-- not activate a reader, writer, worker, provider, or bootstrap record.
CREATE FUNCTION stride_contribution_history_is_immutable()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'contribution authority history is immutable';
END;
$$;

CREATE TABLE stride_contribution_controller_grants (
    grant_id text PRIMARY KEY,
    role text NOT NULL CHECK (role IN ('subject','organization_reviewer','named_party','signing_issuer','person_publisher','outcome_reviewer','drift_controller')),
    organization_id text REFERENCES stride_organizations(organization_id),
    person_id text REFERENCES stride_person_principals(person_id),
    party_id text,
    authority_id text NOT NULL,
    authority_revision bigint NOT NULL CHECK (authority_revision>0),
    policy_revision bigint NOT NULL CHECK (policy_revision>0),
    active boolean NOT NULL DEFAULT false,
    UNIQUE(role,organization_id,person_id,party_id,authority_id,authority_revision,policy_revision),
    CHECK ((role='named_party' AND party_id IS NOT NULL AND person_id IS NULL AND organization_id IS NULL) OR
           (role<>'named_party' AND person_id IS NOT NULL AND party_id IS NULL))
);

CREATE TABLE stride_contribution_claim_revisions (
    claim_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    subject_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    contribution_kind text NOT NULL CHECK (contribution_kind IN ('originated','shaped','reviewed','decided','delivered')),
    problem_class text NOT NULL,
    outcome_class text NOT NULL,
    source_refs jsonb NOT NULL,
    evidence_manifest_digest bytea NOT NULL CHECK (octet_length(evidence_manifest_digest)=32),
    attribution_method text NOT NULL CHECK (attribution_method IN ('source_observed','subject_submitted','reviewer_submitted')),
    acl_revision bigint NOT NULL CHECK (acl_revision > 0),
    consent_revision bigint NOT NULL CHECK (consent_revision > 0),
    purge_generation bigint NOT NULL CHECK (purge_generation >= 0),
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    mutation_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    mutation_authority_id text NOT NULL,
    mutation_authority_revision bigint NOT NULL CHECK (mutation_authority_revision>0),
    state text NOT NULL CHECK (state IN ('candidate','subject_review','disputed','verified','revalidation_required','revoked','superseded')),
    subject_review_person_id text REFERENCES stride_person_principals(person_id),
    subject_review_authority_id text,
    subject_review_authority_revision bigint,
    organization_review_person_id text REFERENCES stride_person_principals(person_id),
    organization_review_authority_id text,
    organization_review_authority_revision bigint,
    supersedes_claim_id text,
    supersedes_claim_revision bigint,
    state_changed_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (claim_id, revision),
    UNIQUE (claim_id, revision, organization_id, subject_person_id, state, acl_revision, consent_revision, purge_generation, evidence_manifest_digest),
    CHECK (jsonb_typeof(source_refs)='array' AND jsonb_array_length(source_refs)>0),
    CHECK (NOT stride_jsonb_has_forbidden_key(source_refs, ARRAY['body','text','audio','email','secret','token','api_key','authorization','credential','credentials','password','cookie'])),
    CHECK ((state IN ('verified','revalidation_required','superseded')) =
           (subject_review_person_id IS NOT NULL AND subject_review_authority_id IS NOT NULL AND subject_review_authority_revision > 0 AND
            organization_review_person_id IS NOT NULL AND organization_review_authority_id IS NOT NULL AND organization_review_authority_revision > 0)),
    CHECK (subject_review_person_id IS NULL OR subject_review_person_id=subject_person_id),
    CHECK (state<>'revoked' OR (organization_review_person_id IS NOT NULL AND organization_review_authority_id IS NOT NULL AND organization_review_authority_revision > 0)),
    CHECK ((revision>1)=(supersedes_claim_id IS NOT NULL AND supersedes_claim_revision IS NOT NULL)),
    CHECK (supersedes_claim_revision IS NULL OR supersedes_claim_revision > 0)
);
CREATE TRIGGER stride_contribution_claim_history_immutable
BEFORE UPDATE OR DELETE ON stride_contribution_claim_revisions
FOR EACH ROW EXECUTE FUNCTION stride_contribution_history_is_immutable();

CREATE TABLE stride_contribution_claims_current (
    claim_id text PRIMARY KEY,
    revision bigint NOT NULL,
    organization_id text NOT NULL,
    subject_person_id text NOT NULL,
    state text NOT NULL,
    acl_revision bigint NOT NULL,
    consent_revision bigint NOT NULL,
    purge_generation bigint NOT NULL,
    evidence_manifest_digest bytea NOT NULL,
    eligibility_fence_generation bigint NOT NULL DEFAULT 0 CHECK (eligibility_fence_generation >= 0),
    eligibility_fenced_at timestamptz,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (claim_id,revision,organization_id,subject_person_id,state,acl_revision,consent_revision,purge_generation,evidence_manifest_digest)
      REFERENCES stride_contribution_claim_revisions
      (claim_id,revision,organization_id,subject_person_id,state,acl_revision,consent_revision,purge_generation,evidence_manifest_digest),
    CHECK ((state='revalidation_required')=(eligibility_fenced_at IS NOT NULL) OR state IN ('revoked','superseded')),
    CHECK ((eligibility_fenced_at IS NULL)=(eligibility_fence_generation=0))
);

CREATE TABLE stride_contribution_fence_receipts (
    claim_id text NOT NULL,
    claim_revision bigint NOT NULL,
    fence_generation bigint NOT NULL CHECK (fence_generation > 0),
    reason text NOT NULL CHECK (reason IN ('source_revision','acl_revision','consent_revision','purge_generation','evidence_manifest','revoked','superseded')),
    source_acl_revision bigint NOT NULL,
    source_consent_revision bigint NOT NULL,
    source_purge_generation bigint NOT NULL,
    fenced_at timestamptz NOT NULL,
    PRIMARY KEY (claim_id,fence_generation),
    FOREIGN KEY (claim_id,claim_revision) REFERENCES stride_contribution_claim_revisions(claim_id,revision)
);
CREATE TRIGGER stride_contribution_fence_receipt_immutable
BEFORE UPDATE OR DELETE ON stride_contribution_fence_receipts
FOR EACH ROW EXECUTE FUNCTION stride_contribution_history_is_immutable();

CREATE FUNCTION stride_validate_contribution_claim_current()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE reason_code text;
        actor_role text;
BEGIN
    IF TG_OP='INSERT' THEN
        IF NEW.revision<>1 OR NEW.state<>'candidate' OR NEW.eligibility_fence_generation<>0 OR NEW.eligibility_fenced_at IS NOT NULL THEN
            RAISE EXCEPTION 'initial contribution current must be an unfenced candidate revision 1';
        END IF;
    ELSE
        IF NEW.claim_id<>OLD.claim_id OR NEW.organization_id<>OLD.organization_id OR NEW.subject_person_id<>OLD.subject_person_id OR NEW.revision<>OLD.revision+1 THEN
            RAISE EXCEPTION 'contribution claim current identity is immutable and revision must advance';
        END IF;
        IF NOT EXISTS (SELECT 1 FROM stride_contribution_claim_revisions r WHERE r.claim_id=NEW.claim_id AND r.revision=NEW.revision AND r.supersedes_claim_id=OLD.claim_id AND r.supersedes_claim_revision=OLD.revision) THEN
            RAISE EXCEPTION 'contribution claim must supersede the exact current revision';
        END IF;
        IF NOT ((OLD.state='candidate' AND NEW.state IN ('subject_review','revoked')) OR
                (OLD.state='subject_review' AND NEW.state IN ('disputed','verified','revoked')) OR
                (OLD.state='disputed' AND NEW.state IN ('subject_review','revoked')) OR
                (OLD.state='verified' AND NEW.state IN ('revalidation_required','revoked','superseded')) OR
                (OLD.state='revalidation_required' AND NEW.state IN ('verified','revoked','superseded'))) THEN
            RAISE EXCEPTION 'invalid contribution claim state transition';
        END IF;
        IF NEW.acl_revision<>OLD.acl_revision OR NEW.consent_revision<>OLD.consent_revision OR
           NEW.purge_generation<>OLD.purge_generation OR NEW.evidence_manifest_digest<>OLD.evidence_manifest_digest THEN
            IF OLD.state='verified' AND NEW.state<>'revalidation_required' THEN
                RAISE EXCEPTION 'source authority drift requires synchronous revalidation fence';
            END IF;
            IF NEW.eligibility_fence_generation<=OLD.eligibility_fence_generation OR NEW.eligibility_fenced_at IS NULL THEN
                RAISE EXCEPTION 'source authority drift must advance the fence generation';
            END IF;
            reason_code := CASE WHEN NEW.acl_revision<>OLD.acl_revision THEN 'acl_revision'
                                WHEN NEW.consent_revision<>OLD.consent_revision THEN 'consent_revision'
                                WHEN NEW.purge_generation<>OLD.purge_generation THEN 'purge_generation'
                                ELSE 'evidence_manifest' END;
        ELSIF NEW.state IN ('revalidation_required','revoked','superseded') AND OLD.state<>NEW.state THEN
            IF NEW.eligibility_fence_generation<=OLD.eligibility_fence_generation OR NEW.eligibility_fenced_at IS NULL THEN
                RAISE EXCEPTION 'ineligible contribution must advance the fence generation';
            END IF;
            reason_code := CASE WHEN NEW.state='revoked' THEN 'revoked' WHEN NEW.state='superseded' THEN 'superseded' ELSE 'source_revision' END;
        END IF;
    END IF;
    actor_role := CASE WHEN NEW.state IN ('subject_review','disputed') THEN 'subject'
                       WHEN NEW.state='revalidation_required' THEN 'drift_controller'
                       ELSE 'organization_reviewer' END;
    IF NOT EXISTS (
        SELECT 1 FROM stride_contribution_claim_revisions r
        JOIN stride_contribution_controller_grants g ON g.person_id=r.mutation_person_id AND g.authority_id=r.mutation_authority_id AND g.authority_revision=r.mutation_authority_revision AND g.policy_revision=r.policy_revision
        WHERE r.claim_id=NEW.claim_id AND r.revision=NEW.revision AND g.active AND g.role=actor_role
          AND (actor_role='subject' AND g.person_id=NEW.subject_person_id OR actor_role<>'subject' AND g.organization_id=NEW.organization_id)) THEN
        RAISE EXCEPTION 'contribution claim revision lacks exact active controller grant';
    END IF;
    IF reason_code IS NOT NULL THEN
        INSERT INTO stride_contribution_fence_receipts
          (claim_id,claim_revision,fence_generation,reason,source_acl_revision,source_consent_revision,source_purge_generation,fenced_at)
        VALUES (NEW.claim_id,NEW.revision,NEW.eligibility_fence_generation,reason_code,NEW.acl_revision,NEW.consent_revision,NEW.purge_generation,NEW.eligibility_fenced_at);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_contribution_claim_current_gate
BEFORE INSERT OR UPDATE ON stride_contribution_claims_current
FOR EACH ROW EXECUTE FUNCTION stride_validate_contribution_claim_current();

CREATE TABLE stride_field_release_approval_revisions (
    approval_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    subject_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    attestation_id text NOT NULL,
    attestation_revision bigint NOT NULL CHECK (attestation_revision>0),
    field_key text NOT NULL CHECK (field_key IN ('category','contribution_role','coarse_date','issuer','customer','collaborator','project','artifact','excerpt','metric','outcome')),
    field_value_digest bytea NOT NULL CHECK (octet_length(field_value_digest)=32),
    source_ref jsonb NOT NULL,
    source_consent_revision bigint NOT NULL CHECK (source_consent_revision>0),
    source_acl_revision bigint NOT NULL CHECK (source_acl_revision>0),
    source_purge_generation bigint NOT NULL CHECK (source_purge_generation>=0),
    visibility text NOT NULL CHECK (visibility IN ('private','signed_in_network','exact_link')),
    required_party_ids text[] NOT NULL,
    approver_role text NOT NULL CHECK (approver_role IN ('subject','organization','named_party')),
    approver_party_id text,
    controller_principal_id text NOT NULL,
    controller_authority_id text NOT NULL,
    controller_authority_revision bigint NOT NULL CHECK (controller_authority_revision>0),
    policy_revision bigint NOT NULL CHECK (policy_revision>0),
    state text NOT NULL CHECK (state IN ('pending','approved','denied','withdrawn','expired','superseded')),
    approved_at timestamptz,
    expires_at timestamptz,
    state_changed_at timestamptz NOT NULL,
    supersedes_revision bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (approval_id,revision),
    UNIQUE (approval_id,revision,attestation_id,attestation_revision,field_key,field_value_digest,subject_person_id,state),
    CHECK ((approver_role='named_party' AND cardinality(required_party_ids)>0 AND approver_party_id=ANY(required_party_ids)) OR
           (approver_role IN ('subject','organization') AND cardinality(required_party_ids)=0 AND approver_party_id IS NULL)),
    CHECK (approver_role<>'subject' OR controller_principal_id=subject_person_id),
    CHECK (jsonb_typeof(source_ref)='object' AND NOT stride_jsonb_has_forbidden_key(source_ref,ARRAY['body','text','audio','email','secret','token','api_key','authorization','credential','credentials','password','cookie'])),
    CHECK ((state='approved')=(approved_at IS NOT NULL)),
    CHECK (expires_at IS NULL OR (approved_at IS NOT NULL AND expires_at>approved_at)),
    CHECK (supersedes_revision IS NULL OR supersedes_revision<revision)
);
CREATE TRIGGER stride_field_release_approval_history_immutable
BEFORE UPDATE OR DELETE ON stride_field_release_approval_revisions
FOR EACH ROW EXECUTE FUNCTION stride_contribution_history_is_immutable();

CREATE TABLE stride_field_release_approvals_current (
    approval_id text PRIMARY KEY,
    revision bigint NOT NULL,
    attestation_id text NOT NULL,
    attestation_revision bigint NOT NULL,
    field_key text NOT NULL,
    field_value_digest bytea NOT NULL,
    subject_person_id text NOT NULL,
    state text NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (approval_id,revision,attestation_id,attestation_revision,field_key,field_value_digest,subject_person_id,state)
      REFERENCES stride_field_release_approval_revisions
      (approval_id,revision,attestation_id,attestation_revision,field_key,field_value_digest,subject_person_id,state)
);
CREATE FUNCTION stride_validate_field_release_approval_current()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        IF NEW.revision<>1 OR NEW.state<>'pending' THEN RAISE EXCEPTION 'initial field approval current must be pending revision 1'; END IF;
    ELSE
        IF NEW.approval_id<>OLD.approval_id OR NEW.attestation_id<>OLD.attestation_id OR NEW.field_key<>OLD.field_key OR NEW.subject_person_id<>OLD.subject_person_id OR NEW.revision<>OLD.revision+1 THEN
            RAISE EXCEPTION 'field approval CAS identity is immutable and revision must advance';
        END IF;
        IF NOT EXISTS(SELECT 1 FROM stride_field_release_approval_revisions r WHERE r.approval_id=NEW.approval_id AND r.revision=NEW.revision AND r.supersedes_revision=OLD.revision) THEN
            RAISE EXCEPTION 'field approval must supersede exact current revision';
        END IF;
        IF OLD.state<>'pending' AND NOT (OLD.state='approved' AND NEW.state IN ('withdrawn','expired','superseded')) THEN
            RAISE EXCEPTION 'terminal field approval cannot be revived';
        END IF;
        IF OLD.state='pending' AND NEW.state NOT IN ('approved','denied','withdrawn','expired','superseded') THEN
            RAISE EXCEPTION 'invalid field approval transition';
        END IF;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM stride_field_release_approval_revisions r
        JOIN stride_contribution_controller_grants g ON g.authority_id=r.controller_authority_id AND g.authority_revision=r.controller_authority_revision AND g.policy_revision=r.policy_revision
        WHERE r.approval_id=NEW.approval_id AND r.revision=NEW.revision AND g.active
          AND ((r.approver_role='subject' AND g.role='subject' AND g.person_id=r.subject_person_id AND g.person_id=r.controller_principal_id) OR
               (r.approver_role='organization' AND g.role='organization_reviewer' AND g.organization_id=r.organization_id AND g.person_id=r.controller_principal_id) OR
               (r.approver_role='named_party' AND g.role='named_party' AND g.party_id=r.approver_party_id AND g.party_id=r.controller_principal_id))) THEN
        RAISE EXCEPTION 'field approval lacks exact active subject, organization, or named-party controller grant';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_field_release_approval_current_gate
BEFORE INSERT OR UPDATE ON stride_field_release_approvals_current
FOR EACH ROW EXECUTE FUNCTION stride_validate_field_release_approval_current();

CREATE TABLE stride_contribution_attestation_revisions (
    attestation_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    subject_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    claim_id text NOT NULL,
    claim_revision bigint NOT NULL,
    evidence_manifest_digest bytea NOT NULL CHECK (octet_length(evidence_manifest_digest)=32),
    released_fields_digest bytea NOT NULL CHECK (octet_length(released_fields_digest)=32),
    verification_tier text NOT NULL CHECK (verification_tier IN ('organization_verified_opaque','organization_verified_redacted')),
    issuer_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    issuer_authority_id text NOT NULL,
    issuer_authority_revision bigint NOT NULL CHECK (issuer_authority_revision>0),
    policy_revision bigint NOT NULL CHECK (policy_revision>0),
    signing_key_id text NOT NULL,
    signing_key_revision bigint NOT NULL CHECK (signing_key_revision>0),
    signature_digest bytea NOT NULL CHECK (octet_length(signature_digest)=32),
    state text NOT NULL CHECK (state IN ('active','revoked','superseded')),
    supersedes_attestation_id text,
    supersedes_attestation_revision bigint,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (attestation_id,revision),
    UNIQUE (attestation_id,revision,claim_id,claim_revision,organization_id,subject_person_id,state),
    FOREIGN KEY (claim_id,claim_revision) REFERENCES stride_contribution_claim_revisions(claim_id,revision),
    CHECK ((state='active')=(revoked_at IS NULL)),
    CHECK ((state='superseded')=(supersedes_attestation_id IS NOT NULL AND supersedes_attestation_revision IS NOT NULL))
);
CREATE TRIGGER stride_contribution_attestation_history_immutable
BEFORE UPDATE OR DELETE ON stride_contribution_attestation_revisions
FOR EACH ROW EXECUTE FUNCTION stride_contribution_history_is_immutable();

ALTER TABLE stride_field_release_approval_revisions ADD CONSTRAINT stride_field_approval_attestation_revision
FOREIGN KEY (attestation_id,attestation_revision) REFERENCES stride_contribution_attestation_revisions(attestation_id,revision) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE stride_contribution_attestation_fields (
    attestation_id text NOT NULL,
    attestation_revision bigint NOT NULL,
    field_key text NOT NULL,
    value_digest bytea NOT NULL CHECK (octet_length(value_digest)=32),
    PRIMARY KEY (attestation_id,attestation_revision,field_key),
    FOREIGN KEY (attestation_id,attestation_revision) REFERENCES stride_contribution_attestation_revisions(attestation_id,revision) ON DELETE RESTRICT,
    CHECK (field_key IN ('category','contribution_role','coarse_date','issuer','customer','collaborator','project','artifact','excerpt','metric','outcome'))
);
CREATE TABLE stride_contribution_attestation_field_approvals (
    attestation_id text NOT NULL,
    attestation_revision bigint NOT NULL,
    field_key text NOT NULL,
    approval_id text NOT NULL,
    approval_revision bigint NOT NULL,
    PRIMARY KEY (attestation_id,attestation_revision,field_key,approval_id,approval_revision),
    FOREIGN KEY (attestation_id,attestation_revision,field_key) REFERENCES stride_contribution_attestation_fields(attestation_id,attestation_revision,field_key),
    FOREIGN KEY (approval_id,approval_revision) REFERENCES stride_field_release_approval_revisions(approval_id,revision) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER stride_attestation_field_history_immutable
BEFORE UPDATE OR DELETE ON stride_contribution_attestation_fields
FOR EACH ROW EXECUTE FUNCTION stride_contribution_history_is_immutable();
CREATE TRIGGER stride_attestation_field_approval_history_immutable
BEFORE UPDATE OR DELETE ON stride_contribution_attestation_field_approvals
FOR EACH ROW EXECUTE FUNCTION stride_contribution_history_is_immutable();

CREATE TABLE stride_contribution_attestations_current (
    attestation_id text PRIMARY KEY,
    revision bigint NOT NULL,
    claim_id text NOT NULL,
    claim_revision bigint NOT NULL,
    organization_id text NOT NULL,
    subject_person_id text NOT NULL,
    state text NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (attestation_id,revision,claim_id,claim_revision,organization_id,subject_person_id,state)
      REFERENCES stride_contribution_attestation_revisions(attestation_id,revision,claim_id,claim_revision,organization_id,subject_person_id,state)
);

CREATE FUNCTION stride_validate_attestation_current()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE field_row record;
BEGIN
	IF TG_OP='INSERT' AND (NEW.revision<>1 OR NEW.state<>'active') THEN RAISE EXCEPTION 'initial attestation current must be active revision 1'; END IF;
    IF TG_OP='UPDATE' AND (NEW.attestation_id<>OLD.attestation_id OR NEW.revision<=OLD.revision OR OLD.state<>'active' OR NEW.state NOT IN ('revoked','superseded')) THEN
        RAISE EXCEPTION 'invalid immutable attestation current transition';
    END IF;
	IF NOT EXISTS (SELECT 1 FROM stride_contribution_attestation_revisions r JOIN stride_contribution_controller_grants g ON g.role='signing_issuer' AND g.organization_id=r.organization_id AND g.person_id=r.issuer_person_id AND g.authority_id=r.issuer_authority_id AND g.authority_revision=r.issuer_authority_revision AND g.policy_revision=r.policy_revision AND g.active WHERE r.attestation_id=NEW.attestation_id AND r.revision=NEW.revision) THEN RAISE EXCEPTION 'attestation lacks exact active issuer authority'; END IF;
    IF NEW.state='active' THEN
        IF NOT EXISTS (SELECT 1 FROM stride_contribution_claims_current c WHERE c.claim_id=NEW.claim_id AND c.revision=NEW.claim_revision AND c.state='verified' AND c.subject_person_id=NEW.subject_person_id AND c.organization_id=NEW.organization_id) THEN
            RAISE EXCEPTION 'active attestation requires exact current verified claim';
        END IF;
        IF NOT EXISTS (SELECT 1 FROM stride_contribution_attestation_fields f WHERE f.attestation_id=NEW.attestation_id AND f.attestation_revision=NEW.revision) THEN
            RAISE EXCEPTION 'attestation requires released fields';
        END IF;
        FOR field_row IN SELECT * FROM stride_contribution_attestation_fields f WHERE f.attestation_id=NEW.attestation_id AND f.attestation_revision=NEW.revision LOOP
            IF NOT EXISTS (
                SELECT 1 FROM stride_contribution_attestation_field_approvals ar
                JOIN stride_field_release_approvals_current a ON a.approval_id=ar.approval_id AND a.revision=ar.approval_revision
                JOIN stride_field_release_approval_revisions av ON av.approval_id=a.approval_id AND av.revision=a.revision
                WHERE ar.attestation_id=NEW.attestation_id AND ar.attestation_revision=NEW.revision AND ar.field_key=field_row.field_key
                  AND a.attestation_id=NEW.attestation_id AND a.attestation_revision=NEW.revision
                  AND a.field_key=field_row.field_key AND a.field_value_digest=field_row.value_digest
                  AND a.subject_person_id=NEW.subject_person_id AND a.state='approved' AND av.approver_role='subject') OR
               NOT EXISTS (
                SELECT 1 FROM stride_contribution_attestation_field_approvals ar
                JOIN stride_field_release_approvals_current a ON a.approval_id=ar.approval_id AND a.revision=ar.approval_revision
                JOIN stride_field_release_approval_revisions av ON av.approval_id=a.approval_id AND av.revision=a.revision
                WHERE ar.attestation_id=NEW.attestation_id AND ar.attestation_revision=NEW.revision AND ar.field_key=field_row.field_key
                  AND a.attestation_id=NEW.attestation_id AND a.attestation_revision=NEW.revision
                  AND a.field_key=field_row.field_key AND a.field_value_digest=field_row.value_digest
                  AND a.subject_person_id=NEW.subject_person_id AND a.state='approved' AND av.approver_role='organization') OR
               EXISTS (
                SELECT 1 FROM stride_contribution_attestation_field_approvals ar
                JOIN stride_field_release_approval_revisions av ON av.approval_id=ar.approval_id AND av.revision=ar.approval_revision
                CROSS JOIN LATERAL unnest(av.required_party_ids) required_party
                WHERE ar.attestation_id=NEW.attestation_id AND ar.attestation_revision=NEW.revision AND ar.field_key=field_row.field_key
                  AND av.approver_role='named_party' AND NOT EXISTS (
                    SELECT 1 FROM stride_contribution_attestation_field_approvals ar2
                    JOIN stride_field_release_approvals_current a2 ON a2.approval_id=ar2.approval_id AND a2.revision=ar2.approval_revision
                    JOIN stride_field_release_approval_revisions av2 ON av2.approval_id=a2.approval_id AND av2.revision=a2.revision
                    WHERE ar2.attestation_id=NEW.attestation_id AND ar2.attestation_revision=NEW.revision AND ar2.field_key=field_row.field_key
                      AND a2.state='approved' AND av2.approver_role='named_party' AND av2.approver_party_id=required_party)) THEN
                RAISE EXCEPTION 'released attestation field requires subject, organization, and every named-party approval';
            END IF;
        END LOOP;
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER stride_attestation_current_gate
AFTER INSERT OR UPDATE ON stride_contribution_attestations_current
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stride_validate_attestation_current();

CREATE TABLE stride_published_contribution_revisions (
    publication_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    subject_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    narrative_digest bytea NOT NULL CHECK (octet_length(narrative_digest)=32),
    released_fields_digest bytea NOT NULL CHECK (octet_length(released_fields_digest)=32),
    visibility text NOT NULL CHECK (visibility IN ('private','signed_in_network','exact_link')),
    controller_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    controller_authority_id text NOT NULL,
    controller_authority_revision bigint NOT NULL CHECK (controller_authority_revision>0),
    policy_revision bigint NOT NULL CHECK (policy_revision>0),
    state text NOT NULL CHECK (state IN ('draft','approval_required','published','superseded','withdrawn')),
    state_changed_at timestamptz NOT NULL,
    supersedes_revision bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (publication_id,revision),
    UNIQUE (publication_id,revision,subject_person_id,state),
    CHECK (controller_person_id=subject_person_id),
    CHECK (state='published' OR visibility='private'),
    CHECK (supersedes_revision IS NULL OR supersedes_revision<revision)
);
CREATE TRIGGER stride_published_contribution_history_immutable
BEFORE UPDATE OR DELETE ON stride_published_contribution_revisions
FOR EACH ROW EXECUTE FUNCTION stride_contribution_history_is_immutable();
CREATE TABLE stride_published_contribution_attestation_refs (
    publication_id text NOT NULL,
    publication_revision bigint NOT NULL,
    attestation_id text NOT NULL,
    attestation_revision bigint NOT NULL,
    PRIMARY KEY (publication_id,publication_revision,attestation_id,attestation_revision),
    FOREIGN KEY (publication_id,publication_revision) REFERENCES stride_published_contribution_revisions(publication_id,revision),
    FOREIGN KEY (attestation_id,attestation_revision) REFERENCES stride_contribution_attestation_revisions(attestation_id,revision)
);
CREATE TRIGGER stride_publication_attestation_ref_immutable
BEFORE UPDATE OR DELETE ON stride_published_contribution_attestation_refs
FOR EACH ROW EXECUTE FUNCTION stride_contribution_history_is_immutable();
CREATE TABLE stride_published_contributions_current (
    publication_id text PRIMARY KEY,
    revision bigint NOT NULL,
    subject_person_id text NOT NULL,
    state text NOT NULL,
    visibility text NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (publication_id,revision,subject_person_id,state) REFERENCES stride_published_contribution_revisions(publication_id,revision,subject_person_id,state)
);
CREATE TABLE stride_published_contribution_eligibility (
    publication_id text PRIMARY KEY REFERENCES stride_published_contributions_current(publication_id),
    eligible boolean NOT NULL,
    fence_generation bigint NOT NULL DEFAULT 0 CHECK (fence_generation>=0),
    reason text,
    fenced_at timestamptz,
    updated_at timestamptz NOT NULL,
    CHECK ((eligible AND reason IS NULL AND fenced_at IS NULL) OR (NOT eligible AND reason IS NOT NULL AND fenced_at IS NOT NULL))
);
CREATE TABLE stride_contribution_purge_receipts (
    publication_id text NOT NULL,
    fence_generation bigint NOT NULL CHECK (fence_generation>0),
    subject_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    reason text NOT NULL,
    affected_fields_digest bytea NOT NULL CHECK (octet_length(affected_fields_digest)=32),
    stores jsonb NOT NULL,
    state text NOT NULL CHECK (state IN ('queued','completed','failed_escalated')),
    eligibility_fenced_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL CHECK (recorded_at>=eligibility_fenced_at),
    PRIMARY KEY (publication_id,fence_generation),
    CHECK (jsonb_typeof(stores)='array' AND jsonb_array_length(stores)=13 AND NOT stride_jsonb_has_forbidden_key(stores,ARRAY['body','text','audio','email','secret','token','credential','password','cookie']))
);
CREATE TRIGGER stride_contribution_purge_receipt_immutable
BEFORE UPDATE OR DELETE ON stride_contribution_purge_receipts
FOR EACH ROW EXECUTE FUNCTION stride_contribution_history_is_immutable();

CREATE FUNCTION stride_set_publication_eligibility(publication text, eligible_now boolean, fence_reason text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE subject text; next_generation bigint;
BEGIN
    SELECT subject_person_id INTO subject FROM stride_published_contributions_current WHERE publication_id=publication;
    IF subject IS NULL THEN RETURN; END IF;
    IF eligible_now THEN
        INSERT INTO stride_published_contribution_eligibility(publication_id,eligible,updated_at) VALUES(publication,true,now())
        ON CONFLICT(publication_id) DO UPDATE SET eligible=true,reason=NULL,fenced_at=NULL,updated_at=now();
    ELSE
        INSERT INTO stride_published_contribution_eligibility(publication_id,eligible,fence_generation,reason,fenced_at,updated_at)
        VALUES(publication,false,1,fence_reason,now(),now())
        ON CONFLICT(publication_id) DO UPDATE SET eligible=false,fence_generation=stride_published_contribution_eligibility.fence_generation+1,reason=fence_reason,fenced_at=now(),updated_at=now()
        RETURNING fence_generation INTO next_generation;
        INSERT INTO stride_contribution_purge_receipts(publication_id,fence_generation,subject_person_id,reason,affected_fields_digest,stores,state,eligibility_fenced_at,recorded_at)
        VALUES(publication,next_generation,subject,fence_reason,decode(md5(publication||':'||next_generation::text||':'||fence_reason)||md5(fence_reason||':'||next_generation::text||':'||publication),'hex'),
          '[{"store":"projection","state":"queued"},{"store":"lexical_index","state":"queued"},{"store":"vector_index","state":"queued"},{"store":"reranker_cache","state":"queued"},{"store":"application_cache","state":"queued"},{"store":"cdn","state":"queued"},{"store":"push_queue","state":"queued"},{"store":"job_queue","state":"queued"},{"store":"analytics","state":"queued"},{"store":"audit_log","state":"queued"},{"store":"test_fixture","state":"queued"},{"store":"export","state":"queued"},{"store":"backup_manifest","state":"queued"}]'::jsonb,'queued',now(),now()) ON CONFLICT DO NOTHING;
    END IF;
END;
$$;

CREATE TABLE stride_contribution_retention_policies (
    data_class text PRIMARY KEY,
    retention_mode text NOT NULL CHECK (retention_mode IN ('source_authority','while_current','fixed_days','manifest_until_expiry')),
    retention_days integer CHECK (retention_days IS NULL OR retention_days>0),
    policy_revision bigint NOT NULL CHECK (policy_revision>0),
    enabled boolean NOT NULL DEFAULT false,
    CHECK ((retention_mode='fixed_days')=(retention_days IS NOT NULL))
);
INSERT INTO stride_contribution_retention_policies(data_class,retention_mode,retention_days,policy_revision,enabled) VALUES
 ('private_source','source_authority',NULL,1,false),('active_projection','while_current',NULL,1,false),
 ('approval_audit_signature','fixed_days',2555,1,false),('export','fixed_days',7,1,false),
 ('backup_manifest','manifest_until_expiry',NULL,1,false);

CREATE FUNCTION stride_validate_publication_current()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF TG_OP='INSERT' AND (NEW.revision<>1 OR NEW.state NOT IN ('draft','published')) THEN RAISE EXCEPTION 'invalid initial publication current'; END IF;
    IF TG_OP='UPDATE' THEN
        IF NEW.publication_id<>OLD.publication_id OR NEW.subject_person_id<>OLD.subject_person_id OR NEW.revision<=OLD.revision THEN RAISE EXCEPTION 'publication CAS revision must advance'; END IF;
        IF NOT ((OLD.state='draft' AND NEW.state='approval_required') OR
                (OLD.state='approval_required' AND NEW.state IN ('draft','published','withdrawn')) OR
                (OLD.state='published' AND NEW.state IN ('withdrawn','superseded'))) THEN RAISE EXCEPTION 'invalid publication transition'; END IF;
        IF NOT EXISTS(SELECT 1 FROM stride_published_contribution_revisions r WHERE r.publication_id=NEW.publication_id AND r.revision=NEW.revision AND r.supersedes_revision=OLD.revision) THEN
            RAISE EXCEPTION 'publication must supersede exact current revision';
        END IF;
    END IF;
	IF NOT EXISTS (SELECT 1 FROM stride_published_contribution_revisions r JOIN stride_contribution_controller_grants g ON g.role='person_publisher' AND g.person_id=r.controller_person_id AND g.authority_id=r.controller_authority_id AND g.authority_revision=r.controller_authority_revision AND g.policy_revision=r.policy_revision AND g.active WHERE r.publication_id=NEW.publication_id AND r.revision=NEW.revision) THEN RAISE EXCEPTION 'publication lacks exact active person publisher authority'; END IF;
    IF NEW.state='published' AND (NOT EXISTS (SELECT 1 FROM stride_published_contribution_attestation_refs r WHERE r.publication_id=NEW.publication_id AND r.publication_revision=NEW.revision) OR
		EXISTS (SELECT 1 FROM stride_published_contribution_attestation_refs r LEFT JOIN stride_contribution_attestations_current a ON a.attestation_id=r.attestation_id AND a.revision=r.attestation_revision LEFT JOIN stride_contribution_claims_current c ON c.claim_id=a.claim_id AND c.revision=a.claim_revision WHERE r.publication_id=NEW.publication_id AND r.publication_revision=NEW.revision AND (a.attestation_id IS NULL OR a.subject_person_id<>NEW.subject_person_id OR a.state<>'active' OR c.claim_id IS NULL OR c.state<>'verified')) OR
		EXISTS (SELECT 1 FROM stride_published_contribution_attestation_refs r JOIN stride_contribution_attestation_field_approvals ar ON ar.attestation_id=r.attestation_id AND ar.attestation_revision=r.attestation_revision LEFT JOIN stride_field_release_approvals_current a ON a.approval_id=ar.approval_id AND a.revision=ar.approval_revision WHERE r.publication_id=NEW.publication_id AND r.publication_revision=NEW.revision AND (a.approval_id IS NULL OR a.state<>'approved'))) THEN
        RAISE EXCEPTION 'published contribution requires exact active attestation';
    END IF;
    PERFORM stride_set_publication_eligibility(NEW.publication_id,NEW.state='published',CASE WHEN NEW.state='published' THEN NULL ELSE 'publication_'||NEW.state END);
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER stride_publication_current_gate
AFTER INSERT OR UPDATE ON stride_published_contributions_current
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stride_validate_publication_current();

CREATE FUNCTION stride_fence_publications_from_attestation()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p record;
BEGIN
    IF NEW.state<>'active' THEN
        FOR p IN SELECT DISTINCT publication_id FROM stride_published_contribution_attestation_refs WHERE attestation_id=NEW.attestation_id LOOP
            PERFORM stride_set_publication_eligibility(p.publication_id,false,'attestation_'||NEW.state);
        END LOOP;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER stride_attestation_publication_fence
AFTER INSERT OR UPDATE ON stride_contribution_attestations_current
FOR EACH ROW EXECUTE FUNCTION stride_fence_publications_from_attestation();

CREATE FUNCTION stride_fence_publications_from_approval()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p record;
BEGIN
    IF NEW.state<>'approved' THEN
        FOR p IN SELECT DISTINCT r.publication_id FROM stride_published_contribution_attestation_refs r WHERE r.attestation_id=NEW.attestation_id LOOP
            PERFORM stride_set_publication_eligibility(p.publication_id,false,'field_approval_'||NEW.state);
        END LOOP;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER stride_approval_publication_fence
AFTER INSERT OR UPDATE ON stride_field_release_approvals_current
FOR EACH ROW EXECUTE FUNCTION stride_fence_publications_from_approval();

CREATE FUNCTION stride_fence_publications_from_claim()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p record;
BEGIN
    IF NEW.state<>'verified' THEN
        FOR p IN
            SELECT DISTINCT r.publication_id
            FROM stride_contribution_attestations_current a
            JOIN stride_published_contribution_attestation_refs r ON r.attestation_id=a.attestation_id
            WHERE a.claim_id=NEW.claim_id
        LOOP
            PERFORM stride_set_publication_eligibility(p.publication_id,false,'claim_'||NEW.state);
        END LOOP;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER stride_claim_publication_fence
AFTER INSERT OR UPDATE ON stride_contribution_claims_current
FOR EACH ROW EXECUTE FUNCTION stride_fence_publications_from_claim();

CREATE TABLE stride_agent_influence_receipt_revisions (
    influence_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    subject_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    evidence_refs jsonb NOT NULL,
    reviewer_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    reviewer_authority_id text NOT NULL,
    reviewer_authority_revision bigint NOT NULL CHECK (reviewer_authority_revision>0),
    policy_revision bigint NOT NULL CHECK (policy_revision>0),
    state text NOT NULL CHECK (state IN ('verified','revalidation_required','revoked','superseded')),
    supersedes_revision bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (influence_id,revision),
    UNIQUE (influence_id,revision,organization_id,subject_person_id,state),
    CHECK (jsonb_typeof(evidence_refs)='object' AND NOT stride_jsonb_has_forbidden_key(evidence_refs,ARRAY['body','text','audio','email','secret','token','api_key','authorization','credential','credentials','password','cookie'])),
    CHECK (evidence_refs ?& ARRAY['agentProfile','runtimeRevision','modelRevision','agentRun','agentOutput','humanInteraction','humanAdoption','outcome']),
    CHECK (supersedes_revision IS NULL OR supersedes_revision<revision)
);
CREATE TRIGGER stride_agent_influence_history_immutable
BEFORE UPDATE OR DELETE ON stride_agent_influence_receipt_revisions
FOR EACH ROW EXECUTE FUNCTION stride_contribution_history_is_immutable();
CREATE TABLE stride_agent_influence_receipts_current (
    influence_id text PRIMARY KEY,
    revision bigint NOT NULL,
    organization_id text NOT NULL,
    subject_person_id text NOT NULL,
    state text NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (influence_id,revision,organization_id,subject_person_id,state) REFERENCES stride_agent_influence_receipt_revisions(influence_id,revision,organization_id,subject_person_id,state)
);
CREATE FUNCTION stride_validate_agent_influence_current()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF TG_OP='INSERT' THEN
		IF NEW.revision<>1 OR NEW.state<>'verified' THEN RAISE EXCEPTION 'initial agent influence current must be verified revision 1'; END IF;
		IF NOT EXISTS (SELECT 1 FROM stride_agent_influence_receipt_revisions r JOIN stride_contribution_controller_grants g ON g.role='outcome_reviewer' AND g.organization_id=r.organization_id AND g.person_id=r.reviewer_person_id AND g.authority_id=r.reviewer_authority_id AND g.authority_revision=r.reviewer_authority_revision AND g.policy_revision=r.policy_revision AND g.active WHERE r.influence_id=NEW.influence_id AND r.revision=NEW.revision) THEN RAISE EXCEPTION 'agent influence lacks exact active reviewer authority'; END IF;
	END IF;
	IF NOT EXISTS (SELECT 1 FROM stride_agent_influence_receipt_revisions r JOIN stride_contribution_controller_grants g ON g.role='outcome_reviewer' AND g.organization_id=r.organization_id AND g.person_id=r.reviewer_person_id AND g.authority_id=r.reviewer_authority_id AND g.authority_revision=r.reviewer_authority_revision AND g.policy_revision=r.policy_revision AND g.active WHERE r.influence_id=NEW.influence_id AND r.revision=NEW.revision) THEN RAISE EXCEPTION 'agent influence lacks exact active reviewer authority'; END IF;
    IF TG_OP='UPDATE' THEN
        IF NEW.influence_id<>OLD.influence_id OR NEW.organization_id<>OLD.organization_id OR NEW.subject_person_id<>OLD.subject_person_id OR NEW.revision<=OLD.revision THEN RAISE EXCEPTION 'agent influence CAS revision must advance'; END IF;
        IF NOT EXISTS(SELECT 1 FROM stride_agent_influence_receipt_revisions r WHERE r.influence_id=NEW.influence_id AND r.revision=NEW.revision AND r.supersedes_revision=OLD.revision) THEN RAISE EXCEPTION 'agent influence must supersede exact current revision'; END IF;
        IF OLD.state='verified' AND NEW.state NOT IN ('revalidation_required','revoked','superseded') OR OLD.state='revalidation_required' AND NEW.state NOT IN ('verified','revoked','superseded') OR OLD.state IN ('revoked','superseded') THEN RAISE EXCEPTION 'invalid agent influence transition'; END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_agent_influence_current_gate
BEFORE INSERT OR UPDATE ON stride_agent_influence_receipts_current
FOR EACH ROW EXECUTE FUNCTION stride_validate_agent_influence_current();

CREATE FUNCTION stride_contribution_current_delete_denied()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'contribution current pointers cannot be deleted; use an authorized terminal revision and fence';
END;
$$;
CREATE TRIGGER stride_claim_current_delete_denied BEFORE DELETE ON stride_contribution_claims_current FOR EACH ROW EXECUTE FUNCTION stride_contribution_current_delete_denied();
CREATE TRIGGER stride_approval_current_delete_denied BEFORE DELETE ON stride_field_release_approvals_current FOR EACH ROW EXECUTE FUNCTION stride_contribution_current_delete_denied();
CREATE TRIGGER stride_attestation_current_delete_denied BEFORE DELETE ON stride_contribution_attestations_current FOR EACH ROW EXECUTE FUNCTION stride_contribution_current_delete_denied();
CREATE TRIGGER stride_publication_current_delete_denied BEFORE DELETE ON stride_published_contributions_current FOR EACH ROW EXECUTE FUNCTION stride_contribution_current_delete_denied();
CREATE TRIGGER stride_influence_current_delete_denied BEFORE DELETE ON stride_agent_influence_receipts_current FOR EACH ROW EXECUTE FUNCTION stride_contribution_current_delete_denied();

INSERT INTO stride_feature_switches(feature_key,enabled,revision) VALUES
 ('contribution_candidate_detection',false,1),
 ('contribution_review',false,1),
 ('work_record_private',false,1)
ON CONFLICT(feature_key) DO NOTHING;

COMMIT;
