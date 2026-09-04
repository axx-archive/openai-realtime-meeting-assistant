package main

// packaging_commission_adapter.go wires the chat-intake seam
// (packaging_intake.go's commissionLauncher, owned by the intake dev) to the
// Packaging Studio commissions API. A completed chat brief becomes the same
// typed commission a studio sheet posts: validated per kind, committed as the
// asker's message in their private thread, launched through
// startConversationPrivateWork, brief stamped on the root. Document asks are
// not a commission kind (documents stay in the chat harness) and return an
// error so the intake keeps its legacy document_report launch.

import (
	"context"
	"fmt"
	"strings"
)

type packagingCommissionLauncherAdapter struct{ app *kanbanBoardApp }

func init() {
	packagingCommissionLauncherFactory = func(app *kanbanBoardApp) commissionLauncher {
		if app == nil {
			return nil
		}
		return packagingCommissionLauncherAdapter{app: app}
	}
}

// packagingIntakeCopyStyle translates the intake's copy-style vocabulary
// (packaging_intake.go reads eight words out of the ask) into the studio's
// four. A word the studio does not know is DROPPED, never failed: copyStyle is
// optional in the brief, and refusing the whole commission over "punchy" would
// lose the ask entirely — the intake record goes terminally failed and cannot
// be retried on that message.
func packagingIntakeCopyStyle(style string) string {
	switch packagingLower(strings.ReplaceAll(style, " ", "-")) {
	case "crisp", "punchy", "formal":
		return "crisp"
	case "narrative", "casual":
		return "narrative"
	case "data-led":
		return "data-led"
	case "persuasive":
		return "persuasive"
	}
	return ""
}

// packagingIntakeImageryMode translates the intake's imagery vocabulary into
// the studio's three modes. Dropped, never failed, for the same reason
// copyStyle is: the intake's own deferral answer writes the literal "Scout's
// call" into every unanswered choice field, and refusing the commission over
// it would mark the intake TERMINALLY failed on that message — the asker could
// not retry. An empty mode is legal; validatePackagingPresentationBrief
// defaults it to hybrid, which is exactly what "Scout's call" means.
func packagingIntakeImageryMode(mode string) string {
	switch mode = packagingLower(mode); mode {
	case packagingImageryFullBleed, packagingImageryOnSlide, packagingImageryHybrid:
		return mode
	case "none":
		// extractPackagingIntakeBrief reads "no images"/"text only" as "none";
		// the studio has no imageless mode, so plates are the closest honest one.
		return packagingImageryOnSlide
	}
	// A numbered reply ("1. full bleed") is stored RAW by applyBriefAnswers'
	// numbered branch — no canonicalization against the question's options,
	// unlike the bare-list branch — so ask the intake's own option matcher
	// which option the asker named. Still drop-don't-fail: "Scout's call"
	// names no option, matches nothing, and defaults to hybrid as before.
	return packagingIntakeNamedOption("imagery", mode)
}

// packagingIntakeDepth translates the intake's research depth. Same
// drop-don't-fail rule: depth is optional to the caller (the adapter defaults
// it to standard), the deliverable is not.
func packagingIntakeDepth(depth string) string {
	if depth = packagingLower(depth); oneOf(depth, packagingResearchDepths...) {
		return depth
	}
	// Same numbered-reply seam as imagery: "1. deep dive" is stored raw and
	// must still reach the engine as "deep" rather than silently standard.
	return packagingIntakeNamedOption("depth", depth)
}

// packagingIntakeNamedOption asks the intake catalog's own matcher which
// option (if any) the stored answer names, so both spellings of a choice —
// the canonical "full-bleed" the option-word path writes and the natural
// "full bleed" a numbered reply stores verbatim — translate identically.
// No option named is not an error: the caller drops the value and the brief
// validator applies its default, which is what a deferral means.
func packagingIntakeNamedOption(questionID string, answer string) string {
	question := packagingIntakeQuestionCatalog[questionID]
	for _, option := range question.Options {
		if packagingIntakeQuestionOptionMatches(question, option, answer) {
			return option
		}
	}
	return ""
}

// packagingIntakeLength translates the intake's length vocabulary ("one page",
// "12 slides", "6 pages", "short", "long") into the studio's (short | standard
// | long | a slide count). Anything else is dropped rather than failing the
// commission — length is optional, the deliverable is not.
func packagingIntakeLength(length string) string {
	length = packagingLower(length)
	switch length {
	case "one page", "one pager", "one-page", "one-pager", "memo":
		return "short"
	}
	if pages := strings.TrimSuffix(strings.TrimSuffix(length, " pages"), " page"); pages != length {
		length = pages + " slides"
	}
	if _, err := packagingLengthSlides(length); err != nil {
		return ""
	}
	return length
}

// packagingIntakeResearchFormat reads the deliverable shape out of the same
// length vocabulary: the intake stores "one page" (never "one-pager"), so a
// one-pager ask must be translated or the research runs as a full report.
func packagingIntakeResearchFormat(length string) string {
	switch packagingLower(length) {
	case "one page", "one pager", "one-page", "one-pager":
		return "one-pager"
	case "memo":
		return "memo"
	}
	return ""
}

// packagingBriefFromIntake maps the chat brief onto the studio brief of the
// same kind. Drive refs (file|<id>) become authorized sources; http(s) and
// artifact ids become link/artifact sources; anything else the asker named
// is folded into the question so nothing they said is dropped.
func packagingBriefFromIntake(kind string, intake packagingIntakeBrief) (packagingBrief, error) {
	sources := make([]packagingSource, 0, len(intake.ContextRefs)+len(intake.Sources))
	named := make([]string, 0)
	for _, ref := range intake.ContextRefs {
		if ref = strings.TrimSpace(ref); strings.HasPrefix(ref, "file|") {
			sources = append(sources, packagingSource{Ref: ref})
		}
	}
	for _, source := range intake.Sources {
		source = strings.TrimSpace(source)
		switch {
		case source == "":
		case strings.HasPrefix(source, "file|"), strings.HasPrefix(source, "artifact|"), strings.HasPrefix(strings.ToLower(source), "http://"), strings.HasPrefix(strings.ToLower(source), "https://"):
			sources = append(sources, packagingSource{Ref: source})
		case strings.HasPrefix(source, "os-artifact-"):
			sources = append(sources, packagingSource{ArtifactID: source})
		default:
			named = append(named, source)
		}
	}
	ask := strings.TrimSpace(intake.Ask)
	if len(named) > 0 {
		ask += " Sources to consult: " + strings.Join(named, "; ") + "."
	}
	switch packagingLower(kind) {
	case packagingCommissionKindResearch:
		research := &packagingResearchBrief{
			Scope: "market", Depth: firstNonEmptyString(packagingIntakeDepth(intake.Depth), "standard"), Format: "report",
			Audience: intake.Audience, Question: ask, Sources: sources,
		}
		if format := packagingIntakeResearchFormat(intake.Length); format != "" {
			research.Format = format
		}
		if err := validatePackagingResearchBrief(research); err != nil {
			return packagingBrief{}, err
		}
		return packagingBrief{Kind: packagingCommissionKindResearch, Research: research}, nil
	case packagingCommissionKindPresentation:
		presentation := &packagingPresentationBrief{
			Subject: ask, Audience: intake.Audience, CopyStyle: packagingIntakeCopyStyle(intake.CopyStyle),
			ImageryMode: packagingIntakeImageryMode(intake.Imagery), Length: packagingIntakeLength(intake.Length),
		}
		// Everything the asker attached or named rides the deck brief: the
		// artifact that reads as research grounds it, and every other source
		// (Drive files above all) is authorized and attached at launch.
		for _, source := range sources {
			if source.ArtifactID != "" && presentation.Research == nil {
				presentation.Research = &packagingResearchInput{ArtifactID: source.ArtifactID}
				continue
			}
			presentation.Sources = append(presentation.Sources, source)
		}
		if err := validatePackagingPresentationBrief(presentation); err != nil {
			return packagingBrief{}, err
		}
		return packagingBrief{Kind: packagingCommissionKindPresentation, Presentation: presentation}, nil
	case packagingCommissionKindStory:
		story := &packagingStoryBrief{Subject: ask, Audience: intake.Audience, Length: packagingIntakeLength(intake.Length)}
		if err := validatePackagingStoryBrief(story); err != nil {
			return packagingBrief{}, err
		}
		return packagingBrief{Kind: packagingCommissionKindStory, Story: story}, nil
	}
	return packagingBrief{}, fmt.Errorf("packaging commissions do not launch %q asks; use the document harness", kind)
}

func (adapter packagingCommissionLauncherAdapter) createPackagingCommission(principal *userAccount, kind string, intake packagingIntakeBrief) (packagingCommissionReceipt, error) {
	if adapter.app == nil || principal == nil {
		return packagingCommissionReceipt{}, fmt.Errorf("packaging studio is unavailable")
	}
	brief, err := packagingBriefFromIntake(kind, intake)
	if err != nil {
		return packagingCommissionReceipt{}, err
	}
	operationID := ""
	// The intake record's own id (packaging_intake.go derives it from the same
	// thread + ask message), carried down so the commission root can be
	// resolved back to the intake that asked for it — the record only learns
	// its commissionId after this call returns.
	intakeID := ""
	if strings.TrimSpace(intake.MessageID) != "" {
		operationID = "packaging-intake-" + sha256Hex([]byte(strings.TrimSpace(intake.ThreadID) + "\x00" + strings.TrimSpace(intake.MessageID)))[:24]
		intakeID = packagingIntakeRecordID(strings.TrimSpace(intake.ThreadID), strings.TrimSpace(intake.MessageID))
	}
	ctx := withPackagingCommissionIntake(context.Background(), intakeID)
	if brief.Kind == packagingCommissionKindStory {
		entry, thread, createErr := adapter.app.createPackagingStoryWithContext(ctx, principal, *brief.Story, operationID)
		if createErr != nil {
			return packagingCommissionReceipt{}, createErr
		}
		return packagingCommissionReceipt{CommissionID: entry.ID, ArtifactID: entry.ID, Label: "Story outline", Thread: &scoutChatThreadRef{ID: thread.ID}}, nil
	}
	result, err := adapter.app.launchPackagingCommission(ctx, principal, brief, intake.ThreadID, operationID)
	if err != nil {
		return packagingCommissionReceipt{}, err
	}
	label := "Research"
	if brief.Kind == packagingCommissionKindPresentation {
		label = "Presentation"
	}
	receipt := packagingCommissionReceipt{CommissionID: result.Launched.Artifact.ID, ArtifactID: result.Launched.Artifact.ID, Label: label}
	if answer, ok := result.Commit["answer"].(scoutChatMessageRecord); ok && answer.Thread != nil {
		ref := *answer.Thread
		receipt.Thread = &ref
	} else {
		receipt.Thread = &scoutChatThreadRef{ID: result.Launched.ID, Mode: result.Launched.Mode, Query: result.Launched.Query, Status: result.Launched.Status, ArtifactID: result.Launched.Artifact.ID}
	}
	return receipt, nil
}
