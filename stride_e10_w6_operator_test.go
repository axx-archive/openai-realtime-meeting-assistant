package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type strideE10W6OperatorTestAuthority struct {
	state     StrideE10W6OperatorCurrentState
	beforeUse func()
}

func (a *strideE10W6OperatorTestAuthority) WithCurrentStrideE10W6OperatorState(_ context.Context, use func(StrideE10W6OperatorCurrentState) error) error {
	if a.beforeUse != nil {
		a.beforeUse()
	}
	return use(cloneContract(a.state))
}

type strideE10W6TestSigner struct {
	personID, keyID string
	public          ed25519.PublicKey
	private         ed25519.PrivateKey
}

func newStrideE10W6TestSigner(t *testing.T, personID, keyID string) strideE10W6TestSigner {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return strideE10W6TestSigner{personID: personID, keyID: keyID, public: public, private: private}
}

func (s strideE10W6TestSigner) raw(t *testing.T, domain, packetDigest, subject string) string {
	t.Helper()
	payload, err := STRIDEContractDigest(struct{ Domain, PacketDigest, Subject, KeyID, PersonID string }{domain, packetDigest, subject, s.keyID, s.personID})
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(s.private, []byte(payload)))
}

func (s strideE10W6TestSigner) detached(t *testing.T, role, domain, packetDigest, subject string, at time.Time) StrideE10W6DetachedSignature {
	signedSubject, err := STRIDEContractDigest(struct {
		Subject  string
		SignedAt time.Time
	}{subject, at.UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return StrideE10W6DetachedSignature{Role: role, SignerPersonID: s.personID, KeyID: s.keyID, PublicKey: base64.StdEncoding.EncodeToString(s.public), Signature: s.raw(t, domain, packetDigest, signedSubject), SignedAt: at}
}

func strideE10W6OperatorFixture(t *testing.T) (StrideE10W6OperatorRequirementsPacket, StrideE10W6OperatorSubmission, *StrideE10W6Operator, *strideE10W6OperatorTestAuthority, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 9, 19, 0, 0, 0, time.UTC)
	_, keys, policy := w6TestPolicyAuthority(t, now)
	release := "1cf3463cf30938e956e892a5cde5c9009eaad296"
	profiles := make([]StrideE10W6ProposedProfile, 0, 5)
	for i := 0; i < 5; i++ {
		person := "cohort_person_" + string(rune('a'+i))
		profiles = append(profiles, StrideE10W6ProposedProfile{PersonID: person, Profile: STRIDEReference{ContractType: STRIDEContractNetworkProfileProjection, ID: "profile_" + person, Revision: 2, Digest: sha256Hex([]byte("profile" + person))}, Publication: STRIDEReference{ContractType: STRIDEContractPublishedContributionClaim, ID: "publication_" + person, Revision: 1, Digest: sha256Hex([]byte("publication" + person))}, AttestationCount: 1})
	}
	rollback := StrideE10W6RollbackBoundary{ActivationID: sha256Hex([]byte("w4-activation")), ActivationReceiptDigest: sha256Hex([]byte("w4-receipt")), ReleaseCommit: release, SnapshotGeneration: 103, SnapshotDigest: sha256Hex([]byte("snapshot")), SessionsDigest: sha256Hex([]byte("sessions")), RollbackVerifierDigest: sha256Hex([]byte("rollback-verifier")), KillSwitches: append([]STRIDEFeature(nil), strideE10W6ActivationSwitches...)}
	health := StrideE10W6HealthBoundary{ShadowSnapshotDigest: sha256Hex([]byte("shadow snapshot")), ReconcileDigest: sha256Hex([]byte("reconcile")), PurgeHealthDigest: sha256Hex([]byte("purge health")), ShadowGeneration: 9, SnapshotRevision: 9, IndexedRevision: 9}
	packet, err := NewStrideE10W6OperatorRequirementsPacket(policy, release, "cohort_pilot", profiles, rollback, health, now, now.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := strideE10W6OperatorPacketDigest(packet)
	submission := StrideE10W6OperatorSubmission{PacketDigest: digest}
	trusted := []StrideE10W6TrustedSigner{}
	for _, role := range packet.RequiredApprovalRoles {
		signer := newStrideE10W6TestSigner(t, "approver_"+role, "key_"+role)
		submission.Approvals = append(submission.Approvals, signer.detached(t, role, "approval", digest, role, now))
		trusted = append(trusted, StrideE10W6TrustedSigner{Role: role, SignerPersonID: signer.personID, KeyID: signer.keyID, PublicKey: base64.StdEncoding.EncodeToString(signer.public)})
	}
	for _, profile := range packet.Profiles {
		signer := newStrideE10W6TestSigner(t, profile.PersonID, "key_"+profile.PersonID)
		profileDigest, _ := STRIDEContractDigest(profile)
		submission.Consents = append(submission.Consents, StrideE10W6ProfileConsent{Profile: profile, Signature: signer.detached(t, "profile_consent", "consent", digest, profileDigest, now)})
		trusted = append(trusted, StrideE10W6TrustedSigner{Role: "profile_consent", SignerPersonID: signer.personID, KeyID: signer.keyID, PublicKey: base64.StdEncoding.EncodeToString(signer.public)})
	}
	for i := 0; i < 2; i++ {
		person, key := "reviewer_"+string(rune('a'+i)), "reviewer_key_"+string(rune('a'+i))
		signer := newStrideE10W6TestSigner(t, person, key)
		reviewSubject, err := STRIDEContractDigest(struct {
			CorpusDigest   string
			ReviewRevision int64
			SignedAt       time.Time
		}{packet.ProhibitedCorpusDigest, 1, now.UTC()})
		if err != nil {
			t.Fatal(err)
		}
		submission.Reviewers = append(submission.Reviewers, StrideE10W6ReviewerAttestation{SignerPersonID: person, KeyID: key, PublicKey: base64.StdEncoding.EncodeToString(signer.public), Signature: signer.raw(t, "reviewer", digest, reviewSubject), ReviewRevision: 1, SignedAt: now})
		trusted = append(trusted, StrideE10W6TrustedSigner{Role: "reviewer", SignerPersonID: signer.personID, KeyID: signer.keyID, PublicKey: base64.StdEncoding.EncodeToString(signer.public)})
	}
	managedKey, err := keys.CurrentW6ManagedMACKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	registrySnapshot := StrideE10W6TrustRegistrySnapshot{Schema: "stride.e10.w6.trust-registry.v1", RegistryID: "w6_external_registry", PolicyID: packet.PolicyID, PolicyRevision: packet.PolicyRevision, PolicyDigest: packet.PolicyDigest, CohortID: packet.CohortID, Signers: trusted, IssuedAt: now.Add(-time.Minute), ExpiresAt: packet.ExpiresAt, KeyID: managedKey.ID, KeyVersion: managedKey.Version}
	payload, err := strideE10W6TrustRegistryPayload(registrySnapshot)
	if err != nil {
		t.Fatal(err)
	}
	registrySnapshot.MAC = strideE10W6OperatorMAC(managedKey.Secret, payload)
	registry, err := NewStrideE10W6SealedTrustRegistry(context.Background(), keys, registrySnapshot, now)
	if err != nil {
		t.Fatal(err)
	}
	features := map[STRIDEFeature]bool{}
	for _, feature := range strideE10W6ActivationSwitches {
		features[feature] = false
	}
	for _, feature := range strideE10W6AlwaysDisabledSwitches {
		features[feature] = false
	}
	authority := &strideE10W6OperatorTestAuthority{state: StrideE10W6OperatorCurrentState{ReleaseCommit: release, W4Ready: true, W4ActivationID: rollback.ActivationID, W4ReceiptDigest: rollback.ActivationReceiptDigest, W4Generation: rollback.SnapshotGeneration, SnapshotDigest: rollback.SnapshotDigest, SessionsDigest: rollback.SessionsDigest, RollbackVerifierDigest: rollback.RollbackVerifierDigest, PolicyID: packet.PolicyID, PolicyRevision: packet.PolicyRevision, PolicyDigest: packet.PolicyDigest, DisclosurePolicyDigest: packet.DisclosurePolicyDigest, ProhibitedCorpusDigest: packet.ProhibitedCorpusDigest, ShadowSnapshotRevision: health.SnapshotRevision, IndexedRevision: health.IndexedRevision, ShadowSnapshotDigest: health.ShadowSnapshotDigest, ReconcileDigest: health.ReconcileDigest, PurgeHealthDigest: health.PurgeHealthDigest, ShadowGeneration: health.ShadowGeneration, ReconcileHealthy: true, PurgeWorkerHealthy: true, Profiles: cloneContract(packet.Profiles), FeatureEnabled: features}}
	operator, err := NewStrideE10W6Operator(authority, registry, keys)
	if err != nil {
		t.Fatal(err)
	}
	return packet, submission, operator, authority, now
}

func TestStrideE10W6OperatorRequiresRealIndependentSignaturesAndCurrentHealthyState(t *testing.T) {
	packet, submission, operator, _, now := strideE10W6OperatorFixture(t)
	receipt, err := operator.VerifySubmission(context.Background(), packet, submission, now.Add(time.Minute))
	if err != nil || receipt.Status != "verified_for_external_activation" || receipt.ActivationPerformed || !isHexDigest(receipt.MAC) {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if err := VerifyStrideE10W6OperatorVerificationReceipt(context.Background(), operator.keys, receipt, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("managed receipt rejected: %v", err)
	}
	tamperedReceipt := receipt
	tamperedReceipt.PacketDigest = sha256Hex([]byte("other packet"))
	if err := VerifyStrideE10W6OperatorVerificationReceipt(context.Background(), operator.keys, tamperedReceipt, now.Add(2*time.Minute)); !errors.Is(err, ErrStrideE10W6OperatorInvalid) {
		t.Fatalf("tampered managed receipt admitted: %v", err)
	}

	for name, mutate := range map[string]func(*StrideE10W6OperatorSubmission, *strideE10W6OperatorTestAuthority){
		"missing legal": func(s *StrideE10W6OperatorSubmission, _ *strideE10W6OperatorTestAuthority) {
			s.Approvals = s.Approvals[1:]
		},
		"self review": func(s *StrideE10W6OperatorSubmission, _ *strideE10W6OperatorTestAuthority) {
			s.Reviewers[0].SignerPersonID = s.Consents[0].Profile.PersonID
		},
		"tampered consent": func(s *StrideE10W6OperatorSubmission, _ *strideE10W6OperatorTestAuthority) {
			s.Consents[0].Profile.AttestationCount++
		},
		"purge backlog": func(_ *StrideE10W6OperatorSubmission, a *strideE10W6OperatorTestAuthority) { a.state.PurgeQueued = 1 },
		"shadow lag":    func(_ *StrideE10W6OperatorSubmission, a *strideE10W6OperatorTestAuthority) { a.state.IndexedRevision-- },
		"switch already on": func(_ *StrideE10W6OperatorSubmission, a *strideE10W6OperatorTestAuthority) {
			a.state.FeatureEnabled[STRIDEFeatureNetworkSearch] = true
		},
		"switch state unavailable": func(_ *StrideE10W6OperatorSubmission, a *strideE10W6OperatorTestAuthority) {
			delete(a.state.FeatureEnabled, STRIDEFeatureNetworkContact)
		},
		"reconcile unhealthy": func(_ *StrideE10W6OperatorSubmission, a *strideE10W6OperatorTestAuthority) {
			a.state.ReconcileHealthy = false
		},
		"purge worker unhealthy": func(_ *StrideE10W6OperatorSubmission, a *strideE10W6OperatorTestAuthority) {
			a.state.PurgeWorkerHealthy = false
		},
		"stale W4 lineage": func(_ *StrideE10W6OperatorSubmission, a *strideE10W6OperatorTestAuthority) { a.state.W4Generation++ },
		"stale policy":     func(_ *StrideE10W6OperatorSubmission, a *strideE10W6OperatorTestAuthority) { a.state.PolicyRevision++ },
		"stale rollback verifier": func(_ *StrideE10W6OperatorSubmission, a *strideE10W6OperatorTestAuthority) {
			a.state.RollbackVerifierDigest = sha256Hex([]byte("other rollback verifier"))
		},
		"signature outside packet window": func(s *StrideE10W6OperatorSubmission, _ *strideE10W6OperatorTestAuthority) {
			s.Approvals[0].SignedAt = packet.ExpiresAt
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, base, candidate, current, _ := strideE10W6OperatorFixture(t)
			mutate(&base, current)
			if _, err := candidate.VerifySubmission(context.Background(), packet, base, now.Add(time.Minute)); !errors.Is(err, ErrStrideE10W6OperatorInvalid) {
				t.Fatalf("invalid operator submission admitted: %v", err)
			}
		})
	}
}

func TestStrideE10W6OperatorRejectsReplacedTrustUniverseAndBodyLeakage(t *testing.T) {
	packet, submission, operator, _, now := strideE10W6OperatorFixture(t)
	replacement := newStrideE10W6TestSigner(t, "fake_legal", "fake_legal_key")
	submission.Approvals[0] = replacement.detached(t, "legal", "approval", submission.PacketDigest, "legal", now)
	if _, err := operator.VerifySubmission(context.Background(), packet, submission, now.Add(time.Minute)); !errors.Is(err, ErrStrideE10W6OperatorInvalid) {
		t.Fatalf("replacement signer universe admitted: %v", err)
	}
	replaced := StrideE10W6OperatorSubmission{PacketDigest: submission.PacketDigest}
	for _, role := range packet.RequiredApprovalRoles {
		signer := newStrideE10W6TestSigner(t, "replacement_"+role, "replacement_key_"+role)
		replaced.Approvals = append(replaced.Approvals, signer.detached(t, role, "approval", replaced.PacketDigest, role, now))
	}
	for _, profile := range packet.Profiles {
		signer := newStrideE10W6TestSigner(t, profile.PersonID, "replacement_key_"+profile.PersonID)
		profileDigest, err := STRIDEContractDigest(profile)
		if err != nil {
			t.Fatal(err)
		}
		replaced.Consents = append(replaced.Consents, StrideE10W6ProfileConsent{Profile: profile, Signature: signer.detached(t, "profile_consent", "consent", replaced.PacketDigest, profileDigest, now)})
	}
	for i := 0; i < 2; i++ {
		person := "replacement_reviewer_" + string(rune('a'+i))
		signer := newStrideE10W6TestSigner(t, person, "replacement_reviewer_key_"+string(rune('a'+i)))
		reviewSubject, err := STRIDEContractDigest(struct {
			CorpusDigest   string
			ReviewRevision int64
			SignedAt       time.Time
		}{packet.ProhibitedCorpusDigest, 1, now.UTC()})
		if err != nil {
			t.Fatal(err)
		}
		replaced.Reviewers = append(replaced.Reviewers, StrideE10W6ReviewerAttestation{SignerPersonID: signer.personID, KeyID: signer.keyID, PublicKey: base64.StdEncoding.EncodeToString(signer.public), Signature: signer.raw(t, "reviewer", replaced.PacketDigest, reviewSubject), ReviewRevision: 1, SignedAt: now})
	}
	if _, err := operator.VerifySubmission(context.Background(), packet, replaced, now.Add(time.Minute)); !errors.Is(err, ErrStrideE10W6OperatorInvalid) {
		t.Fatalf("wholly replaced but cryptographically valid universe admitted: %v", err)
	}

	tamperedRegistry := cloneContract(operator.trust.snapshot)
	tamperedRegistry.Signers[0] = StrideE10W6TrustedSigner{Role: "legal", SignerPersonID: replacement.personID, KeyID: replacement.keyID, PublicKey: base64.StdEncoding.EncodeToString(replacement.public)}
	if _, err := NewStrideE10W6SealedTrustRegistry(context.Background(), operator.keys, tamperedRegistry, now); !errors.Is(err, ErrStrideE10W6OperatorInvalid) {
		t.Fatalf("unsealed replacement trust registry admitted: %v", err)
	}

	encoded, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"\"displayName\":", "\"email\":", "\"bio\":", "\"query\":", "\"note\":", "\"channel\":", "\"publicKey\":", "\"signature\":"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("requirements packet leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestStrideE10W6OperatorCannotGenerateWithoutExactExternalCohortOrRollback(t *testing.T) {
	now := time.Date(2026, 8, 9, 19, 0, 0, 0, time.UTC)
	_, _, policy := w6TestPolicyAuthority(t, now)
	if _, err := NewStrideE10W6OperatorRequirementsPacket(policy, "1cf3463cf30938e956e892a5cde5c9009eaad296", "cohort_pilot", nil, StrideE10W6RollbackBoundary{}, StrideE10W6HealthBoundary{}, now, now.Add(time.Minute)); !errors.Is(err, ErrStrideE10W6OperatorInvalid) {
		t.Fatalf("empty synthetic cohort packet: %v", err)
	}
}
