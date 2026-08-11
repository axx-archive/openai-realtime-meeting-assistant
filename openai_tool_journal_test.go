package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type openAIToolTestHighWater struct {
	mu               sync.Mutex
	anchor           openAIToolJournalAnchor
	lostResponseOnce bool
	rejectOnce       bool
	afterCAS         func()
}

func (store *openAIToolTestHighWater) LoadOpenAIToolJournalAnchor(context.Context, string) (openAIToolJournalAnchor, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.anchor, nil
}

func (store *openAIToolTestHighWater) CompareAndSwapOpenAIToolJournalAnchor(_ context.Context, _ string, current, next openAIToolJournalAnchor) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.anchor != current {
		return errors.New("test high-water conflict")
	}
	if store.rejectOnce {
		store.rejectOnce = false
		return errors.New("test rejected CAS")
	}
	store.anchor = next
	if store.afterCAS != nil {
		callback := store.afterCAS
		store.afterCAS = nil
		callback()
	}
	if store.lostResponseOnce {
		store.lostResponseOnce = false
		return errors.New("test lost CAS response")
	}
	return nil
}

type openAIToolTestKeyring struct {
	mu      sync.Mutex
	current openAIToolJournalKeys
	keys    map[string][]byte
}

type openAIToolInvalidSignatureKeyring struct {
	*openAIToolTestKeyring
}

func (ring *openAIToolInvalidSignatureKeyring) SignOpenAIToolRotationReceipt(ctx context.Context, material []byte) (string, string, string, error) {
	keyID, keyVersion, _, err := ring.openAIToolTestKeyring.SignOpenAIToolRotationReceipt(ctx, material)
	return keyID, keyVersion, "invalid-signature", err
}

func newOpenAIToolTestKeyring() *openAIToolTestKeyring {
	key := func(label string) []byte {
		digest := sha256.Sum256([]byte("openai-tool-test-" + label))
		return append([]byte(nil), digest[:]...)
	}
	current := openAIToolJournalKeys{
		MACKeyID: "managed-mac", MACVersion: "1", MACKey: key("mac"),
		AEADKeyID: "managed-aead", AEADVersion: "1", AEADKey: key("aead"),
		EffectKeyID: "managed-effect", EffectVersion: "1", EffectKey: key("effect"),
	}
	return &openAIToolTestKeyring{current: current, keys: map[string][]byte{
		"mac:managed-mac@1": current.MACKey, "aead:managed-aead@1": current.AEADKey, "effect:managed-effect@1": current.EffectKey,
	}}
}

func (ring *openAIToolTestKeyring) CurrentOpenAIToolJournalKeys(context.Context) (openAIToolJournalKeys, error) {
	ring.mu.Lock()
	defer ring.mu.Unlock()
	return ring.current, nil
}

func (ring *openAIToolTestKeyring) resolve(kind, id, version string) ([]byte, error) {
	ring.mu.Lock()
	defer ring.mu.Unlock()
	key, ok := ring.keys[kind+":"+id+"@"+version]
	if !ok {
		return nil, errors.New("test managed key is unavailable")
	}
	return append([]byte(nil), key...), nil
}

func (ring *openAIToolTestKeyring) OpenAIToolJournalMACKey(ctx context.Context, id, version string) ([]byte, error) {
	return ring.resolve("mac", id, version)
}
func (ring *openAIToolTestKeyring) OpenAIToolJournalAEADKey(ctx context.Context, id, version string) ([]byte, error) {
	return ring.resolve("aead", id, version)
}
func (ring *openAIToolTestKeyring) OpenAIToolJournalEffectKey(ctx context.Context, id, version string) ([]byte, error) {
	return ring.resolve("effect", id, version)
}

func (ring *openAIToolTestKeyring) ValidateOpenAIToolEffectRotationTarget(_ context.Context, id, version string) error {
	ring.mu.Lock()
	defer ring.mu.Unlock()
	target, ok := ring.keys["effect:"+id+"@"+version]
	if !ok {
		return errors.New("test rotation target is unavailable")
	}
	for identity, candidate := range ring.keys {
		if identity == "effect:"+id+"@"+version {
			continue
		}
		if strings.Contains(identity, ":"+id+"@") || hmac.Equal(target, candidate) {
			return errors.New("test rotation target reuses a managed identity or secret")
		}
	}
	return nil
}

func (ring *openAIToolTestKeyring) SignOpenAIToolRotationReceipt(_ context.Context, material []byte) (string, string, string, error) {
	digest := sha256.Sum256(append([]byte("test-audit-signing-key\x00"), material...))
	return "managed-audit-signing", "1", fmt.Sprintf("%x", digest[:]), nil
}

func (ring *openAIToolTestKeyring) VerifyOpenAIToolRotationReceipt(ctx context.Context, id, version string, material []byte, signature string) error {
	wantID, wantVersion, wantSignature, _ := ring.SignOpenAIToolRotationReceipt(ctx, material)
	if id != wantID || version != wantVersion || signature != wantSignature {
		return errors.New("test rotation signature invalid")
	}
	return nil
}

func openAIToolTestExpectation() openAIToolAuthorityExpectation {
	return openAIToolAuthorityExpectation{
		TenantID: "tenant-1", PersonID: "person-1", RequesterAccount: "aj@example.com",
		SessionDigest: "session-digest", ActiveOrgSessionID: "active-session-1", ActiveOrgSessionRev: 7,
		MembershipID: "membership-1", ActiveOrganizationID: "organization-1", MembershipRevision: 8, OrganizationRevision: 9,
		ThreadID: "thread-1", ArtifactID: "artifact-1", ArtifactRevision: "artifact-revision-3",
		SourceWindowDigest: "source-window-11", JobAuthority: codexJobAuthorityWorkspaceWrite,
		RequestPolicyRevision: "request-policy-v1", PolicyRevision: "request-policy-v1",
	}
}

func openAIToolTestOperation(t *testing.T, toolName string, arguments map[string]any) (openAIToolManifestEntry, openAIToolAuthorityExpectation) {
	t.Helper()
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := manifest.admitted(toolName)
	if !ok {
		t.Fatalf("tool %q not admitted", toolName)
	}
	digest, _, err := openAIToolCanonicalDigest(arguments)
	if err != nil {
		t.Fatal(err)
	}
	expectation := openAIToolTestExpectation()
	expectation.ToolName, expectation.ManifestDigest, expectation.SchemaDigest, expectation.ArgumentsDigest, expectation.PolicyRevision = entry.Name, openAIToolManifestV1SHA256, entry.SchemaSHA256, digest, entry.PolicyRevision
	return entry, expectation
}

func openAIToolTestProposal(responseID, callID string) openAIToolJournalProposal {
	return openAIToolJournalProposal{
		ProviderResponseID: responseID, ProviderCallID: callID, PreimageDigest: "preimage-1",
		ManifestDigest: openAIToolManifestV1SHA256, RunID: "run-" + responseID, RunRequestDigest: "test-request-digest",
		ExactOutputItems: []json.RawMessage{json.RawMessage(fmt.Sprintf(`{"type":"function_call","name":"create_artifact","call_id":%q,"arguments":"{}"}`, callID))},
	}
}

func openAIToolSecureTestDirectory(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "managed-journal")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestOpenAIToolJournalNormalRestartReplayAndLostCAS(t *testing.T) {
	ctx := context.Background()
	directory := openAIToolSecureTestDirectory(t)
	highWater := &openAIToolTestHighWater{lostResponseOnce: true}
	keyring := newOpenAIToolTestKeyring()
	journal, err := openOpenAIToolJournal(ctx, directory, "tenant-1-journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	arguments := map[string]any{"mode": "artifacts", "query": "save", "content": "body"}
	entry, expectation := openAIToolTestOperation(t, "create_artifact", arguments)
	record, _, replay, err := journal.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp-1", "call-1"), []openAIResponsesToolInputItem{{Role: "user", Content: "save"}})
	if err != nil || replay || record.State != openAIToolStateReserved || record.AttemptCount != 0 {
		t.Fatalf("reserve record=%+v replay=%v err=%v", record, replay, err)
	}
	if err := journal.BeginAttempt(ctx, record.OperationID, "preimage-1"); err != nil {
		t.Fatal(err)
	}
	output := json.RawMessage(`{"artifact_id":"artifact-created","title":"Saved","type":"document","status":"created"}`)
	if err := journal.CommitEffect(ctx, record.OperationID, openAIToolEffectCommit{FunctionOutput: output, PostimageDigest: "postimage-1", ReconciliationDigest: "reconcile-1"}); err != nil {
		t.Fatal(err)
	}
	history := []openAIResponsesToolInputItem{{Role: "user", Content: "save"}, {Type: "function_call_output", CallID: "call-1", Output: string(output)}}
	if err := journal.MarkContinuationSent(ctx, record.OperationID, history); err != nil {
		t.Fatal(err)
	}
	terminal := []json.RawMessage{json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Saved"}]}`)}
	if err := journal.RecordTerminalResponse(ctx, record.OperationID, "resp-terminal", "Saved", terminal); err != nil {
		t.Fatal(err)
	}
	runDigest, err := openAIToolFinalizationRunDigest(openAIToolRunBaseExpectation(expectation), record.RunID, "Saved", []string{record.OperationID})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.CommitFinalUse(ctx, record.OperationID, openAIToolFinalizationCommit{RunDigest: runDigest, OperationIDs: []string{record.OperationID}, FinalUseDigest: "final-use", FanOutReceiptDigest: "fan-out"}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Complete(ctx, record.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openOpenAIToolJournal(ctx, directory, "tenant-1-journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayedRecord, envelope, replay, err := reopened.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp-2", "call-2"), []openAIResponsesToolInputItem{{Role: "user", Content: "save"}})
	if err != nil || !replay || replayedRecord.OperationID != record.OperationID || replayedRecord.State != openAIToolStateCompleted || string(envelope.ToolOutput) != string(output) || envelope.FinalOutput != "Saved" {
		t.Fatalf("restart replay record=%+v envelope=%+v replay=%v err=%v", replayedRecord, envelope, replay, err)
	}
	if len(replayedRecord.CorrelationDigests) != 5 || replayedRecord.AttemptCount != 1 {
		t.Fatalf("correlation/attempt receipt mismatch: %+v", replayedRecord)
	}
}

func TestOpenAIToolJournalRollbackTamperLinksLockAndKeyReuseFailClosed(t *testing.T) {
	ctx := context.Background()
	t.Run("rollback and tamper", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		highWater := &openAIToolTestHighWater{}
		keyring := newOpenAIToolTestKeyring()
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		initial, err := os.ReadFile(filepath.Join(directory, openAIToolJournalFileName))
		if err != nil {
			t.Fatal(err)
		}
		arguments := map[string]any{"query": "remember"}
		entry, expectation := openAIToolTestOperation(t, "answer_memory_question", arguments)
		_, _, _, err = journal.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp", "call"), []openAIResponsesToolInputItem{{Role: "user", Content: "remember"}})
		if err != nil {
			t.Fatal(err)
		}
		_ = journal.Close()
		if err := os.WriteFile(filepath.Join(directory, openAIToolJournalFileName), initial, 0o600); err != nil {
			t.Fatal(err)
		}
		if reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring); !errors.Is(err, errOpenAIToolJournalRollback) {
			if reopened != nil {
				_ = reopened.Close()
			}
			t.Fatalf("stale backup accepted: %v", err)
		}
	})

	t.Run("lock", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		if second, err := openOpenAIToolJournal(ctx, directory, "journal", &openAIToolTestHighWater{anchor: journal.anchor}, newOpenAIToolTestKeyring()); err == nil {
			_ = second.Close()
			t.Fatal("concurrent journal process acquired the same lock")
		}
	})

	t.Run("hardlink and symlink", func(t *testing.T) {
		for _, kind := range []string{"hardlink", "symlink"} {
			t.Run(kind, func(t *testing.T) {
				directory := openAIToolSecureTestDirectory(t)
				target := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(directory, openAIToolJournalFileName)
				var err error
				if kind == "hardlink" {
					err = os.Link(target, path)
				} else {
					err = os.Symlink(target, path)
				}
				if err != nil {
					t.Fatal(err)
				}
				if journal, err := openOpenAIToolJournal(ctx, directory, "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring()); err == nil {
					_ = journal.Close()
					t.Fatalf("%s journal path was accepted", kind)
				}
			})
		}
	})

	t.Run("pairwise key reuse", func(t *testing.T) {
		ring := newOpenAIToolTestKeyring()
		ring.current.AEADKeyID = ring.current.MACKeyID
		if journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, ring); err == nil || !strings.Contains(err.Error(), "pairwise distinct") {
			if journal != nil {
				_ = journal.Close()
			}
			t.Fatalf("reused managed key identity accepted: %v", err)
		}
	})
}

func TestOpenAIToolJournalEffectKeyRotationPreservesImmutableOperationAndRetirementFence(t *testing.T) {
	ctx := context.Background()
	directory := openAIToolSecureTestDirectory(t)
	highWater := &openAIToolTestHighWater{}
	keyring := newOpenAIToolTestKeyring()
	journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	arguments := map[string]any{"query": "What changed?"}
	entry, expectation := openAIToolTestOperation(t, "answer_memory_question", arguments)
	record, _, _, err := journal.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp-1", "call-1"), []openAIResponsesToolInputItem{{Role: "user", Content: "What changed?"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.CanRetireEffectKey(ctx, "managed-effect", "1"); err == nil {
		t.Fatal("current effect key retired before migration")
	}
	newKeyDigest := sha256.Sum256([]byte("openai-tool-test-effect-2"))
	keyring.mu.Lock()
	keyring.keys["effect:managed-effect-2@2"] = append([]byte(nil), newKeyDigest[:]...)
	keyring.mu.Unlock()
	receipt, err := journal.RotateAliases(ctx, "managed-effect-2", "2")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.OperationCount != 1 || receipt.OperationSetDigest == "" || receipt.Signature == "" {
		t.Fatalf("rotation receipt incomplete: %+v", receipt)
	}
	rotated, _, err := journal.Record(ctx, record.OperationID)
	if err != nil || len(rotated.EffectAliases) != 2 {
		t.Fatalf("rotated aliases=%+v err=%v", rotated.EffectAliases, err)
	}
	keyring.mu.Lock()
	keyring.current.EffectKeyID, keyring.current.EffectVersion, keyring.current.EffectKey = "managed-effect-2", "2", append([]byte(nil), newKeyDigest[:]...)
	keyring.mu.Unlock()
	if err := journal.CanRetireEffectKey(ctx, "managed-effect", "1"); err != nil {
		t.Fatalf("signed complete rotation did not open retirement fence: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayed, _, replay, err := reopened.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp-2", "call-2"), []openAIResponsesToolInputItem{{Role: "user", Content: "What changed?"}})
	if err != nil || !replay || replayed.OperationID != record.OperationID {
		t.Fatalf("post-rotation semantic replay changed operation: got=%+v replay=%v err=%v", replayed, replay, err)
	}
}

func TestOpenAIToolEffectKeyAdvanceWithoutSignedMigrationFailsClosed(t *testing.T) {
	ctx := context.Background()
	directory := openAIToolSecureTestDirectory(t)
	highWater := &openAIToolTestHighWater{}
	keyring := newOpenAIToolTestKeyring()
	journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	request := openAIToolLoopRequest{Instructions: "server", UserTurn: "answer", Expectation: openAIToolTestExpectation()}
	firstProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "response-effect", "call-effect", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))},
		{response: openAIToolTerminalResponse("response-terminal", "Completed")},
	}}
	result, err := openAIToolTestCarrier(t, journal, firstProvider, authority, executor, finalizer).Run(ctx, request)
	if err != nil || len(result.OperationIDs) != 1 || executor.total() != 1 {
		t.Fatalf("initial immutable operation missing: result=%+v effects=%d err=%v", result, executor.total(), err)
	}
	operationID := result.OperationIDs[0]
	keyring.mu.Lock()
	oldEffectID, oldEffectVersion := keyring.current.EffectKeyID, keyring.current.EffectVersion
	oldEffectKey := append([]byte(nil), keyring.current.EffectKey...)
	advanced := sha256.Sum256([]byte("openai-tool-test-unauthorized-effect-advance"))
	keyring.keys["effect:managed-effect-unauthorized@2"] = append([]byte(nil), advanced[:]...)
	keyring.current.EffectKeyID, keyring.current.EffectVersion, keyring.current.EffectKey = "managed-effect-unauthorized", "2", append([]byte(nil), advanced[:]...)
	keyring.mu.Unlock()

	secondProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("response-forbidden", "forbidden")}}}
	if _, err := openAIToolTestCarrier(t, journal, secondProvider, authority, executor, finalizer).Run(ctx, request); err == nil || secondProvider.calls != 0 || executor.total() != 1 {
		t.Fatalf("unsigned effect-key advance admitted work: provider=%d effects=%d err=%v", secondProvider.calls, executor.total(), err)
	}
	if journal.state.EffectAuthorityKeyID != oldEffectID || journal.state.EffectAuthorityKeyVersion != oldEffectVersion || len(journal.state.Records) != 1 {
		t.Fatalf("unsigned effect-key advance changed durable authority or operation identity: authority=%s@%s records=%d", journal.state.EffectAuthorityKeyID, journal.state.EffectAuthorityKeyVersion, len(journal.state.Records))
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring); err == nil {
		_ = reopened.Close()
		t.Fatal("journal reopened after current effect-key authority advanced without migration")
	}

	keyring.mu.Lock()
	keyring.current.EffectKeyID, keyring.current.EffectVersion, keyring.current.EffectKey = oldEffectID, oldEffectVersion, oldEffectKey
	keyring.mu.Unlock()
	recovered, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	record, _, err := recovered.Record(ctx, operationID)
	if err != nil || record.OperationID != operationID || len(recovered.state.Records) != 1 || recovered.state.EffectAuthorityKeyID != oldEffectID {
		t.Fatalf("authorized rollback did not preserve exact operation identity: record=%+v records=%d authority=%s err=%v", record, len(recovered.state.Records), recovered.state.EffectAuthorityKeyID, err)
	}
}

func TestOpenAIToolJournalMACAndAEADRotationRejectsStalePreRotationRestore(t *testing.T) {
	ctx := context.Background()
	directory := openAIToolSecureTestDirectory(t)
	highWater := &openAIToolTestHighWater{}
	keyring := newOpenAIToolTestKeyring()
	journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	arguments := map[string]any{"query": "one"}
	entry, expectation := openAIToolTestOperation(t, "answer_memory_question", arguments)
	record, _, _, err := journal.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp", "call"), []openAIResponsesToolInputItem{{Role: "user", Content: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	staleJournal, err := os.ReadFile(journal.path)
	if err != nil {
		t.Fatal(err)
	}
	newMAC := sha256.Sum256([]byte("openai-tool-test-mac-2"))
	newAEAD := sha256.Sum256([]byte("openai-tool-test-aead-2"))
	keyring.mu.Lock()
	keyring.keys["mac:managed-mac-2@2"] = append([]byte(nil), newMAC[:]...)
	keyring.keys["aead:managed-aead-2@2"] = append([]byte(nil), newAEAD[:]...)
	keyring.current.MACKeyID, keyring.current.MACVersion, keyring.current.MACKey = "managed-mac-2", "2", append([]byte(nil), newMAC[:]...)
	keyring.current.AEADKeyID, keyring.current.AEADVersion, keyring.current.AEADKey = "managed-aead-2", "2", append([]byte(nil), newAEAD[:]...)
	keyring.mu.Unlock()
	if err := journal.BeginAttempt(ctx, record.OperationID, "preimage-1"); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatalf("journal did not reopen after managed MAC/AEAD rotation: %v", err)
	}
	rotated, _, err := reopened.Record(ctx, record.OperationID)
	if err != nil || rotated.AEADKeyID != "managed-aead-2" || reopened.state.MACKeyID != "managed-mac-2" {
		t.Fatalf("rotated journal identities drifted: record=%+v state_mac=%s err=%v", rotated, reopened.state.MACKeyID, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, openAIToolJournalFileName), staleJournal, 0o600); err != nil {
		t.Fatal(err)
	}
	if stale, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring); err == nil || !errors.Is(err, errOpenAIToolJournalRollback) {
		if stale != nil {
			_ = stale.Close()
		}
		t.Fatalf("stale pre-rotation journal was accepted: %v", err)
	}
}

func TestOpenAIToolJournalRejectsRotationKeyReuseAndDirectorySwap(t *testing.T) {
	ctx := context.Background()
	t.Run("rotation target reuses MAC secret", func(t *testing.T) {
		keyring := newOpenAIToolTestKeyring()
		journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, keyring)
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		keyring.mu.Lock()
		keyring.keys["effect:managed-effect-2@2"] = append([]byte(nil), keyring.current.MACKey...)
		keyring.mu.Unlock()
		if _, err := journal.RotateAliases(ctx, "managed-effect-2", "2"); err == nil {
			t.Fatal("effect rotation reused a managed MAC secret")
		}
	})

	t.Run("rotation signature must verify before retirement", func(t *testing.T) {
		base := newOpenAIToolTestKeyring()
		keyring := &openAIToolInvalidSignatureKeyring{openAIToolTestKeyring: base}
		journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, keyring)
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		newKeyDigest := sha256.Sum256([]byte("openai-tool-test-invalid-signature-effect-2"))
		base.mu.Lock()
		base.keys["effect:managed-effect-2@2"] = append([]byte(nil), newKeyDigest[:]...)
		base.mu.Unlock()
		if receipt, err := journal.RotateAliases(ctx, "managed-effect-2", "2"); err == nil || receipt.Signature != "" || len(journal.state.RotationReceipts) != 0 {
			t.Fatalf("invalid signed rotation was committed: receipt=%+v receipts=%d err=%v", receipt, len(journal.state.RotationReceipts), err)
		}
		base.mu.Lock()
		base.current.EffectKeyID, base.current.EffectVersion, base.current.EffectKey = "managed-effect-2", "2", append([]byte(nil), newKeyDigest[:]...)
		base.mu.Unlock()
		if err := journal.CanRetireEffectKey(ctx, "managed-effect", "1"); err == nil {
			t.Fatal("invalid rotation signature opened the retirement fence")
		}
	})

	t.Run("retained directory identity", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		moved := directory + "-moved"
		if err := os.Rename(directory, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, _, err := journal.Record(ctx, "missing"); err == nil || !strings.Contains(err.Error(), "directory identity changed") {
			t.Fatalf("journal accepted a swapped directory path: %v", err)
		}
		if _, err := os.Stat(filepath.Join(directory, openAIToolJournalFileName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("journal wrote into the replacement directory: %v", err)
		}
	})

	t.Run("descriptor relative IO survives swap and restore without redirection", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		moved := directory + "-moved"
		if err := os.Rename(directory, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		replacementProbe := filepath.Join(directory, "probe")
		if err := os.WriteFile(replacementProbe, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := atomicWriteOpenAIToolFileAt(journal.directoryFile, "probe", []byte("retained")); err != nil {
			t.Fatal(err)
		}
		if got, err := readSecureOpenAIToolFileAt(journal.directoryFile, "probe"); err != nil || string(got) != "retained" {
			t.Fatalf("retained directory read was redirected: got=%q err=%v", got, err)
		}
		if got, err := os.ReadFile(replacementProbe); err != nil || string(got) != "replacement" {
			t.Fatalf("replacement directory was mutated: got=%q err=%v", got, err)
		}
		if err := unlinkOpenAIToolFileAt(journal.directoryFile, "probe"); err != nil {
			t.Fatal(err)
		}
		if _, err := readSecureOpenAIToolFileAt(journal.directoryFile, "probe"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("descriptor-relative unlink missed retained directory: %v", err)
		}
		if err := os.Remove(replacementProbe); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(directory); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(moved, directory); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("strict duplicate-key decoder", func(t *testing.T) {
		var state openAIToolJournalFile
		if err := decodeOpenAIToolJSONStrict([]byte(`{"version":"one","version":"two"}`), &state); err == nil {
			t.Fatal("journal wrapper accepted duplicate JSON keys")
		}
	})

	t.Run("encrypted envelope preserves exact output bytes", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		highWater := &openAIToolTestHighWater{}
		keyring := newOpenAIToolTestKeyring()
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		arguments := map[string]any{"query": "one"}
		entry, expectation := openAIToolTestOperation(t, "answer_memory_question", arguments)
		exact := json.RawMessage(`{ "type" : "function_call", "status" : "completed", "call_id" : "call", "name" : "answer_memory_question", "arguments" : "{}" }`)
		proposal := openAIToolTestProposal("resp", "call")
		proposal.ExactOutputItems = []json.RawMessage{exact}
		record, _, _, err := journal.Reserve(ctx, entry, arguments, expectation, proposal, []openAIResponsesToolInputItem{{Raw: json.RawMessage(`{ "role" : "user", "content" : "one" }`)}})
		if err != nil {
			t.Fatal(err)
		}
		if err := journal.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		_, envelope, err := reopened.Record(ctx, record.OperationID)
		if err != nil || len(envelope.ExactOutputItems) != 1 || !bytes.Equal(envelope.ExactOutputItems[0], exact) || len(envelope.ManualHistory) != 1 || !bytes.Equal(envelope.ManualHistory[0].Raw, []byte(`{ "role" : "user", "content" : "one" }`)) {
			t.Fatalf("exact encrypted replay bytes drifted: envelope=%+v err=%v", envelope, err)
		}
	})
}

func TestOpenAIToolJournalTamperCollisionPreimageAndRejectedCASTransactions(t *testing.T) {
	ctx := context.Background()
	t.Run("encrypted envelope tamper", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		highWater := &openAIToolTestHighWater{}
		keyring := newOpenAIToolTestKeyring()
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		arguments := map[string]any{"query": "What changed?"}
		entry, expectation := openAIToolTestOperation(t, "answer_memory_question", arguments)
		record, _, _, err := journal.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp", "call"), []openAIResponsesToolInputItem{{Role: "user", Content: "What changed?"}})
		if err != nil {
			t.Fatal(err)
		}
		_ = journal.Close()
		path := filepath.Join(directory, record.EnvelopeFile)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw[len(raw)/2] ^= 1
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring); err == nil {
			_ = reopened.Close()
			t.Fatal("tampered encrypted replay envelope was accepted")
		}
	})

	t.Run("unknown historical AEAD key", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		highWater := &openAIToolTestHighWater{}
		keyring := newOpenAIToolTestKeyring()
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		arguments := map[string]any{"query": "What changed?"}
		entry, expectation := openAIToolTestOperation(t, "answer_memory_question", arguments)
		if _, _, _, err := journal.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp", "call"), []openAIResponsesToolInputItem{{Role: "user", Content: "What changed?"}}); err != nil {
			t.Fatal(err)
		}
		_ = journal.Close()
		keyring.mu.Lock()
		delete(keyring.keys, "aead:managed-aead@1")
		keyring.mu.Unlock()
		if reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring); err == nil {
			_ = reopened.Close()
			t.Fatal("journal with unavailable historical AEAD key was accepted")
		}
	})

	t.Run("provider call correlation collision quarantines", func(t *testing.T) {
		journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		arguments1 := map[string]any{"query": "one"}
		entry, expectation1 := openAIToolTestOperation(t, "answer_memory_question", arguments1)
		record, _, _, err := journal.Reserve(ctx, entry, arguments1, expectation1, openAIToolTestProposal("resp-1", "reused-call"), []openAIResponsesToolInputItem{{Role: "user", Content: "one"}})
		if err != nil {
			t.Fatal(err)
		}
		arguments2 := map[string]any{"query": "two"}
		_, expectation2 := openAIToolTestOperation(t, "answer_memory_question", arguments2)
		if _, _, _, err := journal.Reserve(ctx, entry, arguments2, expectation2, openAIToolTestProposal("resp-2", "reused-call"), []openAIResponsesToolInputItem{{Role: "user", Content: "two"}}); err == nil {
			t.Fatal("reused provider call id with changed semantic effect was accepted")
		}
		quarantined, _, err := journal.Record(ctx, record.OperationID)
		if err != nil || quarantined.State != openAIToolStateQuarantined {
			t.Fatalf("collision was not durably quarantined: %+v err=%v", quarantined, err)
		}
		if len(journal.state.CollisionReceipts) != 1 || journal.state.CollisionReceipts[0].ExistingOperationID != record.OperationID {
			t.Fatalf("collision did not produce a durable body-minimized receipt: %+v", journal.state.CollisionReceipts)
		}
	})

	t.Run("preimage drift and rejected CAS leave no effect reservation", func(t *testing.T) {
		highWater := &openAIToolTestHighWater{}
		journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", highWater, newOpenAIToolTestKeyring())
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		arguments := map[string]any{"query": "one"}
		entry, expectation := openAIToolTestOperation(t, "answer_memory_question", arguments)
		record, _, _, err := journal.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp", "call"), []openAIResponsesToolInputItem{{Role: "user", Content: "one"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := journal.BeginAttempt(ctx, record.OperationID, "changed-preimage"); err == nil {
			t.Fatal("changed preimage reached effect execution")
		}
		unchanged, _, _ := journal.Record(ctx, record.OperationID)
		if unchanged.AttemptCount != 0 || unchanged.State != openAIToolStateReserved {
			t.Fatalf("preimage failure changed operation: %+v", unchanged)
		}
		highWater.rejectOnce = true
		arguments2 := map[string]any{"query": "two"}
		_, expectation2 := openAIToolTestOperation(t, "answer_memory_question", arguments2)
		if _, _, _, err := journal.Reserve(ctx, entry, arguments2, expectation2, openAIToolTestProposal("resp-2", "call-2"), []openAIResponsesToolInputItem{{Role: "user", Content: "two"}}); !errors.Is(err, errOpenAIToolJournalConflict) {
			t.Fatalf("rejected journal CAS did not fail closed: %v", err)
		}
		if len(journal.state.Records) != 1 || len(journal.state.Aliases) != 1 {
			t.Fatalf("rejected CAS left a reservation: records=%d aliases=%d", len(journal.state.Records), len(journal.state.Aliases))
		}
	})

	t.Run("semantic replay rejects authority and preimage drift", func(t *testing.T) {
		journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		arguments := map[string]any{"query": "one"}
		entry, expectation := openAIToolTestOperation(t, "answer_memory_question", arguments)
		if _, _, _, err := journal.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp-1", "call-1"), []openAIResponsesToolInputItem{{Role: "user", Content: "one"}}); err != nil {
			t.Fatal(err)
		}
		driftedExpectation := expectation
		driftedExpectation.SessionDigest = "different-session"
		if _, _, _, err := journal.Reserve(ctx, entry, arguments, driftedExpectation, openAIToolTestProposal("resp-2", "call-2"), []openAIResponsesToolInputItem{{Role: "user", Content: "one"}}); err == nil {
			t.Fatal("semantic replay accepted a different authority expectation")
		}
		driftedPreimage := openAIToolTestProposal("resp-3", "call-3")
		driftedPreimage.PreimageDigest = "different-preimage"
		if _, _, _, err := journal.Reserve(ctx, entry, arguments, expectation, driftedPreimage, []openAIResponsesToolInputItem{{Role: "user", Content: "one"}}); err == nil {
			t.Fatal("semantic replay accepted a different preimage")
		}
		if len(journal.state.Records) != 1 {
			t.Fatalf("drifted semantic replay changed the immutable operation set: %d", len(journal.state.Records))
		}
	})

	t.Run("advanced high-water poisons instead of rolling back memory", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		highWater := &openAIToolTestHighWater{}
		keyring := newOpenAIToolTestKeyring()
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		highWater.mu.Lock()
		highWater.afterCAS = func() {
			if err := os.Chmod(journal.path, 0o400); err != nil {
				panic(err)
			}
		}
		highWater.mu.Unlock()
		arguments := map[string]any{"query": "one"}
		entry, expectation := openAIToolTestOperation(t, "answer_memory_question", arguments)
		_, _, _, err = journal.Reserve(ctx, entry, arguments, expectation, openAIToolTestProposal("resp", "call"), []openAIResponsesToolInputItem{{Role: "user", Content: "one"}})
		if !errors.Is(err, errOpenAIToolJournalCommittedUnverified) || journal.poisoned == nil || len(journal.state.Records) != 1 {
			t.Fatalf("post-commit verification failure rolled back or remained usable: records=%d poisoned=%v err=%v", len(journal.state.Records), journal.poisoned, err)
		}
		if err := os.Chmod(journal.path, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := journal.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		if len(reopened.state.Records) != 1 {
			t.Fatalf("restart lost a committed generation: records=%d", len(reopened.state.Records))
		}
	})
}
