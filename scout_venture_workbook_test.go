package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readWorkbookParts(t *testing.T, payload []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("open xlsx: %v", err)
	}
	parts := map[string]string{}
	for _, file := range reader.File {
		source, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		data, err := io.ReadAll(source)
		_ = source.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		parts[file.Name] = string(data)
	}
	return parts
}

func TestVentureWorkbookGeneratorIsDeterministicFormulaDrivenAndFactBlank(t *testing.T) {
	first, preview, err := buildVentureWorkbookXLSX(`Series A model for <Nimbus & Co>`)
	if err != nil {
		t.Fatalf("build first: %v", err)
	}
	second, secondPreview, err := buildVentureWorkbookXLSX(`Series A model for <Nimbus & Co>`)
	if err != nil {
		t.Fatalf("build second: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same request produced different XLSX bytes")
	}
	if !reflect.DeepEqual(preview, secondPreview) || preview.SheetCount != 5 || preview.FormulaCount != 63 {
		t.Fatalf("preview=%+v second=%+v", preview, secondPreview)
	}
	parts := readWorkbookParts(t, first)
	for _, required := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/styles.xml", "xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet5.xml"} {
		if _, ok := parts[required]; !ok {
			t.Errorf("missing OOXML part %s", required)
		}
	}
	for name, part := range parts {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		decoder := xml.NewDecoder(strings.NewReader(part))
		for {
			_, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("OOXML part %s is malformed: %v", name, err)
			}
		}
	}
	if !strings.Contains(parts["xl/workbook.xml"], `name="Operating Model"`) || !strings.Contains(parts["xl/worksheets/sheet3.xml"], `<f>B2*(1+Assumptions!B3)</f>`) {
		t.Fatal("workbook lacks named operating model/formula contract")
	}
	formulaCount := 0
	for name, part := range parts {
		if strings.HasPrefix(name, "xl/worksheets/") {
			formulaCount += strings.Count(part, "<f>")
		}
	}
	if formulaCount != preview.FormulaCount {
		t.Fatalf("preview formulaCount=%d actual=%d", preview.FormulaCount, formulaCount)
	}
	if strings.Contains(parts["xl/worksheets/sheet1.xml"], "<Nimbus & Co>") || !strings.Contains(parts["xl/worksheets/sheet1.xml"], "&lt;Nimbus &amp; Co&gt;") {
		t.Fatal("request text was not XML escaped")
	}
	assumptions := parts["xl/worksheets/sheet2.xml"]
	for _, input := range []string{`r="B2"`, `r="B3"`, `r="B4"`, `r="B5"`, `r="B6"`, `r="B7"`, `r="B8"`} {
		index := strings.Index(assumptions, input)
		if index < 0 {
			t.Fatalf("missing input cell %s", input)
		}
		window := assumptions[index:min(len(assumptions), index+24)]
		if !strings.Contains(window, "/>") {
			t.Fatalf("input cell %s is not an empty OOXML cell: %s", input, window)
		}
	}
	joined := strings.Join([]string{parts["xl/workbook.xml"], parts["_rels/.rels"], parts["xl/_rels/workbook.xml.rels"]}, "\n")
	for _, forbidden := range []string{"externalLink", "vbaProject", "hyperlink", "http://evil"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("workbook contains forbidden relationship %q", forbidden)
		}
	}
}

func TestVentureWorkbookOpensInSpreadsheetEngine(t *testing.T) {
	soffice, err := exec.LookPath("soffice")
	if err != nil {
		t.Skip("LibreOffice is not installed in this test environment")
	}
	payload, _, err := buildVentureWorkbookXLSX("Engine compatibility check")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "venture-workbook.xlsx")
	if err := os.WriteFile(input, payload, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, soffice, "-env:UserInstallation=file://"+filepath.Join(dir, "profile"), "--headless", "--convert-to", "csv", "--outdir", dir, input)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("LibreOffice rejected workbook: %v: %s", err, output)
	}
	csv, err := os.ReadFile(filepath.Join(dir, "venture-workbook.csv"))
	if err != nil {
		t.Fatalf("LibreOffice produced no CSV: %v", err)
	}
	if !strings.Contains(string(csv), "Venture workbook") || !strings.Contains(string(csv), "Engine compatibility check") {
		t.Fatalf("converted first sheet missing expected cells: %s", csv)
	}
}

func TestPrivateScoutVentureWorkbookLifecycleCustodyDriveAndNoProvider(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed owner missing")
	}
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Venture model", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	priorRunner := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {
		t.Fatal("deterministic workbook must not launch a provider worker")
	}
	t.Cleanup(func() { startAgentThreadAsync = priorRunner })
	response, err := app.appendScoutChatThreadMessageWithReplyAndTool(ventureWorkbookContext("workbook-operation-0001"), user, thread.ID, "Nimbus Series A operating model", nil, "", "", ventureWorkbookToolID)
	if err != nil {
		t.Fatalf("run workbook: %v", err)
	}
	if response["providerCalls"] != 0 || response["providerExecutionFenced"] != true || response["executionBridge"] != "deterministic_private_venture_workbook_v1" {
		t.Fatalf("execution receipt=%#v", response)
	}
	agent, ok := response["agentThread"].(scoutAgentThread)
	if !ok {
		t.Fatalf("agentThread type=%T", response["agentThread"])
	}
	artifact := agent.Artifact
	if artifactType(artifact) != artifactTypeWorkbook || artifact.Metadata["status"] != artifactStatusComplete || artifact.Metadata["published"] != "false" || !artifactPublicationDisabled(artifact) {
		t.Fatalf("artifact lifecycle/type=%#v", artifact.Metadata)
	}
	beforePublish, err := os.ReadFile(meetingMemoryPath())
	if err != nil {
		t.Fatalf("read pre-publish store: %v", err)
	}
	if _, _, err := app.publishOSArtifact(artifact.ID, true, user.Name); err == nil || !strings.Contains(err.Error(), "cannot be published") {
		t.Fatalf("shared publish seam error=%v", err)
	}
	if _, _, err := app.publishRealtimeArtifact(map[string]any{"artifact_id": artifact.ID, "published": true}); err == nil || !strings.Contains(err.Error(), "cannot be published") {
		t.Fatalf("realtime publish error=%v", err)
	}
	if _, _, err := app.applyToolCallArgs("publish_artifact", map[string]any{"artifact_id": artifact.ID, "published": true}); err == nil || !strings.Contains(err.Error(), "cannot be published") {
		t.Fatalf("tool publish error=%v", err)
	}
	afterPublish, err := os.ReadFile(meetingMemoryPath())
	if err != nil || !bytes.Equal(beforePublish, afterPublish) {
		t.Fatalf("denied publish mutated store err=%v", err)
	}
	cookies := loginAs(t, user.Email, "B0NFIRE!")
	publishRequest := httptest.NewRequest(http.MethodPatch, "/artifacts", strings.NewReader(`{"id":"`+artifact.ID+`","published":true}`))
	publishRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		publishRequest.AddCookie(cookie)
	}
	publishResponse := httptest.NewRecorder()
	artifactsHandler(publishResponse, publishRequest)
	if publishResponse.Code != http.StatusConflict {
		t.Fatalf("publish status=%d body=%s", publishResponse.Code, publishResponse.Body.String())
	}
	current, found := app.osArtifactByID(artifact.ID)
	if !found || current.Metadata["published"] != "false" || current.Metadata["status"] != artifactStatusComplete {
		t.Fatalf("publish denial mutated artifact=%#v", current.Metadata)
	}
	for key, want := range map[string]string{"visibility": "private", "ownerEmail": normalizeAccountEmail(user.Email), "requestedBy": normalizeAccountEmail(user.Email), "originSurface": "chat:" + thread.ID, "toolTemplate": ventureWorkbookToolID, "artifactContract": ventureWorkbookContract, "generationMode": "deterministic_local_workbook"} {
		if artifact.Metadata[key] != want {
			t.Errorf("metadata[%s]=%q want %q", key, artifact.Metadata[key], want)
		}
	}
	assets := artifactAssets(artifact)
	if len(assets) != 1 || assets[0].Kind != "export" || assets[0].Mime != ventureWorkbookMime || !strings.HasSuffix(assets[0].Name, ".xlsx") {
		t.Fatalf("assets=%#v", assets)
	}
	payload, meta, err := getBlob(assets[0].Ref)
	if err != nil || meta.Mime != ventureWorkbookMime {
		t.Fatalf("read workbook blob meta=%+v err=%v", meta, err)
	}
	_ = readWorkbookParts(t, payload)
	var preview ventureWorkbookPreview
	if err := json.Unmarshal([]byte(artifact.Metadata[ventureWorkbookPreviewKey]), &preview); err != nil || preview.SheetCount != 5 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	last := saved.Messages[len(saved.Messages)-1]
	if last.Thread == nil || last.Thread.Status != artifactStatusComplete || last.Thread.ProgressPercent != 100 || !strings.Contains(last.Text, "Workbook delivered") {
		t.Fatalf("terminal card=%#v", last)
	}
	replayed, err := app.createPrivateVentureWorkbook(thread.ID, artifact.Metadata["sourceMessageId"], agent.Query, user)
	if err != nil {
		t.Fatalf("idempotent artifact replay: %v", err)
	}
	if replayed.Artifact.ID != artifact.ID || len(app.osArtifactsSnapshot(0)) != 1 {
		t.Fatalf("replay artifact=%s count=%d want same/1", replayed.Artifact.ID, len(app.osArtifactsSnapshot(0)))
	}
	reloadedStore, err := newMeetingMemoryStore(meetingMemoryPath())
	if err != nil {
		t.Fatalf("reload memory: %v", err)
	}
	reloadedApp := &kanbanBoardApp{memory: reloadedStore}
	reloadedArtifact, found := reloadedApp.osArtifactByID(artifact.ID)
	if !found || len(artifactAssets(reloadedArtifact)) != 1 || artifactType(reloadedArtifact) != artifactTypeWorkbook {
		t.Fatalf("restart artifact=%#v found=%v", reloadedArtifact, found)
	}
	row, err := app.saveDeliverableSnapshotToFilesNamed(artifact, "", "Nimbus-Series-A.xlsx", user.Name)
	if err != nil {
		t.Fatalf("save workbook to Drive: %v", err)
	}
	if row.Mime != ventureWorkbookMime || row.Previewable || !strings.Contains(row.DownloadURL, assets[0].Ref) || row.Name != "Nimbus-Series-A.xlsx" {
		t.Fatalf("Drive row=%#v", row)
	}
	other := accountStore().findUser("tom@shareability.com")
	if other == nil {
		t.Fatal("seed non-owner missing")
	}
	if _, allowed := authorizedArtifactForActions(context.Background(), other, artifact.ID, ACLReadContent); allowed {
		t.Fatal("other user read private workbook")
	}
}

func TestVentureWorkbookRefusesPublicChatWithoutArtifact(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	channel, err := app.createScoutChatThread(user.Email, user.Name, "Public finance", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	before := len(app.osArtifactsSnapshot(0))
	_, err = app.appendScoutChatThreadMessageWithReplyAndTool(ventureWorkbookContext("workbook-operation-public"), user, channel.ID, "make the model", nil, "", "", ventureWorkbookToolID)
	if err == nil || !strings.Contains(err.Error(), "private Scout chat") {
		t.Fatalf("public error=%v", err)
	}
	if after := len(app.osArtifactsSnapshot(0)); after != before {
		t.Fatalf("public attempt created %d artifacts", after-before)
	}
}

func TestVentureWorkbookPreviewUsesWorkbookKickerAndNeverOffersPreviewPDF(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read frontend: %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "workbook: 'workbook'") {
		t.Fatal("artifact stage does not identify workbook artifacts")
	}
	if !strings.Contains(text, "metadata?.type || '').trim() !== 'workbook'") {
		t.Fatal("workbook preview incorrectly offers markdown-PDF export")
	}
	for _, binding := range []string{"toolTemplate === 'economics_waterfall'", "messagePayload.operationId", "response = await postMessage()"} {
		if !strings.Contains(text, binding) {
			t.Fatalf("workbook transport missing stable replay binding %q", binding)
		}
	}
}

func TestVentureWorkbookConcurrentDuplicateExecutesOnce(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Concurrent workbook", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	type outcome struct {
		response map[string]any
		err      error
	}
	outcomes := make(chan outcome, 2)
	for range 2 {
		go func() {
			response, err := app.appendScoutChatThreadMessageWithReplyAndTool(ventureWorkbookContext("workbook-operation-concurrent"), user, thread.ID, "Concurrent model", nil, "", "", ventureWorkbookToolID)
			outcomes <- outcome{response, err}
		}()
	}
	replayed := 0
	for range 2 {
		result := <-outcomes
		if result.err != nil {
			t.Fatalf("duplicate execution: %v", result.err)
		}
		if result.response["replayed"] == true {
			replayed++
		}
	}
	current, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil || len(current.Messages) != 2 || len(app.osArtifactsSnapshot(0)) != 1 || replayed != 1 {
		t.Fatalf("messages=%d artifacts=%d replayed=%d err=%v", len(current.Messages), len(app.osArtifactsSnapshot(0)), replayed, err)
	}
}

func TestVentureWorkbookGenerationLockCardinalityIsBoundedPerThread(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Workbook lock bound", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	for index := range 24 {
		operationID := fmt.Sprintf("workbook-operation-cardinality-%02d", index)
		objective := fmt.Sprintf("Cardinality model %02d", index)
		if _, err := app.appendScoutChatThreadMessageWithReplyAndTool(ventureWorkbookContext(operationID), user, thread.ID, objective, nil, "", "", ventureWorkbookToolID); err != nil {
			t.Fatalf("operation %d: %v", index, err)
		}
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	workbookLocks := 0
	for key := range app.chatThreadLocks {
		if strings.HasPrefix(key, "venture-workbook-") {
			workbookLocks++
		}
	}
	if workbookLocks != 1 {
		t.Fatalf("workbook lock cardinality=%d, want one per durable thread", workbookLocks)
	}
}

func TestVentureWorkbookPostArtifactFailureRestartAndLostResponseReplay(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp; ventureWorkbookBeforeChatCommitProbe = nil })
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Durable workbook", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	cookies := loginAs(t, user.Email, "B0NFIRE!")
	body := `{"text":"Durable venture model","toolTemplate":"economics_waterfall","operationId":"workbook-operation-restart-0001"}`
	postBody := func(requestBody string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(requestBody))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		assistantChatThreadHandler(response, request)
		return response
	}
	post := func() *httptest.ResponseRecorder { return postBody(body) }
	missingOperation := postBody(`{"text":"Durable venture model","toolTemplate":"economics_waterfall"}`)
	if missingOperation.Code != http.StatusBadRequest || len(app.osArtifactsSnapshot(0)) != 0 {
		t.Fatalf("missing operation status=%d artifacts=%d", missingOperation.Code, len(app.osArtifactsSnapshot(0)))
	}
	ventureWorkbookBeforeChatCommitProbe = func() error { return io.ErrUnexpectedEOF }
	failed := post()
	if failed.Code == http.StatusOK || !strings.Contains(failed.Body.String(), "needs reconciliation") {
		t.Fatalf("failed status=%d body=%s", failed.Code, failed.Body.String())
	}
	if len(app.osArtifactsSnapshot(0)) != 1 {
		t.Fatalf("post-artifact failure artifacts=%d want 1 recoverable", len(app.osArtifactsSnapshot(0)))
	}
	currentThread, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil || len(currentThread.Messages) != 0 {
		t.Fatalf("failed commit thread=%#v err=%v", currentThread.Messages, err)
	}

	reloaded, err := newMeetingMemoryStore(meetingMemoryPath())
	if err != nil {
		t.Fatalf("restart memory: %v", err)
	}
	app.memory = reloaded
	ventureWorkbookBeforeChatCommitProbe = nil
	recovered := post()
	if recovered.Code != http.StatusOK {
		t.Fatalf("recovery status=%d body=%s", recovered.Code, recovered.Body.String())
	}
	var recoveredPayload struct {
		Artifact meetingMemoryEntry    `json:"artifact"`
		Thread   scoutChatThreadRecord `json:"thread"`
		Replayed bool                  `json:"replayed"`
	}
	if err := json.Unmarshal(recovered.Body.Bytes(), &recoveredPayload); err != nil {
		t.Fatalf("decode recovery: %v", err)
	}
	if recoveredPayload.Replayed || len(recoveredPayload.Thread.Messages) != 2 || len(app.osArtifactsSnapshot(0)) != 1 {
		t.Fatalf("recovery payload=%#v artifacts=%d", recoveredPayload, len(app.osArtifactsSnapshot(0)))
	}

	replay := post()
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayPayload struct {
		Artifact meetingMemoryEntry    `json:"artifact"`
		Thread   scoutChatThreadRecord `json:"thread"`
		Replayed bool                  `json:"replayed"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayPayload); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if !replayPayload.Replayed || replayPayload.Artifact.ID != recoveredPayload.Artifact.ID || len(replayPayload.Thread.Messages) != 2 || len(app.osArtifactsSnapshot(0)) != 1 {
		t.Fatalf("lost-response replay=%#v recovered=%#v artifacts=%d", replayPayload, recoveredPayload, len(app.osArtifactsSnapshot(0)))
	}
	conflict := postBody(`{"text":"Different objective","toolTemplate":"economics_waterfall","operationId":"workbook-operation-restart-0001"}`)
	if conflict.Code == http.StatusOK || len(app.osArtifactsSnapshot(0)) != 1 {
		t.Fatalf("operation reuse conflict status=%d artifacts=%d", conflict.Code, len(app.osArtifactsSnapshot(0)))
	}
}
