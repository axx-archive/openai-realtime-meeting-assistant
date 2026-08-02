package main

import (
	"errors"
	"sort"
)

// meetingSpecialistAuthorityDigests snapshots only the two ledgers that can
// authorize a specialist: the human-configured Product agent revision and the
// complete Workforce seat revision. It runs while STRIDERuntime.mu is held.
func meetingSpecialistAuthorityDigests(domains *strideRuntimeTenantState) map[string]string {
	result := map[string]string{}
	if domains == nil || domains.product == nil || domains.workforce == nil {
		return result
	}
	agents := map[string]STRIDEProductTeamAgent{}
	for _, agent := range domains.product.agentRoster() {
		agents[agent.ID] = agent
	}
	seats := map[string]STRIDEWorkforceSeat{}
	for _, seat := range domains.workforce.ScoutRosterView().Seats {
		seats[seat.ID] = seat
	}
	ids := map[string]bool{}
	for id := range agents {
		ids[id] = true
	}
	for id := range seats {
		ids[id] = true
	}
	for id := range ids {
		agent, hasAgent := agents[id]
		seat, hasSeat := seats[id]
		result[id] = workDigest(struct {
			HasAgent bool
			Agent    STRIDEProductTeamAgent
			HasSeat  bool
			Seat     STRIDEWorkforceSeat
		}{hasAgent, agent, hasSeat, seat})
	}
	return result
}

func changedMeetingSpecialistAuthorityAgents(before, after map[string]string) []string {
	changed := map[string]bool{}
	for id, digest := range before {
		if after[id] != digest {
			changed[id] = true
		}
	}
	for id, digest := range after {
		if before[id] != digest {
			changed[id] = true
		}
	}
	result := make([]string, 0, len(changed))
	for id := range changed {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func (runtime *STRIDERuntime) setMeetingSpecialistAuthorityObserver(observer func(string) error) {
	if runtime == nil {
		return
	}
	runtime.meetingSpecialistObserverMu.Lock()
	runtime.meetingSpecialistObserver = observer
	runtime.meetingSpecialistObserverMu.Unlock()
}

func (runtime *STRIDERuntime) notifyMeetingSpecialistAuthorityChanges(agentIDs []string) error {
	if runtime == nil || len(agentIDs) == 0 {
		return nil
	}
	runtime.meetingSpecialistObserverMu.RLock()
	observer := runtime.meetingSpecialistObserver
	runtime.meetingSpecialistObserverMu.RUnlock()
	if observer == nil {
		return nil
	}
	var notifyErr error
	for _, agentID := range agentIDs {
		notifyErr = errors.Join(notifyErr, observer(agentID))
	}
	return notifyErr
}

func bindMeetingSpecialistAuthorityObserver(runtime *STRIDERuntime, product *MeetingSpecialistProduct) {
	if runtime == nil || product == nil {
		return
	}
	runtime.setMeetingSpecialistAuthorityObserver(func(agentID string) error {
		return product.RevokeAgentAuthority(agentID, "agent_authority_changed")
	})
}
