package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

var ErrDictationQualificationUnverified = errors.New("dictation capability is not trusted-device-qualified")

type DictationTargetPlatform string

const (
	DictationTargetWeb    DictationTargetPlatform = "web"
	DictationTargetIPhone DictationTargetPlatform = "iphone"
	DictationTargetIPad   DictationTargetPlatform = "ipad"
)

type DictationComposerSurface string

const (
	DictationComposerScout   DictationComposerSurface = "scout"
	DictationComposerPrivate DictationComposerSurface = "private_thread"
	DictationComposerTeam    DictationComposerSurface = "team"
	DictationComposerProject DictationComposerSurface = "project"
	DictationComposerInRoom  DictationComposerSurface = "in_room"
)

type DictationEvidenceClass string

const (
	DictationEvidenceSynthetic DictationEvidenceClass = "synthetic"
	DictationEvidenceLocal     DictationEvidenceClass = "local"
	DictationEvidencePhysical  DictationEvidenceClass = "physical_target_device"
)

type DictationTerminalOutcome string

const (
	DictationSent      DictationTerminalOutcome = "sent"
	DictationStopped   DictationTerminalOutcome = "stopped"
	DictationDeleted   DictationTerminalOutcome = "deleted"
	DictationEmpty     DictationTerminalOutcome = "empty"
	DictationErrored   DictationTerminalOutcome = "errored"
	DictationCancelled DictationTerminalOutcome = "cancelled"
)

// DictationQualificationObservation contains counters and timings only. Raw
// audio and transcript text belong in a separately consented, access-controlled
// corpus; neither is copied into a qualification receipt.
type DictationQualificationObservation struct {
	ID                         string                   `json:"id"`
	Platform                   DictationTargetPlatform  `json:"platform"`
	Composer                   DictationComposerSurface `json:"composer"`
	EvidenceClass              DictationEvidenceClass   `json:"evidenceClass"`
	Outcome                    DictationTerminalOutcome `json:"outcome"`
	ClipDurationMS             int64                    `json:"clipDurationMs"`
	SendRequestedAt            time.Time                `json:"sendRequestedAt"`
	PostedAt                   time.Time                `json:"postedAt,omitempty"`
	PostCount                  int                      `json:"postCount"`
	ModelCalls                 int                      `json:"modelCalls"`
	FirstAttemptSucceeded      bool                     `json:"firstAttemptSucceeded"`
	CancellationRace           bool                     `json:"cancellationRace"`
	MicGenerationOverlap       bool                     `json:"micGenerationOverlap"`
	PersonalRealtimeHandoff    bool                     `json:"personalRealtimeHandoff"`
	MeetingHandoff             bool                     `json:"meetingHandoff"`
	InRoomTrackFramesLeaked    int                      `json:"inRoomTrackFramesLeaked"`
	InRoomTranscriptFramesLeak int                      `json:"inRoomTranscriptFramesLeak"`
	WaveformFPS                float64                  `json:"waveformFps"`
	ReducedMotion              bool                     `json:"reducedMotion"`
	AudioSHA256                string                   `json:"audioSha256"`
	TranscriptReceiptDigest    string                   `json:"transcriptReceiptDigest"`
	LifecycleReceiptDigest     string                   `json:"lifecycleReceiptDigest"`
	DeviceReceiptDigest        string                   `json:"deviceReceiptDigest"`
	CandidateDigest            string                   `json:"candidateDigest"`
	BuildDigest                string                   `json:"buildDigest"`
	PerformanceReceiptDigest   string                   `json:"performanceReceiptDigest"`
	PostReceiptDigests         []string                 `json:"postReceiptDigests"`
	ModelCallReceiptDigests    []string                 `json:"modelCallReceiptDigests"`
}

type ComposerDictationThresholds struct {
	MinimumUtterances        int     `json:"minimumUtterances"`
	MinimumCancellationRaces int     `json:"minimumCancellationRaces"`
	MinimumMicHandoffs       int     `json:"minimumMicHandoffs"`
	MinimumPersonalToDictate int     `json:"minimumPersonalToDictate"`
	MinimumPersonalToMeeting int     `json:"minimumPersonalToMeeting"`
	MinimumInRoomDictations  int     `json:"minimumInRoomDictations"`
	MinimumFirstAttemptRate  float64 `json:"minimumFirstAttemptRate"`
	MinimumWaveformFPS       float64 `json:"minimumWaveformFps"`
	MaximumP50PostLatencyMS  int64   `json:"maximumP50PostLatencyMs"`
	MaximumP95PostLatencyMS  int64   `json:"maximumP95PostLatencyMs"`
}

type ComposerDictationQualificationReceipt struct {
	UtteranceCount                    int                         `json:"utteranceCount"`
	PhysicalTargetDeviceCount         int                         `json:"physicalTargetDeviceCount"`
	Platforms                         []DictationTargetPlatform   `json:"platforms"`
	ComposerSurfaces                  []DictationComposerSurface  `json:"composerSurfaces"`
	FirstAttemptSuccesses             int                         `json:"firstAttemptSuccesses"`
	FirstAttemptRate                  QualificationMetricInterval `json:"firstAttemptRate"`
	P50SubmitToPostMS                 int64                       `json:"p50SubmitToPostMs"`
	P95SubmitToPostMS                 int64                       `json:"p95SubmitToPostMs"`
	P99SubmitToPostMS                 int64                       `json:"p99SubmitToPostMs"`
	P50SubmitToPost                   QualificationMetricInterval `json:"p50SubmitToPost"`
	P95SubmitToPost                   QualificationMetricInterval `json:"p95SubmitToPost"`
	P99SubmitToPost                   QualificationMetricInterval `json:"p99SubmitToPost"`
	ExactlyOncePass                   bool                        `json:"exactlyOncePass"`
	CancellationPass                  bool                        `json:"cancellationPass"`
	MicGenerationPass                 bool                        `json:"micGenerationPass"`
	PersonalToDictateCount            int                         `json:"personalToDictateCount"`
	PersonalToMeetingCount            int                         `json:"personalToMeetingCount"`
	PlatformComposerPairs             int                         `json:"platformComposerPairs"`
	InRoomPrivacyPass                 bool                        `json:"inRoomPrivacyPass"`
	WaveformPass                      bool                        `json:"waveformPass"`
	FidelityPass                      bool                        `json:"fidelityPass"`
	DeterministicPass                 bool                        `json:"deterministicPass"`
	ProviderQualified                 bool                        `json:"providerQualified"`
	DeviceQualified                   bool                        `json:"deviceQualified"`
	ResidualGates                     []string                    `json:"residualGates"`
	RegistryDigest                    string                      `json:"registryDigest,omitempty"`
	TargetDigest                      string                      `json:"targetDigest,omitempty"`
	CandidateDigest                   string                      `json:"candidateDigest,omitempty"`
	BuildDigest                       string                      `json:"buildDigest,omitempty"`
	TranscriptionTargetDigest         string                      `json:"transcriptionTargetDigest,omitempty"`
	TranscriptionCorpusDigest         string                      `json:"transcriptionCorpusDigest,omitempty"`
	TranscriptionObservationSetDigest string                      `json:"transcriptionObservationSetDigest,omitempty"`
	TranscriptionReceiptDigest        string                      `json:"transcriptionReceiptDigest,omitempty"`
	TranscriptionCandidateDigest      string                      `json:"transcriptionCandidateDigest,omitempty"`
	DictationCorpusDigest             string                      `json:"dictationCorpusDigest,omitempty"`
	ObservationSetDigest              string                      `json:"observationSetDigest,omitempty"`
	OperatorSignatureDigest           string                      `json:"operatorSignatureDigest,omitempty"`
	ReviewerSignatureDigest           string                      `json:"reviewerSignatureDigest,omitempty"`
	ReceiptDigest                     string                      `json:"receiptDigest"`
}

type DictationEvidenceBatchRef struct {
	ID                                string `json:"id"`
	TenantID                          string `json:"tenantId"`
	RegistryDigest                    string `json:"registryDigest"`
	TargetDigest                      string `json:"targetDigest"`
	CandidateDigest                   string `json:"candidateDigest"`
	BuildDigest                       string `json:"buildDigest"`
	TranscriptionTargetDigest         string `json:"transcriptionTargetDigest"`
	TranscriptionCorpusDigest         string `json:"transcriptionCorpusDigest"`
	TranscriptionObservationSetDigest string `json:"transcriptionObservationSetDigest"`
	TranscriptionReceiptDigest        string `json:"transcriptionReceiptDigest"`
	TranscriptionCandidateDigest      string `json:"transcriptionCandidateDigest"`
	DictationCorpusDigest             string `json:"dictationCorpusDigest"`
	ObservationSetDigest              string `json:"observationSetDigest"`
	ThresholdsDigest                  string `json:"thresholdsDigest"`
	Receipt                           string `json:"-"`
}

// StoredDictationEvidenceBatch is a locally supplied structure-only packet.
// Its digest-shaped reviewer/operator fields are not external trust proofs.
type StoredDictationEvidenceBatch struct {
	Ref                       DictationEvidenceBatchRef           `json:"ref"`
	Observations              []DictationQualificationObservation `json:"observations"`
	Thresholds                ComposerDictationThresholds         `json:"thresholds"`
	TranscriptionManifest     TranscriptionCorpusManifest         `json:"transcriptionManifest"`
	TranscriptionObservations []TranscriptionObservation          `json:"transcriptionObservations"`
	TranscriptionThresholds   TranscriptionEvidenceThresholds     `json:"transcriptionThresholds"`
	OperatorSignatureDigest   string                              `json:"operatorSignatureDigest"`
	ReviewerSignatureDigest   string                              `json:"reviewerSignatureDigest"`
}

const ComposerDictationEvidenceCandidateState = "structure_only_dictation_evidence_candidate"

// ComposerDictationQualificationCandidate proves only that the local evidence
// packet passed the deterministic, statistical, device-row and exact
// dictation-to-transcript relationship checks. It is not a provider or device
// qualification and cannot authorize route promotion.
type ComposerDictationQualificationCandidate struct {
	TenantID string                                `json:"tenantId"`
	State    string                                `json:"state"`
	Receipt  ComposerDictationQualificationReceipt `json:"receipt"`
}

func EvaluateComposerDictationEvidenceCandidate(ctx context.Context, tenantID string, ref DictationEvidenceBatchRef, transcription TranscriptionQualificationCandidate, store *QualificationEvidenceStore) (ComposerDictationQualificationCandidate, error) {
	if store == nil || !strideIdentifier(tenantID) || !validDictationBatchRef(ref) || ref.TenantID != tenantID ||
		transcription.TenantID != tenantID || transcription.State != TranscriptionEvidenceCandidateState || transcription.Receipt.Score.ProviderQualified || transcription.Receipt.LiveOrDeviceQualified {
		return ComposerDictationQualificationCandidate{}, ErrDictationQualificationUnverified
	}
	batch, err := store.ConsumeDictationEvidence(ctx, ref)
	if err != nil || batch.Ref != ref || workDigest(batch.Thresholds) != ref.ThresholdsDigest ||
		!isHexDigest(batch.OperatorSignatureDigest) || !isHexDigest(batch.ReviewerSignatureDigest) || batch.OperatorSignatureDigest == batch.ReviewerSignatureDigest {
		return ComposerDictationQualificationCandidate{}, ErrDictationQualificationUnverified
	}
	if batch.TranscriptionManifest.Validate() != nil || batch.TranscriptionManifest.Digest != ref.TranscriptionCorpusDigest ||
		workDigest(batch.TranscriptionThresholds) != transcription.Receipt.EvidenceThresholdsDigest ||
		!transcription.Receipt.PreregisteredPass || !transcription.Receipt.Score.DeterministicPass ||
		!isHexDigest(transcription.Receipt.ObservationSetDigest) || !isHexDigest(transcription.Receipt.ProviderAttemptSetDigest) ||
		transcription.Receipt.RegistryDigest != ref.RegistryDigest || transcription.Receipt.TargetDigest != ref.TranscriptionTargetDigest ||
		transcription.Receipt.CandidateDigest != ref.CandidateDigest || transcription.Receipt.Score.CorpusDigest != ref.TranscriptionCorpusDigest ||
		transcription.Receipt.ObservationSetDigest != ref.TranscriptionObservationSetDigest || transcription.Receipt.ReceiptDigest != ref.TranscriptionReceiptDigest || workDigest(transcription) != ref.TranscriptionCandidateDigest {
		return ComposerDictationQualificationCandidate{}, ErrDictationQualificationUnverified
	}
	if transcriptionObservationSetDigest(batch.TranscriptionObservations) != ref.TranscriptionObservationSetDigest ||
		!dictationTranscriptionCorpusMatches(batch.Observations, batch.TranscriptionManifest, batch.TranscriptionObservations) {
		return ComposerDictationQualificationCandidate{}, ErrDictationQualificationUnverified
	}
	recomputedTranscriptionDigest, err := transcriptionEvidenceReceiptDigest(batch.TranscriptionManifest.Digest, batch.TranscriptionThresholds, transcription.Receipt)
	if err != nil || recomputedTranscriptionDigest != transcription.Receipt.ReceiptDigest {
		return ComposerDictationQualificationCandidate{}, ErrDictationQualificationUnverified
	}
	if dictationCorpusDigest(batch.Observations) != ref.DictationCorpusDigest || dictationObservationSetDigest(batch.Observations) != ref.ObservationSetDigest {
		return ComposerDictationQualificationCandidate{}, ErrDictationQualificationUnverified
	}
	for _, observation := range batch.Observations {
		if observation.CandidateDigest != ref.CandidateDigest || observation.BuildDigest != ref.BuildDigest || observation.EvidenceClass != DictationEvidencePhysical {
			return ComposerDictationQualificationCandidate{}, ErrDictationQualificationUnverified
		}
	}
	receipt, err := EvaluateComposerDictationQualification(batch.Observations, transcription.Receipt, batch.Thresholds)
	if err != nil || !receipt.DeterministicPass || receipt.PhysicalTargetDeviceCount != receipt.UtteranceCount {
		return ComposerDictationQualificationCandidate{}, ErrDictationQualificationUnverified
	}
	receipt.ProviderQualified = false
	receipt.DeviceQualified = false
	receipt.ResidualGates = []string{
		"external_provider_project_and_billing_receipt",
		"external_current_model_contract_receipt",
		"external_terminal_usage_and_cost_receipt",
		"independently_anchored_consent_ledger_receipt",
		"independently_anchored_physical_device_lab_receipt",
	}
	receipt.RegistryDigest = ref.RegistryDigest
	receipt.TargetDigest = ref.TargetDigest
	receipt.CandidateDigest = ref.CandidateDigest
	receipt.BuildDigest = ref.BuildDigest
	receipt.TranscriptionTargetDigest = ref.TranscriptionTargetDigest
	receipt.TranscriptionCorpusDigest = ref.TranscriptionCorpusDigest
	receipt.TranscriptionObservationSetDigest = ref.TranscriptionObservationSetDigest
	receipt.TranscriptionReceiptDigest = ref.TranscriptionReceiptDigest
	receipt.TranscriptionCandidateDigest = ref.TranscriptionCandidateDigest
	receipt.DictationCorpusDigest = ref.DictationCorpusDigest
	receipt.ObservationSetDigest = ref.ObservationSetDigest
	receipt.OperatorSignatureDigest = batch.OperatorSignatureDigest
	receipt.ReviewerSignatureDigest = batch.ReviewerSignatureDigest
	receipt.ReceiptDigest, err = composerDictationReceiptDigest(transcription.Receipt.ReceiptDigest, batch.Thresholds, receipt)
	if err != nil {
		return ComposerDictationQualificationCandidate{}, ErrDictationQualificationUnverified
	}
	return ComposerDictationQualificationCandidate{TenantID: tenantID, State: ComposerDictationEvidenceCandidateState, Receipt: receipt}, nil
}

// EvaluateComposerDictationQualification evaluates a local or imported test
// table without trusting its provenance. Physical samples are useful coverage
// signals, but caller-supplied rows do not qualify a device or a provider.
func EvaluateComposerDictationQualification(observations []DictationQualificationObservation, fidelity TranscriptionEvidenceReceipt, thresholds ComposerDictationThresholds) (ComposerDictationQualificationReceipt, error) {
	if thresholds.MinimumUtterances < 250 || thresholds.MinimumCancellationRaces < 100 || thresholds.MinimumMicHandoffs < 200 || thresholds.MinimumPersonalToDictate < 100 || thresholds.MinimumPersonalToMeeting < 100 || thresholds.MinimumInRoomDictations < 100 ||
		thresholds.MinimumFirstAttemptRate < .99 || thresholds.MinimumFirstAttemptRate > 1 || thresholds.MinimumWaveformFPS < 55 || thresholds.MaximumP50PostLatencyMS < 1 || thresholds.MaximumP50PostLatencyMS > 1500 || thresholds.MaximumP95PostLatencyMS < thresholds.MaximumP50PostLatencyMS || thresholds.MaximumP95PostLatencyMS > 3000 {
		return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
	}
	if len(observations) < thresholds.MinimumUtterances {
		return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
	}
	seen := make(map[string]struct{}, len(observations))
	seenAudio := make(map[string]struct{}, len(observations))
	seenLifecycle := make(map[string]struct{}, len(observations))
	seenDevice := make(map[string]struct{}, len(observations))
	seenTranscript := make(map[string]struct{}, len(observations))
	seenPerformance := make(map[string]struct{}, len(observations))
	seenEffectReceipts := make(map[string]struct{}, len(observations)*2)
	candidateDigest, buildDigest := "", ""
	platforms := map[DictationTargetPlatform]bool{}
	composers := map[DictationComposerSurface]bool{}
	pairs := map[string]bool{}
	latencies := make([]int64, 0, len(observations))
	firstAttempts, physical, cancellations, handoffs, personalToDictate, personalToMeeting, inRoom := 0, 0, 0, 0, 0, 0, 0
	exactlyOnce, cancellationSafe, micSafe, inRoomSafe, waveformSafe := true, true, true, true, true
	for _, observation := range observations {
		if !strideIdentifier(observation.ID) || observation.ClipDurationMS < 1 || observation.ClipDurationMS > 30_000 || observation.ModelCalls < 0 || observation.PostCount < 0 || observation.InRoomTrackFramesLeaked < 0 || observation.InRoomTranscriptFramesLeak < 0 ||
			!validDictationPlatform(observation.Platform) || !validDictationComposer(observation.Composer) || !validDictationEvidenceClass(observation.EvidenceClass) || !validDictationOutcome(observation.Outcome) {
			return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
		}
		if !validDictationEvidenceBindings(observation) || observation.PostCount != len(observation.PostReceiptDigests) || observation.ModelCalls != len(observation.ModelCallReceiptDigests) {
			return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
		}
		if _, duplicate := seen[observation.ID]; duplicate {
			return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
		}
		if !claimUniqueTranscriptionBinding(seenAudio, observation.AudioSHA256) || !claimUniqueTranscriptionBinding(seenLifecycle, observation.LifecycleReceiptDigest) ||
			!claimUniqueTranscriptionBinding(seenDevice, observation.DeviceReceiptDigest) || observation.TranscriptReceiptDigest != "" && !claimUniqueTranscriptionBinding(seenTranscript, observation.TranscriptReceiptDigest) ||
			!claimUniqueTranscriptionBinding(seenPerformance, observation.PerformanceReceiptDigest) {
			return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
		}
		for _, digest := range observation.PostReceiptDigests {
			if !claimUniqueTranscriptionBinding(seenEffectReceipts, digest) {
				return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
			}
		}
		for _, digest := range observation.ModelCallReceiptDigests {
			if !claimUniqueTranscriptionBinding(seenEffectReceipts, digest) {
				return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
			}
		}
		if candidateDigest == "" {
			candidateDigest, buildDigest = observation.CandidateDigest, observation.BuildDigest
		} else if candidateDigest != observation.CandidateDigest || buildDigest != observation.BuildDigest {
			return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
		}
		seen[observation.ID] = struct{}{}
		platforms[observation.Platform] = true
		composers[observation.Composer] = true
		pairs[string(observation.Platform)+":"+string(observation.Composer)] = true
		if observation.EvidenceClass == DictationEvidencePhysical {
			physical++
		}
		if observation.Outcome == DictationSent {
			if observation.PostedAt.IsZero() || observation.PostedAt.Before(observation.SendRequestedAt) || observation.ModelCalls < 1 || observation.FirstAttemptSucceeded && observation.ModelCalls != 1 {
				return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
			}
			latencies = append(latencies, observation.PostedAt.Sub(observation.SendRequestedAt).Milliseconds())
			exactlyOnce = exactlyOnce && observation.PostCount == 1
			if observation.FirstAttemptSucceeded {
				firstAttempts++
			}
		} else {
			exactlyOnce = exactlyOnce && observation.PostCount == 0
			if observation.Outcome == DictationStopped {
				cancellationSafe = cancellationSafe && observation.ModelCalls == 0
			}
			if observation.Outcome == DictationDeleted || observation.Outcome == DictationEmpty || observation.Outcome == DictationErrored || observation.Outcome == DictationCancelled {
				cancellationSafe = cancellationSafe && observation.PostCount == 0
			}
		}
		if observation.CancellationRace {
			cancellations++
			cancellationSafe = cancellationSafe && observation.PostCount == 0
		}
		if observation.PersonalRealtimeHandoff || observation.MeetingHandoff {
			handoffs++
			micSafe = micSafe && !observation.MicGenerationOverlap
		}
		if observation.PersonalRealtimeHandoff {
			personalToDictate++
		}
		if observation.MeetingHandoff {
			personalToMeeting++
		}
		if observation.Composer == DictationComposerInRoom {
			inRoom++
			inRoomSafe = inRoomSafe && observation.InRoomTrackFramesLeaked == 0 && observation.InRoomTranscriptFramesLeak == 0
		}
		waveformSafe = waveformSafe && (observation.ReducedMotion || observation.WaveformFPS >= thresholds.MinimumWaveformFPS)
	}
	if len(latencies) == 0 {
		return ComposerDictationQualificationReceipt{}, ErrTranscriptionQualificationInvalid
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	platformList := sortedDictationPlatforms(platforms)
	composerList := sortedDictationComposers(composers)
	receipt := ComposerDictationQualificationReceipt{
		UtteranceCount:            len(observations),
		PhysicalTargetDeviceCount: physical,
		Platforms:                 platformList,
		ComposerSurfaces:          composerList,
		FirstAttemptSuccesses:     firstAttempts,
		FirstAttemptRate:          wilson95(firstAttempts, len(latencies)),
		P50SubmitToPostMS:         percentileNearestRank(latencies, .50),
		P95SubmitToPostMS:         percentileNearestRank(latencies, .95),
		P99SubmitToPostMS:         percentileNearestRank(latencies, .99),
		P50SubmitToPost:           deterministicQuantileBootstrap(latencies, .50, 400, "dictation_submit_to_post_p50_ms"),
		P95SubmitToPost:           deterministicQuantileBootstrap(latencies, .95, 400, "dictation_submit_to_post_p95_ms"),
		P99SubmitToPost:           deterministicQuantileBootstrap(latencies, .99, 400, "dictation_submit_to_post_p99_ms"),
		ExactlyOncePass:           exactlyOnce,
		CancellationPass:          cancellationSafe && cancellations >= thresholds.MinimumCancellationRaces,
		MicGenerationPass:         micSafe && handoffs >= thresholds.MinimumMicHandoffs,
		PersonalToDictateCount:    personalToDictate,
		PersonalToMeetingCount:    personalToMeeting,
		PlatformComposerPairs:     len(pairs),
		InRoomPrivacyPass:         inRoomSafe && inRoom >= thresholds.MinimumInRoomDictations,
		WaveformPass:              waveformSafe,
		FidelityPass:              fidelity.PreregisteredPass,
	}
	receipt.DeterministicPass = receipt.UtteranceCount >= thresholds.MinimumUtterances && hasAllDictationPlatforms(platforms) && hasAllDictationComposers(composers) && hasAllDictationPlatformComposerPairs(pairs) &&
		receipt.FirstAttemptRate.Low >= thresholds.MinimumFirstAttemptRate && receipt.P50SubmitToPost.High <= float64(thresholds.MaximumP50PostLatencyMS) && receipt.P95SubmitToPost.High <= float64(thresholds.MaximumP95PostLatencyMS) &&
		receipt.PersonalToDictateCount >= thresholds.MinimumPersonalToDictate && receipt.PersonalToMeetingCount >= thresholds.MinimumPersonalToMeeting && receipt.ExactlyOncePass && receipt.CancellationPass && receipt.MicGenerationPass && receipt.InRoomPrivacyPass && receipt.WaveformPass && receipt.FidelityPass
	// Never let a test table self-assert live/provider/device status.
	receipt.ProviderQualified = false
	receipt.DeviceQualified = false
	receipt.ResidualGates = []string{"trusted_provider_qualification", "trusted_physical_web_iphone_ipad_receipts", "consented_corpus_and_reviewer_signoff"}
	digest, err := composerDictationReceiptDigest(fidelity.ReceiptDigest, thresholds, receipt)
	if err != nil {
		return ComposerDictationQualificationReceipt{}, err
	}
	receipt.ReceiptDigest = digest
	return receipt, nil
}

func validDictationPlatform(value DictationTargetPlatform) bool {
	return value == DictationTargetWeb || value == DictationTargetIPhone || value == DictationTargetIPad
}

func validDictationComposer(value DictationComposerSurface) bool {
	return value == DictationComposerScout || value == DictationComposerPrivate || value == DictationComposerTeam || value == DictationComposerProject || value == DictationComposerInRoom
}

func validDictationEvidenceClass(value DictationEvidenceClass) bool {
	return value == DictationEvidenceSynthetic || value == DictationEvidenceLocal || value == DictationEvidencePhysical
}

func validDictationOutcome(value DictationTerminalOutcome) bool {
	return value == DictationSent || value == DictationStopped || value == DictationDeleted || value == DictationEmpty || value == DictationErrored || value == DictationCancelled
}

func validDictationEvidenceBindings(observation DictationQualificationObservation) bool {
	transcriptBound := observation.ModelCalls == 0 && observation.TranscriptReceiptDigest == "" || observation.ModelCalls > 0 && isHexDigest(observation.TranscriptReceiptDigest) && containsDictationDigest(observation.ModelCallReceiptDigests, observation.TranscriptReceiptDigest)
	if !isHexDigest(observation.AudioSHA256) || !transcriptBound || !isHexDigest(observation.LifecycleReceiptDigest) ||
		!isHexDigest(observation.DeviceReceiptDigest) || !isHexDigest(observation.CandidateDigest) || !isHexDigest(observation.BuildDigest) || !isHexDigest(observation.PerformanceReceiptDigest) ||
		!uniqueDictationDigests(observation.PostReceiptDigests) || !uniqueDictationDigests(observation.ModelCallReceiptDigests) {
		return false
	}
	return true
}

func containsDictationDigest(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func uniqueDictationDigests(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !isHexDigest(value) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validDictationBatchRef(ref DictationEvidenceBatchRef) bool {
	return strideIdentifier(ref.ID) && strideIdentifier(ref.TenantID) && isHexDigest(ref.RegistryDigest) && isHexDigest(ref.TargetDigest) && isHexDigest(ref.CandidateDigest) &&
		isHexDigest(ref.BuildDigest) && isHexDigest(ref.TranscriptionTargetDigest) && isHexDigest(ref.TranscriptionCorpusDigest) && isHexDigest(ref.TranscriptionReceiptDigest) &&
		isHexDigest(ref.TranscriptionObservationSetDigest) && isHexDigest(ref.TranscriptionCandidateDigest) && isHexDigest(ref.DictationCorpusDigest) && isHexDigest(ref.ObservationSetDigest) && isHexDigest(ref.ThresholdsDigest) && strings.TrimSpace(ref.Receipt) != ""
}

func dictationTranscriptionCorpusMatches(dictations []DictationQualificationObservation, manifest TranscriptionCorpusManifest, observations []TranscriptionObservation) bool {
	modelCalled := 0
	for _, dictation := range dictations {
		if dictation.ModelCalls > 0 {
			modelCalled++
		} else if dictation.TranscriptReceiptDigest != "" {
			return false
		}
	}
	if modelCalled < 250 || len(manifest.Cases) != modelCalled || len(observations) != modelCalled {
		return false
	}
	cases := make(map[string]TranscriptionCorpusCase, len(manifest.Cases))
	for _, testCase := range manifest.Cases {
		cases[testCase.ID] = testCase
	}
	transcripts := make(map[string]TranscriptionObservation, len(observations))
	for _, observation := range observations {
		if _, duplicate := transcripts[observation.CaseID]; duplicate {
			return false
		}
		transcripts[observation.CaseID] = observation
	}
	for _, dictation := range dictations {
		if dictation.ModelCalls == 0 {
			if _, caseOK := cases[dictation.ID]; caseOK {
				return false
			}
			if _, observationOK := transcripts[dictation.ID]; observationOK {
				return false
			}
			continue
		}
		testCase, caseOK := cases[dictation.ID]
		observation, observationOK := transcripts[dictation.ID]
		if !caseOK || !observationOK || testCase.AudioSHA256 != dictation.AudioSHA256 || observation.InputAudioSHA256 != dictation.AudioSHA256 ||
			observation.ProviderReceiptDigest != dictation.TranscriptReceiptDigest {
			return false
		}
	}
	return true
}

func dictationCorpusDigest(observations []DictationQualificationObservation) string {
	type corpusBinding struct {
		ID                      string                   `json:"id"`
		Platform                DictationTargetPlatform  `json:"platform"`
		Composer                DictationComposerSurface `json:"composer"`
		AudioSHA256             string                   `json:"audioSha256"`
		TranscriptReceiptDigest string                   `json:"transcriptReceiptDigest"`
		DeviceReceiptDigest     string                   `json:"deviceReceiptDigest"`
	}
	bindings := make([]corpusBinding, 0, len(observations))
	for _, observation := range observations {
		bindings = append(bindings, corpusBinding{observation.ID, observation.Platform, observation.Composer, observation.AudioSHA256, observation.TranscriptReceiptDigest, observation.DeviceReceiptDigest})
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
	return workDigest(bindings)
}

func dictationObservationSetDigest(observations []DictationQualificationObservation) string {
	clone := append([]DictationQualificationObservation(nil), observations...)
	sort.Slice(clone, func(i, j int) bool { return clone[i].ID < clone[j].ID })
	return workDigest(clone)
}

func composerDictationReceiptDigest(fidelityDigest string, thresholds ComposerDictationThresholds, receipt ComposerDictationQualificationReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return STRIDEContractDigest(struct {
		FidelityDigest string                                `json:"fidelityDigest"`
		Thresholds     ComposerDictationThresholds           `json:"thresholds"`
		Receipt        ComposerDictationQualificationReceipt `json:"receipt"`
	}{fidelityDigest, thresholds, receipt})
}

func sortedDictationPlatforms(values map[DictationTargetPlatform]bool) []DictationTargetPlatform {
	result := make([]DictationTargetPlatform, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func sortedDictationComposers(values map[DictationComposerSurface]bool) []DictationComposerSurface {
	result := make([]DictationComposerSurface, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func hasAllDictationPlatforms(values map[DictationTargetPlatform]bool) bool {
	return values[DictationTargetWeb] && values[DictationTargetIPhone] && values[DictationTargetIPad]
}

func hasAllDictationComposers(values map[DictationComposerSurface]bool) bool {
	return values[DictationComposerScout] && values[DictationComposerPrivate] && values[DictationComposerTeam] && values[DictationComposerProject] && values[DictationComposerInRoom]
}

func hasAllDictationPlatformComposerPairs(values map[string]bool) bool {
	for _, platform := range []DictationTargetPlatform{DictationTargetWeb, DictationTargetIPhone, DictationTargetIPad} {
		for _, composer := range []DictationComposerSurface{DictationComposerScout, DictationComposerPrivate, DictationComposerTeam, DictationComposerProject, DictationComposerInRoom} {
			if !values[string(platform)+":"+string(composer)] {
				return false
			}
		}
	}
	return true
}
