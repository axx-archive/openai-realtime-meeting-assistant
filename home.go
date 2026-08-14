package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const homeSnapshotVersion = "home-v2"

const (
	homeConversationCompactionVersion = "home-conversation-v1"
	homeConversationCompactionKey     = "homeContextCompaction"
	homeConversationFreshnessWindow   = 30 * 24 * time.Hour
)

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

// homeConversationCompaction is the durable, body-minimized recommendation
// input for one conversation. It is regenerated at the same persistence
// boundary as every thread mutation. Home may rank these records, but it must
// never reopen raw message history to synthesize a card-click suggestion.
type homeConversationCompaction struct {
	Version            string                `json:"version"`
	ThreadID           string                `json:"threadId"`
	SourceRevision     string                `json:"sourceRevision"`
	SourceHighWater    string                `json:"sourceHighWater"`
	AudienceDigest     string                `json:"audienceDigest"`
	GeneratedAt        string                `json:"generatedAt"`
	FreshUntil         string                `json:"freshUntil"`
	Topics             []string              `json:"topics,omitempty"`
	Recent             *homeCompactionRecent `json:"recent,omitempty"`
	Work               *homeCompactionWork   `json:"work,omitempty"`
	Action             *homeCompactionAction `json:"action,omitempty"`
	Status             string                `json:"status"`
	InvalidationReason string                `json:"invalidationReason,omitempty"`
	ReceiptDigest      string                `json:"receiptDigest"`
}

type homeCompactionRecent struct {
	MessageID string `json:"messageId"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"createdAt"`
}

type homeCompactionWork struct {
	MessageID string             `json:"messageId"`
	CreatedAt string             `json:"createdAt"`
	Ref       scoutChatThreadRef `json:"ref"`
}

type homeCompactionAction struct {
	MessageID string `json:"messageId"`
	CreatedAt string `json:"createdAt"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
}

func homeConversationAudienceDigest(owner, visibility string, members []string) string {
	canonicalMembers := append([]string(nil), members...)
	for index := range canonicalMembers {
		canonicalMembers[index] = normalizeAccountEmail(canonicalMembers[index])
	}
	sort.Strings(canonicalMembers)
	return exactSHA256([]byte(strings.Join([]string{
		"home-conversation-audience/v1",
		normalizeAccountEmail(owner),
		normalizeScoutChatVisibility(visibility),
		strings.Join(canonicalMembers, ","),
	}, "\x00")))
}

func homeConversationAudienceDigestFromMetadata(metadata map[string]string) string {
	members := []string{}
	if raw := strings.TrimSpace(metadata["memberEmails"]); raw != "" {
		members = strings.Split(raw, ",")
	}
	return homeConversationAudienceDigest(metadata["ownerEmail"], metadata["visibility"], members)
}

func homeConversationSourceHighWater(thread scoutChatThreadRecord) string {
	type sourceMessage struct {
		ID        string `json:"id"`
		Role      string `json:"role"`
		CreatedAt string `json:"createdAt"`
		Text      string `json:"text"`
		WorkID    string `json:"workId,omitempty"`
		WorkQuery string `json:"workQuery,omitempty"`
		WorkState string `json:"workState,omitempty"`
	}
	source := struct {
		Version    string          `json:"version"`
		ThreadID   string          `json:"threadId"`
		Title      string          `json:"title"`
		UpdatedAt  string          `json:"updatedAt"`
		ArchivedAt string          `json:"archivedAt,omitempty"`
		Intake     string          `json:"intake,omitempty"`
		Messages   []sourceMessage `json:"messages"`
	}{
		Version: "home-conversation-source/v1", ThreadID: strings.TrimSpace(thread.ID),
		Title: strings.TrimSpace(thread.Title), UpdatedAt: strings.TrimSpace(thread.UpdatedAt),
		ArchivedAt: strings.TrimSpace(thread.ArchivedAt), Intake: strings.TrimSpace(thread.Intake),
	}
	start := len(thread.Messages) - 3
	if start < 0 {
		start = 0
	}
	for _, message := range thread.Messages[start:] {
		row := sourceMessage{ID: strings.TrimSpace(message.ID), Role: strings.TrimSpace(message.Role), CreatedAt: strings.TrimSpace(message.CreatedAt), Text: strings.TrimSpace(message.Text)}
		if message.Thread != nil {
			row.WorkID = strings.TrimSpace(message.Thread.ID)
			row.WorkQuery = strings.TrimSpace(message.Thread.Query)
			row.WorkState = strings.TrimSpace(message.Thread.Status)
		}
		source.Messages = append(source.Messages, row)
	}
	raw, _ := json.Marshal(source)
	return exactSHA256(raw)
}

func homeConversationCompactionReceiptDigest(compaction homeConversationCompaction) string {
	compaction.ReceiptDigest = ""
	raw, _ := json.Marshal(compaction)
	return exactSHA256(append([]byte("home-conversation-compaction-receipt/v1\x00"), raw...))
}

func homeConversationCompactionForThread(thread scoutChatThreadRecord) homeConversationCompaction {
	revision := firstNonEmptyString(strings.TrimSpace(thread.UpdatedAt), strings.TrimSpace(thread.CreatedAt))
	generated := homeTimestamp(revision)
	status, reason := "current", ""
	switch {
	case strings.TrimSpace(thread.ArchivedAt) != "":
		status, reason = "invalidated", "thread_archived"
	case strings.TrimSpace(thread.Intake) != "":
		status, reason = "invalidated", "intake_not_conversation"
	case generated.IsZero():
		status, reason = "invalidated", "source_high_water_unavailable"
	}
	context := strings.TrimSpace(thread.Title)
	start := len(thread.Messages) - 3
	if start < 0 {
		start = 0
	}
	for _, message := range thread.Messages[start:] {
		context += " " + message.Text
		if message.Thread != nil {
			context += " " + message.Thread.Query
		}
	}
	topics := homeThemeWords(context)
	if len(topics) > 12 {
		topics = topics[:12]
	}
	compaction := homeConversationCompaction{
		Version: homeConversationCompactionVersion, ThreadID: strings.TrimSpace(thread.ID),
		SourceRevision: revision, SourceHighWater: homeConversationSourceHighWater(thread),
		AudienceDigest: homeConversationAudienceDigest(thread.OwnerEmail, scoutChatThreadVisibility(thread), scoutChatThreadMemberEmails(thread)),
		Status:         status, InvalidationReason: reason, Topics: topics,
	}
	workStatuses := map[string]bool{"queued": true, "running": true, "in_progress": true, "working": true, "approval_required": true, "needs_input": true, "parked": true, "needs_attention": true, "error": true, "failed": true}
	for index := len(thread.Messages) - 1; index >= 0; index-- {
		message := thread.Messages[index]
		if compaction.Recent == nil && strings.TrimSpace(message.ID) != "" {
			// The compaction preserves the exact continuation point but not the
			// message body. Preview is already the bounded server-authored rail
			// projection; title is the safe fallback for legacy rows.
			detail := homeOneLine(firstNonEmptyString(thread.Preview, thread.Title), 120)
			if detail != "" {
				compaction.Recent = &homeCompactionRecent{MessageID: message.ID, Detail: detail, CreatedAt: message.CreatedAt}
			}
		}
		if compaction.Work == nil && message.Thread != nil && workStatuses[strings.ToLower(strings.TrimSpace(message.Thread.Status))] {
			compaction.Work = &homeCompactionWork{MessageID: message.ID, CreatedAt: message.CreatedAt, Ref: *message.Thread}
		}
		if compaction.Action == nil {
			pendingProposal := message.Proposal != nil && oneOf(strings.ToLower(strings.TrimSpace(message.Proposal.Status)), "", "pending", "held")
			pendingChoice := message.Choices != nil && strings.TrimSpace(message.Choices.SelectedID) == "" && oneOf(strings.ToLower(strings.TrimSpace(message.Choices.Status)), "", "pending")
			switch {
			case pendingProposal:
				compaction.Action = &homeCompactionAction{MessageID: message.ID, CreatedAt: message.CreatedAt, Kind: "proposal", Title: homeOneLine(firstNonEmptyString(message.Proposal.Summary, message.Proposal.Objective, "Scout needs your decision"), 96)}
			case pendingChoice:
				compaction.Action = &homeCompactionAction{MessageID: message.ID, CreatedAt: message.CreatedAt, Kind: "choices", Title: homeOneLine(firstNonEmptyString(message.Choices.Question, "Scout needs your decision"), 96)}
			}
		}
		if compaction.Recent != nil && compaction.Work != nil && compaction.Action != nil {
			break
		}
	}
	if !generated.IsZero() {
		compaction.GeneratedAt = generated.UTC().Format(time.RFC3339Nano)
		compaction.FreshUntil = generated.Add(homeConversationFreshnessWindow).UTC().Format(time.RFC3339Nano)
	}
	if status != "current" {
		compaction.Topics = nil
		compaction.Recent = nil
		compaction.Work = nil
		compaction.Action = nil
	}
	compaction.ReceiptDigest = homeConversationCompactionReceiptDigest(compaction)
	return compaction
}

func validHomeConversationCompaction(entry meetingMemoryEntry, viewerEmail string, now time.Time) (homeConversationCompaction, bool) {
	var compaction homeConversationCompaction
	if entry.Kind != meetingMemoryKindScoutChat || !scoutChatThreadMetadataAllowsViewer(entry.Metadata, viewerEmail) {
		return compaction, false
	}
	if json.Unmarshal([]byte(strings.TrimSpace(entry.Metadata[homeConversationCompactionKey])), &compaction) != nil {
		return compaction, false
	}
	if compaction.Version != homeConversationCompactionVersion || compaction.ThreadID != entry.ID || compaction.Status != "current" || compaction.InvalidationReason != "" {
		return compaction, false
	}
	if compaction.SourceRevision != firstNonEmptyString(strings.TrimSpace(entry.Metadata["updatedAt"]), strings.TrimSpace(entry.Metadata["createdAt"])) || compaction.AudienceDigest != homeConversationAudienceDigestFromMetadata(entry.Metadata) || compaction.ReceiptDigest != homeConversationCompactionReceiptDigest(compaction) {
		return compaction, false
	}
	generated, freshUntil := homeTimestamp(compaction.GeneratedAt), homeTimestamp(compaction.FreshUntil)
	if generated.IsZero() || freshUntil.IsZero() || generated.After(now.Add(5*time.Minute)) || !freshUntil.After(now) || len(compaction.SourceHighWater) != 64 {
		return compaction, false
	}
	return compaction, true
}

func homeThreadsFromCompactions(entries []meetingMemoryEntry, viewerEmail string, now time.Time, limit int) []scoutChatThreadRecord {
	threads := make([]scoutChatThreadRecord, 0, len(entries))
	for _, entry := range entries {
		compaction, ok := validHomeConversationCompaction(entry, viewerEmail, now)
		if !ok {
			continue
		}
		thread := scoutChatThreadRecord{
			ID: entry.ID, Title: strings.TrimSpace(entry.Metadata["title"]), Preview: strings.TrimSpace(entry.Metadata["preview"]),
			OwnerEmail: normalizeAccountEmail(entry.Metadata["ownerEmail"]), Visibility: normalizeScoutChatVisibility(entry.Metadata["visibility"]),
			CreatedAt: strings.TrimSpace(entry.Metadata["createdAt"]), UpdatedAt: compaction.SourceRevision,
		}
		if rawMembers := strings.TrimSpace(entry.Metadata["memberEmails"]); rawMembers != "" {
			thread.MemberEmails = strings.Split(rawMembers, ",")
		}
		messages := map[string]*scoutChatMessageRecord{}
		ensureMessage := func(id, createdAt string) *scoutChatMessageRecord {
			id = strings.TrimSpace(id)
			if id == "" {
				return nil
			}
			if messages[id] == nil {
				messages[id] = &scoutChatMessageRecord{ID: id, Kind: "home_compaction", Role: "scout", CreatedAt: strings.TrimSpace(createdAt)}
			}
			return messages[id]
		}
		if compaction.Recent != nil {
			if message := ensureMessage(compaction.Recent.MessageID, compaction.Recent.CreatedAt); message != nil {
				message.Text = compaction.Recent.Detail
			}
		}
		if compaction.Work != nil {
			if message := ensureMessage(compaction.Work.MessageID, compaction.Work.CreatedAt); message != nil {
				work := compaction.Work.Ref
				message.Thread = &work
			}
		}
		if compaction.Action != nil {
			if message := ensureMessage(compaction.Action.MessageID, compaction.Action.CreatedAt); message != nil {
				switch compaction.Action.Kind {
				case "proposal":
					message.Proposal = &scoutRouterProposal{Summary: compaction.Action.Title, Status: "pending"}
				case "choices":
					message.Choices = &scoutChatChoices{Question: compaction.Action.Title, Status: "pending"}
				}
			}
		}
		for _, message := range messages {
			thread.Messages = append(thread.Messages, *message)
		}
		sort.Slice(thread.Messages, func(i, j int) bool {
			left, right := homeTimestamp(thread.Messages[i].CreatedAt), homeTimestamp(thread.Messages[j].CreatedAt)
			if !left.Equal(right) {
				return left.Before(right)
			}
			return thread.Messages[i].ID < thread.Messages[j].ID
		})
		threads = append(threads, thread)
	}
	sort.SliceStable(threads, func(i, j int) bool {
		return scoutChatThreadTime(threads[i]).After(scoutChatThreadTime(threads[j]))
	})
	if limit > 0 && len(threads) > limit {
		threads = threads[:limit]
	}
	return threads
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

// homeRecurringThemeFromCompactions performs the live recommendation
// aggregation exclusively over durable body-free metadata. Authorization,
// audience binding, freshness, high-water and receipt integrity are checked
// before any topic can contribute.
func homeRecurringThemeFromCompactions(entries []meetingMemoryEntry, viewerEmail string, now time.Time) *homeRecurringTheme {
	type themeCandidate struct {
		threads map[string]scoutChatThreadRecord
		latest  time.Time
	}
	candidates := map[string]*themeCandidate{}
	for _, entry := range entries {
		compaction, ok := validHomeConversationCompaction(entry, viewerEmail, now)
		if !ok {
			continue
		}
		for _, topic := range compaction.Topics {
			topic = strings.TrimSpace(strings.ToLower(topic))
			if len(homeThemeWords(topic)) != 1 || homeThemeWords(topic)[0] != topic {
				continue
			}
			candidate := candidates[topic]
			if candidate == nil {
				candidate = &themeCandidate{threads: map[string]scoutChatThreadRecord{}}
				candidates[topic] = candidate
			}
			candidate.threads[entry.ID] = scoutChatThreadRecord{
				ID: entry.ID, Title: strings.TrimSpace(entry.Metadata["title"]),
				CreatedAt: strings.TrimSpace(entry.Metadata["createdAt"]), UpdatedAt: compaction.SourceRevision,
			}
			if timestamp := homeTimestamp(compaction.GeneratedAt); timestamp.After(candidate.latest) {
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

func buildHomeSnapshotWithTheme(threads []scoutChatThreadRecord, notifications []map[string]any, rooms []homeRoomCandidate, theme *homeRecurringTheme, now time.Time) homeSnapshot {
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
		Starters: homeStarters(recent, attention, active, theme), AllClear: len(items) == 0,
	}
}

func buildHomeSnapshot(threads []scoutChatThreadRecord, notifications []map[string]any, rooms []homeRoomCandidate, now time.Time) homeSnapshot {
	return buildHomeSnapshotWithTheme(threads, notifications, rooms, homeRecurringThemeForViewer(threads), now)
}

func (app *kanbanBoardApp) homeSnapshotForViewer(viewerEmail string) homeSnapshot {
	now := time.Now()
	if app == nil || app.memory == nil {
		return buildHomeSnapshotWithTheme(nil, nil, nil, nil, now)
	}
	app.scheduleScoutChatIndexMetadataBackfill()
	// This metadata snapshot contains the durable compaction and no chat bodies.
	compactions := app.memory.metadataSnapshotOfKind(meetingMemoryKindScoutChat, 0)
	theme := homeRecurringThemeFromCompactions(compactions, viewerEmail, now)
	threads := homeThreadsFromCompactions(compactions, viewerEmail, now, 100)
	rooms := []homeRoomCandidate{}
	store := appRoomStoreIfOpen()
	if store == nil {
		return buildHomeSnapshotWithTheme(threads, app.notificationsForUser(viewerEmail, notificationListLimit), rooms, theme, now)
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
	return buildHomeSnapshotWithTheme(threads, app.notificationsForUser(viewerEmail, notificationListLimit), rooms, theme, now)
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
