package e10probe

// Token-free, fail-closed machinery for the E10 Scout invocation corpus.
// This package never calls a provider. Local evaluation is deliberately
// untrusted; only the authority-bound entry point can emit a provider-live
// evidence candidate, and it requires an externally anchored signed registry,
// signed evidence, dual attestations, and a durable one-use attempt ledger.

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	ScoutOrdinaryMinimum         = 2000
	ScoutExplicitMinimum         = 500
	ScoutAudienceNegativeMinimum = 1000
	ScoutMaxFalseResponseRate    = 0.005
	ScoutMinExplicitRecall       = 0.98
	ScoutMaxFirstUsefulAudioMS   = int64(2500)
	ScoutMaxBargeInStopMS        = int64(500)
	maxScoutQualificationRawSize = 64 << 10
	scoutBootstrapReplicates     = 1000
	ScoutLaneOrdinaryMinimum     = ScoutOrdinaryMinimum / 2
	ScoutLaneExplicitMinimum     = ScoutExplicitMinimum / 2
	ScoutLaneAudienceMinimum     = ScoutAudienceNegativeMinimum / 2
)

var (
	ErrScoutQualificationInvalid  = errors.New("invalid scout qualification input")
	ErrScoutQualificationEvidence = errors.New("scout qualification evidence is incomplete or untrusted")
	ErrScoutQualificationFailed   = errors.New("scout qualification thresholds were not met")
	ErrScoutAttemptReused         = errors.New("scout qualification attempt was already consumed")
	ErrScoutReceiptExpired        = errors.New("scout qualification receipt expired")
	ErrScoutReceiptReused         = errors.New("scout qualification receipt was already consumed")
)

type ScoutCorpusClass string

type ScoutQualificationLane string
type ScoutQualificationSurface string
type ScoutQualificationPlatform string

const (
	ScoutCorpusOrdinary         ScoutCorpusClass           = "ordinary"
	ScoutCorpusExplicit         ScoutCorpusClass           = "explicit_invocation"
	ScoutCorpusAudienceNegative ScoutCorpusClass           = "audience_authorization_negative"
	ScoutLanePersonal           ScoutQualificationLane     = "personal_scout"
	ScoutLaneMeeting            ScoutQualificationLane     = "meeting_scout"
	ScoutSurfacePrivateVoice    ScoutQualificationSurface  = "private_realtime_voice"
	ScoutSurfaceMeetingVoice    ScoutQualificationSurface  = "meeting_realtime_voice"
	ScoutPlatformPhysicalIOS    ScoutQualificationPlatform = "physical_ios"
	ScoutPlatformDesktopWeb     ScoutQualificationPlatform = "desktop_web"
)

// ScoutQualificationCase stores only the exact audio digest. The consented
// audio itself remains in the private corpus store, never in a receipt.
type ScoutQualificationCase struct {
	ID               string                     `json:"id"`
	Class            ScoutCorpusClass           `json:"class"`
	Lane             ScoutQualificationLane     `json:"lane"`
	Surface          ScoutQualificationSurface  `json:"surface"`
	Platform         ScoutQualificationPlatform `json:"platform"`
	InputAudioSHA256 string                     `json:"inputAudioSha256"`
}

type ScoutQualificationDistribution struct {
	PersonalPhysicalIOS int `json:"personalPhysicalIos"`
	PersonalDesktopWeb  int `json:"personalDesktopWeb"`
	MeetingPhysicalIOS  int `json:"meetingPhysicalIos"`
	MeetingDesktopWeb   int `json:"meetingDesktopWeb"`
}

type ScoutQualificationCorpus struct {
	Schema        string                         `json:"schema"`
	Version       int                            `json:"version"`
	ReleaseSHA256 string                         `json:"releaseSha256"`
	Cases         []ScoutQualificationCase       `json:"cases"`
	Distribution  ScoutQualificationDistribution `json:"distribution"`
	Digest        string                         `json:"digest"`
}

func FreezeScoutQualificationCorpus(version int, releaseSHA256 string, cases []ScoutQualificationCase) (ScoutQualificationCorpus, error) {
	if version < 1 || !validDigest(releaseSHA256) || len(cases) == 0 {
		return ScoutQualificationCorpus{}, ErrScoutQualificationInvalid
	}
	clone := append([]ScoutQualificationCase(nil), cases...)
	sort.Slice(clone, func(i, j int) bool { return clone[i].ID < clone[j].ID })
	ordinary, explicit, audience := 0, 0, 0
	laneClasses := map[ScoutQualificationLane]map[ScoutCorpusClass]int{
		ScoutLanePersonal: {}, ScoutLaneMeeting: {},
	}
	seenIDs := make(map[string]struct{}, len(clone))
	seenAudio := make(map[string]struct{}, len(clone))
	for _, testCase := range clone {
		if !validScoutCase(testCase) {
			return ScoutQualificationCorpus{}, ErrScoutQualificationInvalid
		}
		if _, duplicate := seenIDs[testCase.ID]; duplicate {
			return ScoutQualificationCorpus{}, ErrScoutQualificationInvalid
		}
		if _, duplicate := seenAudio[testCase.InputAudioSHA256]; duplicate {
			return ScoutQualificationCorpus{}, ErrScoutQualificationInvalid
		}
		seenIDs[testCase.ID] = struct{}{}
		seenAudio[testCase.InputAudioSHA256] = struct{}{}
		switch testCase.Class {
		case ScoutCorpusOrdinary:
			ordinary++
		case ScoutCorpusExplicit:
			explicit++
		case ScoutCorpusAudienceNegative:
			audience++
		}
		laneClasses[testCase.Lane][testCase.Class]++
	}
	if ordinary < ScoutOrdinaryMinimum || explicit < ScoutExplicitMinimum || audience < ScoutAudienceNegativeMinimum {
		return ScoutQualificationCorpus{}, ErrScoutQualificationInvalid
	}
	for _, lane := range []ScoutQualificationLane{ScoutLanePersonal, ScoutLaneMeeting} {
		if laneClasses[lane][ScoutCorpusOrdinary] < ScoutLaneOrdinaryMinimum || laneClasses[lane][ScoutCorpusExplicit] < ScoutLaneExplicitMinimum || laneClasses[lane][ScoutCorpusAudienceNegative] < ScoutLaneAudienceMinimum {
			return ScoutQualificationCorpus{}, ErrScoutQualificationInvalid
		}
	}
	distribution := scoutCorpusDistribution(clone)
	payload := struct {
		Schema        string                         `json:"schema"`
		Version       int                            `json:"version"`
		ReleaseSHA256 string                         `json:"releaseSha256"`
		Cases         []ScoutQualificationCase       `json:"cases"`
		Distribution  ScoutQualificationDistribution `json:"distribution"`
	}{"stride.e10.scout-qualification-corpus/v3", version, strings.ToLower(releaseSHA256), clone, distribution}
	digest, err := qualificationDigest(payload)
	if err != nil {
		return ScoutQualificationCorpus{}, err
	}
	return ScoutQualificationCorpus{Schema: payload.Schema, Version: version, ReleaseSHA256: payload.ReleaseSHA256, Cases: clone, Distribution: distribution, Digest: digest}, nil
}

func (corpus ScoutQualificationCorpus) Validate() error {
	if corpus.Schema != "stride.e10.scout-qualification-corpus/v3" || corpus.Version < 1 || !validDigest(corpus.ReleaseSHA256) || !validDigest(corpus.Digest) {
		return ErrScoutQualificationInvalid
	}
	frozen, err := FreezeScoutQualificationCorpus(corpus.Version, corpus.ReleaseSHA256, corpus.Cases)
	if err != nil || frozen.Digest != corpus.Digest || frozen.Distribution != corpus.Distribution {
		return ErrScoutQualificationInvalid
	}
	return nil
}

type ScoutQualificationConfig struct {
	Schema                     string                          `json:"schema"`
	Version                    int                             `json:"version"`
	ReleaseSHA256              string                          `json:"releaseSha256"`
	CandidateSHA256            string                          `json:"candidateSha256"`
	SourceArtifactSetSHA256    string                          `json:"sourceArtifactSetSha256"`
	ProviderIdentity           ScoutProviderIdentity           `json:"providerIdentity"`
	BillingReconciliation      ScoutBillingReconciliationRoute `json:"billingReconciliation"`
	RequiredDistribution       ScoutQualificationDistribution  `json:"requiredDistribution"`
	Model                      string                          `json:"model"`
	ReasoningEffort            string                          `json:"reasoningEffort"`
	Voice                      string                          `json:"voice"`
	VADMode                    string                          `json:"vadMode"`
	VADPolicySHA256            string                          `json:"vadPolicySha256"`
	ToolPolicySHA256           string                          `json:"toolPolicySha256"`
	PromptSHA256               string                          `json:"promptSha256"`
	EventSchemaSHA256          string                          `json:"eventSchemaSha256"`
	RouteIdentitySHA256        string                          `json:"routeIdentitySha256"`
	PricingSource              string                          `json:"pricingSource"`
	PricingRevisionSHA256      string                          `json:"pricingRevisionSha256"`
	InputTextNanoUSDPerToken   int64                           `json:"inputTextNanoUsdPerToken"`
	InputAudioNanoUSDPerToken  int64                           `json:"inputAudioNanoUsdPerToken"`
	CachedTextNanoUSDPerToken  int64                           `json:"cachedTextNanoUsdPerToken"`
	CachedAudioNanoUSDPerToken int64                           `json:"cachedAudioNanoUsdPerToken"`
	OutputTextNanoUSDPerToken  int64                           `json:"outputTextNanoUsdPerToken"`
	OutputAudioNanoUSDPerToken int64                           `json:"outputAudioNanoUsdPerToken"`
	MaxTerminalTokens          int64                           `json:"maxTerminalTokens"`
	MaxTerminalCostNanoUSD     int64                           `json:"maxTerminalCostNanoUsd"`
	MaxAcceptedTokens          int64                           `json:"maxAcceptedTokens"`
	MaxAcceptedCostNanoUSD     int64                           `json:"maxAcceptedCostNanoUsd"`
	MaxRejectedTokens          int64                           `json:"maxRejectedTokens"`
	MaxRejectedCostNanoUSD     int64                           `json:"maxRejectedCostNanoUsd"`
	MaxRejectedTerminalCount   int                             `json:"maxRejectedTerminalCount"`
	Digest                     string                          `json:"digest"`
}

type ScoutCredentialMode string

const ScoutCredentialProjectServiceAccount ScoutCredentialMode = "project_service_account"

type ScoutProviderIdentity struct {
	Provider       string              `json:"provider"`
	ProjectID      string              `json:"projectId"`
	OrganizationID string              `json:"organizationId"`
	CredentialMode ScoutCredentialMode `json:"credentialMode"`
}

type ScoutBillingReconciliationMode string

const ScoutBillingProviderUsageExport ScoutBillingReconciliationMode = "provider_usage_export"

type ScoutBillingReconciliationRoute struct {
	Mode                   ScoutBillingReconciliationMode `json:"mode"`
	BillingAccountID       string                         `json:"billingAccountId"`
	EvidenceSource         string                         `json:"evidenceSource"`
	EvidenceRevisionSHA256 string                         `json:"evidenceRevisionSha256"`
}

func FreezeScoutQualificationConfig(config ScoutQualificationConfig) (ScoutQualificationConfig, error) {
	config.Schema = "stride.e10.scout-qualification-config/v3"
	config.ReleaseSHA256 = strings.ToLower(config.ReleaseSHA256)
	config.CandidateSHA256 = strings.ToLower(config.CandidateSHA256)
	config.SourceArtifactSetSHA256 = strings.ToLower(config.SourceArtifactSetSHA256)
	config.VADPolicySHA256 = strings.ToLower(config.VADPolicySHA256)
	config.ToolPolicySHA256 = strings.ToLower(config.ToolPolicySHA256)
	config.PromptSHA256 = strings.ToLower(config.PromptSHA256)
	config.EventSchemaSHA256 = strings.ToLower(config.EventSchemaSHA256)
	config.PricingRevisionSHA256 = strings.ToLower(config.PricingRevisionSHA256)
	config.BillingReconciliation.EvidenceRevisionSHA256 = strings.ToLower(config.BillingReconciliation.EvidenceRevisionSHA256)
	config.RouteIdentitySHA256 = scoutRouteIdentityDigest(config)
	config.Digest = ""
	if !validScoutConfig(config) {
		return ScoutQualificationConfig{}, ErrScoutQualificationInvalid
	}
	digest, err := qualificationDigest(config)
	if err != nil {
		return ScoutQualificationConfig{}, err
	}
	config.Digest = digest
	return config, nil
}

func (config ScoutQualificationConfig) Validate() error {
	if !validScoutConfig(config) || !validDigest(config.Digest) || scoutRouteIdentityDigest(config) != config.RouteIdentitySHA256 {
		return ErrScoutQualificationInvalid
	}
	copy := config
	copy.Digest = ""
	digest, err := qualificationDigest(copy)
	if err != nil || digest != config.Digest {
		return ErrScoutQualificationInvalid
	}
	return nil
}

// ScoutQualificationTrustRegistry is signed by an out-of-band root. It binds
// the exact candidate, source set, corpus, config, signer identities, physical
// targets, and durable attempt-ledger namespace used by one qualification.
type ScoutQualificationTrustRegistry struct {
	Schema                   string                          `json:"schema"`
	RegistryID               string                          `json:"registryId"`
	AttemptLedgerID          string                          `json:"attemptLedgerId"`
	AttemptLedgerPathSHA256  string                          `json:"attemptLedgerPathSha256"`
	ReceiptLedgerID          string                          `json:"receiptLedgerId"`
	ReceiptLedgerPathSHA256  string                          `json:"receiptLedgerPathSha256"`
	RootKeyID                string                          `json:"rootKeyId"`
	RootKeyFingerprintSHA256 string                          `json:"rootKeyFingerprintSha256"`
	ReleaseSHA256            string                          `json:"releaseSha256"`
	CandidateSHA256          string                          `json:"candidateSha256"`
	SourceArtifactSetSHA256  string                          `json:"sourceArtifactSetSha256"`
	ProviderIdentity         ScoutProviderIdentity           `json:"providerIdentity"`
	BillingReconciliation    ScoutBillingReconciliationRoute `json:"billingReconciliation"`
	RequiredDistribution     ScoutQualificationDistribution  `json:"requiredDistribution"`
	CorpusDistribution       ScoutQualificationDistribution  `json:"corpusDistribution"`
	CorpusSHA256             string                          `json:"corpusSha256"`
	ConfigSHA256             string                          `json:"configSha256"`
	ReceiptValiditySeconds   int64                           `json:"receiptValiditySeconds"`
	Signers                  []ScoutQualificationSigner      `json:"signers"`
}

type ScoutQualificationSigner struct {
	KeyID                      string `json:"keyId"`
	IdentityID                 string `json:"identityId"`
	Role                       string `json:"role"`
	TargetID                   string `json:"targetId,omitempty"`
	TargetEnvironment          string `json:"targetEnvironment,omitempty"`
	TargetRevisionSHA256       string `json:"targetRevisionSha256,omitempty"`
	PhysicalTarget             bool   `json:"physicalTarget,omitempty"`
	PublicKeyFingerprintSHA256 string `json:"publicKeyFingerprintSha256"`
}

// ScoutQualificationAuthority contains the immutable out-of-band anchors. A
// caller cannot obtain trusted output by passing raw observations alone.
type ScoutQualificationAuthority struct {
	RegistryJSON                     []byte
	RegistrySignature                []byte
	RegistryRootPublicKey            []byte
	ExpectedRootKeyFingerprintSHA256 string
	ExpectedRootKeyID                string
	ExpectedRegistrySHA256           string
	ExpectedAttemptLedgerID          string
	ExpectedReceiptLedgerID          string
	AttemptLedgerDirectory           string
	ReceiptSignerKeyID               string
	ReceiptSignerPrivateKey          []byte
}

type ScoutSignedAttestation struct {
	KeyID     string `json:"keyId"`
	PublicKey []byte `json:"-"`
	Signature []byte `json:"-"`
}

type ScoutRawEvidence struct {
	ID          string          `json:"id"`
	Source      string          `json:"source"`
	Class       string          `json:"class"`
	SignerKeyID string          `json:"signerKeyId"`
	PublicKey   []byte          `json:"-"`
	Signature   []byte          `json:"-"`
	Body        json.RawMessage `json:"-"`
	BodySHA256  string          `json:"bodySha256"`
}

type ScoutQualificationObservation struct {
	CaseID              string                 `json:"caseId"`
	AttemptID           string                 `json:"attemptId"`
	RawEvidence         []ScoutRawEvidence     `json:"-"`
	OperatorAttestation ScoutSignedAttestation `json:"-"`
	ReviewerAttestation ScoutSignedAttestation `json:"-"`
}

type ScoutUsageTotals struct {
	InputTextTokens   int64 `json:"inputTextTokens"`
	InputAudioTokens  int64 `json:"inputAudioTokens"`
	CachedTextTokens  int64 `json:"cachedTextTokens"`
	CachedAudioTokens int64 `json:"cachedAudioTokens"`
	OutputTextTokens  int64 `json:"outputTextTokens"`
	OutputAudioTokens int64 `json:"outputAudioTokens"`
	TotalTokens       int64 `json:"totalTokens"`
	CostNanoUSD       int64 `json:"costNanoUsd"`
}

type ScoutRateInterval struct {
	Numerator   int     `json:"numerator"`
	Denominator int     `json:"denominator"`
	Point       float64 `json:"point"`
	Low         float64 `json:"low"`
	High        float64 `json:"high"`
	Method      string  `json:"method"`
}

type ScoutLatencyInterval struct {
	PointMS  int64 `json:"pointMs"`
	Low95MS  int64 `json:"low95Ms"`
	High95MS int64 `json:"high95Ms"`
}

type ScoutLatencySummary struct {
	SampleCount int                  `json:"sampleCount"`
	Replicates  int                  `json:"replicates"`
	Method      string               `json:"method"`
	P50         ScoutLatencyInterval `json:"p50"`
	P95         ScoutLatencyInterval `json:"p95"`
	P99         ScoutLatencyInterval `json:"p99"`
}

type ScoutLaneQualificationReceipt struct {
	Lane                           ScoutQualificationLane    `json:"lane"`
	Surface                        ScoutQualificationSurface `json:"surface"`
	PhysicalIOSCount               int                       `json:"physicalIosCount"`
	DesktopWebCount                int                       `json:"desktopWebCount"`
	OrdinaryCount                  int                       `json:"ordinaryCount"`
	ExplicitCount                  int                       `json:"explicitCount"`
	AudienceNegativeCount          int                       `json:"audienceNegativeCount"`
	FalseResponses                 int                       `json:"falseResponses"`
	FalseResponseRate95            ScoutRateInterval         `json:"falseResponseRate95"`
	ExplicitCorrectUsefulResponses int                       `json:"explicitCorrectUsefulResponses"`
	ExplicitInvocationRecall95     ScoutRateInterval         `json:"explicitInvocationRecall95"`
	AudienceLeaks                  int                       `json:"audienceLeaks"`
	FirstUsefulAudioLatency        ScoutLatencySummary       `json:"firstUsefulAudioLatency"`
	BargeInStopLatency             ScoutLatencySummary       `json:"bargeInStopLatency"`
	AcceptedTerminalCount          int                       `json:"acceptedTerminalCount"`
	RejectedTerminalCount          int                       `json:"rejectedTerminalCount"`
	AcceptedUsage                  ScoutUsageTotals          `json:"acceptedUsage"`
	RejectedUsage                  ScoutUsageTotals          `json:"rejectedUsage"`
	Pass                           bool                      `json:"pass"`
}

type ScoutQualificationReceipt struct {
	Schema                         string                          `json:"schema"`
	EvidenceClass                  string                          `json:"evidenceClass"`
	QualificationState             string                          `json:"qualificationState"`
	Pass                           bool                            `json:"pass"`
	ReleaseSHA256                  string                          `json:"releaseSha256"`
	CandidateSHA256                string                          `json:"candidateSha256"`
	SourceArtifactSetSHA256        string                          `json:"sourceArtifactSetSha256"`
	ProviderIdentitySHA256         string                          `json:"providerIdentitySha256"`
	BillingReconciliationSHA256    string                          `json:"billingReconciliationSha256"`
	CorpusDistribution             ScoutQualificationDistribution  `json:"corpusDistribution"`
	RequiredDistribution           ScoutQualificationDistribution  `json:"requiredDistribution"`
	LaneResults                    []ScoutLaneQualificationReceipt `json:"laneResults"`
	CorpusDigest                   string                          `json:"corpusDigest"`
	ConfigDigest                   string                          `json:"configDigest"`
	RouteIdentitySHA256            string                          `json:"routeIdentitySha256"`
	PricingRevisionSHA256          string                          `json:"pricingRevisionSha256"`
	TrustRegistrySHA256            string                          `json:"trustRegistrySha256,omitempty"`
	AttemptLedgerID                string                          `json:"attemptLedgerId,omitempty"`
	ReceiptLedgerID                string                          `json:"receiptLedgerId,omitempty"`
	OrdinaryCount                  int                             `json:"ordinaryCount"`
	ExplicitCount                  int                             `json:"explicitCount"`
	AudienceNegativeCount          int                             `json:"audienceNegativeCount"`
	FalseResponses                 int                             `json:"falseResponses"`
	FalseResponseRate95            ScoutRateInterval               `json:"falseResponseRate95"`
	ExplicitCorrectUsefulResponses int                             `json:"explicitCorrectUsefulResponses"`
	ExplicitInvocationRecall95     ScoutRateInterval               `json:"explicitInvocationRecall95"`
	AudienceAudioLeaks             int                             `json:"audienceAudioLeaks"`
	AudienceTextLeaks              int                             `json:"audienceTextLeaks"`
	AudienceCardContentLeaks       int                             `json:"audienceCardContentLeaks"`
	AudienceExistenceSignalLeaks   int                             `json:"audienceExistenceSignalLeaks"`
	FirstUsefulAudioLatency        ScoutLatencySummary             `json:"firstUsefulAudioLatency"`
	BargeInStopLatency             ScoutLatencySummary             `json:"bargeInStopLatency"`
	ProviderTerminalCount          int                             `json:"providerTerminalCount"`
	AcceptedTerminalCount          int                             `json:"acceptedTerminalCount"`
	RejectedTerminalCount          int                             `json:"rejectedTerminalCount"`
	DeviceCaptureCount             int                             `json:"deviceCaptureCount"`
	Usage                          ScoutUsageTotals                `json:"usage"`
	AcceptedUsage                  ScoutUsageTotals                `json:"acceptedUsage"`
	RejectedUsage                  ScoutUsageTotals                `json:"rejectedUsage"`
	MaxTerminalTokens              int64                           `json:"maxTerminalTokens"`
	MaxTerminalCostNanoUSD         int64                           `json:"maxTerminalCostNanoUsd"`
	MaxAcceptedTokens              int64                           `json:"maxAcceptedTokens"`
	MaxAcceptedCostNanoUSD         int64                           `json:"maxAcceptedCostNanoUsd"`
	MaxRejectedTokens              int64                           `json:"maxRejectedTokens"`
	MaxRejectedCostNanoUSD         int64                           `json:"maxRejectedCostNanoUsd"`
	MaxRejectedTerminalCount       int                             `json:"maxRejectedTerminalCount"`
	RawEvidenceSetSHA256           string                          `json:"rawEvidenceSetSha256"`
	EvaluatorRevisionSHA256        string                          `json:"evaluatorRevisionSha256"`
	ReceiptSHA256                  string                          `json:"receiptSha256"`
	ReceiptSignerKeyID             string                          `json:"receiptSignerKeyId,omitempty"`
	ReceiptSignerPublicKey         []byte                          `json:"receiptSignerPublicKey,omitempty"`
	ReceiptSignature               []byte                          `json:"receiptSignature,omitempty"`
	CreatedAt                      string                          `json:"createdAt"`
	ExpiresAt                      string                          `json:"expiresAt,omitempty"`
}

type scoutProviderUsage struct {
	InputTextTokens   *int64 `json:"input_text_tokens"`
	InputAudioTokens  *int64 `json:"input_audio_tokens"`
	CachedTextTokens  *int64 `json:"cached_text_tokens"`
	CachedAudioTokens *int64 `json:"cached_audio_tokens"`
	OutputTextTokens  *int64 `json:"output_text_tokens"`
	OutputAudioTokens *int64 `json:"output_audio_tokens"`
	TotalTokens       *int64 `json:"total_tokens"`
}

type scoutProviderTerminal struct {
	Type                     string                          `json:"type"`
	CaseID                   string                          `json:"case_id"`
	AttemptID                string                          `json:"attempt_id"`
	Lane                     ScoutQualificationLane          `json:"lane"`
	Surface                  ScoutQualificationSurface       `json:"surface"`
	Platform                 ScoutQualificationPlatform      `json:"platform"`
	InputAudioSHA256         string                          `json:"input_audio_sha256"`
	CorpusSHA256             string                          `json:"corpus_sha256"`
	ConfigSHA256             string                          `json:"config_sha256"`
	CandidateSHA256          string                          `json:"candidate_sha256"`
	SourceArtifactSetSHA256  string                          `json:"source_artifact_set_sha256"`
	ReleaseSHA256            string                          `json:"release_sha256"`
	RegistrySHA256           string                          `json:"registry_sha256"`
	ProviderCallID           string                          `json:"provider_call_id"`
	Status                   string                          `json:"status"`
	AcceptedOutput           bool                            `json:"accepted_output"`
	ProviderIdentity         ScoutProviderIdentity           `json:"provider_identity"`
	BillingReconciliation    ScoutBillingReconciliationRoute `json:"billing_reconciliation"`
	AudioPublished           bool                            `json:"audio_published"`
	TextPublished            bool                            `json:"text_published"`
	CardContentPublished     bool                            `json:"card_content_published"`
	ExistenceSignalPublished bool                            `json:"existence_signal_published"`
	Usage                    scoutProviderUsage              `json:"usage"`
	CostNanoUSD              *int64                          `json:"cost_nano_usd"`
}

type scoutBillingReconciliation struct {
	Type                    string                          `json:"type"`
	CaseID                  string                          `json:"case_id"`
	AttemptID               string                          `json:"attempt_id"`
	Lane                    ScoutQualificationLane          `json:"lane"`
	Surface                 ScoutQualificationSurface       `json:"surface"`
	Platform                ScoutQualificationPlatform      `json:"platform"`
	ProviderCallID          string                          `json:"provider_call_id"`
	ProviderIdentity        ScoutProviderIdentity           `json:"provider_identity"`
	BillingReconciliation   ScoutBillingReconciliationRoute `json:"billing_reconciliation"`
	CorpusSHA256            string                          `json:"corpus_sha256"`
	ConfigSHA256            string                          `json:"config_sha256"`
	CandidateSHA256         string                          `json:"candidate_sha256"`
	SourceArtifactSetSHA256 string                          `json:"source_artifact_set_sha256"`
	ReleaseSHA256           string                          `json:"release_sha256"`
	RegistrySHA256          string                          `json:"registry_sha256"`
	Status                  string                          `json:"status"`
	AcceptedOutput          bool                            `json:"accepted_output"`
	Usage                   scoutProviderUsage              `json:"usage"`
	CostNanoUSD             *int64                          `json:"cost_nano_usd"`
}

type scoutDeviceCapture struct {
	Type                    string                     `json:"type"`
	TargetID                string                     `json:"target_id"`
	CaseID                  string                     `json:"case_id"`
	AttemptID               string                     `json:"attempt_id"`
	Lane                    ScoutQualificationLane     `json:"lane"`
	Surface                 ScoutQualificationSurface  `json:"surface"`
	Platform                ScoutQualificationPlatform `json:"platform"`
	InputAudioSHA256        string                     `json:"input_audio_sha256"`
	CorpusSHA256            string                     `json:"corpus_sha256"`
	ConfigSHA256            string                     `json:"config_sha256"`
	CandidateSHA256         string                     `json:"candidate_sha256"`
	SourceArtifactSetSHA256 string                     `json:"source_artifact_set_sha256"`
	ReleaseSHA256           string                     `json:"release_sha256"`
	RegistrySHA256          string                     `json:"registry_sha256"`
	AudioObserved           bool                       `json:"audio_observed"`
	TextObserved            bool                       `json:"text_observed"`
	CardContentObserved     bool                       `json:"card_content_observed"`
	ExistenceSignalObserved bool                       `json:"existence_signal_observed"`
	ResponseCorrect         bool                       `json:"response_correct"`
	ResponseUseful          bool                       `json:"response_useful"`
	AddressCompletedMS      *int64                     `json:"address_completed_ms"`
	FirstUsefulAudioMS      *int64                     `json:"first_useful_audio_ms"`
	BargeInDetectedMS       *int64                     `json:"barge_in_detected_ms"`
	AudioStoppedMS          *int64                     `json:"audio_stopped_ms"`
}

type scoutEvaluationMode struct {
	trusted        bool
	registry       ScoutQualificationTrustRegistry
	registryDigest string
	authority      *ScoutQualificationAuthority
}

type scoutLaneEvaluation struct {
	receipt    ScoutLaneQualificationReceipt
	firstAudio []int64
	bargeStop  []int64
}

// EvaluateScoutQualification performs only local, untrusted evaluation. Its
// receipt can never be interpreted as provider-live evidence.
func EvaluateScoutQualification(corpus ScoutQualificationCorpus, config ScoutQualificationConfig, observations []ScoutQualificationObservation, now time.Time) (ScoutQualificationReceipt, error) {
	return evaluateScoutQualification(corpus, config, observations, now, scoutEvaluationMode{})
}

// EvaluateTrustedScoutQualification is the sole provider-live candidate path.
// The trust anchor and ledger namespace must come from operator-owned secure
// configuration, not from the evidence packet itself.
func EvaluateTrustedScoutQualification(corpus ScoutQualificationCorpus, config ScoutQualificationConfig, observations []ScoutQualificationObservation, authority ScoutQualificationAuthority, now time.Time) (ScoutQualificationReceipt, error) {
	registry, registryDigest, err := verifyScoutAuthority(corpus, config, authority)
	if err != nil {
		return ScoutQualificationReceipt{}, err
	}
	return evaluateScoutQualification(corpus, config, observations, now, scoutEvaluationMode{trusted: true, registry: registry, registryDigest: registryDigest, authority: &authority})
}

func evaluateScoutQualification(corpus ScoutQualificationCorpus, config ScoutQualificationConfig, observations []ScoutQualificationObservation, now time.Time, mode scoutEvaluationMode) (ScoutQualificationReceipt, error) {
	if corpus.Validate() != nil || config.Validate() != nil || corpus.ReleaseSHA256 != config.ReleaseSHA256 || !scoutDistributionSatisfies(corpus.Distribution, config.RequiredDistribution) || len(observations) != len(corpus.Cases) || now.IsZero() {
		return ScoutQualificationReceipt{}, ErrScoutQualificationInvalid
	}
	byCase := make(map[string]ScoutQualificationObservation, len(observations))
	attempts := make(map[string]struct{}, len(observations))
	for _, observation := range observations {
		if observation.CaseID == "" || observation.AttemptID == "" || len(observation.RawEvidence) != 3 {
			return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
		}
		if _, exists := byCase[observation.CaseID]; exists {
			return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
		}
		if _, exists := attempts[observation.AttemptID]; exists {
			return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
		}
		byCase[observation.CaseID] = observation
		attempts[observation.AttemptID] = struct{}{}
	}

	receipt := ScoutQualificationReceipt{
		Schema: "stride.e10.scout-qualification-receipt/v3", EvidenceClass: "local_untrusted_evaluation",
		QualificationState: "local_deterministic_evaluation_only", ReleaseSHA256: corpus.ReleaseSHA256,
		CandidateSHA256: config.CandidateSHA256, SourceArtifactSetSHA256: config.SourceArtifactSetSHA256, ProviderIdentitySHA256: scoutProviderIdentityDigest(config.ProviderIdentity), BillingReconciliationSHA256: scoutBillingReconciliationDigest(config.BillingReconciliation),
		CorpusDistribution: corpus.Distribution, RequiredDistribution: config.RequiredDistribution,
		CorpusDigest: corpus.Digest, ConfigDigest: config.Digest, RouteIdentitySHA256: config.RouteIdentitySHA256,
		PricingRevisionSHA256: config.PricingRevisionSHA256, EvaluatorRevisionSHA256: digest("stride.e10.scout-qualification-evaluator/v3"),
		MaxTerminalTokens: config.MaxTerminalTokens, MaxTerminalCostNanoUSD: config.MaxTerminalCostNanoUSD,
		MaxAcceptedTokens: config.MaxAcceptedTokens, MaxAcceptedCostNanoUSD: config.MaxAcceptedCostNanoUSD,
		MaxRejectedTokens: config.MaxRejectedTokens, MaxRejectedCostNanoUSD: config.MaxRejectedCostNanoUSD, MaxRejectedTerminalCount: config.MaxRejectedTerminalCount,
		CreatedAt: now.UTC().Format(time.RFC3339Nano),
	}
	if mode.trusted {
		receipt.EvidenceClass = "provider_live_evidence_candidate"
		receipt.QualificationState = "trusted_authority_bound_deterministic_evaluation"
		receipt.TrustRegistrySHA256 = mode.registryDigest
		receipt.AttemptLedgerID = mode.registry.AttemptLedgerID
		receipt.ReceiptLedgerID = mode.registry.ReceiptLedgerID
	}
	providerCalls := make(map[string]struct{})
	evidenceIDs := make(map[string]struct{})
	evidenceBodies := make(map[string]struct{})
	evidenceDigests := make([]string, 0, len(observations)*3)
	attemptIDs := make([]string, 0, len(observations))
	firstAudio, bargeStop := make([]int64, 0, ScoutExplicitMinimum), make([]int64, 0, ScoutExplicitMinimum)
	laneEvaluations := map[ScoutQualificationLane]*scoutLaneEvaluation{
		ScoutLanePersonal: {receipt: ScoutLaneQualificationReceipt{Lane: ScoutLanePersonal, Surface: ScoutSurfacePrivateVoice}},
		ScoutLaneMeeting:  {receipt: ScoutLaneQualificationReceipt{Lane: ScoutLaneMeeting, Surface: ScoutSurfaceMeetingVoice}},
	}
	for _, testCase := range corpus.Cases {
		observation, found := byCase[testCase.ID]
		if !found {
			return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
		}
		terminal, device, digests, err := parseScoutEvidence(testCase, observation, corpus, config, mode, providerCalls, evidenceIDs, evidenceBodies)
		if err != nil {
			return ScoutQualificationReceipt{}, err
		}
		evidenceDigests = append(evidenceDigests, digests...)
		attemptIDs = append(attemptIDs, observation.AttemptID)
		usage, err := reconcileScoutTerminalUsage(terminal, config)
		if err != nil {
			return ScoutQualificationReceipt{}, err
		}
		receipt.ProviderTerminalCount++
		receipt.DeviceCaptureCount++
		if !addScoutUsage(&receipt.Usage, usage) {
			return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
		}
		if usage.TotalTokens > config.MaxTerminalTokens || usage.CostNanoUSD > config.MaxTerminalCostNanoUSD {
			return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
		}
		if terminal.AcceptedOutput {
			receipt.AcceptedTerminalCount++
			if !addScoutUsage(&receipt.AcceptedUsage, usage) {
				return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
			}
		} else {
			receipt.RejectedTerminalCount++
			if !addScoutUsage(&receipt.RejectedUsage, usage) {
				return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
			}
		}
		laneEvaluation := laneEvaluations[testCase.Lane]
		if laneEvaluation == nil {
			return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
		}
		if testCase.Platform == ScoutPlatformPhysicalIOS {
			laneEvaluation.receipt.PhysicalIOSCount++
		} else {
			laneEvaluation.receipt.DesktopWebCount++
		}
		if terminal.AcceptedOutput {
			laneEvaluation.receipt.AcceptedTerminalCount++
			if !addScoutUsage(&laneEvaluation.receipt.AcceptedUsage, usage) {
				return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
			}
		} else {
			laneEvaluation.receipt.RejectedTerminalCount++
			if !addScoutUsage(&laneEvaluation.receipt.RejectedUsage, usage) {
				return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
			}
		}
		published := terminal.AudioPublished || terminal.TextPublished || terminal.CardContentPublished || terminal.ExistenceSignalPublished
		switch testCase.Class {
		case ScoutCorpusOrdinary:
			receipt.OrdinaryCount++
			laneEvaluation.receipt.OrdinaryCount++
			if published {
				receipt.FalseResponses++
				laneEvaluation.receipt.FalseResponses++
			}
		case ScoutCorpusExplicit:
			receipt.ExplicitCount++
			laneEvaluation.receipt.ExplicitCount++
			if terminal.AudioPublished {
				if device.FirstUsefulAudioMS == nil || device.BargeInDetectedMS == nil || device.AudioStoppedMS == nil || *device.FirstUsefulAudioMS < *device.AddressCompletedMS || *device.AudioStoppedMS < *device.BargeInDetectedMS {
					return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
				}
				firstAudio = append(firstAudio, *device.FirstUsefulAudioMS-*device.AddressCompletedMS)
				bargeStop = append(bargeStop, *device.AudioStoppedMS-*device.BargeInDetectedMS)
				laneEvaluation.firstAudio = append(laneEvaluation.firstAudio, *device.FirstUsefulAudioMS-*device.AddressCompletedMS)
				laneEvaluation.bargeStop = append(laneEvaluation.bargeStop, *device.AudioStoppedMS-*device.BargeInDetectedMS)
				if device.ResponseCorrect && device.ResponseUseful {
					receipt.ExplicitCorrectUsefulResponses++
					laneEvaluation.receipt.ExplicitCorrectUsefulResponses++
				}
			}
		case ScoutCorpusAudienceNegative:
			receipt.AudienceNegativeCount++
			laneEvaluation.receipt.AudienceNegativeCount++
			if terminal.AudioPublished {
				receipt.AudienceAudioLeaks++
				laneEvaluation.receipt.AudienceLeaks++
			}
			if terminal.TextPublished {
				receipt.AudienceTextLeaks++
				laneEvaluation.receipt.AudienceLeaks++
			}
			if terminal.CardContentPublished {
				receipt.AudienceCardContentLeaks++
				laneEvaluation.receipt.AudienceLeaks++
			}
			if terminal.ExistenceSignalPublished {
				receipt.AudienceExistenceSignalLeaks++
				laneEvaluation.receipt.AudienceLeaks++
			}
		}
	}
	if receipt.OrdinaryCount < ScoutOrdinaryMinimum || receipt.ExplicitCount < ScoutExplicitMinimum || receipt.AudienceNegativeCount < ScoutAudienceNegativeMinimum {
		return ScoutQualificationReceipt{}, ErrScoutQualificationEvidence
	}
	sort.Strings(evidenceDigests)
	receipt.RawEvidenceSetSHA256 = digest(strings.Join(evidenceDigests, "\n"))
	receipt.FalseResponseRate95 = scoutWilson95(receipt.FalseResponses, receipt.OrdinaryCount)
	receipt.ExplicitInvocationRecall95 = scoutWilson95(receipt.ExplicitCorrectUsefulResponses, receipt.ExplicitCount)
	seed := receipt.RawEvidenceSetSHA256 + ":" + corpus.Digest + ":" + config.Digest
	receipt.FirstUsefulAudioLatency = scoutLatencySummary(firstAudio, seed+":first-useful-audio")
	receipt.BargeInStopLatency = scoutLatencySummary(bargeStop, seed+":barge-stop")
	for _, lane := range []ScoutQualificationLane{ScoutLanePersonal, ScoutLaneMeeting} {
		laneEvaluation := laneEvaluations[lane]
		laneSeed := seed + ":" + string(lane)
		laneEvaluation.receipt.FalseResponseRate95 = scoutWilson95(laneEvaluation.receipt.FalseResponses, laneEvaluation.receipt.OrdinaryCount)
		laneEvaluation.receipt.ExplicitInvocationRecall95 = scoutWilson95(laneEvaluation.receipt.ExplicitCorrectUsefulResponses, laneEvaluation.receipt.ExplicitCount)
		laneEvaluation.receipt.FirstUsefulAudioLatency = scoutLatencySummary(laneEvaluation.firstAudio, laneSeed+":first-useful-audio")
		laneEvaluation.receipt.BargeInStopLatency = scoutLatencySummary(laneEvaluation.bargeStop, laneSeed+":barge-stop")
		laneEvaluation.receipt.Pass = scoutLanePass(laneEvaluation.receipt, config.RequiredDistribution)
		receipt.LaneResults = append(receipt.LaneResults, laneEvaluation.receipt)
	}
	accountingWithinCeilings := receipt.AcceptedUsage.TotalTokens <= config.MaxAcceptedTokens && receipt.AcceptedUsage.CostNanoUSD <= config.MaxAcceptedCostNanoUSD && receipt.RejectedUsage.TotalTokens <= config.MaxRejectedTokens && receipt.RejectedUsage.CostNanoUSD <= config.MaxRejectedCostNanoUSD && receipt.RejectedTerminalCount <= config.MaxRejectedTerminalCount
	receipt.Pass = accountingWithinCeilings && receipt.LaneResults[0].Pass && receipt.LaneResults[1].Pass && receipt.FalseResponseRate95.High <= ScoutMaxFalseResponseRate && receipt.ExplicitInvocationRecall95.Low >= ScoutMinExplicitRecall && receipt.AudienceAudioLeaks == 0 && receipt.AudienceTextLeaks == 0 && receipt.AudienceCardContentLeaks == 0 && receipt.AudienceExistenceSignalLeaks == 0 && len(firstAudio) == receipt.ExplicitCorrectUsefulResponses && receipt.FirstUsefulAudioLatency.P95.High95MS <= ScoutMaxFirstUsefulAudioMS && receipt.BargeInStopLatency.P95.High95MS <= ScoutMaxBargeInStopMS
	if mode.trusted {
		sort.Strings(attemptIDs)
		if err := claimScoutAttempts(*mode.authority, mode.registryDigest, attemptIDs); err != nil {
			return ScoutQualificationReceipt{}, err
		}
	}
	if mode.trusted {
		receipt.ExpiresAt = now.UTC().Add(time.Duration(mode.registry.ReceiptValiditySeconds) * time.Second).Format(time.RFC3339Nano)
		receipt.ReceiptSignerKeyID = mode.authority.ReceiptSignerKeyID
		receipt.ReceiptSignerPublicKey = ed25519.PrivateKey(mode.authority.ReceiptSignerPrivateKey).Public().(ed25519.PublicKey)
	}
	digest, err := scoutReceiptDigest(receipt)
	if err != nil {
		return ScoutQualificationReceipt{}, err
	}
	receipt.ReceiptSHA256 = digest
	if mode.trusted {
		signature, err := signScoutQualificationReceipt(receipt, *mode.authority, mode.registry)
		if err != nil {
			return ScoutQualificationReceipt{}, err
		}
		receipt.ReceiptSignature = signature
	}
	if !receipt.Pass {
		return receipt, ErrScoutQualificationFailed
	}
	return receipt, nil
}

func scoutLanePass(receipt ScoutLaneQualificationReceipt, required ScoutQualificationDistribution) bool {
	requiredIOS, requiredWeb := 0, 0
	switch receipt.Lane {
	case ScoutLanePersonal:
		if receipt.Surface != ScoutSurfacePrivateVoice {
			return false
		}
		requiredIOS, requiredWeb = required.PersonalPhysicalIOS, required.PersonalDesktopWeb
	case ScoutLaneMeeting:
		if receipt.Surface != ScoutSurfaceMeetingVoice {
			return false
		}
		requiredIOS, requiredWeb = required.MeetingPhysicalIOS, required.MeetingDesktopWeb
	default:
		return false
	}
	return receipt.PhysicalIOSCount >= requiredIOS && receipt.DesktopWebCount >= requiredWeb && receipt.OrdinaryCount >= ScoutLaneOrdinaryMinimum && receipt.ExplicitCount >= ScoutLaneExplicitMinimum && receipt.AudienceNegativeCount >= ScoutLaneAudienceMinimum && receipt.RejectedTerminalCount == 0 && receipt.FalseResponseRate95.High <= ScoutMaxFalseResponseRate && receipt.ExplicitInvocationRecall95.Low >= ScoutMinExplicitRecall && receipt.AudienceLeaks == 0 && receipt.FirstUsefulAudioLatency.SampleCount == receipt.ExplicitCorrectUsefulResponses && receipt.FirstUsefulAudioLatency.P95.High95MS <= ScoutMaxFirstUsefulAudioMS && receipt.BargeInStopLatency.P95.High95MS <= ScoutMaxBargeInStopMS
}

// WriteScoutQualificationReceipt publishes only a local/untrusted receipt. A
// provider-live candidate must use WriteTrustedScoutQualificationReceipt so a
// self-hash and signature-shaped bytes cannot be promoted as authenticated.
func WriteScoutQualificationReceipt(path string, receipt ScoutQualificationReceipt) error {
	if receipt.EvidenceClass == "provider_live_evidence_candidate" {
		return ErrScoutQualificationInvalid
	}
	return writeScoutQualificationReceipt(path, receipt)
}

func WriteTrustedScoutQualificationReceipt(path string, receipt ScoutQualificationReceipt, authority ScoutQualificationAuthority, now time.Time) error {
	if err := VerifyTrustedScoutQualificationReceipt(receipt, authority, now); err != nil {
		return err
	}
	return writeScoutQualificationReceipt(path, receipt)
}

func writeScoutQualificationReceipt(path string, receipt ScoutQualificationReceipt) error {
	if !filepath.IsAbs(path) || receipt.Schema != "stride.e10.scout-qualification-receipt/v3" || !validDigest(receipt.ReceiptSHA256) {
		return ErrScoutQualificationInvalid
	}
	if receipt.EvidenceClass == "provider_live_evidence_candidate" && (receipt.ReceiptSignerKeyID == "" || len(receipt.ReceiptSignerPublicKey) != ed25519.PublicKeySize || len(receipt.ReceiptSignature) != ed25519.SignatureSize || receipt.ExpiresAt == "") {
		return ErrScoutQualificationInvalid
	}
	if digest, err := scoutReceiptDigest(receipt); err != nil || digest != receipt.ReceiptSHA256 {
		return ErrScoutQualificationInvalid
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return writeScoutFileExclusive(path, append(encoded, '\n'))
}

func signScoutQualificationReceipt(receipt ScoutQualificationReceipt, authority ScoutQualificationAuthority, registry ScoutQualificationTrustRegistry) ([]byte, error) {
	if verifyScoutReceiptPrivateKey(authority, registry) != nil {
		return nil, ErrScoutQualificationEvidence
	}
	payload, err := scoutReceiptSignaturePayload(receipt)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(ed25519.PrivateKey(authority.ReceiptSignerPrivateKey), payload), nil
}

// VerifyTrustedScoutQualificationReceipt authenticates a redacted final
// candidate receipt against the independently root-signed registry and its
// dedicated receipt attestor. It does not turn the candidate evidence class
// into a qualified/provider-approved state.
func VerifyTrustedScoutQualificationReceipt(receipt ScoutQualificationReceipt, authority ScoutQualificationAuthority, now time.Time) error {
	if now.IsZero() || receipt.Schema != "stride.e10.scout-qualification-receipt/v3" || receipt.EvidenceClass != "provider_live_evidence_candidate" || receipt.QualificationState != "trusted_authority_bound_deterministic_evaluation" || !validDigest(receipt.ReceiptSHA256) || receipt.ReceiptSignerKeyID == "" || len(receipt.ReceiptSignerPublicKey) != ed25519.PublicKeySize || len(receipt.ReceiptSignature) != ed25519.SignatureSize {
		return ErrScoutQualificationEvidence
	}
	registry, registryDigest, err := verifyScoutReceiptRegistry(authority)
	if err != nil {
		return err
	}
	createdAt, createdErr := time.Parse(time.RFC3339Nano, receipt.CreatedAt)
	expiresAt, expiresErr := time.Parse(time.RFC3339Nano, receipt.ExpiresAt)
	if createdErr != nil || expiresErr != nil || !expiresAt.After(createdAt) || expiresAt.Sub(createdAt) != time.Duration(registry.ReceiptValiditySeconds)*time.Second || now.UTC().Before(createdAt) {
		return ErrScoutQualificationEvidence
	}
	if !now.UTC().Before(expiresAt) {
		return ErrScoutReceiptExpired
	}
	if receipt.TrustRegistrySHA256 != registryDigest || receipt.ReleaseSHA256 != registry.ReleaseSHA256 || receipt.CandidateSHA256 != registry.CandidateSHA256 || receipt.SourceArtifactSetSHA256 != registry.SourceArtifactSetSHA256 || receipt.ProviderIdentitySHA256 != scoutProviderIdentityDigest(registry.ProviderIdentity) || receipt.BillingReconciliationSHA256 != scoutBillingReconciliationDigest(registry.BillingReconciliation) || receipt.CorpusDistribution != registry.CorpusDistribution || receipt.RequiredDistribution != registry.RequiredDistribution || receipt.CorpusDigest != registry.CorpusSHA256 || receipt.ConfigDigest != registry.ConfigSHA256 || receipt.AttemptLedgerID != registry.AttemptLedgerID || receipt.ReceiptLedgerID != registry.ReceiptLedgerID {
		return ErrScoutQualificationEvidence
	}
	if !validScoutReceiptLaneResults(receipt) {
		return ErrScoutQualificationEvidence
	}
	digest, digestErr := scoutReceiptDigest(receipt)
	if digestErr != nil || digest != receipt.ReceiptSHA256 {
		return ErrScoutQualificationEvidence
	}
	payload, payloadErr := scoutReceiptSignaturePayload(receipt)
	if payloadErr != nil || verifyScoutRegistrySignature(receipt.ReceiptSignerKeyID, receipt.ReceiptSignerPublicKey, receipt.ReceiptSignature, payload, registry, "qualification_receipt_attestor", "") != nil {
		return ErrScoutQualificationEvidence
	}
	if !receipt.Pass {
		return ErrScoutQualificationFailed
	}
	return nil
}

// ConsumeTrustedScoutQualificationReceipt is the downstream gate: it first
// verifies the expiring root-anchored receipt, then durably consumes that exact
// receipt once in the independently named receipt-ledger namespace.
func ConsumeTrustedScoutQualificationReceipt(receipt ScoutQualificationReceipt, authority ScoutQualificationAuthority, now time.Time) error {
	if err := VerifyTrustedScoutQualificationReceipt(receipt, authority, now); err != nil {
		return err
	}
	registry, registryDigest, err := verifyScoutReceiptRegistry(authority)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(authority.AttemptLedgerDirectory) {
		return ErrScoutQualificationEvidence
	}
	ledgerPath, err := filepath.EvalSymlinks(authority.AttemptLedgerDirectory)
	if err != nil || registry.ReceiptLedgerPathSHA256 != digest(filepath.Clean(ledgerPath)) {
		return ErrScoutQualificationEvidence
	}
	if info, statErr := os.Stat(authority.AttemptLedgerDirectory); statErr != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return ErrScoutQualificationEvidence
	}
	claim := struct {
		Schema          string `json:"schema"`
		ReceiptLedgerID string `json:"receiptLedgerId"`
		RegistrySHA256  string `json:"registrySha256"`
		ReceiptSHA256   string `json:"receiptSha256"`
		ConsumedAt      string `json:"consumedAt"`
	}{"stride.e10.scout-qualification-receipt-claim/v1", registry.ReceiptLedgerID, registryDigest, receipt.ReceiptSHA256, now.UTC().Format(time.RFC3339Nano)}
	body, err := json.Marshal(claim)
	if err != nil {
		return err
	}
	path := filepath.Join(authority.AttemptLedgerDirectory, "."+digest(registry.ReceiptLedgerID+"\x00"+receipt.ReceiptSHA256)+".receipt-claim.json")
	if err := writeScoutFileExclusive(path, append(body, '\n')); err != nil {
		if os.IsExist(err) {
			return ErrScoutReceiptReused
		}
		return err
	}
	return nil
}

func validScoutReceiptLaneResults(receipt ScoutQualificationReceipt) bool {
	if len(receipt.LaneResults) != 2 || receipt.LaneResults[0].Lane != ScoutLanePersonal || receipt.LaneResults[0].Surface != ScoutSurfacePrivateVoice || receipt.LaneResults[1].Lane != ScoutLaneMeeting || receipt.LaneResults[1].Surface != ScoutSurfaceMeetingVoice || !receipt.LaneResults[0].Pass || !receipt.LaneResults[1].Pass || !scoutDistributionSatisfies(receipt.CorpusDistribution, receipt.RequiredDistribution) {
		return false
	}
	personal, meeting := receipt.LaneResults[0], receipt.LaneResults[1]
	if personal.PhysicalIOSCount != receipt.CorpusDistribution.PersonalPhysicalIOS || personal.DesktopWebCount != receipt.CorpusDistribution.PersonalDesktopWeb || meeting.PhysicalIOSCount != receipt.CorpusDistribution.MeetingPhysicalIOS || meeting.DesktopWebCount != receipt.CorpusDistribution.MeetingDesktopWeb || personal.OrdinaryCount+meeting.OrdinaryCount != receipt.OrdinaryCount || personal.ExplicitCount+meeting.ExplicitCount != receipt.ExplicitCount || personal.AudienceNegativeCount+meeting.AudienceNegativeCount != receipt.AudienceNegativeCount || personal.AcceptedTerminalCount+meeting.AcceptedTerminalCount != receipt.AcceptedTerminalCount || personal.RejectedTerminalCount+meeting.RejectedTerminalCount != receipt.RejectedTerminalCount {
		return false
	}
	accepted, rejected := personal.AcceptedUsage, personal.RejectedUsage
	if !addScoutUsage(&accepted, meeting.AcceptedUsage) || !addScoutUsage(&rejected, meeting.RejectedUsage) {
		return false
	}
	return accepted == receipt.AcceptedUsage && rejected == receipt.RejectedUsage
}

func verifyScoutReceiptRegistry(authority ScoutQualificationAuthority) (ScoutQualificationTrustRegistry, string, error) {
	var registry ScoutQualificationTrustRegistry
	if len(authority.RegistryJSON) == 0 || len(authority.RegistryJSON) > maxScoutQualificationRawSize || !validDigest(authority.ExpectedRootKeyFingerprintSHA256) || !validDigest(authority.ExpectedRegistrySHA256) || authority.ExpectedRootKeyID == "" || authority.ExpectedAttemptLedgerID == "" || authority.ExpectedReceiptLedgerID == "" || decodeScoutRaw(authority.RegistryJSON, &registry) != nil {
		return registry, "", ErrScoutQualificationEvidence
	}
	canonical, err := json.Marshal(registry)
	registryDigest := digestBytes(authority.RegistryJSON)
	rootFingerprint := digestBytes(authority.RegistryRootPublicKey)
	if err != nil || !bytes.Equal(canonical, authority.RegistryJSON) || registryDigest != authority.ExpectedRegistrySHA256 || rootFingerprint != authority.ExpectedRootKeyFingerprintSHA256 || len(authority.RegistryRootPublicKey) != ed25519.PublicKeySize || len(authority.RegistrySignature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(authority.RegistryRootPublicKey), authority.RegistryJSON, authority.RegistrySignature) {
		return registry, "", ErrScoutQualificationEvidence
	}
	if registry.Schema != "stride.e10.scout-qualification-target-registry/v2" || registry.RegistryID == "" || registry.AttemptLedgerID != authority.ExpectedAttemptLedgerID || registry.ReceiptLedgerID != authority.ExpectedReceiptLedgerID || registry.RootKeyID != authority.ExpectedRootKeyID || registry.RootKeyFingerprintSHA256 != rootFingerprint || registry.ReceiptValiditySeconds < 60 || registry.ReceiptValiditySeconds > 24*60*60 || !validScoutProviderIdentity(registry.ProviderIdentity) || !validScoutBillingReconciliation(registry.BillingReconciliation) || !validScoutRequiredDistribution(registry.RequiredDistribution) || !scoutDistributionSatisfies(registry.CorpusDistribution, registry.RequiredDistribution) {
		return registry, "", ErrScoutQualificationEvidence
	}
	return registry, registryDigest, nil
}

func scoutReceiptSignaturePayload(receipt ScoutQualificationReceipt) ([]byte, error) {
	if !validDigest(receipt.ReceiptSHA256) || receipt.ReceiptSignerKeyID == "" || receipt.ExpiresAt == "" {
		return nil, ErrScoutQualificationEvidence
	}
	return json.Marshal(struct {
		Schema              string `json:"schema"`
		ReceiptSHA256       string `json:"receiptSha256"`
		TrustRegistrySHA256 string `json:"trustRegistrySha256"`
		ReceiptSignerKeyID  string `json:"receiptSignerKeyId"`
		ExpiresAt           string `json:"expiresAt"`
	}{"stride.e10.scout-qualification-receipt-signature/v1", receipt.ReceiptSHA256, receipt.TrustRegistrySHA256, receipt.ReceiptSignerKeyID, receipt.ExpiresAt})
}

func verifyScoutAuthority(corpus ScoutQualificationCorpus, config ScoutQualificationConfig, authority ScoutQualificationAuthority) (ScoutQualificationTrustRegistry, string, error) {
	var registry ScoutQualificationTrustRegistry
	if len(authority.RegistryJSON) == 0 || len(authority.RegistryJSON) > maxScoutQualificationRawSize || !validDigest(authority.ExpectedRootKeyFingerprintSHA256) || !validDigest(authority.ExpectedRegistrySHA256) || authority.ExpectedRootKeyID == "" || authority.ExpectedAttemptLedgerID == "" || authority.ExpectedReceiptLedgerID == "" || !filepath.IsAbs(authority.AttemptLedgerDirectory) || authority.ReceiptSignerKeyID == "" || len(authority.ReceiptSignerPrivateKey) != ed25519.PrivateKeySize {
		return registry, "", ErrScoutQualificationEvidence
	}
	if decodeScoutRaw(authority.RegistryJSON, &registry) != nil {
		return registry, "", ErrScoutQualificationEvidence
	}
	canonical, err := json.Marshal(registry)
	if err != nil || !bytes.Equal(canonical, authority.RegistryJSON) {
		return registry, "", ErrScoutQualificationEvidence
	}
	registryDigest := digestBytes(authority.RegistryJSON)
	rootFingerprint := digestBytes(authority.RegistryRootPublicKey)
	ledgerPath, err := filepath.EvalSymlinks(authority.AttemptLedgerDirectory)
	if err != nil {
		return registry, "", ErrScoutQualificationEvidence
	}
	if registryDigest != authority.ExpectedRegistrySHA256 || rootFingerprint != authority.ExpectedRootKeyFingerprintSHA256 || len(authority.RegistryRootPublicKey) != ed25519.PublicKeySize || len(authority.RegistrySignature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(authority.RegistryRootPublicKey), authority.RegistryJSON, authority.RegistrySignature) {
		return registry, "", ErrScoutQualificationEvidence
	}
	if registry.Schema != "stride.e10.scout-qualification-target-registry/v2" || registry.RegistryID == "" || registry.AttemptLedgerID != authority.ExpectedAttemptLedgerID || registry.ReceiptLedgerID != authority.ExpectedReceiptLedgerID || registry.AttemptLedgerPathSHA256 != digest(filepath.Clean(ledgerPath)) || registry.ReceiptLedgerPathSHA256 != digest(filepath.Clean(ledgerPath)) || registry.RootKeyID != authority.ExpectedRootKeyID || registry.RootKeyFingerprintSHA256 != rootFingerprint || registry.ReleaseSHA256 != corpus.ReleaseSHA256 || registry.ReleaseSHA256 != config.ReleaseSHA256 || registry.CandidateSHA256 != config.CandidateSHA256 || registry.SourceArtifactSetSHA256 != config.SourceArtifactSetSHA256 || registry.ProviderIdentity != config.ProviderIdentity || registry.BillingReconciliation != config.BillingReconciliation || registry.RequiredDistribution != config.RequiredDistribution || registry.CorpusDistribution != corpus.Distribution || registry.CorpusSHA256 != corpus.Digest || registry.ConfigSHA256 != config.Digest || registry.ReceiptValiditySeconds < 60 || registry.ReceiptValiditySeconds > 24*60*60 {
		return registry, "", ErrScoutQualificationEvidence
	}
	if info, err := os.Stat(authority.AttemptLedgerDirectory); err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return registry, "", ErrScoutQualificationEvidence
	}
	seenKey, seenFingerprint := map[string]bool{}, map[string]bool{}
	roles := map[string]int{}
	for _, signer := range registry.Signers {
		if signer.KeyID == "" || signer.IdentityID == "" || !validDigest(signer.PublicKeyFingerprintSHA256) || seenKey[signer.KeyID] || seenFingerprint[signer.PublicKeyFingerprintSHA256] {
			return registry, "", ErrScoutQualificationEvidence
		}
		if signer.Role != "provider_attempt_attestor" && signer.Role != "device_evidence_attestor" && signer.Role != "billing_reconciliation_attestor" && signer.Role != "qualification_receipt_attestor" && signer.Role != "operator" && signer.Role != "independent_reviewer" {
			return registry, "", ErrScoutQualificationEvidence
		}
		isDevice := signer.Role == "device_evidence_attestor"
		if isDevice != (signer.TargetID != "") || isDevice != (signer.TargetEnvironment != "") || isDevice != validDigest(signer.TargetRevisionSHA256) || isDevice != signer.PhysicalTarget {
			return registry, "", ErrScoutQualificationEvidence
		}
		seenKey[signer.KeyID], seenFingerprint[signer.PublicKeyFingerprintSHA256] = true, true
		roles[signer.Role]++
	}
	for _, role := range []string{"provider_attempt_attestor", "device_evidence_attestor", "billing_reconciliation_attestor", "qualification_receipt_attestor", "operator", "independent_reviewer"} {
		if roles[role] < 1 {
			return registry, "", ErrScoutQualificationEvidence
		}
	}
	if roles["qualification_receipt_attestor"] != 1 || verifyScoutReceiptPrivateKey(authority, registry) != nil {
		return registry, "", ErrScoutQualificationEvidence
	}
	return registry, registryDigest, nil
}

func verifyScoutReceiptPrivateKey(authority ScoutQualificationAuthority, registry ScoutQualificationTrustRegistry) error {
	publicKey := ed25519.PrivateKey(authority.ReceiptSignerPrivateKey).Public().(ed25519.PublicKey)
	fingerprint := digestBytes(publicKey)
	for _, signer := range registry.Signers {
		if signer.KeyID == authority.ReceiptSignerKeyID && signer.Role == "qualification_receipt_attestor" && signer.TargetID == "" && signer.PublicKeyFingerprintSHA256 == fingerprint {
			return nil
		}
	}
	return ErrScoutQualificationEvidence
}

func parseScoutEvidence(testCase ScoutQualificationCase, observation ScoutQualificationObservation, corpus ScoutQualificationCorpus, config ScoutQualificationConfig, mode scoutEvaluationMode, calls, evidenceIDs, evidenceBodies map[string]struct{}) (scoutProviderTerminal, scoutDeviceCapture, []string, error) {
	var terminal scoutProviderTerminal
	var device scoutDeviceCapture
	var billing scoutBillingReconciliation
	var terminalRaw, deviceRaw, billingRaw *ScoutRawEvidence
	digests := make([]string, 0, 3)
	for i := range observation.RawEvidence {
		evidence := &observation.RawEvidence[i]
		if evidence.ID == "" || len(evidence.Body) == 0 || len(evidence.Body) > maxScoutQualificationRawSize || !validDigest(evidence.BodySHA256) || digestBytes(evidence.Body) != evidence.BodySHA256 {
			return terminal, device, nil, ErrScoutQualificationEvidence
		}
		if _, duplicate := evidenceIDs[evidence.ID]; duplicate {
			return terminal, device, nil, ErrScoutQualificationEvidence
		}
		if _, duplicate := evidenceBodies[evidence.BodySHA256]; duplicate {
			return terminal, device, nil, ErrScoutQualificationEvidence
		}
		evidenceIDs[evidence.ID], evidenceBodies[evidence.BodySHA256] = struct{}{}, struct{}{}
		digests = append(digests, evidence.BodySHA256)
		switch evidence.Source + ":" + evidence.Class {
		case "provider:provider_live_attempt_terminal":
			if terminalRaw != nil || decodeScoutRaw(evidence.Body, &terminal) != nil || terminal.Type != "response.done" || !oneOfScoutTerminalStatus(terminal.Status) || terminal.ProviderCallID == "" || terminal.ProviderIdentity != config.ProviderIdentity || terminal.BillingReconciliation != config.BillingReconciliation {
				return terminal, device, nil, ErrScoutQualificationEvidence
			}
			published := terminal.AudioPublished || terminal.TextPublished || terminal.CardContentPublished || terminal.ExistenceSignalPublished
			if (terminal.Status == "completed") != terminal.AcceptedOutput || (!terminal.AcceptedOutput && published) {
				return terminal, device, nil, ErrScoutQualificationEvidence
			}
			terminalRaw = evidence
			if _, duplicate := calls[terminal.ProviderCallID]; duplicate {
				return terminal, device, nil, ErrScoutQualificationEvidence
			}
			calls[terminal.ProviderCallID] = struct{}{}
		case "device:device_live_capture":
			if deviceRaw != nil || decodeScoutRaw(evidence.Body, &device) != nil || device.Type != "device.capture.complete" || device.TargetID == "" || device.AddressCompletedMS == nil || *device.AddressCompletedMS < 0 {
				return terminal, device, nil, ErrScoutQualificationEvidence
			}
			deviceRaw = evidence
		case "billing:provider_billing_reconciliation":
			if billingRaw != nil || decodeScoutRaw(evidence.Body, &billing) != nil || billing.Type != "provider.billing.reconciled" || billing.ProviderCallID == "" || billing.ProviderIdentity != config.ProviderIdentity || billing.BillingReconciliation != config.BillingReconciliation || !oneOfScoutTerminalStatus(billing.Status) {
				return terminal, device, nil, ErrScoutQualificationEvidence
			}
			billingRaw = evidence
		default:
			return terminal, device, nil, ErrScoutQualificationEvidence
		}
	}
	if terminalRaw == nil || deviceRaw == nil || billingRaw == nil || !matchesScoutRoute(terminal.Lane, terminal.Surface, terminal.Platform, testCase) || !matchesScoutRoute(device.Lane, device.Surface, device.Platform, testCase) || device.TargetID != scoutPlatformTargetID(testCase.Platform) || !matchesScoutBinding(terminal.CaseID, terminal.AttemptID, terminal.InputAudioSHA256, terminal.CorpusSHA256, terminal.ConfigSHA256, terminal.CandidateSHA256, terminal.SourceArtifactSetSHA256, terminal.ReleaseSHA256, terminal.RegistrySHA256, testCase, observation, corpus, config, mode.registryDigest) || !matchesScoutBinding(device.CaseID, device.AttemptID, device.InputAudioSHA256, device.CorpusSHA256, device.ConfigSHA256, device.CandidateSHA256, device.SourceArtifactSetSHA256, device.ReleaseSHA256, device.RegistrySHA256, testCase, observation, corpus, config, mode.registryDigest) || !matchesScoutBillingBinding(billing, terminal, testCase, observation, corpus, config, mode.registryDigest) {
		return terminal, device, nil, ErrScoutQualificationEvidence
	}
	terminalUsage, terminalUsageErr := reconcileScoutTerminalUsage(terminal, config)
	billingUsage, billingUsageErr := reconcileScoutBillingUsage(billing, config)
	if terminalUsageErr != nil || billingUsageErr != nil || terminalUsage != billingUsage {
		return terminal, device, nil, ErrScoutQualificationEvidence
	}
	if terminal.AudioPublished != device.AudioObserved || terminal.TextPublished != device.TextObserved || terminal.CardContentPublished != device.CardContentObserved || terminal.ExistenceSignalPublished != device.ExistenceSignalObserved || ((!terminal.AudioPublished) && (device.ResponseCorrect || device.ResponseUseful)) {
		return terminal, device, nil, ErrScoutQualificationEvidence
	}
	if mode.trusted {
		if verifyScoutSigner(*terminalRaw, mode.registry, "provider_attempt_attestor", "") != nil || verifyScoutSigner(*deviceRaw, mode.registry, "device_evidence_attestor", device.TargetID) != nil || verifyScoutSigner(*billingRaw, mode.registry, "billing_reconciliation_attestor", "") != nil {
			return terminal, device, nil, ErrScoutQualificationEvidence
		}
		binding, err := scoutAttestationPayload(testCase, observation, corpus, config, mode.registryDigest, terminalRaw.BodySHA256, deviceRaw.BodySHA256, billingRaw.BodySHA256)
		if err != nil || verifyScoutAttestation(observation.OperatorAttestation, binding, mode.registry, "operator") != nil || verifyScoutAttestation(observation.ReviewerAttestation, binding, mode.registry, "independent_reviewer") != nil || !distinctScoutAttestations(observation.OperatorAttestation, observation.ReviewerAttestation, mode.registry) {
			return terminal, device, nil, ErrScoutQualificationEvidence
		}
	}
	return terminal, device, digests, nil
}

func oneOfScoutTerminalStatus(status string) bool {
	return status == "completed" || status == "incomplete" || status == "failed"
}

func matchesScoutRoute(lane ScoutQualificationLane, surface ScoutQualificationSurface, platform ScoutQualificationPlatform, testCase ScoutQualificationCase) bool {
	return lane == testCase.Lane && surface == testCase.Surface && platform == testCase.Platform
}

func scoutPlatformTargetID(platform ScoutQualificationPlatform) string {
	if platform == ScoutPlatformPhysicalIOS {
		return "iphone-physical-01"
	}
	if platform == ScoutPlatformDesktopWeb {
		return "desktop-web-01"
	}
	return ""
}

func matchesScoutBillingBinding(billing scoutBillingReconciliation, terminal scoutProviderTerminal, testCase ScoutQualificationCase, observation ScoutQualificationObservation, corpus ScoutQualificationCorpus, config ScoutQualificationConfig, expectedRegistry string) bool {
	registryMatches := billing.RegistrySHA256 == expectedRegistry
	if expectedRegistry == "" {
		registryMatches = validDigest(billing.RegistrySHA256)
	}
	return matchesScoutRoute(billing.Lane, billing.Surface, billing.Platform, testCase) && billing.Lane == terminal.Lane && billing.Surface == terminal.Surface && billing.Platform == terminal.Platform && billing.CaseID == testCase.ID && billing.AttemptID == observation.AttemptID && billing.ProviderCallID == terminal.ProviderCallID && billing.CorpusSHA256 == corpus.Digest && billing.ConfigSHA256 == config.Digest && billing.CandidateSHA256 == config.CandidateSHA256 && billing.SourceArtifactSetSHA256 == config.SourceArtifactSetSHA256 && billing.ReleaseSHA256 == corpus.ReleaseSHA256 && billing.Status == terminal.Status && billing.AcceptedOutput == terminal.AcceptedOutput && registryMatches
}

func matchesScoutBinding(caseID, attemptID, inputAudio, corpusDigest, configDigest, candidateDigest, sourceDigest, releaseDigest, registryDigest string, testCase ScoutQualificationCase, observation ScoutQualificationObservation, corpus ScoutQualificationCorpus, config ScoutQualificationConfig, expectedRegistry string) bool {
	registryMatches := registryDigest == expectedRegistry
	if expectedRegistry == "" {
		registryMatches = validDigest(registryDigest)
	}
	return caseID == testCase.ID && attemptID == observation.AttemptID && inputAudio == testCase.InputAudioSHA256 && corpusDigest == corpus.Digest && configDigest == config.Digest && candidateDigest == config.CandidateSHA256 && sourceDigest == config.SourceArtifactSetSHA256 && releaseDigest == corpus.ReleaseSHA256 && registryMatches
}

func scoutAttestationPayload(testCase ScoutQualificationCase, observation ScoutQualificationObservation, corpus ScoutQualificationCorpus, config ScoutQualificationConfig, registryDigest, terminalDigest, deviceDigest, billingDigest string) ([]byte, error) {
	return json.Marshal(struct {
		Schema                  string                     `json:"schema"`
		CaseID                  string                     `json:"caseId"`
		AttemptID               string                     `json:"attemptId"`
		Lane                    ScoutQualificationLane     `json:"lane"`
		Surface                 ScoutQualificationSurface  `json:"surface"`
		Platform                ScoutQualificationPlatform `json:"platform"`
		InputAudioSHA256        string                     `json:"inputAudioSha256"`
		CorpusSHA256            string                     `json:"corpusSha256"`
		ConfigSHA256            string                     `json:"configSha256"`
		CandidateSHA256         string                     `json:"candidateSha256"`
		SourceArtifactSetSHA256 string                     `json:"sourceArtifactSetSha256"`
		ReleaseSHA256           string                     `json:"releaseSha256"`
		RegistrySHA256          string                     `json:"registrySha256"`
		ProviderEvidenceSHA256  string                     `json:"providerEvidenceSha256"`
		DeviceEvidenceSHA256    string                     `json:"deviceEvidenceSha256"`
		BillingEvidenceSHA256   string                     `json:"billingEvidenceSha256"`
	}{"stride.e10.scout-observation-attestation/v3", testCase.ID, observation.AttemptID, testCase.Lane, testCase.Surface, testCase.Platform, testCase.InputAudioSHA256, corpus.Digest, config.Digest, config.CandidateSHA256, config.SourceArtifactSetSHA256, corpus.ReleaseSHA256, registryDigest, terminalDigest, deviceDigest, billingDigest})
}

func verifyScoutSigner(evidence ScoutRawEvidence, registry ScoutQualificationTrustRegistry, role, targetID string) error {
	return verifyScoutRegistrySignature(evidence.SignerKeyID, evidence.PublicKey, evidence.Signature, evidence.Body, registry, role, targetID)
}

func verifyScoutAttestation(attestation ScoutSignedAttestation, payload []byte, registry ScoutQualificationTrustRegistry, role string) error {
	return verifyScoutRegistrySignature(attestation.KeyID, attestation.PublicKey, attestation.Signature, payload, registry, role, "")
}

func verifyScoutRegistrySignature(keyID string, publicKey, signature, payload []byte, registry ScoutQualificationTrustRegistry, role, targetID string) error {
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return ErrScoutQualificationEvidence
	}
	fingerprint := digestBytes(publicKey)
	for _, signer := range registry.Signers {
		if signer.KeyID == keyID && signer.Role == role && signer.TargetID == targetID && signer.PublicKeyFingerprintSHA256 == fingerprint && ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
			return nil
		}
	}
	return ErrScoutQualificationEvidence
}

func distinctScoutAttestations(operator, reviewer ScoutSignedAttestation, registry ScoutQualificationTrustRegistry) bool {
	if operator.KeyID == reviewer.KeyID || bytes.Equal(operator.PublicKey, reviewer.PublicKey) {
		return false
	}
	identities := map[string]string{}
	for _, signer := range registry.Signers {
		identities[signer.KeyID] = signer.IdentityID
	}
	return identities[operator.KeyID] != "" && identities[reviewer.KeyID] != "" && identities[operator.KeyID] != identities[reviewer.KeyID]
}

func reconcileScoutTerminalUsage(terminal scoutProviderTerminal, config ScoutQualificationConfig) (ScoutUsageTotals, error) {
	return reconcileScoutUsage(terminal.Usage, terminal.CostNanoUSD, config)
}

func reconcileScoutBillingUsage(billing scoutBillingReconciliation, config ScoutQualificationConfig) (ScoutUsageTotals, error) {
	return reconcileScoutUsage(billing.Usage, billing.CostNanoUSD, config)
}

func reconcileScoutUsage(u scoutProviderUsage, reportedCost *int64, config ScoutQualificationConfig) (ScoutUsageTotals, error) {
	if reportedCost == nil || u.InputTextTokens == nil || u.InputAudioTokens == nil || u.CachedTextTokens == nil || u.CachedAudioTokens == nil || u.OutputTextTokens == nil || u.OutputAudioTokens == nil || u.TotalTokens == nil {
		return ScoutUsageTotals{}, ErrScoutQualificationEvidence
	}
	values := []int64{*u.InputTextTokens, *u.InputAudioTokens, *u.CachedTextTokens, *u.CachedAudioTokens, *u.OutputTextTokens, *u.OutputAudioTokens}
	for _, value := range values {
		if value < 0 || value > MaxProbeUsageTokens {
			return ScoutUsageTotals{}, ErrScoutQualificationEvidence
		}
	}
	if *u.CachedTextTokens > *u.InputTextTokens || *u.CachedAudioTokens > *u.InputAudioTokens {
		return ScoutUsageTotals{}, ErrScoutQualificationEvidence
	}
	total, ok := addScoutInt64(*u.InputTextTokens, *u.InputAudioTokens, *u.OutputTextTokens, *u.OutputAudioTokens)
	if !ok || *u.TotalTokens != total {
		return ScoutUsageTotals{}, ErrScoutQualificationEvidence
	}
	cost, ok := scoutUsageCost(config, u)
	if !ok || *reportedCost != cost {
		return ScoutUsageTotals{}, ErrScoutQualificationEvidence
	}
	return ScoutUsageTotals{InputTextTokens: *u.InputTextTokens, InputAudioTokens: *u.InputAudioTokens, CachedTextTokens: *u.CachedTextTokens, CachedAudioTokens: *u.CachedAudioTokens, OutputTextTokens: *u.OutputTextTokens, OutputAudioTokens: *u.OutputAudioTokens, TotalTokens: total, CostNanoUSD: cost}, nil
}

func scoutUsageCost(config ScoutQualificationConfig, u scoutProviderUsage) (int64, bool) {
	pairs := [][2]int64{{*u.InputTextTokens - *u.CachedTextTokens, config.InputTextNanoUSDPerToken}, {*u.CachedTextTokens, config.CachedTextNanoUSDPerToken}, {*u.InputAudioTokens - *u.CachedAudioTokens, config.InputAudioNanoUSDPerToken}, {*u.CachedAudioTokens, config.CachedAudioNanoUSDPerToken}, {*u.OutputTextTokens, config.OutputTextNanoUSDPerToken}, {*u.OutputAudioTokens, config.OutputAudioNanoUSDPerToken}}
	var total int64
	for _, pair := range pairs {
		if pair[0] != 0 && pair[1] > math.MaxInt64/pair[0] {
			return 0, false
		}
		value := pair[0] * pair[1]
		if value < 0 || total > math.MaxInt64-value {
			return 0, false
		}
		total += value
	}
	return total, true
}

func addScoutInt64(values ...int64) (int64, bool) {
	var total int64
	for _, value := range values {
		if value < 0 || total > math.MaxInt64-value {
			return 0, false
		}
		total += value
	}
	return total, true
}

func addScoutUsage(total *ScoutUsageTotals, addition ScoutUsageTotals) bool {
	if willScoutOverflow(total.InputTextTokens, addition.InputTextTokens) || willScoutOverflow(total.InputAudioTokens, addition.InputAudioTokens) || willScoutOverflow(total.CachedTextTokens, addition.CachedTextTokens) || willScoutOverflow(total.CachedAudioTokens, addition.CachedAudioTokens) || willScoutOverflow(total.OutputTextTokens, addition.OutputTextTokens) || willScoutOverflow(total.OutputAudioTokens, addition.OutputAudioTokens) || willScoutOverflow(total.TotalTokens, addition.TotalTokens) || willScoutOverflow(total.CostNanoUSD, addition.CostNanoUSD) {
		return false
	}
	total.InputTextTokens += addition.InputTextTokens
	total.InputAudioTokens += addition.InputAudioTokens
	total.CachedTextTokens += addition.CachedTextTokens
	total.CachedAudioTokens += addition.CachedAudioTokens
	total.OutputTextTokens += addition.OutputTextTokens
	total.OutputAudioTokens += addition.OutputAudioTokens
	total.TotalTokens += addition.TotalTokens
	total.CostNanoUSD += addition.CostNanoUSD
	return true
}

func willScoutOverflow(left, right int64) bool {
	return right < 0 || (right > 0 && left > math.MaxInt64-right)
}

func scoutLatencySummary(values []int64, seedMaterial string) ScoutLatencySummary {
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	result := ScoutLatencySummary{SampleCount: len(sorted), Replicates: scoutBootstrapReplicates, Method: "deterministic_percentile_bootstrap_95"}
	if len(sorted) == 0 {
		return result
	}
	result.P50.PointMS, result.P95.PointMS, result.P99.PointMS = scoutPercentile(sorted, .50), scoutPercentile(sorted, .95), scoutPercentile(sorted, .99)
	seed := sha256.Sum256([]byte(seedMaterial))
	rng := scoutDeterministicRNG{state: binary.LittleEndian.Uint64(seed[:8])}
	if rng.state == 0 {
		rng.state = 0x9e3779b97f4a7c15
	}
	p50, p95, p99 := make([]int64, scoutBootstrapReplicates), make([]int64, scoutBootstrapReplicates), make([]int64, scoutBootstrapReplicates)
	sample := make([]int64, len(sorted))
	for replicate := 0; replicate < scoutBootstrapReplicates; replicate++ {
		for i := range sample {
			sample[i] = sorted[rng.next()%uint64(len(sorted))]
		}
		sort.Slice(sample, func(i, j int) bool { return sample[i] < sample[j] })
		p50[replicate], p95[replicate], p99[replicate] = scoutPercentile(sample, .50), scoutPercentile(sample, .95), scoutPercentile(sample, .99)
	}
	for _, distribution := range [][]int64{p50, p95, p99} {
		sort.Slice(distribution, func(i, j int) bool { return distribution[i] < distribution[j] })
	}
	result.P50.Low95MS, result.P50.High95MS = scoutPercentile(p50, .025), scoutPercentile(p50, .975)
	result.P95.Low95MS, result.P95.High95MS = scoutPercentile(p95, .025), scoutPercentile(p95, .975)
	result.P99.Low95MS, result.P99.High95MS = scoutPercentile(p99, .025), scoutPercentile(p99, .975)
	return result
}

type scoutDeterministicRNG struct{ state uint64 }

func (rng *scoutDeterministicRNG) next() uint64 {
	x := rng.state
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	rng.state = x
	return x
}

func scoutPercentile(sorted []int64, percentile float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func scoutWilson95(successes, total int) ScoutRateInterval {
	result := ScoutRateInterval{Numerator: successes, Denominator: total, Method: "wilson_95"}
	if total < 1 || successes < 0 || successes > total {
		return result
	}
	p := float64(successes) / float64(total)
	z := 1.959963984540054
	zSquared := z * z
	denominator := 1 + zSquared/float64(total)
	center := (p + zSquared/(2*float64(total))) / denominator
	radius := z * math.Sqrt((p*(1-p)+zSquared/(4*float64(total)))/float64(total)) / denominator
	result.Point, result.Low, result.High = p, math.Max(0, center-radius), math.Min(1, center+radius)
	return result
}

func validScoutCase(testCase ScoutQualificationCase) bool {
	validClass := testCase.Class == ScoutCorpusOrdinary || testCase.Class == ScoutCorpusExplicit || testCase.Class == ScoutCorpusAudienceNegative
	validPlatform := testCase.Platform == ScoutPlatformPhysicalIOS || testCase.Platform == ScoutPlatformDesktopWeb
	validRoute := testCase.Lane == ScoutLanePersonal && testCase.Surface == ScoutSurfacePrivateVoice || testCase.Lane == ScoutLaneMeeting && testCase.Surface == ScoutSurfaceMeetingVoice
	return testCase.ID != "" && validDigest(testCase.InputAudioSHA256) && validClass && validPlatform && validRoute
}

func scoutCorpusDistribution(cases []ScoutQualificationCase) ScoutQualificationDistribution {
	var distribution ScoutQualificationDistribution
	for _, testCase := range cases {
		switch {
		case testCase.Lane == ScoutLanePersonal && testCase.Platform == ScoutPlatformPhysicalIOS:
			distribution.PersonalPhysicalIOS++
		case testCase.Lane == ScoutLanePersonal && testCase.Platform == ScoutPlatformDesktopWeb:
			distribution.PersonalDesktopWeb++
		case testCase.Lane == ScoutLaneMeeting && testCase.Platform == ScoutPlatformPhysicalIOS:
			distribution.MeetingPhysicalIOS++
		case testCase.Lane == ScoutLaneMeeting && testCase.Platform == ScoutPlatformDesktopWeb:
			distribution.MeetingDesktopWeb++
		}
	}
	return distribution
}

func validScoutRequiredDistribution(distribution ScoutQualificationDistribution) bool {
	return distribution.PersonalPhysicalIOS > 0 && distribution.PersonalDesktopWeb > 0 && distribution.MeetingPhysicalIOS > 0 && distribution.MeetingDesktopWeb > 0
}

func scoutDistributionSatisfies(actual, required ScoutQualificationDistribution) bool {
	return actual.PersonalPhysicalIOS >= required.PersonalPhysicalIOS && actual.PersonalDesktopWeb >= required.PersonalDesktopWeb && actual.MeetingPhysicalIOS >= required.MeetingPhysicalIOS && actual.MeetingDesktopWeb >= required.MeetingDesktopWeb
}

func validScoutConfig(config ScoutQualificationConfig) bool {
	prices := []int64{config.InputTextNanoUSDPerToken, config.InputAudioNanoUSDPerToken, config.CachedTextNanoUSDPerToken, config.CachedAudioNanoUSDPerToken, config.OutputTextNanoUSDPerToken, config.OutputAudioNanoUSDPerToken}
	if config.Schema != "stride.e10.scout-qualification-config/v3" || config.Version < 1 || config.Model != ScoutRealtimeModel || config.ReasoningEffort == "" || config.Voice == "" || config.VADMode == "" || !strings.HasPrefix(config.PricingSource, "https://") || !validDigest(config.ReleaseSHA256) || !validDigest(config.CandidateSHA256) || !validDigest(config.SourceArtifactSetSHA256) || !validDigest(config.VADPolicySHA256) || !validDigest(config.ToolPolicySHA256) || !validDigest(config.PromptSHA256) || !validDigest(config.EventSchemaSHA256) || !validDigest(config.RouteIdentitySHA256) || !validDigest(config.PricingRevisionSHA256) || !validScoutProviderIdentity(config.ProviderIdentity) || !validScoutBillingReconciliation(config.BillingReconciliation) || !validScoutRequiredDistribution(config.RequiredDistribution) {
		return false
	}
	if config.MaxTerminalTokens <= 0 || config.MaxTerminalCostNanoUSD <= 0 || config.MaxAcceptedTokens < config.MaxTerminalTokens || config.MaxAcceptedCostNanoUSD < config.MaxTerminalCostNanoUSD || config.MaxRejectedTokens < 0 || config.MaxRejectedCostNanoUSD < 0 || config.MaxRejectedTerminalCount < 0 {
		return false
	}
	for _, price := range prices {
		if price <= 0 || price > 1_000_000_000 {
			return false
		}
	}
	return true
}

func validScoutProviderIdentity(identity ScoutProviderIdentity) bool {
	return identity.Provider == "openai" && strings.TrimSpace(identity.ProjectID) == identity.ProjectID && identity.ProjectID != "" && strings.TrimSpace(identity.OrganizationID) == identity.OrganizationID && identity.OrganizationID != "" && identity.CredentialMode == ScoutCredentialProjectServiceAccount
}

func validScoutBillingReconciliation(route ScoutBillingReconciliationRoute) bool {
	return route.Mode == ScoutBillingProviderUsageExport && strings.TrimSpace(route.BillingAccountID) == route.BillingAccountID && route.BillingAccountID != "" && strings.HasPrefix(route.EvidenceSource, "https://") && validDigest(route.EvidenceRevisionSHA256)
}

func scoutProviderIdentityDigest(identity ScoutProviderIdentity) string {
	digest, _ := qualificationDigest(identity)
	return digest
}

func scoutBillingReconciliationDigest(route ScoutBillingReconciliationRoute) string {
	digest, _ := qualificationDigest(route)
	return digest
}

func scoutRouteIdentityDigest(config ScoutQualificationConfig) string {
	payload := struct {
		Model                 string                          `json:"model"`
		ReasoningEffort       string                          `json:"reasoningEffort"`
		Voice                 string                          `json:"voice"`
		VADMode               string                          `json:"vadMode"`
		VADPolicySHA256       string                          `json:"vadPolicySha256"`
		ToolPolicySHA256      string                          `json:"toolPolicySha256"`
		PromptSHA256          string                          `json:"promptSha256"`
		EventSchemaSHA256     string                          `json:"eventSchemaSha256"`
		CandidateSHA256       string                          `json:"candidateSha256"`
		ProviderIdentity      ScoutProviderIdentity           `json:"providerIdentity"`
		BillingReconciliation ScoutBillingReconciliationRoute `json:"billingReconciliation"`
		RequiredDistribution  ScoutQualificationDistribution  `json:"requiredDistribution"`
	}{config.Model, config.ReasoningEffort, config.Voice, config.VADMode, config.VADPolicySHA256, config.ToolPolicySHA256, config.PromptSHA256, config.EventSchemaSHA256, config.CandidateSHA256, config.ProviderIdentity, config.BillingReconciliation, config.RequiredDistribution}
	digest, _ := qualificationDigest(payload)
	return digest
}

func claimScoutAttempts(authority ScoutQualificationAuthority, registryDigest string, attemptIDs []string) error {
	claimDigests := make([]string, 0, len(attemptIDs))
	for _, attemptID := range attemptIDs {
		// The key excludes the registry: an attempt ID is globally one-use in
		// the anchored ledger namespace, including across registry revisions.
		claimDigests = append(claimDigests, digest(authority.ExpectedAttemptLedgerID+"\x00"+attemptID))
	}
	sort.Strings(claimDigests)
	return appendScoutAttemptClaimBatch(authority, registryDigest, claimDigests)
}

type scoutAttemptClaimBatch struct {
	Schema          string   `json:"schema"`
	RegistrySHA256  string   `json:"registrySha256"`
	AttemptLedgerID string   `json:"attemptLedgerId"`
	ClaimSHA256s    []string `json:"claimSha256s"`
}

var scoutAttemptLedgerWrite = func(file *os.File, body []byte) (int, error) {
	return file.Write(body)
}

// appendScoutAttemptClaimBatch uses one locked, append-only, fsynced ledger
// record per evaluation. This preserves cross-process one-use semantics while
// avoiding thousands of separately fsynced files for the required 3,500-case
// corpus. A torn or malformed ledger fails closed.
func appendScoutAttemptClaimBatch(authority ScoutQualificationAuthority, registryDigest string, claims []string) error {
	if len(claims) == 0 || !sort.StringsAreSorted(claims) {
		return ErrScoutQualificationEvidence
	}
	for index, claim := range claims {
		if !validDigest(claim) || (index > 0 && claim == claims[index-1]) {
			return ErrScoutQualificationEvidence
		}
	}
	path := filepath.Join(authority.AttemptLedgerDirectory, "."+digest(authority.ExpectedAttemptLedgerID)+".claims.jsonl")
	_, statErr := os.Lstat(path)
	created := os.IsNotExist(statErr)
	if statErr != nil && !created {
		return statErr
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN) //nolint:errcheck -- unlock follows a completed/failed claim
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > 64<<20 {
		return ErrScoutQualificationEvidence
	}
	if linkInfo, err := os.Lstat(path); err != nil || !os.SameFile(info, linkInfo) {
		return ErrScoutQualificationEvidence
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	used := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var batch scoutAttemptClaimBatch
		if decodeScoutRaw(scanner.Bytes(), &batch) != nil || batch.Schema != "stride.e10.scout-attempt-claim-batch/v1" || batch.AttemptLedgerID != authority.ExpectedAttemptLedgerID || !validDigest(batch.RegistrySHA256) || len(batch.ClaimSHA256s) == 0 || !sort.StringsAreSorted(batch.ClaimSHA256s) {
			return ErrScoutQualificationEvidence
		}
		for index, claim := range batch.ClaimSHA256s {
			if !validDigest(claim) || (index > 0 && claim == batch.ClaimSHA256s[index-1]) {
				return ErrScoutQualificationEvidence
			}
			if _, duplicate := used[claim]; duplicate {
				return ErrScoutQualificationEvidence
			}
			used[claim] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return ErrScoutQualificationEvidence
	}
	for _, claim := range claims {
		if _, exists := used[claim]; exists {
			return ErrScoutAttemptReused
		}
	}
	batch := scoutAttemptClaimBatch{"stride.e10.scout-attempt-claim-batch/v1", registryDigest, authority.ExpectedAttemptLedgerID, claims}
	body, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	startOffset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	record := append(body, '\n')
	for offset := 0; offset < len(record); {
		written, writeErr := scoutAttemptLedgerWrite(file, record[offset:])
		if written < 0 || written > len(record)-offset {
			writeErr = io.ErrShortWrite
			written = 0
		}
		offset += written
		if writeErr != nil || written == 0 {
			if truncateErr := file.Truncate(startOffset); truncateErr != nil {
				return errors.Join(ErrScoutQualificationEvidence, writeErr, truncateErr)
			}
			if _, seekErr := file.Seek(startOffset, io.SeekStart); seekErr != nil {
				return errors.Join(ErrScoutQualificationEvidence, writeErr, seekErr)
			}
			if syncErr := file.Sync(); syncErr != nil {
				return errors.Join(ErrScoutQualificationEvidence, writeErr, syncErr)
			}
			if writeErr != nil {
				return writeErr
			}
			return io.ErrShortWrite
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if created {
		directory, err := os.Open(authority.AttemptLedgerDirectory)
		if err != nil {
			return err
		}
		if err := directory.Sync(); err != nil {
			_ = directory.Close()
			return err
		}
		return directory.Close()
	}
	return nil
}

func writeScoutFileExclusive(path string, body []byte) error {
	if !filepath.IsAbs(path) {
		return ErrScoutQualificationInvalid
	}
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return ErrScoutQualificationInvalid
	}
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	for len(body) > 0 {
		written, writeErr := temporary.Write(body)
		if writeErr != nil {
			_ = temporary.Close()
			return writeErr
		}
		if written < 1 {
			_ = temporary.Close()
			return io.ErrShortWrite
		}
		body = body[written:]
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}

func decodeScoutRaw(raw []byte, destination any) error {
	if err := rejectScoutDuplicateJSONKeys(raw); err != nil {
		return ErrScoutQualificationEvidence
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ErrScoutQualificationEvidence
	}
	return nil
}

func rejectScoutDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := walkScoutJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("raw evidence was not exactly one JSON value")
	}
	return nil
}

func walkScoutJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("object key was not a string")
			}
			if _, duplicate := seen[name]; duplicate {
				return errors.New("raw evidence contains a duplicate JSON key")
			}
			seen[name] = struct{}{}
			if err := walkScoutJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("malformed object")
		}
	case '[':
		for decoder.More() {
			if err := walkScoutJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("malformed array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func qualificationDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

func scoutReceiptDigest(receipt ScoutQualificationReceipt) (string, error) {
	receipt.ReceiptSHA256 = ""
	receipt.ReceiptSignature = nil
	return qualificationDigest(receipt)
}
