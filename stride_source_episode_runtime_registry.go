package main

import (
	"context"
	"sync"
	"time"
)

// SourceEpisodeRuntimeRegistry dispatches authority and exact body reads back
// to native source owners. It stores providers, never source bodies, and keeps
// the file ledger replaceable by PostgreSQL without changing retrieval.
type SourceEpisodeRuntimeRegistry struct {
	mu          sync.RWMutex
	authorities map[string]SourceEpisodeBrainAuthority
	bodies      map[string]SourceEpisodeNativeBodyReader
}

var _ SourceEpisodeBrainAuthority = (*SourceEpisodeRuntimeRegistry)(nil)
var _ SourceEpisodeNativeBodyReader = (*SourceEpisodeRuntimeRegistry)(nil)

func NewSourceEpisodeRuntimeRegistry() *SourceEpisodeRuntimeRegistry {
	return &SourceEpisodeRuntimeRegistry{
		authorities: map[string]SourceEpisodeBrainAuthority{},
		bodies:      map[string]SourceEpisodeNativeBodyReader{},
	}
}

func (registry *SourceEpisodeRuntimeRegistry) RegisterAuthority(sourceFamily string, authority SourceEpisodeBrainAuthority) error {
	if registry == nil || !strideIdentifier(sourceFamily) || authority == nil {
		return ErrSourceEpisodeUnavailable
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.authorities == nil {
		registry.authorities = map[string]SourceEpisodeBrainAuthority{}
	}
	if _, exists := registry.authorities[sourceFamily]; exists {
		return ErrSourceEpisodeConflict
	}
	registry.authorities[sourceFamily] = authority
	return nil
}

func (registry *SourceEpisodeRuntimeRegistry) RegisterBodyReader(bodyFamily string, reader SourceEpisodeNativeBodyReader) error {
	if registry == nil || !strideIdentifier(bodyFamily) || reader == nil {
		return ErrSourceEpisodeUnavailable
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.bodies == nil {
		registry.bodies = map[string]SourceEpisodeNativeBodyReader{}
	}
	if _, exists := registry.bodies[bodyFamily]; exists {
		return ErrSourceEpisodeConflict
	}
	registry.bodies[bodyFamily] = reader
	return nil
}

func (registry *SourceEpisodeRuntimeRegistry) AuthorizeSourceEpisodeMetadata(ctx context.Context, principal ACLPrincipal, episode SourceEpisode) (bool, error) {
	authority, ok := registry.authority(episode.Source.SourceFamily)
	if !ok {
		return false, ErrSourceEpisodeCatalogUnavailable
	}
	return authority.AuthorizeSourceEpisodeMetadata(ctx, principal, episode)
}

func (registry *SourceEpisodeRuntimeRegistry) WithCurrentSourceEpisodeAuthority(ctx context.Context, episode SourceEpisode, use func() error) error {
	authority, ok := registry.authority(episode.Source.SourceFamily)
	if !ok || use == nil {
		return ErrSourceEpisodeCatalogUnavailable
	}
	return authority.WithCurrentSourceEpisodeAuthority(ctx, episode, use)
}

func (registry *SourceEpisodeRuntimeRegistry) ReadExactSourceEpisodeBody(ctx context.Context, ref SourceEpisodeRevisionRef) (SourceEpisodeNativeBody, error) {
	registry.mu.RLock()
	reader, ok := registry.bodies[ref.SourceFamily]
	registry.mu.RUnlock()
	if !ok {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeCatalogUnavailable
	}
	return reader.ReadExactSourceEpisodeBody(ctx, ref)
}

func (registry *SourceEpisodeRuntimeRegistry) authority(family string) (SourceEpisodeBrainAuthority, bool) {
	if registry == nil {
		return nil, false
	}
	registry.mu.RLock()
	authority, ok := registry.authorities[family]
	registry.mu.RUnlock()
	return authority, ok
}

func NewSourceEpisodeShadowBrainAdapter(catalog DurableSourceEpisodeCatalog, registry *SourceEpisodeRuntimeRegistry, pageSize int, now func() time.Time) (*SourceEpisodeBrainAdapter, error) {
	if catalog == nil || registry == nil || pageSize < 1 {
		return nil, ErrSourceEpisodeUnavailable
	}
	return &SourceEpisodeBrainAdapter{Catalog: catalog, Authority: registry, Bodies: registry, PageSize: pageSize, Now: now}, nil
}
