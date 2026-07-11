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

// F15+F21: a proposed-supersession (a proposed REVERSAL) is reachable — it
// carries the ratify affordance via the SAME endpoint, labeled distinctly as a
// reversal — and unknown statuses never mislabel as an active decision.
func TestIndexDecisionLedgerProposedSupersessionRatifyDoor(t *testing.T) {
	html := readIndexForGovernanceLanes(t)
	body := functionBody(html, "function intelDecisionNode(decision, rerender)")
	if body == "" {
		t.Fatal("could not extract intelDecisionNode body")
	}
	for _, want := range []string{
		// the reversal status is recognized and drives the ratify affordance.
		"=== 'proposed-supersession'",
		"awaitingRatification",
		// distinct labeling on the meta line and the status detail row.
		"'proposed reversal · awaiting ratification'",
		"proposed reversal — ratify to supersede",
		"supersedesSummary",
		// wired to the SAME ratify action as a plain proposed default.
		"postAuthJSON('/assistant/decisions/ratify', { decisionId: decision.id })",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("intelDecisionNode body missing %q", want)
		}
	}
	// F21: the active label must be guarded on an EXPLICIT active/empty status, and
	// an unknown status falls through to its literal value — never "active".
	if !strings.Contains(body, "status === 'active' || status === ''") {
		t.Error("the active-decision label must be guarded on an explicit active status (F21)")
	}
	if !strings.Contains(body, "addRow('status', status)") {
		t.Error("an unknown decision status must render literally, never as an active decision on record (F21)")
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
