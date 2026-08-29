BEGIN;

CREATE TABLE stride_source_episode_tenant_fences (
    tenant_id text PRIMARY KEY,
    purge_generation bigint NOT NULL DEFAULT 0 CHECK (purge_generation >= 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE stride_source_episode_revisions (
    tenant_id text NOT NULL,
    episode_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest)=32),
    kind text NOT NULL,
    source_family text NOT NULL,
    source_object_id text NOT NULL,
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    source_digest bytea NOT NULL CHECK (octet_length(source_digest)=32),
    retrieval_family text NOT NULL,
    retrieval_object_id text NOT NULL,
    retrieval_revision bigint NOT NULL CHECK (retrieval_revision > 0),
    retrieval_digest bytea NOT NULL CHECK (octet_length(retrieval_digest)=32),
    sitting_id text,
    acl_revision bigint NOT NULL CHECK (acl_revision > 0),
    acl_digest bytea NOT NULL CHECK (octet_length(acl_digest)=32),
    consent_revision bigint NOT NULL CHECK (consent_revision > 0),
    consent_digest bytea NOT NULL CHECK (octet_length(consent_digest)=32),
    purge_generation bigint NOT NULL CHECK (purge_generation >= 0),
    phase_receipt_digest bytea NOT NULL CHECK (octet_length(phase_receipt_digest)=32),
    idempotency_key_digest bytea NOT NULL CHECK (octet_length(idempotency_key_digest)=32),
    supersedes_revision bigint,
    supersedes_digest bytea,
    episode jsonb NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,episode_id,revision),
    UNIQUE (tenant_id,idempotency_key_digest),
    CHECK ((supersedes_revision IS NULL)=(supersedes_digest IS NULL)),
    CHECK (supersedes_digest IS NULL OR octet_length(supersedes_digest)=32),
    CHECK (jsonb_typeof(episode)='object')
);

CREATE TABLE stride_source_episode_heads (
    tenant_id text NOT NULL,
    episode_id text NOT NULL,
    revision bigint NOT NULL,
    content_digest bytea NOT NULL CHECK (octet_length(content_digest)=32),
    sitting_id text,
    purge_generation bigint NOT NULL CHECK (purge_generation >= 0),
    active boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,episode_id),
    FOREIGN KEY (tenant_id,episode_id,revision)
      REFERENCES stride_source_episode_revisions(tenant_id,episode_id,revision)
);

CREATE TABLE stride_source_episode_sources (
    tenant_id text NOT NULL,
    episode_id text NOT NULL,
    episode_revision bigint NOT NULL,
    object_type text NOT NULL,
    object_id text NOT NULL,
    content_digest bytea NOT NULL CHECK (octet_length(content_digest)=32),
    PRIMARY KEY (tenant_id,episode_id,episode_revision,object_type,object_id),
    FOREIGN KEY (tenant_id,episode_id,episode_revision)
      REFERENCES stride_source_episode_revisions(tenant_id,episode_id,revision)
);
CREATE INDEX stride_source_episode_sources_object
  ON stride_source_episode_sources(tenant_id,object_type,object_id);

CREATE TABLE stride_source_episode_tombstones (
    tenant_id text NOT NULL,
    episode_id text NOT NULL,
    episode_revision bigint NOT NULL,
    episode_digest bytea NOT NULL CHECK (octet_length(episode_digest)=32),
    cause text NOT NULL CHECK (cause IN ('purge','source_retracted','source_corrected','consent_withdrawn','acl_revoked')),
    purge_generation bigint NOT NULL CHECK (purge_generation >= 0),
    reason_digest bytea NOT NULL CHECK (octet_length(reason_digest)=32),
    idempotency_key_digest bytea NOT NULL CHECK (octet_length(idempotency_key_digest)=32),
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,idempotency_key_digest),
    FOREIGN KEY (tenant_id,episode_id,episode_revision)
      REFERENCES stride_source_episode_revisions(tenant_id,episode_id,revision)
);

CREATE OR REPLACE FUNCTION stride_tombstone_source_episode_heads(
  p_tenant text,p_episode_ids text[],p_cause text,p_purge bigint,p_reason text
) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  WITH affected AS (
    UPDATE stride_source_episode_heads h SET active=false,updated_at=clock_timestamp()
    WHERE h.tenant_id=p_tenant AND h.active AND h.episode_id=ANY(p_episode_ids)
    RETURNING h.*
  )
  INSERT INTO stride_source_episode_tombstones(
    tenant_id,episode_id,episode_revision,episode_digest,cause,purge_generation,
    reason_digest,idempotency_key_digest,occurred_at)
  SELECT tenant_id,episode_id,revision,content_digest,p_cause,p_purge,
    sha256(convert_to(concat_ws(E'\x1f',p_reason,episode_id,revision::text,encode(content_digest,'hex')),'UTF8')),
    sha256(convert_to(concat_ws(E'\x1f',tenant_id,episode_id,revision::text,p_cause,p_purge::text),'UTF8')),
    clock_timestamp() FROM affected
  ON CONFLICT (tenant_id,idempotency_key_digest) DO NOTHING;
END $$;

CREATE OR REPLACE FUNCTION stride_source_episode_source_drift_trigger() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ids text[];
BEGIN
  SELECT array_agg(DISTINCT episode_id) INTO ids FROM stride_source_episode_sources
   WHERE tenant_id=NEW.tenant_id AND object_type=NEW.object_type AND object_id=NEW.object_id;
  IF ids IS NOT NULL THEN
    PERFORM stride_tombstone_source_episode_heads(NEW.tenant_id,ids,'source_corrected',
      COALESCE((SELECT purge_generation FROM stride_source_episode_tenant_fences WHERE tenant_id=NEW.tenant_id),0),'canonical_source_drift');
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER stride_source_episode_source_drift
AFTER UPDATE OF content_revision,content_sha256,deleted_at ON objects
FOR EACH ROW WHEN (OLD.content_revision IS DISTINCT FROM NEW.content_revision OR OLD.content_sha256 IS DISTINCT FROM NEW.content_sha256 OR OLD.deleted_at IS DISTINCT FROM NEW.deleted_at)
EXECUTE FUNCTION stride_source_episode_source_drift_trigger();

CREATE OR REPLACE FUNCTION stride_source_episode_acl_drift_trigger() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE tenant text;otype text;oid text;ids text[];purge bigint;
BEGIN
  tenant:=COALESCE(NEW.tenant_id,OLD.tenant_id); otype:=COALESCE(NEW.object_type,OLD.object_type); oid:=COALESCE(NEW.object_id,OLD.object_id);
  SELECT array_agg(DISTINCT episode_id) INTO ids FROM stride_source_episode_sources WHERE tenant_id=tenant AND object_type=otype AND object_id=oid;
  SELECT COALESCE(purge_generation,0) INTO purge FROM stride_source_episode_tenant_fences WHERE tenant_id=tenant;
  IF ids IS NOT NULL THEN PERFORM stride_tombstone_source_episode_heads(tenant,ids,'acl_revoked',COALESCE(purge,0),'canonical_acl_drift'); END IF;
  RETURN COALESCE(NEW,OLD);
END $$;
CREATE TRIGGER stride_source_episode_acl_drift
AFTER INSERT OR UPDATE OR DELETE ON object_grants FOR EACH ROW EXECUTE FUNCTION stride_source_episode_acl_drift_trigger();

CREATE OR REPLACE FUNCTION stride_source_episode_consent_drift_trigger() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ids text[];purge bigint;
BEGIN
  SELECT array_agg(episode_id) INTO ids FROM stride_source_episode_heads
   WHERE tenant_id=NEW.tenant_id AND sitting_id=NEW.sitting_id AND active;
  SELECT COALESCE(purge_generation,0) INTO purge FROM stride_source_episode_tenant_fences WHERE tenant_id=NEW.tenant_id;
  IF ids IS NOT NULL THEN PERFORM stride_tombstone_source_episode_heads(NEW.tenant_id,ids,'consent_withdrawn',COALESCE(purge,0),'consent_authority_changed'); END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER stride_source_episode_consent_drift
AFTER INSERT ON consent_records FOR EACH ROW EXECUTE FUNCTION stride_source_episode_consent_drift_trigger();

CREATE OR REPLACE FUNCTION stride_source_episode_purge_trigger() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_generation bigint;ids text[];
BEGIN
  INSERT INTO stride_source_episode_tenant_fences(tenant_id,purge_generation) VALUES(NEW.tenant_id,1)
  ON CONFLICT(tenant_id) DO UPDATE SET purge_generation=stride_source_episode_tenant_fences.purge_generation+1,updated_at=clock_timestamp()
  RETURNING purge_generation INTO next_generation;
  SELECT array_agg(episode_id) INTO ids FROM stride_source_episode_heads WHERE tenant_id=NEW.tenant_id AND active;
  IF ids IS NOT NULL THEN PERFORM stride_tombstone_source_episode_heads(NEW.tenant_id,ids,'purge',next_generation,'canonical_purge'); END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER stride_source_episode_purge
AFTER INSERT ON purge_ledger FOR EACH ROW EXECUTE FUNCTION stride_source_episode_purge_trigger();

COMMIT;
