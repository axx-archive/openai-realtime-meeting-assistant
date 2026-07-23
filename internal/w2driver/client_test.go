package w2driver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/w2gate"
)

const release = "0123456789abcdef0123456789abcdef01234567"

var now = time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

func TestCollectGateRecomputesMetricsAndPriceFromCorpusBoundPrimitives(t *testing.T) {
	publicKey, privateKey := testCollectorKey()
	gate, corpus := testGate()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer 0123456789abcdef" {
			t.Error("collector token missing")
		}
		var incoming CollectorRequest
		if err := json.NewDecoder(request.Body).Decode(&incoming); err != nil {
			t.Fatal(err)
		}
		response := validCollectorResponse(t, gate, corpus, incoming.ChallengeNonce, privateKey)
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()
	client := testClient(server.URL, publicKey)
	observation, err := client.CollectGate(context.Background(), w2gate.DigestBytes([]byte("manifest")), gate, corpus, release, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if observation.Baseline.Metrics["correctness_rate"] != .95 || observation.Candidate.Metrics["correctness_rate"] != 1 {
		t.Fatalf("locally derived metrics=%+v %+v", observation.Baseline.Metrics, observation.Candidate.Metrics)
	}
	if observation.Baseline.Cost.EstimatedUSD != .000568 || observation.Candidate.Cost.EstimatedUSD != .00132 {
		t.Fatalf("locally derived costs=%+v %+v", observation.Baseline.Cost, observation.Candidate.Cost)
	}
	if observation.HMACSHA256 != "" || observation.KeyID != "" || observation.Run.DriverID != gate.Driver.ID {
		t.Fatalf("driver self-signed or unbound observation=%+v", observation)
	}
}

func TestCheckedManifestLoadsPinnedCollectorAndSignedPriceCatalog(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	manifest, _, err := w2gate.LoadManifest(filepath.Join(root, "testdata", "w2", "gates.json"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := LoadCheckedClient(root, manifest, "https://collector.example", "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if client.CollectorKeyID != manifest.Evidence.CollectorKey.KeyID || client.PriceCatalog.Catalog != "bonfire-w2-operational-prices" || client.PriceCatalogDigest != manifest.Evidence.PriceCatalog.Digest {
		t.Fatalf("checked custody drifted: %+v", client)
	}
}

func TestCollectGateRejectsCollectorForgeryAndPrimitiveDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*CollectorResponse, ed25519.PrivateKey)
	}{
		{"forged collector signature", func(response *CollectorResponse, _ ed25519.PrivateKey) {
			response.Signature.Value = strings.Repeat("A", 88)
		}},
		{"identity drift", func(response *CollectorResponse, privateKey ed25519.PrivateKey) {
			response.Snapshots[1].Identity.Model = "different"
			_ = response.Sign(testCollectorKeyID, privateKey)
		}},
		{"ratio numerator exceeds denominator", func(response *CollectorResponse, privateKey ed25519.PrivateKey) {
			response.Snapshots[1].Samples[0].Primitives[0].Numerator = 11
			_ = response.Sign(testCollectorKeyID, privateKey)
		}},
		{"sample not in frozen corpus", func(response *CollectorResponse, privateKey ed25519.PrivateKey) {
			response.Snapshots[1].Samples[0].CorpusItemDigest = w2gate.DigestBytes([]byte("different item"))
			_ = response.Sign(testCollectorKeyID, privateKey)
		}},
		{"retained artifact payload tamper", func(response *CollectorResponse, privateKey ed25519.PrivateKey) {
			var request ProviderRequestEvidence
			_ = json.Unmarshal(response.Artifacts[0].Payload, &request)
			request.RequestID = "tampered-request"
			response.Artifacts[0].Payload, _ = json.Marshal(request)
			_ = response.Sign(testCollectorKeyID, privateKey)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			publicKey, privateKey := testCollectorKey()
			gate, corpus := testGate()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var incoming CollectorRequest
				_ = json.NewDecoder(request.Body).Decode(&incoming)
				response := validCollectorResponse(t, gate, corpus, incoming.ChallengeNonce, privateKey)
				test.mutate(&response, privateKey)
				_ = json.NewEncoder(writer).Encode(response)
			}))
			defer server.Close()
			client := testClient(server.URL, publicKey)
			if _, err := client.CollectGate(context.Background(), w2gate.DigestBytes([]byte("manifest")), gate, corpus, release, now); err == nil {
				t.Fatal("invalid collector response accepted")
			}
		})
	}
}

func TestCollectGateRejectsFreshWrapperAroundStaleProviderArtifacts(t *testing.T) {
	publicKey, privateKey := testCollectorKey()
	gate, corpus := testGate()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var incoming CollectorRequest
		_ = json.NewDecoder(request.Body).Decode(&incoming)
		response := validCollectorResponse(t, gate, corpus, incoming.ChallengeNonce, privateKey)
		shiftArtifactTimes(t, &response, now.Add(-24*time.Hour))
		_ = response.Sign(testCollectorKeyID, privateKey)
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()
	if _, err := testClient(server.URL, publicKey).CollectGate(context.Background(), w2gate.DigestBytes([]byte("manifest")), gate, corpus, release, now); err == nil || !strings.Contains(err.Error(), "stale provider evidence") {
		t.Fatalf("fresh wrapper accepted stale provider artifacts: %v", err)
	}
}

func TestSystemProbeRecomputesAssertionsWithoutCollectorPassBoolean(t *testing.T) {
	publicKey, privateKey := testCollectorKey()
	failed := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var incoming ProbeRequest
		_ = json.NewDecoder(request.Body).Decode(&incoming)
		assertion := testAssertion("recall-output")
		if failed {
			assertion.ActualDigest = w2gate.DigestBytes([]byte("wrong output"))
		}
		artifacts := integratedArtifacts(t, assertion.Name, "recall-output", "recall-output")
		if failed {
			artifacts = integratedArtifacts(t, assertion.Name, "recall-output", "wrong output")
		}
		assertions, _ := ReplayProbeArtifacts(artifacts)
		manifestDigest, _ := ProbeArtifactManifestDigest(artifacts)
		response := SystemProbeResponse{Schema: SystemProbeSchema, ProbeID: incoming.ProbeID, ReleaseCommit: release, ChallengeNonce: incoming.ChallengeNonce, CollectorImplementationDigest: testImplementationDigest, RawArtifactManifestDigest: manifestDigest, ObservedAt: now, ExpiresAt: now.Add(10 * time.Minute), Assertions: assertions, Artifacts: artifacts}
		_ = response.Sign(testCollectorKeyID, privateKey)
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()
	client := testClient(server.URL, publicKey)
	output, err := client.RunSystemProbe(context.Background(), "production-recall-replay", release)
	if err != nil {
		t.Fatal(err)
	}
	if output.Schema != SystemProbeOutputSchema || output.EvidenceDigest == "" || output.AssertionCount != 1 {
		t.Fatalf("output=%+v", output)
	}
	failed = true
	if _, err := client.RunSystemProbe(context.Background(), "production-recall-replay", release); err == nil {
		t.Fatal("mismatched expected/actual assertion accepted")
	}
}

const testCollectorKeyID = "bonfire-w2-test-collector"

var testImplementationDigest = w2gate.DigestBytes([]byte("checked collector implementation"))

func testClient(baseURL string, publicKey ed25519.PublicKey) Client {
	random := make([]byte, 32)
	for index := range random {
		random[index] = byte(index)
	}
	return Client{
		BaseURL: baseURL, Token: "0123456789abcdef",
		CollectorKeyID: testCollectorKeyID, CollectorPublicKey: publicKey, CollectorImplementationDigest: testImplementationDigest,
		PriceCatalog: testPriceCatalog(), PriceCatalogDigest: w2gate.DigestBytes([]byte("checked price catalog")),
		Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat(random, 8)),
	}
}

func testGate() (w2gate.GateDefinition, w2gate.CorpusManifest) {
	bar := .9
	base := w2gate.EvaluationIdentity{ID: "baseline-model", Lane: "brain", Provider: "openai", Model: "gpt-5.5", Effort: "low"}
	candidate := w2gate.EvaluationIdentity{ID: "candidate-model", Lane: "brain", Provider: "openai", Model: "gpt-5.6-luna", Effort: "low"}
	items := []string{w2gate.DigestBytes([]byte("item one")), w2gate.DigestBytes([]byte("item two"))}
	gate := w2gate.GateDefinition{ID: "w2d-brain-fleet-parity", Driver: w2gate.DriverSpec{ID: "bonfire-w2-evaluator-v1", Argv: []string{"go", "run", "./cmd/w2-evaluator", "--gate", "w2d-brain-fleet-parity", "--corpus-manifest", "testdata/w2/corpora/brain-fleet-parity.json", "--release-commit", "{releaseCommit}"}}, Corpus: w2gate.CorpusReference{Digest: w2gate.DigestBytes([]byte("corpus"))}, Thresholds: []w2gate.MetricThreshold{{ID: "baseline-correctness", Snapshot: "baseline", Metric: "correctness_rate", Operator: "gte", Value: &bar}, {ID: "candidate-correctness", Snapshot: "candidate", Metric: "correctness_rate", Operator: "gte", Value: &bar}}, Baseline: base, Candidate: candidate, RequiredEvidenceMode: w2gate.EvidenceModeLive}
	corpus := w2gate.CorpusManifest{ItemCount: len(items), ItemDigests: items, Custody: w2gate.CorpusCustody{Authority: "bonfire-custodian", ExportID: "export-brain", ItemMerkleRoot: w2gate.DigestBytes([]byte("items"))}}
	return gate, corpus
}

func validCollectorResponse(t *testing.T, gate w2gate.GateDefinition, corpus w2gate.CorpusManifest, nonce string, privateKey ed25519.PrivateKey) CollectorResponse {
	t.Helper()
	makeSamples := func(identity w2gate.EvaluationIdentity, numerators []int64) []RawSample {
		samples := make([]RawSample, len(corpus.ItemDigests))
		for index, item := range corpus.ItemDigests {
			samples[index] = RawSample{
				CorpusItemDigest:   item,
				ProviderCalls:      []ProviderArtifact{{RequestID: identity.ID + "-request-" + string(rune('a'+index)), RequestDigest: w2gate.DigestBytes([]byte(identity.ID + item + " request")), ResponseDigest: w2gate.DigestBytes([]byte(identity.ID + item + " response")), ObservedAt: now}},
				ReferenceLabelHash: w2gate.DigestBytes([]byte(item + " reference")), JudgeArtifactHash: w2gate.DigestBytes([]byte(identity.ID + item + " judge")),
				Primitives: []MetricPrimitive{{Metric: "correctness_rate", Kind: PrimitiveRatio, Numerator: numerators[index], Denominator: 10}},
				Usage:      UsageEvidence{InputTokens: 100, CachedInputTokens: 20, OutputTokens: 10},
			}
		}
		return samples
	}
	response := CollectorResponse{
		Schema: CollectorResponseSchema, GateID: gate.ID, ReleaseCommit: release, CorpusDigest: gate.Corpus.Digest, ChallengeNonce: nonce,
		CollectorImplementationDigest: testImplementationDigest, ObservedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}
	wantSnapshots := []RawSnapshot{{Identity: gate.Baseline, Samples: makeSamples(gate.Baseline, []int64{9, 10})}, {Identity: gate.Candidate, Samples: makeSamples(gate.Candidate, []int64{10, 10})}}
	response.Artifacts = artifactsForSnapshots(t, wantSnapshots)
	response.Snapshots, _ = ReplayGateArtifacts(response.Artifacts)
	response.RawArtifactManifestDigest, _ = RawArtifactManifestDigest(response.Artifacts)
	if err := response.Sign(testCollectorKeyID, privateKey); err != nil {
		t.Fatal(err)
	}
	return response
}

func artifactsForSnapshots(t *testing.T, snapshots []RawSnapshot) []RawArtifact {
	t.Helper()
	result := []RawArtifact{}
	add := func(kind, snapshot, item, name string, value any) RawArtifact {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return RawArtifact{Kind: kind, SnapshotID: snapshot, CorpusItemDigest: item, Name: name, SHA256: w2gate.DigestBytes(raw), Payload: raw}
	}
	for _, snapshot := range snapshots {
		for index, sample := range snapshot.Samples {
			prefix := fmt.Sprintf("%s-%d", snapshot.Identity.ID, index)
			request := add(ArtifactRequest, snapshot.Identity.ID, sample.CorpusItemDigest, prefix+"-request", ProviderRequestEvidence{Schema: "bonfire.w2.provider-request.v1", RequestID: sample.ProviderCalls[0].RequestID, Identity: snapshot.Identity, CorpusItemDigest: sample.CorpusItemDigest, ObservedAt: sample.ProviderCalls[0].ObservedAt})
			response := add(ArtifactResponse, snapshot.Identity.ID, sample.CorpusItemDigest, prefix+"-response", ProviderResponseEvidence{Schema: "bonfire.w2.provider-response.v1", RequestID: sample.ProviderCalls[0].RequestID, RequestArtifactDigest: request.SHA256, ObservedAt: sample.ProviderCalls[0].ObservedAt, Usage: sample.Usage})
			reference := add(ArtifactReference, snapshot.Identity.ID, sample.CorpusItemDigest, prefix+"-reference", ReferenceEvidence{Schema: "bonfire.w2.reference.v1", CorpusItemDigest: sample.CorpusItemDigest, LabelDigest: sample.ReferenceLabelHash})
			judge := add(ArtifactJudge, snapshot.Identity.ID, sample.CorpusItemDigest, prefix+"-judge", JudgeEvidence{Schema: "bonfire.w2.judge.v1", Identity: snapshot.Identity, CorpusItemDigest: sample.CorpusItemDigest, ReferenceArtifactDigest: reference.SHA256, ProviderResponseDigests: []string{response.SHA256}, Primitives: sample.Primitives})
			result = append(result, request, response, reference, judge)
		}
	}
	return result
}

func integratedArtifacts(t *testing.T, name, expected, actual string) []RawArtifact {
	t.Helper()
	assertion := testAssertion(name)
	makeArtifact := func(kind string, value any) RawArtifact {
		raw, _ := json.Marshal(value)
		return RawArtifact{Kind: kind, Name: name, SHA256: w2gate.DigestBytes(raw), Payload: raw}
	}
	expectedArtifact := makeArtifact(ArtifactProbeExpected, ProbeValueEvidence{Schema: "bonfire.w2.probe-expected.v1", Name: name, Value: expected})
	actualArtifact := makeArtifact(ArtifactProbeActual, ProbeValueEvidence{Schema: "bonfire.w2.probe-actual.v1", Name: name, Value: actual})
	request := makeArtifact(ArtifactProbeRequest, ProbeRequestEvidence{Schema: "bonfire.w2.probe-provider-request.v1", Name: name, RequestID: assertion.ProviderArtifact.RequestID, ObservedAt: assertion.ProviderArtifact.ObservedAt})
	response := makeArtifact(ArtifactProbeResponse, ProbeResponseEvidence{Schema: "bonfire.w2.probe-provider-response.v1", Name: name, RequestID: assertion.ProviderArtifact.RequestID, RequestArtifactDigest: request.SHA256, ObservedAt: assertion.ProviderArtifact.ObservedAt, Usage: assertion.Usage})
	reference := makeArtifact(ArtifactProbeReference, ProbeValueEvidence{Schema: "bonfire.w2.probe-reference.v1", Name: name, Value: "retained reference"})
	judge := makeArtifact(ArtifactIntegrated, IntegratedAssertionEvidence{Schema: "bonfire.w2.integrated-judge.v1", Name: name, ExpectedArtifactDigest: expectedArtifact.SHA256, ActualArtifactDigest: actualArtifact.SHA256, ProviderRequestDigest: request.SHA256, ProviderResponseDigest: response.SHA256, ReferenceArtifactDigest: reference.SHA256})
	return []RawArtifact{expectedArtifact, actualArtifact, request, response, reference, judge}
}

func shiftArtifactTimes(t *testing.T, response *CollectorResponse, at time.Time) {
	t.Helper()
	requests := map[string]string{}
	responses := map[string]string{}
	for index := range response.Artifacts {
		artifact := &response.Artifacts[index]
		old := artifact.SHA256
		switch artifact.Kind {
		case ArtifactRequest:
			var value ProviderRequestEvidence
			if err := json.Unmarshal(artifact.Payload, &value); err != nil {
				t.Fatal(err)
			}
			value.ObservedAt = at
			artifact.Payload, _ = json.Marshal(value)
			artifact.SHA256 = w2gate.DigestBytes(artifact.Payload)
			requests[old] = artifact.SHA256
		}
	}
	for index := range response.Artifacts {
		artifact := &response.Artifacts[index]
		if artifact.Kind != ArtifactResponse {
			continue
		}
		old := artifact.SHA256
		var value ProviderResponseEvidence
		_ = json.Unmarshal(artifact.Payload, &value)
		value.ObservedAt = at
		value.RequestArtifactDigest = requests[value.RequestArtifactDigest]
		artifact.Payload, _ = json.Marshal(value)
		artifact.SHA256 = w2gate.DigestBytes(artifact.Payload)
		responses[old] = artifact.SHA256
	}
	for index := range response.Artifacts {
		artifact := &response.Artifacts[index]
		if artifact.Kind != ArtifactJudge {
			continue
		}
		var value JudgeEvidence
		_ = json.Unmarshal(artifact.Payload, &value)
		for i, digest := range value.ProviderResponseDigests {
			value.ProviderResponseDigests[i] = responses[digest]
		}
		artifact.Payload, _ = json.Marshal(value)
		artifact.SHA256 = w2gate.DigestBytes(artifact.Payload)
	}
	response.Snapshots, _ = ReplayGateArtifacts(response.Artifacts)
	response.RawArtifactManifestDigest, _ = RawArtifactManifestDigest(response.Artifacts)
}

func testAssertion(name string) ProbeAssertion {
	digest := w2gate.DigestBytes([]byte(name + " expected"))
	return ProbeAssertion{
		Name:             name,
		ProviderArtifact: ProviderArtifact{RequestID: name + "-request", RequestDigest: w2gate.DigestBytes([]byte(name + " request")), ResponseDigest: w2gate.DigestBytes([]byte(name + " response")), ObservedAt: now},
		ReferenceDigest:  w2gate.DigestBytes([]byte(name + " reference")), JudgeDigest: w2gate.DigestBytes([]byte(name + " judge")), ExpectedDigest: digest, ActualDigest: digest,
		Usage: UsageEvidence{InputTokens: 20, CachedInputTokens: 2, OutputTokens: 5},
	}
}

func testPriceCatalog() PriceCatalog {
	return PriceCatalog{Schema: PriceCatalogSchema, Catalog: "bonfire-test-prices", Version: "2026-07", Currency: "USD", EffectiveAt: now,
		Rates: []PriceRate{{Provider: "openai", Model: "gpt-5.5", InputUSDPerMillion: 2, CachedInputUSDPerMillion: .2, OutputUSDPerMillion: 12}, {Provider: "openai", Model: "gpt-5.6-luna", InputUSDPerMillion: 5, CachedInputUSDPerMillion: .5, OutputUSDPerMillion: 25}}}
}

func testCollectorKey() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := []byte("0123456789abcdef0123456789abcdef")
	privateKey := ed25519.NewKeyFromSeed(seed)
	return privateKey.Public().(ed25519.PublicKey), privateKey
}

func testAuthority() w2gate.HMACAuthority {
	return w2gate.HMACAuthority{KeyID: "bonfire-driver-test-v1", Key: []byte("0123456789abcdef0123456789abcdef")}
}
