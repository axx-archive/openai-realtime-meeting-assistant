package w2gate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const maxDriverOutputBytes = 8 << 20

type DriverExecutor interface {
	Execute(context.Context, string, []string) ([]byte, error)
}

type OSDriverExecutor struct{}

func (OSDriverExecutor) Execute(ctx context.Context, root string, argv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, ErrInvalidManifest
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = root
	command.Env = withoutEnvironment(os.Environ(), "BONFIRE_W2D_AUTHORITY_KEY_FILE", "BONFIRE_W2D_AUTHORITY_KEY_ID")
	var stdout, stderr limitedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: driver timeout: %v", ErrInconclusive, ctx.Err())
		}
		return nil, fmt.Errorf("%w: trusted driver failed: %v", ErrInconclusive, err)
	}
	if stdout.overflow || stderr.overflow {
		return nil, fmt.Errorf("%w: driver output exceeded limit", ErrInconclusive)
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (buffer *limitedBuffer) Write(raw []byte) (int, error) {
	if buffer.Len()+len(raw) > maxDriverOutputBytes {
		remaining := maxDriverOutputBytes - buffer.Len()
		if remaining > 0 {
			_, _ = buffer.Buffer.Write(raw[:remaining])
		}
		buffer.overflow = true
		return len(raw), nil
	}
	return buffer.Buffer.Write(raw)
}

type Runner struct {
	Root           string
	Manifest       Manifest
	ManifestDigest string
	Authority      HMACAuthority
	Executor       DriverExecutor
	ResolveRelease func(context.Context, string) (string, error)
	Now            func() time.Time
}

type ReceiptSetSummary struct {
	Schema           string   `json:"schema"`
	ReleaseCommit    string   `json:"releaseCommit"`
	CheckCount       int      `json:"checkCount"`
	ReceiptCount     int      `json:"receiptCount"`
	CandidateBlocked []string `json:"candidateBlocked"`
	W2DAccepted      bool     `json:"w2dAccepted"`
}

func (runner Runner) RunGate(ctx context.Context, gateID, releaseCommit string) (Receipt, string, error) {
	if err := runner.validate(); err != nil {
		return Receipt{}, "", err
	}
	if err := ValidateReleaseCommit(releaseCommit); err != nil {
		return Receipt{}, "", err
	}
	if err := runner.assertRelease(ctx, releaseCommit); err != nil {
		return Receipt{}, "", err
	}
	gate, ok := runner.Manifest.Gate(gateID)
	if !ok {
		return Receipt{}, "", fmt.Errorf("%w: unknown gate %q", ErrInvalidManifest, gateID)
	}
	corpus, err := LoadCorpus(runner.Root, gate, runner.Authority)
	if err != nil {
		return Receipt{}, "", err
	}
	argv, err := expandArgv(gate.Driver.Argv, releaseCommit)
	if err != nil {
		return Receipt{}, "", err
	}
	gateCtx, cancel := context.WithTimeout(ctx, time.Duration(gate.TimeoutSeconds)*time.Second)
	defer cancel()
	startedAt := runner.now()
	raw, err := runner.executor().Execute(gateCtx, runner.Root, argv)
	completedAt := runner.now()
	if err != nil {
		return Receipt{}, "", err
	}
	if gateCtx.Err() != nil {
		return Receipt{}, "", fmt.Errorf("%w: driver exceeded gate timeout", ErrInconclusive)
	}
	if len(raw) == 0 || len(raw) > maxDriverOutputBytes {
		return Receipt{}, "", ErrInconclusive
	}
	var observation Observation
	if err := decodeStrict(raw, &observation); err != nil {
		return Receipt{}, "", fmt.Errorf("%w: trusted driver output: %v", ErrInvalidInput, err)
	}
	if observation.KeyID != "" || observation.HMACSHA256 != "" {
		return Receipt{}, "", fmt.Errorf("%w: driver may not self-sign an observation", ErrInvalidInput)
	}
	if observation.Run.StartedAt.Before(startedAt.Add(-2*time.Minute)) || observation.Run.CompletedAt.After(completedAt.Add(2*time.Minute)) {
		return Receipt{}, "", fmt.Errorf("%w: driver run is not fresh to this execution", ErrInvalidInput)
	}
	if err := observation.validatePayload(gate, corpus, releaseCommit, completedAt); err != nil {
		return Receipt{}, "", err
	}
	if err := observation.Sign(runner.Authority); err != nil {
		return Receipt{}, "", err
	}
	raw, err = json.Marshal(observation)
	if err != nil {
		return Receipt{}, "", err
	}
	results, baselinePassed, candidatePassed, err := evaluateThresholds(gate, observation)
	if err != nil {
		return Receipt{}, "", err
	}
	if !baselinePassed {
		return Receipt{}, "", ErrBaselineFailed
	}

	observationPath := filepath.Join("artifacts", "w2-gates", releaseCommit, "observations", gate.ID+".json")
	absoluteObservationPath, err := rootedPath(runner.Root, observationPath)
	if err != nil {
		return Receipt{}, "", err
	}
	if err := writeEvidence(absoluteObservationPath, raw); err != nil {
		return Receipt{}, "", err
	}

	verdict := VerdictFail
	if candidatePassed {
		verdict = VerdictPass
	}
	receipt := Receipt{
		Schema: ReceiptSchema, GateID: gate.ID, GeneratedAt: completedAt, ReleaseCommit: releaseCommit,
		ManifestDigest: runner.ManifestDigest, CorpusDigest: gate.Corpus.Digest, ObservationPath: observationPath,
		ObservationDigest: digestBytes(raw), Run: observation.Run, EvidenceMode: observation.EvidenceMode,
		Baseline: observation.Baseline, Candidate: observation.Candidate, RecordOnly: observation.RecordOnly,
		Thresholds: results, Verdict: verdict, W2DAccepted: true, CandidateCanaryAllowed: candidatePassed,
		StopTriggered: !candidatePassed, FeatureFlag: gate.FeatureFlag, StopCondition: gate.StopCondition, RollbackCommand: gate.RollbackCommand,
	}
	if err := receipt.sign(runner.Authority); err != nil {
		return Receipt{}, "", err
	}
	if err := receipt.Validate(runner.ManifestDigest, gate, releaseCommit, runner.Authority, completedAt); err != nil {
		return Receipt{}, "", err
	}
	outputPath := strings.ReplaceAll(gate.Receipt.OutputPath, "{releaseCommit}", releaseCommit)
	path, err := rootedPath(runner.Root, outputPath)
	if err != nil {
		return Receipt{}, "", err
	}
	if err := writeJSON(path, receipt); err != nil {
		return Receipt{}, "", err
	}
	return receipt, outputPath, nil
}

func (runner Runner) RunCheck(ctx context.Context, checkID, releaseCommit string) (CheckReceipt, string, error) {
	if err := runner.validate(); err != nil {
		return CheckReceipt{}, "", err
	}
	if err := ValidateReleaseCommit(releaseCommit); err != nil {
		return CheckReceipt{}, "", err
	}
	if err := runner.assertRelease(ctx, releaseCommit); err != nil {
		return CheckReceipt{}, "", err
	}
	check, ok := runner.Manifest.Check(checkID)
	if !ok {
		return CheckReceipt{}, "", fmt.Errorf("%w: unknown check %q", ErrInvalidManifest, checkID)
	}
	argv, err := expandArgv(check.Driver.Argv, releaseCommit)
	if err != nil {
		return CheckReceipt{}, "", err
	}
	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(check.TimeoutSeconds)*time.Second)
	defer cancel()
	startedAt := runner.now()
	raw, err := runner.executor().Execute(checkCtx, runner.Root, argv)
	completedAt := runner.now()
	if err != nil {
		return CheckReceipt{}, "", err
	}
	if checkCtx.Err() != nil {
		return CheckReceipt{}, "", fmt.Errorf("%w: check exceeded timeout", ErrInconclusive)
	}
	run := RunEvidence{ID: "run-" + digestBytes(append([]byte(check.ID+releaseCommit), raw...))[:24], DriverID: check.Driver.ID, DriverArgvDigest: driverArgvDigest(check.Driver.Argv, releaseCommit), StartedAt: startedAt, CompletedAt: completedAt}
	receipt := CheckReceipt{Schema: CheckReceiptSchema, CheckID: check.ID, GeneratedAt: completedAt, ReleaseCommit: releaseCommit, ManifestDigest: runner.ManifestDigest, Run: run, OutputDigest: digestBytes(raw), Passed: true}
	if err := receipt.sign(runner.Authority); err != nil {
		return CheckReceipt{}, "", err
	}
	if err := receipt.Validate(runner.ManifestDigest, releaseCommit, check, runner.Authority, completedAt); err != nil {
		return CheckReceipt{}, "", err
	}
	outputPath := strings.ReplaceAll(check.ReceiptPath, "{releaseCommit}", releaseCommit)
	path, err := rootedPath(runner.Root, outputPath)
	if err != nil {
		return CheckReceipt{}, "", err
	}
	if err := writeJSON(path, receipt); err != nil {
		return CheckReceipt{}, "", err
	}
	return receipt, outputPath, nil
}

func (runner Runner) VerifyReceiptSet(releaseCommit string) (ReceiptSetSummary, error) {
	if err := runner.validate(); err != nil {
		return ReceiptSetSummary{}, err
	}
	if err := ValidateReleaseCommit(releaseCommit); err != nil {
		return ReceiptSetSummary{}, err
	}
	if err := runner.assertRelease(context.Background(), releaseCommit); err != nil {
		return ReceiptSetSummary{}, err
	}
	now := runner.now()
	summary := ReceiptSetSummary{Schema: "bonfire.w2.gate.receipt-set.v2", ReleaseCommit: releaseCommit, W2DAccepted: true}
	for _, check := range runner.Manifest.Checks {
		path, err := rootedPath(runner.Root, strings.ReplaceAll(check.ReceiptPath, "{releaseCommit}", releaseCommit))
		if err != nil {
			return ReceiptSetSummary{}, err
		}
		var receipt CheckReceipt
		if err := readStrictJSON(path, &receipt); err != nil {
			return ReceiptSetSummary{}, fmt.Errorf("%w: check %s: %v", ErrInconclusive, check.ID, err)
		}
		if err := receipt.Validate(runner.ManifestDigest, releaseCommit, check, runner.Authority, now); err != nil {
			return ReceiptSetSummary{}, fmt.Errorf("%w: check %s", err, check.ID)
		}
		summary.CheckCount++
	}
	for _, gate := range runner.Manifest.Gates {
		corpus, err := LoadCorpus(runner.Root, gate, runner.Authority)
		if err != nil {
			return ReceiptSetSummary{}, err
		}
		path, err := rootedPath(runner.Root, strings.ReplaceAll(gate.Receipt.OutputPath, "{releaseCommit}", releaseCommit))
		if err != nil {
			return ReceiptSetSummary{}, err
		}
		var receipt Receipt
		if err := readStrictJSON(path, &receipt); err != nil {
			return ReceiptSetSummary{}, fmt.Errorf("%w: receipt %s: %v", ErrInconclusive, gate.ID, err)
		}
		if err := receipt.Validate(runner.ManifestDigest, gate, releaseCommit, runner.Authority, now); err != nil {
			return ReceiptSetSummary{}, fmt.Errorf("%w: receipt %s", err, gate.ID)
		}
		observationPath, err := rootedPath(runner.Root, receipt.ObservationPath)
		if err != nil {
			return ReceiptSetSummary{}, ErrReceiptInvalid
		}
		raw, err := secureReadFile(observationPath)
		if err != nil || digestBytes(raw) != receipt.ObservationDigest {
			return ReceiptSetSummary{}, ErrReceiptInvalid
		}
		var observation Observation
		// Observation freshness is a collection-time admission rule. A signed
		// receipt retains that evidence durably, so historical set verification
		// evaluates the observation at the receipt generation time rather than
		// incorrectly reapplying its short network challenge expiry at `now`.
		if decodeStrict(raw, &observation) != nil || observation.Validate(gate, corpus, releaseCommit, runner.Authority, receipt.GeneratedAt) != nil || observation.Run != receipt.Run ||
			!reflect.DeepEqual(observation.Baseline, receipt.Baseline) || !reflect.DeepEqual(observation.Candidate, receipt.Candidate) || !reflect.DeepEqual(observation.RecordOnly, receipt.RecordOnly) {
			return ReceiptSetSummary{}, ErrReceiptInvalid
		}
		summary.ReceiptCount++
		if !receipt.CandidateCanaryAllowed {
			summary.CandidateBlocked = append(summary.CandidateBlocked, gate.ID)
		}
	}
	return summary, nil
}

func (runner Runner) validate() error {
	if strings.TrimSpace(runner.Root) == "" || !isStrongDigest(runner.ManifestDigest) || runner.Authority.Validate() != nil {
		return ErrInvalidManifest
	}
	if err := runner.Manifest.Validate(); err != nil {
		return err
	}
	if _, err := runner.Manifest.VerifyEvidenceFiles(runner.Root); err != nil {
		return err
	}
	for _, gate := range runner.Manifest.Gates {
		path, err := rootedPath(runner.Root, gate.Receipt.SchemaPath)
		if err != nil {
			return err
		}
		raw, err := secureReadFile(path)
		if err != nil || digestBytes(raw) != gate.Receipt.SchemaDigest {
			return fmt.Errorf("%w: receipt schema digest mismatch", ErrInvalidManifest)
		}
	}
	return nil
}

func (runner Runner) executor() DriverExecutor {
	if runner.Executor != nil {
		return runner.Executor
	}
	return OSDriverExecutor{}
}

func (runner Runner) assertRelease(ctx context.Context, releaseCommit string) error {
	resolver := runner.ResolveRelease
	if resolver == nil {
		resolver = func(ctx context.Context, root string) (string, error) {
			command := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
			command.Dir = root
			raw, err := command.Output()
			return strings.TrimSpace(string(raw)), err
		}
	}
	actual, err := resolver(ctx, runner.Root)
	if err != nil || actual != releaseCommit {
		return fmt.Errorf("%w: runner checkout is not release commit %s", ErrInconclusive, releaseCommit)
	}
	if runner.ResolveRelease == nil {
		return VerifyReleaseCheckout(ctx, runner.Root, releaseCommit)
	}
	return nil
}

func VerifyReleaseCheckout(ctx context.Context, root, releaseCommit string) error {
	if ValidateReleaseCommit(releaseCommit) != nil {
		return ErrInconclusive
	}
	head := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	head.Dir = root
	raw, err := head.Output()
	if err != nil || strings.TrimSpace(string(raw)) != releaseCommit {
		return fmt.Errorf("%w: checkout HEAD is not release commit %s", ErrInconclusive, releaseCommit)
	}
	status := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=all")
	status.Dir = root
	raw, statusErr := status.Output()
	if statusErr != nil || len(bytes.TrimSpace(raw)) != 0 {
		return fmt.Errorf("%w: release checkout contains modified or untracked executable inputs", ErrInconclusive)
	}
	committed := exec.CommandContext(ctx, "git", "diff-index", "--quiet", releaseCommit, "--")
	committed.Dir = root
	if committed.Run() != nil {
		return fmt.Errorf("%w: working bytes differ from release commit %s", ErrInconclusive, releaseCommit)
	}
	return nil
}

func withoutEnvironment(environment []string, names ...string) []string {
	blocked := make(map[string]bool, len(names))
	for _, name := range names {
		blocked[name] = true
	}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		name := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			name = entry[:index]
		}
		if !blocked[name] {
			result = append(result, entry)
		}
	}
	return result
}
func (runner Runner) now() time.Time {
	if runner.Now != nil {
		return runner.Now().UTC()
	}
	return time.Now().UTC()
}

func evaluateThresholds(gate GateDefinition, observation Observation) ([]ThresholdResult, bool, bool, error) {
	results := make([]ThresholdResult, 0, len(gate.Thresholds))
	baselinePassed, candidatePassed := true, true
	for _, threshold := range gate.Thresholds {
		actual, ok := metricValue(observation, threshold.Snapshot, threshold.Metric)
		if !ok {
			return nil, false, false, fmt.Errorf("%w: missing %s metric %q", ErrInconclusive, threshold.Snapshot, threshold.Metric)
		}
		expected := 0.0
		if threshold.Value != nil {
			expected = *threshold.Value
		} else {
			reference, found := metricValue(observation, threshold.Reference.Snapshot, threshold.Reference.Metric)
			if !found {
				return nil, false, false, fmt.Errorf("%w: missing reference metric %q", ErrInconclusive, threshold.Reference.Metric)
			}
			expected = reference*threshold.Reference.Multiplier + threshold.Reference.Offset
			if !metricInDomain(threshold.Metric, expected) {
				return nil, false, false, fmt.Errorf("%w: derived threshold outside metric domain", ErrInvalidInput)
			}
		}
		passed := compareMetric(actual, expected, threshold.Operator)
		results = append(results, ThresholdResult{ID: threshold.ID, Snapshot: threshold.Snapshot, Metric: threshold.Metric, Operator: threshold.Operator, Actual: actual, Expected: expected, Passed: passed})
		if strings.HasPrefix(threshold.ID, "baseline-") {
			baselinePassed = baselinePassed && passed
		} else {
			candidatePassed = candidatePassed && passed
		}
	}
	return results, baselinePassed, candidatePassed, nil
}

func metricValue(observation Observation, snapshot, metric string) (float64, bool) {
	switch snapshot {
	case "baseline":
		value, ok := observation.Baseline.Metrics[metric]
		return value, ok
	case "candidate":
		value, ok := observation.Candidate.Metrics[metric]
		return value, ok
	default:
		id := strings.TrimPrefix(snapshot, "recordOnly:")
		for _, item := range observation.RecordOnly {
			if item.Identity.ID == id {
				value, ok := item.Metrics[metric]
				return value, ok
			}
		}
		return 0, false
	}
}

func compareMetric(actual, expected float64, operator string) bool {
	switch operator {
	case "gt":
		return actual > expected
	case "gte":
		return actual >= expected
	case "lt":
		return actual < expected
	case "lte":
		return actual <= expected
	case "eq":
		return actual == expected
	default:
		return false
	}
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeEvidence(path, append(raw, '\n'))
}

func writeEvidence(path string, raw []byte) error {
	if err := mkdirAllNoSymlink(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := rejectSymlinkComponents(filepath.Dir(path), false); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("final evidence symlink is forbidden")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".w2-gate-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := rejectSymlinkComponents(filepath.Dir(path), false); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("final evidence symlink is forbidden")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporaryInfo, err := os.Lstat(temporaryPath)
	if err != nil || !temporaryInfo.Mode().IsRegular() || temporaryInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("temporary evidence custody changed")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	if _, err := secureReadFile(path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func readStrictJSON(path string, target any) error {
	raw, err := secureReadFile(path)
	if err != nil {
		return err
	}
	return decodeStrict(raw, target)
}

func mkdirAllNoSymlink(path string, mode os.FileMode) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	remainder := strings.TrimPrefix(absolute, volume)
	current := volume + string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(remainder, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Mkdir(current, mode); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("evidence directory component is not a real directory: %s", current)
		}
	}
	return nil
}
func IsExecutionTimeout(err error) bool { return errors.Is(err, context.DeadlineExceeded) }
