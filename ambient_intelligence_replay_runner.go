package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// ambientReplayUsageCapture is an optional usage side-channel for the
// Responses seam. The replay runner still works with injected test responders
// and Anthropic's legacy text seam, but production OpenAI calls report the
// provider's actual token usage when the HTTP responder reaches the wire.
type ambientReplayUsageCapture struct {
	mu           sync.Mutex
	inputTokens  int64
	cachedTokens int64
	outputTokens int64
	accepted     bool
	observed     bool
}

type ambientReplayUsageCaptureContextKey struct{}

func withAmbientReplayUsageCapture(ctx context.Context, capture *ambientReplayUsageCapture) context.Context {
	if capture == nil {
		return ctx
	}
	return context.WithValue(ctx, ambientReplayUsageCaptureContextKey{}, capture)
}

func captureAmbientReplayUsage(ctx context.Context, usage *openAIResponsesUsage, accepted bool) {
	if ctx == nil || usage == nil {
		return
	}
	capture, _ := ctx.Value(ambientReplayUsageCaptureContextKey{}).(*ambientReplayUsageCapture)
	if capture == nil {
		return
	}
	inputTokens := usage.InputTokens - usage.InputTokensDetails.CachedTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	capture.mu.Lock()
	capture.inputTokens = inputTokens
	capture.cachedTokens = max(usage.InputTokensDetails.CachedTokens, 0)
	capture.outputTokens = max(usage.OutputTokens, 0)
	capture.accepted = accepted
	capture.observed = true
	capture.mu.Unlock()
}

func (capture *ambientReplayUsageCapture) snapshot() (AmbientReplayUsage, bool) {
	if capture == nil {
		return AmbientReplayUsage{}, false
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if !capture.observed || !capture.accepted {
		return AmbientReplayUsage{}, false
	}
	return AmbientReplayUsage{
		Calls:        1,
		InputTokens:  capture.inputTokens + capture.cachedTokens,
		OutputTokens: capture.outputTokens,
	}, true
}

// productionAmbientReplayStageRunner is deliberately a pure stage adapter.
// It reads the already-authorized source slice and emits digest-bound
// execution artifacts, but it never calls appendEntry, mutates the Kanban
// board, advances a live worker cursor, broadcasts, or writes a production
// memory file. Promotion into canonical projections remains a separate,
// authority-bound operation.
type productionAmbientReplayStageRunner struct {
	app       *kanbanBoardApp
	responder openAITextResponder
	now       func() time.Time
}

func newProductionAmbientReplayStageRunner(app *kanbanBoardApp) *productionAmbientReplayStageRunner {
	if app == nil || app.memory == nil {
		return nil
	}
	return &productionAmbientReplayStageRunner{
		app:       app,
		responder: createOpenAITextResponse,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func (runner *productionAmbientReplayStageRunner) RunAmbientReplayStage(ctx context.Context, manifest AmbientReplayManifest, stage AmbientReplayStageSpec, input []AmbientReplayArtifact) (AmbientReplayStageResult, error) {
	if runner == nil || runner.app == nil || runner.app.memory == nil {
		return AmbientReplayStageResult{}, ErrAmbientReplayUnavailable
	}
	if manifest.Schema != ambientReplaySchema || !isHexDigest(manifest.Digest) || !isHexDigest(manifest.SourceManifestDigest) {
		return AmbientReplayStageResult{}, ErrAmbientReplayInvalid
	}
	if !ambientReplayStageInManifest(manifest, stage) {
		return AmbientReplayStageResult{}, ErrAmbientReplayInvalid
	}
	if err := validateAmbientReplayStageInput(manifest, input); err != nil {
		return AmbientReplayStageResult{}, err
	}

	sources, err := runner.authorizedReplaySources(manifest)
	if err != nil {
		return AmbientReplayStageResult{}, err
	}
	if stage.Deterministic {
		return runner.runDeterministicReplayStage(manifest, stage, input, sources)
	}

	request := ambientReplayStageRequest(manifest, stage, sources, input, runner.currentTime())
	text, usage, err := runner.respond(ctx, request, stage)
	if err != nil {
		return AmbientReplayStageResult{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return AmbientReplayStageResult{}, &openAIOutputRejection{reason: "empty_output"}
	}
	if validator := ambientReplayStageOutputValidator(stage.Name); validator != nil {
		if err := validator(text); err != nil {
			return AmbientReplayStageResult{}, &openAIOutputRejection{reason: "output_validation_error: " + err.Error()}
		}
	}

	artifactDigest, err := digestAmbientReplayValue(struct {
		ManifestDigest       string
		SourceManifestDigest string
		Stage                AmbientReplayStageSpec
		Input                []AmbientReplayArtifact
		Text                 string
	}{manifest.Digest, manifest.SourceManifestDigest, stage, input, text})
	if err != nil {
		return AmbientReplayStageResult{}, err
	}
	artifact := AmbientReplayArtifact{
		ID:                   fmt.Sprintf("ambient-replay:%s:%s", manifest.Digest[:16], stage.Name),
		Kind:                 stage.Name,
		Digest:               artifactDigest,
		SourceManifestDigest: manifest.SourceManifestDigest,
		ManifestDigest:       manifest.Digest,
		Text:                 text,
	}
	return AmbientReplayStageResult{Artifacts: []AmbientReplayArtifact{artifact}, Usage: usage}, nil
}

func (runner *productionAmbientReplayStageRunner) currentTime() time.Time {
	if runner != nil && runner.now != nil {
		return runner.now().UTC()
	}
	return time.Now().UTC()
}

func ambientReplayStageInManifest(manifest AmbientReplayManifest, wanted AmbientReplayStageSpec) bool {
	for _, stage := range manifest.Stages {
		if stage.Name == wanted.Name && stage.Provider == wanted.Provider && stage.Model == wanted.Model &&
			stage.PromptTokenCap == wanted.PromptTokenCap && stage.OutputTokenCap == wanted.OutputTokenCap &&
			stage.CallCap == wanted.CallCap && stage.CostMicrosCap == wanted.CostMicrosCap && stage.Deterministic == wanted.Deterministic {
			return true
		}
	}
	return false
}

func validateAmbientReplayStageInput(manifest AmbientReplayManifest, input []AmbientReplayArtifact) error {
	if len(input) == 0 {
		return ErrAmbientReplayInvalid
	}
	for _, artifact := range input {
		if strings.TrimSpace(artifact.ID) == "" || !isHexDigest(artifact.Digest) ||
			artifact.SourceManifestDigest != manifest.SourceManifestDigest || artifact.ManifestDigest != manifest.Digest {
			return ErrAmbientReplayDrift
		}
	}
	return nil
}

func (runner *productionAmbientReplayStageRunner) authorizedReplaySources(manifest AmbientReplayManifest) ([]meetingMemoryEntry, error) {
	sources := make([]meetingMemoryEntry, 0, len(manifest.Sources))
	for _, source := range manifest.Sources {
		entry, found := runner.app.memory.entryByID(source.ObjectID)
		if !found || entry.Kind != meetingMemoryKindTranscript ||
			digestBrainString(entry.Text) != source.ContentDigest ||
			normalizeRoomID(entry.Metadata["roomId"]) != manifest.RoomID ||
			strings.TrimSpace(entry.Metadata["meetingId"]) != manifest.SittingID {
			return nil, ErrAmbientReplayDrift
		}
		sources = append(sources, entry)
	}
	if len(sources) == 0 {
		return nil, ErrAmbientReplayInvalid
	}
	return sources, nil
}

func ambientReplayStageRequest(manifest AmbientReplayManifest, stage AmbientReplayStageSpec, sources []meetingMemoryEntry, input []AmbientReplayArtifact, generatedAt time.Time) openAITextRequest {
	request := openAITextRequest{
		Model:           stage.Model,
		Instructions:    ambientReplayStageInstructions(stage.Name),
		Input:           ambientReplayStageInput(manifest, stage, sources, input),
		ReasoningEffort: meetingBrainReasoningEffort(),
		Verbosity:       "low",
		MaxOutputTokens: int(stage.OutputTokenCap),
		Seat:            ambientReplayStageSeat(stage.Name),
		Workflow:        "ambient_intelligence_replay",
	}
	if stage.Name == "brain" {
		request.Input = buildMeetingBrainInput(sources, kanbanBoardState{}, nil, generatedAt)
	}
	return request
}

func ambientReplayStageInput(manifest AmbientReplayManifest, stage AmbientReplayStageSpec, sources []meetingMemoryEntry, input []AmbientReplayArtifact) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Ambient replay stage=%s\n", stage.Name)
	fmt.Fprintf(&builder, "manifest=%s\nsourceManifest=%s\nroom=%s\nsitting=%s\n", manifest.Digest, manifest.SourceManifestDigest, manifest.RoomID, manifest.SittingID)
	builder.WriteString("\n# Authorized source references\n")
	captureSequences := make(map[string]uint64, len(manifest.Sources))
	for _, source := range manifest.Sources {
		captureSequences[source.ObjectID] = source.CaptureSequence
	}
	for _, source := range sources {
		fmt.Fprintf(&builder, "- %s | capture=%d | digest=%s", source.ID, captureSequences[source.ID], digestBrainString(source.Text))
		if stage.Name == "brain" {
			builder.WriteString(" | ")
			builder.WriteString(source.Text)
		}
		builder.WriteByte('\n')
	}
	if len(input) > 0 {
		builder.WriteString("\n# Prior replay artifacts\n")
		for _, artifact := range input {
			fmt.Fprintf(&builder, "- %s | kind=%s | digest=%s\n", artifact.ID, artifact.Kind, artifact.Digest)
			if text := strings.TrimSpace(artifact.Text); text != "" {
				builder.WriteString(text)
				builder.WriteString("\n")
			}
		}
	}
	return builder.String()
}

func ambientReplayStageInstructions(name string) string {
	switch name {
	case "brain":
		return meetingBrainInstructions()
	case "decision":
		return decisionLedgerInstructions()
	case "mission":
		return missionIntelInstructions()
	case "narrative":
		return narrativeMaintainerInstructions()
	case "meeting_digest":
		return meetingDigestInstructions()
	case "entity_ledger":
		return ledgerAdjudicationInstructions()
	case "company_digest":
		return companyDigestInstructions()
	default:
		return "Produce only a concise, source-grounded replay artifact. Do not invent facts, identities, dates, authority, or actions."
	}
}

func ambientReplayStageSeat(name string) string {
	switch name {
	case "brain":
		return seatBrain
	case "decision":
		return seatDecisionLedger
	case "mission":
		return seatMissionIntel
	case "narrative":
		return seatNarrative
	case "meeting_digest":
		return seatMeetingDigest
	case "entity_ledger":
		return seatEntityLedger
	case "company_digest":
		return seatCompanyDigest
	default:
		return seatUntagged
	}
}

func ambientReplayStageOutputValidator(name string) func(string) error {
	switch name {
	case "decision":
		return func(text string) error {
			if _, ok := parseDecisionLedgerOutput(text); !ok {
				return errors.New("decision ledger output is not valid JSON")
			}
			return nil
		}
	case "mission":
		return func(text string) error {
			if _, _, ok := parseMissionInsight(text); !ok {
				return errors.New("mission insight output is not valid JSON")
			}
			return nil
		}
	case "narrative":
		return func(text string) error {
			if _, ok := parseNarrativeUpdates(text); !ok {
				return errors.New("narrative output is not valid JSON")
			}
			return nil
		}
	case "meeting_digest":
		return func(text string) error {
			if _, ok := parseMeetingDigest(text); !ok {
				return errors.New("meeting digest output is not valid JSON")
			}
			return nil
		}
	case "entity_ledger":
		return func(text string) error {
			if _, ok := parseLedgerAdjudication(text); !ok {
				return errors.New("entity ledger output is not valid JSON")
			}
			return nil
		}
	case "company_digest":
		return func(text string) error {
			if _, ok := parseCompanyDigest(text); !ok {
				return errors.New("company digest output is not valid JSON")
			}
			return nil
		}
	default:
		return nil
	}
}

func (runner *productionAmbientReplayStageRunner) respond(ctx context.Context, request openAITextRequest, stage AmbientReplayStageSpec) (string, AmbientReplayUsage, error) {
	if runner == nil {
		return "", AmbientReplayUsage{}, ErrAmbientReplayUnavailable
	}
	if stage.Provider == "deterministic" {
		return "", AmbientReplayUsage{}, ErrAmbientReplayInvalid
	}
	apiKey := ""
	runner.app.mu.Lock()
	apiKey = runner.app.apiKey
	runner.app.mu.Unlock()
	if stage.Provider == providerAnthropic {
		text, err := createAnthropicTextResponse(ctx, currentAnthropicAPIKey(), anthropicTextRequest{
			Model: stage.Model, Instructions: request.Instructions, Input: request.Input,
			Effort: request.ReasoningEffort, MaxTokens: request.MaxOutputTokens, Seat: request.Seat,
		})
		if err != nil {
			return "", AmbientReplayUsage{}, err
		}
		return text, ambientReplayEstimatedUsage(request, text), nil
	}
	if runner.responder == nil {
		runner.responder = createOpenAITextResponse
	}
	capture := &ambientReplayUsageCapture{}
	text, err := runner.responder(withAmbientReplayUsageCapture(ctx, capture), apiKey, request)
	if err != nil {
		return "", AmbientReplayUsage{}, err
	}
	if usage, ok := capture.snapshot(); ok {
		usage.CostMicros = ambientReplayCostMicros(request.Model, usage.InputTokens, usage.OutputTokens, false)
		return text, usage, nil
	}
	return text, ambientReplayEstimatedUsage(request, text), nil
}

func ambientReplayEstimatedUsage(request openAITextRequest, output string) AmbientReplayUsage {
	inputTokens := estimateAmbientReplayTokens(request.Input)
	outputTokens := estimateAmbientReplayTokens(output)
	return AmbientReplayUsage{
		Calls:        1,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CostMicros:   ambientReplayCostMicros(request.Model, inputTokens, outputTokens, true),
		Estimated:    true,
	}
}

func estimateAmbientReplayTokens(text string) int64 {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return max(int64(1), int64(math.Ceil(float64(len([]byte(text)))/4)))
}

func ambientReplayCostMicros(model string, inputTokens, outputTokens int64, estimated bool) int64 {
	cost, priced := estimateCostUSDAt(model, time.Now().UTC(), llmTokenUsage{InputTokens: inputTokens, OutputTokens: outputTokens})
	if !priced || cost <= 0 {
		return 0
	}
	return int64(math.Ceil(cost * 1_000_000))
}

func (runner *productionAmbientReplayStageRunner) runDeterministicReplayStage(manifest AmbientReplayManifest, stage AmbientReplayStageSpec, input []AmbientReplayArtifact, sources []meetingMemoryEntry) (AmbientReplayStageResult, error) {
	digest, err := digestAmbientReplayValue(struct {
		Manifest string
		Stage    string
		Sources  []meetingMemoryEntry
		Input    []AmbientReplayArtifact
	}{manifest.SourceManifestDigest, stage.Name, sources, input})
	if err != nil {
		return AmbientReplayStageResult{}, err
	}
	return AmbientReplayStageResult{Artifacts: []AmbientReplayArtifact{{
		ID: fmt.Sprintf("ambient-replay:%s:%s", manifest.Digest[:16], stage.Name), Kind: stage.Name,
		Digest: digest, SourceManifestDigest: manifest.SourceManifestDigest, ManifestDigest: manifest.Digest,
	}}, Usage: AmbientReplayUsage{}}, nil
}
