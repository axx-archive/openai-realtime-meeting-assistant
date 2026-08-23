package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReviewChangesCarriesExactEditedDeckAssetsIntoDraftCompile(t *testing.T) {
	fixture := newPackagingQualityGateFixture(t, "ready", []slideJuryRepair{})
	var scorerCalls atomic.Int32
	fixture.runQualityGate(t, packagingQualityScoreJSON(9.4, "ready"), &scorerCalls)
	if scorerCalls.Load() != 0 {
		t.Fatalf("rendered quality admission called a second scorer %d time(s)", scorerCalls.Load())
	}
	ship := fixture.plan.subtaskByID("ship_compile")
	ship.Status = subtaskRunning
	shipStage := packagingStudioStage(t, fixture.def, "ship_compile")
	body, metadata, err := compilePackagingStudioShip(fixture.app, &fixture.plan, fixture.parentID, shipStage)
	if err != nil {
		t.Fatal(err)
	}
	fixture.engine.completeProcessStage(&fixture.plan, fixture.parentID, ship, shipStage, body, "published fixture", metadata)
	fixture.plan.State = goalStateVerified
	fixture.plan.Report.DeliverableArtifactID = fixture.deck.ID
	bindGoalAcceptedResult(&fixture.plan, fixture.deck)
	parent := mustArtifact(t, fixture.app, fixture.parentID)
	if persisted := fixture.engine.persist(&fixture.plan, fixture.parentID, parent.Text); persisted.ID == "" {
		t.Fatal("verified fixture did not persist")
	}

	previousApp := kanbanApp
	kanbanApp = fixture.app
	t.Cleanup(func() { kanbanApp = previousApp })
	adminCookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	admittedDeck, imported, quality, err := loadDeckDocument(fixture.deck)
	if err != nil || imported || quality != "native" {
		t.Fatalf("load admitted deck imported=%t quality=%q err=%v", imported, quality, err)
	}
	copyPayload, _ := json.Marshal(map[string]any{
		"artifactId": fixture.deck.ID, "expectedVersion": artifactVersion(fixture.deck),
		"title": "Edited imagery review", "fileName": "Edited imagery review", "folderId": "", "deck": admittedDeck,
	})
	copyResponse := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/deck/copies", string(copyPayload), adminCookies, deckEditorCopyHandler)
	if copyResponse.Code != http.StatusCreated {
		t.Fatalf("create review copy status=%d body=%s", copyResponse.Code, copyResponse.Body.String())
	}
	var copied struct {
		Artifact deckArtifactView `json:"artifact"`
	}
	if err := json.Unmarshal(copyResponse.Body.Bytes(), &copied); err != nil || copied.Artifact.ID == "" {
		t.Fatalf("decode review copy: err=%v body=%s", err, copyResponse.Body.String())
	}

	imageBytes := append([]byte("\x89PNG\r\n\x1a\n"), []byte("reviewed studio image")...)
	var uploadBody bytes.Buffer
	writer := multipart.NewWriter(&uploadBody)
	_ = writer.WriteField("artifactId", copied.Artifact.ID)
	_ = writer.WriteField("expectedVersion", strconv.Itoa(copied.Artifact.Version))
	_ = writer.WriteField("slideId", admittedDeck.Slides[0].ID)
	_ = writer.WriteField("placement", "image")
	file, err := writer.CreateFormFile("file", "review-image.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(imageBytes)
	_ = writer.Close()
	uploadRequest := httptest.NewRequest(http.MethodPost, "/artifacts/deck/assets", &uploadBody)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	for _, cookie := range adminCookies {
		uploadRequest.AddCookie(cookie)
	}
	uploadResponse := httptest.NewRecorder()
	deckEditorAssetUploadHandler(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload edited image status=%d body=%s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var uploaded struct {
		Artifact deckArtifactView `json:"artifact"`
		Element  deckElement      `json:"element"`
	}
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &uploaded); err != nil || !validBlobRef(uploaded.Element.Ref) {
		t.Fatalf("decode edited image: err=%v body=%s", err, uploadResponse.Body.String())
	}

	previousStart := startGoalFeedbackResumeAsync
	startGoalFeedbackResumeAsync = func(func()) {}
	t.Cleanup(func() { startGoalFeedbackResumeAsync = previousStart })
	reviewResponse := postArtifactAction(t, adminCookies, `{"id":`+strconv.Quote(fixture.parentID)+`,"action":"review_changes","resultArtifactId":`+strconv.Quote(copied.Artifact.ID)+`}`)
	if reviewResponse.Code != http.StatusAccepted {
		t.Fatalf("review changes status=%d body=%s", reviewResponse.Code, reviewResponse.Body.String())
	}
	reopened := mustGoalPlan(t, fixture.app, fixture.parentID)
	producer := reopened.subtaskByID("ship_deck")
	if producer == nil || producer.ArtifactID != copied.Artifact.ID || producer.Review == nil || producer.Review.ArtifactVersion != uploaded.Artifact.Version || producer.Review.ArtifactDigest == "" || producer.Review.ArtifactSceneRef != uploaded.Artifact.SceneRef {
		t.Fatalf("review did not bind exact edited revision: %+v", producer)
	}

	render := reopened.subtaskByID("draft_compile")
	render.Status = subtaskRunning
	engine := newGoalEngine(fixture.app)
	if err := engine.prepareGoalRoute(&reopened, fixture.parentID); err != nil {
		t.Fatal(err)
	}
	engine.runProcessCompileStage(&reopened, fixture.parentID, render, packagingStudioStage(t, fixture.def, "draft_compile"))
	if render.Status != subtaskComplete {
		t.Fatalf("edited draft compile failed: %+v review=%+v", render, render.Review)
	}
	renderRecord := mustArtifact(t, fixture.app, render.ArtifactID)
	candidate := mustArtifact(t, fixture.app, renderRecord.Metadata["deckArtifactId"])
	if candidate.ID == fixture.deck.ID {
		t.Fatal("edited review overwrote the previously admitted deck")
	}
	foundExactAsset := false
	for _, asset := range artifactAssets(candidate) {
		if asset.Ref == uploaded.Element.Ref && asset.Mime == "image/png" && artifactAssetIsEditableImage(asset) {
			foundExactAsset = true
		}
	}
	if !foundExactAsset {
		t.Fatalf("compiled candidate dropped edited image %s: %+v", uploaded.Element.Ref, artifactAssets(candidate))
	}
	compiledDeck, imported, quality, err := loadDeckDocument(candidate)
	if err != nil || imported || quality != "native" {
		t.Fatalf("compiled candidate scene imported=%t quality=%q err=%v", imported, quality, err)
	}
	imageInScene := false
	for _, element := range compiledDeck.Slides[0].Elements {
		if element.Type == "image" && element.Ref == uploaded.Element.Ref {
			imageInScene = true
		}
	}
	if !imageInScene {
		t.Fatalf("compiled candidate scene dropped edited image %s", uploaded.Element.Ref)
	}
	renderBody, err := artifactRenderBody(candidate)
	if err != nil || !strings.Contains(string(renderBody), "data:image/png;base64,") {
		t.Fatalf("compiled candidate could not prepare exact image for render: err=%v", err)
	}

	// The receipt is a snapshot, not a pointer to whatever the editor saves
	// next. A post-submit mutation must fail before another candidate is filed.
	currentSource := mustArtifact(t, fixture.app, copied.Artifact.ID)
	if _, _, err := fixture.app.updateOSArtifact(currentSource.ID, "", currentSource.Text+"\n<!-- changed after review request -->", "AJ"); err != nil {
		t.Fatal(err)
	}
	render.Status = subtaskRunning
	engine.runProcessCompileStage(&reopened, fixture.parentID, render, packagingStudioStage(t, fixture.def, "draft_compile"))
	if render.Status != subtaskFailed || render.Review == nil || !strings.Contains(render.Review.Reasons, "changed after review was requested") {
		t.Fatalf("post-review source mutation did not fail closed: %+v", render)
	}
}
