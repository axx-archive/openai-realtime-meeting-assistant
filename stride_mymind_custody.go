package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
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
	"unicode/utf8"
)

var (
	ErrMyMindCustodyInvalid  = errors.New("invalid private MyMind custody request")
	ErrMyMindCustodyDenied   = errors.New("private MyMind custody authority denied")
	ErrMyMindCustodyConflict = errors.New("private MyMind custody revision conflict")
	ErrMyMindCustodyTampered = errors.New("private MyMind custody state failed authentication")
	ErrMyMindCustodyNotFound = errors.New("private MyMind custody source not found")
)

const (
	myMindCustodyStateSchema = "stride.mymind.private-custody.v1"
	myMindCustodyMaxBody     = 64 * 1024
)

// MyMindCustodyKey is a per-person envelope key. Retired keys remain resolvable
// only for authenticated reads and resealing; DestroyPersonKeys must make every
// version permanently unavailable.
type MyMindCustodyKey struct {
	ID       string
	Version  int64
	PersonID string
	SourceID string
	Material []byte
}

func (k MyMindCustodyKey) valid(personID, sourceID string) bool {
	return strideIdentifier(k.ID) && k.Version > 0 && k.PersonID == personID && k.SourceID == sourceID && strideIdentifier(personID) && strideIdentifier(sourceID) && len(k.Material) == 32
}

type MyMindKeyDestructionReceipt struct {
	Schema               string    `json:"schema"`
	OperationID          string    `json:"operationId"`
	Scope                string    `json:"scope"`
	PersonID             string    `json:"personId"`
	SourceID             string    `json:"sourceId,omitempty"`
	KeyRefsDigest        string    `json:"keyRefsDigest"`
	EvidenceKeyID        string    `json:"evidenceKeyId"`
	EvidenceVersion      int64     `json:"evidenceVersion"`
	DestroyedAt          time.Time `json:"destroyedAt"`
	MAC                  string    `json:"mac"`
	VerificationContract string    `json:"verificationContract"`
	ReceiptDigest        string    `json:"receiptDigest"`
}

type MyMindCustodyKeyring interface {
	CurrentMyMindCustodyKey(context.Context, string, string) (MyMindCustodyKey, error)
	ResolveMyMindCustodyKey(context.Context, string, string, string, int64) (MyMindCustodyKey, error)
	// Destruction methods must durably bind operationID before destroying keys.
	// Replaying the same operationID and exact arguments must return the same
	// verified receipt; changed arguments for that operationID must fail closed.
	DestroySourceMyMindKeys(context.Context, string, string, string, []myMindCustodyKeyRef) (MyMindKeyDestructionReceipt, error)
	DestroyPersonMyMindKeys(context.Context, string, string, []myMindCustodyKeyRef) (MyMindKeyDestructionReceipt, error)
	VerifyMyMindKeyDestruction(context.Context, MyMindKeyDestructionReceipt) error
}

// MyMindPrivateAuthorityResolver must execute the callback while current
// session and membership authority is held. Merely returning a cached
// principal is intentionally not sufficient for a custody read or write.
type MyMindPrivateAuthorityResolver interface {
	WithCurrentMyMindPrivateAuthority(context.Context, MyMindPrivateAuthority, func(MyMindPrivateAuthority) error) error
}

type MyMindCustodyStateKey struct {
	ID       string
	Version  int64
	Material []byte
}

type MyMindCustodyStateKeyring interface {
	CurrentMyMindCustodyStateKey(context.Context) (MyMindCustodyStateKey, error)
	ResolveMyMindCustodyStateKey(context.Context, string, int64) (MyMindCustodyStateKey, error)
}

type MyMindCustodyHighWater struct {
	Generation    int64
	PayloadDigest string
}

type MyMindCustodyHighWaterStore interface {
	ReadMyMindCustodyHighWater(context.Context, string) (MyMindCustodyHighWater, error)
	AdvanceMyMindCustodyHighWater(context.Context, string, MyMindCustodyHighWater, MyMindCustodyHighWater) error
}

func (k MyMindCustodyStateKey) valid() bool {
	return strideIdentifier(k.ID) && k.Version > 0 && len(k.Material) == 32
}

type MyMindPrivateSource struct {
	PersonID        string    `json:"personId"`
	SourceID        string    `json:"sourceId"`
	Revision        int64     `json:"revision"`
	Kind            string    `json:"kind"`
	Body            string    `json:"body"`
	BodyDigest      string    `json:"bodyDigest"`
	ConsentRevision int64     `json:"consentRevision"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type MyMindPrivateExport struct {
	PersonID       string                `json:"personId"`
	ExportedAt     time.Time             `json:"exportedAt"`
	Sources        []MyMindPrivateSource `json:"sources"`
	ManifestDigest string                `json:"manifestDigest"`
}

type myMindCustodyEnvelope struct {
	Schema          string    `json:"schema"`
	PersonID        string    `json:"personId"`
	SourceID        string    `json:"sourceId"`
	Revision        int64     `json:"revision"`
	Kind            string    `json:"kind"`
	ConsentRevision int64     `json:"consentRevision"`
	BodyDigest      string    `json:"bodyDigest"`
	KeyID           string    `json:"keyId"`
	KeyVersion      int64     `json:"keyVersion"`
	Nonce           []byte    `json:"nonce"`
	Ciphertext      []byte    `json:"ciphertext"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type myMindCustodyOperation struct {
	Fingerprint string `json:"fingerprint"`
	Kind        string `json:"kind"`
	PersonID    string `json:"personId"`
	SourceID    string `json:"sourceId,omitempty"`
	Revision    int64  `json:"revision,omitempty"`
}

type myMindSourceTombstone struct {
	PersonID    string                      `json:"personId"`
	SourceID    string                      `json:"sourceId"`
	Revision    int64                       `json:"revision"`
	KeyRefs     []myMindCustodyKeyRef       `json:"keyRefs"`
	Receipt     MyMindKeyDestructionReceipt `json:"receipt"`
	ForgottenAt time.Time                   `json:"forgottenAt"`
}

type myMindSourceDeletionJournal struct {
	PersonID       string                       `json:"personId"`
	SourceID       string                       `json:"sourceId"`
	Revision       int64                        `json:"revision"`
	IdempotencyKey string                       `json:"idempotencyKey"`
	Fingerprint    string                       `json:"fingerprint"`
	OperationID    string                       `json:"operationId"`
	Phase          string                       `json:"phase"`
	KeyRefs        []myMindCustodyKeyRef        `json:"keyRefs"`
	Receipt        *MyMindKeyDestructionReceipt `json:"receipt,omitempty"`
	Authority      MyMindPrivateAuthority       `json:"authority"`
	StartedAt      time.Time                    `json:"startedAt"`
	UpdatedAt      time.Time                    `json:"updatedAt"`
}

type myMindDeletionJournal struct {
	PersonID              string                       `json:"personId"`
	IdempotencyKey        string                       `json:"idempotencyKey"`
	Fingerprint           string                       `json:"fingerprint"`
	OperationID           string                       `json:"operationId"`
	Phase                 string                       `json:"phase"`
	SourceManifestDigest  string                       `json:"sourceManifestDigest"`
	KeyRefs               []myMindCustodyKeyRef        `json:"keyRefs"`
	KeyDestructionReceipt *MyMindKeyDestructionReceipt `json:"keyDestructionReceipt,omitempty"`
	StartedAt             time.Time                    `json:"startedAt"`
	UpdatedAt             time.Time                    `json:"updatedAt"`
	Authority             MyMindPrivateAuthority       `json:"authority"`
}

type myMindCustodyKeyRef struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type myMindCustodyState struct {
	Schema          string                                 `json:"schema"`
	Generation      int64                                  `json:"generation"`
	Records         map[string]myMindCustodyEnvelope       `json:"records"`
	Operations      map[string]myMindCustodyOperation      `json:"operations"`
	Deletions       map[string]myMindDeletionJournal       `json:"deletions"`
	Tombstones      map[string]myMindSourceTombstone       `json:"tombstones"`
	SourceDeletions map[string]myMindSourceDeletionJournal `json:"sourceDeletions"`
}

type myMindCustodyStateEnvelope struct {
	Schema     string          `json:"schema"`
	KeyID      string          `json:"keyId"`
	KeyVersion int64           `json:"keyVersion"`
	Payload    json.RawMessage `json:"payload"`
	MAC        string          `json:"mac"`
}

type myMindCustodyPublicationJournal struct {
	Schema     string                 `json:"schema"`
	StoreID    string                 `json:"storeId"`
	Prior      MyMindCustodyHighWater `json:"prior"`
	Next       MyMindCustodyHighWater `json:"next"`
	StateBytes []byte                 `json:"stateBytes"`
	KeyID      string                 `json:"keyId"`
	KeyVersion int64                  `json:"keyVersion"`
	MAC        string                 `json:"mac"`
}

type FileMyMindCustody struct {
	mu        sync.Mutex
	path      string
	lockPath  string
	txnPath   string
	stateKeys MyMindCustodyStateKeyring
	highWater MyMindCustodyHighWaterStore
	keys      MyMindCustodyKeyring
	authority MyMindPrivateAuthorityResolver
}

func NewFileMyMindCustody(path string, stateKeys MyMindCustodyStateKeyring, highWater MyMindCustodyHighWaterStore, keys MyMindCustodyKeyring, authority MyMindPrivateAuthorityResolver) (*FileMyMindCustody, error) {
	if strings.TrimSpace(path) == "" || filepath.Clean(path) == "." || stateKeys == nil || highWater == nil || keys == nil || authority == nil {
		return nil, ErrMyMindCustodyInvalid
	}
	service := &FileMyMindCustody{path: filepath.Clean(path), lockPath: filepath.Clean(path) + ".lock", txnPath: filepath.Clean(path) + ".txn", stateKeys: stateKeys, highWater: highWater, keys: keys, authority: authority}
	if err := service.withLockedState(false, func(_ *myMindCustodyState) error { return nil }); err != nil {
		return nil, err
	}
	if err := service.ResumeSourceForgets(context.Background()); err != nil {
		return nil, err
	}
	if err := service.ResumeDeletions(context.Background()); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *FileMyMindCustody) resealStateKey() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var envelope myMindCustodyStateEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || ensureJSONEOF(decoder) != nil {
		return ErrMyMindCustodyTampered
	}
	current, err := s.stateKeys.CurrentMyMindCustodyStateKey(context.Background())
	if err != nil || !current.valid() {
		return ErrMyMindCustodyDenied
	}
	if envelope.KeyID == current.ID && envelope.KeyVersion == current.Version {
		return nil
	}
	resolved, err := s.stateKeys.ResolveMyMindCustodyStateKey(context.Background(), envelope.KeyID, envelope.KeyVersion)
	if err != nil || !resolved.valid() || resolved.ID != envelope.KeyID || resolved.Version != envelope.KeyVersion || !hmac.Equal([]byte(envelope.MAC), []byte(myMindStateMAC(resolved.Material, envelope.Payload))) {
		return ErrMyMindCustodyTampered
	}
	envelope.KeyID, envelope.KeyVersion = current.ID, current.Version
	envelope.MAC = myMindStateMAC(current.Material, envelope.Payload)
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return writeMyMindAtomicFile(s.path, encoded)
}

func (s *FileMyMindCustody) Put(ctx context.Context, requested MyMindPrivateAuthority, idempotencyKey, sourceID, kind, body string, expectedRevision int64) (MyMindPrivateSource, error) {
	if kind == "correction" {
		return MyMindPrivateSource{}, ErrMyMindCustodyInvalid
	}
	return s.put(ctx, requested, idempotencyKey, sourceID, kind, body, expectedRevision)
}

func (s *FileMyMindCustody) put(ctx context.Context, requested MyMindPrivateAuthority, idempotencyKey, sourceID, kind, body string, expectedRevision int64) (MyMindPrivateSource, error) {
	if !validMyMindCustodyMutation(idempotencyKey, sourceID, kind, body, expectedRevision) {
		return MyMindPrivateSource{}, ErrMyMindCustodyInvalid
	}
	var result MyMindPrivateSource
	err := s.withAuthority(ctx, requested, func(current MyMindPrivateAuthority) error {
		fingerprint := myMindCustodyDigest("put", current.PersonID, sourceID, kind, fmt.Sprint(expectedRevision))
		return s.withLockedState(true, func(state *myMindCustodyState) error {
			if err := rejectMyMindDeletion(state, current.PersonID); err != nil {
				return err
			}
			if _, erased := state.Tombstones[myMindRecordKey(current.PersonID, sourceID)]; erased {
				return ErrMyMindCustodyDenied
			}
			operationKey := myMindOperationKey(current.PersonID, idempotencyKey)
			if prior, ok := state.Operations[operationKey]; ok {
				if prior.Fingerprint != fingerprint {
					return ErrMyMindCustodyConflict
				}
				envelope, ok := state.Records[myMindRecordKey(current.PersonID, prior.SourceID)]
				if !ok || envelope.Revision != prior.Revision {
					return ErrMyMindCustodyConflict
				}
				var err error
				result, err = s.open(ctx, envelope, current.PersonID)
				if err == nil && result.Body != body {
					return ErrMyMindCustodyConflict
				}
				return err
			}
			key := myMindRecordKey(current.PersonID, sourceID)
			prior, exists := state.Records[key]
			if (!exists && expectedRevision != 0) || (exists && prior.Revision != expectedRevision) {
				return ErrMyMindCustodyConflict
			}
			revision := expectedRevision + 1
			consentRevision := int64(1)
			if exists {
				consentRevision = prior.ConsentRevision + 1
			}
			envelope, err := s.seal(ctx, current.PersonID, sourceID, revision, kind, consentRevision, body, current.At)
			if err != nil {
				return err
			}
			state.Records[key] = envelope
			state.Operations[operationKey] = myMindCustodyOperation{Fingerprint: fingerprint, Kind: "put", PersonID: current.PersonID, SourceID: sourceID, Revision: revision}
			result = privateSourceFromEnvelope(envelope, body)
			return nil
		})
	})
	return result, err
}

func (s *FileMyMindCustody) Correct(ctx context.Context, requested MyMindPrivateAuthority, idempotencyKey, sourceID, body string, expectedRevision int64) (MyMindPrivateSource, error) {
	if expectedRevision < 1 {
		return MyMindPrivateSource{}, ErrMyMindCustodyInvalid
	}
	return s.put(ctx, requested, idempotencyKey, sourceID, "correction", body, expectedRevision)
}

func (s *FileMyMindCustody) Inspect(ctx context.Context, requested MyMindPrivateAuthority) ([]MyMindPrivateSource, error) {
	var result []MyMindPrivateSource
	err := s.withAuthority(ctx, requested, func(current MyMindPrivateAuthority) error {
		return s.withLockedState(false, func(state *myMindCustodyState) error {
			if err := rejectMyMindDeletion(state, current.PersonID); err != nil {
				return err
			}
			var err error
			result, err = s.inspectState(ctx, state, current.PersonID)
			return err
		})
	})
	return result, err
}

func (s *FileMyMindCustody) Forget(ctx context.Context, requested MyMindPrivateAuthority, idempotencyKey, sourceID string, expectedRevision int64) error {
	if !strideIdentifier(idempotencyKey) || !strideIdentifier(sourceID) || expectedRevision < 1 {
		return ErrMyMindCustodyInvalid
	}
	return s.withAuthority(ctx, requested, func(current MyMindPrivateAuthority) error {
		fingerprint := myMindCustodyDigest("forget", current.PersonID, sourceID, fmt.Sprint(expectedRevision))
		completed := false
		if err := s.withLockedState(true, func(state *myMindCustodyState) error {
			if err := rejectMyMindDeletion(state, current.PersonID); err != nil {
				return err
			}
			opKey := myMindOperationKey(current.PersonID, idempotencyKey)
			if prior, ok := state.Operations[opKey]; ok {
				if prior.Fingerprint != fingerprint {
					return ErrMyMindCustodyConflict
				}
				completed = true
				return nil
			}
			key := myMindRecordKey(current.PersonID, sourceID)
			prior, ok := state.Records[key]
			if !ok {
				return ErrMyMindCustodyNotFound
			}
			if prior.Revision != expectedRevision {
				return ErrMyMindCustodyConflict
			}
			refs := []myMindCustodyKeyRef{{ID: prior.KeyID, Version: prior.KeyVersion}}
			if existing, ok := state.SourceDeletions[key]; ok {
				if existing.IdempotencyKey != idempotencyKey || existing.Fingerprint != fingerprint {
					return ErrMyMindCustodyConflict
				}
				return nil
			}
			operationID := myMindDestructionOperationID("source", current.PersonID, sourceID, idempotencyKey, fingerprint, refs)
			state.SourceDeletions[key] = myMindSourceDeletionJournal{PersonID: current.PersonID, SourceID: sourceID, Revision: expectedRevision, IdempotencyKey: idempotencyKey, Fingerprint: fingerprint, OperationID: operationID, Phase: "prepared", KeyRefs: refs, Authority: current, StartedAt: current.At, UpdatedAt: current.At}
			return nil
		}); err != nil {
			return err
		}
		if completed {
			return nil
		}
		return s.resumeSourceForgetCurrent(ctx, current, sourceID)
	})
}

func (s *FileMyMindCustody) resumeSourceForgetCurrent(ctx context.Context, current MyMindPrivateAuthority, sourceID string) error {
	key := myMindRecordKey(current.PersonID, sourceID)
	var journal myMindSourceDeletionJournal
	if err := s.withLockedState(false, func(state *myMindCustodyState) error {
		var ok bool
		journal, ok = state.SourceDeletions[key]
		if !ok {
			return ErrMyMindCustodyNotFound
		}
		return nil
	}); err != nil {
		return err
	}
	if journal.Phase == "prepared" {
		receipt, err := s.keys.DestroySourceMyMindKeys(ctx, journal.OperationID, current.PersonID, sourceID, journal.KeyRefs)
		if err != nil || s.keys.VerifyMyMindKeyDestruction(ctx, receipt) != nil || !validMyMindDestructionReceipt(receipt, journal.OperationID, "source", current.PersonID, sourceID, journal.KeyRefs) {
			return ErrMyMindCustodyDenied
		}
		if err := s.withLockedState(true, func(state *myMindCustodyState) error {
			j := state.SourceDeletions[key]
			if j.Phase != "prepared" {
				return ErrMyMindCustodyConflict
			}
			j.Phase = "keys_destroyed"
			j.Receipt = &receipt
			j.UpdatedAt = current.At
			state.SourceDeletions[key] = j
			return nil
		}); err != nil {
			return err
		}
		journal.Phase = "keys_destroyed"
	}
	if journal.Phase == "keys_destroyed" {
		return s.withLockedState(true, func(state *myMindCustodyState) error {
			j := state.SourceDeletions[key]
			if j.Phase != "keys_destroyed" || j.Receipt == nil || s.keys.VerifyMyMindKeyDestruction(ctx, *j.Receipt) != nil || !validMyMindDestructionReceipt(*j.Receipt, j.OperationID, "source", current.PersonID, sourceID, j.KeyRefs) {
				return ErrMyMindCustodyTampered
			}
			delete(state.Records, key)
			state.Tombstones[key] = myMindSourceTombstone{PersonID: current.PersonID, SourceID: sourceID, Revision: j.Revision, KeyRefs: j.KeyRefs, Receipt: *j.Receipt, ForgottenAt: current.At}
			state.Operations[myMindOperationKey(current.PersonID, j.IdempotencyKey)] = myMindCustodyOperation{Fingerprint: j.Fingerprint, Kind: "forget", PersonID: current.PersonID, SourceID: sourceID, Revision: j.Revision}
			delete(state.SourceDeletions, key)
			return nil
		})
	}
	return nil
}

func (s *FileMyMindCustody) ResumeSourceForgets(ctx context.Context) error {
	var journals []myMindSourceDeletionJournal
	if err := s.withLockedState(false, func(state *myMindCustodyState) error {
		for _, j := range state.SourceDeletions {
			journals = append(journals, j)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, j := range journals {
		if err := s.withAuthority(ctx, j.Authority, func(current MyMindPrivateAuthority) error {
			return s.resumeSourceForgetCurrent(ctx, current, j.SourceID)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileMyMindCustody) Export(ctx context.Context, requested MyMindPrivateAuthority) (MyMindPrivateExport, error) {
	var result MyMindPrivateExport
	err := s.withAuthority(ctx, requested, func(current MyMindPrivateAuthority) error {
		return s.withLockedState(false, func(state *myMindCustodyState) error {
			if err := rejectMyMindDeletion(state, current.PersonID); err != nil {
				return err
			}
			sources, err := s.inspectState(ctx, state, current.PersonID)
			if err != nil {
				return err
			}
			result = MyMindPrivateExport{PersonID: current.PersonID, ExportedAt: current.At, Sources: sources, ManifestDigest: myMindCustodyDigest("export", current.PersonID, myMindPersonManifest(state, current.PersonID))}
			return nil
		})
	})
	return result, err
}

func (s *FileMyMindCustody) inspectState(ctx context.Context, state *myMindCustodyState, personID string) ([]MyMindPrivateSource, error) {
	result := []MyMindPrivateSource{}
	for _, envelope := range state.Records {
		if envelope.PersonID != personID {
			continue
		}
		item, err := s.open(ctx, envelope, personID)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SourceID < result[j].SourceID })
	return result, nil
}

func (s *FileMyMindCustody) Rotate(ctx context.Context, requested MyMindPrivateAuthority, idempotencyKey string) error {
	if !strideIdentifier(idempotencyKey) {
		return ErrMyMindCustodyInvalid
	}
	return s.withAuthority(ctx, requested, func(current MyMindPrivateAuthority) error {
		return s.withLockedState(true, func(state *myMindCustodyState) error {
			if err := rejectMyMindDeletion(state, current.PersonID); err != nil {
				return err
			}
			var targetRefs []string
			for _, envelope := range state.Records {
				if envelope.PersonID != current.PersonID {
					continue
				}
				key, err := s.keys.CurrentMyMindCustodyKey(ctx, current.PersonID, envelope.SourceID)
				if err != nil || !key.valid(current.PersonID, envelope.SourceID) {
					return ErrMyMindCustodyDenied
				}
				targetRefs = append(targetRefs, fmt.Sprintf("%s/%s/%d", envelope.SourceID, key.ID, key.Version))
			}
			sort.Strings(targetRefs)
			fingerprint := myMindCustodyDigest(append([]string{"rotate", current.PersonID}, targetRefs...)...)
			opKey := myMindOperationKey(current.PersonID, idempotencyKey)
			if prior, ok := state.Operations[opKey]; ok {
				if prior.Fingerprint != fingerprint {
					return ErrMyMindCustodyConflict
				}
				return nil
			}
			for key, envelope := range state.Records {
				if envelope.PersonID != current.PersonID {
					continue
				}
				item, err := s.open(ctx, envelope, current.PersonID)
				if err != nil {
					return err
				}
				newKey, err := s.keys.CurrentMyMindCustodyKey(ctx, current.PersonID, envelope.SourceID)
				if err != nil || !newKey.valid(current.PersonID, envelope.SourceID) {
					return ErrMyMindCustodyDenied
				}
				resealed, err := sealMyMindEnvelope(newKey, envelope.PersonID, envelope.SourceID, envelope.Revision, envelope.Kind, envelope.ConsentRevision, item.Body, envelope.UpdatedAt)
				if err != nil {
					return err
				}
				state.Records[key] = resealed
			}
			state.Operations[opKey] = myMindCustodyOperation{Fingerprint: fingerprint, Kind: "rotate", PersonID: current.PersonID}
			return nil
		})
	})
}

func (s *FileMyMindCustody) DeletePerson(ctx context.Context, requested MyMindPrivateAuthority, idempotencyKey string) error {
	if !strideIdentifier(idempotencyKey) {
		return ErrMyMindCustodyInvalid
	}
	if err := s.withAuthority(ctx, requested, func(current MyMindPrivateAuthority) error {
		return s.prepareDeletion(current, idempotencyKey)
	}); err != nil {
		return err
	}
	return s.resumePersonDeletion(ctx, requested.PersonID)
}

func (s *FileMyMindCustody) ResumeDeletions(ctx context.Context) error {
	var people []string
	if err := s.withLockedState(false, func(state *myMindCustodyState) error {
		for personID, journal := range state.Deletions {
			if journal.Phase != "completed" {
				people = append(people, personID)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(people)
	for _, personID := range people {
		if err := s.resumePersonDeletion(ctx, personID); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileMyMindCustody) prepareDeletion(current MyMindPrivateAuthority, idempotencyKey string) error {
	fingerprint := myMindCustodyDigest("delete", current.PersonID, idempotencyKey)
	return s.withLockedState(true, func(state *myMindCustodyState) error {
		if prior, ok := state.Deletions[current.PersonID]; ok {
			if prior.IdempotencyKey != idempotencyKey || prior.Fingerprint != fingerprint {
				return ErrMyMindCustodyConflict
			}
			return nil
		}
		manifest := myMindPersonManifest(state, current.PersonID)
		refs := myMindPersonKeyRefs(state, current.PersonID)
		operationID := myMindDestructionOperationID("person", current.PersonID, "", idempotencyKey, fingerprint, refs)
		state.Deletions[current.PersonID] = myMindDeletionJournal{PersonID: current.PersonID, IdempotencyKey: idempotencyKey, Fingerprint: fingerprint, OperationID: operationID, Phase: "prepared", SourceManifestDigest: manifest, KeyRefs: refs, StartedAt: current.At, UpdatedAt: current.At, Authority: current}
		return nil
	})
}

func (s *FileMyMindCustody) resumePersonDeletion(ctx context.Context, personID string) error {
	var requested MyMindPrivateAuthority
	if err := s.withLockedState(false, func(state *myMindCustodyState) error {
		journal, ok := state.Deletions[personID]
		if !ok {
			return ErrMyMindCustodyNotFound
		}
		requested = journal.Authority
		return nil
	}); err != nil {
		return err
	}
	return s.withAuthority(ctx, requested, func(current MyMindPrivateAuthority) error {
		if current.PersonID != personID {
			return ErrMyMindCustodyDenied
		}
		return s.resumePersonDeletionCurrent(ctx, personID, current.At)
	})
}

func (s *FileMyMindCustody) resumePersonDeletionCurrent(ctx context.Context, personID string, currentAt time.Time) error {
	var phase string
	if err := s.withLockedState(true, func(state *myMindCustodyState) error {
		journal, ok := state.Deletions[personID]
		if !ok {
			return ErrMyMindCustodyNotFound
		}
		phase = journal.Phase
		if phase == "prepared" {
			if myMindPersonManifest(state, personID) != journal.SourceManifestDigest {
				return ErrMyMindCustodyTampered
			}
			for key, envelope := range state.Records {
				if envelope.PersonID == personID {
					delete(state.Records, key)
				}
			}
			journal.Phase, journal.UpdatedAt = "records_removed", currentAt
			state.Deletions[personID] = journal
			phase = journal.Phase
		}
		return nil
	}); err != nil {
		return err
	}
	if phase == "records_removed" {
		var refs []myMindCustodyKeyRef
		if err := s.withLockedState(false, func(state *myMindCustodyState) error {
			refs = append(refs, state.Deletions[personID].KeyRefs...)
			return nil
		}); err != nil {
			return err
		}
		var operationID string
		if err := s.withLockedState(false, func(state *myMindCustodyState) error {
			operationID = state.Deletions[personID].OperationID
			return nil
		}); err != nil {
			return err
		}
		receipt, err := s.keys.DestroyPersonMyMindKeys(ctx, operationID, personID, refs)
		if err != nil || s.keys.VerifyMyMindKeyDestruction(ctx, receipt) != nil || !validMyMindDestructionReceipt(receipt, operationID, "person", personID, "", refs) {
			return ErrMyMindCustodyDenied
		}
		if err := s.withLockedState(true, func(state *myMindCustodyState) error {
			journal := state.Deletions[personID]
			if journal.Phase != "records_removed" {
				return ErrMyMindCustodyConflict
			}
			journal.Phase, journal.KeyDestructionReceipt, journal.UpdatedAt = "keys_destroyed", &receipt, currentAt
			state.Deletions[personID] = journal
			return nil
		}); err != nil {
			return err
		}
		phase = "keys_destroyed"
	}
	if phase == "keys_destroyed" {
		return s.withLockedState(true, func(state *myMindCustodyState) error {
			journal := state.Deletions[personID]
			if journal.Phase != "keys_destroyed" || journal.KeyDestructionReceipt == nil || s.keys.VerifyMyMindKeyDestruction(ctx, *journal.KeyDestructionReceipt) != nil || !validMyMindDestructionReceipt(*journal.KeyDestructionReceipt, journal.OperationID, "person", personID, "", journal.KeyRefs) {
				return ErrMyMindCustodyTampered
			}
			journal.Phase, journal.UpdatedAt = "completed", currentAt
			state.Deletions[personID] = journal
			state.Operations[myMindOperationKey(personID, journal.IdempotencyKey)] = myMindCustodyOperation{Fingerprint: journal.Fingerprint, Kind: "delete", PersonID: personID}
			return nil
		})
	}
	return nil
}

func (s *FileMyMindCustody) VerifyRestore(ctx context.Context) error {
	return s.withLockedState(false, func(state *myMindCustodyState) error {
		for _, journal := range state.Deletions {
			expectedOperationID := myMindDestructionOperationID("person", journal.PersonID, "", journal.IdempotencyKey, journal.Fingerprint, journal.KeyRefs)
			if !oneOf(journal.Phase, "prepared", "records_removed", "keys_destroyed", "completed") || !isHexDigest(journal.SourceManifestDigest) || journal.OperationID != expectedOperationID || !validMyMindPrivateAuthority(journal.Authority) || journal.Authority.PersonID != journal.PersonID {
				return ErrMyMindCustodyTampered
			}
			for _, ref := range journal.KeyRefs {
				if !strideIdentifier(ref.ID) || ref.Version < 1 {
					return ErrMyMindCustodyTampered
				}
				if oneOf(journal.Phase, "keys_destroyed", "completed") {
					if journal.KeyDestructionReceipt == nil || s.keys.VerifyMyMindKeyDestruction(ctx, *journal.KeyDestructionReceipt) != nil || !validMyMindDestructionReceipt(*journal.KeyDestructionReceipt, journal.OperationID, "person", journal.PersonID, "", journal.KeyRefs) {
						return ErrMyMindCustodyTampered
					}
				}
			}
			if oneOf(journal.Phase, "records_removed", "keys_destroyed", "completed") {
				for _, record := range state.Records {
					if record.PersonID == journal.PersonID {
						return ErrMyMindCustodyTampered
					}
				}
			}
		}
		for key, tombstone := range state.Tombstones {
			if key != myMindRecordKey(tombstone.PersonID, tombstone.SourceID) || tombstone.Revision < 1 || tombstone.ForgottenAt.IsZero() || s.keys.VerifyMyMindKeyDestruction(ctx, tombstone.Receipt) != nil || !validMyMindDestructionReceipt(tombstone.Receipt, tombstone.Receipt.OperationID, "source", tombstone.PersonID, tombstone.SourceID, tombstone.KeyRefs) {
				return ErrMyMindCustodyTampered
			}
			if _, exists := state.Records[key]; exists {
				return ErrMyMindCustodyTampered
			}
			for _, ref := range tombstone.KeyRefs {
				if _, err := s.keys.ResolveMyMindCustodyKey(ctx, tombstone.PersonID, tombstone.SourceID, ref.ID, ref.Version); err == nil {
					return ErrMyMindCustodyTampered
				}
			}
		}
		for key, journal := range state.SourceDeletions {
			expectedOperationID := myMindDestructionOperationID("source", journal.PersonID, journal.SourceID, journal.IdempotencyKey, journal.Fingerprint, journal.KeyRefs)
			if key != myMindRecordKey(journal.PersonID, journal.SourceID) || !oneOf(journal.Phase, "prepared", "keys_destroyed") || journal.Revision < 1 || journal.OperationID != expectedOperationID || !validMyMindPrivateAuthority(journal.Authority) || journal.Authority.PersonID != journal.PersonID {
				return ErrMyMindCustodyTampered
			}
			if journal.Phase == "keys_destroyed" {
				if journal.Receipt == nil || s.keys.VerifyMyMindKeyDestruction(ctx, *journal.Receipt) != nil || !validMyMindDestructionReceipt(*journal.Receipt, journal.OperationID, "source", journal.PersonID, journal.SourceID, journal.KeyRefs) {
					return ErrMyMindCustodyTampered
				}
			}
		}
		for _, envelope := range state.Records {
			if _, err := s.open(ctx, envelope, envelope.PersonID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *FileMyMindCustody) withAuthority(ctx context.Context, requested MyMindPrivateAuthority, fn func(MyMindPrivateAuthority) error) error {
	if s == nil || s.authority == nil || !validMyMindPrivateAuthority(requested) {
		return ErrMyMindCustodyDenied
	}
	return s.authority.WithCurrentMyMindPrivateAuthority(ctx, requested, func(current MyMindPrivateAuthority) error {
		if !validMyMindPrivateAuthority(current) || current.PersonID != requested.PersonID || current.OrganizationID != requested.OrganizationID || current.MembershipID != requested.MembershipID || current.MembershipRevision != requested.MembershipRevision || current.SessionSubjectDigest != requested.SessionSubjectDigest || current.SessionRevision != requested.SessionRevision {
			return ErrMyMindCustodyDenied
		}
		return fn(current)
	})
}

func (s *FileMyMindCustody) seal(ctx context.Context, personID, sourceID string, revision int64, kind string, consentRevision int64, body string, at time.Time) (myMindCustodyEnvelope, error) {
	key, err := s.keys.CurrentMyMindCustodyKey(ctx, personID, sourceID)
	if err != nil || !key.valid(personID, sourceID) {
		return myMindCustodyEnvelope{}, ErrMyMindCustodyDenied
	}
	return sealMyMindEnvelope(key, personID, sourceID, revision, kind, consentRevision, body, at)
}

func sealMyMindEnvelope(key MyMindCustodyKey, personID, sourceID string, revision int64, kind string, consentRevision int64, body string, at time.Time) (myMindCustodyEnvelope, error) {
	if !key.valid(personID, sourceID) || !validMyMindPrivateBody(body) || !strideIdentifier(sourceID) || revision < 1 || consentRevision < 1 || !oneOf(kind, "preference", "reflection", "correction") || at.IsZero() {
		return myMindCustodyEnvelope{}, ErrMyMindCustodyInvalid
	}
	envelope := myMindCustodyEnvelope{Schema: myMindCustodyStateSchema, PersonID: personID, SourceID: sourceID, Revision: revision, Kind: kind, ConsentRevision: consentRevision, BodyDigest: myMindPrivateBodyDigest(key.Material, body), KeyID: key.ID, KeyVersion: key.Version, UpdatedAt: at.UTC()}
	block, err := aes.NewCipher(key.Material)
	if err != nil {
		return myMindCustodyEnvelope{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return myMindCustodyEnvelope{}, err
	}
	envelope.Nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, envelope.Nonce); err != nil {
		return myMindCustodyEnvelope{}, err
	}
	envelope.Ciphertext = gcm.Seal(nil, envelope.Nonce, []byte(body), myMindEnvelopeAAD(envelope))
	return envelope, nil
}

func (s *FileMyMindCustody) open(ctx context.Context, envelope myMindCustodyEnvelope, expectedPersonID string) (MyMindPrivateSource, error) {
	if envelope.Schema != myMindCustodyStateSchema || envelope.PersonID != expectedPersonID || !strideIdentifier(envelope.SourceID) || envelope.Revision < 1 || envelope.ConsentRevision < 1 || !isHexDigest(envelope.BodyDigest) || !oneOf(envelope.Kind, "preference", "reflection", "correction") || envelope.UpdatedAt.IsZero() {
		return MyMindPrivateSource{}, ErrMyMindCustodyTampered
	}
	key, err := s.keys.ResolveMyMindCustodyKey(ctx, expectedPersonID, envelope.SourceID, envelope.KeyID, envelope.KeyVersion)
	if err != nil || !key.valid(expectedPersonID, envelope.SourceID) || key.ID != envelope.KeyID || key.Version != envelope.KeyVersion {
		return MyMindPrivateSource{}, ErrMyMindCustodyDenied
	}
	block, err := aes.NewCipher(key.Material)
	if err != nil {
		return MyMindPrivateSource{}, ErrMyMindCustodyDenied
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(envelope.Nonce) != gcm.NonceSize() {
		return MyMindPrivateSource{}, ErrMyMindCustodyTampered
	}
	body, err := gcm.Open(nil, envelope.Nonce, envelope.Ciphertext, myMindEnvelopeAAD(envelope))
	if err != nil || !validMyMindPrivateBody(string(body)) || myMindPrivateBodyDigest(key.Material, string(body)) != envelope.BodyDigest {
		return MyMindPrivateSource{}, ErrMyMindCustodyTampered
	}
	return privateSourceFromEnvelope(envelope, string(body)), nil
}

func (s *FileMyMindCustody) withLockedState(write bool, fn func(*myMindCustodyState) error) error {
	if s == nil {
		return ErrMyMindCustodyInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	if err := rejectUnsafeMyMindCustodyPath(s.path); err != nil {
		return err
	}
	if err := rejectUnsafeMyMindCustodyPath(s.lockPath); err != nil {
		return err
	}
	if err := rejectUnsafeMyMindCustodyPath(s.txnPath); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := s.recoverPublication(context.Background()); err != nil {
		return err
	}
	state, err := s.loadState()
	if err != nil {
		return err
	}
	before := state.Generation
	if err := fn(&state); err != nil {
		return err
	}
	if write {
		state.Generation = before + 1
		return s.writeState(state)
	}
	return s.resealStateKey()
}

func (s *FileMyMindCustody) loadState() (myMindCustodyState, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		high, highErr := s.highWater.ReadMyMindCustodyHighWater(context.Background(), s.path)
		if highErr != nil || high.Generation != 0 || high.PayloadDigest != "" {
			return myMindCustodyState{}, ErrMyMindCustodyTampered
		}
		return newMyMindCustodyState(), nil
	}
	if err != nil {
		return myMindCustodyState{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope myMindCustodyStateEnvelope
	if decoder.Decode(&envelope) != nil || ensureJSONEOF(decoder) != nil || envelope.Schema != myMindCustodyStateSchema {
		return myMindCustodyState{}, ErrMyMindCustodyTampered
	}
	stateKey, keyErr := s.stateKeys.ResolveMyMindCustodyStateKey(context.Background(), envelope.KeyID, envelope.KeyVersion)
	if keyErr != nil || !stateKey.valid() || stateKey.ID != envelope.KeyID || stateKey.Version != envelope.KeyVersion || !hmac.Equal([]byte(envelope.MAC), []byte(myMindStateMAC(stateKey.Material, envelope.Payload))) {
		return myMindCustodyState{}, ErrMyMindCustodyTampered
	}
	decoder = json.NewDecoder(bytes.NewReader(envelope.Payload))
	decoder.DisallowUnknownFields()
	var state myMindCustodyState
	if decoder.Decode(&state) != nil || ensureJSONEOF(decoder) != nil || state.Schema != myMindCustodyStateSchema || state.Generation < 0 || state.Records == nil || state.Operations == nil || state.Deletions == nil || state.Tombstones == nil || state.SourceDeletions == nil {
		return myMindCustodyState{}, ErrMyMindCustodyTampered
	}
	payloadDigest := myMindBytesDigest(envelope.Payload)
	high, highErr := s.highWater.ReadMyMindCustodyHighWater(context.Background(), s.path)
	if highErr != nil || high.Generation != state.Generation || high.PayloadDigest != payloadDigest {
		return myMindCustodyState{}, ErrMyMindCustodyTampered
	}
	return state, nil
}

func (s *FileMyMindCustody) writeState(state myMindCustodyState) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	stateKey, err := s.stateKeys.CurrentMyMindCustodyStateKey(context.Background())
	if err != nil || !stateKey.valid() {
		return ErrMyMindCustodyDenied
	}
	envelope := myMindCustodyStateEnvelope{Schema: myMindCustodyStateSchema, KeyID: stateKey.ID, KeyVersion: stateKey.Version, Payload: payload, MAC: myMindStateMAC(stateKey.Material, payload)}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	prior, err := s.highWater.ReadMyMindCustodyHighWater(context.Background(), s.path)
	if err != nil || prior.Generation != state.Generation-1 {
		return ErrMyMindCustodyTampered
	}
	next := MyMindCustodyHighWater{Generation: state.Generation, PayloadDigest: myMindBytesDigest(payload)}
	journal := myMindCustodyPublicationJournal{Schema: "stride.mymind.private-custody-publication.v1", StoreID: s.path, Prior: prior, Next: next, StateBytes: raw, KeyID: stateKey.ID, KeyVersion: stateKey.Version}
	journal.MAC = myMindPublicationMAC(stateKey.Material, journal)
	journalBytes, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	if err := writeMyMindAtomicFile(s.txnPath, journalBytes); err != nil {
		return err
	}
	if err := writeMyMindAtomicFile(s.path, raw); err != nil {
		return err
	}
	if err := s.highWater.AdvanceMyMindCustodyHighWater(context.Background(), s.path, prior, next); err != nil {
		return ErrMyMindCustodyTampered
	}
	return removeMyMindPublicationJournal(s.txnPath)
}

func (s *FileMyMindCustody) recoverPublication(ctx context.Context) error {
	raw, err := os.ReadFile(s.txnPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var journal myMindCustodyPublicationJournal
	if decoder.Decode(&journal) != nil || ensureJSONEOF(decoder) != nil || journal.Schema != "stride.mymind.private-custody-publication.v1" || journal.StoreID != s.path || journal.Next.Generation != journal.Prior.Generation+1 || !strideE10W7Digest(journal.Next.PayloadDigest) {
		return ErrMyMindCustodyTampered
	}
	key, err := s.stateKeys.ResolveMyMindCustodyStateKey(ctx, journal.KeyID, journal.KeyVersion)
	if err != nil || !key.valid() || key.ID != journal.KeyID || key.Version != journal.KeyVersion || !hmac.Equal([]byte(journal.MAC), []byte(myMindPublicationMAC(key.Material, journal))) {
		return ErrMyMindCustodyTampered
	}
	var envelope myMindCustodyStateEnvelope
	decoder = json.NewDecoder(bytes.NewReader(journal.StateBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || ensureJSONEOF(decoder) != nil || myMindBytesDigest(envelope.Payload) != journal.Next.PayloadDigest {
		return ErrMyMindCustodyTampered
	}
	high, err := s.highWater.ReadMyMindCustodyHighWater(ctx, s.path)
	if err != nil {
		return err
	}
	if high == journal.Prior {
		if err := writeMyMindAtomicFile(s.path, journal.StateBytes); err != nil {
			return err
		}
		if err := s.highWater.AdvanceMyMindCustodyHighWater(ctx, s.path, journal.Prior, journal.Next); err != nil {
			return ErrMyMindCustodyTampered
		}
	} else if high != journal.Next {
		return ErrMyMindCustodyTampered
	}
	current, err := os.ReadFile(s.path)
	if err != nil || !bytes.Equal(current, journal.StateBytes) {
		if err := writeMyMindAtomicFile(s.path, journal.StateBytes); err != nil {
			return err
		}
	}
	return removeMyMindPublicationJournal(s.txnPath)
}

func removeMyMindPublicationJournal(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func writeMyMindAtomicFile(path string, raw []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mymind-custody-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(raw)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return err
	}
	return nil
}

func newMyMindCustodyState() myMindCustodyState {
	return myMindCustodyState{Schema: myMindCustodyStateSchema, Records: map[string]myMindCustodyEnvelope{}, Operations: map[string]myMindCustodyOperation{}, Deletions: map[string]myMindDeletionJournal{}, Tombstones: map[string]myMindSourceTombstone{}, SourceDeletions: map[string]myMindSourceDeletionJournal{}}
}
func rejectUnsafeMyMindCustodyPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ErrMyMindCustodyInvalid
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink != 1 {
		return ErrMyMindCustodyInvalid
	}
	return nil
}
func myMindRecordKey(personID, sourceID string) string       { return personID + "\x00" + sourceID }
func myMindOperationKey(personID, idempotency string) string { return personID + "\x00" + idempotency }
func myMindCustodyDigest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte{0})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func myMindBytesDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
func myMindCustodyFingerprint(key []byte, parts ...string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("stride.mymind.private-operation.v1"))
	for _, part := range parts {
		h.Write([]byte{0})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func myMindPrivateBodyDigest(key []byte, body string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("stride.mymind.private-body.v1"))
	h.Write([]byte{0})
	h.Write([]byte(body))
	return hex.EncodeToString(h.Sum(nil))
}
func myMindKeyRefsDigest(refs []myMindCustodyKeyRef) string {
	copyRefs := append([]myMindCustodyKeyRef(nil), refs...)
	sort.Slice(copyRefs, func(i, j int) bool {
		if copyRefs[i].ID != copyRefs[j].ID {
			return copyRefs[i].ID < copyRefs[j].ID
		}
		return copyRefs[i].Version < copyRefs[j].Version
	})
	parts := make([]string, 0, len(copyRefs))
	for _, ref := range copyRefs {
		parts = append(parts, fmt.Sprintf("%s/%d", ref.ID, ref.Version))
	}
	return myMindCustodyDigest(parts...)
}
func myMindDestructionOperationID(scope, personID, sourceID, idempotencyKey, fingerprint string, refs []myMindCustodyKeyRef) string {
	return "mymind_destroy_" + myMindCustodyDigest("stride.mymind.key-destruction-operation.v1", scope, personID, sourceID, idempotencyKey, fingerprint, myMindKeyRefsDigest(refs))
}
func myMindDestructionMAC(key []byte, receipt MyMindKeyDestructionReceipt) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(strings.Join([]string{receipt.Schema, receipt.OperationID, receipt.Scope, receipt.PersonID, receipt.SourceID, receipt.KeyRefsDigest, receipt.EvidenceKeyID, fmt.Sprint(receipt.EvidenceVersion), receipt.DestroyedAt.UTC().Format(time.RFC3339Nano), receipt.VerificationContract}, "\x00")))
	return hex.EncodeToString(h.Sum(nil))
}
func validMyMindDestructionReceipt(receipt MyMindKeyDestructionReceipt, operationID, scope, personID, sourceID string, refs []myMindCustodyKeyRef) bool {
	return receipt.Schema == "stride.mymind.key-destruction.v1" && receipt.OperationID == operationID && strideIdentifier(receipt.OperationID) && receipt.Scope == scope && receipt.PersonID == personID && receipt.SourceID == sourceID && receipt.KeyRefsDigest == myMindKeyRefsDigest(refs) && strideIdentifier(receipt.EvidenceKeyID) && receipt.EvidenceVersion > 0 && !receipt.DestroyedAt.IsZero() && isHexDigest(receipt.MAC) && receipt.VerificationContract == "managed_keyring_v1" && receipt.ReceiptDigest == myMindDestructionReceiptDigest(receipt)
}
func myMindDestructionReceiptDigest(receipt MyMindKeyDestructionReceipt) string {
	receipt.ReceiptDigest = ""
	body, _ := json.Marshal(receipt)
	return myMindBytesDigest(body)
}
func myMindStateMAC(key, payload []byte) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(myMindCustodyStateSchema))
	h.Write([]byte{0})
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}
func myMindPublicationMAC(key []byte, journal myMindCustodyPublicationJournal) string {
	journal.MAC = ""
	body, _ := json.Marshal(journal)
	h := hmac.New(sha256.New, key)
	h.Write([]byte("stride.mymind.private-custody-publication.v1\x00"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
func myMindEnvelopeAAD(e myMindCustodyEnvelope) []byte {
	return []byte(strings.Join([]string{e.Schema, e.PersonID, e.SourceID, fmt.Sprint(e.Revision), e.Kind, fmt.Sprint(e.ConsentRevision), e.BodyDigest, e.KeyID, fmt.Sprint(e.KeyVersion), e.UpdatedAt.UTC().Format(time.RFC3339Nano)}, "\x00"))
}
func privateSourceFromEnvelope(e myMindCustodyEnvelope, body string) MyMindPrivateSource {
	return MyMindPrivateSource{PersonID: e.PersonID, SourceID: e.SourceID, Revision: e.Revision, Kind: e.Kind, Body: body, BodyDigest: e.BodyDigest, ConsentRevision: e.ConsentRevision, UpdatedAt: e.UpdatedAt}
}
func validMyMindPrivateBody(body string) bool {
	return body == strings.TrimSpace(body) && body != "" && utf8.ValidString(body) && len([]byte(body)) <= myMindCustodyMaxBody && !strings.ContainsRune(body, 0)
}
func validMyMindCustodyMutation(idempotency, sourceID, kind, body string, expected int64) bool {
	return strideIdentifier(idempotency) && strideIdentifier(sourceID) && oneOf(kind, "preference", "reflection", "correction") && validMyMindPrivateBody(body) && expected >= 0
}
func validMyMindPrivateAuthority(a MyMindPrivateAuthority) bool {
	return strideIdentifier(a.PersonID) && strideIdentifier(a.OrganizationID) && strideIdentifier(a.MembershipID) && a.MembershipRevision > 0 && isHexDigest(a.SessionSubjectDigest) && a.SessionRevision > 0 && !a.At.IsZero()
}
func rejectMyMindDeletion(state *myMindCustodyState, personID string) error {
	if _, ok := state.Deletions[personID]; ok {
		return ErrMyMindCustodyDenied
	}
	return nil
}
func myMindPersonManifest(state *myMindCustodyState, personID string) string {
	var refs []string
	for _, e := range state.Records {
		if e.PersonID == personID {
			refs = append(refs, strings.Join([]string{e.SourceID, fmt.Sprint(e.Revision), e.BodyDigest, e.KeyID, fmt.Sprint(e.KeyVersion)}, ":"))
		}
	}
	sort.Strings(refs)
	return myMindCustodyDigest(refs...)
}

func myMindPersonKeyRefs(state *myMindCustodyState, personID string) []myMindCustodyKeyRef {
	seen := map[string]myMindCustodyKeyRef{}
	for _, envelope := range state.Records {
		if envelope.PersonID != personID {
			continue
		}
		key := fmt.Sprintf("%s\x00%d", envelope.KeyID, envelope.KeyVersion)
		seen[key] = myMindCustodyKeyRef{ID: envelope.KeyID, Version: envelope.KeyVersion}
	}
	refs := make([]myMindCustodyKeyRef, 0, len(seen))
	for _, ref := range seen {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].ID != refs[j].ID {
			return refs[i].ID < refs[j].ID
		}
		return refs[i].Version < refs[j].Version
	})
	return refs
}
