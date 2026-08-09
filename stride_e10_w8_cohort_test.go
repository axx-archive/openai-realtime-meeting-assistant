package main

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

type StrideE10W8TrustPolicy struct {
	CohortOperator, SoakObserver StrideE10W7TrustedKey
	Keys                         map[string]StrideE10W7TrustedKey
	PolicyDigest                 string
}

func (p StrideE10W8TrustPolicy) ResolveStrideE10W8Trust(kind string) (StrideE10W7TrustedKey, error) {
	if kind == "cohort_activation" {
		return p.CohortOperator, nil
	}
	if kind == "production_soak" {
		return p.SoakObserver, nil
	}
	key, ok := p.Keys[kind]
	if !ok {
		return StrideE10W7TrustedKey{}, ErrStrideE10W8NotReady
	}
	return key, nil
}
func (p StrideE10W8TrustPolicy) StrideE10W8TrustPolicyDigest() string { return p.PolicyDigest }

func strideE10W8TestRootKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("w8-untrusted-test-root"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func validateStrideE10W8TestActivation(t *testing.T, manifest StrideE10W8ActivationManifest) StrideE10W8PreflightResult {
	t.Helper()
	return ValidateStrideE10W8Activation(manifest)
}

func sealStrideE10W8TestManifest(t *testing.T, manifest *StrideE10W8ActivationManifest, trust StrideE10W8TrustPolicy, root ed25519.PrivateKey) {
	t.Helper()
	keys := make(map[string]StrideE10W7TrustedKey, len(trust.Keys)+2)
	for kind, key := range trust.Keys {
		keys[kind] = key
	}
	keys["cohort_activation"] = trust.CohortOperator
	keys["production_soak"] = trust.SoakObserver
	manifest.RootPolicy = StrideE10W8RootPolicy{Schema: "stride.e10.w8.root-policy.v1", RootKeyID: strideE10W8CompiledRootKeyID, PolicyID: "w8-test-policy", Keys: keys}
	manifest.RootPolicy.ManifestDigest = strideE10W8ManifestBindingDigest(*manifest)
	manifest.TrustPolicyDigest = strideE10W8PolicyDigest(manifest.RootPolicy)
	input, err := strideE10W8RootPolicyInput(manifest.RootPolicy)
	if err != nil {
		t.Fatal(err)
	}
	manifest.RootPolicy.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(root, input))
}

const strideE10W8SealedFixture = "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"

func strideE10W8TestManifest(t *testing.T) (StrideE10W8ActivationManifest, StrideE10W8TrustPolicy, ed25519.PrivateKey, ed25519.PrivateKey) {
	t.Helper()
	compressed, err := base64.StdEncoding.DecodeString(strideE10W8SealedFixture)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil || reader.Close() != nil {
		t.Fatal(err)
	}
	var manifest StrideE10W8ActivationManifest
	if json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("invalid sealed W8 fixture")
	}
	trust := StrideE10W8TrustPolicy{Keys: map[string]StrideE10W7TrustedKey{}, PolicyDigest: manifest.TrustPolicyDigest}
	for kind, key := range manifest.RootPolicy.Keys {
		switch kind {
		case "cohort_activation":
			trust.CohortOperator = key
		case "production_soak":
			trust.SoakObserver = key
		default:
			trust.Keys[kind] = key
		}
	}
	cohortSeed := sha256.Sum256([]byte("w8-cohort-key"))
	soakSeed := sha256.Sum256([]byte("w8-soak-key"))
	return manifest, trust, ed25519.NewKeyFromSeed(cohortSeed[:]), ed25519.NewKeyFromSeed(soakSeed[:])
}

func strideE10W8DynamicTestManifest(t *testing.T) (StrideE10W8ActivationManifest, StrideE10W8TrustPolicy, ed25519.PrivateKey, ed25519.PrivateKey) {
	t.Helper()
	root := strideE10W8TestRootKey()
	frozen := time.Now().UTC().Truncate(time.Second)
	commit := strings.Repeat("b", 40)
	cohortSeed := sha256.Sum256([]byte("w8-cohort-key"))
	soakSeed := sha256.Sum256([]byte("w8-soak-key"))
	cohortKey := ed25519.NewKeyFromSeed(cohortSeed[:])
	soakKey := ed25519.NewKeyFromSeed(soakSeed[:])
	trust := StrideE10W8TrustPolicy{
		CohortOperator: StrideE10W7TrustedKey{KeyID: "w8-cohort-operator", PublicKey: base64.StdEncoding.EncodeToString(cohortKey.Public().(ed25519.PublicKey))},
		SoakObserver:   StrideE10W7TrustedKey{KeyID: "w8-independent-soak", PublicKey: base64.StdEncoding.EncodeToString(soakKey.Public().(ed25519.PublicKey))},
		Keys:           map[string]StrideE10W7TrustedKey{}, PolicyDigest: strideE10W7TestDigest("pinned-w8-policy"),
	}
	manifest := StrideE10W8ActivationManifest{Schema: "stride.e10.w8.activation-manifest.v1", CandidateCommit: commit, TrustPolicyDigest: trust.PolicyDigest, RouteMapDigest: strideE10W7TestDigest("route-map"), W7ManifestDigest: strideE10W7TestDigest("w7"), W5Disposition: "aj_explicitly_deferred", W5DecisionDigest: strideE10W7TestDigest("w5"), W6QualificationDigest: strideE10W7TestDigest("w6"), RollbackReceiptDigest: strideE10W7TestDigest("rollback"), FrozenAt: frozen}
	keys := map[string]ed25519.PrivateKey{}
	for _, kind := range []string{"w7_result", "aj_w5_decision", "w6_qualification", "rollback_readiness", "cohort_activation_effect", "kill_switch_test", "sitting_observation", "final_rollback"} {
		seed := sha256.Sum256([]byte("w8-" + kind))
		key := ed25519.NewKeyFromSeed(seed[:])
		keys[kind] = key
		trust.Keys[kind] = StrideE10W7TrustedKey{KeyID: "key-" + kind, PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))}
	}
	for _, dep := range []struct {
		kind, digest, disposition string
		target                    *StrideE10W8SignedReceipt
	}{{"w7_result", manifest.W7ManifestDigest, "ready", &manifest.W7Result}, {"aj_w5_decision", manifest.W5DecisionDigest, manifest.W5Disposition, &manifest.W5Decision}, {"w6_qualification", manifest.W6QualificationDigest, "qualified", &manifest.W6Qualification}, {"rollback_readiness", manifest.RollbackReceiptDigest, "ready", &manifest.Rollback}} {
		*dep.target, _ = SignStrideE10W8Receipt(dep.kind, trust.Keys[dep.kind].KeyID, frozen.Add(-time.Hour), StrideE10W8DependencyReceipt{Source: "independent_signed", ReleaseCommit: commit, ManifestDigest: dep.digest, Disposition: dep.disposition, Ready: true}, keys[dep.kind])
	}
	previous := ""
	for index, name := range strideE10W8CohortOrder {
		receipt := StrideE10W8CohortReceipt{Source: "production_observed", Index: int64(index + 1), Name: name, ReleaseCommit: commit, RouteMapDigest: manifest.RouteMapDigest, TenantID: "tenant-bonfire", CohortID: "cohort-" + name, EnabledFeatures: append([]string(nil), strideE10W8CohortFeatures[name]...), KillSwitch: "kill-" + name, PreviousCohortReceiptDigest: previous, RollbackProven: true, PurgeFenceProven: true, ActivatedAt: frozen.Add(time.Duration(-30+index) * time.Hour)}
		raw, _ := json.Marshal(receipt)
		binding := strideE10W8ParentBindingDigest("cohort_activation", raw)
		for _, kind := range []string{"cohort_activation_effect", "kill_switch_test"} {
			sub, _ := SignStrideE10W8Receipt(kind, trust.Keys[kind].KeyID, receipt.ActivatedAt.Add(time.Minute), StrideE10W8BoundSubreceipt{Source: "production_observed", ReleaseCommit: commit, ParentKind: "cohort_activation", ParentPayloadDigest: binding, Verdict: "passed"}, keys[kind])
			manifest.Subreceipts = append(manifest.Subreceipts, sub)
			if kind == "cohort_activation_effect" {
				receipt.ActivationReceiptDigest = strideE10W8ReceiptDigest(sub)
			} else {
				receipt.KillSwitchTestReceiptDigest = strideE10W8ReceiptDigest(sub)
			}
		}
		signed, err := SignStrideE10W8Receipt("cohort_activation", trust.CohortOperator.KeyID, receipt.ActivatedAt.Add(time.Minute), receipt, cohortKey)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Cohorts = append(manifest.Cohorts, signed)
		previous = strideE10W8ReceiptDigest(signed)
	}
	killSwitches := make([]string, 0, len(strideE10W8CohortOrder))
	for _, name := range strideE10W8CohortOrder {
		killSwitches = append(killSwitches, "kill-"+name)
	}
	soak := StrideE10W8SoakReceipt{Source: "production_observed", ReleaseCommit: commit, RouteMapDigest: manifest.RouteMapDigest, W7ManifestDigest: manifest.W7ManifestDigest, LastRouteChangeAt: frozen.Add(-25 * time.Hour), StartedAt: frozen.Add(-24 * time.Hour), EndedAt: frozen, KillSwitchesExercised: killSwitches, ProductionObservation: true, ExactReleaseUnchanged: true, FinalRollbackProven: true}
	for index := 0; index < 10; index++ {
		started := soak.StartedAt.Add(time.Duration(index*2) * time.Hour)
		sitting := StrideE10W8Sitting{ID: "sitting-" + string(rune('a'+index)), StartedAt: started, EndedAt: started.Add(time.Hour), Participants: 3, CohortsObserved: append([]string(nil), strideE10W8CohortOrder...), RevokeLatencySeconds: 30, PurgeLatencySeconds: 60}
		raw, _ := json.Marshal(sitting)
		d := sha256.Sum256(raw)
		sub, _ := SignStrideE10W8Receipt("sitting_observation", trust.Keys["sitting_observation"].KeyID, sitting.EndedAt, StrideE10W8BoundSubreceipt{Source: "production_observed", ReleaseCommit: commit, ParentKind: "sitting", ParentPayloadDigest: fmt.Sprintf("%x", d), Verdict: "passed"}, keys["sitting_observation"])
		manifest.Subreceipts = append(manifest.Subreceipts, sub)
		sitting.ReceiptDigest = strideE10W8ReceiptDigest(sub)
		soak.Sittings = append(soak.Sittings, sitting)
	}
	rawSoak, _ := json.Marshal(soak)
	sub, _ := SignStrideE10W8Receipt("final_rollback", trust.Keys["final_rollback"].KeyID, frozen, StrideE10W8BoundSubreceipt{Source: "production_observed", ReleaseCommit: commit, ParentKind: "production_soak", ParentPayloadDigest: strideE10W8ParentBindingDigest("production_soak", rawSoak), Verdict: "passed"}, keys["final_rollback"])
	manifest.Subreceipts = append(manifest.Subreceipts, sub)
	soak.FinalRollbackReceiptDigest = strideE10W8ReceiptDigest(sub)
	manifest.Soak, _ = SignStrideE10W8Receipt("production_soak", trust.SoakObserver.KeyID, frozen, soak, soakKey)
	sealStrideE10W8TestManifest(t, &manifest, trust, root)
	return manifest, trust, cohortKey, soakKey
}

func TestStrideE10W8ActivationRequiresOrderedCohortsAndProductionSoak(t *testing.T) {
	manifest, _, _, _ := strideE10W8TestManifest(t)
	result := validateStrideE10W8TestActivation(t, manifest)
	if !result.Ready || result.Error() != nil || !strideE10W7Digest(result.ManifestDigest) {
		t.Fatalf("result=%+v err=%v", result, result.Error())
	}
}

func TestStrideE10W8ActivationRejectsMissingOutOfOrderAndSyntheticCohorts(t *testing.T) {
	manifest, _, _, _ := strideE10W8TestManifest(t)
	manifest.Cohorts[1], manifest.Cohorts[2] = manifest.Cohorts[2], manifest.Cohorts[1]
	result := validateStrideE10W8TestActivation(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w8_cohort_invalid_contribution_work_record_private") {
		t.Fatalf("order reasons=%v", result.Reasons)
	}

	manifest, trust, cohortKey, _ := strideE10W8TestManifest(t)
	var receipt StrideE10W8CohortReceipt
	_ = strideE10W7Decode(manifest.Cohorts[2].Payload, &receipt)
	receipt.Source = "synthetic"
	manifest.Cohorts[2], _ = SignStrideE10W8Receipt("cohort_activation", trust.CohortOperator.KeyID, manifest.Cohorts[2].ObservedAt, receipt, cohortKey)
	result = validateStrideE10W8TestActivation(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w8_cohort_invalid_network_publication") {
		t.Fatalf("synthetic reasons=%v", result.Reasons)
	}
}

func TestStrideE10W8ActivationRejectsShortOrUnsafeSoakAndTamper(t *testing.T) {
	manifest, trust, _, soakKey := strideE10W8TestManifest(t)
	var soak StrideE10W8SoakReceipt
	_ = strideE10W7Decode(manifest.Soak.Payload, &soak)
	soak.EndedAt = soak.StartedAt.Add(23 * time.Hour)
	soak.Sittings[0].PurgeLatencySeconds = 301
	manifest.Soak, _ = SignStrideE10W8Receipt("production_soak", trust.SoakObserver.KeyID, manifest.FrozenAt, soak, soakKey)
	result := validateStrideE10W8TestActivation(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w8_soak_invalid") || !containsSTRIDEString(result.Reasons, "w8_sitting_invalid_sitting-a") {
		t.Fatalf("unsafe soak reasons=%v", result.Reasons)
	}

	manifest, trust, _, _ = strideE10W8TestManifest(t)
	manifest.Soak.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	result = validateStrideE10W8TestActivation(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w8_soak_signature_invalid") {
		t.Fatalf("tamper reasons=%v", result.Reasons)
	}
}

func TestStrideE10W8ActivationRequiresExplicitW5DecisionAndW6Receipt(t *testing.T) {
	manifest, _, _, _ := strideE10W8TestManifest(t)
	manifest.W5Disposition = "pending"
	manifest.W6QualificationDigest = ""
	result := validateStrideE10W8TestActivation(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w8_manifest_invalid") {
		t.Fatalf("dependency reasons=%v", result.Reasons)
	}
}

func TestStrideE10W8ActivationRequiresIndependentSoakObserver(t *testing.T) {
	manifest, trust, _, _ := strideE10W8TestManifest(t)
	trust.SoakObserver = trust.CohortOperator
	sealStrideE10W8TestManifest(t, &manifest, trust, strideE10W8TestRootKey())
	result := validateStrideE10W8TestActivation(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w8_soak_observer_not_independent") {
		t.Fatalf("reasons=%v", result.Reasons)
	}
}

func TestStrideE10W8ActivationRejectsSelfSignedDependencyAndMissingBoundEvidence(t *testing.T) {
	manifest, _, _, _ := strideE10W8TestManifest(t)
	seed := sha256.Sum256([]byte("replacement-w7-result"))
	attacker := ed25519.NewKeyFromSeed(seed[:])
	var payload StrideE10W8DependencyReceipt
	_ = strideE10W7Decode(manifest.W7Result.Payload, &payload)
	manifest.W7Result, _ = SignStrideE10W8Receipt("w7_result", "attacker", manifest.W7Result.ObservedAt, payload, attacker)
	result := validateStrideE10W8TestActivation(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w8_dependency_invalid_w7_result") {
		t.Fatalf("replacement reasons=%v", result.Reasons)
	}
	manifest, _, _, _ = strideE10W8TestManifest(t)
	manifest.Subreceipts = nil
	result = validateStrideE10W8TestActivation(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w8_cohort_subreceipt_unresolved_organization_profile_private") || !containsSTRIDEString(result.Reasons, "w8_final_rollback_unresolved") {
		t.Fatalf("missing evidence reasons=%v", result.Reasons)
	}
}

func TestStrideE10W8ActivationRejectsWhollyReplacedTrustUniverse(t *testing.T) {
	manifest, trust, _, _ := strideE10W8TestManifest(t)
	rootSeed := sha256.Sum256([]byte("w8-attacker-root"))
	attackerRoot := ed25519.NewKeyFromSeed(rootSeed[:])
	attackerKeys := map[string]ed25519.PrivateKey{}
	for kind := range manifest.RootPolicy.Keys {
		seed := sha256.Sum256([]byte("w8-attacker-" + kind))
		key := ed25519.NewKeyFromSeed(seed[:])
		attackerKeys[kind] = key
		trusted := StrideE10W7TrustedKey{KeyID: "attacker-" + kind, PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))}
		if kind == "cohort_activation" {
			trust.CohortOperator = trusted
		} else if kind == "production_soak" {
			trust.SoakObserver = trusted
		} else {
			trust.Keys[kind] = trusted
		}
	}
	resign := func(receipt StrideE10W8SignedReceipt) StrideE10W8SignedReceipt {
		result, _ := SignStrideE10W8Receipt(receipt.Kind, "attacker-"+receipt.Kind, receipt.ObservedAt, json.RawMessage(receipt.Payload), attackerKeys[receipt.Kind])
		return result
	}
	manifest.W7Result = resign(manifest.W7Result)
	manifest.W5Decision = resign(manifest.W5Decision)
	manifest.W6Qualification = resign(manifest.W6Qualification)
	manifest.Rollback = resign(manifest.Rollback)
	manifest.Soak = resign(manifest.Soak)
	for index := range manifest.Cohorts {
		manifest.Cohorts[index] = resign(manifest.Cohorts[index])
	}
	for index := range manifest.Subreceipts {
		manifest.Subreceipts[index] = resign(manifest.Subreceipts[index])
	}
	sealStrideE10W8TestManifest(t, &manifest, trust, attackerRoot)
	result := validateStrideE10W8TestActivation(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w8_manifest_invalid") {
		t.Fatalf("replacement universe reasons=%v", result.Reasons)
	}
}

func TestStrideE10W8ActivationRejectsSignerRoleCollisions(t *testing.T) {
	manifest, trust, _, _ := strideE10W8TestManifest(t)
	trust.Keys["w6_qualification"] = trust.Keys["w7_result"]
	sealStrideE10W8TestManifest(t, &manifest, trust, strideE10W8TestRootKey())
	result := validateStrideE10W8TestActivation(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w8_signer_roles_not_independent") {
		t.Fatalf("signer collision reasons=%v", result.Reasons)
	}
}
