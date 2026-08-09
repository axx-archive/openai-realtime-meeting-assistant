package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	strideE10OperationPrepared  = "prepared"
	strideE10OperationCommitted = "committed"
	strideE10OperationCompleted = "completed"
)

type StrideE10ProductOperationRecord struct {
	Key                  string                     `json:"key"`
	PersonID             string                     `json:"personId"`
	ActionID             string                     `json:"actionId"`
	IdempotencyKeyDigest string                     `json:"idempotencyKeyDigest"`
	Fingerprint          string                     `json:"fingerprint"`
	State                string                     `json:"state"`
	Binding              StrideE10LiveActionBinding `json:"binding"`
	Response             json.RawMessage            `json:"response,omitempty"`
	PreparedAt           time.Time                  `json:"preparedAt"`
	CommittedAt          *time.Time                 `json:"committedAt,omitempty"`
	CompletedAt          *time.Time                 `json:"completedAt,omitempty"`
}

type StrideE10ProductOperationStore interface {
	Load(key string) (StrideE10ProductOperationRecord, bool, error)
	FindAction(personID, actionID string) (StrideE10ProductOperationRecord, bool, error)
	Prepare(record StrideE10ProductOperationRecord) (StrideE10ProductOperationRecord, bool, error)
	Commit(key, fingerprint string, at time.Time) error
	Complete(key, fingerprint string, response any, at time.Time) error
	Abort(key, fingerprint string) error
}

type strideE10ProductOperationStore struct {
	mu         sync.RWMutex
	path       string
	keys       StrideE10ProductOperationKeyring
	generation uint64
	records    map[string]StrideE10ProductOperationRecord
}

type StrideE10ProductOperationMACKey struct {
	ID      string
	Version uint64
	Secret  []byte
}

type StrideE10ProductOperationKeyring interface {
	CurrentStrideE10ProductOperationKey(context.Context) (StrideE10ProductOperationMACKey, error)
	ResolveStrideE10ProductOperationKey(context.Context, string, uint64) (StrideE10ProductOperationMACKey, error)
}

type strideE10ProductOperationEnvelope struct {
	SchemaVersion uint64          `json:"schemaVersion"`
	Generation    uint64          `json:"generation"`
	KeyID         string          `json:"keyId"`
	KeyVersion    uint64          `json:"keyVersion"`
	Payload       json.RawMessage `json:"payload"`
	MAC           string          `json:"mac"`
}

func newStrideE10MemoryOperationStore() StrideE10ProductOperationStore {
	return &strideE10ProductOperationStore{records: map[string]StrideE10ProductOperationRecord{}}
}

func newStrideE10FileOperationStore(path string, keys StrideE10ProductOperationKeyring) (StrideE10ProductOperationStore, error) {
	if path == "" || filepath.Clean(path) != path || keys == nil {
		return nil, ErrStrideE10Invalid
	}
	store := &strideE10ProductOperationStore{path: path, keys: keys, records: map[string]StrideE10ProductOperationRecord{}}
	body, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(body) > 0 {
		var envelope strideE10ProductOperationEnvelope
		if json.Unmarshal(body, &envelope) != nil || envelope.SchemaVersion != 1 || envelope.Generation < 1 || !isHexDigest(envelope.MAC) {
			return nil, ErrStrideE10Invalid
		}
		key, keyErr := keys.ResolveStrideE10ProductOperationKey(context.Background(), envelope.KeyID, envelope.KeyVersion)
		if keyErr != nil || validateStrideE10ProductOperationMACKey(key) != nil || key.ID != envelope.KeyID || key.Version != envelope.KeyVersion || !hmac.Equal([]byte(envelope.MAC), []byte(strideE10ProductOperationMAC(key, envelope.Generation, envelope.Payload))) {
			return nil, ErrStrideE10Denied
		}
		if json.Unmarshal(envelope.Payload, &store.records) != nil {
			return nil, ErrStrideE10Invalid
		}
		store.generation = envelope.Generation
	}
	return store, nil
}

func (s *strideE10ProductOperationStore) Load(key string) (StrideE10ProductOperationRecord, bool, error) {
	if s == nil {
		return StrideE10ProductOperationRecord{}, false, ErrStrideE10Invalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[key]
	return cloneStrideE10OperationRecord(record), ok, nil
}

func (s *strideE10ProductOperationStore) FindAction(personID, actionID string) (StrideE10ProductOperationRecord, bool, error) {
	if s == nil || !strideIdentifier(personID) || !strideIdentifier(actionID) {
		return StrideE10ProductOperationRecord{}, false, ErrStrideE10Invalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.records {
		if record.PersonID == personID && record.ActionID == actionID {
			return cloneStrideE10OperationRecord(record), true, nil
		}
	}
	return StrideE10ProductOperationRecord{}, false, nil
}

func (s *strideE10ProductOperationStore) Prepare(record StrideE10ProductOperationRecord) (StrideE10ProductOperationRecord, bool, error) {
	if s == nil || !isHexDigest(record.Key) || !strideIdentifier(record.PersonID) || !strideIdentifier(record.ActionID) || !isHexDigest(record.IdempotencyKeyDigest) || !isHexDigest(record.Fingerprint) || record.State != strideE10OperationPrepared || record.PreparedAt.IsZero() {
		return StrideE10ProductOperationRecord{}, false, ErrStrideE10Invalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.records[record.Key]; ok {
		if current.Fingerprint != record.Fingerprint || current.PersonID != record.PersonID || current.ActionID != record.ActionID || current.IdempotencyKeyDigest != record.IdempotencyKeyDigest {
			return StrideE10ProductOperationRecord{}, true, ErrStrideE10Conflict
		}
		return cloneStrideE10OperationRecord(current), true, nil
	}
	for _, current := range s.records {
		if current.PersonID == record.PersonID && current.ActionID == record.ActionID {
			return StrideE10ProductOperationRecord{}, true, ErrStrideE10NotFound
		}
	}
	s.records[record.Key] = cloneStrideE10OperationRecord(record)
	if err := s.persistLocked(); err != nil {
		delete(s.records, record.Key)
		return StrideE10ProductOperationRecord{}, false, err
	}
	return cloneStrideE10OperationRecord(record), false, nil
}

func (s *strideE10ProductOperationStore) Commit(key, fingerprint string, at time.Time) error {
	if s == nil || key == "" || !isHexDigest(fingerprint) || at.IsZero() {
		return ErrStrideE10Invalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[key]
	if !ok || record.Fingerprint != fingerprint {
		return ErrStrideE10Conflict
	}
	if record.State == strideE10OperationCommitted || record.State == strideE10OperationCompleted {
		return nil
	}
	if record.State != strideE10OperationPrepared {
		return ErrStrideE10Conflict
	}
	record.State = strideE10OperationCommitted
	stamp := at.UTC()
	record.CommittedAt = &stamp
	s.records[key] = record
	return s.persistLocked()
}

func (s *strideE10ProductOperationStore) Complete(key, fingerprint string, response any, at time.Time) error {
	if s == nil || key == "" || !isHexDigest(fingerprint) || at.IsZero() {
		return ErrStrideE10Invalid
	}
	body, err := json.Marshal(response)
	if err != nil || len(body) > strideE10MaxBodyBytes {
		return ErrStrideE10Invalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[key]
	if !ok || record.Fingerprint != fingerprint || record.State == strideE10OperationPrepared {
		return ErrStrideE10Conflict
	}
	if record.State == strideE10OperationCompleted {
		if string(record.Response) != string(body) {
			return ErrStrideE10Conflict
		}
		return nil
	}
	record.State = strideE10OperationCompleted
	record.Response = append(json.RawMessage(nil), body...)
	stamp := at.UTC()
	record.CompletedAt = &stamp
	s.records[key] = record
	return s.persistLocked()
}

func (s *strideE10ProductOperationStore) Abort(key, fingerprint string) error {
	if s == nil || key == "" || !isHexDigest(fingerprint) {
		return ErrStrideE10Invalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[key]
	if !ok {
		return nil
	}
	if record.Fingerprint != fingerprint || record.State != strideE10OperationPrepared {
		return ErrStrideE10Conflict
	}
	delete(s.records, key)
	return s.persistLocked()
}

func (s *strideE10ProductOperationStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	payload, err := json.Marshal(s.records)
	if err != nil {
		return err
	}
	key, err := s.keys.CurrentStrideE10ProductOperationKey(context.Background())
	if err != nil || validateStrideE10ProductOperationMACKey(key) != nil {
		return ErrStrideE10Invalid
	}
	s.generation++
	envelope := strideE10ProductOperationEnvelope{SchemaVersion: 1, Generation: s.generation, KeyID: key.ID, KeyVersion: key.Version, Payload: payload}
	envelope.MAC = strideE10ProductOperationMAC(key, envelope.Generation, payload)
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, body, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}

func validateStrideE10ProductOperationMACKey(key StrideE10ProductOperationMACKey) error {
	if !strideIdentifier(key.ID) || key.Version < 1 || len(key.Secret) < 32 {
		return ErrStrideE10Invalid
	}
	return nil
}

func strideE10ProductOperationMAC(key StrideE10ProductOperationMACKey, generation uint64, payload []byte) string {
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write([]byte(fmt.Sprintf("stride-e10-product-operation\x00v1\x00%d\x00%s\x00%d\x00", generation, key.ID, key.Version)))
	_, _ = mac.Write(payload)
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func strideE10ProductOperationKey(personID, actionID, idempotencyKey string) string {
	return sha256Hex([]byte(personID + "\x00" + actionID + "\x00" + sha256Hex([]byte(idempotencyKey))))
}

func cloneStrideE10OperationRecord(record StrideE10ProductOperationRecord) StrideE10ProductOperationRecord {
	body, _ := json.Marshal(record)
	var clone StrideE10ProductOperationRecord
	_ = json.Unmarshal(body, &clone)
	return clone
}
