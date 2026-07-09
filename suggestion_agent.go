package main

// Item B — proactive research suggestion from passive listening.
//
// The founder wants Scout to VOLUNTEER a research workstream when the room
// discusses one, without anyone saying "Hey Scout". Today the board worker is
// the only ambient path that proposes an agent task, and it fires only on an
// UNMISTAKABLE commitment tied to a board card. Room voice is wake-gated. So a
// room that spends ten minutes chewing on "we should really look into the
// Samsung TV+ opportunity" — never quite committing — leaves no trace and no
// offer to dig in.
//
// This agent closes that gap. It is an agent_runner.go ambient agent that, like
// the board worker, consumes the durable meeting-brain summaries (inputKind
// brain) and, when it detects DISCUSSED-but-not-yet-committed research intent,
// calls proposeCodexTask(mode=research, ...) — the proven propose→confirm→launch
// seam (codex_proposals.go). Its firing bar is deliberately LOOSER than the
// board worker's: this is a SUGGESTION a human approves or dismisses from the
// existing room card + everyone-notification, never an auto-launch, so the
// confirm-first trust model is intact by construction (proposeCodexTask launches
// nothing on its own).
//
// Cursor model (why this agent persists NO artifact of its own): the board
// worker appends a kind=board_update artifact each pass, which doubles as its
// durable consumed-through cursor. A suggestion pass has no artifact worth
// persisting — its only durable output is the codex_proposal, which already
// carries its own broadcast, notification, and recall-exclusion handling. So
// instead of minting a new memory kind (which would need matching entries in
// the search / timeline / boot-resume exclusion lists), this agent advances the
// ambient runner's in-memory per-agent baseline itself at the end of a
// successful pass and hands the runner a sentinel artifactKind that is never
// written. unconsumedEntriesAfter then finds no artifact of that kind and keys
// consumption purely off the advancing baseline. A pass that proposes nothing
// still advances the baseline, so a considered window is never reconsidered; a
// FAILED pass leaves the baseline untouched so the runner's A8 backoff /
// dead-letter machinery still governs retries.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	researchSuggestionAgentName       = "research suggestion"
	defaultResearchSuggestionInterval = 3 * time.Minute
	researchSuggestionRequestTimeout  = 90 * time.Second
	defaultResearchSuggestionMinInput = 1
	defaultResearchSuggestionMaxInput = 4
	// researchSuggestionMaxPerPass caps how many proposals one pass may volunteer
	// so a lively brainstorm cannot bury the room in confirm cards.
	researchSuggestionMaxPerPass = 2
	// researchSuggestionCursorKind is the sentinel artifactKind handed to the
	// ambient runner. NO entry of this kind is ever written: consumption is keyed
	// off the advancing per-agent baseline (see the file header), so the runner's
	// artifact-cursor scan simply finds nothing and honors the baseline floor.
	researchSuggestionCursorKind = "research_suggestion_cursor"
	// researchSuggestionCursorKey is inert for the same reason — no artifact
	// carries it — but is set for symmetry with the other brain consumers.
	researchSuggestionCursorKey = "throughBrainId"
	// researchSuggestionDedupeJaccard is the token-set overlap at or above which
	// a candidate topic is treated as already-proposed / already-running and
	// dropped. Mirrors linkageFuzzyMatchThreshold: a missed dedupe (one extra
	// confirm card) is cheap; re-suggesting a live thread is annoying, so the bar
	// is intentionally on the permissive side of "same topic".
	researchSuggestionDedupeJaccard = 0.6
	// researchSuggestionKnownTopicScan bounds how many recent proposals /
	// artifacts the dedupe context scans.
	researchSuggestionKnownTopicScan = 40
)

// researchSuggestion is one volunteered research workstream parsed from the
// model's pass output.
type researchSuggestion struct {
	Title  string `json:"title"`
	Query  string `json:"query"`
	Reason string `json:"reason,omitempty"`
}

type researchSuggestionAnalysis struct {
	Suggestions []researchSuggestion `json:"suggestions"`
}

func researchSuggestionAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              researchSuggestionAgentName,
		defaultInterval:   defaultResearchSuggestionInterval,
		intervalEnv:       "RESEARCH_SUGGESTION_INTERVAL",
		disabledEnv:       "RESEARCH_SUGGESTION_DISABLED",
		backfillEnv:       "RESEARCH_SUGGESTION_BACKFILL",
		minBatchEnv:       "RESEARCH_SUGGESTION_MIN_INPUTS",
		defaultMinBatch:   defaultResearchSuggestionMinInput,
		maxBatchEnv:       "RESEARCH_SUGGESTION_MAX_INPUTS",
		defaultMaxBatch:   defaultResearchSuggestionMaxInput,
		inputKind:         meetingMemoryKindBrain,
		artifactKind:      researchSuggestionCursorKind,
		cursorMetadataKey: researchSuggestionCursorKey,
		requestTimeout:    researchSuggestionRequestTimeout,
		roomScoped:        true, // W4 §7.4: per-room brain windows + baselines
		produce:           (*kanbanBoardApp).produceResearchSuggestions,
	}
}

func (app *kanbanBoardApp) startResearchSuggestionWorker(apiKey string) {
	app.startAmbientAgent(researchSuggestionAgent(), apiKey)
}

// runResearchSuggestionOnce runs a single pass with an injectable responder —
// the test seam mirroring runMeetingBoardOnce.
func (app *kanbanBoardApp) runResearchSuggestionOnce(ctx context.Context, apiKey string, responder openAITextResponder) (meetingMemoryEntry, error) {
	agent := researchSuggestionAgent()
	return app.runAmbientAgentOnce(agent, ctx, apiKey, responder, agent.minBatch())
}

func (app *kanbanBoardApp) produceResearchSuggestions(ctx context.Context, apiKey string, summaries []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	if len(summaries) == 0 {
		return meetingMemoryEntry{}, nil
	}
	roomID := ambientWindowRoomID(summaries)
	baselineKey := ambientAgentKey(researchSuggestionAgentName, roomID)
	windowLast := summaries[len(summaries)-1]
	// §7.3 layer 1: listen-only brains are excluded from the suggestion window
	// while the baseline still advances (this agent's own skip-while-advancing
	// idiom) — a guest-exposed sitting never volunteers a proposal.
	summaries, _ = app.filterListenOnly(summaries)
	if len(summaries) == 0 {
		app.setAmbientAgentBaselineID(baselineKey, windowLast.ID)
		return meetingMemoryEntry{}, nil
	}

	known := app.existingResearchTopicStrings(researchSuggestionKnownTopicScan)
	model := researchSuggestionModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:        model,
		Instructions: researchSuggestionInstructions(),
		Input:        buildResearchSuggestionInput(summaries, known, app.participantSnapshotForRoom(roomID), time.Now().UTC()),
		// A suggestion is lighter than a board mutation, but the output is still
		// structured JSON the parse depends on; medium buys reliable shape for a
		// cheap, infrequent step (the board worker's A2 reasoning about effort).
		ReasoningEffort: "medium",
		Verbosity:       "low",
		MaxOutputTokens: 700,
	})
	// A model failure must NOT advance the baseline: leaving it put lets the
	// ambient runner's A8 backoff / dead-letter machinery govern the retry
	// exactly as it does for every other brain consumer.
	if err != nil {
		return meetingMemoryEntry{}, err
	}

	analysis, err := parseResearchSuggestionAnalysis(text)
	if err != nil {
		return meetingMemoryEntry{}, err
	}

	proposed := 0
	for _, suggestion := range analysis.Suggestions {
		if proposed >= researchSuggestionMaxPerPass {
			break
		}
		title := canonicalizeBoardText(suggestion.Title)
		query := canonicalizeBoardText(suggestion.Query)
		if title == "" || query == "" {
			continue
		}
		// Programmatic dedupe backstop to the prompt's own "do not re-suggest"
		// instruction: never volunteer a topic that already has an open proposal
		// or a running/finished research thread (which covers a room that already
		// @scout-launched it). known already holds those topics.
		if researchTopicIsKnown(title+" "+query, known) {
			continue
		}
		if _, _, err := app.proposeCodexTask(map[string]any{
			"title":          title,
			"mode":           "research",
			"query":          query,
			"origin_room_id": roomID,
		}, "suggestion_worker"); err != nil {
			// Log-and-continue: a single rejected suggestion (e.g. an empty field
			// the model slipped through) must not fail the whole pass and re-feed
			// the window. The others still get their shot.
			log.Errorf("research suggestion propose failed for %q: %v", title, err)
			continue
		}
		// Only the topics we actually proposed extend the dedupe set within this
		// pass, so two near-identical suggestions in one batch collapse to one.
		known = append(known, title+" "+query)
		proposed++
	}

	// Successful pass (proposed something or deliberately nothing): advance the
	// in-memory baseline past this window so it is never reconsidered. This IS
	// the agent's cursor — see the file header on why no artifact is persisted.
	// W4: the baseline keys on (agent, room) and advances through the ORIGINAL
	// window's tail so a filtered listen-only trailing brain never re-feeds.
	app.setAmbientAgentBaselineID(baselineKey, windowLast.ID)
	return meetingMemoryEntry{}, nil
}

// existingResearchTopicStrings gathers the "title — query" text of research
// work the room already has in flight or on record: every codex_proposal in
// research mode (any status — a pending confirm, a launched confirm, or a human
// dismissal all mean "already offered, do not re-offer") and every os_artifact
// in research mode (a launched thread from a confirmed proposal OR a direct
// @scout research launch). These feed both the model's dedupe context and the
// programmatic backstop.
func (app *kanbanBoardApp) existingResearchTopicStrings(limit int) []string {
	if app == nil || app.memory == nil {
		return nil
	}
	topics := make([]string, 0, limit)
	seen := map[string]struct{}{}
	add := func(title, query string) {
		text := strings.TrimSpace(strings.TrimSpace(title) + " " + strings.TrimSpace(query))
		if text == "" {
			return
		}
		key := strings.ToLower(text)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		topics = append(topics, text)
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindCodexProposal, limit) {
		if strings.EqualFold(strings.TrimSpace(entry.Metadata["mode"]), "research") {
			add(entry.Metadata["title"], entry.Metadata["query"])
		}
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, limit) {
		if strings.EqualFold(strings.TrimSpace(entry.Metadata["mode"]), "research") {
			add(entry.Metadata["title"], entry.Metadata["query"])
		}
	}
	return topics
}

// researchTopicIsKnown reports whether candidate overlaps an already-known
// research topic at or above the dedupe threshold (token-set Jaccard, the
// board-card matcher's measure).
func researchTopicIsKnown(candidate string, known []string) bool {
	candidateTokens := linkageMatchTokens(candidate)
	if len(candidateTokens) == 0 {
		return false
	}
	for _, topic := range known {
		if tokenSetJaccard(candidateTokens, linkageMatchTokens(topic)) >= researchSuggestionDedupeJaccard {
			return true
		}
	}
	return false
}

func researchSuggestionModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_SUGGESTION_MODEL")); model != "" {
		return model
	}
	return meetingBrainModel()
}

func researchSuggestionInstructions() string {
	return strings.Join([]string{
		"You are Scout's ambient research-suggestion worker for Bonfire, listening passively to a live meeting.",
		"Your job is to notice when the room is DISCUSSING a question or workstream worth researching and volunteer it as a research task the team can approve with one click — WITHOUT anyone addressing Scout.",
		"Use ONLY the supplied meeting brain summaries. Do not invent topics, clients, people, or facts the summaries do not contain.",
		"Fire on DISCUSSED-but-not-yet-committed research intent — a looser bar than a firm commitment. Good triggers: \"we should look into / dig into / research X\", \"someone should figure out X\", \"that would be an interesting project\", an open strategic or competitive question the team keeps circling, or a market/technical/opportunity unknown they clearly want answered.",
		"A research task means producing a read-only written deliverable: a brief, an analysis, a comparison, a landscape, a pressure-test of an assumption. Never anything that commits, deploys, or changes an external system.",
		"Do NOT suggest: work the team already firmly committed and owns (the board worker captures that), casual asides or filler, pure opinions with no researchable question, or a decision that needs no outside investigation.",
		"Do NOT suggest anything already listed under \"Already proposed or running research\" — those are in flight or on record; re-offering them is noise.",
		"Suggest at most two research tasks per pass, and only your most clearly-warranted ones. When in doubt between two facets of the same topic, propose ONE combined task.",
		"Each suggestion is a SUGGESTION: a human confirms or dismisses it, and nothing launches until they do. Phrase the query as a read-only deliverable to research, draft, or analyze.",
		"Return strict JSON only, shape: {\"suggestions\":[{\"title\":\"short human title\",\"query\":\"what the research should produce\",\"reason\":\"the discussion that warrants it\"}]}.",
		"When nothing in the summaries warrants a research suggestion, return {\"suggestions\":[]}. Silence is the right answer far more often than not.",
	}, " ")
}

func buildResearchSuggestionInput(summaries []meetingMemoryEntry, knownTopics []string, participants []string, generatedAt time.Time) string {
	location := meetingTimeLocation()

	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.In(location).Format(time.RFC1123))
	builder.WriteString("\n\n# Active participants\n")
	if len(participants) == 0 {
		builder.WriteString("Unknown\n")
	} else {
		builder.WriteString(strings.Join(participants, ", "))
		builder.WriteByte('\n')
	}

	builder.WriteString("\n# Already proposed or running research\n")
	if len(knownTopics) == 0 {
		builder.WriteString("None yet.\n")
	} else {
		for _, topic := range knownTopics {
			builder.WriteString("- ")
			builder.WriteString(topic)
			builder.WriteByte('\n')
		}
	}

	builder.WriteString("\n# Meeting brain summaries to consider\n")
	for _, entry := range summaries {
		builder.WriteString("- id=")
		builder.WriteString(entry.ID)
		builder.WriteString(" time=")
		builder.WriteString(entry.CreatedAt.In(location).Format(time.RFC3339))
		builder.WriteByte('\n')
		for _, line := range strings.Split(entry.Text, "\n") {
			builder.WriteString("  ")
			builder.WriteString(strings.TrimSpace(line))
			builder.WriteByte('\n')
		}
	}

	return builder.String()
}

func parseResearchSuggestionAnalysis(text string) (researchSuggestionAnalysis, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return researchSuggestionAnalysis{}, fmt.Errorf("research suggestion analysis was empty")
	}

	candidates := []string{text}
	if fenced := stripJSONCodeFence(text); fenced != text {
		candidates = append(candidates, fenced)
	}
	if object := extractJSONCandidate(text); object != "" && object != text {
		candidates = append(candidates, object)
	}

	var lastErr error
	for _, candidate := range candidates {
		if analysis, err := decodeResearchSuggestionAnalysis(candidate); err == nil {
			return analysis, nil
		} else {
			lastErr = err
		}
	}

	return researchSuggestionAnalysis{}, fmt.Errorf("parse research suggestion analysis: %w", lastErr)
}

func decodeResearchSuggestionAnalysis(text string) (researchSuggestionAnalysis, error) {
	var analysis struct {
		Suggestions []researchSuggestion `json:"suggestions"`
		Tasks       []researchSuggestion `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &analysis); err == nil {
		suggestions := analysis.Suggestions
		if len(suggestions) == 0 {
			suggestions = analysis.Tasks
		}
		return researchSuggestionAnalysis{Suggestions: suggestions}, nil
	}

	var suggestions []researchSuggestion
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &suggestions); err != nil {
		return researchSuggestionAnalysis{}, err
	}

	return researchSuggestionAnalysis{Suggestions: suggestions}, nil
}
