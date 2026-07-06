# StationTenn live drive-through — good/bad/ugly (draft, updating as the run completes)

Run: packaging_studio on thebonfire.xyz, public thread "StationTenn", agent driving AJ's account (disclosed). Goal artifact os-artifact-workflow-1783306661952448772. Bar: the /packaging skill's LONG LIGHT kit (gated 9.55 after one rejection at 8.65).

## THE GOOD (real, not grading on a curve)
1. **The pipeline is real and fast.** intake → red-team (47K ledger) → identity disclosed-skip → 3 rival architects (34K) → judges (17K) → choice → write → gate → voice, with the four checkpoints parking exactly as designed. Red-team through judges took ~10 minutes wall clock on production Fable.
2. **Gate quality matches the skill.** OS gate accepted at 9.2 round one ("every round-1 objection is answered inside the document before it can be asked") vs the skill's first build rejected at 8.65. The closed-loop persona machinery works.
3. **Judge quality is genuinely good.** Winner "You Can't License an Afternoon" chosen 3-of-4 with a sharp rationale (the only spine answering "what can't Ryman buy?" with an idea); the synthesis surfaced architect CONVERGENCE (both invented an audit-forward rights slide) as settled signal — that's judge-panel craft the skill would be proud of.
4. **Graceful degradation, disclosed.** One architect died on provider overload; the synthesis said so and covered the space with 6 spines from 2 architects. Identity skip was disclosed, not silent.
5. **The send-back teeth work in production.** founder_pass revise re-queued WRITE with notes as protected law, cascade-invalidated gate+voice, re-ran both, re-parked. Protected lines survived the redo verbatim.
6. **Constraint fidelity.** No "Vonn" typo, Paramount stayed at the honesty floor, founder verbatims present in draft + script, CONCEPT RENDER law carried into the deck copy's production notes.

## THE BAD (shipped fixes during the run)
1. **Boot TDZ crash** — the deployed frontend was half-dead (palette unopenable, meetingRecord/scoutCaptionTimer cascades) from ONE `let` declared 19K lines after its boot-pass use (probeRenderSidecar). Fixed + regression guard test (frontend_boot_tdz_test.go) + hotfix deployed (61367d4).
2. **Process ids unlaunchable from chat** — palette handoff armed packaging_studio but the chat launch path validated via toolByID only → "unknown tool template". Fixed: process ids route to launchGoalThread (9f5c0af), test-pinned.

## THE UGLY (findings → fixes in flight)
1. **[FOUNDER-CONFIRMED, #1] Deliverables invisible at the decision moment.** founder_pass says "read the draft and decide" — no link to the draft anywhere in the thread. Only an API-reading agent could complete the checkpoint. → narration-thinker spec: checkpoint cards in-thread with artifact chips + inline choices.
2. **[FOUNDER-CONFIRMED] No live push.** Stage completions, parks, new messages require page refresh. → same spec: consume the existing office websocket in the chat/thread view.
3. **[FOUNDER-CONFIRMED] Scroll wedges app-wide.** Chat history not scrollable from within a thread; Intelligence page similar; checkpoint card buried in an unscrollable nested pane. → scroll-fixer agent on it.
4. **compete_choice options array EMPTY** — processCheckpointOptionsFromText extracted nothing from the judges' synthesis; the card would render zero buttons. Free-text choice saved the run. → fallback options + extraction fix.
5. **"open in library" routes to the Intelligence page**, not the artifact detail; the detail then needs a sub-scrolled pane. Checkpoint interaction is effectively 3 pages from where the process talks to you.
6. **Copy-law sweep doesn't cover studio contracts** — 17 em dashes reached the gated draft (law sweep only covers one_pager_v1/deck_outline_v1/update_memo_v1/package_binder_v1; deck_copy_v1/presenter_script_v1 not marked ClientFacing). The founder send-back caught it; the machine should have. NOTE: redo's remaining dashes are markdown scaffolding (slide headers) — the sweep needs a scaffolding-vs-copy distinction.
7. **presenter_script_v1 artifact missing its processStage stamp** (metadata gap — filters by stage miss it).
8. Goal card shows "tool · research" for a process run and progress % regressed visually when parked (68% at every park) — cosmetic but confusing.

## THE UGLY, continued (SHIP stage, found at ship_approval park)
9. **The deck was not a deck.** ship_deck completed with a 4.4K markdown DESCRIPTION of the deck; the compiler stamped it html_deck blindly; render failed on it; jury starved (disclosed skip — honest at least). No mechanical guard demanded the artifact itself. → FIXED: processStageLawSweep (packaging_deck_v1 must start <!doctype html and close </html>, zero model cost, revise short-circuit), compiler refuses to mistype, ship_approval gained "send back — rebuild the deck" (revise→ship_deck).
10. **Chromium SIGTRAP in the render container** — Docker's default seccomp profile kills chromium 150 (trace/breakpoint trap; the dbus errors are noise). → FIX staged: security_opt seccomp:unconfined on render-runner (blast radius already bounded: egress-less network). Pending probe confirmation.
11. presenter_script_v1 artifact filed without its processStage stamp (stage filters miss it).
12. Shared-Playwright hazard (ops, not product): a subagent navigated the session browser mid-drive to localhost — agents must never touch the shared browser; noted for future agent briefs.

## FIXES SHIPPED DURING THE RUN (deployed)
- Boot TDZ hotfix + guard test (61367d4)
- Process ids launchable from chat (9f5c0af)

## FIXES BUILT DURING THE RUN (pending deploy at next safe window)
- processStageLawSweep + honest compile typing + ship_approval send-back (+pins)
- seccomp:unconfined on render-runner (pending probe)
- App-wide scroll repair (scroll-fixer agent)
- In-thread goal cards + stage-deliverable chips + park cards + live push + options-extraction fix (thread-cards agent, from narration-thinker's verified spec)

## RUN TWO (on the repaired pipeline, new UX) — verified live
- **The founder's #1 fix works in production, in pixels**: stage chips streamed into the public thread as each stage completed (red-team 5-seat, identity, architects 3-seat, judges 4-seat, write, voice — each a tappable chip); checkpoint parks arrived as full goalcards at the thread bottom with REAL BUTTONS; intake, compete choice, and founder pass were all answered by button tap; write→gate→voice chips + the founder-pass park arrived into an open, untouched view — NO REFRESH.
- **Options extraction fix verified**: compete card rendered 3 real options from the judges' artifact (cultural-moment / franchise-playbook / founder-conviction).
- **Quality: run two beat run one.** Judges unanimous 4/4 for "The Timetable" (franchise-playbook) — the departure board converts nine-properties-at-four-reality-levels from weakest fact into the design system (BOARDING / AT THE PLATFORM / IN NEGOTIATION / UNCONFIRMED tiers), self-verifying under diligence, printed 24-months-without-Paramount downside. Gate: accept 9.5 round one (skill baseline: rejected 8.65 then 9.55; run one: 9.2). Constraint fidelity: Theo Von guarded in production-law text, verbatim founder lines present, em dashes only in tier-label scaffolding.
- **Misclick finding (mine, disclosed in-thread)**: tapping via `.goalcard__choice first()` hit the HELD run-one card's "approve the ship" (a proceed action resumes a held goal by design) and un-held it. Design follow-up: confirm-step on proceed-after-hold; visually demote non-latest cards.
- **Stale-tab socket**: after heavy SPA navigation my tab's websocket went stale (view froze while the record grew); a reload healed it. Follow-up: socket reconnect/backfill check.
- ship_deck generation is LONG (~25+ min; 32K single-file deck at effort high, likely one 15m-timeout retry). Follow-up: raise the deliverable child budget/timeout for packaging_deck_v1 or split deck generation into sections.

## STILL TO OBSERVE
- ship_deck (running, ~7min+), ship_compile five-artifact filing, render sidecar PDF flatten on real deck, slide_jury first live vision run, ship_approval hold/proceed, Deal Room gallery, share links, survey chips, deck viewer + Present, Export PDF button.
- Baseline comparison once the deck exists.

## THE VERDICT (independent judge, both packages read in full)

Baseline (terminal /packaging skill) wins 5 of 6 artifact categories; the OS wins process economics overwhelmingly (~35 min + 4 taps vs a founder-attended day, ~10x output per human-minute). **The OS did not meet the founder's bar this run.** The factory is right; the last mile isn't sellable yet.

Where the OS already competes or wins: red-team and compete thinking ("The Timetable" unanimous; "We print our own status because you'll check it anyway" and "Nobody licenses a station. You build one, and then you run it on time" called the two best new sentences of the day by either pipeline), checkpoint provenance (every human decision recorded with choice + decider), and the raw diligence material (Ryman business-affairs teeth, MCR sanctioning paper, post-ELVIS-Act instincts).

Where it lost: the deck (~3x craft gap — no images under CONCEPT RENDER labels, one template, no print CSS so no clean flatten, jury's 5.7 fix unapplied, "## Orchestrator evidence" appended AFTER </html> in a client-facing file); The Talk shipped as the agent's process report instead of the presenter sheet; the 41K companion is 60% duplication and lacks the claims table / spoken Q&A / three-altitude AI memo; and the 9.5 gate MISSED a factual inflation (six incubating properties listed under "Boarding — operating and dated" — the deck's own honesty claim weaponized against it) plus the wrong red-team seating (VC/LP personas for a talent-rep audience → no handshake page).

## THE NEXT WAVE (the judge's five fixes, adopted as backlog)

1. **Deliverable-contract check at ship_compile**: the artifact body must BE the deliverable — never the agent's report, never with orchestrator evidence appended (extend the law-sweep class; strip evidence footers from client-facing bodies).
2. **Image pipeline or honest omission**: never a CONCEPT RENDER label over nothing (wire imagery_board/gpt-image-2 into SHIP, or drop the label).
3. **Jury completeness + one revision round**: render ALL pages (the 1-page render needs the print-CSS fix), run one bounded revision on the jury's consensus fixes before ship.
4. **Claims-vs-source gate check**: the gate must audit facts against intake truths, not grade prose (the boarding-tier inflation class).
5. **Audience-seated red teams**: personas from the brief's stated audience, not a default investor quartet (would have surfaced the handshake page).

Also queued from the drive: confirm-step on proceed-after-hold; presenter script ships as The Talk one-sheet (parse from deck, paper-kit render); ship_deck generation budget (25+ min, one timeout retry); socket reconnect after long SPA sessions; agents must never navigate the shared Playwright browser.
