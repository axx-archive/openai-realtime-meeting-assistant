package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type w6TestKeyring struct {
	keys    map[uint64]W6ManagedMACKey
	current uint64
}

func (k *w6TestKeyring) CurrentW6ManagedMACKey(context.Context) (W6ManagedMACKey, error) {
	key, ok := k.keys[k.current]
	if !ok {
		return W6ManagedMACKey{}, errors.New("missing")
	}
	key.Secret = append([]byte(nil), key.Secret...)
	return key, nil
}
func (k *w6TestKeyring) ResolveW6ManagedMACKey(_ context.Context, id string, version uint64) (W6ManagedMACKey, error) {
	key, ok := k.keys[version]
	if !ok || key.ID != id {
		return W6ManagedMACKey{}, errors.New("missing")
	}
	key.Secret = append([]byte(nil), key.Secret...)
	return key, nil
}

func w6TestPolicy(now time.Time) W6NetworkPolicyRevision {
	return W6NetworkPolicyRevision{PolicyID: "policy_w6", Revision: 7, Enabled: true, CohortIDs: []string{"cohort_pilot"},
		DisclosurePolicyDigest: sha256Hex([]byte("approved disclosure")), ProhibitedCorpusDigest: W6FrozenProhibitedCorpusDigest(), MinimumPublishedCohort: 5,
		TimingToleranceMillis: 25, DisputeSLAMinutes: 1440, Limits: W6NetworkPolicyLimits{PersonSearchesPerHour: 2, OrganizationSearchesPerHour: 4, GlobalSearchesPerHour: 8, ResultsPerSearch: 3, PersonDistinctResultsPerHour: 3, OrganizationDistinctPerHour: 6, GlobalDistinctResultsPerHour: 12, PersonContactsPerDay: 1, OrganizationContactsPerDay: 2, GlobalContactsPerDay: 4},
		EffectiveAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour)}
}

func w6TestPolicyAuthority(t *testing.T, now time.Time) (*W6NetworkPolicyAuthority, *w6TestKeyring, W6NetworkPolicyRevision) {
	t.Helper()
	keys := &w6TestKeyring{current: 1, keys: map[uint64]W6ManagedMACKey{1: {ID: "w6_key", Version: 1, Secret: []byte(strings.Repeat("k", 32))}}}
	policy, err := SignW6NetworkPolicy(context.Background(), keys, w6TestPolicy(now))
	if err != nil {
		t.Fatal(err)
	}
	authority := NewW6NetworkPolicyAuthority(keys)
	if err := authority.Install(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	return authority, keys, policy
}

func TestW6PolicyDefaultOffMACRotationAndRevisionFence(t *testing.T) {
	now := time.Date(2026, 8, 9, 16, 0, 0, 0, time.UTC)
	if err := NewW6NetworkPolicyAuthority(nil).WithCurrentW6Policy(context.Background(), 1, "cohort_pilot", now, func(W6NetworkPolicyRevision) error { return nil }); !errors.Is(err, ErrW6PolicyUnavailable) {
		t.Fatalf("default must be unavailable: %v", err)
	}
	authority, keys, policy := w6TestPolicyAuthority(t, now)
	called := false
	if err := authority.WithCurrentW6Policy(context.Background(), policy.Revision, "cohort_pilot", now, func(got W6NetworkPolicyRevision) error {
		called = true
		if got.MAC != policy.MAC {
			t.Fatal("wrong policy")
		}
		return nil
	}); err != nil || !called {
		t.Fatalf("current policy: %v", err)
	}
	tampered := policy
	tampered.Limits.PersonSearchesPerHour++
	if err := authority.Install(context.Background(), tampered); !errors.Is(err, ErrW6PolicyInvalid) {
		t.Fatalf("tamper accepted: %v", err)
	}
	if err := authority.Install(context.Background(), policy); !errors.Is(err, ErrW6PolicyConflict) {
		t.Fatalf("same revision accepted: %v", err)
	}
	keys.current = 2
	keys.keys[2] = W6ManagedMACKey{ID: "w6_key", Version: 2, Secret: []byte(strings.Repeat("z", 32))}
	next := w6TestPolicy(now)
	next.Revision++
	signed, err := SignW6NetworkPolicy(context.Background(), keys, next)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Install(context.Background(), signed); err != nil {
		t.Fatalf("rotation rejected: %v", err)
	}
	disabled := next
	disabled.Revision++
	disabled.Enabled = false
	disabled, err = SignW6NetworkPolicy(context.Background(), keys, disabled)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Install(context.Background(), disabled); err != nil {
		t.Fatal(err)
	}
	if err := authority.WithCurrentW6Policy(context.Background(), disabled.Revision, "cohort_pilot", now, func(W6NetworkPolicyRevision) error { t.Fatal("disabled policy callback"); return nil }); !errors.Is(err, ErrW6PolicyUnavailable) {
		t.Fatalf("disabled revision activated: %v", err)
	}
	delete(keys.keys, 2)
	if err := authority.Install(context.Background(), func() W6NetworkPolicyRevision {
		v := next
		v.Revision = disabled.Revision + 1
		v.KeyID = "w6_key"
		v.KeyVersion = 2
		v.MAC = strings.Repeat("a", 64)
		return v
	}()); !errors.Is(err, ErrW6PolicyUnavailable) {
		t.Fatalf("missing managed key accepted: %v", err)
	}
}

func TestW6DeterministicProposalConfirmationAndFrozenCorpus(t *testing.T) {
	now := time.Now().UTC()
	_, _, policy := w6TestPolicyAuthority(t, now)
	proposal, err := ProposeW6NetworkInterpretation(policy, "problem_class:distributed systems")
	if err != nil || proposal.Verdict != W6PolicyVerdictTransformWithConfirmation || len(proposal.Filters) != 1 {
		t.Fatalf("proposal: %#v %v", proposal, err)
	}
	confirmation := W6NetworkInterpretationConfirmation{ProposalID: proposal.ProposalID, Revision: proposal.Revision, PolicyRevision: policy.Revision, ProposalDigest: proposal.Digest}
	if filters, err := ConfirmW6NetworkInterpretation(policy, proposal, confirmation); err != nil || len(filters) != 1 {
		t.Fatalf("confirm: %v", err)
	}
	stale := confirmation
	stale.PolicyRevision--
	if _, err := ConfirmW6NetworkInterpretation(policy, proposal, stale); !errors.Is(err, ErrW6PolicyConflict) {
		t.Fatalf("stale confirm accepted: %v", err)
	}
	tampered := proposal
	tampered.Filters[0].VisibleValue = "security"
	tampered.Filters[0].ValueDigest = sha256Hex([]byte("security"))
	if _, err := ConfirmW6NetworkInterpretation(policy, tampered, confirmation); !errors.Is(err, ErrW6PolicyConflict) {
		t.Fatalf("tampered proposal accepted: %v", err)
	}
	for _, query := range W6FrozenProhibitedQueryCorpus() {
		if !W6QueryProhibited(query) {
			t.Fatalf("corpus gap: %q", query)
		}
		got, err := ProposeW6NetworkInterpretation(policy, query)
		if err != nil || got.Verdict != W6PolicyVerdictReject || len(got.Filters) != 0 {
			t.Fatalf("prohibited proposal: %q %#v %v", query, got, err)
		}
	}
	if got, _ := ProposeW6NetworkInterpretation(policy, "find someone good"); got.Verdict != W6PolicyVerdictAbstain {
		t.Fatalf("free text must abstain: %#v", got)
	}
}
