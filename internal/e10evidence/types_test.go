package e10evidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type signerFixture struct {
	registryPublic, operatorPublic, reviewerPublic, pilotReviewerOnePublic, pilotReviewerTwoPublic      ed25519.PublicKey
	registryPrivate, operatorPrivate, reviewerPrivate, pilotReviewerOnePrivate, pilotReviewerTwoPrivate ed25519.PrivateKey
	roots                                                                                               TrustRoots
	approved                                                                                            ApprovedTrustRoots
}

func TestRegistryRequiresExplicitApprovedTrustAnchorAndCanonicalBytes(t *testing.T) {
	fixture := newSignerFixture(t)
	trustRaw := mustJSON(t, fixture.roots)
	if _, err := LoadApprovedTrustRoots(trustRaw, hash("unapproved-trust-root")); err == nil || !strings.Contains(err.Error(), "approved") {
		t.Fatalf("unapproved trust-root bytes accepted: %v", err)
	}
	registry := validRegistry(fixture)
	raw := mustJSON(t, registry)
	signature := ed25519.Sign(fixture.registryPrivate, raw)

	verified, err := VerifyTargetRegistry(raw, signature, fixture.registryPublic, fixture.approved)
	if err != nil {
		t.Fatal(err)
	}
	if verified.RegistryID != registry.RegistryID {
		t.Fatal("wrong registry returned")
	}

	roguePublic, roguePrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	registry.Signer.SignerPublicKeyFingerprintSHA256 = PublicKeyFingerprint(roguePublic)
	rogueRaw := mustJSON(t, registry)
	rogueSignature := ed25519.Sign(roguePrivate, rogueRaw)
	if _, err := VerifyTargetRegistry(rogueRaw, rogueSignature, roguePublic, fixture.approved); err == nil {
		t.Fatalf("caller-supplied self-trust was accepted: %v", err)
	}

	formatted, err := json.MarshalIndent(verified, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTargetRegistry(formatted, ed25519.Sign(fixture.registryPrivate, formatted), fixture.registryPublic, fixture.approved); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("non-canonical signed bytes accepted: %v", err)
	}
}

func TestApprovedSignerCannotLoosenRegistryAfterPreMeasurementAnchor(t *testing.T) {
	fixture := newSignerFixture(t)
	tight := validRegistry(fixture)
	roomIndex := targetIndex(tight.Targets, "room-media-aggregate")
	joinIndex := thresholdIndex(tight.Targets[roomIndex].RequiredMetrics, "join_success_percent")
	tight.Targets[roomIndex].RequiredMetrics[joinIndex].Value = 99.9
	tightRaw := mustJSON(t, tight)

	roots := fixture.roots
	roots.PreMeasurementTargetRegistrySHA256 = RegistryDigest(tightRaw)
	rootsRaw := mustJSON(t, roots)
	approved, err := LoadApprovedTrustRoots(rootsRaw, RegistryDigest(rootsRaw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTargetRegistry(tightRaw, ed25519.Sign(fixture.registryPrivate, tightRaw), fixture.registryPublic, approved); err != nil {
		t.Fatalf("pre-measurement registry rejected: %v", err)
	}

	loosened := cloneTargetRegistry(t, tight)
	loosened.Targets[roomIndex].RequiredMetrics[joinIndex].Value = 99.5
	loosenedRaw := mustJSON(t, loosened)
	if _, err := VerifyTargetRegistry(loosenedRaw, ed25519.Sign(fixture.registryPrivate, loosenedRaw), fixture.registryPublic, approved); err == nil || !strings.Contains(err.Error(), "pre-measurement") {
		t.Fatalf("post-result loosening by approved signer was accepted: %v", err)
	}
}

func TestTrustRootDraftHasConstructibleFiveRoleShape(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "e10", "trust-roots.draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := DecodeStrict[TrustRoots](raw)
	if err != nil {
		t.Fatal(err)
	}
	draft.PreMeasurementTargetRegistrySHA256 = hash("approved-pre-measurement-registry")
	for index := range draft.ApprovedSigners {
		draft.ApprovedSigners[index].PublicKeyFingerprintSHA256 = hash(fmt.Sprintf("draft-signer-%d", index))
	}
	if err := ValidateTrustRoots(draft); err != nil {
		t.Fatalf("trust-root draft is not constructible: %v", err)
	}
}

func TestCorpusRequiresUniqueEvidenceFullPhysicalCoverageAndDualSignatures(t *testing.T) {
	fixture := newSignerFixture(t)
	registry, registryRaw := signedRegistry(t, fixture)
	manifest := validCorpus("composer_dictation", 250, fixture, registryRaw)
	raw := mustJSON(t, manifest)
	operatorSignature := ed25519.Sign(fixture.operatorPrivate, raw)
	reviewerSignature := ed25519.Sign(fixture.reviewerPrivate, raw)

	if err := ValidateCorpus(manifest, registry, RegistryDigest(registryRaw)); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePacketSignatures(raw, manifest.Approval, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved); err != nil {
		t.Fatal(err)
	}
	registrySignature := ed25519.Sign(fixture.registryPrivate, registryRaw)
	_, receipt, verifyErr := VerifyCorpusReceipt(raw, registryRaw, registrySignature, fixture.registryPublic, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved)
	if verifyErr != nil {
		t.Fatal(verifyErr)
	}
	if _, err := EncodeReceipt(receipt); err != nil {
		t.Fatalf("fully verified corpus receipt did not encode: %v", err)
	}

	duplicate := manifest
	duplicate.Clips = append([]CorpusClip(nil), manifest.Clips...)
	duplicate.Clips[1].AudioSHA256 = duplicate.Clips[0].AudioSHA256
	duplicate.Approval.SourceArtifactSetSHA256 = CorpusSourceArtifactSetDigest(duplicate.Clips)
	if err := ValidateCorpus(duplicate, registry, RegistryDigest(registryRaw)); err == nil || !strings.Contains(err.Error(), "duplicate audio") {
		t.Fatalf("duplicate relabeled audio accepted: %v", err)
	}
	crossKind := manifest
	crossKind.Clips = append([]CorpusClip(nil), manifest.Clips...)
	crossKind.Clips[1].ReferenceSHA256 = crossKind.Clips[0].AudioSHA256
	crossKind.Approval.SourceArtifactSetSHA256 = CorpusSourceArtifactSetDigest(crossKind.Clips)
	if err := ValidateCorpus(crossKind, registry, RegistryDigest(registryRaw)); err == nil || !strings.Contains(err.Error(), "duplicate reference") {
		t.Fatalf("cross-kind relabeled source evidence accepted: %v", err)
	}

	missing := manifest
	missing.Clips = append([]CorpusClip(nil), manifest.Clips...)
	for index := range missing.Clips {
		if missing.Clips[index].Platform == "ipad" && missing.Clips[index].ComposerSurface == "in_room" {
			missing.Clips[index].ComposerSurface = "team"
		}
	}
	missing.Approval.SourceArtifactSetSHA256 = CorpusSourceArtifactSetDigest(missing.Clips)
	if err := ValidateCorpus(missing, registry, RegistryDigest(registryRaw)); err == nil || !strings.Contains(err.Error(), "ipad/in_room") {
		t.Fatalf("missing physical platform/surface cross-product accepted: %v", err)
	}

	badReviewer := append([]byte(nil), reviewerSignature...)
	badReviewer[0] ^= 1
	if err := ValidatePacketSignatures(raw, manifest.Approval, operatorSignature, fixture.operatorPublic, badReviewer, fixture.reviewerPublic, fixture.approved); err == nil {
		t.Fatal("bad independent-review signature accepted")
	}
	_, unminted, verifyErr := VerifyCorpusReceipt(raw, registryRaw, registrySignature, fixture.registryPublic, operatorSignature, fixture.operatorPublic, badReviewer, fixture.reviewerPublic, fixture.approved)
	if verifyErr == nil {
		t.Fatal("invalid packet signature minted a receipt")
	}
	if _, err := EncodeReceipt(unminted); err == nil {
		t.Fatal("failed packet verification returned an encodable receipt")
	}
}

func TestMeetingCorpusDurationAndArtifactSetBinding(t *testing.T) {
	fixture := newSignerFixture(t)
	registry, registryRaw := signedRegistry(t, fixture)
	manifest := validCorpus("meeting_stt", 120, fixture, registryRaw)
	if err := ValidateCorpus(manifest, registry, RegistryDigest(registryRaw)); err != nil {
		t.Fatal(err)
	}
	short := manifest
	short.Clips = append([]CorpusClip(nil), manifest.Clips...)
	short.Clips[0].DurationMillis = 1
	if err := ValidateCorpus(short, registry, RegistryDigest(registryRaw)); err == nil || !strings.Contains(err.Error(), "60 minutes") {
		t.Fatalf("short corpus accepted: %v", err)
	}
	tampered := manifest
	tampered.Approval.SourceArtifactSetSHA256 = hash("wrong-set")
	if err := ValidateCorpus(tampered, registry, RegistryDigest(registryRaw)); err == nil || !strings.Contains(err.Error(), "artifact-set") {
		t.Fatalf("wrong artifact-set digest accepted: %v", err)
	}
}

func TestComposerDictationCorpusRejectsClipsOverThirtySeconds(t *testing.T) {
	fixture := newSignerFixture(t)
	registry, registryRaw := signedRegistry(t, fixture)
	manifest := validCorpus("composer_dictation", 250, fixture, registryRaw)
	manifest.Clips[0].DurationMillis = 30_001
	manifest.Approval.SourceArtifactSetSHA256 = CorpusSourceArtifactSetDigest(manifest.Clips)
	if err := ValidateCorpus(manifest, registry, RegistryDigest(registryRaw)); err == nil || !strings.Contains(err.Error(), "30 seconds") {
		t.Fatalf("overlong dictation clip accepted: %v", err)
	}
}

func TestPilotPacketBindsRegistryCandidateArtifactsAndIndependentSignatures(t *testing.T) {
	fixture := newSignerFixture(t)
	registry, registryRaw := signedRegistry(t, fixture)
	packet := validPilotPacket(fixture, registryRaw)
	if err := ValidatePilotPacket(packet, registry, RegistryDigest(registryRaw)); err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(t, packet)
	operatorSignature := ed25519.Sign(fixture.operatorPrivate, raw)
	reviewerSignature := ed25519.Sign(fixture.reviewerPrivate, raw)
	if err := ValidatePacketSignatures(raw, packet.Approval, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved); err != nil {
		t.Fatal(err)
	}
	_, receipt, verifyErr := VerifyPilotReceipt(raw, registryRaw, ed25519.Sign(fixture.registryPrivate, registryRaw), fixture.registryPublic, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved)
	if verifyErr != nil {
		t.Fatal(verifyErr)
	}
	if _, err := EncodeReceipt(receipt); err != nil {
		t.Fatal(err)
	}

	duplicate := packet
	duplicate.Pilots = append([]IOPilot(nil), packet.Pilots...)
	duplicate.Pilots[1].ArtifactDigest = duplicate.Pilots[0].ArtifactDigest
	duplicate.Approval.SourceArtifactSetSHA256 = PilotSourceArtifactSetDigest(duplicate)
	if err := ValidatePilotPacket(duplicate, registry, RegistryDigest(registryRaw)); err == nil || !strings.Contains(err.Error(), "duplicate source") {
		t.Fatalf("duplicate I&O evidence accepted: %v", err)
	}
}

func TestPilotReceiptRejectsReviewAuthorityAndTerminalEvidenceTampering(t *testing.T) {
	fixture := newSignerFixture(t)
	_, registryRaw := signedRegistry(t, fixture)

	for name, mutate := range map[string]func(*PilotPacket){
		"review-signature": func(packet *PilotPacket) {
			packet.Pilots[0].Reviews[0].SignatureHex = strings.Repeat("0", ed25519.SignatureSize*2)
		},
		"reviewer-identity-substitution": func(packet *PilotPacket) {
			packet.ReviewerRoster[1].ReviewerID = "substituted-reviewer"
			for index := range packet.Pilots {
				packet.Pilots[index].Reviews[1].ReviewerID = "substituted-reviewer"
			}
		},
		"reviewer-key-substitution": func(packet *PilotPacket) {
			packet.Pilots[0].Reviews[1].ReviewerPublicKeyHex = hex.EncodeToString(fixture.pilotReviewerOnePublic)
		},
		"disposition": func(packet *PilotPacket) {
			packet.Pilots[0].Disposition = "rejected"
		},
		"candidate": func(packet *PilotPacket) {
			packet.Candidate.RouteMapDigest = hash("wrong-route-map")
		},
		"eligibility": func(packet *PilotPacket) {
			packet.ReviewerRoster[0].EligibilityReceiptDigest = hash("mutated-eligibility")
		},
		"terminal-visibility": func(packet *PilotPacket) {
			packet.Pilots[8].TerminalVisibilityReceiptDigest = hash("mutated-terminal-visibility")
		},
		"external-effect-audit": func(packet *PilotPacket) {
			packet.Pilots[0].ExternalEffectAuditReceiptDigest = hash("mutated-external-effect-audit")
		},
		"missing-second-reviewer": func(packet *PilotPacket) {
			packet.ReviewerRoster = packet.ReviewerRoster[:1]
			for index := range packet.Pilots {
				packet.Pilots[index].Reviews = packet.Pilots[index].Reviews[:1]
			}
		},
		"invented-claim": func(packet *PilotPacket) {
			packet.Pilots[0].InventedAssertedClaimCount = 1
		},
		"external-write": func(packet *PilotPacket) {
			packet.Pilots[0].ExternalWriteCount = 1
		},
		"missing-external-effect-audit": func(packet *PilotPacket) {
			packet.Pilots[0].ExternalEffectAuditReceiptDigest = ""
		},
		"missing-terminal-visibility": func(packet *PilotPacket) {
			packet.Pilots[9].TerminalVisibilityReceiptDigest = ""
		},
		"eleventh-failed-pilot": func(packet *PilotPacket) {
			extra := packet.Pilots[9]
			extra.PilotID = "pilot-extra-failed"
			extra.InputDigest = hash("extra-input")
			extra.RunReceiptDigest = hash("extra-run")
			extra.ArtifactDigest = hash("extra-artifact")
			extra.Disposition = "failed"
			extra.DispositionReasonDigest = hash("extra-disposition")
			extra.TerminalVisibilityReceiptDigest = hash("extra-terminal-visibility")
			extra.ExternalEffectAuditReceiptDigest = hash("extra-external-effect-audit")
			packet.Pilots = append(packet.Pilots, extra)
		},
	} {
		t.Run(name, func(t *testing.T) {
			packet := clonePilotPacket(t, validPilotPacket(fixture, registryRaw))
			mutate(&packet)
			packet.Approval.SourceArtifactSetSHA256 = PilotSourceArtifactSetDigest(packet)
			assertPilotReceiptRejected(t, packet, registryRaw, fixture)
		})
	}

	t.Run("non-rooted-second-reviewer", func(t *testing.T) {
		packet := clonePilotPacket(t, validPilotPacket(fixture, registryRaw))
		roguePublic, roguePrivate := keyPair(t)
		packet.ReviewerRoster[1].ReviewerKeyID = "rogue-pilot-review-key"
		packet.ReviewerRoster[1].ReviewerPublicKeyFingerprintSHA256 = PublicKeyFingerprint(roguePublic)
		for index := range packet.Pilots {
			review := &packet.Pilots[index].Reviews[1]
			review.ReviewerKeyID = "rogue-pilot-review-key"
			review.ReviewerPublicKeyHex = hex.EncodeToString(roguePublic)
			payload, err := pilotReviewSigningPayload(packet, packet.Pilots[index], packet.ReviewerRoster[1], *review)
			if err != nil {
				t.Fatal(err)
			}
			review.SignatureHex = hex.EncodeToString(ed25519.Sign(roguePrivate, payload))
		}
		packet.Approval.SourceArtifactSetSHA256 = PilotSourceArtifactSetDigest(packet)
		assertPilotReceiptRejected(t, packet, registryRaw, fixture)
	})
}

func TestExternalMatrixEnforcesFullPreregisteredContract(t *testing.T) {
	fixture := newSignerFixture(t)
	registry, registryRaw := signedRegistry(t, fixture)
	matrix := validExternalMatrix("physical_device_webrtc", registry, fixture, registryRaw)
	if err := ValidateExternalMatrix(matrix, registry, RegistryDigest(registryRaw)); err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(t, matrix)
	operatorSignature := ed25519.Sign(fixture.operatorPrivate, raw)
	reviewerSignature := ed25519.Sign(fixture.reviewerPrivate, raw)
	if err := ValidatePacketSignatures(raw, matrix.Approval, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved); err != nil {
		t.Fatal(err)
	}
	_, receipt, verifyErr := VerifyMatrixReceipt(raw, registryRaw, ed25519.Sign(fixture.registryPrivate, registryRaw), fixture.registryPublic, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved)
	if verifyErr != nil {
		t.Fatal(verifyErr)
	}
	if _, err := EncodeReceipt(receipt); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*ExternalMatrix){
		"fixture":     func(value *ExternalMatrix) { value.Observations[0].FixtureSHA256 = hash("wrong") },
		"environment": func(value *ExternalMatrix) { value.Observations[0].Environment = "wrong-environment" },
		"sample":      func(value *ExternalMatrix) { value.Observations[0].SampleSize = 1 },
		"revision":    func(value *ExternalMatrix) { value.Observations[0].MeasurementRevisionSHA256 = hash("wrong-revision") },
		"metric": func(value *ExternalMatrix) {
			for key := range value.Observations[0].Metrics {
				delete(value.Observations[0].Metrics, key)
				break
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := matrix
			bad.Observations = append([]ExternalObservation(nil), matrix.Observations...)
			bad.Observations[0].Metrics = cloneMetrics(matrix.Observations[0].Metrics)
			mutate(&bad)
			bad.Approval.SourceArtifactSetSHA256 = ExternalSourceArtifactSetDigest(bad.Observations)
			if err := ValidateExternalMatrix(bad, registry, RegistryDigest(registryRaw)); err == nil {
				t.Fatal("contract drift accepted")
			}
		})
	}
}

func TestExternalMatrixRejectsStatisticalEvidenceTampering(t *testing.T) {
	fixture := newSignerFixture(t)
	registry, registryRaw := signedRegistry(t, fixture)
	base := validExternalMatrix("physical_device_webrtc", registry, fixture, registryRaw)

	for name, mutate := range map[string]func(*ExternalMatrix){
		"point-only-rate": func(matrix *ExternalMatrix) {
			setMatrixMetric(matrix, "room-media-aggregate", "join_success_percent", Metric{Value: 100, Unit: "percent"})
		},
		"point-only-latency": func(matrix *ExternalMatrix) {
			setMatrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds", Metric{Value: 1, Unit: "seconds"})
		},
		"numerator-value-mismatch": func(matrix *ExternalMatrix) {
			metric := matrixMetric(matrix, "room-media-aggregate", "join_success_percent")
			metric.Value -= .1
			setMatrixMetric(matrix, "room-media-aggregate", "join_success_percent", metric)
		},
		"wilson-mismatch": func(matrix *ExternalMatrix) {
			metric := matrixMetric(matrix, "room-media-aggregate", "join_success_percent")
			metric.Interval95.Low += .1
			setMatrixMetric(matrix, "room-media-aggregate", "join_success_percent", metric)
		},
		"p50-mismatch": func(matrix *ExternalMatrix) {
			metric := matrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds")
			*metric.P50 += .1
			setMatrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds", metric)
		},
		"p95-mismatch": func(matrix *ExternalMatrix) {
			metric := matrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds")
			*metric.P95 += .1
			setMatrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds", metric)
		},
		"p99-mismatch": func(matrix *ExternalMatrix) {
			metric := matrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds")
			*metric.P99 += .1
			setMatrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds", metric)
		},
		"bootstrap-mismatch": func(matrix *ExternalMatrix) {
			metric := matrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds")
			metric.Interval95.High += .1
			setMatrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds", metric)
		},
		"sample-count-mismatch": func(matrix *ExternalMatrix) {
			metric := matrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds")
			metric.Samples = metric.Samples[:len(metric.Samples)-1]
			setMatrixMetric(matrix, "room-media-aggregate", "first_remote_audio_p95_seconds", metric)
		},
		"point-pass-conservative-rate-fail": func(matrix *ExternalMatrix) {
			metric := matrixMetric(matrix, "room-media-aggregate", "join_success_percent")
			numerator, denominator := 199, 200
			low, high := wilsonInterval95(numerator, denominator)
			metric.Value = 99.5
			metric.Numerator, metric.Denominator = &numerator, &denominator
			metric.Interval95 = &StatisticalInterval{Low: low, High: high, Method: "wilson_95"}
			setMatrixMetric(matrix, "room-media-aggregate", "join_success_percent", metric)
		},
		"exact-100-with-one-failure": func(matrix *ExternalMatrix) {
			metric := matrixMetric(matrix, "locked-device-push-deep-link", "locked_delivery_success_percent")
			numerator, denominator := 99, 100
			low, high := wilsonInterval95(numerator, denominator)
			metric.Value = 99
			metric.Numerator, metric.Denominator = &numerator, &denominator
			metric.Interval95 = &StatisticalInterval{Low: low, High: high, Method: "wilson_95"}
			setMatrixMetric(matrix, "locked-device-push-deep-link", "locked_delivery_success_percent", metric)
		},
		"fake-statistics-on-scalar": func(matrix *ExternalMatrix) {
			metric := matrixMetric(matrix, "room-media-aggregate", "concurrent_room_count")
			numerator, denominator := 2, 2
			metric.Numerator, metric.Denominator = &numerator, &denominator
			setMatrixMetric(matrix, "room-media-aggregate", "concurrent_room_count", metric)
		},
		"point-pass-conservative-latency-fail": func(matrix *ExternalMatrix) {
			observation := matrixObservation(matrix, "packet-loss-disconnect-rejoin-cleanup")
			observation.SampleSize = 20
			target := registry.Targets[targetIndex(registry.Targets, "packet-loss-disconnect-rejoin-cleanup")]
			for _, threshold := range target.RequiredMetrics {
				observation.Metrics[threshold.Name] = passingMetric(threshold, observation.SampleSize)
			}
			metric := observation.Metrics["cleanup_p95_seconds"]
			metric.Samples = make([]float64, observation.SampleSize)
			for index := range metric.Samples {
				metric.Samples[index] = 1
			}
			metric.Samples[len(metric.Samples)-1] = 20
			values := append([]float64(nil), metric.Samples...)
			sort.Float64s(values)
			p50, p95, p99 := percentileFloat(values, .50), percentileFloat(values, .95), percentileFloat(values, .99)
			low, high := deterministicBootstrapQuantile95(metric.Samples, .95, "cleanup_p95_seconds")
			if p95 > 10 || high <= 10 {
				t.Fatalf("test fixture does not isolate conservative latency bound: p95=%v high=%v", p95, high)
			}
			metric.Value, metric.P50, metric.P95, metric.P99 = p95, &p50, &p95, &p99
			metric.Interval95 = &StatisticalInterval{Low: low, High: high, Method: "deterministic_bootstrap_95"}
			observation.Metrics["cleanup_p95_seconds"] = metric
		},
	} {
		t.Run(name, func(t *testing.T) {
			matrix := cloneExternalMatrix(t, base)
			mutate(&matrix)
			matrix.Approval.SourceArtifactSetSHA256 = ExternalSourceArtifactSetDigest(matrix.Observations)
			assertMatrixReceiptRejected(t, matrix, registryRaw, fixture)
		})
	}
}

func TestCriticalTargetThresholdSetsCannotCollapseToLabels(t *testing.T) {
	fixture := newSignerFixture(t)
	registry := validRegistry(fixture)
	if err := ValidateTargetRegistry(registry); err != nil {
		t.Fatal(err)
	}
	roomIndex := targetIndex(registry.Targets, "room-media-aggregate")
	registry.Targets[roomIndex].RequiredMetrics = registry.Targets[roomIndex].RequiredMetrics[:1]
	if err := ValidateTargetRegistry(registry); err == nil || !strings.Contains(err.Error(), "multi-metric") {
		t.Fatalf("single-label target accepted: %v", err)
	}
	registry = validRegistry(fixture)
	roomIndex = targetIndex(registry.Targets, "room-media-aggregate")
	registry.Targets[roomIndex].RequiredMetrics[0].Value = 99
	if err := ValidateTargetRegistry(registry); err == nil || !strings.Contains(err.Error(), "weakens") {
		t.Fatalf("weakened room aggregate floor accepted: %v", err)
	}
	registry = validRegistry(fixture)
	registry.Targets = append([]EvidenceTarget(nil), registry.Targets[:len(registry.Targets)-1]...)
	if err := ValidateTargetRegistry(registry); err == nil || !strings.Contains(err.Error(), "omits required target") {
		t.Fatalf("missing required target accepted: %v", err)
	}
}

func TestRequiredTargetInventoryAndEveryFloorAreNonWeakenable(t *testing.T) {
	expectedIDs := []string{
		"bluetooth-audio-route-change",
		"browser-native-mixed-room",
		"camera-switch-background-lock",
		"canonical-data-rpo",
		"control-data-rpo",
		"crash-restart-idempotency",
		"default-deny-egress-enforcement",
		"encrypted-immutable-offsite-custody",
		"ephemeral-worker-per-run",
		"external-write-and-agent-loop-gates",
		"gallery-speaker-expanded-screen-share",
		"guest-room-boundary",
		"independent-key-and-restore-host-custody",
		"induced-ai-failure-human-media",
		"live-app-control-failover",
		"locked-device-push-deep-link",
		"multiple-devices-one-account",
		"no-production-or-company-brain-mount",
		"packet-loss-disconnect-rejoin-cleanup",
		"purge-continuity",
		"restrictive-network-turn",
		"retained-release-rollback",
		"room-media-aggregate",
		"short-lived-run-bound-credentials",
		"signed-authenticated-four-root-restore",
		"signed-callback-and-replay-fence",
		"turn-failover-and-session-drain",
		"twenty-four-hour-ten-sitting-soak",
		"two-three-person-two-hour-rooms",
		"worker-resource-and-time-caps",
	}
	actualIDs := make([]string, 0, len(requiredTargetContracts))
	for id := range requiredTargetContracts {
		actualIDs = append(actualIDs, id)
	}
	sort.Strings(actualIDs)
	if !reflect.DeepEqual(actualIDs, expectedIDs) {
		t.Fatalf("required target inventory drifted\n got: %v\nwant: %v", actualIDs, expectedIDs)
	}

	fixture := newSignerFixture(t)
	base := validRegistry(fixture)
	for _, id := range expectedIDs {
		id := id
		t.Run(id+"/omitted", func(t *testing.T) {
			registry := cloneTargetRegistry(t, base)
			index := targetIndex(registry.Targets, id)
			registry.Targets = append(registry.Targets[:index], registry.Targets[index+1:]...)
			assertRegistryRejected(t, registry)
		})
		t.Run(id+"/category", func(t *testing.T) {
			registry := cloneTargetRegistry(t, base)
			target := &registry.Targets[targetIndex(registry.Targets, id)]
			if target.Category == "ha_dr" {
				target.Category = "worker_orchestrator"
			} else {
				target.Category = "ha_dr"
			}
			assertRegistryRejected(t, registry)
		})
		t.Run(id+"/artifacts", func(t *testing.T) {
			registry := cloneTargetRegistry(t, base)
			registry.Targets[targetIndex(registry.Targets, id)].MinimumArtifacts--
			assertRegistryRejected(t, registry)
		})
		t.Run(id+"/sample", func(t *testing.T) {
			registry := cloneTargetRegistry(t, base)
			registry.Targets[targetIndex(registry.Targets, id)].MinimumSampleSize--
			assertRegistryRejected(t, registry)
		})
		for metricIndex := range requiredTargetContracts[id].Metrics {
			metricIndex := metricIndex
			t.Run(fmt.Sprintf("%s/metric-%d", id, metricIndex), func(t *testing.T) {
				registry := cloneTargetRegistry(t, base)
				metric := &registry.Targets[targetIndex(registry.Targets, id)].RequiredMetrics[metricIndex]
				switch metric.Comparator {
				case "at_least":
					metric.Value--
				case "at_most", "exactly":
					metric.Value++
				}
				assertRegistryRejected(t, registry)
			})
		}
	}

	draftRaw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "e10", "target-registry.draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := DecodeStrict[TargetRegistry](draftRaw)
	if err != nil {
		t.Fatal(err)
	}
	draftIDs := make([]string, 0, len(draft.Targets))
	for _, target := range draft.Targets {
		draftIDs = append(draftIDs, target.ID)
	}
	sort.Strings(draftIDs)
	if !reflect.DeepEqual(draftIDs, expectedIDs) {
		t.Fatalf("draft target inventory drifted\n got: %v\nwant: %v", draftIDs, expectedIDs)
	}
	draft.Signer = base.Signer
	draft.Candidate = base.Candidate
	for index := range draft.Targets {
		draft.Targets[index].FixtureSHA256 = hash("draft-fixture-" + draft.Targets[index].ID)
		draft.Targets[index].MeasurementRevisionSHA256 = hash("draft-measurement-code-" + draft.Targets[index].ID)
	}
	if err := ValidateTargetRegistry(draft); err != nil {
		t.Fatalf("draft target contract does not satisfy the non-weakenable floor: %v", err)
	}
}

func TestTargetRegistryRequiresMeasurementCodeDigestNotMutableLabel(t *testing.T) {
	fixture := newSignerFixture(t)
	registry := validRegistry(fixture)
	registry.Targets[0].MeasurementRevisionSHA256 = "measurement-v1"
	if err := ValidateTargetRegistry(registry); err == nil || !strings.Contains(err.Error(), "measurement-code SHA-256") {
		t.Fatalf("mutable measurement revision label accepted: %v", err)
	}
}

func TestStrictDecodeAndReceiptClaimsFailClosed(t *testing.T) {
	fixture := newSignerFixture(t)
	registry := validRegistry(fixture)
	raw := mustJSON(t, registry)
	registrySignature := ed25519.Sign(fixture.registryPrivate, raw)
	withUnknown := append([]byte(nil), raw[:len(raw)-1]...)
	withUnknown = append(withUnknown, []byte(`,"qualification":"pass"}`)...)
	if _, err := DecodeStrict[TargetRegistry](withUnknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field accepted: %v", err)
	}
	duplicate := append([]byte(`{"schemaVersion":"stride.e10.target-registry/v3",`), raw[1:]...)
	if _, err := DecodeStrict[TargetRegistry](duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate key accepted: %v", err)
	}
	_, receipt, verifyErr := VerifyTargetRegistryReceipt(raw, registrySignature, fixture.registryPublic, fixture.approved)
	if verifyErr != nil {
		t.Fatal(verifyErr)
	}
	if _, err := EncodeReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	forged := receipt
	forged.proof = [sha256.Size]byte{}
	if _, err := EncodeReceipt(forged); err == nil || !strings.Contains(err.Error(), "not minted") {
		t.Fatalf("raw shape-valid receipt construction minted a receipt: %v", err)
	}
	mutated := receipt
	mutated.receipt.ItemCount++
	if _, err := EncodeReceipt(mutated); err == nil || !strings.Contains(err.Error(), "mutated") {
		t.Fatalf("valid-shape sealed receipt mutation was accepted: %v", err)
	}
	receipt.receipt.State = "provider_qualified"
	if _, err := EncodeReceipt(receipt); err == nil {
		t.Fatal("arbitrary state encoded")
	}
	_, receipt, verifyErr = VerifyTargetRegistryReceipt(raw, registrySignature, fixture.registryPublic, fixture.approved)
	if verifyErr != nil {
		t.Fatal(verifyErr)
	}
	receipt.receipt.EvidenceClass = "production_accepted"
	if _, err := EncodeReceipt(receipt); err == nil {
		t.Fatal("arbitrary evidence class encoded")
	}
	badRegistrySignature := append([]byte(nil), registrySignature...)
	badRegistrySignature[0] ^= 1
	_, unminted, verifyErr := VerifyTargetRegistryReceipt(raw, badRegistrySignature, fixture.registryPublic, fixture.approved)
	if verifyErr == nil {
		t.Fatal("invalid registry signature minted a receipt")
	}
	if _, err := EncodeReceipt(unminted); err == nil {
		t.Fatal("failed registry verification returned an encodable receipt")
	}
}

func newSignerFixture(t *testing.T) signerFixture {
	t.Helper()
	registryPublic, registryPrivate := keyPair(t)
	operatorPublic, operatorPrivate := keyPair(t)
	reviewerPublic, reviewerPrivate := keyPair(t)
	pilotReviewerOnePublic, pilotReviewerOnePrivate := keyPair(t)
	pilotReviewerTwoPublic, pilotReviewerTwoPrivate := keyPair(t)
	fixture := signerFixture{
		registryPublic: registryPublic, registryPrivate: registryPrivate,
		operatorPublic: operatorPublic, operatorPrivate: operatorPrivate,
		reviewerPublic: reviewerPublic, reviewerPrivate: reviewerPrivate,
		pilotReviewerOnePublic: pilotReviewerOnePublic, pilotReviewerOnePrivate: pilotReviewerOnePrivate,
		pilotReviewerTwoPublic: pilotReviewerTwoPublic, pilotReviewerTwoPrivate: pilotReviewerTwoPrivate,
	}
	preMeasurementRegistryRaw := mustJSON(t, validRegistry(fixture))
	roots := TrustRoots{SchemaVersion: TrustRootsSchema, TrustRootID: "approved-e10-roots-001", PreMeasurementTargetRegistrySHA256: RegistryDigest(preMeasurementRegistryRaw), ApprovedSigners: []ApprovedSigner{
		{KeyID: "registry-key-001", IdentityID: "release-owner", Role: "registry_owner", PublicKeyFingerprintSHA256: PublicKeyFingerprint(registryPublic)},
		{KeyID: "operator-key-001", IdentityID: "evidence-operator", Role: "operator", PublicKeyFingerprintSHA256: PublicKeyFingerprint(operatorPublic)},
		{KeyID: "reviewer-key-001", IdentityID: "independent-reviewer", Role: "independent_reviewer", PublicKeyFingerprintSHA256: PublicKeyFingerprint(reviewerPublic)},
		{KeyID: "pilot-reviewer-key-001", IdentityID: "reviewer-one", Role: "pilot_reviewer", PublicKeyFingerprintSHA256: PublicKeyFingerprint(pilotReviewerOnePublic)},
		{KeyID: "pilot-reviewer-key-002", IdentityID: "reviewer-two", Role: "pilot_reviewer", PublicKeyFingerprintSHA256: PublicKeyFingerprint(pilotReviewerTwoPublic)},
	}}
	rootsRaw := mustJSON(t, roots)
	approved, err := LoadApprovedTrustRoots(rootsRaw, RegistryDigest(rootsRaw))
	if err != nil {
		t.Fatal(err)
	}
	fixture.roots, fixture.approved = roots, approved
	return fixture
}

func keyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey, privateKey
}

func validRegistry(fixture signerFixture) TargetRegistry {
	registry := TargetRegistry{
		SchemaVersion: TargetRegistrySchema,
		RegistryID:    "e10-targets-001",
		Signer:        RegistrySignerBinding{SignerKeyID: "registry-key-001", SignerIdentityID: "release-owner", SignerPublicKeyFingerprintSHA256: PublicKeyFingerprint(fixture.registryPublic)},
		Candidate:     validCandidate(),
	}
	ids := make([]string, 0, len(requiredTargetContracts))
	for id := range requiredTargetContracts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		floor := requiredTargetContracts[id]
		value := target(id, floor.Category, append([]MetricThreshold(nil), floor.Metrics...))
		value.MinimumArtifacts = floor.MinimumArtifacts
		value.MinimumSampleSize = floor.MinimumSampleSize
		registry.Targets = append(registry.Targets, value)
	}
	return registry
}

func target(id, category string, metrics []MetricThreshold) EvidenceTarget {
	return EvidenceTarget{ID: id, Category: category, FixtureSHA256: hash("fixture-" + id), Environment: "physical-staging-001", MinimumArtifacts: 1, MinimumSampleSize: 3, MeasurementRevisionSHA256: hash("measurement-" + id), OwnerID: "evidence-operator", IndependentReviewerID: "independent-reviewer", RollbackTrigger: "rollback-on-threshold-failure", PhysicalOrProduction: true, RequiredMetrics: metrics}
}

func targetIndex(targets []EvidenceTarget, id string) int {
	for index := range targets {
		if targets[index].ID == id {
			return index
		}
	}
	panic("missing test target " + id)
}

func thresholdIndex(thresholds []MetricThreshold, name string) int {
	for index := range thresholds {
		if thresholds[index].Name == name {
			return index
		}
	}
	panic("missing test threshold " + name)
}

func signedRegistry(t *testing.T, fixture signerFixture) (TargetRegistry, []byte) {
	t.Helper()
	registry := validRegistry(fixture)
	raw := mustJSON(t, registry)
	if _, err := VerifyTargetRegistry(raw, ed25519.Sign(fixture.registryPrivate, raw), fixture.registryPublic, fixture.approved); err != nil {
		t.Fatal(err)
	}
	return registry, raw
}

func validApproval(fixture signerFixture, registryRaw []byte) DualApprovalBinding {
	return DualApprovalBinding{RegistrySHA256: RegistryDigest(registryRaw), OperatorID: "evidence-operator", OperatorKeyID: "operator-key-001", OperatorPublicKeyFingerprintSHA256: PublicKeyFingerprint(fixture.operatorPublic), ReviewerID: "independent-reviewer", ReviewerKeyID: "reviewer-key-001", ReviewerPublicKeyFingerprintSHA256: PublicKeyFingerprint(fixture.reviewerPublic)}
}

func validCorpus(lane string, count int, fixture signerFixture, registryRaw []byte) CorpusManifest {
	manifest := CorpusManifest{SchemaVersion: CorpusManifestSchema, CorpusID: lane + "-001", Lane: lane, EvidenceClass: "authorized_real_capture", Candidate: validCandidate(), Approval: validApproval(fixture, registryRaw)}
	platforms := []string{"web", "iphone", "ipad"}
	surfaces := []string{"scout", "private_thread", "team", "project", "in_room"}
	for index := 0; index < count; index++ {
		clip := CorpusClip{ClipID: id("clip", index), AudioSHA256: hash(fmt.Sprintf("audio-%d", index)), ReferenceSHA256: hash(fmt.Sprintf("reference-%d", index)), ConsentReceiptSHA256: hash(fmt.Sprintf("consent-%d", index)), SpeakerIDHash: hash(fmt.Sprintf("speaker-id-%d", index%12)), SpeakerEvidenceSHA256: hash(fmt.Sprintf("speaker-evidence-%d", index)), TrackID: id("track", index), TrackEvidenceSHA256: hash(fmt.Sprintf("track-evidence-%d", index)), DurationMillis: 30_000, Platform: "meeting_room", SourceOrder: int64(index)}
		if lane == "composer_dictation" {
			combo := index % (len(platforms) * len(surfaces))
			clip.Platform = platforms[combo/len(surfaces)]
			clip.ComposerSurface = surfaces[combo%len(surfaces)]
			clip.TargetDevice = true
		}
		manifest.Clips = append(manifest.Clips, clip)
	}
	manifest.Approval.SourceArtifactSetSHA256 = CorpusSourceArtifactSetDigest(manifest.Clips)
	return manifest
}

func validPilotPacket(fixture signerFixture, registryRaw []byte) PilotPacket {
	packet := PilotPacket{SchemaVersion: PilotPacketSchema, PacketID: "io-packet-001", EvidenceClass: "authorized_real_input_human_review", Candidate: validCandidate(), Approval: validApproval(fixture, registryRaw), ReviewerRoster: []EligiblePilotReviewer{
		{ReviewerID: "reviewer-one", ReviewerKeyID: "pilot-reviewer-key-001", ReviewerPublicKeyFingerprintSHA256: PublicKeyFingerprint(fixture.pilotReviewerOnePublic), EligibilityReceiptDigest: hash("reviewer-one-eligibility")},
		{ReviewerID: "reviewer-two", ReviewerKeyID: "pilot-reviewer-key-002", ReviewerPublicKeyFingerprintSHA256: PublicKeyFingerprint(fixture.pilotReviewerTwoPublic), EligibilityReceiptDigest: hash("reviewer-two-eligibility")},
	}}
	for index := 0; index < 10; index++ {
		disposition := "accepted"
		if index >= 8 {
			disposition = []string{"rejected", "blocked"}[index-8]
		}
		pilot := IOPilot{
			PilotID: id("pilot", index), InputDigest: hash(fmt.Sprintf("input-%d", index)), RunReceiptDigest: hash(fmt.Sprintf("run-%d", index)), ArtifactDigest: hash(fmt.Sprintf("artifact-%d", index)),
			Disposition: disposition, DispositionReasonDigest: hash(fmt.Sprintf("disposition-%d", index)), TerminalVisibilityReceiptDigest: hash(fmt.Sprintf("terminal-visibility-%d", index)), RevisionCount: index % 3,
			AssertedClaimCount: 10, SourcedAssertedClaimCount: 10, ExternalEffectAuditReceiptDigest: hash(fmt.Sprintf("external-effect-audit-%d", index)),
			Reviews: []PilotReviewDecision{
				{ReviewerID: "reviewer-one", ReviewerKeyID: "pilot-reviewer-key-001", ReviewerPublicKeyHex: hex.EncodeToString(fixture.pilotReviewerOnePublic), ReviewReceiptDigest: hash(fmt.Sprintf("review-one-%d", index)), Disposition: disposition},
				{ReviewerID: "reviewer-two", ReviewerKeyID: "pilot-reviewer-key-002", ReviewerPublicKeyHex: hex.EncodeToString(fixture.pilotReviewerTwoPublic), ReviewReceiptDigest: hash(fmt.Sprintf("review-two-%d", index)), Disposition: disposition},
			},
		}
		for reviewIndex := range pilot.Reviews {
			payload, err := pilotReviewSigningPayload(packet, pilot, packet.ReviewerRoster[reviewIndex], pilot.Reviews[reviewIndex])
			if err != nil {
				panic(err)
			}
			privateKey := fixture.pilotReviewerOnePrivate
			if reviewIndex == 1 {
				privateKey = fixture.pilotReviewerTwoPrivate
			}
			pilot.Reviews[reviewIndex].SignatureHex = hex.EncodeToString(ed25519.Sign(privateKey, payload))
		}
		packet.Pilots = append(packet.Pilots, pilot)
	}
	packet.Approval.SourceArtifactSetSHA256 = PilotSourceArtifactSetDigest(packet)
	return packet
}

func validExternalMatrix(category string, registry TargetRegistry, fixture signerFixture, registryRaw []byte) ExternalMatrix {
	matrix := ExternalMatrix{SchemaVersion: ExternalMatrixSchema, MatrixID: category + "-matrix-001", Category: category, EvidenceClass: "external_observation_independently_reviewed", Candidate: registry.Candidate, Approval: validApproval(fixture, registryRaw)}
	for _, target := range registry.Targets {
		if target.Category != category {
			continue
		}
		for artifact := 0; artifact < target.MinimumArtifacts; artifact++ {
			metrics := make(map[string]Metric, len(target.RequiredMetrics))
			for _, threshold := range target.RequiredMetrics {
				metrics[threshold.Name] = passingMetric(threshold, target.MinimumSampleSize)
			}
			matrix.Observations = append(matrix.Observations, ExternalObservation{
				TargetID: target.ID, ArtifactSHA256: hash(fmt.Sprintf("%s-artifact-%d", target.ID, artifact)), FixtureSHA256: target.FixtureSHA256,
				Environment: target.Environment, SampleSize: target.MinimumSampleSize, MeasurementRevisionSHA256: target.MeasurementRevisionSHA256,
				ObservedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), Verdict: "pass", Metrics: metrics,
			})
		}
	}
	matrix.Approval.SourceArtifactSetSHA256 = ExternalSourceArtifactSetDigest(matrix.Observations)
	return matrix
}

func passingMetric(threshold MetricThreshold, sampleSize int) Metric {
	value := threshold.Value
	if threshold.Comparator == "at_most" && threshold.Value > 0 {
		value = threshold.Value / 2
	}
	metric := Metric{Value: value, Unit: threshold.Unit}
	if isRateThreshold(threshold) {
		denominator := sampleSize
		if threshold.Comparator == "at_least" {
			denominator = maxInt(100_000, sampleSize)
		}
		numerator := denominator
		if threshold.Comparator == "exactly" && threshold.Value == 0 {
			numerator = 0
		}
		metric.Numerator, metric.Denominator = &numerator, &denominator
		metric.Value = 100 * float64(numerator) / float64(denominator)
		low, high := wilsonInterval95(numerator, denominator)
		metric.Interval95 = &StatisticalInterval{Low: low, High: high, Method: "wilson_95"}
		return metric
	}
	if isLatencyThreshold(threshold) {
		metric.Samples = make([]float64, sampleSize)
		for index := range metric.Samples {
			metric.Samples[index] = value
		}
		p50, p95, p99 := value, value, value
		metric.P50, metric.P95, metric.P99 = &p50, &p95, &p99
		quantile := .95
		if strings.Contains(threshold.Name, "p50") {
			quantile = .50
		} else if strings.Contains(threshold.Name, "p99") {
			quantile = .99
		}
		low, high := deterministicBootstrapQuantile95(metric.Samples, quantile, threshold.Name)
		metric.Interval95 = &StatisticalInterval{Low: low, High: high, Method: "deterministic_bootstrap_95"}
	}
	return metric
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func cloneMetrics(value map[string]Metric) map[string]Metric {
	result := map[string]Metric{}
	for key, metric := range value {
		metric.Samples = append([]float64(nil), metric.Samples...)
		if metric.Numerator != nil {
			copyValue := *metric.Numerator
			metric.Numerator = &copyValue
		}
		if metric.Denominator != nil {
			copyValue := *metric.Denominator
			metric.Denominator = &copyValue
		}
		if metric.P50 != nil {
			copyValue := *metric.P50
			metric.P50 = &copyValue
		}
		if metric.P95 != nil {
			copyValue := *metric.P95
			metric.P95 = &copyValue
		}
		if metric.P99 != nil {
			copyValue := *metric.P99
			metric.P99 = &copyValue
		}
		if metric.Interval95 != nil {
			copyValue := *metric.Interval95
			metric.Interval95 = &copyValue
		}
		result[key] = metric
	}
	return result
}

func cloneTargetRegistry(t *testing.T, value TargetRegistry) TargetRegistry {
	t.Helper()
	raw := mustJSON(t, value)
	cloned, err := DecodeStrict[TargetRegistry](raw)
	if err != nil {
		t.Fatal(err)
	}
	return cloned
}

func clonePilotPacket(t *testing.T, value PilotPacket) PilotPacket {
	t.Helper()
	raw := mustJSON(t, value)
	cloned, err := DecodeStrict[PilotPacket](raw)
	if err != nil {
		t.Fatal(err)
	}
	return cloned
}

func cloneExternalMatrix(t *testing.T, value ExternalMatrix) ExternalMatrix {
	t.Helper()
	raw := mustJSON(t, value)
	cloned, err := DecodeStrict[ExternalMatrix](raw)
	if err != nil {
		t.Fatal(err)
	}
	return cloned
}

func assertRegistryRejected(t *testing.T, registry TargetRegistry) {
	t.Helper()
	if err := ValidateTargetRegistry(registry); err == nil {
		t.Fatal("weakened required target contract was accepted")
	}
}

func assertPilotReceiptRejected(t *testing.T, packet PilotPacket, registryRaw []byte, fixture signerFixture) {
	t.Helper()
	raw := mustJSON(t, packet)
	_, receipt, err := VerifyPilotReceipt(
		raw,
		registryRaw,
		ed25519.Sign(fixture.registryPrivate, registryRaw),
		fixture.registryPublic,
		ed25519.Sign(fixture.operatorPrivate, raw),
		fixture.operatorPublic,
		ed25519.Sign(fixture.reviewerPrivate, raw),
		fixture.reviewerPublic,
		fixture.approved,
	)
	if err == nil {
		t.Fatal("tampered pilot packet minted a receipt")
	}
	if _, encodeErr := EncodeReceipt(receipt); encodeErr == nil {
		t.Fatal("failed pilot verification returned an encodable receipt")
	}
}

func assertMatrixReceiptRejected(t *testing.T, matrix ExternalMatrix, registryRaw []byte, fixture signerFixture) {
	t.Helper()
	raw := mustJSON(t, matrix)
	_, receipt, err := VerifyMatrixReceipt(
		raw,
		registryRaw,
		ed25519.Sign(fixture.registryPrivate, registryRaw),
		fixture.registryPublic,
		ed25519.Sign(fixture.operatorPrivate, raw),
		fixture.operatorPublic,
		ed25519.Sign(fixture.reviewerPrivate, raw),
		fixture.reviewerPublic,
		fixture.approved,
	)
	if err == nil {
		t.Fatal("tampered external matrix minted a receipt")
	}
	if _, encodeErr := EncodeReceipt(receipt); encodeErr == nil {
		t.Fatal("failed matrix verification returned an encodable receipt")
	}
}

func matrixObservation(matrix *ExternalMatrix, targetID string) *ExternalObservation {
	for index := range matrix.Observations {
		if matrix.Observations[index].TargetID == targetID {
			return &matrix.Observations[index]
		}
	}
	panic("missing matrix observation " + targetID)
}

func matrixMetric(matrix *ExternalMatrix, targetID, metricName string) Metric {
	metric, ok := matrixObservation(matrix, targetID).Metrics[metricName]
	if !ok {
		panic("missing matrix metric " + targetID + "/" + metricName)
	}
	return metric
}

func setMatrixMetric(matrix *ExternalMatrix, targetID, metricName string, metric Metric) {
	matrixObservation(matrix, targetID).Metrics[metricName] = metric
}

func validCandidate() CandidateBinding {
	return CandidateBinding{ReleaseCommit: strings.Repeat("a", 40), GitTreeDigest: strings.Repeat("b", 64), ImageDigest: strings.Repeat("c", 64), ConfigDigest: strings.Repeat("d", 64), RouteMapDigest: strings.Repeat("e", 64)}
}
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalizeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
func id(prefix string, index int) string { return fmt.Sprintf("%s-%06d", prefix, index) }
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
