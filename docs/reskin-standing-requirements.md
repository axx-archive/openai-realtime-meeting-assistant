# Reskin — standing requirements (every wave must honor these)

Founder directives that apply across ALL surfaces, in addition to the per-surface inventory (docs/reskin-inventory.md) and the design laws (.ds-ref/readme.md).

## 1. Glass & ink v2 core laws (recap)
- Monochrome `--accent` (ink light / white dark): primary buttons, brand mark, unread badges, "you" bubbles.
- Color = STATE + one earned exception: `--live #30D158` (live/speaking/presence); `--ember #FF6B4A` coral = the sanctioned IGNITION accent, EARNED never ambient (Run/launch, a running goal node, live pulse, deck emphasis). Semantic `--danger/--warn/--info` at *-soft. No decorative hue, no gradients, no third accent.
- Fonts: sans = **Google Sans Flex**; mono = **Geist Mono**. System text (labels, statuses, timestamps, pills, log lines, counts) = lowercase Geist Mono; person text = Google Sans Flex. Numbers mono.
- Glass floating chrome: bg var(--glass-chrome|panel) + backdrop-filter var(--glass-blur) + var(--glass-border) + box-shadow var(--glass-highlight),var(--glass-shadow). Radii 12/16/22/28/full. Cards: surface-2 + line-1 + r16, no rest shadow. Avatars: monochrome initials on surface-3. Motion: press .97, breathe 2400ms only-while-listening. No emoji.

## 2. Copy & formatting audit (founder: "superfluous text / weird line breaks")
On EVERY surface, hunt and fix:
- **Superfluous descriptions** that don't belong in a shipped product — dev-y explainer captions, over-helpful "here's how this works" subtitles, redundant hints. Keep the calm koan voice, but cut anything that reads like scaffolding/onboarding filler on a mature product.
- **Bad line-breaks / wrapping / truncation** — text that wraps awkwardly (orphan words like "…start↵one"), mid-word ellipsis truncation, labels that overflow their container, unformatted runs. Ensure clamps are intentional (2-line) and containers fit their text.
- Verbose sidebar/footer helper text → tighten.

## 3. Verify against RICH mock data (founder: chat cards, multi-participant)
Chat especially must be refined against a POPULATED conversation, on desktop AND mobile:
- Multiple participants talking to each other (not just you + Scout).
- People @-tagging each other AND @scout.
- Every card type visible: deliverable cards, question cards, agent letters, goal/run cards, plain bubbles, inline image.
Seed this mock data into the reskin instance before refining chat; screenshot desktop + mobile.

## 4. Digests in the proper aesthetic (founder)
Morning Brief, Portfolio Health, and any Scout-rendered brief/summary/digest content (office_brief.go) — and any email digests (resend.go) — must render in Google Sans Flex + Geist Mono + glass & ink. No plain/unstyled text, no markdown leak, no foreign serif fallback.
