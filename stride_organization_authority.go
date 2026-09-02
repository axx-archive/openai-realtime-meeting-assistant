package main

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrOrganizationAuthorityInvalid  = errors.New("invalid organization authority mutation")
	ErrOrganizationAuthorityDenied   = errors.New("organization authority denied")
	ErrOrganizationAuthorityNotFound = errors.New("organization authority object not found")
	ErrOrganizationAuthorityConflict = errors.New("organization authority revision conflict")
	ErrOrganizationCapacity          = errors.New("person already has three active organizations")
	ErrOrganizationFinalOwner        = errors.New("active organization requires an active owner")
)

// OrganizationAuthorityService is the deterministic W1 policy adapter. It is
// intentionally route-free and provider-free. The PostgreSQL adapter must
// preserve the same serializable mutation boundary before any default-off
// reader or writer can activate.
type OrganizationAuthorityService struct {
	mu             sync.RWMutex
	persons        map[string]PersonPrincipal
	accountPersons map[string]string
	profiles       map[string]PersonProfile
	memberProfiles map[string]OrganizationMemberProfile
	organizations  map[string]Organization
	memberships    map[string]OrganizationMembership
	joinRequests   map[string]OrganizationJoinRequest
	sessions       map[string]ActiveOrganizationSession
	audit          map[string][]OrganizationAuditEvent
	idempotency    map[string]string
}

func NewOrganizationAuthorityService() *OrganizationAuthorityService {
	return &OrganizationAuthorityService{
		persons:        make(map[string]PersonPrincipal),
		accountPersons: make(map[string]string),
		profiles:       make(map[string]PersonProfile),
		memberProfiles: make(map[string]OrganizationMemberProfile),
		organizations:  make(map[string]Organization),
		memberships:    make(map[string]OrganizationMembership),
		joinRequests:   make(map[string]OrganizationJoinRequest),
		sessions:       make(map[string]ActiveOrganizationSession),
		audit:          make(map[string][]OrganizationAuditEvent),
		idempotency:    make(map[string]string),
	}
}

func (s *OrganizationAuthorityService) RegisterPerson(person PersonPrincipal) error {
	if s == nil || person.Validate() != nil || person.Status != "active" {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existingID := s.accountPersons[person.AccountSubjectDigest]; existingID != "" && existingID != person.Header.ID {
		return ErrOrganizationAuthorityConflict
	}
	if current, ok := s.persons[person.Header.ID]; ok {
		if current.Header.Revision == person.Header.Revision && current.Header.ContentDigest == person.Header.ContentDigest {
			return nil
		}
		if person.Header.Revision <= current.Header.Revision || current.AccountSubjectDigest != person.AccountSubjectDigest {
			return ErrOrganizationAuthorityConflict
		}
	}
	s.persons[person.Header.ID] = clonePersonPrincipal(person)
	s.accountPersons[person.AccountSubjectDigest] = person.Header.ID
	return nil
}

func (s *OrganizationAuthorityService) ResolvePersonByAccountDigest(accountSubjectDigest string) (PersonPrincipal, error) {
	if s == nil || !isHexDigest(accountSubjectDigest) {
		return PersonPrincipal{}, ErrOrganizationAuthorityInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	personID := s.accountPersons[accountSubjectDigest]
	person, ok := s.persons[personID]
	if !ok || person.Status != "active" {
		return PersonPrincipal{}, ErrOrganizationAuthorityNotFound
	}
	return clonePersonPrincipal(person), nil
}

func (s *OrganizationAuthorityService) PutSelfProfile(actorPersonID string, expectedRevision int64, profile PersonProfile) error {
	if s == nil || !strideIdentifier(actorPersonID) || profile.Validate() != nil || profile.PersonID != actorPersonID || profile.Status != "active" {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.persons[actorPersonID]; !ok {
		return ErrOrganizationAuthorityNotFound
	}
	current, found := s.profiles[actorPersonID]
	if (!found && expectedRevision != 0) || (found && (current.Header.Revision != expectedRevision || profile.Header.Revision != expectedRevision+1)) || (!found && profile.Header.Revision != 1) {
		return ErrOrganizationAuthorityConflict
	}
	s.profiles[actorPersonID] = clonePersonProfile(profile)
	return nil
}

func (s *OrganizationAuthorityService) PutOrganizationMemberProfile(actorMembershipID string, actorMembershipRevision, expectedRevision int64, profile OrganizationMemberProfile) error {
	if s == nil || profile.Validate() != nil || actorMembershipRevision < 1 || expectedRevision < 0 {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	actor, err := s.requireAdministratorLocked(actorMembershipID, actorMembershipRevision, profile.OrganizationID)
	if err != nil || actor.Header.ID != profile.UpdatedByMembershipID || actor.Header.Revision != profile.UpdatedByMembershipRevision {
		return ErrOrganizationAuthorityDenied
	}
	target, err := s.requireMembershipLocked(profile.MembershipID, profile.MembershipRevision, profile.OrganizationID)
	if err != nil || target.PersonID != profile.PersonID {
		return ErrOrganizationAuthorityDenied
	}
	current, found := s.memberProfiles[profile.MembershipID]
	if (!found && expectedRevision != 0) || (!found && profile.Header.Revision != 1) ||
		(found && (current.Header.Revision != expectedRevision || profile.Header.Revision != expectedRevision+1)) {
		return ErrOrganizationAuthorityConflict
	}
	s.memberProfiles[profile.MembershipID] = profile
	return nil
}

func (s *OrganizationAuthorityService) CreateOrganization(actorPersonID string, organization Organization, owner OrganizationMembership, event OrganizationAuditEvent) error {
	if s == nil || !strideIdentifier(actorPersonID) || organization.Validate() != nil || owner.Validate() != nil || event.Validate() != nil ||
		organization.CreatorPersonID != actorPersonID || owner.PersonID != actorPersonID || owner.OrganizationID != organization.Header.ID ||
		owner.Role != "owner" || owner.Status != "active" || owner.Header.Revision != 1 || event.Action != "create" ||
		event.OrganizationID != organization.Header.ID || event.ActorPersonID != actorPersonID || event.NewRevision != organization.Header.Revision {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result, replay := s.idempotentReplayLocked(event, organization.Header, owner.Header); replay {
		return result
	}
	if _, ok := s.persons[actorPersonID]; !ok {
		return ErrOrganizationAuthorityNotFound
	}
	if _, ok := s.organizations[organization.Header.ID]; ok || s.membershipExistsLocked(owner.Header.ID) {
		return ErrOrganizationAuthorityConflict
	}
	if s.activeMembershipCountLocked(actorPersonID) >= 3 {
		return ErrOrganizationCapacity
	}
	s.organizations[organization.Header.ID] = cloneOrganization(organization)
	s.memberships[owner.Header.ID] = cloneOrganizationMembership(owner)
	s.recordAuditLocked(event, organization.Header, owner.Header)
	return nil
}

func (s *OrganizationAuthorityService) RequestJoin(request OrganizationJoinRequest, event OrganizationAuditEvent) error {
	if s == nil || request.Validate() != nil || event.Validate() != nil || request.Status != "pending" || request.Header.Revision != 1 ||
		event.Action != "request" || event.OrganizationID != request.OrganizationID || event.ActorPersonID != request.PersonID || event.SubjectPersonID != request.PersonID || event.NewRevision != request.Header.Revision {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result, replay := s.idempotentReplayLocked(event, request.Header); replay {
		return result
	}
	organization, ok := s.organizations[request.OrganizationID]
	if !ok || organization.Status != "active" {
		return ErrOrganizationAuthorityNotFound
	}
	if _, ok := s.persons[request.PersonID]; !ok {
		return ErrOrganizationAuthorityNotFound
	}
	if s.activeMembershipForPersonOrganizationLocked(request.PersonID, request.OrganizationID) != nil || s.pendingJoinForPersonOrganizationLocked(request.PersonID, request.OrganizationID) != nil {
		return ErrOrganizationAuthorityConflict
	}
	s.joinRequests[request.Header.ID] = cloneOrganizationJoinRequest(request)
	s.recordAuditLocked(event, request.Header)
	return nil
}

func (s *OrganizationAuthorityService) DecideJoin(actorMembershipID string, actorMembershipRevision, expectedRequestRevision int64, decision OrganizationJoinRequest, approvedMembership *OrganizationMembership, event OrganizationAuditEvent) error {
	if s == nil || !strideIdentifier(actorMembershipID) || actorMembershipRevision < 1 || expectedRequestRevision < 1 || decision.Validate() != nil || event.Validate() != nil ||
		!oneOf(decision.Status, "approved", "denied") || decision.Header.Revision != expectedRequestRevision+1 ||
		decision.DecidedByMembershipID != actorMembershipID || event.Action != map[bool]string{true: "approve", false: "deny"}[decision.Status == "approved"] ||
		event.OrganizationID != decision.OrganizationID || event.ActorMembershipID != actorMembershipID || event.ActorMembershipRevision != actorMembershipRevision || event.SubjectPersonID != decision.PersonID || event.NewRevision != decision.Header.Revision {
		return ErrOrganizationAuthorityInvalid
	}
	if decision.Status == "approved" {
		if approvedMembership == nil || approvedMembership.Validate() != nil || approvedMembership.PersonID != decision.PersonID || approvedMembership.OrganizationID != decision.OrganizationID || approvedMembership.Role != "member" || approvedMembership.Status != "active" || approvedMembership.Header.Revision != 1 || approvedMembership.GrantedByMembershipID != actorMembershipID {
			return ErrOrganizationAuthorityInvalid
		}
	} else if approvedMembership != nil {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	results := []STRIDEContractHeader{decision.Header}
	if approvedMembership != nil {
		results = append(results, approvedMembership.Header)
	}
	if result, replay := s.idempotentReplayLocked(event, results...); replay {
		return result
	}
	actor, err := s.requireAdministratorLocked(actorMembershipID, actorMembershipRevision, decision.OrganizationID)
	if err != nil {
		return err
	}
	if actor.PersonID != event.ActorPersonID {
		return ErrOrganizationAuthorityDenied
	}
	current, ok := s.joinRequests[decision.Header.ID]
	if !ok {
		return ErrOrganizationAuthorityNotFound
	}
	if current.Header.Revision != expectedRequestRevision || current.Status != "pending" || current.PersonID != decision.PersonID || current.OrganizationID != decision.OrganizationID {
		return ErrOrganizationAuthorityConflict
	}
	if decision.Status == "approved" {
		if s.activeMembershipCountLocked(decision.PersonID) >= 3 {
			return ErrOrganizationCapacity
		}
		if s.activeMembershipForPersonOrganizationLocked(decision.PersonID, decision.OrganizationID) != nil || s.membershipExistsLocked(approvedMembership.Header.ID) {
			return ErrOrganizationAuthorityConflict
		}
		s.memberships[approvedMembership.Header.ID] = cloneOrganizationMembership(*approvedMembership)
	}
	s.joinRequests[decision.Header.ID] = cloneOrganizationJoinRequest(decision)
	s.recordAuditLocked(event, results...)
	return nil
}

func (s *OrganizationAuthorityService) CloseJoinRequest(expectedRevision int64, next OrganizationJoinRequest, event OrganizationAuditEvent) error {
	if s == nil || expectedRevision < 1 || next.Validate() != nil || event.Validate() != nil ||
		!oneOf(next.Status, "cancelled", "expired") || next.Header.Revision != expectedRevision+1 ||
		event.Action != map[bool]string{true: "cancel", false: "expire"}[next.Status == "cancelled"] ||
		event.OrganizationID != next.OrganizationID || event.SubjectPersonID != next.PersonID || event.NewRevision != next.Header.Revision {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result, replay := s.idempotentReplayLocked(event, next.Header); replay {
		return result
	}
	current, ok := s.joinRequests[next.Header.ID]
	if !ok {
		return ErrOrganizationAuthorityNotFound
	}
	if current.Header.Revision != expectedRevision || current.Status != "pending" || current.PersonID != next.PersonID || current.OrganizationID != next.OrganizationID {
		return ErrOrganizationAuthorityConflict
	}
	if next.Status == "cancelled" && event.ActorPersonID != next.PersonID {
		return ErrOrganizationAuthorityDenied
	}
	if next.Status == "expired" && event.ActorPersonID == "" {
		return ErrOrganizationAuthorityInvalid
	}
	s.joinRequests[next.Header.ID] = cloneOrganizationJoinRequest(next)
	s.recordAuditLocked(event, next.Header)
	return nil
}

func (s *OrganizationAuthorityService) ChangeMembershipRole(actorMembershipID string, actorRevision, expectedRevision int64, next OrganizationMembership, event OrganizationAuditEvent) error {
	if s == nil || expectedRevision < 1 || next.Validate() != nil || event.Validate() != nil || next.Status != "active" ||
		next.Header.Revision != expectedRevision+1 || event.Action != "role_change" || event.OrganizationID != next.OrganizationID ||
		event.SubjectPersonID != next.PersonID || event.ActorMembershipID != actorMembershipID || event.ActorMembershipRevision != actorRevision || event.NewRevision != next.Header.Revision {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result, replay := s.idempotentReplayLocked(event, next.Header); replay {
		return result
	}
	actor, err := s.requireMembershipLocked(actorMembershipID, actorRevision, next.OrganizationID)
	if err != nil || actor.PersonID != event.ActorPersonID {
		return ErrOrganizationAuthorityDenied
	}
	current, ok := s.memberships[next.Header.ID]
	if !ok {
		return ErrOrganizationAuthorityNotFound
	}
	if current.Header.Revision != expectedRevision || current.Status != "active" || current.PersonID != next.PersonID || current.OrganizationID != next.OrganizationID || current.Role == next.Role {
		return ErrOrganizationAuthorityConflict
	}
	// Owner transitions use the atomic transfer operation; role management may
	// only move member/admin under a current owner.
	if actor.Role != "owner" || current.Role == "owner" || next.Role == "owner" {
		return ErrOrganizationAuthorityDenied
	}
	s.memberships[next.Header.ID] = cloneOrganizationMembership(next)
	s.recordAuditLocked(event, next.Header)
	return nil
}

func (s *OrganizationAuthorityService) TransferOwnership(actorMembershipID string, actorRevision int64, priorOwnerNext, newOwnerNext OrganizationMembership, event OrganizationAuditEvent) error {
	if s == nil || priorOwnerNext.Validate() != nil || newOwnerNext.Validate() != nil || event.Validate() != nil || event.Action != "transfer" ||
		priorOwnerNext.OrganizationID != newOwnerNext.OrganizationID || priorOwnerNext.Role == "owner" || newOwnerNext.Role != "owner" ||
		priorOwnerNext.Status != "active" || newOwnerNext.Status != "active" || event.OrganizationID != newOwnerNext.OrganizationID ||
		event.ActorMembershipID != actorMembershipID || event.ActorMembershipRevision != actorRevision || event.SubjectPersonID != newOwnerNext.PersonID ||
		event.NewRevision != newOwnerNext.Header.Revision {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result, replay := s.idempotentReplayLocked(event, priorOwnerNext.Header, newOwnerNext.Header); replay {
		return result
	}
	actor, err := s.requireMembershipLocked(actorMembershipID, actorRevision, newOwnerNext.OrganizationID)
	if err != nil || actor.Role != "owner" || actor.PersonID != event.ActorPersonID || actor.Header.ID != priorOwnerNext.Header.ID {
		return ErrOrganizationAuthorityDenied
	}
	prior := s.memberships[priorOwnerNext.Header.ID]
	target := s.memberships[newOwnerNext.Header.ID]
	if prior.Header.ID == "" || target.Header.ID == "" || prior.OrganizationID != newOwnerNext.OrganizationID || target.OrganizationID != newOwnerNext.OrganizationID ||
		prior.Role != "owner" || target.Status != "active" ||
		prior.Header.Revision+1 != priorOwnerNext.Header.Revision || target.Header.Revision+1 != newOwnerNext.Header.Revision ||
		prior.PersonID != priorOwnerNext.PersonID || target.PersonID != newOwnerNext.PersonID {
		return ErrOrganizationAuthorityConflict
	}
	s.memberships[priorOwnerNext.Header.ID] = cloneOrganizationMembership(priorOwnerNext)
	s.memberships[newOwnerNext.Header.ID] = cloneOrganizationMembership(newOwnerNext)
	s.recordAuditLocked(event, priorOwnerNext.Header, newOwnerNext.Header)
	return nil
}

func (s *OrganizationAuthorityService) EndMembership(actorMembershipID string, actorRevision int64, expectedRevision int64, next OrganizationMembership, event OrganizationAuditEvent) error {
	if s == nil || next.Validate() != nil || event.Validate() != nil || expectedRevision < 1 || !oneOf(next.Status, "departed", "revoked") ||
		next.Header.Revision != expectedRevision+1 || event.OrganizationID != next.OrganizationID || event.SubjectPersonID != next.PersonID || event.NewRevision != next.Header.Revision ||
		(next.Status == "departed" && event.Action != "leave") || (next.Status == "revoked" && event.Action != "revoke") {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result, replay := s.idempotentReplayLocked(event, next.Header); replay {
		return result
	}
	current, ok := s.memberships[next.Header.ID]
	if !ok {
		return ErrOrganizationAuthorityNotFound
	}
	if current.Header.Revision != expectedRevision || current.Status != "active" || current.PersonID != next.PersonID || current.OrganizationID != next.OrganizationID || current.Role != next.Role {
		return ErrOrganizationAuthorityConflict
	}
	actor, err := s.requireMembershipLocked(actorMembershipID, actorRevision, next.OrganizationID)
	if err != nil || actor.PersonID != event.ActorPersonID {
		return ErrOrganizationAuthorityDenied
	}
	if next.Status == "departed" {
		if actor.Header.ID != current.Header.ID {
			return ErrOrganizationAuthorityDenied
		}
	} else if actor.Role != "owner" && actor.Role != "admin" {
		return ErrOrganizationAuthorityDenied
	}
	if current.Role == "owner" && s.activeOwnerCountLocked(current.OrganizationID) <= 1 {
		return ErrOrganizationFinalOwner
	}
	s.memberships[next.Header.ID] = cloneOrganizationMembership(next)
	for digest, session := range s.sessions {
		if session.PersonID == next.PersonID && session.OrganizationID == next.OrganizationID && session.MembershipID == next.Header.ID && session.MembershipRevision <= expectedRevision {
			delete(s.sessions, digest)
		}
	}
	s.recordAuditLocked(event, next.Header)
	return nil
}

func (s *OrganizationAuthorityService) BindActiveSession(expectedSessionRevision int64, session ActiveOrganizationSession, event OrganizationAuditEvent) error {
	if s == nil || session.Validate() != nil || event.Validate() != nil || event.Action != "switch" || event.OrganizationID != session.OrganizationID ||
		event.ActorPersonID != session.PersonID || event.ActorMembershipID != session.MembershipID || event.ActorMembershipRevision != session.MembershipRevision ||
		event.NewRevision != session.SessionRevision || session.SessionRevision != expectedSessionRevision+1 || session.Status != "active" {
		return ErrOrganizationAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result, replay := s.idempotentReplayLocked(event, session.Header); replay {
		return result
	}
	membership, err := s.requireMembershipLocked(session.MembershipID, session.MembershipRevision, session.OrganizationID)
	if err != nil {
		return err
	}
	if membership.PersonID != session.PersonID {
		return ErrOrganizationAuthorityDenied
	}
	current, found := s.sessions[session.SessionSubjectDigest]
	if (!found && expectedSessionRevision != 0) || (found && (current.SessionRevision != expectedSessionRevision || current.PersonID != session.PersonID)) {
		return ErrOrganizationAuthorityConflict
	}
	s.sessions[session.SessionSubjectDigest] = cloneActiveOrganizationSession(session)
	s.recordAuditLocked(event, session.Header)
	return nil
}

func (s *OrganizationAuthorityService) Membership(id string) (OrganizationMembership, error) {
	if s == nil {
		return OrganizationMembership{}, ErrOrganizationAuthorityNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	membership, ok := s.memberships[id]
	if !ok {
		return OrganizationMembership{}, ErrOrganizationAuthorityNotFound
	}
	return cloneOrganizationMembership(membership), nil
}

func (s *OrganizationAuthorityService) JoinRequest(id string) (OrganizationJoinRequest, error) {
	if s == nil {
		return OrganizationJoinRequest{}, ErrOrganizationAuthorityNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	request, ok := s.joinRequests[id]
	if !ok {
		return OrganizationJoinRequest{}, ErrOrganizationAuthorityNotFound
	}
	return cloneOrganizationJoinRequest(request), nil
}

func (s *OrganizationAuthorityService) ActiveMembershipCount(personID string) int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeMembershipCountLocked(personID)
}

// SingleActiveOrganizationName returns the name of the workspace's one active
// organization. The product ships as a single-organization workspace (Bonfire;
// creation is closed), so the shell chip needs a label even for a session that
// is not organization-bound yet. Returns "" when there is not exactly one.
func (s *OrganizationAuthorityService) SingleActiveOrganizationName() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	name := ""
	for _, organization := range s.organizations {
		if organization.Status != "active" {
			continue
		}
		if name != "" {
			return ""
		}
		name = organization.Name
	}
	return name
}

func (s *OrganizationAuthorityService) Audit(organizationID string) []OrganizationAuditEvent {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]OrganizationAuditEvent(nil), s.audit[organizationID]...)
}

func (s *OrganizationAuthorityService) requireAdministratorLocked(membershipID string, revision int64, organizationID string) (OrganizationMembership, error) {
	membership, err := s.requireMembershipLocked(membershipID, revision, organizationID)
	if err != nil {
		return OrganizationMembership{}, err
	}
	if membership.Role != "owner" && membership.Role != "admin" {
		return OrganizationMembership{}, ErrOrganizationAuthorityDenied
	}
	return membership, nil
}

func (s *OrganizationAuthorityService) requireMembershipLocked(membershipID string, revision int64, organizationID string) (OrganizationMembership, error) {
	membership, ok := s.memberships[membershipID]
	if !ok {
		return OrganizationMembership{}, ErrOrganizationAuthorityNotFound
	}
	if membership.Header.Revision != revision || membership.OrganizationID != organizationID || membership.Status != "active" {
		return OrganizationMembership{}, ErrOrganizationAuthorityDenied
	}
	return membership, nil
}

func (s *OrganizationAuthorityService) activeMembershipCountLocked(personID string) int {
	count := 0
	for _, membership := range s.memberships {
		if membership.PersonID == personID && membership.Status == "active" {
			count++
		}
	}
	return count
}

func (s *OrganizationAuthorityService) activeOwnerCountLocked(organizationID string) int {
	count := 0
	for _, membership := range s.memberships {
		if membership.OrganizationID == organizationID && membership.Status == "active" && membership.Role == "owner" {
			count++
		}
	}
	return count
}

func (s *OrganizationAuthorityService) activeMembershipForPersonOrganizationLocked(personID, organizationID string) *OrganizationMembership {
	for _, membership := range s.memberships {
		if membership.PersonID == personID && membership.OrganizationID == organizationID && membership.Status == "active" {
			copy := cloneOrganizationMembership(membership)
			return &copy
		}
	}
	return nil
}

func (s *OrganizationAuthorityService) pendingJoinForPersonOrganizationLocked(personID, organizationID string) *OrganizationJoinRequest {
	for _, request := range s.joinRequests {
		if request.PersonID == personID && request.OrganizationID == organizationID && request.Status == "pending" {
			copy := cloneOrganizationJoinRequest(request)
			return &copy
		}
	}
	return nil
}

func (s *OrganizationAuthorityService) membershipExistsLocked(id string) bool {
	_, ok := s.memberships[id]
	return ok
}

func (s *OrganizationAuthorityService) idempotentReplayLocked(event OrganizationAuditEvent, results ...STRIDEContractHeader) (error, bool) {
	key := event.ActorPersonID + "\x00" + event.Action + "\x00" + event.IdempotencyKeyDigest
	value := organizationAuthorityIdempotencyValue(event, results...)
	if existing, ok := s.idempotency[key]; ok {
		if existing == value {
			return nil, true
		}
		return ErrOrganizationAuthorityConflict, true
	}
	return nil, false
}

func (s *OrganizationAuthorityService) recordAuditLocked(event OrganizationAuditEvent, results ...STRIDEContractHeader) {
	key := event.ActorPersonID + "\x00" + event.Action + "\x00" + event.IdempotencyKeyDigest
	s.idempotency[key] = organizationAuthorityIdempotencyValue(event, results...)
	s.audit[event.OrganizationID] = append(s.audit[event.OrganizationID], event)
}

func organizationAuthorityIdempotencyValue(event OrganizationAuditEvent, results ...STRIDEContractHeader) string {
	value := event.Header.ContentDigest
	for _, result := range results {
		value += fmt.Sprintf("\x00%s\x00%s\x00%d\x00%s", result.ContractType, result.ID, result.Revision, result.ContentDigest)
	}
	return value
}

func clonePersonProfile(value PersonProfile) PersonProfile {
	value.WorkModes = append([]string(nil), value.WorkModes...)
	value.OpenTo = append([]string(nil), value.OpenTo...)
	value.VisibleOrganizationIDs = append([]string(nil), value.VisibleOrganizationIDs...)
	return value
}

func cloneOrganization(value Organization) Organization { return value }

func cloneOrganizationMembership(value OrganizationMembership) OrganizationMembership { return value }

func cloneOrganizationJoinRequest(value OrganizationJoinRequest) OrganizationJoinRequest {
	return value
}

func cloneActiveOrganizationSession(value ActiveOrganizationSession) ActiveOrganizationSession {
	return value
}
