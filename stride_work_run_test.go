package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func strideWorkRunTestReference(kind STRIDEContractType, id, digestChar string, revision int64) STRIDEReference {
	return STRIDEReference{ContractType: kind, ID: id, Revision: revision, Digest: strings.Repeat(digestChar, 64)}
}

func strideWorkRunTestRun(at time.Time) STRIDECanonicalWorkRun {
	return STRIDECanonicalWorkRun{
		ID: "work-run-1", TenantID: "tenant-1", IdempotencyKeyDigest: strings.Repeat("1", 64), OutputKind: "presentation",
		OutcomeSummary: "Create an evidence-backed launch presentation", Origin: strideWorkRunTestReference(STRIDEContractConversationEvent, "conversation-1", "2", 1),
		Destination: "thread-1", ProjectID: "project-1", AccountableAgent: STRIDEWorkAgentScout,
		Audience: STRIDEAudience{Visibility: "project", Principals: []string{"user-aj", "user-sam"}}, ACLVersion: 3, PurgeGeneration: 2,
		Phase: STRIDEWorkRunUnderstand, CreatedBy: "user-aj", CreatedAt: at,
	}
}

func strideWorkRunTestAssignment(t *testing.T, run STRIDECanonicalWorkRun, id, agent, output, digestChar string, at time.Time) STRIDECanonicalAgentAssignment {
	t.Helper()
	snapshot := STRIDEAssignmentAuthoritySnapshot{
		Audience: run.Audience, ACLVersion: run.ACLVersion, PurgeGeneration: run.PurgeGeneration, ConsentRevision: 4,
		SourceHighWater: strings.Repeat("3", 64), SourceRefs: []STRIDEReference{run.Origin},
		CapabilityRef: strideWorkRunTestReference(STRIDEContractAgentCapabilityManifest, "capability-"+agent, "4", 1), CapturedAt: at,
	}
	assignment := STRIDECanonicalAgentAssignment{
		ID: id, RunID: run.ID, IdempotencyKeyDigest: strings.Repeat(digestChar, 64), Agent: agent,
		PurposeSummary: "Own the " + output + " stage", OutputContract: output, AssignedBy: "user-aj",
		AuthoritySnapshot: snapshot, AssignedAt: at,
	}
	fence, err := assignment.FenceDigest()
	if err != nil {
		t.Fatal(err)
	}
	assignment.AuthorityFenceDigest = fence
	return assignment
}

func strideWorkRunTestCommand(run STRIDECanonicalWorkRun, id, digestChar string, eventType STRIDEAgentRunEventType, summary string, at time.Time) STRIDEAgentRunEvent {
	return STRIDEAgentRunEvent{
		ID: id, TenantID: run.TenantID, RunID: run.ID, IdempotencyKeyDigest: strings.Repeat(digestChar, 64), Type: eventType,
		ActorPrincipal: "user-aj", ActivitySummary: summary, OccurredAt: at,
	}
}

func appendSTRIDEWorkRunTestEvent(t *testing.T, repository *STRIDEWorkRunRepository, event STRIDEAgentRunEvent) STRIDEAgentRunEvent {
	t.Helper()
	stored, appended, err := repository.Append(event)
	if err != nil || !appended {
		t.Fatalf("append %s appended=%v err=%v", event.Type, appended, err)
	}
	return stored
}

func TestSTRIDEWorkRunContractsFenceAuthorityAndFixedRoster(t *testing.T) {
	at := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	run := strideWorkRunTestRun(at)
	if err := run.Validate(); err != nil {
		t.Fatal(err)
	}
	assignment := strideWorkRunTestAssignment(t, run, "assignment-research", STRIDEWorkAgentResearcher, "research_report", "5", at)
	if err := assignment.Validate(); err != nil {
		t.Fatal(err)
	}
	critic := assignment
	critic.Agent = "critic"
	if !errors.Is(critic.Validate(), ErrSTRIDEWorkRunInvalid) {
		t.Fatal("critic became an addressable WorkRun agent")
	}
	stale := assignment
	stale.AuthoritySnapshot.ACLVersion++
	if !errors.Is(stale.Validate(), ErrSTRIDEWorkRunInvalid) {
		t.Fatal("assignment accepted an authority snapshot that no longer matched its fence")
	}
	wrongOutput := assignment
	wrongOutput.OutputContract = "presentation"
	if !errors.Is(wrongOutput.Validate(), ErrSTRIDEWorkRunInvalid) {
		t.Fatal("researcher accepted the presenter output contract")
	}
	transplanted := assignment
	transplanted.RunID = "work-run-other"
	if !errors.Is(transplanted.Validate(), ErrSTRIDEWorkRunInvalid) {
		t.Fatal("authority fence was transplantable to another run")
	}
}

func TestSTRIDEWorkRunSideCardReplaysTypedAgentActivity(t *testing.T) {
	at := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	run := strideWorkRunTestRun(at)
	repository, err := NewSTRIDEWorkRunRepository("")
	if err != nil {
		t.Fatal(err)
	}
	created := strideWorkRunTestCommand(run, "event-created", "a", STRIDERunCreated, "Scout opened the presentation run", at)
	created.Run = &run
	appendSTRIDEWorkRunTestEvent(t, repository, created)

	researcher := strideWorkRunTestAssignment(t, run, "assignment-research", STRIDEWorkAgentResearcher, "research_report", "5", at.Add(time.Second))
	assigned := strideWorkRunTestCommand(run, "event-research-assigned", "b", STRIDEAssignmentAdded, "Researcher was assigned to verify the launch evidence", at.Add(time.Second))
	assigned.Agent, assigned.AssignmentID, assigned.Assignment = researcher.Agent, researcher.ID, &researcher
	appendSTRIDEWorkRunTestEvent(t, repository, assigned)
	started := strideWorkRunTestCommand(run, "event-research-started", "c", STRIDEAssignmentStarted, "Researcher started checking the source record", at.Add(2*time.Second))
	started.Agent, started.AssignmentID = researcher.Agent, researcher.ID
	appendSTRIDEWorkRunTestEvent(t, repository, started)
	evidence := strideWorkRunTestCommand(run, "event-evidence", "d", STRIDEEvidenceAdded, "Researcher added the approved market brief", at.Add(3*time.Second))
	evidence.Agent, evidence.AssignmentID = researcher.Agent, researcher.ID
	evidence.EvidenceRefs = []STRIDEReference{strideWorkRunTestReference(STRIDEContractAnalysisProjection, "evidence-1", "6", 1)}
	appendSTRIDEWorkRunTestEvent(t, repository, evidence)

	presenter := strideWorkRunTestAssignment(t, run, "assignment-present", STRIDEWorkAgentPresenter, "presentation", "7", at.Add(4*time.Second))
	presenterEvent := strideWorkRunTestCommand(run, "event-presenter-assigned", "e", STRIDEAssignmentAdded, "Presenter was assigned to build the narrative", at.Add(4*time.Second))
	presenterEvent.Agent, presenterEvent.AssignmentID, presenterEvent.Assignment = presenter.Agent, presenter.ID, &presenter
	appendSTRIDEWorkRunTestEvent(t, repository, presenterEvent)
	handoff := strideWorkRunTestCommand(run, "event-handoff", "f", STRIDEHandoffRequested, "Researcher handed the verified evidence to Presenter", at.Add(5*time.Second))
	handoff.Agent, handoff.TargetAgent, handoff.AssignmentID = researcher.Agent, presenter.Agent, researcher.ID
	appendSTRIDEWorkRunTestEvent(t, repository, handoff)
	artifact := strideWorkRunTestReference(STRIDEContractArtifactDisposition, "deck-1", "8", 2)
	revised := strideWorkRunTestCommand(run, "event-artifact", "9", STRIDEArtifactRevised, "Presenter produced revision two of the launch deck", at.Add(6*time.Second))
	revised.Agent, revised.AssignmentID = presenter.Agent, presenter.ID
	revised.ArtifactLineage = []STRIDEArtifactLineageRef{{Artifact: artifact, Parent: ptrSTRIDEWorkRunReference(strideWorkRunTestReference(STRIDEContractArtifactDisposition, "deck-1", "7", 1)), Relation: "revised", Label: "Launch presentation revision two"}}
	appendSTRIDEWorkRunTestEvent(t, repository, revised)
	completed := strideWorkRunTestCommand(run, "event-completed", "0", STRIDEAgentRunCompleted, "Scout delivered the reviewed presentation", at.Add(7*time.Second))
	appendSTRIDEWorkRunTestEvent(t, repository, completed)

	card, err := repository.SideCard(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card.Status != "completed" || card.Phase != STRIDEWorkRunDeliver || len(card.Assignments) != 2 || len(card.Evidence) != 1 || len(card.ArtifactLineage) != 1 || len(card.Activity) != 8 {
		t.Fatalf("side card=%+v", card)
	}
	if card.Activity[5].Summary != "Researcher handed the verified evidence to Presenter" || card.Activity[5].Agent != STRIDEWorkAgentResearcher {
		t.Fatalf("human-visible handoff=%+v", card.Activity[5])
	}

	replayed, appended, err := repository.Append(handoff)
	if err != nil || appended || replayed.ID != "event-handoff" {
		t.Fatalf("idempotent replay appended=%v event=%+v err=%v", appended, replayed, err)
	}
	conflict := handoff
	conflict.ActivitySummary = "A different event tried to reuse the same idempotency key"
	if _, _, err := repository.Append(conflict); !errors.Is(err, ErrSTRIDEWorkRunConflict) {
		t.Fatalf("idempotency collision error=%v", err)
	}
}

func TestSTRIDEWorkRunFileRepositoryRestartsFromDurableEvents(t *testing.T) {
	at := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	run := strideWorkRunTestRun(at)
	path := filepath.Join(t.TempDir(), "work-runs.jsonl")
	repository, err := NewSTRIDEWorkRunRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	created := strideWorkRunTestCommand(run, "event-created", "a", STRIDERunCreated, "Scout opened the research run", at)
	created.Run = &run
	appendSTRIDEWorkRunTestEvent(t, repository, created)
	assignment := strideWorkRunTestAssignment(t, run, "assignment-scout", STRIDEWorkAgentScout, "orchestration", "5", at.Add(time.Second))
	assigned := strideWorkRunTestCommand(run, "event-assigned", "b", STRIDEAssignmentAdded, "Scout accepted accountability for the run", at.Add(time.Second))
	assigned.Agent, assigned.AssignmentID, assigned.Assignment = assignment.Agent, assignment.ID, &assignment
	appendSTRIDEWorkRunTestEvent(t, repository, assigned)
	artifactEvent := strideWorkRunTestCommand(run, "event-artifact", "c", STRIDEArtifactRevised, "Scout attached the completed research artifact", at.Add(2*time.Second))
	artifactEvent.Agent, artifactEvent.AssignmentID = assignment.Agent, assignment.ID
	artifactEvent.ArtifactLineage = []STRIDEArtifactLineageRef{{Artifact: strideWorkRunTestReference(STRIDEContractArtifactDisposition, "report-1", "d", 1), Relation: "created", Label: "Launch research report"}}
	appendSTRIDEWorkRunTestEvent(t, repository, artifactEvent)
	terminal := strideWorkRunTestCommand(run, "event-terminal", "e", STRIDEAgentRunCompleted, "Scout delivered the research report", at.Add(3*time.Second))
	appendSTRIDEWorkRunTestEvent(t, repository, terminal)
	want, err := repository.SideCard(run.ID)
	if err != nil {
		t.Fatal(err)
	}

	restarted, err := NewSTRIDEWorkRunRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.SideCard(run.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("restart card=%+v want=%+v err=%v", got, want, err)
	}
	events, eventsErr := restarted.Events(run.ID)
	if eventsErr != nil || len(events) != 4 || events[0].EventDigest == "" || events[3].PreviousEventDigest != events[2].EventDigest {
		t.Fatalf("durable event chain=%+v", events)
	}
}

func TestSTRIDEWorkRunNeverProjectsFailedAppendOptimistically(t *testing.T) {
	at := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	run := strideWorkRunTestRun(at)
	repository, err := NewSTRIDEWorkRunRepository("")
	if err != nil {
		t.Fatal(err)
	}
	repository.path = filepath.Join(t.TempDir(), "work-runs.jsonl")
	repository.write = func(string, []byte) error { return errors.New("injected fsync failure") }
	created := strideWorkRunTestCommand(run, "event-created", "a", STRIDERunCreated, "Scout opened the work run", at)
	created.Run = &run
	if _, appended, err := repository.Append(created); err == nil || appended {
		t.Fatalf("failed durable append appended=%v err=%v", appended, err)
	}
	if _, err := repository.SideCard(run.ID); !errors.Is(err, ErrSTRIDEWorkRunUnavailable) {
		t.Fatalf("failed append created optimistic side card: %v", err)
	}
	if _, _, err := repository.Append(created); !errors.Is(err, ErrSTRIDEWorkRunUnavailable) {
		t.Fatalf("poisoned repository accepted a retry: %v", err)
	}
}

func TestSTRIDEWorkRunAmbiguousFullAppendRequiresRestartThenDedupes(t *testing.T) {
	at := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	run := strideWorkRunTestRun(at)
	path := filepath.Join(t.TempDir(), "work-runs.jsonl")
	repository, err := NewSTRIDEWorkRunRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	repository.write = func(path string, raw []byte) error {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return err
		}
		return errors.New("injected ambiguous sync failure")
	}
	created := strideWorkRunTestCommand(run, "event-created", "a", STRIDERunCreated, "Scout opened the work run", at)
	created.Run = &run
	if _, appended, err := repository.Append(created); err == nil || appended {
		t.Fatalf("ambiguous append appended=%v err=%v", appended, err)
	}
	if _, err := repository.SideCard(run.ID); !errors.Is(err, ErrSTRIDEWorkRunUnavailable) {
		t.Fatalf("ambiguous current-process projection error=%v", err)
	}

	restarted, err := NewSTRIDEWorkRunRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	card, err := restarted.SideCard(run.ID)
	if err != nil || card.LastSequence != 1 {
		t.Fatalf("restart did not recover full landed event: card=%+v err=%v", card, err)
	}
	stored, appended, err := restarted.Append(created)
	if err != nil || appended || stored.ID != created.ID {
		t.Fatalf("restart idempotency appended=%v stored=%+v err=%v", appended, stored, err)
	}
}

func TestSTRIDEWorkRunPartialTailPoisonsAndFailsRestartClosed(t *testing.T) {
	at := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	run := strideWorkRunTestRun(at)
	path := filepath.Join(t.TempDir(), "work-runs.jsonl")
	repository, err := NewSTRIDEWorkRunRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	repository.write = func(path string, raw []byte) error {
		partial := raw[:len(raw)/2]
		if err := os.WriteFile(path, partial, 0o600); err != nil {
			return err
		}
		return errors.New("injected partial append")
	}
	created := strideWorkRunTestCommand(run, "event-created", "a", STRIDERunCreated, "Scout opened the work run", at)
	created.Run = &run
	if _, _, err := repository.Append(created); err == nil {
		t.Fatal("partial append reported success")
	}
	if _, _, err := repository.Append(created); !errors.Is(err, ErrSTRIDEWorkRunUnavailable) {
		t.Fatalf("partial-tail repository accepted later append: %v", err)
	}
	if _, err := NewSTRIDEWorkRunRepository(path); !errors.Is(err, ErrSTRIDEWorkRunInvalid) {
		t.Fatalf("restart accepted partial tail: %v", err)
	}
}

func TestSTRIDEWorkRunUsesGlobalLedgerAndContiguousPerRunSequences(t *testing.T) {
	at := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	first := strideWorkRunTestRun(at)
	second := strideWorkRunTestRun(at)
	second.ID, second.IdempotencyKeyDigest, second.Origin.ID = "work-run-2", strings.Repeat("f", 64), "conversation-2"
	repository, err := NewSTRIDEWorkRunRepository("")
	if err != nil {
		t.Fatal(err)
	}
	firstCreated := strideWorkRunTestCommand(first, "event-first-created", "a", STRIDERunCreated, "Scout opened the first run", at)
	firstCreated.Run = &first
	storedFirst := appendSTRIDEWorkRunTestEvent(t, repository, firstCreated)
	secondCreated := strideWorkRunTestCommand(second, "event-second-created", "b", STRIDERunCreated, "Scout opened the second run", at.Add(time.Second))
	secondCreated.Run = &second
	storedSecond := appendSTRIDEWorkRunTestEvent(t, repository, secondCreated)
	phase := strideWorkRunTestCommand(first, "event-first-phase", "c", STRIDEPhaseChanged, "Scout moved the first run into evidence", at.Add(2*time.Second))
	phase.Phase = STRIDEWorkRunEvidence
	storedPhase := appendSTRIDEWorkRunTestEvent(t, repository, phase)
	if storedFirst.LedgerSequence != 1 || storedSecond.LedgerSequence != 2 || storedPhase.LedgerSequence != 3 ||
		storedFirst.Sequence != 1 || storedSecond.Sequence != 1 || storedPhase.Sequence != 2 {
		t.Fatalf("sequence policy first=%+v second=%+v phase=%+v", storedFirst, storedSecond, storedPhase)
	}
	card, err := repository.SideCard(first.ID)
	if err != nil || len(card.Activity) != 2 || card.Activity[0].Sequence != 1 || card.Activity[1].Sequence != 2 || card.LastSequence != 2 {
		t.Fatalf("side-card run sequence card=%+v err=%v", card, err)
	}
}

func TestSTRIDEWorkRunRejectsTamperedDurableEventChain(t *testing.T) {
	at := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	run := strideWorkRunTestRun(at)
	path := filepath.Join(t.TempDir(), "work-runs.jsonl")
	repository, err := NewSTRIDEWorkRunRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	created := strideWorkRunTestCommand(run, "event-created", "a", STRIDERunCreated, "Scout opened the work run", at)
	created.Run = &run
	stored := appendSTRIDEWorkRunTestEvent(t, repository, created)
	stored.ActivitySummary = "Tampered after durable append"
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSTRIDEWorkRunRepository(path); !errors.Is(err, ErrSTRIDEWorkRunInvalid) {
		t.Fatalf("tampered ledger error=%v", err)
	}
}

func ptrSTRIDEWorkRunReference(value STRIDEReference) *STRIDEReference { return &value }
