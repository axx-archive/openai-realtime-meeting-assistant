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

// annexB3 wraps NAL units with 3-byte (0x000001) Annex-B start codes — the
// short form of the delimiter that annexB emits in its 4-byte form.
func annexB3(nalus ...[]byte) []byte {
	var buf []byte
	for _, n := range nalus {
		buf = append(buf, 0x00, 0x00, 0x01)
		buf = append(buf, n...)
	}
	return buf
}

// h265NALUFull builds a NAL unit with explicit F / LayerID / TID header fields,
// for exercising the Aggregation Packet header's cross-unit F/LayerID/TID math.
func h265NALUFull(naluType uint8, f bool, layerID, tid uint8, bodyLen int) []byte {
	var header uint16
	if f {
		header |= 1 << 15
	}
	header |= uint16(naluType&0x3F) << 9
	header |= uint16(layerID&0x3F) << 3
	header |= uint16(tid & 0x07)
	nalu := make([]byte, h265NALUHeaderSize+bodyLen)
	binary.BigEndian.PutUint16(nalu[:h265NALUHeaderSize], header)
	for i := 0; i < bodyLen; i++ {
		nalu[h265NALUHeaderSize+i] = byte((int(naluType) + i) % 251)
	}
	return nalu
}

func packetsEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// reassembleH265 walks emitted RTP payloads back into the original ordered list
// of NAL units, coping with all three packet forms the payloader emits
// (single-NALU, Aggregation Packet, Fragmentation Unit). It round-trips through
// Pion's reference depacketizers, so a mismatch means the payloader produced
// something the parse side cannot reconstruct.
func reassembleH265(t *testing.T, packets [][]byte) [][]byte {
	t.Helper()
	var out [][]byte
	var fuHeader []byte // reconstructed 2-byte original NAL header for the in-flight FU
	var fuBuf []byte
	for i, pkt := range packets {
		if len(pkt) < h265NALUHeaderSize {
			t.Fatalf("packet %d is %d bytes, too short for a NAL header", i, len(pkt))
		}
		switch codecs.H265NALUHeader(binary.BigEndian.Uint16(pkt[:h265NALUHeaderSize])).Type() {
		case h265PayloadTypeAP:
			ap := &codecs.H265AggregationPacket{}
			if _, err := ap.Unmarshal(pkt); err != nil {
				t.Fatalf("packet %d: aggregation unmarshal: %v", i, err)
			}
			out = append(out, ap.FirstUnit().NalUnit())
			for _, u := range ap.OtherUnits() {
				out = append(out, u.NalUnit())
			}
		case h265PayloadTypeFU:
			fu := &codecs.H265FragmentationUnitPacket{}
			if _, err := fu.Unmarshal(pkt); err != nil {
				t.Fatalf("packet %d: fragmentation unmarshal: %v", i, err)
			}
			if fu.FuHeader().S() {
				// Rebuild the original NAL header: the FU payload header carries
				// F/LayerID/TID; its Type field is swapped back for the FU type.
				payloadHdr := binary.BigEndian.Uint16(pkt[:h265NALUHeaderSize])
				orig := (payloadHdr &^ (uint16(h265NALUTypeFieldMask) << 9)) | (uint16(fu.FuHeader().FuType()) << 9)
				fuHeader = []byte{byte(orig >> 8), byte(orig)}
				fuBuf = nil
			}
			fuBuf = append(fuBuf, fu.Payload()...)
			if fu.FuHeader().E() {
				out = append(out, append(append([]byte{}, fuHeader...), fuBuf...))
				fuHeader, fuBuf = nil, nil
			}
		default:
			// Single-NALU packet: the payload is the NAL unit verbatim.
			out = append(out, append([]byte{}, pkt...))
		}
	}
	return out
}

// TestH265PayloaderThreeByteStartCodes proves the NAL splitter treats 3-byte
// start codes — and a stream that mixes 3- and 4-byte delimiters — identically
// to the canonical 4-byte form, and that the result still round-trips.
func TestH265PayloaderThreeByteStartCodes(t *testing.T) {
	a := h265NALU(32 /* VPS */, 11)
	b := h265NALU(33 /* SPS */, 13)
	c := h265NALU(34 /* PPS */, 9)

	want := (&H265Payloader{}).Payload(1200, annexB(a, b, c))

	if got := (&H265Payloader{}).Payload(1200, annexB3(a, b, c)); !packetsEqual(want, got) {
		t.Fatal("3-byte start codes produced different packets than the 4-byte form")
	}

	// A single stream mixing 4-byte, 3-byte, then 4-byte delimiters.
	var mixed []byte
	mixed = append(mixed, 0x00, 0x00, 0x00, 0x01)
	mixed = append(mixed, a...)
	mixed = append(mixed, 0x00, 0x00, 0x01)
	mixed = append(mixed, b...)
	mixed = append(mixed, 0x00, 0x00, 0x00, 0x01)
	mixed = append(mixed, c...)
	if got := (&H265Payloader{}).Payload(1200, mixed); !packetsEqual(want, got) {
		t.Fatal("mixed 3/4-byte start codes produced different packets than the 4-byte form")
	}

	if got := reassembleH265(t, (&H265Payloader{}).Payload(1200, annexB3(a, b, c))); !packetsEqual(got, [][]byte{a, b, c}) {
		t.Fatal("3-byte-delimited stream did not round-trip through the depacketizer")
	}
}

// TestH265PayloaderRawNALUNoStartCode feeds a bare NAL unit with no Annex-B
// framing: it must become a single, byte-identical single-NALU packet.
func TestH265PayloaderRawNALUNoStartCode(t *testing.T) {
	nalu := h265NALU(1 /* TRAIL_R */, 40)
	packets := (&H265Payloader{}).Payload(1200, nalu)
	if len(packets) != 1 {
		t.Fatalf("got %d packets, want 1 single-NALU packet", len(packets))
	}
	if !bytes.Equal(packets[0], nalu) {
		t.Fatal("single-NALU packet is not byte-identical to the bare NAL unit")
	}
	single := &codecs.H265SingleNALUnitPacket{}
	if _, err := single.Unmarshal(packets[0]); err != nil {
		t.Fatalf("Pion failed to depacketize the bare NALU: %v", err)
	}
	if !bytes.Equal(single.Payload(), nalu[h265NALUHeaderSize:]) {
		t.Fatal("bare NALU payload did not round-trip")
	}
}

// TestH265PayloaderTinyMTU pins the mtu<=3 guard (no room for even a 1-byte FU
// fragment) and confirms mtu=4 — the smallest workable MTU — still fragments
// into valid, reassemble-able FUs.
func TestH265PayloaderTinyMTU(t *testing.T) {
	p := &H265Payloader{}
	nalu := h265NALU(19 /* IDR_W_RADL */, 40)

	if got := p.Payload(3, annexB(nalu)); got != nil {
		t.Fatalf("Payload(mtu=3) = %v, want nil", got)
	}
	if got := p.Payload(0, annexB(nalu)); got != nil {
		t.Fatalf("Payload(mtu=0) = %v, want nil", got)
	}

	packets := p.Payload(4, annexB(nalu))
	if len(packets) < 2 {
		t.Fatalf("got %d packets, expected fragmentation at mtu=4", len(packets))
	}
	for i, pkt := range packets {
		if len(pkt) > 4 {
			t.Fatalf("fragment %d is %d bytes, exceeds mtu=4", i, len(pkt))
		}
	}
	if got := reassembleH265(t, packets); len(got) != 1 || !bytes.Equal(got[0], nalu) {
		t.Fatal("mtu=4 fragments did not reassemble to the original NAL unit")
	}
}

// TestH265PayloaderAPHeaderSemantics pins RFC 7798 §4.4.2: the Aggregation
// Packet header's F bit is the OR of the units' F bits, while LayerID and TID
// take the minimum across units. (This AP intentionally carries F=1, which the
// AP depacketizer rejects as corrupt, so the header is parsed raw.)
func TestH265PayloaderAPHeaderSemantics(t *testing.T) {
	a := h265NALUFull(32, false, 5, 6, 10)
	b := h265NALUFull(33, true, 2, 4, 12)
	c := h265NALUFull(34, false, 3, 1, 8)

	packets := (&H265Payloader{}).Payload(1200, annexB(a, b, c))
	if len(packets) != 1 {
		t.Fatalf("got %d packets, want 1 aggregation packet", len(packets))
	}

	hdr := codecs.H265NALUHeader(binary.BigEndian.Uint16(packets[0][:h265NALUHeaderSize]))
	if got := hdr.Type(); got != h265PayloadTypeAP {
		t.Fatalf("AP header Type=%d, want %d", got, h265PayloadTypeAP)
	}
	if !hdr.F() {
		t.Fatal("AP header F bit must be the OR of the units (one unit had F=1)")
	}
	if got := hdr.LayerID(); got != 2 {
		t.Fatalf("AP header LayerID=%d, want min 2", got)
	}
	if got := hdr.TID(); got != 1 {
		t.Fatalf("AP header TID=%d, want min 1", got)
	}
}

// TestH265PayloaderNeverExceedsMTU sweeps a mix of tiny/mid/oversized NAL units
// — including one sized exactly to the MTU, which must stay a single-NALU packet
// rather than an FU (the fragmentation boundary is a strict len(nalu) > maxLen)
// — across several MTUs, asserting no packet ever exceeds the MTU and every
// input NAL unit reassembles intact.
func TestH265PayloaderNeverExceedsMTU(t *testing.T) {
	for _, mtu := range []uint16{4, 12, 40, 200, 1200} {
		boundary := h265NALU(19, int(mtu)-h265NALUHeaderSize) // len(nalu) == mtu
		bp := (&H265Payloader{}).Payload(mtu, annexB(boundary))
		if len(bp) != 1 {
			t.Fatalf("mtu=%d: boundary NALU produced %d packets, want 1", mtu, len(bp))
		}
		if len(bp[0]) > int(mtu) {
			t.Fatalf("mtu=%d: boundary packet is %d bytes, exceeds mtu", mtu, len(bp[0]))
		}
		if codecs.H265NALUHeader(binary.BigEndian.Uint16(bp[0][:h265NALUHeaderSize])).IsFragmentationUnit() {
			t.Fatalf("mtu=%d: boundary NALU was fragmented, want a single-NALU packet", mtu)
		}
		if got := reassembleH265(t, bp); len(got) != 1 || !bytes.Equal(got[0], boundary) {
			t.Fatalf("mtu=%d: boundary NALU did not round-trip", mtu)
		}

		nalus := [][]byte{
			h265NALU(32, 3),
			h265NALU(33, int(mtu)/2),
			h265NALU(19, int(mtu)*3), // forces fragmentation
			h265NALU(34, int(mtu)-h265NALUHeaderSize),
			h265NALU(1, 5),
		}
		packets := (&H265Payloader{}).Payload(mtu, annexB(nalus...))
		for i, pkt := range packets {
			if len(pkt) > int(mtu) {
				t.Fatalf("mtu=%d: packet %d is %d bytes, exceeds mtu", mtu, i, len(pkt))
			}
		}
		if got := reassembleH265(t, packets); !packetsEqual(got, nalus) {
			t.Fatalf("mtu=%d: reassembled %d NAL units, want the %d input units", mtu, len(got), len(nalus))
		}
	}
}
