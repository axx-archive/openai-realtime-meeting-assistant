BEGIN;

ALTER TABLE purge_ledger ADD COLUMN object_type text;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM purge_ledger p
        LEFT JOIN objects o
          ON o.tenant_id = p.tenant_id
         AND o.object_id = p.object_id
        GROUP BY p.tenant_id, p.object_id, p.revision_id
        HAVING count(o.object_type) <> 1
    ) THEN
        RAISE EXCEPTION 'purge ledger backfill requires exactly one canonical object family per existing row';
    END IF;
END $$;

UPDATE purge_ledger p
SET object_type = o.object_type
FROM objects o
WHERE o.tenant_id = p.tenant_id
  AND o.object_id = p.object_id;

ALTER TABLE purge_ledger
    ALTER COLUMN object_type SET NOT NULL,
    ADD CHECK (object_type = btrim(object_type) AND object_type <> ''),
    DROP CONSTRAINT purge_ledger_pkey,
    ADD PRIMARY KEY (tenant_id, object_type, object_id, revision_id);

CREATE INDEX purge_ledger_tenant_recorded
    ON purge_ledger (tenant_id, recorded_at, object_type, object_id, revision_id);

COMMIT;
