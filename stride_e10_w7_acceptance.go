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

const strideE10W7ReceiptSchema = "stride.e10.w7.signed-evidence.v1"

var ErrStrideE10W7NotReady = errors.New("stride e10 w7 acceptance not ready")

var strideE10W7Kinds = []string{
	"native_distribution",
	"iphone_physical",
	"ipad_physical",
	"accessibility_privacy",
	"restrictive_turn_webrtc",
	"encrypted_offsite_restore",
	"ha_failover",
	"independent_release_attestation",
}

var strideE10W7SubreceiptKinds = []string{
	"restore_execution", "postgres_failover", "application_failover", "turn_failover", "traffic_rollback",
}

type StrideE10W7SignedEvidence struct {
	Schema     string          `json:"schema"`
	Kind       string          `json:"kind"`
	KeyID      string          `json:"keyId"`
	ObservedAt time.Time       `json:"observedAt"`
	Payload    json.RawMessage `json:"payload"`
	Signature  string          `json:"signature"`
}

type StrideE10W7TrustedKey struct {
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"`
}

type StrideE10W7TrustController interface {
	ResolveStrideE10W7Trust(string) (StrideE10W7TrustedKey, error)
	StrideE10W7TrustPolicyDigest() string
}

const strideE10W7CompiledRootKeyID = "stride-e10-w7-root-2026-08"
const strideE10W7ProductionRootPublicKey = "2Cu7nSCMSnNuf6lxH8qekgYxrOinFTrciWOU27PZoHc="

type StrideE10W7RootPolicy struct {
	Schema         string                           `json:"schema"`
	RootKeyID      string                           `json:"rootKeyId"`
	PolicyID       string                           `json:"policyId"`
	ManifestDigest string                           `json:"manifestDigest"`
	Keys           map[string]StrideE10W7TrustedKey `json:"keys"`
	Signature      string                           `json:"signature"`
}

type StrideE10W7AcceptanceManifest struct {
	Schema                   string                      `json:"schema"`
	CandidateCommit          string                      `json:"candidateCommit"`
	CandidateBuild           int64                       `json:"candidateBuild"`
	RequiredTestFlightGroups []string                    `json:"requiredTestFlightGroups"`
	TrustPolicyDigest        string                      `json:"trustPolicyDigest"`
	RootPolicy               StrideE10W7RootPolicy       `json:"rootPolicy"`
	MaxRPOSeconds            int64                       `json:"maxRpoSeconds"`
	MaxRTOSeconds            int64                       `json:"maxRtoSeconds"`
	FrozenAt                 time.Time                   `json:"frozenAt"`
	Evidence                 []StrideE10W7SignedEvidence `json:"evidence"`
	Subreceipts              []StrideE10W7SignedEvidence `json:"subreceipts"`
}

type StrideE10W7NativeDistribution struct {
	Source             string   `json:"source"`
	Commit             string   `json:"commit"`
	BuildNumber        int64    `json:"buildNumber"`
	EASBuildID         string   `json:"easBuildId"`
	EASSubmissionID    string   `json:"easSubmissionId"`
	AppleBuildID       string   `json:"appleBuildId"`
	EASStatus          string   `json:"easStatus"`
	SubmissionStatus   string   `json:"submissionStatus"`
	AppleState         string   `json:"appleState"`
	BetaState          string   `json:"betaState"`
	Expired            bool     `json:"expired"`
	Groups             []string `json:"groups"`
	ProvenanceResolved bool     `json:"provenanceResolved"`
	ArtifactDigest     string   `json:"artifactDigest"`
}

type StrideE10W7PhysicalDevice struct {
	Source         string          `json:"source"`
	DeviceClass    string          `json:"deviceClass"`
	Physical       bool            `json:"physical"`
	Commit         string          `json:"commit"`
	BuildNumber    int64           `json:"buildNumber"`
	HardwareDigest string          `json:"hardwareDigest"`
	OSVersion      string          `json:"osVersion"`
	ArtifactDigest string          `json:"artifactDigest"`
	Flows          map[string]bool `json:"flows"`
}

type StrideE10W7AccessibilityPrivacy struct {
	Source                string          `json:"source"`
	Commit                string          `json:"commit"`
	BuildNumber           int64           `json:"buildNumber"`
	PrivacyManifestDigest string          `json:"privacyManifestDigest"`
	AppPrivacyDigest      string          `json:"appPrivacyDigest"`
	ProductApprover       string          `json:"productApprover"`
	LegalApprover         string          `json:"legalApprover"`
	PrivacyApprover       string          `json:"privacyApprover"`
	Checks                map[string]bool `json:"checks"`
}

type StrideE10W7TURNWebRTC struct {
	Source                 string   `json:"source"`
	ReleaseCommit          string   `json:"releaseCommit"`
	NativeCommit           string   `json:"nativeCommit"`
	NativeBuild            int64    `json:"nativeBuild"`
	RestrictiveNetwork     bool     `json:"restrictiveNetwork"`
	RealWebRTC             bool     `json:"realWebrtc"`
	RelayOnly              bool     `json:"relayOnly"`
	RelayProtocols         []string `json:"relayProtocols"`
	BrowserNativeMixed     bool     `json:"browserNativeMixed"`
	RoomCount              int64    `json:"roomCount"`
	ParticipantsPerRoom    int64    `json:"participantsPerRoom"`
	DurationMinutesPerRoom int64    `json:"durationMinutesPerRoom"`
	InboundRTPBytes        int64    `json:"inboundRtpBytes"`
	OutboundRTPBytes       int64    `json:"outboundRtpBytes"`
	BackgroundRecovery     bool     `json:"backgroundRecovery"`
	AudioRouteChange       bool     `json:"audioRouteChange"`
	CameraSwitch           bool     `json:"cameraSwitch"`
	ConsentCurrent         bool     `json:"consentCurrent"`
	InducedAIFailurePassed bool     `json:"inducedAiFailurePassed"`
	ReceiptDigest          string   `json:"receiptDigest"`
}

type StrideE10W7OffsiteRestore struct {
	Source                     string   `json:"source"`
	ReleaseCommit              string   `json:"releaseCommit"`
	Encryption                 string   `json:"encryption"`
	ObjectLockMode             string   `json:"objectLockMode"`
	KMSKeyID                   string   `json:"kmsKeyId"`
	CloudTrailEvidenceDigest   string   `json:"cloudTrailEvidenceDigest"`
	Roots                      []string `json:"roots"`
	PurgeAuthorityHeadDigest   string   `json:"purgeAuthorityHeadDigest"`
	SignedRestoreReceiptDigest string   `json:"signedRestoreReceiptDigest"`
	IsolatedApplicationBoot    bool     `json:"isolatedApplicationBoot"`
	RestoreCurrentAuthority    bool     `json:"restoreCurrentAuthority"`
	PurgeRollbackDetected      bool     `json:"purgeRollbackDetected"`
	RPOSeconds                 int64    `json:"rpoSeconds"`
	RTOSeconds                 int64    `json:"rtoSeconds"`
}

type StrideE10W7HAFailover struct {
	Source                   string `json:"source"`
	ReleaseCommit            string `json:"releaseCommit"`
	PostgresFailover         bool   `json:"postgresFailover"`
	ApplicationFailover      bool   `json:"applicationFailover"`
	TURNFailover             bool   `json:"turnFailover"`
	ReversibleTrafficShift   bool   `json:"reversibleTrafficShift"`
	OldHostRetained          bool   `json:"oldHostRetained"`
	ActiveCallsEnded         int64  `json:"activeCallsEnded"`
	DataLossDetected         bool   `json:"dataLossDetected"`
	PostgresReceiptDigest    string `json:"postgresReceiptDigest"`
	ApplicationReceiptDigest string `json:"applicationReceiptDigest"`
	TURNReceiptDigest        string `json:"turnReceiptDigest"`
	RollbackReceiptDigest    string `json:"rollbackReceiptDigest"`
}

type StrideE10W7BoundSubreceipt struct {
	Source              string `json:"source"`
	ReleaseCommit       string `json:"releaseCommit"`
	ParentKind          string `json:"parentKind"`
	ParentPayloadDigest string `json:"parentPayloadDigest"`
	Verdict             string `json:"verdict"`
}

type StrideE10W7ReleaseAttestation struct {
	Source                string    `json:"source"`
	ReleaseCommit         string    `json:"releaseCommit"`
	GitTreeDigest         string    `json:"gitTreeDigest"`
	SourceArchiveDigest   string    `json:"sourceArchiveDigest"`
	ImageDigest           string    `json:"imageDigest"`
	NativeArtifactDigest  string    `json:"nativeArtifactDigest"`
	BinaryDigest          string    `json:"binaryDigest"`
	ConfigurationDigest   string    `json:"configurationDigest"`
	RunningImageDigest    string    `json:"runningImageDigest"`
	IndependentSigner     bool      `json:"independentSigner"`
	OffHost               bool      `json:"offHost"`
	IssuedAt              time.Time `json:"issuedAt"`
	ExpiresAt             time.Time `json:"expiresAt"`
	AttestationBodyDigest string    `json:"attestationBodyDigest"`
}

type StrideE10W7PreflightResult struct {
	Ready          bool     `json:"ready"`
	Reasons        []string `json:"reasons"`
	ManifestDigest string   `json:"manifestDigest,omitempty"`
}

func strideE10W7SignatureInput(e StrideE10W7SignedEvidence) ([]byte, error) {
	var compact bytes.Buffer
	if json.Compact(&compact, e.Payload) != nil || compact.Len() == 0 {
		return nil, ErrStrideE10W7NotReady
	}
	return []byte("meetingassist/stride/e10/w7/evidence/v1\x00" + e.Schema + "\x00" + e.Kind + "\x00" + e.KeyID + "\x00" + e.ObservedAt.UTC().Format(time.RFC3339Nano) + "\x00" + compact.String()), nil
}

func ValidateStrideE10W7Acceptance(manifest StrideE10W7AcceptanceManifest) StrideE10W7PreflightResult {
	return validateStrideE10W7Acceptance(manifest)
}

func validateStrideE10W7Acceptance(manifest StrideE10W7AcceptanceManifest) StrideE10W7PreflightResult {
	reasons := make([]string, 0)
	trust, trustErr := strideE10W7RegistryFromRootPolicy(manifest)
	if trustErr != nil || manifest.Schema != "stride.e10.w7.acceptance-manifest.v1" || !strideE10W7Commit(manifest.CandidateCommit) || manifest.CandidateBuild < 1 || manifest.MaxRPOSeconds < 1 || manifest.MaxRTOSeconds < 1 || manifest.MaxRTOSeconds > 3600 || manifest.FrozenAt.IsZero() || manifest.FrozenAt.After(time.Now().UTC().Add(5*time.Minute)) || !strideE10W7ExactStrings(manifest.RequiredTestFlightGroups, manifest.RequiredTestFlightGroups, 1, 4) || !strideE10W7Digest(manifest.TrustPolicyDigest) {
		reasons = append(reasons, "w7_manifest_invalid")
	}
	if !strideE10W7IndependentTrust(trust) {
		reasons = append(reasons, "w7_independent_attestor_not_independent")
	}
	byKind := map[string]StrideE10W7SignedEvidence{}
	for _, evidence := range manifest.Evidence {
		if !containsSTRIDEString(strideE10W7Kinds, evidence.Kind) {
			reasons = append(reasons, "w7_unknown_evidence_kind")
			continue
		}
		if _, exists := byKind[evidence.Kind]; exists {
			reasons = append(reasons, "w7_duplicate_"+evidence.Kind)
			continue
		}
		byKind[evidence.Kind] = evidence
		key, err := trust.ResolveStrideE10W7Trust(evidence.Kind)
		if err != nil || evidence.Schema != strideE10W7ReceiptSchema || evidence.KeyID != key.KeyID || evidence.ObservedAt.IsZero() || evidence.ObservedAt.After(manifest.FrozenAt) || manifest.FrozenAt.Sub(evidence.ObservedAt) > 30*24*time.Hour || !strideE10W7VerifyEvidence(evidence, key.PublicKey) {
			reasons = append(reasons, "w7_evidence_signature_invalid_"+evidence.Kind)
		}
	}
	subreceipts := map[string]StrideE10W7SignedEvidence{}
	for _, signed := range manifest.Subreceipts {
		digest := strideE10W7EvidenceDigest(signed)
		key, err := trust.ResolveStrideE10W7Trust(signed.Kind)
		if !containsSTRIDEString(strideE10W7SubreceiptKinds, signed.Kind) || err != nil || !strideE10W7Digest(digest) || signed.Schema != strideE10W7ReceiptSchema || !strideE10W7VerifyEvidence(signed, key.PublicKey) || signed.KeyID != key.KeyID || signed.ObservedAt.IsZero() || signed.ObservedAt.After(manifest.FrozenAt) || manifest.FrozenAt.Sub(signed.ObservedAt) > 30*24*time.Hour {
			reasons = append(reasons, "w7_subreceipt_invalid_"+signed.Kind)
			continue
		}
		if _, exists := subreceipts[digest]; exists {
			reasons = append(reasons, "w7_duplicate_subreceipt")
		}
		subreceipts[digest] = signed
	}
	for _, kind := range strideE10W7Kinds {
		if _, ok := byKind[kind]; !ok {
			reasons = append(reasons, "w7_missing_"+kind)
		}
	}
	artifactDigest := ""
	if evidence, ok := byKind["native_distribution"]; ok {
		var value StrideE10W7NativeDistribution
		if strideE10W7Decode(evidence.Payload, &value) != nil || value.Source != "external_observed" || value.Commit != manifest.CandidateCommit || value.BuildNumber != manifest.CandidateBuild || !strideE10W7ID(value.EASBuildID) || !strideE10W7ID(value.EASSubmissionID) || !strideE10W7ID(value.AppleBuildID) || value.EASStatus != "FINISHED" || value.SubmissionStatus != "FINISHED" || value.AppleState != "VALID" || value.BetaState != "IN_BETA_TESTING" || value.Expired || !value.ProvenanceResolved || !strideE10W7ExactStrings(value.Groups, manifest.RequiredTestFlightGroups, 1, 4) || !strideE10W7Digest(value.ArtifactDigest) {
			reasons = append(reasons, "w7_native_distribution_invalid")
		} else {
			artifactDigest = value.ArtifactDigest
		}
	}
	for _, device := range []string{"iphone", "ipad"} {
		if evidence, ok := byKind[device+"_physical"]; ok {
			var value StrideE10W7PhysicalDevice
			required := []string{"organization", "work_record", "publish", "pause", "search", "evidence", "contact", "block", "revoke", "background_foreground", "locked_recovery", "push_deep_link"}
			if strideE10W7Decode(evidence.Payload, &value) != nil || value.Source != "physical_device" || value.DeviceClass != device || !value.Physical || value.Commit != manifest.CandidateCommit || value.BuildNumber != manifest.CandidateBuild || !strideE10W7Digest(value.HardwareDigest) || strings.TrimSpace(value.OSVersion) == "" || value.ArtifactDigest != artifactDigest || !strideE10W7ExactTrueMap(value.Flows, required) {
				reasons = append(reasons, "w7_"+device+"_physical_invalid")
			}
		}
	}
	if evidence, ok := byKind["accessibility_privacy"]; ok {
		var value StrideE10W7AccessibilityPrivacy
		required := []string{"voiceover", "dynamic_type_xxl", "contrast", "reduced_motion", "focus_order", "hit_targets", "keyboard", "privacy_prompts", "background_privacy"}
		if strideE10W7Decode(evidence.Payload, &value) != nil || value.Source != "external_approved" || value.Commit != manifest.CandidateCommit || value.BuildNumber != manifest.CandidateBuild || !strideE10W7Digest(value.PrivacyManifestDigest) || !strideE10W7Digest(value.AppPrivacyDigest) || !strideE10W7Approver(value.ProductApprover) || !strideE10W7Approver(value.LegalApprover) || !strideE10W7Approver(value.PrivacyApprover) || !strideE10W7ExactTrueMap(value.Checks, required) {
			reasons = append(reasons, "w7_accessibility_privacy_invalid")
		}
	}
	if evidence, ok := byKind["restrictive_turn_webrtc"]; ok {
		var value StrideE10W7TURNWebRTC
		if strideE10W7Decode(evidence.Payload, &value) != nil || value.Source != "production_observed" || value.ReleaseCommit != manifest.CandidateCommit || value.NativeCommit != manifest.CandidateCommit || value.NativeBuild != manifest.CandidateBuild || !value.RestrictiveNetwork || !value.RealWebRTC || !value.RelayOnly || !strideE10W7ExactStrings(value.RelayProtocols, []string{"tcp", "tls", "udp"}, 3, 3) || !value.BrowserNativeMixed || value.RoomCount < 2 || value.ParticipantsPerRoom < 3 || value.DurationMinutesPerRoom < 120 || value.InboundRTPBytes < 1 || value.OutboundRTPBytes < 1 || !value.BackgroundRecovery || !value.AudioRouteChange || !value.CameraSwitch || !value.ConsentCurrent || !value.InducedAIFailurePassed || !strideE10W7Digest(value.ReceiptDigest) {
			reasons = append(reasons, "w7_restrictive_turn_webrtc_invalid")
		}
	}
	if evidence, ok := byKind["encrypted_offsite_restore"]; ok {
		var value StrideE10W7OffsiteRestore
		roots := []string{"canonical_postgres", "codex_queue", "meeting_data", "usage_ledger"}
		if strideE10W7Decode(evidence.Payload, &value) != nil || value.Source != "production_drill" || value.ReleaseCommit != manifest.CandidateCommit || value.Encryption != "AES-256-GCM" || value.ObjectLockMode != "COMPLIANCE" || !strideE10W7ID(value.KMSKeyID) || !strideE10W7Digest(value.CloudTrailEvidenceDigest) || !strideE10W7ExactStrings(value.Roots, roots, 4, 4) || !strideE10W7Digest(value.PurgeAuthorityHeadDigest) || !strideE10W7Digest(value.SignedRestoreReceiptDigest) || !value.IsolatedApplicationBoot || !value.RestoreCurrentAuthority || value.PurgeRollbackDetected || value.RPOSeconds < 0 || value.RPOSeconds > manifest.MaxRPOSeconds || value.RTOSeconds < 1 || value.RTOSeconds > manifest.MaxRTOSeconds {
			reasons = append(reasons, "w7_encrypted_offsite_restore_invalid")
		} else if !strideE10W7BoundSubreceiptValid(subreceipts[value.SignedRestoreReceiptDigest], "restore_execution", "encrypted_offsite_restore", evidence.Payload, manifest.CandidateCommit) {
			reasons = append(reasons, "w7_restore_subreceipt_unresolved")
		}
	}
	if evidence, ok := byKind["ha_failover"]; ok {
		var value StrideE10W7HAFailover
		if strideE10W7Decode(evidence.Payload, &value) != nil || value.Source != "production_drill" || value.ReleaseCommit != manifest.CandidateCommit || !value.PostgresFailover || !value.ApplicationFailover || !value.TURNFailover || !value.ReversibleTrafficShift || !value.OldHostRetained || value.ActiveCallsEnded != 0 || value.DataLossDetected || !strideE10W7Digest(value.PostgresReceiptDigest) || !strideE10W7Digest(value.ApplicationReceiptDigest) || !strideE10W7Digest(value.TURNReceiptDigest) || !strideE10W7Digest(value.RollbackReceiptDigest) {
			reasons = append(reasons, "w7_ha_failover_invalid")
		} else {
			for kind, digest := range map[string]string{"postgres_failover": value.PostgresReceiptDigest, "application_failover": value.ApplicationReceiptDigest, "turn_failover": value.TURNReceiptDigest, "traffic_rollback": value.RollbackReceiptDigest} {
				if !strideE10W7BoundSubreceiptValid(subreceipts[digest], kind, "ha_failover", evidence.Payload, manifest.CandidateCommit) {
					reasons = append(reasons, "w7_ha_subreceipt_unresolved_"+kind)
				}
			}
		}
	}
	if evidence, ok := byKind["independent_release_attestation"]; ok {
		var value StrideE10W7ReleaseAttestation
		if strideE10W7Decode(evidence.Payload, &value) != nil || value.Source != "independent_external" || value.ReleaseCommit != manifest.CandidateCommit || !strideE10W7Digest(value.GitTreeDigest) || !strideE10W7Digest(value.SourceArchiveDigest) || value.NativeArtifactDigest != artifactDigest || !strideE10W7Digest(value.ImageDigest) || value.ImageDigest != value.RunningImageDigest || value.ImageDigest == value.NativeArtifactDigest || !strideE10W7Digest(value.BinaryDigest) || !strideE10W7Digest(value.ConfigurationDigest) || !value.IndependentSigner || !value.OffHost || value.IssuedAt.IsZero() || value.IssuedAt.After(manifest.FrozenAt) || !value.ExpiresAt.After(value.IssuedAt) || value.ExpiresAt.Sub(value.IssuedAt) > 30*24*time.Hour || !value.ExpiresAt.After(manifest.FrozenAt) || !strideE10W7Digest(value.AttestationBodyDigest) {
			reasons = append(reasons, "w7_independent_release_attestation_invalid")
		}
	}
	sort.Strings(reasons)
	reasons = strideE10W7Unique(reasons)
	result := StrideE10W7PreflightResult{Ready: len(reasons) == 0, Reasons: reasons}
	if result.Ready {
		body, _ := json.Marshal(manifest)
		digest := sha256.Sum256(body)
		result.ManifestDigest = hex.EncodeToString(digest[:])
	}
	return result
}

type strideE10W7SealedRegistry struct {
	keys   map[string]StrideE10W7TrustedKey
	digest string
}

func (r *strideE10W7SealedRegistry) ResolveStrideE10W7Trust(kind string) (StrideE10W7TrustedKey, error) {
	key, ok := r.keys[kind]
	if !ok {
		return StrideE10W7TrustedKey{}, ErrStrideE10W7NotReady
	}
	return key, nil
}
func (r *strideE10W7SealedRegistry) StrideE10W7TrustPolicyDigest() string { return r.digest }

func strideE10W7RegistryFromRootPolicy(manifest StrideE10W7AcceptanceManifest) (StrideE10W7TrustController, error) {
	p := manifest.RootPolicy
	keys := make(map[string]StrideE10W7TrustedKey, len(p.Keys))
	for kind, key := range p.Keys {
		keys[kind] = key
	}
	diagnosticRegistry := &strideE10W7SealedRegistry{keys: keys, digest: manifest.TrustPolicyDigest}
	if p.Schema != "stride.e10.w7.root-policy.v1" || p.RootKeyID != strideE10W7CompiledRootKeyID || !strideE10W7ID(p.PolicyID) || p.ManifestDigest != strideE10W7ManifestBindingDigest(manifest) || manifest.TrustPolicyDigest != strideE10W7PolicyDigest(p) {
		return diagnosticRegistry, ErrStrideE10W7NotReady
	}
	publicKey, err := base64.StdEncoding.DecodeString(strideE10W7ProductionRootPublicKey)
	signature, sigErr := base64.StdEncoding.DecodeString(p.Signature)
	input, inputErr := strideE10W7RootPolicyInput(p)
	if err != nil || sigErr != nil || inputErr != nil || len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(publicKey), input, signature) {
		return diagnosticRegistry, ErrStrideE10W7NotReady
	}
	return diagnosticRegistry, nil
}
func strideE10W7RootPolicyInput(p StrideE10W7RootPolicy) ([]byte, error) {
	copy := p
	copy.Signature = ""
	body, err := json.Marshal(copy)
	if err != nil {
		return nil, err
	}
	return append([]byte("meetingassist/stride/e10/w7/root-policy/v1\x00"), body...), nil
}
func strideE10W7PolicyDigest(p StrideE10W7RootPolicy) string {
	copy := p
	copy.Signature = ""
	body, _ := json.Marshal(copy)
	d := sha256.Sum256(body)
	return hex.EncodeToString(d[:])
}
func strideE10W7ManifestBindingDigest(m StrideE10W7AcceptanceManifest) string {
	m.RootPolicy.ManifestDigest = ""
	m.RootPolicy.Signature = ""
	m.TrustPolicyDigest = ""
	body, _ := json.Marshal(m)
	d := sha256.Sum256(body)
	return hex.EncodeToString(d[:])
}

func strideE10W7IndependentTrust(trust StrideE10W7TrustController) bool {
	if trust == nil {
		return false
	}
	seenKeyIDs := map[string]struct{}{}
	seenPublicKeys := map[string]struct{}{}
	for _, kind := range append(append([]string(nil), strideE10W7Kinds...), strideE10W7SubreceiptKinds...) {
		key, keyErr := trust.ResolveStrideE10W7Trust(kind)
		publicKey, decodeErr := base64.StdEncoding.DecodeString(key.PublicKey)
		if keyErr != nil || !strideE10W7ID(key.KeyID) || decodeErr != nil || len(publicKey) != ed25519.PublicKeySize {
			return false
		}
		if _, exists := seenKeyIDs[key.KeyID]; exists {
			return false
		}
		publicKeyIdentity := string(publicKey)
		if _, exists := seenPublicKeys[publicKeyIdentity]; exists {
			return false
		}
		seenKeyIDs[key.KeyID] = struct{}{}
		seenPublicKeys[publicKeyIdentity] = struct{}{}
	}
	return true
}

func strideE10W7EvidenceDigest(e StrideE10W7SignedEvidence) string {
	body, err := json.Marshal(e)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func strideE10W7BoundSubreceiptValid(signed StrideE10W7SignedEvidence, kind, parentKind string, parent json.RawMessage, commit string) bool {
	if signed.Kind != kind {
		return false
	}
	var value StrideE10W7BoundSubreceipt
	digest := strideE10W7ParentBindingDigest(parentKind, parent)
	return strideE10W7Decode(signed.Payload, &value) == nil && value.Source == "independent_observed" && value.ReleaseCommit == commit && value.ParentKind == parentKind && value.ParentPayloadDigest == digest && value.Verdict == "passed"
}

func strideE10W7ParentBindingDigest(kind string, raw json.RawMessage) string {
	var normalized any
	switch kind {
	case "encrypted_offsite_restore":
		var value StrideE10W7OffsiteRestore
		if strideE10W7Decode(raw, &value) != nil {
			return ""
		}
		value.SignedRestoreReceiptDigest = ""
		normalized = value
	case "ha_failover":
		var value StrideE10W7HAFailover
		if strideE10W7Decode(raw, &value) != nil {
			return ""
		}
		value.PostgresReceiptDigest, value.ApplicationReceiptDigest, value.TURNReceiptDigest, value.RollbackReceiptDigest = "", "", "", ""
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

func strideE10W7VerifyEvidence(e StrideE10W7SignedEvidence, publicKey string) bool {
	key, err := base64.StdEncoding.DecodeString(publicKey)
	signature, sigErr := base64.StdEncoding.DecodeString(e.Signature)
	input, inputErr := strideE10W7SignatureInput(e)
	return err == nil && sigErr == nil && inputErr == nil && len(key) == ed25519.PublicKeySize && len(signature) == ed25519.SignatureSize && ed25519.Verify(ed25519.PublicKey(key), input, signature)
}

func strideE10W7Decode(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) == nil {
		return ErrStrideE10W7NotReady
	}
	return nil
}

func strideE10W7Commit(value string) bool { return len(value) == 40 && strideE10W7Hex(value) }
func strideE10W7Digest(value string) bool { return len(value) == 64 && strideE10W7Hex(value) }
func strideE10W7Hex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}
func strideE10W7ID(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 && len(value) <= 160 && !strings.ContainsAny(value, "\r\n\x00")
}
func strideE10W7Approver(value string) bool { return strideE10W7ID(value) }

func strideE10W7ExactStrings(got, want []string, min, max int) bool {
	if len(got) < min || len(got) > max || len(got) != len(want) {
		return false
	}
	a := append([]string(nil), got...)
	b := append([]string(nil), want...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if !strideE10W7ID(a[i]) || a[i] != b[i] || i > 0 && a[i] == a[i-1] {
			return false
		}
	}
	return true
}

func strideE10W7ExactTrueMap(got map[string]bool, required []string) bool {
	if len(got) != len(required) {
		return false
	}
	for _, key := range required {
		if !got[key] {
			return false
		}
	}
	return true
}

func strideE10W7Unique(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func SignStrideE10W7Evidence(kind, keyID string, observedAt time.Time, payload any, privateKey ed25519.PrivateKey) (StrideE10W7SignedEvidence, error) {
	if (!containsSTRIDEString(strideE10W7Kinds, kind) && !containsSTRIDEString(strideE10W7SubreceiptKinds, kind)) || !strideE10W7ID(keyID) || observedAt.IsZero() || len(privateKey) != ed25519.PrivateKeySize {
		return StrideE10W7SignedEvidence{}, ErrStrideE10W7NotReady
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return StrideE10W7SignedEvidence{}, err
	}
	evidence := StrideE10W7SignedEvidence{Schema: strideE10W7ReceiptSchema, Kind: kind, KeyID: keyID, ObservedAt: observedAt.UTC(), Payload: raw}
	input, err := strideE10W7SignatureInput(evidence)
	if err != nil {
		return StrideE10W7SignedEvidence{}, err
	}
	evidence.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, input))
	return evidence, nil
}

func (r StrideE10W7PreflightResult) Error() error {
	if r.Ready {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrStrideE10W7NotReady, strings.Join(r.Reasons, ","))
}
