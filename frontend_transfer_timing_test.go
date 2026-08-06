package main

import (
	"os"
	"strings"
	"testing"
)

func TestExplicitTransferCarriesBoundedStageTimingsIntoFirstMediaQualityReport(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)

	for _, want := range []string{
		"let activeRoomJoinTiming = null",
		"let pendingRoomJoinTimingReport = null",
		"schema: 'room_join_timing_v1'",
		"return Math.min(24 * 60 * 60 * 1000, Math.round(endedAt - startedAt))",
		"activeRoomJoinTiming.freshPresenceLookupStartedAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.freshPresenceLookupEndedAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.freshPresenceDecisionStartedAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.freshPresenceDecisionEndedAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.transferDialogOpenedAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.transferDecisionAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.transferDialogClosedAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.localMediaStartedAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.localMediaEndedAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.websocketDialStartedAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.websocketOpenedAt = roomJoinTimingNow()",
		"activeRoomJoinTiming.accessGrantedAt = roomJoinTimingNow()",
		"totalAutomaticJoinMs: Math.max(0, totalElapsedMs - humanDecisionMs)",
		"joinTiming: joinTiming || undefined",
		"pendingRoomJoinTimingReport = null",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("explicit transfer timing is missing %q", want)
		}
	}

	completeStart := strings.Index(html, "function completeRoomJoinTiming()")
	completeEnd := strings.Index(html[completeStart:], "function ensureEndpointId()")
	if completeStart < 0 || completeEnd < 0 {
		t.Fatal("could not isolate the transfer timing completion helper")
	}
	completeBody := html[completeStart : completeStart+completeEnd]
	if !strings.Contains(completeBody, "if (!timing?.explicitTransfer)") || !strings.Contains(completeBody, "mode: 'explicit_transfer'") {
		t.Fatal("join timing must be retained only for the explicit transfer path")
	}

	reportStart := strings.Index(html, "function sendMediaQualityReport(")
	reportEnd := strings.Index(html[reportStart:], "function mediaQualityDelta(")
	if reportStart < 0 || reportEnd < 0 {
		t.Fatal("could not isolate the existing media-quality report")
	}
	reportBody := html[reportStart : reportStart+reportEnd]
	sendAt := strings.Index(reportBody, "ws.send(JSON.stringify({")
	consumeAt := strings.Index(reportBody, "pendingRoomJoinTimingReport = null")
	if sendAt < 0 || consumeAt <= sendAt {
		t.Fatal("the transfer timing record must be consumed only after the first successful existing report send")
	}
	if strings.Contains(reportBody, "fetch(") {
		t.Fatal("transfer timing must use the existing websocket report, not add an external endpoint")
	}
}

func TestClientMediaQualityReportLogsExplicitTransferStageTimings(t *testing.T) {
	out := captureStdout(t, func() {
		logClientMediaQualityReport(
			`{"joinTiming":{"mode":"explicit_transfer","freshPresenceLookupMs":41,"freshPresenceDecisionMs":2,"humanDecisionMs":1300,"transferDialogCloseMs":221,"localMediaAcquisitionMs":377,"localMediaReused":false,"websocketDialToOpenMs":18,"websocketOpenToAccessGrantedMs":54,"accessGrantedToRoomReadyMs":83,"totalAutomaticJoinMs":796,"totalElapsedMs":2096}}`,
			"AJ", "sess-transfer")
	})
	for _, want := range []string{
		"joinMode=explicit_transfer",
		"joinPresenceLookupMs=41",
		"joinPresenceDecisionMs=2",
		"joinHumanDecisionMs=1300",
		"joinDialogCloseMs=221",
		"joinMediaAcquireMs=377",
		"joinMediaReused=false",
		"joinWSDialOpenMs=18",
		"joinWSOpenGrantMs=54",
		"joinGrantReadyMs=83",
		"joinAutomaticMs=796",
		"joinTotalMs=2096",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("transfer timing field %q missing from media-quality log: %s", want, out)
		}
	}

	ordinary := captureStdout(t, func() {
		logClientMediaQualityReport(`{"stats":{}}`, "AJ", "sess-ordinary")
	})
	if strings.Contains(ordinary, "joinMode=") || strings.Contains(ordinary, "joinAutomaticMs=") {
		t.Fatalf("ordinary recurring quality reports must not repeat empty one-shot join timing fields: %s", ordinary)
	}
}
