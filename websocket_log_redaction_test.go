package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// §4.5: the room passcode rides the participant hello and must never reach
// the logs — prod runs PION_LOG_INFO=all, so the read loop's raw-frame Info
// log is a log sink. These tests pin the scrubber and pin the read loop to it.

func TestWebsocketFrameForLogRedactsHelloPasscode(t *testing.T) {
	const secret = "hunter2-super-secret"
	hello, err := json.Marshal(map[string]string{
		"endpointId": "ep-abc123",
		"passcode":   secret,
	})
	if err != nil {
		t.Fatalf("marshal hello payload: %v", err)
	}
	frame, err := json.Marshal(websocketMessage{Event: "participant", Data: string(hello)})
	if err != nil {
		t.Fatalf("marshal hello frame: %v", err)
	}

	logged := websocketFrameForLog(frame)
	if strings.Contains(logged, secret) {
		t.Fatalf("passcode leaked into the log line: %s", logged)
	}
	if !strings.Contains(logged, "[redacted]") {
		t.Fatalf("expected redaction marker in log line, got: %s", logged)
	}
	// The rest of the hello stays debuggable.
	if !strings.Contains(logged, "participant") || !strings.Contains(logged, "ep-abc123") {
		t.Fatalf("redaction destroyed non-secret frame content: %s", logged)
	}
}

func TestWebsocketFrameForLogPassesOrdinaryFramesVerbatim(t *testing.T) {
	for _, raw := range []string{
		`{"event":"candidate","data":"{\"candidate\":\"candidate:1 1 udp 2130706431 10.0.0.1 3478 typ host\"}"}`,
		`{"event":"room_chat","data":"{\"text\":\"see you at 3\"}"}`,
		`{"event":"participant","data":"{\"endpointId\":\"ep-1\"}"}`,
	} {
		if got := websocketFrameForLog([]byte(raw)); got != raw {
			t.Fatalf("frame without a passcode must log verbatim:\n raw=%s\n got=%s", raw, got)
		}
	}
}

func TestWebsocketFrameForLogFailsClosedOnUnparseableSecretFrames(t *testing.T) {
	const secret = "hunter2-super-secret"
	cases := []string{
		// Truncated frame that still holds the secret in cleartext.
		`{"event":"participant","data":"{\"passcode\":\"` + secret + `\"`,
		// Envelope parses but the payload is not a JSON object.
		`{"event":"participant","data":"passcode=` + secret + `"}`,
	}
	for _, raw := range cases {
		logged := websocketFrameForLog([]byte(raw))
		if strings.Contains(logged, secret) {
			t.Fatalf("secret leaked from an unparseable frame:\n raw=%s\n got=%s", raw, logged)
		}
		if !strings.Contains(logged, "withheld") {
			t.Fatalf("expected fail-closed withholding, got: %s", logged)
		}
	}
}

// The read loop must log frames only through the scrubber — a bare
// log of raw would reintroduce the passcode leak.
func TestWebsocketReadLoopLogsThroughScrubber(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(source), `log.Infof("Got message: %s", websocketFrameForLog(raw))`) {
		t.Fatal("read loop no longer logs inbound frames through websocketFrameForLog")
	}
	if strings.Contains(string(source), `log.Infof("Got message: %s", raw)`) {
		t.Fatal("read loop logs the raw inbound frame — the hello passcode would land in the logs (§4.5)")
	}
}
