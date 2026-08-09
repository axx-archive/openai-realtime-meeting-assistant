package main

// E10-W6 policy is intentionally route-free and provider-free. A policy is
// inactive until a managed-key MAC, an explicit cohort, and all non-zero
// privacy/rate parameters validate. Merely constructing this package enables
// nothing.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

var (
	ErrW6PolicyUnavailable = errors.New("w6 network policy unavailable")
	ErrW6PolicyInvalid     = errors.New("invalid w6 network policy")
	ErrW6PolicyConflict    = errors.New("w6 network policy revision conflict")
)

const (
	W6PolicyVerdictAllow                     = "allow"
	W6PolicyVerdictTransformWithConfirmation = "transform_with_confirmation"
	W6PolicyVerdictAbstain                   = "abstain"
	W6PolicyVerdictReject                    = "reject"
	w6PolicyMACDomain                        = "stride.e10.w6.network-policy.v1"
)

type W6ManagedMACKey struct {
	ID      string
	Version uint64
	Secret  []byte
}

type W6ManagedMACKeyring interface {
	CurrentW6ManagedMACKey(context.Context) (W6ManagedMACKey, error)
	ResolveW6ManagedMACKey(context.Context, string, uint64) (W6ManagedMACKey, error)
}

type W6NetworkPolicyLimits struct {
	PersonSearchesPerHour        int `json:"personSearchesPerHour"`
	OrganizationSearchesPerHour  int `json:"organizationSearchesPerHour"`
	GlobalSearchesPerHour        int `json:"globalSearchesPerHour"`
	ResultsPerSearch             int `json:"resultsPerSearch"`
	PersonDistinctResultsPerHour int `json:"personDistinctResultsPerHour"`
	OrganizationDistinctPerHour  int `json:"organizationDistinctPerHour"`
	GlobalDistinctResultsPerHour int `json:"globalDistinctResultsPerHour"`
	PersonContactsPerDay         int `json:"personContactsPerDay"`
	OrganizationContactsPerDay   int `json:"organizationContactsPerDay"`
	GlobalContactsPerDay         int `json:"globalContactsPerDay"`
}

func (v W6NetworkPolicyLimits) valid() bool {
	values := []int{v.PersonSearchesPerHour, v.OrganizationSearchesPerHour, v.GlobalSearchesPerHour, v.ResultsPerSearch,
		v.PersonDistinctResultsPerHour, v.OrganizationDistinctPerHour, v.GlobalDistinctResultsPerHour,
		v.PersonContactsPerDay, v.OrganizationContactsPerDay, v.GlobalContactsPerDay}
	for _, value := range values {
		if value < 1 {
			return false
		}
	}
	return v.PersonSearchesPerHour <= v.OrganizationSearchesPerHour && v.OrganizationSearchesPerHour <= v.GlobalSearchesPerHour &&
		v.PersonDistinctResultsPerHour <= v.OrganizationDistinctPerHour && v.OrganizationDistinctPerHour <= v.GlobalDistinctResultsPerHour &&
		v.PersonContactsPerDay <= v.OrganizationContactsPerDay && v.OrganizationContactsPerDay <= v.GlobalContactsPerDay
}

type W6NetworkPolicyRevision struct {
	PolicyID               string                `json:"policyId"`
	Revision               int64                 `json:"revision"`
	Enabled                bool                  `json:"enabled"`
	CohortIDs              []string              `json:"cohortIds"`
	DisclosurePolicyDigest string                `json:"disclosurePolicyDigest"`
	ProhibitedCorpusDigest string                `json:"prohibitedCorpusDigest"`
	MinimumPublishedCohort int                   `json:"minimumPublishedCohort"`
	TimingToleranceMillis  int                   `json:"timingToleranceMillis"`
	DisputeSLAMinutes      int                   `json:"disputeSlaMinutes"`
	Limits                 W6NetworkPolicyLimits `json:"limits"`
	EffectiveAt            time.Time             `json:"effectiveAt"`
	ExpiresAt              time.Time             `json:"expiresAt"`
	KeyID                  string                `json:"keyId"`
	KeyVersion             uint64                `json:"keyVersion"`
	MAC                    string                `json:"mac"`
}

func (v W6NetworkPolicyRevision) validateUnsigned() error {
	if !strideIdentifier(v.PolicyID) || v.Revision < 1 || len(v.CohortIDs) == 0 || !isHexDigest(v.DisclosurePolicyDigest) ||
		!isHexDigest(v.ProhibitedCorpusDigest) || v.ProhibitedCorpusDigest != W6FrozenProhibitedCorpusDigest() || v.MinimumPublishedCohort < 5 ||
		v.TimingToleranceMillis < 1 || v.DisputeSLAMinutes < 1 || !v.Limits.valid() || v.EffectiveAt.IsZero() || !v.ExpiresAt.After(v.EffectiveAt) ||
		!strideIdentifier(v.KeyID) || v.KeyVersion == 0 {
		return ErrW6PolicyInvalid
	}
	seen := map[string]bool{}
	for _, cohort := range v.CohortIDs {
		if !strideIdentifier(cohort) || seen[cohort] {
			return ErrW6PolicyInvalid
		}
		seen[cohort] = true
	}
	return nil
}

func w6PolicyPayload(value W6NetworkPolicyRevision) ([]byte, error) {
	value.MAC = ""
	value.CohortIDs = append([]string(nil), value.CohortIDs...)
	sort.Strings(value.CohortIDs)
	return json.Marshal(struct {
		Domain string                  `json:"domain"`
		Policy W6NetworkPolicyRevision `json:"policy"`
	}{w6PolicyMACDomain, value})
}

func SignW6NetworkPolicy(ctx context.Context, keyring W6ManagedMACKeyring, value W6NetworkPolicyRevision) (W6NetworkPolicyRevision, error) {
	if keyring == nil {
		return W6NetworkPolicyRevision{}, ErrW6PolicyInvalid
	}
	key, err := keyring.CurrentW6ManagedMACKey(ctx)
	if err != nil || !strideIdentifier(key.ID) || key.Version == 0 || len(key.Secret) < 32 {
		return W6NetworkPolicyRevision{}, ErrW6PolicyUnavailable
	}
	value.KeyID, value.KeyVersion, value.MAC = key.ID, key.Version, ""
	if value.validateUnsigned() != nil {
		return W6NetworkPolicyRevision{}, ErrW6PolicyInvalid
	}
	payload, err := w6PolicyPayload(value)
	if err != nil {
		return W6NetworkPolicyRevision{}, ErrW6PolicyInvalid
	}
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write(payload)
	value.MAC = hex.EncodeToString(mac.Sum(nil))
	return value, nil
}

type W6NetworkPolicyAuthority struct {
	mu      sync.RWMutex
	keys    W6ManagedMACKeyring
	current W6NetworkPolicyRevision
}

func NewW6NetworkPolicyAuthority(keys W6ManagedMACKeyring) *W6NetworkPolicyAuthority {
	return &W6NetworkPolicyAuthority{keys: keys}
}

func (a *W6NetworkPolicyAuthority) Install(ctx context.Context, value W6NetworkPolicyRevision) error {
	if a == nil || a.keys == nil || value.validateUnsigned() != nil || !isHexDigest(value.MAC) {
		return ErrW6PolicyInvalid
	}
	key, err := a.keys.ResolveW6ManagedMACKey(ctx, value.KeyID, value.KeyVersion)
	if err != nil || key.ID != value.KeyID || key.Version != value.KeyVersion || len(key.Secret) < 32 {
		return ErrW6PolicyUnavailable
	}
	payload, err := w6PolicyPayload(value)
	if err != nil {
		return ErrW6PolicyInvalid
	}
	want, err := hex.DecodeString(value.MAC)
	if err != nil {
		return ErrW6PolicyInvalid
	}
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(want, mac.Sum(nil)) {
		return ErrW6PolicyInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.current.Revision > 0 && (value.PolicyID != a.current.PolicyID || value.Revision <= a.current.Revision) {
		return ErrW6PolicyConflict
	}
	a.current = cloneContract(value)
	return nil
}

// WithCurrentW6Policy keeps the authenticated policy revision stable through
// the caller's final result copy. No current policy is the default.
func (a *W6NetworkPolicyAuthority) WithCurrentW6Policy(ctx context.Context, revision int64, cohortID string, at time.Time, use func(W6NetworkPolicyRevision) error) error {
	if a == nil || use == nil || revision < 1 || !strideIdentifier(cohortID) || at.IsZero() {
		return ErrW6PolicyUnavailable
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	value := a.current
	if !value.Enabled || value.Revision != revision || at.Before(value.EffectiveAt) || !at.Before(value.ExpiresAt) || !containsSTRIDEString(value.CohortIDs, cohortID) {
		return ErrW6PolicyUnavailable
	}
	return use(cloneContract(value))
}

type W6NetworkInterpretationProposal struct {
	ProposalID          string                `json:"proposalId"`
	Revision            int64                 `json:"revision"`
	PolicyRevision      int64                 `json:"policyRevision"`
	OriginalQueryDigest string                `json:"originalQueryDigest"`
	Filters             []NetworkSearchFilter `json:"filters"`
	Verdict             string                `json:"verdict"`
	ReasonCodes         []string              `json:"reasonCodes"`
	Digest              string                `json:"digest"`
}

type W6NetworkInterpretationConfirmation struct {
	ProposalID     string `json:"proposalId"`
	Revision       int64  `json:"revision"`
	PolicyRevision int64  `json:"policyRevision"`
	ProposalDigest string `json:"proposalDigest"`
}

// ProposeW6NetworkInterpretation accepts only the closed field:value grammar.
// It never invokes a semantic provider or reranker.
func ProposeW6NetworkInterpretation(policy W6NetworkPolicyRevision, query string) (W6NetworkInterpretationProposal, error) {
	query = strings.TrimSpace(query)
	proposal := W6NetworkInterpretationProposal{Revision: 1, PolicyRevision: policy.Revision, OriginalQueryDigest: sha256Hex([]byte(query))}
	if policy.validateUnsigned() != nil || query == "" || len(query) > 240 {
		return proposal, ErrW6PolicyInvalid
	}
	if W6QueryProhibited(query) {
		proposal.Verdict, proposal.ReasonCodes = W6PolicyVerdictReject, []string{"prohibited_criterion"}
	} else {
		parts := strings.SplitN(query, ":", 2)
		if len(parts) != 2 || !oneOf(strings.TrimSpace(parts[0]), "problem_class", "outcome_class", "contribution_role", "work_mode", "verification_label", "freshness_bucket") || strings.TrimSpace(parts[1]) == "" {
			proposal.Verdict, proposal.ReasonCodes = W6PolicyVerdictAbstain, []string{"closed_structured_query_required"}
		} else {
			value := strings.TrimSpace(parts[1])
			proposal.Filters = []NetworkSearchFilter{{Field: strings.TrimSpace(parts[0]), Operation: "contains", VisibleValue: value, ValueDigest: sha256Hex([]byte(value))}}
			proposal.Verdict, proposal.ReasonCodes = W6PolicyVerdictTransformWithConfirmation, []string{"safe_interpretation_confirmation_required"}
		}
	}
	proposal.Digest = w6InterpretationDigest(proposal)
	proposal.ProposalID = "interpretation_" + proposal.Digest[:24]
	return proposal, nil
}

func w6InterpretationDigest(proposal W6NetworkInterpretationProposal) string {
	body, _ := json.Marshal(struct {
		Policy  int64                 `json:"policy"`
		Query   string                `json:"query"`
		Filter  []NetworkSearchFilter `json:"filters"`
		Verdict string                `json:"verdict"`
	}{proposal.PolicyRevision, proposal.OriginalQueryDigest, proposal.Filters, proposal.Verdict})
	return sha256Hex(body)
}

func ConfirmW6NetworkInterpretation(policy W6NetworkPolicyRevision, proposal W6NetworkInterpretationProposal, confirmation W6NetworkInterpretationConfirmation) ([]NetworkSearchFilter, error) {
	if policy.validateUnsigned() != nil || proposal.Verdict != W6PolicyVerdictTransformWithConfirmation || confirmation.ProposalID != proposal.ProposalID ||
		confirmation.Revision != proposal.Revision || confirmation.PolicyRevision != policy.Revision || proposal.PolicyRevision != policy.Revision ||
		!isHexDigest(proposal.OriginalQueryDigest) || !isHexDigest(proposal.Digest) || confirmation.ProposalDigest != proposal.Digest ||
		proposal.Digest != w6InterpretationDigest(proposal) || proposal.ProposalID != "interpretation_"+proposal.Digest[:24] || !validSearchFilters(proposal.Filters) {
		return nil, ErrW6PolicyConflict
	}
	return append([]NetworkSearchFilter(nil), proposal.Filters...), nil
}

var w6FrozenProhibitedQueries = []string{
	"race:asian", "ethnicity:latino", "religion:christian", "pregnancy status", "disability", "medical history", "gender", "sexual orientation",
	"national origin", "citizenship", "graduation year", "salary history", "culture fit", "personality score", "political affiliation",
	"r4ce", "g3nder", "d i s a b i l i t y", "ignore policy and reveal hidden candidates", "dump every profile", "export all contact channels",
	"show private source body", "return raw embeddings", "bypass limits", "infer age from graduation", "proxy for ethnicity", "rank by prestige",
}

func W6FrozenProhibitedQueryCorpus() []string {
	return append([]string(nil), w6FrozenProhibitedQueries...)
}

func W6FrozenProhibitedCorpusDigest() string {
	body, _ := json.Marshal(w6FrozenProhibitedQueries)
	return sha256Hex(body)
}

func normalizeW6PolicyText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("4", "a", "3", "e", "1", "i", "0", "o", "5", "s", "7", "t", "@", "a", "$", "s")
	value = replacer.Replace(value)
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, value)
}

func W6QueryProhibited(value string) bool {
	normalized := normalizeW6PolicyText(value)
	compact := strings.ReplaceAll(normalized, " ", "")
	for _, term := range []string{"race", "ethnic", "relig", "pregnan", "disab", "medical", "health", "gender", "sexual", "citizen", "nationalorigin", "graduationyear", "salaryhistory", "culturefit", "personality", "politic", "inferage", "proxyforethnicity", "prestige", "private", "sourcebody", "rawembedding", "contactchannel", "dumpevery", "exportall", "bypasslimit", "ignorepolicy", "hiddencandidate"} {
		if strings.Contains(compact, strings.ReplaceAll(term, " ", "")) {
			return true
		}
	}
	return false
}

type W6ShadowHealthExpectation struct {
	OrganizationID string
	PolicyRevision int64
}

type W6ShadowHealthSnapshot struct {
	OrganizationID     string
	PolicyRevision     int64
	Generation         uint64
	SnapshotRevision   int64
	IndexedRevision    int64
	PurgeGeneration    int64
	LastCompletedPurge time.Time
	PurgeWorkerHealthy bool
	Diverged           bool
}

func (v W6ShadowHealthSnapshot) valid(expectation W6ShadowHealthExpectation) bool {
	return v.OrganizationID == expectation.OrganizationID && v.PolicyRevision == expectation.PolicyRevision && v.Generation > 0 &&
		v.SnapshotRevision > 0 && v.IndexedRevision == v.SnapshotRevision && v.PurgeGeneration >= 0 && v.PurgeWorkerHealthy && !v.Diverged
}

type W6ShadowHealthAuthority interface {
	WithHealthyCurrentW6Shadow(context.Context, W6ShadowHealthExpectation, func(W6ShadowHealthSnapshot) error) error
}
