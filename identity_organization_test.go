package main

import (
	"testing"
	"time"
)

// The shell chip and the phone subtitle read the organization name from the
// identity payload. A single-organization workspace never shows an
// "unavailable" label: the authority store's one active organization wins,
// then the environment, then the product default.
func TestIdentityPayloadCarriesWorkspaceOrganizationName(t *testing.T) {
	t.Setenv("STRIDE_ORGANIZATION_NAME", "")
	user := &userAccount{Email: "aj@example.com", Name: "AJ"}
	if got := identityPayload(user)["organization"]; got != defaultWorkspaceOrganizationName {
		t.Fatalf("default organization = %v, want %q", got, defaultWorkspaceOrganizationName)
	}
	t.Setenv("STRIDE_ORGANIZATION_NAME", "Ember Labs")
	if got := identityPayload(user)["organization"]; got != "Ember Labs" {
		t.Fatalf("env organization = %v, want Ember Labs", got)
	}
}

func TestSingleActiveOrganizationNameRequiresExactlyOne(t *testing.T) {
	service := NewOrganizationAuthorityService()
	if got := service.SingleActiveOrganizationName(); got != "" {
		t.Fatalf("empty store = %q, want empty", got)
	}
	now := time.Now().UTC()
	service.organizations["org-1"] = Organization{Name: "Bonfire", Status: "active", CreatedAt: now, UpdatedAt: now}
	if got := service.SingleActiveOrganizationName(); got != "Bonfire" {
		t.Fatalf("single org = %q, want Bonfire", got)
	}
	service.organizations["org-2"] = Organization{Name: "Other", Status: "archived", CreatedAt: now, UpdatedAt: now}
	if got := service.SingleActiveOrganizationName(); got != "Bonfire" {
		t.Fatalf("inactive sibling should not count, got %q", got)
	}
	service.organizations["org-3"] = Organization{Name: "Third", Status: "active", CreatedAt: now, UpdatedAt: now}
	if got := service.SingleActiveOrganizationName(); got != "" {
		t.Fatalf("two active orgs = %q, want empty", got)
	}
}
