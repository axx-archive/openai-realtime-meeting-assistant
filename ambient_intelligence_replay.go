package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	ambientReplaySchema                 = "ambient-intelligence-replay/v1"
	ambientReplayMaxSources             = 48
	ambientReplayAuthorizationTTL       = 15 * time.Minute
	ambientReplayExecutionLease         = 5 * time.Minute
	ambientReplayDefaultMaxCalls        = 7
	ambientReplayDefaultMaxTokens int64 = 80_000
	ambientReplayDefaultMaxCost   int64 = 5_000_000
)

var (
	ErrAmbientReplayInvalid       = errors.New("ambient intelligence replay request is invalid")
	ErrAmbientReplayUnauthorized  = errors.New("ambient intelligence replay is not authorized")
	ErrAmbientReplayUnavailable   = errors.New("ambient intelligence replay dependency is unavailable")
	ErrAmbientReplayDrift         = errors.New("ambient intelligence replay authority or source drifted")
	ErrAmbientReplayCeiling       = errors.New("ambient intelligence replay ceiling exceeded")
	ErrAmbientReplayAlreadyActive = errors.New("ambient intelligence replay already active for sitting")
)

type AmbientReplayStageSpec struct {
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	PromptTokenCap int64  `json:"promptTokenCap"`
	OutputTokenCap int64  `json:"outputTokenCap"`
	CallCap        int    `json:"callCap"`
	CostMicrosCap  int64  `json:"costMicrosCap"`
	Deterministic  bool   `json:"deterministic,omitempty"`
}

type AmbientReplaySource struct {
	ObjectID           string    `json:"objectId"`
	CaptureSequence    uint64    `json:"captureSequence"`
	ContentRevision    int64     `json:"contentRevision"`
	ContentDigest      string    `json:"contentDigest"`
	ACLVersion         int64     `json:"aclVersion"`
	PurgeGeneration    int64     `json:"purgeGeneration"`
	OccurredStart      time.Time `json:"occurredStart"`
	OccurredEnd        time.Time `json:"occurredEnd"`
	ConsentFenceDigest string    `json:"consentFenceDigest"`
	RoomID             string    `json:"roomId"`
	SittingID          string    `json:"sittingId"`
}

type AmbientReplayManifest struct {
	Schema               string                   `json:"schema"`
	IdempotencyKey       string                   `json:"idempotencyKey"`
	TenantID             string                   `json:"tenantId"`
	RoomID               string                   `json:"roomId"`
	SittingID            string                   `json:"sittingId"`
	StartAfter           uint64                   `json:"startAfter"`
	EndAt                uint64                   `json:"endAt"`
	Sources              []AmbientReplaySource    `json:"sources"`
	ExcludedSources      []string                 `json:"excludedSources"`
	Stages               []AmbientReplayStageSpec `json:"stages"`
	CursorDigests        map[string]string        `json:"cursorDigests"`
	SourceManifestDigest string                   `json:"sourceManifestDigest"`
	PurgeGeneration      int64                    `json:"purgeGeneration"`
	MaxCalls             int                      `json:"maxCalls"`
	MaxTokens            int64                    `json:"maxTokens"`
	MaxCostMicros        int64                    `json:"maxCostMicros"`
	AuthorizedBy         string                   `json:"authorizedBy"`
	ApprovalReference    string                   `json:"approvalReference"`
	GeneratedAt          time.Time                `json:"generatedAt"`
	ExpiresAt            time.Time                `json:"expiresAt"`
	ReleaseCommit        string                   `json:"releaseCommit"`
	ReleaseTreeDigest    string                   `json:"releaseTreeDigest"`
	ReleaseReceiptDigest string                   `json:"releaseReceiptDigest"`
	RollbackFloor        string                   `json:"rollbackFloor"`
	Digest               string                   `json:"digest"`
}

type AmbientReplayPlanRequest struct {
	IdempotencyKey    string
	TenantID          string
	RoomID            string
	SittingID         string
	StartAfter        uint64
	EndAt             uint64
	StageNames        []string
	MaxCalls          int
	MaxTokens         int64
	MaxCostMicros     int64
	AuthorizedBy      string
	ApprovalReference string
	RollbackFloor     string
	ExpiresAt         time.Time
}

type AmbientReplayAuthoritySnapshot struct {
	Sources              []AmbientReplaySource
	ExcludedSources      []string
	CursorDigests        map[string]string
	PurgeGeneration      int64
	ApprovalReference    string
	RollbackFloor        string
	ReleaseCommit        string
	ReleaseTreeDigest    string
	ReleaseReceiptDigest string
}

type AmbientReplayAuthority interface {
	Plan(context.Context, AmbientReplayPlanRequest) (AmbientReplayAuthoritySnapshot, error)
	Revalidate(context.Context, AmbientReplayManifest) error
}

type AmbientReplayArtifact struct {
	ID                   string `json:"id"`
	Kind                 string `json:"kind"`
	Digest               string `json:"digest"`
	SourceManifestDigest string `json:"sourceManifestDigest"`
	ManifestDigest       string `json:"manifestDigest"`
}

type AmbientReplayUsage struct {
	Calls        int   `json:"calls"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	CostMicros   int64 `json:"costMicros"`
}

type AmbientReplayStageResult struct {
	Artifacts []AmbientReplayArtifact `json:"artifacts"`
	Usage     AmbientReplayUsage      `json:"usage"`
}

type AmbientReplayStageRunner interface {
	RunAmbientReplayStage(context.Context, AmbientReplayManifest, AmbientReplayStageSpec, []AmbientReplayArtifact) (AmbientReplayStageResult, error)
}

type AmbientReplayStageReceipt struct {
	ManifestDigest string             `json:"manifestDigest"`
	ExecutionID    string             `json:"executionId"`
	Stage          string             `json:"stage"`
	Ordinal        int                `json:"ordinal"`
	Status         string             `json:"status"`
	InputDigest    string             `json:"inputArtifactDigest"`
	OutputDigest   string             `json:"outputArtifactDigest,omitempty"`
	SourceDigest   string             `json:"sourceManifestDigest"`
	Usage          AmbientReplayUsage `json:"usage"`
	StartedAt      time.Time          `json:"startedAt"`
	CompletedAt    time.Time          `json:"completedAt,omitempty"`
	ErrorCode      string             `json:"errorCode,omitempty"`
}

type AmbientReplayExecution struct {
	ExecutionID string                      `json:"executionId"`
	Manifest    AmbientReplayManifest       `json:"manifest"`
	Status      string                      `json:"status"`
	Receipts    []AmbientReplayStageReceipt `json:"receipts"`
	Usage       AmbientReplayUsage          `json:"usage"`
}

type AmbientReplayStore interface {
	SaveManifest(context.Context, AmbientReplayManifest) (AmbientReplayManifest, bool, error)
	LoadManifest(context.Context, string) (AmbientReplayManifest, string, error)
	BeginExecution(context.Context, AmbientReplayManifest, string, time.Time) (string, bool, error)
	RenewExecutionLease(context.Context, string, string, time.Time, time.Time) error
	RecordStageReceipt(context.Context, AmbientReplayStageReceipt) error
	CompleteExecution(context.Context, string, string, string, time.Time) error
	ReclaimExpired(context.Context, time.Time) (int64, error)
}

type AmbientReplayEngine struct {
	Authority AmbientReplayAuthority
	Store     AmbientReplayStore
	Runner    AmbientReplayStageRunner
	Now       func() time.Time
	NewID     func() string
	mu        sync.Mutex
}

func ambientReplayDefaultStages() []AmbientReplayStageSpec {
	narrativeProvider, narrativeModel := providerOpenAI, meetingBrainModel()
	if currentAnthropicAPIKey() != "" {
		narrativeProvider, narrativeModel = providerAnthropic, chatModel()
	}
	return []AmbientReplayStageSpec{
		{Name: "brain", Provider: providerOpenAI, Model: meetingBrainModel(), PromptTokenCap: 32_000, OutputTokenCap: 2_400, CallCap: 1, CostMicrosCap: 1_000_000},
		{Name: "decision", Provider: providerOpenAI, Model: meetingBrainModel(), PromptTokenCap: 12_000, OutputTokenCap: 1_600, CallCap: 1, CostMicrosCap: 500_000},
		{Name: "mission", Provider: providerOpenAI, Model: meetingBrainModel(), PromptTokenCap: 12_000, OutputTokenCap: 1_600, CallCap: 1, CostMicrosCap: 500_000},
		{Name: "narrative", Provider: narrativeProvider, Model: narrativeModel, PromptTokenCap: 12_000, OutputTokenCap: 4_000, CallCap: 1, CostMicrosCap: 1_000_000},
		{Name: "meeting_digest", Provider: providerOpenAI, Model: meetingBrainModel(), PromptTokenCap: 12_000, OutputTokenCap: 2_000, CallCap: 1, CostMicrosCap: 750_000},
		{Name: "day_fold", Provider: "deterministic", Model: "meeting-digest-fold-v1", PromptTokenCap: 0, OutputTokenCap: 0, CallCap: 0, CostMicrosCap: 0, Deterministic: true},
		{Name: "entity_ledger", Provider: providerOpenAI, Model: meetingBrainModel(), PromptTokenCap: 8_000, OutputTokenCap: 1_600, CallCap: 1, CostMicrosCap: 500_000},
		{Name: "company_digest", Provider: providerOpenAI, Model: meetingBrainModel(), PromptTokenCap: 12_000, OutputTokenCap: 2_000, CallCap: 1, CostMicrosCap: 750_000},
	}
}

func (engine *AmbientReplayEngine) now() time.Time {
	if engine != nil && engine.Now != nil {
		return engine.Now().UTC()
	}
	return time.Now().UTC()
}

func (engine *AmbientReplayEngine) Plan(ctx context.Context, request AmbientReplayPlanRequest) (AmbientReplayManifest, error) {
	if engine == nil || engine.Authority == nil || engine.Store == nil {
		return AmbientReplayManifest{}, ErrAmbientReplayUnavailable
	}
	now := engine.now()
	if _, err := engine.Store.ReclaimExpired(ctx, now); err != nil {
		return AmbientReplayManifest{}, err
	}
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.TenantID, request.RoomID, request.SittingID = strings.TrimSpace(request.TenantID), normalizeRoomID(request.RoomID), strings.TrimSpace(request.SittingID)
	request.AuthorizedBy, request.ApprovalReference, request.RollbackFloor = normalizeAccountEmail(request.AuthorizedBy), strings.TrimSpace(request.ApprovalReference), strings.TrimSpace(request.RollbackFloor)
	if !isHexDigest(request.IdempotencyKey) || request.TenantID == "" || request.RoomID == "" || request.AuthorizedBy == "" ||
		!isHexDigest(request.ApprovalReference) || !isHexDigest(request.RollbackFloor) {
		return AmbientReplayManifest{}, ErrAmbientReplayInvalid
	}
	if request.ExpiresAt.IsZero() {
		request.ExpiresAt = now.Add(ambientReplayAuthorizationTTL)
	}
	request.ExpiresAt = request.ExpiresAt.UTC()
	if !request.ExpiresAt.After(now) || request.ExpiresAt.After(now.Add(ambientReplayAuthorizationTTL)) {
		return AmbientReplayManifest{}, ErrAmbientReplayUnauthorized
	}
	if request.MaxCalls == 0 {
		request.MaxCalls = ambientReplayDefaultMaxCalls
	}
	if request.MaxTokens == 0 {
		request.MaxTokens = ambientReplayDefaultMaxTokens
	}
	if request.MaxCostMicros == 0 {
		request.MaxCostMicros = ambientReplayDefaultMaxCost
	}
	if request.MaxCalls < 1 || request.MaxCalls > ambientReplayDefaultMaxCalls || request.MaxTokens < 1 || request.MaxTokens > ambientReplayDefaultMaxTokens || request.MaxCostMicros < 0 || request.MaxCostMicros > ambientReplayDefaultMaxCost {
		return AmbientReplayManifest{}, ErrAmbientReplayCeiling
	}
	stages, err := selectAmbientReplayStages(request.StageNames)
	if err != nil {
		return AmbientReplayManifest{}, err
	}
	authority, err := engine.Authority.Plan(ctx, request)
	if err != nil {
		return AmbientReplayManifest{}, err
	}
	if len(authority.Sources) == 0 || len(authority.Sources) > ambientReplayMaxSources || authority.PurgeGeneration < 0 || authority.ReleaseCommit == "" {
		return AmbientReplayManifest{}, ErrAmbientReplayInvalid
	}
	if !isHexDigest(authority.ApprovalReference) || !isHexDigest(authority.RollbackFloor) ||
		authority.ApprovalReference != request.ApprovalReference || authority.RollbackFloor != request.RollbackFloor {
		return AmbientReplayManifest{}, ErrAmbientReplayUnauthorized
	}
	sources := append([]AmbientReplaySource(nil), authority.Sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].CaptureSequence < sources[j].CaptureSequence })
	sitting := sources[0].SittingID
	room := normalizeRoomID(sources[0].RoomID)
	for i, source := range sources {
		if source.SittingID == "" || source.SittingID != sitting || normalizeRoomID(source.RoomID) != room || source.CaptureSequence == 0 || !isHexDigest(source.ContentDigest) || !isHexDigest(source.ConsentFenceDigest) || source.ContentRevision < 1 || source.ACLVersion < 1 || source.PurgeGeneration != authority.PurgeGeneration || source.OccurredStart.IsZero() || !source.OccurredStart.Before(source.OccurredEnd) || (i > 0 && sources[i-1].CaptureSequence >= source.CaptureSequence) {
			return AmbientReplayManifest{}, ErrAmbientReplayInvalid
		}
	}
	if request.SittingID != "" && request.SittingID != sitting || room != request.RoomID {
		return AmbientReplayManifest{}, ErrAmbientReplayDrift
	}
	startAfter := request.StartAfter
	if startAfter == 0 && sources[0].CaptureSequence > 0 {
		startAfter = sources[0].CaptureSequence - 1
	}
	// EndAt is always the exact selected tail. A caller may use request.EndAt
	// to narrow source selection, but an authorization range beyond the 48-row
	// slice may never advance the replay cursor past sources absent from the
	// digest-bound manifest.
	endAt := sources[len(sources)-1].CaptureSequence
	if sources[0].CaptureSequence <= startAfter || endAt <= startAfter {
		return AmbientReplayManifest{}, ErrAmbientReplayInvalid
	}
	sourceDigest, err := digestAmbientReplayValue(sources)
	if err != nil {
		return AmbientReplayManifest{}, err
	}
	manifest := AmbientReplayManifest{Schema: ambientReplaySchema, IdempotencyKey: request.IdempotencyKey, TenantID: request.TenantID, RoomID: room, SittingID: sitting,
		StartAfter: startAfter, EndAt: endAt, Sources: sources, ExcludedSources: uniqueSortedStrings(authority.ExcludedSources), Stages: stages,
		CursorDigests: cloneStringMap(authority.CursorDigests), SourceManifestDigest: sourceDigest, PurgeGeneration: authority.PurgeGeneration,
		MaxCalls: request.MaxCalls, MaxTokens: request.MaxTokens, MaxCostMicros: request.MaxCostMicros, AuthorizedBy: request.AuthorizedBy,
		ApprovalReference: authority.ApprovalReference, GeneratedAt: now, ExpiresAt: request.ExpiresAt, ReleaseCommit: authority.ReleaseCommit,
		ReleaseTreeDigest: authority.ReleaseTreeDigest, ReleaseReceiptDigest: authority.ReleaseReceiptDigest, RollbackFloor: authority.RollbackFloor}
	manifest.Digest, err = ambientReplayManifestDigest(manifest)
	if err != nil {
		return AmbientReplayManifest{}, err
	}
	stored, _, err := engine.Store.SaveManifest(ctx, manifest)
	return stored, err
}

func selectAmbientReplayStages(names []string) ([]AmbientReplayStageSpec, error) {
	defaults := ambientReplayDefaultStages()
	if len(names) == 0 {
		return defaults, nil
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || name == "board" {
			return nil, ErrAmbientReplayInvalid
		}
		wanted[name] = true
	}
	result := make([]AmbientReplayStageSpec, 0, len(wanted))
	for _, stage := range defaults {
		if wanted[stage.Name] {
			result = append(result, stage)
			delete(wanted, stage.Name)
		}
	}
	if len(wanted) != 0 || len(result) == 0 || result[0].Name != "brain" {
		return nil, ErrAmbientReplayInvalid
	}
	return result, nil
}

func ambientReplayManifestDigest(manifest AmbientReplayManifest) (string, error) {
	manifest.Digest = ""
	return digestAmbientReplayValue(manifest)
}

func ambientReplayManifestEquivalentForRetry(left, right AmbientReplayManifest) bool {
	left.Digest, right.Digest = "", ""
	left.GeneratedAt, right.GeneratedAt = time.Time{}, time.Time{}
	left.ExpiresAt, right.ExpiresAt = time.Time{}, time.Time{}
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}

func digestAmbientReplayValue(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func digestAmbientReplayArtifacts(artifacts []AmbientReplayArtifact) (string, error) {
	copy := append([]AmbientReplayArtifact(nil), artifacts...)
	sort.Slice(copy, func(i, j int) bool {
		if copy[i].Kind != copy[j].Kind {
			return copy[i].Kind < copy[j].Kind
		}
		return copy[i].ID < copy[j].ID
	})
	return digestAmbientReplayValue(copy)
}

func (engine *AmbientReplayEngine) Execute(ctx context.Context, digest, actor string) (AmbientReplayExecution, error) {
	if engine == nil || engine.Authority == nil || engine.Store == nil || engine.Runner == nil {
		return AmbientReplayExecution{}, ErrAmbientReplayUnavailable
	}
	digest, actor = strings.TrimSpace(digest), normalizeAccountEmail(actor)
	if !isHexDigest(digest) || actor == "" {
		return AmbientReplayExecution{}, ErrAmbientReplayInvalid
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if _, err := engine.Store.ReclaimExpired(ctx, engine.now()); err != nil {
		return AmbientReplayExecution{}, err
	}
	manifest, status, err := engine.Store.LoadManifest(ctx, digest)
	if err != nil {
		return AmbientReplayExecution{}, err
	}
	calculated, err := ambientReplayManifestDigest(manifest)
	if err != nil || calculated != digest || manifest.Digest != digest {
		return AmbientReplayExecution{}, ErrAmbientReplayDrift
	}
	now := engine.now()
	if actor != manifest.AuthorizedBy {
		return AmbientReplayExecution{}, ErrAmbientReplayUnauthorized
	}
	if status == "completed" {
		return AmbientReplayExecution{Manifest: manifest, Status: status}, nil
	}
	if status == "running" {
		return AmbientReplayExecution{Manifest: manifest, Status: status}, nil
	}
	if status != "planned" || !now.Before(manifest.ExpiresAt) {
		return AmbientReplayExecution{}, ErrAmbientReplayUnauthorized
	}
	if err := engine.Authority.Revalidate(ctx, manifest); err != nil {
		return AmbientReplayExecution{}, fmt.Errorf("%w: %v", ErrAmbientReplayDrift, err)
	}
	executionID := ""
	if engine.NewID != nil {
		executionID = engine.NewID()
	}
	if executionID == "" {
		executionID = uuid.NewString()
	}
	executionID, existing, err := engine.Store.BeginExecution(ctx, manifest, executionID, now)
	if err != nil {
		return AmbientReplayExecution{}, err
	}
	if existing {
		return AmbientReplayExecution{ExecutionID: executionID, Manifest: manifest, Status: status}, nil
	}
	execution := AmbientReplayExecution{ExecutionID: executionID, Manifest: manifest, Status: "running"}
	input := make([]AmbientReplayArtifact, 0, len(manifest.Sources))
	for _, source := range manifest.Sources {
		input = append(input, AmbientReplayArtifact{ID: source.ObjectID, Kind: "transcript", Digest: source.ContentDigest, SourceManifestDigest: manifest.SourceManifestDigest, ManifestDigest: manifest.Digest})
	}
	for ordinal, stage := range manifest.Stages {
		if stage.Name == "board" {
			return execution, engine.fail(ctx, &execution, stage.Name, ErrAmbientReplayInvalid)
		}
		inputDigest, _ := digestAmbientReplayArtifacts(input)
		if err := engine.Store.RenewExecutionLease(ctx, manifest.Digest, executionID, engine.now(), engine.now().Add(ambientReplayExecutionLease)); err != nil {
			return execution, engine.failStage(ctx, &execution, AmbientReplayStageReceipt{ManifestDigest: digest, ExecutionID: executionID,
				Stage: stage.Name, Ordinal: ordinal, Status: "prepared", InputDigest: inputDigest, SourceDigest: manifest.SourceManifestDigest,
				StartedAt: engine.now()}, err)
		}
		if err := engine.Authority.Revalidate(ctx, manifest); err != nil {
			return execution, engine.failStage(ctx, &execution, AmbientReplayStageReceipt{ManifestDigest: digest, ExecutionID: executionID,
				Stage: stage.Name, Ordinal: ordinal, Status: "prepared", InputDigest: inputDigest, SourceDigest: manifest.SourceManifestDigest,
				StartedAt: engine.now()}, fmt.Errorf("%w: %v", ErrAmbientReplayDrift, err))
		}
		started := engine.now()
		prepared := AmbientReplayStageReceipt{ManifestDigest: digest, ExecutionID: executionID, Stage: stage.Name, Ordinal: ordinal, Status: "prepared", InputDigest: inputDigest, SourceDigest: manifest.SourceManifestDigest, StartedAt: started}
		if err := engine.Store.RecordStageReceipt(ctx, prepared); err != nil {
			return execution, err
		}
		result, runErr := engine.Runner.RunAmbientReplayStage(ctx, manifest, stage, append([]AmbientReplayArtifact(nil), input...))
		if runErr != nil {
			return execution, engine.failStage(ctx, &execution, prepared, runErr)
		}
		for _, artifact := range result.Artifacts {
			if artifact.ID == "" || artifact.Kind == "" || !isHexDigest(artifact.Digest) || artifact.SourceManifestDigest != manifest.SourceManifestDigest || artifact.ManifestDigest != manifest.Digest {
				return execution, engine.failStage(ctx, &execution, prepared, ErrAmbientReplayDrift)
			}
		}
		if result.Usage.Calls < 0 || result.Usage.InputTokens < 0 || result.Usage.OutputTokens < 0 || result.Usage.CostMicros < 0 ||
			result.Usage.Calls > stage.CallCap || result.Usage.InputTokens > stage.PromptTokenCap || result.Usage.OutputTokens > stage.OutputTokenCap || result.Usage.CostMicros > stage.CostMicrosCap {
			return execution, engine.failStage(ctx, &execution, prepared, ErrAmbientReplayCeiling)
		}
		execution.Usage.Calls += result.Usage.Calls
		execution.Usage.InputTokens += result.Usage.InputTokens
		execution.Usage.OutputTokens += result.Usage.OutputTokens
		execution.Usage.CostMicros += result.Usage.CostMicros
		if execution.Usage.Calls > manifest.MaxCalls || execution.Usage.InputTokens+execution.Usage.OutputTokens > manifest.MaxTokens || execution.Usage.CostMicros > manifest.MaxCostMicros {
			return execution, engine.failStage(ctx, &execution, prepared, ErrAmbientReplayCeiling)
		}
		outputDigest, _ := digestAmbientReplayArtifacts(result.Artifacts)
		completed := prepared
		completed.Status = "completed"
		completed.OutputDigest = outputDigest
		completed.Usage = result.Usage
		completed.CompletedAt = engine.now()
		if err := engine.Store.RecordStageReceipt(ctx, completed); err != nil {
			return execution, err
		}
		execution.Receipts = append(execution.Receipts, completed)
		input = append([]AmbientReplayArtifact(nil), result.Artifacts...)
	}
	if err := engine.Store.CompleteExecution(ctx, manifest.Digest, executionID, "completed", engine.now()); err != nil {
		return execution, err
	}
	execution.Status = "completed"
	return execution, nil
}

func (engine *AmbientReplayEngine) failStage(ctx context.Context, execution *AmbientReplayExecution, receipt AmbientReplayStageReceipt, cause error) error {
	receipt.Status = "failed"
	if errors.Is(cause, ErrAmbientReplayDrift) {
		receipt.Status = "drifted"
	}
	receipt.ErrorCode = ambientReplayErrorCode(cause)
	receipt.CompletedAt = engine.now()
	_ = engine.Store.RecordStageReceipt(ctx, receipt)
	execution.Receipts = append(execution.Receipts, receipt)
	_ = engine.Store.CompleteExecution(ctx, execution.Manifest.Digest, execution.ExecutionID, receipt.Status, engine.now())
	execution.Status = receipt.Status
	return cause
}

func (engine *AmbientReplayEngine) fail(ctx context.Context, execution *AmbientReplayExecution, stage string, cause error) error {
	receipt := AmbientReplayStageReceipt{ManifestDigest: execution.Manifest.Digest, ExecutionID: execution.ExecutionID, Stage: stage, Status: "failed", SourceDigest: execution.Manifest.SourceManifestDigest, StartedAt: engine.now()}
	return engine.failStage(ctx, execution, receipt, cause)
}

func ambientReplayErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrAmbientReplayDrift):
		return "authority_drift"
	case errors.Is(err, ErrAmbientReplayCeiling):
		return "ceiling_exceeded"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	default:
		return "stage_failed"
	}
}
