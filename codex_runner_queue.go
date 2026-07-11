package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	codexJobAuthorityReadOnly       = "read_only"
	codexJobAuthorityWorkspaceWrite = "workspace_write"
	codexJobAuthorityExternalWrite  = "external_write"

	codexJobStatusQueued           = "queued"
	codexJobStatusRunning          = "running"
	codexJobStatusComplete         = "complete"
	codexJobStatusFailed           = "failed"
	codexJobStatusApprovalRequired = "approval_required"

	defaultCodexRunnerPollInterval = 2 * time.Second
	defaultCodexRunnerStaleAfter   = 2 * time.Minute
)

type codexRunnerJob struct {
	ID             string            `json:"id"`
	ArtifactID     string            `json:"artifact_id"`
	ThreadID       string            `json:"thread_id"`
	Mode           string            `json:"mode"`
	Query          string            `json:"query"`
	Prompt         string            `json:"prompt"`
	Authority      string            `json:"authority"`
	Status         string            `json:"status"`
	CreatedAt      time.Time         `json:"created_at"`
	StartedAt      time.Time         `json:"started_at,omitempty"`
	CompletedAt    time.Time         `json:"completed_at,omitempty"`
	Attempts       int               `json:"attempts"`
	RunnerID       string            `json:"runner_id,omitempty"`
	Error          string            `json:"error,omitempty"`
	RunnerEvidence string            `json:"runner_evidence,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type codexRunnerJobStore struct {
	dir string
}

type codexRunnerCallbackPayload struct {
	JobID          string            `json:"job_id"`
	ArtifactID     string            `json:"artifact_id"`
	ThreadID       string            `json:"thread_id,omitempty"`
	Status         string            `json:"status"`
	Text           string            `json:"text,omitempty"`
	Error          string            `json:"error,omitempty"`
	RunnerEvidence string            `json:"runner_evidence,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

func codexRunnerQueuePath() string {
	if path := strings.TrimSpace(os.Getenv("BONFIRE_CODEX_QUEUE_PATH")); path != "" {
		return filepath.Clean(path)
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "codex-runner-jobs")
}

func codexRunnerHeartbeatPath() string {
	if path := strings.TrimSpace(os.Getenv("BONFIRE_CODEX_HEARTBEAT_PATH")); path != "" {
		return filepath.Clean(path)
	}
	return filepath.Join(filepath.Dir(codexRunnerQueuePath()), "codex-runner-heartbeat.json")
}

func codexRunnerPollInterval() time.Duration {
	return durationEnv("BONFIRE_CODEX_RUNNER_POLL_INTERVAL", defaultCodexRunnerPollInterval, 250*time.Millisecond)
}

func newCodexRunnerJobStore(dir string) *codexRunnerJobStore {
	return &codexRunnerJobStore{dir: filepath.Clean(strings.TrimSpace(dir))}
}

func (store *codexRunnerJobStore) enqueue(job codexRunnerJob) (codexRunnerJob, error) {
	if store == nil || strings.TrimSpace(store.dir) == "" {
		return codexRunnerJob{}, fmt.Errorf("Codex runner queue path is not configured")
	}
	if strings.TrimSpace(job.ID) == "" {
		job.ID = newCodexRunnerJobID()
	}
	if strings.TrimSpace(job.Status) == "" {
		job.Status = codexJobStatusQueued
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	job.Authority = normalizeCodexJobAuthority(job.Authority)
	if job.Metadata == nil {
		job.Metadata = map[string]string{}
	}

	if err := os.MkdirAll(store.dir, 0o755); err != nil {
		return codexRunnerJob{}, fmt.Errorf("create Codex runner queue: %w", err)
	}
	if err := writeJSONFileAtomically(store.jobPath(job.ID), "Codex runner job", job); err != nil {
		return codexRunnerJob{}, err
	}
	return job, nil
}

func (store *codexRunnerJobStore) claimNext(runnerID string) (*codexRunnerJob, error) {
	if store == nil || strings.TrimSpace(store.dir) == "" {
		return nil, fmt.Errorf("Codex runner queue path is not configured")
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read Codex runner queue: %w", err)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := store.read(entry.Name())
		if err != nil {
			return nil, err
		}
		if job.Status != codexJobStatusQueued {
			continue
		}
		now := time.Now().UTC()
		job.Status = codexJobStatusRunning
		job.StartedAt = now
		job.Attempts++
		job.RunnerID = runnerID
		if job.Metadata == nil {
			job.Metadata = map[string]string{}
		}
		job.Metadata["claimedAt"] = now.Format(time.RFC3339Nano)
		job.Metadata["runnerId"] = runnerID
		if err := store.update(*job); err != nil {
			return nil, err
		}
		return job, nil
	}

	return nil, nil
}

func (store *codexRunnerJobStore) update(job codexRunnerJob) error {
	if strings.TrimSpace(job.ID) == "" {
		return fmt.Errorf("Codex runner job id is required")
	}
	return writeJSONFileAtomically(store.jobPath(job.ID), "Codex runner job", job)
}

func (store *codexRunnerJobStore) read(filename string) (*codexRunnerJob, error) {
	path := filepath.Join(store.dir, filepath.Base(filename))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Codex runner job: %w", err)
	}
	var job codexRunnerJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, fmt.Errorf("decode Codex runner job %s: %w", filepath.Base(filename), err)
	}
	return &job, nil
}

func (store *codexRunnerJobStore) jobPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = newCodexRunnerJobID()
	}
	return filepath.Join(store.dir, id+".json")
}

func newCodexRunnerJobID() string {
	return fmt.Sprintf("codex-job-%d-%d", time.Now().UTC().UnixNano(), os.Getpid())
}

func normalizeCodexJobAuthority(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "read-only", "readonly", "read_only":
		return codexJobAuthorityReadOnly
	case "external", "external-write", "external_write":
		return codexJobAuthorityExternalWrite
	default:
		return codexJobAuthorityWorkspaceWrite
	}
}

func codexJobAuthorityForThread(thread scoutAgentThread) string {
	mode := normalizeAgentThreadMode(thread.Mode)
	lower := strings.ToLower(strings.Join(strings.Fields(thread.Query), " "))
	if hasAssistantPhrase(lower, "commit", "push", "deploy", "ssh", "rsync", "docker compose", "send email", "email this", "call the api", "production mutation", "mutate production", "ship this live", "ship it live", "make this live", "release to production", "restart production", "run the migration", "run migration", "apply migration") {
		return codexJobAuthorityExternalWrite
	}
	if mode == "research" && !hasAssistantPhrase(lower, "edit", "implement", "write code", "change files", "test the app", "build the app") {
		return codexJobAuthorityReadOnly
	}
	if hasAssistantPhrase(lower, "audit", "research", "investigate", "report", "plan") && !hasAssistantPhrase(lower, "implement", "fix", "change", "write", "build") {
		return codexJobAuthorityReadOnly
	}
	return codexJobAuthorityWorkspaceWrite
}

func codexExecConfigForAuthority(cfg codexExecConfig, authority string, mode string) codexExecConfig {
	authority = normalizeCodexJobAuthority(authority)
	switch authority {
	case codexJobAuthorityReadOnly:
		cfg.Sandbox = "read-only"
	case codexJobAuthorityWorkspaceWrite:
		if cfg.Sandbox == "" || cfg.Sandbox == "read-only" {
			cfg.Sandbox = "workspace-write"
		}
	}
	if normalizeAgentThreadMode(mode) == "research" {
		cfg.Search = true
	}
	return cfg
}

func (app *kanbanBoardApp) enqueueCodexAgentThreadArtifact(_ context.Context, thread scoutAgentThread) (agentThreadWorkerResult, error) {
	if app == nil {
		return agentThreadWorkerResult{}, fmt.Errorf("assistant is unavailable")
	}

	authority := codexJobAuthorityForThread(thread)
	// Wave-6 handoff: a /goal subtask child (goalParentId present) already had
	// its authority clamped by goalChildAuthority — never above workspace_write,
	// never out-privileging its parent. Re-deriving from the title text here
	// would ignore that clamp and, worse, could spuriously trip the approval
	// gate on a child whose title merely mentions "deploy". Honor the stamped,
	// already-clamped authority so the sidecar respects the engine's decision.
	if strings.TrimSpace(thread.Artifact.Metadata["goalParentId"]) != "" {
		if stamped := normalizeCodexJobAuthority(thread.Artifact.Metadata["authority"]); stamped == codexJobAuthorityReadOnly || stamped == codexJobAuthorityWorkspaceWrite {
			authority = stamped
		}
	}
	if authority == codexJobAuthorityExternalWrite {
		return codexApprovalRequiredResult(thread, authority), nil
	}

	return app.enqueueCodexAgentThreadJob(thread, authority)
}

func codexApprovalRequiredResult(thread scoutAgentThread, authority string) agentThreadWorkerResult {
	metadata := codexRunnerQueuedMetadata(thread, authority)
	metadata["workerBoundary"] = "codex_external_write_gate"
	metadata["status"] = codexJobStatusApprovalRequired
	metadata["threadStatus"] = codexJobStatusApprovalRequired
	metadata["goalStatus"] = "approval_required"
	metadata["currentStage"] = "gate_before_shipping"
	metadata["progressPercent"] = "68"
	metadata["reviewGate"] = "approval_required"
	metadata["codexRunner"] = "approval_required"
	// Card 069: a run that parks at the external-write gate IS heavy-lane work
	// regardless of its launch-time stamp — consumers read the artifact's
	// current stamp, never the launch-time one.
	metadata["approvalLane"] = approvalLaneHeavy
	return agentThreadWorkerResult{
		Text:     buildCodexApprovalRequiredArtifact(thread, authority),
		Metadata: metadata,
		Terminal: false,
	}
}

func (app *kanbanBoardApp) enqueueCodexAgentThreadJob(thread scoutAgentThread, authority string) (agentThreadWorkerResult, error) {
	authority = normalizeCodexJobAuthority(authority)
	metadata := codexRunnerQueuedMetadata(thread, authority)
	store := newCodexRunnerJobStore(codexRunnerQueuePath())
	prompt := app.buildCodexAgentThreadPrompt(thread, time.Now(), authority)
	job, err := store.enqueue(codexRunnerJob{
		ArtifactID: thread.Artifact.ID,
		ThreadID:   thread.ID,
		Mode:       thread.Mode,
		Query:      thread.Query,
		Prompt:     prompt,
		Authority:  authority,
		Metadata: map[string]string{
			"toolRegistry":   codexToolRegistrySummary(),
			"requestedTools": codexRequestedToolsForMode(thread.Mode),
			"worker":         agentThreadWorkerCodexExec,
			"workerBoundary": "codex_sidecar_queue",
			// Carry the raw-document contract so the runner's result handler
			// keeps the worker-evidence footer OFF a deck (a markdown section
			// after </html> renders as a trailing junk page in the export).
			"outputContract": strings.TrimSpace(thread.Artifact.Metadata["outputContract"]),
		},
	})
	if err != nil {
		return agentThreadWorkerResult{Metadata: metadata}, err
	}

	metadata["runnerJobId"] = job.ID
	metadata["runnerQueuePath"] = store.dir
	metadata["createdAt"] = job.CreatedAt.Format(time.RFC3339Nano)

	return agentThreadWorkerResult{
		Text:     buildCodexQueuedArtifact(thread, job),
		Metadata: metadata,
		Terminal: false,
	}, nil
}

func codexRunnerQueuedMetadata(thread scoutAgentThread, authority string) map[string]string {
	worker := agentThreadWorkerCodexExec
	return map[string]string{
		"worker":          worker,
		"workerBoundary":  "codex_sidecar_queue",
		"codexRunnerMode": configuredCodexRunnerMode(),
		"codexRunner":     "queued",
		"authority":       normalizeCodexJobAuthority(authority),
		"requestedTools":  codexRequestedToolsForMode(thread.Mode),
		"status":          codexJobStatusQueued,
		"threadStatus":    codexJobStatusQueued,
		"goalStatus":      codexJobStatusQueued,
		"currentStage":    "queued_for_codex_runner",
		"progressPercent": "12",
		"workflowStages":  goalWorkflowStageMetadata,
		"reviewGate":      "pending",
		"queuedAt":        time.Now().UTC().Format(time.RFC3339Nano),
		"published":       "false",
	}
}

func buildCodexQueuedArtifact(thread scoutAgentThread, job codexRunnerJob) string {
	contextLine := "Codex runner job " + job.ID + " queued with " + normalizeCodexJobAuthority(job.Authority) + " authority."
	lines := []string{
		"Scout work thread",
		"",
		"Vision: " + compactAssistantLine(thread.Query),
		"Status: queued",
		"Thread mode: " + assistantToolLabel(thread.Mode),
		"Runner: Codex sidecar queue",
		"Authority: " + normalizeCodexJobAuthority(job.Authority),
		"",
		"Execution log",
		"- Realtime 2 created the artifact and kept the voice/UI loop free.",
		"- The app enqueued a Codex job for the sidecar runner.",
		"- The runner will claim one job at a time, execute with explicit sandbox and approval settings, then call back with evidence.",
	}
	return strings.Join(appendGoalWorkflow(lines, thread.Mode, thread.Query, contextLine, agentThreadDeliverable(thread.Mode), "waiting for Codex runner claim"), "\n")
}

func buildCodexApprovalRequiredArtifact(thread scoutAgentThread, authority string) string {
	contextLine := "The request requires " + normalizeCodexJobAuthority(authority) + " authority before Codex can run external side effects."
	lines := []string{
		"Scout work thread",
		"",
		"Vision: " + compactAssistantLine(thread.Query),
		"Status: approval required",
		"Thread mode: " + assistantToolLabel(thread.Mode),
		"Runner: Codex sidecar queue",
		"Authority: " + normalizeCodexJobAuthority(authority),
		"",
		"Execution log",
		"- Realtime 2 created the artifact.",
		"- The requested action appears to involve commit, push, deploy, SSH, external APIs, email, or production mutation.",
		"- Codex did not run that side effect. Approve the exact side effect before resuming.",
	}
	return strings.Join(appendGoalWorkflow(lines, thread.Mode, thread.Query, contextLine, agentThreadDeliverable(thread.Mode), "approval required before external write"), "\n")
}

func codexToolRegistrySummary() string {
	return "research:read_only/report,design:workspace_write/artifact,grill:read_only/scorecard,workflow:workspace_write/goal_loop"
}

func codexRequestedToolsForMode(mode string) string {
	switch normalizeAgentThreadMode(mode) {
	case "research":
		return "research"
	case "design":
		return "design"
	case "grill":
		return "grill"
	case "workflow":
		return "workflow,research,grill"
	default:
		return "workflow"
	}
}

func runCodexRunnerLoop(ctx context.Context) error {
	store := newCodexRunnerJobStore(codexRunnerQueuePath())
	runnerID := codexRunnerID()
	pollInterval := codexRunnerPollInterval()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	log.Infof("Codex runner started id=%s queue=%s poll=%s", runnerID, store.dir, pollInterval)
	for {
		if err := writeCodexRunnerHeartbeat(runnerID); err != nil {
			log.Errorf("Codex runner heartbeat failed: %v", err)
		}
		job, err := store.claimNext(runnerID)
		if err != nil {
			log.Errorf("Codex runner queue claim failed: %v", err)
		} else if job != nil {
			processCodexRunnerJob(ctx, store, *job)
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func processCodexRunnerJob(ctx context.Context, store *codexRunnerJobStore, job codexRunnerJob) {
	authority := normalizeCodexJobAuthority(job.Authority)
	cfg := codexExecConfigForAuthority(codexExecConfigFromEnv(), authority, job.Mode)
	now := time.Now().UTC()
	runningMetadata := map[string]string{
		"status":              codexJobStatusRunning,
		"threadStatus":        codexJobStatusRunning,
		"goalStatus":          codexJobStatusRunning,
		"currentStage":        "codex_runner_executing",
		"progressPercent":     "35",
		"reviewGate":          "pending",
		"runnerJobId":         job.ID,
		"runnerId":            job.RunnerID,
		"worker":              agentThreadWorkerCodexExec,
		"workerBoundary":      "codex_sidecar_queue",
		"authority":           authority,
		"codexCommand":        cfg.Command,
		"codexCwd":            cfg.CWD,
		"codexSandbox":        cfg.Sandbox,
		"codexApprovalPolicy": cfg.ApprovalPolicy,
		"codexReasoning":      cfg.Reasoning,
		"codexSearch":         strconv.FormatBool(cfg.Search),
		"startedAt":           firstNonEmptyString(job.StartedAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)),
	}
	_ = sendCodexRunnerCallback(ctx, codexRunnerCallbackPayload{
		JobID:      job.ID,
		ArtifactID: job.ArtifactID,
		ThreadID:   job.ThreadID,
		Status:     codexJobStatusRunning,
		Metadata:   runningMetadata,
	})

	job.Status = codexJobStatusRunning
	job.Metadata = mergeStringMaps(job.Metadata, runningMetadata)
	if err := store.update(job); err != nil {
		log.Errorf("Codex runner could not persist running job %s: %v", job.ID, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	result, err := runCodexExecCommand(runCtx, cfg, strings.TrimSpace(job.Prompt))
	completedAt := time.Now().UTC()
	if err != nil {
		job.Status = codexJobStatusFailed
		job.CompletedAt = completedAt
		job.Error = err.Error()
		job.RunnerEvidence = codexRunnerCommandEvidence(result, cfg)
		job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
			"status":          "error",
			"threadStatus":    "error",
			"goalStatus":      "needs_attention",
			"currentStage":    "gate_before_shipping",
			"progressPercent": "72",
			"reviewGate":      "blocked",
			"completedAt":     completedAt.Format(time.RFC3339Nano),
			"error":           err.Error(),
		})
		if updateErr := store.update(job); updateErr != nil {
			log.Errorf("Codex runner could not persist failed job %s: %v", job.ID, updateErr)
		}
		_ = sendCodexRunnerCallback(ctx, codexRunnerCallbackPayload{
			JobID:          job.ID,
			ArtifactID:     job.ArtifactID,
			ThreadID:       job.ThreadID,
			Status:         codexJobStatusFailed,
			Text:           buildCodexRunnerErrorArtifact(job, err),
			Error:          err.Error(),
			RunnerEvidence: job.RunnerEvidence,
			Metadata:       job.Metadata,
		})
		return
	}

	output := strings.TrimSpace(result.FinalMessage)
	if output == "" {
		output = strings.TrimSpace(result.Stdout)
	}
	status := codexJobStatusComplete
	reviewGate := "passed"
	goalStatus := "verified"
	progress := "100"
	if codexOutputRequiresExternalApproval(output) {
		status = codexJobStatusApprovalRequired
		reviewGate = "approval_required"
		goalStatus = "approval_required"
		progress = "82"
	}
	text := appendCodexWorkerEvidenceForContract(output, cfg, job.Metadata["outputContract"])
	job.Status = status
	job.CompletedAt = completedAt
	job.RunnerEvidence = codexRunnerCommandEvidence(result, cfg)
	job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
		"status":          status,
		"threadStatus":    status,
		"goalStatus":      goalStatus,
		"currentStage":    "verify_goal_completed",
		"progressPercent": progress,
		"reviewGate":      reviewGate,
		"completedAt":     completedAt.Format(time.RFC3339Nano),
		"codexFinalBytes": strconv.Itoa(len(output)),
	})
	if status == codexJobStatusApprovalRequired {
		job.Metadata["currentStage"] = "gate_before_shipping"
	}
	if err := store.update(job); err != nil {
		log.Errorf("Codex runner could not persist completed job %s: %v", job.ID, err)
	}

	if err := sendCodexRunnerCallback(ctx, codexRunnerCallbackPayload{
		JobID:          job.ID,
		ArtifactID:     job.ArtifactID,
		ThreadID:       job.ThreadID,
		Status:         status,
		Text:           text,
		RunnerEvidence: job.RunnerEvidence,
		Metadata:       job.Metadata,
	}); err != nil {
		log.Errorf("Codex runner callback failed for job %s: %v", job.ID, err)
	}
}

func buildCodexRunnerErrorArtifact(job codexRunnerJob, err error) string {
	lines := []string{
		"Scout work thread",
		"",
		"Vision: " + compactAssistantLine(job.Query),
		"Status: needs attention",
		"Thread mode: " + assistantToolLabel(job.Mode),
		"",
		"Execution log",
		"- The sidecar Codex runner claimed the job.",
		"- Worker error: " + strings.TrimSpace(err.Error()),
		"",
		"Next action: inspect runner logs, credentials, queue health, or sandbox access, then rerun the thread.",
	}
	return strings.Join(appendGoalWorkflow(lines, job.Mode, job.Query, err.Error(), agentThreadDeliverable(job.Mode), "worker error recorded on artifact"), "\n")
}

func codexRunnerCommandEvidence(result codexExecResult, cfg codexExecConfig) string {
	parts := []string{
		"command=" + cfg.Command,
		"cwd=" + cfg.CWD,
		"sandbox=" + cfg.Sandbox,
		"approval=" + cfg.ApprovalPolicy,
		"reasoning=" + cfg.Reasoning,
		"search=" + strconv.FormatBool(cfg.Search),
		"stdout_bytes=" + strconv.Itoa(len(result.Stdout)),
		"stderr_bytes=" + strconv.Itoa(len(result.Stderr)),
	}
	if strings.TrimSpace(result.Stderr) != "" {
		parts = append(parts, "stderr="+compactAssistantLine(result.Stderr))
	}
	return strings.Join(parts, "\n")
}

func codexOutputRequiresExternalApproval(output string) bool {
	const marker = "EXTERNAL_WRITE_APPROVAL_REQUIRED"
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimLeft(trimmed, "-*>` \t")
		if !strings.HasPrefix(strings.ToUpper(trimmed), marker) {
			continue
		}
		remainder := strings.TrimSpace(trimmed[len(marker):])
		remainder = strings.TrimLeft(remainder, "*` \t")
		if remainder == "" || strings.HasPrefix(remainder, ":") || strings.HasPrefix(remainder, "-") {
			return true
		}
	}
	return false
}

func codexRunnerID() string {
	if value := strings.TrimSpace(os.Getenv("BONFIRE_CODEX_RUNNER_ID")); value != "" {
		return value
	}
	hostname, _ := os.Hostname()
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = "codex-runner"
	}
	return hostname + "-" + strconv.Itoa(os.Getpid())
}

func writeCodexRunnerHeartbeat(runnerID string) error {
	cfg := codexExecConfigFromEnv()
	payload := map[string]any{
		"ok":           true,
		"runnerId":     runnerID,
		"queuePath":    codexRunnerQueuePath(),
		"codexCwd":     cfg.CWD,
		"workspaceGit": codexWorkspaceHasGit(cfg.CWD),
		"time":         time.Now().UTC().Format(time.RFC3339Nano),
	}
	return writeJSONFileAtomically(codexRunnerHeartbeatPath(), "Codex runner heartbeat", payload)
}

func readinessCodexRunnerSnapshot() map[string]any {
	worker := configuredAgentThreadWorkerMode()
	snapshot := map[string]any{
		"worker":          worker,
		"runnerMode":      configuredCodexRunnerMode(),
		"queuePath":       codexRunnerQueuePath(),
		"heartbeatPath":   codexRunnerHeartbeatPath(),
		"callbackSecured": strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_TOKEN")) != "",
	}
	if worker != agentThreadWorkerCodexExec {
		snapshot["enabled"] = false
		return snapshot
	}
	snapshot["enabled"] = true
	raw, err := os.ReadFile(codexRunnerHeartbeatPath())
	if err != nil {
		snapshot["heartbeatOK"] = false
		snapshot["heartbeatError"] = "missing"
		return snapshot
	}
	var heartbeat struct {
		RunnerID     string `json:"runnerId"`
		CodexCWD     string `json:"codexCwd"`
		WorkspaceGit bool   `json:"workspaceGit"`
		Time         string `json:"time"`
	}
	if err := json.Unmarshal(raw, &heartbeat); err != nil {
		snapshot["heartbeatOK"] = false
		snapshot["heartbeatError"] = "invalid"
		return snapshot
	}
	parsed, err := time.Parse(time.RFC3339Nano, heartbeat.Time)
	if err != nil {
		snapshot["heartbeatOK"] = false
		snapshot["heartbeatError"] = "invalid_time"
		return snapshot
	}
	age := time.Since(parsed)
	snapshot["heartbeatOK"] = age <= defaultCodexRunnerStaleAfter
	snapshot["heartbeatAgeSeconds"] = int(age.Seconds())
	snapshot["runnerId"] = heartbeat.RunnerID
	snapshot["codexCwd"] = heartbeat.CodexCWD
	snapshot["workspaceGit"] = heartbeat.WorkspaceGit
	return snapshot
}

func codexWorkspaceHasGit(cwd string) bool {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(cwd, ".git"))
	return err == nil && info != nil
}

func sendCodexRunnerCallback(ctx context.Context, payload codexRunnerCallbackPayload) error {
	callbackURL := strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_CALLBACK_URL"))
	if callbackURL == "" {
		callbackURL = "http://meetingassist:3000/internal/codex/jobs/result"
	}
	token := strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_TOKEN"))
	if token == "" {
		return fmt.Errorf("BONFIRE_RUNNER_TOKEN is required for Codex runner callbacks")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Codex runner callback: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("create Codex runner callback request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send Codex runner callback: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Codex runner callback returned %s", resp.Status)
	}
	return nil
}

func internalCodexRunnerResultHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !runnerCallbackAuthorized(r) {
		writeSystemStatusJSON(w, r, http.StatusUnauthorized, map[string]any{
			"ok":    false,
			"error": "runner callback not authorized",
		})
		return
	}
	if kanbanApp == nil {
		writeSystemStatusJSON(w, r, http.StatusServiceUnavailable, map[string]any{
			"ok":    false,
			"error": "assistant is unavailable",
		})
		return
	}

	var payload codexRunnerCallbackPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&payload); err != nil {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "could not read runner callback",
		})
		return
	}
	artifactID := strings.TrimSpace(payload.ArtifactID)
	if artifactID == "" {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "artifact_id is required",
		})
		return
	}
	existing, exists := kanbanApp.osArtifactByID(artifactID)
	if !exists {
		writeSystemStatusJSON(w, r, http.StatusNotFound, map[string]any{
			"ok":    false,
			"error": "artifact not found",
		})
		return
	}
	expectedJobID := strings.TrimSpace(existing.Metadata["runnerJobId"])
	callbackJobID := strings.TrimSpace(payload.JobID)
	if expectedJobID != "" && callbackJobID != "" && callbackJobID != expectedJobID {
		writeSystemStatusJSON(w, r, http.StatusConflict, map[string]any{
			"ok":    false,
			"error": "runner job does not match artifact",
		})
		return
	}

	metadata := map[string]string{
		"runnerJobId": payload.JobID,
		"codexRunner": "callback",
	}
	if payload.Status != "" {
		metadata["status"] = payload.Status
		metadata["threadStatus"] = payload.Status
	}
	if payload.Error != "" {
		metadata["error"] = payload.Error
	}
	for key, value := range payload.Metadata {
		if strings.TrimSpace(value) != "" {
			metadata[key] = value
		}
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		text = existing.Text
	}

	title := existing.Metadata["title"]
	if strings.ToLower(strings.TrimSpace(payload.Status)) == codexJobStatusComplete && strings.TrimSpace(payload.Text) != "" {
		// An explicit runner-supplied title wins; otherwise derive from the
		// finished body so the prompt stops masquerading as the title.
		if runnerTitle := strings.TrimSpace(payload.Metadata["title"]); runnerTitle != "" {
			title = runnerTitle
		} else if derived := agentThreadDisplayTitle(text, title); derived != "" && derived != title {
			title = derived
			metadata["titleSource"] = "derived"
		}
	}
	// Grill runs landing through the queued-runner callback get the same
	// READINESS parse as the synchronous seams (runAgentThread and the
	// follow-up runner), so the readiness dial never depends on which worker
	// produced the run.
	if strings.ToLower(strings.TrimSpace(payload.Status)) == codexJobStatusComplete {
		stampReadinessMetadata(existing, firstNonEmptyString(existing.Metadata["mode"], existing.Kind), text, metadata)
	}

	artifact, changed, err := kanbanApp.updateOSArtifactWithMetadata(artifactID, title, text, "Codex runner", metadata)
	if err != nil {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	// Run ledger for the queued-runner lane too: codex-sidecar runs and
	// approved external-write jobs terminate through this callback instead of
	// the synchronous seam in runAgentThread, so the run_log line has to land
	// here or "what has Scout run for us?" recall silently misses them. The
	// ledger id derives from the thread id, so a retried callback dedupes in
	// the store; failed maps to the ledger's complete/error vocabulary (the
	// error summary reads the artifact's freshly stamped error metadata).
	switch strings.ToLower(strings.TrimSpace(payload.Status)) {
	case codexJobStatusComplete:
		kanbanApp.appendAgentRunLogEntryForArtifact(artifact, "complete", text)
	case codexJobStatusFailed:
		kanbanApp.appendAgentRunLogEntryForArtifact(artifact, "error", text)
	}

	// W0-5 lane metering (seat codex), sidecar path: the local exec meters each
	// job with a wall-clock defer (codex_runner.go), but sidecar-queued jobs run
	// in the codex-runner container and terminate through THIS callback, so the
	// codex seat only books the sidecar lane if we meter it here. Mirror the
	// local entry — duration-only, Estimated, model from BONFIRE_CODEX_MODEL so
	// an unpinned fossil still trips price_missing — deriving duration from the
	// artifact's launch stamp. Gate on the terminal statuses AND `changed` so a
	// retried callback cannot double-meter (the notify/deliver guards below do
	// the same). Non-blocking and error-safe, like every recordLLMUsage caller.
	switch strings.ToLower(strings.TrimSpace(payload.Status)) {
	case codexJobStatusComplete, codexJobStatusFailed:
		if changed {
			usageEntry := llmUsageEntry{
				Provider:  providerOpenAI,
				Model:     strings.TrimSpace(os.Getenv("BONFIRE_CODEX_MODEL")),
				Seat:      seatCodex,
				ThreadID:  firstNonEmptyString(strings.TrimSpace(payload.ThreadID), strings.TrimSpace(existing.Metadata["threadId"])),
				Estimated: true,
			}
			if startedAt, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(existing.Metadata["startedAt"])); parseErr == nil {
				usageEntry.DurationMS = time.Since(startedAt).Milliseconds()
			}
			if jobErr := strings.TrimSpace(payload.Error); jobErr != "" {
				usageEntry.Error = jobErr
			}
			recordLLMUsage(usageEntry)
		}
	}

	// Durable milestone: queued Codex jobs land through this callback instead
	// of the synchronous runner paths (agent_thread_runner.go), so the creator
	// notification has to happen here too. Gate on `changed` so a retried
	// identical callback cannot re-notify.
	statusMessage := codexRunnerStatusMessage(payload.Status, artifact)
	switch strings.ToLower(strings.TrimSpace(payload.Status)) {
	case codexJobStatusComplete, codexJobStatusFailed, codexJobStatusApprovalRequired:
		if changed {
			kanbanApp.notifyAgentThreadCreator(artifact, notificationKindAgent, agentThreadNotificationText(statusMessage, artifact))
			// Close the loop for queued Codex completions too; deliveredAt
			// makes a retried callback a no-op.
			if strings.ToLower(strings.TrimSpace(payload.Status)) == codexJobStatusComplete {
				kanbanApp.deliverArtifactToOrigin(artifact, firstNonEmptyString(artifact.Metadata["latestThreadRun"], artifact.Metadata["threadId"]))
			}
			// Board auto-advance for the queued-runner terminal seam too:
			// complete → Done, failed/approval_required → Blocked. The same
			// `changed` guard keeps a retried callback from re-syncing.
			kanbanApp.syncLinkedCardForArtifact(artifact, payload.Status)
			// Goal-engine linkage: a codex-executed subtask child (or the single
			// commit_push child) folds its terminal result back into the parent
			// plan. This is the codex-callback twin of the runAgentThread fold
			// hook — without it, execution-tagged subtasks strand the plan since
			// their completion never passes through the synchronous runner seam.
			// Fold on its own goroutine so a re-drive (which may make model calls)
			// never blocks this HTTP callback; no-op for non-goal artifacts.
			if parentID := strings.TrimSpace(artifact.Metadata["goalParentId"]); parentID != "" {
				switch strings.ToLower(strings.TrimSpace(payload.Status)) {
				case codexJobStatusComplete, codexJobStatusFailed:
					foldGoalChildAsync(kanbanApp, parentID, artifact.Metadata["goalSubtaskId"], artifact, payload.Status)
				}
			}
		}
	}

	actions := kanbanApp.osAssistantActions(firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["title"]), artifact.Metadata["mode"], artifact)
	broadcastSignedInKanbanEvent("memory", kanbanApp.memorySnapshotForClients(20))
	broadcastAssistantEvent("action", statusMessage, map[string]any{
		"tool":       "codex_runner",
		"artifact":   artifact,
		"actions":    actions,
		"voiceState": "listening",
	})
	writeSystemStatusJSON(w, r, http.StatusOK, map[string]any{
		"ok":       true,
		"artifact": artifact,
		"actions":  actions,
	})
}

func artifactRunnerActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}

	payload := struct {
		ID     string `json:"id"`
		Action string `json:"action"`
		Reason string `json:"reason"`
		// Choice is the human_checkpoint pick (index.html submitApproval): the
		// checkpoint's option label plus any appended notes. It MUST reach
		// resumeApprovedGoalWithChoice — dropping it turns every negative
		// option (hold, send back) into a silent proceed.
		Choice string `json:"choice"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read artifact action")
		return
	}
	artifactID := strings.TrimSpace(payload.ID)
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	if artifactID == "" || action == "" {
		writeAuthError(w, http.StatusBadRequest, "artifact id and action are required")
		return
	}
	artifact, exists := kanbanApp.osArtifactByID(artifactID)
	if !exists {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}

	switch action {
	case "approve":
		// External-write approval stays admin-gated, now with the card-069
		// heavy-lane consensus door: the admin approves alone, and two distinct
		// non-admin members together carry the same weight. A non-admin approve
		// on a PARKED artifact records an endorsement (202, n/2); the
		// endorsement that completes the pair falls through and executes the
		// exact approve path the admin would. A non-admin approve on anything
		// not parked stays 403 — approve is not a general-purpose action.
		endorsedToExecution := false
		if !isArtifactApprovalAdmin(user) {
			if !artifactAwaitingApproval(artifact.Metadata) {
				writeAuthError(w, http.StatusForbidden, "external-write approval is admin-only")
				return
			}
			endorsements, reached, err := kanbanApp.recordApprovalEndorsement(artifactID, user.Email)
			if err != nil {
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			if !reached {
				updated, _ := kanbanApp.osArtifactByID(artifactID)
				writeAuthJSON(w, http.StatusAccepted, map[string]any{
					"ok":       true,
					"artifact": updated,
					"endorsement": map[string]any{
						"count":    len(endorsements),
						"required": approvalConsensusRequired,
					},
					"message": fmt.Sprintf("endorsement recorded (%d/%d)", len(endorsements), approvalConsensusRequired),
				})
				return
			}
			endorsedToExecution = true
		}
		// A /goal artifact parked at its ship gate resumes through the goal
		// engine (commit_push), which ships exactly the command the gate
		// recorded — not a fresh codex job re-derived from the objective text.
		if artifact.Metadata["mode"] == "goal" {
			// The checkpoint choice rides through: a hold-action choice keeps
			// the goal parked and a revise-action choice re-queues its target —
			// resumeProcessCheckpoint's teeth are only real if the choice
			// survives the HTTP door.
			if err := kanbanApp.resumeApprovedGoalWithChoice(artifactID, user.Name, payload.Choice); err != nil {
				if endorsedToExecution {
					// The consensus was consumed but the execution failed:
					// un-consume it so a retry by either endorser can complete
					// the launch (resolveCodexProposal's revert discipline).
					kanbanApp.clearApprovalConsensusStamp(artifactID)
				}
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			updated, _ := kanbanApp.osArtifactByID(artifactID)
			// Only a proceed is a sign-off. A hold parked the goal; a revise
			// (send-back) asked for changes — including the disclosed
			// budget-spent fallback, where the founder asked for revision and
			// did NOT approve. Neither earns the durable approval stamp (it
			// unlocks sharing) or the "approved · sent" fan-out.
			if plan, ok := decodeGoalPlan(updated.Metadata["goalPlan"]); !ok || plan.Checkpoint == nil ||
				(!plan.Checkpoint.Held && plan.Checkpoint.LastAction != processCheckpointActionRevise) {
				// Durable human-approval record (share_links.go): reviewGate/status
				// keep moving as the resumed work runs, so the share gate keys on
				// this stamp instead.
				kanbanApp.stampArtifactHumanApproval(artifactID, user.Name)
				// Round-trip loop: fan the approval to the push channel + the
				// requester so their origin surface flips to "approved · sent".
				kanbanApp.recordApprovalOutcome(artifact, "approve", "", user.Name)
				updated, _ = kanbanApp.osArtifactByID(artifactID)
			}
			actions := kanbanApp.osAssistantActions(updated.Metadata["threadQuery"], updated.Metadata["mode"], updated)
			writeAuthJSON(w, http.StatusAccepted, map[string]any{
				"ok":       true,
				"artifact": updated,
				"actions":  actions,
			})
			return
		}
		updated, actions, err := kanbanApp.approveCodexArtifactExternalWrite(artifact, user.Name)
		if err != nil {
			if endorsedToExecution {
				kanbanApp.clearApprovalConsensusStamp(artifactID)
			}
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Durable human-approval record (share_links.go): survives the queued
		// job's later reviewGate/status rewrites.
		kanbanApp.stampArtifactHumanApproval(artifact.ID, user.Name)
		kanbanApp.recordApprovalOutcome(artifact, "approve", "", user.Name)
		writeAuthJSON(w, http.StatusAccepted, map[string]any{
			"ok":       true,
			"artifact": updated,
			"actions":  actions,
		})
	case "resume":
		// The blocked-goal recovery door: requester or admin only, goal mode
		// only. Resets exhausted subtasks and re-drives from where it stopped.
		if artifact.Metadata["mode"] != "goal" {
			writeAuthError(w, http.StatusBadRequest, "resume applies to goal runs")
			return
		}
		requester := strings.TrimSpace(artifact.Metadata["requestedBy"])
		if !isArtifactApprovalAdmin(user) && !strings.EqualFold(requester, normalizeAccountEmail(user.Email)) && !strings.EqualFold(requester, user.Name) {
			writeAuthError(w, http.StatusForbidden, "only the requester or an admin resumes a blocked run")
			return
		}
		if err := kanbanApp.resumeBlockedGoal(artifactID, user.Name); err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		updated, _ := kanbanApp.osArtifactByID(artifactID)
		writeAuthJSON(w, http.StatusAccepted, map[string]any{"ok": true, "artifact": updated})
		return
	case "reject":
		if !isArtifactApprovalAdmin(user) {
			writeAuthError(w, http.StatusForbidden, "external-write approval is admin-only")
			return
		}
		updated, actions, err := kanbanApp.rejectCodexArtifactGate(artifact, user.Name)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Round-trip loop: the requester's card returns with the admin's reason.
		kanbanApp.recordApprovalOutcome(artifact, "reject", payload.Reason, user.Name)
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"artifact": updated,
			"actions":  actions,
		})
	case "rerun":
		// Rerun is the same capability as POST /assistant/threads, which is
		// open to every signed-in user.
		mode := rerunThreadMode(artifact)
		query := firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["title"], compactAssistantLine(artifact.Text))
		// A rerun inherits the prior artifact's origin ONLY when delivery there
		// is still safe for THIS user (GATE-FINDINGS G2); everything else drops
		// to originKind tool, which keeps the creator-notification behavior.
		origin := kanbanApp.rerunOriginForUser(artifact, user.Email)
		thread, err := kanbanApp.launchAgentThreadWithOrigin(mode, query, user.Name, origin)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeAuthJSON(w, http.StatusAccepted, map[string]any{
			"ok":       true,
			"thread":   thread,
			"artifact": thread.Artifact,
			"actions":  thread.Actions,
		})
	default:
		writeAuthError(w, http.StatusBadRequest, "unknown artifact action")
	}
}

// rerunThreadMode resolves which thread mode a rerun relaunches with.
// metadata["mode"] carries the launch mode ("research", "grill", …);
// artifact.Kind is always "os_artifact", so reading Kind alone silently
// dropped every rerun to workflow mode and lost the research contract — the
// same firstNonEmptyString fallback the follow-up runner already uses
// (agent_thread_followup.go).
func rerunThreadMode(artifact meetingMemoryEntry) string {
	mode := normalizeAgentThreadMode(firstNonEmptyString(artifact.Metadata["mode"], artifact.Kind))
	if mode == "" {
		mode = "workflow"
	}
	return mode
}

// rerunOriginForUser decides which origin metadata a rerun may inherit from
// the stored artifact (GATE-FINDINGS G2 — conditional origin inheritance):
//   - channel origins survive only while the origin thread is still a public,
//     unarchived channel;
//   - private-thread origins survive only when the rerunning user OWNS the
//     origin thread (a non-owner rerun must never post into someone else's
//     private thread);
//   - room origins survive only while the origin meeting is still the active
//     meeting;
//   - everything else (tool, absent, unresolvable) drops to originKind tool,
//     which keeps the creator-notification-only completion behavior.
func (app *kanbanBoardApp) rerunOriginForUser(artifact meetingMemoryEntry, userEmail string) map[string]string {
	origin := map[string]string{"originKind": agentThreadOriginTool}
	if app == nil || app.memory == nil {
		return origin
	}
	originID := strings.TrimSpace(artifact.Metadata["originId"])
	switch strings.TrimSpace(artifact.Metadata["originKind"]) {
	case agentThreadOriginChannel, agentThreadOriginPrivateThread:
		if originID == "" {
			return origin
		}
		entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, originID)
		if !ok {
			return origin
		}
		thread, decoded := decodeScoutChatThreadEntry(entry)
		if !decoded || thread.ArchivedAt != "" {
			return origin
		}
		if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
			origin["originKind"] = agentThreadOriginChannel
			origin["originId"] = originID
			return origin
		}
		if normalizeAccountEmail(thread.OwnerEmail) != normalizeAccountEmail(userEmail) {
			return origin
		}
		origin["originKind"] = agentThreadOriginPrivateThread
		origin["originId"] = originID
	case agentThreadOriginRoom:
		originMeetingID := strings.TrimSpace(artifact.Metadata["originMeetingId"])
		if originMeetingID == "" || originMeetingID != app.memory.currentMeetingID(officeRoomID) {
			return origin
		}
		origin["originKind"] = agentThreadOriginRoom
		origin["originMeetingId"] = originMeetingID
		if originID != "" {
			origin["originId"] = originID
		}
	}
	return origin
}

func (app *kanbanBoardApp) approveCodexArtifactExternalWrite(artifact meetingMemoryEntry, approvedBy string) (meetingMemoryEntry, []osAssistantAction, error) {
	// Serialize the approve EXECUTION and re-read the artifact's CURRENT state:
	// the caller's copy was fetched at handler entry, before any concurrent
	// approve (a racing admin tap, or the endorsement that completes the
	// 2-member consensus) flipped the gate. Guarding on that stale copy lets
	// both approves pass reviewGate==approval_required and enqueue the SAME
	// external_write job twice. Under the lock exactly one caller observes the
	// parked gate and flips it to approved; the loser re-reads reviewGate=approved
	// and returns the not-waiting error below.
	approvalExecuteMu.Lock()
	defer approvalExecuteMu.Unlock()
	if current, exists := app.osArtifactByID(artifact.ID); exists {
		artifact = current
	}
	if artifact.Metadata["reviewGate"] != "approval_required" && artifact.Metadata["threadStatus"] != codexJobStatusApprovalRequired {
		return meetingMemoryEntry{}, nil, fmt.Errorf("artifact is not waiting for external-write approval")
	}
	mode := normalizeAgentThreadMode(artifact.Kind)
	if mode == "" {
		mode = "workflow"
	}
	threadID := firstNonEmptyString(artifact.Metadata["threadId"], fmt.Sprintf("agent-thread-%s-%d", mode, time.Now().UTC().UnixNano()))
	thread := scoutAgentThread{
		ID:       threadID,
		Mode:     mode,
		Query:    firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["title"], compactAssistantLine(artifact.Text)),
		Status:   codexJobStatusQueued,
		Artifact: artifact,
	}
	result, err := app.enqueueCodexAgentThreadJob(thread, codexJobAuthorityExternalWrite)
	if err != nil {
		return meetingMemoryEntry{}, nil, err
	}
	result.Metadata["approvedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	result.Metadata["approvedBy"] = canonicalRoomActorName(approvedBy)
	result.Metadata["reviewGate"] = "approved"
	result.Metadata["approvalAuthority"] = codexJobAuthorityExternalWrite
	app.updateQueuedAgentThread(thread, result)
	updated, exists := app.osArtifactByID(artifact.ID)
	if !exists {
		return meetingMemoryEntry{}, nil, fmt.Errorf("approved artifact was not found after queue update")
	}
	actions := app.osAssistantActions(thread.Query, mode, updated)
	return updated, actions, nil
}

func (app *kanbanBoardApp) rejectCodexArtifactGate(artifact meetingMemoryEntry, rejectedBy string) (meetingMemoryEntry, []osAssistantAction, error) {
	// Idempotency guard, mirroring approveCodexArtifactExternalWrite: a second
	// reject on an artifact that is no longer at the gate must not rewrite it
	// again, or the handler would double-fire the requester's "Rejected"
	// notification + push event. A double-clicked/resubmitted reject is a no-op.
	if !artifactAwaitingApproval(artifact.Metadata) {
		return meetingMemoryEntry{}, nil, fmt.Errorf("artifact is not waiting for external-write approval")
	}
	metadata := map[string]string{
		"status":          "rejected",
		"threadStatus":    "rejected",
		"goalStatus":      "rejected",
		"currentStage":    "gate_before_shipping",
		"progressPercent": "68",
		"reviewGate":      "rejected",
		"rejectedAt":      time.Now().UTC().Format(time.RFC3339Nano),
		"rejectedBy":      canonicalRoomActorName(rejectedBy),
	}
	updated, _, err := app.updateOSArtifactWithMetadata(artifact.ID, artifact.Metadata["title"], artifact.Text, rejectedBy, metadata)
	if err != nil {
		return meetingMemoryEntry{}, nil, err
	}
	actions := app.osAssistantActions(updated.Metadata["title"], updated.Kind, updated)
	broadcastSignedInKanbanEvent("memory", app.memorySnapshotForClients(20))
	broadcastAssistantEvent("action", assistantToolLabel(updated.Kind)+" thread rejected", map[string]any{
		"tool":       "codex_runner",
		"artifact":   updated,
		"actions":    actions,
		"voiceState": "listening",
	})
	return updated, actions, nil
}

func runnerCallbackAuthorized(r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_TOKEN"))
	if expected == "" {
		return false
	}
	provided := strings.TrimSpace(r.Header.Get("X-Bonfire-Runner-Token"))
	if provided == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			provided = strings.TrimSpace(auth[len("bearer "):])
		}
	}
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func codexRunnerStatusMessage(status string, artifact meetingMemoryEntry) string {
	label := assistantToolLabel(artifact.Kind)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case codexJobStatusRunning:
		return label + " thread running in Codex"
	case codexJobStatusFailed:
		return label + " thread needs attention"
	case codexJobStatusApprovalRequired:
		return label + " thread needs approval"
	default:
		return label + " thread complete"
	}
}

func mergeStringMaps(base map[string]string, overlay map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	for key, value := range overlay {
		if strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	return merged
}
