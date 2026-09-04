CREATE SCHEMA IF NOT EXISTS business;
DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='business_runtime') THEN CREATE ROLE business_runtime NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
CREATE TABLE business.organizations (id text PRIMARY KEY, name text NOT NULL, revision bigint NOT NULL DEFAULT 1);
CREATE TABLE business.memberships (organization_id text NOT NULL REFERENCES business.organizations, id text NOT NULL, person_id text NOT NULL, role text NOT NULL CHECK(role IN ('owner','member')), status text NOT NULL CHECK(status IN ('active','revoked')), revision bigint NOT NULL, PRIMARY KEY(organization_id,id), UNIQUE(organization_id,person_id));
CREATE TABLE business.businesses (organization_id text NOT NULL REFERENCES business.organizations, id text NOT NULL, body jsonb NOT NULL, PRIMARY KEY(organization_id,id));
CREATE TABLE business.employments (organization_id text NOT NULL, id text NOT NULL, business_id text NOT NULL, body jsonb NOT NULL, PRIMARY KEY(organization_id,id), FOREIGN KEY(organization_id,business_id) REFERENCES business.businesses);
CREATE TABLE business.mandates (organization_id text NOT NULL, id text NOT NULL, business_id text NOT NULL, employment_id text NOT NULL, body jsonb NOT NULL, PRIMARY KEY(organization_id,id), FOREIGN KEY(organization_id,business_id) REFERENCES business.businesses, FOREIGN KEY(organization_id,employment_id) REFERENCES business.employments);
CREATE TABLE business.budgets (organization_id text NOT NULL, business_id text NOT NULL, funded_micros bigint NOT NULL CHECK(funded_micros BETWEEN 0 AND 1000000000000000), cap_micros bigint NOT NULL CHECK(cap_micros BETWEEN 0 AND 1000000000000000), reserved_micros bigint NOT NULL DEFAULT 0 CHECK(reserved_micros>=0), revision bigint NOT NULL DEFAULT 1, PRIMARY KEY(organization_id,business_id), FOREIGN KEY(organization_id,business_id) REFERENCES business.businesses, CHECK(reserved_micros<=funded_micros AND reserved_micros<=cap_micros));
CREATE TABLE business.work_intents (organization_id text NOT NULL, id text NOT NULL, business_id text NOT NULL, employment_id text NOT NULL, mandate_id text NOT NULL, body jsonb NOT NULL, PRIMARY KEY(organization_id,id), FOREIGN KEY(organization_id,business_id) REFERENCES business.businesses, FOREIGN KEY(organization_id,employment_id) REFERENCES business.employments, FOREIGN KEY(organization_id,mandate_id) REFERENCES business.mandates);
CREATE INDEX work_business ON business.work_intents(organization_id,business_id);
CREATE TABLE business.operations (organization_id text NOT NULL REFERENCES business.organizations, idempotency_key text NOT NULL, actor_kind text NOT NULL, actor_id text NOT NULL, digest text NOT NULL, result jsonb NOT NULL, PRIMARY KEY(organization_id,idempotency_key));
CREATE TABLE business.events (organization_id text NOT NULL REFERENCES business.organizations, id bigint GENERATED ALWAYS AS IDENTITY, kind text NOT NULL, entity_id text NOT NULL, body jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(organization_id,id));
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['organizations','memberships','businesses','employments','mandates','budgets','work_intents','operations','events'] LOOP
  EXECUTE format('ALTER TABLE business.%I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('ALTER TABLE business.%I FORCE ROW LEVEL SECURITY',t);
  EXECUTE format('CREATE POLICY tenant_scope ON business.%I USING (%I = current_setting(''business.organization_id'',true)) WITH CHECK (%I = current_setting(''business.organization_id'',true))',t,CASE WHEN t='organizations' THEN 'id' ELSE 'organization_id' END,CASE WHEN t='organizations' THEN 'id' ELSE 'organization_id' END);
 END LOOP;
END $$;
GRANT USAGE ON SCHEMA business TO business_runtime;
GRANT SELECT,INSERT,UPDATE ON business.organizations,business.memberships,business.businesses,business.employments,business.mandates,business.budgets,business.work_intents TO business_runtime;
GRANT SELECT,INSERT ON business.operations,business.events TO business_runtime;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA business TO business_runtime;
-- Only this narrow directory query crosses scope. Its actor is supplied by the
-- authenticated server adapter; it grants no authority to the returned rows.
CREATE FUNCTION business.organizations_for_person(person text) RETURNS TABLE(id text,name text,revision bigint) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,business AS $$ SELECT o.id,o.name,o.revision FROM business.organizations o JOIN business.memberships m ON m.organization_id=o.id WHERE m.person_id=person AND m.status='active' ORDER BY o.name,o.id $$;
REVOKE ALL ON FUNCTION business.organizations_for_person(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION business.organizations_for_person(text) TO business_runtime;
