package main

import (
	"strings"
	"testing"
)

// Wave 2 — UI polish pass (docs/superpowers/specs/
// 2026-07-06-bonfire-topline-design.md). The founder asked for a feed that is
// slick, uncrowded, and free of odd wraps: cards share one spine, the run card
// is de-chromed, the small type step exists, and deliverable titles wrap
// instead of hard-truncating. Grep-pinned against index.html.

func TestPolishSmallLabelStepDefined(t *testing.T) {
	html := readIndexForComposerPolish(t)
	// The run card references var(--type-label-sm, …); if the token is missing
	// every caption silently renders at --type-label-lg and the hierarchy flattens.
	if !strings.Contains(html, "--type-label-sm:") {
		t.Error("--type-label-sm is undefined — run-card group/meta/resolved lines lose their step down")
	}
}

func TestPolishRunCardOnFeedSpine(t *testing.T) {
	html := readIndexForComposerPolish(t)
	rule := cssRuleBody(html, ".scout-proposal-card")
	if rule == "" {
		t.Fatal(".scout-proposal-card rule missing")
	}
	if strings.Contains(rule, "align-self: stretch") {
		t.Error(".scout-proposal-card still stretches the full lane — it must center on --feed-measure like the goalcard")
	}
	if !strings.Contains(rule, "align-self: center") || !strings.Contains(rule, "var(--feed-measure)") {
		t.Error(".scout-proposal-card must center at width min(var(--feed-measure), 100%) so all feed cards share one spine")
	}
}

func TestPolishManifestCardCentered(t *testing.T) {
	html := readIndexForComposerPolish(t)
	rule := cssRuleBody(html, ".manifest-card")
	if rule == "" {
		t.Fatal(".manifest-card rule missing")
	}
	if !strings.Contains(rule, "align-self: center") {
		t.Error(".manifest-card must align-self: center — it otherwise left-aligns off the feed spine")
	}
}

func TestPolishRunCardMetaDeChromed(t *testing.T) {
	html := readIndexForComposerPolish(t)
	// The authority + weight lines must not carry the bordered-pill treatment
	// that competed with the primary Run button.
	rule := cssRuleBody(html, ".scout-proposal-card__weight")
	if strings.Contains(rule, "border: 1px solid var(--line-1)") || strings.Contains(rule, "background: var(--surface-1)") {
		t.Error("authority/weight still rendered as bordered pills — de-chrome them to a quiet caption line")
	}
}

func TestPolishManifestTitleWraps(t *testing.T) {
	html := readIndexForComposerPolish(t)
	rule := cssRuleBody(html, ".manifest-card__row-title")
	if rule == "" {
		t.Fatal(".manifest-card__row-title rule missing")
	}
	if strings.Contains(rule, "white-space: nowrap") {
		t.Error("deliverable titles still hard-truncate on one line — they must wrap (line-clamp) so real names are visible")
	}
	if !strings.Contains(rule, "-webkit-line-clamp: 2") {
		t.Error(".manifest-card__row-title must clamp to 2 lines, not ellipsis a single line")
	}
}

func TestPolishCheckpointNotesLabelHumanized(t *testing.T) {
	html := readIndexForComposerPolish(t)
	if strings.Contains(html, "notes for the next stage (do_not_touch, answers, must-keeps)") {
		t.Error("the 60-char jargon notes label still leaks internal syntax — shorten to 'notes for the next stage'")
	}
}

func TestPolishRunCardFieldsNoIOSZoom(t *testing.T) {
	html := readIndexForComposerPolish(t)
	// The coarse-pointer 16px block must include .palette__field so tapping a
	// run-card field never zooms the viewport on iOS Safari.
	i := strings.Index(html, ".scout-chat-input,\n        .palette__field,")
	if i < 0 {
		t.Error(".palette__field is not in the coarse-pointer 16px block — run-card fields will zoom on iOS tap")
	}
}

// Wave 4 — task discovery. The empty private thread must seed tappable run
// starters (the visible signpost the gesture-gated palette lacked), the
// composer must hint the launcher, and the '+' door must be labeled.

func TestDiscoveryStartersSeeded(t *testing.T) {
	html := readIndexForComposerPolish(t)
	if !strings.Contains(html, "function buildScoutStarterRow(") {
		t.Fatal("buildScoutStarterRow missing — the empty thread has no discovery starters")
	}
	if !strings.Contains(html, ".scout-starters {") {
		t.Error(".scout-starters CSS missing — starter chips have no styling")
	}
	empty := functionBody(html, "function ensureScoutChatEmptyState(")
	if empty == "" {
		t.Fatal("ensureScoutChatEmptyState not found")
	}
	if !strings.Contains(empty, "buildScoutStarterRow()") {
		t.Error("ensureScoutChatEmptyState must seed starters on the private empty state")
	}
	for _, want := range []string{"What do you want to accomplish?", "Scout can answer, start private work, or ask for approval when it actually matters."} {
		if !strings.Contains(empty, want) {
			t.Errorf("private empty state missing conversation-first copy %q", want)
		}
	}
	// The starters must lifecycle with the empty state (torn down together when
	// a message arrives), not linger orphaned.
	if !strings.Contains(html, "'.scout-chat-empty, .scout-starters'") {
		t.Error("starters are not removed alongside .scout-chat-empty — they will orphan after the first message")
	}
	// Starters place ordinary recurring requests in the composer; they never
	// select a capability or expose internal process names.
	row := functionBody(html, "function buildScoutStarterRow(")
	for _, want := range []string{"Create a polished 10-slide pitch deck", "Research ", "Build a financial model", "Design ", "scout-starter__label", "scout-starter__example", "scoutChatInput.value = s.prompt"} {
		if !strings.Contains(row, want) {
			t.Errorf("buildScoutStarterRow missing %q", want)
		}
	}
	for _, forbidden := range []string{"packaging studio", "openToolPalette(", "Browse all tasks"} {
		if strings.Contains(row, forbidden) {
			t.Errorf("buildScoutStarterRow exposes internal tool choice %q", forbidden)
		}
	}
}

func TestDiscoveryComposerInvitesNaturalLanguage(t *testing.T) {
	html := readIndexForComposerPolish(t)
	if !strings.Contains(html, "`ask ${directAgent || 'Scout'} anything`") {
		t.Error("composer placeholder must invite ordinary conversation and work")
	}
	if strings.Contains(html, `aria-label="Run a task or tool"`) {
		t.Error("normal composer exposes a tool picker")
	}
}

func TestPD1PresentationRejectionFixtureKeepsOneActivityStateOutOfConversation(t *testing.T) {
	html := readIndexForComposerPolish(t)
	fixture := functionBody(html, "function pd1PresentationWorkFixtureThread(")
	for _, want := range []string{
		"pd1PresentationFixture", "Can you make a 10 slide deck pitching me this platform?",
		"Create a polished 10-slide pitch deck for the STRIDE platform", "I’m building your presentation now. I’ll post the finished file here.",
		"intentOutcome: 'start_private_work'", "kind: 'thread'", "mode: 'goal'",
	} {
		if !strings.Contains(fixture, want) {
			t.Errorf("PD1 rendered presentation fixture missing %q", want)
		}
	}
	for _, forbidden := range []string{"Packaging Studio", "staged process", "human checkpoint", "toolTemplate"} {
		if strings.Contains(fixture, forbidden) {
			t.Errorf("PD1 rendered presentation fixture leaks internal process copy %q", forbidden)
		}
	}
	if strings.Count(fixture, "kind: 'thread'") != 1 {
		t.Fatalf("PD1 presentation fixture must contain one durable work activity ref, got %d", strings.Count(fixture, "kind: 'thread'"))
	}
	projection := functionBody(html, "function scoutChatRecordBelongsInTimeline(message)")
	if !strings.Contains(projection, "projection.richResult || projection.checkpoint") || !strings.Contains(projection, "scoutThreadTimelineProjection(resultMessage).richResult") {
		t.Fatal("generic work lifecycle must stay out of the conversation while rich results and decisions remain")
	}
	for _, name := range []string{"function syncDesktopActiveWorkIndicator()", "function renderDesktopWorkContext(message, artifact)"} {
		if !strings.Contains(html, name) {
			t.Errorf("presentation activity sidecar contract missing %q", name)
		}
	}
}

func TestPD1RecurringWorkUsesOnePremiumPresentationGrammar(t *testing.T) {
	html := readIndexForComposerPolish(t)
	family := functionBody(html, "function desktopWorkFamily(")
	for _, want := range []string{"Financial model", "Presentation", "Design", "Research", "Document", "Meeting recap", "Revision", "Scheduled work", "Build", "Data visualization", "Project plan"} {
		if !strings.Contains(family, want) {
			t.Errorf("desktop work family grammar missing %q", want)
		}
	}
	phase := functionBody(html, "function desktopWorkPhase(")
	if !strings.Contains(phase, "build|draft|synth|execute|codex|assembl|compos|prepar") {
		t.Fatal("desktop work phase grammar must map assemble/compose/prepare stages to Building like native")
	}
	for _, want := range []string{"ref?.currentStage", "'completed'", "'needs_attention'"} {
		if !strings.Contains(phase, want) {
			t.Errorf("desktop work phase grammar is not aligned with the thread/native projection: missing %q", want)
		}
	}
	threadProjection := functionBody(html, "function scoutThreadFromChatMessage(")
	for _, want := range []string{"governedWorkThread", "start_private_work", "projectedArtifactId"} {
		if !strings.Contains(threadProjection, want) {
			t.Errorf("conversation-owned work without a material artifact cannot reach the premium card: missing %q", want)
		}
	}
	threadRenderer := functionBody(html, "function scoutChatMessageRecordNode(")
	if !strings.Contains(threadRenderer, "String(message.intentOutcome || '') === 'start_private_work'") {
		t.Fatal("desktop must route every server-classified private work thread through the premium family card")
	}
	premiumCard := functionBody(html, "function scoutGoalRefRecordNode(")
	for _, want := range []string{
		"desktopWorkCardIdentity(message?.thread, artifact)",
		"desktopWorkCardIdentity(candidate?.thread, null)",
		"Artifact identity remains for Open/Save only",
	} {
		if !strings.Contains(premiumCard, want) {
			t.Errorf("artifact attachment can split one evolving work card: missing %q", want)
		}
	}
	if strings.Contains(premiumCard, "candidate?.thread?.artifactId || (String(candidate?.intentOutcome") {
		t.Fatal("artifactless conversation work must still collapse repeated projections to one evolving premium card")
	}
	identity := functionBody(html, "function desktopWorkCardIdentity(")
	if !strings.Contains(identity, "ref?.id || ref?.artifactId || artifact?.id") {
		t.Fatal("work-card identity must prefer immutable run identity before the attached material artifact")
	}
	safeNote := functionBody(html, "function desktopSafeWorkNote(")
	for _, approved := range []string{"shaping the deck brief", "gathering reliable sources", "building the first draft", "checking the work", "preparing your deliverable", "waiting for your input", "ready for review", "saving the final deliverable", "drafting the document", "turning the meeting into decisions", "preparing the revision", "setting the schedule", "preparing the handoff", "assembling the package", "building the visualization", "mapping the plan"} {
		if !strings.Contains(safeNote, approved) {
			t.Errorf("desktop progress sanitizer does not admit reviewed copy %q", approved)
		}
	}
	if !strings.Contains(safeNote, "approvedNotes[value.toLowerCase()] || fallback") {
		t.Fatal("desktop progress sanitizer must fail closed to the stable phase for every unreviewed note")
	}
	if strings.Contains(safeNote, "return exposesRuntime ? fallback : value") {
		t.Fatal("desktop progress sanitizer still relies on a bypassable runtime-token denylist")
	}
	context := functionBody(html, "function renderDesktopWorkContext(")
	for _, want := range []string{"desktopWorkFamily", "desktopSafeWorkNote"} {
		if !strings.Contains(context, want) {
			t.Errorf("desktop work inspector missing %q", want)
		}
	}
	for _, forbidden := range []string{"orchestratorModel", "reasoningEffort", "`run ${ref.id", "artifact ${ref.artifactId", "stage ${artifact?.metadata?.currentStage"} {
		if strings.Contains(context, forbidden) {
			t.Errorf("desktop work inspector exposes internal runtime marker %q", forbidden)
		}
	}
}

func TestPD1RecurringWorkAccessibilityAndLiveFocusContract(t *testing.T) {
	html := readIndexForComposerPolish(t)
	renderer := functionBody(html, "function scoutDesktopGoalWorkCardNode(")
	for _, want := range []string{
		"aria-live", "aria-atomic",
		"View ${family} activity, ${phase.label}", "focus-visible",
	} {
		if !strings.Contains(renderer, want) && !strings.Contains(html, want) {
			t.Errorf("desktop work card accessibility contract missing %q", want)
		}
	}
	focusSnapshot := functionBody(html, "function scoutWorkCardFocusSnapshot(")
	for _, want := range []string{"document.activeElement", ".scout-chat-work-card__open", "workArtifactId"} {
		if !strings.Contains(focusSnapshot, want) {
			t.Errorf("work-card focus snapshot missing %q", want)
		}
	}
	focusRestore := functionBody(html, "function restoreScoutWorkCardFocus(")
	for _, want := range []string{"data-work-artifact-id", ".scout-chat-work-card__open", "preventScroll: true"} {
		if !strings.Contains(focusRestore, want) {
			t.Errorf("work-card live-rerender focus restore missing %q", want)
		}
	}
	activeRender := functionBodyAfterSignature(html, "function renderActiveScoutThread(options = {})")
	for _, want := range []string{"scoutWorkCardFocusSnapshot()", "restoreScoutWorkCardFocus(focusedWorkCardId)"} {
		if !strings.Contains(activeRender, want) {
			t.Errorf("active thread render does not preserve focused work control: missing %q", want)
		}
	}
	if !strings.Contains(html, "event.key === 'Escape' && !chatContextRail?.hidden") || !strings.Contains(html, "closeDesktopChatContext()") {
		t.Fatal("desktop work inspector must close on Escape and use the existing focus-return contract")
	}
}

func TestOrganizationSwitcherKeepsClosedStateQuietAndMovesChoicesIntoMenu(t *testing.T) {
	html := readIndexForComposerPolish(t)
	body := functionBody(html, "function renderOrganizationSwitcher(")
	for _, want := range []string{"organizationProjectionAvailable", "const currentLabel", "organizationName.textContent = currentLabel", "organizationMenuItems.replaceChildren()", "role', 'menuitemradio'"} {
		if !strings.Contains(body, want) {
			t.Errorf("organization chooser contract missing %q", want)
		}
	}
	switcher := pd1Slice(t, html, `<button id="topbarOrganizationSwitcher"`, `</button>`)
	for _, forbidden := range []string{"topbarOrganizationRole", "topbarOrganizationCount", "topbarOrganizationPending", "organization-label", "organization-meta"} {
		if strings.Contains(switcher, forbidden) {
			t.Errorf("closed organization chooser still exposes dense metadata %q", forbidden)
		}
	}
	if !strings.Contains(switcher, `id="topbarOrganizationName"`) || !strings.Contains(switcher, `aria-haspopup="menu"`) {
		t.Fatal("closed organization chooser must expose only the current name and menu disclosure")
	}
	if !strings.Contains(html, `id="topbarOrganizationCreate"`) || !strings.Contains(html, `Create organization`) {
		t.Fatal("organization menu must expose the create-organization action")
	}
}
