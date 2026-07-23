package mediasoak

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	ManifestSchema          = "bonfire.w2a.media-soak.gate.v1"
	ObservationSchema       = "bonfire.w2a.media-soak.observation.v1"
	SignedObservationSchema = "bonfire.w2a.media-soak.signed-observation.v1"
	ReceiptSchema           = "bonfire.w2a.media-soak.receipt.v1"

	EvidenceModeLive    = "live_soak"
	EvidenceModeFixture = "deterministic_fixture"

	VerdictPass               = "pass"
	VerdictFail               = "fail"
	VerdictNonQualifying      = "non_qualifying_fixture"
	SignatureAlgorithmEd25519 = "ed25519"
)

var (
	ErrInvalidManifest    = errors.New("invalid W2A media soak manifest")
	ErrInvalidObservation = errors.New("invalid W2A media soak observation")
	ErrInvalidSignature   = errors.New("invalid W2A media soak signature")
	ErrInconclusive       = errors.New("W2A media soak evidence is incomplete or inconclusive")
	ErrInvalidReceipt     = errors.New("invalid W2A media soak receipt")

	releaseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	environmentPattern   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

type Manifest struct {
	Schema               string            `json:"schema"`
	ID                   string            `json:"id"`
	Description          string            `json:"description"`
	Command              string            `json:"command"`
	TimeoutSeconds       int               `json:"timeoutSeconds"`
	ReleaseCommit        string            `json:"releaseCommit"`
	Backend              string            `json:"backend"`
	FeatureFlag          string            `json:"featureFlag"`
	FeatureFlagValue     string            `json:"featureFlagValue"`
	RequiredEvidenceMode string            `json:"requiredEvidenceMode"`
	AllowedSignerKeyIDs  []string          `json:"allowedSignerKeyIds"`
	TrustedSignerKeys    []TrustedSigner   `json:"trustedSignerKeys"`
	Collector            CollectorContract `json:"collector"`
	Freshness            FreshnessContract `json:"freshness"`
	Thresholds           SoakThresholds    `json:"thresholds"`
	Receipt              ReceiptContract   `json:"receipt"`
	StopCondition        string            `json:"stopCondition"`
	RollbackCommand      string            `json:"rollbackCommand"`
}

type TrustedSigner struct {
	Role            string `json:"role"`
	KeyID           string `json:"keyId"`
	PublicKeySHA256 string `json:"publicKeySha256"`
}

type CollectorContract struct {
	Command                 []string `json:"command"`
	SignedEvidencePath      string   `json:"signedEvidencePath"`
	MinimumRawSamples       int      `json:"minimumRawSamples"`
	MaximumSampleGapSeconds float64  `json:"maximumSampleGapSeconds"`
}

type FreshnessContract struct {
	MaximumAgeSeconds    int    `json:"maximumAgeSeconds"`
	MaximumFutureSeconds int    `json:"maximumFutureSeconds"`
	NonceLedgerPath      string `json:"nonceLedgerPath"`
}

type SoakThresholds struct {
	MinimumDurationSeconds                 float64 `json:"minimumDurationSeconds"`
	MinimumConcurrentRoomSeconds           float64 `json:"minimumConcurrentRoomSeconds"`
	MinimumRoomCount                       int     `json:"minimumRoomCount"`
	MinimumPublishersPerRoom               int     `json:"minimumPublishersPerRoom"`
	MinimumSubscribersPerRoom              int     `json:"minimumSubscribersPerRoom"`
	MinimumParticipantMinutes              float64 `json:"minimumParticipantMinutes"`
	MinimumBlockedOfferSeconds             float64 `json:"minimumBlockedOfferSeconds"`
	MinimumAdmissionSamples                int     `json:"minimumAdmissionSamples"`
	MaximumAdmissionP95Seconds             float64 `json:"maximumAdmissionP95Seconds"`
	MinimumRenegotiationSamples            int     `json:"minimumRenegotiationSamples"`
	MaximumRenegotiationP95Seconds         float64 `json:"maximumRenegotiationP95Seconds"`
	MaximumRoomBAdmissionFailures          int     `json:"maximumRoomBAdmissionFailures"`
	MaximumRoomBRenegotiationFailures      int     `json:"maximumRoomBRenegotiationFailures"`
	MaximumPacketLossPercent               float64 `json:"maximumPacketLossPercent"`
	MaximumUnexpectedDisconnectPercent     float64 `json:"maximumUnexpectedDisconnectParticipantMinutePercent"`
	MaximumSustainedCPUPercent             float64 `json:"maximumSustainedCpuPercent"`
	MaximumRSSContainerLimitPercent        float64 `json:"maximumRssContainerLimitPercent"`
	MinimumNetworkSampleCount              int     `json:"minimumNetworkSampleCount"`
	MinimumNetworkSampleCoverageSeconds    float64 `json:"minimumNetworkSampleCoverageSeconds"`
	MinimumResourceSampleCount             int     `json:"minimumResourceSampleCount"`
	MinimumResourceSampleCoverageSeconds   float64 `json:"minimumResourceSampleCoverageSeconds"`
	MinimumCanaryChecksPerBoundary         int     `json:"minimumCanaryChecksPerBoundary"`
	MinimumSourceCanaryObservationRate     float64 `json:"minimumSourceCanaryObservationRate"`
	MaximumLeakCountPerSurface             int     `json:"maximumLeakCountPerSurface"`
	MinimumAIProviderFailureInjections     int     `json:"minimumAiProviderFailureInjections"`
	MaximumMediaInterruptionsDuringAIFault int     `json:"maximumMediaInterruptionsDuringAiFault"`
	MaximumAdmissionFailuresDuringAIFault  int     `json:"maximumAdmissionFailuresDuringAiFault"`
}

type ReceiptContract struct {
	LiveOutputPath     string `json:"liveOutputPath"`
	FixtureOutputPath  string `json:"fixtureOutputPath"`
	Schema             string `json:"schema"`
	SignatureAlgorithm string `json:"signatureAlgorithm"`
	PublicKeyFileFlag  string `json:"publicKeyFileFlag"`
}

type SignedObservation struct {
	Schema    string            `json:"schema"`
	Payload   SoakObservation   `json:"payload"`
	Evidence  SignedRawEvidence `json:"evidence"`
	Signature SignatureEnvelope `json:"signature"`
}

type SignatureEnvelope struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"keyId"`
	Value     string `json:"value"`
}

type SoakObservation struct {
	Schema                string              `json:"schema"`
	EvidenceMode          string              `json:"evidenceMode"`
	Synthetic             bool                `json:"synthetic"`
	Conclusive            bool                `json:"conclusive"`
	ReleaseCommit         string              `json:"releaseCommit"`
	Backend               string              `json:"backend"`
	FeatureFlag           string              `json:"featureFlag"`
	FeatureFlagValue      string              `json:"featureFlagValue"`
	ProbeVersion          string              `json:"probeVersion"`
	RunID                 string              `json:"runId"`
	Nonce                 string              `json:"nonce"`
	ExpiresAt             time.Time           `json:"expiresAt"`
	RawEvidenceDigest     string              `json:"rawEvidenceDigest"`
	HostAttestationDigest string              `json:"hostAttestationDigest"`
	StartedAt             time.Time           `json:"startedAt"`
	EndedAt               time.Time           `json:"endedAt"`
	DurationSeconds       float64             `json:"durationSeconds"`
	ConcurrentRoomSeconds float64             `json:"concurrentRoomSeconds"`
	ParticipantMinutes    float64             `json:"participantMinutes"`
	Rooms                 []RoomScopeEvidence `json:"rooms"`
	HeadOfLine            HeadOfLineEvidence  `json:"headOfLine"`
	MediaHealth           MediaHealthEvidence `json:"mediaHealth"`
	Canaries              CanaryEvidence      `json:"canaries"`
	AIFailure             AIFailureEvidence   `json:"aiFailure"`
}

type RoomScopeEvidence struct {
	RoomDigest            string  `json:"roomDigest"`
	SittingDigest         string  `json:"sittingDigest"`
	MediaGenerationDigest string  `json:"mediaGenerationDigest"`
	MinimumPublishers     int     `json:"minimumPublishers"`
	MinimumSubscribers    int     `json:"minimumSubscribers"`
	ActiveSeconds         float64 `json:"activeSeconds"`
}

type HeadOfLineEvidence struct {
	BlockedOfferSeconds          float64 `json:"blockedOfferSeconds"`
	RoomBAdmissionSamples        int     `json:"roomBAdmissionSamples"`
	RoomBAdmissionP95Seconds     float64 `json:"roomBAdmissionP95Seconds"`
	RoomBAdmissionFailures       int     `json:"roomBAdmissionFailures"`
	RoomBRenegotiationSamples    int     `json:"roomBRenegotiationSamples"`
	RoomBRenegotiationP95Seconds float64 `json:"roomBRenegotiationP95Seconds"`
	RoomBRenegotiationFailures   int     `json:"roomBRenegotiationFailures"`
}

type MediaHealthEvidence struct {
	PacketLossPercent                            float64 `json:"packetLossPercent"`
	UnexpectedDisconnectParticipantMinutePercent float64 `json:"unexpectedDisconnectParticipantMinutePercent"`
	SustainedCPUPercent                          float64 `json:"sustainedCpuPercent"`
	RSSContainerLimitPercent                     float64 `json:"rssContainerLimitPercent"`
	NetworkSampleCount                           int     `json:"networkSampleCount"`
	NetworkSampleCoverageSeconds                 float64 `json:"networkSampleCoverageSeconds"`
	ResourceSampleCount                          int     `json:"resourceSampleCount"`
	ResourceSampleCoverageSeconds                float64 `json:"resourceSampleCoverageSeconds"`
}

type CanaryEvidence struct {
	RoomBoundaryChecks            int     `json:"roomBoundaryChecks"`
	SittingBoundaryChecks         int     `json:"sittingBoundaryChecks"`
	MediaGenerationBoundaryChecks int     `json:"mediaGenerationBoundaryChecks"`
	SourceObservationRate         float64 `json:"sourceObservationRate"`
	TrackLeakCount                int     `json:"trackLeakCount"`
	IdentityLeakCount             int     `json:"identityLeakCount"`
	ChatLeakCount                 int     `json:"chatLeakCount"`
	ScoutLeakCount                int     `json:"scoutLeakCount"`
	TranscriptLeakCount           int     `json:"transcriptLeakCount"`
	RecapLeakCount                int     `json:"recapLeakCount"`
	ArtifactLeakCount             int     `json:"artifactLeakCount"`
	WrongSittingLeakCount         int     `json:"wrongSittingLeakCount"`
	WrongMediaGenerationLeakCount int     `json:"wrongMediaGenerationLeakCount"`
}

type AIFailureEvidence struct {
	FailureInjections  int `json:"failureInjections"`
	MediaInterruptions int `json:"mediaInterruptions"`
	AdmissionFailures  int `json:"admissionFailures"`
}

type ThresholdResult struct {
	ID       string  `json:"id"`
	Operator string  `json:"operator"`
	Actual   float64 `json:"actual"`
	Expected float64 `json:"expected"`
	Passed   bool    `json:"passed"`
}

type Receipt struct {
	Schema           string            `json:"schema"`
	GateID           string            `json:"gateId"`
	GeneratedAt      time.Time         `json:"generatedAt"`
	ReleaseCommit    string            `json:"releaseCommit"`
	Backend          string            `json:"backend"`
	FeatureFlag      string            `json:"featureFlag"`
	FeatureFlagValue string            `json:"featureFlagValue"`
	EvidenceMode     string            `json:"evidenceMode"`
	ProbeVersion     string            `json:"probeVersion"`
	ManifestDigest   string            `json:"manifestDigest"`
	PayloadDigest    string            `json:"payloadDigest"`
	SignatureDigest  string            `json:"signatureDigest"`
	SignerKeyID      string            `json:"signerKeyId"`
	SignerKeyDigest  string            `json:"signerKeyDigest"`
	Observation      SoakObservation   `json:"observation"`
	Evidence         SignedRawEvidence `json:"evidence"`
	Signature        SignatureEnvelope `json:"signature"`
	Thresholds       []ThresholdResult `json:"thresholds"`
	Verdict          string            `json:"verdict"`
	ReleaseQualified bool              `json:"releaseQualified"`
	StopTriggered    bool              `json:"stopTriggered"`
	StopCondition    string            `json:"stopCondition"`
	RollbackCommand  string            `json:"rollbackCommand"`
	ReceiptDigest    string            `json:"receiptDigest"`
}

func LoadManifest(path string) (Manifest, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, "", err
	}
	var manifest Manifest
	if err := decodeStrict(raw, &manifest); err != nil {
		return Manifest{}, "", fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, "", err
	}
	return manifest, digest(raw), nil
}

func (manifest Manifest) Validate() error {
	if manifest.Schema != ManifestSchema || strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.Description) == "" ||
		manifest.TimeoutSeconds < 7200 || manifest.ReleaseCommit != "${BONFIRE_RELEASE_COMMIT}" || strings.TrimSpace(manifest.Backend) == "" ||
		!environmentPattern.MatchString(manifest.FeatureFlag) || strings.TrimSpace(manifest.FeatureFlagValue) == "" || manifest.RequiredEvidenceMode != EvidenceModeLive ||
		!strings.Contains(manifest.Command, "--manifest") || !strings.Contains(manifest.Command, "--collect") ||
		!strings.Contains(manifest.Command, "--public-key-file") || !strings.Contains(manifest.Command, "--collector-public-key-file") || !strings.Contains(manifest.Command, "--release-commit") ||
		!strings.Contains(manifest.Command, "--release-private-key-file") ||
		len(manifest.AllowedSignerKeyIDs) == 0 || strings.TrimSpace(manifest.StopCondition) == "" ||
		strings.TrimSpace(manifest.RollbackCommand) == "" || !strings.Contains(manifest.RollbackCommand, manifest.FeatureFlag) {
		return fmt.Errorf("%w: incomplete execution contract", ErrInvalidManifest)
	}
	seenSigners := map[string]bool{}
	for _, keyID := range manifest.AllowedSignerKeyIDs {
		keyID = strings.TrimSpace(keyID)
		if keyID == "" || seenSigners[keyID] {
			return fmt.Errorf("%w: signer key ids", ErrInvalidManifest)
		}
		seenSigners[keyID] = true
	}
	seenRoles := map[string]bool{}
	seenKeyIDs := map[string]bool{}
	seenKeyDigests := map[string]bool{}
	for _, signer := range manifest.TrustedSignerKeys {
		if (signer.Role != SignerRoleRelease && signer.Role != SignerRoleCollector) || strings.TrimSpace(signer.KeyID) == "" ||
			!isDigest(signer.PublicKeySHA256) || seenRoles[signer.Role] || seenKeyIDs[signer.KeyID] || seenKeyDigests[signer.PublicKeySHA256] {
			return fmt.Errorf("%w: trusted signer custody", ErrInvalidManifest)
		}
		if signer.Role == SignerRoleRelease && !seenSigners[signer.KeyID] {
			return fmt.Errorf("%w: release signer key id is not allowed", ErrInvalidManifest)
		}
		seenRoles[signer.Role] = true
		seenKeyIDs[signer.KeyID] = true
		seenKeyDigests[signer.PublicKeySHA256] = true
	}
	collectorCommand := strings.Join(manifest.Collector.Command, " ")
	expectedCollectorCommand := []string{"node", "testdata/w2a/live-media-soak-probe.mjs", "--release-commit", "{releaseCommit}", "--output", "{signedEvidencePath}"}
	if len(manifest.TrustedSignerKeys) != 2 || !seenRoles[SignerRoleRelease] || !seenRoles[SignerRoleCollector] || len(manifest.Collector.Command) == 0 ||
		strings.TrimSpace(manifest.Collector.Command[0]) == "" || validateRelativePath(manifest.Collector.SignedEvidencePath) != nil ||
		!equalStrings(manifest.Collector.Command, expectedCollectorCommand) ||
		!strings.Contains(manifest.Collector.SignedEvidencePath, "{releaseCommit}") || !strings.Contains(collectorCommand, "{releaseCommit}") ||
		!strings.Contains(collectorCommand, "{signedEvidencePath}") ||
		manifest.Collector.MinimumRawSamples < 120 || !finite(manifest.Collector.MaximumSampleGapSeconds) ||
		manifest.Collector.MaximumSampleGapSeconds <= 0 || manifest.Collector.MaximumSampleGapSeconds > 90 ||
		manifest.Freshness.MaximumAgeSeconds <= 0 || manifest.Freshness.MaximumAgeSeconds > 3600 ||
		manifest.Freshness.MaximumFutureSeconds < 0 || manifest.Freshness.MaximumFutureSeconds > 60 ||
		validateRelativePath(manifest.Freshness.NonceLedgerPath) != nil || !strings.Contains(manifest.Freshness.NonceLedgerPath, "{releaseCommit}") {
		return fmt.Errorf("%w: collector, freshness, or nonce custody", ErrInvalidManifest)
	}
	if err := manifest.Thresholds.Validate(); err != nil {
		return err
	}
	if manifest.Receipt.Schema != ReceiptSchema || manifest.Receipt.SignatureAlgorithm != SignatureAlgorithmEd25519 || manifest.Receipt.PublicKeyFileFlag != "--public-key-file" ||
		!strings.Contains(manifest.Receipt.LiveOutputPath, "artifacts/w2-gates/{releaseCommit}/") ||
		!strings.Contains(manifest.Receipt.FixtureOutputPath, "artifacts/w2-gates/non-qualifying/{releaseCommit}/") ||
		validateRelativePath(manifest.Receipt.LiveOutputPath) != nil || validateRelativePath(manifest.Receipt.FixtureOutputPath) != nil {
		return fmt.Errorf("%w: receipt contract", ErrInvalidManifest)
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (thresholds SoakThresholds) Validate() error {
	values := []float64{
		thresholds.MinimumDurationSeconds, thresholds.MinimumConcurrentRoomSeconds, thresholds.MinimumParticipantMinutes,
		thresholds.MinimumBlockedOfferSeconds, thresholds.MaximumAdmissionP95Seconds, thresholds.MaximumRenegotiationP95Seconds,
		thresholds.MaximumPacketLossPercent, thresholds.MaximumUnexpectedDisconnectPercent, thresholds.MaximumSustainedCPUPercent,
		thresholds.MaximumRSSContainerLimitPercent, thresholds.MinimumNetworkSampleCoverageSeconds,
		thresholds.MinimumResourceSampleCoverageSeconds, thresholds.MinimumSourceCanaryObservationRate,
	}
	for _, value := range values {
		if !finite(value) || value < 0 {
			return fmt.Errorf("%w: invalid numeric threshold", ErrInvalidManifest)
		}
	}
	if thresholds.MinimumDurationSeconds < 7200 || thresholds.MinimumConcurrentRoomSeconds < 7200 || thresholds.MinimumRoomCount < 2 ||
		thresholds.MinimumPublishersPerRoom < 3 || thresholds.MinimumSubscribersPerRoom < 3 || thresholds.MinimumParticipantMinutes < 720 ||
		thresholds.MinimumBlockedOfferSeconds < 10 || thresholds.MinimumAdmissionSamples < 3 || thresholds.MaximumAdmissionP95Seconds > 2 ||
		thresholds.MinimumRenegotiationSamples < 3 || thresholds.MaximumRenegotiationP95Seconds > 3 || thresholds.MaximumRoomBAdmissionFailures != 0 ||
		thresholds.MaximumRoomBRenegotiationFailures != 0 || thresholds.MaximumPacketLossPercent > 2 || thresholds.MaximumUnexpectedDisconnectPercent > 1 ||
		thresholds.MaximumSustainedCPUPercent > 80 || thresholds.MaximumRSSContainerLimitPercent > 75 || thresholds.MinimumCanaryChecksPerBoundary < 8 ||
		thresholds.MinimumNetworkSampleCount < 120 || thresholds.MinimumNetworkSampleCoverageSeconds < 7200 ||
		thresholds.MinimumResourceSampleCount < 120 || thresholds.MinimumResourceSampleCoverageSeconds < 7200 ||
		thresholds.MinimumSourceCanaryObservationRate < 1 || thresholds.MaximumLeakCountPerSurface != 0 || thresholds.MinimumAIProviderFailureInjections < 1 ||
		thresholds.MaximumMediaInterruptionsDuringAIFault != 0 || thresholds.MaximumAdmissionFailuresDuringAIFault != 0 {
		return fmt.Errorf("%w: thresholds weaken the approved W2A gate", ErrInvalidManifest)
	}
	return nil
}

func DecodeSignedObservation(raw []byte) (SignedObservation, error) {
	var observation SignedObservation
	if err := decodeStrict(raw, &observation); err != nil {
		return SignedObservation{}, fmt.Errorf("%w: %v", ErrInvalidObservation, err)
	}
	return observation, nil
}

func CanonicalPayload(payload SoakObservation) ([]byte, error) {
	return json.Marshal(payload)
}

func ParsePublicKey(raw []byte) (ed25519.PublicKey, error) {
	value := strings.TrimSpace(string(raw))
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: public key must be base64 Ed25519", ErrInvalidSignature)
	}
	return ed25519.PublicKey(decoded), nil
}

// ParsePrivateKey accepts an Ed25519 PKCS#8 PEM or a base64-encoded 64-byte
// Ed25519 private key. Private key material is never included in receipts.
func ParsePrivateKey(raw []byte) (ed25519.PrivateKey, error) {
	if block, _ := pem.Decode(raw); block != nil {
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		privateKey, ok := key.(ed25519.PrivateKey)
		if err != nil || !ok || len(privateKey) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("%w: private key must be Ed25519 PKCS#8", ErrInvalidSignature)
		}
		return privateKey, nil
	}
	value := strings.TrimSpace(string(raw))
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: private key must be Ed25519 PKCS#8 or base64", ErrInvalidSignature)
	}
	return ed25519.PrivateKey(decoded), nil
}

func (signed SignedObservation) Verify(manifest Manifest, releaseCommit string, publicKey, collectorKey ed25519.PublicKey, now time.Time) ([]byte, []byte, error) {
	trusted, trustedOK := manifest.trustedSigner(SignerRoleRelease, signed.Signature.KeyID)
	if signed.Schema != SignedObservationSchema || signed.Signature.Algorithm != SignatureAlgorithmEd25519 ||
		!contains(manifest.AllowedSignerKeyIDs, signed.Signature.KeyID) || !trustedOK || len(publicKey) != ed25519.PublicKeySize ||
		digest(publicKey) != trusted.PublicKeySHA256 {
		return nil, nil, ErrInvalidSignature
	}
	payloadRaw, err := CanonicalPayload(signed.Payload)
	if err != nil {
		return nil, nil, err
	}
	signature, err := base64.StdEncoding.DecodeString(signed.Signature.Value)
	if err != nil {
		signature, err = base64.RawStdEncoding.DecodeString(signed.Signature.Value)
	}
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, payloadRaw, signature) {
		return nil, nil, ErrInvalidSignature
	}
	rawEvidence, err := signed.Evidence.Verify(manifest, collectorKey)
	if err != nil || digest(rawEvidence) != signed.Payload.RawEvidenceDigest || signed.Evidence.Payload.ReleaseCommit != releaseCommit {
		return nil, nil, ErrInvalidSignature
	}
	recomputed, err := RecomputeObservation(manifest, signed.Evidence.Payload)
	if err != nil {
		return nil, nil, err
	}
	recomputedRaw, leftErr := CanonicalPayload(recomputed)
	if leftErr != nil || !bytes.Equal(recomputedRaw, payloadRaw) {
		return nil, nil, fmt.Errorf("%w: signed aggregates were not recomputed from raw evidence", ErrInvalidObservation)
	}
	if err := signed.Payload.Validate(manifest, releaseCommit); err != nil {
		return nil, nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if signed.Payload.EndedAt.After(now.Add(time.Duration(manifest.Freshness.MaximumFutureSeconds)*time.Second)) ||
		now.After(signed.Payload.ExpiresAt) || !signed.Payload.ExpiresAt.Equal(signed.Payload.EndedAt.Add(time.Duration(manifest.Freshness.MaximumAgeSeconds)*time.Second)) {
		return nil, nil, fmt.Errorf("%w: observation is stale, future-dated, or replayable", ErrInvalidObservation)
	}
	return payloadRaw, signature, nil
}

func (observation SoakObservation) Validate(manifest Manifest, releaseCommit string) error {
	if observation.Schema != ObservationSchema || observation.ReleaseCommit != releaseCommit || observation.Backend != manifest.Backend ||
		observation.FeatureFlag != manifest.FeatureFlag || observation.FeatureFlagValue != manifest.FeatureFlagValue || strings.TrimSpace(observation.ProbeVersion) == "" ||
		!isDigest(observation.RunID) || !isDigest(observation.Nonce) || observation.ExpiresAt.IsZero() || !isDigest(observation.RawEvidenceDigest) || !isDigest(observation.HostAttestationDigest) {
		return fmt.Errorf("%w: release, backend, flag, or probe mismatch", ErrInvalidObservation)
	}
	if !observation.Conclusive {
		return ErrInconclusive
	}
	if observation.EvidenceMode == EvidenceModeLive {
		if observation.Synthetic {
			return fmt.Errorf("%w: live evidence is marked synthetic", ErrInvalidObservation)
		}
	} else if observation.EvidenceMode == EvidenceModeFixture {
		if !observation.Synthetic {
			return fmt.Errorf("%w: fixture evidence is not marked synthetic", ErrInvalidObservation)
		}
	} else {
		return fmt.Errorf("%w: evidence mode", ErrInvalidObservation)
	}
	if observation.StartedAt.IsZero() || observation.EndedAt.IsZero() || !observation.EndedAt.After(observation.StartedAt) ||
		!finite(observation.DurationSeconds) || !finite(observation.ConcurrentRoomSeconds) || !finite(observation.ParticipantMinutes) ||
		observation.DurationSeconds < 0 || observation.ConcurrentRoomSeconds < 0 || observation.ParticipantMinutes < 0 {
		return ErrInconclusive
	}
	wallSeconds := observation.EndedAt.Sub(observation.StartedAt).Seconds()
	if math.Abs(wallSeconds-observation.DurationSeconds) > 5 {
		return fmt.Errorf("%w: duration does not match signed timestamps", ErrInvalidObservation)
	}
	if len(observation.Rooms) == 0 {
		return ErrInconclusive
	}
	seenScopes := map[string]bool{}
	seenRooms := map[string]bool{}
	for _, room := range observation.Rooms {
		if !isDigest(room.RoomDigest) || !isDigest(room.SittingDigest) || !isDigest(room.MediaGenerationDigest) ||
			room.MinimumPublishers < 0 || room.MinimumSubscribers < 0 || !finite(room.ActiveSeconds) || room.ActiveSeconds < 0 {
			return fmt.Errorf("%w: room scope", ErrInvalidObservation)
		}
		scope := room.RoomDigest + ":" + room.SittingDigest + ":" + room.MediaGenerationDigest
		if seenScopes[scope] {
			return fmt.Errorf("%w: duplicate room scope", ErrInvalidObservation)
		}
		if seenRooms[room.RoomDigest] {
			return fmt.Errorf("%w: duplicate room digest", ErrInvalidObservation)
		}
		seenScopes[scope] = true
		seenRooms[room.RoomDigest] = true
	}
	return validateFiniteMetrics(observation)
}

func validateFiniteMetrics(observation SoakObservation) error {
	values := []float64{
		observation.HeadOfLine.BlockedOfferSeconds, observation.HeadOfLine.RoomBAdmissionP95Seconds,
		observation.HeadOfLine.RoomBRenegotiationP95Seconds, observation.MediaHealth.PacketLossPercent,
		observation.MediaHealth.UnexpectedDisconnectParticipantMinutePercent, observation.MediaHealth.SustainedCPUPercent,
		observation.MediaHealth.RSSContainerLimitPercent, observation.MediaHealth.NetworkSampleCoverageSeconds,
		observation.MediaHealth.ResourceSampleCoverageSeconds, observation.Canaries.SourceObservationRate,
	}
	for _, value := range values {
		if !finite(value) || value < 0 {
			return fmt.Errorf("%w: non-finite or negative metric", ErrInvalidObservation)
		}
	}
	if observation.MediaHealth.PacketLossPercent > 100 || observation.MediaHealth.UnexpectedDisconnectParticipantMinutePercent > 100 ||
		observation.MediaHealth.SustainedCPUPercent > 100 || observation.MediaHealth.RSSContainerLimitPercent > 100 ||
		observation.Canaries.SourceObservationRate > 1 {
		return fmt.Errorf("%w: metric outside its domain", ErrInvalidObservation)
	}
	counts := []int{
		observation.HeadOfLine.RoomBAdmissionSamples, observation.HeadOfLine.RoomBAdmissionFailures,
		observation.HeadOfLine.RoomBRenegotiationSamples, observation.HeadOfLine.RoomBRenegotiationFailures,
		observation.MediaHealth.NetworkSampleCount, observation.MediaHealth.ResourceSampleCount,
		observation.Canaries.RoomBoundaryChecks, observation.Canaries.SittingBoundaryChecks, observation.Canaries.MediaGenerationBoundaryChecks,
		observation.Canaries.TrackLeakCount, observation.Canaries.IdentityLeakCount, observation.Canaries.ChatLeakCount,
		observation.Canaries.ScoutLeakCount, observation.Canaries.TranscriptLeakCount, observation.Canaries.RecapLeakCount,
		observation.Canaries.ArtifactLeakCount, observation.Canaries.WrongSittingLeakCount, observation.Canaries.WrongMediaGenerationLeakCount,
		observation.AIFailure.FailureInjections, observation.AIFailure.MediaInterruptions, observation.AIFailure.AdmissionFailures,
	}
	for _, value := range counts {
		if value < 0 {
			return fmt.Errorf("%w: negative count", ErrInvalidObservation)
		}
	}
	return nil
}

func (receipt Receipt) canonicalDigest() (string, error) {
	receipt.ReceiptDigest = ""
	raw, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	return digest(raw), nil
}

func (receipt Receipt) Validate(manifest Manifest, manifestDigest, releaseCommit string, publicKey, collectorKey ed25519.PublicKey, now time.Time) error {
	if receipt.Schema != ReceiptSchema || receipt.GateID != manifest.ID || receipt.GeneratedAt.IsZero() || receipt.ReleaseCommit != releaseCommit ||
		receipt.Backend != manifest.Backend || receipt.FeatureFlag != manifest.FeatureFlag || receipt.FeatureFlagValue != manifest.FeatureFlagValue ||
		(receipt.EvidenceMode != EvidenceModeLive && receipt.EvidenceMode != EvidenceModeFixture) || strings.TrimSpace(receipt.ProbeVersion) == "" ||
		receipt.ManifestDigest != manifestDigest || !isDigest(receipt.PayloadDigest) || !isDigest(receipt.SignatureDigest) ||
		!contains(manifest.AllowedSignerKeyIDs, receipt.SignerKeyID) || !isDigest(receipt.SignerKeyDigest) ||
		receipt.StopCondition != manifest.StopCondition || receipt.RollbackCommand != manifest.RollbackCommand || len(receipt.Thresholds) == 0 {
		return ErrInvalidReceipt
	}
	signed := SignedObservation{Schema: SignedObservationSchema, Payload: receipt.Observation, Evidence: receipt.Evidence, Signature: receipt.Signature}
	payloadRaw, signatureRaw, err := signed.Verify(manifest, releaseCommit, publicKey, collectorKey, now)
	if err != nil || digest(payloadRaw) != receipt.PayloadDigest || digest(signatureRaw) != receipt.SignatureDigest ||
		receipt.SignerKeyID != receipt.Signature.KeyID || digest(publicKey) != receipt.SignerKeyDigest || receipt.ProbeVersion != receipt.Observation.ProbeVersion {
		return ErrInvalidReceipt
	}
	if receipt.EvidenceMode != receipt.Observation.EvidenceMode {
		return ErrInvalidReceipt
	}
	wantThresholds := evaluate(manifest, receipt.Observation)
	if len(wantThresholds) != len(receipt.Thresholds) {
		return ErrInvalidReceipt
	}
	allPassed := true
	for index, result := range receipt.Thresholds {
		if result != wantThresholds[index] || strings.TrimSpace(result.ID) == "" || !finite(result.Actual) || !finite(result.Expected) {
			return ErrInvalidReceipt
		}
		allPassed = allPassed && result.Passed
	}
	switch receipt.EvidenceMode {
	case EvidenceModeLive:
		if receipt.ReleaseQualified != allPassed || receipt.StopTriggered == receipt.ReleaseQualified ||
			(receipt.ReleaseQualified && receipt.Verdict != VerdictPass) || (!receipt.ReleaseQualified && receipt.Verdict != VerdictFail) {
			return ErrInvalidReceipt
		}
	case EvidenceModeFixture:
		if receipt.ReleaseQualified || !receipt.StopTriggered || receipt.Verdict != VerdictNonQualifying {
			return ErrInvalidReceipt
		}
	}
	want, err := receipt.canonicalDigest()
	if err != nil || want != receipt.ReceiptDigest {
		return ErrInvalidReceipt
	}
	return nil
}

func ValidateReleaseCommit(value string) error {
	if !releaseCommitPattern.MatchString(value) {
		return errors.New("release commit must be a full lowercase Git SHA")
	}
	return nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func isDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func validateRelativePath(path string) error {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return errors.New("path must be clean and repository-relative")
	}
	return nil
}

func rootedPath(root, path string) (string, error) {
	if err := validateRelativePath(path); err != nil {
		return "", err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(root, path)
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes repository root")
	}
	current := root
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			// The remaining suffix does not exist yet; callers create it beneath
			// the last verified real directory.
			break
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("path contains a symlink")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", errors.New("path parent is not a directory")
		}
	}
	return resolved, nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func compare(actual, expected float64, operator string) bool {
	switch operator {
	case "gte":
		return actual >= expected
	case "lt":
		return actual < expected
	case "eq":
		return actual == expected
	default:
		return false
	}
}
