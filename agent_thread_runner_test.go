package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStrideE10ScoutCutoverFailsBeforeSingletonSourcesProviderAndBroadcast(t *testing.T) {
	converter, _, _, _, _ := strideE10TenantEnvelopeTestSetup(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	t.Setenv("ANTHROPIC_API_KEY", "")
	runID, mode, query := "agent-thread-research-tenant-one", "research", "map the bounded market"
	purpose := StrideE10TenantAuthorityPurposeForScoutThread(runID, mode, query)
	envelope, err := MintStrideE10TenantAuthorityEnvelopeForSurface(context.Background(), converter, strings.Repeat("a", 64), StrideE10TenantSurfaceScout, purpose, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	priorAsync := startAgentThreadAsync
	asyncCalls := 0
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) { asyncCalls++ }
	t.Cleanup(func() { startAgentThreadAsync = priorAsync })
	before := len(app.memory.snapshot(0))
	if _, err := app.launchAgentThreadWithSpecAndTenantAuthority(mode, query, "person-one", map[string]string{"originKind": agentThreadOriginChannel, "originId": "private-other-org"}, agentThreadGoalSpec{ContextRefs: "file:cross-org-private"}, runID, &envelope); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("cutover launch used singleton sources: %v", err)
	}
	if asyncCalls != 0 || len(app.memory.snapshot(0)) != before {
		t.Fatalf("cutover launch persisted or broadcast work: async=%d before=%d after=%d", asyncCalls, before, len(app.memory.snapshot(0)))
	}

	thread := scoutAgentThread{ID: runID, Mode: mode, Query: query, TenantAuthority: &envelope, Artifact: meetingMemoryEntry{ID: "artifact-cross-org", Metadata: map[string]string{
		"requestedBy": "person-attacker", "originKind": agentThreadOriginChannel, "originId": "private-other-org", "contextRefs": "file:cross-org-private",
	}}}
	providerCalls := 0
	if _, err := app.produceAgentThreadArtifactWithWorker(context.Background(), thread, func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls++
		return "must not run", nil
	}); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("cross-org provider admission err=%v", err)
	}
	if providerCalls != 0 {
		t.Fatalf("cross-org board/memory/File/private-channel context reached provider %d times", providerCalls)
	}
}

// A /goal subtask child (goalParentId set) must NOT fire its own creator
// notification — the parent goal engine notifies once on the terminal state, so
// a revised subtask can't flood "Finished Recently" with v1/v2/v3 pings. A
// standalone thread always notifies.
func TestShouldNotifyAgentThreadCreator(t *testing.T) {
	standalone := meetingMemoryEntry{Metadata: map[string]string{}}
	if !shouldNotifyAgentThreadCreator(standalone) {
		t.Fatal("standalone thread must notify its creator")
	}
	child := meetingMemoryEntry{Metadata: map[string]string{"goalParentId": "agent-thread-goal-1"}}
	if shouldNotifyAgentThreadCreator(child) {
		t.Fatal("goal subtask child must be suppressed (parent notifies once)")
	}
}

// The deliverable flag becomes the goalDeliverable metadata key (the flag the
// runner reads for the heavier budget); an unset flag stamps nothing.
func TestAgentThreadGoalSpecStampsDeliverableFlag(t *testing.T) {
	if got := (agentThreadGoalSpec{Deliverable: true}).metadata()["goalDeliverable"]; got != "true" {
		t.Fatalf("goalDeliverable=%q, want true", got)
	}
	if _, present := (agentThreadGoalSpec{}).metadata()["goalDeliverable"]; present {
		t.Fatal("goalDeliverable stamped on a non-deliverable spec")
	}
}

func TestPersistAgentThreadProgressAdvancesDurableChatRef(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	chat, err := app.createScoutChatThread("aj@shareability.com", "AJ", "progress projection", "")
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	thread, err := app.launchAgentThreadWithOrigin("research", "map the category", "AJ", map[string]string{
		"originKind":  agentThreadOriginPrivateThread,
		"originId":    chat.ID,
		"requestedBy": "aj@shareability.com",
	})
	if err != nil {
		t.Fatalf("launch thread: %v", err)
	}
	refMessage := scoutChatMessageRecord{
		ID:        "progress-ref-1",
		Kind:      "thread",
		Role:      "scout",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread: &scoutChatThreadRef{
			ID: thread.ID, Mode: "research", Query: thread.Query, Status: "running",
			ArtifactID: thread.Artifact.ID, ProgressPercent: 35,
		},
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", chat.ID, refMessage); err != nil {
		t.Fatalf("commit work ref: %v", err)
	}

	app.persistAgentThreadProgress(thread, AgentProgress{
		Stage: "gate_before_shipping", ProgressPercent: 78, GoalStatus: "review", ReviewGate: "pending", Note: "checking every cited claim",
	})

	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", chat.ID)
	if err != nil {
		t.Fatalf("reload chat: %v", err)
	}
	var projected *scoutChatThreadRef
	for index := range saved.Messages {
		if saved.Messages[index].Thread != nil && saved.Messages[index].Thread.ID == thread.ID {
			projected = saved.Messages[index].Thread
			break
		}
	}
	if projected == nil {
		t.Fatal("durable work ref disappeared")
	}
	if projected.ProgressPercent != 78 || projected.CurrentStage != "gate_before_shipping" || projected.ProgressNote != "checking every cited claim" {
		t.Fatalf("projected ref=%#v, want the bounded mid-run progress", projected)
	}
}

func TestAgentThreadProducesStructuredArtifactWithResponder(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	thread := scoutAgentThread{
		ID:     "agent-thread-research-1",
		Mode:   "research",
		Query:  "identify the evidence needed for Realtime 2 as UI",
		Status: "running",
	}

	var captured openAITextRequest
	output, err := app.produceAgentThreadArtifact(context.Background(), thread, func(_ context.Context, apiKey string, request openAITextRequest) (string, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		captured = request
		return completeResearchArtifactForTest(), nil
	})
	if err != nil {
		t.Fatalf("produceAgentThreadArtifact: %v", err)
	}
	if !strings.Contains(output, "Bonfire comparable-company map") {
		t.Fatalf("output=%q, want responder output", output)
	}
	for _, want := range []string{"finished decision brief", "source-bound conversation", "Executive Summary", "Search tags", "Thesis", "Comparable Companies", "Sources", "Do not claim browser"} {
		if !strings.Contains(captured.Instructions, want) {
			t.Fatalf("instructions missing %q: %s", want, captured.Instructions)
		}
	}
	if !strings.Contains(captured.Input, thread.Query) || !strings.Contains(captured.Input, "Current authorized context") || strings.Contains(captured.Input, "Board and memory context") {
		t.Fatalf("input=%q, want thread query and context", captured.Input)
	}
	if captured.MaxOutputTokens != defaultResearchAgentThreadMaxOutputTokens {
		t.Fatalf("MaxOutputTokens=%d, want research artifact headroom %d", captured.MaxOutputTokens, defaultResearchAgentThreadMaxOutputTokens)
	}
	if !captured.EnableWebSearch {
		t.Fatal("research thread did not receive the hosted web-search tool")
	}
	if captured.Workflow != "agent_thread_research" || !strings.Contains(captured.Instructions, "Live research authority") {
		t.Fatalf("research request provenance/instructions=%q / %q", captured.Workflow, captured.Instructions)
	}
}

func TestAgentThreadLiveWebSearchIsResearchScoped(t *testing.T) {
	for _, tt := range []struct {
		name   string
		thread scoutAgentThread
		want   bool
	}{
		{name: "research", thread: scoutAgentThread{Mode: "research"}, want: true},
		{name: "deep research tool", thread: scoutAgentThread{Mode: "artifact", Artifact: meetingMemoryEntry{Metadata: map[string]string{"toolTemplate": "deep_research"}}}, want: true},
		{name: "design", thread: scoutAgentThread{Mode: "design"}, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentThreadUsesLiveWebSearch(tt.thread); got != tt.want {
				t.Fatalf("agentThreadUsesLiveWebSearch=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentThreadRequestContextLeavesLiveResearchUnbounded(t *testing.T) {
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	t.Setenv("ANTHROPIC_API_KEY", "")

	for _, tt := range []struct {
		name         string
		thread       scoutAgentThread
		wantDeadline bool
	}{
		{name: "ordinary work remains bounded", thread: scoutAgentThread{Mode: "design"}, wantDeadline: true},
		{name: "research runs until completion or cancellation", thread: scoutAgentThread{Mode: "research"}, wantDeadline: false},
		{name: "deep research tool runs until completion or cancellation", thread: scoutAgentThread{Mode: "artifact", Artifact: meetingMemoryEntry{Metadata: map[string]string{"toolTemplate": "deep_research"}}}, wantDeadline: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := agentThreadRequestContext(context.Background(), tt.thread)
			defer cancel()
			_, hasDeadline := ctx.Deadline()
			if hasDeadline != tt.wantDeadline {
				t.Fatalf("context deadline=%v, want %v", hasDeadline, tt.wantDeadline)
			}
		})
	}
}

func TestAgentThreadRequestContextResearchUnboundedRegardlessOfRunner(t *testing.T) {
	// Research must be unbounded even when the deployment is configured for a
	// different runner, because selectAgentRunner routes research to
	// openAITextAgentRunner regardless of the configured runner.
	for _, runner := range []string{agentRunnerStub, agentRunnerAnthropicFable, "anthropic", "fable"} {
		t.Run("runner="+runner, func(t *testing.T) {
			t.Setenv("BONFIRE_AGENT_RUNNER", runner)
			t.Setenv("ANTHROPIC_API_KEY", "")
			ctx, cancel := agentThreadRequestContext(context.Background(), scoutAgentThread{Mode: "research"})
			defer cancel()
			if _, hasDeadline := ctx.Deadline(); hasDeadline {
				t.Fatalf("research context has deadline with runner=%q; research must be unbounded", runner)
			}
		})
	}
}

func TestAgentThreadMaxOutputTokensIsBounded(t *testing.T) {
	t.Setenv("BONFIRE_AGENT_THREAD_MAX_OUTPUT_TOKENS", "1")
	if got := agentThreadMaxOutputTokens(); got != 3200 {
		t.Fatalf("low budget=%d, want 3200", got)
	}
	t.Setenv("BONFIRE_AGENT_THREAD_MAX_OUTPUT_TOKENS", "999999")
	if got := agentThreadMaxOutputTokens(); got != 12000 {
		t.Fatalf("high budget=%d, want 12000", got)
	}

	research := scoutAgentThread{Mode: "research"}
	t.Setenv("BONFIRE_RESEARCH_MAX_OUTPUT_TOKENS", "1")
	if got := agentThreadMaxOutputTokensForThread(research); got != 12000 {
		t.Fatalf("low research budget=%d, want 12000", got)
	}
	t.Setenv("BONFIRE_RESEARCH_MAX_OUTPUT_TOKENS", "999999")
	if got := agentThreadMaxOutputTokensForThread(research); got != 32000 {
		t.Fatalf("high research budget=%d, want 32000", got)
	}

	deliverable := scoutAgentThread{Mode: "artifacts", Artifact: meetingMemoryEntry{Metadata: map[string]string{
		"goalParentId": "goal-1", "goalSubtaskId": "ship_deck", "goalDeliverable": "true", "outputContract": packagingStudioDeckContract,
	}}}
	t.Setenv("BONFIRE_DELIVERABLE_MAX_TOKENS", "1")
	if got := agentThreadMaxOutputTokensForThread(deliverable); got != 12000 {
		t.Fatalf("low deliverable budget=%d, want 12000", got)
	}
	t.Setenv("BONFIRE_DELIVERABLE_MAX_TOKENS", "999999")
	if got := agentThreadMaxOutputTokensForThread(deliverable); got != 64000 {
		t.Fatalf("high deliverable budget=%d, want 64000", got)
	}
}

func TestGroundedDeliverableUsesLongRequestWindow(t *testing.T) {
	t.Setenv("BONFIRE_ORCHESTRATOR_TIMEOUT", "7m")
	thread := scoutAgentThread{Mode: "artifacts", Artifact: meetingMemoryEntry{Metadata: map[string]string{
		"goalParentId": "goal-1", "goalSubtaskId": "ship_deck", "goalDeliverable": "true", "outputContract": packagingStudioDeckContract,
	}}}
	if got := agentThreadRequestTimeout(thread); got != 7*time.Minute {
		t.Fatalf("grounded deliverable timeout=%s, want 7m", got)
	}
}

func TestScoutChatThreadAttentionReasonIsClosedAndActionable(t *testing.T) {
	for _, test := range []struct {
		metadata map[string]string
		want     string
	}{
		{metadata: map[string]string{"error": "OpenAI output rejected: max_output_truncation"}, want: "output_truncated"},
		{metadata: map[string]string{"error": "OpenAI output rejected: output_validation_error: missing sources"}, want: "quality_gate_failed"},
		{metadata: map[string]string{"goalBlocker": `subtask "context_snapshot" blocked after 2 revisions: context snapshot research authority is invalid: not one bounded comparative evidence lane`}, want: "research_scope_failed"},
		{metadata: map[string]string{"goalBlocker": `subtask "evidence" stopped after an external-evidence format failure; the source gate stayed closed`}, want: "evidence_gate_failed"},
		{metadata: map[string]string{"error": "provider transport_error"}, want: "provider_unavailable"},
		{metadata: map[string]string{"error": "provider unavailable"}, want: "provider_unavailable"},
		{metadata: map[string]string{"error": "saved artifact unavailable during rendered review"}, want: "work_failed"},
		{metadata: map[string]string{"error": "render command timed out"}, want: "work_failed"},
		{metadata: map[string]string{"error": "unexpected provider failure"}, want: "work_failed"},
		{metadata: map[string]string{}, want: ""},
	} {
		if got := scoutChatThreadAttentionReason(test.metadata); got != test.want {
			t.Fatalf("attention reason for %v=%q, want %q", test.metadata, got, test.want)
		}
	}
}

func TestAgentThreadModeContractsDifferentiateResearchAndDesign(t *testing.T) {
	for _, tt := range []struct {
		mode string
		want []string
	}{
		{mode: "research", want: []string{"Executive Summary", "Thesis", "Evidence", "Sources", "Counterarguments", "Recommendation", "Search tags"}},
		{mode: "design", want: []string{"Design intent", "Context and research used", "Core screens", "Responsive behavior", "Implementation handoff"}},
	} {
		got := agentThreadModeContract(tt.mode)
		for _, want := range tt.want {
			if !strings.Contains(got, want) {
				t.Fatalf("mode %s contract missing %q: %s", tt.mode, want, got)
			}
		}
	}
}

func TestAgentThreadResearchModeCarriesArtifactContractMetadata(t *testing.T) {
	metadata := agentThreadModeMetadata("research")
	if metadata["artifactContract"] != "research_brief_v3" {
		t.Fatalf("artifactContract=%q, want research_brief_v3", metadata["artifactContract"])
	}
	for _, want := range []string{"executive summary", "comparable companies", "sources", "worker evidence"} {
		if !strings.Contains(metadata["artifactHeadings"], want) {
			t.Fatalf("artifactHeadings missing %q: %s", want, metadata["artifactHeadings"])
		}
	}
	if got := agentThreadModeMetadata("design"); got != nil {
		t.Fatalf("design metadata=%v, want nil", got)
	}
}

// Origin metadata is stamped at launch so completion can close the loop —
// and only the three origin keys survive into artifact metadata.
func TestLaunchAgentThreadWithOriginStampsOnlyOriginKeys(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	meetingID := app.memory.ensureMeetingID(officeRoomID)
	thread, err := app.launchAgentThreadWithOrigin("research", "map the delivery loop", "AJ", map[string]string{
		"originKind":      agentThreadOriginRoom,
		"originId":        "codex-proposal-42",
		"originMeetingId": meetingID,
		"stray":           "must be dropped",
	})
	if err != nil {
		t.Fatalf("launchAgentThreadWithOrigin: %v", err)
	}
	metadata := thread.Artifact.Metadata
	if metadata["originKind"] != agentThreadOriginRoom || metadata["originId"] != "codex-proposal-42" || metadata["originMeetingId"] != meetingID {
		t.Fatalf("origin metadata=%#v, want kind/id/meeting stamped", metadata)
	}
	if metadata["stray"] != "" {
		t.Fatalf("stray origin key leaked into metadata: %q", metadata["stray"])
	}

	// The plain launch keeps origin absent — completion stays notification-only.
	plain, err := app.launchAgentThread("research", "no origin here", "AJ")
	if err != nil {
		t.Fatalf("launchAgentThread: %v", err)
	}
	if plain.Artifact.Metadata["originKind"] != "" {
		t.Fatalf("originKind=%q on a plain launch, want empty", plain.Artifact.Metadata["originKind"])
	}
}

func TestOfficeRealtimeAgentLaunchBindsAttributedRequesterAndMeetingDigest(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	digest, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, "Transcript analysis: the team asked for a partner landscape and evidence-backed shortlist.", map[string]string{
		"meetingId": meetingID, "roomId": officeRoomID,
	})
	if err != nil {
		t.Fatalf("append meeting digest: %v", err)
	}
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.mediaGen = 7
	state.mediaSittingID = meetingID
	state.activeSpeakerName = "Tom"
	state.participantCounts["Tom"] = 1
	app.mu.Unlock()
	app.captureOfficeScoutRequesterCandidate()
	app.armOfficeScoutRequesterCandidate()
	app.bindOfficeScoutRequesterToResponse("response-room-research")
	app.bindOfficeScoutRequesterToCall("response-room-research", "call-room-research")

	previousRunner := startAgentThreadAsync
	var launched scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { launched = thread }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	result, _, err := app.applyOfficeRealtimeToolCallArgs(kanbanRealtimeOutputItem{Name: "launch_agent_thread", CallID: "call-room-research", ResponseID: "response-room-research"}, map[string]any{
		"mode": "research", "query": "Research the opportunity discussed in this meeting and use the transcript analysis to scope it.",
	}, meetingID)
	if err != nil {
		t.Fatalf("launch_agent_thread: %v", err)
	}
	if launched.ID == "" || asString(result["ok"]) == "false" {
		t.Fatalf("result=%v launched=%+v", result, launched)
	}
	metadata := launched.Artifact.Metadata
	if metadata["requestedBy"] != "tom@shareability.com" || metadata["createdBy"] != scoutParticipantName || metadata["originKind"] != agentThreadOriginRoom || metadata["originId"] != officeRoomID || metadata["originMeetingId"] != meetingID {
		t.Fatalf("metadata=%v, want Tom-bound office meeting work owned visibly by Scout", metadata)
	}
	providerContext, err := app.agentThreadProviderContext(context.Background(), launched)
	if err != nil {
		t.Fatalf("provider context: %v", err)
	}
	foundDigest := false
	for _, entry := range providerContext.Memory {
		if entry.ID == digest.ID && strings.Contains(entry.Text, "Transcript analysis") {
			foundDigest = true
		}
	}
	if !foundDigest {
		t.Fatalf("provider memory=%v, want exact authorized current-meeting analysis", providerContext.Memory)
	}
	if _, _, err := app.applyOfficeRealtimeToolCallArgs(kanbanRealtimeOutputItem{Name: "launch_agent_thread", CallID: "call-room-research", ResponseID: "response-room-research"}, map[string]any{
		"mode": "research", "query": "start a second job from the same spoken turn",
	}, meetingID); err == nil || !strings.Contains(err.Error(), "attributed signed-in") {
		t.Fatalf("second launch from consumed requester binding err=%v, want fail closed", err)
	}
}

func TestOfficeRealtimeAgentLaunchRejectsUnattributedOrStaleRequester(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.mediaGen = 3
	state.mediaSittingID = meetingID
	app.mu.Unlock()

	previousRunner := startAgentThreadAsync
	launches := 0
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches++ }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	args := map[string]any{"mode": "research", "query": "Research the meeting request"}
	if _, _, err := app.applyOfficeRealtimeToolCallArgs(kanbanRealtimeOutputItem{Name: "launch_agent_thread", CallID: "unattributed-call", ResponseID: "unattributed-response"}, args, meetingID); err == nil || !strings.Contains(err.Error(), "attributed signed-in") {
		t.Fatalf("unattributed launch err=%v, want fail closed", err)
	}

	app.mu.Lock()
	state.activeSpeakerName = "Tom"
	state.participantCounts["Tom"] = 1
	app.mu.Unlock()
	app.captureOfficeScoutRequesterCandidate()
	app.armOfficeScoutRequesterCandidate()
	app.bindOfficeScoutRequesterToResponse("stale-response")
	app.bindOfficeScoutRequesterToCall("stale-response", "stale-call")
	app.mu.Lock()
	state.mediaGen++
	state.mediaSittingID = "successor-sitting"
	app.mu.Unlock()
	app.memory.rotateMeetingID(officeRoomID)
	if _, _, err := app.applyOfficeRealtimeToolCallArgs(kanbanRealtimeOutputItem{Name: "launch_agent_thread", CallID: "stale-call", ResponseID: "stale-response"}, args, meetingID); err == nil || !strings.Contains(err.Error(), "attributed signed-in") {
		t.Fatalf("stale-sitting launch err=%v, want fail closed", err)
	}
	if launches != 0 {
		t.Fatalf("launches=%d, want zero for unattributed/stale requests", launches)
	}
}

func TestOfficeRealtimeAgentLaunchKeepsInterleavedSpeakersBoundToTheirResponses(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.mediaGen = 9
	state.mediaSittingID = meetingID
	state.participantCounts["Tom"] = 1
	state.participantCounts["Tyler"] = 1
	state.activeSpeakerName = "Tom"
	app.mu.Unlock()
	app.captureOfficeScoutRequesterCandidate()
	app.bindOfficeScoutRequesterToInputItem("item-tom")

	// Tyler commits a second turn before Tom's delayed transcription completes.
	// Each provider input item must retain its own immutable requester.
	app.mu.Lock()
	state.activeSpeakerName = "Tyler"
	app.mu.Unlock()
	app.captureOfficeScoutRequesterCandidate()
	app.bindOfficeScoutRequesterToInputItem("item-tyler")
	if !app.armScoutVoiceResponseForInputItem("Scout, research the pricing question Tom raised.", "item-tom") {
		t.Fatal("Tom input-item transcription was not admitted")
	}
	if !app.armScoutVoiceResponseForInputItem("Scout, research the distribution question Tyler raised.", "item-tyler") {
		t.Fatal("Tyler input-item transcription was not admitted")
	}
	responseCreate := app.realtimeResponseCreateForInputItem("item-tom")
	metadata := responseCreate["metadata"].(map[string]string)
	if metadata["stride_input_item_id"] != "item-tom" {
		t.Fatalf("response.create metadata=%v, want exact input item", metadata)
	}
	// response.created for A is deliberately delayed until after B's transcript.
	// Echoed server metadata must still bind each response to its own input item.
	app.handleRealtimeEvent([]byte(`{"type":"response.created","response":{"id":"response-tom","metadata":{"stride_input_item_id":"item-tom"}}}`))
	app.handleRealtimeEvent([]byte(`{"type":"response.created","response":{"id":"response-tyler","metadata":{"stride_input_item_id":"item-tyler"}}}`))
	app.bindOfficeScoutRequesterToCall("response-tom", "call-tom")
	app.bindOfficeScoutRequesterToCall("response-tyler", "call-tyler")

	previousRunner := startAgentThreadAsync
	var launches atomic.Int32
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	tomResult, _, err := app.applyOfficeRealtimeToolCallArgs(kanbanRealtimeOutputItem{Name: "launch_agent_thread", CallID: "call-tom", ResponseID: "response-tom"}, map[string]any{
		"mode": "research", "query": "Research the pricing question Tom raised.",
	}, meetingID)
	if err != nil {
		t.Fatalf("Tom launch: %v", err)
	}
	tylerResult, _, err := app.applyOfficeRealtimeToolCallArgs(kanbanRealtimeOutputItem{Name: "launch_agent_thread", CallID: "call-tyler", ResponseID: "response-tyler"}, map[string]any{
		"mode": "research", "query": "Research the distribution question Tyler raised.",
	}, meetingID)
	if err != nil {
		t.Fatalf("Tyler launch: %v", err)
	}
	tomThread := tomResult["thread"].(scoutAgentThread)
	tylerThread := tylerResult["thread"].(scoutAgentThread)
	if tomThread.Artifact.Metadata["requestedBy"] != "tom@shareability.com" || tylerThread.Artifact.Metadata["requestedBy"] != "tyler@shareability.com" {
		t.Fatalf("requesters Tom=%q Tyler=%q, want response-owned identities", tomThread.Artifact.Metadata["requestedBy"], tylerThread.Artifact.Metadata["requestedBy"])
	}
	if launches.Load() != 2 {
		t.Fatalf("launches=%d, want one exact launch per response", launches.Load())
	}
}

func TestRoomVoiceWorkExactRetryAndTerminalReplayUseOneRun(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.mediaGen = 3
	state.mediaSittingID = meetingID
	app.mu.Unlock()

	previousRunner := startAgentThreadAsync
	var launches atomic.Int32
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	args := map[string]any{"mode": "research", "query": "Map the market opportunity discussed in this meeting."}
	origin := map[string]string{
		"originKind": agentThreadOriginRoom, "originId": officeRoomID,
		"originMeetingId": meetingID, "requestedBy": "aj@shareability.com",
	}
	first, _, err := app.launchRealtimeAgentThreadForOperation(args, origin, agentThreadGoalSpec{}, "call-first")
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	retry, _, err := app.launchRealtimeAgentThreadForOperation(args, origin, agentThreadGoalSpec{}, "call-lost-response-retry")
	if err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	firstThread := first["thread"].(scoutAgentThread)
	retryThread := retry["thread"].(scoutAgentThread)
	if firstThread.ID != retryThread.ID || firstThread.Artifact.ID != retryThread.Artifact.ID || launches.Load() != 1 {
		t.Fatalf("first=%s/%s retry=%s/%s launches=%d, want one durable run", firstThread.ID, firstThread.Artifact.ID, retryThread.ID, retryThread.Artifact.ID, launches.Load())
	}
	if got := len(app.roomChatHistory(20)); got != 1 {
		t.Fatalf("room cards=%d, want one idempotent running card", got)
	}
	completed, _, err := app.updateOSArtifactWithMetadata(firstThread.Artifact.ID, "", "# Complete\n\nEvidence-backed result.", scoutParticipantName, map[string]string{
		roomWorkActivationMetadataKey: roomWorkActivationComplete,
		"status":                      "complete", "threadStatus": "complete", "goalStatus": "complete", "progressPercent": "100",
	})
	if err != nil {
		t.Fatalf("complete room work: %v", err)
	}
	if !app.projectRoomAgentThreadStatus(completed, firstThread.ID, "complete") {
		t.Fatal("complete room work projection failed")
	}
	terminalReplay, _, err := app.launchRealtimeAgentThreadForOperation(args, origin, agentThreadGoalSpec{}, "call-terminal-retry")
	if err != nil {
		t.Fatalf("terminal replay: %v", err)
	}
	terminalThread := terminalReplay["thread"].(scoutAgentThread)
	if terminalThread.ID != firstThread.ID || terminalThread.Status != "complete" || launches.Load() != 1 {
		t.Fatalf("terminal replay=%+v launches=%d, want prior completed run without restart", terminalThread, launches.Load())
	}
}

func TestRoomVoiceWorkBootRecoveryStartsExactPersistedRunOnce(t *testing.T) {
	setupAuthTestEnv(t)
	directory := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(directory, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(directory, "board.json"))
	seed := newKanbanBoardApp()
	meetingID := seed.memory.ensureMeetingID(officeRoomID)
	seed.mu.Lock()
	state := seed.roomLiveLocked(officeRoomID)
	state.mediaGen = 4
	state.mediaSittingID = meetingID
	seed.mu.Unlock()

	previousRunner := startAgentThreadAsync
	var starts atomic.Int32
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { starts.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	args := map[string]any{"mode": "research", "query": "Recover this exact room research after a process loss."}
	origin := map[string]string{
		"originKind": agentThreadOriginRoom, "originId": officeRoomID,
		"originMeetingId": meetingID, "requestedBy": "aj@shareability.com",
	}
	result, _, err := seed.launchRealtimeAgentThreadForOperation(args, origin, agentThreadGoalSpec{}, "call-before-crash")
	if err != nil {
		t.Fatalf("seed launch: %v", err)
	}
	thread := result["thread"].(scoutAgentThread)
	if starts.Load() != 1 {
		t.Fatalf("seed starts=%d, want one pre-crash activation", starts.Load())
	}

	// A process restart has a fresh in-process start registry. Boot recovery may
	// start the exact durable started run once; a duplicate reconciliation in
	// the same process must not start it again or mint another artifact/card.
	starts.Store(0)
	restarted := newKanbanBoardApp()
	restarted.reconcileRoomAgentThreadsAtBoot()
	if starts.Load() != 1 {
		t.Fatalf("recovery starts=%d, want exactly one", starts.Load())
	}
	stored, ok := restarted.osArtifactByID(thread.Artifact.ID)
	if !ok || stored.Metadata[roomWorkActivationMetadataKey] != roomWorkActivationStarted || stored.Metadata["threadId"] != thread.ID {
		t.Fatalf("recovered artifact=%+v ok=%t", stored, ok)
	}
	roomArtifacts := 0
	for _, entry := range restarted.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		if entry.Metadata[roomWorkOperationDigestMetadataKey] != "" {
			roomArtifacts++
		}
	}
	if roomArtifacts != 1 || len(restarted.roomChatHistory(20)) != 1 {
		t.Fatalf("room artifacts=%d cards=%d, want one durable run/card", roomArtifacts, len(restarted.roomChatHistory(20)))
	}
}

func TestRoomVoiceWorkBootRecoveryRunsBeforeMissingProviderConfigurationGate(t *testing.T) {
	setupAuthTestEnv(t)
	directory := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(directory, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(directory, "board.json"))
	t.Setenv("OPENAI_API_KEY", "")
	seed := newKanbanBoardApp()
	meetingID := seed.memory.ensureMeetingID(officeRoomID)
	seed.mu.Lock()
	state := seed.roomLiveLocked(officeRoomID)
	state.mediaGen = 6
	state.mediaSittingID = meetingID
	seed.mu.Unlock()

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	result, _, err := seed.launchRealtimeAgentThreadForOperation(map[string]any{
		"mode": "research", "query": "Recover honestly when provider configuration disappears.",
	}, map[string]string{
		"originKind": agentThreadOriginRoom, "originId": officeRoomID,
		"originMeetingId": meetingID, "requestedBy": "aj@shareability.com",
	}, agentThreadGoalSpec{}, "call-before-config-loss")
	if err != nil {
		t.Fatalf("seed launch: %v", err)
	}
	thread := result["thread"].(scoutAgentThread)

	restarted := newKanbanBoardApp()
	startAgentThreadAsync = func(app *kanbanBoardApp, recovered scoutAgentThread) {
		app.runAgentThread(recovered)
	}
	if err := restarted.JoinConferenceRoom(); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("join err=%v, want provider configuration failure after recovery", err)
	}
	stored, ok := restarted.osArtifactByID(thread.Artifact.ID)
	if !ok || stored.Metadata[roomWorkActivationMetadataKey] != roomWorkActivationNeedsAttention || stored.Metadata["status"] != "error" || !strings.Contains(stored.Metadata["error"], "OPENAI_API_KEY") {
		t.Fatalf("recovered artifact=%+v ok=%t, want durable needs_attention", stored, ok)
	}
	history := restarted.roomChatHistory(20)
	workRuns := map[string]bool{}
	for _, event := range history {
		workRuns[asString(event["workRunId"])] = true
	}
	if len(history) != 2 || len(workRuns) != 1 || asString(history[len(history)-1]["workStatus"]) != "needs_attention" {
		t.Fatalf("room cards=%+v, want one evolving run with truthful terminal projection", history)
	}
}

func TestRoomVoiceWorkProjectionFailureLeavesRecoverableReservation(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.mediaGen = 5
	state.mediaSittingID = meetingID
	app.mu.Unlock()
	previousRunner := startAgentThreadAsync
	var starts atomic.Int32
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { starts.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	args := map[string]any{"mode": "research", "query": "Persist the card before starting this work."}
	origin := map[string]string{
		"originKind": agentThreadOriginRoom, "originId": officeRoomID,
		"originMeetingId": meetingID, "requestedBy": "aj@shareability.com",
	}
	body, err := canonicalJSON(map[string]any{
		"mode": "research", "query": canonicalizeBoardText(asString(args["query"])), "requester": "aj@shareability.com",
		"roomId": officeRoomID, "sittingId": meetingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	runID := "agent-thread-room-" + sha256Hex(append([]byte("room-voice-work/v1\x00"), body...))[:24]
	spec := agentThreadGoalSpec{OperationID: runID, OperationBodyDigest: sha256Hex(append([]byte("room-voice-work/v1\x00"), body...)), RequestedBy: "aj@shareability.com"}
	reserved, err := app.launchAgentThreadWithSpecBound("research", asString(args["query"]), scoutParticipantName, origin, spec, runID, nil, false)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	directory := filepath.Dir(app.memory.path)
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	activationErr := app.activateReservedRoomAgentThread(reserved, spec, scoutParticipantName)
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if activationErr == nil {
		t.Fatal("activation unexpectedly succeeded while durable card projection was unavailable")
	}
	stored, _ := app.osArtifactByID(reserved.Artifact.ID)
	if stored.Metadata[roomWorkActivationMetadataKey] != roomWorkActivationReserved || starts.Load() != 0 {
		t.Fatalf("failed activation state=%q starts=%d, want reserved and zero worker", stored.Metadata[roomWorkActivationMetadataKey], starts.Load())
	}
	result, _, err := app.launchRealtimeAgentThreadForOperation(args, origin, agentThreadGoalSpec{}, "call-after-storage-recovery")
	if err != nil {
		t.Fatalf("retry after storage recovery: %v", err)
	}
	recovered := result["thread"].(scoutAgentThread)
	if recovered.ID != runID || recovered.Artifact.ID != reserved.Artifact.ID || starts.Load() != 1 {
		t.Fatalf("recovered=%s/%s reserved=%s/%s starts=%d", recovered.ID, recovered.Artifact.ID, runID, reserved.Artifact.ID, starts.Load())
	}
}

func TestManualArchiveCancelsInterleavedRoomLaunchAndAdvancesMediaScope(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admit AJ: %v", err)
	}
	app.noteMeetingAdmission(officeRoomID, "AJ")
	oldMeeting, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("active meeting missing")
	}
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.mediaGen = 11
	state.mediaSittingID = oldMeeting.ID
	state.activeSpeakerName = "AJ"
	state.participantCounts["AJ"] = 1
	app.mu.Unlock()
	app.captureOfficeScoutRequesterCandidate()
	app.armOfficeScoutRequesterCandidate()
	app.bindOfficeScoutRequesterToResponse("response-before-archive")
	app.bindOfficeScoutRequesterToCall("response-before-archive", "call-before-archive")

	previousRunner := startAgentThreadAsync
	var starts atomic.Int32
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { starts.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	entered := make(chan struct{})
	release := make(chan struct{})
	app.mu.Lock()
	app.officeToolBeforeCommit = func() {
		close(entered)
		<-release
	}
	app.mu.Unlock()
	t.Cleanup(func() {
		app.mu.Lock()
		app.officeToolBeforeCommit = nil
		app.mu.Unlock()
	})
	toolDone := make(chan struct{})
	go func() {
		defer close(toolDone)
		app.finishToolCallForSitting(kanbanRealtimeOutputItem{
			Type: "function_call", Name: "launch_agent_thread",
			ResponseID: "response-before-archive", CallID: "call-before-archive",
		}, map[string]any{
			"mode": "research", "query": "Research the topic from the meeting being archived.",
		}, nil, false, oldMeeting.ID)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("room work did not reach the pre-commit seam")
	}
	app.mu.Lock()
	oldEpoch := app.officeWorkEpoch
	app.mu.Unlock()
	type archiveOutcome struct {
		result meetingArchiveResult
		err    error
	}
	archiveDone := make(chan archiveOutcome, 1)
	go func() {
		result, err := app.archiveMeetingWithOfficeScoutFence("AJ")
		archiveDone <- archiveOutcome{result: result, err: err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		app.mu.Lock()
		advanced := app.officeWorkEpoch != oldEpoch
		app.mu.Unlock()
		if advanced {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manual archive did not advance the room-work epoch")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	select {
	case <-toolDone:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled room work did not drain")
	}
	var archived archiveOutcome
	select {
	case archived = <-archiveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("manual archive did not complete")
	}
	if archived.err != nil {
		t.Fatalf("archive: %v", archived.err)
	}
	if starts.Load() != 0 {
		t.Fatalf("old-sitting worker starts=%d, want zero", starts.Load())
	}
	successor, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || successor.ID == oldMeeting.ID || archived.result.MeetingID != oldMeeting.ID {
		t.Fatalf("archived=%+v successor=%+v ok=%t", archived.result, successor, ok)
	}
	app.mu.Lock()
	state = app.roomLiveLocked(officeRoomID)
	mediaSittingID, mediaGeneration := state.mediaSittingID, state.mediaGen
	bindingsRemain := len(app.officeScoutRequesterByResponse) + len(app.officeScoutRequesterByCall)
	app.mu.Unlock()
	if mediaSittingID != successor.ID || mediaGeneration <= 11 || bindingsRemain != 0 {
		t.Fatalf("media sitting=%q generation=%d bindings=%d, want successor %q and retired predecessor", mediaSittingID, mediaGeneration, bindingsRemain, successor.ID)
	}
}

// A terminal queued-worker completion derives the display title from the
// finished body; the launch prompt survives as threadQuery.
func TestUpdateQueuedAgentThreadDerivesTitleOnCompletion(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	query := "dig into coyote logistics pricing and produce a brief"
	thread, err := app.launchAgentThread("research", query, "AJ")
	if err != nil {
		t.Fatalf("launchAgentThread: %v", err)
	}
	if got := thread.Artifact.Metadata["title"]; !strings.EqualFold(got, query) {
		// creation-time fallback stays the compacted prompt
		t.Fatalf("scaffold title=%q, want the launch prompt %q", got, query)
	}

	app.updateQueuedAgentThread(thread, agentThreadWorkerResult{
		Text:     "# Coyote pricing teardown\n\nExecutive Summary: margins compress.",
		Metadata: map[string]string{"status": codexJobStatusComplete, "threadStatus": codexJobStatusComplete},
		Terminal: true,
	})

	stored, ok := app.osArtifactByID(thread.Artifact.ID)
	if !ok {
		t.Fatalf("artifact %s disappeared", thread.Artifact.ID)
	}
	if stored.Metadata["title"] != "Coyote pricing teardown" {
		t.Fatalf("title=%q, want derived from the body heading", stored.Metadata["title"])
	}
	if stored.Metadata["titleSource"] != "derived" {
		t.Fatalf("titleSource=%q, want derived", stored.Metadata["titleSource"])
	}
	if stored.Metadata["threadQuery"] != query {
		t.Fatalf("threadQuery=%q, want the durable launch prompt %q", stored.Metadata["threadQuery"], query)
	}

	// A non-terminal status update keeps whatever title the artifact carries.
	app.updateQueuedAgentThread(thread, agentThreadWorkerResult{
		Metadata: map[string]string{"status": codexJobStatusRunning, "threadStatus": codexJobStatusRunning},
	})
	stored, _ = app.osArtifactByID(thread.Artifact.ID)
	if stored.Metadata["title"] != "Coyote pricing teardown" {
		t.Fatalf("title=%q after status update, want unchanged", stored.Metadata["title"])
	}
}

// Room-origin completion posts exactly one compact card into the origin
// meeting's chat (via the transcript-entering path) and never delivers twice.
func TestDeliverArtifactToOriginRoomPostsCardOnce(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	meetingID := app.memory.ensureMeetingID(officeRoomID)
	artifact, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing", "# Coyote pricing teardown\n\nEvidence.", "AJ", map[string]string{
		"title":           "Coyote pricing teardown",
		"threadStatus":    "complete",
		"status":          "complete",
		"originKind":      agentThreadOriginRoom,
		"originMeetingId": meetingID,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	app.deliverArtifactToOrigin(artifact, "agent-thread-research-1")

	history := app.roomChatHistory(10)
	if len(history) != 1 {
		t.Fatalf("room chat history=%d messages, want exactly one delivery card", len(history))
	}
	payload := history[0]
	if asString(payload["artifactId"]) != artifact.ID {
		t.Fatalf("payload=%#v, want artifactId %s for the client chip", payload, artifact.ID)
	}
	if asString(payload["name"]) != scoutParticipantName {
		t.Fatalf("sender=%q, want %q", payload["name"], scoutParticipantName)
	}
	text := asString(payload["text"])
	if !strings.Contains(text, "finished") || !strings.Contains(text, "Coyote pricing teardown") {
		t.Fatalf("delivery text=%q, want finished + title", text)
	}

	stored, ok := app.osArtifactByID(artifact.ID)
	if !ok || stored.Metadata["deliveredAt"] == "" {
		t.Fatalf("metadata=%#v, want deliveredAt stamped", stored.Metadata)
	}

	// A retried completion callback re-reads the stored artifact — deliveredAt
	// makes the second delivery a no-op.
	app.deliverArtifactToOrigin(stored, "agent-thread-research-1")
	if got := len(app.roomChatHistory(10)); got != 1 {
		t.Fatalf("room chat history=%d after retry, want still 1", got)
	}
}

func TestDeliverArtifactToOriginSuppressesGoalChildrenFromChannel(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "quiet goal channel", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	artifact, _, err := app.createOSArtifactWithMetadata("artifacts", "internal story stage", "# Internal story\n\nNot yet reviewed.", "Scout", map[string]string{
		"source": "scout_thread", "status": "complete", "threadStatus": "complete",
		"goalParentId": "goal-quiet-1", "goalSubtaskId": "story", "goalDeliverable": "true",
		"originKind": agentThreadOriginChannel, "originId": channel.ID,
	})
	if err != nil {
		t.Fatalf("create goal child: %v", err)
	}

	app.deliverArtifactToOrigin(artifact, "agent-thread-internal-story")
	saved, _, err := app.scoutChatThreadByID(channel.OwnerEmail, channel.ID)
	if err != nil {
		t.Fatalf("read channel: %v", err)
	}
	if len(saved.Messages) != 0 {
		t.Fatalf("internal goal child emitted %d channel messages, want zero", len(saved.Messages))
	}
	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Metadata["deliveredAt"] != "" {
		t.Fatalf("internal goal child stamped deliveredAt=%q", stored.Metadata["deliveredAt"])
	}
}

func TestRoomAgentThreadStatusProjectsOneIdempotentRevisionPerState(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	artifact, _, err := app.createOSArtifactWithMetadata("research", "partner landscape", "# Private work\n\nSecure work is queued.", scoutParticipantName, map[string]string{
		"title": "Partner landscape", "mode": "research", "status": "running", "threadStatus": "running",
		"progressPercent": "35", "originKind": agentThreadOriginRoom, "originId": officeRoomID, "originMeetingId": meetingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	threadID := "agent-thread-research-room-projection"
	if !app.projectRoomAgentThreadStatus(artifact, threadID, "running") {
		t.Fatal("running projection failed")
	}
	if !app.projectRoomAgentThreadStatus(artifact, threadID, "running") {
		t.Fatal("idempotent running replay should reconcile successfully")
	}
	updated, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, scoutParticipantName, map[string]string{
		"status": "error", "threadStatus": "error", "goalStatus": "needs_attention", "progressPercent": "72",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !app.projectRoomAgentThreadStatus(updated, threadID, "error") {
		t.Fatal("needs-attention projection failed")
	}
	history := app.roomChatHistory(10)
	if len(history) != 2 {
		t.Fatalf("history=%v, want running + needs-attention revisions exactly once", history)
	}
	for index, message := range history {
		if asString(message["workRunId"]) != threadID || asString(message["artifactId"]) != artifact.ID || asString(message["agentId"]) != "scout" {
			t.Fatalf("history[%d]=%v, want one governed Scout work identity", index, message)
		}
	}
	if asString(history[0]["workStatus"]) != "running" || asString(history[1]["workStatus"]) != "needs_attention" {
		t.Fatalf("statuses=%q,%q", asString(history[0]["workStatus"]), asString(history[1]["workStatus"]))
	}
}

func TestRoomAgentThreadStatusProjectsOnlyClosedExactResultAndExplicitTopology(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	imageRef := strings.Repeat("a", 64)
	artifact, _, err := app.createOSArtifactWithMetadata("artifacts", "campaign hero", "Generated image artifact.", scoutParticipantName, map[string]string{
		"title": "Campaign hero", "type": artifactTypeImage, "status": "complete", "threadStatus": "complete",
		"originKind": agentThreadOriginRoom, "originId": officeRoomID, "originMeetingId": meetingID,
		"rootRunId": "root-run", "parentRunId": "parent-run",
		"assets": `[{"ref":"` + imageRef + `","mime":"image/png","name":"hero.png","kind":"image"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !app.projectRoomAgentThreadStatus(artifact, "child-run", "complete") {
		t.Fatalf("closed image result projection failed: meeting=%q current=%q history=%v metadata=%v", meetingID, app.memory.currentMeetingID(officeRoomID), app.roomChatHistory(10), artifact.Metadata)
	}
	history := app.roomChatHistory(10)
	if len(history) != 1 {
		t.Fatalf("history=%v, want one result revision", history)
	}
	projected := history[0]
	if asString(projected["workRunId"]) != "child-run" || asString(projected["workRootRunId"]) != "root-run" || asString(projected["workParentRunId"]) != "parent-run" {
		t.Fatalf("topology=%v, want explicit child/root identity", projected)
	}
	if asString(projected["resultArtifactId"]) != artifact.ID || asString(projected["resultArtifactType"]) != artifactTypeImage || asString(projected["resultArtifactVersion"]) != strconv.Itoa(artifactVersion(artifact)) || asString(projected["resultArtifactDigest"]) != artifactCapabilityDigest(artifact) {
		t.Fatalf("result tuple=%v, want exact closed artifact", projected)
	}

	malformed, _, err := app.createOSArtifactWithMetadata("artifacts", "raw substitute", `{"image":"not-a-file"}`, scoutParticipantName, map[string]string{
		"title": "Raw substitute", "type": artifactTypeImage, "status": "complete", "threadStatus": "complete",
		"originKind": agentThreadOriginRoom, "originId": officeRoomID, "originMeetingId": meetingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !app.projectRoomAgentThreadStatus(malformed, "malformed-run", "complete") {
		t.Fatal("malformed lifecycle projection failed")
	}
	history = app.roomChatHistory(10)
	if len(history) != 2 {
		t.Fatalf("history=%v, want lifecycle retained in Activity", history)
	}
	unsafe := history[1]
	if asString(unsafe["resultArtifactId"]) != "" || asString(unsafe["resultArtifactType"]) != "" || asString(unsafe["resultArtifactDigest"]) != "" {
		t.Fatalf("malformed result leaked a typed feed envelope: %v", unsafe)
	}
}

// A room delivery after the origin meeting rotated (archive / idle end) must
// not post into — or fabricate — a new meeting.
func TestDeliverArtifactToOriginSkipsRotatedMeeting(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	meetingID := app.memory.ensureMeetingID(officeRoomID)
	artifact, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing", "# Coyote pricing teardown\n\nEvidence.", "AJ", map[string]string{
		"title":           "Coyote pricing teardown",
		"threadStatus":    "complete",
		"status":          "complete",
		"originKind":      agentThreadOriginRoom,
		"originMeetingId": meetingID,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	app.memory.rotateMeetingID(officeRoomID)
	app.deliverArtifactToOrigin(artifact, "agent-thread-research-1")

	if got := app.memory.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("meeting id %q was minted, delivery after rotation must not fabricate a meeting", got)
	}
	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Metadata["deliveredAt"] != "" {
		t.Fatalf("deliveredAt=%q, want empty when the room delivery was skipped", stored.Metadata["deliveredAt"])
	}
	if got := len(app.roomChatHistory(10)); got != 0 {
		t.Fatalf("room chat history=%d, want no delivery card after rotation", got)
	}
}

// The room delivery append is gated atomically on the origin meeting id: a
// rotation landing between deliverArtifactToOrigin's guard and the append can
// neither mint a phantom meeting nor leak into the successor meeting.
func TestRoomChatDeliveryAppendGatedOnMeetingID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)

	// active origin meeting: the gated append lands.
	if _, ok := app.recordRoomChatMessageWithArtifact(officeRoomID, scoutParticipantName, "finished Research — brief", "os-artifact-live", meetingID); !ok {
		t.Fatal("gated append must land while the origin meeting is active")
	}

	// the id rotates AFTER the caller's guard would have passed: the append
	// skips and must not lazily mint a phantom meeting.
	app.memory.rotateMeetingID(officeRoomID)
	if _, ok := app.recordRoomChatMessageWithArtifact(officeRoomID, scoutParticipantName, "finished Research — brief", "os-artifact-stale", meetingID); ok {
		t.Fatal("gated append landed after the origin meeting rotated")
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("meeting id %q was minted; the skipped delivery must not fabricate a meeting", got)
	}

	// a successor meeting is running: the stale-origin append must not leak
	// into its transcript stream either.
	successorID := app.memory.ensureMeetingID(officeRoomID)
	if successorID == meetingID {
		t.Fatalf("successor id=%q, want a fresh meeting id", successorID)
	}
	if _, ok := app.recordRoomChatMessageWithArtifact(officeRoomID, scoutParticipantName, "finished Research — brief", "os-artifact-stale-2", meetingID); ok {
		t.Fatal("stale-origin delivery leaked into the successor meeting")
	}
	for _, entry := range app.memory.snapshotForMeeting(successorID, 0) {
		if entry.Metadata["artifactId"] == "os-artifact-stale-2" {
			t.Fatalf("successor meeting carries the stale delivery entry: %#v", entry)
		}
	}
}

// deliverArtifactToOrigin must not resurrect an archived channel: archiving is
// a creator-only action, and the owner-context commit would bypass the
// archived-thread guard every user-facing writer enforces. The creator
// notification remains the completion signal.
func TestDeliverArtifactToOriginSkipsArchivedChannel(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth channel", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	if _, err := app.setScoutChatThreadArchived("aj@shareability.com", channel.ID, true); err != nil {
		t.Fatalf("archive channel: %v", err)
	}

	artifact, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing", "# Coyote pricing teardown\n\nEvidence.", "AJ", map[string]string{
		"title":        "Coyote pricing teardown",
		"threadStatus": "complete",
		"status":       "complete",
		"originKind":   agentThreadOriginChannel,
		"originId":     channel.ID,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	app.deliverArtifactToOrigin(artifact, "agent-thread-research-arch")

	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(saved.Messages) != 0 {
		t.Fatalf("archived channel messages=%d, want no completion card", len(saved.Messages))
	}
	if saved.ArchivedAt == "" {
		t.Fatal("channel must stay archived")
	}
	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Metadata["deliveredAt"] != "" {
		t.Fatalf("deliveredAt=%q, want empty when the archived-channel delivery was skipped", stored.Metadata["deliveredAt"])
	}
}

// GATE-FINDINGS G2: a rerun inherits origin metadata only when delivery there
// is still safe for the rerunning user; everything else drops to tool.
func TestRerunOriginForUserConditionalInheritance(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	originArtifact := func(kind string, id string, meetingID string) meetingMemoryEntry {
		metadata := map[string]string{"originKind": kind}
		if id != "" {
			metadata["originId"] = id
		}
		if meetingID != "" {
			metadata["originMeetingId"] = meetingID
		}
		return meetingMemoryEntry{ID: "os-artifact-origin", Metadata: metadata}
	}

	// private-thread origin: the owner inherits it...
	origin := app.rerunOriginForUser(originArtifact(agentThreadOriginPrivateThread, private.ID, ""), "aj@shareability.com")
	if origin["originKind"] != agentThreadOriginPrivateThread || origin["originId"] != private.ID {
		t.Fatalf("owner rerun origin=%v, want the private thread inherited", origin)
	}
	// ...and a NON-owner drops to tool: the rerun must never post into someone
	// else's private thread.
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginPrivateThread, private.ID, ""), "tim@shareability.com")
	if origin["originKind"] != agentThreadOriginTool || origin["originId"] != "" {
		t.Fatalf("non-owner rerun origin=%v, want originKind tool with no originId", origin)
	}

	// channel origin survives while the channel is public and unarchived...
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginChannel, channel.ID, ""), "tim@shareability.com")
	if origin["originKind"] != agentThreadOriginChannel || origin["originId"] != channel.ID {
		t.Fatalf("channel rerun origin=%v, want the public channel inherited", origin)
	}
	// ...but an archived channel drops to tool.
	if _, err := app.setScoutChatThreadArchived("aj@shareability.com", channel.ID, true); err != nil {
		t.Fatalf("archive channel: %v", err)
	}
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginChannel, channel.ID, ""), "tim@shareability.com")
	if origin["originKind"] != agentThreadOriginTool {
		t.Fatalf("archived-channel rerun origin=%v, want tool", origin)
	}

	// room origin survives only while the origin meeting is still active.
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginRoom, "", meetingID), "tim@shareability.com")
	if origin["originKind"] != agentThreadOriginRoom || origin["originMeetingId"] != meetingID {
		t.Fatalf("active-room rerun origin=%v, want room inherited", origin)
	}
	app.memory.rotateMeetingID(officeRoomID)
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginRoom, "", meetingID), "tim@shareability.com")
	if origin["originKind"] != agentThreadOriginTool || origin["originMeetingId"] != "" {
		t.Fatalf("rotated-room rerun origin=%v, want tool", origin)
	}

	// absent / unresolvable origins stay tool.
	origin = app.rerunOriginForUser(meetingMemoryEntry{Metadata: map[string]string{}}, "aj@shareability.com")
	if origin["originKind"] != agentThreadOriginTool {
		t.Fatalf("absent-origin rerun=%v, want tool", origin)
	}
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginChannel, "scout-chat-missing", ""), "aj@shareability.com")
	if origin["originKind"] != agentThreadOriginTool {
		t.Fatalf("missing-channel rerun origin=%v, want tool", origin)
	}
}

// GATE-FINDINGS G2 end to end: a non-owner rerun of a private-thread-origin
// artifact completes without posting anything into the origin thread.
func TestNonOwnerRerunNeverPostsIntoPrivateOriginThread(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return completeResearchArtifactForTest(), nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	stored := meetingMemoryEntry{Metadata: map[string]string{
		"originKind": agentThreadOriginPrivateThread,
		"originId":   private.ID,
	}}

	// the handler computes the safe origin for the rerunning user, then
	// launches and completes the rerun.
	origin := app.rerunOriginForUser(stored, "tim@shareability.com")
	thread, err := app.launchAgentThreadWithOrigin("research", "rerun the brief", "Tim", origin)
	if err != nil {
		t.Fatalf("launchAgentThreadWithOrigin: %v", err)
	}
	app.runAgentThread(thread)

	completed, ok := app.osArtifactByID(thread.Artifact.ID)
	if !ok || completed.Metadata["threadStatus"] != "complete" {
		t.Fatalf("artifact=%#v, want a completed rerun", completed.Metadata)
	}
	if completed.Metadata["originKind"] != agentThreadOriginTool {
		t.Fatalf("originKind=%q, want tool for the non-owner rerun", completed.Metadata["originKind"])
	}
	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", private.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(saved.Messages) != 0 {
		t.Fatalf("victim's private thread got %d message(s) from a non-owner rerun, want none", len(saved.Messages))
	}
}

// Channel-origin completion: when the channel already holds the launch card
// (agent ref) the ref rewrite is the delivery — no duplicate; a rerun without
// a ref appends exactly one completion card.
func TestDeliverArtifactToOriginChannelDedupe(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth channel", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	launchRef := &scoutChatThreadRef{ID: "agent-thread-research-9", Mode: "research", Query: "coyote pricing", Status: "running"}
	if _, err := app.commitScoutChatThreadMessages(channel.OwnerEmail, channel.ID, scoutChatMessageRecord{
		ID:        "scout-chat-message-launch",
		Kind:      "thread",
		Role:      "scout",
		Text:      "research thread launched",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    launchRef,
	}); err != nil {
		t.Fatalf("seed launch card: %v", err)
	}

	artifact, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing", "# Coyote pricing teardown\n\nEvidence.", "AJ", map[string]string{
		"title":        "Coyote pricing teardown",
		"threadStatus": "complete",
		"status":       "complete",
		"originKind":   agentThreadOriginChannel,
		"originId":     channel.ID,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	// The launch card exists: delivery is the ref rewrite, not a new message.
	app.deliverArtifactToOrigin(artifact, "agent-thread-research-9")
	thread, _, err := app.scoutChatThreadByID(channel.OwnerEmail, channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(thread.Messages) != 1 {
		t.Fatalf("channel messages=%d, want the launch card only (no duplicate)", len(thread.Messages))
	}
	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Metadata["deliveredAt"] != "" {
		t.Fatalf("deliveredAt=%q, want empty when the existing ref is the delivery", stored.Metadata["deliveredAt"])
	}

	// A rerun completing under a fresh thread id has no in-channel card yet:
	// exactly one completion card lands, then the retry is a no-op.
	app.deliverArtifactToOrigin(stored, "agent-thread-research-10")
	thread, _, err = app.scoutChatThreadByID(channel.OwnerEmail, channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID after delivery: %v", err)
	}
	if len(thread.Messages) != 2 {
		t.Fatalf("channel messages=%d, want launch card + one completion card", len(thread.Messages))
	}
	card := thread.Messages[len(thread.Messages)-1]
	if card.Kind != "thread" || card.Thread == nil || card.Thread.ArtifactID != artifact.ID || card.Thread.Status != "complete" {
		t.Fatalf("completion card=%#v, want a complete thread ref carrying the artifact id", card)
	}
	if !strings.Contains(card.Text, "finished") || !strings.Contains(card.Text, "Coyote pricing teardown") {
		t.Fatalf("card text=%q, want finished + title", card.Text)
	}

	stored, _ = app.osArtifactByID(artifact.ID)
	if stored.Metadata["deliveredAt"] == "" {
		t.Fatal("deliveredAt must be stamped after the channel delivery")
	}
	app.deliverArtifactToOrigin(stored, "agent-thread-research-10")
	thread, _, _ = app.scoutChatThreadByID(channel.OwnerEmail, channel.ID)
	if len(thread.Messages) != 2 {
		t.Fatalf("channel messages=%d after retry, want still 2", len(thread.Messages))
	}
}

// The live Ember run's death loop, root-caused: agentThreadInstructions
// hard-demands "Markdown sections" and a "one-line Vision" from EVERY child —
// so a writer stage whose contract is a RAW document (packaging_deck_v1: the
// deck HTML file itself) had a system prompt at war with its stage prompt,
// and the model obeyed the system prompt 4 rounds straight into a block. A
// raw-document output contract must REPLACE the generic instructions.
func TestRawDocumentContractOverridesGenericInstructions(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousAsync := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousAsync })

	deck, err := app.launchAgentThreadWithSpec("artifacts", "Ship — the self-contained presenter deck", "AJ", nil, agentThreadGoalSpec{
		OutputContract: "packaging_deck_v1",
		Deliverable:    true,
	})
	if err != nil {
		t.Fatalf("launch deck child: %v", err)
	}
	if deck.Artifact.Metadata["outputContract"] != "packaging_deck_v1" {
		t.Fatalf("outputContract=%q, want the stage contract stamped on the child", deck.Artifact.Metadata["outputContract"])
	}
	instructions := app.agentThreadInstructionsForThread(deck)
	for _, banned := range []string{"Markdown sections", "one-line Vision", "Work decomposition", "Workflow profiles loaded for this run"} {
		if strings.Contains(instructions, banned) {
			t.Fatalf("raw-document child still carries the generic instruction %q:\n%s", banned, instructions)
		}
	}
	if !strings.Contains(instructions, "<!doctype html>") {
		t.Fatalf("raw-document instructions must demand the doctype-first file:\n%s", instructions)
	}

	// A plain child keeps the generic workflow instructions byte-identical.
	plain, err := app.launchAgentThreadWithSpec("artifacts", "meeting notes summary", "AJ", nil, agentThreadGoalSpec{})
	if err != nil {
		t.Fatalf("launch plain child: %v", err)
	}
	if got := app.agentThreadInstructionsForThread(plain); !strings.Contains(got, "Markdown sections") || !strings.Contains(got, "Workflow profiles loaded for this run: "+coworkerWorkflowGoalLoop) {
		t.Fatalf("plain child lost the generic instructions or native workflow profile:\n%s", got)
	}
}

// The founder's "alert us when done": a channel-origin completion must fire a
// COMPANY-WIDE broadcast notification (UserEmail "") — and it must fire even in
// the common case where launchApprovedProposal already posted the live launch
// card, which trips the duplicate-card dedup guard. Before the fix the alert
// sat inside that guarded block and never fired for the primary path.
func TestDeliverArtifactToChannelBroadcastsCompletionDespiteExistingLaunchCard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Samsung", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	// Seed the launch card so the dedup guard WILL trip — the case that used to
	// swallow the notification.
	if _, err := app.commitScoutChatThreadMessages(channel.OwnerEmail, channel.ID, scoutChatMessageRecord{
		ID:        "scout-chat-message-launch",
		Kind:      "thread",
		Role:      "scout",
		Text:      "research thread launched",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: "agent-thread-research-77", Mode: "research", Query: "samsung tv", Status: "running"},
	}); err != nil {
		t.Fatalf("seed launch card: %v", err)
	}

	artifact, _, err := app.createOSArtifactWithMetadata("research", "samsung tv", "# Samsung TV audience\n\nEvidence.", "AJ", map[string]string{
		"title":        "Samsung TV audience report",
		"threadStatus": "complete",
		"status":       "complete",
		"originKind":   agentThreadOriginChannel,
		"originId":     channel.ID,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	before := len(app.notifications)
	app.deliverArtifactToOrigin(artifact, "agent-thread-research-77") // dedup guard trips (launch card exists)

	var broadcast *notificationRecord
	for i := before; i < len(app.notifications); i++ {
		rec := app.notifications[i]
		if rec.UserEmail == "" && strings.Contains(rec.Text, "Samsung TV audience report") {
			broadcast = &app.notifications[i]
			break
		}
	}
	if broadcast == nil {
		t.Fatalf("no company-wide completion notification fired (records added: %d); the alert must survive the dedup guard", len(app.notifications)-before)
	}
	if broadcast.ArtifactID != artifact.ID || broadcast.ThreadID != channel.ID {
		t.Fatalf("notification links wrong: artifact=%q thread=%q, want %q / %q", broadcast.ArtifactID, broadcast.ThreadID, artifact.ID, channel.ID)
	}
	if !strings.Contains(broadcast.Text, "#Samsung") {
		t.Fatalf("notification text=%q, want it to name the channel", broadcast.Text)
	}
}

// A channel-origin launch (what approving a proposal now produces) must NOT
// carry navigation actions in its room-wide broadcast — otherwise every client
// in the room gets yanked to the chat tab. Room/tool origins keep their actions
// (the initiator's own navigation rides a separate direct response).
func TestBroadcastNavigationActionsDropsChannelOriginNav(t *testing.T) {
	actions := []osAssistantAction{{Type: "open_tool", Tool: "chat", ArtifactID: "os-artifact-1"}}

	if got := broadcastNavigationActions(agentThreadOriginChannel, actions); got != nil {
		t.Fatalf("channel origin should drop broadcast navigation actions, got %+v", got)
	}
	if got := broadcastNavigationActions(agentThreadOriginRoom, actions); len(got) != 1 {
		t.Fatalf("room origin should keep its actions, got %+v", got)
	}
	if got := broadcastNavigationActions(agentThreadOriginTool, actions); len(got) != 1 {
		t.Fatalf("tool origin should keep its actions, got %+v", got)
	}
	if got := broadcastNavigationActions("", actions); len(got) != 1 {
		t.Fatalf("empty origin should keep its actions (today's default), got %+v", got)
	}
}

func TestAgentThreadOfficeTelemetryIsBodyFree(t *testing.T) {
	metadata := agentThreadBroadcastMetadata("launch_agent_thread", "agent-thread-private-1", "complete", "listening")
	if len(metadata) != 4 || metadata["tool"] != "launch_agent_thread" || metadata["threadId"] != "agent-thread-private-1" || metadata["status"] != "complete" || metadata["voiceState"] != "listening" {
		t.Fatalf("metadata=%#v, want exact body-free status projection", metadata)
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"artifact", "actions", "originId", "originSurface", "threadQuery", "text"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("office telemetry leaked %q: %s", forbidden, raw)
		}
	}
}

// buildAgentThreadError must not tell the room to "run the Codex handoff" as the
// remedy for a worker error: research/design threads run on the in-process Fable
// orchestrator, and that misleading line made a live meeting believe a failed
// research report was a Codex problem. It must surface the real error and point
// at a retry, not an external Codex worker.
func TestBuildAgentThreadErrorDoesNotPrescribeCodexHandoff(t *testing.T) {
	thread := scoutAgentThread{
		ID:    "agent-thread-research-1",
		Mode:  "research",
		Query: "run a research report on Samsung TV audience",
	}
	body := buildAgentThreadError(thread, errors.New("api request failed (400 Bad Request)"))
	if !strings.Contains(body, "400 Bad Request") {
		t.Fatalf("error body should surface the real worker error, got:\n%s", body)
	}
	if strings.Contains(body, "run the Codex/MCP handoff") || strings.Contains(body, "reconnect the worker or run the Codex") {
		t.Fatalf("error body must not prescribe a Codex handoff as the remedy:\n%s", body)
	}
	if !strings.Contains(strings.ToLower(body), "retry the run") {
		t.Fatalf("error body should point at a retry, got:\n%s", body)
	}
}

// research_brief_v2 bodies open with contract headings, which titled a live
// completed report "Executive Summary". A derived title that is a generic
// contract heading (any mode's contract, case-insensitive) falls back to the
// launch query / stored title; a real subject heading still wins.
func TestAgentThreadDisplayTitleRejectsGenericContractHeadings(t *testing.T) {
	for _, tt := range []struct {
		name     string
		body     string
		fallback string
		want     string
	}{
		{
			name:     "research contract heading falls back to the launch query",
			body:     "# Executive Summary\n\nSamsung is exposed on HBM4 supply.",
			fallback: "Samsung HBM4 exposure",
			want:     "Samsung HBM4 exposure",
		},
		{
			name:     "matching is case-insensitive",
			body:     "## EXECUTIVE SUMMARY\n\nbody",
			fallback: "the launch query",
			want:     "the launch query",
		},
		{
			name:     "tool role heading falls back to the research subject",
			body:     "# ROLE\n\nColton — Research Partner.",
			fallback: "compare Zoom and Otter",
			want:     "compare Zoom and Otter",
		},
		{
			name:     "workflow contract heading falls back",
			body:     "# Vision\n\nbody",
			fallback: "package the Aurora IP",
			want:     "package the Aurora IP",
		},
		{
			name:     "grill contract heading falls back",
			body:     "## Strongest objections\n\nbody",
			fallback: "grill the Q3 pitch",
			want:     "grill the Q3 pitch",
		},
		{
			name:     "real subject heading still wins",
			body:     "# Samsung HBM4 supply outlook\n\nbody",
			fallback: "the launch query",
			want:     "Samsung HBM4 supply outlook",
		},
		{
			name:     "generic derivation with empty fallback keeps the stored title",
			body:     "# Overview\n\nbody",
			fallback: "",
			want:     "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentThreadDisplayTitle(tt.body, tt.fallback); got != tt.want {
				t.Fatalf("agentThreadDisplayTitle=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentThreadArtifactWriterUsesNamedCoworker(t *testing.T) {
	thread := scoutAgentThread{Artifact: meetingMemoryEntry{Metadata: map[string]string{"agentName": "Colton"}}}
	if got := agentThreadArtifactWriter(thread, agentThreadWorkerResult{}); got != "Colton" {
		t.Fatalf("artifact writer=%q, want Colton", got)
	}
	if got := agentThreadArtifactWriter(thread, agentThreadWorkerResult{Metadata: map[string]string{"agentName": "Marvin"}}); got != "Marvin" {
		t.Fatalf("reauthorized artifact writer=%q, want Marvin", got)
	}
	if got := agentThreadArtifactWriter(scoutAgentThread{}, agentThreadWorkerResult{}); got != scoutParticipantName {
		t.Fatalf("ordinary artifact writer=%q, want Scout", got)
	}
}

func TestNamedCoworkerOwnsSingularFirstPersonIdentityAcrossRunnerPrompts(t *testing.T) {
	thread := scoutAgentThread{Mode: "research", Artifact: meetingMemoryEntry{Metadata: map[string]string{
		"agentId": "agent_colton-research", "agentName": "Colton", "agentRole": "Research Partner", "delegatedBy": "Scout",
	}}}
	app := &kanbanBoardApp{}
	writerPrompt := app.agentThreadInstructionsForThread(thread)
	if strings.Contains(writerPrompt, "You are Scout's") || !strings.Contains(writerPrompt, "You are delivering this work as Colton") || !strings.Contains(writerPrompt, "Speak in first person") {
		t.Fatalf("writer prompt has conflicting identity:\n%s", writerPrompt)
	}
	runner := &anthropicFableRunner{app: app}
	system := runner.systemPrompt(AgentJob{Mode: "research", Authority: toolAuthorityReadOnly, thread: thread})
	if !strings.Contains(system, "You are Colton") || strings.Contains(system, "You are Scout, the in-process orchestrator") || !strings.Contains(system, "Scout is the delegator") {
		t.Fatalf("Anthropic prompt has conflicting identity:\n%s", system)
	}
}

// The W0 trigger-surface mapping: fine-grained originSurface stamps win,
// coarse originKind decides otherwise, and a bare launch reads as palette.
func TestAgentThreadTriggerSurfaceMapping(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]string
		want     string
	}{
		{"chat surface", map[string]string{"originSurface": "chat:thread-1"}, triggerSurfaceChatRouter},
		{"goal surface", map[string]string{"originSurface": "goal_door"}, triggerSurfaceGoalDoor},
		{"palette surface", map[string]string{"originSurface": "palette"}, triggerSurfacePalette},
		{"scheduler surface", map[string]string{"originSurface": "scheduler"}, triggerSurfaceScheduler},
		{"suggestion surface", map[string]string{"originSurface": "suggestion_agent"}, triggerSurfaceSuggestionAgent},
		{"channel origin", map[string]string{"originKind": agentThreadOriginChannel}, triggerSurfaceChannel},
		{"room origin", map[string]string{"originKind": agentThreadOriginRoom}, triggerSurfaceRoomVoice},
		{"private thread origin", map[string]string{"originKind": agentThreadOriginPrivateThread}, triggerSurfaceChatRouter},
		{"bare launch", map[string]string{}, triggerSurfacePalette},
	}
	for _, testCase := range cases {
		if got := agentThreadTriggerSurface(testCase.metadata); got != testCase.want {
			t.Fatalf("%s: surface=%q, want %q", testCase.name, got, testCase.want)
		}
	}
}

func TestLaunchAgentThreadRecordsWorkflowProvenance(t *testing.T) {
	dir := boardWorkerLedgerDir(t)
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	thread, err := app.launchAgentThreadWithOrigin("research", "map the churn drivers", "AJ", map[string]string{
		"originKind": agentThreadOriginChannel,
		"originId":   "channel-1",
	})
	if err != nil {
		t.Fatalf("launchAgentThreadWithOrigin: %v", err)
	}

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	var run map[string]any
	var launched map[string]any
	for _, row := range rows {
		switch row["type"] {
		case telemetryTypeWorkflowRun:
			run = row
		case telemetryTypeProposal:
			if row["kind"] == proposalEventLaunched {
				launched = row
			}
		}
	}
	if run == nil {
		t.Fatalf("no workflow_run event recorded at launch; rows=%v", rows)
	}
	entry := run["fields"].(map[string]any)["run"].(map[string]any)
	if entry["workflow_id"] != "agent_thread_research" {
		t.Fatalf("workflow_id=%v, want agent_thread_research", entry["workflow_id"])
	}
	if entry["trigger_surface"] != triggerSurfaceChannel {
		t.Fatalf("trigger_surface=%v, want %q", entry["trigger_surface"], triggerSurfaceChannel)
	}
	if entry["outcome"] != workflowOutcomeLaunched {
		t.Fatalf("outcome=%v, want %q", entry["outcome"], workflowOutcomeLaunched)
	}
	if entry["thread_id"] != thread.ID {
		t.Fatalf("thread_id=%v, want %q", entry["thread_id"], thread.ID)
	}
	if entry["proposer"] != "AJ" {
		t.Fatalf("proposer=%v, want AJ", entry["proposer"])
	}
	if launched == nil {
		t.Fatal("no proposal launched event recorded at launch")
	}
	launchedFields := launched["fields"].(map[string]any)
	if launchedFields["path"] != triggerSurfaceChannel || launchedFields["thread_id"] != thread.ID {
		t.Fatalf("launched fields=%v, want channel path + thread id", launchedFields)
	}
}

func TestAgentRunLogRecordsTerminalProvenance(t *testing.T) {
	dir := boardWorkerLedgerDir(t)
	app := newIsolatedKanbanBoardApp(t)

	artifact := meetingMemoryEntry{
		ID: "artifact-1",
		Metadata: map[string]string{
			"startedAt":    time.Now().UTC().Add(-90 * time.Second).Format(time.RFC3339Nano),
			"proposalId":   "codex-proposal-1",
			"approvalLane": "standard",
			"originKind":   agentThreadOriginRoom,
			"title":        "Churn brief",
		},
	}
	thread := scoutAgentThread{ID: "agent-thread-research-1", Mode: "research", Query: "churn", Artifact: artifact}
	app.appendAgentRunLogEntry(thread, artifact, "complete", "## Executive Summary\nDone.")

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	var run map[string]any
	var terminal map[string]any
	for _, row := range rows {
		switch row["type"] {
		case telemetryTypeWorkflowRun:
			run = row
		case telemetryTypeProposal:
			if row["kind"] == proposalEventTerminal {
				terminal = row
			}
		}
	}
	if run == nil {
		t.Fatalf("no terminal workflow_run event; rows=%v", rows)
	}
	entry := run["fields"].(map[string]any)["run"].(map[string]any)
	if entry["outcome"] != workflowOutcomeCompleted {
		t.Fatalf("outcome=%v, want %q", entry["outcome"], workflowOutcomeCompleted)
	}
	if entry["proposal_id"] != "codex-proposal-1" {
		t.Fatalf("proposal_id=%v, want codex-proposal-1", entry["proposal_id"])
	}
	if entry["trigger_surface"] != triggerSurfaceRoomVoice {
		t.Fatalf("trigger_surface=%v, want %q", entry["trigger_surface"], triggerSurfaceRoomVoice)
	}
	if duration, _ := entry["duration_ms"].(float64); duration <= 0 {
		t.Fatalf("duration_ms=%v, want > 0 from the startedAt stamp", entry["duration_ms"])
	}
	if terminal == nil {
		t.Fatal("no proposal terminal event recorded")
	}
	terminalFields := terminal["fields"].(map[string]any)
	if terminalFields["proposal_id"] != "codex-proposal-1" || terminalFields["outcome"] != "complete" {
		t.Fatalf("terminal fields=%v, want proposal id + complete outcome", terminalFields)
	}
}
