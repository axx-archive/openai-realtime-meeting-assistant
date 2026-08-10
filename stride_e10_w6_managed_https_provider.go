package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	strideE10W6ManagedHTTPSRPCSchema = "stride.e10.w6.managed-https-rpc.v1"
	strideE10W6ManagedHTTPSRPCPath   = "/v1/stride/network/qualification"
	strideE10W6ManagedHTTPSMaxBody   = 256 * 1024
)

// StrideE10W6ManagedHTTPSProviderConfig contains transport identity only.
// Session authority remains local and lock-held; the remote service owns only
// managed MAC keys, attestation, and idempotent derived-store purge effects.
type StrideE10W6ManagedHTTPSProviderConfig struct {
	Endpoint              string
	CAPath                string
	ClientCertificatePath string
	ClientKeyPath         string
	ClientSPKISHA256      string
	ServerSPKISHA256      string
	Timeout               time.Duration
}

type strideE10W6ManagedHTTPSProvider struct {
	endpoint string
	client   *http.Client
}

type strideE10W6ManagedHTTPSAdapters struct {
	provider    *strideE10W6ManagedHTTPSProvider
	expectation StrideE10W6ManagedProductionExpectation
}

type strideE10W6ManagedHTTPSRequest struct {
	Schema      string                                   `json:"schema"`
	Operation   string                                   `json:"operation"`
	Expectation *StrideE10W6ManagedProductionExpectation `json:"expectation"`
	Receipt     *DerivedPurgeReceipt                     `json:"receipt,omitempty"`
	Store       string                                   `json:"store,omitempty"`
	OperationID string                                   `json:"operationId,omitempty"`
}

type strideE10W6ManagedHTTPSResponse struct {
	Schema        string                                   `json:"schema"`
	Operation     string                                   `json:"operation"`
	Attestation   *StrideE10W6ManagedProductionAttestation `json:"attestation,omitempty"`
	Key           *W6ManagedMACKey                         `json:"key,omitempty"`
	RetainedKeys  []W6ManagedMACKey                        `json:"retainedKeys,omitempty"`
	OperationID   string                                   `json:"operationId,omitempty"`
	ReceiptDigest string                                   `json:"receiptDigest,omitempty"`
	Store         string                                   `json:"store,omitempty"`
	Acknowledged  *bool                                    `json:"acknowledged,omitempty"`
}

func strideE10W6ManagedHTTPSProviderFromEnvironment() (StrideE10W6ManagedProductionProvider, error) {
	return NewStrideE10W6ManagedHTTPSProvider(StrideE10W6ManagedHTTPSProviderConfig{
		Endpoint:              strings.TrimSpace(os.Getenv("STRIDE_E10_W6_MANAGED_PROVIDER_URL")),
		CAPath:                strings.TrimSpace(os.Getenv("STRIDE_E10_W6_MANAGED_PROVIDER_CA_PATH")),
		ClientCertificatePath: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_MANAGED_PROVIDER_CLIENT_CERT_PATH")),
		ClientKeyPath:         strings.TrimSpace(os.Getenv("STRIDE_E10_W6_MANAGED_PROVIDER_CLIENT_KEY_PATH")),
		ClientSPKISHA256:      strings.TrimSpace(os.Getenv("STRIDE_E10_W6_MANAGED_PROVIDER_CLIENT_SPKI_SHA256")),
		ServerSPKISHA256:      strings.TrimSpace(os.Getenv("STRIDE_E10_W6_MANAGED_PROVIDER_SERVER_SPKI_SHA256")),
		Timeout:               5 * time.Second,
	})
}

// NewStrideE10W6ManagedHTTPSProvider constructs the compiled W6 managed
// provider. It requires TLS 1.3 mutual authentication, exact client and server
// SPKI pins, stable credential files, closed JSON, and bounded I/O.
func NewStrideE10W6ManagedHTTPSProvider(config StrideE10W6ManagedHTTPSProviderConfig) (StrideE10W6ManagedProductionProvider, error) {
	parsed, err := url.Parse(config.Endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		config.Timeout < time.Second || config.Timeout > 15*time.Second || !isHexDigest(config.ClientSPKISHA256) || !isHexDigest(config.ServerSPKISHA256) {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	caPEM, err := readStrideE10W5ManagedCredential(config.CAPath, false)
	if err != nil {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	certPEM, err := readStrideE10W5ManagedCredential(config.ClientCertificatePath, false)
	if err != nil {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	keyPEM, err := readStrideE10W5ManagedCredential(config.ClientKeyPath, true)
	if err != nil {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	defer clear(keyPEM)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	clientCertificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil || len(clientCertificate.Certificate) < 1 {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	clientLeaf, err := x509.ParseCertificate(clientCertificate.Certificate[0])
	expectedClientSPKI, decodeClientErr := hex.DecodeString(config.ClientSPKISHA256)
	if err != nil || decodeClientErr != nil || len(expectedClientSPKI) != sha256.Size {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	clientSPKI := sha256.Sum256(clientLeaf.RawSubjectPublicKeyInfo)
	if !hmac.Equal(clientSPKI[:], expectedClientSPKI) {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	pinnedSPKI, err := hex.DecodeString(config.ServerSPKISHA256)
	if err != nil || len(pinnedSPKI) != sha256.Size {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{clientCertificate}, ServerName: parsed.Hostname(),
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) < 1 || len(state.VerifiedChains) < 1 {
				return ErrStrideE10W6RuntimeInvalid
			}
			digest := sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo)
			if !hmac.Equal(digest[:], pinnedSPKI) {
				return ErrStrideE10W6RuntimeInvalid
			}
			return nil
		},
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, TLSHandshakeTimeout: config.Timeout, ResponseHeaderTimeout: config.Timeout, DisableCompression: true, ForceAttemptHTTP2: true, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second}
	return &strideE10W6ManagedHTTPSProvider{
		endpoint: strings.TrimSuffix(parsed.String(), "/") + strideE10W6ManagedHTTPSRPCPath,
		client:   &http.Client{Transport: transport, Timeout: config.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrStrideE10W6RuntimeInvalid }},
	}, nil
}

func (p *strideE10W6ManagedHTTPSProvider) call(ctx context.Context, request strideE10W6ManagedHTTPSRequest) (strideE10W6ManagedHTTPSResponse, error) {
	var response strideE10W6ManagedHTTPSResponse
	if p == nil || p.client == nil || ctx == nil || request.Schema != strideE10W6ManagedHTTPSRPCSchema || request.Expectation == nil || !request.Expectation.valid() || !oneOf(request.Operation, "preflight", "purge_store") {
		return response, ErrStrideE10W6RuntimeInvalid
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > strideE10W6ManagedHTTPSMaxBody {
		return response, ErrStrideE10W6RuntimeInvalid
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return response, ErrStrideE10W6RuntimeUnavailable
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpResponse, err := p.client.Do(httpRequest)
	if err != nil {
		return response, ErrStrideE10W6RuntimeUnavailable
	}
	defer httpResponse.Body.Close()
	mediaType, mediaParams, mediaErr := mime.ParseMediaType(httpResponse.Header.Get("Content-Type"))
	if httpResponse.StatusCode != http.StatusOK || mediaErr != nil || mediaType != "application/json" || len(mediaParams) != 0 {
		return response, ErrStrideE10W6RuntimeUnavailable
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, strideE10W6ManagedHTTPSMaxBody+1))
	if readErr != nil || len(responseBody) == 0 || len(responseBody) > strideE10W6ManagedHTTPSMaxBody {
		return response, ErrStrideE10W6RuntimeUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&response) != nil || decoder.Decode(&struct{}{}) != io.EOF || response.Schema != strideE10W6ManagedHTTPSRPCSchema || response.Operation != request.Operation || !validStrideE10W6ManagedHTTPSResponseShape(response) {
		return strideE10W6ManagedHTTPSResponse{}, ErrStrideE10W6RuntimeUnavailable
	}
	return response, nil
}

func validStrideE10W6ManagedHTTPSResponseShape(response strideE10W6ManagedHTTPSResponse) bool {
	switch response.Operation {
	case "preflight":
		return response.Attestation != nil && response.Key != nil && response.OperationID == "" && response.ReceiptDigest == "" && response.Store == "" && response.Acknowledged == nil
	case "purge_store":
		return response.Attestation == nil && response.Key == nil && len(response.RetainedKeys) == 0 && isHexDigest(response.OperationID) && isHexDigest(response.ReceiptDigest) && oneOf(response.Store, contributionPurgeStores...) && response.Acknowledged != nil
	default:
		return false
	}
}

func (p *strideE10W6ManagedHTTPSProvider) PreflightStrideE10W6ManagedProduction(ctx context.Context, expectation StrideE10W6ManagedProductionExpectation) (StrideE10W6ManagedProductionAdapters, StrideE10W6ManagedProductionAttestation, error) {
	response, err := p.call(ctx, strideE10W6ManagedHTTPSRequest{Schema: strideE10W6ManagedHTTPSRPCSchema, Operation: "preflight", Expectation: &expectation})
	if err != nil || response.Attestation == nil || response.Key == nil {
		return StrideE10W6ManagedProductionAdapters{}, StrideE10W6ManagedProductionAttestation{}, ErrStrideE10W6RuntimeUnavailable
	}
	adapters := StrideE10W6ManagedProductionAdapters{
		Key: cloneStrideE10W6ManagedKey(*response.Key), RetainedKeys: cloneStrideE10W6ManagedKeys(response.RetainedKeys),
		Sessions:      &strideE10W6MainSessionAuthority{runtime: strideE10LiveProductRuntime, sessions: userSessionStore()},
		PurgeExecutor: &strideE10W6ManagedHTTPSAdapters{provider: p, expectation: expectation},
	}
	return adapters, *response.Attestation, nil
}

func (a *strideE10W6ManagedHTTPSAdapters) PurgeSTRIDENetworkShadowStore(ctx context.Context, receipt DerivedPurgeReceipt, store string) error {
	if a == nil || a.provider == nil || receipt.Validate() != nil || !oneOf(store, contributionPurgeStores...) {
		return ErrStrideE10W6RuntimeInvalid
	}
	receiptDigest, err := STRIDEContractDigest(receipt)
	if err != nil || !isHexDigest(receiptDigest) {
		return ErrStrideE10W6RuntimeInvalid
	}
	operationID := sha256Hex([]byte("stride.e10.w6.purge-store.v1\x00" + receiptDigest + "\x00" + store))
	response, err := a.provider.call(ctx, strideE10W6ManagedHTTPSRequest{Schema: strideE10W6ManagedHTTPSRPCSchema, Operation: "purge_store", Expectation: &a.expectation, Receipt: &receipt, Store: store, OperationID: operationID})
	if err != nil || response.Acknowledged == nil || !*response.Acknowledged || response.OperationID != operationID || response.ReceiptDigest != receiptDigest || response.Store != store {
		return ErrStrideE10W6RuntimeUnavailable
	}
	return nil
}

// strideE10W6MainSessionAuthority retains the exact local session ->
// organization lock order through final use. A remote service never receives
// bearer tokens and cannot mint or extend session authority.
type strideE10W6MainSessionAuthority struct {
	runtime  *StrideE10ProductLiveRuntime
	sessions *sessionStore
}

func (a *strideE10W6MainSessionAuthority) WithCurrentStrideE10W6Session(ctx context.Context, organizationID, sessionHash string, use func(StrideE10W6CurrentSession) error) error {
	if a == nil || a.runtime == nil || a.runtime.organization == nil || a.sessions == nil || ctx == nil || use == nil || !strideIdentifier(organizationID) || !validStrideE10SessionHash(sessionHash) {
		return ErrSTRIDENetworkShadowAuthority
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now := a.runtime.now().UTC()
	a.sessions.mu.Lock()
	defer a.sessions.mu.Unlock()
	organization := a.runtime.organization
	organization.mu.RLock()
	defer organization.mu.RUnlock()
	record, ok := a.sessions.sessions[sessionHash]
	if !ok || record.Kind != "" || !now.Before(record.Expires) || record.ActiveOrganizationID != organizationID || !strideIdentifier(record.PersonID) || !isHexDigest(record.AccountSubjectDigest) || record.AuthorityGeneration < 1 || !strideIdentifier(record.OrganizationMembershipID) || record.OrganizationMembershipRev < 1 || record.ActiveOrganizationSessionRev < 1 {
		return ErrSTRIDENetworkShadowAuthority
	}
	person, personOK := organization.persons[record.PersonID]
	membership, membershipOK := organization.memberships[record.OrganizationMembershipID]
	active, activeOK := organization.sessions[sessionHash]
	if !personOK || person.Validate() != nil || person.Status != "active" || person.AccountSubjectDigest != record.AccountSubjectDigest || organization.accountPersons[record.AccountSubjectDigest] != record.PersonID || !membershipOK || membership.Status != "active" || membership.PersonID != record.PersonID || membership.OrganizationID != organizationID || membership.Header.Revision != record.OrganizationMembershipRev ||
		!activeOK || active.Validate() != nil || active.Status != "active" || active.SessionSubjectDigest != sessionHash || active.PersonID != record.PersonID || active.OrganizationID != organizationID || active.MembershipID != record.OrganizationMembershipID || active.MembershipRevision != record.OrganizationMembershipRev || active.SessionRevision != record.ActiveOrganizationSessionRev || !now.Before(active.ExpiresAt) {
		return ErrSTRIDENetworkShadowAuthority
	}
	return use(StrideE10W6CurrentSession{SessionHash: sessionHash, PersonID: record.PersonID, OrganizationID: organizationID, MembershipID: record.OrganizationMembershipID, MembershipRevision: record.OrganizationMembershipRev, ActiveOrganizationSessionID: active.Header.ID, ActiveOrganizationSessionRev: active.SessionRevision})
}
