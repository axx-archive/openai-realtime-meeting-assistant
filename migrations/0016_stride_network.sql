BEGIN;

-- Revision-1 network authority remains data-only and default-off. Searchable
-- eligibility is a separate synchronous fence from immutable profile history.
CREATE FUNCTION stride_network_history_is_immutable()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'network authority history is immutable'; END;
$$;

CREATE FUNCTION stride_network_fields_are_safe(fields_document jsonb)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE field_record jsonb;
BEGIN
    IF jsonb_typeof(fields_document)<>'array' OR jsonb_array_length(fields_document)=0 THEN RETURN false; END IF;
    FOR field_record IN SELECT value FROM jsonb_array_elements(fields_document) LOOP
        IF field_record->>'fieldKey' NOT IN ('display_name','avatar','pronouns','bio','work_mode','open_to','problem_class','outcome_class','contribution_role','coarse_date','issuer','artifact','outcome') THEN RETURN false; END IF;
    END LOOP;
    RETURN true;
END;
$$;

CREATE TABLE stride_network_profile_revisions (
    projection_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    subject_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    publication_id text NOT NULL,
    publication_revision bigint NOT NULL,
    fields jsonb NOT NULL,
    fields_digest bytea NOT NULL CHECK (octet_length(fields_digest)=32),
    discoverability text NOT NULL CHECK (discoverability IN ('unlisted','signed_in_network','exact_link')),
    purge_generation bigint NOT NULL CHECK (purge_generation>=0),
    controller_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    controller_authority_id text NOT NULL,
    controller_authority_revision bigint NOT NULL CHECK (controller_authority_revision>0),
    policy_revision bigint NOT NULL CHECK (policy_revision>0),
    state text NOT NULL CHECK (state IN ('draft','published','paused','off','deleted')),
    state_changed_at timestamptz NOT NULL,
    supersedes_revision bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (projection_id,revision),
    UNIQUE (projection_id,revision,subject_person_id,publication_id,publication_revision,state,discoverability,purge_generation,fields_digest),
    FOREIGN KEY (publication_id,publication_revision) REFERENCES stride_published_contribution_revisions(publication_id,revision),
    CHECK (controller_person_id=subject_person_id),
    CHECK (stride_network_fields_are_safe(fields)),
    CHECK (NOT stride_jsonb_has_forbidden_key(fields,ARRAY['body','text','audio','email','secret','token','api_key','authorization','credential','credentials','password','cookie','mymind','ambientmind','agentmind','score','ranking'])),
    CHECK (state='published' OR discoverability='unlisted'),
    CHECK (supersedes_revision IS NULL OR supersedes_revision<revision)
);
CREATE TRIGGER stride_network_profile_history_immutable
BEFORE UPDATE OR DELETE ON stride_network_profile_revisions
FOR EACH ROW EXECUTE FUNCTION stride_network_history_is_immutable();

CREATE TABLE stride_network_profiles_current (
    projection_id text PRIMARY KEY,
    revision bigint NOT NULL,
    subject_person_id text NOT NULL,
    publication_id text NOT NULL,
    publication_revision bigint NOT NULL,
    state text NOT NULL,
    discoverability text NOT NULL,
    purge_generation bigint NOT NULL,
    fields_digest bytea NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (projection_id,revision,subject_person_id,publication_id,publication_revision,state,discoverability,purge_generation,fields_digest)
      REFERENCES stride_network_profile_revisions(projection_id,revision,subject_person_id,publication_id,publication_revision,state,discoverability,purge_generation,fields_digest)
);
CREATE UNIQUE INDEX stride_network_profile_one_current_person ON stride_network_profiles_current(subject_person_id);
CREATE TABLE stride_network_projection_eligibility (
    projection_id text PRIMARY KEY REFERENCES stride_network_profiles_current(projection_id),
    eligible boolean NOT NULL,
    fence_generation bigint NOT NULL DEFAULT 0 CHECK (fence_generation>=0),
    reason text,
    fenced_at timestamptz,
    updated_at timestamptz NOT NULL,
    CHECK ((eligible AND reason IS NULL AND fenced_at IS NULL) OR (NOT eligible AND reason IS NOT NULL AND fenced_at IS NOT NULL))
);

CREATE TABLE stride_derived_purge_receipts (
    purge_receipt_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    subject_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    trigger_contract_type text NOT NULL,
    trigger_id text NOT NULL,
    trigger_revision bigint NOT NULL CHECK (trigger_revision>0),
    purge_generation bigint NOT NULL CHECK (purge_generation>0),
    affected_fields_digest bytea NOT NULL CHECK (octet_length(affected_fields_digest)=32),
    stores jsonb NOT NULL,
    eligibility_fenced_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL CHECK (recorded_at>=eligibility_fenced_at),
    state text NOT NULL CHECK (state IN ('queued','completed','failed_escalated')),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (purge_receipt_id,revision),
    CHECK (stores='[{"store":"projection","state":"queued","attemptCount":1},{"store":"lexical_index","state":"queued","attemptCount":1},{"store":"vector_index","state":"queued","attemptCount":1},{"store":"reranker_cache","state":"queued","attemptCount":1},{"store":"application_cache","state":"queued","attemptCount":1},{"store":"cdn","state":"queued","attemptCount":1},{"store":"push_queue","state":"queued","attemptCount":1},{"store":"job_queue","state":"queued","attemptCount":1},{"store":"analytics","state":"queued","attemptCount":1},{"store":"audit_log","state":"queued","attemptCount":1},{"store":"test_fixture","state":"queued","attemptCount":1},{"store":"export","state":"queued","attemptCount":1},{"store":"backup_manifest","state":"queued","attemptCount":1}]'::jsonb),
    CHECK (NOT stride_jsonb_has_forbidden_key(stores,ARRAY['body','text','audio','email','secret','token','api_key','authorization','credential','credentials','password','cookie']))
);
CREATE TRIGGER stride_derived_purge_history_immutable
BEFORE UPDATE OR DELETE ON stride_derived_purge_receipts
FOR EACH ROW EXECUTE FUNCTION stride_network_history_is_immutable();

CREATE FUNCTION stride_set_network_eligibility(projection text, eligible_now boolean, fence_reason text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE p stride_network_profiles_current%ROWTYPE; next_generation bigint;
BEGIN
    SELECT * INTO p FROM stride_network_profiles_current WHERE projection_id=projection;
    IF p.projection_id IS NULL THEN RETURN; END IF;
    IF eligible_now THEN
        INSERT INTO stride_network_projection_eligibility(projection_id,eligible,updated_at) VALUES(projection,true,now())
        ON CONFLICT(projection_id) DO UPDATE SET eligible=true,reason=NULL,fenced_at=NULL,updated_at=now();
    ELSE
        INSERT INTO stride_network_projection_eligibility(projection_id,eligible,fence_generation,reason,fenced_at,updated_at)
        VALUES(projection,false,1,fence_reason,now(),now())
        ON CONFLICT(projection_id) DO UPDATE SET eligible=false,fence_generation=stride_network_projection_eligibility.fence_generation+1,reason=fence_reason,fenced_at=now(),updated_at=now()
        RETURNING fence_generation INTO next_generation;
        INSERT INTO stride_derived_purge_receipts
          (purge_receipt_id,revision,subject_person_id,trigger_contract_type,trigger_id,trigger_revision,purge_generation,affected_fields_digest,stores,eligibility_fenced_at,recorded_at,state,created_at)
        VALUES (projection||':fence:'||next_generation,1,p.subject_person_id,'network_profile_projection',projection,p.revision,next_generation,p.fields_digest,
                '[{"store":"projection","state":"queued","attemptCount":1},{"store":"lexical_index","state":"queued","attemptCount":1},{"store":"vector_index","state":"queued","attemptCount":1},{"store":"reranker_cache","state":"queued","attemptCount":1},{"store":"application_cache","state":"queued","attemptCount":1},{"store":"cdn","state":"queued","attemptCount":1},{"store":"push_queue","state":"queued","attemptCount":1},{"store":"job_queue","state":"queued","attemptCount":1},{"store":"analytics","state":"queued","attemptCount":1},{"store":"audit_log","state":"queued","attemptCount":1},{"store":"test_fixture","state":"queued","attemptCount":1},{"store":"export","state":"queued","attemptCount":1},{"store":"backup_manifest","state":"queued","attemptCount":1}]',now(),now(),'queued',now()) ON CONFLICT DO NOTHING;
    END IF;
END;
$$;

CREATE FUNCTION stride_validate_network_profile_current()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE publication_ok boolean;
BEGIN
    IF TG_OP='UPDATE' AND (NEW.projection_id<>OLD.projection_id OR NEW.subject_person_id<>OLD.subject_person_id OR NEW.revision<=OLD.revision) THEN
        RAISE EXCEPTION 'network profile CAS identity is immutable and revision must advance';
    END IF;
    IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM stride_network_profile_revisions r WHERE r.projection_id=NEW.projection_id AND r.revision=NEW.revision AND r.supersedes_revision=OLD.revision) THEN RAISE EXCEPTION 'network profile must supersede exact current revision'; END IF;
    IF TG_OP='UPDATE' AND NOT ((OLD.state='draft' AND NEW.state IN ('published','off','deleted')) OR
       (OLD.state='published' AND NEW.state IN ('paused','off','deleted')) OR
       (OLD.state='paused' AND NEW.state IN ('published','off','deleted')) OR
       (OLD.state='off' AND NEW.state IN ('draft','deleted'))) THEN
        RAISE EXCEPTION 'invalid network profile transition';
    END IF;
    SELECT EXISTS (SELECT 1 FROM stride_published_contributions_current p
      JOIN stride_published_contribution_eligibility e ON e.publication_id=p.publication_id AND e.eligible
      WHERE p.publication_id=NEW.publication_id AND p.revision=NEW.publication_revision AND p.subject_person_id=NEW.subject_person_id AND p.state='published') INTO publication_ok;
    IF NEW.state='published' AND NOT publication_ok THEN RAISE EXCEPTION 'published network projection requires exact eligible published contribution'; END IF;
    PERFORM stride_set_network_eligibility(NEW.projection_id,NEW.state='published' AND publication_ok,
      CASE WHEN NEW.state='published' AND publication_ok THEN NULL ELSE 'projection_'||NEW.state END);
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER stride_network_profile_current_gate
AFTER INSERT OR UPDATE ON stride_network_profiles_current
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stride_validate_network_profile_current();

CREATE FUNCTION stride_fence_network_from_publication()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p record;
BEGIN
    IF NOT NEW.eligible THEN
        FOR p IN SELECT projection_id FROM stride_network_profiles_current WHERE publication_id=NEW.publication_id LOOP
            PERFORM stride_set_network_eligibility(p.projection_id,false,'publication_'||COALESCE(NEW.reason,'ineligible'));
        END LOOP;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER stride_publication_network_fence
AFTER INSERT OR UPDATE ON stride_published_contribution_eligibility
FOR EACH ROW EXECUTE FUNCTION stride_fence_network_from_publication();

CREATE TABLE stride_talent_search_grant_revisions (
    grant_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL,
    searcher_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    administrator_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    administrator_authority_id text NOT NULL,
    administrator_authority_revision bigint NOT NULL CHECK (administrator_authority_revision>0),
    policy_revision bigint NOT NULL CHECK (policy_revision>0),
    state text NOT NULL CHECK (state IN ('active','revoked','expired')),
    granted_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at>granted_at),
    revoked_at timestamptz,
    supersedes_revision bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (grant_id,revision),
    UNIQUE (grant_id,revision,organization_id,membership_id,membership_revision,searcher_person_id,state,expires_at),
    FOREIGN KEY (membership_id,membership_revision) REFERENCES stride_organization_membership_revisions(membership_id,revision),
    CHECK ((state='active')=(revoked_at IS NULL)),
    CHECK (supersedes_revision IS NULL OR supersedes_revision<revision)
);
CREATE TRIGGER stride_talent_search_grant_history_immutable
BEFORE UPDATE OR DELETE ON stride_talent_search_grant_revisions
FOR EACH ROW EXECUTE FUNCTION stride_network_history_is_immutable();
CREATE TABLE stride_talent_search_grants_current (
    grant_id text PRIMARY KEY,
    revision bigint NOT NULL,
    organization_id text NOT NULL,
    membership_id text NOT NULL,
    membership_revision bigint NOT NULL,
    searcher_person_id text NOT NULL,
    state text NOT NULL,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (grant_id,revision,organization_id,membership_id,membership_revision,searcher_person_id,state,expires_at)
      REFERENCES stride_talent_search_grant_revisions(grant_id,revision,organization_id,membership_id,membership_revision,searcher_person_id,state,expires_at)
);
CREATE FUNCTION stride_validate_talent_search_grant_current()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='UPDATE' AND (NEW.grant_id<>OLD.grant_id OR NEW.revision<=OLD.revision) THEN RAISE EXCEPTION 'talent grant CAS revision must advance'; END IF;
    IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM stride_talent_search_grant_revisions r WHERE r.grant_id=NEW.grant_id AND r.revision=NEW.revision AND r.supersedes_revision=OLD.revision) THEN RAISE EXCEPTION 'talent grant must supersede exact current revision'; END IF;
    IF NEW.state='active' AND (NEW.expires_at<=now() OR NOT EXISTS (SELECT 1 FROM stride_organization_memberships_current m WHERE m.membership_id=NEW.membership_id AND m.revision=NEW.membership_revision AND m.organization_id=NEW.organization_id AND m.person_id=NEW.searcher_person_id AND m.status='active')) THEN
        RAISE EXCEPTION 'active talent grant requires exact current organization membership';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_talent_search_grant_current_gate
BEFORE INSERT OR UPDATE ON stride_talent_search_grants_current
FOR EACH ROW EXECUTE FUNCTION stride_validate_talent_search_grant_current();

CREATE TABLE stride_network_search_receipts (
    search_receipt_id text PRIMARY KEY,
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    grant_id text NOT NULL,
    grant_revision bigint NOT NULL,
    original_query_digest bytea NOT NULL CHECK (octet_length(original_query_digest)=32),
    policy_revision bigint NOT NULL CHECK (policy_revision>0),
    policy_verdict text NOT NULL CHECK (policy_verdict IN ('allow','transform_with_confirmation','abstain','reject')),
    policy_reason_codes text[] NOT NULL,
    structured_filters jsonb NOT NULL,
    interpretation_confirmed boolean NOT NULL,
    ordering text[] NOT NULL,
    results jsonb NOT NULL,
    route_ref jsonb,
    cost_microunits bigint NOT NULL CHECK (cost_microunits>=0),
    searched_at timestamptz NOT NULL,
    FOREIGN KEY (grant_id,grant_revision) REFERENCES stride_talent_search_grant_revisions(grant_id,revision),
    CHECK (jsonb_typeof(structured_filters)='array' AND jsonb_typeof(results)='array'),
    CHECK (NOT stride_jsonb_has_forbidden_key(structured_filters,ARRAY['body','audio','email','secret','token','api_key','authorization','credential','credentials','password','cookie','score','personality','culture_fit','protected_trait'])),
    CHECK (NOT stride_jsonb_has_forbidden_key(results,ARRAY['body','audio','email','secret','token','api_key','authorization','credential','credentials','password','cookie','score','ranking','private_source'])),
    CHECK ((policy_verdict NOT IN ('abstain','reject')) OR (jsonb_array_length(structured_filters)=0 AND jsonb_array_length(results)=0 AND route_ref IS NULL AND cost_microunits=0)),
    CHECK (policy_verdict<>'transform_with_confirmation' OR interpretation_confirmed)
);
CREATE TRIGGER stride_network_search_receipt_immutable
BEFORE UPDATE OR DELETE ON stride_network_search_receipts
FOR EACH ROW EXECUTE FUNCTION stride_network_history_is_immutable();
CREATE TABLE stride_network_search_result_projections (
    search_receipt_id text NOT NULL REFERENCES stride_network_search_receipts(search_receipt_id),
    projection_id text NOT NULL,
    projection_revision bigint NOT NULL,
    PRIMARY KEY(search_receipt_id,projection_id),
    FOREIGN KEY (projection_id,projection_revision) REFERENCES stride_network_profile_revisions(projection_id,revision)
);
CREATE TRIGGER stride_network_search_result_ref_immutable
BEFORE UPDATE OR DELETE ON stride_network_search_result_projections
FOR EACH ROW EXECUTE FUNCTION stride_network_history_is_immutable();

CREATE TABLE stride_network_blocks_revisions (
    block_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    blocker_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    blocked_person_id text REFERENCES stride_person_principals(person_id),
    blocked_organization_id text REFERENCES stride_organizations(organization_id),
    controller_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    controller_authority_id text NOT NULL,
    controller_authority_revision bigint NOT NULL CHECK(controller_authority_revision>0),
    policy_revision bigint NOT NULL CHECK(policy_revision>0),
    state text NOT NULL CHECK(state IN ('active','withdrawn')),
    state_changed_at timestamptz NOT NULL,
    supersedes_revision bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY(block_id,revision),
    UNIQUE(block_id,revision,blocker_person_id,blocked_person_id,blocked_organization_id,state),
    CHECK ((blocked_person_id IS NULL)<>(blocked_organization_id IS NULL)),
    CHECK (blocked_person_id IS NULL OR blocked_person_id<>blocker_person_id),
    CHECK (controller_person_id=blocker_person_id),
    CHECK (supersedes_revision IS NULL OR supersedes_revision<revision)
);
CREATE TRIGGER stride_network_block_history_immutable
BEFORE UPDATE OR DELETE ON stride_network_blocks_revisions
FOR EACH ROW EXECUTE FUNCTION stride_network_history_is_immutable();
CREATE TABLE stride_network_blocks_current (
    block_id text PRIMARY KEY,
    revision bigint NOT NULL,
    blocker_person_id text NOT NULL,
    blocked_person_id text,
    blocked_organization_id text,
    state text NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY(block_id,revision,blocker_person_id,blocked_person_id,blocked_organization_id,state)
      REFERENCES stride_network_blocks_revisions(block_id,revision,blocker_person_id,blocked_person_id,blocked_organization_id,state)
);
CREATE FUNCTION stride_validate_network_block_current()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='UPDATE' AND (NEW.block_id<>OLD.block_id OR NEW.blocker_person_id<>OLD.blocker_person_id OR NEW.revision<=OLD.revision OR OLD.state<>'active' OR NEW.state<>'withdrawn') THEN
        RAISE EXCEPTION 'invalid network block CAS transition';
    END IF;
    IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM stride_network_blocks_revisions r WHERE r.block_id=NEW.block_id AND r.revision=NEW.revision AND r.supersedes_revision=OLD.revision) THEN RAISE EXCEPTION 'network block must supersede exact current revision'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_network_block_current_gate BEFORE UPDATE ON stride_network_blocks_current
FOR EACH ROW EXECUTE FUNCTION stride_validate_network_block_current();

CREATE TABLE stride_contact_request_revisions (
    contact_id text NOT NULL,
    revision bigint NOT NULL CHECK(revision>0),
    sender_organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    sender_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    recipient_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    recipient_projection_id text NOT NULL,
    recipient_projection_revision bigint NOT NULL,
    purpose text NOT NULL,
    note_digest bytea NOT NULL CHECK(octet_length(note_digest)=32),
    collaboration_type text NOT NULL CHECK(collaboration_type IN ('collaboration','advisory','employment','recruiting','organization_join')),
    accepted_channel_digest bytea,
    recipient_controller_authority_id text,
    recipient_controller_authority_revision bigint,
    state text NOT NULL CHECK(state IN ('pending','accepted','declined','withdrawn','expired')),
    expires_at timestamptz NOT NULL,
    state_changed_at timestamptz NOT NULL,
    supersedes_revision bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY(contact_id,revision),
    UNIQUE(contact_id,revision,sender_organization_id,sender_person_id,recipient_person_id,recipient_projection_id,recipient_projection_revision,state),
    FOREIGN KEY(recipient_projection_id,recipient_projection_revision) REFERENCES stride_network_profile_revisions(projection_id,revision),
    CHECK(sender_person_id<>recipient_person_id),
    CHECK(expires_at>created_at),
    CHECK ((state='accepted')=(accepted_channel_digest IS NOT NULL AND octet_length(accepted_channel_digest)=32 AND recipient_controller_authority_id IS NOT NULL AND recipient_controller_authority_revision>0)),
    CHECK(supersedes_revision IS NULL OR supersedes_revision<revision)
);
CREATE TRIGGER stride_contact_request_history_immutable
BEFORE UPDATE OR DELETE ON stride_contact_request_revisions
FOR EACH ROW EXECUTE FUNCTION stride_network_history_is_immutable();
CREATE TABLE stride_contact_requests_current (
    contact_id text PRIMARY KEY,
    revision bigint NOT NULL,
    sender_organization_id text NOT NULL,
    sender_person_id text NOT NULL,
    recipient_person_id text NOT NULL,
    recipient_projection_id text NOT NULL,
    recipient_projection_revision bigint NOT NULL,
    state text NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY(contact_id,revision,sender_organization_id,sender_person_id,recipient_person_id,recipient_projection_id,recipient_projection_revision,state)
      REFERENCES stride_contact_request_revisions(contact_id,revision,sender_organization_id,sender_person_id,recipient_person_id,recipient_projection_id,recipient_projection_revision,state)
);
CREATE FUNCTION stride_validate_contact_current()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='UPDATE' AND (NEW.contact_id<>OLD.contact_id OR NEW.revision<=OLD.revision OR OLD.state<>'pending' OR NEW.state NOT IN ('accepted','declined','withdrawn','expired')) THEN RAISE EXCEPTION 'invalid contact CAS transition'; END IF;
    IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM stride_contact_request_revisions r WHERE r.contact_id=NEW.contact_id AND r.revision=NEW.revision AND r.supersedes_revision=OLD.revision) THEN RAISE EXCEPTION 'contact request must supersede exact current revision'; END IF;
    IF NEW.state='pending' THEN
        IF NOT EXISTS(SELECT 1 FROM stride_network_profiles_current p JOIN stride_network_projection_eligibility e ON e.projection_id=p.projection_id AND e.eligible WHERE p.projection_id=NEW.recipient_projection_id AND p.revision=NEW.recipient_projection_revision AND p.subject_person_id=NEW.recipient_person_id AND p.state='published') THEN RAISE EXCEPTION 'contact requires exact eligible published recipient'; END IF;
        IF EXISTS(SELECT 1 FROM stride_network_blocks_current b WHERE b.blocker_person_id=NEW.recipient_person_id AND b.state='active' AND (b.blocked_person_id=NEW.sender_person_id OR b.blocked_organization_id=NEW.sender_organization_id)) THEN RAISE EXCEPTION 'contact blocked by recipient'; END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stride_contact_current_gate BEFORE INSERT OR UPDATE ON stride_contact_requests_current
FOR EACH ROW EXECUTE FUNCTION stride_validate_contact_current();

-- Counters are canonical accounting, not configurable activation. Admission
-- policy may compare these exact person/org/global windows before a later write.
CREATE TABLE stride_network_rate_breadth_accounting (
    scope_kind text NOT NULL CHECK(scope_kind IN ('person','organization','global')),
    scope_id text NOT NULL,
    operation text NOT NULL CHECK(operation IN ('search','contact')),
    window_started_at timestamptz NOT NULL,
    window_ends_at timestamptz NOT NULL CHECK(window_ends_at>window_started_at),
    request_count bigint NOT NULL CHECK(request_count>=0),
    distinct_subject_count bigint NOT NULL CHECK(distinct_subject_count>=0 AND distinct_subject_count<=request_count),
    last_receipt_id text NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY(scope_kind,scope_id,operation,window_started_at)
);
CREATE TABLE stride_network_breadth_subjects (
    scope_kind text NOT NULL,
    scope_id text NOT NULL,
    operation text NOT NULL,
    window_started_at timestamptz NOT NULL,
    subject_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    first_receipt_id text NOT NULL,
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(scope_kind,scope_id,operation,window_started_at,subject_person_id),
    FOREIGN KEY(scope_kind,scope_id,operation,window_started_at) REFERENCES stride_network_rate_breadth_accounting(scope_kind,scope_id,operation,window_started_at)
);

CREATE FUNCTION stride_validate_search_receipt_results()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r record; grant_row stride_talent_search_grants_current%ROWTYPE;
BEGIN
    SELECT * INTO grant_row FROM stride_talent_search_grants_current WHERE grant_id=NEW.grant_id;
    IF grant_row.grant_id IS NULL OR grant_row.revision<>NEW.grant_revision OR grant_row.organization_id<>NEW.organization_id OR grant_row.state<>'active' OR NEW.searched_at>=grant_row.expires_at THEN RAISE EXCEPTION 'search receipt requires exact current unexpired talent grant'; END IF;
    FOR r IN SELECT * FROM stride_network_search_result_projections WHERE search_receipt_id=NEW.search_receipt_id LOOP
        IF NOT EXISTS(SELECT 1 FROM stride_network_profiles_current p JOIN stride_network_projection_eligibility e ON e.projection_id=p.projection_id AND e.eligible WHERE p.projection_id=r.projection_id AND p.revision=r.projection_revision AND p.state='published') THEN RAISE EXCEPTION 'search results require exact eligible published projections'; END IF;
    END LOOP;
    IF jsonb_array_length(NEW.results)<>(SELECT count(*) FROM stride_network_search_result_projections WHERE search_receipt_id=NEW.search_receipt_id) THEN
        RAISE EXCEPTION 'search result receipt and exact projection references disagree';
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER stride_search_receipt_result_gate
AFTER INSERT ON stride_network_search_receipts DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION stride_validate_search_receipt_results();

INSERT INTO stride_feature_switches(feature_key,enabled,revision) VALUES
 ('network_profile_publication',false,1),
 ('network_projection_shadow',false,1),
 ('network_search',false,1),
 ('network_contact',false,1),
 ('network_query_parser_provider',false,1),
 ('network_semantic_reranker',false,1)
ON CONFLICT(feature_key) DO NOTHING;

COMMIT;
