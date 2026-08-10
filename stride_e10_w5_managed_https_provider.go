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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	strideE10W5ManagedHTTPSRPCSchema = "stride.mymind.managed-https-rpc.v1"
	strideE10W5ManagedHTTPSRPCPath   = "/v1/stride/mymind/custody"
	strideE10W5ManagedHTTPSMaxBody   = 256 * 1024
	strideE10W5ManagedHTTPSMaxFile   = 1024 * 1024
)

// StrideE10W5ManagedHTTPSProviderConfig contains transport identity only. The
// client certificate authenticates this application to an independently owned
// custody service; it is not a custody, state-MAC, or destruction key.
type StrideE10W5ManagedHTTPSProviderConfig struct {
	Endpoint              string
	CAPath                string
	ClientCertificatePath string
	ClientKeyPath         string
	ClientSPKISHA256      string
	ServerSPKISHA256      string
	Timeout               time.Duration
}

type strideE10W5ManagedHTTPSProvider struct {
	endpoint string
	client   *http.Client
}

type strideE10W5ManagedHTTPSAdapters struct {
	provider    *strideE10W5ManagedHTTPSProvider
	expectation StrideE10W5ManagedProductionExpectation
}

type strideE10W5ManagedHTTPSRequest struct {
	Schema      string                                   `json:"schema"`
	Operation   string                                   `json:"operation"`
	Expectation *StrideE10W5ManagedProductionExpectation `json:"expectation,omitempty"`
	StatePath   string                                   `json:"statePath,omitempty"`
	PersonID    string                                   `json:"personId,omitempty"`
	SourceID    string                                   `json:"sourceId,omitempty"`
	KeyID       string                                   `json:"keyId,omitempty"`
	KeyVersion  int64                                    `json:"keyVersion,omitempty"`
	OperationID string                                   `json:"operationId,omitempty"`
	KeyRefs     []myMindCustodyKeyRef                    `json:"keyRefs,omitempty"`
	Current     *strideE10W5ManagedHTTPSHighWater        `json:"current,omitempty"`
	Next        *strideE10W5ManagedHTTPSHighWater        `json:"next,omitempty"`
	Receipt     *MyMindKeyDestructionReceipt             `json:"receipt,omitempty"`
}

type strideE10W5ManagedHTTPSResponse struct {
	Schema       string                                   `json:"schema"`
	Operation    string                                   `json:"operation"`
	Attestation  *StrideE10W5ManagedProductionAttestation `json:"attestation,omitempty"`
	StateKey     *strideE10W5ManagedHTTPSStateKey         `json:"stateKey,omitempty"`
	HighWater    *strideE10W5ManagedHTTPSHighWater        `json:"highWater,omitempty"`
	CustodyKey   *strideE10W5ManagedHTTPSCustodyKey       `json:"custodyKey,omitempty"`
	Receipt      *MyMindKeyDestructionReceipt             `json:"receipt,omitempty"`
	Acknowledged *bool                                    `json:"acknowledged,omitempty"`
}

type strideE10W5ManagedHTTPSStateKey struct {
	ID       string `json:"id"`
	Version  int64  `json:"version"`
	Material []byte `json:"material"`
}

type strideE10W5ManagedHTTPSCustodyKey struct {
	ID       string `json:"id"`
	Version  int64  `json:"version"`
	PersonID string `json:"personId"`
	SourceID string `json:"sourceId"`
	Material []byte `json:"material"`
}

type strideE10W5ManagedHTTPSHighWater struct {
	Generation    int64  `json:"generation"`
	PayloadDigest string `json:"payloadDigest,omitempty"`
}

func strideE10W5ManagedHTTPSProviderFromEnvironment() (StrideE10W5ManagedProductionProvider, error) {
	return NewStrideE10W5ManagedHTTPSProvider(StrideE10W5ManagedHTTPSProviderConfig{
		Endpoint:              strings.TrimSpace(os.Getenv("STRIDE_E10_W5_MANAGED_PROVIDER_URL")),
		CAPath:                strings.TrimSpace(os.Getenv("STRIDE_E10_W5_MANAGED_PROVIDER_CA_PATH")),
		ClientCertificatePath: strings.TrimSpace(os.Getenv("STRIDE_E10_W5_MANAGED_PROVIDER_CLIENT_CERT_PATH")),
		ClientKeyPath:         strings.TrimSpace(os.Getenv("STRIDE_E10_W5_MANAGED_PROVIDER_CLIENT_KEY_PATH")),
		ClientSPKISHA256:      strings.TrimSpace(os.Getenv("STRIDE_E10_W5_MANAGED_PROVIDER_CLIENT_SPKI_SHA256")),
		ServerSPKISHA256:      strings.TrimSpace(os.Getenv("STRIDE_E10_W5_MANAGED_PROVIDER_SERVER_SPKI_SHA256")),
		Timeout:               5 * time.Second,
	})
}

// NewStrideE10W5ManagedHTTPSProvider constructs the only compiled production
// W5 provider. It requires TLS 1.3, mutual certificate authentication, a pinned
// server SPKI, stable private credential files, closed JSON, bounded responses,
// no proxy, and no redirects.
func NewStrideE10W5ManagedHTTPSProvider(config StrideE10W5ManagedHTTPSProviderConfig) (StrideE10W5ManagedProductionProvider, error) {
	parsed, err := url.Parse(config.Endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		config.Timeout < time.Second || config.Timeout > 15*time.Second || !isHexDigest(config.ClientSPKISHA256) || !isHexDigest(config.ServerSPKISHA256) {
		return nil, ErrMyMindCustodyDenied
	}
	caPEM, err := readStrideE10W5ManagedCredential(config.CAPath, false)
	if err != nil {
		return nil, ErrMyMindCustodyDenied
	}
	certPEM, err := readStrideE10W5ManagedCredential(config.ClientCertificatePath, false)
	if err != nil {
		return nil, ErrMyMindCustodyDenied
	}
	keyPEM, err := readStrideE10W5ManagedCredential(config.ClientKeyPath, true)
	if err != nil {
		return nil, ErrMyMindCustodyDenied
	}
	defer clear(keyPEM)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, ErrMyMindCustodyDenied
	}
	clientCertificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, ErrMyMindCustodyDenied
	}
	if len(clientCertificate.Certificate) < 1 {
		return nil, ErrMyMindCustodyDenied
	}
	clientLeaf, err := x509.ParseCertificate(clientCertificate.Certificate[0])
	if err != nil {
		return nil, ErrMyMindCustodyDenied
	}
	expectedClientSPKI, err := hex.DecodeString(config.ClientSPKISHA256)
	if err != nil || len(expectedClientSPKI) != sha256.Size {
		return nil, ErrMyMindCustodyDenied
	}
	clientSPKI := sha256.Sum256(clientLeaf.RawSubjectPublicKeyInfo)
	if !hmac.Equal(clientSPKI[:], expectedClientSPKI) {
		return nil, ErrMyMindCustodyDenied
	}
	pinnedSPKI, err := hex.DecodeString(config.ServerSPKISHA256)
	if err != nil || len(pinnedSPKI) != sha256.Size {
		return nil, ErrMyMindCustodyDenied
	}
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		Certificates: []tls.Certificate{clientCertificate},
		ServerName:   parsed.Hostname(),
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) < 1 || len(state.VerifiedChains) < 1 {
				return ErrMyMindCustodyDenied
			}
			digest := sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo)
			if !hmac.Equal(digest[:], pinnedSPKI) {
				return ErrMyMindCustodyDenied
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   config.Timeout,
		ResponseHeaderTimeout: config.Timeout,
		DisableCompression:    true,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          2,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
	}
	endpoint := strings.TrimSuffix(parsed.String(), "/") + strideE10W5ManagedHTTPSRPCPath
	return &strideE10W5ManagedHTTPSProvider{
		endpoint: endpoint,
		client: &http.Client{
			Transport: transport,
			Timeout:   config.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return ErrMyMindCustodyDenied
			},
		},
	}, nil
}

func readStrideE10W5ManagedCredential(path string, private bool) ([]byte, error) {
	return readStrideE10W5ManagedCredentialWithHook(path, private, nil)
}

func readStrideE10W5ManagedCredentialWithHook(path string, private bool, afterFirstRead func() error) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrMyMindCustodyDenied
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, ErrMyMindCustodyDenied
	}
	before, err := os.Lstat(path)
	beforeStat, err := validateStrideE10W5ManagedCredentialInfo(before, err, private)
	if err != nil {
		return nil, ErrMyMindCustodyDenied
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrMyMindCustodyDenied
	}
	defer file.Close()
	opened, err := file.Stat()
	openedStat, err := validateStrideE10W5ManagedCredentialInfo(opened, err, private)
	if err != nil {
		return nil, ErrMyMindCustodyDenied
	}
	if !sameStrideE10W5ManagedCredentialInfo(before, beforeStat, opened, openedStat) {
		return nil, ErrMyMindCustodyDenied
	}
	value, err := readStrideE10W5ManagedCredentialFile(file)
	if err != nil {
		return nil, ErrMyMindCustodyDenied
	}
	if afterFirstRead != nil && afterFirstRead() != nil {
		clear(value)
		return nil, ErrMyMindCustodyDenied
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		clear(value)
		return nil, ErrMyMindCustodyDenied
	}
	second, err := readStrideE10W5ManagedCredentialFile(file)
	if err != nil || !bytes.Equal(value, second) {
		clear(value)
		clear(second)
		return nil, ErrMyMindCustodyDenied
	}
	clear(second)
	openedAfter, err := file.Stat()
	openedAfterStat, err := validateStrideE10W5ManagedCredentialInfo(openedAfter, err, private)
	if err != nil || !sameStrideE10W5ManagedCredentialInfo(opened, openedStat, openedAfter, openedAfterStat) {
		clear(value)
		return nil, ErrMyMindCustodyDenied
	}
	after, err := os.Lstat(path)
	afterStat, err := validateStrideE10W5ManagedCredentialInfo(after, err, private)
	if err != nil || !sameStrideE10W5ManagedCredentialInfo(openedAfter, openedAfterStat, after, afterStat) {
		clear(value)
		return nil, ErrMyMindCustodyDenied
	}
	return value, nil
}

func readStrideE10W5ManagedCredentialFile(file *os.File) ([]byte, error) {
	value, err := io.ReadAll(io.LimitReader(file, strideE10W5ManagedHTTPSMaxFile+1))
	if err != nil || len(value) < 1 || len(value) > strideE10W5ManagedHTTPSMaxFile {
		clear(value)
		return nil, ErrMyMindCustodyDenied
	}
	return value, nil
}

func validateStrideE10W5ManagedCredentialInfo(info os.FileInfo, err error, private bool) (*syscall.Stat_t, error) {
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > strideE10W5ManagedHTTPSMaxFile {
		return nil, ErrMyMindCustodyDenied
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 || (stat.Uid != uint32(os.Geteuid()) && stat.Uid != 0) {
		return nil, ErrMyMindCustodyDenied
	}
	if private {
		if info.Mode().Perm()&0o077 != 0 {
			return nil, ErrMyMindCustodyDenied
		}
	} else if info.Mode().Perm()&0o022 != 0 {
		return nil, ErrMyMindCustodyDenied
	}
	return stat, nil
}

func sameStrideE10W5ManagedCredentialInfo(left os.FileInfo, leftStat *syscall.Stat_t, right os.FileInfo, rightStat *syscall.Stat_t) bool {
	return left != nil && right != nil && leftStat != nil && rightStat != nil &&
		leftStat.Dev == rightStat.Dev && leftStat.Ino == rightStat.Ino && leftStat.Nlink == rightStat.Nlink && leftStat.Uid == rightStat.Uid &&
		left.Size() == right.Size() && left.Mode() == right.Mode() && left.ModTime().Equal(right.ModTime())
}

func (p *strideE10W5ManagedHTTPSProvider) call(ctx context.Context, request strideE10W5ManagedHTTPSRequest) (strideE10W5ManagedHTTPSResponse, error) {
	if p == nil || p.client == nil || ctx == nil || request.Operation == "" {
		return strideE10W5ManagedHTTPSResponse{}, ErrMyMindCustodyDenied
	}
	request.Schema = strideE10W5ManagedHTTPSRPCSchema
	body, err := json.Marshal(request)
	if err != nil || len(body) > strideE10W5ManagedHTTPSMaxBody {
		return strideE10W5ManagedHTTPSResponse{}, ErrMyMindCustodyDenied
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return strideE10W5ManagedHTTPSResponse{}, ErrMyMindCustodyDenied
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Cache-Control", "no-store")
	response, err := p.client.Do(httpRequest)
	if err != nil {
		return strideE10W5ManagedHTTPSResponse{}, ErrMyMindCustodyDenied
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusConflict {
			return strideE10W5ManagedHTTPSResponse{}, ErrMyMindCustodyConflict
		}
		return strideE10W5ManagedHTTPSResponse{}, ErrMyMindCustodyDenied
	}
	if media := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]); media != "application/json" {
		return strideE10W5ManagedHTTPSResponse{}, ErrMyMindCustodyDenied
	}
	limited, err := io.ReadAll(io.LimitReader(response.Body, strideE10W5ManagedHTTPSMaxBody+1))
	if err != nil || len(limited) < 2 || len(limited) > strideE10W5ManagedHTTPSMaxBody {
		return strideE10W5ManagedHTTPSResponse{}, ErrMyMindCustodyDenied
	}
	defer clear(limited)
	decoder := json.NewDecoder(bytes.NewReader(limited))
	decoder.DisallowUnknownFields()
	var decoded strideE10W5ManagedHTTPSResponse
	if decoder.Decode(&decoded) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		decoded.Schema != strideE10W5ManagedHTTPSRPCSchema || decoded.Operation != request.Operation {
		return strideE10W5ManagedHTTPSResponse{}, ErrMyMindCustodyDenied
	}
	return decoded, nil
}

func strideE10W5ManagedHTTPSPayloadCount(response strideE10W5ManagedHTTPSResponse) int {
	count := 0
	for _, present := range []bool{response.Attestation != nil, response.StateKey != nil, response.HighWater != nil, response.CustodyKey != nil, response.Receipt != nil, response.Acknowledged != nil} {
		if present {
			count++
		}
	}
	return count
}

func (p *strideE10W5ManagedHTTPSProvider) PreflightStrideE10W5ManagedProduction(ctx context.Context, expectation StrideE10W5ManagedProductionExpectation) (StrideE10W5ManagedProductionAdapters, StrideE10W5ManagedProductionAttestation, error) {
	if !expectation.valid() {
		return StrideE10W5ManagedProductionAdapters{}, StrideE10W5ManagedProductionAttestation{}, ErrMyMindCustodyDenied
	}
	response, err := p.call(ctx, strideE10W5ManagedHTTPSRequest{Operation: "preflight", Expectation: &expectation})
	if err != nil || strideE10W5ManagedHTTPSPayloadCount(response) != 1 || response.Attestation == nil {
		return StrideE10W5ManagedProductionAdapters{}, StrideE10W5ManagedProductionAttestation{}, ErrMyMindCustodyDenied
	}
	bound := &strideE10W5ManagedHTTPSAdapters{provider: p, expectation: expectation}
	return StrideE10W5ManagedProductionAdapters{StateKeys: bound, HighWater: bound, Keys: bound}, *response.Attestation, nil
}

func (a *strideE10W5ManagedHTTPSAdapters) call(ctx context.Context, request strideE10W5ManagedHTTPSRequest) (strideE10W5ManagedHTTPSResponse, error) {
	if a == nil || a.provider == nil || !a.expectation.valid() {
		return strideE10W5ManagedHTTPSResponse{}, ErrMyMindCustodyDenied
	}
	request.Expectation = &a.expectation
	return a.provider.call(ctx, request)
}

func (a *strideE10W5ManagedHTTPSAdapters) CurrentMyMindCustodyStateKey(ctx context.Context) (MyMindCustodyStateKey, error) {
	response, err := a.call(ctx, strideE10W5ManagedHTTPSRequest{Operation: "state_key_current"})
	if err != nil || strideE10W5ManagedHTTPSPayloadCount(response) != 1 || response.StateKey == nil {
		return MyMindCustodyStateKey{}, ErrMyMindCustodyDenied
	}
	key := MyMindCustodyStateKey{ID: response.StateKey.ID, Version: response.StateKey.Version, Material: append([]byte(nil), response.StateKey.Material...)}
	clear(response.StateKey.Material)
	if !key.valid() || key.ID != a.expectation.StateKeyID || key.Version != a.expectation.StateKeyVersion {
		return MyMindCustodyStateKey{}, ErrMyMindCustodyDenied
	}
	return key, nil
}

func (a *strideE10W5ManagedHTTPSAdapters) ResolveMyMindCustodyStateKey(ctx context.Context, id string, version int64) (MyMindCustodyStateKey, error) {
	if !strideIdentifier(id) || version < 1 {
		return MyMindCustodyStateKey{}, ErrMyMindCustodyDenied
	}
	response, err := a.call(ctx, strideE10W5ManagedHTTPSRequest{Operation: "state_key_resolve", KeyID: id, KeyVersion: version})
	if err != nil || strideE10W5ManagedHTTPSPayloadCount(response) != 1 || response.StateKey == nil {
		return MyMindCustodyStateKey{}, ErrMyMindCustodyDenied
	}
	key := MyMindCustodyStateKey{ID: response.StateKey.ID, Version: response.StateKey.Version, Material: append([]byte(nil), response.StateKey.Material...)}
	clear(response.StateKey.Material)
	if !key.valid() || key.ID != id || key.Version != version {
		return MyMindCustodyStateKey{}, ErrMyMindCustodyDenied
	}
	return key, nil
}

func (a *strideE10W5ManagedHTTPSAdapters) ReadMyMindCustodyHighWater(ctx context.Context, statePath string) (MyMindCustodyHighWater, error) {
	if statePath != a.expectation.StatePath {
		return MyMindCustodyHighWater{}, ErrMyMindCustodyDenied
	}
	response, err := a.call(ctx, strideE10W5ManagedHTTPSRequest{Operation: "high_water_read", StatePath: statePath})
	if err != nil || strideE10W5ManagedHTTPSPayloadCount(response) != 1 || response.HighWater == nil {
		return MyMindCustodyHighWater{}, ErrMyMindCustodyDenied
	}
	value := MyMindCustodyHighWater{Generation: response.HighWater.Generation, PayloadDigest: response.HighWater.PayloadDigest}
	if value.Generation < 0 || (value.Generation == 0 && value.PayloadDigest != "") || (value.Generation > 0 && !isHexDigest(value.PayloadDigest)) {
		return MyMindCustodyHighWater{}, ErrMyMindCustodyDenied
	}
	return value, nil
}

func (a *strideE10W5ManagedHTTPSAdapters) AdvanceMyMindCustodyHighWater(ctx context.Context, statePath string, current, next MyMindCustodyHighWater) error {
	if statePath != a.expectation.StatePath || !validStrideE10W5ManagedHighWater(current) || !validStrideE10W5ManagedHighWater(next) || next.Generation != current.Generation+1 || !isHexDigest(next.PayloadDigest) {
		return ErrMyMindCustodyDenied
	}
	response, err := a.call(ctx, strideE10W5ManagedHTTPSRequest{Operation: "high_water_advance", StatePath: statePath, Current: &strideE10W5ManagedHTTPSHighWater{Generation: current.Generation, PayloadDigest: current.PayloadDigest}, Next: &strideE10W5ManagedHTTPSHighWater{Generation: next.Generation, PayloadDigest: next.PayloadDigest}})
	if err != nil {
		return err
	}
	if strideE10W5ManagedHTTPSPayloadCount(response) != 1 || response.Acknowledged == nil || !*response.Acknowledged {
		return ErrMyMindCustodyDenied
	}
	return nil
}

func (a *strideE10W5ManagedHTTPSAdapters) CurrentMyMindCustodyKey(ctx context.Context, personID, sourceID string) (MyMindCustodyKey, error) {
	return a.custodyKey(ctx, "custody_key_current", personID, sourceID, "", 0)
}

func (a *strideE10W5ManagedHTTPSAdapters) ResolveMyMindCustodyKey(ctx context.Context, personID, sourceID, id string, version int64) (MyMindCustodyKey, error) {
	return a.custodyKey(ctx, "custody_key_resolve", personID, sourceID, id, version)
}

func (a *strideE10W5ManagedHTTPSAdapters) custodyKey(ctx context.Context, operation, personID, sourceID, id string, version int64) (MyMindCustodyKey, error) {
	if !strideIdentifier(personID) || !strideIdentifier(sourceID) || (operation == "custody_key_resolve" && (!strideIdentifier(id) || version < 1)) {
		return MyMindCustodyKey{}, ErrMyMindCustodyDenied
	}
	response, err := a.call(ctx, strideE10W5ManagedHTTPSRequest{Operation: operation, PersonID: personID, SourceID: sourceID, KeyID: id, KeyVersion: version})
	if err != nil || strideE10W5ManagedHTTPSPayloadCount(response) != 1 || response.CustodyKey == nil {
		return MyMindCustodyKey{}, ErrMyMindCustodyDenied
	}
	key := MyMindCustodyKey{ID: response.CustodyKey.ID, Version: response.CustodyKey.Version, PersonID: response.CustodyKey.PersonID, SourceID: response.CustodyKey.SourceID, Material: append([]byte(nil), response.CustodyKey.Material...)}
	clear(response.CustodyKey.Material)
	if !key.valid(personID, sourceID) || (id != "" && (key.ID != id || key.Version != version)) {
		return MyMindCustodyKey{}, ErrMyMindCustodyDenied
	}
	return key, nil
}

func (a *strideE10W5ManagedHTTPSAdapters) DestroySourceMyMindKeys(ctx context.Context, operationID, personID, sourceID string, refs []myMindCustodyKeyRef) (MyMindKeyDestructionReceipt, error) {
	return a.destroy(ctx, "destroy_source", operationID, personID, sourceID, refs)
}

func (a *strideE10W5ManagedHTTPSAdapters) DestroyPersonMyMindKeys(ctx context.Context, operationID, personID string, refs []myMindCustodyKeyRef) (MyMindKeyDestructionReceipt, error) {
	return a.destroy(ctx, "destroy_person", operationID, personID, "", refs)
}

func (a *strideE10W5ManagedHTTPSAdapters) destroy(ctx context.Context, operation, operationID, personID, sourceID string, refs []myMindCustodyKeyRef) (MyMindKeyDestructionReceipt, error) {
	if !strideIdentifier(operationID) || !strideIdentifier(personID) || (operation == "destroy_source" && !strideIdentifier(sourceID)) || !validStrideE10W5ManagedKeyRefs(refs) {
		return MyMindKeyDestructionReceipt{}, ErrMyMindCustodyDenied
	}
	response, err := a.call(ctx, strideE10W5ManagedHTTPSRequest{Operation: operation, OperationID: operationID, PersonID: personID, SourceID: sourceID, KeyRefs: append([]myMindCustodyKeyRef(nil), refs...)})
	if err != nil || strideE10W5ManagedHTTPSPayloadCount(response) != 1 || response.Receipt == nil {
		return MyMindKeyDestructionReceipt{}, ErrMyMindCustodyDenied
	}
	scope := "person"
	if operation == "destroy_source" {
		scope = "source"
	}
	if !validMyMindDestructionReceipt(*response.Receipt, operationID, scope, personID, sourceID, refs) {
		return MyMindKeyDestructionReceipt{}, ErrMyMindCustodyDenied
	}
	return *response.Receipt, nil
}

func (a *strideE10W5ManagedHTTPSAdapters) VerifyMyMindKeyDestruction(ctx context.Context, receipt MyMindKeyDestructionReceipt) error {
	if receipt.Schema != "stride.mymind.key-destruction.v1" || !strideIdentifier(receipt.OperationID) || !oneOf(receipt.Scope, "source", "person") || !strideIdentifier(receipt.PersonID) || (receipt.Scope == "source" && !strideIdentifier(receipt.SourceID)) || !isHexDigest(receipt.KeyRefsDigest) || !strideIdentifier(receipt.EvidenceKeyID) || receipt.EvidenceVersion < 1 || receipt.DestroyedAt.IsZero() || !isHexDigest(receipt.MAC) || receipt.VerificationContract != "managed_keyring_v1" || receipt.ReceiptDigest != myMindDestructionReceiptDigest(receipt) {
		return ErrMyMindCustodyDenied
	}
	response, err := a.call(ctx, strideE10W5ManagedHTTPSRequest{Operation: "verify_destruction", Receipt: &receipt})
	if err != nil {
		return err
	}
	if strideE10W5ManagedHTTPSPayloadCount(response) != 1 || response.Acknowledged == nil || !*response.Acknowledged {
		return ErrMyMindCustodyDenied
	}
	return nil
}

func validStrideE10W5ManagedHighWater(value MyMindCustodyHighWater) bool {
	return value.Generation >= 0 && ((value.Generation == 0 && value.PayloadDigest == "") || (value.Generation > 0 && isHexDigest(value.PayloadDigest)))
}

func validStrideE10W5ManagedKeyRefs(refs []myMindCustodyKeyRef) bool {
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		key := ref.ID + "/" + strconv.FormatInt(ref.Version, 10)
		if !strideIdentifier(ref.ID) || ref.Version < 1 || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

var _ StrideE10W5ManagedProductionProvider = (*strideE10W5ManagedHTTPSProvider)(nil)
var _ MyMindCustodyStateKeyring = (*strideE10W5ManagedHTTPSAdapters)(nil)
var _ MyMindCustodyHighWaterStore = (*strideE10W5ManagedHTTPSAdapters)(nil)
var _ MyMindCustodyKeyring = (*strideE10W5ManagedHTTPSAdapters)(nil)
