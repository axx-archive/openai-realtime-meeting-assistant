package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMemoryQuestionContextLimit = 60
	memoryQuestionRequestTimeout      = 45 * time.Second
	assistantQueryRequestTimeout      = 25 * time.Second
	defaultMeetingTimeZone            = "America/Los_Angeles"
)

var (
	pastDurationQueryPattern = regexp.MustCompile(`\b(?:last|past)\s+(\d{1,3})\s*(minutes?|mins?|hours?|hrs?)\b`)
	agoDurationQueryPattern  = regexp.MustCompile(`\b(\d{1,3})\s*(minutes?|mins?|hours?|hrs?)\s+ago\b`)
)

// assistantQueryResult is the broadcast-free outcome of answering a query, so
// the room-wide ask bar and the private scout chat share one answer engine.
type assistantQueryResult struct {
	query        string
	answer       string
	source       string // "board" or "assistant"
	matchedCards int
	matches      int
	contextSize  int
}

func (app *kanbanBoardApp) answerAssistantQuery(query string) (map[string]any, bool, error) {
	result, err := app.resolveAssistantQuery(query, nil)
	if err != nil {
		return nil, false, err
	}

	if result.source == "board" {
		broadcastAssistantEvent("answer", result.answer, map[string]any{
			"query":        result.query,
			"source":       "board",
			"matchedCards": result.matchedCards,
		})
		return map[string]any{
			"ok":           true,
			"query":        result.query,
			"answer":       result.answer,
			"source":       "board",
			"matchedCards": result.matchedCards,
		}, false, nil
	}

	broadcastAssistantEvent("answer", result.answer, map[string]any{
		"query":  result.query,
		"source": "assistant",
	})

	return map[string]any{
		"ok":      true,
		"query":   result.query,
		"answer":  result.answer,
		"source":  "assistant",
		"matches": result.matches,
		"context": result.contextSize,
	}, false, nil
}

// resolveAssistantQuery answers from the current board and the shared room
// memory store without broadcasting anything. history threads prior private
// chat turns into the model input so follow-up questions work.
func (app *kanbanBoardApp) resolveAssistantQuery(query string, history []scoutChatTurn) (assistantQueryResult, error) {
	query = canonicalizeBoardText(query)
	if query == "" {
		return assistantQueryResult{}, fmt.Errorf("query is required")
	}

	if answer, matchedCards, ok := app.answerCurrentBoardQuestion(query); ok {
		return assistantQueryResult{
			query:        query,
			answer:       answer,
			source:       "board",
			matchedCards: matchedCards,
		}, nil
	}

	matches, contextEntries := app.memoryMatchesAndContext(query)
	board := app.snapshotState()
	answer, modelErr := app.answerAssistantQueryWithModel(query, board.Cards, contextEntries, history)
	if modelErr != nil {
		log.Errorf("Failed to answer assistant query with model: %v", modelErr)
	}
	if strings.TrimSpace(answer) == "" {
		answer = buildMemoryAnswer(query, matches)
	}

	return assistantQueryResult{
		query:       query,
		answer:      answer,
		source:      "assistant",
		matches:     len(matches),
		contextSize: len(contextEntries),
	}, nil
}

func (app *kanbanBoardApp) answerAssistantQueryWithModel(query string, cards []kanbanCard, entries []meetingMemoryEntry, history []scoutChatTurn) (string, error) {
	if app == nil {
		return "", fmt.Errorf("assistant is unavailable")
	}

	app.mu.Lock()
	apiKey := app.apiKey
	app.mu.Unlock()
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), assistantQueryRequestTimeout)
	defer cancel()

	return createOpenAITextResponse(ctx, apiKey, openAITextRequest{
		Model:           meetingBrainModel(),
		Instructions:    assistantQueryInstructions(),
		Input:           buildAssistantQueryInput(query, cards, entries, history, time.Now()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: 500,
	})
}

func assistantQueryInstructions() string {
	return strings.Join([]string{
		"You are Scout, the Bonfire meeting assistant.",
		"Answer using the supplied current Kanban board and memory context only.",
		"Use the current board as source of truth for present card status, owner, notes, tags, due date, and key dates.",
		"Use memory only for past discussion, decisions, transcript recall, or archived meeting questions.",
		"If the board contains a relevant card, do not say you cannot see the current status.",
		"If the context does not answer the question, say what you could not find instead of guessing.",
		"When a conversation history is supplied, resolve follow-up references from it.",
		"Keep the answer concise and practical.",
	}, " ")
}

func buildAssistantQueryInput(query string, cards []kanbanCard, entries []meetingMemoryEntry, history []scoutChatTurn, now time.Time) string {
	location := meetingTimeLocation()
	boardJSON, err := json.MarshalIndent(cards, "", "  ")
	if err != nil {
		boardJSON = []byte("[]")
	}

	var builder strings.Builder
	builder.WriteString("# Current time\n")
	builder.WriteString(now.In(location).Format(time.RFC1123))
	if len(history) > 0 {
		builder.WriteString("\n\n# Conversation so far\n")
		for _, turn := range history {
			builder.WriteString(turn.role)
			builder.WriteString(": ")
			builder.WriteString(turn.text)
			builder.WriteByte('\n')
		}
	}
	builder.WriteString("\n\n# User question\n")
	builder.WriteString(query)
	builder.WriteString("\n\n# Current Kanban board\n")
	builder.Write(boardJSON)
	builder.WriteString("\n\n# Memory context\n")
	if len(entries) == 0 {
		builder.WriteString("No matching memory context.\n")
		return builder.String()
	}
	for _, entry := range entries {
		builder.WriteString("- id=")
		builder.WriteString(entry.ID)
		builder.WriteString(" kind=")
		builder.WriteString(entry.Kind)
		builder.WriteString(" time=")
		builder.WriteString(entry.CreatedAt.In(location).Format(time.RFC3339))
		if speaker := strings.TrimSpace(entry.Metadata["speaker"]); speaker != "" {
			builder.WriteString(" speaker=")
			builder.WriteString(speaker)
		}
		if meetingID := strings.TrimSpace(entry.Metadata["meetingId"]); meetingID != "" {
			builder.WriteString(" meeting=")
			builder.WriteString(meetingID)
		}
		builder.WriteString("\n")
		for _, line := range strings.Split(entry.Text, "\n") {
			builder.WriteString("  ")
			builder.WriteString(strings.TrimSpace(line))
			builder.WriteByte('\n')
		}
	}

	return builder.String()
}

func (app *kanbanBoardApp) answerMemoryQuestionWithModel(query string, entries []meetingMemoryEntry) (string, error) {
	if app == nil {
		return "", fmt.Errorf("assistant is unavailable")
	}

	app.mu.Lock()
	apiKey := app.apiKey
	app.mu.Unlock()
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}
	if len(entries) == 0 {
		return "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), memoryQuestionRequestTimeout)
	defer cancel()

	return createOpenAITextResponse(ctx, apiKey, openAITextRequest{
		Model:           meetingBrainModel(),
		Instructions:    memoryQuestionInstructions(),
		Input:           buildMemoryQuestionInput(query, entries, time.Now()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: 700,
	})
}

func (app *kanbanBoardApp) memoryMatchesAndContext(query string) ([]meetingMemoryMatch, []meetingMemoryEntry) {
	if app == nil || app.memory == nil {
		return nil, nil
	}

	return app.memory.search(query, 8), app.memory.contextEntriesForQuery(query, defaultMemoryQuestionContextLimit, time.Now())
}

func (app *kanbanBoardApp) answerCurrentBoardQuestion(query string) (string, int, bool) {
	board := app.snapshotState()
	if len(board.Cards) == 0 {
		return "", 0, false
	}

	matches := rankBoardCardsForQuery(query, board.Cards)
	if len(matches) == 0 || matches[0].Score < 35 {
		if status, ok := statusMentionedInBoardQuery(query); ok && asksForBoardStatusGroup(query) {
			var cards []kanbanCard
			for _, card := range board.Cards {
				if card.Status == status {
					cards = append(cards, card)
				}
			}
			if len(cards) == 0 {
				return fmt.Sprintf("No cards are currently %s.", status), 0, true
			}

			return formatBoardStatusGroupAnswer(status, cards), len(cards), true
		}

		return "", 0, false
	}
	if isMemoryRecallQuery(query) && !isCurrentBoardQuery(query) {
		return "", 0, false
	}
	if len(matches) > 1 && matches[1].Score == matches[0].Score {
		cards := make([]kanbanCard, 0, min(len(matches), 5))
		for index := 0; index < len(matches) && index < 5; index++ {
			if matches[index].Score != matches[0].Score {
				break
			}
			cards = append(cards, matches[index].Card)
		}

		return formatMultipleBoardCardsAnswer(cards), len(cards), true
	}

	return formatBoardCardAnswer(matches[0].Card), 1, true
}

type rankedBoardCard struct {
	Card  kanbanCard
	Score int
}

func rankBoardCardsForQuery(query string, cards []kanbanCard) []rankedBoardCard {
	queryTokens := tokenSet(query)
	queryCompact := compactSearchText(query)
	ranked := make([]rankedBoardCard, 0, len(cards))
	for _, card := range cards {
		score := boardCardQueryScore(queryTokens, queryCompact, card)
		if score > 0 {
			ranked = append(ranked, rankedBoardCard{Card: card, Score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		return ranked[i].Card.Title < ranked[j].Card.Title
	})

	return ranked
}

func boardCardQueryScore(queryTokens map[string]struct{}, queryCompact string, card kanbanCard) int {
	score := 0
	if card.ID != "" && strings.Contains(queryCompact, compactSearchText(card.ID)) {
		score += 60
	}

	titleTokens := memoryTokenPattern.FindAllString(strings.ToLower(card.Title), -1)
	titleCompact := compactSearchText(card.Title)
	if titleCompact != "" && strings.Contains(queryCompact, titleCompact) {
		score += 60
	}
	if len(titleTokens) > 0 && allTokensPresent(queryTokens, titleTokens) {
		score += 45
	}
	for _, token := range titleTokens {
		if _, ok := queryTokens[token]; ok {
			score += 12
		}
	}

	for _, token := range memoryTokenPattern.FindAllString(strings.ToLower(card.Owner+" "+strings.Join(card.Tags, " ")), -1) {
		if _, ok := queryTokens[token]; ok {
			score += 4
		}
	}
	for _, token := range memoryTokenPattern.FindAllString(strings.ToLower(card.Notes), -1) {
		if _, ok := queryTokens[token]; ok {
			score += 1
		}
	}
	for _, token := range memoryTokenPattern.FindAllString(strings.ToLower(card.DueDate+" "+formatKanbanKeyDates(card.KeyDates)), -1) {
		if _, ok := queryTokens[token]; ok {
			score += 2
		}
	}

	return score
}

func tokenSet(value string) map[string]struct{} {
	tokens := map[string]struct{}{}
	for _, token := range memoryTokenPattern.FindAllString(strings.ToLower(value), -1) {
		tokens[token] = struct{}{}
	}

	return tokens
}

func allTokensPresent(tokenSet map[string]struct{}, tokens []string) bool {
	for _, token := range tokens {
		if _, ok := tokenSet[token]; !ok {
			return false
		}
	}

	return true
}

func compactSearchText(value string) string {
	return strings.Join(memoryTokenPattern.FindAllString(strings.ToLower(value), -1), "")
}

func isCurrentBoardQuery(query string) bool {
	normalized := strings.ToLower(query)
	for _, marker := range []string{
		"current", "status", "owner", "own", "assigned", "blocked", "done", "progress", "backlog", "board", "card", "ticket", "notes", "tags", "date", "due", "deadline", "milestone", "now",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}

	return false
}

func isMemoryRecallQuery(query string) bool {
	normalized := strings.ToLower(query)
	for _, marker := range []string{
		"what did", "who said", "said", "decided", "discussed", "mentioned", "remember", "earlier", "yesterday", "last meeting", "last week", "meeting went",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}

	return false
}

func statusMentionedInBoardQuery(query string) (kanbanStatus, bool) {
	normalized := strings.ToLower(query)
	switch {
	case strings.Contains(normalized, "blocked") || strings.Contains(normalized, "stuck") || strings.Contains(normalized, "waiting"):
		return kanbanStatusBlocked, true
	case strings.Contains(normalized, "in progress") || strings.Contains(normalized, "working") || strings.Contains(normalized, "started"):
		return kanbanStatusInProgress, true
	case strings.Contains(normalized, "done") || strings.Contains(normalized, "complete") || strings.Contains(normalized, "shipped"):
		return kanbanStatusDone, true
	case strings.Contains(normalized, "backlog") || strings.Contains(normalized, "todo") || strings.Contains(normalized, "to do"):
		return kanbanStatusBacklog, true
	default:
		return "", false
	}
}

func asksForBoardStatusGroup(query string) bool {
	normalized := strings.ToLower(query)
	return strings.Contains(normalized, "what") ||
		strings.Contains(normalized, "which") ||
		strings.Contains(normalized, "list") ||
		strings.Contains(normalized, "show") ||
		strings.Contains(normalized, "any")
}

func formatBoardCardAnswer(card kanbanCard) string {
	parts := []string{fmt.Sprintf("%s is currently %s.", card.Title, card.Status)}
	if owner := strings.TrimSpace(card.Owner); owner != "" {
		parts = append(parts, "Owner: "+owner+".")
	}
	if notes := strings.TrimSpace(card.Notes); notes != "" {
		parts = append(parts, "Notes: "+strings.TrimRight(notes, ".")+".")
	}
	if len(card.Tags) > 0 {
		parts = append(parts, "Tags: "+strings.Join(card.Tags, ", ")+".")
	}
	if dueDate := strings.TrimSpace(card.DueDate); dueDate != "" {
		parts = append(parts, "Due: "+dueDate+".")
	}
	if len(card.KeyDates) > 0 {
		parts = append(parts, "Key dates: "+formatKanbanKeyDates(card.KeyDates)+".")
	}

	return strings.Join(parts, " ")
}

func formatMultipleBoardCardsAnswer(cards []kanbanCard) string {
	if len(cards) == 0 {
		return "I found matching cards, but could not summarize them."
	}

	parts := make([]string, 0, len(cards))
	for _, card := range cards {
		parts = append(parts, fmt.Sprintf("%s (%s)", card.Title, card.Status))
	}

	return "I found multiple matching cards: " + strings.Join(parts, "; ") + "."
}

func formatBoardStatusGroupAnswer(status kanbanStatus, cards []kanbanCard) string {
	parts := make([]string, 0, len(cards))
	for _, card := range cards {
		owner := strings.TrimSpace(card.Owner)
		if owner == "" || owner == "Unassigned" {
			parts = append(parts, card.Title)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", card.Title, owner))
	}

	return fmt.Sprintf("%s cards: %s.", status, strings.Join(parts, "; "))
}

func memoryQuestionInstructions() string {
	return strings.Join([]string{
		"You are Scout, the Bonfire meeting assistant.",
		"Answer the user's recall question using only the supplied memory context.",
		"Use the brain write-ups for synthesis and the transcript entries as source-of-truth references.",
		"Preserve speaker attribution. When useful, name who said what and include dates or transcript IDs.",
		"If the context does not answer the question, say what you could not find instead of guessing.",
		"Keep the answer concise and useful. Use bullets for highlights.",
	}, " ")
}

func buildMemoryQuestionInput(query string, entries []meetingMemoryEntry, now time.Time) string {
	location := meetingTimeLocation()
	var builder strings.Builder
	builder.WriteString("# Current time\n")
	builder.WriteString(now.In(location).Format(time.RFC1123))
	builder.WriteString("\n\n# User question\n")
	builder.WriteString(query)
	builder.WriteString("\n\n# Memory context\n")
	for _, entry := range entries {
		builder.WriteString("- id=")
		builder.WriteString(entry.ID)
		builder.WriteString(" kind=")
		builder.WriteString(entry.Kind)
		builder.WriteString(" time=")
		builder.WriteString(entry.CreatedAt.In(location).Format(time.RFC3339))
		if speaker := strings.TrimSpace(entry.Metadata["speaker"]); speaker != "" {
			builder.WriteString(" speaker=")
			builder.WriteString(speaker)
		}
		if meetingID := strings.TrimSpace(entry.Metadata["meetingId"]); meetingID != "" {
			builder.WriteString(" meeting=")
			builder.WriteString(meetingID)
		}
		builder.WriteString("\n")
		for _, line := range strings.Split(entry.Text, "\n") {
			builder.WriteString("  ")
			builder.WriteString(strings.TrimSpace(line))
			builder.WriteByte('\n')
		}
	}

	return builder.String()
}

func (store *meetingMemoryStore) contextEntriesForQuery(query string, limit int, now time.Time) []meetingMemoryEntry {
	if store == nil || limit <= 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}

	selected := map[string]meetingMemoryEntry{}
	add := func(entry meetingMemoryEntry) {
		if strings.TrimSpace(entry.ID) == "" {
			return
		}
		selected[entry.ID] = entry
	}

	entries := store.snapshot(250)
	rangeStart, rangeEnd, hasTimeRange := relativeQueryTimeRange(query, now)
	entryAllowedByTime := func(entry meetingMemoryEntry) bool {
		if !hasTimeRange {
			return true
		}
		return (entry.CreatedAt.Equal(rangeStart) || entry.CreatedAt.After(rangeStart)) && entry.CreatedAt.Before(rangeEnd)
	}

	for _, match := range store.search(query, limit) {
		if entryAllowedByTime(match.Entry) {
			add(match.Entry)
		}
	}

	if hasTimeRange {
		for _, entry := range entries {
			if entryAllowedByTime(entry) {
				add(entry)
			}
		}
	}

	for _, participant := range participantsMentionedInQuery(query) {
		for _, entry := range entries {
			if entryAllowedByTime(entry) && memoryEntryMentionsParticipant(entry, participant) {
				add(entry)
			}
		}
	}

	if !hasTimeRange {
		recentBrainEntries := 0
		for index := len(entries) - 1; index >= 0 && recentBrainEntries < 8; index-- {
			if entries[index].Kind == meetingMemoryKindBrain {
				add(entries[index])
				recentBrainEntries++
			}
		}
	}

	if len(selected) == 0 {
		recent := tailMemoryEntries(entries, min(limit, 20))
		for _, entry := range recent {
			add(entry)
		}
	}

	contextEntries := make([]meetingMemoryEntry, 0, len(selected))
	for _, entry := range selected {
		contextEntries = append(contextEntries, entry)
	}
	sort.SliceStable(contextEntries, func(i, j int) bool {
		return contextEntries[i].CreatedAt.Before(contextEntries[j].CreatedAt)
	})
	if len(contextEntries) > limit {
		contextEntries = contextEntries[len(contextEntries)-limit:]
	}

	return contextEntries
}

func participantsMentionedInQuery(query string) []string {
	tokens := map[string]struct{}{}
	for _, token := range memoryTokenPattern.FindAllString(strings.ToLower(query), -1) {
		tokens[token] = struct{}{}
	}

	var participants []string
	for _, participant := range meetingParticipantNames {
		participantTokens := memoryTokenPattern.FindAllString(strings.ToLower(participant), -1)
		if len(participantTokens) == 0 {
			continue
		}
		matched := true
		for _, token := range participantTokens {
			if _, ok := tokens[token]; !ok {
				matched = false
				break
			}
		}
		if matched {
			participants = append(participants, participant)
		}
	}

	return participants
}

func memoryEntryMentionsParticipant(entry meetingMemoryEntry, participant string) bool {
	participant = canonicalParticipantName(participant)
	if participant == "" {
		return false
	}
	if strings.Contains(strings.ToLower(entry.Metadata["speaker"]), strings.ToLower(participant)) {
		return true
	}

	lowerText := strings.ToLower(entry.Text)
	lowerParticipant := strings.ToLower(participant)
	return strings.HasPrefix(lowerText, lowerParticipant+":") ||
		strings.HasPrefix(lowerText, lowerParticipant+" +") ||
		strings.Contains(lowerText, " "+lowerParticipant+":") ||
		strings.Contains(lowerText, " "+lowerParticipant+" +")
}

func relativeQueryTimeRange(query string, now time.Time) (time.Time, time.Time, bool) {
	normalized := strings.ToLower(query)
	location := meetingTimeLocation()
	localNow := now.In(location)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)

	if start, end, ok := relativeDurationQueryTimeRange(normalized, localNow); ok {
		return start, end, true
	}

	switch {
	case strings.Contains(normalized, "yesterday"):
		start := dayStart.AddDate(0, 0, -1)
		return start.UTC(), dayStart.UTC(), true
	case strings.Contains(normalized, "today"):
		return dayStart.UTC(), dayStart.AddDate(0, 0, 1).UTC(), true
	case strings.Contains(normalized, "last week"):
		thisWeek := dayStart.AddDate(0, 0, -int((int(localNow.Weekday())+6)%7))
		lastWeek := thisWeek.AddDate(0, 0, -7)
		return lastWeek.UTC(), thisWeek.UTC(), true
	case strings.Contains(normalized, "this week"):
		thisWeek := dayStart.AddDate(0, 0, -int((int(localNow.Weekday())+6)%7))
		return thisWeek.UTC(), thisWeek.AddDate(0, 0, 7).UTC(), true
	default:
		return time.Time{}, time.Time{}, false
	}
}

func relativeDurationQueryTimeRange(normalized string, localNow time.Time) (time.Time, time.Time, bool) {
	if match := pastDurationQueryPattern.FindStringSubmatch(normalized); len(match) == 3 {
		if duration, ok := queryDuration(match[1], match[2]); ok {
			return localNow.Add(-duration).UTC(), localNow.UTC(), true
		}
	}
	if match := agoDurationQueryPattern.FindStringSubmatch(normalized); len(match) == 3 {
		if duration, ok := queryDuration(match[1], match[2]); ok {
			padding := duration / 4
			if padding < 2*time.Minute {
				padding = 2 * time.Minute
			}
			if padding > 15*time.Minute {
				padding = 15 * time.Minute
			}
			start := localNow.Add(-duration - padding)
			end := localNow.Add(-duration + padding)
			if end.After(localNow) {
				end = localNow
			}
			return start.UTC(), end.UTC(), true
		}
	}

	return time.Time{}, time.Time{}, false
}

func queryDuration(amount string, unit string) (time.Duration, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(amount))
	if err != nil || value <= 0 {
		return 0, false
	}

	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "minute", "minutes", "min", "mins":
		return time.Duration(value) * time.Minute, true
	case "hour", "hours", "hr", "hrs":
		return time.Duration(value) * time.Hour, true
	default:
		return 0, false
	}
}

func meetingTimeLocation() *time.Location {
	name := strings.TrimSpace(getenvDefault("MEETING_TIME_ZONE", defaultMeetingTimeZone))
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}

	return location
}

func getenvDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}
