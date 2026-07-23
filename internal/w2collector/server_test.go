package w2collector

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/w2driver"
	"github.com/openai/openai-realtime-meeting-assistant/internal/w2gate"
)

const testRelease = "0123456789abcdef0123456789abcdef01234567"

var testNow = time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

func TestServerSignsOneUseChallengeOverRawArtifactManifest(t *testing.T) {
	seed := []byte("0123456789abcdef0123456789abcdef")
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	bundleDir := t.TempDir()
	gateID := "w2d-brain-commitments"
	corpusDigest := w2gate.DigestBytes([]byte("corpus"))
	itemDigest := w2gate.DigestBytes([]byte("item"))
	identity := w2gate.EvaluationIdentity{ID: "brain-baseline", Lane: "brain", Provider: "openai", Model: "gpt-5.5", Effort: "low"}
	writeArtifact := func(name, kind string, value any) (ArtifactReference, string) {
		raw, _ := json.Marshal(value)
		digest := w2gate.DigestBytes(raw)
		if err := os.WriteFile(filepath.Join(bundleDir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return ArtifactReference{Kind: kind, SnapshotID: identity.ID, CorpusItemDigest: itemDigest, Name: name, Path: name, SHA256: digest}, digest
	}
	requestRef, requestDigest := writeArtifact("request.json", w2driver.ArtifactRequest, w2driver.ProviderRequestEvidence{Schema: "bonfire.w2.provider-request.v1", RequestID: "provider-request-1", Identity: identity, CorpusItemDigest: itemDigest, ObservedAt: testNow})
	responseRef, responseDigest := writeArtifact("response.json", w2driver.ArtifactResponse, w2driver.ProviderResponseEvidence{Schema: "bonfire.w2.provider-response.v1", RequestID: "provider-request-1", RequestArtifactDigest: requestDigest, ObservedAt: testNow, Usage: w2driver.UsageEvidence{InputTokens: 10, OutputTokens: 2}})
	reference, referenceDigest := writeArtifact("reference.json", w2driver.ArtifactReference, w2driver.ReferenceEvidence{Schema: "bonfire.w2.reference.v1", CorpusItemDigest: itemDigest, LabelDigest: w2gate.DigestBytes([]byte("reference label"))})
	judge, _ := writeArtifact("judge.json", w2driver.ArtifactJudge, w2driver.JudgeEvidence{Schema: "bonfire.w2.judge.v1", Identity: identity, CorpusItemDigest: itemDigest, ReferenceArtifactDigest: referenceDigest, ProviderResponseDigests: []string{responseDigest}, Primitives: []w2driver.MetricPrimitive{{Metric: "correctness_rate", Kind: w2driver.PrimitiveRatio, Numerator: 1, Denominator: 1}}})
	bundle := GateArtifactManifest{Schema: GateBundleSchema, GateID: gateID, ReleaseCommit: testRelease, CorpusDigest: corpusDigest, Artifacts: []ArtifactReference{requestRef, responseRef, reference, judge}}
	raw, _ := json.Marshal(bundle)
	if err := os.WriteFile(filepath.Join(bundleDir, "gate-"+gateID+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	implementationDigest := w2gate.DigestBytes([]byte("implementation"))
	server := &Server{ReleaseCommit: testRelease, CollectorImplementationDigest: implementationDigest, KeyID: "collector-key", PrivateKey: privateKey, Token: "0123456789abcdef", BundleDir: bundleDir, Now: func() time.Time { return testNow }}
	request := w2driver.CollectorRequest{Schema: w2driver.CollectorRequestSchema, GateID: gateID, ReleaseCommit: testRelease, CorpusDigest: corpusDigest, ChallengeNonce: w2gate.DigestBytes([]byte("unique challenge")), CollectorImplementationDigest: implementationDigest}

	first := execute(t, server, "/v1/w2/gates/"+gateID, request)
	if first.Code != http.StatusOK {
		t.Fatalf("first response=%d %s", first.Code, first.Body.String())
	}
	var response w2driver.CollectorResponse
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ChallengeNonce != request.ChallengeNonce || response.Verify("collector-key", publicKey) != nil {
		t.Fatalf("response is not challenge-bound and independently signed: %+v", response)
	}
	wantManifest, _ := w2driver.RawArtifactManifestDigest(response.Artifacts)
	if response.RawArtifactManifestDigest != wantManifest {
		t.Fatalf("artifact manifest=%s want=%s", response.RawArtifactManifestDigest, wantManifest)
	}

	second := execute(t, server, "/v1/w2/gates/"+gateID, request)
	if second.Code != http.StatusConflict {
		t.Fatalf("replayed challenge status=%d", second.Code)
	}
}

func TestServerRejectsPrebuiltVerdictBundle(t *testing.T) {
	seed := []byte("0123456789abcdef0123456789abcdef")
	privateKey := ed25519.NewKeyFromSeed(seed)
	bundleDir := t.TempDir()
	gateID := "w2d-brain-commitments"
	corpusDigest := w2gate.DigestBytes([]byte("corpus"))
	prebuilt := map[string]any{"schema": GateBundleSchema, "gateId": gateID, "releaseCommit": testRelease, "corpusDigest": corpusDigest, "snapshots": []any{map[string]any{"verdict": "pass"}}}
	raw, _ := json.Marshal(prebuilt)
	if err := os.WriteFile(filepath.Join(bundleDir, "gate-"+gateID+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	implementationDigest := w2gate.DigestBytes([]byte("implementation"))
	server := &Server{ReleaseCommit: testRelease, CollectorImplementationDigest: implementationDigest, KeyID: "collector-key", PrivateKey: privateKey, Token: "0123456789abcdef", BundleDir: bundleDir, Now: func() time.Time { return testNow }}
	request := w2driver.CollectorRequest{Schema: w2driver.CollectorRequestSchema, GateID: gateID, ReleaseCommit: testRelease, CorpusDigest: corpusDigest, ChallengeNonce: w2gate.DigestBytes([]byte("unique prebuilt challenge")), CollectorImplementationDigest: implementationDigest}
	response := execute(t, server, "/v1/w2/gates/"+gateID, request)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("prebuilt verdict bundle status=%d body=%s", response.Code, response.Body.String())
	}
}

func execute(t *testing.T, server http.Handler, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(value)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer 0123456789abcdef")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}
