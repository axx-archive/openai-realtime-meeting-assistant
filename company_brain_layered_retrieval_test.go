package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func appendLayeredRetrievalTranscript(t *testing.T, app *kanbanBoardApp, meetingID, id, speaker, body string) meetingMemoryEntry {
	t.Helper()
	entry, appended, err := app.memory.appendAttributedTranscriptEntry(
		officeRoomID,
		id,
		"",
		speaker,
		"human_attributed",
		body,
		map[string]string{
			"meetingId":  meetingID,
			"source":     "meeting_audio",
			"visibility": "organization",
			"tenantId":   canonicalArtifactTenantID(),
		},
		true,
		meetingID,
	)
	if err != nil || !appended {
		t.Fatalf("append transcript %s: appended=%t err=%v", id, appended, err)
	}
	return entry
}

func upsertLayeredRetrievalDigest(t *testing.T, app *kanbanBoardApp, meetingID string, anchor meetingMemoryEntry, summary string) meetingMemoryEntry {
	t.Helper()
	now := time.Now().UTC()
	payload := meetingDigestPayload{
		MeetingID: meetingID,
		Title:     "Sagebrush strategy review",
		Day:       now.Format("2006-01-02"),
		Topics: []meetingDigestTopic{{
			T: summary, Anchor: anchor.ID, At: anchor.CreatedAt.UTC().Format(time.RFC3339), Importance: 5,
		}},
		Themes: []string{"Unbound model theme must not survive current-source projection."},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(body), map[string]string{
		"meetingId":                meetingID,
		digestDayMetadataKey:       now.Format("2006-01-02"),
		digestSpanStartMetadataKey: anchor.CreatedAt.Add(-time.Minute).UTC().Format(time.RFC3339),
		digestSpanEndMetadataKey:   anchor.CreatedAt.Add(time.Minute).UTC().Format(time.RFC3339),
		meetingRecordDigestSourceRevisionsMetadataKey: meetingRecordDigestSourceRevisionMetadata(
			payload,
			meetingRecordSegments(app.memory.snapshotForMeeting(meetingID, 0), meetingID),
		),
		"visibility": "organization",
		"tenantId":   canonicalArtifactTenantID(),
	})
	if err != nil {
		t.Fatalf("upsert digest: %v", err)
	}
	return entry
}

func companyBrainSourceLineCount(grounding string) int {
	count := 0
	for _, line := range strings.Split(grounding, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- [") {
			count++
		}
	}
	return count
}

func TestCompanyBrainRetrievalDepthIntentClassifier(t *testing.T) {
	tests := []struct {
		query string
		want  companyBrainRetrievalDepth
	}{
		{"Summarize what Tyler said and synthesize the patterns across the last month.", companyBrainRetrievalSummary},
		{"Build a market opportunity deck from the company history.", companyBrainRetrievalSummary},
		{"What did Tyler say on August 12 about the launch threshold?", companyBrainRetrievalExact},
		{"Quote the exact words from the transcript and cite the source.", companyBrainRetrievalExact},
	}
	for _, test := range tests {
		if got := companyBrainRetrievalDepthForQuery(test.query); got != test.want {
			t.Errorf("depth for %q = %q, want %q", test.query, got, test.want)
		}
	}
}

func TestCompanyBrainLayeredRetrievalBroadSynthesisPrefersDigestWithinLargeCorpus(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	const topic = "sagebrush constellation creator threshold"

	var anchor meetingMemoryEntry
	for index := 0; index < 180; index++ {
		entry := appendLayeredRetrievalTranscript(t, app, meetingID, fmt.Sprintf("sagebrush-raw-%03d", index), "Tyler",
			fmt.Sprintf("%s discussion fragment %03d with detailed operational chatter that should not flood a synthesis prompt.", topic, index))
		if index == 179 {
			anchor = entry
		}
	}
	digest := upsertLayeredRetrievalDigest(t, app, meetingID, anchor,
		"The sagebrush constellation strategy uses one attributable creator threshold before scale.")

	engine := newGoalEngine(app)
	plan := &goalPlan{
		ProcessID:   packagingStudioProcessID,
		Objective:   "Create a strategic overview deck that synthesizes the " + topic + " and the opportunity around it.",
		RequestedBy: "aj@shareability.com",
	}
	grounding, err := engine.processStageCompanyContextAuthorized(context.Background(), plan, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(grounding, "depth="+string(companyBrainRetrievalSummary)) {
		t.Fatalf("broad synthesis did not select summary-first retrieval:\n%s", grounding)
	}
	if !strings.Contains(grounding, digest.ID) || !strings.Contains(grounding, "kind="+meetingMemoryKindMeetingDigest+" layer=summary") {
		t.Fatalf("current meeting digest did not lead the compact context:\n%s", grounding)
	}
	if strings.Contains(grounding, "Unbound model theme") {
		t.Fatalf("summary-first retrieval bypassed the digest's current-source projection:\n%s", grounding)
	}
	if strings.Contains(grounding, "\n- [source_id=sagebrush-raw-") {
		t.Fatalf("same-meeting raw transcript flooded a digest-backed broad synthesis:\n%s", grounding)
	}
	if got := companyBrainSourceLineCount(grounding); got > companyBrainContextMaxSources {
		t.Fatalf("source lines=%d, max=%d", got, companyBrainContextMaxSources)
	}
	if len(grounding) > companyBrainContextMaxBytes {
		t.Fatalf("grounding bytes=%d, max=%d", len(grounding), companyBrainContextMaxBytes)
	}
	if plan.ContextCheckpoint == nil || plan.ContextCheckpoint.RetrievalDepth != string(companyBrainRetrievalSummary) ||
		len(plan.ContextCheckpoint.SourceRefs) == 0 || !strings.Contains(strings.Join(plan.ContextCheckpoint.SourceRefs, "\n"), digest.ID) {
		t.Fatalf("summary retrieval checkpoint missing exact compact manifest: %+v", plan.ContextCheckpoint)
	}
	if got, want := plan.ContextCheckpoint.SourceManifestDigest, sha256Hex([]byte(strings.Join(plan.ContextCheckpoint.SourceRefs, "\n"))); got != want {
		t.Fatalf("manifest digest=%q, want %q", got, want)
	}
	authority, err := processInternalAuthoritySources(app, plan)
	if err != nil {
		t.Fatal(err)
	}
	projected := app.currentSourceRecallEntries([]meetingMemoryEntry{digest})
	if len(projected) != 1 {
		t.Fatalf("digest did not retain one current-source projection: %+v", projected)
	}
	ref := companyBrainEntryAuthorityRef(projected[0])
	if source, ok := authority[ref]; !ok || !strings.Contains(source.Text, "attributable creator threshold") {
		t.Fatalf("summary retrieval lost its exact current-source authority: ref=%q source=%+v", ref, source)
	}
}

func TestCompanyBrainLayeredRetrievalPreciseAskDrillsIntoExactPrimaryExcerpt(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	prefix := strings.Repeat("Routine logistics filled this part of the meeting without the requested proof point. ", 18)
	exactWords := "Tyler: The launch threshold is 417 activated creators on August 12, and we should not scale before that proof."
	primary := appendLayeredRetrievalTranscript(t, app, meetingID, "sagebrush-exact-primary", "Tyler", prefix+exactWords)
	digest := upsertLayeredRetrievalDigest(t, app, meetingID, primary,
		"The team discussed a creator activation threshold before scale.")

	engine := newGoalEngine(app)
	plan := &goalPlan{
		ProcessID:   documentReportProcessID,
		Objective:   "What exactly did Tyler say on August 12 about the 417 activated creator threshold? Quote the source.",
		RequestedBy: "aj@shareability.com",
	}
	grounding, err := engine.processStageCompanyContextAuthorized(context.Background(), plan, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(grounding, "depth="+string(companyBrainRetrievalExact)) {
		t.Fatalf("precise ask did not select exact-primary retrieval:\n%s", grounding)
	}
	if !strings.Contains(grounding, primary.ID) || !strings.Contains(grounding, "kind="+meetingMemoryKindTranscript+" layer=primary") {
		t.Fatalf("exact current transcript was not retrieved:\n%s", grounding)
	}
	if !strings.Contains(grounding, "417 activated creators on August 12") {
		t.Fatalf("query-centered bounded excerpt lost the exact requested words:\n%s", grounding)
	}
	if digestAt, primaryAt := strings.Index(grounding, digest.ID), strings.Index(grounding, primary.ID); digestAt >= 0 && primaryAt > digestAt {
		t.Fatalf("digest outranked the exact primary source for a precise ask:\n%s", grounding)
	}
	if plan.ContextCheckpoint == nil || plan.ContextCheckpoint.RetrievalDepth != string(companyBrainRetrievalExact) ||
		!strings.Contains(strings.Join(plan.ContextCheckpoint.SourceRefs, "\n"), primary.ID) {
		t.Fatalf("exact retrieval checkpoint missing primary source: %+v", plan.ContextCheckpoint)
	}
	authority, err := processInternalAuthoritySources(app, plan)
	if err != nil {
		t.Fatal(err)
	}
	ref := companyBrainEntryAuthorityRef(primary)
	if source, ok := authority[ref]; !ok || !strings.Contains(source.Text, exactWords) {
		t.Fatalf("exact retrieval lost full primary authority behind its bounded excerpt: ref=%q source=%+v", ref, source)
	}
}

func TestCompanyBrainExactDepthStillCannotLaunderPrivateSourceIntoSharedDestination(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	const topic = "cinder ridge launch code 417"
	visible, _, err := app.createOSArtifactWithMetadata("artifacts", "Cinder Ridge shared record", topic+" is cleared for the organization.", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "organization", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	private, _, err := app.createOSArtifactWithMetadata("artifacts", "Cinder Ridge private record", topic+" PRIVATE EXACT CANARY", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "private", "ownerEmail": "aj@shareability.com", "requestedBy": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, created, err := app.ensureScoutChatThread("cinder-ridge-shared", "aj@shareability.com", "AJ", "Cinder Ridge", scoutChatVisibilityPublic, nil)
	if err != nil || !created {
		t.Fatalf("create shared destination: created=%t err=%v", created, err)
	}
	plan := &goalPlan{
		ProcessID:    packagingStudioProcessID,
		Objective:    "Quote the exact source for the " + topic + ".",
		RequestedBy:  "aj@shareability.com",
		RouteReceipt: &goalRouteReceipt{Requester: "aj@shareability.com", OriginKind: agentThreadOriginChannel, OriginID: destination.ID},
	}
	grounding, err := newGoalEngine(app).processStageCompanyContextAuthorized(context.Background(), plan, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(grounding, "depth="+string(companyBrainRetrievalExact)) || !strings.Contains(grounding, visible.ID) {
		t.Fatalf("exact-depth positive control was not retrieved:\n%s", grounding)
	}
	if strings.Contains(grounding, private.ID) || strings.Contains(grounding, "PRIVATE EXACT CANARY") {
		t.Fatalf("exact-depth retrieval bypassed destination authorization:\n%s", grounding)
	}
}

func TestGoalContextCheckpointSurvivesCompactionCyclesAndNeverBecomesAuthority(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	source, _, err := app.createOSArtifactWithMetadata("artifacts", "Juniper continuity record", "juniper continuity launch constraint", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "organization", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := newGoalEngine(app)
	engine.now = func() time.Time { return time.Date(2026, 8, 22, 15, 4, 5, 0, time.UTC) }
	plan := &goalPlan{
		PlanVersion: goalPlanVersion,
		GoalID:      "goal-juniper-continuity", Objective: "Build a report about the juniper continuity launch constraint",
		CreatedBy: "AJ", RequestedBy: "aj@shareability.com", Authority: "analysis_only",
		ContextRefs: "file|juniper|1|digest", ProcessID: documentReportProcessID, State: goalStateExecute,
		Subtasks:     []goalSubtask{{ID: "context_snapshot", Title: "Context snapshot", Mode: "research", Runner: agentRunnerCodexSidecar, Authority: "analysis_only", Status: subtaskComplete, ArtifactID: "artifact-context"}},
		Gate:         goalGate{Status: "pending", Reason: "retain the gate decision"},
		Report:       goalReport{Headline: "retain the report summary", ArtifactIDs: []string{"artifact-context"}},
		Verification: goalVerification{Verdict: "pending"},
		Blocker:      "retain the active blocker detail",
		Checkpoint:   &goalProcessCheckpoint{StageID: "approval", Question: "Keep this judgment call?", Options: []goalCheckpointOption{{Label: "Continue"}}},
	}
	grounding, err := engine.processStageCompanyContextAuthorized(context.Background(), plan, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(grounding, source.ID) || plan.ContextCheckpoint == nil {
		t.Fatalf("initial compact context checkpoint missing source: %+v\n%s", plan.ContextCheckpoint, grounding)
	}

	parent, _, err := app.createOSArtifactWithMetadata("workflow", "Juniper continuity goal", "Durable goal body", "AJ", map[string]string{
		"mode": "goal", "visibility": "private", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 10; cycle++ {
		persisted := engine.persist(plan, parent.ID, "Durable goal body")
		if persisted.ID == "" {
			t.Fatalf("compaction cycle %d did not persist", cycle)
		}
		resumed, ok := decodeGoalPlan(persisted.Metadata["goalPlan"])
		if !ok {
			t.Fatalf("compaction cycle %d did not decode", cycle)
		}
		plan = &resumed
	}
	if plan.Objective != "Build a report about the juniper continuity launch constraint" || plan.State != goalStateExecute ||
		plan.Blocker != "retain the active blocker detail" || plan.Gate.Reason != "retain the gate decision" ||
		plan.Report.Headline != "retain the report summary" || plan.Checkpoint == nil || plan.Checkpoint.Question != "Keep this judgment call?" ||
		plan.ContextCheckpoint == nil || !strings.Contains(strings.Join(plan.ContextCheckpoint.SourceRefs, "\n"), source.ID) {
		t.Fatalf("durable compaction lost objective/phase/decision/blocker/source continuity: %+v", plan)
	}

	// The persisted ref is continuity only. Quarantining the canonical source
	// after restart must remove it on the next request and replace the manifest;
	// a saved checkpoint may never resurrect or authorize the old body.
	if _, _, err := app.memory.updateEntryWithMetadata(source.Kind, source.ID, source.Text, map[string]string{relevanceMetadataKey: relevanceQuarantined}); err != nil {
		t.Fatal(err)
	}
	refreshed, err := engine.processStageCompanyContextAuthorized(context.Background(), plan, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(refreshed, source.ID) || strings.Contains(refreshed, source.Text) {
		t.Fatalf("resumed retrieval trusted the stale checkpoint as authority:\n%s", refreshed)
	}
	if plan.ContextCheckpoint == nil || len(plan.ContextCheckpoint.SourceRefs) != 0 ||
		plan.ContextCheckpoint.SourceManifestDigest != sha256Hex(nil) {
		t.Fatalf("refreshed checkpoint retained revoked source authority: %+v", plan.ContextCheckpoint)
	}
	if plan.Objective != "Build a report about the juniper continuity launch constraint" || plan.Checkpoint == nil || plan.Checkpoint.Question != "Keep this judgment call?" {
		t.Fatalf("context refresh mutated durable goal judgment state: %+v", plan)
	}
}
