package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContextEntriesForQueryIncludesMentionedParticipantsAndYesterday(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	location := meetingTimeLocation()
	now := time.Date(2026, 5, 19, 10, 0, 0, 0, location)
	yesterday := time.Date(2026, 5, 18, 15, 0, 0, 0, location).UTC()
	twoDaysAgo := time.Date(2026, 5, 17, 15, 0, 0, 0, location).UTC()

	store.entries = []meetingMemoryEntry{
		{
			ID:        "tom-yesterday",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tom: Boot Barn meeting went well.",
			CreatedAt: yesterday,
			Metadata:  map[string]string{"speaker": "Tom"},
		},
		{
			ID:        "tyler-yesterday",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tyler: Tom and Tyler talked about next steps.",
			CreatedAt: yesterday.Add(10 * time.Minute),
			Metadata:  map[string]string{"speaker": "Tyler"},
		},
		{
			ID:        "tom-old",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tom: Older unrelated update.",
			CreatedAt: twoDaysAgo,
			Metadata:  map[string]string{"speaker": "Tom"},
		},
	}

	entries := store.contextEntriesForQuery("what did Tom and Tyler talk about yesterday?", 10, now)
	if !memoryEntriesContain(entries, "tom-yesterday") {
		t.Fatal("context missing Tom's yesterday transcript")
	}
	if !memoryEntriesContain(entries, "tyler-yesterday") {
		t.Fatal("context missing Tyler's yesterday transcript")
	}
	if memoryEntriesContain(entries, "tom-old") {
		t.Fatal("context should keep yesterday-scoped questions inside yesterday's transcript window")
	}
}

func TestContextEntriesForQueryUnderstandsRecentDuration(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	location := meetingTimeLocation()
	now := time.Date(2026, 5, 19, 10, 0, 0, 0, location)
	store.entries = []meetingMemoryEntry{
		{
			ID:        "ten-minutes-ago",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tom: Boot Barn follow-up sounded positive.",
			CreatedAt: now.Add(-10 * time.Minute).UTC(),
			Metadata:  map[string]string{"speaker": "Tom"},
		},
		{
			ID:        "twenty-minutes-ago",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tyler: Older unrelated topic.",
			CreatedAt: now.Add(-20 * time.Minute).UTC(),
			Metadata:  map[string]string{"speaker": "Tyler"},
		},
		{
			ID:        "one-minute-ago",
			Kind:      meetingMemoryKindTranscript,
			Text:      "AJ: New unrelated topic.",
			CreatedAt: now.Add(-1 * time.Minute).UTC(),
			Metadata:  map[string]string{"speaker": "AJ"},
		},
	}

	entries := store.contextEntriesForQuery("what happened 10 minutes ago?", 10, now)
	if !memoryEntriesContain(entries, "ten-minutes-ago") {
		t.Fatal("context missing transcript from around 10 minutes ago")
	}
	if memoryEntriesContain(entries, "twenty-minutes-ago") {
		t.Fatal("context should not include transcript from 20 minutes ago")
	}
	if memoryEntriesContain(entries, "one-minute-ago") {
		t.Fatal("context should not include transcript from 1 minute ago for a 10-minutes-ago query")
	}
}

func TestContextEntriesForQueryUnderstandsPastDuration(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	location := meetingTimeLocation()
	now := time.Date(2026, 5, 19, 10, 0, 0, 0, location)
	store.entries = []meetingMemoryEntry{
		{
			ID:        "recent",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tom: Recent Boot Barn note.",
			CreatedAt: now.Add(-5 * time.Minute).UTC(),
			Metadata:  map[string]string{"speaker": "Tom"},
		},
		{
			ID:        "old",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tom: Older Boot Barn note.",
			CreatedAt: now.Add(-15 * time.Minute).UTC(),
			Metadata:  map[string]string{"speaker": "Tom"},
		},
	}

	entries := store.contextEntriesForQuery("what did Tom say in the last 10 minutes?", 10, now)
	if !memoryEntriesContain(entries, "recent") {
		t.Fatal("context missing recent transcript")
	}
	if memoryEntriesContain(entries, "old") {
		t.Fatal("context should not include transcript older than the requested recent window")
	}
}

func TestAssistantQueryAnswersCurrentBoardCardStatus(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, changed, err := app.createTicket(map[string]any{
		"title":  "Dog Perfect",
		"notes":  "Waiting on Erick for launch approval.",
		"owner":  "Erick",
		"tags":   []any{"client", "approval"},
		"status": "Blocked",
	}); err != nil {
		t.Fatalf("createTicket: %v", err)
	} else if !changed {
		t.Fatal("createTicket changed=false, want true")
	}

	result, changed, err := app.answerAssistantQuery("what is the current status of DogPerfect?")
	if err != nil {
		t.Fatalf("answerAssistantQuery: %v", err)
	}
	if changed {
		t.Fatal("answerAssistantQuery changed=true, want false")
	}

	answer := asString(result["answer"])
	for _, want := range []string{"Dog Perfect", "Blocked", "Erick"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer=%q, missing %q", answer, want)
		}
	}
	if source := asString(result["source"]); source != "board" {
		t.Fatalf("source=%q, want board", source)
	}
}

func memoryEntriesContain(entries []meetingMemoryEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}

	return false
}

// Completed artifacts earn a real display title from the body: first markdown
// heading wins, then a "Title:" line, then a short first line — mode scaffold
// openers never become titles, and an unusable body keeps the fallback.
func TestArtifactTitleFromBody(t *testing.T) {
	for _, tt := range []struct {
		name     string
		body     string
		fallback string
		want     string
	}{
		{
			name:     "first heading wins and sheds punctuation",
			body:     "## Coyote pricing teardown.\n\nEvidence follows.",
			fallback: "dig into coyote pricing",
			want:     "Coyote pricing teardown",
		},
		{
			name:     "scaffold opener heading is skipped for the real one",
			body:     "# Scout work thread\n\n## Realtime margin audit\n\nbody",
			fallback: "prompt",
			want:     "Realtime margin audit",
		},
		{
			name:     "title line beats the first plain line",
			body:     "Research brief\n\nTitle: Q3 pipeline reconciliation\n\nDetails.",
			fallback: "prompt",
			want:     "Q3 pipeline reconciliation",
		},
		{
			name:     "short first line is a title",
			body:     "Margin plan for Q3\n\nLong details follow here.",
			fallback: "prompt",
			want:     "Margin plan for Q3",
		},
		{
			name:     "overlong first line keeps the fallback",
			body:     strings.Repeat("margin ", 20) + "\n\nbody",
			fallback: "the original prompt",
			want:     "the original prompt",
		},
		{
			name:     "scaffold-only body keeps the fallback",
			body:     "Scout work thread\n\nStatus: running",
			fallback: "the original prompt",
			want:     "the original prompt",
		},
		{
			name:     "empty body keeps the fallback",
			body:     "",
			fallback: "the original prompt",
			want:     "the original prompt",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := artifactTitleFromBody(tt.body, tt.fallback); got != tt.want {
				t.Fatalf("artifactTitleFromBody=%q, want %q", got, tt.want)
			}
		})
	}
}

// Scout retrieval: a completed artifact whose title matches the query enters
// query context truncated to budget (with the full-artifact marker), while a
// running scaffold with the same subject stays out.
func TestContextEntriesForQueryBudgetsCompletedArtifactBodies(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	longBody := "# Coyote pricing teardown\n\n" + strings.Repeat("Carrier margin evidence with routing detail. ", 80)
	completed, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing", longBody, "AJ", map[string]string{
		"title":        "Coyote pricing teardown",
		"threadStatus": "complete",
		"status":       "complete",
	})
	if err != nil {
		t.Fatalf("create completed artifact: %v", err)
	}
	running, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing second pass", "Scout work thread\n\nVision: coyote pricing second pass", "AJ", map[string]string{
		"title":        "Coyote pricing second pass",
		"threadStatus": "running",
		"status":       "running",
	})
	if err != nil {
		t.Fatalf("create running scaffold: %v", err)
	}

	entries := app.memory.contextEntriesForQuery("what did the coyote pricing teardown conclude", 12, time.Now())
	var artifactEntry *meetingMemoryEntry
	for index := range entries {
		if entries[index].ID == completed.ID {
			artifactEntry = &entries[index]
		}
		if entries[index].ID == running.ID {
			t.Fatal("running scaffold must not enter query context")
		}
	}
	if artifactEntry == nil {
		t.Fatalf("completed artifact %s missing from context entries", completed.ID)
	}
	if len([]rune(artifactEntry.Text)) >= len([]rune(longBody)) {
		t.Fatalf("artifact context len=%d, want truncated below the raw body (%d)", len(artifactEntry.Text), len(longBody))
	}
	if !strings.Contains(artifactEntry.Text, "[truncated — full artifact id="+completed.ID) {
		t.Fatalf("artifact context missing the truncation marker: %q", artifactEntry.Text[len(artifactEntry.Text)-160:])
	}
}

// Recall/report-flavored questions that name a completed artifact at title
// strength skip the board short-circuit so the model answers from the
// artifact body; plain board questions keep the fast path.
func TestQueryPrefersArtifactContext(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, _, err := app.createOSArtifactWithMetadata("research", "reconcile coyote pricing against the q3 board", "# Coyote pricing teardown\n\nEvidence.", "AJ", map[string]string{
		"title":        "Coyote pricing teardown",
		"threadStatus": "complete",
		"status":       "complete",
	}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	if !app.queryPrefersArtifactContext("compare the coyote pricing teardown with the q3 board figures") {
		t.Fatal("artifact-naming comparison question must prefer artifact context")
	}
	if app.queryPrefersArtifactContext("what is on the board right now") {
		t.Fatal("plain board question must keep the board short-circuit")
	}
	if app.queryPrefersArtifactContext("compare the roadmap cards") {
		t.Fatal("flavored question naming no artifact must keep the board short-circuit")
	}
}

// With an Anthropic key present, scout chat Q&A routes to Sonnet 5 with the
// re-baselined 800-token budget at effort low; the gpt-5.5 responder must not
// be touched even when an OpenAI key is also configured.
func TestAnswerAssistantQueryRoutesToSonnetWithAnthropicKey(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-key"
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("BONFIRE_CHAT_MODEL", "")

	var got anthropicTextRequest
	swapAnthropicTextResponder(t, func(_ context.Context, apiKey string, request anthropicTextRequest) (string, error) {
		if apiKey != "sk-ant-test" {
			t.Fatalf("apiKey=%q, want the Anthropic key", apiKey)
		}
		got = request
		return "Pricing locked at $99/mo.", nil
	})
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("OpenAI responder must not run when an Anthropic key is present")
		return "", nil
	})

	answer, err := app.answerAssistantQueryWithModel(context.Background(), "what did we decide on pricing?", nil, nil, nil)
	if err != nil {
		t.Fatalf("answerAssistantQueryWithModel: %v", err)
	}
	if answer != "Pricing locked at $99/mo." {
		t.Fatalf("answer=%q, want the Sonnet answer", answer)
	}
	if got.Model != "claude-sonnet-5" {
		t.Fatalf("model=%q, want claude-sonnet-5", got.Model)
	}
	if got.MaxTokens != 800 || got.Effort != "low" {
		t.Fatalf("chat budget=%d/%q, want 800/low", got.MaxTokens, got.Effort)
	}
	if got.Instructions != assistantQueryInstructions() {
		t.Fatal("Sonnet request must carry the same assistant-query instructions as the OpenAI path")
	}
	if !strings.Contains(got.Input, "what did we decide on pricing?") {
		t.Fatalf("input missing the query: %q", got.Input)
	}
}

// Keyless-Anthropic keeps the gpt-5.5 Responses path byte-for-byte: same
// model dial, same 500-token budget, Anthropic seam untouched.
func TestAnswerAssistantQueryKeylessAnthropicKeepsOpenAIPath(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-key"
	t.Setenv("ANTHROPIC_API_KEY", "")

	var got openAITextRequest
	swapOpenAITextResponder(t, func(_ context.Context, apiKey string, request openAITextRequest) (string, error) {
		if apiKey != "openai-key" {
			t.Fatalf("apiKey=%q, want the OpenAI key", apiKey)
		}
		got = request
		return "Pricing locked at $99/mo.", nil
	})
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("Anthropic responder must not run keyless")
		return "", nil
	})

	if _, err := app.answerAssistantQueryWithModel(context.Background(), "what did we decide on pricing?", nil, nil, nil); err != nil {
		t.Fatalf("answerAssistantQueryWithModel: %v", err)
	}
	if got.Model != meetingBrainModel() {
		t.Fatalf("model=%q, want meetingBrainModel()", got.Model)
	}
	if got.MaxOutputTokens != 500 || got.ReasoningEffort != "low" {
		t.Fatalf("openai budget=%d/%q, want unchanged 500/low", got.MaxOutputTokens, got.ReasoningEffort)
	}
}

// Fully keyless (no OpenAI, no Anthropic) keeps today's polite configuration
// error — the app never crashes or silently answers.
func TestAnswerAssistantQueryKeylessBothStillErrors(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = ""
	t.Setenv("ANTHROPIC_API_KEY", "")

	if _, err := app.answerAssistantQueryWithModel(context.Background(), "anything", nil, nil, nil); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("keyless err=%v, want the OPENAI_API_KEY configuration error", err)
	}
}

// The memory Q&A path follows the same routing rule: Sonnet 5 with the
// 800-token chat budget when an Anthropic key is present.
func TestAnswerMemoryQuestionRoutesToSonnetWithAnthropicKey(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-key"
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("BONFIRE_CHAT_MODEL", "")

	var got anthropicTextRequest
	swapAnthropicTextResponder(t, func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		got = request
		return "We locked pricing at $99/mo.", nil
	})
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("OpenAI responder must not run when an Anthropic key is present")
		return "", nil
	})

	entries := []meetingMemoryEntry{{
		ID:        "decision-pricing",
		Kind:      "decision",
		Text:      "We locked pricing at $99/mo with two design partners.",
		CreatedAt: time.Now().UTC(),
		Metadata:  map[string]string{},
	}}
	answer, err := app.answerMemoryQuestionWithModel("what did we decide on pricing?", entries)
	if err != nil {
		t.Fatalf("answerMemoryQuestionWithModel: %v", err)
	}
	if answer != "We locked pricing at $99/mo." {
		t.Fatalf("answer=%q, want the Sonnet answer", answer)
	}
	if got.Model != "claude-sonnet-5" || got.MaxTokens != 800 || got.Effort != "low" {
		t.Fatalf("memory Q&A request=%q %d/%q, want claude-sonnet-5 800/low", got.Model, got.MaxTokens, got.Effort)
	}
	if got.Instructions != memoryQuestionInstructions() {
		t.Fatal("Sonnet request must carry the same memory-question instructions as the OpenAI path")
	}
	if !strings.Contains(got.Input, "We locked pricing at $99/mo with two design partners.") {
		t.Fatalf("input missing the memory context: %q", got.Input)
	}
}

// Keyless-Anthropic memory Q&A keeps the gpt-5.5 path unchanged (700-token
// budget), and the zero-entries early return survives on both routes.
func TestAnswerMemoryQuestionKeylessAnthropicKeepsOpenAIPath(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-key"
	t.Setenv("ANTHROPIC_API_KEY", "")

	var got openAITextRequest
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		got = request
		return "answer", nil
	})
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("Anthropic responder must not run keyless")
		return "", nil
	})

	if answer, err := app.answerMemoryQuestionWithModel("anything", nil); err != nil || answer != "" {
		t.Fatalf("zero-entries answer=%q err=%v, want empty/nil early return", answer, err)
	}

	entries := []meetingMemoryEntry{{
		ID:        "decision-pricing",
		Kind:      "decision",
		Text:      "We locked pricing at $99/mo.",
		CreatedAt: time.Now().UTC(),
		Metadata:  map[string]string{},
	}}
	if _, err := app.answerMemoryQuestionWithModel("what did we decide?", entries); err != nil {
		t.Fatalf("answerMemoryQuestionWithModel: %v", err)
	}
	if got.MaxOutputTokens != 700 || got.Model != meetingBrainModel() {
		t.Fatalf("openai budget=%d model=%q, want unchanged 700/meetingBrainModel()", got.MaxOutputTokens, got.Model)
	}
}
