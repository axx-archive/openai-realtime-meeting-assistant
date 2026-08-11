package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const conversationFollowUpReceiptMetadataKey = "conversationFollowUpReceipts"

// conversationFollowUpBeforeCardCommitProbe is test-only crash injection at
// the exact boundary where the revision is durable/running but its chat status
// line has not committed. Production leaves it nil.
var conversationFollowUpBeforeCardCommitProbe func(scoutAgentThread) error

type conversationFollowUpBinding struct {
	OperationID         string `json:"operationId"`
	OperationBodyDigest string `json:"operationBodyDigest"`
	SourceMessageID     string `json:"sourceMessageId"`
	OriginID            string `json:"originId"`
	RequesterEmail      string `json:"requesterEmail"`
	TargetArtifactID    string `json:"targetArtifactId"`
}

func newConversationFollowUpBinding(operation conversationTurnOperation, sourceMessageID, originID, requesterEmail, targetArtifactID string) (conversationFollowUpBinding, error) {
	binding := conversationFollowUpBinding{
		OperationID: strings.TrimSpace(operation.ID), OperationBodyDigest: strings.TrimSpace(operation.BodyDigest),
		SourceMessageID: strings.TrimSpace(sourceMessageID), OriginID: strings.TrimSpace(originID),
		RequesterEmail: normalizeAccountEmail(requesterEmail), TargetArtifactID: strings.TrimSpace(targetArtifactID),
	}
	if binding.OperationID == "" || !isHexDigest(binding.OperationBodyDigest) || binding.SourceMessageID == "" || binding.OriginID == "" || binding.RequesterEmail == "" || binding.TargetArtifactID == "" {
		return conversationFollowUpBinding{}, fmt.Errorf("conversation follow-up operation binding is invalid")
	}
	return binding, nil
}

func conversationFollowUpBindingEqual(left, right conversationFollowUpBinding) bool {
	return left.OperationID == right.OperationID && left.OperationBodyDigest == right.OperationBodyDigest &&
		left.SourceMessageID == right.SourceMessageID && left.OriginID == right.OriginID &&
		normalizeAccountEmail(left.RequesterEmail) == normalizeAccountEmail(right.RequesterEmail) &&
		left.TargetArtifactID == right.TargetArtifactID
}

func conversationFollowUpReceipts(metadata map[string]string) ([]conversationFollowUpBinding, error) {
	raw := strings.TrimSpace(metadata[conversationFollowUpReceiptMetadataKey])
	if raw == "" {
		return nil, nil
	}
	var receipts []conversationFollowUpBinding
	if err := json.Unmarshal([]byte(raw), &receipts); err != nil {
		return nil, fmt.Errorf("conversation follow-up receipt ledger is invalid")
	}
	for _, receipt := range receipts {
		if _, err := newConversationFollowUpBinding(conversationTurnOperation{ID: receipt.OperationID, BodyDigest: receipt.OperationBodyDigest}, receipt.SourceMessageID, receipt.OriginID, receipt.RequesterEmail, receipt.TargetArtifactID); err != nil {
			return nil, fmt.Errorf("conversation follow-up receipt ledger is invalid")
		}
	}
	return receipts, nil
}

func appendConversationFollowUpReceipt(metadata map[string]string, binding conversationFollowUpBinding) (string, error) {
	receipts, err := conversationFollowUpReceipts(metadata)
	if err != nil {
		return "", err
	}
	for _, existing := range receipts {
		if existing.OperationID != binding.OperationID {
			continue
		}
		if !conversationFollowUpBindingEqual(existing, binding) {
			return "", fmt.Errorf("%w: conversation follow-up operation id was reused with different content", ErrSTRIDEConversationConflict)
		}
		raw, marshalErr := canonicalJSON(receipts)
		return string(raw), marshalErr
	}
	receipts = append(receipts, binding)
	raw, err := canonicalJSON(receipts)
	return string(raw), err
}

// conversationFollowUpForOperation scans the durable receipt ledger rather
// than trusting the requested target or an in-memory run. It is called before
// any new follow-up effect; an exact match reconstructs the one current work
// record, while any operation-id collision fails closed.
func (app *kanbanBoardApp) conversationFollowUpForOperation(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, binding conversationFollowUpBinding) (scoutAgentThread, bool, error) {
	if app == nil || app.memory == nil || user == nil {
		return scoutAgentThread{}, false, nil
	}
	if binding.OriginID != strings.TrimSpace(thread.ID) || binding.RequesterEmail != normalizeAccountEmail(user.Email) {
		return scoutAgentThread{}, false, fmt.Errorf("%w: conversation follow-up authority changed", ErrSTRIDEConversationConflict)
	}
	var match scoutAgentThread
	matches := 0
	for _, entry := range app.memory.snapshot(0) {
		receipts, err := conversationFollowUpReceipts(entry.Metadata)
		if err != nil {
			return scoutAgentThread{}, false, err
		}
		for _, receipt := range receipts {
			if receipt.OperationID != binding.OperationID {
				continue
			}
			if !conversationFollowUpBindingEqual(receipt, binding) {
				return scoutAgentThread{}, false, fmt.Errorf("%w: conversation follow-up operation id was reused with different content", ErrSTRIDEConversationConflict)
			}
			header := artifactAuthorizationHeaderFromEntry(entry)
			if !artifactHeaderAuthorized(ctx, user, ACLReadContent, header) || !artifactHeaderAuthorized(ctx, user, ACLExecute, header) {
				return scoutAgentThread{}, false, fmt.Errorf("conversation follow-up work is unavailable")
			}
			mode := firstNonEmptyString(strings.TrimSpace(entry.Metadata["mode"]), entry.Kind)
			query := firstNonEmptyString(strings.TrimSpace(entry.Metadata["threadQuery"]), strings.TrimSpace(entry.Metadata["objective"]), strings.TrimSpace(entry.Metadata["title"]))
			match = scoutAgentThread{
				ID: firstNonEmptyString(strings.TrimSpace(entry.Metadata["threadId"]), entry.ID), Mode: mode, Query: query,
				Status: firstNonEmptyString(agentThreadStatusValue(entry), "running"), Artifact: entry,
			}
			match.Actions = app.osAssistantActions(query, mode, entry)
			matches++
		}
	}
	if matches > 1 {
		return scoutAgentThread{}, false, fmt.Errorf("%w: conversation follow-up operation owns multiple work records", ErrSTRIDEConversationConflict)
	}
	return match, matches == 1, nil
}

func conversationFollowUpStatusMessage(userMessage scoutChatMessageRecord, work scoutAgentThread) scoutChatMessageRecord {
	label := scoutChatWorkLabel(work.Artifact.Metadata)
	status := firstNonEmptyString(agentThreadStatusValue(work.Artifact), work.Status, "running")
	text := label + " revision accepted — progress and the finished result will update the existing work card"
	if status == "complete" || status == "verified" || status == "published" {
		text = label + " revision is complete — the existing work card has the latest result"
	} else if status == "error" || status == "failed" || status == "needs_attention" {
		text = label + " revision needs attention — the existing work card has the details"
	}
	createdAt := work.Artifact.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return scoutChatMessageRecord{
		ID:   "scout-chat-message-followup-" + sha256Hex([]byte(userMessage.ID + "\x00" + work.Artifact.ID))[:24],
		Kind: "message", Role: "scout", IntentOutcome: string(conversationIntentStartPrivateWork),
		CausedByMessageID: userMessage.ID, Text: text, CreatedAt: createdAt.Format(time.RFC3339Nano),
	}
}
