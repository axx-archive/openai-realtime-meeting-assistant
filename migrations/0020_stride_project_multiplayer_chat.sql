BEGIN;

-- Project associations may originate in an owner-private Scout conversation
-- or in an exact authorized multiplayer channel/project thread. The canonical
-- event and the association must agree on the complete audience; knowing a
-- thread id, Project id, title, or digest never grants source authority.
ALTER TABLE stride_project_source_authority_receipts
  DROP CONSTRAINT stride_project_source_authority_receipts_source_audience_check1;
ALTER TABLE stride_project_source_authority_receipts
  ADD CONSTRAINT stride_project_source_authority_receipts_visibility_check
  CHECK(source_audience->>'visibility' IN ('private','project','channel'));

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
  JOIN stride_conversation_events source_event
    ON source_event.tenant_id=association_revision.organization_id
   AND source_event.event_id=association_revision.subject_id
 WHERE source_event.invalidated_at IS NULL
   AND source_event.content_revision=association_revision.subject_revision
   AND source_event.content_digest=association_revision.subject_digest
   AND source_event.audience_digest=sha256(convert_to(association_revision.source_audience::text,'UTF8'))
   AND source_event.visibility=association_revision.source_audience->>'visibility'
   AND source_event.visibility IN ('private','project','channel')
   AND source_event.acl_version=association_revision.source_acl_revision
   AND source_event.purge_generation=association_revision.purge_generation
   AND association_revision.source_audience->'principals' @> jsonb_build_array(association_revision.actor_person_id);

CREATE OR REPLACE FUNCTION stride_project_source_receipt_requires_canonical_source()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_event stride_conversation_events%ROWTYPE;
BEGIN
	SELECT * INTO source_event FROM stride_conversation_events
	 WHERE tenant_id=NEW.organization_id AND event_id=NEW.subject_id FOR SHARE;
	IF source_event.event_id IS NULL OR NEW.subject_contract_type<>'conversation_event' OR
	   source_event.content_revision<>NEW.subject_revision OR source_event.content_digest<>NEW.subject_digest OR
	   source_event.invalidated_at IS NOT NULL OR source_event.purge_generation<>NEW.purge_generation OR
	   source_event.visibility NOT IN ('private','project','channel') OR
	   NEW.source_audience->>'visibility'<>source_event.visibility OR
	   NOT (NEW.source_audience->'principals' @> jsonb_build_array(NEW.actor_person_id)) OR
	   source_event.acl_version<>NEW.source_acl_revision OR
	   source_event.audience_digest<>sha256(convert_to(NEW.source_audience::text,'UTF8')) OR
	   NEW.source_acl_digest<>sha256(convert_to(concat_ws(E'\x1f',source_event.tenant_id,source_event.event_id,source_event.content_revision::text,encode(source_event.content_digest,'hex'),encode(source_event.audience_digest,'hex'),source_event.visibility,source_event.acl_version::text,source_event.purge_generation::text),'UTF8')) OR
	   NEW.source_refs<>jsonb_build_array(jsonb_build_object('contractType','conversation_event','id',source_event.event_id,'revision',source_event.content_revision,'digest',encode(source_event.content_digest,'hex'))) THEN
		RAISE EXCEPTION 'Project source receipt requires exact authorized canonical conversation event';
	END IF;
	RETURN NEW;
END; $$;

CREATE OR REPLACE FUNCTION stride_project_association_requires_source_receipt()
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
	   source_event.visibility NOT IN ('private','project','channel') OR
	   NEW.source_audience->>'visibility'<>source_event.visibility OR
	   NOT (NEW.source_audience->'principals' @> jsonb_build_array(NEW.actor_person_id)) OR
	   source_event.acl_version<>NEW.source_acl_revision OR
	   source_event.audience_digest<>sha256(convert_to(NEW.source_audience::text,'UTF8')) OR
	   NEW.source_acl_digest<>sha256(convert_to(concat_ws(E'\x1f',source_event.tenant_id,source_event.event_id,source_event.content_revision::text,encode(source_event.content_digest,'hex'),encode(source_event.audience_digest,'hex'),source_event.visibility,source_event.acl_version::text,source_event.purge_generation::text),'UTF8')) OR
	   NEW.source_refs<>jsonb_build_array(jsonb_build_object('contractType','conversation_event','id',source_event.event_id,'revision',source_event.content_revision,'digest',encode(source_event.content_digest,'hex'))) THEN
		RAISE EXCEPTION 'ProjectAssociation requires exact current source authority receipt';
	END IF;
	RETURN NEW;
END; $$;

CREATE OR REPLACE FUNCTION stride_project_chat_send_requires_exact_truth()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE association stride_project_association_revisions%ROWTYPE;
DECLARE source_event stride_conversation_events%ROWTYPE;
BEGIN
  SELECT * INTO association FROM stride_project_association_revisions
   WHERE association_id=NEW.association_id AND revision=NEW.association_revision
     AND organization_id=NEW.organization_id FOR SHARE;
  SELECT * INTO source_event FROM stride_conversation_events
   WHERE tenant_id=NEW.organization_id AND event_id=NEW.conversation_event_id FOR SHARE;
  IF association.association_id IS NULL OR association.state<>'confirmed' OR
     association.project_id<>NEW.project_id OR association.project_revision<>NEW.project_revision OR
     association.subject_contract_type<>'conversation_event' OR association.subject_id<>NEW.conversation_event_id OR
     association.subject_revision<>NEW.conversation_event_revision OR association.actor_person_id<>NEW.actor_person_id OR
     association.actor_membership_id<>NEW.actor_membership_id OR association.actor_membership_revision<>NEW.actor_membership_revision OR
     association.session_subject_digest<>NEW.session_subject_digest OR association.session_revision<>NEW.session_revision OR
     association.authority_generation<>NEW.authority_generation OR
     source_event.event_id IS NULL OR source_event.source_id<>NEW.message_id OR source_event.thread_id<>NEW.thread_id OR
     source_event.author_principal<>NEW.actor_person_id OR source_event.visibility NOT IN ('private','project','channel') OR
     source_event.visibility<>association.source_audience->>'visibility' OR source_event.invalidated_at IS NOT NULL OR
     source_event.content_revision<>NEW.conversation_event_revision THEN
    RAISE EXCEPTION 'Project chat Send receipt requires exact confirmed canonical truth';
  END IF;
  RETURN NEW;
END; $$;

COMMIT;
