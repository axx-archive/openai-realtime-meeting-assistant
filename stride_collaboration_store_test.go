package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func collaborationStoreEvent(subject, scope, preferenceType, value string, audience STRIDEAudience, evidence STRIDEReference, at time.Time) STRIDECollaborationPreferenceEvent {
	return STRIDECollaborationPreferenceEvent{
		Action: stridePreferenceObserve, SubjectPrincipal: subject, Scope: scope, ScopeID: subject,
		PreferenceType: preferenceType, Value: value, Origin: stridePreferenceExplicit,
		Evidence: []STRIDEReference{evidence}, Confidence: 1, ObservedAt: at.UTC(), ExpiresAt: at.UTC().Add(180 * 24 * time.Hour), Audience: audience,
	}
}

func TestSTRIDEImportedRelationshipPreferenceTypesUseClosedCorrigibleSlots(t *testing.T) {
	for _, preferenceType := range []string{
		"user_instruction_01",
		"identity_context_09",
		"career_context_24",
		"project_context_75",
		"personal_preference_99",
	} {
		if !safeSTRIDECollaborationPreferenceType(preferenceType) {
			t.Fatalf("reviewed import slot %q was rejected", preferenceType)
		}
	}
	for _, preferenceType := range []string{
		"user_instruction_00",
		"identity_context_1",
		"project_context_100",
		"medical_context_01",
		"personal_preference_ignore_policy",
	} {
		if safeSTRIDECollaborationPreferenceType(preferenceType) {
			t.Fatalf("unbounded or sensitive import type %q was accepted", preferenceType)
		}
	}
}

func TestDurableSTRIDECollaborationStoreDefaultsOffAndRequiresSubjectConsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collaboration.json")
	subject := "user:0123456789abcdef01234567"
	now := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	disabled, err := newDurableSTRIDECollaborationStore(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := disabled.SetConsent(subject, 0, true, true, true, now); !errors.Is(err, ErrSTRIDECollaborationStoreDisabled) {
		t.Fatalf("disabled consent error=%v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default-off store wrote state: %v", err)
	}

	store, err := newDurableSTRIDECollaborationStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	event := collaborationStoreEvent(subject, stridePreferencePrivate, "response_length", "brief", STRIDEAudience{Visibility: "private", Principals: []string{subject}}, stridePreferenceEvidence("store-consent-evidence"), now)
	if err := store.Remember(subject, 0, event); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
		t.Fatalf("remember without consent error=%v", err)
	}
	if err := store.SetConsent(subject, 0, true, false, false, now); err != nil {
		t.Fatal(err)
	}
	inferred := event
	inferred.Origin = stridePreferenceInferred
	inferred.Confidence = .75
	if err := store.Remember(subject, 1, inferred); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
		t.Fatalf("inferred memory without inferred consent error=%v", err)
	}
	shared := event
	shared.Scope = stridePreferenceShared
	shared.Audience = STRIDEAudience{Visibility: "channel", Principals: []string{subject, "user:peer"}}
	if err := store.Remember(subject, 1, shared); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
		t.Fatalf("shared memory without shared consent error=%v", err)
	}
	if records, revision, err := store.Inspect("user:different", now.Add(time.Minute)); err != nil || revision != 0 || len(records) != 0 {
		t.Fatalf("cross-subject inspect records=%+v revision=%d err=%v", records, revision, err)
	}
}

func TestDurableSTRIDECollaborationStorePersistsCorrectsForgetsAndRevokes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collaboration.json")
	subject := "user:0123456789abcdef01234567"
	peer := "user:76543210fedcba9876543210"
	t0 := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	store, err := newDurableSTRIDECollaborationStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetConsent(subject, 0, true, false, true, t0); err != nil {
		t.Fatal(err)
	}
	privateEvidence := stridePreferenceEvidence("private-source")
	private := collaborationStoreEvent(subject, stridePreferencePrivate, "feedback_style", "brutally terse", STRIDEAudience{Visibility: "private", Principals: []string{subject}}, privateEvidence, t0.Add(time.Minute))
	if err := store.Remember(subject, 1, private); err != nil {
		t.Fatal(err)
	}
	inspected, revision, err := store.Inspect(subject, t0.Add(2*time.Minute))
	if err != nil || revision != 2 || len(inspected) != 1 || inspected[0].Value != "brutally terse" || inspected[0].SourceEventID == "" {
		t.Fatalf("initial inspect=%+v revision=%d err=%v", inspected, revision, err)
	}
	privateID := inspected[0].Reference.ID
	privateRef := inspected[0].Reference

	restarted, err := newDurableSTRIDECollaborationStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded, gotRevision, reloadErr := restarted.Inspect(subject, t0.Add(3*time.Minute)); reloadErr != nil || gotRevision != 2 || len(reloaded) != 1 || reloaded[0].Reference != privateRef {
		t.Fatalf("restart projection=%+v revision=%d err=%v", reloaded, gotRevision, reloadErr)
	}
	correctionEvidence := stridePreferenceEvidence("correction-source")
	if err := restarted.Correct(subject, privateID, 2, "direct, with the reason", []STRIDEReference{correctionEvidence}, t0.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Correct(subject, privateID, 2, "stale overwrite", []STRIDEReference{correctionEvidence}, t0.Add(5*time.Minute)); !errors.Is(err, ErrSTRIDECollaborationStoreConflict) {
		t.Fatalf("stale correction error=%v", err)
	}
	corrected, revision, err := restarted.Inspect(subject, t0.Add(5*time.Minute))
	if err != nil || revision != 3 || len(corrected) != 1 || corrected[0].Value != "direct, with the reason" || corrected[0].Reference == privateRef || corrected[0].Origin != stridePreferenceExplicit {
		t.Fatalf("corrected=%+v revision=%d err=%v", corrected, revision, err)
	}
	if public, _, err := restarted.ProjectForContext(subject, STRIDEAudience{Visibility: "organization", Principals: []string{subject, peer}}, "channel-other", t0.Add(5*time.Minute)); err != nil || len(public) != 0 {
		t.Fatalf("private memory escaped into public context: %+v err=%v", public, err)
	}

	shared := collaborationStoreEvent(subject, stridePreferenceShared, "response_length", "one paragraph unless I ask", STRIDEAudience{Visibility: "organization", Principals: []string{subject, peer}}, stridePreferenceEvidence("shared-source"), t0.Add(6*time.Minute))
	shared.ScopeID = "channel-team"
	if err := restarted.Remember(subject, 3, shared); err != nil {
		t.Fatal(err)
	}
	sharedContext, revision, err := restarted.ProjectForContext(subject, shared.Audience, shared.ScopeID, t0.Add(7*time.Minute))
	if err != nil || revision != 4 || len(sharedContext) != 1 || sharedContext[0].Value != shared.Value || sharedContext[0].Evidence[0].ID != "shared-source" {
		t.Fatalf("shared context=%+v revision=%d err=%v", sharedContext, revision, err)
	}
	if otherChannel, _, err := restarted.ProjectForContext(subject, shared.Audience, "channel-other", t0.Add(7*time.Minute)); err != nil || len(otherChannel) != 0 {
		t.Fatalf("shared preference crossed channel scope: %+v err=%v", otherChannel, err)
	}
	if narrowed, _, err := restarted.ProjectForContext(subject, STRIDEAudience{Visibility: "organization", Principals: []string{subject}}, shared.ScopeID, t0.Add(7*time.Minute)); err != nil || len(narrowed) != 0 {
		t.Fatalf("shared ACL narrowing leaked memory: %+v err=%v", narrowed, err)
	}

	if err := restarted.Forget(subject, privateID, 4, t0.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	afterForget, revision, err := restarted.Inspect(subject, t0.Add(9*time.Minute))
	if err != nil || revision != 5 || len(afterForget) != 1 || afterForget[0].PreferenceType != "response_length" {
		t.Fatalf("after forget=%+v revision=%d err=%v", afterForget, revision, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"brutally terse", "direct, with the reason", "private-source", "correction-source"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("forgotten value/provenance %q remained in durable state: %s", forbidden, raw)
		}
	}
	if restarted.AuthorizeContextReference(sharedContext[0].Reference, subject, shared.Audience, shared.ScopeID, t0.Add(9*time.Minute)) {
		t.Fatal("pre-forget reference stayed authorized after the subject revision advanced")
	}

	if err := restarted.SetConsent(subject, 5, false, false, false, t0.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), shared.Value) || strings.Contains(string(raw), "shared-source") {
		t.Fatalf("revoked value/provenance remained in durable state: %s", raw)
	}
	revoked, revision, err := restarted.ProjectForContext(subject, shared.Audience, shared.ScopeID, t0.Add(11*time.Minute))
	if err != nil || revision != 6 || len(revoked) != 0 {
		t.Fatalf("revoked context=%+v revision=%d err=%v", revoked, revision, err)
	}
	reopened, err := newDurableSTRIDECollaborationStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	consent, revision, err := reopened.Consent(subject)
	if err != nil || revision != 6 || consent.Enabled {
		t.Fatalf("reopened consent=%+v revision=%d err=%v", consent, revision, err)
	}
}

func TestDurableSTRIDECollaborationInspectShowsStoredMemoryWhenUseConsentIsPaused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collaboration.json")
	subject := "user:0123456789abcdef01234567"
	peer := "user:76543210fedcba9876543210"
	now := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	store, err := newDurableSTRIDECollaborationStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetConsent(subject, 0, true, false, true, now); err != nil {
		t.Fatal(err)
	}
	audience := STRIDEAudience{Visibility: "organization", Principals: []string{subject, peer}}
	event := collaborationStoreEvent(subject, stridePreferenceShared, "response_length", "one paragraph", audience, stridePreferenceEvidence("paused-shared-source"), now.Add(time.Minute))
	event.ScopeID = "channel-team"
	if err := store.Remember(subject, 1, event); err != nil {
		t.Fatal(err)
	}
	if err := store.SetConsent(subject, 2, true, false, false, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	inspected, revision, err := store.Inspect(subject, now.Add(3*time.Minute))
	if err != nil || revision != 3 || len(inspected) != 1 || inspected[0].Scope != stridePreferenceShared {
		t.Fatalf("inspect should expose stored paused memory: %+v revision=%d err=%v", inspected, revision, err)
	}
	projected, _, err := store.ProjectForContext(subject, audience, event.ScopeID, now.Add(3*time.Minute))
	if err != nil || len(projected) != 0 {
		t.Fatalf("paused shared consent projected memory: %+v err=%v", projected, err)
	}
}

func TestDurableSTRIDECollaborationAmbiguousPersistPoisonsQueuedMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collaboration.json")
	subject := "user:0123456789abcdef01234567"
	now := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	store, err := newDurableSTRIDECollaborationStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetConsent(subject, 0, true, false, false, now); err != nil {
		t.Fatal(err)
	}

	var persistCalls atomic.Int32
	firstPersistEntered := make(chan struct{})
	releaseFirstPersist := make(chan struct{})
	store.write = func(path string, raw []byte) error {
		call := persistCalls.Add(1)
		if call == 1 {
			// Model rename-published/parent-fsync-ambiguous semantics: revision 2
			// may already be the visible durable generation when the error lands.
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				return err
			}
			close(firstPersistEntered)
			<-releaseFirstPersist
			return fmt.Errorf("%w: injected parent fsync failure", ErrDurableReplaceAmbiguous)
		}
		return os.WriteFile(path, raw, 0o600)
	}

	event := collaborationStoreEvent(subject, stridePreferencePrivate, "response_length", "lead with the answer", STRIDEAudience{Visibility: "private", Principals: []string{subject}}, stridePreferenceEvidence("queued-poison-source"), now.Add(time.Minute))
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- store.Remember(subject, 1, event)
	}()
	<-firstPersistEntered

	secondDone := make(chan error, 1)
	go func() {
		// Revision 2 is intentional: before the fix this caller could pass the
		// unlocked enabled precheck, queue on mu, then persist revision 3 after
		// the first caller poisoned the store.
		secondDone <- store.SetConsent(subject, 2, true, false, false, now.Add(2*time.Minute))
	}()
	waitForSTRIDECollaborationMutatorQueue(t)
	close(releaseFirstPersist)

	if err := <-firstDone; !errors.Is(err, ErrDurableReplaceAmbiguous) {
		t.Fatalf("first mutation error=%v, want ambiguous durable replace", err)
	}
	if err := <-secondDone; !errors.Is(err, ErrSTRIDECollaborationStoreDisabled) {
		t.Fatalf("queued mutation error=%v, want poisoned store disabled", err)
	}
	if calls := persistCalls.Load(); calls != 1 {
		t.Fatalf("persist calls=%d, want no post-poison persist", calls)
	}

	store.mu.Lock()
	poisoned := !store.enabled
	revision := store.subjects[subject].Revision
	store.mu.Unlock()
	if !poisoned || revision != 2 {
		t.Fatalf("poisoned=%t revision=%d, want poisoned revision 2", poisoned, revision)
	}
	if _, _, err := store.Consent(subject); !errors.Is(err, ErrSTRIDECollaborationStoreDisabled) {
		t.Fatalf("post-poison consent read error=%v, want store disabled", err)
	}
	if _, _, err := store.Inspect(subject, now.Add(3*time.Minute)); !errors.Is(err, ErrSTRIDECollaborationStoreDisabled) {
		t.Fatalf("post-poison inspect error=%v, want store disabled", err)
	}
	if _, _, err := store.ProjectForContext(subject, event.Audience, subject, now.Add(3*time.Minute)); !errors.Is(err, ErrSTRIDECollaborationStoreDisabled) {
		t.Fatalf("post-poison context projection error=%v, want store disabled", err)
	}

	reopened, err := newDurableSTRIDECollaborationStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	preferences, durableRevision, err := reopened.Inspect(subject, now.Add(3*time.Minute))
	if err != nil || durableRevision != 2 || len(preferences) != 1 || preferences[0].Value != event.Value {
		t.Fatalf("reopened preferences=%+v revision=%d err=%v", preferences, durableRevision, err)
	}
}

func waitForSTRIDECollaborationMutatorQueue(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	stack := make([]byte, 1<<20)
	for time.Now().Before(deadline) {
		n := runtime.Stack(stack, true)
		for _, goroutine := range strings.Split(string(stack[:n]), "\n\n") {
			if strings.Contains(goroutine, "mutateSubject") && strings.Contains(goroutine, "sync.(*Mutex).Lock") {
				return
			}
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("queued collaboration mutator never blocked on the store mutex")
}
