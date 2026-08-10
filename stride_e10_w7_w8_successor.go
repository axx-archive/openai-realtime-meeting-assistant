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
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	strideE10W7W8SuccessorSchema        = "stride.e10.w7-w8.successor-acceptance.v1"
	strideE10W7W8SuccessorReceiptSchema = "stride.e10.w7-w8.successor-dependency.v1"
	strideE10W7W8SuccessorRootSchema    = "stride.e10.w7-w8.successor-root-policy.v1"
	strideE10W7W8SuccessorRootKeyID     = "stride-e10-w7-w8-successor-root-2026-08"
	strideE10W7W8SuccessorCLICommand    = "stride-w7-w8-successor-read-only"
	// Public-only, separately generated acceptance root. No production code or
	// test seam can replace it. The corresponding private key is not in this
	// repository.
	strideE10W7W8SuccessorRootPublicKey = "qSm7luy0YP54sJIeXIq1qIC/mcK0HRCEcL6qy6raFZ4="
)

var ErrStrideE10W7W8SuccessorNotReady = errors.New("stride e10 w7/w8 successor acceptance not ready")

var strideE10W7W8SuccessorPrerequisiteKinds = []string{
	"pd0_result",
	"pd1_pilot_result",
	"pi0_a_result",
	"pi0_b_result",
	"pn_normative_result",
	"pn_critic_result",
	"w6_qualification_result",
	"acceptance_environment",
}

var strideE10W7W8SuccessorFinalKinds = []string{
	"w5_governance_result",
	"rollback_readiness_result",
	"w7_acceptance_result",
	"w8_activation_result",
}

func strideE10W7W8SuccessorDependencyKinds() []string {
	return append(append([]string(nil), strideE10W7W8SuccessorPrerequisiteKinds...), strideE10W7W8SuccessorFinalKinds...)
}

var strideE10W7W8SuccessorDispositions = map[string]string{
	"pd0_result":              "implementation_accepted",
	"pd1_pilot_result":        "pilot_subset_accepted",
	"pi0_a_result":            "implementation_and_baseline_accepted",
	"pi0_b_result":            "collection_gate_accepted",
	"pn_normative_result":     "normative_freeze_accepted",
	"pn_critic_result":        "passed",
	"w6_qualification_result": "qualified",
}

type StrideE10W7W8SuccessorSignedDependency struct {
	Schema     string          `json:"schema"`
	Kind       string          `json:"kind"`
	KeyID      string          `json:"keyId"`
	ObservedAt time.Time       `json:"observedAt"`
	Payload    json.RawMessage `json:"payload"`
	Signature  string          `json:"signature"`
}

type StrideE10W7W8SuccessorDependency struct {
	Source         string `json:"source"`
	ReleaseCommit  string `json:"releaseCommit"`
	EvidenceDigest string `json:"evidenceDigest"`
	Disposition    string `json:"disposition"`
	Independent    bool   `json:"independent"`
}

type StrideE10W7W8W5GovernanceResult struct {
	Source         string `json:"source"`
	ReleaseCommit  string `json:"releaseCommit"`
	EvidenceDigest string `json:"evidenceDigest"`
	Decision       string `json:"decision"`
	Independent    bool   `json:"independent"`
}

type StrideE10W7W8RollbackReadinessResult struct {
	Source               string `json:"source"`
	ReleaseCommit        string `json:"releaseCommit"`
	CandidateBuild       int64  `json:"candidateBuild"`
	NativeArtifactDigest string `json:"nativeArtifactDigest"`
	ManifestDigest       string `json:"manifestDigest"`
	ResultDigest         string `json:"resultDigest"`
	Ready                bool   `json:"ready"`
	Independent          bool   `json:"independent"`
}

type StrideE10W7W8LegacyAcceptanceResult struct {
	Source                       string  `json:"source"`
	ReleaseCommit                string  `json:"releaseCommit"`
	CandidateBuild               int64   `json:"candidateBuild"`
	NativeArtifactDigest         string  `json:"nativeArtifactDigest"`
	ValidatorDigest              string  `json:"validatorDigest"`
	ManifestDigest               string  `json:"manifestDigest"`
	ResultDigest                 string  `json:"resultDigest"`
	W7ManifestDigest             string  `json:"w7ManifestDigest,omitempty"`
	W7ResultDigest               string  `json:"w7ResultDigest,omitempty"`
	RollbackManifestDigest       string  `json:"rollbackManifestDigest,omitempty"`
	RollbackResultDigest         string  `json:"rollbackResultDigest,omitempty"`
	DependencyVerificationDigest string  `json:"dependencyVerificationDigest,omitempty"`
	Verdict                      string  `json:"verdict"`
	ActivationComplete           bool    `json:"activationComplete"`
	SoakHours                    float64 `json:"soakHours"`
	SittingCount                 int     `json:"sittingCount"`
	Independent                  bool    `json:"independent"`
}

// StrideE10W7W8AcceptanceEnvironment separates a private W6 qualification
// exercise from W8 public activation. It authorizes no action: it is an
// independently signed observation consumed only by this read-only validator.
type StrideE10W7W8AcceptanceEnvironment struct {
	Source                    string `json:"source"`
	Classification            string `json:"classification"`
	ReleaseCommit             string `json:"releaseCommit"`
	NativeBuild               int64  `json:"nativeBuild"`
	NativeArtifactDigest      string `json:"nativeArtifactDigest"`
	W6QualificationDigest     string `json:"w6QualificationDigest"`
	TenantID                  string `json:"tenantId"`
	CohortID                  string `json:"cohortId"`
	PrivateIngress            bool   `json:"privateIngress"`
	PublicRoutesEnabled       bool   `json:"publicRoutesEnabled"`
	ProductionCohortActivated bool   `json:"productionCohortActivated"`
	OpenWebEnabled            bool   `json:"openWebEnabled"`
	KillSwitchBound           bool   `json:"killSwitchBound"`
	PurgeFenceBound           bool   `json:"purgeFenceBound"`
	EnvironmentDestroyedAfter bool   `json:"environmentDestroyedAfter"`
}

type StrideE10W7W8SuccessorTrustedKey struct {
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"`
}

type StrideE10W7W8SuccessorRootPolicy struct {
	Schema         string                                      `json:"schema"`
	RootKeyID      string                                      `json:"rootKeyId"`
	PolicyID       string                                      `json:"policyId"`
	ManifestDigest string                                      `json:"manifestDigest"`
	Keys           map[string]StrideE10W7W8SuccessorTrustedKey `json:"keys"`
	Signature      string                                      `json:"signature"`
}

type StrideE10W7W8SuccessorManifest struct {
	Schema                  string                                   `json:"schema"`
	CandidateCommit         string                                   `json:"candidateCommit"`
	CandidateBuild          int64                                    `json:"candidateBuild"`
	NativeArtifactDigest    string                                   `json:"nativeArtifactDigest"`
	LegacyW7ValidatorDigest string                                   `json:"legacyW7ValidatorDigest"`
	LegacyW8ValidatorDigest string                                   `json:"legacyW8ValidatorDigest"`
	W6QualificationDigest   string                                   `json:"w6QualificationDigest"`
	W7AcceptanceDigest      string                                   `json:"w7AcceptanceDigest"`
	TrustPolicyDigest       string                                   `json:"trustPolicyDigest"`
	RootPolicy              StrideE10W7W8SuccessorRootPolicy         `json:"rootPolicy"`
	FrozenAt                time.Time                                `json:"frozenAt"`
	Dependencies            []StrideE10W7W8SuccessorSignedDependency `json:"dependencies"`
}

type StrideE10W7W8SuccessorResult struct {
	PrerequisitesReady         bool     `json:"prerequisitesReady"`
	FinalReady                 bool     `json:"finalReady"`
	Ready                      bool     `json:"ready"`
	Reasons                    []string `json:"reasons"`
	PrerequisiteReasons        []string `json:"prerequisiteReasons,omitempty"`
	FinalReasons               []string `json:"finalReasons,omitempty"`
	ManifestDigest             string   `json:"manifestDigest,omitempty"`
	FinalManifestDigest        string   `json:"finalManifestDigest,omitempty"`
	ExternallySealedFinalReady bool     `json:"externallySealedFinalReady"`
}

func ValidateStrideE10W7W8SuccessorAcceptance(manifest StrideE10W7W8SuccessorManifest) StrideE10W7W8SuccessorResult {
	return validateStrideE10W7W8SuccessorAcceptanceAt(manifest, time.Now().UTC())
}

func validateStrideE10W7W8SuccessorAcceptanceAt(manifest StrideE10W7W8SuccessorManifest, now time.Time) StrideE10W7W8SuccessorResult {
	prerequisiteReasons := make([]string, 0)
	finalReasons := make([]string, 0)
	keys, policyOK := strideE10W7W8SuccessorPolicy(manifest)
	if !policyOK || manifest.Schema != strideE10W7W8SuccessorSchema || !strideE10W7Commit(manifest.CandidateCommit) || manifest.CandidateBuild < 1 ||
		!strideE10W7Digest(manifest.NativeArtifactDigest) ||
		!strideE10W7Digest(manifest.LegacyW7ValidatorDigest) || !strideE10W7Digest(manifest.LegacyW8ValidatorDigest) ||
		!strideE10W7Digest(manifest.W6QualificationDigest) || !strideE10W7Digest(manifest.W7AcceptanceDigest) || !strideE10W7Digest(manifest.TrustPolicyDigest) ||
		manifest.FrozenAt.IsZero() || manifest.FrozenAt.After(now.Add(5*time.Minute)) || now.Sub(manifest.FrozenAt) > 30*24*time.Hour {
		prerequisiteReasons = append(prerequisiteReasons, "successor_manifest_invalid")
	}
	if !strideE10W7W8SuccessorIndependentKeys(keys) {
		prerequisiteReasons = append(prerequisiteReasons, "successor_signer_roles_not_independent")
	}
	byKind := map[string]StrideE10W7W8SuccessorSignedDependency{}
	for _, signed := range manifest.Dependencies {
		if !containsSTRIDEString(strideE10W7W8SuccessorDependencyKinds(), signed.Kind) {
			prerequisiteReasons = append(prerequisiteReasons, "successor_unknown_dependency")
			continue
		}
		if _, exists := byKind[signed.Kind]; exists {
			if containsSTRIDEString(strideE10W7W8SuccessorFinalKinds, signed.Kind) {
				finalReasons = append(finalReasons, "successor_duplicate_"+signed.Kind)
			} else {
				prerequisiteReasons = append(prerequisiteReasons, "successor_duplicate_"+signed.Kind)
			}
			continue
		}
		byKind[signed.Kind] = signed
		key := keys[signed.Kind]
		// FrozenAt is the immutable root-policy/static-contract freeze. Evidence is
		// independently signed and may be observed later (notably after the W8
		// 24-hour soak), but it must still be current at validation time.
		if signed.Schema != strideE10W7W8SuccessorReceiptSchema || signed.KeyID != key.KeyID || !strideE10W7W8SuccessorEvidenceTimeValid(manifest.FrozenAt, signed.ObservedAt, now) || !strideE10W7W8SuccessorVerify(signed, key.PublicKey) {
			if containsSTRIDEString(strideE10W7W8SuccessorFinalKinds, signed.Kind) {
				finalReasons = append(finalReasons, "successor_dependency_signature_invalid_"+signed.Kind)
			} else {
				prerequisiteReasons = append(prerequisiteReasons, "successor_dependency_signature_invalid_"+signed.Kind)
			}
		}
	}
	for _, kind := range strideE10W7W8SuccessorPrerequisiteKinds {
		if _, ok := byKind[kind]; !ok {
			prerequisiteReasons = append(prerequisiteReasons, "successor_missing_"+kind)
		}
	}
	for _, kind := range strideE10W7W8SuccessorFinalKinds {
		if _, ok := byKind[kind]; !ok {
			finalReasons = append(finalReasons, "successor_missing_"+kind)
		}
	}
	for kind, disposition := range strideE10W7W8SuccessorDispositions {
		signed, ok := byKind[kind]
		if !ok {
			continue
		}
		var dependency StrideE10W7W8SuccessorDependency
		if strideE10W7Decode(signed.Payload, &dependency) != nil || dependency.Source != "independent_signed" || dependency.ReleaseCommit != manifest.CandidateCommit || !strideE10W7Digest(dependency.EvidenceDigest) || dependency.Disposition != disposition || !dependency.Independent {
			prerequisiteReasons = append(prerequisiteReasons, "successor_dependency_invalid_"+kind)
		} else if kind == "w6_qualification_result" && dependency.EvidenceDigest != manifest.W6QualificationDigest {
			prerequisiteReasons = append(prerequisiteReasons, "successor_w6_qualification_binding_invalid")
		}
	}
	if signed, ok := byKind["acceptance_environment"]; ok {
		var environment StrideE10W7W8AcceptanceEnvironment
		if strideE10W7Decode(signed.Payload, &environment) != nil || environment.Source != "independent_observed" ||
			environment.Classification != "isolated_private_w6_qualification" || environment.ReleaseCommit != manifest.CandidateCommit ||
			environment.NativeBuild != manifest.CandidateBuild || environment.NativeArtifactDigest != manifest.NativeArtifactDigest ||
			environment.W6QualificationDigest != manifest.W6QualificationDigest || !strideE10W7ID(environment.TenantID) || !strideE10W7ID(environment.CohortID) ||
			!environment.PrivateIngress || environment.PublicRoutesEnabled || environment.ProductionCohortActivated || environment.OpenWebEnabled ||
			!environment.KillSwitchBound || !environment.PurgeFenceBound || !environment.EnvironmentDestroyedAfter {
			prerequisiteReasons = append(prerequisiteReasons, "successor_acceptance_environment_invalid")
		}
		if environment.Classification == "public_w8_activation" || environment.ProductionCohortActivated || environment.PublicRoutesEnabled {
			prerequisiteReasons = append(prerequisiteReasons, "successor_circular_public_activation_forbidden")
		}
	}
	validateFinal := func(kind string, target any, valid func() bool) {
		signed, ok := byKind[kind]
		if !ok || strideE10W7Decode(signed.Payload, target) != nil || !valid() {
			finalReasons = append(finalReasons, "successor_final_dependency_invalid_"+kind)
		}
	}
	var w5 StrideE10W7W8W5GovernanceResult
	validateFinal("w5_governance_result", &w5, func() bool {
		return w5.Source == "independent_signed" && w5.ReleaseCommit == manifest.CandidateCommit && strideE10W7Digest(w5.EvidenceDigest) && oneOf(w5.Decision, "approved", "governed_deferral") && w5.Independent
	})
	var rollback StrideE10W7W8RollbackReadinessResult
	validateFinal("rollback_readiness_result", &rollback, func() bool {
		return rollback.Source == "independent_signed" && rollback.ReleaseCommit == manifest.CandidateCommit && rollback.CandidateBuild == manifest.CandidateBuild && rollback.NativeArtifactDigest == manifest.NativeArtifactDigest && strideE10W7Digest(rollback.ManifestDigest) && strideE10W7Digest(rollback.ResultDigest) && rollback.Ready && rollback.Independent
	})
	var w7 StrideE10W7W8LegacyAcceptanceResult
	validateFinal("w7_acceptance_result", &w7, func() bool {
		return strideE10W7W8LegacyResultValid(w7, manifest) && w7.ValidatorDigest == manifest.LegacyW7ValidatorDigest && w7.ResultDigest == manifest.W7AcceptanceDigest && w7.Verdict == "ready" && !w7.ActivationComplete && w7.SoakHours == 0 && w7.SittingCount == 0
	})
	var w8 StrideE10W7W8LegacyAcceptanceResult
	validateFinal("w8_activation_result", &w8, func() bool {
		w7Signed, w7OK := byKind["w7_acceptance_result"]
		rollbackSigned, rollbackOK := byKind["rollback_readiness_result"]
		return strideE10W7W8LegacyResultValid(w8, manifest) && w8.ValidatorDigest == manifest.LegacyW8ValidatorDigest && w8.Verdict == "activated_and_soaked" && w8.ActivationComplete && w8.SoakHours == 24 && w8.SittingCount == 10 &&
			w7OK && rollbackOK && strideE10W7W8FinalLineageValid(w8, w7, rollback, w7Signed, rollbackSigned)
	})
	if signed, ok := byKind["w8_activation_result"]; ok && strideE10W7W8SuccessorVerify(signed, keys["w8_activation_result"].PublicKey) &&
		(w8.W7ManifestDigest == "" || w8.W7ResultDigest == "" || w8.RollbackManifestDigest == "" || w8.RollbackResultDigest == "" || w8.DependencyVerificationDigest == "") {
		finalReasons = append(finalReasons, "successor_externally_sealed_ready_fixture_required")
	}
	sort.Strings(prerequisiteReasons)
	sort.Strings(finalReasons)
	prerequisiteReasons = strideE10W7Unique(prerequisiteReasons)
	finalReasons = strideE10W7Unique(finalReasons)
	result := StrideE10W7W8SuccessorResult{PrerequisitesReady: len(prerequisiteReasons) == 0, FinalReady: len(prerequisiteReasons) == 0 && len(finalReasons) == 0, PrerequisiteReasons: prerequisiteReasons, FinalReasons: finalReasons}
	result.Ready = result.FinalReady
	result.ExternallySealedFinalReady = result.FinalReady
	result.Reasons = strideE10W7Unique(append(append([]string(nil), prerequisiteReasons...), finalReasons...))
	if result.PrerequisitesReady {
		result.ManifestDigest = strideE10W7W8SuccessorPrerequisiteDigest(manifest)
	}
	if result.FinalReady {
		result.FinalManifestDigest = strideE10W7W8SuccessorFullManifestDigest(manifest)
	}
	return result
}

func strideE10W7W8SuccessorEvidenceTimeValid(policyFrozenAt, observedAt, now time.Time) bool {
	return !policyFrozenAt.IsZero() && !observedAt.IsZero() && !now.IsZero() &&
		!observedAt.After(now.Add(5*time.Minute)) && now.Sub(observedAt) <= 30*24*time.Hour
}

func strideE10W7W8SuccessorPrerequisiteDigest(manifest StrideE10W7W8SuccessorManifest) string {
	dependencies := make([]StrideE10W7W8SuccessorSignedDependency, 0, len(strideE10W7W8SuccessorPrerequisiteKinds))
	for _, dependency := range manifest.Dependencies {
		if containsSTRIDEString(strideE10W7W8SuccessorPrerequisiteKinds, dependency.Kind) {
			dependencies = append(dependencies, dependency)
		}
	}
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].Kind < dependencies[j].Kind })
	manifest.Dependencies = dependencies
	return strideE10W7W8SuccessorFullManifestDigest(manifest)
}

func strideE10W7W8SuccessorFullManifestDigest(manifest StrideE10W7W8SuccessorManifest) string {
	dependencies := append([]StrideE10W7W8SuccessorSignedDependency(nil), manifest.Dependencies...)
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].Kind < dependencies[j].Kind })
	manifest.Dependencies = dependencies
	body, _ := json.Marshal(manifest)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func strideE10W7W8FinalLineageValid(w8, w7 StrideE10W7W8LegacyAcceptanceResult, rollback StrideE10W7W8RollbackReadinessResult, w7Signed, rollbackSigned StrideE10W7W8SuccessorSignedDependency) bool {
	return w8.W7ManifestDigest == w7.ManifestDigest && w8.W7ResultDigest == w7.ResultDigest &&
		w8.RollbackManifestDigest == rollback.ManifestDigest && w8.RollbackResultDigest == rollback.ResultDigest &&
		w8.DependencyVerificationDigest == strideE10W7W8FinalDependencyVerificationDigest(w7Signed, rollbackSigned)
}

func strideE10W7W8FinalDependencyVerificationDigest(w7, rollback StrideE10W7W8SuccessorSignedDependency) string {
	body, err := json.Marshal(struct {
		Domain   string                                 `json:"domain"`
		W7       StrideE10W7W8SuccessorSignedDependency `json:"w7"`
		Rollback StrideE10W7W8SuccessorSignedDependency `json:"rollback"`
	}{
		Domain:   "meetingassist/stride/e10/w7-w8/final-dependency-verification/v1",
		W7:       w7,
		Rollback: rollback,
	})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func strideE10W7W8LegacyResultValid(result StrideE10W7W8LegacyAcceptanceResult, manifest StrideE10W7W8SuccessorManifest) bool {
	return result.Source == "independent_signed" && result.ReleaseCommit == manifest.CandidateCommit && result.CandidateBuild == manifest.CandidateBuild &&
		result.NativeArtifactDigest == manifest.NativeArtifactDigest && strideE10W7Digest(result.ValidatorDigest) && strideE10W7Digest(result.ManifestDigest) &&
		strideE10W7Digest(result.ResultDigest) && !math.IsNaN(result.SoakHours) && !math.IsInf(result.SoakHours, 0) && result.SoakHours >= 0 && result.Independent
}

func strideE10W7W8SuccessorPolicy(manifest StrideE10W7W8SuccessorManifest) (map[string]StrideE10W7W8SuccessorTrustedKey, bool) {
	p := manifest.RootPolicy
	keys := make(map[string]StrideE10W7W8SuccessorTrustedKey, len(p.Keys))
	for kind, key := range p.Keys {
		keys[kind] = key
	}
	if p.Schema != strideE10W7W8SuccessorRootSchema || p.RootKeyID != strideE10W7W8SuccessorRootKeyID || !strideE10W7ID(p.PolicyID) ||
		p.ManifestDigest != strideE10W7W8SuccessorManifestBindingDigest(manifest) || manifest.TrustPolicyDigest != strideE10W7W8SuccessorPolicyDigest(p) {
		return keys, false
	}
	root, rootErr := base64.StdEncoding.DecodeString(strideE10W7W8SuccessorRootPublicKey)
	signature, signatureErr := base64.StdEncoding.DecodeString(p.Signature)
	input, inputErr := strideE10W7W8SuccessorRootInput(p)
	return keys, rootErr == nil && signatureErr == nil && inputErr == nil && len(root) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(root), input, signature)
}

func strideE10W7W8SuccessorIndependentKeys(keys map[string]StrideE10W7W8SuccessorTrustedKey) bool {
	if len(keys) != len(strideE10W7W8SuccessorDependencyKinds()) {
		return false
	}
	root, rootErr := base64.StdEncoding.DecodeString(strideE10W7W8SuccessorRootPublicKey)
	if rootErr != nil || len(root) != ed25519.PublicKeySize {
		return false
	}
	keyIDs, publicKeys := map[string]bool{}, map[string]bool{}
	for _, kind := range strideE10W7W8SuccessorDependencyKinds() {
		key, ok := keys[kind]
		decoded, err := base64.StdEncoding.DecodeString(key.PublicKey)
		if !ok || !strideE10W7ID(key.KeyID) || err != nil || len(decoded) != ed25519.PublicKeySize || bytes.Equal(decoded, root) || keyIDs[key.KeyID] || publicKeys[string(decoded)] {
			return false
		}
		keyIDs[key.KeyID], publicKeys[string(decoded)] = true, true
	}
	return true
}

func strideE10W7W8SuccessorSignatureInput(signed StrideE10W7W8SuccessorSignedDependency) ([]byte, error) {
	var compact bytes.Buffer
	if json.Compact(&compact, signed.Payload) != nil || compact.Len() == 0 {
		return nil, ErrStrideE10W7W8SuccessorNotReady
	}
	return []byte("meetingassist/stride/e10/w7-w8/successor-dependency/v1\x00" + signed.Schema + "\x00" + signed.Kind + "\x00" + signed.KeyID + "\x00" + signed.ObservedAt.UTC().Format(time.RFC3339Nano) + "\x00" + compact.String()), nil
}

func strideE10W7W8SuccessorVerify(signed StrideE10W7W8SuccessorSignedDependency, publicKey string) bool {
	key, keyErr := base64.StdEncoding.DecodeString(publicKey)
	signature, signatureErr := base64.StdEncoding.DecodeString(signed.Signature)
	input, inputErr := strideE10W7W8SuccessorSignatureInput(signed)
	return keyErr == nil && signatureErr == nil && inputErr == nil && len(key) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(key), input, signature)
}

func strideE10W7W8SuccessorRootInput(policy StrideE10W7W8SuccessorRootPolicy) ([]byte, error) {
	policy.Signature = ""
	body, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	return append([]byte("meetingassist/stride/e10/w7-w8/successor-root-policy/v1\x00"), body...), nil
}

func strideE10W7W8SuccessorPolicyDigest(policy StrideE10W7W8SuccessorRootPolicy) string {
	policy.Signature = ""
	body, _ := json.Marshal(policy)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func strideE10W7W8SuccessorManifestBindingDigest(manifest StrideE10W7W8SuccessorManifest) string {
	// Dependency payloads carry independent signatures. The root binds the
	// immutable candidate/static contract and complete role registry, allowing a
	// private-prerequisite report to remain valid while final evidence is absent.
	manifest.Dependencies = nil
	manifest.RootPolicy.ManifestDigest = ""
	manifest.RootPolicy.Signature = ""
	manifest.TrustPolicyDigest = ""
	body, _ := json.Marshal(manifest)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

type StrideE10W7W8SuccessorOperatorReport struct {
	PrerequisitesReady         bool     `json:"prerequisitesReady"`
	FinalReady                 bool     `json:"finalReady"`
	Ready                      bool     `json:"ready"`
	ReadOnly                   bool     `json:"readOnly"`
	ActivationCapable          bool     `json:"activationCapable"`
	CandidateCommit            string   `json:"candidateCommit,omitempty"`
	Reasons                    []string `json:"reasons"`
	ManifestDigest             string   `json:"manifestDigest,omitempty"`
	FinalManifestDigest        string   `json:"finalManifestDigest,omitempty"`
	ExternallySealedFinalReady bool     `json:"externallySealedFinalReady"`
}

func BuildStrideE10W7W8SuccessorReadOnlyReport(manifest *StrideE10W7W8SuccessorManifest) StrideE10W7W8SuccessorOperatorReport {
	return buildStrideE10W7W8SuccessorReadOnlyReportAt(manifest, time.Now().UTC())
}

func buildStrideE10W7W8SuccessorReadOnlyReportAt(manifest *StrideE10W7W8SuccessorManifest, now time.Time) StrideE10W7W8SuccessorOperatorReport {
	report := StrideE10W7W8SuccessorOperatorReport{ReadOnly: true, ActivationCapable: false}
	if manifest == nil {
		report.Reasons = []string{"successor_manifest_missing"}
		return report
	}
	report.CandidateCommit = manifest.CandidateCommit
	result := validateStrideE10W7W8SuccessorAcceptanceAt(*manifest, now)
	report.PrerequisitesReady, report.FinalReady, report.Ready, report.Reasons, report.ManifestDigest = result.PrerequisitesReady, result.FinalReady, result.Ready, result.Reasons, result.ManifestDigest
	report.FinalManifestDigest, report.ExternallySealedFinalReady = result.FinalManifestDigest, result.ExternallySealedFinalReady
	return report
}

// HandleStrideE10W7W8SuccessorReadOnlyCLI is the canonical, side-effect-free
// argument dispatcher for a process entrypoint. It never handles unrelated
// commands and exposes no activation operation. main registration is kept
// separate so concurrent bootstrap work can install this exact handler without
// weakening its closed command vocabulary.
func HandleStrideE10W7W8SuccessorReadOnlyCLI(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != strideE10W7W8SuccessorCLICommand {
		return false, 0
	}
	return true, RunStrideE10W7W8SuccessorReadOnlyCLI(args[1:], stdout, stderr)
}

// RunStrideE10W7W8SuccessorReadOnlyCLI executes the closed read-only command
// after dispatch. Process registration remains a separate integration step. It
// reads one absolute private regular manifest, has no callback, route, switch,
// provider, deployment, or activation input, and writes only the report.
func RunStrideE10W7W8SuccessorReadOnlyCLI(args []string, stdout, stderr io.Writer) int {
	return runStrideE10W7W8SuccessorReadOnlyCLIAt(args, stdout, stderr, time.Now().UTC())
}

func runStrideE10W7W8SuccessorReadOnlyCLIAt(args []string, stdout, stderr io.Writer, now time.Time) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) != 2 || args[0] != "--manifest" {
		_, _ = fmt.Fprintln(stderr, `{"ready":false,"readOnly":true,"activationCapable":false,"reasons":["usage: --manifest <absolute-json-path>"]}`)
		return 2
	}
	path := args[1]
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		_, _ = fmt.Fprintln(stderr, `{"ready":false,"readOnly":true,"activationCapable":false,"reasons":["unsafe_manifest_path"]}`)
		return 2
	}
	body, err := strideE10W7W8ReadPrivateManifest(path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, `{"ready":false,"readOnly":true,"activationCapable":false,"reasons":["unsafe_manifest_file"]}`)
		return 2
	}
	var manifest StrideE10W7W8SuccessorManifest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(&struct{}{}) == nil {
		_, _ = fmt.Fprintln(stderr, `{"ready":false,"readOnly":true,"activationCapable":false,"reasons":["manifest_decode_failed"]}`)
		return 2
	}
	report := buildStrideE10W7W8SuccessorReadOnlyReportAt(&manifest, now)
	encoded, _ := json.Marshal(report)
	_, _ = fmt.Fprintln(stdout, string(encoded))
	if !report.Ready {
		return 1
	}
	return 0
}

func strideE10W7W8ReadPrivateManifest(path string) ([]byte, error) {
	return strideE10W7W8ReadPrivateManifestAt(path, nil)
}

func strideE10W7W8ReadPrivateManifestAt(path string, afterOpen func()) ([]byte, error) {
	parts := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(parts) < 2 {
		return nil, ErrStrideE10W7W8SuccessorNotReady
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(fd) }()
	for _, part := range parts[:len(parts)-1] {
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
		if openErr != nil {
			return nil, openErr
		}
		_ = unix.Close(fd)
		fd = next
	}
	var parent unix.Stat_t
	if unix.Fstat(fd, &parent) != nil || parent.Uid != uint32(os.Geteuid()) || parent.Mode&unix.S_IFMT != unix.S_IFDIR || parent.Mode&0o077 != 0 {
		return nil, ErrStrideE10W7W8SuccessorNotReady
	}
	fileFD, err := unix.Openat(fd, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var before unix.Stat_t
	if unix.Fstat(fileFD, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Mode&0o777 != 0o600 || before.Nlink != 1 || before.Uid != uint32(os.Geteuid()) || before.Size < 2 || before.Size > 1<<20 {
		_ = unix.Close(fileFD)
		return nil, ErrStrideE10W7W8SuccessorNotReady
	}
	file := os.NewFile(uintptr(fileFD), path)
	if file == nil {
		_ = unix.Close(fileFD)
		return nil, ErrStrideE10W7W8SuccessorNotReady
	}
	defer file.Close()
	if afterOpen != nil {
		afterOpen()
	}
	beforeInfo, statErr := file.Stat()
	if statErr != nil {
		return nil, ErrStrideE10W7W8SuccessorNotReady
	}
	body, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	var after unix.Stat_t
	afterInfo, afterStatErr := file.Stat()
	currentFD, currentErr := unix.Openat(fd, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if currentErr == nil {
		defer unix.Close(currentFD)
	}
	var current unix.Stat_t
	if err != nil || afterStatErr != nil || currentErr != nil || unix.Fstat(currentFD, &current) != nil || len(body) != int(before.Size) || unix.Fstat(int(file.Fd()), &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size || before.Dev != current.Dev || before.Ino != current.Ino || !os.SameFile(beforeInfo, afterInfo) || beforeInfo.ModTime() != afterInfo.ModTime() || len(body) > 1<<20 {
		return nil, ErrStrideE10W7W8SuccessorNotReady
	}
	return body, nil
}
