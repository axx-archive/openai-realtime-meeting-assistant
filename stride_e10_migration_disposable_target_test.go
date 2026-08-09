package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type strideE10LostResponseTarget struct {
	*StrideE10DisposableMigrationTarget
	lost bool
}

type strideE10MalformedReceiptTarget struct {
	*StrideE10DisposableMigrationTarget
}

func (t *strideE10MalformedReceiptTarget) ApplyStrideE10Migration(ctx context.Context, request StrideE10MigrationWriteRequest) (StrideE10MigrationWriteObservation, error) {
	observation, err := t.StrideE10DisposableMigrationTarget.ApplyStrideE10Migration(ctx, request)
	if err == nil {
		observation.Receipt.AfterHighWater++
	}
	return observation, err
}

func (t *strideE10LostResponseTarget) ApplyStrideE10Migration(ctx context.Context, request StrideE10MigrationWriteRequest) (StrideE10MigrationWriteObservation, error) {
	observation, err := t.StrideE10DisposableMigrationTarget.ApplyStrideE10Migration(ctx, request)
	if err == nil && t.lost {
		t.lost = false
		return StrideE10MigrationWriteObservation{}, errors.New("committed target response lost")
	}
	return observation, err
}

func TestStrideE10DisposableTargetLostResponseRestartReadsExactFifteen(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	keys := strideE10TestKeys("disposable-target-key", 1)
	targetPath := filepath.Join(directory, "canonical-target.json")
	target := &strideE10LostResponseTarget{StrideE10DisposableMigrationTarget: NewStrideE10DisposableMigrationTarget(targetPath, 400, keys), lost: true}
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), target, keys)
	if _, err := RunStrideE10MigrationRehearsal(ctx, config); err == nil {
		t.Fatal("expected ambiguous committed response")
	}

	// A fresh adapter simulates restart: replay must load its durable operation
	// receipt and must not advance the target another 15 rows.
	restarted := NewStrideE10DisposableMigrationTarget(targetPath, 400, keys)
	config.Writer = restarted
	result, err := RunStrideE10MigrationRehearsal(ctx, config)
	if err != nil {
		t.Fatalf("restart replay: %v", err)
	}
	readback, err := restarted.ReadStrideE10MigrationTarget(ctx, "")
	if err != nil || readback.HighWater != 415 || len(readback.Rows) != 15 || result.Receipt.SourceHighWater != 400 || result.Receipt.TargetHighWater != 415 {
		t.Fatalf("durable replay/readback drift: readback=%+v receipt=%+v err=%v", readback, result.Receipt, err)
	}
	for _, row := range readback.Rows {
		if len(row.Body) == 0 {
			t.Fatal("target row omitted exact canonical body")
		}
	}

	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 1
	if err := os.WriteFile(targetPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ReadStrideE10MigrationTarget(ctx, ""); err == nil {
		t.Fatal("managed-MAC target tamper accepted")
	}
}

func TestStrideE10DisposableTargetRejectsPartialManifestAndRestoresBeforeImage(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	keys := strideE10TestKeys("disposable-rollback-key", 1)
	target := NewStrideE10DisposableMigrationTarget(filepath.Join(directory, "target.json"), 900, keys)
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), target, keys)
	result, err := RunStrideE10MigrationRehearsal(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	var backup strideE10MigrationBackup
	if err := strideE10UnmarshalSigned(ctx, keys, mustReadStrideE10Test(t, config.BackupPath), &backup); err != nil {
		t.Fatal(err)
	}
	request := StrideE10MigrationWriteRequest{OperationID: backup.WriterOperationID, MigrationDigest: backup.Input.MigrationDigest, SourceDigest: backup.SourceSnapshotDigest, BackupIdentityDigest: backup.Input.BackupIdentityDigest, ExpectedDelta: StrideE10MigrationExpectedTargetDelta, Manifest: backup.TargetManifest}
	partial := request
	partial.OperationID += "-partial"
	partial.Manifest.Rows = partial.Manifest.Rows[:14]
	partial.Manifest.Digest = strideE10CanonicalTargetManifestDigest(partial.Manifest)
	if _, err := target.ApplyStrideE10Migration(ctx, partial); err == nil {
		t.Fatal("partial canonical manifest accepted")
	}
	rollbackRequest := StrideE10MigrationRollbackRequest{OperationID: request.OperationID, MigrationDigest: request.MigrationDigest, SourceDigest: request.SourceDigest, BackupIdentityDigest: request.BackupIdentityDigest, ManifestDigest: request.Manifest.Digest, WriterReceiptDigest: backup.WriterReceipt.ReceiptDigest, ExpectedBeforeHighWater: backup.WriterReceipt.BeforeHighWater, ExpectedBeforeSnapshotDigest: backup.WriterReceipt.BeforeSnapshotDigest}
	target.persistOverride = func(context.Context, strideE10DisposableTargetState) error {
		return errors.New("injected durable rollback write failure")
	}
	if _, err := target.RollbackStrideE10Migration(ctx, rollbackRequest); err == nil {
		t.Fatal("expected injected rollback persistence failure")
	}
	target.persistOverride = nil
	stillApplied, err := target.ReadStrideE10MigrationTarget(ctx, request.OperationID)
	if err != nil || stillApplied.HighWater != 915 || len(stillApplied.Rows) != 15 {
		t.Fatalf("failed rollback partially mutated durable target: %+v %v", stillApplied, err)
	}
	rollback, err := target.RollbackStrideE10Migration(ctx, rollbackRequest)
	if err != nil {
		t.Fatal(err)
	}
	readback, err := target.ReadStrideE10MigrationTarget(ctx, request.OperationID)
	if err != nil || readback.HighWater != 900 || len(readback.Rows) != 0 || rollback.RestoredDigest != readback.SnapshotDigest || result.Receipt.TargetHighWater != 915 {
		t.Fatalf("exact before-image restore failed: rollback=%+v readback=%+v err=%v", rollback, readback, err)
	}
	second, err := target.RollbackStrideE10Migration(ctx, rollbackRequest)
	if err != nil || second != rollback {
		t.Fatalf("rollback receipt is not durable/idempotent: %+v %v", second, err)
	}
}

func TestStrideE10DisposableTargetMalformedCommittedReceiptIsRecovered(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	keys := strideE10TestKeys("malformed-receipt-key", 1)
	targetPath := filepath.Join(directory, "target.json")
	target := &strideE10MalformedReceiptTarget{StrideE10DisposableMigrationTarget: NewStrideE10DisposableMigrationTarget(targetPath, 700, keys)}
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), target, keys)
	if _, err := RunStrideE10MigrationRehearsal(ctx, config); err == nil {
		t.Fatal("malformed committed writer receipt was accepted")
	}
	restarted := NewStrideE10DisposableMigrationTarget(targetPath, 700, keys)
	readback, err := restarted.ReadStrideE10MigrationTarget(ctx, "")
	if err != nil || readback.HighWater != 700 || len(readback.Rows) != 0 {
		t.Fatalf("untrusted committed apply was not exactly recovered: %+v %v", readback, err)
	}
}

func TestStrideE10DisposableTargetRotationResealsForRetiredKeyReplayReadRollback(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	oldKeys := strideE10TestKeys("target-old-key", 1)
	targetPath := filepath.Join(directory, "target.json")
	target := NewStrideE10DisposableMigrationTarget(targetPath, 500, oldKeys)
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), target, oldKeys)
	if _, err := RunStrideE10MigrationRehearsal(ctx, config); err != nil {
		t.Fatal(err)
	}
	newKey := StrideE10MigrationMACKey{ID: "target-new-key", Version: 2, Secret: []byte("target-new-key/target-new-key/target-new-key/target-new-key/")}
	rotating := &strideE10TestKeyring{current: newKey, keys: map[string]StrideE10MigrationMACKey{"target-old-key": oldKeys.current, "target-new-key": newKey}}
	config.Keys = rotating
	config.Writer = NewStrideE10DisposableMigrationTarget(targetPath, 500, rotating)
	if _, err := RunStrideE10MigrationRehearsal(ctx, config); err != nil {
		t.Fatalf("rotation replay: %v", err)
	}
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope strideE10MigrationEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.KeyID != newKey.ID || envelope.KeyVersion != newKey.Version {
		t.Fatalf("target was not resealed to current key: %+v %v", envelope, err)
	}
	retired := &strideE10TestKeyring{current: newKey, keys: map[string]StrideE10MigrationMACKey{"target-new-key": newKey}}
	fresh := NewStrideE10DisposableMigrationTarget(targetPath, 500, retired)
	readback, err := fresh.ReadStrideE10MigrationTarget(ctx, "")
	if err != nil || readback.HighWater != 515 || len(readback.Rows) != 15 {
		t.Fatalf("fresh read after old-key retirement: %+v %v", readback, err)
	}
	var backup strideE10MigrationBackup
	if err := strideE10UnmarshalSigned(ctx, retired, mustReadStrideE10Test(t, config.BackupPath), &backup); err != nil {
		t.Fatal(err)
	}
	receipt := *backup.WriterReceipt
	rollbackRequest := StrideE10MigrationRollbackRequest{OperationID: backup.WriterOperationID, MigrationDigest: backup.Input.MigrationDigest, SourceDigest: backup.SourceSnapshotDigest, BackupIdentityDigest: backup.Input.BackupIdentityDigest, ManifestDigest: backup.TargetManifest.Digest, WriterReceiptDigest: receipt.ReceiptDigest, ExpectedBeforeHighWater: receipt.BeforeHighWater, ExpectedBeforeSnapshotDigest: receipt.BeforeSnapshotDigest}
	if _, err := fresh.RollbackStrideE10Migration(ctx, rollbackRequest); err != nil {
		t.Fatalf("fresh rollback after old-key retirement: %v", err)
	}
	request := StrideE10MigrationWriteRequest{OperationID: backup.WriterOperationID, MigrationDigest: backup.Input.MigrationDigest, SourceDigest: backup.SourceSnapshotDigest, BackupIdentityDigest: backup.Input.BackupIdentityDigest, ExpectedDelta: StrideE10MigrationExpectedTargetDelta, Manifest: backup.TargetManifest}
	if observation, err := fresh.ApplyStrideE10Migration(ctx, request); err != nil || observation.Receipt != receipt {
		t.Fatalf("fresh replay after old-key retirement: %+v %v", observation, err)
	}
}

func TestStrideE10CoordinatedRestoreFencesSourceUntilTargetRollback(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	keys := strideE10TestKeys("coordinated-restore-key", 1)
	target := NewStrideE10DisposableMigrationTarget(filepath.Join(directory, "target.json"), 800, keys)
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), target, keys)
	if _, err := RunStrideE10MigrationRehearsal(ctx, config); err != nil {
		t.Fatal(err)
	}
	backup := mustReadStrideE10Test(t, config.BackupPath)
	accounts.mu.Lock()
	accounts.users = map[string]*userAccount{"sentinel@example.com": {Email: "sentinel@example.com", Name: "Sentinel"}}
	sentinelAccounts, _ := json.MarshalIndent([]*userAccount{accounts.users["sentinel@example.com"]}, "", "  ")
	if err := os.WriteFile(accounts.path, sentinelAccounts, 0o600); err != nil {
		accounts.mu.Unlock()
		t.Fatal(err)
	}
	accounts.mu.Unlock()
	sessions.mu.Lock()
	sessions.sessions = map[string]sessionRecord{}
	sentinelSessions := []byte("{}")
	if err := os.WriteFile(sessions.path, sentinelSessions, 0o600); err != nil {
		sessions.mu.Unlock()
		t.Fatal(err)
	}
	sessions.mu.Unlock()
	target.persistOverride = func(context.Context, strideE10DisposableTargetState) error {
		return errors.New("injected target rollback persistence failure")
	}
	statePath := filepath.Join(directory, "restored-state.json")
	if err := RestoreStrideE10MigrationRehearsal(ctx, statePath, backup, keys, target, NewStrideE10LocalMigrationRestorer(accounts, sessions)); err == nil {
		t.Fatal("target rollback failure did not stop coordinated restore")
	}
	accountAfter, _ := os.ReadFile(accounts.path)
	sessionAfter, _ := os.ReadFile(sessions.path)
	readback, readErr := target.ReadStrideE10MigrationTarget(ctx, "")
	if !bytes.Equal(accountAfter, sentinelAccounts) || !bytes.Equal(sessionAfter, sentinelSessions) || readErr != nil || readback.HighWater != 815 || len(readback.Rows) != 15 {
		t.Fatalf("target-first restore fence failed: target=%+v readErr=%v", readback, readErr)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("target rollback failure published restored state")
	}
	target.persistOverride = nil
	restorer := NewStrideE10LocalMigrationRestorer(accounts, sessions).(*strideE10LocalMigrationRestorer)
	restorer.write = func(path string, body []byte, mode os.FileMode) error {
		if path == sessions.path {
			return errors.New("injected source restore failure after target rollback")
		}
		return writeFileAtomicallyDurable(path, body, mode)
	}
	if err := RestoreStrideE10MigrationRehearsal(ctx, statePath, backup, keys, target, restorer); err == nil {
		t.Fatal("source restore failure was accepted")
	}
	readback, readErr = target.ReadStrideE10MigrationTarget(ctx, "")
	accountAfter, _ = os.ReadFile(accounts.path)
	sessionAfter, _ = os.ReadFile(sessions.path)
	if readErr != nil || readback.HighWater != 815 || len(readback.Rows) != 15 || !bytes.Equal(accountAfter, sentinelAccounts) || !bytes.Equal(sessionAfter, sentinelSessions) {
		t.Fatalf("source restore failure did not compensate exact applied target/source images: %+v %v", readback, readErr)
	}
	restorer.write = writeFileAtomicallyDurable
	if err := RestoreStrideE10MigrationRehearsal(ctx, statePath, backup, keys, target, restorer); err != nil {
		t.Fatalf("coordinated restore retry: %v", err)
	}
	readback, readErr = target.ReadStrideE10MigrationTarget(ctx, "")
	if readErr != nil || readback.HighWater != 800 || len(readback.Rows) != 0 {
		t.Fatalf("coordinated restore did not verify exact target before-image: %+v %v", readback, readErr)
	}
	var signedBackup strideE10MigrationBackup
	if err := strideE10UnmarshalSigned(ctx, keys, backup, &signedBackup); err != nil {
		t.Fatal(err)
	}
	restoredAccounts, _ := os.ReadFile(accounts.path)
	restoredSessions, _ := os.ReadFile(sessions.path)
	if !bytes.Equal(restoredAccounts, signedBackup.Input.SourceAccountFileBody) || !bytes.Equal(restoredSessions, signedBackup.Input.SourceSessionFileBody) {
		t.Fatal("coordinated restore did not atomically restore exact source authority bytes")
	}
}

func TestStrideE10CoordinatedRestoreJournalResumesEveryAbruptBoundary(t *testing.T) {
	defer func() { strideE10MigrationRestorePhaseHook = nil }()
	for _, crashPhase := range []string{
		strideE10RestoreTargetRollbackStarted,
		strideE10RestoreTargetRollbackVerified,
		strideE10RestoreAccountsWritten,
		strideE10RestoreSessionsWritten,
		strideE10RestoreSourceVerified,
		strideE10RestoreStateStarted,
		strideE10RestoreStateFileWritten,
		strideE10RestoreCompleted,
	} {
		t.Run(crashPhase, func(t *testing.T) {
			ctx := context.Background()
			directory := t.TempDir()
			accounts, sessions := strideE10TestAuthorities(t, directory)
			keys := strideE10TestKeys("restore-journal-"+crashPhase, 1)
			targetPath := filepath.Join(directory, "target.json")
			target := NewStrideE10DisposableMigrationTarget(targetPath, 1200, keys)
			config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), target, keys)
			if _, err := RunStrideE10MigrationRehearsal(ctx, config); err != nil {
				t.Fatal(err)
			}
			backupRaw := mustReadStrideE10Test(t, config.BackupPath)
			var backup strideE10MigrationBackup
			if err := strideE10UnmarshalSigned(ctx, keys, backupRaw, &backup); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(accounts.path, []byte(`[{"email":"sentinel@example.com","name":"Sentinel"}]`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sessions.path, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config.StatePath, []byte("corrupt pre-restore state"), 0o600); err != nil {
				t.Fatal(err)
			}
			corruptAccounts, err := newUserAccountStore(accounts.path)
			if err != nil {
				t.Fatal(err)
			}
			corruptSessions := newSessionStore(sessions.path)
			strideE10MigrationRestorePhaseHook = func(phase string) error {
				if phase == crashPhase {
					return errStrideE10MigrationAbruptRestart
				}
				return nil
			}
			if err := RestoreStrideE10MigrationRehearsal(ctx, config.StatePath, backupRaw, keys, target, NewStrideE10LocalMigrationRestorer(corruptAccounts, corruptSessions)); !errors.Is(err, errStrideE10MigrationAbruptRestart) {
				t.Fatalf("crash phase %s err=%v", crashPhase, err)
			}
			strideE10MigrationRestorePhaseHook = nil
			freshAccounts, err := newUserAccountStore(accounts.path)
			if err != nil {
				t.Fatal(err)
			}
			freshSessions := newSessionStore(sessions.path)
			freshTarget := NewStrideE10DisposableMigrationTarget(targetPath, 1200, keys)
			if err := RestoreStrideE10MigrationRehearsal(ctx, config.StatePath, backupRaw, keys, freshTarget, NewStrideE10LocalMigrationRestorer(freshAccounts, freshSessions)); err != nil {
				t.Fatalf("fresh restart resume after %s: %v", crashPhase, err)
			}
			accountBody, _ := os.ReadFile(accounts.path)
			sessionBody, _ := os.ReadFile(sessions.path)
			readback, readErr := freshTarget.ReadStrideE10MigrationTarget(ctx, "")
			if !bytes.Equal(accountBody, backup.Input.SourceAccountFileBody) || !bytes.Equal(sessionBody, backup.Input.SourceSessionFileBody) || readErr != nil || readback.HighWater != 1200 || len(readback.Rows) != 0 {
				t.Fatalf("mixed restore after %s: target=%+v err=%v", crashPhase, readback, readErr)
			}
			var state strideE10MigrationState
			stateRaw, err := os.ReadFile(config.StatePath)
			if err != nil || strideE10UnmarshalSigned(ctx, keys, stateRaw, &state) != nil || validateStrideE10PersistedState(state) != nil {
				t.Fatalf("restored state invalid after %s: %v", crashPhase, err)
			}
			var journal strideE10MigrationRestoreJournal
			journalRaw, err := os.ReadFile(config.StatePath + ".restore-journal")
			if err != nil || strideE10UnmarshalSigned(ctx, keys, journalRaw, &journal) != nil || journal.Phase != strideE10RestoreCompleted || validateStrideE10MigrationRestoreJournal(journal, backup, backupRaw) != nil {
				t.Fatalf("completed journal invalid after %s: %+v %v", crashPhase, journal, err)
			}
		})
	}
}

func TestStrideE10CoordinatedRestoreJournalTamperStopsBeforeMutation(t *testing.T) {
	defer func() { strideE10MigrationRestorePhaseHook = nil }()
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	keys := strideE10TestKeys("restore-journal-tamper", 1)
	targetPath := filepath.Join(directory, "target.json")
	target := NewStrideE10DisposableMigrationTarget(targetPath, 1400, keys)
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), target, keys)
	if _, err := RunStrideE10MigrationRehearsal(ctx, config); err != nil {
		t.Fatal(err)
	}
	backupRaw := mustReadStrideE10Test(t, config.BackupPath)
	strideE10MigrationRestorePhaseHook = func(phase string) error {
		if phase == strideE10RestoreTargetRollbackStarted {
			return errStrideE10MigrationAbruptRestart
		}
		return nil
	}
	if err := RestoreStrideE10MigrationRehearsal(ctx, config.StatePath, backupRaw, keys, target, NewStrideE10LocalMigrationRestorer(accounts, sessions)); !errors.Is(err, errStrideE10MigrationAbruptRestart) {
		t.Fatal(err)
	}
	strideE10MigrationRestorePhaseHook = nil
	journalPath := config.StatePath + ".restore-journal"
	raw, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 1
	if err := os.WriteFile(journalPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreStrideE10MigrationRehearsal(ctx, config.StatePath, backupRaw, keys, NewStrideE10DisposableMigrationTarget(targetPath, 1400, keys), NewStrideE10LocalMigrationRestorer(accounts, sessions)); err == nil {
		t.Fatal("tampered restore journal was accepted")
	}
	readback, err := target.ReadStrideE10MigrationTarget(ctx, "")
	if err != nil || readback.HighWater != 1415 || len(readback.Rows) != 15 {
		t.Fatalf("journal tamper mutated target: %+v %v", readback, err)
	}
}

func TestStrideE10CoordinatedRestoreJournalResealsAcrossCrashCompletionAndKeyRetirement(t *testing.T) {
	defer func() { strideE10MigrationRestorePhaseHook = nil }()
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	oldKeys := strideE10TestKeys("journal-old", 1)
	targetPath := filepath.Join(directory, "target.json")
	target := NewStrideE10DisposableMigrationTarget(targetPath, 1600, oldKeys)
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), target, oldKeys)
	if _, err := RunStrideE10MigrationRehearsal(ctx, config); err != nil {
		t.Fatal(err)
	}
	backupRaw := mustReadStrideE10Test(t, config.BackupPath)
	strideE10MigrationRestorePhaseHook = func(phase string) error {
		if phase == strideE10RestoreAccountsWritten {
			return errStrideE10MigrationAbruptRestart
		}
		return nil
	}
	if err := RestoreStrideE10MigrationRehearsal(ctx, config.StatePath, backupRaw, oldKeys, target, NewStrideE10LocalMigrationRestorer(accounts, sessions)); !errors.Is(err, errStrideE10MigrationAbruptRestart) {
		t.Fatalf("expected abrupt restore: %v", err)
	}
	strideE10MigrationRestorePhaseHook = nil
	newKey := StrideE10MigrationMACKey{ID: "journal-new", Version: 2, Secret: []byte("journal-new/journal-new/journal-new/journal-new/")}
	rotating := &strideE10TestKeyring{current: newKey, keys: map[string]StrideE10MigrationMACKey{"journal-old": oldKeys.current, "journal-new": newKey}}
	freshTarget := NewStrideE10DisposableMigrationTarget(targetPath, 1600, rotating)
	if err := RestoreStrideE10MigrationRehearsal(ctx, config.StatePath, backupRaw, rotating, freshTarget, NewStrideE10LocalMigrationRestorer(accounts, sessions)); err != nil {
		t.Fatalf("rotation resume: %v", err)
	}
	journalRaw := mustReadStrideE10Test(t, config.StatePath+".restore-journal")
	var envelope strideE10MigrationEnvelope
	if json.Unmarshal(journalRaw, &envelope) != nil || envelope.KeyID != newKey.ID || envelope.KeyVersion != newKey.Version {
		t.Fatalf("in-progress journal not resealed: %+v", envelope)
	}
	var backup strideE10MigrationBackup
	if err := strideE10UnmarshalSigned(ctx, rotating, backupRaw, &backup); err != nil {
		t.Fatal(err)
	}
	resealedBackup, err := strideE10MarshalSigned(ctx, rotating, backup)
	if err != nil {
		t.Fatal(err)
	}
	retired := &strideE10TestKeyring{current: newKey, keys: map[string]StrideE10MigrationMACKey{"journal-new": newKey}}
	if err := RestoreStrideE10MigrationRehearsal(ctx, config.StatePath, resealedBackup, retired, NewStrideE10DisposableMigrationTarget(targetPath, 1600, retired), NewStrideE10LocalMigrationRestorer(accounts, sessions)); err != nil {
		t.Fatalf("completed journal after old-key retirement: %v", err)
	}
	journalRaw = mustReadStrideE10Test(t, config.StatePath+".restore-journal")
	if err := strideE10UnmarshalSigned(ctx, retired, journalRaw, &strideE10MigrationRestoreJournal{}); err != nil {
		t.Fatalf("completed journal did not remain current-key verifiable: %v", err)
	}
	wrong := strideE10TestKeys("journal-wrong", 1)
	readbackBefore, _ := NewStrideE10DisposableMigrationTarget(targetPath, 1600, retired).ReadStrideE10MigrationTarget(ctx, "")
	if err := RestoreStrideE10MigrationRehearsal(ctx, config.StatePath, resealedBackup, wrong, NewStrideE10DisposableMigrationTarget(targetPath, 1600, wrong), NewStrideE10LocalMigrationRestorer(accounts, sessions)); err == nil {
		t.Fatal("wrong key accepted completed restore journal")
	}
	readbackAfter, _ := NewStrideE10DisposableMigrationTarget(targetPath, 1600, retired).ReadStrideE10MigrationTarget(ctx, "")
	if readbackAfter.SnapshotDigest != readbackBefore.SnapshotDigest || readbackAfter.HighWater != readbackBefore.HighWater {
		t.Fatal("wrong-key journal attempt changed disposable target")
	}
}
