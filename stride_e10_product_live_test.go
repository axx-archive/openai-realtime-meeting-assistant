package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type strideE10ProductTestKeyring struct {
	current StrideE10ProductOperationMACKey
	keys    map[string]StrideE10ProductOperationMACKey
}

func (k *strideE10ProductTestKeyring) CurrentStrideE10ProductOperationKey(context.Context) (StrideE10ProductOperationMACKey, error) {
	return k.current, nil
}

func (k *strideE10ProductTestKeyring) ResolveStrideE10ProductOperationKey(_ context.Context, id string, version uint64) (StrideE10ProductOperationMACKey, error) {
	key, ok := k.keys[id]
	if !ok || key.Version != version {
		return StrideE10ProductOperationMACKey{}, errors.New("unknown product operation key")
	}
	return key, nil
}

func strideE10ProductTestKeys(id string, version uint64) *strideE10ProductTestKeyring {
	key := StrideE10ProductOperationMACKey{ID: id, Version: version, Secret: []byte(strings.Repeat(id+"/", 16))}
	return &strideE10ProductTestKeyring{current: key, keys: map[string]StrideE10ProductOperationMACKey{id: key}}
}

func TestStrideE10ProductLiveRuntimeDefaultOffAndRegistered(t *testing.T) {
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC) })
	for _, feature := range allSTRIDEFeatures {
		if runtime.Enabled(feature) {
			t.Fatalf("live feature %q defaulted on", feature)
		}
	}
	mux := http.NewServeMux()
	prior := strideE10LiveProductRuntime
	strideE10LiveProductRuntime = runtime
	t.Cleanup(func() { strideE10LiveProductRuntime = prior })
	registerStrideE10ProductLiveRoutes(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/stride/v1/mobile/surfaces/profile", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("registered live route status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestStrideE10ProductLiveRegisteredRuntimeMintsAndHydratesZeroOrganizationActions(t *testing.T) {
	setupAuthTestEnv(t)
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	for _, feature := range []STRIDEFeature{STRIDEFeaturePersonProfileAuthority, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureActiveOrganizationSession} {
		runtime.setFeatureForTest(feature, true)
	}
	registerPerson := func(personID string) {
		principal := PersonPrincipal{Header: strideE10LiveHeader(STRIDEContractPersonPrincipal, STRIDEGlobalPersonTenant, personID, 1, personID+"-principal", now), AccountSubjectDigest: sha256Hex([]byte(personID + "-account")), Status: "active", RecoveryRevision: 1, CustodyRevision: 1}
		if err := runtime.organization.RegisterPerson(principal); err != nil {
			t.Fatal(err)
		}
	}
	registerPerson("person-zero")
	registerPerson("person-join")
	putSession := func(token, personID string) {
		store := userSessionStore()
		store.mu.Lock()
		email := personID + "@example.com"
		// The session store deliberately evaluates expiry against the real clock;
		// keep this harness session live independently of its frozen contract time.
		store.sessions[hashResetToken(token)] = sessionRecord{Email: email, PersonID: personID, AccountSubjectDigest: sha256Hex([]byte(email)), AuthorityGeneration: 1, Expires: time.Now().UTC().Add(24 * time.Hour)}
		store.mu.Unlock()
	}
	tokenZero, tokenJoin := strings.Repeat("a", 64), strings.Repeat("b", 64)
	putSession(tokenZero, "person-zero")
	putSession(tokenJoin, "person-join")
	prior := strideE10LiveProductRuntime
	strideE10LiveProductRuntime = runtime
	t.Cleanup(func() { strideE10LiveProductRuntime = prior })
	mux := http.NewServeMux()
	registerStrideE10ProductLiveRoutes(mux)
	request := func(method, path, token, key string, body any) *http.Request {
		req := strideE10Request(method, path, key, body)
		req.Header.Set("Authorization", "Bearer "+token)
		return req
	}
	getAction := func(token, surface, action string) string {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, request(http.MethodGet, "/api/stride/v1/mobile/surfaces/"+surface, token, "", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", surface, rr.Code, rr.Body.String())
		}
		var envelope struct {
			Items []struct {
				Actions []struct{ ID, Type string } `json:"actions"`
			} `json:"items"`
		}
		if json.Unmarshal(rr.Body.Bytes(), &envelope) != nil {
			t.Fatal("invalid projection JSON")
		}
		for _, item := range envelope.Items {
			for _, candidate := range item.Actions {
				if candidate.Type == action {
					return candidate.ID
				}
			}
		}
		t.Fatalf("action %s not minted on %s: %s", action, surface, rr.Body.String())
		return ""
	}
	profileAction := getAction(tokenZero, "profile", "profile-update")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodPost, "/api/stride/v1/mobile/actions/"+profileAction, tokenZero, "profile-key", map[string]any{"action": "profile-update", "surface": "profile", "expectedRevision": 1, "values": map[string]any{"displayName": "Zero Org"}}))
	if rr.Code != http.StatusOK {
		t.Fatalf("profile action status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodPost, "/api/stride/v1/mobile/actions/"+profileAction, tokenZero, "profile-key", map[string]any{"action": "profile-update", "surface": "profile", "expectedRevision": 1, "values": map[string]any{"displayName": "Zero Org"}}))
	if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("profile replay status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodPost, "/api/stride/v1/mobile/actions/"+profileAction, tokenZero, "different-profile-key", map[string]any{"action": "profile-update", "surface": "profile", "expectedRevision": 1, "values": map[string]any{"displayName": "Stale Reuse"}}))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("consumed action reuse status=%d body=%s", rr.Code, rr.Body.String())
	}
	organizationAction := getAction(tokenZero, "organizations", "organization-create")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodPost, "/api/stride/v1/mobile/actions/"+organizationAction, tokenZero, "organization-key", map[string]any{"action": "organization-create", "surface": "organizations", "expectedRevision": 1, "values": map[string]any{"name": "Zero Org Company", "slug": "zero-org-company"}}))
	if rr.Code != http.StatusOK || runtime.organization.ActiveMembershipCount("person-zero") != 1 {
		t.Fatalf("organization action status=%d memberships=%d body=%s", rr.Code, runtime.organization.ActiveMembershipCount("person-zero"), rr.Body.String())
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodPost, "/api/stride/v1/mobile/actions/"+organizationAction, tokenZero, "organization-key", map[string]any{"action": "organization-create", "surface": "organizations", "expectedRevision": 1, "values": map[string]any{"name": "Zero Org Company", "slug": "zero-org-company"}}))
	if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("organization replay status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
	runtime.organization.mu.RLock()
	organizationID := ""
	for _, membership := range runtime.organization.memberships {
		if membership.PersonID == "person-zero" {
			organizationID = membership.OrganizationID
		}
	}
	runtime.organization.mu.RUnlock()
	switchAction := getAction(tokenZero, "organizations", "organization-switch")
	runtime.mu.RLock()
	switchBinding := cloneStrideE10LiveActionBinding(runtime.actions[switchAction])
	runtime.mu.RUnlock()
	switchSession, switchAudit, switchBuildErr := runtime.buildOrganizationSwitch(context.WithValue(context.Background(), strideE10LiveSessionTokenKey{}, tokenZero), StrideE10ProductPrincipal{PersonID: "person-zero"}, StrideE10ProductCommand{IdempotencyKey: "switch-key"}, switchBinding, now)
	if switchBuildErr != nil || switchSession.Validate() != nil || switchAudit.Validate() != nil {
		t.Fatalf("invalid minted switch binding session=%+v audit=%+v err=%v sessionErr=%v auditErr=%v", switchSession, switchAudit, switchBuildErr, switchSession.Validate(), switchAudit.Validate())
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodPost, "/api/stride/v1/mobile/actions/"+switchAction, tokenZero, "switch-key", map[string]any{"action": "organization-switch", "surface": "organizations", "expectedRevision": 1, "values": map[string]any{}}))
	if rr.Code != http.StatusOK {
		t.Fatalf("switch action status=%d body=%s", rr.Code, rr.Body.String())
	}
	bound, ok := userSessionStore().lookupMemberRecordByHash(hashResetToken(tokenZero), now)
	if !ok || bound.ActiveOrganizationID != organizationID || bound.OrganizationMembershipRev != 1 || bound.ActiveOrganizationSessionRev != 1 {
		t.Fatalf("switch did not bind canonical session: %+v ok=%t", bound, ok)
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodPost, "/api/stride/v1/mobile/actions/"+switchAction, tokenZero, "switch-key", map[string]any{"action": "organization-switch", "surface": "organizations", "expectedRevision": 1, "values": map[string]any{}}))
	if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("switch replay status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
	// The only owner cannot be offered a leave action that the authority would
	// reject as a final-owner violation.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodGet, "/api/stride/v1/mobile/surfaces/organizations", tokenZero, "", nil))
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), `"type":"organization-leave"`) {
		t.Fatalf("final-owner leave was not suppressed status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := runtime.InstallJoinCodeAuthority("join-zero-company", organizationID); err != nil {
		t.Fatal(err)
	}
	joinAction := getAction(tokenJoin, "organizations", "organization-join")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodPost, "/api/stride/v1/mobile/actions/"+joinAction, tokenJoin, "join-key", map[string]any{"action": "organization-join", "surface": "organizations", "expectedRevision": 1, "values": map[string]any{"joinCode": "join-zero-company"}}))
	if rr.Code != http.StatusOK {
		t.Fatalf("join action status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodPost, "/api/stride/v1/mobile/actions/"+joinAction, tokenJoin, "join-key", map[string]any{"action": "organization-join", "surface": "organizations", "expectedRevision": 1, "values": map[string]any{"joinCode": "join-zero-company"}}))
	if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("join replay status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
	runtime.organization.mu.RLock()
	pending := 0
	for _, join := range runtime.organization.joinRequests {
		if join.PersonID == "person-join" && join.OrganizationID == organizationID && join.Status == "pending" {
			pending++
		}
	}
	runtime.organization.mu.RUnlock()
	if pending != 1 {
		t.Fatalf("pending joins=%d", pending)
	}
	for _, fixture := range []struct {
		revision int
		slug     string
	}{{2, "zero-org-two"}, {3, "zero-org-three"}} {
		createAction := getAction(tokenZero, "organizations", "organization-create")
		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, request(http.MethodPost, "/api/stride/v1/mobile/actions/"+createAction, tokenZero, "organization-key-"+fixture.slug, map[string]any{"action": "organization-create", "surface": "organizations", "expectedRevision": fixture.revision, "values": map[string]any{"name": "Capacity Company " + fixture.slug, "slug": fixture.slug}}))
		if rr.Code != http.StatusOK {
			t.Fatalf("capacity create revision=%d status=%d body=%s", fixture.revision, rr.Code, rr.Body.String())
		}
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request(http.MethodGet, "/api/stride/v1/mobile/surfaces/organizations", tokenZero, "", nil))
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), `"type":"organization-create"`) || strings.Contains(rr.Body.String(), `"type":"organization-join"`) {
		t.Fatalf("capacity actions were not suppressed status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestStrideE10ProductLiveFileOperationStoreReconcilesPreparedMutationAndRestartReplay(t *testing.T) {
	setupAuthTestEnv(t)
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "stride-w2-operations.json")
	keys := strideE10ProductTestKeys("operation-key", 1)
	operationStore, err := newStrideE10FileOperationStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	first := newStrideE10ProductLiveRuntimeWithStores(func() time.Time { return now }, newStrideE10MemoryPortableDeletionStore(), operationStore)
	personID := "person-operation-restart"
	principalContract := PersonPrincipal{Header: strideE10LiveHeader(STRIDEContractPersonPrincipal, STRIDEGlobalPersonTenant, personID, 1, personID+"-principal", now), AccountSubjectDigest: sha256Hex([]byte(personID + "-account")), Status: "active", RecoveryRevision: 1, CustodyRevision: 1}
	if err := first.organization.RegisterPerson(principalContract); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("d", 64)
	userSessionStore().mu.Lock()
	userSessionStore().sessions[hashResetToken(token)] = sessionRecord{Email: "restart@example.com", PersonID: personID, Expires: now.Add(24 * time.Hour)}
	userSessionStore().mu.Unlock()
	binding := StrideE10LiveActionBinding{ID: "action_restart_profile", Type: "profile-update", Surface: "profile", PersonID: personID, ExpectedRevision: 1, ExpiresAt: now.Add(time.Hour)}
	if err := first.BindAction(binding); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": "profile-update", "surface": "profile", "expectedRevision": 1, "values": map[string]any{"displayName": "Recovered Person"}})
	command := StrideE10ProductCommand{Operation: "identity.self_profile", Method: http.MethodPost, Path: "/api/stride/v1/mobile/actions/" + binding.ID, ResourceID: binding.ID, ExpectedRevision: 1, IdempotencyKey: "restart-key", Body: body}
	hydrated, err := first.hydrateLiveActionBinding(context.Background(), StrideE10ProductPrincipal{PersonID: personID}, command, binding, now)
	if err != nil {
		t.Fatal(err)
	}
	bindingJSON, _ := json.Marshal(hydrated)
	fingerprint := sha256Hex(append(append([]byte(nil), body...), bindingJSON...))
	operationKey := strideE10ProductOperationKey(personID, binding.ID, command.IdempotencyKey)
	if _, existed, err := operationStore.Prepare(StrideE10ProductOperationRecord{Key: operationKey, PersonID: personID, ActionID: binding.ID, IdempotencyKeyDigest: sha256Hex([]byte(command.IdempotencyKey)), Fingerprint: fingerprint, State: strideE10OperationPrepared, Binding: hydrated, PreparedAt: now}); err != nil || existed {
		t.Fatalf("prepare existed=%t err=%v", existed, err)
	}
	if _, err := first.executeBoundAction(context.Background(), StrideE10ProductPrincipal{PersonID: personID}, command, hydrated); err != nil {
		t.Fatal(err)
	}
	// Simulate process loss after W1 authority mutation but before the durable
	// commit marker or response projection.
	reloadedStore, err := newStrideE10FileOperationStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	second := newStrideE10ProductLiveRuntimeWithStores(func() time.Time { return now }, first.portableStore, reloadedStore)
	second.organization = first.organization
	second.setFeatureForTest(STRIDEFeaturePersonProfileAuthority, true)
	prior := strideE10LiveProductRuntime
	strideE10LiveProductRuntime = second
	t.Cleanup(func() { strideE10LiveProductRuntime = prior })
	mux := http.NewServeMux()
	registerStrideE10ProductLiveRoutes(mux)
	post := func(key string, payload any) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/"+binding.ID, key, payload)
		req.Header.Set("Authorization", "Bearer "+token)
		mux.ServeHTTP(rr, req)
		return rr
	}
	payload := map[string]any{"action": "profile-update", "surface": "profile", "expectedRevision": 1, "values": map[string]any{"displayName": "Recovered Person"}}
	rr := post("restart-key", payload)
	if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("prepared reconciliation status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
	thirdStore, err := newStrideE10FileOperationStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	third := newStrideE10ProductLiveRuntimeWithStores(func() time.Time { return now }, first.portableStore, thirdStore)
	third.organization = first.organization
	third.setFeatureForTest(STRIDEFeaturePersonProfileAuthority, true)
	strideE10LiveProductRuntime = third
	rr = post("restart-key", payload)
	if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("completed restart replay status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
	rr = post("restart-key", map[string]any{"action": "profile-update", "surface": "profile", "expectedRevision": 1, "values": map[string]any{"displayName": "Changed Body"}})
	if rr.Code != http.StatusConflict {
		t.Fatalf("changed body status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = post("different-key", payload)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("different key status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestStrideE10OrganizationSwitchPreparedLossReconcilesBothSessionStores(t *testing.T) {
	setupAuthTestEnv(t)
	now := time.Date(2026, 8, 8, 18, 10, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "switch-operations.json")
	keys := strideE10ProductTestKeys("switch-operation-key", 1)
	store, err := newStrideE10FileOperationStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	service, _, _, membership, _ := strideE10OrganizationProductFixture(t)
	runtime := newStrideE10ProductLiveRuntimeWithStores(func() time.Time { return now }, newStrideE10MemoryPortableDeletionStore(), store)
	runtime.organization = service
	for _, feature := range []STRIDEFeature{STRIDEFeaturePersonProfileAuthority, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureActiveOrganizationSession} {
		runtime.setFeatureForTest(feature, true)
	}
	token := strings.Repeat("9", 64)
	sessionHash := hashResetToken(token)
	email := "switch-restart@example.com"
	userSessionStore().mu.Lock()
	userSessionStore().sessions[sessionHash] = sessionRecord{Email: email, Expires: now.Add(7 * 24 * time.Hour), PersonID: membership.PersonID, AccountSubjectDigest: sha256Hex([]byte(email)), AuthorityGeneration: 1}
	userSessionStore().persistLocked()
	userSessionStore().mu.Unlock()
	if stored, ok := userSessionStore().lookupMemberRecordByHash(sessionHash, now); !ok || stored.PersonID != membership.PersonID || stored.AuthorityGeneration != 1 {
		t.Fatalf("prepared switch session unavailable before hydration: %+v ok=%t", stored, ok)
	}
	binding := StrideE10LiveActionBinding{
		ID: "action_switch_restart", Type: "organization-switch", Surface: "organizations", PersonID: membership.PersonID,
		ExpectedRevision: 1, ExpiresAt: now.Add(time.Hour),
		Target: STRIDEReference{ContractType: STRIDEContractOrganizationMembership, ID: membership.Header.ID, Revision: membership.Header.Revision, Digest: membership.Header.ContentDigest},
	}
	body, _ := json.Marshal(map[string]any{"action": binding.Type, "surface": binding.Surface, "expectedRevision": int64(1), "values": map[string]any{}})
	command := StrideE10ProductCommand{Operation: "session.switch_organization", Method: http.MethodPost, Path: "/api/stride/v1/mobile/actions/" + binding.ID, ResourceID: binding.ID, ExpectedRevision: 1, IdempotencyKey: "switch-restart-key", Body: body}
	principal := StrideE10ProductPrincipal{PersonID: membership.PersonID}
	ctx := context.WithValue(context.Background(), strideE10LiveSessionTokenKey{}, token)
	hydrated, err := runtime.hydrateLiveActionBinding(ctx, principal, command, binding, now)
	if err != nil {
		t.Fatal(err)
	}
	bindingJSON, _ := json.Marshal(hydrated)
	fingerprint := sha256Hex(append(append([]byte(nil), body...), bindingJSON...))
	operationKey := strideE10ProductOperationKey(principal.PersonID, binding.ID, command.IdempotencyKey)
	if _, existed, err := store.Prepare(StrideE10ProductOperationRecord{Key: operationKey, PersonID: principal.PersonID, ActionID: binding.ID, IdempotencyKeyDigest: sha256Hex([]byte(command.IdempotencyKey)), Fingerprint: fingerprint, State: strideE10OperationPrepared, Binding: hydrated, PreparedAt: now}); err != nil || existed {
		t.Fatalf("prepare existed=%t err=%v", existed, err)
	}
	// Simulate process loss after the organization authority store advanced but
	// before the durable login session was rebound.
	if err := runtime.organization.BindActiveSession(0, *hydrated.ActiveSession, *hydrated.AuditEvent); err != nil {
		t.Fatal(err)
	}
	if runtime.organizationSwitchPostimagesApplied(hydrated) {
		t.Fatal("organization-only postimage was treated as a completed switch")
	}
	reloaded, err := newStrideE10FileOperationStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newStrideE10ProductLiveRuntimeWithStores(func() time.Time { return now }, runtime.portableStore, reloaded)
	restarted.organization = runtime.organization
	for _, feature := range []STRIDEFeature{STRIDEFeaturePersonProfileAuthority, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureActiveOrganizationSession} {
		restarted.setFeatureForTest(feature, true)
	}
	prior := strideE10LiveProductRuntime
	strideE10LiveProductRuntime = restarted
	t.Cleanup(func() { strideE10LiveProductRuntime = prior })
	mux := http.NewServeMux()
	registerStrideE10ProductLiveRoutes(mux)
	rr := httptest.NewRecorder()
	req := strideE10Request(http.MethodPost, command.Path, command.IdempotencyKey, map[string]any{"action": binding.Type, "surface": binding.Surface, "expectedRevision": int64(1), "values": map[string]any{}})
	req.Header.Set("Authorization", "Bearer "+token)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("switch recovery status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
	if !restarted.organizationSwitchPostimagesApplied(hydrated) {
		t.Fatal("retry did not reconcile both exact switch postimages")
	}
	completedStore, err := newStrideE10FileOperationStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := completedStore.Load(operationKey)
	if err != nil || !found || record.State != strideE10OperationCompleted {
		t.Fatalf("restart completion record=%+v found=%t err=%v", record, found, err)
	}
}

func TestStrideE10ProductOperationStoreAuthenticatesTamperWrongKeyRotationAndFreshRestart(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	directory := t.TempDir()
	path := filepath.Join(directory, "operations.json")
	oldKeys := strideE10ProductTestKeys("old-operation-key", 1)
	store, err := newStrideE10FileOperationStore(path, oldKeys)
	if err != nil {
		t.Fatal(err)
	}
	record := StrideE10ProductOperationRecord{Key: sha256Hex([]byte("operation-record")), PersonID: "person-operation", ActionID: "action-operation", IdempotencyKeyDigest: sha256Hex([]byte("private-idempotency-key")), Fingerprint: sha256Hex([]byte("fingerprint")), State: strideE10OperationPrepared, Binding: StrideE10LiveActionBinding{ID: "action-operation", PersonID: "person-operation"}, PreparedAt: now}
	if _, existed, err := store.Prepare(record); err != nil || existed {
		t.Fatalf("prepare existed=%t err=%v", existed, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-idempotency-key") {
		t.Fatal("operation ledger persisted the raw idempotency key")
	}
	wrongKeys := strideE10ProductTestKeys("wrong-operation-key", 1)
	if _, err := newStrideE10FileOperationStore(path, wrongKeys); !errors.Is(err, ErrStrideE10Denied) {
		t.Fatalf("wrong key error=%v", err)
	}
	newKey := StrideE10ProductOperationMACKey{ID: "new-operation-key", Version: 2, Secret: []byte(strings.Repeat("new-operation-key/", 16))}
	rotating := &strideE10ProductTestKeyring{current: newKey, keys: map[string]StrideE10ProductOperationMACKey{oldKeys.current.ID: oldKeys.current, newKey.ID: newKey}}
	rotated, err := newStrideE10FileOperationStore(path, rotating)
	if err != nil {
		t.Fatal(err)
	}
	if err := rotated.Commit(record.Key, record.Fingerprint, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	newOnly := &strideE10ProductTestKeyring{current: newKey, keys: map[string]StrideE10ProductOperationMACKey{newKey.ID: newKey}}
	if _, err := newStrideE10FileOperationStore(path, newOnly); err != nil {
		t.Fatalf("rotated ledger did not reopen with new key: %v", err)
	}
	// A coherent JSON payload edit without a matching managed MAC is denied.
	var envelope strideE10ProductOperationEnvelope
	raw, _ = os.ReadFile(path)
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("invalid signed ledger")
	}
	var records map[string]StrideE10ProductOperationRecord
	if json.Unmarshal(envelope.Payload, &records) != nil {
		t.Fatal("invalid signed payload")
	}
	mutated := records[record.Key]
	mutated.State = strideE10OperationCompleted
	records[record.Key] = mutated
	envelope.Payload, _ = json.Marshal(records)
	tampered, _ := json.Marshal(envelope)
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newStrideE10FileOperationStore(path, newOnly); !errors.Is(err, ErrStrideE10Denied) {
		t.Fatalf("coherent tamper error=%v", err)
	}
	freshPath := filepath.Join(directory, "fresh.json")
	fresh, err := newStrideE10FileOperationStore(freshPath, newOnly)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := fresh.Load(record.Key); err != nil || found {
		t.Fatalf("fresh restart found=%t err=%v", found, err)
	}
}

func TestStrideE10ProductLiveRegisteredInitialDraftAndBlockActions(t *testing.T) {
	setupAuthTestEnv(t)
	fixture := newNetworkAuthorityFixture(t)
	fixture.service.mu.Lock()
	delete(fixture.service.profiles, fixture.profile.Header.ID)
	fixture.service.profileVersions = map[string]NetworkProfileProjection{}
	fixture.service.idempotency = map[string]networkIdempotencyRecord{}
	fixture.service.mu.Unlock()
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return fixture.now.Add(5 * time.Minute) })
	runtime.network = fixture.service
	runtime.setFeatureForTest(STRIDEFeatureWorkRecordPrivate, true)
	runtime.setFeatureForTest(STRIDEFeatureNetworkContact, true)
	token := strings.Repeat("e", 64)
	userSessionStore().mu.Lock()
	userSessionStore().sessions[hashResetToken(token)] = sessionRecord{Email: "candidate@example.com", PersonID: fixture.publication.SubjectPersonID, Expires: fixture.now.Add(24 * time.Hour)}
	userSessionStore().mu.Unlock()
	prior := strideE10LiveProductRuntime
	strideE10LiveProductRuntime = runtime
	t.Cleanup(func() { strideE10LiveProductRuntime = prior })
	mux := http.NewServeMux()
	registerStrideE10ProductLiveRoutes(mux)
	request := func(method, path, key string, body any) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := strideE10Request(method, path, key, body)
		req.Header.Set("Authorization", "Bearer "+token)
		mux.ServeHTTP(rr, req)
		return rr
	}
	draftProjection := request(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-draft", "", nil)
	draftAction := strideE10ActionFromProjection(t, draftProjection, "network-draft-save")
	draftPayload := map[string]any{"action": "network-draft-save", "surface": "network-draft", "expectedRevision": 1, "values": map[string]any{"intro": "I help teams solve growth problems."}}
	rr := request(http.MethodPost, "/api/stride/v1/mobile/actions/"+draftAction, "initial-draft-key", draftPayload)
	if rr.Code != http.StatusOK {
		t.Fatalf("initial draft status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = request(http.MethodPost, "/api/stride/v1/mobile/actions/"+draftAction, "initial-draft-key", draftPayload)
	if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("initial draft replay status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
	fixture.service.mu.Lock()
	var drafted NetworkProfileProjection
	for _, profile := range fixture.service.profiles {
		if profile.SubjectPersonID == fixture.publication.SubjectPersonID {
			drafted = cloneNetworkProjection(profile)
		}
	}
	fixture.service.mu.Unlock()
	if drafted.State != "draft" || drafted.Discoverability != "unlisted" {
		t.Fatalf("initial draft not stored: %+v", drafted)
	}
	publishedDraft := cloneNetworkProjection(drafted)
	publishedDraft.Header = nextAuthorityHeader(publishedDraft.Header, "publish-for-block", fixture.now.Add(6*time.Minute))
	publishedDraft.State, publishedDraft.Discoverability, publishedDraft.StateChangedAt = "published", "signed_in_network", fixture.now.Add(6*time.Minute)
	if stored, _, _, err := fixture.service.PutProfile(fixture.personController, publishedDraft, drafted.Header.Revision, sha256Hex([]byte("publish-for-block"))); err != nil {
		t.Fatal(err)
	} else {
		publishedDraft = stored
	}

	// Create one eligible visible contact, then prove the block action target is
	// bound to that contact rather than accepted from client values.
	admission := NetworkContactAdmission{GrantRef: referenceFromHeader(fixture.grant.Header), SenderPersonID: fixture.grant.SearcherPersonID, SenderOrganizationID: fixture.grant.OrganizationID, MembershipID: fixture.grant.MembershipID, MembershipRevision: fixture.grant.MembershipRevision, RecipientProjection: referenceFromHeader(publishedDraft.Header), Purpose: "discuss_growth", NoteDigest: sha256Hex([]byte("block-note")), CollaborationType: "collaboration", ExpiresAt: fixture.now.Add(12 * time.Hour), IdempotencyKeyDigest: sha256Hex([]byte("block-contact")), At: fixture.now.Add(7 * time.Minute)}
	contact, _, err := fixture.service.CreateContact(admission)
	if err != nil {
		t.Fatal(err)
	}
	blockProjection := request(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-blocks", "", nil)
	blockAction := strideE10ActionFromProjection(t, blockProjection, "network-block")
	fixture.service.mu.Lock()
	currentContact := fixture.service.contacts[contact.Header.ID]
	staleContact := cloneContactRequest(currentContact)
	staleContact.Header = nextAuthorityHeader(staleContact.Header, "stale-test", fixture.now.Add(7*time.Minute))
	fixture.service.contacts[contact.Header.ID] = staleContact
	fixture.service.mu.Unlock()
	blockPayload := map[string]any{"action": "network-block", "surface": "network-blocks", "expectedRevision": contact.Header.Revision, "values": map[string]any{}}
	rr = request(http.MethodPost, "/api/stride/v1/mobile/actions/"+blockAction, "initial-block-key", blockPayload)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("stale block status=%d body=%s", rr.Code, rr.Body.String())
	}
	fixture.service.mu.Lock()
	fixture.service.contacts[contact.Header.ID] = currentContact
	fixture.service.mu.Unlock()
	rr = request(http.MethodPost, "/api/stride/v1/mobile/actions/"+blockAction, "initial-block-key", blockPayload)
	if rr.Code != http.StatusOK {
		t.Fatalf("initial block status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = request(http.MethodPost, "/api/stride/v1/mobile/actions/"+blockAction, "initial-block-key", blockPayload)
	if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("initial block replay status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
}

func TestStrideE10ProductLiveRegisteredNetworkLifecycleActionMatrix(t *testing.T) {
	fixtures := []struct {
		action   string
		revision int64
		values   map[string]any
	}{
		{"network-pause", 2, map[string]any{}},
		{"network-profile-off", 2, map[string]any{}},
		{"network-searchable-fields-update", 2, map[string]any{"fields": []any{"display_name", "contribution_problem_classes"}}},
		{"network-profile-delete", 2, map[string]any{}},
		{"network-profile-export", 1, map[string]any{}},
	}
	for _, test := range fixtures {
		t.Run(test.action, func(t *testing.T) {
			setupAuthTestEnv(t)
			fixture := newNetworkAuthorityFixture(t)
			runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return fixture.now.Add(5 * time.Minute) })
			runtime.network = fixture.service
			runtime.setFeatureForTest(STRIDEFeatureWorkRecordPrivate, true)
			directProjection, directErr := runtime.project(StrideE10ProductPrincipal{PersonID: fixture.profile.SubjectPersonID}, "network-preview")
			if directErr != nil || strideE10ValidateMobileProjection(directProjection, "network-preview") != nil {
				encoded, _ := json.Marshal(directProjection)
				t.Fatalf("invalid direct preview err=%v value=%s", directErr, encoded)
			}
			token := strings.Repeat(string(rune('f'+len(test.action)%10)), 64)
			userSessionStore().mu.Lock()
			userSessionStore().sessions[hashResetToken(token)] = sessionRecord{Email: test.action + "@example.com", PersonID: fixture.profile.SubjectPersonID, Expires: fixture.now.Add(24 * time.Hour)}
			userSessionStore().mu.Unlock()
			prior := strideE10LiveProductRuntime
			strideE10LiveProductRuntime = runtime
			t.Cleanup(func() { strideE10LiveProductRuntime = prior })
			mux := http.NewServeMux()
			registerStrideE10ProductLiveRoutes(mux)
			call := func(method, path, key string, body any) *httptest.ResponseRecorder {
				rr := httptest.NewRecorder()
				req := strideE10Request(method, path, key, body)
				req.Header.Set("Authorization", "Bearer "+token)
				mux.ServeHTTP(rr, req)
				return rr
			}
			projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-preview", "", nil)
			actionID := strideE10ActionFromProjection(t, projection, test.action)
			payload := map[string]any{"action": test.action, "surface": "network-preview", "expectedRevision": test.revision, "values": test.values}
			runtime.setFeatureForTest(STRIDEFeatureWorkRecordPrivate, false)
			rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "matrix-key", payload)
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("parent-off status=%d body=%s", rr.Code, rr.Body.String())
			}
			runtime.setFeatureForTest(STRIDEFeatureWorkRecordPrivate, true)
			rr = call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "matrix-key", payload)
			if rr.Code != http.StatusOK {
				t.Fatalf("normal status=%d body=%s", rr.Code, rr.Body.String())
			}
			rr = call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "matrix-key", payload)
			if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
				t.Fatalf("replay status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
			}
		})
	}
}

func TestStrideE10ProductLiveTypedWorkRecordAndPublishedNetworkDetails(t *testing.T) {
	now := time.Date(2026, 8, 8, 20, 0, 0, 0, time.UTC)
	profile := PersonProfile{OpenToEnabled: true, OpenTo: []string{"advisory"}}
	identity := StrideE10OrganizationSelfView{
		Profile:       &profile,
		Organizations: []Organization{{Header: STRIDEContractHeader{ID: "org_visible"}, Name: "Visible Org"}},
		Memberships:   []OrganizationMembership{{Header: STRIDEContractHeader{ID: "membership_visible"}, OrganizationID: "org_visible", Role: "member", Status: "active"}},
	}
	attestationHeader := STRIDEContractHeader{ID: "attestation_visible", Revision: 1, ContentDigest: strings.Repeat("a", 64)}
	claimHeader := STRIDEContractHeader{ID: "claim_visible", Revision: 1, ContentDigest: strings.Repeat("b", 64)}
	view := StrideE10ContributionView{
		Claims:       []ContributionClaim{{Header: claimHeader, State: "verified", ProblemClass: "growth", OutcomeClass: "retention", ContributionKind: "delivered"}},
		Attestations: []ContributionAttestation{{Header: attestationHeader, Claim: refForHeader(claimHeader), State: "active", VerificationTier: "organization_verified_opaque"}},
		Publications: []PublishedContributionClaim{{Header: STRIDEContractHeader{ID: "publication_visible"}, State: "published", Attestations: []STRIDEReference{refForHeader(attestationHeader)}}},
		Influences:   []AgentInfluenceReceipt{{Header: STRIDEContractHeader{ID: "influence_visible"}, State: "verified"}},
	}
	items := strideE10WorkRecordProjectionItems(view, identity, func(string, string) bool { return true })
	sections := map[string]map[string]any{}
	for _, item := range items {
		if item["kind"] == "work-record-section" {
			detail := item["detail"].(map[string]any)
			sections[detail["section"].(string)] = detail
		}
	}
	for _, section := range []string{"problems-outcomes", "how-i-contribute", "organizations-roles", "work-evidence", "people-agents-helped", "open-to"} {
		if sections[section] == nil {
			t.Fatalf("missing work record section %q: %+v", section, sections)
		}
	}
	if sections["open-to"]["openToEnabled"] != true || !reflect.DeepEqual(sections["open-to"]["entries"], []string{"advisory"}) || !reflect.DeepEqual(sections["organizations-roles"]["entries"], []string{"Visible Org / member"}) || !reflect.DeepEqual(sections["work-evidence"]["entries"], []string{"growth / retention — delivered"}) || !reflect.DeepEqual(sections["people-agents-helped"]["entries"], []string{"Verified agent influence"}) {
		t.Fatalf("work record authority values drifted: %+v", sections)
	}

	fixture := newNetworkAuthorityFixture(t)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	runtime.network = fixture.service
	for _, surface := range []string{"network-preview", "network-recruiter-view"} {
		projected, err := runtime.authorityProjectionItems(StrideE10ProductPrincipal{PersonID: fixture.profile.SubjectPersonID}, surface, "")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range projected {
			if item["kind"] != "network-profile-detail" {
				continue
			}
			detail := item["detail"].(map[string]any)
			if detail["displayName"] != "Candidate" || !reflect.DeepEqual(detail["workModes"], []any{"async"}) || detail["bio"] != nil || detail["email"] != nil {
				t.Fatalf("%s network detail=%+v", surface, detail)
			}
			found = true
		}
		if !found {
			t.Fatalf("%s omitted actual published profile detail: %+v", surface, projected)
		}
	}

	withdrawn := view
	withdrawn.Publications = []PublishedContributionClaim{{Header: STRIDEContractHeader{ID: "publication_visible"}, State: "withdrawn", StateChangedAt: now}}
	withdrawnItems := strideE10WorkRecordProjectionItems(withdrawn, identity, func(string, string) bool { return true })
	for _, item := range withdrawnItems {
		if item["kind"] == "contribution-evidence" {
			t.Fatalf("withdrawn evidence rendered: %+v", item)
		}
	}

	fixture.service.mu.Lock()
	fixture.service.searchWindows["person:"+fixture.grant.SearcherPersonID] = []networkTimedSearch{{At: now.Add(-40 * time.Minute)}, {At: now.Add(-10 * time.Minute)}}
	fixture.service.searchWindows["organization:"+fixture.grant.OrganizationID] = []networkTimedSearch{{At: now.Add(-30 * time.Minute)}}
	fixture.service.searchWindows["global"] = []networkTimedSearch{{At: now.Add(-20 * time.Minute)}}
	fixture.service.contactWindows["person:"+fixture.grant.SearcherPersonID] = []time.Time{now.Add(-20 * time.Hour)}
	fixture.service.contactWindows["organization:"+fixture.grant.OrganizationID] = []time.Time{now.Add(-12 * time.Hour)}
	fixture.service.contactWindows["global"] = []time.Time{now.Add(-4 * time.Hour)}
	fixture.service.mu.Unlock()
	usage := runtime.currentRecruitingUsage(fixture.grant, now)
	if usage.personSearch != 2 || !usage.personSearchEnds.Equal(now.Add(20*time.Minute)) || !usage.organizationSearchEnds.Equal(now.Add(30*time.Minute)) || !usage.globalSearchEnds.Equal(now.Add(40*time.Minute)) || !usage.personContactEnds.Equal(now.Add(4*time.Hour)) || !usage.organizationContactEnds.Equal(now.Add(12*time.Hour)) || !usage.globalContactEnds.Equal(now.Add(20*time.Hour)) {
		t.Fatalf("staggered recruiting windows are synthetic: %+v", usage)
	}
}

func TestStrideE10ProductLiveRegisteredContributionLifecycleActionMatrix(t *testing.T) {
	t.Run("subject-approve", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newContributionAuthorityFixture(t)
		if _, err := fixture.service.CreateClaim(candidateClaim(), authorityAssertion(fixture.org, 0, "matrix-create", contributionAuthorityTime)); err != nil {
			t.Fatal(err)
		}
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(time.Minute) })
		runtime.contribution = fixture.service
		runtime.setFeatureForTest(STRIDEFeatureWorkRecordPrivate, true)
		runtime.setFeatureForTest(STRIDEFeatureContributionReview, true)
		call := strideE10MountRegisteredRuntime(t, runtime, fixture.subject.PersonID, "", "", 0)
		projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/work-record", "", nil)
		actionID, revision := strideE10ActionRevisionFromProjection(t, projection, "contribution-subject-approve")
		payload := map[string]any{"action": "contribution-subject-approve", "surface": "work-record", "expectedRevision": revision, "values": map[string]any{}}
		runtime.setFeatureForTest(STRIDEFeatureContributionReview, false)
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "subject-key", payload); rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("parent-off status=%d body=%s", rr.Code, rr.Body.String())
		}
		runtime.setFeatureForTest(STRIDEFeatureContributionReview, true)
		rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "subject-key", payload)
		if rr.Code != http.StatusOK {
			t.Fatalf("normal status=%d body=%s", rr.Code, rr.Body.String())
		}
		if replay := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "subject-key", payload); replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
		}
	})
	t.Run("subject-dispute", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newContributionAuthorityFixture(t)
		created, err := fixture.service.CreateClaim(candidateClaim(), authorityAssertion(fixture.org, 0, "matrix-dispute-create", contributionAuthorityTime))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.service.SubjectReview(created.Header.ID, false, authorityAssertion(fixture.subject, created.Header.Revision, "matrix-dispute-review", contributionAuthorityTime.Add(time.Minute))); err != nil {
			t.Fatal(err)
		}
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(2 * time.Minute) })
		runtime.contribution = fixture.service
		runtime.setFeatureForTest(STRIDEFeatureWorkRecordPrivate, true)
		runtime.setFeatureForTest(STRIDEFeatureContributionReview, true)
		call := strideE10MountRegisteredRuntime(t, runtime, fixture.subject.PersonID, "", "", 0)
		projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/work-record", "", nil)
		actionID, revision := strideE10ActionRevisionFromProjection(t, projection, "contribution-subject-dispute")
		payload := map[string]any{"action": "contribution-subject-dispute", "surface": "work-record", "expectedRevision": revision, "values": map[string]any{"reason": "Evidence needs correction"}}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "dispute-key", payload); rr.Code != http.StatusOK {
			t.Fatalf("dispute status=%d body=%s", rr.Code, rr.Body.String())
		}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "dispute-key", payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("dispute replay status=%d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("contribution-publish", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newContributionAuthorityFixture(t)
		claim := createVerifiedClaim(t, fixture)
		attestation, publication := issuePublishedContribution(t, fixture, claim)
		fixture.service.mu.Lock()
		delete(fixture.service.publications, publication.Header.ID)
		fixture.service.mu.Unlock()
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(10 * time.Minute) })
		runtime.contribution = fixture.service
		for _, feature := range []STRIDEFeature{STRIDEFeatureWorkRecordPrivate, STRIDEFeatureNetworkProfilePublication, STRIDEFeatureContributionReview} {
			runtime.setFeatureForTest(feature, true)
		}
		call := strideE10MountRegisteredRuntime(t, runtime, fixture.subject.PersonID, "", "", 0)
		projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/work-record", "", nil)
		actionID, revision := strideE10ActionRevisionFromProjection(t, projection, "contribution-publish")
		if revision != attestation.Header.Revision {
			t.Fatalf("publish revision=%d want=%d", revision, attestation.Header.Revision)
		}
		payload := map[string]any{"action": "contribution-publish", "surface": "work-record", "expectedRevision": revision, "values": map[string]any{}}
		runtime.setFeatureForTest(STRIDEFeatureNetworkProfilePublication, false)
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "publish-key", payload); rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("publish parent-off status=%d body=%s", rr.Code, rr.Body.String())
		}
		runtime.setFeatureForTest(STRIDEFeatureNetworkProfilePublication, true)
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "publish-key", payload); rr.Code != http.StatusOK {
			t.Fatalf("publish status=%d body=%s", rr.Code, rr.Body.String())
		}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "publish-key", payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("publish replay status=%d body=%s", rr.Code, rr.Body.String())
		}
	})

	for _, action := range []string{"contribution-organization-approve", "contribution-organization-deny"} {
		t.Run(action, func(t *testing.T) {
			setupAuthTestEnv(t)
			fixture := newContributionAuthorityFixture(t)
			claim := createVerifiedClaim(t, fixture)
			approval := FieldReleaseApproval{Header: contributionNetworkHeader(STRIDEContractFieldReleaseApproval, "approval_matrix_"+strings.TrimPrefix(action, "contribution-organization-"), claim.OrganizationID), OrganizationID: claim.OrganizationID, SubjectPersonID: claim.SubjectPersonID, Attestation: contributionNetworkRef(STRIDEContractContributionAttestation, "attestation_matrix"), FieldKey: "outcome", FieldValueDigest: authorityDigest("matrix-value"), Source: claim.SourceRefs[0], SourceConsentRevision: claim.ConsentRevision, SourceACLRevision: claim.ACLRevision, SourcePurgeGeneration: claim.PurgeGeneration, Visibility: "signed_in_network", ApproverRole: "organization", Controller: fixture.org.Controller, State: "pending", StateChangedAt: contributionAuthorityTime.Add(4 * time.Minute)}
			if _, err := fixture.service.PutFieldApproval(approval, authorityAssertion(fixture.org, 0, "put-"+action, contributionAuthorityTime.Add(4*time.Minute))); err != nil {
				t.Fatal(err)
			}
			runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(5 * time.Minute) })
			runtime.contribution = fixture.service
			for _, feature := range []STRIDEFeature{STRIDEFeatureContributionReview, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite} {
				runtime.setFeatureForTest(feature, true)
			}
			personID, organizationID, membershipID := fixture.org.Controller.PrincipalID, fixture.org.OrganizationID, "membership-org-decision"
			runtime.organization.memberships[membershipID] = OrganizationMembership{Header: STRIDEContractHeader{ID: membershipID, Revision: 1}, PersonID: personID, OrganizationID: organizationID, Role: "owner", Status: "active"}
			call := strideE10MountRegisteredRuntime(t, runtime, personID, organizationID, membershipID, 1)
			projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/contribution-approvals", "", nil)
			actionID, revision := strideE10ActionRevisionFromProjection(t, projection, action)
			payload := map[string]any{"action": action, "surface": "contribution-approvals", "expectedRevision": revision, "values": map[string]any{}}
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusOK {
				t.Fatalf("decision status=%d body=%s", rr.Code, rr.Body.String())
			}
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
				t.Fatalf("decision replay status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}

	for _, action := range []string{"contribution-withdraw", "contribution-correct", "contribution-revoke"} {
		t.Run(action, func(t *testing.T) {
			setupAuthTestEnv(t)
			fixture := newContributionAuthorityFixture(t)
			claim := createVerifiedClaim(t, fixture)
			if action == "contribution-withdraw" {
				_, _ = issuePublishedContribution(t, fixture, claim)
			}
			runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(10 * time.Minute) })
			runtime.contribution = fixture.service
			for _, feature := range []STRIDEFeature{STRIDEFeatureWorkRecordPrivate, STRIDEFeatureContributionReview, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite} {
				runtime.setFeatureForTest(feature, true)
			}
			personID, surface, organizationID, membershipID, membershipRevision := fixture.subject.PersonID, "work-record", "", "", int64(0)
			if action != "contribution-withdraw" {
				personID, surface, organizationID, membershipID, membershipRevision = fixture.org.Controller.PrincipalID, "contribution-approvals", fixture.org.OrganizationID, "membership-reviewer", 1
				runtime.organization.persons[personID] = PersonPrincipal{Status: "active"}
				runtime.organization.organizations[organizationID] = Organization{Status: "active"}
				runtime.organization.memberships[membershipID] = OrganizationMembership{Header: STRIDEContractHeader{ID: membershipID, Revision: 1}, PersonID: personID, OrganizationID: organizationID, Role: "owner", Status: "active"}
			}
			call := strideE10MountRegisteredRuntime(t, runtime, personID, organizationID, membershipID, membershipRevision)
			projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/"+surface, "", nil)
			actionID, revision := strideE10ActionRevisionFromProjection(t, projection, action)
			values := map[string]any{}
			if action == "contribution-correct" || action == "contribution-revoke" {
				values["reason"] = "Correct governed record"
			}
			payload := map[string]any{"action": action, "surface": surface, "expectedRevision": revision, "values": values}
			rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload)
			if rr.Code != http.StatusOK {
				t.Fatalf("normal status=%d body=%s", rr.Code, rr.Body.String())
			}
			replay := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload)
			if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" {
				t.Fatalf("replay status=%d replay=%q body=%s", replay.Code, replay.Header().Get("Idempotency-Replayed"), replay.Body.String())
			}
		})
	}
}

func TestStrideE10ProductLiveRegisteredOrganizationGovernanceActionMatrix(t *testing.T) {
	for _, action := range []string{"organization-request-approve", "organization-request-deny", "organization-member-role-change", "organization-member-revoke", "organization-ownership-transfer", "organization-leave"} {
		t.Run(action, func(t *testing.T) {
			setupAuthTestEnv(t)
			service, organization, owner, member, _ := strideE10OrganizationProductFixture(t)
			runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return organization.UpdatedAt.Add(10 * time.Minute) })
			runtime.organization = service
			for _, feature := range []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeaturePersonProfileAuthority} {
				runtime.setFeatureForTest(feature, true)
			}
			personID, membershipID, membershipRevision, surface := owner.PersonID, owner.Header.ID, owner.Header.Revision, "organization-people"
			values := map[string]any{}
			if strings.HasPrefix(action, "organization-request-") {
				surface = "organization-requests"
			}
			if action == "organization-member-role-change" {
				values["role"] = "admin"
			}
			if action == "organization-leave" {
				personID, membershipID, membershipRevision, surface = member.PersonID, member.Header.ID, member.Header.Revision, "organizations"
			}
			call := strideE10MountRegisteredRuntime(t, runtime, personID, organization.Header.ID, membershipID, membershipRevision)
			projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/"+surface, "", nil)
			actionID, revision := strideE10ActionRevisionFromProjection(t, projection, action)
			payload := map[string]any{"action": action, "surface": surface, "expectedRevision": revision, "values": values}
			runtime.setFeatureForTest(STRIDEFeatureOrganizationAuthorityWrite, false)
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("parent-off status=%d body=%s", rr.Code, rr.Body.String())
			}
			runtime.setFeatureForTest(STRIDEFeatureOrganizationAuthorityWrite, true)
			rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload)
			if rr.Code != http.StatusOK && rr.Code != http.StatusConflict && rr.Code != http.StatusNotFound {
				t.Fatalf("normal status=%d body=%s", rr.Code, rr.Body.String())
			}
			if rr.Code == http.StatusOK && action != "organization-leave" {
				replay := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload)
				if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" {
					t.Fatalf("replay status=%d replay=%q body=%s", replay.Code, replay.Header().Get("Idempotency-Replayed"), replay.Body.String())
				}
			}
			switch action {
			case "organization-member-role-change":
				stored, _ := service.Membership(member.Header.ID)
				if stored.Role != "admin" {
					t.Fatalf("role not changed: %+v", stored)
				}
			case "organization-member-revoke":
				stored, _ := service.Membership(member.Header.ID)
				if stored.Status != "revoked" {
					t.Fatalf("membership not revoked: %+v", stored)
				}
			case "organization-ownership-transfer":
				stored, _ := service.Membership(member.Header.ID)
				if stored.Role != "owner" {
					t.Fatalf("ownership not transferred: %+v", stored)
				}
			case "organization-leave":
				stored, _ := service.Membership(member.Header.ID)
				if stored.Status != "departed" {
					t.Fatalf("membership not departed: %+v", stored)
				}
			}
		})
	}
}

func TestStrideE10ProductLiveRegisteredSearchContactAndRecruitingActionMatrix(t *testing.T) {
	t.Run("search-and-contact-send", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newNetworkAuthorityFixture(t)
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return fixture.now.Add(5 * time.Minute) })
		runtime.network = fixture.service
		policyAuthority, keys, policy := w6TestPolicyAuthority(t, fixture.now.Add(5*time.Minute))
		policy.Revision = fixture.grant.PolicyRevision
		policy, _ = SignW6NetworkPolicy(context.Background(), keys, policy)
		policyAuthority = NewW6NetworkPolicyAuthority(keys)
		if err := policyAuthority.Install(context.Background(), policy); err != nil {
			t.Fatal(err)
		}
		qualification := w6TestQualificationAuthority(t, keys, policy, fixture.now.Add(5*time.Minute))
		if err := fixture.service.ConfigureW6Qualification(policyAuthority, qualification, w6TestShadowForFixture(fixture), "cohort_pilot"); err != nil {
			t.Fatal(err)
		}
		personID, organizationID, membershipID, revision := fixture.grant.SearcherPersonID, fixture.grant.OrganizationID, fixture.grant.MembershipID, fixture.grant.MembershipRevision
		runtime.organization.memberships[membershipID] = OrganizationMembership{Header: STRIDEContractHeader{ID: membershipID, Revision: revision}, PersonID: personID, OrganizationID: organizationID, Role: "member", Status: "active"}
		for _, feature := range []STRIDEFeature{STRIDEFeatureNetworkProfilePublication, STRIDEFeatureNetworkProjectionShadow, STRIDEFeatureNetworkSearch, STRIDEFeatureNetworkContact} {
			runtime.setFeatureForTest(feature, true)
		}
		call := strideE10MountRegisteredRuntime(t, runtime, personID, organizationID, membershipID, revision)
		projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-search", "", nil)
		searchID, searchRevision := strideE10ActionRevisionFromProjection(t, projection, "network-search-propose")
		searchPayload := map[string]any{"action": "network-search-propose", "surface": "network-search", "expectedRevision": searchRevision, "values": map[string]any{"query": "problem_class:growth"}}
		runtime.setFeatureForTest(STRIDEFeatureNetworkSearch, false)
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+searchID, "search-key", searchPayload); rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("search parent-off status=%d body=%s", rr.Code, rr.Body.String())
		}
		runtime.setFeatureForTest(STRIDEFeatureNetworkSearch, true)
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+searchID, "search-key", searchPayload); rr.Code != http.StatusOK {
			t.Fatalf("search status=%d body=%s", rr.Code, rr.Body.String())
		}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+searchID, "search-key", searchPayload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("search replay status=%d body=%s", rr.Code, rr.Body.String())
		}
		projection = call(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-search", "", nil)
		confirmID, confirmRevision := strideE10ActionRevisionFromProjection(t, projection, "network-search-confirm")
		confirmPayload := map[string]any{"action": "network-search-confirm", "surface": "network-search", "expectedRevision": confirmRevision, "values": map[string]any{}}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+confirmID, "confirm-key", confirmPayload); rr.Code != http.StatusOK {
			t.Fatalf("confirm status=%d body=%s", rr.Code, rr.Body.String())
		}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+confirmID, "confirm-key", confirmPayload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("confirm replay status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
		}
		projection = call(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-search", "", nil)
		contactID, contactRevision := strideE10ActionRevisionFromProjection(t, projection, "contact-send")
		contactPayload := map[string]any{"action": "contact-send", "surface": "network-search", "expectedRevision": contactRevision, "values": map[string]any{"purpose": "Discuss growth work", "note": "Would you be open to comparing approaches?", "collaborationType": "collaboration"}}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+contactID, "contact-send-key", contactPayload); rr.Code != http.StatusOK {
			t.Fatalf("contact send status=%d body=%s", rr.Code, rr.Body.String())
		}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+contactID, "contact-send-key", contactPayload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("contact send replay status=%d body=%s", rr.Code, rr.Body.String())
		}
		fixture.service.mu.Lock()
		withdrawn := fixture.service.publications[fixture.publication.Header.ID]
		withdrawn.State, withdrawn.Visibility = "withdrawn", "private"
		fixture.service.publications[withdrawn.Header.ID] = withdrawn
		fixture.service.mu.Unlock()
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+confirmID, "confirm-key", confirmPayload); rr.Code != http.StatusNotFound {
			t.Fatalf("revoked publication leaked completed search replay: status=%d body=%s", rr.Code, rr.Body.String())
		}
	})

	for _, action := range []string{"contact-accept", "contact-decline", "contact-withdraw"} {
		t.Run(action, func(t *testing.T) {
			setupAuthTestEnv(t)
			fixture := newNetworkAuthorityFixture(t)
			admission := NetworkContactAdmission{GrantRef: referenceFromHeader(fixture.grant.Header), SenderPersonID: fixture.grant.SearcherPersonID, SenderOrganizationID: fixture.grant.OrganizationID, MembershipID: fixture.grant.MembershipID, MembershipRevision: fixture.grant.MembershipRevision, RecipientProjection: referenceFromHeader(fixture.profile.Header), Purpose: "contact_matrix", NoteDigest: sha256Hex([]byte(action)), CollaborationType: "collaboration", ExpiresAt: fixture.now.Add(12 * time.Hour), IdempotencyKeyDigest: sha256Hex([]byte("create-" + action)), At: fixture.now.Add(3 * time.Minute)}
			contact, _, err := fixture.service.CreateContact(admission)
			if err != nil {
				t.Fatal(err)
			}
			runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return fixture.now.Add(4 * time.Minute) })
			runtime.network = fixture.service
			runtime.setFeatureForTest(STRIDEFeatureNetworkContact, true)
			personID, organizationID, membershipID, revision := contact.RecipientPersonID, "", "", int64(0)
			if action == "contact-withdraw" {
				personID, organizationID, membershipID, revision = fixture.grant.SearcherPersonID, fixture.grant.OrganizationID, fixture.grant.MembershipID, fixture.grant.MembershipRevision
				runtime.organization.memberships[membershipID] = OrganizationMembership{Header: STRIDEContractHeader{ID: membershipID, Revision: revision}, PersonID: personID, OrganizationID: organizationID, Role: "member", Status: "active"}
			}
			call := strideE10MountRegisteredRuntime(t, runtime, personID, organizationID, membershipID, revision)
			projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/contact-inbox", "", nil)
			actionID, expected := strideE10ActionRevisionFromProjection(t, projection, action)
			payload := map[string]any{"action": action, "surface": "contact-inbox", "expectedRevision": expected, "values": map[string]any{}}
			runtime.setFeatureForTest(STRIDEFeatureNetworkContact, false)
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("decision parent-off status=%d body=%s", rr.Code, rr.Body.String())
			}
			runtime.setFeatureForTest(STRIDEFeatureNetworkContact, true)
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusOK {
				t.Fatalf("decision status=%d body=%s", rr.Code, rr.Body.String())
			}
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
				t.Fatalf("decision replay status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}

	for _, action := range []string{"organization-recruiting-grant-create", "organization-recruiting-grant-revoke"} {
		t.Run(action, func(t *testing.T) {
			setupAuthTestEnv(t)
			fixture := newNetworkAuthorityFixture(t)
			runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return fixture.now.Add(5 * time.Minute) })
			runtime.network = fixture.service
			authority := fixture.capabilityAuthority
			runtime.organization.memberships[authority.MembershipID] = OrganizationMembership{Header: STRIDEContractHeader{ID: authority.MembershipID, Revision: authority.MembershipRevision}, PersonID: authority.ControllerPersonID, OrganizationID: authority.OrganizationID, Role: "owner", Status: "active"}
			if action == "organization-recruiting-grant-create" {
				target := organizationTestMembership("membership_recruiting_target", "person_recruiting_target", authority.OrganizationID, "member", 1, fixture.now.Add(time.Minute), authority.MembershipID)
				runtime.organization.memberships[target.Header.ID] = target
				if err := runtime.network.InstallMembershipAuthority(NetworkMembershipAuthority{MembershipID: target.Header.ID, OrganizationID: target.OrganizationID, PersonID: target.PersonID, Revision: target.Header.Revision, Active: true}); err != nil {
					t.Fatal(err)
				}
			}
			for _, feature := range []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite} {
				runtime.setFeatureForTest(feature, true)
			}
			call := strideE10MountRegisteredRuntime(t, runtime, authority.ControllerPersonID, authority.OrganizationID, authority.MembershipID, authority.MembershipRevision)
			projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/organization-recruiting", "", nil)
			actionID, expected := strideE10ActionRevisionFromProjection(t, projection, action)
			payload := map[string]any{"action": action, "surface": "organization-recruiting", "expectedRevision": expected, "values": map[string]any{}}
			if action == "organization-recruiting-grant-revoke" {
				payload["values"] = map[string]any{"reason": "Access no longer required"}
			} else {
				guessed := map[string]any{"action": action, "surface": "organization-recruiting", "expectedRevision": expected, "values": map[string]any{"personId": "person_attacker_selected"}}
				if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-guessed-target", guessed); rr.Code != http.StatusBadRequest {
					t.Fatalf("client-selected recruiting target status=%d body=%s", rr.Code, rr.Body.String())
				}
			}
			runtime.setFeatureForTest(STRIDEFeatureOrganizationAuthorityWrite, false)
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("recruiting parent-off status=%d body=%s", rr.Code, rr.Body.String())
			}
			runtime.setFeatureForTest(STRIDEFeatureOrganizationAuthorityWrite, true)
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusOK {
				t.Fatalf("recruiting status=%d body=%s", rr.Code, rr.Body.String())
			}
			if action == "organization-recruiting-grant-create" {
				runtime.network.mu.Lock()
				foundTarget := false
				for _, grant := range runtime.network.grants {
					if grant.SearcherPersonID == "person_recruiting_target" && grant.MembershipID == "membership_recruiting_target" && grant.State == "active" {
						foundTarget = true
					}
				}
				runtime.network.mu.Unlock()
				if !foundTarget {
					t.Fatal("server-bound eligible other member did not receive recruiting grant")
				}
			}
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
				t.Fatalf("recruiting replay status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestStrideE10ProductLiveRegisteredRemainingPortableAndNetworkActions(t *testing.T) {
	t.Run("network-publish", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newNetworkAuthorityFixture(t)
		paused := cloneNetworkProjection(fixture.profile)
		paused.Header = nextAuthorityHeader(paused.Header, "pause-matrix", fixture.now.Add(3*time.Minute))
		paused.State, paused.Discoverability, paused.PurgeGeneration, paused.StateChangedAt = "paused", "unlisted", 1, fixture.now.Add(3*time.Minute)
		if stored, _, _, err := fixture.service.PutProfile(fixture.personController, paused, fixture.profile.Header.Revision, sha256Hex([]byte("pause-matrix"))); err != nil {
			t.Fatal(err)
		} else {
			paused = stored
		}
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return fixture.now.Add(4 * time.Minute) })
		runtime.network = fixture.service
		runtime.setFeatureForTest(STRIDEFeatureWorkRecordPrivate, true)
		runtime.setFeatureForTest(STRIDEFeatureNetworkProfilePublication, true)
		call := strideE10MountRegisteredRuntime(t, runtime, paused.SubjectPersonID, "", "", 0)
		projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-preview", "", nil)
		actionID, revision := strideE10ActionRevisionFromProjection(t, projection, "network-publish")
		payload := map[string]any{"action": "network-publish", "surface": "network-preview", "expectedRevision": revision, "values": map[string]any{}}
		runtime.setFeatureForTest(STRIDEFeatureNetworkProfilePublication, false)
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "network-publish-key", payload); rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("parent-off status=%d body=%s", rr.Code, rr.Body.String())
		}
		runtime.setFeatureForTest(STRIDEFeatureNetworkProfilePublication, true)
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "network-publish-key", payload); rr.Code != http.StatusOK {
			t.Fatalf("publish status=%d body=%s", rr.Code, rr.Body.String())
		}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "network-publish-key", payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("publish replay status=%d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("network-unblock", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newNetworkAuthorityFixture(t)
		block := NetworkBlock{Header: strideE10LiveHeader(STRIDEContractNetworkBlock, STRIDEGlobalPersonTenant, "block_matrix_unblock", 1, "block-matrix", fixture.now.Add(3*time.Minute)), BlockerPersonID: fixture.profile.SubjectPersonID, BlockedPersonID: fixture.grant.SearcherPersonID, Controller: fixture.personController, State: "active", StateChangedAt: fixture.now.Add(3 * time.Minute)}
		if _, _, _, err := fixture.service.PutBlock(fixture.personController, block, 0, sha256Hex([]byte("block-matrix"))); err != nil {
			t.Fatal(err)
		}
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return fixture.now.Add(4 * time.Minute) })
		runtime.network = fixture.service
		runtime.setFeatureForTest(STRIDEFeatureNetworkContact, true)
		call := strideE10MountRegisteredRuntime(t, runtime, fixture.profile.SubjectPersonID, "", "", 0)
		projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-blocks", "", nil)
		actionID, revision := strideE10ActionRevisionFromProjection(t, projection, "network-unblock")
		payload := map[string]any{"action": "network-unblock", "surface": "network-blocks", "expectedRevision": revision, "values": map[string]any{}}
		runtime.setFeatureForTest(STRIDEFeatureNetworkContact, false)
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "unblock-key", payload); rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("unblock parent-off status=%d body=%s", rr.Code, rr.Body.String())
		}
		runtime.setFeatureForTest(STRIDEFeatureNetworkContact, true)
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "unblock-key", payload); rr.Code != http.StatusOK {
			t.Fatalf("unblock status=%d body=%s", rr.Code, rr.Body.String())
		}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "unblock-key", payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("unblock replay status=%d body=%s", rr.Code, rr.Body.String())
		}
	})

	for _, action := range []string{"work-record-export", "work-record-delete"} {
		t.Run(action, func(t *testing.T) {
			setupAuthTestEnv(t)
			fixture := newContributionAuthorityFixture(t)
			claim := createVerifiedClaim(t, fixture)
			_, _ = issuePublishedContribution(t, fixture, claim)
			runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(10 * time.Minute) })
			runtime.contribution = fixture.service
			runtime.setFeatureForTest(STRIDEFeatureWorkRecordPrivate, true)
			call := strideE10MountRegisteredRuntime(t, runtime, fixture.subject.PersonID, "", "", 0)
			projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/work-record", "", nil)
			actionID, revision := strideE10ActionRevisionFromProjection(t, projection, action)
			payload := map[string]any{"action": action, "surface": "work-record", "expectedRevision": revision, "values": map[string]any{}}
			runtime.setFeatureForTest(STRIDEFeatureWorkRecordPrivate, false)
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("portable parent-off status=%d body=%s", rr.Code, rr.Body.String())
			}
			runtime.setFeatureForTest(STRIDEFeatureWorkRecordPrivate, true)
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusOK {
				t.Fatalf("portable action status=%d body=%s", rr.Code, rr.Body.String())
			}
			if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, action+"-key", payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
				t.Fatalf("portable replay status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
			}
		})
	}
}

func TestStrideE10ProductLivePreparedLossReconcilesTerminalMutationFamilies(t *testing.T) {
	t.Run("contact-decision", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newNetworkAuthorityFixture(t)
		admission := NetworkContactAdmission{GrantRef: referenceFromHeader(fixture.grant.Header), SenderPersonID: fixture.grant.SearcherPersonID, SenderOrganizationID: fixture.grant.OrganizationID, MembershipID: fixture.grant.MembershipID, MembershipRevision: fixture.grant.MembershipRevision, RecipientProjection: referenceFromHeader(fixture.profile.Header), Purpose: "prepared_contact", NoteDigest: sha256Hex([]byte("prepared-contact")), CollaborationType: "collaboration", ExpiresAt: fixture.now.Add(12 * time.Hour), IdempotencyKeyDigest: sha256Hex([]byte("prepared-contact-create")), At: fixture.now.Add(3 * time.Minute)}
		contact, _, err := fixture.service.CreateContact(admission)
		if err != nil {
			t.Fatal(err)
		}
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return fixture.now.Add(4 * time.Minute) })
		runtime.network = fixture.service
		actor := fixture.personController
		binding := StrideE10LiveActionBinding{ID: "action_prepared_contact", Type: "contact-decline", Surface: "contact-inbox", PersonID: contact.RecipientPersonID, ExpectedRevision: contact.Header.Revision, ExpiresAt: fixture.now.Add(time.Hour), Target: referenceFromHeader(contact.Header), ContactActor: &actor}
		strideE10AssertPreparedLossReconciles(t, runtime, StrideE10ProductPrincipal{PersonID: contact.RecipientPersonID}, binding, map[string]any{}, []STRIDEFeature{STRIDEFeatureNetworkContact})
	})

	t.Run("approval", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newContributionAuthorityFixture(t)
		claim := createVerifiedClaim(t, fixture)
		approval := FieldReleaseApproval{Header: contributionNetworkHeader(STRIDEContractFieldReleaseApproval, "approval_prepared", claim.OrganizationID), OrganizationID: claim.OrganizationID, SubjectPersonID: claim.SubjectPersonID, Attestation: contributionNetworkRef(STRIDEContractContributionAttestation, "attestation_prepared"), FieldKey: "outcome", FieldValueDigest: authorityDigest("prepared-value"), Source: claim.SourceRefs[0], SourceConsentRevision: claim.ConsentRevision, SourceACLRevision: claim.ACLRevision, SourcePurgeGeneration: claim.PurgeGeneration, Visibility: "signed_in_network", ApproverRole: "organization", Controller: fixture.org.Controller, State: "pending", StateChangedAt: contributionAuthorityTime.Add(4 * time.Minute)}
		if _, err := fixture.service.PutFieldApproval(approval, authorityAssertion(fixture.org, 0, "put-prepared", contributionAuthorityTime.Add(4*time.Minute))); err != nil {
			t.Fatal(err)
		}
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(5 * time.Minute) })
		runtime.contribution = fixture.service
		personID, membershipID := fixture.org.Controller.PrincipalID, "membership-prepared-reviewer"
		runtime.organization.memberships[membershipID] = OrganizationMembership{Header: STRIDEContractHeader{ID: membershipID, Revision: 1}, PersonID: personID, OrganizationID: claim.OrganizationID, Role: "owner", Status: "active"}
		assertion := authorityAssertion(fixture.org, approval.Header.Revision, "prepared-approval", contributionAuthorityTime.Add(5*time.Minute))
		binding := StrideE10LiveActionBinding{ID: "action_prepared_approval", Type: "contribution-organization-approve", Surface: "contribution-approvals", PersonID: personID, OrganizationID: claim.OrganizationID, MembershipRevision: 1, SessionRevision: 1, ExpectedRevision: approval.Header.Revision, ExpiresAt: contributionAuthorityTime.Add(time.Hour), Target: refForHeader(approval.Header), ContributionAssertion: &assertion}
		principal := StrideE10ProductPrincipal{PersonID: personID, ActiveOrganizationID: claim.OrganizationID, OrganizationMembershipID: membershipID, OrganizationMembershipRev: 1, ActiveOrganizationSessionRev: 1}
		strideE10AssertPreparedLossReconciles(t, runtime, principal, binding, map[string]any{}, []STRIDEFeature{STRIDEFeatureContributionReview, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite})
	})

	t.Run("claim-revoke", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newContributionAuthorityFixture(t)
		claim := createVerifiedClaim(t, fixture)
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(10 * time.Minute) })
		runtime.contribution = fixture.service
		personID, membershipID := fixture.org.Controller.PrincipalID, "membership-prepared-revoker"
		runtime.organization.memberships[membershipID] = OrganizationMembership{Header: STRIDEContractHeader{ID: membershipID, Revision: 1}, PersonID: personID, OrganizationID: claim.OrganizationID, Role: "owner", Status: "active"}
		assertion := authorityAssertion(fixture.org, claim.Header.Revision, "prepared-revoke", contributionAuthorityTime.Add(10*time.Minute))
		binding := StrideE10LiveActionBinding{ID: "action_prepared_revoke", Type: "contribution-revoke", Surface: "contribution-approvals", PersonID: personID, OrganizationID: claim.OrganizationID, MembershipRevision: 1, SessionRevision: 1, ExpectedRevision: claim.Header.Revision, ExpiresAt: contributionAuthorityTime.Add(time.Hour), Target: refForHeader(claim.Header), ContributionAssertion: &assertion}
		principal := StrideE10ProductPrincipal{PersonID: personID, ActiveOrganizationID: claim.OrganizationID, OrganizationMembershipID: membershipID, OrganizationMembershipRev: 1, ActiveOrganizationSessionRev: 1}
		strideE10AssertPreparedLossReconciles(t, runtime, principal, binding, map[string]any{"reason": "Governed record revoked"}, []STRIDEFeature{STRIDEFeatureContributionReview, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite})
	})

	for _, action := range []string{"contribution-withdraw", "work-record-export", "work-record-delete"} {
		t.Run(action, func(t *testing.T) {
			setupAuthTestEnv(t)
			fixture := newContributionAuthorityFixture(t)
			claim := createVerifiedClaim(t, fixture)
			_, publication := issuePublishedContribution(t, fixture, claim)
			runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(10 * time.Minute) })
			runtime.contribution = fixture.service
			binding := StrideE10LiveActionBinding{ID: "action_prepared_" + strings.ReplaceAll(action, "-", "_"), Type: action, Surface: "work-record", PersonID: fixture.subject.PersonID, ExpectedRevision: 1, ExpiresAt: contributionAuthorityTime.Add(time.Hour)}
			if action == "contribution-withdraw" {
				assertion := authorityAssertion(fixture.publisher, publication.Header.Revision, "prepared-withdraw", contributionAuthorityTime.Add(10*time.Minute))
				binding.ExpectedRevision, binding.Target, binding.ContributionAssertion = publication.Header.Revision, refForHeader(publication.Header), &assertion
			}
			features := []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}
			strideE10AssertPreparedLossReconciles(t, runtime, StrideE10ProductPrincipal{PersonID: fixture.subject.PersonID}, binding, map[string]any{}, features)
		})
	}
}

func TestStrideE10ProductLiveRegisteredExactNamedPartyAndIssuerActions(t *testing.T) {
	t.Run("named-party-decision", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newContributionAuthorityFixture(t)
		claim := createVerifiedClaim(t, fixture)
		pending := FieldReleaseApproval{Header: contributionNetworkHeader(STRIDEContractFieldReleaseApproval, "approval_live_named_party", claim.OrganizationID), OrganizationID: claim.OrganizationID, SubjectPersonID: claim.SubjectPersonID, Attestation: contributionNetworkRef(STRIDEContractContributionAttestation, "attestation_live_named_party"), FieldKey: "outcome", FieldValueDigest: authorityDigest("named-party-value"), Source: claim.SourceRefs[0], SourceConsentRevision: claim.ConsentRevision, SourceACLRevision: claim.ACLRevision, SourcePurgeGeneration: claim.PurgeGeneration, Visibility: "signed_in_network", RequiredPartyIDs: []string{fixture.party.PartyID}, ApproverRole: "named_party", ApproverPartyID: fixture.party.PartyID, Controller: fixture.party.Controller, State: "pending", StateChangedAt: contributionAuthorityTime.Add(4 * time.Minute)}
		if _, err := fixture.service.PutFieldApproval(pending, authorityAssertion(fixture.org, 0, "put-live-named", contributionAuthorityTime.Add(4*time.Minute))); err != nil {
			t.Fatal(err)
		}
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(5 * time.Minute) })
		runtime.contribution = fixture.service
		runtime.organization.memberships["membership_named_party"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership_named_party", Revision: 1}, PersonID: fixture.party.Controller.PrincipalID, OrganizationID: claim.OrganizationID, Role: "member", Status: "active"}
		runtime.setFeatureForTest(STRIDEFeatureOrganizationAuthorityRead, true)
		runtime.setFeatureForTest(STRIDEFeatureContributionReview, true)
		call := strideE10MountRegisteredRuntime(t, runtime, fixture.party.Controller.PrincipalID, claim.OrganizationID, "membership_named_party", 1)
		projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/contribution-approvals", "", nil)
		actionID, revision := strideE10ActionRevisionFromProjection(t, projection, "contribution-named-party-decision")
		payload := map[string]any{"action": "contribution-named-party-decision", "surface": "contribution-approvals", "expectedRevision": revision, "values": map[string]any{"decision": "approved", "reason": "Reviewed"}}
		malicious, ok := runtime.lookupLiveAction(fixture.party.Controller.PrincipalID, actionID)
		if !ok {
			t.Fatal("missing minted named-party action")
		}
		malicious.Decision = "withdrawn"
		if _, err := runtime.executeBoundAction(context.Background(), StrideE10ProductPrincipal{PersonID: fixture.party.Controller.PrincipalID}, StrideE10ProductCommand{IdempotencyKey: "malicious-withdraw"}, malicious); !errors.Is(err, ErrStrideE10Invalid) {
			t.Fatalf("named-party client action accepted withdrawal: %v", err)
		}
		fixture.service.mu.Lock()
		delete(fixture.service.grants, fixture.party.ID)
		fixture.service.mu.Unlock()
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "named-party-stale-key", payload); rr.Code != http.StatusNotFound {
			t.Fatalf("stale named-party action was not opaque: status=%d body=%s", rr.Code, rr.Body.String())
		}
		fixture.service.mu.Lock()
		fixture.service.grants[fixture.party.ID] = fixture.party
		fixture.service.mu.Unlock()
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "named-party-key", payload); rr.Code != http.StatusOK {
			t.Fatalf("decision status=%d body=%s", rr.Code, rr.Body.String())
		}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "named-party-key", payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("decision replay status=%d body=%s", rr.Code, rr.Body.String())
		}
		fixture.service.mu.Lock()
		delete(fixture.service.grants, fixture.party.ID)
		fixture.service.mu.Unlock()
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "named-party-key", payload); rr.Code != http.StatusNotFound {
			t.Fatalf("completed response survived controller revocation: status=%d body=%s", rr.Code, rr.Body.String())
		}
		fixture.service.mu.Lock()
		fixture.service.grants[fixture.party.ID] = fixture.party
		fixture.service.mu.Unlock()
		operationStore := runtime.operationStore.(*strideE10ProductOperationStore)
		operationStore.mu.Lock()
		for key, record := range operationStore.records {
			if record.ActionID == actionID {
				record.State, record.Response, record.CompletedAt = strideE10OperationCommitted, nil, nil
				operationStore.records[key] = record
			}
		}
		operationStore.mu.Unlock()
		fixture.service.mu.Lock()
		delete(fixture.service.grants, fixture.party.ID)
		fixture.service.mu.Unlock()
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "named-party-key", payload); rr.Code != http.StatusNotFound {
			t.Fatalf("committed operation reprojected after controller revocation: status=%d body=%s", rr.Code, rr.Body.String())
		}
		fixture.service.mu.Lock()
		fixture.service.grants[fixture.party.ID] = fixture.party
		fixture.service.mu.Unlock()
		fixture.service.mu.RLock()
		current := fixture.service.approvals[pending.Header.ID]
		fixture.service.mu.RUnlock()
		if current.State != "approved" || current.Controller != fixture.party.Controller {
			t.Fatalf("wrong named-party authority applied: %+v", current)
		}
	})

	t.Run("signing-issuer-revoke", func(t *testing.T) {
		setupAuthTestEnv(t)
		fixture := newContributionAuthorityFixture(t)
		claim := createVerifiedClaim(t, fixture)
		attestation, publication := issuePublishedContribution(t, fixture, claim)
		runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(7 * time.Minute) })
		runtime.contribution = fixture.service
		runtime.organization.memberships["membership_issuer"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership_issuer", Revision: 1}, PersonID: fixture.issuer.Controller.PrincipalID, OrganizationID: claim.OrganizationID, Role: "member", Status: "active"}
		if err := runtime.installNetworkPublicationDependencies(publication, []ContributionAttestation{attestation}); err != nil {
			t.Fatal(err)
		}
		runtime.setFeatureForTest(STRIDEFeatureOrganizationAuthorityRead, true)
		runtime.setFeatureForTest(STRIDEFeatureContributionReview, true)
		call := strideE10MountRegisteredRuntime(t, runtime, fixture.issuer.Controller.PrincipalID, claim.OrganizationID, "membership_issuer", 1)
		projection := call(http.MethodGet, "/api/stride/v1/mobile/surfaces/contribution-approvals", "", nil)
		actionID, revision := strideE10ActionRevisionFromProjection(t, projection, "contribution-attestation-revoke")
		payload := map[string]any{"action": "contribution-attestation-revoke", "surface": "contribution-approvals", "expectedRevision": revision, "values": map[string]any{"reason": "Signing authority revoked"}}
		fixture.service.mu.Lock()
		delete(fixture.service.grants, fixture.issuer.ID)
		fixture.service.mu.Unlock()
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "issuer-stale-key", payload); rr.Code != http.StatusNotFound {
			t.Fatalf("stale issuer action was not opaque: status=%d body=%s", rr.Code, rr.Body.String())
		}
		fixture.service.mu.Lock()
		fixture.service.grants[fixture.issuer.ID] = fixture.issuer
		fixture.service.mu.Unlock()
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "issuer-revoke-key", payload); rr.Code != http.StatusOK {
			t.Fatalf("revoke status=%d body=%s", rr.Code, rr.Body.String())
		}
		if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+actionID, "issuer-revoke-key", payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("revoke replay status=%d body=%s", rr.Code, rr.Body.String())
		}
		fixture.service.mu.RLock()
		current := fixture.service.attestations[attestation.Header.ID]
		fixture.service.mu.RUnlock()
		if current.State != "revoked" || current.Issuer != fixture.issuer.Controller {
			t.Fatalf("wrong issuer authority applied: %+v", current)
		}
	})
}

func TestStrideE10ProductLivePreparedRestartCompletesContributionNetworkFence(t *testing.T) {
	setupAuthTestEnv(t)
	now := contributionAuthorityTime.Add(10 * time.Minute)
	fixture := newContributionAuthorityFixture(t)
	claim := createVerifiedClaim(t, fixture)
	attestation, publication := issuePublishedContribution(t, fixture, claim)
	network := NewNetworkAuthority(func() time.Time { return now })
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	runtime.contribution, runtime.network = fixture.service, network
	if err := runtime.installNetworkPublicationDependencies(publication, []ContributionAttestation{attestation}); err != nil {
		t.Fatal(err)
	}
	profile := NetworkProfileProjection{Header: contributionNetworkHeader(STRIDEContractNetworkProfileProjection, "projection_compound_restart", STRIDEGlobalPersonTenant), SubjectPersonID: publication.SubjectPersonID, Publication: refForHeader(publication.Header), Fields: []NetworkPublishedField{{FieldKey: "outcome", ValueDigest: attestation.ReleasedFields[0].ValueDigest, VisibleValue: json.RawMessage(`"redacted outcome"`), EvidenceLabel: "organization_verified_redacted", Claim: ptrSTRIDEReference(refForHeader(publication.Header))}}, Discoverability: "unlisted", Controller: fixture.publisher.Controller, State: "draft", StateChangedAt: now.Add(-2 * time.Minute)}
	profile.FieldsDigest, _ = STRIDEContractDigest(profile.Fields)
	created, _, _, err := network.PutProfile(profile.Controller, profile, 0, authorityDigest("compound-profile-create"))
	if err != nil {
		t.Fatal(err)
	}
	publishedProfile := cloneNetworkProjection(created)
	publishedProfile.Header = nextAuthorityHeader(created.Header, "publish", now.Add(-time.Minute))
	publishedProfile.State, publishedProfile.Discoverability, publishedProfile.StateChangedAt = "published", "signed_in_network", now.Add(-time.Minute)
	if _, _, _, err := network.PutProfile(publishedProfile.Controller, publishedProfile, created.Header.Revision, authorityDigest("compound-profile-publish")); err != nil {
		t.Fatal(err)
	}

	personID, membershipID := fixture.org.Controller.PrincipalID, "membership_compound_restart"
	runtime.organization.memberships[membershipID] = OrganizationMembership{Header: STRIDEContractHeader{ID: membershipID, Revision: 1}, PersonID: personID, OrganizationID: claim.OrganizationID, Role: "owner", Status: "active"}
	assertion := authorityAssertion(fixture.org, claim.Header.Revision, "compound-revoke", now)
	binding := StrideE10LiveActionBinding{ID: "action_compound_restart", Type: "contribution-revoke", Surface: "contribution-approvals", PersonID: personID, OrganizationID: claim.OrganizationID, MembershipRevision: 1, SessionRevision: 1, ExpectedRevision: claim.Header.Revision, ExpiresAt: now.Add(time.Hour), Target: refForHeader(claim.Header), ContributionAssertion: &assertion}
	if err := runtime.BindAction(binding); err != nil {
		t.Fatal(err)
	}
	values := map[string]any{"reason": "Revoke governed contribution"}
	body, _ := json.Marshal(map[string]any{"action": binding.Type, "surface": binding.Surface, "expectedRevision": binding.ExpectedRevision, "values": values})
	key := "compound-restart-key"
	command := StrideE10ProductCommand{Operation: strideE10MobileActions[binding.Type].op, Method: http.MethodPost, Path: "/api/stride/v1/mobile/actions/" + binding.ID, OrganizationID: claim.OrganizationID, ResourceID: binding.ID, ExpectedRevision: binding.ExpectedRevision, IdempotencyKey: key, Body: body}
	hydrated, err := runtime.hydrateLiveActionBinding(context.Background(), StrideE10ProductPrincipal{PersonID: personID, ActiveOrganizationID: claim.OrganizationID, OrganizationMembershipID: membershipID, OrganizationMembershipRev: 1, ActiveOrganizationSessionRev: 1}, command, binding, now)
	if err != nil {
		t.Fatal(err)
	}
	bindingJSON, _ := json.Marshal(hydrated)
	fingerprint := sha256Hex(append(append([]byte(nil), body...), bindingJSON...))
	path := filepath.Join(t.TempDir(), "compound-operations.json")
	keys := strideE10ProductTestKeys("compound-operation-key", 1)
	store, err := newStrideE10FileOperationStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	operationKey := strideE10ProductOperationKey(personID, binding.ID, key)
	if _, existed, err := store.Prepare(StrideE10ProductOperationRecord{Key: operationKey, PersonID: personID, ActionID: binding.ID, IdempotencyKeyDigest: sha256Hex([]byte(key)), Fingerprint: fingerprint, State: strideE10OperationPrepared, Binding: hydrated, PreparedAt: now}); err != nil || existed {
		t.Fatalf("prepare existed=%t err=%v", existed, err)
	}
	commitAssertion := runtime.liveContributionAssertion(*hydrated.ContributionAssertion, command, now)
	commitAssertion.ExpectedRevision = claim.Header.Revision
	if _, _, err := fixture.service.RevokeClaim(claim.Header.ID, commitAssertion); err != nil {
		t.Fatal(err)
	}
	network.mu.Lock()
	beforeRestart := cloneNetworkProjection(network.profiles[profile.Header.ID])
	network.mu.Unlock()
	if beforeRestart.State != "published" {
		t.Fatalf("test did not stop between compound effects: %+v", beforeRestart)
	}
	fixture.service.mu.Lock()
	delete(fixture.service.grants, fixture.org.ID)
	fixture.service.mu.Unlock()

	reloaded, err := newStrideE10FileOperationStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newStrideE10ProductLiveRuntimeWithStores(func() time.Time { return now }, runtime.portableStore, reloaded)
	restarted.organization, restarted.contribution, restarted.network = runtime.organization, fixture.service, network
	for _, feature := range []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureContributionReview} {
		restarted.setFeatureForTest(feature, true)
	}
	call := strideE10MountRegisteredRuntime(t, restarted, personID, claim.OrganizationID, membershipID, 1)
	payload := map[string]any{"action": binding.Type, "surface": binding.Surface, "expectedRevision": binding.ExpectedRevision, "values": values}
	if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+binding.ID, key, payload); rr.Code != http.StatusNotFound {
		t.Fatalf("revoked controller received stale recovery response: status=%d body=%s", rr.Code, rr.Body.String())
	}
	network.mu.Lock()
	afterRestart := cloneNetworkProjection(network.profiles[profile.Header.ID])
	purges := make([]DerivedPurgeReceipt, 0, len(network.purges))
	for _, receipt := range network.purges {
		purges = append(purges, cloneDerivedPurgeReceipt(receipt))
	}
	network.mu.Unlock()
	if afterRestart.State != "paused" || afterRestart.Discoverability != "unlisted" || len(purges) != 1 {
		t.Fatalf("restart did not complete exact network fence: profile=%+v purges=%d", afterRestart, len(purges))
	}
	assertExactNetworkPurgeReceipt(t, &purges[0])
	fixture.service.mu.Lock()
	fixture.service.grants[fixture.org.ID] = fixture.org
	fixture.service.mu.Unlock()
	if rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+binding.ID, key, payload); rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("re-authorized reconciliation status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
}

func TestStrideE10WorkRecordEvidenceFailsClosedOnEligibilityClaimAndApprovalDrift(t *testing.T) {
	fixture := newContributionAuthorityFixture(t)
	claim := createVerifiedClaim(t, fixture)
	_, publication := issuePublishedContribution(t, fixture, claim)
	view, err := fixture.service.ReadStrideE10ContributionView(StrideE10ContributionViewScope{GrantID: fixture.subject.ID, Controller: fixture.subject.Controller})
	if err != nil {
		t.Fatal(err)
	}
	countEvidence := func(candidate StrideE10ContributionView, eligible func(string, string) bool) int {
		count := 0
		for _, item := range strideE10WorkRecordProjectionItems(candidate, StrideE10OrganizationSelfView{}, eligible) {
			if item["kind"] == "contribution-evidence" {
				count++
			}
		}
		return count
	}
	if got := countEvidence(view, fixture.service.FieldEligible); got != 1 {
		t.Fatalf("current verified evidence count=%d", got)
	}
	if got := countEvidence(view, func(string, string) bool { return false }); got != 0 {
		t.Fatalf("fenced field rendered evidence count=%d", got)
	}
	if got := countEvidence(view, nil); got != 0 {
		t.Fatalf("nil eligibility callback rendered evidence count=%d", got)
	}
	sectionEntries := func(candidate StrideE10ContributionView, section string) []string {
		for _, item := range strideE10WorkRecordProjectionItems(candidate, StrideE10OrganizationSelfView{}, func(string, string) bool { return true }) {
			detail, _ := item["detail"].(map[string]any)
			if detail["kind"] == "work-record-section" && detail["section"] == section {
				entries, _ := detail["entries"].([]string)
				return entries
			}
		}
		return nil
	}
	unrelated := cloneContract(claim)
	unrelated.Header.ID = "claim_unrelated_current"
	unrelated.Header.ContentDigest = authorityDigest("claim_unrelated_current")
	unrelated.ProblemClass = "unrelated-problem"
	unrelated.OutcomeClass = "unrelated-outcome"
	unrelated.ContributionKind = "unrelated-contribution"
	for _, invalidState := range []string{"revalidation_required", "revoked", "superseded"} {
		claimDrift := cloneContract(view)
		claimDrift.Claims = append(claimDrift.Claims, unrelated)
		for index := range claimDrift.Claims {
			if claimDrift.Claims[index].Header.ID == claim.Header.ID {
				claimDrift.Claims[index].State = invalidState
			}
		}
		if got := sectionEntries(claimDrift, "problems-outcomes"); !reflect.DeepEqual(got, []string{"unrelated-problem / unrelated-outcome"}) {
			t.Fatalf("%s claim polluted problems/outcomes section: %#v", invalidState, got)
		}
		if got := sectionEntries(claimDrift, "how-i-contribute"); !reflect.DeepEqual(got, []string{"unrelated-contribution"}) {
			t.Fatalf("%s claim polluted contribution section: %#v", invalidState, got)
		}
	}
	claimDrift := cloneContract(view)
	for index := range claimDrift.Claims {
		if claimDrift.Claims[index].Header.ID == claim.Header.ID {
			claimDrift.Claims[index].State = "revalidation_required"
		}
	}
	if got := countEvidence(claimDrift, func(string, string) bool { return true }); got != 0 {
		t.Fatalf("revalidation-required claim rendered evidence count=%d", got)
	}
	approvalDrift := cloneContract(view)
	for index := range approvalDrift.Approvals {
		if approvalDrift.Approvals[index].Attestation.ID == publication.Attestations[0].ID {
			approvalDrift.Approvals[index].State = "withdrawn"
			break
		}
	}
	if got := countEvidence(approvalDrift, func(string, string) bool { return true }); got != 0 {
		t.Fatalf("withdrawn approval rendered evidence count=%d", got)
	}
}

func TestStrideE10ContributionCompoundPostImagesAreActionSpecific(t *testing.T) {
	controller := STRIDEControllerRevision{PrincipalID: "person_action_specific", AuthorityID: "authority_action_specific", AuthorityRevision: 1, PolicyRevision: 1}
	assertion := &ContributionAuthorityAssertion{Controller: controller}
	target := STRIDEReference{ContractType: STRIDEContractContributionClaim, ID: "claim_action_specific", Revision: 1, Digest: authorityDigest("target")}
	claim := validContributionClaim()
	claim.Header.ID, claim.Header.Revision = target.ID, target.Revision+1
	claim.OrganizationReview = &controller
	publication := PublishedContributionClaim{Header: contributionNetworkHeader(STRIDEContractPublishedContributionClaim, "publication_action_specific", STRIDEGlobalPersonTenant), SubjectPersonID: controller.PrincipalID, Controller: controller, State: "published"}
	publication.Header.Revision = 2
	approval := validFieldApproval()
	approval.Header.Revision, approval.Controller = 2, controller
	attestation := ContributionAttestation{Header: contributionNetworkHeader(STRIDEContractContributionAttestation, "attestation_action_specific", "org_1"), Issuer: controller, State: "revoked"}
	attestation.Header.Revision = 2
	tests := []StrideE10LiveActionBinding{
		{Type: "contribution-publish", ContributionAssertion: assertion, Publication: func() *PublishedContributionClaim { v := publication; v.State = "draft"; return &v }()},
		{Type: "contribution-withdraw", ContributionAssertion: assertion, Target: STRIDEReference{ID: publication.Header.ID, Revision: 1}, Publication: func() *PublishedContributionClaim {
			v := publication
			v.State = "superseded"
			v.Visibility = "private"
			return &v
		}()},
		{Type: "contribution-revoke", ContributionAssertion: assertion, Target: target, Claim: func() *ContributionClaim { v := claim; v.State = "revalidation_required"; return &v }()},
		{Type: "contribution-correct", ContributionAssertion: assertion, Target: target, Claim: func() *ContributionClaim { v := claim; v.State = "revoked"; return &v }(), CorrectedClaim: &claim},
		{Type: "contribution-organization-approve", ContributionAssertion: assertion, Target: STRIDEReference{ID: approval.Header.ID, Revision: 1}, Approval: func() *FieldReleaseApproval { v := approval; v.State = "denied"; return &v }()},
		{Type: "contribution-organization-deny", ContributionAssertion: assertion, Target: STRIDEReference{ID: approval.Header.ID, Revision: 1}, Approval: func() *FieldReleaseApproval { v := approval; v.State = "approved"; return &v }()},
		{Type: "contribution-named-party-decision", Decision: "approved", ContributionAssertion: assertion, Target: STRIDEReference{ID: approval.Header.ID, Revision: 1}, Approval: func() *FieldReleaseApproval { v := approval; v.State = "denied"; return &v }()},
		{Type: "contribution-attestation-revoke", ContributionAssertion: assertion, Target: STRIDEReference{ID: attestation.Header.ID, Revision: 1}, Attestation: func() *ContributionAttestation { v := attestation; v.State = "superseded"; return &v }()},
	}
	for _, binding := range tests {
		if _, _, post, _ := strideE10BoundContributionPostImage(binding); post != nil {
			t.Fatalf("%s accepted another action's terminal state: %+v", binding.Type, post)
		}
	}
}

func strideE10AssertPreparedLossReconciles(t *testing.T, runtime *StrideE10ProductLiveRuntime, principal StrideE10ProductPrincipal, binding StrideE10LiveActionBinding, values map[string]any, features []STRIDEFeature) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prepared-operations.json")
	keys := strideE10ProductTestKeys("prepared-operation-key", 1)
	store, err := newStrideE10FileOperationStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	runtime.operationStore = store
	if err := runtime.BindAction(binding); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": binding.Type, "surface": binding.Surface, "expectedRevision": binding.ExpectedRevision, "values": values})
	key := "prepared-loss-key"
	command := StrideE10ProductCommand{Operation: strideE10MobileActions[binding.Type].op, Method: http.MethodPost, Path: "/api/stride/v1/mobile/actions/" + binding.ID, OrganizationID: principal.ActiveOrganizationID, ResourceID: binding.ID, ExpectedRevision: binding.ExpectedRevision, IdempotencyKey: key, Body: body}
	hydrated, err := runtime.hydrateLiveActionBinding(context.Background(), principal, command, binding, runtime.now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	bindingJSON, _ := json.Marshal(hydrated)
	fingerprint := sha256Hex(append(append([]byte(nil), body...), bindingJSON...))
	operationKey := strideE10ProductOperationKey(principal.PersonID, binding.ID, key)
	record := StrideE10ProductOperationRecord{Key: operationKey, PersonID: principal.PersonID, ActionID: binding.ID, IdempotencyKeyDigest: sha256Hex([]byte(key)), Fingerprint: fingerprint, State: strideE10OperationPrepared, Binding: hydrated, PreparedAt: runtime.now().UTC()}
	if _, existed, err := store.Prepare(record); err != nil || existed {
		t.Fatalf("prepare existed=%t err=%v", existed, err)
	}
	if _, err := runtime.executeBoundAction(context.Background(), principal, command, hydrated); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newStrideE10FileOperationStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newStrideE10ProductLiveRuntimeWithStores(runtime.now, runtime.portableStore, reloaded)
	restarted.organization, restarted.contribution, restarted.network = runtime.organization, runtime.contribution, runtime.network
	for _, feature := range features {
		restarted.setFeatureForTest(feature, true)
	}
	call := strideE10MountRegisteredRuntime(t, restarted, principal.PersonID, principal.ActiveOrganizationID, principal.OrganizationMembershipID, principal.OrganizationMembershipRev)
	payload := map[string]any{"action": binding.Type, "surface": binding.Surface, "expectedRevision": binding.ExpectedRevision, "values": values}
	rr := call(http.MethodPost, "/api/stride/v1/mobile/actions/"+binding.ID, key, payload)
	if rr.Code != http.StatusOK || rr.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("prepared loss recovery status=%d replay=%q body=%s", rr.Code, rr.Header().Get("Idempotency-Replayed"), rr.Body.String())
	}
}

func strideE10OrganizationProductFixture(t *testing.T) (*OrganizationAuthorityService, Organization, OrganizationMembership, OrganizationMembership, OrganizationJoinRequest) {
	t.Helper()
	now := time.Date(2026, 8, 8, 18, 0, 0, 0, time.UTC)
	service := NewOrganizationAuthorityService()
	ownerPerson := organizationTestPerson("person_product_owner", '1', now)
	memberPerson := organizationTestPerson("person_product_member", '2', now)
	joiningPerson := organizationTestPerson("person_product_joining", '3', now)
	for _, person := range []PersonPrincipal{ownerPerson, memberPerson, joiningPerson} {
		if err := service.RegisterPerson(person); err != nil {
			t.Fatal(err)
		}
	}
	organization, owner, createAudit := organizationTestCreate(ownerPerson.Header.ID, 1, now.Add(time.Minute))
	if err := service.CreateOrganization(ownerPerson.Header.ID, organization, owner, createAudit); err != nil {
		t.Fatal(err)
	}
	join := organizationTestJoinRequest(memberPerson.Header.ID, organization.Header.ID, 1, "pending", now.Add(2*time.Minute), "")
	joinAudit := organizationTestAudit(organization.Header.ID, memberPerson.Header.ID, "", 0, memberPerson.Header.ID, "request", 0, 1, join.Header.ID, '4', now.Add(2*time.Minute))
	if err := service.RequestJoin(join, joinAudit); err != nil {
		t.Fatal(err)
	}
	member := organizationTestMembership("membership_product_member", memberPerson.Header.ID, organization.Header.ID, "member", 1, now.Add(3*time.Minute), owner.Header.ID)
	approved := organizationTestJoinRequest(memberPerson.Header.ID, organization.Header.ID, 2, "approved", join.RequestedAt, owner.Header.ID)
	approved.Header.ID, approved.Header.CreatedAt = join.Header.ID, join.Header.CreatedAt
	decidedAt := now.Add(3 * time.Minute)
	approved.DecidedAt = &decidedAt
	approveAudit := organizationTestAudit(organization.Header.ID, owner.PersonID, owner.Header.ID, owner.Header.Revision, member.PersonID, "approve", 1, 2, join.Header.ID, '5', decidedAt)
	if err := service.DecideJoin(owner.Header.ID, owner.Header.Revision, join.Header.Revision, approved, &member, approveAudit); err != nil {
		t.Fatal(err)
	}
	pending := organizationTestJoinRequest(joiningPerson.Header.ID, organization.Header.ID, 1, "pending", now.Add(4*time.Minute), "")
	pendingAudit := organizationTestAudit(organization.Header.ID, joiningPerson.Header.ID, "", 0, joiningPerson.Header.ID, "request", 0, 1, pending.Header.ID, '6', now.Add(4*time.Minute))
	if err := service.RequestJoin(pending, pendingAudit); err != nil {
		t.Fatal(err)
	}
	return service, organization, owner, member, pending
}

func strideE10MountRegisteredRuntime(t *testing.T, runtime *StrideE10ProductLiveRuntime, personID, organizationID, membershipID string, membershipRevision int64) func(string, string, string, any) *httptest.ResponseRecorder {
	t.Helper()
	token := sha256Hex([]byte(t.Name()))
	record := sessionRecord{Email: sha256Hex([]byte(personID))[:12] + "@example.com", PersonID: personID, Expires: runtime.now().Add(24 * time.Hour)}
	if organizationID != "" {
		record.ActiveOrganizationID, record.OrganizationMembershipID, record.OrganizationMembershipRev, record.ActiveOrganizationSessionRev = organizationID, membershipID, membershipRevision, 1
		sessionHash := hashResetToken(token)
		expires := runtime.now().Add(24 * time.Hour)
		runtime.organization.sessions[sessionHash] = ActiveOrganizationSession{Header: strideE10LiveHeader(STRIDEContractActiveOrganizationSession, STRIDEGlobalPersonTenant, "active_session_"+sessionHash[:24], 1, sessionHash+"\x00test", runtime.now()), SessionSubjectDigest: sessionHash, PersonID: personID, OrganizationID: organizationID, MembershipID: membershipID, MembershipRevision: membershipRevision, SessionRevision: 1, Status: "active", BoundAt: runtime.now(), ExpiresAt: expires}
	}
	userSessionStore().mu.Lock()
	userSessionStore().sessions[hashResetToken(token)] = record
	userSessionStore().mu.Unlock()
	prior := strideE10LiveProductRuntime
	strideE10LiveProductRuntime = runtime
	t.Cleanup(func() { strideE10LiveProductRuntime = prior })
	mux := http.NewServeMux()
	registerStrideE10ProductLiveRoutes(mux)
	return func(method, path, key string, body any) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := strideE10Request(method, path, key, body)
		req.Header.Set("Authorization", "Bearer "+token)
		mux.ServeHTTP(rr, req)
		return rr
	}
}

func strideE10ActionFromProjection(t *testing.T, response *httptest.ResponseRecorder, actionType string) string {
	id, _ := strideE10ActionRevisionFromProjection(t, response, actionType)
	return id
}

func strideE10ActionRevisionFromProjection(t *testing.T, response *httptest.ResponseRecorder, actionType string) (string, int64) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("projection status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Items []struct {
			Actions []struct {
				ID               string `json:"id"`
				Type             string `json:"type"`
				ExpectedRevision int64  `json:"expectedRevision"`
			} `json:"actions"`
		} `json:"items"`
	}
	if json.Unmarshal(response.Body.Bytes(), &envelope) != nil {
		t.Fatal("invalid projection envelope")
	}
	for _, item := range envelope.Items {
		for _, action := range item.Actions {
			if action.Type == actionType {
				return action.ID, action.ExpectedRevision
			}
		}
	}
	t.Fatalf("missing %s action: %s", actionType, response.Body.String())
	return "", 0
}

func TestStrideE10ProductLiveW6ActionBoundToExactSessionIdentity(t *testing.T) {
	now := time.Date(2026, 8, 9, 20, 0, 0, 0, time.UTC)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	grant := strideTestRef(STRIDEContractTalentSearchGrant, "grant_session_bound")
	sessionA, sessionB := sha256Hex([]byte("session-a")), sha256Hex([]byte("session-b"))
	binding := StrideE10LiveActionBinding{ID: "action_session_bound", Type: "network-search-propose", Surface: "network-search", PersonID: "person_searcher", OrganizationID: "organization_search", ExpectedRevision: grant.Revision, ExpiresAt: now.Add(time.Minute), Target: grant, MembershipRevision: 4, SessionRevision: 7, SessionHash: sessionA, ActiveSessionID: "active_session_a"}
	if err := runtime.BindAction(binding); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": binding.Type, "surface": binding.Surface, "expectedRevision": binding.ExpectedRevision, "values": map[string]any{"query": "problem_class:growth"}})
	principalB := StrideE10ProductPrincipal{PersonID: binding.PersonID, ActiveOrganizationID: binding.OrganizationID, OrganizationMembershipID: "membership_searcher", OrganizationMembershipRev: binding.MembershipRevision, ActiveOrganizationSessionRev: binding.SessionRevision, SessionHash: sessionB, ActiveOrganizationSessionID: "active_session_b"}
	_, _, err := runtime.Execute(context.Background(), principalB, StrideE10ProductCommand{Operation: "network.search", Method: http.MethodPost, Path: "/api/stride/v1/mobile/actions/" + binding.ID, OrganizationID: binding.OrganizationID, ResourceID: binding.ID, ExpectedRevision: binding.ExpectedRevision, IdempotencyKey: "same-revision-other-session", Body: body})
	if !errors.Is(err, ErrStrideE10NotFound) {
		t.Fatalf("session B used session A action at same revision: %v", err)
	}
}

func TestStrideE10ProductLiveResolverAllowsCanonicalPersonWithoutOrganization(t *testing.T) {
	setupAuthTestEnv(t)
	now := time.Now().UTC()
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	token := strings.Repeat("a", 64)
	store := userSessionStore()
	store.mu.Lock()
	store.sessions[hashResetToken(token)] = sessionRecord{Email: "person@example.com", PersonID: "person-one", Expires: now.Add(time.Hour)}
	store.mu.Unlock()
	request := httptest.NewRequest(http.MethodGet, "/api/stride/v1/mobile/surfaces/profile", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	principal, err := runtime.ResolvePrincipal(request)
	if err != nil || principal.PersonID != "person-one" || principal.ActiveOrganizationID != "" {
		t.Fatalf("principal=%+v err=%v", principal, err)
	}
}

func TestStrideE10ProductLiveActionLedgerScopesExpiryAndConcurrentReplay(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	runtime.contribution.grants["grant-person-one-subject"] = authorityGrant("grant-person-one-subject", "subject", "", "person-one", "", "person-one")
	binding := StrideE10LiveActionBinding{ID: "action-export-one", Type: "work-record-export", Surface: "work-record", PersonID: "person-one", ExpectedRevision: 1, ExpiresAt: now.Add(time.Hour)}
	if err := runtime.BindAction(binding); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": binding.Type, "surface": binding.Surface, "expectedRevision": 1, "values": map[string]any{}})
	command := StrideE10ProductCommand{Operation: "work_record.export", Method: http.MethodPost, Path: "/api/stride/v1/mobile/actions/" + binding.ID, ResourceID: binding.ID, ExpectedRevision: 1, IdempotencyKey: "same-key", Body: body}
	principal := StrideE10ProductPrincipal{PersonID: "person-one"}
	var first atomic.Int64
	var replay atomic.Int64
	var failures atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, repeated, err := runtime.Execute(context.Background(), principal, command)
			if err != nil {
				failures.Add(1)
			} else if repeated {
				replay.Add(1)
			} else {
				first.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 || first.Load() != 1 || replay.Load() != 31 {
		t.Fatalf("first=%d replay=%d failures=%d", first.Load(), replay.Load(), failures.Load())
	}
	if _, _, err := runtime.Execute(context.Background(), StrideE10ProductPrincipal{PersonID: "person-two"}, command); !errors.Is(err, ErrStrideE10NotFound) {
		t.Fatalf("cross-account action err=%v", err)
	}
	expired := binding
	expired.ID = "action-expired"
	expired.ExpiresAt = now.Add(-time.Second)
	if err := runtime.BindAction(expired); !errors.Is(err, ErrStrideE10Invalid) {
		t.Fatalf("expired binding err=%v", err)
	}
	if len(runtime.packages) != 1 {
		t.Fatalf("packages=%d want=1", len(runtime.packages))
	}
	for _, pkg := range runtime.packages {
		if strings.Contains(string(pkg.Body), "action-export-one") || strings.Contains(string(pkg.Body), "actions") {
			t.Fatalf("export package leaked mutation capability: %s", pkg.Body)
		}
	}
}

func TestStrideE10ProductLiveActionBindingRejectsStaleTargetAndMissingAuthorityGeneration(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	target := contributionNetworkRef(STRIDEContractFieldReleaseApproval, "approval-bound")
	stale := StrideE10LiveActionBinding{ID: "action-stale-target", Type: "contribution-organization-approve", Surface: "contribution-approvals", PersonID: "person-admin", OrganizationID: "org-one", ExpectedRevision: target.Revision + 1, ExpiresAt: now.Add(time.Hour), Target: target, MembershipRevision: 2, SessionRevision: 3}
	if err := runtime.BindAction(stale); !errors.Is(err, ErrStrideE10Invalid) {
		t.Fatalf("stale target binding err=%v", err)
	}
	missingGeneration := stale
	missingGeneration.ID = "action-missing-generation"
	missingGeneration.ExpectedRevision = target.Revision
	missingGeneration.MembershipRevision = 0
	if err := runtime.BindAction(missingGeneration); !errors.Is(err, ErrStrideE10Invalid) {
		t.Fatalf("missing authority generation err=%v", err)
	}
}

func TestStrideE10ProductLiveOrganizationCapacityAndFinalOwnerAreConflicts(t *testing.T) {
	for _, err := range []error{ErrOrganizationCapacity, ErrOrganizationFinalOwner} {
		if !errors.Is(strideE10LiveError(err), ErrStrideE10Conflict) {
			t.Fatalf("organization policy conflict mapped as %v", strideE10LiveError(err))
		}
	}
}

func TestStrideE10ProductLiveWorkRecordDeletionPreservesGovernedHistory(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	claim := validContributionClaim()
	runtime.contribution.mu.Lock()
	runtime.contribution.grants["grant-delete-subject"] = authorityGrant("grant-delete-subject", "subject", "", claim.SubjectPersonID, "", claim.SubjectPersonID)
	runtime.contribution.claims[claim.Header.ID] = claim
	runtime.contribution.claimHistory[authorityHistoryKey(claim.Header.ID, claim.Header.Revision)] = claim
	runtime.contribution.mu.Unlock()
	binding := StrideE10LiveActionBinding{ID: "action-delete-work", Type: "work-record-delete", Surface: "work-record", PersonID: claim.SubjectPersonID, ExpectedRevision: 1, ExpiresAt: now.Add(time.Hour)}
	if err := runtime.BindAction(binding); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": binding.Type, "surface": binding.Surface, "expectedRevision": 1, "values": map[string]any{}})
	_, _, err := runtime.Execute(context.Background(), StrideE10ProductPrincipal{PersonID: claim.SubjectPersonID}, StrideE10ProductCommand{Method: http.MethodPost, Path: "/api/stride/v1/mobile/actions/" + binding.ID, ResourceID: binding.ID, ExpectedRevision: 1, IdempotencyKey: "delete-key", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	runtime.contribution.mu.RLock()
	current := runtime.contribution.claims[claim.Header.ID]
	historical := runtime.contribution.claimHistory[authorityHistoryKey(claim.Header.ID, claim.Header.Revision)]
	runtime.contribution.mu.RUnlock()
	if current.Header.ID == "" || historical.Header.ID == "" {
		t.Fatal("portable deletion erased organization-governed claim history")
	}
	projection, err := runtime.project(StrideE10ProductPrincipal{PersonID: claim.SubjectPersonID}, "work-record")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(projection)
	if !strings.Contains(string(raw), "purge-receipt") || strings.Contains(string(raw), claim.Header.ID) {
		t.Fatalf("post-delete projection=%s", raw)
	}
	record, ok := runtime.portableStore.Load(claim.SubjectPersonID)
	if !ok || len(record.PurgeReceipts) != 1 || record.PurgeReceipts[0].Validate() != nil {
		t.Fatalf("durable deletion record=%+v", record)
	}
}

func TestStrideE10ProductLiveDeletionWithdrawsAndPurgesAuthoritiesAcrossRestart(t *testing.T) {
	now := contributionAuthorityTime.Add(20 * time.Minute)
	fixture := newContributionAuthorityFixture(t)
	claim := createVerifiedClaim(t, fixture)
	attestation, publication := issuePublishedContribution(t, fixture, claim)
	network := NewNetworkAuthority(func() time.Time { return now })
	installer := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	installer.contribution, installer.network = fixture.service, network
	if err := installer.installNetworkPublicationDependencies(publication, []ContributionAttestation{attestation}); err != nil {
		t.Fatal(err)
	}
	profile := NetworkProfileProjection{Header: contributionNetworkHeader(STRIDEContractNetworkProfileProjection, "projection_delete", STRIDEGlobalPersonTenant), SubjectPersonID: publication.SubjectPersonID, Publication: refForHeader(publication.Header), Fields: []NetworkPublishedField{{FieldKey: "outcome", ValueDigest: attestation.ReleasedFields[0].ValueDigest, EvidenceLabel: "organization_verified_redacted", Claim: ptrSTRIDEReference(refForHeader(publication.Header))}}, Discoverability: "unlisted", PurgeGeneration: 0, Controller: fixture.publisher.Controller, State: "draft", StateChangedAt: now.Add(-2 * time.Minute)}
	profile.FieldsDigest, _ = STRIDEContractDigest(profile.Fields)
	created, _, _, err := network.PutProfile(profile.Controller, profile, 0, authorityDigest("profile-create"))
	if err != nil {
		t.Fatal(err)
	}
	published := created
	published.Header = nextAuthorityHeader(created.Header, "publish", now.Add(-time.Minute))
	published.State = "published"
	published.Discoverability = "signed_in_network"
	published.StateChangedAt = now.Add(-time.Minute)
	if _, _, _, err := network.PutProfile(published.Controller, published, created.Header.Revision, authorityDigest("profile-publish")); err != nil {
		t.Fatal(err)
	}
	store := newStrideE10MemoryPortableDeletionStore()
	runtime := newStrideE10ProductLiveRuntimeWithStore(func() time.Time { return now }, store)
	runtime.contribution = fixture.service
	runtime.network = network
	receipt := strideE10ExportReceipt{ID: "export-delete", PersonID: publication.SubjectPersonID, Surface: "work-record", Revision: 1, PackageDigest: authorityDigest("package"), ExpiresAt: now.Add(time.Hour)}
	runtime.exports["export-key"] = receipt
	runtime.packages[receipt.ID] = strideE10ExportPackage{Receipt: receipt, Body: json.RawMessage(`{"safe":true}`)}
	binding := StrideE10LiveActionBinding{ID: "action-delete-complete", Type: "work-record-delete", Surface: "work-record", PersonID: publication.SubjectPersonID, ExpectedRevision: 1, ExpiresAt: now.Add(time.Hour)}
	if err := runtime.BindAction(binding); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": binding.Type, "surface": binding.Surface, "expectedRevision": 1, "values": map[string]any{}})
	principal := StrideE10ProductPrincipal{PersonID: publication.SubjectPersonID}
	if _, _, err := runtime.Execute(context.Background(), principal, StrideE10ProductCommand{Method: http.MethodPost, Path: "/api/stride/v1/mobile/actions/" + binding.ID, ResourceID: binding.ID, ExpectedRevision: 1, IdempotencyKey: "delete-complete", Body: body}); err != nil {
		t.Fatal(err)
	}
	fixture.service.mu.RLock()
	withdrawn := fixture.service.publications[publication.Header.ID]
	governed := fixture.service.claims[claim.Header.ID]
	fixture.service.mu.RUnlock()
	network.mu.Lock()
	deleted := network.profiles[published.Header.ID]
	network.mu.Unlock()
	record, ok := store.Load(publication.SubjectPersonID)
	if !ok || withdrawn.State != "withdrawn" || deleted.State != "deleted" || governed.State != "verified" || len(record.PurgeReceipts) < 2 || len(record.RevokedExportIDs) != 1 {
		t.Fatalf("withdrawn=%s deleted=%s governed=%s record=%+v", withdrawn.State, deleted.State, governed.State, record)
	}
	for _, purge := range record.PurgeReceipts {
		if err := purge.Validate(); err != nil {
			t.Fatalf("invalid purge receipt: %v %+v", err, purge)
		}
	}
	restarted := newStrideE10ProductLiveRuntimeWithStore(func() time.Time { return now }, store)
	restarted.contribution = fixture.service
	restarted.network = network
	if _, _, err := restarted.Execute(context.Background(), principal, StrideE10ProductCommand{Method: http.MethodGet, Operation: "work_record.export_download", ResourceID: receipt.ID}); !errors.Is(err, ErrStrideE10NotFound) {
		t.Fatalf("revoked export survived restart: %v", err)
	}
}

func TestStrideE10ProductLiveTypedAuthorityProjections(t *testing.T) {
	contributionFixture := newContributionAuthorityFixture(t)
	claim := createVerifiedClaim(t, contributionFixture)
	_, _ = issuePublishedContribution(t, contributionFixture, claim)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contributionAuthorityTime.Add(20 * time.Minute) })
	runtime.contribution = contributionFixture.service
	workRecord, err := runtime.project(StrideE10ProductPrincipal{PersonID: contributionFixture.subject.PersonID}, "work-record")
	if err != nil || strideE10ValidateMobileProjection(workRecord, "work-record") != nil {
		t.Fatalf("work-record projection err=%v value=%+v", err, workRecord)
	}
	runtime.organization.memberships["membership-reviewer"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership-reviewer", Revision: 2}, PersonID: contributionFixture.org.Controller.PrincipalID, OrganizationID: contributionFixture.org.OrganizationID, Role: "admin", Status: "active"}
	reviewer := StrideE10ProductPrincipal{PersonID: contributionFixture.org.Controller.PrincipalID, ActiveOrganizationID: contributionFixture.org.OrganizationID, OrganizationMembershipID: "membership-reviewer", OrganizationMembershipRev: 2, ActiveOrganizationSessionRev: 1}
	review, err := runtime.project(reviewer, "contribution-approvals")
	if err != nil || strideE10ValidateMobileProjection(review, "contribution-approvals") != nil {
		encoded, _ := json.Marshal(review)
		t.Fatalf("contribution review projection err=%v value=%s", err, encoded)
	}

	networkFixture := newNetworkAuthorityFixture(t)
	if _, _, err := networkFixture.service.Search(networkFixture.searchRequest("typed", "people who solved growth problems", networkFilter("problem_class", "growth"))); err != nil {
		t.Fatal(err)
	}
	runtime.network = networkFixture.service
	searcher := StrideE10ProductPrincipal{PersonID: networkFixture.grant.SearcherPersonID, ActiveOrganizationID: networkFixture.grant.OrganizationID, OrganizationMembershipID: networkFixture.grant.MembershipID, OrganizationMembershipRev: networkFixture.grant.MembershipRevision, ActiveOrganizationSessionRev: 1}
	search, err := runtime.project(searcher, "network-search")
	if !errors.Is(err, ErrStrideE10NotFound) || search != nil {
		t.Fatalf("legacy receipt without a W6 final-copy disclosure remained renderable: err=%v value=%+v", err, search)
	}
}

func TestStrideE10ProductLiveCoworkerProjectionBindsExactSharedOrganizationTarget(t *testing.T) {
	runtime := NewStrideE10ProductLiveRuntime(nil)
	runtime.organization.persons["person-viewer"] = PersonPrincipal{Status: "active"}
	runtime.organization.persons["person-target"] = PersonPrincipal{Status: "active"}
	runtime.organization.persons["person-hidden"] = PersonPrincipal{Status: "active"}
	runtime.organization.profiles["person-target"] = PersonProfile{PersonID: "person-target", DisplayName: "Target", Status: "active"}
	runtime.organization.profiles["person-hidden"] = PersonProfile{PersonID: "person-hidden", DisplayName: "Hidden", Status: "active"}
	runtime.organization.memberships["membership-viewer"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership-viewer", Revision: 2}, PersonID: "person-viewer", OrganizationID: "org-shared", Role: "member", Status: "active"}
	runtime.organization.memberships["membership-target"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership-target", Revision: 3}, PersonID: "person-target", OrganizationID: "org-shared", Role: "member", Status: "active"}
	runtime.organization.memberships["membership-hidden"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership-hidden", Revision: 1}, PersonID: "person-hidden", OrganizationID: "org-hidden", Role: "owner", Status: "active"}
	runtime.organization.memberProfiles["membership-target"] = OrganizationMemberProfile{Header: STRIDEContractHeader{ID: "member-profile-target", Revision: 1}, PersonID: "person-target", OrganizationID: "org-shared", MembershipID: "membership-target", MembershipRevision: 3, JoinedAt: time.Now().UTC()}
	runtime.organization.memberProfiles["membership-hidden"] = OrganizationMemberProfile{Header: STRIDEContractHeader{ID: "member-profile-hidden", Revision: 1}, PersonID: "person-hidden", OrganizationID: "org-hidden", MembershipID: "membership-hidden", MembershipRevision: 1, JoinedAt: time.Now().UTC()}
	viewer := StrideE10ProductPrincipal{PersonID: "person-viewer", ActiveOrganizationID: "org-shared", OrganizationMembershipID: "membership-viewer", OrganizationMembershipRev: 2, ActiveOrganizationSessionRev: 1}
	projection, err := runtime.projectTarget(viewer, "coworker-profile", "person-target")
	if err != nil || !strideE10ProjectionBindsSingleTarget(projection, "person-target") {
		t.Fatalf("target projection err=%v value=%+v", err, projection)
	}
	if _, err := runtime.projectTarget(viewer, "coworker-profile", "person-hidden"); !errors.Is(err, ErrStrideE10NotFound) {
		t.Fatalf("cross-org target err=%v", err)
	}
}

func TestStrideE10ProductLiveProjectionDenialsAreOpaqueNotEmptyAvailable(t *testing.T) {
	runtime := NewStrideE10ProductLiveRuntime(nil)
	if _, err := runtime.project(StrideE10ProductPrincipal{PersonID: "person-no-scope"}, "work-record"); !errors.Is(err, ErrStrideE10NotFound) {
		t.Fatalf("unknown person work record err=%v", err)
	}
	runtime.organization.memberships["membership-member"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership-member", Revision: 1}, PersonID: "person-member", OrganizationID: "org-one", Role: "member", Status: "active"}
	principal := StrideE10ProductPrincipal{PersonID: "person-member", ActiveOrganizationID: "org-one", OrganizationMembershipID: "membership-member", OrganizationMembershipRev: 1, ActiveOrganizationSessionRev: 1}
	if _, err := runtime.project(principal, "organization-recruiting"); !errors.Is(err, ErrStrideE10NotFound) {
		t.Fatalf("non-admin recruiting projection err=%v", err)
	}
}

func TestStrideE10ProductLiveCurrentPersonOwnsEmptyPrivateWorkRecord(t *testing.T) {
	runtime := NewStrideE10ProductLiveRuntime(nil)
	runtime.organization.persons["person-empty-record"] = PersonPrincipal{Status: "active"}
	runtime.organization.profiles["person-empty-record"] = PersonProfile{PersonID: "person-empty-record", DisplayName: "Current person", Status: "active"}
	projection, err := runtime.project(StrideE10ProductPrincipal{PersonID: "person-empty-record"}, "work-record")
	if err != nil || strideE10ValidateMobileProjection(projection, "work-record") != nil {
		t.Fatalf("empty private work record err=%v projection=%+v", err, projection)
	}
	items, ok := projection["items"].([]map[string]any)
	if !ok || len(items) != 6 {
		t.Fatalf("empty private work record items=%T %+v", projection["items"], projection["items"])
	}
	for _, item := range items {
		detail, ok := item["detail"].(map[string]any)
		if !ok || detail["kind"] != "work-record-section" || len(detail["entries"].([]string)) != 0 {
			t.Fatalf("nonempty or malformed private section: %+v", item)
		}
	}
}

func TestStrideE10ProductLiveContactAcceptanceAndGrantRevocationUseBoundAuthority(t *testing.T) {
	contactFixture := newNetworkAuthorityFixture(t)
	admission := NetworkContactAdmission{GrantRef: referenceFromHeader(contactFixture.grant.Header), SenderPersonID: contactFixture.grant.SearcherPersonID, SenderOrganizationID: contactFixture.grant.OrganizationID, MembershipID: contactFixture.grant.MembershipID, MembershipRevision: contactFixture.grant.MembershipRevision, RecipientProjection: referenceFromHeader(contactFixture.profile.Header), Purpose: "discuss_growth_work", NoteDigest: strideTestDigest("d"), CollaborationType: "collaboration", ExpiresAt: contactFixture.now.Add(12 * time.Hour), IdempotencyKeyDigest: strideTestDigest("e"), At: contactFixture.now.Add(3 * time.Minute)}
	contact, _, err := contactFixture.service.CreateContact(admission)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return contactFixture.now.Add(4 * time.Minute) })
	runtime.network = contactFixture.service
	recipient := STRIDEControllerRevision{PrincipalID: contactFixture.profile.SubjectPersonID, AuthorityID: "recipient_contact", AuthorityRevision: 1, PolicyRevision: 1}
	binding := StrideE10LiveActionBinding{Type: "contact-accept", ContactActor: &recipient, Target: referenceFromHeader(contact.Header), AcceptedChannelDigest: strideTestDigest("f")}
	command := StrideE10ProductCommand{ExpectedRevision: contact.Header.Revision, IdempotencyKey: "accept-contact"}
	if _, err := runtime.executeBoundAction(context.Background(), StrideE10ProductPrincipal{PersonID: recipient.PrincipalID}, command, binding); err != nil {
		t.Fatal(err)
	}
	contactFixture.service.mu.Lock()
	accepted := contactFixture.service.contacts[contact.Header.ID]
	contactFixture.service.mu.Unlock()
	if accepted.State != "accepted" || accepted.AcceptedChannelDigest != binding.AcceptedChannelDigest {
		t.Fatalf("accepted contact=%+v", accepted)
	}
	missingDigest := binding
	missingDigest.AcceptedChannelDigest = ""
	if _, err := runtime.executeBoundAction(context.Background(), StrideE10ProductPrincipal{PersonID: recipient.PrincipalID}, command, missingDigest); !errors.Is(err, ErrStrideE10Invalid) {
		t.Fatalf("accept without server channel digest err=%v", err)
	}

	grantFixture := newNetworkAuthorityFixture(t)
	runtime.network = grantFixture.service
	revoked := cloneContract(grantFixture.grant)
	revoked.Header = nextAuthorityHeader(revoked.Header, "revoke", grantFixture.now.Add(5*time.Minute))
	revoked.State = "revoked"
	revokedAt := grantFixture.now.Add(5 * time.Minute)
	revoked.RevokedAt = &revokedAt
	grantBinding := StrideE10LiveActionBinding{Type: "organization-recruiting-grant-revoke", TalentAssertion: &grantFixture.capabilityAssertion, TalentGrant: &revoked, Target: referenceFromHeader(grantFixture.grant.Header)}
	if _, err := runtime.executeBoundAction(context.Background(), StrideE10ProductPrincipal{PersonID: grantFixture.capabilityAuthority.ControllerPersonID}, StrideE10ProductCommand{ExpectedRevision: 99, IdempotencyKey: "grant-revoke"}, grantBinding); err != nil {
		t.Fatal(err)
	}
	grantFixture.service.mu.Lock()
	storedGrant := grantFixture.service.grants[grantFixture.grant.Header.ID]
	grantFixture.service.mu.Unlock()
	if storedGrant.State != "revoked" || storedGrant.Header.Revision != grantFixture.grant.Header.Revision+1 {
		t.Fatalf("revoked grant=%+v", storedGrant)
	}
}

func TestStrideE10ProductLiveSearchableFieldsAndOffFenceExactCurrentProfile(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return fixture.now.Add(4 * time.Minute) })
	runtime.network = fixture.service
	updated := cloneNetworkProjection(fixture.profile)
	updated.Header = nextAuthorityHeader(updated.Header, "searchable_fields", fixture.now.Add(3*time.Minute))
	updated.StateChangedAt = fixture.now.Add(3 * time.Minute)
	binding := StrideE10LiveActionBinding{Type: "network-searchable-fields-update", NetworkActor: &fixture.personController, NetworkProfile: &updated, Target: referenceFromHeader(fixture.profile.Header)}
	if _, err := runtime.executeBoundAction(context.Background(), StrideE10ProductPrincipal{PersonID: fixture.profile.SubjectPersonID}, StrideE10ProductCommand{ExpectedRevision: 99, IdempotencyKey: "searchable-fields"}, binding); err != nil {
		t.Fatal(err)
	}
	off := cloneNetworkProjection(updated)
	off.Header = nextAuthorityHeader(off.Header, "off", fixture.now.Add(4*time.Minute))
	off.State = "off"
	off.Discoverability = "unlisted"
	off.PurgeGeneration++
	off.StateChangedAt = fixture.now.Add(4 * time.Minute)
	offBinding := StrideE10LiveActionBinding{Type: "network-profile-off", NetworkActor: &fixture.personController, NetworkProfile: &off, Target: referenceFromHeader(updated.Header)}
	if _, err := runtime.executeBoundAction(context.Background(), StrideE10ProductPrincipal{PersonID: fixture.profile.SubjectPersonID}, StrideE10ProductCommand{ExpectedRevision: 99, IdempotencyKey: "profile-off"}, offBinding); err != nil {
		t.Fatal(err)
	}
	malicious := cloneNetworkProjection(off)
	malicious.State = "deleted"
	maliciousBinding := offBinding
	maliciousBinding.NetworkProfile = &malicious
	if _, err := runtime.executeBoundAction(context.Background(), StrideE10ProductPrincipal{PersonID: fixture.profile.SubjectPersonID}, StrideE10ProductCommand{ExpectedRevision: off.Header.Revision, IdempotencyKey: "profile-off-malicious"}, maliciousBinding); !errors.Is(err, ErrStrideE10Invalid) {
		t.Fatalf("network-profile-off accepted destructive deleted binding: %v", err)
	}
	crossProfile := cloneNetworkProjection(off)
	crossProfile.Header.ID = "network_profile_other_same_revision"
	crossProfile.Header.ContentDigest = off.Header.ContentDigest
	crossProfileBinding := offBinding
	crossProfileBinding.NetworkProfile = &crossProfile
	if _, err := runtime.executeBoundAction(context.Background(), StrideE10ProductPrincipal{PersonID: fixture.profile.SubjectPersonID}, StrideE10ProductCommand{ExpectedRevision: off.Header.Revision, IdempotencyKey: "profile-off-cross-profile"}, crossProfileBinding); !errors.Is(err, ErrStrideE10NotFound) {
		t.Fatalf("network-profile-off accepted cross-profile same-revision binding: %v", err)
	}
	fixture.service.mu.Lock()
	stored := fixture.service.profiles[fixture.profile.Header.ID]
	var authorityPurge DerivedPurgeReceipt
	for _, value := range fixture.service.purges {
		authorityPurge = value
	}
	fixture.service.mu.Unlock()
	if stored.State != "off" || stored.Header.Revision != off.Header.Revision {
		t.Fatalf("off profile=%+v", stored)
	}
	receipt, ok := runtime.purges[fixture.profile.SubjectPersonID+"\x00network-preview"]
	if !ok || receipt["status"] != "queued" {
		t.Fatalf("off purge=%+v", receipt)
	}
	if err := authorityPurge.Validate(); err != nil {
		t.Fatalf("off purge is not exact full-store receipt: %v %+v", err, authorityPurge)
	}
	searchReceipt, _, err := fixture.service.Search(fixture.searchRequest("after-runtime-off", "people who solved growth problems", networkFilter("problem_class", "growth")))
	if err != nil || len(searchReceipt.Results) != 0 {
		t.Fatalf("off profile remained discoverable: %+v err=%v", searchReceipt, err)
	}
}

func ptrSTRIDEReference(value STRIDEReference) *STRIDEReference { return &value }
