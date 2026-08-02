package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type qualificationTestClock struct {
	now time.Time
}

type qualificationEvidenceStoreHarness struct {
	authority  *QualificationEvidenceStore
	clock      *qualificationTestClock
	ledgerPath string
	seed       QualificationEvidenceSeed
}

func newQualificationEvidenceStoreHarness(t *testing.T, label string, seed QualificationEvidenceSeed, initial time.Time) *qualificationEvidenceStoreHarness {
	t.Helper()
	clock := &qualificationTestClock{now: initial}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(directory, label+".jsonl")
	authority, err := OpenQualificationEvidenceStore(ledgerPath, seed, "tenant-acme", func() time.Time { return clock.now })
	if err != nil {
		t.Fatal(err)
	}
	return &qualificationEvidenceStoreHarness{authority: authority, clock: clock, ledgerPath: ledgerPath, seed: cloneQualificationEvidenceSeed(seed)}
}

func (harness *qualificationEvidenceStoreHarness) reopen(t *testing.T) *QualificationEvidenceStore {
	t.Helper()
	authority, err := OpenQualificationEvidenceStore(harness.ledgerPath, harness.seed, "tenant-acme", func() time.Time { return harness.clock.now })
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func qualificationEvidenceAttemptSeed(count int) (QualificationEvidenceSeed, []TranscriptionProviderAttemptRef) {
	seed := QualificationEvidenceSeed{}
	refs := make([]TranscriptionProviderAttemptRef, 0, count)
	for index := 0; index < count; index++ {
		ref := TranscriptionProviderAttemptRef{ID: fmt.Sprintf("stored-attempt-%d", index), TenantID: "tenant-acme", Receipt: fmt.Sprintf("stored-token-%d", index)}
		refs = append(refs, ref)
		seed.ProviderAttempts = append(seed.ProviderAttempts, StoredProviderAttemptEvidence{Ref: ref, Observation: TranscriptionObservation{CaseID: fmt.Sprintf("stored-case-%d", index), ObservedSpeakers: []string{"erick"}}})
	}
	return seed, refs
}

func TestQualificationEvidenceStoreDeepClonesSeedAndReturns(t *testing.T) {
	seed, refs := qualificationEvidenceAttemptSeed(1)
	targetRef := TranscriptionEvidenceTargetRef{ID: "stored-target", TenantID: "tenant-acme", Receipt: "stored-target-token"}
	seed.TranscriptionTargets = []StoredTranscriptionEvidenceTarget{{Ref: targetRef, IntegrityBindings: []TranscriptionIntegrityBinding{{Sequence: 1}}, IntegrityEvents: []TranscriptionIntegrityEvent{{Sequence: 1}}}}
	batchRef := DictationEvidenceBatchRef{ID: "stored-batch", TenantID: "tenant-acme", Receipt: "stored-batch-token"}
	seed.DictationBatches = []StoredDictationEvidenceBatch{{Ref: batchRef, Observations: []DictationQualificationObservation{{PostReceiptDigests: []string{"post-original"}, ModelCallReceiptDigests: []string{"model-original"}}}, TranscriptionManifest: TranscriptionCorpusManifest{Cases: []TranscriptionCorpusCase{{Tags: []string{"noise"}, RequiredTerms: []string{"term"}, ExpectedSpeakers: []string{"erick"}}}}, TranscriptionObservations: []TranscriptionObservation{{ObservedSpeakers: []string{"erick"}}}}}

	harness := newQualificationEvidenceStoreHarness(t, "deep-clone", seed, time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC))
	seed.ProviderAttempts[0].Observation.ObservedSpeakers[0] = "mutated-seed"
	seed.TranscriptionTargets[0].IntegrityBindings[0].Sequence = 99
	seed.DictationBatches[0].Observations[0].PostReceiptDigests[0] = "mutated-seed"

	attempt, err := harness.authority.ConsumeProviderAttempt(context.Background(), refs[0])
	if err != nil || attempt.ObservedSpeakers[0] != "erick" {
		t.Fatalf("provider attempt seed alias leaked: observation=%+v err=%v", attempt, err)
	}
	attempt.ObservedSpeakers[0] = "mutated-return"
	if harness.authority.attempts[refs[0].Receipt].Observation.ObservedSpeakers[0] != "erick" {
		t.Fatal("provider attempt return aliased stored evidence")
	}
	target, err := harness.authority.ConsumeEvidenceTarget(context.Background(), targetRef)
	if err != nil || target.IntegrityBindings[0].Sequence != 1 {
		t.Fatalf("target seed alias leaked: target=%+v err=%v", target, err)
	}
	target.IntegrityBindings[0].Sequence = 88
	if harness.authority.targets[targetRef.Receipt].IntegrityBindings[0].Sequence != 1 {
		t.Fatal("target return aliased stored evidence")
	}
	batch, err := harness.authority.ConsumeDictationEvidence(context.Background(), batchRef)
	if err != nil || batch.Observations[0].PostReceiptDigests[0] != "post-original" {
		t.Fatalf("dictation seed alias leaked: batch=%+v err=%v", batch, err)
	}
	batch.Observations[0].PostReceiptDigests[0] = "mutated-return"
	batch.TranscriptionManifest.Cases[0].Tags[0] = "mutated-return"
	batch.TranscriptionObservations[0].ObservedSpeakers[0] = "mutated-return"
	storedBatch := harness.authority.dictation[batchRef.Receipt]
	if storedBatch.Observations[0].PostReceiptDigests[0] != "post-original" || storedBatch.TranscriptionManifest.Cases[0].Tags[0] != "noise" || storedBatch.TranscriptionObservations[0].ObservedSpeakers[0] != "erick" {
		t.Fatal("dictation return aliased stored evidence")
	}
}

func TestQualificationEvidenceStoreSerializesTwoInstancesAndRestart(t *testing.T) {
	seed, refs := qualificationEvidenceAttemptSeed(1)
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "shared.jsonl")
	now := func() time.Time { return time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC) }
	first, err := OpenQualificationEvidenceStore(path, seed, "tenant-acme", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenQualificationEvidenceStore(path, seed, "tenant-acme", now)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	for _, store := range []*QualificationEvidenceStore{first, second} {
		wait.Add(1)
		go func(store *QualificationEvidenceStore) {
			defer wait.Done()
			_, consumeErr := store.ConsumeProviderAttempt(context.Background(), refs[0])
			errorsFound <- consumeErr
		}(store)
	}
	wait.Wait()
	close(errorsFound)
	successes := 0
	for consumeErr := range errorsFound {
		if consumeErr == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("cross-instance one-use successes=%d want=1", successes)
	}
	restarted, err := OpenQualificationEvidenceStore(path, seed, "tenant-acme", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ConsumeProviderAttempt(context.Background(), refs[0]); err == nil {
		t.Fatal("restart forgot consumed evidence")
	}
}

func TestQualificationEvidenceStoreFullWriteAndRollback(t *testing.T) {
	seed, refs := qualificationEvidenceAttemptSeed(3)
	harness := newQualificationEvidenceStoreHarness(t, "write-rollback", seed, time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC))
	store := harness.authority
	defaultWrite := store.write
	partial := true
	store.write = func(file *os.File, value []byte) (int, error) {
		if partial && len(value) > 1 {
			partial = false
			return file.Write(value[:len(value)/2])
		}
		return defaultWrite(file, value)
	}
	if _, err := store.ConsumeProviderAttempt(context.Background(), refs[0]); err != nil {
		t.Fatalf("full-write loop rejected valid partial write: %v", err)
	}

	store.write = func(_ *os.File, _ []byte) (int, error) { return 0, nil }
	if _, err := store.ConsumeProviderAttempt(context.Background(), refs[1]); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("zero write did not fail closed: %v", err)
	}
	store.write = defaultWrite
	if _, err := store.ConsumeProviderAttempt(context.Background(), refs[1]); err != nil {
		t.Fatalf("safe retry after zero-write rollback failed: %v", err)
	}

	defaultSync := store.sync
	failSync := true
	store.sync = func(file *os.File) error {
		if failSync {
			failSync = false
			return errors.New("injected sync failure")
		}
		return defaultSync(file)
	}
	if _, err := store.ConsumeProviderAttempt(context.Background(), refs[2]); err == nil {
		t.Fatal("sync failure did not fail closed")
	}
	store.sync = defaultSync
	if _, err := store.ConsumeProviderAttempt(context.Background(), refs[2]); err != nil {
		t.Fatalf("safe retry after sync rollback failed: %v", err)
	}
	if _, err := harness.reopen(t).ConsumeProviderAttempt(context.Background(), refs[2]); err == nil {
		t.Fatal("reopen lost successfully consumed evidence")
	}
}

func TestQualificationEvidenceStoreRejectsInsecurePathsAndCorruption(t *testing.T) {
	seed, _ := qualificationEvidenceAttemptSeed(1)
	now := func() time.Time { return time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC) }
	t.Run("symlink", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		realPath := filepath.Join(directory, "real.jsonl")
		if err := os.WriteFile(realPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		linkPath := filepath.Join(directory, "link.jsonl")
		if err := os.Symlink(realPath, linkPath); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenQualificationEvidenceStore(linkPath, seed, "tenant-acme", now); err == nil {
			t.Fatal("symlink ledger accepted")
		}
	})
	t.Run("mode", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "mode.jsonl")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenQualificationEvidenceStore(path, seed, "tenant-acme", now); err == nil {
			t.Fatal("non-private ledger accepted")
		}
	})
	t.Run("directory", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.Chmod(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenQualificationEvidenceStore(filepath.Join(directory, "ledger.jsonl"), seed, "tenant-acme", now); err == nil {
			t.Fatal("non-private ledger directory accepted")
		}
	})
	t.Run("corruption", func(t *testing.T) {
		harness := newQualificationEvidenceStoreHarness(t, "corrupt", seed, now())
		if err := os.WriteFile(harness.ledgerPath, []byte("not-json\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenQualificationEvidenceStore(harness.ledgerPath, seed, "tenant-acme", now); err == nil {
			t.Fatal("corrupt ledger accepted")
		}
	})
}

func liveTranscriptionQualificationFixture(t *testing.T) (TranscriptionCorpusManifest, []TranscriptionProviderAttemptRef, TranscriptionEvidenceTargetRef, *QualificationEvidenceStore) {
	t.Helper()
	manifest, evidence, thresholds := preregisteredTranscriptionEvidence(t)
	attempts := make([]TranscriptionProviderAttemptRef, 0, len(evidence.Observations))
	seed := QualificationEvidenceSeed{}
	for index, original := range evidence.Observations {
		observation := original
		observation.Synthetic = false
		attempt := TranscriptionProviderAttemptRef{ID: fmt.Sprintf("provider-attempt-%03d", index), TenantID: "tenant-acme", CorpusDigest: manifest.Digest, CaseID: observation.CaseID, InputAudioSHA256: observation.InputAudioSHA256, Model: observation.Model, RouteDigest: observation.RouteDigest, SegmentID: observation.SegmentID, ProviderItemIDHash: observation.ProviderItemIDHash, TrackID: observation.TrackID, ConsentReceiptDigest: observation.ConsentReceiptDigest}
		attempt.Receipt = fmt.Sprintf("provider-attempt-token-%03d", index)
		attempts = append(attempts, attempt)
		seed.ProviderAttempts = append(seed.ProviderAttempts, StoredProviderAttemptEvidence{Ref: attempt, Observation: observation})
	}
	targetRef := TranscriptionEvidenceTargetRef{ID: "target-transcription-e10", TenantID: "tenant-acme", RegistryDigest: strings.Repeat("b", 64), TargetDigest: strings.Repeat("c", 64), CandidateDigest: strings.Repeat("d", 64), CorpusDigest: manifest.Digest, ThresholdsDigest: workDigest(thresholds)}
	target := StoredTranscriptionEvidenceTarget{Thresholds: thresholds, IntegrityBindings: evidence.IntegrityBindings, IntegrityEvents: evidence.IntegrityEvents, OperatorSignatureDigest: strings.Repeat("e", 64), ReviewerSignatureDigest: strings.Repeat("f", 64)}
	targetRef.Receipt = "transcription-target-token"
	target.Ref = targetRef
	seed.TranscriptionTargets = append(seed.TranscriptionTargets, target)
	authority := newQualificationEvidenceStoreHarness(t, "transcription", seed, time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC)).authority
	return manifest, attempts, targetRef, authority
}

func preregisteredTranscriptionEvidence(t *testing.T) (TranscriptionCorpusManifest, TranscriptionQualificationEvidence, TranscriptionEvidenceThresholds) {
	t.Helper()
	cases := make([]TranscriptionCorpusCase, 0, 120)
	observations := make([]TranscriptionObservation, 0, 120)
	start := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for index := 0; index < 120; index++ {
		id := fmt.Sprintf("clip-%03d", index)
		name := fmt.Sprintf("StrideTerm%d", index)
		number := fmt.Sprintf("%d", index+100)
		text := "Erick approved " + name + " for " + number + " accounts with StrideTeam in 2026"
		tags := []string{"company_name", "numbers", "speaker_attribution"}
		requiredTerms := []string{name, "StrideTeam"}
		expectedSpeakers := []string{"erick"}
		switch {
		case index < 20:
			tags = append(tags, "accent")
		case index < 40:
			tags = append(tags, "code_switching")
			text = "Erick aprobó " + name + " for " + number + " accounts with StrideTeam in 2026"
		case index < 60:
			tags = append(tags, "short_phrase")
			text = name + " " + number + " StrideTeam 2026"
		case index < 80:
			tags = append(tags, "crosstalk")
			expectedSpeakers = []string{"erick", "aj"}
		case index < 100:
			tags = append(tags, "noise")
		case index >= 110:
			text = ""
			tags = []string{"silence"}
			requiredTerms = nil
			expectedSpeakers = nil
		}
		cases = append(cases, TranscriptionCorpusCase{ID: id, AudioSHA256: fmt.Sprintf("%064x", index+1), ReferenceText: text, Tags: tags, RequiredTerms: requiredTerms, ExpectedSpeakers: expectedSpeakers, DurationMS: 30_000})
		observations = append(observations, TranscriptionObservation{CaseID: id, Model: "gpt-transcribe", RouteDigest: strings.Repeat("a", 64), OutputText: text, IncumbentOutputText: text, SegmentID: "segment-" + id, ProviderItemIDHash: fmt.Sprintf("%064x", index+1000), InputAudioSHA256: fmt.Sprintf("%064x", index+1), TrackID: fmt.Sprintf("track-%03d", index), ConsentReceiptDigest: fmt.Sprintf("%064x", index+20_000), ObservedSpeakers: expectedSpeakers, CommittedAt: start, FinalAt: start.Add(time.Duration(500+index%20) * time.Millisecond), Synthetic: true, ProviderReceiptDigest: fmt.Sprintf("%064x", index+30_000)})
	}
	manifest, err := FreezeTranscriptionCorpus(2, cases)
	if err != nil {
		t.Fatal(err)
	}
	bindings := make([]TranscriptionIntegrityBinding, 0, 10_000)
	events := make([]TranscriptionIntegrityEvent, 0, 10_000)
	for index := 0; index < 10_000; index++ {
		caseID := fmt.Sprintf("clip-%03d", index%120)
		attemptID := fmt.Sprintf("integrity-attempt-%05d", index)
		segmentID := fmt.Sprintf("integrity-segment-%05d", index)
		itemDigest := fmt.Sprintf("%064x", index+40_000)
		providerDigest := fmt.Sprintf("%064x", index+60_000)
		trackID := fmt.Sprintf("integrity-track-%05d", index)
		consentDigest := fmt.Sprintf("%064x", index+80_000)
		speakerID := ""
		expectedSpeakers := manifest.Cases[index%120].ExpectedSpeakers
		if len(expectedSpeakers) > 0 {
			speakerID = expectedSpeakers[index%len(expectedSpeakers)]
		}
		bindings = append(bindings, TranscriptionIntegrityBinding{Sequence: int64(index + 1), TenantID: "tenant-acme", CandidateDigest: strings.Repeat("d", 64), Model: "gpt-transcribe", RouteDigest: strings.Repeat("a", 64), AttemptID: attemptID, CaseID: caseID, InputAudioSHA256: manifest.Cases[index%120].AudioSHA256, SegmentID: segmentID, ProviderItemIDHash: itemDigest, ProviderReceiptDigest: providerDigest, TrackID: trackID, ConsentReceiptDigest: consentDigest, SpeakerID: speakerID})
		events = append(events, TranscriptionIntegrityEvent{Sequence: int64(index + 1), TenantID: "tenant-acme", CandidateDigest: strings.Repeat("d", 64), Model: "gpt-transcribe", RouteDigest: strings.Repeat("a", 64), AttemptID: attemptID, CaseID: caseID, InputAudioSHA256: manifest.Cases[index%120].AudioSHA256, SegmentID: segmentID, ProviderItemIDHash: itemDigest, ProviderReceiptDigest: providerDigest, TrackID: trackID, ConsentReceiptDigest: consentDigest, SpeakerID: speakerID, Terminal: true})
	}
	// The harness must tolerate the actual failure-prone order: terminals can
	// complete in any order while their stable sequence bindings remain unique.
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return manifest, TranscriptionQualificationEvidence{Observations: observations, IntegrityBindings: bindings, IntegrityEvents: events}, TranscriptionEvidenceThresholds{
		Base:         TranscriptionQualificationThresholds{MaxMeanWER: .10, MinRequiredTermRecall: .97, MaxP95FinalLatencyMS: 3000, RequireSpeakerMatch: true, RequireSegmentBinding: true},
		MinimumClips: 120, MinimumDurationMS: 60 * 60 * 1000, MaximumWERDeltaToIncumbent: .005, MinimumDomainTermAccuracy: .97, MinimumNumericAccuracy: .98, MinimumIntegrityEvents: 10_000, BootstrapSamples: 400,
	}
}

func dictationTranscriptionEvidence(t *testing.T, dictations []DictationQualificationObservation) (TranscriptionCorpusManifest, TranscriptionQualificationEvidence, TranscriptionEvidenceThresholds) {
	t.Helper()
	cases := make([]TranscriptionCorpusCase, 0, len(dictations))
	observations := make([]TranscriptionObservation, 0, len(dictations))
	start := time.Date(2026, 8, 1, 12, 30, 0, 0, time.UTC)
	corpusIndex := 0
	for _, dictation := range dictations {
		if dictation.ModelCalls == 0 {
			continue
		}
		name := fmt.Sprintf("DictationTerm%d", corpusIndex)
		number := fmt.Sprintf("%d", corpusIndex+1000)
		text := "Erick dictated " + name + " for " + number + " accounts with StrideTeam in 2026"
		tags := []string{"company_name", "numbers", "speaker_attribution"}
		requiredTerms := []string{name, "StrideTeam"}
		expectedSpeakers := []string{"erick"}
		switch {
		case corpusIndex < 10:
			text = ""
			tags = []string{"silence"}
			requiredTerms = nil
			expectedSpeakers = nil
		case corpusIndex < 30:
			tags = append(tags, "accent")
		case corpusIndex < 50:
			tags = append(tags, "code_switching")
			text = "Erick dictó " + name + " for " + number + " accounts with StrideTeam in 2026"
		case corpusIndex < 70:
			tags = append(tags, "short_phrase")
			text = name + " " + number + " StrideTeam 2026"
		case corpusIndex < 90:
			tags = append(tags, "crosstalk")
			expectedSpeakers = []string{"erick", "aj"}
		case corpusIndex < 110:
			tags = append(tags, "noise")
		}
		cases = append(cases, TranscriptionCorpusCase{ID: dictation.ID, AudioSHA256: dictation.AudioSHA256, ReferenceText: text, Tags: tags, RequiredTerms: requiredTerms, ExpectedSpeakers: expectedSpeakers, DurationMS: dictation.ClipDurationMS})
		observations = append(observations, TranscriptionObservation{CaseID: dictation.ID, Model: "gpt-transcribe", RouteDigest: strings.Repeat("a", 64), OutputText: text, IncumbentOutputText: text, SegmentID: "segment-" + dictation.ID, ProviderItemIDHash: fmt.Sprintf("%064x", corpusIndex+200_000), InputAudioSHA256: dictation.AudioSHA256, TrackID: fmt.Sprintf("dictation-track-%03d", corpusIndex), ConsentReceiptDigest: fmt.Sprintf("%064x", corpusIndex+220_000), ObservedSpeakers: expectedSpeakers, CommittedAt: start, FinalAt: start.Add(time.Duration(500+corpusIndex%20) * time.Millisecond), Synthetic: true, ProviderReceiptDigest: dictation.TranscriptReceiptDigest})
		corpusIndex++
	}
	manifest, err := FreezeTranscriptionCorpus(3, cases)
	if err != nil {
		t.Fatal(err)
	}
	bindings := make([]TranscriptionIntegrityBinding, 0, 10_000)
	events := make([]TranscriptionIntegrityEvent, 0, 10_000)
	for index := 0; index < 10_000; index++ {
		testCase := manifest.Cases[index%len(manifest.Cases)]
		speakerID := ""
		if len(testCase.ExpectedSpeakers) > 0 {
			speakerID = testCase.ExpectedSpeakers[index%len(testCase.ExpectedSpeakers)]
		}
		binding := TranscriptionIntegrityBinding{Sequence: int64(index + 1), TenantID: "tenant-acme", CandidateDigest: strings.Repeat("d", 64), Model: "gpt-transcribe", RouteDigest: strings.Repeat("a", 64), AttemptID: fmt.Sprintf("dictation-integrity-attempt-%05d", index), CaseID: testCase.ID, InputAudioSHA256: testCase.AudioSHA256, SegmentID: fmt.Sprintf("dictation-integrity-segment-%05d", index), ProviderItemIDHash: fmt.Sprintf("%064x", index+240_000), ProviderReceiptDigest: fmt.Sprintf("%064x", index+260_000), TrackID: fmt.Sprintf("dictation-integrity-track-%05d", index), ConsentReceiptDigest: fmt.Sprintf("%064x", index+280_000), SpeakerID: speakerID}
		bindings = append(bindings, binding)
		events = append(events, TranscriptionIntegrityEvent{Sequence: binding.Sequence, TenantID: binding.TenantID, CandidateDigest: binding.CandidateDigest, Model: binding.Model, RouteDigest: binding.RouteDigest, AttemptID: binding.AttemptID, CaseID: binding.CaseID, InputAudioSHA256: binding.InputAudioSHA256, SegmentID: binding.SegmentID, ProviderItemIDHash: binding.ProviderItemIDHash, ProviderReceiptDigest: binding.ProviderReceiptDigest, TrackID: binding.TrackID, ConsentReceiptDigest: binding.ConsentReceiptDigest, SpeakerID: binding.SpeakerID, Terminal: true})
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	thresholds := TranscriptionEvidenceThresholds{Base: TranscriptionQualificationThresholds{MaxMeanWER: .10, MinRequiredTermRecall: .97, MaxP95FinalLatencyMS: 3000, RequireSpeakerMatch: true, RequireSegmentBinding: true}, MinimumClips: 120, MinimumDurationMS: 60 * 60 * 1000, MaximumWERDeltaToIncumbent: .005, MinimumDomainTermAccuracy: .97, MinimumNumericAccuracy: .98, MinimumIntegrityEvents: 10_000, BootstrapSamples: 400}
	return manifest, TranscriptionQualificationEvidence{Observations: observations, IntegrityBindings: bindings, IntegrityEvents: events}, thresholds
}

func liveDictationTranscriptionQualificationFixture(t *testing.T, dictations []DictationQualificationObservation) (TranscriptionCorpusManifest, []TranscriptionObservation, TranscriptionEvidenceThresholds, TranscriptionEvidenceTargetRef, TranscriptionQualificationCandidate, *QualificationEvidenceStore) {
	t.Helper()
	manifest, evidence, thresholds := dictationTranscriptionEvidence(t, dictations)
	attempts := make([]TranscriptionProviderAttemptRef, 0, len(evidence.Observations))
	seed := QualificationEvidenceSeed{}
	for index, original := range evidence.Observations {
		observation := original
		observation.Synthetic = false
		attempt := TranscriptionProviderAttemptRef{ID: fmt.Sprintf("dictation-provider-attempt-%03d", index), TenantID: "tenant-acme", CorpusDigest: manifest.Digest, CaseID: observation.CaseID, InputAudioSHA256: observation.InputAudioSHA256, Model: observation.Model, RouteDigest: observation.RouteDigest, SegmentID: observation.SegmentID, ProviderItemIDHash: observation.ProviderItemIDHash, TrackID: observation.TrackID, ConsentReceiptDigest: observation.ConsentReceiptDigest, Receipt: fmt.Sprintf("dictation-provider-token-%03d", index)}
		attempts = append(attempts, attempt)
		seed.ProviderAttempts = append(seed.ProviderAttempts, StoredProviderAttemptEvidence{Ref: attempt, Observation: observation})
		evidence.Observations[index] = observation
	}
	targetRef := TranscriptionEvidenceTargetRef{ID: "target-dictation-transcription-e10", TenantID: "tenant-acme", RegistryDigest: strings.Repeat("b", 64), TargetDigest: strings.Repeat("c", 64), CandidateDigest: strings.Repeat("d", 64), CorpusDigest: manifest.Digest, ThresholdsDigest: workDigest(thresholds), Receipt: "dictation-transcription-target-token"}
	target := StoredTranscriptionEvidenceTarget{Ref: targetRef, Thresholds: thresholds, IntegrityBindings: evidence.IntegrityBindings, IntegrityEvents: evidence.IntegrityEvents, OperatorSignatureDigest: strings.Repeat("e", 64), ReviewerSignatureDigest: strings.Repeat("f", 64)}
	seed.TranscriptionTargets = append(seed.TranscriptionTargets, target)
	authority := newQualificationEvidenceStoreHarness(t, "dictation-transcription", seed, time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC)).authority
	candidate, err := EvaluateLiveTranscriptionCandidate(context.Background(), "tenant-acme", manifest, attempts, targetRef, authority)
	if err != nil {
		t.Fatal(err)
	}
	return manifest, evidence.Observations, thresholds, targetRef, candidate, authority
}

func localDictationCandidateFixture(t *testing.T, observations []DictationQualificationObservation, thresholds ComposerDictationThresholds) (DictationEvidenceBatchRef, StoredDictationEvidenceBatch, TranscriptionQualificationCandidate, *QualificationEvidenceStore, *QualificationEvidenceStore) {
	t.Helper()
	clonedObservations := append([]DictationQualificationObservation(nil), observations...)
	manifest, transcriptionObservations, transcriptionThresholds, transcriptionTarget, candidate, transcriptionAuthority := liveDictationTranscriptionQualificationFixture(t, clonedObservations)
	ref := DictationEvidenceBatchRef{
		ID: "dictation-e10-target", TenantID: "tenant-acme",
		RegistryDigest: transcriptionTarget.RegistryDigest, TargetDigest: strings.Repeat("2", 64),
		CandidateDigest: transcriptionTarget.CandidateDigest, BuildDigest: strings.Repeat("7", 64),
		TranscriptionTargetDigest: transcriptionTarget.TargetDigest, TranscriptionCorpusDigest: manifest.Digest,
		TranscriptionObservationSetDigest: candidate.Receipt.ObservationSetDigest,
		TranscriptionReceiptDigest:        candidate.Receipt.ReceiptDigest, TranscriptionCandidateDigest: workDigest(candidate),
		DictationCorpusDigest: dictationCorpusDigest(clonedObservations), ObservationSetDigest: dictationObservationSetDigest(clonedObservations),
		ThresholdsDigest: workDigest(thresholds), Receipt: "local-batch-once",
	}
	batch := StoredDictationEvidenceBatch{
		Ref: ref, Observations: clonedObservations, Thresholds: thresholds,
		TranscriptionManifest: manifest, TranscriptionObservations: transcriptionObservations, TranscriptionThresholds: transcriptionThresholds,
		OperatorSignatureDigest: strings.Repeat("3", 64), ReviewerSignatureDigest: strings.Repeat("4", 64),
	}
	dictationAuthority := newQualificationEvidenceStoreHarness(t, "dictation", QualificationEvidenceSeed{DictationBatches: []StoredDictationEvidenceBatch{batch}}, time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC)).authority
	return ref, batch, candidate, dictationAuthority, transcriptionAuthority
}

func TestTranscriptionEvidenceEnforcesPreregisteredCorpusStatisticsAndHonestBoundary(t *testing.T) {
	manifest, evidence, thresholds := preregisteredTranscriptionEvidence(t)
	receipt, err := EvaluateTranscriptionEvidence(manifest, evidence, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.PreregisteredPass || receipt.LiveOrDeviceQualified || receipt.ClipCount != 120 || receipt.DurationMS != 60*60*1000 || receipt.P50FinalLatencyMS == 0 || receipt.P99FinalLatencyMS == 0 || receipt.Integrity.EventCount != 10_000 || !receipt.Integrity.DeterministicPass || !receipt.Integrity.AllSpeakerBound || !receipt.SilencePass || receipt.SilenceCaseCount != 10 || receipt.SilenceFalseInsertions != 0 {
		t.Fatalf("receipt=%+v", receipt)
	}
	if receipt.DomainTermAccuracy.Method != "wilson_95" || receipt.DomainTermAccuracy.Numerator == 0 || receipt.DomainTermAccuracy.Denominator == 0 || receipt.NumericAccuracy.Method != "wilson_95" || receipt.WER.Method != "deterministic_bootstrap_ratio_95" || receipt.P50FinalLatency.Method != "deterministic_quantile_bootstrap_95" || receipt.P95FinalLatency.Method != "deterministic_quantile_bootstrap_95" || receipt.P99FinalLatency.Method != "deterministic_quantile_bootstrap_95" {
		t.Fatalf("interval methods=%+v %+v %+v %+v/%+v/%+v", receipt.DomainTermAccuracy, receipt.NumericAccuracy, receipt.WER, receipt.P50FinalLatency, receipt.P95FinalLatency, receipt.P99FinalLatency)
	}
	if receipt.WERDeltaInterval.Method != "deterministic_paired_bootstrap_ratio_delta_95" || receipt.WERDeltaInterval.High > thresholds.MaximumWERDeltaToIncumbent {
		t.Fatalf("paired non-inferiority interval=%+v", receipt.WERDeltaInterval)
	}
	if receipt.WER.High > thresholds.Base.MaxMeanWER || receipt.DomainTermAccuracy.Low < thresholds.MinimumDomainTermAccuracy || receipt.NumericAccuracy.Low < thresholds.MinimumNumericAccuracy || receipt.P95FinalLatency.High > float64(thresholds.Base.MaxP95FinalLatencyMS) {
		t.Fatalf("qualification did not conservatively gate intervals: %+v", receipt)
	}

	downgraded := thresholds
	downgraded.MinimumClips = 1
	if _, err := EvaluateTranscriptionEvidence(manifest, evidence, downgraded); err == nil {
		t.Fatal("qualification accepted a corpus-size threshold below the E10 floor")
	}
	if receipt.ReceiptDigest == "" || len(receipt.ResidualGates) == 0 {
		t.Fatalf("missing honest receipt boundary=%+v", receipt)
	}

	wrongNumber := evidence
	wrongNumber.Observations = append([]TranscriptionObservation(nil), evidence.Observations...)
	for index := 0; index < 5; index++ {
		wrongNumber.Observations[index].OutputText = fmt.Sprintf("Erick approved StrideTerm%d for 999 accounts", index)
	}
	failed, err := EvaluateTranscriptionEvidence(manifest, wrongNumber, thresholds)
	if err != nil || failed.PreregisteredPass {
		t.Fatalf("numeric regression receipt=%+v err=%v", failed, err)
	}
}

func TestTranscriptionEvidenceRequiresCompleteE10CoverageAndZeroSilenceInsertions(t *testing.T) {
	requiredTags := []string{"company_name", "numbers", "accent", "code_switching", "short_phrase", "crosstalk", "noise", "silence", "speaker_attribution"}
	for _, missingTag := range requiredTags {
		t.Run("missing "+missingTag, func(t *testing.T) {
			manifest, evidence, thresholds := preregisteredTranscriptionEvidence(t)
			cases := append([]TranscriptionCorpusCase(nil), manifest.Cases...)
			for index := range cases {
				cases[index].Tags = append([]string(nil), cases[index].Tags...)
				cases[index].RequiredTerms = append([]string(nil), cases[index].RequiredTerms...)
				cases[index].ExpectedSpeakers = append([]string(nil), cases[index].ExpectedSpeakers...)
				filtered := cases[index].Tags[:0]
				for _, tag := range cases[index].Tags {
					if tag != missingTag {
						filtered = append(filtered, tag)
					}
				}
				cases[index].Tags = filtered
				if missingTag == "silence" && isTranscriptionSilenceCase(manifest.Cases[index]) {
					cases[index].ReferenceText = "control phrase"
					cases[index].Tags = []string{"noise"}
					for observationIndex := range evidence.Observations {
						if evidence.Observations[observationIndex].CaseID == cases[index].ID {
							evidence.Observations[observationIndex].OutputText = "control phrase"
							evidence.Observations[observationIndex].IncumbentOutputText = "control phrase"
						}
					}
				}
			}
			manifest, err := FreezeTranscriptionCorpus(manifest.Version, cases)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := EvaluateTranscriptionEvidence(manifest, evidence, thresholds); !errors.Is(err, ErrTranscriptionQualificationInvalid) {
				t.Fatalf("corpus missing %s was accepted: %v", missingTag, err)
			}
		})
	}

	manifest, evidence, thresholds := preregisteredTranscriptionEvidence(t)
	for index := range evidence.Observations {
		if isTranscriptionSilenceCase(manifest.Cases[index]) {
			evidence.Observations[index].OutputText = "hallucinated words"
			break
		}
	}
	receipt, err := EvaluateTranscriptionEvidence(manifest, evidence, thresholds)
	if err != nil || receipt.PreregisteredPass || receipt.SilencePass || receipt.SilenceFalseInsertions != 2 {
		t.Fatalf("silence false insertion was not fail-closed: receipt=%+v err=%v", receipt, err)
	}
}

func TestTranscriptionEvidenceRejectsPointEstimateOnlyPasses(t *testing.T) {
	manifest, evidence, thresholds := preregisteredTranscriptionEvidence(t)
	werUncertain := evidence
	werUncertain.Observations = append([]TranscriptionObservation(nil), evidence.Observations...)
	for index := 0; index < 15; index++ {
		shortened := fmt.Sprintf("StrideTerm%d %d StrideTeam 2026", index, index+100)
		werUncertain.Observations[index].OutputText = shortened
		werUncertain.Observations[index].IncumbentOutputText = shortened
	}
	receipt, err := EvaluateTranscriptionEvidence(manifest, werUncertain, thresholds)
	if err != nil || receipt.PreregisteredPass || receipt.WER.Point > thresholds.Base.MaxMeanWER || receipt.WER.High <= thresholds.Base.MaxMeanWER || receipt.WERDeltaInterval.High > thresholds.MaximumWERDeltaToIncumbent {
		t.Fatalf("WER point-estimate uncertainty was not fail-closed: receipt=%+v err=%v", receipt, err)
	}

	domainUncertain := evidence
	domainUncertain.Observations = append([]TranscriptionObservation(nil), evidence.Observations...)
	for index := 0; index < 2; index++ {
		domainUncertain.Observations[index].OutputText = strings.Replace(domainUncertain.Observations[index].OutputText, fmt.Sprintf("StrideTerm%d", index), "UnknownTerm", 1)
	}
	receipt, err = EvaluateTranscriptionEvidence(manifest, domainUncertain, thresholds)
	if err != nil || receipt.PreregisteredPass || receipt.DomainTermAccuracy.Point < thresholds.MinimumDomainTermAccuracy || receipt.DomainTermAccuracy.Low >= thresholds.MinimumDomainTermAccuracy {
		t.Fatalf("domain point-estimate uncertainty was not fail-closed: receipt=%+v err=%v", receipt, err)
	}

	numericUncertain := evidence
	numericUncertain.Observations = append([]TranscriptionObservation(nil), evidence.Observations...)
	numericUncertain.Observations[0].OutputText = strings.Replace(numericUncertain.Observations[0].OutputText, "100", "9999", 1)
	receipt, err = EvaluateTranscriptionEvidence(manifest, numericUncertain, thresholds)
	if err != nil || receipt.PreregisteredPass || receipt.NumericAccuracy.Point < thresholds.MinimumNumericAccuracy || receipt.NumericAccuracy.Low >= thresholds.MinimumNumericAccuracy {
		t.Fatalf("numeric point-estimate uncertainty was not fail-closed: receipt=%+v err=%v", receipt, err)
	}

	latencyUncertain := evidence
	latencyUncertain.Observations = append([]TranscriptionObservation(nil), evidence.Observations...)
	for index := 0; index < 6; index++ {
		latencyUncertain.Observations[index].FinalAt = latencyUncertain.Observations[index].CommittedAt.Add(4 * time.Second)
	}
	receipt, err = EvaluateTranscriptionEvidence(manifest, latencyUncertain, thresholds)
	if err != nil || receipt.PreregisteredPass || receipt.P95FinalLatency.Point > float64(thresholds.Base.MaxP95FinalLatencyMS) || receipt.P95FinalLatency.High <= float64(thresholds.Base.MaxP95FinalLatencyMS) {
		t.Fatalf("latency point-estimate uncertainty was not fail-closed: receipt=%+v err=%v", receipt, err)
	}
}

func TestTranscriptionIntegrityRejectsDuplicateSequenceEvenWhenInputIsOutOfOrder(t *testing.T) {
	manifest, evidence, thresholds := preregisteredTranscriptionEvidence(t)
	evidence.IntegrityEvents[1].Sequence = evidence.IntegrityEvents[0].Sequence
	if _, err := EvaluateTranscriptionIntegrity(manifest, evidence.IntegrityBindings, evidence.IntegrityEvents, thresholds.MinimumIntegrityEvents); err == nil {
		t.Fatal("duplicate sequence was accepted")
	}
}

func TestTranscriptionIntegrityRejectsRelabeledRelationshipsAndManifestTampering(t *testing.T) {
	for name, mutate := range map[string]func(*TranscriptionCorpusManifest, *TranscriptionQualificationEvidence){
		"reused provider item": func(_ *TranscriptionCorpusManifest, evidence *TranscriptionQualificationEvidence) {
			evidence.IntegrityBindings[1].ProviderItemIDHash = evidence.IntegrityBindings[0].ProviderItemIDHash
		},
		"wrong observed track": func(_ *TranscriptionCorpusManifest, evidence *TranscriptionQualificationEvidence) {
			evidence.IntegrityEvents[0].TrackID = "wrong-track"
		},
		"wrong consent binding": func(_ *TranscriptionCorpusManifest, evidence *TranscriptionQualificationEvidence) {
			evidence.IntegrityBindings[0].ConsentReceiptDigest = strings.Repeat("9", 64)
		},
		"swapped crosstalk speaker": func(manifest *TranscriptionCorpusManifest, evidence *TranscriptionQualificationEvidence) {
			for index := range evidence.IntegrityEvents {
				event := &evidence.IntegrityEvents[index]
				if event.CaseID != "clip-060" {
					continue
				}
				for _, testCase := range manifest.Cases {
					if testCase.ID == event.CaseID {
						if event.SpeakerID == testCase.ExpectedSpeakers[0] {
							event.SpeakerID = testCase.ExpectedSpeakers[1]
						} else {
							event.SpeakerID = testCase.ExpectedSpeakers[0]
						}
						return
					}
				}
			}
		},
		"tampered manifest": func(manifest *TranscriptionCorpusManifest, _ *TranscriptionQualificationEvidence) {
			manifest.Cases[0].ReferenceText = "tampered after freeze"
		},
	} {
		t.Run(name, func(t *testing.T) {
			manifest, evidence, thresholds := preregisteredTranscriptionEvidence(t)
			mutate(&manifest, &evidence)
			receipt, err := EvaluateTranscriptionEvidence(manifest, evidence, thresholds)
			if err == nil && receipt.PreregisteredPass {
				t.Fatal("misbound evidence passed")
			}
		})
	}
}

func TestTranscriptionEvidenceRejectsEveryAcceptanceFloorDowngrade(t *testing.T) {
	for name, mutate := range map[string]func(*TranscriptionEvidenceThresholds){
		"corpus clips":    func(value *TranscriptionEvidenceThresholds) { value.MinimumClips = 119 },
		"duration":        func(value *TranscriptionEvidenceThresholds) { value.MinimumDurationMS-- },
		"corpus WER":      func(value *TranscriptionEvidenceThresholds) { value.Base.MaxMeanWER = .1001 },
		"term accuracy":   func(value *TranscriptionEvidenceThresholds) { value.Base.MinRequiredTermRecall = .969 },
		"latency":         func(value *TranscriptionEvidenceThresholds) { value.Base.MaxP95FinalLatencyMS = 3001 },
		"non inferiority": func(value *TranscriptionEvidenceThresholds) { value.MaximumWERDeltaToIncumbent = .0051 },
		"domain":          func(value *TranscriptionEvidenceThresholds) { value.MinimumDomainTermAccuracy = .969 },
		"numeric":         func(value *TranscriptionEvidenceThresholds) { value.MinimumNumericAccuracy = .979 },
		"integrity":       func(value *TranscriptionEvidenceThresholds) { value.MinimumIntegrityEvents = 9999 },
		"bootstrap":       func(value *TranscriptionEvidenceThresholds) { value.BootstrapSamples = 399 },
	} {
		t.Run(name, func(t *testing.T) {
			manifest, evidence, thresholds := preregisteredTranscriptionEvidence(t)
			mutate(&thresholds)
			if _, err := EvaluateTranscriptionEvidence(manifest, evidence, thresholds); err == nil {
				t.Fatal("downgraded threshold was accepted")
			}
		})
	}
}

func TestLiveTranscriptionQualificationRequiresFullStoredEvidenceAndIsOneUse(t *testing.T) {
	manifest, attempts, target, authority := liveTranscriptionQualificationFixture(t)
	candidate, err := EvaluateLiveTranscriptionCandidate(context.Background(), "tenant-acme", manifest, attempts, target, authority)
	if err != nil || candidate.State != TranscriptionEvidenceCandidateState || !candidate.Receipt.PreregisteredPass || candidate.Receipt.LiveOrDeviceQualified || candidate.Receipt.Score.ProviderQualified || candidate.Receipt.RegistryDigest != target.RegistryDigest {
		t.Fatalf("candidate=%+v err=%v", candidate, err)
	}
	wantAttemptSet := transcriptionProviderAttemptSetDigest(attempts)
	if candidate.Receipt.ProviderAttemptSetDigest != wantAttemptSet || !isHexDigest(candidate.Receipt.ObservationSetDigest) || len(candidate.Receipt.ResidualGates) == 0 {
		t.Fatalf("candidate evidence-set bindings=%+v", candidate.Receipt)
	}
	reordered := append([]TranscriptionProviderAttemptRef(nil), attempts...)
	for left, right := 0, len(reordered)-1; left < right; left, right = left+1, right-1 {
		reordered[left], reordered[right] = reordered[right], reordered[left]
	}
	if transcriptionProviderAttemptSetDigest(reordered) != wantAttemptSet {
		t.Fatal("attempt-set digest changed with ordering")
	}
	changedAttempt := append([]TranscriptionProviderAttemptRef(nil), attempts...)
	changedAttempt[0].Receipt += "-different"
	if transcriptionProviderAttemptSetDigest(changedAttempt) == wantAttemptSet {
		t.Fatal("attempt-set digest ignored the exact opaque evidence receipt")
	}
	if _, err := EvaluateLiveTranscriptionCandidate(context.Background(), "tenant-acme", manifest, attempts, target, authority); !errors.Is(err, ErrTranscriptionQualificationUnverified) {
		t.Fatalf("local evidence replay accepted: %v", err)
	}
}

func TestLiveTranscriptionQualificationRejectsCallerAuthoredOrMisboundEvidence(t *testing.T) {
	for name, mutate := range map[string]func(TranscriptionCorpusManifest, []TranscriptionProviderAttemptRef, TranscriptionEvidenceTargetRef, *QualificationEvidenceStore){
		"forged target receipt": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, _ *QualificationEvidenceStore) {
			target.Receipt = "forged"
		},
		"forged attempt receipt": func(_ TranscriptionCorpusManifest, attempts []TranscriptionProviderAttemptRef, _ TranscriptionEvidenceTargetRef, _ *QualificationEvidenceStore) {
			attempts[0].Receipt = "forged"
		},
		"cross corpus": func(_ TranscriptionCorpusManifest, attempts []TranscriptionProviderAttemptRef, _ TranscriptionEvidenceTargetRef, _ *QualificationEvidenceStore) {
			attempts[0].CorpusDigest = strings.Repeat("9", 64)
		},
		"wrong input audio": func(_ TranscriptionCorpusManifest, attempts []TranscriptionProviderAttemptRef, _ TranscriptionEvidenceTargetRef, _ *QualificationEvidenceStore) {
			attempts[0].InputAudioSHA256 = strings.Repeat("8", 64)
		},
		"duplicate provider item": func(_ TranscriptionCorpusManifest, attempts []TranscriptionProviderAttemptRef, _ TranscriptionEvidenceTargetRef, _ *QualificationEvidenceStore) {
			attempts[1].ProviderItemIDHash = attempts[0].ProviderItemIDHash
		},
		"synthetic stored attempt": func(_ TranscriptionCorpusManifest, attempts []TranscriptionProviderAttemptRef, _ TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.attempts[attempts[0].Receipt]
			stored.Observation.Synthetic = true
			authority.attempts[attempts[0].Receipt] = stored
		},
		"downgraded threshold": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.targets[target.Receipt]
			stored.Thresholds.Base.MaxMeanWER = 1
			authority.targets[target.Receipt] = stored
		},
		"relabeled integrity binding": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.targets[target.Receipt]
			stored.IntegrityBindings[0].ProviderItemIDHash = strings.Repeat("7", 64)
			authority.targets[target.Receipt] = stored
		},
		"integrity tenant mismatch": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.targets[target.Receipt]
			stored.IntegrityEvents[0].TenantID = "tenant-other"
			authority.targets[target.Receipt] = stored
		},
		"integrity candidate mismatch": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.targets[target.Receipt]
			stored.IntegrityEvents[0].CandidateDigest = strings.Repeat("8", 64)
			authority.targets[target.Receipt] = stored
		},
		"integrity model mismatch": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.targets[target.Receipt]
			stored.IntegrityEvents[0].Model = "model-other"
			authority.targets[target.Receipt] = stored
		},
		"integrity route mismatch": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.targets[target.Receipt]
			stored.IntegrityEvents[0].RouteDigest = strings.Repeat("8", 64)
			authority.targets[target.Receipt] = stored
		},
		"integrity audio mismatch": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.targets[target.Receipt]
			stored.IntegrityEvents[0].InputAudioSHA256 = strings.Repeat("8", 64)
			authority.targets[target.Receipt] = stored
		},
		"cross candidate integrity packet": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.targets[target.Receipt]
			for index := range stored.IntegrityBindings {
				stored.IntegrityBindings[index].CandidateDigest = strings.Repeat("8", 64)
				stored.IntegrityEvents[index].CandidateDigest = strings.Repeat("8", 64)
			}
			authority.targets[target.Receipt] = stored
		},
		"cross model integrity packet": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.targets[target.Receipt]
			for index := range stored.IntegrityBindings {
				stored.IntegrityBindings[index].Model = "model-other"
				stored.IntegrityEvents[index].Model = "model-other"
			}
			authority.targets[target.Receipt] = stored
		},
		"cross route integrity packet": func(_ TranscriptionCorpusManifest, _ []TranscriptionProviderAttemptRef, target TranscriptionEvidenceTargetRef, authority *QualificationEvidenceStore) {
			stored := authority.targets[target.Receipt]
			for index := range stored.IntegrityBindings {
				stored.IntegrityBindings[index].RouteDigest = strings.Repeat("8", 64)
				stored.IntegrityEvents[index].RouteDigest = strings.Repeat("8", 64)
			}
			authority.targets[target.Receipt] = stored
		},
	} {
		t.Run(name, func(t *testing.T) {
			manifest, attempts, target, authority := liveTranscriptionQualificationFixture(t)
			// Mutators operate on reference-backed slices/maps. For target-by-value
			// changes, mutate the authoritative store in the cases that need it.
			if name == "forged target receipt" {
				target.Receipt = "forged"
			} else {
				mutate(manifest, attempts, target, authority)
			}
			if _, err := EvaluateLiveTranscriptionCandidate(context.Background(), "tenant-acme", manifest, attempts, target, authority); !errors.Is(err, ErrTranscriptionQualificationUnverified) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestComposerDictationQualificationCoversAllTargetStatesButCannotSelfQualify(t *testing.T) {
	manifest, evidence, transcriptionThresholds := preregisteredTranscriptionEvidence(t)
	fidelity, err := EvaluateTranscriptionEvidence(manifest, evidence, transcriptionThresholds)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC)
	platforms := []DictationTargetPlatform{DictationTargetWeb, DictationTargetIPhone, DictationTargetIPad}
	surfaces := []DictationComposerSurface{DictationComposerScout, DictationComposerPrivate, DictationComposerTeam, DictationComposerProject}
	observations := make([]DictationQualificationObservation, 0, 500)
	for index := 0; index < 500; index++ {
		surface := surfaces[index%len(surfaces)]
		if index < 100 {
			surface = DictationComposerInRoom
		}
		modelCallReceipt := fmt.Sprintf("%064x", index+160_000)
		observation := DictationQualificationObservation{ID: fmt.Sprintf("dictation-%03d", index), Platform: platforms[index%len(platforms)], Composer: surface, EvidenceClass: DictationEvidencePhysical, Outcome: DictationSent, ClipDurationMS: 20_000, SendRequestedAt: start, PostedAt: start.Add(time.Duration(900+index%20) * time.Millisecond), PostCount: 1, ModelCalls: 1, FirstAttemptSucceeded: true, PersonalRealtimeHandoff: index < 100, MeetingHandoff: index >= 100 && index < 200, WaveformFPS: 60,
			AudioSHA256: fmt.Sprintf("%064x", index+100_000), TranscriptReceiptDigest: modelCallReceipt, LifecycleReceiptDigest: fmt.Sprintf("%064x", index+120_000), DeviceReceiptDigest: fmt.Sprintf("%064x", index+130_000), CandidateDigest: strings.Repeat("d", 64), BuildDigest: strings.Repeat("7", 64), PerformanceReceiptDigest: fmt.Sprintf("%064x", index+140_000), PostReceiptDigests: []string{fmt.Sprintf("%064x", index+150_000)}, ModelCallReceiptDigests: []string{modelCallReceipt}}
		if index < 100 {
			observation.Outcome = DictationCancelled
			observation.PostedAt = time.Time{}
			observation.PostCount = 0
			observation.CancellationRace = true
			observation.ModelCalls = 1
			observation.PostReceiptDigests = nil
			observation.FirstAttemptSucceeded = false
		}
		if index == 100 {
			observation.Outcome = DictationStopped
			observation.PostedAt = time.Time{}
			observation.PostCount = 0
			observation.ModelCalls = 0
			observation.PostReceiptDigests = nil
			observation.ModelCallReceiptDigests = nil
			observation.TranscriptReceiptDigest = ""
			observation.FirstAttemptSucceeded = false
		}
		observations = append(observations, observation)
	}
	thresholds := ComposerDictationThresholds{MinimumUtterances: 250, MinimumCancellationRaces: 100, MinimumMicHandoffs: 200, MinimumPersonalToDictate: 100, MinimumPersonalToMeeting: 100, MinimumInRoomDictations: 100, MinimumFirstAttemptRate: .99, MinimumWaveformFPS: 55, MaximumP50PostLatencyMS: 1500, MaximumP95PostLatencyMS: 3000}
	receipt, err := EvaluateComposerDictationQualification(observations, fidelity, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.DeterministicPass || receipt.ProviderQualified || receipt.DeviceQualified || !receipt.ExactlyOncePass || !receipt.CancellationPass || !receipt.MicGenerationPass || !receipt.InRoomPrivacyPass || !receipt.WaveformPass || !receipt.FidelityPass {
		t.Fatalf("receipt=%+v", receipt)
	}
	if receipt.PhysicalTargetDeviceCount != 500 || len(receipt.Platforms) != 3 || len(receipt.ComposerSurfaces) != 5 || receipt.PlatformComposerPairs != 15 || receipt.PersonalToDictateCount != 100 || receipt.PersonalToMeetingCount != 100 || receipt.P95SubmitToPostMS > 3000 || receipt.P50SubmitToPost.Method != "deterministic_quantile_bootstrap_95" || receipt.P95SubmitToPost.Method != "deterministic_quantile_bootstrap_95" || receipt.P99SubmitToPost.Method != "deterministic_quantile_bootstrap_95" {
		t.Fatalf("coverage=%+v", receipt)
	}
	if receipt.FirstAttemptRate.Low < thresholds.MinimumFirstAttemptRate || receipt.P50SubmitToPost.High > float64(thresholds.MaximumP50PostLatencyMS) || receipt.P95SubmitToPost.High > float64(thresholds.MaximumP95PostLatencyMS) {
		t.Fatalf("dictation qualification did not conservatively gate intervals: %+v", receipt)
	}
	uncertainFirstAttempt := append([]DictationQualificationObservation(nil), observations...)
	for index := range uncertainFirstAttempt {
		if uncertainFirstAttempt[index].Outcome == DictationSent {
			uncertainFirstAttempt[index].FirstAttemptSucceeded = false
			break
		}
	}
	uncertainReceipt, err := EvaluateComposerDictationQualification(uncertainFirstAttempt, fidelity, thresholds)
	if err != nil || uncertainReceipt.DeterministicPass || uncertainReceipt.FirstAttemptRate.Point < thresholds.MinimumFirstAttemptRate || uncertainReceipt.FirstAttemptRate.Low >= thresholds.MinimumFirstAttemptRate {
		t.Fatalf("dictation first-attempt point estimate crossed the confidence gate: receipt=%+v err=%v", uncertainReceipt, err)
	}
	uncertainLatency := append([]DictationQualificationObservation(nil), observations...)
	highLatencyCount := 0
	for index := range uncertainLatency {
		if uncertainLatency[index].Outcome == DictationSent && highLatencyCount < 19 {
			uncertainLatency[index].PostedAt = uncertainLatency[index].SendRequestedAt.Add(4 * time.Second)
			highLatencyCount++
		}
	}
	uncertainReceipt, err = EvaluateComposerDictationQualification(uncertainLatency, fidelity, thresholds)
	if err != nil || uncertainReceipt.DeterministicPass || uncertainReceipt.P95SubmitToPost.Point > float64(thresholds.MaximumP95PostLatencyMS) || uncertainReceipt.P95SubmitToPost.High <= float64(thresholds.MaximumP95PostLatencyMS) {
		t.Fatalf("dictation latency point estimate crossed the confidence gate: receipt=%+v err=%v", uncertainReceipt, err)
	}

	localOnly := append([]DictationQualificationObservation(nil), observations...)
	for index := range localOnly {
		localOnly[index].EvidenceClass = DictationEvidenceLocal
	}
	failed, err := EvaluateComposerDictationQualification(localOnly, fidelity, thresholds)
	if err != nil || !failed.DeterministicPass || failed.DeviceQualified {
		t.Fatalf("local deterministic evidence crossed the device trust boundary receipt=%+v err=%v", failed, err)
	}

	mixed := append([]DictationQualificationObservation(nil), observations...)
	mixed[0].EvidenceClass = DictationEvidenceLocal
	failed, err = EvaluateComposerDictationQualification(mixed, fidelity, thresholds)
	if err != nil || !failed.DeterministicPass || failed.DeviceQualified {
		t.Fatalf("mixed local evidence crossed the device trust boundary receipt=%+v err=%v", failed, err)
	}

	for name, mutate := range map[string]func(*ComposerDictationThresholds){
		"utterances":         func(value *ComposerDictationThresholds) { value.MinimumUtterances = 249 },
		"cancellation races": func(value *ComposerDictationThresholds) { value.MinimumCancellationRaces = 99 },
		"combined handoffs":  func(value *ComposerDictationThresholds) { value.MinimumMicHandoffs = 199 },
		"dictate handoffs":   func(value *ComposerDictationThresholds) { value.MinimumPersonalToDictate = 99 },
		"meeting handoffs":   func(value *ComposerDictationThresholds) { value.MinimumPersonalToMeeting = 99 },
		"in room":            func(value *ComposerDictationThresholds) { value.MinimumInRoomDictations = 99 },
		"first attempt":      func(value *ComposerDictationThresholds) { value.MinimumFirstAttemptRate = .989 },
		"waveform":           func(value *ComposerDictationThresholds) { value.MinimumWaveformFPS = 54.9 },
		"p50":                func(value *ComposerDictationThresholds) { value.MaximumP50PostLatencyMS = 1501 },
		"p95":                func(value *ComposerDictationThresholds) { value.MaximumP95PostLatencyMS = 3001 },
	} {
		t.Run("reject downgraded "+name, func(t *testing.T) {
			downgraded := thresholds
			mutate(&downgraded)
			if _, err := EvaluateComposerDictationQualification(observations, fidelity, downgraded); err == nil {
				t.Fatal("dictation qualification accepted a threshold below the E10 floor")
			}
		})
	}

	duplicatedEvidence := append([]DictationQualificationObservation(nil), observations...)
	duplicatedEvidence[1].LifecycleReceiptDigest = duplicatedEvidence[0].LifecycleReceiptDigest
	if _, err := EvaluateComposerDictationQualification(duplicatedEvidence, fidelity, thresholds); err == nil {
		t.Fatal("dictation qualification accepted reused lifecycle evidence")
	}
	duplicatedPostReceipt := append([]DictationQualificationObservation(nil), observations...)
	duplicatedPostReceipt[102].PostReceiptDigests = append([]string(nil), duplicatedPostReceipt[101].PostReceiptDigests...)
	if _, err := EvaluateComposerDictationQualification(duplicatedPostReceipt, fidelity, thresholds); err == nil {
		t.Fatal("dictation qualification accepted a post receipt reused across utterances")
	}
	duplicatedModelReceipt := append([]DictationQualificationObservation(nil), observations...)
	duplicatedModelReceipt[2].ModelCallReceiptDigests = append([]string(nil), duplicatedModelReceipt[1].ModelCallReceiptDigests...)
	if _, err := EvaluateComposerDictationQualification(duplicatedModelReceipt, fidelity, thresholds); err == nil {
		t.Fatal("dictation qualification accepted a model-call receipt reused across utterances")
	}
	stoppedWithTranscript := append([]DictationQualificationObservation(nil), observations...)
	stoppedWithTranscript[100].TranscriptReceiptDigest = strings.Repeat("9", 64)
	if _, err := EvaluateComposerDictationQualification(stoppedWithTranscript, fidelity, thresholds); err == nil {
		t.Fatal("stopped zero-call dictation carried impossible transcript/provider evidence")
	}
	multiCallFirstAttempt := append([]DictationQualificationObservation(nil), observations...)
	multiCallFirstAttempt[101].ModelCalls = 2
	multiCallFirstAttempt[101].ModelCallReceiptDigests = append(multiCallFirstAttempt[101].ModelCallReceiptDigests, strings.Repeat("8", 64))
	if _, err := EvaluateComposerDictationQualification(multiCallFirstAttempt, fidelity, thresholds); err == nil {
		t.Fatal("first-attempt success accepted more than one model call")
	}

	ref, _, transcriptionCandidate, store, _ := localDictationCandidateFixture(t, observations, thresholds)
	dictationCandidate, err := EvaluateComposerDictationEvidenceCandidate(context.Background(), "tenant-acme", ref, transcriptionCandidate, store)
	if err != nil || dictationCandidate.State != ComposerDictationEvidenceCandidateState || dictationCandidate.Receipt.ProviderQualified || dictationCandidate.Receipt.DeviceQualified || !dictationCandidate.Receipt.DeterministicPass || dictationCandidate.Receipt.RegistryDigest != ref.RegistryDigest {
		t.Fatalf("structure-only dictation candidate=%+v err=%v", dictationCandidate, err)
	}
	if dictationCandidate.Receipt.TranscriptionReceiptDigest != transcriptionCandidate.Receipt.ReceiptDigest || dictationCandidate.Receipt.DictationCorpusDigest != ref.DictationCorpusDigest || dictationCandidate.Receipt.ObservationSetDigest != ref.ObservationSetDigest || dictationCandidate.Receipt.BuildDigest != ref.BuildDigest || len(dictationCandidate.Receipt.ResidualGates) == 0 {
		t.Fatalf("dictation candidate binding receipt=%+v", dictationCandidate)
	}
	if _, err := EvaluateComposerDictationEvidenceCandidate(context.Background(), "tenant-acme", ref, transcriptionCandidate, store); !errors.Is(err, ErrDictationQualificationUnverified) {
		t.Fatalf("dictation evidence replay accepted: %v", err)
	}

	wrongTranscriptRef, wrongTranscriptBatch, wrongTranscriptCandidate, _, _ := localDictationCandidateFixture(t, observations, thresholds)
	wrongTranscriptBatch.TranscriptionObservations[0].OutputText = "arbitrarily wrong transcript"
	wrongTranscriptBatch.Ref = wrongTranscriptRef
	wrongTranscriptDictationAuthority := newQualificationEvidenceStoreHarness(t, "dictation-wrong-transcript", QualificationEvidenceSeed{DictationBatches: []StoredDictationEvidenceBatch{wrongTranscriptBatch}}, time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC)).authority
	if _, err := EvaluateComposerDictationEvidenceCandidate(context.Background(), "tenant-acme", wrongTranscriptRef, wrongTranscriptCandidate, wrongTranscriptDictationAuthority); !errors.Is(err, ErrDictationQualificationUnverified) {
		t.Fatalf("mutated dictation transcript passed against stale fidelity candidate: %v", err)
	}

	crossRef, crossBatch, _, _, _ := localDictationCandidateFixture(t, observations, thresholds)
	meetingManifest, meetingAttempts, meetingTarget, meetingAuthority := liveTranscriptionQualificationFixture(t)
	meetingCandidate, err := EvaluateLiveTranscriptionCandidate(context.Background(), "tenant-acme", meetingManifest, meetingAttempts, meetingTarget, meetingAuthority)
	if err != nil {
		t.Fatal(err)
	}
	_, meetingEvidence, _ := preregisteredTranscriptionEvidence(t)
	for index := range meetingEvidence.Observations {
		meetingEvidence.Observations[index].Synthetic = false
	}
	crossRef.TranscriptionTargetDigest = meetingTarget.TargetDigest
	crossRef.TranscriptionCorpusDigest = meetingManifest.Digest
	crossRef.TranscriptionObservationSetDigest = meetingCandidate.Receipt.ObservationSetDigest
	crossRef.TranscriptionReceiptDigest = meetingCandidate.Receipt.ReceiptDigest
	crossRef.TranscriptionCandidateDigest = workDigest(meetingCandidate)
	crossBatch.Ref = crossRef
	crossBatch.TranscriptionManifest = meetingManifest
	crossBatch.TranscriptionObservations = meetingEvidence.Observations
	crossBatch.TranscriptionThresholds = meetingAuthority.targets[meetingTarget.Receipt].Thresholds
	crossDictationAuthority := newQualificationEvidenceStoreHarness(t, "dictation-cross-lane", QualificationEvidenceSeed{DictationBatches: []StoredDictationEvidenceBatch{crossBatch}}, time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC)).authority
	if _, err := EvaluateComposerDictationEvidenceCandidate(context.Background(), "tenant-acme", crossRef, meetingCandidate, crossDictationAuthority); !errors.Is(err, ErrDictationQualificationUnverified) {
		t.Fatalf("meeting-STT qualification substituted for dictation corpus: %v", err)
	}

	for name, mutate := range map[string]func(*TranscriptionQualificationCandidate, *DictationEvidenceBatchRef, *StoredDictationEvidenceBatch){
		"flipped local booleans": func(value *TranscriptionQualificationCandidate, ref *DictationEvidenceBatchRef, _ *StoredDictationEvidenceBatch) {
			value.Receipt = fidelity
			value.Receipt.Score.ProviderQualified = true
			value.Receipt.LiveOrDeviceQualified = true
			ref.TranscriptionReceiptDigest = value.Receipt.ReceiptDigest
			ref.TranscriptionCandidateDigest = workDigest(*value)
		},
		"forged provider state": func(value *TranscriptionQualificationCandidate, ref *DictationEvidenceBatchRef, _ *StoredDictationEvidenceBatch) {
			value.State = "provider_qualified"
			ref.TranscriptionCandidateDigest = workDigest(*value)
		},
		"mutated candidate receipt": func(value *TranscriptionQualificationCandidate, ref *DictationEvidenceBatchRef, _ *StoredDictationEvidenceBatch) {
			value.Receipt.P95FinalLatencyMS++
			ref.TranscriptionCandidateDigest = workDigest(*value)
		},
		"mutated provider attempt set": func(value *TranscriptionQualificationCandidate, ref *DictationEvidenceBatchRef, _ *StoredDictationEvidenceBatch) {
			value.Receipt.ProviderAttemptSetDigest = strings.Repeat("9", 64)
			ref.TranscriptionCandidateDigest = workDigest(*value)
		},
		"cross candidate": func(value *TranscriptionQualificationCandidate, ref *DictationEvidenceBatchRef, _ *StoredDictationEvidenceBatch) {
			value.Receipt.CandidateDigest = strings.Repeat("9", 64)
			ref.TranscriptionCandidateDigest = workDigest(*value)
		},
		"cross transcription corpus": func(value *TranscriptionQualificationCandidate, ref *DictationEvidenceBatchRef, _ *StoredDictationEvidenceBatch) {
			value.Receipt.Score.CorpusDigest = strings.Repeat("9", 64)
			ref.TranscriptionCandidateDigest = workDigest(*value)
		},
		"cross transcription target": func(value *TranscriptionQualificationCandidate, ref *DictationEvidenceBatchRef, _ *StoredDictationEvidenceBatch) {
			value.Receipt.TargetDigest = strings.Repeat("9", 64)
			ref.TranscriptionCandidateDigest = workDigest(*value)
		},
		"local fidelity": func(value *TranscriptionQualificationCandidate, ref *DictationEvidenceBatchRef, _ *StoredDictationEvidenceBatch) {
			value.Receipt = fidelity
			ref.TranscriptionReceiptDigest = value.Receipt.ReceiptDigest
			ref.TranscriptionCandidateDigest = workDigest(*value)
		},
		"mutated dictation observation set": func(_ *TranscriptionQualificationCandidate, _ *DictationEvidenceBatchRef, batch *StoredDictationEvidenceBatch) {
			batch.Observations[0].WaveformFPS++
		},
		"cross build": func(_ *TranscriptionQualificationCandidate, _ *DictationEvidenceBatchRef, batch *StoredDictationEvidenceBatch) {
			batch.Observations[0].BuildDigest = strings.Repeat("8", 64)
		},
		"mutated dictation thresholds": func(_ *TranscriptionQualificationCandidate, _ *DictationEvidenceBatchRef, batch *StoredDictationEvidenceBatch) {
			batch.Thresholds.MaximumP95PostLatencyMS--
		},
	} {
		t.Run(name, func(t *testing.T) {
			ref, batch, candidate, _, _ := localDictationCandidateFixture(t, observations, thresholds)
			mutate(&candidate, &ref, &batch)
			batch.Ref = ref
			dictationAuthority := newQualificationEvidenceStoreHarness(t, "dictation-negative", QualificationEvidenceSeed{DictationBatches: []StoredDictationEvidenceBatch{batch}}, time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC)).authority
			if _, err := EvaluateComposerDictationEvidenceCandidate(context.Background(), "tenant-acme", ref, candidate, dictationAuthority); !errors.Is(err, ErrDictationQualificationUnverified) {
				t.Fatalf("candidate authority escalation accepted: %v", err)
			}
		})
	}
}
