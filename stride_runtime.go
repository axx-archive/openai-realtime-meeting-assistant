package main

// This file is the app-owned integration boundary for the token-free STRIDE
// domain reducers. The individual E1-E8 libraries remain data-only; this
// boundary gives them one tenant scope, one authenticated snapshot generation,
// one lifecycle, and one truthful health surface. It deliberately contains no
// provider adapter, HTTP route, feature activation path, or production claim.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	strideRuntimeSnapshotFormat    = 1
	strideRuntimeGenerationFormat  = 1
	strideRuntimeMaxSnapshotBytes  = 64 << 20
	strideRuntimeSnapshotDomain    = "stride_runtime"
	strideRuntimeGenerationDomain  = "stride_runtime_generation"
	defaultSTRIDERuntimeSnapshot   = "stride/runtime.snapshot.json"
	defaultSTRIDERuntimeGeneration = "stride/runtime.generation.json"
)

var (
	ErrSTRIDERuntimeDisabled      = errors.New("STRIDE runtime is disabled")
	ErrSTRIDERuntimeUnavailable   = errors.New("STRIDE runtime is unavailable")
	ErrSTRIDERuntimeTenantDenied  = errors.New("STRIDE runtime tenant denied")
	ErrSTRIDERuntimeSnapshot      = errors.New("invalid STRIDE runtime snapshot")
	ErrSTRIDERuntimeGeneration    = errors.New("invalid STRIDE runtime generation")
	ErrSTRIDERuntimeClosed        = errors.New("STRIDE runtime is closed")
	ErrSTRIDERuntimeCrossTenant   = errors.New("STRIDE runtime contains cross-tenant state")
	ErrSTRIDERuntimeConfiguration = errors.New("invalid STRIDE runtime configuration")
)

type STRIDERuntimeConfig struct {
	Enabled           bool
	TenantID          string
	SnapshotPath      string
	GenerationPath    string
	Authority         STRIDESnapshotMACAuthority
	MinimumGeneration uint64
	RecallThreadIDs   []string
	// BootstrapEmpty is a one-time, explicit authority to create an empty local
	// state when neither snapshot nor generation ledger exists. It is never
	// inferred from Enabled: a missing snapshot must not silently become empty.
	BootstrapEmpty bool
	// ProductPreviewEnabled admits only the signed, deterministic-local E6-E8
	// product adapters. It is a separate default-off authority from registry
	// feature flags and can never mint a provider/runtime grant.
	ProductPreviewEnabled bool
	// RelationshipMemoryEnabled admits the durable collaboration-preference
	// store. It remains independently default-off; each subject must then grant
	// explicit persisted consent before any memory can be written or projected.
	RelationshipMemoryEnabled bool
	Now                       func() time.Time
}

type STRIDERuntimeState string

const (
	STRIDERuntimeDisabled    STRIDERuntimeState = "disabled"
	STRIDERuntimeStandby     STRIDERuntimeState = "standby"
	STRIDERuntimeUnavailable STRIDERuntimeState = "unavailable"
	STRIDERuntimeClosed      STRIDERuntimeState = "closed"
)

type STRIDERuntimeCapabilityHealth struct {
	Capability       string             `json:"capability"`
	State            STRIDERuntimeState `json:"state"`
	DurableState     bool               `json:"durableState"`
	FeatureEnabled   bool               `json:"featureEnabled"`
	ActivationFenced bool               `json:"activationFenced"`
}

type STRIDERuntimeHealth struct {
	State           STRIDERuntimeState              `json:"state"`
	Configured      bool                            `json:"configured"`
	TenantID        string                          `json:"tenantId,omitempty"`
	Restored        bool                            `json:"restored"`
	Generation      uint64                          `json:"generation"`
	LastPersistedAt time.Time                       `json:"lastPersistedAt,omitempty"`
	Error           string                          `json:"error,omitempty"`
	Capabilities    []STRIDERuntimeCapabilityHealth `json:"capabilities"`
	Features        []STRIDEFeatureState            `json:"features"`
}

// STRIDERuntimeDomains is handed only to a tenant-checked callback while the
// owning runtime lock is held. WorkOrchestrator remains structurally disabled;
// a later reviewed activation boundary must supply authorities and opt in.
type STRIDERuntimeDomains struct {
	ConversationLedger *STRIDEConversationLedger
	Registry           *STRIDERegistry
	Marketplace        *STRIDEMarketplace
	Workforce          *STRIDEWorkforceRuntime
	WorkOrchestrator   STRIDEWorkOrchestrator
	Product            *STRIDEProductState
}

type strideRuntimeTemporalSnapshot struct {
	RoomID    string          `json:"roomId"`
	SittingID string          `json:"sittingId"`
	Snapshot  json.RawMessage `json:"snapshot"`
}

type strideRuntimeSnapshotPayload struct {
	Format       int                             `json:"format"`
	TenantID     string                          `json:"tenantId"`
	Generation   uint64                          `json:"generation"`
	KeyID        string                          `json:"keyId"`
	CreatedAt    time.Time                       `json:"createdAt"`
	Conversation STRIDEConversationSnapshot      `json:"conversation"`
	Temporal     []strideRuntimeTemporalSnapshot `json:"temporal"`
	Registry     STRIDERegistrySnapshot          `json:"registry"`
	Marketplace  STRIDEMarketplaceSnapshot       `json:"marketplace"`
	Workforce    STRIDEWorkforceSnapshot         `json:"workforce"`
	WorkPayload  []byte                          `json:"workPayload"`
	WorkDigest   string                          `json:"workDigest"`
	// Pointer + omitempty preserves the canonical digest of pre-product
	// snapshots. New snapshots always include the product ledger.
	Product *STRIDEProductSnapshot `json:"product,omitempty"`
}

type strideRuntimeSnapshotEnvelope struct {
	Payload   strideRuntimeSnapshotPayload `json:"payload"`
	Digest    string                       `json:"digest"`
	Signature string                       `json:"signature"`
}

type strideRuntimeGenerationPayload struct {
	Format         int    `json:"format"`
	TenantID       string `json:"tenantId"`
	Generation     uint64 `json:"generation"`
	KeyID          string `json:"keyId"`
	SnapshotDigest string `json:"snapshotDigest"`
}

type strideRuntimeGenerationRecord struct {
	Payload   strideRuntimeGenerationPayload `json:"payload"`
	Digest    string                         `json:"digest"`
	Signature string                         `json:"signature"`
}

type strideRuntimeTenantState struct {
	conversation *STRIDEConversationLedger
	temporal     map[string]*TemporalMeetingBrain
	registry     *STRIDERegistry
	marketplace  *STRIDEMarketplace
	workforce    *STRIDEWorkforceRuntime
	workStore    *STRIDEWorkOrchestrationStore
	product      *STRIDEProductState
}

type STRIDERuntime struct {
	mu      sync.Mutex
	config  STRIDERuntimeConfig
	state   STRIDERuntimeState
	domains *strideRuntimeTenantState
	// liveTemporal is an explicitly ephemeral current-sitting projection. Raw
	// transcript brains never enter domains.temporal and are therefore excluded
	// from every compound runtime snapshot and unrelated Save.
	liveTemporal                     map[string]*TemporalMeetingBrain
	generation                       uint64
	restored                         bool
	legacyProjectionNeverInitialized bool
	legacyConversationProof          *STRIDEConversationLedger
	lastPersistedAt                  time.Time
	healthErr                        error
	closeOnce                        sync.Once
	closeErr                         error

	// meetingSpecialistObserver is runtime-only. Durable Product/Workforce
	// state remains the authority; the callback merely makes revocation of an
	// already-joined specialist synchronous with a successful mutation.
	meetingSpecialistObserverMu sync.RWMutex
	meetingSpecialistObserver   func(string) error
}

func NewSTRIDERuntime(config STRIDERuntimeConfig) (*STRIDERuntime, error) {
	config.TenantID = strings.TrimSpace(config.TenantID)
	config.SnapshotPath = filepath.Clean(strings.TrimSpace(config.SnapshotPath))
	config.GenerationPath = filepath.Clean(strings.TrimSpace(config.GenerationPath))
	config.RecallThreadIDs = append([]string(nil), config.RecallThreadIDs...)
	runtime := &STRIDERuntime{config: config, state: STRIDERuntimeDisabled, liveTemporal: map[string]*TemporalMeetingBrain{}}
	if !config.Enabled {
		snapshotExists, snapshotErr := strideRuntimeFileExists(config.SnapshotPath)
		generationExists, generationErr := strideRuntimeFileExists(config.GenerationPath)
		runtime.legacyProjectionNeverInitialized = snapshotErr == nil && generationErr == nil && !snapshotExists && !generationExists
		return runtime, nil
	}
	if err := validateSTRIDERuntimeConfig(config); err != nil {
		runtime.failClosedLocked(err)
		return runtime, err
	}

	snapshotExists, err := strideRuntimeFileExists(config.SnapshotPath)
	if err != nil {
		runtime.failClosedLocked(err)
		return runtime, err
	}
	generationExists, err := strideRuntimeFileExists(config.GenerationPath)
	if err != nil {
		runtime.failClosedLocked(err)
		return runtime, err
	}
	if !snapshotExists && !generationExists {
		if !config.BootstrapEmpty {
			err = fmt.Errorf("%w: empty bootstrap authority is absent", ErrSTRIDERuntimeGeneration)
			runtime.failClosedLocked(err)
			return runtime, err
		}
		domains, domainErr := newSTRIDERuntimeTenantState(config)
		if domainErr != nil {
			runtime.failClosedLocked(domainErr)
			return runtime, domainErr
		}
		runtime.domains = domains
		runtime.generation = config.MinimumGeneration - 1
		runtime.state = STRIDERuntimeStandby
		return runtime, nil
	}
	if snapshotExists != generationExists {
		err = fmt.Errorf("%w: snapshot and generation ledger must both exist", ErrSTRIDERuntimeGeneration)
		runtime.failClosedLocked(err)
		return runtime, err
	}

	envelope, err := readSTRIDERuntimeSnapshot(config.SnapshotPath)
	if err != nil {
		runtime.failClosedLocked(err)
		return runtime, err
	}
	generation, err := readSTRIDERuntimeGeneration(config.GenerationPath)
	if err != nil {
		runtime.failClosedLocked(err)
		return runtime, err
	}
	if err = verifySTRIDERuntimeEnvelope(config, envelope, generation); err != nil {
		runtime.failClosedLocked(err)
		return runtime, err
	}
	// Preserve a separately validated, authenticated conversation subset even
	// when a later unrelated domain (for example an obsolete registry entry)
	// makes the compound legacy runtime unavailable. The subset is read-only and
	// can prove only exact absence or an already-recorded delete; it never grants
	// authority to append while the compound runtime is unavailable.
	conversationProof, _ := RestoreSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: config.RecallThreadIDs}, envelope.Payload.Conversation)
	domains, err := restoreSTRIDERuntimeTenantState(config, envelope.Payload)
	if err != nil {
		// This proof exists only for an authenticated constructor failure before
		// the runtime can enter standby or accept a post-restore mutation. A
		// healthy runtime never retains a stale startup copy.
		runtime.legacyConversationProof = conversationProof
		runtime.failClosedLocked(err)
		return runtime, err
	}
	runtime.domains = domains
	runtime.generation = envelope.Payload.Generation
	runtime.restored = true
	runtime.lastPersistedAt = envelope.Payload.CreatedAt.UTC()
	runtime.state = STRIDERuntimeStandby
	return runtime, nil
}

func (runtime *STRIDERuntime) legacyTeamChatProjectionProvablyAbsent() bool {
	if runtime == nil {
		return false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.legacyProjectionNeverInitialized && runtime.state == STRIDERuntimeDisabled && runtime.domains == nil && runtime.generation == 0 && !runtime.restored
}

func (runtime *STRIDERuntime) authenticatedLegacyTeamChatEvent(threadID, messageID string) (ConversationEvent, bool, bool) {
	if runtime == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(messageID) == "" {
		return ConversationEvent{}, false, false
	}
	runtime.mu.Lock()
	proof := runtime.legacyConversationProof
	tenantID := runtime.config.TenantID
	runtime.mu.Unlock()
	if proof == nil {
		return ConversationEvent{}, false, false
	}
	snapshot, err := proof.Snapshot()
	if err != nil {
		return ConversationEvent{}, false, false
	}
	var latest ConversationEvent
	var sequence uint64
	found := false
	for _, record := range snapshot.Events {
		event := record.Append.Event
		if event.Header.TenantID == tenantID && event.SourceType == "channel_message" && event.ThreadID == threadID && event.SourceID == messageID && (!found || record.Sequence > sequence) {
			latest, sequence, found = event, record.Sequence, true
		}
	}
	return latest, found, true
}

func validateSTRIDERuntimeConfig(config STRIDERuntimeConfig) error {
	if !strideIdentifier(config.TenantID) || config.SnapshotPath == "." || config.GenerationPath == "." || config.SnapshotPath == config.GenerationPath ||
		!config.Authority.valid() || config.MinimumGeneration == 0 || config.MinimumGeneration == math.MaxUint64 {
		return ErrSTRIDERuntimeConfiguration
	}
	if _, err := NewSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: config.RecallThreadIDs}); err != nil {
		return fmt.Errorf("%w: conversation config", ErrSTRIDERuntimeConfiguration)
	}
	return nil
}

func newSTRIDERuntimeTenantState(config STRIDERuntimeConfig) (*strideRuntimeTenantState, error) {
	conversation, err := NewSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: config.RecallThreadIDs})
	if err != nil {
		return nil, err
	}
	return &strideRuntimeTenantState{
		conversation: conversation,
		temporal:     map[string]*TemporalMeetingBrain{},
		registry:     NewSTRIDERegistry(),
		marketplace:  NewSTRIDEMarketplace(),
		workforce:    NewSTRIDEWorkforceRuntime(),
		workStore:    NewSTRIDEWorkOrchestrationStore(),
		product:      NewSTRIDEProductState(),
	}, nil
}

func (runtime *STRIDERuntime) WithTenantDomains(tenantID string, use func(STRIDERuntimeDomains) error) error {
	if runtime == nil || use == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := runtime.requireTenantLocked(tenantID); err != nil {
		return err
	}
	domains := STRIDERuntimeDomains{
		ConversationLedger: runtime.domains.conversation,
		Registry:           runtime.domains.registry,
		Marketplace:        runtime.domains.marketplace,
		Workforce:          runtime.domains.workforce,
		WorkOrchestrator: STRIDEWorkOrchestrator{
			Enabled: false, TenantID: runtime.config.TenantID, Store: runtime.domains.workStore,
		},
		Product: runtime.domains.product,
	}
	if err := use(domains); err != nil {
		return err
	}
	if err := validateSTRIDERuntimeTenantState(runtime.config.TenantID, runtime.domains); err != nil {
		runtime.failClosedLocked(err)
		return err
	}
	return nil
}

// WithTemporalMeetingBrain serializes access to one exact tenant/room/sitting
// brain. A different configuration for an existing scope is rejected instead
// of silently reinterpreting durable evidence.
func (runtime *STRIDERuntime) WithTemporalMeetingBrain(tenantID string, config TemporalMeetingBrainConfig, use func(*TemporalMeetingBrain) error) error {
	if runtime == nil || use == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := runtime.requireTenantLocked(tenantID); err != nil {
		return err
	}
	if config.TenantID != runtime.config.TenantID || config.validate() != nil {
		return ErrSTRIDERuntimeTenantDenied
	}
	key := strideRuntimeTemporalKey(config.RoomID, config.SittingID)
	brain := runtime.domains.temporal[key]
	if brain == nil {
		var err error
		brain, err = NewTemporalMeetingBrain(config)
		if err != nil {
			return err
		}
		runtime.domains.temporal[key] = brain
	} else if brain.CurrentState().Config != config {
		return ErrTemporalBrainInvalid
	}
	if err := use(brain); err != nil {
		return err
	}
	if brain.CurrentState().Config.TenantID != runtime.config.TenantID {
		err := ErrSTRIDERuntimeCrossTenant
		runtime.failClosedLocked(err)
		return err
	}
	return nil
}

// ApplyLiveTemporalEvidence stages one current-meeting mutation while holding
// the runtime lock, commits the external consent/ACL/purge authority boundary,
// and only then publishes the staged brain in memory. The transcript row is
// already durable; this live projection deliberately does not rewrite the
// compound lifetime STRIDE snapshot. Durable post-meeting brain/digest output
// is owned by meeting finalization.
func (runtime *STRIDERuntime) ApplyLiveTemporalEvidence(tenantID string, config TemporalMeetingBrainConfig, event TemporalMeetingEvent, commitAuthority func() error) error {
	if runtime == nil || commitAuthority == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := runtime.requireTenantLocked(tenantID); err != nil {
		return err
	}
	if config.TenantID != runtime.config.TenantID || config.validate() != nil {
		return ErrSTRIDERuntimeTenantDenied
	}
	key := strideRuntimeTemporalKey(config.RoomID, config.SittingID)
	priorBrain, existed := runtime.liveTemporal[key]
	var staged *TemporalMeetingBrain
	if existed {
		if priorBrain.CurrentState().Config != config {
			return ErrTemporalBrainInvalid
		}
		staged = cloneTemporalMeetingBrain(priorBrain)
	} else {
		var err error
		staged, err = NewTemporalMeetingBrain(config)
		if err != nil {
			return err
		}
	}
	if err := staged.Apply(event); err != nil {
		return err
	}
	if staged.CurrentState().Config.TenantID != runtime.config.TenantID {
		return ErrSTRIDERuntimeCrossTenant
	}
	// Install behind runtime.mu while the canonical transaction still holds its
	// source/grant/purge locks. Readers take the same mutex, so the staged brain
	// is invisible until authority commits; a commit failure restores the exact
	// prior pointer before any reader can observe it.
	runtime.liveTemporal[key] = staged
	if err := commitAuthority(); err != nil {
		if existed {
			runtime.liveTemporal[key] = priorBrain
		} else {
			delete(runtime.liveTemporal, key)
		}
		return err
	}
	return nil
}

func (runtime *STRIDERuntime) ClearLiveTemporalMeetingBrain(tenantID, roomID, sittingID string) error {
	if runtime == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := runtime.requireTenantLocked(tenantID); err != nil {
		return err
	}
	delete(runtime.liveTemporal, strideRuntimeTemporalKey(normalizeRoomID(roomID), strings.TrimSpace(sittingID)))
	return nil
}

func cloneTemporalMeetingBrain(brain *TemporalMeetingBrain) *TemporalMeetingBrain {
	if brain == nil {
		return nil
	}
	cloned := *brain
	cloned.sources = make(map[string]TemporalTranscriptSource, len(brain.sources))
	for id, source := range brain.sources {
		cloned.sources[id] = cloneContract(source)
	}
	cloned.facts = make(map[string]TemporalMeetingFact, len(brain.facts))
	for id, fact := range brain.facts {
		cloned.facts[id] = cloneContract(fact)
	}
	cloned.purgedRevisions = make(map[string]bool, len(brain.purgedRevisions))
	for id, purged := range brain.purgedRevisions {
		cloned.purgedRevisions[id] = purged
	}
	return &cloned
}

// ReadTemporalMeetingBrain is the non-creating product read seam for one exact
// room sitting. A recall request must never manufacture an empty brain and then
// present that absence as complete coverage; projection owns creation.
func (runtime *STRIDERuntime) ReadTemporalMeetingBrain(tenantID, roomID, sittingID string, use func(*TemporalMeetingBrain) error) error {
	if runtime == nil || use == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := runtime.requireTenantLocked(tenantID); err != nil {
		return err
	}
	key := strideRuntimeTemporalKey(normalizeRoomID(roomID), strings.TrimSpace(sittingID))
	brain := runtime.liveTemporal[key]
	if brain == nil {
		brain = runtime.domains.temporal[key]
	}
	if brain == nil {
		return ErrBrainRetrievalUnavailable
	}
	if brain.CurrentState().Config.TenantID != runtime.config.TenantID {
		err := ErrSTRIDERuntimeCrossTenant
		runtime.failClosedLocked(err)
		return err
	}
	return use(brain)
}

func (runtime *STRIDERuntime) Save() error {
	if runtime == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.state == STRIDERuntimeClosed {
		return ErrSTRIDERuntimeClosed
	}
	if runtime.state == STRIDERuntimeDisabled {
		return ErrSTRIDERuntimeDisabled
	}
	if runtime.state != STRIDERuntimeStandby || runtime.domains == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	return runtime.saveLocked()
}

func (runtime *STRIDERuntime) saveLocked() error {
	if err := validateSTRIDERuntimeTenantState(runtime.config.TenantID, runtime.domains); err != nil {
		runtime.failClosedLocked(err)
		return err
	}
	nextGeneration := runtime.generation + 1
	if nextGeneration < runtime.config.MinimumGeneration {
		nextGeneration = runtime.config.MinimumGeneration
	}
	// Reserve the generation in process before authenticated sub-snapshots mutate
	// their own high-water. A later local retry advances again; it never reuses a
	// partially attempted generation.
	runtime.generation = nextGeneration
	payload, err := runtime.snapshotPayloadLocked(nextGeneration)
	if err != nil {
		runtime.failClosedLocked(err)
		return err
	}
	digest, err := STRIDEContractDigest(payload)
	if err != nil {
		runtime.failClosedLocked(err)
		return err
	}
	signature, err := strideSnapshotMAC(runtime.config.Authority, strideRuntimeSnapshotDomain, nextGeneration, digest)
	if err != nil {
		runtime.failClosedLocked(err)
		return err
	}
	envelope := strideRuntimeSnapshotEnvelope{Payload: payload, Digest: digest, Signature: signature}
	raw, err := canonicalJSON(envelope)
	if err != nil {
		runtime.failClosedLocked(err)
		return err
	}
	if err := writeFileAtomicallyDurable(runtime.config.SnapshotPath, append(raw, '\n'), 0o600); err != nil {
		runtime.failClosedLocked(fmt.Errorf("persist STRIDE runtime snapshot: %w", err))
		return runtime.healthErr
	}

	generationPayload := strideRuntimeGenerationPayload{Format: strideRuntimeGenerationFormat, TenantID: runtime.config.TenantID, Generation: nextGeneration, KeyID: runtime.config.Authority.KeyID, SnapshotDigest: digest}
	generationDigest, err := STRIDEContractDigest(generationPayload)
	if err != nil {
		runtime.failClosedLocked(err)
		return err
	}
	generationSignature, err := strideSnapshotMAC(runtime.config.Authority, strideRuntimeGenerationDomain, nextGeneration, generationDigest)
	if err != nil {
		runtime.failClosedLocked(err)
		return err
	}
	recordRaw, err := canonicalJSON(strideRuntimeGenerationRecord{Payload: generationPayload, Digest: generationDigest, Signature: generationSignature})
	if err != nil {
		runtime.failClosedLocked(err)
		return err
	}
	if err := writeFileAtomicallyDurable(runtime.config.GenerationPath, append(recordRaw, '\n'), 0o600); err != nil {
		runtime.failClosedLocked(fmt.Errorf("persist STRIDE runtime generation: %w", err))
		return runtime.healthErr
	}
	runtime.lastPersistedAt = payload.CreatedAt.UTC()
	return nil
}

func (runtime *STRIDERuntime) snapshotPayloadLocked(generation uint64) (strideRuntimeSnapshotPayload, error) {
	conversation, err := runtime.domains.conversation.Snapshot()
	if err != nil {
		return strideRuntimeSnapshotPayload{}, err
	}
	// The standalone ledger leaves its checksum empty until the first append or
	// rebuild. A runtime snapshot must also make the empty state replayable.
	if conversation.Checkpoint.Checksum == "" {
		if _, err := runtime.domains.conversation.Rebuild(); err != nil {
			return strideRuntimeSnapshotPayload{}, err
		}
		conversation, err = runtime.domains.conversation.Snapshot()
		if err != nil {
			return strideRuntimeSnapshotPayload{}, err
		}
	}
	registry, err := runtime.domains.registry.Snapshot()
	if err != nil {
		return strideRuntimeSnapshotPayload{}, err
	}
	marketplace, err := runtime.domains.marketplace.Snapshot()
	if err != nil {
		return strideRuntimeSnapshotPayload{}, err
	}
	workforce, err := runtime.domains.workforce.AuthenticatedSnapshot(runtime.config.Authority, generation)
	if err != nil {
		return strideRuntimeSnapshotPayload{}, err
	}
	workPayload, workDigest, err := runtime.domains.workStore.Snapshot()
	if err != nil {
		return strideRuntimeSnapshotPayload{}, err
	}
	product, err := runtime.domains.product.Snapshot()
	if err != nil {
		return strideRuntimeSnapshotPayload{}, err
	}
	temporal := make([]strideRuntimeTemporalSnapshot, 0, len(runtime.domains.temporal))
	for _, key := range sortedSTRIDERuntimeTemporalKeys(runtime.domains.temporal) {
		brain := runtime.domains.temporal[key]
		state := brain.CurrentState()
		raw, snapshotErr := brain.AuthenticatedSnapshot(runtime.config.Authority, generation)
		if snapshotErr != nil {
			return strideRuntimeSnapshotPayload{}, snapshotErr
		}
		temporal = append(temporal, strideRuntimeTemporalSnapshot{RoomID: state.Config.RoomID, SittingID: state.Config.SittingID, Snapshot: raw})
	}
	now := time.Now().UTC()
	if runtime.config.Now != nil {
		now = runtime.config.Now().UTC()
	}
	return strideRuntimeSnapshotPayload{
		Format: strideRuntimeSnapshotFormat, TenantID: runtime.config.TenantID, Generation: generation, KeyID: runtime.config.Authority.KeyID, CreatedAt: now,
		Conversation: conversation, Temporal: temporal, Registry: registry, Marketplace: marketplace, Workforce: workforce,
		WorkPayload: workPayload, WorkDigest: workDigest, Product: &product,
	}, nil
}

func (runtime *STRIDERuntime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.closeOnce.Do(func() {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		wasStandby := runtime.state == STRIDERuntimeStandby && runtime.domains != nil
		if wasStandby {
			runtime.closeErr = runtime.saveLocked()
		}
		if wasStandby && runtime.closeErr == nil {
			runtime.state = STRIDERuntimeClosed
		}
	})
	return runtime.closeErr
}

func (runtime *STRIDERuntime) Health() STRIDERuntimeHealth {
	if runtime == nil {
		return STRIDERuntimeHealth{State: STRIDERuntimeUnavailable, Error: ErrSTRIDERuntimeUnavailable.Error(), Capabilities: strideRuntimeCapabilityHealth(STRIDERuntimeUnavailable)}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	health := STRIDERuntimeHealth{State: runtime.state, Configured: runtime.config.Enabled, TenantID: runtime.config.TenantID, Restored: runtime.restored, Generation: runtime.generation, LastPersistedAt: runtime.lastPersistedAt}
	if runtime.healthErr != nil {
		health.Error = runtime.healthErr.Error()
	}
	health.Capabilities = strideRuntimeCapabilityHealth(runtime.state)
	if runtime.domains != nil && runtime.domains.registry != nil {
		if snapshot, err := runtime.domains.registry.Snapshot(); err == nil {
			health.Features = append([]STRIDEFeatureState(nil), snapshot.Features...)
		}
	}
	if len(health.Features) == 0 {
		for _, feature := range allSTRIDEFeatures {
			health.Features = append(health.Features, STRIDEFeatureState{Feature: feature, Enabled: false})
		}
	}
	return health
}

func (runtime *STRIDERuntime) requireTenantLocked(tenantID string) error {
	if runtime.state == STRIDERuntimeDisabled {
		return ErrSTRIDERuntimeDisabled
	}
	if runtime.state == STRIDERuntimeClosed {
		return ErrSTRIDERuntimeClosed
	}
	if runtime.state != STRIDERuntimeStandby || runtime.domains == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	if strings.TrimSpace(tenantID) != runtime.config.TenantID {
		return ErrSTRIDERuntimeTenantDenied
	}
	return nil
}

func (runtime *STRIDERuntime) failClosedLocked(err error) {
	runtime.state = STRIDERuntimeUnavailable
	runtime.domains = nil
	runtime.healthErr = err
}

func strideRuntimeCapabilityHealth(state STRIDERuntimeState) []STRIDERuntimeCapabilityHealth {
	durable := state == STRIDERuntimeStandby || state == STRIDERuntimeClosed
	capabilityState := state
	if state == STRIDERuntimeStandby {
		capabilityState = STRIDERuntimeStandby
	}
	values := []string{"conversation_ledger", "temporal_brain", "registry", "marketplace", "workforce", "work_orchestration"}
	out := make([]STRIDERuntimeCapabilityHealth, 0, len(values))
	for _, value := range values {
		out = append(out, STRIDERuntimeCapabilityHealth{Capability: value, State: capabilityState, DurableState: durable, FeatureEnabled: false, ActivationFenced: true})
	}
	return out
}

func restoreSTRIDERuntimeTenantState(config STRIDERuntimeConfig, payload strideRuntimeSnapshotPayload) (*strideRuntimeTenantState, error) {
	conversation, err := RestoreSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: config.RecallThreadIDs}, payload.Conversation)
	if err != nil {
		return nil, fmt.Errorf("%w: conversation: %v", ErrSTRIDERuntimeSnapshot, err)
	}
	registry, err := restoreSTRIDERegistry(payload.Registry)
	if err != nil {
		return nil, fmt.Errorf("%w: registry: %v", ErrSTRIDERuntimeSnapshot, err)
	}
	marketplace, err := RestoreSTRIDEMarketplace(payload.Marketplace)
	if err != nil {
		return nil, fmt.Errorf("%w: marketplace: %v", ErrSTRIDERuntimeSnapshot, err)
	}
	policy := STRIDESnapshotRestorePolicy{Authority: config.Authority, MinimumGeneration: payload.Generation}
	workforce, err := RestoreSTRIDEWorkforceRuntime(payload.Workforce, policy)
	if err != nil {
		return nil, fmt.Errorf("%w: workforce: %v", ErrSTRIDERuntimeSnapshot, err)
	}
	workStore, err := RestoreSTRIDEWorkOrchestrationStore(payload.WorkPayload, payload.WorkDigest)
	if err != nil {
		return nil, fmt.Errorf("%w: work orchestration: %v", ErrSTRIDERuntimeSnapshot, err)
	}
	product := NewSTRIDEProductState()
	if payload.Product != nil {
		product, err = RestoreSTRIDEProductState(*payload.Product)
		if err != nil {
			return nil, fmt.Errorf("%w: product: %v", ErrSTRIDERuntimeSnapshot, err)
		}
	}
	domains := &strideRuntimeTenantState{conversation: conversation, temporal: map[string]*TemporalMeetingBrain{}, registry: registry, marketplace: marketplace, workforce: workforce, workStore: workStore, product: product}
	for _, item := range payload.Temporal {
		if !strideIdentifier(item.RoomID) || !strideIdentifier(item.SittingID) {
			return nil, ErrSTRIDERuntimeSnapshot
		}
		key := strideRuntimeTemporalKey(item.RoomID, item.SittingID)
		if _, exists := domains.temporal[key]; exists {
			return nil, ErrSTRIDERuntimeSnapshot
		}
		brain, restoreErr := RestoreTemporalMeetingBrain(item.Snapshot, policy)
		if restoreErr != nil {
			return nil, fmt.Errorf("%w: temporal: %v", ErrSTRIDERuntimeSnapshot, restoreErr)
		}
		state := brain.CurrentState()
		if state.Config.TenantID != config.TenantID || state.Config.RoomID != item.RoomID || state.Config.SittingID != item.SittingID {
			return nil, ErrSTRIDERuntimeCrossTenant
		}
		domains.temporal[key] = brain
	}
	if err := validateSTRIDERuntimeTenantState(config.TenantID, domains); err != nil {
		return nil, err
	}
	return domains, nil
}

func restoreSTRIDERegistry(snapshot STRIDERegistrySnapshot) (*STRIDERegistry, error) {
	if !isHexDigest(snapshot.Digest) || len(snapshot.Features) == 0 {
		return nil, ErrSTRIDERegistryInvalid
	}
	digest, err := STRIDEContractDigest(struct {
		Entries  []STRIDERegistryEntry `json:"entries"`
		Features []STRIDEFeatureState  `json:"features"`
	}{snapshot.Entries, snapshot.Features})
	if err != nil || digest != snapshot.Digest {
		return nil, ErrSTRIDERegistryInvalid
	}
	seen := make(map[STRIDEFeature]bool, len(snapshot.Features))
	for index, feature := range snapshot.Features {
		if !validSTRIDEFeature(feature.Feature) || feature.Enabled || seen[feature.Feature] {
			return nil, ErrSTRIDERegistryInvalid
		}
		if index > 0 && snapshot.Features[index-1].Feature >= feature.Feature {
			return nil, ErrSTRIDERegistryInvalid
		}
		seen[feature.Feature] = true
	}
	registry := NewSTRIDERegistry()
	for index, entry := range snapshot.Entries {
		if !seen[entry.Feature] || index > 0 && strideRegistryEntryKey(snapshot.Entries[index-1].TenantID, snapshot.Entries[index-1].Kind, snapshot.Entries[index-1].Key) >= strideRegistryEntryKey(entry.TenantID, entry.Kind, entry.Key) {
			return nil, ErrSTRIDERegistryInvalid
		}
		if err := registry.Register(entry); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func validateSTRIDERuntimeTenantState(tenantID string, domains *strideRuntimeTenantState) error {
	if domains == nil || domains.conversation == nil || domains.registry == nil || domains.marketplace == nil || domains.workforce == nil || domains.workStore == nil || domains.product == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	conversation, err := domains.conversation.Snapshot()
	if err != nil {
		return err
	}
	for _, record := range conversation.Events {
		if record.Append.Event.Header.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	for _, edge := range conversation.Edges {
		if edge.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	for _, invalidation := range conversation.Invalidations {
		if invalidation.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	for _, brain := range domains.temporal {
		if brain == nil || brain.CurrentState().Config.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	registry, err := domains.registry.Snapshot()
	if err != nil {
		return err
	}
	for _, entry := range registry.Entries {
		if entry.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	for _, feature := range registry.Features {
		if feature.Enabled {
			return ErrSTRIDEActivationFenced
		}
	}
	marketplace, err := domains.marketplace.Snapshot()
	if err != nil {
		return err
	}
	for _, record := range marketplace.Packages {
		if record.Manifest.Header.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	for _, record := range marketplace.Listings {
		if record.Listing.Header.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	workforce, err := domains.workforce.Snapshot()
	if err != nil {
		return err
	}
	for _, record := range workforce.Learning {
		if record.Header.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	for _, record := range workforce.Performance {
		if record.Header.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	for _, record := range workforce.Updates {
		if record.Proposal.Header.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	workPayload, workDigest, err := domains.workStore.Snapshot()
	if err != nil {
		return err
	}
	workStore, err := RestoreSTRIDEWorkOrchestrationStore(workPayload, workDigest)
	if err != nil {
		return err
	}
	for _, intent := range workStore.Intents {
		if intent.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	for _, card := range workStore.Cards {
		if card.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	for _, run := range workStore.Runs {
		if run.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	for _, approval := range workStore.EffectApprovals {
		if approval.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
	}
	product, err := domains.product.Snapshot()
	if err != nil {
		return err
	}
	productWork := make(map[string]STRIDEProductWorkRecord, len(product.Work))
	for _, record := range product.Work {
		productWork[record.ID] = record
	}
	for _, state := range product.Insights {
		record, found := productWork[state.WorkID]
		if !found || state.TenantID != tenantID {
			return ErrSTRIDERuntimeCrossTenant
		}
		workflow, _, _, restoreErr := restoreSTRIDEProductInsightsState(record, state)
		if restoreErr != nil {
			return restoreErr
		}
		expectedRunIDs := make(map[string]bool, len(workflow.runs))
		expectedArtifactIDs := make(map[string]bool, len(workflow.runs))
		expectedOutcomeIDs := make(map[string]bool, len(workflow.runs))
		expectedFeedback := map[string]STRIDEWorkFeedback{}
		for _, insightsRun := range workflow.runs {
			run, exists := workStore.Runs[insightsRun.RunID]
			if !exists || run.Status != STRIDERunCompleted || run.CardID != record.ID || run.TenantID != tenantID || run.Owner != record.OwnerID || run.Reviewer != record.ReviewerID ||
				!sameReferenceSet(run.Evidence, record.SourceEvents) || record.DestinationAudience == nil || run.Destination.ThreadID != record.DestinationThreadID ||
				run.Destination.ACLVersion != record.DestinationACLVersion || !sameAudience(run.Destination.Audience, *record.DestinationAudience) {
				return ErrSTRIDEProductInvalid
			}
			expectedRunIDs[run.ID] = true
			if insightsRun.Request.ParentRunID == "" {
				if run.ID != record.RunID || run.ParentRunID != "" || run.ParentFeedbackID != "" || insightsRun.Artifact.ArtifactID != record.ArtifactID {
					return ErrSTRIDEProductInvalid
				}
			} else {
				parentInsightsRun, parentFound := workflow.runs[insightsRun.Request.ParentRunID]
				parentRun, parentStored := workStore.Runs[insightsRun.Request.ParentRunID]
				var parentFeedback StrideInsightsFeedback
				feedbackFound := false
				if parentFound {
					for _, candidate := range parentInsightsRun.Feedback {
						if candidate.Action == insightsFeedbackRequestRevision && candidate.NewRunID == insightsRun.RunID {
							parentFeedback, feedbackFound = candidate, true
							break
						}
					}
				}
				if !parentFound || !parentStored || !feedbackFound || run.ParentRunID != parentRun.ID || run.ParentFeedbackID != parentFeedback.FeedbackID ||
					run.IdempotencyDigest != temporalDigest("stride-insights-rerun/v1\x00"+parentRun.ID+"\x00"+parentFeedback.FeedbackID+"\x00"+insightsRun.Request.RequestDigest+"\x00"+insightsRun.Artifact.ArtifactDigest) ||
					len(run.Checkpoints) != 1 || run.Checkpoints[0].EvidenceDigest != insightsRun.Outcome.OutcomeDigest {
					return ErrSTRIDEProductInvalid
				}
			}

			artifactID := "artifact_" + insightsRun.RunID
			artifact, artifactFound := workStore.Artifacts[artifactID]
			if !artifactFound || artifact.RunID != run.ID || artifact.StageID != run.CurrentStage || artifact.Artifact.ContractType != STRIDEContractOutcome ||
				artifact.Artifact.ID != insightsRun.Artifact.ArtifactID || artifact.Artifact.Revision != int64(insightsRun.Request.RequestRevision) || artifact.Artifact.Digest != insightsRun.Artifact.ArtifactDigest {
				return ErrSTRIDEProductInvalid
			}
			expectedArtifactIDs[artifactID] = true
			outcomeID := "outcome_" + insightsRun.RunID
			outcome, outcomeFound := workStore.Outcomes[outcomeID]
			if !outcomeFound || outcome.RunID != run.ID || outcome.Verdict != "accepted" || len(outcome.ArtifactIDs) != 1 || outcome.ArtifactIDs[0] != artifactID || outcome.Reviewer != run.Reviewer {
				return ErrSTRIDEProductInvalid
			}
			expectedOutcomeIDs[outcomeID] = true

			for _, feedback := range insightsRun.Feedback {
				stored, exists := workStore.Feedback[feedback.FeedbackID]
				if !exists || stored.RunID != feedback.RunID || stored.Kind != strideProductWorkFeedbackKind(feedback.Action) || stored.Author != feedback.ActorID ||
					stored.BodyDigest != feedback.FeedbackDigest || !stored.CreatedAt.Equal(feedback.At) || stored.Rerun != (feedback.Action == insightsFeedbackRequestRevision) {
					return ErrSTRIDEProductInvalid
				}
				if feedback.Action == insightsFeedbackRequestRevision {
					successor, successorFound := workflow.runs[feedback.NewRunID]
					successorRun, successorStored := workStore.Runs[feedback.NewRunID]
					if !successorFound || !successorStored || successor.Request.ParentRunID != insightsRun.RunID || successor.Request.ParentReportDigest != feedback.ReportDigest ||
						successor.Request.RequestRevision != feedback.NewRequestRevision || successorRun.ParentRunID != insightsRun.RunID || successorRun.ParentFeedbackID != feedback.FeedbackID {
						return ErrSTRIDEProductInvalid
					}
				}
				expectedFeedback[feedback.FeedbackID] = stored
			}
		}
		for _, run := range workStore.Runs {
			if run.CardID == record.ID && !expectedRunIDs[run.ID] {
				return ErrSTRIDEProductInvalid
			}
		}
		for id, artifact := range workStore.Artifacts {
			if expectedRunIDs[artifact.RunID] && !expectedArtifactIDs[id] {
				return ErrSTRIDEProductInvalid
			}
		}
		for id, outcome := range workStore.Outcomes {
			if expectedRunIDs[outcome.RunID] && !expectedOutcomeIDs[id] {
				return ErrSTRIDEProductInvalid
			}
		}
		for id, feedback := range workStore.Feedback {
			if expectedRunIDs[feedback.RunID] {
				if expected, found := expectedFeedback[id]; !found || expected != feedback {
					return ErrSTRIDEProductInvalid
				}
			}
		}
	}
	return nil
}

func verifySTRIDERuntimeEnvelope(config STRIDERuntimeConfig, envelope strideRuntimeSnapshotEnvelope, generation strideRuntimeGenerationRecord) error {
	payload := envelope.Payload
	if payload.Format != strideRuntimeSnapshotFormat || payload.TenantID != config.TenantID || payload.Generation < config.MinimumGeneration || payload.KeyID != config.Authority.KeyID || payload.CreatedAt.IsZero() ||
		!isHexDigest(envelope.Digest) || !isHexDigest(payload.WorkDigest) || len(payload.WorkPayload) == 0 {
		return ErrSTRIDERuntimeSnapshot
	}
	digest, err := STRIDEContractDigest(payload)
	if err != nil || digest != envelope.Digest || !verifySTRIDESnapshotMAC(STRIDESnapshotRestorePolicy{Authority: config.Authority, MinimumGeneration: config.MinimumGeneration}, strideRuntimeSnapshotDomain, payload.KeyID, payload.Generation, envelope.Digest, envelope.Signature) {
		return ErrSTRIDERuntimeSnapshot
	}
	gp := generation.Payload
	if gp.Format != strideRuntimeGenerationFormat || gp.TenantID != config.TenantID || gp.Generation != payload.Generation || gp.KeyID != config.Authority.KeyID || gp.SnapshotDigest != envelope.Digest || !isHexDigest(generation.Digest) {
		return ErrSTRIDERuntimeGeneration
	}
	generationDigest, err := STRIDEContractDigest(gp)
	if err != nil || generationDigest != generation.Digest || !verifySTRIDESnapshotMAC(STRIDESnapshotRestorePolicy{Authority: config.Authority, MinimumGeneration: config.MinimumGeneration}, strideRuntimeGenerationDomain, gp.KeyID, gp.Generation, generation.Digest, generation.Signature) {
		return ErrSTRIDERuntimeGeneration
	}
	return nil
}

func readSTRIDERuntimeSnapshot(path string) (strideRuntimeSnapshotEnvelope, error) {
	var envelope strideRuntimeSnapshotEnvelope
	if err := readSTRIDERuntimeJSON(path, &envelope); err != nil {
		return envelope, fmt.Errorf("%w: %v", ErrSTRIDERuntimeSnapshot, err)
	}
	return envelope, nil
}

func readSTRIDERuntimeGeneration(path string) (strideRuntimeGenerationRecord, error) {
	var record strideRuntimeGenerationRecord
	if err := readSTRIDERuntimeJSON(path, &record); err != nil {
		return record, fmt.Errorf("%w: %v", ErrSTRIDERuntimeGeneration, err)
	}
	return record, nil
}

func readSTRIDERuntimeJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, strideRuntimeMaxSnapshotBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrSTRIDERuntimeSnapshot
	}
	if info, err := file.Stat(); err != nil || info.Size() > strideRuntimeMaxSnapshotBytes {
		return ErrSTRIDERuntimeSnapshot
	}
	return nil
}

func strideRuntimeFileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func strideRuntimeTemporalKey(roomID, sittingID string) string { return roomID + "\x00" + sittingID }

func sortedSTRIDERuntimeTemporalKeys(values map[string]*TemporalMeetingBrain) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func initializeSTRIDERuntimeFromEnvironment() (*STRIDERuntime, error) {
	enabled := boolEnv("STRIDE_RUNTIME_ENABLED")
	dataDir := filepath.Dir(meetingMemoryPath())
	config := STRIDERuntimeConfig{
		Enabled: enabled, TenantID: canonicalTenantID(),
		SnapshotPath: filepath.Join(dataDir, defaultSTRIDERuntimeSnapshot), GenerationPath: filepath.Join(dataDir, defaultSTRIDERuntimeGeneration),
		BootstrapEmpty:            boolEnv("STRIDE_RUNTIME_BOOTSTRAP_EMPTY"),
		ProductPreviewEnabled:     boolEnv("STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED"),
		RelationshipMemoryEnabled: boolEnv("STRIDE_RELATIONSHIP_MEMORY_ENABLED"),
	}
	if !enabled {
		return NewSTRIDERuntime(config)
	}
	if value := strings.TrimSpace(os.Getenv("STRIDE_RUNTIME_SNAPSHOT_PATH")); value != "" {
		config.SnapshotPath = value
	}
	if value := strings.TrimSpace(os.Getenv("STRIDE_RUNTIME_GENERATION_PATH")); value != "" {
		config.GenerationPath = value
	}
	if value := strings.TrimSpace(os.Getenv("STRIDE_RUNTIME_RECALL_THREAD_IDS")); value != "" {
		for _, threadID := range strings.Split(value, ",") {
			if threadID = strings.TrimSpace(threadID); threadID != "" {
				config.RecallThreadIDs = append(config.RecallThreadIDs, threadID)
			}
		}
	}
	minimum, err := strconv.ParseUint(strings.TrimSpace(os.Getenv("STRIDE_RUNTIME_MIN_GENERATION")), 10, 64)
	if err != nil || minimum == 0 {
		runtime := &STRIDERuntime{config: config, state: STRIDERuntimeUnavailable, healthErr: ErrSTRIDERuntimeConfiguration}
		return runtime, ErrSTRIDERuntimeConfiguration
	}
	config.MinimumGeneration = minimum
	key, err := decodeSTRIDERuntimeMACKey(os.Getenv("STRIDE_RUNTIME_SNAPSHOT_MAC_KEY"))
	if err != nil {
		runtime := &STRIDERuntime{config: config, state: STRIDERuntimeUnavailable, healthErr: err}
		return runtime, err
	}
	config.Authority = STRIDESnapshotMACAuthority{KeyID: strings.TrimSpace(os.Getenv("STRIDE_RUNTIME_SNAPSHOT_KEY_ID")), Key: key}
	return NewSTRIDERuntime(config)
}

func decodeSTRIDERuntimeMACKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, ErrSTRIDERuntimeConfiguration
	}
	if strings.HasPrefix(value, "base64:") {
		value = strings.TrimPrefix(value, "base64:")
	}
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) < strideSnapshotMinimumMACKeyBytes {
		return nil, ErrSTRIDERuntimeConfiguration
	}
	return append([]byte(nil), key...), nil
}
