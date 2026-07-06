package main

// The process checkpoint cards + process-visibility frontend contract
// (packaging OS §3 "Where humans sit", Wave 4 item 18 — the UI half). These
// grep-style pins hold the client half of the human touchpoints: a goal parked
// at a process human_checkpoint renders its question + options as a tappable
// choice card INSIDE the goalcard terminal (reusing the confirmation-card
// grammar, not a new card system), the resume posts the existing approve seam
// carrying {choice}, process stages read legibly in the working log (titles,
// disclosed skips, render-export status), and the palette gains the fifth
// "Processes" group glyph additively.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForCheckpoint(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// 1. CHECKPOINT CARDS — a parked human_checkpoint OWNS the goalcard's gate
// terminal: the choice card, not the generic approve/send-back pair.
func TestIndexCheckpointCardOwnsGateTerminal(t *testing.T) {
	html := readIndexForCheckpoint(t)
	body := functionBody(html, "function goalCardRenderTerminal(card, artifact, plan, state, prevState)")
	if body == "" {
		t.Fatal("could not extract goalCardRenderTerminal body")
	}
	for _, want := range []string{
		// the pending-checkpoint interception inside the gate branch
		"const pendingCheckpoint = goalPendingCheckpoint(artifact, plan)",
		"if (pendingCheckpoint) {",
		"goalCardRenderCheckpoint(terminal, card, artifact, plan, pendingCheckpoint)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("gate terminal missing checkpoint interception marker %q", want)
		}
	}
}

// goalPendingCheckpoint reads the persisted plan.checkpoint first, falls back
// to the metadata["checkpoint"] mirror, and treats a resolved checkpoint (or a
// questionless one) as absent — so a running-again goal shows no card.
func TestIndexPendingCheckpointResolution(t *testing.T) {
	html := readIndexForCheckpoint(t)
	body := functionBody(html, "function goalPendingCheckpoint(artifact, plan)")
	if body == "" {
		t.Fatal("could not extract goalPendingCheckpoint body")
	}
	for _, want := range []string{
		"let checkpoint = plan?.checkpoint || null",
		"const raw = artifact?.metadata?.checkpoint",
		// resolved OR questionless → no card
		"if (!checkpoint || checkpoint.resolvedAt || !String(checkpoint.question || '').trim()) return null",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("goalPendingCheckpoint missing marker %q", want)
		}
	}
}

// The choice card's three shapes from one renderer: tappable options (COMPETE
// winner pick), a "review the draft" door per input stage (FOUNDER PASS / the
// judged synthesis), and a free-form notes input whose text IS the {choice}
// (INTAKE answers, do_not_touch notes).
func TestIndexCheckpointCardThreeShapes(t *testing.T) {
	html := readIndexForCheckpoint(t)
	body := functionBody(html, "function goalCardRenderCheckpoint(terminal, card, artifact, plan, checkpoint)")
	if body == "" {
		t.Fatal("could not extract goalCardRenderCheckpoint body")
	}
	for _, want := range []string{
		// the question is the legible headline
		"goalcard__checkpoint-question",
		"String(checkpoint.question || 'Approve this stage to continue?')",
		// review-the-draft doors open the input stages' artifacts in the
		// chat-side artifact stage (§7 — Intelligence stays the data room)
		"const inputTasks = (stageTask?.dependsOn || [])",
		"openArtifactStage(input.artifactId, input.title || input.id)",
		// inline brief exposes the judge scores + steals without leaving the card
		"goalcard__checkpoint-brief",
		// options → tappable choice buttons (labels, actions ride separately)
		"const choiceBtn = bfEl('button', 'goalcard__choice', option.label)",
		// every tap appends the typed notes as the choice suffix (the
		// prefix-matched founder-pass grammar)
		"post(noteText() ? `${option.label} — ${noteText()}` : option.label)",
		// no options → the do_not_touch notes input IS the choice
		"do_not_touch",
		"const noteText = () => String(notes?.value || '').trim()",
		"go.addEventListener('click', () => post(noteText()))",
		// resume rides the EXISTING approve seam carrying {choice}
		"submitApproval(artifact.id, 'approve', '', choice)",
		// admin-gated, mirrored honestly
		"if (!canApproveExternalWrites())",
		"'waiting on AJ'",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("goalCardRenderCheckpoint missing marker %q", want)
		}
	}
	// the node persists per card+stage so re-renders never wipe a half-typed note
	if !strings.Contains(body, "card.__checkpointNode && card.__checkpointStageId === stageId") {
		t.Error("checkpoint card must persist its node per card+stage across terminal re-renders")
	}
}

// The negative-option teeth on the card (Wave 4's disclosed gap): options
// normalize from both persisted shapes (legacy plain string → proceed, the
// {label, action} object), revise options expose the generalized do_not_touch
// notes input, hold renders the held state — badge, held-aware cache key, and
// non-proceed options disabled to mirror the server's refusal.
func TestIndexCheckpointCardNegativeActions(t *testing.T) {
	html := readIndexForCheckpoint(t)
	body := functionBody(html, "function goalCardRenderCheckpoint(terminal, card, artifact, plan, checkpoint)")
	if body == "" {
		t.Fatal("could not extract goalCardRenderCheckpoint body")
	}
	for _, want := range []string{
		// both persisted option shapes normalize; action defaults to proceed
		"typeof option === 'string'",
		"{ label: option.trim(), action: 'proceed' }",
		"String(option?.action || 'proceed').trim() || 'proceed'",
		// revise options bring the generalized send-back notes input
		"const hasRevise = options.some(option => option.action === 'revise')",
		"if (!options.length || hasRevise) {",
		"'notes for the send-back (do_not_touch lines are preserved exactly)'",
		// the actions read as what they mechanically do
		"choiceBtn.classList.add('goalcard__choice--revise')",
		"choiceBtn.classList.add('goalcard__choice--hold')",
		// the held state: badge + only proceed stays live
		"const held = !!checkpoint.held",
		"goalcard__checkpoint-held",
		"if (held && option.action !== 'proceed') choiceBtn.disabled = true",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("goalCardRenderCheckpoint missing negative-action marker %q", want)
		}
	}
	// a hold landing from the server must re-render the badge: the persisted
	// node's cache key carries the held state
	if !strings.Contains(body, "card.__checkpointStageId === stageId && card.__checkpointHeld === held") ||
		!strings.Contains(body, "card.__checkpointHeld = held") {
		t.Error("checkpoint card cache key must include the held state so a hold re-renders")
	}
}

// submitApproval carries the optional {choice} on the SAME /artifacts/action
// approve seam, sent only when present so every existing caller is unchanged.
func TestIndexSubmitApprovalCarriesChoice(t *testing.T) {
	html := readIndexForCheckpoint(t)
	body := functionBody(html, "async function submitApproval(id, action, reason, choice)")
	if body == "" {
		t.Fatal("could not extract submitApproval body (signature must gain the choice param)")
	}
	for _, want := range []string{
		"const body = { id, action, reason }",
		"if (String(choice || '').trim()) body.choice = String(choice).trim()",
		"postAuthJSON('/artifacts/action', body)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("submitApproval missing choice-forwarding marker %q", want)
		}
	}
}

// 2. PROCESS PROGRESS — the working log adds one disclosure line per process
// stage: disclosed skips ("skipped: no brand assets"), render-export status,
// gate scores, checkpoint choices.
func TestIndexProcessStageDetailLines(t *testing.T) {
	html := readIndexForCheckpoint(t)
	body := functionBody(html, "function goalProcessStageDetail(task, plan)")
	if body == "" {
		t.Fatal("could not extract goalProcessStageDetail body")
	}
	for _, want := range []string{
		// free-form goals (no role) render nothing extra
		"const role = String(task?.role || '')",
		"if (!role) return ''",
		// disclosed skips → "skipped: <reason>"
		"const skipped = reasons.match(/skipped \\(disclosed\\):\\s*(.+)/)",
		"return `skipped: ${skipped[1]}`",
		// render stages surface the PDF export's live status off the source artifact
		"const status = String(source?.metadata?.renderStatus || '').trim()",
		"return `pdf export · ${status}`",
		// gate score + checkpoint choice
		"return `gate · scored ${score.toFixed(1)}`",
		"return reasons.replace(/^human checkpoint:\\s*/, 'choice: ')",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("goalProcessStageDetail missing marker %q", want)
		}
	}
	// the working-log loop must actually render the detail line
	update := functionBody(html, "function updateGoalCard(card, artifact)")
	for _, want := range []string{
		"const stageDetail = goalProcessStageDetail(task, plan)",
		"goalcard__work-line--detail",
	} {
		if !strings.Contains(update, want) {
			t.Errorf("updateGoalCard working log missing process-stage detail marker %q", want)
		}
	}
	// a parked checkpoint drives the live stage line (the question, not a
	// generic wait)
	if !strings.Contains(update, "const pendingCheckpoint = goalPendingCheckpoint(artifact, plan)") ||
		!strings.Contains(update, "String(pendingCheckpoint.question || '')") {
		t.Error("updateGoalCard stage line must surface the pending checkpoint question")
	}
}

// 3. PALETTE — the fifth "Processes" group renders additively: a new glyph
// keyed by the group id, the four lifecycle glyphs untouched.
func TestIndexPaletteProcessesGroupGlyph(t *testing.T) {
	html := readIndexForCheckpoint(t)
	body := functionBody(html, "function paletteGroupGlyph(group)")
	if body == "" {
		t.Fatal("could not extract paletteGroupGlyph body")
	}
	// the fifth key is present and additive
	if !strings.Contains(body, "processes: '<rect") {
		t.Error("paletteGroupGlyph missing the additive 'processes' group glyph")
	}
	// the four lifecycle groups stay in the map unchanged
	for _, want := range []string{"ideate:", "package:", "market:", "portfolio:"} {
		if !strings.Contains(body, want) {
			t.Errorf("paletteGroupGlyph lost an existing lifecycle group glyph %q", want)
		}
	}
	// the palette renders groups straight off the payload (the fifth group
	// carried by GET /assistant/tools is already iterated, not hard-coded)
	renderBody := functionBody(html, "function paletteRenderList()")
	if !strings.Contains(renderBody, "for (const group of (assistantToolsCache || []))") {
		t.Error("paletteRenderList must iterate every payload group so the fifth renders additively")
	}
}

// The checkpoint choice + brief CSS exists (the confirmation-card grammar, not
// a new card system): choice buttons, the question, the inline brief.
func TestIndexCheckpointCardStyles(t *testing.T) {
	html := readIndexForCheckpoint(t)
	for _, want := range []string{
		".goalcard__checkpoint {",
		".goalcard__checkpoint-question {",
		".goalcard__checkpoint-brief {",
		".goalcard__checkpoint-held {",
		".goalcard__choice {",
		".goalcard__work-line--detail {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing checkpoint card style %q", want)
		}
	}
}
