package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	openAIToolJournalFormatVersion = "stride-openai-tool-journal-v1"
	openAIToolEnvelopeVersion      = "stride-openai-tool-envelope-v1"
	openAIToolJournalFileName      = "journal.json"
	openAIToolJournalLockName      = "journal.lock"
)

var (
	errOpenAIToolJournalTampered            = errors.New("OpenAI tool journal authentication failed")
	errOpenAIToolJournalRollback            = errors.New("OpenAI tool journal rollback detected")
	errOpenAIToolJournalConflict            = errors.New("OpenAI tool journal external high-water conflict")
	errOpenAIToolJournalCommittedUnverified = errors.New("OpenAI tool journal commit requires restart verification")
)

type openAIToolOperationState string

const (
	openAIToolStateReserved         openAIToolOperationState = "reserved"
	openAIToolStateEffectCommitted  openAIToolOperationState = "effect_committed"
	openAIToolStateContinuationSent openAIToolOperationState = "continuation_sent"
	openAIToolStateCompleted        openAIToolOperationState = "completed"
	openAIToolStateQuarantined      openAIToolOperationState = "quarantined"
)

type openAIToolRunState string

const (
	openAIToolRunActive     openAIToolRunState = "active"
	openAIToolRunCompleted  openAIToolRunState = "completed"
	openAIToolRunBlocked    openAIToolRunState = "blocked"
	openAIToolRunSuperseded openAIToolRunState = "superseded"
)

var errOpenAIToolRunMaxTurns = errors.New("OpenAI function-tool run reached its durable provider-turn limit")

type openAIToolJournalAnchor struct {
	Generation uint64 `json:"generation"`
	Digest     string `json:"digest"`
}

type openAIToolJournalHighWater interface {
	LoadOpenAIToolJournalAnchor(context.Context, string) (openAIToolJournalAnchor, error)
	CompareAndSwapOpenAIToolJournalAnchor(context.Context, string, openAIToolJournalAnchor, openAIToolJournalAnchor) error
}

type openAIToolJournalKeys struct {
	MACKeyID      string
	MACVersion    string
	MACKey        []byte
	AEADKeyID     string
	AEADVersion   string
	AEADKey       []byte
	EffectKeyID   string
	EffectVersion string
	EffectKey     []byte
}

type openAIToolJournalKeyring interface {
	CurrentOpenAIToolJournalKeys(context.Context) (openAIToolJournalKeys, error)
	OpenAIToolJournalMACKey(context.Context, string, string) ([]byte, error)
	OpenAIToolJournalAEADKey(context.Context, string, string) ([]byte, error)
	OpenAIToolJournalEffectKey(context.Context, string, string) ([]byte, error)
	ValidateOpenAIToolEffectRotationTarget(context.Context, string, string) error
	SignOpenAIToolRotationReceipt(context.Context, []byte) (string, string, string, error)
	VerifyOpenAIToolRotationReceipt(context.Context, string, string, []byte, string) error
}

type openAIToolAuthorityExpectation struct {
	TenantID              string `json:"tenant_id"`
	PersonID              string `json:"person_id"`
	RequesterAccount      string `json:"requester_account"`
	SessionDigest         string `json:"session_digest"`
	ActiveOrgSessionID    string `json:"active_organization_session_id"`
	ActiveOrgSessionRev   uint64 `json:"active_organization_session_revision"`
	MembershipID          string `json:"membership_id"`
	ActiveOrganizationID  string `json:"active_organization_id"`
	MembershipRevision    uint64 `json:"membership_revision"`
	OrganizationRevision  uint64 `json:"organization_revision"`
	ThreadID              string `json:"thread_id"`
	ArtifactID            string `json:"artifact_id"`
	ArtifactRevision      string `json:"artifact_revision"`
	SourceWindowDigest    string `json:"source_window_digest"`
	JobAuthority          string `json:"job_authority"`
	RequestPolicyRevision string `json:"request_policy_revision"`
	PolicyRevision        string `json:"policy_revision"`
	ToolName              string `json:"tool_name,omitempty"`
	ManifestDigest        string `json:"manifest_digest,omitempty"`
	SchemaDigest          string `json:"schema_digest,omitempty"`
	ArgumentsDigest       string `json:"arguments_digest,omitempty"`
}

func (expectation openAIToolAuthorityExpectation) validate() error {
	required := map[string]string{
		"tenant_id": expectation.TenantID, "person_id": expectation.PersonID,
		"requester_account": expectation.RequesterAccount, "session_digest": expectation.SessionDigest,
		"active_organization_session_id": expectation.ActiveOrgSessionID, "membership_id": expectation.MembershipID,
		"active_organization_id": expectation.ActiveOrganizationID, "thread_id": expectation.ThreadID,
		"artifact_id": expectation.ArtifactID, "artifact_revision": expectation.ArtifactRevision,
		"source_window_digest": expectation.SourceWindowDigest,
		"job_authority":        expectation.JobAuthority, "request_policy_revision": expectation.RequestPolicyRevision,
		"policy_revision": expectation.PolicyRevision,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("OpenAI tool authority expectation %s is required", name)
		}
	}
	if expectation.ActiveOrgSessionRev == 0 || expectation.MembershipRevision == 0 || expectation.OrganizationRevision == 0 {
		return errors.New("OpenAI tool authority session, membership, and organization revisions must be positive")
	}
	return nil
}

func (expectation openAIToolAuthorityExpectation) validateOperation() error {
	if err := expectation.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(expectation.ToolName) == "" || strings.TrimSpace(expectation.ManifestDigest) == "" || strings.TrimSpace(expectation.SchemaDigest) == "" || strings.TrimSpace(expectation.ArgumentsDigest) == "" {
		return errors.New("OpenAI tool authority tool-set, tool, schema, and argument digests are required")
	}
	return nil
}

type openAIToolReplayEnvelope struct {
	Version             string                         `json:"version"`
	OperationID         string                         `json:"operation_id"`
	ToolName            string                         `json:"tool_name"`
	Arguments           map[string]any                 `json:"arguments"`
	Expectation         openAIToolAuthorityExpectation `json:"expectation"`
	ManualHistory       []openAIResponsesToolInputItem `json:"manual_history,omitempty"`
	ProviderResponseIDs []string                       `json:"provider_response_ids,omitempty"`
	TerminalResponseIDs []string                       `json:"terminal_response_ids,omitempty"`
	ProviderCallIDs     []string                       `json:"provider_call_ids,omitempty"`
	ExactOutputItems    []json.RawMessage              `json:"exact_output_items,omitempty"`
	ToolOutput          json.RawMessage                `json:"tool_output,omitempty"`
	FinalOutputItems    []json.RawMessage              `json:"final_output_items,omitempty"`
	FinalOutput         string                         `json:"final_output,omitempty"`
	UpdatedAt           time.Time                      `json:"updated_at"`
}

type openAIToolReplayEnvelopeWire struct {
	Version             string                         `json:"version"`
	OperationID         string                         `json:"operation_id"`
	ToolName            string                         `json:"tool_name"`
	Arguments           map[string]any                 `json:"arguments"`
	Expectation         openAIToolAuthorityExpectation `json:"expectation"`
	ManualHistory       []string                       `json:"manual_history_exact_base64,omitempty"`
	ProviderResponseIDs []string                       `json:"provider_response_ids,omitempty"`
	TerminalResponseIDs []string                       `json:"terminal_response_ids,omitempty"`
	ProviderCallIDs     []string                       `json:"provider_call_ids,omitempty"`
	ExactOutputItems    []string                       `json:"exact_output_items_base64,omitempty"`
	ToolOutput          string                         `json:"tool_output_base64,omitempty"`
	FinalOutputItems    []string                       `json:"final_output_items_base64,omitempty"`
	FinalOutput         string                         `json:"final_output,omitempty"`
	UpdatedAt           time.Time                      `json:"updated_at"`
}

func (envelope openAIToolReplayEnvelope) MarshalJSON() ([]byte, error) {
	history, err := encodeOpenAIToolExactHistory(envelope.ManualHistory)
	if err != nil {
		return nil, err
	}
	wire := openAIToolReplayEnvelopeWire{
		Version: envelope.Version, OperationID: envelope.OperationID, ToolName: envelope.ToolName,
		Arguments: envelope.Arguments, Expectation: envelope.Expectation, ManualHistory: history,
		ProviderResponseIDs: append([]string(nil), envelope.ProviderResponseIDs...), TerminalResponseIDs: append([]string(nil), envelope.TerminalResponseIDs...), ProviderCallIDs: append([]string(nil), envelope.ProviderCallIDs...),
		ExactOutputItems: encodeOpenAIToolExactBytes(envelope.ExactOutputItems), ToolOutput: encodeOpenAIToolExactByteSlice(envelope.ToolOutput),
		FinalOutputItems: encodeOpenAIToolExactBytes(envelope.FinalOutputItems), FinalOutput: envelope.FinalOutput, UpdatedAt: envelope.UpdatedAt,
	}
	return json.Marshal(wire)
}

func (envelope *openAIToolReplayEnvelope) UnmarshalJSON(raw []byte) error {
	var wire openAIToolReplayEnvelopeWire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	history, err := decodeOpenAIToolExactHistory(wire.ManualHistory)
	if err != nil {
		return err
	}
	exactItems, err := decodeOpenAIToolExactBytes(wire.ExactOutputItems)
	if err != nil {
		return err
	}
	toolOutput, err := decodeOpenAIToolExactByteSlice(wire.ToolOutput)
	if err != nil {
		return err
	}
	finalItems, err := decodeOpenAIToolExactBytes(wire.FinalOutputItems)
	if err != nil {
		return err
	}
	*envelope = openAIToolReplayEnvelope{
		Version: wire.Version, OperationID: wire.OperationID, ToolName: wire.ToolName,
		Arguments: wire.Arguments, Expectation: wire.Expectation, ManualHistory: history,
		ProviderResponseIDs: wire.ProviderResponseIDs, TerminalResponseIDs: wire.TerminalResponseIDs, ProviderCallIDs: wire.ProviderCallIDs,
		ExactOutputItems: exactItems, ToolOutput: toolOutput, FinalOutputItems: finalItems,
		FinalOutput: wire.FinalOutput, UpdatedAt: wire.UpdatedAt,
	}
	return nil
}

func encodeOpenAIToolExactHistory(history []openAIResponsesToolInputItem) ([]string, error) {
	encoded := make([]string, 0, len(history))
	for _, item := range history {
		raw := item.Raw
		if len(raw) == 0 {
			var err error
			raw, err = json.Marshal(item)
			if err != nil {
				return nil, err
			}
		}
		encoded = append(encoded, base64.RawStdEncoding.EncodeToString(raw))
	}
	return encoded, nil
}

func decodeOpenAIToolExactHistory(encoded []string) ([]openAIResponsesToolInputItem, error) {
	history := make([]openAIResponsesToolInputItem, 0, len(encoded))
	for _, value := range encoded {
		raw, err := base64.RawStdEncoding.DecodeString(value)
		if err != nil || !json.Valid(raw) {
			return nil, errOpenAIToolJournalTampered
		}
		var item openAIResponsesToolInputItem
		if err := item.UnmarshalJSON(raw); err != nil {
			return nil, errOpenAIToolJournalTampered
		}
		history = append(history, item)
	}
	return history, nil
}

func encodeOpenAIToolExactBytes(items []json.RawMessage) []string {
	encoded := make([]string, 0, len(items))
	for _, item := range items {
		encoded = append(encoded, base64.RawStdEncoding.EncodeToString(item))
	}
	return encoded
}

func decodeOpenAIToolExactBytes(encoded []string) ([]json.RawMessage, error) {
	items := make([]json.RawMessage, 0, len(encoded))
	for _, value := range encoded {
		raw, err := base64.RawStdEncoding.DecodeString(value)
		if err != nil || !json.Valid(raw) {
			return nil, errOpenAIToolJournalTampered
		}
		items = append(items, json.RawMessage(raw))
	}
	return items, nil
}

func encodeOpenAIToolExactByteSlice(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(raw)
}

func decodeOpenAIToolExactByteSlice(encoded string) (json.RawMessage, error) {
	if encoded == "" {
		return nil, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || !json.Valid(raw) {
		return nil, errOpenAIToolJournalTampered
	}
	return json.RawMessage(raw), nil
}

type openAIToolJournalProposal struct {
	ProviderResponseID string
	ProviderCallID     string
	ExactOutputItems   []json.RawMessage
	PreimageDigest     string
	ManifestDigest     string
	RunID              string
	RunSequence        uint64
	RunRequestDigest   string
}

type openAIToolEffectCommit struct {
	FunctionOutput       json.RawMessage
	PostimageDigest      string
	ReconciliationDigest string
}

type openAIToolJournalRecord struct {
	OperationID              string                         `json:"operation_id"`
	RunID                    string                         `json:"run_id"`
	RunSequence              uint64                         `json:"run_sequence"`
	ToolName                 string                         `json:"tool_name"`
	State                    openAIToolOperationState       `json:"state"`
	SemanticEffectID         string                         `json:"semantic_effect_id"`
	ManifestSHA256           string                         `json:"manifest_sha256"`
	ArgumentsSHA256          string                         `json:"arguments_sha256"`
	SchemaSHA256             string                         `json:"schema_sha256"`
	PolicyRevision           string                         `json:"policy_revision"`
	SourceDigest             string                         `json:"source_digest"`
	ExpectationSHA256        string                         `json:"expectation_sha256"`
	Expectation              openAIToolAuthorityExpectation `json:"expectation"`
	CorrelationDigests       []string                       `json:"provider_correlation_digests"`
	PreimageDigest           string                         `json:"preimage_digest"`
	PostimageDigest          string                         `json:"postimage_digest,omitempty"`
	ResultDigest             string                         `json:"bounded_result_digest,omitempty"`
	ReconciliationDigest     string                         `json:"reconciliation_digest,omitempty"`
	FinalizationRunDigest    string                         `json:"finalization_run_digest,omitempty"`
	FinalizationOperationIDs []string                       `json:"finalization_operation_ids,omitempty"`
	FinalUseDigest           string                         `json:"final_use_digest,omitempty"`
	FanOutReceiptDigest      string                         `json:"fan_out_receipt_digest,omitempty"`
	AttemptCount             uint64                         `json:"attempt_count"`
	EnvelopeFile             string                         `json:"envelope_file"`
	EnvelopeSHA256           string                         `json:"envelope_sha256"`
	AEADKeyID                string                         `json:"aead_key_id"`
	AEADKeyVersion           string                         `json:"aead_key_version"`
	EffectAliases            map[string]string              `json:"effect_aliases"`
	CreatedAt                time.Time                      `json:"created_at"`
	UpdatedAt                time.Time                      `json:"updated_at"`
	QuarantineReason         string                         `json:"quarantine_reason,omitempty"`
}

type openAIToolRunRecord struct {
	RunID             string                         `json:"run_id"`
	RequestDigest     string                         `json:"request_digest"`
	ExpectationDigest string                         `json:"expectation_digest"`
	Expectation       openAIToolAuthorityExpectation `json:"expectation"`
	State             openAIToolRunState             `json:"state"`
	ProviderTurnCount uint64                         `json:"provider_turn_count"`
	TerminalDigest    string                         `json:"terminal_digest,omitempty"`
	BlockedReason     string                         `json:"blocked_reason,omitempty"`
	SupersededByRunID string                         `json:"superseded_by_run_id,omitempty"`
	CreatedAt         time.Time                      `json:"created_at"`
	UpdatedAt         time.Time                      `json:"updated_at"`
}

type openAIToolJournalFile struct {
	Version                   string                                  `json:"version"`
	JournalID                 string                                  `json:"journal_id"`
	Generation                uint64                                  `json:"generation"`
	PreviousDigest            string                                  `json:"previous_digest,omitempty"`
	MACKeyID                  string                                  `json:"mac_key_id"`
	MACKeyVersion             string                                  `json:"mac_key_version"`
	EffectAuthorityKeyID      string                                  `json:"effect_authority_key_id"`
	EffectAuthorityKeyVersion string                                  `json:"effect_authority_key_version"`
	Runs                      map[string]openAIToolRunRecord          `json:"runs"`
	Records                   map[string]openAIToolJournalRecord      `json:"records"`
	Aliases                   map[string]string                       `json:"aliases"`
	Correlations              map[string]string                       `json:"correlations"`
	RotationReceipts          []openAIToolAliasRotationReceipt        `json:"rotation_receipts"`
	CollisionReceipts         []openAIToolCorrelationCollisionReceipt `json:"correlation_collision_receipts"`
	UpdatedAt                 time.Time                               `json:"updated_at"`
	MAC                       string                                  `json:"mac"`
}

type openAIToolCorrelationCollisionReceipt struct {
	ExistingOperationID     string    `json:"existing_operation_id"`
	AttemptedSemanticEffect string    `json:"attempted_semantic_effect_id"`
	CorrelationDigest       string    `json:"correlation_digest"`
	ToolName                string    `json:"tool_name"`
	ManifestDigest          string    `json:"manifest_digest"`
	CreatedAt               time.Time `json:"created_at"`
}

type openAIToolAliasRotationReceipt struct {
	JournalID          string    `json:"journal_id"`
	Generation         uint64    `json:"generation"`
	EffectKeyID        string    `json:"effect_key_id"`
	EffectKeyVersion   string    `json:"effect_key_version"`
	OperationCount     int       `json:"operation_count"`
	OperationSetDigest string    `json:"operation_set_digest"`
	CreatedAt          time.Time `json:"created_at"`
	SigningKeyID       string    `json:"signing_key_id"`
	SigningKeyVersion  string    `json:"signing_key_version"`
	Signature          string    `json:"signature"`
}

type openAIToolJournal struct {
	mu             sync.Mutex
	directory      string
	path           string
	journalID      string
	highWater      openAIToolJournalHighWater
	keyring        openAIToolJournalKeyring
	directoryFile  *os.File
	directoryInfo  os.FileInfo
	lockFile       *os.File
	state          openAIToolJournalFile
	anchor         openAIToolJournalAnchor
	liveRaw        []byte
	liveIdentity   openAIToolFileIdentity
	poisoned       error
	now            func() time.Time
	operationLocks sync.Map
}

type openAIToolFileIdentity struct {
	Device uint64
	Inode  uint64
	Owner  uint32
	Links  uint64
	Mode   os.FileMode
	Size   int64
}

func openAIToolFileIdentityFromInfo(info os.FileInfo) (openAIToolFileIdentity, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return openAIToolFileIdentity{}, false
	}
	return openAIToolFileIdentity{
		Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Owner: stat.Uid,
		Links: uint64(stat.Nlink), Mode: info.Mode(), Size: info.Size(),
	}, true
}

func openOpenAIToolJournal(ctx context.Context, directory, journalID string, highWater openAIToolJournalHighWater, keyring openAIToolJournalKeyring) (*openAIToolJournal, error) {
	if highWater == nil || keyring == nil {
		return nil, errors.New("OpenAI tool journal requires external high-water and managed keys")
	}
	journalID = strings.TrimSpace(journalID)
	if journalID == "" {
		return nil, errors.New("OpenAI tool journal id is required")
	}
	resolved, err := secureOpenAIToolJournalDirectory(directory)
	if err != nil {
		return nil, err
	}
	directoryFile, directoryInfo, err := openSecureOpenAIToolDirectory(resolved)
	if err != nil {
		return nil, err
	}
	lockFile, err := openSecureOpenAIToolFileAt(directoryFile, openAIToolJournalLockName, true)
	if err != nil {
		_ = directoryFile.Close()
		return nil, fmt.Errorf("open OpenAI tool journal lock: %w", err)
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lockFile.Close()
		_ = directoryFile.Close()
		return nil, fmt.Errorf("lock OpenAI tool journal: %w", err)
	}
	journal := &openAIToolJournal{
		directory: resolved, path: filepath.Join(resolved, openAIToolJournalFileName), journalID: journalID,
		highWater: highWater, keyring: keyring, directoryFile: directoryFile, directoryInfo: directoryInfo,
		lockFile: lockFile, now: func() time.Time { return time.Now().UTC() },
	}
	if err := journal.loadOrInitialize(ctx); err != nil {
		_ = journal.Close()
		return nil, err
	}
	return journal, nil
}

func openSecureOpenAIToolDirectory(path string) (*os.File, os.FileInfo, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open OpenAI tool journal directory: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, nil, errors.New("OpenAI tool journal directory descriptor is invalid")
	}
	opened, openedErr := file.Stat()
	current, currentErr := os.Lstat(path)
	stat, statOK := opened.Sys().(*syscall.Stat_t)
	if openedErr != nil || currentErr != nil || !opened.IsDir() || opened.Mode().Perm() != 0o700 || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) || !statOK || stat.Uid != uint32(os.Geteuid()) {
		_ = file.Close()
		return nil, nil, errors.New("OpenAI tool journal directory identity, owner, or mode is unsafe")
	}
	return file, opened, nil
}

func secureOpenAIToolJournalDirectory(directory string) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "", errors.New("OpenAI tool journal directory is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve OpenAI tool journal directory: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if direct, lstatErr := os.Lstat(absolute); lstatErr == nil && direct.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("OpenAI tool journal directory itself cannot be a symlink")
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("create OpenAI tool journal directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve OpenAI tool journal symlinks: %w", err)
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return "", errors.New("OpenAI tool journal directory must be a non-symlink 0700 directory")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return "", errors.New("OpenAI tool journal directory owner mismatch")
	}
	return resolved, nil
}

func openSecureOpenAIToolFileAt(directory *os.File, base string, create bool) (*os.File, error) {
	if directory == nil || filepath.Base(base) != base || base == "." || base == string(filepath.Separator) || strings.Contains(base, string(filepath.Separator)) {
		return nil, errors.New("OpenAI tool journal file name is unsafe")
	}
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if create {
		flags |= unix.O_CREAT
	}
	fd, err := unix.Openat(int(directory.Fd()), base, flags, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), base)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("OpenAI tool journal file descriptor is invalid")
	}
	opened, openedErr := file.Stat()
	var current unix.Stat_t
	currentErr := unix.Fstatat(int(directory.Fd()), base, &current, unix.AT_SYMLINK_NOFOLLOW)
	stat, statOK := opened.Sys().(*syscall.Stat_t)
	if openedErr != nil || currentErr != nil || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0o600 || !statOK || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || current.Dev != stat.Dev || current.Ino != stat.Ino || current.Uid != stat.Uid || current.Nlink != stat.Nlink || current.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = file.Close()
		return nil, errors.New("OpenAI tool journal file identity, owner, mode, or link count is unsafe")
	}
	return file, nil
}

func (journal *openAIToolJournal) Close() error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.lockFile == nil && journal.directoryFile == nil {
		return nil
	}
	var closeErr error
	if journal.lockFile != nil {
		_ = unix.Flock(int(journal.lockFile.Fd()), unix.LOCK_UN)
		closeErr = journal.lockFile.Close()
		journal.lockFile = nil
	}
	if journal.directoryFile != nil {
		if err := journal.directoryFile.Close(); closeErr == nil {
			closeErr = err
		}
		journal.directoryFile = nil
	}
	return closeErr
}

func (journal *openAIToolJournal) loadOrInitialize(ctx context.Context) error {
	if err := journal.verifyDirectoryIdentityLocked(); err != nil {
		return err
	}
	keys, err := journal.currentKeys(ctx)
	if err != nil {
		return err
	}
	anchor, err := journal.highWater.LoadOpenAIToolJournalAnchor(ctx, journal.journalID)
	if err != nil {
		return fmt.Errorf("load OpenAI tool journal high-water: %w", err)
	}
	raw, liveIdentity, readErr := readSecureOpenAIToolFileAtWithIdentity(journal.directoryFile, openAIToolJournalFileName)
	if errors.Is(readErr, os.ErrNotExist) {
		if anchor.Generation != 0 || anchor.Digest != "" {
			return errOpenAIToolJournalRollback
		}
		journal.state = openAIToolJournalFile{
			Version: openAIToolJournalFormatVersion, JournalID: journal.journalID,
			Runs: map[string]openAIToolRunRecord{}, Records: map[string]openAIToolJournalRecord{}, Aliases: map[string]string{}, Correlations: map[string]string{},
			RotationReceipts: []openAIToolAliasRotationReceipt{}, CollisionReceipts: []openAIToolCorrelationCollisionReceipt{},
			MACKeyID: keys.MACKeyID, MACKeyVersion: keys.MACVersion,
			EffectAuthorityKeyID: keys.EffectKeyID, EffectAuthorityKeyVersion: keys.EffectVersion,
		}
		journal.anchor = anchor
		return journal.persistLocked(ctx)
	}
	if readErr != nil {
		return fmt.Errorf("read OpenAI tool journal: %w", readErr)
	}
	var state openAIToolJournalFile
	if err := decodeOpenAIToolJSONStrict(raw, &state); err != nil {
		return fmt.Errorf("decode OpenAI tool journal: %w", errOpenAIToolJournalTampered)
	}
	if state.Version != openAIToolJournalFormatVersion || state.JournalID != journal.journalID || state.Generation == 0 || strings.TrimSpace(state.EffectAuthorityKeyID) == "" || strings.TrimSpace(state.EffectAuthorityKeyVersion) == "" || state.Runs == nil || state.Records == nil || state.Aliases == nil || state.Correlations == nil || state.RotationReceipts == nil || state.CollisionReceipts == nil {
		return errOpenAIToolJournalTampered
	}
	canonicalState, canonicalErr := canonicalJSON(state)
	if canonicalErr != nil || !hmac.Equal(raw, canonicalState) {
		return errOpenAIToolJournalTampered
	}
	macKey, err := journal.keyring.OpenAIToolJournalMACKey(ctx, state.MACKeyID, state.MACKeyVersion)
	if err != nil {
		return fmt.Errorf("resolve OpenAI tool journal MAC key: %w", err)
	}
	digest, err := authenticateOpenAIToolJournal(state, macKey)
	if err != nil {
		return err
	}
	stateAnchor := openAIToolJournalAnchor{Generation: state.Generation, Digest: digest}
	if anchor != stateAnchor {
		if state.Generation != anchor.Generation+1 || state.PreviousDigest != anchor.Digest {
			return errOpenAIToolJournalRollback
		}
		casErr := journal.highWater.CompareAndSwapOpenAIToolJournalAnchor(ctx, journal.journalID, anchor, stateAnchor)
		if casErr != nil {
			observed, loadErr := journal.highWater.LoadOpenAIToolJournalAnchor(ctx, journal.journalID)
			if loadErr != nil || observed != stateAnchor {
				return fmt.Errorf("%w: recover journal rename before high-water CAS: %v", errOpenAIToolJournalConflict, casErr)
			}
		}
		anchor = stateAnchor
	}
	for runID, run := range state.Runs {
		expectationDigest, _, digestErr := openAIToolCanonicalDigest(openAIToolRunBaseExpectation(run.Expectation))
		if runID != run.RunID || strings.TrimSpace(run.RequestDigest) == "" || strings.TrimSpace(run.ExpectationDigest) == "" || digestErr != nil || expectationDigest != run.ExpectationDigest || run.Expectation.validate() != nil || !validOpenAIToolRunState(run.State) || run.ProviderTurnCount > openAIToolRunnerMaxTurns || run.CreatedAt.IsZero() || run.UpdatedAt.IsZero() {
			return errOpenAIToolJournalTampered
		}
		if run.State == openAIToolRunBlocked && (run.ProviderTurnCount != openAIToolRunnerMaxTurns || run.BlockedReason != "max_provider_turns") {
			return errOpenAIToolJournalTampered
		}
		if run.State == openAIToolRunCompleted && strings.TrimSpace(run.TerminalDigest) == "" {
			return errOpenAIToolJournalTampered
		}
		if run.State == openAIToolRunSuperseded && strings.TrimSpace(run.SupersededByRunID) == "" {
			return errOpenAIToolJournalTampered
		}
	}
	for operationID, record := range state.Records {
		run, runExists := state.Runs[record.RunID]
		if operationID != record.OperationID || !runExists || !openAIToolSameRunExpectation(run.Expectation, record.Expectation) || !validOpenAIToolOperationState(record.State) || record.Expectation.validateOperation() != nil || record.ManifestSHA256 != record.Expectation.ManifestDigest || record.SchemaSHA256 != record.Expectation.SchemaDigest || record.ArgumentsSHA256 != record.Expectation.ArgumentsDigest || record.PolicyRevision != record.Expectation.PolicyRevision || record.SourceDigest != record.Expectation.SourceWindowDigest || record.SemanticEffectID == "" || record.AEADKeyID == "" || len(record.EffectAliases) == 0 || len(record.CorrelationDigests) == 0 {
			return errOpenAIToolJournalTampered
		}
		if record.State == openAIToolStateCompleted && (record.FinalizationRunDigest == "" || len(record.FinalizationOperationIDs) == 0 || record.FinalUseDigest == "" || record.FanOutReceiptDigest == "") {
			return errOpenAIToolJournalTampered
		}
		if (record.FinalizationRunDigest == "") != (len(record.FinalizationOperationIDs) == 0) ||
			(record.FinalizationRunDigest == "") != (record.FinalUseDigest == "") ||
			(record.FinalizationRunDigest == "") != (record.FanOutReceiptDigest == "") {
			return errOpenAIToolJournalTampered
		}
		if _, err := journal.readEnvelope(ctx, record); err != nil {
			return fmt.Errorf("verify OpenAI tool replay envelope %q: %w", operationID, err)
		}
	}
	runs := map[string][]openAIToolJournalRecord{}
	for _, record := range state.Records {
		runs[record.RunID] = append(runs[record.RunID], record)
	}
	for runID, records := range runs {
		sort.Slice(records, func(i, k int) bool { return records[i].RunSequence < records[k].RunSequence })
		memberIDs := make([]string, len(records))
		for index := range records {
			memberIDs[index] = records[index].OperationID
		}
		var finalized *openAIToolJournalRecord
		for index, record := range records {
			if record.RunID != runID || record.RunSequence != uint64(index) || !openAIToolSameRunExpectation(records[0].Expectation, record.Expectation) {
				return errOpenAIToolJournalTampered
			}
			if record.FinalizationRunDigest != "" {
				if !equalOpenAIToolStrings(record.FinalizationOperationIDs, memberIDs) {
					return errOpenAIToolJournalTampered
				}
				envelope, envelopeErr := journal.readEnvelope(ctx, record)
				wantRunDigest, digestErr := openAIToolFinalizationRunDigest(openAIToolRunBaseExpectation(record.Expectation), record.RunID, envelope.FinalOutput, memberIDs)
				if envelopeErr != nil || digestErr != nil || record.FinalizationRunDigest != wantRunDigest {
					return errOpenAIToolJournalTampered
				}
				if finalized != nil && (record.FinalizationRunDigest != finalized.FinalizationRunDigest || record.FinalUseDigest != finalized.FinalUseDigest || record.FanOutReceiptDigest != finalized.FanOutReceiptDigest) {
					return errOpenAIToolJournalTampered
				}
				copy := record
				finalized = &copy
			}
		}
	}
	for alias, operationID := range state.Aliases {
		record, ok := state.Records[operationID]
		if !ok || !openAIToolRecordContainsAlias(record, alias) {
			return errOpenAIToolJournalTampered
		}
	}
	for digest, operationID := range state.Correlations {
		record, ok := state.Records[operationID]
		if !ok || !openAIToolContainsString(record.CorrelationDigests, digest) {
			return errOpenAIToolJournalTampered
		}
	}
	for _, receipt := range state.RotationReceipts {
		material, materialErr := openAIToolRotationReceiptMaterial(receipt)
		if materialErr != nil || journal.keyring.VerifyOpenAIToolRotationReceipt(ctx, receipt.SigningKeyID, receipt.SigningKeyVersion, material, receipt.Signature) != nil {
			return errOpenAIToolJournalTampered
		}
	}
	for _, receipt := range state.CollisionReceipts {
		record, ok := state.Records[receipt.ExistingOperationID]
		if !ok || record.State != openAIToolStateQuarantined || receipt.AttemptedSemanticEffect == "" || receipt.CorrelationDigest == "" || receipt.ToolName == "" || receipt.ManifestDigest != openAIToolManifestV1SHA256 || receipt.CreatedAt.IsZero() {
			return errOpenAIToolJournalTampered
		}
	}
	journal.state, journal.anchor = state, anchor
	journal.liveRaw = append([]byte(nil), raw...)
	journal.liveIdentity = liveIdentity
	return journal.requireUsableLocked(ctx)
}

func openAIToolRecordContainsAlias(record openAIToolJournalRecord, alias string) bool {
	for _, candidate := range record.EffectAliases {
		if candidate == alias {
			return true
		}
	}
	return false
}

func openAIToolContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func readSecureOpenAIToolFileAt(directory *os.File, base string) ([]byte, error) {
	raw, _, err := readSecureOpenAIToolFileAtWithIdentity(directory, base)
	return raw, err
}

func readSecureOpenAIToolFileAtWithIdentity(directory *os.File, base string) ([]byte, openAIToolFileIdentity, error) {
	file, err := openSecureOpenAIToolFileAt(directory, base, false)
	if err != nil {
		return nil, openAIToolFileIdentity{}, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, openAIToolFileIdentity{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		return nil, openAIToolFileIdentity{}, err
	}
	after, afterErr := file.Stat()
	var current unix.Stat_t
	currentErr := unix.Fstatat(int(directory.Fd()), base, &current, unix.AT_SYMLINK_NOFOLLOW)
	beforeStat, beforeOK := before.Sys().(*syscall.Stat_t)
	afterStat, afterOK := after.Sys().(*syscall.Stat_t)
	if afterErr != nil || currentErr != nil || !os.SameFile(before, after) || before.Size() != after.Size() || int64(len(raw)) != before.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() || !beforeOK || !afterOK || beforeStat.Uid != uint32(os.Geteuid()) || afterStat.Uid != beforeStat.Uid || afterStat.Nlink != 1 || beforeStat.Dev != afterStat.Dev || beforeStat.Ino != afterStat.Ino || current.Dev != afterStat.Dev || current.Ino != afterStat.Ino || current.Uid != afterStat.Uid || current.Nlink != afterStat.Nlink || current.Size != after.Size() || current.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, openAIToolFileIdentity{}, errors.New("OpenAI tool journal file changed during read")
	}
	identity, ok := openAIToolFileIdentityFromInfo(after)
	if !ok {
		return nil, openAIToolFileIdentity{}, errors.New("OpenAI tool journal file identity is unavailable")
	}
	return raw, identity, nil
}

func (journal *openAIToolJournal) currentKeys(ctx context.Context) (openAIToolJournalKeys, error) {
	keys, err := journal.keyring.CurrentOpenAIToolJournalKeys(ctx)
	if err != nil {
		return openAIToolJournalKeys{}, fmt.Errorf("load OpenAI tool journal keys: %w", err)
	}
	if strings.TrimSpace(keys.MACKeyID) == "" || strings.TrimSpace(keys.AEADKeyID) == "" || strings.TrimSpace(keys.EffectKeyID) == "" || strings.TrimSpace(keys.MACVersion) == "" || strings.TrimSpace(keys.AEADVersion) == "" || strings.TrimSpace(keys.EffectVersion) == "" || len(keys.MACKey) < 32 || len(keys.AEADKey) != 32 || len(keys.EffectKey) < 32 {
		return openAIToolJournalKeys{}, errors.New("OpenAI tool journal keys or versions are invalid")
	}
	if keys.MACKeyID == keys.AEADKeyID || keys.MACKeyID == keys.EffectKeyID || keys.AEADKeyID == keys.EffectKeyID || hmac.Equal(keys.MACKey, keys.AEADKey) || hmac.Equal(keys.MACKey, keys.EffectKey) || hmac.Equal(keys.AEADKey, keys.EffectKey) {
		return openAIToolJournalKeys{}, errors.New("OpenAI tool journal MAC, AEAD, and effect keys must be pairwise distinct")
	}
	return keys, nil
}

func (journal *openAIToolJournal) signOpenAIToolProductEffectReceipt(ctx context.Context, material []byte) (string, string, string, error) {
	if journal == nil || len(material) == 0 {
		return "", "", "", errors.New("OpenAI tool product effect receipt material is required")
	}
	keys, err := journal.currentKeys(ctx)
	if err != nil {
		return "", "", "", err
	}
	mac := hmac.New(sha256.New, keys.EffectKey)
	_, _ = mac.Write([]byte("stride-openai-tool-product-effect-receipt-v1"))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(material)
	return keys.EffectKeyID, keys.EffectVersion, hex.EncodeToString(mac.Sum(nil)), nil
}

func (journal *openAIToolJournal) verifyOpenAIToolProductEffectReceipt(ctx context.Context, keyID, keyVersion string, material []byte, authentication string) error {
	if journal == nil || strings.TrimSpace(keyID) == "" || strings.TrimSpace(keyVersion) == "" || len(material) == 0 || !isHexDigest(authentication) {
		return errors.New("OpenAI tool product effect receipt authentication is invalid")
	}
	key, err := journal.keyring.OpenAIToolJournalEffectKey(ctx, keyID, keyVersion)
	if err != nil || len(key) < 32 {
		return errors.New("OpenAI tool product effect receipt key is unavailable")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("stride-openai-tool-product-effect-receipt-v1"))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(material)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(authentication)) {
		return errors.New("OpenAI tool product effect receipt authentication failed")
	}
	return nil
}

func authenticateOpenAIToolJournal(state openAIToolJournalFile, key []byte) (string, error) {
	want := state.MAC
	state.MAC = ""
	raw, err := canonicalJSON(state)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	digest := hex.EncodeToString(mac.Sum(nil))
	if want != "" && !hmac.Equal([]byte(want), []byte(digest)) {
		return "", errOpenAIToolJournalTampered
	}
	return digest, nil
}

func (journal *openAIToolJournal) persistLocked(ctx context.Context) error {
	if journal.poisoned != nil {
		return journal.poisoned
	}
	if err := journal.verifyDirectoryIdentityLocked(); err != nil {
		journal.poisoned = err
		return err
	}
	keys, err := journal.currentKeys(ctx)
	if err != nil {
		return err
	}
	previousState, previousAnchor := journal.state, journal.anchor
	journal.state.Generation = previousAnchor.Generation + 1
	journal.state.PreviousDigest = previousAnchor.Digest
	journal.state.MACKeyID = keys.MACKeyID
	journal.state.MACKeyVersion = keys.MACVersion
	journal.state.UpdatedAt = journal.now()
	journal.state.MAC = ""
	digest, err := authenticateOpenAIToolJournal(journal.state, keys.MACKey)
	if err != nil {
		journal.state = previousState
		return err
	}
	journal.state.MAC = digest
	raw, err := canonicalJSON(journal.state)
	if err != nil {
		journal.state = previousState
		return err
	}
	previousRaw, previousExists := []byte(nil), false
	previousIdentity := openAIToolFileIdentity{}
	if prior, identity, readErr := readSecureOpenAIToolFileAtWithIdentity(journal.directoryFile, openAIToolJournalFileName); readErr == nil {
		previousRaw, previousIdentity, previousExists = prior, identity, true
	} else if !errors.Is(readErr, os.ErrNotExist) {
		journal.state = previousState
		return readErr
	}
	if previousExists {
		if len(journal.liveRaw) == 0 || previousIdentity != journal.liveIdentity || !hmac.Equal(previousRaw, journal.liveRaw) {
			journal.state = previousState
			journal.poisoned = fmt.Errorf("%w: journal target changed before the next generation write", errOpenAIToolJournalTampered)
			return journal.poisoned
		}
	} else if previousAnchor.Generation != 0 || previousAnchor.Digest != "" {
		journal.state = previousState
		journal.poisoned = fmt.Errorf("%w: authenticated journal target disappeared before the next generation write", errOpenAIToolJournalTampered)
		return journal.poisoned
	}
	if err := atomicWriteOpenAIToolFileAt(journal.directoryFile, openAIToolJournalFileName, raw); err != nil {
		if openAIToolCommitMayBeDurable(err) {
			journal.poisoned = err
		} else {
			journal.state = previousState
		}
		return err
	}
	writtenRaw, writtenIdentity, writtenErr := readSecureOpenAIToolFileAtWithIdentity(journal.directoryFile, openAIToolJournalFileName)
	if writtenErr != nil || !hmac.Equal(raw, writtenRaw) {
		journal.poisoned = fmt.Errorf("%w: pre-CAS journal reread failed", errOpenAIToolJournalCommittedUnverified)
		return journal.poisoned
	}
	if err := journal.verifyDirectoryIdentityLocked(); err != nil {
		journal.poisoned = fmt.Errorf("%w: %v", errOpenAIToolJournalCommittedUnverified, err)
		return journal.poisoned
	}
	nextAnchor := openAIToolJournalAnchor{Generation: journal.state.Generation, Digest: digest}
	casErr := journal.highWater.CompareAndSwapOpenAIToolJournalAnchor(ctx, journal.journalID, previousAnchor, nextAnchor)
	if casErr != nil {
		observed, loadErr := journal.highWater.LoadOpenAIToolJournalAnchor(ctx, journal.journalID)
		if loadErr == nil && observed == nextAnchor {
			journal.anchor = nextAnchor
		} else {
			var restoreErr error
			if previousExists {
				restoreErr = atomicWriteOpenAIToolFileAt(journal.directoryFile, openAIToolJournalFileName, previousRaw)
			} else if removeErr := unlinkOpenAIToolFileAt(journal.directoryFile, openAIToolJournalFileName); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				restoreErr = removeErr
			} else {
				restoreErr = journal.directoryFile.Sync()
			}
			if restoreErr != nil {
				journal.poisoned = fmt.Errorf("%w: failed to restore after high-water conflict: %v", errOpenAIToolJournalCommittedUnverified, restoreErr)
				return journal.poisoned
			}
			journal.state, journal.anchor = previousState, previousAnchor
			journal.liveRaw = append(journal.liveRaw[:0], previousRaw...)
			if previousExists {
				restoredRaw, restoredIdentity, readErr := readSecureOpenAIToolFileAtWithIdentity(journal.directoryFile, openAIToolJournalFileName)
				if readErr != nil || !hmac.Equal(restoredRaw, previousRaw) {
					journal.poisoned = fmt.Errorf("%w: failed to verify restored journal target", errOpenAIToolJournalCommittedUnverified)
					return journal.poisoned
				}
				journal.liveIdentity = restoredIdentity
			} else {
				journal.liveIdentity = openAIToolFileIdentity{}
			}
			return fmt.Errorf("%w: %v", errOpenAIToolJournalConflict, casErr)
		}
	} else {
		journal.anchor = nextAnchor
	}
	verified, verifiedIdentity, err := readSecureOpenAIToolFileAtWithIdentity(journal.directoryFile, openAIToolJournalFileName)
	if err != nil || verifiedIdentity != writtenIdentity || !hmac.Equal(raw, verified) {
		journal.poisoned = fmt.Errorf("%w: post-write verification failed", errOpenAIToolJournalCommittedUnverified)
		return journal.poisoned
	}
	journal.liveRaw = append(journal.liveRaw[:0], raw...)
	journal.liveIdentity = verifiedIdentity
	return nil
}

func openAIToolCommitMayBeDurable(err error) bool {
	return errors.Is(err, errOpenAIToolJournalCommittedUnverified)
}

func (journal *openAIToolJournal) requireUsableLocked(ctx context.Context) error {
	if journal.poisoned != nil {
		return journal.poisoned
	}
	if err := journal.verifyDirectoryIdentityLocked(); err != nil {
		journal.poisoned = err
		return err
	}
	if err := journal.verifyLiveTargetsLocked(ctx); err != nil {
		journal.poisoned = err
		return err
	}
	observed, err := journal.highWater.LoadOpenAIToolJournalAnchor(ctx, journal.journalID)
	if err != nil {
		return fmt.Errorf("load current OpenAI tool journal high-water: %w", err)
	}
	if observed != journal.anchor {
		journal.poisoned = fmt.Errorf("%w: external high-water diverged from the open journal generation", errOpenAIToolJournalRollback)
		return journal.poisoned
	}
	keys, err := journal.currentKeys(ctx)
	if err != nil {
		return err
	}
	if err := journal.ensureCurrentEffectAuthorityLocked(ctx, keys); err != nil {
		journal.poisoned = err
		return err
	}
	return nil
}

func (journal *openAIToolJournal) ensureCurrentEffectAuthorityLocked(ctx context.Context, keys openAIToolJournalKeys) error {
	currentVersion := keys.EffectKeyID + "@" + keys.EffectVersion
	operationIDs := make([]string, 0, len(journal.state.Records))
	for operationID, record := range journal.state.Records {
		operationIDs = append(operationIDs, operationID)
		envelope, err := journal.readEnvelope(ctx, record)
		if err != nil {
			return err
		}
		wantAlias, err := openAIToolEffectAlias(keys.EffectKeyID, keys.EffectVersion, keys.EffectKey, envelope.Expectation)
		if err != nil || record.EffectAliases[currentVersion] != wantAlias || journal.state.Aliases[wantAlias] != operationID {
			return errors.New("OpenAI tool current effect-key authority lacks an exact alias for every live operation")
		}
	}
	sort.Strings(operationIDs)
	if journal.state.EffectAuthorityKeyID == keys.EffectKeyID && journal.state.EffectAuthorityKeyVersion == keys.EffectVersion {
		return nil
	}
	operationSetDigest, _, err := openAIToolCanonicalDigest(operationIDs)
	if err != nil {
		return err
	}
	var migration *openAIToolAliasRotationReceipt
	for index := len(journal.state.RotationReceipts) - 1; index >= 0; index-- {
		receipt := journal.state.RotationReceipts[index]
		if receipt.EffectKeyID == keys.EffectKeyID && receipt.EffectKeyVersion == keys.EffectVersion && receipt.OperationCount == len(operationIDs) && receipt.OperationSetDigest == operationSetDigest {
			copy := receipt
			migration = &copy
			break
		}
	}
	if migration == nil {
		return errors.New("OpenAI tool current effect-key authority advanced without a signed complete alias migration")
	}
	material, err := openAIToolRotationReceiptMaterial(*migration)
	if err != nil || journal.keyring.VerifyOpenAIToolRotationReceipt(ctx, migration.SigningKeyID, migration.SigningKeyVersion, material, migration.Signature) != nil {
		return errors.New("OpenAI tool current effect-key migration receipt is invalid")
	}
	journal.state.EffectAuthorityKeyID = keys.EffectKeyID
	journal.state.EffectAuthorityKeyVersion = keys.EffectVersion
	if err := journal.persistLocked(ctx); err != nil {
		return fmt.Errorf("adopt OpenAI tool current effect-key authority: %w", err)
	}
	return nil
}

func (journal *openAIToolJournal) verifyLiveTargetsLocked(ctx context.Context) error {
	if journal.lockFile == nil || journal.directoryFile == nil {
		return errors.New("OpenAI tool journal descriptors are closed")
	}
	heldLock, heldErr := journal.lockFile.Stat()
	if heldErr != nil {
		return fmt.Errorf("%w: held journal lock descriptor is unavailable", errOpenAIToolJournalTampered)
	}
	heldStat, heldOK := heldLock.Sys().(*syscall.Stat_t)
	var namedLock unix.Stat_t
	namedErr := unix.Fstatat(int(journal.directoryFile.Fd()), openAIToolJournalLockName, &namedLock, unix.AT_SYMLINK_NOFOLLOW)
	if namedErr != nil || !heldOK || !heldLock.Mode().IsRegular() || heldLock.Mode().Perm() != 0o600 || heldStat.Uid != uint32(os.Geteuid()) || heldStat.Nlink != 1 || namedLock.Dev != heldStat.Dev || namedLock.Ino != heldStat.Ino || namedLock.Uid != heldStat.Uid || namedLock.Nlink != heldStat.Nlink || namedLock.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("%w: held journal lock no longer matches its private directory target", errOpenAIToolJournalTampered)
	}
	raw, identity, err := readSecureOpenAIToolFileAtWithIdentity(journal.directoryFile, openAIToolJournalFileName)
	if err != nil {
		return fmt.Errorf("%w: current journal target is unavailable: %v", errOpenAIToolJournalTampered, err)
	}
	expectedRaw, err := canonicalJSON(journal.state)
	if err != nil || identity != journal.liveIdentity || !hmac.Equal(raw, expectedRaw) {
		return fmt.Errorf("%w: current journal target differs from the open authenticated generation", errOpenAIToolJournalTampered)
	}
	macKey, err := journal.keyring.OpenAIToolJournalMACKey(ctx, journal.state.MACKeyID, journal.state.MACKeyVersion)
	if err != nil {
		return fmt.Errorf("%w: current journal MAC key is unavailable", errOpenAIToolJournalTampered)
	}
	digest, err := authenticateOpenAIToolJournal(journal.state, macKey)
	if err != nil || journal.state.Generation != journal.anchor.Generation || digest != journal.anchor.Digest || journal.state.MAC != digest {
		return fmt.Errorf("%w: current journal generation does not match its external anchor", errOpenAIToolJournalTampered)
	}
	return nil
}

func (journal *openAIToolJournal) verifyDirectoryIdentityLocked() error {
	if journal.directoryFile == nil || journal.directoryInfo == nil {
		return errors.New("OpenAI tool journal directory descriptor is closed")
	}
	opened, openedErr := journal.directoryFile.Stat()
	current, currentErr := os.Lstat(journal.directory)
	openedStat, openedOK := opened.Sys().(*syscall.Stat_t)
	initialStat, initialOK := journal.directoryInfo.Sys().(*syscall.Stat_t)
	if openedErr != nil || currentErr != nil || !openedOK || !initialOK || !opened.IsDir() || opened.Mode().Perm() != 0o700 || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) || openedStat.Uid != uint32(os.Geteuid()) || openedStat.Dev != initialStat.Dev || openedStat.Ino != initialStat.Ino {
		return errors.New("OpenAI tool journal directory identity changed")
	}
	return nil
}

func atomicWriteOpenAIToolFileAt(directory *os.File, target string, raw []byte) error {
	if directory == nil || filepath.Base(target) != target || target == "." || strings.Contains(target, string(filepath.Separator)) {
		return errors.New("OpenAI tool journal target name is unsafe")
	}
	temporaryName := ".openai-tool-" + uuid.NewString() + ".tmp"
	fd, err := unix.Openat(int(directory.Fd()), temporaryName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	temporary := os.NewFile(uintptr(fd), temporaryName)
	if temporary == nil {
		_ = unix.Close(fd)
		_ = unix.Unlinkat(int(directory.Fd()), temporaryName, 0)
		return errors.New("OpenAI tool journal temporary descriptor is invalid")
	}
	defer unix.Unlinkat(int(directory.Fd()), temporaryName, 0)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	var current unix.Stat_t
	if err := unix.Fstatat(int(directory.Fd()), target, &current, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if current.Mode&unix.S_IFMT != unix.S_IFREG || current.Mode&0o777 != 0o600 || current.Uid != uint32(os.Geteuid()) || current.Nlink != 1 {
			return errors.New("OpenAI tool journal target identity is unsafe")
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return err
	}
	if err := unix.Renameat(int(directory.Fd()), temporaryName, int(directory.Fd()), target); err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("%w: directory fsync after descriptor-relative rename: %v", errOpenAIToolJournalCommittedUnverified, err)
	}
	return nil
}

func unlinkOpenAIToolFileAt(directory *os.File, base string) error {
	if directory == nil || filepath.Base(base) != base || base == "." || strings.Contains(base, string(filepath.Separator)) {
		return errors.New("OpenAI tool journal unlink name is unsafe")
	}
	if err := unix.Unlinkat(int(directory.Fd()), base, 0); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return os.ErrNotExist
		}
		return err
	}
	return nil
}

func validOpenAIToolOperationState(state openAIToolOperationState) bool {
	switch state {
	case openAIToolStateReserved, openAIToolStateEffectCommitted, openAIToolStateContinuationSent, openAIToolStateCompleted, openAIToolStateQuarantined:
		return true
	default:
		return false
	}
}

func validOpenAIToolRunState(state openAIToolRunState) bool {
	switch state {
	case openAIToolRunActive, openAIToolRunCompleted, openAIToolRunBlocked, openAIToolRunSuperseded:
		return true
	default:
		return false
	}
}

func openAIToolCanonicalDigest(value any) (string, []byte, error) {
	raw, err := canonicalJSON(value)
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), raw, nil
}

func openAIToolEffectAlias(keyID, keyVersion string, key []byte, expectation openAIToolAuthorityExpectation) (string, error) {
	if err := expectation.validateOperation(); err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	for _, component := range []string{
		"stride-openai-tool-effect-v1", expectation.TenantID, expectation.PersonID,
		expectation.ThreadID, expectation.ArtifactRevision, expectation.SourceWindowDigest,
		expectation.ToolName, expectation.SchemaDigest, expectation.ArgumentsDigest, expectation.PolicyRevision,
	} {
		_, _ = mac.Write([]byte(component))
		_, _ = mac.Write([]byte{0})
	}
	return keyID + "@" + keyVersion + ":" + hex.EncodeToString(mac.Sum(nil)), nil
}

func (journal *openAIToolJournal) resolveSemanticOperationLocked(ctx context.Context, keys openAIToolJournalKeys, expectation openAIToolAuthorityExpectation) (string, string, bool, error) {
	currentVersion := keys.EffectKeyID + "@" + keys.EffectVersion
	versions := map[string]bool{currentVersion: true}
	for _, record := range journal.state.Records {
		for version := range record.EffectAliases {
			versions[version] = true
		}
	}
	versionList := make([]string, 0, len(versions))
	for version := range versions {
		versionList = append(versionList, version)
	}
	sort.Strings(versionList)
	currentAlias := ""
	matchingIDs := map[string]bool{}
	for _, version := range versionList {
		separator := strings.LastIndex(version, "@")
		if separator <= 0 || separator == len(version)-1 {
			return "", "", false, errOpenAIToolJournalTampered
		}
		keyID, keyVersion := version[:separator], version[separator+1:]
		var key []byte
		if version == currentVersion {
			key = keys.EffectKey
		} else {
			resolved, err := journal.keyring.OpenAIToolJournalEffectKey(ctx, keyID, keyVersion)
			if err != nil {
				// A formally retired historical key may be unavailable. Its alias
				// remains MAC-authenticated, while the required current alias still
				// resolves every live semantic tuple.
				continue
			}
			key = resolved
		}
		alias, err := openAIToolEffectAlias(keyID, keyVersion, key, expectation)
		if err != nil {
			return "", "", false, err
		}
		if version == currentVersion {
			currentAlias = alias
		}
		if operationID, exists := journal.state.Aliases[alias]; exists {
			record, ok := journal.state.Records[operationID]
			if !ok || record.EffectAliases[version] != alias {
				return "", "", false, errOpenAIToolJournalTampered
			}
			matchingIDs[operationID] = true
		}
	}
	if currentAlias == "" {
		return "", "", false, errors.New("OpenAI tool current semantic-effect alias is unavailable")
	}
	if len(matchingIDs) > 1 {
		return "", "", false, errors.New("OpenAI tool retained aliases resolve one semantic tuple to multiple operation IDs")
	}
	for operationID := range matchingIDs {
		return currentAlias, operationID, true, nil
	}
	return currentAlias, "", false, nil
}

func (journal *openAIToolJournal) Reserve(ctx context.Context, entry openAIToolManifestEntry, arguments map[string]any, expectation openAIToolAuthorityExpectation, proposal openAIToolJournalProposal, history []openAIResponsesToolInputItem) (openAIToolJournalRecord, openAIToolReplayEnvelope, bool, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
	}
	if !entry.Admitted || entry.Name != expectation.ToolName || entry.SchemaSHA256 != expectation.SchemaDigest || entry.PolicyRevision != expectation.PolicyRevision || proposal.ManifestDigest != openAIToolManifestV1SHA256 || expectation.ManifestDigest != proposal.ManifestDigest {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool manifest and authority expectation drift")
	}
	if err := expectation.validateOperation(); err != nil {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
	}
	if strings.TrimSpace(proposal.ProviderResponseID) == "" || strings.TrimSpace(proposal.ProviderCallID) == "" || len(proposal.ExactOutputItems) == 0 || strings.TrimSpace(proposal.PreimageDigest) == "" || strings.TrimSpace(proposal.RunID) == "" {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool run, provider correlations, exact output items, and preimage are required")
	}
	argumentsDigest, _, err := openAIToolCanonicalDigest(arguments)
	if err != nil {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
	}
	expectationDigest, _, err := openAIToolCanonicalDigest(expectation)
	if err != nil {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
	}
	if argumentsDigest != expectation.ArgumentsDigest {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool canonical arguments digest drift")
	}
	keys, err := journal.currentKeys(ctx)
	if err != nil {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
	}
	alias, semanticOperationID, semanticExists, err := journal.resolveSemanticOperationLocked(ctx, keys, expectation)
	if err != nil {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
	}
	run, runExists := journal.state.Runs[proposal.RunID]
	if runExists {
		if run.State != openAIToolRunActive || !openAIToolSameRunExpectation(run.Expectation, expectation) || strings.TrimSpace(proposal.RunRequestDigest) != "" && run.RequestDigest != strings.TrimSpace(proposal.RunRequestDigest) {
			return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool operation proposal does not match its active durable run")
		}
	} else if !semanticExists && strings.TrimSpace(proposal.RunRequestDigest) == "" {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool new operation requires a durable run request digest")
	}
	correlations := []string{openAIToolCorrelationDigest("response", proposal.ProviderResponseID), openAIToolCorrelationDigest("call", proposal.ProviderCallID)}
	for _, correlation := range correlations {
		if existingID, exists := journal.state.Correlations[correlation]; exists && (!semanticExists || semanticOperationID != existingID) {
			existing := journal.state.Records[existingID]
			previousExisting := existing
			previousCollisionReceipts := append([]openAIToolCorrelationCollisionReceipt(nil), journal.state.CollisionReceipts...)
			existing.State = openAIToolStateQuarantined
			existing.QuarantineReason = "provider correlation reused for a different semantic effect"
			existing.UpdatedAt = journal.now()
			journal.state.Records[existingID] = existing
			journal.state.CollisionReceipts = append(journal.state.CollisionReceipts, openAIToolCorrelationCollisionReceipt{
				ExistingOperationID: existingID, AttemptedSemanticEffect: alias, CorrelationDigest: correlation,
				ToolName: entry.Name, ManifestDigest: proposal.ManifestDigest, CreatedAt: journal.now(),
			})
			if persistErr := journal.persistLocked(ctx); persistErr != nil {
				if !openAIToolCommitMayBeDurable(persistErr) {
					journal.state.Records[existingID] = previousExisting
					journal.state.CollisionReceipts = previousCollisionReceipts
				}
				return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, persistErr
			}
			return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool provider correlation collision was quarantined")
		}
	}
	if semanticExists {
		operationID := semanticOperationID
		record := journal.state.Records[operationID]
		owningRun, owningRunExists := journal.state.Runs[record.RunID]
		if !owningRunExists {
			return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errOpenAIToolJournalTampered
		}
		if strings.TrimSpace(proposal.RunRequestDigest) == "" || owningRun.RequestDigest != strings.TrimSpace(proposal.RunRequestDigest) {
			return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool semantic replay crossed a different durable request identity")
		}
		envelope, err := journal.readEnvelope(ctx, record)
		if err != nil {
			return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
		}
		if record.ExpectationSHA256 != expectationDigest || record.PreimageDigest != proposal.PreimageDigest {
			return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool semantic replay expectation or preimage changed")
		}
		changed := false
		previousCorrelations := cloneOpenAIToolStringMap(journal.state.Correlations)
		for _, correlation := range correlations {
			if !openAIToolContainsString(record.CorrelationDigests, correlation) {
				record.CorrelationDigests = append(record.CorrelationDigests, correlation)
				journal.state.Correlations[correlation] = operationID
				changed = true
			}
		}
		if !openAIToolContainsString(envelope.ProviderResponseIDs, proposal.ProviderResponseID) {
			envelope.ProviderResponseIDs = append(envelope.ProviderResponseIDs, proposal.ProviderResponseID)
			changed = true
		}
		if !openAIToolContainsString(envelope.ProviderCallIDs, proposal.ProviderCallID) {
			envelope.ProviderCallIDs = append(envelope.ProviderCallIDs, proposal.ProviderCallID)
			changed = true
		}
		if changed {
			previousRecord := journal.state.Records[operationID]
			oldEnvelopeFile := record.EnvelopeFile
			record.UpdatedAt, envelope.UpdatedAt = journal.now(), journal.now()
			record.AEADKeyID, record.AEADKeyVersion = keys.AEADKeyID, keys.AEADVersion
			if err := journal.writeEnvelope(ctx, &record, envelope, keys.AEADKey); err != nil {
				journal.state.Correlations = previousCorrelations
				return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
			}
			journal.state.Records[operationID] = record
			if err := journal.persistLocked(ctx); err != nil {
				if !openAIToolCommitMayBeDurable(err) {
					journal.state.Records[operationID] = previousRecord
					journal.state.Correlations = previousCorrelations
					_ = unlinkOpenAIToolFileAt(journal.directoryFile, record.EnvelopeFile)
				}
				return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
			}
			_ = oldEnvelopeFile // retained as authenticated crash/rollback history until governed purge
		}
		return record, envelope, true, nil
	}
	now := journal.now()
	createdRun := false
	if !runExists {
		baseExpectation := openAIToolRunBaseExpectation(expectation)
		expectationDigest, _, err := openAIToolCanonicalDigest(baseExpectation)
		if err != nil {
			return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
		}
		run = openAIToolRunRecord{
			RunID: proposal.RunID, RequestDigest: strings.TrimSpace(proposal.RunRequestDigest), ExpectationDigest: expectationDigest,
			Expectation: baseExpectation, State: openAIToolRunActive, CreatedAt: now, UpdatedAt: now,
		}
		journal.state.Runs = cloneOpenAIToolRuns(journal.state.Runs)
		journal.state.Runs[run.RunID] = run
		createdRun = true
	}
	runMemberCount := uint64(0)
	for _, existing := range journal.state.Records {
		if existing.RunID == proposal.RunID && existing.RunSequence == proposal.RunSequence {
			return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool run sequence is already bound to another operation")
		}
		if existing.RunID == proposal.RunID {
			if existing.FinalizationRunDigest != "" {
				return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool finalized run cannot admit another operation")
			}
			runMemberCount++
		}
	}
	if proposal.RunSequence != runMemberCount {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, errors.New("OpenAI tool run sequence is not contiguous")
	}
	operationID := uuid.NewString()
	envelope := openAIToolReplayEnvelope{
		Version: openAIToolEnvelopeVersion, OperationID: operationID, ToolName: entry.Name,
		Arguments: arguments, Expectation: expectation, ManualHistory: cloneOpenAIToolHistory(history),
		ProviderResponseIDs: []string{proposal.ProviderResponseID}, ProviderCallIDs: []string{proposal.ProviderCallID},
		ExactOutputItems: cloneOpenAIToolRawItems(proposal.ExactOutputItems), UpdatedAt: now,
	}
	record := openAIToolJournalRecord{
		OperationID: operationID, RunID: proposal.RunID, RunSequence: proposal.RunSequence,
		ToolName: entry.Name, State: openAIToolStateReserved, SemanticEffectID: alias, ManifestSHA256: proposal.ManifestDigest,
		ArgumentsSHA256: argumentsDigest, SchemaSHA256: entry.SchemaSHA256, PolicyRevision: entry.PolicyRevision,
		SourceDigest: expectation.SourceWindowDigest, ExpectationSHA256: expectationDigest, Expectation: expectation,
		CorrelationDigests: correlations, PreimageDigest: proposal.PreimageDigest,
		AEADKeyID: keys.AEADKeyID, AEADKeyVersion: keys.AEADVersion,
		EffectAliases: map[string]string{keys.EffectKeyID + "@" + keys.EffectVersion: alias}, CreatedAt: now, UpdatedAt: now,
	}
	if err := journal.writeEnvelope(ctx, &record, envelope, keys.AEADKey); err != nil {
		if createdRun {
			delete(journal.state.Runs, run.RunID)
		}
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
	}
	journal.state.Records[operationID] = record
	journal.state.Aliases[alias] = operationID
	for _, correlation := range correlations {
		journal.state.Correlations[correlation] = operationID
	}
	if err := journal.persistLocked(ctx); err != nil {
		if !openAIToolCommitMayBeDurable(err) {
			if createdRun {
				delete(journal.state.Runs, run.RunID)
			}
			delete(journal.state.Records, operationID)
			delete(journal.state.Aliases, alias)
			for _, correlation := range correlations {
				delete(journal.state.Correlations, correlation)
			}
			_ = unlinkOpenAIToolFileAt(journal.directoryFile, record.EnvelopeFile)
		}
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, false, err
	}
	return record, envelope, false, nil
}

func openAIToolCorrelationDigest(kind, value string) string {
	digest := sha256.Sum256([]byte("stride-openai-tool-provider-" + kind + "-v1\x00" + strings.TrimSpace(value)))
	return kind + ":" + hex.EncodeToString(digest[:])
}

func (journal *openAIToolJournal) BeginAttempt(ctx context.Context, operationID, exactPreimageDigest string) error {
	return journal.transition(ctx, operationID, openAIToolStateReserved, func(envelope *openAIToolReplayEnvelope, record *openAIToolJournalRecord) error {
		if record.PreimageDigest != strings.TrimSpace(exactPreimageDigest) {
			return errors.New("OpenAI tool effect preimage changed before execution")
		}
		record.AttemptCount++
		return nil
	})
}

func (journal *openAIToolJournal) CommitEffect(ctx context.Context, operationID string, commit openAIToolEffectCommit) error {
	return journal.transition(ctx, operationID, openAIToolStateEffectCommitted, func(envelope *openAIToolReplayEnvelope, record *openAIToolJournalRecord) error {
		if !json.Valid(commit.FunctionOutput) {
			return errors.New("OpenAI tool output must be valid JSON")
		}
		if strings.TrimSpace(commit.PostimageDigest) == "" || strings.TrimSpace(commit.ReconciliationDigest) == "" {
			return errors.New("OpenAI tool effect postimage and reconciliation digests are required")
		}
		if err := validateOpenAIToolMinimizedResult(record.ToolName, commit.FunctionOutput); err != nil {
			return err
		}
		resultDigest := sha256.Sum256(commit.FunctionOutput)
		record.PostimageDigest, record.ReconciliationDigest = commit.PostimageDigest, commit.ReconciliationDigest
		record.ResultDigest = hex.EncodeToString(resultDigest[:])
		envelope.ToolOutput = append(json.RawMessage(nil), commit.FunctionOutput...)
		return nil
	})
}

func (journal *openAIToolJournal) MarkContinuationSent(ctx context.Context, operationID string, history []openAIResponsesToolInputItem) error {
	return journal.transition(ctx, operationID, openAIToolStateContinuationSent, func(envelope *openAIToolReplayEnvelope, record *openAIToolJournalRecord) error {
		envelope.ManualHistory = cloneOpenAIToolHistory(history)
		return nil
	})
}

func (journal *openAIToolJournal) RecordTerminalResponse(ctx context.Context, operationID, providerResponseID, finalOutput string, exactOutputItems []json.RawMessage) error {
	return journal.RecordRunTerminalResponse(ctx, []string{operationID}, providerResponseID, finalOutput, exactOutputItems)
}

func (journal *openAIToolJournal) RecordRunTerminalResponse(ctx context.Context, operationIDs []string, providerResponseID, finalOutput string, exactOutputItems []json.RawMessage) error {
	if strings.TrimSpace(providerResponseID) == "" || strings.TrimSpace(finalOutput) == "" || len(exactOutputItems) == 0 {
		return errors.New("OpenAI tool terminal response id, text, and exact output items are required")
	}
	if len(operationIDs) == 0 {
		return errors.New("OpenAI tool terminal response requires an exact run operation set")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return err
	}
	workingRecords := cloneOpenAIToolRecords(journal.state.Records)
	workingCorrelations := cloneOpenAIToolStringMap(journal.state.Correlations)
	first, exists := workingRecords[operationIDs[0]]
	if !exists {
		return errors.New("OpenAI tool operation is unknown")
	}
	runMembers := make([]openAIToolJournalRecord, 0, len(operationIDs))
	for _, candidate := range workingRecords {
		if candidate.RunID == first.RunID {
			runMembers = append(runMembers, candidate)
		}
	}
	sort.Slice(runMembers, func(i, k int) bool { return runMembers[i].RunSequence < runMembers[k].RunSequence })
	memberIDs := make([]string, len(runMembers))
	for index := range runMembers {
		memberIDs[index] = runMembers[index].OperationID
	}
	if !equalOpenAIToolStrings(operationIDs, memberIDs) {
		return errors.New("OpenAI tool terminal response operation set is not the complete ordered run")
	}
	keys, err := journal.currentKeys(ctx)
	if err != nil {
		return err
	}
	newEnvelopeFiles := make([]string, 0, len(operationIDs))
	cleanup := func() {
		for _, envelopeFile := range newEnvelopeFiles {
			_ = unlinkOpenAIToolFileAt(journal.directoryFile, envelopeFile)
		}
	}
	for _, operationID := range operationIDs {
		record := workingRecords[operationID]
		if record.RunID != first.RunID || record.State != openAIToolStateContinuationSent && record.State != openAIToolStateCompleted {
			cleanup()
			return errors.New("OpenAI tool run member is not ready for a terminal response")
		}
		envelope, err := journal.readEnvelope(ctx, record)
		if err != nil {
			cleanup()
			return err
		}
		if envelope.FinalOutput != "" || len(envelope.FinalOutputItems) != 0 {
			if envelope.FinalOutput != strings.TrimSpace(finalOutput) || !openAIToolRawItemsEqual(envelope.FinalOutputItems, exactOutputItems) {
				cleanup()
				return errors.New("OpenAI terminal response changed after its first durable record")
			}
		}
		// One terminal Responses item closes the whole serial run. Bind its
		// minimized correlation independently to each immutable operation so the
		// journal can authenticate every member without treating the shared
		// provider response as a cross-operation collision.
		correlation := openAIToolCorrelationDigest("terminal-response:"+record.OperationID, providerResponseID)
		if existingID, exists := workingCorrelations[correlation]; exists && existingID != operationID {
			cleanup()
			return errors.New("OpenAI terminal response correlation collides with another operation")
		}
		if !openAIToolContainsString(record.CorrelationDigests, correlation) {
			record.CorrelationDigests = append(record.CorrelationDigests, correlation)
			workingCorrelations[correlation] = operationID
		}
		if !openAIToolContainsString(envelope.TerminalResponseIDs, providerResponseID) {
			envelope.TerminalResponseIDs = append(envelope.TerminalResponseIDs, providerResponseID)
		}
		envelope.FinalOutput = strings.TrimSpace(finalOutput)
		envelope.FinalOutputItems = cloneOpenAIToolRawItems(exactOutputItems)
		record.UpdatedAt, envelope.UpdatedAt = journal.now(), journal.now()
		record.AEADKeyID, record.AEADKeyVersion = keys.AEADKeyID, keys.AEADVersion
		if err := journal.writeEnvelope(ctx, &record, envelope, keys.AEADKey); err != nil {
			cleanup()
			return err
		}
		newEnvelopeFiles = append(newEnvelopeFiles, record.EnvelopeFile)
		workingRecords[operationID] = record
	}
	previousRecords, previousCorrelations := journal.state.Records, journal.state.Correlations
	journal.state.Records, journal.state.Correlations = workingRecords, workingCorrelations
	if err := journal.persistLocked(ctx); err != nil {
		if !openAIToolCommitMayBeDurable(err) {
			journal.state.Records, journal.state.Correlations = previousRecords, previousCorrelations
			cleanup()
		}
		return err
	}
	return nil
}

func (journal *openAIToolJournal) Complete(ctx context.Context, operationID string) error {
	return journal.transition(ctx, operationID, openAIToolStateCompleted, func(envelope *openAIToolReplayEnvelope, record *openAIToolJournalRecord) error {
		if strings.TrimSpace(envelope.FinalOutput) == "" || len(envelope.FinalOutputItems) == 0 {
			return errors.New("OpenAI tool terminal response was not durably recorded")
		}
		if strings.TrimSpace(record.FinalizationRunDigest) == "" || len(record.FinalizationOperationIDs) == 0 || strings.TrimSpace(record.FinalUseDigest) == "" || strings.TrimSpace(record.FanOutReceiptDigest) == "" {
			return errors.New("OpenAI tool final use and fan-out were not durably receipted")
		}
		return nil
	})
}

func (journal *openAIToolJournal) CommitFinalUse(ctx context.Context, operationID string, commit openAIToolFinalizationCommit) error {
	if strings.TrimSpace(commit.RunDigest) == "" || len(commit.OperationIDs) == 0 || strings.TrimSpace(commit.FinalUseDigest) == "" || strings.TrimSpace(commit.FanOutReceiptDigest) == "" {
		return errors.New("OpenAI tool final use and fan-out receipt digests are required")
	}
	return journal.transition(ctx, operationID, openAIToolStateContinuationSent, func(envelope *openAIToolReplayEnvelope, record *openAIToolJournalRecord) error {
		members := make([]openAIToolJournalRecord, 0, len(commit.OperationIDs))
		for _, candidate := range journal.state.Records {
			if candidate.RunID == record.RunID {
				members = append(members, candidate)
			}
		}
		sort.Slice(members, func(i, k int) bool { return members[i].RunSequence < members[k].RunSequence })
		memberIDs := make([]string, len(members))
		for index := range members {
			if members[index].RunSequence != uint64(index) {
				return errors.New("OpenAI tool run sequence is not contiguous")
			}
			memberIDs[index] = members[index].OperationID
		}
		if !equalOpenAIToolStrings(commit.OperationIDs, memberIDs) {
			return errors.New("OpenAI tool finalization operation set changed")
		}
		wantRunDigest, err := openAIToolFinalizationRunDigest(openAIToolRunBaseExpectation(record.Expectation), record.RunID, envelope.FinalOutput, memberIDs)
		if err != nil || commit.RunDigest != wantRunDigest {
			return errors.New("OpenAI tool finalization digest changed")
		}
		if record.FinalUseDigest != "" && (record.FinalizationRunDigest != commit.RunDigest || !equalOpenAIToolStrings(record.FinalizationOperationIDs, commit.OperationIDs) || record.FinalUseDigest != commit.FinalUseDigest || record.FanOutReceiptDigest != commit.FanOutReceiptDigest) {
			return errors.New("OpenAI tool final use receipt changed on replay")
		}
		record.FinalizationRunDigest = strings.TrimSpace(commit.RunDigest)
		record.FinalizationOperationIDs = append([]string(nil), commit.OperationIDs...)
		record.FinalUseDigest = strings.TrimSpace(commit.FinalUseDigest)
		record.FanOutReceiptDigest = strings.TrimSpace(commit.FanOutReceiptDigest)
		return nil
	})
}

func (journal *openAIToolJournal) Quarantine(ctx context.Context, operationID, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("OpenAI tool quarantine reason is required")
	}
	return journal.transition(ctx, operationID, openAIToolStateQuarantined, func(envelope *openAIToolReplayEnvelope, record *openAIToolJournalRecord) error {
		record.QuarantineReason = reason
		return nil
	})
}

func (journal *openAIToolJournal) transition(ctx context.Context, operationID string, next openAIToolOperationState, mutate func(*openAIToolReplayEnvelope, *openAIToolJournalRecord) error) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return err
	}
	record, ok := journal.state.Records[strings.TrimSpace(operationID)]
	if !ok {
		return errors.New("OpenAI tool operation is unknown")
	}
	if !allowedOpenAIToolTransition(record.State, next) {
		return fmt.Errorf("invalid OpenAI tool journal transition %s -> %s", record.State, next)
	}
	effectiveNext := next
	if record.State == openAIToolStateCompleted && next == openAIToolStateContinuationSent {
		effectiveNext = openAIToolStateCompleted
	}
	previousCorrelations := cloneOpenAIToolStringMap(journal.state.Correlations)
	envelope, err := journal.readEnvelope(ctx, record)
	if err != nil {
		return err
	}
	if err := mutate(&envelope, &record); err != nil {
		journal.state.Correlations = previousCorrelations
		return err
	}
	keys, err := journal.currentKeys(ctx)
	if err != nil {
		return err
	}
	record.State, record.UpdatedAt, envelope.UpdatedAt = effectiveNext, journal.now(), journal.now()
	record.AEADKeyID, record.AEADKeyVersion = keys.AEADKeyID, keys.AEADVersion
	oldEnvelopeFile := record.EnvelopeFile
	if err := journal.writeEnvelope(ctx, &record, envelope, keys.AEADKey); err != nil {
		journal.state.Correlations = previousCorrelations
		return err
	}
	previous := journal.state.Records[record.OperationID]
	journal.state.Records[record.OperationID] = record
	if err := journal.persistLocked(ctx); err != nil {
		if !openAIToolCommitMayBeDurable(err) {
			journal.state.Records[record.OperationID] = previous
			journal.state.Correlations = previousCorrelations
			_ = unlinkOpenAIToolFileAt(journal.directoryFile, record.EnvelopeFile)
		}
		return err
	}
	_ = oldEnvelopeFile // retained until governed purge; never overwritten in place
	return nil
}

func cloneOpenAIToolStringMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneOpenAIToolRecords(source map[string]openAIToolJournalRecord) map[string]openAIToolJournalRecord {
	clone := make(map[string]openAIToolJournalRecord, len(source))
	for operationID, record := range source {
		record.EffectAliases = cloneOpenAIToolStringMap(record.EffectAliases)
		record.CorrelationDigests = append([]string(nil), record.CorrelationDigests...)
		record.FinalizationOperationIDs = append([]string(nil), record.FinalizationOperationIDs...)
		clone[operationID] = record
	}
	return clone
}

func cloneOpenAIToolRuns(source map[string]openAIToolRunRecord) map[string]openAIToolRunRecord {
	clone := make(map[string]openAIToolRunRecord, len(source))
	for runID, record := range source {
		clone[runID] = record
	}
	return clone
}

func allowedOpenAIToolTransition(current, next openAIToolOperationState) bool {
	if next == openAIToolStateQuarantined && current != openAIToolStateQuarantined {
		return true
	}
	switch current {
	case openAIToolStateReserved:
		return next == openAIToolStateReserved || next == openAIToolStateEffectCommitted
	case openAIToolStateEffectCommitted:
		return next == openAIToolStateContinuationSent
	case openAIToolStateContinuationSent:
		return next == openAIToolStateContinuationSent || next == openAIToolStateCompleted
	case openAIToolStateCompleted:
		return next == openAIToolStateContinuationSent || next == openAIToolStateCompleted
	default:
		return false
	}
}

func (journal *openAIToolJournal) Record(ctx context.Context, operationID string) (openAIToolJournalRecord, openAIToolReplayEnvelope, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, err
	}
	return journal.recordLocked(ctx, operationID)
}

func (journal *openAIToolJournal) recordLocked(ctx context.Context, operationID string) (openAIToolJournalRecord, openAIToolReplayEnvelope, error) {
	record, ok := journal.state.Records[strings.TrimSpace(operationID)]
	if !ok {
		return openAIToolJournalRecord{}, openAIToolReplayEnvelope{}, errors.New("OpenAI tool operation is unknown")
	}
	envelope, err := journal.readEnvelope(ctx, record)
	return record, envelope, err
}

func (journal *openAIToolJournal) BeginOrResumeRun(ctx context.Context, expectation openAIToolAuthorityExpectation, requestDigest, proposedRunID string) (openAIToolRunRecord, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return openAIToolRunRecord{}, err
	}
	expectation = openAIToolRunBaseExpectation(expectation)
	if err := expectation.validate(); err != nil {
		return openAIToolRunRecord{}, err
	}
	requestDigest, proposedRunID = strings.TrimSpace(requestDigest), strings.TrimSpace(proposedRunID)
	if requestDigest == "" || proposedRunID == "" {
		return openAIToolRunRecord{}, errors.New("OpenAI tool run request digest and proposed ID are required")
	}
	expectationDigest, _, err := openAIToolCanonicalDigest(expectation)
	if err != nil {
		return openAIToolRunRecord{}, err
	}
	var match *openAIToolRunRecord
	for _, run := range journal.state.Runs {
		if run.RequestDigest != requestDigest || run.ExpectationDigest != expectationDigest || run.State != openAIToolRunActive && run.State != openAIToolRunBlocked && run.State != openAIToolRunCompleted {
			continue
		}
		if run.State == openAIToolRunCompleted {
			hasOperation := false
			for _, record := range journal.state.Records {
				if record.RunID == run.RunID {
					hasOperation = true
					break
				}
			}
			// Terminal-only conversation runs have no encrypted operation
			// envelope from which to reconstruct their exact terminal text.
			// They remain provider-replayable; effect-bearing completed runs
			// are returned directly and require zero provider calls.
			if !hasOperation {
				continue
			}
		}
		if match != nil {
			return openAIToolRunRecord{}, errors.New("OpenAI tool journal has multiple live runs for one exact request")
		}
		copy := run
		match = &copy
	}
	if match != nil {
		if match.State == openAIToolRunBlocked {
			return *match, errOpenAIToolRunMaxTurns
		}
		return *match, nil
	}
	if _, exists := journal.state.Runs[proposedRunID]; exists {
		return openAIToolRunRecord{}, errors.New("OpenAI tool proposed run ID is already bound")
	}
	now := journal.now()
	run := openAIToolRunRecord{
		RunID: proposedRunID, RequestDigest: requestDigest, ExpectationDigest: expectationDigest,
		Expectation: expectation, State: openAIToolRunActive, CreatedAt: now, UpdatedAt: now,
	}
	previousRuns := journal.state.Runs
	journal.state.Runs = cloneOpenAIToolRuns(journal.state.Runs)
	journal.state.Runs[run.RunID] = run
	if err := journal.persistLocked(ctx); err != nil {
		if !openAIToolCommitMayBeDurable(err) {
			journal.state.Runs = previousRuns
		}
		return openAIToolRunRecord{}, err
	}
	return run, nil
}

// ActiveOperationRunForExpectation recovers an interrupted run from its exact
// encrypted operation envelopes even when reconstructing non-authoritative
// display context (for example the wall clock) changes the candidate request
// digest after process restart. The immutable tenant/thread/artifact/source
// expectation still has to match and exactly one live run with operations may
// exist. A zero-operation run is never adopted because it has no encrypted
// manual history to replay.
func (journal *openAIToolJournal) ActiveOperationRunForExpectation(ctx context.Context, expectation openAIToolAuthorityExpectation) (openAIToolRunRecord, bool, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return openAIToolRunRecord{}, false, err
	}
	expectation = openAIToolRunBaseExpectation(expectation)
	if err := expectation.validate(); err != nil {
		return openAIToolRunRecord{}, false, err
	}
	var match *openAIToolRunRecord
	for _, run := range journal.state.Runs {
		if run.State != openAIToolRunActive && run.State != openAIToolRunBlocked || !openAIToolSameRunExpectation(expectation, run.Expectation) {
			continue
		}
		hasOperation := false
		for _, record := range journal.state.Records {
			if record.RunID == run.RunID {
				hasOperation = true
				break
			}
		}
		if !hasOperation {
			continue
		}
		if match != nil {
			return openAIToolRunRecord{}, false, errors.New("OpenAI tool journal has multiple live operation runs for one authority expectation")
		}
		copy := run
		match = &copy
	}
	if match == nil {
		return openAIToolRunRecord{}, false, nil
	}
	return *match, true, nil
}

func (journal *openAIToolJournal) BeginProviderTurn(ctx context.Context, runID string) (uint64, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return 0, err
	}
	run, exists := journal.state.Runs[strings.TrimSpace(runID)]
	if !exists {
		return 0, errors.New("OpenAI tool run is unknown")
	}
	if run.State == openAIToolRunBlocked {
		return run.ProviderTurnCount, errOpenAIToolRunMaxTurns
	}
	if run.State != openAIToolRunActive {
		return run.ProviderTurnCount, errors.New("OpenAI tool run is not active")
	}
	previousRuns := journal.state.Runs
	journal.state.Runs = cloneOpenAIToolRuns(journal.state.Runs)
	if run.ProviderTurnCount >= openAIToolRunnerMaxTurns {
		run.State, run.BlockedReason, run.UpdatedAt = openAIToolRunBlocked, "max_provider_turns", journal.now()
		journal.state.Runs[run.RunID] = run
		if err := journal.persistLocked(ctx); err != nil {
			if !openAIToolCommitMayBeDurable(err) {
				journal.state.Runs = previousRuns
			}
			return run.ProviderTurnCount, err
		}
		return run.ProviderTurnCount, errOpenAIToolRunMaxTurns
	}
	run.ProviderTurnCount++
	run.UpdatedAt = journal.now()
	journal.state.Runs[run.RunID] = run
	if err := journal.persistLocked(ctx); err != nil {
		if !openAIToolCommitMayBeDurable(err) {
			journal.state.Runs = previousRuns
		}
		return 0, err
	}
	return run.ProviderTurnCount, nil
}

func (journal *openAIToolJournal) CompleteRun(ctx context.Context, runID, terminalText string) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return err
	}
	run, exists := journal.state.Runs[strings.TrimSpace(runID)]
	if !exists {
		return errors.New("OpenAI tool run is unknown")
	}
	terminalText = strings.TrimSpace(terminalText)
	if terminalText == "" {
		return errors.New("OpenAI tool completed run terminal text is required")
	}
	operationMembers := make([]openAIToolJournalRecord, 0)
	for _, record := range journal.state.Records {
		if record.RunID == run.RunID {
			operationMembers = append(operationMembers, record)
		}
	}
	sort.Slice(operationMembers, func(i, k int) bool { return operationMembers[i].RunSequence < operationMembers[k].RunSequence })
	operationIDs := make([]string, len(operationMembers))
	for index, record := range operationMembers {
		if record.RunSequence != uint64(index) || record.State != openAIToolStateCompleted {
			return errors.New("OpenAI tool run cannot complete with unfinished operations")
		}
		_, envelope, err := journal.recordLocked(ctx, record.OperationID)
		if err != nil || envelope.FinalOutput != terminalText || len(envelope.FinalOutputItems) == 0 {
			return errors.New("OpenAI tool run terminal envelope is missing or changed")
		}
		operationIDs[index] = record.OperationID
	}
	terminalDigest, _, err := openAIToolCanonicalDigest(map[string]any{
		"domain": "stride-openai-tool-run-terminal-v1", "run_id": run.RunID,
		"terminal_text": terminalText, "operation_ids": operationIDs,
	})
	if err != nil {
		return err
	}
	if run.State == openAIToolRunCompleted {
		if run.TerminalDigest != terminalDigest {
			return errors.New("OpenAI tool completed run terminal digest changed")
		}
		return nil
	}
	if run.State != openAIToolRunActive {
		return errors.New("OpenAI tool run cannot complete from its current state")
	}
	previousRuns := journal.state.Runs
	journal.state.Runs = cloneOpenAIToolRuns(journal.state.Runs)
	run.State, run.TerminalDigest, run.UpdatedAt = openAIToolRunCompleted, terminalDigest, journal.now()
	journal.state.Runs[run.RunID] = run
	if err := journal.persistLocked(ctx); err != nil {
		if !openAIToolCommitMayBeDurable(err) {
			journal.state.Runs = previousRuns
		}
		return err
	}
	return nil
}

func (journal *openAIToolJournal) SupersedeRun(ctx context.Context, runID, adoptedRunID string) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return err
	}
	run, exists := journal.state.Runs[strings.TrimSpace(runID)]
	_, adoptedExists := journal.state.Runs[strings.TrimSpace(adoptedRunID)]
	if !exists || !adoptedExists || run.RunID == adoptedRunID || run.State != openAIToolRunActive {
		return errors.New("OpenAI tool run supersession is invalid")
	}
	for _, record := range journal.state.Records {
		if record.RunID == run.RunID {
			return errors.New("OpenAI tool run with bound operations cannot be superseded")
		}
	}
	previousRuns := journal.state.Runs
	journal.state.Runs = cloneOpenAIToolRuns(journal.state.Runs)
	run.State, run.SupersededByRunID, run.UpdatedAt = openAIToolRunSuperseded, adoptedRunID, journal.now()
	journal.state.Runs[run.RunID] = run
	if err := journal.persistLocked(ctx); err != nil {
		if !openAIToolCommitMayBeDurable(err) {
			journal.state.Runs = previousRuns
		}
		return err
	}
	return nil
}

func (journal *openAIToolJournal) VerifyCurrent(ctx context.Context) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	return journal.requireUsableLocked(ctx)
}

type openAIToolPendingOperation struct {
	Record   openAIToolJournalRecord
	Envelope openAIToolReplayEnvelope
}

func (journal *openAIToolJournal) RunOperations(ctx context.Context, runID string) ([]openAIToolPendingOperation, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return nil, err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, errors.New("OpenAI tool run ID is required")
	}
	if _, exists := journal.state.Runs[runID]; !exists {
		return nil, errors.New("OpenAI tool run is unknown")
	}
	members := make([]openAIToolPendingOperation, 0)
	for _, record := range journal.state.Records {
		if record.RunID != runID {
			continue
		}
		envelope, err := journal.readEnvelope(ctx, record)
		if err != nil {
			return nil, err
		}
		members = append(members, openAIToolPendingOperation{Record: record, Envelope: envelope})
	}
	sort.Slice(members, func(i, k int) bool { return members[i].Record.RunSequence < members[k].Record.RunSequence })
	for index := range members {
		if members[index].Record.RunSequence != uint64(index) {
			return nil, errors.New("OpenAI tool run sequence is not contiguous")
		}
	}
	return members, nil
}

func (journal *openAIToolJournal) PendingForExpectation(ctx context.Context, expectation openAIToolAuthorityExpectation) ([]openAIToolPendingOperation, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return nil, err
	}
	pending := make([]openAIToolPendingOperation, 0)
	pendingRunIDs := map[string]bool{}
	for _, record := range journal.state.Records {
		if record.State == openAIToolStateCompleted || !openAIToolSameRunExpectation(expectation, record.Expectation) {
			continue
		}
		pendingRunIDs[record.RunID] = true
	}
	if len(pendingRunIDs) > 1 {
		return nil, errors.New("OpenAI tool journal has multiple pending runs for one authority expectation")
	}
	if len(pendingRunIDs) == 0 {
		return pending, nil
	}
	var pendingRunID string
	for runID := range pendingRunIDs {
		pendingRunID = runID
	}
	for _, record := range journal.state.Records {
		if record.RunID != pendingRunID {
			continue
		}
		envelope, err := journal.readEnvelope(ctx, record)
		if err != nil {
			return nil, err
		}
		pending = append(pending, openAIToolPendingOperation{Record: record, Envelope: envelope})
	}
	sort.Slice(pending, func(i, k int) bool {
		return pending[i].Record.RunSequence < pending[k].Record.RunSequence
	})
	return pending, nil
}

func openAIToolSameRunExpectation(base, operation openAIToolAuthorityExpectation) bool {
	base = openAIToolRunBaseExpectation(base)
	operation = openAIToolRunBaseExpectation(operation)
	baseRaw, baseErr := canonicalJSON(base)
	operationRaw, operationErr := canonicalJSON(operation)
	return baseErr == nil && operationErr == nil && hmac.Equal(baseRaw, operationRaw)
}

func openAIToolRunBaseExpectation(expectation openAIToolAuthorityExpectation) openAIToolAuthorityExpectation {
	expectation.ToolName, expectation.ManifestDigest, expectation.SchemaDigest, expectation.ArgumentsDigest = "", "", "", ""
	expectation.PolicyRevision = expectation.RequestPolicyRevision
	return expectation
}

func (journal *openAIToolJournal) lockOperation(operationID string) func() {
	value, _ := journal.operationLocks.LoadOrStore(operationID, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func (journal *openAIToolJournal) RotateAliases(ctx context.Context, targetKeyID, targetKeyVersion string) (openAIToolAliasRotationReceipt, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return openAIToolAliasRotationReceipt{}, err
	}
	previousRecords := cloneOpenAIToolRecords(journal.state.Records)
	previousAliases := cloneOpenAIToolStringMap(journal.state.Aliases)
	previousReceipts := append([]openAIToolAliasRotationReceipt(nil), journal.state.RotationReceipts...)
	committed := false
	defer func() {
		if !committed {
			journal.state.Records = previousRecords
			journal.state.Aliases = previousAliases
			journal.state.RotationReceipts = previousReceipts
		}
	}()
	targetKeyID, targetKeyVersion = strings.TrimSpace(targetKeyID), strings.TrimSpace(targetKeyVersion)
	if targetKeyID == "" || targetKeyVersion == "" {
		return openAIToolAliasRotationReceipt{}, errors.New("OpenAI tool effect rotation target key identity is required")
	}
	targetKey, err := journal.keyring.OpenAIToolJournalEffectKey(ctx, targetKeyID, targetKeyVersion)
	if err != nil {
		return openAIToolAliasRotationReceipt{}, err
	}
	if len(targetKey) < 32 {
		return openAIToolAliasRotationReceipt{}, errors.New("OpenAI tool effect rotation target key is invalid")
	}
	if err := journal.keyring.ValidateOpenAIToolEffectRotationTarget(ctx, targetKeyID, targetKeyVersion); err != nil {
		return openAIToolAliasRotationReceipt{}, fmt.Errorf("OpenAI tool effect rotation target is not distinct: %w", err)
	}
	currentKeys, err := journal.currentKeys(ctx)
	if err != nil {
		return openAIToolAliasRotationReceipt{}, err
	}
	if targetKeyID == currentKeys.MACKeyID || targetKeyID == currentKeys.AEADKeyID || hmac.Equal(targetKey, currentKeys.MACKey) || hmac.Equal(targetKey, currentKeys.AEADKey) {
		return openAIToolAliasRotationReceipt{}, errors.New("OpenAI tool effect rotation target reuses a current MAC or AEAD identity or secret")
	}
	operationIDs := make([]string, 0, len(journal.state.Records))
	for operationID := range journal.state.Records {
		operationIDs = append(operationIDs, operationID)
	}
	sort.Strings(operationIDs)
	changed := false
	aliasVersion := targetKeyID + "@" + targetKeyVersion
	for _, operationID := range operationIDs {
		record := journal.state.Records[operationID]
		if _, exists := record.EffectAliases[aliasVersion]; exists {
			continue
		}
		envelope, err := journal.readEnvelope(ctx, record)
		if err != nil {
			return openAIToolAliasRotationReceipt{}, err
		}
		alias, err := openAIToolEffectAlias(targetKeyID, targetKeyVersion, targetKey, envelope.Expectation)
		if err != nil {
			return openAIToolAliasRotationReceipt{}, err
		}
		if other, exists := journal.state.Aliases[alias]; exists && other != operationID {
			return openAIToolAliasRotationReceipt{}, errOpenAIToolJournalConflict
		}
		record.EffectAliases[aliasVersion] = alias
		record.UpdatedAt = journal.now()
		journal.state.Records[operationID] = record
		journal.state.Aliases[alias] = operationID
		changed = true
	}
	operationSetDigest, _, err := openAIToolCanonicalDigest(operationIDs)
	if err != nil {
		return openAIToolAliasRotationReceipt{}, err
	}
	receipt := openAIToolAliasRotationReceipt{
		JournalID: journal.journalID, Generation: journal.anchor.Generation + 1,
		EffectKeyID: targetKeyID, EffectKeyVersion: targetKeyVersion,
		OperationCount: len(operationIDs), OperationSetDigest: operationSetDigest, CreatedAt: journal.now(),
	}
	material, err := openAIToolRotationReceiptMaterial(receipt)
	if err != nil {
		return openAIToolAliasRotationReceipt{}, err
	}
	receipt.SigningKeyID, receipt.SigningKeyVersion, receipt.Signature, err = journal.keyring.SignOpenAIToolRotationReceipt(ctx, material)
	if err != nil || strings.TrimSpace(receipt.SigningKeyID) == "" || strings.TrimSpace(receipt.SigningKeyVersion) == "" || strings.TrimSpace(receipt.Signature) == "" {
		return openAIToolAliasRotationReceipt{}, errors.New("OpenAI tool effect rotation receipt signing failed")
	}
	if err := journal.keyring.VerifyOpenAIToolRotationReceipt(ctx, receipt.SigningKeyID, receipt.SigningKeyVersion, material, receipt.Signature); err != nil {
		return openAIToolAliasRotationReceipt{}, errors.New("OpenAI tool effect rotation receipt signature did not verify")
	}
	journal.state.RotationReceipts = append(journal.state.RotationReceipts, receipt)
	if !changed && len(operationIDs) == 0 {
		// An empty signed receipt is still the retirement proof for an empty
		// journal and therefore advances one authenticated generation.
	}
	if err := journal.persistLocked(ctx); err != nil {
		if openAIToolCommitMayBeDurable(err) {
			committed = true
		}
		return openAIToolAliasRotationReceipt{}, err
	}
	committed = true
	return receipt, nil
}

func openAIToolRotationReceiptMaterial(receipt openAIToolAliasRotationReceipt) ([]byte, error) {
	receipt.SigningKeyID, receipt.SigningKeyVersion, receipt.Signature = "", "", ""
	return canonicalJSON(receipt)
}

func (journal *openAIToolJournal) CanRetireEffectKey(ctx context.Context, keyID, keyVersion string) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if err := journal.requireUsableLocked(ctx); err != nil {
		return err
	}
	keys, err := journal.currentKeys(ctx)
	if err != nil {
		return err
	}
	keyID, keyVersion = strings.TrimSpace(keyID), strings.TrimSpace(keyVersion)
	if keyID == keys.EffectKeyID && keyVersion == keys.EffectVersion {
		return errors.New("OpenAI tool current effect key cannot retire")
	}
	operationIDs := make([]string, 0, len(journal.state.Records))
	currentAliasVersion := keys.EffectKeyID + "@" + keys.EffectVersion
	for operationID, record := range journal.state.Records {
		operationIDs = append(operationIDs, operationID)
		if _, ok := record.EffectAliases[currentAliasVersion]; !ok {
			return errors.New("OpenAI tool effect key retirement lacks current aliases for every live operation")
		}
	}
	sort.Strings(operationIDs)
	digest, _, err := openAIToolCanonicalDigest(operationIDs)
	if err != nil {
		return err
	}
	for index := len(journal.state.RotationReceipts) - 1; index >= 0; index-- {
		receipt := journal.state.RotationReceipts[index]
		if receipt.EffectKeyID == keys.EffectKeyID && receipt.EffectKeyVersion == keys.EffectVersion && receipt.OperationCount == len(operationIDs) && receipt.OperationSetDigest == digest {
			material, materialErr := openAIToolRotationReceiptMaterial(receipt)
			if materialErr != nil || journal.keyring.VerifyOpenAIToolRotationReceipt(ctx, receipt.SigningKeyID, receipt.SigningKeyVersion, material, receipt.Signature) != nil {
				return errors.New("OpenAI tool effect key retirement receipt signature is invalid")
			}
			return nil
		}
	}
	return errors.New("OpenAI tool effect key retirement lacks a signed complete migration receipt")
}

func (journal *openAIToolJournal) writeEnvelope(ctx context.Context, record *openAIToolJournalRecord, envelope openAIToolReplayEnvelope, key []byte) error {
	if envelope.OperationID != record.OperationID || envelope.ToolName != record.ToolName || envelope.Version != openAIToolEnvelopeVersion {
		return errors.New("OpenAI tool replay envelope identity mismatch")
	}
	plaintext, err := canonicalJSON(envelope)
	if err != nil {
		return err
	}
	fileToken := uuid.NewString()
	record.EnvelopeFile = "operation-" + record.OperationID + "-" + fileToken + ".enc"
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	aad := []byte(journal.journalID + "\x00" + record.OperationID + "\x00" + record.ToolName + "\x00" + record.AEADKeyID + "\x00" + record.AEADKeyVersion)
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	wrapper, err := canonicalJSON(map[string]string{
		"version": openAIToolEnvelopeVersion, "key_id": record.AEADKeyID, "key_version": record.AEADKeyVersion,
		"nonce": base64.RawStdEncoding.EncodeToString(nonce), "ciphertext": base64.RawStdEncoding.EncodeToString(ciphertext),
	})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(wrapper)
	record.EnvelopeSHA256 = hex.EncodeToString(digest[:])
	if err := atomicWriteOpenAIToolFileAt(journal.directoryFile, record.EnvelopeFile, wrapper); err != nil {
		if openAIToolCommitMayBeDurable(err) {
			journal.poisoned = err
		} else {
			_ = unlinkOpenAIToolFileAt(journal.directoryFile, record.EnvelopeFile)
		}
		return err
	}
	verified, err := readSecureOpenAIToolFileAt(journal.directoryFile, record.EnvelopeFile)
	if err != nil || !hmac.Equal(wrapper, verified) {
		journal.poisoned = fmt.Errorf("%w: replay envelope post-write verification failed", errOpenAIToolJournalCommittedUnverified)
		return journal.poisoned
	}
	return nil
}

func (journal *openAIToolJournal) readEnvelope(ctx context.Context, record openAIToolJournalRecord) (openAIToolReplayEnvelope, error) {
	if filepath.Base(record.EnvelopeFile) != record.EnvelopeFile || !strings.HasPrefix(record.EnvelopeFile, "operation-") || !strings.HasSuffix(record.EnvelopeFile, ".enc") {
		return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
	}
	raw, err := readSecureOpenAIToolFileAt(journal.directoryFile, record.EnvelopeFile)
	if err != nil {
		return openAIToolReplayEnvelope{}, err
	}
	digest := sha256.Sum256(raw)
	if !hmac.Equal([]byte(record.EnvelopeSHA256), []byte(hex.EncodeToString(digest[:]))) {
		return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
	}
	var wrapper struct {
		Version    string `json:"version"`
		KeyID      string `json:"key_id"`
		KeyVersion string `json:"key_version"`
		Nonce      string `json:"nonce"`
		Ciphertext string `json:"ciphertext"`
	}
	if err := decodeOpenAIToolJSONStrict(raw, &wrapper); err != nil || wrapper.Version != openAIToolEnvelopeVersion || wrapper.KeyID != record.AEADKeyID || wrapper.KeyVersion != record.AEADKeyVersion {
		return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
	}
	key, err := journal.keyring.OpenAIToolJournalAEADKey(ctx, wrapper.KeyID, wrapper.KeyVersion)
	if err != nil {
		return openAIToolReplayEnvelope{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return openAIToolReplayEnvelope{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return openAIToolReplayEnvelope{}, err
	}
	nonce, nonceErr := base64.RawStdEncoding.DecodeString(wrapper.Nonce)
	ciphertext, cipherErr := base64.RawStdEncoding.DecodeString(wrapper.Ciphertext)
	if nonceErr != nil || cipherErr != nil || len(nonce) != aead.NonceSize() {
		return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
	}
	aad := []byte(journal.journalID + "\x00" + record.OperationID + "\x00" + record.ToolName + "\x00" + record.AEADKeyID + "\x00" + record.AEADKeyVersion)
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
	}
	var envelope openAIToolReplayEnvelope
	if err := decodeOpenAIToolJSONStrict(plaintext, &envelope); err != nil || envelope.Version != openAIToolEnvelopeVersion || envelope.OperationID != record.OperationID || envelope.ToolName != record.ToolName {
		return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
	}
	argumentsDigest, _, _ := openAIToolCanonicalDigest(envelope.Arguments)
	expectationDigest, _, _ := openAIToolCanonicalDigest(envelope.Expectation)
	if argumentsDigest != record.ArgumentsSHA256 || expectationDigest != record.ExpectationSHA256 {
		return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
	}
	if len(envelope.ManualHistory) == 0 || len(envelope.ProviderResponseIDs) == 0 || len(envelope.ProviderCallIDs) == 0 || len(envelope.ExactOutputItems) == 0 {
		return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
	}
	for _, responseID := range envelope.ProviderResponseIDs {
		if strings.TrimSpace(responseID) == "" || !openAIToolContainsString(record.CorrelationDigests, openAIToolCorrelationDigest("response", responseID)) {
			return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
		}
	}
	for _, callID := range envelope.ProviderCallIDs {
		if strings.TrimSpace(callID) == "" || !openAIToolContainsString(record.CorrelationDigests, openAIToolCorrelationDigest("call", callID)) {
			return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
		}
	}
	for _, responseID := range envelope.TerminalResponseIDs {
		if strings.TrimSpace(responseID) == "" || !openAIToolContainsString(record.CorrelationDigests, openAIToolCorrelationDigest("terminal-response:"+record.OperationID, responseID)) {
			return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
		}
	}
	if record.State == openAIToolStateEffectCommitted || record.State == openAIToolStateContinuationSent || record.State == openAIToolStateCompleted {
		if !json.Valid(envelope.ToolOutput) || record.PostimageDigest == "" || record.ReconciliationDigest == "" || record.ResultDigest == "" {
			return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
		}
		resultDigest := sha256.Sum256(envelope.ToolOutput)
		if !hmac.Equal([]byte(record.ResultDigest), []byte(hex.EncodeToString(resultDigest[:]))) || validateOpenAIToolMinimizedResult(record.ToolName, envelope.ToolOutput) != nil {
			return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
		}
	}
	if record.State == openAIToolStateContinuationSent || record.State == openAIToolStateCompleted {
		if len(envelope.ManualHistory) < 2 {
			return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
		}
	}
	if envelope.FinalOutput != "" || len(envelope.FinalOutputItems) > 0 {
		if strings.TrimSpace(envelope.FinalOutput) == "" || len(envelope.FinalOutputItems) == 0 || len(envelope.TerminalResponseIDs) == 0 {
			return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
		}
	}
	if record.State == openAIToolStateCompleted && (strings.TrimSpace(envelope.FinalOutput) == "" || len(envelope.FinalOutputItems) == 0) {
		return openAIToolReplayEnvelope{}, errOpenAIToolJournalTampered
	}
	return envelope, nil
}

func decodeOpenAIToolJSONStrict(raw []byte, target any) error {
	uniqueDecoder := json.NewDecoder(bytes.NewReader(raw))
	uniqueDecoder.UseNumber()
	if _, err := decodeUniqueJSONValue(uniqueDecoder); err != nil {
		return err
	}
	if token, err := uniqueDecoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON token %v", token)
		}
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func cloneOpenAIToolRawItems(items []json.RawMessage) []json.RawMessage {
	if len(items) == 0 {
		return nil
	}
	clone := make([]json.RawMessage, len(items))
	for index := range items {
		clone[index] = append(json.RawMessage(nil), items[index]...)
	}
	return clone
}

func openAIToolRawItemsEqual(left, right []json.RawMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !hmac.Equal(left[index], right[index]) {
			return false
		}
	}
	return true
}

func cloneOpenAIToolHistory(history []openAIResponsesToolInputItem) []openAIResponsesToolInputItem {
	if len(history) == 0 {
		return nil
	}
	clone := make([]openAIResponsesToolInputItem, len(history))
	copy(clone, history)
	for index := range clone {
		clone[index].Raw = append(json.RawMessage(nil), history[index].Raw...)
		clone[index].Arguments = append(json.RawMessage(nil), history[index].Arguments...)
	}
	return clone
}
