package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
)

func TestWorkFeedbackTerminalCASRejectsChangedSourcesWithoutPublishing(t *testing.T) {
	for _, scenario := range []string{"corrected_source", "superseding_review"} {
		t.Run(scenario, func(t *testing.T) {
			f := setupWorkFeedbackFixture(t)
			seedObservedWorkFeedback(t, f)
			app := kanbanApp
			metadata := cloneStringMap(f.root.Metadata)
			delete(metadata, "objectId")
			delete(metadata, artifactContentDigestMetadataKey)
			metadata["threadId"] = "feedback-terminal-cas-run"
			target, _, err := app.createOSArtifactWithMetadata("research", "Next pilot", "# Running\n\nOriginal scaffold must survive a rejected write.", "Scout", metadata)
			if err != nil {
				t.Fatal(err)
			}
			thread := scoutAgentThread{ID: "feedback-terminal-cas-run", Mode: "research", Query: "Design next pilot", Artifact: target}
			used := app.priorWorkFeedbackContext(context.Background(), thread)
			if len(used) != 1 {
				t.Fatalf("missing authorized prior feedback: %+v", used)
			}
			citations, _ := json.Marshal(workFeedbackEvidenceCitations(used))
			proposed := map[string]string{"status": "complete", "threadStatus": "complete", "reviewGate": "passed", "workFeedbackEvidence": string(citations), "workFeedbackEvidenceDigests": workFeedbackUsedDigests(used), "workFeedbackEvidenceSourceVersion": strconv.Itoa(artifactVersion(target) + 1)}
			fence, err := app.prepareWorkFeedbackTerminalFence(context.Background(), thread, proposed)
			if err != nil || fence == nil {
				t.Fatalf("prepare fence: %v", err)
			}
			expected, found := app.memory.artifactAuthorizationHeaderByID(target.ID)
			if !found {
				t.Fatal("target header missing")
			}
			if scenario == "corrected_source" {
				if _, _, err := app.updateOSArtifactWithMetadata(f.root.ID, "", "# Corrected source\n\nThe prior conclusion no longer holds.", "AJ", nil); err != nil {
					t.Fatal(err)
				}
			} else {
				review := studioWorkFeedbackRequest{Type: "review", Verdict: "revision_requested", Note: "The conclusion needs new paid-adoption evidence.", IdempotencyKey: "terminal-cas-superseding-review", Result: studioWorkCurrentResult(f.detail)}
				reply := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", workFeedbackBody(f, review), f.cookies, studioProjectsHandler)
				if reply.Code != http.StatusOK {
					t.Fatalf("new review %d: %s", reply.Code, reply.Body.String())
				}
			}
			_, changed, err := app.memory.updateOSArtifactWithMetadataIfHeaderMetadataAndFeedbackMatch(expected, nil, target.ID, "", "# Stale provider result must not publish", "Scout", proposed, fence)
			if changed || !errors.Is(err, errPriorWorkFeedbackChanged) {
				t.Fatalf("stale publication not rejected atomically: changed=%v err=%v", changed, err)
			}
			after, found := app.osArtifactByID(target.ID)
			if !found || after.Text != target.Text || artifactVersion(after) != artifactVersion(target) || artifactCapabilityDigest(after) != artifactCapabilityDigest(target) {
				t.Fatal("failed CAS changed the durable result")
			}
			if after.Metadata["workFeedbackEvidence"] != "" || after.Metadata["workFeedbackEvidenceDigests"] != "" {
				t.Fatal("failed CAS persisted a stale success citation")
			}
			for _, key := range []string{"status", "threadStatus", "reviewGate", "workFeedbackEvidenceSourceVersion"} {
				if after.Metadata[key] != target.Metadata[key] {
					t.Fatalf("failed CAS changed success metadata %s: %q -> %q", key, target.Metadata[key], after.Metadata[key])
				}
			}
		})
	}
}
