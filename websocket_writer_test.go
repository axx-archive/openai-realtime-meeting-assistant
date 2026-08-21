package main

import (
	"testing"
	"time"
)

func TestThreadSafeWriterFailsClosedAfterFirstWriteError(t *testing.T) {
	writer := mediaSoakTestWriter(t)
	// Close the transport underneath the wrapper to reproduce a peer that
	// disappeared before the room fan-out observed it.
	if err := writer.Conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteJSON(map[string]any{"event": "first"}); err == nil {
		t.Fatal("first write to closed transport unexpectedly succeeded")
	}
	if !writer.failed.Load() {
		t.Fatal("failed writer was not marked terminal")
	}

	started := time.Now()
	if err := writer.WriteJSON(map[string]any{"event": "second"}); err == nil {
		t.Fatal("terminal writer accepted a queued follow-up write")
	}
	if elapsed := time.Since(started); elapsed > 20*time.Millisecond {
		t.Fatalf("terminal writer took %s to reject a queued write", elapsed)
	}
}
