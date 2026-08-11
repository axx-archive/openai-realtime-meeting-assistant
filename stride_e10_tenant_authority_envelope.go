package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"
)

const (
	strideE10TenantAuthorityEnvelopeVersion       = 1
	strideE10TenantAuthorityPurposeCodexJobPrefix = "codex_runner_job:"
	strideE10TenantAuthorityPurposeScoutPrefix    = "scout_agent_thread:"
	strideE10TenantAuthorityPurposeBrainPrefix    = "brain_work:"
	strideE10TenantAuthorityEnvelopeMaxTTL        = 24 * time.Hour
)

type StrideE10TenantAuthorityEnvelopeKey struct {
	ID      string
	Version uint64
	Secret  []byte
}

type StrideE10TenantAuthorityEnvelopeKeyring interface {
	CurrentStrideE10TenantAuthorityEnvelopeKey(context.Context) (StrideE10TenantAuthorityEnvelopeKey, error)
	ResolveStrideE10TenantAuthorityEnvelopeKey(context.Context, string, uint64) (StrideE10TenantAuthorityEnvelopeKey, error)
}

// StrideE10TenantAuthorityEnvelope is the body-free durable authority carried
// by delayed work. It contains no raw token, email, profile, or source body.
type StrideE10TenantAuthorityEnvelope struct {
	Version              int                    `json:"version"`
	PersonID             string                 `json:"personId"`
	OrganizationID       string                 `json:"organizationId"`
	MembershipID         string                 `json:"membershipId"`
	MembershipRevision   int64                  `json:"membershipRevision"`
	SessionSubjectDigest string                 `json:"sessionSubjectDigest"`
	SessionRevision      int64                  `json:"sessionRevision"`
	AuthorityGeneration  uint64                 `json:"authorityGeneration"`
	Surface              StrideE10TenantSurface `json:"surface"`
	Purpose              string                 `json:"purpose"`
	ExpiresAt            time.Time              `json:"expiresAt"`
	KeyID                string                 `json:"keyId"`
	KeyVersion           uint64                 `json:"keyVersion"`
	MAC                  string                 `json:"mac"`
}

type strideE10TenantAuthorityEnvelopeRuntime struct {
	keys StrideE10TenantAuthorityEnvelopeKeyring
	now  func() time.Time
}

var strideE10TenantEnvelopeRuntimeState atomic.Pointer[strideE10TenantAuthorityEnvelopeRuntime]

func InstallStrideE10TenantAuthorityEnvelopeRuntime(keys StrideE10TenantAuthorityEnvelopeKeyring) func() {
	next := &strideE10TenantAuthorityEnvelopeRuntime{keys: keys, now: time.Now}
	previous := strideE10TenantEnvelopeRuntimeState.Swap(next)
	return func() { strideE10TenantEnvelopeRuntimeState.Store(previous) }
}

func strideE10CurrentTenantEnvelopeRuntime() *strideE10TenantAuthorityEnvelopeRuntime {
	return strideE10TenantEnvelopeRuntimeState.Load()
}

func strideE10TenantEnvelopeMAC(key StrideE10TenantAuthorityEnvelopeKey, envelope StrideE10TenantAuthorityEnvelope) string {
	envelope.MAC = ""
	raw, _ := json.Marshal(envelope)
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func validStrideE10TenantEnvelopeKey(key StrideE10TenantAuthorityEnvelopeKey) bool {
	return strideIdentifier(key.ID) && key.Version > 0 && len(key.Secret) >= 32
}

func (e StrideE10TenantAuthorityEnvelope) validateShape(now time.Time) error {
	prefix := map[StrideE10TenantSurface]string{StrideE10TenantSurfaceWorkQueue: strideE10TenantAuthorityPurposeCodexJobPrefix, StrideE10TenantSurfaceScout: strideE10TenantAuthorityPurposeScoutPrefix, StrideE10TenantSurfaceBrain: strideE10TenantAuthorityPurposeBrainPrefix}[e.Surface]
	purposeDigest := strings.TrimPrefix(e.Purpose, prefix)
	if e.Version != strideE10TenantAuthorityEnvelopeVersion || !strideIdentifier(e.PersonID) || !strideIdentifier(e.OrganizationID) || !strideIdentifier(e.MembershipID) || e.MembershipRevision < 1 || !validStrideE10SessionHash(e.SessionSubjectDigest) || e.SessionRevision < 1 || e.AuthorityGeneration < 1 || prefix == "" || !strings.HasPrefix(e.Purpose, prefix) || !isHexDigest(purposeDigest) || e.ExpiresAt.IsZero() || !now.UTC().Before(e.ExpiresAt.UTC()) || e.ExpiresAt.UTC().After(now.UTC().Add(strideE10TenantAuthorityEnvelopeMaxTTL)) || !strideIdentifier(e.KeyID) || e.KeyVersion < 1 || !isHexDigest(e.MAC) {
		return ErrStrideE10TenantAuthorityInvalid
	}
	return nil
}

func validateStrideE10TenantAuthorityEnvelope(ctx context.Context, envelope StrideE10TenantAuthorityEnvelope, now time.Time) error {
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	if runtime == nil || runtime.keys == nil || envelope.validateShape(now) != nil {
		return ErrStrideE10TenantAuthorityInvalid
	}
	key, err := runtime.keys.ResolveStrideE10TenantAuthorityEnvelopeKey(ctx, envelope.KeyID, envelope.KeyVersion)
	if err != nil || !validStrideE10TenantEnvelopeKey(key) || !hmac.Equal([]byte(envelope.MAC), []byte(strideE10TenantEnvelopeMAC(key, envelope))) {
		return ErrStrideE10TenantAuthorityInvalid
	}
	return nil
}

// MintStrideE10TenantAuthorityEnvelope creates the envelope only while the
// converter is holding its current session/membership resolver callback.
func MintStrideE10TenantAuthorityEnvelope(ctx context.Context, converter *StrideE10TenantConverter, sessionSubjectDigest, purpose string, expiresAt time.Time) (StrideE10TenantAuthorityEnvelope, error) {
	return MintStrideE10TenantAuthorityEnvelopeForSurface(ctx, converter, sessionSubjectDigest, StrideE10TenantSurfaceWorkQueue, purpose, expiresAt)
}

func MintStrideE10TenantAuthorityEnvelopeForSurface(ctx context.Context, converter *StrideE10TenantConverter, sessionSubjectDigest string, surface StrideE10TenantSurface, purpose string, expiresAt time.Time) (StrideE10TenantAuthorityEnvelope, error) {
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	now := time.Now().UTC()
	prefix := map[StrideE10TenantSurface]string{StrideE10TenantSurfaceWorkQueue: strideE10TenantAuthorityPurposeCodexJobPrefix, StrideE10TenantSurfaceScout: strideE10TenantAuthorityPurposeScoutPrefix, StrideE10TenantSurfaceBrain: strideE10TenantAuthorityPurposeBrainPrefix}[surface]
	if converter == nil || runtime == nil || runtime.keys == nil || !validStrideE10SessionHash(sessionSubjectDigest) || prefix == "" || !strings.HasPrefix(purpose, prefix) || !isHexDigest(strings.TrimPrefix(purpose, prefix)) || !now.Before(expiresAt.UTC()) || expiresAt.UTC().After(now.Add(strideE10TenantAuthorityEnvelopeMaxTTL)) {
		return StrideE10TenantAuthorityEnvelope{}, ErrStrideE10TenantAuthorityInvalid
	}
	resolution, err := converter.Resolve(ctx, surface, sessionSubjectDigest)
	if err != nil || resolution.Capability == nil {
		return StrideE10TenantAuthorityEnvelope{}, ErrStrideE10TenantAuthorityStale
	}
	var envelope StrideE10TenantAuthorityEnvelope
	err = converter.WithCurrentPrincipal(ctx, resolution.Capability, func(principal StrideE10TenantPrincipal) error {
		key, keyErr := runtime.keys.CurrentStrideE10TenantAuthorityEnvelopeKey(ctx)
		if keyErr != nil || !validStrideE10TenantEnvelopeKey(key) {
			return ErrStrideE10TenantAuthorityInvalid
		}
		envelope = StrideE10TenantAuthorityEnvelope{
			Version: strideE10TenantAuthorityEnvelopeVersion, PersonID: principal.PersonID,
			OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID,
			MembershipRevision: principal.OrganizationMembershipRev, SessionSubjectDigest: sessionSubjectDigest,
			SessionRevision: principal.ActiveOrganizationSessionRev, AuthorityGeneration: principal.AuthorityGeneration,
			Surface: surface, Purpose: purpose,
			ExpiresAt: expiresAt.UTC(), KeyID: key.ID, KeyVersion: key.Version,
		}
		envelope.MAC = strideE10TenantEnvelopeMAC(key, envelope)
		return nil
	})
	if err != nil || envelope.validateShape(time.Now().UTC()) != nil {
		return StrideE10TenantAuthorityEnvelope{}, ErrStrideE10TenantAuthorityStale
	}
	return envelope, nil
}

func mintStrideE10TenantAuthorityEnvelopeFromHeldContext(ctx context.Context, surface StrideE10TenantSurface, purpose string, expiresAt time.Time) (StrideE10TenantAuthorityEnvelope, error) {
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	fence := strideE10HeldTenantAuthorityFromContext(ctx)
	sessionSubjectDigest := strideE10TenantSessionHashFromContext(ctx)
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)
	now := time.Now().UTC()
	prefix := map[StrideE10TenantSurface]string{StrideE10TenantSurfaceWorkQueue: strideE10TenantAuthorityPurposeCodexJobPrefix, StrideE10TenantSurfaceScout: strideE10TenantAuthorityPurposeScoutPrefix, StrideE10TenantSurfaceBrain: strideE10TenantAuthorityPurposeBrainPrefix}[surface]
	if runtime == nil || runtime.keys == nil || fence == nil || !canonical || principal != fence.principal || sessionSubjectDigest != fence.snapshot.SessionHash || prefix == "" || !strings.HasPrefix(purpose, prefix) || !isHexDigest(strings.TrimPrefix(purpose, prefix)) || !now.Before(expiresAt.UTC()) || expiresAt.UTC().After(now.Add(strideE10TenantAuthorityEnvelopeMaxTTL)) {
		return StrideE10TenantAuthorityEnvelope{}, ErrStrideE10TenantAuthorityStale
	}
	key, err := runtime.keys.CurrentStrideE10TenantAuthorityEnvelopeKey(ctx)
	if err != nil || !validStrideE10TenantEnvelopeKey(key) {
		return StrideE10TenantAuthorityEnvelope{}, ErrStrideE10TenantAuthorityInvalid
	}
	envelope := StrideE10TenantAuthorityEnvelope{
		Version: strideE10TenantAuthorityEnvelopeVersion, PersonID: principal.PersonID,
		OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID,
		MembershipRevision: principal.OrganizationMembershipRev, SessionSubjectDigest: sessionSubjectDigest,
		SessionRevision: principal.ActiveOrganizationSessionRev, AuthorityGeneration: principal.AuthorityGeneration,
		Surface: surface, Purpose: purpose, ExpiresAt: expiresAt.UTC(), KeyID: key.ID, KeyVersion: key.Version,
	}
	envelope.MAC = strideE10TenantEnvelopeMAC(key, envelope)
	if envelope.validateShape(now) != nil {
		return StrideE10TenantAuthorityEnvelope{}, ErrStrideE10TenantAuthorityInvalid
	}
	return envelope, nil
}

func strideE10TenantActionPurpose(prefix string, values ...string) string {
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return prefix + hex.EncodeToString(sum[:])
}

func StrideE10TenantAuthorityPurposeForScoutThread(runID, mode, query string) string {
	return strideE10TenantActionPurpose(strideE10TenantAuthorityPurposeScoutPrefix, strings.TrimSpace(runID), normalizeAgentThreadMode(mode), canonicalizeBoardText(query))
}

func StrideE10TenantAuthorityPurposeForBrainWork(workID, kind string) string {
	return strideE10TenantActionPurpose(strideE10TenantAuthorityPurposeBrainPrefix, strings.TrimSpace(workID), strings.TrimSpace(kind))
}

func StrideE10TenantAuthorityPurposeForCodexJob(artifactID, threadID, mode, query, authority string) string {
	material := struct {
		ArtifactID string `json:"artifactId"`
		ThreadID   string `json:"threadId"`
		Mode       string `json:"mode"`
		Query      string `json:"query"`
		Authority  string `json:"authority"`
	}{strings.TrimSpace(artifactID), strings.TrimSpace(threadID), strings.TrimSpace(mode), strings.TrimSpace(query), normalizeCodexJobAuthority(authority)}
	raw, _ := json.Marshal(material)
	sum := sha256.Sum256(raw)
	return strideE10TenantAuthorityPurposeCodexJobPrefix + hex.EncodeToString(sum[:])
}

func withStrideE10TenantEnvelopeAuthority(ctx context.Context, envelope *StrideE10TenantAuthorityEnvelope, surface StrideE10TenantSurface, now time.Time, effect func(StrideE10TenantPrincipal) error) error {
	if effect == nil {
		return ErrStrideE10TenantAuthorityInvalid
	}
	return withStrideE10TenantEnvelopeAuthorityContext(ctx, envelope, surface, now, func(_ context.Context, principal StrideE10TenantPrincipal) error {
		return effect(principal)
	})
}

func withStrideE10TenantEnvelopeAuthorityContext(ctx context.Context, envelope *StrideE10TenantAuthorityEnvelope, surface StrideE10TenantSurface, now time.Time, effect func(context.Context, StrideE10TenantPrincipal) error) error {
	converter := currentStrideE10TenantRuntimeConverter()
	if converter == nil || converter.gate == nil || !converter.gate.Enabled() || converter.mode != StrideE10TenantConversionCutover || envelope == nil || effect == nil || !oneOf(string(surface), string(StrideE10TenantSurfaceWorkQueue), string(StrideE10TenantSurfaceWorker), string(StrideE10TenantSurfaceScout), string(StrideE10TenantSurfaceBrain)) {
		return ErrStrideE10TenantAuthorityStale
	}
	if err := validateStrideE10TenantAuthorityEnvelope(ctx, *envelope, now); err != nil {
		return ErrStrideE10TenantAuthorityStale
	}
	if (surface == StrideE10TenantSurfaceWorker && envelope.Surface != StrideE10TenantSurfaceWorkQueue) || (surface != StrideE10TenantSurfaceWorker && envelope.Surface != surface) {
		return ErrStrideE10TenantAuthorityStale
	}
	if fence := strideE10HeldTenantAuthorityFromContext(ctx); fence != nil && fence.converter == converter && fence.snapshot.SessionHash == envelope.SessionSubjectDigest {
		principal, canonical := strideE10TenantPrincipalFromContext(ctx)
		if !canonical || principal != fence.principal || principal.PersonID != envelope.PersonID || principal.ActiveOrganizationID != envelope.OrganizationID || principal.OrganizationMembershipID != envelope.MembershipID || principal.OrganizationMembershipRev != envelope.MembershipRevision || principal.ActiveOrganizationSessionRev != envelope.SessionRevision || principal.AuthorityGeneration != envelope.AuthorityGeneration {
			return ErrStrideE10TenantAuthorityStale
		}
		return effect(ctx, principal)
	}
	resolution, err := converter.Resolve(ctx, surface, envelope.SessionSubjectDigest)
	if err != nil || resolution.Capability == nil {
		return ErrStrideE10TenantAuthorityStale
	}
	return converter.WithCurrentPrincipal(ctx, resolution.Capability, func(principal StrideE10TenantPrincipal) error {
		if !converter.gate.Enabled() || principal.PersonID != envelope.PersonID || principal.ActiveOrganizationID != envelope.OrganizationID || principal.OrganizationMembershipID != envelope.MembershipID || principal.OrganizationMembershipRev != envelope.MembershipRevision || principal.ActiveOrganizationSessionRev != envelope.SessionRevision || principal.AuthorityGeneration != envelope.AuthorityGeneration {
			return ErrStrideE10TenantAuthorityStale
		}
		bound, release, err := strideE10BindCurrentHeldTenantAuthority(ctx, converter, principal, envelope.SessionSubjectDigest, surface)
		if err != nil {
			return err
		}
		defer release()
		return effect(bound, principal)
	})
}

func strideE10TenantEnvelopeBindingEqual(left, right *StrideE10TenantAuthorityEnvelope) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return hmac.Equal(a, b)
}

func strideE10TenantEnvelopeContainsPrivateAuthority(raw []byte) bool {
	lower := strings.ToLower(string(raw))
	return strings.Contains(lower, "@") || strings.Contains(lower, "sessiontoken") || strings.Contains(lower, "password")
}
