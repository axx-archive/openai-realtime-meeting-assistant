package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func postPDFExportForIdempotencyTest(pathBody string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/artifacts/export-pdf", strings.NewReader(pathBody))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	artifactExportPDFHandler(recorder, req)
	return recorder
}

func renderQueueJSONFiles(t *testing.T, queueDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(queueDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry.Name())
		}
	}
	return files
}

func TestArtifactExportPDFDoublePOSTReusesOneExactJob(t *testing.T) {
	_, member := shareLinkTestEnv(t)
	queueDir := setupRenderSidecarEnv(t)
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "idempotency-runner"); err != nil {
		t.Fatal(err)
	}
	artifact := seedShareArtifact(t, "draft", "# The exact report\n\nOne durable proof point.", nil)
	body := fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, artifact.ID, artifactVersion(artifact))

	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			responses <- postPDFExportForIdempotencyTest(body, member)
		}()
	}
	close(start)
	group.Wait()
	close(responses)

	jobIDs := map[string]bool{}
	reused := 0
	for recorder := range responses {
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("double POST status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		jobIDs[strings.TrimSpace(fmt.Sprint(payload["jobId"]))] = true
		if value, _ := payload["reused"].(bool); value {
			reused++
		}
	}
	if len(jobIDs) != 1 || reused != 1 {
		t.Fatalf("double POST jobIDs=%v reused=%d, want one job and one reuse", jobIDs, reused)
	}
	if files := renderQueueJSONFiles(t, queueDir); len(files) != 1 {
		t.Fatalf("render queue files=%v, want exactly one durable job", files)
	}
	stored, _ := kanbanApp.osArtifactByID(artifact.ID)
	for jobID := range jobIDs {
		if stored.Metadata["renderJobId"] != jobID || stored.Metadata[renderSourceContentDigestMetadataKey] == "" {
			t.Fatalf("artifact binding=%v, want exact reusable job %s + digest", stored.Metadata, jobID)
		}
	}
}

func TestArtifactExportPDFTimeoutRetryResumesRunningJob(t *testing.T) {
	_, member := shareLinkTestEnv(t)
	queueDir := setupRenderSidecarEnv(t)
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "timeout-runner"); err != nil {
		t.Fatal(err)
	}
	artifact := seedShareArtifact(t, "draft", "<!doctype html><html><body>six slide deck</body></html>", map[string]string{"type": artifactTypeHTMLDeck})
	body := fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, artifact.ID, artifactVersion(artifact))
	first := postPDFExportForIdempotencyTest(body, member)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first export status=%d body=%s", first.Code, first.Body.String())
	}
	firstPayload := decodeJSON(t, first)
	jobID := strings.TrimSpace(fmt.Sprint(firstPayload["jobId"]))
	store := newRenderRunnerJobStore(queueDir)
	job := readRenderJobForTest(t, queueDir, jobID)
	job.Status = renderJobStatusRunning
	job.StartedAt = job.CreatedAt.Add(1)
	job.Attempts = 1
	if err := store.update(job); err != nil {
		t.Fatal(err)
	}
	current, _ := kanbanApp.osArtifactByID(artifact.ID)
	header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(current))
	if _, changed, err := kanbanApp.memory.updateOSArtifactMetadataIfHeaderAndMetadataMatch(header, map[string]string{"renderJobId": jobID}, artifact.ID, map[string]string{"renderStatus": renderJobStatusRunning}); err != nil || !changed {
		t.Fatalf("mark render running: changed=%v err=%v", changed, err)
	}
	// A browser may retry after its polling window while the heartbeat happens
	// to be between writes. An already-running exact job remains resumable.
	if err := os.Remove(renderRunnerHeartbeatPath()); err != nil {
		t.Fatal(err)
	}
	retry := postPDFExportForIdempotencyTest(body, member)
	if retry.Code != http.StatusAccepted {
		t.Fatalf("timeout retry status=%d body=%s, want 202", retry.Code, retry.Body.String())
	}
	retryPayload := decodeJSON(t, retry)
	if fmt.Sprint(retryPayload["jobId"]) != jobID || retryPayload["reused"] != true || retryPayload["renderStatus"] != renderJobStatusRunning {
		t.Fatalf("timeout retry=%v, want same running job %s", retryPayload, jobID)
	}
	if files := renderQueueJSONFiles(t, queueDir); len(files) != 1 {
		t.Fatalf("timeout retry queued duplicate work: %v", files)
	}
}

func TestArtifactExportPDFLateFirstCallbackStillAttachesAfterRetry(t *testing.T) {
	_, member := shareLinkTestEnv(t)
	setupRenderSidecarEnv(t)
	t.Setenv("BONFIRE_RUNNER_TOKEN", "render-secret")
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "late-callback-runner"); err != nil {
		t.Fatal(err)
	}
	artifact := seedShareArtifact(t, "draft", "<!doctype html><html><body>late callback deck</body></html>", map[string]string{"type": artifactTypeHTMLDeck})
	body := fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, artifact.ID, artifactVersion(artifact))
	first := postPDFExportForIdempotencyTest(body, member)
	retry := postPDFExportForIdempotencyTest(body, member)
	firstJobID := strings.TrimSpace(fmt.Sprint(decodeJSON(t, first)["jobId"]))
	if retry.Code != http.StatusAccepted || strings.TrimSpace(fmt.Sprint(decodeJSON(t, retry)["jobId"])) != firstJobID {
		t.Fatalf("retry replaced first job: first=%s retry=%s", first.Body.String(), retry.Body.String())
	}

	pdfBytes := []byte("%PDF-1.7 first callback stayed authoritative")
	callback := renderRunnerCallbackPayload{
		JobID: firstJobID, ArtifactID: artifact.ID, Status: renderJobStatusComplete,
		PDFBase64: base64.StdEncoding.EncodeToString(pdfBytes), Flattened: true, PageCount: 6,
	}
	landed := renderCallbackRequest(t, "render-secret", callback)
	if landed.Code != http.StatusOK || !strings.Contains(landed.Body.String(), `"attached":true`) {
		t.Fatalf("late first callback status=%d body=%s", landed.Code, landed.Body.String())
	}
	current, _ := kanbanApp.osArtifactByID(artifact.ID)
	if current.Metadata["renderStatus"] != renderJobStatusComplete || current.Metadata["renderJobId"] != "" || len(artifactAssets(current)) != 1 {
		t.Fatalf("late first callback did not settle artifact: metadata=%v assets=%v", current.Metadata, artifactAssets(current))
	}
}

func TestArtifactExportPDFReusesExactCompletedAssetWithoutQueueOrVersionMutation(t *testing.T) {
	_, member := shareLinkTestEnv(t)
	queueDir := setupRenderSidecarEnv(t)
	t.Setenv("BONFIRE_RUNNER_TOKEN", "render-secret")
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "completed-idempotency-runner"); err != nil {
		t.Fatal(err)
	}
	artifact := seedShareArtifact(t, "draft", "<!doctype html><html><body>completed exact deck</body></html>", map[string]string{"type": artifactTypeHTMLDeck})
	firstBody := fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, artifact.ID, artifactVersion(artifact))
	first := postPDFExportForIdempotencyTest(firstBody, member)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first export status=%d body=%s", first.Code, first.Body.String())
	}
	jobID := strings.TrimSpace(fmt.Sprint(decodeJSON(t, first)["jobId"]))
	callback := renderRunnerCallbackPayload{
		JobID: jobID, ArtifactID: artifact.ID, Kind: renderJobKindDeck, Status: renderJobStatusComplete,
		PDFBase64: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 exact completed deck")), Flattened: true, PageCount: 5,
	}
	landed := renderCallbackRequest(t, "render-secret", callback)
	if landed.Code != http.StatusOK || !strings.Contains(landed.Body.String(), `"attached":true`) {
		t.Fatalf("callback status=%d body=%s", landed.Code, landed.Body.String())
	}
	current, found := kanbanApp.osArtifactByID(artifact.ID)
	if !found {
		t.Fatal("completed artifact disappeared")
	}
	asset, hasPDF := firstArtifactAssetOfKind(current, "pdf")
	if !hasPDF {
		t.Fatalf("completed artifact assets=%v, want PDF", artifactAssets(current))
	}
	beforeArtifact, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	queueFiles := renderQueueJSONFiles(t, queueDir)
	if len(queueFiles) != 1 {
		t.Fatalf("queue files=%v, want original job only", queueFiles)
	}
	beforeJob, err := os.ReadFile(filepath.Join(queueDir, queueFiles[0]))
	if err != nil {
		t.Fatal(err)
	}
	// A completed exact export must be retrievable even when no renderer is
	// healthy: no new work is necessary and the existing artifact is the result.
	if err := os.Remove(renderRunnerHeartbeatPath()); err != nil {
		t.Fatal(err)
	}
	retryBody := fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, current.ID, artifactVersion(current))
	retry := postPDFExportForIdempotencyTest(retryBody, member)
	if retry.Code != http.StatusOK {
		t.Fatalf("completed retry status=%d body=%s, want 200", retry.Code, retry.Body.String())
	}
	payload := decodeJSON(t, retry)
	returnedAsset, _ := payload["asset"].(map[string]any)
	if payload["reused"] != true || payload["renderStatus"] != renderJobStatusComplete ||
		fmt.Sprint(returnedAsset["ref"]) != asset.Ref || int(payload["artifactVersion"].(float64)) != artifactVersion(current) {
		t.Fatalf("completed retry payload=%v, want exact asset %s at v%d", payload, asset.Ref, artifactVersion(current))
	}
	after, _ := kanbanApp.osArtifactByID(artifact.ID)
	afterArtifact, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterArtifact) != string(beforeArtifact) {
		t.Fatalf("completed retry mutated artifact\nbefore=%s\nafter=%s", beforeArtifact, afterArtifact)
	}
	if files := renderQueueJSONFiles(t, queueDir); len(files) != 1 || files[0] != queueFiles[0] {
		t.Fatalf("completed retry queued work: before=%v after=%v", queueFiles, files)
	}
	afterJob, err := os.ReadFile(filepath.Join(queueDir, queueFiles[0]))
	if err != nil || string(afterJob) != string(beforeJob) {
		t.Fatalf("completed retry mutated original job: err=%v", err)
	}

	if _, changed, err := kanbanApp.memory.updateOSArtifactWithMetadata(artifact.ID, "", "<!doctype html><html><body>edited after completed PDF</body></html>", "AJ", nil); err != nil || !changed {
		t.Fatalf("edit after completed PDF: changed=%t err=%v", changed, err)
	}
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "completed-idempotency-runner"); err != nil {
		t.Fatal(err)
	}
	edited, _ := kanbanApp.osArtifactByID(artifact.ID)
	editedRetry := postPDFExportForIdempotencyTest(fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, edited.ID, artifactVersion(edited)), member)
	if editedRetry.Code != http.StatusAccepted || decodeJSON(t, editedRetry)["reused"] != false {
		t.Fatalf("edited retry status=%d body=%s, want a new queued render", editedRetry.Code, editedRetry.Body.String())
	}
	if files := renderQueueJSONFiles(t, queueDir); len(files) != 2 {
		t.Fatalf("edited revision reused completed PDF: queue files=%v, want old receipt plus new job", files)
	}
}

func TestArtifactExportPDFStaleRevisionCannotReuseOrAttach(t *testing.T) {
	_, member := shareLinkTestEnv(t)
	queueDir := setupRenderSidecarEnv(t)
	t.Setenv("BONFIRE_RUNNER_TOKEN", "render-secret")
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "stale-revision-runner"); err != nil {
		t.Fatal(err)
	}
	artifact := seedShareArtifact(t, "draft", "<!doctype html><html><body>revision one</body></html>", map[string]string{"type": artifactTypeHTMLDeck})
	oldVersion := artifactVersion(artifact)
	oldBody := fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, artifact.ID, oldVersion)
	first := postPDFExportForIdempotencyTest(oldBody, member)
	jobID := strings.TrimSpace(fmt.Sprint(decodeJSON(t, first)["jobId"]))
	if _, changed, err := kanbanApp.memory.updateOSArtifactWithMetadata(artifact.ID, "", "<!doctype html><html><body>revision two</body></html>", "AJ", nil); err != nil || !changed {
		t.Fatalf("edit artifact: changed=%v err=%v", changed, err)
	}

	staleRetry := postPDFExportForIdempotencyTest(oldBody, member)
	if staleRetry.Code != http.StatusConflict {
		t.Fatalf("stale revision retry status=%d body=%s, want 409", staleRetry.Code, staleRetry.Body.String())
	}
	if files := renderQueueJSONFiles(t, queueDir); len(files) != 1 {
		t.Fatalf("stale retry queued replacement work: %v", files)
	}
	callback := renderRunnerCallbackPayload{
		JobID: jobID, ArtifactID: artifact.ID, Status: renderJobStatusComplete,
		PDFBase64: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 stale")), Flattened: true, PageCount: 1,
	}
	response := renderCallbackRequest(t, "render-secret", callback)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stale":true`) {
		t.Fatalf("stale callback status=%d body=%s", response.Code, response.Body.String())
	}
	current, _ := kanbanApp.osArtifactByID(artifact.ID)
	if artifactVersion(current) != oldVersion+1 || current.Metadata["renderStatus"] != renderJobStatusStale || current.Metadata["renderJobId"] != "" || len(artifactAssets(current)) != 0 {
		t.Fatalf("stale callback crossed revision boundary: v=%d metadata=%v assets=%v", artifactVersion(current), current.Metadata, artifactAssets(current))
	}
	if current.Metadata[renderSourceArtifactVersionMetadataKey] != strconv.Itoa(oldVersion) {
		t.Fatalf("stale receipt lost original source version: %v", current.Metadata)
	}
	if _, err := os.Stat(filepath.Join(queueDir, jobID+".json")); err != nil {
		t.Fatalf("source job receipt disappeared: %v", err)
	}
}
