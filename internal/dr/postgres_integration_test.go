package dr

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestLogicalDatabaseDigestChangesForNonPurgeRowMutation(t *testing.T) {
	ctx, databaseURL := migratedDRPostgres(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `INSERT INTO principals(tenant_id,principal_type,principal_id,status,created_at) VALUES ('tenant-a','user','alice','active','2026-07-22T20:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	empty := sha256.Sum256(nil)
	authority := []PurgeState{{TenantID: "tenant-a", HighWater: 0, ManifestSHA256: fmt.Sprintf("%x", empty)}}
	before, err := QueryDatabaseState(ctx, databaseURL, authority)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := QueryDatabaseState(ctx, databaseURL, authority)
	if err != nil {
		t.Fatal(err)
	}
	if before.LogicalSHA256 != repeated.LogicalSHA256 {
		t.Fatal("unchanged canonical database did not produce a deterministic logical digest")
	}
	if _, err := conn.Exec(ctx, `UPDATE principals SET status='disabled' WHERE tenant_id='tenant-a' AND principal_id='alice'`); err != nil {
		t.Fatal(err)
	}
	after, err := QueryDatabaseState(ctx, databaseURL, authority)
	if err != nil {
		t.Fatal(err)
	}
	if before.LogicalSHA256 == after.LogicalSHA256 {
		t.Fatal("non-purge canonical row mutation did not change logical database digest")
	}
	if !equalJSON(before.Tenants, after.Tenants) || !equalJSON(before.AuthorityTenantPrefixes, after.AuthorityTenantPrefixes) {
		t.Fatal("test mutation unexpectedly changed purge state instead of only the complete logical digest")
	}
}

func migratedDRPostgres(t *testing.T) (context.Context, string) {
	t.Helper()
	initdb, initErr := exec.LookPath("initdb")
	postgres, postgresErr := exec.LookPath("postgres")
	pgCtl, ctlErr := exec.LookPath("pg_ctl")
	if initErr != nil || postgresErr != nil || ctlErr != nil {
		t.Skipf("disposable PostgreSQL unavailable: initdb=%v postgres=%v pg_ctl=%v", initErr, postgresErr, ctlErr)
	}
	_ = postgres
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	if output, err := exec.Command(initdb, "-D", dataDir, "-A", "trust", "-U", "postgres", "--no-locale", "--encoding=UTF8").CombinedOutput(); err != nil {
		t.Skipf("initdb failed: %v\n%s", err, output)
	}
	logPath := filepath.Join(filepath.Dir(dataDir), "postgres.log")
	options := fmt.Sprintf("-F -p %d -h 127.0.0.1", port)
	if output, err := exec.Command(pgCtl, "-D", dataDir, "-l", logPath, "-o", options, "-w", "start").CombinedOutput(); err != nil {
		t.Skipf("postgres start failed: %v\n%s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command(pgCtl, "-D", dataDir, "-m", "immediate", "-w", "stop").Run() })
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	databaseURL := fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", port)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	entries, err := os.ReadDir("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("../../migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("apply %s: %v", entry.Name(), err)
		}
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations(version,sha256) VALUES ($1,$2)`, version, digest[:]); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, databaseURL
}
