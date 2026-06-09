package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// h265NALU builds a 2-byte-header H.265 NAL unit of the given type with a body
// of the requested length filled with a recognizable pattern.
func h265NALU(naluType uint8, bodyLen int) []byte {
	header := (uint16(naluType&0x3F) << 9) // F=0, LayerID=0, TID=0
	nalu := make([]byte, h265NALUHeaderSize+bodyLen)
	binary.BigEndian.PutUint16(nalu[:h265NALUHeaderSize], header)
	for i := 0; i < bodyLen; i++ {
		nalu[h265NALUHeaderSize+i] = byte((int(naluType) + i) % 251)
	}
	return nalu
}

func annexB(nalus ...[]byte) []byte {
	var buf []byte
	for _, n := range nalus {
		buf = append(buf, 0x00, 0x00, 0x00, 0x01)
		buf = append(buf, n...)
	}
	return buf
}

func TestH265PayloaderEmptyPayload(t *testing.T) {
	p := &H265Payloader{}
	if got := p.Payload(1200, nil); got != nil {
		t.Fatalf("nil payload: got %v, want nil", got)
	}
	if got := p.Payload(1200, []byte{}); got != nil {
		t.Fatalf("empty payload: got %v, want nil", got)
	}
}

func TestH265PayloaderSingleSmallNALU(t *testing.T) {
	nalu := h265NALU(1 /* TRAIL_R, a VCL type */, 40)
	packets := (&H265Payloader{}).Payload(1200, annexB(nalu))
	if len(packets) != 1 {
		t.Fatalf("got %d packets, want 1 single-NALU packet", len(packets))
	}

	single := &codecs.H265SingleNALUnitPacket{}
	if _, err := single.Unmarshal(packets[0]); err != nil {
		t.Fatalf("Pion failed to depacketize single NALU: %v", err)
	}
	if !bytes.Equal(single.Payload(), nalu[h265NALUHeaderSize:]) {
		t.Fatal("single NALU payload did not round-trip")
	}
}

func TestH265PayloaderAggregatesSmallNALUs(t *testing.T) {
	a := h265NALU(32 /* VPS */, 10)
	b := h265NALU(33 /* SPS */, 12)
	c := h265NALU(34 /* PPS */, 8)
	packets := (&H265Payloader{}).Payload(1200, annexB(a, b, c))
	if len(packets) != 1 {
		t.Fatalf("got %d packets, want 1 aggregation packet", len(packets))
	}

	ap := &codecs.H265AggregationPacket{}
	if _, err := ap.Unmarshal(packets[0]); err != nil {
		t.Fatalf("Pion failed to depacketize aggregation packet: %v", err)
	}
	first := ap.FirstUnit()
	if first == nil {
		t.Fatal("aggregation packet missing first unit")
	}
	if !bytes.Equal(first.NalUnit(), a) {
		t.Fatal("first aggregated NAL unit did not round-trip")
	}
	others := ap.OtherUnits()
	if len(others) != 2 {
		t.Fatalf("got %d trailing aggregation units, want 2", len(others))
	}
	if !bytes.Equal(others[0].NalUnit(), b) || !bytes.Equal(others[1].NalUnit(), c) {
		t.Fatal("trailing aggregated NAL units did not round-trip")
	}
}

func TestH265PayloaderDisableAggregationEmitsSingles(t *testing.T) {
	a := h265NALU(32, 10)
	b := h265NALU(33, 12)
	packets := (&H265Payloader{DisableAggregation: true}).Payload(1200, annexB(a, b))
	if len(packets) != 2 {
		t.Fatalf("got %d packets, want 2 single-NALU packets", len(packets))
	}
	for i, want := range [][]byte{a, b} {
		single := &codecs.H265SingleNALUnitPacket{}
		if _, err := single.Unmarshal(packets[i]); err != nil {
			t.Fatalf("packet %d not a single NALU: %v", i, err)
		}
		if !bytes.Equal(single.Payload(), want[h265NALUHeaderSize:]) {
			t.Fatalf("packet %d payload did not round-trip", i)
		}
	}
}

func TestH265PayloaderFragmentsLargeNALU(t *testing.T) {
	const mtu = 200
	nalu := h265NALU(19 /* IDR_W_RADL, VCL */, 700) // far larger than MTU
	packets := (&H265Payloader{}).Payload(mtu, annexB(nalu))
	if len(packets) < 2 {
		t.Fatalf("got %d packets, expected the NALU to be fragmented", len(packets))
	}

	var reassembled []byte
	for i, pkt := range packets {
		if len(pkt) > mtu {
			t.Fatalf("fragment %d is %d bytes, exceeds MTU %d", i, len(pkt), mtu)
		}
		fu := &codecs.H265FragmentationUnitPacket{}
		if _, err := fu.Unmarshal(pkt); err != nil {
			t.Fatalf("fragment %d not a valid FU: %v", i, err)
		}
		s, e := fu.FuHeader().S(), fu.FuHeader().E()
		if i == 0 && !s {
			t.Fatal("first fragment must set the Start bit")
		}
		if i != 0 && s {
			t.Fatalf("fragment %d wrongly set the Start bit", i)
		}
		if i == len(packets)-1 && !e {
			t.Fatal("last fragment must set the End bit")
		}
		if i != len(packets)-1 && e {
			t.Fatalf("fragment %d wrongly set the End bit", i)
		}
		if got := fu.FuHeader().FuType(); got != 19 {
			t.Fatalf("fragment %d FuType=%d, want 19", i, got)
		}
		reassembled = append(reassembled, fu.Payload()...)
	}

	if !bytes.Equal(reassembled, nalu[h265NALUHeaderSize:]) {
		t.Fatal("reassembled fragments did not reconstruct the original NAL unit body")
	}
}

// TestH265PayloaderMarkerBitViaPacketizer proves marker-bit handling: when the
// payloader is driven by rtp.Packetizer, only the final packet of an access unit
// carries the marker bit.
func TestH265PayloaderMarkerBitViaPacketizer(t *testing.T) {
	const mtu = 200
	nalu := h265NALU(19, 700)
	packetizer := rtp.NewPacketizer(mtu, 98, 0x1234ABCD, &H265Payloader{}, rtp.NewRandomSequencer(), 90000)

	packets := packetizer.Packetize(annexB(nalu), 3000)
	if len(packets) < 2 {
		t.Fatalf("expected multiple RTP packets, got %d", len(packets))
	}
	for i, pkt := range packets {
		wantMarker := i == len(packets)-1
		if pkt.Marker != wantMarker {
			t.Fatalf("packet %d Marker=%v, want %v", i, pkt.Marker, wantMarker)
		}
	}
}

func TestH265PayloaderFlushesPendingBeforeFragment(t *testing.T) {
	const mtu = 200
	small := h265NALU(32, 20)
	large := h265NALU(19, 700)
	// A small NALU followed by an oversized one: the small must be emitted first
	// (as a single packet), then the large NALU's fragments — preserving order.
	packets := (&H265Payloader{}).Payload(mtu, annexB(small, large))
	if len(packets) < 3 {
		t.Fatalf("got %d packets, want the single + multiple fragments", len(packets))
	}

	single := &codecs.H265SingleNALUnitPacket{}
	if _, err := single.Unmarshal(packets[0]); err != nil {
		t.Fatalf("first packet should be the flushed single NALU: %v", err)
	}
	if !bytes.Equal(single.Payload(), small[h265NALUHeaderSize:]) {
		t.Fatal("flushed single NALU did not round-trip")
	}
	fu := &codecs.H265FragmentationUnitPacket{}
	if _, err := fu.Unmarshal(packets[1]); err != nil {
		t.Fatalf("second packet should be the first fragment of the large NALU: %v", err)
	}
	if !fu.FuHeader().S() {
		t.Fatal("first fragment after flush must set the Start bit")
	}
}
