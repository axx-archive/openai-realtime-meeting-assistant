package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e9readiness"
)

type output struct {
	Readiness        e9readiness.ReadinessReport         `json:"readiness"`
	Worker           e9readiness.WorkerIsolationReport   `json:"workerIsolation"`
	ContractDrill    e9readiness.ContractDrillReceipt    `json:"contractDrill"`
	LocalIntegration e9readiness.LocalIntegrationReceipt `json:"localIntegration"`
}

func main() {
	readinessPath := flag.String("readiness", "deploy/e9/readiness-plan.json", "E9 readiness plan")
	workerPath := flag.String("worker", "deploy/e9/worker-isolation-policy.json", "worker isolation policy")
	composePath := flag.String("compose", "deploy/digitalocean/docker-compose.yml", "production-style Compose candidate")
	dockerfilePath := flag.String("dockerfile", "Dockerfile", "production image build candidate")
	mainPath := flag.String("main", "main.go", "server binary entrypoint candidate")
	codexRunnerPath := flag.String("codex-runner-source", "codex_runner.go", "legacy Codex runner implementation candidate")
	agentRunnerPath := flag.String("agent-runner-source", "agent_runner_iface.go", "agent runner selection candidate")
	drillPath := flag.String("drill", "deploy/e9/contract-drill.json", "manifest-only contract drill")
	moduleRoot := flag.String("module-root", ".", "repository root used by the temp-only local integration test")
	integrationTimeout := flag.Duration("integration-timeout", 2*time.Minute, "maximum duration for the local integration test")
	flag.Parse()

	readiness, err := load[e9readiness.ReadinessManifest](*readinessPath)
	if err != nil {
		fatal("readiness manifest", err)
	}
	report := e9readiness.EvaluateReadiness(readiness)
	if !report.ContractValid {
		fatal("readiness manifest", fmt.Errorf("%s", report.Blockers[0]))
	}
	worker, err := load[e9readiness.WorkerIsolationPolicy](*workerPath)
	if err != nil {
		fatal("worker isolation policy", err)
	}
	candidate := e9readiness.WorkerCandidateSources{}
	for _, source := range []struct {
		scope string
		path  string
		into  *[]byte
	}{
		{"worker Compose candidate", *composePath, &candidate.Compose},
		{"worker Dockerfile candidate", *dockerfilePath, &candidate.Dockerfile},
		{"worker binary candidate", *mainPath, &candidate.Main},
		{"Codex runner source candidate", *codexRunnerPath, &candidate.CodexRunner},
		{"agent runner source candidate", *agentRunnerPath, &candidate.AgentRunnerIface},
	} {
		content, readErr := os.ReadFile(source.path)
		if readErr != nil {
			fatal(source.scope, readErr)
		}
		*source.into = content
	}
	workerReport := e9readiness.EvaluateWorkerIsolation(worker, candidate)
	if !workerReport.ContractValid || !workerReport.DeploymentClosed {
		fatal("worker isolation", fmt.Errorf("%s", workerReport.Blockers[0]))
	}
	drill, err := load[e9readiness.ContractDrillManifest](*drillPath)
	if err != nil {
		fatal("drill manifest", err)
	}
	receipt, err := e9readiness.ExecuteContractDrill(drill)
	if err != nil {
		fatal("contract drill", err)
	}
	localIntegration, err := runLocalIntegration(*moduleRoot, *integrationTimeout)
	if err != nil {
		fatal("local integration drill", err)
	}
	encoded, err := json.MarshalIndent(output{Readiness: report, Worker: workerReport, ContractDrill: receipt, LocalIntegration: localIntegration}, "", "  ")
	if err != nil {
		fatal("encode report", err)
	}
	fmt.Println(string(encoded))
}

func runLocalIntegration(moduleRoot string, timeout time.Duration) (e9readiness.LocalIntegrationReceipt, error) {
	var zero e9readiness.LocalIntegrationReceipt
	if timeout <= 0 || timeout > 10*time.Minute {
		return zero, fmt.Errorf("integration timeout must be positive and no more than 10 minutes")
	}
	root, err := filepath.Abs(filepath.Clean(strings.TrimSpace(moduleRoot)))
	if err != nil {
		return zero, err
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return zero, fmt.Errorf("repository go.mod: %w", err)
	}
	tempRoot, err := os.MkdirTemp("", "meetingassist-e9-local-")
	if err != nil {
		return zero, err
	}
	defer os.RemoveAll(tempRoot)
	receiptPath := filepath.Join(tempRoot, "receipt.json")
	for _, path := range []string{
		filepath.Join(tempRoot, "home"),
		filepath.Join(tempRoot, "tmp"),
		filepath.Join(tempRoot, "go-build-cache"),
		filepath.Join(tempRoot, "xdg-cache"),
		filepath.Join(tempRoot, "xdg-config"),
		filepath.Join(tempRoot, "xdg-data"),
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return zero, fmt.Errorf("prepare hermetic child directory: %w", err)
		}
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		return zero, fmt.Errorf("find Go tool: %w", err)
	}
	moduleCache, err := e9GoModuleCache(goExecutable)
	if err != nil {
		return zero, err
	}
	overrides, err := e9readiness.LocalIntegrationApplicationEnvironment(tempRoot, receiptPath)
	if err != nil {
		return zero, err
	}
	for key, value := range map[string]string{
		"HOME":            filepath.Join(tempRoot, "home"),
		"TMPDIR":          filepath.Join(tempRoot, "tmp"),
		"TMP":             filepath.Join(tempRoot, "tmp"),
		"TEMP":            filepath.Join(tempRoot, "tmp"),
		"GOCACHE":         filepath.Join(tempRoot, "go-build-cache"),
		"GOTMPDIR":        filepath.Join(tempRoot, "tmp"),
		"GOMODCACHE":      moduleCache,
		"GOPROXY":         "off",
		"GOSUMDB":         "off",
		"GOTOOLCHAIN":     "local",
		"GOTELEMETRY":     "off",
		"GOENV":           "off",
		"GOWORK":          "off",
		"GOPRIVATE":       "",
		"GONOPROXY":       "",
		"GONOSUMDB":       "",
		"XDG_CACHE_HOME":  filepath.Join(tempRoot, "xdg-cache"),
		"XDG_CONFIG_HOME": filepath.Join(tempRoot, "xdg-config"),
		"XDG_DATA_HOME":   filepath.Join(tempRoot, "xdg-data"),
	} {
		overrides[key] = value
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, goExecutable, "test", "-count=1", "-run", "^"+e9readiness.LocalIntegrationTestName+"$", ".")
	command.Dir = root
	command.Env = e9LocalIntegrationEnvironment(os.Environ(), overrides)
	testOutput, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return zero, fmt.Errorf("integration test timed out: %w", ctx.Err())
	}
	if err != nil {
		return zero, fmt.Errorf("integration test failed: %w: %s", err, strings.TrimSpace(string(testOutput)))
	}
	receipt, err := load[e9readiness.LocalIntegrationReceipt](receiptPath)
	if err != nil {
		return zero, fmt.Errorf("read integration receipt: %w", err)
	}
	if err := e9readiness.ValidateLocalIntegrationReceipt(receipt); err != nil {
		return zero, err
	}
	return receipt, nil
}

func e9LocalIntegrationEnvironment(base []string, overrides map[string]string) []string {
	merged := make(map[string]string, len(overrides)+16)
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok && e9readiness.IsLocalIntegrationToolEnvironmentKey(key) {
			merged[key] = value
		}
	}
	for key, value := range overrides {
		merged[key] = value
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+merged[key])
	}
	return result
}

func e9GoModuleCache(goExecutable string) (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("resolve current user home for Go module cache: %w", err)
	}
	if currentUser == nil || strings.TrimSpace(currentUser.HomeDir) == "" {
		return "", errors.New("resolve current user home for Go module cache: home directory is empty")
	}
	command := exec.Command(goExecutable, "env", "GOMODCACHE")
	command.Env = []string{
		"GOENV=off",
		"GOTOOLCHAIN=local",
		"HOME=" + currentUser.HomeDir,
		"PATH=" + os.Getenv("PATH"),
	}
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("resolve Go module cache: %w", err)
	}
	path := filepath.Clean(strings.TrimSpace(string(output)))
	if path == "." || !filepath.IsAbs(path) {
		return "", errors.New("Go module cache path is not absolute")
	}
	return path, nil
}

func load[T any](path string) (T, error) {
	var zero T
	file, err := os.Open(path)
	if err != nil {
		return zero, err
	}
	defer file.Close()
	return e9readiness.DecodeStrict[T](file)
}

func fatal(scope string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", scope, err)
	os.Exit(1)
}
