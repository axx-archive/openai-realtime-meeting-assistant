package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSTRIDESubjectAuthoredRelationshipControlIsSignedRevisionBoundAndRestartSafe(t *testing.T) {
	now := time.Date(2026, 8, 1, 22, 0, 0, 0, time.UTC)
	config := strideIntegratedRuntimeConfig(filepath.Join(t.TempDir(), "runtime"))
	config.ProductPreviewEnabled = true
	config.RelationshipMemoryEnabled = true
	receipt, err := mintSTRIDEProductActivationReceipt(config, 1, STRIDEProductScopeCoworker, now)
	if err != nil {
		t.Fatal(err)
	}
	actor := strideRuntimePrincipalForEmail("aj@shareability.com")
	privateAudience := STRIDEAudience{Visibility: "private", Principals: []string{actor}}
	evidence, err := mintSTRIDECollaborationControlEvidence(config, receipt, actor, "remember", "", "response_length", "Lead with the recommendation.", stridePreferencePrivate, privateAudience, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if !verifySTRIDECollaborationControlEvidence(config.Authority, evidence) || evidence.Reference().ContractType != STRIDEContractConversationEvent || !strings.HasPrefix(evidence.Reference().ID, "relationship_control_") {
		t.Fatalf("invalid signed control evidence: %+v", evidence)
	}
	tampered := evidence
	tampered.ValueDigest = temporalDigest("silently changed")
	if verifySTRIDECollaborationControlEvidence(config.Authority, tampered) {
		t.Fatal("tampered Settings control evidence verified")
	}
	orphaned := durableSTRIDECollaborationSubject{
		Consent: &STRIDECollaborationMemoryConsent{
			SubjectPrincipal: actor, Revision: 2, Enabled: true, UpdatedAt: now, UpdatedBy: actor,
		},
		Revision:        2,
		ControlEvidence: []STRIDECollaborationControlEvidence{evidence},
		Controls: []STRIDECollaborationControlReceipt{
			{Action: "enable", Actor: actor, ResultingRevision: 1, OccurredAt: now},
			{Action: "remember", Actor: actor, PreferenceType: "response_length", EvidenceID: evidence.Event.Header.ID, ResultingRevision: 2, OccurredAt: now},
		},
	}
	if err := validateDurableSTRIDECollaborationSubject(actor, orphaned, config.Authority); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
		t.Fatalf("orphaned signed control evidence error=%v", err)
	}

	path := filepath.Join(t.TempDir(), "collaboration.json")
	store, err := newDurableSTRIDECollaborationStore(path, true, config.Authority)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetConsent(actor, 0, true, false, false, now); err != nil {
		t.Fatal(err)
	}
	event := STRIDECollaborationPreferenceEvent{
		Action: stridePreferenceObserve, SubjectPrincipal: actor, Scope: stridePreferencePrivate, ScopeID: actor,
		PreferenceType: "response_length", Value: "Lead with the recommendation.", Origin: stridePreferenceExplicit,
		Evidence: []STRIDEReference{evidence.Reference()}, Confidence: 1, ObservedAt: now, ExpiresAt: now.Add(180 * 24 * time.Hour),
		Audience: privateAudience,
	}
	if err := store.RememberFromControl(actor, 1, event, evidence); err != nil {
		t.Fatal(err)
	}
	reopened, err := newDurableSTRIDECollaborationStore(path, true, config.Authority)
	if err != nil {
		t.Fatal(err)
	}
	preferences, revision, err := reopened.Inspect(actor, now.Add(time.Minute))
	if err != nil || revision != 2 || len(preferences) != 1 || preferences[0].Scope != stridePreferencePrivate || preferences[0].Evidence[0] != evidence.Reference() {
		t.Fatalf("restarted controls preferences=%+v revision=%d err=%v", preferences, revision, err)
	}
	if _, err := newDurableSTRIDECollaborationStore(path, true, STRIDESnapshotMACAuthority{KeyID: config.Authority.KeyID, Key: []byte(strings.Repeat("x", 32))}); err == nil {
		t.Fatal("relationship store accepted signed control evidence under the wrong authority")
	}
	if err := reopened.SetConsent(actor, 2, false, false, false, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("revoke signed Settings memory: %v", err)
	}
	revoked, err := newDurableSTRIDECollaborationStore(path, true, config.Authority)
	if err != nil {
		t.Fatal(err)
	}
	if preferences, revision, err := revoked.Inspect(actor, now.Add(3*time.Minute)); err != nil || revision != 3 || len(preferences) != 0 {
		t.Fatalf("revoked preferences=%+v revision=%d err=%v", preferences, revision, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Lead with the recommendation.", evidence.Event.Header.ID, evidence.Signature} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("revoked Settings value/evidence %q remained on disk: %s", forbidden, raw)
		}
	}
}
