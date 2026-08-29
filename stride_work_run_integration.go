package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const strideWorkRunPathEnvironment = "STRIDE_WORK_RUN_PATH"

// strideWorkRunPath keeps the append-only activity ledger beside the existing
// durable application data by default while allowing an explicit deployment
// path. Construction happens in newKanbanBoardApp, outside every media path.
func strideWorkRunPath() string {
	if path := strings.TrimSpace(os.Getenv(strideWorkRunPathEnvironment)); path != "" {
		return path
	}
	if path := strings.TrimSpace(meetingMemoryPath()); path != "" {
		return filepath.Join(filepath.Dir(path), "stride-work-runs.jsonl")
	}
	return ""
}

func strideWorkRunOutputKind(thread scoutAgentThread) string {
	switch strings.ToLower(strings.TrimSpace(thread.Mode)) {
	case "research":
		return "research"
	case "design", "deck", "presentation", "slides":
		return "presentation"
	}
	return ""
}

func strideWorkRunSpecialist(outputKind string) (agent, outputContract string) {
	if outputKind == "research" {
		return STRIDEWorkAgentResearcher, "research_report"
	}
	if outputKind == "presentation" {
		return STRIDEWorkAgentPresenter, "presentation"
	}
	return "", ""
}

func strideWorkRunSummary(value, fallback string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		value = fallback
	}
	if len(value) > 280 {
		value = strings.TrimSpace(value[:280])
	}
	return value
}

func strideWorkRunDigest(value string) string {
	value = strings.TrimSpace(value)
	if isHexDigest(value) {
		return strings.ToLower(value)
	}
	return sha256Hex([]byte(value))
}

func strideWorkRunEvent(run STRIDECanonicalWorkRun, suffix string, eventType STRIDEAgentRunEventType, actor, summary string, at time.Time) STRIDEAgentRunEvent {
	return STRIDEAgentRunEvent{
		ID:                   run.ID + "-" + suffix,
		TenantID:             run.TenantID,
		RunID:                run.ID,
		IdempotencyKeyDigest: sha256Hex([]byte("stride-work-event/v1\x00" + run.ID + "\x00" + suffix)),
		Type:                 eventType,
		ActorPrincipal:       actor,
		ActivitySummary:      strideWorkRunSummary(summary, "Work activity was committed"),
		OccurredAt:           at.UTC(),
	}
}

func strideWorkRunAssignment(run STRIDECanonicalWorkRun, agent, outputContract, assignedBy, purpose string, at time.Time) (STRIDECanonicalAgentAssignment, error) {
	id := run.ID + "-assignment-" + agent
	capability := STRIDEReference{
		ContractType: STRIDEContractAgentCapabilityManifest,
		ID:           "fixed-agent-" + agent + "-capability",
		Revision:     1,
		Digest:       sha256Hex([]byte("fixed-agent-capability/v1\x00" + agent)),
	}
	snapshot := STRIDEAssignmentAuthoritySnapshot{
		Audience: run.Audience, ACLVersion: run.ACLVersion, PurgeGeneration: run.PurgeGeneration,
		ConsentRevision: 1, SourceHighWater: run.Origin.Digest, SourceRefs: []STRIDEReference{run.Origin},
		CapabilityRef: capability, CapturedAt: at.UTC(),
	}
	assignment := STRIDECanonicalAgentAssignment{
		ID: id, RunID: run.ID,
		IdempotencyKeyDigest: sha256Hex([]byte("stride-work-assignment/v1\x00" + run.ID + "\x00" + agent)),
		Agent:                agent, PurposeSummary: strideWorkRunSummary(purpose, "Own the assigned work"), OutputContract: outputContract,
		AssignedBy: assignedBy, AuthoritySnapshot: snapshot, AssignedAt: at.UTC(),
	}
	fence, err := assignment.FenceDigest()
	if err != nil {
		return STRIDECanonicalAgentAssignment{}, err
	}
	assignment.AuthorityFenceDigest = fence
	return assignment, assignment.Validate()
}

func (app *kanbanBoardApp) appendSTRIDEWorkRunEvent(event STRIDEAgentRunEvent) error {
	if app == nil || app.workRuns == nil {
		if app != nil && app.workRunsErr != nil {
			return app.workRunsErr
		}
		return ErrSTRIDEWorkRunUnavailable
	}
	_, _, err := app.workRuns.Append(event)
	return err
}

// ensureSTRIDEPublicWorkRun is called only after verifyAcceptedPublicConversationWorkDispatch
// proves the source proposal, artifact reservation, and visible root card are
// durable. A retry emits byte-identical commands and therefore appends nothing.
func (app *kanbanBoardApp) ensureSTRIDEPublicWorkRun(thread scoutAgentThread) error {
	outputKind := strideWorkRunOutputKind(thread)
	if outputKind == "" {
		return nil
	}
	metadata := thread.Artifact.Metadata
	requester := normalizeAccountEmail(metadata["requestedBy"])
	destination := strings.TrimSpace(metadata["originId"])
	channel, _, err := app.scoutChatThreadByID(requester, destination)
	if err != nil {
		return err
	}
	audience, aclVersion, err := strideRuntimeChatAudienceAuthority(channel)
	if err != nil {
		return err
	}
	createdBy := strideRuntimePrincipalForEmail(requester)
	if createdBy == "" {
		return ErrSTRIDEWorkRunInvalid
	}
	createdAt := thread.Artifact.CreatedAt.UTC()
	if createdAt.IsZero() {
		return ErrSTRIDEWorkRunInvalid
	}
	projectID := strings.TrimSpace(metadata["projectWorkId"])
	if !validOptionalSTRIDEID(projectID) {
		projectID = ""
	}
	purgeGeneration, _ := strconv.ParseInt(strings.TrimSpace(metadata["purgeGeneration"]), 10, 64)
	if purgeGeneration < 0 {
		purgeGeneration = 0
	}
	run := STRIDECanonicalWorkRun{
		ID: thread.ID, TenantID: canonicalTenantID(),
		IdempotencyKeyDigest: strideWorkRunDigest(metadata["operationBodyDigest"]), OutputKind: outputKind,
		OutcomeSummary: strideWorkRunSummary(thread.Query, "Create the approved "+outputKind+" deliverable"),
		Origin:         STRIDEReference{ContractType: STRIDEContractConversationEvent, ID: metadata["sourceMessageId"], Revision: 1, Digest: strideWorkRunDigest(metadata["sourceMessageDigest"])},
		Destination:    destination, ProjectID: projectID, AccountableAgent: STRIDEWorkAgentScout,
		Audience: audience, ACLVersion: aclVersion, PurgeGeneration: purgeGeneration,
		Phase: STRIDEWorkRunUnderstand, CreatedBy: createdBy, CreatedAt: createdAt,
	}
	created := strideWorkRunEvent(run, "created", STRIDERunCreated, createdBy, "Scout opened the approved "+outputKind+" work", createdAt)
	created.Run = &run
	if err := app.appendSTRIDEWorkRunEvent(created); err != nil {
		return err
	}
	scout, err := strideWorkRunAssignment(run, STRIDEWorkAgentScout, "orchestration", createdBy, "Own the approved work and customer handoff", createdAt.Add(time.Nanosecond))
	if err != nil {
		return err
	}
	scoutAdded := strideWorkRunEvent(run, "assignment-scout", STRIDEAssignmentAdded, createdBy, "Scout took accountability for the work", scout.AssignedAt)
	scoutAdded.Agent, scoutAdded.AssignmentID, scoutAdded.Assignment = scout.Agent, scout.ID, &scout
	if err := app.appendSTRIDEWorkRunEvent(scoutAdded); err != nil {
		return err
	}
	scoutStarted := strideWorkRunEvent(run, "assignment-scout-started", STRIDEAssignmentStarted, createdBy, "Scout started the approved work", createdAt.Add(2*time.Nanosecond))
	scoutStarted.Agent, scoutStarted.AssignmentID = scout.Agent, scout.ID
	if err := app.appendSTRIDEWorkRunEvent(scoutStarted); err != nil {
		return err
	}
	agent, outputContract := strideWorkRunSpecialist(outputKind)
	specialist, err := strideWorkRunAssignment(run, agent, outputContract, STRIDEWorkAgentScout, "Create the approved "+outputKind+" deliverable", createdAt.Add(3*time.Nanosecond))
	if err != nil {
		return err
	}
	specialistAdded := strideWorkRunEvent(run, "assignment-"+agent, STRIDEAssignmentAdded, STRIDEWorkAgentScout, strings.Title(agent)+" was assigned to the work", specialist.AssignedAt)
	specialistAdded.Agent, specialistAdded.AssignmentID, specialistAdded.Assignment = specialist.Agent, specialist.ID, &specialist
	if err := app.appendSTRIDEWorkRunEvent(specialistAdded); err != nil {
		return err
	}
	handoff := strideWorkRunEvent(run, "handoff-"+agent, STRIDEHandoffRequested, STRIDEWorkAgentScout, "Scout handed the committed brief to "+strings.Title(agent), createdAt.Add(4*time.Nanosecond))
	handoff.Agent, handoff.TargetAgent, handoff.AssignmentID = STRIDEWorkAgentScout, agent, scout.ID
	return app.appendSTRIDEWorkRunEvent(handoff)
}

func (app *kanbanBoardApp) strideWorkRunCard(runID string) (STRIDEWorkRunSideCard, bool) {
	if app == nil || app.workRuns == nil {
		return STRIDEWorkRunSideCard{}, false
	}
	card, err := app.workRuns.SideCard(strings.TrimSpace(runID))
	return card, err == nil
}

func (app *kanbanBoardApp) projectSTRIDEWorkRunSideCard(runID string) *STRIDEWorkRunSideCard {
	card, ok := app.strideWorkRunCard(runID)
	if !ok {
		return nil
	}
	return &card
}

func strideWorkRunSpecialistAssignment(card STRIDEWorkRunSideCard) (STRIDECanonicalAgentAssignment, string, bool) {
	agent, _ := strideWorkRunSpecialist(card.Run.OutputKind)
	for _, state := range card.Assignments {
		if state.Assignment.Agent == agent {
			return state.Assignment, state.Status, true
		}
	}
	return STRIDECanonicalAgentAssignment{}, "", false
}

func (app *kanbanBoardApp) recordSTRIDEWorkRunProgress(thread scoutAgentThread, artifact meetingMemoryEntry, progress AgentProgress) {
	card, ok := app.strideWorkRunCard(thread.ID)
	if !ok {
		return
	}
	assignment, status, ok := strideWorkRunSpecialistAssignment(card)
	if !ok {
		return
	}
	at := strideWorkRunArtifactOccurredAt(artifact)
	if at.IsZero() {
		return
	}
	if status == "assigned" {
		started := strideWorkRunEvent(card.Run, "assignment-"+assignment.Agent+"-started", STRIDEAssignmentStarted, assignment.Agent, strings.Title(assignment.Agent)+" started the committed work", at)
		started.Agent, started.AssignmentID = assignment.Agent, assignment.ID
		if err := app.appendSTRIDEWorkRunEvent(started); err != nil && !errors.Is(err, ErrSTRIDEWorkRunConflict) {
			log.Errorf("STRIDE WorkRun progress start failed for %s: %v", thread.ID, err)
			return
		}
	}
	phase := STRIDEWorkRunCreate
	if card.Run.OutputKind == "research" {
		phase = STRIDEWorkRunEvidence
	}
	if strings.Contains(strings.ToLower(progress.Stage+" "+progress.ReviewGate), "review") {
		phase = STRIDEWorkRunReview
	}
	progressDigest := sha256Hex([]byte(strings.Join([]string{progress.Stage, progress.GoalStatus, fmt.Sprint(progress.ProgressPercent), progress.ReviewGate, progress.Note}, "\x00")))
	changed := strideWorkRunEvent(card.Run, "phase-"+string(phase)+"-"+progressDigest[:16], STRIDEPhaseChanged, assignment.Agent, firstNonEmptyString(progress.Note, progress.Stage, strings.Title(string(phase))+" work was committed"), at)
	changed.Agent, changed.AssignmentID, changed.Phase = assignment.Agent, assignment.ID, phase
	if err := app.appendSTRIDEWorkRunEvent(changed); err != nil {
		log.Errorf("STRIDE WorkRun progress failed for %s: %v", thread.ID, err)
	}
}

func strideWorkRunArtifactReference(artifact meetingMemoryEntry) STRIDEReference {
	return STRIDEReference{ContractType: STRIDEContractOutcome, ID: artifact.ID, Revision: int64(artifactVersion(artifact)), Digest: artifactCapabilityDigest(artifact)}
}

func strideWorkRunArtifactParentReference(artifact meetingMemoryEntry) *STRIDEReference {
	version := artifactVersion(artifact)
	if version <= 1 {
		return nil
	}
	for _, prior := range artifactVersionHistory(artifact) {
		if prior.V == version-1 && isHexDigest(prior.ContentDigest) {
			ref := STRIDEReference{ContractType: STRIDEContractOutcome, ID: artifact.ID, Revision: int64(prior.V), Digest: prior.ContentDigest}
			return &ref
		}
	}
	return nil
}

func strideWorkRunArtifactOccurredAt(artifact meetingMemoryEntry) time.Time {
	for _, key := range []string{"completedAt", "updatedAt"} {
		if value, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(artifact.Metadata[key])); err == nil && !value.IsZero() {
			return value.UTC()
		}
	}
	return artifact.CreatedAt.UTC()
}

func (app *kanbanBoardApp) recordSTRIDEWorkRunTerminal(thread scoutAgentThread, artifact meetingMemoryEntry, status string) {
	card, ok := app.strideWorkRunCard(thread.ID)
	if !ok {
		return
	}
	assignment, assignmentStatus, ok := strideWorkRunSpecialistAssignment(card)
	if !ok {
		return
	}
	at := strideWorkRunArtifactOccurredAt(artifact)
	if at.IsZero() {
		return
	}
	if assignmentStatus == "assigned" {
		started := strideWorkRunEvent(card.Run, "assignment-"+assignment.Agent+"-started", STRIDEAssignmentStarted, assignment.Agent, strings.Title(assignment.Agent)+" started the committed work", at)
		started.Agent, started.AssignmentID = assignment.Agent, assignment.ID
		if err := app.appendSTRIDEWorkRunEvent(started); err != nil && !errors.Is(err, ErrSTRIDEWorkRunConflict) {
			log.Errorf("STRIDE WorkRun terminal start failed for %s: %v", thread.ID, err)
			return
		}
	}
	if strings.TrimSpace(status) == "complete" {
		ref := strideWorkRunArtifactReference(artifact)
		lineage := STRIDEArtifactLineageRef{Artifact: ref, Parent: strideWorkRunArtifactParentReference(artifact), Relation: "created", Label: "Committed " + card.Run.OutputKind + " artifact revision"}
		if ref.Revision > 1 {
			lineage.Relation = "revised"
		}
		revised := strideWorkRunEvent(card.Run, fmt.Sprintf("artifact-%s-v%d-%s", artifact.ID, ref.Revision, ref.Digest[:16]), STRIDEArtifactRevised, assignment.Agent, "Committed the exact "+card.Run.OutputKind+" artifact revision", at)
		revised.Agent, revised.AssignmentID, revised.ArtifactLineage = assignment.Agent, assignment.ID, []STRIDEArtifactLineageRef{lineage}
		if err := app.appendSTRIDEWorkRunEvent(revised); err != nil {
			log.Errorf("STRIDE WorkRun artifact lineage failed for %s: %v", thread.ID, err)
			return
		}
		completed := strideWorkRunEvent(card.Run, "assignment-"+assignment.Agent+"-completed-v"+strconv.Itoa(artifactVersion(artifact)), STRIDEAssignmentCompleted, assignment.Agent, strings.Title(assignment.Agent)+" completed the committed work", at)
		completed.Agent, completed.AssignmentID = assignment.Agent, assignment.ID
		if err := app.appendSTRIDEWorkRunEvent(completed); err != nil {
			log.Errorf("STRIDE WorkRun assignment completion failed for %s: %v", thread.ID, err)
			return
		}
		terminal := strideWorkRunEvent(card.Run, "completed-v"+strconv.Itoa(artifactVersion(artifact)), STRIDEAgentRunCompleted, STRIDEWorkAgentScout, "Scout delivered the committed "+card.Run.OutputKind+" artifact", at)
		if err := app.appendSTRIDEWorkRunEvent(terminal); err != nil {
			log.Errorf("STRIDE WorkRun completion failed for %s: %v", thread.ID, err)
		}
		return
	}
	failed := strideWorkRunEvent(card.Run, "assignment-"+assignment.Agent+"-failed-v"+strconv.Itoa(artifactVersion(artifact)), STRIDEAssignmentFailed, assignment.Agent, strings.Title(assignment.Agent)+" could not complete the committed work", at)
	failed.Agent, failed.AssignmentID = assignment.Agent, assignment.ID
	if err := app.appendSTRIDEWorkRunEvent(failed); err != nil {
		log.Errorf("STRIDE WorkRun assignment failure failed for %s: %v", thread.ID, err)
		return
	}
	terminal := strideWorkRunEvent(card.Run, "failed-v"+strconv.Itoa(artifactVersion(artifact)), STRIDEAgentRunFailed, STRIDEWorkAgentScout, "The committed "+card.Run.OutputKind+" work needs attention", at)
	if err := app.appendSTRIDEWorkRunEvent(terminal); err != nil {
		log.Errorf("STRIDE WorkRun failure failed for %s: %v", thread.ID, err)
	}
}

// reconcileSTRIDEWorkRunTerminalGaps runs once during app construction, before
// any room can admit media. The native artifact commit is authoritative; this
// repairs a process loss between that commit and the append-only activity
// ledger without re-running a provider or creating a second Work card.
func (app *kanbanBoardApp) reconcileSTRIDEWorkRunTerminalGaps() error {
	if app == nil || app.memory == nil || app.workRuns == nil {
		return nil
	}
	app.memory.mu.Lock()
	artifacts := make([]meetingMemoryEntry, 0)
	for _, entry := range app.memory.entries {
		if entry.Kind != meetingMemoryKindOSArtifact {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(firstNonEmptyString(entry.Metadata["threadStatus"], entry.Metadata["status"])))
		runID := strings.TrimSpace(firstNonEmptyString(entry.Metadata["latestThreadRun"], entry.Metadata["threadId"]))
		if !oneOf(status, "complete", "error", "failed") || !strideIdentifier(runID) {
			continue
		}
		artifacts = append(artifacts, cloneMemoryEntry(entry))
	}
	app.memory.mu.Unlock()

	var firstErr error
	for _, artifact := range artifacts {
		runID := strings.TrimSpace(firstNonEmptyString(artifact.Metadata["latestThreadRun"], artifact.Metadata["threadId"]))
		card, err := app.workRuns.SideCard(runID)
		if errors.Is(err, ErrSTRIDEWorkRunAbsent) {
			continue
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if terminalSTRIDEWorkRunStatus(card.Status) {
			continue
		}
		thread := scoutAgentThread{
			ID:       runID,
			Mode:     normalizeAgentThreadMode(firstNonEmptyString(artifact.Metadata["mode"], artifact.Kind)),
			Query:    firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["title"], compactAssistantLine(artifact.Text)),
			Artifact: artifact,
		}
		terminalStatus := "error"
		if strings.EqualFold(strings.TrimSpace(firstNonEmptyString(artifact.Metadata["threadStatus"], artifact.Metadata["status"])), "complete") {
			terminalStatus = "complete"
		}
		app.recordSTRIDEWorkRunTerminal(thread, artifact, terminalStatus)
		if refreshed, refreshErr := app.workRuns.SideCard(runID); refreshErr != nil || !terminalSTRIDEWorkRunStatus(refreshed.Status) {
			if firstErr == nil {
				if refreshErr != nil {
					firstErr = refreshErr
				} else {
					firstErr = ErrSTRIDEWorkRunUnavailable
				}
			}
		}
	}
	return firstErr
}
