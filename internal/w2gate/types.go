package w2gate

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ManifestSchema     = "bonfire.w2.gates.v2"
	ObservationSchema  = "bonfire.w2.gate.observation.v2"
	ReceiptSchema      = "bonfire.w2.gate.receipt.v2"
	CheckReceiptSchema = "bonfire.w2.integrated-check.receipt.v1"
	CorpusSchema       = "bonfire.w2.corpus-manifest.v2"

	EvidenceModeFixture = "deterministic_fixture"
	EvidenceModeLive    = "live_provider"

	VerdictPass = "pass"
	VerdictFail = "fail"

	DomainUnitInterval = "unit_interval"
	DomainNonnegative  = "nonnegative"
)

var (
	ErrInvalidManifest  = errors.New("invalid W2 gate manifest")
	ErrInvalidInput     = errors.New("invalid W2 gate observation")
	ErrCorpusNotFrozen  = errors.New("W2 gate corpus is not frozen")
	ErrInconclusive     = errors.New("W2 gate observation is missing or inconclusive")
	ErrBaselineFailed   = errors.New("W2 product-correctness baseline failed")
	ErrReceiptInvalid   = errors.New("invalid W2 gate receipt")
	ErrSignatureInvalid = errors.New("invalid W2 evidence signature")

	releaseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	environmentPattern   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	identifierPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{2,127}$`)
	keyIDPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{2,127}$`)
)

var requiredGateIDs = []string{
	"w2d-stt-fidelity",
	"w2d-brain-commitments",
	"w2d-proposal-kickoff",
	"w2d-recall-quality",
	"w2d-brain-fleet-parity",
	"w2d-board-fidelity",
	"w2d-realtime-voice",
	"w2d-review-shadow",
	"w2d-embedding-retrieval",
}

var requiredCheckIDs = []string{
	"integrated-normal",
	"consolidated-race",
	"migration-replay",
	"two-room-live-soak",
	"production-recall-replay",
	"workflow-pilots",
}

type Manifest struct {
	Schema   string                      `json:"schema"`
	Evidence EvidenceContract            `json:"evidence"`
	Checks   []IntegratedCheckDefinition `json:"checks"`
	Gates    []GateDefinition            `json:"gates"`
}

// EvidenceContract pins the independently-custodied collector implementation,
// its verification key, and the price catalog used to derive every reported
// cost. File digests are checked against the release checkout before any driver
// is allowed to run.
type EvidenceContract struct {
	CollectorImplementation []CheckedFile       `json:"collectorImplementation"`
	CollectorKey            Ed25519KeyReference `json:"collectorKey"`
	PriceCatalog            CheckedFile         `json:"priceCatalog"`
	PriceKey                Ed25519KeyReference `json:"priceKey"`
}

type CheckedFile struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type Ed25519KeyReference struct {
	KeyID  string `json:"keyId"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type DriverSpec struct {
	ID   string   `json:"id"`
	Argv []string `json:"argv"`
}

type IntegratedCheckDefinition struct {
	ID             string     `json:"id"`
	Description    string     `json:"description"`
	Driver         DriverSpec `json:"driver"`
	TimeoutSeconds int        `json:"timeoutSeconds"`
	ReceiptPath    string     `json:"receiptPath"`
}

type GateDefinition struct {
	ID                   string               `json:"id"`
	Description          string               `json:"description"`
	Driver               DriverSpec           `json:"driver"`
	Corpus               CorpusReference      `json:"corpus"`
	TimeoutSeconds       int                  `json:"timeoutSeconds"`
	Thresholds           []MetricThreshold    `json:"thresholds"`
	Receipt              ReceiptSpecification `json:"receipt"`
	ReleaseCommit        string               `json:"releaseCommit"`
	FeatureFlag          string               `json:"featureFlag"`
	StopCondition        string               `json:"stopCondition"`
	RollbackCommand      string               `json:"rollbackCommand"`
	Baseline             EvaluationIdentity   `json:"baseline"`
	Candidate            EvaluationIdentity   `json:"candidate"`
	RecordOnly           []EvaluationIdentity `json:"recordOnly,omitempty"`
	RequiredEvidenceMode string               `json:"requiredEvidenceMode"`
}

type CorpusReference struct {
	ManifestPath string `json:"manifestPath"`
	Digest       string `json:"digest"`
	MinimumItems int    `json:"minimumItems"`
}

type ReceiptSpecification struct {
	OutputPath   string `json:"outputPath"`
	Schema       string `json:"schema"`
	SchemaPath   string `json:"schemaPath"`
	SchemaDigest string `json:"schemaDigest"`
}

type EvaluationIdentity struct {
	ID       string `json:"id"`
	Lane     string `json:"lane"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
}

type MetricReference struct {
	Snapshot   string  `json:"snapshot"`
	Metric     string  `json:"metric"`
	Multiplier float64 `json:"multiplier"`
	Offset     float64 `json:"offset"`
}

type MetricThreshold struct {
	ID        string           `json:"id"`
	Snapshot  string           `json:"snapshot"`
	Metric    string           `json:"metric"`
	Operator  string           `json:"operator"`
	Value     *float64         `json:"value,omitempty"`
	Reference *MetricReference `json:"reference,omitempty"`
}

type CorpusCustody struct {
	Authority       string    `json:"authority"`
	Controller      string    `json:"controller"`
	SourceSystem    string    `json:"sourceSystem"`
	ExportID        string    `json:"exportId"`
	SourceHighWater string    `json:"sourceHighWater"`
	ExportedAt      time.Time `json:"exportedAt"`
	DeletedAt       time.Time `json:"deletedAt"`
	ItemMerkleRoot  string    `json:"itemMerkleRoot"`
}

type CorpusManifest struct {
	Schema       string        `json:"schema"`
	GateID       string        `json:"gateId"`
	Status       string        `json:"status"`
	BodyIncluded bool          `json:"bodyIncluded"`
	ItemCount    int           `json:"itemCount"`
	ItemDigests  []string      `json:"itemDigests"`
	Custody      CorpusCustody `json:"custody"`
	KeyID        string        `json:"keyId"`
	HMACSHA256   string        `json:"hmacSha256"`
}

type RunEvidence struct {
	ID               string    `json:"id"`
	DriverID         string    `json:"driverId"`
	DriverArgvDigest string    `json:"driverArgvDigest"`
	StartedAt        time.Time `json:"startedAt"`
	CompletedAt      time.Time `json:"completedAt"`
}

type CorpusCustodyProof struct {
	ManifestDigest string `json:"manifestDigest"`
	Authority      string `json:"authority"`
	ExportID       string `json:"exportId"`
	ItemMerkleRoot string `json:"itemMerkleRoot"`
}

type Observation struct {
	Schema        string               `json:"schema"`
	GateID        string               `json:"gateId"`
	ReleaseCommit string               `json:"releaseCommit"`
	CorpusDigest  string               `json:"corpusDigest"`
	EvidenceMode  string               `json:"evidenceMode"`
	IssuedAt      time.Time            `json:"issuedAt"`
	ExpiresAt     time.Time            `json:"expiresAt"`
	Run           RunEvidence          `json:"run"`
	CorpusCustody CorpusCustodyProof   `json:"corpusCustody"`
	Baseline      EvaluationSnapshot   `json:"baseline"`
	Candidate     EvaluationSnapshot   `json:"candidate"`
	RecordOnly    []EvaluationSnapshot `json:"recordOnly,omitempty"`
	KeyID         string               `json:"keyId"`
	HMACSHA256    string               `json:"hmacSha256"`
}

type EvaluationSnapshot struct {
	Identity       EvaluationIdentity `json:"identity"`
	Metrics        map[string]float64 `json:"metrics"`
	Cost           CostSnapshot       `json:"cost"`
	EvidenceDigest string             `json:"evidenceDigest"`
}

type PriceEvidence struct {
	Catalog     string    `json:"catalog"`
	Version     string    `json:"version"`
	RetrievedAt time.Time `json:"retrievedAt"`
	Digest      string    `json:"digest"`
}

type CostSnapshot struct {
	Currency          string        `json:"currency"`
	EstimatedUSD      float64       `json:"estimatedUsd"`
	InputTokens       int64         `json:"inputTokens"`
	CachedInputTokens int64         `json:"cachedInputTokens"`
	OutputTokens      int64         `json:"outputTokens"`
	AudioSeconds      float64       `json:"audioSeconds"`
	Price             PriceEvidence `json:"price"`
}

type ThresholdResult struct {
	ID       string  `json:"id"`
	Snapshot string  `json:"snapshot"`
	Metric   string  `json:"metric"`
	Operator string  `json:"operator"`
	Actual   float64 `json:"actual"`
	Expected float64 `json:"expected"`
	Passed   bool    `json:"passed"`
}

type Receipt struct {
	Schema                 string               `json:"schema"`
	GateID                 string               `json:"gateId"`
	GeneratedAt            time.Time            `json:"generatedAt"`
	ReleaseCommit          string               `json:"releaseCommit"`
	ManifestDigest         string               `json:"manifestDigest"`
	CorpusDigest           string               `json:"corpusDigest"`
	ObservationPath        string               `json:"observationPath"`
	ObservationDigest      string               `json:"observationDigest"`
	Run                    RunEvidence          `json:"run"`
	EvidenceMode           string               `json:"evidenceMode"`
	Baseline               EvaluationSnapshot   `json:"baseline"`
	Candidate              EvaluationSnapshot   `json:"candidate"`
	RecordOnly             []EvaluationSnapshot `json:"recordOnly,omitempty"`
	Thresholds             []ThresholdResult    `json:"thresholds"`
	Verdict                string               `json:"verdict"`
	W2DAccepted            bool                 `json:"w2dAccepted"`
	CandidateCanaryAllowed bool                 `json:"candidateCanaryAllowed"`
	StopTriggered          bool                 `json:"stopTriggered"`
	FeatureFlag            string               `json:"featureFlag"`
	StopCondition          string               `json:"stopCondition"`
	RollbackCommand        string               `json:"rollbackCommand"`
	KeyID                  string               `json:"keyId"`
	HMACSHA256             string               `json:"hmacSha256"`
}

type CheckReceipt struct {
	Schema         string      `json:"schema"`
	CheckID        string      `json:"checkId"`
	GeneratedAt    time.Time   `json:"generatedAt"`
	ReleaseCommit  string      `json:"releaseCommit"`
	ManifestDigest string      `json:"manifestDigest"`
	Run            RunEvidence `json:"run"`
	OutputDigest   string      `json:"outputDigest"`
	Passed         bool        `json:"passed"`
	KeyID          string      `json:"keyId"`
	HMACSHA256     string      `json:"hmacSha256"`
}

type HMACAuthority struct {
	KeyID string
	Key   []byte
}

func LoadAuthority(path, keyID string) (HMACAuthority, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return HMACAuthority{}, err
	}
	authority := HMACAuthority{KeyID: strings.TrimSpace(keyID), Key: bytes.TrimSpace(raw)}
	if err := authority.Validate(); err != nil {
		return HMACAuthority{}, err
	}
	return authority, nil
}

func (authority HMACAuthority) Validate() error {
	if !keyIDPattern.MatchString(authority.KeyID) || len(authority.Key) < 32 {
		return errors.New("W2 authority needs a valid key id and at least 256 bits of key material")
	}
	return nil
}

func LoadManifest(path string) (Manifest, string, error) {
	raw, err := secureReadFile(path)
	if err != nil {
		return Manifest{}, "", err
	}
	var manifest Manifest
	if err := decodeStrict(raw, &manifest); err != nil {
		return Manifest{}, "", fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, "", err
	}
	return manifest, digestBytes(raw), nil
}

func (manifest Manifest) Validate() error {
	if manifest.Schema != ManifestSchema || len(manifest.Gates) == 0 || manifest.Evidence.Validate() != nil {
		return fmt.Errorf("%w: schema or gates", ErrInvalidManifest)
	}
	checks := map[string]bool{}
	for _, check := range manifest.Checks {
		if err := check.Validate(); err != nil || checks[check.ID] {
			return fmt.Errorf("%w: integrated check %q", ErrInvalidManifest, check.ID)
		}
		checks[check.ID] = true
	}
	for _, id := range requiredCheckIDs {
		if !checks[id] {
			return fmt.Errorf("%w: required integrated check %q is absent", ErrInvalidManifest, id)
		}
	}
	seen := map[string]bool{}
	for _, gate := range manifest.Gates {
		if err := gate.Validate(); err != nil || seen[gate.ID] {
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: duplicate gate %q", ErrInvalidManifest, gate.ID)
		}
		seen[gate.ID] = true
	}
	for _, id := range requiredGateIDs {
		if !seen[id] {
			return fmt.Errorf("%w: required gate %q is absent", ErrInvalidManifest, id)
		}
	}
	return validateLaneContracts(manifest)
}

func (contract EvidenceContract) Validate() error {
	if len(contract.CollectorImplementation) < 2 || contract.CollectorKey.Validate() != nil ||
		contract.PriceCatalog.Validate() != nil || contract.PriceKey.Validate() != nil ||
		contract.CollectorKey.KeyID == contract.PriceKey.KeyID {
		return errors.New("collector and price evidence contract is incomplete")
	}
	seen := map[string]bool{}
	for _, file := range contract.CollectorImplementation {
		if file.Validate() != nil || seen[file.Path] {
			return errors.New("collector implementation custody is invalid")
		}
		seen[file.Path] = true
	}
	return nil
}

func (file CheckedFile) Validate() error {
	if validateRelativePath(file.Path) != nil || !isStrongDigest(file.Digest) {
		return errors.New("checked file path or digest is invalid")
	}
	return nil
}

func (reference Ed25519KeyReference) Validate() error {
	if !keyIDPattern.MatchString(reference.KeyID) || validateRelativePath(reference.Path) != nil || !isStrongDigest(reference.Digest) {
		return errors.New("Ed25519 key reference is invalid")
	}
	return nil
}

// VerifyEvidenceFiles proves that the collector executable sources, public
// keys, and price catalog are the exact files pinned by this manifest. The
// returned digest is the collector build identity included in every signed
// response.
func (manifest Manifest) VerifyEvidenceFiles(root string) (string, error) {
	if err := manifest.Evidence.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	identities := make([]string, 0, len(manifest.Evidence.CollectorImplementation))
	for _, file := range manifest.Evidence.CollectorImplementation {
		if err := verifyCheckedFile(root, file); err != nil {
			return "", err
		}
		identities = append(identities, file.Path+":"+file.Digest)
	}
	if err := verifyCheckedFile(root, CheckedFile{Path: manifest.Evidence.CollectorKey.Path, Digest: manifest.Evidence.CollectorKey.Digest}); err != nil {
		return "", err
	}
	if err := verifyCheckedFile(root, manifest.Evidence.PriceCatalog); err != nil {
		return "", err
	}
	if err := verifyCheckedFile(root, CheckedFile{Path: manifest.Evidence.PriceKey.Path, Digest: manifest.Evidence.PriceKey.Digest}); err != nil {
		return "", err
	}
	return digestStrings(identities), nil
}

func verifyCheckedFile(root string, file CheckedFile) error {
	path, err := rootedPath(root, file.Path)
	if err != nil {
		return fmt.Errorf("%w: checked file path", ErrInvalidManifest)
	}
	raw, err := secureReadFile(path)
	if err != nil || digestBytes(raw) != file.Digest {
		return fmt.Errorf("%w: checked file digest mismatch for %s", ErrInvalidManifest, file.Path)
	}
	return nil
}

func (check IntegratedCheckDefinition) Validate() error {
	if strings.TrimSpace(check.Description) == "" || check.TimeoutSeconds < 1 || check.Driver.ID != check.ID || validateCheckArgv(check.ID, check.Driver.Argv) != nil {
		return ErrInvalidManifest
	}
	if validateRelativePath(check.ReceiptPath) != nil || !strings.Contains(check.ReceiptPath, "artifacts/w2-gates/{releaseCommit}/checks/") || !strings.HasSuffix(check.ReceiptPath, "/"+check.ID+".json") {
		return ErrInvalidManifest
	}
	return nil
}

func (gate GateDefinition) Validate() error {
	if strings.TrimSpace(gate.ID) == "" || strings.TrimSpace(gate.Description) == "" || gate.TimeoutSeconds < 1 || len(gate.Thresholds) == 0 ||
		gate.ReleaseCommit != "${BONFIRE_RELEASE_COMMIT}" || !environmentPattern.MatchString(gate.FeatureFlag) || strings.TrimSpace(gate.StopCondition) == "" ||
		strings.TrimSpace(gate.RollbackCommand) == "" || !strings.Contains(gate.RollbackCommand, gate.FeatureFlag) ||
		(gate.RequiredEvidenceMode != EvidenceModeLive && gate.RequiredEvidenceMode != EvidenceModeFixture) || validateGateArgv(gate) != nil {
		return fmt.Errorf("%w: gate %q has an incomplete execution contract", ErrInvalidManifest, gate.ID)
	}
	if validateRelativePath(gate.Corpus.ManifestPath) != nil || !isStrongDigest(gate.Corpus.Digest) || gate.Corpus.MinimumItems < 1 {
		return fmt.Errorf("%w: gate %q corpus contract", ErrInvalidManifest, gate.ID)
	}
	if gate.Receipt.Schema != ReceiptSchema || !strings.Contains(gate.Receipt.OutputPath, "artifacts/w2-gates/{releaseCommit}/") ||
		!strings.HasSuffix(gate.Receipt.OutputPath, "/"+gate.ID+".json") || validateRelativePath(gate.Receipt.OutputPath) != nil ||
		validateRelativePath(gate.Receipt.SchemaPath) != nil || !isStrongDigest(gate.Receipt.SchemaDigest) {
		return fmt.Errorf("%w: gate %q receipt contract", ErrInvalidManifest, gate.ID)
	}
	if err := gate.Baseline.Validate(); err != nil {
		return fmt.Errorf("%w: gate %q baseline: %v", ErrInvalidManifest, gate.ID, err)
	}
	if err := gate.Candidate.Validate(); err != nil {
		return fmt.Errorf("%w: gate %q candidate: %v", ErrInvalidManifest, gate.ID, err)
	}
	identities := map[EvaluationIdentity]bool{gate.Baseline: true, gate.Candidate: true}
	for _, identity := range gate.RecordOnly {
		if err := identity.Validate(); err != nil || identities[identity] {
			return fmt.Errorf("%w: gate %q record-only identity", ErrInvalidManifest, gate.ID)
		}
		identities[identity] = true
	}
	thresholds := map[string]bool{}
	hasBaselineCorrectness := false
	for _, threshold := range gate.Thresholds {
		if err := threshold.Validate(gate); err != nil || thresholds[threshold.ID] {
			return fmt.Errorf("%w: gate %q threshold %q", ErrInvalidManifest, gate.ID, threshold.ID)
		}
		thresholds[threshold.ID] = true
		if threshold.ID == "baseline-correctness" && threshold.Snapshot == "baseline" && threshold.Metric == "correctness_rate" && threshold.Operator == "gte" && threshold.Value != nil && *threshold.Value >= .9 && *threshold.Value <= 1 {
			hasBaselineCorrectness = true
		}
	}
	if !hasBaselineCorrectness {
		return fmt.Errorf("%w: gate %q has no mandatory baseline correctness threshold", ErrInvalidManifest, gate.ID)
	}
	return nil
}

func (identity EvaluationIdentity) Validate() error {
	if !identifierPattern.MatchString(identity.ID) || !identifierPattern.MatchString(identity.Lane) || strings.TrimSpace(identity.Provider) == "" || strings.TrimSpace(identity.Model) == "" || strings.TrimSpace(identity.Effort) == "" {
		return errors.New("identity fields are required")
	}
	return nil
}

func (threshold MetricThreshold) Validate(gate GateDefinition) error {
	if strings.TrimSpace(threshold.ID) == "" || !validSnapshotName(gate, threshold.Snapshot) || strings.TrimSpace(threshold.Metric) == "" {
		return errors.New("threshold identity is invalid")
	}
	switch threshold.Operator {
	case "gt", "gte", "lt", "lte", "eq":
	default:
		return errors.New("threshold operator is invalid")
	}
	if (threshold.Value == nil) == (threshold.Reference == nil) {
		return errors.New("threshold requires exactly one numeric value or reference")
	}
	if threshold.Value != nil && (!finite(*threshold.Value) || !metricInDomain(threshold.Metric, *threshold.Value)) {
		return errors.New("threshold value is outside its metric domain")
	}
	if threshold.Reference != nil && (!validSnapshotName(gate, threshold.Reference.Snapshot) || strings.TrimSpace(threshold.Reference.Metric) == "" || !finite(threshold.Reference.Multiplier) || !finite(threshold.Reference.Offset)) {
		return errors.New("threshold reference is invalid")
	}
	return nil
}

func (manifest Manifest) Gate(id string) (GateDefinition, bool) {
	for _, gate := range manifest.Gates {
		if gate.ID == strings.TrimSpace(id) {
			return gate, true
		}
	}
	return GateDefinition{}, false
}
func (manifest Manifest) Check(id string) (IntegratedCheckDefinition, bool) {
	for _, check := range manifest.Checks {
		if check.ID == strings.TrimSpace(id) {
			return check, true
		}
	}
	return IntegratedCheckDefinition{}, false
}

func (observation Observation) Validate(gate GateDefinition, corpus CorpusManifest, releaseCommit string, authority HMACAuthority, now time.Time) error {
	if err := observation.verify(authority); err != nil {
		return err
	}
	return observation.validatePayload(gate, corpus, releaseCommit, now)
}

func (observation Observation) validatePayload(gate GateDefinition, corpus CorpusManifest, releaseCommit string, now time.Time) error {
	if observation.Schema != ObservationSchema || observation.GateID != gate.ID || observation.ReleaseCommit != releaseCommit || observation.CorpusDigest != gate.Corpus.Digest || observation.EvidenceMode != gate.RequiredEvidenceMode {
		return fmt.Errorf("%w: identity, release, corpus, or evidence mode mismatch", ErrInvalidInput)
	}
	if observation.IssuedAt.IsZero() || observation.ExpiresAt.IsZero() || observation.ExpiresAt.Before(now) || observation.IssuedAt.After(now.Add(2*time.Minute)) || now.Sub(observation.IssuedAt) > 15*time.Minute || !observation.ExpiresAt.After(observation.IssuedAt) || observation.ExpiresAt.Sub(observation.IssuedAt) > time.Hour {
		return fmt.Errorf("%w: stale or invalid observation validity window", ErrInconclusive)
	}
	if observation.Run.DriverID != gate.Driver.ID || observation.Run.DriverArgvDigest != driverArgvDigest(gate.Driver.Argv, releaseCommit) || !validRun(observation.Run) || observation.IssuedAt.Before(observation.Run.CompletedAt.Add(-2*time.Minute)) || observation.IssuedAt.After(observation.Run.CompletedAt.Add(2*time.Minute)) {
		return fmt.Errorf("%w: run provenance mismatch", ErrInvalidInput)
	}
	wantCustody := CorpusCustodyProof{ManifestDigest: gate.Corpus.Digest, Authority: corpus.Custody.Authority, ExportID: corpus.Custody.ExportID, ItemMerkleRoot: corpus.Custody.ItemMerkleRoot}
	if observation.CorpusCustody != wantCustody {
		return fmt.Errorf("%w: corpus custody mismatch", ErrInvalidInput)
	}
	if err := observation.Baseline.Validate(gate.Baseline); err != nil {
		return fmt.Errorf("%w: baseline: %v", ErrInvalidInput, err)
	}
	if err := observation.Candidate.Validate(gate.Candidate); err != nil {
		return fmt.Errorf("%w: candidate: %v", ErrInvalidInput, err)
	}
	if len(observation.RecordOnly) != len(gate.RecordOnly) {
		return fmt.Errorf("%w: record-only snapshot count", ErrInvalidInput)
	}
	for i, snapshot := range observation.RecordOnly {
		if err := snapshot.Validate(gate.RecordOnly[i]); err != nil {
			return fmt.Errorf("%w: record-only snapshot %d: %v", ErrInvalidInput, i, err)
		}
	}
	return nil
}

func (snapshot EvaluationSnapshot) Validate(want EvaluationIdentity) error {
	if snapshot.Identity != want || len(snapshot.Metrics) == 0 || !isStrongDigest(snapshot.EvidenceDigest) {
		return errors.New("snapshot identity, metrics, or evidence digest is invalid")
	}
	for name, value := range snapshot.Metrics {
		if strings.TrimSpace(name) == "" || !metricInDomain(name, value) {
			return fmt.Errorf("metric %q is outside its domain", name)
		}
	}
	return snapshot.Cost.Validate()
}

func (cost CostSnapshot) Validate() error {
	if cost.Currency != "USD" || !finite(cost.EstimatedUSD) || !finite(cost.AudioSeconds) || cost.EstimatedUSD < 0 || cost.AudioSeconds < 0 || cost.InputTokens < 0 || cost.CachedInputTokens < 0 || cost.OutputTokens < 0 || cost.CachedInputTokens > cost.InputTokens ||
		!identifierPattern.MatchString(cost.Price.Catalog) || strings.TrimSpace(cost.Price.Version) == "" || cost.Price.RetrievedAt.IsZero() || !isStrongDigest(cost.Price.Digest) {
		return errors.New("cost snapshot or price custody is incomplete or invalid")
	}
	return nil
}

func LoadCorpus(root string, gate GateDefinition, authority HMACAuthority) (CorpusManifest, error) {
	path, err := rootedPath(root, gate.Corpus.ManifestPath)
	if err != nil {
		return CorpusManifest{}, err
	}
	raw, err := secureReadFile(path)
	if err != nil {
		return CorpusManifest{}, err
	}
	if digestBytes(raw) != gate.Corpus.Digest {
		return CorpusManifest{}, fmt.Errorf("%w: corpus digest mismatch", ErrInvalidManifest)
	}
	var corpus CorpusManifest
	if err := decodeStrict(raw, &corpus); err != nil {
		return CorpusManifest{}, fmt.Errorf("%w: corpus manifest: %v", ErrInvalidManifest, err)
	}
	if corpus.Schema != CorpusSchema || corpus.GateID != gate.ID || corpus.Status != "frozen" || corpus.BodyIncluded || corpus.ItemCount < gate.Corpus.MinimumItems || len(corpus.ItemDigests) != corpus.ItemCount {
		return CorpusManifest{}, ErrCorpusNotFrozen
	}
	if err := authority.Validate(); err != nil {
		return CorpusManifest{}, err
	}
	if corpus.KeyID != authority.KeyID || corpus.Custody.ExportedAt.IsZero() || corpus.Custody.DeletedAt.IsZero() || corpus.Custody.DeletedAt.Before(corpus.Custody.ExportedAt) || !identifierPattern.MatchString(corpus.Custody.ExportID) || strings.TrimSpace(corpus.Custody.Authority) == "" || strings.TrimSpace(corpus.Custody.Controller) == "" || strings.TrimSpace(corpus.Custody.SourceSystem) == "" || strings.TrimSpace(corpus.Custody.SourceHighWater) == "" {
		return CorpusManifest{}, ErrCorpusNotFrozen
	}
	seen := map[string]bool{}
	for _, digest := range corpus.ItemDigests {
		if !isStrongDigest(digest) || seen[digest] {
			return CorpusManifest{}, fmt.Errorf("%w: corpus item digests", ErrInvalidManifest)
		}
		seen[digest] = true
	}
	if corpus.Custody.ItemMerkleRoot != digestStrings(corpus.ItemDigests) || corpus.verify(authority) != nil {
		return CorpusManifest{}, ErrSignatureInvalid
	}
	return corpus, nil
}

func (receipt Receipt) Validate(manifestDigest string, gate GateDefinition, releaseCommit string, authority HMACAuthority, now time.Time) error {
	if receipt.Schema != ReceiptSchema || receipt.GateID != gate.ID || receipt.GeneratedAt.IsZero() || receipt.GeneratedAt.After(now.Add(2*time.Minute)) || receipt.ReleaseCommit != releaseCommit || receipt.ManifestDigest != manifestDigest || receipt.CorpusDigest != gate.Corpus.Digest || !isStrongDigest(receipt.ObservationDigest) || validateRelativePath(receipt.ObservationPath) != nil || receipt.EvidenceMode != gate.RequiredEvidenceMode || !receipt.W2DAccepted || receipt.FeatureFlag != gate.FeatureFlag || receipt.StopCondition != gate.StopCondition || receipt.RollbackCommand != gate.RollbackCommand || receipt.Run.DriverID != gate.Driver.ID || receipt.Run.DriverArgvDigest != driverArgvDigest(gate.Driver.Argv, releaseCommit) {
		return ErrReceiptInvalid
	}
	if err := receipt.verify(authority); err != nil {
		return ErrReceiptInvalid
	}
	if err := receipt.Baseline.Validate(gate.Baseline); err != nil || len(receipt.Thresholds) != len(gate.Thresholds) {
		return ErrReceiptInvalid
	}
	if err := receipt.Candidate.Validate(gate.Candidate); err != nil || len(receipt.RecordOnly) != len(gate.RecordOnly) {
		return ErrReceiptInvalid
	}
	for i, snapshot := range receipt.RecordOnly {
		if snapshot.Validate(gate.RecordOnly[i]) != nil {
			return ErrReceiptInvalid
		}
	}
	if receipt.Verdict != VerdictPass && receipt.Verdict != VerdictFail {
		return ErrReceiptInvalid
	}
	observation := Observation{Baseline: receipt.Baseline, Candidate: receipt.Candidate, RecordOnly: receipt.RecordOnly}
	want, baselinePassed, candidatePassed, err := evaluateThresholds(gate, observation)
	if err != nil || !baselinePassed || candidatePassed != receipt.CandidateCanaryAllowed || receipt.StopTriggered == receipt.CandidateCanaryAllowed {
		return ErrReceiptInvalid
	}
	if (candidatePassed && receipt.Verdict != VerdictPass) || (!candidatePassed && receipt.Verdict != VerdictFail) {
		return ErrReceiptInvalid
	}
	for i := range want {
		if receipt.Thresholds[i] != want[i] {
			return ErrReceiptInvalid
		}
	}
	return nil
}

func (receipt CheckReceipt) Validate(manifestDigest, releaseCommit string, check IntegratedCheckDefinition, authority HMACAuthority, now time.Time) error {
	if receipt.Schema != CheckReceiptSchema || receipt.CheckID != check.ID || receipt.ReleaseCommit != releaseCommit || receipt.ManifestDigest != manifestDigest || !receipt.Passed || !isStrongDigest(receipt.OutputDigest) || receipt.Run.DriverID != check.Driver.ID || receipt.Run.DriverArgvDigest != driverArgvDigest(check.Driver.Argv, releaseCommit) || receipt.GeneratedAt.IsZero() || receipt.GeneratedAt.After(now.Add(2*time.Minute)) || !validRun(receipt.Run) || receipt.Run.CompletedAt.After(receipt.GeneratedAt.Add(2*time.Minute)) || receipt.verify(authority) != nil {
		return ErrReceiptInvalid
	}
	return nil
}

func (observation *Observation) Sign(authority HMACAuthority) error {
	observation.KeyID = authority.KeyID
	observation.HMACSHA256 = ""
	mac, err := signCanonical(*observation, authority)
	if err == nil {
		observation.HMACSHA256 = mac
	}
	return err
}
func (observation Observation) verify(authority HMACAuthority) error {
	mac := observation.HMACSHA256
	observation.HMACSHA256 = ""
	return verifyCanonical(observation.KeyID, mac, observation, authority)
}
func (receipt *Receipt) sign(authority HMACAuthority) error {
	receipt.KeyID = authority.KeyID
	receipt.HMACSHA256 = ""
	mac, err := signCanonical(*receipt, authority)
	if err == nil {
		receipt.HMACSHA256 = mac
	}
	return err
}
func (receipt Receipt) verify(authority HMACAuthority) error {
	mac := receipt.HMACSHA256
	receipt.HMACSHA256 = ""
	return verifyCanonical(receipt.KeyID, mac, receipt, authority)
}
func (receipt *CheckReceipt) sign(authority HMACAuthority) error {
	receipt.KeyID = authority.KeyID
	receipt.HMACSHA256 = ""
	mac, err := signCanonical(*receipt, authority)
	if err == nil {
		receipt.HMACSHA256 = mac
	}
	return err
}
func (receipt CheckReceipt) verify(authority HMACAuthority) error {
	mac := receipt.HMACSHA256
	receipt.HMACSHA256 = ""
	return verifyCanonical(receipt.KeyID, mac, receipt, authority)
}
func (corpus *CorpusManifest) Sign(authority HMACAuthority) error {
	corpus.KeyID = authority.KeyID
	corpus.HMACSHA256 = ""
	mac, err := signCanonical(*corpus, authority)
	if err == nil {
		corpus.HMACSHA256 = mac
	}
	return err
}
func (corpus CorpusManifest) verify(authority HMACAuthority) error {
	mac := corpus.HMACSHA256
	corpus.HMACSHA256 = ""
	return verifyCanonical(corpus.KeyID, mac, corpus, authority)
}

func signCanonical(value any, authority HMACAuthority) (string, error) {
	if err := authority.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, authority.Key)
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func verifyCanonical(keyID, signature string, value any, authority HMACAuthority) error {
	if err := authority.Validate(); err != nil || keyID != authority.KeyID || !isStrongDigest(signature) {
		return ErrSignatureInvalid
	}
	want, err := signCanonical(value, authority)
	if err != nil || !hmac.Equal([]byte(signature), []byte(want)) {
		return ErrSignatureInvalid
	}
	return nil
}

func validateLaneContracts(manifest Manifest) error {
	require := func(gateID, lane, provider, effort string) error {
		gate, ok := manifest.Gate(gateID)
		if !ok {
			return ErrInvalidManifest
		}
		for _, identity := range []EvaluationIdentity{gate.Baseline, gate.Candidate} {
			if identity.Lane != lane || identity.Provider != provider || identity.Effort != effort {
				return fmt.Errorf("%w: %s identity drift", ErrInvalidManifest, gateID)
			}
		}
		return nil
	}
	if err := require("w2d-brain-commitments", "brain", "openai", "low"); err != nil {
		return err
	}
	if err := require("w2d-brain-fleet-parity", "brain", "openai", "low"); err != nil {
		return err
	}
	if err := require("w2d-proposal-kickoff", "chat", "anthropic", "medium"); err != nil {
		return err
	}
	recall, _ := manifest.Gate("w2d-recall-quality")
	if recall.Baseline.Lane != "chat" || recall.Candidate.Lane != "chat" || recall.Baseline.Provider != "anthropic" || recall.Candidate.Provider != "anthropic" || recall.Baseline.Model != "claude-sonnet-5" || recall.Candidate.Model != "claude-sonnet-5" || recall.Baseline.Effort != "medium" || recall.Candidate.Effort != "medium" {
		return fmt.Errorf("%w: recall chat identity drift", ErrInvalidManifest)
	}
	want := map[string]EvaluationIdentity{
		"brain":    {Lane: "brain", Provider: "openai", Effort: "low"},
		"voice":    {Lane: "voice", Provider: "anthropic", Model: "claude-sonnet-5", Effort: "low"},
		"catch_up": {Lane: "catch_up", Provider: "openai", Effort: "low"},
	}
	for _, identity := range recall.RecordOnly {
		expected, ok := want[identity.Lane]
		if !ok || identity.Provider != expected.Provider || identity.Effort != expected.Effort || (expected.Model != "" && identity.Model != expected.Model) {
			return fmt.Errorf("%w: recall lane identity drift", ErrInvalidManifest)
		}
		delete(want, identity.Lane)
	}
	if len(want) != 0 {
		return fmt.Errorf("%w: recall lane identity missing", ErrInvalidManifest)
	}
	return nil
}

func validSnapshotName(gate GateDefinition, name string) bool {
	if name == "baseline" || name == "candidate" {
		return true
	}
	if !strings.HasPrefix(name, "recordOnly:") {
		return false
	}
	id := strings.TrimPrefix(name, "recordOnly:")
	for _, identity := range gate.RecordOnly {
		if identity.ID == id {
			return true
		}
	}
	return false
}

func metricInDomain(name string, value float64) bool {
	if !finite(value) || value < 0 {
		return false
	}
	if metricDomain(name) == DomainUnitInterval && value > 1 {
		return false
	}
	return true
}

func metricDomain(name string) string {
	lower := strings.ToLower(name)
	unitTokens := []string{"_rate", "precision", "recall", "groundedness", "completeness", "honesty", "normalized_wer", "confidence_half_width", "coverage_gap", "parse_failure_delta"}
	for _, token := range unitTokens {
		if strings.Contains(lower, token) {
			return DomainUnitInterval
		}
	}
	return DomainNonnegative
}

func validateGateArgv(gate GateDefinition) error {
	want := []string{"go", "run", "./cmd/w2-evaluator", "--gate", gate.ID, "--corpus-manifest", gate.Corpus.ManifestPath, "--release-commit", "{releaseCommit}"}
	if gate.Driver.ID != "bonfire-w2-evaluator-v1" || !equalStrings(gate.Driver.Argv, want) {
		return errors.New("untrusted gate driver argv")
	}
	return nil
}

func validateCheckArgv(id string, argv []string) error {
	allowed := map[string][]string{
		"integrated-normal":        {"go", "test", "-count=1", "./..."},
		"consolidated-race":        {"go", "test", "-race", "-count=1", ".", "./internal/w2gate", "./internal/mediasoak"},
		"migration-replay":         {"go", "test", "-count=1", ".", "-run", "Test(CanonicalMigration|ProductionCatchUp|MeetingMemoryBrainAdapter|ExactCatchUp)"},
		"two-room-live-soak":       {"go", "run", "./cmd/media-soak", "--collect", "--manifest", "testdata/w2a/media-soak.json", "--public-key-file", "{env:BONFIRE_MEDIA_SOAK_PUBLIC_KEY_FILE}", "--collector-public-key-file", "{env:BONFIRE_MEDIA_SOAK_COLLECTOR_PUBLIC_KEY_FILE}", "--release-private-key-file", "{env:BONFIRE_MEDIA_SOAK_RELEASE_PRIVATE_KEY_FILE}", "--release-commit", "{releaseCommit}"},
		"production-recall-replay": {"go", "run", "./cmd/w2-recall-replay", "--release-commit", "{releaseCommit}"},
		"workflow-pilots":          {"go", "run", "./cmd/w2-workflow-pilot", "--workflow", "insights-opportunities", "--release-commit", "{releaseCommit}"},
	}
	want, ok := allowed[id]
	if !ok || !equalStrings(argv, want) {
		return errors.New("untrusted integrated-check argv")
	}
	return nil
}

func expandArgv(argv []string, releaseCommit string) ([]string, error) {
	expanded := make([]string, len(argv))
	for i, arg := range argv {
		switch {
		case arg == "{releaseCommit}":
			expanded[i] = releaseCommit
		case strings.HasPrefix(arg, "{env:") && strings.HasSuffix(arg, "}"):
			name := strings.TrimSuffix(strings.TrimPrefix(arg, "{env:"), "}")
			if !environmentPattern.MatchString(name) {
				return nil, ErrInvalidManifest
			}
			value := os.Getenv(name)
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("%w: required environment %s", ErrInconclusive, name)
			}
			expanded[i] = value
		case strings.Contains(arg, "{") || strings.Contains(arg, "}"):
			return nil, ErrInvalidManifest
		default:
			expanded[i] = arg
		}
	}
	return expanded, nil
}

func driverArgvDigest(argv []string, releaseCommit string) string {
	raw, _ := json.Marshal(struct {
		Argv    []string `json:"argv"`
		Release string   `json:"releaseCommit"`
	}{argv, releaseCommit})
	return digestBytes(raw)
}

// DriverArgvDigest binds trusted-driver output to the checked manifest argv
// grammar rather than the caller's expanded process arguments.
func DriverArgvDigest(argv []string, releaseCommit string) string {
	return driverArgvDigest(argv, releaseCommit)
}

// DigestBytes is exposed to checked-in trusted drivers so evidence digests use
// the exact same SHA-256 representation as the gate verifier.
func DigestBytes(raw []byte) string { return digestBytes(raw) }

func validRun(run RunEvidence) bool {
	return identifierPattern.MatchString(run.ID) && !run.StartedAt.IsZero() && !run.CompletedAt.IsZero() && !run.CompletedAt.Before(run.StartedAt) && isStrongDigest(run.DriverArgvDigest)
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
func digestBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func digestStrings(values []string) string {
	values = append([]string(nil), values...)
	sort.Strings(values)
	raw, _ := json.Marshal(values)
	return digestBytes(raw)
}
func isStrongDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return false
	}
	allSame := true
	for i := 1; i < len(raw); i++ {
		if raw[i] != raw[0] {
			allSame = false
			break
		}
	}
	return !allSame
}
func ValidateReleaseCommit(value string) error {
	if !releaseCommitPattern.MatchString(value) {
		return errors.New("release commit must be a full lowercase Git SHA")
	}
	return nil
}
func validateRelativePath(path string) error {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return errors.New("path must be clean and repository-relative")
	}
	return nil
}
func rootedPath(root, path string) (string, error) {
	if err := validateRelativePath(path); err != nil {
		return "", err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// macOS commonly exposes /var as a system symlink to /private/var. Resolve
	// the trusted repository root once, then reject symlinks only in the
	// custody path beneath it. This preserves the no-symlink boundary without
	// rejecting a legitimate checkout solely because of an OS-level ancestor.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if err := rejectSymlinkComponents(root, false); err != nil {
		return "", err
	}
	resolved := filepath.Join(root, path)
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes repository root")
	}
	if err := rejectSymlinkComponents(resolved, true); err != nil {
		return "", err
	}
	return resolved, nil
}

func rejectSymlinkComponents(path string, allowMissing bool) error {
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
		if errors.Is(statErr, os.ErrNotExist) && allowMissing {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path component is forbidden: %s", current)
		}
	}
	return nil
}

func secureReadFile(path string) ([]byte, error) {
	if err := rejectSymlinkComponents(path, false); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("secure evidence path is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("secure evidence path changed while opening")
	}
	return io.ReadAll(file)
}
func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func DecodeObservation(raw []byte) (Observation, error) {
	var observation Observation
	if err := decodeStrict(raw, &observation); err != nil {
		return Observation{}, err
	}
	return observation, nil
}
func EncodeReceipt(receipt Receipt) ([]byte, error) { return json.MarshalIndent(receipt, "", "  ") }
func IsBlockingEvidenceError(err error) bool {
	return errors.Is(err, ErrCorpusNotFrozen) || errors.Is(err, ErrInconclusive) || errors.Is(err, ErrBaselineFailed) || errors.Is(err, ErrReceiptInvalid) || errors.Is(err, ErrSignatureInvalid)
}
