package main

import (
	"context"
	"strings"
)

// meetingAnalysisSourceEpisodeProvider keeps generic brain retrieval bound to
// the exact closed-sitting authority that published the episode. Raw transcript
// bodies are never exposed here; the default retrieval body is the durable
// post-close Brain analysis.
type meetingAnalysisSourceEpisodeProvider struct{ app *kanbanBoardApp }

func registerMeetingSourceEpisodeRuntime(app *kanbanBoardApp, registry *SourceEpisodeRuntimeRegistry) error {
	if app == nil || registry == nil {
		return ErrSourceEpisodeUnavailable
	}
	provider := &meetingAnalysisSourceEpisodeProvider{app: app}
	if err := registry.RegisterAuthority(SourceEpisodeFamilyMeetingAnalysis, provider); err != nil {
		return err
	}
	return registry.RegisterBodyReader(SourceEpisodeFamilyMeetingAnalysisBody, provider)
}

func (provider *meetingAnalysisSourceEpisodeProvider) AuthorizeSourceEpisodeMetadata(ctx context.Context, principal ACLPrincipal, episode SourceEpisode) (bool, error) {
	allowed := false
	err := provider.WithCurrentSourceEpisodeAuthority(ctx, episode, func() error {
		allowed = sourceEpisodeAudienceAllowsPrincipal(episode.Authority.Audience, principal)
		return nil
	})
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, ErrSourceEpisodeAuthorityDenied
	}
	return true, nil
}

func (provider *meetingAnalysisSourceEpisodeProvider) WithCurrentSourceEpisodeAuthority(ctx context.Context, episode SourceEpisode, use func() error) error {
	if provider == nil || provider.app == nil || provider.app.memory == nil || provider.app.meetings == nil || provider.app.sourceEpisodes == nil || use == nil ||
		episode.Kind != SourceEpisodeMeetingAnalysis || episode.Source.SourceFamily != SourceEpisodeFamilyMeetingAnalysis ||
		episode.RetrievalBody.SourceFamily != SourceEpisodeFamilyMeetingAnalysisBody {
		return ErrSourceEpisodeAuthorityStale
	}
	meetingID := strings.TrimSpace(episode.Scope.SittingID)
	if meetingID == "" || episode.Header.ID != meetingSourceEpisodeID(meetingID) || episode.Source.ObjectID != episode.Header.ID {
		return ErrSourceEpisodeAuthorityStale
	}

	// Retrieval takes the same exact-sitting exclusive lease as publication.
	// A successor sitting uses a distinct lease and live media never waits.
	lease := provider.app.memory.sourceEpisodeLease(meetingID)
	lease.Lock()
	defer lease.Unlock()
	record, found := provider.app.meetings.recordByID(meetingID)
	if !found || record.Finalization == nil || !meetingFinalizationReceiptReady(record.Finalization) || !provider.app.meetingFinalizationOutputsReady(record) {
		return ErrSourceEpisodeAuthorityStale
	}
	entries := meetingSourceEpisodeEntries(provider.app.memory, meetingID)
	fences, consentRevision, consentDigest, audience, err := resolveMeetingSourceEpisodeConsent(ctx, entries)
	if err != nil {
		return ErrSourceEpisodeAuthorityStale
	}
	consentAuthority := currentConsentLaneAuthority()
	if consentAuthority == nil {
		return ErrSourceEpisodeAuthorityStale
	}
	return consentAuthority.CommitWithFences(ctx, fences, func() error {
		material, err := derivePostCloseMeetingSourceMaterial(record, entries, fences, consentRevision, consentDigest, audience)
		if err == nil && provider.app.postgresMeetingSourceEpisodes != nil {
			material, err = provider.app.postgresMeetingSourceEpisodes.ResolveAuthority(ctx, material)
		}
		if err != nil || !meetingAnalysisSourceEpisodeMatchesMaterial(episode, material) {
			return ErrSourceEpisodeAuthorityStale
		}
		if provider.app.postgresMeetingSourceEpisodes != nil {
			return provider.app.postgresMeetingSourceEpisodes.WithCurrentSourceEpisodeAuthority(ctx, []SourceEpisode{episode}, use)
		}
		current, active, err := provider.app.currentMeetingSourceEpisode(ctx, episode.Header.ID)
		if err != nil || !active || referenceFromHeader(current.Header) != referenceFromHeader(episode.Header) {
			return ErrSourceEpisodeAuthorityStale
		}
		return use()
	})
}

func meetingAnalysisSourceEpisodeMatchesMaterial(episode SourceEpisode, material postCloseMeetingSourceMaterial) bool {
	if episode.Header.TenantID != canonicalTenantID() || episode.Scope.CompanyID != canonicalTenantID() ||
		episode.Scope.SittingID != material.Record.ID || episode.Scope.RoomID != meetingRoomID(material.Record) ||
		episode.RetrievalBody.ObjectID != material.Brain.ID || episode.RetrievalBody.ContentDigest != material.Brain.BodyDigest ||
		episode.RetrievalBody.ContentRevision != int64(material.Record.Finalization.Brain.OutputRevision) ||
		episode.RetrievalBody.SizeBytes != int64(len(material.Brain.Text)) || episode.PhaseProof.ReceiptDigest != material.ReceiptDigest ||
		!episode.OccurredStart.Equal(material.StartedAt) || !episode.OccurredEnd.Equal(material.ClosedAt) ||
		episode.Authority.ACLRevision != material.ACLRevision || episode.Authority.ACLDigest != material.ACLDigest ||
		episode.Authority.ConsentRevision != material.ConsentRev || episode.Authority.ConsentDigest != material.ConsentDigest ||
		episode.Authority.PurgeGeneration != material.Purge {
		return false
	}
	leftAudience, leftErr := canonicalJSON(episode.Authority.Audience)
	rightAudience, rightErr := canonicalJSON(material.Audience)
	return leftErr == nil && rightErr == nil && string(leftAudience) == string(rightAudience)
}

func (provider *meetingAnalysisSourceEpisodeProvider) ReadExactSourceEpisodeBody(_ context.Context, ref SourceEpisodeRevisionRef) (SourceEpisodeNativeBody, error) {
	if provider == nil || provider.app == nil || provider.app.memory == nil || ref.SourceFamily != SourceEpisodeFamilyMeetingAnalysisBody {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	entry, found := provider.app.memory.entryByKindAndID(meetingMemoryKindBrain, ref.ObjectID)
	if !found || int64(len(entry.Text)) != ref.SizeBytes || sha256Hex([]byte(entry.Text)) != ref.ContentDigest {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	revision, ok := meetingFinalizationOutputRevision(entry)
	if !ok || int64(revision) != ref.ContentRevision {
		return SourceEpisodeNativeBody{}, ErrSourceEpisodeBodyMissing
	}
	return SourceEpisodeNativeBody{Revision: ref, Body: entry.Text}, nil
}

var (
	_ SourceEpisodeBrainAuthority   = (*meetingAnalysisSourceEpisodeProvider)(nil)
	_ SourceEpisodeNativeBodyReader = (*meetingAnalysisSourceEpisodeProvider)(nil)
)
