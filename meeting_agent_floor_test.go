package main

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func meetingAgentFloorFixture(now *time.Time) (*MeetingAgentFloorController, MeetingAgentFloorScope, MeetingAgentFloorPolicy) {
	controller := NewMeetingAgentFloorController(func() time.Time { return *now })
	scope := MeetingAgentFloorScope{
		RoomID: "dog-perfect", SittingID: "sitting-1", MediaGeneration: 7,
		InvitationID: "invite-1", SessionID: "specialist-session-1", AgentID: "mary",
		RuntimePrincipal: "runtime-mary-1", AudioTrackID: "verified-agent-track-1",
	}
	policy := MeetingAgentFloorPolicy{SessionTTL: 5 * time.Minute, MaxFloorLease: 20 * time.Second, TurnBudget: 3, AudioBudgetSecond: 45, CostBudgetCents: 25}
	return controller, scope, policy
}

func TestMeetingAgentFloorOneSessionOneSpeakerAndHumanPriority(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	controller, scope, policy := meetingAgentFloorFixture(&now)
	session, err := controller.Admit(scope, policy)
	if err != nil {
		t.Fatal(err)
	}
	other := scope
	other.SessionID = "specialist-session-2"
	other.AgentID = "researcher"
	other.RuntimePrincipal = "runtime-researcher-1"
	other.AudioTrackID = "verified-agent-track-2"
	if _, err := controller.Admit(other, policy); !errors.Is(err, ErrMeetingAgentFloorOccupied) {
		t.Fatalf("second specialist err=%v, want occupied", err)
	}
	floor, err := controller.RequestFloor(session, "approved_scout_handoff", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got := floor.ExpiresAt.Sub(floor.GrantedAt); got != policy.MaxFloorLease {
		t.Fatalf("bounded floor lease=%s", got)
	}
	if _, err := controller.RequestFloor(session, "human_followup", time.Second); !errors.Is(err, ErrMeetingAgentFloorOccupied) {
		t.Fatalf("parallel floor err=%v", err)
	}
	if _, err := controller.AcceptProviderOutput(floor, scope.AudioTrackID, 5, 2); err != nil {
		t.Fatal(err)
	}
	interrupt, ok := controller.HumanBargeIn(scope.RoomID, scope.SittingID, scope.MediaGeneration)
	if !ok || !interrupt.CancelProvider || interrupt.Reason != "human_barge_in" {
		t.Fatalf("interruption=%+v ok=%v", interrupt, ok)
	}
	if _, err := controller.AcceptProviderOutput(floor, scope.AudioTrackID, 1, 0); !errors.Is(err, ErrMeetingAgentFloorFence) {
		t.Fatalf("stale provider output err=%v", err)
	}
	if _, err := controller.RequestFloor(session, "human_followup", 5*time.Second); err != nil {
		t.Fatalf("human follow-up should reacquire: %v", err)
	}
}

func TestMeetingAgentFloorBlocksFeedbackAndAgentTurnChains(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	controller, scope, policy := meetingAgentFloorFixture(&now)
	session, _ := controller.Admit(scope, policy)
	if _, err := controller.AcceptHumanInput(session, scope.AudioTrackID, "human"); !errors.Is(err, ErrMeetingAgentFloorFeedback) {
		t.Fatalf("own track feedback err=%v", err)
	}
	if _, err := controller.AcceptHumanInput(session, "agent-track-other", "agent"); !errors.Is(err, ErrMeetingAgentFloorFeedback) {
		t.Fatalf("agent feedback err=%v", err)
	}
	if highWater, err := controller.AcceptHumanInput(session, "human-track-erick", "human"); err != nil || highWater != 1 {
		t.Fatalf("human input highWater=%d err=%v", highWater, err)
	}
	for _, trigger := range []string{"agent_output", "specialist_followup", "another_agent"} {
		if _, err := controller.RequestFloor(session, trigger, time.Second); !errors.Is(err, ErrMeetingAgentFloorAgentLoop) {
			t.Fatalf("trigger %q err=%v, want agent loop", trigger, err)
		}
	}
}

func TestMeetingAgentFloorGenerationExpiryBudgetAndTeardown(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	controller, scope, policy := meetingAgentFloorFixture(&now)
	session, _ := controller.Admit(scope, policy)
	floor, _ := controller.RequestFloor(session, "explicit_human_request", 10*time.Second)
	if _, err := controller.AcceptProviderOutput(floor, "wrong-track", 1, 0); !errors.Is(err, ErrMeetingAgentFloorFence) {
		t.Fatalf("wrong track err=%v", err)
	}
	if _, err := controller.AcceptProviderOutput(floor, scope.AudioTrackID, 46, 0); !errors.Is(err, ErrMeetingAgentFloorBudget) {
		t.Fatalf("over-budget output err=%v", err)
	}
	if _, err := controller.AcceptProviderOutput(floor, scope.AudioTrackID, 1, 0); !errors.Is(err, ErrMeetingAgentFloorFence) {
		t.Fatalf("budget failure must fence floor: %v", err)
	}
	floor, err := controller.RequestFloor(session, "human_followup", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	if _, err := controller.AcceptProviderOutput(floor, scope.AudioTrackID, 1, 0); !errors.Is(err, ErrMeetingAgentFloorFence) && !errors.Is(err, ErrMeetingAgentFloorExpired) {
		t.Fatalf("expired floor err=%v", err)
	}
	receipt, err := controller.Terminate(session, "dismissed")
	if err != nil || len(receipt) != 64 {
		t.Fatalf("teardown receipt=%q err=%v", receipt, err)
	}
	repeat, err := controller.Terminate(session, "dismissed")
	if err != nil || repeat != receipt {
		t.Fatalf("idempotent teardown receipt=%q err=%v", repeat, err)
	}
	if _, err := controller.AcceptHumanInput(session, "human-track", "human"); !errors.Is(err, ErrMeetingAgentFloorTerminated) {
		t.Fatalf("terminated session input err=%v", err)
	}
}

func TestMeetingAgentFloorConcurrentAcquisitionHasOneWinner(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	controller, scope, policy := meetingAgentFloorFixture(&now)
	session, _ := controller.Admit(scope, policy)
	var wg sync.WaitGroup
	winners := make(chan MeetingAgentFloorLease, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if lease, err := controller.RequestFloor(session, "explicit_human_request", time.Second); err == nil {
				winners <- lease
			}
		}()
	}
	wg.Wait()
	close(winners)
	if got := len(winners); got != 1 {
		t.Fatalf("floor winners=%d, want 1", got)
	}
}

func TestMeetingAgentFloorSessionExpiryReleasesSeatWithoutTouchingHumanTracks(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	controller, scope, policy := meetingAgentFloorFixture(&now)
	session, _ := controller.Admit(scope, policy)
	if _, err := controller.AcceptHumanInput(session, "human-track-aj", "human"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(policy.SessionTTL + time.Nanosecond)
	snapshot := controller.Snapshot()
	if snapshot.Session != nil || snapshot.Floor != nil || snapshot.TeardownReceiptDigest == "" || snapshot.TerminalReason != scope.SessionID+"\x00expired" {
		t.Fatalf("expired session still active: %+v", snapshot)
	}
	if _, err := controller.Admit(scope, policy); err != nil {
		t.Fatalf("expired seat did not release: %v", err)
	}
}

func TestMeetingAgentFloorGuestParticipantTerminationMintsReceipt(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	controller, scope, policy := meetingAgentFloorFixture(&now)
	session, err := controller.Admit(scope, policy)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := controller.Terminate(session, "guest_participant")
	if err != nil || len(receipt) != 64 {
		t.Fatalf("guest terminal receipt=%q err=%v", receipt, err)
	}
	snapshot := controller.Snapshot()
	if snapshot.Session != nil || snapshot.TeardownReceiptDigest != receipt || snapshot.TerminalReason != scope.SessionID+"\x00guest_participant" {
		t.Fatalf("guest terminal snapshot=%+v", snapshot)
	}
}
