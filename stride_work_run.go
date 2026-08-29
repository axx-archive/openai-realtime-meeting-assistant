package main

// Durable, event-sourced WorkRun visibility. This ledger is intentionally
// separate from provider execution state: the side card can report only facts
// that survived durable append and deterministic replay.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	STRIDEWorkAgentScout      = "scout"
	STRIDEWorkAgentResearcher = "researcher"
	STRIDEWorkAgentPresenter  = "presenter"

	strideWorkRunMaxEventBytes = 1 << 20
)

var (
	ErrSTRIDEWorkRunInvalid     = errors.New("STRIDE WorkRun event is invalid")
	ErrSTRIDEWorkRunConflict    = errors.New("STRIDE WorkRun event conflicts with durable history")
	ErrSTRIDEWorkRunAbsent      = errors.New("STRIDE WorkRun is absent")
	ErrSTRIDEWorkRunUnavailable = errors.New("STRIDE WorkRun repository is unavailable")
)

type STRIDEWorkRunPhase string

const (
	STRIDEWorkRunUnderstand STRIDEWorkRunPhase = "understand"
	STRIDEWorkRunEvidence   STRIDEWorkRunPhase = "evidence"
	STRIDEWorkRunCreate     STRIDEWorkRunPhase = "create"
	STRIDEWorkRunReview     STRIDEWorkRunPhase = "review"
	STRIDEWorkRunDeliver    STRIDEWorkRunPhase = "deliver"
)

type STRIDECanonicalWorkRun struct {
	ID                   string             `json:"id"`
	TenantID             string             `json:"tenantId"`
	IdempotencyKeyDigest string             `json:"idempotencyKeyDigest"`
	OutputKind           string             `json:"outputKind"`
	OutcomeSummary       string             `json:"outcomeSummary"`
	Origin               STRIDEReference    `json:"origin"`
	Destination          string             `json:"destination"`
	ProjectID            string             `json:"projectId,omitempty"`
	AccountableAgent     string             `json:"accountableAgent"`
	Audience             STRIDEAudience     `json:"audience"`
	ACLVersion           int64              `json:"aclVersion"`
	PurgeGeneration      int64              `json:"purgeGeneration"`
	Phase                STRIDEWorkRunPhase `json:"phase"`
	CreatedBy            string             `json:"createdBy"`
	CreatedAt            time.Time          `json:"createdAt"`
}

func (run STRIDECanonicalWorkRun) Validate() error {
	if !strideIdentifier(run.ID) || !strideIdentifier(run.TenantID) || !isHexDigest(run.IdempotencyKeyDigest) ||
		!oneOf(run.OutputKind, "research", "presentation") || !humanActivitySummary(run.OutcomeSummary) || run.Origin.Validate() != nil ||
		!strideIdentifier(run.Destination) || !validOptionalSTRIDEID(run.ProjectID) || run.AccountableAgent != STRIDEWorkAgentScout ||
		run.Audience.Validate() != nil || run.ACLVersion < 1 || run.PurgeGeneration < 0 || !validSTRIDEWorkRunPhase(run.Phase) ||
		!strideIdentifier(run.CreatedBy) || run.CreatedAt.IsZero() {
		return ErrSTRIDEWorkRunInvalid
	}
	return nil
}

type STRIDEAssignmentAuthoritySnapshot struct {
	Audience        STRIDEAudience    `json:"audience"`
	ACLVersion      int64             `json:"aclVersion"`
	PurgeGeneration int64             `json:"purgeGeneration"`
	ConsentRevision int64             `json:"consentRevision"`
	SourceHighWater string            `json:"sourceHighWater"`
	SourceRefs      []STRIDEReference `json:"sourceRefs"`
	CapabilityRef   STRIDEReference   `json:"capabilityRef"`
	CapturedAt      time.Time         `json:"capturedAt"`
}

func (snapshot STRIDEAssignmentAuthoritySnapshot) Validate() error {
	if snapshot.Audience.Validate() != nil || snapshot.ACLVersion < 1 || snapshot.PurgeGeneration < 0 || snapshot.ConsentRevision < 1 ||
		!isHexDigest(snapshot.SourceHighWater) || !validateSTRIDERefs(snapshot.SourceRefs) || snapshot.CapabilityRef.Validate() != nil || snapshot.CapturedAt.IsZero() {
		return ErrSTRIDEWorkRunInvalid
	}
	return nil
}

func (snapshot STRIDEAssignmentAuthoritySnapshot) FenceDigest() (string, error) {
	if err := snapshot.Validate(); err != nil {
		return "", err
	}
	return STRIDEContractDigest(snapshot)
}

type STRIDECanonicalAgentAssignment struct {
	ID                   string                            `json:"id"`
	RunID                string                            `json:"runId"`
	IdempotencyKeyDigest string                            `json:"idempotencyKeyDigest"`
	Agent                string                            `json:"agent"`
	PurposeSummary       string                            `json:"purposeSummary"`
	OutputContract       string                            `json:"outputContract"`
	AssignedBy           string                            `json:"assignedBy"`
	AuthoritySnapshot    STRIDEAssignmentAuthoritySnapshot `json:"authoritySnapshot"`
	AuthorityFenceDigest string                            `json:"authorityFenceDigest"`
	AssignedAt           time.Time                         `json:"assignedAt"`
}

func (assignment STRIDECanonicalAgentAssignment) Validate() error {
	wantFence, fenceErr := assignment.FenceDigest()
	if !strideIdentifier(assignment.ID) || !strideIdentifier(assignment.RunID) || !isHexDigest(assignment.IdempotencyKeyDigest) ||
		!validSTRIDEWorkAgent(assignment.Agent) || !humanActivitySummary(assignment.PurposeSummary) ||
		!oneOf(assignment.OutputContract, "orchestration", "research_report", "presentation") || !strideIdentifier(assignment.AssignedBy) ||
		fenceErr != nil || assignment.AuthorityFenceDigest != wantFence || assignment.AssignedAt.IsZero() {
		return ErrSTRIDEWorkRunInvalid
	}
	if assignment.Agent == STRIDEWorkAgentScout && assignment.OutputContract != "orchestration" ||
		assignment.Agent == STRIDEWorkAgentResearcher && assignment.OutputContract != "research_report" ||
		assignment.Agent == STRIDEWorkAgentPresenter && assignment.OutputContract != "presentation" {
		return ErrSTRIDEWorkRunInvalid
	}
	return nil
}

func (assignment STRIDECanonicalAgentAssignment) FenceDigest() (string, error) {
	if assignment.AuthoritySnapshot.Validate() != nil || !strideIdentifier(assignment.ID) || !strideIdentifier(assignment.RunID) ||
		!validSTRIDEWorkAgent(assignment.Agent) || !humanActivitySummary(assignment.PurposeSummary) || !strideIdentifier(assignment.AssignedBy) ||
		!oneOf(assignment.OutputContract, "orchestration", "research_report", "presentation") {
		return "", ErrSTRIDEWorkRunInvalid
	}
	// Bind the authority observation to the exact work and duty so a valid
	// source/ACL snapshot cannot be transplanted to another run or specialist.
	return STRIDEContractDigest(struct {
		AssignmentID      string                            `json:"assignmentId"`
		RunID             string                            `json:"runId"`
		Agent             string                            `json:"agent"`
		PurposeSummary    string                            `json:"purposeSummary"`
		OutputContract    string                            `json:"outputContract"`
		AssignedBy        string                            `json:"assignedBy"`
		AuthoritySnapshot STRIDEAssignmentAuthoritySnapshot `json:"authoritySnapshot"`
	}{assignment.ID, assignment.RunID, assignment.Agent, assignment.PurposeSummary, assignment.OutputContract, assignment.AssignedBy, assignment.AuthoritySnapshot})
}

type STRIDEArtifactLineageRef struct {
	Artifact STRIDEReference  `json:"artifact"`
	Parent   *STRIDEReference `json:"parent,omitempty"`
	Relation string           `json:"relation"`
	Label    string           `json:"label"`
}

func (ref STRIDEArtifactLineageRef) Validate() error {
	if ref.Artifact.Validate() != nil || (ref.Parent != nil && ref.Parent.Validate() != nil) ||
		!oneOf(ref.Relation, "created", "revised", "derived", "delivered") || !humanActivitySummary(ref.Label) {
		return ErrSTRIDEWorkRunInvalid
	}
	if ref.Parent != nil && ref.Parent.ID == ref.Artifact.ID && ref.Parent.Revision >= ref.Artifact.Revision {
		return ErrSTRIDEWorkRunInvalid
	}
	return nil
}

type STRIDEAgentRunEventType string

const (
	STRIDERunCreated               STRIDEAgentRunEventType = "run_created"
	STRIDEAssignmentAdded          STRIDEAgentRunEventType = "assignment_added"
	STRIDEAssignmentStarted        STRIDEAgentRunEventType = "assignment_started"
	STRIDEPhaseChanged             STRIDEAgentRunEventType = "phase_changed"
	STRIDEEvidenceAdded            STRIDEAgentRunEventType = "evidence_added"
	STRIDEQuestionRaised           STRIDEAgentRunEventType = "question_raised"
	STRIDEHumanInputRequested      STRIDEAgentRunEventType = "human_input_requested"
	STRIDEHumanInputReceived       STRIDEAgentRunEventType = "human_input_received"
	STRIDEHandoffRequested         STRIDEAgentRunEventType = "handoff_requested"
	STRIDEArtifactRevised          STRIDEAgentRunEventType = "artifact_revised"
	STRIDEReviewRequested          STRIDEAgentRunEventType = "review_requested"
	STRIDEReviewCompleted          STRIDEAgentRunEventType = "review_completed"
	STRIDEAssignmentCompleted      STRIDEAgentRunEventType = "assignment_completed"
	STRIDEAssignmentFailed         STRIDEAgentRunEventType = "assignment_failed"
	STRIDEAgentRunCompleted        STRIDEAgentRunEventType = "run_completed"
	STRIDEAgentRunFailed           STRIDEAgentRunEventType = "run_failed"
	STRIDEAgentRunCancelled        STRIDEAgentRunEventType = "run_cancelled"
	STRIDEProviderResponseRecorded STRIDEAgentRunEventType = "provider_response_recorded"
	STRIDEMilestoneRecorded        STRIDEAgentRunEventType = "milestone_recorded"
	STRIDEShadowCandidateRecorded  STRIDEAgentRunEventType = "shadow_candidate_recorded"
)

type STRIDEAgentRunEvent struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
	RunID    string `json:"runId"`
	// LedgerSequence orders the shared append log and its hash chain. Sequence
	// is independently contiguous within one run and is the number shown in
	// that run's side card; interleaved work therefore creates no false gaps.
	LedgerSequence       uint64                          `json:"ledgerSequence"`
	Sequence             uint64                          `json:"sequence"`
	IdempotencyKeyDigest string                          `json:"idempotencyKeyDigest"`
	PreviousEventDigest  string                          `json:"previousEventDigest,omitempty"`
	EventDigest          string                          `json:"eventDigest"`
	Type                 STRIDEAgentRunEventType         `json:"type"`
	ActorPrincipal       string                          `json:"actorPrincipal"`
	Agent                string                          `json:"agent,omitempty"`
	TargetAgent          string                          `json:"targetAgent,omitempty"`
	AssignmentID         string                          `json:"assignmentId,omitempty"`
	Phase                STRIDEWorkRunPhase              `json:"phase,omitempty"`
	ActivitySummary      string                          `json:"activitySummary"`
	Run                  *STRIDECanonicalWorkRun         `json:"run,omitempty"`
	Assignment           *STRIDECanonicalAgentAssignment `json:"assignment,omitempty"`
	EvidenceRefs         []STRIDEReference               `json:"evidenceRefs,omitempty"`
	ArtifactLineage      []STRIDEArtifactLineageRef      `json:"artifactLineage,omitempty"`
	ProviderReceipt      *STRIDELeadProviderReceipt      `json:"providerReceipt,omitempty"`
	Milestone            *STRIDELeadMilestone            `json:"milestone,omitempty"`
	OccurredAt           time.Time                       `json:"occurredAt"`
}

func (event STRIDEAgentRunEvent) validateCommand() error {
	if !strideIdentifier(event.ID) || !strideIdentifier(event.TenantID) || !strideIdentifier(event.RunID) || !isHexDigest(event.IdempotencyKeyDigest) ||
		!validSTRIDEAgentRunEventType(event.Type) || !strideIdentifier(event.ActorPrincipal) || !humanActivitySummary(event.ActivitySummary) || event.OccurredAt.IsZero() ||
		(event.Agent != "" && !validSTRIDEWorkAgent(event.Agent)) || (event.TargetAgent != "" && !validSTRIDEWorkAgent(event.TargetAgent)) ||
		!validOptionalSTRIDEID(event.AssignmentID) || (event.Phase != "" && !validSTRIDEWorkRunPhase(event.Phase)) ||
		!validateOptionalSTRIDERefs(event.EvidenceRefs) {
		return ErrSTRIDEWorkRunInvalid
	}
	for _, lineage := range event.ArtifactLineage {
		if lineage.Validate() != nil {
			return ErrSTRIDEWorkRunInvalid
		}
	}
	if event.Type != STRIDERunCreated && event.Run != nil || event.Type != STRIDEAssignmentAdded && event.Assignment != nil ||
		event.Type != STRIDEEvidenceAdded && len(event.EvidenceRefs) > 0 ||
		event.Type != STRIDEArtifactRevised && event.Type != STRIDEShadowCandidateRecorded && len(event.ArtifactLineage) > 0 ||
		event.Type != STRIDEPhaseChanged && event.Phase != "" || event.Type != STRIDEHandoffRequested && event.TargetAgent != "" ||
		event.Type != STRIDEProviderResponseRecorded && event.ProviderReceipt != nil || event.Type != STRIDEMilestoneRecorded && event.Milestone != nil {
		return ErrSTRIDEWorkRunInvalid
	}
	switch event.Type {
	case STRIDERunCreated:
		if event.Run == nil || event.Run.Validate() != nil || event.Run.ID != event.RunID || event.Run.TenantID != event.TenantID || event.Assignment != nil {
			return ErrSTRIDEWorkRunInvalid
		}
	case STRIDEAssignmentAdded:
		if event.Assignment == nil || event.Assignment.Validate() != nil || event.Assignment.RunID != event.RunID || event.Assignment.ID != event.AssignmentID || event.Assignment.Agent != event.Agent ||
			event.Assignment.AssignedBy != event.ActorPrincipal || event.OccurredAt.Before(event.Assignment.AssignedAt) || event.Run != nil {
			return ErrSTRIDEWorkRunInvalid
		}
	case STRIDEPhaseChanged:
		if event.Phase == "" {
			return ErrSTRIDEWorkRunInvalid
		}
	case STRIDEEvidenceAdded:
		if len(event.EvidenceRefs) == 0 {
			return ErrSTRIDEWorkRunInvalid
		}
	case STRIDEHandoffRequested:
		if event.Agent == "" || event.TargetAgent == "" || event.Agent == event.TargetAgent || event.AssignmentID == "" {
			return ErrSTRIDEWorkRunInvalid
		}
	case STRIDEArtifactRevised, STRIDEShadowCandidateRecorded:
		if len(event.ArtifactLineage) == 0 {
			return ErrSTRIDEWorkRunInvalid
		}
	case STRIDEAssignmentStarted, STRIDEAssignmentCompleted, STRIDEAssignmentFailed:
		if event.Agent == "" || event.AssignmentID == "" {
			return ErrSTRIDEWorkRunInvalid
		}
	case STRIDEProviderResponseRecorded:
		if event.Agent == "" || event.AssignmentID == "" || event.ProviderReceipt == nil || event.ProviderReceipt.Validate() != nil ||
			event.ProviderReceipt.RunID != event.RunID || event.ProviderReceipt.AssignmentID != event.AssignmentID || !event.OccurredAt.Equal(event.ProviderReceipt.ObservedAt) {
			return ErrSTRIDEWorkRunInvalid
		}
	case STRIDEMilestoneRecorded:
		if event.Agent == "" || event.AssignmentID == "" || event.Milestone == nil || event.Milestone.Validate() != nil ||
			event.Milestone.RunID != event.RunID || event.Milestone.AssignmentID != event.AssignmentID || !event.OccurredAt.Equal(event.Milestone.CommittedAt) {
			return ErrSTRIDEWorkRunInvalid
		}
	}
	return nil
}

func (event STRIDEAgentRunEvent) validateDurable() error {
	if event.validateCommand() != nil || event.LedgerSequence == 0 || event.Sequence == 0 || !isHexDigest(event.EventDigest) || !validOptionalDigest(event.PreviousEventDigest) {
		return ErrSTRIDEWorkRunInvalid
	}
	digest, err := strideAgentRunEventDigest(event)
	if err != nil || digest != event.EventDigest {
		return ErrSTRIDEWorkRunInvalid
	}
	return nil
}

type STRIDEWorkRunAssignmentState struct {
	Assignment STRIDECanonicalAgentAssignment `json:"assignment"`
	Status     string                         `json:"status"`
}

type STRIDEWorkRunActivity struct {
	Sequence   uint64                  `json:"sequence"`
	Type       STRIDEAgentRunEventType `json:"type"`
	Agent      string                  `json:"agent,omitempty"`
	Summary    string                  `json:"summary"`
	OccurredAt time.Time               `json:"occurredAt"`
}

type STRIDEWorkRunSideCard struct {
	Run             STRIDECanonicalWorkRun         `json:"run"`
	Status          string                         `json:"status"`
	Phase           STRIDEWorkRunPhase             `json:"phase"`
	Assignments     []STRIDEWorkRunAssignmentState `json:"assignments"`
	Evidence        []STRIDEReference              `json:"evidence,omitempty"`
	ArtifactLineage []STRIDEArtifactLineageRef     `json:"artifactLineage,omitempty"`
	Provider        *STRIDELeadProviderReceipt     `json:"provider,omitempty"`
	Milestones      []STRIDELeadMilestone          `json:"milestones,omitempty"`
	Activity        []STRIDEWorkRunActivity        `json:"activity"`
	LastSequence    uint64                         `json:"lastSequence"`
}

type STRIDEWorkRunRepository struct {
	mu          sync.Mutex
	path        string
	events      []STRIDEAgentRunEvent
	idempotency map[string]STRIDEAgentRunEvent
	write       func(string, []byte) error
	poisoned    error
}

func NewSTRIDEWorkRunRepository(path string) (*STRIDEWorkRunRepository, error) {
	repository := &STRIDEWorkRunRepository{
		path: strings.TrimSpace(path), idempotency: map[string]STRIDEAgentRunEvent{},
		write: func(path string, raw []byte) error { return appendFileDurably(path, raw, 0o600) },
	}
	if repository.path == "" {
		return repository, nil
	}
	if err := repository.reloadLocked(); err != nil {
		return nil, err
	}
	return repository, nil
}

func (repository *STRIDEWorkRunRepository) Append(command STRIDEAgentRunEvent) (STRIDEAgentRunEvent, bool, error) {
	if repository == nil || command.LedgerSequence != 0 || command.Sequence != 0 || command.EventDigest != "" || command.PreviousEventDigest != "" || command.validateCommand() != nil {
		return STRIDEAgentRunEvent{}, false, ErrSTRIDEWorkRunInvalid
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.poisoned != nil {
		return STRIDEAgentRunEvent{}, false, fmt.Errorf("%w: %v", ErrSTRIDEWorkRunUnavailable, repository.poisoned)
	}
	if existing, found := repository.idempotency[command.IdempotencyKeyDigest]; found {
		if sameSTRIDEAgentRunCommand(existing, command) {
			return existing, false, nil
		}
		return STRIDEAgentRunEvent{}, false, ErrSTRIDEWorkRunConflict
	}
	projected, err := projectSTRIDEWorkRun(repository.events, command.RunID)
	if command.Type == STRIDERunCreated {
		if err == nil || !errors.Is(err, ErrSTRIDEWorkRunAbsent) {
			return STRIDEAgentRunEvent{}, false, ErrSTRIDEWorkRunConflict
		}
	} else if err != nil {
		return STRIDEAgentRunEvent{}, false, err
	}
	if err := applySTRIDEAgentRunEvent(&projected, command); err != nil {
		return STRIDEAgentRunEvent{}, false, err
	}
	command.LedgerSequence = uint64(len(repository.events) + 1)
	command.Sequence = 1
	for _, event := range repository.events {
		if event.RunID == command.RunID && event.Sequence >= command.Sequence {
			command.Sequence = event.Sequence + 1
		}
	}
	if len(repository.events) > 0 {
		command.PreviousEventDigest = repository.events[len(repository.events)-1].EventDigest
	}
	command.EventDigest, err = strideAgentRunEventDigest(command)
	if err != nil {
		return STRIDEAgentRunEvent{}, false, err
	}
	if repository.path != "" {
		raw, encodeErr := json.Marshal(command)
		if encodeErr != nil {
			return STRIDEAgentRunEvent{}, false, encodeErr
		}
		if repository.write == nil {
			return STRIDEAgentRunEvent{}, false, ErrSTRIDEWorkRunUnavailable
		}
		if persistErr := repository.write(repository.path, append(raw, '\n')); persistErr != nil {
			// An append error may mean no bytes, a complete visible record whose
			// fsync failed, or a partial tail. Keep the pre-append projection and
			// poison this process instance. Restart replay either recognizes the
			// full idempotent event or fails closed on the malformed tail; this
			// instance can never append after uncertain history.
			repository.poisoned = persistErr
			return STRIDEAgentRunEvent{}, false, persistErr
		}
	}
	repository.events = append(repository.events, cloneSTRIDEAgentRunEvent(command))
	repository.idempotency[command.IdempotencyKeyDigest] = cloneSTRIDEAgentRunEvent(command)
	return cloneSTRIDEAgentRunEvent(command), true, nil
}

func (repository *STRIDEWorkRunRepository) SideCard(runID string) (STRIDEWorkRunSideCard, error) {
	if repository == nil || !strideIdentifier(runID) {
		return STRIDEWorkRunSideCard{}, ErrSTRIDEWorkRunInvalid
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.poisoned != nil {
		return STRIDEWorkRunSideCard{}, fmt.Errorf("%w: %v", ErrSTRIDEWorkRunUnavailable, repository.poisoned)
	}
	// Deliberately reduce from the durable event ledger on every read. There is
	// no mutable projection for an execution worker to advance optimistically.
	return projectSTRIDEWorkRun(repository.events, runID)
}

func (repository *STRIDEWorkRunRepository) Events(runID string) ([]STRIDEAgentRunEvent, error) {
	if repository == nil || !strideIdentifier(runID) {
		return nil, ErrSTRIDEWorkRunInvalid
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.poisoned != nil {
		return nil, fmt.Errorf("%w: %v", ErrSTRIDEWorkRunUnavailable, repository.poisoned)
	}
	var result []STRIDEAgentRunEvent
	for _, event := range repository.events {
		if event.RunID == runID {
			result = append(result, cloneSTRIDEAgentRunEvent(event))
		}
	}
	return result, nil
}

func (repository *STRIDEWorkRunRepository) reloadLocked() error {
	events, idempotency, err := loadSTRIDEAgentRunEvents(repository.path)
	if err != nil {
		return err
	}
	repository.events, repository.idempotency = events, idempotency
	return nil
}

func loadSTRIDEAgentRunEvents(path string) ([]STRIDEAgentRunEvent, map[string]STRIDEAgentRunEvent, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, map[string]STRIDEAgentRunEvent{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var events []STRIDEAgentRunEvent
	idempotency := map[string]STRIDEAgentRunEvent{}
	runSequences := map[string]uint64{}
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if errors.Is(readErr, io.EOF) {
				// Every committed event is newline framed. A non-empty EOF tail is
				// an interrupted append even if its JSON happens to be parseable.
				return nil, nil, ErrSTRIDEWorkRunInvalid
			}
			if len(line) > strideWorkRunMaxEventBytes {
				return nil, nil, ErrSTRIDEWorkRunInvalid
			}
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.DisallowUnknownFields()
			var event STRIDEAgentRunEvent
			if err := decoder.Decode(&event); err != nil || ensureJSONEOF(decoder) != nil || event.validateDurable() != nil {
				return nil, nil, ErrSTRIDEWorkRunInvalid
			}
			if event.LedgerSequence != uint64(len(events)+1) || event.Sequence != runSequences[event.RunID]+1 ||
				len(events) == 0 && event.PreviousEventDigest != "" || len(events) > 0 && event.PreviousEventDigest != events[len(events)-1].EventDigest {
				return nil, nil, ErrSTRIDEWorkRunInvalid
			}
			if existing, found := idempotency[event.IdempotencyKeyDigest]; found {
				if !sameSTRIDEAgentRunCommand(existing, event) {
					return nil, nil, ErrSTRIDEWorkRunConflict
				}
				return nil, nil, ErrSTRIDEWorkRunConflict
			}
			candidate := append(append([]STRIDEAgentRunEvent(nil), events...), event)
			if _, err := projectSTRIDEWorkRun(candidate, event.RunID); err != nil {
				return nil, nil, err
			}
			events = candidate
			runSequences[event.RunID] = event.Sequence
			idempotency[event.IdempotencyKeyDigest] = cloneSTRIDEAgentRunEvent(event)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, nil, readErr
		}
	}
	return events, idempotency, nil
}

func projectSTRIDEWorkRun(events []STRIDEAgentRunEvent, runID string) (STRIDEWorkRunSideCard, error) {
	var card STRIDEWorkRunSideCard
	found := false
	for _, event := range events {
		if event.RunID != runID {
			continue
		}
		if event.Type == STRIDERunCreated {
			if found {
				return STRIDEWorkRunSideCard{}, ErrSTRIDEWorkRunConflict
			}
			found = true
		}
		if err := applySTRIDEAgentRunEvent(&card, event); err != nil {
			return STRIDEWorkRunSideCard{}, err
		}
	}
	if !found {
		return STRIDEWorkRunSideCard{}, ErrSTRIDEWorkRunAbsent
	}
	sort.Slice(card.Assignments, func(i, j int) bool {
		return card.Assignments[i].Assignment.AssignedAt.Before(card.Assignments[j].Assignment.AssignedAt)
	})
	return card, nil
}

func applySTRIDEAgentRunEvent(card *STRIDEWorkRunSideCard, event STRIDEAgentRunEvent) error {
	if card == nil || event.validateCommand() != nil {
		return ErrSTRIDEWorkRunInvalid
	}
	if event.Type == STRIDERunCreated {
		if card.Run.ID != "" {
			return ErrSTRIDEWorkRunConflict
		}
		card.Run = *event.Run
		card.Status, card.Phase = "queued", event.Run.Phase
	} else if card.Run.ID == "" || card.Run.ID != event.RunID || card.Run.TenantID != event.TenantID ||
		terminalSTRIDEWorkRunStatus(card.Status) && !oneOf(string(event.Type), string(STRIDEProviderResponseRecorded), string(STRIDEMilestoneRecorded), string(STRIDEShadowCandidateRecorded)) {
		return ErrSTRIDEWorkRunConflict
	}
	assignmentIndex := func(id string) int {
		for index := range card.Assignments {
			if card.Assignments[index].Assignment.ID == id {
				return index
			}
		}
		return -1
	}
	if event.Type != STRIDEAssignmentAdded && event.Agent != "" {
		index := assignmentIndex(event.AssignmentID)
		if index < 0 || card.Assignments[index].Assignment.Agent != event.Agent {
			return ErrSTRIDEWorkRunConflict
		}
	}
	switch event.Type {
	case STRIDERunCreated:
	case STRIDEAssignmentAdded:
		if assignmentIndex(event.AssignmentID) >= 0 || !sameSTRIDEWorkRunAudience(event.Assignment.AuthoritySnapshot.Audience, card.Run.Audience) ||
			event.Assignment.AuthoritySnapshot.ACLVersion != card.Run.ACLVersion || event.Assignment.AuthoritySnapshot.PurgeGeneration != card.Run.PurgeGeneration {
			return ErrSTRIDEWorkRunConflict
		}
		card.Assignments = append(card.Assignments, STRIDEWorkRunAssignmentState{Assignment: *event.Assignment, Status: "assigned"})
	case STRIDEAssignmentStarted:
		index := assignmentIndex(event.AssignmentID)
		if index < 0 || card.Assignments[index].Assignment.Agent != event.Agent || card.Assignments[index].Status != "assigned" {
			return ErrSTRIDEWorkRunConflict
		}
		card.Assignments[index].Status, card.Status = "running", "running"
	case STRIDEAssignmentCompleted, STRIDEAssignmentFailed:
		index := assignmentIndex(event.AssignmentID)
		if index < 0 || card.Assignments[index].Assignment.Agent != event.Agent || !oneOf(card.Assignments[index].Status, "assigned", "running") {
			return ErrSTRIDEWorkRunConflict
		}
		if event.Type == STRIDEAssignmentCompleted {
			card.Assignments[index].Status = "completed"
		} else {
			card.Assignments[index].Status = "failed"
			card.Status = "blocked"
		}
	case STRIDEPhaseChanged:
		card.Phase = event.Phase
	case STRIDEEvidenceAdded:
		card.Evidence = appendUniqueSTRIDEReferences(card.Evidence, event.EvidenceRefs...)
	case STRIDEHumanInputRequested:
		card.Status = "awaiting_input"
	case STRIDEHumanInputReceived:
		card.Status = "running"
	case STRIDEReviewRequested:
		card.Status, card.Phase = "awaiting_review", STRIDEWorkRunReview
	case STRIDEReviewCompleted:
		card.Status = "running"
	case STRIDEArtifactRevised, STRIDEShadowCandidateRecorded:
		card.ArtifactLineage = append(card.ArtifactLineage, event.ArtifactLineage...)
	case STRIDEAgentRunCompleted:
		if len(card.ArtifactLineage) == 0 {
			return ErrSTRIDEWorkRunConflict
		}
		card.Status, card.Phase = "completed", STRIDEWorkRunDeliver
	case STRIDEAgentRunFailed:
		card.Status = "failed"
	case STRIDEAgentRunCancelled:
		card.Status = "cancelled"
	case STRIDEHandoffRequested:
		targetAssigned := false
		for _, assignment := range card.Assignments {
			if assignment.Assignment.Agent == event.TargetAgent && !oneOf(assignment.Status, "failed", "cancelled") {
				targetAssigned = true
				break
			}
		}
		if !targetAssigned {
			return ErrSTRIDEWorkRunConflict
		}
	case STRIDEQuestionRaised:
	case STRIDEProviderResponseRecorded:
		assignment := card.Assignments[assignmentIndex(event.AssignmentID)].Assignment
		if assignment.Agent != STRIDEWorkAgentScout || assignment.OutputContract != "orchestration" {
			return ErrSTRIDEWorkRunConflict
		}
		if card.Provider != nil {
			prior := *card.Provider
			if prior.ResponseID == event.ProviderReceipt.ResponseID {
				if !validSTRIDELeadProviderTransition(prior.Status, event.ProviderReceipt.Status) || prior.RequestDigest != event.ProviderReceipt.RequestDigest ||
					prior.AssignmentID != event.ProviderReceipt.AssignmentID || prior.ConversationID != event.ProviderReceipt.ConversationID ||
					prior.PreviousResponseID != event.ProviderReceipt.PreviousResponseID || prior.Attempt != event.ProviderReceipt.Attempt ||
					prior.SpendBoundaryDigest != event.ProviderReceipt.SpendBoundaryDigest || prior.SourceManifestDigest != event.ProviderReceipt.SourceManifestDigest ||
					prior.AuthorityFenceDigest != event.ProviderReceipt.AuthorityFenceDigest || prior.ToolAdmissionDigest != event.ProviderReceipt.ToolAdmissionDigest {
					return ErrSTRIDEWorkRunConflict
				}
			} else {
				next := event.ProviderReceipt
				if !leadProviderTerminal(prior.Status) || next.AssignmentID != prior.AssignmentID || next.Attempt != prior.Attempt+1 || next.Recovery != "resumed" {
					return ErrSTRIDEWorkRunConflict
				}
				if next.RequestDigest == prior.RequestDigest {
					if prior.Status == "completed" || next.SourceManifestDigest != prior.SourceManifestDigest ||
						next.AuthorityFenceDigest != prior.AuthorityFenceDigest || next.ToolAdmissionDigest != prior.ToolAdmissionDigest {
						return ErrSTRIDEWorkRunConflict
					}
				} else if prior.Status != "completed" || next.PreviousResponseID != prior.ResponseID &&
					(prior.ConversationID == "" || next.ConversationID != prior.ConversationID) {
					return ErrSTRIDEWorkRunConflict
				}
			}
		}
		copy := *event.ProviderReceipt
		card.Provider = &copy
	case STRIDEMilestoneRecorded:
		assignment := card.Assignments[assignmentIndex(event.AssignmentID)].Assignment
		if assignment.Agent != STRIDEWorkAgentScout || assignment.OutputContract != "orchestration" {
			return ErrSTRIDEWorkRunConflict
		}
		for _, milestone := range card.Milestones {
			if milestone.Kind == event.Milestone.Kind {
				return ErrSTRIDEWorkRunConflict
			}
		}
		card.Milestones = append(card.Milestones, *event.Milestone)
	}
	card.Activity = append(card.Activity, STRIDEWorkRunActivity{Sequence: event.Sequence, Type: event.Type, Agent: event.Agent, Summary: event.ActivitySummary, OccurredAt: event.OccurredAt})
	if event.Sequence > card.LastSequence {
		card.LastSequence = event.Sequence
	}
	return nil
}

func appendUniqueSTRIDEReferences(values []STRIDEReference, additions ...STRIDEReference) []STRIDEReference {
	seen := map[string]bool{}
	for _, value := range values {
		seen[fmt.Sprintf("%s|%s|%d|%s", value.ContractType, value.ID, value.Revision, value.Digest)] = true
	}
	for _, value := range additions {
		key := fmt.Sprintf("%s|%s|%d|%s", value.ContractType, value.ID, value.Revision, value.Digest)
		if !seen[key] {
			values, seen[key] = append(values, value), true
		}
	}
	return values
}

func strideAgentRunEventDigest(event STRIDEAgentRunEvent) (string, error) {
	event.EventDigest = ""
	return STRIDEContractDigest(event)
}

func sameSTRIDEAgentRunCommand(left, right STRIDEAgentRunEvent) bool {
	left.LedgerSequence, right.LedgerSequence = 0, 0
	left.Sequence, right.Sequence = 0, 0
	left.PreviousEventDigest, right.PreviousEventDigest = "", ""
	left.EventDigest, right.EventDigest = "", ""
	leftRaw, leftErr := canonicalJSON(left)
	rightRaw, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func cloneSTRIDEAgentRunEvent(event STRIDEAgentRunEvent) STRIDEAgentRunEvent {
	raw, _ := json.Marshal(event)
	var cloned STRIDEAgentRunEvent
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func humanActivitySummary(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 280 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validSTRIDEWorkAgent(agent string) bool {
	return oneOf(agent, STRIDEWorkAgentScout, STRIDEWorkAgentResearcher, STRIDEWorkAgentPresenter)
}

func sameSTRIDEWorkRunAudience(left, right STRIDEAudience) bool {
	leftDigest, leftErr := STRIDEContractDigest(left)
	rightDigest, rightErr := STRIDEContractDigest(right)
	return leftErr == nil && rightErr == nil && leftDigest == rightDigest
}

func validSTRIDEWorkRunPhase(phase STRIDEWorkRunPhase) bool {
	return phase == STRIDEWorkRunUnderstand || phase == STRIDEWorkRunEvidence || phase == STRIDEWorkRunCreate || phase == STRIDEWorkRunReview || phase == STRIDEWorkRunDeliver
}

func validSTRIDEAgentRunEventType(eventType STRIDEAgentRunEventType) bool {
	switch eventType {
	case STRIDERunCreated, STRIDEAssignmentAdded, STRIDEAssignmentStarted, STRIDEPhaseChanged, STRIDEEvidenceAdded,
		STRIDEQuestionRaised, STRIDEHumanInputRequested, STRIDEHumanInputReceived, STRIDEHandoffRequested,
		STRIDEArtifactRevised, STRIDEReviewRequested, STRIDEReviewCompleted, STRIDEAssignmentCompleted,
		STRIDEAssignmentFailed, STRIDEAgentRunCompleted, STRIDEAgentRunFailed, STRIDEAgentRunCancelled,
		STRIDEProviderResponseRecorded, STRIDEMilestoneRecorded, STRIDEShadowCandidateRecorded:
		return true
	}
	return false
}

func validSTRIDELeadProviderTransition(before, after string) bool {
	if before == after {
		return true
	}
	switch before {
	case "queued":
		return oneOf(after, "in_progress", "completed", "incomplete", "failed", "cancelled")
	case "in_progress":
		return oneOf(after, "completed", "incomplete", "failed", "cancelled")
	default:
		return false
	}
}

func terminalSTRIDEWorkRunStatus(status string) bool {
	return oneOf(status, "completed", "failed", "cancelled")
}
