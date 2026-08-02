package e10evidence

import (
	"crypto/ed25519"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSerializedReceiptsReverifyEveryOriginalSignedEnvelope(t *testing.T) {
	fixture := newSignerFixture(t)
	registry, registryRaw := signedRegistry(t, fixture)
	registrySignature := ed25519.Sign(fixture.registryPrivate, registryRaw)

	_, registryReceipt, err := VerifyTargetRegistryReceipt(registryRaw, registrySignature, fixture.registryPublic, fixture.approved)
	if err != nil {
		t.Fatal(err)
	}
	registryEncoded := mustEncodeReceipt(t, registryReceipt)
	if _, err := ReverifyEncodedTargetRegistryReceipt(registryEncoded, registryRaw, registrySignature, fixture.registryPublic, fixture.approved); err != nil {
		t.Fatalf("registry receipt did not reverify: %v", err)
	}
	if _, err := ReverifyEncodedTargetRegistryReceipt(append(registryEncoded, '\n'), registryRaw, registrySignature, fixture.registryPublic, fixture.approved); err == nil || !strings.Contains(err.Error(), "exactly match") {
		t.Fatalf("format-drifted registry receipt accepted: %v", err)
	}
	tamperedRegistryReceipt := append([]byte(nil), registryEncoded...)
	tamperedRegistryReceipt = []byte(strings.Replace(string(tamperedRegistryReceipt), `"itemCount": 33`, `"itemCount": 34`, 1))
	if _, err := ReverifyEncodedTargetRegistryReceipt(tamperedRegistryReceipt, registryRaw, registrySignature, fixture.registryPublic, fixture.approved); err == nil || !strings.Contains(err.Error(), "exactly match") {
		t.Fatalf("tampered registry receipt accepted: %v", err)
	}
	badRegistrySignature := append([]byte(nil), registrySignature...)
	badRegistrySignature[0] ^= 1
	if _, err := ReverifyEncodedTargetRegistryReceipt(registryEncoded, registryRaw, badRegistrySignature, fixture.registryPublic, fixture.approved); err == nil {
		t.Fatal("serialized registry receipt replaced a failed source signature")
	}

	manifest := validCorpus("composer_dictation", 250, fixture, registryRaw)
	corpusRaw := mustJSON(t, manifest)
	operatorSignature := ed25519.Sign(fixture.operatorPrivate, corpusRaw)
	reviewerSignature := ed25519.Sign(fixture.reviewerPrivate, corpusRaw)
	_, corpusReceipt, err := VerifyCorpusReceipt(corpusRaw, registryRaw, registrySignature, fixture.registryPublic, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved)
	if err != nil {
		t.Fatal(err)
	}
	corpusEncoded := mustEncodeReceipt(t, corpusReceipt)
	if _, err := ReverifyEncodedCorpusReceipt(corpusEncoded, corpusRaw, registryRaw, registrySignature, fixture.registryPublic, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved); err != nil {
		t.Fatalf("corpus receipt did not reverify: %v", err)
	}
	badOperatorSignature := append([]byte(nil), operatorSignature...)
	badOperatorSignature[0] ^= 1
	if _, err := ReverifyEncodedCorpusReceipt(corpusEncoded, corpusRaw, registryRaw, registrySignature, fixture.registryPublic, badOperatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved); err == nil {
		t.Fatal("serialized corpus receipt replaced a failed operator signature")
	}

	packet := validPilotPacket(fixture, registryRaw)
	pilotRaw := mustJSON(t, packet)
	operatorSignature = ed25519.Sign(fixture.operatorPrivate, pilotRaw)
	reviewerSignature = ed25519.Sign(fixture.reviewerPrivate, pilotRaw)
	_, pilotReceipt, err := VerifyPilotReceipt(pilotRaw, registryRaw, registrySignature, fixture.registryPublic, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved)
	if err != nil {
		t.Fatal(err)
	}
	pilotEncoded := mustEncodeReceipt(t, pilotReceipt)
	if _, err := ReverifyEncodedPilotReceipt(pilotEncoded, pilotRaw, registryRaw, registrySignature, fixture.registryPublic, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved); err != nil {
		t.Fatalf("pilot receipt did not reverify: %v", err)
	}

	matrix := validExternalMatrix("worker_orchestrator", registry, fixture, registryRaw)
	matrixRaw := mustJSON(t, matrix)
	operatorSignature = ed25519.Sign(fixture.operatorPrivate, matrixRaw)
	reviewerSignature = ed25519.Sign(fixture.reviewerPrivate, matrixRaw)
	_, matrixReceipt, err := VerifyMatrixReceipt(matrixRaw, registryRaw, registrySignature, fixture.registryPublic, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved)
	if err != nil {
		t.Fatal(err)
	}
	matrixEncoded := mustEncodeReceipt(t, matrixReceipt)
	if _, err := ReverifyEncodedMatrixReceipt(matrixEncoded, matrixRaw, registryRaw, registrySignature, fixture.registryPublic, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved); err != nil {
		t.Fatalf("matrix receipt did not reverify: %v", err)
	}
	wrongSource := append([]byte(nil), matrixRaw...)
	wrongSource[len(wrongSource)-1] ^= 1
	if _, err := ReverifyEncodedMatrixReceipt(matrixEncoded, wrongSource, registryRaw, registrySignature, fixture.registryPublic, operatorSignature, fixture.operatorPublic, reviewerSignature, fixture.reviewerPublic, fixture.approved); err == nil {
		t.Fatal("serialized matrix receipt replaced an invalid source envelope")
	}
}

func TestValidationReceiptConsumerIsDurablyOneUse(t *testing.T) {
	fixture := newSignerFixture(t)
	registry := validRegistry(fixture)
	registryRaw := mustJSON(t, registry)
	registrySignature := ed25519.Sign(fixture.registryPrivate, registryRaw)
	_, verified, err := VerifyTargetRegistryReceipt(registryRaw, registrySignature, fixture.registryPublic, fixture.approved)
	if err != nil {
		t.Fatal(err)
	}
	encoded := mustEncodeReceipt(t, verified)
	reverified, err := ReverifyEncodedTargetRegistryReceipt(encoded, registryRaw, registrySignature, fixture.registryPublic, fixture.approved)
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(directory, "consumed.jsonl")
	first, err := OpenValidationReceiptConsumer(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenValidationReceiptConsumer(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wait sync.WaitGroup
	for index := 0; index < 24; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			consumer := first
			if index%2 == 1 {
				consumer = second
			}
			if consumeErr := consumer.Consume(reverified); consumeErr == nil {
				successes.Add(1)
			}
		}(index)
	}
	wait.Wait()
	if successes.Load() != 1 {
		t.Fatalf("same receipt consumed %d times; want exactly one", successes.Load())
	}
	restarted, err := OpenValidationReceiptConsumer(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Consume(reverified); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("replay after restart accepted: %v", err)
	}
	changedRoots := fixture.roots
	changedRoots.TrustRootID = "approved-e10-roots-002"
	changedRootsRaw := mustJSON(t, changedRoots)
	changedApproved, err := LoadApprovedTrustRoots(changedRootsRaw, RegistryDigest(changedRootsRaw))
	if err != nil {
		t.Fatal(err)
	}
	_, changedAuthorityReceipt, err := VerifyTargetRegistryReceipt(registryRaw, registrySignature, fixture.registryPublic, changedApproved)
	if err != nil {
		t.Fatal(err)
	}
	changedEncoded := mustEncodeReceipt(t, changedAuthorityReceipt)
	if string(changedEncoded) == string(encoded) {
		t.Fatal("changed trust authority did not produce a distinct serialized receipt")
	}
	changedReverified, err := ReverifyEncodedTargetRegistryReceipt(changedEncoded, registryRaw, registrySignature, fixture.registryPublic, changedApproved)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Consume(changedReverified); err == nil || !strings.Contains(err.Error(), "source envelope was already consumed") {
		t.Fatalf("same source envelope under changed trust-root metadata replayed: %v", err)
	}
	if err := restarted.Consume(VerifiedValidationReceipt{}); err == nil {
		t.Fatalf("hand-authored receipt capability accepted: %v", err)
	}
}

func TestValidationReceiptConsumerRollsBackShortWriteAndRejectsCorruption(t *testing.T) {
	fixture := newSignerFixture(t)
	registry := validRegistry(fixture)
	registryRaw := mustJSON(t, registry)
	_, verified, err := VerifyTargetRegistryReceipt(registryRaw, ed25519.Sign(fixture.registryPrivate, registryRaw), fixture.registryPublic, fixture.approved)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(directory, "consumed.jsonl")
	consumer, err := OpenValidationReceiptConsumer(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	realSync := consumer.sync
	consumer.sync = func(*os.File) error { return errors.New("forced sync failure") }
	if err := consumer.Consume(verified); err == nil || !strings.Contains(err.Error(), "forced sync failure") {
		t.Fatalf("sync failure did not fail closed: %v", err)
	}
	consumer.sync = realSync
	realWrite := consumer.write
	consumer.write = func(file *os.File, value []byte) (int, error) {
		return file.Write(value[:len(value)/2])
	}
	if err := consumer.Consume(verified); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write did not fail closed: %v", err)
	}
	consumer.write = realWrite
	if err := consumer.Consume(verified); err != nil {
		t.Fatalf("rolled-back receipt could not be retried: %v", err)
	}
	if err := os.WriteFile(ledgerPath, []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenValidationReceiptConsumer(ledgerPath); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("corrupt replay ledger accepted: %v", err)
	}
}

func mustEncodeReceipt(t *testing.T, receipt VerifiedValidationReceipt) []byte {
	t.Helper()
	encoded, err := EncodeReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
