# STRIDE Private Riff execution ledger

Updated: 2026-08-15

## Goal

Ship a source-bound private conversation from any authorized public channel or
thread, keep its context stable until the user explicitly refreshes it, show
truthful semantic activity without exposing hidden chain-of-thought, and let
the user selectively return completed text to the source channel with clear
authorship and provenance on web and native iOS.

## Frozen product and authority contract

- The user action is **Riff privately**. It creates a normal owner-only chat
  thread; STRIDE does not create a second chat system.
- The server, never the client, resolves and hashes the exact authorized public
  source window through one message. The durable binding stores body-free
  source, window, Brain, agent, and revision receipts.
- A riff never follows new channel messages implicitly. The UI reports the
  pending count and **Update context** mints a new immutable revision.
- Private riff turns remain excluded from company Brain, shared recall, and
  public search. Each model turn reauthorizes the source checkpoint and fails
  closed if its source was edited, deleted, archived, or no longer readable.
- Public channels remain human-first. A completed private answer can return by
  either **Share Scout's answer** (agent-authored, visibly “Shared by <person>
  from a private riff”) or **Use in my message** (editable user draft). Only
  server-issued paragraph selections are accepted and publication is
  reauthorized at action time.
- Activity exposes semantic state, elapsed time, evidence/source count,
  citations, uncertainty, and a short outcome rationale. It never exposes raw
  chain-of-thought, hidden prompts, provider/model/effort labels, tool traces,
  internal IDs, or fabricated percent progress.
- The server-owned five-way conversational router remains authoritative. The
  legacy public keyword fallback may not override a conversational or negated
  intent.

## Waves

| Wave | State | Acceptance boundary |
|---|---|---|
| 0 — rebaseline | complete | Local HEAD/main/dirty tree, live VPS/rollback, Expo 57, EAS/ASC Build 63 and Build 64 next number read back; `stride-site/` preserved. |
| 1 — contract and server | complete | Durable riff binding, create/refresh/preview/publish endpoints, per-turn source reauthorization, safe activity receipt, routing-negation repair, focused normal/race tests. |
| 2 — web | complete | Public-channel entry, private checkpoint strip, explicit refresh, semantic activity/collapse, paragraph share/draft flow, responsive and accessibility evidence. |
| 3 — native iOS | complete | Existing Thread stack reuse, 44pt entry, checkpoint sheet, refresh, share/draft flow, Expo 57-compatible tests/typecheck; Build 64 source awaits the integrated release commit. |
| 4 — integrated gate | complete | Focused normal/race suites, vet/diff/dependency checks, full mobile suite/export, rendered desktop/responsive QA, independent critic PASS. The all-package Go sweep's only remaining result was an unrelated rendered-browser timeout under load; that exact harness passed alone in 4.28s. |
| 5 — web release | in progress | Reviewed commit, exact clean archive, retained-tool activation, `verified-local-unsigned`, ledger/image/public SHA agreement, rollback retained. |
| 6 — TestFlight | pending | Exact Build 64 EAS artifact and submission, Apple upload complete/testing/unexpired, Team (Expo) and seven-person Bonfire assignment readback. |

## Baseline and evidence

- Local branch: `codex/country-golf-stride`; baseline
  `c5a0d05a074619c6c2274e9a240e3ee90cf721f9`, equal to `axx/main` at
  rebaseline. The only pre-existing dirty path is untracked `stride-site/`.
- Live VPS ledger generation 85 serves
  `1e64bcaf522312ba13ef1967dbd1eb4db27321b4`, with
  `8e8a73aa70aad258dc023e9e1baaa84e2327fea7` retained as rollback. The
  retained verifier returns `verified-local-unsigned`; health/readiness and
  image identities agree; the operation lock is absent.
- iOS Build 63 is Testing, unexpired, present in Team (Expo) and the
  seven-person Bonfire group. No Build 64 exists. `autoIncrement` is false.
- Live reference behavior observed before implementation: Grok Expert Mode
  exposes semantic phases and elapsed time, then collapses the process; it
  does not expose raw chain-of-thought. The current STRIDE public keyword
  fallback can misread negated “research” wording and is included in Wave 1.
- Focused server tests and their race variants pass. Go vet passes. Mobile
  Expo Doctor 21/21, typecheck, iOS export, and the 566-test suite pass. The
  independent critic's final review is PASS after
  closing early artifact-follow-up containment, per-answer checkpoint
  provenance, responsive web, draft-destination, checkpoint cleanup, and
  negation seams. Build 64 is pinned in the resolved production app config.

## Risks and rollback

- Source drift or membership loss: refuse turn/refresh/publication; retain the
  private transcript but replace source details with an unavailable state.
- Duplicate create, refresh, or publish: operation-derived identity and exact
  body/source digest replay; conflicting reuse fails closed.
- Private leakage: source bodies exist only in authorized model input and
  owner responses; thread indexes remain body-free; only selected paragraphs
  reach the public destination.
- UI regression: reuse existing thread, FlashList, composer, websocket, and
  native modal primitives; no token-delta protocol in this release.
- Web release rollback: retained generation-85 bundle and immutable images.
- Mobile rollback: Build 63 remains assigned and available until Build 64 is
  independently accepted.

## Exact resume point

Finish Wave 5 from the exact reviewed commit: stage only the intended paths,
excluding `stride-site/`; push the full SHA to `axx/main`; prepare/build/activate
only its clean archive; require the retained verifier, ledger, image IDs, and
public probes to agree. Then build and submit iOS Build 64 from that same SHA.
