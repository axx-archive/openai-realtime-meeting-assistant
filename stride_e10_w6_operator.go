package main

// This file is a route-free, activation-free W6 operator preflight. It can
// describe and verify a real proposed cohort, but it cannot select people,
// create signatures, flip switches, install authority, or mutate runtime data.

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

var ErrStrideE10W6OperatorInvalid = errors.New("invalid stride e10 w6 operator packet")

const strideE10W6OperatorSchema = "stride.e10.w6.operator-requirements.v1"

var strideE10W6ActivationSwitches = []STRIDEFeature{
	STRIDEFeatureNetworkProfilePublication,
	STRIDEFeatureNetworkProjectionShadow,
	STRIDEFeatureNetworkSearch,
	STRIDEFeatureNetworkContact,
}

var strideE10W6AlwaysDisabledSwitches = []STRIDEFeature{
	STRIDEFeatureNetworkQueryParserProvider,
	STRIDEFeatureNetworkSemanticReranker,
}

type StrideE10W6ProposedProfile struct {
	PersonID         string          `json:"personId"`
	Profile          STRIDEReference `json:"profile"`
	Publication      STRIDEReference `json:"publication"`
	AttestationCount int             `json:"attestationCount"`
}

type StrideE10W6RollbackBoundary struct {
	ActivationID            string          `json:"activationId"`
	ActivationReceiptDigest string          `json:"activationReceiptDigest"`
	ReleaseCommit           string          `json:"releaseCommit"`
	SnapshotGeneration      uint64          `json:"snapshotGeneration"`
	SnapshotDigest          string          `json:"snapshotDigest"`
	SessionsDigest          string          `json:"sessionsDigest"`
	RollbackVerifierDigest  string          `json:"rollbackVerifierDigest"`
	KillSwitches            []STRIDEFeature `json:"killSwitches"`
}

type StrideE10W6HealthBoundary struct {
	ShadowSnapshotDigest string `json:"shadowSnapshotDigest"`
	ReconcileDigest      string `json:"reconcileDigest"`
	PurgeHealthDigest    string `json:"purgeHealthDigest"`
	ShadowGeneration     uint64 `json:"shadowGeneration"`
	SnapshotRevision     int64  `json:"snapshotRevision"`
	IndexedRevision      int64  `json:"indexedRevision"`
}

type StrideE10W6OperatorRequirementsPacket struct {
	Schema                     string                       `json:"schema"`
	PacketID                   string                       `json:"packetId"`
	ReleaseCommit              string                       `json:"releaseCommit"`
	PolicyID                   string                       `json:"policyId"`
	PolicyRevision             int64                        `json:"policyRevision"`
	PolicyDigest               string                       `json:"policyDigest"`
	DisclosurePolicyDigest     string                       `json:"disclosurePolicyDigest"`
	ProhibitedCorpusDigest     string                       `json:"prohibitedCorpusDigest"`
	CohortID                   string                       `json:"cohortId"`
	Profiles                   []StrideE10W6ProposedProfile `json:"profiles"`
	RequiredReviewerCount      int                          `json:"requiredReviewerCount"`
	RequiredApprovalRoles      []string                     `json:"requiredApprovalRoles"`
	RequiredActivationSwitches []STRIDEFeature              `json:"requiredActivationSwitches"`
	RequiredDisabledSwitches   []STRIDEFeature              `json:"requiredDisabledSwitches"`
	Rollback                   StrideE10W6RollbackBoundary  `json:"rollback"`
	Health                     StrideE10W6HealthBoundary    `json:"health"`
	GeneratedAt                time.Time                    `json:"generatedAt"`
	ExpiresAt                  time.Time                    `json:"expiresAt"`
}

type StrideE10W6DetachedSignature struct {
	Role           string    `json:"role"`
	SignerPersonID string    `json:"signerPersonId"`
	KeyID          string    `json:"keyId"`
	PublicKey      string    `json:"publicKey"`
	Signature      string    `json:"signature"`
	SignedAt       time.Time `json:"signedAt"`
}

type StrideE10W6ProfileConsent struct {
	Profile   StrideE10W6ProposedProfile
	Signature StrideE10W6DetachedSignature
}

type StrideE10W6ReviewerAttestation struct {
	SignerPersonID, KeyID, PublicKey, Signature string
	ReviewRevision                              int64
	SignedAt                                    time.Time
}

type StrideE10W6OperatorSubmission struct {
	PacketDigest string
	Approvals    []StrideE10W6DetachedSignature
	Consents     []StrideE10W6ProfileConsent
	Reviewers    []StrideE10W6ReviewerAttestation
}

type StrideE10W6OperatorCurrentState struct {
	ReleaseCommit                                            string
	W4Ready                                                  bool
	W4ActivationID, W4ReceiptDigest                          string
	W4Generation                                             uint64
	SnapshotDigest, SessionsDigest, RollbackVerifierDigest   string
	PolicyID, PolicyDigest                                   string
	PolicyRevision                                           int64
	DisclosurePolicyDigest, ProhibitedCorpusDigest           string
	ShadowSnapshotRevision, IndexedRevision                  int64
	ShadowSnapshotDigest, ReconcileDigest, PurgeHealthDigest string
	ShadowGeneration                                         uint64
	ReconcileHealthy, PurgeWorkerHealthy                     bool
	PurgeQueued, PurgeRunning, PurgeFailed                   int
	Profiles                                                 []StrideE10W6ProposedProfile
	FeatureEnabled                                           map[STRIDEFeature]bool
}

type StrideE10W6OperatorAuthority interface {
	WithCurrentStrideE10W6OperatorState(context.Context, func(StrideE10W6OperatorCurrentState) error) error
}

type StrideE10W6TrustedSigner struct {
	Role           string `json:"role"`
	SignerPersonID string `json:"signerPersonId"`
	KeyID          string `json:"keyId"`
	PublicKey      string `json:"publicKey"`
}

type StrideE10W6TrustRegistrySnapshot struct {
	Schema         string                     `json:"schema"`
	RegistryID     string                     `json:"registryId"`
	PolicyID       string                     `json:"policyId"`
	PolicyRevision int64                      `json:"policyRevision"`
	PolicyDigest   string                     `json:"policyDigest"`
	CohortID       string                     `json:"cohortId"`
	Signers        []StrideE10W6TrustedSigner `json:"signers"`
	IssuedAt       time.Time                  `json:"issuedAt"`
	ExpiresAt      time.Time                  `json:"expiresAt"`
	KeyID          string                     `json:"keyId"`
	KeyVersion     uint64                     `json:"keyVersion"`
	MAC            string                     `json:"mac"`
}

type StrideE10W6SealedTrustRegistry struct {
	snapshot StrideE10W6TrustRegistrySnapshot
	bySigner map[string]StrideE10W6TrustedSigner
}

type StrideE10W6Operator struct {
	authority StrideE10W6OperatorAuthority
	trust     *StrideE10W6SealedTrustRegistry
	keys      W6ManagedMACKeyring
}

type StrideE10W6OperatorVerificationReceipt struct {
	Schema              string    `json:"schema"`
	PacketID            string    `json:"packetId"`
	PacketDigest        string    `json:"packetDigest"`
	ReleaseCommit       string    `json:"releaseCommit"`
	TrustRegistryID     string    `json:"trustRegistryId"`
	TrustRegistryDigest string    `json:"trustRegistryDigest"`
	VerifiedAt          time.Time `json:"verifiedAt"`
	ExpiresAt           time.Time `json:"expiresAt"`
	Status              string    `json:"status"`
	ActivationPerformed bool      `json:"activationPerformed"`
	KeyID               string    `json:"keyId"`
	KeyVersion          uint64    `json:"keyVersion"`
	MAC                 string    `json:"mac"`
}

func NewStrideE10W6SealedTrustRegistry(ctx context.Context, keys W6ManagedMACKeyring, snapshot StrideE10W6TrustRegistrySnapshot, at time.Time) (*StrideE10W6SealedTrustRegistry, error) {
	if ctx == nil || keys == nil || at.IsZero() || snapshot.Schema != "stride.e10.w6.trust-registry.v1" || !strideIdentifier(snapshot.RegistryID) || !strideIdentifier(snapshot.PolicyID) || snapshot.PolicyRevision < 1 || !isHexDigest(snapshot.PolicyDigest) || !strideIdentifier(snapshot.CohortID) || snapshot.IssuedAt.IsZero() || !snapshot.ExpiresAt.After(snapshot.IssuedAt) || at.Before(snapshot.IssuedAt) || !at.Before(snapshot.ExpiresAt) || !strideIdentifier(snapshot.KeyID) || snapshot.KeyVersion == 0 || !isHexDigest(snapshot.MAC) {
		return nil, ErrStrideE10W6OperatorInvalid
	}
	key, err := keys.ResolveW6ManagedMACKey(ctx, snapshot.KeyID, snapshot.KeyVersion)
	if err != nil || key.ID != snapshot.KeyID || key.Version != snapshot.KeyVersion || len(key.Secret) < 32 {
		return nil, ErrStrideE10W6OperatorInvalid
	}
	payload, err := strideE10W6TrustRegistryPayload(snapshot)
	if err != nil || !verifyStrideE10W6OperatorMAC(key.Secret, payload, snapshot.MAC) {
		return nil, ErrStrideE10W6OperatorInvalid
	}
	registry := &StrideE10W6SealedTrustRegistry{snapshot: cloneContract(snapshot), bySigner: map[string]StrideE10W6TrustedSigner{}}
	people, keyIDs, publicKeys := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, signer := range snapshot.Signers {
		publicKey, decodeErr := base64.StdEncoding.DecodeString(signer.PublicKey)
		if decodeErr != nil || len(publicKey) != ed25519.PublicKeySize || !strideIdentifier(signer.SignerPersonID) || !strideIdentifier(signer.KeyID) || !validStrideE10W6TrustRole(signer.Role) || people[signer.SignerPersonID] || keyIDs[signer.KeyID] || publicKeys[signer.PublicKey] {
			return nil, ErrStrideE10W6OperatorInvalid
		}
		people[signer.SignerPersonID], keyIDs[signer.KeyID], publicKeys[signer.PublicKey] = true, true, true
		registry.bySigner[strideE10W6TrustedSignerKey(signer.Role, signer.SignerPersonID, signer.KeyID)] = signer
	}
	return registry, nil
}

func NewStrideE10W6Operator(authority StrideE10W6OperatorAuthority, trust *StrideE10W6SealedTrustRegistry, keys W6ManagedMACKeyring) (*StrideE10W6Operator, error) {
	if authority == nil || trust == nil || keys == nil {
		return nil, ErrStrideE10W6OperatorInvalid
	}
	return &StrideE10W6Operator{authority: authority, trust: trust, keys: keys}, nil
}

func NewStrideE10W6OperatorRequirementsPacket(policy W6NetworkPolicyRevision, releaseCommit, cohortID string, profiles []StrideE10W6ProposedProfile, rollback StrideE10W6RollbackBoundary, health StrideE10W6HealthBoundary, generatedAt, expiresAt time.Time) (StrideE10W6OperatorRequirementsPacket, error) {
	if policy.validateUnsigned() != nil || !releaseCommitPattern.MatchString(releaseCommit) || !containsSTRIDEString(policy.CohortIDs, cohortID) || len(profiles) != 5 || generatedAt.IsZero() || !expiresAt.After(generatedAt) || expiresAt.After(policy.ExpiresAt) || !validStrideE10W6RollbackBoundary(rollback, releaseCommit) || !validStrideE10W6HealthBoundary(health) {
		return StrideE10W6OperatorRequirementsPacket{}, ErrStrideE10W6OperatorInvalid
	}
	profiles = append([]StrideE10W6ProposedProfile(nil), profiles...)
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].PersonID < profiles[j].PersonID })
	seen := map[string]bool{}
	for _, profile := range profiles {
		if !validStrideE10W6ProposedProfile(profile) || seen[profile.PersonID] {
			return StrideE10W6OperatorRequirementsPacket{}, ErrStrideE10W6OperatorInvalid
		}
		seen[profile.PersonID] = true
	}
	packet := StrideE10W6OperatorRequirementsPacket{Schema: strideE10W6OperatorSchema, ReleaseCommit: releaseCommit, PolicyID: policy.PolicyID, PolicyRevision: policy.Revision, PolicyDigest: policy.MAC, DisclosurePolicyDigest: policy.DisclosurePolicyDigest, ProhibitedCorpusDigest: policy.ProhibitedCorpusDigest, CohortID: cohortID, Profiles: profiles, RequiredReviewerCount: 2, RequiredApprovalRoles: []string{"legal", "privacy", "product"}, RequiredActivationSwitches: append([]STRIDEFeature(nil), strideE10W6ActivationSwitches...), RequiredDisabledSwitches: append([]STRIDEFeature(nil), strideE10W6AlwaysDisabledSwitches...), Rollback: rollback, Health: health, GeneratedAt: generatedAt.UTC(), ExpiresAt: expiresAt.UTC()}
	digest, err := strideE10W6OperatorPacketDigest(packet)
	if err != nil {
		return StrideE10W6OperatorRequirementsPacket{}, ErrStrideE10W6OperatorInvalid
	}
	packet.PacketID = "w6_packet_" + digest[:24]
	return packet, nil
}

func (o *StrideE10W6Operator) VerifySubmission(ctx context.Context, packet StrideE10W6OperatorRequirementsPacket, submission StrideE10W6OperatorSubmission, at time.Time) (StrideE10W6OperatorVerificationReceipt, error) {
	if o == nil || o.authority == nil || o.trust == nil || o.keys == nil || ctx == nil || at.IsZero() || at.Before(packet.GeneratedAt) || !at.Before(packet.ExpiresAt) || !validStrideE10W6OperatorPacket(packet) || o.trust.snapshot.PolicyID != packet.PolicyID || o.trust.snapshot.PolicyRevision != packet.PolicyRevision || o.trust.snapshot.PolicyDigest != packet.PolicyDigest || o.trust.snapshot.CohortID != packet.CohortID || at.Before(o.trust.snapshot.IssuedAt) || !at.Before(o.trust.snapshot.ExpiresAt) {
		return StrideE10W6OperatorVerificationReceipt{}, ErrStrideE10W6OperatorInvalid
	}
	digest, err := strideE10W6OperatorPacketDigest(packet)
	if err != nil || submission.PacketDigest != digest || !verifyStrideE10W6ExternalSignatures(o.trust, packet, submission, digest, at) {
		return StrideE10W6OperatorVerificationReceipt{}, ErrStrideE10W6OperatorInvalid
	}
	trustDigest, err := STRIDEContractDigest(o.trust.snapshot)
	if err != nil {
		return StrideE10W6OperatorVerificationReceipt{}, ErrStrideE10W6OperatorInvalid
	}
	var verified bool
	var receipt StrideE10W6OperatorVerificationReceipt
	err = o.authority.WithCurrentStrideE10W6OperatorState(ctx, func(current StrideE10W6OperatorCurrentState) error {
		if !current.W4Ready || !current.ReconcileHealthy || !current.PurgeWorkerHealthy || current.ReleaseCommit != packet.ReleaseCommit || current.W4ActivationID != packet.Rollback.ActivationID || current.W4ReceiptDigest != packet.Rollback.ActivationReceiptDigest || current.W4Generation != packet.Rollback.SnapshotGeneration || current.SnapshotDigest != packet.Rollback.SnapshotDigest || current.SessionsDigest != packet.Rollback.SessionsDigest || current.RollbackVerifierDigest != packet.Rollback.RollbackVerifierDigest || current.PolicyID != packet.PolicyID || current.PolicyRevision != packet.PolicyRevision || current.PolicyDigest != packet.PolicyDigest || current.DisclosurePolicyDigest != packet.DisclosurePolicyDigest || current.ProhibitedCorpusDigest != packet.ProhibitedCorpusDigest || current.ShadowSnapshotDigest != packet.Health.ShadowSnapshotDigest || current.ReconcileDigest != packet.Health.ReconcileDigest || current.PurgeHealthDigest != packet.Health.PurgeHealthDigest || current.ShadowGeneration != packet.Health.ShadowGeneration || current.ShadowSnapshotRevision != packet.Health.SnapshotRevision || current.IndexedRevision != packet.Health.IndexedRevision || current.PurgeQueued+current.PurgeRunning+current.PurgeFailed != 0 || !sameStrideE10W6ProposedProfiles(current.Profiles, packet.Profiles) {
			return ErrStrideE10W6OperatorInvalid
		}
		for _, feature := range strideE10W6ActivationSwitches {
			enabled, known := current.FeatureEnabled[feature]
			if !known || enabled {
				return ErrStrideE10W6OperatorInvalid
			}
		}
		for _, feature := range strideE10W6AlwaysDisabledSwitches {
			enabled, known := current.FeatureEnabled[feature]
			if !known || enabled {
				return ErrStrideE10W6OperatorInvalid
			}
		}
		key, keyErr := o.keys.CurrentW6ManagedMACKey(ctx)
		if keyErr != nil || !strideIdentifier(key.ID) || key.Version == 0 || len(key.Secret) < 32 {
			return ErrStrideE10W6OperatorInvalid
		}
		receipt = StrideE10W6OperatorVerificationReceipt{Schema: "stride.e10.w6.operator-verification.v1", PacketID: packet.PacketID, PacketDigest: digest, ReleaseCommit: packet.ReleaseCommit, TrustRegistryID: o.trust.snapshot.RegistryID, TrustRegistryDigest: trustDigest, VerifiedAt: at.UTC(), ExpiresAt: packet.ExpiresAt, Status: "verified_for_external_activation", ActivationPerformed: false, KeyID: key.ID, KeyVersion: key.Version}
		payload, payloadErr := strideE10W6OperatorReceiptPayload(receipt)
		if payloadErr != nil {
			return ErrStrideE10W6OperatorInvalid
		}
		receipt.MAC = strideE10W6OperatorMAC(key.Secret, payload)
		verified = true
		return nil
	})
	if err != nil || !verified {
		return StrideE10W6OperatorVerificationReceipt{}, ErrStrideE10W6OperatorInvalid
	}
	return receipt, nil
}

func VerifyStrideE10W6OperatorVerificationReceipt(ctx context.Context, keys W6ManagedMACKeyring, receipt StrideE10W6OperatorVerificationReceipt, at time.Time) error {
	if ctx == nil || keys == nil || at.IsZero() || receipt.Schema != "stride.e10.w6.operator-verification.v1" || !strideIdentifier(receipt.PacketID) || !isHexDigest(receipt.PacketDigest) || !releaseCommitPattern.MatchString(receipt.ReleaseCommit) || !strideIdentifier(receipt.TrustRegistryID) || !isHexDigest(receipt.TrustRegistryDigest) || receipt.VerifiedAt.IsZero() || !receipt.ExpiresAt.After(receipt.VerifiedAt) || at.Before(receipt.VerifiedAt) || !at.Before(receipt.ExpiresAt) || receipt.Status != "verified_for_external_activation" || receipt.ActivationPerformed || !strideIdentifier(receipt.KeyID) || receipt.KeyVersion == 0 || !isHexDigest(receipt.MAC) {
		return ErrStrideE10W6OperatorInvalid
	}
	key, err := keys.ResolveW6ManagedMACKey(ctx, receipt.KeyID, receipt.KeyVersion)
	if err != nil || key.ID != receipt.KeyID || key.Version != receipt.KeyVersion || len(key.Secret) < 32 {
		return ErrStrideE10W6OperatorInvalid
	}
	payload, err := strideE10W6OperatorReceiptPayload(receipt)
	if err != nil || !verifyStrideE10W6OperatorMAC(key.Secret, payload, receipt.MAC) {
		return ErrStrideE10W6OperatorInvalid
	}
	return nil
}

func strideE10W6OperatorPacketDigest(packet StrideE10W6OperatorRequirementsPacket) (string, error) {
	packet.PacketID = ""
	return STRIDEContractDigest(struct {
		Domain string
		Packet StrideE10W6OperatorRequirementsPacket
	}{"stride.e10.w6.operator.packet.v1", packet})
}

func validStrideE10W6ProposedProfile(v StrideE10W6ProposedProfile) bool {
	return strideIdentifier(v.PersonID) && v.Profile.Validate() == nil && v.Profile.ContractType == STRIDEContractNetworkProfileProjection && v.Publication.Validate() == nil && v.Publication.ContractType == STRIDEContractPublishedContributionClaim && v.AttestationCount > 0
}

func validStrideE10W6RollbackBoundary(v StrideE10W6RollbackBoundary, releaseCommit string) bool {
	if !isHexDigest(v.ActivationID) || !isHexDigest(v.ActivationReceiptDigest) || v.ReleaseCommit != releaseCommit || v.SnapshotGeneration == 0 || !isHexDigest(v.SnapshotDigest) || !isHexDigest(v.SessionsDigest) || !isHexDigest(v.RollbackVerifierDigest) || len(v.KillSwitches) != len(strideE10W6ActivationSwitches) {
		return false
	}
	for i := range strideE10W6ActivationSwitches {
		if v.KillSwitches[i] != strideE10W6ActivationSwitches[i] {
			return false
		}
	}
	return true
}

func validStrideE10W6HealthBoundary(v StrideE10W6HealthBoundary) bool {
	return isHexDigest(v.ShadowSnapshotDigest) && isHexDigest(v.ReconcileDigest) && isHexDigest(v.PurgeHealthDigest) && v.ShadowGeneration > 0 && v.SnapshotRevision > 0 && v.SnapshotRevision == v.IndexedRevision
}

func validStrideE10W6OperatorPacket(packet StrideE10W6OperatorRequirementsPacket) bool {
	if packet.Schema != strideE10W6OperatorSchema || !releaseCommitPattern.MatchString(packet.ReleaseCommit) || !strideIdentifier(packet.PolicyID) || packet.PolicyRevision < 1 || !isHexDigest(packet.PolicyDigest) || !isHexDigest(packet.DisclosurePolicyDigest) || !isHexDigest(packet.ProhibitedCorpusDigest) || !strideIdentifier(packet.CohortID) || packet.RequiredReviewerCount != 2 || len(packet.Profiles) != 5 || !packet.ExpiresAt.After(packet.GeneratedAt) || !validStrideE10W6RollbackBoundary(packet.Rollback, packet.ReleaseCommit) || !validStrideE10W6HealthBoundary(packet.Health) {
		return false
	}
	if len(packet.RequiredApprovalRoles) != 3 || packet.RequiredApprovalRoles[0] != "legal" || packet.RequiredApprovalRoles[1] != "privacy" || packet.RequiredApprovalRoles[2] != "product" || len(packet.RequiredActivationSwitches) != len(strideE10W6ActivationSwitches) || len(packet.RequiredDisabledSwitches) != len(strideE10W6AlwaysDisabledSwitches) {
		return false
	}
	seen := map[string]bool{}
	for index, profile := range packet.Profiles {
		if !validStrideE10W6ProposedProfile(profile) || seen[profile.PersonID] {
			return false
		}
		if index > 0 && packet.Profiles[index-1].PersonID >= profile.PersonID {
			return false
		}
		seen[profile.PersonID] = true
	}
	for i := range strideE10W6ActivationSwitches {
		if packet.RequiredActivationSwitches[i] != strideE10W6ActivationSwitches[i] {
			return false
		}
	}
	for i := range strideE10W6AlwaysDisabledSwitches {
		if packet.RequiredDisabledSwitches[i] != strideE10W6AlwaysDisabledSwitches[i] {
			return false
		}
	}
	digest, err := strideE10W6OperatorPacketDigest(packet)
	return err == nil && packet.PacketID == "w6_packet_"+digest[:24]
}

func verifyStrideE10W6ExternalSignatures(trust *StrideE10W6SealedTrustRegistry, packet StrideE10W6OperatorRequirementsPacket, submission StrideE10W6OperatorSubmission, digest string, at time.Time) bool {
	keys, publicKeys, people := map[string]bool{}, map[string]bool{}, map[string]bool{}
	roles := map[string]bool{}
	for _, approval := range submission.Approvals {
		if !trust.matches(approval.Role, approval.SignerPersonID, approval.KeyID, approval.PublicKey) || !validStrideE10W6SignatureTime(packet, approval.SignedAt, at) || !containsSTRIDEString(packet.RequiredApprovalRoles, approval.Role) || roles[approval.Role] || !verifyStrideE10W6Detached(approval, "approval", digest, approval.Role) || keys[approval.KeyID] || publicKeys[approval.PublicKey] || people[approval.SignerPersonID] {
			return false
		}
		roles[approval.Role], keys[approval.KeyID], publicKeys[approval.PublicKey], people[approval.SignerPersonID] = true, true, true, true
	}
	if len(roles) != len(packet.RequiredApprovalRoles) || len(submission.Consents) != len(packet.Profiles) || len(submission.Reviewers) != packet.RequiredReviewerCount {
		return false
	}
	profileByPerson := map[string]StrideE10W6ProposedProfile{}
	for _, profile := range packet.Profiles {
		profileByPerson[profile.PersonID] = profile
	}
	for _, consent := range submission.Consents {
		want, ok := profileByPerson[consent.Profile.PersonID]
		profileDigest, _ := STRIDEContractDigest(consent.Profile)
		if !ok || consent.Profile != want || !trust.matches("profile_consent", consent.Signature.SignerPersonID, consent.Signature.KeyID, consent.Signature.PublicKey) || !validStrideE10W6SignatureTime(packet, consent.Signature.SignedAt, at) || consent.Signature.Role != "profile_consent" || consent.Signature.SignerPersonID != want.PersonID || !verifyStrideE10W6Detached(consent.Signature, "consent", digest, profileDigest) || keys[consent.Signature.KeyID] || publicKeys[consent.Signature.PublicKey] || people[consent.Signature.SignerPersonID] {
			return false
		}
		delete(profileByPerson, want.PersonID)
		keys[consent.Signature.KeyID], publicKeys[consent.Signature.PublicKey], people[consent.Signature.SignerPersonID] = true, true, true
	}
	for _, reviewer := range submission.Reviewers {
		reviewSubject, reviewErr := STRIDEContractDigest(struct {
			CorpusDigest   string
			ReviewRevision int64
			SignedAt       time.Time
		}{packet.ProhibitedCorpusDigest, reviewer.ReviewRevision, reviewer.SignedAt.UTC()})
		if reviewErr != nil || !trust.matches("reviewer", reviewer.SignerPersonID, reviewer.KeyID, reviewer.PublicKey) || reviewer.ReviewRevision < 1 || !validStrideE10W6SignatureTime(packet, reviewer.SignedAt, at) || keys[reviewer.KeyID] || publicKeys[reviewer.PublicKey] || people[reviewer.SignerPersonID] || !verifyStrideE10W6RawSignature(reviewer.PublicKey, reviewer.Signature, reviewer.KeyID, reviewer.SignerPersonID, "reviewer", digest, reviewSubject) {
			return false
		}
		keys[reviewer.KeyID], publicKeys[reviewer.PublicKey], people[reviewer.SignerPersonID] = true, true, true
	}
	return len(profileByPerson) == 0
}

func verifyStrideE10W6Detached(v StrideE10W6DetachedSignature, domain, packetDigest, subject string) bool {
	signedSubject, err := STRIDEContractDigest(struct {
		Subject  string
		SignedAt time.Time
	}{subject, v.SignedAt.UTC()})
	return err == nil && verifyStrideE10W6RawSignature(v.PublicKey, v.Signature, v.KeyID, v.SignerPersonID, domain, packetDigest, signedSubject)
}

func validStrideE10W6SignatureTime(packet StrideE10W6OperatorRequirementsPacket, signedAt, at time.Time) bool {
	return !signedAt.Before(packet.GeneratedAt) && signedAt.Before(packet.ExpiresAt) && !signedAt.After(at)
}

func verifyStrideE10W6RawSignature(publicValue, signatureValue, keyID, personID, domain, packetDigest, subject string) bool {
	publicKey, publicErr := base64.StdEncoding.DecodeString(publicValue)
	signature, signatureErr := base64.StdEncoding.DecodeString(signatureValue)
	payload, payloadErr := STRIDEContractDigest(struct{ Domain, PacketDigest, Subject, KeyID, PersonID string }{domain, packetDigest, subject, keyID, personID})
	return publicErr == nil && signatureErr == nil && payloadErr == nil && strideIdentifier(keyID) && strideIdentifier(personID) && len(publicKey) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(publicKey), []byte(payload), signature)
}

func sameStrideE10W6ProposedProfiles(left, right []StrideE10W6ProposedProfile) bool {
	left = append([]StrideE10W6ProposedProfile(nil), left...)
	right = append([]StrideE10W6ProposedProfile(nil), right...)
	sort.Slice(left, func(i, j int) bool { return left[i].PersonID < left[j].PersonID })
	sort.Slice(right, func(i, j int) bool { return right[i].PersonID < right[j].PersonID })
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (r *StrideE10W6SealedTrustRegistry) matches(role, personID, keyID, publicKey string) bool {
	if r == nil {
		return false
	}
	want, ok := r.bySigner[strideE10W6TrustedSignerKey(role, personID, keyID)]
	return ok && want.PublicKey == publicKey
}

func validStrideE10W6TrustRole(role string) bool {
	return role == "legal" || role == "privacy" || role == "product" || role == "profile_consent" || role == "reviewer"
}

func strideE10W6TrustedSignerKey(role, personID, keyID string) string {
	return role + "\x00" + personID + "\x00" + keyID
}

func strideE10W6TrustRegistryPayload(snapshot StrideE10W6TrustRegistrySnapshot) ([]byte, error) {
	snapshot.MAC = ""
	snapshot.Signers = append([]StrideE10W6TrustedSigner(nil), snapshot.Signers...)
	sort.Slice(snapshot.Signers, func(i, j int) bool {
		if snapshot.Signers[i].Role != snapshot.Signers[j].Role {
			return snapshot.Signers[i].Role < snapshot.Signers[j].Role
		}
		return snapshot.Signers[i].SignerPersonID < snapshot.Signers[j].SignerPersonID
	})
	return json.Marshal(struct {
		Domain   string                           `json:"domain"`
		Registry StrideE10W6TrustRegistrySnapshot `json:"registry"`
	}{"stride.e10.w6.trust-registry.v1", snapshot})
}

func strideE10W6OperatorReceiptPayload(receipt StrideE10W6OperatorVerificationReceipt) ([]byte, error) {
	receipt.MAC = ""
	return json.Marshal(struct {
		Domain  string                                 `json:"domain"`
		Receipt StrideE10W6OperatorVerificationReceipt `json:"receipt"`
	}{"stride.e10.w6.operator-verification.v1", receipt})
}

func strideE10W6OperatorMAC(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func verifyStrideE10W6OperatorMAC(secret, payload []byte, value string) bool {
	want, err := hex.DecodeString(value)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return hmac.Equal(want, mac.Sum(nil))
}
