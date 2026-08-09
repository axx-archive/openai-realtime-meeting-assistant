package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	strideE10TenantRuntimeModeEnv        = "STRIDE_E10_TENANT_CONVERSION_MODE"
	strideE10TenantReceiptPathEnv        = "STRIDE_E10_TENANT_RECEIPT_PATH"
	strideE10TenantReceiptKeyIDEnv       = "STRIDE_E10_TENANT_RECEIPT_KEY_ID"
	strideE10TenantReceiptKeyVersionEnv  = "STRIDE_E10_TENANT_RECEIPT_KEY_VERSION"
	strideE10TenantReceiptKeySecretEnv   = "STRIDE_E10_TENANT_RECEIPT_KEY_SECRET_BASE64"
	strideE10TenantEnvelopeKeyIDEnv      = "STRIDE_E10_TENANT_ENVELOPE_KEY_ID"
	strideE10TenantEnvelopeKeyVersionEnv = "STRIDE_E10_TENANT_ENVELOPE_KEY_VERSION"
	strideE10TenantEnvelopeKeySecretEnv  = "STRIDE_E10_TENANT_ENVELOPE_KEY_SECRET_BASE64"
	strideE10TenantRuntimeModeOff        = "off"
)

type strideE10TenantProductionGate struct{ enabled bool }

func (g *strideE10TenantProductionGate) Enabled() bool { return g != nil && g.enabled }

type strideE10TenantProductionLegacyIDs struct{ organizations *OrganizationAuthorityService }

func (a *strideE10TenantProductionLegacyIDs) WithMappedLegacyPerson(_ context.Context, digest string, use func(string) error) error {
	if a == nil || a.organizations == nil || !isHexDigest(digest) || use == nil {
		return ErrStrideE10TenantAuthorityStale
	}
	a.organizations.mu.RLock()
	defer a.organizations.mu.RUnlock()
	personID := a.organizations.accountPersons[digest]
	person, ok := a.organizations.persons[personID]
	if !ok || person.Validate() != nil || person.Status != "active" || person.AccountSubjectDigest != digest {
		return ErrStrideE10TenantAuthorityStale
	}
	return use(personID)
}

type strideE10TenantFileReceiptSink struct {
	mu   sync.Mutex
	path string
	key  StrideE10TenantReceiptKey
}

func (s *strideE10TenantFileReceiptSink) RecordStrideE10TenantDiscrepancy(_ context.Context, receipt StrideE10TenantDiscrepancyReceipt) error {
	if s == nil || !filepath.IsAbs(s.path) || filepath.Clean(s.path) != s.path || receipt.ValidateWithKey(s.key) != nil {
		return ErrStrideE10TenantAuthorityInvalid
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(append(body, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

type strideE10TenantManagedEnvelopeKeyring struct {
	current StrideE10TenantAuthorityEnvelopeKey
}

func (k *strideE10TenantManagedEnvelopeKeyring) CurrentStrideE10TenantAuthorityEnvelopeKey(context.Context) (StrideE10TenantAuthorityEnvelopeKey, error) {
	if k == nil || !validStrideE10TenantEnvelopeKey(k.current) {
		return StrideE10TenantAuthorityEnvelopeKey{}, ErrStrideE10TenantAuthorityInvalid
	}
	return cloneStrideE10TenantEnvelopeKey(k.current), nil
}

func (k *strideE10TenantManagedEnvelopeKeyring) ResolveStrideE10TenantAuthorityEnvelopeKey(_ context.Context, id string, version uint64) (StrideE10TenantAuthorityEnvelopeKey, error) {
	if k == nil || id != k.current.ID || version != k.current.Version || !validStrideE10TenantEnvelopeKey(k.current) {
		return StrideE10TenantAuthorityEnvelopeKey{}, ErrStrideE10TenantAuthorityInvalid
	}
	return cloneStrideE10TenantEnvelopeKey(k.current), nil
}

func cloneStrideE10TenantEnvelopeKey(key StrideE10TenantAuthorityEnvelopeKey) StrideE10TenantAuthorityEnvelopeKey {
	key.Secret = append([]byte(nil), key.Secret...)
	return key
}

type strideE10TenantProductionBootstrapConfig struct {
	mode          string
	receiptPath   string
	receiptKey    StrideE10TenantReceiptKey
	envelopeKey   StrideE10TenantAuthorityEnvelopeKey
	sessions      *sessionStore
	organizations *OrganizationAuthorityService
}

func strideE10TenantProductionBootstrapConfigFromEnvironment() (strideE10TenantProductionBootstrapConfig, error) {
	config := strideE10TenantProductionBootstrapConfig{mode: strings.ToLower(strings.TrimSpace(os.Getenv(strideE10TenantRuntimeModeEnv))), sessions: userSessionStore(), organizations: strideE10LiveProductRuntime.organization}
	if config.mode == "" {
		config.mode = strideE10TenantRuntimeModeOff
	}
	if config.mode == strideE10TenantRuntimeModeOff {
		return config, nil
	}
	// W3 production composition is observation-only. W4 must install durable
	// canonical organization and product-operation stores before cutover can be
	// a valid process mode.
	if config.mode != string(StrideE10TenantConversionShadow) {
		return strideE10TenantProductionBootstrapConfig{}, ErrStrideE10TenantAuthorityInvalid
	}
	receiptVersion, receiptErr := strconv.ParseInt(strings.TrimSpace(os.Getenv(strideE10TenantReceiptKeyVersionEnv)), 10, 64)
	envelopeVersion, envelopeErr := strconv.ParseUint(strings.TrimSpace(os.Getenv(strideE10TenantEnvelopeKeyVersionEnv)), 10, 64)
	receiptSecret, receiptSecretErr := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv(strideE10TenantReceiptKeySecretEnv)))
	envelopeSecret, envelopeSecretErr := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv(strideE10TenantEnvelopeKeySecretEnv)))
	if receiptErr != nil || envelopeErr != nil || receiptSecretErr != nil || envelopeSecretErr != nil {
		return strideE10TenantProductionBootstrapConfig{}, ErrStrideE10TenantAuthorityInvalid
	}
	config.receiptPath = strings.TrimSpace(os.Getenv(strideE10TenantReceiptPathEnv))
	config.receiptKey = StrideE10TenantReceiptKey{ID: strings.TrimSpace(os.Getenv(strideE10TenantReceiptKeyIDEnv)), Version: receiptVersion, Secret: receiptSecret}
	config.envelopeKey = StrideE10TenantAuthorityEnvelopeKey{ID: strings.TrimSpace(os.Getenv(strideE10TenantEnvelopeKeyIDEnv)), Version: envelopeVersion, Secret: envelopeSecret}
	return config, nil
}

// installStrideE10TenantProductionRuntime composes the real main-process
// resolver, body-free parity authority, durable private receipt sink, and
// managed envelope keyring. Off installs a disabled valve with no keys and is
// byte-compatible with the legacy path. W3 production accepts only off/shadow;
// cutover remains a local verification mode until W4 installs durable canonical
// organization and product-operation persistence.
func installStrideE10TenantProductionRuntime(config strideE10TenantProductionBootstrapConfig) (func(), error) {
	if config.sessions == nil || config.organizations == nil {
		return nil, ErrStrideE10TenantAuthorityInvalid
	}
	if config.mode == string(StrideE10TenantConversionCutover) {
		return nil, ErrStrideE10TenantAuthorityInvalid
	}
	gate := &strideE10TenantProductionGate{enabled: config.mode != "" && config.mode != strideE10TenantRuntimeModeOff}
	mode := StrideE10TenantConversionShadow
	if config.mode == string(StrideE10TenantConversionCutover) {
		mode = StrideE10TenantConversionCutover
	}
	resolver := &strideE10MainTenantAuthorityResolver{sessions: config.sessions, organizations: config.organizations}
	legacyIDs := &strideE10TenantProductionLegacyIDs{organizations: config.organizations}
	if !gate.enabled {
		converter := NewStrideE10TenantConverter(gate, resolver, nil, legacyIDs, StrideE10TenantReceiptKey{}, mode)
		restoreConverter := InstallStrideE10TenantRuntimeConverter(converter)
		restoreEnvelope := InstallStrideE10TenantAuthorityEnvelopeRuntime(nil)
		return func() { restoreEnvelope(); restoreConverter() }, nil
	}
	if config.mode != string(StrideE10TenantConversionShadow) || !filepath.IsAbs(config.receiptPath) || filepath.Clean(config.receiptPath) != config.receiptPath || !strideIdentifier(config.receiptKey.ID) || config.receiptKey.Version < 1 || len(config.receiptKey.Secret) < 32 || !validStrideE10TenantEnvelopeKey(config.envelopeKey) {
		return nil, ErrStrideE10TenantAuthorityInvalid
	}
	sink := &strideE10TenantFileReceiptSink{path: config.receiptPath, key: config.receiptKey}
	converter := NewStrideE10TenantConverter(gate, resolver, sink, legacyIDs, config.receiptKey, mode)
	keyring := &strideE10TenantManagedEnvelopeKeyring{current: cloneStrideE10TenantEnvelopeKey(config.envelopeKey)}
	restoreEnvelope := InstallStrideE10TenantAuthorityEnvelopeRuntime(keyring)
	restoreConverter := InstallStrideE10TenantRuntimeConverter(converter)
	return func() { restoreConverter(); restoreEnvelope() }, nil
}

func installStrideE10TenantProductionRuntimeFromEnvironment() (func(), error) {
	config, err := strideE10TenantProductionBootstrapConfigFromEnvironment()
	if err != nil {
		return nil, err
	}
	return installStrideE10TenantProductionRuntime(config)
}

var _ StrideE10TenantReceiptSink = (*strideE10TenantFileReceiptSink)(nil)
var _ StrideE10LegacyIdentityAuthority = (*strideE10TenantProductionLegacyIDs)(nil)
var _ StrideE10TenantAuthorityEnvelopeKeyring = (*strideE10TenantManagedEnvelopeKeyring)(nil)
