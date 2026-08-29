package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	strideLeadShadowAPIKeyEnvironment       = "BONFIRE_STRIDE_LEAD_HARNESS_API_KEY"
	strideLeadShadowProjectEnvironment      = "BONFIRE_STRIDE_LEAD_HARNESS_PROJECT_ID"
	strideLeadShadowSpendCeilingEnvironment = "BONFIRE_STRIDE_LEAD_HARNESS_MAX_SPEND_CENTS"
	strideLeadShadowCandidateDirEnvironment = "BONFIRE_STRIDE_LEAD_HARNESS_CANDIDATE_DIR"
	strideLeadShadowPollInterval            = 5 * time.Second
	strideLeadShadowMaxPolls                = 120
)

var (
	ErrSTRIDELeadPromotionRequired = errors.New("STRIDE lead candidate publication requires benchmark promotion")
	ErrSTRIDELeadCandidateInvalid  = errors.New("STRIDE lead candidate artifact is invalid")
	ErrSTRIDELeadValidationPending = errors.New("STRIDE lead candidate native validation is pending")
	ErrSTRIDELeadShadowDeferred    = errors.New("STRIDE lead shadow deferred for live meeting work")
)

type STRIDELeadCandidateArtifactRequest struct {
	Run        STRIDECanonicalWorkRun
	Assignment STRIDECanonicalAgentAssignment
	Provider   STRIDELeadProviderReceipt
	Body       string
	ObservedAt time.Time
}

func (request STRIDELeadCandidateArtifactRequest) Validate() error {
	if request.Run.Validate() != nil || request.Assignment.Validate() != nil || request.Provider.Validate() != nil ||
		request.Assignment.RunID != request.Run.ID || request.Provider.RunID != request.Run.ID ||
		request.Provider.Status != "completed" || request.Provider.AssignmentID == request.Assignment.ID ||
		!oneOf(request.Assignment.Agent, STRIDEWorkAgentResearcher, STRIDEWorkAgentPresenter) ||
		strings.TrimSpace(request.Body) == "" || request.ObservedAt.IsZero() || request.ObservedAt.Location() != time.UTC {
		return ErrSTRIDELeadCandidateInvalid
	}
	return nil
}

type STRIDELeadCandidateArtifactReceipt struct {
	RunID                   string          `json:"runId"`
	AssignmentID            string          `json:"assignmentId"`
	ProviderResponseID      string          `json:"providerResponseId"`
	OutputKind              string          `json:"outputKind"`
	ArtifactType            string          `json:"artifactType"`
	Artifact                STRIDEReference `json:"artifact"`
	StorageRef              string          `json:"storageRef"`
	BodyDigest              string          `json:"bodyDigest"`
	NativeRenderValidated   bool            `json:"nativeRenderValidated"`
	OpenValidated           bool            `json:"openValidated"`
	EditabilityValidated    bool            `json:"editabilityValidated"`
	ValidationReceiptDigest string          `json:"validationReceiptDigest"`
	ValidatedAt             time.Time       `json:"validatedAt"`
}

func (receipt STRIDELeadCandidateArtifactReceipt) Validate() error {
	if !strideIdentifier(receipt.RunID) || !strideIdentifier(receipt.AssignmentID) || !strideIdentifier(receipt.ProviderResponseID) ||
		!oneOf(receipt.OutputKind, "research", "presentation") || !oneOf(receipt.ArtifactType, artifactTypeMarkdown, artifactTypeHTMLDeck) ||
		receipt.Artifact.Validate() != nil || receipt.Artifact.ContractType != STRIDEContractOutcome ||
		!strideIdentifier(receipt.StorageRef) || !allDigests(receipt.BodyDigest, receipt.ValidationReceiptDigest) || receipt.Artifact.Digest != receipt.BodyDigest ||
		!receipt.NativeRenderValidated || !receipt.OpenValidated || !receipt.EditabilityValidated || receipt.ValidatedAt.IsZero() || receipt.ValidatedAt.Location() != time.UTC {
		return ErrSTRIDELeadCandidateInvalid
	}
	return nil
}

type STRIDELeadBenchmarkPromotion struct {
	BenchmarkDigest string          `json:"benchmarkDigest"`
	Approval        STRIDEReference `json:"approval"`
	ApprovedBy      string          `json:"approvedBy"`
	ApprovedAt      time.Time       `json:"approvedAt"`
}

func (promotion STRIDELeadBenchmarkPromotion) Validate() error {
	if !isHexDigest(promotion.BenchmarkDigest) || promotion.Approval.Validate() != nil ||
		!strideIdentifier(promotion.ApprovedBy) || promotion.ApprovedAt.IsZero() || promotion.ApprovedAt.Location() != time.UTC {
		return ErrSTRIDELeadPromotionRequired
	}
	return nil
}

type STRIDELeadDriveCommitReceipt struct {
	Artifact    STRIDEReference `json:"artifact"`
	DriveFile   STRIDEReference `json:"driveFile"`
	CommittedAt time.Time       `json:"committedAt"`
}

func (receipt STRIDELeadDriveCommitReceipt) Validate() error {
	if receipt.Artifact.Validate() != nil || receipt.DriveFile.Validate() != nil || receipt.CommittedAt.IsZero() || receipt.CommittedAt.Location() != time.UTC {
		return ErrSTRIDELeadCandidateInvalid
	}
	return nil
}

type STRIDELeadChannelDeliveryReceipt struct {
	Artifact    STRIDEReference `json:"artifact"`
	ChannelID   string          `json:"channelId"`
	MessageID   string          `json:"messageId"`
	DeliveredAt time.Time       `json:"deliveredAt"`
}

func (receipt STRIDELeadChannelDeliveryReceipt) Validate() error {
	if receipt.Artifact.Validate() != nil || !strideIdentifier(receipt.ChannelID) || !strideIdentifier(receipt.MessageID) ||
		receipt.DeliveredAt.IsZero() || receipt.DeliveredAt.Location() != time.UTC {
		return ErrSTRIDELeadCandidateInvalid
	}
	return nil
}

type STRIDELeadArtifactAdapter interface {
	StageCandidate(context.Context, STRIDELeadCandidateArtifactRequest) (STRIDELeadCandidateArtifactReceipt, error)
	CommitCandidateToDrive(context.Context, STRIDELeadCandidateArtifactReceipt, STRIDELeadBenchmarkPromotion) (STRIDELeadDriveCommitReceipt, error)
	DeliverCandidateToChannel(context.Context, STRIDELeadDriveCommitReceipt, string, STRIDELeadBenchmarkPromotion) (STRIDELeadChannelDeliveryReceipt, error)
}

// nativeSTRIDELeadShadowArtifactAdapter stores model output in a private,
// non-Drive candidate directory and validates it with the same deterministic
// native document/deck contracts used by customer artifacts. Its publication
// callbacks fail closed until a benchmark promotion is supplied.
type nativeSTRIDELeadShadowArtifactAdapter struct {
	dir string
}

func newNativeSTRIDELeadShadowArtifactAdapter(dir string) (*nativeSTRIDELeadShadowArtifactAdapter, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, ErrSTRIDELeadCandidateInvalid
	}
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrSTRIDELeadCandidateInvalid
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	return &nativeSTRIDELeadShadowArtifactAdapter{dir: dir}, nil
}

func (adapter *nativeSTRIDELeadShadowArtifactAdapter) StageCandidate(_ context.Context, request STRIDELeadCandidateArtifactRequest) (STRIDELeadCandidateArtifactReceipt, error) {
	if adapter == nil || request.Validate() != nil {
		return STRIDELeadCandidateArtifactReceipt{}, ErrSTRIDELeadCandidateInvalid
	}
	body := strings.TrimSpace(request.Body)
	bodyDigest := sha256Hex([]byte(body))
	artifactID := "stride-shadow-candidate-" + sha256Hex([]byte(request.Run.ID + "\x00" + request.Provider.ResponseID + "\x00" + bodyDigest))[:24]
	artifactType, outputContract := artifactTypeMarkdown, documentReportOutputContract
	if request.Run.OutputKind == "presentation" {
		artifactType, outputContract = artifactTypeHTMLDeck, "packaging_deck_v1"
	}
	record := struct {
		RunID              string    `json:"runId"`
		AssignmentID       string    `json:"assignmentId"`
		ProviderResponseID string    `json:"providerResponseId"`
		ArtifactType       string    `json:"artifactType"`
		Body               string    `json:"body"`
		BodyDigest         string    `json:"bodyDigest"`
		StagedAt           time.Time `json:"stagedAt"`
	}{request.Run.ID, request.Assignment.ID, request.Provider.ResponseID, artifactType, body, bodyDigest, request.ObservedAt}
	raw, err := json.Marshal(record)
	if err != nil {
		return STRIDELeadCandidateArtifactReceipt{}, err
	}
	path := filepath.Join(adapter.dir, artifactID+".json")
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
			return STRIDELeadCandidateArtifactReceipt{}, ErrSTRIDELeadCandidateInvalid
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil || sha256Hex(existing) != sha256Hex(raw) {
			return STRIDELeadCandidateArtifactReceipt{}, ErrSTRIDELeadCandidateInvalid
		}
	} else if !os.IsNotExist(statErr) {
		return STRIDELeadCandidateArtifactReceipt{}, statErr
	} else if err := writeFileAtomicallyForCanonicalMode(path, raw, 0o600); err != nil {
		return STRIDELeadCandidateArtifactReceipt{}, err
	}
	if repair, blocked := processStageLawSweep(ProcessStage{OutputContract: outputContract}, body); blocked {
		return STRIDELeadCandidateArtifactReceipt{}, errors.Join(ErrSTRIDELeadCandidateInvalid, errors.New(repair))
	}
	// Passing the law sweep proves only eligibility for the native pipeline; it
	// is not a render/open/editability receipt. Keep the private candidate
	// durable but append no artifact milestone until real validators issue all
	// three revision-bound receipts.
	return STRIDELeadCandidateArtifactReceipt{}, ErrSTRIDELeadValidationPending
}

func (adapter *nativeSTRIDELeadShadowArtifactAdapter) CommitCandidateToDrive(_ context.Context, _ STRIDELeadCandidateArtifactReceipt, promotion STRIDELeadBenchmarkPromotion) (STRIDELeadDriveCommitReceipt, error) {
	if promotion.Validate() != nil {
		return STRIDELeadDriveCommitReceipt{}, ErrSTRIDELeadPromotionRequired
	}
	return STRIDELeadDriveCommitReceipt{}, ErrSTRIDELeadPromotionRequired
}

func (adapter *nativeSTRIDELeadShadowArtifactAdapter) DeliverCandidateToChannel(_ context.Context, _ STRIDELeadDriveCommitReceipt, _ string, promotion STRIDELeadBenchmarkPromotion) (STRIDELeadChannelDeliveryReceipt, error) {
	if promotion.Validate() != nil {
		return STRIDELeadChannelDeliveryReceipt{}, ErrSTRIDELeadPromotionRequired
	}
	return STRIDELeadChannelDeliveryReceipt{}, ErrSTRIDELeadPromotionRequired
}

type STRIDELeadShadowRuntime struct {
	Harness      *STRIDELeadHarness
	Artifacts    STRIDELeadArtifactAdapter
	SpendCeiling int64
	Promotion    *STRIDELeadBenchmarkPromotion
	PollInterval time.Duration
	MaxPolls     int
	Now          func() time.Time
	Done         func(string, error)
	Idle         func() bool

	mu     sync.Mutex
	active map[string]struct{}
}

func newSTRIDELeadShadowRuntimeFromEnvironment(workRuns *STRIDEWorkRunRepository) (*STRIDELeadShadowRuntime, error) {
	if !strideLeadHarnessShadowEnabled() {
		return nil, nil
	}
	apiKey := strings.TrimSpace(os.Getenv(strideLeadShadowAPIKeyEnvironment))
	projectID := strings.TrimSpace(os.Getenv(strideLeadShadowProjectEnvironment))
	ceiling, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(strideLeadShadowSpendCeilingEnvironment)), 10, 64)
	if apiKey == "" || !strideIdentifier(projectID) || err != nil || ceiling < 1 || workRuns == nil {
		return nil, ErrSTRIDELeadHarnessInvalid
	}
	dir := strings.TrimSpace(os.Getenv(strideLeadShadowCandidateDirEnvironment))
	if dir == "" {
		path := strideWorkRunPath()
		if path == "" {
			return nil, ErrSTRIDELeadHarnessInvalid
		}
		dir = filepath.Join(filepath.Dir(path), "stride-lead-shadow-candidates")
	}
	artifacts, err := newNativeSTRIDELeadShadowArtifactAdapter(dir)
	if err != nil {
		return nil, err
	}
	provider := newSTRIDELeadResponsesClient(apiKey, projectID)
	return &STRIDELeadShadowRuntime{
		Harness: &STRIDELeadHarness{Enabled: true, WorkRuns: workRuns, Provider: provider}, Artifacts: artifacts,
		SpendCeiling: ceiling, PollInterval: strideLeadShadowPollInterval, MaxPolls: strideLeadShadowMaxPolls, Now: time.Now,
		active: map[string]struct{}{},
	}, nil
}

func (runtime *STRIDELeadShadowRuntime) Schedule(thread scoutAgentThread) {
	if runtime == nil || runtime.Harness == nil || runtime.Artifacts == nil || strideWorkRunOutputKind(thread) == "" {
		return
	}
	if runtime.Idle != nil && !runtime.Idle() {
		return
	}
	runtime.mu.Lock()
	if runtime.active == nil {
		runtime.active = map[string]struct{}{}
	}
	if _, active := runtime.active[thread.ID]; active {
		runtime.mu.Unlock()
		return
	}
	runtime.active[thread.ID] = struct{}{}
	runtime.mu.Unlock()
	go func() {
		err := runtime.run(context.Background(), thread)
		runtime.mu.Lock()
		delete(runtime.active, thread.ID)
		runtime.mu.Unlock()
		if runtime.Done != nil {
			runtime.Done(thread.ID, err)
		}
	}()
}

func (runtime *STRIDELeadShadowRuntime) run(ctx context.Context, thread scoutAgentThread) error {
	maxPolls := runtime.MaxPolls
	if maxPolls < 1 {
		maxPolls = 1
	}
	interval := runtime.PollInterval
	if interval <= 0 {
		interval = strideLeadShadowPollInterval
	}
	for poll := 0; poll < maxPolls; poll++ {
		if runtime.Idle != nil && !runtime.Idle() {
			return ErrSTRIDELeadShadowDeferred
		}
		receipt, err := runtime.runOnce(ctx, thread)
		if err != nil {
			return err
		}
		if receipt.Status == "completed" || leadProviderTerminal(receipt.Status) {
			return nil
		}
		if poll+1 == maxPolls {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func (runtime *STRIDELeadShadowRuntime) runOnce(ctx context.Context, thread scoutAgentThread) (STRIDELeadProviderReceipt, error) {
	if runtime == nil || runtime.Harness == nil || runtime.Harness.WorkRuns == nil || runtime.Artifacts == nil || runtime.SpendCeiling < 1 {
		return STRIDELeadProviderReceipt{}, ErrSTRIDELeadHarnessInvalid
	}
	card, err := runtime.Harness.WorkRuns.SideCard(thread.ID)
	if err != nil {
		return STRIDELeadProviderReceipt{}, err
	}
	approval := STRIDEReference{ContractType: STRIDEContractWorkProposal, ID: strings.TrimSpace(thread.Artifact.Metadata["approvedProposalId"]), Revision: 1, Digest: strings.TrimSpace(thread.Artifact.Metadata["operationBodyDigest"])}
	if approval.Validate() != nil {
		return STRIDELeadProviderReceipt{}, ErrSTRIDELeadHarnessInvalid
	}
	spendFence, err := STRIDEContractDigest(struct {
		RunID, ProjectID string
		Approval         STRIDEReference
		Ceiling          int64
	}{card.Run.ID, strings.TrimSpace(os.Getenv(strideLeadShadowProjectEnvironment)), approval, runtime.SpendCeiling})
	if err != nil {
		return STRIDELeadProviderReceipt{}, err
	}
	now := time.Now().UTC()
	if runtime.Now != nil {
		now = runtime.Now().UTC()
	}
	receipt, err := runtime.Harness.Run(ctx, STRIDELeadHarnessRequest{
		RunID:        card.Run.ID,
		Instructions: "Act as Scout, the accountable lead. Produce the exact native " + card.Run.OutputKind + " candidate from the admitted sources. Do not publish or claim delivery.",
		Input:        strings.TrimSpace(thread.Query),
		Spend:        STRIDELeadSpendBoundary{Approval: approval, MaximumSpendCents: runtime.SpendCeiling, ApprovalFenceDigest: spendFence},
		Now:          now,
	})
	if err != nil || receipt.Status != "completed" {
		return receipt, err
	}
	if strideLeadCandidateMilestone(card) {
		return receipt, nil
	}
	body, err := runtime.Harness.CompletedOutput(ctx, receipt)
	if err != nil {
		return receipt, err
	}
	card, err = runtime.Harness.WorkRuns.SideCard(thread.ID)
	if err != nil {
		return receipt, err
	}
	_, specialist, ok := strideLeadHarnessAssignments(card)
	if !ok {
		return receipt, ErrSTRIDELeadHarnessInvalid
	}
	if runtime.Idle != nil && !runtime.Idle() {
		return receipt, ErrSTRIDELeadShadowDeferred
	}
	candidate, err := runtime.Artifacts.StageCandidate(ctx, STRIDELeadCandidateArtifactRequest{Run: card.Run, Assignment: specialist, Provider: receipt, Body: body, ObservedAt: now})
	if err != nil || candidate.Validate() != nil {
		return receipt, errors.Join(ErrSTRIDELeadHarnessRecoverable, err)
	}
	if err := runtime.recordCandidate(card, specialist, candidate, now); err != nil {
		return receipt, err
	}
	// The production factory intentionally installs no Promotion. Benchmark
	// qualification must produce an explicit promotion contract before these
	// customer-visible Drive/channel callbacks can run.
	if runtime.Promotion == nil {
		return receipt, nil
	}
	if runtime.Promotion.Validate() != nil {
		return receipt, ErrSTRIDELeadPromotionRequired
	}
	drive, err := runtime.Artifacts.CommitCandidateToDrive(ctx, candidate, *runtime.Promotion)
	if err != nil || drive.Validate() != nil {
		return receipt, errors.Join(ErrSTRIDELeadPromotionRequired, err)
	}
	delivery, err := runtime.Artifacts.DeliverCandidateToChannel(ctx, drive, card.Run.Destination, *runtime.Promotion)
	if err != nil || delivery.Validate() != nil {
		return receipt, errors.Join(ErrSTRIDELeadPromotionRequired, err)
	}
	scout, _, _ := strideLeadHarnessAssignments(card)
	return receipt, runtime.Harness.appendMilestone(card.Run, scout, "delivery_committed", card.Phase, "Scout recorded the benchmark-promoted channel delivery", delivery.DeliveredAt)
}

func strideLeadCandidateMilestone(card STRIDEWorkRunSideCard) bool {
	for _, milestone := range card.Milestones {
		if milestone.Kind == "artifact_committed" {
			return true
		}
	}
	return false
}

func (runtime *STRIDELeadShadowRuntime) recordCandidate(card STRIDEWorkRunSideCard, specialist STRIDECanonicalAgentAssignment, candidate STRIDELeadCandidateArtifactReceipt, at time.Time) error {
	found := false
	for _, lineage := range card.ArtifactLineage {
		if lineage.Artifact == candidate.Artifact {
			found = true
			break
		}
	}
	if !found {
		event := strideWorkRunEvent(card.Run, "shadow-candidate-"+candidate.Artifact.ID, STRIDEShadowCandidateRecorded, specialist.Agent, strings.Title(specialist.Agent)+" validated a private benchmark candidate", at)
		event.Agent, event.AssignmentID = specialist.Agent, specialist.ID
		event.ArtifactLineage = []STRIDEArtifactLineageRef{{Artifact: candidate.Artifact, Relation: "created", Label: "Validated private shadow " + card.Run.OutputKind + " candidate"}}
		if _, _, err := runtime.Harness.WorkRuns.Append(event); err != nil {
			return err
		}
	}
	card, err := runtime.Harness.WorkRuns.SideCard(card.Run.ID)
	if err != nil {
		return err
	}
	if strideLeadCandidateMilestone(card) {
		return nil
	}
	scout, _, ok := strideLeadHarnessAssignments(card)
	if !ok {
		return ErrSTRIDELeadHarnessInvalid
	}
	milestone := STRIDELeadMilestone{RunID: card.Run.ID, AssignmentID: scout.ID, Kind: "artifact_committed", Phase: card.Phase, Summary: "Scout recorded a validated private candidate for benchmark review", Evidence: []STRIDEReference{candidate.Artifact}, CommittedAt: at}
	event := strideWorkRunEvent(card.Run, "milestone-artifact_committed", STRIDEMilestoneRecorded, scout.Agent, milestone.Summary, at)
	event.Agent, event.AssignmentID, event.Milestone = scout.Agent, scout.ID, &milestone
	_, _, err = runtime.Harness.WorkRuns.Append(event)
	return err
}

func (app *kanbanBoardApp) launchSTRIDELeadShadowAsync(thread scoutAgentThread) {
	if app != nil && app.strideLeadShadow != nil {
		app.strideLeadShadow.Schedule(thread)
	}
}

func (app *kanbanBoardApp) strideLeadShadowIdleAdmission() bool {
	if app == nil || app.meetings == nil || len(app.meetings.openRoomIDs()) > 0 {
		return false
	}
	app.meetingFinalizationRunMu.Lock()
	busy := app.meetingFinalizationWorker || len(app.meetingFinalizationQueue) > 0 || len(app.meetingFinalizationBacklog) > 0 ||
		len(app.meetingFinalizationRunning) > 0 || len(app.meetingFinalizationRetryTimers) > 0
	app.meetingFinalizationRunMu.Unlock()
	return !busy
}

// scheduleSTRIDELeadShadowRetrySweep is signaled only by stable close/idle
// boundaries. Missing WorkRun candidate milestones are the durable queue;
// these process-local flags merely coalesce concurrent signals.
func (app *kanbanBoardApp) scheduleSTRIDELeadShadowRetrySweep() {
	if app == nil || app.strideLeadShadow == nil {
		return
	}
	app.meetingFinalizationRunMu.Lock()
	if app.strideLeadShadowRetrySweep {
		app.strideLeadShadowRetryAgain = true
		app.meetingFinalizationRunMu.Unlock()
		return
	}
	app.strideLeadShadowRetrySweep = true
	app.meetingFinalizationRunMu.Unlock()
	go func() {
		for {
			if app.strideLeadShadowIdleAdmission() {
				app.reconcileSTRIDELeadShadowAtBoot()
			}
			app.meetingFinalizationRunMu.Lock()
			if app.strideLeadShadowRetryAgain {
				app.strideLeadShadowRetryAgain = false
				app.meetingFinalizationRunMu.Unlock()
				continue
			}
			app.strideLeadShadowRetrySweep = false
			app.meetingFinalizationRunMu.Unlock()
			return
		}
	}()
}

func (app *kanbanBoardApp) reconcileSTRIDELeadShadowAtBoot() {
	if app == nil || app.strideLeadShadow == nil || app.memory == nil || app.workRuns == nil {
		return
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		threadID := strings.TrimSpace(entry.Metadata["threadId"])
		if entry.Metadata["originKind"] != agentThreadOriginChannel || threadID == "" || strideWorkRunOutputKind(scoutAgentThread{Mode: entry.Metadata["mode"]}) == "" {
			continue
		}
		card, err := app.workRuns.SideCard(threadID)
		if err != nil || strideLeadCandidateMilestone(card) || card.Provider != nil && leadProviderTerminal(card.Provider.Status) && card.Provider.Status != "completed" {
			continue
		}
		app.launchSTRIDELeadShadowAsync(scoutAgentThread{ID: threadID, Mode: entry.Metadata["mode"], Query: firstNonEmptyString(entry.Metadata["threadQuery"], entry.Metadata["query"]), Status: entry.Metadata["threadStatus"], Artifact: entry})
	}
}

var _ STRIDELeadArtifactAdapter = (*nativeSTRIDELeadShadowArtifactAdapter)(nil)
