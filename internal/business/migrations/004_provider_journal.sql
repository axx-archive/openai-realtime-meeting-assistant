-- Host grants are independent, explicitly allocated budgets. Runtime can consume
-- a grant but cannot create, raise, replace, or revoke its host-owned policy.
CREATE TABLE business.provider_grants (
 organization_id text NOT NULL, id text NOT NULL, business_id text NOT NULL,
 status text NOT NULL CHECK(status IN ('active','revoked')), body jsonb NOT NULL,
 PRIMARY KEY(organization_id,id), FOREIGN KEY(organization_id,business_id) REFERENCES business.businesses
);
CREATE TABLE business.provider_requests (
 organization_id text NOT NULL, work_id text NOT NULL, grant_id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(organization_id,work_id), FOREIGN KEY(organization_id,work_id) REFERENCES business.work_intents,
 FOREIGN KEY(organization_id,grant_id) REFERENCES business.provider_grants
);
CREATE TABLE business.provider_reservations (
 organization_id text NOT NULL, work_id text NOT NULL, grant_id text NOT NULL,
 held_micros bigint NOT NULL CHECK(held_micros BETWEEN 0 AND 1000000000000000),
 settled_micros bigint NOT NULL DEFAULT 0 CHECK(settled_micros BETWEEN 0 AND 1000000000000000),
 slot_reserved boolean NOT NULL DEFAULT true,
 PRIMARY KEY(organization_id,work_id), FOREIGN KEY(organization_id,work_id) REFERENCES business.provider_requests,
 FOREIGN KEY(organization_id,grant_id) REFERENCES business.provider_grants
);
CREATE INDEX provider_reservations_grant ON business.provider_reservations(organization_id,grant_id);
CREATE TABLE business.provider_journal (
 organization_id text NOT NULL, id text NOT NULL, work_id text NOT NULL, attempt_id text NOT NULL,
 grant_id text NOT NULL, account_id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(organization_id,id), UNIQUE(organization_id,attempt_id), UNIQUE(organization_id,id,account_id),
 FOREIGN KEY(organization_id,work_id) REFERENCES business.provider_requests,
 FOREIGN KEY(organization_id,attempt_id) REFERENCES business.attempts,
 FOREIGN KEY(organization_id,grant_id) REFERENCES business.provider_grants
);
CREATE INDEX provider_journal_grant ON business.provider_journal(organization_id,grant_id);
CREATE TABLE business.provider_receipt_capabilities (
 organization_id text NOT NULL, operation_id text NOT NULL, token_digest text NOT NULL, generation bigint NOT NULL,
 PRIMARY KEY(organization_id,operation_id,token_digest),
 FOREIGN KEY(organization_id,operation_id) REFERENCES business.provider_journal
);
CREATE TABLE business.provider_response_bindings (
 organization_id text NOT NULL, operation_id text NOT NULL, account_id text NOT NULL, response_id text NOT NULL,
 PRIMARY KEY(account_id,response_id), UNIQUE(organization_id,operation_id),
 FOREIGN KEY(organization_id,operation_id,account_id) REFERENCES business.provider_journal(organization_id,id,account_id)
);
CREATE TABLE business.provider_facts (
 organization_id text NOT NULL, id text NOT NULL, operation_id text NOT NULL, idempotency_key text NOT NULL,
 digest text NOT NULL, kind text NOT NULL CHECK(kind IN ('accepted','terminal')), body jsonb NOT NULL, sequence integer NOT NULL CHECK(sequence BETWEEN 1 AND 10),
 PRIMARY KEY(organization_id,id), UNIQUE(organization_id,operation_id,idempotency_key), UNIQUE(organization_id,operation_id,sequence),
 FOREIGN KEY(organization_id,operation_id) REFERENCES business.provider_journal
);
CREATE INDEX provider_facts_operation ON business.provider_facts(organization_id,operation_id,kind);
DO $$ DECLARE t text; BEGIN FOREACH t IN ARRAY ARRAY['provider_grants','provider_requests','provider_reservations','provider_journal','provider_receipt_capabilities','provider_response_bindings','provider_facts'] LOOP
 EXECUTE format('ALTER TABLE business.%I ENABLE ROW LEVEL SECURITY',t);
 EXECUTE format('ALTER TABLE business.%I FORCE ROW LEVEL SECURITY',t);
 EXECUTE format('CREATE POLICY tenant_scope ON business.%I USING (organization_id=current_setting(''business.organization_id'',true)) WITH CHECK (organization_id=current_setting(''business.organization_id'',true))',t);
END LOOP; END $$;
GRANT SELECT ON business.provider_grants TO business_runtime;
GRANT SELECT,INSERT ON business.provider_requests,business.provider_journal,business.provider_receipt_capabilities,business.provider_response_bindings,business.provider_facts TO business_runtime;
GRANT SELECT,INSERT,UPDATE ON business.provider_reservations TO business_runtime;
-- Existing attempt/work writers settle and release this reservation in the same
-- transaction. Dispatch must be disabled before rollback; pre-004 images reject
-- the extra migration rather than safely maintaining the new resource ledger.
