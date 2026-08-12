package main

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrProjectProjectionInvalid  = errors.New("invalid Project Record projection input")
	ErrProjectProjectionConflict = errors.New("Project Record projection revision conflict")
)

// ProjectRecordNode is deliberately body-free. It is a rebuildable index edge,
// not a message, artifact, contribution claim, professional Work Record, or
// grant of access to its source.
type ProjectRecordNode struct {
	Association       STRIDEReference   `json:"association"`
	Project           STRIDEReference   `json:"project"`
	Subject           STRIDEReference   `json:"subject"`
	SourceRefs        []STRIDEReference `json:"sourceRefs"`
	SourceACLRevision int64             `json:"sourceAclRevision"`
	SourceACLDigest   string            `json:"sourceAclDigest"`
	ConsentRevision   int64             `json:"consentRevision"`
	PurgeGeneration   uint64            `json:"purgeGeneration"`
	RecordedAt        time.Time         `json:"recordedAt"`
}

type ProjectRecordProjection struct {
	Project   STRIDEReference     `json:"project"`
	Title     string              `json:"title"`
	Lifecycle string              `json:"lifecycle"`
	Nodes     []ProjectRecordNode `json:"nodes"`
}

type projectProjectionCursor struct {
	revision int64
	digest   string
}

type ProjectProjectionService struct {
	mu        sync.RWMutex
	authority *ProjectAuthorityService
	nodes     map[string]map[string]ProjectRecordNode
	cursors   map[string]projectProjectionCursor
}

func NewProjectProjectionService(authority *ProjectAuthorityService) *ProjectProjectionService {
	return &ProjectProjectionService{authority: authority, nodes: map[string]map[string]ProjectRecordNode{}, cursors: map[string]projectProjectionCursor{}}
}

// RebuildProjectRecord reconstructs one Project from authorized current truth
// after restart. It emits no node until each association has independently
// passed the current source resolver; current terminal revisions become cursors
// but never visible nodes.
func (s *ProjectProjectionService) RebuildProjectRecord(authority ProjectAuthorityContext, projectID string) error {
	if s == nil || s.authority == nil || !strideIdentifier(projectID) {
		return ErrProjectProjectionInvalid
	}
	return s.authority.withProjectAssociationSnapshot(authority, projectID, func(_ Project, associations []ProjectAssociation) error {
		nodes := map[string]ProjectRecordNode{}
		cursors := map[string]projectProjectionCursor{}
		for _, association := range associations {
			cursors[association.Header.ID] = projectProjectionCursor{revision: association.Header.Revision, digest: association.Header.ContentDigest}
			if association.State == "confirmed" {
				nodes[association.Header.ID] = ProjectRecordNode{
					Association: projectAssociationRef(association), Project: association.Project, Subject: association.Subject,
					SourceRefs: append([]STRIDEReference(nil), association.SourceRefs...), SourceACLRevision: association.SourceACLRevision,
					SourceACLDigest: association.SourceACLDigest, ConsentRevision: association.ConsentRevision,
					PurgeGeneration: association.PurgeGeneration, RecordedAt: association.RecordedAt,
				}
			}
		}
		s.mu.Lock()
		delete(s.nodes, projectID)
		if len(nodes) > 0 {
			s.nodes[projectID] = nodes
		}
		for id, cursor := range cursors {
			s.cursors[id] = cursor
		}
		s.mu.Unlock()
		return nil
	})
}

// ApplyCurrentAssociation consumes only the authoritative current revision. A
// duplicate is idempotent; skipped/out-of-order revisions fail closed so the
// durable outbox can replay from the missing predecessor.
func (s *ProjectProjectionService) ApplyCurrentAssociation(authority ProjectAuthorityContext, associationID string) error {
	if s == nil || s.authority == nil || !strideIdentifier(associationID) {
		return ErrProjectProjectionInvalid
	}
	association, err := s.authority.CurrentAssociation(authority, associationID)
	if err != nil {
		return err
	}
	ref := projectAssociationRef(association)
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.cursors[associationID]
	if previous.revision == association.Header.Revision && previous.digest == association.Header.ContentDigest {
		return nil
	}
	if previous.revision > 0 && (association.Header.Revision != previous.revision+1 || association.Supersedes == nil || association.Supersedes.Digest != previous.digest) {
		return ErrProjectProjectionConflict
	}
	if previous.revision == 0 && association.Header.Revision != 1 {
		return ErrProjectProjectionConflict
	}
	for projectID, nodes := range s.nodes {
		delete(nodes, associationID)
		if len(nodes) == 0 {
			delete(s.nodes, projectID)
		}
	}
	if association.State == "confirmed" {
		if s.nodes[association.Project.ID] == nil {
			s.nodes[association.Project.ID] = map[string]ProjectRecordNode{}
		}
		s.nodes[association.Project.ID][associationID] = ProjectRecordNode{
			Association: ref, Project: association.Project, Subject: association.Subject,
			SourceRefs: append([]STRIDEReference(nil), association.SourceRefs...), SourceACLRevision: association.SourceACLRevision,
			SourceACLDigest: association.SourceACLDigest, ConsentRevision: association.ConsentRevision,
			PurgeGeneration: association.PurgeGeneration, RecordedAt: association.RecordedAt,
		}
	}
	s.cursors[associationID] = projectProjectionCursor{revision: association.Header.Revision, digest: association.Header.ContentDigest}
	return nil
}

func (s *ProjectProjectionService) ReadProjectRecord(authority ProjectAuthorityContext, projectID string) (ProjectRecordProjection, error) {
	if s == nil || s.authority == nil || !strideIdentifier(projectID) {
		return ProjectRecordProjection{}, ErrProjectProjectionInvalid
	}
	var result ProjectRecordProjection
	err := s.authority.withProjectAssociationSnapshot(authority, projectID, func(project Project, associations []ProjectAssociation) error {
		current := make(map[string]ProjectAssociation, len(associations))
		for _, association := range associations {
			current[association.Header.ID] = association
		}
		s.mu.RLock()
		visible := make([]ProjectRecordNode, 0, len(s.nodes[projectID]))
		for _, node := range s.nodes[projectID] {
			association, exists := current[node.Association.ID]
			if !exists || association.State != "confirmed" || association.Project.ID != projectID ||
				association.Header.Revision != node.Association.Revision || association.Header.ContentDigest != node.Association.Digest {
				continue
			}
			copy := node
			copy.SourceRefs = append([]STRIDEReference(nil), node.SourceRefs...)
			visible = append(visible, copy)
		}
		s.mu.RUnlock()
		sort.Slice(visible, func(i, j int) bool {
			if visible[i].RecordedAt.Equal(visible[j].RecordedAt) {
				return visible[i].Association.ID < visible[j].Association.ID
			}
			return visible[i].RecordedAt.Before(visible[j].RecordedAt)
		})
		result = ProjectRecordProjection{
			Project: STRIDEReference{ContractType: STRIDEContractProject, ID: project.ProjectID, Revision: project.Header.Revision, Digest: project.Header.ContentDigest},
			Title:   project.Title, Lifecycle: project.Lifecycle, Nodes: visible,
		}
		return nil
	})
	return result, err
}
