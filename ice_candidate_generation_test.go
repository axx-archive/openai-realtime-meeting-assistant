package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestRemoteICECandidateShouldQueueByUsernameFragment(t *testing.T) {
	current := &webrtc.SessionDescription{SDP: "v=0\r\na=ice-ufrag:old-generation\r\n"}
	oldCandidate := webrtc.ICECandidateInit{Candidate: "candidate:old", UsernameFragment: candidateStringPointer("old-generation")}
	newCandidate := webrtc.ICECandidateInit{Candidate: "candidate:new", UsernameFragment: candidateStringPointer("new-generation")}
	legacyCandidate := webrtc.ICECandidateInit{Candidate: "candidate:legacy"}

	if remoteICECandidateShouldQueue(oldCandidate, current) {
		t.Fatal("candidate matching the current remote generation was queued")
	}
	if !remoteICECandidateShouldQueue(newCandidate, current) {
		t.Fatal("candidate for the future answer generation was applied to the old remote description")
	}
	if !remoteICECandidateShouldQueue(oldCandidate, nil) {
		t.Fatal("candidate with no remote description was not queued")
	}
	if remoteICECandidateShouldQueue(legacyCandidate, current) {
		t.Fatal("legacy candidate without usernameFragment changed established behavior")
	}
}

func TestRemoteICECandidateUsernameFragmentUsesFieldThenCandidateExtension(t *testing.T) {
	explicit := webrtc.ICECandidateInit{
		Candidate:        "candidate:1 1 udp 1 127.0.0.1 9000 typ host ufrag embedded-generation",
		UsernameFragment: candidateStringPointer("explicit-generation"),
	}
	embedded := webrtc.ICECandidateInit{Candidate: "candidate:2 1 udp 1 127.0.0.1 9001 typ host ufrag embedded-generation"}
	unscoped := webrtc.ICECandidateInit{Candidate: "candidate:3 1 udp 1 127.0.0.1 9002 typ host"}

	if got := remoteICECandidateUsernameFragment(explicit); got != "explicit-generation" {
		t.Fatalf("explicit username fragment=%q, want explicit-generation", got)
	}
	if got := remoteICECandidateUsernameFragment(embedded); got != "embedded-generation" {
		t.Fatalf("embedded username fragment=%q, want embedded-generation", got)
	}
	if got := remoteICECandidateUsernameFragment(unscoped); got != "" {
		t.Fatalf("truly unscoped candidate fragment=%q, want blank", got)
	}
}

func TestRemoteDescriptionICEUfragsCollectsSessionAndMediaValues(t *testing.T) {
	description := &webrtc.SessionDescription{SDP: strings.Join([]string{
		"v=0",
		"a=ice-ufrag:session-generation",
		"m=video 9 UDP/TLS/RTP/SAVPF 96",
		"a=ice-ufrag:video-generation",
		"m=audio 9 UDP/TLS/RTP/SAVPF 111",
		"a=ice-ufrag:audio-generation",
	}, "\r\n")}
	got := remoteDescriptionICEUfrags(description)
	for _, want := range []string{"session-generation", "video-generation", "audio-generation"} {
		if _, ok := got[want]; !ok {
			t.Errorf("remote description ufrags=%v, missing %q", got, want)
		}
	}
	if len(got) != 3 {
		t.Fatalf("remote description ufrags=%v, want exactly 3", got)
	}
}

func TestPendingRemoteICECandidateQueueDeduplicatesAndStaysBounded(t *testing.T) {
	queue := pendingRemoteICECandidateQueue{}
	first := webrtc.ICECandidateInit{Candidate: "candidate:0", UsernameFragment: candidateStringPointer("generation")}
	if queued, evicted := queue.enqueue(first); !queued || evicted {
		t.Fatalf("first enqueue queued=%t evicted=%t, want true/false", queued, evicted)
	}
	if queued, evicted := queue.enqueue(first); queued || evicted {
		t.Fatalf("duplicate enqueue queued=%t evicted=%t, want false/false", queued, evicted)
	}

	for i := 1; i <= maxPendingRemoteICECandidates; i++ {
		candidate := webrtc.ICECandidateInit{
			Candidate:        fmt.Sprintf("candidate:%d", i),
			UsernameFragment: candidateStringPointer("generation"),
		}
		queued, evicted := queue.enqueue(candidate)
		if !queued {
			t.Fatalf("unique candidate %d was coalesced", i)
		}
		if (i == maxPendingRemoteICECandidates) != evicted {
			t.Fatalf("candidate %d evicted=%t, want %t", i, evicted, i == maxPendingRemoteICECandidates)
		}
	}
	if len(queue.candidates) != maxPendingRemoteICECandidates {
		t.Fatalf("pending candidate queue length=%d, want bound %d", len(queue.candidates), maxPendingRemoteICECandidates)
	}
	if queue.candidates[0].Candidate == first.Candidate {
		t.Fatal("bounded queue retained the oldest candidate instead of the newest window")
	}
}

func TestPendingRemoteICECandidateQueueFlushesOnlyAcceptedAnswerGeneration(t *testing.T) {
	queue := pendingRemoteICECandidateQueue{}
	for _, candidate := range []webrtc.ICECandidateInit{
		{Candidate: "candidate:new", UsernameFragment: candidateStringPointer("new-generation")},
		{Candidate: "candidate:media", UsernameFragment: candidateStringPointer("media-generation")},
		{Candidate: "candidate:stale", UsernameFragment: candidateStringPointer("old-generation")},
		{Candidate: "candidate:unscoped"},
	} {
		queue.enqueue(candidate)
	}
	description := &webrtc.SessionDescription{SDP: "v=0\r\na=ice-ufrag:new-generation\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=ice-ufrag:media-generation\r\n"}

	matching, discarded := queue.takeMatching(description)
	if len(matching) != 3 || matching[0].Candidate != "candidate:new" || matching[1].Candidate != "candidate:media" || matching[2].Candidate != "candidate:unscoped" {
		t.Fatalf("matching candidates=%v, want new/media generations plus legacy unscoped candidate", matching)
	}
	if discarded != 1 {
		t.Fatalf("discarded candidates=%d, want only the explicit stale generation (1)", discarded)
	}
	if len(queue.candidates) != 0 {
		t.Fatalf("flush retained %d queued candidates", len(queue.candidates))
	}
}

func TestWebsocketCandidateHandlerUsesGenerationAwareQueue(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	main := string(source)
	for _, want := range []string{
		"remoteICECandidateShouldQueue(candidate, peerConnection.RemoteDescription())",
		"pendingRemoteCandidates.enqueue(candidate)",
		"pendingRemoteCandidates.takeMatching(peerConnection.RemoteDescription())",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("candidate handler is missing generation-aware wiring %q", want)
		}
	}
}

func candidateStringPointer(value string) *string {
	return &value
}
