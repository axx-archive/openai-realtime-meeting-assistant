BEGIN;

-- The chat body remains in the existing encrypted/private conversation store.
-- This receipt is the body-free, restart-safe bridge from an explicitly sent
-- private Scout turn to its canonical Project association transaction.
CREATE TABLE stride_project_chat_send_receipts (
    organization_id text NOT NULL REFERENCES stride_organizations(organization_id),
    operation_id text NOT NULL,
    operation_key_digest bytea NOT NULL CHECK (octet_length(operation_key_digest)=32),
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint)=32),
    thread_id text NOT NULL,
    message_id text NOT NULL,
    conversation_event_id text NOT NULL,
    conversation_event_revision bigint NOT NULL CHECK (conversation_event_revision>0),
    project_id text NOT NULL,
    project_revision bigint NOT NULL CHECK (project_revision>0),
    association_id text NOT NULL,
    association_revision bigint NOT NULL CHECK (association_revision>0),
    actor_person_id text NOT NULL REFERENCES stride_person_principals(person_id),
    actor_membership_id text NOT NULL,
    actor_membership_revision bigint NOT NULL CHECK(actor_membership_revision>0),
    session_subject_digest bytea NOT NULL,
    session_revision bigint NOT NULL CHECK(session_revision>0),
    authority_generation bigint NOT NULL CHECK(authority_generation>0),
    status text NOT NULL CHECK(status='confirmed'),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY(organization_id,operation_id),
    UNIQUE(organization_id,operation_key_digest),
    UNIQUE(organization_id,conversation_event_id),
    UNIQUE(organization_id,association_id,association_revision),
    FOREIGN KEY(project_id,project_revision,organization_id)
      REFERENCES stride_project_revisions(project_id,revision,organization_id),
    FOREIGN KEY(association_id,association_revision,organization_id)
      REFERENCES stride_project_association_revisions(association_id,revision,organization_id),
    FOREIGN KEY(actor_membership_id,actor_membership_revision)
      REFERENCES stride_organization_membership_revisions(membership_id,revision),
    FOREIGN KEY(session_subject_digest)
      REFERENCES stride_active_organization_sessions(session_subject_digest),
    FOREIGN KEY(organization_id,conversation_event_id)
      REFERENCES stride_conversation_events(tenant_id,event_id),
    CHECK(operation_id=btrim(operation_id) AND operation_id<>''),
    CHECK(thread_id=btrim(thread_id) AND thread_id<>''),
    CHECK(message_id=btrim(message_id) AND message_id<>'')
);

CREATE FUNCTION stride_project_chat_send_requires_current_authority()
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
    RAISE EXCEPTION 'Project chat Send requires current organization session authority';
  END IF;
  RETURN NEW;
END; $$;

CREATE TRIGGER stride_project_chat_send_authority_guard
BEFORE INSERT ON stride_project_chat_send_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_send_requires_current_authority();

-- The send receipt is trusted for exact response-loss replay, so it must be
-- both append-only and reproducible from the canonical rows it summarizes.
CREATE FUNCTION stride_project_chat_send_requires_exact_truth()
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
     source_event.author_principal<>NEW.actor_person_id OR source_event.visibility<>'private' OR source_event.invalidated_at IS NOT NULL OR
     source_event.content_revision<>NEW.conversation_event_revision THEN
    RAISE EXCEPTION 'Project chat Send receipt requires exact confirmed canonical truth';
  END IF;
  RETURN NEW;
END; $$;
CREATE TRIGGER stride_project_chat_send_truth_guard
BEFORE INSERT ON stride_project_chat_send_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_send_requires_exact_truth();

CREATE FUNCTION stride_project_chat_send_receipt_immutable()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'Project chat Send receipts are append-only';
END; $$;
CREATE TRIGGER stride_project_chat_send_receipt_immutable_guard
BEFORE UPDATE OR DELETE ON stride_project_chat_send_receipts
FOR EACH ROW EXECUTE FUNCTION stride_project_chat_send_receipt_immutable();

COMMIT;
