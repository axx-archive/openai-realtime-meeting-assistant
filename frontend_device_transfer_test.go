package main

import (
	"os"
	"strings"
	"testing"
)

func readIndexHTMLForDeviceTransfer(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func deviceTransferSourceSection(t *testing.T, source, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(source, startMarker)
	if start == -1 {
		t.Fatalf("source missing start marker %q", startMarker)
	}
	rest := source[start:]
	end := strings.Index(rest, endMarker)
	if end == -1 {
		t.Fatalf("source after %q missing end marker %q", startMarker, endMarker)
	}
	return rest[:end]
}

func TestIndexDeviceTransferPromptIsExplicitAndAccessible(t *testing.T) {
	html := readIndexHTMLForDeviceTransfer(t)
	for _, want := range []string{
		`id="roomTransferDialog"`,
		`role="dialog" aria-modal="true"`,
		`aria-labelledby="roomTransferTitle"`,
		`aria-describedby="roomTransferCopy"`,
		`Already in this room`,
		`<strong>Add this device</strong>`,
		`Keep both devices in the call.`,
		`<strong>Transfer here</strong>`,
		`Leave on the other device.`,
		`id="roomTransferCancel"`,
		`.room-transfer__choice:active,`,
		`transform: scale(0.96);`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing device-transfer prompt contract %q", want)
		}
	}
}

func TestIndexDeviceTransferDecisionUsesFreshEndpointTruth(t *testing.T) {
	html := readIndexHTMLForDeviceTransfer(t)

	fetchBody := functionBody(html, "async function fetchSelectedRoomSnapshot()")
	for _, want := range []string{
		"activeJoin.roomId || 'office'",
		"{ cache: 'no-store' }",
		"return response.json()",
	} {
		if !strings.Contains(fetchBody, want) {
			t.Fatalf("fetchSelectedRoomSnapshot must use current authoritative room state; missing %q", want)
		}
	}

	detectionBody := functionBody(html, "function roomSnapshotHasOtherAccountEndpoint(payload, participantName, endpointId)")
	for _, want := range []string{
		"payload?.participants",
		"payload?.endpointMediaStates",
		"Object.keys(endpointStates)",
		"endpointIds.some(id => id !== endpointId)",
		"payload?.endpointCounts",
		"endpointCount > 1",
	} {
		if !strings.Contains(detectionBody, want) {
			t.Fatalf("other-device detection is not endpoint-aware; missing %q", want)
		}
	}

	decisionBody := functionBody(html, "async function resolveRoomJoinDeviceDecision()")
	for _, want := range []string{
		"if (!authedUser || activeJoin.guest || guestMode)",
		"await fetchSelectedRoomSnapshot()",
		"participantNameFromAccount(authedUser)",
		"roomSnapshotHasOtherAccountEndpoint(payload, participantName, ensureEndpointId())",
		"return promptRoomTransferDecision(roomName)",
		"return 'keep'",
	} {
		if !strings.Contains(decisionBody, want) {
			t.Fatalf("join device decision missing %q", want)
		}
	}
}

func TestIndexDeviceTransferIsOneShotAndReconnectSafe(t *testing.T) {
	html := readIndexHTMLForDeviceTransfer(t)

	joinBody := deviceTransferSourceSection(t, html, "async function joinRoom(options = {})", "function joinMediaWithWatchdog(voiceOnly)")
	decisionAt := strings.Index(joinBody, "await resolveRoomJoinDeviceDecision()")
	mediaAt := strings.Index(joinBody, "await joinMediaWithWatchdog(voiceOnly)")
	if decisionAt == -1 || mediaAt == -1 || decisionAt > mediaAt {
		t.Fatal("the device choice must finish before camera/microphone acquisition")
	}
	for _, want := range []string{
		"if (deviceJoinDecision === 'cancel')",
		"const transferExisting = deviceJoinDecision === 'transfer'",
		"openRoomWebSocket({ transferExisting })",
	} {
		if !strings.Contains(joinBody, want) {
			t.Fatalf("joinRoom missing device-transfer behavior %q", want)
		}
	}

	openBody := deviceTransferSourceSection(t, html, "function openRoomWebSocket(options = {})", "function roomCanReconnectSignal()")
	for _, want := range []string{
		"const reconnect = Boolean(options.reconnect)",
		"const initialTransferExisting = !reconnect && options.transferExisting === true",
		"if (initialTransferExisting)",
		"participantHello.transferExisting = true",
	} {
		if !strings.Contains(openBody, want) {
			t.Fatalf("openRoomWebSocket missing one-shot transfer guard %q", want)
		}
	}
	if strings.Contains(openBody, "transferExisting: false") {
		t.Fatal("ordinary joins must omit transferExisting instead of sending a false transfer instruction")
	}

	reconnectBody := deviceTransferSourceSection(t, html, "function scheduleSignalingReconnect(reason, options = {})", "function failSignalingReconnect(reason)")
	if !strings.Contains(reconnectBody, "openRoomWebSocket({ reconnect: true })") {
		t.Fatal("signaling reconnect must redial without carrying the initial transfer choice")
	}
}
