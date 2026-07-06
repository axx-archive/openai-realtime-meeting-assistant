package main

// The micro-survey chips' frontend contract (packaging OS §5 "garnish, not a
// surface", Wave 2 item 11). These pins hold rule zero: two chips on EXISTING
// completion surfaces (return card, goalcard complete terminal, goal-verified
// notification) — never a new surface — with a dumb client: the server owns
// every rate rule, and a 429 makes the chips vanish silently.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForSurveyChips(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// The chip pair: "landed" posts immediately; "off" reveals the one-line
// optional note first and posts on the second tap / send / Enter.
func TestIndexSurveyChipPairAndOptionalNote(t *testing.T) {
	html := readIndexForSurveyChips(t)
	body := functionBody(html, "function surveyChipsNode(artifactId)")
	if body == "" {
		t.Fatal("could not extract surveyChipsNode body")
	}
	for _, want := range []string{
		"'landed'",
		"'off'",
		"submitArtifactSurvey(wrap, id, 'landed', '')",
		"noteRow.hidden = false",
		"submitArtifactSurvey(wrap, id, 'off', note.value)",
		"'what was off? (optional)'",
		// answered artifacts never re-show the chips on a re-render
		"surveyAnsweredArtifacts.has(id)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("surveyChipsNode body missing %q", want)
		}
	}
}

// The POST shape and the dumb-client rule: {artifactId, verdict, note} to
// /signals/survey, 429 (or any refusal) hides the chips silently.
func TestIndexSurveySubmitShapeAnd429Silence(t *testing.T) {
	html := readIndexForSurveyChips(t)
	body := functionBody(html, "async function submitArtifactSurvey(wrap, artifactId, verdict, noteText)")
	if body == "" {
		t.Fatal("could not extract submitArtifactSurvey body")
	}
	for _, want := range []string{
		"postAuthJSON('/signals/survey'",
		"artifactId,",
		"verdict,",
		"note: String(noteText || '').trim()",
		"status === 429 || !ok",
		"wrap.remove()",
		"'noted'",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("submitArtifactSurvey body missing %q", want)
		}
	}
}

// The three existing surfaces carry the chips — and only those; no new
// survey surface exists.
func TestIndexSurveyChipsRideExistingSurfaces(t *testing.T) {
	html := readIndexForSurveyChips(t)

	// 1. the return-to-origin completion card in chat
	returnCard := functionBodyAfterSignature(html, "function renderReturnCard(artifact, options = {})")
	if returnCard == "" {
		t.Fatal("could not extract renderReturnCard body")
	}
	if !strings.Contains(returnCard, "surveyChipsNode(artifact?.id)") {
		t.Error("renderReturnCard must append the survey chips")
	}

	// 2. the goalcard's complete terminal, with the persistent-node guard so
	// terminal re-renders never wipe a half-typed "off" note — the chips ride
	// inside the labeled afterword row (feed redesign §4)
	terminal := functionBody(html, "function goalCardRenderTerminal(card, artifact, plan, state, prevState)")
	if terminal == "" {
		t.Fatal("could not extract goalCardRenderTerminal body")
	}
	for _, want := range []string{
		"if (!card.__surveyNode) card.__surveyNode = surveyChipsNode(artifact.id)",
		"afterword.appendChild(card.__surveyNode)",
		"goalcard__afterword",
		"'did this land?'",
	} {
		if !strings.Contains(terminal, want) {
			t.Errorf("goalCardRenderTerminal body missing %q", want)
		}
	}

	// 3. the goal-verified notification row (a div, like approvals, so the
	// chips can legally nest)
	notification := functionBody(html, "function notificationItemNode(entry)")
	if notification == "" {
		t.Fatal("could not extract notificationItemNode body")
	}
	for _, want := range []string{
		"entry.text.startsWith('Goal verified')",
		"surveyChipsNode(entry.artifactId)",
		"notification-item--survey",
	} {
		if !strings.Contains(notification, want) {
			t.Errorf("notificationItemNode body missing %q", want)
		}
	}
}
