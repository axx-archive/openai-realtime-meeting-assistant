package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	ErrStrideE10TenantConversionDisabled = errors.New("stride tenant conversion disabled")
	ErrStrideE10TenantAuthorityInvalid   = errors.New("invalid stride tenant authority")
	ErrStrideE10TenantAuthorityStale     = errors.New("stale stride tenant authority")
)

// StrideE10TenantSurface is the closed W3 cutover inventory. A future cutover
// must explicitly cover every value; accepting an arbitrary surface string
// would let a singleton email/tenant path silently escape shadow comparison.
type StrideE10TenantSurface string

const (
	StrideE10TenantSurfaceAuthSession    StrideE10TenantSurface = "auth_session"
	StrideE10TenantSurfaceHTTP           StrideE10TenantSurface = "http"
	StrideE10TenantSurfaceWebSocket      StrideE10TenantSurface = "websocket"
	StrideE10TenantSurfaceChat           StrideE10TenantSurface = "chat"
	StrideE10TenantSurfaceDrive          StrideE10TenantSurface = "drive"
	StrideE10TenantSurfaceProductContext StrideE10TenantSurface = "product_context"
	StrideE10TenantSurfacePushDelivery   StrideE10TenantSurface = "push_delivery"
	StrideE10TenantSurfaceNotification   StrideE10TenantSurface = "notification_projection"
	StrideE10TenantSurfaceRoomAdmission  StrideE10TenantSurface = "room_admission"
	StrideE10TenantSurfaceArtifactACL    StrideE10TenantSurface = "artifact_acl"
	StrideE10TenantSurfaceBoard          StrideE10TenantSurface = "board"
	StrideE10TenantSurfaceBrain          StrideE10TenantSurface = "brain"
	StrideE10TenantSurfaceScout          StrideE10TenantSurface = "scout"
	StrideE10TenantSurfaceMarketplace    StrideE10TenantSurface = "marketplace"
	StrideE10TenantSurfaceWorkQueue      StrideE10TenantSurface = "work_queue"
	StrideE10TenantSurfaceCache          StrideE10TenantSurface = "cache"
	StrideE10TenantSurfaceWorker         StrideE10TenantSurface = "worker"
)

var strideE10TenantSurfaceInventory = []StrideE10TenantSurface{
	StrideE10TenantSurfaceAuthSession,
	StrideE10TenantSurfaceHTTP,
	StrideE10TenantSurfaceWebSocket,
	StrideE10TenantSurfaceChat,
	StrideE10TenantSurfaceDrive,
	StrideE10TenantSurfaceProductContext,
	StrideE10TenantSurfacePushDelivery,
	StrideE10TenantSurfaceNotification,
	StrideE10TenantSurfaceRoomAdmission,
	StrideE10TenantSurfaceArtifactACL,
	StrideE10TenantSurfaceBoard,
	StrideE10TenantSurfaceBrain,
	StrideE10TenantSurfaceScout,
	StrideE10TenantSurfaceMarketplace,
	StrideE10TenantSurfaceWorkQueue,
	StrideE10TenantSurfaceCache,
	StrideE10TenantSurfaceWorker,
}

type StrideE10TenantHookStatus string

const (
	StrideE10TenantHookPending StrideE10TenantHookStatus = "pending"
	StrideE10TenantHookActive  StrideE10TenantHookStatus = "active"
)

type StrideE10TenantSurfaceCoverage struct {
	Surface          StrideE10TenantSurface
	LegacySingletons []string
	HookStatus       StrideE10TenantHookStatus
}

var strideE10TenantCoverageInventory = []StrideE10TenantSurfaceCoverage{
	{Surface: StrideE10TenantSurfaceAuthSession, LegacySingletons: []string{"sessionStore.lookupMemberRecordByHash", "userFromRequest.email"}, HookStatus: StrideE10TenantHookActive},
	{Surface: StrideE10TenantSurfaceHTTP, LegacySingletons: []string{"userFromRequest.email", "canonicalTenantID"}, HookStatus: StrideE10TenantHookActive},
	{Surface: StrideE10TenantSurfaceWebSocket, LegacySingletons: []string{"websocketHandler.userFromRequest", "sendKanbanEventToUser"}, HookStatus: StrideE10TenantHookPending},
	{Surface: StrideE10TenantSurfaceChat, LegacySingletons: []string{"scoutChatThread.ownerEmail", "message.AuthorEmail"}, HookStatus: StrideE10TenantHookPending},
	{Surface: StrideE10TenantSurfaceDrive, LegacySingletons: []string{"assistantFileRecord.ownerEmail", "canonicalArtifactTenantID"}, HookStatus: StrideE10TenantHookActive},
	{Surface: StrideE10TenantSurfaceProductContext, LegacySingletons: []string{"canonicalTenantID", "strideRuntimePrincipalForEmail"}, HookStatus: StrideE10TenantHookPending},
	{Surface: StrideE10TenantSurfacePushDelivery, LegacySingletons: []string{"devicePushBinding.UserEmail", "deliverWebPushForRecord"}, HookStatus: StrideE10TenantHookActive},
	{Surface: StrideE10TenantSurfaceNotification, LegacySingletons: []string{"notificationRecord.TenantID", "notificationRecord.UserEmail"}, HookStatus: StrideE10TenantHookActive},
	{Surface: StrideE10TenantSurfaceRoomAdmission, LegacySingletons: []string{"canonicalTenantID", "memberAdmissionPrincipal"}, HookStatus: StrideE10TenantHookPending},
	{Surface: StrideE10TenantSurfaceArtifactACL, LegacySingletons: []string{"canonicalArtifactTenantID", "RecallPrincipal.User.Email"}, HookStatus: StrideE10TenantHookActive},
	{Surface: StrideE10TenantSurfaceBoard, LegacySingletons: []string{"kanbanBoardApp singleton", "card creator email"}, HookStatus: StrideE10TenantHookPending},
	{Surface: StrideE10TenantSurfaceBrain, LegacySingletons: []string{"canonicalTenantID", "meetingMemory speaker email"}, HookStatus: StrideE10TenantHookPending},
	{Surface: StrideE10TenantSurfaceScout, LegacySingletons: []string{"requesterEmail", "strideRuntimePrincipalForEmail"}, HookStatus: StrideE10TenantHookPending},
	{Surface: StrideE10TenantSurfaceMarketplace, LegacySingletons: []string{"canonicalTenantID", "strideRuntimePrincipalForEmail"}, HookStatus: StrideE10TenantHookPending},
	{Surface: StrideE10TenantSurfaceWorkQueue, LegacySingletons: []string{"AgentJob requester email", "thread creator email"}, HookStatus: StrideE10TenantHookPending},
	{Surface: StrideE10TenantSurfaceCache, LegacySingletons: []string{"tenant singleton cache keys", "viewer email cache keys"}, HookStatus: StrideE10TenantHookPending},
	{Surface: StrideE10TenantSurfaceWorker, LegacySingletons: []string{"worker requester email", "canonicalTenantID"}, HookStatus: StrideE10TenantHookPending},
}

func StrideE10TenantSurfaceInventory() []StrideE10TenantSurface {
	return append([]StrideE10TenantSurface(nil), strideE10TenantSurfaceInventory...)
}

func StrideE10TenantSurfaceCoverageInventory() []StrideE10TenantSurfaceCoverage {
	result := make([]StrideE10TenantSurfaceCoverage, len(strideE10TenantCoverageInventory))
	for i, coverage := range strideE10TenantCoverageInventory {
		result[i] = StrideE10TenantSurfaceCoverage{Surface: coverage.Surface, LegacySingletons: append([]string(nil), coverage.LegacySingletons...), HookStatus: coverage.HookStatus}
	}
	return result
}

type StrideE10TenantSurfaceAdapter interface {
	Surface() StrideE10TenantSurface
	Resolve(context.Context, *StrideE10TenantConverter, string) (StrideE10TenantResolution, error)
}

type strideE10TenantSurfaceAdapter struct{ surface StrideE10TenantSurface }

func (a strideE10TenantSurfaceAdapter) Surface() StrideE10TenantSurface { return a.surface }
func (a strideE10TenantSurfaceAdapter) Resolve(ctx context.Context, converter *StrideE10TenantConverter, sessionHash string) (StrideE10TenantResolution, error) {
	return converter.Resolve(ctx, a.surface, sessionHash)
}

func StrideE10TenantSurfaceAdapters() []StrideE10TenantSurfaceAdapter {
	adapters := make([]StrideE10TenantSurfaceAdapter, 0, len(strideE10TenantSurfaceInventory))
	for _, surface := range strideE10TenantSurfaceInventory {
		adapters = append(adapters, strideE10TenantSurfaceAdapter{surface: surface})
	}
	return adapters
}

func validStrideE10TenantSurface(surface StrideE10TenantSurface) bool {
	for _, candidate := range strideE10TenantSurfaceInventory {
		if surface == candidate {
			return true
		}
	}
	return false
}

// StrideE10TenantAuthoritySnapshot is resolved atomically by the canonical
// session/membership adapter. Generation is a monotonic restart-safe CAS fence.
type StrideE10TenantAuthoritySnapshot struct {
	SessionHash   string
	Session       sessionRecord
	Person        PersonPrincipal
	Organization  Organization
	Membership    OrganizationMembership
	ActiveSession ActiveOrganizationSession
	Legacy        StrideE10LegacyPrincipalProjection
	Generation    uint64
}

type StrideE10TenantAuthorityResolver interface {
	WithCurrentTenantAuthority(context.Context, StrideE10TenantSurface, string, func(StrideE10TenantAuthoritySnapshot) error) error
}

type StrideE10TenantConversionGate interface {
	Enabled() bool
}

// StrideE10LegacyPrincipalProjection is observation-only. Raw email is
// deliberately absent; the account-subject digest must be resolved through
// StrideE10LegacyIdentityAuthority before parity can be evaluated.
type StrideE10LegacyPrincipalProjection struct {
	TenantID             string
	AccountSubjectDigest string
}

// StrideE10LegacyIdentityAuthority is the only accepted email-to-person parity
// seam. The resolver supplies a non-reversible account-subject digest; this
// authority maps it to a person while holding its read fence through the
// callback. Neither the converter nor a runtime surface compares raw email.
type StrideE10LegacyIdentityAuthority interface {
	WithMappedLegacyPerson(context.Context, string, func(string) error) error
}

type StrideE10TenantPrincipal struct {
	TenantID                     string
	PersonID                     string
	ActiveOrganizationID         string
	OrganizationMembershipID     string
	OrganizationMembershipRev    int64
	ActiveOrganizationSessionRev int64
	AuthorityGeneration          uint64
}

type StrideE10TenantConversionMode string

const (
	StrideE10TenantConversionShadow  StrideE10TenantConversionMode = "shadow"
	StrideE10TenantConversionCutover StrideE10TenantConversionMode = "cutover"
)

type StrideE10TenantDiscrepancyReceipt struct {
	ReceiptID                string
	SchemaVersion            int
	Classification           string
	KeyID                    string
	KeyVersion               int64
	Surface                  StrideE10TenantSurface
	AuthorityGeneration      uint64
	Matches                  bool
	MismatchCodes            []string
	CanonicalTenantDigest    string
	LegacyTenantDigest       string
	CanonicalPrincipalDigest string
	LegacyPrincipalDigest    string
	ReceiptMAC               string
}

func (r StrideE10TenantDiscrepancyReceipt) Validate() error {
	if !strideIdentifier(r.ReceiptID) || r.SchemaVersion != 1 || r.Classification != "private_security_audit" || !strideIdentifier(r.KeyID) || r.KeyVersion < 1 || !validStrideE10TenantSurface(r.Surface) || r.AuthorityGeneration == 0 ||
		!isHexDigest(r.CanonicalTenantDigest) || !isHexDigest(r.LegacyTenantDigest) ||
		!isHexDigest(r.CanonicalPrincipalDigest) || !isHexDigest(r.LegacyPrincipalDigest) || !isHexDigest(r.ReceiptMAC) || r.ReceiptID != "tenant-shadow-"+r.ReceiptMAC[:24] {
		return ErrStrideE10TenantAuthorityInvalid
	}
	if r.Matches != (len(r.MismatchCodes) == 0) {
		return ErrStrideE10TenantAuthorityInvalid
	}
	for i, code := range r.MismatchCodes {
		if !oneOf(code, "legacy_tenant_mismatch", "legacy_principal_mismatch") || i > 0 && r.MismatchCodes[i-1] >= code {
			return ErrStrideE10TenantAuthorityInvalid
		}
	}
	return nil
}

type StrideE10TenantReceiptKey struct {
	ID      string
	Version int64
	Secret  []byte
}

func (r StrideE10TenantDiscrepancyReceipt) ValidateWithKey(key StrideE10TenantReceiptKey) error {
	if r.Validate() != nil || key.ID != r.KeyID || key.Version != r.KeyVersion || len(key.Secret) < 32 {
		return ErrStrideE10TenantAuthorityInvalid
	}
	want := strideE10TenantReceiptMAC(key, r)
	if !hmac.Equal([]byte(want), []byte(r.ReceiptMAC)) {
		return ErrStrideE10TenantAuthorityInvalid
	}
	return nil
}

type StrideE10TenantReceiptSink interface {
	RecordStrideE10TenantDiscrepancy(context.Context, StrideE10TenantDiscrepancyReceipt) error
}

type StrideE10TenantCapability struct {
	Principal   StrideE10TenantPrincipal
	sessionHash string
	surface     StrideE10TenantSurface
}

type StrideE10TenantResolution struct {
	Observation StrideE10TenantDiscrepancyReceipt
	Capability  *StrideE10TenantCapability
}

type StrideE10TenantConverter struct {
	gate       StrideE10TenantConversionGate
	resolver   StrideE10TenantAuthorityResolver
	receipts   StrideE10TenantReceiptSink
	legacyIDs  StrideE10LegacyIdentityAuthority
	receiptKey StrideE10TenantReceiptKey
	mode       StrideE10TenantConversionMode
	now        func() time.Time
}

var strideE10TenantRuntimeConverter atomic.Pointer[StrideE10TenantConverter]

// InstallStrideE10TenantRuntimeConverter installs the route-free runtime hook.
// A nil converter, or an installed converter whose gate is off, preserves the
// exact legacy call path. Callers must still use withStrideE10TenantRuntimeAuthority
// at their final authority-sensitive effect; installing a converter alone does
// not convert or authorize any surface.
func InstallStrideE10TenantRuntimeConverter(converter *StrideE10TenantConverter) (restore func()) {
	previous := strideE10TenantRuntimeConverter.Swap(converter)
	return func() { strideE10TenantRuntimeConverter.Store(previous) }
}

func currentStrideE10TenantRuntimeConverter() *StrideE10TenantConverter {
	return strideE10TenantRuntimeConverter.Load()
}

// withStrideE10TenantRuntimeAuthority is the default-off cutover valve used by
// real runtime surfaces. Shadow executes observation best-effort and then the
// exact legacy callback: it can neither grant nor suppress authority. Cutover
// executes only the canonical callback, inside WithCurrentPrincipal's resolver
// callback so session/membership revocation is linearized through the effect.
func withStrideE10TenantRuntimeAuthority(ctx context.Context, surface StrideE10TenantSurface, sessionHash string, legacy func() error, canonical func(StrideE10TenantPrincipal) error) error {
	converter := currentStrideE10TenantRuntimeConverter()
	if converter == nil || converter.gate == nil || !converter.gate.Enabled() {
		if legacy == nil {
			return ErrStrideE10TenantAuthorityInvalid
		}
		return legacy()
	}
	if converter.mode == StrideE10TenantConversionShadow {
		_, _ = converter.Resolve(ctx, surface, sessionHash)
		if legacy == nil {
			return ErrStrideE10TenantAuthorityInvalid
		}
		return legacy()
	}
	if converter.mode != StrideE10TenantConversionCutover || canonical == nil {
		return ErrStrideE10TenantAuthorityStale
	}
	resolution, err := converter.Resolve(ctx, surface, sessionHash)
	if err != nil || resolution.Capability == nil {
		return ErrStrideE10TenantAuthorityStale
	}
	return converter.WithCurrentPrincipal(ctx, resolution.Capability, canonical)
}

func NewStrideE10TenantConverter(gate StrideE10TenantConversionGate, resolver StrideE10TenantAuthorityResolver, receipts StrideE10TenantReceiptSink, legacyIDs StrideE10LegacyIdentityAuthority, receiptKey StrideE10TenantReceiptKey, mode StrideE10TenantConversionMode) *StrideE10TenantConverter {
	receiptKey.Secret = append([]byte(nil), receiptKey.Secret...)
	return &StrideE10TenantConverter{gate: gate, resolver: resolver, receipts: receipts, legacyIDs: legacyIDs, receiptKey: receiptKey, mode: mode, now: time.Now}
}

// Resolve is route-free and default-off. sessionHash is a server-held session
// lookup key, never a client tenant selection. The resolver derives both the
// canonical snapshot and legacy shadow observation; this boundary accepts no
// client tenant or email input.
func (c *StrideE10TenantConverter) Resolve(ctx context.Context, surface StrideE10TenantSurface, sessionHash string) (StrideE10TenantResolution, error) {
	if c == nil || c.gate == nil || !c.gate.Enabled() {
		return StrideE10TenantResolution{}, ErrStrideE10TenantConversionDisabled
	}
	if c.resolver == nil || c.receipts == nil || c.legacyIDs == nil || !strideIdentifier(c.receiptKey.ID) || c.receiptKey.Version < 1 || len(c.receiptKey.Secret) < 32 || !oneOf(string(c.mode), string(StrideE10TenantConversionShadow), string(StrideE10TenantConversionCutover)) || !validStrideE10TenantSurface(surface) || !validStrideE10SessionHash(sessionHash) {
		return StrideE10TenantResolution{}, ErrStrideE10TenantAuthorityInvalid
	}
	var result StrideE10TenantResolution
	err := c.resolver.WithCurrentTenantAuthority(ctx, surface, sessionHash, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		principal, err := c.principalFromSnapshot(snapshot, sessionHash, surface)
		if err != nil {
			return err
		}
		return c.legacyIDs.WithMappedLegacyPerson(ctx, snapshot.Legacy.AccountSubjectDigest, func(mappedPersonID string) error {
			receipt := strideE10TenantComparisonReceipt(c.receiptKey, surface, principal, snapshot.Legacy, mappedPersonID)
			if receipt.ValidateWithKey(c.receiptKey) != nil || c.receipts.RecordStrideE10TenantDiscrepancy(ctx, receipt) != nil {
				return ErrStrideE10TenantAuthorityStale
			}
			result.Observation = receipt
			if c.mode == StrideE10TenantConversionCutover {
				if !receipt.Matches {
					return ErrStrideE10TenantAuthorityStale
				}
				result.Capability = &StrideE10TenantCapability{Principal: principal, sessionHash: sessionHash, surface: surface}
			}
			return nil
		})
	})
	if err != nil {
		return StrideE10TenantResolution{}, ErrStrideE10TenantAuthorityStale
	}
	return result, nil
}

// Revalidate fences a request immediately before an authority-sensitive use.
// Any restart, session switch, membership revision/status change, or resolver
// race invalidates the lease instead of falling back to email/singleton tenant.
func (c *StrideE10TenantConverter) WithCurrentPrincipal(ctx context.Context, capability *StrideE10TenantCapability, use func(StrideE10TenantPrincipal) error) error {
	if c == nil || capability == nil || use == nil || c.mode != StrideE10TenantConversionCutover || c.gate == nil || !c.gate.Enabled() || c.resolver == nil || c.legacyIDs == nil {
		return ErrStrideE10TenantAuthorityStale
	}
	err := c.resolver.WithCurrentTenantAuthority(ctx, capability.surface, capability.sessionHash, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		principal, err := c.principalFromSnapshot(snapshot, capability.sessionHash, capability.surface)
		if err != nil || principal != capability.Principal {
			return ErrStrideE10TenantAuthorityStale
		}
		return c.legacyIDs.WithMappedLegacyPerson(ctx, snapshot.Legacy.AccountSubjectDigest, func(mappedPersonID string) error {
			receipt := strideE10TenantComparisonReceipt(c.receiptKey, capability.surface, principal, snapshot.Legacy, mappedPersonID)
			if receipt.ValidateWithKey(c.receiptKey) != nil || !receipt.Matches {
				return ErrStrideE10TenantAuthorityStale
			}
			return use(principal)
		})
	})
	if err != nil {
		return ErrStrideE10TenantAuthorityStale
	}
	return nil
}

func (c *StrideE10TenantConverter) principalFromSnapshot(snapshot StrideE10TenantAuthoritySnapshot, sessionHash string, surface StrideE10TenantSurface) (StrideE10TenantPrincipal, error) {
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	session := snapshot.Session
	person := snapshot.Person
	organization := snapshot.Organization
	membership := snapshot.Membership
	activeSession := snapshot.ActiveSession
	if snapshot.SessionHash != sessionHash || snapshot.Generation == 0 || session.Kind != "" || !now.Before(session.Expires) ||
		!strideIdentifier(session.PersonID) || person.Validate() != nil || person.Status != "active" || person.Header.ID != session.PersonID {
		return StrideE10TenantPrincipal{}, ErrStrideE10TenantAuthorityStale
	}
	zeroOrganization := session.ActiveOrganizationID == "" && session.OrganizationMembershipID == "" && session.OrganizationMembershipRev == 0 && session.ActiveOrganizationSessionRev == 0
	if zeroOrganization {
		if !oneOf(string(surface), string(StrideE10TenantSurfaceAuthSession), string(StrideE10TenantSurfaceHTTP)) || organization != (Organization{}) || membership != (OrganizationMembership{}) || activeSession != (ActiveOrganizationSession{}) {
			return StrideE10TenantPrincipal{}, ErrStrideE10TenantAuthorityStale
		}
		return StrideE10TenantPrincipal{TenantID: STRIDEGlobalPersonTenant, PersonID: session.PersonID, AuthorityGeneration: snapshot.Generation}, nil
	}
	if !strideIdentifier(session.ActiveOrganizationID) || !strideIdentifier(session.OrganizationMembershipID) || organization.Validate() != nil ||
		organization.Status != "active" || organization.Header.ID != session.ActiveOrganizationID ||
		session.OrganizationMembershipRev < 1 || session.ActiveOrganizationSessionRev < 1 || membership.Validate() != nil ||
		membership.Status != "active" || membership.EndedAt != nil || membership.Header.ID != session.OrganizationMembershipID ||
		membership.Header.Revision != session.OrganizationMembershipRev || membership.PersonID != session.PersonID ||
		membership.OrganizationID != session.ActiveOrganizationID || membership.Header.TenantID != session.ActiveOrganizationID ||
		activeSession.Validate() != nil || activeSession.Status != "active" || activeSession.InvalidatedAt != nil || !now.Before(activeSession.ExpiresAt) ||
		activeSession.SessionSubjectDigest != sessionHash || activeSession.PersonID != session.PersonID ||
		activeSession.OrganizationID != session.ActiveOrganizationID || activeSession.MembershipID != session.OrganizationMembershipID ||
		activeSession.MembershipRevision != session.OrganizationMembershipRev || activeSession.SessionRevision != session.ActiveOrganizationSessionRev ||
		activeSession.Header.Revision != activeSession.SessionRevision || !activeSession.ExpiresAt.Equal(session.Expires) {
		return StrideE10TenantPrincipal{}, ErrStrideE10TenantAuthorityStale
	}
	return StrideE10TenantPrincipal{
		TenantID: session.ActiveOrganizationID, PersonID: session.PersonID,
		ActiveOrganizationID: session.ActiveOrganizationID, OrganizationMembershipID: session.OrganizationMembershipID,
		OrganizationMembershipRev:    session.OrganizationMembershipRev,
		ActiveOrganizationSessionRev: session.ActiveOrganizationSessionRev, AuthorityGeneration: snapshot.Generation,
	}, nil
}

func strideE10TenantComparisonReceipt(receiptKey StrideE10TenantReceiptKey, surface StrideE10TenantSurface, principal StrideE10TenantPrincipal, legacy StrideE10LegacyPrincipalProjection, mappedPersonID string) StrideE10TenantDiscrepancyReceipt {
	mismatches := make([]string, 0, 2)
	if strings.TrimSpace(legacy.TenantID) != principal.TenantID {
		mismatches = append(mismatches, "legacy_tenant_mismatch")
	}
	if !isHexDigest(legacy.AccountSubjectDigest) || !strideIdentifier(mappedPersonID) || mappedPersonID != principal.PersonID {
		mismatches = append(mismatches, "legacy_principal_mismatch")
	}
	sort.Strings(mismatches)
	canonicalTenantDigest := strideE10TenantCommitment(receiptKey.Secret, "canonical_tenant", principal.TenantID)
	legacyTenantDigest := strideE10TenantCommitment(receiptKey.Secret, "legacy_tenant", strings.TrimSpace(legacy.TenantID))
	canonicalPrincipalDigest := strideE10TenantCommitment(receiptKey.Secret, "canonical_principal", principal.PersonID)
	legacyPrincipalDigest := strideE10TenantCommitment(receiptKey.Secret, "legacy_principal", strings.TrimSpace(mappedPersonID)+"\x00"+legacy.AccountSubjectDigest)
	receipt := StrideE10TenantDiscrepancyReceipt{
		SchemaVersion: 1, Classification: "private_security_audit", KeyID: receiptKey.ID, KeyVersion: receiptKey.Version,
		Surface: surface, AuthorityGeneration: principal.AuthorityGeneration,
		Matches: len(mismatches) == 0, MismatchCodes: mismatches,
		CanonicalTenantDigest: canonicalTenantDigest, LegacyTenantDigest: legacyTenantDigest,
		CanonicalPrincipalDigest: canonicalPrincipalDigest, LegacyPrincipalDigest: legacyPrincipalDigest,
	}
	receipt.ReceiptMAC = strideE10TenantReceiptMAC(receiptKey, receipt)
	receipt.ReceiptID = "tenant-shadow-" + receipt.ReceiptMAC[:24]
	return receipt
}

func strideE10TenantReceiptMAC(key StrideE10TenantReceiptKey, receipt StrideE10TenantDiscrepancyReceipt) string {
	return strideE10TenantCommitment(key.Secret, "receipt_mac", strings.Join([]string{
		strconv.Itoa(receipt.SchemaVersion), receipt.Classification, key.ID, strconv.FormatInt(key.Version, 10),
		string(receipt.Surface), strconv.FormatUint(receipt.AuthorityGeneration, 10), strconv.FormatBool(receipt.Matches),
		strings.Join(receipt.MismatchCodes, ","), receipt.CanonicalTenantDigest, receipt.LegacyTenantDigest,
		receipt.CanonicalPrincipalDigest, receipt.LegacyPrincipalDigest,
	}, "\x00"))
}

func strideE10TenantCommitment(key []byte, label, value string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(label))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func validStrideE10SessionHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
