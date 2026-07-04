package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestPrivateVoiceServerPayloadStaysBounded is the W6 regression gate. The full
// private-voice session (instructions + every tool schema) is built server-side
// and sent server→OpenAI at call creation (privateRealtimeVoiceSessionConfig →
// createRealtimeCallWithSession). It is NOT the browser data-channel payload.
// Measured 2026-07 at ~32,186 bytes (6,155 B instructions + 24,775 B for 30 tool
// schemas); guard a sane ceiling so a tool-schema blowup can't silently balloon
// it beyond WebRTC/SCTP + OpenAI-request headroom.
func TestPrivateVoiceServerPayloadStaysBounded(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	cfg := app.privateRealtimeVoiceSessionConfig("gpt-realtime-2")
	full, err := json.Marshal(map[string]any{"type": "session.update", "session": cfg})
	if err != nil {
		t.Fatalf("marshal session.update: %v", err)
	}
	const maxBytes = 64 << 10 // 64 KB ceiling; current measured value ~32 KB
	if len(full) > maxBytes {
		t.Fatalf("private-voice server session.update = %d bytes, over the %d-byte ceiling (a tool-schema blowup?)", len(full), maxBytes)
	}
	// It must genuinely carry instructions + tools — an empty config would pass
	// the size bound vacuously.
	if _, ok := cfg["instructions"]; !ok {
		t.Fatal("private-voice config is missing instructions")
	}
	if _, ok := cfg["tools"]; !ok {
		t.Fatal("private-voice config is missing tools")
	}
}

// TestPrivateGrillClientSwapIsInstructionsOnly pins the OTHER half of the split:
// the browser's grill session.update over its own data channel carries the
// instructions string ONLY — never the tool schemas (that is the server's job).
// functionBody-scoped so it asserts the actual swap, not a stray file match.
func TestPrivateGrillClientSwapIsInstructionsOnly(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	body := functionBody(string(rawHTML), "function applyPrivateGrillSessionUpdate(instructions)")
	if body == "" {
		t.Fatal("index.html missing applyPrivateGrillSessionUpdate")
	}
	for _, want := range []string{
		"type: 'session.update'",
		"session: { type: 'realtime', instructions: text }",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("client grill swap must send %q (instructions-only session.update)", want)
		}
	}
	// The client swap must NOT smuggle tool schemas over the data channel.
	if strings.Contains(body, "tools") {
		t.Fatal("client grill swap must not carry tool schemas over the data channel — those are server-side only")
	}
}
