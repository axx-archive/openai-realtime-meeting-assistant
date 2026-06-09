package main

import (
	"bytes"
	"encoding/binary"
)

// HEVC (H.265) RTP payloader — RFC 7798.
//
// This completes the outbound HEVC packetization the meeting board's
// "Finish RTP HEVC Packetizer" item calls for: NAL-unit fragmentation,
// aggregation, and marker-bit handling. Pion ships an H.265 *depacketizer*
// (codecs.H265*Packet, parse side) but no payloader, so the send-side logic
// below is the missing piece.
//
// H265Payloader implements the github.com/pion/rtp/codecs.Payloader interface
// (Payload(mtu uint16, payload []byte) [][]byte), so it can be plugged straight
// into rtp.NewPacketizer — which sets the RTP marker bit on the final packet of
// each access unit, giving correct marker-bit handling for outbound streams.
//
// It is intentionally NOT registered in the live media engine: the SFU forwards
// inbound video RTP verbatim and has no outbound H.265 encode path yet, so there
// is nothing to feed it in production. Wiring it requires that encode/transcode
// pipeline first. Until then this is a verified, ready-to-use component (see
// hevc_packetizer_test.go, which round-trips every output through Pion's
// reference H.265 depacketizer).

const (
	h265NALUHeaderSize    = 2  // bytes; H.265 NAL unit header (RFC 7798 §1.1.4)
	h265FUHeaderSize      = 1  // bytes; Fragmentation Unit header (§4.4.3)
	h265APLengthFieldSize = 2  // bytes; per-NAL size prefix inside an AP (§4.4.2)
	h265PayloadTypeAP     = 48 // Aggregation Packet
	h265PayloadTypeFU     = 49 // Fragmentation Unit
	h265NALUTypeFieldMask = 0x3F
)

// h265AnnexBStartCode is the 3-byte NAL unit start code; Pion's NAL splitter
// also handles the 4-byte (0x00000001) form.
var h265AnnexBStartCode = []byte{0x00, 0x00, 0x01}

// H265Payloader packetizes one HEVC access unit (an Annex-B / start-code
// delimited NAL stream) into RTP payloads.
type H265Payloader struct {
	// DisableAggregation emits every small NAL unit as its own single-NALU
	// packet instead of combining adjacent ones into an Aggregation Packet.
	DisableAggregation bool
}

// Payload fragments and/or aggregates an HEVC access unit into RTP payloads.
func (p *H265Payloader) Payload(mtu uint16, payload []byte) [][]byte {
	maxLen := int(mtu)
	if len(payload) == 0 || maxLen <= h265NALUHeaderSize+h265FUHeaderSize {
		return nil
	}

	var nalus [][]byte
	emitH265NALUs(payload, func(nalu []byte) {
		if len(nalu) >= h265NALUHeaderSize {
			nalus = append(nalus, nalu)
		}
	})

	var out [][]byte
	var pending [][]byte
	pendingLen := 0 // serialized AP length, including the 2-byte AP header

	flush := func() {
		switch len(pending) {
		case 0:
			return
		case 1:
			// A lone NAL unit is sent as a single-NALU packet (no AP overhead).
			out = append(out, cloneBytes(pending[0]))
		default:
			out = append(out, buildH265AggregationPacket(pending))
		}
		pending = nil
		pendingLen = 0
	}

	for _, nalu := range nalus {
		// A NAL unit too large to fit must be fragmented; flush any aggregation
		// first so packet ordering matches the bitstream.
		if len(nalu) > maxLen {
			flush()
			out = append(out, fragmentH265NALU(nalu, maxLen)...)
			continue
		}

		if p.DisableAggregation {
			out = append(out, cloneBytes(nalu))
			continue
		}

		unitLen := h265APLengthFieldSize + len(nalu)
		projected := pendingLen
		if projected == 0 {
			projected = h265NALUHeaderSize // AP header is added once a 2nd unit joins
		}
		if projected+unitLen > maxLen {
			flush()
		}
		if pendingLen == 0 {
			pendingLen = h265NALUHeaderSize
		}
		pending = append(pending, nalu)
		pendingLen += unitLen
	}
	flush()

	return out
}

// fragmentH265NALU splits a single NAL unit into Fragmentation Unit packets
// (RFC 7798 §4.4.3): PayloadHdr (type 49) + FU header (S/E bits + original NAL
// type) + a slice of the NAL unit payload (its 2-byte header excluded).
func fragmentH265NALU(nalu []byte, maxLen int) [][]byte {
	header := binary.BigEndian.Uint16(nalu[:h265NALUHeaderSize])
	origType := uint8((header >> 9) & h265NALUTypeFieldMask)

	// PayloadHdr keeps F/LayerID/TID, replacing the Type field with 49 (FU).
	fuPayloadHdr := (header &^ (uint16(h265NALUTypeFieldMask) << 9)) | (uint16(h265PayloadTypeFU) << 9)
	hi, lo := byte(fuPayloadHdr>>8), byte(fuPayloadHdr)

	data := nalu[h265NALUHeaderSize:]
	maxFragment := maxLen - h265NALUHeaderSize - h265FUHeaderSize
	remaining := len(data)
	index := 0

	var out [][]byte
	for remaining > 0 {
		fragment := min(maxFragment, remaining)

		pkt := make([]byte, 0, h265NALUHeaderSize+h265FUHeaderSize+fragment)
		pkt = append(pkt, hi, lo)

		fuHeader := origType & h265NALUTypeFieldMask
		if index == 0 {
			fuHeader |= 1 << 7 // Start bit
		}
		if remaining-fragment == 0 {
			fuHeader |= 1 << 6 // End bit
		}
		pkt = append(pkt, fuHeader)
		pkt = append(pkt, data[index:index+fragment]...)
		out = append(out, pkt)

		index += fragment
		remaining -= fragment
	}

	return out
}

// buildH265AggregationPacket combines >=2 NAL units into one Aggregation Packet
// (RFC 7798 §4.4.2) with DONL/DOND disabled: AP PayloadHdr (type 48) followed by
// [2-byte size][NAL unit] for each unit. The AP header's F bit is the OR of the
// units' F bits, and LayerID/TID take the minimum across units, per the RFC.
func buildH265AggregationPacket(nalus [][]byte) []byte {
	var fBit uint16
	minLayerID := uint16(h265NALUTypeFieldMask) // 6-bit max, narrowed below
	minTID := uint16(0x07)                      // 3-bit max, narrowed below
	total := h265NALUHeaderSize
	for _, n := range nalus {
		h := binary.BigEndian.Uint16(n[:h265NALUHeaderSize])
		fBit |= h & 0x8000
		if layerID := (h >> 3) & 0x3F; layerID < minLayerID {
			minLayerID = layerID
		}
		if tid := h & 0x07; tid < minTID {
			minTID = tid
		}
		total += h265APLengthFieldSize + len(n)
	}

	apHeader := fBit | (uint16(h265PayloadTypeAP) << 9) | (minLayerID << 3) | minTID

	buf := make([]byte, 0, total)
	buf = append(buf, byte(apHeader>>8), byte(apHeader))
	for _, n := range nalus {
		var size [h265APLengthFieldSize]byte
		binary.BigEndian.PutUint16(size[:], uint16(len(n)))
		buf = append(buf, size[:]...)
		buf = append(buf, n...)
	}

	return buf
}

// emitH265NALUs splits an Annex-B NAL stream into individual NAL units,
// supporting both 3-byte and 4-byte start codes. Mirrors Pion's H.264 splitter.
func emitH265NALUs(stream []byte, emit func([]byte)) {
	start := bytes.Index(stream, h265AnnexBStartCode)
	offset := 3
	if start == -1 {
		emit(stream)
		return
	}

	length := len(stream)
	for start < length {
		end := bytes.Index(stream[start+offset:], h265AnnexBStartCode)
		if end == -1 {
			emit(stream[start+offset:])
			break
		}

		nextStart := start + offset + end
		endIs4Byte := stream[nextStart-1] == 0
		if endIs4Byte {
			nextStart--
		}

		emit(stream[start+offset : nextStart])
		start = nextStart
		if endIs4Byte {
			offset = 4
		} else {
			offset = 3
		}
	}
}

func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
