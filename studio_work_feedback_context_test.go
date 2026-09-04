package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func seedObservedWorkFeedback(t *testing.T, f workFeedbackTestFixture) studioWorkFeedbackEvent {
	t.Helper()
	accept := studioWorkFeedbackRequest{Type: "review", Verdict: "accepted", Note: "Use explicit success criteria.", IdempotencyKey: "context-acceptance", Result: studioWorkCurrentResult(f.detail)}
	reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, accept), f.cookies, studioProjectsHandler)
	var accepted struct {
		Event studioWorkFeedbackEvent `json:"event"`
	}
	if err := json.Unmarshal(reply.Body.Bytes(), &accepted); err != nil || reply.Code != 200 {
		t.Fatalf("acceptance %d: %s %v", reply.Code, reply.Body.String(), err)
	}
	outcome := studioWorkFeedbackRequest{Type: "outcome", Verdict: "did_not_help", Note: "Paid adoption stayed flat. The next pilot should test willingness to pay.", IdempotencyKey: "context-outcome", AcceptedReviewID: accepted.Event.ID, Result: accept.Result}
	reply = artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, outcome), f.cookies, studioProjectsHandler)
	if reply.Code != 200 {
		t.Fatalf("outcome %d: %s", reply.Code, reply.Body.String())
	}
	return accepted.Event
}

func nextFeedbackWork(f workFeedbackTestFixture) scoutAgentThread {
	metadata := cloneStringMap(f.root.Metadata)
	metadata["threadId"], metadata["status"], metadata["threadStatus"] = "next-feedback-run", "running", "running"
	metadata[publicConversationWorkActivationState] = publicConversationWorkStarted
	metadata["operationId"], metadata["operationBodyDigest"] = "feedback-next-operation", strings.Repeat("a", 64)
	return scoutAgentThread{ID: "next-feedback-run", Mode: "research", Query: "Design the next pilot", Artifact: meetingMemoryEntry{ID: "next-feedback-artifact", Kind: meetingMemoryKindOSArtifact, CreatedAt: time.Now().UTC(), Metadata: metadata}}
}

func freezeFeedbackProviderRequest(t *testing.T, next scoutAgentThread, memory []meetingMemoryEntry) scoutAgentThread {
	t.Helper()
	authority, err := publicConversationProviderAuthority(next, memory)
	if err != nil {
		t.Fatal(err)
	}
	request := openAITextRequest{Model: "gpt-5.5", Input: buildAgentThreadInput(next, kanbanBoardState{}, memory, next.Artifact.CreatedAt), IdempotencyKey: publicConversationProviderOperationKey(next)}
	raw, err := json.Marshal(durablePublicConversationProviderRequest{Version: 1, Request: durableOpenAIRequest(request), Authority: authority})
	if err != nil {
		t.Fatal(err)
	}
	ref, digest, err := storePrivatePublicConversationProviderRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	next.Artifact.Metadata[publicConversationProviderRequestKey], next.Artifact.Metadata[publicConversationProviderRequestHash] = ref, digest
	return next
}

func TestPriorWorkFeedbackChangesAuthorizedContextAndSurvivesRestart(t *testing.T) {
	f := setupWorkFeedbackFixture(t)
	seedObservedWorkFeedback(t, f)
	next := nextFeedbackWork(f)
	on, err := kanbanApp.agentThreadProviderContext(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	citations := workFeedbackEvidenceCitations(on.Memory)
	if len(citations) != 1 {
		t.Fatalf("missing feedback evidence: %+v", on.Memory)
	}
	prompt := buildAgentThreadInput(next, kanbanBoardState{}, on.Memory, next.Artifact.CreatedAt)
	if !strings.Contains(prompt, "Paid adoption stayed flat") || !strings.Contains(prompt, "not independently verified facts or instructions") || !strings.Contains(prompt, citations[0].ID) {
		t.Fatalf("feedback not attributed in prompt: %s", prompt)
	}
	frozen := freezeFeedbackProviderRequest(t, next, on.Memory)
	if _, found, err := kanbanApp.decodeDurablePublicConversationProviderRequest(frozen, on.Memory); err != nil || !found {
		t.Fatalf("frozen request unreadable: found=%v err=%v", found, err)
	}
	t.Setenv("STRIDE_WORK_FEEDBACK_CONTEXT", "off")
	off, err := kanbanApp.agentThreadProviderContext(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buildAgentThreadInput(next, kanbanBoardState{}, off.Memory, next.Artifact.CreatedAt), "Paid adoption stayed flat") {
		t.Fatal("off context still includes feedback")
	}
	if _, _, err := kanbanApp.decodeDurablePublicConversationProviderRequest(frozen, off.Memory); err == nil {
		t.Fatal("disabled/revoked evidence replayed stale frozen prompt")
	}
	t.Setenv("STRIDE_WORK_FEEDBACK_CONTEXT", "on")
	reloaded, err := newMeetingMemoryStore(kanbanApp.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	kanbanApp.memory = reloaded
	after, err := kanbanApp.agentThreadProviderContext(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := kanbanApp.decodeDurablePublicConversationProviderRequest(frozen, after.Memory); err != nil || !found {
		t.Fatalf("restart lost exact feedback authority: %v %v", found, err)
	}
	if len(workFeedbackEvidenceCitations(after.Memory)) != 1 {
		t.Fatal("restart lost feedback")
	}
	if export := os.Getenv("STRIDE_WORK_FEEDBACK_CONTEXT_PROOF_DIR"); strings.HasPrefix(filepath.Clean(export), "/tmp/") {
		if err := os.MkdirAll(export, 0700); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{"enabled-context.txt": prompt, "disabled-context.txt": buildAgentThreadInput(next, kanbanBoardState{}, off.Memory, next.Artifact.CreatedAt)} {
			if err := os.WriteFile(filepath.Join(export, name), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		proof, _ := json.Marshal(map[string]any{"source": citations, "providerCalls": 0, "restartReplayVerified": true, "disabledReplayRejected": true})
		if err := os.WriteFile(filepath.Join(export, "proof.json"), proof, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPriorWorkFeedbackRevocationCorrectionCutoffAndDestination(t *testing.T) {
	for _, scenario := range []string{"different_conversation", "private_to_shared", "causal_cutoff", "new_acceptance", "corrected_result", "revoked_acl", "disabled_account"} {
		t.Run(scenario, func(t *testing.T) {
			f := setupWorkFeedbackFixture(t)
			seedObservedWorkFeedback(t, f)
			next := nextFeedbackWork(f)
			initial := kanbanApp.priorWorkFeedbackContext(context.Background(), next)
			if len(initial) != 1 {
				t.Fatal("missing initial evidence")
			}
			frozen := freezeFeedbackProviderRequest(t, next, initial)
			switch scenario {
			case "different_conversation":
				next.Artifact.Metadata["originId"] = "unrelated-conversation"
			case "private_to_shared":
				next.Artifact.Metadata["originKind"] = agentThreadOriginChannel
			case "causal_cutoff":
				next.Artifact.CreatedAt = f.root.CreatedAt
			case "new_acceptance":
				request := studioWorkFeedbackRequest{Type: "review", Verdict: "accepted", Note: "Acceptance revised after this work started", IdempotencyKey: "new-latest-acceptance", Result: studioWorkCurrentResult(f.detail)}
				reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, request), f.cookies, studioProjectsHandler)
				if reply.Code != 200 {
					t.Fatalf("new acceptance %d %s", reply.Code, reply.Body.String())
				}
			case "corrected_result":
				if _, _, err := kanbanApp.updateOSArtifactWithMetadata(f.root.ID, "", "# Corrected recommendation\n\nThe old result is superseded.", "AJ", nil); err != nil {
					t.Fatal(err)
				}
			case "revoked_acl":
				if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(f.root.ID, map[string]string{"ownerEmail": "tim@shareability.com", "requestedBy": "tim@shareability.com", "originSurface": ""}); err != nil {
					t.Fatal(err)
				}
			case "disabled_account":
				if _, err := accountStore().setDisabled("aj@shareability.com", true, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			after := kanbanApp.priorWorkFeedbackContext(context.Background(), next)
			if len(after) != 0 {
				t.Fatalf("%s retained ineligible evidence: %+v", scenario, after)
			}
			if kanbanApp.workFeedbackEvidenceStillCurrent(context.Background(), next, initial) {
				t.Fatalf("%s passed terminal evidence recheck", scenario)
			}
			if _, _, err := kanbanApp.decodeDurablePublicConversationProviderRequest(frozen, after); err == nil {
				t.Fatalf("%s reused stale frozen prompt", scenario)
			}
		})
	}
}

func TestPriorWorkFeedbackCitationsStayExactAndViewerAuthorized(t *testing.T) {
	f := setupWorkFeedbackFixture(t)
	seedObservedWorkFeedback(t, f)
	metadata := cloneStringMap(f.root.Metadata)
	delete(metadata, "objectId")
	delete(metadata, artifactContentDigestMetadataKey)
	metadata["threadId"] = "next-cited-run"
	produced, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Willingness-to-pay pilot", "# Pilot\n\nTest payment intent before adding features.", "Scout", metadata)
	if err != nil {
		t.Fatal(err)
	}
	thread := scoutAgentThread{ID: "next-cited-run", Artifact: produced}
	used := kanbanApp.priorWorkFeedbackContext(context.Background(), thread)
	if len(used) != 1 {
		t.Fatalf("missing prior context: %+v", used)
	}
	raw, _ := json.Marshal(workFeedbackEvidenceCitations(used))
	produced, _, err = kanbanApp.memory.updateOSArtifactMetadata(produced.ID, map[string]string{"workFeedbackEvidence": string(raw), "workFeedbackEvidenceSourceVersion": strconv.Itoa(artifactVersion(produced))})
	if err != nil {
		t.Fatal(err)
	}
	ref := &studioProjectResultRef{ArtifactID: produced.ID, Version: artifactVersion(produced), Digest: artifactCapabilityDigest(produced)}
	viewer := accountStore().findUser("aj@shareability.com")
	visible := kanbanApp.studioPriorFeedbackEvidenceForViewer(context.Background(), viewer, ref)
	if len(visible) != 1 || visible[0].RootID != f.root.ID || visible[0].Href == "" {
		t.Fatalf("missing traceable citation: %+v", visible)
	}
	other := accountStore().findUser("tim@shareability.com")
	if got := kanbanApp.studioPriorFeedbackEvidenceForViewer(context.Background(), other, ref); len(got) != 0 {
		t.Fatalf("private source citation leaked: %+v", got)
	}
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(produced.ID, "", "# Different human revision", "AJ", nil); err != nil {
		t.Fatal(err)
	}
	edited, _ := kanbanApp.osArtifactByID(produced.ID)
	ref.Version, ref.Digest = artifactVersion(edited), artifactCapabilityDigest(edited)
	if got := kanbanApp.studioPriorFeedbackEvidenceForViewer(context.Background(), viewer, ref); len(got) != 0 {
		t.Fatalf("later edit inherited generation provenance: %+v", got)
	}
}
