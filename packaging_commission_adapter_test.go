package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// The chat intake and the studio briefs speak two vocabularies. The adapter is
// the only translation seam between them, and a word it cannot translate must
// never fail the commission: packagingIntakeLaunchLocked marks a failed intake
// TERMINAL, so one unmappable word ("punchy") would lose the ask for good.
func TestPackagingBriefFromIntakeTranslatesTheIntakeVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		style     string
		wantStyle string
	}{
		{"studio word passes", "crisp", "crisp"},
		{"punchy is crisp", "punchy", "crisp"},
		{"formal is crisp", "formal", "crisp"},
		{"casual is narrative", "casual", "narrative"},
		{"data-led passes", "data-led", "data-led"},
		{"data led normalizes", "data led", "data-led"},
		{"persuasive passes", "persuasive", "persuasive"},
		{"unknown is dropped, never failed", "shouty", ""},
	} {
		brief, err := packagingBriefFromIntake(packagingIntakeKindPresentation, packagingIntakeBrief{Ask: "a deck on Q3", CopyStyle: tc.style})
		if err != nil {
			t.Fatalf("%s: copyStyle %q failed the commission: %v", tc.name, tc.style, err)
		}
		if brief.Presentation.CopyStyle != tc.wantStyle {
			t.Fatalf("%s: copyStyle %q → %q, want %q", tc.name, tc.style, brief.Presentation.CopyStyle, tc.wantStyle)
		}
	}

	// Length: the intake emits "one page", "N pages" and "N slides"; the studio
	// takes short/standard/long or a slide count.
	for _, tc := range []struct {
		length     string
		wantLength string
		wantSlides int
		wantFormat string
	}{
		{"one page", "short", 8, "one-pager"},
		{"one-pager", "short", 8, "one-pager"},
		{"short", "short", 8, ""},
		{"long", "long", 20, ""},
		{"12 slides", "12 slides", 12, ""},
		{"6 pages", "6 slides", 6, ""},
		{"memo", "short", 8, "memo"},
		{"whenever", "", 0, ""},
	} {
		deck, err := packagingBriefFromIntake(packagingIntakeKindPresentation, packagingIntakeBrief{Ask: "a deck on Q3", Length: tc.length})
		if err != nil {
			t.Fatalf("length %q failed the presentation commission: %v", tc.length, err)
		}
		if deck.Presentation.Length != tc.wantLength {
			t.Fatalf("length %q → %q, want %q", tc.length, deck.Presentation.Length, tc.wantLength)
		}
		if count, slideErr := packagingLengthSlides(deck.Presentation.Length); slideErr != nil || count != tc.wantSlides {
			t.Fatalf("length %q → %d slides (err=%v), want %d", tc.length, count, slideErr, tc.wantSlides)
		}
		story, err := packagingBriefFromIntake(packagingIntakeKindStory, packagingIntakeBrief{Ask: "a story on Q3", Length: tc.length})
		if err != nil {
			t.Fatalf("length %q failed the story commission: %v", tc.length, err)
		}
		if story.Story.Length != tc.wantLength {
			t.Fatalf("story length %q → %q, want %q", tc.length, story.Story.Length, tc.wantLength)
		}
		research, err := packagingBriefFromIntake(packagingIntakeKindResearch, packagingIntakeBrief{Ask: "what is the market?", Length: tc.length})
		if err != nil {
			t.Fatalf("length %q failed the research commission: %v", tc.length, err)
		}
		wantFormat := firstNonEmptyString(tc.wantFormat, "report")
		if research.Research.Format != wantFormat {
			t.Fatalf("length %q → research format %q, want %q", tc.length, research.Research.Format, wantFormat)
		}
	}
}

// Imagery and depth are the intake's other two off-vocabulary fields, and the
// intake writes the LITERAL "Scout's call" into every unanswered choice when
// the asker defers ("your call"). Failing on that word marks the intake
// TERMINALLY failed on that message — the exact loss the copyStyle/length
// translators exist to prevent — so both must translate or drop, never fail.
func TestPackagingBriefFromIntakeTranslatesImageryAndDepth(t *testing.T) {
	for _, tc := range []struct {
		imagery string
		want    string
	}{
		{"full-bleed", packagingImageryFullBleed},
		{"on-slide", packagingImageryOnSlide},
		{"hybrid", packagingImageryHybrid},
		{"  Hybrid  ", packagingImageryHybrid},
		// extractPackagingIntakeBrief reads "no images"/"text only" as "none".
		{"none", packagingImageryOnSlide},
		// Natural spellings: a numbered reply stores its segment raw, so the
		// words the asker actually used must name the same option.
		{"full bleed", packagingImageryFullBleed},
		{"on slide", packagingImageryOnSlide},
		// The deferral answer, and the numbered-answer path's raw free text.
		{"Scout's call", packagingImageryHybrid},
		{"mixed media", packagingImageryHybrid},
		{"", packagingImageryHybrid},
	} {
		brief, err := packagingBriefFromIntake(packagingIntakeKindPresentation, packagingIntakeBrief{Ask: "a deck on Q3", Imagery: tc.imagery})
		if err != nil {
			t.Fatalf("imagery %q failed the whole commission: %v", tc.imagery, err)
		}
		if brief.Presentation.ImageryMode != tc.want {
			t.Fatalf("imagery %q → %q, want %q", tc.imagery, brief.Presentation.ImageryMode, tc.want)
		}
	}

	for _, tc := range []struct {
		depth string
		want  string
	}{
		{"brief", "brief"},
		{"standard", "standard"},
		{"deep", "deep"},
		{" Deep ", "deep"},
		{"deep dive", "deep"},
		{"Scout's call", "standard"},
		{"you decide", "standard"},
		{"", "standard"},
	} {
		research, err := packagingBriefFromIntake(packagingIntakeKindResearch, packagingIntakeBrief{Ask: "what is the market?", Depth: tc.depth})
		if err != nil {
			t.Fatalf("depth %q failed the whole commission: %v", tc.depth, err)
		}
		if research.Research.Depth != tc.want {
			t.Fatalf("depth %q → %q, want %q", tc.depth, research.Research.Depth, tc.want)
		}
	}
}

// The loss, end to end through the intake's own writer: "your call" closes
// every open question with the literal "Scout's call", and the brief that
// produces must still build a commission for every kind the intake launches.
func TestPackagingIntakeDeferralStillBuildsACommissionBrief(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		question string
		field    func(packagingIntakeBrief) string
	}{
		{packagingIntakeKindPresentation, "imagery", func(b packagingIntakeBrief) string { return b.Imagery }},
		{packagingIntakeKindResearch, "depth", func(b packagingIntakeBrief) string { return b.Depth }},
	} {
		record := &packagingIntakeRecord{
			Kind:          tc.kind,
			OpenQuestions: []packagingIntakeQuestion{packagingIntakeQuestionCatalog[tc.question]},
			Brief:         packagingIntakeBrief{Kind: tc.kind, Ask: "make me something for the packaging pilot"},
		}
		if closed := applyBriefAnswers(record, nil, "your call"); closed != 1 {
			t.Fatalf("%s: deferral closed %d questions, want 1", tc.question, closed)
		}
		// The intake really does store an off-vocabulary word here; if it ever
		// stops, this pin should be retired rather than quietly passing.
		if got := tc.field(record.Brief); got != "Scout's call" {
			t.Fatalf("%s: deferral stored %q, want the literal \"Scout's call\"", tc.question, got)
		}
		if _, err := packagingBriefFromIntake(tc.kind, record.Brief); err != nil {
			t.Fatalf("%s: a deferred answer failed the commission (intake goes terminally failed): %v", tc.question, err)
		}
	}
}

// Everything the asker attached or named reaches the deck brief: the research
// artifact grounds it, Drive files and links ride Sources (authorized at
// launch), and anything unresolvable stays in the question text.
func TestPackagingBriefFromIntakeCarriesEveryNamedSourceOntoTheDeck(t *testing.T) {
	brief, err := packagingBriefFromIntake(packagingIntakeKindPresentation, packagingIntakeBrief{
		Ask:         "turn this into a board deck",
		Audience:    "board",
		ContextRefs: []string{"file|drive-1"},
		Sources:     []string{"os-artifact-research-9", "https://example.test/report", "file|drive-2", "the Q3 spreadsheet"},
	})
	if err != nil {
		t.Fatalf("presentation brief: %v", err)
	}
	presentation := brief.Presentation
	if presentation.Research == nil || presentation.Research.ArtifactID != "os-artifact-research-9" {
		t.Fatalf("research input=%+v", presentation.Research)
	}
	refs := make([]string, 0, len(presentation.Sources))
	for _, source := range presentation.Sources {
		refs = append(refs, firstNonEmptyString(source.Ref, source.URL, source.ArtifactID))
	}
	if strings.Join(refs, ",") != "file|drive-1,https://example.test/report,file|drive-2" {
		t.Fatalf("deck sources=%v, want the Drive files and the link", refs)
	}
	if !strings.Contains(presentation.Subject, "the Q3 spreadsheet") {
		t.Fatalf("an unresolvable named source was dropped: %q", presentation.Subject)
	}
	// The objective the engine reads names them, so the deck is built from the
	// attached material instead of invented numbers.
	objective := packagingPresentationObjective(*presentation, "", "")
	for _, want := range []string{"Drive file drive-1", "Drive file drive-2", "https://example.test/report"} {
		if !strings.Contains(objective, want) {
			t.Fatalf("objective missing %q:\n%s", want, objective)
		}
	}
}

// The real adapter, end to end: a chat ask with an attached Drive file becomes
// a commission whose goal carries that file as a contextRef, and whose root is
// bound back to the intake record that asked for it.
func TestPackagingCommissionAdapterLaunchesWithAttachedDriveFileAndIntakeBinding(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	mine := uploadDriveFileRow(t, cookies, "q3-results.csv", "text/csv", []byte("quarter,revenue\nQ3,10"), nil)
	thread, err := kanbanApp.createScoutChatThread(aj.Email, aj.Name, "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	adapter := packagingCommissionLauncherAdapter{app: kanbanApp}
	intake := packagingIntakeBrief{
		Kind: packagingIntakeKindPresentation, Ask: "build a board deck from this", Audience: "board",
		CopyStyle: "punchy", Length: "one page", Imagery: "hybrid",
		ThreadID: thread.ID, MessageID: "scout-chat-message-adapter-1",
		ContextRefs: []string{assistantFileContextRef(mine.ID)},
	}
	receipt, err := adapter.createPackagingCommission(aj, packagingIntakeKindPresentation, intake)
	if err != nil {
		t.Fatalf("adapter launch: %v", err)
	}
	root, ok := kanbanApp.osArtifactByID(receipt.CommissionID)
	if !ok {
		t.Fatalf("no commission root for %q", receipt.CommissionID)
	}
	refs := decodeAssistantContextRefs(root.Metadata["contextRefs"])
	if len(refs) != 1 || refs[0] != assistantFileContextRef(mine.ID) {
		t.Fatalf("launched contextRefs=%v, want the attached Drive file", refs)
	}
	stored := decodePackagingBriefMetadata(root.Metadata)
	if stored == nil || stored.Presentation == nil || stored.Presentation.CopyStyle != "crisp" || stored.Presentation.Length != "short" || len(stored.Presentation.Sources) != 1 {
		t.Fatalf("stored deck brief=%+v", stored)
	}
	if want := packagingIntakeRecordID(thread.ID, intake.MessageID); root.Metadata[packagingCommissionIntakeIDMetadataKey] != want {
		t.Fatalf("intake binding=%q, want %q", root.Metadata[packagingCommissionIntakeIDMetadataKey], want)
	}
	// A Drive file another member cannot read refuses the whole commission.
	joelCookies := loginAs(t, "joel@shareability.com", "B0NFIRE!")
	secret := uploadDriveFileRow(t, joelCookies, "secret.csv", "text/csv", []byte("private"), map[string]string{"visibility": "private"})
	intake.MessageID = "scout-chat-message-adapter-2"
	intake.ContextRefs = []string{assistantFileContextRef(secret.ID)}
	if _, err := adapter.createPackagingCommission(aj, packagingIntakeKindPresentation, intake); err == nil {
		t.Fatal("an unreadable Drive source launched a deck")
	}
}

// The whole chat spine: a private ask with no gaps launches a real commission
// through the real adapter, and the commission root stays bound to its intake
// record even while the record's own post-launch write has not landed (the
// launcher runs BEFORE that write, which is best-effort).
func TestPackagingChatAskLaunchesCommissionBoundToItsIntakeRecord(t *testing.T) {
	_, aj := setupPackagingStudioTest(t)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		return "", fmt.Errorf("no provider in this test (workflow=%s)", request.Workflow)
	})
	thread, err := kanbanApp.createScoutChatThread(aj.Email, aj.Name, "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ask := "make me a deck for the board, punchy copy and hybrid imagery, on the packaging pilot"
	if kind, isAsk := packagingIntakeDetect(ask, nil, nil); !isAsk || kind != packagingIntakeKindPresentation {
		t.Skipf("intake detection contract changed (%q → %q/%v); this pin needs a new gap-free ask", ask, kind, isAsk)
	}
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), aj, thread.ID, ask, nil, ""); err != nil {
		t.Fatalf("append ask: %v", err)
	}
	records := kanbanApp.packagingIntakeRecordsForThread(thread.ID)
	if len(records) != 1 {
		t.Fatalf("intake records=%+v, want one", records)
	}
	record := records[0]
	if record.Status != packagingIntakeStatusLaunched || record.CommissionID == "" {
		t.Fatalf("gap-free ask did not launch: %+v (error=%q)", record.Status, record.Error)
	}
	root, ok := kanbanApp.osArtifactByID(record.CommissionID)
	if !ok {
		t.Fatalf("no commission root for %q", record.CommissionID)
	}
	if root.Metadata[packagingCommissionIntakeIDMetadataKey] != record.ID {
		t.Fatalf("root intake binding=%q, want %q", root.Metadata[packagingCommissionIntakeIDMetadataKey], record.ID)
	}
	// "punchy" is intake vocabulary; the studio only knows four styles. It must
	// be translated, not refused — a refusal marks the intake terminally failed.
	stored := decodePackagingBriefMetadata(root.Metadata)
	if stored == nil || stored.Presentation == nil || stored.Presentation.CopyStyle != "crisp" || stored.Presentation.ImageryMode != packagingImageryHybrid {
		t.Fatalf("stored brief=%+v", stored)
	}
	// The launch window: the launcher already stamped the root while the
	// record is still persisted as waiting with no commissionId of its own.
	// The root's own stamp is what keeps the hub row bound.
	pending := record
	pending.Status = packagingIntakeStatusWaiting
	pending.CommissionID = ""
	pending.WaitingOn, pending.WaitingOnName = aj.Email, aj.Name
	pending.OpenQuestions = []packagingIntakeQuestion{{ID: "audience", Prompt: "Who is this for?", Kind: "text"}}
	if err := kanbanApp.savePackagingIntakeRecord(pending); err != nil {
		t.Fatal(err)
	}
	state := kanbanApp.packagingCommissionWaitingStateFor(context.Background(), aj, root)
	if state.IntakeID != record.ID || state.BriefComplete || state.WaitingOn != aj.Email || len(state.Questions) != 1 {
		t.Fatalf("waiting state=%+v, want the bound pending intake", state)
	}
	// Still viewer-fenced: a member who cannot read the private thread sees none of it.
	joel := accountStore().findUser("joel@shareability.com")
	if fenced := kanbanApp.packagingCommissionWaitingStateFor(context.Background(), joel, root); fenced.IntakeID != "" || !fenced.BriefComplete {
		t.Fatalf("waiting state leaked across the thread fence: %+v", fenced)
	}
}

// The story door of the adapter mints the outline through the real Story
// Studio path and hands back the outline's own thread.
func TestPackagingCommissionAdapterStoryDoorBindsOutlineToItsThread(t *testing.T) {
	_, aj := setupPackagingStudioTest(t)
	kanbanApp.apiKey = ""
	adapter := packagingCommissionLauncherAdapter{app: kanbanApp}
	intake := packagingIntakeBrief{
		Kind: packagingIntakeKindStory, Ask: "workshop the Series B story", Audience: "investors", Length: "one page",
		ThreadID: "", MessageID: "scout-chat-message-story-adapter-1",
	}
	receipt, err := adapter.createPackagingCommission(aj, packagingIntakeKindStory, intake)
	if err != nil {
		t.Fatalf("story adapter launch: %v", err)
	}
	entry, ok := kanbanApp.osArtifactByID(receipt.CommissionID)
	if !ok || !packagingStoryOutlineArtifact(entry) {
		t.Fatalf("story outline missing for %q", receipt.CommissionID)
	}
	if receipt.Thread == nil || entry.Metadata[packagingStoryThreadIDMetadataKey] != receipt.Thread.ID {
		t.Fatalf("story receipt thread=%+v metadata=%q", receipt.Thread, entry.Metadata[packagingStoryThreadIDMetadataKey])
	}
	if want := packagingIntakeRecordID("", intake.MessageID); entry.Metadata[packagingCommissionIntakeIDMetadataKey] != want {
		t.Fatalf("story intake binding=%q, want %q", entry.Metadata[packagingCommissionIntakeIDMetadataKey], want)
	}
	// The intake's length vocabulary reached the scaffold as a real length.
	view := packagingStoryViewFromEntry(entry)
	if view.Brief == nil || view.Brief.Length != "short" || view.Doc == nil || len(view.Doc.Beats) != 8 {
		t.Fatalf("story brief=%+v doc=%+v", view.Brief, view.Doc)
	}
	// A second delivery of the same ask adopts the same outline.
	again, err := adapter.createPackagingCommission(aj, packagingIntakeKindStory, intake)
	if err != nil || again.CommissionID != receipt.CommissionID {
		t.Fatalf("redelivered story ask=%+v err=%v", again, err)
	}
}

// A numbered reply ("1. full bleed") stores its segment RAW for a choice
// question — applyBriefAnswers' numbered branch does not canonicalize against
// the question's options the way the bare-list branch does — so the
// translators must recognize the option the asker actually named. The same
// words typed as free text already close these questions canonically, and a
// silent fall-through to the default builds the deck nobody asked for: one
// full-bleed crescendo when the asker said full-bleed everywhere.
func TestPackagingBriefFromIntakeReadsNaturallySpelledNumberedAnswers(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		question string
		reply    string
		stored   string
		field    func(packagingIntakeBrief) string
		got      func(packagingBrief) string
		want     string
	}{
		{packagingIntakeKindPresentation, "imagery", "1. full bleed", "full bleed",
			func(b packagingIntakeBrief) string { return b.Imagery },
			func(b packagingBrief) string { return b.Presentation.ImageryMode }, packagingImageryFullBleed},
		{packagingIntakeKindPresentation, "imagery", "1. on slide", "on slide",
			func(b packagingIntakeBrief) string { return b.Imagery },
			func(b packagingBrief) string { return b.Presentation.ImageryMode }, packagingImageryOnSlide},
		{packagingIntakeKindResearch, "depth", "1. deep dive", "deep dive",
			func(b packagingIntakeBrief) string { return b.Depth },
			func(b packagingBrief) string { return b.Research.Depth }, "deep"},
	} {
		record := &packagingIntakeRecord{
			Kind:          tc.kind,
			OpenQuestions: []packagingIntakeQuestion{packagingIntakeQuestionCatalog[tc.question]},
			Brief:         packagingIntakeBrief{Kind: tc.kind, Ask: "make me something for the packaging pilot"},
		}
		if closed := applyBriefAnswers(record, nil, tc.reply); closed != 1 {
			t.Fatalf("%s: %q closed %d questions, want 1", tc.question, tc.reply, closed)
		}
		// The raw store is the intake's behaviour, not the adapter's; if it
		// ever canonicalizes, retire this pin rather than let it pass quietly.
		if got := tc.field(record.Brief); got != tc.stored {
			t.Fatalf("%s: %q stored %q, want the raw segment %q", tc.question, tc.reply, got, tc.stored)
		}
		brief, err := packagingBriefFromIntake(tc.kind, record.Brief)
		if err != nil {
			t.Fatalf("%s: %q failed the commission: %v", tc.question, tc.reply, err)
		}
		if got := tc.got(brief); got != tc.want {
			t.Fatalf("%s: %q → %q, want %q (the option the asker named)", tc.question, tc.reply, got, tc.want)
		}
	}
}
