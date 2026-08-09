package main

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

type StrideE10W7TrustPolicy struct {
	Keys         map[string]StrideE10W7TrustedKey
	PolicyDigest string
}

func (p StrideE10W7TrustPolicy) ResolveStrideE10W7Trust(kind string) (StrideE10W7TrustedKey, error) {
	key, ok := p.Keys[kind]
	if !ok {
		return StrideE10W7TrustedKey{}, ErrStrideE10W7NotReady
	}
	return key, nil
}
func (p StrideE10W7TrustPolicy) StrideE10W7TrustPolicyDigest() string { return p.PolicyDigest }

func strideE10W7TestDigest(label string) string {
	digest := sha256.Sum256([]byte(label))
	return hex.EncodeToString(digest[:])
}

func strideE10W7TestRootKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("w7-untrusted-test-root"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func validateStrideE10W7TestAcceptance(t *testing.T, manifest StrideE10W7AcceptanceManifest) StrideE10W7PreflightResult {
	t.Helper()
	return ValidateStrideE10W7Acceptance(manifest)
}

func sealStrideE10W7TestManifest(t *testing.T, manifest *StrideE10W7AcceptanceManifest, trust StrideE10W7TrustPolicy, root ed25519.PrivateKey) {
	t.Helper()
	manifest.RootPolicy = StrideE10W7RootPolicy{Schema: "stride.e10.w7.root-policy.v1", RootKeyID: strideE10W7CompiledRootKeyID, PolicyID: "w7-test-policy", Keys: trust.Keys}
	manifest.RootPolicy.ManifestDigest = strideE10W7ManifestBindingDigest(*manifest)
	manifest.TrustPolicyDigest = strideE10W7PolicyDigest(manifest.RootPolicy)
	input, err := strideE10W7RootPolicyInput(manifest.RootPolicy)
	if err != nil {
		t.Fatal(err)
	}
	manifest.RootPolicy.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(root, input))
}

const strideE10W7SealedFixture = "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"

func strideE10W7TestManifest(t *testing.T) (StrideE10W7AcceptanceManifest, StrideE10W7TrustPolicy, map[string]ed25519.PrivateKey) {
	t.Helper()
	compressed, err := base64.StdEncoding.DecodeString(strideE10W7SealedFixture)
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
	var manifest StrideE10W7AcceptanceManifest
	if json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("invalid sealed W7 fixture")
	}
	trust := StrideE10W7TrustPolicy{Keys: manifest.RootPolicy.Keys, PolicyDigest: manifest.TrustPolicyDigest}
	privateKeys := map[string]ed25519.PrivateKey{}
	for kind := range trust.Keys {
		seed := sha256.Sum256([]byte("w7-" + kind))
		privateKeys[kind] = ed25519.NewKeyFromSeed(seed[:])
	}
	return manifest, trust, privateKeys
}

func strideE10W7DynamicTestManifest(t *testing.T) (StrideE10W7AcceptanceManifest, StrideE10W7TrustPolicy, map[string]ed25519.PrivateKey) {
	t.Helper()
	root := strideE10W7TestRootKey()
	frozen := time.Now().UTC().Truncate(time.Second)
	commit := strings.Repeat("a", 40)
	policyDigest := strideE10W7TestDigest("pinned-w7-trust-policy")
	manifest := StrideE10W7AcceptanceManifest{Schema: "stride.e10.w7.acceptance-manifest.v1", CandidateCommit: commit, CandidateBuild: 50, RequiredTestFlightGroups: []string{"Bonfire", "Team (Expo)"}, TrustPolicyDigest: policyDigest, MaxRPOSeconds: 60, MaxRTOSeconds: 3600, FrozenAt: frozen}
	trust := StrideE10W7TrustPolicy{Keys: map[string]StrideE10W7TrustedKey{}, PolicyDigest: policyDigest}
	privateKeys := map[string]ed25519.PrivateKey{}
	allKinds := append(append([]string(nil), strideE10W7Kinds...), strideE10W7SubreceiptKinds...)
	for _, kind := range allKinds {
		seed := sha256.Sum256([]byte("w7-" + kind))
		privateKey := ed25519.NewKeyFromSeed(seed[:])
		trust.Keys[kind] = StrideE10W7TrustedKey{KeyID: "key-" + kind, PublicKey: base64.StdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey))}
		privateKeys[kind] = privateKey
	}
	artifact := strideE10W7TestDigest("exact-native-artifact")
	restore := StrideE10W7OffsiteRestore{Source: "production_drill", ReleaseCommit: commit, Encryption: "AES-256-GCM", ObjectLockMode: "COMPLIANCE", KMSKeyID: "kms-production-key", CloudTrailEvidenceDigest: strideE10W7TestDigest("cloudtrail"), Roots: []string{"usage_ledger", "meeting_data", "canonical_postgres", "codex_queue"}, PurgeAuthorityHeadDigest: strideE10W7TestDigest("purge"), IsolatedApplicationBoot: true, RestoreCurrentAuthority: true, RPOSeconds: 30, RTOSeconds: 300}
	ha := StrideE10W7HAFailover{Source: "production_drill", ReleaseCommit: commit, PostgresFailover: true, ApplicationFailover: true, TURNFailover: true, ReversibleTrafficShift: true, OldHostRetained: true}
	for _, binding := range []struct {
		kind, parent string
		value        any
	}{{"restore_execution", "encrypted_offsite_restore", restore}, {"postgres_failover", "ha_failover", ha}, {"application_failover", "ha_failover", ha}, {"turn_failover", "ha_failover", ha}, {"traffic_rollback", "ha_failover", ha}} {
		raw, _ := json.Marshal(binding.value)
		payload := StrideE10W7BoundSubreceipt{Source: "independent_observed", ReleaseCommit: commit, ParentKind: binding.parent, ParentPayloadDigest: strideE10W7ParentBindingDigest(binding.parent, raw), Verdict: "passed"}
		signed, err := SignStrideE10W7Evidence(binding.kind, trust.Keys[binding.kind].KeyID, frozen.Add(-2*time.Minute), payload, privateKeys[binding.kind])
		if err != nil {
			t.Fatal(err)
		}
		manifest.Subreceipts = append(manifest.Subreceipts, signed)
		d := strideE10W7EvidenceDigest(signed)
		switch binding.kind {
		case "restore_execution":
			restore.SignedRestoreReceiptDigest = d
		case "postgres_failover":
			ha.PostgresReceiptDigest = d
		case "application_failover":
			ha.ApplicationReceiptDigest = d
		case "turn_failover":
			ha.TURNReceiptDigest = d
		case "traffic_rollback":
			ha.RollbackReceiptDigest = d
		}
	}
	payloads := map[string]any{
		"native_distribution":             StrideE10W7NativeDistribution{Source: "external_observed", Commit: commit, BuildNumber: 50, EASBuildID: "eas-build-50", EASSubmissionID: "eas-submission-50", AppleBuildID: "apple-build-50", EASStatus: "FINISHED", SubmissionStatus: "FINISHED", AppleState: "VALID", BetaState: "IN_BETA_TESTING", Groups: []string{"Team (Expo)", "Bonfire"}, ProvenanceResolved: true, ArtifactDigest: artifact},
		"iphone_physical":                 strideE10W7PhysicalFixture("iphone", commit, 50, artifact),
		"ipad_physical":                   strideE10W7PhysicalFixture("ipad", commit, 50, artifact),
		"accessibility_privacy":           StrideE10W7AccessibilityPrivacy{Source: "external_approved", Commit: commit, BuildNumber: 50, PrivacyManifestDigest: strideE10W7TestDigest("privacy-manifest"), AppPrivacyDigest: strideE10W7TestDigest("app-privacy"), ProductApprover: "product-approver", LegalApprover: "legal-approver", PrivacyApprover: "privacy-approver", Checks: strideE10W7TrueMap([]string{"voiceover", "dynamic_type_xxl", "contrast", "reduced_motion", "focus_order", "hit_targets", "keyboard", "privacy_prompts", "background_privacy"})},
		"restrictive_turn_webrtc":         StrideE10W7TURNWebRTC{Source: "production_observed", ReleaseCommit: commit, NativeCommit: commit, NativeBuild: 50, RestrictiveNetwork: true, RealWebRTC: true, RelayOnly: true, RelayProtocols: []string{"udp", "tcp", "tls"}, BrowserNativeMixed: true, RoomCount: 2, ParticipantsPerRoom: 3, DurationMinutesPerRoom: 120, InboundRTPBytes: 1000, OutboundRTPBytes: 1000, BackgroundRecovery: true, AudioRouteChange: true, CameraSwitch: true, ConsentCurrent: true, InducedAIFailurePassed: true, ReceiptDigest: strideE10W7TestDigest("turn")},
		"encrypted_offsite_restore":       restore,
		"ha_failover":                     ha,
		"independent_release_attestation": StrideE10W7ReleaseAttestation{Source: "independent_external", ReleaseCommit: commit, GitTreeDigest: strideE10W7TestDigest("tree"), SourceArchiveDigest: strideE10W7TestDigest("archive"), NativeArtifactDigest: artifact, ImageDigest: strideE10W7TestDigest("server-image"), RunningImageDigest: strideE10W7TestDigest("server-image"), BinaryDigest: strideE10W7TestDigest("binary"), ConfigurationDigest: strideE10W7TestDigest("config"), IndependentSigner: true, OffHost: true, IssuedAt: frozen.Add(-time.Hour), ExpiresAt: frozen.Add(7 * 24 * time.Hour), AttestationBodyDigest: strideE10W7TestDigest("attestation")},
	}
	for _, kind := range strideE10W7Kinds {
		evidence, err := SignStrideE10W7Evidence(kind, trust.Keys[kind].KeyID, frozen.Add(-time.Minute), payloads[kind], privateKeys[kind])
		if err != nil {
			t.Fatal(err)
		}
		manifest.Evidence = append(manifest.Evidence, evidence)
	}
	sealStrideE10W7TestManifest(t, &manifest, trust, root)
	return manifest, trust, privateKeys
}

func strideE10W7PhysicalFixture(device, commit string, build int64, artifact string) StrideE10W7PhysicalDevice {
	return StrideE10W7PhysicalDevice{Source: "physical_device", DeviceClass: device, Physical: true, Commit: commit, BuildNumber: build, HardwareDigest: strideE10W7TestDigest(device + "-hardware"), OSVersion: "iOS 20.0", ArtifactDigest: artifact, Flows: strideE10W7TrueMap([]string{"organization", "work_record", "publish", "pause", "search", "evidence", "contact", "block", "revoke", "background_foreground", "locked_recovery", "push_deep_link"})}
}

func strideE10W7TrueMap(keys []string) map[string]bool {
	result := map[string]bool{}
	for _, key := range keys {
		result[key] = true
	}
	return result
}

func TestStrideE10W7AcceptanceRequiresEverySignedExternalGate(t *testing.T) {
	manifest, _, _ := strideE10W7TestManifest(t)
	result := validateStrideE10W7TestAcceptance(t, manifest)
	if !result.Ready || result.Error() != nil || !strideE10W7Digest(result.ManifestDigest) {
		t.Fatalf("result=%+v err=%v", result, result.Error())
	}
	missing := manifest
	missing.Evidence = nil
	result = validateStrideE10W7TestAcceptance(t, missing)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w7_missing_iphone_physical") || !containsSTRIDEString(result.Reasons, "w7_missing_independent_release_attestation") {
		t.Fatalf("missing reasons=%v", result.Reasons)
	}
}

func TestStrideE10W7AcceptanceFailsClosedOnTamperSyntheticAndWrongBuild(t *testing.T) {
	manifest, _, keys := strideE10W7TestManifest(t)
	for index := range manifest.Evidence {
		if manifest.Evidence[index].Kind == "iphone_physical" {
			manifest.Evidence[index].Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		}
	}
	result := validateStrideE10W7TestAcceptance(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w7_evidence_signature_invalid_iphone_physical") {
		t.Fatalf("tamper reasons=%v", result.Reasons)
	}

	manifest, _, keys = strideE10W7TestManifest(t)
	for index, evidence := range manifest.Evidence {
		if evidence.Kind == "native_distribution" {
			bad := StrideE10W7NativeDistribution{Source: "synthetic", Commit: manifest.CandidateCommit, BuildNumber: 49, EASBuildID: "old", EASSubmissionID: "old", AppleBuildID: "old", EASStatus: "FINISHED", SubmissionStatus: "FINISHED", AppleState: "VALID", BetaState: "IN_BETA_TESTING", Groups: manifest.RequiredTestFlightGroups, ProvenanceResolved: false, ArtifactDigest: strideE10W7TestDigest("old")}
			manifest.Evidence[index], _ = SignStrideE10W7Evidence(evidence.Kind, evidence.KeyID, evidence.ObservedAt, bad, keys[evidence.Kind])
		}
	}
	result = validateStrideE10W7TestAcceptance(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w7_native_distribution_invalid") {
		t.Fatalf("synthetic reasons=%v", result.Reasons)
	}
}

func TestStrideE10W7AcceptanceRejectsUnknownBodiesAndIncompleteDR(t *testing.T) {
	manifest, _, keys := strideE10W7TestManifest(t)
	for index, evidence := range manifest.Evidence {
		if evidence.Kind == "encrypted_offsite_restore" {
			bad := map[string]any{"source": "production_drill", "releaseCommit": manifest.CandidateCommit, "privateBackupBody": "forbidden"}
			manifest.Evidence[index], _ = SignStrideE10W7Evidence(evidence.Kind, evidence.KeyID, evidence.ObservedAt, bad, keys[evidence.Kind])
		}
	}
	result := validateStrideE10W7TestAcceptance(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w7_encrypted_offsite_restore_invalid") {
		t.Fatalf("unknown body reasons=%v", result.Reasons)
	}
}

func TestStrideE10W7AcceptanceRequiresIndependentAttestorAndManifestRecoveryTargets(t *testing.T) {
	manifest, trust, _ := strideE10W7TestManifest(t)
	manifest.RootPolicy.Keys["independent_release_attestation"] = manifest.RootPolicy.Keys["ha_failover"]
	trust.Keys = manifest.RootPolicy.Keys
	sealStrideE10W7TestManifest(t, &manifest, trust, strideE10W7TestRootKey())
	result := validateStrideE10W7TestAcceptance(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w7_independent_attestor_not_independent") {
		t.Fatalf("trust reasons=%v", result.Reasons)
	}

	manifest, _, _ = strideE10W7TestManifest(t)
	manifest.MaxRPOSeconds = 0
	result = validateStrideE10W7TestAcceptance(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w7_manifest_invalid") {
		t.Fatalf("recovery target reasons=%v", result.Reasons)
	}
}

func TestStrideE10W7AcceptanceRequiresPairwiseUniqueSignerRoles(t *testing.T) {
	tests := []struct {
		name   string
		target string
		source string
	}{
		{name: "native and physical device", target: "iphone_physical", source: "native_distribution"},
		{name: "device and privacy", target: "accessibility_privacy", source: "ipad_physical"},
		{name: "TURN and restore", target: "encrypted_offsite_restore", source: "restrictive_turn_webrtc"},
		{name: "restore and HA", target: "ha_failover", source: "encrypted_offsite_restore"},
		{name: "two failover subreceipts", target: "application_failover", source: "postgres_failover"},
		{name: "restore and failover subreceipt", target: "restore_execution", source: "traffic_rollback"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, trust, _ := strideE10W7TestManifest(t)
			manifest.RootPolicy.Keys[test.target] = manifest.RootPolicy.Keys[test.source]
			trust.Keys = manifest.RootPolicy.Keys
			sealStrideE10W7TestManifest(t, &manifest, trust, strideE10W7TestRootKey())
			result := validateStrideE10W7TestAcceptance(t, manifest)
			if result.Ready || !containsSTRIDEString(result.Reasons, "w7_independent_attestor_not_independent") {
				t.Fatalf("role collision %s/%s reasons=%v", test.target, test.source, result.Reasons)
			}
		})
	}
}

func TestStrideE10W7AcceptanceRejectsSelfSignedReplacementAndUnresolvedSubreceipt(t *testing.T) {
	manifest, _, _ := strideE10W7TestManifest(t)
	seed := sha256.Sum256([]byte("attacker-key"))
	attacker := ed25519.NewKeyFromSeed(seed[:])
	for index, evidence := range manifest.Evidence {
		if evidence.Kind == "native_distribution" {
			var payload StrideE10W7NativeDistribution
			_ = strideE10W7Decode(evidence.Payload, &payload)
			manifest.Evidence[index], _ = SignStrideE10W7Evidence(evidence.Kind, "attacker-key", evidence.ObservedAt, payload, attacker)
		}
	}
	result := validateStrideE10W7TestAcceptance(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w7_evidence_signature_invalid_native_distribution") {
		t.Fatalf("replacement reasons=%v", result.Reasons)
	}
	manifest, _, _ = strideE10W7TestManifest(t)
	manifest.Subreceipts = manifest.Subreceipts[1:]
	result = validateStrideE10W7TestAcceptance(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w7_restore_subreceipt_unresolved") {
		t.Fatalf("subreceipt reasons=%v", result.Reasons)
	}
}

func TestStrideE10W7AcceptanceRejectsWhollyReplacedTrustUniverse(t *testing.T) {
	manifest, trust, _ := strideE10W7TestManifest(t)
	attackerRootSeed := sha256.Sum256([]byte("w7-attacker-root"))
	attackerRoot := ed25519.NewKeyFromSeed(attackerRootSeed[:])
	attackerKeys := map[string]ed25519.PrivateKey{}
	for kind := range trust.Keys {
		seed := sha256.Sum256([]byte("w7-attacker-" + kind))
		key := ed25519.NewKeyFromSeed(seed[:])
		attackerKeys[kind] = key
		trust.Keys[kind] = StrideE10W7TrustedKey{KeyID: "attacker-" + kind, PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))}
	}
	for index, evidence := range manifest.Evidence {
		manifest.Evidence[index], _ = SignStrideE10W7Evidence(evidence.Kind, trust.Keys[evidence.Kind].KeyID, evidence.ObservedAt, json.RawMessage(evidence.Payload), attackerKeys[evidence.Kind])
	}
	for index, evidence := range manifest.Subreceipts {
		manifest.Subreceipts[index], _ = SignStrideE10W7Evidence(evidence.Kind, trust.Keys[evidence.Kind].KeyID, evidence.ObservedAt, json.RawMessage(evidence.Payload), attackerKeys[evidence.Kind])
	}
	sealStrideE10W7TestManifest(t, &manifest, trust, attackerRoot)
	result := validateStrideE10W7TestAcceptance(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w7_manifest_invalid") {
		t.Fatalf("replacement universe reasons=%v", result.Reasons)
	}
}

func TestStrideE10W7AcceptanceSeparatesNativeAndServerArtifacts(t *testing.T) {
	manifest, trust, keys := strideE10W7TestManifest(t)
	for index, evidence := range manifest.Evidence {
		if evidence.Kind != "independent_release_attestation" {
			continue
		}
		var payload StrideE10W7ReleaseAttestation
		if err := strideE10W7Decode(evidence.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		payload.ImageDigest = payload.NativeArtifactDigest
		payload.RunningImageDigest = payload.NativeArtifactDigest
		manifest.Evidence[index], _ = SignStrideE10W7Evidence(evidence.Kind, evidence.KeyID, evidence.ObservedAt, payload, keys[evidence.Kind])
	}
	sealStrideE10W7TestManifest(t, &manifest, trust, strideE10W7TestRootKey())
	result := validateStrideE10W7TestAcceptance(t, manifest)
	if result.Ready || !containsSTRIDEString(result.Reasons, "w7_independent_release_attestation_invalid") {
		t.Fatalf("native/server artifact collision reasons=%v", result.Reasons)
	}
}
