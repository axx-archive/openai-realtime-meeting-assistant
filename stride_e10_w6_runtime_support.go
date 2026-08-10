package main

// Durable and current-authority adapters for the route-free W6 runtime.

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

type strideE10W6PurgeStoreEnvelope struct {
	Schema     string                         `json:"schema"`
	Generation uint64                         `json:"generation"`
	Works      []STRIDENetworkShadowPurgeWork `json:"works"`
	KeyID      string                         `json:"keyId"`
	KeyVersion uint64                         `json:"keyVersion"`
	MAC        string                         `json:"mac"`
}

type strideE10W6FilePurgeStore struct {
	mu       sync.Mutex
	path     string
	lockPath string
	keys     *strideE10W6ManagedKeyring
}

var errStrideE10W6NoPurgeMutation = errors.New("no purge mutation")

func newStrideE10W6FilePurgeStore(path string, keys *strideE10W6ManagedKeyring) (*strideE10W6FilePurgeStore, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || keys == nil || !validStrideE10W6ManagedKey(keys.current) {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	s := &strideE10W6FilePurgeStore{path: path, lockPath: path + ".lock", keys: keys}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, ErrStrideE10W6RuntimeUnavailable
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		empty := strideE10W6PurgeStoreEnvelope{Schema: strideE10W6PurgeSchema, Generation: 1, Works: []STRIDENetworkShadowPurgeWork{}}
		if err := s.write(empty); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, ErrStrideE10W6RuntimeUnavailable
	}
	if _, err := s.read(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *strideE10W6FilePurgeStore) signed(v strideE10W6PurgeStoreEnvelope) (strideE10W6PurgeStoreEnvelope, error) {
	key, err := s.keys.CurrentW6ManagedMACKey(context.Background())
	if err != nil {
		return v, err
	}
	v.Schema, v.KeyID, v.KeyVersion, v.MAC = strideE10W6PurgeSchema, key.ID, key.Version, ""
	payload, err := json.Marshal(v)
	if err != nil {
		return v, err
	}
	v.MAC = strideE10W6MAC(key.Secret, strideE10W6PurgeDomain, payload)
	return v, nil
}

func (s *strideE10W6FilePurgeStore) read() (strideE10W6PurgeStoreEnvelope, error) {
	v, err := loadStrideE10W6JSON[strideE10W6PurgeStoreEnvelope](s.path)
	if err != nil || v.Schema != strideE10W6PurgeSchema || v.Generation < 1 || !isHexDigest(v.MAC) {
		return v, ErrStrideE10W6RuntimeUnavailable
	}
	key, err := s.keys.ResolveW6ManagedMACKey(context.Background(), v.KeyID, v.KeyVersion)
	if err != nil {
		return v, ErrStrideE10W6RuntimeUnavailable
	}
	mac := v.MAC
	v.MAC = ""
	payload, err := json.Marshal(v)
	v.MAC = mac
	want, decodeWantErr := hex.DecodeString(strideE10W6MAC(key.Secret, strideE10W6PurgeDomain, payload))
	got, decodeGotErr := hex.DecodeString(mac)
	if err != nil || decodeWantErr != nil || decodeGotErr != nil || !hmac.Equal(want, got) {
		return v, ErrStrideE10W6RuntimeUnavailable
	}
	seen := map[string]bool{}
	for _, work := range v.Works {
		if !validSTRIDENetworkShadowPurgeWork(work) || seen[work.Receipt.Header.ID] {
			return v, ErrStrideE10W6RuntimeUnavailable
		}
		seen[work.Receipt.Header.ID] = true
	}
	return v, nil
}

func (s *strideE10W6FilePurgeStore) write(v strideE10W6PurgeStoreEnvelope) error {
	sort.Slice(v.Works, func(i, j int) bool { return v.Works[i].Receipt.Header.ID < v.Works[j].Receipt.Header.ID })
	signed, err := s.signed(v)
	if err != nil {
		return ErrStrideE10W6RuntimeUnavailable
	}
	return writeStrideE10W6JSON(s.path, signed)
}

func (s *strideE10W6FilePurgeStore) locked(write bool, use func(*strideE10W6PurgeStoreEnvelope) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return ErrStrideE10W6RuntimeUnavailable
	}
	defer lock.Close()
	lockInfo, err := lock.Stat()
	lockStat, ok := lockInfo.Sys().(*syscall.Stat_t)
	if err != nil || !ok || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm() != 0o600 || lockStat.Nlink != 1 || int(lockStat.Uid) != os.Geteuid() {
		return ErrStrideE10W6RuntimeUnavailable
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return ErrStrideE10W6RuntimeUnavailable
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	v, err := s.read()
	if err != nil {
		return err
	}
	if err := use(&v); err != nil {
		if errors.Is(err, errStrideE10W6NoPurgeMutation) {
			return nil
		}
		return err
	}
	if write {
		v.Generation++
		return s.write(v)
	}
	return nil
}

func (s *strideE10W6FilePurgeStore) CreateSTRIDENetworkShadowPurgeWork(_ context.Context, work STRIDENetworkShadowPurgeWork) (created bool, err error) {
	if !validSTRIDENetworkShadowPurgeWork(work) {
		return false, ErrStrideE10W6RuntimeInvalid
	}
	err = s.locked(true, func(v *strideE10W6PurgeStoreEnvelope) error {
		for _, prior := range v.Works {
			if prior.Receipt.Header.ID == work.Receipt.Header.ID {
				if !sameSTRIDENetworkShadowPurgeIdentity(prior.Receipt, work.Receipt) {
					return ErrStrideE10W6RuntimeConflict
				}
				created = false
				return errStrideE10W6NoPurgeMutation
			}
		}
		v.Works = append(v.Works, cloneContract(work))
		created = true
		return nil
	})
	return
}

func (s *strideE10W6FilePurgeStore) GetSTRIDENetworkShadowPurgeWork(_ context.Context, id string) (out STRIDENetworkShadowPurgeWork, found bool, err error) {
	if !strideIdentifier(id) {
		return out, false, ErrStrideE10W6RuntimeInvalid
	}
	err = s.locked(false, func(v *strideE10W6PurgeStoreEnvelope) error {
		for _, work := range v.Works {
			if work.Receipt.Header.ID == id {
				out, found = cloneContract(work), true
				break
			}
		}
		return nil
	})
	return
}

func (s *strideE10W6FilePurgeStore) ListSTRIDENetworkShadowPurgeWork(_ context.Context) (out []STRIDENetworkShadowPurgeWork, err error) {
	err = s.locked(false, func(v *strideE10W6PurgeStoreEnvelope) error { out = cloneContract(v.Works); return nil })
	return
}

func (s *strideE10W6FilePurgeStore) CompareAndSwapSTRIDENetworkShadowPurgeWork(_ context.Context, version uint64, work STRIDENetworkShadowPurgeWork) (swapped bool, err error) {
	if version == 0 || !validSTRIDENetworkShadowPurgeWork(work) {
		return false, ErrStrideE10W6RuntimeInvalid
	}
	err = s.locked(true, func(v *strideE10W6PurgeStoreEnvelope) error {
		for i, prior := range v.Works {
			if prior.Receipt.Header.ID != work.Receipt.Header.ID {
				continue
			}
			if !sameSTRIDENetworkShadowPurgeIdentity(prior.Receipt, work.Receipt) || prior.Version != version {
				return errStrideE10W6NoPurgeMutation
			}
			v.Works[i] = cloneContract(work)
			swapped = true
			return nil
		}
		return errStrideE10W6NoPurgeMutation
	})
	return
}

type strideE10W6LiveAuthorityResolver struct{ network *NetworkAuthority }

func (r *strideE10W6LiveAuthorityResolver) AuthorizeSTRIDEDerivedPurge(receipt DerivedPurgeReceipt) bool {
	if r == nil || r.network == nil || receipt.Validate() != nil {
		return false
	}
	r.network.mu.Lock()
	defer r.network.mu.Unlock()
	current, ok := r.network.purges[receipt.Header.ID]
	return ok && sameSTRIDENetworkShadowPurgeIdentity(current, receipt)
}

func (r *strideE10W6LiveAuthorityResolver) ResolveCurrentSTRIDENetworkShadowAuthority(expectation STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error) {
	if r == nil || r.network == nil {
		return STRIDENetworkShadowAuthoritySnapshot{}, ErrSTRIDENetworkShadowAuthority
	}
	r.network.mu.Lock()
	defer r.network.mu.Unlock()
	return r.resolveLocked(expectation)
}

func (r *strideE10W6LiveAuthorityResolver) WithCurrentSTRIDENetworkShadowAuthorities(snapshots []STRIDENetworkShadowAuthoritySnapshot, use func() error) error {
	if r == nil || r.network == nil || use == nil {
		return ErrSTRIDENetworkShadowAuthority
	}
	r.network.mu.Lock()
	defer r.network.mu.Unlock()
	for _, expected := range snapshots {
		attestations := make([]STRIDEReference, 0, len(expected.Attestations))
		for _, attestation := range expected.Attestations {
			attestations = append(attestations, attestation.Reference)
		}
		current, err := r.resolveLocked(STRIDENetworkShadowAuthorityExpectation{SubjectPersonID: expected.SubjectPersonID, Publication: expected.Publication, Attestations: attestations})
		if err != nil || !sameStrideE10W6AuthoritySnapshot(current, expected) {
			return ErrSTRIDENetworkShadowAuthority
		}
	}
	return use()
}

func sameStrideE10W6AuthoritySnapshot(a, b STRIDENetworkShadowAuthoritySnapshot) bool {
	if a.Generation != b.Generation || a.SubjectPersonID != b.SubjectPersonID || a.Publication != b.Publication || a.PublicationState != b.PublicationState || a.PublicationVisibility != b.PublicationVisibility || len(a.Attestations) != len(b.Attestations) || a.ResolvedTerminalReason != b.ResolvedTerminalReason || a.ResolvedTerminalTarget != b.ResolvedTerminalTarget || !a.ResolvedAt.Equal(b.ResolvedAt) {
		return false
	}
	for i := range a.Attestations {
		if a.Attestations[i] != b.Attestations[i] {
			return false
		}
	}
	return true
}

func (r *strideE10W6LiveAuthorityResolver) resolveLocked(expectation STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error) {
	publication, ok := r.network.publications[expectation.Publication.ID]
	if !ok || publication.SubjectPersonID != expectation.SubjectPersonID {
		return STRIDENetworkShadowAuthoritySnapshot{}, ErrSTRIDENetworkShadowAuthority
	}
	out := STRIDENetworkShadowAuthoritySnapshot{Generation: uint64(publication.Header.Revision), SubjectPersonID: publication.SubjectPersonID, Publication: referenceFromHeader(publication.Header), PublicationState: publication.State, PublicationVisibility: publication.Visibility, ResolvedAt: publication.StateChangedAt.UTC()}
	if publication.Header.Revision != expectation.Publication.Revision || publication.Header.ContentDigest != expectation.Publication.Digest {
		if publication.Header.Revision > expectation.Publication.Revision {
			out.ResolvedTerminalReason, out.ResolvedTerminalTarget = "superseded", expectation.Publication
		}
		return out, nil
	}
	if publication.State != "published" {
		out.ResolvedTerminalReason, out.ResolvedTerminalTarget = publication.State, expectation.Publication
		return out, nil
	}
	for _, ref := range expectation.Attestations {
		attestation, ok := r.network.attestations[ref.ID]
		if !ok || attestation.SubjectPersonID != expectation.SubjectPersonID {
			return STRIDENetworkShadowAuthoritySnapshot{}, ErrSTRIDENetworkShadowAuthority
		}
		state := STRIDENetworkShadowAttestationAuthority{Reference: referenceFromHeader(attestation.Header), State: attestation.State}
		out.Attestations = append(out.Attestations, state)
		if attestation.Header.Revision != ref.Revision || attestation.Header.ContentDigest != ref.Digest || attestation.State != "active" {
			out.ResolvedTerminalReason, out.ResolvedTerminalTarget = "revoked", ref
			if attestation.State == "superseded" {
				out.ResolvedTerminalReason = "superseded"
			}
			if attestation.RevokedAt != nil {
				out.ResolvedAt = attestation.RevokedAt.UTC()
			}
		}
	}
	sort.Slice(out.Attestations, func(i, j int) bool { return out.Attestations[i].Reference.ID < out.Attestations[j].Reference.ID })
	return out, nil
}

type strideE10W6LiveSearchAuthorityResolver struct {
	network  *NetworkAuthority
	sessions StrideE10W6SessionAuthority
}

func (*strideE10W6LiveSearchAuthorityResolver) STRIDENetworkShadowCombinedAuthorityValidation() {}

func (r *strideE10W6LiveSearchAuthorityResolver) WithCurrentSTRIDENetworkShadowSearchAuthority(ctx context.Context, expectation STRIDENetworkShadowSearchAuthorityExpectation, use func(STRIDENetworkShadowSearchAuthoritySnapshot) error) error {
	if r == nil || r.network == nil || r.sessions == nil || use == nil || !strideIdentifier(expectation.OrganizationID) || expectation.SessionHash == "" {
		return ErrSTRIDENetworkShadowAuthority
	}
	return r.sessions.WithCurrentStrideE10W6Session(ctx, expectation.OrganizationID, expectation.SessionHash, func(session StrideE10W6CurrentSession) error {
		if session.SessionHash != expectation.SessionHash || session.OrganizationID != expectation.OrganizationID || session.ActiveOrganizationSessionID != expectation.ActiveOrganizationSessionID || !strideIdentifier(session.PersonID) || !strideIdentifier(session.MembershipID) || session.MembershipRevision < 1 || !strideIdentifier(session.ActiveOrganizationSessionID) || session.ActiveOrganizationSessionRev < 1 {
			return ErrSTRIDENetworkShadowAuthority
		}
		r.network.mu.Lock()
		defer r.network.mu.Unlock()
		membership, ok := r.network.membershipAuthorities[session.MembershipID]
		if !ok || !membership.Active || membership.OrganizationID != session.OrganizationID || membership.PersonID != session.PersonID || membership.Revision != session.MembershipRevision {
			return ErrSTRIDENetworkShadowAuthority
		}
		var grant TalentSearchGrant
		found := false
		for _, candidate := range r.network.grants {
			if candidate.OrganizationID != session.OrganizationID || candidate.SearcherPersonID != session.PersonID || candidate.MembershipID != session.MembershipID || candidate.MembershipRevision != session.MembershipRevision || candidate.State != "active" || !r.network.now().UTC().Before(candidate.ExpiresAt) {
				continue
			}
			if found {
				return ErrSTRIDENetworkShadowAuthority
			}
			grant, found = candidate, true
		}
		if !found {
			return ErrSTRIDENetworkShadowAuthority
		}
		for _, expected := range expectation.Authorities {
			attestations := make([]STRIDEReference, 0, len(expected.Attestations))
			for _, attestation := range expected.Attestations {
				attestations = append(attestations, attestation.Reference)
			}
			current, resolveErr := (&strideE10W6LiveAuthorityResolver{network: r.network}).resolveLocked(STRIDENetworkShadowAuthorityExpectation{SubjectPersonID: expected.SubjectPersonID, Publication: expected.Publication, Attestations: attestations})
			if resolveErr != nil || !sameStrideE10W6AuthoritySnapshot(current, expected) {
				return ErrSTRIDENetworkShadowAuthority
			}
		}
		capability, ok := r.network.capabilityAuthorities[grant.CapabilityAdministrator.AuthorityID]
		if !ok || !capability.Active || capability.Revision != grant.CapabilityAdministrator.AuthorityRevision || capability.ControllerPersonID != grant.CapabilityAdministrator.PrincipalID || capability.OrganizationID != grant.OrganizationID || capability.PolicyRevision != grant.PolicyRevision {
			return ErrSTRIDENetworkShadowAuthority
		}
		snapshot := STRIDENetworkShadowSearchAuthoritySnapshot{Generation: uint64(grant.Header.Revision), SessionHash: session.SessionHash, PersonID: session.PersonID, OrganizationID: session.OrganizationID, MembershipID: session.MembershipID, MembershipRevision: session.MembershipRevision, ActiveOrganizationSessionID: session.ActiveOrganizationSessionID, ActiveOrganizationSessionRev: session.ActiveOrganizationSessionRev, Grant: referenceFromHeader(grant.Header), GrantOrganizationID: grant.OrganizationID, GrantSearcherPersonID: grant.SearcherPersonID, GrantMembershipID: grant.MembershipID, GrantMembershipRevision: grant.MembershipRevision, GrantState: grant.State}
		return use(snapshot)
	})
}

// Keep the compiler honest about the callback-held wall clock boundary.
var _ = time.Time{}
