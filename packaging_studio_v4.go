package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

const packagingStudioV4Digest = "b3001cc5bf1a32fce682a446ead3c495e76683a42b66dec0c3c7ef24e3ed7f3d"

// packagingStudioV4DefinitionJSON is the exact JSON projection of the active
// generation-169 definition at dcab3fadb8ad9c4d07e373ef5f15d558b7d4271f.
// It is retained so persisted production receipts remain inspectable and
// identity-verifiable after the current process ships. Unfinished v4 work is
// quarantined at execution time; it is never resumed under current authority.
//
//go:embed packaging_studio_v4_definition.json
var packagingStudioV4DefinitionJSON []byte

func packagingStudioDefinitionV4() ProcessDefinition {
	var definition ProcessDefinition
	if err := json.Unmarshal(packagingStudioV4DefinitionJSON, &definition); err != nil {
		panic(fmt.Sprintf("frozen Packaging Studio v4 definition is invalid: %v", err))
	}
	for index := range definition.Stages {
		switch definition.Stages[index].ID {
		case "source_snapshot":
			definition.Stages[index].Compile = compileExternalEvidenceSourceSnapshots
		case "evidence":
			definition.Stages[index].Compile = compileProcessEvidenceDossier
		case "imagery_generate":
			definition.Stages[index].Compile = compilePackagingStudioImagery
		case "draft_compile":
			definition.Stages[index].Compile = compilePackagingStudioDraft
		case "slide_jury":
			definition.Stages[index].Compile = compilePackagingStudioSlideJury
		case "ship_compile":
			definition.Stages[index].Compile = compilePackagingStudioShip
		}
	}
	return definition
}
