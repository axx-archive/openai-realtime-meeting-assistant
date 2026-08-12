package main

import (
	"strings"
	"testing"
	"time"
)

func TestProjectLinkerAuthoritativeContextAndStaleFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	service := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	project, binding := projectAuthorityProject(authority, "project_stride", "STRIDE", "thread_stride", now.Add(2*time.Minute))
	if err := service.CreateProject(authority, project, binding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	ref := STRIDEReference{ContractType: STRIDEContractProject, ID: project.ProjectID, Revision: project.Header.Revision, Digest: project.Header.ContentDigest}
	decision, err := service.ResolveProjectLink(authority, ProjectLinkRequest{Text: "ignore a misleading other title", AuthoritativeProject: &ref})
	if err != nil || decision.Status != "proposed" || decision.Candidate == nil || decision.Candidate.ProjectID != project.ProjectID || decision.Candidate.Basis != "authoritative_context" {
		t.Fatalf("exact ancestry did not win: %#v %v", decision, err)
	}
	ref.Revision++
	decision, err = service.ResolveProjectLink(authority, ProjectLinkRequest{Text: "STRIDE", AuthoritativeProject: &ref})
	if err != nil || decision.Status != "unlinked" || decision.Candidate != nil {
		t.Fatalf("stale ancestry fell back by title: %#v %v", decision, err)
	}
}

func TestProjectLinkerOneMatchClarifyAndNoMatch(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	service := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	stride, strideBinding := projectAuthorityProject(authority, "project_stride", "STRIDE", "thread_stride", now.Add(2*time.Minute))
	stride.Aliases = []string{"Network where work happens"}
	other, otherBinding := projectAuthorityProject(authority, "project_other", "Launch", "thread_launch", now.Add(3*time.Minute))
	if err := service.CreateProject(authority, stride, strideBinding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateProject(authority, other, otherBinding, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	decision, err := service.ResolveProjectLink(authority, ProjectLinkRequest{Text: "Update the STRIDE launch brief"})
	if err != nil || decision.Status != "clarify" || len(decision.Clarify) != 2 {
		t.Fatalf("multi-project text did not clarify once: %#v %v", decision, err)
	}
	decision, err = service.ResolveProjectLink(authority, ProjectLinkRequest{Text: "Continue the network where work happens roadmap"})
	if err != nil || decision.Status != "proposed" || decision.Candidate == nil || decision.Candidate.ProjectID != stride.ProjectID {
		t.Fatalf("one authorized alias did not propose: %#v %v", decision, err)
	}
	decision, err = service.ResolveProjectLink(authority, ProjectLinkRequest{Text: "Prepare an unrelated memo"})
	if err != nil || decision.Status != "unlinked" || decision.Candidate != nil || len(decision.Clarify) != 0 {
		t.Fatalf("no-match request invented context: %#v %v", decision, err)
	}
}

func TestProjectLinkerSameNameClarifiesAndArchivedNeverSuggests(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	service := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	first, firstBinding := projectAuthorityProject(authority, "project_one", "Launch", "thread_one", now.Add(2*time.Minute))
	second, secondBinding := projectAuthorityProject(authority, "project_two", "Launch", "thread_two", now.Add(3*time.Minute))
	if err := service.CreateProject(authority, first, firstBinding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateProject(authority, second, secondBinding, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	decision, _ := service.ResolveProjectLink(authority, ProjectLinkRequest{Text: "Launch"})
	if decision.Status != "clarify" || len(decision.Clarify) != 2 || decision.Clarify[0].ProjectID == decision.Clarify[1].ProjectID {
		t.Fatalf("same-name Projects collapsed into title identity: %#v", decision)
	}
	archived := first
	archived.Header = organizationTestHeader("bonfire", first.ProjectID, 2, STRIDEContractProject, 'c', now.Add(4*time.Minute))
	archived.Lifecycle, archived.UpdatedAt = "archived", archived.Header.CreatedAt
	archived.Supersedes = &STRIDEReference{ContractType: STRIDEContractProject, ID: first.ProjectID, Revision: 1, Digest: first.Header.ContentDigest}
	if err := service.ReviseProject(authority, 1, archived, strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	decision, _ = service.ResolveProjectLink(authority, ProjectLinkRequest{Text: "Launch"})
	if decision.Status != "proposed" || decision.Candidate == nil || decision.Candidate.ProjectID != second.ProjectID {
		t.Fatalf("archived Project remained a candidate: %#v", decision)
	}
}

func TestProjectLinkerClarifiesUnequalEligibleMatches(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	service := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	first, firstBinding := projectAuthorityProject(authority, "project_launch", "Launch", "thread_launch", now.Add(2*time.Minute))
	second, secondBinding := projectAuthorityProject(authority, "project_launch_plan", "Launch plan", "thread_plan", now.Add(3*time.Minute))
	if err := service.CreateProject(authority, first, firstBinding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateProject(authority, second, secondBinding, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	decision, err := service.ResolveProjectLink(authority, ProjectLinkRequest{Text: "Launch plan"})
	if err != nil || decision.Status != "clarify" || len(decision.Clarify) != 2 || decision.Clarify[0].Confidence == decision.Clarify[1].Confidence {
		t.Fatalf("unequal eligible matches silently selected: %#v %v", decision, err)
	}
}
