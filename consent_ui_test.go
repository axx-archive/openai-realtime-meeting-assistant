package main

import (
	"os"
	"strings"
	"testing"
)

func TestRoomConsentUIExposesExplicitMemberAndGuestChoices(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	for _, required := range []string{
		`id="consentToggle"`, `id="consentPanel"`, `/api/consent`,
		`guestMode ? '?as=guest' : ''`,
		`['audio_capture',`, `['transcription',`, `['model_analysis',`, `['org_memory',`,
		`Direct room audio, video, and chat continue when these are off.`,
		`disposition === 'granted' ? 'withdrawn' : 'denied'`,
	} {
		if !strings.Contains(page, required) {
			t.Fatalf("consent UI missing %q", required)
		}
	}
}
