package dr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// tenantBearingTables is the reviewed registry of every canonical table with
// a tenant_id column. QueryPurgeStates compares it with information_schema so a
// migration cannot add a tenant-only table without also updating this gate.
var tenantBearingTables = []string{
	"approvals", "brain_projection_backfill_requests", "brain_projection_checkpoints",
	"brain_projection_work", "canonical_events", "catch_up_publications", "consent_records",
	"jobs", "object_grants", "object_revisions", "objects", "org_memberships", "principals",
	"purge_ledger", "retention_state", "revision_bodies",
}

// canonicalTables is the exhaustive registry for the logical database digest.
// The migration drift test and live information_schema check both fail closed
// when a canonical table is added without joining this registry.
var canonicalTables = []string{
	"approval_endorsements", "approvals", "blobs", "brain_projection_backfill_requests",
	"brain_projection_checkpoints", "brain_projection_work", "canonical_events",
	"catch_up_publications", "consent_records", "execution_receipts", "jobs",
	"legacy_object_versions", "migration_epochs", "object_grants", "object_revisions",
	"objects", "org_memberships", "outbox", "principals", "projection_checkpoints",
	"purge_ledger", "retention_state", "revision_bodies", "schema_migrations",
}

type DatabaseState struct {
	Tenants                 []PurgeState
	AuthorityTenantPrefixes []PurgeState
	LogicalSHA256           string
}

func QueryDatabaseState(ctx context.Context, databaseURL string, authority []PurgeState) (DatabaseState, error) {
	return queryDatabaseState(ctx, databaseURL, authority, false)
}

func QueryPurgeStates(ctx context.Context, databaseURL string, authority []PurgeState) ([]PurgeState, []PurgeState, error) {
	state, err := queryDatabaseState(ctx, databaseURL, authority, false)
	return state.Tenants, state.AuthorityTenantPrefixes, err
}

// QueryPurgeStatesForAuthorityAdvance proves every tenant in the preceding
// head while permitting newly discovered tenants to be added to the next one.
// It never permits a preceding tenant to disappear or change its signed prefix.
func QueryPurgeStatesForAuthorityAdvance(ctx context.Context, databaseURL string, preceding []PurgeState) ([]PurgeState, error) {
	state, err := queryDatabaseState(ctx, databaseURL, preceding, true)
	if err != nil {
		return nil, err
	}
	if !tenantStatesExtendAuthority(state.Tenants, state.AuthorityTenantPrefixes, preceding) {
		return nil, errors.New("database purge state does not monotonically extend the preceding authority head")
	}
	return state.Tenants, nil
}

func queryDatabaseState(ctx context.Context, databaseURL string, authority []PurgeState, allowAdditional bool) (DatabaseState, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return DatabaseState{}, errors.New("database-url is required")
	}
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return DatabaseState{}, err
	}
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return DatabaseState{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL TIME ZONE 'UTC'`); err != nil {
		return DatabaseState{}, err
	}
	if err := verifyCanonicalTableRegistry(ctx, tx); err != nil {
		return DatabaseState{}, err
	}
	if err := verifyTenantTableRegistry(ctx, tx); err != nil {
		return DatabaseState{}, err
	}
	logicalDigest, err := queryLogicalDatabaseDigest(ctx, tx)
	if err != nil {
		return DatabaseState{}, err
	}
	parts := make([]string, len(tenantBearingTables))
	for index, table := range tenantBearingTables {
		parts[index] = `SELECT tenant_id FROM "` + table + `"`
	}
	tenantRows, err := tx.Query(ctx, `SELECT tenant_id FROM (`+strings.Join(parts, ` UNION `)+`) tenants ORDER BY tenant_id`)
	if err != nil {
		return DatabaseState{}, err
	}
	tenants := []string{}
	for tenantRows.Next() {
		var tenant string
		if err := tenantRows.Scan(&tenant); err != nil {
			tenantRows.Close()
			return DatabaseState{}, err
		}
		tenants = append(tenants, tenant)
	}
	tenantRows.Close()
	if err := tenantRows.Err(); err != nil {
		return DatabaseState{}, err
	}
	headByTenant := map[string]PurgeState{}
	for _, state := range authority {
		headByTenant[state.TenantID] = state
	}
	states := make([]PurgeState, 0, len(tenants))
	prefixes := make([]PurgeState, 0, len(tenants))
	for _, tenant := range tenants {
		rows, err := tx.Query(ctx, `SELECT object_type,object_id,revision_id,encode(content_sha256,'hex'),policy_id,purged_at,recorded_at,destruction_evidence::text FROM purge_ledger WHERE tenant_id=$1 ORDER BY recorded_at,object_type,object_id,revision_id`, tenant)
		if err != nil {
			return DatabaseState{}, err
		}
		hash := sha256.New()
		var count int64
		head := headByTenant[tenant]
		prefixDigest := ""
		if head.HighWater == 0 {
			prefixDigest = hex.EncodeToString(hash.Sum(nil))
		}
		for rows.Next() {
			var objectType, objectID, revisionID, contentDigest, policyID, evidence string
			var purgedAt, recordedAt time.Time
			if err := rows.Scan(&objectType, &objectID, &revisionID, &contentDigest, &policyID, &purgedAt, &recordedAt, &evidence); err != nil {
				rows.Close()
				return DatabaseState{}, err
			}
			line, _ := json.Marshal([]any{tenant, objectType, objectID, revisionID, contentDigest, policyID, purgedAt.UTC().Format(time.RFC3339Nano), recordedAt.UTC().Format(time.RFC3339Nano), json.RawMessage(evidence)})
			hash.Write(line)
			hash.Write([]byte{'\n'})
			count++
			if count == head.HighWater {
				prefixDigest = hex.EncodeToString(hash.Sum(nil))
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return DatabaseState{}, err
		}
		states = append(states, PurgeState{TenantID: tenant, HighWater: count, ManifestSHA256: hex.EncodeToString(hash.Sum(nil))})
		if authority != nil {
			if _, ok := headByTenant[tenant]; !ok {
				if allowAdditional {
					continue
				}
				return DatabaseState{}, fmt.Errorf("tenant %s is absent from the current authority head", tenant)
			}
			if prefixDigest == "" {
				return DatabaseState{}, fmt.Errorf("authority tenant %s cannot be proved at its high-water", tenant)
			}
			prefixes = append(prefixes, PurgeState{TenantID: tenant, HighWater: head.HighWater, ManifestSHA256: prefixDigest})
		}
	}
	if len(states) == 0 || (authority != nil && ((!allowAdditional && len(authority) != len(states)) || len(prefixes) != len(authority))) {
		return DatabaseState{}, errors.New("database tenant coverage does not match authority")
	}
	if err := tx.Commit(ctx); err != nil {
		return DatabaseState{}, err
	}
	return DatabaseState{Tenants: states, AuthorityTenantPrefixes: prefixes, LogicalSHA256: logicalDigest}, nil
}

func verifyCanonicalTableRegistry(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE' ORDER BY table_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	actual := []string{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return err
		}
		actual = append(actual, table)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	expected := append([]string(nil), canonicalTables...)
	sort.Strings(expected)
	if !equalJSON(actual, expected) {
		return fmt.Errorf("canonical table registry mismatch: database=%v code=%v", actual, expected)
	}
	return nil
}

func queryLogicalDatabaseDigest(ctx context.Context, tx pgx.Tx) (string, error) {
	hash := sha256.New()
	hash.Write([]byte("bonfire-canonical-logical-v1\n"))
	var schema, serverVersion, serverEncoding, collation, characterType string
	if err := tx.QueryRow(ctx, `SELECT current_schema(),current_setting('server_version_num'),pg_encoding_to_char(encoding),datcollate,datctype FROM pg_database WHERE datname=current_database()`).Scan(&schema, &serverVersion, &serverEncoding, &collation, &characterType); err != nil {
		return "", err
	}
	writeLogicalRecord(hash, []any{"database", schema, serverVersion, serverEncoding, collation, characterType})
	tables := append([]string(nil), canonicalTables...)
	sort.Strings(tables)
	columns, err := tx.Query(ctx, `SELECT table_name,ordinal_position,column_name,data_type,udt_schema,udt_name,is_nullable,COALESCE(column_default,''),COALESCE(collation_name,''),is_identity,COALESCE(identity_generation,''),is_generated,COALESCE(generation_expression,'') FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=ANY($1) ORDER BY table_name,ordinal_position`, tables)
	if err != nil {
		return "", err
	}
	for columns.Next() {
		var table, column, dataType, udtSchema, udtName, nullable, defaultValue, collation, identity, identityGeneration, generated, generationExpression string
		var ordinal int
		if err := columns.Scan(&table, &ordinal, &column, &dataType, &udtSchema, &udtName, &nullable, &defaultValue, &collation, &identity, &identityGeneration, &generated, &generationExpression); err != nil {
			columns.Close()
			return "", err
		}
		writeLogicalRecord(hash, []any{"column", table, ordinal, column, dataType, udtSchema, udtName, nullable, defaultValue, collation, identity, identityGeneration, generated, generationExpression})
	}
	columns.Close()
	if err := columns.Err(); err != nil {
		return "", err
	}
	constraints, err := tx.Query(ctx, `SELECT c.relname,con.conname,con.contype::text,pg_get_constraintdef(con.oid,true) FROM pg_constraint con JOIN pg_class c ON c.oid=con.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname=ANY($1) ORDER BY c.relname,con.conname`, tables)
	if err != nil {
		return "", err
	}
	for constraints.Next() {
		var table, name, kind, definition string
		if err := constraints.Scan(&table, &name, &kind, &definition); err != nil {
			constraints.Close()
			return "", err
		}
		writeLogicalRecord(hash, []any{"constraint", table, name, kind, definition})
	}
	constraints.Close()
	if err := constraints.Err(); err != nil {
		return "", err
	}
	indexes, err := tx.Query(ctx, `SELECT t.relname,i.relname,pg_get_indexdef(i.oid) FROM pg_index x JOIN pg_class t ON t.oid=x.indrelid JOIN pg_class i ON i.oid=x.indexrelid JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname=current_schema() AND t.relname=ANY($1) ORDER BY t.relname,i.relname`, tables)
	if err != nil {
		return "", err
	}
	for indexes.Next() {
		var table, name, definition string
		if err := indexes.Scan(&table, &name, &definition); err != nil {
			indexes.Close()
			return "", err
		}
		writeLogicalRecord(hash, []any{"index", table, name, definition})
	}
	indexes.Close()
	if err := indexes.Err(); err != nil {
		return "", err
	}
	triggers, err := tx.Query(ctx, `SELECT c.relname,t.tgname,pg_get_triggerdef(t.oid,true) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE NOT t.tgisinternal AND n.nspname=current_schema() AND c.relname=ANY($1) ORDER BY c.relname,t.tgname`, tables)
	if err != nil {
		return "", err
	}
	for triggers.Next() {
		var table, name, definition string
		if err := triggers.Scan(&table, &name, &definition); err != nil {
			triggers.Close()
			return "", err
		}
		writeLogicalRecord(hash, []any{"trigger", table, name, definition})
	}
	triggers.Close()
	if err := triggers.Err(); err != nil {
		return "", err
	}
	for _, table := range tables {
		writeLogicalRecord(hash, []any{"table", table})
		rows, err := tx.Query(ctx, `SELECT to_jsonb(t)::text FROM "`+table+`" t ORDER BY to_jsonb(t)::text COLLATE "C"`)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				rows.Close()
				return "", err
			}
			if !json.Valid([]byte(row)) {
				rows.Close()
				return "", errors.New("database row did not encode as canonical JSON")
			}
			writeLogicalRecord(hash, []any{"row", table, json.RawMessage(row)})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeLogicalRecord(hash interface{ Write([]byte) (int, error) }, value any) {
	raw, _ := json.Marshal(value)
	hash.Write(raw)
	hash.Write([]byte{'\n'})
}

func verifyTenantTableRegistry(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT table_name FROM information_schema.columns WHERE table_schema=current_schema() AND column_name='tenant_id' ORDER BY table_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	actual := []string{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return err
		}
		actual = append(actual, table)
	}
	expected := append([]string(nil), tenantBearingTables...)
	sort.Strings(expected)
	if !equalJSON(actual, expected) {
		return fmt.Errorf("tenant-bearing table registry mismatch: database=%v code=%v", actual, expected)
	}
	return rows.Err()
}
