package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupPackagingStudioTest wires the Wave 11 fixture: an isolated app, the
// seeded accounts, a configured worker key (a registry-tool launch needs one
// to mint its goal), and stubbed async starters so nothing actually runs.
func setupPackagingStudioTest(t *testing.T) ([]*http.Cookie, *userAccount) {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))
	t.Setenv("BONFIRE_GOAL_USER_CAP", "10")
	kanbanApp.apiKey = "openai-router-test"
	// Earlier tests in the same process may have tripped the chat seat's
	// breaker with the fake key; Story Studio's model draft rides that seat.
	providerBreakers.reset()
	t.Cleanup(providerBreakers.reset)
	previousGoalStarter := startGoalThreadAsync
	previousThreadStarter := startAgentThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStarter; startAgentThreadAsync = previousThreadStarter })
	aj := accountStore().findUser("aj@shareability.com")
	if aj == nil {
		t.Fatal("seed owner missing")
	}
	return loginAs(t, "aj@shareability.com", "B0NFIRE!"), aj
}

func postPackagingCommission(t *testing.T, cookies []*http.Cookie, body string) (int, map[string]any) {
	t.Helper()
	response := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packaging/commissions", body, cookies, packagingCommissionsHandler)
	payload := map[string]any{}
	if response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode %s: %v", response.Body.String(), err)
		}
	}
	return response.Code, payload
}

func commissionMap(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	commission, ok := payload["commission"].(map[string]any)
	if !ok {
		t.Fatalf("commission missing from %v", payload)
	}
	return commission
}

func TestPackagingCommissionBriefValidationFailsClosedPerKind(t *testing.T) {
	cookies, _ := setupPackagingStudioTest(t)
	for name, body := range map[string]string{
		"unknown kind":            `{"kind":"document","brief":{"subject":"x"}}`,
		"missing brief":           `{"kind":"research"}`,
		"research bad scope":      `{"kind":"research","brief":{"scope":"galaxy","depth":"brief","format":"memo","question":"q?"}}`,
		"research bad depth":      `{"kind":"research","brief":{"scope":"market","depth":"infinite","format":"memo","question":"q?"}}`,
		"research bad format":     `{"kind":"research","brief":{"scope":"market","depth":"brief","format":"poem","question":"q?"}}`,
		"research no question":    `{"kind":"research","brief":{"scope":"market","depth":"brief","format":"memo","question":"  "}}`,
		"research bad source":     `{"kind":"research","brief":{"scope":"market","depth":"brief","format":"memo","question":"q?","sources":[{"url":"ftp://x"}]}}`,
		"research ambiguous src":  `{"kind":"research","brief":{"scope":"market","depth":"brief","format":"memo","question":"q?","sources":[{"ref":"file|a","url":"https://x.test"}]}}`,
		"presentation no subject": `{"kind":"presentation","brief":{"audience":"board"}}`,
		"presentation copy style": `{"kind":"presentation","brief":{"subject":"s","copyStyle":"shouty"}}`,
		"presentation imagery":    `{"kind":"presentation","brief":{"subject":"s","imageryMode":"hologram"}}`,
		"presentation theme":      `{"kind":"presentation","brief":{"subject":"s","lookFeel":{"themeId":"neon"}}}`,
		"presentation length":     `{"kind":"presentation","brief":{"subject":"s","length":"99"}}`,
		"presentation both":       `{"kind":"presentation","brief":{"subject":"s","research":{"artifactId":"os-artifact-1","commissionFirst":true}}}`,
		"story no subject":        `{"kind":"story","brief":{"thesis":"t"}}`,
	} {
		code, payload := postPackagingCommission(t, cookies, body)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d payload=%v, want 400", name, code, payload)
		}
	}

	// Normalization: a numeric length is a slide count, imagery defaults to
	// hybrid, commissionFirst derives a research brief from the subject.
	brief, err := decodePackagingBrief("presentation", json.RawMessage(`{"subject":" Q3  board  update ","length":"14 slides","research":{"commissionFirst":true},"lookFeel":{"themeId":"PUTTY"}}`))
	if err != nil {
		t.Fatal(err)
	}
	presentation := brief.Presentation
	if presentation.Subject != "Q3 board update" || presentation.ImageryMode != packagingImageryHybrid || presentation.LookFeel.ThemeID != "putty" {
		t.Fatalf("normalized presentation=%+v", presentation)
	}
	if count, _ := packagingLengthSlides(presentation.Length); count != 14 {
		t.Fatalf("length %q → %d slides, want 14", presentation.Length, count)
	}
	if presentation.Research == nil || presentation.Research.Brief == nil || presentation.Research.Brief.Scope != "market" || !strings.Contains(presentation.Research.Brief.Question, "Q3 board update") {
		t.Fatalf("commissionFirst research brief=%+v", presentation.Research)
	}
	objective := packagingPresentationObjective(*presentation, "", "")
	if count, ok := packagingRequestedSlideCount(objective); !ok || count != 14 {
		t.Fatalf("objective %q did not carry the slide count for the engine", objective)
	}
}

func TestPackagingResearchCommissionLaunchesResearchWorkWithBriefProvenance(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	body := `{"kind":"research","operationId":"op-research-1","brief":{"scope":"competitor","depth":"deep","format":"one-pager","audience":"the board","question":"Who wins the mid-market in 2027?","sources":[{"url":"https://example.com/report"}]}}`
	code, payload := postPackagingCommission(t, cookies, body)
	if code != http.StatusCreated {
		t.Fatalf("research commission status=%d payload=%v", code, payload)
	}
	commission := commissionMap(t, payload)
	rootID := fmt.Sprint(commission["id"])
	if commission["kind"] != packagingCommissionKindResearch || commission["projectId"] != rootID || fmt.Sprint(commission["threadId"]) == "" || fmt.Sprint(commission["messageId"]) == "" {
		t.Fatalf("commission view=%v", commission)
	}
	root, ok := kanbanApp.osArtifactByID(rootID)
	if !ok {
		t.Fatal("work root missing")
	}
	if root.Metadata["mode"] != "research" || root.Metadata[packagingCommissionKindMetadataKey] != packagingCommissionKindResearch || root.Metadata[packagingCommissionByMetadataKey] != aj.Email {
		t.Fatalf("root metadata=%v", root.Metadata)
	}
	stored := decodePackagingBriefMetadata(root.Metadata)
	if stored == nil || stored.Research == nil || stored.Research.Scope != "competitor" || stored.Research.Depth != "deep" || len(stored.Research.Sources) != 1 || stored.Research.Sources[0].URL != "https://example.com/report" {
		t.Fatalf("stored brief=%+v", stored)
	}
	objective := root.Metadata["threadQuery"]
	for _, want := range []string{"Who wins the mid-market in 2027?", "competitor research", "one-page", "Audience: the board", "https://example.com/report"} {
		if !strings.Contains(objective, want) {
			t.Fatalf("objective %q missing %q", objective, want)
		}
	}
	// The brief is the requester's own message in the private thread and the
	// work card follows it.
	thread, _, err := kanbanApp.scoutChatThreadByID(aj.Email, fmt.Sprint(commission["threadId"]))
	if err != nil {
		t.Fatal(err)
	}
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate || !strings.HasPrefix(thread.Title, "Research: ") {
		t.Fatalf("thread=%+v", thread)
	}
	messageIndex := scoutChatMessageIndex(thread, fmt.Sprint(commission["messageId"]))
	if messageIndex < 0 || thread.Messages[messageIndex].Role != "user" || !strings.Contains(thread.Messages[messageIndex].Text, "Commission research — competitor · deep · one-pager") {
		t.Fatalf("brief message not on thread: %+v", thread.Messages)
	}
	cardFound := false
	for _, message := range thread.Messages {
		if message.Thread != nil && message.Thread.ArtifactID == rootID && message.CausedByMessageID == fmt.Sprint(commission["messageId"]) {
			cardFound = true
		}
	}
	if !cardFound {
		t.Fatalf("work card missing from thread: %+v", thread.Messages)
	}
	// GET /assistant/packaging/commissions/{id} and the studio row carry the
	// brief + commission identity.
	get := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/commissions/"+rootID, "", cookies, packagingCommissionHandler)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"kind":"research"`) {
		t.Fatalf("commission GET status=%d body=%s", get.Code, get.Body.String())
	}
	list := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+rootID, "", cookies, studioProjectsHandler)
	if list.Code != http.StatusOK {
		t.Fatalf("studio row status=%d body=%s", list.Code, list.Body.String())
	}
	var row struct {
		Project studioProjectView `json:"project"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.Project.Kind != studioProjectKindResearch || row.Project.Commission == nil || row.Project.Commission.Kind != packagingCommissionKindResearch || row.Project.Commission.ThreadID != thread.ID || row.Project.Brief == nil {
		t.Fatalf("studio row=%+v", row.Project)
	}
	if research, _ := row.Project.Brief["research"].(map[string]any); research["question"] != "Who wins the mid-market in 2027?" {
		t.Fatalf("row brief=%v", row.Project.Brief)
	}
	// Replay with the same operation adopts the same commission — no second
	// message, no second root.
	replayCode, replay := postPackagingCommission(t, cookies, body)
	if replayCode != http.StatusCreated || commissionMap(t, replay)["id"] != rootID {
		t.Fatalf("replay status=%d payload=%v", replayCode, replay)
	}
	if again, _, _ := kanbanApp.scoutChatThreadByID(aj.Email, thread.ID); len(again.Messages) != len(thread.Messages) {
		t.Fatalf("replay appended messages: %d → %d", len(thread.Messages), len(again.Messages))
	}
	unknown := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/commissions/os-artifact-nope", "", cookies, packagingCommissionHandler)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown commission status=%d", unknown.Code)
	}
}

func TestPackagingPresentationCommissionMapsImageryModeOntoBleedCap(t *testing.T) {
	cookies, _ := setupPackagingStudioTest(t)
	launch := func(mode string) (meetingMemoryEntry, goalPlan) {
		t.Helper()
		code, payload := postPackagingCommission(t, cookies, fmt.Sprintf(`{"kind":"presentation","brief":{"subject":"Series B narrative %s","audience":"investors","copyStyle":"persuasive","lookFeel":{"themeId":"graphite","notes":"restrained, editorial"},"imageryMode":%q,"length":"long"}}`, mode, mode))
		if code != http.StatusCreated {
			t.Fatalf("presentation commission (%s) status=%d payload=%v", mode, code, payload)
		}
		root, ok := kanbanApp.osArtifactByID(fmt.Sprint(commissionMap(t, payload)["id"]))
		if !ok {
			t.Fatal("presentation root missing")
		}
		plan, ok := decodeGoalPlan(root.Metadata["goalPlan"])
		if !ok || plan.ProcessID != packagingStudioProcessID {
			t.Fatalf("presentation root is not a Packaging Studio goal: %v", root.Metadata)
		}
		return root, plan
	}
	fullBleed, fullPlan := launch(packagingImageryFullBleed)
	if fullBleed.Metadata[packagingImageryModeMetadataKey] != packagingImageryFullBleed || fullBleed.Metadata["processId"] != packagingStudioProcessID {
		t.Fatalf("full-bleed root metadata=%v", fullBleed.Metadata)
	}
	for _, want := range []string{"Series B narrative full-bleed", "Audience: investors", "persuasive", "theme graphite", "restrained, editorial", "Imagery mode: full-bleed", "20 slides"} {
		if !strings.Contains(fullPlan.Objective, want) {
			t.Fatalf("objective %q missing %q", fullPlan.Objective, want)
		}
	}
	if cap := packagingStudioBleedCapForPlan(kanbanApp, &fullPlan); cap != packagingStudioImageryMaxShots {
		t.Fatalf("full-bleed cap=%d, want %d", cap, packagingStudioImageryMaxShots)
	}
	_, onSlidePlan := launch(packagingImageryOnSlide)
	if cap := packagingStudioBleedCapForPlan(kanbanApp, &onSlidePlan); cap != 0 {
		t.Fatalf("on-slide cap=%d, want 0", cap)
	}
	_, hybridPlan := launch(packagingImageryHybrid)
	if cap := packagingStudioBleedCapForPlan(kanbanApp, &hybridPlan); cap != packagingStudioDefaultBleedCap {
		t.Fatalf("hybrid cap=%d, want %d", cap, packagingStudioDefaultBleedCap)
	}
	if cap := packagingStudioBleedCapForPlan(kanbanApp, &goalPlan{GoalID: "agent-thread-goal-unknown"}); cap != packagingStudioDefaultBleedCap {
		t.Fatalf("unknown plan cap=%d, want default", cap)
	}

	// The identity-direction parser honors the cap: two bleeds pass under
	// full-bleed, fail under the default law, and any bleed fails under on-slide.
	value := packagingIdentityDirectionValueForTest()
	shots := value["shots"].([]any)
	second := shots[1].(map[string]any)
	second["slot"] = "bleed"
	allowed := map[string]struct{}{"cover": {}, "proof": {}}
	body := fencedIdentityDirectionForTest(t, value)
	if _, err := parseImageryDirectionWithBleedCap(body, allowed, false, packagingStudioBleedCapForImageryMode(packagingImageryFullBleed)); err != nil {
		t.Fatalf("full-bleed rejected two bleeds: %v", err)
	}
	if _, err := parseImageryDirection(body, allowed); err == nil || !strings.Contains(err.Error(), "at most one bleed") {
		t.Fatalf("default law accepted two bleeds: %v", err)
	}
	if _, err := parseImageryDirectionWithBleedCap(body, allowed, false, 0); err == nil || !strings.Contains(err.Error(), "on-slide") {
		t.Fatalf("on-slide accepted a bleed: %v", err)
	}
	// The prompt itself names the override so the model directs the slots.
	identity := ""
	for _, stage := range packagingStudioDefinition().Stages {
		if stage.ID == "identity" {
			identity = stage.PromptBody
		}
	}
	if !strings.Contains(identity, "IMAGERY MODE") || !strings.Contains(identity, "on-slide") {
		t.Fatal("identity stage prompt does not carry the imagery-mode override")
	}
}

func TestPackagingCommissionFirstChainsResearchIntoPresentation(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	code, payload := postPackagingCommission(t, cookies, `{"kind":"presentation","brief":{"subject":"Enter the Nordics","audience":"exec team","imageryMode":"on-slide","research":{"commissionFirst":true,"brief":{"scope":"market","depth":"standard","format":"report","question":"How big is the Nordic mid-market?"}}}}`)
	if code != http.StatusCreated {
		t.Fatalf("chain commission status=%d payload=%v", code, payload)
	}
	commission := commissionMap(t, payload)
	researchID := fmt.Sprint(commission["id"])
	chain, _ := commission["chain"].(map[string]any)
	if commission["kind"] != packagingCommissionKindResearch || chain == nil || chain["state"] != packagingChainStateWaiting {
		t.Fatalf("chain commission=%v", commission)
	}
	research, _ := kanbanApp.osArtifactByID(researchID)
	if research.Metadata["mode"] != "research" || research.Metadata[packagingChainBriefMetadataKey] == "" {
		t.Fatalf("research root=%v", research.Metadata)
	}
	// While the research runs, reads leave the chain waiting.
	get := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/commissions/"+researchID, "", cookies, packagingCommissionHandler)
	if !strings.Contains(get.Body.String(), `"state":"waiting"`) {
		t.Fatalf("premature chain advance: %s", get.Body.String())
	}
	before := len(kanbanApp.memory.studioProjectProjectionSnapshot())
	// The research result lands → the requester's next read launches the deck.
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(researchID, "", "# Nordic mid-market\n\n## Executive Summary\n\nBig.", "Scout", map[string]string{"status": artifactStatusComplete, "threadStatus": artifactStatusComplete}); err != nil {
		t.Fatal(err)
	}
	get = artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/commissions/"+researchID, "", cookies, packagingCommissionHandler)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"state":"launched"`) {
		t.Fatalf("chain did not advance: status=%d body=%s", get.Code, get.Body.String())
	}
	advanced, _ := kanbanApp.osArtifactByID(researchID)
	presentationID := advanced.Metadata[packagingChainPresentationIDMetadataKey]
	presentation, ok := kanbanApp.osArtifactByID(presentationID)
	if !ok || presentation.Metadata["processId"] != packagingStudioProcessID || presentation.Metadata[packagingImageryModeMetadataKey] != packagingImageryOnSlide {
		t.Fatalf("presentation root=%v", presentation.Metadata)
	}
	stored := decodePackagingBriefMetadata(presentation.Metadata)
	if stored == nil || stored.Presentation == nil || stored.Presentation.Research == nil || stored.Presentation.Research.ArtifactID != researchID {
		t.Fatalf("presentation brief=%+v", stored)
	}
	if !strings.Contains(presentation.Metadata["threadQuery"], "artifact "+researchID) {
		t.Fatalf("presentation objective does not name the research: %q", presentation.Metadata["threadQuery"])
	}
	if presentation.Metadata[packagingCommissionThreadIDMetadataKey] != research.Metadata[packagingCommissionThreadIDMetadataKey] {
		t.Fatal("chained presentation left the research conversation")
	}
	if after := len(kanbanApp.memory.studioProjectProjectionSnapshot()); after != before+1 {
		t.Fatalf("studio directory grew by %d, want 1", after-before)
	}
	// Idempotent: a second read does not launch a second deck.
	artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/commissions/"+researchID, "", cookies, packagingCommissionHandler)
	if again, _ := kanbanApp.osArtifactByID(researchID); again.Metadata[packagingChainPresentationIDMetadataKey] != presentationID {
		t.Fatal("chain relaunched")
	}
	if len(kanbanApp.memory.studioProjectProjectionSnapshot()) != before+1 {
		t.Fatal("second read launched another deck")
	}
	// Another member reading the same root never advances someone else's chain.
	joel := accountStore().findUser("joel@shareability.com")
	if joel == nil {
		t.Fatal("seed member missing")
	}
	if _, advancedByJoel := kanbanApp.advancePackagingCommissionChain(context.Background(), joel, research); advancedByJoel {
		t.Fatal("a non-requester advanced the chain")
	}
	_ = aj
}

func TestPackagingStoryOutlineVersionsThreadAndDeckHandoff(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	create := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packaging/stories", `{"brief":{"subject":"Why now for compute credits","audience":"CFOs","thesis":"Credits beat discounts","length":"short"}}`, cookies, packagingStoriesHandler)
	if create.Code != http.StatusCreated {
		t.Fatalf("story create status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		Story    packagingStoryView `json:"story"`
		ThreadID string             `json:"threadId"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	story := created.Story
	if story.ID == "" || story.Version != 1 || story.ThreadID == "" || story.ThreadID != created.ThreadID || story.Title != "Why now for compute credits" {
		t.Fatalf("story=%+v", story)
	}
	for _, want := range []string{"# Why now for compute credits", "**Thesis:** Credits beat discounts", "## Beats", "1. **Open — why now**", "Close — the ask", "## Open questions"} {
		if !strings.Contains(story.Outline, want) {
			t.Fatalf("outline missing %q:\n%s", want, story.Outline)
		}
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(aj.Email, story.ThreadID)
	if err != nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate || len(thread.Messages) != 1 || !strings.Contains(thread.Messages[0].Text, "Workshop a story") {
		t.Fatalf("bound thread=%+v err=%v", thread, err)
	}
	entry, _ := kanbanApp.osArtifactByID(story.ID)
	if !packagingStoryOutlineArtifact(entry) || studioProjectProjectionRelevantEntry(entry) {
		t.Fatalf("story artifact classification wrong: %v", entry.Metadata)
	}
	// The commissions door mints the same outline for kind=story.
	code, payload := postPackagingCommission(t, cookies, `{"kind":"story","brief":{"subject":"Second story"}}`)
	if code != http.StatusCreated || payload["story"] == nil {
		t.Fatalf("story via commissions status=%d payload=%v", code, payload)
	}

	// Turn-based edits keep a version per save; a stale save conflicts.
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/packaging/stories/"+story.ID, `{"outline":"# Why now\n\n1. Open\n2. Close","expectedVersion":1}`, cookies, packagingStoryHandler)
	if patch.Code != http.StatusOK {
		t.Fatalf("story patch status=%d body=%s", patch.Code, patch.Body.String())
	}
	var patched struct {
		Story packagingStoryView `json:"story"`
	}
	if err := json.Unmarshal(patch.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Story.Version != 2 || patched.Story.Outline != "# Why now\n\n1. Open\n2. Close" || len(patched.Story.Versions) != 1 || patched.Story.Versions[0].Version != 1 {
		t.Fatalf("patched story=%+v", patched.Story)
	}
	stale := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/packaging/stories/"+story.ID, `{"outline":"x","expectedVersion":1}`, cookies, packagingStoryHandler)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale patch status=%d", stale.Code)
	}
	get := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/stories/"+story.ID, "", cookies, packagingStoryHandler)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"version":2`) {
		t.Fatalf("story GET status=%d body=%s", get.Code, get.Body.String())
	}
	list := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/stories", "", cookies, packagingStoriesHandler)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), story.ID) {
		t.Fatalf("story list status=%d body=%s", list.Code, list.Body.String())
	}

	// Build the deck: the outline becomes the presentation's settled narrative
	// and the commission lands in the story's own thread.
	deck := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packaging/stories/"+story.ID+"/deck", `{"brief":{"copyStyle":"narrative","imageryMode":"full-bleed"}}`, cookies, packagingStoryHandler)
	if deck.Code != http.StatusCreated {
		t.Fatalf("story deck status=%d body=%s", deck.Code, deck.Body.String())
	}
	var deckPayload map[string]any
	if err := json.Unmarshal(deck.Body.Bytes(), &deckPayload); err != nil {
		t.Fatal(err)
	}
	commission := commissionMap(t, deckPayload)
	if commission["kind"] != packagingCommissionKindPresentation || commission["threadId"] != story.ThreadID {
		t.Fatalf("deck commission=%v", commission)
	}
	root, _ := kanbanApp.osArtifactByID(fmt.Sprint(commission["id"]))
	stored := decodePackagingBriefMetadata(root.Metadata)
	if stored == nil || stored.Presentation == nil || stored.Presentation.StoryOutlineArtifactID != story.ID || stored.Presentation.Subject != "Why now for compute credits" || stored.Presentation.Audience != "CFOs" || stored.Presentation.CopyStyle != "narrative" {
		t.Fatalf("deck brief=%+v", stored)
	}
	if objective := root.Metadata["threadQuery"]; !strings.Contains(objective, "Settled narrative") || !strings.Contains(objective, "1. Open 2. Close") || !strings.Contains(objective, "8 slides") {
		t.Fatalf("deck objective=%q", objective)
	}
	// A body-less POST also works (defaults from the story brief).
	empty := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packaging/stories/"+story.ID+"/deck", "", cookies, packagingStoryHandler)
	if empty.Code != http.StatusCreated {
		t.Fatalf("empty deck POST status=%d body=%s", empty.Code, empty.Body.String())
	}
	missing := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packaging/stories/os-artifact-nope/deck", "", cookies, packagingStoryHandler)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing story deck status=%d", missing.Code)
	}
}

func TestPackagingCommissionRefusesUnreadableSourcesAndKeylessServers(t *testing.T) {
	cookies, _ := setupPackagingStudioTest(t)
	joelCookies := loginAs(t, "joel@shareability.com", "B0NFIRE!")
	secret := uploadDriveFileRow(t, joelCookies, "secret.txt", "text/plain", []byte("joel private notes"), map[string]string{"visibility": "private"})
	mine := uploadDriveFileRow(t, cookies, "brief.txt", "text/plain", []byte("aj brief"), nil)

	forbidden := fmt.Sprintf(`{"kind":"research","brief":{"scope":"company","depth":"brief","format":"memo","question":"q?","sources":[{"ref":%q}]}}`, assistantFileContextRef(secret.ID))
	code, payload := postPackagingCommission(t, cookies, forbidden)
	if code != http.StatusForbidden {
		t.Fatalf("unreadable Drive source status=%d payload=%v", code, payload)
	}
	before := len(kanbanApp.memory.studioProjectProjectionSnapshot())
	unknownArtifact := `{"kind":"presentation","brief":{"subject":"s","research":{"artifactId":"os-artifact-does-not-exist"}}}`
	if code, payload = postPackagingCommission(t, cookies, unknownArtifact); code != http.StatusForbidden {
		t.Fatalf("unknown research artifact status=%d payload=%v", code, payload)
	}
	if len(kanbanApp.memory.studioProjectProjectionSnapshot()) != before {
		t.Fatal("a refused source launched work")
	}
	allowed := fmt.Sprintf(`{"kind":"research","brief":{"scope":"company","depth":"brief","format":"memo","question":"q?","sources":[{"ref":%q}]}}`, assistantFileContextRef(mine.ID))
	code, payload = postPackagingCommission(t, cookies, allowed)
	if code != http.StatusCreated {
		t.Fatalf("readable Drive source status=%d payload=%v", code, payload)
	}
	root, _ := kanbanApp.osArtifactByID(fmt.Sprint(commissionMap(t, payload)["id"]))
	refs := decodeAssistantContextRefs(root.Metadata["contextRefs"])
	if len(refs) != 1 || refs[0] != assistantFileContextRef(mine.ID) {
		t.Fatalf("launched contextRefs=%v", refs)
	}

	kanbanApp.apiKey = ""
	code, payload = postPackagingCommission(t, cookies, `{"kind":"research","brief":{"scope":"company","depth":"brief","format":"memo","question":"q?"}}`)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("keyless status=%d payload=%v", code, payload)
	}
}

func packagingWaitingIntakeForTest(t *testing.T, root meetingMemoryEntry, requester *userAccount) packagingIntakeRecord {
	t.Helper()
	threadID := root.Metadata[packagingCommissionThreadIDMetadataKey]
	record := packagingIntakeRecord{
		ID: packagingIntakeRecordID(threadID, root.Metadata[packagingCommissionMessageIDMetadataKey]), Kind: packagingCommissionKindResearch,
		Status: packagingIntakeStatusWaiting, ThreadID: threadID, ThreadVisibility: scoutChatVisibilityPrivate,
		AskMessageID: root.Metadata[packagingCommissionMessageIDMetadataKey], RequesterEmail: requester.Email, RequesterName: requester.Name,
		WaitingOn: requester.Email, WaitingOnName: requester.Name,
		OpenQuestions: []packagingIntakeQuestion{
			{ID: "audience", Prompt: "Who is this for?", Options: []string{"board", "customers"}, Kind: "choice"},
			{ID: "length", Prompt: "How long?", Kind: "text"},
		},
		CommissionID: root.ID, ArtifactID: root.ID,
	}
	if err := kanbanApp.savePackagingIntakeRecord(record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestPackagingCommissionViewCarriesWaitingState(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	code, payload := postPackagingCommission(t, cookies, `{"kind":"research","brief":{"scope":"market","depth":"standard","format":"report","question":"What is the Nordic mid-market worth?"}}`)
	if code != http.StatusCreated {
		t.Fatalf("commission status=%d payload=%v", code, payload)
	}
	rootID := fmt.Sprint(commissionMap(t, payload)["id"])
	// No intake yet → the brief is complete and nothing is waiting.
	get := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/commissions/"+rootID, "", cookies, packagingCommissionHandler)
	var first struct {
		Commission packagingCommissionView `json:"commission"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if !first.Commission.BriefComplete || first.Commission.WaitingOn != "" || len(first.Commission.Questions) != 0 {
		t.Fatalf("commission without intake=%+v", first.Commission.packagingCommissionWaitingState)
	}
	root, _ := kanbanApp.osArtifactByID(rootID)
	record := packagingWaitingIntakeForTest(t, root, aj)
	get = artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/commissions/"+rootID, "", cookies, packagingCommissionHandler)
	if get.Code != http.StatusOK {
		t.Fatalf("commission GET status=%d body=%s", get.Code, get.Body.String())
	}
	var waiting struct {
		Commission packagingCommissionView `json:"commission"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &waiting); err != nil {
		t.Fatal(err)
	}
	state := waiting.Commission.packagingCommissionWaitingState
	if state.IntakeID != record.ID || state.BriefComplete || state.WaitingOn != aj.Email || state.WaitingOnName != aj.Name || len(state.Questions) != 2 || state.Questions[0].ID != "audience" || len(state.Questions[0].Options) != 2 || state.Questions[1].Kind != "text" {
		t.Fatalf("waiting state=%+v", state)
	}
	for _, want := range []string{`"waitingOn":"aj@shareability.com"`, `"waitingOnName":"` + aj.Name + `"`, `"questions":[{"id":"audience","prompt":"Who is this for?","options":["board","customers"],"kind":"choice"}`, `"briefComplete":false`} {
		if !strings.Contains(get.Body.String(), want) {
			t.Fatalf("commission JSON missing %q: %s", want, get.Body.String())
		}
	}
	// Answered → brief complete, no open questions, still linked to the intake.
	record.Status = packagingIntakeStatusLaunched
	record.WaitingOn, record.WaitingOnName, record.OpenQuestions = "", "", nil
	if err := kanbanApp.savePackagingIntakeRecord(record); err != nil {
		t.Fatal(err)
	}
	get = artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/commissions/"+rootID, "", cookies, packagingCommissionHandler)
	if !strings.Contains(get.Body.String(), `"briefComplete":true`) || strings.Contains(get.Body.String(), `"waitingOn"`) {
		t.Fatalf("answered intake still waiting: %s", get.Body.String())
	}
	// The visibility fence: a viewer who cannot read the intake's private
	// thread never sees the waiting state.
	record.Status = packagingIntakeStatusWaiting
	record.WaitingOn, record.WaitingOnName = aj.Email, aj.Name
	if err := kanbanApp.savePackagingIntakeRecord(record); err != nil {
		t.Fatal(err)
	}
	joel := accountStore().findUser("joel@shareability.com")
	if fenced := kanbanApp.packagingCommissionWaitingStateFor(context.Background(), joel, root); fenced.IntakeID != "" || fenced.WaitingOn != "" || !fenced.BriefComplete {
		t.Fatalf("waiting state leaked across the thread fence: %+v", fenced)
	}
}

func TestStudioProjectCommissionRefCarriesWaitingState(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	code, payload := postPackagingCommission(t, cookies, `{"kind":"research","brief":{"scope":"technical","depth":"brief","format":"memo","question":"What is the Nordic platform landscape?"}}`)
	if code != http.StatusCreated {
		t.Fatalf("commission status=%d payload=%v", code, payload)
	}
	rootID := fmt.Sprint(commissionMap(t, payload)["id"])
	root, _ := kanbanApp.osArtifactByID(rootID)
	record := packagingWaitingIntakeForTest(t, root, aj)
	for _, target := range []string{"/api/studio-projects/v1?id=" + rootID, "/api/studio-projects/v1"} {
		list := artifactAuthorizationRequest(t, http.MethodGet, target, "", cookies, studioProjectsHandler)
		if list.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", target, list.Code, list.Body.String())
		}
		var rows struct {
			Project  studioProjectView   `json:"project"`
			Projects []studioProjectView `json:"projects"`
		}
		if err := json.Unmarshal(list.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		row := rows.Project
		for _, candidate := range rows.Projects {
			if candidate.ID == rootID {
				row = candidate
			}
		}
		if row.Commission == nil || row.Commission.IntakeID != record.ID || row.Commission.BriefComplete || row.Commission.WaitingOn != aj.Email || row.Commission.WaitingOnName != aj.Name || len(row.Commission.Questions) != 2 {
			t.Fatalf("%s commission ref=%+v", target, row.Commission)
		}
		if !strings.Contains(list.Body.String(), `"waitingOnName":"`+aj.Name+`"`) || !strings.Contains(list.Body.String(), `"questions":[{"id":"audience"`) {
			t.Fatalf("%s JSON lacks the waiting state: %s", target, list.Body.String())
		}
	}
}

// A Story Studio outline is the private thread's own material. Minting it with
// the Document Studio default (organization-visible) let every signed-in
// member read the brief and rewrite the outline through
// /assistant/packaging/stories — the outline carries the same fence as the
// commission roots that share its conversation.
func TestPackagingStoryOutlineIsPrivateToItsThreadOwner(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	kanbanApp.apiKey = ""
	story := createStoryForTest(t, cookies, `{"brief":{"subject":"Series B narrative","audience":"investors","thesis":"We compound"}}`)
	entry, ok := kanbanApp.osArtifactByID(story.ID)
	if !ok {
		t.Fatalf("story %q missing", story.ID)
	}
	if entry.Metadata["visibility"] != scoutChatVisibilityPrivate || normalizeAccountEmail(entry.Metadata["ownerEmail"]) != normalizeAccountEmail(aj.Email) ||
		entry.Metadata["originSurface"] != "chat:"+story.ThreadID {
		t.Fatalf("story scope=%q/%q/%q, want chat:%s/private/%s", entry.Metadata["originSurface"], entry.Metadata["visibility"], entry.Metadata["ownerEmail"], story.ThreadID, aj.Email)
	}
	joelCookies := loginAs(t, "joel@shareability.com", "B0NFIRE!")
	joel := accountStore().findUser("joel@shareability.com")
	for _, view := range kanbanApp.listPackagingStories(context.Background(), joel) {
		if view.ID == story.ID {
			t.Fatalf("another member's story listed for %s: %+v", joel.Email, view)
		}
	}
	list := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/stories", "", joelCookies, packagingStoriesHandler)
	if strings.Contains(list.Body.String(), story.ID) || strings.Contains(list.Body.String(), "Series B narrative") {
		t.Fatalf("story leaked into another member's list: %s", list.Body.String())
	}
	get := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/stories/"+story.ID, "", joelCookies, packagingStoryHandler)
	if get.Code != http.StatusNotFound {
		t.Fatalf("another member read the outline: status=%d body=%s", get.Code, get.Body.String())
	}
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/packaging/stories/"+story.ID, `{"outline":"# Joel's rewrite","expectedVersion":1}`, joelCookies, packagingStoryHandler)
	if patch.Code == http.StatusOK {
		t.Fatalf("another member rewrote the outline: %s", patch.Body.String())
	}
	deck := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packaging/stories/"+story.ID+"/deck", "", joelCookies, packagingStoryHandler)
	if deck.Code != http.StatusNotFound {
		t.Fatalf("another member built a deck from the outline: status=%d", deck.Code)
	}
	if after, _ := kanbanApp.osArtifactByID(story.ID); artifactVersion(after) != 1 {
		t.Fatalf("outline version after the refused writes=%d, want 1", artifactVersion(after))
	}
	// The owner still reads and workshops their own outline.
	if own := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/stories/"+story.ID, "", cookies, packagingStoryHandler); own.Code != http.StatusOK {
		t.Fatalf("owner GET status=%d body=%s", own.Code, own.Body.String())
	}
}

// The replay stamp a commission is adopted by is written only at the END of a
// launch. Two overlapping posts of the same operation must still mint ONE
// commission — not two roots in two conversations.
func TestPackagingCommissionConcurrentLaunchesMintOneCommission(t *testing.T) {
	_, aj := setupPackagingStudioTest(t)
	brief := packagingBrief{Kind: packagingCommissionKindResearch, Research: &packagingResearchBrief{
		Scope: "market", Depth: "standard", Format: "report", Question: "How big is the Nordic mid-market?",
	}}
	before := len(kanbanApp.memory.studioProjectProjectionSnapshot())
	type launch struct {
		result packagingLaunchResult
		err    error
	}
	results := make(chan launch, 2)
	for index := 0; index < 2; index++ {
		go func() {
			result, err := kanbanApp.launchPackagingCommission(context.Background(), aj, brief, "", "op-concurrent-1")
			results <- launch{result: result, err: err}
		}()
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("launch errors: %v / %v", first.err, second.err)
	}
	if first.result.Launched.Artifact.ID != second.result.Launched.Artifact.ID || first.result.Thread.ID != second.result.Thread.ID {
		t.Fatalf("two commissions for one operation: %s in %s vs %s in %s",
			first.result.Launched.Artifact.ID, first.result.Thread.ID, second.result.Launched.Artifact.ID, second.result.Thread.ID)
	}
	if after := len(kanbanApp.memory.studioProjectProjectionSnapshot()); after != before+1 {
		t.Fatalf("studio directory grew by %d, want 1", after-before)
	}
	roots := 0
	for _, entry := range kanbanApp.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		if strings.TrimSpace(entry.Metadata[packagingCommissionOperationMetadataKey]) == "op-concurrent-1" {
			roots++
		}
	}
	if roots != 1 {
		t.Fatalf("commission roots stamped with the operation=%d, want 1", roots)
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(aj.Email, first.result.Thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	userMessages := 0
	for _, message := range thread.Messages {
		if message.Role == "user" {
			userMessages++
		}
	}
	if userMessages != 1 {
		t.Fatalf("brief committed %d times, want once: %+v", userMessages, thread.Messages)
	}
}

// operationId is client-supplied. Adopting a prior commission by that name
// alone hands a different brief — or a different KIND — the wrong root; the
// stored body digest makes adoption an identity check.
func TestPackagingCommissionOperationReuseWithDifferentBodyConflicts(t *testing.T) {
	cookies, _ := setupPackagingStudioTest(t)
	research := `{"kind":"research","operationId":"op-shared","brief":{"scope":"market","depth":"standard","format":"report","question":"How big is the Nordic mid-market?"}}`
	code, payload := postPackagingCommission(t, cookies, research)
	if code != http.StatusCreated {
		t.Fatalf("first commission status=%d payload=%v", code, payload)
	}
	rootID := fmt.Sprint(commissionMap(t, payload)["id"])
	// The identical body still replays onto the same commission.
	code, payload = postPackagingCommission(t, cookies, research)
	if code != http.StatusCreated || fmt.Sprint(commissionMap(t, payload)["id"]) != rootID {
		t.Fatalf("identical replay status=%d payload=%v, want the same commission %s", code, payload, rootID)
	}
	before := len(kanbanApp.memory.studioProjectProjectionSnapshot())
	for name, body := range map[string]string{
		"different kind":     `{"kind":"presentation","operationId":"op-shared","brief":{"subject":"Enter the Nordics"}}`,
		"different question": `{"kind":"research","operationId":"op-shared","brief":{"scope":"market","depth":"standard","format":"report","question":"Who wins the mid-market?"}}`,
		"story door":         `{"kind":"story","operationId":"op-shared","brief":{"subject":"Nordic story"}}`,
	} {
		if code, payload = postPackagingCommission(t, cookies, body); code != http.StatusConflict {
			t.Fatalf("%s: status=%d payload=%v, want 409", name, code, payload)
		}
	}
	if after := len(kanbanApp.memory.studioProjectProjectionSnapshot()); after != before {
		t.Fatalf("a reused operationId launched %d more commissions", after-before)
	}
}

// F7's reuse check reads a digest stamp that is only written at the END of a
// launch, so the per-operation lock is the only thing keeping two doors of the
// same operationId from both passing it and minting two roots that claim one
// id. The story door and the commission door must therefore take the SAME
// mutex — when they keyed it differently, a concurrent
// {kind:"story"} + {kind:"presentation"} pair on one operationId raced past
// the 409 and one of the two commissions became unreachable by its own id.
func TestPackagingStoryDoorTakesTheSharedCommissionOperationLock(t *testing.T) {
	_, aj := setupPackagingStudioTest(t)
	kanbanApp.apiKey = ""
	brief := packagingStoryBrief{Subject: "Nordic story", Audience: "board"}
	operation, err := packagingCommissionOperation(aj, "op-both-doors", packagingBrief{Kind: packagingCommissionKindStory, Story: &brief})
	if err != nil {
		t.Fatal(err)
	}
	// Hold the lock the NON-story doors take (launchPackagingCommission's).
	unlock := kanbanApp.packagingCommissionOperationLock(aj, operation)
	done := make(chan error, 1)
	go func() {
		_, _, storyErr := kanbanApp.createPackagingStoryWithContext(context.Background(), aj, brief, "op-both-doors")
		done <- storyErr
	}()
	select {
	case storyErr := <-done:
		unlock()
		t.Fatalf("the story door ran to completion (err=%v) while the shared per-operation lock was held: the two doors key different mutexes, so F7's 409 can be raced past", storyErr)
	case <-time.After(250 * time.Millisecond):
	}
	unlock()
	select {
	case storyErr := <-done:
		if storyErr != nil {
			t.Fatalf("story create after the lock released: %v", storyErr)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the story door never completed after the shared lock released")
	}
	// And it registered no private mutex of its own for anyone to bypass it on.
	kanbanApp.mu.Lock()
	keys := make([]string, 0, len(kanbanApp.chatThreadLocks))
	for key := range kanbanApp.chatThreadLocks {
		keys = append(keys, key)
	}
	kanbanApp.mu.Unlock()
	for _, key := range keys {
		if strings.HasPrefix(key, "packaging-story-operation-") {
			t.Fatalf("the story door still keys its own per-operation mutex (%q); the commission door cannot see it", key)
		}
	}
}

// A rename sends {title, expectedVersion} and no outline. The body must
// survive: an absent field is "leave it unchanged", only an explicit
// "outline":"" clears it.
func TestPackagingStoryRenameKeepsTheOutline(t *testing.T) {
	cookies, _ := setupPackagingStudioTest(t)
	kanbanApp.apiKey = ""
	story := createStoryForTest(t, cookies, `{"brief":{"subject":"Why now for compute credits","audience":"CFOs","thesis":"Credits beat discounts"}}`)
	rename := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/packaging/stories/"+story.ID, `{"title":"Series B story","expectedVersion":1}`, cookies, packagingStoryHandler)
	if rename.Code != http.StatusOK {
		t.Fatalf("rename status=%d body=%s", rename.Code, rename.Body.String())
	}
	var renamed struct {
		Story packagingStoryView `json:"story"`
	}
	if err := json.Unmarshal(rename.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Story.Title != "Series B story" {
		t.Fatalf("title=%q", renamed.Story.Title)
	}
	if renamed.Story.Outline != story.Outline {
		t.Fatalf("the rename rewrote the body:\n%s", renamed.Story.Outline)
	}
	if renamed.Story.Doc == nil || len(renamed.Story.Doc.Beats) < packagingStoryMinBeats || renamed.Story.Doc.Thesis != "Credits beat discounts" {
		t.Fatalf("the rename lost the outline structure: %+v", renamed.Story.Doc)
	}
	// An explicit empty outline is still an explicit clear.
	cleared := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/packaging/stories/"+story.ID,
		fmt.Sprintf(`{"outline":"","expectedVersion":%d}`, renamed.Story.Version), cookies, packagingStoryHandler)
	if cleared.Code != http.StatusOK {
		t.Fatalf("explicit clear status=%d body=%s", cleared.Code, cleared.Body.String())
	}
	var emptied struct {
		Story packagingStoryView `json:"story"`
	}
	if err := json.Unmarshal(cleared.Body.Bytes(), &emptied); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(emptied.Story.Outline) != "" {
		t.Fatalf("explicit clear kept the body: %q", emptied.Story.Outline)
	}
}

// A PATCH workshop turn speaks in the story's private conversation and spends
// a provider call. It takes the same fence the chat door takes: only the owner
// of the bound thread may run one.
func TestPackagingStoryWorkshopPatchRefusesAnotherMember(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	kanbanApp.apiKey = ""
	story := createStoryForTest(t, cookies, `{"brief":{"subject":"Owner only","audience":"board"}}`)
	joelCookies := loginAs(t, "joel@shareability.com", "B0NFIRE!")
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/packaging/stories/"+story.ID, `{"message":"cut the objection beat","expectedVersion":1}`, joelCookies, packagingStoryHandler)
	if patch.Code != http.StatusNotFound && patch.Code != http.StatusForbidden {
		t.Fatalf("another member workshopped the outline: status=%d body=%s", patch.Code, patch.Body.String())
	}
	if after, _ := kanbanApp.osArtifactByID(story.ID); artifactVersion(after) != 1 {
		t.Fatalf("a refused workshop turn journaled a version: v%d", artifactVersion(after))
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(aj.Email, story.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Messages) != 1 {
		t.Fatalf("a refused workshop turn wrote into the owner's thread: %+v", thread.Messages)
	}
	// The owner's own turn still works and lands on the thread.
	own := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/packaging/stories/"+story.ID, `{"message":"cut the objection beat","expectedVersion":1}`, cookies, packagingStoryHandler)
	if own.Code != http.StatusOK {
		t.Fatalf("owner workshop status=%d body=%s", own.Code, own.Body.String())
	}
	if saved, _, _ := kanbanApp.scoutChatThreadByID(aj.Email, story.ThreadID); len(saved.Messages) != 3 {
		t.Fatalf("owner turn did not commit into the thread: %d messages", len(saved.Messages))
	}
}

// The commission poll and the studio list both advance a waiting chain. The
// waiting-check → launch → launched-stamp window must launch ONE deck.
func TestPackagingCommissionChainAdvancesOnceUnderConcurrentReads(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	code, payload := postPackagingCommission(t, cookies, `{"kind":"presentation","brief":{"subject":"Enter the Nordics","audience":"exec team","research":{"commissionFirst":true,"brief":{"scope":"market","depth":"standard","format":"report","question":"How big is the Nordic mid-market?"}}}}`)
	if code != http.StatusCreated {
		t.Fatalf("chain commission status=%d payload=%v", code, payload)
	}
	researchID := fmt.Sprint(commissionMap(t, payload)["id"])
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(researchID, "", "# Nordic mid-market\n\n## Executive Summary\n\nBig.", "Scout", map[string]string{"status": artifactStatusComplete, "threadStatus": artifactStatusComplete}); err != nil {
		t.Fatal(err)
	}
	root, _ := kanbanApp.osArtifactByID(researchID)
	before := len(kanbanApp.memory.studioProjectProjectionSnapshot())
	done := make(chan bool, 2)
	for index := 0; index < 2; index++ {
		go func() {
			_, advanced := kanbanApp.advancePackagingCommissionChain(context.Background(), aj, root)
			done <- advanced
		}()
	}
	<-done
	<-done
	if after := len(kanbanApp.memory.studioProjectProjectionSnapshot()); after != before+1 {
		t.Fatalf("concurrent reads launched %d decks, want 1", after-before)
	}
	advanced, _ := kanbanApp.osArtifactByID(researchID)
	if advanced.Metadata[packagingChainStateMetadataKey] != packagingChainStateLaunched || advanced.Metadata[packagingChainPresentationIDMetadataKey] == "" {
		t.Fatalf("chain state=%v", advanced.Metadata[packagingChainStateMetadataKey])
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(aj.Email, advanced.Metadata[packagingCommissionThreadIDMetadataKey])
	if err != nil {
		t.Fatal(err)
	}
	deckBriefs := 0
	for _, message := range thread.Messages {
		if strings.HasPrefix(message.Text, "Commission a presentation") {
			deckBriefs++
		}
	}
	if deckBriefs != 1 {
		t.Fatalf("the deck brief was committed %d times, want once", deckBriefs)
	}
}
