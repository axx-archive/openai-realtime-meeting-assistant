package main

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"
)

func strideAcceptanceTargetForTest(id string, threshold float64) STRIDEAcceptanceTarget {
	return STRIDEAcceptanceTarget{ID: id, Wave: "E4", MetricDefinition: "authorized retrieval top-one rate", FixtureDigest: strings.Repeat("a", 64), EnvironmentDigest: strings.Repeat("b", 64), SampleSize: 200, Comparator: STRIDEAcceptanceAtLeast, Threshold: threshold, ConfidenceMethod: "wilson_95", MeasurementRevision: strings.Repeat("c", 64), OwnerRole: "intelligence_owner", RollbackTrigger: "disable public retrieval", Reviewer: "independent_reviewer", CreatedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)}
}

func TestSTRIDEAcceptanceRegistryRequiresValidSignatureAndChain(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewSignedSTRIDEAcceptanceRegistryRevision("stride_e4_targets", 1, "", []STRIDEAcceptanceTarget{strideAcceptanceTargetForTest("retrieval_precision", .95)}, time.Now().UTC(), "acceptance_owner", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	book, err := NewSTRIDEAcceptanceTargetBook("acceptance_owner", publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Register(first); err != nil {
		t.Fatal(err)
	}
	tampered := first
	tampered.Targets[0].Threshold = .90
	tamperBook, err := NewSTRIDEAcceptanceTargetBook("acceptance_owner", publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := tamperBook.Register(tampered); !errors.Is(err, ErrSTRIDEAcceptanceSignature) {
		t.Fatalf("tamper error=%v", err)
	}
	broken, err := NewSignedSTRIDEAcceptanceRegistryRevision("stride_e4_targets", 3, first.ContentDigest, []STRIDEAcceptanceTarget{strideAcceptanceTargetForTest("retrieval_precision", .96)}, time.Now().UTC().Add(time.Minute), "acceptance_owner", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Register(broken); !errors.Is(err, ErrSTRIDEAcceptanceRevision) {
		t.Fatalf("chain error=%v", err)
	}
}

func TestSTRIDEAcceptanceRegistryPinsSignerIdentityAndKey(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	_, otherPrivateKey, _ := ed25519.GenerateKey(nil)
	created := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	first, _ := NewSignedSTRIDEAcceptanceRegistryRevision("stride_e4_targets", 1, "", []STRIDEAcceptanceTarget{strideAcceptanceTargetForTest("retrieval_precision", .95)}, created, "acceptance_owner", privateKey)
	book, err := NewSTRIDEAcceptanceTargetBook("acceptance_owner", publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Register(first); err != nil {
		t.Fatal(err)
	}

	changedKey, _ := NewSignedSTRIDEAcceptanceRegistryRevision(first.RegistryID, 2, first.ContentDigest, []STRIDEAcceptanceTarget{strideAcceptanceTargetForTest("retrieval_precision", .96)}, created.Add(time.Minute), "acceptance_owner", otherPrivateKey)
	if err := book.Register(changedKey); !errors.Is(err, ErrSTRIDEAcceptanceSignature) {
		t.Fatalf("changed key error=%v", err)
	}

	changedKeyID, _ := NewSignedSTRIDEAcceptanceRegistryRevision(first.RegistryID, 2, first.ContentDigest, []STRIDEAcceptanceTarget{strideAcceptanceTargetForTest("retrieval_precision", .96)}, created.Add(time.Minute), "other_owner", privateKey)
	if err := book.Register(changedKeyID); !errors.Is(err, ErrSTRIDEAcceptanceSigner) {
		t.Fatalf("changed key id error=%v", err)
	}

	untrustedBook, err := NewSTRIDEAcceptanceTargetBook("acceptance_owner", publicKey)
	if err != nil {
		t.Fatal(err)
	}
	untrustedFirst, _ := NewSignedSTRIDEAcceptanceRegistryRevision(first.RegistryID, 1, "", []STRIDEAcceptanceTarget{strideAcceptanceTargetForTest("retrieval_precision", .95)}, created, "acceptance_owner", otherPrivateKey)
	if err := untrustedBook.Register(untrustedFirst); !errors.Is(err, ErrSTRIDEAcceptanceSignature) {
		t.Fatalf("caller-supplied first signer became trusted: %v", err)
	}
}

func TestSTRIDEAcceptanceMeasuredTargetCanTightenButNeverLoosen(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	created := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	first, _ := NewSignedSTRIDEAcceptanceRegistryRevision("stride_e4_targets", 1, "", []STRIDEAcceptanceTarget{strideAcceptanceTargetForTest("retrieval_precision", .95)}, created, "acceptance_owner", privateKey)
	book, err := NewSTRIDEAcceptanceTargetBook("acceptance_owner", publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Register(first); err != nil {
		t.Fatal(err)
	}
	start, err := book.BeginMeasurement(first.RegistryID, first.Revision, "retrieval_precision", "synthetic", created.Add(time.Minute))
	if err != nil || start.ProviderQualified || start.RegistryDigest != first.ContentDigest {
		t.Fatalf("measurement=%#v err=%v", start, err)
	}
	looser, _ := NewSignedSTRIDEAcceptanceRegistryRevision(first.RegistryID, 2, first.ContentDigest, []STRIDEAcceptanceTarget{strideAcceptanceTargetForTest("retrieval_precision", .90)}, created.Add(2*time.Minute), "acceptance_owner", privateKey)
	if err := book.Register(looser); !errors.Is(err, ErrSTRIDEAcceptanceLoosened) {
		t.Fatalf("loosen error=%v", err)
	}
	tighter, _ := NewSignedSTRIDEAcceptanceRegistryRevision(first.RegistryID, 2, first.ContentDigest, []STRIDEAcceptanceTarget{strideAcceptanceTargetForTest("retrieval_precision", .97)}, created.Add(3*time.Minute), "acceptance_owner", privateKey)
	if err := book.Register(tighter); err != nil {
		t.Fatalf("tighten: %v", err)
	}
}

func TestSTRIDEAcceptanceHardGatesAreExactlyZero(t *testing.T) {
	target := strideAcceptanceTargetForTest("acl_disclosure", 0)
	target.Comparator, target.ConfidenceMethod, target.HardGate = STRIDEAcceptanceExactlyZero, "none", true
	if err := target.Validate(); err != nil {
		t.Fatal(err)
	}
	target.Threshold = 0.001
	if err := target.Validate(); !errors.Is(err, ErrSTRIDEAcceptanceTargetInvalid) {
		t.Fatalf("nonzero hard gate error=%v", err)
	}
}

func TestSTRIDEAcceptanceRequiresSortedUniqueTargets(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	targets := []STRIDEAcceptanceTarget{strideAcceptanceTargetForTest("z_target", .9), strideAcceptanceTargetForTest("a_target", .9)}
	if _, err := NewSignedSTRIDEAcceptanceRegistryRevision("stride_e4_targets", 1, "", targets, time.Now().UTC(), "acceptance_owner", privateKey); !errors.Is(err, ErrSTRIDEAcceptanceTargetInvalid) {
		t.Fatalf("unsorted target error=%v", err)
	}
	if sorted := SortSTRIDEAcceptanceTargets(targets); sorted[0].ID != "a_target" {
		t.Fatalf("sort=%#v", sorted)
	}
}
