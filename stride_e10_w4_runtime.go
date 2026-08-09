package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	strideE10W4ModeEnv          = "STRIDE_E10_W4_MODE"
	strideE10W4SnapshotPathEnv  = "STRIDE_E10_W4_SNAPSHOT_PATH"
	strideE10W4OperationPathEnv = "STRIDE_E10_W4_OPERATION_PATH"
	strideE10W4KeyIDEnv         = "STRIDE_E10_W4_KEY_ID"
	strideE10W4KeyVersionEnv    = "STRIDE_E10_W4_KEY_VERSION"
	strideE10W4KeySecretEnv     = "STRIDE_E10_W4_KEY_SECRET_BASE64"
	strideE10W4CanaryMode       = "bonfire_private_canary"
)

var strideE10W4RuntimeState struct {
	sync.RWMutex
	ready bool
}

func strideE10W4ProductionRuntimeReady() bool {
	strideE10W4RuntimeState.RLock()
	defer strideE10W4RuntimeState.RUnlock()
	return strideE10W4RuntimeState.ready
}

type strideE10W4Keyring struct{ key StrideE10MigrationMACKey }

func (k *strideE10W4Keyring) CurrentStrideE10MigrationKey(context.Context) (StrideE10MigrationMACKey, error) {
	if k == nil || validateStrideE10MigrationMACKey(k.key) != nil {
		return StrideE10MigrationMACKey{}, ErrStrideE10Invalid
	}
	return cloneStrideE10MigrationMACKey(k.key), nil
}

func (k *strideE10W4Keyring) ResolveStrideE10MigrationKey(_ context.Context, id string, version uint64) (StrideE10MigrationMACKey, error) {
	if k == nil || id != k.key.ID || version != k.key.Version || validateStrideE10MigrationMACKey(k.key) != nil {
		return StrideE10MigrationMACKey{}, ErrStrideE10Denied
	}
	return cloneStrideE10MigrationMACKey(k.key), nil
}

func (k *strideE10W4Keyring) CurrentStrideE10ProductOperationKey(context.Context) (StrideE10ProductOperationMACKey, error) {
	if k == nil || validateStrideE10MigrationMACKey(k.key) != nil {
		return StrideE10ProductOperationMACKey{}, ErrStrideE10Invalid
	}
	return StrideE10ProductOperationMACKey{ID: k.key.ID, Version: k.key.Version, Secret: append([]byte(nil), k.key.Secret...)}, nil
}

func (k *strideE10W4Keyring) ResolveStrideE10ProductOperationKey(_ context.Context, id string, version uint64) (StrideE10ProductOperationMACKey, error) {
	if k == nil || id != k.key.ID || version != k.key.Version || validateStrideE10MigrationMACKey(k.key) != nil {
		return StrideE10ProductOperationMACKey{}, ErrStrideE10Denied
	}
	return StrideE10ProductOperationMACKey{ID: k.key.ID, Version: k.key.Version, Secret: append([]byte(nil), k.key.Secret...)}, nil
}

func cloneStrideE10MigrationMACKey(key StrideE10MigrationMACKey) StrideE10MigrationMACKey {
	key.Secret = append([]byte(nil), key.Secret...)
	return key
}

func strideE10W4KeyringFromEnvironment() (*strideE10W4Keyring, error) {
	version, versionErr := strconv.ParseUint(strings.TrimSpace(os.Getenv(strideE10W4KeyVersionEnv)), 10, 64)
	secret, secretErr := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv(strideE10W4KeySecretEnv)))
	key := StrideE10MigrationMACKey{ID: strings.TrimSpace(os.Getenv(strideE10W4KeyIDEnv)), Version: version, Secret: secret}
	if versionErr != nil || secretErr != nil || validateStrideE10MigrationMACKey(key) != nil {
		return nil, ErrStrideE10Invalid
	}
	return &strideE10W4Keyring{key: key}, nil
}

type strideE10W4OrganizationSnapshot struct {
	Persons        map[string]PersonPrincipal           `json:"persons"`
	AccountPersons map[string]string                    `json:"accountPersons"`
	Profiles       map[string]PersonProfile             `json:"profiles"`
	MemberProfiles map[string]OrganizationMemberProfile `json:"memberProfiles"`
	Organizations  map[string]Organization              `json:"organizations"`
	Memberships    map[string]OrganizationMembership    `json:"memberships"`
	JoinRequests   map[string]OrganizationJoinRequest   `json:"joinRequests"`
	Sessions       map[string]ActiveOrganizationSession `json:"sessions"`
	Audit          map[string][]OrganizationAuditEvent  `json:"audit"`
	Idempotency    map[string]string                    `json:"idempotency"`
}

type strideE10W4ContributionSnapshot struct {
	Grants             map[string]ContributionAuthorityGrant `json:"grants"`
	Claims             map[string]ContributionClaim          `json:"claims"`
	ClaimHistory       map[string]ContributionClaim          `json:"claimHistory"`
	Approvals          map[string]FieldReleaseApproval       `json:"approvals"`
	ApprovalHistory    map[string]FieldReleaseApproval       `json:"approvalHistory"`
	Attestations       map[string]ContributionAttestation    `json:"attestations"`
	AttestationHistory map[string]ContributionAttestation    `json:"attestationHistory"`
	Publications       map[string]PublishedContributionClaim `json:"publications"`
	PublicationHistory map[string]PublishedContributionClaim `json:"publicationHistory"`
	Influences         map[string]AgentInfluenceReceipt      `json:"influences"`
	FencedFields       map[string]map[string]bool            `json:"fencedFields"`
	PurgeQueue         map[string]DerivedPurgeReceipt        `json:"purgeQueue"`
	PurgeGenerations   map[string]int64                      `json:"purgeGenerations"`
}

type strideE10W4NetworkSnapshot struct {
	Profiles              map[string]NetworkProfileProjection        `json:"profiles"`
	Grants                map[string]TalentSearchGrant               `json:"grants"`
	CapabilityAuthorities map[string]TalentSearchCapabilityAuthority `json:"capabilityAuthorities"`
	MembershipAuthorities map[string]NetworkMembershipAuthority      `json:"membershipAuthorities"`
	Publications          map[string]PublishedContributionClaim      `json:"publications"`
	Claims                map[string]ContributionClaim               `json:"claims"`
	Approvals             map[string]FieldReleaseApproval            `json:"approvals"`
	Attestations          map[string]ContributionAttestation         `json:"attestations"`
	ExpiryAuthorities     map[string]NetworkContactExpiryAuthority   `json:"expiryAuthorities"`
	SearchReceipts        map[string]NetworkSearchReceipt            `json:"searchReceipts"`
	Blocks                map[string]NetworkBlock                    `json:"blocks"`
	Contacts              map[string]ContactRequest                  `json:"contacts"`
	Purges                map[string]DerivedPurgeReceipt             `json:"purges"`
	SearchWindows         map[string][]networkTimedSearch            `json:"searchWindows"`
	ContactWindows        map[string][]time.Time                     `json:"contactWindows"`
	PurgeGenerations      map[string]int64                           `json:"purgeGenerations"`
	ProfileVersions       map[string]NetworkProfileProjection        `json:"profileVersions"`
	GrantVersions         map[string]TalentSearchGrant               `json:"grantVersions"`
	BlockVersions         map[string]NetworkBlock                    `json:"blockVersions"`
	ContactVersions       map[string]ContactRequest                  `json:"contactVersions"`
}

type strideE10W4RuntimeSnapshot struct {
	Organization strideE10W4OrganizationSnapshot            `json:"organization"`
	Contribution strideE10W4ContributionSnapshot            `json:"contribution"`
	Network      strideE10W4NetworkSnapshot                 `json:"network"`
	Actions      map[string]StrideE10LiveActionBinding      `json:"actions"`
	ActionUses   map[string]string                          `json:"actionUses"`
	JoinCodes    map[string]string                          `json:"joinCodes"`
	Exports      map[string]strideE10ExportReceipt          `json:"exports"`
	Packages     map[string]strideE10ExportPackage          `json:"packages"`
	Purges       map[string]map[string]any                  `json:"purges"`
	Portable     map[string]StrideE10PortableDeletionRecord `json:"portable"`
}

type strideE10W4SnapshotEnvelope struct {
	SchemaVersion uint64          `json:"schemaVersion"`
	Generation    uint64          `json:"generation"`
	KeyID         string          `json:"keyId"`
	KeyVersion    uint64          `json:"keyVersion"`
	Payload       json.RawMessage `json:"payload"`
	MAC           string          `json:"mac"`
}

func strideE10W4SnapshotMAC(key StrideE10MigrationMACKey, generation uint64, payload []byte) string {
	mac := hmac.New(sha256.New, key.Secret)
	fmt.Fprintf(mac, "meetingassist/stride/e10/w4-runtime/v1\x00%d\x00", generation)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func captureStrideE10W4OrganizationSnapshot(service *OrganizationAuthorityService) (strideE10W4OrganizationSnapshot, error) {
	if service == nil {
		return strideE10W4OrganizationSnapshot{}, ErrStrideE10Invalid
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	raw, err := json.Marshal(strideE10W4OrganizationSnapshot{
		Persons: service.persons, AccountPersons: service.accountPersons, Profiles: service.profiles,
		MemberProfiles: service.memberProfiles, Organizations: service.organizations, Memberships: service.memberships,
		JoinRequests: service.joinRequests, Sessions: service.sessions, Audit: service.audit, Idempotency: service.idempotency,
	})
	if err != nil {
		return strideE10W4OrganizationSnapshot{}, err
	}
	var snapshot strideE10W4OrganizationSnapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return strideE10W4OrganizationSnapshot{}, ErrStrideE10Invalid
	}
	return snapshot, nil
}

func captureStrideE10W4RuntimeSnapshot(runtime *StrideE10ProductLiveRuntime) (strideE10W4RuntimeSnapshot, error) {
	if runtime == nil || runtime.organization == nil || runtime.contribution == nil || runtime.network == nil {
		return strideE10W4RuntimeSnapshot{}, ErrStrideE10Invalid
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	organization, err := captureStrideE10W4OrganizationSnapshot(runtime.organization)
	if err != nil {
		return strideE10W4RuntimeSnapshot{}, err
	}
	runtime.contribution.mu.RLock()
	defer runtime.contribution.mu.RUnlock()
	runtime.network.mu.Lock()
	defer runtime.network.mu.Unlock()
	portable, ok := runtime.portableStore.(*strideE10MemoryPortableDeletionStore)
	if !ok {
		return strideE10W4RuntimeSnapshot{}, ErrStrideE10Invalid
	}
	portable.mu.RLock()
	defer portable.mu.RUnlock()
	snapshot := strideE10W4RuntimeSnapshot{Organization: organization,
		Contribution: strideE10W4ContributionSnapshot{runtime.contribution.grants, runtime.contribution.claims, runtime.contribution.claimHistory, runtime.contribution.approvals, runtime.contribution.approvalHistory, runtime.contribution.attestations, runtime.contribution.attestationHistory, runtime.contribution.publications, runtime.contribution.publicationHistory, runtime.contribution.influences, runtime.contribution.fencedFields, runtime.contribution.purgeQueue, runtime.contribution.purgeGenerations},
		Network:      strideE10W4NetworkSnapshot{runtime.network.profiles, runtime.network.grants, runtime.network.capabilityAuthorities, runtime.network.membershipAuthorities, runtime.network.publications, runtime.network.claims, runtime.network.approvals, runtime.network.attestations, runtime.network.expiryAuthorities, runtime.network.searchReceipts, runtime.network.blocks, runtime.network.contacts, runtime.network.purges, runtime.network.searchWindows, runtime.network.contactWindows, runtime.network.purgeGenerations, runtime.network.profileVersions, runtime.network.grantVersions, runtime.network.blockVersions, runtime.network.contactVersions},
		Actions:      runtime.actions, ActionUses: runtime.actionUses, JoinCodes: runtime.joinCodes, Exports: runtime.exports, Packages: runtime.packages, Purges: runtime.purges, Portable: portable.records}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return strideE10W4RuntimeSnapshot{}, err
	}
	var clone strideE10W4RuntimeSnapshot
	if json.Unmarshal(raw, &clone) != nil {
		return strideE10W4RuntimeSnapshot{}, ErrStrideE10Invalid
	}
	return clone, nil
}

func writeStrideE10W4RuntimeSnapshot(path string, generation uint64, keyring *strideE10W4Keyring, runtime *StrideE10ProductLiveRuntime) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || generation < 1 || keyring == nil {
		return ErrStrideE10Invalid
	}
	snapshot, err := captureStrideE10W4RuntimeSnapshot(runtime)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	envelope := strideE10W4SnapshotEnvelope{SchemaVersion: 1, Generation: generation, KeyID: keyring.key.ID, KeyVersion: keyring.key.Version, Payload: payload}
	envelope.MAC = strideE10W4SnapshotMAC(keyring.key, generation, payload)
	body, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomicallyDurable(path, body, 0o600)
}

func loadStrideE10W4Snapshot(path string, keyring *strideE10W4Keyring) (strideE10W4RuntimeSnapshot, uint64, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || keyring == nil {
		return strideE10W4RuntimeSnapshot{}, 0, ErrStrideE10Invalid
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return strideE10W4RuntimeSnapshot{}, 0, err
	}
	var envelope strideE10W4SnapshotEnvelope
	if json.Unmarshal(body, &envelope) != nil || envelope.SchemaVersion != 1 || envelope.Generation < 1 || envelope.KeyID != keyring.key.ID || envelope.KeyVersion != keyring.key.Version {
		return strideE10W4RuntimeSnapshot{}, 0, ErrStrideE10Denied
	}
	var compact bytes.Buffer
	if json.Compact(&compact, envelope.Payload) != nil || !hmac.Equal([]byte(envelope.MAC), []byte(strideE10W4SnapshotMAC(keyring.key, envelope.Generation, compact.Bytes()))) {
		return strideE10W4RuntimeSnapshot{}, 0, ErrStrideE10Denied
	}
	var snapshot strideE10W4RuntimeSnapshot
	if json.Unmarshal(envelope.Payload, &snapshot) != nil || len(snapshot.Organization.Persons) != 7 || len(snapshot.Organization.Organizations) != 1 || len(snapshot.Organization.Memberships) != 7 || len(snapshot.Organization.AccountPersons) != 7 {
		return strideE10W4RuntimeSnapshot{}, 0, ErrStrideE10Invalid
	}
	for _, person := range snapshot.Organization.Persons {
		if person.Validate() != nil || person.Status != "active" {
			return strideE10W4RuntimeSnapshot{}, 0, ErrStrideE10Invalid
		}
	}
	ownerCount := 0
	for _, membership := range snapshot.Organization.Memberships {
		if membership.Validate() != nil || membership.Status != "active" {
			return strideE10W4RuntimeSnapshot{}, 0, ErrStrideE10Invalid
		}
		if membership.Role == "owner" {
			ownerCount++
		}
	}
	if ownerCount != 1 {
		return strideE10W4RuntimeSnapshot{}, 0, ErrStrideE10Invalid
	}
	return snapshot, envelope.Generation, nil
}

func runtimeFromStrideE10W4Snapshot(snapshot strideE10W4RuntimeSnapshot, operationStore StrideE10ProductOperationStore) *StrideE10ProductLiveRuntime {
	runtime := newStrideE10ProductLiveRuntimeWithStores(nil, newStrideE10MemoryPortableDeletionStore(), operationStore)
	o := snapshot.Organization
	runtime.organization.persons = o.Persons
	runtime.organization.accountPersons = o.AccountPersons
	runtime.organization.profiles = o.Profiles
	runtime.organization.memberProfiles = o.MemberProfiles
	runtime.organization.organizations = o.Organizations
	runtime.organization.memberships = o.Memberships
	runtime.organization.joinRequests = o.JoinRequests
	runtime.organization.sessions = o.Sessions
	runtime.organization.audit = o.Audit
	runtime.organization.idempotency = o.Idempotency
	c := snapshot.Contribution
	runtime.contribution.grants = c.Grants
	runtime.contribution.claims = c.Claims
	runtime.contribution.claimHistory = c.ClaimHistory
	runtime.contribution.approvals = c.Approvals
	runtime.contribution.approvalHistory = c.ApprovalHistory
	runtime.contribution.attestations = c.Attestations
	runtime.contribution.attestationHistory = c.AttestationHistory
	runtime.contribution.publications = c.Publications
	runtime.contribution.publicationHistory = c.PublicationHistory
	runtime.contribution.influences = c.Influences
	runtime.contribution.fencedFields = c.FencedFields
	runtime.contribution.purgeQueue = c.PurgeQueue
	runtime.contribution.purgeGenerations = c.PurgeGenerations
	n := snapshot.Network
	runtime.network.profiles = n.Profiles
	runtime.network.grants = n.Grants
	runtime.network.capabilityAuthorities = n.CapabilityAuthorities
	runtime.network.membershipAuthorities = n.MembershipAuthorities
	runtime.network.publications = n.Publications
	runtime.network.claims = n.Claims
	runtime.network.approvals = n.Approvals
	runtime.network.attestations = n.Attestations
	runtime.network.expiryAuthorities = n.ExpiryAuthorities
	runtime.network.searchReceipts = n.SearchReceipts
	runtime.network.blocks = n.Blocks
	runtime.network.contacts = n.Contacts
	runtime.network.purges = n.Purges
	runtime.network.searchWindows = n.SearchWindows
	runtime.network.contactWindows = n.ContactWindows
	runtime.network.purgeGenerations = n.PurgeGenerations
	runtime.network.profileVersions = n.ProfileVersions
	runtime.network.grantVersions = n.GrantVersions
	runtime.network.blockVersions = n.BlockVersions
	runtime.network.contactVersions = n.ContactVersions
	runtime.actions = snapshot.Actions
	runtime.actionUses = snapshot.ActionUses
	runtime.joinCodes = snapshot.JoinCodes
	runtime.exports = snapshot.Exports
	runtime.packages = snapshot.Packages
	runtime.purges = snapshot.Purges
	runtime.portableStore.(*strideE10MemoryPortableDeletionStore).records = snapshot.Portable
	return runtime
}

func installStrideE10W4ProductionRuntimeFromEnvironment() error {
	mode := strings.TrimSpace(os.Getenv(strideE10W4ModeEnv))
	if mode == "" || mode == "off" {
		return nil
	}
	if mode != strideE10W4CanaryMode {
		return ErrStrideE10Invalid
	}
	keyring, err := strideE10W4KeyringFromEnvironment()
	if err != nil {
		return err
	}
	snapshotPath := strings.TrimSpace(os.Getenv(strideE10W4SnapshotPathEnv))
	operationPath := strings.TrimSpace(os.Getenv(strideE10W4OperationPathEnv))
	snapshot, generation, err := loadStrideE10W4Snapshot(snapshotPath, keyring)
	if err != nil {
		return err
	}
	operationStore, err := newStrideE10FileOperationStore(operationPath, keyring)
	if err != nil {
		return err
	}
	runtime := runtimeFromStrideE10W4Snapshot(snapshot, operationStore)
	for _, feature := range []STRIDEFeature{STRIDEFeaturePersonProfileAuthority, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureActiveOrganizationSession, STRIDEFeatureWorkRecordPrivate} {
		runtime.features[feature] = true
	}
	var persistMu sync.Mutex
	runtime.persistRuntime = func(current *StrideE10ProductLiveRuntime) error {
		persistMu.Lock()
		defer persistMu.Unlock()
		generation++
		return writeStrideE10W4RuntimeSnapshot(snapshotPath, generation, keyring, current)
	}
	strideE10LiveProductRuntime = runtime
	strideE10W4RuntimeState.Lock()
	strideE10W4RuntimeState.ready = true
	strideE10W4RuntimeState.Unlock()
	return nil
}

var _ StrideE10MigrationKeyring = (*strideE10W4Keyring)(nil)
var _ StrideE10ProductOperationKeyring = (*strideE10W4Keyring)(nil)
