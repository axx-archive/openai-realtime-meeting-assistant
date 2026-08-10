package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type strideE10W6ManagedHTTPSTestFixture struct {
	t            *testing.T
	runtime      strideE10W6RuntimeFixture
	expectation  StrideE10W6ManagedProductionExpectation
	config       StrideE10W6ManagedHTTPSProviderConfig
	server       *httptest.Server
	mu           sync.Mutex
	operations   []string
	operationIDs []string
	mutate       func(*strideE10W6ManagedHTTPSResponse)
	rawResponse  []byte
	contentType  string
}

func newStrideE10W6ManagedHTTPSTestFixture(t *testing.T) *strideE10W6ManagedHTTPSTestFixture {
	t.Helper()
	isolateStrideE10W6ProductionAdapterTest(t)
	runtime := newStrideE10W6RuntimeFixture(t)
	strideE10LiveProductRuntime = runtime.live
	expectation := strideE10W6ProductionExpectation(t, runtime)
	certNow := time.Now().UTC()
	ca, caKey, caPEM := strideE10W5ManagedHTTPSTestCA(t, certNow)
	serverTLS, _, _ := strideE10W5ManagedHTTPSTestCertificate(t, certNow, ca, caKey, "w6-provider", true)
	clientTLS, clientPEM, clientKeyPEM := strideE10W5ManagedHTTPSTestCertificate(t, certNow, ca, caKey, "w6-client", false)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	caPath, certPath, keyPath := filepath.Join(root, "ca.pem"), filepath.Join(root, "client.pem"), filepath.Join(root, "client-key.pem")
	if os.WriteFile(caPath, caPEM, 0o644) != nil || os.WriteFile(certPath, clientPEM, 0o644) != nil || os.WriteFile(keyPath, clientKeyPEM, 0o600) != nil {
		t.Fatal("write W6 test credentials")
	}
	fixture := &strideE10W6ManagedHTTPSTestFixture{t: t, runtime: runtime, expectation: expectation}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	server := httptest.NewUnstartedServer(http.HandlerFunc(fixture.serveHTTP))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverTLS}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	server.StartTLS()
	t.Cleanup(server.Close)
	serverLeaf, _ := x509.ParseCertificate(serverTLS.Certificate[0])
	clientLeaf, _ := x509.ParseCertificate(clientTLS.Certificate[0])
	serverSPKI, clientSPKI := sha256.Sum256(serverLeaf.RawSubjectPublicKeyInfo), sha256.Sum256(clientLeaf.RawSubjectPublicKeyInfo)
	fixture.server = server
	fixture.config = StrideE10W6ManagedHTTPSProviderConfig{Endpoint: server.URL, CAPath: caPath, ClientCertificatePath: certPath, ClientKeyPath: keyPath, ClientSPKISHA256: hex.EncodeToString(clientSPKI[:]), ServerSPKISHA256: hex.EncodeToString(serverSPKI[:]), Timeout: 5 * time.Second}
	return fixture
}

func (f *strideE10W6ManagedHTTPSTestFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != strideE10W6ManagedHTTPSRPCPath || r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
		http.Error(w, "denied", http.StatusForbidden)
		return
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request strideE10W6ManagedHTTPSRequest
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF || request.Schema != strideE10W6ManagedHTTPSRPCSchema || request.Expectation == nil || *request.Expectation != f.expectation {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.operations = append(f.operations, request.Operation)
	f.operationIDs = append(f.operationIDs, request.OperationID)
	f.mu.Unlock()
	if f.rawResponse != nil {
		contentType := f.contentType
		if contentType == "" {
			contentType = "application/json"
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(f.rawResponse)
		return
	}
	response := strideE10W6ManagedHTTPSResponse{Schema: strideE10W6ManagedHTTPSRPCSchema, Operation: request.Operation}
	switch request.Operation {
	case "preflight":
		attestation := strideE10W6ProductionAttestation(f.expectation, f.runtime.config.Now().UTC())
		key := cloneStrideE10W6ManagedKey(f.runtime.config.Key)
		response.Attestation, response.Key = &attestation, &key
		response.RetainedKeys = cloneStrideE10W6ManagedKeys(f.runtime.config.RetainedKeys)
	case "purge_store":
		if request.Receipt == nil || request.Receipt.Validate() != nil || !oneOf(request.Store, contributionPurgeStores...) || !isHexDigest(request.OperationID) {
			http.Error(w, "invalid purge", http.StatusBadRequest)
			return
		}
		digest, _ := STRIDEContractDigest(*request.Receipt)
		ack := true
		response.Acknowledged, response.OperationID, response.ReceiptDigest, response.Store = &ack, request.OperationID, digest, request.Store
	default:
		http.Error(w, "unknown", http.StatusBadRequest)
		return
	}
	if f.mutate != nil {
		f.mutate(&response)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (f *strideE10W6ManagedHTTPSTestFixture) setEnvironment(t *testing.T) {
	setStrideE10W6ManagedEnvironment(t, f.expectation)
	values := map[string]string{
		"STRIDE_E10_W6_MANAGED_PROVIDER_URL":                f.config.Endpoint,
		"STRIDE_E10_W6_MANAGED_PROVIDER_CA_PATH":            f.config.CAPath,
		"STRIDE_E10_W6_MANAGED_PROVIDER_CLIENT_CERT_PATH":   f.config.ClientCertificatePath,
		"STRIDE_E10_W6_MANAGED_PROVIDER_CLIENT_KEY_PATH":    f.config.ClientKeyPath,
		"STRIDE_E10_W6_MANAGED_PROVIDER_CLIENT_SPKI_SHA256": f.config.ClientSPKISHA256,
		"STRIDE_E10_W6_MANAGED_PROVIDER_SERVER_SPKI_SHA256": f.config.ServerSPKISHA256,
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
}

func TestStrideE10W6ManagedHTTPSProviderPreflightPurgeAndStartupFallback(t *testing.T) {
	f := newStrideE10W6ManagedHTTPSTestFixture(t)
	providerValue, err := NewStrideE10W6ManagedHTTPSProvider(f.config)
	if err != nil {
		t.Fatal(err)
	}
	provider := providerValue.(*strideE10W6ManagedHTTPSProvider)
	adapters, attestation, err := provider.PreflightStrideE10W6ManagedProduction(context.Background(), f.expectation)
	if err != nil || adapters.Sessions == nil || adapters.PurgeExecutor == nil || attestation.AdapterID != f.expectation.AdapterID || adapters.Key.ID != f.expectation.KeyID || adapters.Key.Version != f.expectation.KeyVersion {
		t.Fatalf("managed preflight failed: adapters=%+v attestation=%+v err=%v", adapters, attestation, err)
	}
	receipt := strideE10W6QueuedPurgeReceipt(f.runtime.config.Now())
	for i := 0; i < 2; i++ {
		if err := adapters.PurgeExecutor.PurgeSTRIDENetworkShadowStore(context.Background(), receipt, contributionPurgeStores[0]); err != nil {
			t.Fatalf("idempotent purge %d: %v", i, err)
		}
	}
	f.mu.Lock()
	if len(f.operationIDs) != 3 || f.operationIDs[1] == "" || f.operationIDs[1] != f.operationIDs[2] {
		t.Fatalf("purge operation identity drift: %+v", f.operationIDs)
	}
	f.mu.Unlock()

	// With no test-only provider installed, managed startup must use the
	// compiled HTTPS provider and still leave every W6 product switch off.
	f.setEnvironment(t)
	if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); err != nil {
		t.Fatalf("compiled provider startup: %v", err)
	}
	if ready := strideE10W6RuntimeReadinessSnapshot(); !ready.Ready || !ready.Installed || !ready.CanonicalBound {
		t.Fatalf("compiled provider did not bind runtime: %+v", ready)
	}
	for _, feature := range append(append([]STRIDEFeature{}, strideE10W6ActivationSwitches...), strideE10W6AlwaysDisabledSwitches...) {
		if f.runtime.live.Enabled(feature) {
			t.Fatalf("compiled provider enabled %s", feature)
		}
	}
}

func TestStrideE10W6ManagedHTTPSProviderFailsClosedOnPinsAndResponseDrift(t *testing.T) {
	f := newStrideE10W6ManagedHTTPSTestFixture(t)
	badClient := f.config
	badClient.ClientSPKISHA256 = strings.Repeat("0", 64)
	if _, err := NewStrideE10W6ManagedHTTPSProvider(badClient); err == nil {
		t.Fatal("wrong client pin accepted")
	}
	badServer := f.config
	badServer.ServerSPKISHA256 = strings.Repeat("1", 64)
	provider, err := NewStrideE10W6ManagedHTTPSProvider(badServer)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.PreflightStrideE10W6ManagedProduction(context.Background(), f.expectation); err == nil {
		t.Fatal("wrong server pin authenticated")
	}
	assertDenied := func(name string, raw []byte, contentType string) {
		t.Helper()
		f.rawResponse, f.contentType = raw, contentType
		provider, err = NewStrideE10W6ManagedHTTPSProvider(f.config)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := provider.PreflightStrideE10W6ManagedProduction(context.Background(), f.expectation); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	assertDenied("unknown response field", []byte(`{"schema":"stride.e10.w6.managed-https-rpc.v1","operation":"preflight","unknown":true}`), "application/json")
	valid, _ := json.Marshal(strideE10W6ManagedHTTPSResponse{Schema: strideE10W6ManagedHTTPSRPCSchema, Operation: "preflight", Attestation: ptrStrideE10W6Attestation(strideE10W6ProductionAttestation(f.expectation, f.runtime.config.Now().UTC())), Key: ptrStrideE10W6Key(f.runtime.config.Key)})
	assertDenied("MIME spoof", valid, "application/jsonp")
	assertDenied("oversized trailing whitespace", append(valid, []byte(strings.Repeat(" ", strideE10W6ManagedHTTPSMaxBody))...), "application/json")
	extraKnown := strideE10W6ManagedHTTPSResponse{Schema: strideE10W6ManagedHTTPSRPCSchema, Operation: "preflight", Attestation: ptrStrideE10W6Attestation(strideE10W6ProductionAttestation(f.expectation, f.runtime.config.Now().UTC())), Key: ptrStrideE10W6Key(f.runtime.config.Key), OperationID: strings.Repeat("a", 64)}
	extraKnownBody, _ := json.Marshal(extraKnown)
	assertDenied("operation-inapplicable known field", extraKnownBody, "application/json")
}

func ptrStrideE10W6Attestation(value StrideE10W6ManagedProductionAttestation) *StrideE10W6ManagedProductionAttestation {
	return &value
}

func ptrStrideE10W6Key(value W6ManagedMACKey) *W6ManagedMACKey { return &value }

func TestStrideE10W6MainSessionAuthorityHoldsCurrentSessionAndOrganization(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	service, organization, _, member, _ := strideE10OrganizationProductFixture(t)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	runtime.organization = service
	sessionHash := strings.Repeat("a", 64)
	expires := now.Add(time.Hour)
	active := ActiveOrganizationSession{Header: strideE10LiveHeader(STRIDEContractActiveOrganizationSession, STRIDEGlobalPersonTenant, "active_session_w6_local", 1, "w6-local-session", now), SessionSubjectDigest: sessionHash, PersonID: member.PersonID, OrganizationID: organization.Header.ID, MembershipID: member.Header.ID, MembershipRevision: member.Header.Revision, SessionRevision: 1, Status: "active", BoundAt: now, ExpiresAt: expires}
	service.sessions[sessionHash] = active
	person := service.persons[member.PersonID]
	store := &sessionStore{sessions: map[string]sessionRecord{sessionHash: {Email: "member@example.com", Expires: expires, PersonID: member.PersonID, AccountSubjectDigest: person.AccountSubjectDigest, AuthorityGeneration: 1, ActiveOrganizationID: organization.Header.ID, OrganizationMembershipID: member.Header.ID, OrganizationMembershipRev: member.Header.Revision, ActiveOrganizationSessionRev: 1}}}
	authority := &strideE10W6MainSessionAuthority{runtime: runtime, sessions: store}
	entered, release, sessionLock, organizationLock := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- authority.WithCurrentStrideE10W6Session(context.Background(), organization.Header.ID, sessionHash, func(current StrideE10W6CurrentSession) error {
			if current.PersonID != member.PersonID || current.MembershipID != member.Header.ID || current.ActiveOrganizationSessionID != active.Header.ID {
				t.Errorf("wrong current session: %+v", current)
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	go func() { store.mu.Lock(); store.mu.Unlock(); close(sessionLock) }()
	go func() { service.mu.Lock(); service.mu.Unlock(); close(organizationLock) }()
	select {
	case <-sessionLock:
		t.Fatal("session mutation interleaved with final use")
	case <-organizationLock:
		t.Fatal("organization mutation interleaved with final use")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-sessionLock:
	case <-time.After(time.Second):
		t.Fatal("session lock not released")
	}
	select {
	case <-organizationLock:
	case <-time.After(time.Second):
		t.Fatal("organization lock not released")
	}
	store.mu.Lock()
	record := store.sessions[sessionHash]
	record.ActiveOrganizationSessionRev++
	store.sessions[sessionHash] = record
	store.mu.Unlock()
	if err := authority.WithCurrentStrideE10W6Session(context.Background(), organization.Header.ID, sessionHash, func(StrideE10W6CurrentSession) error { return nil }); err == nil {
		t.Fatal("stale session revision authorized")
	}
}
