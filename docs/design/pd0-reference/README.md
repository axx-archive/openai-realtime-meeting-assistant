# STRIDE PD0 executable reference harness

This isolated harness is **PD0 reference evidence**, not product implementation,
native-device acceptance, publication authority, or activation proof. It uses one
deterministic fictional fixture and makes no live application, provider, or
production-data calls. Every PN1–PN5, W5, W6, semantic-provider, reranker, and
moderation gate is explicitly false.

## Run locally

From the repository root:

```sh
node docs/design/pd0-reference/validate.mjs
python3 -m http.server 18181 --directory docs/design/pd0-reference
```

Then open `http://127.0.0.1:18181/`. The HTTP server is necessary because the
browser intentionally loads `fixture.json` as a separate closed artifact and
does not embed a fallback.

## What the harness proves

- J1–J13 are navigable in desktop, iPhone, and iPad reference compositions.
- The shell uses the exact closed information architecture—Home, Work, Network,
  Work Search, and You—and each journey executes its own destination-specific
  screens, actions, transitions, and terminal outcome instead of sharing a
  generic step script.
- The platform switch changes shell density and information composition without
  changing the journey's semantic outcome.
- HumanMessage, AgentContribution, and SystemEvent remain visibly distinct.
- Governed Work Record, workspace, Work Object, Workstream, four-profile,
  Work Search/contact, agent, settings/public-presence, moderation, and consensus
  semantics are represented without private bodies or production identities.
- The exact state vocabulary is interactive: `idle`, `loading`, `empty`,
  `ready`, `feature_off`, `unavailable`, `blocked_dependency`,
  `pending_approval`, `offline`, `retrying`, `stale`, `unauthorized`, `revoked`,
  `quarantined`, `corrected`, `purge_pending`, `purge_failed`, `restored`, and
  `terminal`.
- Every journey has three or four deterministic steps, Back and safe Resume
  behavior, a terminal state, and an in-memory Restart. Failure states hold the
  current step and cannot replay an effect.
- The representative and concurrent-state selectors feed one deterministic
  resolver using the complete frozen precedence sequence. False gated parents
  still force `feature_off` before this local state demonstration is considered.
- Public/network controls remain opaque and disabled under false parent gates.
  Every requested state on a gated journey resolves to `feature_off`; no child
  projection is mounted by default. A separate, off-by-default layout-preview
  toggle exposes component names and field shapes only. It has its own explicit
  local Start/Advance/Back/interruption/Resume/terminal/Restart state machine;
  its warning is persistent, example values are withheld, and it never changes
  production state, enables a production action, or claims current authority.
- Fixture components use exactly the 23-name closed semantic catalog. The
  validator rejects unknown types, fields and categorical values, binds every
  non-enum field to an exact fictional allowlist, freezes each journey's ordered
  component roster/cardinality, and runs mutation negatives for markup, private
  contact-like bodies, safe-token body/secret values, forbidden body keys, and the
  prohibited human/agent/system, artifact/outcome, record/profile,
  workspace/organization, object/artifact, stream/search and
  moderation/consensus collapses. Collapse negatives substitute independently
  schema-valid target components so the journey contract—not a field mismatch—
  rejects the semantic substitution.
- The 23 component types also resolve through a closed one-to-one semantic
  family registry and 23 distinct rendered structures. Suggested Work, public
  Work Objects, artifacts, outcomes, evidence, the four profile boundaries,
  moderation, and consensus cannot collapse into one generic card renderer;
  source-mutation negatives prove those structural boundaries remain distinct.
- `fixture.json` embeds a closed manifest of the authoritative PD0 parity audit,
  semantic-system contract, reference-flow contract, primary-source research,
  and PD0/PI0 checkpoint. The validator recomputes every SHA-256, verifies the
  checkpoint's clean independent PD0 gate, and rejects root, state, journey,
  composition, step, journey-contract, destination, or component drift.
- Each step exposes `aria-current="step"`. Because every transition replaces the
  action dock, focus moves to a stable live step-status element that announces
  Start, Advance, Back, Resume, terminal and Restart outcomes. Journey,
  platform, representative-state, concurrent-state, and preview changes also
  replace that status so an earlier journey announcement cannot remain stale.
- Every fixture-derived title, label, field value, composition, state, gate, and
  journey string is HTML-escaped before it reaches a rendered markup template.
- The iPhone expression exposes hard states, gate status, platform composition,
  and fixture boundaries in an accessible disclosure rather than hiding the
  desktop inspector's capability.
- Keyboard traversal, focus visibility, pointer/touch hit areas, responsive
  collapse, and reduced-motion behavior are encoded in the reference.
- Colors, spacing, radii, elevation, glass material, motion duration, and motion
  curves are consumed through governed semantic variables/scales. The validator
  binds the exact governed root token map and values, while mutation negatives
  reject unknown tokens, value drift, direct literals, positional/inset values,
  gradient stops, and ad hoc shadows or material effects outside that
  declaration.

## What it does not prove

- No production feature, router, server authority, provider, publication,
  cohort, legal/privacy approval, or feature switch is installed or enabled.
- The iPhone/iPad modes are browser-rendered composition references, not native
  React Native code, simulator evidence, VoiceOver acceptance, Dynamic Type
  acceptance, Split View/Stage Manager proof, or real-device acceptance.
- Browser rendering is not WCAG, localization, performance-budget, Web Vitals,
  screen-reader, cache/purge, restart, or production security acceptance.
- The fixture does not mint actions, search people, send contact requests, vote,
  moderate content, publish work, or persist state.

## Files

- `fixture.json` — digest-bound deterministic fictional IA, journey contracts,
  platform compositions, hard states, and false gates.
- `index.html` — semantic shell and accessible control structure.
- `styles.css` — code-native desktop/iPhone/iPad reference expressions.
- `app.js` — local interaction and fail-closed rendering.
- `validate.mjs` — dependency-free fixture and source-contract validator.
