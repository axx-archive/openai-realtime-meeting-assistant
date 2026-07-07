package main

// Card 069 frontend pins: the Mission Intelligence ledger renders a PROPOSED
// (default) decision honestly — "proposed · awaiting ratification" on the meta
// line plus a ratify door that POSTs the real handler path — and the approval
// card surfaces the heavy-lane endorsement progress the server returns.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForGovernanceLanes(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// A proposed decision announces itself and carries the ratify door — the
// exact POST shape assistantDecisionRatifyHandler decodes. Card 081 extracted
// the row builder into the shared intelDecisionNode helper, so the ratify door
// lives there now.
func TestIndexDecisionLedgerProposedRatifyDoor(t *testing.T) {
	html := readIndexForGovernanceLanes(t)
	body := functionBody(html, "function intelDecisionNode(decision, rerender)")
	if body == "" {
		t.Fatal("could not extract intelDecisionNode body")
	}
	for _, want := range []string{
		"'proposed · awaiting ratification'",
		"decision?.status || '') === 'proposed'",
		"postAuthJSON('/assistant/decisions/ratify', { decisionId: decision.id })",
		"intel-item__ratify",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("intelDecisionNode body missing %q", want)
		}
	}
	// The button has its style — the door must not render as an unstyled stub.
	if !strings.Contains(html, ".intel-item__ratify {") {
		t.Error("index.html missing the .intel-item__ratify style block")
	}
}

// The approve seam surfaces the server's endorsement progress ("endorsement
// recorded (1/2)") instead of silence — the heavy-lane consensus is honest.
func TestIndexSubmitApprovalSurfacesEndorsementProgress(t *testing.T) {
	html := readIndexForGovernanceLanes(t)
	body := functionBody(html, "async function submitApproval(id, action, reason, choice)")
	if body == "" {
		t.Fatal("could not extract submitApproval body")
	}
	for _, want := range []string{
		"const { ok, data } = await postAuthJSON('/artifacts/action', body)",
		"typeof data.message === 'string'",
		"setLog(data.message)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("submitApproval body missing %q", want)
		}
	}
}
