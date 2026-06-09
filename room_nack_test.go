package main

import "testing"

func TestRoomNackResponderSizeDefaults(t *testing.T) {
	t.Setenv("ROOM_NACK_BUFFER_PACKETS", "")
	if got := roomNackResponderSize(); got != defaultNackResponderPackets {
		t.Fatalf("unset env: got %d, want default %d", got, defaultNackResponderPackets)
	}
}

func TestRoomNackResponderSizeValidPowerOfTwo(t *testing.T) {
	for _, size := range []string{"1", "256", "2048", "32768"} {
		t.Run(size, func(t *testing.T) {
			t.Setenv("ROOM_NACK_BUFFER_PACKETS", size)
			got := roomNackResponderSize()
			if uint64(got) == 0 || got&(got-1) != 0 {
				t.Fatalf("size %s: got %d, expected a power of two", size, got)
			}
			if want := map[string]uint16{"1": 1, "256": 256, "2048": 2048, "32768": 32768}[size]; got != want {
				t.Fatalf("size %s: got %d, want %d", size, got, want)
			}
		})
	}
}

func TestRoomNackResponderSizeRejectsBadValues(t *testing.T) {
	// Zero, non-numeric, non-power-of-two, and over-range values must all fall
	// back to the bounded default rather than enlarging or breaking the buffer.
	for _, bad := range []string{"0", "abc", "1000", "3", "65536", "-8", "1024.5"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("ROOM_NACK_BUFFER_PACKETS", bad)
			if got := roomNackResponderSize(); got != defaultNackResponderPackets {
				t.Fatalf("bad value %q: got %d, want fallback %d", bad, got, defaultNackResponderPackets)
			}
		})
	}
}

func TestStableRoomMediaEngineRegistersNack(t *testing.T) {
	// The retransmission buffer is wired through the media engine builder; a
	// successful build with a NACK-capable video codec confirms registration
	// did not error.
	mediaEngine, registry, err := stableRoomMediaEngine()
	if err != nil {
		t.Fatalf("stableRoomMediaEngine: %v", err)
	}
	if mediaEngine == nil || registry == nil {
		t.Fatal("expected non-nil media engine and interceptor registry")
	}
}
