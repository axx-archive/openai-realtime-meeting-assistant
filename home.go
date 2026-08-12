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
	ID          string          `json:"id"`
	Text        string          `json:"text"`
	Destination homeDestination `json:"destination"`
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

func homeStarters(recent *homeItem) []homeStarter {
	newPrivate := homeDestination{Route: "new-private"}
	if recent == nil || strings.TrimSpace(recent.Title) == "" || recent.Destination.Route != "thread" || strings.TrimSpace(recent.Destination.ThreadID) == "" {
		return []homeStarter{
			{
				ID: "continue", Label: "Continue", Detail: "Pick up recent work",
				Suggestions: []homeStarterSuggestion{
					{ID: "continue-where-left-off", Text: "Tell me what you want to pick back up.", Destination: newPrivate},
				},
			},
			{
				ID: "explore", Label: "Explore", Detail: "Understand and discover",
				Suggestions: []homeStarterSuggestion{
					{ID: "explore-open-question", Text: "Help me explore the most important open question.", Destination: newPrivate},
				},
			},
			{
				ID: "create", Label: "Create", Detail: "Make the next useful thing",
				Suggestions: []homeStarterSuggestion{
					{ID: "create-next-deliverable", Text: "Help me create the next useful deliverable.", Destination: newPrivate},
				},
			},
			{
				ID: "challenge", Label: "Challenge", Detail: "Grill and red-team",
				Suggestions: []homeStarterSuggestion{
					{ID: "challenge-assumptions", Text: "Challenge my current thinking and identify the weakest assumptions.", Destination: newPrivate},
				},
			},
		}
	}
	subject := strings.TrimSpace(recent.Title)
	continueDestination := recent.Destination
	return []homeStarter{
		{
			ID: "continue", Label: "Continue", Detail: "Pick up recent work",
			Suggestions: []homeStarterSuggestion{
				{ID: "continue-where-left-off", Text: "Continue where we left off in " + subject + ".", Destination: continueDestination},
				{ID: "continue-whats-changed", Text: "Tell me what has changed in " + subject + " and the best next step.", Destination: continueDestination},
				{ID: "continue-open-decision", Text: "Pick up the most important unfinished decision in " + subject + ".", Destination: continueDestination},
			},
		},
		{
			ID: "explore", Label: "Explore", Detail: "Understand and discover",
			Suggestions: []homeStarterSuggestion{
				{ID: "explore-open-question", Text: "Explore the biggest open question in " + subject + ".", Destination: newPrivate},
				{ID: "explore-options", Text: "Compare the strongest options for " + subject + " and show me the tradeoffs.", Destination: newPrivate},
				{ID: "explore-blind-spots", Text: "Discover what we may be missing in " + subject + ".", Destination: newPrivate},
			},
		},
		{
			ID: "create", Label: "Create", Detail: "Make the next useful thing",
			Suggestions: []homeStarterSuggestion{
				{ID: "create-next-deliverable", Text: "Create the next useful deliverable for " + subject + ".", Destination: newPrivate},
				{ID: "create-plan", Text: "Turn the current thinking in " + subject + " into a concise plan.", Destination: newPrivate},
				{ID: "create-update", Text: "Draft a clear project update for " + subject + ".", Destination: newPrivate},
			},
		},
		{
			ID: "challenge", Label: "Challenge", Detail: "Grill and red-team",
			Suggestions: []homeStarterSuggestion{
				{ID: "challenge-assumptions", Text: "Challenge the current thinking in " + subject + " and identify the weakest assumptions.", Destination: newPrivate},
				{ID: "challenge-grill", Text: "Grill me on " + subject + " like a skeptical investor.", Destination: newPrivate},
				{ID: "challenge-failure", Text: "Red-team the plan for " + subject + " and show me how it could fail.", Destination: newPrivate},
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
		Starters: homeStarters(recent), AllClear: len(items) == 0,
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
