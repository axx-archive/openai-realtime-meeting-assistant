package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
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
)

type CanonicalMode string

const (
	CanonicalModeOff      CanonicalMode = "off"
	CanonicalModeShadow   CanonicalMode = "shadow"
	CanonicalModeRequired CanonicalMode = "required"
)

type CanonicalSpoolPhase string

const (
	CanonicalSpoolPrepared  CanonicalSpoolPhase = "prepared"
	CanonicalSpoolCommitted CanonicalSpoolPhase = "committed"
	CanonicalSpoolAborted   CanonicalSpoolPhase = "aborted"
)

var (
	ErrCanonicalSpoolCorrupt   = errors.New("canonical spool corruption")
	ErrCanonicalSpoolSequence  = errors.New("canonical spool sequence is not monotonic")
	ErrCanonicalFamilyFrozen   = errors.New("canonical family frozen for reconciliation")
	ErrCanonicalCaptureInvalid = errors.New("invalid canonical capture transition")
	ErrCanonicalSpoolPoisoned  = errors.New("canonical spool unusable until reopen")
)

const canonicalSpoolMaxFrame = 8 << 20

var canonicalSpoolMagic = [4]byte{'B', 'C', 'S', '1'}

type CanonicalSpoolRecord struct {
	Sequence             uint64              `json:"sequence"`
	Phase                CanonicalSpoolPhase `json:"phase"`
	MutationID           string              `json:"mutation_id"`
	Family               string              `json:"family"`
	ObjectKey            string              `json:"object_key"`
	BeforeStateDigest    string              `json:"before_state_digest,omitempty"`
	AfterStateDigest     string              `json:"after_state_digest,omitempty"`
	PreviousFamilyDigest string              `json:"previous_family_digest,omitempty"`
	FamilyDigest         string              `json:"family_digest,omitempty"`
	Fact                 json.RawMessage     `json:"fact,omitempty"`
}

type CanonicalCapturedFact struct {
	Prepare CanonicalSpoolRecord
	Commit  CanonicalSpoolRecord
}

type CanonicalRecoveryState func(family, objectKey string) (stateDigest string, exists bool, err error)

type CanonicalRecoveryResult struct {
	SynthesizedCommits []string
	SynthesizedAborts  []string
	FrozenFamilies     []string
	TruncatedBytes     int64
}

type CanonicalCaptureSpool struct {
	mu          sync.Mutex
	path        string
	mode        CanonicalMode
	records     []CanonicalSpoolRecord
	byMutation  map[string][]int
	familyHeads map[string]string
	frozen      map[string]bool
	poisoned    bool
	next        uint64
}

type canonicalAppendFile interface {
	io.Writer
	Stat() (os.FileInfo, error)
	Truncate(int64) error
	Sync() error
	Close() error
}

var canonicalCaptureOpenAppend = func(path string) (canonicalAppendFile, bool, error) {
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return nil, false, statErr
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	return file, created, err
}

var canonicalCaptureSyncParent = syncCanonicalParentDir

func OpenCanonicalCaptureSpool(path string, mode CanonicalMode) (*CanonicalCaptureSpool, error) {
	if mode != CanonicalModeOff && mode != CanonicalModeShadow && mode != CanonicalModeRequired {
		return nil, fmt.Errorf("%w: unknown mode %q", ErrCanonicalCaptureInvalid, mode)
	}
	spool := &CanonicalCaptureSpool{
		path: path, mode: mode, byMutation: make(map[string][]int),
		familyHeads: make(map[string]string), frozen: make(map[string]bool), next: 1,
	}
	if mode == CanonicalModeOff {
		return spool, nil
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: spool path required", ErrCanonicalCaptureInvalid)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		// Creation is intentionally lazy. The first durable frame creates and
		// fsyncs the directory entry in the same operation.
		return spool, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	records, validBytes, truncated, err := readCanonicalSpoolFrames(file)
	if err != nil {
		return nil, err
	}
	if truncated > 0 {
		if err := file.Truncate(validBytes); err != nil {
			return nil, err
		}
		if err := file.Sync(); err != nil {
			return nil, err
		}
		if err := syncCanonicalParentDir(path); err != nil {
			return nil, err
		}
	}
	if err := spool.rebuild(records); err != nil {
		return nil, err
	}
	return spool, nil
}

func (spool *CanonicalCaptureSpool) Prepare(mutationID, family, objectKey, beforeDigest, afterDigest string, fact json.RawMessage) (CanonicalSpoolRecord, error) {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.mode == CanonicalModeOff {
		return CanonicalSpoolRecord{}, nil
	}
	if spool.frozen[family] {
		return CanonicalSpoolRecord{}, ErrCanonicalFamilyFrozen
	}
	if strings.TrimSpace(mutationID) == "" || strings.TrimSpace(family) == "" || strings.TrimSpace(objectKey) == "" ||
		!validOptionalStateDigest(beforeDigest) || !isHexDigest(afterDigest) || !json.Valid(fact) {
		return CanonicalSpoolRecord{}, ErrCanonicalCaptureInvalid
	}
	if _, _, terminal := spool.mutationState(mutationID); terminal || len(spool.byMutation[mutationID]) > 0 {
		return CanonicalSpoolRecord{}, fmt.Errorf("%w: duplicate mutation", ErrCanonicalCaptureInvalid)
	}
	record := CanonicalSpoolRecord{
		Sequence: spool.next, Phase: CanonicalSpoolPrepared, MutationID: mutationID,
		Family: family, ObjectKey: objectKey, BeforeStateDigest: beforeDigest, AfterStateDigest: afterDigest,
		PreviousFamilyDigest: spool.familyHeads[family], Fact: append(json.RawMessage(nil), fact...),
	}
	if err := spool.appendLocked(record); err != nil {
		return CanonicalSpoolRecord{}, err
	}
	return cloneCanonicalSpoolRecord(record), nil
}

func (spool *CanonicalCaptureSpool) Commit(mutationID string) (CanonicalSpoolRecord, error) {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.mode == CanonicalModeOff {
		return CanonicalSpoolRecord{}, nil
	}
	prepare, terminalRecord, terminal := spool.mutationState(mutationID)
	if terminal {
		if terminalRecord.Phase == CanonicalSpoolCommitted {
			return cloneCanonicalSpoolRecord(terminalRecord), nil
		}
		return CanonicalSpoolRecord{}, fmt.Errorf("%w: mutation already aborted", ErrCanonicalCaptureInvalid)
	}
	if prepare == nil || spool.frozen[prepare.Family] || prepare.PreviousFamilyDigest != spool.familyHeads[prepare.Family] {
		return CanonicalSpoolRecord{}, ErrCanonicalFamilyFrozen
	}
	record := spool.commitRecord(*prepare)
	if err := spool.appendLocked(record); err != nil {
		return CanonicalSpoolRecord{}, err
	}
	return cloneCanonicalSpoolRecord(record), nil
}

func (spool *CanonicalCaptureSpool) Abort(mutationID string) (CanonicalSpoolRecord, error) {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.mode == CanonicalModeOff {
		return CanonicalSpoolRecord{}, nil
	}
	prepare, terminalRecord, terminal := spool.mutationState(mutationID)
	if terminal {
		if terminalRecord.Phase == CanonicalSpoolAborted {
			return cloneCanonicalSpoolRecord(terminalRecord), nil
		}
		return CanonicalSpoolRecord{}, fmt.Errorf("%w: mutation already committed", ErrCanonicalCaptureInvalid)
	}
	if prepare == nil {
		return CanonicalSpoolRecord{}, fmt.Errorf("%w: prepare not found", ErrCanonicalCaptureInvalid)
	}
	record := CanonicalSpoolRecord{
		Sequence: spool.next, Phase: CanonicalSpoolAborted, MutationID: mutationID,
		Family: prepare.Family, ObjectKey: prepare.ObjectKey,
	}
	if err := spool.appendLocked(record); err != nil {
		return CanonicalSpoolRecord{}, err
	}
	return cloneCanonicalSpoolRecord(record), nil
}

func (spool *CanonicalCaptureSpool) Recover(current CanonicalRecoveryState) (CanonicalRecoveryResult, error) {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	if spool.mode == CanonicalModeOff {
		return CanonicalRecoveryResult{}, nil
	}
	if current == nil {
		return CanonicalRecoveryResult{}, errors.New("recovery state resolver required")
	}
	result := CanonicalRecoveryResult{}
	for _, record := range append([]CanonicalSpoolRecord(nil), spool.records...) {
		if record.Phase != CanonicalSpoolPrepared {
			continue
		}
		prepare, _, terminal := spool.mutationState(record.MutationID)
		if terminal || prepare == nil {
			continue
		}
		state, exists, err := current(record.Family, record.ObjectKey)
		if err != nil {
			return result, err
		}
		afterMatches := exists && state == record.AfterStateDigest
		beforeMatches := (!exists && record.BeforeStateDigest == "") || (exists && state == record.BeforeStateDigest)
		provedByLaterChain := spool.laterPrepareProvesCommit(record)
		switch {
		case afterMatches || provedByLaterChain:
			commit := spool.commitRecord(record)
			if err := spool.appendLocked(commit); err != nil {
				return result, err
			}
			result.SynthesizedCommits = append(result.SynthesizedCommits, record.MutationID)
		case beforeMatches:
			abort := CanonicalSpoolRecord{Sequence: spool.next, Phase: CanonicalSpoolAborted, MutationID: record.MutationID, Family: record.Family, ObjectKey: record.ObjectKey}
			if err := spool.appendLocked(abort); err != nil {
				return result, err
			}
			result.SynthesizedAborts = append(result.SynthesizedAborts, record.MutationID)
		default:
			spool.frozen[record.Family] = true
		}
	}
	for family := range spool.frozen {
		result.FrozenFamilies = append(result.FrozenFamilies, family)
	}
	sort.Strings(result.FrozenFamilies)
	return result, nil
}

func (spool *CanonicalCaptureSpool) CommittedFacts() []CanonicalCapturedFact {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	result := make([]CanonicalCapturedFact, 0)
	for mutationID := range spool.byMutation {
		prepare, terminal, done := spool.mutationState(mutationID)
		if done && terminal.Phase == CanonicalSpoolCommitted && prepare != nil {
			result = append(result, CanonicalCapturedFact{Prepare: cloneCanonicalSpoolRecord(*prepare), Commit: cloneCanonicalSpoolRecord(terminal)})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Commit.Sequence < result[j].Commit.Sequence })
	return result
}

func (spool *CanonicalCaptureSpool) Frozen(family string) bool {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	return spool.frozen[family]
}

func (spool *CanonicalCaptureSpool) appendLocked(record CanonicalSpoolRecord) error {
	if spool.poisoned {
		return ErrCanonicalSpoolPoisoned
	}
	if record.Sequence != spool.next {
		return ErrCanonicalSpoolSequence
	}
	if err := validateCanonicalSpoolRecord(record); err != nil {
		return err
	}
	poisoned, err := appendCanonicalSpoolFrame(spool.path, record)
	if err != nil {
		spool.poisoned = poisoned
		return err
	}
	spool.index(record)
	return nil
}

func (spool *CanonicalCaptureSpool) index(record CanonicalSpoolRecord) {
	index := len(spool.records)
	spool.records = append(spool.records, cloneCanonicalSpoolRecord(record))
	spool.byMutation[record.MutationID] = append(spool.byMutation[record.MutationID], index)
	if record.Phase == CanonicalSpoolCommitted {
		spool.familyHeads[record.Family] = record.FamilyDigest
	}
	spool.next = record.Sequence + 1
}

func (spool *CanonicalCaptureSpool) rebuild(records []CanonicalSpoolRecord) error {
	for _, record := range records {
		if record.Sequence != spool.next {
			return ErrCanonicalSpoolSequence
		}
		if err := validateCanonicalSpoolRecord(record); err != nil {
			return err
		}
		prepare, _, terminal := spool.mutationState(record.MutationID)
		if record.Phase == CanonicalSpoolPrepared && (prepare != nil || terminal) {
			return ErrCanonicalSpoolCorrupt
		}
		if record.Phase == CanonicalSpoolCommitted {
			if terminal || prepare == nil || prepare.Family != record.Family || prepare.ObjectKey != record.ObjectKey ||
				prepare.PreviousFamilyDigest != spool.familyHeads[record.Family] || record.FamilyDigest != canonicalFamilyDigest(*prepare) {
				return ErrCanonicalSpoolCorrupt
			}
		}
		if record.Phase == CanonicalSpoolAborted && (terminal || prepare == nil || prepare.Family != record.Family || prepare.ObjectKey != record.ObjectKey) {
			return ErrCanonicalSpoolCorrupt
		}
		spool.index(record)
	}
	return nil
}

func (spool *CanonicalCaptureSpool) mutationState(mutationID string) (*CanonicalSpoolRecord, CanonicalSpoolRecord, bool) {
	var prepare *CanonicalSpoolRecord
	var terminal CanonicalSpoolRecord
	for _, index := range spool.byMutation[mutationID] {
		record := &spool.records[index]
		if record.Phase == CanonicalSpoolPrepared {
			prepare = record
		} else {
			terminal = *record
			return prepare, terminal, true
		}
	}
	return prepare, terminal, false
}

func (spool *CanonicalCaptureSpool) commitRecord(prepare CanonicalSpoolRecord) CanonicalSpoolRecord {
	return CanonicalSpoolRecord{
		Sequence: spool.next, Phase: CanonicalSpoolCommitted, MutationID: prepare.MutationID,
		Family: prepare.Family, ObjectKey: prepare.ObjectKey, FamilyDigest: canonicalFamilyDigest(prepare),
	}
}

func (spool *CanonicalCaptureSpool) laterPrepareProvesCommit(prepare CanonicalSpoolRecord) bool {
	expected := canonicalFamilyDigest(prepare)
	for _, later := range spool.records {
		if later.Sequence > prepare.Sequence && later.Phase == CanonicalSpoolPrepared && later.Family == prepare.Family && later.PreviousFamilyDigest == expected {
			return true
		}
	}
	return false
}

func canonicalFamilyDigest(prepare CanonicalSpoolRecord) string {
	factDigest := sha256.Sum256(prepare.Fact)
	value := strings.Join([]string{prepare.PreviousFamilyDigest, prepare.MutationID, prepare.Family, prepare.ObjectKey, prepare.BeforeStateDigest, prepare.AfterStateDigest, hex.EncodeToString(factDigest[:])}, "\x1f")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func validateCanonicalSpoolRecord(record CanonicalSpoolRecord) error {
	if record.Sequence == 0 || strings.TrimSpace(record.MutationID) == "" || strings.TrimSpace(record.Family) == "" || strings.TrimSpace(record.ObjectKey) == "" {
		return ErrCanonicalCaptureInvalid
	}
	switch record.Phase {
	case CanonicalSpoolPrepared:
		if !validOptionalStateDigest(record.BeforeStateDigest) || !isHexDigest(record.AfterStateDigest) || !json.Valid(record.Fact) || record.FamilyDigest != "" {
			return ErrCanonicalCaptureInvalid
		}
	case CanonicalSpoolCommitted:
		if !isHexDigest(record.FamilyDigest) || len(record.Fact) != 0 {
			return ErrCanonicalCaptureInvalid
		}
	case CanonicalSpoolAborted:
		if record.FamilyDigest != "" || len(record.Fact) != 0 {
			return ErrCanonicalCaptureInvalid
		}
	default:
		return ErrCanonicalCaptureInvalid
	}
	return nil
}

func validOptionalStateDigest(value string) bool { return value == "" || isHexDigest(value) }

func cloneCanonicalSpoolRecord(record CanonicalSpoolRecord) CanonicalSpoolRecord {
	record.Fact = append(json.RawMessage(nil), record.Fact...)
	return record
}

func appendCanonicalSpoolFrame(path string, record CanonicalSpoolRecord) (bool, error) {
	payload, err := canonicalJSON(record)
	if err != nil {
		return false, err
	}
	if len(payload) > canonicalSpoolMaxFrame {
		return false, errors.New("canonical spool frame too large")
	}
	digest := sha256.Sum256(payload)
	var header [8]byte
	copy(header[:4], canonicalSpoolMagic[:])
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	frame := make([]byte, 0, len(header)+len(payload)+len(digest))
	frame = append(frame, header[:]...)
	frame = append(frame, payload...)
	frame = append(frame, digest[:]...)
	file, created, err := canonicalCaptureOpenAppend(path)
	if err != nil {
		return false, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return true, err
	}
	start := info.Size()
	if err := writeCanonicalAll(file, frame); err != nil {
		if rollbackCanonicalAppend(file, path, start, created) {
			return false, err
		}
		return true, err
	}
	if err := file.Sync(); err != nil {
		if rollbackCanonicalAppend(file, path, start, created) {
			return false, err
		}
		return true, err
	}
	if err := file.Close(); err != nil {
		return true, err
	}
	if created {
		if err := canonicalCaptureSyncParent(path); err != nil {
			return true, err
		}
	}
	return false, nil
}

// rollbackCanonicalAppend is safe only while the same open file description
// is known usable. Failure at any rollback step leaves on-disk state uncertain.
func rollbackCanonicalAppend(file canonicalAppendFile, path string, start int64, created bool) bool {
	if err := file.Truncate(start); err != nil {
		file.Close()
		return false
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return false
	}
	if err := file.Close(); err != nil {
		return false
	}
	if created {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false
		}
		if err := canonicalCaptureSyncParent(path); err != nil {
			return false
		}
	}
	return true
}

func writeCanonicalAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func readCanonicalSpoolFrames(file *os.File) ([]CanonicalSpoolRecord, int64, int64, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, 0, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, 0, 0, err
	}
	var records []CanonicalSpoolRecord
	var offset int64
	for offset < info.Size() {
		remaining := info.Size() - offset
		if remaining < 8 {
			return records, offset, remaining, nil
		}
		var header [8]byte
		if _, err := io.ReadFull(file, header[:]); err != nil {
			return nil, offset, 0, err
		}
		if !bytes.Equal(header[:4], canonicalSpoolMagic[:]) {
			return nil, offset, 0, ErrCanonicalSpoolCorrupt
		}
		length := int64(binary.BigEndian.Uint32(header[4:]))
		if length < 2 || length > canonicalSpoolMaxFrame {
			return nil, offset, 0, ErrCanonicalSpoolCorrupt
		}
		frameSize := int64(8) + length + sha256.Size
		if remaining < frameSize {
			return records, offset, remaining, nil
		}
		payload := make([]byte, length)
		var recordedDigest [32]byte
		if _, err := io.ReadFull(file, payload); err != nil {
			return nil, offset, 0, err
		}
		if _, err := io.ReadFull(file, recordedDigest[:]); err != nil {
			return nil, offset, 0, err
		}
		if sha256.Sum256(payload) != recordedDigest {
			return nil, offset, 0, ErrCanonicalSpoolCorrupt
		}
		var record CanonicalSpoolRecord
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, offset, 0, ErrCanonicalSpoolCorrupt
		}
		records = append(records, record)
		offset += frameSize
	}
	return records, offset, 0, nil
}
