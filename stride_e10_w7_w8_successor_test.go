package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const strideE10W7W8SuccessorSealedFixture = `{"schema":"stride.e10.w7-w8.successor-acceptance.v1","candidateCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","candidateBuild":50,"nativeArtifactDigest":"9c2ce3baaa1ea56a27097c2b1be1843271101091e1dea9e84e11c708d2449ca4","legacyW7ValidatorDigest":"9944efc178ab3a7f45108ab4f0fb3649ccf509d400851deee809cf14f3712375","legacyW8ValidatorDigest":"908ea9b3cc41a7637656abddb84118c46ce2ab1cfcc07ceffce9641677f97caf","w6QualificationDigest":"817c3bdc1278fc6ea7c6c6f38efe9ea3d6dfccfa4465774b042e5086ab75f998","w7AcceptanceDigest":"70ef0cbf4f15dbacff7a5c9acedf46e859c54879a5f3ec14697d090d7696d79f","trustPolicyDigest":"38bba30f6aa97f7ca47e4d33efaa68d300d41e4800b105756e229f056512c607","rootPolicy":{"schema":"stride.e10.w7-w8.successor-root-policy.v1","rootKeyId":"stride-e10-w7-w8-successor-root-2026-08","policyId":"successor-policy-2026-08","manifestDigest":"9c36be92dc33070fa079dba4eb789ac3a561ecf6af15ffeb9770896d121ca1dc","keys":{"acceptance_environment":{"keyId":"successor-key-acceptance_environment","publicKey":"dTsEWIAhjT0B2Mou0j609B3TwP1HmY468PorC2dYtA8="},"pd0_result":{"keyId":"successor-key-pd0_result","publicKey":"dXGTrFA9c7Pz1be2rltZq6hM8t7ZBwFo6/grX4W4IQA="},"pd1_pilot_result":{"keyId":"successor-key-pd1_pilot_result","publicKey":"oC8DZlvILHPDytekUacbLWEa9nPN3Q6fluryN7AGD3M="},"pi0_a_result":{"keyId":"successor-key-pi0_a_result","publicKey":"E+f1KlwE7xKrubRbv6+KVgh4qA/GUwQudAPL2fvwU0Q="},"pi0_b_result":{"keyId":"successor-key-pi0_b_result","publicKey":"JxsvTnznKZzKOekToc1mkm1yr79Re03NvjDaD67+ED4="},"pn_critic_result":{"keyId":"successor-key-pn_critic_result","publicKey":"DiZ2jQs84rSm0AmlgByoZNnncfIDx0h4yxeIZhathzQ="},"pn_normative_result":{"keyId":"successor-key-pn_normative_result","publicKey":"wTqXYR3KLW5whEzTQJnCRqqs5uBamd/Sz34jVfhCCNw="},"rollback_readiness_result":{"keyId":"successor-key-rollback_readiness_result","publicKey":"4aYK5C1t0dZKNY9Xol6avHv9LwS+GYdLcKygXr2bwjM="},"w5_governance_result":{"keyId":"successor-key-w5_governance_result","publicKey":"NgiTWtUJAro0HnDd6HaP66WGUllG5Dj6lecBqRBWC8s="},"w6_qualification_result":{"keyId":"successor-key-w6_qualification_result","publicKey":"Q50Fg3gLDp6svQJwy4KKSGQgK+BH73v/YiShAu6V0Zg="},"w7_acceptance_result":{"keyId":"successor-key-w7_acceptance_result","publicKey":"cEswbPuoNIGO63p7NO+zEU9WGetUhuYOK0WTPjFUPwE="},"w8_activation_result":{"keyId":"successor-key-w8_activation_result","publicKey":"V1uN9VNqItHf6wz0vHI3JdChQw0s2DeYu/nRlCuRuRg="}},"signature":"93NWvNQkR6LXIlUlFuWQkn5u7RrSLjBg4U2JjLIvQq6NDsli/XXETBnu73+qbuEqV8L2KK/fmbbKeOZ3xaWdDg=="},"frozenAt":"2026-08-09T20:01:00Z","dependencies":[{"keyId":"successor-key-pd0_result","kind":"pd0_result","observedAt":"2026-08-09T20:00:00Z","payload":{"disposition":"implementation_accepted","evidenceDigest":"43f8079576a53ee5061b7ec724d6ec59eea20e44935c618d9fc3527a1b86f7ad","independent":true,"releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":"independent_signed"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"RbXjM5Um87hT7U1T7jzqm4vSMJNBITNlzJEHucTiJKnDTnxPqAhHy49uzGGKdN6j2ng0Bs41xK0Z9j2w5whLCQ=="},{"keyId":"successor-key-pd1_pilot_result","kind":"pd1_pilot_result","observedAt":"2026-08-09T20:00:00Z","payload":{"disposition":"pilot_subset_accepted","evidenceDigest":"ec7193af32f40aed2e8f0d72285424650d143728006bdb7c16f617695f9e4cd2","independent":true,"releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":"independent_signed"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"OSIDJQj1ugSH+bKaTx8bTMOBAIGCqukhQ0MtbLdF/fdC5NWBtTGF6DHjY5XacRkyG71lcIFxSZJc1/UwRtdeDQ=="},{"keyId":"successor-key-pi0_a_result","kind":"pi0_a_result","observedAt":"2026-08-09T20:00:00Z","payload":{"disposition":"implementation_and_baseline_accepted","evidenceDigest":"a11a08f2087738ccffb77a498295076183b4f5f4b7ed118cb3449ce1a2f7e247","independent":true,"releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":"independent_signed"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"3S+LKhVs0gprjq31jxKi5MDwyjSJFbr0SIV1/QJwPEIOBZqFtSPAGDVPWvMQSsNmRWLDRdDcc8hyp8X2n3zjDA=="},{"keyId":"successor-key-pi0_b_result","kind":"pi0_b_result","observedAt":"2026-08-09T20:00:00Z","payload":{"disposition":"collection_gate_accepted","evidenceDigest":"e98ac39923f8849760c378f373021f1ca16162f84e433aef3b6c88614af3ddf9","independent":true,"releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":"independent_signed"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"TFe6KY8U4z5d28morS1D3Egw6nHNsRu/aGDp7Oxucr6mzuEYVcPY2GSRY2C1XH8pN0fSMgEOeM4mppKVNdzXDw=="},{"keyId":"successor-key-pn_normative_result","kind":"pn_normative_result","observedAt":"2026-08-09T20:00:00Z","payload":{"disposition":"normative_freeze_accepted","evidenceDigest":"03f948a59d7b7652cbbd2512e5b8265c794f6f5793d4a239f3cf4b3bc4e97a36","independent":true,"releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":"independent_signed"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"1Y91ya5uvzrYg+kPbW7gpLnvs/4cRePwm/qShRA+YHAP8p6lPkId3BjCOK3qRA0JRLD2uBalk6zbIwCx0Ao5BA=="},{"keyId":"successor-key-pn_critic_result","kind":"pn_critic_result","observedAt":"2026-08-09T20:00:00Z","payload":{"disposition":"passed","evidenceDigest":"1818aded467b2cfefed2192af23ad8fc1c06aec17214f69fd66902da6278a4d9","independent":true,"releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":"independent_signed"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"y6P+9tZfcDL9SU+lbvz1ehyS0a0MQirbI8guWMEF+Lftv3wRfZwXgjSkpa/W4ntiPUNX5yBYdxBC0Mei9ErwDg=="},{"keyId":"successor-key-w6_qualification_result","kind":"w6_qualification_result","observedAt":"2026-08-09T20:00:00Z","payload":{"disposition":"qualified","evidenceDigest":"817c3bdc1278fc6ea7c6c6f38efe9ea3d6dfccfa4465774b042e5086ab75f998","independent":true,"releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":"independent_signed"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"nUvmkJiD0/GHlYYXvzyubwceIL7NULoQZ8Chi5XkLZxqySvaHdaumbfcKOPfoCTu2pzaGOTh3L5IPxivRAfYDA=="},{"keyId":"successor-key-acceptance_environment","kind":"acceptance_environment","observedAt":"2026-08-09T20:00:00Z","payload":{"classification":"isolated_private_w6_qualification","cohortId":"cohort-private-w6","environmentDestroyedAfter":true,"killSwitchBound":true,"nativeArtifactDigest":"9c2ce3baaa1ea56a27097c2b1be1843271101091e1dea9e84e11c708d2449ca4","nativeBuild":50,"openWebEnabled":false,"privateIngress":true,"productionCohortActivated":false,"publicRoutesEnabled":false,"purgeFenceBound":true,"releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":"independent_observed","tenantId":"tenant-private-w6","w6QualificationDigest":"817c3bdc1278fc6ea7c6c6f38efe9ea3d6dfccfa4465774b042e5086ab75f998"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"pGrb7EOlR0y9bs0jcR6loj07xrssjg63mkOBDywAuzxCkz+G1D86oyy+xGmexXR2hlUjPfxtcO3HlKW3JrGcBg=="},{"keyId":"successor-key-w5_governance_result","kind":"w5_governance_result","observedAt":"2026-08-09T20:00:00Z","payload":{"decision":"governed_deferral","evidenceDigest":"7e5ae3b074e36c8915b604158a742d472063b5c04d3ba8bf5f8f30ec831cb002","independent":true,"releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":"independent_signed"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"izB9GaGuJU48Mg1Tii2/Vott1HVQMdMoxwWa/Ja5m4cB4ree/S/P0MQoLpm2g5HBDK08dpRB2LJ8PV7CqHilDQ=="},{"keyId":"successor-key-rollback_readiness_result","kind":"rollback_readiness_result","observedAt":"2026-08-09T20:00:00Z","payload":{"candidateBuild":50,"independent":true,"manifestDigest":"aa1deca195afd98742691df39e32a40e7e6a57a38546d7a7366a2c86e0b01c2c","nativeArtifactDigest":"9c2ce3baaa1ea56a27097c2b1be1843271101091e1dea9e84e11c708d2449ca4","ready":true,"releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resultDigest":"c991d91ae43772921be6c50cdac0949d1246fd2c311205b47788871dae9e7502","source":"independent_signed"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"LAfI5iDRXzb11rAcKgGgaehL3yrRCCGUC7kA+dpseqqinOjX+Wy2tqxoWZdepB8b/F7HeP5jJGlqzqbVDActCA=="},{"keyId":"successor-key-w7_acceptance_result","kind":"w7_acceptance_result","observedAt":"2026-08-09T20:00:00Z","payload":{"activationComplete":false,"candidateBuild":50,"independent":true,"manifestDigest":"acc88e7cd23a259d2251b4bedd5a824dff6bf885dc1ef8560b6c51b7fd1318e3","nativeArtifactDigest":"9c2ce3baaa1ea56a27097c2b1be1843271101091e1dea9e84e11c708d2449ca4","releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resultDigest":"70ef0cbf4f15dbacff7a5c9acedf46e859c54879a5f3ec14697d090d7696d79f","sittingCount":0,"soakHours":0,"source":"independent_signed","validatorDigest":"9944efc178ab3a7f45108ab4f0fb3649ccf509d400851deee809cf14f3712375","verdict":"ready"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"w78ZfZirO9kVYXKz0J+D9vSRv9jGlMGjthx618rRXd4TsSyDEEUE62oynyI2laQIoqfuOHVHFqlBcJ18yeGZDg=="},{"keyId":"successor-key-w8_activation_result","kind":"w8_activation_result","observedAt":"2026-08-09T20:00:00Z","payload":{"activationComplete":true,"candidateBuild":50,"independent":true,"manifestDigest":"2f0c65ac351641b1904049a8009e6d14bdd649a10a084aed8868226c64064fb9","nativeArtifactDigest":"9c2ce3baaa1ea56a27097c2b1be1843271101091e1dea9e84e11c708d2449ca4","releaseCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resultDigest":"cc7231d105d76f55fc1bc97f37da502a8ddb721b45235731813031cf6971a1aa","sittingCount":10,"soakHours":24,"source":"independent_signed","validatorDigest":"908ea9b3cc41a7637656abddb84118c46ce2ab1cfcc07ceffce9641677f97caf","verdict":"activated_and_soaked"},"schema":"stride.e10.w7-w8.successor-dependency.v1","signature":"bkJyggt3Ng2QEHI4jgx7Nrfg7HvZFpCFXDmiO5ar0eGstSwp0MdL0EmbFKJNeJs7F7joShiIKxKT0hnwSWj6Bw=="}]}
`

func strideE10W7W8SuccessorFixture(t *testing.T) StrideE10W7W8SuccessorManifest {
	t.Helper()
	var manifest StrideE10W7W8SuccessorManifest
	if err := json.Unmarshal([]byte(strideE10W7W8SuccessorSealedFixture), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func strideE10W7W8SuccessorHasReason(reasons []string, reason string) bool {
	return slices.Contains(reasons, reason)
}

func TestStrideE10W7W8SuccessorAcceptsRootSignedPrerequisitesButRequiresNewFinalLineage(t *testing.T) {
	manifest := strideE10W7W8SuccessorFixture(t)
	result := validateStrideE10W7W8SuccessorAcceptanceAt(manifest, manifest.FrozenAt.Add(time.Minute))
	if !result.PrerequisitesReady || result.FinalReady || result.Ready || result.ExternallySealedFinalReady || !strideE10W7W8SuccessorHasReason(result.FinalReasons, "successor_final_dependency_invalid_w8_activation_result") || !strideE10W7W8SuccessorHasReason(result.FinalReasons, "successor_externally_sealed_ready_fixture_required") || !strideE10W7Digest(result.ManifestDigest) || result.FinalManifestDigest != "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestStrideE10W7W8SuccessorAllowsFreshEvidenceAfterStaticPolicyFreeze(t *testing.T) {
	policyFreeze := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	observed := policyFreeze.Add(25 * time.Hour)
	now := observed.Add(time.Minute)
	if !strideE10W7W8SuccessorEvidenceTimeValid(policyFreeze, observed, now) {
		t.Fatal("fresh post-freeze W8 evidence was rejected")
	}
	if strideE10W7W8SuccessorEvidenceTimeValid(policyFreeze, now.Add(6*time.Minute), now) {
		t.Fatal("future evidence passed")
	}
	if strideE10W7W8SuccessorEvidenceTimeValid(policyFreeze, now.Add(-31*24*time.Hour), now) {
		t.Fatal("stale evidence passed")
	}
}

func TestStrideE10W7W8SuccessorSeparatesPrivatePrerequisitesFromFinalReadiness(t *testing.T) {
	manifest := strideE10W7W8SuccessorFixture(t)
	dependencies := manifest.Dependencies[:0]
	for _, dependency := range manifest.Dependencies {
		if !containsSTRIDEString(strideE10W7W8SuccessorFinalKinds, dependency.Kind) {
			dependencies = append(dependencies, dependency)
		}
	}
	manifest.Dependencies = dependencies
	result := validateStrideE10W7W8SuccessorAcceptanceAt(manifest, manifest.FrozenAt.Add(time.Minute))
	if !result.PrerequisitesReady || result.FinalReady || result.Ready || !strideE10W7W8SuccessorHasReason(result.FinalReasons, "successor_missing_w8_activation_result") {
		t.Fatalf("result=%+v", result)
	}
}

func TestStrideE10W7W8SuccessorFinalEvidenceFailuresDoNotInvalidatePrerequisites(t *testing.T) {
	baselineManifest := strideE10W7W8SuccessorFixture(t)
	baseline := validateStrideE10W7W8SuccessorAcceptanceAt(baselineManifest, baselineManifest.FrozenAt.Add(time.Minute))
	for _, test := range []struct {
		name   string
		reason string
		mutate func(*StrideE10W7W8SuccessorManifest)
	}{
		{
			name:   "duplicate final",
			reason: "successor_duplicate_w8_activation_result",
			mutate: func(manifest *StrideE10W7W8SuccessorManifest) {
				for _, dependency := range manifest.Dependencies {
					if dependency.Kind == "w8_activation_result" {
						manifest.Dependencies = append(manifest.Dependencies, dependency)
						return
					}
				}
			},
		},
		{
			name:   "malformed final",
			reason: "successor_dependency_signature_invalid_w8_activation_result",
			mutate: func(manifest *StrideE10W7W8SuccessorManifest) {
				for index := range manifest.Dependencies {
					if manifest.Dependencies[index].Kind == "w8_activation_result" {
						manifest.Dependencies[index].Payload = json.RawMessage(`{"source":`)
						return
					}
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := strideE10W7W8SuccessorFixture(t)
			test.mutate(&manifest)
			result := validateStrideE10W7W8SuccessorAcceptanceAt(manifest, manifest.FrozenAt.Add(time.Minute))
			if !result.PrerequisitesReady || result.FinalReady || result.Ready || len(result.PrerequisiteReasons) != 0 || !strideE10W7W8SuccessorHasReason(result.FinalReasons, test.reason) || result.ManifestDigest != baseline.ManifestDigest {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestStrideE10W7W8SuccessorFinalW8RequiresExactW7AndRollbackLineage(t *testing.T) {
	manifest := strideE10W7W8SuccessorFixture(t)
	byKind := make(map[string]StrideE10W7W8SuccessorSignedDependency, len(manifest.Dependencies))
	for _, dependency := range manifest.Dependencies {
		byKind[dependency.Kind] = dependency
	}
	var w7 StrideE10W7W8LegacyAcceptanceResult
	var rollback StrideE10W7W8RollbackReadinessResult
	var w8 StrideE10W7W8LegacyAcceptanceResult
	if json.Unmarshal(byKind["w7_acceptance_result"].Payload, &w7) != nil || json.Unmarshal(byKind["rollback_readiness_result"].Payload, &rollback) != nil || json.Unmarshal(byKind["w8_activation_result"].Payload, &w8) != nil {
		t.Fatal("decode fixture")
	}
	w8.W7ManifestDigest = w7.ManifestDigest
	w8.W7ResultDigest = w7.ResultDigest
	w8.RollbackManifestDigest = rollback.ManifestDigest
	w8.RollbackResultDigest = rollback.ResultDigest
	w8.DependencyVerificationDigest = strideE10W7W8FinalDependencyVerificationDigest(byKind["w7_acceptance_result"], byKind["rollback_readiness_result"])
	if !strideE10W7W8FinalLineageValid(w8, w7, rollback, byKind["w7_acceptance_result"], byKind["rollback_readiness_result"]) {
		t.Fatalf("lineage=%+v", w8)
	}
	for _, mutate := range []func(*StrideE10W7W8LegacyAcceptanceResult){
		func(result *StrideE10W7W8LegacyAcceptanceResult) { result.W7ManifestDigest = strings.Repeat("f", 64) },
		func(result *StrideE10W7W8LegacyAcceptanceResult) { result.W7ResultDigest = strings.Repeat("f", 64) },
		func(result *StrideE10W7W8LegacyAcceptanceResult) {
			result.RollbackManifestDigest = strings.Repeat("f", 64)
		},
		func(result *StrideE10W7W8LegacyAcceptanceResult) {
			result.RollbackResultDigest = strings.Repeat("f", 64)
		},
		func(result *StrideE10W7W8LegacyAcceptanceResult) {
			result.DependencyVerificationDigest = strings.Repeat("f", 64)
		},
	} {
		substituted := w8
		mutate(&substituted)
		if strideE10W7W8FinalLineageValid(substituted, w7, rollback, byKind["w7_acceptance_result"], byKind["rollback_readiness_result"]) {
			t.Fatal("substituted final lineage accepted")
		}
	}
}

func TestStrideE10W7W8SuccessorRejectsMissingStaleAndSubstitutedDependencies(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		manifest := strideE10W7W8SuccessorFixture(t)
		manifest.Dependencies = manifest.Dependencies[1:]
		result := validateStrideE10W7W8SuccessorAcceptanceAt(manifest, manifest.FrozenAt.Add(time.Minute))
		if result.Ready || !strideE10W7W8SuccessorHasReason(result.Reasons, "successor_missing_pd0_result") {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("stale", func(t *testing.T) {
		manifest := strideE10W7W8SuccessorFixture(t)
		manifest.Dependencies[0].ObservedAt = manifest.FrozenAt.Add(-31 * 24 * time.Hour)
		result := validateStrideE10W7W8SuccessorAcceptanceAt(manifest, manifest.FrozenAt.Add(time.Minute))
		if result.Ready || !strideE10W7W8SuccessorHasReason(result.Reasons, "successor_dependency_signature_invalid_pd0_result") {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("substitution", func(t *testing.T) {
		manifest := strideE10W7W8SuccessorFixture(t)
		var dependency StrideE10W7W8SuccessorDependency
		if err := json.Unmarshal(manifest.Dependencies[1].Payload, &dependency); err != nil {
			t.Fatal(err)
		}
		dependency.EvidenceDigest = strings.Repeat("f", 64)
		manifest.Dependencies[1].Payload, _ = json.Marshal(dependency)
		result := validateStrideE10W7W8SuccessorAcceptanceAt(manifest, manifest.FrozenAt.Add(time.Minute))
		if result.Ready || !strideE10W7W8SuccessorHasReason(result.Reasons, "successor_dependency_signature_invalid_pd1_pilot_result") {
			t.Fatalf("result=%+v", result)
		}
	})
}

func TestStrideE10W7W8SuccessorRejectsCircularPublicActivation(t *testing.T) {
	manifest := strideE10W7W8SuccessorFixture(t)
	for index := range manifest.Dependencies {
		if manifest.Dependencies[index].Kind != "acceptance_environment" {
			continue
		}
		var environment StrideE10W7W8AcceptanceEnvironment
		if err := json.Unmarshal(manifest.Dependencies[index].Payload, &environment); err != nil {
			t.Fatal(err)
		}
		environment.Classification = "public_w8_activation"
		environment.PrivateIngress = false
		environment.PublicRoutesEnabled = true
		environment.ProductionCohortActivated = true
		manifest.Dependencies[index].Payload, _ = json.Marshal(environment)
	}
	result := validateStrideE10W7W8SuccessorAcceptanceAt(manifest, manifest.FrozenAt.Add(time.Minute))
	if result.Ready || !strideE10W7W8SuccessorHasReason(result.Reasons, "successor_circular_public_activation_forbidden") {
		t.Fatalf("result=%+v", result)
	}
}

func TestStrideE10W7W8SuccessorBindsExactW6AndW7Results(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		mutate       func(*StrideE10W7W8SuccessorManifest)
	}{
		{"w6", "successor_w6_qualification_binding_invalid", func(manifest *StrideE10W7W8SuccessorManifest) {
			manifest.W6QualificationDigest = strings.Repeat("e", 64)
		}},
		{"w7", "successor_final_dependency_invalid_w7_acceptance_result", func(manifest *StrideE10W7W8SuccessorManifest) { manifest.W7AcceptanceDigest = strings.Repeat("e", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := strideE10W7W8SuccessorFixture(t)
			test.mutate(&manifest)
			result := validateStrideE10W7W8SuccessorAcceptanceAt(manifest, manifest.FrozenAt.Add(time.Minute))
			if result.Ready || !strideE10W7W8SuccessorHasReason(result.Reasons, test.reason) {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestStrideE10W7W8SuccessorRejectsReplacementTrustUniverse(t *testing.T) {
	manifest := strideE10W7W8SuccessorFixture(t)
	manifest.RootPolicy.Signature = strings.Repeat("A", 88)
	result := validateStrideE10W7W8SuccessorAcceptanceAt(manifest, manifest.FrozenAt.Add(time.Minute))
	if result.Ready || !strideE10W7W8SuccessorHasReason(result.Reasons, "successor_manifest_invalid") {
		t.Fatalf("result=%+v", result)
	}
}

func TestStrideE10W7W8SuccessorReadOnlyCLIHasNoMutationOrActivationSurface(t *testing.T) {
	dir := t.TempDir()
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(canonicalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(canonicalDir, "successor.json")
	if err := os.WriteFile(path, []byte(strideE10W7W8SuccessorSealedFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := strideE10W7W8ReadPrivateManifest(path); err != nil {
		t.Fatalf("canonical private manifest: %v path=%s", err, path)
	}
	before, _ := os.ReadFile(path)
	beforeDigest := sha256.Sum256(before)
	beforeEntries, _ := os.ReadDir(dir)
	var stdout, stderr bytes.Buffer
	manifest := strideE10W7W8SuccessorFixture(t)
	exit := runStrideE10W7W8SuccessorReadOnlyCLIAt([]string{"--manifest", path}, &stdout, &stderr, manifest.FrozenAt.Add(time.Minute))
	if exit != 1 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	var report StrideE10W7W8SuccessorOperatorReport
	if json.Unmarshal(stdout.Bytes(), &report) != nil || !report.PrerequisitesReady || report.FinalReady || report.Ready || !report.ReadOnly || report.ActivationCapable || !strideE10W7W8SuccessorHasReason(report.Reasons, "successor_final_dependency_invalid_w8_activation_result") {
		t.Fatalf("report=%+v", report)
	}
	after, _ := os.ReadFile(path)
	afterDigest := sha256.Sum256(after)
	afterEntries, _ := os.ReadDir(dir)
	if beforeDigest != afterDigest || len(beforeEntries) != len(afterEntries) {
		t.Fatalf("read-only CLI changed input: before=%s after=%s entries=%d/%d", hex.EncodeToString(beforeDigest[:]), hex.EncodeToString(afterDigest[:]), len(beforeEntries), len(afterEntries))
	}
	stdout.Reset()
	stderr.Reset()
	if got := RunStrideE10W7W8SuccessorReadOnlyCLI([]string{"--activate", path}, &stdout, &stderr); got != 2 {
		t.Fatalf("activation-like argument exit=%d", got)
	}
	after, _ = os.ReadFile(path)
	if sha256.Sum256(after) != beforeDigest {
		t.Fatal("rejected activation-like argument mutated manifest")
	}
	stdout.Reset()
	stderr.Reset()
	if handled, exit := HandleStrideE10W7W8SuccessorReadOnlyCLI([]string{strideE10W7W8SuccessorCLICommand, "--manifest", path}, &stdout, &stderr); !handled || exit != 1 {
		t.Fatalf("canonical handler handled=%t exit=%d stderr=%s", handled, exit, stderr.String())
	}
	if handled, exit := HandleStrideE10W7W8SuccessorReadOnlyCLI([]string{"serve", "--manifest", path}, &stdout, &stderr); handled || exit != 0 {
		t.Fatalf("unrelated command intercepted handled=%t exit=%d", handled, exit)
	}
}

func TestStrideE10W7W8SuccessorReadOnlyCLIRejectsLinkAndPermissionAmbiguity(t *testing.T) {
	manifest := strideE10W7W8SuccessorFixture(t)
	run := func(path string) int {
		var stdout, stderr bytes.Buffer
		return runStrideE10W7W8SuccessorReadOnlyCLIAt([]string{"--manifest", path}, &stdout, &stderr, manifest.FrozenAt.Add(time.Minute))
	}
	t.Run("hardlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.json")
		link := filepath.Join(dir, "link.json")
		if os.WriteFile(target, []byte(strideE10W7W8SuccessorSealedFixture), 0o600) != nil || os.Link(target, link) != nil {
			t.Fatal("fixture")
		}
		if run(link) != 2 {
			t.Fatal("hardlinked manifest passed")
		}
	})
	t.Run("symlink parent", func(t *testing.T) {
		root := t.TempDir()
		real := filepath.Join(root, "real")
		if os.Mkdir(real, 0o700) != nil || os.WriteFile(filepath.Join(real, "manifest.json"), []byte(strideE10W7W8SuccessorSealedFixture), 0o600) != nil || os.Symlink(real, filepath.Join(root, "linked")) != nil {
			t.Fatal("fixture")
		}
		if run(filepath.Join(root, "linked", "manifest.json")) != 2 {
			t.Fatal("symlink-parent manifest passed")
		}
	})
	t.Run("nonprivate parent", func(t *testing.T) {
		dir := t.TempDir()
		if os.Chmod(dir, 0o755) != nil {
			t.Fatal("chmod")
		}
		path := filepath.Join(dir, "manifest.json")
		if os.WriteFile(path, []byte(strideE10W7W8SuccessorSealedFixture), 0o600) != nil {
			t.Fatal("fixture")
		}
		if run(path) != 2 {
			t.Fatal("nonprivate parent passed")
		}
	})
	t.Run("path swap after open", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		replacement := filepath.Join(dir, "replacement.json")
		if os.WriteFile(path, []byte(strideE10W7W8SuccessorSealedFixture), 0o600) != nil || os.WriteFile(replacement, []byte(strideE10W7W8SuccessorSealedFixture), 0o600) != nil {
			t.Fatal("fixture")
		}
		_, err := strideE10W7W8ReadPrivateManifestAt(path, func() {
			if renameErr := os.Rename(replacement, path); renameErr != nil {
				t.Fatal(renameErr)
			}
		})
		if err == nil {
			t.Fatal("path replacement after descriptor open passed")
		}
	})
}
