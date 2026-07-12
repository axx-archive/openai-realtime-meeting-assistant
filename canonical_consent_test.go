package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func consentFixture(id string, disposition ConsentDisposition, scopes ...ConsentScope) ConsentRecord {
	return ConsentRecord{
		ID: consentTestID(id), TenantID: "tenant-1", PrincipalKind: ACLPrincipalUser, PrincipalID: "user-1",
		RoomID: "room-1", SittingID: "sitting-1", PolicyVersion: "policy-v1", Scopes: scopes,
		Disposition: disposition, EvidenceKind: "explicit_ui", EvidenceRef: "evidence-" + id,
		RecordedAt: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC),
	}
}

func consentTestID(label string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("bonfire.test/consent/"+label)).String()
}

func TestConsentFailsClosedForAbsentDeniedWithdrawnAndLateJoin(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryConsentStore()
	query := ConsentQuery{TenantID: "tenant-1", PrincipalKind: ACLPrincipalUser, PrincipalID: "user-1", RoomID: "room-1", SittingID: "sitting-1", PolicyVersion: "policy-v1", Scopes: []ConsentScope{ConsentAudioCapture}}
	decision, err := store.Effective(ctx, query)
	if err != nil || decision.Allowed {
		t.Fatalf("absent decision = %+v, err=%v", decision, err)
	}
	if _, err := store.Append(ctx, consentFixture("deny", ConsentDenied, ConsentAudioCapture)); err != nil {
		t.Fatal(err)
	}
	decision, _ = store.Effective(ctx, query)
	if decision.Allowed {
		t.Fatal("denied consent allowed capture")
	}
	grant := consentFixture("grant", ConsentGranted, ConsentAudioCapture)
	grant.RecordedAt = grant.RecordedAt.Add(time.Minute)
	if _, err := store.Append(ctx, grant); err != nil {
		t.Fatal(err)
	}
	decision, _ = store.Effective(ctx, query)
	if !decision.Allowed {
		t.Fatalf("explicit grant denied: %+v", decision)
	}
	withdraw := consentFixture("withdraw", ConsentWithdrawn, ConsentAudioCapture)
	withdraw.RecordedAt = withdraw.RecordedAt.Add(2 * time.Minute)
	if _, err := store.Append(ctx, withdraw); err != nil {
		t.Fatal(err)
	}
	decision, _ = store.Effective(ctx, query)
	if decision.Allowed {
		t.Fatal("withdrawal was not immediate")
	}
	lateJoin := query
	lateJoin.PrincipalID = "late-user"
	decision, _ = store.Effective(ctx, lateJoin)
	if decision.Allowed {
		t.Fatal("late join inherited another participant's consent")
	}
}

func TestConsentIsBoundToTenantPrincipalRoomSittingAndPolicy(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryConsentStore()
	if _, err := store.Append(ctx, consentFixture("grant", ConsentGranted, ConsentAudioCapture, ConsentTranscription)); err != nil {
		t.Fatal(err)
	}
	base := ConsentQuery{TenantID: "tenant-1", PrincipalKind: ACLPrincipalUser, PrincipalID: "user-1", RoomID: "room-1", SittingID: "sitting-1", PolicyVersion: "policy-v1", Scopes: []ConsentScope{ConsentAudioCapture, ConsentTranscription}}
	if decision, _ := store.Effective(ctx, base); !decision.Allowed || len(decision.RecordIDs) != 2 {
		t.Fatalf("exact binding denied: %+v", decision)
	}
	mutations := []func(*ConsentQuery){
		func(q *ConsentQuery) { q.TenantID = "tenant-2" }, func(q *ConsentQuery) { q.PrincipalID = "user-2" },
		func(q *ConsentQuery) { q.RoomID = "room-2" }, func(q *ConsentQuery) { q.SittingID = "sitting-2" },
		func(q *ConsentQuery) { q.PolicyVersion = "policy-v2" },
	}
	for index, mutate := range mutations {
		query := base
		mutate(&query)
		if decision, _ := store.Effective(ctx, query); decision.Allowed {
			t.Fatalf("mutation %d inherited consent", index)
		}
	}
}

func TestConsentScopesFoldIndependentlyAndWithdrawalDoesNotEraseEvidence(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryConsentStore()
	if _, err := store.Append(ctx, consentFixture("all", ConsentGranted, ConsentAudioCapture, ConsentTranscription, ConsentModelAnalysis, ConsentOrgMemory)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, consentFixture("withdraw-model", ConsentWithdrawn, ConsentModelAnalysis)); err != nil {
		t.Fatal(err)
	}
	query := ConsentQuery{TenantID: "tenant-1", PrincipalKind: ACLPrincipalUser, PrincipalID: "user-1", RoomID: "room-1", SittingID: "sitting-1", PolicyVersion: "policy-v1", Scopes: []ConsentScope{ConsentAudioCapture, ConsentModelAnalysis}}
	decision, err := store.Effective(ctx, query)
	if err != nil || decision.Allowed || len(decision.MissingScopes) != 1 || decision.MissingScopes[0] != ConsentModelAnalysis {
		t.Fatalf("decision = %+v, err=%v", decision, err)
	}
	if decision.RecordIDs[ConsentAudioCapture] != consentTestID("all") {
		t.Fatalf("unaffected scope evidence lost: %+v", decision.RecordIDs)
	}
}

func TestConsentAppendIsIdempotentAndConflictsFail(t *testing.T) {
	store := NewMemoryConsentStore()
	record := consentFixture("same", ConsentGranted, ConsentAudioCapture)
	if existing, err := store.Append(context.Background(), record); err != nil || existing {
		t.Fatalf("first append existing=%v err=%v", existing, err)
	}
	if existing, err := store.Append(context.Background(), record); err != nil || !existing {
		t.Fatalf("retry existing=%v err=%v", existing, err)
	}
	record.Disposition = ConsentWithdrawn
	if _, err := store.Append(context.Background(), record); !errors.Is(err, ErrConsentConflict) {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestConsentOutOfOrderOldGrantCannotReverseNewWithdrawal(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryConsentStore()
	withdraw := consentFixture("withdraw-new", ConsentWithdrawn, ConsentAudioCapture)
	withdraw.RecordedAt = withdraw.RecordedAt.Add(time.Hour)
	if _, err := store.Append(ctx, withdraw); err != nil {
		t.Fatal(err)
	}
	// Delayed import arrives later in append order but is older evidence.
	if _, err := store.Append(ctx, consentFixture("grant-old", ConsentGranted, ConsentAudioCapture)); err != nil {
		t.Fatal(err)
	}
	query := ConsentQuery{TenantID: "tenant-1", PrincipalKind: ACLPrincipalUser, PrincipalID: "user-1", RoomID: "room-1", SittingID: "sitting-1", PolicyVersion: "policy-v1", Scopes: []ConsentScope{ConsentAudioCapture}}
	decision, err := store.Effective(ctx, query)
	if err != nil || decision.Allowed || decision.RecordIDs[ConsentAudioCapture] != consentTestID("withdraw-new") {
		t.Fatalf("decision = %+v, err=%v", decision, err)
	}
}

func TestCanonicalConsentCheckerAdaptsExactObjectSitting(t *testing.T) {
	store := NewMemoryConsentStore()
	if _, err := store.Append(context.Background(), consentFixture("grant", ConsentGranted, ConsentTranscription)); err != nil {
		t.Fatal(err)
	}
	checker := CanonicalConsentChecker{Store: store, PolicyVersion: "policy-v1"}
	principal := ACLPrincipal{TenantID: "tenant-1", ID: "user-1", Kind: ACLPrincipalUser}
	object := ACLObject{RoomID: "room-1", SittingID: "sitting-1"}
	if ok, err := checker.HasConsent(context.Background(), principal, object, string(ConsentTranscription)); err != nil || !ok {
		t.Fatalf("checker = %v, %v", ok, err)
	}
	object.SittingID = "sitting-later"
	if ok, err := checker.HasConsent(context.Background(), principal, object, string(ConsentTranscription)); err != nil || ok {
		t.Fatalf("later sitting checker = %v, %v", ok, err)
	}
}

func TestConsentValidationRejectsUnknownScopeAndMissingEvidence(t *testing.T) {
	record := consentFixture("bad", ConsentGranted, ConsentScope("camera_capture"))
	if err := record.Validate(); !errors.Is(err, ErrConsentInvalid) {
		t.Fatalf("scope error = %v", err)
	}
	record = consentFixture("bad-evidence", ConsentGranted, ConsentAudioCapture)
	record.EvidenceRef = ""
	if err := record.Validate(); !errors.Is(err, ErrConsentInvalid) {
		t.Fatalf("evidence error = %v", err)
	}
}

func TestConsentAppendNormalizesAlternateUUIDSpelling(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryConsentStore()
	record := consentFixture("alternate-id", ConsentGranted, ConsentAudioCapture)
	canonicalID := record.ID
	record.ID = "{" + canonicalID + "}"
	if _, err := store.Append(ctx, record); err != nil {
		t.Fatalf("append alternate UUID: %v", err)
	}
	query := ConsentQuery{TenantID: "tenant-1", PrincipalKind: ACLPrincipalUser, PrincipalID: "user-1", RoomID: "room-1", SittingID: "sitting-1", PolicyVersion: "policy-v1", Scopes: []ConsentScope{ConsentAudioCapture}}
	decision, err := store.Effective(ctx, query)
	if err != nil || decision.RecordIDs[ConsentAudioCapture] != canonicalID {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}
