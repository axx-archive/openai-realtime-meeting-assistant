BEGIN;

-- Project authority persists an explicit generation for receipt readability,
-- but its sole canonical clock is the existing session revision. Keeping both
-- equal avoids a second, unwritten lifecycle.
ALTER TABLE stride_active_organization_sessions
	ADD COLUMN authority_generation bigint;
ALTER TABLE stride_active_organization_sessions DISABLE TRIGGER stride_active_organization_session_gate;
UPDATE stride_active_organization_sessions SET authority_generation=session_revision;
ALTER TABLE stride_active_organization_sessions ENABLE TRIGGER stride_active_organization_session_gate;
ALTER TABLE stride_active_organization_sessions
	ALTER COLUMN authority_generation SET NOT NULL,
	ALTER COLUMN authority_generation SET DEFAULT 1,
	ADD CONSTRAINT stride_project_session_generation_matches_revision CHECK(authority_generation=session_revision);

CREATE FUNCTION stride_project_session_generation_monotonic()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.authority_generation<>NEW.session_revision THEN
		RAISE EXCEPTION 'Project authority generation must equal canonical session revision';
	END IF;
	IF TG_OP='UPDATE' AND (NEW.authority_generation<>OLD.authority_generation+1 OR NEW.session_revision<>OLD.session_revision+1) THEN
		RAISE EXCEPTION 'Project authority generation must advance exactly once';
	END IF;
	RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_session_generation_guard
BEFORE INSERT OR UPDATE
ON stride_active_organization_sessions FOR EACH ROW EXECUTE FUNCTION stride_project_session_generation_monotonic();

-- PD1 Project authority is additive and default-off. A Project has stable
-- organization-scoped identity; titles and chat threads are attributes/bindings
-- only. Bodies never enter these authority/index tables.
CREATE FUNCTION stride_project_ref_array_valid(refs jsonb, contract_types text[])
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT jsonb_typeof(refs)='array' AND jsonb_array_length(refs)>0
    AND NOT stride_jsonb_has_forbidden_key(refs,ARRAY['body','text','audio','email','token','authorization','secret','credential','password','cookie'])
    AND NOT EXISTS (
      SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(refs)='array' THEN refs ELSE '[]'::jsonb END) entry(value)
       WHERE jsonb_typeof(value)<>'object'
          OR (SELECT count(*) FROM jsonb_object_keys(CASE WHEN jsonb_typeof(value)='object' THEN value ELSE '{}'::jsonb END))<>4
          OR NOT value ?& ARRAY['contractType','id','revision','digest']
          OR jsonb_typeof(value->'contractType')<>'string' OR NOT (value->>'contractType'=ANY(contract_types))
          OR jsonb_typeof(value->'id')<>'string' OR value->>'id' !~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$'
          OR jsonb_typeof(value->'revision')<>'number' OR value->>'revision' !~ '^[1-9][0-9]*$'
          OR jsonb_typeof(value->'digest')<>'string' OR value->>'digest' !~ '^[0-9A-Fa-f]{64}$'
    );
$$;

CREATE FUNCTION stride_project_audience_valid(audience jsonb)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT jsonb_typeof(audience)='object'
    AND (SELECT count(*) FROM jsonb_object_keys(CASE WHEN jsonb_typeof(audience)='object' THEN audience ELSE '{}'::jsonb END))=2
    AND audience ?& ARRAY['visibility','principals']
    AND audience->>'visibility'='project'
    AND jsonb_typeof(audience->'principals')='array' AND jsonb_array_length(audience->'principals')>0
    AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(audience->'principals') p(value)
      WHERE jsonb_typeof(value)<>'string' OR trim(both '"' from value::text) !~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$');
$$;

CREATE FUNCTION stride_project_aliases_valid(aliases jsonb)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT jsonb_typeof(aliases)='array'
    AND NOT stride_jsonb_has_forbidden_key(aliases,ARRAY['body','text','email','token','authorization','secret'])
    AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(aliases)='array' THEN aliases ELSE '[]'::jsonb END) a(value)
      WHERE jsonb_typeof(value)<>'string' OR char_length(trim(both '"' from value::text))=0 OR char_length(trim(both '"' from value::text))>120);
$$;

CREATE FUNCTION stride_project_source_audience_valid(audience jsonb)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT jsonb_typeof(audience)='object'
    AND (SELECT count(*) FROM jsonb_object_keys(CASE WHEN jsonb_typeof(audience)='object' THEN audience ELSE '{}'::jsonb END))=2
    AND audience ?& ARRAY['visibility','principals']
    AND audience->>'visibility' IN ('private','project','channel','organization','meeting')
    AND jsonb_typeof(audience->'principals')='array' AND jsonb_array_length(audience->'principals')>0
    AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(audience->'principals') p(value)
      WHERE jsonb_typeof(value)<>'string' OR trim(both '"' from value::text) !~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$');
$$;

CREATE TABLE stride_project_revisions (
    project_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    title text NOT NULL,
    aliases jsonb NOT NULL DEFAULT '[]'::jsonb,
    lifecycle text NOT NULL CHECK (lifecycle IN ('draft','active','archived')),
    retention_policy text NOT NULL,
    controller_memberships jsonb NOT NULL,
    audience jsonb NOT NULL,
    acl_revision bigint NOT NULL CHECK (acl_revision > 0),
    creator_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    supersedes_revision bigint,
	supersedes_digest bytea,
    content_digest bytea NOT NULL CHECK (octet_length(content_digest)=32),
    PRIMARY KEY (project_id, revision),
	UNIQUE (project_id, revision, organization_id),
	UNIQUE (project_id, revision, organization_id, lifecycle, content_digest),
    CHECK (project_id=btrim(project_id) AND project_id<>''),
    CHECK (title=btrim(title) AND title<>'' AND char_length(title)<=120),
    CHECK (retention_policy=btrim(retention_policy) AND retention_policy<>''),
    CHECK (stride_project_aliases_valid(aliases)),
    CHECK (stride_project_ref_array_valid(controller_memberships,ARRAY['organization_membership'])),
    CHECK (stride_project_audience_valid(audience)),
    CHECK (NOT stride_jsonb_has_forbidden_key(aliases,ARRAY['email','body','text','token','authorization','secret'])),
    CHECK (NOT stride_jsonb_has_forbidden_key(controller_memberships,ARRAY['email','body','token','authorization','secret'])),
    CHECK (NOT stride_jsonb_has_forbidden_key(audience,ARRAY['email','body','token','authorization','secret'])),
    CHECK (updated_at>=created_at),
	CHECK ((revision=1 AND supersedes_revision IS NULL AND supersedes_digest IS NULL) OR
	       (revision>1 AND supersedes_revision=revision-1 AND octet_length(supersedes_digest)=32))
);

CREATE TABLE stride_projects_current (
    project_id text PRIMARY KEY,
    revision bigint NOT NULL CHECK (revision>0),
    organization_id text NOT NULL,
    lifecycle text NOT NULL CHECK (lifecycle IN ('draft','active','archived')),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest)=32),
    updated_at timestamptz NOT NULL,
    UNIQUE (project_id, revision),
    FOREIGN KEY (project_id,revision,organization_id,lifecycle,content_digest)
      REFERENCES stride_project_revisions(project_id,revision,organization_id,lifecycle,content_digest)
);
CREATE INDEX stride_projects_current_organization
  ON stride_projects_current(organization_id,lifecycle,updated_at DESC);

CREATE FUNCTION stride_project_revision_scope_guard()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE controller jsonb;
BEGIN
	IF NOT EXISTS(SELECT 1 FROM stride_organization_memberships_current m WHERE m.organization_id=NEW.organization_id AND m.person_id=NEW.creator_person_id AND m.status='active') THEN
		RAISE EXCEPTION 'Project creator must be a current organization member';
	END IF;
	FOR controller IN SELECT value FROM jsonb_array_elements(NEW.controller_memberships) LOOP
		IF NOT EXISTS(SELECT 1 FROM stride_organization_membership_revisions m WHERE m.organization_id=NEW.organization_id AND m.membership_id=controller->>'id' AND m.revision=(controller->>'revision')::bigint) THEN
			RAISE EXCEPTION 'Project controller membership must belong to Project organization';
		END IF;
	END LOOP;
	RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_revision_scope BEFORE INSERT ON stride_project_revisions
FOR EACH ROW EXECUTE FUNCTION stride_project_revision_scope_guard();

CREATE TABLE stride_project_thread_binding_revisions (
    binding_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    project_id text NOT NULL,
    project_revision bigint NOT NULL CHECK (project_revision>0),
    thread_id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('primary','related')),
    state text NOT NULL CHECK (state IN ('active','removed')),
    thread_audience_revision bigint NOT NULL CHECK (thread_audience_revision>0),
    thread_acl_digest bytea NOT NULL CHECK (octet_length(thread_acl_digest)=32),
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK (actor_membership_revision>0),
    bound_at timestamptz NOT NULL,
    supersedes_revision bigint,
	supersedes_digest bytea,
    content_digest bytea NOT NULL CHECK (octet_length(content_digest)=32),
    PRIMARY KEY(binding_id,revision),
	UNIQUE(binding_id,revision,organization_id),
    UNIQUE(binding_id,revision,organization_id,project_id,thread_id,kind,state,content_digest),
	FOREIGN KEY(project_id,project_revision,organization_id) REFERENCES stride_project_revisions(project_id,revision,organization_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision) REFERENCES stride_organization_membership_revisions(membership_id,revision),
    CHECK(binding_id=btrim(binding_id) AND binding_id<>'' AND thread_id=btrim(thread_id) AND thread_id<>''),
	CHECK((revision=1 AND supersedes_revision IS NULL AND supersedes_digest IS NULL) OR
	      (revision>1 AND supersedes_revision=revision-1 AND octet_length(supersedes_digest)=32))
);

CREATE TABLE stride_project_thread_bindings_current (
    binding_id text PRIMARY KEY,
    revision bigint NOT NULL CHECK(revision>0),
    organization_id text NOT NULL,
    project_id text NOT NULL,
    thread_id text NOT NULL,
    kind text NOT NULL CHECK(kind IN ('primary','related')),
    state text NOT NULL CHECK(state IN ('active','removed')),
    content_digest bytea NOT NULL CHECK(octet_length(content_digest)=32),
    updated_at timestamptz NOT NULL,
    FOREIGN KEY(binding_id,revision,organization_id,project_id,thread_id,kind,state,content_digest)
      REFERENCES stride_project_thread_binding_revisions(binding_id,revision,organization_id,project_id,thread_id,kind,state,content_digest)
);

CREATE FUNCTION stride_project_binding_scope_guard()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NOT EXISTS(SELECT 1 FROM stride_organization_membership_revisions m WHERE m.organization_id=NEW.organization_id AND m.membership_id=NEW.actor_membership_id AND m.revision=NEW.actor_membership_revision AND m.person_id=NEW.actor_person_id) THEN
		RAISE EXCEPTION 'Project binding actor membership must belong to Project organization';
	END IF;
	RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_binding_scope BEFORE INSERT ON stride_project_thread_binding_revisions
FOR EACH ROW EXECUTE FUNCTION stride_project_binding_scope_guard();
CREATE UNIQUE INDEX stride_project_one_active_primary_thread
  ON stride_project_thread_bindings_current(project_id)
  WHERE kind='primary' AND state='active';
CREATE UNIQUE INDEX stride_project_thread_one_active_owner
  ON stride_project_thread_bindings_current(thread_id)
  WHERE state='active';

CREATE TABLE stride_project_operation_receipts (
    operation_id text PRIMARY KEY,
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    operation_kind text NOT NULL CHECK(operation_kind IN ('create_project','revise_project','bind_thread','unbind_thread')),
    project_id text NOT NULL,
    project_revision bigint NOT NULL CHECK(project_revision>0),
    binding_id text,
    binding_revision bigint,
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
    session_subject_digest bytea NOT NULL,
    session_revision bigint NOT NULL CHECK(session_revision>0),
    authority_generation bigint NOT NULL CHECK(authority_generation>0),
    idempotency_key_digest bytea NOT NULL CHECK(octet_length(idempotency_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
    recorded_at timestamptz NOT NULL,
	UNIQUE(organization_id,operation_kind,idempotency_key_digest),
	FOREIGN KEY(project_id,project_revision,organization_id) REFERENCES stride_project_revisions(project_id,revision,organization_id),
	FOREIGN KEY(binding_id,binding_revision,organization_id) REFERENCES stride_project_thread_binding_revisions(binding_id,revision,organization_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision) REFERENCES stride_organization_membership_revisions(membership_id,revision),
    FOREIGN KEY(session_subject_digest) REFERENCES stride_active_organization_sessions(session_subject_digest),
    CHECK(operation_id=btrim(operation_id) AND operation_id<>''),
    CHECK((binding_id IS NULL)=(binding_revision IS NULL)),
    CHECK((operation_kind IN ('create_project','bind_thread','unbind_thread'))=(binding_id IS NOT NULL))
);

-- A server-owned source resolver emits this body-free receipt only after exact
-- source revision and per-node ACL/consent/purge authorization. Associations
-- must reproduce the receipted snapshot; client-supplied refs alone cannot
-- enter current Project truth.
CREATE TABLE stride_project_source_authority_receipts (
	source_authority_receipt_id text NOT NULL,
	organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
	subject_contract_type text NOT NULL CHECK(subject_contract_type='conversation_event'),
	subject_id text NOT NULL,
	subject_revision bigint NOT NULL CHECK(subject_revision>0),
	subject_digest bytea NOT NULL CHECK(octet_length(subject_digest)=32),
	source_refs jsonb NOT NULL,
	evidence_coverage_digest bytea NOT NULL CHECK(octet_length(evidence_coverage_digest)=32),
	source_audience jsonb NOT NULL,
	source_acl_revision bigint NOT NULL CHECK(source_acl_revision>0),
	source_acl_digest bytea NOT NULL CHECK(octet_length(source_acl_digest)=32),
	consent_revision bigint NOT NULL CHECK(consent_revision>0),
	purge_generation bigint NOT NULL CHECK(purge_generation>0),
	actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
	actor_membership_id text NOT NULL,
	actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
	session_subject_digest bytea NOT NULL,
	session_revision bigint NOT NULL CHECK(session_revision>0),
	authority_generation bigint NOT NULL CHECK(authority_generation>0),
	idempotency_key_digest bytea NOT NULL CHECK(octet_length(idempotency_key_digest)=32),
	request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
	recorded_at timestamptz NOT NULL,
	expires_at timestamptz NOT NULL,
	PRIMARY KEY(source_authority_receipt_id),
	UNIQUE(source_authority_receipt_id,organization_id),
	UNIQUE(organization_id,idempotency_key_digest),
	FOREIGN KEY(actor_membership_id,actor_membership_revision) REFERENCES stride_organization_membership_revisions(membership_id,revision),
	FOREIGN KEY(session_subject_digest) REFERENCES stride_active_organization_sessions(session_subject_digest),
	FOREIGN KEY(organization_id,subject_id) REFERENCES stride_conversation_events(tenant_id,event_id),
	CHECK(source_authority_receipt_id=btrim(source_authority_receipt_id) AND source_authority_receipt_id<>''),
	CHECK(subject_id=btrim(subject_id) AND subject_id<>''),
	CHECK(stride_project_ref_array_valid(source_refs,ARRAY['conversation_event'])),
	CHECK(stride_project_source_audience_valid(source_audience)),
	CHECK(source_audience->>'visibility'='private'),
	CHECK(jsonb_array_length(source_refs)=1),
	CHECK(expires_at>recorded_at)
);

CREATE TABLE stride_project_association_revisions (
    association_id text NOT NULL,
    revision bigint NOT NULL CHECK(revision>0),
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    project_id text NOT NULL,
    project_revision bigint NOT NULL CHECK(project_revision>0),
    subject_contract_type text NOT NULL CHECK(subject_contract_type IN ('conversation_event','transcript_segment','transcript_revision','work_proposal','work_run','outcome','rich_message_part','artifact_disposition')),
    subject_id text NOT NULL,
    subject_revision bigint NOT NULL CHECK(subject_revision>0),
    subject_digest bytea NOT NULL CHECK(octet_length(subject_digest)=32),
    source_refs jsonb NOT NULL,
	source_authority_receipt_id text NOT NULL,
    evidence_coverage_digest bytea NOT NULL CHECK(octet_length(evidence_coverage_digest)=32),
    state text NOT NULL CHECK(state IN ('proposed','confirmed','corrected','removed','expired','revoked')),
    basis text NOT NULL CHECK(basis IN ('authoritative_context','suggested','selected')),
    classifier_revision text NOT NULL,
    confidence double precision NOT NULL CHECK(confidence>=0 AND confidence<=1),
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
    session_subject_digest bytea NOT NULL,
    session_revision bigint NOT NULL CHECK(session_revision>0),
    authority_generation bigint NOT NULL CHECK(authority_generation>0),
    source_audience jsonb NOT NULL,
    source_acl_revision bigint NOT NULL CHECK(source_acl_revision>0),
    source_acl_digest bytea NOT NULL CHECK(octet_length(source_acl_digest)=32),
    consent_revision bigint NOT NULL CHECK(consent_revision>0),
    purge_generation bigint NOT NULL CHECK(purge_generation>0),
    idempotency_key_digest bytea NOT NULL CHECK(octet_length(idempotency_key_digest)=32),
    expires_at timestamptz,
    supersedes_revision bigint,
	supersedes_digest bytea,
    replacement_association_id text,
    replacement_association_revision bigint,
    replacement_association_digest bytea,
    recorded_at timestamptz NOT NULL,
    content_digest bytea NOT NULL CHECK(octet_length(content_digest)=32),
    PRIMARY KEY(association_id,revision),
	UNIQUE(association_id,revision,organization_id),
	UNIQUE(association_id,revision,organization_id,project_id,state,content_digest),
	FOREIGN KEY(project_id,project_revision,organization_id) REFERENCES stride_project_revisions(project_id,revision,organization_id),
	FOREIGN KEY(source_authority_receipt_id,organization_id) REFERENCES stride_project_source_authority_receipts(source_authority_receipt_id,organization_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision) REFERENCES stride_organization_membership_revisions(membership_id,revision),
    FOREIGN KEY(session_subject_digest) REFERENCES stride_active_organization_sessions(session_subject_digest),
    CHECK(association_id=btrim(association_id) AND association_id<>''),
    CHECK(subject_contract_type=btrim(subject_contract_type) AND subject_contract_type<>'' AND subject_id=btrim(subject_id) AND subject_id<>''),
    CHECK(classifier_revision=btrim(classifier_revision) AND classifier_revision<>''),
    CHECK(stride_project_ref_array_valid(source_refs,ARRAY['conversation_event','transcript_segment','transcript_revision','analysis_projection','knowledge_assertion','work_intent','work_proposal','work_run','outcome','rich_message_part','artifact_disposition'])),
    CHECK(stride_project_source_audience_valid(source_audience)),
    CHECK(NOT stride_jsonb_has_forbidden_key(source_refs,ARRAY['body','text','email','token','authorization','secret'])),
    CHECK(NOT stride_jsonb_has_forbidden_key(source_audience,ARRAY['body','text','email','token','authorization','secret'])),
    CHECK((state='proposed' AND expires_at>recorded_at) OR (state<>'proposed' AND expires_at IS NULL)),
	CHECK((revision=1 AND supersedes_revision IS NULL AND supersedes_digest IS NULL) OR
	      (revision>1 AND supersedes_revision=revision-1 AND octet_length(supersedes_digest)=32)),
    CHECK((state='corrected')=(replacement_association_id IS NOT NULL)),
    CHECK((replacement_association_id IS NULL)=(replacement_association_revision IS NULL) AND
          (replacement_association_id IS NULL)=(replacement_association_digest IS NULL)),
    CHECK(replacement_association_revision IS NULL OR replacement_association_revision>0),
    CHECK(replacement_association_digest IS NULL OR octet_length(replacement_association_digest)=32)
);

CREATE TABLE stride_project_associations_current (
    association_id text PRIMARY KEY,
    revision bigint NOT NULL CHECK(revision>0),
    organization_id text NOT NULL,
    project_id text NOT NULL,
    state text NOT NULL CHECK(state IN ('proposed','confirmed','corrected','removed','expired','revoked')),
    content_digest bytea NOT NULL CHECK(octet_length(content_digest)=32),
    updated_at timestamptz NOT NULL,
    FOREIGN KEY(association_id,revision,organization_id,project_id,state,content_digest)
      REFERENCES stride_project_association_revisions(association_id,revision,organization_id,project_id,state,content_digest)
);
CREATE INDEX stride_project_associations_current_project
  ON stride_project_associations_current(organization_id,project_id,state,updated_at DESC);

CREATE TABLE stride_project_association_events (
    event_id text PRIMARY KEY,
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    association_id text NOT NULL,
    association_revision bigint NOT NULL CHECK(association_revision>0),
    action text NOT NULL CHECK(action IN ('propose','confirm','correct','remove','expire','revoke')),
	resulting_state text NOT NULL CHECK(resulting_state IN ('proposed','confirmed','corrected','removed','expired','revoked')),
    prior_revision bigint NOT NULL CHECK(prior_revision>=0),
    new_revision bigint NOT NULL CHECK(new_revision>0),
    replacement_association_id text,
    replacement_association_revision bigint,
	replacement_association_digest bytea,
	correction_id text,
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
    session_subject_digest bytea NOT NULL,
    session_revision bigint NOT NULL CHECK(session_revision>0),
    authority_generation bigint NOT NULL CHECK(authority_generation>0),
    idempotency_key_digest bytea NOT NULL CHECK(octet_length(idempotency_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
    occurred_at timestamptz NOT NULL,
	UNIQUE(event_id,organization_id),
	UNIQUE(organization_id,action,idempotency_key_digest,association_id),
	FOREIGN KEY(association_id,association_revision,organization_id) REFERENCES stride_project_association_revisions(association_id,revision,organization_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision) REFERENCES stride_organization_membership_revisions(membership_id,revision),
	FOREIGN KEY(session_subject_digest) REFERENCES stride_active_organization_sessions(session_subject_digest),
	CHECK(new_revision=association_revision),
	CHECK((action='propose' AND resulting_state='proposed' AND prior_revision=0 AND new_revision=1) OR
	      (action='confirm' AND resulting_state='confirmed' AND ((prior_revision=0 AND new_revision=1) OR new_revision=prior_revision+1)) OR
	      (action='correct' AND resulting_state='corrected' AND new_revision=prior_revision+1) OR
	      (action='remove' AND resulting_state='removed' AND new_revision=prior_revision+1) OR
	      (action='expire' AND resulting_state='expired' AND new_revision=prior_revision+1) OR
	      (action='revoke' AND resulting_state='revoked' AND new_revision=prior_revision+1)),
	CHECK((action='correct')=(replacement_association_id IS NOT NULL)),
	CHECK((correction_id IS NOT NULL)=(action='correct' OR action='confirm' AND prior_revision=0)),
    CHECK((replacement_association_id IS NULL)=(replacement_association_revision IS NULL) AND
          (replacement_association_id IS NULL)=(replacement_association_digest IS NULL))
);

CREATE TABLE stride_project_correction_receipts (
	correction_id text PRIMARY KEY,
	organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
	old_association_id text NOT NULL,
	old_association_revision bigint NOT NULL CHECK(old_association_revision>1),
	replacement_association_id text NOT NULL,
	replacement_association_revision bigint NOT NULL CHECK(replacement_association_revision=1),
	old_event_id text NOT NULL,
	replacement_event_id text NOT NULL,
	actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
	actor_membership_id text NOT NULL,
	actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
	session_subject_digest bytea NOT NULL,
	session_revision bigint NOT NULL CHECK(session_revision>0),
	authority_generation bigint NOT NULL CHECK(authority_generation>0),
	idempotency_key_digest bytea NOT NULL CHECK(octet_length(idempotency_key_digest)=32),
	request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
	recorded_at timestamptz NOT NULL,
	UNIQUE(organization_id,idempotency_key_digest),
	FOREIGN KEY(old_association_id,old_association_revision,organization_id) REFERENCES stride_project_association_revisions(association_id,revision,organization_id),
	FOREIGN KEY(replacement_association_id,replacement_association_revision,organization_id) REFERENCES stride_project_association_revisions(association_id,revision,organization_id),
	FOREIGN KEY(old_event_id,organization_id) REFERENCES stride_project_association_events(event_id,organization_id),
	FOREIGN KEY(replacement_event_id,organization_id) REFERENCES stride_project_association_events(event_id,organization_id),
	FOREIGN KEY(actor_membership_id,actor_membership_revision) REFERENCES stride_organization_membership_revisions(membership_id,revision),
	FOREIGN KEY(session_subject_digest) REFERENCES stride_active_organization_sessions(session_subject_digest),
	CHECK(correction_id=btrim(correction_id) AND correction_id<>'' AND old_association_id<>replacement_association_id)
);

CREATE TABLE stride_project_projection_outbox (
    outbox_id bigserial PRIMARY KEY,
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    association_id text NOT NULL,
    association_revision bigint NOT NULL CHECK(association_revision>0),
    operation text NOT NULL CHECK(operation IN ('unlist_old','list_new','purge')),
    projection_family text NOT NULL CHECK(projection_family IN ('home','work','board','project_record')),
    source_ref_digest bytea NOT NULL CHECK(octet_length(source_ref_digest)=32),
    authority_digest bytea NOT NULL CHECK(octet_length(authority_digest)=32),
    status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','applied','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
    next_attempt_at timestamptz NOT NULL,
    applied_at timestamptz,
    UNIQUE(organization_id,association_id,association_revision,operation,projection_family),
	FOREIGN KEY(association_id,association_revision,organization_id) REFERENCES stride_project_association_revisions(association_id,revision,organization_id)
);

-- Every durable Project read uses this view, never the raw current-pointer
-- table. Source drift therefore removes even metadata/count visibility in the
-- same transaction that invalidates the canonical conversation event.
CREATE VIEW stride_project_associations_authorized_current AS
SELECT current_association.*
  FROM stride_project_associations_current current_association
  JOIN stride_project_association_revisions association_revision
    ON association_revision.association_id=current_association.association_id
   AND association_revision.revision=current_association.revision
   AND association_revision.organization_id=current_association.organization_id
  JOIN stride_project_source_authority_receipts receipt
    ON receipt.source_authority_receipt_id=association_revision.source_authority_receipt_id
   AND receipt.organization_id=association_revision.organization_id
  JOIN stride_conversation_events source_event
    ON source_event.tenant_id=association_revision.organization_id
   AND source_event.event_id=association_revision.subject_id
	WHERE source_event.invalidated_at IS NULL
   AND source_event.content_revision=association_revision.subject_revision
   AND source_event.content_digest=association_revision.subject_digest
   AND source_event.audience_digest=sha256(convert_to(association_revision.source_audience::text,'UTF8'))
   AND source_event.visibility='private'
   AND source_event.acl_version=association_revision.source_acl_revision
   AND source_event.purge_generation=association_revision.purge_generation
   AND association_revision.source_audience->'principals' @> jsonb_build_array(association_revision.actor_person_id);

CREATE FUNCTION stride_project_source_drift_unlist()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.invalidated_at IS NOT NULL OR NEW.content_revision<>OLD.content_revision OR NEW.content_digest<>OLD.content_digest OR
	   NEW.audience_digest<>OLD.audience_digest OR NEW.visibility<>OLD.visibility OR NEW.acl_version<>OLD.acl_version OR
	   NEW.purge_generation<>OLD.purge_generation THEN
		INSERT INTO stride_project_projection_outbox(organization_id,association_id,association_revision,operation,projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at)
		SELECT association_revision.organization_id,association_revision.association_id,association_revision.revision,'purge',family.name,
		       association_revision.subject_digest,
		       sha256(convert_to(concat_ws(E'\x1f',NEW.tenant_id,NEW.event_id,NEW.content_revision::text,encode(NEW.content_digest,'hex'),encode(NEW.audience_digest,'hex'),NEW.visibility,NEW.acl_version::text,NEW.purge_generation::text,COALESCE(NEW.invalidated_at::text,'')),'UTF8')),
		       'pending',0,clock_timestamp()
		  FROM stride_project_associations_current current_association
		  JOIN stride_project_association_revisions association_revision
		    ON association_revision.association_id=current_association.association_id
		   AND association_revision.revision=current_association.revision
		 CROSS JOIN (VALUES('home'),('work'),('board'),('project_record')) family(name)
		 WHERE association_revision.organization_id=NEW.tenant_id
		   AND association_revision.subject_contract_type='conversation_event'
		   AND association_revision.subject_id=NEW.event_id
		ON CONFLICT(organization_id,association_id,association_revision,operation,projection_family) DO NOTHING;
	END IF;
	RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_source_drift_unlist_guard
AFTER UPDATE OF content_revision,content_digest,audience_digest,visibility,acl_version,purge_generation,invalidated_at
ON stride_conversation_events FOR EACH ROW EXECUTE FUNCTION stride_project_source_drift_unlist();

CREATE FUNCTION stride_project_operation_requires_current_authority()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE membership stride_organization_memberships_current%ROWTYPE;
DECLARE session_binding stride_active_organization_sessions%ROWTYPE;
DECLARE authority_at timestamptz;
BEGIN
  authority_at := COALESCE((to_jsonb(NEW)->>'recorded_at')::timestamptz,(to_jsonb(NEW)->>'occurred_at')::timestamptz);
  SELECT * INTO membership FROM stride_organization_memberships_current WHERE membership_id=NEW.actor_membership_id FOR SHARE;
  SELECT * INTO session_binding FROM stride_active_organization_sessions WHERE session_subject_digest=NEW.session_subject_digest FOR SHARE;
	IF authority_at IS NULL OR authority_at<session_binding.bound_at OR authority_at>clock_timestamp()+interval '1 minute' OR
	   membership.membership_id IS NULL OR membership.status<>'active' OR membership.organization_id<>NEW.organization_id OR
     membership.person_id<>NEW.actor_person_id OR membership.revision<>NEW.actor_membership_revision OR
	   session_binding.session_subject_digest IS NULL OR session_binding.status<>'active' OR session_binding.expires_at<=clock_timestamp() OR session_binding.expires_at<=authority_at OR
     session_binding.person_id<>NEW.actor_person_id OR session_binding.organization_id<>NEW.organization_id OR
     session_binding.membership_id<>NEW.actor_membership_id OR session_binding.membership_revision<>NEW.actor_membership_revision OR
	   session_binding.session_revision<>NEW.session_revision OR NEW.authority_generation<>session_binding.authority_generation THEN
    RAISE EXCEPTION 'Project operation requires current organization session authority';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_operation_authority_guard BEFORE INSERT ON stride_project_operation_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_operation_requires_current_authority();
CREATE TRIGGER stride_project_association_event_authority_guard BEFORE INSERT ON stride_project_association_events
FOR EACH ROW EXECUTE FUNCTION stride_project_operation_requires_current_authority();
CREATE TRIGGER stride_project_correction_authority_guard BEFORE INSERT ON stride_project_correction_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_operation_requires_current_authority();
CREATE TRIGGER stride_project_source_authority_guard BEFORE INSERT ON stride_project_source_authority_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_operation_requires_current_authority();

-- A source receipt is valid only when PostgreSQL can reproduce it from exact
-- current canonical conversation truth and prove the held person belongs to
-- that private audience. Knowing an event identifier or digest is not enough.
CREATE FUNCTION stride_project_source_receipt_requires_canonical_source()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_event stride_conversation_events%ROWTYPE;
BEGIN
	SELECT * INTO source_event FROM stride_conversation_events
	 WHERE tenant_id=NEW.organization_id AND event_id=NEW.subject_id FOR SHARE;
	IF source_event.event_id IS NULL OR NEW.subject_contract_type<>'conversation_event' OR
	   source_event.content_revision<>NEW.subject_revision OR source_event.content_digest<>NEW.subject_digest OR
	   source_event.invalidated_at IS NOT NULL OR source_event.purge_generation<>NEW.purge_generation OR
	   source_event.visibility<>'private' OR NEW.source_audience->>'visibility'<>'private' OR
	   NOT (NEW.source_audience->'principals' @> jsonb_build_array(NEW.actor_person_id)) OR
	   source_event.acl_version<>NEW.source_acl_revision OR
	   source_event.audience_digest<>sha256(convert_to(NEW.source_audience::text,'UTF8')) OR
	   NEW.source_acl_digest<>sha256(convert_to(concat_ws(E'\x1f',source_event.tenant_id,source_event.event_id,source_event.content_revision::text,encode(source_event.content_digest,'hex'),encode(source_event.audience_digest,'hex'),source_event.visibility,source_event.acl_version::text,source_event.purge_generation::text),'UTF8')) OR
	   NEW.source_refs<>jsonb_build_array(jsonb_build_object('contractType','conversation_event','id',source_event.event_id,'revision',source_event.content_revision,'digest',encode(source_event.content_digest,'hex'))) THEN
		RAISE EXCEPTION 'Project source receipt requires exact authorized canonical conversation event';
	END IF;
	RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_source_canonical_guard BEFORE INSERT ON stride_project_source_authority_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_source_receipt_requires_canonical_source();

CREATE FUNCTION stride_project_association_requires_source_receipt()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE receipt stride_project_source_authority_receipts%ROWTYPE;
DECLARE source_event stride_conversation_events%ROWTYPE;
BEGIN
	SELECT * INTO receipt FROM stride_project_source_authority_receipts
	 WHERE source_authority_receipt_id=NEW.source_authority_receipt_id AND organization_id=NEW.organization_id FOR SHARE;
	SELECT * INTO source_event FROM stride_conversation_events WHERE tenant_id=NEW.organization_id AND event_id=NEW.subject_id FOR SHARE;
	IF receipt.source_authority_receipt_id IS NULL OR receipt.expires_at<=clock_timestamp() OR source_event.event_id IS NULL OR
	   receipt.subject_contract_type<>NEW.subject_contract_type OR receipt.subject_id<>NEW.subject_id OR
	   receipt.subject_revision<>NEW.subject_revision OR receipt.subject_digest<>NEW.subject_digest OR
	   receipt.source_refs<>NEW.source_refs OR receipt.evidence_coverage_digest<>NEW.evidence_coverage_digest OR
	   receipt.source_audience<>NEW.source_audience OR receipt.source_acl_revision<>NEW.source_acl_revision OR
	   receipt.source_acl_digest<>NEW.source_acl_digest OR receipt.consent_revision<>NEW.consent_revision OR
	   receipt.purge_generation<>NEW.purge_generation OR receipt.actor_person_id<>NEW.actor_person_id OR
	   receipt.actor_membership_id<>NEW.actor_membership_id OR receipt.actor_membership_revision<>NEW.actor_membership_revision OR
	   receipt.session_subject_digest<>NEW.session_subject_digest OR receipt.session_revision<>NEW.session_revision OR
	   receipt.authority_generation<>NEW.authority_generation OR
	   source_event.content_revision<>NEW.subject_revision OR source_event.content_digest<>NEW.subject_digest OR
	   source_event.invalidated_at IS NOT NULL OR source_event.purge_generation<>NEW.purge_generation OR
	   source_event.visibility<>'private' OR NEW.source_audience->>'visibility'<>'private' OR
	   NOT (NEW.source_audience->'principals' @> jsonb_build_array(NEW.actor_person_id)) OR
	   source_event.visibility<>NEW.source_audience->>'visibility' OR source_event.acl_version<>NEW.source_acl_revision OR
	   source_event.audience_digest<>sha256(convert_to(NEW.source_audience::text,'UTF8')) OR
	   NEW.source_acl_digest<>sha256(convert_to(concat_ws(E'\x1f',source_event.tenant_id,source_event.event_id,source_event.content_revision::text,encode(source_event.content_digest,'hex'),encode(source_event.audience_digest,'hex'),source_event.visibility,source_event.acl_version::text,source_event.purge_generation::text),'UTF8')) OR
	   NEW.source_refs<>jsonb_build_array(jsonb_build_object('contractType','conversation_event','id',source_event.event_id,'revision',source_event.content_revision,'digest',encode(source_event.content_digest,'hex'))) THEN
		RAISE EXCEPTION 'ProjectAssociation requires exact current source authority receipt';
	END IF;
	RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_association_source_authority_guard BEFORE INSERT ON stride_project_association_revisions
FOR EACH ROW EXECUTE FUNCTION stride_project_association_requires_source_receipt();

CREATE FUNCTION stride_project_current_revision_gate()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_row stride_project_revisions%ROWTYPE;
BEGIN
  IF NOT EXISTS(SELECT 1 FROM stride_project_operation_receipts r WHERE r.organization_id=NEW.organization_id AND r.project_id=NEW.project_id AND r.project_revision=NEW.revision) THEN
    RAISE EXCEPTION 'Project current revision requires authority receipt';
  END IF;
  IF TG_OP='INSERT' THEN RETURN NEW; END IF;
  IF NEW.project_id<>OLD.project_id OR NEW.organization_id<>OLD.organization_id THEN
    RAISE EXCEPTION 'Project identity is immutable';
  END IF;
  IF NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'Project current revision must advance exactly once'; END IF;
  SELECT * INTO next_row FROM stride_project_revisions WHERE project_id=NEW.project_id AND revision=NEW.revision;
	IF next_row.supersedes_revision<>OLD.revision OR next_row.supersedes_digest<>OLD.content_digest THEN RAISE EXCEPTION 'Project revision must supersede exact current digest'; END IF;
  IF OLD.lifecycle='archived' AND NEW.lifecycle<>'archived' OR OLD.lifecycle='active' AND NEW.lifecycle='draft' THEN
    RAISE EXCEPTION 'Project lifecycle cannot move backward';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_current_revision_guard BEFORE INSERT OR UPDATE ON stride_projects_current
FOR EACH ROW EXECUTE FUNCTION stride_project_current_revision_gate();

CREATE FUNCTION stride_project_thread_binding_current_gate()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_row stride_project_thread_binding_revisions%ROWTYPE;
BEGIN
  IF NOT EXISTS(SELECT 1 FROM stride_project_operation_receipts r WHERE r.organization_id=NEW.organization_id AND r.binding_id=NEW.binding_id AND r.binding_revision=NEW.revision) THEN
    RAISE EXCEPTION 'Project thread binding current revision requires authority receipt';
  END IF;
  IF TG_OP='INSERT' THEN RETURN NEW; END IF;
  IF NEW.binding_id<>OLD.binding_id OR NEW.organization_id<>OLD.organization_id OR
     NEW.project_id<>OLD.project_id OR NEW.thread_id<>OLD.thread_id OR NEW.kind<>OLD.kind THEN
    RAISE EXCEPTION 'Project thread binding identity is immutable';
  END IF;
  IF NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'Project thread binding current revision must advance exactly once'; END IF;
  SELECT * INTO next_row FROM stride_project_thread_binding_revisions WHERE binding_id=NEW.binding_id AND revision=NEW.revision;
	IF next_row.supersedes_revision<>OLD.revision OR next_row.supersedes_digest<>OLD.content_digest THEN RAISE EXCEPTION 'Project thread binding must supersede exact current digest'; END IF;
  IF OLD.state='removed' THEN RAISE EXCEPTION 'removed Project thread binding cannot be resurrected'; END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_thread_binding_current_guard BEFORE INSERT OR UPDATE ON stride_project_thread_bindings_current
FOR EACH ROW EXECUTE FUNCTION stride_project_thread_binding_current_gate();

CREATE FUNCTION stride_project_association_current_gate()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_row stride_project_association_revisions%ROWTYPE;
BEGIN
	IF NOT EXISTS(SELECT 1 FROM stride_project_association_events e WHERE e.organization_id=NEW.organization_id AND e.association_id=NEW.association_id AND e.association_revision=NEW.revision AND e.resulting_state=NEW.state) THEN
    RAISE EXCEPTION 'ProjectAssociation current revision requires authority event';
  END IF;
  IF TG_OP='INSERT' THEN RETURN NEW; END IF;
  IF NEW.association_id<>OLD.association_id OR NEW.organization_id<>OLD.organization_id THEN
    RAISE EXCEPTION 'ProjectAssociation identity is immutable';
  END IF;
  IF NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'ProjectAssociation current revision must advance exactly once'; END IF;
  SELECT * INTO next_row FROM stride_project_association_revisions WHERE association_id=NEW.association_id AND revision=NEW.revision;
	IF next_row.supersedes_revision<>OLD.revision OR next_row.supersedes_digest<>OLD.content_digest THEN RAISE EXCEPTION 'ProjectAssociation must supersede exact current digest'; END IF;
  IF NOT ((OLD.state='proposed' AND NEW.state IN ('confirmed','removed','expired','revoked')) OR
          (OLD.state='confirmed' AND NEW.state IN ('corrected','removed','revoked'))) THEN
    RAISE EXCEPTION 'illegal ProjectAssociation transition or resurrection';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_association_current_guard BEFORE INSERT OR UPDATE ON stride_project_associations_current
FOR EACH ROW EXECUTE FUNCTION stride_project_association_current_gate();

CREATE FUNCTION stride_project_correction_complete()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE receipt stride_project_correction_receipts%ROWTYPE;
DECLARE old_revision stride_project_association_revisions%ROWTYPE;
DECLARE replacement_revision stride_project_association_revisions%ROWTYPE;
DECLARE old_event stride_project_association_events%ROWTYPE;
DECLARE replacement_event stride_project_association_events%ROWTYPE;
DECLARE old_current stride_project_associations_current%ROWTYPE;
DECLARE replacement_current stride_project_associations_current%ROWTYPE;
DECLARE wanted_correction_id text;
BEGIN
	IF TG_TABLE_NAME='stride_project_correction_receipts' THEN wanted_correction_id:=NEW.correction_id;
	ELSIF TG_TABLE_NAME='stride_project_association_events' THEN wanted_correction_id:=NEW.correction_id;
	ELSIF NEW.state='corrected' THEN
		SELECT correction_id INTO wanted_correction_id FROM stride_project_association_events WHERE organization_id=NEW.organization_id AND association_id=NEW.association_id AND association_revision=NEW.revision AND action='correct';
	ELSE RETURN NEW; END IF;
	IF wanted_correction_id IS NULL THEN RETURN NEW; END IF;
	SELECT * INTO receipt FROM stride_project_correction_receipts WHERE correction_id=wanted_correction_id;
	IF receipt.correction_id IS NULL THEN RAISE EXCEPTION 'Project correction requires atomic receipt'; END IF;
	SELECT * INTO old_revision FROM stride_project_association_revisions WHERE association_id=receipt.old_association_id AND revision=receipt.old_association_revision;
	SELECT * INTO replacement_revision FROM stride_project_association_revisions WHERE association_id=receipt.replacement_association_id AND revision=receipt.replacement_association_revision;
	SELECT * INTO old_event FROM stride_project_association_events WHERE event_id=receipt.old_event_id;
	SELECT * INTO replacement_event FROM stride_project_association_events WHERE event_id=receipt.replacement_event_id;
	SELECT * INTO old_current FROM stride_project_associations_current WHERE association_id=receipt.old_association_id;
	SELECT * INTO replacement_current FROM stride_project_associations_current WHERE association_id=receipt.replacement_association_id;
	IF old_revision.association_id IS NULL OR replacement_revision.association_id IS NULL OR old_event.event_id IS NULL OR replacement_event.event_id IS NULL OR
	   old_current.association_id IS NULL OR replacement_current.association_id IS NULL OR
	   old_revision.state<>'corrected' OR old_revision.replacement_association_id<>replacement_revision.association_id OR old_revision.replacement_association_revision<>replacement_revision.revision OR old_revision.replacement_association_digest<>replacement_revision.content_digest OR
	   replacement_revision.state<>'confirmed' OR old_event.action<>'correct' OR old_event.resulting_state<>'corrected' OR old_event.correction_id<>receipt.correction_id OR
	   old_event.association_id<>old_revision.association_id OR old_event.association_revision<>old_revision.revision OR
	   old_event.replacement_association_id<>replacement_revision.association_id OR old_event.replacement_association_revision<>replacement_revision.revision OR old_event.replacement_association_digest<>replacement_revision.content_digest OR
	   replacement_event.action<>'confirm' OR replacement_event.resulting_state<>'confirmed' OR replacement_event.correction_id<>receipt.correction_id OR
	   replacement_event.association_id<>replacement_revision.association_id OR replacement_event.association_revision<>replacement_revision.revision OR
	   old_event.actor_person_id<>receipt.actor_person_id OR replacement_event.actor_person_id<>receipt.actor_person_id OR
	   old_event.actor_membership_id<>receipt.actor_membership_id OR replacement_event.actor_membership_id<>receipt.actor_membership_id OR
	   old_event.actor_membership_revision<>receipt.actor_membership_revision OR replacement_event.actor_membership_revision<>receipt.actor_membership_revision OR
	   old_event.session_subject_digest<>receipt.session_subject_digest OR replacement_event.session_subject_digest<>receipt.session_subject_digest OR
	   old_event.session_revision<>receipt.session_revision OR replacement_event.session_revision<>receipt.session_revision OR
	   old_event.authority_generation<>receipt.authority_generation OR replacement_event.authority_generation<>receipt.authority_generation OR
	   old_event.idempotency_key_digest<>receipt.idempotency_key_digest OR replacement_event.idempotency_key_digest<>receipt.idempotency_key_digest OR
	   old_event.request_fingerprint<>receipt.request_fingerprint OR replacement_event.request_fingerprint<>receipt.request_fingerprint OR
	   old_revision.actor_person_id<>receipt.actor_person_id OR replacement_revision.actor_person_id<>receipt.actor_person_id OR
	   old_revision.actor_membership_id<>receipt.actor_membership_id OR replacement_revision.actor_membership_id<>receipt.actor_membership_id OR
	   old_revision.actor_membership_revision<>receipt.actor_membership_revision OR replacement_revision.actor_membership_revision<>receipt.actor_membership_revision OR
	   old_revision.session_subject_digest<>receipt.session_subject_digest OR replacement_revision.session_subject_digest<>receipt.session_subject_digest OR
	   old_revision.session_revision<>receipt.session_revision OR replacement_revision.session_revision<>receipt.session_revision OR
	   old_revision.authority_generation<>receipt.authority_generation OR replacement_revision.authority_generation<>receipt.authority_generation OR
	   old_revision.idempotency_key_digest<>receipt.idempotency_key_digest OR replacement_revision.idempotency_key_digest<>receipt.idempotency_key_digest OR
	   old_current.revision<>old_revision.revision OR old_current.state<>'corrected' OR replacement_current.revision<>replacement_revision.revision OR replacement_current.state<>'confirmed' THEN
		RAISE EXCEPTION 'Project correction receipt does not bind exact old and replacement current edges';
	END IF;
	RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER stride_project_correction_receipt_complete AFTER INSERT ON stride_project_correction_receipts
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stride_project_correction_complete();
CREATE CONSTRAINT TRIGGER stride_project_correction_event_complete AFTER INSERT ON stride_project_association_events
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.correction_id IS NOT NULL) EXECUTE FUNCTION stride_project_correction_complete();
CREATE CONSTRAINT TRIGGER stride_project_corrected_current_complete AFTER INSERT OR UPDATE ON stride_project_associations_current
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.state='corrected') EXECUTE FUNCTION stride_project_correction_complete();

CREATE FUNCTION stride_project_append_only()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'Project authority history is append-only'; END; $$;
CREATE TRIGGER stride_project_revision_immutable BEFORE UPDATE OR DELETE ON stride_project_revisions
FOR EACH ROW EXECUTE FUNCTION stride_project_append_only();
CREATE TRIGGER stride_project_binding_revision_immutable BEFORE UPDATE OR DELETE ON stride_project_thread_binding_revisions
FOR EACH ROW EXECUTE FUNCTION stride_project_append_only();
CREATE TRIGGER stride_project_association_revision_immutable BEFORE UPDATE OR DELETE ON stride_project_association_revisions
FOR EACH ROW EXECUTE FUNCTION stride_project_append_only();
CREATE TRIGGER stride_project_association_event_immutable BEFORE UPDATE OR DELETE ON stride_project_association_events
FOR EACH ROW EXECUTE FUNCTION stride_project_append_only();
CREATE TRIGGER stride_project_operation_receipt_immutable BEFORE UPDATE OR DELETE ON stride_project_operation_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_append_only();
CREATE TRIGGER stride_project_source_authority_receipt_immutable BEFORE UPDATE OR DELETE ON stride_project_source_authority_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_append_only();
CREATE TRIGGER stride_project_correction_receipt_immutable BEFORE UPDATE OR DELETE ON stride_project_correction_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_append_only();
CREATE TRIGGER stride_project_current_no_delete BEFORE DELETE ON stride_projects_current
FOR EACH ROW EXECUTE FUNCTION stride_project_append_only();
CREATE TRIGGER stride_project_binding_current_no_delete BEFORE DELETE ON stride_project_thread_bindings_current
FOR EACH ROW EXECUTE FUNCTION stride_project_append_only();
CREATE TRIGGER stride_project_association_current_no_delete BEFORE DELETE ON stride_project_associations_current
FOR EACH ROW EXECUTE FUNCTION stride_project_append_only();

INSERT INTO stride_feature_switches(feature_key,enabled,revision) VALUES
 ('project_authority_read',false,1),
 ('project_authority_write',false,1),
 ('project_smart_link',false,1),
 ('project_record_projection',false,1);

COMMIT;
