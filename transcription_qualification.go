package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

var (
	ErrTranscriptionQualificationInvalid    = errors.New("invalid transcription qualification input")
	ErrTranscriptionQualificationSynthetic  = errors.New("synthetic transcription evidence cannot qualify a live route")
	ErrTranscriptionQualificationUnverified = errors.New("transcription capability is not provider-qualified")
)

const (
	e10TranscriptionMinimumClips             = 120
	e10TranscriptionMinimumDurationMS        = int64(60 * 60 * 1000)
	e10TranscriptionMinimumCasesPerTag       = 10
	e10TranscriptionMaximumCorpusWER         = 0.10
	e10TranscriptionMaximumWERDelta          = 0.005
	e10TranscriptionMinimumDomainAccuracy    = 0.97
	e10TranscriptionMinimumNumericAccuracy   = 0.98
	e10TranscriptionMaximumP95FinalLatencyMS = int64(3000)
	e10TranscriptionMinimumIntegrityEvents   = 10_000
	e10TranscriptionMinimumBootstrapSamples  = 400
)

type TranscriptionPurpose string

const (
	TranscriptionPurposeMeetingAuthoritative TranscriptionPurpose = "meeting_authoritative"
	TranscriptionPurposeMeetingProvisional   TranscriptionPurpose = "meeting_provisional"
	TranscriptionPurposeComposerDictation    TranscriptionPurpose = "composer_dictation"
)

type TranscriptionTransport string

const (
	TranscriptionTransportRealtimeWebSocket TranscriptionTransport = "realtime_websocket"
	TranscriptionTransportFileHTTP          TranscriptionTransport = "file_http"
)

// TranscriptionCapabilitySpec is a frozen candidate declaration, not a claim
// about current provider availability. This package has no external provider
// trust-root verifier, so ProviderQualified and provider proof digests must
// remain unset.
type TranscriptionCapabilitySpec struct {
	Model                 string                 `json:"model"`
	Purpose               TranscriptionPurpose   `json:"purpose"`
	Transport             TranscriptionTransport `json:"transport"`
	EventSchemaVersion    string                 `json:"eventSchemaVersion"`
	PromptMode            string                 `json:"promptMode"`
	SupportsLanguageHint  bool                   `json:"supportsLanguageHint"`
	SupportsKeywordHints  bool                   `json:"supportsKeywordHints"`
	SupportsTimestamps    bool                   `json:"supportsTimestamps"`
	SupportsDiarization   bool                   `json:"supportsDiarization"`
	Streaming             bool                   `json:"streaming"`
	ProvisionalOnly       bool                   `json:"provisionalOnly"`
	ProviderQualified     bool                   `json:"providerQualified"`
	DocumentationDigest   string                 `json:"documentationDigest,omitempty"`
	ProviderReceiptDigest string                 `json:"providerReceiptDigest,omitempty"`
}

func (spec TranscriptionCapabilitySpec) Validate() error {
	if !strideIdentifier(spec.Model) || !oneOf(string(spec.Purpose), string(TranscriptionPurposeMeetingAuthoritative), string(TranscriptionPurposeMeetingProvisional), string(TranscriptionPurposeComposerDictation)) ||
		!oneOf(string(spec.Transport), string(TranscriptionTransportRealtimeWebSocket), string(TranscriptionTransportFileHTTP)) ||
		!strideIdentifier(spec.EventSchemaVersion) || !oneOf(spec.PromptMode, "none", "free_text", "keywords", "unknown") ||
		(spec.Purpose == TranscriptionPurposeMeetingAuthoritative && spec.ProvisionalOnly) ||
		(spec.Purpose == TranscriptionPurposeMeetingProvisional && !spec.ProvisionalOnly) ||
		(spec.Transport == TranscriptionTransportFileHTTP && spec.Streaming) || spec.ProviderQualified || spec.DocumentationDigest != "" || spec.ProviderReceiptDigest != "" {
		return ErrTranscriptionQualificationInvalid
	}
	return nil
}

// PlannedTranscriptionCandidate is deliberately conservative. The target
// names are represented so adapters and evals can be built without spending
// tokens, while every capability that still needs current documentation/live
// verification remains unknown or false rather than guessed.
func PlannedTranscriptionCandidate(model string, purpose TranscriptionPurpose) (TranscriptionCapabilitySpec, error) {
	model = strings.TrimSpace(model)
	spec := TranscriptionCapabilitySpec{Model: model, Purpose: purpose, EventSchemaVersion: "recorded-v1"}
	switch purpose {
	case TranscriptionPurposeMeetingAuthoritative:
		spec.Transport = TranscriptionTransportRealtimeWebSocket
		spec.Streaming = true
	case TranscriptionPurposeMeetingProvisional:
		spec.Transport = TranscriptionTransportRealtimeWebSocket
		spec.Streaming = true
		spec.ProvisionalOnly = true
	case TranscriptionPurposeComposerDictation:
		spec.Transport = TranscriptionTransportFileHTTP
	default:
		return TranscriptionCapabilitySpec{}, ErrTranscriptionQualificationInvalid
	}
	switch {
	case strings.Contains(strings.ToLower(model), "gpt-4o-transcribe"):
		spec.PromptMode = "free_text"
		spec.SupportsLanguageHint = true
	case strings.Contains(strings.ToLower(model), "whisper"):
		spec.PromptMode = "none"
		spec.SupportsLanguageHint = true
	case model == "gpt-transcribe", model == "gpt-live-transcribe":
		// These are planned aliases from the product design. Do not infer their
		// wire features from their names; E10 documentation + contract probes do
		// that one field at a time.
		spec.PromptMode = "unknown"
	default:
		spec.PromptMode = "unknown"
	}
	if err := spec.Validate(); err != nil {
		return TranscriptionCapabilitySpec{}, err
	}
	return spec, nil
}

type TranscriptionCorpusCase struct {
	ID               string   `json:"id"`
	AudioSHA256      string   `json:"audioSha256"`
	ReferenceText    string   `json:"referenceText"`
	Tags             []string `json:"tags"`
	RequiredTerms    []string `json:"requiredTerms,omitempty"`
	ExpectedSpeakers []string `json:"expectedSpeakers,omitempty"`
	DurationMS       int64    `json:"durationMs"`
}

func (testCase TranscriptionCorpusCase) Validate() error {
	silence := isTranscriptionSilenceCase(testCase)
	if !strideIdentifier(testCase.ID) || !isHexDigest(testCase.AudioSHA256) ||
		(silence && (len(testCase.Tags) != 1 || strings.TrimSpace(testCase.ReferenceText) != "" || len(testCase.RequiredTerms) != 0 || len(testCase.ExpectedSpeakers) != 0)) ||
		(!silence && strings.TrimSpace(testCase.ReferenceText) == "") ||
		testCase.DurationMS <= 0 || !validTranscriptionTags(testCase.Tags) || !uniqueNonEmptyStrings(testCase.RequiredTerms) ||
		!uniqueOptionalSTRIDEIDs(testCase.ExpectedSpeakers) {
		return ErrTranscriptionQualificationInvalid
	}
	if (hasTranscriptionTag(testCase.Tags, "company_name") && len(testCase.RequiredTerms) == 0) ||
		(hasTranscriptionTag(testCase.Tags, "numbers") && len(transcriptionNumberSet(testCase.ReferenceText)) == 0) ||
		(hasTranscriptionTag(testCase.Tags, "speaker_attribution") && len(testCase.ExpectedSpeakers) == 0) ||
		(hasTranscriptionTag(testCase.Tags, "crosstalk") && len(testCase.ExpectedSpeakers) < 2) ||
		(hasTranscriptionTag(testCase.Tags, "short_phrase") && len(transcriptionTokens(testCase.ReferenceText)) > 6) {
		return ErrTranscriptionQualificationInvalid
	}
	return nil
}

type TranscriptionCorpusManifest struct {
	Version int                       `json:"version"`
	Cases   []TranscriptionCorpusCase `json:"cases"`
	Digest  string                    `json:"digest"`
}

func (manifest TranscriptionCorpusManifest) Validate() error {
	if manifest.Version < 1 || len(manifest.Cases) == 0 || !isHexDigest(manifest.Digest) {
		return ErrTranscriptionQualificationInvalid
	}
	frozen, err := FreezeTranscriptionCorpus(manifest.Version, manifest.Cases)
	if err != nil || frozen.Digest != manifest.Digest {
		return ErrTranscriptionQualificationInvalid
	}
	return nil
}

func FreezeTranscriptionCorpus(version int, cases []TranscriptionCorpusCase) (TranscriptionCorpusManifest, error) {
	if version < 1 || len(cases) == 0 {
		return TranscriptionCorpusManifest{}, ErrTranscriptionQualificationInvalid
	}
	clone := append([]TranscriptionCorpusCase(nil), cases...)
	seen := make(map[string]struct{}, len(clone))
	seenAudio := make(map[string]struct{}, len(clone))
	for index := range clone {
		clone[index].Tags = sortedTranscriptionStrings(clone[index].Tags)
		clone[index].RequiredTerms = sortedTranscriptionStrings(clone[index].RequiredTerms)
		clone[index].ExpectedSpeakers = sortedTranscriptionStrings(clone[index].ExpectedSpeakers)
		if clone[index].Validate() != nil {
			return TranscriptionCorpusManifest{}, ErrTranscriptionQualificationInvalid
		}
		if _, duplicate := seen[clone[index].ID]; duplicate {
			return TranscriptionCorpusManifest{}, ErrTranscriptionQualificationInvalid
		}
		if _, duplicate := seenAudio[clone[index].AudioSHA256]; duplicate {
			return TranscriptionCorpusManifest{}, ErrTranscriptionQualificationInvalid
		}
		seen[clone[index].ID] = struct{}{}
		seenAudio[clone[index].AudioSHA256] = struct{}{}
	}
	sort.Slice(clone, func(i, j int) bool { return clone[i].ID < clone[j].ID })
	digest, err := STRIDEContractDigest(struct {
		Version int                       `json:"version"`
		Cases   []TranscriptionCorpusCase `json:"cases"`
	}{version, clone})
	if err != nil {
		return TranscriptionCorpusManifest{}, err
	}
	return TranscriptionCorpusManifest{Version: version, Cases: clone, Digest: digest}, nil
}

type TranscriptionObservation struct {
	CaseID                   string    `json:"caseId"`
	Model                    string    `json:"model"`
	RouteDigest              string    `json:"routeDigest"`
	OutputText               string    `json:"outputText"`
	IncumbentOutputText      string    `json:"incumbentOutputText,omitempty"`
	SegmentID                string    `json:"segmentId"`
	ProviderItemIDHash       string    `json:"providerItemIdHash"`
	InputAudioSHA256         string    `json:"inputAudioSha256,omitempty"`
	TrackID                  string    `json:"trackId,omitempty"`
	ConsentReceiptDigest     string    `json:"consentReceiptDigest,omitempty"`
	ObservedSpeakers         []string  `json:"observedSpeakers,omitempty"`
	CommittedAt              time.Time `json:"committedAt"`
	FinalAt                  time.Time `json:"finalAt"`
	AcceptedOutputCostMicros int64     `json:"acceptedOutputCostMicros"`
	Synthetic                bool      `json:"synthetic"`
	ProviderReceiptDigest    string    `json:"providerReceiptDigest,omitempty"`
}

type TranscriptionCaseScore struct {
	CaseID              string  `json:"caseId"`
	WordErrorRate       float64 `json:"wordErrorRate"`
	WordErrors          int     `json:"wordErrors"`
	ReferenceWords      int     `json:"referenceWords"`
	RequiredTermRecall  float64 `json:"requiredTermRecall"`
	RequiredTermHits    int     `json:"requiredTermHits"`
	RequiredTermCount   int     `json:"requiredTermCount"`
	SpeakerMatch        bool    `json:"speakerMatch"`
	SegmentBound        bool    `json:"segmentBound"`
	FinalLatencyMS      int64   `json:"finalLatencyMs"`
	CostMicros          int64   `json:"costMicros"`
	Silence             bool    `json:"silence"`
	FalseInsertionWords int     `json:"falseInsertionWords"`
}

func ScoreTranscriptionCase(testCase TranscriptionCorpusCase, observation TranscriptionObservation) (TranscriptionCaseScore, error) {
	if testCase.Validate() != nil || observation.CaseID != testCase.ID || !strideIdentifier(observation.Model) || !isHexDigest(observation.RouteDigest) ||
		observation.CommittedAt.IsZero() || observation.FinalAt.Before(observation.CommittedAt) || observation.AcceptedOutputCostMicros < 0 ||
		(observation.ProviderReceiptDigest != "" && !isHexDigest(observation.ProviderReceiptDigest)) {
		return TranscriptionCaseScore{}, ErrTranscriptionQualificationInvalid
	}
	reference := transcriptionTokens(testCase.ReferenceText)
	output := transcriptionTokens(observation.OutputText)
	silence := isTranscriptionSilenceCase(testCase)
	if len(reference) == 0 && !silence {
		return TranscriptionCaseScore{}, ErrTranscriptionQualificationInvalid
	}
	matchedTerms := 0
	normalizedOutput := " " + strings.Join(output, " ") + " "
	for _, term := range testCase.RequiredTerms {
		needle := " " + strings.Join(transcriptionTokens(term), " ") + " "
		if strings.TrimSpace(needle) != "" && strings.Contains(normalizedOutput, needle) {
			matchedTerms++
		}
	}
	termRecall := 1.0
	if len(testCase.RequiredTerms) > 0 {
		termRecall = float64(matchedTerms) / float64(len(testCase.RequiredTerms))
	}
	wantSpeakers := sortedTranscriptionStrings(testCase.ExpectedSpeakers)
	gotSpeakers := sortedTranscriptionStrings(observation.ObservedSpeakers)
	wordErrors := 0
	wordErrorRate := 0.0
	falseInsertionWords := 0
	if silence {
		falseInsertionWords = len(output)
	} else {
		wordErrors = wordEditDistance(reference, output)
		wordErrorRate = float64(wordErrors) / float64(len(reference))
	}
	return TranscriptionCaseScore{
		CaseID:              testCase.ID,
		WordErrorRate:       wordErrorRate,
		WordErrors:          wordErrors,
		ReferenceWords:      len(reference),
		RequiredTermRecall:  termRecall,
		RequiredTermHits:    matchedTerms,
		RequiredTermCount:   len(testCase.RequiredTerms),
		SpeakerMatch:        equalStrings(wantSpeakers, gotSpeakers),
		SegmentBound:        strideIdentifier(observation.SegmentID) && isHexDigest(observation.ProviderItemIDHash),
		FinalLatencyMS:      observation.FinalAt.Sub(observation.CommittedAt).Milliseconds(),
		CostMicros:          observation.AcceptedOutputCostMicros,
		Silence:             silence,
		FalseInsertionWords: falseInsertionWords,
	}, nil
}

type TranscriptionQualificationThresholds struct {
	MaxMeanWER            float64 `json:"maxMeanWer"`
	MinRequiredTermRecall float64 `json:"minRequiredTermRecall"`
	MaxP95FinalLatencyMS  int64   `json:"maxP95FinalLatencyMs"`
	RequireSpeakerMatch   bool    `json:"requireSpeakerMatch"`
	RequireSegmentBinding bool    `json:"requireSegmentBinding"`
}

type TranscriptionQualificationReceipt struct {
	CorpusDigest             string                   `json:"corpusDigest"`
	ThresholdsDigest         string                   `json:"thresholdsDigest"`
	RouteDigest              string                   `json:"routeDigest"`
	Model                    string                   `json:"model"`
	Scores                   []TranscriptionCaseScore `json:"scores"`
	MeanWER                  float64                  `json:"meanWer"`
	MeanRequiredTermRecall   float64                  `json:"meanRequiredTermRecall"`
	P95FinalLatencyMS        int64                    `json:"p95FinalLatencyMs"`
	AcceptedOutputCostMicros int64                    `json:"acceptedOutputCostMicros"`
	SilenceCaseCount         int                      `json:"silenceCaseCount"`
	SilenceFalseInsertions   int                      `json:"silenceFalseInsertions"`
	SilencePass              bool                     `json:"silencePass"`
	DeterministicPass        bool                     `json:"deterministicPass"`
	ProviderQualified        bool                     `json:"providerQualified"`
	ReceiptDigest            string                   `json:"receiptDigest"`
}

type TranscriptionProviderAttemptRef struct {
	ID                   string `json:"id"`
	TenantID             string `json:"tenantId"`
	CorpusDigest         string `json:"corpusDigest"`
	CaseID               string `json:"caseId"`
	InputAudioSHA256     string `json:"inputAudioSha256"`
	Model                string `json:"model"`
	RouteDigest          string `json:"routeDigest"`
	SegmentID            string `json:"segmentId"`
	ProviderItemIDHash   string `json:"providerItemIdHash"`
	TrackID              string `json:"trackId"`
	ConsentReceiptDigest string `json:"consentReceiptDigest"`
	Receipt              string `json:"-"`
}

// TranscriptionEvidenceTargetRef identifies one preregistered target packet
// held by the durable local evidence store. The opaque Receipt is consumed
// exactly once; this protects structure and replay integrity but does not prove
// provider provenance.
type TranscriptionEvidenceTargetRef struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenantId"`
	RegistryDigest   string `json:"registryDigest"`
	TargetDigest     string `json:"targetDigest"`
	CandidateDigest  string `json:"candidateDigest"`
	CorpusDigest     string `json:"corpusDigest"`
	ThresholdsDigest string `json:"thresholdsDigest"`
	Receipt          string `json:"-"`
}

// StoredTranscriptionEvidenceTarget is a locally supplied evidence packet.
// OperatorSignatureDigest and ReviewerSignatureDigest are structural bindings
// only; this package does not verify either signature against an external trust
// root and therefore cannot promote this packet to provider-qualified status.
type StoredTranscriptionEvidenceTarget struct {
	Ref                     TranscriptionEvidenceTargetRef  `json:"ref"`
	Thresholds              TranscriptionEvidenceThresholds `json:"thresholds"`
	IntegrityBindings       []TranscriptionIntegrityBinding `json:"integrityBindings"`
	IntegrityEvents         []TranscriptionIntegrityEvent   `json:"integrityEvents"`
	OperatorSignatureDigest string                          `json:"operatorSignatureDigest"`
	ReviewerSignatureDigest string                          `json:"reviewerSignatureDigest"`
}

const TranscriptionEvidenceCandidateState = "local_structure_only_evidence_candidate"

// TranscriptionQualificationCandidate records that locally assembled evidence
// passed the preregistered statistical and relationship checks. It is not a
// provider qualification and is intentionally not signed by this process.
// Promotion remains impossible until a separate verifier can validate an
// independently rooted provider project/billing/contract/usage receipt and the
// corresponding consent authority.
type TranscriptionQualificationCandidate struct {
	TenantID string                       `json:"tenantId"`
	State    string                       `json:"state"`
	Receipt  TranscriptionEvidenceReceipt `json:"receipt"`
}

func EvaluateTranscriptionCandidate(manifest TranscriptionCorpusManifest, observations []TranscriptionObservation, thresholds TranscriptionQualificationThresholds) (TranscriptionQualificationReceipt, error) {
	return evaluateTranscriptionCandidate(manifest, observations, thresholds)
}

func evaluateTranscriptionCandidate(manifest TranscriptionCorpusManifest, observations []TranscriptionObservation, thresholds TranscriptionQualificationThresholds) (TranscriptionQualificationReceipt, error) {
	if manifest.Validate() != nil || len(observations) != len(manifest.Cases) ||
		thresholds.MaxMeanWER < 0 || thresholds.MaxMeanWER > 1 || thresholds.MinRequiredTermRecall < 0 || thresholds.MinRequiredTermRecall > 1 || thresholds.MaxP95FinalLatencyMS <= 0 {
		return TranscriptionQualificationReceipt{}, ErrTranscriptionQualificationInvalid
	}
	byCase := make(map[string]TranscriptionObservation, len(observations))
	for _, observation := range observations {
		if _, duplicate := byCase[observation.CaseID]; duplicate {
			return TranscriptionQualificationReceipt{}, ErrTranscriptionQualificationInvalid
		}
		byCase[observation.CaseID] = observation
	}
	receipt := TranscriptionQualificationReceipt{CorpusDigest: manifest.Digest, ThresholdsDigest: workDigest(thresholds)}
	latencies := make([]int64, 0, len(manifest.Cases))
	allSpeaker, allBound := true, true
	totalWordErrors, totalReferenceWords := 0, 0
	totalTermHits, totalRequiredTerms := 0, 0
	for _, testCase := range manifest.Cases {
		observation, found := byCase[testCase.ID]
		if !found {
			return TranscriptionQualificationReceipt{}, ErrTranscriptionQualificationInvalid
		}
		if receipt.Model == "" {
			receipt.Model, receipt.RouteDigest = observation.Model, observation.RouteDigest
		} else if receipt.Model != observation.Model || receipt.RouteDigest != observation.RouteDigest {
			return TranscriptionQualificationReceipt{}, ErrTranscriptionQualificationInvalid
		}
		score, err := ScoreTranscriptionCase(testCase, observation)
		if err != nil {
			return TranscriptionQualificationReceipt{}, err
		}
		receipt.Scores = append(receipt.Scores, score)
		if score.Silence {
			receipt.SilenceCaseCount++
			receipt.SilenceFalseInsertions += score.FalseInsertionWords
		} else {
			totalWordErrors += score.WordErrors
			totalReferenceWords += score.ReferenceWords
		}
		totalTermHits += score.RequiredTermHits
		totalRequiredTerms += score.RequiredTermCount
		receipt.AcceptedOutputCostMicros += score.CostMicros
		latencies = append(latencies, score.FinalLatencyMS)
		allSpeaker = allSpeaker && score.SpeakerMatch
		allBound = allBound && score.SegmentBound
	}
	sort.Slice(receipt.Scores, func(i, j int) bool { return receipt.Scores[i].CaseID < receipt.Scores[j].CaseID })
	receipt.MeanWER = ratioInts(totalWordErrors, totalReferenceWords)
	receipt.MeanRequiredTermRecall = 1
	if totalRequiredTerms > 0 {
		receipt.MeanRequiredTermRecall = float64(totalTermHits) / float64(totalRequiredTerms)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	receipt.P95FinalLatencyMS = percentileNearestRank(latencies, 0.95)
	receipt.SilencePass = receipt.SilenceFalseInsertions == 0
	receipt.DeterministicPass = receipt.MeanWER <= thresholds.MaxMeanWER && receipt.MeanRequiredTermRecall >= thresholds.MinRequiredTermRecall &&
		receipt.P95FinalLatencyMS <= thresholds.MaxP95FinalLatencyMS && receipt.SilencePass && (!thresholds.RequireSpeakerMatch || allSpeaker) && (!thresholds.RequireSegmentBinding || allBound)
	receipt.ProviderQualified = false
	digest, err := STRIDEContractDigest(struct {
		CorpusDigest      string                   `json:"corpusDigest"`
		ThresholdsDigest  string                   `json:"thresholdsDigest"`
		RouteDigest       string                   `json:"routeDigest"`
		Model             string                   `json:"model"`
		Scores            []TranscriptionCaseScore `json:"scores"`
		DeterministicPass bool                     `json:"deterministicPass"`
		ProviderQualified bool                     `json:"providerQualified"`
	}{receipt.CorpusDigest, receipt.ThresholdsDigest, receipt.RouteDigest, receipt.Model, receipt.Scores, receipt.DeterministicPass, receipt.ProviderQualified})
	if err != nil {
		return TranscriptionQualificationReceipt{}, err
	}
	receipt.ReceiptDigest = digest
	return receipt, nil
}

// EvaluateLiveTranscriptionCandidate resolves every observation and the full
// preregistered target from a durable, one-use local evidence store before
// running the 120-clip, 60-minute, non-inferiority, accuracy, latency and
// 10,000-event integrity gates. The result remains an explicit candidate:
// opaque provider-attempt digests are not proof of the intended provider
// project, billing account, current contract, usage, cost, or consent trust
// root, so this function has no provider-promotion path.
func EvaluateLiveTranscriptionCandidate(ctx context.Context, tenantID string, manifest TranscriptionCorpusManifest, attempts []TranscriptionProviderAttemptRef, targetRef TranscriptionEvidenceTargetRef, store *QualificationEvidenceStore) (TranscriptionQualificationCandidate, error) {
	if store == nil || !strideIdentifier(tenantID) || manifest.Validate() != nil || len(attempts) != len(manifest.Cases) ||
		!validTranscriptionTargetRef(targetRef) || targetRef.TenantID != tenantID || targetRef.CorpusDigest != manifest.Digest {
		return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
	}
	target, err := store.ConsumeEvidenceTarget(ctx, targetRef)
	if err != nil || !sameTranscriptionTargetRef(target.Ref, targetRef) || workDigest(target.Thresholds) != targetRef.ThresholdsDigest ||
		!isHexDigest(target.OperatorSignatureDigest) || !isHexDigest(target.ReviewerSignatureDigest) || target.OperatorSignatureDigest == target.ReviewerSignatureDigest {
		return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
	}
	cases := make(map[string]TranscriptionCorpusCase, len(manifest.Cases))
	for _, testCase := range manifest.Cases {
		cases[testCase.ID] = testCase
	}
	observations := make([]TranscriptionObservation, 0, len(attempts))
	seenAttempts := make(map[string]struct{}, len(attempts))
	seenCases := make(map[string]struct{}, len(attempts))
	seenProviderItems := make(map[string]struct{}, len(attempts))
	seenProviderReceipts := make(map[string]struct{}, len(attempts))
	for _, attempt := range attempts {
		testCase, knownCase := cases[attempt.CaseID]
		if !knownCase || !validTranscriptionAttemptRef(attempt, tenantID, manifest.Digest, testCase) {
			return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
		}
		if _, duplicate := seenAttempts[attempt.ID]; duplicate {
			return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
		}
		if _, duplicate := seenCases[attempt.CaseID]; duplicate {
			return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
		}
		if _, duplicate := seenProviderItems[attempt.ProviderItemIDHash]; duplicate {
			return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
		}
		seenAttempts[attempt.ID] = struct{}{}
		seenCases[attempt.CaseID] = struct{}{}
		seenProviderItems[attempt.ProviderItemIDHash] = struct{}{}
		observation, consumeErr := store.ConsumeProviderAttempt(ctx, attempt)
		if consumeErr != nil || !observationMatchesAttempt(observation, attempt) || observation.Synthetic || !isHexDigest(observation.ProviderReceiptDigest) {
			return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
		}
		if _, duplicate := seenProviderReceipts[observation.ProviderReceiptDigest]; duplicate {
			return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
		}
		seenProviderReceipts[observation.ProviderReceiptDigest] = struct{}{}
		observations = append(observations, observation)
	}
	evidence := TranscriptionQualificationEvidence{Observations: observations, IntegrityBindings: target.IntegrityBindings, IntegrityEvents: target.IntegrityEvents}
	receipt, err := evaluateTranscriptionEvidence(manifest, evidence, target.Thresholds, true)
	if err != nil || !receipt.PreregisteredPass || receipt.LiveOrDeviceQualified || receipt.Score.ProviderQualified ||
		!transcriptionIntegrityMatchesCandidate(target.IntegrityBindings, target.IntegrityEvents, tenantID, targetRef.CandidateDigest, receipt.Score.Model, receipt.Score.RouteDigest) {
		return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
	}
	receipt.RegistryDigest = targetRef.RegistryDigest
	receipt.TargetDigest = targetRef.TargetDigest
	receipt.CandidateDigest = targetRef.CandidateDigest
	receipt.ProviderAttemptSetDigest = transcriptionProviderAttemptSetDigest(attempts)
	receipt.OperatorSignatureDigest = target.OperatorSignatureDigest
	receipt.ReviewerSignatureDigest = target.ReviewerSignatureDigest
	if !isHexDigest(receipt.EvidenceThresholdsDigest) || !isHexDigest(receipt.ObservationSetDigest) || !isHexDigest(receipt.ProviderAttemptSetDigest) {
		return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
	}
	receipt.ReceiptDigest = ""
	receipt.ReceiptDigest, err = transcriptionEvidenceReceiptDigest(manifest.Digest, target.Thresholds, receipt)
	if err != nil {
		return TranscriptionQualificationCandidate{}, ErrTranscriptionQualificationUnverified
	}
	return TranscriptionQualificationCandidate{TenantID: tenantID, State: TranscriptionEvidenceCandidateState, Receipt: receipt}, nil
}

func validTranscriptionTargetRef(ref TranscriptionEvidenceTargetRef) bool {
	return strideIdentifier(ref.ID) && strideIdentifier(ref.TenantID) && isHexDigest(ref.RegistryDigest) && isHexDigest(ref.TargetDigest) &&
		isHexDigest(ref.CandidateDigest) && isHexDigest(ref.CorpusDigest) && isHexDigest(ref.ThresholdsDigest) && strings.TrimSpace(ref.Receipt) != ""
}

func sameTranscriptionTargetRef(left, right TranscriptionEvidenceTargetRef) bool {
	return left == right
}

func validTranscriptionAttemptRef(ref TranscriptionProviderAttemptRef, tenantID, corpusDigest string, testCase TranscriptionCorpusCase) bool {
	return strideIdentifier(ref.ID) && ref.TenantID == tenantID && ref.CorpusDigest == corpusDigest && ref.CaseID == testCase.ID && ref.InputAudioSHA256 == testCase.AudioSHA256 &&
		strideIdentifier(ref.Model) && isHexDigest(ref.RouteDigest) && strideIdentifier(ref.SegmentID) && isHexDigest(ref.ProviderItemIDHash) && strideIdentifier(ref.TrackID) &&
		isHexDigest(ref.ConsentReceiptDigest) && strings.TrimSpace(ref.Receipt) != ""
}

func observationMatchesAttempt(observation TranscriptionObservation, ref TranscriptionProviderAttemptRef) bool {
	return observation.CaseID == ref.CaseID && observation.InputAudioSHA256 == ref.InputAudioSHA256 && observation.Model == ref.Model && observation.RouteDigest == ref.RouteDigest &&
		observation.SegmentID == ref.SegmentID && observation.ProviderItemIDHash == ref.ProviderItemIDHash && observation.TrackID == ref.TrackID && observation.ConsentReceiptDigest == ref.ConsentReceiptDigest
}

func validTranscriptionTags(tags []string) bool {
	if len(tags) == 0 {
		return false
	}
	allowed := map[string]bool{"company_name": true, "numbers": true, "accent": true, "code_switching": true, "short_phrase": true, "crosstalk": true, "noise": true, "silence": true, "speaker_attribution": true}
	seen := map[string]bool{}
	for _, tag := range tags {
		if !allowed[tag] || seen[tag] {
			return false
		}
		seen[tag] = true
	}
	return true
}

func isTranscriptionSilenceCase(testCase TranscriptionCorpusCase) bool {
	return hasTranscriptionTag(testCase.Tags, "silence")
}

func hasTranscriptionTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

func validE10TranscriptionCorpusCoverage(manifest TranscriptionCorpusManifest) bool {
	required := []string{"company_name", "numbers", "accent", "code_switching", "short_phrase", "crosstalk", "noise", "silence", "speaker_attribution"}
	counts := make(map[string]int, len(required))
	for _, testCase := range manifest.Cases {
		for _, tag := range testCase.Tags {
			counts[tag]++
		}
	}
	for _, tag := range required {
		if counts[tag] < e10TranscriptionMinimumCasesPerTag {
			return false
		}
	}
	return true
}

func transcriptionTokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(strings.TrimSpace(text)), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

func wordEditDistance(left, right []string) int {
	prior := make([]int, len(right)+1)
	for index := range prior {
		prior[index] = index
	}
	for i, want := range left {
		current := make([]int, len(right)+1)
		current[0] = i + 1
		for j, got := range right {
			cost := 1
			if want == got {
				cost = 0
			}
			current[j+1] = minInt(current[j]+1, prior[j+1]+1, prior[j]+cost)
		}
		prior = current
	}
	return prior[len(right)]
}

func percentileNearestRank(values []int64, percentile float64) int64 {
	if len(values) == 0 || percentile <= 0 || percentile > 1 {
		return 0
	}
	index := int(float64(len(values))*percentile+0.999999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func minInt(values ...int) int {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func uniqueNonEmptyStrings(values []string) bool {
	if len(values) == 0 {
		return true
	}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func uniqueOptionalSTRIDEIDs(values []string) bool {
	if len(values) == 0 {
		return true
	}
	return uniqueSTRIDEIDs(values)
}

func sortedTranscriptionStrings(values []string) []string {
	clone := append([]string(nil), values...)
	sort.Strings(clone)
	return clone
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

func containsTranscriptionSpeaker(speakers []string, want string) bool {
	for _, speaker := range speakers {
		if speaker == want {
			return true
		}
	}
	return false
}

// ValidateTranscriptionPurpose isolates optional live captions from the
// authoritative meeting record. A provisional route can never be selected as
// the durable source, even if its latency score is better.
func ValidateTranscriptionPurpose(spec TranscriptionCapabilitySpec, want TranscriptionPurpose) error {
	if spec.Validate() != nil || spec.Purpose != want {
		return ErrTranscriptionQualificationInvalid
	}
	if want == TranscriptionPurposeMeetingAuthoritative && spec.ProvisionalOnly {
		return fmt.Errorf("%w: provisional captions cannot become authoritative", ErrTranscriptionQualificationInvalid)
	}
	return nil
}

// QualificationMetricInterval records a deterministic 95% interval. Rate
// metrics use Wilson intervals; latency and WER aggregates use a deterministic
// bootstrap. The method is carried in the receipt so a local fixture cannot be
// mistaken for a statistical claim made by a provider or a device lab.
type QualificationMetricInterval struct {
	Point       float64 `json:"point"`
	Low         float64 `json:"low"`
	High        float64 `json:"high"`
	Numerator   int     `json:"numerator,omitempty"`
	Denominator int     `json:"denominator,omitempty"`
	Method      string  `json:"method"`
}

type TranscriptionIntegrityEvent struct {
	Sequence              int64  `json:"sequence"`
	TenantID              string `json:"tenantId"`
	CandidateDigest       string `json:"candidateDigest"`
	Model                 string `json:"model"`
	RouteDigest           string `json:"routeDigest"`
	AttemptID             string `json:"attemptId"`
	CaseID                string `json:"caseId"`
	InputAudioSHA256      string `json:"inputAudioSha256"`
	SegmentID             string `json:"segmentId"`
	ProviderItemIDHash    string `json:"providerItemIdHash"`
	ProviderReceiptDigest string `json:"providerReceiptDigest"`
	TrackID               string `json:"trackId"`
	ConsentReceiptDigest  string `json:"consentReceiptDigest"`
	SpeakerID             string `json:"speakerId,omitempty"`
	Terminal              bool   `json:"terminal"`
}

// TranscriptionIntegrityBinding is the preregistered expected identity for one
// terminal. It is distinct from the observed event so the evaluator checks a
// relationship rather than trusting self-described identifiers.
type TranscriptionIntegrityBinding struct {
	Sequence              int64  `json:"sequence"`
	TenantID              string `json:"tenantId"`
	CandidateDigest       string `json:"candidateDigest"`
	Model                 string `json:"model"`
	RouteDigest           string `json:"routeDigest"`
	AttemptID             string `json:"attemptId"`
	CaseID                string `json:"caseId"`
	InputAudioSHA256      string `json:"inputAudioSha256"`
	SegmentID             string `json:"segmentId"`
	ProviderItemIDHash    string `json:"providerItemIdHash"`
	ProviderReceiptDigest string `json:"providerReceiptDigest"`
	TrackID               string `json:"trackId"`
	ConsentReceiptDigest  string `json:"consentReceiptDigest"`
	SpeakerID             string `json:"speakerId,omitempty"`
}

// TranscriptionIntegrityReceipt contains no transcript body. It proves only
// that an out-of-order terminal-event replay remained bound to its segment,
// consent and media track. Events need not arrive in sequence order, but their
// sequence numbers must be unique and every supplied event must be terminal.
type TranscriptionIntegrityReceipt struct {
	EventCount         int    `json:"eventCount"`
	UniqueSequences    int    `json:"uniqueSequences"`
	ContiguousSequence bool   `json:"contiguousSequence"`
	AllTerminal        bool   `json:"allTerminal"`
	AllCaseBound       bool   `json:"allCaseBound"`
	AllSegmentBound    bool   `json:"allSegmentBound"`
	AllTrackBound      bool   `json:"allTrackBound"`
	AllConsentBound    bool   `json:"allConsentBound"`
	AllAttemptBound    bool   `json:"allAttemptBound"`
	AllProviderBound   bool   `json:"allProviderBound"`
	AllSpeakerBound    bool   `json:"allSpeakerBound"`
	AllContextBound    bool   `json:"allContextBound"`
	AllAudioBound      bool   `json:"allAudioBound"`
	DeterministicPass  bool   `json:"deterministicPass"`
	Digest             string `json:"digest"`
}

// EvaluateTranscriptionIntegrity deliberately accepts shuffled input. This is
// the deterministic local harness for the 10,000-completion order gate; it is
// not a substitute for a consented live capture replay.
func EvaluateTranscriptionIntegrity(manifest TranscriptionCorpusManifest, bindings []TranscriptionIntegrityBinding, events []TranscriptionIntegrityEvent, minimumEvents int) (TranscriptionIntegrityReceipt, error) {
	if manifest.Validate() != nil || minimumEvents < 1 || len(events) < minimumEvents || len(bindings) != len(events) {
		return TranscriptionIntegrityReceipt{}, ErrTranscriptionQualificationInvalid
	}
	cases := make(map[string]TranscriptionCorpusCase, len(manifest.Cases))
	for _, testCase := range manifest.Cases {
		cases[testCase.ID] = testCase
	}
	receipt := TranscriptionIntegrityReceipt{EventCount: len(events), AllTerminal: true, AllCaseBound: true, AllSegmentBound: true, AllTrackBound: true, AllConsentBound: true, AllAttemptBound: true, AllProviderBound: true, AllSpeakerBound: true, AllContextBound: true, AllAudioBound: true}
	expected := make(map[int64]TranscriptionIntegrityBinding, len(bindings))
	uniqueAttempts := make(map[string]struct{}, len(bindings))
	uniqueSegments := make(map[string]struct{}, len(bindings))
	uniqueItems := make(map[string]struct{}, len(bindings))
	uniqueProviderReceipts := make(map[string]struct{}, len(bindings))
	uniqueTracks := make(map[string]struct{}, len(bindings))
	uniqueConsent := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		testCase, knownCase := cases[binding.CaseID]
		if !knownCase || binding.Sequence < 1 || !strideIdentifier(binding.TenantID) || !isHexDigest(binding.CandidateDigest) || !strideIdentifier(binding.Model) || !isHexDigest(binding.RouteDigest) ||
			binding.InputAudioSHA256 != testCase.AudioSHA256 || !strideIdentifier(binding.AttemptID) || !strideIdentifier(binding.SegmentID) ||
			!isHexDigest(binding.ProviderItemIDHash) || !isHexDigest(binding.ProviderReceiptDigest) || !strideIdentifier(binding.TrackID) || !isHexDigest(binding.ConsentReceiptDigest) {
			return TranscriptionIntegrityReceipt{}, ErrTranscriptionQualificationInvalid
		}
		if isTranscriptionSilenceCase(testCase) {
			if binding.SpeakerID != "" {
				return TranscriptionIntegrityReceipt{}, ErrTranscriptionQualificationInvalid
			}
		} else if !strideIdentifier(binding.SpeakerID) || !containsTranscriptionSpeaker(testCase.ExpectedSpeakers, binding.SpeakerID) {
			return TranscriptionIntegrityReceipt{}, ErrTranscriptionQualificationInvalid
		}
		if _, duplicate := expected[binding.Sequence]; duplicate {
			return TranscriptionIntegrityReceipt{}, ErrTranscriptionQualificationInvalid
		}
		if !claimUniqueTranscriptionBinding(uniqueAttempts, binding.AttemptID) || !claimUniqueTranscriptionBinding(uniqueSegments, binding.SegmentID) ||
			!claimUniqueTranscriptionBinding(uniqueItems, binding.ProviderItemIDHash) || !claimUniqueTranscriptionBinding(uniqueProviderReceipts, binding.ProviderReceiptDigest) ||
			!claimUniqueTranscriptionBinding(uniqueTracks, binding.TrackID) || !claimUniqueTranscriptionBinding(uniqueConsent, binding.ConsentReceiptDigest) {
			return TranscriptionIntegrityReceipt{}, ErrTranscriptionQualificationInvalid
		}
		expected[binding.Sequence] = binding
	}
	sequences := make(map[int64]struct{}, len(events))
	for _, event := range events {
		if _, duplicate := sequences[event.Sequence]; duplicate || event.Sequence < 1 {
			return TranscriptionIntegrityReceipt{}, ErrTranscriptionQualificationInvalid
		}
		sequences[event.Sequence] = struct{}{}
		binding, knownBinding := expected[event.Sequence]
		_, knownCase := cases[event.CaseID]
		receipt.AllTerminal = receipt.AllTerminal && event.Terminal
		receipt.AllCaseBound = receipt.AllCaseBound && knownCase && knownBinding && event.CaseID == binding.CaseID
		receipt.AllContextBound = receipt.AllContextBound && knownBinding && event.TenantID == binding.TenantID && event.CandidateDigest == binding.CandidateDigest && event.Model == binding.Model && event.RouteDigest == binding.RouteDigest
		receipt.AllAudioBound = receipt.AllAudioBound && knownBinding && event.InputAudioSHA256 == binding.InputAudioSHA256
		receipt.AllAttemptBound = receipt.AllAttemptBound && knownBinding && event.AttemptID == binding.AttemptID
		receipt.AllSegmentBound = receipt.AllSegmentBound && knownBinding && event.SegmentID == binding.SegmentID && event.ProviderItemIDHash == binding.ProviderItemIDHash
		receipt.AllProviderBound = receipt.AllProviderBound && knownBinding && event.ProviderReceiptDigest == binding.ProviderReceiptDigest
		receipt.AllTrackBound = receipt.AllTrackBound && knownBinding && event.TrackID == binding.TrackID
		receipt.AllConsentBound = receipt.AllConsentBound && knownBinding && event.ConsentReceiptDigest == binding.ConsentReceiptDigest
		receipt.AllSpeakerBound = receipt.AllSpeakerBound && knownBinding && event.SpeakerID == binding.SpeakerID
	}
	receipt.UniqueSequences = len(sequences)
	receipt.ContiguousSequence = true
	for sequence := 1; sequence <= receipt.EventCount; sequence++ {
		if _, found := sequences[int64(sequence)]; !found {
			receipt.ContiguousSequence = false
			break
		}
	}
	receipt.DeterministicPass = receipt.EventCount >= minimumEvents && receipt.UniqueSequences == receipt.EventCount && receipt.ContiguousSequence && receipt.AllTerminal && receipt.AllCaseBound && receipt.AllAttemptBound && receipt.AllSegmentBound && receipt.AllProviderBound && receipt.AllTrackBound && receipt.AllConsentBound && receipt.AllSpeakerBound && receipt.AllContextBound && receipt.AllAudioBound
	digest, err := STRIDEContractDigest(struct {
		CorpusDigest string                          `json:"corpusDigest"`
		Minimum      int                             `json:"minimum"`
		Bindings     []TranscriptionIntegrityBinding `json:"bindings"`
		Events       []TranscriptionIntegrityEvent   `json:"events"`
	}{manifest.Digest, minimumEvents, bindings, events})
	if err != nil {
		return TranscriptionIntegrityReceipt{}, err
	}
	receipt.Digest = digest
	return receipt, nil
}

func transcriptionIntegrityMatchesCandidate(bindings []TranscriptionIntegrityBinding, events []TranscriptionIntegrityEvent, tenantID, candidateDigest, model, routeDigest string) bool {
	if len(bindings) == 0 || len(bindings) != len(events) || !strideIdentifier(tenantID) || !isHexDigest(candidateDigest) || !strideIdentifier(model) || !isHexDigest(routeDigest) {
		return false
	}
	for _, binding := range bindings {
		if binding.TenantID != tenantID || binding.CandidateDigest != candidateDigest || binding.Model != model || binding.RouteDigest != routeDigest {
			return false
		}
	}
	for _, event := range events {
		if event.TenantID != tenantID || event.CandidateDigest != candidateDigest || event.Model != model || event.RouteDigest != routeDigest {
			return false
		}
	}
	return true
}

func claimUniqueTranscriptionBinding(seen map[string]struct{}, value string) bool {
	if _, duplicate := seen[value]; duplicate {
		return false
	}
	seen[value] = struct{}{}
	return true
}

type TranscriptionQualificationEvidence struct {
	Observations      []TranscriptionObservation      `json:"observations"`
	IntegrityBindings []TranscriptionIntegrityBinding `json:"integrityBindings"`
	IntegrityEvents   []TranscriptionIntegrityEvent   `json:"integrityEvents"`
}

// TranscriptionEvidenceThresholds are intentionally opt-in so the original
// small deterministic contract tests remain useful. E10 must set every field
// below: leaving a minimum at zero creates a local diagnostic receipt, never a
// live/provider qualification.
type TranscriptionEvidenceThresholds struct {
	Base                       TranscriptionQualificationThresholds `json:"base"`
	MinimumClips               int                                  `json:"minimumClips"`
	MinimumDurationMS          int64                                `json:"minimumDurationMs"`
	MaximumWERDeltaToIncumbent float64                              `json:"maximumWerDeltaToIncumbent"`
	MinimumDomainTermAccuracy  float64                              `json:"minimumDomainTermAccuracy"`
	MinimumNumericAccuracy     float64                              `json:"minimumNumericAccuracy"`
	MinimumIntegrityEvents     int                                  `json:"minimumIntegrityEvents"`
	BootstrapSamples           int                                  `json:"bootstrapSamples"`
}

type TranscriptionEvidenceReceipt struct {
	Score                    TranscriptionQualificationReceipt `json:"score"`
	EvidenceThresholdsDigest string                            `json:"evidenceThresholdsDigest"`
	ObservationSetDigest     string                            `json:"observationSetDigest"`
	ProviderAttemptSetDigest string                            `json:"providerAttemptSetDigest,omitempty"`
	ClipCount                int                               `json:"clipCount"`
	DurationMS               int64                             `json:"durationMs"`
	IncumbentMeanWER         float64                           `json:"incumbentMeanWer"`
	WERDeltaToIncumbent      float64                           `json:"werDeltaToIncumbent"`
	WERDeltaInterval         QualificationMetricInterval       `json:"werDeltaInterval"`
	DomainTermAccuracy       QualificationMetricInterval       `json:"domainTermAccuracy"`
	NumericAccuracy          QualificationMetricInterval       `json:"numericAccuracy"`
	SilenceCaseCount         int                               `json:"silenceCaseCount"`
	SilenceFalseInsertions   int                               `json:"silenceFalseInsertions"`
	SilencePass              bool                              `json:"silencePass"`
	WER                      QualificationMetricInterval       `json:"wer"`
	P50FinalLatencyMS        int64                             `json:"p50FinalLatencyMs"`
	P95FinalLatencyMS        int64                             `json:"p95FinalLatencyMs"`
	P99FinalLatencyMS        int64                             `json:"p99FinalLatencyMs"`
	P50FinalLatency          QualificationMetricInterval       `json:"p50FinalLatency"`
	P95FinalLatency          QualificationMetricInterval       `json:"p95FinalLatency"`
	P99FinalLatency          QualificationMetricInterval       `json:"p99FinalLatency"`
	Integrity                TranscriptionIntegrityReceipt     `json:"integrity"`
	PreregisteredPass        bool                              `json:"preregisteredPass"`
	LiveOrDeviceQualified    bool                              `json:"liveOrDeviceQualified"`
	ResidualGates            []string                          `json:"residualGates"`
	RegistryDigest           string                            `json:"registryDigest,omitempty"`
	TargetDigest             string                            `json:"targetDigest,omitempty"`
	CandidateDigest          string                            `json:"candidateDigest,omitempty"`
	OperatorSignatureDigest  string                            `json:"operatorSignatureDigest,omitempty"`
	ReviewerSignatureDigest  string                            `json:"reviewerSignatureDigest,omitempty"`
	ReceiptDigest            string                            `json:"receiptDigest"`
}

// EvaluateTranscriptionEvidence produces a reproducible local receipt. It
// never returns live/provider/device qualification: that requires the existing
// server-owned provider-attempt ledger plus consented corpus custody and a
// separately trusted device evidence authority.
func EvaluateTranscriptionEvidence(manifest TranscriptionCorpusManifest, evidence TranscriptionQualificationEvidence, thresholds TranscriptionEvidenceThresholds) (TranscriptionEvidenceReceipt, error) {
	return evaluateTranscriptionEvidence(manifest, evidence, thresholds, false)
}

func evaluateTranscriptionEvidence(manifest TranscriptionCorpusManifest, evidence TranscriptionQualificationEvidence, thresholds TranscriptionEvidenceThresholds, locallyStored bool) (TranscriptionEvidenceReceipt, error) {
	if manifest.Validate() != nil || !validE10TranscriptionCorpusCoverage(manifest) || !validE10TranscriptionEvidenceThresholds(thresholds) {
		return TranscriptionEvidenceReceipt{}, ErrTranscriptionQualificationInvalid
	}
	if len(manifest.Cases) != len(evidence.Observations) || len(manifest.Cases) < thresholds.MinimumClips {
		return TranscriptionEvidenceReceipt{}, ErrTranscriptionQualificationInvalid
	}
	base, err := EvaluateTranscriptionCandidate(manifest, evidence.Observations, thresholds.Base)
	if err != nil {
		return TranscriptionEvidenceReceipt{}, err
	}
	byCase := make(map[string]TranscriptionObservation, len(evidence.Observations))
	for _, observation := range evidence.Observations {
		if _, duplicate := byCase[observation.CaseID]; duplicate {
			return TranscriptionEvidenceReceipt{}, ErrTranscriptionQualificationInvalid
		}
		byCase[observation.CaseID] = observation
	}
	candidateWordErrors := make([]int, 0, len(manifest.Cases))
	incumbentWordErrors := make([]int, 0, len(manifest.Cases))
	referenceWordCounts := make([]int, 0, len(manifest.Cases))
	latencies := make([]int64, 0, len(manifest.Cases))
	duration := int64(0)
	domainExpected, domainPredicted, domainCorrect := 0, 0, 0
	numericExpected, numericPredicted, numericCorrect := 0, 0, 0
	silenceCases, silenceFalseInsertions := 0, 0
	seenSegments := make(map[string]struct{}, len(manifest.Cases))
	seenItems := make(map[string]struct{}, len(manifest.Cases))
	seenProviderReceipts := make(map[string]struct{}, len(manifest.Cases))
	domainLexicon := map[string]struct{}{}
	for _, testCase := range manifest.Cases {
		for term := range transcriptionTermSet(testCase.RequiredTerms) {
			domainLexicon[term] = struct{}{}
		}
	}
	for _, testCase := range manifest.Cases {
		observation, found := byCase[testCase.ID]
		if !found || observation.InputAudioSHA256 != testCase.AudioSHA256 || !strideIdentifier(observation.SegmentID) || !isHexDigest(observation.ProviderItemIDHash) ||
			!strideIdentifier(observation.TrackID) || !isHexDigest(observation.ConsentReceiptDigest) || !isHexDigest(observation.ProviderReceiptDigest) {
			return TranscriptionEvidenceReceipt{}, ErrTranscriptionQualificationInvalid
		}
		if _, duplicate := seenSegments[observation.SegmentID]; duplicate {
			return TranscriptionEvidenceReceipt{}, ErrTranscriptionQualificationInvalid
		}
		if _, duplicate := seenItems[observation.ProviderItemIDHash]; duplicate {
			return TranscriptionEvidenceReceipt{}, ErrTranscriptionQualificationInvalid
		}
		if _, duplicate := seenProviderReceipts[observation.ProviderReceiptDigest]; duplicate {
			return TranscriptionEvidenceReceipt{}, ErrTranscriptionQualificationInvalid
		}
		seenSegments[observation.SegmentID] = struct{}{}
		seenItems[observation.ProviderItemIDHash] = struct{}{}
		seenProviderReceipts[observation.ProviderReceiptDigest] = struct{}{}
		reference := transcriptionTokens(testCase.ReferenceText)
		candidate := transcriptionTokens(observation.OutputText)
		incumbent := transcriptionTokens(observation.IncumbentOutputText)
		if isTranscriptionSilenceCase(testCase) {
			silenceCases++
			silenceFalseInsertions += len(candidate)
		} else {
			if len(incumbent) == 0 {
				return TranscriptionEvidenceReceipt{}, ErrTranscriptionQualificationInvalid
			}
			candidateWordErrors = append(candidateWordErrors, wordEditDistance(reference, candidate))
			incumbentWordErrors = append(incumbentWordErrors, wordEditDistance(reference, incumbent))
			referenceWordCounts = append(referenceWordCounts, len(reference))
		}
		latencies = append(latencies, observation.FinalAt.Sub(observation.CommittedAt).Milliseconds())
		duration += testCase.DurationMS
		expectedTerms := transcriptionTermSet(testCase.RequiredTerms)
		candidateTerms := transcriptionTermSetFromText(observation.OutputText, domainLexicon)
		domainExpected += len(expectedTerms)
		domainPredicted += len(candidateTerms)
		domainCorrect += transcriptionSetIntersection(expectedTerms, candidateTerms)
		expectedNumbers := transcriptionNumberSet(testCase.ReferenceText)
		candidateNumbers := transcriptionNumberSet(observation.OutputText)
		numericExpected += len(expectedNumbers)
		numericPredicted += len(candidateNumbers)
		numericCorrect += transcriptionSetIntersection(expectedNumbers, candidateNumbers)
	}
	if domainExpected == 0 || numericExpected == 0 {
		return TranscriptionEvidenceReceipt{}, ErrTranscriptionQualificationInvalid
	}
	latencyCopy := append([]int64(nil), latencies...)
	sort.Slice(latencyCopy, func(i, j int) bool { return latencyCopy[i] < latencyCopy[j] })
	integrity, err := EvaluateTranscriptionIntegrity(manifest, evidence.IntegrityBindings, evidence.IntegrityEvents, thresholds.MinimumIntegrityEvents)
	if err != nil {
		return TranscriptionEvidenceReceipt{}, err
	}
	corpusWER := ratioInts(sumInts(candidateWordErrors), sumInts(referenceWordCounts))
	incumbentWER := ratioInts(sumInts(incumbentWordErrors), sumInts(referenceWordCounts))
	_, domainDenominator := pairedAccuracy(domainCorrect, domainExpected, domainPredicted)
	_, numericDenominator := pairedAccuracy(numericCorrect, numericExpected, numericPredicted)
	p50 := percentileNearestRank(latencyCopy, .50)
	p95 := percentileNearestRank(latencyCopy, .95)
	p99 := percentileNearestRank(latencyCopy, .99)
	receipt := TranscriptionEvidenceReceipt{
		Score:                    base,
		EvidenceThresholdsDigest: workDigest(thresholds),
		ObservationSetDigest:     transcriptionObservationSetDigest(evidence.Observations),
		ClipCount:                len(manifest.Cases),
		DurationMS:               duration,
		IncumbentMeanWER:         incumbentWER,
		WERDeltaToIncumbent:      corpusWER - incumbentWER,
		WERDeltaInterval:         deterministicPairedRatioDeltaBootstrap(candidateWordErrors, incumbentWordErrors, referenceWordCounts, thresholds.BootstrapSamples, "wer_delta_to_incumbent"),
		DomainTermAccuracy:       wilson95(domainCorrect, domainDenominator),
		NumericAccuracy:          wilson95(numericCorrect, numericDenominator),
		SilenceCaseCount:         silenceCases,
		SilenceFalseInsertions:   silenceFalseInsertions,
		SilencePass:              silenceCases >= e10TranscriptionMinimumCasesPerTag && silenceFalseInsertions == 0,
		WER:                      deterministicRatioBootstrap(candidateWordErrors, referenceWordCounts, thresholds.BootstrapSamples, "wer"),
		P50FinalLatencyMS:        p50,
		P95FinalLatencyMS:        p95,
		P99FinalLatencyMS:        p99,
		P50FinalLatency:          deterministicQuantileBootstrap(latencies, .50, thresholds.BootstrapSamples, "latency_p50_ms"),
		P95FinalLatency:          deterministicQuantileBootstrap(latencies, .95, thresholds.BootstrapSamples, "latency_p95_ms"),
		P99FinalLatency:          deterministicQuantileBootstrap(latencies, .99, thresholds.BootstrapSamples, "latency_p99_ms"),
		Integrity:                integrity,
	}
	receipt.PreregisteredPass = base.DeterministicPass && receipt.ClipCount >= thresholds.MinimumClips && receipt.DurationMS >= thresholds.MinimumDurationMS &&
		receipt.WER.High <= thresholds.Base.MaxMeanWER && receipt.WERDeltaToIncumbent <= thresholds.MaximumWERDeltaToIncumbent && receipt.WERDeltaInterval.High <= thresholds.MaximumWERDeltaToIncumbent &&
		receipt.DomainTermAccuracy.Low >= thresholds.MinimumDomainTermAccuracy && receipt.NumericAccuracy.Low >= thresholds.MinimumNumericAccuracy &&
		receipt.P95FinalLatency.High <= float64(thresholds.Base.MaxP95FinalLatencyMS) && receipt.SilencePass && integrity.DeterministicPass
	// Local evidence, including rows read from the durable one-use store, cannot
	// establish provider identity, the intended project/billing account, a
	// current contract, authoritative usage/cost, or a consent trust root.
	receipt.Score.ProviderQualified = false
	receipt.LiveOrDeviceQualified = false
	receipt.ResidualGates = []string{
		"external_provider_project_and_billing_receipt",
		"external_current_model_contract_receipt",
		"external_terminal_usage_and_cost_receipt",
		"independently_anchored_consent_ledger_receipt",
		"trusted_target_web_iphone_ipad_device_receipts",
	}
	if !locallyStored {
		receipt.ResidualGates = append([]string{"durable_one_use_local_evidence_consumption"}, receipt.ResidualGates...)
	}
	digest, err := transcriptionEvidenceReceiptDigest(manifest.Digest, thresholds, receipt)
	if err != nil {
		return TranscriptionEvidenceReceipt{}, err
	}
	receipt.ReceiptDigest = digest
	return receipt, nil
}

func transcriptionObservationSetDigest(observations []TranscriptionObservation) string {
	clone := append([]TranscriptionObservation(nil), observations...)
	sort.Slice(clone, func(i, j int) bool { return clone[i].CaseID < clone[j].CaseID })
	return workDigest(clone)
}

func transcriptionProviderAttemptSetDigest(attempts []TranscriptionProviderAttemptRef) string {
	clone := append([]TranscriptionProviderAttemptRef(nil), attempts...)
	sort.Slice(clone, func(i, j int) bool { return clone[i].ID < clone[j].ID })
	type boundAttempt struct {
		Ref           TranscriptionProviderAttemptRef `json:"ref"`
		ReceiptDigest string                          `json:"receiptDigest"`
	}
	bound := make([]boundAttempt, 0, len(clone))
	for _, attempt := range clone {
		receiptDigest := workDigest(attempt.Receipt)
		attempt.Receipt = ""
		bound = append(bound, boundAttempt{Ref: attempt, ReceiptDigest: receiptDigest})
	}
	return workDigest(bound)
}

func validE10TranscriptionEvidenceThresholds(thresholds TranscriptionEvidenceThresholds) bool {
	return thresholds.MinimumClips >= e10TranscriptionMinimumClips && thresholds.MinimumDurationMS >= e10TranscriptionMinimumDurationMS &&
		thresholds.MinimumIntegrityEvents >= e10TranscriptionMinimumIntegrityEvents && thresholds.BootstrapSamples >= e10TranscriptionMinimumBootstrapSamples &&
		thresholds.Base.RequireSpeakerMatch && thresholds.Base.RequireSegmentBinding && thresholds.Base.MaxMeanWER >= 0 && thresholds.Base.MaxMeanWER <= e10TranscriptionMaximumCorpusWER &&
		thresholds.Base.MinRequiredTermRecall >= e10TranscriptionMinimumDomainAccuracy && thresholds.Base.MinRequiredTermRecall <= 1 &&
		thresholds.Base.MaxP95FinalLatencyMS >= 1 && thresholds.Base.MaxP95FinalLatencyMS <= e10TranscriptionMaximumP95FinalLatencyMS &&
		thresholds.MaximumWERDeltaToIncumbent >= 0 && thresholds.MaximumWERDeltaToIncumbent <= e10TranscriptionMaximumWERDelta &&
		thresholds.MinimumDomainTermAccuracy >= e10TranscriptionMinimumDomainAccuracy && thresholds.MinimumDomainTermAccuracy <= 1 &&
		thresholds.MinimumNumericAccuracy >= e10TranscriptionMinimumNumericAccuracy && thresholds.MinimumNumericAccuracy <= 1
}

func transcriptionEvidenceReceiptDigest(manifestDigest string, thresholds TranscriptionEvidenceThresholds, receipt TranscriptionEvidenceReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return STRIDEContractDigest(struct {
		ManifestDigest string                          `json:"manifestDigest"`
		Thresholds     TranscriptionEvidenceThresholds `json:"thresholds"`
		Receipt        TranscriptionEvidenceReceipt    `json:"receipt"`
	}{manifestDigest, thresholds, receipt})
}

func wilson95(successes, total int) QualificationMetricInterval {
	if total < 1 || successes < 0 || successes > total {
		return QualificationMetricInterval{Method: "wilson_95"}
	}
	p := float64(successes) / float64(total)
	z := 1.959963984540054
	z2 := z * z
	denominator := 1 + z2/float64(total)
	centre := (p + z2/(2*float64(total))) / denominator
	radius := z * math.Sqrt((p*(1-p)+z2/(4*float64(total)))/float64(total)) / denominator
	return QualificationMetricInterval{Point: p, Low: math.Max(0, centre-radius), High: math.Min(1, centre+radius), Numerator: successes, Denominator: total, Method: "wilson_95"}
}

func deterministicBootstrap(values []float64, samples int, label string) QualificationMetricInterval {
	if len(values) == 0 || samples < 1 {
		return QualificationMetricInterval{Method: "deterministic_bootstrap_95"}
	}
	means := make([]float64, samples)
	for sample := 0; sample < samples; sample++ {
		total := 0.0
		for draw := range values {
			// SplitMix64 yields a deterministic bootstrap index without carrying
			// global random-number state or collapsing small fixtures into a short
			// arithmetic cycle.
			seed := uint64(sample+1)*0x9e3779b97f4a7c15 ^ uint64(draw+1)*0xbf58476d1ce4e5b9 ^ uint64(len(label))
			seed ^= seed >> 30
			seed *= 0xbf58476d1ce4e5b9
			seed ^= seed >> 27
			seed *= 0x94d049bb133111eb
			seed ^= seed >> 31
			index := int(seed % uint64(len(values)))
			total += values[index]
		}
		means[sample] = total / float64(len(values))
	}
	sort.Float64s(means)
	return QualificationMetricInterval{Point: meanFloat64(values), Low: means[int(float64(samples-1)*.025)], High: means[int(float64(samples-1)*.975)], Method: "deterministic_bootstrap_95"}
}

func deterministicRatioBootstrap(numerators, denominators []int, samples int, label string) QualificationMetricInterval {
	if len(numerators) == 0 || len(numerators) != len(denominators) || samples < 1 {
		return QualificationMetricInterval{Method: "deterministic_bootstrap_ratio_95"}
	}
	values := make([]float64, samples)
	for sample := 0; sample < samples; sample++ {
		numerator, denominator := 0, 0
		for draw := range numerators {
			index := deterministicBootstrapIndex(sample, draw, len(numerators), label)
			numerator += numerators[index]
			denominator += denominators[index]
		}
		values[sample] = ratioInts(numerator, denominator)
	}
	sort.Float64s(values)
	return QualificationMetricInterval{Point: ratioInts(sumInts(numerators), sumInts(denominators)), Low: values[int(float64(samples-1)*.025)], High: values[int(float64(samples-1)*.975)], Numerator: sumInts(numerators), Denominator: sumInts(denominators), Method: "deterministic_bootstrap_ratio_95"}
}

func deterministicPairedRatioDeltaBootstrap(candidateNumerators, incumbentNumerators, denominators []int, samples int, label string) QualificationMetricInterval {
	if len(candidateNumerators) == 0 || len(candidateNumerators) != len(incumbentNumerators) || len(candidateNumerators) != len(denominators) || samples < 1 {
		return QualificationMetricInterval{Method: "deterministic_paired_bootstrap_ratio_delta_95"}
	}
	values := make([]float64, samples)
	for sample := 0; sample < samples; sample++ {
		candidate, incumbent, denominator := 0, 0, 0
		for draw := range candidateNumerators {
			index := deterministicBootstrapIndex(sample, draw, len(candidateNumerators), label)
			candidate += candidateNumerators[index]
			incumbent += incumbentNumerators[index]
			denominator += denominators[index]
		}
		values[sample] = ratioInts(candidate, denominator) - ratioInts(incumbent, denominator)
	}
	sort.Float64s(values)
	point := ratioInts(sumInts(candidateNumerators), sumInts(denominators)) - ratioInts(sumInts(incumbentNumerators), sumInts(denominators))
	return QualificationMetricInterval{Point: point, Low: values[int(float64(samples-1)*.025)], High: values[int(float64(samples-1)*.975)], Method: "deterministic_paired_bootstrap_ratio_delta_95"}
}

func deterministicQuantileBootstrap(values []int64, percentile float64, samples int, label string) QualificationMetricInterval {
	if len(values) == 0 || percentile <= 0 || percentile > 1 || samples < 1 {
		return QualificationMetricInterval{Method: "deterministic_quantile_bootstrap_95"}
	}
	bootstrapped := make([]float64, samples)
	resample := make([]int64, len(values))
	for sample := 0; sample < samples; sample++ {
		for draw := range values {
			resample[draw] = values[deterministicBootstrapIndex(sample, draw, len(values), label)]
		}
		sort.Slice(resample, func(i, j int) bool { return resample[i] < resample[j] })
		bootstrapped[sample] = float64(percentileNearestRank(resample, percentile))
	}
	sort.Float64s(bootstrapped)
	actual := append([]int64(nil), values...)
	sort.Slice(actual, func(i, j int) bool { return actual[i] < actual[j] })
	return QualificationMetricInterval{Point: float64(percentileNearestRank(actual, percentile)), Low: bootstrapped[int(float64(samples-1)*.025)], High: bootstrapped[int(float64(samples-1)*.975)], Method: "deterministic_quantile_bootstrap_95"}
}

func deterministicBootstrapIndex(sample, draw, length int, label string) int {
	seed := uint64(sample+1)*0x9e3779b97f4a7c15 ^ uint64(draw+1)*0xbf58476d1ce4e5b9 ^ uint64(len(label))
	seed ^= seed >> 30
	seed *= 0xbf58476d1ce4e5b9
	seed ^= seed >> 27
	seed *= 0x94d049bb133111eb
	seed ^= seed >> 31
	return int(seed % uint64(length))
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func ratioInts(numerator, denominator int) float64 {
	if denominator < 1 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func meanFloat64(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func int64ToFloat64(values []int64) []float64 {
	result := make([]float64, len(values))
	for index, value := range values {
		result[index] = float64(value)
	}
	return result
}

func pairedAccuracy(correct, expected, predicted int) (float64, int) {
	denominator := expected
	if predicted > denominator {
		denominator = predicted
	}
	if denominator == 0 {
		return 1, 0
	}
	return float64(correct) / float64(denominator), denominator
}

func transcriptionTermSet(terms []string) map[string]struct{} {
	result := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		result[strings.Join(transcriptionTokens(term), " ")] = struct{}{}
	}
	return result
}

func transcriptionTermSetFromText(text string, candidates map[string]struct{}) map[string]struct{} {
	result := map[string]struct{}{}
	normalized := " " + strings.Join(transcriptionTokens(text), " ") + " "
	for candidate := range candidates {
		if strings.Contains(normalized, " "+candidate+" ") {
			result[candidate] = struct{}{}
		}
	}
	return result
}

func transcriptionNumberSet(text string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, token := range transcriptionTokens(text) {
		allNumbers := token != ""
		for _, value := range token {
			allNumbers = allNumbers && unicode.IsNumber(value)
		}
		if allNumbers {
			result[token] = struct{}{}
		}
	}
	return result
}

func transcriptionSetIntersection(left, right map[string]struct{}) int {
	count := 0
	for value := range left {
		if _, ok := right[value]; ok {
			count++
		}
	}
	return count
}
