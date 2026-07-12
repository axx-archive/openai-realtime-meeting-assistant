package main

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestPeerICERestartCoalescesBrowserAndServerTriggersForOneOutage(t *testing.T) {
	base := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	state := peerICERestartState{}

	// The browser sees the disconnect first and asks immediately.
	if !state.queue(webrtc.ICEConnectionStateDisconnected, base) {
		t.Fatal("first trigger for an outage was not queued")
	}
	state.start(base)

	// The server's grace timer sees the same sustained disconnect later. It
	// must not spend a second ICE restart while the browser-triggered one is in
	// flight or after its answer arrives.
	if state.queue(webrtc.ICEConnectionStateDisconnected, base.Add(iceDisconnectGrace)) {
		t.Fatal("server grace trigger queued a duplicate restart for the same outage")
	}
	state.complete()
	if state.queue(webrtc.ICEConnectionStateDisconnected, base.Add(30*time.Second)) {
		t.Fatal("sustained outage opened another restart window without a healthy transition")
	}
	if state.restartedOutage != 1 || state.outageGeneration != 1 {
		t.Fatalf("same outage generations restarted=%d observed=%d, want 1/1", state.restartedOutage, state.outageGeneration)
	}

	// A later, distinct outage after recovery is eligible immediately; it is
	// not held behind the manual-repair cooldown.
	state.observeConnectionState(webrtc.ICEConnectionStateConnected)
	if !state.queue(webrtc.ICEConnectionStateDisconnected, base.Add(31*time.Second)) {
		t.Fatal("distinct outage after recovery was not queued")
	}
	state.start(base.Add(31 * time.Second))
	if state.restartedOutage != 2 || state.outageGeneration != 2 {
		t.Fatalf("second outage generations restarted=%d observed=%d, want 2/2", state.restartedOutage, state.outageGeneration)
	}
}

func TestPeerICERestartManualRepairUsesCooldown(t *testing.T) {
	base := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	state := peerICERestartState{}

	if !state.queue(webrtc.ICEConnectionStateConnected, base) {
		t.Fatal("first connected-state repair was not queued")
	}
	state.start(base)
	state.complete()

	if state.queue(webrtc.ICEConnectionStateCompleted, base.Add(iceRestartRequestCooldown-time.Millisecond)) {
		t.Fatal("manual repair inside cooldown was queued")
	}
	if !state.queue(webrtc.ICEConnectionStateConnected, base.Add(iceRestartRequestCooldown)) {
		t.Fatal("manual repair at cooldown boundary was not queued")
	}
}

func TestPeerICERestartRetainsOneDistinctOutageWhileOfferInFlight(t *testing.T) {
	base := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	state := peerICERestartState{}

	if !state.queue(webrtc.ICEConnectionStateDisconnected, base) {
		t.Fatal("first outage was not queued")
	}
	state.start(base)

	state.observeConnectionState(webrtc.ICEConnectionStateConnected)
	state.observeConnectionState(webrtc.ICEConnectionStateDisconnected)
	if !state.queue(webrtc.ICEConnectionStateDisconnected, base.Add(time.Second)) {
		t.Fatal("distinct outage while the first offer was in flight was not retained")
	}
	if state.queue(webrtc.ICEConnectionStateDisconnected, base.Add(2*time.Second)) {
		t.Fatal("duplicate trigger queued more than one follow-up")
	}
	state.complete()
	if !state.queued || state.inFlight {
		t.Fatalf("follow-up state queued=%t inFlight=%t, want true/false after first answer", state.queued, state.inFlight)
	}

	state.start(base.Add(3 * time.Second))
	if state.restartedOutage != 2 {
		t.Fatalf("follow-up restart recorded outage=%d, want 2", state.restartedOutage)
	}
}
