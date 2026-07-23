package mediasoak

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testReleaseCommit = "0123456789abcdef0123456789abcdef01234567"

type staticProbe struct {
	observation SignedObservation
	err         error
}

func (probe staticProbe) Observe(_ context.Context, _ Manifest, _ string) (SignedObservation, error) {
	return probe.observation, probe.err
}

type blockingProbe struct{}

func (blockingProbe) Observe(ctx context.Context, _ Manifest, _ string) (SignedObservation, error) {
	<-ctx.Done()
	return SignedObservation{}, ctx.Err()
}

type testRig struct {
	runner           Runner
	releasePublic    ed25519.PublicKey
	releasePrivate   ed25519.PrivateKey
	collectorPublic  ed25519.PublicKey
	collectorPrivate ed25519.PrivateKey
}

func TestCheckedManifestPinsApprovedW2AGate(t *testing.T) {
	manifest, _, err := LoadManifest(filepath.Join("..", "..", "testdata", "w2a", "media-soak.json"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	thresholds := manifest.Thresholds
	if manifest.Backend != "pion-room-actor" || manifest.FeatureFlag != "BONFIRE_MEDIA_BACKEND" || manifest.FeatureFlagValue != "pion" {
		t.Fatalf("backend binding drifted: %+v", manifest)
	}
	if len(manifest.TrustedSignerKeys) != 2 || !strings.Contains(strings.Join(manifest.Collector.Command, " "), "live-media-soak-probe.mjs") {
		t.Fatalf("collector custody drifted: %+v", manifest.Collector)
	}
	if thresholds.MinimumDurationSeconds != 7200 || thresholds.MinimumConcurrentRoomSeconds != 7200 || thresholds.MinimumRoomCount != 2 ||
		thresholds.MinimumPublishersPerRoom != 3 || thresholds.MinimumSubscribersPerRoom != 3 || thresholds.MinimumParticipantMinutes != 720 ||
		thresholds.MinimumBlockedOfferSeconds != 10 || thresholds.MaximumAdmissionP95Seconds != 2 || thresholds.MaximumRenegotiationP95Seconds != 3 ||
		thresholds.MaximumPacketLossPercent != 2 || thresholds.MaximumUnexpectedDisconnectPercent != 1 ||
		thresholds.MaximumSustainedCPUPercent != 80 || thresholds.MaximumRSSContainerLimitPercent != 75 {
		t.Fatalf("approved threshold drift: %+v", thresholds)
	}
}

func TestCollectorProbeDoesNotPutRoomPasswordInChildArgv(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "w2a", "live-media-soak-probe.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "'--password'") || strings.Contains(string(raw), `\"--password\"`) {
		t.Fatal("collector must pass the room password only through the child environment, never process argv")
	}
	for _, secret := range []string{"BONFIRE_MEDIA_SOAK_COLLECTOR_PRIVATE_KEY_FILE", "BONFIRE_MEDIA_SOAK_RELEASE_PRIVATE_KEY_FILE", "BONFIRE_MEDIA_SOAK_PASSWORD", "BONFIRE_MEDIA_SOAK_OBSERVER_TOKEN"} {
		if !strings.Contains(string(raw), "delete environment[secret]") || !strings.Contains(string(raw), `"`+secret+`"`) && !strings.Contains(string(raw), `'`+secret+`'`) {
			t.Fatalf("collector subprocess environment does not explicitly strip %s", secret)
		}
	}
}

func TestCollectorUsesFixedCheckedObserversAndSameCommitExecutableInputs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "w2a", "live-media-soak-probe.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, forbidden := range []string{
		"BONFIRE_MEDIA_SOAK_RESOURCE_COMMAND", "BONFIRE_MEDIA_SOAK_HOST_ATTEST_COMMAND", "BONFIRE_MEDIA_SOAK_HOL_COMMAND",
		"BONFIRE_MEDIA_SOAK_AI_FAILURE_COMMAND", "BONFIRE_MEDIA_SOAK_CANARY_COMMAND", "runJSONHook", "'/bin/sh'", "'-lc'",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("caller-controlled verdict hook remains: %s", forbidden)
		}
	}
	for _, required := range []string{
		"/internal/media-soak/${kind}", "bonfire.w2a.runtime-observation-request.v1", "bonfire.w2a.runtime-observation.v1",
		"verifyGitIdentity(releaseCommit)", "git', ['rev-parse', 'HEAD']", "git', ['status', '--porcelain', '--untracked-files=all'", "git', ['hash-object'",
		"testdata/w2a/live-media-soak-probe.mjs", "scripts/live-media-smoke.mjs", "cmd/media-soak/main.go",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("checked observer/executable binding missing %q", required)
		}
	}
}

func TestModifiedCollectorScriptCannotQualifyCheckout(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Dockerfile":                             "FROM scratch\n",
		"go.mod":                                 "module example.invalid/media-soak-test\n",
		"go.sum":                                 "",
		"deploy/digitalocean/docker-compose.yml": "services: {}\n",
		"deploy/digitalocean/Caddyfile":          "example.invalid {}\n",
		"testdata/w2a/live-media-soak-probe.mjs": "console.log('collector')\n",
		"scripts/live-media-smoke.mjs":           "console.log('browser')\n",
		"cmd/media-soak/main.go":                 "package main\n",
		"internal/mediasoak/gate.go":             "package mediasoak\n",
	}
	for path, body := range files {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit := func(arguments ...string) string {
		t.Helper()
		command := exec.Command("git", arguments...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	runGit("init", "-q")
	runGit("add", ".")
	runGit("-c", "user.name=Media Soak Test", "-c", "user.email=media-soak@example.invalid", "commit", "-qm", "fixture")
	commit := runGit("rev-parse", "HEAD")
	executable, err := verifyCollectorCheckout(context.Background(), root, commit)
	if err != nil {
		t.Fatalf("clean same-commit checkout rejected: %v", err)
	}
	exported, cleanup, err := exportCollectorSource(context.Background(), root, commit, executable)
	if err != nil {
		t.Fatalf("immutable export: %v", err)
	}
	if err := os.WriteFile(filepath.Join(exported, "scripts", "live-media-smoke.mjs"), []byte("tampered\n"), 0o600); err == nil {
		cleanup()
		t.Fatal("read-only collector export allowed a tracked input rewrite")
	}
	var source collectorSourceManifest
	rawSource, readErr := os.ReadFile(filepath.Join(exported, ".bonfire-release-source.json"))
	if readErr != nil || json.Unmarshal(rawSource, &source) != nil || source.Schema != "bonfire.w2a.immutable-source-export.v1" || source.Executable != executable || len(source.Inputs) != executable.InputCount {
		cleanup()
		t.Fatalf("immutable export manifest mismatch: read=%v manifest=%+v", readErr, source)
	}
	cleanup()
	collectorPath := filepath.Join(root, "testdata", "w2a", "live-media-soak-probe.mjs")
	if err := os.WriteFile(collectorPath, []byte("console.log('modified')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCollectorCheckout(context.Background(), root, commit); err == nil {
		t.Fatal("modified collector script was accepted")
	}
}

func TestSignedRawFixtureIsAlwaysNonQualifying(t *testing.T) {
	rig := newTestRig(t)
	signed := passingSignedObservation(t, rig, EvidenceModeFixture)
	receipt, outputPath, err := rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: signed})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if receipt.Verdict != VerdictNonQualifying || receipt.ReleaseQualified || !receipt.StopTriggered || receipt.EvidenceMode != EvidenceModeFixture {
		t.Fatalf("fixture was treated as release evidence: %+v", receipt)
	}
	if !strings.Contains(outputPath, "artifacts/w2-gates/non-qualifying/") {
		t.Fatalf("fixture receipt path = %q", outputPath)
	}
	for _, result := range receipt.Thresholds {
		if !result.Passed {
			t.Fatalf("passing fixture failed %s: %+v", result.ID, result)
		}
	}
	raw, err := os.ReadFile(filepath.Join(rig.runner.Root, outputPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"body":`, `"text":`, `"prompt":`, `"transcript":`, `"participantName":`, `"roomId":`, `"sittingId":`} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("body-free receipt contains %s", forbidden)
		}
	}
	verified, err := rig.runner.VerifyReceipt(outputPath, testReleaseCommit, rig.releasePublic)
	if err != nil || verified.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("VerifyReceipt: %v", err)
	}
}

func TestPinnedTwoPartyLiveEvidenceCanQualify(t *testing.T) {
	rig := newTestRig(t)
	signed := passingSignedObservation(t, rig, EvidenceModeLive)
	receipt, _, err := rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: signed})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.ReleaseQualified || receipt.Verdict != VerdictPass || receipt.StopTriggered {
		t.Fatalf("valid live evidence did not qualify: %+v", receipt)
	}
}

func TestCommandProbeRunsCollectorThenOperatorSignsRecomputedAggregate(t *testing.T) {
	rig := newTestRig(t)
	raw := passingRawEvidence(t, rig, EvidenceModeLive)
	evidence, err := SignRawEvidence(raw, "bonfire-media-collector", rig.collectorPrivate)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	probe := CommandProbe{
		Root: rig.runner.Root, ReleaseKeyID: "bonfire-release-operator", ReleasePrivateKey: rig.releasePrivate,
		verifyCheckout: func(context.Context, string, string) (ExecutableAttestation, error) { return raw.Executable, nil },
		runCollector: func(_ context.Context, _ string, _ []string, environment []string, _ string) error {
			for _, entry := range environment {
				if strings.HasPrefix(entry, "BONFIRE_MEDIA_SOAK_SIGNED_EVIDENCE_PATH=") {
					return os.WriteFile(strings.TrimPrefix(entry, "BONFIRE_MEDIA_SOAK_SIGNED_EVIDENCE_PATH="), encoded, 0o600)
				}
			}
			return errors.New("collector output path missing")
		},
	}
	receipt, _, err := rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, probe)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.ReleaseQualified {
		t.Fatalf("command probe did not qualify valid live evidence: %+v", receipt)
	}
	for _, entry := range collectorEnvironment(
		[]string{"BONFIRE_MEDIA_SOAK_RELEASE_PRIVATE_KEY_FILE=/secret", "SAFE=value"}, nil, testReleaseCommit, "/tmp/output",
	) {
		if strings.Contains(entry, "RELEASE_PRIVATE") || strings.Contains(entry, "/secret") {
			t.Fatalf("release private-key custody leaked to collector: %q", entry)
		}
	}
}

func TestForgedOperatorOrCollectorKeyIsRefused(t *testing.T) {
	t.Run("operator", func(t *testing.T) {
		rig := newTestRig(t)
		signed := passingSignedObservation(t, rig, EvidenceModeLive)
		_, wrongPrivate, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		forged, err := SignObservationWithEvidence(signed.Payload, signed.Evidence, signed.Signature.KeyID, wrongPrivate)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: forged})
		if !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("forged operator key got %v", err)
		}
	})
	t.Run("collector", func(t *testing.T) {
		rig := newTestRig(t)
		signed := passingSignedObservation(t, rig, EvidenceModeLive)
		_, wrongPrivate, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		signed.Evidence, err = SignRawEvidence(signed.Evidence.Payload, signed.Evidence.Signature.KeyID, wrongPrivate)
		if err != nil {
			t.Fatal(err)
		}
		signed, err = SignObservationWithEvidence(signed.Payload, signed.Evidence, signed.Signature.KeyID, rig.releasePrivate)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: signed})
		if !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("forged collector key got %v", err)
		}
	})
}

func TestImpossibleAggregateAndDummyDurationAreRefused(t *testing.T) {
	t.Run("aggregate", func(t *testing.T) {
		rig := newTestRig(t)
		signed := passingSignedObservation(t, rig, EvidenceModeLive)
		signed.Payload.MediaHealth.PacketLossPercent = 0
		var err error
		signed, err = SignObservationWithEvidence(signed.Payload, signed.Evidence, signed.Signature.KeyID, rig.releasePrivate)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: signed})
		if !errors.Is(err, ErrInvalidObservation) {
			t.Fatalf("impossible aggregate got %v", err)
		}
	})
	t.Run("dummy duration", func(t *testing.T) {
		rig := newTestRig(t)
		raw := passingRawEvidence(t, rig, EvidenceModeLive)
		raw.EndedAt = raw.EndedAt.Add(time.Hour)
		raw.Host.CapturedAt = raw.EndedAt
		_, err := RecomputeObservation(rig.runner.Manifest, raw)
		if !errors.Is(err, ErrInconclusive) {
			t.Fatalf("duration without sample coverage got %v", err)
		}
	})
}

func TestReplayOverwriteAndArtifactMutationAreRefused(t *testing.T) {
	t.Run("nonce replay", func(t *testing.T) {
		rig := newTestRig(t)
		signed := passingSignedObservation(t, rig, EvidenceModeFixture)
		if _, _, err := rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: signed}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: signed}); !errors.Is(err, ErrInvalidObservation) {
			t.Fatalf("replay got %v", err)
		}
	})
	t.Run("receipt no overwrite", func(t *testing.T) {
		rig := newTestRig(t)
		first := passingSignedObservation(t, rig, EvidenceModeFixture)
		if _, _, err := rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: first}); err != nil {
			t.Fatal(err)
		}
		second := passingSignedObservationWithSalt(t, rig, EvidenceModeFixture, "second")
		if _, _, err := rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: second}); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("overwrite got %v", err)
		}
	})
	t.Run("artifact mutation", func(t *testing.T) {
		rig := newTestRig(t)
		signed := passingSignedObservation(t, rig, EvidenceModeLive)
		path := filepath.Join(rig.runner.Root, filepath.FromSlash(signed.Evidence.Payload.Artifacts[0].Path))
		if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: signed})
		if !errors.Is(err, ErrInvalidObservation) {
			t.Fatalf("artifact mutation got %v", err)
		}
	})
	t.Run("artifact semantic divergence with valid digest", func(t *testing.T) {
		rig := newTestRig(t)
		raw := passingRawEvidence(t, rig, EvidenceModeLive)
		for index := range raw.Artifacts {
			if raw.Artifacts[index].Kind != "container_metrics" {
				continue
			}
			path := filepath.Join(rig.runner.Root, filepath.FromSlash(raw.Artifacts[index].Path))
			forged := append([]RawResourceSample(nil), raw.ResourceSamples...)
			forged[0].ProcessCPUPercent = 0
			body, err := json.MarshalIndent(forged, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			body = append(body, '\n')
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			raw.Artifacts[index].SHA256 = digest(body)
			raw.Artifacts[index].Bytes = int64(len(body))
		}
		evidence, err := SignRawEvidence(raw, "bonfire-media-collector", rig.collectorPrivate)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := RecomputeObservation(rig.runner.Manifest, raw)
		if err != nil {
			t.Fatal(err)
		}
		signed, err := SignObservationWithEvidence(observation, evidence, "bonfire-release-operator", rig.releasePrivate)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: signed})
		if !errors.Is(err, ErrInvalidObservation) {
			t.Fatalf("semantically divergent artifact got %v", err)
		}
	})
}

func TestRuntimeAndExecutableCustodyMismatchesAreRefused(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*RawEvidenceManifest)
	}{
		{"collector nonce", func(raw *RawEvidenceManifest) { raw.Host.CollectorNonce = digest([]byte("wrong nonce")) }},
		{"runtime tree", func(raw *RawEvidenceManifest) { raw.Host.GitTreeDigest = "not-a-digest" }},
		{"collector head", func(raw *RawEvidenceManifest) { raw.Executable.Head = strings.Repeat("f", 40) }},
		{"collector blob", func(raw *RawEvidenceManifest) { raw.Executable.CollectorGitBlob = "modified-script" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newTestRig(t)
			raw := passingRawEvidence(t, rig, EvidenceModeLive)
			test.mutate(&raw)
			if _, err := RecomputeObservation(rig.runner.Manifest, raw); !errors.Is(err, ErrInvalidObservation) {
				t.Fatalf("custody mismatch got %v", err)
			}
		})
	}
}

func TestCanaryCoverageMatrixIsFailClosed(t *testing.T) {
	rig := newTestRig(t)
	raw := passingRawEvidence(t, rig, EvidenceModeLive)
	raw.CanaryChecks = raw.CanaryChecks[1:]
	if _, err := RecomputeObservation(rig.runner.Manifest, raw); !errors.Is(err, ErrInconclusive) {
		t.Fatalf("missing surface/direction/sentinel coverage got %v", err)
	}
}

func TestCanarySentinelsChangeExactlyOneIsolationAxis(t *testing.T) {
	rig := newTestRig(t)
	raw := passingRawEvidence(t, rig, EvidenceModeLive)
	for index := range raw.CanaryChecks {
		if raw.CanaryChecks[index].Sentinel == "prior_sitting" {
			raw.CanaryChecks[index].ObservedGenerationDigest = digest([]byte("masked-sitting-leak"))
			if _, err := RecomputeObservation(rig.runner.Manifest, raw); !errors.Is(err, ErrInvalidObservation) {
				t.Fatalf("multi-axis sentinel got %v", err)
			}
			return
		}
	}
	t.Fatal("fixture omitted prior-sitting sentinel")
}

func TestCanaryPublicationRecipientDigestMismatchFailsQualification(t *testing.T) {
	rig := newTestRig(t)
	raw := passingRawEvidence(t, rig, EvidenceModeLive)
	for index := range raw.CanaryChecks {
		if raw.CanaryChecks[index].Surface == "chat" {
			raw.CanaryChecks[index].DeletionRecipientSetDigest = digest([]byte("mismatched-delete-recipients"))
			if _, err := RecomputeObservation(rig.runner.Manifest, raw); !errors.Is(err, ErrInvalidObservation) {
				t.Fatalf("recipient-set mismatch got %v", err)
			}
			return
		}
	}
	t.Fatal("fixture omitted chat canary")
}

func TestCrossRoomCanaryReaderLeakFailsQualification(t *testing.T) {
	for _, surface := range []string{"track", "chat", "scout", "transcript", "recap", "artifact"} {
		t.Run(surface, func(t *testing.T) {
			rig := newTestRig(t)
			raw := passingRawEvidence(t, rig, EvidenceModeLive)
			leaked := false
			for index := range raw.CanaryChecks {
				check := &raw.CanaryChecks[index]
				if check.Surface == surface && check.Sentinel == "unrelated_room" {
					check.Observed = true
					leaked = true
					break
				}
			}
			if !leaked {
				t.Fatal("fixture omitted unrelated-room canary")
			}
			observation, err := RecomputeObservation(rig.runner.Manifest, raw)
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			for _, result := range evaluate(rig.runner.Manifest, observation) {
				if strings.HasSuffix(result.ID, "-leaks") && !result.Passed {
					failed = true
				}
			}
			if !failed {
				t.Fatal("cross-room reader leak passed qualification")
			}
		})
	}
}

func TestTransientBrowserAttemptsRejectFabricatedCorrelationAndRTP(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*RawLatencyAttempt)
	}{
		{"offer answer mismatch", func(attempt *RawLatencyAttempt) { attempt.AnswerDigest = digest([]byte("different answer")) }},
		{"non increasing RTP", func(attempt *RawLatencyAttempt) { attempt.RTPAfter = attempt.RTPBefore }},
		{"missing ontrack", func(attempt *RawLatencyAttempt) { attempt.OnTrackAt = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newTestRig(t)
			raw := passingRawEvidence(t, rig, EvidenceModeLive)
			test.mutate(&raw.HeadOfLine.AdmissionAttempts[0])
			if _, err := RecomputeObservation(rig.runner.Manifest, raw); !errors.Is(err, ErrInvalidObservation) {
				t.Fatalf("fabricated transient browser proof got %v", err)
			}
		})
	}
}

func TestBuildManifestRejectsDifferentValidEd25519Key(t *testing.T) {
	rig := newTestRig(t)
	wrongPublic, wrongPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"schema": "bonfire.w2a.build-manifest.v1", "releaseCommit": testReleaseCommit}
	canonical, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope := struct {
		Schema    string            `json:"schema"`
		Payload   map[string]any    `json:"payload"`
		Signature SignatureEnvelope `json:"signature"`
	}{Schema: "bonfire.w2a.signed-build-manifest.v1", Payload: payload,
		Signature: SignatureEnvelope{Algorithm: SignatureAlgorithmEd25519, KeyID: "bonfire-release-operator", Value: base64.StdEncoding.EncodeToString(ed25519.Sign(wrongPrivate, canonical))}}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyBuildManifestSignature(rig.runner.Manifest, wrongPublic, envelope.Signature, raw); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("different valid release key accepted: %v", err)
	}
	if err := verifyBuildManifestSignature(rig.runner.Manifest, rig.releasePublic, envelope.Signature, raw); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("wrong-key signature accepted by pinned release key: %v", err)
	}
}

func TestCanaryLifecycleAcknowledgementsAreRequired(t *testing.T) {
	for _, field := range []string{"ingress", "read", "scrub"} {
		t.Run(field, func(t *testing.T) {
			rig := newTestRig(t)
			raw := passingRawEvidence(t, rig, EvidenceModeLive)
			switch field {
			case "ingress":
				raw.CanaryChecks[0].IngressAcknowledged = false
			case "read":
				raw.CanaryChecks[0].ReadAcknowledged = false
			case "scrub":
				raw.CanaryChecks[0].ScrubAcknowledged = false
			}
			if _, err := RecomputeObservation(rig.runner.Manifest, raw); !errors.Is(err, ErrInconclusive) {
				t.Fatalf("missing %s acknowledgement got %v", field, err)
			}
		})
	}
}

func TestCustodyPathsRejectSymlinkedComponents(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "artifacts")); err != nil {
		t.Fatal(err)
	}
	if _, err := rootedPath(root, "artifacts/w2-gates/receipt.json"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked custody path was accepted: %v", err)
	}
}

func TestFreshnessAndUnknownFieldsFailClosed(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		rig := newTestRig(t)
		signed := passingSignedObservation(t, rig, EvidenceModeLive)
		rig.runner.Now = func() time.Time { return signed.Payload.ExpiresAt.Add(time.Second) }
		_, _, err := rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: signed})
		if !errors.Is(err, ErrInvalidObservation) {
			t.Fatalf("stale got %v", err)
		}
	})
	t.Run("body field", func(t *testing.T) {
		rig := newTestRig(t)
		signed := passingSignedObservation(t, rig, EvidenceModeFixture)
		raw, err := json.Marshal(signed)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw[:len(raw)-1], []byte(`,"body":"secret"}`)...)
		if _, err := DecodeSignedObservation(raw); !errors.Is(err, ErrInvalidObservation) {
			t.Fatalf("unknown field got %v", err)
		}
	})
}

func TestReceiptCannotBeRewrittenEvenWithRecomputedSelfDigest(t *testing.T) {
	rig := newTestRig(t)
	receipt, _, err := rig.runner.Run(context.Background(), testReleaseCommit, rig.releasePublic, staticProbe{observation: passingSignedObservation(t, rig, EvidenceModeFixture)})
	if err != nil {
		t.Fatal(err)
	}
	tampered := receipt
	tampered.Thresholds = append([]ThresholdResult(nil), receipt.Thresholds...)
	tampered.Thresholds[0].Actual = 1
	tampered.Thresholds[0].Passed = false
	tampered.ReceiptDigest, err = tampered.canonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if err := tampered.Validate(rig.runner.Manifest, rig.runner.ManifestDigest, testReleaseCommit, rig.releasePublic, rig.collectorPublic, rig.runner.Now()); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("rewritten threshold accepted: %v", err)
	}
}

func TestStrictLimitsAndManifestWeakening(t *testing.T) {
	rig := newTestRig(t)
	observation, err := RecomputeObservation(rig.runner.Manifest, passingRawEvidence(t, rig, EvidenceModeFixture))
	if err != nil {
		t.Fatal(err)
	}
	observation.HeadOfLine.RoomBAdmissionP95Seconds = 2
	assertThreshold(t, evaluate(rig.runner.Manifest, observation), "room-b-admission-p95", false)
	observation.MediaHealth.PacketLossPercent = 2
	assertThreshold(t, evaluate(rig.runner.Manifest, observation), "packet-loss-percent", false)

	manifest := rig.runner.Manifest
	manifest.Thresholds.MinimumDurationSeconds = 60
	if err := manifest.Validate(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("weakened manifest got %v", err)
	}
	manifest = rig.runner.Manifest
	manifest.TrustedSignerKeys[1].PublicKeySHA256 = manifest.TrustedSignerKeys[0].PublicKeySHA256
	if err := manifest.Validate(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("shared custody key got %v", err)
	}
}

func TestProbeTimeoutAndMissingReceiptBlock(t *testing.T) {
	rig := newTestRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := rig.runner.Run(ctx, testReleaseCommit, rig.releasePublic, blockingProbe{}); !errors.Is(err, ErrInconclusive) {
		t.Fatalf("timeout got %v", err)
	}
	if _, err := rig.runner.VerifyReceipt("artifacts/w2-gates/missing.json", testReleaseCommit, rig.releasePublic); !errors.Is(err, ErrInconclusive) {
		t.Fatalf("missing receipt got %v", err)
	}
}

func TestKeyParsersAcceptOnlyEd25519(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parsedPublic, err := ParsePublicKey([]byte(base64.StdEncoding.EncodeToString(publicKey)))
	if err != nil || !publicKey.Equal(parsedPublic) {
		t.Fatalf("public parser: %v", err)
	}
	parsedPrivate, err := ParsePrivateKey([]byte(base64.StdEncoding.EncodeToString(privateKey)))
	if err != nil || !privateKey.Equal(parsedPrivate) {
		t.Fatalf("private parser: %v", err)
	}
	if _, err := ParsePublicKey([]byte("not-a-key")); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("invalid public key got %v", err)
	}
	if _, err := ParsePrivateKey([]byte("not-a-key")); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("invalid private key got %v", err)
	}
}

func newTestRig(t *testing.T) testRig {
	t.Helper()
	manifest, _, err := LoadManifest(filepath.Join("..", "..", "testdata", "w2a", "media-soak.json"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	releasePublic, releasePrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	collectorPublic, collectorPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest.TrustedSignerKeys = []TrustedSigner{
		{Role: SignerRoleRelease, KeyID: "bonfire-release-operator", PublicKeySHA256: digest(releasePublic)},
		{Role: SignerRoleCollector, KeyID: "bonfire-media-collector", PublicKeySHA256: digest(collectorPublic)},
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{
		Root: t.TempDir(), Manifest: manifest, ManifestDigest: digest(manifestRaw), CollectorPublicKey: collectorPublic,
		Now: func() time.Time { return time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC) },
	}
	return testRig{runner: runner, releasePublic: releasePublic, releasePrivate: releasePrivate, collectorPublic: collectorPublic, collectorPrivate: collectorPrivate}
}

func passingSignedObservation(t *testing.T, rig testRig, mode string) SignedObservation {
	t.Helper()
	return passingSignedObservationWithSalt(t, rig, mode, "first")
}

func passingSignedObservationWithSalt(t *testing.T, rig testRig, mode, salt string) SignedObservation {
	t.Helper()
	raw := passingRawEvidence(t, rig, mode)
	raw.RunID = digest([]byte(t.Name() + ":run:" + salt))
	raw.Nonce = digest([]byte(t.Name() + ":nonce:" + salt))
	raw.Host.CollectorNonce = raw.Nonce
	writeRawEvidenceArtifacts(t, rig, &raw)
	evidence, err := SignRawEvidence(raw, "bonfire-media-collector", rig.collectorPrivate)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := RecomputeObservation(rig.runner.Manifest, raw)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignObservationWithEvidence(observation, evidence, "bonfire-release-operator", rig.releasePrivate)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func passingRawEvidence(t *testing.T, rig testRig, mode string) RawEvidenceManifest {
	t.Helper()
	ended := rig.runner.Now().Add(-time.Minute)
	started := ended.Add(-2 * time.Hour)
	roomA, roomB := digest([]byte("room-a")), digest([]byte("room-b"))
	sittingA, sittingB := digest([]byte("sitting-a")), digest([]byte("sitting-b"))
	generationA, generationB := digest([]byte("generation-a")), digest([]byte("generation-b"))
	raw := RawEvidenceManifest{
		Schema: RawEvidenceSchema, EvidenceMode: mode, Synthetic: mode == EvidenceModeFixture,
		RunID: digest([]byte(t.Name() + ":run")), Nonce: digest([]byte(t.Name() + ":nonce")), CollectorVersion: "test-collector-v2",
		ReleaseCommit: testReleaseCommit, Backend: rig.runner.Manifest.Backend, FeatureFlag: rig.runner.Manifest.FeatureFlag,
		FeatureFlagValue: rig.runner.Manifest.FeatureFlagValue, StartedAt: started, EndedAt: ended,
		Host: HostAttestation{HostDigest: digest([]byte("host")), ContainerDigest: digest([]byte("container")), ImageDigest: digest([]byte("image")),
			GitTreeDigest: digest([]byte("git-tree")), ConfigSHA256: digest([]byte("config")), TransitiveInputsSHA256: digest([]byte("inputs")), SourceArchiveSHA256: digest([]byte("archive")), CollectorNonce: digest([]byte(t.Name() + ":nonce")),
			ReleaseCommit: testReleaseCommit, Backend: rig.runner.Manifest.Backend, FeatureFlag: rig.runner.Manifest.FeatureFlag,
			FeatureFlagValue: rig.runner.Manifest.FeatureFlagValue, CapturedAt: ended},
		Executable: ExecutableAttestation{Head: testReleaseCommit, GitTreeObject: testReleaseCommit, GitTreeDigest: digest([]byte("git-tree")), SourceArchiveSHA256: digest([]byte("archive")), TransitiveInputsSHA256: digest([]byte("inputs")), ConfigSHA256: digest([]byte("config")), InputCount: 42, CollectorGitBlob: testReleaseCommit, BrowserGitBlob: testReleaseCommit, GateGitBlob: testReleaseCommit},
	}
	for index := 0; index <= 120; index++ {
		at := started.Add(time.Duration(index) * time.Minute)
		raw.RoomSamples = append(raw.RoomSamples,
			RawRoomSample{At: at, SourceDigest: sampleDigest("room-a", index), RoomDigest: roomA, SittingDigest: sittingA, MediaGenerationDigest: generationA, Publishers: 3, Subscribers: 3, Participants: 3},
			RawRoomSample{At: at, SourceDigest: sampleDigest("room-b", index), RoomDigest: roomB, SittingDigest: sittingB, MediaGenerationDigest: generationB, Publishers: 3, Subscribers: 3, Participants: 3},
		)
		raw.NetworkSamples = append(raw.NetworkSamples,
			RawNetworkSample{At: at, SourceDigest: sampleDigest("network-a", index), RoomDigest: roomA, PacketsExpected: uint64(index * 1000), PacketsLost: uint64(index * 10)},
			RawNetworkSample{At: at, SourceDigest: sampleDigest("network-b", index), RoomDigest: roomB, PacketsExpected: uint64(index * 1000), PacketsLost: uint64(index * 10)},
		)
		raw.ResourceSamples = append(raw.ResourceSamples, RawResourceSample{At: at, SourceDigest: sampleDigest("resource", index), ProcessCPUPercent: 70, RSSContainerLimitPercent: 65})
	}
	blockedAt := started.Add(time.Hour)
	raw.HeadOfLine = RawHeadOfLineEvidence{
		RoomADigest: roomA, RoomBDigest: roomB, BlockedAt: blockedAt, ReleasedAt: blockedAt.Add(10 * time.Second), SourceDigest: digest([]byte("hol-log")),
		AdmissionAttempts: []RawLatencyAttempt{
			latencyAttempt(blockedAt.Add(time.Second), time.Second, "admit-1"), latencyAttempt(blockedAt.Add(3*time.Second), 1200*time.Millisecond, "admit-2"), latencyAttempt(blockedAt.Add(6*time.Second), 1500*time.Millisecond, "admit-3"),
		},
		RenegotiationEvents: []RawLatencyAttempt{
			latencyAttempt(blockedAt.Add(time.Second), 2*time.Second, "reneg-1"), latencyAttempt(blockedAt.Add(4*time.Second), 2200*time.Millisecond, "reneg-2"), latencyAttempt(blockedAt.Add(7*time.Second), 2500*time.Millisecond, "reneg-3"),
		},
	}
	surfaces := []string{"track", "chat", "scout", "transcript", "recap", "artifact"}
	sentinels := []string{"current", "prior_sitting", "prior_generation", "unrelated_room"}
	sequence := 0
	for _, surface := range surfaces {
		for _, direction := range []string{"a_to_b", "b_to_a"} {
			for _, sentinel := range sentinels {
				sourceRoom, observedRoom, sourceSitting, observedSitting, sourceGeneration, observedGeneration := roomA, roomA, sittingA, sittingA, generationA, generationA
				unrelatedRoom := roomB
				if direction == "b_to_a" {
					sourceRoom, observedRoom, sourceSitting, observedSitting, sourceGeneration, observedGeneration = roomB, roomB, sittingB, sittingB, generationB, generationB
					unrelatedRoom = roomA
				}
				positive := sentinel == "current"
				if !positive {
					switch sentinel {
					case "prior_sitting":
						observedSitting = digest([]byte(direction + ":prior-sitting"))
					case "prior_generation":
						observedGeneration = digest([]byte(direction + ":prior-generation"))
					case "unrelated_room":
						observedRoom = unrelatedRoom
					}
				}
				check := RawCanaryCheck{At: started.Add(time.Duration(10+sequence%90) * time.Minute), SourceDigest: sampleDigest("canary", sequence), Surface: surface, Direction: direction, Sentinel: sentinel, SourceRoomDigest: sourceRoom, ObservedRoomDigest: observedRoom, SourceSittingDigest: sourceSitting, ObservedSittingDigest: observedSitting, SourceGenerationDigest: sourceGeneration, ObservedGenerationDigest: observedGeneration, ExpectedPresent: positive, Observed: positive, IngressAcknowledged: true, ReadAcknowledged: true, ScrubAcknowledged: true}
				if surface == "chat" || surface == "recap" || surface == "artifact" {
					check.PublicationRecipientSetDigest = digest([]byte(direction + ":recipients"))
					check.DeletionRecipientSetDigest = check.PublicationRecipientSetDigest
					check.PublicationRecipientCount, check.DeletionRecipientCount = 3, 3
				}
				raw.CanaryChecks = append(raw.CanaryChecks, check)
				sequence++
			}
		}
	}
	aiStarted := started.Add(90 * time.Minute)
	raw.AIFailures = []RawAIFailureEvidence{{InjectedAt: aiStarted, RecoveredAt: aiStarted.Add(time.Minute), SourceDigest: digest([]byte("ai-fault-log")), RoomBCompletions: []RawLatencyAttempt{
		latencyAttempt(aiStarted.Add(time.Second), time.Second, "ai-client-1"), latencyAttempt(aiStarted.Add(3*time.Second), time.Second, "ai-client-2"), latencyAttempt(aiStarted.Add(5*time.Second), time.Second, "ai-client-3"),
	}}}
	writeRawEvidenceArtifacts(t, rig, &raw)
	return raw
}

func writeRawEvidenceArtifacts(t *testing.T, rig testRig, raw *RawEvidenceManifest) {
	t.Helper()
	raw.Artifacts = nil
	roomIndexes := map[string]int{}
	browserRooms := make([][]map[string]any, 0, 2)
	for _, sample := range raw.RoomSamples {
		index, ok := roomIndexes[sample.RoomDigest]
		if !ok {
			index = len(browserRooms)
			roomIndexes[sample.RoomDigest] = index
			browserRooms = append(browserRooms, nil)
		}
		browserRooms[index] = append(browserRooms[index], map[string]any{
			"at": sample.At, "sourceDigest": sample.SourceDigest, "connectedClients": sample.Subscribers,
			"publishers": sample.Publishers, "participants": sample.Participants,
		})
	}
	buildManifest := struct {
		Schema  string `json:"schema"`
		Payload struct {
			Schema                 string    `json:"schema"`
			ReleaseCommit          string    `json:"releaseCommit"`
			GitTreeObject          string    `json:"gitTreeObject"`
			GitTreeDigest          string    `json:"gitTreeDigest"`
			SourceArchiveSHA256    string    `json:"sourceArchiveSha256"`
			TransitiveInputsSHA256 string    `json:"transitiveInputsSha256"`
			ConfigSHA256           string    `json:"configSha256"`
			InputCount             int       `json:"inputCount"`
			BinarySHA256           string    `json:"binarySha256"`
			OCIImageDigest         string    `json:"ociImageDigest"`
			ImageReference         string    `json:"imageReference"`
			CreatedAt              time.Time `json:"createdAt"`
		} `json:"payload"`
		Signature SignatureEnvelope `json:"signature"`
	}{Schema: "bonfire.w2a.signed-build-manifest.v1", Signature: SignatureEnvelope{Algorithm: SignatureAlgorithmEd25519, KeyID: "bonfire-release-operator", Value: "fixture"}}
	buildManifest.Payload.Schema = "bonfire.w2a.build-manifest.v1"
	buildManifest.Payload.ReleaseCommit, buildManifest.Payload.GitTreeObject = raw.ReleaseCommit, raw.Executable.GitTreeObject
	buildManifest.Payload.GitTreeDigest, buildManifest.Payload.SourceArchiveSHA256 = raw.Executable.GitTreeDigest, raw.Executable.SourceArchiveSHA256
	buildManifest.Payload.TransitiveInputsSHA256, buildManifest.Payload.ConfigSHA256, buildManifest.Payload.InputCount = raw.Executable.TransitiveInputsSHA256, raw.Executable.ConfigSHA256, raw.Executable.InputCount
	buildManifest.Payload.BinarySHA256, buildManifest.Payload.OCIImageDigest = raw.Host.ContainerDigest, raw.Host.ImageDigest
	buildManifest.Payload.ImageReference, buildManifest.Payload.CreatedAt = "example.invalid/bonfire@sha256:"+raw.Host.ImageDigest, raw.EndedAt
	payloadBody, err := json.Marshal(buildManifest.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var canonicalPayload map[string]any
	if err := json.Unmarshal(payloadBody, &canonicalPayload); err != nil {
		t.Fatal(err)
	}
	canonicalBody, err := json.Marshal(canonicalPayload)
	if err != nil {
		t.Fatal(err)
	}
	buildManifest.Signature.Value = base64.StdEncoding.EncodeToString(ed25519.Sign(rig.releasePrivate, canonicalBody))
	buildBody, err := json.MarshalIndent(buildManifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	buildBody = append(buildBody, '\n')
	raw.Host.BuildManifestSHA256 = digest(buildBody)
	values := []struct {
		kind  string
		value any
	}{
		{"browser_events", struct {
			RoomSamples []RawRoomSample    `json:"roomSamples"`
			Rooms       [][]map[string]any `json:"rooms"`
		}{raw.RoomSamples, browserRooms}},
		{"webrtc_stats", raw.NetworkSamples},
		{"container_metrics", raw.ResourceSamples},
		{"fault_log", struct {
			HeadOfLine   RawHeadOfLineEvidence `json:"headOfLine"`
			AIFailure    RawAIFailureEvidence  `json:"aiFailure"`
			CanaryChecks []RawCanaryCheck      `json:"canaryChecks"`
		}{raw.HeadOfLine, raw.AIFailures[0], raw.CanaryChecks}},
		{"config_attestation", raw.Host},
		{"probe_log", struct {
			CollectorVersion      string                `json:"collectorVersion"`
			ReleaseCommit         string                `json:"releaseCommit"`
			Executable            ExecutableAttestation `json:"executable"`
			RoomCount             int                   `json:"roomCount"`
			ClientsPerRoom        int                   `json:"clientsPerRoom"`
			BrowserProbeExitCodes []int                 `json:"browserProbeExitCodes"`
			DurationMS            int64                 `json:"durationMs"`
		}{raw.CollectorVersion, raw.ReleaseCommit, raw.Executable, 2, 3, []int{0, 0}, int64(raw.EndedAt.Sub(raw.StartedAt) / time.Millisecond)}},
		{"probe_image", struct {
			ImageDigest     string `json:"imageDigest"`
			ContainerDigest string `json:"containerDigest"`
		}{raw.Host.ImageDigest, raw.Host.ContainerDigest}},
		{"build_manifest", buildManifest},
	}
	for _, item := range values {
		content, err := json.MarshalIndent(item.value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		content = append(content, '\n')
		path := filepath.ToSlash(filepath.Join("artifacts", "w2-gates", "probe", testReleaseCommit, "raw", item.kind+".json"))
		absolute := filepath.Join(rig.runner.Root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, content, 0o600); err != nil {
			t.Fatal(err)
		}
		raw.Artifacts = append(raw.Artifacts, RawEvidenceArtifact{Kind: item.kind, Path: path, SHA256: digest(content), Bytes: int64(len(content))})
	}
}

func latencyAttempt(started time.Time, duration time.Duration, source string) RawLatencyAttempt {
	correlation := digest([]byte(source + ":offer-answer-correlation"))
	return RawLatencyAttempt{StartedAt: started, OnTrackAt: started.Add(duration / 2), CompletedAt: started.Add(duration), Succeeded: true,
		SourceDigest: digest([]byte(source)), ClientDigest: digest([]byte(source + ":client")), OfferDigest: correlation,
		AnswerDigest: correlation, RTPBefore: 10, RTPAfter: 20}
}

func sampleDigest(kind string, index int) string {
	return digest([]byte(kind + ":" + time.Duration(index).String()))
}

func assertThreshold(t *testing.T, results []ThresholdResult, id string, want bool) {
	t.Helper()
	for _, result := range results {
		if result.ID == id {
			if result.Passed != want {
				t.Fatalf("threshold %s passed=%v, want %v", id, result.Passed, want)
			}
			return
		}
	}
	t.Fatalf("threshold %s not found", id)
}
