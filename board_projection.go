package main

import (
	"context"
	"sort"
	"strings"
	"time"
)

const (
	boardDeliveryRequested = "requested"
	boardDeliveryDelivered = "delivered"
	boardDeliveryDrive     = "drive"
)

type boardCardViewerProjection struct {
	CardID            string `json:"cardId"`
	DeliveryStage     string `json:"deliveryStage"`
	ProjectID         string `json:"projectId"`
	ProjectTitle      string `json:"projectTitle"`
	ProjectResolution string `json:"projectResolution"`
	ArtifactID        string `json:"artifactId,omitempty"`
}

type boardProjectViewerOption struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type boardViewerProjection struct {
	Cards    []boardCardViewerProjection `json:"cards"`
	Projects []boardProjectViewerOption  `json:"projects"`
}

// boardProjectionForViewer is a read-only, ACL-bound view over the canonical
// Board. It never changes card status or schema: it joins only artifacts and
// public channels the signed-in viewer is already authorized to read.
func (app *kanbanBoardApp) boardProjectionForViewer(ctx context.Context, user *userAccount) boardViewerProjection {
	projection := boardViewerProjection{}
	if app == nil || user == nil {
		return projection
	}
	artifacts := app.boardAuthorizedArtifactMetadata(ctx, user, 500)
	latestArtifactByCard := map[string]meetingMemoryEntry{}
	for _, entry := range artifacts {
		if entry.Kind != meetingMemoryKindOSArtifact {
			continue
		}
		cardID := strings.TrimSpace(entry.Metadata["boardCardId"])
		if cardID == "" {
			continue
		}
		current, found := latestArtifactByCard[cardID]
		if !found || entry.CreatedAt.After(current.CreatedAt) {
			latestArtifactByCard[cardID] = entry
		}
	}

	channels := map[string]scoutChatThreadRecord{}
	channelByTitle := map[string]scoutChatThreadRecord{}
	projects := map[string]string{"needs-project": "Needs project"}
	for _, thread := range app.scoutChatThreadsSnapshot(user.Email, false, 500) {
		if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
			continue
		}
		channels[thread.ID] = thread
		if title := strings.ToLower(strings.TrimSpace(thread.Title)); title != "" {
			channelByTitle[title] = thread
		}
		title := strings.TrimSpace(thread.Title)
		// Same fence as strideProductProjectDestinationEligible: the pinned org
		// channels (Bonfire Chat, #meetings) are never board projects.
		if !scoutChatThreadIsPinnedSystem(thread) && !strings.EqualFold(title, "team") && !strings.EqualFold(title, "general") {
			projects[thread.ID] = title
		}
	}
	for _, card := range app.snapshotState().Cards {
		artifact, linked := latestArtifactByCard[card.ID]
		stage := boardDeliveryRequested
		if linked && strings.EqualFold(strings.TrimSpace(artifact.Metadata["savedToFiles"]), "true") {
			stage = boardDeliveryDrive
		} else if card.Status == kanbanStatusDone || (linked && boardArtifactDelivered(artifact)) {
			stage = boardDeliveryDelivered
		}

		projectID, projectTitle, resolution := "needs-project", "Needs project", "missing"
		if linked {
			originID := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["originId"]), strings.TrimSpace(artifact.Metadata["originThreadId"]))
			if thread, found := channels[originID]; found {
				projectID, projectTitle, resolution = thread.ID, thread.Title, "linked"
			}
		}
		if resolution == "missing" {
			for _, tag := range card.Tags {
				if thread, found := channelByTitle[strings.ToLower(strings.TrimSpace(tag))]; found {
					projectID, projectTitle, resolution = thread.ID, thread.Title, "tag"
					break
				}
			}
		}
		projects[projectID] = projectTitle
		row := boardCardViewerProjection{
			CardID: card.ID, DeliveryStage: stage,
			ProjectID: projectID, ProjectTitle: projectTitle, ProjectResolution: resolution,
		}
		if linked {
			row.ArtifactID = artifact.ID
		}
		projection.Cards = append(projection.Cards, row)
	}

	for id, title := range projects {
		projection.Projects = append(projection.Projects, boardProjectViewerOption{ID: id, Title: title})
	}
	sort.Slice(projection.Projects, func(i, j int) bool {
		if projection.Projects[i].ID == "needs-project" {
			return false
		}
		if projection.Projects[j].ID == "needs-project" {
			return true
		}
		return strings.ToLower(projection.Projects[i].Title) < strings.ToLower(projection.Projects[j].Title)
	})
	return projection
}

// boardAuthorizedArtifactMetadata returns body-free artifact projections. It
// authorizes headers first, then rechecks the exact header under the store lock
// before copying metadata, matching the artifact object read contract without
// ever loading a deliverable body into the Board response.
func (app *kanbanBoardApp) boardAuthorizedArtifactMetadata(ctx context.Context, user *userAccount, limit int) []meetingMemoryEntry {
	if app == nil || app.memory == nil || user == nil {
		return nil
	}
	type candidate struct {
		id        string
		createdAt time.Time
		header    ArtifactAuthorizationHeader
	}
	app.memory.mu.Lock()
	candidates := make([]candidate, 0)
	for _, entry := range app.memory.entries {
		if entry.Kind != meetingMemoryKindOSArtifact || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		header := app.memory.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata}))
		candidates = append(candidates, candidate{id: entry.ID, createdAt: entry.CreatedAt, header: header})
	}
	app.memory.mu.Unlock()
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[len(candidates)-limit:]
	}
	result := make([]meetingMemoryEntry, 0, len(candidates))
	for _, item := range candidates {
		if !artifactHeaderAuthorized(ctx, user, ACLReadContent, item.header) {
			continue
		}
		app.memory.mu.Lock()
		for _, stored := range app.memory.entries {
			if stored.Kind != meetingMemoryKindOSArtifact || stored.ID != item.id || memoryEntryHiddenFromRecall(stored) {
				continue
			}
			current := app.memory.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: stored.ID, Kind: stored.Kind, Metadata: stored.Metadata}))
			if artifactAuthorizationHeaderEqual(item.header, current) {
				metadata := make(map[string]string, len(stored.Metadata))
				for key, value := range stored.Metadata {
					metadata[key] = value
				}
				result = append(result, meetingMemoryEntry{ID: stored.ID, Kind: stored.Kind, CreatedAt: stored.CreatedAt, Metadata: metadata})
			}
			break
		}
		app.memory.mu.Unlock()
	}
	return result
}

func boardArtifactDelivered(entry meetingMemoryEntry) bool {
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyString(entry.Metadata["threadStatus"], entry.Metadata["status"])))
	switch status {
	case artifactStatusComplete, artifactStatusPublished, artifactStatusApproved:
		return true
	default:
		return false
	}
}
