BEGIN;

-- One immutable receipt bridges a user-confirmed correction of a sent chat
-- message to the append-only ProjectAssociation graph. It covers both
-- replacement (A -> B) and explicit removal (A -> no Project) without
-- rewriting the original Send receipt or source message.
CREATE TABLE stride_project_chat_correction_receipts (
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    operation_id text NOT NULL,
    operation_key_digest bytea NOT NULL CHECK(octet_length(operation_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
    token_digest bytea NOT NULL CHECK(octet_length(token_digest)=32),
    thread_id text NOT NULL,
    message_id text NOT NULL,
    source_event_id text NOT NULL,
    source_event_revision bigint NOT NULL CHECK(source_event_revision>0),
    old_association_id text NOT NULL,
    old_association_revision bigint NOT NULL CHECK(old_association_revision>0),
    old_result_revision bigint NOT NULL CHECK(old_result_revision=old_association_revision+1),
    result_state text NOT NULL CHECK(result_state IN ('corrected','removed')),
    replacement_association_id text,
    replacement_association_revision bigint,
    replacement_project_id text,
    replacement_project_revision bigint,
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
    FOREIGN KEY(organization_id,source_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    FOREIGN KEY(old_association_id,old_result_revision,organization_id)
      REFERENCES stride_project_association_revisions(association_id,revision,organization_id),
    FOREIGN KEY(replacement_association_id,replacement_association_revision,organization_id)
      REFERENCES stride_project_association_revisions(association_id,revision,organization_id),
    FOREIGN KEY(replacement_project_id,replacement_project_revision,organization_id)
      REFERENCES stride_project_revisions(project_id,revision,organization_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision)
      REFERENCES stride_organization_membership_revisions(membership_id,revision),
    FOREIGN KEY(session_subject_digest)
      REFERENCES stride_active_organization_sessions(session_subject_digest),
    CHECK(operation_id=btrim(operation_id) AND operation_id<>''),
    CHECK(thread_id=btrim(thread_id) AND thread_id<>''),
    CHECK(message_id=btrim(message_id) AND message_id<>''),
    CHECK((result_state='corrected')=(replacement_association_id IS NOT NULL)),
    CHECK((replacement_association_id IS NULL)=(replacement_association_revision IS NULL) AND
          (replacement_association_id IS NULL)=(replacement_project_id IS NULL) AND
          (replacement_association_id IS NULL)=(replacement_project_revision IS NULL)),
    CHECK(replacement_association_revision IS NULL OR replacement_association_revision=1)
);

CREATE FUNCTION stride_project_chat_correction_requires_current_authority()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE membership stride_organization_memberships_current%ROWTYPE;
DECLARE session_binding stride_active_organization_sessions%ROWTYPE;
BEGIN
  SELECT * INTO membership FROM stride_organization_memberships_current
   WHERE membership_id=NEW.actor_membership_id FOR SHARE;
  SELECT * INTO session_binding FROM stride_active_organization_sessions
   WHERE session_subject_digest=NEW.session_subject_digest FOR SHARE;
  IF membership.membership_id IS NULL OR membership.status<>'active' OR
     membership.organization_id<>NEW.organization_id OR membership.person_id<>NEW.actor_person_id OR
     membership.revision<>NEW.actor_membership_revision OR
     session_binding.session_subject_digest IS NULL OR session_binding.status<>'active' OR
     session_binding.expires_at<=clock_timestamp() OR session_binding.person_id<>NEW.actor_person_id OR
     session_binding.organization_id<>NEW.organization_id OR session_binding.membership_id<>NEW.actor_membership_id OR
     session_binding.membership_revision<>NEW.actor_membership_revision OR
     session_binding.session_revision<>NEW.session_revision OR
     session_binding.authority_generation<>NEW.authority_generation THEN
    RAISE EXCEPTION 'Project chat correction requires current organization session authority';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_chat_correction_authority_guard
BEFORE INSERT ON stride_project_chat_correction_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_correction_requires_current_authority();

CREATE FUNCTION stride_project_chat_correction_requires_exact_truth()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_result stride_project_association_revisions%ROWTYPE;
DECLARE old_current stride_project_associations_current%ROWTYPE;
DECLARE replacement stride_project_association_revisions%ROWTYPE;
DECLARE replacement_current stride_project_associations_current%ROWTYPE;
DECLARE source_event stride_conversation_events%ROWTYPE;
BEGIN
  IF EXISTS(SELECT 1 FROM stride_project_chat_correction_abandonments abandonment
    WHERE abandonment.organization_id=NEW.organization_id AND abandonment.operation_id=NEW.operation_id) THEN
    RAISE EXCEPTION 'Project chat correction operation was abandoned';
  END IF;
  SELECT * INTO old_result FROM stride_project_association_revisions
   WHERE association_id=NEW.old_association_id AND revision=NEW.old_result_revision
     AND organization_id=NEW.organization_id FOR SHARE;
  SELECT * INTO old_current FROM stride_project_associations_current
   WHERE association_id=NEW.old_association_id AND organization_id=NEW.organization_id FOR SHARE;
  SELECT * INTO source_event FROM stride_conversation_events
   WHERE tenant_id=NEW.organization_id AND event_id=NEW.source_event_id FOR SHARE;
  IF old_result.association_id IS NULL OR old_result.state<>NEW.result_state OR
     old_result.supersedes_revision<>NEW.old_association_revision OR
     old_result.actor_person_id<>NEW.actor_person_id OR
     old_result.actor_membership_id<>NEW.actor_membership_id OR
     old_result.actor_membership_revision<>NEW.actor_membership_revision OR
     old_result.session_subject_digest<>NEW.session_subject_digest OR
     old_result.session_revision<>NEW.session_revision OR
     old_result.authority_generation<>NEW.authority_generation OR
     old_current.association_id IS NULL OR old_current.revision<>NEW.old_result_revision OR
     old_current.state<>NEW.result_state OR
     source_event.event_id IS NULL OR source_event.source_id<>NEW.message_id OR
     source_event.thread_id<>NEW.thread_id OR source_event.author_principal<>NEW.actor_person_id OR
     source_event.content_revision<>NEW.source_event_revision OR source_event.invalidated_at IS NOT NULL OR
     old_result.subject_id<>NEW.source_event_id OR old_result.subject_revision<>NEW.source_event_revision THEN
    RAISE EXCEPTION 'Project chat correction receipt requires exact canonical result';
  END IF;
  IF NEW.result_state='corrected' THEN
    SELECT * INTO replacement FROM stride_project_association_revisions
     WHERE association_id=NEW.replacement_association_id AND revision=NEW.replacement_association_revision
       AND organization_id=NEW.organization_id FOR SHARE;
    SELECT * INTO replacement_current FROM stride_project_associations_current
     WHERE association_id=NEW.replacement_association_id AND organization_id=NEW.organization_id FOR SHARE;
    IF replacement.association_id IS NULL OR replacement.state<>'confirmed' OR
       replacement.project_id<>NEW.replacement_project_id OR replacement.project_revision<>NEW.replacement_project_revision OR
       replacement.subject_id<>NEW.source_event_id OR replacement.subject_revision<>NEW.source_event_revision OR
       replacement.actor_person_id<>NEW.actor_person_id OR
       replacement.actor_membership_id<>NEW.actor_membership_id OR
       replacement.actor_membership_revision<>NEW.actor_membership_revision OR
       replacement.session_subject_digest<>NEW.session_subject_digest OR
       replacement.session_revision<>NEW.session_revision OR replacement.authority_generation<>NEW.authority_generation OR
       replacement_current.association_id IS NULL OR replacement_current.revision<>1 OR replacement_current.state<>'confirmed' THEN
      RAISE EXCEPTION 'Project chat correction receipt requires exact replacement truth';
    END IF;
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_chat_correction_truth_guard
BEFORE INSERT ON stride_project_chat_correction_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_correction_requires_exact_truth();

CREATE FUNCTION stride_project_chat_correction_receipt_immutable()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'Project chat correction receipts are append-only';
END; $$;
CREATE TRIGGER stride_project_chat_correction_receipt_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_correction_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_correction_receipt_immutable();

-- A client may disappear after the legacy journal is durable but before the
-- canonical correction commits. Recovery may reopen the chooser only by
-- recording this immutable abandonment while holding the same organization
-- advisory lock as correction commits. A correction receipt and abandonment
-- for one operation are therefore mutually exclusive.
CREATE TABLE stride_project_chat_correction_abandonments (
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    operation_id text NOT NULL,
    token_digest bytea NOT NULL CHECK(octet_length(token_digest)=32),
    thread_id text NOT NULL,
    message_id text NOT NULL,
    old_association_id text NOT NULL,
    old_association_revision bigint NOT NULL CHECK(old_association_revision>0),
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
    session_subject_digest bytea NOT NULL,
    session_revision bigint NOT NULL CHECK(session_revision>0),
    authority_generation bigint NOT NULL CHECK(authority_generation>0),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,operation_id),
    FOREIGN KEY(old_association_id,old_association_revision,organization_id)
      REFERENCES stride_project_association_revisions(association_id,revision,organization_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision)
      REFERENCES stride_organization_membership_revisions(membership_id,revision),
    FOREIGN KEY(session_subject_digest)
      REFERENCES stride_active_organization_sessions(session_subject_digest),
    CHECK(operation_id=btrim(operation_id) AND operation_id<>''),
    CHECK(thread_id=btrim(thread_id) AND thread_id<>''),
    CHECK(message_id=btrim(message_id) AND message_id<>'')
);
CREATE TRIGGER stride_project_chat_correction_abandonment_authority_guard
BEFORE INSERT ON stride_project_chat_correction_abandonments
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_correction_requires_current_authority();

CREATE FUNCTION stride_project_chat_correction_abandonment_requires_exact_truth()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  association_record stride_project_association_revisions%ROWTYPE;
  current_record stride_project_associations_current%ROWTYPE;
  source_event stride_conversation_events%ROWTYPE;
BEGIN
  IF EXISTS(SELECT 1 FROM stride_project_chat_correction_receipts receipt
    WHERE receipt.organization_id=NEW.organization_id AND receipt.operation_id=NEW.operation_id) THEN
    RAISE EXCEPTION 'Project chat correction already committed';
  END IF;
  SELECT * INTO association_record FROM stride_project_association_revisions
   WHERE organization_id=NEW.organization_id AND association_id=NEW.old_association_id
     AND revision=NEW.old_association_revision FOR SHARE;
  SELECT * INTO current_record FROM stride_project_associations_current
   WHERE organization_id=NEW.organization_id AND association_id=NEW.old_association_id FOR SHARE;
  SELECT * INTO source_event FROM stride_conversation_events
   WHERE tenant_id=NEW.organization_id AND event_id=association_record.subject_id FOR SHARE;
  IF association_record.association_id IS NULL OR association_record.state<>'confirmed' OR
     association_record.subject_contract_type<>'conversation_event' OR
     association_record.actor_person_id<>NEW.actor_person_id OR
     current_record.association_id IS NULL OR current_record.revision<>NEW.old_association_revision OR
     current_record.state<>'confirmed' OR source_event.event_id IS NULL OR
     source_event.thread_id<>NEW.thread_id OR source_event.source_id<>NEW.message_id OR
     source_event.author_principal<>NEW.actor_person_id OR source_event.invalidated_at IS NOT NULL THEN
    RAISE EXCEPTION 'Project chat correction abandonment requires exact current truth';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_chat_correction_abandonment_truth_guard
BEFORE INSERT ON stride_project_chat_correction_abandonments
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_correction_abandonment_requires_exact_truth();
CREATE TRIGGER stride_project_chat_correction_abandonment_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_correction_abandonments
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_correction_receipt_immutable();

-- Editing or deleting a message that anchors Project truth is a second
-- cross-store transaction. The legacy chat journal is written first; this
-- immutable receipt then proves that the exact canonical source was
-- invalidated before the edited/deleted body becomes visible.
CREATE TABLE stride_project_chat_source_mutation_receipts (
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    operation_id text NOT NULL,
    operation_key_digest bytea NOT NULL CHECK(octet_length(operation_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK(octet_length(request_fingerprint)=32),
    mutation_kind text NOT NULL CHECK(mutation_kind IN ('edit','delete')),
    thread_id text NOT NULL,
    message_id text NOT NULL,
    source_event_id text NOT NULL,
    source_prior_revision bigint NOT NULL CHECK(source_prior_revision>0),
    source_result_revision bigint NOT NULL CHECK(source_result_revision=source_prior_revision+1),
    association_id text NOT NULL,
    association_revision bigint NOT NULL CHECK(association_revision>0),
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
    session_subject_digest bytea NOT NULL,
    session_revision bigint NOT NULL CHECK(session_revision>0),
    authority_generation bigint NOT NULL CHECK(authority_generation>0),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,operation_id),
    UNIQUE(organization_id,operation_key_digest),
    FOREIGN KEY(organization_id,source_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    FOREIGN KEY(association_id,association_revision,organization_id)
      REFERENCES stride_project_association_revisions(association_id,revision,organization_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision)
      REFERENCES stride_organization_membership_revisions(membership_id,revision),
    FOREIGN KEY(session_subject_digest)
      REFERENCES stride_active_organization_sessions(session_subject_digest),
    CHECK(operation_id=btrim(operation_id) AND operation_id<>''),
    CHECK(thread_id=btrim(thread_id) AND thread_id<>''),
    CHECK(message_id=btrim(message_id) AND message_id<>'')
);

CREATE TRIGGER stride_project_chat_source_mutation_authority_guard
BEFORE INSERT ON stride_project_chat_source_mutation_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_correction_requires_current_authority();

CREATE FUNCTION stride_project_chat_source_mutation_requires_exact_truth()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  source_event stride_conversation_events%ROWTYPE;
  association_record stride_project_association_revisions%ROWTYPE;
BEGIN
  SELECT * INTO source_event FROM stride_conversation_events
   WHERE tenant_id=NEW.organization_id AND event_id=NEW.source_event_id FOR SHARE;
  SELECT * INTO association_record FROM stride_project_association_revisions
   WHERE organization_id=NEW.organization_id AND association_id=NEW.association_id AND revision=NEW.association_revision FOR SHARE;
  IF source_event.event_id IS NULL OR source_event.thread_id<>NEW.thread_id OR
     source_event.source_id<>NEW.message_id OR source_event.author_principal<>NEW.actor_person_id OR
     source_event.content_revision<>NEW.source_result_revision OR source_event.invalidated_at IS NULL OR
     source_event.event_type<>NEW.mutation_kind OR
     source_event.invalidation_reason<>(NEW.mutation_kind || ':' || NEW.operation_id) OR
     association_record.association_id IS NULL OR association_record.subject_contract_type<>'conversation_event' OR
     association_record.subject_id<>NEW.source_event_id OR association_record.subject_revision<>NEW.source_prior_revision OR
     association_record.actor_person_id<>NEW.actor_person_id OR
     EXISTS(SELECT 1 FROM stride_project_associations_authorized_current current_association
       WHERE current_association.organization_id=NEW.organization_id AND
             current_association.association_id=NEW.association_id) THEN
    RAISE EXCEPTION 'Project chat source mutation receipt requires exact invalidated canonical truth';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_chat_source_mutation_truth_guard
BEFORE INSERT ON stride_project_chat_source_mutation_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_source_mutation_requires_exact_truth();

CREATE TRIGGER stride_project_chat_source_mutation_receipt_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_source_mutation_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_correction_receipt_immutable();

COMMIT;
