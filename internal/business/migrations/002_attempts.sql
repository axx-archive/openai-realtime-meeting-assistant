ALTER TABLE business.budgets ADD COLUMN settled_micros bigint NOT NULL DEFAULT 0 CHECK(settled_micros BETWEEN 0 AND 1000000000000000);
-- Actual settlement may exceed the authorized allowance. Preserve the truth;
-- admission enforces the ceiling and blocks an overdrawn balance.
ALTER TABLE business.budgets DROP CONSTRAINT budgets_check;
UPDATE business.work_intents SET body=body || jsonb_build_object('heldMicros',CASE WHEN body->>'status'='admitted' THEN (body->>'reservationMicros')::bigint ELSE 0 END,'settledMicros',0);
CREATE TABLE business.attempts(organization_id text NOT NULL, id text NOT NULL, work_id text NOT NULL, ordinal integer NOT NULL CHECK(ordinal>0), body jsonb NOT NULL, PRIMARY KEY(organization_id,id), UNIQUE(organization_id,work_id,ordinal), FOREIGN KEY(organization_id,work_id) REFERENCES business.work_intents);
CREATE TABLE business.results(organization_id text NOT NULL,id text NOT NULL,work_id text NOT NULL,attempt_id text NOT NULL,body jsonb NOT NULL,PRIMARY KEY(organization_id,id),UNIQUE(organization_id,work_id),FOREIGN KEY(organization_id,work_id) REFERENCES business.work_intents,FOREIGN KEY(organization_id,attempt_id) REFERENCES business.attempts);
CREATE TABLE business.settlements(organization_id text NOT NULL,attempt_id text NOT NULL,operation_id text NOT NULL,actual_micros bigint CHECK(actual_micros BETWEEN 0 AND 1000000000000000),evidence_ref text NOT NULL,PRIMARY KEY(organization_id,attempt_id),UNIQUE(organization_id,operation_id),FOREIGN KEY(organization_id,attempt_id) REFERENCES business.attempts);
DO $$ DECLARE t text; BEGIN FOREACH t IN ARRAY ARRAY['attempts','results','settlements'] LOOP
 EXECUTE format('ALTER TABLE business.%I ENABLE ROW LEVEL SECURITY',t);
 EXECUTE format('ALTER TABLE business.%I FORCE ROW LEVEL SECURITY',t);
 EXECUTE format('CREATE POLICY tenant_scope ON business.%I USING (organization_id=current_setting(''business.organization_id'',true)) WITH CHECK (organization_id=current_setting(''business.organization_id'',true))',t);
END LOOP;END $$;
GRANT SELECT,INSERT,UPDATE ON business.attempts,business.settlements TO business_runtime;
GRANT SELECT,INSERT ON business.results TO business_runtime;

UPDATE business.work_intents w SET body=w.body || jsonb_build_object('businessRevision',(b.body->>'revision')::bigint,'employmentRevision',(e.body->>'revision')::bigint) FROM business.businesses b,business.employments e WHERE b.organization_id=w.organization_id AND b.id=w.business_id AND e.organization_id=w.organization_id AND e.id=w.employment_id;

GRANT SELECT ON business.schema_migrations TO business_runtime;
