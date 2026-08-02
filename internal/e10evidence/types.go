// Package e10evidence validates the operator-owned evidence envelopes used by
// STRIDE E10. It never captures media, calls a provider, changes production, or
// turns a structurally valid envelope into provider or production acceptance.
package e10evidence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	TrustRootsSchema                = "stride.e10.trust-roots/v2"
	TargetRegistrySchema            = "stride.e10.target-registry/v4"
	CorpusManifestSchema            = "stride.e10.corpus-manifest/v4"
	PilotPacketSchema               = "stride.e10.io-pilot-packet/v5"
	QualificationResultSchema       = "stride.e10.qualification-result/v2"
	QualificationImportBundleSchema = "stride.e10.qualification-import-bundle/v1"
	ExternalMatrixSchema            = "stride.e10.external-matrix/v3"
	ValidationSchema                = "stride.e10.capture-validation/v2"

	EvidenceClass = "operator_packet_structure_only"
)

var (
	shaPattern    = mustPattern(64, "0123456789abcdef")
	commitPattern = mustPatternEither(40, 64, "0123456789abcdef")
	identifier    = mustIdentifier()
)

type CandidateBinding struct {
	ReleaseCommit  string `json:"releaseCommit"`
	GitTreeDigest  string `json:"gitTreeDigest"`
	ImageDigest    string `json:"imageDigest"`
	ConfigDigest   string `json:"configDigest"`
	RouteMapDigest string `json:"routeMapDigest"`
}

type TrustRoots struct {
	SchemaVersion                      string           `json:"schemaVersion"`
	TrustRootID                        string           `json:"trustRootId"`
	PreMeasurementTargetRegistrySHA256 string           `json:"preMeasurementTargetRegistrySha256"`
	ApprovedSigners                    []ApprovedSigner `json:"approvedSigners"`
}

// ApprovedTrustRoots can only be constructed by matching exact trust-root bytes
// to a separately provisioned approved digest. A packet caller therefore cannot
// turn an arbitrary supplied public key into its own trust anchor.
type ApprovedTrustRoots struct {
	roots     TrustRoots
	rawDigest string
}

type ApprovedSigner struct {
	KeyID                      string `json:"keyId"`
	IdentityID                 string `json:"identityId"`
	Role                       string `json:"role"`
	PublicKeyFingerprintSHA256 string `json:"publicKeyFingerprintSha256"`
}

type RegistrySignerBinding struct {
	SignerKeyID                      string `json:"signerKeyId"`
	SignerIdentityID                 string `json:"signerIdentityId"`
	SignerPublicKeyFingerprintSHA256 string `json:"signerPublicKeyFingerprintSha256"`
}

type DualApprovalBinding struct {
	RegistrySHA256                     string `json:"registrySha256"`
	SourceArtifactSetSHA256            string `json:"sourceArtifactSetSha256"`
	OperatorID                         string `json:"operatorId"`
	OperatorKeyID                      string `json:"operatorKeyId"`
	OperatorPublicKeyFingerprintSHA256 string `json:"operatorPublicKeyFingerprintSha256"`
	ReviewerID                         string `json:"reviewerId"`
	ReviewerKeyID                      string `json:"reviewerKeyId"`
	ReviewerPublicKeyFingerprintSHA256 string `json:"reviewerPublicKeyFingerprintSha256"`
}

type TargetRegistry struct {
	SchemaVersion string                `json:"schemaVersion"`
	RegistryID    string                `json:"registryId"`
	Signer        RegistrySignerBinding `json:"signer"`
	Candidate     CandidateBinding      `json:"candidate"`
	Targets       []EvidenceTarget      `json:"targets"`
}

type EvidenceTarget struct {
	ID                        string            `json:"id"`
	Category                  string            `json:"category"`
	FixtureSHA256             string            `json:"fixtureSha256"`
	Environment               string            `json:"environment"`
	MinimumArtifacts          int               `json:"minimumArtifacts"`
	MinimumSampleSize         int               `json:"minimumSampleSize"`
	MeasurementRevisionSHA256 string            `json:"measurementRevisionSha256"`
	OwnerID                   string            `json:"ownerId"`
	IndependentReviewerID     string            `json:"independentReviewerId"`
	RollbackTrigger           string            `json:"rollbackTrigger"`
	PhysicalOrProduction      bool              `json:"physicalOrProduction"`
	RequiredMetrics           []MetricThreshold `json:"requiredMetrics"`
}

type MetricThreshold struct {
	Name       string  `json:"name"`
	Comparator string  `json:"comparator"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
}

type CorpusManifest struct {
	SchemaVersion              string                                `json:"schemaVersion"`
	TenantID                   string                                `json:"tenantId"`
	CorpusID                   string                                `json:"corpusId"`
	TargetID                   string                                `json:"targetId"`
	FixtureSHA256              string                                `json:"fixtureSha256"`
	QualificationSubjectSHA256 string                                `json:"qualificationSubjectSha256"`
	Lane                       string                                `json:"lane"`
	EvidenceClass              string                                `json:"evidenceClass"`
	Candidate                  CandidateBinding                      `json:"candidate"`
	Approval                   DualApprovalBinding                   `json:"approval"`
	MeetingSpecialistBinding   MeetingSpecialistQualificationBinding `json:"meetingSpecialistBinding"`
	Clips                      []CorpusClip                          `json:"clips"`
}

// MeetingSpecialistQualificationBinding is the exact provider/voice contract
// whose canonical digest becomes the preregistered target fixture. A production
// adapter can compare every field to server-owned configuration without
// interpreting an opaque local config claim.
type MeetingSpecialistQualificationBinding struct {
	Provider                string `json:"provider"`
	Model                   string `json:"model"`
	Voice                   string `json:"voice"`
	RouteDigest             string `json:"routeDigest"`
	AccountingProfileDigest string `json:"accountingProfileDigest"`
	RuntimeProfileDigest    string `json:"runtimeProfileDigest"`
	CapabilityPolicyDigest  string `json:"capabilityPolicyDigest"`
}

type CorpusClip struct {
	ClipID                string `json:"clipId"`
	AudioSHA256           string `json:"audioSha256"`
	ReferenceSHA256       string `json:"referenceSha256"`
	ConsentReceiptSHA256  string `json:"consentReceiptSha256"`
	SpeakerIDHash         string `json:"speakerIdHash"`
	SpeakerEvidenceSHA256 string `json:"speakerEvidenceSha256"`
	TrackID               string `json:"trackId"`
	TrackEvidenceSHA256   string `json:"trackEvidenceSha256"`
	DurationMillis        int64  `json:"durationMillis"`
	Platform              string `json:"platform"`
	ComposerSurface       string `json:"composerSurface,omitempty"`
	SourceOrder           int64  `json:"sourceOrder"`
	TargetDevice          bool   `json:"targetDevice"`
	Synthetic             bool   `json:"synthetic"`
}

type PilotPacket struct {
	SchemaVersion              string                  `json:"schemaVersion"`
	TenantID                   string                  `json:"tenantId"`
	PacketID                   string                  `json:"packetId"`
	TargetID                   string                  `json:"targetId"`
	FixtureSHA256              string                  `json:"fixtureSha256"`
	QualificationSubjectSHA256 string                  `json:"qualificationSubjectSha256"`
	EvidenceClass              string                  `json:"evidenceClass"`
	Candidate                  CandidateBinding        `json:"candidate"`
	Approval                   DualApprovalBinding     `json:"approval"`
	ReviewerRoster             []EligiblePilotReviewer `json:"reviewerRoster"`
	Pilots                     []IOPilot               `json:"pilots"`
}

// QualificationResultPacket is the independently signed bridge between a
// preregistered source packet and an evaluator result. Evaluator output remains
// in the governed evidence store; this packet binds only its exact digest and
// the immutable evaluator configuration used to produce it.
type QualificationResultPacket struct {
	SchemaVersion              string              `json:"schemaVersion"`
	ResultID                   string              `json:"resultId"`
	TenantID                   string              `json:"tenantId"`
	TargetID                   string              `json:"targetId"`
	Lane                       string              `json:"lane"`
	EvidenceClass              string              `json:"evidenceClass"`
	Candidate                  CandidateBinding    `json:"candidate"`
	Approval                   DualApprovalBinding `json:"approval"`
	SourcePacketKind           string              `json:"sourcePacketKind"`
	SourcePacketSHA256         string              `json:"sourcePacketSha256"`
	QualificationSubjectSHA256 string              `json:"qualificationSubjectSha256"`
	EvaluatorConfigSHA256      string              `json:"evaluatorConfigSha256"`
	EvaluatorResultSHA256      string              `json:"evaluatorResultSha256"`
	Qualified                  bool                `json:"qualified"`
	EvaluatedAt                string              `json:"evaluatedAt"`
}

// QualificationImportRecord is a read-only projection of a package-minted
// capability. Possessing or constructing this projection does not authorize an
// import; QualificationEvidenceStore accepts VerifiedQualificationResult.
type QualificationImportRecord struct {
	ResultID                   string                                `json:"resultId"`
	TenantID                   string                                `json:"tenantId"`
	TargetID                   string                                `json:"targetId"`
	FixtureSHA256              string                                `json:"fixtureSha256"`
	QualificationSubjectSHA256 string                                `json:"qualificationSubjectSha256"`
	Lane                       string                                `json:"lane"`
	Qualified                  bool                                  `json:"qualified"`
	Candidate                  CandidateBinding                      `json:"candidate"`
	MeetingSpecialistBinding   MeetingSpecialistQualificationBinding `json:"meetingSpecialistBinding"`
	RegistrySHA256             string                                `json:"registrySha256"`
	TrustRootSHA256            string                                `json:"trustRootSha256"`
	SourcePacketKind           string                                `json:"sourcePacketKind"`
	SourcePacketSHA256         string                                `json:"sourcePacketSha256"`
	SourceSignatureSetSHA256   string                                `json:"sourceSignatureSetSha256"`
	ResultPacketSHA256         string                                `json:"resultPacketSha256"`
	ResultSignatureSetSHA256   string                                `json:"resultSignatureSetSha256"`
	EvaluatorConfigSHA256      string                                `json:"evaluatorConfigSha256"`
	EvaluatorResultSHA256      string                                `json:"evaluatorResultSha256"`
	EvaluatedAt                string                                `json:"evaluatedAt"`
}

// VerifiedQualificationResult is opaque. It can only be minted after the
// registry, source receipt, source bytes, evaluator-result packet, and both
// detached result signatures have all been verified together.
type VerifiedQualificationResult struct {
	record QualificationImportRecord
	proof  [sha256.Size]byte
}

// QualificationSignatureMaterial carries one detached signature and its exact
// public key. The key is still only a claim until it is matched to the
// separately anchored trust-root signer roster during bundle verification.
type QualificationSignatureMaterial struct {
	SignatureHex string `json:"signatureHex"`
	PublicKeyHex string `json:"publicKeyHex"`
}

// QualificationImportBundle is the serializable handoff between an evidence
// verifier and a qualification store. It contains the complete signed source
// chain needed to re-run trust verification in another process; the bundle is
// never itself an authority or trust root.
type QualificationImportBundle struct {
	SchemaVersion     string                         `json:"schemaVersion"`
	TenantID          string                         `json:"tenantId"`
	SourcePacketKind  string                         `json:"sourcePacketKind"`
	RegistryRaw       json.RawMessage                `json:"registry"`
	RegistryAuthority QualificationSignatureMaterial `json:"registryAuthority"`
	SourceRaw         json.RawMessage                `json:"sourcePacket"`
	SourceOperator    QualificationSignatureMaterial `json:"sourceOperator"`
	SourceReviewer    QualificationSignatureMaterial `json:"sourceReviewer"`
	ResultRaw         json.RawMessage                `json:"resultPacket"`
	ResultOperator    QualificationSignatureMaterial `json:"resultOperator"`
	ResultReviewer    QualificationSignatureMaterial `json:"resultReviewer"`
}

type IOPilot struct {
	PilotID                          string                `json:"pilotId"`
	InputDigest                      string                `json:"inputDigest"`
	RunReceiptDigest                 string                `json:"runReceiptDigest"`
	ArtifactDigest                   string                `json:"artifactDigest"`
	Disposition                      string                `json:"disposition"`
	DispositionReasonDigest          string                `json:"dispositionReasonDigest"`
	TerminalVisibilityReceiptDigest  string                `json:"terminalVisibilityReceiptDigest"`
	RevisionCount                    int                   `json:"revisionCount"`
	AssertedClaimCount               int                   `json:"assertedClaimCount"`
	SourcedAssertedClaimCount        int                   `json:"sourcedAssertedClaimCount"`
	InventedAssertedClaimCount       int                   `json:"inventedAssertedClaimCount"`
	UnauthorizedDisclosureCount      int                   `json:"unauthorizedDisclosureCount"`
	ExternalWriteCount               int                   `json:"externalWriteCount"`
	ExternalEffectAuditReceiptDigest string                `json:"externalEffectAuditReceiptDigest"`
	Reviews                          []PilotReviewDecision `json:"reviews"`
}

type EligiblePilotReviewer struct {
	ReviewerID                         string `json:"reviewerId"`
	ReviewerKeyID                      string `json:"reviewerKeyId"`
	ReviewerPublicKeyFingerprintSHA256 string `json:"reviewerPublicKeyFingerprintSha256"`
	EligibilityReceiptDigest           string `json:"eligibilityReceiptDigest"`
}

type PilotReviewDecision struct {
	ReviewerID           string `json:"reviewerId"`
	ReviewerKeyID        string `json:"reviewerKeyId"`
	ReviewerPublicKeyHex string `json:"reviewerPublicKeyHex"`
	ReviewReceiptDigest  string `json:"reviewReceiptDigest"`
	Disposition          string `json:"disposition"`
	SignatureHex         string `json:"signatureHex"`
}

type ExternalMatrix struct {
	SchemaVersion string                `json:"schemaVersion"`
	MatrixID      string                `json:"matrixId"`
	Category      string                `json:"category"`
	EvidenceClass string                `json:"evidenceClass"`
	Candidate     CandidateBinding      `json:"candidate"`
	Approval      DualApprovalBinding   `json:"approval"`
	Observations  []ExternalObservation `json:"observations"`
}

type ExternalObservation struct {
	TargetID                  string            `json:"targetId"`
	ArtifactSHA256            string            `json:"artifactSha256"`
	FixtureSHA256             string            `json:"fixtureSha256"`
	Environment               string            `json:"environment"`
	SampleSize                int               `json:"sampleSize"`
	MeasurementRevisionSHA256 string            `json:"measurementRevisionSha256"`
	ObservedAt                string            `json:"observedAt"`
	Verdict                   string            `json:"verdict"`
	Metrics                   map[string]Metric `json:"metrics"`
}

type Metric struct {
	Value       float64              `json:"value"`
	Unit        string               `json:"unit"`
	Numerator   *int                 `json:"numerator,omitempty"`
	Denominator *int                 `json:"denominator,omitempty"`
	P50         *float64             `json:"p50,omitempty"`
	P95         *float64             `json:"p95,omitempty"`
	P99         *float64             `json:"p99,omitempty"`
	Interval95  *StatisticalInterval `json:"interval95,omitempty"`
	Samples     []float64            `json:"samples,omitempty"`
}

type StatisticalInterval struct {
	Low    float64 `json:"low"`
	High   float64 `json:"high"`
	Method string  `json:"method"`
}

type ValidationReceipt struct {
	SchemaVersion      string           `json:"schemaVersion"`
	EvidenceClass      string           `json:"evidenceClass"`
	PacketKind         string           `json:"packetKind"`
	State              string           `json:"state"`
	InputSHA256        string           `json:"inputSha256"`
	RegistrySHA256     string           `json:"registrySha256"`
	TrustRootSHA256    string           `json:"trustRootSha256"`
	SignatureSetSHA256 string           `json:"signatureSetSha256"`
	Candidate          CandidateBinding `json:"candidate"`
	ItemCount          int              `json:"itemCount"`
	ClaimsExcluded     []string         `json:"claimsExcluded"`
}

// VerifiedValidationReceipt is an opaque, package-minted capability. Its
// public JSON projection is deliberately only a structure-only receipt; any
// downstream consumer must either retain this in-process value or reverify the
// serialized receipt against the original signed source envelope.
type VerifiedValidationReceipt struct {
	receipt ValidationReceipt
	proof   [sha256.Size]byte
}

func DecodeStrict[T any](body []byte) (T, error) {
	var value T
	if err := rejectDuplicateJSONKeys(body); err != nil {
		return value, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return value, errors.New("multiple JSON values are not allowed")
		}
		return value, err
	}
	return value, nil
}

// DecodeCanonicalStrict rejects semantically equivalent reformatting. Detached
// signatures therefore cover the exact single canonical byte representation.
func DecodeCanonicalStrict[T any](body []byte) (T, error) {
	value, err := DecodeStrict[T](body)
	if err != nil {
		return value, err
	}
	canonical, err := CanonicalizeJSON(body)
	if err != nil {
		return value, err
	}
	if !bytes.Equal(body, canonical) {
		return value, errors.New("packet is not canonical JSON; sign the exact compact canonical bytes")
	}
	return value, nil
}

// CanonicalizeJSON returns the compact, recursively key-sorted representation
// used for every detached signature. Numbers retain their submitted JSON
// lexical representation, so producers must serialize once before signing.
func CanonicalizeJSON(body []byte) ([]byte, error) {
	if err := rejectDuplicateJSONKeys(body); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values are not allowed")
		}
		return nil, err
	}
	return json.Marshal(value)
}

func rejectDuplicateJSONKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("JSON object contains a duplicate or invalid key")
			}
			seen[name] = true
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("JSON object is malformed")
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("JSON array is malformed")
		}
	default:
		return errors.New("JSON delimiter is invalid")
	}
	return nil
}

func ValidateTrustRoots(roots TrustRoots) error {
	if roots.SchemaVersion != TrustRootsSchema || !validID(roots.TrustRootID) || !validSHA(roots.PreMeasurementTargetRegistrySHA256) || len(roots.ApprovedSigners) < 5 {
		return errors.New("trust roots schema, identity, or signer set is invalid")
	}
	seenKeys := map[string]bool{}
	seenFingerprints := map[string]bool{}
	roles := map[string]bool{}
	pilotReviewers := map[string]bool{}
	for _, signer := range roots.ApprovedSigners {
		if !validID(signer.KeyID) || !validID(signer.IdentityID) || !oneOf(signer.Role, "registry_owner", "operator", "independent_reviewer", "pilot_reviewer") || !validSHA(signer.PublicKeyFingerprintSHA256) {
			return fmt.Errorf("approved signer %q is invalid", signer.KeyID)
		}
		if seenKeys[signer.KeyID] || seenFingerprints[signer.PublicKeyFingerprintSHA256] {
			return fmt.Errorf("approved signer key/fingerprint %q is duplicated", signer.KeyID)
		}
		seenKeys[signer.KeyID] = true
		seenFingerprints[signer.PublicKeyFingerprintSHA256] = true
		roles[signer.Role] = true
		if signer.Role == "pilot_reviewer" {
			pilotReviewers[signer.IdentityID] = true
		}
	}
	for _, role := range []string{"registry_owner", "operator", "independent_reviewer"} {
		if !roles[role] {
			return fmt.Errorf("trust roots lack an approved %s signer", role)
		}
	}
	if len(pilotReviewers) < 2 {
		return errors.New("trust roots lack two distinct approved pilot_reviewer identities")
	}
	return nil
}

func LoadApprovedTrustRoots(raw []byte, approvedSHA256 string) (ApprovedTrustRoots, error) {
	if !validSHA(approvedSHA256) || RegistryDigest(raw) != approvedSHA256 {
		return ApprovedTrustRoots{}, errors.New("trust-root bytes do not match the separately approved SHA-256 anchor")
	}
	roots, err := DecodeStrict[TrustRoots](raw)
	if err != nil {
		return ApprovedTrustRoots{}, fmt.Errorf("trust roots: %w", err)
	}
	if err := ValidateTrustRoots(roots); err != nil {
		return ApprovedTrustRoots{}, err
	}
	return ApprovedTrustRoots{roots: roots, rawDigest: approvedSHA256}, nil
}

type requiredTargetContract struct {
	Category          string
	MinimumArtifacts  int
	MinimumSampleSize int
	Metrics           []MetricThreshold
}

func requiredTarget(category string, artifacts, samples int, metrics ...MetricThreshold) requiredTargetContract {
	return requiredTargetContract{Category: category, MinimumArtifacts: artifacts, MinimumSampleSize: samples, Metrics: metrics}
}

func metricFloor(name, comparator string, value float64, unit string) MetricThreshold {
	return MetricThreshold{Name: name, Comparator: comparator, Value: value, Unit: unit}
}

// requiredTargetContracts is the additive-only E10 floor. Registries may add
// targets or tighten these values, but cannot omit or weaken any target after
// measurements exist without a new independently approved design revision.
var requiredTargetContracts = map[string]requiredTargetContract{
	"meeting-stt-live-provider-evaluation": requiredTarget("qualification_evaluator", 1, 120,
		metricFloor("clip_count", "at_least", 120, "count"), metricFloor("duration_seconds", "at_least", 3600, "seconds"), metricFloor("corpus_word_error_rate", "at_most", 10, "percent"), metricFloor("domain_term_accuracy", "at_least", 97, "percent"), metricFloor("numeric_accuracy", "at_least", 98, "percent"), metricFloor("final_latency_p95_seconds", "at_most", 3, "seconds"), metricFloor("integrity_events", "at_least", 10000, "count")),
	"composer-dictation-target-device-evaluation": requiredTarget("qualification_evaluator", 1, 250,
		metricFloor("utterance_count", "at_least", 250, "count"), metricFloor("first_attempt_success_percent", "at_least", 99, "percent"), metricFloor("submit_to_post_p95_seconds", "at_most", 3, "seconds"), metricFloor("duplicate_posts", "exactly", 0, "count"), metricFloor("privacy_leaks", "exactly", 0, "count")),
	"insights-opportunities-real-input-pilots": requiredTarget("qualification_evaluator", 10, 10,
		metricFloor("pilot_count", "exactly", 10, "count"), metricFloor("accepted_pilots", "at_least", 8, "count"), metricFloor("independent_reviewers", "at_least", 2, "count"), metricFloor("invented_asserted_claims", "exactly", 0, "count"), metricFloor("unauthorized_external_writes", "exactly", 0, "count")),
	"meeting-specialist-provider-voice-evaluation": requiredTarget("qualification_evaluator", 3, 10,
		metricFloor("approved_session_count", "at_least", 10, "count"), metricFloor("provider_join_success_percent", "exactly", 100, "percent"), metricFloor("audible_output_success_percent", "exactly", 100, "percent"), metricFloor("physical_target_devices", "at_least", 2, "count"), metricFloor("unauthorized_joins", "exactly", 0, "count"), metricFloor("human_media_interruptions", "exactly", 0, "count"), metricFloor("budget_or_receipt_failures", "exactly", 0, "count")),
	"two-three-person-two-hour-rooms": requiredTarget("physical_device_webrtc", 2, 3,
		metricFloor("duration_seconds", "at_least", 7200, "seconds"), metricFloor("participant_count", "at_least", 3, "count"), metricFloor("fatal_media_failures", "exactly", 0, "count"), metricFloor("transcript_gap_seconds", "at_most", 5, "seconds")),
	"gallery-speaker-expanded-screen-share": requiredTarget("physical_device_webrtc", 3, 4,
		metricFloor("mode_success_percent", "exactly", 100, "percent"), metricFloor("screen_share_success_percent", "exactly", 100, "percent"), metricFloor("fatal_failures", "exactly", 0, "count")),
	"packet-loss-disconnect-rejoin-cleanup": requiredTarget("physical_device_webrtc", 3, 10,
		metricFloor("successful_rejoin_percent", "at_least", 99, "percent"), metricFloor("orphan_media_tracks", "exactly", 0, "count"), metricFloor("cleanup_p95_seconds", "at_most", 10, "seconds")),
	"browser-native-mixed-room": requiredTarget("physical_device_webrtc", 3, 6,
		metricFloor("join_success_percent", "exactly", 100, "percent"), metricFloor("cross_client_media_success_percent", "exactly", 100, "percent")),
	"guest-room-boundary": requiredTarget("physical_device_webrtc", 3, 10,
		metricFloor("authorized_join_success_percent", "exactly", 100, "percent"), metricFloor("unauthorized_observations", "exactly", 0, "count")),
	"restrictive-network-turn": requiredTarget("physical_device_webrtc", 3, 10,
		metricFloor("turn_relay_percent", "exactly", 100, "percent"), metricFloor("successful_media_flows", "at_least", 10, "count"), metricFloor("fatal_media_failures", "exactly", 0, "count")),
	"bluetooth-audio-route-change": requiredTarget("physical_device_webrtc", 2, 6,
		metricFloor("route_change_success_percent", "exactly", 100, "percent"), metricFloor("duplicate_audio_tracks", "exactly", 0, "count")),
	"camera-switch-background-lock": requiredTarget("physical_device_webrtc", 3, 9,
		metricFloor("lifecycle_recovery_percent", "exactly", 100, "percent"), metricFloor("stale_track_publications", "exactly", 0, "count")),
	"multiple-devices-one-account": requiredTarget("physical_device_webrtc", 2, 6,
		metricFloor("correct_roster_identity_percent", "exactly", 100, "percent"), metricFloor("cross_device_session_leaks", "exactly", 0, "count")),
	"induced-ai-failure-human-media": requiredTarget("physical_device_webrtc", 3, 10,
		metricFloor("human_media_continuity_percent", "exactly", 100, "percent"), metricFloor("human_media_interruptions", "exactly", 0, "count")),
	"room-media-aggregate": requiredTarget("physical_device_webrtc", 2, 200,
		metricFloor("join_success_percent", "at_least", 99.5, "percent"), metricFloor("first_remote_audio_p95_seconds", "at_most", 2.5, "seconds"), metricFloor("first_remote_video_p95_seconds", "at_most", 3.5, "seconds"), metricFloor("network_loss_recovery_p95_seconds", "at_most", 8, "seconds"), metricFloor("concurrent_room_count", "at_least", 2, "count"), metricFloor("participants_per_room", "at_least", 3, "count"), metricFloor("concurrent_soak_duration_seconds", "at_least", 7200, "seconds"), metricFloor("cross_room_events", "exactly", 0, "count"), metricFloor("unintended_fatal_disconnects", "exactly", 0, "count"), metricFloor("participant_outages_over_five_seconds", "exactly", 0, "count"), metricFloor("cpu_p95_percent_of_baseline", "at_most", 110, "percent_of_baseline"), metricFloor("rss_p95_percent_of_baseline", "at_most", 110, "percent_of_baseline"), metricFloor("post_cycle_rss_drift_percent", "at_most", 5, "percent"), metricFloor("join_leave_cycles", "at_least", 20, "count")),
	"locked-device-push-deep-link": requiredTarget("physical_device_webrtc", 3, 100,
		metricFloor("locked_delivery_success_percent", "exactly", 100, "percent"), metricFloor("authorized_deep_link_success_percent", "exactly", 100, "percent"), metricFloor("wrong_destination_opens", "exactly", 0, "count"), metricFloor("unauthorized_opens", "exactly", 0, "count"), metricFloor("lock_screen_private_content_disclosures", "exactly", 0, "count"), metricFloor("open_p95_seconds", "at_most", 5, "seconds")),
	"encrypted-immutable-offsite-custody": requiredTarget("ha_dr", 2, 2,
		metricFloor("encrypted_objects_percent", "exactly", 100, "percent"), metricFloor("immutable_objects_percent", "exactly", 100, "percent"), metricFloor("offsite_digest_local_manifest_match_percent", "exactly", 100, "percent"), metricFloor("custody_policy_violations", "exactly", 0, "count")),
	"independent-key-and-restore-host-custody": requiredTarget("ha_dr", 2, 2,
		metricFloor("independent_key_holders", "at_least", 2, "count"), metricFloor("independent_restore_hosts", "at_least", 1, "count"), metricFloor("restore_host_production_memberships", "exactly", 0, "count"), metricFloor("production_mounts", "exactly", 0, "count")),
	"signed-authenticated-four-root-restore": requiredTarget("ha_dr", 2, 2,
		metricFloor("root_count", "at_least", 4, "count"), metricFloor("approved_restore_authentications_percent", "exactly", 100, "percent"), metricFloor("invalid_or_unapproved_restores_accepted", "exactly", 0, "count"), metricFloor("snapshot_age_before_mutation_seconds", "at_most", 900, "seconds"), metricFloor("rto_seconds", "at_most", 3600, "seconds"), metricFloor("restore_attempts_over_3600_seconds", "exactly", 0, "count"), metricFloor("canonical_database_manifest_parity_percent", "exactly", 100, "percent"), metricFloor("canonical_files_blobs_manifest_parity_percent", "exactly", 100, "percent"), metricFloor("workflow_manifest_parity_percent", "exactly", 100, "percent"), metricFloor("purge_manifest_parity_percent", "exactly", 100, "percent"), metricFloor("events_lost_after_snapshot_watermark", "exactly", 0, "count"), metricFloor("integrity_failures", "exactly", 0, "count"), metricFloor("purge_continuity_percent", "exactly", 100, "percent")),
	"canonical-data-rpo": requiredTarget("ha_dr", 3, 3,
		metricFloor("rpo_seconds", "at_most", 300, "seconds"), metricFloor("rpo_observations_over_300_seconds", "exactly", 0, "count"), metricFloor("missing_canonical_events", "exactly", 0, "count")),
	"control-data-rpo": requiredTarget("ha_dr", 3, 3,
		metricFloor("rpo_seconds", "at_most", 300, "seconds"), metricFloor("rpo_observations_over_300_seconds", "exactly", 0, "count"), metricFloor("missing_control_records", "exactly", 0, "count")),
	"live-app-control-failover": requiredTarget("ha_dr", 3, 3,
		metricFloor("successful_failovers_percent", "exactly", 100, "percent"), metricFloor("failover_seconds", "at_most", 60, "seconds"), metricFloor("failovers_over_60_seconds", "exactly", 0, "count"), metricFloor("control_plane_integrity_failures", "exactly", 0, "count")),
	"turn-failover-and-session-drain": requiredTarget("ha_dr", 3, 10,
		metricFloor("new_session_failover_percent", "exactly", 100, "percent"), metricFloor("existing_session_drain_percent", "exactly", 100, "percent"), metricFloor("media_interruption_p95_seconds", "at_most", 2, "seconds"), metricFloor("media_interruptions_over_two_seconds", "exactly", 0, "count"), metricFloor("cross_tenant_media_leaks", "exactly", 0, "count")),
	"purge-continuity": requiredTarget("ha_dr", 3, 10,
		metricFloor("purged_objects_resurrected", "exactly", 0, "count"), metricFloor("purge_authority_continuity_percent", "exactly", 100, "percent")),
	"retained-release-rollback": requiredTarget("ha_dr", 2, 2,
		metricFloor("rollback_success_percent", "exactly", 100, "percent"), metricFloor("purge_authority_regressions", "exactly", 0, "count")),
	"twenty-four-hour-ten-sitting-soak": requiredTarget("ha_dr", 1, 10,
		metricFloor("duration_seconds", "at_least", 86400, "seconds"), metricFloor("sitting_count", "at_least", 10, "count"), metricFloor("fatal_failures", "exactly", 0, "count"), metricFloor("safety_gate_failures", "exactly", 0, "count"), metricFloor("cross_tenant_leaks", "exactly", 0, "count")),
	"ephemeral-worker-per-run": requiredTarget("worker_orchestrator", 3, 10,
		metricFloor("ephemeral_run_percent", "exactly", 100, "percent"), metricFloor("cross_run_state_leaks", "exactly", 0, "count")),
	"default-deny-egress-enforcement": requiredTarget("worker_orchestrator", 3, 20,
		metricFloor("approved_egress_success_percent", "exactly", 100, "percent"), metricFloor("blocked_unapproved_egress_percent", "exactly", 100, "percent"), metricFloor("successful_unapproved_egress", "exactly", 0, "count")),
	"short-lived-run-bound-credentials": requiredTarget("worker_orchestrator", 3, 20,
		metricFloor("expired_or_cross_run_credentials_accepted", "exactly", 0, "count"), metricFloor("run_bound_credentials_percent", "exactly", 100, "percent")),
	"worker-resource-and-time-caps": requiredTarget("worker_orchestrator", 3, 20,
		metricFloor("cap_enforcement_percent", "exactly", 100, "percent"), metricFloor("uncapped_runs", "exactly", 0, "count")),
	"signed-callback-and-replay-fence": requiredTarget("worker_orchestrator", 3, 20,
		metricFloor("valid_signed_callbacks_accepted_percent", "exactly", 100, "percent"), metricFloor("invalid_callbacks_accepted", "exactly", 0, "count"), metricFloor("replayed_callbacks_accepted", "exactly", 0, "count")),
	"no-production-or-company-brain-mount": requiredTarget("worker_orchestrator", 3, 20,
		metricFloor("blocked_mount_attempts", "at_least", 20, "count"), metricFloor("accessible_forbidden_mounts", "exactly", 0, "count")),
	"crash-restart-idempotency": requiredTarget("worker_orchestrator", 3, 20,
		metricFloor("recovered_runs_percent", "exactly", 100, "percent"), metricFloor("duplicate_side_effects", "exactly", 0, "count")),
	"external-write-and-agent-loop-gates": requiredTarget("worker_orchestrator", 3, 20,
		metricFloor("unauthorized_external_writes", "exactly", 0, "count"), metricFloor("autonomous_agent_loops", "exactly", 0, "count")),
}

func ValidateTargetRegistry(registry TargetRegistry) error {
	if registry.SchemaVersion != TargetRegistrySchema || !validID(registry.RegistryID) {
		return fmt.Errorf("schemaVersion must be %s and registryId must be stable", TargetRegistrySchema)
	}
	if !validID(registry.Signer.SignerKeyID) || !validID(registry.Signer.SignerIdentityID) || !validSHA(registry.Signer.SignerPublicKeyFingerprintSHA256) {
		return errors.New("registry signer binding is invalid")
	}
	if err := validateCandidate(registry.Candidate); err != nil {
		return err
	}
	if len(registry.Targets) == 0 {
		return errors.New("target registry is empty")
	}
	seen := map[string]bool{}
	for _, target := range registry.Targets {
		if !validID(target.ID) || !oneOf(target.Category, "physical_device_webrtc", "ha_dr", "worker_orchestrator", "qualification_evaluator") {
			return fmt.Errorf("target %q has invalid identity/category", target.ID)
		}
		if seen[target.ID] {
			return fmt.Errorf("duplicate target %s", target.ID)
		}
		seen[target.ID] = true
		if !validSHA(target.MeasurementRevisionSHA256) {
			return fmt.Errorf("target %s lacks an immutable measurement-code SHA-256", target.ID)
		}
		if !validSHA(target.FixtureSHA256) || !validID(target.Environment) || target.MinimumArtifacts < 1 || target.MinimumSampleSize < 1 || !validID(target.OwnerID) || !validID(target.IndependentReviewerID) || target.OwnerID == target.IndependentReviewerID || !validID(target.RollbackTrigger) || !target.PhysicalOrProduction {
			return fmt.Errorf("target %s lacks fixture, environment, sample, revision, owner/reviewer, rollback, or physical binding", target.ID)
		}
		if floor, required := requiredTargetContracts[target.ID]; required && (target.Category != floor.Category || target.MinimumArtifacts < floor.MinimumArtifacts || target.MinimumSampleSize < floor.MinimumSampleSize) {
			return fmt.Errorf("target %s weakens required category/artifact/sample contract", target.ID)
		}
		if err := validateThresholdSet(target); err != nil {
			return err
		}
	}
	for id := range requiredTargetContracts {
		if !seen[id] {
			return fmt.Errorf("target registry omits required target %s", id)
		}
	}
	return nil
}

func validateThresholdSet(target EvidenceTarget) error {
	if len(target.RequiredMetrics) < 2 {
		return fmt.Errorf("target %s requires an explicit multi-metric threshold set", target.ID)
	}
	seen := map[string]bool{}
	byName := map[string]MetricThreshold{}
	for _, metric := range target.RequiredMetrics {
		if !validID(metric.Name) || seen[metric.Name] || !oneOf(metric.Comparator, "at_least", "at_most", "exactly") || metric.Value < 0 || math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) || strings.TrimSpace(metric.Unit) == "" {
			return fmt.Errorf("target %s has an invalid or duplicate metric threshold", target.ID)
		}
		seen[metric.Name] = true
		byName[metric.Name] = metric
	}
	for _, floor := range requiredTargetContracts[target.ID].Metrics {
		metric, ok := byName[floor.Name]
		weakened := !ok || metric.Comparator != floor.Comparator || metric.Unit != floor.Unit ||
			floor.Comparator == "at_least" && metric.Value < floor.Value ||
			floor.Comparator == "at_most" && metric.Value > floor.Value ||
			floor.Comparator == "exactly" && metric.Value != floor.Value
		if weakened {
			return fmt.Errorf("target %s weakens required metric contract %s", target.ID, floor.Name)
		}
	}
	return nil
}

func VerifyTargetRegistry(raw, signature, publicKey []byte, approved ApprovedTrustRoots) (TargetRegistry, error) {
	registry, err := DecodeCanonicalStrict[TargetRegistry](raw)
	if err != nil {
		return TargetRegistry{}, fmt.Errorf("target registry: %w", err)
	}
	if err := validateApprovedTrustRoots(approved); err != nil {
		return TargetRegistry{}, err
	}
	if RegistryDigest(raw) != approved.roots.PreMeasurementTargetRegistrySHA256 {
		return TargetRegistry{}, errors.New("target registry bytes do not match the separately anchored pre-measurement registry SHA-256")
	}
	if err := ValidateTargetRegistry(registry); err != nil {
		return TargetRegistry{}, err
	}
	if err := verifyApprovedSignature(raw, signature, publicKey, approved.roots, registry.Signer.SignerKeyID, registry.Signer.SignerIdentityID, "registry_owner", registry.Signer.SignerPublicKeyFingerprintSHA256); err != nil {
		return TargetRegistry{}, fmt.Errorf("target registry: %w", err)
	}
	return registry, nil
}

func VerifyDualSignatures(raw, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey []byte, approval DualApprovalBinding, approved ApprovedTrustRoots) error {
	if err := validateApprovedTrustRoots(approved); err != nil {
		return err
	}
	if approval.OperatorID == approval.ReviewerID || approval.OperatorKeyID == approval.ReviewerKeyID || approval.OperatorPublicKeyFingerprintSHA256 == approval.ReviewerPublicKeyFingerprintSHA256 {
		return errors.New("operator and independent reviewer must have distinct identities and keys")
	}
	if err := verifyApprovedSignature(raw, operatorSignature, operatorPublicKey, approved.roots, approval.OperatorKeyID, approval.OperatorID, "operator", approval.OperatorPublicKeyFingerprintSHA256); err != nil {
		return fmt.Errorf("operator signature: %w", err)
	}
	if err := verifyApprovedSignature(raw, reviewerSignature, reviewerPublicKey, approved.roots, approval.ReviewerKeyID, approval.ReviewerID, "independent_reviewer", approval.ReviewerPublicKeyFingerprintSHA256); err != nil {
		return fmt.Errorf("independent-review signature: %w", err)
	}
	return nil
}

func verifyApprovedSignature(raw, signature, publicKey []byte, roots TrustRoots, keyID, identityID, role, claimedFingerprint string) error {
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return errors.New("Ed25519 key or signature length is invalid")
	}
	fingerprint := PublicKeyFingerprint(publicKey)
	if claimedFingerprint != fingerprint {
		return errors.New("packet signer fingerprint does not match supplied public key")
	}
	approved := false
	for _, signer := range roots.ApprovedSigners {
		if signer.KeyID == keyID && signer.IdentityID == identityID && signer.Role == role && signer.PublicKeyFingerprintSHA256 == fingerprint {
			approved = true
			break
		}
	}
	if !approved {
		return errors.New("signer is not anchored in the approved trust roots")
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), raw, signature) {
		return errors.New("Ed25519 signature is invalid")
	}
	return nil
}

func PublicKeyFingerprint(publicKey []byte) string { return digestBytes(publicKey) }

func (approved ApprovedTrustRoots) Digest() string { return approved.rawDigest }

func ValidateCorpus(manifest CorpusManifest, registry TargetRegistry, registrySHA256 string) error {
	if manifest.SchemaVersion != CorpusManifestSchema || !validID(manifest.TenantID) || !validID(manifest.CorpusID) || !validID(manifest.TargetID) {
		return errors.New("corpus schema or identity is invalid")
	}
	if !oneOf(manifest.Lane, "meeting_stt", "composer_dictation", "meeting_specialist") || manifest.EvidenceClass != "authorized_real_capture" {
		return errors.New("corpus lane/evidenceClass is invalid")
	}
	if err := validatePacketBinding(manifest.Candidate, manifest.Approval, registry, registrySHA256); err != nil {
		return err
	}
	target, ok := registryTarget(registry, manifest.TargetID)
	if !ok || target.Category != "qualification_evaluator" || target.ID != qualificationTargetForLane(manifest.Lane) || manifest.FixtureSHA256 != target.FixtureSHA256 || manifest.QualificationSubjectSHA256 != target.FixtureSHA256 || target.OwnerID != manifest.Approval.OperatorID || target.IndependentReviewerID != manifest.Approval.ReviewerID {
		return errors.New("corpus is not bound to its preregistered qualification target and accountable signers")
	}
	if manifest.Lane == "meeting_specialist" {
		if validateMeetingSpecialistQualificationBinding(manifest.MeetingSpecialistBinding) != nil || manifest.FixtureSHA256 != MeetingSpecialistQualificationFixtureDigest(manifest.MeetingSpecialistBinding) || manifest.Candidate.RouteMapDigest != manifest.MeetingSpecialistBinding.RouteDigest {
			return errors.New("meeting specialist corpus is not bound to its preregistered provider, model, route, voice, accounting, runtime, and capability profile")
		}
	} else if manifest.MeetingSpecialistBinding != (MeetingSpecialistQualificationBinding{}) {
		return errors.New("non-specialist corpus contains a meeting specialist qualification binding")
	}
	minimum := 120
	switch manifest.Lane {
	case "composer_dictation":
		minimum = 250
	case "meeting_specialist":
		minimum = 10
	}
	if target.MinimumSampleSize > minimum {
		minimum = target.MinimumSampleSize
	}
	if len(manifest.Clips) < minimum {
		return fmt.Errorf("%s corpus requires at least %d clips", manifest.Lane, minimum)
	}
	seenIDs := map[string]bool{}
	seenOrders := map[int64]bool{}
	uniqueEvidence := map[string]string{}
	coverage := map[string]bool{}
	var duration int64
	for _, clip := range manifest.Clips {
		if !validID(clip.ClipID) || seenIDs[clip.ClipID] {
			return fmt.Errorf("clip identity %q is invalid or duplicated", clip.ClipID)
		}
		seenIDs[clip.ClipID] = true
		digests := map[string]string{"audio": clip.AudioSHA256, "reference": clip.ReferenceSHA256, "consent": clip.ConsentReceiptSHA256, "speaker": clip.SpeakerEvidenceSHA256, "track": clip.TrackEvidenceSHA256}
		for kind, digest := range digests {
			if _, exists := uniqueEvidence[digest]; !validSHA(digest) || exists {
				return fmt.Errorf("clip %s has invalid or duplicate %s evidence", clip.ClipID, kind)
			}
			uniqueEvidence[digest] = kind
		}
		if !validSHA(clip.SpeakerIDHash) || !validID(clip.TrackID) || clip.DurationMillis <= 0 || clip.DurationMillis > 30*60*1000 || clip.SourceOrder < 0 {
			return fmt.Errorf("clip %s timing/speaker/track binding is invalid", clip.ClipID)
		}
		if clip.Synthetic {
			return fmt.Errorf("clip %s is synthetic and cannot enter a qualification corpus", clip.ClipID)
		}
		if seenOrders[clip.SourceOrder] {
			return fmt.Errorf("sourceOrder %d is duplicated", clip.SourceOrder)
		}
		seenOrders[clip.SourceOrder] = true
		duration += clip.DurationMillis
		if manifest.Lane != "composer_dictation" {
			if clip.ComposerSurface != "" || clip.TargetDevice {
				return fmt.Errorf("meeting capture %s has dictation-only fields", clip.ClipID)
			}
		} else {
			if clip.DurationMillis > 30_000 || !clip.TargetDevice || !oneOf(clip.Platform, "web", "iphone", "ipad") || !oneOf(clip.ComposerSurface, "scout", "private_thread", "team", "project", "in_room") {
				return fmt.Errorf("dictation clip %s exceeds 30 seconds or lacks a supported physical platform/composer binding", clip.ClipID)
			}
			coverage[clip.Platform+"/"+clip.ComposerSurface] = true
		}
	}
	if manifest.Lane == "meeting_stt" && duration < 60*60*1000 {
		return errors.New("meeting_stt corpus requires at least 60 minutes")
	}
	if manifest.Lane == "composer_dictation" {
		for _, platform := range []string{"web", "iphone", "ipad"} {
			for _, surface := range []string{"scout", "private_thread", "team", "project", "in_room"} {
				if !coverage[platform+"/"+surface] {
					return fmt.Errorf("composer_dictation corpus is missing physical %s/%s coverage", platform, surface)
				}
			}
		}
	}
	expected := corpusArtifactSetDigest(manifest.Clips)
	if manifest.Approval.SourceArtifactSetSHA256 != expected {
		return errors.New("corpus source artifact-set digest is invalid")
	}
	return nil
}

func ValidatePilotPacket(packet PilotPacket, registry TargetRegistry, registrySHA256 string) error {
	if packet.SchemaVersion != PilotPacketSchema || !validID(packet.TenantID) || !validID(packet.PacketID) || !validID(packet.TargetID) || packet.EvidenceClass != "authorized_real_input_human_review" {
		return errors.New("I&O pilot packet schema, identity, or evidenceClass is invalid")
	}
	if err := validatePacketBinding(packet.Candidate, packet.Approval, registry, registrySHA256); err != nil {
		return err
	}
	target, ok := registryTarget(registry, packet.TargetID)
	if !ok || target.Category != "qualification_evaluator" || target.ID != qualificationTargetForLane("insights_opportunities") || packet.FixtureSHA256 != target.FixtureSHA256 || packet.QualificationSubjectSHA256 != target.FixtureSHA256 || target.OwnerID != packet.Approval.OperatorID || target.IndependentReviewerID != packet.Approval.ReviewerID {
		return errors.New("I&O packet is not bound to its preregistered qualification target and accountable signers")
	}
	if len(packet.Pilots) != 10 {
		return errors.New("I&O packet requires exactly ten pilots")
	}
	seen := map[string]bool{}
	artifacts := map[string]bool{}
	eligibleReviewers := map[string]EligiblePilotReviewer{}
	for _, reviewer := range packet.ReviewerRoster {
		if !validID(reviewer.ReviewerID) || eligibleReviewers[reviewer.ReviewerID].ReviewerID != "" || !validID(reviewer.ReviewerKeyID) || !validSHA(reviewer.ReviewerPublicKeyFingerprintSHA256) || reviewer.ReviewerID == packet.Approval.OperatorID || !claimUniqueDigest(artifacts, reviewer.EligibilityReceiptDigest) {
			return fmt.Errorf("I&O reviewer %q lacks unique signed-roster eligibility evidence", reviewer.ReviewerID)
		}
		eligibleReviewers[reviewer.ReviewerID] = reviewer
	}
	if len(eligibleReviewers) < 2 {
		return errors.New("I&O packet requires an exact signed roster with at least two eligible human reviewers")
	}
	actualReviewers := map[string]bool{}
	accepted := 0
	for _, pilot := range packet.Pilots {
		if !validID(pilot.PilotID) || seen[pilot.PilotID] {
			return fmt.Errorf("pilot identity %q is invalid or duplicated", pilot.PilotID)
		}
		seen[pilot.PilotID] = true
		for _, digest := range []string{pilot.InputDigest, pilot.RunReceiptDigest, pilot.ArtifactDigest, pilot.DispositionReasonDigest, pilot.TerminalVisibilityReceiptDigest, pilot.ExternalEffectAuditReceiptDigest} {
			if !claimUniqueDigest(artifacts, digest) {
				return fmt.Errorf("pilot %s has invalid or duplicate source evidence", pilot.PilotID)
			}
		}
		if !oneOf(pilot.Disposition, "accepted", "rejected", "blocked", "failed") || pilot.RevisionCount < 0 || pilot.RevisionCount > 2 ||
			pilot.AssertedClaimCount < 1 || pilot.SourcedAssertedClaimCount != pilot.AssertedClaimCount || pilot.InventedAssertedClaimCount != 0 ||
			pilot.UnauthorizedDisclosureCount != 0 || pilot.ExternalWriteCount != 0 {
			return fmt.Errorf("pilot %s fails terminal disposition, revision, sourcing, invention, disclosure, or external-write controls", pilot.PilotID)
		}
		if len(pilot.Reviews) < 2 {
			return fmt.Errorf("pilot %s lacks two eligible human review decisions", pilot.PilotID)
		}
		pilotReviewers := map[string]bool{}
		for _, review := range pilot.Reviews {
			roster, rostered := eligibleReviewers[review.ReviewerID]
			publicKey, publicKeyErr := decodeCanonicalHex(review.ReviewerPublicKeyHex, ed25519.PublicKeySize)
			_, signatureErr := decodeCanonicalHex(review.SignatureHex, ed25519.SignatureSize)
			if !rostered || roster.ReviewerKeyID != review.ReviewerKeyID || roster.ReviewerPublicKeyFingerprintSHA256 != digestBytes(publicKey) || publicKeyErr != nil || signatureErr != nil || pilotReviewers[review.ReviewerID] || review.Disposition != pilot.Disposition || !claimUniqueDigest(artifacts, review.ReviewReceiptDigest) {
				return fmt.Errorf("pilot %s has an ineligible, duplicate, contradictory, or unbound review", pilot.PilotID)
			}
			pilotReviewers[review.ReviewerID] = true
			actualReviewers[review.ReviewerID] = true
		}
		if pilot.Disposition == "accepted" {
			accepted++
		}
	}
	if accepted < 8 || len(actualReviewers) < 2 {
		return errors.New("I&O packet requires at least eight accepted pilots and two actually participating eligible reviewers")
	}
	if packet.Approval.SourceArtifactSetSHA256 != pilotArtifactSetDigest(packet.ReviewerRoster, packet.Pilots) {
		return errors.New("I&O source artifact-set digest is invalid")
	}
	return nil
}

func qualificationTargetForLane(lane string) string {
	switch lane {
	case "meeting_stt":
		return "meeting-stt-live-provider-evaluation"
	case "composer_dictation":
		return "composer-dictation-target-device-evaluation"
	case "insights_opportunities":
		return "insights-opportunities-real-input-pilots"
	case "meeting_specialist":
		return "meeting-specialist-provider-voice-evaluation"
	default:
		return ""
	}
}

func validateMeetingSpecialistQualificationBinding(binding MeetingSpecialistQualificationBinding) error {
	if !validID(binding.Provider) || strings.TrimSpace(binding.Model) == "" || strings.TrimSpace(binding.Model) != binding.Model || strings.TrimSpace(binding.Voice) == "" || strings.TrimSpace(binding.Voice) != binding.Voice || !validSHA(binding.RouteDigest) || !validSHA(binding.AccountingProfileDigest) || !validSHA(binding.RuntimeProfileDigest) || !validSHA(binding.CapabilityPolicyDigest) {
		return errors.New("meeting specialist qualification binding is invalid")
	}
	return nil
}

func MeetingSpecialistQualificationFixtureDigest(binding MeetingSpecialistQualificationBinding) string {
	raw, _ := json.Marshal(binding)
	return digestBytes(append([]byte("stride.e10.meeting-specialist-qualification-fixture/v1\x00"), raw...))
}

func registryTarget(registry TargetRegistry, targetID string) (EvidenceTarget, bool) {
	for _, target := range registry.Targets {
		if target.ID == targetID {
			return target, true
		}
	}
	return EvidenceTarget{}, false
}

func verifyPilotReviewSignatures(packet PilotPacket, approved ApprovedTrustRoots) error {
	if err := validateApprovedTrustRoots(approved); err != nil {
		return err
	}
	roster := make(map[string]EligiblePilotReviewer, len(packet.ReviewerRoster))
	for _, reviewer := range packet.ReviewerRoster {
		roster[reviewer.ReviewerID] = reviewer
	}
	for _, pilot := range packet.Pilots {
		for _, review := range pilot.Reviews {
			reviewer, ok := roster[review.ReviewerID]
			if !ok || reviewer.ReviewerKeyID != review.ReviewerKeyID {
				return fmt.Errorf("pilot %s review is not bound to the signed reviewer roster", pilot.PilotID)
			}
			publicKey, publicKeyErr := decodeCanonicalHex(review.ReviewerPublicKeyHex, ed25519.PublicKeySize)
			signature, signatureErr := decodeCanonicalHex(review.SignatureHex, ed25519.SignatureSize)
			if publicKeyErr != nil || signatureErr != nil {
				return fmt.Errorf("pilot %s review has invalid canonical key/signature encoding", pilot.PilotID)
			}
			payload, err := pilotReviewSigningPayload(packet, pilot, reviewer, review)
			if err != nil {
				return err
			}
			if err := verifyApprovedSignature(payload, signature, publicKey, approved.roots, reviewer.ReviewerKeyID, reviewer.ReviewerID, "pilot_reviewer", reviewer.ReviewerPublicKeyFingerprintSHA256); err != nil {
				return fmt.Errorf("pilot %s review signature: %w", pilot.PilotID, err)
			}
		}
	}
	return nil
}

func pilotReviewSigningPayload(packet PilotPacket, pilot IOPilot, reviewer EligiblePilotReviewer, review PilotReviewDecision) ([]byte, error) {
	type payload struct {
		Purpose                          string           `json:"purpose"`
		SchemaVersion                    string           `json:"schemaVersion"`
		TenantID                         string           `json:"tenantId"`
		PacketID                         string           `json:"packetId"`
		Candidate                        CandidateBinding `json:"candidate"`
		PilotID                          string           `json:"pilotId"`
		InputDigest                      string           `json:"inputDigest"`
		RunReceiptDigest                 string           `json:"runReceiptDigest"`
		ArtifactDigest                   string           `json:"artifactDigest"`
		Disposition                      string           `json:"disposition"`
		DispositionReasonDigest          string           `json:"dispositionReasonDigest"`
		TerminalVisibilityReceiptDigest  string           `json:"terminalVisibilityReceiptDigest"`
		RevisionCount                    int              `json:"revisionCount"`
		AssertedClaimCount               int              `json:"assertedClaimCount"`
		SourcedAssertedClaimCount        int              `json:"sourcedAssertedClaimCount"`
		InventedAssertedClaimCount       int              `json:"inventedAssertedClaimCount"`
		UnauthorizedDisclosureCount      int              `json:"unauthorizedDisclosureCount"`
		ExternalWriteCount               int              `json:"externalWriteCount"`
		ExternalEffectAuditReceiptDigest string           `json:"externalEffectAuditReceiptDigest"`
		ReviewerID                       string           `json:"reviewerId"`
		ReviewerKeyID                    string           `json:"reviewerKeyId"`
		ReviewerFingerprint              string           `json:"reviewerPublicKeyFingerprintSha256"`
		EligibilityReceiptDigest         string           `json:"eligibilityReceiptDigest"`
		ReviewReceiptDigest              string           `json:"reviewReceiptDigest"`
		ReviewDisposition                string           `json:"reviewDisposition"`
	}
	return json.Marshal(payload{
		Purpose: "stride.e10.io-pilot-review/v1", SchemaVersion: packet.SchemaVersion, TenantID: packet.TenantID, PacketID: packet.PacketID, Candidate: packet.Candidate,
		PilotID: pilot.PilotID, InputDigest: pilot.InputDigest, RunReceiptDigest: pilot.RunReceiptDigest, ArtifactDigest: pilot.ArtifactDigest,
		Disposition: pilot.Disposition, DispositionReasonDigest: pilot.DispositionReasonDigest, TerminalVisibilityReceiptDigest: pilot.TerminalVisibilityReceiptDigest,
		RevisionCount: pilot.RevisionCount, AssertedClaimCount: pilot.AssertedClaimCount, SourcedAssertedClaimCount: pilot.SourcedAssertedClaimCount,
		InventedAssertedClaimCount: pilot.InventedAssertedClaimCount, UnauthorizedDisclosureCount: pilot.UnauthorizedDisclosureCount, ExternalWriteCount: pilot.ExternalWriteCount,
		ExternalEffectAuditReceiptDigest: pilot.ExternalEffectAuditReceiptDigest,
		ReviewerID:                       reviewer.ReviewerID, ReviewerKeyID: reviewer.ReviewerKeyID, ReviewerFingerprint: reviewer.ReviewerPublicKeyFingerprintSHA256,
		EligibilityReceiptDigest: reviewer.EligibilityReceiptDigest, ReviewReceiptDigest: review.ReviewReceiptDigest, ReviewDisposition: review.Disposition,
	})
}

func ValidateExternalMatrix(matrix ExternalMatrix, registry TargetRegistry, registrySHA256 string) error {
	if matrix.SchemaVersion != ExternalMatrixSchema || !validID(matrix.MatrixID) || !oneOf(matrix.Category, "physical_device_webrtc", "ha_dr", "worker_orchestrator") || matrix.EvidenceClass != "external_observation_independently_reviewed" {
		return errors.New("external matrix schema, identity, category, or evidenceClass is invalid")
	}
	if err := validatePacketBinding(matrix.Candidate, matrix.Approval, registry, registrySHA256); err != nil {
		return err
	}
	targets := map[string]EvidenceTarget{}
	for _, target := range registry.Targets {
		if target.Category == matrix.Category {
			targets[target.ID] = target
		}
	}
	if len(targets) == 0 {
		return errors.New("signed registry has no targets for matrix category")
	}
	counts := map[string]int{}
	artifacts := map[string]bool{}
	for _, observation := range matrix.Observations {
		target, ok := targets[observation.TargetID]
		if !ok {
			return fmt.Errorf("observation references unknown or wrong-category target %s", observation.TargetID)
		}
		if !validSHA(observation.ArtifactSHA256) || artifacts[observation.ArtifactSHA256] {
			return fmt.Errorf("observation for %s has invalid or duplicate artifact", observation.TargetID)
		}
		artifacts[observation.ArtifactSHA256] = true
		if target.OwnerID != matrix.Approval.OperatorID || target.IndependentReviewerID != matrix.Approval.ReviewerID {
			return fmt.Errorf("observation for %s is not signed by its registered owner/reviewer", observation.TargetID)
		}
		if observation.FixtureSHA256 != target.FixtureSHA256 || observation.Environment != target.Environment || observation.SampleSize < target.MinimumSampleSize || observation.MeasurementRevisionSHA256 != target.MeasurementRevisionSHA256 {
			return fmt.Errorf("observation for %s drifts from fixture/environment/sample/revision contract", observation.TargetID)
		}
		when, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
		if err != nil || when.After(time.Now().Add(5*time.Minute)) {
			return fmt.Errorf("observation for %s has invalid time", observation.TargetID)
		}
		if observation.Verdict != "pass" {
			return fmt.Errorf("observation for %s did not pass", observation.TargetID)
		}
		if len(observation.Metrics) != len(target.RequiredMetrics) {
			return fmt.Errorf("observation for %s does not contain the exact preregistered metric set", observation.TargetID)
		}
		for _, threshold := range target.RequiredMetrics {
			metric, ok := observation.Metrics[threshold.Name]
			if !ok {
				return fmt.Errorf("observation for %s lacks required metric %s", observation.TargetID, threshold.Name)
			}
			if err := validateObservedMetric(threshold, metric, observation.SampleSize); err != nil {
				return fmt.Errorf("observation for %s fails required metric %s", observation.TargetID, threshold.Name)
			}
		}
		counts[observation.TargetID]++
	}
	for id, target := range targets {
		if counts[id] < target.MinimumArtifacts {
			return fmt.Errorf("target %s requires at least %d artifacts", id, target.MinimumArtifacts)
		}
	}
	if matrix.Approval.SourceArtifactSetSHA256 != externalArtifactSetDigest(matrix.Observations) {
		return errors.New("external source artifact-set digest is invalid")
	}
	return nil
}

func validateObservedMetric(threshold MetricThreshold, metric Metric, sampleSize int) error {
	if metric.Unit != threshold.Unit || metric.Value < 0 || math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) || sampleSize < 1 {
		return errors.New("metric value/unit/sample is invalid")
	}
	conservative := metric.Value
	switch {
	case isRateThreshold(threshold):
		if metric.Numerator == nil || metric.Denominator == nil || *metric.Denominator < sampleSize || *metric.Numerator < 0 || *metric.Numerator > *metric.Denominator || metric.Interval95 == nil || metric.Interval95.Method != "wilson_95" || metric.P50 != nil || metric.P95 != nil || metric.P99 != nil || len(metric.Samples) != 0 {
			return errors.New("rate metric lacks numerator/denominator and Wilson interval")
		}
		point := 100 * float64(*metric.Numerator) / float64(*metric.Denominator)
		low, high := wilsonInterval95(*metric.Numerator, *metric.Denominator)
		if !nearlyEqual(metric.Value, point) || !nearlyEqual(metric.Interval95.Low, low) || !nearlyEqual(metric.Interval95.High, high) {
			return errors.New("rate metric values do not recompute")
		}
		if threshold.Comparator == "at_least" {
			conservative = low
		} else if threshold.Comparator == "at_most" {
			conservative = high
		}
	case isLatencyThreshold(threshold):
		if metric.Numerator != nil || metric.Denominator != nil || metric.P50 == nil || metric.P95 == nil || metric.P99 == nil || metric.Interval95 == nil || metric.Interval95.Method != "deterministic_bootstrap_95" || len(metric.Samples) != sampleSize {
			return errors.New("latency metric lacks exact samples, quantiles, or bootstrap interval")
		}
		values := append([]float64(nil), metric.Samples...)
		for _, value := range values {
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return errors.New("latency sample is invalid")
			}
		}
		sort.Float64s(values)
		p50, p95, p99 := percentileFloat(values, .50), percentileFloat(values, .95), percentileFloat(values, .99)
		quantile := .95
		selected := p95
		if strings.Contains(threshold.Name, "p50") {
			quantile, selected = .50, p50
		} else if strings.Contains(threshold.Name, "p99") {
			quantile, selected = .99, p99
		}
		low, high := deterministicBootstrapQuantile95(metric.Samples, quantile, threshold.Name)
		if !nearlyEqual(*metric.P50, p50) || !nearlyEqual(*metric.P95, p95) || !nearlyEqual(*metric.P99, p99) || !nearlyEqual(metric.Value, selected) || !nearlyEqual(metric.Interval95.Low, low) || !nearlyEqual(metric.Interval95.High, high) {
			return errors.New("latency metric values do not recompute")
		}
		if threshold.Comparator == "at_least" {
			conservative = low
		} else if threshold.Comparator == "at_most" {
			conservative = high
		}
	default:
		if metric.Numerator != nil || metric.Denominator != nil || metric.P50 != nil || metric.P95 != nil || metric.P99 != nil || metric.Interval95 != nil || len(metric.Samples) != 0 {
			return errors.New("scalar metric carries unverified statistical fields")
		}
	}
	pointPass := compareMetric(metric.Value, threshold.Comparator, threshold.Value)
	if !pointPass {
		return errors.New("point estimate misses threshold")
	}
	// A preregistered exactly-100% rate is a zero-failure gate. Its Wilson
	// interval is still reported and recomputed, but a finite interval cannot
	// have a lower bound of exactly 100%. Other rate/latency comparisons use the
	// conservative interval boundary.
	if isRateThreshold(threshold) && threshold.Comparator == "exactly" {
		if threshold.Value == 100 && *metric.Numerator != *metric.Denominator || threshold.Value == 0 && *metric.Numerator != 0 {
			return errors.New("exact rate has failures")
		}
		return nil
	}
	if (isRateThreshold(threshold) || isLatencyThreshold(threshold)) && !compareMetric(conservative, threshold.Comparator, threshold.Value) {
		return errors.New("confidence interval misses threshold")
	}
	return nil
}

func isRateThreshold(threshold MetricThreshold) bool {
	return threshold.Unit == "percent" && !strings.Contains(threshold.Name, "drift")
}

func compareMetric(value float64, comparator string, threshold float64) bool {
	switch comparator {
	case "at_least":
		return value >= threshold
	case "at_most":
		return value <= threshold
	case "exactly":
		return nearlyEqual(value, threshold)
	default:
		return false
	}
}

func isLatencyThreshold(threshold MetricThreshold) bool {
	if threshold.Unit != "seconds" && threshold.Unit != "milliseconds" {
		return false
	}
	name := threshold.Name
	for _, marker := range []string{"latency", "recovery", "cleanup", "failover", "interruption", "rto", "rpo", "p50", "p95", "p99"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func wilsonInterval95(numerator, denominator int) (float64, float64) {
	if denominator <= 0 {
		return math.NaN(), math.NaN()
	}
	z := 1.959963984540054
	n := float64(denominator)
	p := float64(numerator) / n
	denom := 1 + z*z/n
	center := (p + z*z/(2*n)) / denom
	margin := z * math.Sqrt((p*(1-p)+z*z/(4*n))/n) / denom
	return 100 * math.Max(0, center-margin), 100 * math.Min(1, center+margin)
}

func percentileFloat(sorted []float64, quantile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func deterministicBootstrapQuantile95(samples []float64, quantile float64, label string) (float64, float64) {
	seedBytes := sha256.Sum256([]byte(label + ":" + fmt.Sprint(samples)))
	seed := uint64(0)
	for index := 0; index < 8; index++ {
		seed = seed<<8 | uint64(seedBytes[index])
	}
	if seed == 0 {
		seed = 1
	}
	next := func() uint64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return seed
	}
	estimates := make([]float64, 400)
	resample := make([]float64, len(samples))
	for iteration := range estimates {
		for index := range resample {
			resample[index] = samples[int(next()%uint64(len(samples)))]
		}
		sort.Float64s(resample)
		estimates[iteration] = percentileFloat(resample, quantile)
	}
	sort.Float64s(estimates)
	return percentileFloat(estimates, .025), percentileFloat(estimates, .975)
}

func nearlyEqual(left, right float64) bool {
	return math.Abs(left-right) <= 1e-9*math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
}

func ValidatePacketSignatures(raw []byte, approval DualApprovalBinding, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey []byte, approved ApprovedTrustRoots) error {
	return VerifyDualSignatures(raw, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, approval, approved)
}

func validateApprovedTrustRoots(approved ApprovedTrustRoots) error {
	if !validSHA(approved.rawDigest) {
		return errors.New("approved trust-root anchor is absent")
	}
	return ValidateTrustRoots(approved.roots)
}

func CorpusSourceArtifactSetDigest(clips []CorpusClip) string { return corpusArtifactSetDigest(clips) }
func PilotSourceArtifactSetDigest(packet PilotPacket) string {
	return pilotArtifactSetDigest(packet.ReviewerRoster, packet.Pilots)
}
func ExternalSourceArtifactSetDigest(observations []ExternalObservation) string {
	return externalArtifactSetDigest(observations)
}

// VerifyTargetRegistryReceipt is the only registry-receipt minting path. It
// verifies canonical bytes, the approved trust anchor, the registry contract,
// and the detached registry-owner signature before returning an opaque proof.
func VerifyTargetRegistryReceipt(raw, registrySignature, registryPublicKey []byte, approved ApprovedTrustRoots) (TargetRegistry, VerifiedValidationReceipt, error) {
	registry, err := VerifyTargetRegistry(raw, registrySignature, registryPublicKey, approved)
	if err != nil {
		return TargetRegistry{}, VerifiedValidationReceipt{}, err
	}
	receipt := validationReceipt(raw, registry.Candidate, len(registry.Targets), "target_registry", "trusted_registry_structure_valid", RegistryDigest(raw), approved.Digest(), signatureSetDigest(registrySignature, nil, nil))
	return registry, mintVerifiedValidationReceipt(receipt), nil
}

// VerifyCorpusReceipt performs the complete registry, candidate, artifact-set,
// contract, and dual-signature verification before minting an opaque receipt.
func VerifyCorpusReceipt(raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey []byte, approved ApprovedTrustRoots) (CorpusManifest, VerifiedValidationReceipt, error) {
	registry, err := VerifyTargetRegistry(registryRaw, registrySignature, registryPublicKey, approved)
	if err != nil {
		return CorpusManifest{}, VerifiedValidationReceipt{}, err
	}
	manifest, err := DecodeCanonicalStrict[CorpusManifest](raw)
	if err != nil {
		return CorpusManifest{}, VerifiedValidationReceipt{}, err
	}
	if err := ValidateCorpus(manifest, registry, RegistryDigest(registryRaw)); err != nil {
		return CorpusManifest{}, VerifiedValidationReceipt{}, err
	}
	if err := ValidatePacketSignatures(raw, manifest.Approval, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, approved); err != nil {
		return CorpusManifest{}, VerifiedValidationReceipt{}, err
	}
	receipt := validationReceipt(raw, manifest.Candidate, len(manifest.Clips), "corpus", "dual_signed_corpus_structure_valid", manifest.Approval.RegistrySHA256, approved.Digest(), signatureSetDigest(registrySignature, operatorSignature, reviewerSignature))
	return manifest, mintVerifiedValidationReceipt(receipt), nil
}

// VerifyPilotReceipt performs the complete registry, candidate, artifact-set,
// pilot-contract, and dual-signature verification before minting a receipt.
func VerifyPilotReceipt(raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey []byte, approved ApprovedTrustRoots) (PilotPacket, VerifiedValidationReceipt, error) {
	registry, err := VerifyTargetRegistry(registryRaw, registrySignature, registryPublicKey, approved)
	if err != nil {
		return PilotPacket{}, VerifiedValidationReceipt{}, err
	}
	packet, err := DecodeCanonicalStrict[PilotPacket](raw)
	if err != nil {
		return PilotPacket{}, VerifiedValidationReceipt{}, err
	}
	if err := ValidatePilotPacket(packet, registry, RegistryDigest(registryRaw)); err != nil {
		return PilotPacket{}, VerifiedValidationReceipt{}, err
	}
	if err := verifyPilotReviewSignatures(packet, approved); err != nil {
		return PilotPacket{}, VerifiedValidationReceipt{}, err
	}
	if err := ValidatePacketSignatures(raw, packet.Approval, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, approved); err != nil {
		return PilotPacket{}, VerifiedValidationReceipt{}, err
	}
	receipt := validationReceipt(raw, packet.Candidate, len(packet.Pilots), "io_pilots", "dual_signed_io_packet_structure_valid", packet.Approval.RegistrySHA256, approved.Digest(), signatureSetDigest(registrySignature, operatorSignature, reviewerSignature))
	return packet, mintVerifiedValidationReceipt(receipt), nil
}

// VerifyQualificationResultReceipt verifies a signed evaluator-result packet
// against the exact opaque source receipt that was minted for the corpus or
// I&O packet. It returns a second opaque capability suitable for durable import;
// neither result JSON nor serialized receipt JSON can substitute for it.
func VerifyQualificationResultReceipt(raw, sourceRaw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey []byte, approved ApprovedTrustRoots, source VerifiedValidationReceipt) (QualificationResultPacket, VerifiedValidationReceipt, VerifiedQualificationResult, error) {
	registry, err := VerifyTargetRegistry(registryRaw, registrySignature, registryPublicKey, approved)
	if err != nil {
		return QualificationResultPacket{}, VerifiedValidationReceipt{}, VerifiedQualificationResult{}, err
	}
	packet, err := DecodeCanonicalStrict[QualificationResultPacket](raw)
	if err != nil {
		return QualificationResultPacket{}, VerifiedValidationReceipt{}, VerifiedQualificationResult{}, err
	}
	if err := validateQualificationResultPacket(packet, registry, RegistryDigest(registryRaw), approved.Digest(), sourceRaw, source); err != nil {
		return QualificationResultPacket{}, VerifiedValidationReceipt{}, VerifiedQualificationResult{}, err
	}
	if err := ValidatePacketSignatures(raw, packet.Approval, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, approved); err != nil {
		return QualificationResultPacket{}, VerifiedValidationReceipt{}, VerifiedQualificationResult{}, err
	}
	receipt := validationReceipt(raw, packet.Candidate, 1, "qualification_result", "dual_signed_qualification_result_valid", packet.Approval.RegistrySHA256, approved.Digest(), signatureSetDigest(registrySignature, operatorSignature, reviewerSignature))
	verifiedReceipt := mintVerifiedValidationReceipt(receipt)
	target, targetFound := registryTarget(registry, packet.TargetID)
	sourceReceipt, sourceReceiptErr := inspectVerifiedReceipt(source)
	if !targetFound || sourceReceiptErr != nil {
		return QualificationResultPacket{}, VerifiedValidationReceipt{}, VerifiedQualificationResult{}, errors.New("verified qualification bindings became unavailable")
	}
	var meetingSpecialistBinding MeetingSpecialistQualificationBinding
	if packet.Lane == "meeting_specialist" {
		manifest, decodeErr := DecodeCanonicalStrict[CorpusManifest](sourceRaw)
		if decodeErr != nil {
			return QualificationResultPacket{}, VerifiedValidationReceipt{}, VerifiedQualificationResult{}, errors.New("verified meeting specialist source binding became unavailable")
		}
		meetingSpecialistBinding = manifest.MeetingSpecialistBinding
	}
	record := QualificationImportRecord{
		ResultID: packet.ResultID, TenantID: packet.TenantID, TargetID: packet.TargetID, FixtureSHA256: target.FixtureSHA256, QualificationSubjectSHA256: packet.QualificationSubjectSHA256, Lane: packet.Lane, Qualified: packet.Qualified,
		Candidate: packet.Candidate, MeetingSpecialistBinding: meetingSpecialistBinding, RegistrySHA256: receipt.RegistrySHA256, TrustRootSHA256: receipt.TrustRootSHA256,
		SourcePacketKind: packet.SourcePacketKind, SourcePacketSHA256: packet.SourcePacketSHA256, SourceSignatureSetSHA256: sourceReceipt.SignatureSetSHA256,
		ResultPacketSHA256: receipt.InputSHA256, ResultSignatureSetSHA256: receipt.SignatureSetSHA256,
		EvaluatorConfigSHA256: packet.EvaluatorConfigSHA256, EvaluatorResultSHA256: packet.EvaluatorResultSHA256, EvaluatedAt: packet.EvaluatedAt,
	}
	return packet, verifiedReceipt, mintVerifiedQualificationResult(record), nil
}

// BuildQualificationImportBundle freezes the complete signed registry, source,
// and result chain into one canonical JSON artifact. Verification is repeated
// from that artifact before it is returned, so callers cannot accidentally
// serialize a different set of bytes than the set they verified.
func BuildQualificationImportBundle(tenantID, sourcePacketKind string, registryRaw, registrySignature, registryPublicKey, sourceRaw, sourceOperatorSignature, sourceOperatorPublicKey, sourceReviewerSignature, sourceReviewerPublicKey, resultRaw, resultOperatorSignature, resultOperatorPublicKey, resultReviewerSignature, resultReviewerPublicKey []byte, approved ApprovedTrustRoots) ([]byte, VerifiedQualificationResult, error) {
	bundle := QualificationImportBundle{
		SchemaVersion: QualificationImportBundleSchema,
		TenantID:      strings.TrimSpace(tenantID), SourcePacketKind: strings.TrimSpace(sourcePacketKind),
		RegistryRaw:       append(json.RawMessage(nil), registryRaw...),
		RegistryAuthority: qualificationSignatureMaterial(registrySignature, registryPublicKey),
		SourceRaw:         append(json.RawMessage(nil), sourceRaw...),
		SourceOperator:    qualificationSignatureMaterial(sourceOperatorSignature, sourceOperatorPublicKey),
		SourceReviewer:    qualificationSignatureMaterial(sourceReviewerSignature, sourceReviewerPublicKey),
		ResultRaw:         append(json.RawMessage(nil), resultRaw...),
		ResultOperator:    qualificationSignatureMaterial(resultOperatorSignature, resultOperatorPublicKey),
		ResultReviewer:    qualificationSignatureMaterial(resultReviewerSignature, resultReviewerPublicKey),
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return nil, VerifiedQualificationResult{}, err
	}
	canonical, err := CanonicalizeJSON(raw)
	if err != nil {
		return nil, VerifiedQualificationResult{}, err
	}
	_, verified, err := VerifyQualificationImportBundle(canonical, approved)
	if err != nil {
		return nil, VerifiedQualificationResult{}, err
	}
	return canonical, verified, nil
}

// VerifyQualificationImportBundle replays every trust decision from a
// serialized bundle against separately configured approved roots. No digest or
// receipt embedded in the bundle can replace the registry/source/result bytes,
// detached signatures, or anchored signer roster.
func VerifyQualificationImportBundle(raw []byte, approved ApprovedTrustRoots) (QualificationImportBundle, VerifiedQualificationResult, error) {
	bundle, err := DecodeCanonicalStrict[QualificationImportBundle](raw)
	if err != nil {
		return QualificationImportBundle{}, VerifiedQualificationResult{}, fmt.Errorf("qualification import bundle: %w", err)
	}
	if bundle.SchemaVersion != QualificationImportBundleSchema || !validID(bundle.TenantID) || !oneOf(bundle.SourcePacketKind, "corpus", "io_pilots") {
		return QualificationImportBundle{}, VerifiedQualificationResult{}, errors.New("qualification import bundle identity is invalid")
	}
	registrySignature, registryPublicKey, err := decodeQualificationSignatureMaterial(bundle.RegistryAuthority)
	if err != nil {
		return QualificationImportBundle{}, VerifiedQualificationResult{}, fmt.Errorf("qualification import registry authority: %w", err)
	}
	sourceOperatorSignature, sourceOperatorPublicKey, err := decodeQualificationSignatureMaterial(bundle.SourceOperator)
	if err != nil {
		return QualificationImportBundle{}, VerifiedQualificationResult{}, fmt.Errorf("qualification import source operator: %w", err)
	}
	sourceReviewerSignature, sourceReviewerPublicKey, err := decodeQualificationSignatureMaterial(bundle.SourceReviewer)
	if err != nil {
		return QualificationImportBundle{}, VerifiedQualificationResult{}, fmt.Errorf("qualification import source reviewer: %w", err)
	}
	resultOperatorSignature, resultOperatorPublicKey, err := decodeQualificationSignatureMaterial(bundle.ResultOperator)
	if err != nil {
		return QualificationImportBundle{}, VerifiedQualificationResult{}, fmt.Errorf("qualification import result operator: %w", err)
	}
	resultReviewerSignature, resultReviewerPublicKey, err := decodeQualificationSignatureMaterial(bundle.ResultReviewer)
	if err != nil {
		return QualificationImportBundle{}, VerifiedQualificationResult{}, fmt.Errorf("qualification import result reviewer: %w", err)
	}

	var sourceReceipt VerifiedValidationReceipt
	switch bundle.SourcePacketKind {
	case "corpus":
		manifest, verifiedReceipt, verifyErr := VerifyCorpusReceipt(bundle.SourceRaw, bundle.RegistryRaw, registrySignature, registryPublicKey, sourceOperatorSignature, sourceOperatorPublicKey, sourceReviewerSignature, sourceReviewerPublicKey, approved)
		if verifyErr != nil {
			return QualificationImportBundle{}, VerifiedQualificationResult{}, verifyErr
		}
		if manifest.TenantID != bundle.TenantID {
			return QualificationImportBundle{}, VerifiedQualificationResult{}, errors.New("qualification import corpus tenant binding is invalid")
		}
		sourceReceipt = verifiedReceipt
	case "io_pilots":
		packet, verifiedReceipt, verifyErr := VerifyPilotReceipt(bundle.SourceRaw, bundle.RegistryRaw, registrySignature, registryPublicKey, sourceOperatorSignature, sourceOperatorPublicKey, sourceReviewerSignature, sourceReviewerPublicKey, approved)
		if verifyErr != nil {
			return QualificationImportBundle{}, VerifiedQualificationResult{}, verifyErr
		}
		if packet.TenantID != bundle.TenantID {
			return QualificationImportBundle{}, VerifiedQualificationResult{}, errors.New("qualification import pilot tenant binding is invalid")
		}
		sourceReceipt = verifiedReceipt
	}
	packet, _, verified, err := VerifyQualificationResultReceipt(bundle.ResultRaw, bundle.SourceRaw, bundle.RegistryRaw, registrySignature, registryPublicKey, resultOperatorSignature, resultOperatorPublicKey, resultReviewerSignature, resultReviewerPublicKey, approved, sourceReceipt)
	if err != nil {
		return QualificationImportBundle{}, VerifiedQualificationResult{}, err
	}
	if packet.TenantID != bundle.TenantID || packet.SourcePacketKind != bundle.SourcePacketKind {
		return QualificationImportBundle{}, VerifiedQualificationResult{}, errors.New("qualification import result tenant or source-kind binding is invalid")
	}
	return bundle, verified, nil
}

func qualificationSignatureMaterial(signature, publicKey []byte) QualificationSignatureMaterial {
	return QualificationSignatureMaterial{SignatureHex: hex.EncodeToString(signature), PublicKeyHex: hex.EncodeToString(publicKey)}
}

func decodeQualificationSignatureMaterial(material QualificationSignatureMaterial) ([]byte, []byte, error) {
	signature, signatureErr := decodeCanonicalHex(material.SignatureHex, ed25519.SignatureSize)
	publicKey, publicKeyErr := decodeCanonicalHex(material.PublicKeyHex, ed25519.PublicKeySize)
	if signatureErr != nil || publicKeyErr != nil {
		return nil, nil, errors.New("detached signature or public key is not canonical Ed25519 material")
	}
	return signature, publicKey, nil
}

func validateQualificationResultPacket(packet QualificationResultPacket, registry TargetRegistry, registrySHA256, trustRootSHA256 string, sourceRaw []byte, source VerifiedValidationReceipt) error {
	if packet.SchemaVersion != QualificationResultSchema || !validID(packet.ResultID) || !validID(packet.TenantID) || !validID(packet.TargetID) || !oneOf(packet.Lane, "meeting_stt", "composer_dictation", "insights_opportunities", "meeting_specialist") || packet.EvidenceClass != "dual_signed_evaluator_result" {
		return errors.New("qualification result schema, identity, lane, or evidenceClass is invalid")
	}
	if err := validatePacketBinding(packet.Candidate, packet.Approval, registry, registrySHA256); err != nil {
		return err
	}
	target, ok := registryTarget(registry, packet.TargetID)
	if !ok || target.Category != "qualification_evaluator" || target.ID != qualificationTargetForLane(packet.Lane) || packet.QualificationSubjectSHA256 != target.FixtureSHA256 || target.OwnerID != packet.Approval.OperatorID || target.IndependentReviewerID != packet.Approval.ReviewerID {
		return errors.New("qualification result is not bound to its preregistered target and accountable signers")
	}
	if packet.EvaluatorConfigSHA256 != target.MeasurementRevisionSHA256 {
		return errors.New("qualification result evaluator configuration drifts from the preregistered measurement revision")
	}
	wantSourceKind := "corpus"
	if packet.Lane == "insights_opportunities" {
		wantSourceKind = "io_pilots"
	}
	sourceReceipt, err := inspectVerifiedReceipt(source)
	if err != nil || packet.SourcePacketKind != wantSourceKind || sourceReceipt.PacketKind != wantSourceKind || packet.SourcePacketSHA256 != RegistryDigest(sourceRaw) || sourceReceipt.InputSHA256 != packet.SourcePacketSHA256 || sourceReceipt.RegistrySHA256 != registrySHA256 || sourceReceipt.TrustRootSHA256 != trustRootSHA256 || sourceReceipt.Candidate != packet.Candidate {
		return errors.New("qualification result source receipt, packet bytes, registry, trust root, or candidate binding is invalid")
	}
	switch packet.Lane {
	case "meeting_stt", "composer_dictation", "meeting_specialist":
		manifest, decodeErr := DecodeCanonicalStrict[CorpusManifest](sourceRaw)
		if decodeErr != nil || manifest.TenantID != packet.TenantID || manifest.TargetID != packet.TargetID || manifest.Lane != packet.Lane || manifest.QualificationSubjectSHA256 != packet.QualificationSubjectSHA256 {
			return errors.New("qualification result references the wrong signed corpus")
		}
	case "insights_opportunities":
		pilot, decodeErr := DecodeCanonicalStrict[PilotPacket](sourceRaw)
		if decodeErr != nil || pilot.TenantID != packet.TenantID || pilot.TargetID != packet.TargetID || pilot.QualificationSubjectSHA256 != packet.QualificationSubjectSHA256 {
			return errors.New("qualification result references the wrong signed I&O packet")
		}
	}
	when, timeErr := time.Parse(time.RFC3339Nano, packet.EvaluatedAt)
	if timeErr != nil || when.After(time.Now().Add(5*time.Minute)) || !validSHA(packet.EvaluatorConfigSHA256) || !validSHA(packet.EvaluatorResultSHA256) {
		return errors.New("qualification evaluator configuration, result digest, or time is invalid")
	}
	if packet.Approval.SourceArtifactSetSHA256 != QualificationSourceArtifactSetDigest(packet.SourcePacketSHA256, packet.EvaluatorConfigSHA256, packet.EvaluatorResultSHA256) {
		return errors.New("qualification result source artifact-set digest is invalid")
	}
	return nil
}

func inspectVerifiedReceipt(verified VerifiedValidationReceipt) (ValidationReceipt, error) {
	if _, err := EncodeReceipt(verified); err != nil {
		return ValidationReceipt{}, err
	}
	return verified.receipt, nil
}

// QualificationImport extracts an immutable record only after rechecking the
// private proof on the opaque qualification capability.
func QualificationImport(verified VerifiedQualificationResult) (QualificationImportRecord, error) {
	expected := verifiedQualificationResultProof(verified.record)
	if subtle.ConstantTimeCompare(verified.proof[:], expected[:]) != 1 || ValidateQualificationImportRecord(verified.record) != nil {
		return QualificationImportRecord{}, errors.New("qualification result was not minted by complete signed-source verification")
	}
	return verified.record, nil
}

func mintVerifiedQualificationResult(record QualificationImportRecord) VerifiedQualificationResult {
	return VerifiedQualificationResult{record: record, proof: verifiedQualificationResultProof(record)}
}

func verifiedQualificationResultProof(record QualificationImportRecord) [sha256.Size]byte {
	payload, _ := json.Marshal(record)
	return sha256.Sum256(append([]byte("stride-e10-verified-qualification-result/v1\x00"), payload...))
}

func ValidateQualificationImportRecord(record QualificationImportRecord) error {
	when, err := time.Parse(time.RFC3339Nano, record.EvaluatedAt)
	if err != nil || when.After(time.Now().Add(5*time.Minute)) || !validID(record.ResultID) || !validID(record.TenantID) || !validID(record.TargetID) || qualificationTargetForLane(record.Lane) != record.TargetID ||
		!oneOf(record.SourcePacketKind, "corpus", "io_pilots") || !validSHA(record.FixtureSHA256) || record.QualificationSubjectSHA256 != record.FixtureSHA256 || !validSHA(record.RegistrySHA256) || !validSHA(record.TrustRootSHA256) || !validSHA(record.SourcePacketSHA256) || !validSHA(record.SourceSignatureSetSHA256) || !validSHA(record.ResultPacketSHA256) || !validSHA(record.ResultSignatureSetSHA256) || !validSHA(record.EvaluatorConfigSHA256) || !validSHA(record.EvaluatorResultSHA256) || validateCandidate(record.Candidate) != nil {
		return errors.New("qualification import record is invalid")
	}
	if record.Lane == "meeting_specialist" {
		if validateMeetingSpecialistQualificationBinding(record.MeetingSpecialistBinding) != nil || MeetingSpecialistQualificationFixtureDigest(record.MeetingSpecialistBinding) != record.QualificationSubjectSHA256 || record.Candidate.RouteMapDigest != record.MeetingSpecialistBinding.RouteDigest {
			return errors.New("meeting specialist qualification import subject is invalid")
		}
	} else if record.MeetingSpecialistBinding != (MeetingSpecialistQualificationBinding{}) {
		return errors.New("non-specialist qualification import contains a specialist subject")
	}
	return nil
}

func QualificationSourceArtifactSetDigest(sourcePacketSHA256, evaluatorConfigSHA256, evaluatorResultSHA256 string) string {
	return digestArtifactEntries([]string{"source_packet:" + sourcePacketSHA256, "evaluator_config:" + evaluatorConfigSHA256, "evaluator_result:" + evaluatorResultSHA256})
}

// VerifyMatrixReceipt performs the complete registry, target, artifact-set,
// observation, and dual-signature verification before minting a receipt.
func VerifyMatrixReceipt(raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey []byte, approved ApprovedTrustRoots) (ExternalMatrix, VerifiedValidationReceipt, error) {
	registry, err := VerifyTargetRegistry(registryRaw, registrySignature, registryPublicKey, approved)
	if err != nil {
		return ExternalMatrix{}, VerifiedValidationReceipt{}, err
	}
	matrix, err := DecodeCanonicalStrict[ExternalMatrix](raw)
	if err != nil {
		return ExternalMatrix{}, VerifiedValidationReceipt{}, err
	}
	if err := ValidateExternalMatrix(matrix, registry, RegistryDigest(registryRaw)); err != nil {
		return ExternalMatrix{}, VerifiedValidationReceipt{}, err
	}
	if err := ValidatePacketSignatures(raw, matrix.Approval, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, approved); err != nil {
		return ExternalMatrix{}, VerifiedValidationReceipt{}, err
	}
	receipt := validationReceipt(raw, matrix.Candidate, len(matrix.Observations), "external_matrix", "dual_signed_external_matrix_structure_valid", matrix.Approval.RegistrySHA256, approved.Digest(), signatureSetDigest(registrySignature, operatorSignature, reviewerSignature))
	return matrix, mintVerifiedValidationReceipt(receipt), nil
}

func RegistryDigest(raw []byte) string { return digestBytes(raw) }

func EncodeReceipt(verified VerifiedValidationReceipt) ([]byte, error) {
	receipt := verified.receipt
	states := map[string]string{
		"target_registry":      "trusted_registry_structure_valid",
		"corpus":               "dual_signed_corpus_structure_valid",
		"io_pilots":            "dual_signed_io_packet_structure_valid",
		"external_matrix":      "dual_signed_external_matrix_structure_valid",
		"qualification_result": "dual_signed_qualification_result_valid",
	}
	if receipt.SchemaVersion != ValidationSchema || receipt.EvidenceClass != EvidenceClass || states[receipt.PacketKind] != receipt.State || !validSHA(receipt.InputSHA256) || !validSHA(receipt.RegistrySHA256) || !validSHA(receipt.TrustRootSHA256) || !validSHA(receipt.SignatureSetSHA256) || receipt.ItemCount < 1 || errString(validateCandidate(receipt.Candidate)) != "" || !sameStrings(receipt.ClaimsExcluded, excludedClaims()) {
		return nil, errors.New("validation receipt is incomplete or contains an unauthorized class/state/claim set")
	}
	expectedProof := verifiedReceiptProof(receipt)
	if subtle.ConstantTimeCompare(verified.proof[:], expectedProof[:]) != 1 {
		return nil, errors.New("validation receipt was not minted by a complete verification or was mutated after verification")
	}
	return json.MarshalIndent(receipt, "", "  ")
}

func validationReceipt(raw []byte, candidate CandidateBinding, count int, kind, state, registryDigest, trustRootDigest, signaturesDigest string) ValidationReceipt {
	return ValidationReceipt{SchemaVersion: ValidationSchema, EvidenceClass: EvidenceClass, PacketKind: kind, State: state, InputSHA256: RegistryDigest(raw), RegistrySHA256: registryDigest, TrustRootSHA256: trustRootDigest, SignatureSetSHA256: signaturesDigest, Candidate: candidate, ItemCount: count, ClaimsExcluded: excludedClaims()}
}

func mintVerifiedValidationReceipt(receipt ValidationReceipt) VerifiedValidationReceipt {
	return VerifiedValidationReceipt{receipt: receipt, proof: verifiedReceiptProof(receipt)}
}

func verifiedReceiptProof(receipt ValidationReceipt) [sha256.Size]byte {
	type proofPayload struct {
		SchemaVersion      string           `json:"schemaVersion"`
		EvidenceClass      string           `json:"evidenceClass"`
		PacketKind         string           `json:"packetKind"`
		State              string           `json:"state"`
		InputSHA256        string           `json:"inputSha256"`
		RegistrySHA256     string           `json:"registrySha256"`
		TrustRootSHA256    string           `json:"trustRootSha256"`
		SignatureSetSHA256 string           `json:"signatureSetSha256"`
		Candidate          CandidateBinding `json:"candidate"`
		ItemCount          int              `json:"itemCount"`
		ClaimsExcluded     []string         `json:"claimsExcluded"`
	}
	payload, _ := json.Marshal(proofPayload{
		SchemaVersion: receipt.SchemaVersion, EvidenceClass: receipt.EvidenceClass,
		PacketKind: receipt.PacketKind, State: receipt.State, InputSHA256: receipt.InputSHA256,
		RegistrySHA256: receipt.RegistrySHA256, TrustRootSHA256: receipt.TrustRootSHA256,
		SignatureSetSHA256: receipt.SignatureSetSHA256, Candidate: receipt.Candidate,
		ItemCount: receipt.ItemCount, ClaimsExcluded: receipt.ClaimsExcluded,
	})
	return sha256.Sum256(append([]byte("stride-e10-verified-validation-receipt/v2\x00"), payload...))
}

func signatureSetDigest(registrySignature, operatorSignature, reviewerSignature []byte) string {
	if len(registrySignature) != ed25519.SignatureSize || operatorSignature != nil && len(operatorSignature) != ed25519.SignatureSize || reviewerSignature != nil && len(reviewerSignature) != ed25519.SignatureSize {
		return ""
	}
	entries := []string{"registry:" + hex.EncodeToString(registrySignature)}
	if operatorSignature != nil {
		entries = append(entries, "operator:"+hex.EncodeToString(operatorSignature))
	}
	if reviewerSignature != nil {
		entries = append(entries, "reviewer:"+hex.EncodeToString(reviewerSignature))
	}
	return digestArtifactEntries(entries)
}

func excludedClaims() []string {
	return []string{"provider_qualified", "physical_device_verified", "production_accepted", "release_qualified", "route_enabled", "launch_ready"}
}

func validatePacketBinding(candidate CandidateBinding, approval DualApprovalBinding, registry TargetRegistry, registrySHA256 string) error {
	if err := validateCandidate(candidate); err != nil {
		return err
	}
	if candidate != registry.Candidate || !validSHA(registrySHA256) || approval.RegistrySHA256 != registrySHA256 || !validSHA(approval.SourceArtifactSetSHA256) || !validID(approval.OperatorID) || !validID(approval.OperatorKeyID) || !validSHA(approval.OperatorPublicKeyFingerprintSHA256) || !validID(approval.ReviewerID) || !validID(approval.ReviewerKeyID) || !validSHA(approval.ReviewerPublicKeyFingerprintSHA256) {
		return errors.New("packet registry/candidate/source/signer binding is invalid")
	}
	if approval.OperatorID == approval.ReviewerID || approval.OperatorKeyID == approval.ReviewerKeyID || approval.OperatorPublicKeyFingerprintSHA256 == approval.ReviewerPublicKeyFingerprintSHA256 {
		return errors.New("operator and independent reviewer bindings are not distinct")
	}
	return nil
}

func corpusArtifactSetDigest(clips []CorpusClip) string {
	entries := make([]string, 0, len(clips)*5)
	for _, clip := range clips {
		entries = append(entries, "audio:"+clip.AudioSHA256, "reference:"+clip.ReferenceSHA256, "consent:"+clip.ConsentReceiptSHA256, "speaker:"+clip.SpeakerEvidenceSHA256, "track:"+clip.TrackEvidenceSHA256)
	}
	return digestArtifactEntries(entries)
}
func pilotArtifactSetDigest(roster []EligiblePilotReviewer, pilots []IOPilot) string {
	entries := make([]string, 0, len(roster)+len(pilots)*7)
	for _, reviewer := range roster {
		entries = append(entries, "reviewer_eligibility:"+reviewer.ReviewerID+":"+reviewer.EligibilityReceiptDigest)
	}
	for _, pilot := range pilots {
		entries = append(entries, "input:"+pilot.InputDigest, "run:"+pilot.RunReceiptDigest, "artifact:"+pilot.ArtifactDigest, "disposition_reason:"+pilot.DispositionReasonDigest, "terminal_visibility:"+pilot.TerminalVisibilityReceiptDigest, "external_effect_audit:"+pilot.ExternalEffectAuditReceiptDigest)
		for _, review := range pilot.Reviews {
			entries = append(entries, "review:"+review.ReviewerID+":"+review.ReviewReceiptDigest+":"+review.Disposition+":"+digestBytes([]byte(review.SignatureHex)))
		}
	}
	return digestArtifactEntries(entries)
}

func claimUniqueDigest(seen map[string]bool, digest string) bool {
	if !validSHA(digest) || seen[digest] {
		return false
	}
	seen[digest] = true
	return true
}
func externalArtifactSetDigest(observations []ExternalObservation) string {
	entries := make([]string, 0, len(observations))
	for _, observation := range observations {
		entries = append(entries, "observation:"+observation.ArtifactSHA256)
	}
	return digestArtifactEntries(entries)
}
func digestArtifactEntries(entries []string) string {
	sort.Strings(entries)
	return digestBytes([]byte(strings.Join(entries, "\n")))
}
func digestBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }

func decodeCanonicalHex(value string, size int) ([]byte, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return nil, errors.New("hex value is not canonical")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != size || hex.EncodeToString(decoded) != value {
		return nil, errors.New("hex value is not canonical")
	}
	return decoded, nil
}

func validateCandidate(candidate CandidateBinding) error {
	if !commitPattern(candidate.ReleaseCommit) {
		return errors.New("candidate releaseCommit is invalid")
	}
	for name, value := range map[string]string{"gitTreeDigest": candidate.GitTreeDigest, "imageDigest": candidate.ImageDigest, "configDigest": candidate.ConfigDigest, "routeMapDigest": candidate.RouteMapDigest} {
		if !validSHA(value) {
			return fmt.Errorf("candidate %s is invalid", name)
		}
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func validSHA(value string) bool { return shaPattern(value) }
func validID(value string) bool  { return identifier(strings.TrimSpace(value)) }
func oneOf(value string, allowed ...string) bool {
	values := sortedCopy(allowed)
	index := sort.SearchStrings(values, value)
	return index < len(values) && values[index] == value
}
func sortedCopy(values []string) []string {
	copyValues := append([]string(nil), values...)
	sort.Strings(copyValues)
	return copyValues
}
func mustPattern(length int, alphabet string) func(string) bool {
	return func(value string) bool {
		if len(value) != length {
			return false
		}
		for _, char := range value {
			if !strings.ContainsRune(alphabet, char) {
				return false
			}
		}
		return true
	}
}
func mustPatternEither(first, second int, alphabet string) func(string) bool {
	return func(value string) bool {
		return mustPattern(first, alphabet)(value) || mustPattern(second, alphabet)(value)
	}
}
func mustIdentifier() func(string) bool {
	return func(value string) bool {
		if len(value) < 3 || len(value) > 128 {
			return false
		}
		for index, char := range value {
			if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || (index > 0 && char == '.') {
				continue
			}
			return false
		}
		return true
	}
}
