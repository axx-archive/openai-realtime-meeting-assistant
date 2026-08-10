package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type strideE10W5ManagedHTTPSTestFixture struct {
	testing     *testing.T
	now         time.Time
	root        string
	ca          *x509.Certificate
	caKey       ed25519.PrivateKey
	expectation StrideE10W5ManagedProductionExpectation
	config      StrideE10W5ManagedHTTPSProviderConfig
	server      *httptest.Server
	serverTLS   tls.Certificate
	clientTLS   tls.Certificate
	caPool      *x509.CertPool
	mu          sync.Mutex
	operations  []string
	mutate      func(*strideE10W5ManagedHTTPSResponse)
	status      int
	rawResponse []byte
}

func newStrideE10W5ManagedHTTPSTestFixture(t *testing.T) *strideE10W5ManagedHTTPSTestFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	caCert, caKey, caPEM := strideE10W5ManagedHTTPSTestCA(t, now)
	serverTLS, serverCertificatePEM, serverKeyPEM := strideE10W5ManagedHTTPSTestCertificate(t, now, caCert, caKey, "custody-provider", true)
	clientTLS, clientCertificatePEM, clientKeyPEM := strideE10W5ManagedHTTPSTestCertificate(t, now, caCert, caKey, "meetingassist-client", false)
	_ = serverCertificatePEM
	_ = serverKeyPEM
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(root, "ca.pem")
	certPath := filepath.Join(root, "client.pem")
	keyPath := filepath.Join(root, "client-key.pem")
	if err := os.WriteFile(caPath, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, clientCertificatePEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, clientKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)
	expectation := strideE10W5TestManagedExpectation(filepath.Join(root, "custody", "mymind.json"))
	fixture := &strideE10W5ManagedHTTPSTestFixture{testing: t, now: now, root: root, ca: caCert, caKey: caKey, expectation: expectation, serverTLS: serverTLS, clientTLS: clientTLS, caPool: caPool}
	server := httptest.NewUnstartedServer(http.HandlerFunc(fixture.serveHTTP))
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverTLS},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	serverLeaf, err := x509.ParseCertificate(serverTLS.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	spki := sha256.Sum256(serverLeaf.RawSubjectPublicKeyInfo)
	clientLeaf, err := x509.ParseCertificate(clientTLS.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	clientSPKI := sha256.Sum256(clientLeaf.RawSubjectPublicKeyInfo)
	fixture.server = server
	fixture.config = StrideE10W5ManagedHTTPSProviderConfig{
		Endpoint:              server.URL,
		CAPath:                caPath,
		ClientCertificatePath: certPath,
		ClientKeyPath:         keyPath,
		ClientSPKISHA256:      hex.EncodeToString(clientSPKI[:]),
		ServerSPKISHA256:      hex.EncodeToString(spki[:]),
		Timeout:               5 * time.Second,
	}
	return fixture
}

func strideE10W5ManagedHTTPSTestCA(t *testing.T, now time.Time) (*x509.Certificate, ed25519.PrivateKey, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "stride-w5-test-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, privateKey, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func strideE10W5ManagedHTTPSTestCertificate(t *testing.T, now time.Time, ca *x509.Certificate, caKey ed25519.PrivateKey, commonName string, server bool) (tls.Certificate, []byte, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	usage := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usage,
	}
	if server {
		template.SerialNumber = big.NewInt(3)
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, publicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	pair, err := tls.X509KeyPair(certificatePEM, privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	return pair, certificatePEM, privatePEM
}

func (f *strideE10W5ManagedHTTPSTestFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != strideE10W5ManagedHTTPSRPCPath || r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
		http.Error(w, "denied", http.StatusForbidden)
		return
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request strideE10W5ManagedHTTPSRequest
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF || request.Schema != strideE10W5ManagedHTTPSRPCSchema || request.Expectation == nil || *request.Expectation != f.expectation {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.operations = append(f.operations, request.Operation)
	f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	response := strideE10W5ManagedHTTPSResponse{Schema: strideE10W5ManagedHTTPSRPCSchema, Operation: request.Operation}
	acknowledged := true
	switch request.Operation {
	case "preflight":
		attestation := strideE10W5TestManagedAttestation(f.expectation, f.now)
		response.Attestation = &attestation
	case "state_key_current", "state_key_resolve":
		response.StateKey = &strideE10W5ManagedHTTPSStateKey{ID: f.expectation.StateKeyID, Version: f.expectation.StateKeyVersion, Material: []byte(strings.Repeat("s", 32))}
	case "high_water_read":
		response.HighWater = &strideE10W5ManagedHTTPSHighWater{}
	case "high_water_advance", "verify_destruction":
		response.Acknowledged = &acknowledged
	case "custody_key_current", "custody_key_resolve":
		id := request.KeyID
		version := request.KeyVersion
		if id == "" {
			id = "mymind_source_key"
			version = 1
		}
		response.CustodyKey = &strideE10W5ManagedHTTPSCustodyKey{ID: id, Version: version, PersonID: request.PersonID, SourceID: request.SourceID, Material: []byte(strings.Repeat("k", 32))}
	case "destroy_source", "destroy_person":
		scope := "person"
		if request.Operation == "destroy_source" {
			scope = "source"
		}
		receipt := MyMindKeyDestructionReceipt{Schema: "stride.mymind.key-destruction.v1", OperationID: request.OperationID, Scope: scope, PersonID: request.PersonID, SourceID: request.SourceID, KeyRefsDigest: myMindKeyRefsDigest(request.KeyRefs), EvidenceKeyID: f.expectation.DestructionEvidenceKeyID, EvidenceVersion: f.expectation.DestructionEvidenceVersion, DestroyedAt: f.now, VerificationContract: "managed_keyring_v1"}
		receipt.MAC = myMindDestructionMAC([]byte(strings.Repeat("d", 32)), receipt)
		receipt.ReceiptDigest = myMindDestructionReceiptDigest(receipt)
		response.Receipt = &receipt
	default:
		http.Error(w, "unknown", http.StatusBadRequest)
		return
	}
	if f.mutate != nil {
		f.mutate(&response)
	}
	w.Header().Set("Content-Type", "application/json")
	if f.rawResponse != nil {
		_, _ = w.Write(f.rawResponse)
		return
	}
	_ = json.NewEncoder(w).Encode(response)
}

func (f *strideE10W5ManagedHTTPSTestFixture) setEnvironment(t *testing.T) {
	t.Helper()
	setStrideE10W5ManagedEnvironment(t, f.expectation)
	t.Setenv("STRIDE_E10_W5_MANAGED_PROVIDER_URL", f.config.Endpoint)
	t.Setenv("STRIDE_E10_W5_MANAGED_PROVIDER_CA_PATH", f.config.CAPath)
	t.Setenv("STRIDE_E10_W5_MANAGED_PROVIDER_CLIENT_CERT_PATH", f.config.ClientCertificatePath)
	t.Setenv("STRIDE_E10_W5_MANAGED_PROVIDER_CLIENT_KEY_PATH", f.config.ClientKeyPath)
	t.Setenv("STRIDE_E10_W5_MANAGED_PROVIDER_CLIENT_SPKI_SHA256", f.config.ClientSPKISHA256)
	t.Setenv("STRIDE_E10_W5_MANAGED_PROVIDER_SERVER_SPKI_SHA256", f.config.ServerSPKISHA256)
}

func TestStrideE10W5ManagedHTTPSProviderExactClosedOperations(t *testing.T) {
	fixture := newStrideE10W5ManagedHTTPSTestFixture(t)
	providerValue, err := NewStrideE10W5ManagedHTTPSProvider(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	provider := providerValue.(*strideE10W5ManagedHTTPSProvider)
	adapters, attestation, err := provider.PreflightStrideE10W5ManagedProduction(context.Background(), fixture.expectation)
	if err != nil || validateStrideE10W5ManagedAttestation(fixture.expectation, attestation, fixture.now) != nil {
		t.Fatalf("managed HTTPS preflight failed: attestation=%+v err=%v", attestation, err)
	}
	state, err := adapters.StateKeys.CurrentMyMindCustodyStateKey(context.Background())
	if err != nil || state.ID != fixture.expectation.StateKeyID || state.Version != fixture.expectation.StateKeyVersion {
		t.Fatalf("current state key mismatch: %+v err=%v", state, err)
	}
	if _, err := adapters.StateKeys.ResolveMyMindCustodyStateKey(context.Background(), state.ID, state.Version); err != nil {
		t.Fatal(err)
	}
	water, err := adapters.HighWater.ReadMyMindCustodyHighWater(context.Background(), fixture.expectation.StatePath)
	if err != nil || water.Generation != 0 {
		t.Fatalf("high-water read mismatch: %+v err=%v", water, err)
	}
	next := MyMindCustodyHighWater{Generation: 1, PayloadDigest: strings.Repeat("a", 64)}
	if err := adapters.HighWater.AdvanceMyMindCustodyHighWater(context.Background(), fixture.expectation.StatePath, water, next); err != nil {
		t.Fatal(err)
	}
	key, err := adapters.Keys.CurrentMyMindCustodyKey(context.Background(), "person_one", "source_one")
	if err != nil || !key.valid("person_one", "source_one") {
		t.Fatalf("current custody key mismatch: %+v err=%v", key, err)
	}
	if _, err := adapters.Keys.ResolveMyMindCustodyKey(context.Background(), key.PersonID, key.SourceID, key.ID, key.Version); err != nil {
		t.Fatal(err)
	}
	refs := []myMindCustodyKeyRef{{ID: key.ID, Version: key.Version}}
	sourceReceipt, err := adapters.Keys.DestroySourceMyMindKeys(context.Background(), "destroy_source_one", key.PersonID, key.SourceID, refs)
	if err != nil || !validMyMindDestructionReceipt(sourceReceipt, "destroy_source_one", "source", key.PersonID, key.SourceID, refs) {
		t.Fatalf("source destruction mismatch: %+v err=%v", sourceReceipt, err)
	}
	personReceipt, err := adapters.Keys.DestroyPersonMyMindKeys(context.Background(), "destroy_person_one", key.PersonID, refs)
	if err != nil || !validMyMindDestructionReceipt(personReceipt, "destroy_person_one", "person", key.PersonID, "", refs) {
		t.Fatalf("person destruction mismatch: %+v err=%v", personReceipt, err)
	}
	if err := adapters.Keys.VerifyMyMindKeyDestruction(context.Background(), personReceipt); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	operations := append([]string(nil), fixture.operations...)
	fixture.mu.Unlock()
	want := []string{"preflight", "state_key_current", "state_key_resolve", "high_water_read", "high_water_advance", "custody_key_current", "custody_key_resolve", "destroy_source", "destroy_person", "verify_destruction"}
	if strings.Join(operations, ",") != strings.Join(want, ",") {
		t.Fatalf("managed provider operation sequence mismatch: got %v want %v", operations, want)
	}
}

func TestStrideE10W5ManagedHTTPSProviderFailsClosedOnTransportCredentialsAndProtocol(t *testing.T) {
	fixture := newStrideE10W5ManagedHTTPSTestFixture(t)
	plain := fixture.config
	plain.Endpoint = strings.Replace(plain.Endpoint, "https://", "http://", 1)
	if _, err := NewStrideE10W5ManagedHTTPSProvider(plain); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("plain HTTP provider was accepted: %v", err)
	}

	wrongPin := fixture.config
	wrongPin.ServerSPKISHA256 = strings.Repeat("f", 64)
	value, err := NewStrideE10W5ManagedHTTPSProvider(wrongPin)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := value.PreflightStrideE10W5ManagedProduction(context.Background(), fixture.expectation); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("wrong server pin reached preflight: %v", err)
	}

	symlinkPath := filepath.Join(t.TempDir(), "client-key.pem")
	if err := os.Symlink(fixture.config.ClientKeyPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	symlinked := fixture.config
	symlinked.ClientKeyPath = symlinkPath
	if _, err := NewStrideE10W5ManagedHTTPSProvider(symlinked); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("symlinked client key was accepted: %v", err)
	}

	linkedPath := filepath.Join(t.TempDir(), "client-key.pem")
	if err := os.Link(fixture.config.ClientKeyPath, linkedPath); err != nil {
		t.Fatal(err)
	}
	linked := fixture.config
	linked.ClientKeyPath = linkedPath
	if _, err := NewStrideE10W5ManagedHTTPSProvider(linked); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("hard-linked client key was accepted: %v", err)
	}

	rotatedTLS, rotatedCertificatePEM, rotatedKeyPEM := strideE10W5ManagedHTTPSTestCertificate(t, fixture.now, fixture.ca, fixture.caKey, "meetingassist-client-rotated", false)
	rotatedCertificatePath := filepath.Join(fixture.root, "client-rotated.pem")
	rotatedKeyPath := filepath.Join(fixture.root, "client-rotated-key.pem")
	if err := os.WriteFile(rotatedCertificatePath, rotatedCertificatePEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rotatedKeyPath, rotatedKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	rotated := fixture.config
	rotated.ClientCertificatePath = rotatedCertificatePath
	rotated.ClientKeyPath = rotatedKeyPath
	if _, err := NewStrideE10W5ManagedHTTPSProvider(rotated); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("valid but unpinned rotated client certificate was accepted: %v", err)
	}
	rotatedLeaf, err := x509.ParseCertificate(rotatedTLS.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	rotatedSPKI := sha256.Sum256(rotatedLeaf.RawSubjectPublicKeyInfo)
	rotated.ClientSPKISHA256 = hex.EncodeToString(rotatedSPKI[:])
	rotatedProvider, err := NewStrideE10W5ManagedHTTPSProvider(rotated)
	if err != nil {
		t.Fatalf("operator-pinned client certificate rotation was denied: %v", err)
	}
	if _, _, err := rotatedProvider.PreflightStrideE10W5ManagedProduction(context.Background(), fixture.expectation); err != nil {
		t.Fatalf("operator-pinned rotated client certificate could not authenticate: %v", err)
	}
}

func TestStrideE10W5ManagedHTTPSCredentialReadRejectsInPlaceMetadataAndContentRaces(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "client-key.pem")
	original := []byte("credential-material-one")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := readStrideE10W5ManagedCredential(path, true)
	if err != nil || !bytes.Equal(value, original) {
		t.Fatalf("stable private credential was denied: %v", err)
	}
	clear(value)
	if _, err := readStrideE10W5ManagedCredentialWithHook(path, true, func() error {
		return os.Chmod(path, 0o644)
	}); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("same-inode private credential chmod was accepted: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	replacement := []byte("credential-material-two")
	if len(replacement) != len(original) {
		t.Fatal("credential race fixture must preserve file size")
	}
	if _, err := readStrideE10W5ManagedCredentialWithHook(path, true, func() error {
		return os.WriteFile(path, replacement, 0o600)
	}); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("same-inode in-place credential rewrite was accepted: %v", err)
	}
}

func TestStrideE10W5ManagedHTTPSProviderRejectsClosedProtocolDriftAndConflict(t *testing.T) {
	fixture := newStrideE10W5ManagedHTTPSTestFixture(t)
	providerValue, err := NewStrideE10W5ManagedHTTPSProvider(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	fixture.mutate = func(response *strideE10W5ManagedHTTPSResponse) {
		if response.Operation == "preflight" {
			key := strideE10W5ManagedHTTPSStateKey{ID: fixture.expectation.StateKeyID, Version: fixture.expectation.StateKeyVersion, Material: []byte(strings.Repeat("s", 32))}
			response.StateKey = &key
		}
	}
	if _, _, err := providerValue.PreflightStrideE10W5ManagedProduction(context.Background(), fixture.expectation); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("multi-payload preflight response was accepted: %v", err)
	}
	fixture.mutate = nil
	adapters, _, err := providerValue.PreflightStrideE10W5ManagedProduction(context.Background(), fixture.expectation)
	if err != nil {
		t.Fatal(err)
	}
	fixture.status = http.StatusConflict
	if err := adapters.HighWater.AdvanceMyMindCustodyHighWater(context.Background(), fixture.expectation.StatePath, MyMindCustodyHighWater{}, MyMindCustodyHighWater{Generation: 1, PayloadDigest: strings.Repeat("a", 64)}); !errors.Is(err, ErrMyMindCustodyConflict) {
		t.Fatalf("provider CAS conflict was not preserved: %v", err)
	}
}

func TestStrideE10W5ManagedHTTPSProviderRejectsUnknownTrailingAndOversizedResponses(t *testing.T) {
	fixture := newStrideE10W5ManagedHTTPSTestFixture(t)
	provider, err := NewStrideE10W5ManagedHTTPSProvider(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	fixture.rawResponse = []byte(`{"schema":"stride.mymind.managed-https-rpc.v1","operation":"preflight","unexpected":true}`)
	if _, _, err := provider.PreflightStrideE10W5ManagedProduction(context.Background(), fixture.expectation); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("unknown response field was accepted: %v", err)
	}
	fixture.rawResponse = []byte(`{"schema":"stride.mymind.managed-https-rpc.v1","operation":"preflight"}{}`)
	if _, _, err := provider.PreflightStrideE10W5ManagedProduction(context.Background(), fixture.expectation); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("trailing response object was accepted: %v", err)
	}
	fixture.rawResponse = []byte(`{"schema":"stride.mymind.managed-https-rpc.v1","operation":"preflight","padding":"` + strings.Repeat("x", strideE10W5ManagedHTTPSMaxBody) + `"}`)
	if _, _, err := provider.PreflightStrideE10W5ManagedProduction(context.Background(), fixture.expectation); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("oversized response was accepted: %v", err)
	}
}

func TestStrideE10W5ManagedHTTPSAdaptersRejectInvalidAuthorityBeforeNetwork(t *testing.T) {
	fixture := newStrideE10W5ManagedHTTPSTestFixture(t)
	provider, err := NewStrideE10W5ManagedHTTPSProvider(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	adapters, _, err := provider.PreflightStrideE10W5ManagedProduction(context.Background(), fixture.expectation)
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	before := len(fixture.operations)
	fixture.mu.Unlock()
	if _, err := adapters.HighWater.ReadMyMindCustodyHighWater(context.Background(), fixture.expectation.StatePath+"-other"); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("cross-path high-water read was accepted: %v", err)
	}
	if err := adapters.HighWater.AdvanceMyMindCustodyHighWater(context.Background(), fixture.expectation.StatePath, MyMindCustodyHighWater{}, MyMindCustodyHighWater{Generation: 2, PayloadDigest: strings.Repeat("a", 64)}); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("non-consecutive high-water advance was accepted: %v", err)
	}
	if _, err := adapters.Keys.CurrentMyMindCustodyKey(context.Background(), "invalid person", "source_one"); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("invalid person reached custody: %v", err)
	}
	duplicate := []myMindCustodyKeyRef{{ID: "key_one", Version: 1}, {ID: "key_one", Version: 1}}
	if _, err := adapters.Keys.DestroyPersonMyMindKeys(context.Background(), "destroy_invalid", "person_one", duplicate); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("duplicate destruction refs reached provider: %v", err)
	}
	fixture.mu.Lock()
	after := len(fixture.operations)
	fixture.mu.Unlock()
	if after != before {
		t.Fatalf("invalid authority reached provider: before=%d after=%d", before, after)
	}
}

func TestStrideE10W5ManagedHTTPSProviderProductionBootstrapIsDefaultOffAndCompiled(t *testing.T) {
	fixture := newStrideE10W5ManagedHTTPSTestFixture(t)
	fixture.setEnvironment(t)
	restoreProvider := installStrideE10W5ManagedProductionProvider(nil)
	defer restoreProvider()
	priorRuntime := strideE10LiveProductRuntime
	runtime := NewStrideE10ProductLiveRuntime(time.Now)
	strideE10LiveProductRuntime = runtime
	defer func() {
		uninstallStrideE10W5ProductRuntime()
		strideE10LiveProductRuntime = priorRuntime
	}()
	if err := installStrideE10W5ProductionRuntimeFromEnvironment(context.Background()); err != nil {
		t.Fatalf("compiled managed HTTPS provider did not install: %v", err)
	}
	if runtime.features[STRIDEFeaturePersonMyMindContext] {
		t.Fatal("compiled provider enabled person_mymind_context")
	}
	if snapshot := strideE10W5RuntimeReadinessSnapshot(); snapshot["installed"] != true || snapshot["configured"] != false || snapshot["ready"] != true {
		t.Fatalf("compiled provider readiness mismatch: %+v", snapshot)
	}
	if _, err := os.Stat(fixture.expectation.StatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty compiled-provider boot unexpectedly wrote state: %v", err)
	}
}
