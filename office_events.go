package main

import (
	"strings"
	"sync"
	"time"
)

// office_events.go — the unified push channel (Wave 3, the keystone).
//
// Every authenticated session already holds an office socket (in the room or
// not; see broadcastOfficeKanbanEvent). This file makes that socket the single
// typed event stream every producer fans finished work onto, so a change made
// in one session lands in every other session instantly — no room join, no
// reload. Events ride the existing office fan-out (exactly-once per signed-in
// tab) or the targeted per-user send; no second socket is created.
//
// Trust boundary (packages.go:593, notifications, chat): OS events carry
// TITLES ONLY, never body text. Ref points at the record; consumers that need
// the body fetch it by ref under the normal auth guards.
//
// Consumer classes (contract, enforced client-side in index.html):
//   - light consumers render straight from the payload (bell, brief counters,
//     the "work finished" arrival hook);
//   - rich consumers treat the event as an invalidation signal and fetch-by-ref
//     on receipt (board re-read, package rail re-read).
// A missed event self-heals because the same fetch runs on the next snapshot
// read. Consumers are idempotent by (kind, ref, at). The per-surface polling
// paths stay in place as the fallback until the two-session acceptance test
// proves the channel.
const (
	osEventArtifactCompleted = "artifact_completed"
	osEventArtifactProgress  = "artifact_progress"
	osEventProposal          = "proposal"
	osEventNotification      = "notification"
	osEventChannelPost       = "channel_post"
	osEventPackageAdvanced   = "package_advanced"
	// osEventQuarantineChange is a Wave-7 stub: the kind is defined so the
	// client router and tests can pin it, but no producer emits it yet (the
	// slop classifier lands in Wave 7).
	osEventQuarantineChange = "quarantine_change"

	// osEventName is the single kanban event envelope that carries every OS
	// event. One event, one client-side router by kind.
	osEventName = "os_event"
)

// osEvent is the one wire schema for the unified push channel. Titles only —
// the body never crosses this boundary.
type osEvent struct {
	Kind          string `json:"kind"`
	Ref           string `json:"ref"`
	Title         string `json:"title,omitempty"`
	OriginSurface string `json:"originSurface,omitempty"`
	Actor         string `json:"actor,omitempty"`
	At            string `json:"at"`
}

func normalizeOSEvent(event osEvent) osEvent {
	event.Kind = strings.TrimSpace(event.Kind)
	event.Ref = strings.TrimSpace(event.Ref)
	event.Title = strings.TrimSpace(event.Title)
	event.OriginSurface = strings.TrimSpace(event.OriginSurface)
	event.Actor = strings.TrimSpace(event.Actor)
	if strings.TrimSpace(event.At) == "" {
		event.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return event
}

// broadcastOSEvent fans one OS event out over the office channel to every
// authenticated session (in room or not). Reuses broadcastOfficeKanbanEvent —
// the exactly-once signed-in channel — so no second socket is introduced.
func broadcastOSEvent(event osEvent) {
	event = normalizeOSEvent(event)
	if event.Kind == "" || event.Ref == "" {
		return
	}
	broadcastOfficeKanbanEvent(osEventName, event)
}

// sendOSEventToUser delivers an OS event only to one account's own sockets —
// the targeted path for private records (a private artifact, a targeted
// notification). Same trust boundary: titles only.
func sendOSEventToUser(email string, event osEvent) {
	event = normalizeOSEvent(event)
	if event.Kind == "" || event.Ref == "" {
		return
	}
	sendKanbanEventToUser(email, osEventName, event)
}

// osNotificationEventTitle derives a body-free label for a notification OS
// event. A notificationRecord conflates its title and body into free-text
// .Text — and chat notifications compose up to 140 chars of the actual message
// into it (scout_chat_threads.go postToChannel). That body must never ride the
// unified push channel (titles only), so the OS event carries a kind-derived
// label instead. The bell keeps rendering the full .Text via the separate
// 'notification' event; light consumers here only need the kind for counters.
func osNotificationEventTitle(record notificationRecord) string {
	switch record.Kind {
	case notificationKindChat:
		return "New chat activity"
	case notificationKindTask:
		return "New task proposal"
	case notificationKindAgent:
		return "Agent update"
	case notificationKindAlert:
		return "New alert"
	default:
		return "New notification"
	}
}

// --- artifact events: the memory_query.go create/update seam producer ---

// osArtifactEventGuard suppresses re-emits of an artifact event whose
// user-visible state has not changed. The delivery pipeline re-writes an
// artifact several times for bookkeeping (e.g. deliverArtifactToOrigin stamps
// deliveredAt without touching status/title); those re-writes must not fan out
// a second "completed" event. Keyed by artifact id → last-emitted signature.
var (
	osArtifactEventMu    sync.Mutex
	osArtifactEventState = map[string]string{}
)

// osArtifactEventSignature captures the fields that decide the event: a change
// in any of them is a genuine transition worth fanning out; a re-write that
// leaves them all identical is a no-op. currentStage + progressPercent are
// included because a goal artifact stays goalStatus="running"/status="draft"
// for its whole execute phase (goal_engine.go persist) while these two climb
// per subtask — excluding them would collapse every live progress tick after
// the first into a deduped no-op and freeze Wave 11's stage rail.
func osArtifactEventSignature(kind string, metadata map[string]string) string {
	return strings.Join([]string{
		kind,
		strings.ToLower(strings.TrimSpace(metadata["status"])),
		strings.TrimSpace(metadata["published"]),
		strings.ToLower(strings.TrimSpace(metadata["goalStatus"])),
		strings.TrimSpace(metadata["currentStage"]),
		strings.TrimSpace(metadata["progressPercent"]),
		strings.TrimSpace(metadata["title"]),
	}, "|")
}

// osArtifactIsTerminal reports whether an artifact's metadata describes
// finished work (a completed/published/verified draft, or a terminal goal).
func osArtifactIsTerminal(metadata map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(metadata["status"])) {
	case "complete", "completed", "published", "verified":
		return true
	}
	if strings.TrimSpace(metadata["published"]) == "true" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(metadata["goalStatus"])) {
	case "complete", "completed", "verified", "done":
		return true
	}
	return false
}

// osArtifactIsInFlight reports whether an artifact is a worker/goal scaffold
// still being produced — a create-time placeholder or a non-terminal goal.
func osArtifactIsInFlight(metadata map[string]string) bool {
	if strings.TrimSpace(metadata["latestThreadRun"]) != "" ||
		strings.TrimSpace(metadata["workflow"]) != "" ||
		strings.TrimSpace(metadata["goalStatus"]) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(metadata["status"])) {
	case "running", "queued", "in_progress":
		return true
	}
	return false
}

// osArtifactEventKind classifies an artifact into a push-channel kind. A
// terminal artifact is a completion; an in-flight worker/goal scaffold is
// progress; anything else (a directly-saved piece of known content) is
// finished on arrival.
func osArtifactEventKind(metadata map[string]string) string {
	if osArtifactIsTerminal(metadata) {
		return osEventArtifactCompleted
	}
	if osArtifactIsInFlight(metadata) {
		return osEventArtifactProgress
	}
	return osEventArtifactCompleted
}

// emitOSArtifactEvent fans an artifact create/update out over the push channel,
// deduped by (id, signature) so bookkeeping re-writes stay silent. Called from
// the memory_query.go app seams (createOSArtifactWithMetadata,
// updateOSArtifactWithMetadata); publishOSArtifact rides through the update
// seam, so it needs no separate call. Title only — entry.Text never leaves.
func emitOSArtifactEvent(entry meetingMemoryEntry) {
	if strings.TrimSpace(entry.ID) == "" || entry.Kind != meetingMemoryKindOSArtifact {
		return
	}
	kind := osArtifactEventKind(entry.Metadata)
	signature := osArtifactEventSignature(kind, entry.Metadata)

	osArtifactEventMu.Lock()
	if osArtifactEventState[entry.ID] == signature {
		osArtifactEventMu.Unlock()
		return
	}
	osArtifactEventState[entry.ID] = signature
	// Bound the guard so a long-lived process never accumulates one entry per
	// artifact forever. The cap is generous; eviction only risks a harmless
	// duplicate re-fetch on a very old artifact.
	if len(osArtifactEventState) > 4096 {
		osArtifactEventState = map[string]string{entry.ID: signature}
	}
	osArtifactEventMu.Unlock()

	mode := firstNonEmptyString(entry.Metadata["mode"], entry.Kind)
	broadcastOSEvent(osEvent{
		Kind:          kind,
		Ref:           entry.ID,
		Title:         firstNonEmptyString(strings.TrimSpace(entry.Metadata["title"]), assistantToolLabel(mode)+" artifact"),
		OriginSurface: firstNonEmptyString(strings.TrimSpace(entry.Metadata["originKind"]), "artifacts"),
		Actor:         firstNonEmptyString(entry.Metadata["updatedBy"], entry.Metadata["createdBy"], scoutParticipantName),
	})

	// Wave 8: the same transition seam is where the admin learns an artifact has
	// parked at the external-write gate (fires once per gate entry, admin-only).
	if kanbanApp != nil {
		kanbanApp.maybeNotifyApprovalGate(entry)
	}
}
