package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func enqueueRunnerQueueTestJob(t *testing.T, store *codexRunnerJobStore, id string) codexRunnerJob {
	t.Helper()
	job, err := store.enqueue(codexRunnerJob{
		ID:         id,
		ArtifactID: "artifact-" + id,
		ThreadID:   "thread-" + id,
		Mode:       "research",
		Query:      "inspect the evidence",
		Authority:  codexJobAuthorityReadOnly,
	})
	if err != nil {
		t.Fatalf("enqueue %s: %v", id, err)
	}
	return job
}

func TestCodexRunnerClaimIsAtomicAcrossConcurrentRunners(t *testing.T) {
	store := newCodexRunnerJobStore(t.TempDir())
	enqueueRunnerQueueTestJob(t, store, "codex-job-concurrent")

	const runners = 32
	start := make(chan struct{})
	results := make(chan *codexRunnerJob, runners)
	errorsSeen := make(chan error, runners)
	var wg sync.WaitGroup
	for index := 0; index < runners; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			job, err := store.claimNextAt("runner-"+time.Unix(int64(index), 0).Format("150405"), time.Now().UTC(), time.Minute)
			results <- job
			errorsSeen <- err
		}(index)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errorsSeen)

	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent claim: %v", err)
		}
	}
	claimed := 0
	var winner *codexRunnerJob
	for job := range results {
		if job != nil {
			claimed++
			winner = job
		}
	}
	if claimed != 1 {
		t.Fatalf("successful claims=%d, want exactly one", claimed)
	}
	if winner.ClaimGeneration != 1 || winner.Attempts != 1 || winner.FencingToken == "" || winner.LeaseExpiresAt.IsZero() {
		t.Fatalf("winner=%+v, want generation-one fenced lease", winner)
	}
}

func TestCodexRunnerLeaseRenewalAndRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	store := newCodexRunnerJobStore(dir)
	enqueueRunnerQueueTestJob(t, store, "codex-job-restart")
	t0 := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	first, err := store.claimNextAt("runner-one", t0, time.Second)
	if err != nil || first == nil {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	renewed, err := store.renewClaimAt(*first, t0.Add(500*time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !renewed.LeaseExpiresAt.Equal(t0.Add(1500 * time.Millisecond)) {
		t.Fatalf("renewed expiry=%s", renewed.LeaseExpiresAt)
	}
	if stolen, err := store.claimNextAt("runner-two", t0.Add(1100*time.Millisecond), time.Second); err != nil || stolen != nil {
		t.Fatalf("claim during renewed lease=%+v err=%v, want nil/nil", stolen, err)
	}

	// A fresh store models a restarted process. Once the durable lease expires,
	// it recovers the same deterministic job ID with a new generation/token.
	restarted := newCodexRunnerJobStore(dir)
	second, err := restarted.claimNextAt("runner-two", t0.Add(1600*time.Millisecond), time.Second)
	if err != nil || second == nil {
		t.Fatalf("restart recovery=%+v err=%v", second, err)
	}
	if second.ID != first.ID || second.ClaimGeneration != 2 || second.Attempts != 2 || second.FencingToken == first.FencingToken {
		t.Fatalf("recovered=%+v first=%+v", second, first)
	}
	if second.Metadata["recoveredExpiredClaimAt"] == "" {
		t.Fatalf("recovery receipt missing: %v", second.Metadata)
	}
}

func TestCodexRunnerStaleGenerationCannotRenewOrComplete(t *testing.T) {
	store := newCodexRunnerJobStore(t.TempDir())
	enqueueRunnerQueueTestJob(t, store, "codex-job-fenced")
	t0 := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	first, err := store.claimNextAt("runner-one", t0, time.Second)
	if err != nil || first == nil {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	second, err := store.claimNextAt("runner-two", t0.Add(2*time.Second), time.Second)
	if err != nil || second == nil {
		t.Fatalf("second claim=%+v err=%v", second, err)
	}
	if _, err := store.renewClaimAt(*first, t0.Add(2100*time.Millisecond), time.Second); !errors.Is(err, errCodexRunnerClaimLost) {
		t.Fatalf("stale renew error=%v, want claim lost", err)
	}
	staleTerminal := *first
	staleTerminal.Status = codexJobStatusComplete
	staleTerminal.CompletedAt = t0.Add(2100 * time.Millisecond)
	if err := store.updateAt(staleTerminal, t0.Add(2100*time.Millisecond)); !errors.Is(err, errCodexRunnerClaimLost) {
		t.Fatalf("stale terminal update error=%v, want claim lost", err)
	}
	validTerminal := *second
	validTerminal.Status = codexJobStatusComplete
	validTerminal.CompletedAt = t0.Add(2200 * time.Millisecond)
	if err := store.updateAt(validTerminal, t0.Add(2200*time.Millisecond)); err != nil {
		t.Fatalf("current owner complete: %v", err)
	}
	persisted, err := store.read(filepath.Base(store.jobPath(second.ID)))
	if err != nil {
		t.Fatalf("read terminal job: %v", err)
	}
	if persisted.Status != codexJobStatusComplete || persisted.RunnerID != "runner-two" || persisted.ClaimGeneration != 2 {
		t.Fatalf("terminal job=%+v, want runner-two generation two", persisted)
	}
}

func TestCodexRunnerClaimFailsClosedOnUnreadableEarlierEntry(t *testing.T) {
	store := newCodexRunnerJobStore(t.TempDir())
	if err := os.MkdirAll(store.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.dir, "000-corrupt.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	enqueueRunnerQueueTestJob(t, store, "zzz-valid-job")
	claimed, err := store.claimNextAt("runner-one", time.Now().UTC(), time.Minute)
	if err == nil || claimed != nil {
		t.Fatalf("claim=%+v err=%v, want fail-closed decode error", claimed, err)
	}
	persisted, readErr := store.read("zzz-valid-job.json")
	if readErr != nil || persisted.Status != codexJobStatusQueued {
		t.Fatalf("later job mutated despite ambiguity: job=%+v err=%v", persisted, readErr)
	}
}

func TestCodexRunnerCallbackRejectsExpiredGenerationEvenWithValidOldSignature(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)
	t.Setenv("BONFIRE_RUNNER_TOKEN", "runner-secret")
	store := newCodexRunnerJobStore(queueDir)
	queued := enqueueRunnerQueueTestJob(t, store, "codex-job-callback-fence")
	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "Build", "queued", "tester", map[string]string{
		"threadId": queued.ThreadID, "runnerJobId": queued.ID, "threadStatus": codexJobStatusQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	first, err := store.claimNextAt("runner-one", t0, time.Second)
	if err != nil || first == nil {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	if second, err := store.claimNextAt("runner-two", t0.Add(2*time.Second), time.Minute); err != nil || second == nil {
		t.Fatalf("second claim=%+v err=%v", second, err)
	}
	payload := codexRunnerCallbackPayload{
		JobID: first.ID, ArtifactID: artifact.ID, ThreadID: first.ThreadID,
		Status: codexJobStatusRunning, ClaimGeneration: first.ClaimGeneration, FencingToken: first.FencingToken,
	}
	payload.Capability = codexRunnerCallbackCapabilityV2("runner-secret", payload.JobID, payload.ArtifactID, payload.ThreadID, payload.ClaimGeneration, payload.FencingToken)
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/internal/codex/jobs/result", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runner-secret")
	recorder := httptest.NewRecorder()
	internalCodexRunnerResultHandler(recorder, req)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale callback status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	unchanged, _ := app.osArtifactByID(artifact.ID)
	if unchanged.Metadata["threadStatus"] != codexJobStatusQueued {
		t.Fatalf("stale callback mutated artifact: %v", unchanged.Metadata)
	}
}

// artifact.Kind is always "os_artifact", so the rerun action's old
// normalizeAgentThreadMode(artifact.Kind) silently dropped every rerun to
// workflow mode and lost the research contract. The mode metadata the launch
// stamped is the truth; Kind stays the last-resort fallback (the same
// firstNonEmptyString pattern the follow-up runner uses).
func TestRerunThreadModeUsesMetadataMode(t *testing.T) {
	for _, tt := range []struct {
		name     string
		artifact meetingMemoryEntry
		want     string
	}{
		{
			name:     "research mode survives a rerun",
			artifact: meetingMemoryEntry{Kind: "os_artifact", Metadata: map[string]string{"mode": "research"}},
			want:     "research",
		},
		{
			name:     "grill mode survives a rerun",
			artifact: meetingMemoryEntry{Kind: "os_artifact", Metadata: map[string]string{"mode": "grill"}},
			want:     "grill",
		},
		{
			name:     "missing mode falls back to workflow",
			artifact: meetingMemoryEntry{Kind: "os_artifact", Metadata: map[string]string{}},
			want:     "workflow",
		},
		{
			name:     "unknown mode falls back to workflow",
			artifact: meetingMemoryEntry{Kind: "os_artifact", Metadata: map[string]string{"mode": "nonsense"}},
			want:     "workflow",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := rerunThreadMode(tt.artifact); got != tt.want {
				t.Fatalf("rerunThreadMode=%q, want %q", got, tt.want)
			}
		})
	}
}
