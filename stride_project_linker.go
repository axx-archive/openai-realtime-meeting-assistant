package main

import (
	"sort"
	"strings"
	"unicode"
)

const ProjectLinkerRevisionV1 = "project_linker_v1"

type ProjectLinkRequest struct {
	Text                 string
	AuthoritativeProject *STRIDEReference
}

type ProjectLinkCandidate struct {
	ProjectID       string  `json:"projectId"`
	ProjectRevision int64   `json:"projectRevision"`
	Title           string  `json:"title"`
	Basis           string  `json:"basis"`
	Confidence      float64 `json:"confidence"`
}

type ProjectLinkDecision struct {
	Status             string                 `json:"status"`
	ClassifierRevision string                 `json:"classifierRevision"`
	Candidate          *ProjectLinkCandidate  `json:"candidate,omitempty"`
	Clarify            []ProjectLinkCandidate `json:"clarify,omitempty"`
}

// ResolveProjectLink is deterministic and provider-free. Its input Projects
// already passed the current organization/session authority fence; it never
// queries a foreign tenant or title index before authorization.
func (s *ProjectAuthorityService) ResolveProjectLink(authority ProjectAuthorityContext, request ProjectLinkRequest) (ProjectLinkDecision, error) {
	projects, err := s.VisibleProjects(authority)
	if err != nil {
		return ProjectLinkDecision{}, err
	}
	decision := ProjectLinkDecision{Status: "unlinked", ClassifierRevision: ProjectLinkerRevisionV1}
	if request.AuthoritativeProject != nil {
		if request.AuthoritativeProject.Validate() != nil || request.AuthoritativeProject.ContractType != STRIDEContractProject {
			return ProjectLinkDecision{}, ErrProjectAuthorityInvalid
		}
		for _, project := range projects {
			if project.ProjectID == request.AuthoritativeProject.ID && project.Header.Revision == request.AuthoritativeProject.Revision &&
				project.Header.ContentDigest == request.AuthoritativeProject.Digest {
				decision.Status = "proposed"
				decision.Candidate = projectLinkCandidate(project, "authoritative_context", 1)
				return decision, nil
			}
		}
		// Stale, archived, revoked or foreign ancestry never falls back by title.
		return decision, nil
	}
	normalized := normalizeProjectLinkText(request.Text)
	if normalized == "" {
		return decision, nil
	}
	type scored struct {
		project Project
		score   float64
	}
	matches := make([]scored, 0)
	for _, project := range projects {
		score := projectLinkScore(normalized, project)
		if score >= .8 {
			matches = append(matches, scored{project: project, score: score})
		}
	}
	if len(matches) == 0 {
		return decision, nil
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].project.ProjectID < matches[j].project.ProjectID
		}
		return matches[i].score > matches[j].score
	})
	if len(matches) == 1 {
		decision.Status = "proposed"
		decision.Candidate = projectLinkCandidate(matches[0].project, "suggested", matches[0].score)
		return decision, nil
	}
	decision.Status = "clarify"
	decision.Clarify = make([]ProjectLinkCandidate, 0, len(matches))
	for _, match := range matches {
		decision.Clarify = append(decision.Clarify, *projectLinkCandidate(match.project, "suggested", match.score))
	}
	return decision, nil
}

func projectLinkCandidate(project Project, basis string, confidence float64) *ProjectLinkCandidate {
	return &ProjectLinkCandidate{ProjectID: project.ProjectID, ProjectRevision: project.Header.Revision, Title: project.Title, Basis: basis, Confidence: confidence}
}

func projectLinkScore(text string, project Project) float64 {
	values := append([]string{project.Title}, project.Aliases...)
	best := float64(0)
	for _, value := range values {
		candidate := normalizeProjectLinkText(value)
		if candidate == "" {
			continue
		}
		score := float64(0)
		if text == candidate {
			score = .96
		} else if strings.Contains(" "+text+" ", " "+candidate+" ") {
			score = .9
		}
		if score > best {
			best = score
		}
	}
	return best
}

func normalizeProjectLinkText(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}
