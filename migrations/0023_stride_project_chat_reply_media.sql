BEGIN;

-- Project-linked reply media is supporting evidence only. It never receives a
-- ProjectAssociation and never changes the replied-to event's Project truth.
-- RichMessagePart author identity was person-only in migration 22. Supporting
-- media may truthfully be authored by the existing server-owned Scout
-- principal; this is provenance only and does not grant person rights.
DO $$
DECLARE constraint_record record;
BEGIN
  FOR constraint_record IN
    SELECT constraint_row.conname
      FROM pg_constraint constraint_row
     WHERE constraint_row.conrelid='stride_rich_message_part_revisions'::regclass
       AND constraint_row.contype='f'
       AND constraint_row.confrelid='stride_person_principals'::regclass
       AND constraint_row.conkey=ARRAY[(SELECT attnum FROM pg_attribute
         WHERE attrelid='stride_rich_message_part_revisions'::regclass AND attname='author_principal')]
  LOOP
    EXECUTE format('ALTER TABLE stride_rich_message_part_revisions DROP CONSTRAINT %I',constraint_record.conname);
  END LOOP;
END; $$;

CREATE FUNCTION stride_rich_message_part_author_principal_exact()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.author_principal<>'service:scout' AND NOT EXISTS(SELECT 1 FROM stride_person_principals person
                 WHERE person.person_id=NEW.author_principal AND person.status='active') THEN
    RAISE EXCEPTION 'Rich message part requires an exact active author principal';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_rich_message_part_author_principal_guard
BEFORE INSERT ON stride_rich_message_part_revisions
FOR EACH ROW EXECUTE FUNCTION stride_rich_message_part_author_principal_exact();

DO $$
DECLARE constraint_record record;
BEGIN
  FOR constraint_record IN
    SELECT constraint_row.conname
      FROM pg_constraint constraint_row
     WHERE constraint_row.conrelid='stride_project_chat_reply_dependencies'::regclass
       AND constraint_row.contype='f'
       AND constraint_row.confrelid='stride_person_principals'::regclass
       AND constraint_row.conkey=ARRAY[(SELECT attnum FROM pg_attribute
         WHERE attrelid='stride_project_chat_reply_dependencies'::regclass AND attname='parent_author_principal')]
  LOOP
    EXECUTE format('ALTER TABLE stride_project_chat_reply_dependencies DROP CONSTRAINT %I',constraint_record.conname);
  END LOOP;
END; $$;

CREATE FUNCTION stride_project_chat_reply_author_principal_exact()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.parent_author_principal<>'service:scout' AND NOT EXISTS(SELECT 1 FROM stride_person_principals person
                 WHERE person.person_id=NEW.parent_author_principal AND person.status='active') THEN
    RAISE EXCEPTION 'Project chat reply requires an exact active parent principal';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_chat_reply_author_principal_guard
BEFORE INSERT ON stride_project_chat_reply_dependencies
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_reply_author_principal_exact();

-- Existing rows are v2. Only v3 groups may carry canonical reply media.
ALTER TABLE stride_project_chat_source_groups
  ADD COLUMN source_manifest_version integer NOT NULL DEFAULT 2
    CHECK(source_manifest_version IN (2,3));

CREATE TABLE stride_project_chat_reply_media_authority_receipts (
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    receipt_id text NOT NULL,
    child_event_id text NOT NULL,
    parent_event_id text NOT NULL,
    part_id text NOT NULL,
    part_revision bigint NOT NULL CHECK(part_revision>0),
    part_digest bytea NOT NULL CHECK(octet_length(part_digest)=32),
    source_audience jsonb NOT NULL,
    source_acl_revision bigint NOT NULL CHECK(source_acl_revision>0),
    purge_generation bigint NOT NULL CHECK(purge_generation>0),
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
    session_subject_digest bytea NOT NULL,
    session_revision bigint NOT NULL CHECK(session_revision>0),
    authority_generation bigint NOT NULL CHECK(authority_generation>0),
    operation_key_digest bytea NOT NULL CHECK(octet_length(operation_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
    recorded_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK(expires_at>recorded_at),
    PRIMARY KEY(organization_id,receipt_id),
    -- Each source-group creation or correction mints a fresh, immutable
    -- authority receipt for the same support part. The dependency retains the
    -- original receipt as admission evidence; later groups prove fresh
    -- authority with their own receipt recorded at the group boundary.
    FOREIGN KEY(organization_id,child_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    FOREIGN KEY(organization_id,parent_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    FOREIGN KEY(part_id,part_revision,organization_id)
      REFERENCES stride_rich_message_part_revisions(part_id,revision,organization_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision)
      REFERENCES stride_organization_membership_revisions(membership_id,revision),
    FOREIGN KEY(session_subject_digest)
      REFERENCES stride_active_organization_sessions(session_subject_digest),
    CHECK(stride_project_source_audience_valid(source_audience))
);

CREATE TABLE stride_project_chat_reply_media_dependencies (
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    child_event_id text NOT NULL,
    parent_event_id text NOT NULL,
    ordinal integer NOT NULL CHECK(ordinal>=0),
    media_kind text NOT NULL CHECK(media_kind IN ('file','generated_image')),
    part_id text NOT NULL,
    part_revision bigint NOT NULL CHECK(part_revision>0),
    part_digest bytea NOT NULL CHECK(octet_length(part_digest)=32),
    authority_receipt_id text NOT NULL,
    source_manifest_digest bytea NOT NULL CHECK(octet_length(source_manifest_digest)=32),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,child_event_id,ordinal),
    UNIQUE(organization_id,child_event_id,part_id,part_revision),
    FOREIGN KEY(organization_id,child_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    FOREIGN KEY(organization_id,parent_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    FOREIGN KEY(part_id,part_revision,organization_id)
      REFERENCES stride_rich_message_part_revisions(part_id,revision,organization_id),
    FOREIGN KEY(organization_id,authority_receipt_id)
      REFERENCES stride_project_chat_reply_media_authority_receipts(organization_id,receipt_id)
);
CREATE INDEX stride_project_chat_reply_media_parent
  ON stride_project_chat_reply_media_dependencies(organization_id,parent_event_id,part_id,part_revision);

CREATE FUNCTION stride_project_chat_reply_media_exact()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE support_part stride_rich_message_part_revisions%ROWTYPE;
DECLARE support_current stride_rich_message_parts_current%ROWTYPE;
DECLARE support_receipt stride_project_chat_reply_media_authority_receipts%ROWTYPE;
DECLARE child_event stride_conversation_events%ROWTYPE;
DECLARE parent_event stride_conversation_events%ROWTYPE;
DECLARE membership stride_organization_memberships_current%ROWTYPE;
DECLARE session_binding stride_active_organization_sessions%ROWTYPE;
BEGIN
  SELECT * INTO support_part FROM stride_rich_message_part_revisions
   WHERE organization_id=NEW.organization_id AND part_id=NEW.part_id AND revision=NEW.part_revision FOR SHARE;
  SELECT * INTO support_current FROM stride_rich_message_parts_current
   WHERE organization_id=NEW.organization_id AND part_id=NEW.part_id FOR SHARE;
  SELECT * INTO support_receipt FROM stride_project_chat_reply_media_authority_receipts
   WHERE organization_id=NEW.organization_id AND receipt_id=NEW.authority_receipt_id FOR SHARE;
  SELECT * INTO child_event FROM stride_conversation_events
   WHERE tenant_id=NEW.organization_id AND event_id=NEW.child_event_id FOR SHARE;
  SELECT * INTO parent_event FROM stride_conversation_events
   WHERE tenant_id=NEW.organization_id AND event_id=NEW.parent_event_id FOR SHARE;
  SELECT * INTO membership FROM stride_organization_memberships_current
   WHERE membership_id=support_receipt.actor_membership_id FOR SHARE;
  SELECT * INTO session_binding FROM stride_active_organization_sessions
   WHERE session_subject_digest=support_receipt.session_subject_digest FOR SHARE;
  IF child_event.event_id IS NULL OR parent_event.event_id IS NULL OR child_event.reply_to_event_id<>parent_event.event_id OR
     child_event.thread_id IS DISTINCT FROM parent_event.thread_id OR support_part.part_id IS NULL OR support_part.invalidated_at IS NOT NULL OR
     support_part.conversation_event_id<>parent_event.event_id OR support_part.conversation_event_revision<>parent_event.content_revision OR
     support_part.author_principal<>parent_event.author_principal OR support_part.ordinal<>NEW.ordinal OR support_part.content_digest<>NEW.part_digest OR
     support_current.part_id IS NULL OR support_current.revision<>NEW.part_revision OR support_current.content_digest<>NEW.part_digest OR
     support_receipt.receipt_id IS NULL OR support_receipt.child_event_id<>NEW.child_event_id OR support_receipt.parent_event_id<>NEW.parent_event_id OR
     support_receipt.part_id<>NEW.part_id OR support_receipt.part_revision<>NEW.part_revision OR support_receipt.part_digest<>NEW.part_digest OR
     support_receipt.source_audience<>support_part.source_audience OR support_receipt.source_acl_revision<>support_part.source_acl_revision OR
     support_receipt.purge_generation<>support_part.purge_generation OR support_receipt.expires_at<=clock_timestamp() OR
     NOT (support_receipt.source_audience->'principals' @> jsonb_build_array(support_receipt.actor_person_id)) OR
     membership.membership_id IS NULL OR membership.organization_id<>NEW.organization_id OR membership.person_id<>support_receipt.actor_person_id OR
     membership.revision<>support_receipt.actor_membership_revision OR membership.status<>'active' OR
     session_binding.session_subject_digest IS NULL OR session_binding.organization_id<>NEW.organization_id OR
     session_binding.person_id<>support_receipt.actor_person_id OR session_binding.membership_id<>support_receipt.actor_membership_id OR
     session_binding.membership_revision<>support_receipt.actor_membership_revision OR session_binding.session_revision<>support_receipt.session_revision OR
     session_binding.authority_generation<>support_receipt.authority_generation OR session_binding.status<>'active' OR
     session_binding.expires_at<=clock_timestamp() OR
     support_receipt.request_fingerprint<>sha256(convert_to(concat_ws(E'\x1f','project-chat-reply-media/v1',NEW.organization_id,
       NEW.child_event_id,NEW.parent_event_id,NEW.ordinal::text,NEW.media_kind,NEW.part_id,NEW.part_revision::text,
       encode(NEW.part_digest,'hex'),encode(NEW.source_manifest_digest,'hex'),encode(support_receipt.operation_key_digest,'hex')),'UTF8')) THEN
    RAISE EXCEPTION 'Project chat reply media requires exact current authorized support evidence';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_chat_reply_media_exact_guard
BEFORE INSERT ON stride_project_chat_reply_media_dependencies
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_reply_media_exact();

CREATE FUNCTION stride_project_chat_reply_media_immutable()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  RAISE EXCEPTION 'Project chat reply media evidence is append-only';
END; $$;
CREATE TRIGGER stride_project_chat_reply_media_receipt_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_reply_media_authority_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_reply_media_immutable();
CREATE TRIGGER stride_project_chat_reply_media_dependency_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_reply_media_dependencies
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_reply_media_immutable();

-- Preserve the exact v2 digest grammar for every released group. V3 appends
-- the ordered evidence-only parent-media parts after the reply event tuple.
CREATE OR REPLACE FUNCTION stride_project_chat_source_group_manifest_digest(group_organization text,group_identifier text)
RETURNS bytea LANGUAGE sql STABLE AS $$
  SELECT sha256(convert_to(
    concat_ws(chr(30),
      CASE WHEN source_group.source_manifest_version=3 THEN 'project-chat-source-manifest/v3' ELSE 'project-chat-source-manifest/v2' END,
      'thread',source_group.thread_id,encode(root_event.content_digest,'hex'),
      COALESCE((SELECT string_agg(concat_ws(chr(31),'attachment',member.ordinal::text,part.source_id,part.source_revision,part.blob_ref,
          encode(part.blob_digest,'hex'),part.media_type,part.byte_size::text,part.destination_revision,
          COALESCE(part.source_origin_id,''),COALESCE(part.source_origin_revision,'')),chr(30) ORDER BY member.ordinal)
        FROM stride_project_chat_source_group_members member
        JOIN stride_rich_message_part_revisions part ON part.organization_id=member.organization_id
          AND part.part_id=member.subject_id AND part.revision=member.subject_revision
        WHERE member.organization_id=source_group.organization_id AND member.group_id=source_group.group_id AND member.ordinal>0),''),
      CASE WHEN reply.parent_event_id IS NULL THEN '' ELSE concat_ws(chr(31),'reply',reply.parent_event_id,reply.parent_event_revision::text,
        encode(reply.parent_event_digest,'hex'),reply.parent_author_principal,encode(reply.parent_legacy_snapshot_digest,'hex'),encode(reply.parent_audience_digest,'hex'),
        reply.parent_acl_revision::text,reply.parent_purge_generation::text) END,
      CASE WHEN source_group.source_manifest_version=3 THEN COALESCE((SELECT string_agg(concat_ws(chr(31),'reply_media',support.ordinal::text,
          support.media_kind,part.source_id,part.source_revision,part.blob_ref,encode(part.blob_digest,'hex'),part.media_type,
          part.byte_size::text,part.destination_revision,COALESCE(part.source_origin_id,''),COALESCE(part.source_origin_revision,''),
          encode(part.content_digest,'hex')),chr(30) ORDER BY support.ordinal)
        FROM stride_project_chat_reply_media_dependencies support
        JOIN stride_rich_message_part_revisions part ON part.organization_id=support.organization_id
          AND part.part_id=support.part_id AND part.revision=support.part_revision
        WHERE support.organization_id=source_group.organization_id AND support.child_event_id=source_group.conversation_event_id),'') ELSE NULL END
    ),'UTF8'))
  FROM stride_project_chat_source_groups source_group
  JOIN stride_conversation_events root_event
    ON root_event.tenant_id=source_group.organization_id AND root_event.event_id=source_group.conversation_event_id
  LEFT JOIN stride_project_chat_reply_dependencies reply
    ON reply.organization_id=source_group.organization_id AND reply.child_event_id=source_group.conversation_event_id
  WHERE source_group.organization_id=group_organization AND source_group.group_id=group_identifier;
$$;

CREATE FUNCTION stride_project_chat_reply_media_group_exact()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE media_count integer;
DECLARE minimum_ordinal integer;
DECLARE maximum_ordinal integer;
BEGIN
  SELECT count(*),min(ordinal),max(ordinal) INTO media_count,minimum_ordinal,maximum_ordinal
    FROM stride_project_chat_reply_media_dependencies
   WHERE organization_id=NEW.organization_id AND child_event_id=NEW.conversation_event_id;
  IF NEW.source_manifest_version=2 AND media_count<>0 THEN
    RAISE EXCEPTION 'Project chat v2 source group cannot contain reply media';
  END IF;
  IF NEW.source_manifest_version=3 AND ((media_count>0 AND (minimum_ordinal<>0 OR maximum_ordinal<>media_count-1)) OR
     EXISTS(SELECT 1 FROM stride_project_chat_reply_media_dependencies support
       JOIN stride_rich_message_part_revisions part ON part.organization_id=support.organization_id
         AND part.part_id=support.part_id AND part.revision=support.part_revision
       LEFT JOIN stride_rich_message_parts_current current_part ON current_part.organization_id=support.organization_id
         AND current_part.part_id=support.part_id
       WHERE support.organization_id=NEW.organization_id AND support.child_event_id=NEW.conversation_event_id AND
        (support.parent_event_id IS DISTINCT FROM part.conversation_event_id OR support.source_manifest_digest<>NEW.source_manifest_digest OR
         current_part.part_id IS NULL OR current_part.revision<>support.part_revision OR current_part.content_digest<>support.part_digest OR
         part.invalidated_at IS NOT NULL OR part.content_digest<>support.part_digest OR NOT EXISTS(
           SELECT 1 FROM stride_project_chat_reply_media_authority_receipts receipt
            WHERE receipt.organization_id=support.organization_id AND receipt.child_event_id=support.child_event_id
              AND receipt.parent_event_id=support.parent_event_id AND receipt.part_id=support.part_id
              AND receipt.part_revision=support.part_revision AND receipt.part_digest=support.part_digest
              AND receipt.source_audience=part.source_audience AND receipt.source_acl_revision=part.source_acl_revision
              AND receipt.purge_generation=part.purge_generation AND receipt.actor_person_id=NEW.actor_person_id
              AND receipt.actor_membership_id=NEW.actor_membership_id
              AND receipt.actor_membership_revision=NEW.actor_membership_revision
              AND receipt.session_subject_digest=NEW.session_subject_digest AND receipt.session_revision=NEW.session_revision
              AND receipt.authority_generation=NEW.authority_generation AND receipt.recorded_at=NEW.recorded_at
              AND receipt.expires_at>clock_timestamp()
              AND receipt.request_fingerprint=sha256(convert_to(concat_ws(E'\x1f','project-chat-reply-media/v1',support.organization_id,
                support.child_event_id,support.parent_event_id,support.ordinal::text,support.media_kind,support.part_id,
                support.part_revision::text,encode(support.part_digest,'hex'),encode(support.source_manifest_digest,'hex'),
                encode(receipt.operation_key_digest,'hex')),'UTF8'))
         )))) THEN
    RAISE EXCEPTION 'Project chat v3 source group requires exact complete reply media truth';
  END IF;
  RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER stride_project_chat_reply_media_group_truth_guard
AFTER INSERT ON stride_project_chat_source_groups
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_reply_media_group_exact();

-- Migration 22's drift receipt recognized only association-owning parts.
-- V3 support parts are deliberately association-free, so extend the same
-- immutable group-terminal receipt to prove drift through either ownership
-- path without inventing a Project edge on the parent media.
CREATE OR REPLACE FUNCTION stride_project_chat_source_group_drift_exact()
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
        AND (part.part_id IS NULL OR part.invalidated_at IS NOT NULL OR current_part.revision<>member.subject_revision OR current_part.content_digest<>member.subject_digest)) OR
      EXISTS(SELECT 1 FROM stride_project_chat_reply_media_dependencies support
        LEFT JOIN stride_rich_message_part_revisions part ON part.organization_id=support.organization_id AND part.part_id=support.part_id AND part.revision=support.part_revision
        LEFT JOIN stride_rich_message_parts_current current_part ON current_part.organization_id=support.organization_id AND current_part.part_id=support.part_id
        WHERE support.organization_id=NEW.organization_id AND support.child_event_id=source_group.conversation_event_id
          AND support.part_id=NEW.drift_subject_id AND support.part_revision=NEW.expected_revision AND support.part_digest=NEW.expected_digest
          AND (part.part_id IS NULL OR part.invalidated_at IS NOT NULL OR current_part.revision<>support.part_revision OR current_part.content_digest<>support.part_digest))
      INTO drift_proven;
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

-- Wrap the released authorized-current view so any support-part drift hides
-- every Project edge in the child group immediately, even before the durable
-- reverse-invalidation worker finishes its four-family purge transaction.
ALTER VIEW stride_project_associations_authorized_current
  RENAME TO stride_project_associations_authorized_current_v2;
CREATE VIEW stride_project_associations_authorized_current AS
SELECT base.*
FROM stride_project_associations_authorized_current_v2 base
WHERE NOT EXISTS (
  SELECT 1
  FROM stride_project_chat_source_group_members member
  JOIN stride_project_chat_source_groups source_group ON source_group.organization_id=member.organization_id AND source_group.group_id=member.group_id
  JOIN stride_project_chat_reply_media_dependencies support ON support.organization_id=source_group.organization_id AND support.child_event_id=source_group.conversation_event_id
  JOIN stride_rich_message_part_revisions part ON part.organization_id=support.organization_id AND part.part_id=support.part_id AND part.revision=support.part_revision
  JOIN stride_project_chat_reply_media_authority_receipts receipt ON receipt.organization_id=support.organization_id AND receipt.receipt_id=support.authority_receipt_id
  LEFT JOIN stride_rich_message_parts_current current_part ON current_part.organization_id=support.organization_id AND current_part.part_id=support.part_id
  WHERE member.organization_id=base.organization_id AND member.association_id=base.association_id
    AND (part.invalidated_at IS NOT NULL OR part.content_digest<>support.part_digest OR current_part.part_id IS NULL OR
         current_part.revision<>support.part_revision OR current_part.content_digest<>support.part_digest OR
         part.source_audience<>receipt.source_audience OR part.source_acl_revision<>receipt.source_acl_revision OR
         part.purge_generation<>receipt.purge_generation)
);

COMMIT;
