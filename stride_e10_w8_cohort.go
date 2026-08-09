package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const strideE10W8ReceiptSchema = "stride.e10.w8.signed-receipt.v1"

var ErrStrideE10W8NotReady = errors.New("stride e10 w8 activation not ready")

var strideE10W8CohortOrder = []string{
	"organization_profile_private",
	"contribution_work_record_private",
	"network_publication",
	"evidence_search",
	"network_contact",
}

var strideE10W8CohortFeatures = map[string][]string{
	"organization_profile_private":     {"active_organization_session", "organization_authority_read", "organization_authority_write", "person_profile_authority"},
	"contribution_work_record_private": {"contribution_review", "work_record_private"},
	"network_publication":              {"network_profile_publication"},
	"evidence_search":                  {"network_projection_shadow", "network_search"},
	"network_contact":                  {"network_contact"},
}

type StrideE10W8SignedReceipt struct {
	Schema     string          `json:"schema"`
	Kind       string          `json:"kind"`
	KeyID      string          `json:"keyId"`
	ObservedAt time.Time       `json:"observedAt"`
	Payload    json.RawMessage `json:"payload"`
	Signature  string          `json:"signature"`
}

type StrideE10W8TrustController interface {
	ResolveStrideE10W8Trust(string) (StrideE10W7TrustedKey, error)
	StrideE10W8TrustPolicyDigest() string
}

const strideE10W8CompiledRootKeyID = "stride-e10-w8-root-2026-08"
const strideE10W8ProductionRootPublicKey = "xa6emyM6Mg/WrzVIPa8ou99kiNlUbbIjR7X8djjKIpU="

type StrideE10W8RootPolicy struct {
	Schema         string                           `json:"schema"`
	RootKeyID      string                           `json:"rootKeyId"`
	PolicyID       string                           `json:"policyId"`
	ManifestDigest string                           `json:"manifestDigest"`
	Keys           map[string]StrideE10W7TrustedKey `json:"keys"`
	Signature      string                           `json:"signature"`
}

type StrideE10W8ActivationManifest struct {
	Schema                string                     `json:"schema"`
	CandidateCommit       string                     `json:"candidateCommit"`
	TrustPolicyDigest     string                     `json:"trustPolicyDigest"`
	RootPolicy            StrideE10W8RootPolicy      `json:"rootPolicy"`
	RouteMapDigest        string                     `json:"routeMapDigest"`
	W7ManifestDigest      string                     `json:"w7ManifestDigest"`
	W5Disposition         string                     `json:"w5Disposition"`
	W5DecisionDigest      string                     `json:"w5DecisionDigest"`
	W6QualificationDigest string                     `json:"w6QualificationDigest"`
	RollbackReceiptDigest string                     `json:"rollbackReceiptDigest"`
	FrozenAt              time.Time                  `json:"frozenAt"`
	Cohorts               []StrideE10W8SignedReceipt `json:"cohorts"`
	Soak                  StrideE10W8SignedReceipt   `json:"soak"`
	W7Result              StrideE10W8SignedReceipt   `json:"w7Result"`
	W5Decision            StrideE10W8SignedReceipt   `json:"w5Decision"`
	W6Qualification       StrideE10W8SignedReceipt   `json:"w6Qualification"`
	Rollback              StrideE10W8SignedReceipt   `json:"rollback"`
	Subreceipts           []StrideE10W8SignedReceipt `json:"subreceipts"`
}

type StrideE10W8CohortReceipt struct {
	Source                      string    `json:"source"`
	Index                       int64     `json:"index"`
	Name                        string    `json:"name"`
	ReleaseCommit               string    `json:"releaseCommit"`
	RouteMapDigest              string    `json:"routeMapDigest"`
	TenantID                    string    `json:"tenantId"`
	CohortID                    string    `json:"cohortId"`
	EnabledFeatures             []string  `json:"enabledFeatures"`
	KillSwitch                  string    `json:"killSwitch"`
	ActivationReceiptDigest     string    `json:"activationReceiptDigest"`
	KillSwitchTestReceiptDigest string    `json:"killSwitchTestReceiptDigest"`
	PreviousCohortReceiptDigest string    `json:"previousCohortReceiptDigest,omitempty"`
	RollbackProven              bool      `json:"rollbackProven"`
	PurgeFenceProven            bool      `json:"purgeFenceProven"`
	ActivatedAt                 time.Time `json:"activatedAt"`
}

type StrideE10W8Sitting struct {
	ID                       string    `json:"id"`
	StartedAt                time.Time `json:"startedAt"`
	EndedAt                  time.Time `json:"endedAt"`
	Participants             int64     `json:"participants"`
	CohortsObserved          []string  `json:"cohortsObserved"`
	RevokeLatencySeconds     int64     `json:"revokeLatencySeconds"`
	PurgeLatencySeconds      int64     `json:"purgeLatencySeconds"`
	ProhibitedLeakageCount   int64     `json:"prohibitedLeakageCount"`
	StaleHitCount            int64     `json:"staleHitCount"`
	DuplicateSideEffectCount int64     `json:"duplicateSideEffectCount"`
	HiddenFallbackCount      int64     `json:"hiddenFallbackCount"`
	UnauthorizedContactCount int64     `json:"unauthorizedContactCount"`
	ReceiptDigest            string    `json:"receiptDigest"`
}

type StrideE10W8SoakReceipt struct {
	Source                     string               `json:"source"`
	ReleaseCommit              string               `json:"releaseCommit"`
	RouteMapDigest             string               `json:"routeMapDigest"`
	W7ManifestDigest           string               `json:"w7ManifestDigest"`
	LastRouteChangeAt          time.Time            `json:"lastRouteChangeAt"`
	StartedAt                  time.Time            `json:"startedAt"`
	EndedAt                    time.Time            `json:"endedAt"`
	Sittings                   []StrideE10W8Sitting `json:"sittings"`
	KillSwitchesExercised      []string             `json:"killSwitchesExercised"`
	ProductionObservation      bool                 `json:"productionObservation"`
	ExactReleaseUnchanged      bool                 `json:"exactReleaseUnchanged"`
	FinalRollbackProven        bool                 `json:"finalRollbackProven"`
	FinalRollbackReceiptDigest string               `json:"finalRollbackReceiptDigest"`
}

type StrideE10W8DependencyReceipt struct {
	Source         string `json:"source"`
	ReleaseCommit  string `json:"releaseCommit"`
	ManifestDigest string `json:"manifestDigest"`
	Disposition    string `json:"disposition"`
	Ready          bool   `json:"ready"`
}

type StrideE10W8BoundSubreceipt struct {
	Source              string `json:"source"`
	ReleaseCommit       string `json:"releaseCommit"`
	ParentKind          string `json:"parentKind"`
	ParentPayloadDigest string `json:"parentPayloadDigest"`
	Verdict             string `json:"verdict"`
}

type StrideE10W8PreflightResult struct {
	Ready          bool     `json:"ready"`
	Reasons        []string `json:"reasons"`
	ManifestDigest string   `json:"manifestDigest,omitempty"`
}

func ValidateStrideE10W8Activation(manifest StrideE10W8ActivationManifest) StrideE10W8PreflightResult {
	return validateStrideE10W8Activation(manifest)
}

func validateStrideE10W8Activation(manifest StrideE10W8ActivationManifest) StrideE10W8PreflightResult {
	reasons := make([]string, 0)
	trust, trustErr := strideE10W8RegistryFromRootPolicy(manifest)
	if trustErr != nil || manifest.Schema != "stride.e10.w8.activation-manifest.v1" || !strideE10W7Commit(manifest.CandidateCommit) || !strideE10W7Digest(manifest.TrustPolicyDigest) || !strideE10W7Digest(manifest.RouteMapDigest) || !strideE10W7Digest(manifest.W7ManifestDigest) || !strideE10W7Digest(manifest.W5DecisionDigest) || !strideE10W7Digest(manifest.W6QualificationDigest) || !strideE10W7Digest(manifest.RollbackReceiptDigest) || !oneOf(manifest.W5Disposition, "completed", "aj_explicitly_deferred") || manifest.FrozenAt.IsZero() || manifest.FrozenAt.After(time.Now().UTC().Add(5*time.Minute)) {
		reasons = append(reasons, "w8_manifest_invalid")
	}
	if !strideE10W8IndependentTrust(trust) {
		reasons = append(reasons, "w8_signer_roles_not_independent")
	}
	cohortTrust, _ := trust.ResolveStrideE10W8Trust("cohort_activation")
	soakTrust, _ := trust.ResolveStrideE10W8Trust("production_soak")
	if cohortTrust.KeyID == soakTrust.KeyID || cohortTrust.PublicKey == soakTrust.PublicKey {
		reasons = append(reasons, "w8_soak_observer_not_independent")
	}
	for _, dep := range []struct {
		kind                string
		signed              StrideE10W8SignedReceipt
		digest, disposition string
	}{{"w7_result", manifest.W7Result, manifest.W7ManifestDigest, "ready"}, {"aj_w5_decision", manifest.W5Decision, manifest.W5DecisionDigest, manifest.W5Disposition}, {"w6_qualification", manifest.W6Qualification, manifest.W6QualificationDigest, "qualified"}, {"rollback_readiness", manifest.Rollback, manifest.RollbackReceiptDigest, "ready"}} {
		key, _ := trust.ResolveStrideE10W8Trust(dep.kind)
		var payload StrideE10W8DependencyReceipt
		if !strideE10W8VerifySigned(dep.signed, dep.kind, key, manifest.FrozenAt) || strideE10W7Decode(dep.signed.Payload, &payload) != nil || payload.Source != "independent_signed" || payload.ReleaseCommit != manifest.CandidateCommit || payload.ManifestDigest != dep.digest || payload.Disposition != dep.disposition || !payload.Ready {
			reasons = append(reasons, "w8_dependency_invalid_"+dep.kind)
		}
	}
	subs := map[string]StrideE10W8SignedReceipt{}
	for _, signed := range manifest.Subreceipts {
		key, _ := trust.ResolveStrideE10W8Trust(signed.Kind)
		d := strideE10W8ReceiptDigest(signed)
		if !strideE10W8VerifySigned(signed, signed.Kind, key, manifest.FrozenAt) {
			reasons = append(reasons, "w8_subreceipt_invalid_"+signed.Kind)
		} else {
			subs[d] = signed
		}
	}
	if len(manifest.Cohorts) != len(strideE10W8CohortOrder) {
		reasons = append(reasons, "w8_cohort_count_invalid")
	}
	previousDigest := ""
	lastActivation := time.Time{}
	killSwitches := make([]string, 0, len(strideE10W8CohortOrder))
	for index, expectedName := range strideE10W8CohortOrder {
		if index >= len(manifest.Cohorts) {
			reasons = append(reasons, "w8_missing_cohort_"+expectedName)
			continue
		}
		signed := manifest.Cohorts[index]
		if !strideE10W8VerifySigned(signed, "cohort_activation", cohortTrust, manifest.FrozenAt) {
			reasons = append(reasons, "w8_cohort_signature_invalid_"+expectedName)
		}
		var receipt StrideE10W8CohortReceipt
		if strideE10W7Decode(signed.Payload, &receipt) != nil || receipt.Source != "production_observed" || receipt.Index != int64(index+1) || receipt.Name != expectedName || receipt.ReleaseCommit != manifest.CandidateCommit || receipt.RouteMapDigest != manifest.RouteMapDigest || !strideE10W7ID(receipt.TenantID) || !strideE10W7ID(receipt.CohortID) || !strideE10W7ExactStrings(receipt.EnabledFeatures, strideE10W8CohortFeatures[expectedName], len(strideE10W8CohortFeatures[expectedName]), len(strideE10W8CohortFeatures[expectedName])) || !strideE10W7ID(receipt.KillSwitch) || !strideE10W7Digest(receipt.ActivationReceiptDigest) || !strideE10W7Digest(receipt.KillSwitchTestReceiptDigest) || receipt.PreviousCohortReceiptDigest != previousDigest || !receipt.RollbackProven || !receipt.PurgeFenceProven || receipt.ActivatedAt.IsZero() || receipt.ActivatedAt.After(manifest.FrozenAt) || signed.ObservedAt.Before(receipt.ActivatedAt) || !lastActivation.IsZero() && !receipt.ActivatedAt.After(lastActivation) {
			reasons = append(reasons, "w8_cohort_invalid_"+expectedName)
		}
		previousDigest = strideE10W8ReceiptDigest(signed)
		lastActivation = receipt.ActivatedAt
		killSwitches = append(killSwitches, receipt.KillSwitch)
		if !strideE10W8BoundSubreceiptValid(subs[receipt.ActivationReceiptDigest], "cohort_activation_effect", "cohort_activation", signed.Payload, manifest.CandidateCommit) || !strideE10W8BoundSubreceiptValid(subs[receipt.KillSwitchTestReceiptDigest], "kill_switch_test", "cohort_activation", signed.Payload, manifest.CandidateCommit) {
			reasons = append(reasons, "w8_cohort_subreceipt_unresolved_"+expectedName)
		}
	}
	if !strideE10W8VerifySigned(manifest.Soak, "production_soak", soakTrust, manifest.FrozenAt) {
		reasons = append(reasons, "w8_soak_signature_invalid")
	}
	var soak StrideE10W8SoakReceipt
	if strideE10W7Decode(manifest.Soak.Payload, &soak) != nil || soak.Source != "production_observed" || soak.ReleaseCommit != manifest.CandidateCommit || soak.RouteMapDigest != manifest.RouteMapDigest || soak.W7ManifestDigest != manifest.W7ManifestDigest || soak.LastRouteChangeAt.IsZero() || soak.StartedAt.Before(soak.LastRouteChangeAt) || soak.EndedAt.Sub(soak.StartedAt) < 24*time.Hour || manifest.Soak.ObservedAt.Before(soak.EndedAt) || len(soak.Sittings) < 10 || !strideE10W7ExactStrings(soak.KillSwitchesExercised, killSwitches, len(killSwitches), len(killSwitches)) || !soak.ProductionObservation || !soak.ExactReleaseUnchanged || !soak.FinalRollbackProven {
		reasons = append(reasons, "w8_soak_invalid")
	} else if !lastActivation.IsZero() && soak.LastRouteChangeAt.Before(lastActivation) {
		reasons = append(reasons, "w8_soak_precedes_final_cohort")
	}
	seenSittings := map[string]bool{}
	for _, sitting := range soak.Sittings {
		if !strideE10W7ID(sitting.ID) || seenSittings[sitting.ID] || sitting.StartedAt.IsZero() || !sitting.EndedAt.After(sitting.StartedAt) || sitting.StartedAt.Before(soak.StartedAt) || sitting.EndedAt.After(soak.EndedAt) || sitting.Participants < 1 || !strideE10W7ExactStrings(sitting.CohortsObserved, strideE10W8CohortOrder, len(strideE10W8CohortOrder), len(strideE10W8CohortOrder)) || sitting.RevokeLatencySeconds < 0 || sitting.RevokeLatencySeconds > 300 || sitting.PurgeLatencySeconds < 0 || sitting.PurgeLatencySeconds > 300 || sitting.ProhibitedLeakageCount != 0 || sitting.StaleHitCount != 0 || sitting.DuplicateSideEffectCount != 0 || sitting.HiddenFallbackCount != 0 || sitting.UnauthorizedContactCount != 0 || !strideE10W7Digest(sitting.ReceiptDigest) {
			reasons = append(reasons, "w8_sitting_invalid_"+sitting.ID)
		}
		seenSittings[sitting.ID] = true
		if !strideE10W8BoundSittingValid(subs[sitting.ReceiptDigest], sitting, manifest.CandidateCommit) {
			reasons = append(reasons, "w8_sitting_receipt_unresolved_"+sitting.ID)
		}
	}
	if !strideE10W8BoundSubreceiptValid(subs[soak.FinalRollbackReceiptDigest], "final_rollback", "production_soak", manifest.Soak.Payload, manifest.CandidateCommit) {
		reasons = append(reasons, "w8_final_rollback_unresolved")
	}
	sort.Strings(reasons)
	reasons = strideE10W7Unique(reasons)
	result := StrideE10W8PreflightResult{Ready: len(reasons) == 0, Reasons: reasons}
	if result.Ready {
		body, _ := json.Marshal(manifest)
		digest := sha256.Sum256(body)
		result.ManifestDigest = hex.EncodeToString(digest[:])
	}
	return result
}

type strideE10W8SealedRegistry struct {
	keys   map[string]StrideE10W7TrustedKey
	digest string
}

func (r *strideE10W8SealedRegistry) ResolveStrideE10W8Trust(kind string) (StrideE10W7TrustedKey, error) {
	key, ok := r.keys[kind]
	if !ok {
		return StrideE10W7TrustedKey{}, ErrStrideE10W8NotReady
	}
	return key, nil
}
func (r *strideE10W8SealedRegistry) StrideE10W8TrustPolicyDigest() string { return r.digest }
func strideE10W8RegistryFromRootPolicy(m StrideE10W8ActivationManifest) (StrideE10W8TrustController, error) {
	p := m.RootPolicy
	keys := map[string]StrideE10W7TrustedKey{}
	for k, v := range p.Keys {
		keys[k] = v
	}
	diagnosticRegistry := &strideE10W8SealedRegistry{keys: keys, digest: m.TrustPolicyDigest}
	if p.Schema != "stride.e10.w8.root-policy.v1" || p.RootKeyID != strideE10W8CompiledRootKeyID || !strideE10W7ID(p.PolicyID) || p.ManifestDigest != strideE10W8ManifestBindingDigest(m) || m.TrustPolicyDigest != strideE10W8PolicyDigest(p) {
		return diagnosticRegistry, ErrStrideE10W8NotReady
	}
	pub, e := base64.StdEncoding.DecodeString(strideE10W8ProductionRootPublicKey)
	sig, se := base64.StdEncoding.DecodeString(p.Signature)
	input, ie := strideE10W8RootPolicyInput(p)
	if e != nil || se != nil || ie != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(pub), input, sig) {
		return diagnosticRegistry, ErrStrideE10W8NotReady
	}
	return diagnosticRegistry, nil
}

func strideE10W8IndependentTrust(trust StrideE10W8TrustController) bool {
	if trust == nil {
		return false
	}
	kinds := []string{"w7_result", "aj_w5_decision", "w6_qualification", "rollback_readiness", "cohort_activation", "production_soak", "cohort_activation_effect", "kill_switch_test", "sitting_observation", "final_rollback"}
	keyIDs, publicKeys := map[string]bool{}, map[string]bool{}
	for _, kind := range kinds {
		key, err := trust.ResolveStrideE10W8Trust(kind)
		if err != nil || !strideE10W7ID(key.KeyID) || strings.TrimSpace(key.PublicKey) == "" || keyIDs[key.KeyID] || publicKeys[key.PublicKey] {
			return false
		}
		keyIDs[key.KeyID], publicKeys[key.PublicKey] = true, true
	}
	return true
}
func strideE10W8RootPolicyInput(p StrideE10W8RootPolicy) ([]byte, error) {
	p.Signature = ""
	body, e := json.Marshal(p)
	if e != nil {
		return nil, e
	}
	return append([]byte("meetingassist/stride/e10/w8/root-policy/v1\x00"), body...), nil
}
func strideE10W8PolicyDigest(p StrideE10W8RootPolicy) string {
	p.Signature = ""
	body, _ := json.Marshal(p)
	d := sha256.Sum256(body)
	return hex.EncodeToString(d[:])
}
func strideE10W8ManifestBindingDigest(m StrideE10W8ActivationManifest) string {
	m.RootPolicy.ManifestDigest = ""
	m.RootPolicy.Signature = ""
	m.TrustPolicyDigest = ""
	body, _ := json.Marshal(m)
	d := sha256.Sum256(body)
	return hex.EncodeToString(d[:])
}

func strideE10W8VerifySigned(receipt StrideE10W8SignedReceipt, kind string, trusted StrideE10W7TrustedKey, frozenAt time.Time) bool {
	if receipt.Schema != strideE10W8ReceiptSchema || receipt.Kind != kind || receipt.KeyID != trusted.KeyID || receipt.ObservedAt.IsZero() || receipt.ObservedAt.After(frozenAt) || frozenAt.Sub(receipt.ObservedAt) > 30*24*time.Hour {
		return false
	}
	key, keyErr := base64.StdEncoding.DecodeString(trusted.PublicKey)
	signature, sigErr := base64.StdEncoding.DecodeString(receipt.Signature)
	input, inputErr := strideE10W8SignatureInput(receipt)
	return keyErr == nil && sigErr == nil && inputErr == nil && len(key) == ed25519.PublicKeySize && len(signature) == ed25519.SignatureSize && ed25519.Verify(ed25519.PublicKey(key), input, signature)
}

func strideE10W8SignatureInput(receipt StrideE10W8SignedReceipt) ([]byte, error) {
	var compact bytes.Buffer
	if json.Compact(&compact, receipt.Payload) != nil || compact.Len() == 0 {
		return nil, ErrStrideE10W8NotReady
	}
	return []byte("meetingassist/stride/e10/w8/receipt/v1\x00" + receipt.Schema + "\x00" + receipt.Kind + "\x00" + receipt.KeyID + "\x00" + receipt.ObservedAt.UTC().Format(time.RFC3339Nano) + "\x00" + compact.String()), nil
}

func SignStrideE10W8Receipt(kind, keyID string, observedAt time.Time, payload any, privateKey ed25519.PrivateKey) (StrideE10W8SignedReceipt, error) {
	if !oneOf(kind, "cohort_activation", "production_soak", "w7_result", "aj_w5_decision", "w6_qualification", "rollback_readiness", "cohort_activation_effect", "kill_switch_test", "sitting_observation", "final_rollback") || !strideE10W7ID(keyID) || observedAt.IsZero() || len(privateKey) != ed25519.PrivateKeySize {
		return StrideE10W8SignedReceipt{}, ErrStrideE10W8NotReady
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return StrideE10W8SignedReceipt{}, err
	}
	receipt := StrideE10W8SignedReceipt{Schema: strideE10W8ReceiptSchema, Kind: kind, KeyID: keyID, ObservedAt: observedAt.UTC(), Payload: raw}
	input, err := strideE10W8SignatureInput(receipt)
	if err != nil {
		return StrideE10W8SignedReceipt{}, err
	}
	receipt.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, input))
	return receipt, nil
}

func strideE10W8BoundSubreceiptValid(signed StrideE10W8SignedReceipt, kind, parentKind string, raw json.RawMessage, commit string) bool {
	if signed.Kind != kind {
		return false
	}
	var value StrideE10W8BoundSubreceipt
	return strideE10W7Decode(signed.Payload, &value) == nil && value.Source == "production_observed" && value.ReleaseCommit == commit && value.ParentKind == parentKind && value.ParentPayloadDigest == strideE10W8ParentBindingDigest(parentKind, raw) && value.Verdict == "passed"
}

func strideE10W8ParentBindingDigest(kind string, raw json.RawMessage) string {
	var normalized any
	switch kind {
	case "cohort_activation":
		var value StrideE10W8CohortReceipt
		if strideE10W7Decode(raw, &value) != nil {
			return ""
		}
		value.ActivationReceiptDigest, value.KillSwitchTestReceiptDigest = "", ""
		normalized = value
	case "production_soak":
		var value StrideE10W8SoakReceipt
		if strideE10W7Decode(raw, &value) != nil {
			return ""
		}
		value.FinalRollbackReceiptDigest = ""
		normalized = value
	default:
		return ""
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func strideE10W8BoundSittingValid(signed StrideE10W8SignedReceipt, sitting StrideE10W8Sitting, commit string) bool {
	if signed.Kind != "sitting_observation" {
		return false
	}
	copy := sitting
	copy.ReceiptDigest = ""
	raw, _ := json.Marshal(copy)
	digest := sha256.Sum256(raw)
	var value StrideE10W8BoundSubreceipt
	return strideE10W7Decode(signed.Payload, &value) == nil && value.Source == "production_observed" && value.ReleaseCommit == commit && value.ParentKind == "sitting" && value.ParentPayloadDigest == hex.EncodeToString(digest[:]) && value.Verdict == "passed"
}

func strideE10W8ReceiptDigest(receipt StrideE10W8SignedReceipt) string {
	body, _ := json.Marshal(receipt)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func (r StrideE10W8PreflightResult) Error() error {
	if r.Ready {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrStrideE10W8NotReady, strings.Join(r.Reasons, ","))
}
