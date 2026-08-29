package main

import (
	"context"
	"errors"
	"time"
)

// CanonicalSourceEpisodePurgeResolver mirrors the canonical PostgreSQL purge
// high-water into the local append-only SourceEpisode catalog before any
// inventory is read. A new canonical purge therefore durably tombstones every
// older-generation native episode across restart instead of trusting the
// historical zero generation.
type CanonicalSourceEpisodePurgeResolver struct {
	Canonical BrainPurgeGenerationResolver
	Ledger    *FileSourceEpisodeLedger
	Now       func() time.Time
}

func (resolver *CanonicalSourceEpisodePurgeResolver) CurrentPurgeGeneration(ctx context.Context, tenantID string) (int64, error) {
	if resolver == nil || resolver.Canonical == nil || resolver.Ledger == nil {
		return -1, ErrSourceEpisodeUnavailable
	}
	generation, err := resolver.Canonical.CurrentPurgeGeneration(ctx, tenantID)
	if err != nil {
		return -1, err
	}
	at := time.Now().UTC()
	if resolver.Now != nil {
		at = resolver.Now().UTC()
	}
	if err := resolver.Ledger.AdvanceTenantPurgeGeneration(ctx, tenantID, generation, at); err != nil {
		return -1, err
	}
	return generation, nil
}

// DurableSourceEpisodeAuthorizationCatalog resolves the exact body identity
// emitted as BrainEvidenceRef. It stays separate from body reads and can be
// implemented transactionally by a future PostgreSQL catalog.
type DurableSourceEpisodeAuthorizationCatalog interface {
	FindSourceEpisodeByACLObject(context.Context, ACLObjectRef) (SourceEpisode, bool, error)
}

// SourceEpisodeACLStore is the canonical-kernel bridge used by
// BrainRetrievalPlanner. It does not trust inventory admission: every object
// and grant lookup rechecks the native revision plus ACL, consent, purge and
// retention authority through the source owner. Non-SourceEpisode identities
// may fall through to an existing canonical store.
type SourceEpisodeACLStore struct {
	Catalog   DurableSourceEpisodeAuthorizationCatalog
	Authority SourceEpisodeBrainAuthority
	Delegate  ACLStore
}

var _ ACLStore = (*SourceEpisodeACLStore)(nil)

func (store *SourceEpisodeACLStore) ResolveACLObject(ctx context.Context, ref ACLObjectRef) (ACLObject, error) {
	episode, found, err := store.current(ctx, ref)
	if err != nil {
		return ACLObject{}, err
	}
	if !found {
		if store != nil && store.Delegate != nil {
			return store.Delegate.ResolveACLObject(ctx, ref)
		}
		return ACLObject{}, ErrACLObjectNotFound
	}
	return ACLObject{
		Ref: ref, RoomID: episode.Scope.RoomID, SittingID: episode.Scope.SittingID,
		CurrentContentRevision: episode.RetrievalBody.ContentRevision, CurrentContentDigest: episode.RetrievalBody.ContentDigest,
	}, nil
}

func (store *SourceEpisodeACLStore) ListACLGrants(ctx context.Context, ref ACLObjectRef) ([]ACLGrant, error) {
	episode, found, err := store.current(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !found {
		if store != nil && store.Delegate != nil {
			return store.Delegate.ListACLGrants(ctx, ref)
		}
		return nil, ErrACLObjectNotFound
	}
	actions := []ACLAction{ACLReadMetadata, ACLReadContent}
	if episode.Authority.Audience.Visibility == "organization" {
		return []ACLGrant{{
			ID: "source_episode_organization_" + episode.Header.ContentDigest[:24], TenantID: ref.TenantID,
			ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion,
			SubjectKind: ACLSubjectTeam, SubjectID: "organization", Actions: actions,
		}}, nil
	}
	grants := make([]ACLGrant, 0, len(episode.Authority.Audience.Principals))
	for _, principalID := range episode.Authority.Audience.Principals {
		grants = append(grants, ACLGrant{
			ID: "source_episode_principal_" + sha256Hex([]byte(episode.Header.ID + "\x00" + principalID))[:24], TenantID: ref.TenantID,
			ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion,
			SubjectKind: ACLSubjectPrincipal, SubjectID: principalID, SubjectPrincipalKind: ACLPrincipalUser, Actions: actions,
		})
	}
	return grants, nil
}

func (store *SourceEpisodeACLStore) current(ctx context.Context, ref ACLObjectRef) (SourceEpisode, bool, error) {
	if store == nil || store.Catalog == nil || store.Authority == nil {
		return SourceEpisode{}, false, ErrSourceEpisodeCatalogUnavailable
	}
	episode, found, err := store.Catalog.FindSourceEpisodeByACLObject(ctx, ref)
	if err != nil {
		return SourceEpisode{}, false, err
	}
	if !found {
		return SourceEpisode{}, false, nil
	}
	if episode.Validate() != nil || episode.Header.TenantID != ref.TenantID || episode.RetrievalBody.SourceFamily != ref.Type ||
		episode.RetrievalBody.ObjectID != ref.ID || episode.Authority.ACLRevision != ref.ACLVersion {
		return SourceEpisode{}, false, ErrSourceEpisodeAuthorityStale
	}
	if err := store.Authority.WithCurrentSourceEpisodeAuthority(ctx, episode, func() error { return nil }); err != nil {
		if errors.Is(err, ErrSourceEpisodeAuthorityDenied) || errors.Is(err, ErrSourceEpisodeAuthorityStale) || errors.Is(err, ErrSourceEpisodeBodyMissing) {
			return SourceEpisode{}, false, ErrACLObjectNotFound
		}
		return SourceEpisode{}, false, err
	}
	return episode, true, nil
}

func NewSourceEpisodePlanner(adapter *SourceEpisodeBrainAdapter, ledger *FileSourceEpisodeLedger, registry *SourceEpisodeRuntimeRegistry, delegate ACLStore, limits BrainPromptLimits) (BrainRetrievalPlanner, error) {
	return NewSourceEpisodeCatalogPlanner(adapter, ledger, registry, ledger, delegate, limits)
}

func NewSourceEpisodeCatalogPlanner(adapter *SourceEpisodeBrainAdapter, catalog DurableSourceEpisodeAuthorizationCatalog, registry *SourceEpisodeRuntimeRegistry, purge BrainPurgeGenerationResolver, delegate ACLStore, limits BrainPromptLimits) (BrainRetrievalPlanner, error) {
	if adapter == nil || catalog == nil || registry == nil || purge == nil || limits.validate() != nil {
		return BrainRetrievalPlanner{}, ErrSourceEpisodeUnavailable
	}
	acl := &SourceEpisodeACLStore{Catalog: catalog, Authority: registry, Delegate: delegate}
	return BrainRetrievalPlanner{
		Inventory: adapter, Bodies: adapter, Kernel: AuthorizationKernel{Store: acl}, Purge: purge, PromptLimits: limits,
	}, nil
}
