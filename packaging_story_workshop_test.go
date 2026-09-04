package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// storyResponderForTest swaps the provider responder for a fake that answers
// the Story Studio workflows with canned JSON and records what it was asked.
func storyResponderForTest(t *testing.T, draft func(request openAITextRequest) string, revise func(request openAITextRequest) string) *[]openAITextRequest {
	t.Helper()
	previous := createOpenAITextResponse
	calls := &[]openAITextRequest{}
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		*calls = append(*calls, request)
		switch request.Workflow {
		case packagingStoryWorkflowDraft:
			if draft == nil {
				return "", fmt.Errorf("draft responder not configured")
			}
			output := draft(request)
			if request.ValidateOutput != nil {
				if err := request.ValidateOutput(output); err != nil {
					return "", err
				}
			}
			return output, nil
		case packagingStoryWorkflowRevise:
			if revise == nil {
				return "", fmt.Errorf("revise responder not configured")
			}
			output := revise(request)
			if request.ValidateOutput != nil {
				if err := request.ValidateOutput(output); err != nil {
					return "", err
				}
			}
			return output, nil
		}
		return "", fmt.Errorf("unexpected workflow %q", request.Workflow)
	}
	t.Cleanup(func() { createOpenAITextResponse = previous })
	return calls
}

func storyDraftJSONForTest(t *testing.T, beats int) string {
	t.Helper()
	doc := packagingStoryDoc{Title: "Why now for compute credits", Thesis: "Credits beat discounts because they compound loyalty.", Audience: "CFOs"}
	for index := 0; index < beats; index++ {
		doc.Beats = append(doc.Beats, packagingStoryBeat{
			ID: fmt.Sprintf("b%d", index+1), Headline: fmt.Sprintf("Beat %d headline", index+1), Intent: fmt.Sprintf("Make them feel beat %d.", index+1),
			EvidenceNeeds: []string{fmt.Sprintf("fact %d", index+1)},
		})
	}
	doc.OpenQuestions = []string{"Which single decision should the deck end on?"}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func createStoryForTest(t *testing.T, cookies []*http.Cookie, body string) packagingStoryView {
	t.Helper()
	create := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packaging/stories", body, cookies, packagingStoriesHandler)
	if create.Code != http.StatusCreated {
		t.Fatalf("story create status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		Story packagingStoryView `json:"story"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created.Story
}

func TestStoryStudioFirstDraftComesFromTheModelInTheNarrativeContract(t *testing.T) {
	cookies, _ := setupPackagingStudioTest(t)
	calls := storyResponderForTest(t, func(openAITextRequest) string { return storyDraftJSONForTest(t, 9) }, nil)
	story := createStoryForTest(t, cookies, `{"brief":{"subject":"Why now for compute credits","audience":"CFOs","thesis":"Credits beat discounts","length":"standard"}}`)
	if len(*calls) != 1 || (*calls)[0].Seat != seatChat || (*calls)[0].JSONSchema == nil || (*calls)[0].JSONSchema.Name != packagingStoryOutlineContract {
		t.Fatalf("draft call=%+v draftedBy=%q", *calls, story.DraftedBy)
	}
	if !strings.Contains((*calls)[0].Input, "Subject: Why now for compute credits") || !strings.Contains((*calls)[0].Input, "Audience: CFOs") || !strings.Contains((*calls)[0].Input, "12 slides") {
		t.Fatalf("draft input=%q", (*calls)[0].Input)
	}
	if !strings.HasPrefix(story.DraftedBy, "model:") || story.Version != 1 || story.Doc == nil || len(story.Doc.Beats) != 9 || story.Doc.Thesis != "Credits beat discounts because they compound loyalty." || len(story.Doc.OpenQuestions) != 1 {
		t.Fatalf("story=%+v", story)
	}
	for _, want := range []string{"# Why now for compute credits", "**Audience:** CFOs", "**Thesis:** Credits beat discounts because they compound loyalty.", "## Beats", "1. **Beat 1 headline** — Make them feel beat 1.", "- Evidence: fact 1", "9. **Beat 9 headline**", "## Open questions", "- Which single decision should the deck end on?"} {
		if !strings.Contains(story.Outline, want) {
			t.Fatalf("outline missing %q:\n%s", want, story.Outline)
		}
	}
	// The rendered outline parses back to the same structure (hand edits and
	// model revisions share one shape).
	if parsed := parsePackagingStoryOutline(story.Outline, nil); len(parsed.Beats) != 9 || parsed.Beats[0].Headline != "Beat 1 headline" || parsed.Beats[0].Intent != "Make them feel beat 1." || parsed.Beats[0].EvidenceNeeds[0] != "fact 1" || parsed.Thesis != story.Doc.Thesis {
		t.Fatalf("parsed=%+v", parsed)
	}
	// A model draft outside the 8–12 beat contract is rejected → scaffold.
	storyResponderForTest(t, func(openAITextRequest) string { return storyDraftJSONForTest(t, 3) }, nil)
	thin := createStoryForTest(t, cookies, `{"brief":{"subject":"Thin story"}}`)
	if !strings.HasPrefix(thin.DraftedBy, packagingStoryDraftedByScaffold) || thin.Doc == nil || len(thin.Doc.Beats) < packagingStoryMinBeats {
		t.Fatalf("thin draft=%+v", thin)
	}
}

func TestStoryStudioScaffoldFallbackStampsDraftedByWhenKeyless(t *testing.T) {
	cookies, _ := setupPackagingStudioTest(t)
	kanbanApp.apiKey = ""
	calls := storyResponderForTest(t, nil, nil)
	for length, wantBeats := range map[string]int{"short": 8, "standard": 10, "long": 12} {
		story := createStoryForTest(t, cookies, fmt.Sprintf(`{"brief":{"subject":"Keyless %s","thesis":"t","length":%q}}`, length, length))
		if story.DraftedBy != packagingStoryDraftedByScaffold || story.Doc == nil || len(story.Doc.Beats) != wantBeats || story.Doc.Thesis != "t" {
			t.Fatalf("%s scaffold story=%+v", length, story)
		}
		if err := packagingStoryDocValid(*story.Doc); err != nil {
			t.Fatalf("%s scaffold outside the contract: %v", length, err)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("keyless server called the provider %d times", len(*calls))
	}
	entry, _ := kanbanApp.osArtifactByID(createStoryForTest(t, cookies, `{"brief":{"subject":"Stamp check"}}`).ID)
	if entry.Metadata[packagingStoryDraftedByMetadataKey] != packagingStoryDraftedByScaffold {
		t.Fatalf("draftedBy metadata=%q", entry.Metadata[packagingStoryDraftedByMetadataKey])
	}
	// A keyless workshop turn keeps the outline and says so; never a question.
	story := createStoryForTest(t, cookies, `{"brief":{"subject":"Keyless turn"}}`)
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/packaging/stories/"+story.ID, `{"message":"move the ask earlier","expectedVersion":1}`, cookies, packagingStoryHandler)
	var keyless struct {
		Answer scoutChatMessageRecord `json:"answer"`
		Story  packagingStoryView     `json:"story"`
	}
	if err := json.Unmarshal(patch.Body.Bytes(), &keyless); err != nil {
		t.Fatal(err)
	}
	if patch.Code != http.StatusOK || !strings.Contains(keyless.Answer.Text, "Kept the outline as it is") || strings.Contains(keyless.Answer.Text, "?") || keyless.Story.Version != 1 {
		t.Fatalf("keyless turn status=%d answer=%+v version=%d", patch.Code, keyless.Answer, keyless.Story.Version)
	}
}

func TestStoryStudioWorkshopTurnKeepsUnchallengedBeatsAndRepliesWithADiff(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	storyResponderForTest(t, func(openAITextRequest) string { return storyDraftJSONForTest(t, 9) }, func(request openAITextRequest) string {
		if !strings.Contains(request.Input, "Workshop message:\nput the risk before the ask and cut beat 7") || !strings.Contains(request.Input, `"id":"b1"`) {
			t.Fatalf("revision input=%q", request.Input)
		}
		var prior packagingStoryDoc
		_ = json.Unmarshal([]byte(storyDraftJSONForTest(t, 9)), &prior)
		revision := packagingStoryRevision{packagingStoryDoc: prior, ChangedBeatIDs: []string{"b3", "b7", "b8", "b9", "risk"}, Summary: "Moved the risk beat before the ask; cut beat 7."}
		// b1 is rewritten WITHOUT being declared changed → must be restored.
		revision.Beats[0].Headline = "Sneaky rewrite"
		// b3 is a declared revision, b7 is cut, a risk beat lands before the ask (b9), b8/b9 swap.
		revision.Beats[2].Intent = "Sharper intent for beat 3."
		beats := []packagingStoryBeat{revision.Beats[0], revision.Beats[1], revision.Beats[2], revision.Beats[3], revision.Beats[4], revision.Beats[5], revision.Beats[7]}
		beats = append(beats, packagingStoryBeat{ID: "risk", Headline: "The risk of waiting", Intent: "Name what it costs to do nothing.", EvidenceNeeds: []string{"a dated cost figure"}}, revision.Beats[8])
		revision.Beats = beats
		raw, _ := json.Marshal(revision)
		return string(raw)
	})
	story := createStoryForTest(t, cookies, `{"brief":{"subject":"Why now for compute credits","audience":"CFOs"}}`)
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/packaging/stories/"+story.ID, `{"message":"put the risk before the ask and cut beat 7","expectedVersion":1}`, cookies, packagingStoryHandler)
	if patch.Code != http.StatusOK {
		t.Fatalf("workshop turn status=%d body=%s", patch.Code, patch.Body.String())
	}
	var turn struct {
		Story  packagingStoryView     `json:"story"`
		Answer scoutChatMessageRecord `json:"answer"`
		Thread scoutChatThreadRecord  `json:"thread"`
	}
	if err := json.Unmarshal(patch.Body.Bytes(), &turn); err != nil {
		t.Fatal(err)
	}
	revised := turn.Story
	if revised.Version != 2 || revised.Doc == nil || len(revised.Doc.Beats) != 9 || !strings.HasPrefix(revised.DraftedBy, "model:") {
		t.Fatalf("revised story=%+v", revised)
	}
	ids := make([]string, 0, len(revised.Doc.Beats))
	for _, beat := range revised.Doc.Beats {
		ids = append(ids, beat.ID)
	}
	if strings.Join(ids, ",") != "b1,b2,b3,b4,b5,b6,b8,risk,b9" {
		t.Fatalf("beat order=%v", ids)
	}
	if revised.Doc.Beats[0].Headline != "Beat 1 headline" {
		t.Fatalf("unchallenged beat 1 was rewritten: %+v", revised.Doc.Beats[0])
	}
	if revised.Doc.Beats[2].Intent != "Sharper intent for beat 3." || revised.Doc.Beats[7].Headline != "The risk of waiting" {
		t.Fatalf("declared changes lost: %+v", revised.Doc.Beats)
	}
	if !strings.Contains(revised.Outline, "7. **Beat 8 headline**") || strings.Contains(revised.Outline, "Beat 7 headline") || strings.Contains(revised.Outline, "Sneaky") {
		t.Fatalf("revised outline=%s", revised.Outline)
	}
	if turn.Answer.Role != "scout" || !strings.Contains(turn.Answer.Text, "Moved the risk beat before the ask; cut beat 7.") || !strings.Contains(turn.Answer.Text, "(outline v2)") || strings.Contains(turn.Answer.Text, "?") {
		t.Fatalf("scout reply=%+v", turn.Answer)
	}
	// The turn is on the bound thread: brief, user message, Scout's diff.
	thread, _, err := kanbanApp.scoutChatThreadByID(aj.Email, story.ThreadID)
	if err != nil || len(thread.Messages) != 3 || thread.Messages[1].Role != "user" || thread.Messages[1].Text != "put the risk before the ask and cut beat 7" || thread.Messages[2].ID != turn.Answer.ID {
		t.Fatalf("thread=%+v err=%v", thread.Messages, err)
	}
	if len(revised.Versions) != 1 || revised.Versions[0].Version != 1 {
		t.Fatalf("versions=%+v", revised.Versions)
	}
	// Server-computed diff summary stands in when the model gives none.
	prior := *story.Doc
	if summary := strings.ToLower(packagingStoryDiffSummary(prior, *revised.Doc)); !strings.Contains(summary, "cut beat 7") || !strings.Contains(summary, "added the risk of waiting") || !strings.Contains(summary, "revised beat 3 headline") {
		t.Fatalf("diff summary=%q", summary)
	}
	// Build the deck: the current version is the settled narrative.
	deck := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packaging/stories/"+story.ID+"/deck", "", cookies, packagingStoryHandler)
	if deck.Code != http.StatusCreated {
		t.Fatalf("deck status=%d body=%s", deck.Code, deck.Body.String())
	}
	var deckPayload map[string]any
	_ = json.Unmarshal(deck.Body.Bytes(), &deckPayload)
	root, _ := kanbanApp.osArtifactByID(fmt.Sprint(commissionMap(t, deckPayload)["id"]))
	stored := decodePackagingBriefMetadata(root.Metadata)
	if stored == nil || stored.Presentation == nil || stored.Presentation.StoryVersion != 2 || stored.Presentation.StoryOutlineArtifactID != story.ID {
		t.Fatalf("deck brief=%+v", stored)
	}
	if !strings.Contains(root.Metadata["threadQuery"], "The risk of waiting") {
		t.Fatalf("deck objective lacks the revised narrative: %q", root.Metadata["threadQuery"])
	}
}

func TestStoryStudioThreadTurnHookAndOperationDedup(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	storyResponderForTest(t, func(openAITextRequest) string { return storyDraftJSONForTest(t, 8) }, func(openAITextRequest) string {
		var prior packagingStoryDoc
		_ = json.Unmarshal([]byte(storyDraftJSONForTest(t, 8)), &prior)
		revision := packagingStoryRevision{packagingStoryDoc: prior, ChangedBeatIDs: []string{"b2"}, Summary: "Sharpened beat 2."}
		revision.Beats[1].Headline = "Beat 2 sharpened"
		raw, _ := json.Marshal(revision)
		return string(raw)
	})
	first := createStoryForTest(t, cookies, `{"operationId":"story-op-1","brief":{"subject":"Dedup story"}}`)
	second := createStoryForTest(t, cookies, `{"operationId":"story-op-1","brief":{"subject":"Dedup story"}}`)
	if first.ID != second.ID || first.ThreadID != second.ThreadID {
		t.Fatalf("double trigger minted two stories: %s / %s", first.ID, second.ID)
	}
	// The append-path hook: a message in the bound private thread is a
	// workshop turn; the committer receives the user turn + Scout's diff.
	thread, _, err := kanbanApp.scoutChatThreadByID(aj.Email, first.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	userMessage := scoutChatMessageRecord{ID: "scout-chat-message-hook-1", Kind: "message", Role: "user", Text: "sharpen beat 2", AuthorName: aj.Name, AuthorEmail: aj.Email}
	response, handled := kanbanApp.packagingStoryThreadTurn(context.Background(), aj, thread, userMessage, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return kanbanApp.commitScoutChatThreadMessages(aj.Email, thread.ID, messages...)
	})
	if !handled {
		t.Fatal("story thread turn was not owned by the workshop")
	}
	answer, _ := response["answer"].(scoutChatMessageRecord)
	view, _ := response["story"].(packagingStoryView)
	if !strings.Contains(answer.Text, "Sharpened beat 2.") || view.Version != 2 || view.Doc.Beats[1].Headline != "Beat 2 sharpened" || view.Doc.Beats[0].Headline != "Beat 1 headline" {
		t.Fatalf("hook response answer=%+v story=%+v", answer, view)
	}
	saved, _, _ := kanbanApp.scoutChatThreadByID(aj.Email, thread.ID)
	if len(saved.Messages) != 3 || saved.Messages[2].Role != "scout" {
		t.Fatalf("hook did not commit the turn: %+v", saved.Messages)
	}
	// Second delivery of the same message id: no new version, no second reply.
	again, handled := kanbanApp.packagingStoryThreadTurn(context.Background(), aj, saved, userMessage, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		t.Fatal("redelivered turn committed messages again")
		return scoutChatThreadRecord{}, nil
	})
	if !handled || again["replayed"] != true {
		t.Fatalf("redelivery not treated as a no-op: %v", again)
	}
	if entry, _ := kanbanApp.osArtifactByID(first.ID); artifactVersion(entry) != 2 {
		t.Fatalf("redelivery journaled a version: v%d", artifactVersion(entry))
	}
	if replayed, _, _ := kanbanApp.scoutChatThreadByID(aj.Email, thread.ID); len(replayed.Messages) != 3 {
		t.Fatalf("redelivery appended messages: %d", len(replayed.Messages))
	}
	// Unbound threads and other members' threads hand the turn back.
	other, _ := kanbanApp.createScoutChatThread(aj.Email, aj.Name, "plain", scoutChatVisibilityPrivate)
	if _, handled := kanbanApp.packagingStoryThreadTurn(context.Background(), aj, other, userMessage, nil); handled {
		t.Fatal("an unbound thread was treated as a story workshop")
	}
	joel := accountStore().findUser("joel@shareability.com")
	if _, handled := kanbanApp.packagingStoryThreadTurn(context.Background(), joel, thread, userMessage, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return scoutChatThreadRecord{}, nil
	}); handled {
		t.Fatal("another member workshopped a private story")
	}
}

func TestCreatePackagingStoryAdoptsExistingOperation(t *testing.T) {
	_, aj := setupPackagingStudioTest(t)
	kanbanApp.apiKey = ""
	brief := packagingStoryBrief{Subject: "Adopted story", Audience: "board"}
	first, firstThread, err := kanbanApp.createPackagingStoryWithContext(context.Background(), aj, brief, "packaging-intake-abc")
	if err != nil {
		t.Fatal(err)
	}
	// A replayed or concurrent ask with the same operation adopts the outline
	// AND its workshop thread; no second artifact, no second thread.
	before := len(kanbanApp.listPackagingStories(context.Background(), aj))
	second, secondThread, err := kanbanApp.createPackagingStoryWithContext(context.Background(), aj, brief, "packaging-intake-abc")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || secondThread.ID != firstThread.ID || len(kanbanApp.listPackagingStories(context.Background(), aj)) != before {
		t.Fatalf("replay minted a second story: %s/%s vs %s/%s", first.ID, firstThread.ID, second.ID, secondThread.ID)
	}
	// Same brief without an operation id derives one from requester + brief,
	// so a bare double-post still adopts; a different operation mints anew.
	third, _, err := kanbanApp.createPackagingStoryWithContext(context.Background(), aj, brief, "")
	if err != nil {
		t.Fatal(err)
	}
	fourth, _, err := kanbanApp.createPackagingStoryWithContext(context.Background(), aj, brief, "")
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID || fourth.ID != third.ID {
		t.Fatalf("derived operation adoption wrong: first=%s third=%s fourth=%s", first.ID, third.ID, fourth.ID)
	}
	fifth, _, err := kanbanApp.createPackagingStoryWithContext(context.Background(), aj, brief, "packaging-intake-xyz")
	if err != nil {
		t.Fatal(err)
	}
	if fifth.ID == first.ID || fifth.ID == third.ID {
		t.Fatal("a different operation adopted an unrelated story")
	}
	// Another requester with the same operation id never adopts AJ's story.
	joel := accountStore().findUser("joel@shareability.com")
	joels, _, err := kanbanApp.createPackagingStoryWithContext(context.Background(), joel, brief, "packaging-intake-abc")
	if err != nil {
		t.Fatal(err)
	}
	if joels.ID == first.ID {
		t.Fatal("operation adoption crossed requesters")
	}
}

func TestStoryStudioAppendPathRunsWorkshopTurnsAndLeavesNewAsksToIntake(t *testing.T) {
	_, aj := setupPackagingStudioTest(t)
	storyResponderForTest(t, func(openAITextRequest) string { return storyDraftJSONForTest(t, 8) }, func(openAITextRequest) string {
		var prior packagingStoryDoc
		_ = json.Unmarshal([]byte(storyDraftJSONForTest(t, 8)), &prior)
		revision := packagingStoryRevision{packagingStoryDoc: prior, ChangedBeatIDs: []string{"b8"}, Summary: "Cut the closing beat to one line."}
		revision.Beats[7].Intent = "One line."
		raw, _ := json.Marshal(revision)
		return string(raw)
	})
	story, thread, err := kanbanApp.createPackagingStoryWithContext(context.Background(), aj, packagingStoryBrief{Subject: "Append path story"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// A plain message in the bound thread goes through the ordinary chat door
	// and lands as a workshop turn: one user message, one Scout diff reply,
	// one new outline version.
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), aj, thread.ID, "make the close one line", nil, "")
	if err != nil {
		t.Fatalf("append err=%v", err)
	}
	answer, _ := response["answer"].(scoutChatMessageRecord)
	if answer.Role != "scout" || !strings.Contains(answer.Text, "Cut the closing beat to one line.") || !strings.Contains(answer.Text, "(outline v2)") {
		t.Fatalf("append answer=%+v", answer)
	}
	entry, _ := kanbanApp.osArtifactByID(story.ID)
	if artifactVersion(entry) != 2 || packagingStoryDocForEntry(entry).Beats[7].Intent != "One line." {
		t.Fatalf("outline after append: v%d %+v", artifactVersion(entry), packagingStoryDocForEntry(entry).Beats[7])
	}
	saved, _, _ := kanbanApp.scoutChatThreadByID(aj.Email, thread.ID)
	scoutReplies := 0
	for _, message := range saved.Messages {
		if message.Role == "scout" {
			scoutReplies++
		}
	}
	if len(saved.Messages) != 3 || scoutReplies != 1 {
		t.Fatalf("thread after append: %d messages, %d scout replies", len(saved.Messages), scoutReplies)
	}
	// A NEW packaging ask in the same thread belongs to the intake, not the
	// workshop: no outline version is journaled by it.
	kind, isAsk := packagingIntakeDetect("@scout build me a presentation for the board about pricing", nil, nil)
	if !isAsk || kind == "" {
		t.Skip("intake detection contract changed; the precedence pin needs its fixture updated")
	}
	before := artifactVersion(entry)
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), aj, thread.ID, "@scout build me a presentation for the board about pricing", nil, ""); err != nil {
		t.Fatalf("intake append err=%v", err)
	}
	if after, _ := kanbanApp.osArtifactByID(story.ID); artifactVersion(after) != before {
		t.Fatalf("a new packaging ask revised the outline: v%d → v%d", before, artifactVersion(after))
	}
}

// A workshop turn is read outline → model → save. Two overlapping turns on the
// same outline must not silently discard one revision while Scout reports both
// as applied: the turn serializes on the outline and re-reads it under the
// lock, so the second revision is built on the first.
func TestStoryStudioConcurrentWorkshopTurnsDoNotLoseARevision(t *testing.T) {
	_, aj := setupPackagingStudioTest(t)
	// A responder with no shared state: the two turns run concurrently, so the
	// recording helper's call slice would be the test's own data race.
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		output := storyDraftJSONForTest(t, 8)
		if request.Workflow == packagingStoryWorkflowRevise {
			// Revise whatever outline the server handed the model, renaming
			// only the beat this message names.
			var prior packagingStoryDoc
			marker := "Current outline (JSON):\n"
			rest := request.Input[strings.Index(request.Input, marker)+len(marker):]
			if err := json.Unmarshal([]byte(rest[:strings.Index(rest, "\n")]), &prior); err != nil {
				return "", fmt.Errorf("revision input did not carry the current outline: %w", err)
			}
			target, summary := "b2", "Sharpened beat 2."
			if strings.Contains(request.Input, "sharpen beat 3") {
				target, summary = "b3", "Sharpened beat 3."
			}
			revision := packagingStoryRevision{packagingStoryDoc: prior, ChangedBeatIDs: []string{target}, Summary: summary}
			for index := range revision.Beats {
				if revision.Beats[index].ID == target {
					revision.Beats[index].Headline = target + " sharpened"
				}
			}
			raw, _ := json.Marshal(revision)
			output = string(raw)
		}
		if request.ValidateOutput != nil {
			if err := request.ValidateOutput(output); err != nil {
				return "", err
			}
		}
		return output, nil
	})
	story, _, err := kanbanApp.createPackagingStoryWithContext(context.Background(), aj, packagingStoryBrief{Subject: "Concurrent story"}, "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 2)
	for _, text := range []string{"sharpen beat 2", "sharpen beat 3"} {
		go func(text string) {
			entry, _ := kanbanApp.osArtifactByID(story.ID)
			_, _, ok := kanbanApp.packagingStoryWorkshopTurn(context.Background(), aj, entry,
				scoutChatMessageRecord{ID: "scout-chat-message-" + strings.ReplaceAll(text, " ", "-"), Kind: "message", Role: "user", Text: text, AuthorEmail: aj.Email})
			done <- ok
		}(text)
	}
	<-done
	<-done
	final, _ := kanbanApp.osArtifactByID(story.ID)
	doc := packagingStoryDocForEntry(final)
	headlines := map[string]string{}
	for _, beat := range doc.Beats {
		headlines[beat.ID] = beat.Headline
	}
	if artifactVersion(final) != 3 {
		t.Fatalf("two turns journaled v%d, want v3 (one version each)", artifactVersion(final))
	}
	if headlines["b2"] != "b2 sharpened" || headlines["b3"] != "b3 sharpened" {
		t.Fatalf("a concurrent turn was lost: b2=%q b3=%q", headlines["b2"], headlines["b3"])
	}
}

// The append path calls the story hook a SECOND time for the same message when
// the first turn's commit fails (packaging_intake.go calls it, then
// scout_chat_threads.go calls it again). One user message must never buy two
// model calls and two outline versions.
func TestStoryStudioWorkshopTurnIsIdempotentWhenTheCommitFails(t *testing.T) {
	_, aj := setupPackagingStudioTest(t)
	calls := storyResponderForTest(t, func(openAITextRequest) string { return storyDraftJSONForTest(t, 8) }, func(openAITextRequest) string {
		var prior packagingStoryDoc
		_ = json.Unmarshal([]byte(storyDraftJSONForTest(t, 8)), &prior)
		revision := packagingStoryRevision{packagingStoryDoc: prior, ChangedBeatIDs: []string{"b2"}, Summary: "Sharpened beat 2."}
		revision.Beats[1].Headline = "Beat 2 sharpened"
		raw, _ := json.Marshal(revision)
		return string(raw)
	})
	story, thread, err := kanbanApp.createPackagingStoryWithContext(context.Background(), aj, packagingStoryBrief{Subject: "Retry story"}, "")
	if err != nil {
		t.Fatal(err)
	}
	draftCalls := len(*calls)
	userMessage := scoutChatMessageRecord{ID: "scout-chat-message-retry-1", Kind: "message", Role: "user", Text: "sharpen beat 2", AuthorName: aj.Name, AuthorEmail: aj.Email}
	// First delivery: the revision lands, the commit fails, the turn is handed
	// back exactly as the append path expects.
	if _, handled := kanbanApp.packagingStoryThreadTurn(context.Background(), aj, thread, userMessage, func(...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return scoutChatThreadRecord{}, fmt.Errorf("thread commit failed")
	}); handled {
		t.Fatal("a failed commit must hand the turn back")
	}
	afterFirst, _ := kanbanApp.osArtifactByID(story.ID)
	if artifactVersion(afterFirst) != 2 {
		t.Fatalf("first turn version=%d, want 2", artifactVersion(afterFirst))
	}
	revisionCalls := len(*calls) - draftCalls
	// The retry: same message id, no committed reply to recognize it by. It
	// must replay the recorded answer, not revise again.
	response, handled := kanbanApp.packagingStoryThreadTurn(context.Background(), aj, thread, userMessage, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return kanbanApp.commitScoutChatThreadMessages(aj.Email, thread.ID, messages...)
	})
	if !handled {
		t.Fatal("the retry was not owned by the workshop")
	}
	if len(*calls)-draftCalls != revisionCalls {
		t.Fatalf("the retry spent another provider call: %d → %d", revisionCalls, len(*calls)-draftCalls)
	}
	final, _ := kanbanApp.osArtifactByID(story.ID)
	if artifactVersion(final) != 2 {
		t.Fatalf("the retry journaled a second version for one message: v%d", artifactVersion(final))
	}
	answer, _ := response["answer"].(scoutChatMessageRecord)
	if !strings.Contains(answer.Text, "Sharpened beat 2.") || answer.CausedByMessageID != userMessage.ID {
		t.Fatalf("replayed answer=%+v", answer)
	}
	saved, _, _ := kanbanApp.scoutChatThreadByID(aj.Email, thread.ID)
	scoutReplies := 0
	for _, message := range saved.Messages {
		if message.Role == "scout" {
			scoutReplies++
		}
	}
	if scoutReplies != 1 {
		t.Fatalf("scout replies after the retry=%d, want 1", scoutReplies)
	}
}
