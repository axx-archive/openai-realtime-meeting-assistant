package main

// W6 qualification receipts prove a bounded, consenting, human-reviewed
// corpus. They do not activate publication, search, contact, or a provider.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

var ErrW6QualificationInvalid = errors.New("invalid w6 qualification receipt")

const w6QualificationMACDomain = "stride.e10.w6.qualification.v1"

type W6ConsentedProfileQualification struct {
	PersonID string `json:"personId"`
	// ConsentRevision is signed reviewer evidence only. It is deliberately not
	// runtime-authorizing until a current consent authority resolver exists;
	// profile/publication/attestation authority remains independently current.
	ConsentRevision        int64           `json:"consentRevision"`
	Profile                STRIDEReference `json:"profile"`
	Publication            STRIDEReference `json:"publication"`
	AttestationCount       int             `json:"attestationCount"`
	DisclosurePolicyDigest string          `json:"disclosurePolicyDigest"`
}

type W6ReviewerQualification struct {
	PersonID       string `json:"personId"`
	ReviewRevision int64  `json:"reviewRevision"`
	CorpusDigest   string `json:"corpusDigest"`
	Passed         bool   `json:"passed"`
}

type W6NetworkQualificationReceipt struct {
	ReceiptID              string                            `json:"receiptId"`
	Revision               int64                             `json:"revision"`
	PolicyID               string                            `json:"policyId"`
	PolicyRevision         int64                             `json:"policyRevision"`
	CohortID               string                            `json:"cohortId"`
	Profiles               []W6ConsentedProfileQualification `json:"profiles"`
	Reviewers              []W6ReviewerQualification         `json:"reviewers"`
	ProhibitedCorpusDigest string                            `json:"prohibitedCorpusDigest"`
	ProviderUsed           bool                              `json:"providerUsed"`
	QualifiedAt            time.Time                         `json:"qualifiedAt"`
	ExpiresAt              time.Time                         `json:"expiresAt"`
	KeyID                  string                            `json:"keyId"`
	KeyVersion             uint64                            `json:"keyVersion"`
	MAC                    string                            `json:"mac"`
}

func (v W6NetworkQualificationReceipt) validateUnsigned(policy W6NetworkPolicyRevision) error {
	if !strideIdentifier(v.ReceiptID) || v.Revision < 1 || v.PolicyID != policy.PolicyID || v.PolicyRevision != policy.Revision ||
		!containsSTRIDEString(policy.CohortIDs, v.CohortID) || len(v.Profiles) < policy.MinimumPublishedCohort || len(v.Profiles) < 5 || len(v.Reviewers) < 2 ||
		v.ProhibitedCorpusDigest != policy.ProhibitedCorpusDigest || v.ProviderUsed || v.QualifiedAt.IsZero() || !v.ExpiresAt.After(v.QualifiedAt) ||
		v.ExpiresAt.After(policy.ExpiresAt) || !strideIdentifier(v.KeyID) || v.KeyVersion == 0 {
		return ErrW6QualificationInvalid
	}
	people := map[string]bool{}
	for _, profile := range v.Profiles {
		if !strideIdentifier(profile.PersonID) || people[profile.PersonID] || profile.ConsentRevision < 1 || profile.Profile.Validate() != nil ||
			profile.Profile.ContractType != STRIDEContractNetworkProfileProjection || profile.Publication.Validate() != nil ||
			profile.Publication.ContractType != STRIDEContractPublishedContributionClaim || profile.AttestationCount < 1 || profile.DisclosurePolicyDigest != policy.DisclosurePolicyDigest {
			return ErrW6QualificationInvalid
		}
		people[profile.PersonID] = true
	}
	reviewers := map[string]bool{}
	for _, reviewer := range v.Reviewers {
		if !strideIdentifier(reviewer.PersonID) || reviewers[reviewer.PersonID] || reviewer.ReviewRevision < 1 || reviewer.CorpusDigest != policy.ProhibitedCorpusDigest || !reviewer.Passed {
			return ErrW6QualificationInvalid
		}
		reviewers[reviewer.PersonID] = true
	}
	return nil
}

func w6QualificationPayload(value W6NetworkQualificationReceipt) ([]byte, error) {
	value.MAC = ""
	value.Profiles = append([]W6ConsentedProfileQualification(nil), value.Profiles...)
	value.Reviewers = append([]W6ReviewerQualification(nil), value.Reviewers...)
	sort.Slice(value.Profiles, func(i, j int) bool { return value.Profiles[i].PersonID < value.Profiles[j].PersonID })
	sort.Slice(value.Reviewers, func(i, j int) bool { return value.Reviewers[i].PersonID < value.Reviewers[j].PersonID })
	return json.Marshal(struct {
		Domain  string                        `json:"domain"`
		Receipt W6NetworkQualificationReceipt `json:"receipt"`
	}{w6QualificationMACDomain, value})
}

func SignW6NetworkQualification(ctx context.Context, keys W6ManagedMACKeyring, policy W6NetworkPolicyRevision, value W6NetworkQualificationReceipt) (W6NetworkQualificationReceipt, error) {
	if keys == nil {
		return W6NetworkQualificationReceipt{}, ErrW6QualificationInvalid
	}
	key, err := keys.CurrentW6ManagedMACKey(ctx)
	if err != nil || !strideIdentifier(key.ID) || key.Version == 0 || len(key.Secret) < 32 {
		return W6NetworkQualificationReceipt{}, ErrW6QualificationInvalid
	}
	value.KeyID, value.KeyVersion, value.MAC = key.ID, key.Version, ""
	if value.validateUnsigned(policy) != nil {
		return W6NetworkQualificationReceipt{}, ErrW6QualificationInvalid
	}
	payload, err := w6QualificationPayload(value)
	if err != nil {
		return W6NetworkQualificationReceipt{}, ErrW6QualificationInvalid
	}
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write(payload)
	value.MAC = hex.EncodeToString(mac.Sum(nil))
	return value, nil
}

func VerifyW6NetworkQualification(ctx context.Context, keys W6ManagedMACKeyring, policy W6NetworkPolicyRevision, value W6NetworkQualificationReceipt, at time.Time) error {
	if keys == nil || value.validateUnsigned(policy) != nil || !isHexDigest(value.MAC) || at.Before(value.QualifiedAt) || !at.Before(value.ExpiresAt) {
		return ErrW6QualificationInvalid
	}
	key, err := keys.ResolveW6ManagedMACKey(ctx, value.KeyID, value.KeyVersion)
	if err != nil || key.ID != value.KeyID || key.Version != value.KeyVersion || len(key.Secret) < 32 {
		return ErrW6QualificationInvalid
	}
	payload, err := w6QualificationPayload(value)
	if err != nil {
		return ErrW6QualificationInvalid
	}
	want, err := hex.DecodeString(value.MAC)
	if err != nil {
		return ErrW6QualificationInvalid
	}
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(want, mac.Sum(nil)) {
		return ErrW6QualificationInvalid
	}
	return nil
}

type W6NetworkQualificationAuthority struct {
	mu      sync.RWMutex
	keys    W6ManagedMACKeyring
	current W6NetworkQualificationReceipt
}

func NewW6NetworkQualificationAuthority(keys W6ManagedMACKeyring) *W6NetworkQualificationAuthority {
	return &W6NetworkQualificationAuthority{keys: keys}
}

func (a *W6NetworkQualificationAuthority) Install(ctx context.Context, policy W6NetworkPolicyRevision, value W6NetworkQualificationReceipt, at time.Time) error {
	if a == nil || VerifyW6NetworkQualification(ctx, a.keys, policy, value, at) != nil {
		return ErrW6QualificationInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.current.Revision > 0 && (value.ReceiptID != a.current.ReceiptID || value.Revision <= a.current.Revision) {
		return ErrW6QualificationInvalid
	}
	a.current = cloneContract(value)
	return nil
}

// WithCurrentW6Qualification holds the exact authenticated qualification
// through final use. It proves evidence readiness, never feature activation.
func (a *W6NetworkQualificationAuthority) WithCurrentW6Qualification(ctx context.Context, policy W6NetworkPolicyRevision, cohortID string, at time.Time, use func(W6NetworkQualificationReceipt) error) error {
	if a == nil || use == nil {
		return ErrW6QualificationInvalid
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	value := a.current
	if value.PolicyID != policy.PolicyID || value.PolicyRevision != policy.Revision || value.CohortID != cohortID || VerifyW6NetworkQualification(ctx, a.keys, policy, value, at) != nil {
		return ErrW6QualificationInvalid
	}
	return use(cloneContract(value))
}
