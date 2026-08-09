package main

import (
	"sort"
	"sync"
	"time"
)

// StrideE10PortableDeletionRecord is body-free durable coordination state. It
// contains only current contract references and W1 purge receipts; governed
// claims, attestations, approvals, and audit history are intentionally absent.
type StrideE10PortableDeletionRecord struct {
	PersonID              string
	DeletedAt             time.Time
	WithdrawnPublications []STRIDEReference
	DeletedProfiles       []STRIDEReference
	RevokedExportIDs      []string
	PurgeReceipts         []DerivedPurgeReceipt
}

type StrideE10PortableDeletionStore interface {
	Load(personID string) (StrideE10PortableDeletionRecord, bool)
	Save(record StrideE10PortableDeletionRecord)
}

type strideE10MemoryPortableDeletionStore struct {
	mu      sync.RWMutex
	records map[string]StrideE10PortableDeletionRecord
}

func newStrideE10MemoryPortableDeletionStore() *strideE10MemoryPortableDeletionStore {
	return &strideE10MemoryPortableDeletionStore{records: map[string]StrideE10PortableDeletionRecord{}}
}

func (s *strideE10MemoryPortableDeletionStore) Load(personID string) (StrideE10PortableDeletionRecord, bool) {
	if s == nil {
		return StrideE10PortableDeletionRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[personID]
	return cloneStrideE10PortableDeletionRecord(record), ok
}

func (s *strideE10MemoryPortableDeletionStore) Save(record StrideE10PortableDeletionRecord) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.PersonID] = cloneStrideE10PortableDeletionRecord(record)
}

func cloneStrideE10PortableDeletionRecord(record StrideE10PortableDeletionRecord) StrideE10PortableDeletionRecord {
	result := record
	result.WithdrawnPublications = append([]STRIDEReference(nil), record.WithdrawnPublications...)
	result.DeletedProfiles = append([]STRIDEReference(nil), record.DeletedProfiles...)
	result.RevokedExportIDs = append([]string(nil), record.RevokedExportIDs...)
	result.PurgeReceipts = make([]DerivedPurgeReceipt, 0, len(record.PurgeReceipts))
	for _, receipt := range record.PurgeReceipts {
		result.PurgeReceipts = append(result.PurgeReceipts, cloneDerivedPurgeReceipt(receipt))
	}
	return result
}

// fenceStrideE10PortableAuthorities performs one lock-serialized W1 authority
// transition. Every current person publication is withdrawn and field-fenced,
// and every current Network projection is deleted and purged. The operation
// never mutates organization-governed claims, approvals, attestations, or audit.
func fenceStrideE10PortableAuthorities(contribution *ContributionAuthorityService, network *NetworkAuthority, personID string, at time.Time) (StrideE10PortableDeletionRecord, error) {
	if contribution == nil || network == nil || !strideIdentifier(personID) || at.IsZero() {
		return StrideE10PortableDeletionRecord{}, ErrStrideE10Invalid
	}
	contribution.mu.Lock()
	defer contribution.mu.Unlock()
	network.mu.Lock()
	defer network.mu.Unlock()

	publications := make([]PublishedContributionClaim, 0)
	for _, publication := range contribution.publications {
		if publication.SubjectPersonID != personID || publication.State != "published" {
			continue
		}
		authorized := false
		for _, grant := range contribution.grants {
			if grant.Role == "person_publisher" && grant.PersonID == personID && grant.Controller == publication.Controller {
				authorized = true
				break
			}
		}
		if !authorized {
			return StrideE10PortableDeletionRecord{}, ErrStrideE10Denied
		}
		publications = append(publications, cloneContract(publication))
	}
	profiles := make([]NetworkProfileProjection, 0)
	for _, profile := range network.profiles {
		if profile.SubjectPersonID != personID || profile.State == "deleted" {
			continue
		}
		if profile.Controller.Validate() != nil || profile.Controller.PrincipalID != personID {
			return StrideE10PortableDeletionRecord{}, ErrStrideE10Denied
		}
		profiles = append(profiles, cloneNetworkProjection(profile))
	}
	claims := make([]ContributionClaim, 0)
	for _, claim := range contribution.claims {
		if claim.SubjectPersonID == personID {
			claims = append(claims, cloneContract(claim))
		}
	}
	if len(publications) == 0 && len(profiles) == 0 && len(claims) == 0 {
		return StrideE10PortableDeletionRecord{}, ErrStrideE10NotFound
	}
	sort.Slice(publications, func(i, j int) bool { return publications[i].Header.ID < publications[j].Header.ID })
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Header.ID < profiles[j].Header.ID })
	sort.Slice(claims, func(i, j int) bool { return claims[i].Header.ID < claims[j].Header.ID })
	record := StrideE10PortableDeletionRecord{PersonID: personID, DeletedAt: at.UTC()}
	for _, publication := range publications {
		publication.State = "withdrawn"
		publication.Visibility = "private"
		publication.StateChangedAt = at.UTC()
		prior := refForHeader(publication.Header)
		publication.Header = nextAuthorityHeader(publication.Header, "portable_delete", at.UTC())
		publication.Supersedes = &prior
		if err := contribution.storePublicationLocked(publication); err != nil {
			return StrideE10PortableDeletionRecord{}, err
		}
		record.WithdrawnPublications = append(record.WithdrawnPublications, refForHeader(publication.Header))
		for _, effect := range contribution.fencePublicationLocked(publication, nil, "portable_delete", 0, at.UTC()) {
			record.PurgeReceipts = append(record.PurgeReceipts, cloneDerivedPurgeReceipt(effect.PurgeReceipt))
		}
	}
	for _, profile := range profiles {
		profile.State = "deleted"
		profile.Discoverability = "unlisted"
		profile.StateChangedAt = at.UTC()
		profile.PurgeGeneration = network.nextPurgeGenerationLocked(personID)
		profile.Header = nextAuthorityHeader(profile.Header, "portable_delete", at.UTC())
		receipt := network.emitPurgeLocked(personID, referenceFromHeader(profile.Header), profile.PurgeGeneration, profile.FieldsDigest, "portable_delete", at.UTC(), contributionPurgeStores)
		network.profiles[profile.Header.ID] = cloneNetworkProjection(profile)
		network.profileVersions[networkVersionKey(profile.Header.ID, profile.Header.Revision)] = cloneNetworkProjection(profile)
		network.fenceContactsLocked(personID, "", at.UTC())
		record.DeletedProfiles = append(record.DeletedProfiles, referenceFromHeader(profile.Header))
		record.PurgeReceipts = append(record.PurgeReceipts, receipt)
	}
	if len(record.PurgeReceipts) == 0 && len(claims) > 0 {
		trigger := refForHeader(claims[0].Header)
		generation := contribution.purgeGenerations[trigger.ID] + 1
		contribution.purgeGenerations[trigger.ID] = generation
		fieldsDigest := sha256Hex([]byte("portable_projection"))
		stores := make([]PurgeStoreResult, 0, len(contributionPurgeStores))
		for _, store := range contributionPurgeStores {
			stores = append(stores, PurgeStoreResult{Store: store, State: "queued", AttemptCount: 1})
		}
		receiptID := "purge_" + sha256Hex([]byte(personID + "\x00" + trigger.ID + "\x00portable_delete"))[:24]
		receipt := DerivedPurgeReceipt{Header: STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: receiptID, Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractDerivedPurgeReceipt, ContentDigest: fieldsDigest, CreatedAt: at.UTC()}, SubjectPersonID: personID, Trigger: trigger, PurgeGeneration: generation, AffectedFieldsDigest: fieldsDigest, Stores: stores, EligibilityFencedAt: at.UTC(), RecordedAt: at.UTC(), State: "queued"}
		contribution.purgeQueue[receiptID] = cloneDerivedPurgeReceipt(receipt)
		record.PurgeReceipts = append(record.PurgeReceipts, receipt)
	}
	return record, nil
}
