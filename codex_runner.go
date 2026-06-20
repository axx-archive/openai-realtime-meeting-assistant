package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	agentThreadWorkerOpenAI    = "openai_text_response"
	agentThreadWorkerCodexExec = "codex_exec"

	codexRunnerModeSidecar   = "sidecar_queue"
	codexRunnerModeLocalExec = "local_exec"

	defaultCodexExecTimeout        = 20 * time.Minute
	defaultCodexExecMaxOutputBytes = 256 * 1024
)

type codexExecConfig struct {
	Command        string
	CWD            string
	Sandbox        string
	ApprovalPolicy string
	Profile        string
	Model          string
	Reasoning      string
	Timeout        time.Duration
	MaxOutputBytes int64
	Search         bool
	Ephemeral      bool
	SkipGitCheck   bool
}

type codexExecResult struct {
	FinalMessage string
	Stdout       string
	Stderr       string
}

var runCodexExecCommand = runCodexExecCommandContext

func configuredAgentThreadWorkerMode() string {
	if boolEnv("BONFIRE_CODEX_AGENT_THREADS") {
		return agentThreadWorkerCodexExec
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("BONFIRE_AGENT_THREAD_WORKER"))) {
	case "codex", "codex-exec", "codex_exec":
		return agentThreadWorkerCodexExec
	case "", "openai", "responses", "text", "text-response", "text_response":
		return agentThreadWorkerOpenAI
	default:
		return agentThreadWorkerOpenAI
	}
}

func configuredAgentThreadWorkerName() string {
	return configuredAgentThreadWorkerMode()
}

func configuredCodexRunnerMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BONFIRE_CODEX_RUNNER_MODE"))) {
	case "local", "local-exec", "local_exec", "in-process", "in_process":
		return codexRunnerModeLocalExec
	default:
		return codexRunnerModeSidecar
	}
}

func agentThreadWorkerBoundary(worker string) string {
	switch worker {
	case agentThreadWorkerCodexExec:
		if configuredCodexRunnerMode() == codexRunnerModeLocalExec {
			return "codex_cli_noninteractive"
		}
		return "codex_sidecar_queue"
	default:
		return "responses_artifact_writer"
	}
}

func agentThreadWorkerInstruction() string {
	switch configuredAgentThreadWorkerMode() {
	case agentThreadWorkerCodexExec:
		return "launch_agent_thread enqueues a job for the sidecar Codex runner with explicit sandbox, approval, and working-directory settings. Realtime stays the fast voice/UI control layer while Codex performs slower research, code, browser/tool, test, or gated workflow work and writes evidence back to the artifact. Commit, push, deploy, SSH, email, APIs, and production mutations require an explicit approval gate."
	default:
		return "launch_agent_thread starts the current lightweight Responses worker, which writes a structured goal workflow artifact but cannot run Codex, browser automation, SSH, shell commands, tests, or deploys. Treat external Codex execution as a handoff until BONFIRE_AGENT_THREAD_WORKER=codex_exec is configured."
	}
}

func codexExecConfigFromEnv() codexExecConfig {
	cwd := strings.TrimSpace(os.Getenv("BONFIRE_CODEX_CWD"))
	if cwd == "" {
		if current, err := os.Getwd(); err == nil {
			cwd = current
		}
	}
	if cwd == "" {
		cwd = "."
	}

	maxOutputBytes := int64(positiveIntEnv("BONFIRE_CODEX_MAX_OUTPUT_BYTES", defaultCodexExecMaxOutputBytes))

	return codexExecConfig{
		Command:        getenvDefault("BONFIRE_CODEX_COMMAND", "codex"),
		CWD:            filepath.Clean(cwd),
		Sandbox:        normalizeCodexSandbox(getenvDefault("BONFIRE_CODEX_SANDBOX", "workspace-write")),
		ApprovalPolicy: normalizeCodexApprovalPolicy(getenvDefault("BONFIRE_CODEX_APPROVAL_POLICY", "never")),
		Profile:        strings.TrimSpace(os.Getenv("BONFIRE_CODEX_PROFILE")),
		Model:          strings.TrimSpace(os.Getenv("BONFIRE_CODEX_MODEL")),
		Reasoning:      normalizeCodexReasoningEffort(getenvDefault("BONFIRE_CODEX_REASONING_EFFORT", "high")),
		Timeout:        durationEnv("BONFIRE_CODEX_TIMEOUT", defaultCodexExecTimeout, 30*time.Second),
		MaxOutputBytes: maxOutputBytes,
		Search:         boolEnv("BONFIRE_CODEX_SEARCH"),
		Ephemeral:      boolEnv("BONFIRE_CODEX_EPHEMERAL"),
		SkipGitCheck:   boolEnv("BONFIRE_CODEX_SKIP_GIT_REPO_CHECK"),
	}
}

func normalizeCodexSandbox(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "read-only", "readonly", "read_only":
		return "read-only"
	case "danger-full-access", "danger", "full", "full-access", "full_access":
		return "danger-full-access"
	default:
		return "workspace-write"
	}
}

func normalizeCodexApprovalPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "untrusted":
		return "untrusted"
	case "on-request", "on_request", "request":
		return "on-request"
	default:
		return "never"
	}
}

func normalizeCodexReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "high"
	}
}

func durationEnv(name string, fallback time.Duration, minimum time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed < minimum {
		return fallback
	}

	return parsed
}

func (app *kanbanBoardApp) produceCodexAgentThreadArtifact(ctx context.Context, thread scoutAgentThread) (agentThreadWorkerResult, error) {
	cfg := codexExecConfigFromEnv()
	authority := codexJobAuthorityForThread(thread)
	if authority == codexJobAuthorityExternalWrite {
		return codexApprovalRequiredResult(thread, authority), nil
	}
	cfg = codexExecConfigForAuthority(cfg, authority, thread.Mode)
	prompt := app.buildCodexAgentThreadPrompt(thread, time.Now(), authority)
	result, err := runCodexExecCommand(ctx, cfg, prompt)
	metadata := map[string]string{
		"worker":              agentThreadWorkerCodexExec,
		"workerBoundary":      agentThreadWorkerBoundary(agentThreadWorkerCodexExec),
		"authority":           authority,
		"codexCommand":        cfg.Command,
		"codexCwd":            cfg.CWD,
		"codexSandbox":        cfg.Sandbox,
		"codexApprovalPolicy": cfg.ApprovalPolicy,
		"codexReasoning":      cfg.Reasoning,
		"codexSearch":         strconv.FormatBool(cfg.Search),
		"codexSkipGitCheck":   strconv.FormatBool(cfg.SkipGitCheck),
	}
	if cfg.Profile != "" {
		metadata["codexProfile"] = cfg.Profile
	}
	if cfg.Model != "" {
		metadata["codexModel"] = cfg.Model
	}
	if err != nil {
		return agentThreadWorkerResult{Metadata: metadata}, err
	}

	output := strings.TrimSpace(result.FinalMessage)
	if output == "" {
		return agentThreadWorkerResult{Metadata: metadata}, fmt.Errorf("Codex worker produced no final message")
	}
	metadata["codexFinalBytes"] = strconv.Itoa(len(output))

	return agentThreadWorkerResult{
		Text:     appendCodexWorkerEvidence(output, cfg),
		Metadata: metadata,
		Terminal: true,
	}, nil
}

func (app *kanbanBoardApp) buildCodexAgentThreadPrompt(thread scoutAgentThread, now time.Time, authority string) string {
	board := app.snapshotState()
	memory := app.memorySnapshotForClients(20)
	contextLine := boardAndMemoryContextLine(board, memory)
	authority = normalizeCodexJobAuthority(authority)

	var builder strings.Builder
	builder.WriteString("You are the Codex execution worker launched by Bonfire OS / Scout.\n\n")
	builder.WriteString("User-visible worker mode: ")
	builder.WriteString(assistantToolLabel(thread.Mode))
	builder.WriteString("\nThread id: ")
	builder.WriteString(thread.ID)
	builder.WriteString("\nAuthority class: ")
	builder.WriteString(authority)
	builder.WriteString("\nCurrent time: ")
	builder.WriteString(now.Format(time.RFC3339))
	builder.WriteString("\nUser request: ")
	builder.WriteString(thread.Query)
	builder.WriteString("\n\nFollow this goal loop in order:\n")
	builder.WriteString("1. Identify and restate the goal.\n")
	builder.WriteString("2. Decompose the work.\n")
	builder.WriteString("3. Assign the right agents.\n")
	builder.WriteString("4. Coordinate dependencies.\n")
	builder.WriteString("5. Execute in order.\n")
	builder.WriteString("6. Review against the original goal.\n")
	builder.WriteString("7. Gate before shipping or publishing.\n")
	builder.WriteString("8. Save what worked.\n")
	builder.WriteString("9. Report only what matters.\n")
	builder.WriteString("10. Verify the goal as completed or name the blocker.\n\n")
	builder.WriteString("Use Codex subagents when the work benefits from parallel research, implementation, review, or evidence gathering. For a research/report request, default to at least a research agent, synthesis agent, and skeptical review agent; wait for them and consolidate their findings. If the environment blocks subagents or tools, say that plainly in the final artifact.\n\n")
	builder.WriteString("Safety and evidence rules:\n")
	builder.WriteString("- Use only tools available in this Codex runner environment.\n")
	builder.WriteString("- Do not claim browser, SSH, filesystem, test, deployment, or external service work unless you actually performed it and can cite the evidence.\n")
	builder.WriteString("- The current authority class is ")
	builder.WriteString(authority)
	builder.WriteString(". read_only may inspect and report; workspace_write may edit/check files inside the workspace; external_write is required for commit, push, deploy, SSH, external APIs, email, or production mutations.\n")
	builder.WriteString("- Do not commit, push, deploy, SSH, send email, call external write APIs, or mutate production systems unless this job has explicit external_write approval for that exact side effect.\n")
	builder.WriteString("- If an external side effect is needed, stop and put a Gate line that starts with EXTERNAL_WRITE_APPROVAL_REQUIRED: followed by the precise command/action you would run after approval.\n")
	builder.WriteString("- If credentials, access, approvals, network, or sandbox restrictions block the work, leave the artifact useful and mark the gate as blocked.\n\n")
	builder.WriteString("Mode-specific artifact contract:\n")
	builder.WriteString(agentThreadModeContract(thread.Mode))
	builder.WriteString("\n\n")
	builder.WriteString("Bonfire OS context: ")
	builder.WriteString(contextLine)
	builder.WriteString("\n\nRecent durable memory:\n")
	for _, entry := range memory {
		builder.WriteString("- ")
		builder.WriteString(entry.Kind)
		if title := strings.TrimSpace(entry.Metadata["title"]); title != "" {
			builder.WriteString(" / ")
			builder.WriteString(title)
		}
		builder.WriteString(": ")
		builder.WriteString(compactAssistantLine(entry.Text))
		builder.WriteByte('\n')
	}
	builder.WriteString("\nReturn a polished Markdown artifact with stable headings for: Vision, Goal, Context used, Work decomposition, Agent assignment, Execution log, Review, Gate, What worked, Report, Next moves, Verification, and Codex worker evidence. Keep the output readable in an artifact viewer, with short paragraphs or bullets under each heading.\n")

	return builder.String()
}

func appendCodexWorkerEvidence(output string, cfg codexExecConfig) string {
	if strings.Contains(strings.ToLower(output), "codex worker evidence") {
		return output
	}

	lines := []string{
		output,
		"",
		"## Codex worker evidence",
		"- Worker: codex exec",
		"- Working directory: " + cfg.CWD,
		"- Sandbox: " + cfg.Sandbox,
		"- Approval policy: " + cfg.ApprovalPolicy,
	}
	if cfg.Model != "" {
		lines = append(lines, "- Model: "+cfg.Model)
	}
	if cfg.Reasoning != "" {
		lines = append(lines, "- Reasoning effort: "+cfg.Reasoning)
	}
	if cfg.Search {
		lines = append(lines, "- Live search: enabled")
	}

	return strings.Join(lines, "\n")
}

func runCodexExecCommandContext(ctx context.Context, cfg codexExecConfig, prompt string) (codexExecResult, error) {
	outputFile, err := os.CreateTemp("", "bonfire-codex-thread-*.md")
	if err != nil {
		return codexExecResult{}, fmt.Errorf("create Codex output file: %w", err)
	}
	outputPath := outputFile.Name()
	if err := outputFile.Close(); err != nil {
		_ = os.Remove(outputPath)
		return codexExecResult{}, fmt.Errorf("close Codex output file: %w", err)
	}
	defer os.Remove(outputPath)

	args := []string{}
	if cfg.Search {
		args = append(args, "--search")
	}
	args = append(args,
		"exec",
		"--cd", cfg.CWD,
		"--sandbox", cfg.Sandbox,
		"--output-last-message", outputPath,
		"-c", fmt.Sprintf("approval_policy=%q", cfg.ApprovalPolicy),
		"-c", fmt.Sprintf("model_reasoning_effort=%q", cfg.Reasoning),
	)
	if cfg.Profile != "" {
		args = append(args, "--profile", cfg.Profile)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.Ephemeral {
		args = append(args, "--ephemeral")
	}
	if cfg.SkipGitCheck {
		args = append(args, "--skip-git-repo-check")
	}
	args = append(args, "-")

	command := exec.CommandContext(ctx, cfg.Command, args...)
	command.Dir = cfg.CWD
	command.Stdin = strings.NewReader(prompt)
	command.Env = codexExecEnvironment(os.Environ())

	var stdout cappedBuffer
	var stderr cappedBuffer
	stdout.Limit = cfg.MaxOutputBytes
	stderr.Limit = cfg.MaxOutputBytes
	command.Stdout = &stdout
	command.Stderr = &stderr

	err = command.Run()
	finalMessage, readErr := readLimitedTextFile(outputPath, cfg.MaxOutputBytes)
	result := codexExecResult{
		FinalMessage: strings.TrimSpace(firstNonEmptyString(finalMessage, stdout.String())),
		Stdout:       stdout.String(),
		Stderr:       stderr.String(),
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("Codex worker timed out or was canceled: %w", ctx.Err())
	}
	if err != nil {
		return result, fmt.Errorf("run codex exec: %w", err)
	}
	if readErr != nil {
		return result, readErr
	}
	if strings.TrimSpace(result.FinalMessage) == "" {
		return result, fmt.Errorf("Codex worker produced no final message")
	}

	return result, nil
}

func codexExecEnvironment(base []string) []string {
	if envValue(base, "CODEX_API_KEY") != "" {
		return base
	}
	openAIKey := envValue(base, "OPENAI_API_KEY")
	if strings.TrimSpace(openAIKey) == "" {
		return base
	}
	return append(base, "CODEX_API_KEY="+openAIKey)
}

func envValue(env []string, name string) string {
	prefix := name + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(entry, prefix))
		}
	}
	return ""
}

type cappedBuffer struct {
	bytes.Buffer
	Limit int64
}

func (buffer *cappedBuffer) Write(chunk []byte) (int, error) {
	if buffer.Limit <= 0 {
		return len(chunk), nil
	}
	remaining := buffer.Limit - int64(buffer.Buffer.Len())
	if remaining > 0 {
		if int64(len(chunk)) > remaining {
			_, _ = buffer.Buffer.Write(chunk[:remaining])
		} else {
			_, _ = buffer.Buffer.Write(chunk)
		}
	}

	return len(chunk), nil
}

func readLimitedTextFile(path string, maxBytes int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read Codex output: %w", err)
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read Codex output: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		raw = raw[:maxBytes]
	}

	return strings.TrimSpace(string(raw)), nil
}
