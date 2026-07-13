package main

// AuthorizationSurfaceStatus lets the W1 inventory land before every legacy
// handler is cut over. legacy_guarded means the current session/capability
// guard is documented but still owes canonical Authorize enforcement;
// canonical_required means a release may not treat the legacy guard as enough.
type AuthorizationSurfaceStatus string

const (
	AuthorizationLegacyGuarded     AuthorizationSurfaceStatus = "legacy_guarded"
	AuthorizationCanonicalNeeded   AuthorizationSurfaceStatus = "canonical_required"
	AuthorizationCanonicalEnforced AuthorizationSurfaceStatus = "canonical_enforced"
)

type AuthorizationSurfaceKind string

const (
	AuthorizationHTTP         AuthorizationSurfaceKind = "http"
	AuthorizationWebSocketIn  AuthorizationSurfaceKind = "websocket_inbound"
	AuthorizationWebSocketOut AuthorizationSurfaceKind = "websocket_outbound"
	AuthorizationWSBootstrap  AuthorizationSurfaceKind = "websocket_bootstrap"
	AuthorizationWorker       AuthorizationSurfaceKind = "background_worker"
	AuthorizationCapability   AuthorizationSurfaceKind = "public_capability"
)

// AuthorizationSurface is the machine-readable W1 release inventory. Source
// is a route, websocket event, fan-out/bootstrap name, or worker entry point.
// ReadsBody and AuthorizeBeforeBodyRead make the most important IDOR invariant
// explicit: canonical-required readers must authorize an object header before
// fetching its user-authored body.
type AuthorizationSurface struct {
	ID                      string                     `json:"id"`
	Kind                    AuthorizationSurfaceKind   `json:"kind"`
	Source                  string                     `json:"source"`
	ObjectFamilies          []string                   `json:"object_families"`
	RequiredActions         []ACLAction                `json:"required_actions"`
	PrincipalKinds          []string                   `json:"principal_kinds"`
	ReadsBody               bool                       `json:"reads_body"`
	AuthorizeBeforeBodyRead bool                       `json:"authorize_before_body_read"`
	Status                  AuthorizationSurfaceStatus `json:"status"`
}

func authSurface(id string, kind AuthorizationSurfaceKind, source string, families []string, actions []ACLAction, principals []string, readsBody, authFirst bool, status AuthorizationSurfaceStatus) AuthorizationSurface {
	return AuthorizationSurface{ID: id, Kind: kind, Source: source, ObjectFamilies: families, RequiredActions: actions, PrincipalKinds: principals, ReadsBody: readsBody, AuthorizeBeforeBodyRead: authFirst, Status: status}
}

var authorizationHTTPSurfaces = []AuthorizationSurface{
	authSurface("http.websocket_upgrade", AuthorizationHTTP, "/websocket", []string{"room", "membership", "guest_capability"}, []ACLAction{ACLReadMetadata, ACLCreateChild}, []string{"user", "guest"}, false, true, AuthorizationLegacyGuarded),
	authSurface("http.assistant.query", AuthorizationHTTP, "/assistant/query", []string{"memory", "artifact", "board"}, []ACLAction{ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.chat_threads", AuthorizationHTTP, "/assistant/chat-threads", []string{"chat_thread"}, []ACLAction{ACLReadContent, ACLCreateChild}, []string{"user"}, true, true, AuthorizationLegacyGuarded),
	authSurface("http.assistant.chat_thread", AuthorizationHTTP, "/assistant/chat-threads/", []string{"chat_thread", "artifact", "file"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"user"}, true, true, AuthorizationLegacyGuarded),
	authSurface("http.assistant.attachments", AuthorizationHTTP, "/assistant/attachments", []string{"file", "blob", "chat_thread"}, []ACLAction{ACLCreateChild, ACLWrite}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.agent_threads", AuthorizationHTTP, "/assistant/threads", []string{"workflow", "artifact"}, []ACLAction{ACLExecute, ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.agent_followup", AuthorizationHTTP, "/assistant/threads/follow-up", []string{"workflow", "artifact"}, []ACLAction{ACLReadContent, ACLExecute, ACLWrite}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.assistant.goal", AuthorizationHTTP, "/assistant/goal", []string{"goal", "workflow", "artifact"}, []ACLAction{ACLExecute, ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.goal_cancel", AuthorizationHTTP, "/assistant/goal/cancel", []string{"goal"}, []ACLAction{ACLExecute}, []string{"user"}, false, true, AuthorizationLegacyGuarded),
	authSurface("http.assistant.decision_supersede", AuthorizationHTTP, "/assistant/decisions/supersede", []string{"decision"}, []ACLAction{ACLWrite}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.decision_ratify", AuthorizationHTTP, "/assistant/decisions/ratify", []string{"decision"}, []ACLAction{ACLApprove}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.tools", AuthorizationHTTP, "/assistant/tools", []string{"memory", "board", "artifact", "workflow"}, []ACLAction{ACLReadContent, ACLWrite, ACLExecute}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.notifications", AuthorizationHTTP, "/assistant/notifications", []string{"notification"}, []ACLAction{ACLReadContent}, []string{"user"}, true, true, AuthorizationLegacyGuarded),
	authSurface("http.assistant.notifications_read", AuthorizationHTTP, "/assistant/notifications/read", []string{"notification"}, []ACLAction{ACLWrite}, []string{"user"}, false, true, AuthorizationLegacyGuarded),
	authSurface("http.assistant.notifications_clear", AuthorizationHTTP, "/assistant/notifications/clear", []string{"notification"}, []ACLAction{ACLDelete}, []string{"user"}, false, true, AuthorizationLegacyGuarded),
	authSurface("http.assistant.board", AuthorizationHTTP, "/assistant/board", []string{"board_card"}, []ACLAction{ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.board_drafts", AuthorizationHTTP, "/assistant/board/drafts/", []string{"board_card"}, []ACLAction{ACLWrite, ACLApprove}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.memory", AuthorizationHTTP, "/assistant/memory", []string{"memory", "meeting", "artifact"}, []ACLAction{ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.files", AuthorizationHTTP, "/assistant/files", []string{"file", "blob", "artifact", "folder"}, []ACLAction{ACLReadContent}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.assistant.files_upload", AuthorizationHTTP, "/assistant/files/upload", []string{"file", "blob"}, []ACLAction{ACLCreateChild, ACLWrite}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.file_folders", AuthorizationHTTP, "/assistant/files/folders", []string{"folder"}, []ACLAction{ACLCreateChild, ACLWrite, ACLDelete}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.file_move", AuthorizationHTTP, "/assistant/files/move", []string{"file", "folder"}, []ACLAction{ACLWrite}, []string{"user"}, false, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.file_save", AuthorizationHTTP, "/assistant/files/save", []string{"artifact", "file", "folder"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.assistant.meetings", AuthorizationHTTP, "/assistant/meetings", []string{"meeting", "memory", "board_card"}, []ACLAction{ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.mission", AuthorizationHTTP, "/assistant/mission", []string{"package", "artifact", "decision"}, []ACLAction{ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.mission_refresh", AuthorizationHTTP, "/assistant/mission/refresh", []string{"package", "artifact", "decision"}, []ACLAction{ACLExecute}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.proposals", AuthorizationHTTP, "/assistant/proposals/", []string{"proposal", "workflow"}, []ACLAction{ACLApprove, ACLExecute}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.quarantine", AuthorizationHTTP, "/assistant/quarantine", []string{"memory"}, []ACLAction{ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.quarantine_action", AuthorizationHTTP, "/assistant/quarantine/", []string{"memory"}, []ACLAction{ACLWrite, ACLDelete}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.packages", AuthorizationHTTP, "/assistant/packages", []string{"package", "artifact", "decision", "board_card"}, []ACLAction{ACLReadContent, ACLCreateChild}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.package_action", AuthorizationHTTP, "/assistant/packages/", []string{"package", "artifact", "decision", "board_card"}, []ACLAction{ACLWrite}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.deal_room_request", AuthorizationHTTP, "/assistant/deal-room/request", []string{"deal_room", "package", "artifact"}, []ACLAction{ACLReadContent, ACLShare}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.assistant.deal_room_resolve", AuthorizationHTTP, "/assistant/deal-room/resolve", []string{"deal_room", "artifact"}, []ACLAction{ACLReadContent, ACLShare, ACLApprove}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.assistant.deal_room_revoke", AuthorizationHTTP, "/assistant/deal-room/revoke", []string{"deal_room"}, []ACLAction{ACLShare}, []string{"user"}, false, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.deal_room_list", AuthorizationHTTP, "/assistant/deal-room/list", []string{"deal_room"}, []ACLAction{ACLReadMetadata}, []string{"user"}, false, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.brief", AuthorizationHTTP, "/assistant/brief", []string{"memory", "meeting", "package"}, []ACLAction{ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.portfolio", AuthorizationHTTP, "/assistant/portfolio", []string{"package", "artifact", "decision"}, []ACLAction{ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.realtime_offer", AuthorizationHTTP, "/assistant/realtime-offer", []string{"room", "meeting"}, []ACLAction{ACLReadMetadata, ACLExecute}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.assistant.realtime_tool", AuthorizationHTTP, "/assistant/realtime-tool", []string{"memory", "board_card", "artifact", "workflow", "room"}, []ACLAction{ACLReadContent, ACLWrite, ACLExecute}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("http.artifacts", AuthorizationHTTP, "/artifacts", []string{"artifact", "revision"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.artifacts.action", AuthorizationHTTP, "/artifacts/action", []string{"artifact", "workflow", "approval"}, []ACLAction{ACLReadMetadata, ACLReadContent, ACLApprove, ACLExecute, ACLWrite}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.artifacts.open", AuthorizationHTTP, "/artifacts/open", []string{"artifact"}, []ACLAction{ACLReadContent}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.signals.survey", AuthorizationHTTP, "/signals/survey", []string{"artifact", "signal"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.artifacts.render_token", AuthorizationHTTP, "/artifacts/render-token", []string{"artifact", "revision"}, []ACLAction{ACLReadContent, ACLExport}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.artifacts.blob", AuthorizationHTTP, "/artifacts/blob", []string{"blob", "artifact", "revision", "file"}, []ACLAction{ACLReadContent}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.artifacts.share", AuthorizationHTTP, "/artifacts/share", []string{"artifact", "revision", "capability"}, []ACLAction{ACLReadMetadata, ACLReadContent, ACLShare}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.artifacts.export_pdf", AuthorizationHTTP, "/artifacts/export-pdf", []string{"artifact", "revision", "blob"}, []ACLAction{ACLExport}, []string{"user"}, true, true, AuthorizationCanonicalEnforced),
	authSurface("http.archives", AuthorizationHTTP, "/archives/", []string{"archive", "meeting", "blob"}, []ACLAction{ACLReadContent, ACLExport}, []string{"public_capability"}, true, true, AuthorizationLegacyGuarded),
	authSurface("http.participants", AuthorizationHTTP, "/participants", []string{"room", "membership"}, []ACLAction{ACLReadMetadata}, []string{"anonymous", "user"}, false, true, AuthorizationLegacyGuarded),
	authSurface("http.rooms", AuthorizationHTTP, "/rooms", []string{"room", "membership"}, []ACLAction{ACLReadMetadata, ACLCreateChild}, []string{"user"}, false, false, AuthorizationCanonicalNeeded),
	authSurface("http.room_action", AuthorizationHTTP, "/rooms/", []string{"room", "membership", "guest_capability"}, []ACLAction{ACLManage}, []string{"user"}, false, false, AuthorizationCanonicalNeeded),
	authSurface("http.internal_codex_result", AuthorizationHTTP, "/internal/codex/jobs/result", []string{"job", "artifact", "workflow"}, []ACLAction{ACLExecute, ACLWrite}, []string{"service"}, true, true, AuthorizationLegacyGuarded),
	authSurface("http.internal_render_result", AuthorizationHTTP, "/internal/render/jobs/result", []string{"job", "artifact", "blob"}, []ACLAction{ACLExecute, ACLWrite}, []string{"service"}, true, true, AuthorizationLegacyGuarded),
}

var authorizationCapabilitySurfaces = []AuthorizationSurface{
	authSurface("capability.artifact_render", AuthorizationCapability, "/artifacts/render", []string{"artifact", "revision"}, []ACLAction{ACLReadContent}, []string{"public_capability"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("capability.artifact_share", AuthorizationCapability, "/a/", []string{"artifact", "revision"}, []ACLAction{ACLReadContent}, []string{"public_capability"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("capability.deal_room", AuthorizationCapability, "/deal-room/", []string{"deal_room", "package", "artifact", "revision", "blob"}, []ACLAction{ACLReadContent, ACLExport}, []string{"public_capability"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("capability.guest_join", AuthorizationCapability, "/guest/join", []string{"room", "guest_capability", "membership"}, []ACLAction{ACLCreateChild}, []string{"public_capability"}, false, true, AuthorizationLegacyGuarded),
}

var authorizationWebSocketInboundEvents = []string{
	"participant", "office", "office_ping", "room_ping", "media_ready", "request_participant_tracks", "candidate", "answer", "restart_ice", "select_layer",
	"assistant_query", "scout_chat_reset", "scout_chat", "room_chat", "room_chat_delete", "manual_create_ticket", "manual_update_ticket", "manual_delete_ticket",
	"undo_delete_ticket", "archive_meeting", "set_recording", "participant_media_state", "voice_control", "media_quality", "media_error", "screen_share_started", "screen_share_stopped",
}

func websocketInboundAuthorizationSurfaces() []AuthorizationSurface {
	result := make([]AuthorizationSurface, 0, len(authorizationWebSocketInboundEvents))
	for _, event := range authorizationWebSocketInboundEvents {
		action := ACLExecute
		families := []string{"room", "meeting"}
		readsBody := false
		status := AuthorizationLegacyGuarded
		switch event {
		case "assistant_query", "scout_chat_reset", "scout_chat":
			action, families, readsBody, status = ACLReadContent, []string{"memory", "chat_thread", "artifact"}, true, AuthorizationCanonicalNeeded
		case "room_chat", "room_chat_delete":
			action, families, readsBody, status = ACLWrite, []string{"room", "meeting", "chat_thread"}, true, AuthorizationCanonicalNeeded
		case "manual_create_ticket", "manual_update_ticket", "manual_delete_ticket", "undo_delete_ticket":
			action, families, readsBody, status = ACLWrite, []string{"board_card"}, true, AuthorizationCanonicalNeeded
		case "archive_meeting":
			action, families, status = ACLExport, []string{"meeting", "archive"}, AuthorizationCanonicalNeeded
		case "set_recording", "voice_control":
			action, families, status = ACLManage, []string{"room", "meeting"}, AuthorizationCanonicalNeeded
		}
		result = append(result, authSurface("ws.in."+event, AuthorizationWebSocketIn, event, families, []ACLAction{action}, []string{"user", "guest"}, readsBody, false, status))
	}
	return result
}

var authorizationFanoutSurfaces = []AuthorizationSurface{
	authSurface("ws.bootstrap.member_room", AuthorizationWSBootstrap, "member room bootstrap", []string{"room", "meeting", "board_card", "memory", "notification", "proposal"}, []ACLAction{ACLReadMetadata, ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("ws.bootstrap.member_office", AuthorizationWSBootstrap, "member office bootstrap", []string{"room", "meeting", "board_card", "memory", "notification", "proposal"}, []ACLAction{ACLReadMetadata, ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("ws.bootstrap.guest_room", AuthorizationWSBootstrap, "guest room bootstrap", []string{"room", "meeting", "room_chat"}, []ACLAction{ACLReadMetadata, ACLReadContent}, []string{"guest"}, true, true, AuthorizationLegacyGuarded),
	authSurface("ws.out.memory", AuthorizationWebSocketOut, "memory", []string{"memory", "meeting", "artifact"}, []ACLAction{ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("ws.out.artifact", AuthorizationWebSocketOut, "artifact", []string{"artifact", "revision"}, []ACLAction{ACLReadMetadata, ACLReadContent}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("ws.out.room_chat", AuthorizationWebSocketOut, "room_chat", []string{"room", "meeting", "room_chat"}, []ACLAction{ACLReadContent}, []string{"user", "guest"}, true, true, AuthorizationLegacyGuarded),
	authSurface("ws.out.meeting", AuthorizationWebSocketOut, "meeting", []string{"room", "meeting"}, []ACLAction{ACLReadMetadata, ACLReadContent}, []string{"user", "guest"}, true, true, AuthorizationLegacyGuarded),
	authSurface("ws.out.notification", AuthorizationWebSocketOut, "notification", []string{"notification"}, []ACLAction{ACLReadContent}, []string{"user"}, true, true, AuthorizationLegacyGuarded),
	authSurface("ws.out.proposal", AuthorizationWebSocketOut, "codex_proposal", []string{"proposal", "workflow"}, []ACLAction{ACLReadContent, ACLApprove, ACLExecute}, []string{"user"}, true, false, AuthorizationCanonicalNeeded),
}

var authorizationWorkerSurfaces = []AuthorizationSurface{
	authSurface("worker.meeting_brain", AuthorizationWorker, "runMeetingBrainOnce", []string{"meeting", "memory"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.meeting_board", AuthorizationWorker, "runMeetingBoardOnce", []string{"meeting", "memory", "board_card"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.research_suggestion", AuthorizationWorker, "runResearchSuggestionOnce", []string{"meeting", "memory", "proposal"}, []ACLAction{ACLReadContent, ACLCreateChild}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.mission_intelligence", AuthorizationWorker, "mission intelligence", []string{"memory", "package", "artifact", "decision"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.decision_ledger", AuthorizationWorker, "decision ledger", []string{"memory", "decision"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.narrative", AuthorizationWorker, "narrative maintainer", []string{"memory", "artifact"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.meeting_digest", AuthorizationWorker, "meeting digest", []string{"meeting", "memory", "digest"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.day_company_digest", AuthorizationWorker, "day/company digest", []string{"memory", "digest"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.embedding_maintainer", AuthorizationWorker, "runEmbeddingMaintainerOnce", []string{"memory", "artifact", "embedding"}, []ACLAction{ACLReadContent, ACLWrite}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.agent_thread", AuthorizationWorker, "runAgentThread", []string{"workflow", "artifact", "memory", "board_card"}, []ACLAction{ACLReadContent, ACLWrite, ACLExecute}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.codex_runner", AuthorizationWorker, "codex runner", []string{"job", "workflow", "artifact"}, []ACLAction{ACLExecute, ACLWrite}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
	authSurface("worker.render_runner", AuthorizationWorker, "render runner", []string{"job", "artifact", "revision", "blob"}, []ACLAction{ACLReadContent, ACLExport, ACLWrite}, []string{"service"}, true, false, AuthorizationCanonicalNeeded),
}

// AuthorizationSurfaces returns a copy suitable for JSON export, CI checks,
// and parity tooling without exposing the registry's backing slices.
func AuthorizationSurfaces() []AuthorizationSurface {
	result := make([]AuthorizationSurface, 0, len(authorizationHTTPSurfaces)+len(authorizationCapabilitySurfaces)+len(authorizationFanoutSurfaces)+len(authorizationWorkerSurfaces)+len(authorizationWebSocketInboundEvents))
	result = append(result, authorizationHTTPSurfaces...)
	result = append(result, authorizationCapabilitySurfaces...)
	result = append(result, websocketInboundAuthorizationSurfaces()...)
	result = append(result, authorizationFanoutSurfaces...)
	result = append(result, authorizationWorkerSurfaces...)
	return result
}
