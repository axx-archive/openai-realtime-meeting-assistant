package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func strideE10TestDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type strideE10TestKeyring struct {
	current StrideE10MigrationMACKey
	keys    map[string]StrideE10MigrationMACKey
}

func (k *strideE10TestKeyring) CurrentStrideE10MigrationKey(context.Context) (StrideE10MigrationMACKey, error) {
	return k.current, nil
}

func (k *strideE10TestKeyring) ResolveStrideE10MigrationKey(_ context.Context, id string, version uint64) (StrideE10MigrationMACKey, error) {
	key, ok := k.keys[id]
	if !ok || key.Version != version {
		return StrideE10MigrationMACKey{}, errors.New("unknown key")
	}
	return key, nil
}

func strideE10TestKeys(id string, version uint64) *strideE10TestKeyring {
	key := StrideE10MigrationMACKey{ID: id, Version: version, Secret: []byte(strings.Repeat(id+"/", 16))}
	return &strideE10TestKeyring{current: key, keys: map[string]StrideE10MigrationMACKey{id: key}}
}

type strideE10TestWriter struct {
	path         string
	before       uint64
	after        uint64
	apply        func()
	receipts     map[string]StrideE10MigrationWriteReceipt
	lostResponse bool
	applied      int
	rows         []StrideE10CanonicalTargetRow
	beforeRows   []StrideE10CanonicalTargetRow
	rolledBack   bool
	replayAfter  uint64
}

func (w *strideE10TestWriter) strideE10DisposableTargetPath() string { return w.path }

func (w *strideE10TestWriter) ApplyStrideE10Migration(_ context.Context, request StrideE10MigrationWriteRequest) (StrideE10MigrationWriteObservation, error) {
	if request.ExpectedDelta != StrideE10MigrationExpectedTargetDelta || request.OperationID == "" || request.SourceDigest == "" || request.MigrationDigest == "" || request.BackupIdentityDigest == "" {
		return StrideE10MigrationWriteObservation{}, errors.New("incomplete write contract")
	}
	if receipt, ok := w.receipts[request.OperationID]; ok {
		if w.rolledBack {
			w.after = w.replayAfter
			w.rows = append([]StrideE10CanonicalTargetRow(nil), request.Manifest.Rows...)
			w.rolledBack = false
			return StrideE10MigrationWriteObservation{Receipt: receipt}, nil
		}
		return StrideE10MigrationWriteObservation{Receipt: receipt}, nil
	}
	if w.apply != nil {
		w.apply()
	}
	w.beforeRows = append([]StrideE10CanonicalTargetRow(nil), w.rows...)
	w.rows = append([]StrideE10CanonicalTargetRow(nil), request.Manifest.Rows...)
	receipt := StrideE10MigrationWriteReceipt{OperationID: request.OperationID, MigrationDigest: request.MigrationDigest, SourceDigest: request.SourceDigest, BackupIdentityDigest: request.BackupIdentityDigest, BeforeHighWater: w.before, AfterHighWater: w.after, ExpectedDelta: request.ExpectedDelta, ManifestDigest: request.Manifest.Digest, BeforeSnapshotDigest: strideE10TargetSnapshotDigest(w.before, w.beforeRows), AfterSnapshotDigest: strideE10TargetSnapshotDigest(w.after, w.rows)}
	receipt.ReceiptDigest = strideE10MigrationWriteReceiptDigest(receipt)
	if w.receipts == nil {
		w.receipts = map[string]StrideE10MigrationWriteReceipt{}
	}
	w.receipts[request.OperationID] = receipt
	w.applied++
	if w.lostResponse {
		w.lostResponse = false
		return StrideE10MigrationWriteObservation{}, errors.New("committed but response lost")
	}
	return StrideE10MigrationWriteObservation{Receipt: receipt}, nil
}

func (w *strideE10TestWriter) ReadStrideE10MigrationTarget(_ context.Context, _ string) (StrideE10MigrationTargetReadback, error) {
	rows := append([]StrideE10CanonicalTargetRow(nil), w.rows...)
	highWater := w.after
	if w.applied == 0 {
		highWater = w.before
	}
	return StrideE10MigrationTargetReadback{HighWater: highWater, Rows: rows, SnapshotDigest: strideE10TargetSnapshotDigest(highWater, rows)}, nil
}

func (w *strideE10TestWriter) RollbackStrideE10Migration(_ context.Context, request StrideE10MigrationRollbackRequest) (StrideE10MigrationRollbackReceipt, error) {
	receipt, ok := w.receipts[request.OperationID]
	if !ok {
		return StrideE10MigrationRollbackReceipt{}, errors.New("missing apply")
	}
	if request.ExpectedBeforeHighWater != receipt.BeforeHighWater || request.ExpectedBeforeSnapshotDigest != receipt.BeforeSnapshotDigest {
		return StrideE10MigrationRollbackReceipt{}, errors.New("before-image recovery binding drift")
	}
	w.rows = append([]StrideE10CanonicalTargetRow(nil), w.beforeRows...)
	w.replayAfter = receipt.AfterHighWater
	w.after = receipt.BeforeHighWater
	w.rolledBack = true
	rollback := StrideE10MigrationRollbackReceipt{OperationID: request.OperationID, BeforeHighWater: receipt.BeforeHighWater, RestoredHighWater: receipt.BeforeHighWater, BeforeSnapshotDigest: receipt.BeforeSnapshotDigest, RestoredDigest: receipt.BeforeSnapshotDigest}
	rollback.ReceiptDigest = strideE10MigrationRollbackReceiptDigest(rollback)
	return rollback, nil
}

func strideE10TestContract() StrideE10MigrationContractInput {
	return StrideE10MigrationContractInput{
		OrganizationName: "Bonfire", OrganizationSlug: "bonfire",
		SchemaDigest: strideE10TestDigest("schema"), PolicyDigest: strideE10TestDigest("policy"),
		MigrationDigest: strideE10TestDigest("migration"), SwitchDigest: strideE10TestDigest("switches-off"),
		OperatorDigest: strideE10TestDigest("operator"), ReviewerDigest: strideE10TestDigest("reviewer"),
		RollbackIdentityDigest: strideE10TestDigest("rollback-authority"),
	}
}

func strideE10TestAuthorities(t *testing.T, directory string) (*userAccountStore, *sessionStore) {
	t.Helper()
	accountsPath := filepath.Join(directory, "users.json")
	accounts, err := newUserAccountStore(accountsPath)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	accounts.mu.Lock()
	accounts.users["extra@example.com"] = &userAccount{Email: "extra@example.com", Name: "Extra", PasswordHash: []byte("extra-password"), WebAuthnHandle: []byte("extra-handle"), AvatarDataURL: "data:image/png;base64,private-extra", PasswordChangedAt: time.Now().UTC()}
	if err := accounts.persistLocked(); err != nil {
		accounts.mu.Unlock()
		t.Fatalf("persist accounts: %v", err)
	}
	accounts.mu.Unlock()

	sessions := newSessionStore(filepath.Join(directory, "sessions.json"))
	sessions.mu.Lock()
	for index, seed := range seededAccounts {
		hash := strideE10TestDigest("session/" + seed.Email)
		sessions.sessions[hash] = sessionRecord{Email: seed.Email, Expires: time.Now().UTC().Add(24*time.Hour + time.Duration(index)*time.Minute)}
	}
	sessions.persistLocked()
	sessions.mu.Unlock()
	return accounts, sessions
}

func strideE10TestConfig(directory string, source StrideE10MigrationSource, writer StrideE10MigrationTargetWriter, keys StrideE10MigrationKeyring) StrideE10MigrationRunConfig {
	if testWriter, ok := writer.(*strideE10TestWriter); ok && testWriter.path == "" {
		testWriter.path = filepath.Join(directory, "test-target.json")
	}
	return StrideE10MigrationRunConfig{StatePath: filepath.Join(directory, "state.json"), BackupPath: filepath.Join(directory, "backup.json"), PublicReceiptPath: filepath.Join(directory, "receipt.json"), Source: source, Writer: writer, Keys: keys, Contract: strideE10TestContract()}
}

func TestStrideE10MigrationRehearsalAuthoritativeIdempotentSignedBackupRestore(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	keys := strideE10TestKeys("managed-primary", 1)
	writer := &strideE10TestWriter{before: 100, after: 115}
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), writer, keys)

	first, err := RunStrideE10MigrationRehearsal(ctx, config)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Receipt.MigratedRoots != 7 || first.Receipt.MigratedMemberships != 7 || first.Receipt.PreservedExtraAccounts != 1 || first.Receipt.SourceHighWater != 100 || first.Receipt.TargetHighWater != 115 {
		t.Fatalf("unexpected receipt: %+v", first.Receipt)
	}
	owners := 0
	for index, binding := range first.PrivateManifest.Bindings {
		if binding.Role == "owner" {
			owners++
			if binding.NormalizedSubject != "aj@shareability.com" {
				t.Fatalf("owner is not AJ: %+v", binding)
			}
		}
		if !reflect.DeepEqual(first.Legacy[index], first.Canonical[index].Snapshot) {
			t.Fatalf("legacy/canonical behavior drift at %d", index)
		}
	}
	if owners != 1 {
		t.Fatalf("owners=%d", owners)
	}
	public, _ := json.Marshal(first.Receipt)
	if strings.Contains(string(public), "@") || strings.Contains(string(public), "private-extra") || strings.Contains(string(public), "session/") {
		t.Fatalf("public receipt leaked source identity/body: %s", public)
	}
	signedPublic := mustReadStrideE10Test(t, config.PublicReceiptPath)
	verifiedPublic, err := VerifyStrideE10MigrationPublicReceipt(ctx, keys, signedPublic)
	if err != nil || !reflect.DeepEqual(verifiedPublic, first.Receipt) {
		t.Fatalf("separate public receipt verification: %+v %v", verifiedPublic, err)
	}
	var publicEnvelope strideE10MigrationEnvelope
	if json.Unmarshal(signedPublic, &publicEnvelope) != nil || strings.Contains(string(publicEnvelope.Payload), "@") || strings.Contains(string(publicEnvelope.Payload), "private-extra") || strings.Contains(string(publicEnvelope.Payload), "session/") {
		t.Fatalf("signed public envelope leaked private identity/body: %s", signedPublic)
	}
	var privateState strideE10MigrationState
	if err := strideE10UnmarshalSignedDomain(ctx, keys, signedPublic, &privateState, strideE10MigrationPrivateMACDomain); err == nil {
		t.Fatal("public receipt envelope was accepted as private state")
	}
	privateRaw := mustReadStrideE10Test(t, config.StatePath)
	if _, err := VerifyStrideE10MigrationPublicReceipt(ctx, keys, privateRaw); err == nil {
		t.Fatal("private state envelope was accepted as public receipt")
	}
	tamperedPublic := append([]byte(nil), signedPublic...)
	var tamperedEnvelope strideE10MigrationEnvelope
	_ = json.Unmarshal(tamperedPublic, &tamperedEnvelope)
	tamperedEnvelope.Payload = bytes.Replace(tamperedEnvelope.Payload, []byte("Bonfire"), []byte("BonfireX"), 1)
	tamperedPublic, _ = json.Marshal(tamperedEnvelope)
	if _, err := VerifyStrideE10MigrationPublicReceipt(ctx, keys, tamperedPublic); err == nil {
		t.Fatal("public receipt payload substitution was accepted")
	}

	stateBefore, _ := os.ReadFile(config.StatePath)
	publicBefore, _ := os.ReadFile(config.PublicReceiptPath)
	second, err := RunStrideE10MigrationRehearsal(ctx, config)
	if err != nil {
		t.Fatalf("idempotent run: %v", err)
	}
	stateAfter, _ := os.ReadFile(config.StatePath)
	publicAfter, _ := os.ReadFile(config.PublicReceiptPath)
	if !reflect.DeepEqual(first.Receipt, second.Receipt) || !reflect.DeepEqual(first.PrivateManifest, second.PrivateManifest) || !reflect.DeepEqual(stateBefore, stateAfter) || !bytes.Equal(publicBefore, publicAfter) {
		t.Fatal("idempotent replay changed signed payload or stable opaque IDs")
	}

	backup, err := BackupStrideE10MigrationRehearsal(ctx, config.BackupPath, keys)
	if err != nil {
		t.Fatalf("read signed backup: %v", err)
	}
	var privateBackup strideE10MigrationBackup
	if err := strideE10UnmarshalSigned(ctx, keys, backup, &privateBackup); err != nil {
		t.Fatalf("verify private backup: %v", err)
	}
	if privateBackup.BackupID == "" || privateBackup.BackupID == first.Receipt.OrganizationID || !reflectStrideE10MigrationInput(privateBackup.Input, first.RollbackInput()) {
		t.Fatal("backup is not independently identified exact credential/profile/avatar/passkey/session/extra source state")
	}
	restoredPath := filepath.Join(directory, "fresh-process-state.json")
	accounts.mu.Lock()
	accounts.users = map[string]*userAccount{"corrupt@example.com": {Email: "corrupt@example.com"}}
	accounts.mu.Unlock()
	sessions.mu.Lock()
	sessions.sessions = map[string]sessionRecord{"corrupt": {Email: "corrupt@example.com"}}
	sessions.mu.Unlock()
	if err := os.WriteFile(accounts.path, []byte("corrupt accounts"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessions.path, []byte("corrupt sessions"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreStrideE10MigrationRehearsal(ctx, restoredPath, backup, keys, config.Writer, NewStrideE10LocalMigrationRestorer(accounts, sessions)); err != nil {
		t.Fatalf("fresh restore: %v", err)
	}
	restoredAccounts, err := os.ReadFile(accounts.path)
	if err != nil {
		t.Fatal(err)
	}
	restoredSessions, err := os.ReadFile(sessions.path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restoredAccounts, privateBackup.Input.SourceAccountFileBody) || !reflect.DeepEqual(restoredSessions, privateBackup.Input.SourceSessionFileBody) {
		t.Fatal("source restore did not reproduce the exact authoritative account/session bytes")
	}
	restoredConfig := config
	restoredConfig.StatePath = restoredPath
	restored, err := RunStrideE10MigrationRehearsal(ctx, restoredConfig)
	if err != nil || !reflect.DeepEqual(restored.PrivateManifest, first.PrivateManifest) {
		t.Fatalf("fresh restart/restore drift: %v", err)
	}
	rollback := first.RollbackInput()
	if len(rollback.Extras) != 1 || !strings.Contains(string(rollback.Extras[0].Body), "private-extra") {
		t.Fatal("rollback omitted exact extra account bytes")
	}
	rollback.Extras[0].Body[0] ^= 0xff
	if reflect.DeepEqual(rollback.Extras[0].Body, first.RollbackInput().Extras[0].Body) {
		t.Fatal("rollback aliases private backup bytes")
	}
}

func TestStrideE10MigrationRehearsalRejectsWriterDeltaAndConcurrentSourceDrift(t *testing.T) {
	for name, bounds := range map[string][2]uint64{"equal": {100, 100}, "wrong": {100, 114}, "reversed": {100, 99}, "zero": {0, 15}} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			accounts, sessions := strideE10TestAuthorities(t, directory)
			config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), &strideE10TestWriter{before: bounds[0], after: bounds[1]}, strideE10TestKeys("key-"+name, 1))
			if _, err := RunStrideE10MigrationRehearsal(context.Background(), config); err == nil {
				t.Fatal("expected invalid writer observation to fail")
			}
		})
	}

	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	writer := &strideE10TestWriter{before: 100, after: 115, apply: func() {
		accounts.mu.Lock()
		accounts.users["extra@example.com"].AvatarDataURL += "-concurrent"
		accounts.mu.Unlock()
	}}
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), writer, strideE10TestKeys("drift-key", 1))
	if _, err := RunStrideE10MigrationRehearsal(context.Background(), config); err == nil {
		t.Fatal("expected concurrent authoritative source drift to fail")
	}
}

func TestStrideE10MigrationRehearsalCommitLostResponseReusesDurableWriterReceipt(t *testing.T) {
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	writer := &strideE10TestWriter{before: 100, after: 115, lostResponse: true}
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), writer, strideE10TestKeys("lost-response-key", 1))
	if _, err := RunStrideE10MigrationRehearsal(context.Background(), config); err == nil || writer.applied != 1 {
		t.Fatalf("expected committed lost response, err=%v applied=%d", err, writer.applied)
	}
	result, err := RunStrideE10MigrationRehearsal(context.Background(), config)
	if err != nil {
		t.Fatalf("durable replay: %v", err)
	}
	if writer.applied != 1 || result.Receipt.SourceHighWater != 100 || result.Receipt.TargetHighWater != 115 {
		t.Fatalf("writer replay applied twice or shifted bounds: applied=%d receipt=%+v", writer.applied, result.Receipt)
	}
}

func TestStrideE10MigrationSourceRestoreRollsBackBothAuthoritiesOnFailure(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	keys := strideE10TestKeys("restore-atomic-key", 1)
	writer := &strideE10TestWriter{before: 100, after: 115}
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), writer, keys)
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

	restorer := NewStrideE10LocalMigrationRestorer(accounts, sessions).(*strideE10LocalMigrationRestorer)
	restorer.write = func(path string, body []byte, mode os.FileMode) error {
		if path == sessions.path {
			return errors.New("injected second-authority write failure")
		}
		return writeFileAtomicallyDurable(path, body, mode)
	}
	statePath := filepath.Join(directory, "failed-restore-state.json")
	if err := RestoreStrideE10MigrationRehearsal(ctx, statePath, backup, keys, config.Writer, restorer); err == nil {
		t.Fatal("expected source restore failure")
	}
	accountAfter, _ := os.ReadFile(accounts.path)
	sessionAfter, _ := os.ReadFile(sessions.path)
	accounts.mu.Lock()
	_, sentinelPresent := accounts.users["sentinel@example.com"]
	accounts.mu.Unlock()
	if !reflect.DeepEqual(accountAfter, sentinelAccounts) || !reflect.DeepEqual(sessionAfter, sentinelSessions) || !sentinelPresent {
		t.Fatal("failed source restore did not atomically roll back both files and in-memory authorities")
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed source restore published migration state")
	}
	if writer.after != 115 || len(writer.rows) != 15 || writer.rolledBack {
		t.Fatal("failed source restore did not compensate target back to applied image")
	}

	// Publishing the restored migration state is part of the same authority
	// transaction. If it fails after both source writes, both source authorities
	// must still return to their exact pre-restore state.
	restorer.write = writeFileAtomicallyDurable
	blockedParent := filepath.Join(directory, "blocked-state-parent")
	if err := os.WriteFile(blockedParent, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingStatePath := filepath.Join(blockedParent, "restore-state.json")
	if err := RestoreStrideE10MigrationRehearsal(ctx, missingStatePath, backup, keys, config.Writer, restorer); err == nil {
		t.Fatal("expected final state publication failure")
	}
	accountAfter, _ = os.ReadFile(accounts.path)
	sessionAfter, _ = os.ReadFile(sessions.path)
	accounts.mu.Lock()
	_, sentinelPresent = accounts.users["sentinel@example.com"]
	accounts.mu.Unlock()
	if !reflect.DeepEqual(accountAfter, sentinelAccounts) || !reflect.DeepEqual(sessionAfter, sentinelSessions) || !sentinelPresent {
		t.Fatal("finalize failure did not atomically roll back both source authorities")
	}
	blockedParentAfter, err := os.ReadFile(blockedParent)
	if err != nil || string(blockedParentAfter) != "not-a-directory" {
		t.Fatal("finalize failure altered the blocked state target")
	}
	if writer.after != 115 || len(writer.rows) != 15 || writer.rolledBack {
		t.Fatal("failed state publication did not compensate target back to applied image")
	}
}

func TestStrideE10MigrationRehearsalMACWrongKeyTamperAndRotation(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	oldKeys := strideE10TestKeys("old-key", 1)
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), &strideE10TestWriter{before: 100, after: 115}, oldKeys)
	first, err := RunStrideE10MigrationRehearsal(ctx, config)
	if err != nil {
		t.Fatalf("initial run: %v", err)
	}
	wrong := strideE10TestKeys("wrong-key", 1)
	wrongConfig := config
	wrongConfig.Keys = wrong
	if _, err := RunStrideE10MigrationRehearsal(ctx, wrongConfig); err == nil {
		t.Fatal("wrong managed key accepted")
	}

	raw, _ := os.ReadFile(config.StatePath)
	var envelope strideE10MigrationEnvelope
	_ = json.Unmarshal(raw, &envelope)
	envelope.Payload = bytes.Replace(envelope.Payload, []byte("Bonfire"), []byte("BonfireX"), 1)
	tampered, _ := json.Marshal(envelope)
	if err := os.WriteFile(config.StatePath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunStrideE10MigrationRehearsal(ctx, config); err == nil {
		t.Fatal("coherent payload tamper without managed MAC accepted")
	}
	if err := RestoreStrideE10MigrationRehearsal(ctx, config.StatePath, mustReadStrideE10Test(t, config.BackupPath), oldKeys, config.Writer, NewStrideE10LocalMigrationRestorer(accounts, sessions)); err != nil {
		t.Fatalf("restore after tamper: %v", err)
	}

	newKey := StrideE10MigrationMACKey{ID: "new-key", Version: 2, Secret: []byte(strings.Repeat("new-key/", 16))}
	rotating := &strideE10TestKeyring{current: newKey, keys: map[string]StrideE10MigrationMACKey{"old-key": oldKeys.current, "new-key": newKey}}
	config.Keys = rotating
	rotated, err := RunStrideE10MigrationRehearsal(ctx, config)
	if err != nil {
		t.Fatalf("rotation run: %v", err)
	}
	if !reflect.DeepEqual(rotated.PrivateManifest, first.PrivateManifest) {
		t.Fatal("key rotation changed stable migration identities")
	}
	raw, _ = os.ReadFile(config.StatePath)
	_ = json.Unmarshal(raw, &envelope)
	if envelope.KeyID != "new-key" || envelope.KeyVersion != 2 {
		t.Fatalf("state was not resealed to current key: %+v", envelope)
	}
	publicRaw := mustReadStrideE10Test(t, config.PublicReceiptPath)
	_ = json.Unmarshal(publicRaw, &envelope)
	if envelope.KeyID != "new-key" || envelope.KeyVersion != 2 {
		t.Fatalf("public receipt was not resealed to current key: %+v", envelope)
	}
}

func TestStrideE10MigrationRehearsalRejectsAliasesAndNonDisposableTargetsWithoutSourceMutation(t *testing.T) {
	for _, name := range []string{"state-accounts", "backup-sessions", "target-accounts", "symlink-state", "hardlink-state", "hardlink-backup", "hardlink-public", "hardlink-target"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			accounts, sessions := strideE10TestAuthorities(t, directory)
			writer := &strideE10TestWriter{before: 20, after: 35}
			config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), writer, strideE10TestKeys("alias-"+name, 1))
			switch name {
			case "state-accounts":
				config.StatePath = accounts.path
			case "backup-sessions":
				config.BackupPath = sessions.path
			case "target-accounts":
				writer.path = accounts.path
			case "symlink-state":
				alias := filepath.Join(directory, "accounts-alias.json")
				if err := os.Symlink(accounts.path, alias); err != nil {
					t.Fatal(err)
				}
				config.StatePath = alias
			case "hardlink-state":
				config.StatePath = filepath.Join(directory, "accounts-state-hardlink.json")
				if err := os.Link(accounts.path, config.StatePath); err != nil {
					t.Fatal(err)
				}
			case "hardlink-backup":
				config.BackupPath = filepath.Join(directory, "sessions-backup-hardlink.json")
				if err := os.Link(sessions.path, config.BackupPath); err != nil {
					t.Fatal(err)
				}
			case "hardlink-public":
				config.PublicReceiptPath = filepath.Join(directory, "accounts-public-hardlink.json")
				if err := os.Link(accounts.path, config.PublicReceiptPath); err != nil {
					t.Fatal(err)
				}
			case "hardlink-target":
				writer.path = filepath.Join(directory, "accounts-target-hardlink.json")
				if err := os.Link(accounts.path, writer.path); err != nil {
					t.Fatal(err)
				}
			}
			accountsBefore := mustReadStrideE10Test(t, accounts.path)
			sessionsBefore := mustReadStrideE10Test(t, sessions.path)
			if _, err := RunStrideE10MigrationRehearsal(context.Background(), config); err == nil {
				t.Fatal("aliased rehearsal topology accepted")
			}
			if !bytes.Equal(accountsBefore, mustReadStrideE10Test(t, accounts.path)) || !bytes.Equal(sessionsBefore, mustReadStrideE10Test(t, sessions.path)) {
				t.Fatal("topology rejection changed authoritative source bytes")
			}
		})
	}

	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	inner := &strideE10TestWriter{path: filepath.Join(directory, "target.json"), before: 30, after: 45}
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), struct{ StrideE10MigrationTargetWriter }{inner}, strideE10TestKeys("non-disposable", 1))
	if _, err := RunStrideE10MigrationRehearsal(context.Background(), config); err == nil {
		t.Fatal("writer without disposable capability accepted")
	}

	restoreDirectory := t.TempDir()
	restoreAccounts, restoreSessions := strideE10TestAuthorities(t, restoreDirectory)
	restoreKeys := strideE10TestKeys("restore-alias", 1)
	restoreWriter := &strideE10TestWriter{before: 80, after: 95}
	restoreConfig := strideE10TestConfig(restoreDirectory, NewStrideE10LocalMigrationSource(restoreAccounts, restoreSessions), restoreWriter, restoreKeys)
	if _, err := RunStrideE10MigrationRehearsal(context.Background(), restoreConfig); err != nil {
		t.Fatal(err)
	}
	accountsBefore := mustReadStrideE10Test(t, restoreAccounts.path)
	sessionsBefore := mustReadStrideE10Test(t, restoreSessions.path)
	if err := RestoreStrideE10MigrationRehearsal(context.Background(), restoreAccounts.path, mustReadStrideE10Test(t, restoreConfig.BackupPath), restoreKeys, restoreWriter, NewStrideE10LocalMigrationRestorer(restoreAccounts, restoreSessions)); err == nil {
		t.Fatal("restore state/source alias accepted")
	}
	if !bytes.Equal(accountsBefore, mustReadStrideE10Test(t, restoreAccounts.path)) || !bytes.Equal(sessionsBefore, mustReadStrideE10Test(t, restoreSessions.path)) {
		t.Fatal("restore topology rejection changed authoritative source bytes")
	}
	readbackBefore, err := restoreWriter.ReadStrideE10MigrationTarget(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	hardlinkState := filepath.Join(restoreDirectory, "restore-state-source-hardlink.json")
	if err := os.Link(restoreAccounts.path, hardlinkState); err != nil {
		t.Fatal(err)
	}
	if err := RestoreStrideE10MigrationRehearsal(context.Background(), hardlinkState, mustReadStrideE10Test(t, restoreConfig.BackupPath), restoreKeys, restoreWriter, NewStrideE10LocalMigrationRestorer(restoreAccounts, restoreSessions)); err == nil {
		t.Fatal("restore hard-linked state/source alias accepted")
	}
	readbackAfter, err := restoreWriter.ReadStrideE10MigrationTarget(context.Background(), "")
	if err != nil || readbackAfter.HighWater != readbackBefore.HighWater || readbackAfter.SnapshotDigest != readbackBefore.SnapshotDigest || !bytes.Equal(accountsBefore, mustReadStrideE10Test(t, restoreAccounts.path)) || !bytes.Equal(sessionsBefore, mustReadStrideE10Test(t, restoreSessions.path)) {
		t.Fatalf("restore hard-link topology rejection changed source/target: before=%+v after=%+v err=%v", readbackBefore, readbackAfter, err)
	}
}

func TestStrideE10MigrationRehearsalPreservesGuestAndOrphanSessionsWithoutPersonMapping(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	accounts, sessions := strideE10TestAuthorities(t, directory)
	guestHash := strideE10TestDigest("guest-session")
	orphanHash := strideE10TestDigest("orphan-session")
	sessions.mu.Lock()
	sessions.sessions[guestHash] = sessionRecord{Kind: "guest", RoomID: "private-room", GuestName: "Private Guest", Expires: time.Now().UTC().Add(time.Hour)}
	sessions.sessions[orphanHash] = sessionRecord{Email: "departed@example.com", Expires: time.Now().UTC().Add(2 * time.Hour)}
	sessions.persistLocked()
	sessions.mu.Unlock()
	sessionsBefore := mustReadStrideE10Test(t, sessions.path)
	keys := strideE10TestKeys("session-extras", 1)
	config := strideE10TestConfig(directory, NewStrideE10LocalMigrationSource(accounts, sessions), &strideE10TestWriter{before: 60, after: 75}, keys)
	result, err := RunStrideE10MigrationRehearsal(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.MigratedRoots != 7 || result.Receipt.PreservedExtraSessions != 2 || result.PrivateManifest.PreservedExtraSessions != 2 {
		t.Fatalf("session extra mapping drift: receipt=%+v manifest=%+v", result.Receipt, result.PrivateManifest)
	}
	for _, binding := range result.PrivateManifest.Bindings {
		if binding.NormalizedSubject == "departed@example.com" {
			t.Fatal("orphan session was mapped to a person")
		}
	}
	var backup strideE10MigrationBackup
	if err := strideE10UnmarshalSigned(ctx, keys, mustReadStrideE10Test(t, config.BackupPath), &backup); err != nil || len(backup.Input.SessionExtras) != 2 {
		t.Fatalf("private backup omitted session extras: %v", err)
	}
	publicRaw := mustReadStrideE10Test(t, config.PublicReceiptPath)
	var publicEnvelope strideE10MigrationEnvelope
	_ = json.Unmarshal(publicRaw, &publicEnvelope)
	if strings.Contains(string(publicEnvelope.Payload), "Private Guest") || strings.Contains(string(publicEnvelope.Payload), "private-room") || strings.Contains(string(publicEnvelope.Payload), "departed@example.com") || strings.Contains(string(publicEnvelope.Payload), guestHash) {
		t.Fatal("public receipt leaked preserved session extra")
	}
	if !bytes.Equal(sessionsBefore, backup.Input.SourceSessionFileBody) {
		t.Fatal("backup did not preserve exact session authority bytes")
	}
	sessions.mu.Lock()
	sessions.sessions = map[string]sessionRecord{"corrupt": {Email: "corrupt@example.com"}}
	sessions.mu.Unlock()
	if err := os.WriteFile(sessions.path, []byte(`{"corrupt":{"email":"corrupt@example.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreStrideE10MigrationRehearsal(ctx, filepath.Join(directory, "restored-state.json"), mustReadStrideE10Test(t, config.BackupPath), keys, config.Writer, NewStrideE10LocalMigrationRestorer(accounts, sessions)); err != nil {
		t.Fatalf("restore session extras: %v", err)
	}
	if !bytes.Equal(sessionsBefore, mustReadStrideE10Test(t, sessions.path)) {
		t.Fatal("restore did not reproduce guest/orphan session bytes exactly")
	}
}

func mustReadStrideE10Test(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
