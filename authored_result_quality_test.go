package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type finalExportCapabilityResponse struct {
	ArtifactID      string `json:"artifactId"`
	ArtifactVersion int    `json:"artifactVersion"`
	QualityState    string `json:"qualityState"`
	Managed         bool   `json:"managed"`
	CanExport       bool   `json:"canExport"`
}

func readFinalExportCapability(t *testing.T, cookies []*http.Cookie, artifactID string) finalExportCapabilityResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/artifacts/final-export-capability?id="+url.QueryEscape(artifactID), nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	authoredResultFinalExportCapabilityHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("capability status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response finalExportCapabilityResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func readArtifactBlob(t *testing.T, cookies []*http.Cookie, ref string) *httptest.ResponseRecorder {
	t.Helper()
	return artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/blob?ref="+url.QueryEscape(ref), "", cookies, artifactBlobHandler)
}

func TestFinalExportCapabilityDistinguishesDraftAdmittedAndUnmanagedArtifacts(t *testing.T) {
	setupAuthTestEnv(t)
	fixture := seedDocumentReportQualityFixture(t, 2)
	fixture.fileJury(t, 9.4, 2, "KEEP")
	fileAdmittedPublishedDocument(t, &fixture)

	unmanaged, _, err := fixture.app.createOSArtifactWithMetadata("research", "Ordinary report", "# Ordinary report\n\nComplete.", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "status": "complete", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	ordinaryPDFRef, err := putBlob([]byte("%PDF-1.7 ordinary report"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if unmanaged, err = fixture.app.appendArtifactAsset(unmanaged.ID, artifactAsset{Ref: ordinaryPDFRef, Mime: "application/pdf", Name: "ordinary.pdf", Kind: "pdf"}); err != nil {
		t.Fatal(err)
	}
	blockedGoal, _, err := fixture.app.createOSArtifactWithMetadata("workflow", "Blocked report goal", "goal", "AJ", map[string]string{
		"mode": "goal", "status": "needs_attention", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	blockedReport, _, err := fixture.app.createOSArtifactWithMetadata("workflow", "Blocked report", "# Blocked report\n\nWorking draft.", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "goalParentId": blockedGoal.ID, "goalSubtaskId": "write", "status": "complete", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	draftPDFRef, err := putBlob([]byte("%PDF-1.7 blocked jury draft"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if blockedReport, err = fixture.app.appendArtifactAsset(blockedReport.ID, artifactAsset{Ref: draftPDFRef, Mime: "application/pdf", Name: "blocked-draft.pdf", Kind: "pdf"}); err != nil {
		t.Fatal(err)
	}
	blockedPlan, _ := json.Marshal(goalPlan{ProcessID: documentReportProcessID, State: goalStateBlocked})
	if _, changed, updateErr := fixture.app.updateOSArtifactWithMetadata(blockedGoal.ID, "", blockedGoal.Text, "AJ", map[string]string{"goalPlan": string(blockedPlan)}); updateErr != nil || !changed {
		t.Fatalf("bind blocked plan: changed=%t err=%v", changed, updateErr)
	}

	previousApp := kanbanApp
	kanbanApp = fixture.app
	t.Cleanup(func() { kanbanApp = previousApp })
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	draft := readFinalExportCapability(t, cookies, blockedReport.ID)
	if draft.ArtifactID != blockedReport.ID || draft.QualityState != authoredResultQualityDraftNeedsAttention || !draft.Managed || draft.CanExport {
		t.Fatalf("draft capability=%+v", draft)
	}
	lock := goalEngineLock(blockedGoal.ID)
	lock.Lock()
	busyRequest := httptest.NewRequest(http.MethodGet, "/artifacts/final-export-capability?id="+url.QueryEscape(blockedReport.ID), nil)
	for _, cookie := range cookies {
		busyRequest.AddCookie(cookie)
	}
	busyRecorder := httptest.NewRecorder()
	authoredResultFinalExportCapabilityHandler(busyRecorder, busyRequest)
	lock.Unlock()
	if busyRecorder.Code != http.StatusConflict || busyRecorder.Body.String() == "" {
		t.Fatalf("busy capability status=%d body=%s", busyRecorder.Code, busyRecorder.Body.String())
	}
	lock.Lock()
	busyBlob := readArtifactBlob(t, cookies, draftPDFRef)
	lock.Unlock()
	if busyBlob.Code != http.StatusNotFound || busyBlob.Header().Get("ETag") != "" {
		t.Fatalf("busy draft blob status=%d headers=%v body=%s", busyBlob.Code, busyBlob.Header(), busyBlob.Body.String())
	}
	admitted := readFinalExportCapability(t, cookies, fixture.report.ID)
	if admitted.ArtifactID != fixture.report.ID || admitted.QualityState != authoredResultQualityAdmitted || !admitted.Managed || !admitted.CanExport {
		t.Fatalf("admitted capability=%+v", admitted)
	}
	ordinary := readFinalExportCapability(t, cookies, unmanaged.ID)
	if ordinary.ArtifactID != unmanaged.ID || ordinary.QualityState != "" || ordinary.Managed || !ordinary.CanExport {
		t.Fatalf("unmanaged capability=%+v", ordinary)
	}

	if draftBlob := readArtifactBlob(t, cookies, draftPDFRef); draftBlob.Code != http.StatusNotFound || draftBlob.Header().Get("ETag") != "" {
		t.Fatalf("managed draft PDF leaked status=%d headers=%v body=%s", draftBlob.Code, draftBlob.Header(), draftBlob.Body.String())
	}
	admittedPDFRef := fixture.report.Metadata[renderPDFAssetRefMetadataKey]
	if admittedBlob := readArtifactBlob(t, cookies, admittedPDFRef); admittedBlob.Code != http.StatusOK || admittedBlob.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("admitted PDF status=%d headers=%v body=%s", admittedBlob.Code, admittedBlob.Header(), admittedBlob.Body.String())
	}
	if ordinaryBlob := readArtifactBlob(t, cookies, ordinaryPDFRef); ordinaryBlob.Code != http.StatusOK || ordinaryBlob.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("unmanaged PDF status=%d headers=%v body=%s", ordinaryBlob.Code, ordinaryBlob.Header(), ordinaryBlob.Body.String())
	}
	previousAfterReadProbe := artifactBlobAfterReadProbe
	defer func() { artifactBlobAfterReadProbe = previousAfterReadProbe }()
	artifactBlobAfterReadProbe = func(ref string) {
		if ref == ordinaryPDFRef {
			if _, _, updateErr := fixture.app.memory.updateOSArtifactMetadata(unmanaged.ID, map[string]string{artifactAssetsMetadataKey: "[]"}); updateErr != nil {
				t.Fatalf("remove raced asset: %v", updateErr)
			}
		}
	}
	racedBlob := readArtifactBlob(t, cookies, ordinaryPDFRef)
	artifactBlobAfterReadProbe = previousAfterReadProbe
	if racedBlob.Code != http.StatusNotFound || racedBlob.Header().Get("ETag") != "" || racedBlob.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("removed PDF raced through status=%d headers=%v body=%s", racedBlob.Code, racedBlob.Header(), racedBlob.Body.String())
	}
}

func TestFinalExportCapabilityRequiresReadAndExportAuthority(t *testing.T) {
	cookies, artifact, _ := setupArtifactAuthorizationSlice(t)
	pdfRef, err := putBlob([]byte("%PDF-1.7 denied export"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if artifact, err = kanbanApp.appendArtifactAsset(artifact.ID, artifactAsset{Ref: pdfRef, Mime: "application/pdf", Name: "denied.pdf", Kind: "pdf"}); err != nil {
		t.Fatal(err)
	}
	imageRef, err := putBlob([]byte("image bytes"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if artifact, err = kanbanApp.appendArtifactAsset(artifact.ID, artifactAsset{Ref: imageRef, Mime: "image/png", Name: "editable.png", Kind: "image"}); err != nil {
		t.Fatal(err)
	}
	authorizer := &surfaceRecordingArtifactAuthorizer{allow: func(action ACLAction, _ meetingMemoryEntry) bool {
		return action != ACLExport
	}}
	installRecordingArtifactAuthorizer(t, authorizer)
	response := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/final-export-capability?id="+url.QueryEscape(artifact.ID), "", cookies, authoredResultFinalExportCapabilityHandler)
	if response.Code != http.StatusNotFound || !authorizer.saw(ACLReadContent, artifact.ID) || !authorizer.saw(ACLExport, artifact.ID) || response.Body.String() == "" {
		t.Fatalf("denied export capability status=%d body=%s calls=%+v", response.Code, response.Body.String(), authorizer.calls)
	}
	blobResponse := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/blob?ref="+url.QueryEscape(pdfRef), "", cookies, artifactBlobHandler)
	if blobResponse.Code != http.StatusNotFound || blobResponse.Header().Get("ETag") != "" || !authorizer.saw(ACLExport, artifact.ID) {
		t.Fatalf("denied artifact PDF status=%d headers=%v body=%s calls=%+v", blobResponse.Code, blobResponse.Header(), blobResponse.Body.String(), authorizer.calls)
	}
	imageAuthorizer := &surfaceRecordingArtifactAuthorizer{allow: func(action ACLAction, _ meetingMemoryEntry) bool {
		return action != ACLExport
	}}
	artifactObjectAuthorizer = imageAuthorizer
	imageResponse := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/blob?ref="+url.QueryEscape(imageRef), "", cookies, artifactBlobHandler)
	if imageResponse.Code != http.StatusOK || imageResponse.Header().Get("Content-Type") != "image/png" || imageAuthorizer.saw(ACLExport, artifact.ID) {
		t.Fatalf("read-only image status=%d headers=%v body=%s calls=%+v", imageResponse.Code, imageResponse.Header(), imageResponse.Body.String(), imageAuthorizer.calls)
	}
}

func TestGenericArtifactPatchCannotPublishManagedAuthoredResult(t *testing.T) {
	cookies, artifact, _ := setupArtifactAuthorizationSlice(t)
	goal, _, err := kanbanApp.createOSArtifactWithMetadata("workflow", "Managed goal", "goal", "AJ", map[string]string{
		"mode": "goal", "status": "complete", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, changed, err := kanbanApp.memory.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "AJ", map[string]string{
		"goalParentId": goal.ID, "goalDeliverable": "true",
	})
	if err != nil || !changed {
		t.Fatalf("bind managed artifact: changed=%t err=%v", changed, err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": artifact.ID, "title": "Bypass", "text": "unreviewed replacement", "published": true,
	})
	response := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts", string(body), cookies, artifactsHandler)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "review stage") {
		t.Fatalf("managed generic publish status=%d body=%s", response.Code, response.Body.String())
	}
	stored := mustArtifact(t, kanbanApp, artifact.ID)
	if stored.Text != artifact.Text || strings.EqualFold(stored.Metadata["published"], "true") {
		t.Fatalf("managed generic publish mutated artifact: text=%q metadata=%+v", stored.Text, stored.Metadata)
	}
}

func TestBlobFinalExportClassificationUsesImmutableMIMEAndEveryDuplicateDeclaration(t *testing.T) {
	cookies, artifact, _ := setupArtifactAuthorizationSlice(t)
	pdfRef, err := putBlob([]byte("%PDF-1.7 immutable classification"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		name   string
		assets []artifactAsset
	}{
		{
			name: "mislabeled image still follows pdf sidecar",
			assets: []artifactAsset{{
				Ref: pdfRef, Mime: "image/png", Name: "misleading.png", Kind: "image",
			}},
		},
		{
			name: "later duplicate pdf declaration cannot be shadowed",
			assets: []artifactAsset{
				{Ref: pdfRef, Mime: "image/png", Name: "first.png", Kind: "image"},
				{Ref: pdfRef, Mime: "application/pdf", Name: "final.pdf", Kind: "pdf"},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			raw, marshalErr := json.Marshal(testCase.assets)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if _, _, updateErr := kanbanApp.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{artifactAssetsMetadataKey: string(raw)}); updateErr != nil {
				t.Fatal(updateErr)
			}
			authorizer := &surfaceRecordingArtifactAuthorizer{allow: func(action ACLAction, _ meetingMemoryEntry) bool {
				return action != ACLExport
			}}
			previous := artifactObjectAuthorizer
			artifactObjectAuthorizer = authorizer
			response := readArtifactBlob(t, cookies, pdfRef)
			artifactObjectAuthorizer = previous
			if response.Code != http.StatusNotFound || !authorizer.saw(ACLExport, artifact.ID) || response.Header().Get("ETag") != "" {
				t.Fatalf("classification status=%d headers=%v body=%s calls=%+v", response.Code, response.Header(), response.Body.String(), authorizer.calls)
			}
		})
	}
}

func TestFinalExportAdmissionFailsClosedWhenJuryEvidenceMovesDuringEvaluation(t *testing.T) {
	setupAuthTestEnv(t)
	fixture := seedDocumentReportQualityFixture(t, 2)
	fixture.fileJury(t, 9.4, documentReportMinimumJurySeats, "KEEP")
	fileAdmittedPublishedDocument(t, &fixture)
	juryStage := fixture.plan.subtaskByID(documentReportJuryStageID)
	if juryStage == nil || juryStage.ArtifactID == "" {
		t.Fatal("document jury stage is missing")
	}
	juryRecord := mustArtifact(t, fixture.app, juryStage.ArtifactID)
	juryID := juryRecord.Metadata["documentJuryArtifactId"]
	jury := mustArtifact(t, fixture.app, juryID)
	previousApp := kanbanApp
	kanbanApp = fixture.app
	t.Cleanup(func() { kanbanApp = previousApp })
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	previousProbe := authoredAdmissionAfterQualityProbe
	mutated := false
	authoredAdmissionAfterQualityProbe = func() {
		if mutated {
			return
		}
		mutated = true
		if _, _, err := fixture.app.updateOSArtifact(jury.ID, jury.Metadata["title"], jury.Text+"\nTAMPERED DURING ADMISSION", "AJ"); err != nil {
			t.Fatalf("mutate jury during admission: %v", err)
		}
	}
	t.Cleanup(func() { authoredAdmissionAfterQualityProbe = previousProbe })
	request := httptest.NewRequest(http.MethodGet, "/artifacts/final-export-capability?id="+url.QueryEscape(fixture.report.ID), nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	authoredResultFinalExportCapabilityHandler(recorder, request)
	authoredAdmissionAfterQualityProbe = previousProbe
	if !mutated || recorder.Code != http.StatusConflict || recorder.Header().Get("ETag") != "" {
		t.Fatalf("jury race capability status=%d headers=%v body=%s mutated=%t", recorder.Code, recorder.Header(), recorder.Body.String(), mutated)
	}
	if leaked := readArtifactBlob(t, cookies, fixture.report.Metadata[renderPDFAssetRefMetadataKey]); leaked.Code != http.StatusNotFound || leaked.Header().Get("ETag") != "" {
		t.Fatalf("jury-raced PDF leaked status=%d headers=%v body=%s", leaked.Code, leaked.Header(), leaked.Body.String())
	}
}

func TestFinalExportAdmissionFailsClosedWhenJuryMetadataMovesDuringEvaluation(t *testing.T) {
	setupAuthTestEnv(t)
	fixture := seedDocumentReportQualityFixture(t, 2)
	fixture.fileJury(t, 9.4, documentReportMinimumJurySeats, "KEEP")
	fileAdmittedPublishedDocument(t, &fixture)
	juryStage := fixture.plan.subtaskByID(documentReportJuryStageID)
	if juryStage == nil || juryStage.ArtifactID == "" {
		t.Fatal("document jury stage is missing")
	}
	juryRecord := mustArtifact(t, fixture.app, juryStage.ArtifactID)
	previousApp := kanbanApp
	kanbanApp = fixture.app
	t.Cleanup(func() { kanbanApp = previousApp })
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	previousProbe := authoredAdmissionAfterQualityProbe
	mutated := false
	authoredAdmissionAfterQualityProbe = func() {
		if mutated {
			return
		}
		mutated = true
		if _, _, err := fixture.app.memory.updateOSArtifactMetadata(juryRecord.ID, map[string]string{"reviewVerdict": "needs_attention"}); err != nil {
			t.Fatalf("mutate jury metadata during admission: %v", err)
		}
	}
	t.Cleanup(func() { authoredAdmissionAfterQualityProbe = previousProbe })
	request := httptest.NewRequest(http.MethodGet, "/artifacts/final-export-capability?id="+url.QueryEscape(fixture.report.ID), nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	authoredResultFinalExportCapabilityHandler(recorder, request)
	authoredAdmissionAfterQualityProbe = previousProbe
	if !mutated || recorder.Code != http.StatusConflict || recorder.Header().Get("ETag") != "" {
		t.Fatalf("jury metadata race capability status=%d headers=%v body=%s mutated=%t", recorder.Code, recorder.Header(), recorder.Body.String(), mutated)
	}
	if leaked := readArtifactBlob(t, cookies, fixture.report.Metadata[renderPDFAssetRefMetadataKey]); leaked.Code != http.StatusNotFound || leaked.Header().Get("ETag") != "" {
		t.Fatalf("jury-metadata-raced PDF leaked status=%d headers=%v body=%s", leaked.Code, leaked.Header(), leaked.Body.String())
	}
}
