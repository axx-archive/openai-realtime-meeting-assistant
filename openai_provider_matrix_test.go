package main

import "testing"

func TestFounderApprovedOpenAIProviderMatrixIsServerOwned(t *testing.T) {
	// Every retired deployment dial is hostile input for this test. The exact
	// product lane must remain the compiled founder-approved value.
	for name, value := range map[string]string{
		"ANTHROPIC_API_KEY":                   "legacy-key-present",
		"BONFIRE_AGENT_RUNNER":                "",
		"BONFIRE_AGENT_THREAD_WORKER":         "",
		"BONFIRE_CODEX_AGENT_THREADS":         "false",
		"BONFIRE_CODEX_MODEL":                 "client-model",
		"BONFIRE_CODEX_REASONING_EFFORT":      "ultra",
		"EMBEDDINGS_MODEL":                    "client-embedding-model",
		"OPENAI_BRAIN_MODEL":                  "client-brain-model",
		"OPENAI_BRAIN_REASONING_EFFORT":       "ultra",
		"OPENAI_BOARD_MODEL":                  "client-board-model",
		"OPENAI_DICTATION_TRANSCRIPT_MODEL":   "client-dictation-model",
		"OPENAI_IMAGE_MODEL":                  "client-image-model",
		"OPENAI_REALTIME_MODEL":               "client-realtime-model",
		"OPENAI_REALTIME_REASONING_EFFORT":    "minimal",
		"OPENAI_REALTIME_TRANSCRIPTION_MODEL": "client-live-transcribe-model",
		"OPENAI_RESEARCH_MODEL":               "client-research-model",
		"OPENAI_RESEARCH_REASONING_EFFORT":    "ultra",
		"OPENAI_RESPONSES_BASE_URL":           "https://attacker.example/v1",
		"OPENAI_SCOUT_CHAT_MODEL":             "client-chat-model",
		"OPENAI_SCOUT_EXTRACTION_MODEL":       "client-extraction-model",
		"OPENAI_SCOUT_REASONING_EFFORT":       "max",
		"OPENAI_SCOUT_ROUTER_MODEL":           "client-router-model",
		"OPENAI_TRANSCRIPT_MODEL":             "client-transcript-model",
	} {
		t.Setenv(name, value)
	}

	assertRoute := func(name, gotModel, gotEffort, wantModel, wantEffort string) {
		t.Helper()
		if gotModel != wantModel || gotEffort != wantEffort {
			t.Fatalf("%s route=%s/%s, want %s/%s", name, gotModel, gotEffort, wantModel, wantEffort)
		}
	}

	assertRoute("intent", scoutRouterModel(), scoutRouterReasoningEffort(), "gpt-5.6-luna", "medium")
	if got := openAIResponsesURL(); got != "https://api.openai.com/v1/responses" {
		t.Fatalf("Responses endpoint=%q, want fixed OpenAI endpoint", got)
	}
	assertRoute("extraction", scoutExtractionModel(), scoutExtractionReasoningEffort(), "gpt-5.6-luna", "medium")
	assertRoute("conversation", scoutChatModel(), scoutReasoningEffort(), "gpt-5.6-terra", "high")
	assertRoute("proactive", researchSuggestionModel(), scoutRouterReasoningEffort(), "gpt-5.6-luna", "medium")
	assertRoute("brain", meetingBrainModel(), meetingBrainReasoningEffort(), "gpt-5.6-terra", "high")
	assertRoute("board", meetingBoardModel(), meetingBrainReasoningEffort(), "gpt-5.6-terra", "high")
	assertRoute("research", researchModel(), researchReasoningEffort(), "gpt-5.6-sol", "high")
	deliverableThread := scoutAgentThread{Mode: "design", Artifact: meetingMemoryEntry{Metadata: map[string]string{
		"goalDeliverable": "true", "goalParentId": "goal-1", "goalSubtaskId": "writer", "outputContract": "packaging_deck_v1",
	}}}
	assertRoute("deliverable writer", agentThreadTextModel(deliverableThread), agentThreadTextReasoningEffort(deliverableThread), "gpt-5.6-sol", "high")
	deliverableThread.Artifact.Metadata["outputContract"] = packagingStudioImageryDirectionContract
	assertRoute("image direction writer", agentThreadTextModel(deliverableThread), agentThreadTextReasoningEffort(deliverableThread), "gpt-5.6-terra", "high")
	assertRoute("function tools", openAIToolRunnerModel, openAIToolRunnerReasoningEffort, "gpt-5.6-terra", "high")
	assertRoute("goal orchestration", openAIGoalModel(), defaultOpenAIGoalEffort, "gpt-5.6-sol", "high")
	assertRoute("goal review", openAIGoalReviewModel(), defaultOpenAIGoalReviewEffort, "gpt-5.6-sol", "max")
	assertRoute("image direction", scoutImageDirectionModel(), scoutImageDirectionReasoningEffort(), "gpt-5.6-terra", "high")
	assertRoute("narrative", narrativeMaintainerModel(), narrativeMaintainerEffort(), "gpt-5.6-sol", "high")
	assertRoute("codex handoff", codexExecConfigFromEnv().Model, codexExecConfigFromEnv().Reasoning, "gpt-5.6-sol", "high")
	assertRoute("private voice", realtimeModel(), realtimeReasoningEffort(), "gpt-realtime-2.1", "high")
	assertRoute("shared voice", realtimeModel(), realtimeRoomReasoningEffort(), "gpt-realtime-2.1", "medium")

	if houseStyleEffort != "high" || tasteAnalystEffort != "high" || meetingBrainModel() != "gpt-5.6-terra" {
		t.Fatalf("house-style/taste route is not Terra/high")
	}
	if got := defaultOffMeetingSpecialistRealtimeConfig(); got.Model != "gpt-realtime-2.1" || got.ReasoningEffort != "medium" {
		t.Fatalf("meeting specialist route=%s/%s, want shared-room gpt-realtime-2.1/medium", got.Model, got.ReasoningEffort)
	}
	if realtimeTranscriptionModel() != "gpt-live-transcribe" || transcriptionLaneModel() != "gpt-transcribe" || dictationTranscriptionModel() != "gpt-transcribe" {
		t.Fatalf("transcription routes=%s/%s/%s", realtimeTranscriptionModel(), transcriptionLaneModel(), dictationTranscriptionModel())
	}
	if openAIImageModel() != "gpt-image-2" || embeddingModel() != "text-embedding-3-small" {
		t.Fatalf("specialized routes image=%s embedding=%s", openAIImageModel(), embeddingModel())
	}
	route := insightsOpportunitiesStaticRoute()
	assertRoute("insights orchestration", route.Orchestration.Model, route.Orchestration.Effort, "gpt-5.6-sol", "high")
	assertRoute("insights generation", route.Generation.Model, route.Generation.Effort, "gpt-5.6-sol", "high")
	assertRoute("insights review", route.Review.Model, route.Review.Effort, "gpt-5.6-sol", "max")

	if got := selectedAgentRunnerName(); got != agentRunnerOpenAIText {
		t.Fatalf("installed Anthropic key selected runner %q, want %q", got, agentRunnerOpenAIText)
	}
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerAnthropicFable)
	if got := selectedAgentRunnerName(); got != agentRunnerStub {
		t.Fatalf("retired Anthropic assignment selected %q, want fail-closed %q", got, agentRunnerStub)
	}

	for _, effort := range []string{"none", "low", "medium", "high", "xhigh", "max"} {
		if !validOpenAIReasoningEffort(effort) {
			t.Fatalf("documented GPT-5.6 effort %q was rejected", effort)
		}
	}
	for _, effort := range []string{"", "minimal", "ultra", "junk"} {
		if validOpenAIReasoningEffort(effort) {
			t.Fatalf("legacy/unknown GPT-5.6 effort %q was admitted", effort)
		}
	}
}
