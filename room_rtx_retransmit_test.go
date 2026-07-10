package main

import (
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// These tests exercise the bounded NACK responder end to end: they build the
// real room interceptor chain (stableRoomMediaEngine -> registry.Build), bind a
// local video stream with RTX repair, push RTP through it, then feed a
// TransportLayerNack through the chain's RTCP reader and assert the responder
// retransmits the nacked packet on the repair SSRC/payload type with the
// original sequence number as the 2-byte OSN prefix (RFC 4588). A companion
// case pins the bounded history: a NACK for a sequence the responder never
// buffered produces no retransmission.

const (
	rtxTestMediaSSRC uint32 = 0x1a2b3c4d
	rtxTestRTXSSRC   uint32 = 0x5e6f7a8b
	rtxTestPrimaryPT uint8  = 102
	rtxTestRTXPT     uint8  = 103
)

// rtxRetransmitHarness wires one local stream through the room interceptor
// chain and captures every RTP packet the chain writes downstream — both the
// original packets and the responder's asynchronous retransmissions.
type rtxRetransmitHarness struct {
	t       *testing.T
	writer  interceptor.RTPWriter
	reader  interceptor.RTCPReader
	pending chan []byte

	mu      sync.Mutex
	written []capturedRTP
}

type capturedRTP struct {
	header  rtp.Header
	payload []byte
}

func newRTXRetransmitHarness(t *testing.T) *rtxRetransmitHarness {
	t.Helper()

	_, registry, err := stableRoomMediaEngine()
	if err != nil {
		t.Fatalf("stableRoomMediaEngine: %v", err)
	}
	chain, err := registry.Build("test")
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	t.Cleanup(func() { _ = chain.Close() })

	h := &rtxRetransmitHarness{t: t, pending: make(chan []byte, 8)}

	capture := interceptor.RTPWriterFunc(
		func(header *rtp.Header, payload []byte, _ interceptor.Attributes) (int, error) {
			// The responder reuses packet buffers after Write returns, so copy.
			buf := make([]byte, len(payload))
			copy(buf, payload)
			h.mu.Lock()
			h.written = append(h.written, capturedRTP{header: *header, payload: buf})
			h.mu.Unlock()

			return len(payload), nil
		})

	info := &interceptor.StreamInfo{
		SSRC:                      rtxTestMediaSSRC,
		SSRCRetransmission:        rtxTestRTXSSRC,
		PayloadType:               rtxTestPrimaryPT,
		PayloadTypeRetransmission: rtxTestRTXPT,
		MimeType:                  webrtc.MimeTypeH264,
		ClockRate:                 90000,
		// The responder only buffers streams that negotiated NACK feedback.
		RTCPFeedback: []interceptor.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}},
	}
	h.writer = chain.BindLocalStream(info, capture)

	// The chain's RTCP reader pulls raw batches queued by feedRTCP; the
	// responder dispatches any TransportLayerNack it observes to a resend
	// goroutine.
	h.reader = chain.BindRTCPReader(interceptor.RTCPReaderFunc(
		func(b []byte, a interceptor.Attributes) (int, interceptor.Attributes, error) {
			raw := <-h.pending
			if a == nil {
				a = interceptor.Attributes{}
			}

			return copy(b, raw), a, nil
		}))

	return h
}

// writeRTP sends one media packet through the chain (SSRC = media SSRC) so the
// responder retains it for possible retransmission.
func (h *rtxRetransmitHarness) writeRTP(seq uint16) {
	h.t.Helper()
	header := &rtp.Header{
		Version:        2,
		SSRC:           rtxTestMediaSSRC,
		PayloadType:    rtxTestPrimaryPT,
		SequenceNumber: seq,
	}
	if _, err := h.writer.Write(header, []byte{0xaa, 0xbb, 0xcc, 0xdd}, interceptor.Attributes{}); err != nil {
		h.t.Fatalf("write rtp seq=%d: %v", seq, err)
	}
}

// feedNack pushes one marshaled TransportLayerNack through the chain's RTCP
// reader, matching how a subscriber-facing sender's inbound RTCP reaches the
// responder in production (see forwardSubscriberRTCP).
func (h *rtxRetransmitHarness) feedNack(seq uint16) {
	h.t.Helper()
	raw, err := rtcp.Marshal([]rtcp.Packet{&rtcp.TransportLayerNack{
		MediaSSRC:  rtxTestMediaSSRC,
		SenderSSRC: rtxTestRTXSSRC,
		Nacks:      []rtcp.NackPair{{PacketID: seq, LostPackets: 0}},
	}})
	if err != nil {
		h.t.Fatalf("marshal nack: %v", err)
	}
	h.pending <- raw
	if _, _, err := h.reader.Read(make([]byte, 1500), interceptor.Attributes{}); err != nil {
		h.t.Fatalf("rtcp read: %v", err)
	}
}

// resentPacketFor reports whether the capture holds a retransmission of the
// given original sequence number on the RTX SSRC/payload type.
func (h *rtxRetransmitHarness) resentPacketFor(originalSeq uint16) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, w := range h.written {
		if w.header.SSRC == rtxTestRTXSSRC &&
			w.header.PayloadType == rtxTestRTXPT &&
			len(w.payload) >= 2 &&
			binary.BigEndian.Uint16(w.payload) == originalSeq {
			return true
		}
	}

	return false
}

func TestRoomNackTriggersRTXRetransmit(t *testing.T) {
	h := newRTXRetransmitHarness(t)

	for _, seq := range []uint16{100, 101, 102, 103, 104} {
		h.writeRTP(seq)
	}

	const nacked uint16 = 102
	h.feedNack(nacked)

	// The responder resends on its own goroutine; poll with a deadline (same
	// idiom as goal_engine_test.go / listen_only_test.go).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.resentPacketFor(nacked) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no RTX retransmission of seq %d observed within deadline (SSRC=%#x PT=%d)", nacked, rtxTestRTXSSRC, rtxTestRTXPT)
}

func TestRoomNackForUnbufferedSeqDoesNotRetransmit(t *testing.T) {
	h := newRTXRetransmitHarness(t)

	for _, seq := range []uint16{100, 101, 102, 103, 104} {
		h.writeRTP(seq)
	}

	// 500 was never written, so the bounded history holds nothing to resend.
	const unsent uint16 = 500
	h.feedNack(unsent)

	// Give the resend goroutine time to run, then confirm it produced nothing.
	time.Sleep(250 * time.Millisecond)
	if h.resentPacketFor(unsent) {
		t.Fatalf("responder retransmitted seq %d that was never buffered", unsent)
	}
}
