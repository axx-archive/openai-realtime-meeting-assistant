package main

import (
	"os"
	"strings"
	"testing"
)

func TestWebRTCSignalingDoesNotLogRawICECandidates(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	text := string(source)
	for _, forbidden := range []string{
		`Send candidate to client: %s`,
		`Got candidate: %v`,
		`candidate: %v`,
		`local=%s:%d`,
		`remote=%s:%d`,
		`room_signal_offer_payload participant=`,
		`room_signal_answer participant=`,
		`restart_ice_rate_limited session=`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("main.go contains raw ICE logging format %q", forbidden)
		}
	}
}

func TestMediaLogPrincipalRedactsAccountIdentity(t *testing.T) {
	if got := mediaLogPrincipal("member display name"); got != "redacted" {
		t.Fatalf("media log principal = %q, want redacted", got)
	}
	if got := mediaLogPrincipal("  "); got != "" {
		t.Fatalf("empty media log principal = %q, want empty", got)
	}
}
