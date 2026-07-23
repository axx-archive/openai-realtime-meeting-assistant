package w2driver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/w2gate"
)

const (
	CollectorRequestSchema  = "bonfire.w2.live-collector.request.v2"
	CollectorResponseSchema = "bonfire.w2.live-collector.response.v2"
	SystemProbeSchema       = "bonfire.w2.system-probe.response.v2"
	SystemProbeOutputSchema = "bonfire.w2.system-probe.output.v2"
	PublicKeySchema         = "bonfire.w2.ed25519-public-key.v1"
	PriceCatalogSchema      = "bonfire.w2.price-catalog.v1"
	SignatureAlgorithm      = "ed25519"
	maxResponseBytes        = 32 << 20

	PrimitiveRatio         = "ratio"
	PrimitiveCount         = "count"
	PrimitiveScalar        = "scalar"
	PrimitiveWilson        = "binomial_95ci_half_width"
	ArtifactRequest        = "provider_request"
	ArtifactResponse       = "provider_response"
	ArtifactReference      = "reference_label"
	ArtifactJudge          = "judge_result"
	ArtifactProbeExpected  = "probe_expected"
	ArtifactProbeActual    = "probe_actual"
	ArtifactProbeRequest   = "probe_provider_request"
	ArtifactProbeResponse  = "probe_provider_response"
	ArtifactProbeReference = "probe_reference"
	ArtifactIntegrated     = "integrated_judge"
)

type Client struct {
	BaseURL                       string
	Token                         string
	CollectorKeyID                string
	CollectorPublicKey            ed25519.PublicKey
	CollectorImplementationDigest string
	PriceCatalog                  PriceCatalog
	PriceCatalogDigest            string
	HTTP                          *http.Client
	Now                           func() time.Time
	Random                        io.Reader
}

type CollectorRequest struct {
	Schema                        string                      `json:"schema"`
	GateID                        string                      `json:"gateId"`
	ReleaseCommit                 string                      `json:"releaseCommit"`
	ManifestDigest                string                      `json:"manifestDigest"`
	CorpusDigest                  string                      `json:"corpusDigest"`
	CorpusExportID                string                      `json:"corpusExportId"`
	ChallengeNonce                string                      `json:"challengeNonce"`
	CollectorImplementationDigest string                      `json:"collectorImplementationDigest"`
	Baseline                      w2gate.EvaluationIdentity   `json:"baseline"`
	Candidate                     w2gate.EvaluationIdentity   `json:"candidate"`
	RecordOnly                    []w2gate.EvaluationIdentity `json:"recordOnly,omitempty"`
}

type CollectorResponse struct {
	Schema                        string        `json:"schema"`
	GateID                        string        `json:"gateId"`
	ReleaseCommit                 string        `json:"releaseCommit"`
	CorpusDigest                  string        `json:"corpusDigest"`
	ChallengeNonce                string        `json:"challengeNonce"`
	CollectorImplementationDigest string        `json:"collectorImplementationDigest"`
	RawArtifactManifestDigest     string        `json:"rawArtifactManifestDigest"`
	ObservedAt                    time.Time     `json:"observedAt"`
	ExpiresAt                     time.Time     `json:"expiresAt"`
	Snapshots                     []RawSnapshot `json:"snapshots"`
	Artifacts                     []RawArtifact `json:"artifacts"`
	Signature                     Signature     `json:"signature"`
}

type RawArtifact struct {
	Kind             string          `json:"kind"`
	SnapshotID       string          `json:"snapshotId,omitempty"`
	CorpusItemDigest string          `json:"corpusItemDigest,omitempty"`
	Name             string          `json:"name"`
	SHA256           string          `json:"sha256"`
	Payload          json.RawMessage `json:"payload"`
}

type ProviderRequestEvidence struct {
	Schema           string                    `json:"schema"`
	RequestID        string                    `json:"requestId"`
	Identity         w2gate.EvaluationIdentity `json:"identity"`
	CorpusItemDigest string                    `json:"corpusItemDigest"`
	ObservedAt       time.Time                 `json:"observedAt"`
}

type ProviderResponseEvidence struct {
	Schema                string        `json:"schema"`
	RequestID             string        `json:"requestId"`
	RequestArtifactDigest string        `json:"requestArtifactDigest"`
	ObservedAt            time.Time     `json:"observedAt"`
	Usage                 UsageEvidence `json:"usage"`
}

type ReferenceEvidence struct {
	Schema           string `json:"schema"`
	CorpusItemDigest string `json:"corpusItemDigest"`
	LabelDigest      string `json:"labelDigest"`
}

type JudgeEvidence struct {
	Schema                  string                    `json:"schema"`
	Identity                w2gate.EvaluationIdentity `json:"identity"`
	CorpusItemDigest        string                    `json:"corpusItemDigest"`
	ReferenceArtifactDigest string                    `json:"referenceArtifactDigest"`
	ProviderResponseDigests []string                  `json:"providerResponseDigests"`
	Primitives              []MetricPrimitive         `json:"primitives"`
}

type IntegratedAssertionEvidence struct {
	Schema                  string `json:"schema"`
	Name                    string `json:"name"`
	ExpectedArtifactDigest  string `json:"expectedArtifactDigest"`
	ActualArtifactDigest    string `json:"actualArtifactDigest"`
	ProviderRequestDigest   string `json:"providerRequestDigest"`
	ProviderResponseDigest  string `json:"providerResponseDigest"`
	ReferenceArtifactDigest string `json:"referenceArtifactDigest"`
}

type ProbeValueEvidence struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}

type ProbeRequestEvidence struct {
	Schema     string    `json:"schema"`
	Name       string    `json:"name"`
	RequestID  string    `json:"requestId"`
	ObservedAt time.Time `json:"observedAt"`
}
type ProbeResponseEvidence struct {
	Schema                string        `json:"schema"`
	Name                  string        `json:"name"`
	RequestID             string        `json:"requestId"`
	RequestArtifactDigest string        `json:"requestArtifactDigest"`
	ObservedAt            time.Time     `json:"observedAt"`
	Usage                 UsageEvidence `json:"usage"`
}

type RawSnapshot struct {
	Identity w2gate.EvaluationIdentity `json:"identity"`
	Samples  []RawSample               `json:"samples"`
}

// RawSample contains primitive, item-bound evidence. It intentionally has no
// arbitrary metric map or pass/fail field: every gate metric is recomputed by
// this checked client from typed counts, ratios, or scalar observations.
type RawSample struct {
	CorpusItemDigest   string             `json:"corpusItemDigest"`
	ProviderCalls      []ProviderArtifact `json:"providerCalls"`
	ReferenceLabelHash string             `json:"referenceLabelDigest"`
	JudgeArtifactHash  string             `json:"judgeArtifactDigest"`
	Primitives         []MetricPrimitive  `json:"primitives"`
	Usage              UsageEvidence      `json:"usage"`
}

type ProviderArtifact struct {
	RequestID      string    `json:"requestId"`
	RequestDigest  string    `json:"requestDigest"`
	ResponseDigest string    `json:"responseDigest"`
	ObservedAt     time.Time `json:"observedAt"`
}

type MetricPrimitive struct {
	Metric      string  `json:"metric"`
	Kind        string  `json:"kind"`
	Numerator   int64   `json:"numerator,omitempty"`
	Denominator int64   `json:"denominator,omitempty"`
	Value       float64 `json:"value,omitempty"`
}

type UsageEvidence struct {
	InputTokens       int64   `json:"inputTokens"`
	CachedInputTokens int64   `json:"cachedInputTokens"`
	OutputTokens      int64   `json:"outputTokens"`
	AudioSeconds      float64 `json:"audioSeconds"`
}

type ProbeRequest struct {
	Schema                        string `json:"schema"`
	ProbeID                       string `json:"probeId"`
	ReleaseCommit                 string `json:"releaseCommit"`
	ChallengeNonce                string `json:"challengeNonce"`
	CollectorImplementationDigest string `json:"collectorImplementationDigest"`
}

type ProbeAssertion struct {
	Name             string           `json:"name"`
	ProviderArtifact ProviderArtifact `json:"providerArtifact"`
	ReferenceDigest  string           `json:"referenceDigest"`
	JudgeDigest      string           `json:"judgeDigest"`
	ExpectedDigest   string           `json:"expectedDigest"`
	ActualDigest     string           `json:"actualDigest"`
	Usage            UsageEvidence    `json:"usage"`
}

type SystemProbeResponse struct {
	Schema                        string           `json:"schema"`
	ProbeID                       string           `json:"probeId"`
	ReleaseCommit                 string           `json:"releaseCommit"`
	ChallengeNonce                string           `json:"challengeNonce"`
	CollectorImplementationDigest string           `json:"collectorImplementationDigest"`
	RawArtifactManifestDigest     string           `json:"rawArtifactManifestDigest"`
	ObservedAt                    time.Time        `json:"observedAt"`
	ExpiresAt                     time.Time        `json:"expiresAt"`
	Assertions                    []ProbeAssertion `json:"assertions"`
	Artifacts                     []RawArtifact    `json:"artifacts"`
	Signature                     Signature        `json:"signature"`
}

type SystemProbeOutput struct {
	Schema         string    `json:"schema"`
	ProbeID        string    `json:"probeId"`
	ReleaseCommit  string    `json:"releaseCommit"`
	ObservedAt     time.Time `json:"observedAt"`
	EvidenceDigest string    `json:"evidenceDigest"`
	CollectorKeyID string    `json:"collectorKeyId"`
	AssertionCount int       `json:"assertionCount"`
}

type Signature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"keyId"`
	Value     string `json:"value"`
}

type PublicKeyFile struct {
	Schema    string `json:"schema"`
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"`
}

type PrivateKeyFile struct {
	Schema     string `json:"schema"`
	KeyID      string `json:"keyId"`
	PrivateKey string `json:"privateKey"`
}

type PriceCatalog struct {
	Schema      string      `json:"schema"`
	Catalog     string      `json:"catalog"`
	Version     string      `json:"version"`
	Currency    string      `json:"currency"`
	EffectiveAt time.Time   `json:"effectiveAt"`
	Rates       []PriceRate `json:"rates"`
	Signature   Signature   `json:"signature"`
}

type PriceRate struct {
	Provider                 string  `json:"provider"`
	Model                    string  `json:"model"`
	InputUSDPerMillion       float64 `json:"inputUsdPerMillion"`
	CachedInputUSDPerMillion float64 `json:"cachedInputUsdPerMillion"`
	OutputUSDPerMillion      float64 `json:"outputUsdPerMillion"`
	AudioUSDPerMinute        float64 `json:"audioUsdPerMinute"`
}

// LoadCheckedClient builds a verifier exclusively from files pinned by the W2
// manifest. Collector and price authority are separate Ed25519 keys. This
// process never receives the HMAC used by the parent receipt writer.
func LoadCheckedClient(root string, manifest w2gate.Manifest, baseURL, token string) (Client, error) {
	implementationDigest, err := manifest.VerifyEvidenceFiles(root)
	if err != nil {
		return Client{}, err
	}
	collectorKey, err := loadPublicKey(root, manifest.Evidence.CollectorKey)
	if err != nil {
		return Client{}, err
	}
	priceKey, err := loadPublicKey(root, manifest.Evidence.PriceKey)
	if err != nil {
		return Client{}, err
	}
	catalog, err := loadPriceCatalog(root, manifest.Evidence.PriceCatalog, manifest.Evidence.PriceKey.KeyID, priceKey)
	if err != nil {
		return Client{}, err
	}
	return Client{
		BaseURL: baseURL, Token: token,
		CollectorKeyID: manifest.Evidence.CollectorKey.KeyID, CollectorPublicKey: collectorKey,
		CollectorImplementationDigest: implementationDigest,
		PriceCatalog:                  catalog, PriceCatalogDigest: manifest.Evidence.PriceCatalog.Digest,
	}, nil
}

func LoadCollectorPublicKey(root string, manifest w2gate.Manifest) (ed25519.PublicKey, error) {
	if _, err := manifest.VerifyEvidenceFiles(root); err != nil {
		return nil, err
	}
	return loadPublicKey(root, manifest.Evidence.CollectorKey)
}

func LoadPrivateKey(path, keyID string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file PrivateKeyFile
	if strictDecode(raw, &file) != nil || file.Schema != "bonfire.w2.ed25519-private-key.v1" || file.KeyID != keyID {
		return nil, errors.New("collector private key file is invalid")
	}
	decoded, err := base64.StdEncoding.DecodeString(file.PrivateKey)
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("collector private key bytes are invalid")
	}
	return ed25519.PrivateKey(decoded), nil
}

func (client Client) CollectGate(ctx context.Context, manifestDigest string, gate w2gate.GateDefinition, corpus w2gate.CorpusManifest, releaseCommit string, startedAt time.Time) (w2gate.Observation, error) {
	if err := client.validate(); err != nil {
		return w2gate.Observation{}, err
	}
	nonce, err := client.challenge()
	if err != nil {
		return w2gate.Observation{}, err
	}
	request := CollectorRequest{
		Schema: CollectorRequestSchema, GateID: gate.ID, ReleaseCommit: releaseCommit, ManifestDigest: manifestDigest,
		CorpusDigest: gate.Corpus.Digest, CorpusExportID: corpus.Custody.ExportID, ChallengeNonce: nonce,
		CollectorImplementationDigest: client.CollectorImplementationDigest,
		Baseline:                      gate.Baseline, Candidate: gate.Candidate, RecordOnly: gate.RecordOnly,
	}
	var response CollectorResponse
	if err := client.post(ctx, "/v1/w2/gates/"+url.PathEscape(gate.ID), request, &response); err != nil {
		return w2gate.Observation{}, err
	}
	now := client.now()
	if response.Schema != CollectorResponseSchema || response.GateID != gate.ID || response.ReleaseCommit != releaseCommit ||
		response.CorpusDigest != gate.Corpus.Digest || response.ChallengeNonce != nonce ||
		response.CollectorImplementationDigest != client.CollectorImplementationDigest || !fresh(response.ObservedAt, response.ExpiresAt, now) ||
		response.Verify(client.CollectorKeyID, client.CollectorPublicKey) != nil {
		return w2gate.Observation{}, errors.New("live collector response is stale, mismatched, or unauthenticated")
	}
	replayed, err := ReplayGateArtifacts(response.Artifacts)
	if err != nil || !reflect.DeepEqual(replayed, response.Snapshots) {
		return w2gate.Observation{}, errors.New("collector raw artifacts do not replay to its snapshots")
	}
	for _, snapshot := range replayed {
		for _, sample := range snapshot.Samples {
			for _, call := range sample.ProviderCalls {
				if call.ObservedAt.Before(response.ObservedAt.Add(-15*time.Minute)) || call.ObservedAt.After(response.ObservedAt.Add(2*time.Minute)) {
					return w2gate.Observation{}, errors.New("collector wrapped stale provider evidence in a fresh response")
				}
			}
		}
	}
	artifactDigest, err := RawArtifactManifestDigest(response.Artifacts)
	if err != nil || artifactDigest != response.RawArtifactManifestDigest {
		return w2gate.Observation{}, errors.New("collector raw-artifact manifest is incomplete or mismatched")
	}
	wanted := append([]w2gate.EvaluationIdentity{gate.Baseline, gate.Candidate}, gate.RecordOnly...)
	byIdentity := make(map[w2gate.EvaluationIdentity]RawSnapshot, len(response.Snapshots))
	for _, snapshot := range response.Snapshots {
		if _, duplicate := byIdentity[snapshot.Identity]; duplicate {
			return w2gate.Observation{}, errors.New("collector duplicated an evaluation identity")
		}
		byIdentity[snapshot.Identity] = snapshot
	}
	if len(byIdentity) != len(wanted) {
		return w2gate.Observation{}, errors.New("collector returned an incomplete identity set")
	}
	computed := make([]w2gate.EvaluationSnapshot, 0, len(wanted))
	for index, identity := range wanted {
		raw, ok := byIdentity[identity]
		if !ok {
			return w2gate.Observation{}, fmt.Errorf("collector identity drift for %s", identity.ID)
		}
		snapshot, err := client.computeSnapshot(gate, snapshotName(index, identity), corpus, raw)
		if err != nil {
			return w2gate.Observation{}, err
		}
		computed = append(computed, snapshot)
	}
	rawResponse, _ := json.Marshal(response)
	completedAt := client.now()
	observation := w2gate.Observation{
		Schema: w2gate.ObservationSchema, GateID: gate.ID, ReleaseCommit: releaseCommit, CorpusDigest: gate.Corpus.Digest, EvidenceMode: gate.RequiredEvidenceMode,
		IssuedAt: completedAt, ExpiresAt: completedAt.Add(30 * time.Minute),
		Run:           w2gate.RunEvidence{ID: "collector-" + w2gate.DigestBytes(rawResponse)[:24], DriverID: gate.Driver.ID, DriverArgvDigest: w2gate.DriverArgvDigest(gate.Driver.Argv, releaseCommit), StartedAt: startedAt.UTC(), CompletedAt: completedAt},
		CorpusCustody: w2gate.CorpusCustodyProof{ManifestDigest: gate.Corpus.Digest, Authority: corpus.Custody.Authority, ExportID: corpus.Custody.ExportID, ItemMerkleRoot: corpus.Custody.ItemMerkleRoot},
		Baseline:      computed[0], Candidate: computed[1], RecordOnly: computed[2:],
	}
	return observation, nil
}

func (client Client) RunSystemProbe(ctx context.Context, probeID, releaseCommit string) (SystemProbeOutput, error) {
	if err := client.validate(); err != nil {
		return SystemProbeOutput{}, err
	}
	nonce, err := client.challenge()
	if err != nil {
		return SystemProbeOutput{}, err
	}
	request := ProbeRequest{Schema: "bonfire.w2.system-probe.request.v2", ProbeID: probeID, ReleaseCommit: releaseCommit, ChallengeNonce: nonce, CollectorImplementationDigest: client.CollectorImplementationDigest}
	var response SystemProbeResponse
	if err := client.post(ctx, "/v1/w2/probes/"+url.PathEscape(probeID), request, &response); err != nil {
		return SystemProbeOutput{}, err
	}
	now := client.now()
	if response.Schema != SystemProbeSchema || response.ProbeID != probeID || response.ReleaseCommit != releaseCommit || response.ChallengeNonce != nonce ||
		response.CollectorImplementationDigest != client.CollectorImplementationDigest || !fresh(response.ObservedAt, response.ExpiresAt, now) ||
		response.Verify(client.CollectorKeyID, client.CollectorPublicKey) != nil || len(response.Assertions) == 0 {
		return SystemProbeOutput{}, errors.New("system probe is stale, mismatched, or unauthenticated")
	}
	replayedAssertions, err := ReplayProbeArtifacts(response.Artifacts)
	if err != nil || !reflect.DeepEqual(replayedAssertions, response.Assertions) {
		return SystemProbeOutput{}, errors.New("system probe artifacts do not replay to its assertions")
	}
	for _, assertion := range response.Assertions {
		if err := assertion.Validate(); err != nil || assertion.ActualDigest != assertion.ExpectedDigest || assertion.ProviderArtifact.ObservedAt.Before(response.ObservedAt.Add(-15*time.Minute)) || assertion.ProviderArtifact.ObservedAt.After(response.ObservedAt.Add(2*time.Minute)) {
			return SystemProbeOutput{}, errors.New("system probe assertion failed or lacks primitive evidence")
		}
	}
	wantDigest, err := ProbeArtifactManifestDigest(response.Artifacts)
	if err != nil || response.RawArtifactManifestDigest != wantDigest {
		return SystemProbeOutput{}, errors.New("system probe raw-artifact manifest is incomplete or mismatched")
	}
	evidenceRaw, _ := json.Marshal(struct {
		Assertions []ProbeAssertion `json:"assertions"`
		Price      string           `json:"priceCatalogDigest"`
	}{response.Assertions, client.PriceCatalogDigest})
	return SystemProbeOutput{Schema: SystemProbeOutputSchema, ProbeID: probeID, ReleaseCommit: releaseCommit, ObservedAt: response.ObservedAt, EvidenceDigest: w2gate.DigestBytes(evidenceRaw), CollectorKeyID: response.Signature.KeyID, AssertionCount: len(response.Assertions)}, nil
}

func (client Client) computeSnapshot(gate w2gate.GateDefinition, name string, corpus w2gate.CorpusManifest, raw RawSnapshot) (w2gate.EvaluationSnapshot, error) {
	if len(raw.Samples) != len(corpus.ItemDigests) || len(raw.Samples) == 0 {
		return w2gate.EvaluationSnapshot{}, errors.New("collector snapshot does not cover the exact frozen corpus")
	}
	wantItems := make(map[string]bool, len(corpus.ItemDigests))
	for _, digest := range corpus.ItemDigests {
		wantItems[digest] = true
	}
	required := requiredMetrics(gate, name)
	primitiveSets := make(map[string][]MetricPrimitive, len(required))
	seenItems := map[string]bool{}
	usage := UsageEvidence{}
	for _, sample := range raw.Samples {
		if !wantItems[sample.CorpusItemDigest] || seenItems[sample.CorpusItemDigest] {
			return w2gate.EvaluationSnapshot{}, errors.New("collector sample is not uniquely bound to a frozen corpus item")
		}
		seenItems[sample.CorpusItemDigest] = true
		if err := sample.Validate(); err != nil {
			return w2gate.EvaluationSnapshot{}, err
		}
		seenMetrics := map[string]bool{}
		for _, primitive := range sample.Primitives {
			if !required[primitive.Metric] || seenMetrics[primitive.Metric] {
				return w2gate.EvaluationSnapshot{}, fmt.Errorf("unexpected or duplicated primitive %s", primitive.Metric)
			}
			seenMetrics[primitive.Metric] = true
			primitiveSets[primitive.Metric] = append(primitiveSets[primitive.Metric], primitive)
		}
		if len(seenMetrics) != len(required) {
			return w2gate.EvaluationSnapshot{}, errors.New("sample is missing a required metric primitive")
		}
		usage.InputTokens += sample.Usage.InputTokens
		usage.CachedInputTokens += sample.Usage.CachedInputTokens
		usage.OutputTokens += sample.Usage.OutputTokens
		usage.AudioSeconds += sample.Usage.AudioSeconds
	}
	metrics := make(map[string]float64, len(required))
	for metric := range required {
		value, err := aggregatePrimitives(metric, primitiveSets[metric])
		if err != nil {
			return w2gate.EvaluationSnapshot{}, err
		}
		metrics[metric] = value
	}
	cost, err := client.deriveCost(raw.Identity, usage)
	if err != nil {
		return w2gate.EvaluationSnapshot{}, err
	}
	evidenceRaw, _ := json.Marshal(struct {
		Identity w2gate.EvaluationIdentity `json:"identity"`
		Samples  []RawSample               `json:"samples"`
		Cost     w2gate.CostSnapshot       `json:"cost"`
	}{raw.Identity, raw.Samples, cost})
	return w2gate.EvaluationSnapshot{Identity: raw.Identity, Metrics: metrics, Cost: cost, EvidenceDigest: w2gate.DigestBytes(evidenceRaw)}, nil
}

func (sample RawSample) Validate() error {
	if !strongDigest(sample.CorpusItemDigest) || !strongDigest(sample.ReferenceLabelHash) || !strongDigest(sample.JudgeArtifactHash) || len(sample.ProviderCalls) == 0 || len(sample.Primitives) == 0 || sample.Usage.Validate() != nil {
		return errors.New("raw sample evidence is incomplete")
	}
	seenCalls := map[string]bool{}
	for _, call := range sample.ProviderCalls {
		if err := call.Validate(); err != nil || seenCalls[call.RequestID] {
			return errors.New("provider request/response custody is invalid")
		}
		seenCalls[call.RequestID] = true
	}
	for _, primitive := range sample.Primitives {
		if err := primitive.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (artifact ProviderArtifact) Validate() error {
	if strings.TrimSpace(artifact.RequestID) == "" || !strongDigest(artifact.RequestDigest) || !strongDigest(artifact.ResponseDigest) || artifact.ObservedAt.IsZero() {
		return errors.New("provider artifact is incomplete")
	}
	return nil
}

func (primitive MetricPrimitive) Validate() error {
	if strings.TrimSpace(primitive.Metric) == "" || math.IsNaN(primitive.Value) || math.IsInf(primitive.Value, 0) {
		return errors.New("metric primitive is invalid")
	}
	switch primitive.Kind {
	case PrimitiveRatio, PrimitiveWilson:
		if primitive.Denominator <= 0 || primitive.Numerator < 0 || primitive.Numerator > primitive.Denominator || primitive.Value != 0 {
			return errors.New("ratio primitive counts are invalid")
		}
	case PrimitiveCount:
		if primitive.Numerator != 0 || primitive.Denominator != 0 || primitive.Value < 0 || math.Trunc(primitive.Value) != primitive.Value {
			return errors.New("count primitive is invalid")
		}
	case PrimitiveScalar:
		if primitive.Numerator != 0 || primitive.Denominator != 0 || primitive.Value < 0 {
			return errors.New("scalar primitive is invalid")
		}
	default:
		return errors.New("unknown metric primitive kind")
	}
	return nil
}

func (usage UsageEvidence) Validate() error {
	if usage.InputTokens < 0 || usage.CachedInputTokens < 0 || usage.OutputTokens < 0 || usage.CachedInputTokens > usage.InputTokens || usage.AudioSeconds < 0 || math.IsNaN(usage.AudioSeconds) || math.IsInf(usage.AudioSeconds, 0) {
		return errors.New("provider usage evidence is invalid")
	}
	return nil
}

func (assertion ProbeAssertion) Validate() error {
	if strings.TrimSpace(assertion.Name) == "" || assertion.ProviderArtifact.Validate() != nil || !strongDigest(assertion.ReferenceDigest) || !strongDigest(assertion.JudgeDigest) || !strongDigest(assertion.ExpectedDigest) || !strongDigest(assertion.ActualDigest) || assertion.Usage.Validate() != nil {
		return errors.New("probe assertion evidence is incomplete")
	}
	return nil
}

func (artifact RawArtifact) Validate() error {
	if strings.TrimSpace(artifact.Name) == "" || !strongDigest(artifact.SHA256) || len(artifact.Payload) == 0 || w2gate.DigestBytes(artifact.Payload) != artifact.SHA256 {
		return errors.New("retained raw artifact custody is invalid")
	}
	switch artifact.Kind {
	case ArtifactRequest, ArtifactResponse, ArtifactReference, ArtifactJudge:
		if artifact.SnapshotID == "" || !strongDigest(artifact.CorpusItemDigest) {
			return errors.New("gate artifact identity is invalid")
		}
	case ArtifactIntegrated, ArtifactProbeExpected, ArtifactProbeActual, ArtifactProbeRequest, ArtifactProbeResponse, ArtifactProbeReference:
		if artifact.SnapshotID != "" || artifact.CorpusItemDigest != "" {
			return errors.New("integrated artifact identity is invalid")
		}
	default:
		return errors.New("unknown retained artifact kind")
	}
	return nil
}

func ReplayGateArtifacts(artifacts []RawArtifact) ([]RawSnapshot, error) {
	type sampleKey struct{ snapshot, item string }
	type sampleState struct {
		identity w2gate.EvaluationIdentity
		requests map[string]struct {
			digest string
			value  ProviderRequestEvidence
		}
		responses map[string]struct {
			digest string
			value  ProviderResponseEvidence
		}
		referenceDigest string
		judgeDigest     string
		judge           JudgeEvidence
	}
	states := map[sampleKey]*sampleState{}
	seenNames := map[string]bool{}
	for _, artifact := range artifacts {
		if artifact.Validate() != nil || artifact.Kind == ArtifactIntegrated || seenNames[artifact.Name] {
			return nil, errors.New("invalid or duplicate gate artifact")
		}
		seenNames[artifact.Name] = true
		key := sampleKey{artifact.SnapshotID, artifact.CorpusItemDigest}
		state := states[key]
		if state == nil {
			state = &sampleState{requests: map[string]struct {
				digest string
				value  ProviderRequestEvidence
			}{}, responses: map[string]struct {
				digest string
				value  ProviderResponseEvidence
			}{}}
			states[key] = state
		}
		switch artifact.Kind {
		case ArtifactRequest:
			var value ProviderRequestEvidence
			if strictDecode(artifact.Payload, &value) != nil || value.Schema != "bonfire.w2.provider-request.v1" || value.Identity.ID != artifact.SnapshotID || value.CorpusItemDigest != artifact.CorpusItemDigest || value.RequestID == "" || value.ObservedAt.IsZero() {
				return nil, errors.New("provider request artifact is invalid")
			}
			if state.identity != (w2gate.EvaluationIdentity{}) && state.identity != value.Identity {
				return nil, errors.New("artifact identity drift")
			}
			state.identity = value.Identity
			if _, ok := state.requests[value.RequestID]; ok {
				return nil, errors.New("duplicate provider request")
			}
			state.requests[value.RequestID] = struct {
				digest string
				value  ProviderRequestEvidence
			}{artifact.SHA256, value}
		case ArtifactResponse:
			var value ProviderResponseEvidence
			if strictDecode(artifact.Payload, &value) != nil || value.Schema != "bonfire.w2.provider-response.v1" || value.RequestID == "" || value.ObservedAt.IsZero() || value.Usage.Validate() != nil {
				return nil, errors.New("provider response artifact is invalid")
			}
			if _, ok := state.responses[value.RequestID]; ok {
				return nil, errors.New("duplicate provider response")
			}
			state.responses[value.RequestID] = struct {
				digest string
				value  ProviderResponseEvidence
			}{artifact.SHA256, value}
		case ArtifactReference:
			var value ReferenceEvidence
			if strictDecode(artifact.Payload, &value) != nil || value.Schema != "bonfire.w2.reference.v1" || value.CorpusItemDigest != artifact.CorpusItemDigest || !strongDigest(value.LabelDigest) || state.referenceDigest != "" {
				return nil, errors.New("reference artifact is invalid")
			}
			state.referenceDigest = artifact.SHA256
		case ArtifactJudge:
			if strictDecode(artifact.Payload, &state.judge) != nil || state.judge.Schema != "bonfire.w2.judge.v1" || state.judge.Identity.ID != artifact.SnapshotID || state.judge.CorpusItemDigest != artifact.CorpusItemDigest || state.judgeDigest != "" {
				return nil, errors.New("judge artifact is invalid")
			}
			state.judgeDigest = artifact.SHA256
		}
	}
	bySnapshot := map[w2gate.EvaluationIdentity][]RawSample{}
	for key, state := range states {
		if state.identity.ID != key.snapshot || state.referenceDigest == "" || state.judgeDigest == "" || state.judge.Identity != state.identity || state.judge.ReferenceArtifactDigest != state.referenceDigest || len(state.requests) == 0 || len(state.requests) != len(state.responses) {
			return nil, errors.New("artifact sample set is incomplete")
		}
		calls := make([]ProviderArtifact, 0, len(state.requests))
		usage := UsageEvidence{}
		responseDigests := []string{}
		for id, request := range state.requests {
			response, ok := state.responses[id]
			if !ok || response.value.RequestArtifactDigest != request.digest {
				return nil, errors.New("provider request/response lineage mismatch")
			}
			calls = append(calls, ProviderArtifact{RequestID: id, RequestDigest: request.digest, ResponseDigest: response.digest, ObservedAt: response.value.ObservedAt})
			responseDigests = append(responseDigests, response.digest)
			usage.InputTokens += response.value.Usage.InputTokens
			usage.CachedInputTokens += response.value.Usage.CachedInputTokens
			usage.OutputTokens += response.value.Usage.OutputTokens
			usage.AudioSeconds += response.value.Usage.AudioSeconds
		}
		sort.Slice(calls, func(i, j int) bool { return calls[i].RequestID < calls[j].RequestID })
		sort.Strings(responseDigests)
		want := append([]string(nil), state.judge.ProviderResponseDigests...)
		sort.Strings(want)
		if !reflect.DeepEqual(responseDigests, want) || len(state.judge.Primitives) == 0 {
			return nil, errors.New("judge/provider lineage mismatch")
		}
		bySnapshot[state.identity] = append(bySnapshot[state.identity], RawSample{CorpusItemDigest: key.item, ProviderCalls: calls, ReferenceLabelHash: state.referenceDigest, JudgeArtifactHash: state.judgeDigest, Primitives: state.judge.Primitives, Usage: usage})
	}
	result := make([]RawSnapshot, 0, len(bySnapshot))
	for identity, samples := range bySnapshot {
		sort.Slice(samples, func(i, j int) bool { return samples[i].CorpusItemDigest < samples[j].CorpusItemDigest })
		result = append(result, RawSnapshot{Identity: identity, Samples: samples})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Identity.ID < result[j].Identity.ID })
	return result, nil
}

func ReplayProbeArtifacts(artifacts []RawArtifact) ([]ProbeAssertion, error) {
	type state struct {
		expected, actual, request, response, reference, judgeArtifact *RawArtifact
		judge                                                         *IntegratedAssertionEvidence
	}
	seen := map[string]bool{}
	states := map[string]*state{}
	for _, artifact := range artifacts {
		if artifact.Validate() != nil || (artifact.Kind != ArtifactIntegrated && artifact.Kind != ArtifactProbeExpected && artifact.Kind != ArtifactProbeActual && artifact.Kind != ArtifactProbeRequest && artifact.Kind != ArtifactProbeResponse && artifact.Kind != ArtifactProbeReference) || seen[artifact.Kind+"\x00"+artifact.Name] {
			return nil, errors.New("invalid integrated artifact")
		}
		seen[artifact.Kind+"\x00"+artifact.Name] = true
		current := states[artifact.Name]
		if current == nil {
			current = &state{}
			states[artifact.Name] = current
		}
		switch artifact.Kind {
		case ArtifactProbeExpected:
			current.expected = &artifact
		case ArtifactProbeActual:
			current.actual = &artifact
		case ArtifactProbeRequest:
			current.request = &artifact
		case ArtifactProbeResponse:
			current.response = &artifact
		case ArtifactProbeReference:
			current.reference = &artifact
		case ArtifactIntegrated:
			var value IntegratedAssertionEvidence
			if strictDecode(artifact.Payload, &value) != nil || value.Schema != "bonfire.w2.integrated-judge.v1" || value.Name != artifact.Name {
				return nil, errors.New("integrated judge artifact is invalid")
			}
			current.judge = &value
			current.judgeArtifact = &artifact
		}
	}
	result := make([]ProbeAssertion, 0, len(states))
	for name, current := range states {
		if current.expected == nil || current.actual == nil || current.request == nil || current.response == nil || current.reference == nil || current.judge == nil || current.judgeArtifact == nil {
			return nil, errors.New("integrated artifact set is incomplete")
		}
		var expected, actual, reference ProbeValueEvidence
		var request ProbeRequestEvidence
		var response ProbeResponseEvidence
		if strictDecode(current.expected.Payload, &expected) != nil || strictDecode(current.actual.Payload, &actual) != nil || strictDecode(current.request.Payload, &request) != nil || strictDecode(current.response.Payload, &response) != nil || strictDecode(current.reference.Payload, &reference) != nil || expected.Schema != "bonfire.w2.probe-expected.v1" || actual.Schema != "bonfire.w2.probe-actual.v1" || request.Schema != "bonfire.w2.probe-provider-request.v1" || response.Schema != "bonfire.w2.probe-provider-response.v1" || reference.Schema != "bonfire.w2.probe-reference.v1" || expected.Name != name || actual.Name != name || request.Name != name || response.Name != name || reference.Name != name || expected.Value != actual.Value || request.RequestID == "" || request.ObservedAt.IsZero() || response.RequestID != request.RequestID || response.ObservedAt.IsZero() || response.RequestArtifactDigest != current.request.SHA256 || response.Usage.Validate() != nil || current.judge.ExpectedArtifactDigest != current.expected.SHA256 || current.judge.ActualArtifactDigest != current.actual.SHA256 || current.judge.ProviderRequestDigest != current.request.SHA256 || current.judge.ProviderResponseDigest != current.response.SHA256 || current.judge.ReferenceArtifactDigest != current.reference.SHA256 {
			return nil, errors.New("integrated assertion artifacts failed semantic replay")
		}
		result = append(result, ProbeAssertion{Name: name, ProviderArtifact: ProviderArtifact{RequestID: request.RequestID, RequestDigest: current.request.SHA256, ResponseDigest: current.response.SHA256, ObservedAt: response.ObservedAt}, ReferenceDigest: current.reference.SHA256, JudgeDigest: current.judgeArtifact.SHA256, ExpectedDigest: w2gate.DigestBytes([]byte(expected.Value)), ActualDigest: w2gate.DigestBytes([]byte(actual.Value)), Usage: response.Usage})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func aggregatePrimitives(metric string, primitives []MetricPrimitive) (float64, error) {
	if len(primitives) == 0 {
		return 0, fmt.Errorf("missing primitive evidence for %s", metric)
	}
	kind := primitives[0].Kind
	if strings.Contains(strings.ToLower(metric), "confidence_half_width") {
		if kind != PrimitiveWilson {
			return 0, fmt.Errorf("confidence metric %s requires binomial evidence", metric)
		}
	} else if unitMetric(metric) && kind != PrimitiveRatio {
		return 0, fmt.Errorf("unit metric %s requires numerator/denominator evidence", metric)
	}
	values := make([]float64, 0, len(primitives))
	var numerator, denominator int64
	for _, primitive := range primitives {
		if primitive.Metric != metric || primitive.Kind != kind || primitive.Validate() != nil {
			return 0, fmt.Errorf("mixed or invalid primitive evidence for %s", metric)
		}
		if kind == PrimitiveRatio || kind == PrimitiveWilson {
			numerator += primitive.Numerator
			denominator += primitive.Denominator
		} else {
			values = append(values, primitive.Value)
		}
	}
	if kind == PrimitiveWilson {
		p := float64(numerator) / float64(denominator)
		z := 1.959963984540054
		n := float64(denominator)
		return z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / (1 + z*z/n), nil
	}
	if kind == PrimitiveRatio {
		return float64(numerator) / float64(denominator), nil
	}
	return aggregate(metric, values), nil
}

func (client Client) deriveCost(identity w2gate.EvaluationIdentity, usage UsageEvidence) (w2gate.CostSnapshot, error) {
	if err := usage.Validate(); err != nil {
		return w2gate.CostSnapshot{}, err
	}
	rate, ok := client.PriceCatalog.rate(identity.Provider, identity.Model)
	if !ok {
		return w2gate.CostSnapshot{}, fmt.Errorf("price catalog has no rate for %s/%s", identity.Provider, identity.Model)
	}
	uncached := usage.InputTokens - usage.CachedInputTokens
	estimated := float64(uncached)*rate.InputUSDPerMillion/1_000_000 +
		float64(usage.CachedInputTokens)*rate.CachedInputUSDPerMillion/1_000_000 +
		float64(usage.OutputTokens)*rate.OutputUSDPerMillion/1_000_000 +
		usage.AudioSeconds*rate.AudioUSDPerMinute/60
	return w2gate.CostSnapshot{
		Currency: "USD", EstimatedUSD: estimated, InputTokens: usage.InputTokens, CachedInputTokens: usage.CachedInputTokens,
		OutputTokens: usage.OutputTokens, AudioSeconds: usage.AudioSeconds,
		Price: w2gate.PriceEvidence{Catalog: client.PriceCatalog.Catalog, Version: client.PriceCatalog.Version, RetrievedAt: client.PriceCatalog.EffectiveAt, Digest: client.PriceCatalogDigest},
	}, nil
}

func (catalog PriceCatalog) rate(provider, model string) (PriceRate, bool) {
	for _, rate := range catalog.Rates {
		if rate.Provider == provider && rate.Model == model {
			return rate, true
		}
	}
	return PriceRate{}, false
}

func requiredMetrics(gate w2gate.GateDefinition, snapshot string) map[string]bool {
	metrics := map[string]bool{}
	for _, threshold := range gate.Thresholds {
		if threshold.Snapshot == snapshot {
			metrics[threshold.Metric] = true
		}
		if threshold.Reference != nil && threshold.Reference.Snapshot == snapshot {
			metrics[threshold.Reference.Metric] = true
		}
	}
	return metrics
}

func snapshotName(index int, identity w2gate.EvaluationIdentity) string {
	if index == 0 {
		return "baseline"
	}
	if index == 1 {
		return "candidate"
	}
	return "recordOnly:" + identity.ID
}

func aggregate(metric string, values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	lower := strings.ToLower(metric)
	if strings.Contains(lower, "p95") {
		index := int(math.Ceil(.95*float64(len(sorted)))) - 1
		if index < 0 {
			index = 0
		}
		return sorted[index]
	}
	if strings.HasPrefix(lower, "max_") || strings.Contains(lower, "repeatability") || strings.Contains(lower, "gap_") || strings.Contains(lower, "_delta") {
		return sorted[len(sorted)-1]
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	if unitMetric(metric) {
		return sum / float64(len(values))
	}
	return sum
}

func unitMetric(name string) bool {
	lower := strings.ToLower(name)
	for _, token := range []string{"_rate", "precision", "recall", "groundedness", "completeness", "honesty", "normalized_wer", "confidence_half_width", "coverage_gap", "parse_failure_delta"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// RawArtifactManifestDigest is the deterministic manifest of every corpus item
// and provider/reference/judge artifact in a gate response.
func RawArtifactManifestDigest(artifacts []RawArtifact) (string, error) {
	entries := append([]RawArtifact(nil), artifacts...)
	for _, artifact := range entries {
		if err := artifact.Validate(); err != nil {
			return "", err
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	raw, _ := json.Marshal(entries)
	return w2gate.DigestBytes(raw), nil
}

func ProbeArtifactManifestDigest(artifacts []RawArtifact) (string, error) {
	return RawArtifactManifestDigest(artifacts)
}

func (client Client) validate() error {
	if len(strings.TrimSpace(client.Token)) < 16 || client.CollectorKeyID == "" || len(client.CollectorPublicKey) != ed25519.PublicKeySize || !strongDigest(client.CollectorImplementationDigest) || !strongDigest(client.PriceCatalogDigest) || client.PriceCatalog.Validate() != nil {
		return errors.New("live collector custody, token, or price catalog is unavailable")
	}
	parsed, err := url.Parse(client.BaseURL)
	if err != nil || parsed.Host == "" {
		return errors.New("live collector URL is invalid")
	}
	host := strings.Split(parsed.Host, ":")[0]
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (host == "127.0.0.1" || host == "localhost")) {
		return errors.New("live collector requires HTTPS")
	}
	return nil
}

func (client Client) challenge() (string, error) {
	random := client.Random
	if random == nil {
		random = rand.Reader
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (client Client) now() time.Time {
	if client.Now != nil {
		return client.Now().UTC()
	}
	return time.Now().UTC()
}

func fresh(observedAt, expiresAt, now time.Time) bool {
	return !observedAt.IsZero() && !expiresAt.IsZero() && !observedAt.Before(now.Add(-15*time.Minute)) && !observedAt.After(now.Add(2*time.Minute)) && expiresAt.After(now) && expiresAt.After(observedAt) && expiresAt.Sub(observedAt) <= 30*time.Minute
}

func (client Client) post(ctx context.Context, path string, request any, response any) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(client.BaseURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+client.Token)
	httpClient := client.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("collector redirects are forbidden") }}
	}
	result, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		return fmt.Errorf("live collector returned HTTP %d", result.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(result.Body, maxResponseBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(response); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("collector returned trailing JSON")
	}
	return nil
}

func (response *CollectorResponse) Sign(keyID string, privateKey ed25519.PrivateKey) error {
	response.Signature = Signature{Algorithm: SignatureAlgorithm, KeyID: keyID}
	raw, err := response.signingBytes()
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("invalid collector signing custody")
	}
	response.Signature.Value = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, raw))
	return nil
}

func (response CollectorResponse) Verify(keyID string, publicKey ed25519.PublicKey) error {
	return verifySignature(response.Signature, keyID, publicKey, response.signingBytes)
}

func (response CollectorResponse) signingBytes() ([]byte, error) {
	response.Signature.Value = ""
	return json.Marshal(response)
}

func (response *SystemProbeResponse) Sign(keyID string, privateKey ed25519.PrivateKey) error {
	response.Signature = Signature{Algorithm: SignatureAlgorithm, KeyID: keyID}
	raw, err := response.signingBytes()
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("invalid collector signing custody")
	}
	response.Signature.Value = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, raw))
	return nil
}

func (response SystemProbeResponse) Verify(keyID string, publicKey ed25519.PublicKey) error {
	return verifySignature(response.Signature, keyID, publicKey, response.signingBytes)
}

func (response SystemProbeResponse) signingBytes() ([]byte, error) {
	response.Signature.Value = ""
	return json.Marshal(response)
}

func verifySignature(signature Signature, keyID string, publicKey ed25519.PublicKey, signingBytes func() ([]byte, error)) error {
	if signature.Algorithm != SignatureAlgorithm || signature.KeyID != keyID || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid Ed25519 signature identity")
	}
	rawSignature, err := base64.StdEncoding.DecodeString(signature.Value)
	if err != nil || len(rawSignature) != ed25519.SignatureSize {
		return errors.New("invalid Ed25519 signature encoding")
	}
	raw, err := signingBytes()
	if err != nil || !ed25519.Verify(publicKey, raw, rawSignature) {
		return errors.New("invalid Ed25519 signature")
	}
	return nil
}

func loadPublicKey(root string, reference w2gate.Ed25519KeyReference) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(root + string(os.PathSeparator) + reference.Path)
	if err != nil || w2gate.DigestBytes(raw) != reference.Digest {
		return nil, errors.New("pinned Ed25519 public key file is unavailable")
	}
	var file PublicKeyFile
	if strictDecode(raw, &file) != nil || file.Schema != PublicKeySchema || file.KeyID != reference.KeyID {
		return nil, errors.New("pinned Ed25519 public key file is invalid")
	}
	decoded, err := base64.StdEncoding.DecodeString(file.PublicKey)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("pinned Ed25519 public key bytes are invalid")
	}
	return ed25519.PublicKey(decoded), nil
}

func loadPriceCatalog(root string, file w2gate.CheckedFile, keyID string, publicKey ed25519.PublicKey) (PriceCatalog, error) {
	raw, err := os.ReadFile(root + string(os.PathSeparator) + file.Path)
	if err != nil || w2gate.DigestBytes(raw) != file.Digest {
		return PriceCatalog{}, errors.New("pinned price catalog is unavailable")
	}
	var catalog PriceCatalog
	if strictDecode(raw, &catalog) != nil || catalog.Validate() != nil || catalog.Signature.KeyID != keyID {
		return PriceCatalog{}, errors.New("pinned price catalog is invalid")
	}
	if err := verifySignature(catalog.Signature, keyID, publicKey, catalog.signingBytes); err != nil {
		return PriceCatalog{}, err
	}
	return catalog, nil
}

func (catalog PriceCatalog) Validate() error {
	if catalog.Schema != PriceCatalogSchema || strings.TrimSpace(catalog.Catalog) == "" || strings.TrimSpace(catalog.Version) == "" || catalog.Currency != "USD" || catalog.EffectiveAt.IsZero() || len(catalog.Rates) == 0 {
		return errors.New("price catalog metadata is invalid")
	}
	seen := map[string]bool{}
	for _, rate := range catalog.Rates {
		key := rate.Provider + "\x00" + rate.Model
		if strings.TrimSpace(rate.Provider) == "" || strings.TrimSpace(rate.Model) == "" || seen[key] || !finiteNonnegative(rate.InputUSDPerMillion) || !finiteNonnegative(rate.CachedInputUSDPerMillion) || !finiteNonnegative(rate.OutputUSDPerMillion) || !finiteNonnegative(rate.AudioUSDPerMinute) {
			return errors.New("price catalog rate is invalid")
		}
		seen[key] = true
	}
	return nil
}

func (catalog PriceCatalog) signingBytes() ([]byte, error) {
	catalog.Signature.Value = ""
	return json.Marshal(catalog)
}

func strictDecode(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func strongDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return false
	}
	for index := 1; index < len(raw); index++ {
		if raw[index] != raw[0] {
			return true
		}
	}
	return false
}

func finiteNonnegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
