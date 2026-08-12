package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const homeSnapshotVersion = "home-v2"

type homeDestination struct {
	Route     string `json:"route"`
	ThreadID  string `json:"threadId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	RoomID    string `json:"roomId,omitempty"`
	Title     string `json:"title,omitempty"`
}

type homeItem struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Eyebrow        string          `json:"eyebrow"`
	Title          string          `json:"title"`
	Detail         string          `json:"detail"`
	SourceRevision string          `json:"sourceRevision,omitempty"`
	WorkID         string          `json:"workId,omitempty"`
	Destination    homeDestination `json:"destination"`
}

type homeStarter struct {
	ID          string                  `json:"id"`
	Label       string                  `json:"label"`
	Detail      string                  `json:"detail"`
	Suggestions []homeStarterSuggestion `json:"suggestions"`
}

type homeStarterSuggestion struct {
	ID             string                 `json:"id"`
	Text           string                 `json:"text"`
	Destination    homeDestination        `json:"destination"`
	WhyThis        string                 `json:"whyThis"`
	SourceCoverage []homeSuggestionSource `json:"sourceCoverage,omitempty"`
}

// homeSuggestionSource is deliberately body-free. The Home projection may
// explain why a recommendation exists, but it must not copy private message
// bodies, audience membership, or internal authority material into the UI.
type homeSuggestionSource struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

type homeSnapshot struct {
	Version     string        `json:"version"`
	GeneratedAt string        `json:"generatedAt"`
	Items       []homeItem    `json:"items"`
	Starters    []homeStarter `json:"starters"`
	AllClear    bool          `json:"allClear"`
}

type homeWorkCandidate struct {
	thread  scoutChatThreadRecord
	message scoutChatMessageRecord
	ref     scoutChatThreadRef
}

type homeRoomCandidate struct {
	ID               string
	Name             string
	SourceRevision   string
	Live             bool
	ParticipantCount int
}

type homeRecurringTheme struct {
	Topic   string
	Threads []scoutChatThreadRecord
}

func homeTimestamp(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func homeOneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

func homeWorkFamily(ref scoutChatThreadRef) string {
	haystack := strings.ToLower(strings.Join([]string{ref.Mode, ref.Query}, " "))
	hasResearch := containsAnyWord(haystack, "research", "investigate", "compare", "market scan", "source", "sources", "due diligence")
	hasDocument := containsAnyWord(haystack, "document", "memo", "brief", "one-pager", "report")
	hasDeck := containsAnyWord(haystack, "deck", "presentation", "slide", "slides", "pitch deck", "powerpoint", "pptx")
	hasWorkbook := containsAnyWord(haystack, "financial model", "forecast", "budget", "valuation", "cap table", "cash flow", "waterfall", "xlsx", "workbook", "spreadsheet")
	switch {
	case containsAnyWord(haystack, "schedule", "scheduled", "recurring", "daily", "weekly", "monthly", "every day", "every weekday", "every week", "every month"):
		return "Scheduled work"
	case containsAnyWord(haystack, "revise", "revision", "redline", "translate", "translation", "regenerate", "rewrite", "edit this", "edit the", "edit my", "update this", "update the", "update my"):
		return "Revision"
	case containsAnyWord(haystack, "mixed package", "investor package", "diligence package", "fundraising package", "data room package") || boolCount(hasResearch, hasDocument, hasDeck, hasWorkbook) >= 2:
		return "Mixed package"
	case containsAnyWord(haystack, "meeting recap", "meeting notes", "action record", "decision log", "transcript recap"):
		return "Meeting recap"
	case containsAnyWord(haystack, "chart", "visualization", "dashboard", "plot", "graph", "data table"):
		return "Data visualization"
	case containsAnyWord(haystack, "code", "implementation", "repository", "pull request", "execution handoff", "deployment handoff"):
		return "Build"
	case containsAnyWord(haystack, "project plan", "task board", "operating plan", "roadmap", "work breakdown"):
		return "Project plan"
	case hasWorkbook:
		return "Financial model"
	case hasDeck:
		return "Presentation"
	case containsAnyWord(haystack, "image", "images", "design", "visual", "logo", "brand", "mockup", "illustration", "render", "creative"):
		return "Design"
	case hasResearch:
		return "Research"
	case hasDocument:
		return "Document"
	default:
		return "Work"
	}
}

func containsAnyWord(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		start := 0
		for {
			index := strings.Index(value[start:], candidate)
			if index < 0 {
				break
			}
			index += start
			beforeOK := index == 0 || !isHomeWordByte(value[index-1])
			after := index + len(candidate)
			afterOK := after == len(value) || !isHomeWordByte(value[after])
			if beforeOK && afterOK {
				return true
			}
			start = index + 1
		}
	}
	return false
}

func isHomeWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func homeWorkPhase(ref scoutChatThreadRef) string {
	status := strings.ToLower(strings.TrimSpace(ref.Status))
	stage := strings.ToLower(strings.TrimSpace(ref.CurrentStage))
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approval_required", "needs_input":
		return "Waiting for you"
	case "parked", "needs_attention", "error", "failed":
		return "Needs attention"
	case "complete", "completed", "done":
		return "Ready"
	}
	switch {
	case containsAnyWord(stage, "deliver", "ship", "verify_goal_completed"):
		return "Preparing delivery"
	case containsAnyWord(stage, "gate", "review", "verif"):
		return "Reviewing"
	case containsAnyWord(stage, "research", "source", "evidence"):
		return "Gathering evidence"
	case containsAnyWord(stage, "build", "draft", "synth", "execute", "codex", "assembl", "compos", "prepar"):
		return "Building"
	case containsAnyWord(stage, "assign", "decompose", "identify", "goal", "plan"):
		return "Understanding"
	case status == "queued":
		return "Starting"
	default:
		return "Working"
	}
}

func homeWorkDetail(ref scoutChatThreadRef) string {
	switch homeWorkPhase(ref) {
	case "Waiting for you":
		return "Open to review and respond"
	case "Needs attention":
		return "Open to resolve the next step"
	case "Starting":
		return "Getting the work ready"
	case "Ready":
		return "Open the result"
	case "Gathering evidence":
		return "Gathering reliable sources"
	case "Building":
		return "Building the first draft"
	case "Reviewing":
		return "Checking the work"
	case "Preparing delivery":
		return "Preparing your deliverable"
	case "Understanding":
		return "Shaping the work"
	default:
		return "Work is underway"
	}
}

func homeWorkCandidates(threads []scoutChatThreadRecord) []homeWorkCandidate {
	result := []homeWorkCandidate{}
	for _, thread := range threads {
		for _, message := range thread.Messages {
			if message.Thread == nil || strings.TrimSpace(message.Thread.ID) == "" {
				continue
			}
			result = append(result, homeWorkCandidate{thread: thread, message: message, ref: *message.Thread})
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		return homeTimestamp(result[left].message.CreatedAt).After(homeTimestamp(result[right].message.CreatedAt))
	})
	return result
}

func homeAttentionItem(threads []scoutChatThreadRecord, notifications []map[string]any) *homeItem {
	attention := map[string]bool{
		"approval_required": true,
		"needs_input":       true,
		"parked":            true,
		"needs_attention":   true,
		"error":             true,
		"failed":            true,
	}
	for _, candidate := range homeWorkCandidates(threads) {
		status := strings.ToLower(strings.TrimSpace(candidate.ref.Status))
		if !attention[status] {
			continue
		}
		title := homeOneLine(firstNonEmptyString(candidate.ref.Query, candidate.thread.Title, "Work"), 96)
		return &homeItem{
			ID:             "needs-work-" + candidate.ref.ID,
			Kind:           "needs-you",
			Eyebrow:        "Needs you",
			Title:          title,
			Detail:         homeWorkDetail(candidate.ref),
			SourceRevision: firstNonEmptyString(candidate.thread.UpdatedAt, candidate.message.CreatedAt),
			WorkID:         candidate.ref.ID,
			Destination: homeDestination{
				Route: "thread", ThreadID: candidate.thread.ID, MessageID: candidate.message.ID, Title: candidate.thread.Title,
			},
		}
	}

	for _, thread := range threads {
		for index := len(thread.Messages) - 1; index >= 0; index-- {
			message := thread.Messages[index]
			pendingProposal := message.Proposal != nil && oneOf(strings.ToLower(strings.TrimSpace(message.Proposal.Status)), "", "pending", "held")
			pendingChoice := message.Choices != nil && strings.TrimSpace(message.Choices.SelectedID) == "" && oneOf(strings.ToLower(strings.TrimSpace(message.Choices.Status)), "", "pending")
			if !pendingProposal && !pendingChoice {
				continue
			}
			title := "Scout needs your decision"
			if pendingProposal {
				title = homeOneLine(firstNonEmptyString(message.Proposal.Summary, message.Proposal.Objective, title), 96)
			} else if pendingChoice {
				title = homeOneLine(firstNonEmptyString(message.Choices.Question, title), 96)
			}
			return &homeItem{
				ID: "needs-message-" + message.ID, Kind: "needs-you", Eyebrow: "Needs you", Title: title,
				Detail: "Open to respond", SourceRevision: firstNonEmptyString(thread.UpdatedAt, message.CreatedAt),
				Destination: homeDestination{Route: "thread", ThreadID: thread.ID, MessageID: message.ID, Title: thread.Title},
			}
		}
	}

	for _, notification := range notifications {
		if read, _ := notification["read"].(bool); read {
			continue
		}
		proposalID := asString(notification["proposalId"])
		artifactID := asString(notification["artifactId"])
		threadID := asString(notification["threadId"])
		if proposalID == "" && (artifactID == "" || threadID == "") {
			continue
		}
		title := homeOneLine(asString(notification["text"]), 96)
		if title == "" {
			title = "A decision is waiting"
		}
		destination := homeDestination{Route: "alerts"}
		if threadID != "" {
			destination = homeDestination{Route: "thread", ThreadID: threadID, MessageID: asString(notification["messageId"]), Title: asString(notification["threadName"])}
		}
		return &homeItem{
			ID: "needs-notification-" + asString(notification["id"]), Kind: "needs-you", Eyebrow: "Needs you", Title: title,
			Detail: "Open to respond", SourceRevision: asString(notification["createdAt"]), Destination: destination,
		}
	}
	return nil
}

func homeActiveWorkItem(threads []scoutChatThreadRecord) *homeItem {
	active := map[string]bool{"queued": true, "running": true, "in_progress": true, "working": true}
	for _, candidate := range homeWorkCandidates(threads) {
		status := strings.ToLower(strings.TrimSpace(candidate.ref.Status))
		if !active[status] {
			continue
		}
		family := homeWorkFamily(candidate.ref)
		phase := homeWorkPhase(candidate.ref)
		return &homeItem{
			ID: "active-work-" + candidate.ref.ID, Kind: "active-work", Eyebrow: family + " · " + phase,
			Title:  homeOneLine(firstNonEmptyString(candidate.ref.Query, candidate.thread.Title, family), 96),
			Detail: homeWorkDetail(candidate.ref), SourceRevision: firstNonEmptyString(candidate.thread.UpdatedAt, candidate.message.CreatedAt), WorkID: candidate.ref.ID,
			Destination: homeDestination{Route: "thread", ThreadID: candidate.thread.ID, MessageID: candidate.message.ID, Title: candidate.thread.Title},
		}
	}
	return nil
}

func homeRecentItem(threads []scoutChatThreadRecord, excluded map[string]bool) *homeItem {
	ordered := append([]scoutChatThreadRecord(nil), threads...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return scoutChatThreadTime(ordered[left]).After(scoutChatThreadTime(ordered[right]))
	})
	for _, thread := range ordered {
		if thread.ArchivedAt != "" || thread.Intake != "" || excluded[thread.ID] || strings.TrimSpace(thread.ID) == "" || len(thread.Messages) == 0 {
			continue
		}
		for index := len(thread.Messages) - 1; index >= 0; index-- {
			message := thread.Messages[index]
			if strings.TrimSpace(message.ID) == "" {
				continue
			}
			detail := homeOneLine(message.Text, 120)
			if detail == "" && message.Work != nil {
				detail = homeOneLine(firstNonEmptyString(message.Work.Summary, message.Work.Title), 120)
			}
			if detail == "" && message.Thread != nil {
				detail = homeOneLine(message.Thread.Query, 120)
			}
			if detail == "" {
				detail = homeOneLine(thread.Preview, 120)
			}
			if detail == "" {
				continue
			}
			title := homeOneLine(firstNonEmptyString(thread.Title, "Conversation"), 80)
			return &homeItem{
				ID: "recent-thread-" + thread.ID, Kind: "recent-thread", Eyebrow: "Continue", Title: title, Detail: detail,
				SourceRevision: firstNonEmptyString(thread.UpdatedAt, message.CreatedAt),
				Destination:    homeDestination{Route: "thread", ThreadID: thread.ID, MessageID: message.ID, Title: title},
			}
		}
	}
	return nil
}

func homeLiveMeetingItem(rooms []homeRoomCandidate) *homeItem {
	live := []homeRoomCandidate{}
	for _, room := range rooms {
		if room.Live {
			live = append(live, room)
		}
	}
	sort.SliceStable(live, func(left, right int) bool {
		return live[left].ParticipantCount > live[right].ParticipantCount
	})
	if len(live) == 0 {
		return nil
	}
	count := live[0].ParticipantCount
	detail := "Meeting is live"
	if count > 0 {
		if count == 1 {
			detail = "1 person is here"
		} else {
			detail = fmt.Sprintf("%d people are here", count)
		}
	}
	return &homeItem{
		ID: "live-room-" + live[0].ID, Kind: "live-meeting", Eyebrow: "Live meeting", Title: homeOneLine(live[0].Name, 80), Detail: detail,
		SourceRevision: fmt.Sprintf("%s:%d", live[0].SourceRevision, count), Destination: homeDestination{Route: "room", RoomID: live[0].ID, Title: live[0].Name},
	}
}

func homeSuggestionSourceForItem(item *homeItem) []homeSuggestionSource {
	if item == nil || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.SourceRevision) == "" {
		return nil
	}
	return []homeSuggestionSource{{Kind: item.Kind, ID: item.ID, Revision: item.SourceRevision}}
}

func homeSuggestion(id, text, why string, destination homeDestination, source *homeItem) homeStarterSuggestion {
	return homeStarterSuggestion{
		ID: id, Text: text, Destination: destination, WhyThis: why,
		SourceCoverage: homeSuggestionSourceForItem(source),
	}
}

func homeSuggestionWithCoverage(id, text, why string, destination homeDestination, sources []homeSuggestionSource) homeStarterSuggestion {
	return homeStarterSuggestion{ID: id, Text: text, Destination: destination, WhyThis: why, SourceCoverage: sources}
}

var homeThemeStopWords = map[string]bool{
	"about": true, "after": true, "again": true, "against": true, "because": true, "before": true, "being": true,
	"between": true, "could": true, "create": true, "current": true, "from": true, "have": true, "help": true,
	"into": true, "meeting": true, "most": true, "need": true, "project": true, "review": true, "scout": true,
	"should": true, "their": true, "there": true, "these": true, "thing": true, "think": true, "this": true,
	"through": true, "update": true, "want": true, "what": true, "where": true, "which": true, "with": true,
	"work": true, "would": true, "your": true,
}

func homeThemeWords(value string) []string {
	words := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r < 'a' || r > 'z' })
	result := make([]string, 0, len(words))
	seen := map[string]bool{}
	for _, word := range words {
		if len(word) < 5 || homeThemeStopWords[word] || seen[word] {
			continue
		}
		seen[word] = true
		result = append(result, word)
	}
	return result
}

// homeRecurringTheme finds one deterministic topic repeated across at least
// two viewer-authorized conversations. It runs over the already filtered
// thread snapshot and emits only body-free source coverage; raw messages never
// enter the Home response.
func homeRecurringThemeForViewer(threads []scoutChatThreadRecord) *homeRecurringTheme {
	type themeCandidate struct {
		threads map[string]scoutChatThreadRecord
		latest  time.Time
	}
	candidates := map[string]*themeCandidate{}
	for _, thread := range threads {
		if thread.ArchivedAt != "" || thread.Intake != "" || strings.TrimSpace(thread.ID) == "" {
			continue
		}
		context := thread.Title
		for index, included := len(thread.Messages)-1, 0; index >= 0 && included < 3; index, included = index-1, included+1 {
			context += " " + thread.Messages[index].Text
			if thread.Messages[index].Thread != nil {
				context += " " + thread.Messages[index].Thread.Query
			}
		}
		for _, word := range homeThemeWords(context) {
			candidate := candidates[word]
			if candidate == nil {
				candidate = &themeCandidate{threads: map[string]scoutChatThreadRecord{}}
				candidates[word] = candidate
			}
			candidate.threads[thread.ID] = thread
			if timestamp := scoutChatThreadTime(thread); timestamp.After(candidate.latest) {
				candidate.latest = timestamp
			}
		}
	}
	words := make([]string, 0, len(candidates))
	for word, candidate := range candidates {
		if len(candidate.threads) >= 2 {
			words = append(words, word)
		}
	}
	if len(words) == 0 {
		return nil
	}
	sort.Slice(words, func(i, j int) bool {
		left, right := candidates[words[i]], candidates[words[j]]
		if len(left.threads) != len(right.threads) {
			return len(left.threads) > len(right.threads)
		}
		if !left.latest.Equal(right.latest) {
			return left.latest.After(right.latest)
		}
		return words[i] < words[j]
	})
	selected := candidates[words[0]]
	result := &homeRecurringTheme{Topic: words[0]}
	for _, thread := range selected.threads {
		result.Threads = append(result.Threads, thread)
	}
	sort.Slice(result.Threads, func(i, j int) bool {
		return scoutChatThreadTime(result.Threads[i]).After(scoutChatThreadTime(result.Threads[j]))
	})
	if len(result.Threads) > 3 {
		result.Threads = result.Threads[:3]
	}
	return result
}

func homeThemeCoverage(theme *homeRecurringTheme) []homeSuggestionSource {
	if theme == nil {
		return nil
	}
	result := make([]homeSuggestionSource, 0, len(theme.Threads))
	for _, thread := range theme.Threads {
		result = append(result, homeSuggestionSource{Kind: "conversation", ID: thread.ID, Revision: firstNonEmptyString(thread.UpdatedAt, thread.CreatedAt)})
	}
	return result
}

func homeContextTitle(item *homeItem) string {
	if item == nil {
		return ""
	}
	return strings.TrimSpace(item.Title)
}

// homeStarters is a deterministic, permission-filtered chief-of-staff
// projection. Its inputs are already viewer-authorized Home items; it never
// searches another tenant, starts provider work, or mutates a Project. The
// ordering encodes the product priority: needs-you, active work, exact
// continuation, then safe generic fallbacks.
func homeStarters(recent, attention, active *homeItem, theme *homeRecurringTheme) []homeStarter {
	newPrivate := homeDestination{Route: "new-private"}
	hasThreadContext := func(item *homeItem) bool {
		return item != nil && strings.TrimSpace(item.Title) != "" && item.Destination.Route == "thread" && strings.TrimSpace(item.Destination.ThreadID) != ""
	}
	if !hasThreadContext(recent) && !hasThreadContext(attention) && !hasThreadContext(active) {
		continueSuggestion := homeSuggestion("continue-where-left-off", "Tell me what you want to pick back up.", "A safe starting point when there is no current conversation to resume.", newPrivate, nil)
		return []homeStarter{
			{
				ID: "continue", Label: "Continue", Detail: "Pick up recent work",
				Suggestions: []homeStarterSuggestion{continueSuggestion},
			},
			{
				ID: "explore", Label: "Explore", Detail: "Understand and discover",
				Suggestions: []homeStarterSuggestion{
					homeSuggestion("explore-open-question", "Help me explore the most important open question.", "A fresh private conversation while Scout gets to know your current work.", newPrivate, nil),
				},
			},
			{
				ID: "create", Label: "Create", Detail: "Make the next useful thing",
				Suggestions: []homeStarterSuggestion{
					homeSuggestion("create-next-deliverable", "Help me create the next useful deliverable.", "A fresh private conversation while Scout gets to know your current work.", newPrivate, nil),
				},
			},
			{
				ID: "challenge", Label: "Challenge", Detail: "Grill and red-team",
				Suggestions: []homeStarterSuggestion{
					homeSuggestion("challenge-assumptions", "Challenge my current thinking and identify the weakest assumptions.", "A fresh private conversation while Scout gets to know your current work.", newPrivate, nil),
				},
			},
		}
	}
	// Needs-you and active-work rows are valid chief-of-staff context even when
	// they are the viewer's only conversation. The Home item projection removes
	// them from the separate recent slot to avoid duplicate rows, but the starter
	// projection must still use them rather than falling back to generic copy.
	if !hasThreadContext(recent) {
		if hasThreadContext(attention) {
			recent = attention
		} else {
			recent = active
		}
	}
	subject := strings.TrimSpace(recent.Title)
	continueDestination := recent.Destination
	continueSuggestions := []homeStarterSuggestion{
		homeSuggestion("continue-where-left-off", "Continue where we left off in "+subject+".", "You were last working here.", continueDestination, recent),
		homeSuggestion("continue-whats-changed", "Tell me what has changed in "+subject+" and the best next step.", "You were last working here.", continueDestination, recent),
	}
	if attention != nil && attention.Destination.Route == "thread" && attention.Destination.ThreadID != recent.Destination.ThreadID {
		continueSuggestions = append(continueSuggestions, homeSuggestion(
			"continue-needs-you", "Help me resolve what is waiting on me in "+homeContextTitle(attention)+".",
			"This work is currently waiting for your decision or input.", attention.Destination, attention))
	} else {
		continueSuggestions = append(continueSuggestions, homeSuggestion(
			"continue-open-decision", "Pick up the most important unfinished decision in "+subject+".",
			"You were last working here.", continueDestination, recent))
	}

	createSource := recent
	createSubject := subject
	createWhy := "You were last working here."
	if active != nil && homeContextTitle(active) != "" {
		createSource, createSubject = active, homeContextTitle(active)
		createWhy = "Work is already underway here, so a concrete next deliverable is timely."
	}
	challengeSource := recent
	challengeSubject := subject
	challengeWhy := "You were last working here."
	if attention != nil && homeContextTitle(attention) != "" {
		challengeSource, challengeSubject = attention, homeContextTitle(attention)
		challengeWhy = "This work is waiting on a decision, making its assumptions worth testing now."
	}
	exploreSuggestions := []homeStarterSuggestion{
		homeSuggestion("explore-open-question", "Explore the biggest open question in "+subject+".", "You were last working here.", newPrivate, recent),
		homeSuggestion("explore-options", "Compare the strongest options for "+subject+" and show me the tradeoffs.", "You were last working here.", newPrivate, recent),
	}
	if theme != nil && len(theme.Threads) >= 2 {
		exploreSuggestions = append(exploreSuggestions, homeSuggestionWithCoverage(
			"explore-recurring-theme", "Connect what has come up across your conversations about "+theme.Topic+" and identify the useful next move.",
			fmt.Sprintf("%s has come up across %d conversations you can open.", strings.Title(theme.Topic), len(theme.Threads)), newPrivate, homeThemeCoverage(theme)))
	} else {
		exploreSuggestions = append(exploreSuggestions, homeSuggestion("explore-blind-spots", "Discover what we may be missing in "+subject+".", "You were last working here.", newPrivate, recent))
	}
	return []homeStarter{
		{
			ID: "continue", Label: "Continue", Detail: "Pick up recent work",
			Suggestions: continueSuggestions,
		},
		{
			ID: "explore", Label: "Explore", Detail: "Understand and discover",
			Suggestions: exploreSuggestions,
		},
		{
			ID: "create", Label: "Create", Detail: "Make the next useful thing",
			Suggestions: []homeStarterSuggestion{
				homeSuggestion("create-next-deliverable", "Create the next useful deliverable for "+createSubject+".", createWhy, newPrivate, createSource),
				homeSuggestion("create-plan", "Turn the current thinking in "+createSubject+" into a concise plan.", createWhy, newPrivate, createSource),
				homeSuggestion("create-update", "Draft a clear project update for "+createSubject+".", createWhy, newPrivate, createSource),
			},
		},
		{
			ID: "challenge", Label: "Challenge", Detail: "Grill and red-team",
			Suggestions: []homeStarterSuggestion{
				homeSuggestion("challenge-assumptions", "Challenge the current thinking in "+challengeSubject+" and identify the weakest assumptions.", challengeWhy, newPrivate, challengeSource),
				homeSuggestion("challenge-grill", "Grill me on "+challengeSubject+" like a skeptical investor.", challengeWhy, newPrivate, challengeSource),
				homeSuggestion("challenge-failure", "Red-team the plan for "+challengeSubject+" and show me how it could fail.", challengeWhy, newPrivate, challengeSource),
			},
		},
	}
}

func buildHomeSnapshot(threads []scoutChatThreadRecord, notifications []map[string]any, rooms []homeRoomCandidate, now time.Time) homeSnapshot {
	attention := homeAttentionItem(threads, notifications)
	active := homeActiveWorkItem(threads)
	excluded := map[string]bool{}
	for _, item := range []*homeItem{attention, active} {
		if item != nil && item.Destination.ThreadID != "" {
			excluded[item.Destination.ThreadID] = true
		}
	}
	recent := homeRecentItem(threads, excluded)
	meeting := homeLiveMeetingItem(rooms)
	items := []homeItem{}
	for _, item := range []*homeItem{recent, attention, active, meeting} {
		if item != nil {
			items = append(items, *item)
		}
	}
	if len(items) > 4 {
		items = items[:4]
	}
	return homeSnapshot{
		Version: homeSnapshotVersion, GeneratedAt: now.UTC().Format(time.RFC3339Nano), Items: items,
		Starters: homeStarters(recent, attention, active, homeRecurringThemeForViewer(threads)), AllClear: len(items) == 0,
	}
}

func (app *kanbanBoardApp) homeSnapshotForViewer(viewerEmail string) homeSnapshot {
	threads := app.scoutChatThreadsSnapshot(viewerEmail, false, 100)
	for index := range threads {
		threads[index] = app.projectScoutChatThreadForViewer(viewerEmail, threads[index])
	}
	rooms := []homeRoomCandidate{}
	store := appRoomStoreIfOpen()
	if store == nil {
		return buildHomeSnapshot(threads, app.notificationsForUser(viewerEmail, notificationListLimit), rooms, time.Now())
	}
	for _, room := range store.list() {
		if room.Archived {
			continue
		}
		live, count := roomLiveStats(room.ID)
		rooms = append(rooms, homeRoomCandidate{
			ID: room.ID, Name: room.Name, SourceRevision: room.CreatedAt.UTC().Format(time.RFC3339Nano),
			Live: live, ParticipantCount: count,
		})
	}
	return buildHomeSnapshot(threads, app.notificationsForUser(viewerEmail, notificationListLimit), rooms, time.Now())
}

func assistantHomeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "home is unavailable")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "home": kanbanApp.homeSnapshotForViewer(user.Email)})
}
