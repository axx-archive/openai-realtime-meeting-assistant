# Public work identity: one excellent, permissioned case study

Decision owner: STRIDE product lead. Date: 2026-09-04. Design only; no public profile or release capability is claimed live. Implements the optional public-identity and Human Contribution Protocol sections of `stride-agent-business-product-contract.md`. The tenant-native Business store owns the underlying Work. Legacy objects below are prototypes, not an authority to activate.

## Product decision

A public STRIDE profile is an optional portfolio of work someone can help accomplish. Its most important unit is an excellent case study: what was being built, the actual artifact, who changed the work, what happened, and which parts another person can inspect. This earns credibility through work that happened inside STRIDE. It supports an invitation to collaborate without requiring a feed, follower graph, talent search engine, marketplace, or universal rating.

Ship one flow: **exact private Work result → contribution draft → selected public case study → approval and publish → correction or withdrawal → verify current status**. A person and an employed agent can each feature the same released case study with their own contribution highlighted. They do not receive separate invented credit for the same intervention. Publishing is optional; completing excellent private work is still the primary product.

The first proof is STRIDE Builders producing a useful private artifact. A human identifies a real defect; the agent makes the evidenced change; the business records what the improved artifact helped accomplish, or honestly records that the outcome is not yet known. The human and the employed agent receive different contribution statements over that one work history. AJ may approve a minimal public version when the actual result exists. We do not fabricate a customer or publish this plan as proof.

## Experience worth sharing

From the result viewer, **Create a case study** opens a private composer already anchored to that immutable result. It does not make the result public. The first screen is the finished-looking case study, not a permission spreadsheet. It contains a strong title, a concise problem statement, a large artifact preview, a small contribution story, and an outcome with its evidence label. A persistent “Private draft” label and a clear **Review what will be public** action explain the next step.

The editor proposes concise text only from available evidence. It distinguishes “Recorded in STRIDE,” “Contributor’s account,” “Evaluator’s assessment,” and “Outcome reported by ….” These are labels on particular claims, not a badge granting credibility to the whole page. Missing evidence stays “Not shared” or “Not yet measured.” An evaluation saying the change helped is not rendered as observed revenue or hours saved.

The release preview is exactly the anonymous viewer’s page. Selecting a name, company, metric, screenshot, quote, or linked artifact shows its release status inline. A side panel on desktop, and a bottom sheet on phone, explains the one unresolved permission in plain language: “Maya needs to approve being named” or “This screenshot includes unreleased customer data.” The editor can remove that field and continue. No default checklist asking every organization member to approve.

The finished page reads like a considered editorial case study: generous type, an artifact that can be examined at full size, concise context, and an evidence drawer for the interested reader. Native markdown/document previews must be fast and accessible, not a screenshot of a desktop app shrunk onto a phone. Use art-directed image crops with accessible captions, keyboard controls, reduced-motion support, and no autoplay media. Screenshots are optional and added only after the authored text proof works. Do not send private files to an external preview service.

A profile has a name/avatar, a short self-authored description of what the collaborator helps build, selected case studies, and optional availability text. No empty skills cloud, synthetic endorsements, token leaderboard, “AI score,” or social counts. A case study can link consenting collaborators’ profiles. A business can be unnamed; merely publishing a person’s profile must not reveal private memberships. An agent profile identifies an actual employment, not a model pretending to be an enduring person. An offering publisher cannot add an employer’s case study to its catalogue without a separate release.

Initial sharing offers **Public link** and a copy-link action. It is readable without signing in, uses `noindex`, and is absent from browse/search directories. The copy action does not send a message. It is public to anyone with the link, not access-controlled or confidential. Public indexing is a later explicit owner-and-subject choice, not an automatic upgrade. Inbound messages and contact discovery remain out of this slice; an optional subject-selected public collaboration link can appear only if independently approved as a public field.

## Current source and reuse decision

| Prototype or current foundation | Useful semantics | Decision for this slice |
| --- | --- | --- |
| `stride_contribution_network_contracts.go`: `ContributionClaim`, `FieldReleaseApproval`, `ContributionAttestation`, `PublishedContributionClaim` | Exact references, field-value digests, separate source/subject approval, revision and supersession states | Carry the concepts and adversarial test cases. Replace person-only subjects, legacy tenant references and opaque whole-claim “verified” labels with typed person/employment subjects and claim-level evidence classes. |
| `stride_contribution_authority.go`: `Publish`, `WithdrawPublication`, `FenceDrift` | Current approval checks, idempotency, CAS and immediate field invalidation | Implement these rules in the same PostgreSQL transaction domain as new Business Work and current memberships. Do not activate the in-memory service as a second authority. |
| `migrations/0015_stride_contributions.sql` | Immutable history and body-minimized authority rows | Do not apply as the new slice migration: it references legacy `stride_organizations` and person-only claims. Add a new Business migration with current foreign keys and deliberate public/private separation. |
| `stride_network_authority.go`, `NetworkProfileProjection` | Selective public fields, consent, contact controls and anti-extraction ideas | Borrow projection allowlists and withdrawal tests. Defer network search, ranking, contacts, grant mirrors and their process-local maps. |
| `stride_e10_product_live.go` constructor | Shows contribution/network services are installed separately with empty initial grants | This is not evidence of a working public portfolio or new Business publication authority. Keep it separate. |
| `internal/business/attempt_contracts.go`, `attempt_store.go` | Exact Work/attempt/result identity, immutable private content, current eligibility and actual/unknown cost | Use as the first source. `GetResult().Eligible` is necessary for a new release but never sufficient publication permission. Add source release and contribution records; do not treat an execution receipt as contribution evaluation. |

The new SQL domain is the single owner of publication derived from its Work. No write-through to W4 or legacy contribution stores. Public profile identity references the same authenticated person ID used by the login adapter, without importing W4 organization membership. A profile for an agent references one explicit employment. Profiles spanning future independently hosted data sources require a source-specific release adapter; this first implementation only accepts sources already owned by the new SQL domain.

## Attribution, assessment and outcome

A `ContributionRecord` is private by default and binds:

`id, revision, organizationId, businessId, subject:{kind:person|employment,id}, workId, attemptIds, resultId, resultDigest, contributionType, actionRefs, sourceRefs, attributionMethod, attributionText, assessment?, outcomeRefs, limitations, state, supersedesId, createdBy, createdAt`.

Contribution types describe interventions: `originated_idea`, `supplied_context`, `created_artifact`, `changed_decision`, `identified_error`, `tested_assumption`, `coordinated_delivery`, or `preserved_dissent`. Multiple records can describe distinct interventions in one Work. The first proof requires a source-bound correction/intervention record comparing exact before/after results; absent that record, the composer cannot assert “caught the defect” just because someone clicked Accept.

An optional assessment contains `evaluatorPrincipal, evaluatorKind, actualRouteRevision?, independenceClass, evidenceRefs, assessmentText, limitations, createdAt`. Record whether the evaluator was the contributor, collaborator, same-provider model, or an independently evidenced reviewer. No “independent” label based only on different prompts or seat names. This first slice can publish source-observed attribution without an automated assessment; it must not invent one to fill the layout. A later evaluator can append an assessment without rewriting the observed action.

An outcome records a measured observation or a named report, its period and evidence, and uncertainty. “The revised onboarding page shipped” may be observed; “the change doubled conversion” requires a corresponding measurement and cannot follow automatically from shipping. No forced allocation of 100% credit. Actual model spend may be a released execution fact; it is neither a score nor causal contribution evidence.

## Exact publication and consent contract

The publisher assembles a new, sanitized `CaseStudyRevision`. It references the private source manifest internally but contains only the selected public projection. Its `publicManifestDigest` is computed over canonical JSON containing the actual public fields, approved asset bytes/digests, schema version and evidence labels. It never includes private IDs or a hash of low-entropy private text. Private source bindings stay in the authenticated store. A redacted public artifact gets its own digest; it is not represented as the original private document.

Public field allowlist:

| Field | Value and limit | Required authority |
| --- | --- | --- |
| `title`, `summary`, `problem`, `approach` | Plain text or sanitized markdown; 120 / 600 / 2,000 / 3,000 characters | Publisher plus source-release authority for every source-derived statement. Self-authored narrative remains explicitly labeled and must still have source clearance if it reveals company information. |
| `artifact` | One released markdown artifact, at most 256 KB, exact public digest | Current source controller for that immutable result and every included dependency; no private attachment URLs or scripts. |
| `contributors[]` | Opaque public profile ref, approved name, kind, contribution record projection; at most 8 | Each named person’s subject consent; employment controller for an agent; source authority for claimed intervention. Omit unapproved identities entirely. |
| `business` | Optional display name/logo/public link | Current business release authority; omission must remove identifying metadata and preview text too. |
| `outcome` | At most 1,000 characters plus evidence class and optional approved metric | Outcome source controller and any separately identified customer/party. No inference promoted to a measured observation. |
| `assessment` | Optional text, evaluator identity/independence class, limitations | Assessment controller, assessed subject’s public consent, source release and named evaluator consent where identifying. |
| `media[]` | Initially empty; later at most 3 reviewed first-party images with captions and public digests | Exact asset release, all depicted/named parties when required, metadata scrub. Video/call recordings are excluded initially. |
| `profileSummary`, `availability`, `collaborationLink` | Subject-authored, bounded text/safe HTTPS link | Subject/controller only, unless the content identifies a private organization or other person. |

No audience expansion follows from a generic owner role, the Business full-autonomy preset, a private result acceptance, or an execution lease. The new release grant must explicitly permit `publish_case_study` for the exact source scope and selected fields. In the first slice, the current Business owner supplies that source approval, and every named person supplies their own subject consent. The same person can fill both roles, but the two decisions are recorded separately. This deliberately small implementation can later accept a standing agent publication mandate over defined sources without inserting human approval into every autonomous workflow. Existing private-work mandates do not supply that authority.

A person controls their public profile, naming consent and withdrawal even after leaving an employer; losing private source access does not remove the right to withdraw an existing public use of their identity. They cannot fetch the private source after departure. An employed agent’s current business controller approves its public representation. The agent’s offering publisher has no implicit right to the employment’s history. Any named customer, collaborator, or third-party source without a supported authoritative release path is omitted for this first slice. The owner cannot waive another person’s consent by declaring the whole company public.

A `ReleaseApproval` binds `approverPrincipal, authorityKind, authorityRevision, sourceRefs/revisions, fieldKeys, fieldValueDigests, destination:public_link, consentRevision, expiresAt?, decision, caseStudyId, createdAt`. Approval is over bytes and destination, not a mutable title. Required approvers are computed from authoritative source dependencies, never supplied solely by the draft. Changing a field or its dependency invalidates the affected approval. Exact unchanged fields may retain valid approvals in a corrected revision, but the publisher must review the new final public manifest.

`Publish` atomically locks the case study and source authority, rechecks current result eligibility, membership/controller rights, current subject consent and every selected source/field approval, then commits the public revision and status. The private draft remains usable when publication is unavailable. Nobody approves a half-defined action: the final preview is exactly the releasable manifest.

## Ownership, delivery and verification

Add private Business tables for contribution revisions, source dependencies, release approvals and case-study drafts/history, plus a narrow public projection keyed by random publication ID. Profile controller records are not scoped by a former employer’s membership; their person-only operations authorize the authenticated subject directly. Agent profile control is derived from current Business employment authority. All underlying Work, drafts, approvals and private source IDs remain behind tenant RLS.

Public readers use a dedicated restricted read path that can return only the explicit projection after checking its current published status and fence generation. They cannot query arbitrary Business tables. Publication and withdrawal update that current projection in the same SQL transaction as their authority records. This first slice serves HTML, verification JSON, markdown downloads and any approved assets through that online authority check with `Cache-Control: no-store`. No CDN-stored public bytes, long-lived object URLs, service-worker caching or external screenshot service are needed for one excellent case study. Network writes use action-time authorization; a response already sent cannot be remotely recalled.

Suggested authenticated routes under `/api/business/v1`:

- `POST /businesses/:id/case-studies` creates a private draft from exact source/result refs and selected contributions.
- `PATCH /case-studies/:id` changes the draft with expected revision and idempotency key.
- `POST /case-studies/:id/approvals` records the current actor’s exact allowed field approvals.
- `POST /case-studies/:id/publish` binds expected draft revision and public manifest digest.
- `POST /case-studies/:id/correct` creates a replacement draft; a factual challenge can fence disputed fields immediately.
- `POST /case-studies/:id/withdraw` withdraws as publisher, subject for their own release, or source controller for their released material; body cannot designate someone else’s consent as authority.

Public routes are `/w/:opaquePublicationId` and `/api/public/work/:opaquePublicationId`. Optional profile `/people/:handle` or `/agents/:handle` lists only current permitted projections, not private membership. A handle is cosmetic; publication IDs and profile IDs survive renaming. No search/index/contact endpoint is part of this slice.

The JSON verification response returns `schemaVersion, publicationId, revision, state, publicManifestDigest, publishedAt, lastCheckedAt, supersedesPublicRevision?, evidenceSummary, limitations` and the selected public fields while active. It states that STRIDE can verify the released artifact/receipt binding and current publication status; it does not declare all narratives or evaluator judgments true. A downloaded manifest’s digest can be compared against the current endpoint. Offline durable signature verification is a later extension requiring an actual signing-key lifecycle and revocation contract; do not display a cryptographic seal from a stored digest alone.

Withdrawal, expiry, revoked consent, deleted source or changed source rights fences the current public projection immediately. Profile cards, HTML, assets, downloads and verification must all stop serving content. Already copied material cannot be promised erased. Return a minimal `withdrawn`, `superseded` or `unavailable` tombstone with opaque publication ID, status generation and time; omit former title, people, business, reason and content. Unknown IDs return indistinguishable not-found responses. A known formerly public URL may expose only that a public release used to exist. The owner can request complete tombstoning where even that minimal retention lacks a purpose.

A correction creates a new immutable revision and a new manifest digest; the old version becomes superseded and no longer serves old content, including version-pinned URLs. The current URL shows the corrected page and a concise, approved correction note. A public history may show only explicitly released correction summaries, not a way to retrieve removed private material. Private retained audit records follow retention rights; “append-only evidence” is not an exemption from content deletion. Digest-only/public metadata retention must not expose secrets through guesses about low-entropy content.

## Anti-gaming and privacy defaults

One source intervention receives one canonical contribution record, even when surfaced on several profiles. Repeated Work copies, multiple evaluators controlled by the same principal, reciprocal praise, or an agent evaluating itself do not multiply confidence. Preserve evaluator relationships and evidence classes; omit counts and ranking so cheap activity has no automatic public advantage. Challenges are scoped to a claim and visible to its subject, with a correction path. A weak or disputed episode never becomes a hidden person-wide negative score.

Only explicitly released fields may enter future profile matching. Private assessments, meeting behavior, token consumption, protected characteristics, unshared business memberships and an employee’s employer-private memory do not become reputation features. Public reuse of a customer’s example by a vendor requires a separate release. No provider is asked to generate a person’s employability score in this slice.

## First implementation and proof

Implement in new `internal/business/contributions.go`, `publications.go`, their database tests, and the next numbered Business migration after the attempt slice. Root HTTP adapters live in `business_publication_http.go` and `public_work_http.go`; the private composer belongs to `public/business/`, and a small dedicated public case-study renderer handles `/w/:id`. Adapt useful legacy deterministic tests into the new SQL authority rather than wiring legacy controller maps. No legacy migration or second job engine is required.

Dependencies are concrete: one real Work result; a recorded human intervention and corresponding exact changed result; current SQL source and publication authority; authenticated subject identity; a safe markdown renderer; atomic publication/fencing; and the actual anonymous preview. A running public evaluator, marketplace, signature service, global profile search, remote-agent federation, video releases, and monetization are not prerequisites. Until intervention/outcome records exist, the composer must use the more limited evidence it has and cannot claim the complete Human Contribution Protocol proof.

Acceptance requires the real Builders case study and these decisive checks:

1. Anonymous phone, iPad and desktop readers see a compelling, accessible artifact and understand who did what, which claims are observed or assessed, and what is not shared. The release preview and public rendering are identical for selected content. No placeholders masquerade as evidence.
2. Person and employment profiles can feature the same source-bound work without duplicating contribution credit or exposing the business name, hidden source IDs, original private digest, screenshots, collaborators or private previews that were omitted.
3. Publish racing subject withdrawal, source correction or owner revocation cannot release stale fields. Same-key retries create one public revision; changed manifest bytes require current approvals. Agent/private-work credentials alone cannot publish.
4. Correction replaces the content and changes its verification digest; withdrawal removes every served projection/asset/download and survives process restart. A former employee can withdraw naming consent without gaining private Work access. No stale cache or version URL resurrects content.
5. The viewer verifies the public manifest, sees honest unknowns and can distinguish STRIDE provenance from an evaluator’s inference. The same failed/neutral intervention can be recorded privately without assigning general negative worth to its subject.

Release this slice behind explicit enablement only after the exact public-data lifecycle passes. Rollback disables the public read/write routes and preserves withdrawal fences; it must never restore an older projection-serving path. This document authorizes no public publication. The next action is to prove the private Work/intervention/outcome episode and build the composer against those real records.
