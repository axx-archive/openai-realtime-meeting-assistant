package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const strideLearningAuditPathEnvironment = "STRIDE_LEARNING_AUDIT_PATH"

func strideLearningAuditPath() string {
	if path := strings.TrimSpace(os.Getenv(strideLearningAuditPathEnvironment)); path != "" {
		return path
	}
	if path := strings.TrimSpace(meetingMemoryPath()); path != "" {
		return filepath.Join(filepath.Dir(path), "stride-learning-audit.jsonl")
	}
	return ""
}

func strideLearningSourceScope(episode SourceEpisode) (STRIDELearningScope, string) {
	if len(episode.Scope.ProjectIDs) > 0 {
		return STRIDELearningProject, episode.Scope.ProjectIDs[0]
	}
	if episode.Authority.Audience.Visibility == "private" && len(episode.Authority.Audience.Principals) > 0 {
		return STRIDELearningPerson, episode.Authority.Audience.Principals[0]
	}
	return STRIDELearningCompany, episode.Header.TenantID
}

func sourceEpisodeReference(episode SourceEpisode) STRIDEReference {
	return STRIDEReference{ContractType: STRIDEContractSourceEpisode, ID: episode.Header.ID, Revision: episode.Header.Revision, Digest: episode.Header.ContentDigest}
}

// withCurrentSTRIDELearningSourceAuthority re-resolves every exact source
// revision carried by a candidate and holds the native source authority while
// use runs. The audit snapshot is evidence of what was true at proposal time;
// it is never treated as current authority for evaluation or activation.
func (app *kanbanBoardApp) withCurrentSTRIDELearningSourceAuthority(ctx context.Context, candidate STRIDEReviewedLearningCandidate, use func() error) error {
	if app == nil || app.sourceEpisodes == nil || app.sourceEpisodeRegistry == nil || use == nil || candidate.Validate() != nil {
		return ErrSTRIDELearningUnavailable
	}
	var visit func(int) error
	visit = func(index int) error {
		if index == len(candidate.SourceEpisodes) {
			return use()
		}
		ref := candidate.SourceEpisodes[index]
		current, found, err := app.sourceEpisodes.CurrentSourceEpisode(ctx, candidate.TenantID, ref.ID)
		if err != nil {
			return errors.Join(ErrSTRIDELearningUnavailable, err)
		}
		authorityDigest, digestErr := STRIDEContractDigest(current.Authority)
		if !found || digestErr != nil || sourceEpisodeReference(current) != ref ||
			current.Authority.ACLRevision != candidate.Authority.ACLRevision ||
			current.Authority.ConsentRevision != candidate.Authority.ConsentRevision ||
			current.Authority.PurgeGeneration != candidate.Authority.PurgeGeneration ||
			authorityDigest != candidate.Authority.SourceAuthorityDigest ||
			!sameSTRIDELearningAudience(current.Authority.Audience, candidate.Authority.SourceAudience) {
			return ErrSTRIDELearningPrivacy
		}
		return app.sourceEpisodeRegistry.WithCurrentSourceEpisodeAuthority(ctx, current, func() error {
			// Hold the durable head as well as the native authority. This makes
			// the evaluation/ratification publication indivisible against a
			// concurrent tombstone, superseding revision, or tenant purge.
			if leaseErr := app.sourceEpisodes.WithCurrentSourceEpisodeLease(ctx, candidate.TenantID, ref, func() error {
				return visit(index + 1)
			}); leaseErr != nil {
				if errors.Is(leaseErr, ErrSourceEpisodeUnavailable) || errors.Is(leaseErr, ErrSourceEpisodeConflict) {
					return ErrSTRIDELearningPrivacy
				}
				return errors.Join(ErrSTRIDELearningUnavailable, leaseErr)
			}
			return nil
		})
	}
	return visit(0)
}

func (app *kanbanBoardApp) completedWorkLearningAdmitted(learning STRIDEProductAgentLearning) bool {
	if app == nil || app.learningAudits == nil || learning.Origin != "completed_work" || !strideIdentifier(learning.ID) {
		return false
	}
	state, err := app.learningAudits.State(learning.ID)
	if err != nil || state.Status != "active" || state.Ratification == nil {
		return false
	}
	return app.withCurrentSTRIDELearningSourceAuthority(context.Background(), state.Candidate, func() error { return nil }) == nil
}

func (app *kanbanBoardApp) installCompletedWorkLearningAdmission() {
	if app == nil || app.strideRuntime == nil {
		return
	}
	app.strideRuntime.mu.Lock()
	if app.strideRuntime.domains != nil && app.strideRuntime.domains.product != nil {
		app.strideRuntime.domains.product.setCompletedWorkLearningAdmission(app.completedWorkLearningAdmitted)
	}
	app.strideRuntime.mu.Unlock()
}

func (app *kanbanBoardApp) proposeGovernedLearningFromCompletedWork(agentID, learningID string, thread scoutAgentThread, artifact meetingMemoryEntry, summary string, proposedAt time.Time) error {
	if app == nil || app.learningAudits == nil || app.sourceEpisodes == nil || app.workRuns == nil {
		return ErrSTRIDELearningUnavailable
	}
	if !validSTRIDEWorkAgent(agentID) || !strideIdentifier(learningID) || !strideIdentifier(thread.ID) || proposedAt.IsZero() {
		return ErrSTRIDELearningInvalid
	}
	episode, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), workArtifactSourceEpisodeTenant(artifact), workArtifactSourceEpisodeID(artifact.ID))
	if err != nil || !found {
		return ErrSTRIDELearningUnavailable
	}
	card, err := app.workRuns.SideCard(thread.ID)
	if err != nil || card.Status != "completed" {
		return ErrSTRIDELearningUnavailable
	}
	runDigest, err := STRIDEContractDigest(card.Run)
	if err != nil {
		return err
	}
	sourceRef := sourceEpisodeReference(episode)
	workRef := STRIDEReference{ContractType: STRIDEContractWorkRun, ID: card.Run.ID, Revision: 1, Digest: runDigest}
	outcomeRef := strideWorkRunArtifactReference(artifact)
	scope, scopeID := strideLearningSourceScope(episode)
	lessonDigest := sha256Hex([]byte(strings.TrimSpace(summary)))
	learningRecord := AgentLearningRecord{
		Header: STRIDEContractHeader{
			TenantID: episode.Header.TenantID, ID: "learning-record-" + temporalDigest(learningID)[:20], Revision: 1,
			SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractAgentLearningRecord,
			ContentDigest: sha256Hex([]byte("stride-learning-record/v1\x00" + lessonDigest + "\x00" + sourceRef.Digest)), CreatedAt: proposedAt.UTC(),
		},
		AgentID: agentID, Kind: "competency_candidate", Subject: scopeID, Scope: string(scope), LessonDigest: lessonDigest,
		Evidence: []STRIDEReference{sourceRef}, Confidence: .6, FirstObserved: episode.OccurredEnd, LastObserved: episode.OccurredEnd,
		ReinforcementCount: 1, Audience: episode.Authority.Audience, Status: "candidate",
	}
	authorityDigest, err := STRIDEContractDigest(episode.Authority)
	if err != nil {
		return err
	}
	candidate := STRIDEReviewedLearningCandidate{
		ID: learningID, TenantID: episode.Header.TenantID,
		IdempotencyKeyDigest: sha256Hex([]byte("stride-learning-candidate/v1\x00" + learningID + "\x00" + sourceRef.Digest + "\x00" + outcomeRef.Digest)),
		Agent:                agentID, Scope: scope, ScopeID: scopeID, Impact: "procedure", Learning: learningRecord,
		SourceEpisodes: []STRIDEReference{sourceRef}, WorkRuns: []STRIDEReference{workRef}, Outcomes: []STRIDEReference{outcomeRef},
		Authority: STRIDELearningAuthorityFence{
			SourceScope: scope, SourceScopeID: scopeID, SourceAudience: episode.Authority.Audience, CandidateAudience: episode.Authority.Audience,
			ACLRevision: episode.Authority.ACLRevision, ConsentRevision: episode.Authority.ConsentRevision, PurgeGeneration: episode.Authority.PurgeGeneration,
			SourceAuthorityDigest: authorityDigest, ObservedAt: episode.Authority.ObservedAt,
		},
		ProposedBy: "system-learning-review", ProposedAt: proposedAt.UTC(),
	}
	candidate.AuthorityFenceDigest, err = candidate.FenceDigest()
	if err != nil || candidate.Validate() != nil {
		return ErrSTRIDELearningInvalid
	}
	event := STRIDELearningAuditEvent{
		ID: "learning-proposed-" + temporalDigest(learningID)[:20], TenantID: candidate.TenantID,
		IdempotencyKeyDigest: sha256Hex([]byte("stride-learning-audit/proposed/v1\x00" + candidate.IdempotencyKeyDigest)),
		Type:                 STRIDELearningCandidateProposed, CandidateID: candidate.ID, ActorKind: "system", Actor: "system-learning-review",
		Summary: "Learning candidate proposed from completed governed work", Candidate: &candidate, OccurredAt: proposedAt.UTC(),
	}
	_, _, err = app.learningAudits.Append(event)
	return err
}

// completed-work learning cannot enter active provider context through the
// legacy one-click mutation. Approval mirrors an already active governed
// candidate only after held-out evaluation, qualification, review, and human
// ratification. Forget remains immediate and conservative.
func (app *kanbanBoardApp) authorizeCompletedWorkLearningResolution(agent STRIDEProductTeamAgent, learningID, action, actor string, at time.Time, commit func() error) error {
	var learning *STRIDEProductAgentLearning
	for index := range agent.Learning {
		if agent.Learning[index].ID == learningID {
			learning = &agent.Learning[index]
			break
		}
	}
	if learning == nil || learning.Origin != "completed_work" {
		if commit != nil {
			return commit()
		}
		return nil
	}
	if app == nil || app.learningAudits == nil {
		return ErrSTRIDELearningUnavailable
	}
	state, err := app.learningAudits.State(learningID)
	if err != nil {
		return err
	}
	switch action {
	case "approve":
		return app.withCurrentSTRIDELearningSourceAuthority(context.Background(), state.Candidate, func() error {
			// Re-read after acquiring the source lease. A concurrent evaluator or
			// reviewer may have advanced the immutable audit before this request.
			state, err = app.learningAudits.State(learningID)
			if err != nil {
				return err
			}
			if state.Status == "active" {
				if commit != nil {
					return commit()
				}
				return nil
			}
			if state.Qualification == nil || !oneOf(state.Status, "candidate", "reviewed") {
				return ErrSTRIDELearningGate
			}
			if state.Status == "candidate" {
				reviewed := STRIDELearningAuditEvent{
					ID: "learning-reviewed-" + temporalDigest(learningID + "\x00" + actor)[:20], TenantID: state.Candidate.TenantID,
					IdempotencyKeyDigest: sha256Hex([]byte("stride-learning-audit/reviewed/v1\x00" + learningID + "\x00" + actor)),
					Type:                 STRIDELearningCandidateReviewed, CandidateID: learningID, ActorKind: "human", Actor: actor,
					Summary: "Human reviewed the evidence and held-out evaluation", OccurredAt: at.UTC(),
				}
				if _, _, err := app.learningAudits.Append(reviewed); err != nil && !errors.Is(err, ErrSTRIDELearningConflict) {
					return err
				}
				state, err = app.learningAudits.State(learningID)
				if err != nil {
					return err
				}
			}
			candidateDigest, digestErr := state.Candidate.Digest()
			if digestErr != nil || state.Qualification == nil {
				return ErrSTRIDELearningGate
			}
			ratification := STRIDELearningHumanRatification{
				ID: "learning-ratification-" + temporalDigest(learningID + "\x00" + actor)[:20], CandidateID: learningID,
				CandidateDigest: candidateDigest, QualificationID: state.Qualification.ID, QualificationDigest: state.Qualification.QualificationDigest,
				Decision: "approved", RatifiedBy: actor, RatifiedAt: at.UTC(),
			}
			ratified := STRIDELearningAuditEvent{
				ID: "learning-activated-" + temporalDigest(learningID + "\x00" + actor)[:20], TenantID: state.Candidate.TenantID,
				IdempotencyKeyDigest: sha256Hex([]byte("stride-learning-audit/activated/v1\x00" + learningID + "\x00" + actor)),
				Type:                 STRIDELearningActivationRatified, CandidateID: learningID, ActorKind: "human", Actor: actor,
				Summary: "Human ratified the qualified learning change", Ratification: &ratification, OccurredAt: at.UTC(),
			}
			if _, _, err := app.learningAudits.Append(ratified); err != nil {
				return err
			}
			if commit != nil {
				return commit()
			}
			return nil
		})
	case "correct":
		return ErrSTRIDELearningGate
	case "forget":
		event := STRIDELearningAuditEvent{
			ID: "learning-forgotten-" + temporalDigest(learningID + "\x00" + actor)[:20], TenantID: state.Candidate.TenantID,
			IdempotencyKeyDigest: sha256Hex([]byte("stride-learning-audit/forgotten/v1\x00" + learningID + "\x00" + actor)),
			Type:                 STRIDELearningCandidateForgotten, CandidateID: learningID, ActorKind: "human", Actor: actor,
			Summary: "Human requested that this learning be forgotten", OccurredAt: at.UTC(),
		}
		_, _, err = app.learningAudits.Append(event)
		if errors.Is(err, ErrSTRIDELearningConflict) && state.Status == "forgotten" {
			err = nil
		}
		if err != nil {
			return err
		}
		if commit != nil {
			return commit()
		}
		return nil
	default:
		return ErrSTRIDELearningInvalid
	}
}

// recordGovernedLearningEvaluation is the trusted held-out evaluator seam. It
// records the before/after receipt and deterministic policy qualification; it
// never activates learning and cannot substitute for the later human action.
func (app *kanbanBoardApp) recordGovernedLearningEvaluation(candidateID string, receipt STRIDELearningEvaluationReceipt, policy STRIDELearningActivationPolicy, at time.Time) error {
	if app == nil || app.learningAudits == nil || !strideIdentifier(candidateID) || at.IsZero() {
		return ErrSTRIDELearningUnavailable
	}
	state, err := app.learningAudits.State(candidateID)
	if err != nil || state.Status != "candidate" || state.Evaluation != nil || state.Qualification != nil {
		return ErrSTRIDELearningGate
	}
	return app.withCurrentSTRIDELearningSourceAuthority(context.Background(), state.Candidate, func() error {
		evaluated := STRIDELearningAuditEvent{
			ID: "learning-evaluated-" + temporalDigest(candidateID + "\x00" + receipt.ID)[:20], TenantID: state.Candidate.TenantID,
			IdempotencyKeyDigest: sha256Hex([]byte("stride-learning-audit/evaluated/v1\x00" + candidateID + "\x00" + receipt.ReceiptDigest)),
			Type:                 STRIDELearningEvaluationRecorded, CandidateID: candidateID, ActorKind: "system", Actor: "system-eval-heldout",
			Summary: "Held-out before and after evaluation recorded", Evaluation: &receipt, OccurredAt: receipt.EvaluatedAt.UTC(),
		}
		if _, _, err := app.learningAudits.Append(evaluated); err != nil {
			return err
		}
		qualification, err := QualifySTRIDELearningActivation(state.Candidate, receipt, policy, at.UTC())
		if err != nil {
			return err
		}
		qualified := STRIDELearningAuditEvent{
			ID: "learning-qualified-" + temporalDigest(candidateID + "\x00" + qualification.ID)[:20], TenantID: state.Candidate.TenantID,
			IdempotencyKeyDigest: sha256Hex([]byte("stride-learning-audit/qualified/v1\x00" + candidateID + "\x00" + qualification.QualificationDigest)),
			Type:                 STRIDELearningActivationQualified, CandidateID: candidateID, ActorKind: "system", Actor: "system-eval-heldout",
			Summary: "Candidate passed the governed activation quality gate", Qualification: &qualification, OccurredAt: at.UTC(),
		}
		_, _, err = app.learningAudits.Append(qualified)
		return err
	})
}
