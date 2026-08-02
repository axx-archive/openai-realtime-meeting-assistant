package main

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendScoutMemoryIsInspectableCorrigibleAndDefaultPrivate(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`data-settings-section="scout-memory"`,
		`What Scout remembers about me`,
		`turn on private memory`,
		`learn from repeated patterns · not active yet`,
		`shared-channel preferences · not active yet`,
		`no conversation is inferred into a preference in this build`,
		`channel conversations are not currently turned into preferences by the app`,
		`scope: 'private'`,
		`added by you in settings`,
		`inferred from repeated patterns`,
		`paused ? ' · paused'`,
		`expires ${relationshipMemoryExpiryFormatter.format(date)}`,
		`save correction`,
		`Forget this preference and remove its source evidence?`,
		`Turn off Scout memory and remove every saved preference and its source evidence?`,
		`nothing is being learned or stored`,
		`view source`,
		`relationshipMemoryLoadEpoch`,
		`identity !== String(authedUser?.email`,
		`function setAuthenticatedUser(nextUser)`,
		`case 'relationship_memory_changed':`,
		`handleRelationshipMemoryChanged(message.data)`,
		`stopRealtimeVoiceConversation({`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Scout memory surface missing %q", want)
		}
	}
	for _, route := range []string{
		`/api/stride/v1/coworker/relationships'`,
		`/api/stride/v1/coworker/relationships/consent`,
		`/api/stride/v1/coworker/relationships/remember`,
		`/api/stride/v1/coworker/relationships/correct`,
		`/api/stride/v1/coworker/relationships/forget`,
	} {
		if !strings.Contains(html, route) {
			t.Errorf("Scout memory surface missing route %q", route)
		}
	}
}

func TestFrontendScoutMemoryAuthAndRealtimeInvalidationAreFailClosed(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	if got := strings.Count(html, "authedUser ="); got != 2 { // declaration + setAuthenticatedUser assignment
		t.Fatalf("found %d direct authedUser assignments; every transition must use setAuthenticatedUser", got)
	}
	for _, want := range []string{
		"relationshipMemoryLoadEpoch += 1",
		"relationshipMemorySnapshot = null",
		"sendKanbanEventToUser(userEmail, \"relationship_memory_changed\"",
		"if (relationshipMemoryBusy || !authedUser)",
		"privateRealtimeVoiceInFlight()",
		"void loadRelationshipMemory()",
	} {
		if !strings.Contains(html+string(mustReadTestFile(t, "stride_coworker_http.go")), want) {
			t.Errorf("relationship-memory invalidation missing %q", want)
		}
	}
	handlerStart := strings.Index(html, "function handleRelationshipMemoryChanged(payload)")
	handlerEnd := strings.Index(html[handlerStart:], "async function setRelationshipMemoryConsent")
	if handlerStart < 0 || handlerEnd < 0 {
		t.Fatal("could not isolate relationship-memory invalidation handler")
	}
	handler := html[handlerStart : handlerStart+handlerEnd]
	closeIndex := strings.Index(handler, "stopRealtimeVoiceConversation({")
	busyIndex := strings.Index(handler, "if (relationshipMemoryBusy || !authedUser)")
	if closeIndex < 0 || busyIndex < 0 || closeIndex > busyIndex {
		t.Fatal("relationship-memory invalidation must close stale Realtime before the busy-write early return")
	}
}

func TestFrontendScoutMemoryAuthTransitionsFencePrivateRealtimeBeforeIdentitySwap(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	setterStart := strings.Index(html, "function setAuthenticatedUser(nextUser)")
	setterEndOffset := -1
	if setterStart >= 0 {
		setterEndOffset = strings.Index(html[setterStart:], "let roomRecordingUpdatedAt")
	}
	if setterStart < 0 || setterEndOffset < 0 {
		t.Fatal("could not isolate the authenticated-user transition setter")
	}
	setter := html[setterStart : setterStart+setterEndOffset]

	for _, want := range []string{
		"const identityChanged = previousIdentity !== nextIdentity",
		"if (identityChanged) {",
		"if (privateRealtimeVoiceInFlight())",
		"stopRealtimeVoiceConversation({",
		"notifyServer: false",
		"terminalReason: 'identity_changed'",
		"relationshipMemoryReconnectFence = null",
		"authedUser = nextUser || null",
	} {
		if !strings.Contains(setter, want) {
			t.Errorf("authenticated-user transition missing %q", want)
		}
	}
	stopIndex := strings.Index(setter, "stopRealtimeVoiceConversation({")
	assignmentIndex := strings.Index(setter, "authedUser = nextUser || null")
	if stopIndex < 0 || assignmentIndex < 0 || stopIndex > assignmentIndex {
		t.Fatal("private Realtime must be fenced before the replacement identity becomes visible")
	}

	// The setter is declared before the private voice helpers in this script.
	// Requiring function declarations (rather than later const initializers)
	// preserves hoisting for the asynchronous boot-time auth transition.
	for _, declaration := range []string{
		"function privateRealtimeVoiceInFlight()",
		"function stopRealtimeVoiceConversation(options = {})",
	} {
		if !strings.Contains(html, declaration) {
			t.Errorf("auth fence requires hoisted declaration %q", declaration)
		}
	}

	signOutStart := strings.Index(html, "async function signOutOfAccount()")
	signOutEndOffset := -1
	if signOutStart >= 0 {
		signOutEndOffset = strings.Index(html[signOutStart:], "/* ---------- Scout relationship memory")
	}
	if signOutStart < 0 || signOutEndOffset < 0 || !strings.Contains(html[signOutStart:signOutStart+signOutEndOffset], "setAuthenticatedUser(null)") {
		t.Fatal("sign-out must cross the authenticated-user transition fence")
	}
	if !strings.Contains(html, "setAuthenticatedUser(await response.json())") {
		t.Fatal("server-observed account switches must cross the authenticated-user transition fence")
	}
}

func TestFrontendScoutMemoryOfficeControlChannelFailsClosedAndReconcilesRevision(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)

	officeStart := strings.Index(html, "function ensureOfficeSocket()")
	officeEndOffset := -1
	if officeStart >= 0 {
		officeEndOffset = strings.Index(html[officeStart:], "function scheduleOfficeSocketReconnect()")
	}
	if officeStart < 0 || officeEndOffset < 0 {
		t.Fatal("could not isolate office socket lifecycle")
	}
	office := html[officeStart : officeStart+officeEndOffset]

	onCloseStart := strings.Index(office, "socket.onclose = () => {")
	onCloseEndOffset := -1
	if onCloseStart >= 0 {
		onCloseEndOffset = strings.Index(office[onCloseStart:], "socket.onerror = () => {")
	}
	if onCloseStart < 0 || onCloseEndOffset < 0 {
		t.Fatal("could not isolate office-socket close handler")
	}
	onClose := office[onCloseStart : onCloseStart+onCloseEndOffset]
	for _, want := range []string{
		"relationshipMemoryReconnectFence = {",
		"revision: relationshipMemoryVisibleRevision()",
		"if (privateRealtimeVoiceInFlight())",
		"stopRealtimeVoiceConversation({",
		"notifyServer: false",
		"terminalReason: 'control_channel_lost'",
		"scheduleOfficeSocketReconnect()",
	} {
		if !strings.Contains(onClose, want) {
			t.Errorf("office-socket close fence missing %q", want)
		}
	}
	if stopIndex, reconnectIndex := strings.Index(onClose, "stopRealtimeVoiceConversation({"), strings.Index(onClose, "scheduleOfficeSocketReconnect()"); stopIndex < 0 || reconnectIndex < 0 || stopIndex > reconnectIndex {
		t.Fatal("private Realtime must stop before the office socket schedules reconnection")
	}

	onOpenStart := strings.Index(office, "socket.onopen = () => {")
	onOpenEndOffset := -1
	if onOpenStart >= 0 {
		onOpenEndOffset = strings.Index(office[onOpenStart:], "socket.onmessage = event => {")
	}
	if onOpenStart < 0 || onOpenEndOffset < 0 {
		t.Fatal("could not isolate office-socket open handler")
	}
	onOpen := office[onOpenStart : onOpenStart+onOpenEndOffset]
	for _, want := range []string{
		"const reconnectFence = relationshipMemoryReconnectFence",
		"syncOSAssistantAvailability()",
		"reconcileRelationshipMemoryAfterOfficeReconnect(reconnectFence)",
	} {
		if !strings.Contains(onOpen, want) {
			t.Errorf("office-socket reconnect reconciliation missing %q", want)
		}
	}

	reconcileStart := strings.Index(html, "function reconcileRelationshipMemoryAfterOfficeReconnect(fence)")
	reconcileEndOffset := -1
	if reconcileStart >= 0 {
		reconcileEndOffset = strings.Index(html[reconcileStart:], "function setRelationshipMemoryStatus")
	}
	if reconcileStart < 0 || reconcileEndOffset < 0 {
		t.Fatal("could not isolate relationship-memory reconnect reconciliation")
	}
	reconcile := html[reconcileStart : reconcileStart+reconcileEndOffset]
	for _, want := range []string{
		"fence.identity !== identity",
		"relationshipMemoryBusy",
		"loadRelationshipMemory({ previousVisibleRevision: fence.revision })",
	} {
		if !strings.Contains(reconcile, want) {
			t.Errorf("relationship-memory reconnect fence missing %q", want)
		}
	}

	loadStart := strings.Index(html, "async function loadRelationshipMemory(options = {})")
	loadEndOffset := -1
	if loadStart >= 0 {
		loadEndOffset = strings.Index(html[loadStart:], "async function mutateRelationshipMemory")
	}
	if loadStart < 0 || loadEndOffset < 0 {
		t.Fatal("could not isolate authoritative relationship-memory reload")
	}
	load := html[loadStart : loadStart+loadEndOffset]
	for _, want := range []string{
		"previousVisibleRevision",
		"nextRevision !== previousVisibleRevision",
		"privateRealtimeVoiceInFlight()",
		"terminalReason: 'relationship_revision_changed'",
		"relationshipMemorySnapshot = nextSnapshot",
	} {
		if !strings.Contains(load, want) {
			t.Errorf("relationship-memory reconnect reload missing %q", want)
		}
	}
	stopIndex := strings.Index(load, "terminalReason: 'relationship_revision_changed'")
	applyIndex := strings.Index(load, "relationshipMemorySnapshot = nextSnapshot")
	if stopIndex < 0 || applyIndex < 0 || stopIndex > applyIndex {
		t.Fatal("a changed authoritative revision must fence private Realtime before the snapshot is applied")
	}
}

func TestFrontendScoutMemoryPrivateRealtimeRequiresLiveOfficeControlChannel(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)

	availabilityStart := strings.Index(html, "function osAssistantAvailable()")
	availabilityEndOffset := -1
	if availabilityStart >= 0 {
		availabilityEndOffset = strings.Index(html[availabilityStart:], "function syncOSAssistantAvailability()")
	}
	if availabilityStart < 0 || availabilityEndOffset < 0 {
		t.Fatal("could not isolate personal Scout availability predicate")
	}
	availability := html[availabilityStart : availabilityStart+availabilityEndOffset]
	if !strings.Contains(availability, "Boolean(authedUser) && privateRealtimeVoiceSurfaceAvailable() && officeSocketLive()") {
		t.Fatal("personal Scout must not appear available without its live office control channel")
	}

	start := strings.Index(html, "async function startPrivateRealtimeVoiceConversation()")
	endOffset := -1
	if start >= 0 {
		endOffset = strings.Index(html[start:], "async function beginPrivateRealtimeVoiceSession")
	}
	if start < 0 || endOffset < 0 {
		t.Fatal("could not isolate private Realtime start path")
	}
	startPath := html[start : start+endOffset]
	for _, want := range []string{
		"if (!officeSocketLive())",
		"if (privateRealtimeVoiceInFlight())",
		"stopRealtimeVoiceConversation({",
		"notifyServer: false",
		"terminalReason: 'control_channel_lost'",
		"ensureOfficeSocket()",
		"Scout is reconnecting. Try again in a moment.",
	} {
		if !strings.Contains(startPath, want) {
			t.Errorf("private Realtime control-channel gate missing %q", want)
		}
	}
	gateIndex := strings.Index(startPath, "if (!officeSocketLive())")
	modeIndex := strings.Index(startPath, "setRealtimeVoiceMode('private')")
	beginIndex := strings.Index(startPath, "await beginPrivateRealtimeVoiceSession(sessionToken)")
	if gateIndex < 0 || modeIndex < 0 || beginIndex < 0 || gateIndex > modeIndex || gateIndex > beginIndex {
		t.Fatal("the live control-channel gate must precede all private Realtime startup state and transport work")
	}

	officeStart := strings.Index(html, "function ensureOfficeSocket()")
	officeEndOffset := -1
	if officeStart >= 0 {
		officeEndOffset = strings.Index(html[officeStart:], "function scheduleOfficeSocketReconnect()")
	}
	if officeStart < 0 || officeEndOffset < 0 {
		t.Fatal("could not isolate office socket lifecycle")
	}
	office := html[officeStart : officeStart+officeEndOffset]
	onCloseStart := strings.Index(office, "socket.onclose = () => {")
	onCloseEndOffset := -1
	if onCloseStart >= 0 {
		onCloseEndOffset = strings.Index(office[onCloseStart:], "socket.onerror = () => {")
	}
	if onCloseStart < 0 || onCloseEndOffset < 0 {
		t.Fatal("could not isolate office socket close availability transition")
	}
	onClose := office[onCloseStart : onCloseStart+onCloseEndOffset]
	nullIndex := strings.Index(onClose, "officeWs = null")
	syncIndex := strings.Index(onClose, "syncOSAssistantAvailability()")
	if nullIndex < 0 || syncIndex < 0 || nullIndex > syncIndex {
		t.Fatal("office socket close must revoke visible personal Scout availability after clearing the live socket")
	}
}

func mustReadTestFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFrontendScoutMemoryRendersRuntimeValuesAsTextNodes(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.Index(html, "function renderRelationshipMemoryList(preferences)")
	end := strings.Index(html[start:], "function renderRelationshipMemoryEditor")
	if start < 0 || end < 0 {
		t.Fatal("could not isolate Scout memory renderer")
	}
	renderer := html[start : start+end]
	if strings.Contains(renderer, "innerHTML") {
		t.Fatal("Scout memory renderer must not interpolate memory values into HTML")
	}
	for _, want := range []string{"document.createElement", "textContent", "replaceChildren"} {
		if !strings.Contains(renderer, want) {
			t.Errorf("Scout memory renderer missing safe DOM primitive %q", want)
		}
	}
}
