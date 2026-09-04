-- These indexes match the bounded overview ordering without sorting all history.
CREATE INDEX business_work_overview_recent ON business.work_intents(organization_id,business_id,(body->>'createdAt') DESC,id DESC);
CREATE INDEX business_employment_overview ON business.employments(organization_id,business_id,id);

-- Disposable read projection only. Spending admission and reconciliation retain
-- their authoritative attempt/work/settlement checks. The trigger runs inside
-- the existing attempt transaction, including retained image INSERT/UPDATEs.
CREATE TABLE business.unknown_cost_operations (
 organization_id text NOT NULL,
 business_id text NOT NULL,
 attempt_id text NOT NULL,
 PRIMARY KEY(organization_id,business_id,attempt_id),
 UNIQUE(organization_id,attempt_id),
 FOREIGN KEY(organization_id,business_id) REFERENCES business.businesses,
 FOREIGN KEY(organization_id,attempt_id) REFERENCES business.attempts
);
ALTER TABLE business.unknown_cost_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE business.unknown_cost_operations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_scope ON business.unknown_cost_operations
 USING(organization_id=current_setting('business.organization_id',true))
 WITH CHECK(organization_id=current_setting('business.organization_id',true));
GRANT SELECT,INSERT,DELETE ON business.unknown_cost_operations TO business_runtime;

-- Invoker rights: callers see only their transaction's current tenant. Exact
-- Business binding comes from the Work FK, never from attempt JSON or a caller.
CREATE FUNCTION business.validate_unknown_cost_projection() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog,business AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM business.attempts a JOIN business.work_intents w
  ON w.organization_id=a.organization_id AND w.id=a.work_id
  WHERE a.organization_id=NEW.organization_id AND a.id=NEW.attempt_id
   AND w.business_id=NEW.business_id AND a.body->>'costState'='unknown') THEN
  RAISE EXCEPTION 'unknown cost projection has no exact current source' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER business_unknown_cost_validate BEFORE INSERT ON business.unknown_cost_operations
 FOR EACH ROW EXECUTE FUNCTION business.validate_unknown_cost_projection();
REVOKE ALL ON FUNCTION business.validate_unknown_cost_projection() FROM PUBLIC;

CREATE FUNCTION business.project_unknown_cost_operation() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog,business AS $$
DECLARE bound_business text;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.organization_id<>OLD.organization_id OR NEW.id<>OLD.id OR NEW.work_id<>OLD.work_id THEN
   RAISE EXCEPTION 'attempt identity is immutable' USING ERRCODE='23514';
  END IF;
  IF (NEW.body->>'costState') IS NOT DISTINCT FROM (OLD.body->>'costState') THEN RETURN NEW; END IF;
 END IF;
 IF NEW.body->>'costState'='unknown' THEN
  SELECT w.business_id INTO STRICT bound_business FROM business.work_intents w
   WHERE w.organization_id=NEW.organization_id AND w.id=NEW.work_id;
  INSERT INTO business.unknown_cost_operations VALUES(NEW.organization_id,bound_business,NEW.id);
 ELSE
  DELETE FROM business.unknown_cost_operations WHERE organization_id=NEW.organization_id AND attempt_id=NEW.id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER business_attempt_unknown_cost AFTER INSERT OR UPDATE ON business.attempts
 FOR EACH ROW EXECUTE FUNCTION business.project_unknown_cost_operation();
REVOKE ALL ON FUNCTION business.project_unknown_cost_operation() FROM PUBLIC;

-- Migrate owns one transaction/advisory lock; no partial backfill is visible.
INSERT INTO business.unknown_cost_operations(organization_id,business_id,attempt_id)
 SELECT a.organization_id,w.business_id,a.id FROM business.attempts a
 JOIN business.work_intents w ON w.organization_id=a.organization_id AND w.id=a.work_id
 WHERE a.body->>'costState'='unknown';

-- Additive rollback: retained attempt-aware writes still run these invoker
-- triggers. Do not drop the projection while those triggers exist. A binary
-- expecting only migrations001/002 rejects the extra version at startup, so
-- rollback must use a compatible image or disable the whole Business DB binding.
