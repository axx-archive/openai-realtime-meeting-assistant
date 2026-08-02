package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func transcriptionQualificationCorpus(t *testing.T) TranscriptionCorpusManifest {
	t.Helper()
	digest := strings.Repeat("a", 64)
	manifest, err := FreezeTranscriptionCorpus(1, []TranscriptionCorpusCase{
		{ID: "names-and-numbers", AudioSHA256: digest, ReferenceText: "Erick moved STRIDE to 42 accounts", Tags: []string{"numbers", "company_name"}, RequiredTerms: []string{"Erick", "STRIDE", "42"}, ExpectedSpeakers: []string{"erick"}, DurationMS: 2200},
		{ID: "code-switch-noise", AudioSHA256: strings.Repeat("b", 64), ReferenceText: "Dog Perfect esta listo", Tags: []string{"code_switching", "noise", "speaker_attribution"}, RequiredTerms: []string{"Dog Perfect"}, ExpectedSpeakers: []string{"aj"}, DurationMS: 1800},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestPlannedTranscriptionCandidatesDoNotInventProviderCapabilities(t *testing.T) {
	for _, model := range []string{"gpt-transcribe", "gpt-live-transcribe"} {
		purpose := TranscriptionPurposeMeetingAuthoritative
		if strings.Contains(model, "live") {
			purpose = TranscriptionPurposeMeetingProvisional
		}
		spec, err := PlannedTranscriptionCandidate(model, purpose)
		if err != nil {
			t.Fatal(err)
		}
		if spec.ProviderQualified || spec.PromptMode != "unknown" || spec.SupportsDiarization || spec.SupportsTimestamps {
			t.Fatalf("candidate invented live capabilities: %+v", spec)
		}
		if purpose == TranscriptionPurposeMeetingProvisional && !spec.ProvisionalOnly {
			t.Fatal("live caption candidate must remain provisional")
		}
	}
	incumbent, err := PlannedTranscriptionCandidate("gpt-4o-transcribe", TranscriptionPurposeComposerDictation)
	if err != nil || incumbent.PromptMode != "free_text" || incumbent.Streaming {
		t.Fatalf("incumbent declaration=%+v err=%v", incumbent, err)
	}
	for name, mutate := range map[string]func(*TranscriptionCapabilitySpec){
		"provider boolean":     func(value *TranscriptionCapabilitySpec) { value.ProviderQualified = true },
		"documentation digest": func(value *TranscriptionCapabilitySpec) { value.DocumentationDigest = strings.Repeat("a", 64) },
		"provider receipt":     func(value *TranscriptionCapabilitySpec) { value.ProviderReceiptDigest = strings.Repeat("b", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			forged := incumbent
			mutate(&forged)
			if err := forged.Validate(); !errors.Is(err, ErrTranscriptionQualificationInvalid) {
				t.Fatalf("caller-authored provider claim accepted: %v", err)
			}
		})
	}
}

func TestFreezeTranscriptionCorpusIsDeterministicAndClosed(t *testing.T) {
	first := transcriptionQualificationCorpus(t)
	second, err := FreezeTranscriptionCorpus(1, []TranscriptionCorpusCase{first.Cases[1], first.Cases[0]})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("corpus digest drift %s != %s", first.Digest, second.Digest)
	}
	bad := first.Cases[0]
	bad.Tags = append(bad.Tags, "marketing_magic")
	if _, err := FreezeTranscriptionCorpus(1, []TranscriptionCorpusCase{bad}); !errors.Is(err, ErrTranscriptionQualificationInvalid) {
		t.Fatalf("unknown corpus tag err=%v", err)
	}
	reusedAudio := append([]TranscriptionCorpusCase(nil), first.Cases...)
	reusedAudio[1].AudioSHA256 = reusedAudio[0].AudioSHA256
	if _, err := FreezeTranscriptionCorpus(1, reusedAudio); !errors.Is(err, ErrTranscriptionQualificationInvalid) {
		t.Fatalf("reused audio evidence err=%v", err)
	}
	tampered := first
	tampered.Cases = append([]TranscriptionCorpusCase(nil), first.Cases...)
	tampered.Cases[0].ReferenceText = "tampered"
	if err := tampered.Validate(); !errors.Is(err, ErrTranscriptionQualificationInvalid) {
		t.Fatalf("tampered manifest err=%v", err)
	}
}

func TestTranscriptionSilenceCaseUsesFalseInsertionContract(t *testing.T) {
	testCase := TranscriptionCorpusCase{ID: "silence-control", AudioSHA256: strings.Repeat("9", 64), Tags: []string{"silence"}, DurationMS: 1200}
	manifest, err := FreezeTranscriptionCorpus(1, []TranscriptionCorpusCase{testCase})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	observation := TranscriptionObservation{CaseID: testCase.ID, Model: "gpt-transcribe", RouteDigest: strings.Repeat("8", 64), SegmentID: "silence-segment", ProviderItemIDHash: strings.Repeat("7", 64), CommittedAt: start, FinalAt: start.Add(300 * time.Millisecond), Synthetic: true}
	thresholds := TranscriptionQualificationThresholds{MaxMeanWER: .1, MinRequiredTermRecall: .97, MaxP95FinalLatencyMS: 1000, RequireSegmentBinding: true}
	receipt, err := EvaluateTranscriptionCandidate(manifest, []TranscriptionObservation{observation}, thresholds)
	if err != nil || !receipt.DeterministicPass || !receipt.SilencePass || receipt.SilenceCaseCount != 1 || receipt.SilenceFalseInsertions != 0 || !receipt.Scores[0].Silence {
		t.Fatalf("silence receipt=%+v err=%v", receipt, err)
	}
	observation.OutputText = "invented speech"
	receipt, err = EvaluateTranscriptionCandidate(manifest, []TranscriptionObservation{observation}, thresholds)
	if err != nil || receipt.DeterministicPass || receipt.SilencePass || receipt.SilenceFalseInsertions != 2 || receipt.Scores[0].FalseInsertionWords != 2 {
		t.Fatalf("false insertion receipt=%+v err=%v", receipt, err)
	}
	badSilence := testCase
	badSilence.ReferenceText = "not silence"
	if _, err := FreezeTranscriptionCorpus(1, []TranscriptionCorpusCase{badSilence}); !errors.Is(err, ErrTranscriptionQualificationInvalid) {
		t.Fatalf("silence case with a reference was accepted: %v", err)
	}
	badSpeech := testCase
	badSpeech.Tags = []string{"noise"}
	if _, err := FreezeTranscriptionCorpus(1, []TranscriptionCorpusCase{badSpeech}); !errors.Is(err, ErrTranscriptionQualificationInvalid) {
		t.Fatalf("non-silence case without a reference was accepted: %v", err)
	}
}

func TestTranscriptionCorpusRejectsMechanicallyFalseLabels(t *testing.T) {
	for name, testCase := range map[string]TranscriptionCorpusCase{
		"company without required term": {ID: "bad-company", AudioSHA256: strings.Repeat("1", 64), ReferenceText: "Bonfire update", Tags: []string{"company_name"}, DurationMS: 1000},
		"numbers without number":        {ID: "bad-numbers", AudioSHA256: strings.Repeat("2", 64), ReferenceText: "no quantity here", Tags: []string{"numbers"}, DurationMS: 1000},
		"speaker without identity":      {ID: "bad-speaker", AudioSHA256: strings.Repeat("3", 64), ReferenceText: "hello", Tags: []string{"speaker_attribution"}, DurationMS: 1000},
		"crosstalk with one speaker":    {ID: "bad-crosstalk", AudioSHA256: strings.Repeat("4", 64), ReferenceText: "hello", Tags: []string{"crosstalk"}, ExpectedSpeakers: []string{"erick"}, DurationMS: 1000},
		"long short phrase":             {ID: "bad-short", AudioSHA256: strings.Repeat("5", 64), ReferenceText: "one two three four five six seven", Tags: []string{"short_phrase"}, DurationMS: 1000},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := FreezeTranscriptionCorpus(1, []TranscriptionCorpusCase{testCase}); !errors.Is(err, ErrTranscriptionQualificationInvalid) {
				t.Fatalf("false corpus label was accepted: %v", err)
			}
		})
	}
}

func TestTranscriptionQualificationScoresBindingsTermsLatencyAndSyntheticBoundary(t *testing.T) {
	manifest := transcriptionQualificationCorpus(t)
	start := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	route := strings.Repeat("c", 64)
	providerReceipt := strings.Repeat("d", 64)
	observations := []TranscriptionObservation{
		{CaseID: "names-and-numbers", Model: "gpt-transcribe", RouteDigest: route, OutputText: "Erick moved STRIDE to 42 accounts", SegmentID: "segment-1", ProviderItemIDHash: strings.Repeat("e", 64), ObservedSpeakers: []string{"erick"}, CommittedAt: start, FinalAt: start.Add(400 * time.Millisecond), AcceptedOutputCostMicros: 3, Synthetic: true},
		{CaseID: "code-switch-noise", Model: "gpt-transcribe", RouteDigest: route, OutputText: "Dog Perfect esta listo", SegmentID: "segment-2", ProviderItemIDHash: strings.Repeat("f", 64), ObservedSpeakers: []string{"aj"}, CommittedAt: start, FinalAt: start.Add(600 * time.Millisecond), AcceptedOutputCostMicros: 4, Synthetic: true},
	}
	thresholds := TranscriptionQualificationThresholds{MaxMeanWER: 0.05, MinRequiredTermRecall: 1, MaxP95FinalLatencyMS: 1000, RequireSpeakerMatch: true, RequireSegmentBinding: true}
	receipt, err := EvaluateTranscriptionCandidate(manifest, observations, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.DeterministicPass || receipt.ProviderQualified || receipt.AcceptedOutputCostMicros != 7 || receipt.P95FinalLatencyMS != 600 {
		t.Fatalf("synthetic receipt=%+v", receipt)
	}
	for index := range observations {
		observations[index].Synthetic = false
		observations[index].ProviderReceiptDigest = providerReceipt
	}
	rawLive, err := EvaluateTranscriptionCandidate(manifest, observations, thresholds)
	if err != nil || rawLive.ProviderQualified {
		t.Fatalf("caller observations became live receipt=%+v err=%v", rawLive, err)
	}
}

func TestTranscriptionQualificationRejectsMixedRoutesAndBadMapping(t *testing.T) {
	manifest := transcriptionQualificationCorpus(t)
	start := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	observations := []TranscriptionObservation{
		{CaseID: manifest.Cases[0].ID, Model: "gpt-transcribe", RouteDigest: strings.Repeat("a", 64), OutputText: manifest.Cases[0].ReferenceText, SegmentID: "segment-1", ProviderItemIDHash: strings.Repeat("b", 64), ObservedSpeakers: manifest.Cases[0].ExpectedSpeakers, CommittedAt: start, FinalAt: start.Add(time.Second), Synthetic: true},
		{CaseID: manifest.Cases[1].ID, Model: "gpt-transcribe", RouteDigest: strings.Repeat("c", 64), OutputText: manifest.Cases[1].ReferenceText, SegmentID: "segment-2", ProviderItemIDHash: strings.Repeat("d", 64), ObservedSpeakers: manifest.Cases[1].ExpectedSpeakers, CommittedAt: start, FinalAt: start.Add(time.Second), Synthetic: true},
	}
	_, err := EvaluateTranscriptionCandidate(manifest, observations, TranscriptionQualificationThresholds{MaxMeanWER: 1, MinRequiredTermRecall: 0, MaxP95FinalLatencyMS: 2000})
	if !errors.Is(err, ErrTranscriptionQualificationInvalid) {
		t.Fatalf("mixed route err=%v", err)
	}
}

func TestProvisionalCaptionCannotBeAuthoritative(t *testing.T) {
	spec, err := PlannedTranscriptionCandidate("gpt-live-transcribe", TranscriptionPurposeMeetingProvisional)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTranscriptionPurpose(spec, TranscriptionPurposeMeetingAuthoritative); !errors.Is(err, ErrTranscriptionQualificationInvalid) {
		t.Fatalf("provisional authority err=%v", err)
	}
}
