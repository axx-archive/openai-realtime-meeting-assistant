package main

// STRIDEProductAgentExport is deliberately non-portable as authority. It is a
// clean, body-free attribution receipt for an organization-owned coworker; it
// excludes tenant identity, owner, direct thread, memberships, assignments,
// budgets, local personality notes, learning, performance, and credentials.
type STRIDEProductAgentExport struct {
	Schema                    string `json:"schema"`
	ListingID                 string `json:"listingId"`
	Category                  string `json:"category"`
	LifecycleStatus           string `json:"lifecycleStatus"`
	HistoricalAttributionHash string `json:"historicalAttributionHash"`
	ProviderExecutionFenced   bool   `json:"providerExecutionFenced"`
	AccessRevoked             bool   `json:"accessRevoked"`
	ContainsTenantData        bool   `json:"containsTenantData"`
	ContainsCredentials       bool   `json:"containsCredentials"`
	ContainsMemory            bool   `json:"containsMemory"`
	ContainsAssignments       bool   `json:"containsAssignments"`
	ContainsPrivateEvidence   bool   `json:"containsPrivateEvidence"`
}

func safeSTRIDEProductAgentExport(agent STRIDEProductTeamAgent) (STRIDEProductAgentExport, error) {
	if validateSTRIDEProductAgent(agent) != nil {
		return STRIDEProductAgentExport{}, ErrSTRIDEProductInvalid
	}
	attribution, err := STRIDEContractDigest(struct {
		AgentID   string
		ListingID string
		CreatedAt string
	}{agent.ID, agent.ListingID, agent.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")})
	if err != nil {
		return STRIDEProductAgentExport{}, ErrSTRIDEProductInvalid
	}
	return STRIDEProductAgentExport{
		Schema: "stride.agent_export.v1", ListingID: agent.ListingID, Category: agent.Category, LifecycleStatus: agent.Status,
		HistoricalAttributionHash: attribution, ProviderExecutionFenced: true, AccessRevoked: agent.AccessRevoked,
		ContainsTenantData: false, ContainsCredentials: false, ContainsMemory: false, ContainsAssignments: false, ContainsPrivateEvidence: false,
	}, nil
}
