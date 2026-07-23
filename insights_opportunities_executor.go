package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	insightsExecutorJournalVersion = 1
	// bufio.Scanner rejects tokens that reach its configured maximum. Keeping
	// the append boundary and replay boundary on one constant prevents a valid
	// provider response from creating a journal that cannot be reopened.
	insightsExecutorJournalMaxEventBytes = 4 << 20
	insightsRunAwaitingApproval          = "awaiting_approval"
	insightsRunAwaitingReport            = "awaiting_report"
	insightsRunAwaitingCheckpoint        = "awaiting_critic_checkpoint"
	insightsRunAwaitingPublication       = "awaiting_workspace_publication"
	insightsRunAccepted                  = "accepted"
	insightsRunRejected                  = "rejected"
	insightsRunRevisionExhausted         = "revision_exhausted"
)

var (
	ErrInsightsOpportunitiesDisabled      = errors.New("insights opportunities v1 is disabled")
	ErrInsightsOpportunitiesUnavailable   = errors.New("insights opportunities v1 executor is unavailable")
	ErrInsightsOpportunitiesConflict      = errors.New("insights opportunities v1 durable conflict")
	ErrInsightsOpportunitiesEventTooLarge = errors.New("insights opportunities v1 journal event exceeds replay capacity")
)

// InsightsOpportunitiesProviderExecution is provider-owned telemetry returned
// beside a model result. The executor accepts only the closed static seat and
// journals this exact receipt; model-authored route labels are never trusted.
type InsightsOpportunitiesProviderExecution struct {
	Provider string                     `json:"provider"`
	Model    string                     `json:"model"`
	Effort   string                     `json:"effort,omitempty"`
	Retries  int                        `json:"retries"`
	Usage    InsightsOpportunitiesUsage `json:"usage"`
	Request  string                     `json:"requestId"`
	Metadata map[string]string          `json:"metadata,omitempty"`
}

func (execution InsightsOpportunitiesProviderExecution) validate(seat InsightsOpportunitiesRouteSeat) error {
	if strings.TrimSpace(execution.Provider) == "" || strings.TrimSpace(execution.Request) == "" || execution.Model != seat.Model || execution.Effort != seat.Effort || execution.Retries < 0 ||
		execution.Usage.InputTokens < 0 || execution.Usage.CachedInputTokens < 0 || execution.Usage.OutputTokens < 0 {
		return fmt.Errorf("provider execution does not match pinned %s seat", seat.Purpose)
	}
	return nil
}

type InsightsOpportunitiesOrchestrationPlan struct {
	Focus       []string `json:"focus"`
	Constraints []string `json:"constraints"`
}

func (plan InsightsOpportunitiesOrchestrationPlan) validate() error {
	if len(plan.Focus) == 0 || validateInsightsTextList("orchestration focus", plan.Focus) != nil || validateInsightsTextList("orchestration constraints", plan.Constraints) != nil {
		return errors.New("orchestration plan is empty or malformed")
	}
	return nil
}

type InsightsOpportunitiesGenerationProvider interface {
	Orchestrate(context.Context, InsightsOpportunitiesRequest, *InsightsOpportunitiesReport, *InsightsOpportunitiesCriticVerdict) (InsightsOpportunitiesOrchestrationPlan, InsightsOpportunitiesProviderExecution, error)
	Generate(context.Context, InsightsOpportunitiesRequest, InsightsOpportunitiesOrchestrationPlan, int, *InsightsOpportunitiesReport, *InsightsOpportunitiesCriticVerdict) (InsightsOpportunitiesReport, InsightsOpportunitiesProviderExecution, error)
}

type InsightsOpportunitiesReviewProvider interface {
	Review(context.Context, InsightsOpportunitiesRequest, InsightsOpportunitiesReport) (InsightsOpportunitiesCriticVerdict, InsightsOpportunitiesProviderExecution, error)
}

// The only writer capability exposed to W2C is the already-approved workspace
// report destination. There is deliberately no email/share/publish/deploy or
// generic external tool interface.
type InsightsOpportunitiesWorkspaceWriter interface {
	WriteInsightsOpportunitiesReport(context.Context, ACLPrincipal, InsightsOpportunitiesRequest, InsightsOpportunitiesReport, InsightsOpportunitiesWorkspaceWriteAuthority, string, InsightsOpportunitiesPublicationGuard) (InsightsOpportunitiesWorkspaceWriteReceipt, error)
}

// InsightsOpportunitiesPublicationGuard is invoked by the workspace writer
// after it owns the mutation lock and immediately before it appends or updates
// the artifact. It deliberately re-reads both authorization metadata and the
// authoritative evidence bodies/consent state; a preflight decision is not a
// publication capability.
type InsightsOpportunitiesPublicationGuard func(context.Context, bool) error

type InsightsOpportunitiesEvidenceReauthorizer interface {
	ReauthorizeEvidence(context.Context, ACLPrincipal, []RetrievalSnapshotSource) error
}

// InsightsOpportunitiesWorkspaceWriteAuthority is an immutable, server-issued
// capability for exactly one report mutation. The writer validates the target
// and idempotency binding at the mutation boundary, eliminating a revocable
// check followed later by an unguarded write.
type InsightsOpportunitiesWorkspaceWriteAuthority struct {
	AuthorityID    string      `json:"authorityId"`
	TargetDigest   string      `json:"targetDigest"`
	IdempotencyKey string      `json:"idempotencyKey"`
	Decision       ACLDecision `json:"decision"`
}

type InsightsOpportunitiesPublicationAuthorizer interface {
	AuthorizeInsightsOpportunitiesPublication(context.Context, ACLPrincipal, InsightsOpportunitiesAuthorizationTarget, string) (InsightsOpportunitiesWorkspaceWriteAuthority, error)
	VerifyInsightsOpportunitiesPublication(context.Context, ACLPrincipal, InsightsOpportunitiesAuthorizationTarget, string, InsightsOpportunitiesWorkspaceWriteAuthority) error
}

func (authority InsightsOpportunitiesWorkspaceWriteAuthority) validate(target InsightsOpportunitiesAuthorizationTarget, key string) error {
	digest, err := CanonicalStateDigest(target)
	if err != nil || strings.TrimSpace(authority.AuthorityID) == "" || authority.TargetDigest != digest || authority.IdempotencyKey != key || !validInsightsAuthorizationDecision(authority.Decision) {
		return errors.New("workspace write authority is invalid or does not match the mutation")
	}
	return nil
}

// Approval recovery is mandatory for a runnable executor. Consume is a
// server-durable direct-once transition; if the process loses power before its
// local journal records the receipt, Recover returns that exact receipt rather
// than consuming again or weakening replay protection.
type InsightsOpportunitiesApprovalRecovery interface {
	RecoverInsightsOpportunitiesApproval(context.Context, ACLPrincipal, InsightsOpportunitiesAuthorizationTarget) (InsightsOpportunitiesApprovalConsumption, error)
}

type InsightsOpportunitiesWorkspaceWriteReceipt struct {
	IdempotencyKey string    `json:"idempotencyKey"`
	ArtifactID     string    `json:"artifactId"`
	ReportDigest   string    `json:"reportDigest"`
	WrittenAt      time.Time `json:"writtenAt"`
}

func (receipt InsightsOpportunitiesWorkspaceWriteReceipt) validate(key string, report InsightsOpportunitiesReport) error {
	if receipt.IdempotencyKey != key || strings.TrimSpace(receipt.ArtifactID) == "" || receipt.ReportDigest != report.ReportDigest || receipt.WrittenAt.IsZero() {
		return errors.New("workspace writer returned an invalid direct-once receipt")
	}
	return nil
}

type InsightsOpportunitiesCandidate struct {
	Report     InsightsOpportunitiesReport                       `json:"report"`
	Verdict    InsightsOpportunitiesCriticVerdict                `json:"verdict"`
	Plan       InsightsOpportunitiesOrchestrationPlan            `json:"plan"`
	Executions map[string]InsightsOpportunitiesProviderExecution `json:"executions"`
	CreatedAt  time.Time                                         `json:"createdAt"`
}

type InsightsOpportunitiesCriticCheckpoint struct {
	ReportDigest  string    `json:"reportDigest"`
	VerdictDigest string    `json:"verdictDigest"`
	CheckpointID  string    `json:"checkpointId"`
	Resumed       bool      `json:"resumed"`
	At            time.Time `json:"at"`
}

type InsightsOpportunitiesRun struct {
	RunID        string                                       `json:"runId"`
	Status       string                                       `json:"status"`
	Request      InsightsOpportunitiesRequest                 `json:"request"`
	Principal    ACLPrincipal                                 `json:"principal"`
	Approval     *InsightsOpportunitiesApprovalConsumption    `json:"approval,omitempty"`
	Candidates   []InsightsOpportunitiesCandidate             `json:"candidates"`
	Reports      []InsightsOpportunitiesReport                `json:"reports"`
	Verdicts     []InsightsOpportunitiesCriticVerdict         `json:"verdicts"`
	Checkpoints  []InsightsOpportunitiesCriticCheckpoint      `json:"checkpoints"`
	Publication  *InsightsOpportunitiesWorkspaceWriteReceipt  `json:"publication,omitempty"`
	Publications []InsightsOpportunitiesWorkspaceWriteReceipt `json:"publications,omitempty"`
	Feedback     []InsightsOpportunitiesFeedback              `json:"feedback,omitempty"`
	PilotReviews []InsightsOpportunitiesPilotReview           `json:"pilotReviews,omitempty"`
	CreatedAt    time.Time                                    `json:"createdAt"`
	UpdatedAt    time.Time                                    `json:"updatedAt"`
}

type insightsExecutorJournalEvent struct {
	Version     int                                         `json:"version"`
	Sequence    uint64                                      `json:"sequence"`
	Type        string                                      `json:"type"`
	RunID       string                                      `json:"runId"`
	At          time.Time                                   `json:"at"`
	Digest      string                                      `json:"digest"`
	Request     *InsightsOpportunitiesRequest               `json:"request,omitempty"`
	Principal   *ACLPrincipal                               `json:"principal,omitempty"`
	Approval    *InsightsOpportunitiesApprovalConsumption   `json:"approval,omitempty"`
	Candidate   *InsightsOpportunitiesCandidate             `json:"candidate,omitempty"`
	Checkpoint  *InsightsOpportunitiesCriticCheckpoint      `json:"checkpoint,omitempty"`
	Publication *InsightsOpportunitiesWorkspaceWriteReceipt `json:"publication,omitempty"`
	Feedback    *InsightsOpportunitiesFeedback              `json:"feedback,omitempty"`
	PilotReview *InsightsOpportunitiesPilotReview           `json:"pilotReview,omitempty"`
}

func (event insightsExecutorJournalEvent) canonicalDigest() (string, error) {
	event.Digest = ""
	return CanonicalStateDigest(event)
}

type InsightsOpportunitiesRunStore struct {
	mu              sync.Mutex
	path            string
	nextSequence    uint64
	runs            map[string]*InsightsOpportunitiesRun
	feedbackKeys    map[string]string
	pilotReviewKeys map[string]string
}

type InsightsOpportunitiesPilotQualification struct {
	ReleaseCommit   string   `json:"releaseCommit"`
	PromptVersion   string   `json:"promptVersion"`
	QualifyingRuns  int      `json:"qualifyingRuns"`
	EligibleReviews int      `json:"eligibleReviews"`
	Reviewers       []string `json:"reviewers"`
	Qualified       bool     `json:"qualified"`
}

type InsightsOpportunitiesRunHeader struct {
	RunID               string
	TenantID            string
	OwnerID             string
	ReportID            string
	ReportDigest        string
	ArtifactDestination string
	RequestDigest       string
	EvidenceDigest      string
	HasReport           bool
}

func OpenInsightsOpportunitiesRunStore(path string) (*InsightsOpportunitiesRunStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("insights executor journal path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	store := &InsightsOpportunitiesRunStore{path: path, nextSequence: 1, runs: map[string]*InsightsOpportunitiesRun{}, feedbackKeys: map[string]string{}, pilotReviewKeys: map[string]string{}}
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := recoverInsightsJournalTail(file, path); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), insightsExecutorJournalMaxEventBytes)
	for scanner.Scan() {
		var event insightsExecutorJournalEvent
		if err := decodeInsightsStrict(scanner.Bytes(), &event, "insights executor journal event"); err != nil {
			return nil, err
		}
		want, digestErr := event.canonicalDigest()
		if digestErr != nil || event.Version != insightsExecutorJournalVersion || event.Sequence != store.nextSequence || event.At.IsZero() || event.Digest != want {
			return nil, errors.New("insights executor journal continuity or digest failure")
		}
		if err := store.applyLocked(event); err != nil {
			return nil, err
		}
		store.nextSequence++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return store, nil
}

// A crash during a single append can leave only the final JSONL record torn.
// Earlier corruption remains a hard failure; only a non-newline-terminated tail
// is truncated back to the last durable record boundary.
func recoverInsightsJournalTail(file *os.File, path string) error {
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	const chunkSize int64 = 64 << 10
	end := info.Size()
	truncateAt := int64(0)
	for end > 0 {
		start := end - chunkSize
		if start < 0 {
			start = 0
		}
		chunk := make([]byte, end-start)
		if _, err := file.ReadAt(chunk, start); err != nil {
			return err
		}
		if index := strings.LastIndexByte(string(chunk), '\n'); index >= 0 {
			truncateAt = start + int64(index) + 1
			break
		}
		end = start
	}
	if err := file.Truncate(truncateAt); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func (store *InsightsOpportunitiesRunStore) appendLocked(event insightsExecutorJournalEvent) error {
	event.Version, event.Sequence = insightsExecutorJournalVersion, store.nextSequence
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	digest, err := event.canonicalDigest()
	if err != nil {
		return err
	}
	event.Digest = digest
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if len(raw) >= insightsExecutorJournalMaxEventBytes {
		return ErrInsightsOpportunitiesEventTooLarge
	}
	// applyLocked is the transition validator and state folder. Run it against
	// an isolated clone before fsync so an invalid internal transition can
	// never poison the durable journal or make the next restart fail replay.
	preflight, err := store.cloneForFoldLocked(event)
	if err != nil {
		return err
	}
	if err := preflight.applyLocked(event); err != nil {
		return err
	}
	if err := appendFileDurably(store.path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	// With the lock held, the real fold must be identical to the successful
	// preflight. A failure here is an invariant violation, not a caller error.
	if err := store.applyLocked(event); err != nil {
		return fmt.Errorf("durable insights event passed preflight but could not be folded: %w", err)
	}
	store.nextSequence++
	return nil
}

func (store *InsightsOpportunitiesRunStore) cloneForFoldLocked(event insightsExecutorJournalEvent) (*InsightsOpportunitiesRunStore, error) {
	clone := &InsightsOpportunitiesRunStore{
		path:            store.path,
		nextSequence:    store.nextSequence,
		runs:            make(map[string]*InsightsOpportunitiesRun, 1),
		feedbackKeys:    make(map[string]string, 1),
		pilotReviewKeys: make(map[string]string, 1),
	}
	if run, found := store.runs[event.RunID]; found {
		raw, err := json.Marshal(run)
		if err != nil {
			return nil, err
		}
		var copied InsightsOpportunitiesRun
		if err := json.Unmarshal(raw, &copied); err != nil {
			return nil, err
		}
		clone.runs[event.RunID] = &copied
	}
	if event.Feedback != nil {
		key := event.RunID + "\x00" + event.Feedback.IdempotencyKey
		if digest := store.feedbackKeys[key]; digest != "" {
			clone.feedbackKeys[key] = digest
		}
	}
	if event.PilotReview != nil {
		key := event.RunID + "\x00" + event.PilotReview.PilotReviewID
		if digest := store.pilotReviewKeys[key]; digest != "" {
			clone.pilotReviewKeys[key] = digest
		}
	}
	return clone, nil
}

func (store *InsightsOpportunitiesRunStore) applyLocked(event insightsExecutorJournalEvent) error {
	run := store.runs[event.RunID]
	switch event.Type {
	case "run_intent":
		if run != nil || event.Request == nil || event.Principal == nil || event.Approval != nil || event.Request.RunID != event.RunID {
			return ErrInsightsOpportunitiesConflict
		}
		store.runs[event.RunID] = &InsightsOpportunitiesRun{RunID: event.RunID, Status: insightsRunAwaitingApproval, Request: *event.Request, Principal: *event.Principal, CreatedAt: event.At, UpdatedAt: event.At}
	case "approval_checkpoint":
		if run == nil || run.Status != insightsRunAwaitingApproval || run.Approval != nil || event.Approval == nil || event.Approval.RunID != event.RunID {
			return ErrInsightsOpportunitiesConflict
		}
		receipt := *event.Approval
		run.Approval, run.Status, run.UpdatedAt = &receipt, insightsRunAwaitingReport, event.At
	case "candidate":
		if run == nil || run.Status != insightsRunAwaitingReport || run.Approval == nil || event.Candidate == nil || len(run.Candidates) != len(run.Reports) || len(run.Candidates) >= 2 {
			return ErrInsightsOpportunitiesConflict
		}
		candidate := *event.Candidate
		if candidate.Report.Revision != len(run.Candidates)+1 || candidate.Report.RunID != run.RunID ||
			!validInsightsCriticOutcome(candidate.Verdict.Outcome) || candidate.Verdict.ReportDigest != candidate.Report.ReportDigest ||
			candidate.Report.RunMetadata.CriticOutcome != candidate.Verdict.Outcome ||
			candidate.Report.Terminal != (candidate.Verdict.Outcome != insightsCriticRevise || candidate.Report.Revision == 2) {
			return ErrInsightsOpportunitiesConflict
		}
		run.Candidates = append(run.Candidates, candidate)
		run.Status, run.UpdatedAt = insightsRunAwaitingCheckpoint, event.At
	case "critic_checkpoint":
		if run == nil || run.Status != insightsRunAwaitingCheckpoint || event.Checkpoint == nil || len(run.Candidates) != len(run.Reports)+1 {
			return ErrInsightsOpportunitiesConflict
		}
		candidate := run.Candidates[len(run.Candidates)-1]
		if !validInsightsCriticOutcome(candidate.Verdict.Outcome) || strings.TrimSpace(event.Checkpoint.CheckpointID) == "" || event.Checkpoint.At.IsZero() ||
			event.Checkpoint.ReportDigest != candidate.Report.ReportDigest || event.Checkpoint.VerdictDigest != candidate.Verdict.VerdictDigest {
			return ErrInsightsOpportunitiesConflict
		}
		run.Reports = append(run.Reports, candidate.Report)
		run.Verdicts = append(run.Verdicts, candidate.Verdict)
		run.Checkpoints = append(run.Checkpoints, *event.Checkpoint)
		switch candidate.Verdict.Outcome {
		case insightsCriticRevise:
			if candidate.Report.Revision < 2 {
				run.Status = insightsRunAwaitingReport
			} else {
				run.Status = insightsRunRevisionExhausted
			}
		case insightsCriticReject:
			run.Status = insightsRunRejected
		case insightsCriticAccept:
			run.Status = insightsRunAwaitingPublication
		default:
			return ErrInsightsOpportunitiesConflict
		}
		run.UpdatedAt = event.At
	case "workspace_published":
		if run == nil || event.Publication == nil || run.Status != insightsRunAwaitingPublication || len(run.Reports) == 0 || event.Publication.ReportDigest != run.Reports[len(run.Reports)-1].ReportDigest {
			return ErrInsightsOpportunitiesConflict
		}
		if run.Publication != nil {
			if len(run.Reports) != 2 || len(run.Publications) != 1 || run.Publication.ReportDigest != run.Reports[0].ReportDigest || event.Publication.ReportDigest == run.Publication.ReportDigest {
				return ErrInsightsOpportunitiesConflict
			}
		} else if len(run.Publications) != 0 {
			return ErrInsightsOpportunitiesConflict
		}
		receipt := *event.Publication
		run.Publications = append(run.Publications, receipt)
		run.Publication, run.Status, run.UpdatedAt = &receipt, insightsRunAccepted, event.At
	case "feedback":
		if run == nil || event.Feedback == nil || len(run.Reports) == 0 || (run.Status != insightsRunAccepted && run.Status != insightsRunRejected && run.Status != insightsRunRevisionExhausted) {
			return ErrInsightsOpportunitiesConflict
		}
		key := event.RunID + "\x00" + event.Feedback.IdempotencyKey
		if prior := store.feedbackKeys[key]; prior != "" {
			if prior == event.Feedback.ActionDigest {
				return nil
			}
			return ErrInsightsOpportunitiesConflict
		}
		store.feedbackKeys[key] = event.Feedback.ActionDigest
		run.Feedback = append(run.Feedback, *event.Feedback)
		if event.Feedback.Action == insightsFeedbackRequestRevision && len(run.Reports) == 1 {
			run.Status = insightsRunAwaitingReport
		}
		run.UpdatedAt = event.At
	case "pilot_review":
		if run == nil || event.PilotReview == nil || (run.Status != insightsRunAccepted && run.Status != insightsRunRejected && run.Status != insightsRunRevisionExhausted) {
			return ErrInsightsOpportunitiesConflict
		}
		key := event.RunID + "\x00" + event.PilotReview.PilotReviewID
		if prior := store.pilotReviewKeys[key]; prior != "" {
			if prior == event.PilotReview.ReviewDigest {
				return nil
			}
			return ErrInsightsOpportunitiesConflict
		}
		store.pilotReviewKeys[key] = event.PilotReview.ReviewDigest
		run.PilotReviews = append(run.PilotReviews, *event.PilotReview)
		run.UpdatedAt = event.At
	default:
		return fmt.Errorf("unknown insights executor journal event %q", event.Type)
	}
	return nil
}

func cloneInsightsRun(run *InsightsOpportunitiesRun) (InsightsOpportunitiesRun, bool) {
	if run == nil {
		return InsightsOpportunitiesRun{}, false
	}
	raw, _ := json.Marshal(run)
	var clone InsightsOpportunitiesRun
	_ = json.Unmarshal(raw, &clone)
	return clone, true
}

func (store *InsightsOpportunitiesRunStore) Run(runID string) (InsightsOpportunitiesRun, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return cloneInsightsRun(store.runs[strings.TrimSpace(runID)])
}

func (store *InsightsOpportunitiesRunStore) RunHeader(runID string) (InsightsOpportunitiesRunHeader, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	run := store.runs[strings.TrimSpace(runID)]
	if run == nil {
		return InsightsOpportunitiesRunHeader{}, false
	}
	header := InsightsOpportunitiesRunHeader{RunID: run.RunID, TenantID: run.Request.TenantID, OwnerID: run.Principal.ID, ArtifactDestination: run.Request.ArtifactDestination, RequestDigest: run.Request.RequestDigest, EvidenceDigest: run.Request.EvidenceSnapshot.SnapshotID}
	if len(run.Reports) > 0 {
		report := run.Reports[len(run.Reports)-1]
		header.ReportID, header.ReportDigest, header.HasReport = report.ReportID, report.ReportDigest, true
	}
	return header, true
}

// PilotQualification is a body-free release gate. It never creates pilot
// rows: only authenticated durable reviews already present in the journal can
// count, and a behavior-affecting release/prompt change naturally selects an
// empty qualification set.
func (store *InsightsOpportunitiesRunStore) PilotQualification(releaseCommit, promptVersion string) InsightsOpportunitiesPilotQualification {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := InsightsOpportunitiesPilotQualification{ReleaseCommit: strings.TrimSpace(releaseCommit), PromptVersion: strings.TrimSpace(promptVersion)}
	runs, reviewers := map[string]bool{}, map[string]bool{}
	for runID, run := range store.runs {
		if run == nil || len(run.Reports) == 0 || (run.Status != insightsRunAccepted && run.Status != insightsRunRejected && run.Status != insightsRunRevisionExhausted) {
			continue
		}
		for _, review := range run.PilotReviews {
			if review.ReleaseCommit != result.ReleaseCommit || review.PromptVersion != result.PromptVersion || review.ProcessVersion != insightsOpportunitiesProcessVersion || review.SchemaVersion != insightsOpportunitiesReportSchema {
				continue
			}
			result.EligibleReviews++
			runs[runID], reviewers[review.ReviewerID] = true, true
		}
	}
	result.QualifyingRuns = len(runs)
	for reviewer := range reviewers {
		result.Reviewers = append(result.Reviewers, reviewer)
	}
	sort.Strings(result.Reviewers)
	result.Qualified = result.QualifyingRuns >= 10 && len(result.Reviewers) >= 2
	return result
}

func (store *InsightsOpportunitiesRunStore) startIntent(request InsightsOpportunitiesRequest, principal ACLPrincipal, at time.Time) (InsightsOpportunitiesRun, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing := store.runs[request.RunID]; existing != nil {
		if existing.Request.RequestDigest != request.RequestDigest || !sameInsightsPrincipal(existing.Principal, principal) {
			return InsightsOpportunitiesRun{}, false, ErrInsightsOpportunitiesConflict
		}
		clone, _ := cloneInsightsRun(existing)
		return clone, false, nil
	}
	event := insightsExecutorJournalEvent{Type: "run_intent", RunID: request.RunID, At: at, Request: &request, Principal: &principal}
	if err := store.appendLocked(event); err != nil {
		return InsightsOpportunitiesRun{}, false, err
	}
	clone, _ := cloneInsightsRun(store.runs[request.RunID])
	return clone, true, nil
}

func (store *InsightsOpportunitiesRunStore) checkpointApproval(runID string, approval InsightsOpportunitiesApprovalConsumption, at time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.appendLocked(insightsExecutorJournalEvent{Type: "approval_checkpoint", RunID: runID, At: at, Approval: &approval})
}

func (store *InsightsOpportunitiesRunStore) appendCandidate(runID string, candidate InsightsOpportunitiesCandidate) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.appendLocked(insightsExecutorJournalEvent{Type: "candidate", RunID: runID, At: candidate.CreatedAt, Candidate: &candidate})
}

func (store *InsightsOpportunitiesRunStore) checkpoint(runID string, checkpoint InsightsOpportunitiesCriticCheckpoint) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.appendLocked(insightsExecutorJournalEvent{Type: "critic_checkpoint", RunID: runID, At: checkpoint.At, Checkpoint: &checkpoint})
}

func (store *InsightsOpportunitiesRunStore) publish(runID string, receipt InsightsOpportunitiesWorkspaceWriteReceipt) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.appendLocked(insightsExecutorJournalEvent{Type: "workspace_published", RunID: runID, At: receipt.WrittenAt, Publication: &receipt})
}

func (store *InsightsOpportunitiesRunStore) appendFeedback(runID string, feedback InsightsOpportunitiesFeedback) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := runID + "\x00" + feedback.IdempotencyKey
	if prior := store.feedbackKeys[key]; prior != "" {
		if prior == feedback.ActionDigest {
			return nil
		}
		return ErrInsightsOpportunitiesConflict
	}
	return store.appendLocked(insightsExecutorJournalEvent{Type: "feedback", RunID: runID, At: feedback.At, Feedback: &feedback})
}

func (store *InsightsOpportunitiesRunStore) feedbackRecorded(runID, key, digest string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	prior := store.feedbackKeys[strings.TrimSpace(runID)+"\x00"+strings.TrimSpace(key)]
	if prior == "" {
		return false, nil
	}
	if prior != digest {
		return false, ErrInsightsOpportunitiesConflict
	}
	return true, nil
}

func (store *InsightsOpportunitiesRunStore) appendPilotReview(runID string, review InsightsOpportunitiesPilotReview) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := runID + "\x00" + review.PilotReviewID
	if prior := store.pilotReviewKeys[key]; prior != "" {
		if prior == review.ReviewDigest {
			return nil
		}
		return ErrInsightsOpportunitiesConflict
	}
	return store.appendLocked(insightsExecutorJournalEvent{Type: "pilot_review", RunID: runID, At: review.ReviewedAt, PilotReview: &review})
}

type InsightsOpportunitiesExecutor struct {
	Store      *InsightsOpportunitiesRunStore
	Kernel     AuthorizationKernel
	Purge      BrainPurgeGenerationResolver
	Evidence   InsightsOpportunitiesEvidenceReauthorizer
	Verifier   InsightsOpportunitiesAuthorizationVerifier
	Generation InsightsOpportunitiesGenerationProvider
	Review     InsightsOpportunitiesReviewProvider
	Writer     InsightsOpportunitiesWorkspaceWriter
	Now        func() time.Time
	Enabled    func() bool
	runLocks   sync.Map
}

func (executor *InsightsOpportunitiesExecutor) lockRun(runID string) func() {
	lockValue, _ := executor.runLocks.LoadOrStore(strings.TrimSpace(runID), &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func (executor *InsightsOpportunitiesExecutor) now() time.Time {
	if executor != nil && executor.Now != nil {
		return executor.Now().UTC()
	}
	return time.Now().UTC()
}

func (executor *InsightsOpportunitiesExecutor) ready() error {
	if executor == nil || executor.Store == nil || executor.Kernel.Store == nil || executor.Purge == nil || executor.Evidence == nil || executor.Verifier == nil || executor.Generation == nil || executor.Review == nil || executor.Writer == nil {
		return ErrInsightsOpportunitiesUnavailable
	}
	if _, ok := executor.Verifier.(InsightsOpportunitiesApprovalRecovery); !ok {
		return fmt.Errorf("%w: approval receipt recovery is required", ErrInsightsOpportunitiesUnavailable)
	}
	if _, ok := executor.Verifier.(InsightsOpportunitiesPublicationAuthorizer); !ok {
		return fmt.Errorf("%w: mutation-bound publication authority is required", ErrInsightsOpportunitiesUnavailable)
	}
	if executor.Enabled != nil {
		if !executor.Enabled() {
			return ErrInsightsOpportunitiesDisabled
		}
	} else if !insightsOpportunitiesRequested() {
		return ErrInsightsOpportunitiesDisabled
	}
	return nil
}

func (executor *InsightsOpportunitiesExecutor) reauthorizeEvidence(ctx context.Context, principal ACLPrincipal, request InsightsOpportunitiesRequest) error {
	if err := ReauthorizeRetrievalSnapshot(ctx, executor.Kernel, executor.Purge, principal, request.EvidenceSnapshot); err != nil {
		return err
	}
	if err := executor.Evidence.ReauthorizeEvidence(ctx, principal, request.EvidenceSnapshot.Sources); err != nil {
		return err
	}
	return nil
}

func sameInsightsPrincipal(a, b ACLPrincipal) bool {
	if a.TenantID != b.TenantID || a.ID != b.ID || a.Kind != b.Kind || a.RoomID != b.RoomID || a.SittingID != b.SittingID || len(a.TeamIDs) != len(b.TeamIDs) {
		return false
	}
	for index := range a.TeamIDs {
		if a.TeamIDs[index] != b.TeamIDs[index] {
			return false
		}
	}
	return true
}

func insightsExecutorApprovalTarget(request InsightsOpportunitiesRequest) InsightsOpportunitiesAuthorizationTarget {
	return InsightsOpportunitiesAuthorizationTarget{
		Purpose: "direct_request_approval", TenantID: request.TenantID, ResourceType: "insights_request", ResourceID: request.RequestID,
		ContentDigest: request.RequestDigest, ArtifactDestination: request.ArtifactDestination, ApprovalID: request.Approval.ApprovalID,
		ApprovalKind: request.Approval.ApprovalKind, ApprovedAt: request.Approval.ApprovedAt, ActorID: request.Approval.ApprovedBy,
		RequestRevisionDigest: request.RequestDigest, EvidenceSnapshotDigest: request.Approval.EvidenceSnapshotDigest, RecallCoverageDigest: request.Approval.RecallCoverageDigest,
		ProcessVersion: request.Approval.ProcessVersion, PromptVersion: request.Approval.PromptVersion, Action: request.Approval.Action,
		WorkspaceWriteDigest: request.Approval.WorkspaceWriteDigest, RunID: request.RunID,
	}
}

func (executor *InsightsOpportunitiesExecutor) Execute(ctx context.Context, principal ACLPrincipal, request InsightsOpportunitiesRequest) (InsightsOpportunitiesRun, error) {
	if err := executor.ready(); err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	unlock := executor.lockRun(request.RunID)
	defer unlock()
	run, exists := executor.Store.Run(request.RunID)
	createdIntent := false
	if !exists {
		if err := request.Validate(); err != nil {
			return InsightsOpportunitiesRun{}, err
		}
		if err := validateInsightsAuthenticatedPrincipal(principal, request.TenantID, request.PrincipalKind, request.PrincipalID); err != nil || request.Approval.ApprovedBy != principal.ID {
			if err != nil {
				return InsightsOpportunitiesRun{}, err
			}
			return InsightsOpportunitiesRun{}, errors.New("approval actor does not match the authenticated principal")
		}
		var err error
		run, createdIntent, err = executor.Store.startIntent(request, principal, executor.now())
		if err != nil {
			return InsightsOpportunitiesRun{}, err
		}
	} else {
		if run.Request.RequestDigest != request.RequestDigest || !sameInsightsPrincipal(run.Principal, principal) {
			return InsightsOpportunitiesRun{}, ErrInsightsOpportunitiesConflict
		}
	}
	if run.Approval == nil {
		var receipt InsightsOpportunitiesApprovalConsumption
		var err error
		if createdIntent {
			receipt, err = request.ValidateAuthorized(ctx, principal, executor.Kernel, executor.Purge, executor.Verifier)
		} else {
			recovery := executor.Verifier.(InsightsOpportunitiesApprovalRecovery)
			receipt, err = recovery.RecoverInsightsOpportunitiesApproval(ctx, principal, insightsExecutorApprovalTarget(request))
			if err == nil {
				err = request.ResumeAuthorized(ctx, principal, executor.Kernel, executor.Purge, executor.Verifier, receipt)
			}
		}
		if err != nil {
			return InsightsOpportunitiesRun{}, err
		}
		if err := executor.Store.checkpointApproval(request.RunID, receipt, executor.now()); err != nil {
			return InsightsOpportunitiesRun{}, err
		}
		run, _ = executor.Store.Run(request.RunID)
	} else {
		if err := request.ResumeAuthorized(ctx, principal, executor.Kernel, executor.Purge, executor.Verifier, *run.Approval); err != nil {
			return InsightsOpportunitiesRun{}, err
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return InsightsOpportunitiesRun{}, err
		}
		run, _ = executor.Store.Run(request.RunID)
		switch run.Status {
		case insightsRunAccepted, insightsRunRejected, insightsRunRevisionExhausted:
			return run, nil
		case insightsRunAwaitingPublication:
			return executor.publish(ctx, principal, run)
		case insightsRunAwaitingCheckpoint:
			if err := executor.checkpointCandidate(ctx, principal, run); err != nil {
				return InsightsOpportunitiesRun{}, err
			}
		case insightsRunAwaitingReport:
			candidate, err := executor.buildCandidate(ctx, run)
			if err != nil {
				return InsightsOpportunitiesRun{}, err
			}
			if err := executor.Store.appendCandidate(run.RunID, candidate); err != nil {
				return InsightsOpportunitiesRun{}, err
			}
		default:
			return InsightsOpportunitiesRun{}, fmt.Errorf("%w: unknown run state %q", ErrInsightsOpportunitiesConflict, run.Status)
		}
	}
}

func (executor *InsightsOpportunitiesExecutor) buildCandidate(ctx context.Context, run InsightsOpportunitiesRun) (InsightsOpportunitiesCandidate, error) {
	revision := len(run.Reports) + 1
	if revision < 1 || revision > 2 {
		return InsightsOpportunitiesCandidate{}, ErrInsightsOpportunitiesConflict
	}
	var priorReport *InsightsOpportunitiesReport
	var priorVerdict *InsightsOpportunitiesCriticVerdict
	if len(run.Reports) > 0 {
		priorReport = &run.Reports[len(run.Reports)-1]
		priorVerdict = &run.Verdicts[len(run.Verdicts)-1]
	}
	plan, orchestrationExecution, err := executor.Generation.Orchestrate(ctx, run.Request, priorReport, priorVerdict)
	if err != nil {
		return InsightsOpportunitiesCandidate{}, fmt.Errorf("orchestration provider: %w", err)
	}
	route := insightsOpportunitiesStaticRoute()
	if err := plan.validate(); err != nil {
		return InsightsOpportunitiesCandidate{}, err
	}
	if err := orchestrationExecution.validate(route.Orchestration); err != nil {
		return InsightsOpportunitiesCandidate{}, err
	}
	report, generationExecution, err := executor.Generation.Generate(ctx, run.Request, plan, revision, priorReport, priorVerdict)
	if err != nil {
		return InsightsOpportunitiesCandidate{}, fmt.Errorf("generation provider: %w", err)
	}
	if err := generationExecution.validate(route.Generation); err != nil {
		return InsightsOpportunitiesCandidate{}, err
	}
	// Bind all request/route/revision metadata from server-owned state before the
	// reviewer sees the candidate. Content IDs, evidence references, claims, and
	// opportunities remain provider output and are validated below.
	report.Schema = insightsOpportunitiesReportSchema
	report.RunID = run.RunID
	report.Revision = revision
	report.RequestDigest = run.Request.RequestDigest
	report.EvidenceSnapshotID = run.Request.EvidenceSnapshot.SnapshotID
	report.EvidenceSnapshotDigest = run.Request.EvidenceSnapshot.SnapshotID
	report.RecallCoverageDigest = run.Request.RecallCoverage.Digest
	report.RecallCoverageStatus = run.Request.RecallCoverage.Status
	report.RecallCoverageReason = run.Request.RecallCoverage.Reason
	report.ContainsUntrustedEvidence = run.Request.EvidenceSnapshot.HasUntrustedEvidence()
	report.ProcessVersion = insightsOpportunitiesProcessVersion
	report.PromptVersion = run.Request.PromptVersion
	report.ArtifactDestination = run.Request.ArtifactDestination
	report.ActualRoute = route
	report.GeneratedAt = executor.now()
	report.ParentReportDigest = ""
	if priorReport != nil {
		report.ParentReportDigest = priorReport.ReportDigest
	}
	if err := validateInsightsGeneratedContent(report, run.Request); err != nil {
		return InsightsOpportunitiesCandidate{}, fmt.Errorf("generated report: %w", err)
	}

	verdict, reviewExecution, err := executor.Review.Review(ctx, run.Request, report)
	if err != nil {
		return InsightsOpportunitiesCandidate{}, fmt.Errorf("review provider: %w", err)
	}
	if err := reviewExecution.validate(route.Review); err != nil {
		return InsightsOpportunitiesCandidate{}, err
	}
	if !validInsightsCriticOutcome(verdict.Outcome) {
		return InsightsOpportunitiesCandidate{}, errors.New("review provider returned an invalid critic outcome")
	}
	report.RunMetadata = InsightsOpportunitiesRunMetadata{
		OrchestrationProvider: orchestrationExecution.Provider,
		GenerationProvider:    generationExecution.Provider,
		ReviewProvider:        reviewExecution.Provider,
		PromptVersion:         run.Request.PromptVersion,
		Retries:               revision - 1,
		CriticOutcome:         verdict.Outcome,
		Usage: map[string]InsightsOpportunitiesUsage{
			route.Orchestration.Purpose: orchestrationExecution.Usage,
			route.Generation.Purpose:    generationExecution.Usage,
			route.Review.Purpose:        reviewExecution.Usage,
		},
	}
	report.Terminal = verdict.Outcome != insightsCriticRevise || revision == 2
	report.ReportDigest = ""
	report.ReportDigest, err = insightsReportDigest(report)
	if err != nil {
		return InsightsOpportunitiesCandidate{}, err
	}
	if err := report.Validate(run.Request); err != nil {
		return InsightsOpportunitiesCandidate{}, fmt.Errorf("generated report: %w", err)
	}

	verdict.Schema = insightsOpportunitiesCriticSchema
	verdict.ReviewerID = run.Principal.ID
	verdict.RunID = run.RunID
	verdict.ReportID = report.ReportID
	verdict.ReportDigest = report.ReportDigest
	verdict.EvidenceSnapshotDigest = run.Request.EvidenceSnapshot.SnapshotID
	verdict.Route = route.Review
	verdict.VerdictDigest = ""
	verdict.VerdictDigest, err = insightsCriticVerdictDigest(verdict)
	if err != nil {
		return InsightsOpportunitiesCandidate{}, err
	}
	if err := verdict.Validate(report, run.Request); err != nil {
		return InsightsOpportunitiesCandidate{}, fmt.Errorf("critic verdict: %w", err)
	}
	return InsightsOpportunitiesCandidate{
		Report: report, Verdict: verdict, Plan: plan, CreatedAt: executor.now(),
		Executions: map[string]InsightsOpportunitiesProviderExecution{
			route.Orchestration.Purpose: orchestrationExecution,
			route.Generation.Purpose:    generationExecution,
			route.Review.Purpose:        reviewExecution,
		},
	}, nil
}

// validateInsightsGeneratedContent runs before the report is handed to the
// reviewer. It keeps invented evidence, malformed claims, and duplicate model
// identifiers out of the second provider prompt even though server-owned
// route/usage/critic fields cannot be finalized until after review.
func validateInsightsGeneratedContent(report InsightsOpportunitiesReport, request InsightsOpportunitiesRequest) error {
	if strings.TrimSpace(report.ReportID) == "" || len(report.Claims) == 0 || len(report.Opportunities) == 0 {
		return errors.New("report requires an id, claims, and opportunities")
	}
	usable, fresh, err := insightsUsableEvidenceIDSets(request.EvidenceSnapshot, request.RecallCoverage)
	if err != nil {
		return err
	}
	claimIDs := make(map[string]bool, len(report.Claims))
	for _, claim := range report.Claims {
		if err := claim.Validate(usable, fresh); err != nil {
			return err
		}
		if claimIDs[claim.ClaimID] {
			return fmt.Errorf("report has duplicate claimId %q", claim.ClaimID)
		}
		claimIDs[claim.ClaimID] = true
	}
	opportunityIDs := make(map[string]bool, len(report.Opportunities))
	for _, opportunity := range report.Opportunities {
		if err := opportunity.Validate(claimIDs, usable); err != nil {
			return err
		}
		if opportunityIDs[opportunity.OpportunityID] {
			return fmt.Errorf("report has duplicate opportunityId %q", opportunity.OpportunityID)
		}
		opportunityIDs[opportunity.OpportunityID] = true
	}
	return nil
}

func (executor *InsightsOpportunitiesExecutor) checkpointCandidate(ctx context.Context, principal ACLPrincipal, run InsightsOpportunitiesRun) error {
	if len(run.Candidates) != len(run.Reports)+1 {
		return ErrInsightsOpportunitiesConflict
	}
	candidate := run.Candidates[len(run.Candidates)-1]
	transition, err := candidate.Verdict.CheckpointAuthorized(ctx, principal, executor.Kernel, executor.Purge, executor.Verifier, candidate.Report, run.Request)
	if err != nil {
		return err
	}
	return executor.Store.checkpoint(run.RunID, InsightsOpportunitiesCriticCheckpoint{
		ReportDigest: candidate.Report.ReportDigest, VerdictDigest: candidate.Verdict.VerdictDigest,
		CheckpointID: transition.CheckpointID, Resumed: transition.Resumed, At: executor.now(),
	})
}

func (executor *InsightsOpportunitiesExecutor) publish(ctx context.Context, principal ACLPrincipal, run InsightsOpportunitiesRun) (InsightsOpportunitiesRun, error) {
	if len(run.Reports) == 0 || run.Verdicts[len(run.Verdicts)-1].Outcome != insightsCriticAccept {
		return InsightsOpportunitiesRun{}, ErrInsightsOpportunitiesConflict
	}
	report := run.Reports[len(run.Reports)-1]
	key := digestBrainString(strings.Join([]string{insightsOpportunitiesProcessID, run.RunID, report.ReportDigest, run.Request.Approval.WorkspaceWriteDigest}, "\x00"))
	target, authority, err := executor.authorizePublication(ctx, principal, run.Request, report, key)
	if err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	if err := authority.validate(target, key); err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	guard := func(guardCtx context.Context, reauthorizeBodies bool) error {
		// These reads intentionally occur at the writer's mutation boundary. The
		// earlier checks are useful preflight only and never authorize a write.
		if err := validateInsightsAuthenticatedPrincipal(principal, run.Request.TenantID, run.Request.PrincipalKind, run.Request.PrincipalID); err != nil {
			return err
		}
		var evidenceErr error
		if reauthorizeBodies {
			evidenceErr = executor.reauthorizeEvidence(guardCtx, principal, run.Request)
		} else {
			evidenceErr = ReauthorizeRetrievalSnapshot(guardCtx, executor.Kernel, executor.Purge, principal, run.Request.EvidenceSnapshot)
		}
		if evidenceErr != nil {
			return fmt.Errorf("publication evidence reauthorization failed: %w", evidenceErr)
		}
		if err := requireInsightsRequirement(guardCtx, executor.Verifier, principal, insightsRequirementActiveOrganizationMember, target); err != nil {
			return err
		}
		if err := requireInsightsAuthorization(guardCtx, executor.Verifier, principal, ACLWrite, target); err != nil {
			return err
		}
		return executor.Verifier.(InsightsOpportunitiesPublicationAuthorizer).VerifyInsightsOpportunitiesPublication(guardCtx, principal, target, key, authority)
	}
	receipt, err := executor.Writer.WriteInsightsOpportunitiesReport(ctx, principal, run.Request, report, authority, key, guard)
	if err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	if err := receipt.validate(key, report); err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	if err := executor.Store.publish(run.RunID, receipt); err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	completed, _ := executor.Store.Run(run.RunID)
	return completed, nil
}

func (executor *InsightsOpportunitiesExecutor) authorizePublication(ctx context.Context, principal ACLPrincipal, request InsightsOpportunitiesRequest, report InsightsOpportunitiesReport, key string) (InsightsOpportunitiesAuthorizationTarget, InsightsOpportunitiesWorkspaceWriteAuthority, error) {
	if err := validateInsightsAuthenticatedPrincipal(principal, request.TenantID, request.PrincipalKind, request.PrincipalID); err != nil {
		return InsightsOpportunitiesAuthorizationTarget{}, InsightsOpportunitiesWorkspaceWriteAuthority{}, err
	}
	if err := executor.reauthorizeEvidence(ctx, principal, request); err != nil {
		return InsightsOpportunitiesAuthorizationTarget{}, InsightsOpportunitiesWorkspaceWriteAuthority{}, fmt.Errorf("publication evidence reauthorization failed: %w", err)
	}
	target := insightsPublicationTarget(principal, request, report)
	authorizer := executor.Verifier.(InsightsOpportunitiesPublicationAuthorizer)
	authority, err := authorizer.AuthorizeInsightsOpportunitiesPublication(ctx, principal, target, key)
	if err != nil {
		return InsightsOpportunitiesAuthorizationTarget{}, InsightsOpportunitiesWorkspaceWriteAuthority{}, err
	}
	if err := authority.validate(target, key); err != nil {
		return InsightsOpportunitiesAuthorizationTarget{}, InsightsOpportunitiesWorkspaceWriteAuthority{}, err
	}
	return target, authority, nil
}

func (executor *InsightsOpportunitiesExecutor) SubmitFeedback(ctx context.Context, principal ACLPrincipal, runID string, feedback InsightsOpportunitiesFeedback) (InsightsOpportunitiesRun, error) {
	if err := executor.ready(); err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	if recorded, err := executor.Store.feedbackRecorded(runID, feedback.IdempotencyKey, feedback.ActionDigest); err != nil {
		return InsightsOpportunitiesRun{}, err
	} else if recorded {
		run, ok := executor.Store.Run(runID)
		if !ok {
			return InsightsOpportunitiesRun{}, os.ErrNotExist
		}
		if feedback.Action == insightsFeedbackRequestRevision && run.Status == insightsRunAwaitingReport {
			return executor.Execute(ctx, principal, run.Request)
		}
		return run, nil
	}
	run, ok := executor.Store.Run(runID)
	if !ok || len(run.Reports) == 0 {
		return InsightsOpportunitiesRun{}, os.ErrNotExist
	}
	report := run.Reports[len(run.Reports)-1]
	if err := feedback.ValidateAuthorized(ctx, principal, executor.Kernel, executor.Purge, executor.Verifier, report, run.Request); err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	if err := executor.Store.appendFeedback(runID, feedback); err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	updated, _ := executor.Store.Run(runID)
	if feedback.Action == insightsFeedbackRequestRevision && updated.Status == insightsRunAwaitingReport {
		return executor.Execute(ctx, principal, updated.Request)
	}
	return updated, nil
}

func (executor *InsightsOpportunitiesExecutor) SubmitPilotReview(ctx context.Context, principal ACLPrincipal, runID string, review InsightsOpportunitiesPilotReview) (InsightsOpportunitiesRun, error) {
	if err := executor.ready(); err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	run, ok := executor.Store.Run(runID)
	if !ok || len(run.Reports) == 0 || (run.Status != insightsRunAccepted && run.Status != insightsRunRejected && run.Status != insightsRunRevisionExhausted) {
		return InsightsOpportunitiesRun{}, os.ErrNotExist
	}
	report := run.Reports[len(run.Reports)-1]
	if err := review.ValidateAuthorized(ctx, principal, executor.Kernel, executor.Purge, executor.Verifier, report, run.Request); err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	if err := executor.Store.appendPilotReview(runID, review); err != nil {
		return InsightsOpportunitiesRun{}, err
	}
	updated, _ := executor.Store.Run(runID)
	return updated, nil
}

type appInsightsOpportunitiesWorkspaceWriter struct {
	app      *kanbanBoardApp
	sources  *MeetingMemoryBrainAdapter
	postgres *PostgresCanonicalStore
}

func insightsWorkspaceArtifactID(runID string) string {
	return "os-artifact-insights-opportunities-" + digestBrainString(strings.TrimSpace(runID))[:24]
}

func renderInsightsOpportunitiesReport(report InsightsOpportunitiesReport) string {
	var body strings.Builder
	body.WriteString("# Insights & Opportunities\n\n")
	body.WriteString(fmt.Sprintf("Evidence snapshot: `%s`  \nCoverage: **%s**", report.EvidenceSnapshotDigest, report.RecallCoverageStatus))
	if report.RecallCoverageReason != "" {
		body.WriteString(" — " + report.RecallCoverageReason)
	}
	body.WriteString("\n\n## Claims\n")
	for _, claim := range report.Claims {
		body.WriteString(fmt.Sprintf("\n- **%s** (%s): %s\n", claim.ClaimID, claim.State, claim.Text))
	}
	body.WriteString("\n## Opportunities\n")
	for _, opportunity := range report.Opportunities {
		body.WriteString(fmt.Sprintf("\n### %s\n\n%s\n\nNext: %s  \nOwner: %s  \nConfidence: %.2f  \nDecision: %s\n", opportunity.OpportunityID, opportunity.ExpectedImpact, opportunity.RecommendedNextAction, opportunity.ProposedOwner, opportunity.Confidence, opportunity.DecisionStatus))
	}
	body.WriteString(fmt.Sprintf("\n---\nRun `%s`, immutable report revision %d, digest `%s`.\n", report.RunID, report.Revision, report.ReportDigest))
	return body.String()
}

func (writer appInsightsOpportunitiesWorkspaceWriter) WriteInsightsOpportunitiesReport(ctx context.Context, principal ACLPrincipal, request InsightsOpportunitiesRequest, report InsightsOpportunitiesReport, authority InsightsOpportunitiesWorkspaceWriteAuthority, idempotencyKey string, guard InsightsOpportunitiesPublicationGuard) (InsightsOpportunitiesWorkspaceWriteReceipt, error) {
	if writer.sources == nil {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, ErrInsightsOpportunitiesUnavailable
	}
	fences, err := writer.sources.ReauthorizeEvidenceWithConsentFences(ctx, principal, request.EvidenceSnapshot.Sources)
	if err != nil {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, err
	}
	var receipt InsightsOpportunitiesWorkspaceWriteReceipt
	commit := func() error {
		return writer.withCanonicalInsightsSourceFence(ctx, request, func() error {
			var commitErr error
			receipt, commitErr = writer.writeInsightsOpportunitiesReportLocked(ctx, principal, request, report, authority, idempotencyKey, guard)
			return commitErr
		})
	}
	if len(fences) == 0 {
		err = commit()
	} else {
		err = currentConsentLaneAuthority().CommitWithFences(ctx, fences, commit)
	}
	return receipt, err
}

// withCanonicalInsightsSourceFence holds shared locks on every exact canonical
// source object while purge/ACL/source mutations use conflicting row locks.
// The local body/workspace conditional commit occurs before those locks are
// released, making the cross-store result linearizable or fail-closed.
func (writer appInsightsOpportunitiesWorkspaceWriter) withCanonicalInsightsSourceFence(ctx context.Context, request InsightsOpportunitiesRequest, commit func() error) error {
	if writer.postgres == nil || writer.postgres.pool == nil || commit == nil {
		return ErrInsightsOpportunitiesUnavailable
	}
	tx, err := writer.postgres.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	sources := append([]RetrievalSnapshotSource(nil), request.EvidenceSnapshot.Sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Evidence.ObjectID < sources[j].Evidence.ObjectID })
	for _, source := range sources {
		expected := source.Evidence
		var contentRevision, aclVersion int64
		var contentDigest []byte
		var deleted bool
		err := tx.QueryRow(ctx, `SELECT content_revision,content_sha256,acl_version,(deleted_at IS NOT NULL)
			FROM objects WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 AND room_id=$4
			AND COALESCE(meeting_id,'')=$5 FOR SHARE`,
			expected.TenantID, expected.SourceFamily, expected.ObjectID, NormalizeCanonicalRoomID(expected.RoomID), expected.SittingID).
			Scan(&contentRevision, &contentDigest, &aclVersion, &deleted)
		if err != nil || deleted || contentRevision != expected.ContentRevision || aclVersion != expected.ACLVersion || hex.EncodeToString(contentDigest) != expected.ContentDigest {
			return ErrRetrievalSnapshotStale
		}
		grantRows, grantErr := tx.Query(ctx, `SELECT grant_id FROM object_grants
			WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 AND acl_version=$4 FOR SHARE`,
			expected.TenantID, expected.SourceFamily, expected.ObjectID, expected.ACLVersion)
		if grantErr != nil {
			return ErrRetrievalSnapshotStale
		}
		grantRows.Close()
	}
	var purgeGeneration int64
	if err := tx.QueryRow(ctx, `SELECT count(*)::bigint FROM purge_ledger WHERE tenant_id=$1`, request.TenantID).Scan(&purgeGeneration); err != nil || purgeGeneration != request.EvidenceSnapshot.PurgeGeneration {
		return ErrRetrievalSnapshotStale
	}
	if err := commit(); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit insights source authority fence: %w", err)
	}
	return nil
}

func (writer appInsightsOpportunitiesWorkspaceWriter) writeInsightsOpportunitiesReportLocked(ctx context.Context, principal ACLPrincipal, request InsightsOpportunitiesRequest, report InsightsOpportunitiesReport, authority InsightsOpportunitiesWorkspaceWriteAuthority, idempotencyKey string, guard InsightsOpportunitiesPublicationGuard) (InsightsOpportunitiesWorkspaceWriteReceipt, error) {
	if writer.app == nil || writer.app.memory == nil || report.ArtifactDestination != request.ArtifactDestination || !strings.HasPrefix(request.ArtifactDestination, "workspace:") {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, ErrInsightsOpportunitiesUnavailable
	}
	target := insightsPublicationTarget(principal, request, report)
	if err := authority.validate(target, idempotencyKey); err != nil {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, err
	}
	if guard == nil {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, errors.New("workspace publication guard is required")
	}
	artifactID := insightsWorkspaceArtifactID(report.RunID)
	body := renderInsightsOpportunitiesReport(report)
	metadata := map[string]string{
		"mode": "research", "title": "Insights & Opportunities", "status": "draft", "published": "false", "visibility": "organization",
		"workflow": insightsOpportunitiesProcessID, "workflowVersion": fmt.Sprint(insightsOpportunitiesProcessVersion), "runId": report.RunID,
		"requestDigest": request.RequestDigest, "reportDigest": report.ReportDigest, "reportRevision": fmt.Sprint(report.Revision),
		"artifactDestination": request.ArtifactDestination, "workspaceWriteDigest": request.Approval.WorkspaceWriteDigest,
		"idempotencyKey": idempotencyKey, "createdBy": principal.ID, "updatedBy": principal.ID,
	}
	if report.Revision < 1 || report.Revision > 2 {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, ErrInsightsOpportunitiesConflict
	}
	receipt, entry, changed, err := writer.app.memory.commitInsightsOpportunitiesArtifact(ctx, request, report, artifactID, body, metadata, idempotencyKey, guard)
	if err == nil && changed {
		emitOSArtifactEvent(entry)
	}
	return receipt, err
}

// commitInsightsOpportunitiesArtifact owns the same lock as every transcript
// body and workspace artifact mutation. Exact source bodies, the destination's
// current revision, fresh ACL/purge/workspace authority, and the JSONL write
// are therefore one conditional local commit inside the canonical row fence.
func (store *meetingMemoryStore) commitInsightsOpportunitiesArtifact(ctx context.Context, request InsightsOpportunitiesRequest, report InsightsOpportunitiesReport, artifactID, body string, metadata map[string]string, idempotencyKey string, guard InsightsOpportunitiesPublicationGuard) (InsightsOpportunitiesWorkspaceWriteReceipt, meetingMemoryEntry, bool, error) {
	if store == nil || guard == nil {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, meetingMemoryEntry{}, false, ErrInsightsOpportunitiesUnavailable
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, source := range request.EvidenceSnapshot.Sources {
		var entry *meetingMemoryEntry
		for index := range store.entries {
			candidate := &store.entries[index]
			if candidate.ID == source.Evidence.ObjectID && candidate.Kind == meetingMemoryKindTranscript {
				entry = candidate
				break
			}
		}
		if entry == nil || memoryEntryHiddenFromRecall(*entry) || digestBrainString(entry.Text) != source.Evidence.ContentDigest ||
			normalizeRoomID(entry.Metadata["roomId"]) != normalizeRoomID(source.Evidence.RoomID) || strings.TrimSpace(entry.Metadata["meetingId"]) != source.Evidence.SittingID {
			return InsightsOpportunitiesWorkspaceWriteReceipt{}, meetingMemoryEntry{}, false, ErrRetrievalSnapshotStale
		}
	}
	if err := guard(ctx, false); err != nil {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, meetingMemoryEntry{}, false, err
	}
	index := -1
	for candidateIndex := range store.entries {
		if store.entries[candidateIndex].ID == artifactID && store.entries[candidateIndex].Kind == meetingMemoryKindOSArtifact {
			index = candidateIndex
			break
		}
	}
	if index >= 0 {
		existing := store.entries[index]
		if existing.Metadata["reportDigest"] == report.ReportDigest && existing.Metadata["idempotencyKey"] == idempotencyKey {
			stamp, parseErr := time.Parse(time.RFC3339Nano, existing.Metadata["insightsWrittenAt"])
			if parseErr != nil {
				stamp = existing.CreatedAt
			}
			return InsightsOpportunitiesWorkspaceWriteReceipt{IdempotencyKey: idempotencyKey, ArtifactID: artifactID, ReportDigest: report.ReportDigest, WrittenAt: stamp.UTC()}, cloneMemoryEntry(existing), false, nil
		}
		if report.Revision != 2 || existing.Metadata["runId"] != report.RunID || existing.Metadata["reportDigest"] != report.ParentReportDigest {
			return InsightsOpportunitiesWorkspaceWriteReceipt{}, meetingMemoryEntry{}, false, ErrInsightsOpportunitiesConflict
		}
		writtenAt := time.Now().UTC()
		metadata["insightsWrittenAt"] = writtenAt.Format(time.RFC3339Nano)
		updated := cloneMemoryEntry(existing)
		if updated.Metadata == nil {
			updated.Metadata = map[string]string{}
		}
		for key, value := range metadata {
			updated.Metadata[key] = value
		}
		updated.Text = normalizeMemoryEntryText(meetingMemoryKindOSArtifact, body)
		bumpArtifactVersionLocked(&updated, existing)
		invalidateArtifactApprovalForRevision(&updated, metadata["status"], metadata[artifactHumanApprovedAtKey])
		updated.Metadata["updatedAt"] = writtenAt.Format(time.RFC3339Nano)
		updated.Metadata[artifactContentDigestMetadataKey] = artifactCapabilityDigest(updated)
		store.entries[index] = updated
		if err := store.rewriteLocked(true); err != nil {
			store.entries[index] = existing
			return InsightsOpportunitiesWorkspaceWriteReceipt{}, meetingMemoryEntry{}, false, err
		}
		return InsightsOpportunitiesWorkspaceWriteReceipt{IdempotencyKey: idempotencyKey, ArtifactID: artifactID, ReportDigest: report.ReportDigest, WrittenAt: writtenAt}, cloneMemoryEntry(updated), true, nil
	}
	if report.Revision != 1 {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, meetingMemoryEntry{}, false, ErrInsightsOpportunitiesConflict
	}
	if _, exists := store.seen[artifactID]; exists {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, meetingMemoryEntry{}, false, ErrInsightsOpportunitiesConflict
	}
	writtenAt := time.Now().UTC()
	metadata["insightsWrittenAt"] = writtenAt.Format(time.RFC3339Nano)
	metadata["tenantId"], metadata["objectId"], metadata["aclVersion"] = canonicalArtifactTenantID(), artifactID, "1"
	if metadata["ownerEmail"] == "" {
		metadata["ownerEmail"] = normalizeAccountEmail(metadata["createdBy"])
	}
	entry := meetingMemoryEntry{ID: artifactID, Kind: meetingMemoryKindOSArtifact, Text: normalizeMemoryEntryText(meetingMemoryKindOSArtifact, body), CreatedAt: writtenAt, Metadata: metadata}
	entry.Metadata["roomId"] = officeRoomID
	entry.Metadata["meetingId"] = store.currentMeetingIDLocked(officeRoomID)
	entry.Metadata[artifactContentDigestMetadataKey] = artifactCapabilityDigest(entry)
	if err := store.appendEntryLineLocked(entry); err != nil {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, meetingMemoryEntry{}, false, err
	}
	store.entries = append(store.entries, entry)
	store.seen[artifactID] = struct{}{}
	return InsightsOpportunitiesWorkspaceWriteReceipt{IdempotencyKey: idempotencyKey, ArtifactID: artifactID, ReportDigest: report.ReportDigest, WrittenAt: writtenAt}, cloneMemoryEntry(entry), true, nil
}

func insightsPublicationTarget(principal ACLPrincipal, request InsightsOpportunitiesRequest, report InsightsOpportunitiesReport) InsightsOpportunitiesAuthorizationTarget {
	return InsightsOpportunitiesAuthorizationTarget{
		Purpose: "workspace_report_publication", TenantID: request.TenantID, ResourceType: "workspace_destination",
		ResourceID: request.ArtifactDestination, ContentDigest: request.Approval.WorkspaceWriteDigest,
		ArtifactDestination: request.ArtifactDestination, ActorID: principal.ID,
		RequestRevisionDigest: request.RequestDigest, EvidenceSnapshotDigest: request.EvidenceSnapshot.SnapshotID,
		RecallCoverageDigest: request.RecallCoverage.Digest, ProcessVersion: insightsOpportunitiesProcessVersion,
		PromptVersion: request.PromptVersion, Action: toolAuthorityWorkspaceWrite,
		WorkspaceWriteDigest: request.Approval.WorkspaceWriteDigest, RunID: request.RunID,
		ReportDigest: report.ReportDigest, ReportRevision: report.Revision, ParentReportDigest: report.ParentReportDigest,
		CriticOutcome: insightsCriticAccept, Terminal: report.Terminal,
	}
}

var insightsOpportunitiesExecutorRuntime struct {
	sync.RWMutex
	executor *InsightsOpportunitiesExecutor
}

func installInsightsOpportunitiesExecutor(executor *InsightsOpportunitiesExecutor) func() {
	insightsOpportunitiesExecutorRuntime.Lock()
	prior := insightsOpportunitiesExecutorRuntime.executor
	insightsOpportunitiesExecutorRuntime.executor = executor
	insightsOpportunitiesExecutorRuntime.Unlock()
	return func() {
		insightsOpportunitiesExecutorRuntime.Lock()
		insightsOpportunitiesExecutorRuntime.executor = prior
		insightsOpportunitiesExecutorRuntime.Unlock()
	}
}

func currentInsightsOpportunitiesExecutor() *InsightsOpportunitiesExecutor {
	insightsOpportunitiesExecutorRuntime.RLock()
	defer insightsOpportunitiesExecutorRuntime.RUnlock()
	return insightsOpportunitiesExecutorRuntime.executor
}

func insightsOpportunitiesExecutorHandler(w http.ResponseWriter, r *http.Request) {
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if !insightsOpportunitiesRequested() {
		writeAuthError(w, http.StatusNotFound, ErrInsightsOpportunitiesDisabled.Error())
		return
	}
	executor := currentInsightsOpportunitiesExecutor()
	if executor == nil {
		writeAuthError(w, http.StatusServiceUnavailable, ErrInsightsOpportunitiesUnavailable.Error())
		return
	}
	if err := executor.ready(); err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, ErrInsightsOpportunitiesDisabled) {
			status = http.StatusNotFound
		}
		writeAuthError(w, status, err.Error())
		return
	}
	base := "/api/insights-opportunities/v1/"
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, base), "/")
	parts := strings.Split(path, "/")
	principalFor := func(tenant string) ACLPrincipal {
		return ACLPrincipal{TenantID: tenant, ID: normalizeAccountEmail(user.Email), Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}}
	}
	if path == "runs" && r.Method == http.MethodPost {
		raw, err := readInsightsHTTPBody(w, r)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		request, err := DecodeInsightsOpportunitiesRequest(raw)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		run, err := executor.Execute(r.Context(), principalFor(request.TenantID), request)
		writeInsightsExecutorResult(w, run, err)
		return
	}
	if len(parts) >= 2 && parts[0] == "runs" {
		header, ok := executor.Store.RunHeader(parts[1])
		if !ok {
			writeAuthError(w, http.StatusNotFound, "run not found")
			return
		}
		principal := principalFor(header.TenantID)
		if len(parts) == 3 {
			target := InsightsOpportunitiesAuthorizationTarget{Purpose: "insights_api_prefilter", TenantID: header.TenantID, ResourceType: "insights_report", ResourceID: header.ReportID, ContentDigest: header.ReportDigest, ArtifactDestination: header.ArtifactDestination, ActorID: principal.ID, RunID: header.RunID}
			if !header.HasReport || requireInsightsRequirement(r.Context(), executor.Verifier, principal, insightsRequirementActiveOrganizationMember, target) != nil {
				writeAuthError(w, http.StatusNotFound, "run not found")
				return
			}
			action := ACLWrite
			if parts[2] == "pilot-reviews" {
				if requireInsightsRequirement(r.Context(), executor.Verifier, principal, insightsRequirementPilotReviewerRole, target) != nil {
					writeAuthError(w, http.StatusNotFound, "run not found")
					return
				}
				action = ACLApprove
			}
			if requireInsightsAuthorization(r.Context(), executor.Verifier, principal, action, target) != nil {
				writeAuthError(w, http.StatusNotFound, "run not found")
				return
			}
		}
		run, ok := executor.Store.Run(parts[1])
		if !ok {
			writeAuthError(w, http.StatusNotFound, "run not found")
			return
		}
		if len(parts) == 2 && r.Method == http.MethodGet {
			if run.Principal.ID != principal.ID || run.Request.TenantID != principal.TenantID {
				writeAuthError(w, http.StatusNotFound, "run not found")
				return
			}
			resourceID, reportDigest, reportRevision := run.RunID, "", 0
			if len(run.Reports) > 0 {
				current := run.Reports[len(run.Reports)-1]
				resourceID, reportDigest, reportRevision = current.ReportID, current.ReportDigest, current.Revision
			}
			target := InsightsOpportunitiesAuthorizationTarget{
				Purpose: "insights_run_read", TenantID: run.Request.TenantID, ResourceType: "insights_report", ResourceID: resourceID,
				ContentDigest: reportDigest, ArtifactDestination: run.Request.ArtifactDestination, ActorID: principal.ID,
				RunID: run.RunID, ReportDigest: reportDigest, ReportRevision: reportRevision,
				RequestRevisionDigest: run.Request.RequestDigest, EvidenceSnapshotDigest: run.Request.EvidenceSnapshot.SnapshotID,
			}
			if requireInsightsRequirement(r.Context(), executor.Verifier, principal, insightsRequirementActiveOrganizationMember, target) != nil ||
				requireInsightsAuthorization(r.Context(), executor.Verifier, principal, ACLReadContent, target) != nil ||
				executor.reauthorizeEvidence(r.Context(), principal, run.Request) != nil {
				writeAuthError(w, http.StatusNotFound, "run not found")
				return
			}
			writeAuthJSON(w, http.StatusOK, run)
			return
		}
		if len(parts) == 3 && r.Method == http.MethodPost && parts[2] == "feedback" {
			raw, err := readInsightsHTTPBody(w, r)
			if err != nil {
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			if len(run.Reports) == 0 {
				writeAuthError(w, http.StatusConflict, "run has no checkpointed report")
				return
			}
			feedback, err := DecodeInsightsOpportunitiesFeedback(raw, run.Reports[len(run.Reports)-1], run.Request)
			if err != nil {
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			updated, err := executor.SubmitFeedback(r.Context(), principal, run.RunID, feedback)
			writeInsightsExecutorResult(w, updated, err)
			return
		}
		if len(parts) == 3 && r.Method == http.MethodPost && parts[2] == "pilot-reviews" {
			raw, err := readInsightsHTTPBody(w, r)
			if err != nil {
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			if len(run.Reports) == 0 {
				writeAuthError(w, http.StatusConflict, "run has no checkpointed report")
				return
			}
			review, err := DecodeInsightsOpportunitiesPilotReview(raw, run.Reports[len(run.Reports)-1], run.Request)
			if err != nil {
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			updated, err := executor.SubmitPilotReview(r.Context(), principal, run.RunID, review)
			writeInsightsExecutorResult(w, updated, err)
			return
		}
	}
	http.NotFound(w, r)
}

func readInsightsHTTPBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	reader := http.MaxBytesReader(w, r.Body, 1<<20)
	defer reader.Close()
	var raw json.RawMessage
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&raw); err != nil || len(raw) == 0 {
		return nil, errors.New("could not read insights opportunities request")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("insights opportunities request has trailing JSON")
		}
		return nil, fmt.Errorf("insights opportunities request has trailing input: %w", err)
	}
	return raw, nil
}

func writeInsightsExecutorResult(w http.ResponseWriter, run InsightsOpportunitiesRun, err error) {
	if err == nil {
		writeAuthJSON(w, http.StatusOK, run)
		return
	}
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, os.ErrNotExist):
		status = http.StatusNotFound
	case errors.Is(err, ErrInsightsOpportunitiesConflict):
		status = http.StatusConflict
	case errors.Is(err, ErrInsightsOpportunitiesDisabled):
		status = http.StatusNotFound
	case errors.Is(err, ErrInsightsOpportunitiesUnavailable):
		status = http.StatusServiceUnavailable
	}
	writeAuthError(w, status, err.Error())
}
