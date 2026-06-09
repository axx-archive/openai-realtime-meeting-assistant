package main

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestICEStateNeedsRecovery(t *testing.T) {
	cases := map[webrtc.ICEConnectionState]bool{
		webrtc.ICEConnectionStateDisconnected: true,
		// Recovery must NOT fire on these: failed is torn down by the PC-level
		// failure handler, and the rest are healthy or in-progress states.
		webrtc.ICEConnectionStateFailed:    false,
		webrtc.ICEConnectionStateConnected: false,
		webrtc.ICEConnectionStateCompleted: false,
		webrtc.ICEConnectionStateChecking:  false,
		webrtc.ICEConnectionStateNew:       false,
		webrtc.ICEConnectionStateClosed:    false,
	}
	for state, want := range cases {
		if got := iceStateNeedsRecovery(state); got != want {
			t.Errorf("iceStateNeedsRecovery(%s) = %v, want %v", state, got, want)
		}
	}
}
