package main

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrProjectAuthorityInvalid  = errors.New("invalid Project authority request")
	ErrProjectAuthorityDenied   = errors.New("Project authority denied")
	ErrProjectAuthorityNotFound = errors.New("Project not found")
	ErrProjectAuthorityConflict = errors.New("Project revision conflict")
)

// ProjectAuthorityContext is the already-held canonical organization session
// fence. HTTP bodies and client project IDs can never construct one.
type ProjectAuthorityContext struct {
	Person        PersonPrincipal
	Organization  Organization
	Membership    OrganizationMembership
	ActiveSession ActiveOrganizationSession
	Generation    uint64
	At            time.Time
}

func (v ProjectAuthorityContext) Validate() error {
	if v.Person.Validate() != nil || v.Person.Status != "active" || v.Organization.Validate() != nil || v.Organization.Status != "active" ||
		v.Membership.Validate() != nil || v.Membership.Status != "active" || v.ActiveSession.Validate() != nil || v.ActiveSession.Status != "active" ||
		v.Membership.PersonID != v.Person.Header.ID || v.Membership.OrganizationID != v.Organization.Header.ID ||
		v.ActiveSession.PersonID != v.Person.Header.ID || v.ActiveSession.OrganizationID != v.Organization.Header.ID ||
		v.ActiveSession.MembershipID != v.Membership.Header.ID || v.ActiveSession.MembershipRevision != v.Membership.Header.Revision ||
		v.Generation < 1 || v.Generation != uint64(v.ActiveSession.SessionRevision) || v.At.IsZero() || !v.At.Before(v.ActiveSession.ExpiresAt) {
		return ErrProjectAuthorityDenied
	}
	return nil
}

type ProjectAuthorityFence interface {
	WithCurrentProjectAuthority(ProjectAuthorityContext, func() error) error
}

type ProjectSourceAuthoritySnapshot struct {
	ReceiptID              string
	Subject                STRIDEReference
	SourceRefs             []STRIDEReference
	EvidenceCoverageDigest string
	Audience               STRIDEAudience
	ACLRevision            int64
	ACLDigest              string
	ConsentRevision        int64
	PurgeGeneration        uint64
	ExpiresAt              time.Time
}

func (v ProjectSourceAuthoritySnapshot) Validate() error {
	if !strideIdentifier(v.ReceiptID) || v.Subject.Validate() != nil || !validateSTRIDERefs(v.SourceRefs) || !isHexDigest(v.EvidenceCoverageDigest) ||
		v.Audience.Validate() != nil || v.ACLRevision < 1 || !isHexDigest(v.ACLDigest) || v.ConsentRevision < 1 || v.PurgeGeneration < 1 || v.ExpiresAt.IsZero() {
		return ErrProjectAuthorityDenied
	}
	return nil
}

type ProjectSourceAuthorityResolver interface {
	WithCurrentProjectSource(ProjectAuthorityContext, STRIDEReference, []STRIDEReference, func(ProjectSourceAuthoritySnapshot) error) error
}

type ProjectSourceAuthorityRequest struct {
	Subject    STRIDEReference
	SourceRefs []STRIDEReference
}

// ProjectSourceAuthorityBatchResolver holds every source authorization in one
// resolver snapshot. Project Record rebuild/read requires this stronger
// boundary so no source can drift midway through a returned aggregate.
type ProjectSourceAuthorityBatchResolver interface {
	WithCurrentProjectSources(ProjectAuthorityContext, []ProjectSourceAuthorityRequest, func([]ProjectSourceAuthoritySnapshot) error) error
}

// ProjectAuthorityService is route-free and provider-free. It freezes the
// domain transaction semantics before a PostgreSQL adapter or product route is
// allowed to expose them.
type ProjectAuthorityService struct {
	mu           sync.RWMutex
	fence        ProjectAuthorityFence
	sources      ProjectSourceAuthorityResolver
	projects     map[string]Project
	bindings     map[string]ProjectThreadBinding
	associations map[string]ProjectAssociation
	events       map[string][]ProjectAssociationEvent
	idempotency  map[string]string
	version      uint64
}

func NewProjectAuthorityService(fence ProjectAuthorityFence, sources ...ProjectSourceAuthorityResolver) *ProjectAuthorityService {
	var sourceResolver ProjectSourceAuthorityResolver
	if len(sources) == 1 {
		sourceResolver = sources[0]
	}
	return &ProjectAuthorityService{
		fence: fence, sources: sourceResolver, projects: map[string]Project{}, bindings: map[string]ProjectThreadBinding{},
		associations: map[string]ProjectAssociation{}, events: map[string][]ProjectAssociationEvent{}, idempotency: map[string]string{},
	}
}

func (s *ProjectAuthorityService) CreateProject(authority ProjectAuthorityContext, project Project, binding ProjectThreadBinding, operationDigest string) error {
	if s == nil || project.Validate() != nil || binding.Validate() != nil || !isHexDigest(operationDigest) ||
		project.Header.Revision != 1 || project.OrganizationID != authority.Organization.Header.ID ||
		project.CreatorPersonID != authority.Person.Header.ID || binding.Project.ID != project.ProjectID ||
		binding.Project.Revision != project.Header.Revision || binding.Project.Digest != project.Header.ContentDigest ||
		binding.Kind != "primary" || binding.State != "active" || !projectControllerMatches(project, authority.Membership) ||
		binding.ActorPersonID != authority.Person.Header.ID || binding.ActorMembershipID != authority.Membership.Header.ID ||
		binding.ActorMembershipRevision != authority.Membership.Header.Revision {
		return ErrProjectAuthorityInvalid
	}
	if err := s.requireCurrent(authority); err != nil {
		return err
	}
	fingerprint, _ := STRIDEContractDigest(struct {
		Project Project
		Binding ProjectThreadBinding
	}{project, binding})
	return s.withCurrent(authority, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err, replay := s.idempotentLocked(project.OrganizationID, projectOperationCreateProject, operationDigest, fingerprint); replay {
			return err
		}
		if _, exists := s.projects[project.ProjectID]; exists || s.activeThreadBoundLocked(binding.ThreadID) {
			s.idempotency[projectIdempotencyKey(project.OrganizationID, projectOperationCreateProject, operationDigest)] = "conflict:" + fingerprint
			return ErrProjectAuthorityConflict
		}
		s.projects[project.ProjectID] = cloneProject(project)
		s.bindings[binding.Header.ID] = binding
		s.idempotency[projectIdempotencyKey(project.OrganizationID, projectOperationCreateProject, operationDigest)] = fingerprint
		s.version++
		return nil
	})
}

func (s *ProjectAuthorityService) ReviseProject(authority ProjectAuthorityContext, expectedRevision int64, next Project, operationDigest string) error {
	if s == nil || expectedRevision < 1 || next.Validate() != nil || !isHexDigest(operationDigest) || next.Header.Revision != expectedRevision+1 {
		return ErrProjectAuthorityInvalid
	}
	if err := s.requireCurrent(authority); err != nil {
		return err
	}
	fingerprint, _ := STRIDEContractDigest(next)
	return s.withCurrent(authority, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err, replay := s.idempotentLocked(next.OrganizationID, projectOperationReviseProject, operationDigest, fingerprint); replay {
			return err
		}
		current, ok := s.projects[next.ProjectID]
		if !ok || current.OrganizationID != authority.Organization.Header.ID {
			return ErrProjectAuthorityNotFound
		}
		if !projectControllerMatches(current, authority.Membership) {
			return ErrProjectAuthorityDenied
		}
		if current.Header.Revision != expectedRevision || next.Header.TenantID != current.Header.TenantID ||
			next.ProjectID != current.ProjectID || next.OrganizationID != current.OrganizationID ||
			next.CreatorPersonID != current.CreatorPersonID || !next.CreatedAt.Equal(current.CreatedAt) || next.Supersedes == nil || next.Supersedes.Digest != current.Header.ContentDigest ||
			!legalProjectLifecycle(current.Lifecycle, next.Lifecycle) || next.ACLRevision < current.ACLRevision {
			return ErrProjectAuthorityConflict
		}
		s.projects[next.ProjectID] = cloneProject(next)
		s.idempotency[projectIdempotencyKey(next.OrganizationID, projectOperationReviseProject, operationDigest)] = fingerprint
		s.version++
		return nil
	})
}

func (s *ProjectAuthorityService) ProposeAssociation(authority ProjectAuthorityContext, association ProjectAssociation, event ProjectAssociationEvent) error {
	if s == nil || association.Validate() != nil || event.Validate() != nil || association.State != "proposed" || association.Header.Revision != 1 ||
		event.Action != "propose" || event.Association.ID != association.Header.ID || event.Association.Revision != association.Header.Revision ||
		event.Association.Digest != association.Header.ContentDigest || !associationAuthorityMatches(association, authority) || !eventAuthorityMatches(event, authority) ||
		event.IdempotencyKeyDigest != association.IdempotencyKeyDigest {
		return ErrProjectAuthorityInvalid
	}
	if err := s.requireCurrent(authority); err != nil {
		return err
	}
	return s.withCurrentSource(authority, association.Subject, association.SourceRefs, func(source ProjectSourceAuthoritySnapshot) error {
		if !associationMatchesSource(association, source) {
			return ErrProjectAuthorityDenied
		}
		return s.withCurrent(authority, func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			if err, replay := s.idempotentLocked(authority.Organization.Header.ID, "propose_association", event.IdempotencyKeyDigest, association.Header.ContentDigest); replay {
				return err
			}
			project, ok := s.projects[association.Project.ID]
			if !ok || project.OrganizationID != authority.Organization.Header.ID || project.Lifecycle != "active" ||
				project.Header.Revision != association.Project.Revision || project.Header.ContentDigest != association.Project.Digest || !projectVisibleTo(project, authority) {
				return ErrProjectAuthorityDenied
			}
			if _, exists := s.associations[association.Header.ID]; exists {
				return ErrProjectAuthorityConflict
			}
			s.associations[association.Header.ID] = cloneProjectAssociation(association)
			s.events[association.Header.ID] = append(s.events[association.Header.ID], event)
			s.idempotency[projectIdempotencyKey(authority.Organization.Header.ID, "propose_association", event.IdempotencyKeyDigest)] = association.Header.ContentDigest
			s.version++
			return nil
		})
	})
}

func (s *ProjectAuthorityService) TransitionAssociation(authority ProjectAuthorityContext, expectedRevision int64, next ProjectAssociation, event ProjectAssociationEvent) error {
	if s == nil || expectedRevision < 1 || next.Validate() != nil || event.Validate() != nil ||
		next.Header.Revision != expectedRevision+1 || event.Association.ID != next.Header.ID || event.Association.Revision != next.Header.Revision ||
		event.Association.Digest != next.Header.ContentDigest || !associationAuthorityMatches(next, authority) || !eventAuthorityMatches(event, authority) ||
		event.IdempotencyKeyDigest != next.IdempotencyKeyDigest || event.Action == "correct" {
		return ErrProjectAuthorityInvalid
	}
	if err := s.requireCurrent(authority); err != nil {
		return err
	}
	return s.withCurrentSource(authority, next.Subject, next.SourceRefs, func(source ProjectSourceAuthoritySnapshot) error {
		if !associationMatchesSource(next, source) {
			return ErrProjectAuthorityDenied
		}
		return s.withCurrent(authority, func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			if err, replay := s.idempotentLocked(authority.Organization.Header.ID, "transition_association", event.IdempotencyKeyDigest, next.Header.ContentDigest); replay {
				return err
			}
			current, ok := s.associations[next.Header.ID]
			if !ok {
				return ErrProjectAuthorityNotFound
			}
			if current.Header.Revision != expectedRevision || current.Project != next.Project || current.Subject != next.Subject || next.Supersedes == nil || next.Supersedes.Digest != current.Header.ContentDigest ||
				!sameSTRIDEReferences(current.SourceRefs, next.SourceRefs) || !legalAssociationTransition(current, next, event.Action, authority.At) {
				return ErrProjectAuthorityConflict
			}
			project, ok := s.projects[next.Project.ID]
			if !ok || project.OrganizationID != authority.Organization.Header.ID || project.Header.Revision != next.Project.Revision || project.Header.ContentDigest != next.Project.Digest || !projectVisibleTo(project, authority) {
				return ErrProjectAuthorityDenied
			}
			s.associations[next.Header.ID] = cloneProjectAssociation(next)
			s.events[next.Header.ID] = append(s.events[next.Header.ID], event)
			s.idempotency[projectIdempotencyKey(authority.Organization.Header.ID, "transition_association", event.IdempotencyKeyDigest)] = next.Header.ContentDigest
			s.version++
			return nil
		})
	})
}

// CorrectAssociation terminalizes the old edge and creates the replacement in
// one lock/transaction. A caller can never observe both as current-confirmed.
func (s *ProjectAuthorityService) CorrectAssociation(authority ProjectAuthorityContext, expectedRevision int64, corrected, replacement ProjectAssociation, event ProjectAssociationEvent) error {
	if s == nil || expectedRevision < 1 || corrected.Validate() != nil || replacement.Validate() != nil || event.Validate() != nil ||
		corrected.State != "corrected" || replacement.State != "confirmed" || replacement.Header.Revision != 1 || corrected.Header.Revision != expectedRevision+1 ||
		corrected.Replacement == nil || *corrected.Replacement != projectAssociationRef(replacement) || event.Action != "correct" || event.Replacement == nil ||
		*event.Replacement != projectAssociationRef(replacement) || event.Association != projectAssociationRef(corrected) ||
		!associationAuthorityMatches(corrected, authority) || !associationAuthorityMatches(replacement, authority) || !eventAuthorityMatches(event, authority) ||
		event.IdempotencyKeyDigest != corrected.IdempotencyKeyDigest || replacement.IdempotencyKeyDigest != corrected.IdempotencyKeyDigest ||
		corrected.Subject != replacement.Subject || !sameSTRIDEReferences(corrected.SourceRefs, replacement.SourceRefs) {
		return ErrProjectAuthorityInvalid
	}
	if err := s.requireCurrent(authority); err != nil {
		return err
	}
	return s.withCurrentSource(authority, corrected.Subject, corrected.SourceRefs, func(oldSource ProjectSourceAuthoritySnapshot) error {
		// Correction preserves the exact canonical subject/source authority
		// snapshot; only the Project edge changes. Resolve it once so callback-
		// based resolvers can hold a non-reentrant lock through the commit.
		if !associationMatchesSource(corrected, oldSource) || !associationMatchesSource(replacement, oldSource) {
			return ErrProjectAuthorityDenied
		}
		return s.withCurrent(authority, func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			fingerprint := corrected.Header.ContentDigest + ":" + replacement.Header.ContentDigest
			if err, replay := s.idempotentLocked(authority.Organization.Header.ID, "correct_association", event.IdempotencyKeyDigest, fingerprint); replay {
				return err
			}
			current, ok := s.associations[corrected.Header.ID]
			if !ok || current.Header.Revision != expectedRevision || current.State != "confirmed" || corrected.Supersedes == nil || corrected.Supersedes.Digest != current.Header.ContentDigest ||
				current.Project != corrected.Project || current.Subject != corrected.Subject || !sameSTRIDEReferences(current.SourceRefs, corrected.SourceRefs) {
				return ErrProjectAuthorityConflict
			}
			if _, exists := s.associations[replacement.Header.ID]; exists {
				return ErrProjectAuthorityConflict
			}
			oldProject, oldOK := s.projects[corrected.Project.ID]
			newProject, newOK := s.projects[replacement.Project.ID]
			if !oldOK || !newOK || oldProject.OrganizationID != authority.Organization.Header.ID || newProject.OrganizationID != authority.Organization.Header.ID ||
				oldProject.Header.Revision != corrected.Project.Revision || oldProject.Header.ContentDigest != corrected.Project.Digest ||
				newProject.Header.Revision != replacement.Project.Revision || newProject.Header.ContentDigest != replacement.Project.Digest ||
				newProject.Lifecycle != "active" || !projectVisibleTo(oldProject, authority) || !projectVisibleTo(newProject, authority) {
				return ErrProjectAuthorityDenied
			}
			s.associations[corrected.Header.ID] = cloneProjectAssociation(corrected)
			s.associations[replacement.Header.ID] = cloneProjectAssociation(replacement)
			s.events[corrected.Header.ID] = append(s.events[corrected.Header.ID], event)
			replacementEvent := event
			replacementEvent.Header.ID = event.Header.ID + ":replacement"
			replacementEvent.Association = projectAssociationRef(replacement)
			replacementEvent.Action = "confirm"
			replacementEvent.ResultingState = "confirmed"
			replacementEvent.PriorRevision = 0
			replacementEvent.NewRevision = 1
			replacementEvent.Replacement = nil
			s.events[replacement.Header.ID] = append(s.events[replacement.Header.ID], replacementEvent)
			s.idempotency[projectIdempotencyKey(authority.Organization.Header.ID, "correct_association", event.IdempotencyKeyDigest)] = fingerprint
			s.version++
			return nil
		})
	})
}

func (s *ProjectAuthorityService) VisibleProjects(authority ProjectAuthorityContext) ([]Project, error) {
	if s == nil {
		return nil, ErrProjectAuthorityInvalid
	}
	if err := s.requireCurrent(authority); err != nil {
		return nil, err
	}
	var result []Project
	err := s.withCurrent(authority, func() error {
		s.mu.RLock()
		defer s.mu.RUnlock()
		result = make([]Project, 0)
		for _, project := range s.projects {
			if project.OrganizationID == authority.Organization.Header.ID && project.Lifecycle != "archived" && projectVisibleTo(project, authority) {
				result = append(result, cloneProject(project))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ProjectID < result[j].ProjectID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func (s *ProjectAuthorityService) projectForViewer(authority ProjectAuthorityContext, projectID string) (Project, error) {
	if s == nil || !strideIdentifier(projectID) {
		return Project{}, ErrProjectAuthorityInvalid
	}
	if err := s.requireCurrent(authority); err != nil {
		return Project{}, err
	}
	var result Project
	err := s.withCurrent(authority, func() error {
		s.mu.RLock()
		defer s.mu.RUnlock()
		project, ok := s.projects[projectID]
		if !ok || project.OrganizationID != authority.Organization.Header.ID || !projectVisibleTo(project, authority) {
			return ErrProjectAuthorityNotFound
		}
		result = cloneProject(project)
		return nil
	})
	return result, err
}

func (s *ProjectAuthorityService) CurrentAssociation(authority ProjectAuthorityContext, associationID string) (ProjectAssociation, error) {
	if s == nil || !strideIdentifier(associationID) {
		return ProjectAssociation{}, ErrProjectAuthorityInvalid
	}
	if err := s.requireCurrent(authority); err != nil {
		return ProjectAssociation{}, err
	}
	s.mu.RLock()
	association, ok := s.associations[associationID]
	if !ok {
		s.mu.RUnlock()
		return ProjectAssociation{}, ErrProjectAuthorityNotFound
	}
	project, ok := s.projects[association.Project.ID]
	if !ok || project.OrganizationID != authority.Organization.Header.ID || !projectVisibleTo(project, authority) {
		s.mu.RUnlock()
		return ProjectAssociation{}, ErrProjectAuthorityNotFound
	}
	copy := cloneProjectAssociation(association)
	s.mu.RUnlock()
	err := s.withCurrentSource(authority, copy.Subject, copy.SourceRefs, func(source ProjectSourceAuthoritySnapshot) error {
		if !associationMatchesSource(copy, source) {
			return ErrProjectAuthorityNotFound
		}
		return s.withCurrent(authority, func() error {
			s.mu.RLock()
			defer s.mu.RUnlock()
			current, stillCurrent := s.associations[associationID]
			if !stillCurrent || current.Header.Revision != copy.Header.Revision || current.Header.ContentDigest != copy.Header.ContentDigest {
				return ErrProjectAuthorityConflict
			}
			return nil
		})
	})
	if err != nil {
		return ProjectAssociation{}, err
	}
	return copy, nil
}

func (s *ProjectAuthorityService) CurrentProjectAssociations(authority ProjectAuthorityContext, projectID string) ([]ProjectAssociation, error) {
	var result []ProjectAssociation
	err := s.withProjectAssociationSnapshot(authority, projectID, func(_ Project, associations []ProjectAssociation) error {
		result = make([]ProjectAssociation, len(associations))
		for index := range associations {
			result[index] = cloneProjectAssociation(associations[index])
		}
		return nil
	})
	return result, err
}

var errProjectSnapshotDrift = errors.New("Project authority snapshot drifted")

// withProjectAssociationSnapshot holds all source receipts, the organization
// authority fence, and the domain read lock through the caller's effect. A
// version check turns any mutation between discovery and lock acquisition into
// a bounded retry rather than a silently incomplete Project Record.
func (s *ProjectAuthorityService) withProjectAssociationSnapshot(authority ProjectAuthorityContext, projectID string, effect func(Project, []ProjectAssociation) error) error {
	if s == nil || !strideIdentifier(projectID) || effect == nil {
		return ErrProjectAuthorityInvalid
	}
	batch, ok := s.sources.(ProjectSourceAuthorityBatchResolver)
	if !ok {
		return ErrProjectAuthorityDenied
	}
	for attempt := 0; attempt < 4; attempt++ {
		s.mu.RLock()
		version := s.version
		ids := make([]string, 0)
		for id, association := range s.associations {
			if association.Project.ID == projectID {
				ids = append(ids, id)
			}
		}
		s.mu.RUnlock()
		sort.Strings(ids)
		requests := make([]ProjectSourceAuthorityRequest, len(ids))
		s.mu.RLock()
		if s.version != version {
			s.mu.RUnlock()
			continue
		}
		for index, id := range ids {
			association := s.associations[id]
			requests[index] = ProjectSourceAuthorityRequest{Subject: association.Subject, SourceRefs: append([]STRIDEReference(nil), association.SourceRefs...)}
		}
		s.mu.RUnlock()
		err := batch.WithCurrentProjectSources(authority, requests, func(snapshots []ProjectSourceAuthoritySnapshot) error {
			if len(snapshots) != len(requests) {
				return ErrProjectAuthorityDenied
			}
			for _, snapshot := range snapshots {
				if snapshot.Validate() != nil || !authority.At.Before(snapshot.ExpiresAt) || !audienceAllowsProjectSource(snapshot.Audience, authority) {
					return ErrProjectAuthorityDenied
				}
			}
			return s.withCurrent(authority, func() error {
				s.mu.RLock()
				defer s.mu.RUnlock()
				if s.version != version {
					return errProjectSnapshotDrift
				}
				project, exists := s.projects[projectID]
				if !exists || project.OrganizationID != authority.Organization.Header.ID || !projectVisibleTo(project, authority) {
					return ErrProjectAuthorityNotFound
				}
				associations := make([]ProjectAssociation, 0, len(ids))
				for index, id := range ids {
					association, exists := s.associations[id]
					if !exists || association.Project.ID != projectID {
						return errProjectSnapshotDrift
					}
					if associationMatchesSource(association, snapshots[index]) {
						associations = append(associations, cloneProjectAssociation(association))
					}
				}
				return effect(cloneProject(project), associations)
			})
		})
		if errors.Is(err, errProjectSnapshotDrift) {
			continue
		}
		return err
	}
	return ErrProjectAuthorityConflict
}

func (s *ProjectAuthorityService) requireCurrent(authority ProjectAuthorityContext) error {
	if authority.Validate() != nil || s.fence == nil {
		return ErrProjectAuthorityDenied
	}
	return s.withCurrent(authority, func() error { return nil })
}

func (s *ProjectAuthorityService) withCurrent(authority ProjectAuthorityContext, effect func() error) error {
	if s == nil || s.fence == nil || authority.Validate() != nil || effect == nil {
		return ErrProjectAuthorityDenied
	}
	return s.fence.WithCurrentProjectAuthority(authority, effect)
}

func (s *ProjectAuthorityService) activeThreadBoundLocked(threadID string) bool {
	for _, binding := range s.bindings {
		if binding.ThreadID == threadID && binding.State == "active" {
			return true
		}
	}
	return false
}

func (s *ProjectAuthorityService) idempotentLocked(organizationID, operation, key, fingerprint string) (error, bool) {
	if prior, ok := s.idempotency[projectIdempotencyKey(organizationID, operation, key)]; ok {
		if prior == fingerprint {
			return nil, true
		}
		return ErrProjectAuthorityConflict, true
	}
	return nil, false
}

func projectIdempotencyKey(organizationID, operation, digest string) string {
	return organizationID + "\x00" + operation + "\x00" + digest
}

func (s *ProjectAuthorityService) withCurrentSource(authority ProjectAuthorityContext, subject STRIDEReference, refs []STRIDEReference, effect func(ProjectSourceAuthoritySnapshot) error) error {
	if s == nil || s.sources == nil || effect == nil {
		return ErrProjectAuthorityDenied
	}
	return s.sources.WithCurrentProjectSource(authority, subject, refs, func(snapshot ProjectSourceAuthoritySnapshot) error {
		if snapshot.Validate() != nil || !authority.At.Before(snapshot.ExpiresAt) || snapshot.Subject != subject || !sameSTRIDEReferences(snapshot.SourceRefs, refs) || !audienceAllowsProjectSource(snapshot.Audience, authority) {
			return ErrProjectAuthorityDenied
		}
		return effect(snapshot)
	})
}

func associationMatchesSource(association ProjectAssociation, source ProjectSourceAuthoritySnapshot) bool {
	// Receipt IDs are point-in-time authorization evidence. Current canonical
	// source identity/ACL generations may be renewed under a new receipt without
	// rewriting the historical association edge.
	return association.Subject == source.Subject && sameSTRIDEReferences(association.SourceRefs, source.SourceRefs) &&
		association.EvidenceCoverageDigest == source.EvidenceCoverageDigest && association.SourceAudience.Visibility == source.Audience.Visibility &&
		sameStrings(association.SourceAudience.Principals, source.Audience.Principals) && association.SourceACLRevision == source.ACLRevision &&
		association.SourceACLDigest == source.ACLDigest && association.ConsentRevision == source.ConsentRevision && association.PurgeGeneration == source.PurgeGeneration
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func projectControllerMatches(project Project, membership OrganizationMembership) bool {
	for _, controller := range project.ControllerMemberships {
		if controller.ContractType == STRIDEContractOrganizationMembership && controller.ID == membership.Header.ID &&
			controller.Revision == membership.Header.Revision && controller.Digest == membership.Header.ContentDigest {
			return true
		}
	}
	return false
}

func projectVisibleTo(project Project, authority ProjectAuthorityContext) bool {
	for _, principal := range project.Audience.Principals {
		if principal == authority.Person.Header.ID || principal == authority.Membership.Header.ID {
			return true
		}
	}
	return projectControllerMatches(project, authority.Membership)
}

func audienceAllowsProjectSource(audience STRIDEAudience, authority ProjectAuthorityContext) bool {
	for _, principal := range audience.Principals {
		if principal == authority.Person.Header.ID || principal == authority.Membership.Header.ID || principal == authority.Organization.Header.ID {
			return true
		}
	}
	return false
}

func legalProjectLifecycle(current, next string) bool {
	return current == next || current == "draft" && (next == "active" || next == "archived") || current == "active" && next == "archived"
}

func legalAssociationTransition(current, next ProjectAssociation, action string, now time.Time) bool {
	wantState := map[string]string{"confirm": "confirmed", "remove": "removed", "expire": "expired", "revoke": "revoked"}[action]
	if wantState == "" || next.State != wantState || next.Replacement != nil {
		return false
	}
	switch action {
	case "confirm":
		return current.State == "proposed" && current.ExpiresAt != nil && now.Before(*current.ExpiresAt)
	case "remove", "revoke":
		return current.State == "proposed" || current.State == "confirmed"
	case "expire":
		return current.State == "proposed" && current.ExpiresAt != nil && !now.Before(*current.ExpiresAt)
	default:
		return false
	}
}

func associationAuthorityMatches(association ProjectAssociation, authority ProjectAuthorityContext) bool {
	return association.Header.TenantID == authority.Organization.Header.ID && association.ActorPersonID == authority.Person.Header.ID &&
		association.ActorMembershipID == authority.Membership.Header.ID && association.ActorMembershipRevision == authority.Membership.Header.Revision &&
		association.SessionSubjectDigest == authority.ActiveSession.SessionSubjectDigest && association.SessionRevision == authority.ActiveSession.SessionRevision &&
		association.AuthorityGeneration == authority.Generation
}

func eventAuthorityMatches(event ProjectAssociationEvent, authority ProjectAuthorityContext) bool {
	return event.Header.TenantID == authority.Organization.Header.ID && event.ActorPersonID == authority.Person.Header.ID &&
		event.ActorMembershipID == authority.Membership.Header.ID && event.ActorMembershipRevision == authority.Membership.Header.Revision &&
		event.SessionSubjectDigest == authority.ActiveSession.SessionSubjectDigest && event.SessionRevision == authority.ActiveSession.SessionRevision &&
		event.AuthorityGeneration == authority.Generation
}

func projectAssociationRef(value ProjectAssociation) STRIDEReference {
	return STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: value.Header.ID, Revision: value.Header.Revision, Digest: value.Header.ContentDigest}
}

func cloneProject(value Project) Project {
	value.Aliases = append([]string(nil), value.Aliases...)
	value.ControllerMemberships = append([]STRIDEReference(nil), value.ControllerMemberships...)
	value.Audience.Principals = append([]string(nil), value.Audience.Principals...)
	if value.Supersedes != nil {
		copy := *value.Supersedes
		value.Supersedes = &copy
	}
	return value
}

func cloneProjectAssociation(value ProjectAssociation) ProjectAssociation {
	value.SourceRefs = append([]STRIDEReference(nil), value.SourceRefs...)
	value.SourceAudience.Principals = append([]string(nil), value.SourceAudience.Principals...)
	if value.ExpiresAt != nil {
		copy := *value.ExpiresAt
		value.ExpiresAt = &copy
	}
	if value.Supersedes != nil {
		copy := *value.Supersedes
		value.Supersedes = &copy
	}
	if value.Replacement != nil {
		copy := *value.Replacement
		value.Replacement = &copy
	}
	return value
}

func sameSTRIDEReferences(left, right []STRIDEReference) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
