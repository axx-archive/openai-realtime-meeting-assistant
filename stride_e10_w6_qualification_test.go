package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func w6QualificationFixture(policy W6NetworkPolicyRevision, at time.Time, profileCount, reviewerCount int) W6NetworkQualificationReceipt {
	receipt := W6NetworkQualificationReceipt{ReceiptID: "qualification_pilot", Revision: 1, PolicyID: policy.PolicyID, PolicyRevision: policy.Revision, CohortID: "cohort_pilot", ProhibitedCorpusDigest: policy.ProhibitedCorpusDigest, QualifiedAt: at, ExpiresAt: at.Add(time.Hour)}
	for i := 0; i < profileCount; i++ {
		person := fmt.Sprintf("person_%d", i)
		receipt.Profiles = append(receipt.Profiles, W6ConsentedProfileQualification{PersonID: person, ConsentRevision: 1,
			Profile:          STRIDEReference{ContractType: STRIDEContractNetworkProfileProjection, ID: "profile_" + person, Revision: 1, Digest: sha256Hex([]byte("profile" + person))},
			Publication:      STRIDEReference{ContractType: STRIDEContractPublishedContributionClaim, ID: "publication_" + person, Revision: 1, Digest: sha256Hex([]byte("publication" + person))},
			AttestationCount: 1, DisclosurePolicyDigest: policy.DisclosurePolicyDigest})
	}
	for i := 0; i < reviewerCount; i++ {
		receipt.Reviewers = append(receipt.Reviewers, W6ReviewerQualification{PersonID: fmt.Sprintf("reviewer_%d", i), ReviewRevision: 1, CorpusDigest: policy.ProhibitedCorpusDigest, Passed: true})
	}
	return receipt
}

func w6TestQualificationAuthority(t *testing.T, keys W6ManagedMACKeyring, policy W6NetworkPolicyRevision, at time.Time) *W6NetworkQualificationAuthority {
	t.Helper()
	receipt, err := SignW6NetworkQualification(context.Background(), keys, policy, w6QualificationFixture(policy, at, 5, 2))
	if err != nil {
		t.Fatal(err)
	}
	authority := NewW6NetworkQualificationAuthority(keys)
	if err := authority.Install(context.Background(), policy, receipt, at); err != nil {
		t.Fatal(err)
	}
	return authority
}

func TestW6QualificationRequiresFiveConsentersTwoReviewersAndNoProvider(t *testing.T) {
	now := time.Date(2026, 8, 9, 17, 0, 0, 0, time.UTC)
	_, keys, policy := w6TestPolicyAuthority(t, now)
	for name, mutate := range map[string]func(*W6NetworkQualificationReceipt){
		"four profiles": func(v *W6NetworkQualificationReceipt) { v.Profiles = v.Profiles[:4] },
		"one reviewer":  func(v *W6NetworkQualificationReceipt) { v.Reviewers = v.Reviewers[:1] },
		"provider":      func(v *W6NetworkQualificationReceipt) { v.ProviderUsed = true },
		"wrong disclosure": func(v *W6NetworkQualificationReceipt) {
			v.Profiles[0].DisclosurePolicyDigest = sha256Hex([]byte("other"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := w6QualificationFixture(policy, now, 5, 2)
			mutate(&value)
			if _, err := SignW6NetworkQualification(context.Background(), keys, policy, value); !errors.Is(err, ErrW6QualificationInvalid) {
				t.Fatalf("invalid qualification signed: %v", err)
			}
		})
	}
	signed, err := SignW6NetworkQualification(context.Background(), keys, policy, w6QualificationFixture(policy, now, 5, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyW6NetworkQualification(context.Background(), keys, policy, signed, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// ConsentRevision is authenticated reviewer evidence, not a current runtime
	// authority. A newly signed evidence revision is valid without claiming a
	// consent resolver; runtime admission still revalidates profile,
	// publication, and attestation authority independently.
	evidenceOnly := w6QualificationFixture(policy, now, 5, 2)
	evidenceOnly.ReceiptID, evidenceOnly.Revision = "qualification_consent_evidence", 2
	evidenceOnly.Profiles[0].ConsentRevision = 9
	evidenceSigned, err := SignW6NetworkQualification(context.Background(), keys, policy, evidenceOnly)
	if err != nil || evidenceSigned.MAC == signed.MAC || VerifyW6NetworkQualification(context.Background(), keys, policy, evidenceSigned, now.Add(time.Minute)) != nil {
		t.Fatalf("evidence-only consent revision was not independently authenticated: %v", err)
	}
	tampered := cloneContract(signed)
	tampered.Profiles[0].ConsentRevision++
	if err := VerifyW6NetworkQualification(context.Background(), keys, policy, tampered, now.Add(time.Minute)); !errors.Is(err, ErrW6QualificationInvalid) {
		t.Fatalf("tamper accepted: %v", err)
	}
	authority := NewW6NetworkQualificationAuthority(keys)
	if err := authority.Install(context.Background(), policy, signed, now); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := authority.WithCurrentW6Qualification(context.Background(), policy, "cohort_pilot", now.Add(time.Minute), func(W6NetworkQualificationReceipt) error { called = true; return nil }); err != nil || !called {
		t.Fatalf("current qualification: %v", err)
	}
	if err := authority.Install(context.Background(), policy, signed, now); !errors.Is(err, ErrW6QualificationInvalid) {
		t.Fatalf("replayed receipt accepted: %v", err)
	}
}
