package e10probe

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type scoutQualificationKeys struct {
	root, provider, deviceIOS, deviceWeb, billing, receipt, operator, reviewer ed25519.PrivateKey
}

func TestTrustedScoutQualificationPassesAnchoredMinimumsAndWritesRedactedReceipt(t *testing.T) {
	corpus, config, observations, authority, _ := scoutTrustedQualificationFixture(t)
	receipt, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Pass || receipt.EvidenceClass != "provider_live_evidence_candidate" || receipt.QualificationState != "trusted_authority_bound_deterministic_evaluation" || receipt.OrdinaryCount != ScoutOrdinaryMinimum || receipt.ExplicitCount != ScoutExplicitMinimum || receipt.AudienceNegativeCount != ScoutAudienceNegativeMinimum || receipt.AcceptedTerminalCount != len(observations) || receipt.RejectedTerminalCount != 0 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if len(receipt.LaneResults) != 2 || receipt.LaneResults[0].Lane != ScoutLanePersonal || receipt.LaneResults[0].Surface != ScoutSurfacePrivateVoice || !receipt.LaneResults[0].Pass || receipt.LaneResults[1].Lane != ScoutLaneMeeting || receipt.LaneResults[1].Surface != ScoutSurfaceMeetingVoice || !receipt.LaneResults[1].Pass {
		t.Fatalf("lanes were not independently qualified: %+v", receipt.LaneResults)
	}
	if receipt.FirstUsefulAudioLatency.P95.PointMS != 500 || receipt.BargeInStopLatency.P95.PointMS != 200 || receipt.FirstUsefulAudioLatency.Method != "deterministic_percentile_bootstrap_95" || receipt.FirstUsefulAudioLatency.Replicates != scoutBootstrapReplicates {
		t.Fatalf("latency evidence incomplete: %+v", receipt)
	}
	if receipt.Usage.TotalTokens != int64(len(observations))*3 || receipt.Usage.CostNanoUSD != int64(len(observations))*3000 || receipt.FalseResponseRate95.Numerator != 0 || receipt.FalseResponseRate95.Denominator != ScoutOrdinaryMinimum || receipt.ExplicitInvocationRecall95.Numerator != ScoutExplicitMinimum || receipt.ExplicitInvocationRecall95.Denominator != ScoutExplicitMinimum {
		t.Fatalf("rate or usage evidence incomplete: %+v", receipt)
	}
	if !validDigest(receipt.TrustRegistrySHA256) || !validDigest(receipt.RouteIdentitySHA256) || !validDigest(receipt.PricingRevisionSHA256) || !validDigest(receipt.ProviderIdentitySHA256) || !validDigest(receipt.BillingReconciliationSHA256) || !validDigest(receipt.RawEvidenceSetSHA256) || !validDigest(receipt.ReceiptSHA256) {
		t.Fatalf("immutable bindings missing: %+v", receipt)
	}
	verifierAuthority := authority
	verifierAuthority.ReceiptSignerPrivateKey = nil
	if err := VerifyTrustedScoutQualificationReceipt(receipt, verifierAuthority, time.Date(2026, 8, 1, 20, 1, 0, 0, time.UTC)); err != nil {
		t.Fatalf("independent receipt verification: %v", err)
	}
	if err := ConsumeTrustedScoutQualificationReceipt(receipt, verifierAuthority, time.Date(2026, 8, 1, 20, 1, 0, 0, time.UTC)); err != nil {
		t.Fatalf("downstream receipt consumption: %v", err)
	}
	if err := ConsumeTrustedScoutQualificationReceipt(receipt, verifierAuthority, time.Date(2026, 8, 1, 20, 1, 1, 0, time.UTC)); !errors.Is(err, ErrScoutReceiptReused) {
		t.Fatalf("receipt was reusable downstream: %v", err)
	}
	path := filepath.Join(authority.AttemptLedgerDirectory, "scout-qualification.json")
	if err := WriteTrustedScoutQualificationReceipt(path, receipt, verifierAuthority, time.Date(2026, 8, 1, 20, 1, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytesContainAny(data, observations[0].RawEvidence[0].Body, observations[0].RawEvidence[1].Body) {
		t.Fatal("receipt retained raw private evidence")
	}
	if bytesContainAny(data, []byte(config.ProviderIdentity.ProjectID), []byte(config.ProviderIdentity.OrganizationID), []byte(config.BillingReconciliation.BillingAccountID)) {
		t.Fatal("receipt retained raw provider or billing identity")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt permission=%v err=%v", info.Mode().Perm(), err)
	}
	if err := WriteTrustedScoutQualificationReceipt(path, receipt, verifierAuthority, time.Date(2026, 8, 1, 20, 1, 0, 0, time.UTC)); err == nil {
		t.Fatal("receipt path was overwritten")
	}
	entries, err := os.ReadDir(authority.AttemptLedgerDirectory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary publication leaked: %s", entry.Name())
		}
	}
}

func TestLocalScoutEvaluationCanNeverEarnProviderLiveCandidate(t *testing.T) {
	corpus, config, observations, _, _ := scoutTrustedQualificationFixture(t)
	receipt, err := EvaluateScoutQualification(corpus, config, observations, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Pass || receipt.EvidenceClass != "local_untrusted_evaluation" || receipt.QualificationState != "local_deterministic_evaluation_only" || receipt.TrustRegistrySHA256 != "" || receipt.AttemptLedgerID != "" {
		t.Fatalf("local JSON crossed trust boundary: %+v", receipt)
	}
}

func TestTrustedScoutQualificationThresholdsAndAllAudienceSurfaces(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, []ScoutQualificationObservation, ScoutQualificationCorpus, ScoutQualificationConfig, ScoutQualificationAuthority, scoutQualificationKeys){
		"false response rate": func(t *testing.T, observations []ScoutQualificationObservation, corpus ScoutQualificationCorpus, config ScoutQualificationConfig, authority ScoutQualificationAuthority, keys scoutQualificationKeys) {
			for i := 0; i < 11; i++ {
				scoutSetPublication(t, &observations[i], true, false, false, false, true, true)
				scoutResignObservation(t, &observations[i], corpus.Cases[i], corpus, config, authority, keys)
			}
		},
		"explicit correctness and usefulness": func(t *testing.T, observations []ScoutQualificationObservation, corpus ScoutQualificationCorpus, config ScoutQualificationConfig, authority ScoutQualificationAuthority, keys scoutQualificationKeys) {
			for i := ScoutOrdinaryMinimum; i < ScoutOrdinaryMinimum+11; i++ {
				scoutSetPublication(t, &observations[i], true, false, false, false, true, false)
				scoutResignObservation(t, &observations[i], corpus.Cases[i], corpus, config, authority, keys)
			}
		},
		"first useful audio latency": func(t *testing.T, observations []ScoutQualificationObservation, corpus ScoutQualificationCorpus, config ScoutQualificationConfig, authority ScoutQualificationAuthority, keys scoutQualificationKeys) {
			for i := ScoutOrdinaryMinimum; i < ScoutOrdinaryMinimum+26; i++ {
				scoutSetDeviceLatency(t, &observations[i], 2600, 200)
				scoutResignObservation(t, &observations[i], corpus.Cases[i], corpus, config, authority, keys)
			}
		},
		"barge in stop latency": func(t *testing.T, observations []ScoutQualificationObservation, corpus ScoutQualificationCorpus, config ScoutQualificationConfig, authority ScoutQualificationAuthority, keys scoutQualificationKeys) {
			for i := ScoutOrdinaryMinimum; i < ScoutOrdinaryMinimum+26; i++ {
				scoutSetDeviceLatency(t, &observations[i], 500, 501)
				scoutResignObservation(t, &observations[i], corpus.Cases[i], corpus, config, authority, keys)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			corpus, config, observations, authority, keys := scoutTrustedQualificationFixture(t)
			mutate(t, observations, corpus, config, authority, keys)
			receipt, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Now())
			if !errors.Is(err, ErrScoutQualificationFailed) || receipt.Pass || receipt.Schema != "stride.e10.scout-qualification-receipt/v3" || receipt.EvidenceClass != "provider_live_evidence_candidate" || receipt.QualificationState != "trusted_authority_bound_deterministic_evaluation" || receipt.OrdinaryCount != ScoutOrdinaryMinimum || receipt.ExplicitCount != ScoutExplicitMinimum || receipt.AudienceNegativeCount != ScoutAudienceNegativeMinimum || receipt.ProviderTerminalCount != len(observations) || receipt.DeviceCaptureCount != len(observations) || !validDigest(receipt.TrustRegistrySHA256) || !validDigest(receipt.RawEvidenceSetSHA256) || !validDigest(receipt.ReceiptSHA256) {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
		})
	}

	for name, publication := range map[string][4]bool{
		"audio": {true, false, false, false}, "text": {false, true, false, false}, "card content": {false, false, true, false}, "existence signal": {false, false, false, true},
	} {
		t.Run("audience "+name, func(t *testing.T) {
			corpus, config, observations, _, _ := scoutTrustedQualificationFixture(t)
			index := ScoutOrdinaryMinimum + ScoutExplicitMinimum
			scoutSetPublication(t, &observations[index], publication[0], publication[1], publication[2], publication[3], publication[0], publication[0])
			receipt, err := EvaluateScoutQualification(corpus, config, observations, time.Now())
			if !errors.Is(err, ErrScoutQualificationFailed) || receipt.Pass {
				t.Fatalf("audience %s disclosure passed: %+v err=%v", name, receipt, err)
			}
			leaks := receipt.AudienceAudioLeaks + receipt.AudienceTextLeaks + receipt.AudienceCardContentLeaks + receipt.AudienceExistenceSignalLeaks
			if leaks != 1 {
				t.Fatalf("audience %s leak was not classified: %+v", name, receipt)
			}
		})
	}
}

func TestScoutLanesQualifyIndependently(t *testing.T) {
	for _, failingLane := range []ScoutQualificationLane{ScoutLanePersonal, ScoutLaneMeeting} {
		t.Run(string(failingLane), func(t *testing.T) {
			corpus, config, observations, _, _ := scoutTrustedQualificationFixture(t)
			mutated := 0
			for index := ScoutOrdinaryMinimum; index < ScoutOrdinaryMinimum+ScoutExplicitMinimum && mutated < 6; index++ {
				if corpus.Cases[index].Lane != failingLane {
					continue
				}
				scoutSetPublication(t, &observations[index], true, false, false, false, true, false)
				mutated++
			}
			receipt, err := EvaluateScoutQualification(corpus, config, observations, time.Now())
			if !errors.Is(err, ErrScoutQualificationFailed) || receipt.Pass || len(receipt.LaneResults) != 2 {
				t.Fatalf("lane failure receipt=%+v err=%v", receipt, err)
			}
			for _, laneReceipt := range receipt.LaneResults {
				if laneReceipt.Lane == failingLane && laneReceipt.Pass {
					t.Fatalf("failing lane passed: %+v", laneReceipt)
				}
				if laneReceipt.Lane != failingLane && !laneReceipt.Pass {
					t.Fatalf("healthy lane was collapsed into aggregate failure: %+v", laneReceipt)
				}
			}
		})
	}
}

func TestScoutLaneSurfacePlatformSubstitutionAndReuseFailClosed(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		index  int
		mutate func(map[string]any, map[string]any, map[string]any)
	}{
		{name: "personal to meeting", index: 0, mutate: func(provider, device, billing map[string]any) {
			for _, value := range []map[string]any{provider, device, billing} {
				value["lane"], value["surface"] = ScoutLaneMeeting, ScoutSurfaceMeetingVoice
			}
		}},
		{name: "meeting to personal", index: 1, mutate: func(provider, device, billing map[string]any) {
			for _, value := range []map[string]any{provider, device, billing} {
				value["lane"], value["surface"] = ScoutLanePersonal, ScoutSurfacePrivateVoice
			}
		}},
		{name: "ios to web", index: 0, mutate: func(provider, device, billing map[string]any) {
			for _, value := range []map[string]any{provider, device, billing} {
				value["platform"] = ScoutPlatformDesktopWeb
			}
			device["target_id"] = "desktop-web-01"
		}},
		{name: "web to ios", index: 2, mutate: func(provider, device, billing map[string]any) {
			for _, value := range []map[string]any{provider, device, billing} {
				value["platform"] = ScoutPlatformPhysicalIOS
			}
			device["target_id"] = "iphone-physical-01"
		}},
		{name: "terminal lane mismatch", index: 0, mutate: func(provider, _, _ map[string]any) { provider["lane"] = ScoutLaneMeeting }},
		{name: "device platform mismatch", index: 0, mutate: func(_, device, _ map[string]any) { device["platform"] = ScoutPlatformDesktopWeb }},
		{name: "billing surface mismatch", index: 0, mutate: func(_, _, billing map[string]any) { billing["surface"] = ScoutSurfaceMeetingVoice }},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			corpus, config, observations, authority, keys := scoutTrustedQualificationFixture(t)
			observation := &observations[fixture.index]
			providerRaw, deviceRaw, billingRaw := scoutFindRaw(t, observation, "provider"), scoutFindRaw(t, observation, "device"), scoutFindRaw(t, observation, "billing")
			var provider, device, billing map[string]any
			mustUnmarshal(t, providerRaw.Body, &provider)
			mustUnmarshal(t, deviceRaw.Body, &device)
			mustUnmarshal(t, billingRaw.Body, &billing)
			fixture.mutate(provider, device, billing)
			scoutReplaceRaw(t, providerRaw, provider)
			scoutReplaceRaw(t, deviceRaw, device)
			scoutReplaceRaw(t, billingRaw, billing)
			scoutResignObservation(t, observation, corpus.Cases[fixture.index], corpus, config, authority, keys)
			if _, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Now()); !errors.Is(err, ErrScoutQualificationEvidence) {
				t.Fatalf("route substitution err=%v", err)
			}
		})
	}

	t.Run("cross lane evidence reuse", func(t *testing.T) {
		corpus, config, observations, authority, keys := scoutTrustedQualificationFixture(t)
		observations[0].RawEvidence = append([]ScoutRawEvidence(nil), observations[1].RawEvidence...)
		scoutResignObservation(t, &observations[0], corpus.Cases[0], corpus, config, authority, keys)
		if _, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Now()); !errors.Is(err, ErrScoutQualificationEvidence) {
			t.Fatalf("cross-lane evidence reuse err=%v", err)
		}
	})
}

func TestTrustedScoutQualificationFailsClosedOnAuthorityAndEvidenceAttacks(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *ScoutQualificationCorpus, *ScoutQualificationConfig, []ScoutQualificationObservation, *ScoutQualificationAuthority, scoutQualificationKeys){
		"missing evidence signature": func(t *testing.T, _ *ScoutQualificationCorpus, _ *ScoutQualificationConfig, observations []ScoutQualificationObservation, _ *ScoutQualificationAuthority, _ scoutQualificationKeys) {
			observations[0].RawEvidence[0].Signature = nil
		},
		"unanchored signer": func(t *testing.T, _ *ScoutQualificationCorpus, _ *ScoutQualificationConfig, observations []ScoutQualificationObservation, _ *ScoutQualificationAuthority, _ scoutQualificationKeys) {
			foreign := scoutTestPrivateKey("foreign")
			observations[0].RawEvidence[0].PublicKey = foreign.Public().(ed25519.PublicKey)
			observations[0].RawEvidence[0].Signature = ed25519.Sign(foreign, observations[0].RawEvidence[0].Body)
		},
		"same operator and reviewer": func(t *testing.T, _ *ScoutQualificationCorpus, _ *ScoutQualificationConfig, observations []ScoutQualificationObservation, _ *ScoutQualificationAuthority, _ scoutQualificationKeys) {
			observations[0].ReviewerAttestation = observations[0].OperatorAttestation
		},
		"input audio digest substitution": func(t *testing.T, corpus *ScoutQualificationCorpus, config *ScoutQualificationConfig, observations []ScoutQualificationObservation, authority *ScoutQualificationAuthority, keys scoutQualificationKeys) {
			raw := scoutFindRaw(t, &observations[0], "provider")
			var value map[string]any
			mustUnmarshal(t, raw.Body, &value)
			value["input_audio_sha256"] = digest("substituted-audio")
			scoutReplaceRawAndSign(t, raw, value, keys.provider)
			scoutResignObservation(t, &observations[0], corpus.Cases[0], *corpus, *config, *authority, keys)
		},
		"registry signature tamper": func(t *testing.T, _ *ScoutQualificationCorpus, _ *ScoutQualificationConfig, _ []ScoutQualificationObservation, authority *ScoutQualificationAuthority, _ scoutQualificationKeys) {
			authority.RegistrySignature[0] ^= 0xff
		},
		"attempt ledger path substitution": func(t *testing.T, _ *ScoutQualificationCorpus, _ *ScoutQualificationConfig, _ []ScoutQualificationObservation, authority *ScoutQualificationAuthority, _ scoutQualificationKeys) {
			authority.AttemptLedgerDirectory = t.TempDir()
			if err := os.Chmod(authority.AttemptLedgerDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"duplicate attempt": func(t *testing.T, corpus *ScoutQualificationCorpus, config *ScoutQualificationConfig, observations []ScoutQualificationObservation, authority *ScoutQualificationAuthority, keys scoutQualificationKeys) {
			observations[1].AttemptID = observations[0].AttemptID
			scoutSetAttemptID(t, &observations[1], observations[0].AttemptID)
			scoutResignObservation(t, &observations[1], corpus.Cases[1], *corpus, *config, *authority, keys)
		},
		"duplicate evidence id": func(t *testing.T, _ *ScoutQualificationCorpus, _ *ScoutQualificationConfig, observations []ScoutQualificationObservation, _ *ScoutQualificationAuthority, _ scoutQualificationKeys) {
			observations[1].RawEvidence[0].ID = observations[0].RawEvidence[0].ID
		},
		"duplicate provider call": func(t *testing.T, corpus *ScoutQualificationCorpus, config *ScoutQualificationConfig, observations []ScoutQualificationObservation, authority *ScoutQualificationAuthority, keys scoutQualificationKeys) {
			raw := scoutFindRaw(t, &observations[1], "provider")
			var value map[string]any
			mustUnmarshal(t, raw.Body, &value)
			value["provider_call_id"] = scoutCaseID("call", 0)
			scoutReplaceRawAndSign(t, raw, value, keys.provider)
			scoutResignObservation(t, &observations[1], corpus.Cases[1], *corpus, *config, *authority, keys)
		},
		"unknown evidence field": func(t *testing.T, _ *ScoutQualificationCorpus, _ *ScoutQualificationConfig, observations []ScoutQualificationObservation, _ *ScoutQualificationAuthority, keys scoutQualificationKeys) {
			raw := scoutFindRaw(t, &observations[0], "provider")
			raw.Body = append(raw.Body[:len(raw.Body)-1], []byte(`,"unknown":true}`)...)
			raw.BodySHA256 = digestBytes(raw.Body)
			raw.Signature = ed25519.Sign(keys.provider, raw.Body)
		},
		"duplicate raw JSON key": func(t *testing.T, _ *ScoutQualificationCorpus, _ *ScoutQualificationConfig, observations []ScoutQualificationObservation, _ *ScoutQualificationAuthority, keys scoutQualificationKeys) {
			raw := scoutFindRaw(t, &observations[0], "provider")
			raw.Body = append([]byte(`{"type":"response.done",`), append(raw.Body[1:len(raw.Body)-1], []byte(`,"type":"response.done"}`)...)...)
			raw.BodySHA256 = digestBytes(raw.Body)
			raw.Signature = ed25519.Sign(keys.provider, raw.Body)
		},
	} {
		t.Run(name, func(t *testing.T) {
			corpus, config, observations, authority, keys := scoutTrustedQualificationFixture(t)
			mutate(t, &corpus, &config, observations, &authority, keys)
			if _, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Now()); !errors.Is(err, ErrScoutQualificationEvidence) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestTrustedScoutAttemptsAreDurablyOneUse(t *testing.T) {
	corpus, config, observations, authority, _ := scoutTrustedQualificationFixture(t)
	if _, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Now()); !errors.Is(err, ErrScoutAttemptReused) {
		t.Fatalf("replayed attempt corpus err=%v", err)
	}
	if err := claimScoutAttempts(authority, digest("different-registry"), []string{observations[0].AttemptID}); !errors.Is(err, ErrScoutAttemptReused) {
		t.Fatalf("attempt was reusable across registry revisions: %v", err)
	}
}

func TestTrustedScoutAttemptsConcurrentAuthoritiesHaveExactlyOneWinner(t *testing.T) {
	corpus, config, observations, authority, _ := scoutTrustedQualificationFixture(t)
	authorityA, authorityB := authority, authority
	authorityA.RegistryJSON = append([]byte(nil), authority.RegistryJSON...)
	authorityB.RegistryJSON = append([]byte(nil), authority.RegistryJSON...)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, currentAuthority := range []ScoutQualificationAuthority{authorityA, authorityB} {
		go func(currentAuthority ScoutQualificationAuthority) {
			<-start
			_, err := EvaluateTrustedScoutQualification(corpus, config, observations, currentAuthority, time.Now())
			results <- err
		}(currentAuthority)
	}
	close(start)
	successes, reused := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, ErrScoutAttemptReused):
			reused++
		default:
			t.Fatalf("concurrent qualification err=%v", err)
		}
	}
	if successes != 1 || reused != 1 {
		t.Fatalf("concurrent outcomes successes=%d reused=%d", successes, reused)
	}
}

func TestScoutAttemptLedgerRejectsHistoricalDuplicatesAndHandlesShortWrites(t *testing.T) {
	t.Run("historical duplicate claim", func(t *testing.T) {
		_, _, observations, authority, _ := scoutTrustedQualificationFixture(t)
		claim := digest(authority.ExpectedAttemptLedgerID + "\x00" + observations[0].AttemptID)
		batch := scoutAttemptClaimBatch{Schema: "stride.e10.scout-attempt-claim-batch/v1", RegistrySHA256: authority.ExpectedRegistrySHA256, AttemptLedgerID: authority.ExpectedAttemptLedgerID, ClaimSHA256s: []string{claim}}
		body, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(authority.AttemptLedgerDirectory, "."+digest(authority.ExpectedAttemptLedgerID)+".claims.jsonl")
		if err := os.WriteFile(path, append(append(append([]byte(nil), body...), '\n'), append(body, '\n')...), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := claimScoutAttempts(authority, authority.ExpectedRegistrySHA256, []string{"new-attempt"}); !errors.Is(err, ErrScoutQualificationEvidence) {
			t.Fatalf("historical duplicate ledger err=%v", err)
		}
	})

	t.Run("partial writes complete", func(t *testing.T) {
		corpus, config, observations, authority, _ := scoutTrustedQualificationFixture(t)
		original := scoutAttemptLedgerWrite
		scoutAttemptLedgerWrite = func(file *os.File, body []byte) (int, error) {
			if len(body) > 7 {
				body = body[:7]
			}
			return file.Write(body)
		}
		t.Cleanup(func() { scoutAttemptLedgerWrite = original })
		if _, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Now()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("zero write rolls back", func(t *testing.T) {
		corpus, config, observations, authority, _ := scoutTrustedQualificationFixture(t)
		original := scoutAttemptLedgerWrite
		t.Cleanup(func() { scoutAttemptLedgerWrite = original })
		scoutAttemptLedgerWrite = func(*os.File, []byte) (int, error) { return 0, nil }
		if _, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Now()); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("zero write err=%v", err)
		}
		scoutAttemptLedgerWrite = original
		if _, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Now()); err != nil {
			t.Fatalf("rolled-back ledger remained poisoned: %v", err)
		}
	})
}

func TestScoutProviderBillingAndTerminalAccountingBindingsFailClosed(t *testing.T) {
	for _, fixture := range []struct {
		name      string
		mutate    func(map[string]any, map[string]any)
		wantError error
		check     func(*testing.T, ScoutQualificationReceipt)
	}{
		{name: "provider project", wantError: ErrScoutQualificationEvidence, mutate: func(terminal, billing map[string]any) {
			identity := terminal["provider_identity"].(map[string]any)
			identity["projectId"] = "proj_foreign"
			billing["provider_identity"] = identity
		}},
		{name: "provider organization", wantError: ErrScoutQualificationEvidence, mutate: func(terminal, billing map[string]any) {
			identity := terminal["provider_identity"].(map[string]any)
			identity["organizationId"] = "org_foreign"
			billing["provider_identity"] = identity
		}},
		{name: "credential mode", wantError: ErrScoutQualificationEvidence, mutate: func(terminal, billing map[string]any) {
			identity := terminal["provider_identity"].(map[string]any)
			identity["credentialMode"] = "user_api_key"
			billing["provider_identity"] = identity
		}},
		{name: "billing account", wantError: ErrScoutQualificationEvidence, mutate: func(terminal, billing map[string]any) {
			route := terminal["billing_reconciliation"].(map[string]any)
			route["billingAccountId"] = "billing-foreign"
			billing["billing_reconciliation"] = route
		}},
		{name: "per terminal ceiling", wantError: ErrScoutQualificationEvidence, mutate: func(terminal, billing map[string]any) {
			usage := map[string]any{"input_text_tokens": int64(9), "input_audio_tokens": int64(0), "cached_text_tokens": int64(0), "cached_audio_tokens": int64(0), "output_text_tokens": int64(0), "output_audio_tokens": int64(2), "total_tokens": int64(11)}
			terminal["usage"], terminal["cost_nano_usd"] = usage, int64(11_000)
			billing["usage"], billing["cost_nano_usd"] = usage, int64(11_000)
		}},
		{name: "aggregate accepted ceiling", wantError: ErrScoutQualificationFailed, mutate: func(terminal, billing map[string]any) {
			usage := map[string]any{"input_text_tokens": int64(3), "input_audio_tokens": int64(0), "cached_text_tokens": int64(0), "cached_audio_tokens": int64(0), "output_text_tokens": int64(0), "output_audio_tokens": int64(2), "total_tokens": int64(5)}
			terminal["usage"], terminal["cost_nano_usd"] = usage, int64(5_000)
			billing["usage"], billing["cost_nano_usd"] = usage, int64(5_000)
		}, check: func(t *testing.T, receipt ScoutQualificationReceipt) {
			if receipt.AcceptedUsage.TotalTokens != 10_502 || receipt.AcceptedUsage.CostNanoUSD != 10_502_000 || receipt.RejectedTerminalCount != 0 {
				t.Fatalf("accepted accounting=%+v rejected=%d", receipt.AcceptedUsage, receipt.RejectedTerminalCount)
			}
		}},
		{name: "rejected terminal", wantError: ErrScoutQualificationFailed, mutate: func(terminal, billing map[string]any) {
			terminal["status"], terminal["accepted_output"] = "failed", false
			billing["status"], billing["accepted_output"] = "failed", false
		}, check: func(t *testing.T, receipt ScoutQualificationReceipt) {
			if receipt.AcceptedTerminalCount != 3_499 || receipt.RejectedTerminalCount != 1 || receipt.RejectedUsage.TotalTokens != 3 || receipt.RejectedUsage.CostNanoUSD != 3_000 {
				t.Fatalf("rejected accounting receipt=%+v", receipt)
			}
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			corpus, config, observations, authority, keys := scoutTrustedQualificationFixture(t)
			provider := scoutFindRaw(t, &observations[0], "provider")
			billingRaw := scoutFindRaw(t, &observations[0], "billing")
			var terminalValue, billingValue map[string]any
			mustUnmarshal(t, provider.Body, &terminalValue)
			mustUnmarshal(t, billingRaw.Body, &billingValue)
			fixture.mutate(terminalValue, billingValue)
			scoutReplaceRawAndSign(t, provider, terminalValue, keys.provider)
			scoutReplaceRawAndSign(t, billingRaw, billingValue, keys.billing)
			scoutResignObservation(t, &observations[0], corpus.Cases[0], corpus, config, authority, keys)
			receipt, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, time.Now())
			if !errors.Is(err, fixture.wantError) {
				t.Fatalf("receipt=%+v err=%v want=%v", receipt, err, fixture.wantError)
			}
			if fixture.check != nil {
				fixture.check(t, receipt)
			}
		})
	}
}

func TestScoutFinalReceiptCannotBePromotedBySelfHashTamperOrExpiry(t *testing.T) {
	now := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	corpus, config, observations, authority, _ := scoutTrustedQualificationFixture(t)
	receipt, err := EvaluateTrustedScoutQualification(corpus, config, observations, authority, now)
	if err != nil {
		t.Fatal(err)
	}
	verifier := authority
	verifier.ReceiptSignerPrivateKey = nil
	if err := WriteScoutQualificationReceipt(filepath.Join(authority.AttemptLedgerDirectory, "candidate-self-hash.json"), receipt); !errors.Is(err, ErrScoutQualificationInvalid) {
		t.Fatalf("candidate bypassed authenticated writer: %v", err)
	}
	if err := VerifyTrustedScoutQualificationReceipt(receipt, verifier, now.Add(5*time.Minute)); !errors.Is(err, ErrScoutReceiptExpired) {
		t.Fatalf("expired receipt err=%v", err)
	}
	for name, mutate := range map[string]func(*ScoutQualificationReceipt){
		"self hash": func(receipt *ScoutQualificationReceipt) {
			receipt.AcceptedTerminalCount++
			receipt.ReceiptSignature = nil
			receipt.ReceiptSHA256, _ = scoutReceiptDigest(*receipt)
		},
		"signature": func(receipt *ScoutQualificationReceipt) { receipt.ReceiptSignature[0] ^= 0xff },
		"provider identity digest": func(receipt *ScoutQualificationReceipt) {
			receipt.ProviderIdentitySHA256 = digest("foreign-provider-identity")
		},
	} {
		t.Run(name, func(t *testing.T) {
			forged := receipt
			forged.ReceiptSignature = append([]byte(nil), receipt.ReceiptSignature...)
			mutate(&forged)
			if err := VerifyTrustedScoutQualificationReceipt(forged, verifier, now.Add(time.Minute)); !errors.Is(err, ErrScoutQualificationEvidence) {
				t.Fatalf("forged receipt err=%v", err)
			}
		})
	}
	local, err := EvaluateScoutQualification(corpus, config, observations, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTrustedScoutQualificationReceipt(local, verifier, now.Add(time.Minute)); !errors.Is(err, ErrScoutQualificationEvidence) {
		t.Fatalf("local self-hash crossed trusted gate: %v", err)
	}
}

func TestScoutCachedTokensAreSubsetsNotAdditionalProviderTotals(t *testing.T) {
	corpus, config, observations, _, _ := scoutTrustedQualificationFixture(t)
	raw := scoutFindRaw(t, &observations[0], "provider")
	var value map[string]any
	mustUnmarshal(t, raw.Body, &value)
	usage := value["usage"].(map[string]any)
	usage["input_text_tokens"] = int64(2)
	usage["cached_text_tokens"] = int64(1)
	usage["total_tokens"] = int64(4)
	value["cost_nano_usd"] = int64(3400)
	scoutReplaceRaw(t, raw, value)
	billing := scoutFindRaw(t, &observations[0], "billing")
	var billingValue map[string]any
	mustUnmarshal(t, billing.Body, &billingValue)
	billingValue["usage"] = usage
	billingValue["cost_nano_usd"] = int64(3400)
	scoutReplaceRaw(t, billing, billingValue)
	receipt, err := EvaluateScoutQualification(corpus, config, observations, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Usage.TotalTokens != int64(len(observations))*3+1 || receipt.Usage.CostNanoUSD != int64(len(observations))*3000+400 {
		t.Fatalf("cached subset accounting wrong: %+v", receipt.Usage)
	}
}

func TestScoutRouteAndPricingIdentityAreImmutable(t *testing.T) {
	_, config, _, _, _ := scoutTrustedQualificationFixture(t)
	for name, mutate := range map[string]func(*ScoutQualificationConfig){
		"voice":            func(config *ScoutQualificationConfig) { config.Voice = "other" },
		"vad policy":       func(config *ScoutQualificationConfig) { config.VADPolicySHA256 = digest("other-vad") },
		"tool policy":      func(config *ScoutQualificationConfig) { config.ToolPolicySHA256 = digest("other-tools") },
		"pricing revision": func(config *ScoutQualificationConfig) { config.PricingRevisionSHA256 = digest("other-pricing") },
		"source":           func(config *ScoutQualificationConfig) { config.SourceArtifactSetSHA256 = digest("other-source") },
	} {
		t.Run(name, func(t *testing.T) {
			copy := config
			mutate(&copy)
			if copy.Validate() == nil {
				t.Fatalf("%s mutation preserved immutable config", name)
			}
		})
	}
}

func TestScoutLatencyBootstrapIsDeterministicForAllPercentiles(t *testing.T) {
	values := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	first := scoutLatencySummary(values, "fixed-seed")
	second := scoutLatencySummary(values, "fixed-seed")
	if first != second || first.Method != "deterministic_percentile_bootstrap_95" || first.SampleCount != len(values) || first.Replicates != scoutBootstrapReplicates {
		t.Fatalf("bootstrap is not deterministic or identified: first=%+v second=%+v", first, second)
	}
	for name, interval := range map[string]ScoutLatencyInterval{"p50": first.P50, "p95": first.P95, "p99": first.P99} {
		if interval.Low95MS > interval.PointMS || interval.PointMS > interval.High95MS {
			t.Fatalf("%s interval does not contain point estimate: %+v", name, interval)
		}
	}
}

func TestScoutQualificationCorpusShortfallAndDuplicateAudioFail(t *testing.T) {
	release := digest("release")
	cases := make([]ScoutQualificationCase, ScoutOrdinaryMinimum+ScoutExplicitMinimum+ScoutAudienceNegativeMinimum-1)
	for i := range cases {
		cases[i] = ScoutQualificationCase{ID: scoutCaseID("shortfall", i), Class: ScoutCorpusOrdinary, InputAudioSHA256: digest(scoutCaseID("audio", i))}
	}
	if _, err := FreezeScoutQualificationCorpus(1, release, cases); !errors.Is(err, ErrScoutQualificationInvalid) {
		t.Fatalf("shortfall err=%v", err)
	}
	corpus, _, _, _, _ := scoutTrustedQualificationFixture(t)
	corpus.Cases[1].InputAudioSHA256 = corpus.Cases[0].InputAudioSHA256
	if _, err := FreezeScoutQualificationCorpus(1, release, corpus.Cases); !errors.Is(err, ErrScoutQualificationInvalid) {
		t.Fatalf("duplicate audio err=%v", err)
	}
}

func scoutTrustedQualificationFixture(t *testing.T) (ScoutQualificationCorpus, ScoutQualificationConfig, []ScoutQualificationObservation, ScoutQualificationAuthority, scoutQualificationKeys) {
	t.Helper()
	keys := scoutQualificationKeys{root: scoutTestPrivateKey("root"), provider: scoutTestPrivateKey("provider"), deviceIOS: scoutTestPrivateKey("device-ios"), deviceWeb: scoutTestPrivateKey("device-web"), billing: scoutTestPrivateKey("billing"), receipt: scoutTestPrivateKey("receipt"), operator: scoutTestPrivateKey("operator"), reviewer: scoutTestPrivateKey("reviewer")}
	release := digest("release-e10-scout")
	cases := make([]ScoutQualificationCase, 0, ScoutOrdinaryMinimum+ScoutExplicitMinimum+ScoutAudienceNegativeMinimum)
	for i := 0; i < ScoutOrdinaryMinimum; i++ {
		lane, surface, platform := scoutCaseRoute(i)
		cases = append(cases, ScoutQualificationCase{ID: scoutCaseID("aordinary", i), Class: ScoutCorpusOrdinary, Lane: lane, Surface: surface, Platform: platform, InputAudioSHA256: digest("ordinary-audio-" + scoutCaseID("aordinary", i))})
	}
	for i := 0; i < ScoutExplicitMinimum; i++ {
		lane, surface, platform := scoutCaseRoute(i)
		cases = append(cases, ScoutQualificationCase{ID: scoutCaseID("bexplicit", i), Class: ScoutCorpusExplicit, Lane: lane, Surface: surface, Platform: platform, InputAudioSHA256: digest("explicit-audio-" + scoutCaseID("bexplicit", i))})
	}
	for i := 0; i < ScoutAudienceNegativeMinimum; i++ {
		lane, surface, platform := scoutCaseRoute(i)
		cases = append(cases, ScoutQualificationCase{ID: scoutCaseID("caudience", i), Class: ScoutCorpusAudienceNegative, Lane: lane, Surface: surface, Platform: platform, InputAudioSHA256: digest("audience-audio-" + scoutCaseID("caudience", i))})
	}
	corpus, err := FreezeScoutQualificationCorpus(1, release, cases)
	if err != nil {
		t.Fatal(err)
	}
	config, err := FreezeScoutQualificationConfig(ScoutQualificationConfig{
		Version: 1, ReleaseSHA256: release, CandidateSHA256: digest("candidate"), SourceArtifactSetSHA256: digest("source-artifacts"), Model: ScoutRealtimeModel,
		ProviderIdentity:      ScoutProviderIdentity{Provider: "openai", ProjectID: "proj_stride_e10", OrganizationID: "org_stride", CredentialMode: ScoutCredentialProjectServiceAccount},
		BillingReconciliation: ScoutBillingReconciliationRoute{Mode: ScoutBillingProviderUsageExport, BillingAccountID: "billing-stride", EvidenceSource: "https://api.openai.com/v1/organization/usage", EvidenceRevisionSHA256: digest("billing-export-revision")},
		RequiredDistribution:  ScoutQualificationDistribution{PersonalPhysicalIOS: 800, PersonalDesktopWeb: 800, MeetingPhysicalIOS: 800, MeetingDesktopWeb: 800},
		ReasoningEffort:       "high", Voice: "marin", VADMode: "server_vad", VADPolicySHA256: digest("vad-policy"), ToolPolicySHA256: digest("tool-policy"), PromptSHA256: digest("prompt"), EventSchemaSHA256: digest("schema"),
		PricingSource: "https://developers.openai.com/api/docs/pricing", PricingRevisionSHA256: digest("pricing-revision"),
		InputTextNanoUSDPerToken: 1000, InputAudioNanoUSDPerToken: 1000, CachedTextNanoUSDPerToken: 400, CachedAudioNanoUSDPerToken: 400, OutputTextNanoUSDPerToken: 1000, OutputAudioNanoUSDPerToken: 1000,
		MaxTerminalTokens: 10, MaxTerminalCostNanoUSD: 10_000, MaxAcceptedTokens: 10_501, MaxAcceptedCostNanoUSD: 10_500_400, MaxRejectedTokens: 0, MaxRejectedCostNanoUSD: 0, MaxRejectedTerminalCount: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	rootPublic := keys.root.Public().(ed25519.PublicKey)
	ledgerDirectory := t.TempDir()
	if err := os.Chmod(ledgerDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	ledgerPath, err := filepath.EvalSymlinks(ledgerDirectory)
	if err != nil {
		t.Fatal(err)
	}
	registry := ScoutQualificationTrustRegistry{
		Schema: "stride.e10.scout-qualification-target-registry/v2", RegistryID: "scout-registry-2026-08", AttemptLedgerID: "scout-attempt-ledger-2026-08", AttemptLedgerPathSHA256: digest(filepath.Clean(ledgerPath)), ReceiptLedgerID: "scout-receipt-ledger-2026-08", ReceiptLedgerPathSHA256: digest(filepath.Clean(ledgerPath)), RootKeyID: "root-key", RootKeyFingerprintSHA256: digestBytes(rootPublic),
		ReleaseSHA256: release, CandidateSHA256: config.CandidateSHA256, SourceArtifactSetSHA256: config.SourceArtifactSetSHA256, ProviderIdentity: config.ProviderIdentity, BillingReconciliation: config.BillingReconciliation, RequiredDistribution: config.RequiredDistribution, CorpusDistribution: corpus.Distribution, CorpusSHA256: corpus.Digest, ConfigSHA256: config.Digest, ReceiptValiditySeconds: 300,
		Signers: []ScoutQualificationSigner{
			scoutRegistrySigner("provider-key", "provider-attestor", "provider_attempt_attestor", "", keys.provider),
			scoutRegistrySigner("device-ios-key", "physical-ios-device-attestor", "device_evidence_attestor", "iphone-physical-01", keys.deviceIOS),
			scoutRegistrySigner("device-web-key", "desktop-web-device-attestor", "device_evidence_attestor", "desktop-web-01", keys.deviceWeb),
			scoutRegistrySigner("billing-key", "billing-attestor", "billing_reconciliation_attestor", "", keys.billing),
			scoutRegistrySigner("receipt-key", "receipt-attestor", "qualification_receipt_attestor", "", keys.receipt),
			scoutRegistrySigner("operator-key", "operator-alice", "operator", "", keys.operator),
			scoutRegistrySigner("reviewer-key", "reviewer-bob", "independent_reviewer", "", keys.reviewer),
		},
	}
	registryJSON, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	authority := ScoutQualificationAuthority{RegistryJSON: registryJSON, RegistrySignature: ed25519.Sign(keys.root, registryJSON), RegistryRootPublicKey: rootPublic, ExpectedRootKeyFingerprintSHA256: digestBytes(rootPublic), ExpectedRootKeyID: registry.RootKeyID, ExpectedRegistrySHA256: digestBytes(registryJSON), ExpectedAttemptLedgerID: registry.AttemptLedgerID, ExpectedReceiptLedgerID: registry.ReceiptLedgerID, AttemptLedgerDirectory: ledgerDirectory, ReceiptSignerKeyID: "receipt-key", ReceiptSignerPrivateKey: keys.receipt}
	observations := make([]ScoutQualificationObservation, 0, len(corpus.Cases))
	for index, testCase := range corpus.Cases {
		published := testCase.Class == ScoutCorpusExplicit
		observations = append(observations, scoutObservation(t, testCase, index, corpus, config, authority, keys, published, 500, 200))
	}
	return corpus, config, observations, authority, keys
}

func scoutRegistrySigner(keyID, identityID, role, targetID string, privateKey ed25519.PrivateKey) ScoutQualificationSigner {
	signer := ScoutQualificationSigner{KeyID: keyID, IdentityID: identityID, Role: role, TargetID: targetID, PublicKeyFingerprintSHA256: digestBytes(privateKey.Public().(ed25519.PublicKey))}
	if role == "device_evidence_attestor" {
		if targetID == "iphone-physical-01" {
			signer.TargetEnvironment = "physical-ios"
		} else {
			signer.TargetEnvironment = "desktop-web"
		}
		signer.TargetRevisionSHA256 = digest(targetID + "-revision")
		signer.PhysicalTarget = true
	}
	return signer
}

func scoutObservation(t *testing.T, testCase ScoutQualificationCase, index int, corpus ScoutQualificationCorpus, config ScoutQualificationConfig, authority ScoutQualificationAuthority, keys scoutQualificationKeys, published bool, firstLatency, bargeLatency int64) ScoutQualificationObservation {
	t.Helper()
	attempt := scoutCaseID("attempt", index)
	usage := map[string]any{"input_text_tokens": int64(1), "input_audio_tokens": int64(0), "cached_text_tokens": int64(0), "cached_audio_tokens": int64(0), "output_text_tokens": int64(0), "output_audio_tokens": int64(2), "total_tokens": int64(3)}
	terminal := map[string]any{"type": "response.done", "case_id": testCase.ID, "attempt_id": attempt, "lane": testCase.Lane, "surface": testCase.Surface, "platform": testCase.Platform, "input_audio_sha256": testCase.InputAudioSHA256, "corpus_sha256": corpus.Digest, "config_sha256": config.Digest, "candidate_sha256": config.CandidateSHA256, "source_artifact_set_sha256": config.SourceArtifactSetSHA256, "release_sha256": corpus.ReleaseSHA256, "registry_sha256": authority.ExpectedRegistrySHA256, "provider_call_id": scoutCaseID("call", index), "status": "completed", "accepted_output": true, "provider_identity": config.ProviderIdentity, "billing_reconciliation": config.BillingReconciliation, "audio_published": published, "text_published": false, "card_content_published": false, "existence_signal_published": false, "usage": usage, "cost_nano_usd": int64(3000)}
	billing := map[string]any{"type": "provider.billing.reconciled", "case_id": testCase.ID, "attempt_id": attempt, "lane": testCase.Lane, "surface": testCase.Surface, "platform": testCase.Platform, "provider_call_id": scoutCaseID("call", index), "provider_identity": config.ProviderIdentity, "billing_reconciliation": config.BillingReconciliation, "corpus_sha256": corpus.Digest, "config_sha256": config.Digest, "candidate_sha256": config.CandidateSHA256, "source_artifact_set_sha256": config.SourceArtifactSetSHA256, "release_sha256": corpus.ReleaseSHA256, "registry_sha256": authority.ExpectedRegistrySHA256, "status": "completed", "accepted_output": true, "usage": usage, "cost_nano_usd": int64(3000)}
	targetID, deviceKeyID, deviceKey := "desktop-web-01", "device-web-key", keys.deviceWeb
	if testCase.Platform == ScoutPlatformPhysicalIOS {
		targetID, deviceKeyID, deviceKey = "iphone-physical-01", "device-ios-key", keys.deviceIOS
	}
	device := map[string]any{"type": "device.capture.complete", "target_id": targetID, "case_id": testCase.ID, "attempt_id": attempt, "lane": testCase.Lane, "surface": testCase.Surface, "platform": testCase.Platform, "input_audio_sha256": testCase.InputAudioSHA256, "corpus_sha256": corpus.Digest, "config_sha256": config.Digest, "candidate_sha256": config.CandidateSHA256, "source_artifact_set_sha256": config.SourceArtifactSetSHA256, "release_sha256": corpus.ReleaseSHA256, "registry_sha256": authority.ExpectedRegistrySHA256, "audio_observed": published, "text_observed": false, "card_content_observed": false, "existence_signal_observed": false, "response_correct": published, "response_useful": published, "address_completed_ms": int64(100)}
	if published {
		device["first_useful_audio_ms"] = int64(100) + firstLatency
		device["barge_in_detected_ms"] = int64(1000)
		device["audio_stopped_ms"] = int64(1000) + bargeLatency
	}
	observation := ScoutQualificationObservation{CaseID: testCase.ID, AttemptID: attempt, RawEvidence: []ScoutRawEvidence{scoutSignedRaw(t, scoutCaseID("provider", index), "provider", "provider_live_attempt_terminal", "provider-key", terminal, keys.provider), scoutSignedRaw(t, scoutCaseID("device", index), "device", "device_live_capture", deviceKeyID, device, deviceKey), scoutSignedRaw(t, scoutCaseID("billing", index), "billing", "provider_billing_reconciliation", "billing-key", billing, keys.billing)}}
	scoutResignObservation(t, &observation, testCase, corpus, config, authority, keys)
	return observation
}

func scoutResignObservation(t *testing.T, observation *ScoutQualificationObservation, testCase ScoutQualificationCase, corpus ScoutQualificationCorpus, config ScoutQualificationConfig, authority ScoutQualificationAuthority, keys scoutQualificationKeys) {
	t.Helper()
	provider := scoutFindRaw(t, observation, "provider")
	device := scoutFindRaw(t, observation, "device")
	billing := scoutFindRaw(t, observation, "billing")
	provider.PublicKey = keys.provider.Public().(ed25519.PublicKey)
	provider.Signature = ed25519.Sign(keys.provider, provider.Body)
	deviceKey := keys.deviceWeb
	device.SignerKeyID = "device-web-key"
	if testCase.Platform == ScoutPlatformPhysicalIOS {
		deviceKey = keys.deviceIOS
		device.SignerKeyID = "device-ios-key"
	}
	device.PublicKey = deviceKey.Public().(ed25519.PublicKey)
	device.Signature = ed25519.Sign(deviceKey, device.Body)
	billing.PublicKey = keys.billing.Public().(ed25519.PublicKey)
	billing.Signature = ed25519.Sign(keys.billing, billing.Body)
	payload, err := scoutAttestationPayload(testCase, *observation, corpus, config, authority.ExpectedRegistrySHA256, provider.BodySHA256, device.BodySHA256, billing.BodySHA256)
	if err != nil {
		t.Fatal(err)
	}
	observation.OperatorAttestation = ScoutSignedAttestation{KeyID: "operator-key", PublicKey: keys.operator.Public().(ed25519.PublicKey), Signature: ed25519.Sign(keys.operator, payload)}
	observation.ReviewerAttestation = ScoutSignedAttestation{KeyID: "reviewer-key", PublicKey: keys.reviewer.Public().(ed25519.PublicKey), Signature: ed25519.Sign(keys.reviewer, payload)}
}

func scoutSetPublication(t *testing.T, observation *ScoutQualificationObservation, audio, text, card, existence, correct, useful bool) {
	t.Helper()
	terminal, device := scoutFindRaw(t, observation, "provider"), scoutFindRaw(t, observation, "device")
	var terminalValue, deviceValue map[string]any
	mustUnmarshal(t, terminal.Body, &terminalValue)
	mustUnmarshal(t, device.Body, &deviceValue)
	terminalValue["audio_published"], deviceValue["audio_observed"] = audio, audio
	terminalValue["text_published"], deviceValue["text_observed"] = text, text
	terminalValue["card_content_published"], deviceValue["card_content_observed"] = card, card
	terminalValue["existence_signal_published"], deviceValue["existence_signal_observed"] = existence, existence
	deviceValue["response_correct"], deviceValue["response_useful"] = correct, useful
	if audio {
		deviceValue["first_useful_audio_ms"], deviceValue["barge_in_detected_ms"], deviceValue["audio_stopped_ms"] = int64(600), int64(1000), int64(1200)
	} else {
		delete(deviceValue, "first_useful_audio_ms")
		delete(deviceValue, "barge_in_detected_ms")
		delete(deviceValue, "audio_stopped_ms")
	}
	scoutReplaceRaw(t, terminal, terminalValue)
	scoutReplaceRaw(t, device, deviceValue)
}

func scoutSetDeviceLatency(t *testing.T, observation *ScoutQualificationObservation, first, barge int64) {
	t.Helper()
	device := scoutFindRaw(t, observation, "device")
	var value map[string]any
	mustUnmarshal(t, device.Body, &value)
	value["first_useful_audio_ms"], value["barge_in_detected_ms"], value["audio_stopped_ms"] = int64(100)+first, int64(1000), int64(1000)+barge
	scoutReplaceRaw(t, device, value)
}

func scoutSetAttemptID(t *testing.T, observation *ScoutQualificationObservation, attemptID string) {
	t.Helper()
	for i := range observation.RawEvidence {
		var value map[string]any
		mustUnmarshal(t, observation.RawEvidence[i].Body, &value)
		value["attempt_id"] = attemptID
		scoutReplaceRaw(t, &observation.RawEvidence[i], value)
	}
}

func scoutFindRaw(t *testing.T, observation *ScoutQualificationObservation, source string) *ScoutRawEvidence {
	t.Helper()
	for i := range observation.RawEvidence {
		if observation.RawEvidence[i].Source == source {
			return &observation.RawEvidence[i]
		}
	}
	t.Fatalf("missing %s raw evidence", source)
	return nil
}

func scoutReplaceRawAndSign(t *testing.T, raw *ScoutRawEvidence, value any, key ed25519.PrivateKey) {
	t.Helper()
	scoutReplaceRaw(t, raw, value)
	raw.PublicKey = key.Public().(ed25519.PublicKey)
	raw.Signature = ed25519.Sign(key, raw.Body)
}

func scoutReplaceRaw(t *testing.T, raw *ScoutRawEvidence, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	raw.Body, raw.BodySHA256 = body, digestBytes(body)
}

func scoutSignedRaw(t *testing.T, id, source, class, keyID string, value any, key ed25519.PrivateKey) ScoutRawEvidence {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return ScoutRawEvidence{ID: id, Source: source, Class: class, SignerKeyID: keyID, PublicKey: key.Public().(ed25519.PublicKey), Signature: ed25519.Sign(key, body), Body: body, BodySHA256: digestBytes(body)}
}

func scoutTestPrivateKey(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("stride-e10-scout-test-key:" + label))
	return ed25519.NewKeyFromSeed(seed[:])
}

func mustUnmarshal(t *testing.T, body []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatal(err)
	}
}

func bytesContainAny(body []byte, values ...[]byte) bool {
	for _, value := range values {
		if len(value) > 0 && strings.Contains(string(body), string(value)) {
			return true
		}
	}
	return false
}

func scoutCaseRoute(index int) (ScoutQualificationLane, ScoutQualificationSurface, ScoutQualificationPlatform) {
	lane, surface := ScoutLanePersonal, ScoutSurfacePrivateVoice
	if index%2 == 1 {
		lane, surface = ScoutLaneMeeting, ScoutSurfaceMeetingVoice
	}
	platform := ScoutPlatformPhysicalIOS
	if index/2%2 == 1 {
		platform = ScoutPlatformDesktopWeb
	}
	return lane, surface, platform
}

func scoutCaseID(prefix string, index int) string { return prefix + "-" + fmt.Sprintf("%06d", index) }
