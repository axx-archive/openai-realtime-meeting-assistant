package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testMyMindKeyring struct {
	mu                     sync.Mutex
	current                map[string]MyMindCustodyKey
	keys                   map[string]MyMindCustodyKey
	destroyed              map[string]bool
	destroyReceipts        map[string]MyMindKeyDestructionReceipt
	destroyBindings        map[string]string
	destroyCalls           map[string]int
	evidenceKey            []byte
	failDestroy            bool
	loseSourceResponseOnce bool
	losePersonResponseOnce bool
	destroyStarted         chan struct{}
	destroyRelease         chan struct{}
}

func newTestMyMindKeyring(personID string) *testMyMindKeyring {
	key := MyMindCustodyKey{ID: "mymind_source_key", Version: 1, PersonID: personID, Material: []byte("0123456789abcdef0123456789abcdef")}
	return &testMyMindKeyring{current: map[string]MyMindCustodyKey{personID: key}, keys: map[string]MyMindCustodyKey{}, destroyed: map[string]bool{}, destroyReceipts: map[string]MyMindKeyDestructionReceipt{}, destroyBindings: map[string]string{}, destroyCalls: map[string]int{}, evidenceKey: []byte("destruction-evidence-key-32-bytes!")}
}

func testMyMindKey(personID, sourceID, keyID string, version int64) string {
	return personID + "\x00" + sourceID + "\x00" + keyID + "\x00" + string(rune(version))
}

func (r *testMyMindKeyring) CurrentMyMindCustodyKey(_ context.Context, personID, sourceID string) (MyMindCustodyKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.destroyed[personID] || r.destroyed[personID+"\x00"+sourceID] {
		return MyMindCustodyKey{}, ErrMyMindCustodyDenied
	}
	key, ok := r.current[personID]
	if !ok {
		return MyMindCustodyKey{}, ErrMyMindCustodyDenied
	}
	key.SourceID = sourceID
	derived := sha256.Sum256(append(append([]byte(nil), key.Material...), []byte("\x00"+sourceID)...))
	key.Material = derived[:]
	r.keys[testMyMindKey(personID, sourceID, key.ID, key.Version)] = key
	key.Material = append([]byte(nil), key.Material...)
	return key, nil
}

func (r *testMyMindKeyring) ResolveMyMindCustodyKey(_ context.Context, personID, sourceID, keyID string, version int64) (MyMindCustodyKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.destroyed[personID] || r.destroyed[personID+"\x00"+sourceID] {
		return MyMindCustodyKey{}, ErrMyMindCustodyDenied
	}
	key, ok := r.keys[testMyMindKey(personID, sourceID, keyID, version)]
	if !ok {
		return MyMindCustodyKey{}, ErrMyMindCustodyDenied
	}
	key.Material = append([]byte(nil), key.Material...)
	return key, nil
}

func (r *testMyMindKeyring) destructionReceipt(operationID, scope, personID, sourceID string, refs []myMindCustodyKeyRef) MyMindKeyDestructionReceipt {
	receipt := MyMindKeyDestructionReceipt{Schema: "stride.mymind.key-destruction.v1", OperationID: operationID, Scope: scope, PersonID: personID, SourceID: sourceID, KeyRefsDigest: myMindKeyRefsDigest(refs), EvidenceKeyID: "test_destruction_key", EvidenceVersion: 1, DestroyedAt: time.Date(2026, 8, 9, 23, 0, 0, 0, time.UTC), VerificationContract: "managed_keyring_v1"}
	receipt.MAC = myMindDestructionMAC(r.evidenceKey, receipt)
	receipt.ReceiptDigest = myMindDestructionReceiptDigest(receipt)
	return receipt
}

func (r *testMyMindKeyring) DestroySourceMyMindKeys(_ context.Context, operationID, personID, sourceID string, refs []myMindCustodyKeyRef) (MyMindKeyDestructionReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	binding := strings.Join([]string{"source", personID, sourceID, myMindKeyRefsDigest(refs)}, "\x00")
	if prior, ok := r.destroyReceipts[operationID]; ok {
		if r.destroyBindings[operationID] != binding {
			return MyMindKeyDestructionReceipt{}, ErrMyMindCustodyConflict
		}
		return prior, nil
	}
	if r.destroyStarted != nil {
		select {
		case r.destroyStarted <- struct{}{}:
		default:
		}
		<-r.destroyRelease
	}
	if r.failDestroy {
		return MyMindKeyDestructionReceipt{}, errors.New("injected key destruction outage")
	}
	r.destroyCalls[operationID]++
	r.destroyed[personID+"\x00"+sourceID] = true
	receipt := r.destructionReceipt(operationID, "source", personID, sourceID, refs)
	r.destroyReceipts[operationID], r.destroyBindings[operationID] = receipt, binding
	if r.loseSourceResponseOnce {
		r.loseSourceResponseOnce = false
		return MyMindKeyDestructionReceipt{}, errors.New("injected lost source destruction response")
	}
	return receipt, nil
}

func (r *testMyMindKeyring) DestroyPersonMyMindKeys(_ context.Context, operationID, personID string, refs []myMindCustodyKeyRef) (MyMindKeyDestructionReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	binding := strings.Join([]string{"person", personID, myMindKeyRefsDigest(refs)}, "\x00")
	if prior, ok := r.destroyReceipts[operationID]; ok {
		if r.destroyBindings[operationID] != binding {
			return MyMindKeyDestructionReceipt{}, ErrMyMindCustodyConflict
		}
		return prior, nil
	}
	if r.failDestroy {
		return MyMindKeyDestructionReceipt{}, errors.New("injected key destruction outage")
	}
	r.destroyCalls[operationID]++
	r.destroyed[personID] = true
	receipt := r.destructionReceipt(operationID, "person", personID, "", refs)
	r.destroyReceipts[operationID], r.destroyBindings[operationID] = receipt, binding
	if r.losePersonResponseOnce {
		r.losePersonResponseOnce = false
		return MyMindKeyDestructionReceipt{}, errors.New("injected lost person destruction response")
	}
	return receipt, nil
}

func (r *testMyMindKeyring) VerifyMyMindKeyDestruction(_ context.Context, receipt MyMindKeyDestructionReceipt) error {
	if receipt.MAC != myMindDestructionMAC(r.evidenceKey, receipt) {
		return ErrMyMindCustodyDenied
	}
	return nil
}

func (r *testMyMindKeyring) rotate(personID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := MyMindCustodyKey{ID: "mymind_source_key", Version: 2, PersonID: personID, Material: []byte("abcdef0123456789abcdef0123456789")}
	r.current[personID] = key
}

func (r *testMyMindKeyring) retire(personID, sourceID, keyID string, version int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.keys, testMyMindKey(personID, sourceID, keyID, version))
}

type testMyMindStateControl struct {
	mu            sync.Mutex
	current       MyMindCustodyStateKey
	keys          map[string]MyMindCustodyStateKey
	high          map[string]MyMindCustodyHighWater
	advanceCalls  int
	failAdvanceAt int
}

func newTestMyMindStateControl(key MyMindCustodyStateKey) *testMyMindStateControl {
	return &testMyMindStateControl{current: key, keys: map[string]MyMindCustodyStateKey{fmt.Sprintf("%s/%d", key.ID, key.Version): key}, high: map[string]MyMindCustodyHighWater{}}
}
func (c *testMyMindStateControl) CurrentMyMindCustodyStateKey(context.Context) (MyMindCustodyStateKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneTestStateKey(c.current), nil
}
func (c *testMyMindStateControl) ResolveMyMindCustodyStateKey(_ context.Context, id string, v int64) (MyMindCustodyStateKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k, ok := c.keys[fmt.Sprintf("%s/%d", id, v)]
	if !ok {
		return MyMindCustodyStateKey{}, ErrMyMindCustodyDenied
	}
	return cloneTestStateKey(k), nil
}
func (c *testMyMindStateControl) ReadMyMindCustodyHighWater(_ context.Context, id string) (MyMindCustodyHighWater, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.high[id], nil
}
func (c *testMyMindStateControl) AdvanceMyMindCustodyHighWater(_ context.Context, id string, prior, next MyMindCustodyHighWater) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.advanceCalls++
	if c.failAdvanceAt == c.advanceCalls {
		return errors.New("injected high-water outage")
	}
	if c.high[id] != prior || next.Generation != prior.Generation+1 {
		return ErrMyMindCustodyConflict
	}
	c.high[id] = next
	return nil
}
func cloneTestStateKey(k MyMindCustodyStateKey) MyMindCustodyStateKey {
	k.Material = append([]byte(nil), k.Material...)
	return k
}
func (c *testMyMindStateControl) rotate(key MyMindCustodyStateKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = key
	c.keys[fmt.Sprintf("%s/%d", key.ID, key.Version)] = key
}
func (c *testMyMindStateControl) retire(id string, version int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.keys, fmt.Sprintf("%s/%d", id, version))
}

type testMyMindAuthorityResolver struct {
	mu      sync.Mutex
	current MyMindPrivateAuthority
	deny    bool
}

func (r *testMyMindAuthorityResolver) WithCurrentMyMindPrivateAuthority(_ context.Context, _ MyMindPrivateAuthority, callback func(MyMindPrivateAuthority) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deny {
		return ErrMyMindCustodyDenied
	}
	return callback(r.current)
}

type myMindCustodyFixture struct {
	service      *FileMyMindCustody
	keys         *testMyMindKeyring
	resolver     *testMyMindAuthorityResolver
	authority    MyMindPrivateAuthority
	path         string
	stateKey     MyMindCustodyStateKey
	stateControl *testMyMindStateControl
}

func newMyMindCustodyFixture(t *testing.T) myMindCustodyFixture {
	t.Helper()
	now := time.Date(2026, 8, 9, 20, 0, 0, 0, time.UTC)
	authority := MyMindPrivateAuthority{PersonID: "person_aj", OrganizationID: "organization_bonfire", MembershipID: "membership_aj", MembershipRevision: 4, SessionSubjectDigest: strings.Repeat("a", 64), SessionRevision: 9, At: now}
	keys := newTestMyMindKeyring(authority.PersonID)
	resolver := &testMyMindAuthorityResolver{current: authority}
	stateKey := MyMindCustodyStateKey{ID: "mymind_state_key", Version: 1, Material: []byte("state-key-0123456789abcdef012345")}
	stateControl := newTestMyMindStateControl(stateKey)
	path := filepath.Join(t.TempDir(), "private-mymind.json")
	service, err := NewFileMyMindCustody(path, stateControl, stateControl, keys, resolver)
	if err != nil {
		t.Fatal(err)
	}
	return myMindCustodyFixture{service: service, keys: keys, resolver: resolver, authority: authority, path: path, stateKey: stateKey, stateControl: stateControl}
}

func TestMyMindPrivateCustodyPutInspectCorrectForgetExportAndRestart(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	created, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create", "preference_one", "preference", "Lead with the answer.", 0)
	if err != nil || created.Revision != 1 || created.Body == "" || !isHexDigest(created.BodyDigest) {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	rawState, err := os.ReadFile(fixture.path)
	if err != nil || strings.Contains(string(rawState), created.Body) {
		t.Fatalf("plaintext persisted err=%v", err)
	}
	if _, err := fixture.service.Correct(context.Background(), fixture.authority, "operation_bad_correct", created.SourceID, "invalid initial correction", 0); !errors.Is(err, ErrMyMindCustodyInvalid) {
		t.Fatalf("initial correction=%v", err)
	}
	replay, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create", "preference_one", "preference", "Lead with the answer.", 0)
	if err != nil || replay != created {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create", "preference_one", "preference", "Changed replay.", 0); !errors.Is(err, ErrMyMindCustodyConflict) {
		t.Fatalf("changed idempotency=%v", err)
	}

	corrected, err := fixture.service.Correct(context.Background(), fixture.authority, "operation_correct", created.SourceID, "Lead with the recommendation, then explain.", 1)
	if err != nil || corrected.Revision != 2 || corrected.ConsentRevision != 2 || corrected.Kind != "correction" {
		t.Fatalf("correct=%+v err=%v", corrected, err)
	}
	exported, err := fixture.service.Export(context.Background(), fixture.authority)
	if err != nil || len(exported.Sources) != 1 || exported.Sources[0] != corrected || !isHexDigest(exported.ManifestDigest) {
		t.Fatalf("export=%+v err=%v", exported, err)
	}

	restarted, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver)
	if err != nil {
		t.Fatal(err)
	}
	items, err := restarted.Inspect(context.Background(), fixture.authority)
	if err != nil || len(items) != 1 || items[0] != corrected {
		t.Fatalf("restart inspect=%+v err=%v", items, err)
	}
	if err := restarted.Forget(context.Background(), fixture.authority, "operation_forget", corrected.SourceID, 2); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Forget(context.Background(), fixture.authority, "operation_forget", corrected.SourceID, 2); err != nil {
		t.Fatalf("forget replay=%v", err)
	}
	items, err = restarted.Inspect(context.Background(), fixture.authority)
	if err != nil || len(items) != 0 {
		t.Fatalf("after forget=%+v err=%v", items, err)
	}
}

func TestMyMindPrivateCustodyRotationRetiredKeyWrongPersonAndTamper(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create", "reflection_one", "reflection", "A private reflection.", 0); err != nil {
		t.Fatal(err)
	}
	fixture.keys.rotate(fixture.authority.PersonID)
	if err := fixture.service.Rotate(context.Background(), fixture.authority, "operation_rotate"); err != nil {
		t.Fatal(err)
	}
	fixture.keys.retire(fixture.authority.PersonID, "reflection_one", "mymind_source_key", 1)
	if err := fixture.service.VerifyRestore(context.Background()); err != nil {
		t.Fatalf("rotated restore=%v", err)
	}
	restarted, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver)
	if err != nil {
		t.Fatal(err)
	}
	if items, err := restarted.Inspect(context.Background(), fixture.authority); err != nil || len(items) != 1 {
		t.Fatalf("retired-key restart=%+v err=%v", items, err)
	}

	wrong := fixture.authority
	wrong.PersonID = "person_other"
	if _, err := fixture.service.Inspect(context.Background(), wrong); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("wrong person=%v", err)
	}

	raw, err := os.ReadFile(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	wrongStateKey := fixture.stateKey
	wrongStateKey.Material = []byte("wrong-key-0123456789abcdef012345")
	wrongControl := newTestMyMindStateControl(wrongStateKey)
	wrongControl.high[fixture.path] = fixture.stateControl.high[fixture.path]
	if _, err := NewFileMyMindCustody(fixture.path, wrongControl, wrongControl, fixture.keys, fixture.resolver); !errors.Is(err, ErrMyMindCustodyTampered) {
		t.Fatalf("wrong state key=%v", err)
	}
	state, err := fixture.service.loadState()
	if err != nil {
		t.Fatal(err)
	}
	for key, envelope := range state.Records {
		envelope.Ciphertext[0] ^= 1
		state.Records[key] = envelope
		break
	}
	state.Generation++
	if err := fixture.service.writeState(state); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Inspect(context.Background(), fixture.authority); !errors.Is(err, ErrMyMindCustodyTampered) {
		t.Fatalf("authenticated ciphertext tamper=%v", err)
	}
	if err := os.WriteFile(fixture.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 1
	if err := os.WriteFile(fixture.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver); !errors.Is(err, ErrMyMindCustodyTampered) {
		t.Fatalf("tampered state=%v", err)
	}
}

func TestMyMindPrivateCustodyAuthorityRevisionAndConcurrentCAS(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	stale := fixture.authority
	stale.MembershipRevision--
	if _, err := fixture.service.Put(context.Background(), stale, "operation_stale", "source_stale", "preference", "Never store this.", 0); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("stale authority=%v", err)
	}
	created, err := fixture.service.Put(context.Background(), fixture.authority, "operation_initial", "source_cas", "preference", "Initial.", 0)
	if err != nil {
		t.Fatal(err)
	}

	other, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for index, service := range []*FileMyMindCustody{fixture.service, other} {
		wg.Add(1)
		go func(index int, service *FileMyMindCustody) {
			defer wg.Done()
			_, err := service.Correct(context.Background(), fixture.authority, "operation_cas_"+string(rune('a'+index)), created.SourceID, "Correction "+string(rune('A'+index))+".", 1)
			results <- err
		}(index, service)
	}
	wg.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrMyMindCustodyConflict) {
			conflicts++
		} else {
			t.Fatalf("CAS err=%v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestMyMindPrivateCustodyDeletionJournalResumesAndCryptoErases(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create", "source_delete", "reflection", "Delete me.", 0); err != nil {
		t.Fatal(err)
	}
	fixture.keys.failDestroy = true
	err := fixture.service.DeletePerson(context.Background(), fixture.authority, "operation_delete")
	if !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("destroy outage=%v", err)
	}
	fixture.keys.failDestroy = false
	restarted, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ResumeDeletions(context.Background()); err != nil {
		t.Fatalf("resume=%v", err)
	}
	if err := restarted.VerifyRestore(context.Background()); err != nil {
		t.Fatalf("verify=%v", err)
	}
	if _, err := restarted.Inspect(context.Background(), fixture.authority); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("inspect deleted=%v", err)
	}
	if _, err := fixture.keys.ResolveMyMindCustodyKey(context.Background(), fixture.authority.PersonID, "source_delete", "mymind_source_key", 1); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("destroyed key resolved=%v", err)
	}
	if err := restarted.DeletePerson(context.Background(), fixture.authority, "operation_delete"); err != nil {
		t.Fatalf("delete replay=%v", err)
	}
}

func TestResolveMyMindPrivateAuthorityUsesCurrentOrganizationSession(t *testing.T) {
	now := time.Date(2026, 8, 9, 21, 0, 0, 0, time.UTC)
	person := PersonPrincipal{Header: STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: "person_aj", Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractPersonPrincipal, ContentDigest: strings.Repeat("a", 64), CreatedAt: now}, AccountSubjectDigest: strings.Repeat("9", 64), Status: "active", RecoveryRevision: 1, CustodyRevision: 1}
	membership := OrganizationMembership{Header: STRIDEContractHeader{TenantID: "organization_bonfire", ID: "membership_aj", Revision: 4, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractOrganizationMembership, ContentDigest: strings.Repeat("b", 64), CreatedAt: now}, PersonID: "person_aj", OrganizationID: "organization_bonfire", Role: "owner", Status: "active", GrantedAt: now}
	session := ActiveOrganizationSession{Header: STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: "session_binding", Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractActiveOrganizationSession, ContentDigest: strings.Repeat("c", 64), CreatedAt: now}, SessionSubjectDigest: strings.Repeat("d", 64), PersonID: membership.PersonID, OrganizationID: membership.OrganizationID, MembershipID: membership.Header.ID, MembershipRevision: membership.Header.Revision, SessionRevision: 2, Status: "active", BoundAt: now, ExpiresAt: now.Add(time.Hour)}
	authority, err := ResolveMyMindPrivateAuthority(person, membership, session, now.Add(time.Minute))
	if err != nil || authority.PersonID != membership.PersonID || authority.MembershipRevision != 4 {
		t.Fatalf("authority=%+v err=%v", authority, err)
	}
	stale := session
	stale.MembershipRevision--
	if _, err := ResolveMyMindPrivateAuthority(person, membership, stale, now.Add(time.Minute)); !errors.Is(err, ErrMyMindDenied) {
		t.Fatalf("stale session=%v", err)
	}
	departed := membership
	departed.Status = "departed"
	departed.EndedAt = myMindTimePtr(now.Add(time.Minute))
	departed.Header.Revision++
	if _, err := ResolveMyMindPrivateAuthority(person, departed, session, now.Add(2*time.Minute)); !errors.Is(err, ErrMyMindDenied) {
		t.Fatalf("departed membership=%v", err)
	}
	deleted := person
	deleted.Status = "deleted"
	deleted.DeletedAt = myMindTimePtr(now.Add(time.Minute))
	deleted.CustodyDeletionReceiptID = "deletion_receipt"
	deleted.Header.Revision++
	if _, err := ResolveMyMindPrivateAuthority(deleted, membership, session, now.Add(2*time.Minute)); !errors.Is(err, ErrMyMindDenied) {
		t.Fatalf("deleted person=%v", err)
	}
}

func TestMyMindPrivateCustodyRejectsSharedModesAndAutomaticImportKinds(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	for _, kind := range []string{"private_import", "public_work", "portable_receipt", "shared_answer", "cite"} {
		if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_"+kind, "source_"+kind, kind, "private value", 0); !errors.Is(err, ErrMyMindCustodyInvalid) {
			t.Fatalf("kind %s=%v", kind, err)
		}
	}
}

func TestMyMindPrivateCustodyLowEntropyDigestsArePersonKeyed(t *testing.T) {
	at := time.Date(2026, 8, 9, 22, 0, 0, 0, time.UTC)
	one := MyMindCustodyKey{ID: "key_one", Version: 1, PersonID: "person_one", SourceID: "preference_one", Material: []byte("11111111111111111111111111111111")}
	two := MyMindCustodyKey{ID: "key_two", Version: 1, PersonID: "person_two", SourceID: "preference_one", Material: []byte("22222222222222222222222222222222")}
	left, err := sealMyMindEnvelope(one, one.PersonID, "preference_one", 1, "preference", 1, "yes", at)
	if err != nil {
		t.Fatal(err)
	}
	right, err := sealMyMindEnvelope(two, two.PersonID, "preference_one", 1, "preference", 1, "yes", at)
	if err != nil {
		t.Fatal(err)
	}
	if left.BodyDigest == right.BodyDigest || left.BodyDigest == myMindCustodyDigest("yes") {
		t.Fatal("low-entropy body digest was not person-keyed")
	}
}

func TestMyMindPrivateCustodyForgetRejectsBackupRollbackAndAuthenticatesDestruction(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create", "source_erase", "preference", "yes", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_keep", "source_keep", "reflection", "still private", 0); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Forget(context.Background(), fixture.authority, "operation_forget", "source_erase", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.keys.ResolveMyMindCustodyKey(context.Background(), fixture.authority.PersonID, "source_erase", "mymind_source_key", 1); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("forgotten source key resolved: %v", err)
	}
	if items, err := fixture.service.Inspect(context.Background(), fixture.authority); err != nil || len(items) != 1 || items[0].SourceID != "source_keep" {
		t.Fatalf("per-source erasure affected sibling source: items=%v err=%v", items, err)
	}
	if err := fixture.service.VerifyRestore(context.Background()); err != nil {
		t.Fatalf("post-forget restore verification: %v", err)
	}
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_resurrect", "source_erase", "preference", "resurrect", 0); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("forgotten source resurrected: %v", err)
	}
	if err := os.WriteFile(fixture.path, backup, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver); !errors.Is(err, ErrMyMindCustodyTampered) {
		t.Fatalf("rolled-back backup accepted: %v", err)
	}
}

func TestMyMindPrivateCustodyPublicationJournalRecoversFileAheadOfHighWater(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	fixture.stateControl.mu.Lock()
	fixture.stateControl.failAdvanceAt = fixture.stateControl.advanceCalls + 1
	fixture.stateControl.mu.Unlock()
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_lost", "source_lost", "reflection", "durable", 0); !errors.Is(err, ErrMyMindCustodyTampered) {
		t.Fatalf("injected publication err=%v", err)
	}
	if _, err := os.Stat(fixture.path + ".txn"); err != nil {
		t.Fatalf("publication journal missing: %v", err)
	}
	restarted, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	item, err := restarted.Put(context.Background(), fixture.authority, "operation_lost", "source_lost", "reflection", "durable", 0)
	if err != nil || item.SourceID != "source_lost" {
		t.Fatalf("lost response replay=%+v err=%v", item, err)
	}
	if _, err := os.Stat(fixture.path + ".txn"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publication journal not cleared: %v", err)
	}
}

func TestMyMindPrivateCustodyPublicationJournalRecoversPreparedAndHighWaterCommitted(t *testing.T) {
	for _, phase := range []string{"prepared", "high_water_committed"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newMyMindCustodyFixture(t)
			if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_base", "source_base", "reflection", "base", 0); err != nil {
				t.Fatal(err)
			}
			state, err := fixture.service.loadState()
			if err != nil {
				t.Fatal(err)
			}
			prior := fixture.stateControl.high[fixture.path]
			state.Generation++
			state.Operations[myMindOperationKey(fixture.authority.PersonID, "recovered_marker")] = myMindCustodyOperation{Fingerprint: myMindCustodyDigest("marker"), Kind: "rotate", PersonID: fixture.authority.PersonID}
			payload, _ := json.Marshal(state)
			key, _ := fixture.stateControl.CurrentMyMindCustodyStateKey(context.Background())
			envelope := myMindCustodyStateEnvelope{Schema: myMindCustodyStateSchema, KeyID: key.ID, KeyVersion: key.Version, Payload: payload, MAC: myMindStateMAC(key.Material, payload)}
			stateBytes, _ := json.Marshal(envelope)
			next := MyMindCustodyHighWater{Generation: state.Generation, PayloadDigest: myMindBytesDigest(payload)}
			journal := myMindCustodyPublicationJournal{Schema: "stride.mymind.private-custody-publication.v1", StoreID: fixture.path, Prior: prior, Next: next, StateBytes: stateBytes, KeyID: key.ID, KeyVersion: key.Version}
			journal.MAC = myMindPublicationMAC(key.Material, journal)
			journalBytes, _ := json.Marshal(journal)
			if err := writeMyMindAtomicFile(fixture.path+".txn", journalBytes); err != nil {
				t.Fatal(err)
			}
			if phase == "high_water_committed" {
				if err := fixture.stateControl.AdvanceMyMindCustodyHighWater(context.Background(), fixture.path, prior, next); err != nil {
					t.Fatal(err)
				}
			}
			restarted, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver)
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := restarted.loadState()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := recovered.Operations[myMindOperationKey(fixture.authority.PersonID, "recovered_marker")]; !ok {
				t.Fatal("publication marker not recovered")
			}
		})
	}
}

func TestMyMindPrivateCustodyForgetResumesAfterKeyDestructionPublicationFailure(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create", "source_crash_forget", "preference", "erase", 0); err != nil {
		t.Fatal(err)
	}
	fixture.stateControl.mu.Lock()
	fixture.stateControl.failAdvanceAt = fixture.stateControl.advanceCalls + 2
	fixture.stateControl.mu.Unlock()
	if err := fixture.service.Forget(context.Background(), fixture.authority, "operation_forget_crash", "source_crash_forget", 1); !errors.Is(err, ErrMyMindCustodyTampered) {
		t.Fatalf("forget publication err=%v", err)
	}
	if _, err := fixture.keys.ResolveMyMindCustodyKey(context.Background(), fixture.authority.PersonID, "source_crash_forget", "mymind_source_key", 1); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("key survived: %v", err)
	}
	restarted, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ResumeSourceForgets(context.Background()); err != nil {
		t.Fatalf("resume source forget: %v", err)
	}
	if err := restarted.VerifyRestore(context.Background()); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if items, err := restarted.Inspect(context.Background(), fixture.authority); err != nil || len(items) != 0 {
		t.Fatalf("items=%v err=%v", items, err)
	}
}

func TestMyMindPrivateCustodyBootstrapReplaysLostSourceDestructionResponse(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create_lost_source", "source_lost_destroy", "preference", "erase", 0); err != nil {
		t.Fatal(err)
	}
	fixture.keys.loseSourceResponseOnce = true
	if err := fixture.service.Forget(context.Background(), fixture.authority, "operation_forget_lost_source", "source_lost_destroy", 1); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("lost destruction response=%v", err)
	}
	state, err := fixture.service.loadState()
	if err != nil {
		t.Fatal(err)
	}
	journal := state.SourceDeletions[myMindRecordKey(fixture.authority.PersonID, "source_lost_destroy")]
	if journal.Phase != "prepared" || !strideIdentifier(journal.OperationID) {
		t.Fatalf("journal=%+v", journal)
	}
	if fixture.keys.destroyCalls[journal.OperationID] != 1 {
		t.Fatalf("external destructions=%d", fixture.keys.destroyCalls[journal.OperationID])
	}
	fixture.resolver.deny = true
	if _, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("bootstrap accepted unresolved forget without current authority: %v", err)
	}
	if fixture.keys.destroyCalls[journal.OperationID] != 1 {
		t.Fatalf("denied bootstrap repeated destruction: %d", fixture.keys.destroyCalls[journal.OperationID])
	}
	fixture.resolver.deny = false
	restarted, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver)
	if err != nil {
		t.Fatalf("bootstrap resume=%v", err)
	}
	if fixture.keys.destroyCalls[journal.OperationID] != 1 {
		t.Fatalf("destruction repeated after lost response: %d", fixture.keys.destroyCalls[journal.OperationID])
	}
	if items, err := restarted.Inspect(context.Background(), fixture.authority); err != nil || len(items) != 0 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if _, err := fixture.keys.DestroySourceMyMindKeys(context.Background(), journal.OperationID, fixture.authority.PersonID, "different_source", journal.KeyRefs); !errors.Is(err, ErrMyMindCustodyConflict) {
		t.Fatalf("changed replay=%v", err)
	}
}

func TestMyMindPrivateCustodyBootstrapReplaysLostPersonDestructionResponse(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create_lost_person", "source_lost_person", "reflection", "erase all", 0); err != nil {
		t.Fatal(err)
	}
	fixture.keys.losePersonResponseOnce = true
	if err := fixture.service.DeletePerson(context.Background(), fixture.authority, "operation_delete_lost_person"); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("lost destruction response=%v", err)
	}
	state, err := fixture.service.loadState()
	if err != nil {
		t.Fatal(err)
	}
	journal := state.Deletions[fixture.authority.PersonID]
	if journal.Phase != "records_removed" || !strideIdentifier(journal.OperationID) {
		t.Fatalf("journal=%+v", journal)
	}
	if fixture.keys.destroyCalls[journal.OperationID] != 1 {
		t.Fatalf("external destructions=%d", fixture.keys.destroyCalls[journal.OperationID])
	}
	restarted, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver)
	if err != nil {
		t.Fatalf("bootstrap resume=%v", err)
	}
	if fixture.keys.destroyCalls[journal.OperationID] != 1 {
		t.Fatalf("destruction repeated after lost response: %d", fixture.keys.destroyCalls[journal.OperationID])
	}
	if _, err := restarted.Inspect(context.Background(), fixture.authority); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("deleted person inspect=%v", err)
	}
	if err := restarted.VerifyRestore(context.Background()); err != nil {
		t.Fatalf("verify=%v", err)
	}
}

func TestMyMindPrivateCustodyForgetHoldsCurrentAuthorityThroughFinalFileEffect(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create", "source_interleave", "reflection", "erase", 0); err != nil {
		t.Fatal(err)
	}
	fixture.keys.destroyStarted = make(chan struct{}, 1)
	fixture.keys.destroyRelease = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- fixture.service.Forget(context.Background(), fixture.authority, "operation_forget_interleave", "source_interleave", 1)
	}()
	select {
	case <-fixture.keys.destroyStarted:
	case <-time.After(time.Second):
		t.Fatal("destruction did not start")
	}
	if fixture.resolver.mu.TryLock() {
		fixture.resolver.mu.Unlock()
		t.Fatal("authority lock released before key destruction/final publication")
	}
	close(fixture.keys.destroyRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !fixture.resolver.mu.TryLock() {
		t.Fatal("authority lock not released after final effect")
	}
	fixture.resolver.mu.Unlock()
	if items, err := fixture.service.Inspect(context.Background(), fixture.authority); err != nil || len(items) != 0 {
		t.Fatalf("items=%v err=%v", items, err)
	}
}

func TestMyMindPrivateCustodyStateKeyRotationResealsAndRetiredKeyRestarts(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create", "source_rotate_state", "reflection", "private", 0); err != nil {
		t.Fatal(err)
	}
	rotated := MyMindCustodyStateKey{ID: "mymind_state_key", Version: 2, Material: []byte("rotated-state-key-0123456789abcd")}
	fixture.stateControl.rotate(rotated)
	if _, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver); err != nil {
		t.Fatalf("reseal: %v", err)
	}
	fixture.stateControl.retire(fixture.stateKey.ID, fixture.stateKey.Version)
	restarted, err := NewFileMyMindCustody(fixture.path, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver)
	if err != nil {
		t.Fatalf("retired old state key restart: %v", err)
	}
	if items, err := restarted.Inspect(context.Background(), fixture.authority); err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
}

func TestMyMindPrivateCustodyOperationFingerprintOmitsLowEntropyBody(t *testing.T) {
	left := newMyMindCustodyFixture(t)
	right := newMyMindCustodyFixture(t)
	if _, err := left.service.Put(context.Background(), left.authority, "operation_same", "source_same", "preference", "yes", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := right.service.Put(context.Background(), right.authority, "operation_same", "source_same", "preference", "no", 0); err != nil {
		t.Fatal(err)
	}
	leftState, _ := left.service.loadState()
	rightState, _ := right.service.loadState()
	lf := leftState.Operations[myMindOperationKey(left.authority.PersonID, "operation_same")].Fingerprint
	rf := rightState.Operations[myMindOperationKey(right.authority.PersonID, "operation_same")].Fingerprint
	if lf != rf || strings.Contains(lf, "yes") || strings.Contains(lf, "no") {
		t.Fatal("operation fingerprint retained a recoverable body commitment")
	}
}

func TestMyMindPrivateCustodyRejectsSymlinkAndHardlinkState(t *testing.T) {
	fixture := newMyMindCustodyFixture(t)
	if _, err := fixture.service.Put(context.Background(), fixture.authority, "operation_create", "source_one", "preference", "Private.", 0); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(t.TempDir(), "symlink.json")
	if err := os.Symlink(fixture.path, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileMyMindCustody(symlink, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver); !errors.Is(err, ErrMyMindCustodyInvalid) {
		t.Fatalf("symlink=%v", err)
	}
	hardlink := filepath.Join(t.TempDir(), "hardlink.json")
	if err := os.Link(fixture.path, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileMyMindCustody(hardlink, fixture.stateControl, fixture.stateControl, fixture.keys, fixture.resolver); !errors.Is(err, ErrMyMindCustodyInvalid) {
		t.Fatalf("hardlink=%v", err)
	}
}

func TestPostgresMyMindPrivateCustodyMigrationIsCiphertextOnlyAndDefaultOff(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	var enabled bool
	if err := store.pool.QueryRow(ctx, `SELECT enabled FROM stride_feature_switches WHERE feature_key='person_mymind_context'`).Scan(&enabled); err != nil || enabled {
		t.Fatalf("feature enabled=%t err=%v", enabled, err)
	}
	var forbiddenColumns int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema='public' AND table_name='stride_mymind_private_custody_envelopes'
		AND column_name IN ('body','text','plaintext','email','token','authorization')`).Scan(&forbiddenColumns); err != nil || forbiddenColumns != 0 {
		t.Fatalf("forbidden custody columns=%d err=%v", forbiddenColumns, err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_person_principals
		(person_id,revision,account_subject_digest,status,recovery_revision,custody_revision,created_at)
		VALUES('person-custody',1,decode(repeat('a',64),'hex'),'active',1,1,now())`); err != nil {
		t.Fatal(err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_organizations
		(organization_id,revision,name,slug,status,creator_person_id,created_at,updated_at)
		VALUES('organization-custody',1,'Custody','custody','active','person-custody',now(),now())`); err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO stride_organization_membership_revisions
			(membership_id,revision,organization_id,person_id,role,status,granted_at,created_at,created_by_person_id)
			VALUES('membership-custody',1,'organization-custody','person-custody','owner','active',now(),now(),'person-custody')`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO stride_organization_memberships_current
		(membership_id,revision,organization_id,person_id,role,status,active_slot,updated_at)
		VALUES('membership-custody',1,'organization-custody','person-custody','owner','active',NULL,now())`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO stride_active_organization_sessions
		(session_subject_digest,person_id,organization_id,membership_id,membership_revision,session_revision,status,bound_at,expires_at,updated_at)
		VALUES(decode(repeat('f',64),'hex'),'person-custody','organization-custody','membership-custody',1,1,'active',now(),now()+interval '1 hour',now())`)
	}
	if err == nil {
		err = tx.Commit(ctx)
	} else {
		_ = tx.Rollback(ctx)
	}
	if err != nil {
		t.Fatalf("current authority fixture: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_operation_receipts
		(person_id,idempotency_key,operation_kind,request_fingerprint,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,recorded_at,authority_at)
		VALUES('person-custody','rotate-valid','rotate',decode(repeat('1',64),'hex'),'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now())`); err != nil {
		t.Fatalf("valid current authority receipt: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_operation_receipts
		(person_id,idempotency_key,operation_kind,request_fingerprint,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,recorded_at,authority_at)
		VALUES('person-custody','rotate-stale','rotate',decode(repeat('2',64),'hex'),'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),2,now(),now())`); err == nil {
		t.Fatal("stale session revision accepted")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_custody_envelopes
		(person_id,source_id,revision,source_kind,consent_revision,body_digest,key_id,key_version,nonce,ciphertext,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,authority_at,updated_at)
		VALUES('person-custody','source-private',1,'preference',1,decode(repeat('b',64),'hex'),'person-key',1,decode(repeat('00',12),'hex'),decode(repeat('11',32),'hex'),'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now())`); err != nil {
		t.Fatalf("valid ciphertext envelope: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_custody_envelopes
		(person_id,source_id,revision,source_kind,consent_revision,body_digest,key_id,key_version,nonce,ciphertext,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,authority_at,updated_at)
		VALUES('person-custody','source-import',1,'private_import',1,decode(repeat('c',64),'hex'),'person-key',1,decode(repeat('01',12),'hex'),decode(repeat('11',32),'hex'),'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now())`); err == nil {
		t.Fatal("automatic import source kind accepted")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_deletion_journals
		(person_id,idempotency_key,destruction_operation_id,request_fingerprint,phase,source_manifest_digest,key_refs,key_refs_digest,key_destruction_receipt_id,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,authority_at,started_at,updated_at)
		VALUES('person-custody','delete-one','destroy-one-operation',decode(repeat('d',64),'hex'),'completed',decode(repeat('e',64),'hex'),'[]'::jsonb,sha256(convert_to('[]','UTF8')),NULL,'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now(),now())`); err == nil {
		t.Fatal("completed deletion journal accepted without key destruction evidence")
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_mymind_private_operation_receipts SET recorded_at=recorded_at WHERE person_id='person-custody' AND idempotency_key='rotate-valid'`); err == nil {
		t.Fatal("immutable operation receipt updated")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_custody_envelopes
		(person_id,source_id,revision,source_kind,consent_revision,body_digest,key_id,key_version,nonce,ciphertext,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,authority_at,updated_at)
		VALUES('person-custody','source-nonce-reuse',1,'preference',1,decode(repeat('c',64),'hex'),'person-key',1,decode(repeat('00',12),'hex'),decode(repeat('22',32),'hex'),'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now())`); err == nil {
		t.Fatal("custody key nonce reuse accepted")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_key_destruction_receipts
		(receipt_id,destruction_operation_id,person_id,scope,source_id,key_refs_digest,evidence_key_id,evidence_key_version,destroyed_at,evidence_mac,verified,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,recorded_at,authority_at)
		VALUES('destroy-unverified','destroy-unverified-operation','person-custody','source','source-private',decode(repeat('1',64),'hex'),'managed-key',1,now(),decode(repeat('2',64),'hex'),false,'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now())`); err == nil {
		t.Fatal("unverified key destruction evidence accepted")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_custody_envelopes
		(person_id,source_id,revision,source_kind,consent_revision,body_digest,key_id,key_version,nonce,ciphertext,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,authority_at,updated_at)
		VALUES('person-custody','source-forgotten',1,'preference',1,decode(repeat('5',64),'hex'),'person-key',2,decode(repeat('02',12),'hex'),decode(repeat('33',32),'hex'),'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now())`); err != nil {
		t.Fatalf("source before forget: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `WITH e AS (SELECT * FROM stride_mymind_private_custody_envelopes WHERE person_id='person-custody' AND source_id='source-forgotten'),
		v AS (SELECT now() AS destroyed_at, jsonb_build_array(jsonb_build_object('id',e.key_id,'version',e.key_version)) refs, stride_mymind_private_source_envelope_digest(e) envelope_digest, e.* FROM e),
		b AS (SELECT *,sha256(convert_to(refs::text,'UTF8')) refs_digest,decode(repeat('4',64),'hex') evidence_mac FROM v)
		INSERT INTO stride_mymind_private_key_destruction_receipts
		(receipt_id,destruction_operation_id,person_id,scope,source_id,source_revision,source_envelope_digest,key_refs_digest,evidence_key_id,evidence_key_version,destroyed_at,evidence_mac,verification_contract,verification_receipt_digest,verified,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,recorded_at,authority_at)
		SELECT 'destroy-source','destroy-source-operation',person_id,'source',source_id,revision,envelope_digest,refs_digest,'managed-key',1,destroyed_at,evidence_mac,'managed_keyring_v1',
		sha256(convert_to(concat_ws(E'\x1f','destroy-source','destroy-source-operation',person_id,'source',source_id,revision::text,encode(envelope_digest,'hex'),encode(refs_digest,'hex'),'managed-key','1',destroyed_at::text,encode(evidence_mac,'hex'),'managed_keyring_v1'),'UTF8')),true,'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now() FROM b`); err != nil {
		t.Fatalf("verified destruction receipt: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_source_tombstones
		(person_id,source_id,source_revision,source_envelope_digest,key_refs,key_refs_digest,deletion_high_water,destruction_operation_id,key_destruction_receipt_id,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,authority_at,forgotten_at)
		SELECT r.person_id,r.source_id,r.source_revision,r.source_envelope_digest,jsonb_build_array(jsonb_build_object('id',e.key_id,'version',e.key_version)),r.key_refs_digest,1,r.destruction_operation_id,r.receipt_id,'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now()
		FROM stride_mymind_private_key_destruction_receipts r JOIN stride_mymind_private_custody_envelopes e USING(person_id,source_id) WHERE r.receipt_id='destroy-source'`); err != nil {
		t.Fatalf("source tombstone: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_custody_envelopes
		(person_id,source_id,revision,source_kind,consent_revision,body_digest,key_id,key_version,nonce,ciphertext,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,authority_at,updated_at)
		VALUES('person-custody','source-forgotten',1,'preference',1,decode(repeat('5',64),'hex'),'person-key',2,decode(repeat('02',12),'hex'),decode(repeat('33',32),'hex'),'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now())`); err == nil {
		t.Fatal("forgotten SQL source resurrected")
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_mymind_private_custody_envelopes SET session_revision=2 WHERE person_id='person-custody' AND source_id='source-private'`); err == nil {
		t.Fatal("stale authority mutated custody envelope")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_private_deletion_journals
		(person_id,idempotency_key,destruction_operation_id,request_fingerprint,phase,source_manifest_digest,key_refs,key_refs_digest,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,authority_at,started_at,updated_at)
		VALUES('person-custody','delete-prepared','destroy-person-operation',decode(repeat('3',64),'hex'),'prepared',decode(repeat('4',64),'hex'),'[]'::jsonb,sha256(convert_to('[]','UTF8')),'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now(),now())`); err != nil {
		t.Fatalf("valid deletion journal: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_mymind_private_deletion_journals SET session_revision=2 WHERE person_id='person-custody'`); err == nil {
		t.Fatal("stale authority mutated deletion journal")
	}
	if _, err := store.pool.Exec(ctx, `WITH v AS (SELECT now() destroyed_at,sha256(convert_to('[]','UTF8')) refs_digest,decode(repeat('6',64),'hex') evidence_mac)
		INSERT INTO stride_mymind_private_key_destruction_receipts
		(receipt_id,destruction_operation_id,person_id,scope,key_refs_digest,evidence_key_id,evidence_key_version,destroyed_at,evidence_mac,verification_contract,verification_receipt_digest,verified,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,recorded_at,authority_at)
		SELECT 'destroy-person','destroy-person-operation','person-custody','person',refs_digest,'managed-key',1,destroyed_at,evidence_mac,'managed_keyring_v1',sha256(convert_to(concat_ws(E'\x1f','destroy-person','destroy-person-operation','person-custody','person','','','',encode(refs_digest,'hex'),'managed-key','1',destroyed_at::text,encode(evidence_mac,'hex'),'managed_keyring_v1'),'UTF8')),true,'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now() FROM v`); err != nil {
		t.Fatalf("person destruction receipt: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `WITH v AS (SELECT now() destroyed_at,sha256(convert_to('[]','UTF8')) refs_digest,decode(repeat('7',64),'hex') evidence_mac)
		INSERT INTO stride_mymind_private_key_destruction_receipts
		(receipt_id,destruction_operation_id,person_id,scope,key_refs_digest,evidence_key_id,evidence_key_version,destroyed_at,evidence_mac,verification_contract,verification_receipt_digest,verified,organization_id,membership_id,membership_revision,session_subject_digest,session_revision,recorded_at,authority_at)
		SELECT 'destroy-person-wrong-operation','different-person-operation','person-custody','person',refs_digest,'managed-key',1,destroyed_at,evidence_mac,'managed_keyring_v1',sha256(convert_to(concat_ws(E'\x1f','destroy-person-wrong-operation','different-person-operation','person-custody','person','','','',encode(refs_digest,'hex'),'managed-key','1',destroyed_at::text,encode(evidence_mac,'hex'),'managed_keyring_v1'),'UTF8')),true,'organization-custody','membership-custody',1,decode(repeat('f',64),'hex'),1,now(),now() FROM v`); err != nil {
		t.Fatalf("alternate operation destruction receipt: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_mymind_private_deletion_journals SET phase='keys_destroyed',key_destruction_receipt_id='destroy-person-wrong-operation',updated_at=now(),authority_at=now() WHERE person_id='person-custody'`); err == nil {
		t.Fatal("destruction receipt from a different managed operation accepted")
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_mymind_private_deletion_journals SET phase='keys_destroyed',key_refs='[{"id":"wrong","version":1}]'::jsonb,key_refs_digest=sha256(convert_to('[{"id": "wrong", "version": 1}]','UTF8')),key_destruction_receipt_id='destroy-person',updated_at=now(),authority_at=now() WHERE person_id='person-custody'`); err == nil {
		t.Fatal("person destruction receipt accepted mismatched journal key refs")
	}
}
