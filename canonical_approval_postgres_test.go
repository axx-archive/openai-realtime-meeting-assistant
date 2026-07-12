package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func approvedPostgresApproval(t *testing.T) (context.Context, *PostgresCanonicalStore, *PostgresApprovalRepository, ApprovalService, ApprovalBinding, *approvalTargetStub, string) {
	t.Helper()
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	repository := NewPostgresApprovalRepository(store.pool)
	service, binding, requester, eligibility, target, now := approvalTestFixture(t)
	service.Repo = repository
	owner := ACLPrincipal{TenantID: binding.Target.TenantID, ID: "owner", Kind: ACLPrincipalUser}
	setApprovalRole(eligibility, owner, ApprovalRoleOwner, true)
	created, err := service.Create(ctx, "", binding, requester, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.Endorse(ctx, created.ID, owner)
	if err != nil || approved.Status != ApprovalApproved {
		t.Fatalf("approve=%+v err=%v", approved, err)
	}
	return ctx, store, repository, service, binding, target, created.ID
}

func TestPostgresApprovalConcurrentConsumeCreatesOneDispatch(t *testing.T) {
	ctx, store, _, service, binding, _, approvalID := approvedPostgresApproval(t)
	type outcome struct {
		dispatch ApprovalDispatch
		fresh    bool
		err      error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 8)
	var wait sync.WaitGroup
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			dispatch, fresh, err := service.Consume(ctx, approvalID, binding)
			outcomes <- outcome{dispatch: dispatch, fresh: fresh, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(outcomes)
	freshCount := 0
	executionKey := ""
	for result := range outcomes {
		if result.err != nil {
			t.Fatalf("consume error: %v", result.err)
		}
		if result.fresh {
			freshCount++
		}
		if executionKey == "" {
			executionKey = result.dispatch.ExecutionKey
		}
		if result.dispatch.ExecutionKey != executionKey || result.dispatch.Status != DispatchPrepared {
			t.Fatalf("dispatch mismatch: %+v", result.dispatch)
		}
	}
	if freshCount != 1 {
		t.Fatalf("fresh dispatch count=%d, want 1", freshCount)
	}
	for table, check := range map[string]struct {
		query string
		args  []any
	}{
		"execution_receipts": {"SELECT count(*) FROM execution_receipts WHERE approval_id=$1", []any{approvalID}},
		"jobs":               {"SELECT count(*) FROM jobs WHERE encode(execution_key,'hex')=$1", []any{executionKey}},
		"outbox":             {"SELECT count(*) FROM outbox WHERE approval_id=$1", []any{approvalID}},
	} {
		var count int
		if err := store.pool.QueryRow(ctx, check.query, check.args...).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	var status string
	if err := store.pool.QueryRow(ctx, "SELECT status FROM approvals WHERE approval_id=$1", approvalID).Scan(&status); err != nil || status != string(ApprovalConsumed) {
		t.Fatalf("approval status=%q err=%v", status, err)
	}
}

func TestPostgresApprovalConsumeRejectsEditAndActionMismatch(t *testing.T) {
	ctx, store, _, service, binding, target, approvalID := approvedPostgresApproval(t)
	changed := binding
	changed.Plan.Command = "different external action"
	if _, _, err := service.Consume(ctx, approvalID, changed); !errors.Is(err, ErrApprovalBindingChanged) {
		t.Fatalf("action mismatch=%v", err)
	}
	target.object.CurrentContentRevision++
	target.object.CurrentContentDigest = strings.Repeat("d", 64)
	if _, _, err := service.Consume(ctx, approvalID, binding); !errors.Is(err, ErrApprovalBindingChanged) {
		t.Fatalf("edit mismatch=%v", err)
	}
	for _, query := range []string{
		"SELECT count(*) FROM execution_receipts WHERE approval_id=$1",
		"SELECT count(*) FROM outbox WHERE approval_id=$1",
	} {
		var count int
		if err := store.pool.QueryRow(ctx, query, approvalID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial dispatch count=%d err=%v", count, err)
		}
	}
}

func TestPostgresApprovalDispatchFailpointRollsBackAtomically(t *testing.T) {
	ctx, store, repository, service, binding, _, approvalID := approvedPostgresApproval(t)
	repository.Failpoint = func(point string) error {
		if point == "after_receipt_before_dispatch" {
			return errors.New("injected approval dispatch crash")
		}
		return nil
	}
	if _, _, err := service.Consume(ctx, approvalID, binding); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("consume failpoint=%v", err)
	}
	var status string
	if err := store.pool.QueryRow(ctx, "SELECT status FROM approvals WHERE approval_id=$1", approvalID).Scan(&status); err != nil || status != string(ApprovalApproved) {
		t.Fatalf("approval rollback status=%q err=%v", status, err)
	}
	for _, check := range []struct {
		query string
		args  []any
	}{
		{"SELECT count(*) FROM execution_receipts WHERE approval_id=$1", []any{approvalID}},
		{"SELECT count(*) FROM jobs WHERE encode(execution_key,'hex')=$1", []any{stableExecutionKey(approvalID, mustApprovalDigest(t, binding))}},
		{"SELECT count(*) FROM outbox WHERE approval_id=$1", []any{approvalID}},
	} {
		var count int
		if err := store.pool.QueryRow(ctx, check.query, check.args...).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial row count=%d err=%v", count, err)
		}
	}
	repository.Failpoint = nil
	if dispatch, fresh, err := service.Consume(ctx, approvalID, binding); err != nil || !fresh || dispatch.Status != DispatchPrepared {
		t.Fatalf("retry=%+v fresh=%v err=%v", dispatch, fresh, err)
	}
}

func TestPostgresApprovalAmbiguousNeverResends(t *testing.T) {
	ctx, store, _, service, binding, _, approvalID := approvedPostgresApproval(t)
	dispatch, fresh, err := service.Consume(ctx, approvalID, binding)
	if err != nil || !fresh {
		t.Fatalf("consume=%+v %v %v", dispatch, fresh, err)
	}
	if _, err := service.TransitionDispatch(ctx, approvalID, DispatchAmbiguous, "", "provider outcome unknown"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Consume(ctx, approvalID, binding); !errors.Is(err, ErrDispatchAmbiguous) {
		t.Fatalf("ambiguous retry=%v", err)
	}
	var jobs, outbox int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE encode(execution_key,'hex')=$1", dispatch.ExecutionKey).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE approval_id=$1", approvalID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || outbox != 1 {
		t.Fatalf("ambiguous resend rows jobs=%d outbox=%d", jobs, outbox)
	}
	var jobStatus string
	var delivered bool
	if err := store.pool.QueryRow(ctx, "SELECT status FROM jobs WHERE encode(execution_key,'hex')=$1", dispatch.ExecutionKey).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, "SELECT delivered_at IS NOT NULL FROM outbox WHERE approval_id=$1", approvalID).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "ambiguous" || !delivered {
		t.Fatalf("ambiguous dispatch remained sendable: job=%s delivered=%v", jobStatus, delivered)
	}
}

func mustApprovalDigest(t *testing.T, binding ApprovalBinding) string {
	t.Helper()
	digest, err := approvalBindingDigest(binding)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
