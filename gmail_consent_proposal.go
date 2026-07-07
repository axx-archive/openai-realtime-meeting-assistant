package main

// Card 083 — Gmail integration consent + scope. This ships NO Gmail code: the
// deliverable is a written consent-and-scope proposal, published as an
// in-product artifact with a broadcast bell nudge, plus an exact ratification
// sentence the decision ledger can extract verbatim. Card 083 stays gated
// until that kind=decision entry exists.
//
// The proposal body is a Go const (not go:embed) so the Docker build context
// stays untouched. It is the runtime MIRROR of docs/proposals/
// gmail-consent-scope-083.md; keep the two in sync — the content test
// (gmail_consent_proposal_test.go) pins the load-bearing sections so the
// policy cannot be hollowed out silently.

import "time"

// gmailConsentProposalArtifactID is fixed so the boot seed is idempotent:
// appendEntryForMeeting dedupes on store.seen by ID, so a restart re-runs the
// seed and appends nothing (appended=false), and the broadcast notification —
// created only on appended==true — is never duplicated either.
const gmailConsentProposalArtifactID = "os-artifact-research-gmail-consent-083"

const gmailConsentProposalTitle = "Proposal: Gmail integration consent + scope"

// gmailConsentProposalNotificationText is the broadcast bell copy every
// teammate sees; clicking it deep-links to the artifact via the artifactId.
const gmailConsentProposalNotificationText = "Proposal ready: Gmail integration consent + scope — read and ratify before any code"

// gmailConsentProposalBody mirrors docs/proposals/gmail-consent-scope-083.md.
const gmailConsentProposalBody = `# Proposal — Gmail Integration: Consent & Scope (card 083)

**Status:** Proposal, awaiting team ratification. **No Gmail code ships until this is ratified.**

## Purpose

We want Bonfire to understand who each of us actually talks to — the counterparties, the cadence, the relationship graph — so the OS can connect board work, deals, and meetings to the real network behind them. Gmail is the richest source of that graph, and the single most sensitive integration we can add. This proposal fixes the exact scopes, the data we ingest, where it may and may not be stored, retention, per-user consent, and disconnect — before a line of Gmail code exists.

## Scope options

### Option A — default, minimal (recommended)

Connect with metadata and contacts only. No message bodies, ever.

- ` + "`https://www.googleapis.com/auth/gmail.metadata`" + ` — message headers only (From, To, Cc, Subject, Date, thread IDs, labels). Google refuses ` + "`format=full`" + ` under this scope, so bodies never come back. Enough to build the counterparty and frequency graph.
- ` + "`https://www.googleapis.com/auth/contacts.readonly`" + ` — the member's own contacts, to resolve addresses to people.
- ` + "`https://www.googleapis.com/auth/contacts.other.readonly`" + ` — auto-collected "other contacts", to resolve people the member emails but has not saved.

gmail.metadata and the contacts scopes are sensitive but not restricted: they clear Google's OAuth verification without the heavier review track. Ship this set first.

### Option B — escalation, requires its own separate ratification

- ` + "`https://www.googleapis.com/auth/gmail.readonly`" + ` — full read access including message bodies and attachments.

gmail.readonly is a Google restricted scope: adopting it triggers OAuth app verification with a restricted-scope justification and an annual CASA (Cloud Application Security Assessment) third-party security audit, at our cost, every year we hold it. **We do not adopt Option B here.** If a feature later needs body content, it returns as its own proposal with its own ratification and an explicit owner for the CASA burden.

## Ingestion inventory

We ingest: contact cards (name, email(s), org) and thread-level counterparty + frequency metadata (who, how often, recency, direction), derived from headers only.

We never ingest, under Option A: message bodies, attachments or their contents, or draft contents. Subject lines are treated as sensitive free text and are not persisted verbatim in any durable digest.

## Storage doctrine — Gmail data must NEVER enter shared meeting memory

The shared meeting-memory JSONL is recalled into every signed-in account's context. Writing one member's Gmail-derived contacts or counterparties there would leak their private relationship graph to every other account — disqualifying. A future implementation MUST use a per-user store isolated by account, never the shared JSONL: OAuth tokens in a new ` + "`data/gmail_tokens.json`" + ` (keyed by email, added to the deploy preserve list next to the preserved ` + "`data/users.json`" + ` and ` + "`data/sessions.json`" + `), and derived digests in a per-account store readable only in that member's own session — never through shared Scout recall, never in another member's mission context.

## Retention

- No raw API responses are persisted; header pulls are processed in memory and discarded.
- Only derived digests (counterparty + frequency graph, resolved contact cards) are stored, per-user, on a 30-day rolling window.
- Disconnect purges all derived digests immediately.

## Per-user consent flow

Consent is individual. Each account in the roster (Joel, Caitlyn, Tyler, AJ, Tim, Erick, Tom) connects Gmail for themselves only, from their own Settings, with their own Google OAuth grant. Nobody connects Gmail on another member's behalf. There is no admin-connects-everyone path and no room-level grant. A member who never connects has zero Gmail data in the system.

## Disconnect (opt-out) / revocation

- One-click disconnect in Settings: revoke the token with Google and purge that member's tokens and all derived digests immediately.
- Auto-purge on failure: a Google ` + "`401 invalid_grant`" + ` (the member revoked from Google's side, or the grant expired) is treated as a disconnect — drop the token and purge the derived digests without a manual click.

## Future environment inventory (informational — NOT added by this card)

None of these are added now; they belong to the future implementation card if this is ratified: env vars ` + "`GOOGLE_OAUTH_CLIENT_ID`" + `, ` + "`GOOGLE_OAUTH_CLIENT_SECRET`" + `, ` + "`GOOGLE_OAUTH_REDIRECT_URL`" + `; Go deps ` + "`golang.org/x/oauth2`" + ` and ` + "`google.golang.org/api`" + `; and ` + "`data/gmail_tokens.json`" + ` added to the preserve-on-deploy list.

## Ratification

To ratify, a team member states the following sentence in a meeting so the decision ledger records it verbatim onto the mission ledger and Scout's "Decisions on record". Card 083 stays gated until that decision entry exists. To adopt Option B later, or to reverse this, use the existing supersede door (` + "`POST /assistant/decisions/supersede`" + `).

**Decision:** Gmail integration ships with Option A minimal scopes only (gmail.metadata + contacts.readonly, never bodies), per-user connect, never in shared memory, one-click disconnect purges all.
`

// seedGmailConsentProposal publishes the card-083 Gmail consent proposal as a
// fixed-ID research artifact and, on the FIRST boot only (appended==true),
// fans out the artifact push event and a broadcast bell nudge. Idempotent
// across restarts via the fixed artifact ID; a keyless boot mints the artifact
// (this is a store write, not a worker), and the first append lazily mints a
// meetingId like every other ambient store write. Nil-guarded like every seam.
func seedGmailConsentProposal(app *kanbanBoardApp) {
	if app == nil || app.memory == nil {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	metadata := map[string]string{
		"mode":                     "research",
		"query":                    "Gmail integration consent + scope",
		"title":                    gmailConsentProposalTitle,
		"status":                   "published",
		"published":                "true",
		"publishedBy":              scoutParticipantName,
		"publishedAt":              now,
		"type":                     artifactTypeMarkdown,
		artifactVersionMetadataKey: "1",
		"createdBy":                scoutParticipantName,
	}

	entry, appended, err := app.memory.appendOSArtifact(gmailConsentProposalArtifactID, gmailConsentProposalBody, metadata)
	if err != nil {
		log.Errorf("Failed to seed the Gmail consent proposal artifact: %v", err)
		return
	}
	if !appended {
		// Restart: the artifact (and its one-time broadcast) already exist.
		return
	}

	// First boot only: fan the artifact out over the push channel and drop the
	// broadcast bell nudge whose artifactId deep-links every teammate to the
	// full proposal.
	emitOSArtifactEvent(entry)
	if _, err := app.createNotification("", notificationKindTask, gmailConsentProposalNotificationText, "", entry.ID, "", false); err != nil {
		log.Errorf("Failed to broadcast the Gmail consent proposal notification: %v", err)
	}
}
