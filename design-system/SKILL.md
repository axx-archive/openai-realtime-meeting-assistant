---
name: bonfire-design
description: Use this skill to generate well-branded interfaces and assets for The Bonfire — a next-gen agentic video conference app — either for production or for throwaway prototypes, mocks, and decks. Contains essential design guidelines, colors, type, fonts, assets, and a pixel-faithful UI kit recreation of the meeting room.
user-invocable: true
---

Read the `README.md` file within this skill, and explore the other available files.

If creating visual artifacts (slides, mocks, throwaway prototypes, marketing pages, screens for a deck): copy assets out and create static HTML files for the user to view. Pull tokens from `colors_and_type.css`, lift logos from `assets/`, and either reuse the components in `ui_kits/bonfire-room/` directly (load the JSX components via `<script type="text/babel" src="…">`) or hand-port the patterns they demonstrate.

If working on production code, you can copy assets and read the rules here to become an expert in designing with this brand.

If the user invokes this skill without any other guidance, ask them what they want to build or design, ask some focused questions (audience, surface, fidelity, options to explore), and act as an expert designer who outputs HTML artifacts _or_ production code, depending on the need.

## Quick orientation

- **The product is the room.** Dark, warm, calm. One signature animation — three ember halos pulsing on a 2400ms clock.
- **Two surfaces** — Night (warm darks) for chrome, Ash / parchment for the board.
- **Three fonts** — Geist for everything UI, Geist Mono for system labels and tabular numbers, Instrument Serif italic *only* for empty states and ceremonial moments. All three load from Google Fonts via `colors_and_type.css`.
- **Tone of voice** — lowercase, second person, mechanical-poetic. "the room is listening", "memory starts when the room speaks", "nothing here yet".
- **No emoji. Ever.**
- **Color discipline** — ember (`#FF7A2B`) is the brand, used sparingly. Don't pile gradients on top of it. Don't substitute cool greys for the warm darks. One sanctioned cool accent: the active-speaker green `--speaker-accent` (`#34D399`), used only on the speaking video tile (border + `--glow-speaker-md`). Don't add a second.
- **Liquid glass is chrome-only** — glass tokens (`--glass-surface`, `--glass-surface-quiet`, `--glass-edge`, `--glass-highlight`) plus three blur tiers (`--blur-hero` for the join gate, `--blur-panel` for rails/bars/toasts, `--blur-overlay` for floating chrome and modal backdrops). Apply glass once per surface — panels, bars, toasts, modals — never per-item inside a scroller; items inside glass use flat translucent fills.
- **Sparks, not confetti.** And only on `Done`.

The complete rules — color, type, motion, hover/press, shadow, layout, content patterns — are in `README.md`. Read it before building.
