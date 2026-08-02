package e10probe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	rtProject    = "project-raw-secret"
	rtAPIKey     = "api-key-raw-secret"
	rtSegment    = "segment_raw_secret_0001"
	rtSessionID  = "sess_provider_raw_secret"
	rtItemID     = "item_provider_raw_secret"
	rtTranscript = "The provider raw transcript must only survive as a hash."
)

type rtFixture struct {
	config    RealtimeTranscribeConfig
	pcm       []byte
	reference string
}

func TestRunRealtimeTranscriptionHappyPathIsExactCorrelatedAndBodyFree(t *testing.T) {
	fixture := rtNewFixture(t, nil)
	server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
		rtServeConfiguredTurn(t, conn, fixture.pcm)
		rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
		rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
		rtWriteJSON(t, conn, map[string]any{
			"type": "conversation.item.input_audio_transcription.delta", "event_id": "evt_delta_raw_secret",
			"item_id": rtItemID, "content_index": 0, "delta": "The provider",
		})
		rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
	})
	defer server.close(t)
	fixture.config.WebSocketURL = server.url

	receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
	if err != nil {
		t.Fatalf("RunRealtimeTranscription: %v", err)
	}
	if !receipt.Success || receipt.Outcome != "pass" || !receipt.CorrelationVerified {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if receipt.EventCount != 6 || receipt.EventOrderSHA256 != digest(strings.Join([]string{
		"session.created",
		"session.updated",
		"input_audio_buffer.committed",
		"conversation.item.created",
		"conversation.item.input_audio_transcription.delta",
		"conversation.item.input_audio_transcription.completed",
	}, "\n")) {
		t.Fatalf("event ledger was not exact: %+v", receipt)
	}
	if receipt.PCMDataSHA256 != digestBytes(fixture.pcm) || receipt.TranscriptSHA256 != digest(rtTranscript) {
		t.Fatalf("payload hashes were not bound: %+v", receipt)
	}
	if receipt.CommitItemIDSHA256 == "" || receipt.CommitItemIDSHA256 != receipt.CreatedItemIDSHA256 || receipt.CreatedItemIDSHA256 != receipt.CompletedItemIDSHA256 {
		t.Fatalf("provider item correlation hashes differ: %+v", receipt)
	}
	if !receipt.AttributionVerified || receipt.AttributionState != "provider_verified" || receipt.ReportedUsageType != "tokens" {
		t.Fatalf("attribution or usage was not verified: %+v", receipt)
	}
	if receipt.HTTPStatus != http.StatusSwitchingProtocols || receipt.RequestIDSHA256 != digest("request_provider_raw_secret") {
		t.Fatalf("handshake evidence was not retained safely: %+v", receipt)
	}
	if receipt.ComputedCostUSD <= 0 || receipt.ComputedCostUSD > receipt.MaxUSD {
		t.Fatalf("cost was not bounded: %+v", receipt)
	}

	receiptPath := filepath.Join(fixture.config.ReceiptDir, "receipt.json")
	encoded, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{rtProject, rtAPIKey, rtSegment, rtSessionID, rtItemID, rtTranscript, fixture.reference, base64.StdEncoding.EncodeToString(fixture.pcm), fixedPrompt, "evt_delta_raw_secret"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("receipt retained forbidden raw value %q", secret)
		}
	}
	if mode := rtMustStat(t, fixture.config.ReceiptDir).Mode().Perm(); mode != 0o700 {
		t.Fatalf("receipt directory mode = %o", mode)
	}
	if mode := rtMustStat(t, receiptPath).Mode().Perm(); mode != 0o600 {
		t.Fatalf("receipt file mode = %o", mode)
	}
}

func TestRunRealtimeTranscriptionAcceptsDocumentedOptionalCreatedFields(t *testing.T) {
	fixture := rtNewFixture(t, nil)
	server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
		rtServeConfiguredTurn(t, conn, fixture.pcm)
		rtWriteJSON(t, conn, map[string]any{
			"type": "input_audio_buffer.committed", "event_id": "evt_committed_optional",
			"item_id": rtItemID,
		})
		rtWriteJSON(t, conn, map[string]any{
			"type": "conversation.item.created", "event_id": "evt_created_optional",
			"item": map[string]any{"id": rtItemID, "type": "message", "role": "user", "content": []any{}},
		})
		rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
	})
	defer server.close(t)
	fixture.config.WebSocketURL = server.url
	receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
	if err != nil || !receipt.Success || receipt.Outcome != "pass" || !receipt.CorrelationVerified {
		t.Fatalf("documented optional fields were not accepted: receipt=%+v err=%v", receipt, err)
	}
}

func TestRunRealtimeTranscriptionAcceptsModernAddedDoneLifecycle(t *testing.T) {
	for _, test := range []struct {
		name               string
		completeBeforeDone bool
		expectedEventOrder []string
	}{
		{
			name:               "item finalized before asynchronous transcription",
			expectedEventOrder: []string{"session.created", "session.updated", "input_audio_buffer.committed", "conversation.item.added", "conversation.item.done", "conversation.item.input_audio_transcription.completed"},
		},
		{
			name:               "asynchronous transcription completes before item finalization",
			completeBeforeDone: true,
			expectedEventOrder: []string{"session.created", "session.updated", "input_audio_buffer.committed", "conversation.item.added", "conversation.item.input_audio_transcription.completed", "conversation.item.done"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := rtNewFixture(t, nil)
			server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
				rtServeConfiguredTurn(t, conn, fixture.pcm)
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtAddedItem(rtItemID, nil))
				if test.completeBeforeDone {
					rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
				}
				rtWriteJSON(t, conn, rtDoneItem(rtItemID, nil))
				if !test.completeBeforeDone {
					rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
				}
			})
			defer server.close(t)
			fixture.config.WebSocketURL = server.url
			receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
			if err != nil || !receipt.Success || receipt.Outcome != "pass" || !receipt.CorrelationVerified {
				t.Fatalf("modern item lifecycle failed: receipt=%+v err=%v", receipt, err)
			}
			if receipt.ItemLifecycle != "modern_added_done" || receipt.FinalizedItemIDSHA256 != digest(rtItemID) || receipt.FinalizedItemIDSHA256 != receipt.CompletedItemIDSHA256 {
				t.Fatalf("modern lifecycle receipt was not self-describing: %+v", receipt)
			}
			if receipt.EventCount != len(test.expectedEventOrder) || receipt.EventOrderSHA256 != digest(strings.Join(test.expectedEventOrder, "\n")) {
				t.Fatalf("modern lifecycle event ledger was not exact: %+v", receipt)
			}
		})
	}
}

func TestRunRealtimeTranscriptionRequiresModernDoneBeforeSuccess(t *testing.T) {
	fixture := rtNewFixture(t, nil)
	server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
		rtServeConfiguredTurn(t, conn, fixture.pcm)
		rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
		rtWriteJSON(t, conn, rtAddedItem(rtItemID, nil))
		rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
	})
	defer server.close(t)
	fixture.config.WebSocketURL = server.url
	receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
	if err == nil || receipt.Success || receipt.CorrelationVerified {
		t.Fatalf("modern lifecycle passed without item.done: receipt=%+v err=%v", receipt, err)
	}
}

func TestRunRealtimeTranscriptionRejectsIncompleteMixedAndUnanchoredItemLifecycles(t *testing.T) {
	tests := []struct {
		name    string
		serve   func(*testing.T, *websocket.Conn)
		outcome string
	}{
		{
			name: "added before commit acknowledgement",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtAddedItem(rtItemID, nil))
			},
			outcome: "out_of_order",
		},
		{
			name: "legacy created before commit acknowledgement",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
			},
			outcome: "out_of_order",
		},
		{
			name: "completion before modern added item",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
			},
			outcome: "out_of_order",
		},
		{
			name: "added and done without ASR completion",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtAddedItem(rtItemID, nil))
				rtWriteJSON(t, conn, rtDoneItem(rtItemID, nil))
			},
		},
		{
			name: "duplicate added",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtAddedItem(rtItemID, nil))
				rtWriteJSON(t, conn, rtConversationItemWithEventID("conversation.item.added", rtItemID, nil, "evt_second_added_raw_secret"))
			},
			outcome: "extra_item",
		},
		{
			name: "mixed legacy created and modern added",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
				rtWriteJSON(t, conn, rtAddedItem(rtItemID, nil))
			},
			outcome: "extra_item",
		},
		{
			name: "mixed legacy created and modern done",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
				rtWriteJSON(t, conn, rtDoneItem(rtItemID, nil))
			},
			outcome: "out_of_order",
		},
		{
			name: "modern added item does not match commit",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtAddedItem("different_item_raw_secret", nil))
			},
			outcome: "correlation_mismatch",
		},
		{
			name: "modern added item missing required content",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				event := rtAddedItem(rtItemID, nil)
				delete(event["item"].(map[string]any), "content")
				rtWriteJSON(t, conn, event)
			},
			outcome: "schema_mismatch",
		},
		{
			name: "modern added item has invalid content kind",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				event := rtAddedItem(rtItemID, nil)
				event["item"].(map[string]any)["content"] = []any{map[string]any{"type": "input_text", "text": "not audio"}}
				rtWriteJSON(t, conn, event)
			},
			outcome: "schema_mismatch",
		},
		{
			name: "modern added item invalid previous chain",
			serve: func(t *testing.T, conn *websocket.Conn) {
				previous := "unexpected_prior_item_raw_secret"
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtAddedItem(rtItemID, &previous))
			},
			outcome: "schema_mismatch",
		},
		{
			name: "modern done item invalid previous chain",
			serve: func(t *testing.T, conn *websocket.Conn) {
				previous := "unexpected_prior_item_raw_secret"
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtAddedItem(rtItemID, nil))
				rtWriteJSON(t, conn, rtDoneItem(rtItemID, &previous))
			},
			outcome: "schema_mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := rtNewFixture(t, nil)
			server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
				rtServeConfiguredTurn(t, conn, fixture.pcm)
				test.serve(t, conn)
			})
			defer server.close(t)
			fixture.config.WebSocketURL = server.url
			receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
			if err == nil || receipt.Success || (test.outcome != "" && receipt.Outcome != test.outcome) {
				t.Fatalf("invalid lifecycle accepted: receipt=%+v err=%v", receipt, err)
			}
		})
	}
}

func TestRunRealtimeTranscriptionAcceptsDocumentedInterleavingsAndOptionalUsageDetails(t *testing.T) {
	t.Run("session acknowledgement omits optional response fields", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtWriteJSON(t, conn, map[string]any{
				"type": "session.created", "event_id": "evt_session_created_raw_secret",
				"session": map[string]any{"id": rtSessionID, "type": "transcription"},
			})
			rtReadExactClientEvent(t, conn, realtimeSessionUpdate(rtSegment))
			rtWriteJSON(t, conn, map[string]any{
				"type": "session.updated", "event_id": "evt_session_updated_raw_secret",
				"session": map[string]any{"id": rtSessionID, "type": "transcription"},
			})
			rtReadCommittedAudio(t, conn, fixture.pcm)
			rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
			rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
			rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err != nil || !receipt.Success {
			t.Fatalf("documented optional response omissions were rejected: receipt=%+v err=%v", receipt, err)
		}
	})

	t.Run("session lifecycle event before update acknowledgement", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtWriteJSON(t, conn, map[string]any{
				"type": "session.created", "event_id": "evt_session_created_raw_secret",
				"session": map[string]any{"id": rtSessionID, "type": "transcription"},
			})
			rtReadExactClientEvent(t, conn, realtimeSessionUpdate(rtSegment))
			rtWriteJSON(t, conn, rtConversationCreated())
			rtWriteJSON(t, conn, rtSessionUpdated())
			rtReadCommittedAudio(t, conn, fixture.pcm)
			rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
			rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
			rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err != nil || !receipt.Success || receipt.Outcome != "pass" || !receipt.CorrelationVerified {
			t.Fatalf("documented conversation lifecycle interleaving was not accepted: receipt=%+v err=%v", receipt, err)
		}
	})

	t.Run("completion before created item is rejected", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtServeConfiguredTurn(t, conn, fixture.pcm)
			rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
			rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
			rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err == nil || receipt.Success || receipt.Outcome != "out_of_order" || receipt.FailureClass != "event_order" {
			t.Fatalf("completion before item admission was accepted: receipt=%+v err=%v", receipt, err)
		}
	})

	t.Run("token usage without optional details", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtServeConfiguredTurn(t, conn, fixture.pcm)
			rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
			rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
			usage := rtTokenUsage()
			delete(usage, "input_token_details")
			rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, usage))
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err != nil || !receipt.Success || receipt.ReportedAudioTokens != nil {
			t.Fatalf("optional token details were not handled safely: receipt=%+v err=%v", receipt, err)
		}
	})

	for _, test := range []struct {
		name          string
		details       map[string]any
		expectedAudio *int64
	}{
		{name: "token usage with only text details", details: map[string]any{"text_tokens": 0}},
		{name: "token usage with only audio details", details: map[string]any{"audio_tokens": 13}, expectedAudio: func() *int64 { value := int64(13); return &value }()},
		{name: "token usage with empty optional details", details: map[string]any{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := rtNewFixture(t, nil)
			server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
				rtServeConfiguredTurn(t, conn, fixture.pcm)
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
				usage := rtTokenUsage()
				usage["input_token_details"] = test.details
				rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, usage))
			})
			defer server.close(t)
			fixture.config.WebSocketURL = server.url
			receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
			if err != nil || !receipt.Success {
				t.Fatalf("partial optional token details were not handled safely: receipt=%+v err=%v", receipt, err)
			}
			if test.expectedAudio == nil && receipt.ReportedAudioTokens != nil {
				t.Fatalf("unexpected reported audio tokens: %+v", receipt.ReportedAudioTokens)
			}
			if test.expectedAudio != nil && (receipt.ReportedAudioTokens == nil || *receipt.ReportedAudioTokens != *test.expectedAudio) {
				t.Fatalf("reported audio tokens = %v, want %d", receipt.ReportedAudioTokens, *test.expectedAudio)
			}
		})
	}
}

func TestRunRealtimeTranscriptionRejectsContradictorySessionEcho(t *testing.T) {
	fixture := rtNewFixture(t, nil)
	server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
		rtWriteJSON(t, conn, map[string]any{
			"type": "session.created", "event_id": "evt_session_created_raw_secret",
			"session": map[string]any{"id": rtSessionID, "type": "transcription"},
		})
		rtReadExactClientEvent(t, conn, realtimeSessionUpdate(rtSegment))
		updated := rtSessionUpdated()
		transcription := updated["session"].(map[string]any)["audio"].(map[string]any)["input"].(map[string]any)["transcription"].(map[string]any)
		transcription["model"] = "gpt-live-transcribe"
		rtWriteJSON(t, conn, updated)
	})
	defer server.close(t)
	fixture.config.WebSocketURL = server.url
	receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
	if err == nil || receipt.Outcome != "schema_mismatch" || receipt.FailureClass != "event_schema" {
		t.Fatalf("contradictory documented session echo was accepted: receipt=%+v err=%v", receipt, err)
	}
}

func TestRunRealtimeTranscriptionRejectsDuplicateEventIDsAndTerminals(t *testing.T) {
	t.Run("duplicate server event id", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtServeConfiguredTurn(t, conn, fixture.pcm)
			rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
			rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
			rtWriteJSON(t, conn, map[string]any{
				"type": "conversation.item.input_audio_transcription.delta", "event_id": "evt_item_created_raw_secret",
				"item_id": rtItemID, "content_index": 0, "delta": "duplicate id",
			})
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err == nil || receipt.Outcome != "duplicate_event" || receipt.FailureClass != "event_id" {
			t.Fatalf("duplicate server event ID was accepted: receipt=%+v err=%v", receipt, err)
		}
	})

	t.Run("duplicate terminal in bounded observation window", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtServeConfiguredTurn(t, conn, fixture.pcm)
			rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
			rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
			rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
			rtWriteJSON(t, conn, rtCompletedItemWithEventID(rtItemID, rtTranscript, rtTokenUsage(), "evt_duplicate_terminal_raw_secret"))
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err == nil || receipt.Outcome != "duplicate_terminal" || receipt.FailureClass != "event_terminal" {
			t.Fatalf("duplicate terminal was accepted: receipt=%+v err=%v", receipt, err)
		}
	})

	t.Run("malformed event after terminal", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtServeConfiguredTurn(t, conn, fixture.pcm)
			rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
			rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
			rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtTokenUsage()))
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":`)); err != nil {
				t.Errorf("write malformed post-terminal event: %v", err)
			}
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err == nil || receipt.Outcome != "transport_error" || receipt.FailureClass != "read" {
			t.Fatalf("malformed post-terminal event was accepted: receipt=%+v err=%v", receipt, err)
		}
	})
}

func TestRunRealtimeTranscriptionCancellationClosesEstablishedSocket(t *testing.T) {
	fixture := rtNewFixture(t, nil)
	blocked := make(chan struct{})
	release := make(chan struct{})
	server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
		rtServeConfiguredTurn(t, conn, fixture.pcm)
		close(blocked)
		<-release
	})
	defer server.close(t)
	defer close(release)
	fixture.config.WebSocketURL = server.url
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		receipt RealtimeTranscriptionReceipt
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		receipt, err := RunRealtimeTranscription(ctx, fixture.config)
		resultCh <- result{receipt: receipt, err: err}
	}()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("test server never reached the blocking post-commit read")
	}
	cancel()
	select {
	case got := <-resultCh:
		if got.err == nil || got.receipt.Success || got.receipt.Outcome != "transport_error" || got.receipt.FailureClass != "read" {
			t.Fatalf("cancellation did not close the established probe socket: receipt=%+v err=%v", got.receipt, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not promptly unblock the established WebSocket read")
	}
}

func TestRunRealtimeTranscriptionFailsClosedOnOrderFailureAndProviderError(t *testing.T) {
	t.Run("out of order", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtWriteJSON(t, conn, rtSessionUpdated())
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err == nil || receipt.Success || receipt.Outcome != "out_of_order" || receipt.FailureClass != "event_order" {
			t.Fatalf("expected out-of-order failure, got receipt=%+v err=%v", receipt, err)
		}
	})

	t.Run("provider error is body free", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtServeConfiguredTurn(t, conn, fixture.pcm)
			rtWriteJSON(t, conn, map[string]any{
				"type": "error", "event_id": "evt_error_raw_secret",
				"error": map[string]any{"type": "invalid_request_error", "message": "provider-body-raw-secret"},
			})
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err == nil || receipt.Outcome != "provider_error" || receipt.FailureClass != "provider_event" {
			t.Fatalf("expected provider failure, got receipt=%+v err=%v", receipt, err)
		}
		encoded, readErr := os.ReadFile(filepath.Join(fixture.config.ReceiptDir, "receipt.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(err.Error(), "provider-body-raw-secret") || bytes.Contains(encoded, []byte("provider-body-raw-secret")) {
			t.Fatal("provider error body escaped the body-free boundary")
		}
	})
}

func TestRunRealtimeTranscriptionRejectsCorrelationAndExtraItems(t *testing.T) {
	tests := []struct {
		name    string
		serve   func(*testing.T, *websocket.Conn)
		outcome string
	}{
		{
			name: "modern finalized item mismatch",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtAddedItem(rtItemID, nil))
				rtWriteJSON(t, conn, rtDoneItem("different_item_raw_secret", nil))
			},
			outcome: "correlation_mismatch",
		},
		{
			name: "modern item done before added",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtDoneItem(rtItemID, nil))
			},
			outcome: "out_of_order",
		},
		{
			name: "duplicate modern item done",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtAddedItem(rtItemID, nil))
				rtWriteJSON(t, conn, rtDoneItem(rtItemID, nil))
				rtWriteJSON(t, conn, rtConversationItemWithEventID("conversation.item.done", rtItemID, nil, "evt_second_done_raw_secret"))
			},
			outcome: "extra_item",
		},
		{
			name: "created item mismatch",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtCreatedItem("different_item_raw_secret", nil))
			},
			outcome: "correlation_mismatch",
		},
		{
			name: "completion item mismatch",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
				rtWriteJSON(t, conn, rtCompletedItem("different_item_raw_secret", rtTranscript, rtTokenUsage()))
			},
			outcome: "correlation_mismatch",
		},
		{
			name: "second item",
			serve: func(t *testing.T, conn *websocket.Conn) {
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
				rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
				rtWriteJSON(t, conn, rtCreatedItemWithEventID(rtItemID, nil, "evt_second_item_raw_secret"))
			},
			outcome: "extra_item",
		},
		{
			name: "invalid previous item chain",
			serve: func(t *testing.T, conn *websocket.Conn) {
				previous := "unexpected_prior_item_raw_secret"
				rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, &previous))
			},
			outcome: "schema_mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := rtNewFixture(t, nil)
			server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
				rtServeConfiguredTurn(t, conn, fixture.pcm)
				test.serve(t, conn)
			})
			defer server.close(t)
			fixture.config.WebSocketURL = server.url
			receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
			if err == nil || receipt.Success || receipt.Outcome != test.outcome {
				t.Fatalf("expected %s, got receipt=%+v err=%v", test.outcome, receipt, err)
			}
		})
	}
}

func TestRunRealtimeTranscriptionRejectsStrictUsageAndEventCap(t *testing.T) {
	t.Run("strict usage", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtServeConfiguredTurn(t, conn, fixture.pcm)
			rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
			rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
			usage := rtTokenUsage()
			usage["undocumented_raw_secret"] = 1
			rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, usage))
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err == nil || receipt.Outcome != "schema_mismatch" || receipt.FailureClass != "event_schema" {
			t.Fatalf("strict usage did not fail closed: receipt=%+v err=%v", receipt, err)
		}
	})

	t.Run("event count cap", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			rtServeConfiguredTurn(t, conn, fixture.pcm)
			rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
			rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
			for i := 0; i < MaxRealtimeServerEvents; i++ {
				if err := conn.WriteJSON(map[string]any{
					"type": "conversation.item.input_audio_transcription.delta", "event_id": fmt.Sprintf("evt_delta_%03d", i),
					"item_id": rtItemID, "content_index": 0, "delta": "x",
				}); err != nil {
					return
				}
			}
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err == nil || receipt.Outcome != "event_cap_exceeded" || receipt.FailureClass != "event_cap" || receipt.EventCount != MaxRealtimeServerEvents+1 {
			t.Fatalf("event cap did not fail closed: receipt=%+v err=%v", receipt, err)
		}
	})

	t.Run("event byte cap", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		server := rtNewServer(t, rtProject, func(conn *websocket.Conn) {
			raw := []byte(`{"type":"session.created","event_id":"evt_large","padding":"` + strings.Repeat("x", int(MaxRealtimeServerEventBytes)) + `"}`)
			_ = conn.WriteMessage(websocket.TextMessage, raw)
		})
		defer server.close(t)
		fixture.config.WebSocketURL = server.url
		receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
		if err == nil || receipt.Outcome != "event_cap_exceeded" || receipt.FailureClass != "event_cap" {
			t.Fatalf("event byte cap did not fail closed: receipt=%+v err=%v", receipt, err)
		}
	})
}

func TestRunRealtimeTranscriptionRejectsNonExactFixturesBeforeNetwork(t *testing.T) {
	tests := []struct {
		name string
		wav  []byte
	}{
		{name: "stereo", wav: rtWAV(2, 24000, 16, 100)},
		{name: "wrong rate", wav: rtWAV(1, 16000, 16, 100)},
		{name: "wrong sample width", wav: rtWAV(1, 24000, 8, 100)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := rtNewFixture(t, test.wav)
			fixture.config.WebSocketURL = "ws://127.0.0.1:1/v1/realtime?intent=transcription"
			_, err := RunRealtimeTranscription(context.Background(), fixture.config)
			if err == nil || !strings.Contains(err.Error(), "mono 24 kHz signed 16-bit PCM") {
				t.Fatalf("expected exact PCM rejection, got %v", err)
			}
			if _, statErr := os.Stat(fixture.config.ReceiptDir); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("network/receipt boundary crossed after fixture rejection: %v", statErr)
			}
		})
	}

	t.Run("fixture digest mismatch", func(t *testing.T) {
		fixture := rtNewFixture(t, nil)
		fixture.config.ExpectedFixtureSHA256 = digest("not-the-approved-fixture")
		fixture.config.WebSocketURL = "ws://127.0.0.1:1/v1/realtime?intent=transcription"
		if _, err := RunRealtimeTranscription(context.Background(), fixture.config); err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("expected fixture digest rejection, got %v", err)
		}
	})
}

func TestRunRealtimeTranscriptionSupportsProjectScopedCredentialMode(t *testing.T) {
	fixture := rtNewFixture(t, nil)
	fixture.config.Project = ""
	fixture.config.ExpectedProjectSHA256 = ""
	fixture.config.AllowProjectScopedKey = true
	fixture.config.APIKey = "sk-proj-test-only-credential"
	server := rtNewServer(t, "", func(conn *websocket.Conn) {
		rtServeConfiguredTurn(t, conn, fixture.pcm)
		rtWriteJSON(t, conn, rtCommitAcknowledgement(rtItemID, nil))
		rtWriteJSON(t, conn, rtCreatedItem(rtItemID, nil))
		rtWriteJSON(t, conn, rtCompletedItem(rtItemID, rtTranscript, rtDurationUsage(0.1)))
	})
	defer server.close(t)
	fixture.config.WebSocketURL = server.url
	receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
	if err != nil || !receipt.Success || receipt.CredentialScope != "project_scoped_api_key" || receipt.AttributionState != "project_credential_bound_unreconciled" || receipt.AttributionVerified {
		t.Fatalf("project-scoped mode failed: receipt=%+v err=%v", receipt, err)
	}
}

func TestRunRealtimeTranscriptionDoesNotFollowRedirectAndRejectsForeignHost(t *testing.T) {
	fixture := rtNewFixture(t, nil)
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetHits.Add(1)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	fixture.config.WebSocketURL = "ws" + strings.TrimPrefix(redirect.URL, "http") + "/v1/realtime?intent=transcription"
	receipt, err := RunRealtimeTranscription(context.Background(), fixture.config)
	if err == nil || receipt.FailureClass != "redirect" || targetHits.Load() != 0 {
		t.Fatalf("redirect was not denied: receipt=%+v err=%v targetHits=%d", receipt, err, targetHits.Load())
	}

	foreign := rtNewFixture(t, nil)
	foreign.config.WebSocketURL = "wss://example.com/v1/realtime?intent=transcription"
	if _, err := RunRealtimeTranscription(context.Background(), foreign.config); err == nil || !strings.Contains(err.Error(), "api.openai.com") {
		t.Fatalf("foreign host was not rejected: %v", err)
	}

	ambiguous := rtNewFixture(t, nil)
	ambiguous.config.BaseURL = "http://127.0.0.1:1"
	if _, err := RunRealtimeTranscription(context.Background(), ambiguous.config); err == nil || !strings.Contains(err.Error(), "does not control") {
		t.Fatalf("ambiguous HTTP override was not rejected: %v", err)
	}
}

type rtServer struct {
	server *httptest.Server
	url    string
	done   chan struct{}
}

func rtNewServer(t *testing.T, responseProject string, serve func(*websocket.Conn)) rtServer {
	t.Helper()
	done := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		if r.URL.Path != "/v1/realtime" || r.URL.RawQuery != "intent=transcription" {
			t.Errorf("unexpected websocket target %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		authorization := r.Header.Get("Authorization")
		if authorization != "Bearer "+rtAPIKey && authorization != "Bearer sk-proj-test-only-credential" {
			t.Error("missing or incorrect Authorization header")
		}
		if got := r.Header.Get("OpenAI-Project"); got != responseProject {
			t.Errorf("OpenAI-Project = %q, want %q", got, responseProject)
		}
		headers := make(http.Header)
		headers.Set("X-Request-ID", "request_provider_raw_secret")
		if responseProject != "" {
			headers.Set("OpenAI-Project", responseProject)
		}
		conn, err := upgrader.Upgrade(w, r, headers)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		serve(conn)
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
	})
	server := httptest.NewServer(handler)
	return rtServer{
		server: server,
		url:    "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?intent=transcription",
		done:   done,
	}
}

func (server rtServer) close(t *testing.T) {
	t.Helper()
	server.server.Close()
	select {
	case <-server.done:
	case <-time.After(2 * time.Second):
		t.Error("fake Realtime server did not stop")
	}
}

func rtServeConfiguredTurn(t *testing.T, conn *websocket.Conn, expectedPCM []byte) {
	t.Helper()
	rtWriteJSON(t, conn, map[string]any{
		"type": "session.created", "event_id": "evt_session_created_raw_secret",
		"session": map[string]any{"id": rtSessionID, "type": "transcription"},
	})
	rtReadExactClientEvent(t, conn, realtimeSessionUpdate(rtSegment))
	rtWriteJSON(t, conn, rtSessionUpdated())
	rtReadCommittedAudio(t, conn, expectedPCM)
}

func rtReadCommittedAudio(t *testing.T, conn *websocket.Conn, expectedPCM []byte) {
	t.Helper()
	appendEvent := rtReadClientMap(t, conn)
	if len(appendEvent) != 3 || appendEvent["type"] != "input_audio_buffer.append" || appendEvent["event_id"] != rtSegment+"-append" {
		t.Errorf("append event was not exact: %#v", appendEvent)
		return
	}
	audio, ok := appendEvent["audio"].(string)
	if !ok {
		t.Errorf("append audio was not a string: %#v", appendEvent)
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(audio)
	if err != nil || !bytes.Equal(decoded, expectedPCM) {
		t.Errorf("append did not contain the exact PCM data bytes")
		return
	}
	commitEvent := rtReadClientMap(t, conn)
	if len(commitEvent) != 2 || commitEvent["type"] != "input_audio_buffer.commit" || commitEvent["event_id"] != rtSegment+"-commit" {
		t.Errorf("commit event was not exact: %#v", commitEvent)
	}
}

func rtSessionUpdated() map[string]any {
	update := realtimeSessionUpdate(rtSegment)
	session := update["session"].(map[string]any)
	transcription := session["audio"].(map[string]any)["input"].(map[string]any)["transcription"].(map[string]any)
	delete(transcription, "keywords")
	session["id"] = rtSessionID
	return map[string]any{"type": "session.updated", "event_id": "evt_session_updated_raw_secret", "session": session}
}

func rtConversationCreated() map[string]any {
	return map[string]any{
		"type": "conversation.created", "event_id": "evt_conversation_created_raw_secret",
		"conversation": map[string]any{"id": "conv_provider_raw_secret", "object": "realtime.conversation"},
	}
}

func rtCommitAcknowledgement(itemID string, previous *string) map[string]any {
	var previousValue any
	if previous != nil {
		previousValue = *previous
	}
	return map[string]any{
		"type": "input_audio_buffer.committed", "event_id": "evt_committed_raw_secret",
		"item_id": itemID, "previous_item_id": previousValue,
	}
}

func rtCreatedItem(itemID string, previous *string) map[string]any {
	return rtCreatedItemWithEventID(itemID, previous, "evt_item_created_raw_secret")
}

func rtCreatedItemWithEventID(itemID string, previous *string, eventID string) map[string]any {
	return rtConversationItemWithEventID("conversation.item.created", itemID, previous, eventID)
}

func rtAddedItem(itemID string, previous *string) map[string]any {
	return rtConversationItemWithEventID("conversation.item.added", itemID, previous, "evt_item_added_raw_secret")
}

func rtDoneItem(itemID string, previous *string) map[string]any {
	return rtConversationItemWithEventID("conversation.item.done", itemID, previous, "evt_item_done_raw_secret")
}

func rtConversationItemWithEventID(eventType, itemID string, previous *string, eventID string) map[string]any {
	var previousValue any
	if previous != nil {
		previousValue = *previous
	}
	content := []any{}
	if eventType == "conversation.item.added" || eventType == "conversation.item.done" {
		content = []any{map[string]any{"type": "input_audio"}}
	}
	return map[string]any{
		"type": eventType, "event_id": eventID, "previous_item_id": previousValue,
		"item": map[string]any{"id": itemID, "object": "realtime.item", "type": "message", "status": "completed", "role": "user", "content": content},
	}
}

func rtCompletedItem(itemID, transcript string, usage map[string]any) map[string]any {
	return rtCompletedItemWithEventID(itemID, transcript, usage, "evt_completed_raw_secret")
}

func rtCompletedItemWithEventID(itemID, transcript string, usage map[string]any, eventID string) map[string]any {
	return map[string]any{
		"type": "conversation.item.input_audio_transcription.completed", "event_id": eventID,
		"item_id": itemID, "content_index": 0, "transcript": transcript, "languages": []map[string]string{{"code": "en"}}, "usage": usage,
	}
}

func rtTokenUsage() map[string]any {
	return map[string]any{
		"type": "tokens", "input_tokens": 13, "output_tokens": 9, "total_tokens": 22,
		"input_token_details": map[string]any{"text_tokens": 0, "audio_tokens": 13},
	}
}

func rtDurationUsage(seconds float64) map[string]any {
	return map[string]any{"type": "duration", "seconds": seconds}
}

func rtReadExactClientEvent(t *testing.T, conn *websocket.Conn, expected any) {
	t.Helper()
	actual := rtReadClientMap(t, conn)
	actualJSON, actualErr := json.Marshal(actual)
	expectedJSON, expectedErr := json.Marshal(expected)
	if actualErr != nil || expectedErr != nil || !bytes.Equal(actualJSON, expectedJSON) {
		t.Errorf("client event mismatch\nactual: %s\nexpected: %s", actualJSON, expectedJSON)
	}
}

func rtReadClientMap(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	messageType, raw, err := conn.ReadMessage()
	if err != nil {
		t.Errorf("read client event: %v", err)
		return nil
	}
	if messageType != websocket.TextMessage {
		t.Errorf("client message type = %d", messageType)
		return nil
	}
	var event map[string]any
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Errorf("decode client event: %v", err)
		return nil
	}
	return event
}

func rtWriteJSON(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	if err := conn.WriteJSON(value); err != nil {
		t.Errorf("write fake server event: %v", err)
	}
}

func rtNewFixture(t *testing.T, wavOverride []byte) rtFixture {
	t.Helper()
	root := t.TempDir()
	manifest := []byte("bounded candidate manifest\n")
	manifestPath := filepath.Join(root, "candidate.manifest")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	wav := wavOverride
	if wav == nil {
		wav = rtWAV(1, 24000, 16, 100)
	}
	audioPath := filepath.Join(root, "fixture.wav")
	if err := os.WriteFile(audioPath, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	reference := "STRIDE reference phrase raw secret.\n"
	referencePath := filepath.Join(root, "reference.txt")
	if err := os.WriteFile(referencePath, []byte(reference), 0o600); err != nil {
		t.Fatal(err)
	}
	_, duration, _, err := loadApprovedWAV(audioPath, digestBytes(wav))
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	if duration != 100*time.Millisecond {
		t.Fatalf("fixture duration = %v", duration)
	}
	pcm, err := extractApprovedPCMForTest(wav)
	if err != nil {
		t.Fatal(err)
	}
	return rtFixture{
		config: RealtimeTranscribeConfig{
			Config: Config{
				CandidateDigest:         digestBytes(manifest),
				CandidateManifestPath:   manifestPath,
				ReceiptDir:              filepath.Join(root, "receipt"),
				Acknowledgement:         "bounded-test-acknowledgement",
				APIKey:                  rtAPIKey,
				Project:                 rtProject,
				ExpectedProjectSHA256:   digest(rtProject),
				Model:                   TranscribeModel,
				AudioPath:               audioPath,
				ExpectedFixtureSHA256:   digestBytes(wav),
				ReferencePath:           referencePath,
				ExpectedReferenceSHA256: digestBytes([]byte(reference)),
				MaxUSD:                  0.01,
			},
			SegmentID: rtSegment,
		},
		pcm:       pcm,
		reference: reference,
	}
}

func rtWAV(channels, sampleRate, bitsPerSample, durationMS int) []byte {
	blockAlign := channels * bitsPerSample / 8
	byteRate := sampleRate * blockAlign
	data := make([]byte, byteRate*durationMS/1000)
	for i := range data {
		data[i] = byte(i % 251)
	}
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36+len(data)))
	wav.WriteString("WAVEfmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&wav, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&wav, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(bitsPerSample))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(data)))
	wav.Write(data)
	return wav.Bytes()
}

func extractApprovedPCMForTest(wav []byte) ([]byte, error) {
	if len(wav) < 44 || string(wav[36:40]) != "data" {
		return nil, errors.New("test WAV did not have the canonical data chunk")
	}
	size := int(binary.LittleEndian.Uint32(wav[40:44]))
	if 44+size != len(wav) {
		return nil, errors.New("test WAV data size was invalid")
	}
	return append([]byte(nil), wav[44:]...), nil
}

func rtMustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
