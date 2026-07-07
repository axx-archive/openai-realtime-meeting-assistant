# Proposal — Gmail Integration: Consent & Scope (card 083)

**Status:** Proposal, awaiting team ratification. **No Gmail code ships until
this is ratified.** This document is the written consent-and-scope proposal for
connecting Bonfire to each member's Gmail. It is mirrored at runtime by the
published in-product artifact seeded from `gmail_consent_proposal.go`
(`gmailConsentProposalBody`); keep the two in sync — the load-bearing sections
below (scope names, retention, disconnect, and the `Decision:` sentence) are
pinned by `gmail_consent_proposal_test.go` so the policy cannot be hollowed out
silently.

## Purpose

We want Bonfire to understand who each of us actually talks to — the
counterparties, the cadence, the relationship graph — so the OS can connect
board work, deals, and meetings to the real network behind them. Gmail is the
richest source of that graph. It is also the single most sensitive integration
we can add: a careless scope grant hands a third-party app the contents of
every member's inbox. This proposal fixes the exact scopes, the exact data we
ingest, where it may and may not be stored, how long it lives, how a member
connects, and how a member walks away — before a line of Gmail code exists.

## Scope options

### Option A — default, minimal (recommended)

Connect with **metadata and contacts only**. No message bodies, ever.

- `https://www.googleapis.com/auth/gmail.metadata` — message **headers only**
  (From, To, Cc, Subject, Date, thread IDs, labels). Google does not return the
  body with this scope; the API refuses `format=full`. This is enough to build
  the counterparty and frequency graph.
- `https://www.googleapis.com/auth/contacts.readonly` — the member's own
  contacts (names, emails, org) to resolve addresses to people.
- `https://www.googleapis.com/auth/contacts.other.readonly` — "other contacts"
  auto-collected by Google, so we resolve people the member emails but has not
  saved as a contact.

`gmail.metadata` and the contacts scopes are **sensitive** but not
**restricted**: they clear Google's OAuth verification without the heavier
review track. This is the scope set we should ship first.

### Option B — escalation, requires its own separate ratification

- `https://www.googleapis.com/auth/gmail.readonly` — full read access
  including **message bodies and attachments**.

`gmail.readonly` is a Google **restricted** scope. Adopting it triggers, at
minimum: OAuth app verification with a restricted-scope justification, and an
annual **CASA** (Cloud Application Security Assessment) third-party security
assessment of our app, at our cost, every year we hold the scope. It also
raises the blast radius of any credential compromise from "who they email" to
"everything in their inbox." **We do not adopt Option B in this proposal.** If a
concrete feature later needs body content, it comes back as its own proposal
with its own ratification, its own retention rules, and an explicit owner for
the CASA burden.

## Ingestion inventory — what we take, what we never take

We ingest:

- **Contact cards** — name, email address(es), organization, resolved from the
  member's contacts and other-contacts.
- **Thread-level counterparty + frequency metadata** — for each correspondent:
  who, how often, most-recent-contact recency, direction (inbound/outbound
  balance), derived from headers only.

We **never** ingest, under Option A:

- Message bodies.
- Attachments or their contents.
- Draft contents.

Subject lines are technically returned by `gmail.metadata`. We treat subjects
as sensitive free text: they are used only to derive a thread's existence and
are **not** persisted verbatim in any durable digest (see Retention).

## Storage doctrine — Gmail data must NEVER enter shared meeting memory

This is the hard architectural constraint and the reason this needs a written
proposal rather than a quick build.

The shared meeting-memory JSONL (`memory.go`) is exactly that — **shared**.
Entries written there are recalled into every signed-in account's context: the
recall path (`memory.go` `recall`) and the shared Scout query context surface
memory entries to whoever is asking, regardless of who wrote them. Writing one
member's Gmail-derived contacts or counterparties into that store would leak
their private relationship graph to every other account. **That is
disqualifying.**

Therefore a future implementation MUST use a **per-user store**, isolated by
account, never the shared JSONL:

- OAuth tokens live in a new `data/gmail_tokens.json`, keyed by account email,
  added to the deploy preserve list alongside the existing preserved
  `data/users.json` and `data/sessions.json` (see `deploy/digitalocean`).
- Derived per-user digests live in a per-account store (per-user file or
  per-user key namespace), readable only in that member's own session — never
  through shared Scout recall, never broadcast, never in another member's
  mission context.

No Gmail-derived byte is ever written to the shared meeting-memory JSONL.

## Retention

- **No raw API responses are persisted.** Header pulls are processed in memory
  into the derived graph and discarded.
- Only **derived digests** (counterparty + frequency graph, resolved contact
  cards) are stored, per-user, on a **30-day rolling window**. Anything older
  than 30 days is recomputed on next sync or dropped.
- **Purge on disconnect** (below) removes all derived digests immediately.

## Per-user consent flow

Consent is **individual**. Each account in the roster (`accounts.go`
`seededAccounts` — Joel, Caitlyn, Tyler, AJ, Tim, Erick, Tom) connects Gmail
**for themselves only**, from their own Settings, with their own Google OAuth
grant. **Nobody connects Gmail on another member's behalf.** There is no
admin-connects-everyone path and no room-level Gmail grant. A member who never
connects has zero Gmail data in the system.

## Opt-out / revocation

- **One-click disconnect** in Settings: revoke the token with Google (OAuth
  token revocation endpoint) and **purge** that member's tokens and all derived
  digests immediately.
- **Auto-purge on failure:** if Google returns `401 invalid_grant` (the member
  revoked access from the Google side, or the grant expired), we treat it as a
  disconnect — drop the token and purge the derived digests without waiting for
  a manual click.

## Future environment inventory (informational — NOT added by this card)

Listed so ratification is fully informed. **None of these are added now.** They
belong to the future implementation card if and when this is ratified:

- Env vars: `GOOGLE_OAUTH_CLIENT_ID`, `GOOGLE_OAUTH_CLIENT_SECRET`,
  `GOOGLE_OAUTH_REDIRECT_URL` (thread through `os.Getenv` defaults,
  `deploy/digitalocean/.env.example`, and the deploy README).
- Go deps: `golang.org/x/oauth2`, `google.golang.org/api`.
- Preserve-on-deploy: add `data/gmail_tokens.json` to the preserved-files list.

## Ratification

To ratify, a team member states the following sentence in a meeting (so the
decision ledger records it verbatim onto the mission ledger and Scout's
"Decisions on record"). Card 083 stays gated until that `kind=decision` entry
exists. To adopt Option B later, or to reverse this, use the existing
supersede door (`POST /assistant/decisions/supersede`).

> **Decision:** Gmail integration ships with Option A minimal scopes only (gmail.metadata + contacts.readonly, never bodies), per-user connect, never in shared memory, one-click disconnect purges all.
