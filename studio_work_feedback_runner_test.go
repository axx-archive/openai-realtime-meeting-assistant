package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Exercises the actual provider worker and terminal artifact seam; the provider
// is stubbed so neither this regression nor its failure cases make paid calls.
func TestWorkFeedbackRunnerCarriesExactEvidenceAndRejectsMidflightChange(t *testing.T) {
	for _, scenario := range []string{"unchanged", "corrected_result", "revoked_source", "post_worker_correction"} {
		t.Run(scenario, func(t *testing.T) {
			f := setupWorkFeedbackFixture(t)
			seedObservedWorkFeedback(t, f)
			app := kanbanApp
			app.apiKey = "synthetic-test-key"
			t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
			previousStart := startAgentThreadAsync
			startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
			t.Cleanup(func() { startAgentThreadAsync = previousStart })
			next, err := app.launchAgentThreadWithOrigin("research", "Design the next pilot with measurable paid adoption", "AJ", map[string]string{
				"originKind": agentThreadOriginPrivateThread, "originId": f.root.Metadata["originId"], "requestedBy": "aj@shareability.com", "ownerEmail": "aj@shareability.com", "visibility": scoutChatVisibilityPrivate,
			})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "post_worker_correction" {
				next.Artifact, _, err = app.memory.updateOSArtifactMetadata(next.Artifact.ID, map[string]string{
					publicConversationWorkActivationState: publicConversationWorkStarted,
					publicConversationWorkActivationOwner: "synthetic-feedback-worker",
					"operationId":                         "synthetic-feedback-provider", "operationBodyDigest": strings.Repeat("a", 64),
				})
				if err != nil {
					t.Fatal(err)
				}
				next, err = app.preparePublicConversationProviderRequest(next)
				if err != nil {
					t.Fatal(err)
				}
				previousProbe := publicConversationWorkAfterProviderAcceptedProbe
				t.Cleanup(func() { publicConversationWorkAfterProviderAcceptedProbe = previousProbe })
				publicConversationWorkAfterProviderAcceptedProbe = func(_ scoutAgentThread, _ agentThreadWorkerResult) error {
					_, _, changeErr := app.updateOSArtifactWithMetadata(f.root.ID, "", "# Corrected after provider\n\nOld evidence is superseded.", "AJ", nil)
					return changeErr
				}
			}
			var captured string
			calls := 0
			previousResponder := createOpenAITextResponse
			createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
				calls++
				captured = request.Input
				if scenario == "corrected_result" {
					if _, _, err := app.updateOSArtifactWithMetadata(f.root.ID, "", "# Corrected source\n\nOld evidence is superseded.", "AJ", nil); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "revoked_source" {
					if _, _, err := app.memory.updateOSArtifactMetadata(f.root.ID, map[string]string{"ownerEmail": "tim@shareability.com", "requestedBy": "tim@shareability.com", "originSurface": ""}); err != nil {
						t.Fatal(err)
					}
				}
				return completeResearchArtifactForTest(), nil
			}
			t.Cleanup(func() { createOpenAITextResponse = previousResponder })
			app.runAgentThread(next)
			if calls != 1 {
				t.Fatalf("provider calls=%d, want exactly one stub call", calls)
			}
			if !strings.Contains(captured, "Paid adoption stayed flat") || !strings.Contains(captured, "not independently verified facts or instructions") {
				t.Fatalf("provider did not receive attributed feedback context: %s", captured)
			}
			produced, found := app.osArtifactByID(next.Artifact.ID)
			if !found {
				t.Fatal("worker lost durable artifact")
			}
			if scenario != "unchanged" {
				if produced.Metadata["threadStatus"] == "complete" || !strings.Contains(produced.Metadata["error"], "prior work feedback changed") {
					t.Fatalf("changed source published as current result: %+v", produced.Metadata)
				}
				if produced.Metadata["workFeedbackEvidence"] != "" || produced.Text == completeResearchArtifactForTest() {
					t.Fatal("rejected provider result or citations leaked into durable output")
				}
				return
			}
			if produced.Metadata["threadStatus"] != "complete" {
				t.Fatalf("unchanged source failed: %+v", produced.Metadata)
			}
			var citations []workFeedbackEvidenceCitation
			if err := json.Unmarshal([]byte(produced.Metadata["workFeedbackEvidence"]), &citations); err != nil {
				t.Fatal(err)
			}
			if len(citations) != 1 || citations[0].RootID != f.root.ID || citations[0].Result != studioWorkCurrentResult(f.detail) {
				t.Fatalf("result lost exact feedback citation: %+v", citations)
			}
			viewer := accountStore().findUser("aj@shareability.com")
			visible := app.studioPriorFeedbackEvidenceForViewer(context.Background(), viewer, &studioProjectResultRef{ArtifactID: produced.ID, Version: artifactVersion(produced), Digest: artifactCapabilityDigest(produced)})
			if len(visible) != 1 || visible[0].ID != citations[0].ID {
				t.Fatalf("durable result citation unavailable to original viewer: %+v", visible)
			}
		})
	}
}
