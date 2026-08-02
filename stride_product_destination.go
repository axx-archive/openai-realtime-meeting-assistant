package main

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// strideProductRecommendDestination inspects only project threads the
// requesting human can see, then independently proves that every Suggested
// Work recipient is present and that the destination is no wider than the
// source audience. It never creates a thread and never starts work.
func (app *kanbanBoardApp) strideProductRecommendDestination(sourceThreadID, outcome string, sourceAudience STRIDEAudience, recipientIDs []string, viewerEmail string, now time.Time) strideProductDestinationProposal {
	eligibleThreadIDs := []string{}
	audit := STRIDEProductDestinationRoutingAudit{
		Resolver:       strideProductDestinationResolver,
		Version:        strideProductDestinationResolverV1,
		EvaluatedAt:    now.UTC(),
		SourceThreadID: strings.TrimSpace(sourceThreadID),
		MatchBasis:     "no_match",
	}
	manual := func(reason string) strideProductDestinationProposal {
		recommendation := STRIDEProductDestinationRecommendation{
			Status:                 strideProductDestinationManual,
			Confidence:             0,
			Reasons:                []string{reason},
			ParticipantEligibility: "unresolved",
			EligibleThreadIDs:      append([]string(nil), eligibleThreadIDs...),
			Audit:                  audit,
		}
		digest, err := strideProductDestinationRecommendationDigest(recommendation)
		if err == nil {
			recommendation.Audit.Digest = digest
		}
		return strideProductDestinationProposal{Recommendation: &recommendation}
	}
	if app == nil || sourceAudience.Validate() != nil || !strideIdentifier(audit.SourceThreadID) || len(uniqueSortedStrings(recipientIDs)) == 0 || normalizeAccountEmail(viewerEmail) == "" || audit.EvaluatedAt.IsZero() {
		return manual("Choose an existing project or create a new one before approval.")
	}

	threads := app.scoutChatThreadsSnapshot(viewerEmail, false, 0)
	candidates := make([]STRIDEProjectThreadCandidate, 0, len(threads))
	byID := make(map[string]scoutChatThreadRecord, len(threads))
	hasEligibleSourceThread := false
	for _, thread := range threads {
		if strideProductProjectDestinationEligible(thread) && thread.ID == audit.SourceThreadID {
			hasEligibleSourceThread = true
			break
		}
	}
	for _, thread := range threads {
		if !strideProductProjectDestinationEligible(thread) {
			continue
		}
		audit.ConsideredCandidates++
		audience, aclVersion, authorityErr := strideProductProjectDestinationAuthority(thread)
		relevant := thread.ID == audit.SourceThreadID
		if !hasEligibleSourceThread {
			relevant = strideProductOutcomeNamesProject(outcome, thread.Title)
		}
		authorized := authorityErr == nil && strideAudiencePrincipalsSubset(audience, sourceAudience) && setContainsAll(stringSet(audience.Principals), stringSet(recipientIDs))
		if authorized {
			eligibleThreadIDs = append(eligibleThreadIDs, thread.ID)
		}
		if relevant {
			audit.RelevantCandidates++
			if authorized {
				audit.AuthorizedRelevantCandidates++
			}
		}
		candidate := STRIDEProjectThreadCandidate{
			ThreadID: thread.ID, ProjectIDs: []string{strideProductProjectKey(thread.Title)}, ParticipantIDs: append([]string(nil), audience.Principals...),
			Authorized: authorized, Archived: thread.ArchivedAt != "", Relevant: relevant, Audience: audience, ACLVersion: aclVersion,
		}
		candidates = append(candidates, candidate)
		byID[thread.ID] = thread
	}
	sort.Strings(eligibleThreadIDs)

	resolution := ResolveSTRIDEProjectThread(nil, recipientIDs, candidates)
	if resolution.Status != STRIDEThreadReuse {
		switch {
		case resolution.Status == STRIDEThreadExplicitChoice:
			audit.MatchBasis = "ambiguous"
			return manual("More than one authorized project matches. Choose the right project before approval.")
		case audit.RelevantCandidates > 0 && audit.AuthorizedRelevantCandidates == 0:
			audit.MatchBasis = "unauthorized"
			return manual("A matching project is not eligible for every relevant participant. Choose another project or create a correctly scoped one.")
		default:
			audit.MatchBasis = "no_match"
			return manual("No authorized existing project exactly matches. Choose a project or create a new one before approval.")
		}
	}
	thread, found := byID[resolution.ThreadID]
	if !found {
		return manual("Choose an existing project or create a new one before approval.")
	}
	basis := "exact_project_title"
	confidence := .96
	reasons := []string{"The outcome names this existing project.", "Every relevant participant can access it at the recorded ACL revision."}
	if thread.ID == audit.SourceThreadID {
		basis = "source_project_thread"
		confidence = .99
		reasons[0] = "The source conversation is already this project thread."
	}
	audit.MatchBasis = basis
	recommendation := STRIDEProductDestinationRecommendation{
		Status: strideProductDestinationRecommended, ThreadID: resolution.ThreadID, Title: thread.Title, Confidence: confidence, Reasons: reasons,
		ParticipantEligibility: "eligible", EligiblePrincipals: uniqueSortedStrings(recipientIDs), EligibleThreadIDs: append([]string(nil), eligibleThreadIDs...), ACLVersion: resolution.ACLVersion, Audit: audit,
	}
	auditDigest, err := strideProductDestinationRecommendationDigest(recommendation)
	if err != nil {
		return manual("Choose an existing project or create a new one before approval.")
	}
	recommendation.Audit.Digest = auditDigest
	audience := cloneAudience(resolution.Audience)
	return strideProductDestinationProposal{Recommendation: &recommendation, Audience: &audience}
}

func strideProductProjectKey(value string) string {
	words := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.Join(words, "-")
}

func strideProductOutcomeNamesProject(outcome, title string) bool {
	outcomeKey := strings.ReplaceAll(strideProductProjectKey(outcome), "-", " ")
	titleKey := strings.ReplaceAll(strideProductProjectKey(strings.TrimPrefix(strings.TrimSpace(title), "#")), "-", " ")
	if outcomeKey == "" || titleKey == "" {
		return false
	}
	return strings.Contains(" "+outcomeKey+" ", " "+titleKey+" ")
}
