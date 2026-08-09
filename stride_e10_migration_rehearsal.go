package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const strideE10MigrationStateVersion = 1

const StrideE10MigrationExpectedTargetDelta uint64 = 15

type StrideE10MigrationMACKey struct {
	ID      string
	Version uint64
	Secret  []byte
}

type StrideE10MigrationKeyring interface {
	CurrentStrideE10MigrationKey(context.Context) (StrideE10MigrationMACKey, error)
	ResolveStrideE10MigrationKey(context.Context, string, uint64) (StrideE10MigrationMACKey, error)
}

type StrideE10MigrationContractInput struct {
	OrganizationName       string
	OrganizationSlug       string
	SchemaDigest           string
	PolicyDigest           string
	MigrationDigest        string
	SwitchDigest           string
	OperatorDigest         string
	ReviewerDigest         string
	RollbackIdentityDigest string
}

type StrideE10MigrationSource interface {
	CaptureStrideE10MigrationSource(context.Context) (StrideE10MigrationInput, error)
}

// These unexported capabilities make the rehearsal boundary explicit. Only an
// offline source snapshot and a disposable target may be admitted by the
// proof-bearing runner.
type strideE10OfflineMigrationSource interface {
	StrideE10MigrationSource
	strideE10MigrationSourcePaths() (string, string)
}

type strideE10OfflineMigrationTarget interface {
	StrideE10MigrationTargetWriter
	strideE10DisposableTargetPath() string
}

type StrideE10MigrationTargetWriter interface {
	ApplyStrideE10Migration(context.Context, StrideE10MigrationWriteRequest) (StrideE10MigrationWriteObservation, error)
	ReadStrideE10MigrationTarget(context.Context, string) (StrideE10MigrationTargetReadback, error)
	RollbackStrideE10Migration(context.Context, StrideE10MigrationRollbackRequest) (StrideE10MigrationRollbackReceipt, error)
}

type StrideE10MigrationSourceRestorer interface {
	RestoreStrideE10MigrationSource(context.Context, StrideE10MigrationInput, func() error) error
}

type strideE10MigrationProgressRestorer interface {
	RestoreStrideE10MigrationSourceWithProgress(context.Context, StrideE10MigrationInput, func(string) error, func() error) error
}

type strideE10OfflineMigrationRestorer interface {
	StrideE10MigrationSourceRestorer
	strideE10MigrationRestorePaths() (string, string)
}

var errStrideE10MigrationAbruptRestart = errors.New("simulated abrupt migration restore restart")
var strideE10MigrationRestorePhaseHook func(string) error

type StrideE10MigrationWriteRequest struct {
	OperationID          string
	MigrationDigest      string
	SourceDigest         string
	BackupIdentityDigest string
	ExpectedDelta        uint64
	Manifest             StrideE10CanonicalTargetManifest
}

type StrideE10CanonicalTargetRow struct {
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Revision int64           `json:"revision"`
	Body     json.RawMessage `json:"body"`
}

type StrideE10CanonicalTargetManifest struct {
	Version int                           `json:"version"`
	Rows    []StrideE10CanonicalTargetRow `json:"rows"`
	Digest  string                        `json:"digest"`
}

type StrideE10MigrationTargetReadback struct {
	HighWater      uint64                        `json:"highWater"`
	Rows           []StrideE10CanonicalTargetRow `json:"rows"`
	SnapshotDigest string                        `json:"snapshotDigest"`
}

type StrideE10MigrationRollbackRequest struct {
	OperationID                  string `json:"operationId"`
	MigrationDigest              string `json:"migrationDigest"`
	SourceDigest                 string `json:"sourceDigest"`
	BackupIdentityDigest         string `json:"backupIdentityDigest"`
	ManifestDigest               string `json:"manifestDigest"`
	WriterReceiptDigest          string `json:"writerReceiptDigest"`
	ExpectedBeforeHighWater      uint64 `json:"expectedBeforeHighWater"`
	ExpectedBeforeSnapshotDigest string `json:"expectedBeforeSnapshotDigest"`
}

type StrideE10MigrationRollbackReceipt struct {
	OperationID          string `json:"operationId"`
	BeforeHighWater      uint64 `json:"beforeHighWater"`
	RestoredHighWater    uint64 `json:"restoredHighWater"`
	BeforeSnapshotDigest string `json:"beforeSnapshotDigest"`
	RestoredDigest       string `json:"restoredDigest"`
	ReceiptDigest        string `json:"receiptDigest"`
}

type StrideE10MigrationWriteObservation struct {
	Receipt StrideE10MigrationWriteReceipt
}

type StrideE10MigrationWriteReceipt struct {
	OperationID          string `json:"operationId"`
	MigrationDigest      string `json:"migrationDigest"`
	SourceDigest         string `json:"sourceDigest"`
	BackupIdentityDigest string `json:"backupIdentityDigest"`
	BeforeHighWater      uint64 `json:"beforeHighWater"`
	AfterHighWater       uint64 `json:"afterHighWater"`
	ExpectedDelta        uint64 `json:"expectedDelta"`
	ManifestDigest       string `json:"manifestDigest"`
	BeforeSnapshotDigest string `json:"beforeSnapshotDigest"`
	AfterSnapshotDigest  string `json:"afterSnapshotDigest"`
	ReceiptDigest        string `json:"receiptDigest"`
}

type StrideE10MigrationRunConfig struct {
	StatePath         string
	BackupPath        string
	PublicReceiptPath string
	Source            StrideE10MigrationSource
	Writer            StrideE10MigrationTargetWriter
	Keys              StrideE10MigrationKeyring
	Contract          StrideE10MigrationContractInput
}

// StrideE10CredentialSnapshot is an offline, authoritative copy of one
// legacy identity root. Component bodies remain private to the rehearsal and
// are represented by digests only in its receipt and persisted mapping.
type StrideE10CredentialSnapshot struct {
	Subject       string                     `json:"subject"`
	Role          string                     `json:"role"`
	AccountBody   []byte                     `json:"accountBody"`
	PasswordBody  []byte                     `json:"passwordBody"`
	ProfileBody   []byte                     `json:"profileBody"`
	AvatarBody    []byte                     `json:"avatarBody"`
	PasskeyBody   []byte                     `json:"passkeyBody"`
	SessionBody   []byte                     `json:"sessionBody"`
	PriorSessions []StrideE10SessionSnapshot `json:"priorSessions"`
}

type StrideE10SessionSnapshot struct {
	Hash      string    `json:"hash"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// StrideE10PreservedAccountSnapshot binds accounts outside the seven-person
// migration without assigning them a canonical person or membership.
type StrideE10PreservedAccountSnapshot struct {
	Subject string `json:"subject"`
	Body    []byte `json:"body"`
}

// StrideE10PreservedSessionSnapshot retains a guest or otherwise non-account
// session exactly without assigning it a Person or membership.
type StrideE10PreservedSessionSnapshot struct {
	Hash string `json:"hash"`
	Body []byte `json:"body"`
}

type StrideE10MigrationInput struct {
	Credentials               []StrideE10CredentialSnapshot       `json:"credentials"`
	Extras                    []StrideE10PreservedAccountSnapshot `json:"extras,omitempty"`
	SessionExtras             []StrideE10PreservedSessionSnapshot `json:"sessionExtras,omitempty"`
	OrganizationName          string                              `json:"organizationName"`
	OrganizationSlug          string                              `json:"organizationSlug"`
	SchemaDigest              string                              `json:"schemaDigest"`
	PolicyDigest              string                              `json:"policyDigest"`
	MigrationDigest           string                              `json:"migrationDigest"`
	SwitchDigest              string                              `json:"switchDigest"`
	SourceHighWater           uint64                              `json:"sourceHighWater"`
	TargetHighWater           uint64                              `json:"targetHighWater"`
	BackupIdentityDigest      string                              `json:"backupIdentityDigest"`
	RollbackIdentityDigest    string                              `json:"rollbackIdentityDigest"`
	OperatorDigest            string                              `json:"operatorDigest"`
	ReviewerDigest            string                              `json:"reviewerDigest"`
	SourceStoreIdentityDigest string                              `json:"sourceStoreIdentityDigest"`
	SourceAccountFileBody     []byte                              `json:"sourceAccountFileBody"`
	SourceSessionFileBody     []byte                              `json:"sourceSessionFileBody"`
}

type strideE10LocalMigrationSource struct {
	accounts *userAccountStore
	sessions *sessionStore
}

type strideE10LocalMigrationRestorer struct {
	accounts *userAccountStore
	sessions *sessionStore
	write    func(string, []byte, os.FileMode) error
}

func NewStrideE10LocalMigrationRestorer(accounts *userAccountStore, sessions *sessionStore) StrideE10MigrationSourceRestorer {
	return &strideE10LocalMigrationRestorer{accounts: accounts, sessions: sessions, write: writeFileAtomicallyDurable}
}

func (r *strideE10LocalMigrationRestorer) strideE10MigrationRestorePaths() (string, string) {
	if r == nil || r.accounts == nil || r.sessions == nil {
		return "", ""
	}
	return r.accounts.path, r.sessions.path
}

func (r *strideE10LocalMigrationRestorer) RestoreStrideE10MigrationSource(_ context.Context, input StrideE10MigrationInput, finalize func() error) error {
	return r.RestoreStrideE10MigrationSourceWithProgress(context.Background(), input, nil, finalize)
}

func (r *strideE10LocalMigrationRestorer) RestoreStrideE10MigrationSourceWithProgress(_ context.Context, input StrideE10MigrationInput, progress func(string) error, finalize func() error) error {
	if r == nil || r.accounts == nil || r.sessions == nil || r.write == nil || finalize == nil || validateStrideE10SourceFileBodies(input) != nil {
		return errors.New("invalid authoritative migration source restore")
	}
	var accountsOnDisk []*userAccount
	var sessionsOnDisk map[string]sessionRecord
	if json.Unmarshal(input.SourceAccountFileBody, &accountsOnDisk) != nil || json.Unmarshal(input.SourceSessionFileBody, &sessionsOnDisk) != nil {
		return errors.New("malformed authoritative migration source backup")
	}
	nextUsers := make(map[string]*userAccount, len(accountsOnDisk))
	for _, account := range accountsOnDisk {
		key := ""
		if account != nil {
			key = normalizeAccountEmail(account.Email)
		}
		if key == "" || nextUsers[key] != nil {
			return errors.New("ambiguous authoritative account restore")
		}
		nextUsers[key] = account
	}
	if sessionsOnDisk == nil {
		sessionsOnDisk = map[string]sessionRecord{}
	}

	r.accounts.mu.Lock()
	r.sessions.mu.Lock()
	defer r.sessions.mu.Unlock()
	defer r.accounts.mu.Unlock()
	oldAccountsFile, accountsReadErr := os.ReadFile(r.accounts.path)
	oldSessionsFile, sessionsReadErr := os.ReadFile(r.sessions.path)
	oldUsers := r.accounts.users
	oldSessions := r.sessions.sessions
	restoreFile := func(path string, body []byte, readErr error) error {
		if errors.Is(readErr, os.ErrNotExist) {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		if readErr != nil {
			return readErr
		}
		return writeFileAtomicallyDurable(path, body, 0o600)
	}
	rollback := func(cause error) error {
		r.accounts.users = oldUsers
		r.sessions.sessions = oldSessions
		accountErr := restoreFile(r.accounts.path, oldAccountsFile, accountsReadErr)
		sessionErr := restoreFile(r.sessions.path, oldSessionsFile, sessionsReadErr)
		if accountErr != nil || sessionErr != nil {
			return fmt.Errorf("restore failed (%v) and source rollback failed (accounts=%v sessions=%v)", cause, accountErr, sessionErr)
		}
		return cause
	}
	if err := r.write(r.accounts.path, input.SourceAccountFileBody, 0o600); err != nil {
		return err
	}
	if progress != nil {
		if err := progress("source_accounts_written"); err != nil {
			if errors.Is(err, errStrideE10MigrationAbruptRestart) {
				return err
			}
			return rollback(err)
		}
	}
	if err := r.write(r.sessions.path, input.SourceSessionFileBody, 0o600); err != nil {
		return rollback(err)
	}
	if progress != nil {
		if err := progress("source_sessions_written"); err != nil {
			if errors.Is(err, errStrideE10MigrationAbruptRestart) {
				return err
			}
			return rollback(err)
		}
	}
	r.accounts.users = nextUsers
	r.sessions.sessions = sessionsOnDisk
	if err := finalize(); err != nil {
		if errors.Is(err, errStrideE10MigrationAbruptRestart) {
			return err
		}
		return rollback(err)
	}
	return nil
}

func NewStrideE10LocalMigrationSource(accounts *userAccountStore, sessions *sessionStore) StrideE10MigrationSource {
	return &strideE10LocalMigrationSource{accounts: accounts, sessions: sessions}
}

func (s *strideE10LocalMigrationSource) strideE10MigrationSourcePaths() (string, string) {
	if s == nil || s.accounts == nil || s.sessions == nil {
		return "", ""
	}
	return s.accounts.path, s.sessions.path
}

type strideE10CapturedSession struct {
	Hash   string        `json:"hash"`
	Record sessionRecord `json:"record"`
}

func (s *strideE10LocalMigrationSource) CaptureStrideE10MigrationSource(_ context.Context) (StrideE10MigrationInput, error) {
	if s == nil || s.accounts == nil || s.sessions == nil || strings.TrimSpace(s.accounts.path) == "" || strings.TrimSpace(s.sessions.path) == "" {
		return StrideE10MigrationInput{}, errors.New("authoritative account/session source is unavailable")
	}
	// Account then session is the single rehearsal lock order. Both maps and
	// their file identities are captured in one source epoch.
	s.accounts.mu.Lock()
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	defer s.accounts.mu.Unlock()

	accountKeys := make([]string, 0, len(s.accounts.users))
	for key := range s.accounts.users {
		accountKeys = append(accountKeys, key)
	}
	sort.Strings(accountKeys)
	accountSubjects := make(map[string]struct{}, len(accountKeys))
	for _, key := range accountKeys {
		accountSubjects[normalizeAccountEmail(key)] = struct{}{}
	}
	sessionByEmail := make(map[string][]strideE10CapturedSession)
	var preservedSessions []StrideE10PreservedSessionSnapshot
	for hash, record := range s.sessions.sessions {
		email := normalizeAccountEmail(record.Email)
		if !validStrideE10SessionHash(hash) {
			return StrideE10MigrationInput{}, errors.New("ambiguous authoritative session record")
		}
		if _, accountSession := accountSubjects[email]; !accountSession {
			body, err := json.Marshal(record)
			if err != nil {
				return StrideE10MigrationInput{}, err
			}
			preservedSessions = append(preservedSessions, StrideE10PreservedSessionSnapshot{Hash: hash, Body: body})
			continue
		}
		sessionByEmail[email] = append(sessionByEmail[email], strideE10CapturedSession{Hash: hash, Record: record})
	}
	for email := range sessionByEmail {
		sort.Slice(sessionByEmail[email], func(i, j int) bool { return sessionByEmail[email][i].Hash < sessionByEmail[email][j].Hash })
	}
	roster := make(map[string]string, len(seededAccounts))
	for _, seed := range seededAccounts {
		roster[normalizeAccountEmail(seed.Email)] = seed.Name
	}
	sort.Slice(preservedSessions, func(i, j int) bool { return preservedSessions[i].Hash < preservedSessions[j].Hash })
	input := StrideE10MigrationInput{SessionExtras: preservedSessions}
	seen := make(map[string]struct{}, len(accountKeys))
	for _, key := range accountKeys {
		account := s.accounts.users[key]
		if account == nil || key != normalizeAccountEmail(account.Email) {
			return StrideE10MigrationInput{}, errors.New("ambiguous authoritative account record")
		}
		if _, duplicate := seen[key]; duplicate {
			return StrideE10MigrationInput{}, errors.New("duplicate authoritative account record")
		}
		seen[key] = struct{}{}
		sessions := sessionByEmail[key]
		accountBytes, err := json.Marshal(account)
		if err != nil {
			return StrideE10MigrationInput{}, err
		}
		sessionBytes, err := json.Marshal(sessions)
		if err != nil {
			return StrideE10MigrationInput{}, err
		}
		if _, seeded := roster[key]; !seeded {
			extraBody, _ := json.Marshal(struct {
				Account  json.RawMessage            `json:"account"`
				Sessions []strideE10CapturedSession `json:"sessions"`
			}{accountBytes, sessions})
			input.Extras = append(input.Extras, StrideE10PreservedAccountSnapshot{Subject: key, Body: extraBody})
			continue
		}
		profileBody, _ := json.Marshal(struct {
			Name      string `json:"name"`
			ThemePref string `json:"themePref"`
		}{account.Name, account.ThemePref})
		passkeyBody, _ := json.Marshal(struct {
			WebAuthnHandle []byte               `json:"webauthnHandle"`
			Credentials    any                  `json:"credentials"`
			PasskeyAddedAt map[string]time.Time `json:"passkeyAddedAt"`
		}{cloneStrideE10MigrationBytes(account.WebAuthnHandle), cloneContract(account.Credentials), cloneContract(account.PasskeyAddedAt)})
		passwordBody, _ := json.Marshal(struct {
			Hash      []byte    `json:"hash"`
			ChangedAt time.Time `json:"changedAt"`
		}{cloneStrideE10MigrationBytes(account.PasswordHash), account.PasswordChangedAt})
		role := "member"
		if key == "aj@shareability.com" {
			role = "owner"
		}
		prior := make([]StrideE10SessionSnapshot, len(sessions))
		for index, session := range sessions {
			prior[index] = StrideE10SessionSnapshot{Hash: session.Hash, ExpiresAt: session.Record.Expires}
		}
		input.Credentials = append(input.Credentials, StrideE10CredentialSnapshot{
			Subject: key, Role: role, AccountBody: cloneStrideE10MigrationBytes(accountBytes), PasswordBody: passwordBody, ProfileBody: profileBody,
			AvatarBody: append([]byte{}, account.AvatarDataURL...), PasskeyBody: passkeyBody, SessionBody: sessionBytes, PriorSessions: prior,
		})
	}
	if len(input.Credentials) != len(seededAccounts) {
		return StrideE10MigrationInput{}, errors.New("authoritative account source is missing a seeded root")
	}
	// Bind the two exact local authority files without publishing either path
	// or their bytes. Locks keep the in-memory enumeration and file snapshot in
	// one source epoch.
	accountFile, accountErr := os.ReadFile(s.accounts.path)
	sessionFile, sessionErr := os.ReadFile(s.sessions.path)
	if accountErr != nil || sessionErr != nil {
		return StrideE10MigrationInput{}, errors.New("authoritative account/session file identity is unavailable")
	}
	input.SourceStoreIdentityDigest = strideE10Digest("source-store-identity", []byte(filepath.Clean(s.accounts.path)), accountFile, []byte(filepath.Clean(s.sessions.path)), sessionFile)
	input.SourceAccountFileBody = cloneStrideE10MigrationBytes(accountFile)
	input.SourceSessionFileBody = cloneStrideE10MigrationBytes(sessionFile)
	return input, nil
}

type StrideE10PrivateMigrationBinding struct {
	NormalizedSubject  string                     `json:"normalizedSubject"`
	PersonID           string                     `json:"personId"`
	MembershipID       string                     `json:"membershipId"`
	MembershipRevision int64                      `json:"membershipRevision"`
	Role               string                     `json:"role"`
	CredentialDigest   string                     `json:"credentialDigest"`
	ProfileDigest      string                     `json:"profileDigest"`
	AvatarDigest       string                     `json:"avatarDigest"`
	PasskeyDigest      string                     `json:"passkeyDigest"`
	SessionDigest      string                     `json:"sessionDigest"`
	PriorSessions      []StrideE10SessionSnapshot `json:"priorSessions"`
}

// StrideE10PrivateMigrationManifest is access-controlled rehearsal state. It
// is deliberately separate from the aggregate public receipt because its
// per-account mappings and session hashes can identify a roster member.
type StrideE10PrivateMigrationManifest struct {
	Version                   int                                `json:"version"`
	OrganizationID            string                             `json:"organizationId"`
	OrganizationName          string                             `json:"organizationName"`
	OrganizationSlug          string                             `json:"organizationSlug"`
	SchemaDigest              string                             `json:"schemaDigest"`
	PolicyDigest              string                             `json:"policyDigest"`
	MigrationDigest           string                             `json:"migrationDigest"`
	SwitchDigest              string                             `json:"switchDigest"`
	SourceHighWater           uint64                             `json:"sourceHighWater"`
	TargetHighWater           uint64                             `json:"targetHighWater"`
	SourceDigest              string                             `json:"sourceDigest"`
	TargetDigest              string                             `json:"targetDigest"`
	BackupIdentityDigest      string                             `json:"backupIdentityDigest"`
	RollbackIdentityDigest    string                             `json:"rollbackIdentityDigest"`
	BackupDigest              string                             `json:"backupDigest"`
	RollbackDigest            string                             `json:"rollbackDigest"`
	OperatorDigest            string                             `json:"operatorDigest"`
	ReviewerDigest            string                             `json:"reviewerDigest"`
	SourceStoreIdentityDigest string                             `json:"sourceStoreIdentityDigest"`
	CredentialSubjectDigest   string                             `json:"credentialSubjectDigest"`
	NoExtraAccountDigest      string                             `json:"noExtraAccountDigest"`
	PreservedExtraAccounts    int                                `json:"preservedExtraAccounts"`
	PreservedExtraSessions    int                                `json:"preservedExtraSessions"`
	ExtraSessionDigest        string                             `json:"extraSessionDigest"`
	Bindings                  []StrideE10PrivateMigrationBinding `json:"bindings"`
}

// StrideE10MigrationReceipt is safe to publish: it contains an opaque org ID,
// aggregate counts, and domain-separated digests, never credential subjects or
// password, profile, avatar, passkey, session, or extra-account bodies.
type StrideE10MigrationReceipt struct {
	Version                int    `json:"version"`
	OrganizationID         string `json:"organizationId"`
	OrganizationName       string `json:"organizationName"`
	OrganizationSlug       string `json:"organizationSlug"`
	MigratedRoots          int    `json:"migratedRoots"`
	MigratedMemberships    int    `json:"migratedMemberships"`
	PreservedExtraAccounts int    `json:"preservedExtraAccounts"`
	PreservedExtraSessions int    `json:"preservedExtraSessions"`
	NoExtraAccountDigest   string `json:"noExtraAccountDigest"`
	SchemaDigest           string `json:"schemaDigest"`
	PolicyDigest           string `json:"policyDigest"`
	MigrationDigest        string `json:"migrationDigest"`
	SwitchDigest           string `json:"switchDigest"`
	SourceHighWater        uint64 `json:"sourceHighWater"`
	TargetHighWater        uint64 `json:"targetHighWater"`
	SourceDigest           string `json:"sourceDigest"`
	TargetDigest           string `json:"targetDigest"`
	BackupDigest           string `json:"backupDigest"`
	RollbackDigest         string `json:"rollbackDigest"`
	BackupIdentityDigest   string `json:"backupIdentityDigest"`
	RollbackIdentityDigest string `json:"rollbackIdentityDigest"`
	OperatorDigest         string `json:"operatorDigest"`
	ReviewerDigest         string `json:"reviewerDigest"`
}

type strideE10MigrationState struct {
	Receipt  StrideE10MigrationReceipt         `json:"publicReceipt"`
	Manifest StrideE10PrivateMigrationManifest `json:"privateManifest"`
}

type strideE10MigrationBackup struct {
	Version               int                                `json:"version"`
	BackupID              string                             `json:"backupId"`
	SourceSnapshotDigest  string                             `json:"sourceSnapshotDigest"`
	WriterOperationID     string                             `json:"writerOperationId"`
	WriterReceipt         *StrideE10MigrationWriteReceipt    `json:"writerReceipt,omitempty"`
	TargetBeforeHighWater uint64                             `json:"targetBeforeHighWater"`
	TargetBeforeDigest    string                             `json:"targetBeforeDigest"`
	TargetManifest        StrideE10CanonicalTargetManifest   `json:"targetManifest"`
	PreparedState         strideE10MigrationState            `json:"preparedState"`
	RollbackReceipt       *StrideE10MigrationRollbackReceipt `json:"rollbackReceipt,omitempty"`
	Input                 StrideE10MigrationInput            `json:"input"`
	State                 strideE10MigrationState            `json:"state"`
}

const (
	strideE10RestoreTargetRollbackStarted  = "target_rollback_started"
	strideE10RestoreTargetRollbackVerified = "target_rollback_verified"
	strideE10RestoreSourceStarted          = "source_restore_started"
	strideE10RestoreAccountsWritten        = "source_accounts_written"
	strideE10RestoreSessionsWritten        = "source_sessions_written"
	strideE10RestoreSourceVerified         = "source_restore_verified"
	strideE10RestoreStateStarted           = "state_publish_started"
	strideE10RestoreStateFileWritten       = "state_file_written"
	strideE10RestoreCompleted              = "completed"
)

type strideE10MigrationRestoreJournal struct {
	Version              int                            `json:"version"`
	BackupID             string                         `json:"backupId"`
	BackupDigest         string                         `json:"backupDigest"`
	OperationID          string                         `json:"operationId"`
	Phase                string                         `json:"phase"`
	WriterReceipt        StrideE10MigrationWriteReceipt `json:"writerReceipt"`
	TargetManifestDigest string                         `json:"targetManifestDigest"`
	SourceAccountsDigest string                         `json:"sourceAccountsDigest"`
	SourceSessionsDigest string                         `json:"sourceSessionsDigest"`
	RestoredStateDigest  string                         `json:"restoredStateDigest"`
}

func strideE10RestorePhaseOrder(phase string) int {
	for index, candidate := range []string{strideE10RestoreTargetRollbackStarted, strideE10RestoreTargetRollbackVerified, strideE10RestoreSourceStarted, strideE10RestoreAccountsWritten, strideE10RestoreSessionsWritten, strideE10RestoreSourceVerified, strideE10RestoreStateStarted, strideE10RestoreCompleted} {
		if phase == candidate {
			return index + 1
		}
	}
	return 0
}

func validateStrideE10MigrationRestoreJournal(journal strideE10MigrationRestoreJournal, backup strideE10MigrationBackup, _ []byte) error {
	stateRaw, _ := json.Marshal(backup.State)
	if journal.Version != strideE10MigrationStateVersion || journal.BackupID != backup.BackupID || journal.BackupDigest != strideE10MigrationRestoreBackupDigest(backup) || journal.OperationID != backup.WriterOperationID || strideE10RestorePhaseOrder(journal.Phase) == 0 || backup.WriterReceipt == nil || journal.WriterReceipt != *backup.WriterReceipt || journal.TargetManifestDigest != backup.TargetManifest.Digest || journal.SourceAccountsDigest != strideE10Digest("restore-accounts", backup.Input.SourceAccountFileBody) || journal.SourceSessionsDigest != strideE10Digest("restore-sessions", backup.Input.SourceSessionFileBody) || journal.RestoredStateDigest != strideE10Digest("restore-state", stateRaw) {
		return errors.New("signed migration restore journal binding drift")
	}
	return nil
}

func strideE10MigrationRestoreBackupDigest(backup strideE10MigrationBackup) string {
	raw, _ := json.Marshal(backup)
	return strideE10Digest("restore-backup-semantic", raw)
}

type strideE10MigrationEnvelope struct {
	KeyID      string `json:"keyId"`
	KeyVersion uint64 `json:"keyVersion"`
	Payload    []byte `json:"payload"`
	MAC        string `json:"mac"`
}

const (
	strideE10MigrationPrivateMACDomain = "meetingassist/stride/e10/migration-private-state/v1\x00"
	strideE10MigrationPublicMACDomain  = "meetingassist/stride/e10/migration-public-receipt/v1\x00"
)

func validateStrideE10MigrationMACKey(key StrideE10MigrationMACKey) error {
	if strings.TrimSpace(key.ID) == "" || key.Version == 0 || len(key.Secret) < 32 {
		return errors.New("invalid managed migration MAC key")
	}
	return nil
}

func strideE10MigrationMACForDomain(key StrideE10MigrationMACKey, domain string, payload []byte) string {
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write([]byte(key.ID))
	_, _ = mac.Write([]byte(fmt.Sprintf("\x00%d\x00", key.Version)))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func strideE10MigrationMAC(key StrideE10MigrationMACKey, payload []byte) string {
	return strideE10MigrationMACForDomain(key, strideE10MigrationPrivateMACDomain, payload)
}

func strideE10MarshalSigned(ctx context.Context, keys StrideE10MigrationKeyring, value any) ([]byte, error) {
	if keys == nil {
		return nil, errors.New("managed migration keyring is required")
	}
	key, err := keys.CurrentStrideE10MigrationKey(ctx)
	if err != nil || validateStrideE10MigrationMACKey(key) != nil {
		return nil, errors.New("current managed migration key is unavailable")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	envelope := strideE10MigrationEnvelope{KeyID: key.ID, KeyVersion: key.Version, Payload: payload, MAC: strideE10MigrationMAC(key, payload)}
	return json.MarshalIndent(envelope, "", "  ")
}

func strideE10UnmarshalSigned(ctx context.Context, keys StrideE10MigrationKeyring, raw []byte, destination any) error {
	return strideE10UnmarshalSignedDomain(ctx, keys, raw, destination, strideE10MigrationPrivateMACDomain)
}

func strideE10UnmarshalSignedDomain(ctx context.Context, keys StrideE10MigrationKeyring, raw []byte, destination any, domain string) error {
	if keys == nil {
		return errors.New("managed migration keyring is required")
	}
	var envelope strideE10MigrationEnvelope
	if json.Unmarshal(raw, &envelope) != nil || strings.TrimSpace(envelope.KeyID) == "" || envelope.KeyVersion == 0 || len(envelope.Payload) == 0 {
		return errors.New("malformed signed migration state")
	}
	key, err := keys.ResolveStrideE10MigrationKey(ctx, envelope.KeyID, envelope.KeyVersion)
	if err != nil || validateStrideE10MigrationMACKey(key) != nil || key.ID != envelope.KeyID || key.Version != envelope.KeyVersion {
		return errors.New("managed migration key is unavailable")
	}
	want, err := hex.DecodeString(envelope.MAC)
	if err != nil || !hmac.Equal(want, mustDecodeStrideE10Hex(strideE10MigrationMACForDomain(key, domain, envelope.Payload))) {
		return errors.New("migration state MAC mismatch")
	}
	if err := json.Unmarshal(envelope.Payload, destination); err != nil {
		return fmt.Errorf("malformed signed migration payload: %w", err)
	}
	return nil
}

func strideE10MarshalPublicReceipt(ctx context.Context, keys StrideE10MigrationKeyring, receipt StrideE10MigrationReceipt) ([]byte, error) {
	if keys == nil {
		return nil, errors.New("managed migration keyring is required")
	}
	key, err := keys.CurrentStrideE10MigrationKey(ctx)
	if err != nil || validateStrideE10MigrationMACKey(key) != nil {
		return nil, errors.New("current managed migration key is unavailable")
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	envelope := strideE10MigrationEnvelope{KeyID: key.ID, KeyVersion: key.Version, Payload: payload, MAC: strideE10MigrationMACForDomain(key, strideE10MigrationPublicMACDomain, payload)}
	return json.MarshalIndent(envelope, "", "  ")
}

// VerifyStrideE10MigrationPublicReceipt verifies the separately publishable,
// body-minimized receipt. Private migration envelopes are rejected by the
// distinct MAC domain.
func VerifyStrideE10MigrationPublicReceipt(ctx context.Context, keys StrideE10MigrationKeyring, raw []byte) (StrideE10MigrationReceipt, error) {
	var receipt StrideE10MigrationReceipt
	if err := strideE10UnmarshalSignedDomain(ctx, keys, raw, &receipt, strideE10MigrationPublicMACDomain); err != nil {
		return StrideE10MigrationReceipt{}, err
	}
	if receipt.Version != strideE10MigrationStateVersion || receipt.MigratedRoots != 7 || receipt.MigratedMemberships != 7 || receipt.OrganizationName != "Bonfire" || receipt.OrganizationSlug != "bonfire" || receipt.SourceHighWater == 0 || receipt.TargetHighWater <= receipt.SourceHighWater || receipt.TargetHighWater-receipt.SourceHighWater != StrideE10MigrationExpectedTargetDelta || receipt.OperatorDigest == receipt.ReviewerDigest {
		return StrideE10MigrationReceipt{}, errors.New("invalid public migration receipt")
	}
	for label, digest := range map[string]string{
		"organization ID": receipt.OrganizationID, "no-extra-account": receipt.NoExtraAccountDigest,
		"schema": receipt.SchemaDigest, "policy": receipt.PolicyDigest, "migration": receipt.MigrationDigest,
		"switch": receipt.SwitchDigest, "source": receipt.SourceDigest, "target": receipt.TargetDigest,
		"backup": receipt.BackupDigest, "rollback": receipt.RollbackDigest,
		"backup identity": receipt.BackupIdentityDigest, "rollback identity": receipt.RollbackIdentityDigest,
		"operator": receipt.OperatorDigest, "reviewer": receipt.ReviewerDigest,
	} {
		if label == "organization ID" {
			if !strings.HasPrefix(digest, "organization_") {
				return StrideE10MigrationReceipt{}, errors.New("invalid public migration organization")
			}
			continue
		}
		if validateStrideE10ContractDigest(label, digest) != nil {
			return StrideE10MigrationReceipt{}, errors.New("invalid public migration receipt digest")
		}
	}
	return receipt, nil
}

func strideE10WritePublicReceipt(ctx context.Context, path string, keys StrideE10MigrationKeyring, receipt StrideE10MigrationReceipt) error {
	raw, err := strideE10MarshalPublicReceipt(ctx, keys, receipt)
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(path, raw, 0o644)
}

func mustDecodeStrideE10Hex(value string) []byte {
	decoded, _ := hex.DecodeString(value)
	return decoded
}

func strideE10WriteSigned(ctx context.Context, path string, keys StrideE10MigrationKeyring, value any) error {
	raw, err := strideE10MarshalSigned(ctx, keys, value)
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(path, raw, 0o600)
}

type StrideE10CanonicalShadow struct {
	PersonID     string
	MembershipID string
	Role         string
	Snapshot     StrideE10CredentialSnapshot
}

type StrideE10MigrationResult struct {
	Receipt         StrideE10MigrationReceipt
	PrivateManifest StrideE10PrivateMigrationManifest
	Legacy          []StrideE10CredentialSnapshot
	Canonical       []StrideE10CanonicalShadow
	input           StrideE10MigrationInput
}

func (r StrideE10MigrationResult) RollbackInput() StrideE10MigrationInput {
	return cloneStrideE10MigrationInput(r.input)
}

func strideE10Digest(domain string, values ...[]byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte("meetingassist/stride/e10/migration-rehearsal/v1/" + domain + "\x00"))
	for _, value := range values {
		_, _ = h.Write([]byte(fmt.Sprintf("%d:", len(value))))
		_, _ = h.Write(value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func validateStrideE10ContractDigest(label, value string) error {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
		return fmt.Errorf("%s must be a lowercase SHA-256 digest", label)
	}
	return nil
}

func cloneStrideE10MigrationBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func cloneStrideE10CredentialSnapshot(snapshot StrideE10CredentialSnapshot) StrideE10CredentialSnapshot {
	snapshot.AccountBody = cloneStrideE10MigrationBytes(snapshot.AccountBody)
	snapshot.PasswordBody = cloneStrideE10MigrationBytes(snapshot.PasswordBody)
	snapshot.ProfileBody = cloneStrideE10MigrationBytes(snapshot.ProfileBody)
	snapshot.AvatarBody = cloneStrideE10MigrationBytes(snapshot.AvatarBody)
	snapshot.PasskeyBody = cloneStrideE10MigrationBytes(snapshot.PasskeyBody)
	snapshot.SessionBody = cloneStrideE10MigrationBytes(snapshot.SessionBody)
	snapshot.PriorSessions = append([]StrideE10SessionSnapshot(nil), snapshot.PriorSessions...)
	return snapshot
}

func cloneStrideE10MigrationInput(input StrideE10MigrationInput) StrideE10MigrationInput {
	cloned := input
	cloned.Credentials = make([]StrideE10CredentialSnapshot, len(input.Credentials))
	for index, snapshot := range input.Credentials {
		cloned.Credentials[index] = cloneStrideE10CredentialSnapshot(snapshot)
	}
	cloned.Extras = make([]StrideE10PreservedAccountSnapshot, len(input.Extras))
	for index, extra := range input.Extras {
		cloned.Extras[index] = StrideE10PreservedAccountSnapshot{Subject: extra.Subject, Body: cloneStrideE10MigrationBytes(extra.Body)}
	}
	cloned.SessionExtras = make([]StrideE10PreservedSessionSnapshot, len(input.SessionExtras))
	for index, extra := range input.SessionExtras {
		cloned.SessionExtras[index] = StrideE10PreservedSessionSnapshot{Hash: extra.Hash, Body: cloneStrideE10MigrationBytes(extra.Body)}
	}
	cloned.SourceAccountFileBody = cloneStrideE10MigrationBytes(input.SourceAccountFileBody)
	cloned.SourceSessionFileBody = cloneStrideE10MigrationBytes(input.SourceSessionFileBody)
	return cloned
}

type strideE10ValidatedInput struct {
	input                 StrideE10MigrationInput
	rosterAggregateDigest string
	noExtraAccountDigest  string
	extraSessionDigest    string
	sourceDigest          string
	backupDigest          string
	rollbackDigest        string
}

func validateStrideE10MigrationInput(input StrideE10MigrationInput) (strideE10ValidatedInput, error) {
	if input.SourceAccountFileBody == nil || input.SourceSessionFileBody == nil {
		return strideE10ValidatedInput{}, errors.New("authoritative account/session source file bodies are required")
	}
	for label, value := range map[string]string{
		"schema digest":                input.SchemaDigest,
		"policy digest":                input.PolicyDigest,
		"migration digest":             input.MigrationDigest,
		"switch digest":                input.SwitchDigest,
		"backup identity digest":       input.BackupIdentityDigest,
		"rollback identity digest":     input.RollbackIdentityDigest,
		"operator digest":              input.OperatorDigest,
		"reviewer digest":              input.ReviewerDigest,
		"source store identity digest": input.SourceStoreIdentityDigest,
	} {
		if err := validateStrideE10ContractDigest(label, value); err != nil {
			return strideE10ValidatedInput{}, err
		}
	}
	if input.OrganizationName != "Bonfire" || input.OrganizationSlug != "bonfire" {
		return strideE10ValidatedInput{}, errors.New("organization identity must be exact Bonfire/bonfire")
	}
	if input.SchemaDigest == input.MigrationDigest {
		return strideE10ValidatedInput{}, errors.New("migration digest must be distinct from schema digest")
	}
	if input.OperatorDigest == input.ReviewerDigest {
		return strideE10ValidatedInput{}, errors.New("operator and reviewer must be distinct")
	}
	if input.SourceHighWater == 0 || input.TargetHighWater < input.SourceHighWater {
		return strideE10ValidatedInput{}, errors.New("source/target high-water bounds are invalid")
	}
	if len(input.Credentials) != len(seededAccounts) {
		return strideE10ValidatedInput{}, fmt.Errorf("migration requires exactly %d credential snapshots", len(seededAccounts))
	}

	expected := make(map[string]string, len(seededAccounts))
	for _, seed := range seededAccounts {
		role := "member"
		if normalizeAccountEmail(seed.Email) == "aj@shareability.com" {
			role = "owner"
		}
		expected[normalizeAccountEmail(seed.Email)] = role
	}

	canonical := cloneStrideE10MigrationInput(input)
	seen := make(map[string]struct{}, len(canonical.Credentials)+len(canonical.Extras))
	owners := 0
	for index := range canonical.Credentials {
		snapshot := &canonical.Credentials[index]
		subject := normalizeAccountEmail(snapshot.Subject)
		expectedRole, ok := expected[subject]
		if !ok {
			return strideE10ValidatedInput{}, fmt.Errorf("credential snapshot has non-roster or ambiguous subject")
		}
		if _, exists := seen[subject]; exists {
			return strideE10ValidatedInput{}, fmt.Errorf("duplicate normalized credential subject")
		}
		seen[subject] = struct{}{}
		if snapshot.Role != expectedRole {
			return strideE10ValidatedInput{}, fmt.Errorf("role drift for credential subject")
		}
		if snapshot.Role == "owner" {
			owners++
		}
		for component, body := range map[string][]byte{"account": snapshot.AccountBody, "password": snapshot.PasswordBody, "profile": snapshot.ProfileBody, "avatar": snapshot.AvatarBody, "passkey": snapshot.PasskeyBody, "session": snapshot.SessionBody} {
			if body == nil {
				return strideE10ValidatedInput{}, fmt.Errorf("credential snapshot %s component must be authoritative, including explicit empty bodies", component)
			}
		}
		if err := validateStrideE10AccountSnapshot(*snapshot); err != nil {
			return strideE10ValidatedInput{}, err
		}
		seenSessions := make(map[string]struct{}, len(snapshot.PriorSessions))
		for _, session := range snapshot.PriorSessions {
			if err := validateStrideE10ContractDigest("prior session hash", session.Hash); err != nil || session.ExpiresAt.IsZero() {
				return strideE10ValidatedInput{}, errors.New("prior session hashes and expiries must be exact")
			}
			if _, duplicate := seenSessions[session.Hash]; duplicate {
				return strideE10ValidatedInput{}, errors.New("duplicate prior session hash")
			}
			seenSessions[session.Hash] = struct{}{}
		}
		sort.Slice(snapshot.PriorSessions, func(i, j int) bool { return snapshot.PriorSessions[i].Hash < snapshot.PriorSessions[j].Hash })
		snapshot.Subject = subject
	}
	if len(seen) != len(expected) || owners != 1 {
		return strideE10ValidatedInput{}, errors.New("missing credential subject or sole-owner invariant failed")
	}
	sort.Slice(canonical.Credentials, func(i, j int) bool {
		return canonical.Credentials[i].Subject < canonical.Credentials[j].Subject
	})

	for index := range canonical.Extras {
		extra := &canonical.Extras[index]
		subject := normalizeAccountEmail(extra.Subject)
		if subject == "" || extra.Body == nil {
			return strideE10ValidatedInput{}, errors.New("extra account snapshot must have a subject and explicit body")
		}
		if _, roster := expected[subject]; roster {
			return strideE10ValidatedInput{}, errors.New("extra account ambiguously overlaps the migration roster")
		}
		if _, exists := seen[subject]; exists {
			return strideE10ValidatedInput{}, errors.New("duplicate normalized extra account subject")
		}
		seen[subject] = struct{}{}
		extra.Subject = subject
	}
	sort.Slice(canonical.Extras, func(i, j int) bool { return canonical.Extras[i].Subject < canonical.Extras[j].Subject })
	seenSessionExtras := make(map[string]struct{}, len(canonical.SessionExtras))
	for index := range canonical.SessionExtras {
		extra := &canonical.SessionExtras[index]
		if !validStrideE10SessionHash(extra.Hash) || extra.Body == nil {
			return strideE10ValidatedInput{}, errors.New("preserved session extra must have an exact hash and body")
		}
		if _, duplicate := seenSessionExtras[extra.Hash]; duplicate {
			return strideE10ValidatedInput{}, errors.New("duplicate preserved session extra")
		}
		var record sessionRecord
		if json.Unmarshal(extra.Body, &record) != nil {
			return strideE10ValidatedInput{}, errors.New("malformed preserved session extra")
		}
		if _, mapped := seen[normalizeAccountEmail(record.Email)]; mapped {
			return strideE10ValidatedInput{}, errors.New("mapped account session cannot be preserved as an extra")
		}
		seenSessionExtras[extra.Hash] = struct{}{}
	}
	sort.Slice(canonical.SessionExtras, func(i, j int) bool { return canonical.SessionExtras[i].Hash < canonical.SessionExtras[j].Hash })
	if err := validateStrideE10SourceFileBodies(canonical); err != nil {
		return strideE10ValidatedInput{}, err
	}

	credentialBytes, _ := json.Marshal(canonical.Credentials)
	extraBytes, _ := json.Marshal(canonical.Extras)
	extraSessionBytes, _ := json.Marshal(canonical.SessionExtras)
	contractBytes, _ := json.Marshal(struct {
		OrganizationName, OrganizationSlug, SchemaDigest, PolicyDigest, MigrationDigest, SwitchDigest           string
		SourceHighWater, TargetHighWater                                                                        uint64
		BackupIdentityDigest, RollbackIdentityDigest, OperatorDigest, ReviewerDigest, SourceStoreIdentityDigest string
	}{canonical.OrganizationName, canonical.OrganizationSlug, canonical.SchemaDigest, canonical.PolicyDigest, canonical.MigrationDigest, canonical.SwitchDigest, canonical.SourceHighWater, canonical.TargetHighWater, canonical.BackupIdentityDigest, canonical.RollbackIdentityDigest, canonical.OperatorDigest, canonical.ReviewerDigest, canonical.SourceStoreIdentityDigest})
	subjects := make([][]byte, 0, len(canonical.Credentials))
	for _, snapshot := range canonical.Credentials {
		subjects = append(subjects, []byte(snapshot.Subject))
	}
	return strideE10ValidatedInput{
		input:                 canonical,
		rosterAggregateDigest: strideE10Digest("credential-subjects", subjects...),
		noExtraAccountDigest:  strideE10Digest("preserved-extras", extraBytes),
		extraSessionDigest:    strideE10Digest("preserved-session-extras", extraSessionBytes),
		sourceDigest:          strideE10Digest("source", credentialBytes, extraBytes, extraSessionBytes, contractBytes),
		backupDigest:          strideE10Digest("backup", credentialBytes, extraBytes, extraSessionBytes, contractBytes),
		rollbackDigest:        strideE10Digest("rollback", credentialBytes, extraBytes, extraSessionBytes, contractBytes),
	}, nil
}

func validateStrideE10AccountSnapshot(snapshot StrideE10CredentialSnapshot) error {
	var account userAccount
	if json.Unmarshal(snapshot.AccountBody, &account) != nil || normalizeAccountEmail(account.Email) != normalizeAccountEmail(snapshot.Subject) {
		return errors.New("authoritative account body does not match credential subject")
	}
	profileBody, _ := json.Marshal(struct {
		Name      string `json:"name"`
		ThemePref string `json:"themePref"`
	}{account.Name, account.ThemePref})
	passkeyBody, _ := json.Marshal(struct {
		WebAuthnHandle []byte               `json:"webauthnHandle"`
		Credentials    any                  `json:"credentials"`
		PasskeyAddedAt map[string]time.Time `json:"passkeyAddedAt"`
	}{account.WebAuthnHandle, account.Credentials, account.PasskeyAddedAt})
	passwordBody, _ := json.Marshal(struct {
		Hash      []byte    `json:"hash"`
		ChangedAt time.Time `json:"changedAt"`
	}{account.PasswordHash, account.PasswordChangedAt})
	if !hmac.Equal(profileBody, snapshot.ProfileBody) || !hmac.Equal(passkeyBody, snapshot.PasskeyBody) || !hmac.Equal(passwordBody, snapshot.PasswordBody) || !hmac.Equal([]byte(account.AvatarDataURL), snapshot.AvatarBody) {
		return errors.New("authoritative account component binding drift")
	}
	return nil
}

func validateStrideE10SourceFileBodies(input StrideE10MigrationInput) error {
	if input.SourceAccountFileBody == nil || input.SourceSessionFileBody == nil {
		return errors.New("missing authoritative source file bodies")
	}
	expectedAccounts := make(map[string]json.RawMessage, len(input.Credentials)+len(input.Extras))
	expectedSessions := map[string]sessionRecord{}
	addSessions := func(raw []byte) error {
		var captured []strideE10CapturedSession
		if json.Unmarshal(raw, &captured) != nil {
			return errors.New("malformed captured session body")
		}
		for _, session := range captured {
			if !validStrideE10SessionHash(session.Hash) {
				return errors.New("invalid captured session hash")
			}
			if _, duplicate := expectedSessions[session.Hash]; duplicate {
				return errors.New("duplicate captured session hash")
			}
			expectedSessions[session.Hash] = session.Record
		}
		return nil
	}
	for _, credential := range input.Credentials {
		key := normalizeAccountEmail(credential.Subject)
		if key == "" || expectedAccounts[key] != nil {
			return errors.New("ambiguous captured account body")
		}
		expectedAccounts[key] = cloneStrideE10MigrationBytes(credential.AccountBody)
		if err := addSessions(credential.SessionBody); err != nil {
			return err
		}
	}
	for _, extra := range input.Extras {
		var captured struct {
			Account  json.RawMessage            `json:"account"`
			Sessions []strideE10CapturedSession `json:"sessions"`
		}
		if json.Unmarshal(extra.Body, &captured) != nil {
			return errors.New("malformed preserved extra source body")
		}
		key := normalizeAccountEmail(extra.Subject)
		if key == "" || expectedAccounts[key] != nil || len(captured.Account) == 0 {
			return errors.New("ambiguous preserved extra source body")
		}
		expectedAccounts[key] = cloneStrideE10MigrationBytes(captured.Account)
		sessionBytes, _ := json.Marshal(captured.Sessions)
		if err := addSessions(sessionBytes); err != nil {
			return err
		}
	}
	for _, extra := range input.SessionExtras {
		if !validStrideE10SessionHash(extra.Hash) || extra.Body == nil {
			return errors.New("invalid preserved session extra")
		}
		if _, duplicate := expectedSessions[extra.Hash]; duplicate {
			return errors.New("duplicate preserved session hash")
		}
		var record sessionRecord
		if json.Unmarshal(extra.Body, &record) != nil {
			return errors.New("malformed preserved session record")
		}
		expectedSessions[extra.Hash] = record
	}
	var accounts []*userAccount
	if json.Unmarshal(input.SourceAccountFileBody, &accounts) != nil || len(accounts) != len(expectedAccounts) {
		return errors.New("authoritative account file cardinality drift")
	}
	seen := map[string]bool{}
	for _, account := range accounts {
		if account == nil {
			return errors.New("nil authoritative account file row")
		}
		key := normalizeAccountEmail(account.Email)
		raw, _ := json.Marshal(account)
		if seen[key] || !hmac.Equal(raw, expectedAccounts[key]) {
			return errors.New("authoritative account file/body drift")
		}
		seen[key] = true
	}
	var sessions map[string]sessionRecord
	if json.Unmarshal(input.SourceSessionFileBody, &sessions) != nil || len(sessions) != len(expectedSessions) {
		return errors.New("authoritative session file cardinality drift")
	}
	actualSessions, _ := json.Marshal(sessions)
	wantSessions, _ := json.Marshal(expectedSessions)
	if !hmac.Equal(actualSessions, wantSessions) {
		return errors.New("authoritative session file/body drift")
	}
	return nil
}

func strideE10OpaqueID(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(raw), nil
}

func strideE10BuildState(validated strideE10ValidatedInput, existing *strideE10MigrationState) (strideE10MigrationState, error) {
	receipt := StrideE10MigrationReceipt{
		Version:                strideE10MigrationStateVersion,
		OrganizationName:       validated.input.OrganizationName,
		OrganizationSlug:       validated.input.OrganizationSlug,
		MigratedRoots:          len(validated.input.Credentials),
		MigratedMemberships:    len(validated.input.Credentials),
		PreservedExtraAccounts: len(validated.input.Extras),
		PreservedExtraSessions: len(validated.input.SessionExtras),
		NoExtraAccountDigest:   validated.noExtraAccountDigest,
		SchemaDigest:           validated.input.SchemaDigest,
		PolicyDigest:           validated.input.PolicyDigest,
		MigrationDigest:        validated.input.MigrationDigest,
		SwitchDigest:           validated.input.SwitchDigest,
		SourceHighWater:        validated.input.SourceHighWater,
		TargetHighWater:        validated.input.TargetHighWater,
		SourceDigest:           validated.sourceDigest,
		BackupDigest:           validated.backupDigest,
		RollbackDigest:         validated.rollbackDigest,
		BackupIdentityDigest:   validated.input.BackupIdentityDigest,
		RollbackIdentityDigest: validated.input.RollbackIdentityDigest,
		OperatorDigest:         validated.input.OperatorDigest,
		ReviewerDigest:         validated.input.ReviewerDigest,
	}
	if existing == nil {
		var err error
		receipt.OrganizationID, err = strideE10OpaqueID("organization")
		if err != nil {
			return strideE10MigrationState{}, err
		}
	} else {
		receipt.OrganizationID = existing.Manifest.OrganizationID
	}

	manifest := StrideE10PrivateMigrationManifest{
		Version:                   receipt.Version,
		OrganizationID:            receipt.OrganizationID,
		OrganizationName:          receipt.OrganizationName,
		OrganizationSlug:          receipt.OrganizationSlug,
		SchemaDigest:              receipt.SchemaDigest,
		PolicyDigest:              receipt.PolicyDigest,
		MigrationDigest:           receipt.MigrationDigest,
		SwitchDigest:              receipt.SwitchDigest,
		SourceHighWater:           receipt.SourceHighWater,
		TargetHighWater:           receipt.TargetHighWater,
		SourceDigest:              receipt.SourceDigest,
		BackupIdentityDigest:      validated.input.BackupIdentityDigest,
		RollbackIdentityDigest:    validated.input.RollbackIdentityDigest,
		BackupDigest:              receipt.BackupDigest,
		RollbackDigest:            receipt.RollbackDigest,
		OperatorDigest:            receipt.OperatorDigest,
		ReviewerDigest:            receipt.ReviewerDigest,
		SourceStoreIdentityDigest: validated.input.SourceStoreIdentityDigest,
		CredentialSubjectDigest:   validated.rosterAggregateDigest,
		NoExtraAccountDigest:      receipt.NoExtraAccountDigest,
		PreservedExtraAccounts:    receipt.PreservedExtraAccounts,
		PreservedExtraSessions:    receipt.PreservedExtraSessions,
		ExtraSessionDigest:        validated.extraSessionDigest,
		Bindings:                  make([]StrideE10PrivateMigrationBinding, len(validated.input.Credentials)),
	}
	for index, snapshot := range validated.input.Credentials {
		binding := StrideE10PrivateMigrationBinding{
			NormalizedSubject:  snapshot.Subject,
			MembershipRevision: 1,
			Role:               snapshot.Role,
			CredentialDigest:   strideE10Digest("password", snapshot.PasswordBody),
			ProfileDigest:      strideE10Digest("profile", snapshot.ProfileBody),
			AvatarDigest:       strideE10Digest("avatar", snapshot.AvatarBody),
			PasskeyDigest:      strideE10Digest("passkey", snapshot.PasskeyBody),
			SessionDigest:      strideE10Digest("session", snapshot.SessionBody),
			PriorSessions:      append([]StrideE10SessionSnapshot(nil), snapshot.PriorSessions...),
		}
		if existing == nil {
			var err error
			binding.PersonID, err = strideE10OpaqueID("person")
			if err != nil {
				return strideE10MigrationState{}, err
			}
			binding.MembershipID, err = strideE10OpaqueID("membership")
			if err != nil {
				return strideE10MigrationState{}, err
			}
		} else {
			if index >= len(existing.Manifest.Bindings) || existing.Manifest.Bindings[index].NormalizedSubject != binding.NormalizedSubject {
				return strideE10MigrationState{}, errors.New("persisted subject mapping drift")
			}
			binding.PersonID = existing.Manifest.Bindings[index].PersonID
			binding.MembershipID = existing.Manifest.Bindings[index].MembershipID
		}
		manifest.Bindings[index] = binding
	}
	targetMaterial := manifest
	targetMaterial.TargetDigest = ""
	targetBytes, _ := json.Marshal(targetMaterial)
	receipt.TargetDigest = strideE10Digest("target", targetBytes)
	manifest.TargetDigest = receipt.TargetDigest
	return strideE10MigrationState{Receipt: receipt, Manifest: manifest}, nil
}

func validateStrideE10PersistedState(state strideE10MigrationState) error {
	receipt, manifest := state.Receipt, state.Manifest
	if receipt.Version != strideE10MigrationStateVersion || manifest.Version != strideE10MigrationStateVersion || receipt.MigratedRoots != len(seededAccounts) || receipt.MigratedMemberships != len(seededAccounts) || len(manifest.Bindings) != len(seededAccounts) {
		return errors.New("invalid migration state cardinality or version")
	}
	if receipt.OrganizationID != manifest.OrganizationID || !strings.HasPrefix(manifest.OrganizationID, "organization_") {
		return errors.New("invalid migration organization ID")
	}
	if manifest.OrganizationName != "Bonfire" || manifest.OrganizationSlug != "bonfire" || manifest.SchemaDigest == manifest.MigrationDigest || manifest.OperatorDigest == manifest.ReviewerDigest || manifest.SourceHighWater == 0 || manifest.TargetHighWater < manifest.SourceHighWater {
		return errors.New("invalid persisted manifest authority bindings")
	}
	if receipt.OrganizationName != manifest.OrganizationName || receipt.OrganizationSlug != manifest.OrganizationSlug ||
		receipt.SchemaDigest != manifest.SchemaDigest || receipt.PolicyDigest != manifest.PolicyDigest || receipt.MigrationDigest != manifest.MigrationDigest || receipt.SwitchDigest != manifest.SwitchDigest ||
		receipt.SourceHighWater != manifest.SourceHighWater || receipt.TargetHighWater != manifest.TargetHighWater || receipt.SourceDigest != manifest.SourceDigest ||
		receipt.BackupIdentityDigest != manifest.BackupIdentityDigest || receipt.RollbackIdentityDigest != manifest.RollbackIdentityDigest || receipt.BackupDigest != manifest.BackupDigest || receipt.RollbackDigest != manifest.RollbackDigest ||
		receipt.OperatorDigest != manifest.OperatorDigest || receipt.ReviewerDigest != manifest.ReviewerDigest || receipt.NoExtraAccountDigest != manifest.NoExtraAccountDigest || receipt.PreservedExtraAccounts != manifest.PreservedExtraAccounts || receipt.PreservedExtraSessions != manifest.PreservedExtraSessions {
		return errors.New("public receipt and private manifest drift")
	}
	if manifest.SourceStoreIdentityDigest == "" {
		return errors.New("missing authoritative source store identity binding")
	}
	owners := 0
	seenIDs := map[string]struct{}{manifest.OrganizationID: {}}
	seenSubjects := make(map[string]struct{}, len(manifest.Bindings))
	expectedRoles := make(map[string]string, len(seededAccounts))
	for _, seed := range seededAccounts {
		role := "member"
		if normalizeAccountEmail(seed.Email) == "aj@shareability.com" {
			role = "owner"
		}
		expectedRoles[normalizeAccountEmail(seed.Email)] = role
	}
	for _, binding := range manifest.Bindings {
		if binding.NormalizedSubject != normalizeAccountEmail(binding.NormalizedSubject) || expectedRoles[binding.NormalizedSubject] != binding.Role {
			return errors.New("persisted roster subject or role drift")
		}
		if binding.Role == "owner" {
			owners++
		} else if binding.Role != "member" {
			return errors.New("invalid persisted membership role")
		}
		if !strings.HasPrefix(binding.PersonID, "person_") || !strings.HasPrefix(binding.MembershipID, "membership_") {
			return errors.New("invalid persisted opaque ID")
		}
		for _, id := range []string{binding.PersonID, binding.MembershipID} {
			if _, exists := seenIDs[id]; exists {
				return errors.New("duplicate persisted opaque ID")
			}
			seenIDs[id] = struct{}{}
		}
		if binding.MembershipRevision != 1 {
			return errors.New("invalid persisted membership revision")
		}
		if _, exists := seenSubjects[binding.NormalizedSubject]; exists {
			return errors.New("duplicate persisted credential subject digest")
		}
		seenSubjects[binding.NormalizedSubject] = struct{}{}
		for label, digest := range map[string]string{
			"credential digest": binding.CredentialDigest,
			"profile digest":    binding.ProfileDigest,
			"avatar digest":     binding.AvatarDigest,
			"passkey digest":    binding.PasskeyDigest,
			"session digest":    binding.SessionDigest,
		} {
			if err := validateStrideE10ContractDigest(label, digest); err != nil {
				return err
			}
		}
		seenSessionHashes := make(map[string]struct{}, len(binding.PriorSessions))
		for _, session := range binding.PriorSessions {
			if err := validateStrideE10ContractDigest("prior session hash", session.Hash); err != nil || session.ExpiresAt.IsZero() {
				return errors.New("invalid persisted session hash or expiry")
			}
			if _, duplicate := seenSessionHashes[session.Hash]; duplicate {
				return errors.New("duplicate persisted session hash")
			}
			seenSessionHashes[session.Hash] = struct{}{}
		}
	}
	if owners != 1 || len(seenSubjects) != len(expectedRoles) {
		return errors.New("persisted state must have exactly one owner")
	}
	for label, digest := range map[string]string{
		"schema digest":                manifest.SchemaDigest,
		"policy digest":                manifest.PolicyDigest,
		"migration digest":             manifest.MigrationDigest,
		"switch digest":                manifest.SwitchDigest,
		"source digest":                manifest.SourceDigest,
		"backup identity digest":       manifest.BackupIdentityDigest,
		"rollback identity digest":     manifest.RollbackIdentityDigest,
		"backup digest":                manifest.BackupDigest,
		"rollback digest":              manifest.RollbackDigest,
		"operator digest":              manifest.OperatorDigest,
		"reviewer digest":              manifest.ReviewerDigest,
		"source store identity digest": manifest.SourceStoreIdentityDigest,
		"credential subject digest":    manifest.CredentialSubjectDigest,
		"no-extra-account digest":      manifest.NoExtraAccountDigest,
		"extra-session digest":         manifest.ExtraSessionDigest,
	} {
		if err := validateStrideE10ContractDigest(label, digest); err != nil {
			return err
		}
	}
	targetMaterial := manifest
	targetMaterial.TargetDigest = ""
	targetBytes, _ := json.Marshal(targetMaterial)
	if strideE10Digest("target", targetBytes) != receipt.TargetDigest || manifest.TargetDigest != receipt.TargetDigest {
		return errors.New("persisted target digest drift")
	}
	return nil
}

func validateStrideE10MigrationBackup(backup strideE10MigrationBackup) error {
	if backup.Version != strideE10MigrationStateVersion || !strings.HasPrefix(backup.BackupID, "backup_") || strideE10SourceSnapshotDigest(backup.Input) != backup.SourceSnapshotDigest {
		return errors.New("invalid signed migration backup source binding")
	}
	if err := validateStrideE10PersistedState(backup.State); err != nil {
		return err
	}
	if err := validateStrideE10PersistedState(backup.PreparedState); err != nil {
		return errors.New("signed migration backup prepared identity state drift")
	}
	preparedManifest, err := strideE10BuildCanonicalTargetManifest(backup.PreparedState)
	if err != nil || preparedManifest.Digest != backup.TargetManifest.Digest || validateStrideE10CanonicalTargetManifest(backup.TargetManifest) != nil {
		return errors.New("signed migration backup canonical target manifest drift")
	}
	validated, err := validateStrideE10MigrationInput(backup.Input)
	if err != nil {
		return err
	}
	wantBackupIdentity := strideE10Digest("backup-identity", []byte(backup.BackupID))
	wantOperationID := strideE10MigrationWriterOperationID(backup.BackupID, backup.SourceSnapshotDigest, backup.Input.MigrationDigest)
	if backup.WriterOperationID != wantOperationID || backup.WriterReceipt == nil {
		return errors.New("signed migration backup is missing its durable writer receipt")
	}
	if backup.TargetBeforeHighWater != backup.WriterReceipt.BeforeHighWater || backup.TargetBeforeDigest != backup.WriterReceipt.BeforeSnapshotDigest {
		return errors.New("signed migration backup target recovery identity drift")
	}
	request := StrideE10MigrationWriteRequest{OperationID: wantOperationID, MigrationDigest: backup.Input.MigrationDigest, SourceDigest: backup.SourceSnapshotDigest, BackupIdentityDigest: wantBackupIdentity, ExpectedDelta: StrideE10MigrationExpectedTargetDelta, Manifest: backup.TargetManifest}
	if err := validateStrideE10MigrationWriteReceipt(request, *backup.WriterReceipt); err != nil || backup.WriterReceipt.BeforeHighWater != backup.Input.SourceHighWater || backup.WriterReceipt.AfterHighWater != backup.Input.TargetHighWater {
		return errors.New("signed migration backup writer receipt binding drift")
	}
	if backup.Input.BackupIdentityDigest != wantBackupIdentity || backup.State.Receipt.SourceDigest != validated.sourceDigest || backup.State.Receipt.BackupDigest != validated.backupDigest || backup.State.Receipt.RollbackDigest != validated.rollbackDigest || backup.State.Receipt.BackupIdentityDigest != wantBackupIdentity || backup.State.Manifest.SourceStoreIdentityDigest != backup.Input.SourceStoreIdentityDigest || backup.State.Manifest.ExtraSessionDigest != validated.extraSessionDigest || backup.State.Receipt.PreservedExtraSessions != len(validated.input.SessionExtras) {
		return errors.New("signed migration backup/state binding drift")
	}
	return nil
}

func runStrideE10MigrationSnapshot(ctx context.Context, path string, input StrideE10MigrationInput, keys StrideE10MigrationKeyring) (StrideE10MigrationResult, error) {
	original := cloneStrideE10MigrationInput(input)
	validated, err := validateStrideE10MigrationInput(input)
	if err != nil {
		return StrideE10MigrationResult{}, err
	}
	var existing *strideE10MigrationState
	if raw, readErr := os.ReadFile(path); readErr == nil {
		var state strideE10MigrationState
		if err := strideE10UnmarshalSigned(ctx, keys, raw, &state); err != nil {
			return StrideE10MigrationResult{}, err
		}
		if err := validateStrideE10PersistedState(state); err != nil {
			return StrideE10MigrationResult{}, err
		}
		existing = &state
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return StrideE10MigrationResult{}, readErr
	}

	state, err := strideE10BuildState(validated, existing)
	if err != nil {
		return StrideE10MigrationResult{}, err
	}
	if existing != nil {
		existingBytes, _ := json.Marshal(existing)
		stateBytes, _ := json.Marshal(state)
		if string(existingBytes) != string(stateBytes) {
			return StrideE10MigrationResult{}, errors.New("migration input, contract, role, or digest drift from persisted state")
		}
	}
	// Always reseal with the current managed key. This makes rotation explicit
	// while the payload and opaque identities remain stable.
	if err := strideE10WriteSigned(ctx, path, keys, state); err != nil {
		return StrideE10MigrationResult{}, err
	}

	result := StrideE10MigrationResult{Receipt: state.Receipt, PrivateManifest: state.Manifest, input: original}
	result.Legacy = make([]StrideE10CredentialSnapshot, len(validated.input.Credentials))
	result.Canonical = make([]StrideE10CanonicalShadow, len(validated.input.Credentials))
	for index, snapshot := range validated.input.Credentials {
		result.Legacy[index] = cloneStrideE10CredentialSnapshot(snapshot)
		result.Canonical[index] = StrideE10CanonicalShadow{
			PersonID:     state.Manifest.Bindings[index].PersonID,
			MembershipID: state.Manifest.Bindings[index].MembershipID,
			Role:         state.Manifest.Bindings[index].Role,
			Snapshot:     cloneStrideE10CredentialSnapshot(snapshot),
		}
	}
	return result, nil
}

func strideE10SourceSnapshotDigest(input StrideE10MigrationInput) string {
	material, _ := json.Marshal(struct {
		Credentials               []StrideE10CredentialSnapshot
		Extras                    []StrideE10PreservedAccountSnapshot
		SessionExtras             []StrideE10PreservedSessionSnapshot
		SourceStoreIdentityDigest string
		SourceAccountFileBody     []byte
		SourceSessionFileBody     []byte
	}{input.Credentials, input.Extras, input.SessionExtras, input.SourceStoreIdentityDigest, input.SourceAccountFileBody, input.SourceSessionFileBody})
	return strideE10Digest("authoritative-source-snapshot", material)
}

func strideE10MigrationWriterOperationID(backupID, sourceDigest, migrationDigest string) string {
	digest := strideE10Digest("writer-operation", []byte(backupID), []byte(sourceDigest), []byte(migrationDigest))
	return "migration-write_" + digest[:24]
}

func strideE10MigrationWriteReceiptDigest(receipt StrideE10MigrationWriteReceipt) string {
	receipt.ReceiptDigest = ""
	raw, _ := json.Marshal(receipt)
	return strideE10Digest("writer-receipt", raw)
}

func strideE10CanonicalTargetManifestDigest(manifest StrideE10CanonicalTargetManifest) string {
	manifest.Digest = ""
	raw, _ := json.Marshal(manifest)
	return strideE10Digest("canonical-target-manifest", raw)
}

func strideE10TargetSnapshotDigest(highWater uint64, rows []StrideE10CanonicalTargetRow) string {
	raw, _ := json.Marshal(struct {
		HighWater uint64
		Rows      []StrideE10CanonicalTargetRow
	}{highWater, rows})
	return strideE10Digest("canonical-target-snapshot", raw)
}

func validateStrideE10CanonicalTargetManifest(manifest StrideE10CanonicalTargetManifest) error {
	if manifest.Version != strideE10MigrationStateVersion || len(manifest.Rows) != int(StrideE10MigrationExpectedTargetDelta) || manifest.Digest != strideE10CanonicalTargetManifestDigest(manifest) {
		return errors.New("canonical target manifest must contain exactly 15 bound objects")
	}
	counts := map[string]int{}
	seen := map[string]struct{}{}
	people := map[string]struct{}{}
	membershipPeople := map[string]struct{}{}
	organizationID := ""
	owners := 0
	for _, row := range manifest.Rows {
		if row.Revision != 1 || len(row.Body) == 0 || (row.Kind != "organization" && row.Kind != "person" && row.Kind != "membership") {
			return errors.New("invalid canonical target row")
		}
		if _, ok := seen[row.ID]; ok || strings.TrimSpace(row.ID) == "" {
			return errors.New("duplicate canonical target row")
		}
		seen[row.ID] = struct{}{}
		counts[row.Kind]++
		switch row.Kind {
		case "organization":
			var body struct{ OrganizationID, Name, Slug string }
			if json.Unmarshal(row.Body, &body) != nil || body.OrganizationID != row.ID || body.Name != "Bonfire" || body.Slug != "bonfire" {
				return errors.New("invalid canonical organization row")
			}
			organizationID = row.ID
		case "person":
			var body struct{ PersonID, CredentialDigest, ProfileDigest, AvatarDigest, PasskeyDigest, SessionDigest string }
			if json.Unmarshal(row.Body, &body) != nil || body.PersonID != row.ID {
				return errors.New("invalid canonical person row")
			}
			for _, digest := range []string{body.CredentialDigest, body.ProfileDigest, body.AvatarDigest, body.PasskeyDigest, body.SessionDigest} {
				if validateStrideE10ContractDigest("canonical person binding", digest) != nil {
					return errors.New("invalid canonical person digest binding")
				}
			}
			people[row.ID] = struct{}{}
		case "membership":
			var body struct{ MembershipID, PersonID, OrganizationID, Role string }
			if json.Unmarshal(row.Body, &body) != nil || body.MembershipID != row.ID || (body.Role != "owner" && body.Role != "member") {
				return errors.New("invalid canonical membership row")
			}
			if body.Role == "owner" {
				owners++
			}
			if _, duplicate := membershipPeople[body.PersonID]; duplicate {
				return errors.New("duplicate canonical membership person")
			}
			membershipPeople[body.PersonID] = struct{}{}
		}
	}
	if counts["organization"] != 1 || counts["person"] != 7 || counts["membership"] != 7 || owners != 1 || len(membershipPeople) != len(people) {
		return errors.New("canonical target manifest cardinality drift")
	}
	for _, row := range manifest.Rows {
		if row.Kind != "membership" {
			continue
		}
		var body struct{ PersonID, OrganizationID string }
		_ = json.Unmarshal(row.Body, &body)
		if body.OrganizationID != organizationID {
			return errors.New("canonical membership organization drift")
		}
		if _, ok := people[body.PersonID]; !ok {
			return errors.New("canonical membership person drift")
		}
	}
	return nil
}

func strideE10BuildCanonicalTargetManifest(state strideE10MigrationState) (StrideE10CanonicalTargetManifest, error) {
	rows := make([]StrideE10CanonicalTargetRow, 0, StrideE10MigrationExpectedTargetDelta)
	organizationBody, _ := json.Marshal(struct {
		OrganizationID string `json:"organizationId"`
		Name           string `json:"name"`
		Slug           string `json:"slug"`
	}{state.Manifest.OrganizationID, state.Manifest.OrganizationName, state.Manifest.OrganizationSlug})
	rows = append(rows, StrideE10CanonicalTargetRow{Kind: "organization", ID: state.Manifest.OrganizationID, Revision: 1, Body: organizationBody})
	for _, binding := range state.Manifest.Bindings {
		personBody, _ := json.Marshal(struct {
			PersonID         string `json:"personId"`
			CredentialDigest string `json:"credentialDigest"`
			ProfileDigest    string `json:"profileDigest"`
			AvatarDigest     string `json:"avatarDigest"`
			PasskeyDigest    string `json:"passkeyDigest"`
			SessionDigest    string `json:"sessionDigest"`
		}{binding.PersonID, binding.CredentialDigest, binding.ProfileDigest, binding.AvatarDigest, binding.PasskeyDigest, binding.SessionDigest})
		membershipBody, _ := json.Marshal(struct {
			MembershipID   string `json:"membershipId"`
			PersonID       string `json:"personId"`
			OrganizationID string `json:"organizationId"`
			Role           string `json:"role"`
		}{binding.MembershipID, binding.PersonID, state.Manifest.OrganizationID, binding.Role})
		rows = append(rows,
			StrideE10CanonicalTargetRow{Kind: "person", ID: binding.PersonID, Revision: 1, Body: personBody},
			StrideE10CanonicalTargetRow{Kind: "membership", ID: binding.MembershipID, Revision: 1, Body: membershipBody},
		)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Kind+"\x00"+rows[i].ID < rows[j].Kind+"\x00"+rows[j].ID })
	manifest := StrideE10CanonicalTargetManifest{Version: strideE10MigrationStateVersion, Rows: rows}
	manifest.Digest = strideE10CanonicalTargetManifestDigest(manifest)
	return manifest, validateStrideE10CanonicalTargetManifest(manifest)
}

func validateStrideE10MigrationTargetReadback(request StrideE10MigrationWriteRequest, receipt StrideE10MigrationWriteReceipt, readback StrideE10MigrationTargetReadback) error {
	if readback.HighWater != receipt.AfterHighWater || readback.SnapshotDigest != strideE10TargetSnapshotDigest(readback.HighWater, readback.Rows) || readback.SnapshotDigest != receipt.AfterSnapshotDigest {
		return errors.New("canonical target independent readback drift")
	}
	want, got := request.Manifest.Rows, readback.Rows
	wantRaw, _ := json.Marshal(want)
	gotRaw, _ := json.Marshal(got)
	if !hmac.Equal(wantRaw, gotRaw) {
		return errors.New("canonical target row readback drift")
	}
	return nil
}

func validateStrideE10MigrationWriteReceipt(request StrideE10MigrationWriteRequest, receipt StrideE10MigrationWriteReceipt) error {
	if validateStrideE10CanonicalTargetManifest(request.Manifest) != nil || receipt.OperationID != request.OperationID || receipt.MigrationDigest != request.MigrationDigest || receipt.SourceDigest != request.SourceDigest || receipt.BackupIdentityDigest != request.BackupIdentityDigest || receipt.ExpectedDelta != request.ExpectedDelta || receipt.ManifestDigest != request.Manifest.Digest || receipt.BeforeHighWater == 0 || receipt.AfterHighWater <= receipt.BeforeHighWater || receipt.AfterHighWater-receipt.BeforeHighWater != request.ExpectedDelta || receipt.BeforeSnapshotDigest == "" || receipt.AfterSnapshotDigest == "" || receipt.ReceiptDigest != strideE10MigrationWriteReceiptDigest(receipt) {
		return errors.New("target writer returned an invalid durable idempotency receipt")
	}
	return nil
}

func strideE10ApplyMigrationContract(input *StrideE10MigrationInput, contract StrideE10MigrationContractInput) {
	input.OrganizationName = contract.OrganizationName
	input.OrganizationSlug = contract.OrganizationSlug
	input.SchemaDigest = contract.SchemaDigest
	input.PolicyDigest = contract.PolicyDigest
	input.MigrationDigest = contract.MigrationDigest
	input.SwitchDigest = contract.SwitchDigest
	input.RollbackIdentityDigest = contract.RollbackIdentityDigest
	input.OperatorDigest = contract.OperatorDigest
	input.ReviewerDigest = contract.ReviewerDigest
}

func strideE10ResolvedMigrationPath(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return "", errors.New("invalid migration path")
	}
	cursor := filepath.Clean(abs)
	suffix := []string{}
	for {
		resolved, resolveErr := filepath.EvalSymlinks(cursor)
		if resolveErr == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", resolveErr
		}
		suffix = append(suffix, filepath.Base(cursor))
		cursor = parent
	}
}

func strideE10RequireDistinctMigrationPaths(named map[string]string) error {
	type migrationPathIdentity struct {
		label    string
		resolved string
		info     os.FileInfo
		exists   bool
	}
	seen := map[string]string{}
	identities := make([]migrationPathIdentity, 0, len(named))
	for label, path := range named {
		resolved, err := strideE10ResolvedMigrationPath(path)
		if err != nil {
			return fmt.Errorf("%s path is invalid: %w", label, err)
		}
		if prior, duplicate := seen[resolved]; duplicate {
			return fmt.Errorf("migration path alias between %s and %s", prior, label)
		}
		seen[resolved] = label
		identity := migrationPathIdentity{label: label, resolved: resolved}
		// Lstat distinguishes a missing path from an unreadable path without
		// silently following a late malformed link. Stat then resolves an
		// existing link to the object identity used by os.SameFile.
		if _, lstatErr := os.Lstat(resolved); lstatErr == nil {
			info, statErr := os.Stat(resolved)
			if statErr != nil {
				return fmt.Errorf("%s path identity is unavailable: %w", label, statErr)
			}
			identity.info, identity.exists = info, true
		} else if !errors.Is(lstatErr, os.ErrNotExist) {
			return fmt.Errorf("%s path identity is unavailable: %w", label, lstatErr)
		}
		identities = append(identities, identity)
	}
	for left := 0; left < len(identities); left++ {
		if !identities[left].exists {
			continue
		}
		for right := left + 1; right < len(identities); right++ {
			if identities[right].exists && os.SameFile(identities[left].info, identities[right].info) {
				return fmt.Errorf("migration file identity alias between %s and %s", identities[left].label, identities[right].label)
			}
		}
	}
	return nil
}

func strideE10ValidateOfflineRunTopology(config StrideE10MigrationRunConfig) error {
	source, sourceOK := config.Source.(strideE10OfflineMigrationSource)
	target, targetOK := config.Writer.(strideE10OfflineMigrationTarget)
	if !sourceOK || !targetOK {
		return errors.New("offline source and disposable target capabilities are required")
	}
	accountsPath, sessionsPath := source.strideE10MigrationSourcePaths()
	return strideE10RequireDistinctMigrationPaths(map[string]string{
		"source accounts": accountsPath, "source sessions": sessionsPath,
		"state": config.StatePath, "backup": config.BackupPath,
		"public receipt": config.PublicReceiptPath, "restore journal": config.StatePath + ".restore-journal",
		"disposable target": target.strideE10DisposableTargetPath(),
	})
}

// RunStrideE10MigrationRehearsal is the only proof-bearing entrypoint. It
// captures the installed account and session authorities, persists an
// independently identified signed backup before target mutation, accepts only
// the writer-observed fixed delta, recaptures the source epoch, and signs the
// resulting private state with the managed keyring.
func RunStrideE10MigrationRehearsal(ctx context.Context, config StrideE10MigrationRunConfig) (StrideE10MigrationResult, error) {
	if ctx == nil || config.Source == nil || config.Writer == nil || config.Keys == nil || strings.TrimSpace(config.StatePath) == "" || strings.TrimSpace(config.BackupPath) == "" || strings.TrimSpace(config.PublicReceiptPath) == "" {
		return StrideE10MigrationResult{}, errors.New("complete authoritative migration configuration is required")
	}
	if err := strideE10ValidateOfflineRunTopology(config); err != nil {
		return StrideE10MigrationResult{}, err
	}
	stateBefore, stateReadErr := os.ReadFile(config.StatePath)
	stateExisted := stateReadErr == nil
	if stateReadErr != nil && !errors.Is(stateReadErr, os.ErrNotExist) {
		return StrideE10MigrationResult{}, stateReadErr
	}
	publicBefore, publicReadErr := os.ReadFile(config.PublicReceiptPath)
	publicExisted := publicReadErr == nil
	if publicReadErr != nil && !errors.Is(publicReadErr, os.ErrNotExist) {
		return StrideE10MigrationResult{}, publicReadErr
	}
	captured, err := config.Source.CaptureStrideE10MigrationSource(ctx)
	if err != nil {
		return StrideE10MigrationResult{}, err
	}
	strideE10ApplyMigrationContract(&captured, config.Contract)
	sourceSnapshotDigest := strideE10SourceSnapshotDigest(captured)

	var backup strideE10MigrationBackup
	backupExists := false
	if raw, readErr := os.ReadFile(config.BackupPath); readErr == nil {
		if err := strideE10UnmarshalSigned(ctx, config.Keys, raw, &backup); err != nil {
			return StrideE10MigrationResult{}, err
		}
		backupExists = true
		if backup.Version != strideE10MigrationStateVersion || !strings.HasPrefix(backup.BackupID, "backup_") || backup.SourceSnapshotDigest != sourceSnapshotDigest || strideE10SourceSnapshotDigest(backup.Input) != sourceSnapshotDigest {
			return StrideE10MigrationResult{}, errors.New("authoritative source or backup binding drift")
		}
		if backup.State.Receipt.Version != 0 {
			if err := validateStrideE10MigrationBackup(backup); err != nil {
				return StrideE10MigrationResult{}, err
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return StrideE10MigrationResult{}, readErr
	}
	if !backupExists {
		backupID, err := strideE10OpaqueID("backup")
		if err != nil {
			return StrideE10MigrationResult{}, err
		}
		backup = strideE10MigrationBackup{Version: strideE10MigrationStateVersion, BackupID: backupID, SourceSnapshotDigest: sourceSnapshotDigest, Input: cloneStrideE10MigrationInput(captured)}
	}
	captured.BackupIdentityDigest = strideE10Digest("backup-identity", []byte(backup.BackupID))
	backup.Input.BackupIdentityDigest = captured.BackupIdentityDigest
	operationID := strideE10MigrationWriterOperationID(backup.BackupID, sourceSnapshotDigest, captured.MigrationDigest)
	if backup.WriterOperationID != "" && backup.WriterOperationID != operationID {
		return StrideE10MigrationResult{}, errors.New("signed backup writer operation drift")
	}
	backup.WriterOperationID = operationID
	// Stable opaque identities and the exact private 15-object target manifest
	// are materialized and signed before the target is allowed to mutate.
	var identityState *strideE10MigrationState
	if backup.PreparedState.Receipt.Version != 0 {
		identityState = &backup.PreparedState
	} else if backup.State.Receipt.Version != 0 {
		identityState = &backup.State
	} else if raw, readErr := os.ReadFile(config.StatePath); readErr == nil {
		var persisted strideE10MigrationState
		if err := strideE10UnmarshalSigned(ctx, config.Keys, raw, &persisted); err != nil {
			return StrideE10MigrationResult{}, err
		}
		identityState = &persisted
	}
	provisional := cloneStrideE10MigrationInput(captured)
	provisional.SourceHighWater = 1
	provisional.TargetHighWater = 1 + StrideE10MigrationExpectedTargetDelta
	validatedProvisional, err := validateStrideE10MigrationInput(provisional)
	if err != nil {
		return StrideE10MigrationResult{}, err
	}
	preparedState, err := strideE10BuildState(validatedProvisional, identityState)
	if err != nil {
		return StrideE10MigrationResult{}, err
	}
	targetManifest, err := strideE10BuildCanonicalTargetManifest(preparedState)
	if err != nil {
		return StrideE10MigrationResult{}, err
	}
	if backup.TargetManifest.Digest != "" && backup.TargetManifest.Digest != targetManifest.Digest {
		return StrideE10MigrationResult{}, fmt.Errorf("signed backup canonical target manifest drift: have %s want %s", backup.TargetManifest.Digest, targetManifest.Digest)
	}
	backup.TargetManifest = targetManifest
	backup.PreparedState = preparedState
	liveTarget, err := config.Writer.ReadStrideE10MigrationTarget(ctx, operationID)
	if err != nil {
		return StrideE10MigrationResult{}, err
	}
	if liveTarget.HighWater == 0 || liveTarget.SnapshotDigest != strideE10TargetSnapshotDigest(liveTarget.HighWater, liveTarget.Rows) {
		return StrideE10MigrationResult{}, errors.New("canonical target pre-apply readback drift")
	}
	if backup.TargetBeforeHighWater == 0 {
		backup.TargetBeforeHighWater = liveTarget.HighWater
		backup.TargetBeforeDigest = liveTarget.SnapshotDigest
	}
	// The signed private backup exists before the writer is allowed to mutate.
	if err := strideE10WriteSigned(ctx, config.BackupPath, config.Keys, backup); err != nil {
		return StrideE10MigrationResult{}, err
	}

	request := StrideE10MigrationWriteRequest{OperationID: operationID, MigrationDigest: captured.MigrationDigest, SourceDigest: sourceSnapshotDigest, BackupIdentityDigest: captured.BackupIdentityDigest, ExpectedDelta: StrideE10MigrationExpectedTargetDelta, Manifest: targetManifest}
	observation, err := config.Writer.ApplyStrideE10Migration(ctx, request)
	if err != nil {
		return StrideE10MigrationResult{}, err
	}
	recoverUntrustedApply := func(cause error) error {
		if backup.WriterReceipt != nil && liveTarget.HighWater == backup.WriterReceipt.AfterHighWater && liveTarget.SnapshotDigest == backup.WriterReceipt.AfterSnapshotDigest {
			unchanged, readErr := config.Writer.ReadStrideE10MigrationTarget(ctx, request.OperationID)
			if readErr == nil && unchanged.HighWater == liveTarget.HighWater && unchanged.SnapshotDigest == liveTarget.SnapshotDigest {
				return cause
			}
		}
		rollbackRequest := StrideE10MigrationRollbackRequest{OperationID: request.OperationID, MigrationDigest: request.MigrationDigest, SourceDigest: request.SourceDigest, BackupIdentityDigest: request.BackupIdentityDigest, ManifestDigest: request.Manifest.Digest, ExpectedBeforeHighWater: backup.TargetBeforeHighWater, ExpectedBeforeSnapshotDigest: backup.TargetBeforeDigest}
		rollback, rollbackErr := config.Writer.RollbackStrideE10Migration(ctx, rollbackRequest)
		if rollbackErr != nil {
			return fmt.Errorf("%v; untrusted canonical target apply recovery failed: %w", cause, rollbackErr)
		}
		readback, readErr := config.Writer.ReadStrideE10MigrationTarget(ctx, request.OperationID)
		if readErr != nil || rollback.ReceiptDigest != strideE10MigrationRollbackReceiptDigest(rollback) || rollback.RestoredHighWater != backup.TargetBeforeHighWater || rollback.RestoredDigest != backup.TargetBeforeDigest || readback.HighWater != backup.TargetBeforeHighWater || readback.SnapshotDigest != backup.TargetBeforeDigest {
			return fmt.Errorf("%v; untrusted canonical target apply recovery verification failed", cause)
		}
		return cause
	}
	if err := validateStrideE10MigrationWriteReceipt(request, observation.Receipt); err != nil {
		return StrideE10MigrationResult{}, recoverUntrustedApply(err)
	}
	if backup.WriterReceipt != nil && *backup.WriterReceipt != observation.Receipt {
		return StrideE10MigrationResult{}, recoverUntrustedApply(errors.New("durable target writer receipt drift on replay"))
	}
	backup.WriterReceipt = &observation.Receipt
	rollbackFailure := func(cause error) error {
		rollbackRequest := StrideE10MigrationRollbackRequest{OperationID: request.OperationID, MigrationDigest: request.MigrationDigest, SourceDigest: request.SourceDigest, BackupIdentityDigest: request.BackupIdentityDigest, ManifestDigest: request.Manifest.Digest, WriterReceiptDigest: observation.Receipt.ReceiptDigest, ExpectedBeforeHighWater: observation.Receipt.BeforeHighWater, ExpectedBeforeSnapshotDigest: observation.Receipt.BeforeSnapshotDigest}
		rollback, rollbackErr := config.Writer.RollbackStrideE10Migration(ctx, rollbackRequest)
		if rollbackErr != nil {
			return fmt.Errorf("%v; canonical target rollback failed: %w", cause, rollbackErr)
		}
		readback, readErr := config.Writer.ReadStrideE10MigrationTarget(ctx, request.OperationID)
		if readErr != nil || rollback.ReceiptDigest != strideE10MigrationRollbackReceiptDigest(rollback) || rollback.RestoredHighWater != observation.Receipt.BeforeHighWater || rollback.RestoredDigest != observation.Receipt.BeforeSnapshotDigest || readback.HighWater != observation.Receipt.BeforeHighWater || readback.SnapshotDigest != observation.Receipt.BeforeSnapshotDigest {
			return fmt.Errorf("%v; canonical target rollback verification failed", cause)
		}
		var restoreErr error
		if stateExisted {
			restoreErr = writeFileAtomicallyDurable(config.StatePath, stateBefore, 0o600)
		} else if removeErr := os.Remove(config.StatePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			restoreErr = removeErr
		}
		var publicRestoreErr error
		if publicExisted {
			publicRestoreErr = writeFileAtomicallyDurable(config.PublicReceiptPath, publicBefore, 0o644)
		} else if removeErr := os.Remove(config.PublicReceiptPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			publicRestoreErr = removeErr
		}
		backup.RollbackReceipt = &rollback
		backupErr := strideE10WriteSigned(ctx, config.BackupPath, config.Keys, backup)
		if restoreErr != nil || publicRestoreErr != nil || backupErr != nil {
			return fmt.Errorf("%v; rollback publication failed: state=%v public=%v backup=%v", cause, restoreErr, publicRestoreErr, backupErr)
		}
		return cause
	}
	readback, err := config.Writer.ReadStrideE10MigrationTarget(ctx, operationID)
	if err != nil {
		return StrideE10MigrationResult{}, rollbackFailure(err)
	}
	if err := validateStrideE10MigrationTargetReadback(request, observation.Receipt, readback); err != nil {
		return StrideE10MigrationResult{}, rollbackFailure(err)
	}
	if err := strideE10WriteSigned(ctx, config.BackupPath, config.Keys, backup); err != nil {
		return StrideE10MigrationResult{}, rollbackFailure(err)
	}
	captured.SourceHighWater = observation.Receipt.BeforeHighWater
	captured.TargetHighWater = observation.Receipt.AfterHighWater

	recaptured, err := config.Source.CaptureStrideE10MigrationSource(ctx)
	if err != nil {
		return StrideE10MigrationResult{}, rollbackFailure(err)
	}
	strideE10ApplyMigrationContract(&recaptured, config.Contract)
	if strideE10SourceSnapshotDigest(recaptured) != sourceSnapshotDigest {
		return StrideE10MigrationResult{}, rollbackFailure(errors.New("authoritative source changed during migration writer epoch"))
	}
	recaptured.BackupIdentityDigest = captured.BackupIdentityDigest
	recaptured.SourceHighWater = captured.SourceHighWater
	recaptured.TargetHighWater = captured.TargetHighWater
	validated, err := validateStrideE10MigrationInput(recaptured)
	if err != nil {
		return StrideE10MigrationResult{}, rollbackFailure(err)
	}
	_ = validated

	if backupExists && backup.Input.SourceHighWater != 0 && !reflectStrideE10MigrationInput(backup.Input, recaptured) {
		return StrideE10MigrationResult{}, rollbackFailure(errors.New("migration replay drift from signed backup"))
	}
	if _, err := os.Stat(config.StatePath); errors.Is(err, os.ErrNotExist) && backup.State.Receipt.Version != 0 {
		if err := validateStrideE10PersistedState(backup.State); err != nil {
			return StrideE10MigrationResult{}, rollbackFailure(err)
		}
		if err := strideE10WriteSigned(ctx, config.StatePath, config.Keys, backup.State); err != nil {
			return StrideE10MigrationResult{}, rollbackFailure(err)
		}
	}
	result, err := runStrideE10MigrationSnapshot(ctx, config.StatePath, recaptured, config.Keys)
	if err != nil {
		return StrideE10MigrationResult{}, rollbackFailure(err)
	}
	backup.Input = cloneStrideE10MigrationInput(recaptured)
	backup.State = strideE10MigrationState{Receipt: result.Receipt, Manifest: result.PrivateManifest}
	if err := strideE10WriteSigned(ctx, config.BackupPath, config.Keys, backup); err != nil {
		return StrideE10MigrationResult{}, rollbackFailure(err)
	}
	if err := strideE10WritePublicReceipt(ctx, config.PublicReceiptPath, config.Keys, result.Receipt); err != nil {
		return StrideE10MigrationResult{}, rollbackFailure(err)
	}
	return result, nil
}

func reflectStrideE10MigrationInput(left, right StrideE10MigrationInput) bool {
	leftBytes, _ := json.Marshal(left)
	rightBytes, _ := json.Marshal(right)
	return hmac.Equal(leftBytes, rightBytes)
}

func BackupStrideE10MigrationRehearsal(ctx context.Context, path string, keys StrideE10MigrationKeyring) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var backup strideE10MigrationBackup
	if err := strideE10UnmarshalSigned(ctx, keys, raw, &backup); err != nil {
		return nil, err
	}
	if err := validateStrideE10MigrationBackup(backup); err != nil {
		return nil, err
	}
	return cloneStrideE10MigrationBytes(raw), nil
}

func RestoreStrideE10MigrationRehearsal(ctx context.Context, statePath string, backupRaw []byte, keys StrideE10MigrationKeyring, writer StrideE10MigrationTargetWriter, restorer StrideE10MigrationSourceRestorer) error {
	var backup strideE10MigrationBackup
	if err := strideE10UnmarshalSigned(ctx, keys, backupRaw, &backup); err != nil {
		return err
	}
	if err := validateStrideE10MigrationBackup(backup); err != nil {
		return err
	}
	offlineTarget, targetOK := writer.(strideE10OfflineMigrationTarget)
	offlineRestorer, restorerOK := restorer.(strideE10OfflineMigrationRestorer)
	if writer == nil || restorer == nil || backup.WriterReceipt == nil || !targetOK || !restorerOK {
		return errors.New("coordinated target and source migration restorers are required")
	}
	accountsPath, sessionsPath := offlineRestorer.strideE10MigrationRestorePaths()
	if err := strideE10RequireDistinctMigrationPaths(map[string]string{
		"source accounts": accountsPath, "source sessions": sessionsPath,
		"state": statePath, "restore journal": statePath + ".restore-journal",
		"disposable target": offlineTarget.strideE10DisposableTargetPath(),
	}); err != nil {
		return err
	}
	request := StrideE10MigrationWriteRequest{OperationID: backup.WriterOperationID, MigrationDigest: backup.Input.MigrationDigest, SourceDigest: backup.SourceSnapshotDigest, BackupIdentityDigest: backup.Input.BackupIdentityDigest, ExpectedDelta: StrideE10MigrationExpectedTargetDelta, Manifest: backup.TargetManifest}
	receipt := *backup.WriterReceipt
	journalPath := statePath + ".restore-journal"
	stateRaw, _ := json.Marshal(backup.State)
	wantJournal := strideE10MigrationRestoreJournal{
		Version: strideE10MigrationStateVersion, BackupID: backup.BackupID, BackupDigest: strideE10MigrationRestoreBackupDigest(backup), OperationID: backup.WriterOperationID,
		WriterReceipt: receipt, TargetManifestDigest: backup.TargetManifest.Digest,
		SourceAccountsDigest: strideE10Digest("restore-accounts", backup.Input.SourceAccountFileBody), SourceSessionsDigest: strideE10Digest("restore-sessions", backup.Input.SourceSessionFileBody), RestoredStateDigest: strideE10Digest("restore-state", stateRaw),
	}
	journal := wantJournal
	if raw, readErr := os.ReadFile(journalPath); readErr == nil {
		if err := strideE10UnmarshalSigned(ctx, keys, raw, &journal); err != nil || validateStrideE10MigrationRestoreJournal(journal, backup, backupRaw) != nil {
			return errors.New("signed migration restore journal is invalid")
		}
		// A valid journal is always resealed before recovery proceeds. This
		// covers both in-progress and completed journals so an old key can be
		// retired without stranding crash recovery.
		if err := strideE10WriteSigned(ctx, journalPath, keys, journal); err != nil {
			return err
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	} else {
		journal.Phase = strideE10RestoreTargetRollbackStarted
		if err := strideE10WriteSigned(ctx, journalPath, keys, journal); err != nil {
			return err
		}
		if strideE10MigrationRestorePhaseHook != nil {
			if err := strideE10MigrationRestorePhaseHook(journal.Phase); err != nil {
				return err
			}
		}
	}
	advanceJournal := func(phase string) error {
		if strideE10RestorePhaseOrder(phase) <= strideE10RestorePhaseOrder(journal.Phase) {
			return nil
		}
		journal.Phase = phase
		if err := strideE10WriteSigned(ctx, journalPath, keys, journal); err != nil {
			return err
		}
		if strideE10MigrationRestorePhaseHook != nil {
			return strideE10MigrationRestorePhaseHook(phase)
		}
		return nil
	}
	recoverAppliedTarget := func(cause error) error {
		reapplied, reapplyErr := writer.ApplyStrideE10Migration(ctx, request)
		if reapplyErr != nil || reapplied.Receipt != receipt {
			return fmt.Errorf("%v; canonical target reapply recovery pending: %v", cause, reapplyErr)
		}
		readback, readErr := writer.ReadStrideE10MigrationTarget(ctx, request.OperationID)
		if readErr != nil || readback.HighWater != receipt.AfterHighWater || readback.SnapshotDigest != receipt.AfterSnapshotDigest {
			return fmt.Errorf("%v; canonical target reapply recovery verification failed", cause)
		}
		return cause
	}
	rollbackRequest := StrideE10MigrationRollbackRequest{OperationID: request.OperationID, MigrationDigest: request.MigrationDigest, SourceDigest: request.SourceDigest, BackupIdentityDigest: request.BackupIdentityDigest, ManifestDigest: request.Manifest.Digest, WriterReceiptDigest: receipt.ReceiptDigest, ExpectedBeforeHighWater: receipt.BeforeHighWater, ExpectedBeforeSnapshotDigest: receipt.BeforeSnapshotDigest}
	rollback, err := writer.RollbackStrideE10Migration(ctx, rollbackRequest)
	if err != nil {
		return fmt.Errorf("canonical target must roll back before source restore: %w", err)
	}
	readback, err := writer.ReadStrideE10MigrationTarget(ctx, request.OperationID)
	if err != nil || rollback.ReceiptDigest != strideE10MigrationRollbackReceiptDigest(rollback) || readback.HighWater != receipt.BeforeHighWater || readback.SnapshotDigest != receipt.BeforeSnapshotDigest {
		return recoverAppliedTarget(errors.New("canonical target rollback could not be independently verified before source restore"))
	}
	if err := advanceJournal(strideE10RestoreTargetRollbackVerified); err != nil {
		if errors.Is(err, errStrideE10MigrationAbruptRestart) {
			return err
		}
		return recoverAppliedTarget(err)
	}
	if err := advanceJournal(strideE10RestoreSourceStarted); err != nil {
		if errors.Is(err, errStrideE10MigrationAbruptRestart) {
			return err
		}
		return recoverAppliedTarget(err)
	}
	finalize := func() error {
		if err := advanceJournal(strideE10RestoreSourceVerified); err != nil {
			return err
		}
		if err := advanceJournal(strideE10RestoreStateStarted); err != nil {
			return err
		}
		if err := strideE10WriteSigned(ctx, statePath, keys, backup.State); err != nil {
			return err
		}
		if strideE10MigrationRestorePhaseHook != nil {
			if err := strideE10MigrationRestorePhaseHook(strideE10RestoreStateFileWritten); err != nil {
				return err
			}
		}
		return advanceJournal(strideE10RestoreCompleted)
	}
	progress := func(phase string) error { return advanceJournal(phase) }
	var restoreErr error
	if progressive, ok := restorer.(strideE10MigrationProgressRestorer); ok {
		restoreErr = progressive.RestoreStrideE10MigrationSourceWithProgress(ctx, cloneStrideE10MigrationInput(backup.Input), progress, finalize)
	} else {
		restoreErr = restorer.RestoreStrideE10MigrationSource(ctx, cloneStrideE10MigrationInput(backup.Input), finalize)
	}
	if restoreErr == nil {
		return nil
	}
	if errors.Is(restoreErr, errStrideE10MigrationAbruptRestart) {
		return restoreErr
	}
	// The source restorer is atomic: on any error it has returned both source
	// authorities to their exact pre-call bytes. Restore the canonical target to
	// its applied image before exposing the failure.
	return recoverAppliedTarget(restoreErr)
}
