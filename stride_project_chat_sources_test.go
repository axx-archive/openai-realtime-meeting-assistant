package main

import (
	"bytes"
	"testing"
)

func TestStableHomeProjectChoiceKeySeparatesCreateTitlesAndReplays(t *testing.T) {
	snapshot := projectChatSnapshotFixture(t)
	key := StrideE10TenantAuthorityEnvelopeKey{ID: "choice_key_test", Version: 1, Secret: bytes.Repeat([]byte{7}, 32)}
	alpha := stableHomeProjectChoiceKey(key, snapshot, "create", homeProjectRow{Title: " Project Alpha "})
	alphaReplay := stableHomeProjectChoiceKey(key, snapshot, "create", homeProjectRow{Title: "project alpha"})
	beta := stableHomeProjectChoiceKey(key, snapshot, "create", homeProjectRow{Title: "Project Beta"})
	if alpha == "" || alpha != alphaReplay || alpha == beta {
		t.Fatalf("create choice keys alpha=%q replay=%q beta=%q", alpha, alphaReplay, beta)
	}
}
