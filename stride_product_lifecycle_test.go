package main

import (
	"strings"
	"testing"
	"time"
)

func TestSTRIDEProductActivationReceiptRoundTripTamperExpiryAndScope(t *testing.T) {
	now := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
	config := STRIDERuntimeConfig{
		Enabled:               true,
		TenantID:              "bonfire",
		ProductPreviewEnabled: true,
		Authority: STRIDESnapshotMACAuthority{
			KeyID: "product_test_key",
			Key:   []byte("0123456789abcdef0123456789abcdef"),
		},
	}
	for _, scope := range []string{STRIDEProductScopeWork, STRIDEProductScopeMarketplace, STRIDEProductScopeTemporal, STRIDEProductScopeCoworker} {
		receipt, err := mintSTRIDEProductActivationReceipt(config, 9, scope, now)
		if err != nil {
			t.Fatalf("mint %s: %v", scope, err)
		}
		if len(receipt.Nonce) != 32 || !isSTRIDEProductNonce(receipt.Nonce) {
			t.Fatalf("%s nonce=%q, want exactly 16 bytes of hex", scope, receipt.Nonce)
		}
		if !verifySTRIDEProductActivationReceipt(config, receipt, scope, now) {
			t.Fatalf("%s receipt did not round trip", scope)
		}
		for _, other := range []string{STRIDEProductScopeWork, STRIDEProductScopeMarketplace, STRIDEProductScopeTemporal, STRIDEProductScopeCoworker} {
			if other != scope && verifySTRIDEProductActivationReceipt(config, receipt, other, now) {
				t.Fatalf("%s receipt crossed into %s", scope, other)
			}
		}
		if verifySTRIDEProductActivationReceipt(config, receipt, scope, receipt.ExpiresAt) {
			t.Fatalf("%s receipt accepted at expiry", scope)
		}
		tampered := receipt
		tampered.Nonce = strings.Repeat("0", len(tampered.Nonce))
		if verifySTRIDEProductActivationReceipt(config, tampered, scope, now) {
			t.Fatalf("%s accepted tampered nonce", scope)
		}
		tampered = receipt
		tampered.Signature = strings.Repeat("0", len(tampered.Signature))
		if verifySTRIDEProductActivationReceipt(config, tampered, scope, now) {
			t.Fatalf("%s accepted tampered signature", scope)
		}
	}

	config.ProductPreviewEnabled = false
	if _, err := mintSTRIDEProductActivationReceipt(config, 9, STRIDEProductScopeWork, now); err != ErrSTRIDEProductDisabled {
		t.Fatalf("default-off mint error=%v", err)
	}
	if isSTRIDEProductNonce("00") || isSTRIDEProductNonce(strings.Repeat("z", 32)) {
		t.Fatal("malformed product nonce accepted")
	}
}

func TestSTRIDEProductLearningIsHumanCorrectableForgettableAndSensitiveSafe(t *testing.T) {
	state := NewSTRIDEProductState()
	now := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
	agent, err := state.beginTrial("mary-marketing", "member_aj", now)
	if err != nil {
		t.Fatal(err)
	}
	agent, err = state.recordAgentLearning(agent.ID, agent.Revision, "positioning", "team", "The team prefers concrete language.", now.Add(time.Minute))
	if err != nil || len(agent.Learning) != 1 || agent.Learning[0].Status != "reviewed" {
		t.Fatalf("record learning agent=%+v err=%v", agent, err)
	}
	learningID := agent.Learning[0].ID
	replayed, err := state.recordAgentLearning(agent.ID, 1, "positioning", "team", "The team prefers concrete language.", now.Add(90*time.Second))
	if err != nil || replayed.Revision != agent.Revision || len(replayed.Learning) != 1 {
		t.Fatalf("record replay agent=%+v err=%v", replayed, err)
	}
	agent, err = state.resolveAgentLearning(agent.ID, agent.Revision, learningID, "correct", "The team prefers concrete, evidence-linked language.", now.Add(2*time.Minute))
	if err != nil || agent.Learning[0].Status != "corrected" || !strings.Contains(agent.Learning[0].Summary, "evidence-linked") {
		t.Fatalf("correct learning agent=%+v err=%v", agent, err)
	}
	replayed, err = state.resolveAgentLearning(agent.ID, agent.Revision-1, learningID, "correct", "The team prefers concrete, evidence-linked language.", now.Add(150*time.Second))
	if err != nil || replayed.Revision != agent.Revision || replayed.Learning[0].Revision != agent.Learning[0].Revision {
		t.Fatalf("correct replay agent=%+v err=%v", replayed, err)
	}
	agent, err = state.resolveAgentLearning(agent.ID, agent.Revision, learningID, "forget", "", now.Add(3*time.Minute))
	if err != nil || agent.Learning[0].Status != "forgotten" || strings.Contains(agent.Learning[0].Summary, "evidence-linked") {
		t.Fatalf("forget learning agent=%+v err=%v", agent, err)
	}
	replayed, err = state.resolveAgentLearning(agent.ID, agent.Revision-1, learningID, "forget", "", now.Add(210*time.Second))
	if err != nil || replayed.Revision != agent.Revision || replayed.Learning[0].Status != "forgotten" {
		t.Fatalf("forget replay agent=%+v err=%v", replayed, err)
	}
	if _, err = state.recordAgentLearning(agent.ID, agent.Revision, "medical_history", "team", "Never retain this.", now.Add(4*time.Minute)); err != ErrSTRIDEProductInvalid {
		t.Fatalf("sensitive learning error=%v", err)
	}
	if _, err = state.resolveAgentLearning(agent.ID, agent.Revision-2, learningID, "correct", "stale", now.Add(5*time.Minute)); err != ErrSTRIDEProductConflict {
		t.Fatalf("stale revision error=%v", err)
	}
}

func TestSTRIDEProductUpdateProposalAndReviewAreExactlyReplayable(t *testing.T) {
	state := NewSTRIDEProductState()
	now := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
	agent, err := state.beginTrial("rowan-research", "member_aj", now)
	if err != nil {
		t.Fatal(err)
	}
	candidate := STRIDEProductAgentConfig{PersonalityNotes: "Lead with sources.", Memberships: []string{"team"}, Proactivity: "quiet"}
	proposed, err := state.proposeAgentUpdate(agent.ID, agent.Revision, "Tighten sourcing behavior", candidate, now.Add(time.Minute))
	if err != nil || len(proposed.Updates) != 1 {
		t.Fatalf("propose=%+v err=%v", proposed, err)
	}
	replayed, err := state.proposeAgentUpdate(agent.ID, agent.Revision, "Tighten sourcing behavior", candidate, now.Add(2*time.Minute))
	if err != nil || replayed.Revision != proposed.Revision || len(replayed.Updates) != 1 {
		t.Fatalf("proposal replay=%+v err=%v", replayed, err)
	}
	approved, err := state.resolveAgentUpdate(agent.ID, proposed.Revision, proposed.Updates[0].ID, "approve", now.Add(3*time.Minute))
	if err != nil || approved.Updates[0].Status != "approved" {
		t.Fatalf("approve=%+v err=%v", approved, err)
	}
	replayed, err = state.resolveAgentUpdate(agent.ID, proposed.Revision, proposed.Updates[0].ID, "approve", now.Add(4*time.Minute))
	if err != nil || replayed.Revision != approved.Revision || replayed.Updates[0].Revision != approved.Updates[0].Revision {
		t.Fatalf("approval replay=%+v err=%v", replayed, err)
	}
}
