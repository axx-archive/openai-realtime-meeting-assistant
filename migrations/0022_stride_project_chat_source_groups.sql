BEGIN;

-- Ordered attachment parts are body-free canonical authority records. The
-- binary remains in the legacy blob store; these rows bind its exact source
-- grant, blob digest, destination audience, and parent ConversationEvent.
CREATE TABLE stride_rich_message_part_revisions (
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    part_id text NOT NULL,
    revision bigint NOT NULL CHECK(revision>0),
    conversation_event_id text NOT NULL,
    conversation_event_revision bigint NOT NULL CHECK(conversation_event_revision>0),
    ordinal integer NOT NULL CHECK(ordinal>=0),
    source_id text NOT NULL,
    source_revision text NOT NULL,
    source_origin_id text,
    source_origin_revision text,
    blob_ref text NOT NULL,
    blob_digest bytea NOT NULL CHECK(octet_length(blob_digest)=32),
    media_type text NOT NULL,
    byte_size bigint NOT NULL CHECK(byte_size>0),
    destination_digest bytea NOT NULL CHECK(octet_length(destination_digest)=32),
    destination_revision text NOT NULL,
    author_principal text NOT NULL REFERENCES stride_person_principals(person_id),
    source_audience jsonb NOT NULL,
    source_acl_revision bigint NOT NULL CHECK(source_acl_revision>0),
    purge_generation bigint NOT NULL CHECK(purge_generation>0),
    recorded_at timestamptz NOT NULL,
    invalidated_at timestamptz,
    invalidation_reason text,
    content_digest bytea NOT NULL CHECK(octet_length(content_digest)=32),
    PRIMARY KEY(part_id,revision),
    UNIQUE(part_id,revision,organization_id),
    UNIQUE(organization_id,conversation_event_id,ordinal,revision),
    FOREIGN KEY(organization_id,conversation_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    CHECK(part_id=btrim(part_id) AND part_id<>''),
    CHECK(source_id=btrim(source_id) AND source_id<>''),
    CHECK(source_revision=btrim(source_revision) AND source_revision<>''),
    CHECK((source_origin_id IS NULL)=(source_origin_revision IS NULL)),
    CHECK(blob_ref=btrim(blob_ref) AND blob_ref<>''),
    CHECK(media_type=btrim(media_type) AND media_type<>''),
    CHECK(destination_revision=btrim(destination_revision) AND destination_revision<>''),
    CHECK(stride_project_source_audience_valid(source_audience)),
    CHECK(source_audience->>'visibility' IN ('private','project','channel')),
    CHECK((invalidated_at IS NULL AND invalidation_reason IS NULL) OR
          (invalidated_at IS NOT NULL AND invalidation_reason=btrim(invalidation_reason) AND invalidation_reason<>''))
);

CREATE TABLE stride_rich_message_parts_current (
    organization_id text NOT NULL,
    part_id text PRIMARY KEY,
    revision bigint NOT NULL CHECK(revision>0),
    conversation_event_id text NOT NULL,
    ordinal integer NOT NULL CHECK(ordinal>=0),
    content_digest bytea NOT NULL CHECK(octet_length(content_digest)=32),
    updated_at timestamptz NOT NULL,
    FOREIGN KEY(part_id,revision,organization_id)
      REFERENCES stride_rich_message_part_revisions(part_id,revision,organization_id),
    UNIQUE(organization_id,conversation_event_id,ordinal)
);

CREATE FUNCTION stride_rich_message_part_revision_immutable()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  RAISE EXCEPTION 'Rich message part revisions are append-only';
END; $$;
CREATE TRIGGER stride_rich_message_part_revision_immutable_guard
BEFORE UPDATE OR DELETE ON stride_rich_message_part_revisions
FOR EACH ROW EXECUTE FUNCTION stride_rich_message_part_revision_immutable();

CREATE FUNCTION stride_rich_message_part_current_exact_transition()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_revision stride_rich_message_part_revisions%ROWTYPE;
DECLARE prior_revision stride_rich_message_part_revisions%ROWTYPE;
BEGIN
  SELECT * INTO next_revision FROM stride_rich_message_part_revisions
   WHERE part_id=NEW.part_id AND revision=NEW.revision AND organization_id=NEW.organization_id FOR SHARE;
  IF next_revision.part_id IS NULL OR next_revision.content_digest<>NEW.content_digest OR
     next_revision.conversation_event_id<>NEW.conversation_event_id OR next_revision.ordinal<>NEW.ordinal THEN
    RAISE EXCEPTION 'Rich message part current pointer requires exact revision';
  END IF;
  IF TG_OP='UPDATE' THEN
    SELECT * INTO prior_revision FROM stride_rich_message_part_revisions
     WHERE part_id=OLD.part_id AND revision=OLD.revision AND organization_id=OLD.organization_id FOR SHARE;
    IF NEW.organization_id<>OLD.organization_id OR NEW.part_id<>OLD.part_id OR NEW.revision<>OLD.revision+1 OR
       prior_revision.part_id IS NULL OR prior_revision.invalidated_at IS NOT NULL OR
       next_revision.conversation_event_id<>prior_revision.conversation_event_id OR next_revision.conversation_event_revision<>prior_revision.conversation_event_revision OR
       next_revision.ordinal<>prior_revision.ordinal OR next_revision.source_id<>prior_revision.source_id OR next_revision.source_revision<>prior_revision.source_revision OR
       next_revision.source_origin_id IS DISTINCT FROM prior_revision.source_origin_id OR next_revision.source_origin_revision IS DISTINCT FROM prior_revision.source_origin_revision OR
       next_revision.blob_ref<>prior_revision.blob_ref OR next_revision.blob_digest<>prior_revision.blob_digest OR next_revision.media_type<>prior_revision.media_type OR
       next_revision.byte_size<>prior_revision.byte_size OR next_revision.destination_digest<>prior_revision.destination_digest OR next_revision.destination_revision<>prior_revision.destination_revision OR
       next_revision.author_principal<>prior_revision.author_principal OR next_revision.source_audience<>prior_revision.source_audience OR
       next_revision.source_acl_revision<>prior_revision.source_acl_revision OR next_revision.purge_generation<>prior_revision.purge_generation+1 OR
       next_revision.content_digest<>prior_revision.content_digest OR
       next_revision.invalidated_at IS NULL OR next_revision.invalidation_reason IS NULL THEN
      RAISE EXCEPTION 'Rich message part current pointer requires exact one-way invalidation';
    END IF;
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_rich_message_part_current_transition_guard
BEFORE INSERT OR UPDATE ON stride_rich_message_parts_current
FOR EACH ROW EXECUTE FUNCTION stride_rich_message_part_current_exact_transition();
CREATE FUNCTION stride_rich_message_part_current_no_delete()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'Rich message part current pointers cannot be deleted'; END; $$;
CREATE TRIGGER stride_rich_message_part_current_no_delete_guard
BEFORE DELETE ON stride_rich_message_parts_current
FOR EACH ROW EXECUTE FUNCTION stride_rich_message_part_current_no_delete();

-- Reply topology is supporting ancestry. It never changes the parent event's
-- Project classification, but invalidating the exact parent revision makes the
-- child's entire source group unauthorized-current.
CREATE TABLE stride_project_chat_reply_dependencies (
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    child_event_id text NOT NULL,
    child_event_revision bigint NOT NULL CHECK(child_event_revision>0),
    parent_event_id text NOT NULL,
    parent_event_revision bigint NOT NULL CHECK(parent_event_revision>0),
    parent_event_digest bytea NOT NULL CHECK(octet_length(parent_event_digest)=32),
    parent_author_principal text NOT NULL REFERENCES stride_person_principals(person_id),
    parent_legacy_snapshot_digest bytea NOT NULL CHECK(octet_length(parent_legacy_snapshot_digest)=32),
    parent_audience_digest bytea NOT NULL CHECK(octet_length(parent_audience_digest)=32),
    parent_acl_revision bigint NOT NULL CHECK(parent_acl_revision>0),
    parent_purge_generation bigint NOT NULL CHECK(parent_purge_generation>0),
    parent_source_authority_receipt_id text NOT NULL,
    source_manifest_digest bytea NOT NULL CHECK(octet_length(source_manifest_digest)=32),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,child_event_id),
    FOREIGN KEY(organization_id,child_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    FOREIGN KEY(organization_id,parent_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    FOREIGN KEY(parent_source_authority_receipt_id,organization_id)
      REFERENCES stride_project_source_authority_receipts(source_authority_receipt_id,organization_id),
    CHECK(child_event_id<>parent_event_id)
);
CREATE INDEX stride_project_chat_reply_parent
  ON stride_project_chat_reply_dependencies(organization_id,parent_event_id,parent_event_revision);

CREATE FUNCTION stride_project_chat_reply_dependency_exact()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE child stride_conversation_events%ROWTYPE;
DECLARE parent stride_conversation_events%ROWTYPE;
DECLARE parent_receipt stride_project_source_authority_receipts%ROWTYPE;
BEGIN
  SELECT * INTO child FROM stride_conversation_events WHERE tenant_id=NEW.organization_id AND event_id=NEW.child_event_id FOR SHARE;
  SELECT * INTO parent FROM stride_conversation_events WHERE tenant_id=NEW.organization_id AND event_id=NEW.parent_event_id FOR SHARE;
	SELECT * INTO parent_receipt FROM stride_project_source_authority_receipts
	 WHERE organization_id=NEW.organization_id AND source_authority_receipt_id=NEW.parent_source_authority_receipt_id FOR SHARE;
  IF child.event_id IS NULL OR parent.event_id IS NULL OR child.thread_id IS NULL OR child.thread_id IS DISTINCT FROM parent.thread_id OR
     child.content_revision<>NEW.child_event_revision OR child.invalidated_at IS NOT NULL OR
     child.reply_to_event_id<>parent.event_id OR parent.content_revision<>NEW.parent_event_revision OR
     parent.content_digest<>NEW.parent_event_digest OR parent.author_principal<>NEW.parent_author_principal OR parent.audience_digest<>NEW.parent_audience_digest OR
     parent.acl_version<>NEW.parent_acl_revision OR parent.purge_generation<>NEW.parent_purge_generation OR parent.invalidated_at IS NOT NULL OR
	 parent_receipt.source_authority_receipt_id IS NULL OR parent_receipt.subject_contract_type<>'conversation_event' OR
	 parent_receipt.subject_id<>parent.event_id OR parent_receipt.subject_revision<>parent.content_revision OR parent_receipt.subject_digest<>parent.content_digest OR
	 parent_receipt.source_audience->>'visibility'<>parent.visibility OR parent_receipt.source_acl_revision<>parent.acl_version OR
	 parent_receipt.source_audience<>jsonb_build_object('visibility',parent.visibility,'principals',parent_receipt.source_audience->'principals') OR
	 parent.audience_digest<>sha256(convert_to(parent_receipt.source_audience::text,'UTF8')) OR
	 parent_receipt.source_acl_digest<>sha256(convert_to(concat_ws(E'\x1f',parent.tenant_id,parent.event_id,parent.content_revision::text,
	   encode(parent.content_digest,'hex'),encode(parent.audience_digest,'hex'),parent.visibility,parent.acl_version::text,parent.purge_generation::text),'UTF8')) OR
	 parent_receipt.purge_generation<>parent.purge_generation OR parent_receipt.expires_at<=clock_timestamp() OR
	 parent_receipt.actor_person_id<>child.author_principal OR
	 NOT (parent_receipt.source_audience->'principals' @> jsonb_build_array(child.author_principal)) THEN
    RAISE EXCEPTION 'Project chat reply dependency requires exact same-thread current ancestry';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_chat_reply_dependency_exact_guard
BEFORE INSERT ON stride_project_chat_reply_dependencies
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_reply_dependency_exact();
CREATE TRIGGER stride_project_chat_reply_dependency_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_reply_dependencies
FOR EACH ROW EXECUTE FUNCTION stride_rich_message_part_revision_immutable();

-- One immutable group is the all-or-nothing Send authority unit. Every member
-- owns a separate ProjectAssociation edge, including each attachment part.
CREATE TABLE stride_project_chat_source_groups (
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    group_id text NOT NULL,
    operation_id text NOT NULL,
    operation_key_digest bytea NOT NULL CHECK(octet_length(operation_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
    source_manifest_digest bytea NOT NULL CHECK(octet_length(source_manifest_digest)=32),
    thread_id text NOT NULL,
    message_id text NOT NULL,
    conversation_event_id text NOT NULL,
    conversation_event_revision bigint NOT NULL CHECK(conversation_event_revision>0),
    project_id text NOT NULL,
    project_revision bigint NOT NULL CHECK(project_revision>0),
    root_association_id text NOT NULL,
    root_association_revision bigint NOT NULL CHECK(root_association_revision>0),
    member_count integer NOT NULL CHECK(member_count>0),
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
    session_subject_digest bytea NOT NULL,
    session_revision bigint NOT NULL CHECK(session_revision>0),
    authority_generation bigint NOT NULL CHECK(authority_generation>0),
    status text NOT NULL CHECK(status='confirmed'),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,group_id),
    UNIQUE(organization_id,operation_id),
    UNIQUE(organization_id,operation_key_digest),
    FOREIGN KEY(organization_id,conversation_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    FOREIGN KEY(project_id,project_revision,organization_id)
      REFERENCES stride_project_revisions(project_id,revision,organization_id),
    FOREIGN KEY(root_association_id,root_association_revision,organization_id)
      REFERENCES stride_project_association_revisions(association_id,revision,organization_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision)
      REFERENCES stride_organization_membership_revisions(membership_id,revision),
    FOREIGN KEY(session_subject_digest)
      REFERENCES stride_active_organization_sessions(session_subject_digest),
    CHECK(group_id=btrim(group_id) AND group_id<>''),
    CHECK(operation_id=btrim(operation_id) AND operation_id<>''),
    CHECK(thread_id=btrim(thread_id) AND thread_id<>''),
    CHECK(message_id=btrim(message_id) AND message_id<>''),
    CHECK(status='confirmed')
);

CREATE TABLE stride_project_chat_source_group_members (
    organization_id text NOT NULL,
    group_id text NOT NULL,
    ordinal integer NOT NULL CHECK(ordinal>=0),
    subject_contract_type text NOT NULL CHECK(subject_contract_type IN ('conversation_event','rich_message_part')),
    subject_id text NOT NULL,
    subject_revision bigint NOT NULL CHECK(subject_revision>0),
    subject_digest bytea NOT NULL CHECK(octet_length(subject_digest)=32),
    source_authority_receipt_id text NOT NULL,
    association_id text NOT NULL,
    association_revision bigint NOT NULL CHECK(association_revision>0),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,group_id,ordinal),
    UNIQUE(organization_id,group_id,subject_contract_type,subject_id),
    UNIQUE(organization_id,group_id,association_id),
    UNIQUE(organization_id,association_id),
    FOREIGN KEY(organization_id,group_id)
      REFERENCES stride_project_chat_source_groups(organization_id,group_id),
    FOREIGN KEY(source_authority_receipt_id,organization_id)
      REFERENCES stride_project_source_authority_receipts(source_authority_receipt_id,organization_id),
    FOREIGN KEY(association_id,association_revision,organization_id)
      REFERENCES stride_project_association_revisions(association_id,revision,organization_id),
    CHECK(subject_id=btrim(subject_id) AND subject_id<>'')
);

-- Serialize ownership of each canonical outgoing event before the group row
-- is visible. Taking this lock in a BEFORE trigger gives a waiting READ
-- COMMITTED inserter a fresh statement snapshot after the winner commits.
CREATE FUNCTION stride_project_chat_source_group_claim_event()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended(concat_ws(E'\x1f','project-chat-source-event/v1',NEW.organization_id,NEW.conversation_event_id),0));
  IF EXISTS(
    SELECT 1 FROM stride_project_chat_source_groups existing
    WHERE existing.organization_id=NEW.organization_id AND existing.conversation_event_id=NEW.conversation_event_id
      AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_invalidations invalidation
        WHERE invalidation.organization_id=existing.organization_id AND invalidation.group_id=existing.group_id)
      AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift
        WHERE drift.organization_id=existing.organization_id AND drift.group_id=existing.group_id)
  ) THEN
    RAISE EXCEPTION 'Project chat canonical event already has an active source group';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_chat_source_group_event_claim_guard
BEFORE INSERT ON stride_project_chat_source_groups
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_claim_event();

-- Corrections/removals claim the old event before the first terminal edge is
-- admitted. A waiting rival rereads the exact group current pointers after
-- the lock holder commits and deterministically fails stale; any replacement
-- rows it prepared remain inside its aborted transaction.
CREATE FUNCTION stride_project_chat_group_terminal_claim()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owned_group_id text;
DECLARE owned_organization_id text;
DECLARE owned_event_id text;
DECLARE member_revision bigint;
DECLARE current_revision bigint;
DECLARE current_state text;
BEGIN
  IF NEW.state NOT IN ('corrected','removed') THEN RETURN NEW; END IF;
  SELECT source_group.group_id,source_group.organization_id,source_group.conversation_event_id,member.association_revision
    INTO owned_group_id,owned_organization_id,owned_event_id,member_revision
  FROM stride_project_chat_source_group_members member
  JOIN stride_project_chat_source_groups source_group ON source_group.organization_id=member.organization_id AND source_group.group_id=member.group_id
  WHERE member.organization_id=NEW.organization_id AND member.association_id=NEW.association_id;
  IF owned_group_id IS NULL THEN RETURN NEW; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended(concat_ws(E'\x1f','project-chat-source-event/v1',owned_organization_id,owned_event_id),0));
  SELECT revision,state INTO current_revision,current_state FROM stride_project_associations_current
   WHERE organization_id=NEW.organization_id AND association_id=NEW.association_id FOR UPDATE;
  IF current_revision IS DISTINCT FROM member_revision OR current_state IS DISTINCT FROM 'confirmed' OR
     NEW.revision<>member_revision+1 OR NEW.supersedes_revision<>member_revision THEN
    RAISE EXCEPTION 'Project chat source group correction is stale';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_chat_group_terminal_claim_guard
BEFORE INSERT ON stride_project_association_revisions
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_group_terminal_claim();

-- Digest-only manifest storage. PostgreSQL reproduces the complete body-free
-- ordered source manifest from canonical event/part/reply rows; no arbitrary
-- client JSON or source body is retained as a second authority surface.
CREATE FUNCTION stride_project_chat_source_group_manifest_digest(group_organization text,group_identifier text)
RETURNS bytea LANGUAGE sql STABLE AS $$
  SELECT sha256(convert_to(
    concat_ws(chr(30),
      'project-chat-source-manifest/v2','thread',source_group.thread_id,
      encode(root_event.content_digest,'hex'),
      COALESCE(string_agg(
        concat_ws(chr(31),'attachment',member.ordinal::text,part.source_id,part.source_revision,part.blob_ref,
          encode(part.blob_digest,'hex'),part.media_type,part.byte_size::text,part.destination_revision,
          COALESCE(part.source_origin_id,''),COALESCE(part.source_origin_revision,''))
        ,chr(30) ORDER BY member.ordinal) FILTER(WHERE member.ordinal>0),''),
      CASE WHEN reply.parent_event_id IS NULL THEN '' ELSE concat_ws(chr(31),'reply',reply.parent_event_id,reply.parent_event_revision::text,
        encode(reply.parent_event_digest,'hex'),reply.parent_author_principal,encode(reply.parent_legacy_snapshot_digest,'hex'),encode(reply.parent_audience_digest,'hex'),
        reply.parent_acl_revision::text,reply.parent_purge_generation::text) END
    ),'UTF8'))
  FROM stride_project_chat_source_groups source_group
  JOIN stride_conversation_events root_event
    ON root_event.tenant_id=source_group.organization_id AND root_event.event_id=source_group.conversation_event_id
  JOIN stride_project_chat_source_group_members member
    ON member.organization_id=source_group.organization_id AND member.group_id=source_group.group_id
  LEFT JOIN stride_rich_message_part_revisions part
    ON member.ordinal>0 AND part.organization_id=member.organization_id AND part.part_id=member.subject_id AND part.revision=member.subject_revision
  LEFT JOIN stride_project_chat_reply_dependencies reply
    ON reply.organization_id=source_group.organization_id AND reply.child_event_id=source_group.conversation_event_id
  WHERE source_group.organization_id=group_organization AND source_group.group_id=group_identifier
  GROUP BY source_group.thread_id,root_event.content_digest,reply.parent_event_id,reply.parent_event_revision,
    reply.parent_event_digest,reply.parent_author_principal,reply.parent_legacy_snapshot_digest,reply.parent_audience_digest,reply.parent_acl_revision,reply.parent_purge_generation;
$$;

-- Invalidation is an immutable one-way receipt, never an in-place group edit.
CREATE TABLE stride_project_chat_source_group_invalidations (
    organization_id text NOT NULL,
    group_id text NOT NULL,
    operation_id text NOT NULL,
    operation_key_digest bytea NOT NULL CHECK(octet_length(operation_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
    reason text NOT NULL,
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
    session_subject_digest bytea NOT NULL,
    session_revision bigint NOT NULL CHECK(session_revision>0),
    authority_generation bigint NOT NULL CHECK(authority_generation>0),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,group_id),
    UNIQUE(organization_id,operation_id),
    UNIQUE(organization_id,operation_key_digest),
    FOREIGN KEY(organization_id,group_id) REFERENCES stride_project_chat_source_groups(organization_id,group_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision) REFERENCES stride_organization_membership_revisions(membership_id,revision),
    FOREIGN KEY(session_subject_digest) REFERENCES stride_active_organization_sessions(session_subject_digest),
    CHECK(operation_id=btrim(operation_id) AND operation_id<>''),
    CHECK(reason=btrim(reason) AND reason<>'')
);

-- Group correction is a group-to-group CAS. The replacement group must cover
-- the same canonical event/parts in the same order; only Project edges change.
CREATE TABLE stride_project_chat_source_group_correction_receipts (
    organization_id text NOT NULL,
    operation_id text NOT NULL,
    operation_key_digest bytea NOT NULL CHECK(octet_length(operation_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
    old_group_id text NOT NULL,
    replacement_group_id text,
    result_state text NOT NULL CHECK(result_state IN ('corrected','removed')),
    context_revision bigint NOT NULL CHECK(context_revision>1),
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
    session_subject_digest bytea NOT NULL,
    session_revision bigint NOT NULL CHECK(session_revision>0),
    authority_generation bigint NOT NULL CHECK(authority_generation>0),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,operation_id),
    UNIQUE(organization_id,operation_key_digest),
    UNIQUE(organization_id,old_group_id),
    FOREIGN KEY(organization_id,old_group_id) REFERENCES stride_project_chat_source_groups(organization_id,group_id),
    FOREIGN KEY(organization_id,replacement_group_id) REFERENCES stride_project_chat_source_groups(organization_id,group_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision) REFERENCES stride_organization_membership_revisions(membership_id,revision),
    FOREIGN KEY(session_subject_digest) REFERENCES stride_active_organization_sessions(session_subject_digest),
    CHECK((result_state='corrected')=(replacement_group_id IS NOT NULL)),
    CHECK(replacement_group_id IS NULL OR replacement_group_id<>old_group_id)
);

CREATE TABLE stride_project_chat_source_group_drift_receipts (
    organization_id text NOT NULL,
    group_id text NOT NULL,
    operation_id text NOT NULL,
    operation_key_digest bytea NOT NULL CHECK(octet_length(operation_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
    drift_contract_type text NOT NULL CHECK(drift_contract_type IN ('conversation_event','rich_message_part','reply_dependency')),
    drift_subject_id text NOT NULL,
    expected_revision bigint NOT NULL CHECK(expected_revision>0),
    expected_digest bytea NOT NULL CHECK(octet_length(expected_digest)=32),
    reason text NOT NULL,
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,group_id),
    UNIQUE(organization_id,operation_id),
    UNIQUE(organization_id,operation_key_digest),
    FOREIGN KEY(organization_id,group_id) REFERENCES stride_project_chat_source_groups(organization_id,group_id),
    CHECK(operation_id=btrim(operation_id) AND operation_id<>''),
    CHECK(drift_subject_id=btrim(drift_subject_id) AND drift_subject_id<>''),
    CHECK(reason=btrim(reason) AND reason<>'')
);

CREATE TABLE stride_project_chat_source_group_authority_loss_receipts (
    organization_id text NOT NULL,
    group_id text NOT NULL,
    operation_id text NOT NULL,
    operation_key_digest bytea NOT NULL CHECK(octet_length(operation_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
    project_id text NOT NULL,
    expected_project_revision bigint NOT NULL CHECK(expected_project_revision>0),
    expected_project_digest bytea NOT NULL CHECK(octet_length(expected_project_digest)=32),
    reason text NOT NULL CHECK(reason IN ('project_archived','project_audience_revoked')),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,group_id),
    UNIQUE(organization_id,operation_id),
    UNIQUE(organization_id,operation_key_digest),
    FOREIGN KEY(organization_id,group_id) REFERENCES stride_project_chat_source_groups(organization_id,group_id),
    FOREIGN KEY(project_id,expected_project_revision,organization_id) REFERENCES stride_project_revisions(project_id,revision,organization_id)
);

-- The 0018 receipt was event-only by constraint, FK and triggers. Replace all
-- three with a polymorphic exact-current guard; an unattested legacy source id
-- or blob grant can never satisfy this SQL boundary.
-- PostgreSQL truncates generated constraint names to NAMEDATALEN, so dropping
-- the source FK by its authored name is not reliable on an upgraded database.
-- Resolve the legacy event-only FK semantically from the catalogs instead.
DO $$
DECLARE constraint_record record;
BEGIN
  FOR constraint_record IN
    SELECT constraint_row.conname
      FROM pg_constraint constraint_row
     WHERE constraint_row.conrelid='stride_project_source_authority_receipts'::regclass
       AND constraint_row.contype='f'
       AND constraint_row.confrelid='stride_conversation_events'::regclass
  LOOP
    EXECUTE format('ALTER TABLE stride_project_source_authority_receipts DROP CONSTRAINT %I',constraint_record.conname);
  END LOOP;
END; $$;
DO $$
DECLARE constraint_record record;
BEGIN
  FOR constraint_record IN
    SELECT conname,pg_get_constraintdef(oid) AS definition
      FROM pg_constraint
     WHERE conrelid='stride_project_source_authority_receipts'::regclass AND contype='c'
  LOOP
    IF constraint_record.definition LIKE '%subject_contract_type = ''conversation_event''%' OR
       (constraint_record.definition LIKE '%stride_project_ref_array_valid(source_refs%' AND
        constraint_record.definition NOT LIKE '%rich_message_part%') THEN
      EXECUTE format('ALTER TABLE stride_project_source_authority_receipts DROP CONSTRAINT %I',constraint_record.conname);
    END IF;
  END LOOP;
END; $$;
ALTER TABLE stride_project_source_authority_receipts
  DROP CONSTRAINT IF EXISTS stride_project_source_authority_receipts_visibility_check;
DO $$
DECLARE constraint_record record;
BEGIN
  FOR constraint_record IN SELECT conname,pg_get_constraintdef(oid) AS definition FROM pg_constraint
    WHERE conrelid='stride_project_source_authority_receipts'::regclass AND contype='c'
  LOOP
    IF constraint_record.definition LIKE '%source_audience%visibility%private%' AND
       constraint_record.definition NOT LIKE '%project%' AND constraint_record.definition NOT LIKE '%channel%' THEN
      EXECUTE format('ALTER TABLE stride_project_source_authority_receipts DROP CONSTRAINT %I',constraint_record.conname);
    END IF;
  END LOOP;
END; $$;
ALTER TABLE stride_project_source_authority_receipts
  ADD CONSTRAINT stride_project_source_authority_receipts_subject_type_v2
    CHECK(subject_contract_type IN ('conversation_event','rich_message_part')),
  ADD CONSTRAINT stride_project_source_authority_receipts_refs_v2
    CHECK(stride_project_ref_array_valid(source_refs,ARRAY['conversation_event','rich_message_part']) AND jsonb_array_length(source_refs)=1),
  ADD CONSTRAINT stride_project_source_authority_receipts_visibility_v2
    CHECK(source_audience->>'visibility' IN ('private','project','channel'));

CREATE OR REPLACE FUNCTION stride_project_source_receipt_requires_canonical_source()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_event stride_conversation_events%ROWTYPE;
DECLARE source_part stride_rich_message_part_revisions%ROWTYPE;
BEGIN
  IF NEW.subject_contract_type='conversation_event' THEN
    SELECT * INTO source_event FROM stride_conversation_events
     WHERE tenant_id=NEW.organization_id AND event_id=NEW.subject_id FOR SHARE;
    IF source_event.event_id IS NULL OR source_event.content_revision<>NEW.subject_revision OR
       source_event.content_digest<>NEW.subject_digest OR source_event.invalidated_at IS NOT NULL OR
       source_event.purge_generation<>NEW.purge_generation OR source_event.visibility NOT IN ('private','project','channel') OR
       NEW.source_audience->>'visibility'<>source_event.visibility OR
       NOT (NEW.source_audience->'principals' @> jsonb_build_array(NEW.actor_person_id)) OR
       source_event.acl_version<>NEW.source_acl_revision OR
       source_event.audience_digest<>sha256(convert_to(NEW.source_audience::text,'UTF8')) OR
       NEW.source_acl_digest<>sha256(convert_to(concat_ws(E'\x1f',source_event.tenant_id,source_event.event_id,source_event.content_revision::text,encode(source_event.content_digest,'hex'),encode(source_event.audience_digest,'hex'),source_event.visibility,source_event.acl_version::text,source_event.purge_generation::text),'UTF8')) OR
       NEW.source_refs<>jsonb_build_array(jsonb_build_object('contractType','conversation_event','id',source_event.event_id,'revision',source_event.content_revision,'digest',encode(source_event.content_digest,'hex'))) THEN
      RAISE EXCEPTION 'Project source receipt requires exact authorized canonical conversation event';
    END IF;
  ELSIF NEW.subject_contract_type='rich_message_part' THEN
    SELECT * INTO source_part FROM stride_rich_message_part_revisions
     WHERE organization_id=NEW.organization_id AND part_id=NEW.subject_id AND revision=NEW.subject_revision FOR SHARE;
    IF source_part.part_id IS NULL OR source_part.author_principal<>NEW.actor_person_id OR
       NOT EXISTS(SELECT 1 FROM stride_rich_message_parts_current current_part WHERE current_part.organization_id=NEW.organization_id
         AND current_part.part_id=NEW.subject_id AND current_part.revision=NEW.subject_revision AND current_part.content_digest=NEW.subject_digest) OR
       source_part.content_digest<>NEW.subject_digest OR source_part.invalidated_at IS NOT NULL OR
       source_part.purge_generation<>NEW.purge_generation OR source_part.source_acl_revision<>NEW.source_acl_revision OR
       source_part.source_audience<>NEW.source_audience OR
       NOT (NEW.source_audience->'principals' @> jsonb_build_array(NEW.actor_person_id)) OR
       NEW.source_acl_digest<>sha256(convert_to(concat_ws(E'\x1f',source_part.organization_id,source_part.part_id,source_part.revision::text,encode(source_part.content_digest,'hex'),encode(sha256(convert_to(source_part.source_audience::text,'UTF8')),'hex'),source_part.source_acl_revision::text,source_part.purge_generation::text),'UTF8')) OR
       NEW.source_refs<>jsonb_build_array(jsonb_build_object('contractType','rich_message_part','id',source_part.part_id,'revision',source_part.revision,'digest',encode(source_part.content_digest,'hex'))) THEN
      RAISE EXCEPTION 'Project source receipt requires exact authorized canonical rich message part';
    END IF;
  ELSE
    RAISE EXCEPTION 'Project source receipt subject type is unsupported';
  END IF;
  RETURN NEW;
END; $$;

CREATE OR REPLACE FUNCTION stride_project_association_requires_source_receipt()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE receipt stride_project_source_authority_receipts%ROWTYPE;
DECLARE drift_member stride_project_chat_source_group_members%ROWTYPE;
DECLARE prior_association stride_project_association_revisions%ROWTYPE;
DECLARE drift_receipt stride_project_chat_source_group_drift_receipts%ROWTYPE;
DECLARE authority_loss_receipt stride_project_chat_source_group_authority_loss_receipts%ROWTYPE;
DECLARE terminal_key bytea;
DECLARE terminal_operation text;
BEGIN
	-- Exact server-owned drift revocation deliberately does not require the
	-- original source receipt or user session to remain current. It may only
	-- append one revoked revision over the exact confirmed member named by a
	-- drift receipt in this same transaction; the deferred drift/group guards
	-- require every sibling, event, current pointer and purge outbox to complete.
	IF NEW.state='revoked' THEN
	  SELECT member.* INTO drift_member FROM stride_project_chat_source_group_members member
	  WHERE member.organization_id=NEW.organization_id AND member.association_id=NEW.association_id
	    AND (EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift WHERE drift.organization_id=member.organization_id AND drift.group_id=member.group_id)
	      OR EXISTS(SELECT 1 FROM stride_project_chat_source_group_authority_loss_receipts authority_loss WHERE authority_loss.organization_id=member.organization_id AND authority_loss.group_id=member.group_id)) FOR SHARE;
	  IF drift_member.association_id IS NOT NULL THEN
	    SELECT drift.* INTO drift_receipt FROM stride_project_chat_source_group_drift_receipts drift
	     WHERE drift.organization_id=NEW.organization_id AND drift.group_id=drift_member.group_id FOR SHARE;
	    SELECT authority_loss.* INTO authority_loss_receipt FROM stride_project_chat_source_group_authority_loss_receipts authority_loss
	     WHERE authority_loss.organization_id=NEW.organization_id AND authority_loss.group_id=drift_member.group_id FOR SHARE;
	    terminal_key:=COALESCE(drift_receipt.operation_key_digest,authority_loss_receipt.operation_key_digest);
	    terminal_operation:=COALESCE(drift_receipt.operation_id,authority_loss_receipt.operation_id);
	    SELECT * INTO prior_association FROM stride_project_association_revisions
	     WHERE organization_id=NEW.organization_id AND association_id=NEW.association_id AND revision=drift_member.association_revision FOR SHARE;
	    IF prior_association.association_id IS NULL OR prior_association.state<>'confirmed' OR
	       NEW.revision<>prior_association.revision+1 OR NEW.supersedes_revision<>prior_association.revision OR NEW.supersedes_digest<>prior_association.content_digest OR
	       NEW.project_id<>prior_association.project_id OR NEW.project_revision<>prior_association.project_revision OR
	       NEW.subject_contract_type<>prior_association.subject_contract_type OR NEW.subject_id<>prior_association.subject_id OR
	       NEW.subject_revision<>prior_association.subject_revision OR NEW.subject_digest<>prior_association.subject_digest OR
	       NEW.source_refs<>prior_association.source_refs OR NEW.source_authority_receipt_id<>prior_association.source_authority_receipt_id OR
	       NEW.evidence_coverage_digest<>prior_association.evidence_coverage_digest OR NEW.basis<>prior_association.basis OR
	       NEW.classifier_revision<>prior_association.classifier_revision OR NEW.confidence<>prior_association.confidence OR
	       NEW.source_audience<>prior_association.source_audience OR NEW.source_acl_revision<>prior_association.source_acl_revision OR
	       NEW.source_acl_digest<>prior_association.source_acl_digest OR NEW.consent_revision<>prior_association.consent_revision OR
	       NEW.purge_generation<>prior_association.purge_generation OR NEW.idempotency_key_digest<>terminal_key OR
	       NEW.expires_at IS NOT NULL OR NEW.replacement_association_id IS NOT NULL OR
	       NEW.actor_person_id<>prior_association.actor_person_id OR NEW.actor_membership_id<>prior_association.actor_membership_id OR
	       NEW.actor_membership_revision<>prior_association.actor_membership_revision OR NEW.session_subject_digest<>prior_association.session_subject_digest OR
	       NEW.session_revision<>prior_association.session_revision OR NEW.authority_generation<>prior_association.authority_generation OR
	       NEW.content_digest<>sha256(convert_to(concat_ws(E'\x1f','project-association/drift-revoked/v1',NEW.association_id,
	         NEW.revision::text,encode(prior_association.content_digest,'hex'),terminal_operation),'UTF8')) THEN
	      RAISE EXCEPTION 'ProjectAssociation drift revocation must supersede exact confirmed group member';
	    END IF;
	    RETURN NEW;
	  END IF;
	END IF;
  SELECT * INTO receipt FROM stride_project_source_authority_receipts
   WHERE source_authority_receipt_id=NEW.source_authority_receipt_id AND organization_id=NEW.organization_id FOR SHARE;
  IF receipt.source_authority_receipt_id IS NULL OR receipt.expires_at<=clock_timestamp() OR
     receipt.subject_contract_type<>NEW.subject_contract_type OR receipt.subject_id<>NEW.subject_id OR
     receipt.subject_revision<>NEW.subject_revision OR receipt.subject_digest<>NEW.subject_digest OR
     receipt.source_refs<>NEW.source_refs OR receipt.evidence_coverage_digest<>NEW.evidence_coverage_digest OR
     receipt.source_audience<>NEW.source_audience OR receipt.source_acl_revision<>NEW.source_acl_revision OR
     receipt.source_acl_digest<>NEW.source_acl_digest OR receipt.consent_revision<>NEW.consent_revision OR
     receipt.purge_generation<>NEW.purge_generation OR receipt.actor_person_id<>NEW.actor_person_id OR
     receipt.actor_membership_id<>NEW.actor_membership_id OR receipt.actor_membership_revision<>NEW.actor_membership_revision OR
     receipt.session_subject_digest<>NEW.session_subject_digest OR receipt.session_revision<>NEW.session_revision OR
     receipt.authority_generation<>NEW.authority_generation OR
     (NEW.subject_contract_type='conversation_event' AND NOT EXISTS(
       SELECT 1 FROM stride_conversation_events source_event
       WHERE source_event.tenant_id=NEW.organization_id AND source_event.event_id=NEW.subject_id
         AND source_event.content_revision=NEW.subject_revision AND source_event.content_digest=NEW.subject_digest
         AND source_event.invalidated_at IS NULL
         AND source_event.audience_digest=sha256(convert_to(NEW.source_audience::text,'UTF8'))
         AND source_event.visibility=NEW.source_audience->>'visibility'
         AND source_event.acl_version=NEW.source_acl_revision AND source_event.purge_generation=NEW.purge_generation)) OR
     (NEW.subject_contract_type='rich_message_part' AND NOT EXISTS(
       SELECT 1 FROM stride_rich_message_part_revisions source_part
       JOIN stride_rich_message_parts_current current_part ON current_part.organization_id=source_part.organization_id
         AND current_part.part_id=source_part.part_id AND current_part.revision=source_part.revision
         AND current_part.content_digest=source_part.content_digest
       WHERE source_part.organization_id=NEW.organization_id AND source_part.part_id=NEW.subject_id
         AND source_part.revision=NEW.subject_revision AND source_part.content_digest=NEW.subject_digest
         AND source_part.invalidated_at IS NULL AND source_part.source_audience=NEW.source_audience
         AND source_part.source_acl_revision=NEW.source_acl_revision AND source_part.purge_generation=NEW.purge_generation)) THEN
    RAISE EXCEPTION 'ProjectAssociation requires exact current source authority receipt';
  END IF;
  RETURN NEW;
END; $$;

-- Replace the generic event authority guard with a narrow drift exception.
-- All human operations keep the original current-session requirement.
CREATE OR REPLACE FUNCTION stride_project_operation_requires_current_authority()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE membership stride_organization_memberships_current%ROWTYPE;
DECLARE session_binding stride_active_organization_sessions%ROWTYPE;
DECLARE authority_at timestamptz;
DECLARE drift_member stride_project_chat_source_group_members%ROWTYPE;
DECLARE drift_receipt stride_project_chat_source_group_drift_receipts%ROWTYPE;
DECLARE authority_loss_receipt stride_project_chat_source_group_authority_loss_receipts%ROWTYPE;
DECLARE prior_association stride_project_association_revisions%ROWTYPE;
BEGIN
  IF TG_TABLE_NAME='stride_project_association_events' AND to_jsonb(NEW)->>'resulting_state'='revoked' THEN
    SELECT member.* INTO drift_member FROM stride_project_chat_source_group_members member
    WHERE member.organization_id=NEW.organization_id AND member.association_id=NEW.association_id
      AND (EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift WHERE drift.organization_id=member.organization_id AND drift.group_id=member.group_id)
        OR EXISTS(SELECT 1 FROM stride_project_chat_source_group_authority_loss_receipts authority_loss WHERE authority_loss.organization_id=member.organization_id AND authority_loss.group_id=member.group_id)) FOR SHARE;
    SELECT drift.* INTO drift_receipt FROM stride_project_chat_source_group_drift_receipts drift
     WHERE drift.organization_id=NEW.organization_id AND drift.group_id=drift_member.group_id FOR SHARE;
    SELECT authority_loss.* INTO authority_loss_receipt FROM stride_project_chat_source_group_authority_loss_receipts authority_loss
     WHERE authority_loss.organization_id=NEW.organization_id AND authority_loss.group_id=drift_member.group_id FOR SHARE;
    SELECT association.* INTO prior_association FROM stride_project_association_revisions association
     WHERE association.organization_id=NEW.organization_id AND association.association_id=NEW.association_id
       AND association.revision=drift_member.association_revision FOR SHARE;
    IF drift_member.association_id IS NOT NULL AND NEW.association_revision=drift_member.association_revision+1 AND
	   prior_association.association_id IS NOT NULL AND NEW.action='revoke' AND NEW.prior_revision=drift_member.association_revision AND
	   NEW.new_revision=NEW.association_revision AND NEW.actor_person_id=prior_association.actor_person_id AND
	   NEW.actor_membership_id=prior_association.actor_membership_id AND NEW.actor_membership_revision=prior_association.actor_membership_revision AND
	   NEW.session_subject_digest=prior_association.session_subject_digest AND NEW.session_revision=prior_association.session_revision AND
	   NEW.authority_generation=prior_association.authority_generation AND NEW.idempotency_key_digest=COALESCE(drift_receipt.operation_key_digest,authority_loss_receipt.operation_key_digest) AND
	   NEW.request_fingerprint=sha256(convert_to(concat_ws(E'\x1f','project-association-event/drift-revoke/v1',
	     NEW.organization_id,NEW.association_id,NEW.association_revision::text,COALESCE(drift_receipt.operation_id,authority_loss_receipt.operation_id)),'UTF8')) AND
	   NEW.occurred_at=COALESCE(drift_receipt.recorded_at,authority_loss_receipt.recorded_at) THEN
      RETURN NEW;
    END IF;
  END IF;
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

-- Authorized-current is polymorphic and additionally requires every member of
-- its immutable group plus exact reply ancestry to remain current.
CREATE OR REPLACE VIEW stride_project_associations_authorized_current AS
SELECT current_association.*
  FROM stride_project_associations_current current_association
  JOIN stride_project_association_revisions association_revision
    ON association_revision.association_id=current_association.association_id
   AND association_revision.revision=current_association.revision
   AND association_revision.organization_id=current_association.organization_id
  JOIN stride_project_source_authority_receipts receipt
    ON receipt.source_authority_receipt_id=association_revision.source_authority_receipt_id
   AND receipt.organization_id=association_revision.organization_id
  JOIN stride_projects_current current_project
    ON current_project.organization_id=association_revision.organization_id
   AND current_project.project_id=association_revision.project_id
   AND current_project.lifecycle<>'archived'
  JOIN stride_project_revisions visible_project
    ON visible_project.organization_id=current_project.organization_id AND visible_project.project_id=current_project.project_id
   AND visible_project.revision=current_project.revision
  JOIN stride_organization_memberships_current current_membership
    ON current_membership.organization_id=association_revision.organization_id
   AND current_membership.person_id=association_revision.actor_person_id
   AND current_membership.status='active'
  LEFT JOIN stride_conversation_events source_event
    ON association_revision.subject_contract_type='conversation_event'
   AND source_event.tenant_id=association_revision.organization_id AND source_event.event_id=association_revision.subject_id
  LEFT JOIN stride_rich_message_part_revisions source_part
    ON association_revision.subject_contract_type='rich_message_part'
   AND source_part.organization_id=association_revision.organization_id AND source_part.part_id=association_revision.subject_id
   AND source_part.revision=association_revision.subject_revision
 LEFT JOIN stride_rich_message_parts_current source_part_current
    ON association_revision.subject_contract_type='rich_message_part'
   AND source_part_current.organization_id=association_revision.organization_id AND source_part_current.part_id=association_revision.subject_id
 WHERE current_association.state='confirmed'
   AND (visible_project.audience->'principals' @> jsonb_build_array(association_revision.actor_person_id)
     OR visible_project.controller_memberships @> jsonb_build_array(jsonb_build_object('contractType','organization_membership',
       'id',current_membership.membership_id,'revision',current_membership.revision)))
   AND ((association_revision.subject_contract_type='conversation_event' AND source_event.invalidated_at IS NULL
          AND source_event.content_revision=association_revision.subject_revision AND source_event.content_digest=association_revision.subject_digest
          AND source_event.audience_digest=sha256(convert_to(association_revision.source_audience::text,'UTF8'))
          AND source_event.visibility=association_revision.source_audience->>'visibility' AND source_event.acl_version=association_revision.source_acl_revision
          AND source_event.purge_generation=association_revision.purge_generation)
     OR (association_revision.subject_contract_type='rich_message_part' AND source_part.invalidated_at IS NULL
          AND source_part.content_digest=association_revision.subject_digest AND source_part.source_audience=association_revision.source_audience
          AND source_part.source_acl_revision=association_revision.source_acl_revision AND source_part.purge_generation=association_revision.purge_generation
          AND source_part_current.revision=association_revision.subject_revision AND source_part_current.content_digest=association_revision.subject_digest))
   AND association_revision.source_audience->'principals' @> jsonb_build_array(association_revision.actor_person_id)
   AND NOT EXISTS (SELECT 1 FROM stride_project_chat_source_group_members owned_member
     JOIN stride_project_chat_source_group_invalidations invalidation
       ON invalidation.organization_id=owned_member.organization_id AND invalidation.group_id=owned_member.group_id
     WHERE owned_member.organization_id=current_association.organization_id AND owned_member.association_id=current_association.association_id)
   AND NOT EXISTS (SELECT 1 FROM stride_project_chat_source_group_members owned_member
     JOIN stride_project_chat_source_group_drift_receipts drift
       ON drift.organization_id=owned_member.organization_id AND drift.group_id=owned_member.group_id
     WHERE owned_member.organization_id=current_association.organization_id AND owned_member.association_id=current_association.association_id)
   AND NOT EXISTS (SELECT 1 FROM stride_project_chat_source_group_members owned_member
     JOIN stride_project_chat_source_group_authority_loss_receipts authority_loss
       ON authority_loss.organization_id=owned_member.organization_id AND authority_loss.group_id=owned_member.group_id
     WHERE owned_member.organization_id=current_association.organization_id AND owned_member.association_id=current_association.association_id)
   AND NOT EXISTS (
     SELECT 1 FROM stride_project_chat_source_group_members member
     JOIN stride_project_chat_source_groups source_group
       ON source_group.organization_id=member.organization_id AND source_group.group_id=member.group_id
     WHERE member.organization_id=current_association.organization_id AND member.association_id=current_association.association_id
       AND (EXISTS (SELECT 1 FROM stride_project_chat_source_group_invalidations invalidation
                     WHERE invalidation.organization_id=source_group.organization_id AND invalidation.group_id=source_group.group_id) OR EXISTS (
         SELECT 1 FROM stride_project_chat_source_group_members sibling
         JOIN stride_project_source_authority_receipts sibling_receipt
           ON sibling_receipt.organization_id=sibling.organization_id AND sibling_receipt.source_authority_receipt_id=sibling.source_authority_receipt_id
         LEFT JOIN stride_conversation_events sibling_event
           ON sibling.subject_contract_type='conversation_event' AND sibling_event.tenant_id=sibling.organization_id AND sibling_event.event_id=sibling.subject_id
         LEFT JOIN stride_rich_message_part_revisions sibling_part
           ON sibling.subject_contract_type='rich_message_part' AND sibling_part.organization_id=sibling.organization_id
          AND sibling_part.part_id=sibling.subject_id AND sibling_part.revision=sibling.subject_revision
         WHERE sibling.organization_id=member.organization_id AND sibling.group_id=member.group_id
           AND ((sibling.subject_contract_type='conversation_event' AND (sibling_event.invalidated_at IS NOT NULL OR sibling_event.content_revision<>sibling.subject_revision OR sibling_event.content_digest<>sibling.subject_digest
                OR (sibling.ordinal=0 AND (sibling_event.thread_id IS DISTINCT FROM source_group.thread_id OR sibling_event.source_id IS DISTINCT FROM source_group.message_id
                  OR sibling_event.author_principal IS DISTINCT FROM source_group.actor_person_id))
                OR sibling_event.audience_digest<>sha256(convert_to(sibling_receipt.source_audience::text,'UTF8')) OR sibling_event.visibility<>sibling_receipt.source_audience->>'visibility'
                OR sibling_event.acl_version<>sibling_receipt.source_acl_revision OR sibling_event.purge_generation<>sibling_receipt.purge_generation))
             OR (sibling.subject_contract_type='rich_message_part' AND (sibling_part.part_id IS NULL OR sibling_part.invalidated_at IS NOT NULL OR sibling_part.content_digest<>sibling.subject_digest
                OR sibling_part.source_audience<>sibling_receipt.source_audience OR sibling_part.source_acl_revision<>sibling_receipt.source_acl_revision
                OR sibling_part.purge_generation<>sibling_receipt.purge_generation
                OR NOT EXISTS (SELECT 1 FROM stride_rich_message_parts_current part_current WHERE part_current.organization_id=sibling.organization_id
                    AND part_current.part_id=sibling.subject_id AND part_current.revision=sibling.subject_revision AND part_current.content_digest=sibling.subject_digest))))
       )))
   AND NOT EXISTS (
     SELECT 1 FROM stride_project_chat_reply_dependencies dependency
     JOIN stride_project_chat_source_groups source_group
       ON source_group.organization_id=dependency.organization_id AND source_group.conversation_event_id=dependency.child_event_id
     LEFT JOIN stride_conversation_events parent
       ON parent.tenant_id=dependency.organization_id AND parent.event_id=dependency.parent_event_id
     WHERE source_group.organization_id=current_association.organization_id
       AND EXISTS (SELECT 1 FROM stride_project_chat_source_group_members member
                    WHERE member.organization_id=source_group.organization_id AND member.group_id=source_group.group_id
                      AND member.association_id=current_association.association_id)
       AND (parent.event_id IS NULL OR parent.invalidated_at IS NOT NULL OR parent.thread_id IS DISTINCT FROM source_group.thread_id OR parent.content_revision<>dependency.parent_event_revision
         OR parent.content_digest<>dependency.parent_event_digest OR parent.audience_digest<>dependency.parent_audience_digest
         OR parent.author_principal<>dependency.parent_author_principal OR parent.acl_version<>dependency.parent_acl_revision OR
		 parent.purge_generation<>dependency.parent_purge_generation OR NOT EXISTS(
		   SELECT 1 FROM stride_project_source_authority_receipts parent_receipt
		   WHERE parent_receipt.organization_id=dependency.organization_id
		     AND parent_receipt.source_authority_receipt_id=dependency.parent_source_authority_receipt_id
		     AND parent_receipt.subject_id=parent.event_id AND parent_receipt.subject_revision=parent.content_revision
		     AND parent_receipt.subject_digest=parent.content_digest AND parent_receipt.source_audience->'principals' @> jsonb_build_array(source_group.actor_person_id))));

CREATE FUNCTION stride_project_chat_source_group_immutable()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  RAISE EXCEPTION 'Project chat source groups are append-only';
END; $$;
CREATE TRIGGER stride_project_chat_source_group_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_source_group_members
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_immutable();
CREATE TRIGGER stride_project_chat_source_group_header_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_source_groups
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_immutable();
CREATE TRIGGER stride_project_chat_source_group_invalidation_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_source_group_invalidations
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_immutable();
CREATE TRIGGER stride_project_chat_source_group_correction_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_source_group_correction_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_immutable();
CREATE TRIGGER stride_project_chat_source_group_drift_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_source_group_drift_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_immutable();
CREATE TRIGGER stride_project_chat_source_group_authority_loss_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_source_group_authority_loss_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_immutable();

CREATE FUNCTION stride_project_chat_source_group_requires_exact_truth()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actual_members integer;
DECLARE root_member stride_project_chat_source_group_members%ROWTYPE;
DECLARE member_record record;
DECLARE membership stride_organization_memberships_current%ROWTYPE;
DECLARE session_binding stride_active_organization_sessions%ROWTYPE;
DECLARE active_groups integer;
DECLARE joined_members integer:=0;
DECLARE root_reply_id text;
DECLARE minimum_ordinal integer;
DECLARE maximum_ordinal integer;
BEGIN
  SELECT count(*) INTO actual_members FROM stride_project_chat_source_group_members
   WHERE organization_id=NEW.organization_id AND group_id=NEW.group_id;
	SELECT min(ordinal),max(ordinal) INTO minimum_ordinal,maximum_ordinal FROM stride_project_chat_source_group_members
	 WHERE organization_id=NEW.organization_id AND group_id=NEW.group_id;
  SELECT * INTO root_member FROM stride_project_chat_source_group_members
   WHERE organization_id=NEW.organization_id AND group_id=NEW.group_id AND ordinal=0;
  SELECT * INTO membership FROM stride_organization_memberships_current WHERE membership_id=NEW.actor_membership_id FOR SHARE;
  SELECT * INTO session_binding FROM stride_active_organization_sessions WHERE session_subject_digest=NEW.session_subject_digest FOR SHARE;
  IF actual_members<>NEW.member_count OR minimum_ordinal<>0 OR maximum_ordinal<>NEW.member_count-1 OR root_member.association_id IS NULL OR
     root_member.subject_contract_type<>'conversation_event' OR root_member.subject_id<>NEW.conversation_event_id OR
     root_member.subject_revision<>NEW.conversation_event_revision OR
     root_member.association_id<>NEW.root_association_id OR root_member.association_revision<>NEW.root_association_revision OR
     membership.membership_id IS NULL OR membership.organization_id<>NEW.organization_id OR membership.person_id<>NEW.actor_person_id OR
     membership.revision<>NEW.actor_membership_revision OR membership.status<>'active' OR
     session_binding.session_subject_digest IS NULL OR session_binding.organization_id<>NEW.organization_id OR
     session_binding.person_id<>NEW.actor_person_id OR session_binding.membership_id<>NEW.actor_membership_id OR
     session_binding.membership_revision<>NEW.actor_membership_revision OR session_binding.session_revision<>NEW.session_revision OR
     session_binding.authority_generation<>NEW.authority_generation OR session_binding.status<>'active' OR session_binding.expires_at<=clock_timestamp() THEN
    RAISE EXCEPTION 'Project chat source group requires exact complete member truth';
  END IF;
	IF NEW.request_fingerprint<>sha256(convert_to(concat_ws(E'\x1f','project-chat-source-group/v1',NEW.organization_id,
	   NEW.group_id,NEW.operation_id,encode(NEW.operation_key_digest,'hex'),encode(NEW.source_manifest_digest,'hex'),NEW.thread_id,
	   NEW.message_id,NEW.conversation_event_id,NEW.conversation_event_revision::text,NEW.project_id,NEW.project_revision::text,
	   NEW.root_association_id,NEW.root_association_revision::text,NEW.member_count::text,NEW.actor_person_id,NEW.actor_membership_id,
	   NEW.actor_membership_revision::text,encode(NEW.session_subject_digest,'hex'),NEW.session_revision::text,NEW.authority_generation::text),'UTF8')) THEN
	  RAISE EXCEPTION 'Project chat source group request fingerprint is not reproducible';
	END IF;
	IF NEW.recorded_at<session_binding.bound_at OR NEW.recorded_at>clock_timestamp()+interval '1 minute' OR session_binding.expires_at<=NEW.recorded_at THEN
	  RAISE EXCEPTION 'Project chat source group recorded_at requires current session window';
	END IF;
  FOR member_record IN
    SELECT member.*,association.*,association.source_authority_receipt_id AS association_receipt_id,
           current_association.revision AS current_revision,current_association.state AS current_state,
           receipt.subject_contract_type AS receipt_type,receipt.subject_id AS receipt_subject_id,
           receipt.subject_revision AS receipt_subject_revision,receipt.subject_digest AS receipt_subject_digest,
		   receipt.expires_at AS receipt_expires_at,
           part.ordinal AS part_ordinal,part.author_principal AS part_author,
           part.conversation_event_id AS part_event_id,part.conversation_event_revision AS part_event_revision,
           root_event.thread_id AS root_thread_id,root_event.source_id AS root_source_id,root_event.author_principal AS root_author
      FROM stride_project_chat_source_group_members member
      JOIN stride_project_association_revisions association
        ON association.organization_id=member.organization_id AND association.association_id=member.association_id AND association.revision=member.association_revision
      JOIN stride_project_associations_current current_association
        ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
      JOIN stride_project_source_authority_receipts receipt
        ON receipt.organization_id=member.organization_id AND receipt.source_authority_receipt_id=member.source_authority_receipt_id
      LEFT JOIN stride_rich_message_part_revisions part
        ON member.subject_contract_type='rich_message_part' AND part.organization_id=member.organization_id
       AND part.part_id=member.subject_id AND part.revision=member.subject_revision
      LEFT JOIN stride_conversation_events root_event
        ON member.subject_contract_type='conversation_event' AND root_event.tenant_id=member.organization_id AND root_event.event_id=member.subject_id
     WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id ORDER BY member.ordinal
  LOOP
	joined_members:=joined_members+1;
    IF member_record.project_id<>NEW.project_id OR member_record.project_revision<>NEW.project_revision OR member_record.state<>'confirmed' OR
       member_record.current_revision<>member_record.association_revision OR member_record.current_state<>'confirmed' OR
       member_record.association_receipt_id<>member_record.source_authority_receipt_id OR
       member_record.subject_contract_type<>member_record.receipt_type OR member_record.subject_id<>member_record.receipt_subject_id OR
       member_record.subject_revision<>member_record.receipt_subject_revision OR member_record.subject_digest<>member_record.receipt_subject_digest OR
	   member_record.receipt_expires_at<=clock_timestamp() OR
       member_record.actor_person_id<>NEW.actor_person_id OR member_record.actor_membership_id<>NEW.actor_membership_id OR
       member_record.actor_membership_revision<>NEW.actor_membership_revision OR member_record.session_subject_digest<>NEW.session_subject_digest OR
       member_record.session_revision<>NEW.session_revision OR member_record.authority_generation<>NEW.authority_generation OR
       (member_record.ordinal=0 AND member_record.subject_contract_type<>'conversation_event') OR
       (member_record.ordinal=0 AND (member_record.root_thread_id IS DISTINCT FROM NEW.thread_id OR member_record.root_source_id IS DISTINCT FROM NEW.message_id OR member_record.root_author IS DISTINCT FROM NEW.actor_person_id)) OR
       (member_record.ordinal>0 AND (member_record.subject_contract_type<>'rich_message_part' OR member_record.part_ordinal<>member_record.ordinal-1 OR
          member_record.part_event_id<>NEW.conversation_event_id OR member_record.part_event_revision<>NEW.conversation_event_revision)) THEN
      RAISE EXCEPTION 'Project chat source group member does not bind exact receipt and association truth';
    END IF;
    IF member_record.ordinal>0 AND member_record.part_author<>NEW.actor_person_id THEN
      RAISE EXCEPTION 'Project chat source group part author must equal group actor';
    END IF;
  END LOOP;
  IF joined_members<>actual_members THEN
    RAISE EXCEPTION 'Project chat source group contains an unattested member';
  END IF;
  SELECT reply_to_event_id INTO root_reply_id FROM stride_conversation_events
   WHERE tenant_id=NEW.organization_id AND event_id=NEW.conversation_event_id;
  IF (root_reply_id IS NULL)<>NOT EXISTS(SELECT 1 FROM stride_project_chat_reply_dependencies dependency
       WHERE dependency.organization_id=NEW.organization_id AND dependency.child_event_id=NEW.conversation_event_id) OR
     EXISTS(SELECT 1 FROM stride_project_chat_reply_dependencies dependency
       WHERE dependency.organization_id=NEW.organization_id AND dependency.child_event_id=NEW.conversation_event_id
         AND (dependency.parent_event_id<>root_reply_id OR dependency.source_manifest_digest<>NEW.source_manifest_digest)) THEN
    RAISE EXCEPTION 'Project chat source group requires exact reply dependency iff root event replies';
  END IF;
	IF EXISTS(SELECT 1 FROM stride_project_chat_reply_dependencies dependency
	  JOIN stride_conversation_events parent ON parent.tenant_id=dependency.organization_id AND parent.event_id=dependency.parent_event_id
	  JOIN stride_project_source_authority_receipts parent_receipt ON parent_receipt.organization_id=dependency.organization_id
	    AND parent_receipt.source_authority_receipt_id=dependency.parent_source_authority_receipt_id
	  WHERE dependency.organization_id=NEW.organization_id AND dependency.child_event_id=NEW.conversation_event_id AND
	   (parent_receipt.expires_at<=clock_timestamp() OR parent.invalidated_at IS NOT NULL OR parent.thread_id IS DISTINCT FROM NEW.thread_id OR
	    parent.content_revision<>dependency.parent_event_revision OR parent.content_digest<>dependency.parent_event_digest OR
	    parent.author_principal<>dependency.parent_author_principal OR parent.audience_digest<>dependency.parent_audience_digest OR
	    parent.acl_version<>dependency.parent_acl_revision OR parent.purge_generation<>dependency.parent_purge_generation OR
	    parent_receipt.subject_id<>parent.event_id OR parent_receipt.subject_revision<>parent.content_revision OR
	    parent_receipt.subject_digest<>parent.content_digest OR parent_receipt.source_audience->'principals' @> jsonb_build_array(NEW.actor_person_id)=false OR
	    parent_receipt.source_acl_revision<>parent.acl_version OR parent_receipt.purge_generation<>parent.purge_generation)) THEN
	  RAISE EXCEPTION 'Project chat source group requires current exact reply source authority at commit';
	END IF;
  IF stride_project_chat_source_group_manifest_digest(NEW.organization_id,NEW.group_id)<>NEW.source_manifest_digest THEN
    RAISE EXCEPTION 'Project chat source group manifest digest is not reproducible from canonical truth';
  END IF;
  SELECT count(*) INTO active_groups FROM stride_project_chat_source_groups candidate
   WHERE candidate.organization_id=NEW.organization_id AND candidate.conversation_event_id=NEW.conversation_event_id
     AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_invalidations invalidation
       WHERE invalidation.organization_id=candidate.organization_id AND invalidation.group_id=candidate.group_id)
     AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift
       WHERE drift.organization_id=candidate.organization_id AND drift.group_id=candidate.group_id)
     AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_authority_loss_receipts authority_loss
       WHERE authority_loss.organization_id=candidate.organization_id AND authority_loss.group_id=candidate.group_id);
  IF active_groups<>1 THEN
    RAISE EXCEPTION 'Project chat source group requires exactly one active group per event';
  END IF;
  RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER stride_project_chat_source_group_truth_guard
AFTER INSERT ON stride_project_chat_source_groups
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_requires_exact_truth();

CREATE FUNCTION stride_project_chat_source_group_invalidation_exact()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_group stride_project_chat_source_groups%ROWTYPE;
DECLARE member_count integer;
DECLARE purge_count integer;
DECLARE expected_terminal_operation text;
DECLARE membership stride_organization_memberships_current%ROWTYPE;
DECLARE session_binding stride_active_organization_sessions%ROWTYPE;
BEGIN
  SELECT * INTO source_group FROM stride_project_chat_source_groups WHERE organization_id=NEW.organization_id AND group_id=NEW.group_id FOR SHARE;
  SELECT count(*) INTO member_count FROM stride_project_chat_source_group_members WHERE organization_id=NEW.organization_id AND group_id=NEW.group_id;
  SELECT CASE WHEN EXISTS(SELECT 1 FROM stride_project_chat_source_group_correction_receipts correction
    WHERE correction.organization_id=NEW.organization_id AND correction.old_group_id=NEW.group_id
      AND correction.operation_id=NEW.operation_id AND correction.result_state='corrected')
    THEN 'unlist_old' ELSE 'purge' END INTO expected_terminal_operation;
  SELECT count(*) INTO purge_count FROM stride_project_chat_source_group_members member
   JOIN stride_project_associations_current current_association
     ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
   JOIN stride_project_projection_outbox outbox
     ON outbox.organization_id=member.organization_id AND outbox.association_id=member.association_id
    AND outbox.association_revision=current_association.revision AND outbox.operation=expected_terminal_operation
   WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id;
  SELECT * INTO membership FROM stride_organization_memberships_current WHERE membership_id=NEW.actor_membership_id FOR SHARE;
  SELECT * INTO session_binding FROM stride_active_organization_sessions WHERE session_subject_digest=NEW.session_subject_digest FOR SHARE;
  IF source_group.group_id IS NULL OR source_group.actor_person_id<>NEW.actor_person_id OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift WHERE drift.organization_id=NEW.organization_id AND drift.group_id=NEW.group_id) OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_correction_receipts correction
       WHERE correction.organization_id=NEW.organization_id AND correction.old_group_id=NEW.group_id
         AND correction.operation_id<>NEW.operation_id) OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_authority_loss_receipts authority_loss
       WHERE authority_loss.organization_id=NEW.organization_id AND authority_loss.group_id=NEW.group_id) OR
     membership.membership_id IS NULL OR membership.organization_id<>NEW.organization_id OR membership.person_id<>NEW.actor_person_id OR
     membership.revision<>NEW.actor_membership_revision OR membership.status<>'active' OR
     session_binding.session_subject_digest IS NULL OR session_binding.organization_id<>NEW.organization_id OR
     session_binding.person_id<>NEW.actor_person_id OR session_binding.membership_id<>NEW.actor_membership_id OR
     session_binding.membership_revision<>NEW.actor_membership_revision OR session_binding.session_revision<>NEW.session_revision OR
     session_binding.authority_generation<>NEW.authority_generation OR session_binding.status<>'active' OR session_binding.expires_at<=clock_timestamp() OR
     NEW.recorded_at<session_binding.bound_at OR NEW.recorded_at>clock_timestamp()+interval '1 minute' OR
     NEW.request_fingerprint<>sha256(convert_to(concat_ws(E'\x1f',NEW.organization_id,NEW.group_id,NEW.operation_id,encode(NEW.operation_key_digest,'hex'),NEW.reason,NEW.actor_person_id,
       NEW.actor_membership_id,NEW.actor_membership_revision::text,encode(NEW.session_subject_digest,'hex'),NEW.session_revision::text,NEW.authority_generation::text,NEW.recorded_at::text),'UTF8')) OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_members member
       JOIN stride_project_associations_current current_association
         ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
       WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id AND current_association.state NOT IN ('revoked','corrected','removed')) OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_members member
       JOIN stride_project_associations_authorized_current authorized
         ON authorized.organization_id=member.organization_id AND authorized.association_id=member.association_id
       WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id) OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_members member
       JOIN stride_project_associations_current current_association
         ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
       JOIN stride_project_projection_outbox outbox ON outbox.organization_id=member.organization_id
         AND outbox.association_id=member.association_id AND outbox.association_revision=current_association.revision
       WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id
         AND outbox.operation IN ('purge','unlist_old') AND outbox.operation<>expected_terminal_operation) OR
     purge_count<>member_count*4 THEN
    RAISE EXCEPTION 'Project chat source group invalidation requires exact terminal associations and four-family terminal outbox';
  END IF;
  RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER stride_project_chat_source_group_invalidation_exact_guard
AFTER INSERT ON stride_project_chat_source_group_invalidations
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_invalidation_exact();

CREATE FUNCTION stride_project_chat_source_group_drift_exact()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_group stride_project_chat_source_groups%ROWTYPE;
DECLARE drift_proven boolean:=false;
DECLARE member_count integer;
DECLARE purge_count integer;
BEGIN
  SELECT * INTO source_group FROM stride_project_chat_source_groups WHERE organization_id=NEW.organization_id AND group_id=NEW.group_id FOR SHARE;
  IF NEW.drift_contract_type='conversation_event' THEN
    SELECT EXISTS(SELECT 1 FROM stride_project_chat_source_group_members member
      LEFT JOIN stride_conversation_events event ON event.tenant_id=member.organization_id AND event.event_id=member.subject_id
      WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id AND member.subject_contract_type='conversation_event'
        AND member.subject_id=NEW.drift_subject_id AND member.subject_revision=NEW.expected_revision AND member.subject_digest=NEW.expected_digest
        AND (event.event_id IS NULL OR event.invalidated_at IS NOT NULL OR event.content_revision<>member.subject_revision OR event.content_digest<>member.subject_digest OR
          (member.ordinal=0 AND (event.thread_id<>source_group.thread_id OR event.source_id<>source_group.message_id OR event.author_principal<>source_group.actor_person_id)) OR
          event.audience_digest<>sha256(convert_to((SELECT receipt.source_audience FROM stride_project_source_authority_receipts receipt
            WHERE receipt.organization_id=member.organization_id AND receipt.source_authority_receipt_id=member.source_authority_receipt_id)::text,'UTF8')) OR
          event.visibility<>(SELECT receipt.source_audience->>'visibility' FROM stride_project_source_authority_receipts receipt
            WHERE receipt.organization_id=member.organization_id AND receipt.source_authority_receipt_id=member.source_authority_receipt_id) OR
          event.acl_version<>(SELECT receipt.source_acl_revision FROM stride_project_source_authority_receipts receipt
            WHERE receipt.organization_id=member.organization_id AND receipt.source_authority_receipt_id=member.source_authority_receipt_id) OR
          event.purge_generation<>(SELECT receipt.purge_generation FROM stride_project_source_authority_receipts receipt
            WHERE receipt.organization_id=member.organization_id AND receipt.source_authority_receipt_id=member.source_authority_receipt_id))) INTO drift_proven;
  ELSIF NEW.drift_contract_type='rich_message_part' THEN
    SELECT EXISTS(SELECT 1 FROM stride_project_chat_source_group_members member
      LEFT JOIN stride_rich_message_part_revisions part ON part.organization_id=member.organization_id AND part.part_id=member.subject_id AND part.revision=member.subject_revision
      LEFT JOIN stride_rich_message_parts_current current_part ON current_part.organization_id=member.organization_id AND current_part.part_id=member.subject_id
      WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id AND member.subject_contract_type='rich_message_part'
        AND member.subject_id=NEW.drift_subject_id AND member.subject_revision=NEW.expected_revision AND member.subject_digest=NEW.expected_digest
        AND (part.part_id IS NULL OR part.invalidated_at IS NOT NULL OR current_part.revision<>member.subject_revision OR current_part.content_digest<>member.subject_digest)) INTO drift_proven;
  ELSE
    SELECT EXISTS(SELECT 1 FROM stride_project_chat_reply_dependencies dependency
      LEFT JOIN stride_conversation_events parent ON parent.tenant_id=dependency.organization_id AND parent.event_id=dependency.parent_event_id
      WHERE dependency.organization_id=NEW.organization_id AND dependency.child_event_id=source_group.conversation_event_id
        AND dependency.parent_event_id=NEW.drift_subject_id AND dependency.parent_event_revision=NEW.expected_revision AND dependency.parent_event_digest=NEW.expected_digest
        AND (parent.event_id IS NULL OR parent.invalidated_at IS NOT NULL OR parent.content_revision<>dependency.parent_event_revision OR
          parent.content_digest<>dependency.parent_event_digest OR parent.author_principal<>dependency.parent_author_principal OR parent.audience_digest<>dependency.parent_audience_digest OR
          parent.acl_version<>dependency.parent_acl_revision OR parent.purge_generation<>dependency.parent_purge_generation)) INTO drift_proven;
  END IF;
  SELECT count(*) INTO member_count FROM stride_project_chat_source_group_members WHERE organization_id=NEW.organization_id AND group_id=NEW.group_id;
  SELECT count(*) INTO purge_count FROM stride_project_chat_source_group_members member
   JOIN stride_project_associations_current current_association ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
   JOIN stride_project_projection_outbox outbox ON outbox.organization_id=member.organization_id AND outbox.association_id=member.association_id
    AND outbox.association_revision=current_association.revision AND outbox.operation='purge'
   WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id;
  IF source_group.group_id IS NULL OR NOT drift_proven OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_invalidations invalidation WHERE invalidation.organization_id=NEW.organization_id AND invalidation.group_id=NEW.group_id) OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_authority_loss_receipts authority_loss WHERE authority_loss.organization_id=NEW.organization_id AND authority_loss.group_id=NEW.group_id) OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_correction_receipts correction WHERE correction.organization_id=NEW.organization_id AND correction.old_group_id=NEW.group_id) OR
     NEW.request_fingerprint<>sha256(convert_to(concat_ws(E'\x1f',NEW.organization_id,NEW.group_id,NEW.operation_id,encode(NEW.operation_key_digest,'hex'),NEW.drift_contract_type,
       NEW.drift_subject_id,NEW.expected_revision::text,encode(NEW.expected_digest,'hex'),NEW.reason,NEW.recorded_at::text),'UTF8')) OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_members member JOIN stride_project_associations_current current_association
       ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
       WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id AND current_association.state<>'revoked') OR
     purge_count<>member_count*4 THEN
    RAISE EXCEPTION 'Project chat source group drift receipt requires exact drift, revoked group and four-family purge';
  END IF;
  RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER stride_project_chat_source_group_drift_exact_guard
AFTER INSERT ON stride_project_chat_source_group_drift_receipts
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_drift_exact();

CREATE FUNCTION stride_project_chat_source_group_authority_loss_exact()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_group stride_project_chat_source_groups%ROWTYPE;
DECLARE project_current stride_projects_current%ROWTYPE;
DECLARE members integer;
DECLARE purges integer;
BEGIN
  SELECT * INTO source_group FROM stride_project_chat_source_groups WHERE organization_id=NEW.organization_id AND group_id=NEW.group_id FOR SHARE;
  SELECT * INTO project_current FROM stride_projects_current WHERE organization_id=NEW.organization_id AND project_id=NEW.project_id FOR SHARE;
  SELECT count(*) INTO members FROM stride_project_chat_source_group_members WHERE organization_id=NEW.organization_id AND group_id=NEW.group_id;
  SELECT count(*) INTO purges FROM stride_project_chat_source_group_members member
    JOIN stride_project_associations_current current_association ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
    JOIN stride_project_projection_outbox outbox ON outbox.organization_id=member.organization_id AND outbox.association_id=member.association_id
      AND outbox.association_revision=current_association.revision AND outbox.operation='purge'
   WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id;
  IF source_group.group_id IS NULL OR project_current.project_id IS NULL OR source_group.project_id IS DISTINCT FROM NEW.project_id OR source_group.project_revision IS DISTINCT FROM NEW.expected_project_revision OR
     NOT EXISTS(SELECT 1 FROM stride_project_revisions revision WHERE revision.organization_id=NEW.organization_id AND revision.project_id=NEW.project_id
       AND revision.revision=NEW.expected_project_revision AND revision.content_digest=NEW.expected_project_digest) OR
     NOT ((NEW.reason='project_archived' AND project_current.lifecycle='archived') OR
          (NEW.reason='project_audience_revoked' AND NOT EXISTS(SELECT 1 FROM stride_project_revisions revision
            JOIN stride_organization_memberships_current membership ON membership.organization_id=revision.organization_id
              AND membership.person_id=source_group.actor_person_id AND membership.status='active'
            WHERE revision.organization_id=project_current.organization_id AND revision.project_id=project_current.project_id AND revision.revision=project_current.revision
              AND (revision.audience->'principals' @> jsonb_build_array(source_group.actor_person_id)
                OR revision.controller_memberships @> jsonb_build_array(jsonb_build_object('contractType','organization_membership',
                  'id',membership.membership_id,'revision',membership.revision)))))) OR
     NEW.request_fingerprint<>sha256(convert_to(concat_ws(E'\x1f',NEW.organization_id,NEW.group_id,NEW.operation_id,encode(NEW.operation_key_digest,'hex'),
       NEW.project_id,NEW.expected_project_revision::text,encode(NEW.expected_project_digest,'hex'),NEW.reason,NEW.recorded_at::text),'UTF8')) OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_members member JOIN stride_project_associations_current current_association
       ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
       WHERE member.organization_id=NEW.organization_id AND member.group_id=NEW.group_id AND current_association.state<>'revoked') OR purges<>members*4 THEN
    RAISE EXCEPTION 'Project chat source group authority loss requires exact Project drift, revoked group and four-family purge';
  END IF;
  RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER stride_project_chat_source_group_authority_loss_exact_guard
AFTER INSERT ON stride_project_chat_source_group_authority_loss_receipts
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_authority_loss_exact();

CREATE FUNCTION stride_project_chat_group_association_atomic_transition()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owned_group text;
DECLARE total_members integer;
DECLARE terminal_members integer;
BEGIN
  SELECT member.group_id INTO owned_group FROM stride_project_chat_source_group_members member
   WHERE member.organization_id=NEW.organization_id AND member.association_id=NEW.association_id;
  IF owned_group IS NULL THEN RETURN NEW; END IF;
  SELECT count(*),count(*) FILTER(WHERE current_association.state IN ('revoked','corrected','removed'))
    INTO total_members,terminal_members
    FROM stride_project_chat_source_group_members member
    JOIN stride_project_associations_current current_association
      ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
   WHERE member.organization_id=NEW.organization_id AND member.group_id=owned_group;
  IF NEW.state IN ('revoked','corrected','removed') AND (terminal_members<>total_members OR
     (NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_invalidations invalidation
       WHERE invalidation.organization_id=NEW.organization_id AND invalidation.group_id=owned_group) AND
      NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift
       WHERE drift.organization_id=NEW.organization_id AND drift.group_id=owned_group) AND
      NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_authority_loss_receipts authority_loss
       WHERE authority_loss.organization_id=NEW.organization_id AND authority_loss.group_id=owned_group))) THEN
    RAISE EXCEPTION 'Project chat source group associations transition atomically';
  END IF;
  RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER stride_project_chat_group_association_atomic_guard
AFTER INSERT OR UPDATE ON stride_project_associations_current
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stride_project_chat_group_association_atomic_transition();
CREATE CONSTRAINT TRIGGER stride_project_chat_group_revision_atomic_guard
AFTER INSERT ON stride_project_association_revisions
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stride_project_chat_group_association_atomic_transition();

CREATE FUNCTION stride_project_chat_source_group_correction_exact()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_group stride_project_chat_source_groups%ROWTYPE;
DECLARE replacement_group stride_project_chat_source_groups%ROWTYPE;
DECLARE membership stride_organization_memberships_current%ROWTYPE;
DECLARE session_binding stride_active_organization_sessions%ROWTYPE;
DECLARE old_member_record record;
DECLARE verified_members integer:=0;
BEGIN
  SELECT * INTO old_group FROM stride_project_chat_source_groups WHERE organization_id=NEW.organization_id AND group_id=NEW.old_group_id FOR SHARE;
  SELECT * INTO membership FROM stride_organization_memberships_current WHERE membership_id=NEW.actor_membership_id FOR SHARE;
  SELECT * INTO session_binding FROM stride_active_organization_sessions WHERE session_subject_digest=NEW.session_subject_digest FOR SHARE;
  IF old_group.group_id IS NULL OR old_group.actor_person_id<>NEW.actor_person_id OR
     EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift WHERE drift.organization_id=NEW.organization_id AND drift.group_id=NEW.old_group_id) OR
     membership.membership_id IS NULL OR membership.organization_id<>NEW.organization_id OR membership.person_id<>NEW.actor_person_id OR
     membership.revision<>NEW.actor_membership_revision OR membership.status<>'active' OR
     session_binding.session_subject_digest IS NULL OR session_binding.organization_id<>NEW.organization_id OR
     session_binding.person_id<>NEW.actor_person_id OR session_binding.membership_id<>NEW.actor_membership_id OR
     session_binding.membership_revision<>NEW.actor_membership_revision OR session_binding.session_revision<>NEW.session_revision OR
     session_binding.authority_generation<>NEW.authority_generation OR session_binding.status<>'active' OR session_binding.expires_at<=clock_timestamp() OR
     NEW.recorded_at<session_binding.bound_at OR NEW.recorded_at>clock_timestamp()+interval '1 minute' OR
     NEW.request_fingerprint<>sha256(convert_to(concat_ws(E'\x1f',NEW.organization_id,NEW.operation_id,encode(NEW.operation_key_digest,'hex'),NEW.old_group_id,
       COALESCE(NEW.replacement_group_id,''),NEW.result_state,NEW.context_revision::text,NEW.actor_person_id,NEW.actor_membership_id,
       NEW.actor_membership_revision::text,encode(NEW.session_subject_digest,'hex'),NEW.session_revision::text,NEW.authority_generation::text,NEW.recorded_at::text),'UTF8')) OR
     NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_invalidations invalidation
       WHERE invalidation.organization_id=NEW.organization_id AND invalidation.group_id=NEW.old_group_id AND invalidation.operation_id=NEW.operation_id) THEN
    RAISE EXCEPTION 'Project chat source group correction requires exact invalidated old group';
  END IF;
  IF NEW.result_state='corrected' THEN
    SELECT * INTO replacement_group FROM stride_project_chat_source_groups WHERE organization_id=NEW.organization_id AND group_id=NEW.replacement_group_id FOR SHARE;
    IF replacement_group.group_id IS NULL OR replacement_group.conversation_event_id<>old_group.conversation_event_id OR
       replacement_group.source_manifest_digest<>old_group.source_manifest_digest OR replacement_group.member_count<>old_group.member_count OR
       replacement_group.operation_id<>NEW.operation_id OR
       replacement_group.operation_key_digest<>sha256(convert_to(concat_ws(E'\x1f','project-chat-group-correction-replacement/v1',
         encode(NEW.operation_key_digest,'hex'),NEW.replacement_group_id),'UTF8')) OR
       EXISTS(SELECT 1 FROM stride_project_chat_source_group_invalidations replacement_invalidation
         WHERE replacement_invalidation.organization_id=NEW.organization_id AND replacement_invalidation.group_id=NEW.replacement_group_id) OR
       EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts replacement_drift
         WHERE replacement_drift.organization_id=NEW.organization_id AND replacement_drift.group_id=NEW.replacement_group_id) OR
       EXISTS(SELECT 1 FROM stride_project_chat_source_group_members old_member
         FULL JOIN stride_project_chat_source_group_members replacement_member
           ON replacement_member.organization_id=old_member.organization_id AND replacement_member.group_id=NEW.replacement_group_id
          AND replacement_member.ordinal=old_member.ordinal AND replacement_member.subject_contract_type=old_member.subject_contract_type
          AND replacement_member.subject_id=old_member.subject_id AND replacement_member.subject_revision=old_member.subject_revision
          AND replacement_member.subject_digest=old_member.subject_digest
         WHERE old_member.organization_id=NEW.organization_id AND old_member.group_id=NEW.old_group_id
           AND (replacement_member.group_id IS NULL OR replacement_member.association_id=old_member.association_id)) THEN
      RAISE EXCEPTION 'Project chat source group correction requires exact replacement group CAS';
    END IF;
  END IF;
	FOR old_member_record IN
	  SELECT old_member.ordinal,old_member.association_id,old_member.association_revision,
	         prior.content_digest AS prior_digest,current_association.revision AS result_revision,current_association.state AS current_state,
	         terminal.state AS terminal_state,terminal.supersedes_revision,terminal.supersedes_digest,
	         terminal.replacement_association_id,terminal.replacement_association_revision,terminal.replacement_association_digest,
	         replacement_member.association_id AS expected_replacement_id,replacement_member.association_revision AS expected_replacement_revision,
	         replacement_revision.content_digest AS expected_replacement_digest,
	         event.resulting_state AS event_state,event.prior_revision AS event_prior,event.new_revision AS event_new,
	         event.idempotency_key_digest AS event_key,
	         (SELECT count(*) FROM stride_project_projection_outbox outbox WHERE outbox.organization_id=old_member.organization_id
	            AND outbox.association_id=old_member.association_id AND outbox.association_revision=current_association.revision
	            AND outbox.operation=CASE WHEN NEW.result_state='corrected' THEN 'unlist_old' ELSE 'purge' END) AS terminal_outbox_count,
	         (SELECT count(*) FROM stride_project_projection_outbox replacement_outbox
	            WHERE replacement_outbox.organization_id=replacement_member.organization_id
	              AND replacement_outbox.association_id=replacement_member.association_id
	              AND replacement_outbox.association_revision=replacement_member.association_revision
	              AND replacement_outbox.operation='list_new') AS replacement_outbox_count
	    FROM stride_project_chat_source_group_members old_member
	    JOIN stride_project_association_revisions prior ON prior.organization_id=old_member.organization_id
	      AND prior.association_id=old_member.association_id AND prior.revision=old_member.association_revision
	    JOIN stride_project_associations_current current_association ON current_association.organization_id=old_member.organization_id
	      AND current_association.association_id=old_member.association_id
	    JOIN stride_project_association_revisions terminal ON terminal.organization_id=old_member.organization_id
	      AND terminal.association_id=old_member.association_id AND terminal.revision=current_association.revision
	    JOIN stride_project_association_events event ON event.organization_id=old_member.organization_id
	      AND event.association_id=old_member.association_id AND event.association_revision=current_association.revision
	    LEFT JOIN stride_project_chat_source_group_members replacement_member ON NEW.result_state='corrected'
	      AND replacement_member.organization_id=old_member.organization_id AND replacement_member.group_id=NEW.replacement_group_id
	      AND replacement_member.ordinal=old_member.ordinal
	    LEFT JOIN stride_project_association_revisions replacement_revision ON replacement_revision.organization_id=replacement_member.organization_id
	      AND replacement_revision.association_id=replacement_member.association_id AND replacement_revision.revision=replacement_member.association_revision
	   WHERE old_member.organization_id=NEW.organization_id AND old_member.group_id=NEW.old_group_id
	LOOP
	  verified_members:=verified_members+1;
	  IF old_member_record.result_revision<>old_member_record.association_revision+1 OR old_member_record.current_state<>NEW.result_state OR
	     old_member_record.terminal_state<>NEW.result_state OR old_member_record.supersedes_revision<>old_member_record.association_revision OR
	     old_member_record.supersedes_digest<>old_member_record.prior_digest OR old_member_record.event_state<>NEW.result_state OR
	     old_member_record.event_prior<>old_member_record.association_revision OR old_member_record.event_new<>old_member_record.result_revision OR
	     old_member_record.event_key<>sha256(convert_to(concat_ws(E'\x1f','project-chat-group-correction-edge/v1',
	       encode(NEW.operation_key_digest,'hex'),old_member_record.ordinal::text,old_member_record.association_id),'UTF8')) OR
	     old_member_record.terminal_outbox_count<>4 OR
	     (NEW.result_state='corrected' AND old_member_record.replacement_outbox_count<>4) OR
	     (NEW.result_state='removed' AND (old_member_record.replacement_association_id IS NOT NULL OR old_member_record.expected_replacement_id IS NOT NULL)) OR
	     (NEW.result_state='corrected' AND (old_member_record.replacement_association_id<>old_member_record.expected_replacement_id OR
	       old_member_record.replacement_association_revision<>old_member_record.expected_replacement_revision OR
	       old_member_record.replacement_association_digest<>old_member_record.expected_replacement_digest OR
	       old_member_record.expected_replacement_id IS NULL)) THEN
	    RAISE EXCEPTION 'Project chat source group correction requires exact per-member terminal lineage';
	  END IF;
	END LOOP;
	IF verified_members<>old_group.member_count THEN
	  RAISE EXCEPTION 'Project chat source group correction omitted a member';
	END IF;
  RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER stride_project_chat_source_group_correction_exact_guard
AFTER INSERT ON stride_project_chat_source_group_correction_receipts
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_group_correction_exact();

COMMIT;
