# Visual upgrade thesis — "Quiet, not faint"

Date: 2026-07-25. Scope: BonfireOS web client (`index.html`). Founder objective, verbatim:
_"do an end-to-end visual audit/upgrade, I want this thing to look gorgeous and be unmatched in how sleek and easy to use it is"_

This document is the rubric. Every finding and every change is ranked against it.

## The diagnosis

The restraint is **right**. It has tipped from *quiet* into *faint and unfinished*.

Every symptom observed in the live app is a variant of one root cause:

> **The design expresses hierarchy by removing emphasis rather than by structuring it.**

Everything secondary received the same treatment — monospace at very low contrast — so
nothing is actually prioritized. When all subordinate content is equally faint, the eye
has nowhere to land, and the surface reads as a draft rather than as a considered whole.
"Sleek" is being pursued by subtraction alone, and subtraction has passed its optimum.

## Principles

1. **Hierarchy by structure, not by fading.** Rank with size, weight, and space. Secondary
   text must remain readable; tertiary text gets *smaller*, not *paler to the point of
   illegibility*.
2. **Mono means machine.** Monospace is reserved for machine facts — timestamps, counts,
   IDs, tags, status. Human and instructional prose is sans. Scarcity makes mono
   meaningful instead of ambient. _(Founder-ratified 2026-07-25.)_
3. **Ember is earned.** Color appears only at moments of consequence — live, alert,
   shipped. The scarcity is the point, but it must actually *occur*, or the vocabulary is
   dead and the product reads as colorless rather than disciplined.
   _(Existing doctrine upheld, founder-ratified 2026-07-25.)_
4. **Empty is an invitation, not a void.** Every empty state does exactly one job: say what
   this surface is, and offer the single next action. It must never repeat copy already
   visible on the same screen.
5. **Compose to the content.** No hollow centers. Related content is grouped; content is
   not pinned to opposite edges with dead space between.
6. **Both themes are first-class.** Every state must be legible in light and dark. Dark
   currently outclasses light; parity is required, not optional.

## Ranking rule

A change earns its place only if it serves a principle above **and** survives this test:

> Would a discerning user notice this within ten seconds of landing, and would its absence
> make the product feel cheaper?

Nitpicks that fail that test are recorded but not shipped in this wave.

## Non-goals

- Do **not** splash accent color to manufacture visual interest (violates #3).
- Do **not** discard the monospace signature wholesale (violates #2; it is brand voice).
- Do **not** restructure information architecture or rename surfaces. This is a visual and
  ease-of-use pass, not a product redesign.
