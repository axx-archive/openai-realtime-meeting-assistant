package main

import (
	"os"
	"strings"
	"testing"
)

func TestRoomConsentUIKeepsChoicesForGuestsAndInternalPolicyOutOfTheWay(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	for _, required := range []string{
		`id="consentToggle"`, `id="consentPanel"`, `/api/consent`,
		`guestMode ? '?as=guest' : ''`,
		`['audio_capture',`, `['transcription',`, `['model_analysis',`, `['org_memory',`,
		`External guests control the server copy of their microphone`,
		`consentToggleButton.hidden = !guestMode`,
		`consentSnapshot?.choicesMutable`,
		`consentSnapshot?.policyManaged`,
		`Internal employee use follows the company rules of the road.`,
		`disposition === 'granted' ? 'withdrawn' : 'denied'`,
	} {
		if !strings.Contains(page, required) {
			t.Fatalf("consent UI missing %q", required)
		}
	}
}
