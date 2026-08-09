package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// StrideE10ProductLiveRuntime is the concrete, provider-free W2 composition.
// It starts with every switch off and no imported authority. Registering its
// handler therefore exposes only authenticated, fail-closed 503/404 behavior;
// it cannot manufacture a person, organization, grant, or product row.
type StrideE10ProductLiveRuntime struct {
	mu             sync.RWMutex
	mutationMu     sync.Mutex
	features       map[STRIDEFeature]bool
	organization   *OrganizationAuthorityService
	contribution   *ContributionAuthorityService
	network        *NetworkAuthority
	actions        map[string]StrideE10LiveActionBinding
	actionUses     map[string]string
	joinCodes      map[string]string
	exports        map[string]strideE10ExportReceipt
	packages       map[string]strideE10ExportPackage
	portableStore  StrideE10PortableDeletionStore
	operationStore StrideE10ProductOperationStore
	purges         map[string]map[string]any
	idempotency    map[string]strideE10LiveIdempotency
	now            func() time.Time
	persistRuntime func(*StrideE10ProductLiveRuntime) error
}

type StrideE10LiveActionBinding struct {
	ID                    string
	Type                  string
	Surface               string
	PersonID              string
	OrganizationID        string
	ExpectedRevision      int64
	ExpiresAt             time.Time
	Target                STRIDEReference
	MembershipRevision    int64
	SessionRevision       int64
	TargetMembershipID    string
	Profile               *PersonProfile
	MemberProfile         *OrganizationMemberProfile
	Organization          *Organization
	OwnerMembership       *OrganizationMembership
	JoinRequest           *OrganizationJoinRequest
	JoinDecision          *OrganizationJoinRequest
	ApprovedMembership    *OrganizationMembership
	ActiveSession         *ActiveOrganizationSession
	SessionAccountDigest  string
	SessionGeneration     uint64
	Claim                 *ContributionClaim
	Approval              *FieldReleaseApproval
	Attestation           *ContributionAttestation
	Publication           *PublishedContributionClaim
	NetworkSearch         *NetworkSearchRequest
	ContactAdmission      *NetworkContactAdmission
	ContactActor          *STRIDEControllerRevision
	AcceptedChannelDigest string
	Decision              string
	Block                 *NetworkBlock
	ReplacementMembership *OrganizationMembership
	PriorOwnerNext        *OrganizationMembership
	NewOwnerNext          *OrganizationMembership
	AuditEvent            *OrganizationAuditEvent
	TalentAssertion       *TalentSearchCapabilityAssertion
	TalentGrant           *TalentSearchGrant
	ContributionAssertion *ContributionAuthorityAssertion
	CorrectedClaim        *ContributionClaim
	NetworkActor          *STRIDEControllerRevision
	NetworkProfile        *NetworkProfileProjection
	ExportReceipt         *strideE10ExportReceipt
	ExportBody            json.RawMessage
}

type strideE10ExportReceipt struct {
	ID            string
	PersonID      string
	Surface       string
	Revision      int64
	PackageDigest string
	ExpiresAt     time.Time
}

type strideE10ExportPackage struct {
	Receipt strideE10ExportReceipt
	Body    json.RawMessage
}

type strideE10LiveIdempotency struct {
	Fingerprint string
	Value       any
}

type strideE10LiveSessionTokenKey struct{}

func NewStrideE10ProductLiveRuntime(now func() time.Time) *StrideE10ProductLiveRuntime {
	return newStrideE10ProductLiveRuntimeWithStores(now, newStrideE10MemoryPortableDeletionStore(), newStrideE10MemoryOperationStore())
}

func newStrideE10ProductLiveRuntimeWithStore(now func() time.Time, portableStore StrideE10PortableDeletionStore) *StrideE10ProductLiveRuntime {
	return newStrideE10ProductLiveRuntimeWithStores(now, portableStore, newStrideE10MemoryOperationStore())
}

func newStrideE10ProductLiveRuntimeWithStores(now func() time.Time, portableStore StrideE10PortableDeletionStore, operationStore StrideE10ProductOperationStore) *StrideE10ProductLiveRuntime {
	contribution, _ := NewContributionAuthorityService(nil)
	if now == nil {
		now = time.Now
	}
	if portableStore == nil {
		portableStore = newStrideE10MemoryPortableDeletionStore()
	}
	if operationStore == nil {
		operationStore = newStrideE10MemoryOperationStore()
	}
	return &StrideE10ProductLiveRuntime{
		features: map[STRIDEFeature]bool{}, organization: NewOrganizationAuthorityService(),
		contribution: contribution, network: NewNetworkAuthority(now), actions: map[string]StrideE10LiveActionBinding{}, actionUses: map[string]string{}, joinCodes: map[string]string{},
		exports: map[string]strideE10ExportReceipt{}, packages: map[string]strideE10ExportPackage{}, portableStore: portableStore, operationStore: operationStore, purges: map[string]map[string]any{}, idempotency: map[string]strideE10LiveIdempotency{}, now: now,
	}
}

func (r *StrideE10ProductLiveRuntime) InstallJoinCodeAuthority(joinCode, organizationID string) error {
	if r == nil || !strideE10BoundedRequiredString(joinCode, 128) || !strideIdentifier(organizationID) {
		return ErrStrideE10Invalid
	}
	r.organization.mu.RLock()
	organization := r.organization.organizations[organizationID]
	r.organization.mu.RUnlock()
	if organization.Header.ID == "" || organization.Status != "active" {
		return ErrStrideE10NotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.joinCodes[sha256Hex([]byte(joinCode))] = organizationID
	return nil
}

func (r *StrideE10ProductLiveRuntime) Enabled(feature STRIDEFeature) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.features[feature]
}

// setFeatureForTest is deliberately package-private. Production registration
// has no environment/string switch that can bypass the receipted activation
// work still frozen outside this slice.
func (r *StrideE10ProductLiveRuntime) setFeatureForTest(feature STRIDEFeature, enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.features[feature] = enabled
}

func (r *StrideE10ProductLiveRuntime) BindAction(binding StrideE10LiveActionBinding) error {
	spec, mobileAction := strideE10MobileActions[binding.Type]
	requiresOrganization := mobileAction && spec.requireOrg || strideE10DirectBindingRequiresOrganization(binding.Type)
	if r == nil || !strideIdentifier(binding.ID) || binding.ExpectedRevision < 1 || !strideIdentifier(binding.PersonID) ||
		!validStrideE10LiveAction(binding.Type, binding.Surface) || binding.ExpiresAt.IsZero() || !r.now().Before(binding.ExpiresAt) ||
		(binding.Target.ID != "" && (binding.Target.Validate() != nil || binding.Target.Revision != binding.ExpectedRevision)) ||
		(requiresOrganization && (!strideIdentifier(binding.OrganizationID) || binding.MembershipRevision < 1 || binding.SessionRevision < 1)) {
		return ErrStrideE10Invalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if prior, ok := r.actions[binding.ID]; ok {
		if !reflect.DeepEqual(prior, binding) {
			return ErrStrideE10Conflict
		}
		return nil
	}
	r.actions[binding.ID] = cloneStrideE10LiveActionBinding(binding)
	return nil
}

func strideE10DirectBindingRequiresOrganization(action string) bool {
	return containsSTRIDEString([]string{"organization-member-profile-update", "organization-member-revoke", "organization-request-expire"}, action)
}

func validStrideE10LiveAction(action, surface string) bool {
	_, ok := strideE10MobileActions[action]
	if ok {
		return strideE10MobileActionSurfaces[action] == surface
	}
	return strideE10DirectBindingSurfaces[action] == surface
}

var strideE10DirectBindingSurfaces = map[string]string{
	"organization-member-profile-update": "organization-people",
	"organization-member-revoke":         "organization-people", "organization-request-expire": "organization-requests", "organization-request-cancel": "organization-requests",
	"contribution-named-party-decision": "contribution-approvals", "contribution-attestation-revoke": "contribution-approvals", "network-profile-off": "network-preview",
}

func cloneStrideE10LiveActionBinding(value StrideE10LiveActionBinding) StrideE10LiveActionBinding {
	result := value
	if value.ReplacementMembership != nil {
		clone := cloneOrganizationMembership(*value.ReplacementMembership)
		result.ReplacementMembership = &clone
	}
	if value.Profile != nil {
		clone := clonePersonProfile(*value.Profile)
		result.Profile = &clone
	}
	if value.MemberProfile != nil {
		clone := cloneContract(*value.MemberProfile)
		result.MemberProfile = &clone
	}
	if value.Organization != nil {
		clone := cloneOrganization(*value.Organization)
		result.Organization = &clone
	}
	if value.OwnerMembership != nil {
		clone := cloneOrganizationMembership(*value.OwnerMembership)
		result.OwnerMembership = &clone
	}
	if value.JoinRequest != nil {
		clone := cloneOrganizationJoinRequest(*value.JoinRequest)
		result.JoinRequest = &clone
	}
	if value.JoinDecision != nil {
		clone := cloneOrganizationJoinRequest(*value.JoinDecision)
		result.JoinDecision = &clone
	}
	if value.ApprovedMembership != nil {
		clone := cloneOrganizationMembership(*value.ApprovedMembership)
		result.ApprovedMembership = &clone
	}
	if value.ActiveSession != nil {
		clone := *value.ActiveSession
		result.ActiveSession = &clone
	}
	if value.Claim != nil {
		clone := cloneStrideE10ContributionClaim(*value.Claim)
		result.Claim = &clone
	}
	if value.Approval != nil {
		clone := cloneStrideE10JSON(*value.Approval)
		result.Approval = &clone
	}
	if value.Attestation != nil {
		clone := cloneStrideE10JSON(*value.Attestation)
		result.Attestation = &clone
	}
	if value.Publication != nil {
		clone := cloneStrideE10JSON(*value.Publication)
		result.Publication = &clone
	}
	if value.NetworkSearch != nil {
		clone := *value.NetworkSearch
		clone.StructuredFilters = append([]NetworkSearchFilter(nil), value.NetworkSearch.StructuredFilters...)
		result.NetworkSearch = &clone
	}
	if value.ContactAdmission != nil {
		clone := *value.ContactAdmission
		result.ContactAdmission = &clone
	}
	if value.ContactActor != nil {
		clone := *value.ContactActor
		result.ContactActor = &clone
	}
	if value.Block != nil {
		clone := cloneStrideE10JSON(*value.Block)
		result.Block = &clone
	}
	if value.PriorOwnerNext != nil {
		clone := cloneOrganizationMembership(*value.PriorOwnerNext)
		result.PriorOwnerNext = &clone
	}
	if value.NewOwnerNext != nil {
		clone := cloneOrganizationMembership(*value.NewOwnerNext)
		result.NewOwnerNext = &clone
	}
	if value.AuditEvent != nil {
		clone := *value.AuditEvent
		result.AuditEvent = &clone
	}
	if value.TalentAssertion != nil {
		clone := *value.TalentAssertion
		result.TalentAssertion = &clone
	}
	if value.TalentGrant != nil {
		clone := cloneTalentSearchGrant(*value.TalentGrant)
		result.TalentGrant = &clone
	}
	if value.ContributionAssertion != nil {
		clone := *value.ContributionAssertion
		result.ContributionAssertion = &clone
	}
	if value.CorrectedClaim != nil {
		clone := cloneStrideE10ContributionClaim(*value.CorrectedClaim)
		result.CorrectedClaim = &clone
	}
	if value.NetworkActor != nil {
		clone := *value.NetworkActor
		result.NetworkActor = &clone
	}
	if value.NetworkProfile != nil {
		clone := cloneNetworkProjection(*value.NetworkProfile)
		result.NetworkProfile = &clone
	}
	if value.ExportReceipt != nil {
		clone := *value.ExportReceipt
		result.ExportReceipt = &clone
	}
	result.ExportBody = append(json.RawMessage(nil), value.ExportBody...)
	return result
}

func cloneStrideE10ContributionClaim(value ContributionClaim) ContributionClaim {
	result := value
	result.SourceRefs = append([]STRIDEReference(nil), value.SourceRefs...)
	if value.SubjectReview != nil {
		clone := *value.SubjectReview
		result.SubjectReview = &clone
	}
	if value.OrganizationReview != nil {
		clone := *value.OrganizationReview
		result.OrganizationReview = &clone
	}
	if value.Supersedes != nil {
		clone := *value.Supersedes
		result.Supersedes = &clone
	}
	return result
}

func strideE10ExpectedApprovalDecision(current FieldReleaseApproval, decision string, controller STRIDEControllerRevision, at time.Time) *FieldReleaseApproval {
	prior := refForHeader(current.Header)
	current.Header = nextAuthorityHeader(current.Header, decision, at)
	current.State, current.Controller, current.StateChangedAt, current.Supersedes = decision, controller, at, &prior
	if decision == "approved" {
		approvedAt := at
		current.ApprovedAt = &approvedAt
	} else {
		current.ApprovedAt = nil
	}
	return &current
}

func cloneStrideE10JSON[T any](value T) T {
	raw, _ := json.Marshal(value)
	var result T
	_ = json.Unmarshal(raw, &result)
	return result
}

func (r *StrideE10ProductLiveRuntime) ResolvePrincipal(request *http.Request) (StrideE10ProductPrincipal, error) {
	if r == nil || request == nil {
		return StrideE10ProductPrincipal{}, ErrStrideE10Denied
	}
	token := sessionTokenFromRequest(request)
	if token == "" {
		return StrideE10ProductPrincipal{}, ErrStrideE10Denied
	}
	*request = *request.WithContext(context.WithValue(request.Context(), strideE10LiveSessionTokenKey{}, token))
	record, ok := userSessionStore().lookupMemberRecordByHash(hashResetToken(token), r.now().UTC())
	if !ok || !strideIdentifier(record.PersonID) {
		return StrideE10ProductPrincipal{}, ErrStrideE10Denied
	}
	if record.ActiveOrganizationID == "" && record.OrganizationMembershipID == "" && record.OrganizationMembershipRev == 0 && record.ActiveOrganizationSessionRev == 0 {
		return StrideE10ProductPrincipal{PersonID: record.PersonID}, nil
	}
	if !strideIdentifier(record.ActiveOrganizationID) || !strideIdentifier(record.OrganizationMembershipID) || record.OrganizationMembershipRev < 1 || record.ActiveOrganizationSessionRev < 1 {
		return StrideE10ProductPrincipal{}, ErrStrideE10Denied
	}
	membership, err := r.organization.Membership(record.OrganizationMembershipID)
	if err != nil || membership.Status != "active" || membership.PersonID != record.PersonID || membership.OrganizationID != record.ActiveOrganizationID || membership.Header.Revision != record.OrganizationMembershipRev {
		return StrideE10ProductPrincipal{}, ErrStrideE10Denied
	}
	return StrideE10ProductPrincipal{PersonID: record.PersonID, ActiveOrganizationID: record.ActiveOrganizationID, OrganizationMembershipID: record.OrganizationMembershipID, OrganizationMembershipRev: record.OrganizationMembershipRev, ActiveOrganizationSessionRev: record.ActiveOrganizationSessionRev}, nil
}

func (r *StrideE10ProductLiveRuntime) Execute(ctx context.Context, principal StrideE10ProductPrincipal, command StrideE10ProductCommand) (any, bool, error) {
	if r == nil || strings.TrimSpace(principal.PersonID) == "" {
		return nil, false, ErrStrideE10Denied
	}
	if command.Method == http.MethodGet && (command.Operation == "work_record.export_download" || command.Operation == "network.profile_export_download") {
		if _, deleted := r.portableStore.Load(principal.PersonID); deleted {
			return nil, false, ErrStrideE10NotFound
		}
		r.mu.RLock()
		pkg, ok := r.packages[command.ResourceID]
		r.mu.RUnlock()
		if !ok || pkg.Receipt.PersonID != principal.PersonID || !r.now().Before(pkg.Receipt.ExpiresAt) {
			return nil, false, ErrStrideE10NotFound
		}
		var contents any
		if json.Unmarshal(pkg.Body, &contents) != nil {
			return nil, false, ErrStrideE10Invalid
		}
		return map[string]any{"receiptId": pkg.Receipt.ID, "packageDigest": pkg.Receipt.PackageDigest, "expiresAt": pkg.Receipt.ExpiresAt.Format(time.RFC3339), "contents": contents}, false, nil
	}
	if command.Operation == "network.preview" && command.Method == http.MethodPost {
		value, err := r.project(principal, "network-preview")
		if err == nil && r.persistRuntime != nil {
			err = r.persistRuntime(r)
		}
		return value, false, err
	}
	if command.Method == http.MethodGet {
		surface := command.ResourceID
		if _, ok := strideE10MobileSurfaces[surface]; !ok {
			surface = strideE10SurfaceForOperation(command.Operation)
		}
		if surface == "" {
			return nil, false, ErrStrideE10NotFound
		}
		value, err := r.projectTarget(principal, surface, command.TargetID)
		if err == nil && r.persistRuntime != nil {
			err = r.persistRuntime(r)
		}
		return value, false, err
	}
	if !strings.HasPrefix(command.Path, "/api/stride/v1/mobile/actions/") {
		var direct struct {
			ActionID         string         `json:"actionId"`
			ExpectedRevision int64          `json:"expectedRevision"`
			Values           map[string]any `json:"values"`
		}
		var keys map[string]json.RawMessage
		if json.Unmarshal(command.Body, &keys) != nil || len(keys) != 3 || keys["actionId"] == nil || keys["expectedRevision"] == nil || keys["values"] == nil ||
			json.Unmarshal(command.Body, &direct) != nil || !strideIdentifier(direct.ActionID) || direct.ExpectedRevision != command.ExpectedRevision || direct.Values == nil {
			return nil, false, ErrStrideE10Invalid
		}
		r.mu.RLock()
		directBinding, ok := r.actions[direct.ActionID]
		r.mu.RUnlock()
		if !ok || !strideE10DirectOperationAllows(command.Operation, directBinding.Type) || !strideE10ValidMobileActionValues(directBinding.Type, direct.Values) ||
			(command.ResourceID != "" && directBinding.Target.ID != "" && command.ResourceID != directBinding.Target.ID) ||
			(command.MembershipID != "" && directBinding.Target.ID != "" && command.MembershipID != directBinding.Target.ID) {
			return nil, false, ErrStrideE10NotFound
		}
		command.ResourceID = direct.ActionID
		command.Body, _ = json.Marshal(map[string]any{"action": directBinding.Type, "surface": directBinding.Surface, "expectedRevision": direct.ExpectedRevision, "values": direct.Values})
	}
	var envelope struct {
		Action           string         `json:"action"`
		Surface          string         `json:"surface"`
		ExpectedRevision int64          `json:"expectedRevision"`
		Values           map[string]any `json:"values"`
	}
	if json.Unmarshal(command.Body, &envelope) != nil || envelope.ExpectedRevision != command.ExpectedRevision || envelope.Surface == "" || envelope.Action == "" {
		return nil, false, ErrStrideE10Invalid
	}
	if _, mobileAction := strideE10MobileActions[envelope.Action]; mobileAction && !strideE10ValidMobileActionValues(envelope.Action, envelope.Values) {
		return nil, false, ErrStrideE10Invalid
	}
	binding, ok := r.lookupLiveAction(principal.PersonID, command.ResourceID)
	if !ok || !r.now().Before(binding.ExpiresAt) || binding.ID != command.ResourceID || binding.Type != envelope.Action || binding.Surface != envelope.Surface || binding.PersonID != principal.PersonID || binding.ExpectedRevision != command.ExpectedRevision || binding.OrganizationID != "" && binding.OrganizationID != command.OrganizationID ||
		(binding.MembershipRevision != 0 && binding.MembershipRevision != principal.OrganizationMembershipRev) || (binding.SessionRevision != 0 && binding.SessionRevision != principal.ActiveOrganizationSessionRev) {
		return nil, false, ErrStrideE10NotFound
	}
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	operationKey := strideE10ProductOperationKey(principal.PersonID, command.ResourceID, command.IdempotencyKey)
	operation, recovered, err := r.operationStore.Load(operationKey)
	if err != nil {
		return nil, false, ErrStrideE10Invalid
	}
	if recovered {
		binding = cloneStrideE10LiveActionBinding(operation.Binding)
	} else {
		binding, err = r.hydrateLiveActionBinding(ctx, principal, command, binding, r.now().UTC())
		if err != nil {
			return nil, false, err
		}
	}
	bindingJSON, _ := json.Marshal(binding)
	fingerprint := sha256Hex(append(append([]byte(nil), command.Body...), bindingJSON...))
	if recovered {
		if operation.Fingerprint != fingerprint {
			return nil, true, ErrStrideE10Conflict
		}
		if strideE10CompoundContributionAction(binding.Type) {
			if err := r.repairBoundContributionNetworkEffect(binding); err != nil {
				return nil, true, strideE10LiveError(err)
			}
			if !r.boundContributionControllerCurrent(binding) {
				return nil, true, ErrStrideE10NotFound
			}
		}
		if operation.State == strideE10OperationCompleted {
			var value any
			if json.Unmarshal(operation.Response, &value) != nil {
				return nil, true, ErrStrideE10Invalid
			}
			return value, true, nil
		}
		if operation.State == strideE10OperationCommitted {
			value, projectErr := r.project(principal, binding.Surface)
			if projectErr != nil {
				return nil, true, projectErr
			}
			if r.persistRuntime != nil && r.persistRuntime(r) != nil {
				return nil, true, ErrStrideE10Invalid
			}
			if r.operationStore.Complete(operationKey, fingerprint, value, r.now().UTC()) != nil {
				return nil, true, ErrStrideE10Invalid
			}
			return value, true, nil
		}
	} else {
		operation = StrideE10ProductOperationRecord{Key: operationKey, PersonID: principal.PersonID, ActionID: binding.ID, IdempotencyKeyDigest: sha256Hex([]byte(command.IdempotencyKey)), Fingerprint: fingerprint, State: strideE10OperationPrepared, Binding: cloneStrideE10LiveActionBinding(binding), PreparedAt: r.now().UTC()}
		var existed bool
		operation, existed, err = r.operationStore.Prepare(operation)
		if err != nil {
			return nil, existed, err
		}
		recovered = existed
	}
	if !recovered && !r.bindingCurrent(binding) {
		_ = r.operationStore.Abort(operationKey, fingerprint)
		return nil, false, ErrStrideE10NotFound
	}
	replayed := recovered
	if !recovered || !r.liveActionBindingApplied(binding) {
		var executeErr error
		authorityReplayed, executeErr := r.executeBoundAction(ctx, principal, command, binding)
		replayed = replayed || authorityReplayed
		if executeErr != nil {
			if !recovered {
				_ = r.operationStore.Abort(operationKey, fingerprint)
			}
			return nil, replayed, executeErr
		}
	}
	if r.persistRuntime != nil {
		if err := r.persistRuntime(r); err != nil {
			return nil, replayed, ErrStrideE10Invalid
		}
	}
	if r.operationStore.Commit(operationKey, fingerprint, r.now().UTC()) != nil {
		return nil, replayed, ErrStrideE10Invalid
	}
	value, err := r.project(principal, binding.Surface)
	if err != nil {
		return nil, replayed, err
	}
	if r.persistRuntime != nil && r.persistRuntime(r) != nil {
		return nil, replayed, ErrStrideE10Invalid
	}
	if r.operationStore.Complete(operationKey, fingerprint, value, r.now().UTC()) != nil {
		return nil, replayed, ErrStrideE10Invalid
	}
	r.idempotency[operationKey] = strideE10LiveIdempotency{Fingerprint: fingerprint, Value: cloneStrideE10LiveValue(value)}
	r.actionUses[binding.ID] = command.IdempotencyKey
	return value, replayed, nil
}

func (r *StrideE10ProductLiveRuntime) repairBoundContributionNetworkEffect(binding StrideE10LiveActionBinding) error {
	if !r.contributionPostImageApplied(binding) || r.contributionNetworkCompoundApplied(binding) {
		return nil
	}
	switch binding.Type {
	case "contribution-publish":
		if binding.Publication == nil {
			return ErrNetworkAuthorityInvalid
		}
		r.contribution.mu.RLock()
		attestations := make([]ContributionAttestation, 0, len(binding.Publication.Attestations))
		for _, ref := range binding.Publication.Attestations {
			attestation := r.contribution.attestations[ref.ID]
			if attestation.Header.Revision != ref.Revision || attestation.Header.ContentDigest != ref.Digest {
				r.contribution.mu.RUnlock()
				return ErrNetworkAuthorityDenied
			}
			attestations = append(attestations, cloneContract(attestation))
		}
		r.contribution.mu.RUnlock()
		return r.installNetworkPublicationDependencies(*binding.Publication, attestations)
	case "contribution-withdraw":
		return r.network.InstallPublicationAuthority(*binding.Publication, nil)
	case "contribution-correct", "contribution-revoke":
		return r.network.InstallClaimAuthority(*binding.Claim)
	case "contribution-attestation-revoke":
		return r.network.InstallAttestationAuthority(*binding.Attestation)
	case "contribution-organization-approve", "contribution-organization-deny", "contribution-named-party-decision":
		return r.network.InstallFieldApprovalAuthority(*binding.Approval)
	}
	return ErrNetworkAuthorityInvalid
}

func (r *StrideE10ProductLiveRuntime) boundContributionControllerCurrent(binding StrideE10LiveActionBinding) bool {
	if binding.ContributionAssertion == nil || binding.ContributionAssertion.Controller.PrincipalID != binding.PersonID {
		return false
	}
	r.contribution.mu.RLock()
	defer r.contribution.mu.RUnlock()
	grant, ok := r.contribution.grants[binding.ContributionAssertion.GrantID]
	if !ok || grant.Controller != binding.ContributionAssertion.Controller {
		return false
	}
	wantedRole := map[string]string{
		"contribution-publish": "person_publisher", "contribution-withdraw": "person_publisher",
		"contribution-organization-approve": "organization_reviewer", "contribution-organization-deny": "organization_reviewer",
		"contribution-named-party-decision": "named_party", "contribution-attestation-revoke": "signing_issuer",
		"contribution-correct": "organization_reviewer", "contribution-revoke": "organization_reviewer",
	}[binding.Type]
	if grant.Role != wantedRole {
		return false
	}
	if wantedRole == "named_party" {
		return binding.Approval != nil && grant.PartyID == binding.Approval.ApproverPartyID
	}
	if wantedRole == "person_publisher" {
		return binding.Publication != nil && grant.PersonID == binding.Publication.SubjectPersonID
	}
	if wantedRole == "signing_issuer" {
		return binding.Attestation != nil && grant.OrganizationID == binding.Attestation.OrganizationID
	}
	return binding.Claim != nil && grant.OrganizationID == binding.Claim.OrganizationID || binding.Approval != nil && grant.OrganizationID == binding.Approval.OrganizationID
}

func strideE10DirectOperationAllows(operation, action string) bool {
	allowed := map[string][]string{
		"identity.self_profile": {"profile-update"}, "organizations.collection": {"organization-create"},
		"organizations.member_profile": {"organization-member-profile-update"},
		"organizations.join_requests":  {"organization-join"}, "organizations.decide_join_request": {"organization-request-approve", "organization-request-deny"},
		"organizations.leave": {"organization-leave"}, "organizations.change_member_role": {"organization-member-role-change"},
		"organizations.transfer_ownership": {"organization-ownership-transfer"}, "organizations.revoke_member": {"organization-member-revoke"},
		"organizations.close_join_request": {"organization-request-cancel"}, "organizations.expire_join_request": {"organization-request-expire"}, "session.switch_organization": {"organization-switch"},
		"contributions.subject_review": {"contribution-subject-approve", "contribution-subject-dispute"}, "contributions.publish": {"contribution-publish"},
		"contributions.withdraw": {"contribution-withdraw"}, "contributions.decide_approval": {"contribution-organization-approve", "contribution-organization-deny"},
		"contributions.correct": {"contribution-correct"}, "contributions.revoke": {"contribution-revoke"}, "contributions.named_party_decision": {"contribution-named-party-decision"}, "contributions.revoke_attestation": {"contribution-attestation-revoke"},
		"network.profile_draft": {"network-draft-save"}, "network.profile": {"network-publish", "network-pause"},
		"network.profile_publish": {"network-publish"}, "network.profile_pause": {"network-pause"}, "network.profile_off": {"network-profile-off"}, "network.profile_delete": {"network-profile-delete"}, "network.searchable_fields": {"network-searchable-fields-update"},
		"network.search": {"network-search-submit"}, "network.contacts": {"contact-send"}, "network.decide_contact": {"contact-accept", "contact-decline", "contact-withdraw"},
		"network.block": {"network-block", "network-unblock"}, "network.recruiting_grants": {"organization-recruiting-grant-create"},
		"network.recruiting_grant_revoke": {"organization-recruiting-grant-revoke"}, "work_record.export": {"work-record-export"},
		"work_record.delete": {"work-record-delete"}, "network.profile_export": {"network-profile-export"},
	}
	return containsSTRIDEString(allowed[operation], action)
}

func strideE10SurfaceForOperation(operation string) string {
	switch operation {
	case "identity.self_profile":
		return "profile"
	case "identity.coworker_profile":
		return "coworker-profile"
	case "work_record.self", "work_record.export":
		return "work-record"
	case "organizations.collection":
		return "organizations"
	case "organizations.member_profile", "organizations.change_member_role", "organizations.transfer_ownership", "organizations.revoke_member":
		return "organization-people"
	case "organizations.join_requests", "organizations.close_join_request", "organizations.decide_join_request", "organizations.expire_join_request":
		return "organization-requests"
	case "contributions.approvals", "contributions.decide_approval", "contributions.correct", "contributions.revoke", "contributions.audit", "contributions.named_party_decision", "contributions.revoke_attestation":
		return "contribution-approvals"
	case "network.profile_draft":
		return "network-draft"
	case "network.profile", "network.preview", "network.profile_publish", "network.profile_pause", "network.profile_off", "network.profile_delete", "network.profile_export":
		return "network-preview"
	case "network.search":
		return "network-search"
	case "network.contacts", "network.decide_contact":
		return "contact-inbox"
	case "network.block":
		return "network-blocks"
	case "network.recruiting", "network.recruiting_grants", "network.recruiting_grant_revoke", "network.recruiting_audit", "network.recruiting_receipts", "network.recruiting_limits":
		return "organization-recruiting"
	}
	return ""
}

func cloneStrideE10LiveValue(value any) any {
	raw, _ := json.Marshal(value)
	var result any
	_ = json.Unmarshal(raw, &result)
	return result
}

func (r *StrideE10ProductLiveRuntime) lookupLiveAction(personID, actionID string) (StrideE10LiveActionBinding, bool) {
	r.mu.RLock()
	binding, ok := r.actions[actionID]
	r.mu.RUnlock()
	if ok {
		return cloneStrideE10LiveActionBinding(binding), true
	}
	record, found, err := r.operationStore.FindAction(personID, actionID)
	if err != nil || !found {
		return StrideE10LiveActionBinding{}, false
	}
	return cloneStrideE10LiveActionBinding(record.Binding), true
}

func (r *StrideE10ProductLiveRuntime) hydrateLiveActionBinding(ctx context.Context, principal StrideE10ProductPrincipal, command StrideE10ProductCommand, binding StrideE10LiveActionBinding, at time.Time) (StrideE10LiveActionBinding, error) {
	switch binding.Type {
	case "profile-update":
		if binding.Profile == nil {
			value, err := r.buildProfileUpdate(principal.PersonID, command, at)
			if err != nil {
				return StrideE10LiveActionBinding{}, err
			}
			binding.Profile = &value
		}
	case "organization-create":
		if binding.Organization == nil || binding.OwnerMembership == nil || binding.AuditEvent == nil {
			organization, membership, audit, err := r.buildOrganizationCreate(principal.PersonID, command, at)
			if err != nil {
				return StrideE10LiveActionBinding{}, err
			}
			binding.Organization, binding.OwnerMembership, binding.AuditEvent = &organization, &membership, &audit
		}
	case "organization-join":
		if binding.JoinRequest == nil || binding.AuditEvent == nil {
			request, audit, err := r.buildOrganizationJoin(principal.PersonID, command, at)
			if err != nil {
				return StrideE10LiveActionBinding{}, err
			}
			binding.JoinRequest, binding.AuditEvent = &request, &audit
		}
	case "organization-switch":
		if binding.ActiveSession == nil || binding.AuditEvent == nil {
			session, audit, err := r.buildOrganizationSwitch(ctx, principal, command, binding, at)
			if err != nil {
				return StrideE10LiveActionBinding{}, err
			}
			binding.ActiveSession, binding.AuditEvent = &session, &audit
		}
		if binding.SessionAccountDigest == "" || binding.SessionGeneration == 0 {
			record, ok := userSessionStore().lookupMemberRecordByHash(binding.ActiveSession.SessionSubjectDigest, at.UTC())
			if !ok || record.PersonID != principal.PersonID || !isHexDigest(record.AccountSubjectDigest) || record.AuthorityGeneration < 1 {
				return StrideE10LiveActionBinding{}, ErrStrideE10Denied
			}
			binding.SessionAccountDigest = record.AccountSubjectDigest
			binding.SessionGeneration = record.AuthorityGeneration + 1
		}
	case "organization-request-approve", "organization-request-deny":
		current, err := r.organization.JoinRequest(binding.Target.ID)
		if err != nil || current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || current.Status != "pending" {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		values, err := strideE10LiveCommandValues(command)
		if err != nil {
			return StrideE10LiveActionBinding{}, err
		}
		next := cloneOrganizationJoinRequest(current)
		next.Header = nextAuthorityHeader(next.Header, binding.Type, at)
		next.Status = map[string]string{"organization-request-approve": "approved", "organization-request-deny": "denied"}[binding.Type]
		next.DecidedAt, next.DecidedByMembershipID = &at, principal.OrganizationMembershipID
		if reason, _ := values["reason"].(string); reason != "" {
			next.DecisionReasonDigest = sha256Hex([]byte(reason))
		}
		action := map[string]string{"organization-request-approve": "approve", "organization-request-deny": "deny"}[binding.Type]
		audit := strideE10LiveOrganizationAudit(current.OrganizationID, principal.PersonID, principal.OrganizationMembershipID, principal.OrganizationMembershipRev, current.PersonID, action, current.Header.Revision, next.Header.Revision, command.IdempotencyKey, at)
		binding.JoinDecision, binding.AuditEvent = &next, &audit
		if binding.Type == "organization-request-approve" {
			seed := current.Header.ID + "\x00" + command.IdempotencyKey
			membership := OrganizationMembership{Header: strideE10LiveHeader(STRIDEContractOrganizationMembership, current.OrganizationID, "membership_"+sha256Hex([]byte(seed))[:24], 1, seed+"\x00membership", at), PersonID: current.PersonID, OrganizationID: current.OrganizationID, Role: "member", Status: "active", GrantedAt: at, GrantedByMembershipID: principal.OrganizationMembershipID}
			if membership.Validate() != nil {
				return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
			}
			binding.ApprovedMembership = &membership
		}
	case "organization-member-role-change":
		current, err := r.organization.Membership(binding.Target.ID)
		if err != nil || current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || current.Status != "active" || current.Role == "owner" {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		values, err := strideE10LiveCommandValues(command)
		if err != nil {
			return StrideE10LiveActionBinding{}, err
		}
		role, _ := values["role"].(string)
		next := cloneOrganizationMembership(current)
		next.Header = nextAuthorityHeader(next.Header, "role_change", at)
		next.Role = role
		audit := strideE10LiveOrganizationAudit(current.OrganizationID, principal.PersonID, principal.OrganizationMembershipID, principal.OrganizationMembershipRev, current.PersonID, "role_change", current.Header.Revision, next.Header.Revision, command.IdempotencyKey, at)
		binding.ReplacementMembership, binding.AuditEvent = &next, &audit
	case "organization-member-revoke":
		current, err := r.organization.Membership(binding.Target.ID)
		actor, actorErr := r.organization.Membership(principal.OrganizationMembershipID)
		if err != nil || actorErr != nil || current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || current.Status != "active" || current.Header.ID == actor.Header.ID || current.OrganizationID != principal.ActiveOrganizationID || current.Role == "owner" || !oneOf(actor.Role, "owner", "admin") {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		next := cloneOrganizationMembership(current)
		next.Header = nextAuthorityHeader(next.Header, "revoke", at)
		next.Status, next.EndedAt = "revoked", &at
		audit := strideE10LiveOrganizationAudit(current.OrganizationID, principal.PersonID, principal.OrganizationMembershipID, principal.OrganizationMembershipRev, current.PersonID, "revoke", current.Header.Revision, next.Header.Revision, command.IdempotencyKey, at)
		binding.ReplacementMembership, binding.AuditEvent = &next, &audit
	case "organization-ownership-transfer":
		target, err := r.organization.Membership(binding.Target.ID)
		actor, actorErr := r.organization.Membership(principal.OrganizationMembershipID)
		if err != nil || actorErr != nil || target.Header.Revision != binding.Target.Revision || target.Header.ContentDigest != binding.Target.Digest || target.Status != "active" || target.Role == "owner" || actor.Role != "owner" {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		priorNext, newNext := cloneOrganizationMembership(actor), cloneOrganizationMembership(target)
		priorNext.Header = nextAuthorityHeader(priorNext.Header, "transfer", at)
		newNext.Header = nextAuthorityHeader(newNext.Header, "transfer", at)
		priorNext.Role, newNext.Role = "admin", "owner"
		audit := strideE10LiveOrganizationAudit(target.OrganizationID, principal.PersonID, actor.Header.ID, actor.Header.Revision, target.PersonID, "transfer", target.Header.Revision, newNext.Header.Revision, command.IdempotencyKey, at)
		binding.PriorOwnerNext, binding.NewOwnerNext, binding.AuditEvent = &priorNext, &newNext, &audit
	case "network-draft-save", "network-publish", "network-pause", "network-profile-off", "network-searchable-fields-update", "network-profile-delete":
		if binding.NetworkActor == nil {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		if binding.Type == "network-draft-save" && binding.Target.ContractType == STRIDEContractPublishedContributionClaim && binding.Publication != nil {
			if binding.Publication.Header.Revision != binding.Target.Revision || binding.Publication.Header.ContentDigest != binding.Target.Digest || binding.Publication.SubjectPersonID != principal.PersonID || binding.Publication.State != "published" {
				return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
			}
			values, err := strideE10LiveCommandValues(command)
			if err != nil {
				return StrideE10LiveActionBinding{}, err
			}
			fields := strideE10ApplyNetworkDraftValues(nil, values)
			if len(fields) == 0 {
				return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
			}
			digest, _ := STRIDEContractDigest(fields)
			seed := principal.PersonID + "\x00network-profile"
			profile := NetworkProfileProjection{Header: strideE10LiveHeader(STRIDEContractNetworkProfileProjection, STRIDEGlobalPersonTenant, "network_profile_"+sha256Hex([]byte(seed))[:24], 1, seed, at), SubjectPersonID: principal.PersonID, Publication: binding.Target, Fields: fields, FieldsDigest: digest, Discoverability: "unlisted", Controller: *binding.NetworkActor, State: "draft", StateChangedAt: at}
			if profile.Validate() != nil {
				return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
			}
			binding.NetworkProfile = &profile
			break
		}
		if binding.Target.ContractType != STRIDEContractNetworkProfileProjection {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		r.network.mu.Lock()
		current := cloneNetworkProjection(r.network.profiles[binding.Target.ID])
		r.network.mu.Unlock()
		if current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		next := cloneNetworkProjection(current)
		next.Header = nextAuthorityHeader(next.Header, binding.Type, at)
		next.StateChangedAt = at
		switch binding.Type {
		case "network-draft-save":
			next.State, next.Discoverability = "draft", "unlisted"
			values, err := strideE10LiveCommandValues(command)
			if err != nil {
				return StrideE10LiveActionBinding{}, err
			}
			next.Fields = strideE10ApplyNetworkDraftValues(next.Fields, values)
		case "network-publish":
			next.State, next.Discoverability = "published", "signed_in_network"
		case "network-pause":
			next.State, next.Discoverability, next.PurgeGeneration = "paused", "unlisted", current.PurgeGeneration+1
		case "network-profile-off":
			next.State, next.Discoverability, next.PurgeGeneration = "off", "unlisted", current.PurgeGeneration+1
		case "network-profile-delete":
			next.State, next.Discoverability, next.PurgeGeneration = "deleted", "unlisted", current.PurgeGeneration+1
		case "network-searchable-fields-update":
			values, err := strideE10LiveCommandValues(command)
			if err != nil {
				return StrideE10LiveActionBinding{}, err
			}
			next.Fields = strideE10FilterNetworkFields(next.Fields, strideE10LiveStringList(values["fields"]))
		}
		digest, err := STRIDEContractDigest(next.Fields)
		if err != nil || len(next.Fields) == 0 {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		next.FieldsDigest = digest
		if next.Validate() != nil {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		binding.NetworkProfile = &next
	case "network-search-submit":
		if binding.TalentGrant == nil || binding.Target.ContractType != STRIDEContractTalentSearchGrant {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		values, err := strideE10LiveCommandValues(command)
		if err != nil {
			return StrideE10LiveActionBinding{}, err
		}
		query, _ := values["query"].(string)
		filter := NetworkSearchFilter{Field: "problem_class", Operation: "contains", VisibleValue: query, ValueDigest: sha256Hex([]byte(query))}
		binding.NetworkSearch = &NetworkSearchRequest{GrantRef: refForHeader(binding.TalentGrant.Header), SearcherPersonID: principal.PersonID, OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID, MembershipRevision: principal.OrganizationMembershipRev, HumanQuery: query, OriginalQueryDigest: sha256Hex([]byte(query)), StructuredFilters: []NetworkSearchFilter{filter}, InterpretationConfirmed: true, Limit: networkResultsPerSearch, IdempotencyKeyDigest: sha256Hex([]byte(command.IdempotencyKey)), At: at}
	case "contact-send":
		if binding.TalentGrant == nil || binding.Target.ContractType != STRIDEContractNetworkProfileProjection {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		values, err := strideE10LiveCommandValues(command)
		if err != nil {
			return StrideE10LiveActionBinding{}, err
		}
		r.network.mu.Lock()
		recipient := cloneNetworkProjection(r.network.profiles[binding.Target.ID])
		r.network.mu.Unlock()
		if recipient.State != "published" || recipient.Header.Revision != binding.Target.Revision || recipient.Header.ContentDigest != binding.Target.Digest {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		purpose, _ := values["purpose"].(string)
		note, _ := values["note"].(string)
		collaboration, _ := values["collaborationType"].(string)
		binding.ContactAdmission = &NetworkContactAdmission{GrantRef: refForHeader(binding.TalentGrant.Header), SenderPersonID: principal.PersonID, SenderOrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID, MembershipRevision: principal.OrganizationMembershipRev, RecipientProjection: binding.Target, Purpose: "purpose_" + sha256Hex([]byte(purpose))[:24], NoteDigest: sha256Hex([]byte(note)), CollaborationType: collaboration, ExpiresAt: at.Add(14 * 24 * time.Hour), IdempotencyKeyDigest: sha256Hex([]byte(command.IdempotencyKey)), At: at}
	case "organization-recruiting-grant-create":
		if binding.TalentAssertion == nil || binding.Target.ContractType != STRIDEContractOrganizationMembership {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		target, targetErr := r.organization.Membership(binding.Target.ID)
		if targetErr != nil || target.Header.Revision != binding.Target.Revision || target.Header.ContentDigest != binding.Target.Digest || target.Status != "active" || target.OrganizationID != principal.ActiveOrganizationID || target.PersonID == principal.PersonID {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		r.network.mu.Lock()
		authority := r.network.capabilityAuthorities[binding.TalentAssertion.AuthorityID]
		for _, existing := range r.network.grants {
			if existing.MembershipID == target.Header.ID && existing.State == "active" && at.Before(existing.ExpiresAt) {
				r.network.mu.Unlock()
				return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
			}
		}
		r.network.mu.Unlock()
		if authority.validate() != nil || authority.Revision != binding.TalentAssertion.AuthorityRevision || authority.ControllerPersonID != binding.TalentAssertion.ControllerPersonID {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		seed := target.PersonID + "\x00" + principal.ActiveOrganizationID + "\x00" + command.IdempotencyKey
		grant := TalentSearchGrant{Header: strideE10LiveHeader(STRIDEContractTalentSearchGrant, principal.ActiveOrganizationID, "talent_grant_"+sha256Hex([]byte(seed))[:24], 1, seed+"\x00grant", at), OrganizationID: principal.ActiveOrganizationID, MembershipID: target.Header.ID, MembershipRevision: target.Header.Revision, SearcherPersonID: target.PersonID, CapabilityAdministrator: STRIDEControllerRevision{PrincipalID: authority.ControllerPersonID, AuthorityID: authority.ID, AuthorityRevision: authority.Revision, PolicyRevision: authority.PolicyRevision}, PolicyRevision: authority.PolicyRevision, State: "active", GrantedAt: at, ExpiresAt: at.Add(30 * 24 * time.Hour)}
		if grant.Validate() != nil {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		binding.TalentGrant = &grant
	case "organization-recruiting-grant-revoke":
		if binding.TalentAssertion == nil || binding.Target.ContractType != STRIDEContractTalentSearchGrant {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		r.network.mu.Lock()
		current := cloneTalentSearchGrant(r.network.grants[binding.Target.ID])
		r.network.mu.Unlock()
		if current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || current.State != "active" {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		next := cloneTalentSearchGrant(current)
		next.Header = nextAuthorityHeader(next.Header, "revoke", at)
		next.State, next.RevokedAt = "revoked", &at
		binding.TalentGrant = &next
	case "contribution-organization-approve", "contribution-organization-deny":
		if binding.ContributionAssertion == nil || binding.Target.ContractType != STRIDEContractFieldReleaseApproval {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		r.contribution.mu.RLock()
		current := cloneStrideE10JSON(r.contribution.approvals[binding.Target.ID])
		r.contribution.mu.RUnlock()
		decision := map[string]string{"contribution-organization-approve": "approved", "contribution-organization-deny": "denied"}[binding.Type]
		if current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || current.State != "pending" {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		binding.Approval = strideE10ExpectedApprovalDecision(current, decision, binding.ContributionAssertion.Controller, at)
	case "contribution-named-party-decision":
		if binding.ContributionAssertion == nil || binding.Target.ContractType != STRIDEContractFieldReleaseApproval {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		values, err := strideE10LiveCommandValues(command)
		if err != nil {
			return StrideE10LiveActionBinding{}, err
		}
		decision, _ := values["decision"].(string)
		if !oneOf(decision, "approved", "denied") {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		binding.Decision = decision
		r.contribution.mu.RLock()
		current := cloneStrideE10JSON(r.contribution.approvals[binding.Target.ID])
		r.contribution.mu.RUnlock()
		if current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || current.State != "pending" {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		binding.Approval = strideE10ExpectedApprovalDecision(current, decision, binding.ContributionAssertion.Controller, at)
	case "contribution-attestation-revoke":
		if binding.ContributionAssertion == nil || binding.Target.ContractType != STRIDEContractContributionAttestation {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		r.contribution.mu.RLock()
		current := cloneStrideE10JSON(r.contribution.attestations[binding.Target.ID])
		r.contribution.mu.RUnlock()
		if current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || current.State != "active" {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		prior := refForHeader(current.Header)
		current.Header = nextAuthorityHeader(current.Header, "revoked", at)
		current.State, current.RevokedAt, current.Supersedes = "revoked", &at, &prior
		binding.Attestation = &current
	case "contribution-withdraw":
		if binding.ContributionAssertion == nil || binding.Target.ContractType != STRIDEContractPublishedContributionClaim {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		r.contribution.mu.RLock()
		current := cloneStrideE10JSON(r.contribution.publications[binding.Target.ID])
		r.contribution.mu.RUnlock()
		if current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || current.State != "published" {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		prior := refForHeader(current.Header)
		current.Header = nextAuthorityHeader(current.Header, "withdrawn", at)
		current.State, current.Visibility, current.StateChangedAt, current.Supersedes = "withdrawn", "private", at, &prior
		binding.Publication = &current
	case "contribution-correct":
		if binding.ContributionAssertion == nil || binding.Target.ContractType != STRIDEContractContributionClaim {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		r.contribution.mu.RLock()
		current := cloneStrideE10ContributionClaim(r.contribution.claims[binding.Target.ID])
		var subjectController *STRIDEControllerRevision
		for _, grant := range r.contribution.grants {
			if grant.Role == "subject" && grant.PersonID == current.SubjectPersonID {
				if subjectController != nil {
					subjectController = nil
					break
				}
				controller := grant.Controller
				subjectController = &controller
			}
		}
		r.contribution.mu.RUnlock()
		if current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || !oneOf(current.State, "verified", "revalidation_required") || subjectController == nil {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		values, err := strideE10LiveCommandValues(command)
		if err != nil {
			return StrideE10LiveActionBinding{}, err
		}
		reason, _ := values["reason"].(string)
		seed := current.Header.ID + "\x00" + command.IdempotencyKey + "\x00" + reason
		replacement := cloneStrideE10ContributionClaim(current)
		replacement.Header = strideE10LiveHeader(STRIDEContractContributionClaim, current.OrganizationID, "claim_"+sha256Hex([]byte(seed))[:24], 1, seed+"\x00replacement", at)
		replacement.State, replacement.StateChangedAt, replacement.Supersedes = "verified", at, nil
		replacement.SubjectReview, replacement.OrganizationReview = subjectController, &binding.ContributionAssertion.Controller
		if replacement.Validate() != nil {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		binding.CorrectedClaim = &replacement
		prior := cloneStrideE10ContributionClaim(current)
		prior.State, prior.OrganizationReview = "superseded", &binding.ContributionAssertion.Controller
		advanceClaimRevision(&prior, "superseded", at)
		binding.Claim = &prior
	case "contribution-revoke":
		if binding.ContributionAssertion == nil || binding.Target.ContractType != STRIDEContractContributionClaim {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		r.contribution.mu.RLock()
		current := cloneStrideE10ContributionClaim(r.contribution.claims[binding.Target.ID])
		r.contribution.mu.RUnlock()
		if current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || !ContributionClaimTransitionAllowed(current.State, "revoked") {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		current.State, current.OrganizationReview = "revoked", &binding.ContributionAssertion.Controller
		advanceClaimRevision(&current, "revoked", at)
		binding.Claim = &current
	case "work-record-export", "network-profile-export":
		if _, deleted := r.portableStore.Load(principal.PersonID); deleted {
			return StrideE10LiveActionBinding{}, ErrStrideE10NotFound
		}
		projection, err := r.project(principal, binding.Surface)
		if err != nil {
			return StrideE10LiveActionBinding{}, err
		}
		body, err := json.Marshal(strideE10PortableExportProjection(projection))
		if err != nil || len(body) > strideE10MaxBodyBytes {
			return StrideE10LiveActionBinding{}, ErrStrideE10Invalid
		}
		seed := principal.PersonID + "\x00" + binding.Surface + "\x00" + sha256Hex([]byte(command.IdempotencyKey))
		receipt := strideE10ExportReceipt{ID: "export-" + sha256Hex([]byte(seed))[:24], PersonID: principal.PersonID, Surface: binding.Surface, Revision: command.ExpectedRevision + 1, PackageDigest: sha256Hex(body), ExpiresAt: at.Add(15 * time.Minute)}
		binding.ExportReceipt, binding.ExportBody = &receipt, append(json.RawMessage(nil), body...)
	}
	return cloneStrideE10LiveActionBinding(binding), nil
}

func (r *StrideE10ProductLiveRuntime) liveActionBindingApplied(binding StrideE10LiveActionBinding) bool {
	if strideE10CompoundContributionAction(binding.Type) {
		return r.contributionNetworkCompoundApplied(binding)
	}
	if binding.Type == "work-record-delete" {
		_, deleted := r.portableStore.Load(binding.PersonID)
		return deleted
	}
	if binding.Profile != nil {
		r.organization.mu.RLock()
		current := r.organization.profiles[binding.Profile.Header.ID]
		r.organization.mu.RUnlock()
		if current.Header.Revision == binding.Profile.Header.Revision && current.Header.ContentDigest == binding.Profile.Header.ContentDigest {
			return true
		}
	}
	if binding.ExportReceipt != nil {
		r.mu.RLock()
		packaged, ok := r.packages[binding.ExportReceipt.ID]
		r.mu.RUnlock()
		return ok && packaged.Receipt.PackageDigest == binding.ExportReceipt.PackageDigest && sha256Hex(packaged.Body) == binding.ExportReceipt.PackageDigest
	}
	if binding.Organization != nil && binding.OwnerMembership != nil {
		r.organization.mu.RLock()
		organization := r.organization.organizations[binding.Organization.Header.ID]
		membership := r.organization.memberships[binding.OwnerMembership.Header.ID]
		r.organization.mu.RUnlock()
		if organization.Header.ContentDigest == binding.Organization.Header.ContentDigest && membership.Header.ContentDigest == binding.OwnerMembership.Header.ContentDigest {
			return true
		}
	}
	if binding.JoinRequest != nil {
		current, err := r.organization.JoinRequest(binding.JoinRequest.Header.ID)
		if err == nil && current.Header.Revision == binding.JoinRequest.Header.Revision && current.Header.ContentDigest == binding.JoinRequest.Header.ContentDigest {
			return true
		}
	}
	if binding.JoinDecision != nil {
		current, err := r.organization.JoinRequest(binding.JoinDecision.Header.ID)
		if err == nil && current.Header.Revision == binding.JoinDecision.Header.Revision && current.Header.ContentDigest == binding.JoinDecision.Header.ContentDigest {
			return true
		}
	}
	if binding.ActiveSession != nil {
		return r.organizationSwitchPostimagesApplied(binding)
	}
	if binding.ReplacementMembership != nil {
		current, err := r.organization.Membership(binding.ReplacementMembership.Header.ID)
		if err == nil && current.Header.Revision == binding.ReplacementMembership.Header.Revision && current.Header.ContentDigest == binding.ReplacementMembership.Header.ContentDigest {
			return true
		}
	}
	if binding.PriorOwnerNext != nil && binding.NewOwnerNext != nil {
		prior, priorErr := r.organization.Membership(binding.PriorOwnerNext.Header.ID)
		next, nextErr := r.organization.Membership(binding.NewOwnerNext.Header.ID)
		if priorErr == nil && nextErr == nil && prior.Header.Revision == binding.PriorOwnerNext.Header.Revision && prior.Header.ContentDigest == binding.PriorOwnerNext.Header.ContentDigest && next.Header.Revision == binding.NewOwnerNext.Header.Revision && next.Header.ContentDigest == binding.NewOwnerNext.Header.ContentDigest {
			return true
		}
	}
	if binding.CorrectedClaim != nil {
		r.contribution.mu.RLock()
		current := r.contribution.claims[binding.CorrectedClaim.Header.ID]
		r.contribution.mu.RUnlock()
		if current.Header.Revision == binding.CorrectedClaim.Header.Revision && current.Header.ContentDigest == binding.CorrectedClaim.Header.ContentDigest {
			return true
		}
	}
	if binding.Publication != nil {
		r.contribution.mu.RLock()
		current := r.contribution.publications[binding.Publication.Header.ID]
		r.contribution.mu.RUnlock()
		if current.Header.Revision == binding.Publication.Header.Revision && current.Header.ContentDigest == binding.Publication.Header.ContentDigest {
			return true
		}
	}
	if binding.Target.ContractType == STRIDEContractPublishedContributionClaim && oneOf(binding.Type, "contribution-withdraw") {
		r.contribution.mu.RLock()
		current := r.contribution.publications[binding.Target.ID]
		r.contribution.mu.RUnlock()
		return current.Header.Revision == binding.Target.Revision+1 && current.State == "withdrawn"
	}
	if binding.Target.ContractType == STRIDEContractContributionClaim && oneOf(binding.Type, "contribution-revoke") {
		r.contribution.mu.RLock()
		current := r.contribution.claims[binding.Target.ID]
		r.contribution.mu.RUnlock()
		return current.Header.Revision == binding.Target.Revision+1 && current.State == "revoked"
	}
	if binding.Target.ContractType == STRIDEContractContributionAttestation && binding.Type == "contribution-attestation-revoke" {
		r.contribution.mu.RLock()
		current := r.contribution.attestations[binding.Target.ID]
		r.contribution.mu.RUnlock()
		return current.Header.Revision == binding.Target.Revision+1 && current.State == "revoked"
	}
	if binding.Target.ContractType == STRIDEContractFieldReleaseApproval && oneOf(binding.Type, "contribution-organization-approve", "contribution-organization-deny", "contribution-named-party-decision") {
		r.contribution.mu.RLock()
		current := r.contribution.approvals[binding.Target.ID]
		r.contribution.mu.RUnlock()
		wanted := binding.Decision
		if wanted == "" {
			wanted = map[string]string{"contribution-organization-approve": "approved", "contribution-organization-deny": "denied"}[binding.Type]
		}
		return current.Header.Revision == binding.Target.Revision+1 && current.State == wanted
	}
	if binding.Target.ContractType == STRIDEContractContributionClaim && oneOf(binding.Type, "contribution-subject-approve", "contribution-subject-dispute") {
		r.contribution.mu.RLock()
		current := r.contribution.claims[binding.Target.ID]
		r.contribution.mu.RUnlock()
		if current.Header.Revision != binding.Target.Revision+1 || current.SubjectReview == nil {
			return false
		}
		return current.State == map[string]string{"contribution-subject-approve": "subject_review", "contribution-subject-dispute": "disputed"}[binding.Type] && binding.ContributionAssertion != nil && *current.SubjectReview == binding.ContributionAssertion.Controller
	}
	if binding.NetworkProfile != nil {
		r.network.mu.Lock()
		current := r.network.profiles[binding.NetworkProfile.Header.ID]
		r.network.mu.Unlock()
		if current.Header.Revision == binding.NetworkProfile.Header.Revision && current.Header.ContentDigest == binding.NetworkProfile.Header.ContentDigest {
			return true
		}
	}
	if binding.NetworkSearch != nil {
		idDigest := sha256Hex([]byte(binding.NetworkSearch.IdempotencyKeyDigest + "\x00" + binding.NetworkSearch.OriginalQueryDigest))
		r.network.mu.Lock()
		current := r.network.searchReceipts["search_"+idDigest[:24]]
		r.network.mu.Unlock()
		return current.Header.ID != "" && current.OriginalQueryDigest == binding.NetworkSearch.OriginalQueryDigest && current.Grant == binding.NetworkSearch.GrantRef && current.OrganizationID == binding.NetworkSearch.OrganizationID
	}
	if binding.ContactAdmission != nil {
		id := "contact_" + binding.ContactAdmission.IdempotencyKeyDigest[:24]
		r.network.mu.Lock()
		current := r.network.contacts[id]
		r.network.mu.Unlock()
		return current.Header.ID == id && current.SenderPersonID == binding.ContactAdmission.SenderPersonID && current.SenderOrganizationID == binding.ContactAdmission.SenderOrganizationID && current.RecipientProjection == binding.ContactAdmission.RecipientProjection && current.Purpose == binding.ContactAdmission.Purpose && current.NoteDigest == binding.ContactAdmission.NoteDigest && current.CollaborationType == binding.ContactAdmission.CollaborationType
	}
	if binding.TalentGrant != nil {
		r.network.mu.Lock()
		current := r.network.grants[binding.TalentGrant.Header.ID]
		r.network.mu.Unlock()
		if current.Header.Revision == binding.TalentGrant.Header.Revision && current.Header.ContentDigest == binding.TalentGrant.Header.ContentDigest {
			return true
		}
	}
	if binding.Block != nil {
		r.network.mu.Lock()
		current := r.network.blocks[binding.Block.Header.ID]
		r.network.mu.Unlock()
		if current.Header.Revision == binding.Block.Header.Revision && current.Header.ContentDigest == binding.Block.Header.ContentDigest {
			return true
		}
	}
	if binding.Target.ContractType == STRIDEContractContactRequest && oneOf(binding.Type, "contact-accept", "contact-decline", "contact-withdraw") {
		r.network.mu.Lock()
		current := r.network.contacts[binding.Target.ID]
		r.network.mu.Unlock()
		wanted := map[string]string{"contact-accept": "accepted", "contact-decline": "declined", "contact-withdraw": "withdrawn"}[binding.Type]
		return current.Header.Revision == binding.Target.Revision+1 && current.State == wanted && (wanted != "accepted" || current.AcceptedChannelDigest == binding.AcceptedChannelDigest)
	}
	return false
}

// organizationSwitchPostimagesApplied treats the organization authority row
// and the durable login-session binding as one compound postimage. A prepared
// operation may have lost the process between those writes; recovery must not
// commit merely because the first store advanced.
func (r *StrideE10ProductLiveRuntime) organizationSwitchPostimagesApplied(binding StrideE10LiveActionBinding) bool {
	if r == nil || binding.ActiveSession == nil || !isHexDigest(binding.SessionAccountDigest) || binding.SessionGeneration < 1 {
		return false
	}
	session := *binding.ActiveSession
	r.organization.mu.RLock()
	current := r.organization.sessions[session.SessionSubjectDigest]
	r.organization.mu.RUnlock()
	if current.Header.Revision != session.Header.Revision || current.Header.ContentDigest != session.Header.ContentDigest {
		return false
	}
	store := userSessionStore()
	record, ok := store.lookupMemberRecordByHash(session.SessionSubjectDigest, r.now().UTC())
	if !ok || !strideE10SwitchSessionRecordMatches(record, binding) {
		return false
	}
	if strings.TrimSpace(store.path) == "" {
		return true
	}
	body, err := os.ReadFile(store.path)
	if err != nil {
		return false
	}
	var durable map[string]sessionRecord
	if json.Unmarshal(body, &durable) != nil {
		return false
	}
	return strideE10SwitchSessionRecordMatches(durable[session.SessionSubjectDigest], binding)
}

func strideE10SwitchSessionRecordMatches(record sessionRecord, binding StrideE10LiveActionBinding) bool {
	if binding.ActiveSession == nil {
		return false
	}
	session := *binding.ActiveSession
	return record.Kind == "" && record.PersonID == session.PersonID && record.ActiveOrganizationID == session.OrganizationID &&
		record.OrganizationMembershipID == session.MembershipID && record.OrganizationMembershipRev == session.MembershipRevision &&
		record.ActiveOrganizationSessionRev == session.SessionRevision && record.AccountSubjectDigest == binding.SessionAccountDigest &&
		record.AuthorityGeneration == binding.SessionGeneration && record.Expires.Equal(session.ExpiresAt)
}

func strideE10CompoundContributionAction(action string) bool {
	return oneOf(action, "contribution-publish", "contribution-withdraw", "contribution-organization-approve", "contribution-organization-deny", "contribution-named-party-decision", "contribution-attestation-revoke", "contribution-correct", "contribution-revoke")
}

func (r *StrideE10ProductLiveRuntime) contributionNetworkCompoundApplied(binding StrideE10LiveActionBinding) bool {
	if !r.contributionPostImageApplied(binding) {
		return false
	}
	contractType, id, expected, invalid := strideE10BoundContributionPostImage(binding)
	if expected == nil {
		return false
	}
	r.network.mu.Lock()
	defer r.network.mu.Unlock()
	switch contractType {
	case STRIDEContractPublishedContributionClaim:
		current := r.network.publications[id]
		if !reflect.DeepEqual(current, *expected.(*PublishedContributionClaim)) {
			return false
		}
	case STRIDEContractContributionClaim:
		current := r.network.claims[id]
		if !reflect.DeepEqual(current, *expected.(*ContributionClaim)) {
			return false
		}
	case STRIDEContractContributionAttestation:
		current := r.network.attestations[id]
		if !reflect.DeepEqual(current, *expected.(*ContributionAttestation)) {
			return false
		}
	case STRIDEContractFieldReleaseApproval:
		current := r.network.approvals[id]
		if !reflect.DeepEqual(current, *expected.(*FieldReleaseApproval)) {
			return false
		}
	default:
		return false
	}
	if !invalid {
		return true
	}
	for _, profile := range r.network.profiles {
		if profile.State == "published" && r.networkProfileDependsOnAuthorityLocked(profile, contractType, id) {
			return false
		}
	}
	return true
}

func (r *StrideE10ProductLiveRuntime) contributionPostImageApplied(binding StrideE10LiveActionBinding) bool {
	contractType, id, expected, _ := strideE10BoundContributionPostImage(binding)
	if expected == nil {
		return false
	}
	r.contribution.mu.RLock()
	defer r.contribution.mu.RUnlock()
	switch contractType {
	case STRIDEContractPublishedContributionClaim:
		if !reflect.DeepEqual(r.contribution.publications[id], *expected.(*PublishedContributionClaim)) {
			return false
		}
	case STRIDEContractContributionClaim:
		if !reflect.DeepEqual(r.contribution.claims[id], *expected.(*ContributionClaim)) {
			return false
		}
		if binding.Type == "contribution-correct" && (binding.CorrectedClaim == nil || !reflect.DeepEqual(r.contribution.claims[binding.CorrectedClaim.Header.ID], *binding.CorrectedClaim)) {
			return false
		}
	case STRIDEContractContributionAttestation:
		if !reflect.DeepEqual(r.contribution.attestations[id], *expected.(*ContributionAttestation)) {
			return false
		}
	case STRIDEContractFieldReleaseApproval:
		if !reflect.DeepEqual(r.contribution.approvals[id], *expected.(*FieldReleaseApproval)) {
			return false
		}
	default:
		return false
	}
	return true
}

func strideE10BoundContributionPostImage(binding StrideE10LiveActionBinding) (STRIDEContractType, string, any, bool) {
	switch binding.Type {
	case "contribution-publish":
		if binding.Publication != nil && binding.ContributionAssertion != nil && binding.Publication.State == "published" && binding.Publication.Controller == binding.ContributionAssertion.Controller {
			return STRIDEContractPublishedContributionClaim, binding.Publication.Header.ID, binding.Publication, false
		}
	case "contribution-withdraw":
		if binding.Publication != nil && binding.ContributionAssertion != nil && binding.Publication.Header.ID == binding.Target.ID && binding.Publication.Header.Revision == binding.Target.Revision+1 && binding.Publication.State == "withdrawn" && binding.Publication.Visibility == "private" && binding.Publication.Controller == binding.ContributionAssertion.Controller {
			return STRIDEContractPublishedContributionClaim, binding.Target.ID, binding.Publication, true
		}
	case "contribution-correct":
		if binding.Claim != nil && binding.CorrectedClaim != nil && binding.ContributionAssertion != nil && binding.Claim.Header.ID == binding.Target.ID && binding.Claim.Header.Revision == binding.Target.Revision+1 && binding.Claim.State == "superseded" && binding.Claim.OrganizationReview != nil && *binding.Claim.OrganizationReview == binding.ContributionAssertion.Controller && binding.CorrectedClaim.State == "verified" && binding.CorrectedClaim.OrganizationReview != nil && *binding.CorrectedClaim.OrganizationReview == binding.ContributionAssertion.Controller {
			return STRIDEContractContributionClaim, binding.Target.ID, binding.Claim, true
		}
	case "contribution-revoke":
		if binding.Claim != nil && binding.ContributionAssertion != nil && binding.Claim.Header.ID == binding.Target.ID && binding.Claim.Header.Revision == binding.Target.Revision+1 && binding.Claim.State == "revoked" && binding.Claim.OrganizationReview != nil && *binding.Claim.OrganizationReview == binding.ContributionAssertion.Controller {
			return STRIDEContractContributionClaim, binding.Target.ID, binding.Claim, true
		}
	case "contribution-attestation-revoke":
		if binding.Attestation != nil && binding.ContributionAssertion != nil && binding.Attestation.Header.ID == binding.Target.ID && binding.Attestation.Header.Revision == binding.Target.Revision+1 && binding.Attestation.State == "revoked" && binding.Attestation.Issuer == binding.ContributionAssertion.Controller {
			return STRIDEContractContributionAttestation, binding.Target.ID, binding.Attestation, true
		}
	case "contribution-organization-approve", "contribution-organization-deny", "contribution-named-party-decision":
		wanted := binding.Decision
		if wanted == "" {
			wanted = map[string]string{"contribution-organization-approve": "approved", "contribution-organization-deny": "denied"}[binding.Type]
		}
		if binding.Approval != nil && binding.ContributionAssertion != nil && binding.Approval.Header.ID == binding.Target.ID && binding.Approval.Header.Revision == binding.Target.Revision+1 && binding.Approval.State == wanted && binding.Approval.Controller == binding.ContributionAssertion.Controller && oneOf(wanted, "approved", "denied") {
			return STRIDEContractFieldReleaseApproval, binding.Target.ID, binding.Approval, wanted != "approved"
		}
	}
	return "", "", nil, false
}

func strideE10BoundContributionPostTime(binding StrideE10LiveActionBinding, fallback time.Time) time.Time {
	if binding.Approval != nil && !binding.Approval.StateChangedAt.IsZero() {
		return binding.Approval.StateChangedAt.UTC()
	}
	if binding.Attestation != nil && binding.Attestation.RevokedAt != nil {
		return binding.Attestation.RevokedAt.UTC()
	}
	if binding.Claim != nil && !binding.Claim.StateChangedAt.IsZero() {
		return binding.Claim.StateChangedAt.UTC()
	}
	if binding.Publication != nil && !binding.Publication.StateChangedAt.IsZero() {
		return binding.Publication.StateChangedAt.UTC()
	}
	return fallback.UTC()
}

func (r *StrideE10ProductLiveRuntime) networkProfileDependsOnAuthorityLocked(profile NetworkProfileProjection, contractType STRIDEContractType, id string) bool {
	publication := r.network.publications[profile.Publication.ID]
	if contractType == STRIDEContractPublishedContributionClaim {
		return publication.Header.ID == id
	}
	for _, attestationRef := range publication.Attestations {
		attestation := r.network.attestations[attestationRef.ID]
		if contractType == STRIDEContractContributionAttestation && attestation.Header.ID == id || contractType == STRIDEContractContributionClaim && attestation.Claim.ID == id {
			return true
		}
		if contractType == STRIDEContractFieldReleaseApproval {
			for _, field := range attestation.ReleasedFields {
				for _, approvalRef := range field.ApprovalRefs {
					if approvalRef.ID == id {
						return true
					}
				}
			}
		}
	}
	return false
}

func (r *StrideE10ProductLiveRuntime) bindingCurrent(binding StrideE10LiveActionBinding) bool {
	if binding.Target.ID == "" {
		switch binding.Type {
		case "profile-update":
			r.organization.mu.RLock()
			profile := r.organization.profiles[binding.PersonID]
			r.organization.mu.RUnlock()
			return profile.Header.ID == "" && binding.ExpectedRevision == 1
		case "organization-create":
			count := r.organization.ActiveMembershipCount(binding.PersonID)
			return count < 3 && binding.ExpectedRevision == int64(count+1)
		case "organization-join":
			if r.organization.ActiveMembershipCount(binding.PersonID) >= 3 {
				return false
			}
			view, err := r.organization.ReadStrideE10SelfOrganizationView(binding.PersonID)
			return err == nil && binding.ExpectedRevision == int64(len(view.JoinRequests)+1)
		default:
			return true
		}
	}
	switch binding.Target.ContractType {
	case STRIDEContractPersonProfile:
		r.organization.mu.RLock()
		defer r.organization.mu.RUnlock()
		current := r.organization.profiles[binding.Target.ID]
		return current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest
	case STRIDEContractOrganizationMembership:
		current, err := r.organization.Membership(binding.Target.ID)
		return err == nil && current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest
	case STRIDEContractOrganizationJoinRequest:
		current, err := r.organization.JoinRequest(binding.Target.ID)
		return err == nil && current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest
	case STRIDEContractContributionClaim:
		r.contribution.mu.RLock()
		defer r.contribution.mu.RUnlock()
		current := r.contribution.claims[binding.Target.ID]
		return current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest
	case STRIDEContractFieldReleaseApproval:
		r.contribution.mu.RLock()
		defer r.contribution.mu.RUnlock()
		current := r.contribution.approvals[binding.Target.ID]
		return current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest && r.contributionBindingControllerCurrentLocked(binding, current.OrganizationID, current.ApproverPartyID, current.ApproverRole)
	case STRIDEContractContributionAttestation:
		r.contribution.mu.RLock()
		defer r.contribution.mu.RUnlock()
		current := r.contribution.attestations[binding.Target.ID]
		return current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest && r.contributionBindingControllerCurrentLocked(binding, current.OrganizationID, "", "signing_issuer")
	case STRIDEContractPublishedContributionClaim:
		r.contribution.mu.RLock()
		current := r.contribution.publications[binding.Target.ID]
		r.contribution.mu.RUnlock()
		if current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest {
			return true
		}
		r.network.mu.Lock()
		networkCurrent := r.network.publications[binding.Target.ID]
		r.network.mu.Unlock()
		return networkCurrent.Header.Revision == binding.Target.Revision && networkCurrent.Header.ContentDigest == binding.Target.Digest
	case STRIDEContractTalentSearchGrant:
		r.network.mu.Lock()
		defer r.network.mu.Unlock()
		current := r.network.grants[binding.Target.ID]
		return current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest
	case STRIDEContractNetworkProfileProjection:
		r.network.mu.Lock()
		defer r.network.mu.Unlock()
		current := r.network.profiles[binding.Target.ID]
		return current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest
	case STRIDEContractContactRequest:
		r.network.mu.Lock()
		defer r.network.mu.Unlock()
		current := r.network.contacts[binding.Target.ID]
		return current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest
	case STRIDEContractNetworkBlock:
		r.network.mu.Lock()
		defer r.network.mu.Unlock()
		current := r.network.blocks[binding.Target.ID]
		return current.Header.Revision == binding.Target.Revision && current.Header.ContentDigest == binding.Target.Digest
	default:
		return false
	}
}

func (r *StrideE10ProductLiveRuntime) contributionBindingControllerCurrentLocked(binding StrideE10LiveActionBinding, organizationID, partyID, objectRole string) bool {
	if binding.ContributionAssertion == nil {
		return !oneOf(binding.Type, "contribution-named-party-decision", "contribution-attestation-revoke")
	}
	grant, ok := r.contribution.grants[binding.ContributionAssertion.GrantID]
	if !ok || grant.Controller != binding.ContributionAssertion.Controller {
		return false
	}
	if binding.Type == "contribution-named-party-decision" {
		return objectRole == "named_party" && grant.Role == "named_party" && grant.PartyID == partyID && grant.Controller.PrincipalID == binding.PersonID
	}
	if binding.Type == "contribution-attestation-revoke" {
		return grant.Role == "signing_issuer" && grant.OrganizationID == organizationID && grant.Controller.PrincipalID == binding.PersonID
	}
	return true
}

func (r *StrideE10ProductLiveRuntime) executeBoundAction(ctx context.Context, principal StrideE10ProductPrincipal, command StrideE10ProductCommand, binding StrideE10LiveActionBinding) (bool, error) {
	now := r.now().UTC()
	contributionAt := strideE10BoundContributionPostTime(binding, now)
	switch binding.Type {
	case "profile-update":
		profile := binding.Profile
		if profile == nil {
			built, err := r.buildProfileUpdate(principal.PersonID, command, now)
			if err != nil {
				return false, err
			}
			profile = &built
		}
		if profile.Header.Revision < 1 {
			return false, ErrStrideE10Invalid
		}
		return false, strideE10LiveError(r.organization.PutSelfProfile(principal.PersonID, profile.Header.Revision-1, *profile))
	case "organization-member-profile-update":
		if binding.MemberProfile == nil || binding.MemberProfile.Header.Revision < 1 {
			return false, ErrStrideE10Invalid
		}
		expected := binding.MemberProfile.Header.Revision - 1
		return false, strideE10LiveError(r.organization.PutOrganizationMemberProfile(principal.OrganizationMembershipID, principal.OrganizationMembershipRev, expected, *binding.MemberProfile))
	case "organization-create":
		organization, owner, event := binding.Organization, binding.OwnerMembership, binding.AuditEvent
		if organization == nil || owner == nil || event == nil {
			builtOrganization, builtOwner, builtEvent, err := r.buildOrganizationCreate(principal.PersonID, command, now)
			if err != nil {
				return false, err
			}
			organization, owner, event = &builtOrganization, &builtOwner, &builtEvent
		}
		return false, strideE10LiveError(r.organization.CreateOrganization(principal.PersonID, *organization, *owner, *event))
	case "organization-join":
		request, event := binding.JoinRequest, binding.AuditEvent
		if request == nil || event == nil {
			builtRequest, builtEvent, err := r.buildOrganizationJoin(principal.PersonID, command, now)
			if err != nil {
				return false, err
			}
			request, event = &builtRequest, &builtEvent
		}
		return false, strideE10LiveError(r.organization.RequestJoin(*request, *event))
	case "organization-request-approve", "organization-request-deny":
		if binding.JoinDecision == nil || binding.AuditEvent == nil {
			return false, ErrStrideE10Invalid
		}
		return false, strideE10LiveError(r.organization.DecideJoin(principal.OrganizationMembershipID, principal.OrganizationMembershipRev, strideE10BoundAuthorityRevision(binding, command.ExpectedRevision), *binding.JoinDecision, binding.ApprovedMembership, *binding.AuditEvent))
	case "organization-leave":
		if binding.ReplacementMembership == nil || binding.AuditEvent == nil {
			return false, ErrStrideE10Invalid
		}
		err := r.organization.EndMembership(principal.OrganizationMembershipID, principal.OrganizationMembershipRev, binding.ReplacementMembership.Header.Revision-1, *binding.ReplacementMembership, *binding.AuditEvent)
		if err == nil {
			userSessionStore().destroyAllForMembershipRevision(principal.PersonID, principal.ActiveOrganizationID, principal.OrganizationMembershipID, principal.OrganizationMembershipRev)
		}
		return false, strideE10LiveError(err)
	case "organization-switch":
		session, event := binding.ActiveSession, binding.AuditEvent
		if session == nil || event == nil {
			builtSession, builtEvent, err := r.buildOrganizationSwitch(ctx, principal, command, binding, now)
			if err != nil {
				return false, err
			}
			session, event = &builtSession, &builtEvent
		}
		if err := r.organization.BindActiveSession(principal.ActiveOrganizationSessionRev, *session, *event); err != nil {
			return false, strideE10LiveError(err)
		}
		token, _ := ctx.Value(strideE10LiveSessionTokenKey{}).(string)
		if token == "" {
			return false, ErrStrideE10Denied
		}
		store := userSessionStore()
		currentRecord, current := store.lookupMemberRecordByHash(session.SessionSubjectDigest, r.now().UTC())
		var err error
		if current && strideE10SwitchSessionRecordMatches(currentRecord, binding) {
			// A retry may observe the in-memory postimage after the prior durable
			// write failed. Re-persist the exact row before considering the
			// compound operation committed.
			store.mu.Lock()
			store.persistLocked()
			store.mu.Unlock()
		} else {
			_, err = store.rebindActiveOrganization(token, principal.PersonID, session.OrganizationID, session.MembershipID, session.MembershipRevision, principal.ActiveOrganizationSessionRev, session.SessionRevision, func(personID, organizationID, membershipID string, revision int64) error {
				membership, membershipErr := r.organization.Membership(membershipID)
				if membershipErr != nil || membership.PersonID != personID || membership.OrganizationID != organizationID || membership.Header.Revision != revision || membership.Status != "active" {
					return ErrOrganizationAuthorityDenied
				}
				return nil
			})
		}
		if err == nil && !r.organizationSwitchPostimagesApplied(binding) {
			err = ErrOrganizationAuthorityConflict
		}
		return false, strideE10LiveError(err)
	case "contribution-subject-approve", "contribution-subject-dispute":
		if binding.ContributionAssertion == nil || binding.Target.ID == "" {
			return false, ErrStrideE10Invalid
		}
		assertion := r.liveContributionAssertion(*binding.ContributionAssertion, command, contributionAt)
		assertion.ExpectedRevision = strideE10BoundAuthorityRevision(binding, command.ExpectedRevision)
		_, err := r.contribution.SubjectReview(binding.Target.ID, binding.Type == "contribution-subject-dispute", assertion)
		return false, strideE10LiveError(err)
	case "contribution-organization-approve", "contribution-organization-deny":
		if binding.ContributionAssertion == nil || binding.Target.ID == "" {
			return false, ErrStrideE10Invalid
		}
		assertion := r.liveContributionAssertion(*binding.ContributionAssertion, command, contributionAt)
		assertion.ExpectedRevision = strideE10BoundAuthorityRevision(binding, command.ExpectedRevision)
		decision := map[bool]string{true: "approved", false: "denied"}[binding.Type == "contribution-organization-approve"]
		approval, _, err := r.contribution.DecideFieldApproval(binding.Target.ID, decision, assertion)
		if err == nil {
			err = r.network.InstallFieldApprovalAuthority(approval)
		}
		return false, strideE10LiveError(err)
	case "contribution-publish":
		if binding.ContributionAssertion == nil || binding.Publication == nil {
			return false, ErrStrideE10Invalid
		}
		assertion := r.liveContributionAssertion(*binding.ContributionAssertion, command, contributionAt)
		assertion.ExpectedRevision = 0
		published, err := r.contribution.Publish(*binding.Publication, assertion)
		if err != nil {
			return false, strideE10LiveError(err)
		}
		r.contribution.mu.RLock()
		attestations := make([]ContributionAttestation, 0, len(published.Attestations))
		for _, reference := range published.Attestations {
			attestation, ok := r.contribution.attestations[reference.ID]
			if !ok || attestation.Header.Revision != reference.Revision || attestation.Header.ContentDigest != reference.Digest {
				r.contribution.mu.RUnlock()
				return false, ErrStrideE10Denied
			}
			attestations = append(attestations, cloneContract(attestation))
		}
		r.contribution.mu.RUnlock()
		if err := r.installNetworkPublicationDependencies(published, attestations); err != nil {
			return false, strideE10LiveError(err)
		}
		return false, nil
	case "contribution-withdraw":
		if binding.ContributionAssertion == nil || binding.Target.ID == "" {
			return false, ErrStrideE10Invalid
		}
		assertion := r.liveContributionAssertion(*binding.ContributionAssertion, command, contributionAt)
		assertion.ExpectedRevision = strideE10BoundAuthorityRevision(binding, command.ExpectedRevision)
		publication, _, err := r.contribution.WithdrawPublication(binding.Target.ID, assertion)
		if err == nil {
			err = r.network.InstallPublicationAuthority(publication, nil)
		}
		return false, strideE10LiveError(err)
	case "network-draft-save", "network-publish", "network-pause":
		if binding.NetworkActor == nil || binding.NetworkProfile == nil || binding.NetworkProfile.Header.Revision < 1 {
			return false, ErrStrideE10Invalid
		}
		_, _, replayed, err := r.network.PutProfile(*binding.NetworkActor, *binding.NetworkProfile, binding.NetworkProfile.Header.Revision-1, sha256Hex([]byte(command.IdempotencyKey)))
		return replayed, strideE10LiveError(err)
	case "network-search-submit":
		if binding.NetworkSearch == nil {
			return false, ErrStrideE10Invalid
		}
		request := *binding.NetworkSearch
		request.IdempotencyKeyDigest = sha256Hex([]byte(command.IdempotencyKey))
		request.At = now
		_, replayed, err := r.network.Search(request)
		return replayed, strideE10LiveError(err)
	case "contact-send":
		if binding.ContactAdmission == nil {
			return false, ErrStrideE10Invalid
		}
		admission := *binding.ContactAdmission
		admission.IdempotencyKeyDigest = sha256Hex([]byte(command.IdempotencyKey))
		admission.At = now
		_, replayed, err := r.network.CreateContact(admission)
		return replayed, strideE10LiveError(err)
	case "contact-accept", "contact-decline", "contact-withdraw":
		if binding.ContactActor == nil || binding.Target.ID == "" {
			return false, ErrStrideE10Invalid
		}
		decision := map[string]string{"contact-accept": "accepted", "contact-decline": "declined", "contact-withdraw": "withdrawn"}[binding.Type]
		channelDigest := ""
		if decision == "accepted" {
			if !isHexDigest(binding.AcceptedChannelDigest) {
				return false, ErrStrideE10Invalid
			}
			channelDigest = binding.AcceptedChannelDigest
		}
		_, replayed, err := r.network.DecideContact(*binding.ContactActor, binding.Target.ID, strideE10BoundAuthorityRevision(binding, command.ExpectedRevision), decision, channelDigest, sha256Hex([]byte(command.IdempotencyKey)), now)
		return replayed, strideE10LiveError(err)
	case "network-block", "network-unblock":
		if binding.NetworkActor == nil || binding.Block == nil || binding.Block.Header.Revision < 1 {
			return false, ErrStrideE10Invalid
		}
		_, _, replayed, err := r.network.PutBlock(*binding.NetworkActor, *binding.Block, binding.Block.Header.Revision-1, sha256Hex([]byte(command.IdempotencyKey)))
		return replayed, strideE10LiveError(err)
	case "organization-member-role-change":
		if binding.ReplacementMembership == nil || binding.AuditEvent == nil {
			return false, ErrStrideE10Invalid
		}
		err := r.organization.ChangeMembershipRole(principal.OrganizationMembershipID, principal.OrganizationMembershipRev, binding.ReplacementMembership.Header.Revision-1, *binding.ReplacementMembership, *binding.AuditEvent)
		return false, strideE10LiveError(err)
	case "organization-ownership-transfer":
		if binding.PriorOwnerNext == nil || binding.NewOwnerNext == nil || binding.AuditEvent == nil {
			return false, ErrStrideE10Invalid
		}
		err := r.organization.TransferOwnership(principal.OrganizationMembershipID, principal.OrganizationMembershipRev, *binding.PriorOwnerNext, *binding.NewOwnerNext, *binding.AuditEvent)
		return false, strideE10LiveError(err)
	case "organization-recruiting-grant-create", "organization-recruiting-grant-revoke":
		if binding.TalentAssertion == nil || binding.TalentGrant == nil {
			return false, ErrStrideE10Invalid
		}
		expected := int64(0)
		if binding.Type == "organization-recruiting-grant-revoke" {
			if binding.Target.ContractType != STRIDEContractTalentSearchGrant || binding.Target.Revision < 1 {
				return false, ErrStrideE10Invalid
			}
			expected = binding.Target.Revision
		}
		_, _, replayed, err := r.network.PutTalentSearchGrant(*binding.TalentAssertion, *binding.TalentGrant, expected, sha256Hex([]byte(command.IdempotencyKey)))
		return replayed, strideE10LiveError(err)
	case "organization-member-revoke":
		if binding.ReplacementMembership == nil || binding.AuditEvent == nil || binding.Target.ContractType != STRIDEContractOrganizationMembership {
			return false, ErrStrideE10Invalid
		}
		err := r.organization.EndMembership(principal.OrganizationMembershipID, principal.OrganizationMembershipRev, binding.Target.Revision, *binding.ReplacementMembership, *binding.AuditEvent)
		if err == nil {
			userSessionStore().destroyAllForMembershipRevision(binding.ReplacementMembership.PersonID, binding.ReplacementMembership.OrganizationID, binding.ReplacementMembership.Header.ID, binding.Target.Revision)
		}
		return false, strideE10LiveError(err)
	case "organization-request-expire", "organization-request-cancel":
		if binding.JoinDecision == nil || binding.AuditEvent == nil || binding.Target.ContractType != STRIDEContractOrganizationJoinRequest {
			return false, ErrStrideE10Invalid
		}
		return false, strideE10LiveError(r.organization.CloseJoinRequest(binding.Target.Revision, *binding.JoinDecision, *binding.AuditEvent))
	case "contribution-named-party-decision":
		if binding.ContributionAssertion == nil || binding.Target.ContractType != STRIDEContractFieldReleaseApproval || !oneOf(binding.Decision, "approved", "denied") {
			return false, ErrStrideE10Invalid
		}
		assertion := r.liveContributionAssertion(*binding.ContributionAssertion, command, contributionAt)
		assertion.ExpectedRevision = binding.Target.Revision
		approval, _, err := r.contribution.DecideFieldApproval(binding.Target.ID, binding.Decision, assertion)
		if err == nil {
			err = r.network.InstallFieldApprovalAuthority(approval)
		}
		return false, strideE10LiveError(err)
	case "contribution-attestation-revoke":
		if binding.ContributionAssertion == nil || binding.Target.ContractType != STRIDEContractContributionAttestation {
			return false, ErrStrideE10Invalid
		}
		assertion := r.liveContributionAssertion(*binding.ContributionAssertion, command, contributionAt)
		assertion.ExpectedRevision = binding.Target.Revision
		attestation, _, err := r.contribution.RevokeAttestation(binding.Target.ID, assertion)
		if err == nil {
			err = r.network.InstallAttestationAuthority(attestation)
		}
		return false, strideE10LiveError(err)
	case "network-profile-off", "network-searchable-fields-update":
		if binding.NetworkActor == nil || binding.NetworkProfile == nil || binding.Target.ContractType != STRIDEContractNetworkProfileProjection {
			return false, ErrStrideE10Invalid
		}
		if binding.Type == "network-profile-off" && (binding.NetworkProfile.State != "off" || binding.NetworkProfile.Discoverability != "unlisted") {
			return false, ErrStrideE10Invalid
		}
		r.network.mu.Lock()
		current := cloneNetworkProjection(r.network.profiles[binding.Target.ID])
		r.network.mu.Unlock()
		if binding.NetworkProfile.Header.ID != binding.Target.ID || binding.NetworkProfile.SubjectPersonID != principal.PersonID || binding.NetworkProfile.Controller != *binding.NetworkActor || binding.NetworkActor.PrincipalID != principal.PersonID || current.Header.Revision != binding.Target.Revision || current.Header.ContentDigest != binding.Target.Digest || current.SubjectPersonID != principal.PersonID || current.Controller != *binding.NetworkActor {
			return false, ErrStrideE10NotFound
		}
		_, purge, replayed, err := r.network.PutProfile(*binding.NetworkActor, *binding.NetworkProfile, binding.Target.Revision, sha256Hex([]byte(command.IdempotencyKey)))
		if err == nil && purge != nil {
			r.recordNetworkPurge(principal.PersonID, purge)
		}
		return replayed, strideE10LiveError(err)
	case "contribution-correct":
		if binding.ContributionAssertion == nil || binding.CorrectedClaim == nil {
			return false, ErrStrideE10Invalid
		}
		assertion := *binding.ContributionAssertion
		assertion.ExpectedRevision = strideE10BoundAuthorityRevision(binding, command.ExpectedRevision)
		assertion.IdempotencyKeyDigest = sha256Hex([]byte(command.IdempotencyKey))
		assertion.At = contributionAt
		claim, err := r.contribution.CorrectClaim(binding.Target.ID, *binding.CorrectedClaim, assertion)
		if err == nil {
			err = r.network.InstallClaimAuthority(claim)
		}
		return false, strideE10LiveError(err)
	case "contribution-revoke":
		if binding.ContributionAssertion == nil || !strideIdentifier(binding.Target.ID) {
			return false, ErrStrideE10Invalid
		}
		assertion := *binding.ContributionAssertion
		assertion.ExpectedRevision = strideE10BoundAuthorityRevision(binding, command.ExpectedRevision)
		assertion.IdempotencyKeyDigest = sha256Hex([]byte(command.IdempotencyKey))
		assertion.At = contributionAt
		claim, _, err := r.contribution.RevokeClaim(binding.Target.ID, assertion)
		if err == nil {
			err = r.network.InstallClaimAuthority(claim)
		}
		return false, strideE10LiveError(err)
	case "network-profile-delete":
		if binding.NetworkActor == nil || binding.NetworkProfile == nil {
			return false, ErrStrideE10Invalid
		}
		_, purge, replayed, err := r.network.PutProfile(*binding.NetworkActor, *binding.NetworkProfile, strideE10BoundAuthorityRevision(binding, binding.NetworkProfile.Header.Revision-1), sha256Hex([]byte(command.IdempotencyKey)))
		if err == nil && purge != nil {
			r.recordNetworkPurge(principal.PersonID, purge)
		}
		return replayed, strideE10LiveError(err)
	case "work-record-export", "network-profile-export":
		if binding.ExportReceipt == nil || len(binding.ExportBody) == 0 || binding.ExportReceipt.PersonID != principal.PersonID || binding.ExportReceipt.Surface != binding.Surface || binding.ExportReceipt.PackageDigest != sha256Hex(binding.ExportBody) {
			return false, ErrStrideE10Invalid
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		key := principal.PersonID + "\x00" + binding.Surface + "\x00" + sha256Hex([]byte(command.IdempotencyKey))
		if _, ok := r.exports[key]; ok {
			return true, nil
		}
		receipt := *binding.ExportReceipt
		r.exports[key] = receipt
		r.packages[receipt.ID] = strideE10ExportPackage{Receipt: receipt, Body: append(json.RawMessage(nil), binding.ExportBody...)}
		return false, nil
	case "work-record-delete":
		record, err := fenceStrideE10PortableAuthorities(r.contribution, r.network, principal.PersonID, now)
		if err != nil {
			return false, err
		}
		r.mu.Lock()
		for key, receipt := range r.exports {
			if receipt.PersonID != principal.PersonID {
				continue
			}
			record.RevokedExportIDs = append(record.RevokedExportIDs, receipt.ID)
			delete(r.packages, receipt.ID)
			delete(r.exports, key)
		}
		r.mu.Unlock()
		sort.Strings(record.RevokedExportIDs)
		r.portableStore.Save(record)
		return false, nil
	default:
		return false, ErrStrideE10NotFound
	}
}

// installNetworkPublicationDependencies transfers only exact current,
// body-minimized authority revisions into the network kernel. The order is
// deliberate: a publication can never become searchable before its governed
// claim and every required field approval are installed and current.
func (r *StrideE10ProductLiveRuntime) installNetworkPublicationDependencies(publication PublishedContributionClaim, attestations []ContributionAttestation) error {
	for _, attestation := range attestations {
		r.contribution.mu.RLock()
		claim, claimOK := r.contribution.claims[attestation.Claim.ID]
		approvals := make([]FieldReleaseApproval, 0)
		approvalOK := true
		for _, field := range attestation.ReleasedFields {
			for _, ref := range field.ApprovalRefs {
				approval, ok := r.contribution.approvals[ref.ID]
				if !ok || approval.Header.Revision != ref.Revision || approval.Header.ContentDigest != ref.Digest {
					approvalOK = false
					break
				}
				approvals = append(approvals, cloneContract(approval))
			}
		}
		r.contribution.mu.RUnlock()
		if !claimOK || claim.Header.Revision != attestation.Claim.Revision || claim.Header.ContentDigest != attestation.Claim.Digest || !approvalOK {
			return ErrNetworkAuthorityDenied
		}
		if err := r.network.InstallClaimAuthority(cloneContract(claim)); err != nil {
			return err
		}
		for _, approval := range approvals {
			if err := r.network.InstallFieldApprovalAuthority(approval); err != nil {
				return err
			}
		}
		if err := r.network.InstallAttestationAuthority(attestation); err != nil {
			return err
		}
	}
	return r.network.InstallPublicationAuthority(publication, attestations)
}

func strideE10PortableExportProjection(projection map[string]any) map[string]any {
	result := map[string]any{"availability": projection["availability"], "surface": projection["surface"], "revision": projection["revision"]}
	rawItems, _ := projection["items"].([]map[string]any)
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		if raw["title"] == "Authorized action" || raw["kind"] == "export-receipt" || raw["kind"] == "purge-receipt" {
			continue
		}
		item := cloneStrideE10LiveValue(raw).(map[string]any)
		delete(item, "actions")
		items = append(items, item)
	}
	result["items"] = items
	return result
}

func (r *StrideE10ProductLiveRuntime) liveContributionAssertion(base ContributionAuthorityAssertion, command StrideE10ProductCommand, now time.Time) ContributionAuthorityAssertion {
	base.ExpectedRevision = command.ExpectedRevision
	base.IdempotencyKeyDigest = sha256Hex([]byte(command.IdempotencyKey))
	base.At = now
	return base
}

func strideE10BoundAuthorityRevision(binding StrideE10LiveActionBinding, fallback int64) int64 {
	if binding.Target.ID != "" && binding.Target.Revision >= 1 {
		return binding.Target.Revision
	}
	return fallback
}

func strideE10LiveCommandValues(command StrideE10ProductCommand) (map[string]any, error) {
	var envelope struct {
		Values map[string]any `json:"values"`
	}
	if json.Unmarshal(command.Body, &envelope) != nil || envelope.Values == nil {
		return nil, ErrStrideE10Invalid
	}
	return envelope.Values, nil
}

func strideE10LiveStringList(value any) []string {
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, _ := item.(string)
		result = append(result, text)
	}
	return result
}

func strideE10ApplyNetworkDraftValues(fields []NetworkPublishedField, values map[string]any) []NetworkPublishedField {
	result := append([]NetworkPublishedField(nil), fields...)
	upsert := func(key string, value any) {
		encoded, _ := json.Marshal(value)
		field := NetworkPublishedField{FieldKey: key, ValueDigest: sha256Hex(encoded), VisibleValue: append(json.RawMessage(nil), encoded...), EvidenceLabel: "self_described"}
		for index := range result {
			if result[index].FieldKey == key {
				result[index] = field
				return
			}
		}
		result = append(result, field)
	}
	if intro, ok := values["intro"].(string); ok {
		upsert("bio", intro)
	}
	if _, ok := values["workModes"]; ok {
		upsert("work_mode", strideE10LiveStringList(values["workModes"]))
	}
	if _, ok := values["openTo"]; ok {
		upsert("open_to", strideE10LiveStringList(values["openTo"]))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FieldKey < result[j].FieldKey })
	return result
}

func strideE10FilterNetworkFields(fields []NetworkPublishedField, selected []string) []NetworkPublishedField {
	allowed := map[string]bool{}
	includeVerified := false
	for _, value := range selected {
		switch value {
		case "display_name", "pronouns", "bio", "open_to":
			allowed[value] = true
		case "work_modes":
			allowed["work_mode"] = true
		case "contribution_problem_classes":
			allowed["problem_class"] = true
		case "contribution_roles":
			allowed["contribution_role"] = true
		case "verified_contributions":
			includeVerified = true
		}
	}
	result := make([]NetworkPublishedField, 0, len(fields))
	for _, field := range fields {
		if allowed[field.FieldKey] || includeVerified && field.Claim != nil {
			result = append(result, field)
		}
	}
	return result
}

func strideE10LiveHeader(contractType STRIDEContractType, tenantID, id string, revision int64, digestSeed string, at time.Time) STRIDEContractHeader {
	return STRIDEContractHeader{TenantID: tenantID, ID: id, Revision: revision, SchemaVersion: STRIDEContractSchemaVersion, ContractType: contractType, ContentDigest: sha256Hex([]byte(digestSeed)), CreatedAt: at.UTC()}
}

func (r *StrideE10ProductLiveRuntime) buildProfileUpdate(personID string, command StrideE10ProductCommand, at time.Time) (PersonProfile, error) {
	values, err := strideE10LiveCommandValues(command)
	if err != nil {
		return PersonProfile{}, err
	}
	view, err := r.organization.ReadStrideE10SelfOrganizationView(personID)
	if err != nil {
		return PersonProfile{}, ErrStrideE10NotFound
	}
	profile := PersonProfile{PersonID: personID, Status: "active", UpdatedAt: at.UTC()}
	revision := int64(1)
	if view.Profile != nil {
		profile = clonePersonProfile(*view.Profile)
		revision = profile.Header.Revision + 1
		profile.UpdatedAt = at.UTC()
	}
	if value, ok := values["displayName"].(string); ok {
		profile.DisplayName = value
	}
	if value, ok := values["pronouns"].(string); ok {
		profile.Pronouns = value
	}
	if value, ok := values["bio"].(string); ok {
		profile.Bio = value
	}
	if _, ok := values["workModes"]; ok {
		profile.WorkModes = strideE10LiveStringList(values["workModes"])
	}
	if _, ok := values["openTo"]; ok {
		profile.OpenTo = strideE10LiveStringList(values["openTo"])
		profile.OpenToEnabled = len(profile.OpenTo) > 0
	}
	encoded, _ := json.Marshal(values)
	profile.Header = strideE10LiveHeader(STRIDEContractPersonProfile, STRIDEGlobalPersonTenant, personID, revision, personID+"\x00profile\x00"+string(encoded), at)
	if profile.Validate() != nil {
		return PersonProfile{}, ErrStrideE10Invalid
	}
	return profile, nil
}

func (r *StrideE10ProductLiveRuntime) buildOrganizationCreate(personID string, command StrideE10ProductCommand, at time.Time) (Organization, OrganizationMembership, OrganizationAuditEvent, error) {
	values, err := strideE10LiveCommandValues(command)
	if err != nil {
		return Organization{}, OrganizationMembership{}, OrganizationAuditEvent{}, err
	}
	name, _ := values["name"].(string)
	slug, _ := values["slug"].(string)
	seed := personID + "\x00" + slug + "\x00" + command.IdempotencyKey
	organizationID := "org_" + sha256Hex([]byte(seed))[:24]
	membershipID := "membership_" + sha256Hex([]byte(seed + "\x00owner"))[:24]
	organization := Organization{Header: strideE10LiveHeader(STRIDEContractOrganization, STRIDEGlobalPersonTenant, organizationID, 1, seed+"\x00organization", at), Name: name, Slug: slug, Status: "active", Discoverability: "private", CreatorPersonID: personID, PolicyRevision: 1, CreatedAt: at.UTC(), UpdatedAt: at.UTC()}
	owner := OrganizationMembership{Header: strideE10LiveHeader(STRIDEContractOrganizationMembership, organizationID, membershipID, 1, seed+"\x00membership", at), PersonID: personID, OrganizationID: organizationID, Role: "owner", Status: "active", GrantedAt: at.UTC()}
	event := strideE10LiveOrganizationAudit(organizationID, personID, "", 0, "", "create", 0, 1, command.IdempotencyKey, at)
	if organization.Validate() != nil || owner.Validate() != nil || event.Validate() != nil {
		return Organization{}, OrganizationMembership{}, OrganizationAuditEvent{}, ErrStrideE10Invalid
	}
	return organization, owner, event, nil
}

func (r *StrideE10ProductLiveRuntime) buildOrganizationJoin(personID string, command StrideE10ProductCommand, at time.Time) (OrganizationJoinRequest, OrganizationAuditEvent, error) {
	values, err := strideE10LiveCommandValues(command)
	if err != nil {
		return OrganizationJoinRequest{}, OrganizationAuditEvent{}, err
	}
	joinCode, _ := values["joinCode"].(string)
	r.mu.RLock()
	organizationID := r.joinCodes[sha256Hex([]byte(joinCode))]
	r.mu.RUnlock()
	if !strideIdentifier(organizationID) {
		return OrganizationJoinRequest{}, OrganizationAuditEvent{}, ErrStrideE10NotFound
	}
	seed := personID + "\x00" + organizationID + "\x00" + command.IdempotencyKey
	requestID := "join_" + sha256Hex([]byte(seed))[:24]
	request := OrganizationJoinRequest{Header: strideE10LiveHeader(STRIDEContractOrganizationJoinRequest, organizationID, requestID, 1, seed+"\x00request", at), PersonID: personID, OrganizationID: organizationID, Status: "pending", RequestedAt: at.UTC(), ExpiresAt: at.UTC().Add(7 * 24 * time.Hour)}
	event := strideE10LiveOrganizationAudit(organizationID, personID, "", 0, personID, "request", 0, 1, command.IdempotencyKey, at)
	if request.Validate() != nil || event.Validate() != nil {
		return OrganizationJoinRequest{}, OrganizationAuditEvent{}, ErrStrideE10Invalid
	}
	return request, event, nil
}

func (r *StrideE10ProductLiveRuntime) buildOrganizationSwitch(ctx context.Context, principal StrideE10ProductPrincipal, command StrideE10ProductCommand, binding StrideE10LiveActionBinding, at time.Time) (ActiveOrganizationSession, OrganizationAuditEvent, error) {
	if binding.Target.ContractType != STRIDEContractOrganizationMembership {
		return ActiveOrganizationSession{}, OrganizationAuditEvent{}, ErrStrideE10Invalid
	}
	membership, err := r.organization.Membership(binding.Target.ID)
	if err != nil || membership.PersonID != principal.PersonID || membership.Status != "active" || membership.Header.Revision != binding.Target.Revision || membership.Header.ContentDigest != binding.Target.Digest {
		return ActiveOrganizationSession{}, OrganizationAuditEvent{}, ErrStrideE10NotFound
	}
	token, _ := ctx.Value(strideE10LiveSessionTokenKey{}).(string)
	if token == "" {
		return ActiveOrganizationSession{}, OrganizationAuditEvent{}, ErrStrideE10Denied
	}
	subjectDigest := hashResetToken(token)
	record, ok := userSessionStore().lookupMemberRecordByHash(subjectDigest, at.UTC())
	if !ok || record.PersonID != principal.PersonID || !isHexDigest(record.AccountSubjectDigest) || record.AuthorityGeneration < 1 {
		return ActiveOrganizationSession{}, OrganizationAuditEvent{}, ErrStrideE10Denied
	}
	nextRevision := principal.ActiveOrganizationSessionRev + 1
	seed := principal.PersonID + "\x00" + membership.OrganizationID + "\x00" + fmt.Sprint(nextRevision) + "\x00" + command.IdempotencyKey
	session := ActiveOrganizationSession{Header: strideE10LiveHeader(STRIDEContractActiveOrganizationSession, STRIDEGlobalPersonTenant, "active_session_"+subjectDigest[:24], nextRevision, seed+"\x00session", at), SessionSubjectDigest: subjectDigest, PersonID: principal.PersonID, OrganizationID: membership.OrganizationID, MembershipID: membership.Header.ID, MembershipRevision: membership.Header.Revision, SessionRevision: nextRevision, Status: "active", BoundAt: at.UTC(), ExpiresAt: record.Expires.UTC()}
	event := strideE10LiveOrganizationAudit(membership.OrganizationID, principal.PersonID, membership.Header.ID, membership.Header.Revision, principal.PersonID, "switch", principal.ActiveOrganizationSessionRev, nextRevision, command.IdempotencyKey, at)
	if session.Validate() != nil || event.Validate() != nil {
		return ActiveOrganizationSession{}, OrganizationAuditEvent{}, ErrStrideE10Invalid
	}
	return session, event, nil
}

func strideE10LiveOrganizationAudit(organizationID, actorPersonID, actorMembershipID string, actorMembershipRevision int64, subjectPersonID, action string, priorRevision, newRevision int64, idempotencyKey string, at time.Time) OrganizationAuditEvent {
	seed := organizationID + "\x00" + action + "\x00" + idempotencyKey
	return OrganizationAuditEvent{Header: strideE10LiveHeader(STRIDEContractOrganizationAuditEvent, organizationID, "audit_"+sha256Hex([]byte(seed))[:24], 1, seed+"\x00audit", at), OrganizationID: organizationID, ActorPersonID: actorPersonID, ActorMembershipID: actorMembershipID, ActorMembershipRevision: actorMembershipRevision, SubjectPersonID: subjectPersonID, Action: action, PriorRevision: priorRevision, NewRevision: newRevision, CorrelationID: "correlation_" + sha256Hex([]byte(seed))[:24], IdempotencyKeyDigest: sha256Hex([]byte(idempotencyKey)), OccurredAt: at.UTC()}
}

func (r *StrideE10ProductLiveRuntime) recordNetworkPurge(personID string, purge *DerivedPurgeReceipt) {
	if purge == nil {
		return
	}
	stores := make([]string, 0, len(purge.Stores))
	for _, store := range purge.Stores {
		stores = append(stores, store.Store)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.purges[personID+"\x00network-preview"] = map[string]any{"kind": "purge-receipt", "status": purge.State, "receiptId": purge.Header.ID, "stores": stores}
}

func strideE10LiveError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrOrganizationAuthorityConflict), errors.Is(err, ErrOrganizationCapacity), errors.Is(err, ErrOrganizationFinalOwner), errors.Is(err, ErrContributionAuthorityConflict), errors.Is(err, ErrNetworkAuthorityConflict), errors.Is(err, ErrNetworkIdempotencyConflict):
		return ErrStrideE10Conflict
	case errors.Is(err, ErrOrganizationAuthorityDenied), errors.Is(err, ErrContributionAuthorityDenied), errors.Is(err, ErrNetworkAuthorityDenied):
		return ErrStrideE10Denied
	case errors.Is(err, ErrOrganizationAuthorityNotFound), errors.Is(err, ErrContributionAuthorityNotFound), errors.Is(err, ErrNetworkAuthorityNotFound):
		return ErrStrideE10NotFound
	default:
		return ErrStrideE10Invalid
	}
}

func (r *StrideE10ProductLiveRuntime) project(principal StrideE10ProductPrincipal, surface string) (map[string]any, error) {
	return r.projectTarget(principal, surface, "")
}

func (r *StrideE10ProductLiveRuntime) mintProjectionActions(principal StrideE10ProductPrincipal, surface string) {
	now := r.now().UTC()
	expires := now.Truncate(5 * time.Minute).Add(5 * time.Minute)
	issueBinding := func(action string, revision int64, target STRIDEReference, specific StrideE10LiveActionBinding) {
		seed := principal.PersonID + "\x00" + surface + "\x00" + action + "\x00" + target.ID + "\x00" + fmt.Sprint(revision) + "\x00" + fmt.Sprint(principal.OrganizationMembershipRev) + "\x00" + fmt.Sprint(principal.ActiveOrganizationSessionRev) + "\x00" + fmt.Sprint(expires.Unix())
		binding := cloneStrideE10LiveActionBinding(specific)
		binding.ID, binding.Type, binding.Surface, binding.PersonID = "action_"+sha256Hex([]byte(seed))[:24], action, surface, principal.PersonID
		binding.OrganizationID, binding.ExpectedRevision, binding.ExpiresAt, binding.Target = principal.ActiveOrganizationID, revision, expires, target
		binding.MembershipRevision, binding.SessionRevision = principal.OrganizationMembershipRev, principal.ActiveOrganizationSessionRev
		if !strideE10MobileActions[action].requireOrg {
			binding.OrganizationID, binding.MembershipRevision, binding.SessionRevision = "", 0, 0
		}
		_ = r.BindAction(binding)
	}
	issue := func(action string, revision int64, target STRIDEReference) {
		issueBinding(action, revision, target, StrideE10LiveActionBinding{})
	}
	switch surface {
	case "profile":
		view, err := r.organization.ReadStrideE10SelfOrganizationView(principal.PersonID)
		if err != nil {
			return
		}
		target := STRIDEReference{}
		revision := int64(1)
		if view.Profile != nil && view.Profile.Status == "active" {
			target = refForHeader(view.Profile.Header)
			revision = target.Revision
		}
		issue("profile-update", revision, target)
	case "organizations":
		view, err := r.organization.ReadStrideE10SelfOrganizationView(principal.PersonID)
		if err != nil {
			return
		}
		activeCount := r.organization.ActiveMembershipCount(principal.PersonID)
		if activeCount < 3 {
			issue("organization-create", int64(activeCount+1), STRIDEReference{})
			issue("organization-join", int64(len(view.JoinRequests)+1), STRIDEReference{})
		}
		for _, membership := range view.Memberships {
			if membership.Status != "active" {
				continue
			}
			if membership.OrganizationID != principal.ActiveOrganizationID {
				issue("organization-switch", membership.Header.Revision, refForHeader(membership.Header))
				continue
			}
			finalOwner := false
			if membership.Role == "owner" {
				admin, err := r.organization.ReadStrideE10OrganizationAdminView(StrideE10AuthorityViewer{PersonID: principal.PersonID, OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID, MembershipRevision: principal.OrganizationMembershipRev})
				finalOwner = err == nil && countStrideE10Owners(admin.Memberships) == 1
			}
			if finalOwner {
				continue
			}
			replacement := cloneOrganizationMembership(membership)
			replacement.Header = nextAuthorityHeader(replacement.Header, "leave", now)
			replacement.Status = "departed"
			replacement.EndedAt = &now
			event := strideE10LiveOrganizationAudit(membership.OrganizationID, principal.PersonID, membership.Header.ID, membership.Header.Revision, principal.PersonID, "leave", membership.Header.Revision, replacement.Header.Revision, "minted_"+membership.Header.ContentDigest, now)
			seed := principal.PersonID + "\x00organizations\x00organization-leave\x00" + membership.Header.ID + "\x00" + fmt.Sprint(membership.Header.Revision) + "\x00" + fmt.Sprint(expires.Unix())
			_ = r.BindAction(StrideE10LiveActionBinding{ID: "action_" + sha256Hex([]byte(seed))[:24], Type: "organization-leave", Surface: "organizations", PersonID: principal.PersonID, OrganizationID: principal.ActiveOrganizationID, ExpectedRevision: membership.Header.Revision, ExpiresAt: expires, Target: refForHeader(membership.Header), MembershipRevision: principal.OrganizationMembershipRev, SessionRevision: principal.ActiveOrganizationSessionRev, ReplacementMembership: &replacement, AuditEvent: &event})
		}
	case "organization-people":
		viewer := StrideE10AuthorityViewer{PersonID: principal.PersonID, OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID, MembershipRevision: principal.OrganizationMembershipRev}
		view, err := r.organization.ReadStrideE10OrganizationAdminView(viewer)
		if err != nil {
			return
		}
		actor, actorErr := r.organization.Membership(principal.OrganizationMembershipID)
		if actorErr != nil || !oneOf(actor.Role, "owner", "admin") {
			return
		}
		for _, membership := range view.Memberships {
			if membership.Status != "active" || membership.Header.ID == actor.Header.ID || membership.Role == "owner" {
				continue
			}
			issueBinding("organization-member-revoke", membership.Header.Revision, refForHeader(membership.Header), StrideE10LiveActionBinding{})
			if actor.Role == "owner" {
				issueBinding("organization-member-role-change", membership.Header.Revision, refForHeader(membership.Header), StrideE10LiveActionBinding{})
				issueBinding("organization-ownership-transfer", membership.Header.Revision, refForHeader(membership.Header), StrideE10LiveActionBinding{})
			}
		}
	case "organization-requests":
		viewer := StrideE10AuthorityViewer{PersonID: principal.PersonID, OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID, MembershipRevision: principal.OrganizationMembershipRev}
		view, err := r.organization.ReadStrideE10OrganizationAdminView(viewer)
		if err != nil {
			return
		}
		for _, request := range view.JoinRequests {
			if request.Status != "pending" || !now.Before(request.ExpiresAt) {
				continue
			}
			if r.organization.ActiveMembershipCount(request.PersonID) < 3 {
				issueBinding("organization-request-approve", request.Header.Revision, refForHeader(request.Header), StrideE10LiveActionBinding{})
			}
			issueBinding("organization-request-deny", request.Header.Revision, refForHeader(request.Header), StrideE10LiveActionBinding{})
		}
	case "work-record":
		scope, ok := r.currentContributionViewScope(principal, false)
		if !ok {
			return
		}
		view, err := r.contribution.ReadStrideE10ContributionView(scope)
		if err != nil {
			return
		}
		assertion := ContributionAuthorityAssertion{GrantID: scope.GrantID, Controller: scope.Controller, ExpectedRevision: 0, IdempotencyKeyDigest: sha256Hex([]byte("minted")), At: now}
		for _, claim := range view.Claims {
			if claim.State == "candidate" && claim.SubjectReview == nil {
				issueBinding("contribution-subject-approve", claim.Header.Revision, refForHeader(claim.Header), StrideE10LiveActionBinding{ContributionAssertion: &assertion})
			}
			if claim.State == "subject_review" && claim.SubjectReview != nil {
				issueBinding("contribution-subject-dispute", claim.Header.Revision, refForHeader(claim.Header), StrideE10LiveActionBinding{ContributionAssertion: &assertion})
			}
		}
		for _, publication := range view.Publications {
			if publication.State == "published" {
				publisherScope, publisherOK := r.currentContributionGrant(principal, "person_publisher", "")
				if publisherOK && publisherScope.Controller == publication.Controller {
					publisherAssertion := ContributionAuthorityAssertion{GrantID: publisherScope.GrantID, Controller: publisherScope.Controller, ExpectedRevision: publication.Header.Revision, IdempotencyKeyDigest: sha256Hex([]byte("minted")), At: now}
					issueBinding("contribution-withdraw", publication.Header.Revision, refForHeader(publication.Header), StrideE10LiveActionBinding{ContributionAssertion: &publisherAssertion})
				}
			}
		}
		publisherScope, publisherOK := r.currentContributionGrant(principal, "person_publisher", "")
		if publisherOK {
			for _, attestation := range view.Attestations {
				if attestation.State != "active" || strideE10ViewHasPublishedAttestation(view.Publications, attestation.Header.ID) {
					continue
				}
				seed := principal.PersonID + "\x00" + attestation.Header.ID + "\x00publish"
				publication := PublishedContributionClaim{Header: strideE10LiveHeader(STRIDEContractPublishedContributionClaim, STRIDEGlobalPersonTenant, "publication_"+sha256Hex([]byte(seed))[:24], 1, seed, now), SubjectPersonID: principal.PersonID, NarrativeDigest: sha256Hex([]byte(seed + "\x00narrative")), Attestations: []STRIDEReference{refForHeader(attestation.Header)}, ReleasedFieldsDigest: attestation.ReleasedFieldsDigest, Visibility: "signed_in_network", Controller: publisherScope.Controller, State: "published", StateChangedAt: now}
				if publication.Validate() != nil {
					continue
				}
				publisherAssertion := ContributionAuthorityAssertion{GrantID: publisherScope.GrantID, Controller: publisherScope.Controller, ExpectedRevision: 0, IdempotencyKeyDigest: sha256Hex([]byte("minted")), At: now}
				issueBinding("contribution-publish", attestation.Header.Revision, refForHeader(attestation.Header), StrideE10LiveActionBinding{ContributionAssertion: &publisherAssertion, Publication: &publication})
			}
		}
		issue("work-record-export", 1, STRIDEReference{})
		if len(view.Claims)+len(view.Publications) > 0 {
			issue("work-record-delete", 1, STRIDEReference{})
		}
	case "contribution-approvals":
		if scope, ok := r.currentContributionViewScope(principal, true); ok {
			viewer := StrideE10AuthorityViewer{PersonID: principal.PersonID, OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID, MembershipRevision: principal.OrganizationMembershipRev}
			if view, err := r.contribution.ReadStrideE10OrganizationContributionView(r.organization, viewer, scope); err == nil {
				assertion := ContributionAuthorityAssertion{GrantID: scope.GrantID, Controller: scope.Controller, ExpectedRevision: 0, IdempotencyKeyDigest: sha256Hex([]byte("minted")), At: now}
				for _, approval := range view.Approvals {
					if approval.State == "pending" && approval.ApproverRole == "organization" {
						issueBinding("contribution-organization-approve", approval.Header.Revision, refForHeader(approval.Header), StrideE10LiveActionBinding{ContributionAssertion: &assertion})
						issueBinding("contribution-organization-deny", approval.Header.Revision, refForHeader(approval.Header), StrideE10LiveActionBinding{ContributionAssertion: &assertion})
					}
				}
				for _, claim := range view.Claims {
					if !oneOf(claim.State, "revoked", "superseded") {
						issueBinding("contribution-revoke", claim.Header.Revision, refForHeader(claim.Header), StrideE10LiveActionBinding{ContributionAssertion: &assertion})
					}
					if oneOf(claim.State, "verified", "revalidation_required") {
						issueBinding("contribution-correct", claim.Header.Revision, refForHeader(claim.Header), StrideE10LiveActionBinding{ContributionAssertion: &assertion})
					}
				}
			}
		}
		for _, controllerScope := range r.currentContributionControllerScopes(principal.PersonID) {
			view, err := r.contribution.ReadStrideE10ControllerDecisionView(controllerScope)
			if err != nil {
				continue
			}
			assertion := ContributionAuthorityAssertion{GrantID: controllerScope.GrantID, Controller: controllerScope.Controller, IdempotencyKeyDigest: sha256Hex([]byte("minted")), At: now}
			for _, approval := range view.Approvals {
				if approval.State == "pending" && approval.ApproverRole == "named_party" && approval.ApproverPartyID == principal.PersonID && approval.Controller == controllerScope.Controller {
					issueBinding("contribution-named-party-decision", approval.Header.Revision, refForHeader(approval.Header), StrideE10LiveActionBinding{ContributionAssertion: &assertion})
				}
			}
			for _, attestation := range view.Attestations {
				if attestation.State == "active" && attestation.Issuer == controllerScope.Controller {
					issueBinding("contribution-attestation-revoke", attestation.Header.Revision, refForHeader(attestation.Header), StrideE10LiveActionBinding{ContributionAssertion: &assertion})
				}
			}
		}
	case "network-draft", "network-preview":
		view, err := r.network.ReadStrideE10NetworkPersonView(principal.PersonID)
		if err != nil {
			return
		}
		if surface == "network-preview" {
			issue("network-profile-export", 1, STRIDEReference{})
		}
		if surface == "network-draft" && len(view.Profiles) == 0 {
			r.network.mu.Lock()
			publications := make([]PublishedContributionClaim, 0)
			for _, publication := range r.network.publications {
				if publication.SubjectPersonID == principal.PersonID && publication.State == "published" && publication.Visibility == "signed_in_network" {
					publications = append(publications, cloneContract(publication))
				}
			}
			r.network.mu.Unlock()
			if len(publications) == 1 {
				actor := publications[0].Controller
				issueBinding("network-draft-save", 1, refForHeader(publications[0].Header), StrideE10LiveActionBinding{NetworkActor: &actor, Publication: &publications[0]})
			}
		}
		for _, profile := range view.Profiles {
			if profile.State == "deleted" {
				continue
			}
			actor := profile.Controller
			if surface == "network-draft" {
				issueBinding("network-draft-save", profile.Header.Revision, refForHeader(profile.Header), StrideE10LiveActionBinding{NetworkActor: &actor})
				continue
			}
			if oneOf(profile.State, "draft", "paused") {
				issueBinding("network-publish", profile.Header.Revision, refForHeader(profile.Header), StrideE10LiveActionBinding{NetworkActor: &actor})
			}
			if profile.State == "published" {
				issueBinding("network-pause", profile.Header.Revision, refForHeader(profile.Header), StrideE10LiveActionBinding{NetworkActor: &actor})
			}
			if oneOf(profile.State, "draft", "published", "paused") {
				issueBinding("network-profile-off", profile.Header.Revision, refForHeader(profile.Header), StrideE10LiveActionBinding{NetworkActor: &actor})
			}
			issueBinding("network-searchable-fields-update", profile.Header.Revision, refForHeader(profile.Header), StrideE10LiveActionBinding{NetworkActor: &actor})
			issueBinding("network-profile-delete", profile.Header.Revision, refForHeader(profile.Header), StrideE10LiveActionBinding{NetworkActor: &actor})
		}
	case "network-search":
		viewer := StrideE10AuthorityViewer{PersonID: principal.PersonID, OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID, MembershipRevision: principal.OrganizationMembershipRev}
		view, err := r.network.ReadStrideE10NetworkOrganizationView(viewer)
		if err != nil {
			return
		}
		for _, grant := range view.Grants {
			if grant.State == "active" && now.Before(grant.ExpiresAt) {
				issueBinding("network-search-submit", grant.Header.Revision, refForHeader(grant.Header), StrideE10LiveActionBinding{TalentGrant: &grant})
			}
		}
		for _, receipt := range view.SearchReceipts {
			for _, result := range receipt.Results {
				issueBinding("contact-send", result.Projection.Revision, result.Projection, StrideE10LiveActionBinding{TalentGrant: findStrideE10Grant(view.Grants, receipt.Grant.ID)})
			}
		}
	case "contact-inbox":
		view, err := r.network.ReadStrideE10NetworkPersonView(principal.PersonID)
		if err != nil {
			return
		}
		for _, contact := range view.Contacts {
			if contact.State != "pending" {
				continue
			}
			if contact.RecipientPersonID == principal.PersonID {
				actor := r.currentNetworkPersonController(principal.PersonID)
				if actor != nil {
					channel := sha256Hex([]byte("contact-channel\x00" + contact.Header.ID + "\x00" + contact.Header.ContentDigest))
					issueBinding("contact-accept", contact.Header.Revision, refForHeader(contact.Header), StrideE10LiveActionBinding{ContactActor: actor, AcceptedChannelDigest: channel})
					issueBinding("contact-decline", contact.Header.Revision, refForHeader(contact.Header), StrideE10LiveActionBinding{ContactActor: actor})
				}
			} else if contact.SenderPersonID == principal.PersonID && contact.SenderOrganizationID == principal.ActiveOrganizationID {
				actor := r.currentNetworkPersonController(principal.PersonID)
				if actor != nil {
					issueBinding("contact-withdraw", contact.Header.Revision, refForHeader(contact.Header), StrideE10LiveActionBinding{ContactActor: actor})
				}
			}
		}
	case "network-blocks":
		view, err := r.network.ReadStrideE10NetworkPersonView(principal.PersonID)
		if err != nil {
			return
		}
		for _, block := range view.Blocks {
			if block.State != "active" {
				continue
			}
			actor := block.Controller
			next := cloneContract(block)
			next.Header = nextAuthorityHeader(next.Header, "unblock", now)
			next.State, next.StateChangedAt = "withdrawn", now
			issueBinding("network-unblock", block.Header.Revision, refForHeader(block.Header), StrideE10LiveActionBinding{NetworkActor: &actor, Block: &next})
		}
		actor := r.currentNetworkPersonController(principal.PersonID)
		if actor != nil {
			for _, contact := range view.Contacts {
				targetPersonID := contact.SenderPersonID
				if targetPersonID == principal.PersonID {
					targetPersonID = contact.RecipientPersonID
				}
				if !strideIdentifier(targetPersonID) || targetPersonID == principal.PersonID || strideE10HasActivePersonBlock(view.Blocks, targetPersonID) {
					continue
				}
				seed := principal.PersonID + "\x00" + targetPersonID + "\x00block"
				block := NetworkBlock{Header: strideE10LiveHeader(STRIDEContractNetworkBlock, STRIDEGlobalPersonTenant, "block_"+sha256Hex([]byte(seed))[:24], 1, seed, now), BlockerPersonID: principal.PersonID, BlockedPersonID: targetPersonID, Controller: *actor, State: "active", StateChangedAt: now}
				if block.Validate() == nil {
					issueBinding("network-block", contact.Header.Revision, refForHeader(contact.Header), StrideE10LiveActionBinding{NetworkActor: actor, Block: &block})
				}
			}
		}
	case "organization-recruiting":
		viewer := StrideE10AuthorityViewer{PersonID: principal.PersonID, OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID, MembershipRevision: principal.OrganizationMembershipRev}
		view, err := r.network.ReadStrideE10NetworkRecruitingAdminView(r.organization, viewer)
		if err != nil {
			return
		}
		assertion, ok := r.currentTalentCapability(principal)
		if !ok {
			return
		}
		activeMembers := map[string]OrganizationMembership{}
		r.organization.mu.RLock()
		for _, membership := range r.organization.memberships {
			if membership.OrganizationID == principal.ActiveOrganizationID && membership.Status == "active" && membership.PersonID != principal.PersonID {
				activeMembers[membership.Header.ID] = cloneOrganizationMembership(membership)
			}
		}
		r.organization.mu.RUnlock()
		for _, grant := range view.Grants {
			if grant.State == "active" && now.Before(grant.ExpiresAt) {
				delete(activeMembers, grant.MembershipID)
				issueBinding("organization-recruiting-grant-revoke", grant.Header.Revision, refForHeader(grant.Header), StrideE10LiveActionBinding{TalentAssertion: &assertion})
			}
		}
		for _, membership := range activeMembers {
			issueBinding("organization-recruiting-grant-create", membership.Header.Revision, refForHeader(membership.Header), StrideE10LiveActionBinding{TalentAssertion: &assertion})
		}
	}
}

func (r *StrideE10ProductLiveRuntime) projectTarget(principal StrideE10ProductPrincipal, surface, targetID string) (map[string]any, error) {
	items := make([]map[string]any, 0)
	authorityItems, err := r.authorityProjectionItems(principal, surface, targetID)
	if err != nil {
		return nil, err
	}
	items = append(items, authorityItems...)
	r.mintProjectionActions(principal, surface)
	r.mu.RLock()
	_, portableDeleted := r.portableStore.Load(principal.PersonID)
	for _, binding := range r.actions {
		if r.actionUses[binding.ID] != "" {
			continue
		}
		if _, used, _ := r.operationStore.FindAction(principal.PersonID, binding.ID); used {
			continue
		}
		if portableDeleted && (binding.Surface == "work-record" || binding.Type == "network-profile-export") {
			continue
		}
		if binding.PersonID != principal.PersonID || binding.Surface != surface || !r.now().Before(binding.ExpiresAt) ||
			binding.OrganizationID != "" && binding.OrganizationID != principal.ActiveOrganizationID ||
			binding.MembershipRevision != 0 && binding.MembershipRevision != principal.OrganizationMembershipRev || binding.SessionRevision != 0 && binding.SessionRevision != principal.ActiveOrganizationSessionRev || !r.bindingCurrent(binding) {
			continue
		}
		if _, mobileAction := strideE10MobileActions[binding.Type]; !mobileAction {
			continue
		}
		items = append(items, map[string]any{"id": "authority-" + binding.ID, "title": "Authorized action", "status": "current", "actions": []map[string]any{{"id": binding.ID, "type": binding.Type, "label": strideE10LiveActionLabel(binding.Type), "expectedRevision": binding.ExpectedRevision}}})
	}
	for _, receipt := range r.exports {
		if receipt.PersonID != principal.PersonID || receipt.Surface != surface || !r.now().Before(receipt.ExpiresAt) {
			continue
		}
		items = append(items, map[string]any{"id": receipt.ID, "title": "Export package prepared", "status": "ready", "kind": "export-receipt", "detail": map[string]any{"kind": "export-receipt", "status": "ready", "packageDigest": receipt.PackageDigest, "expiresAt": receipt.ExpiresAt.Format(time.RFC3339)}})
	}
	if purge := r.purges[principal.PersonID+"\x00"+surface]; purge != nil {
		items = append(items, map[string]any{"id": purge["receiptId"], "title": "Deletion purge queued", "status": purge["status"], "kind": "purge-receipt", "detail": cloneStrideE10LiveValue(purge)})
	}
	r.mu.RUnlock()
	if deletion, ok := r.portableStore.Load(principal.PersonID); ok && (surface == "work-record" || surface == "network-preview") {
		for _, receipt := range deletion.PurgeReceipts {
			if surface == "network-preview" && receipt.Trigger.ContractType != STRIDEContractNetworkProfileProjection {
				continue
			}
			stores := make([]string, 0, len(receipt.Stores))
			for _, store := range receipt.Stores {
				stores = append(stores, store.Store)
			}
			items = append(items, map[string]any{"id": receipt.Header.ID, "title": "Deletion purge queued", "status": receipt.State, "kind": "purge-receipt", "detail": map[string]any{"kind": "purge-receipt", "status": receipt.State, "receiptId": receipt.Header.ID, "stores": stores}})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["id"].(string) < items[j]["id"].(string) })
	return map[string]any{"availability": "available", "surface": surface, "revision": int64(1), "items": items}, nil
}

func (r *StrideE10ProductLiveRuntime) authorityProjectionItems(principal StrideE10ProductPrincipal, surface, targetID string) ([]map[string]any, error) {
	items := make([]map[string]any, 0)
	viewer := StrideE10AuthorityViewer{PersonID: principal.PersonID, OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID, MembershipRevision: principal.OrganizationMembershipRev}
	switch surface {
	case "profile", "organizations":
		view, err := r.organization.ReadStrideE10SelfOrganizationView(principal.PersonID)
		if err != nil {
			return nil, strideE10LiveError(err)
		}
		if surface == "profile" && view.Profile != nil && view.Profile.Status == "active" {
			choices := make([]string, 0, len(view.Organizations))
			for _, organization := range view.Organizations {
				choices = append(choices, organization.Name)
			}
			items = append(items, map[string]any{"id": "self-profile", "title": view.Profile.DisplayName, "status": view.Profile.Status, "updatedAt": view.Profile.UpdatedAt.UTC().Format(time.RFC3339), "kind": "self-profile-detail", "detail": map[string]any{"kind": "self-profile-detail", "displayName": view.Profile.DisplayName, "pronouns": view.Profile.Pronouns, "bio": view.Profile.Bio, "workModes": append([]string{}, view.Profile.WorkModes...), "openTo": append([]string{}, view.Profile.OpenTo...), "openToEnabled": view.Profile.OpenToEnabled, "organizationChoices": choices}})
		}
		if surface == "organizations" {
			pending := 0
			for _, request := range view.JoinRequests {
				if request.Status == "pending" {
					pending++
				}
			}
			for _, membership := range view.Memberships {
				name := "Organization"
				for _, organization := range view.Organizations {
					if organization.Header.ID == membership.OrganizationID {
						name = organization.Name
						break
					}
				}
				items = append(items, map[string]any{"id": membership.Header.ID, "title": name, "status": map[bool]string{true: "current", false: membership.Status}[membership.OrganizationID == principal.ActiveOrganizationID], "context": membership.Role, "kind": "organization-summary", "detail": map[string]any{"kind": "organization-summary", "activeCount": r.organization.ActiveMembershipCount(principal.PersonID), "capacity": 3, "pendingCount": pending, "isCurrent": membership.OrganizationID == principal.ActiveOrganizationID, "role": membership.Role}})
			}
		}
	case "coworker-profile":
		if !strideIdentifier(targetID) {
			return nil, ErrStrideE10NotFound
		}
		profile, memberProfile, err := r.organization.ReadStrideE10CoworkerProfile(viewer, targetID)
		if err != nil {
			return nil, ErrStrideE10NotFound
		}
		membership, err := r.organization.Membership(memberProfile.MembershipID)
		if err != nil || membership.PersonID != targetID || membership.OrganizationID != principal.ActiveOrganizationID || membership.Status != "active" || membership.Header.Revision != memberProfile.MembershipRevision {
			return nil, ErrStrideE10NotFound
		}
		items = append(items, map[string]any{"id": targetID, "title": profile.DisplayName, "kind": "coworker-profile-detail", "detail": map[string]any{"kind": "coworker-profile-detail", "displayName": profile.DisplayName, "role": membership.Role, "title": memberProfile.Title, "team": memberProfile.Team, "joinedAt": memberProfile.JoinedAt.UTC().Format(time.RFC3339)}})
	case "work-record":
		identity, identityErr := r.organization.ReadStrideE10SelfOrganizationView(principal.PersonID)
		scope, ok := r.currentContributionViewScope(principal, false)
		if !ok {
			if identityErr != nil {
				return nil, ErrStrideE10NotFound
			}
			// A current person owns the private Work Record even before the first
			// contribution controller is issued. Render the closed empty section
			// vocabulary; do not invent a controller or expose contribution data.
			items = append(items, strideE10WorkRecordProjectionItems(StrideE10ContributionView{}, identity, nil)...)
			break
		}
		view, err := r.contribution.ReadStrideE10ContributionView(scope)
		if err != nil {
			return nil, ErrStrideE10NotFound
		}
		if identityErr != nil {
			identity = StrideE10OrganizationSelfView{}
		}
		items = append(items, strideE10WorkRecordProjectionItems(view, identity, r.contribution.FieldEligible)...)
	case "contribution-approvals":
		authorized := false
		if scope, ok := r.currentContributionViewScope(principal, true); ok {
			if view, err := r.contribution.ReadStrideE10OrganizationContributionView(r.organization, viewer, scope); err == nil {
				items = append(items, strideE10ContributionReviewProjectionItems(view)...)
				authorized = true
			}
		}
		for _, scope := range r.currentContributionControllerScopes(principal.PersonID) {
			view, err := r.contribution.ReadStrideE10ControllerDecisionView(scope)
			if err != nil {
				continue
			}
			authorized = true
			for _, approval := range view.Approvals {
				items = append(items, map[string]any{"id": approval.Header.ID, "title": "Named-party field decision", "status": approval.State})
			}
			for _, attestation := range view.Attestations {
				items = append(items, map[string]any{"id": attestation.Header.ID, "title": "Issued contribution attestation", "status": attestation.State})
			}
		}
		if !authorized {
			return nil, ErrStrideE10NotFound
		}
	case "organization-people", "organization-requests":
		view, err := r.organization.ReadStrideE10OrganizationAdminView(viewer)
		if err != nil {
			return nil, ErrStrideE10NotFound
		}
		if surface == "organization-people" {
			for _, membership := range view.Memberships {
				items = append(items, map[string]any{"id": membership.Header.ID, "title": "Organization member", "status": membership.Status, "kind": "membership-detail", "detail": map[string]any{"kind": "membership-detail", "role": membership.Role, "status": membership.Status, "isFinalOwner": membership.Role == "owner" && countStrideE10Owners(view.Memberships) == 1}})
			}
			for _, event := range view.Audit {
				items = append(items, map[string]any{"id": event.Header.ID, "title": "Organization audit event", "status": event.Action, "context": event.OccurredAt.UTC().Format(time.RFC3339)})
			}
		} else if surface == "organization-requests" {
			for _, request := range view.JoinRequests {
				items = append(items, map[string]any{"id": request.Header.ID, "title": "Join request", "status": request.Status, "kind": "join-request-detail", "detail": map[string]any{"kind": "join-request-detail", "status": request.Status, "expiresAt": request.ExpiresAt.UTC().Format(time.RFC3339)}})
			}
		}
	case "network-draft", "network-preview", "network-recruiter-view", "contact-inbox", "network-blocks":
		view, err := r.network.ReadStrideE10NetworkPersonView(principal.PersonID)
		if err != nil {
			return nil, strideE10LiveError(err)
		}
		if surface == "network-draft" || surface == "network-preview" || surface == "network-recruiter-view" {
			for _, profile := range view.Profiles {
				if surface == "network-recruiter-view" && profile.State != "published" {
					continue
				}
				fields := make([]string, 0, len(profile.Fields))
				for _, field := range profile.Fields {
					closed := map[string]string{"display_name": "display_name", "pronouns": "pronouns", "bio": "bio", "work_mode": "work_modes", "open_to": "open_to", "problem_class": "contribution_problem_classes", "contribution_role": "contribution_roles"}[field.FieldKey]
					if field.Claim != nil {
						closed = "verified_contributions"
					}
					if closed != "" && !containsSTRIDEString(fields, closed) {
						fields = append(fields, closed)
					}
				}
				state := map[string]string{"published": "live", "draft": "draft", "paused": "paused", "off": "off", "deleted": "off"}[profile.State]
				if surface != "network-recruiter-view" {
					items = append(items, map[string]any{"id": profile.Header.ID, "title": "Network profile", "status": state, "kind": "network-state", "detail": map[string]any{"kind": "network-state", "state": state, "searchableFields": fields}})
				}
				// W1 stores only released-field commitments, never private source
				// bodies. Empty typed lists are therefore honest unknown values;
				// the separately bound evidence row carries exact public refs.
				if detail, ok := strideE10NetworkProfileDetail(profile); ok {
					items = append(items, map[string]any{"id": profile.Header.ID + "-profile", "title": "Published profile fields", "status": state, "kind": "network-profile-detail", "detail": detail})
				}
				if profile.State == "published" {
					if scope, ok := r.currentContributionViewScope(StrideE10ProductPrincipal{PersonID: profile.SubjectPersonID}, false); ok {
						if contributionView, contributionErr := r.contribution.ReadStrideE10ContributionView(scope); contributionErr == nil {
							for _, evidence := range strideE10WorkRecordProjectionItems(contributionView, StrideE10OrganizationSelfView{}, r.contribution.FieldEligible) {
								if evidence["kind"] != "contribution-evidence" {
									continue
								}
								detail, _ := evidence["detail"].(map[string]any)
								published, _ := detail["publishedClaim"].(map[string]any)
								if published["id"] == profile.Publication.ID && published["revision"] == profile.Publication.Revision && published["digest"] == profile.Publication.Digest {
									items = append(items, evidence)
								}
							}
						}
					}
				}
			}
		} else if surface == "contact-inbox" {
			for _, contact := range view.Contacts {
				if contact.SenderPersonID != principal.PersonID && contact.RecipientPersonID != principal.PersonID {
					continue
				}
				items = append(items, map[string]any{"id": contact.Header.ID, "title": "Contact request", "status": contact.State, "kind": "contact-request-detail", "detail": map[string]any{"kind": "contact-request-detail", "purpose": contact.Purpose, "collaborationType": contact.CollaborationType, "state": contact.State, "channelRevealed": contact.State == "accepted" && contact.AcceptedChannelDigest != ""}})
			}
		} else {
			for _, block := range view.Blocks {
				targetKind := "person"
				if block.BlockedOrganizationID != "" {
					targetKind = "organization"
				}
				items = append(items, map[string]any{"id": block.Header.ID, "title": "Network block", "status": block.State, "kind": "block-detail", "detail": map[string]any{"kind": "block-detail", "state": block.State, "targetKind": targetKind}})
			}
		}
	case "organization-recruiting":
		view, err := r.network.ReadStrideE10NetworkRecruitingAdminView(r.organization, viewer)
		if err != nil {
			return nil, ErrStrideE10NotFound
		}
		for _, grant := range view.Grants {
			receipts := make([]map[string]any, 0)
			for _, receipt := range view.SearchReceipts {
				verdict := "denied"
				if receipt.PolicyVerdict == "admitted" {
					verdict = "admitted"
				}
				receipts = append(receipts, map[string]any{"kind": "search", "verdict": verdict, "revision": receipt.Revision, "occurredAt": receipt.SearchedAt})
			}
			for _, receipt := range view.Contacts {
				verdict := "denied"
				if receipt.State == "accepted" {
					verdict = "admitted"
				}
				receipts = append(receipts, map[string]any{"kind": "contact", "verdict": verdict, "revision": receipt.Revision, "occurredAt": receipt.StateChangedAt})
			}
			audit := make([]map[string]any, 0, len(view.Audit))
			for _, entry := range view.Audit {
				audit = append(audit, map[string]any{"action": entry.Kind, "actorRole": "capability_administrator", "revision": entry.Revision, "occurredAt": entry.OccurredAt})
			}
			usage := r.currentRecruitingUsage(grant, r.now().UTC())
			items = append(items, map[string]any{"id": grant.Header.ID, "title": "Recruiting grant", "status": grant.State, "kind": "recruiting-governance", "detail": map[string]any{"kind": "recruiting-governance", "grantState": grant.State, "grantRevision": grant.Header.Revision, "expiresAt": grant.ExpiresAt.UTC().Format(time.RFC3339), "capability": "talent_searcher", "personSearchLimit": strideE10LiveLimitSummary(usage.personSearch, networkSearchesPerHour, usage.personSearchEnds.Format(time.RFC3339)), "organizationSearchLimit": strideE10LiveLimitSummary(usage.organizationSearch, networkSearchesPerHour*10, usage.organizationSearchEnds.Format(time.RFC3339)), "globalSearchLimit": strideE10LiveLimitSummary(usage.globalSearch, networkSearchesPerHour*100, usage.globalSearchEnds.Format(time.RFC3339)), "personContactLimit": strideE10LiveLimitSummary(usage.personContact, networkContactsPerDay, usage.personContactEnds.Format(time.RFC3339)), "organizationContactLimit": strideE10LiveLimitSummary(usage.organizationContact, networkContactsPerDay*10, usage.organizationContactEnds.Format(time.RFC3339)), "globalContactLimit": strideE10LiveLimitSummary(usage.globalContact, networkContactsPerDay*100, usage.globalContactEnds.Format(time.RFC3339)), "receiptSummaries": receipts, "auditEntries": audit}})
		}
	case "network-search":
		view, err := r.network.ReadStrideE10NetworkOrganizationView(viewer)
		if err != nil {
			return nil, ErrStrideE10NotFound
		}
		for _, receipt := range view.SearchReceipts {
			filters := make([]string, 0, len(receipt.StructuredFilters))
			for _, filter := range receipt.StructuredFilters {
				filters = append(filters, filter.Field+" "+filter.Operation+" "+filter.VisibleValue)
			}
			verdict := "denied"
			if receipt.PolicyVerdict == "admitted" {
				verdict = "admitted"
			}
			items = append(items, map[string]any{"id": receipt.Header.ID + "-interpretation", "title": "Search interpretation", "status": verdict, "kind": "network-query-interpretation", "detail": map[string]any{"kind": "network-query-interpretation", "verdict": verdict, "filters": filters}})
			results, ok := r.currentNetworkSearchResults(receipt)
			if !ok {
				return nil, ErrStrideE10NotFound
			}
			items = append(items, results...)
		}
	}
	return items, nil
}

type strideE10RecruitingUsage struct {
	personSearch, organizationSearch, globalSearch                int
	personContact, organizationContact, globalContact             int
	personSearchEnds, organizationSearchEnds, globalSearchEnds    time.Time
	personContactEnds, organizationContactEnds, globalContactEnds time.Time
}

func (r *StrideE10ProductLiveRuntime) currentRecruitingUsage(grant TalentSearchGrant, at time.Time) strideE10RecruitingUsage {
	r.network.mu.Lock()
	defer r.network.mu.Unlock()
	personSearch := pruneSearchWindow(r.network.searchWindows["person:"+grant.SearcherPersonID], at.Add(-time.Hour))
	organizationSearch := pruneSearchWindow(r.network.searchWindows["organization:"+grant.OrganizationID], at.Add(-time.Hour))
	globalSearch := pruneSearchWindow(r.network.searchWindows["global"], at.Add(-time.Hour))
	personContact := pruneTimes(r.network.contactWindows["person:"+grant.SearcherPersonID], at.Add(-24*time.Hour))
	organizationContact := pruneTimes(r.network.contactWindows["organization:"+grant.OrganizationID], at.Add(-24*time.Hour))
	globalContact := pruneTimes(r.network.contactWindows["global"], at.Add(-24*time.Hour))
	return strideE10RecruitingUsage{personSearch: len(personSearch), organizationSearch: len(organizationSearch), globalSearch: len(globalSearch), personContact: len(personContact), organizationContact: len(organizationContact), globalContact: len(globalContact), personSearchEnds: strideE10SearchWindowEnd(personSearch, at), organizationSearchEnds: strideE10SearchWindowEnd(organizationSearch, at), globalSearchEnds: strideE10SearchWindowEnd(globalSearch, at), personContactEnds: strideE10ContactWindowEnd(personContact, at), organizationContactEnds: strideE10ContactWindowEnd(organizationContact, at), globalContactEnds: strideE10ContactWindowEnd(globalContact, at)}
}

func strideE10SearchWindowEnd(values []networkTimedSearch, now time.Time) time.Time {
	if len(values) == 0 {
		return now.UTC()
	}
	oldest := values[0].At
	for _, value := range values[1:] {
		if value.At.Before(oldest) {
			oldest = value.At
		}
	}
	return oldest.UTC().Add(time.Hour)
}

func strideE10ContactWindowEnd(values []time.Time, now time.Time) time.Time {
	if len(values) == 0 {
		return now.UTC()
	}
	oldest := values[0]
	for _, value := range values[1:] {
		if value.Before(oldest) {
			oldest = value
		}
	}
	return oldest.UTC().Add(24 * time.Hour)
}

func (r *StrideE10ProductLiveRuntime) currentContributionViewScope(principal StrideE10ProductPrincipal, organization bool) (StrideE10ContributionViewScope, bool) {
	r.contribution.mu.RLock()
	defer r.contribution.mu.RUnlock()
	var scope StrideE10ContributionViewScope
	for _, grant := range r.contribution.grants {
		matches := grant.Controller.PrincipalID == principal.PersonID
		if organization {
			matches = matches && grant.Role == "organization_reviewer" && grant.OrganizationID == principal.ActiveOrganizationID
		} else {
			matches = matches && grant.Role == "subject" && grant.PersonID == principal.PersonID
		}
		if !matches {
			continue
		}
		if scope.GrantID != "" {
			return StrideE10ContributionViewScope{}, false
		}
		scope = StrideE10ContributionViewScope{GrantID: grant.ID, Controller: grant.Controller}
	}
	return scope, scope.GrantID != ""
}

func (r *StrideE10ProductLiveRuntime) currentContributionGrant(principal StrideE10ProductPrincipal, role, organizationID string) (StrideE10ContributionViewScope, bool) {
	r.contribution.mu.RLock()
	defer r.contribution.mu.RUnlock()
	var scope StrideE10ContributionViewScope
	for _, grant := range r.contribution.grants {
		if grant.Role != role || grant.Controller.PrincipalID != principal.PersonID {
			continue
		}
		if organizationID != "" && grant.OrganizationID != organizationID || organizationID == "" && grant.OrganizationID != "" {
			continue
		}
		if scope.GrantID != "" {
			return StrideE10ContributionViewScope{}, false
		}
		scope = StrideE10ContributionViewScope{GrantID: grant.ID, Controller: grant.Controller}
	}
	return scope, scope.GrantID != ""
}

func (r *StrideE10ProductLiveRuntime) currentContributionControllerScopes(personID string) []StrideE10ContributionViewScope {
	r.contribution.mu.RLock()
	defer r.contribution.mu.RUnlock()
	scopes := make([]StrideE10ContributionViewScope, 0)
	for _, grant := range r.contribution.grants {
		if grant.Controller.PrincipalID == personID && oneOf(grant.Role, "named_party", "signing_issuer") {
			scopes = append(scopes, StrideE10ContributionViewScope{GrantID: grant.ID, Controller: grant.Controller})
		}
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].GrantID < scopes[j].GrantID })
	return scopes
}

func findStrideE10Grant(grants []TalentSearchGrant, id string) *TalentSearchGrant {
	for _, grant := range grants {
		if grant.Header.ID == id {
			clone := cloneTalentSearchGrant(grant)
			return &clone
		}
	}
	return nil
}

func strideE10ViewHasPublishedAttestation(publications []PublishedContributionClaim, attestationID string) bool {
	for _, publication := range publications {
		if publication.State != "published" {
			continue
		}
		for _, reference := range publication.Attestations {
			if reference.ID == attestationID {
				return true
			}
		}
	}
	return false
}

func strideE10HasActivePersonBlock(blocks []NetworkBlock, personID string) bool {
	for _, block := range blocks {
		if block.State == "active" && block.BlockedPersonID == personID {
			return true
		}
	}
	return false
}

func (r *StrideE10ProductLiveRuntime) currentNetworkPersonController(personID string) *STRIDEControllerRevision {
	r.network.mu.Lock()
	defer r.network.mu.Unlock()
	var controller *STRIDEControllerRevision
	for _, profile := range r.network.profiles {
		if profile.SubjectPersonID != personID || profile.State == "deleted" || profile.Controller.Validate() != nil {
			continue
		}
		if controller != nil && *controller != profile.Controller {
			return nil
		}
		value := profile.Controller
		controller = &value
	}
	if controller == nil {
		for _, grant := range r.network.grants {
			if grant.SearcherPersonID != personID || grant.State != "active" {
				continue
			}
			value := STRIDEControllerRevision{PrincipalID: personID, AuthorityID: grant.Header.ID, AuthorityRevision: grant.Header.Revision, PolicyRevision: grant.PolicyRevision}
			if controller != nil && *controller != value {
				return nil
			}
			controller = &value
		}
	}
	return controller
}

func (r *StrideE10ProductLiveRuntime) currentTalentCapability(principal StrideE10ProductPrincipal) (TalentSearchCapabilityAssertion, bool) {
	r.network.mu.Lock()
	defer r.network.mu.Unlock()
	var result TalentSearchCapabilityAssertion
	for _, authority := range r.network.capabilityAuthorities {
		if !authority.Active || authority.OrganizationID != principal.ActiveOrganizationID || authority.ControllerPersonID != principal.PersonID || authority.MembershipID != principal.OrganizationMembershipID || authority.MembershipRevision != principal.OrganizationMembershipRev {
			continue
		}
		if result.AuthorityID != "" {
			return TalentSearchCapabilityAssertion{}, false
		}
		result = TalentSearchCapabilityAssertion{AuthorityID: authority.ID, AuthorityRevision: authority.Revision, ControllerPersonID: authority.ControllerPersonID}
	}
	return result, result.AuthorityID != ""
}

func strideE10WorkRecordProjectionItems(view StrideE10ContributionView, identity StrideE10OrganizationSelfView, fieldEligible func(string, string) bool) []map[string]any {
	items := make([]map[string]any, 0)
	claims := map[string]ContributionClaim{}
	sectionEntries := map[string][]string{
		"problems-outcomes": {}, "how-i-contribute": {}, "organizations-roles": {},
		"work-evidence": {}, "people-agents-helped": {}, "open-to": {},
	}
	appendUnique := func(section, value string) {
		value = strings.TrimSpace(value)
		if value != "" && !containsSTRIDEString(sectionEntries[section], value) {
			sectionEntries[section] = append(sectionEntries[section], value)
		}
	}
	for _, claim := range view.Claims {
		claims[claim.Header.ID] = claim
		if claim.State != "verified" {
			continue
		}
		if strings.TrimSpace(claim.ProblemClass) != "" && strings.TrimSpace(claim.OutcomeClass) != "" {
			appendUnique("problems-outcomes", claim.ProblemClass+" / "+claim.OutcomeClass)
		}
		appendUnique("how-i-contribute", claim.ContributionKind)
	}
	organizationNames := map[string]string{}
	for _, organization := range identity.Organizations {
		organizationNames[organization.Header.ID] = organization.Name
	}
	for _, membership := range identity.Memberships {
		if membership.Status == "active" {
			appendUnique("organizations-roles", strings.TrimSpace(organizationNames[membership.OrganizationID])+" / "+membership.Role)
		}
	}
	for _, influence := range view.Influences {
		if influence.State == "verified" {
			appendUnique("people-agents-helped", "Verified agent influence")
		}
	}
	if identity.Profile != nil && identity.Profile.OpenToEnabled {
		for _, value := range identity.Profile.OpenTo {
			appendUnique("open-to", value)
		}
	}
	attestations := map[string]ContributionAttestation{}
	approvals := map[string]FieldReleaseApproval{}
	for _, approval := range view.Approvals {
		approvals[approval.Header.ID] = approval
	}
	for _, attestation := range view.Attestations {
		attestations[attestation.Header.ID] = attestation
	}
	for _, publication := range view.Publications {
		if publication.State != "published" {
			continue
		}
		for _, attestationRef := range publication.Attestations {
			attestation, ok := attestations[attestationRef.ID]
			if !ok || attestation.State != "active" || attestation.Header.Revision != attestationRef.Revision || attestation.Header.ContentDigest != attestationRef.Digest {
				continue
			}
			claim, ok := claims[attestation.Claim.ID]
			if !ok || claim.State != "verified" || claim.Header.Revision != attestation.Claim.Revision || claim.Header.ContentDigest != attestation.Claim.Digest {
				continue
			}
			if fieldEligible == nil {
				continue
			}
			releasedFields := make([]string, 0, len(attestation.ReleasedFields))
			eligible := true
			for _, field := range attestation.ReleasedFields {
				if !fieldEligible(publication.Header.ID, field.FieldKey) || !strideE10ReleasedFieldApprovalsCurrent(attestation, field, approvals) {
					eligible = false
					break
				}
				releasedFields = append(releasedFields, field.FieldKey)
			}
			if !eligible {
				continue
			}
			appendUnique("work-evidence", strings.TrimSpace(claim.ProblemClass)+" / "+strings.TrimSpace(claim.OutcomeClass)+" — "+strings.TrimSpace(claim.ContributionKind))
			artifactAccess := "authorized"
			if attestation.VerificationTier == "organization_verified_opaque" {
				artifactAccess = "redacted"
			}
			detail := map[string]any{"kind": "contribution-evidence", "problem": claim.ProblemClass, "outcome": claim.OutcomeClass, "contribution": claim.ContributionKind, "verificationTier": attestation.VerificationTier, "releasedFields": releasedFields, "attestation": strideE10LiveReference(attestationRef), "publishedClaim": strideE10LiveReference(refForHeader(publication.Header)), "artifactAccess": artifactAccess}
			for _, influence := range view.Influences {
				if influence.State == "verified" && influence.SubjectPersonID == publication.SubjectPersonID {
					detail["reviewedInfluence"] = influence.Header.ID
					break
				}
			}
			items = append(items, map[string]any{"id": publication.Header.ID + "-" + attestation.Header.ID, "title": "Verified contribution evidence", "status": publication.State, "kind": "contribution-evidence", "detail": detail})
		}
	}
	titles := map[string]string{"problems-outcomes": "Problems and outcomes", "how-i-contribute": "How I contribute", "organizations-roles": "Organizations and roles", "work-evidence": "Work evidence", "people-agents-helped": "People and agents helped", "open-to": "Open to"}
	sections := make([]map[string]any, 0, 6)
	for _, section := range []string{"problems-outcomes", "how-i-contribute", "organizations-roles", "work-evidence", "people-agents-helped", "open-to"} {
		detail := map[string]any{"kind": "work-record-section", "section": section, "entries": sectionEntries[section]}
		if section == "open-to" {
			detail["openToEnabled"] = identity.Profile != nil && identity.Profile.OpenToEnabled
		}
		sections = append(sections, map[string]any{"id": "work-record-" + section, "title": titles[section], "kind": "work-record-section", "detail": detail})
	}
	items = append(sections, items...)
	return items
}

func strideE10ReleasedFieldApprovalsCurrent(attestation ContributionAttestation, field ReleasedContributionField, approvals map[string]FieldReleaseApproval) bool {
	if len(field.ApprovalRefs) == 0 {
		return false
	}
	for _, ref := range field.ApprovalRefs {
		approval, ok := approvals[ref.ID]
		if !ok || approval.State != "approved" || approval.Header.Revision != ref.Revision || approval.Header.ContentDigest != ref.Digest || approval.Attestation.ID != attestation.Header.ID || approval.Attestation.Revision != attestation.Header.Revision || approval.FieldKey != field.FieldKey || approval.FieldValueDigest != field.ValueDigest {
			return false
		}
	}
	return true
}

func strideE10ContributionReviewProjectionItems(view StrideE10ContributionView) []map[string]any {
	items := make([]map[string]any, 0, len(view.Claims))
	for _, claim := range view.Claims {
		if len(claim.SourceRefs) == 0 || claim.SourceRefs[0].Validate() != nil {
			continue
		}
		fieldDiffs := make([]map[string]any, 0)
		namedPartyStates := make([]map[string]any, 0)
		for _, approval := range view.Approvals {
			if approval.SubjectPersonID != claim.SubjectPersonID {
				continue
			}
			fieldDiffs = append(fieldDiffs, map[string]any{"field": approval.FieldKey, "before": "redacted", "after": approval.State, "disclosureTier": "redacted"})
			for range approval.RequiredPartyIDs {
				namedPartyStates = append(namedPartyStates, map[string]any{"partyLabel": "Required named party", "state": approval.State, "required": true})
			}
		}
		audit := []map[string]any{{"action": claim.State, "actorRole": "organization_reviewer", "revision": claim.Header.Revision, "occurredAt": claim.StateChangedAt.UTC().Format(time.RFC3339)}}
		items = append(items, map[string]any{"id": claim.Header.ID, "title": "Contribution review", "status": claim.State, "kind": "contribution-review", "detail": map[string]any{"kind": "contribution-review", "claim": strideE10LiveReference(refForHeader(claim.Header)), "sourceRevision": claim.SourceRefs[0].Revision, "sourceDigest": claim.SourceRefs[0].Digest, "fieldDiffs": fieldDiffs, "namedPartyStates": namedPartyStates, "auditEntries": audit}})
	}
	return items
}

func strideE10LiveReference(reference STRIDEReference) map[string]any {
	return map[string]any{"id": reference.ID, "revision": reference.Revision, "digest": reference.Digest}
}

func strideE10NetworkProfileDetail(profile NetworkProfileProjection) (map[string]any, bool) {
	detail := map[string]any{"kind": "network-profile-detail"}
	for _, field := range profile.Fields {
		if len(field.VisibleValue) == 0 || field.Validate() != nil {
			continue
		}
		var value any
		if json.Unmarshal(field.VisibleValue, &value) != nil {
			continue
		}
		switch field.FieldKey {
		case "display_name":
			detail["displayName"] = value
		case "pronouns":
			detail["pronouns"] = value
		case "bio":
			detail["bio"] = value
		case "work_mode":
			detail["workModes"] = value
		case "open_to":
			detail["openTo"] = value
		}
	}
	return detail, len(detail) > 1
}

func (r *StrideE10ProductLiveRuntime) currentNetworkSearchResults(receipt NetworkSearchReceipt) ([]map[string]any, bool) {
	r.network.mu.Lock()
	defer r.network.mu.Unlock()
	current, ok := r.network.searchReceipts[receipt.Header.ID]
	if !ok || current.Header.Revision != receipt.Header.Revision || current.Header.ContentDigest != receipt.Header.ContentDigest {
		return nil, false
	}
	items := make([]map[string]any, 0, len(receipt.Results))
	for _, result := range receipt.Results {
		profile, ok := r.network.profiles[result.Projection.ID]
		if !ok || profile.Header.Revision != result.Projection.Revision || profile.Header.ContentDigest != result.Projection.Digest || profile.State != "published" {
			return nil, false
		}
		labels := make([]string, 0, len(profile.Fields))
		for _, field := range profile.Fields {
			if !containsSTRIDEString(labels, field.EvidenceLabel) {
				labels = append(labels, field.EvidenceLabel)
			}
		}
		items = append(items, map[string]any{"id": receipt.Header.ID + "-" + profile.Header.ID, "title": "Network search result", "kind": "network-search-result", "detail": map[string]any{"kind": "network-search-result", "why": append([]string(nil), result.Why...), "unknown": append([]string(nil), result.Unknown...), "verificationLabels": labels, "publishedRefs": []map[string]any{strideE10LiveReference(profile.Publication)}}})
	}
	return items, true
}

func strideE10LiveLimitSummary(used, limit int, windowEndsAt string) map[string]any {
	if used > limit {
		used = limit
	}
	return map[string]any{"used": used, "limit": limit, "windowEndsAt": windowEndsAt}
}

func countStrideE10Owners(memberships []OrganizationMembership) int {
	count := 0
	for _, membership := range memberships {
		if membership.Status == "active" && membership.Role == "owner" {
			count++
		}
	}
	return count
}

func strideE10LiveActionLabel(action string) string {
	return map[string]string{
		"profile-update": "Update profile", "organization-create": "Create organization", "organization-join": "Join organization",
		"organization-request-approve": "Approve request", "organization-request-deny": "Deny request", "organization-switch": "Switch organization", "organization-leave": "Leave organization",
		"organization-member-role-change": "Change role", "organization-ownership-transfer": "Transfer ownership", "organization-member-revoke": "Remove member",
		"organization-recruiting-grant-create": "Create recruiting grant", "organization-recruiting-grant-revoke": "Revoke recruiting grant",
		"network-draft-save": "Save private draft", "network-publish": "Publish network profile", "network-pause": "Pause network profile", "network-profile-off": "Turn network profile off",
		"network-search-submit": "Search network", "contact-send": "Send contact request",
		"contribution-subject-approve": "Approve contribution", "contribution-subject-dispute": "Dispute contribution",
		"contribution-organization-approve": "Approve as organization", "contribution-organization-deny": "Deny as organization",
		"contribution-named-party-decision": "Decide as named party", "contribution-attestation-revoke": "Revoke issued attestation",
		"contribution-publish": "Publish contribution", "contribution-withdraw": "Withdraw contribution",
		"contribution-correct": "Correct contribution", "contribution-revoke": "Revoke contribution",
		"work-record-export": "Export work record", "work-record-delete": "Delete work record",
		"network-profile-export": "Export network profile", "network-profile-delete": "Delete network profile",
		"network-searchable-fields-update": "Update searchable fields", "contact-accept": "Accept contact request", "contact-decline": "Decline contact request", "contact-withdraw": "Withdraw contact request",
		"network-block": "Block person", "network-unblock": "Unblock person",
	}[action]
}

var strideE10LiveProductRuntime = NewStrideE10ProductLiveRuntime(time.Now)

func registerStrideE10ProductLiveRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	handler := NewStrideE10ProductHTTP(strideE10LiveProductRuntime.ResolvePrincipal, strideE10LiveProductRuntime, strideE10LiveProductRuntime)
	for _, pattern := range []string{"/api/stride/v1/mobile/", "/api/identity/v1/", "/api/organizations", "/api/organizations/", "/api/session/", "/api/work-record/", "/api/contributions/", "/api/network/"} {
		mux.Handle(pattern, handler)
	}
}
