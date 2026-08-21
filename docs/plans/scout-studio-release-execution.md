# Scout Studio And Continuity Release - Execution Ledger

Goal and source pointers: active Codex goal for Scout reply-context, durable public work, Deck Studio, production Like A Farmer workshop, mobile TestFlight, and repository synchronization.
Current phase: combined web and Mobile Build 69 source is green at `d96fc89b`; record the release checkpoint, push the exact reviewed tree, deploy it, then rerun the production deck and submit Mobile Build 69.

## Invariants

- A public Scout promise is backed by one durable root-channel work receipt before execution, with truthful queued, running, checkpoint, complete, or needs-attention state.
- Reply-thread and attachment context is viewer-authorized, revision-pinned, branch-scoped, and reauthorized before provider use; deleted, revoked, or cross-branch bodies fail closed.
- Stage and deck artifacts never invent missing source facts or claim a save, post, job, or approval without the matching durable receipt.
- Generated deck editing is structured, version-CAS persisted, script-free, full-window, and reopen/present verified.
- Active video calls survive web surface navigation and end only on an actual call end or sign-out.
- Mobile navigation is not changed by web-only Content Studio work.
- Production deploys use an exact clean commit archive and the retained release verifier; TestFlight uses the exact EAS build ID, never `--latest`.
- Unrelated dirty work and the original mobile source receipt are preserved until their integration parity is proved.

## Wave Map

| Wave | Outcome | Dependencies | Gate / rollback | Status |
| --- | --- | --- | --- | --- |
| W1 | Reply-root context, scheduled public work, outline-to-deck flow, structured Deck Studio | Existing Scout/chat/goal authority contracts | Focused normal and race tests; independent critic | Complete in `5fe9e469` |
| W2 | Durable channel checkpoint choices on the existing root card | W1 | Focused normal/race tests, production exact-release verifier, live two-checkpoint journey | Complete and live in `25bf7248` |
| W3 | Source-complete Packaging Studio stages and beautiful, navigable activity drawers | W2 and live blocked-run evidence | Production-path source/ACL tests; rendered desktop/mobile drawer journey; critic | Complete in combined release tree; final gate green |
| W4 | Web-only internal KINO Content Studio and persistent call picture-in-picture | W3 shared `index.html` integration | Cross-origin isolation, navigation/call lifecycle browser tests, rendered QA | Complete in combined release tree; final gate green |
| W5 | Slick web chat: stable scroll anchoring and fast optimistic reactions/comments | W4 shared navigation/chat shell | Instrumented interaction journey, no scroll jump, rollback-safe socket reconciliation | Complete in combined release tree; integration gate green |
| W6 | Rerun and finish the Like A Farmer deck in production | W3-W5 deployed | Exact source present in stages; deck save/reopen/present; edited imagery/layout; channel delivery receipts | Pending |
| W7 | Integrate Mobile Build 69 and publish exact SHA to TestFlight | Final web release SHA | EAS build identity, Apple processing, intended tester groups; no claim of physical-device proof without evidence | Pending |
| W8 | Synchronize GitHub/local refs and prune safe temporary state | W6-W7 | No lost dirty work; main parity; clean retained worktrees | Pending |

## Current Wave

- Root owner: commit the integrated, critic-approved web delta; combine it with Mobile Build 69; push the exact main SHA; execute the receipted VPS release and live browser QA.
- Production owner: rerun the Like A Farmer request against the exact authorized reply/PDF snapshot, answer only genuine checkpoints, then edit and verify the generated deck.
- Mobile owner: build and submit only the final combined main SHA as Build 69, then verify Apple processing and intended groups.

## Completed Evidence

- `25bf7248a82c06c4a6e55993f34ade90af0a4936` is on `axx/main` and serves production generation 137 as `verified-local-unsigned`; the prior release is retained.
- Production #Like A Farmer checkpoint choices were rendered and accepted on the existing root card.
- Live run exposed the next defect: the authorized four-page `Like_A_Farmer_Audience_Growth_Media_Strategy.pdf` contains concrete recommendations and figures, while stage prompts received only the short objective and prior-stage text. The run truthfully ended needs-attention instead of fabricating a deck.
- Live drawer density evidence: Red-team 69,055 chars / 385 nonblank lines / 102 tabular rows; Identity 26,131 / 539 / 70; competing architects 65,375 / 1,094 / 79; judges 20,322 / 203 / 29; Write 19,256 / 482 / 26. Wide Write tables visibly collapse at desktop drawer width.
- Mobile candidate is committed locally as `275946a26aefd22bc520bb679802b225258ff19b` on `codex/mobile-stability-build68-release`; 587 mobile tests, TypeScript, Expo install check, Expo Doctor 21/21, clean prebuild, Build 69 identity, iOS export, and independent critic are green.
- Build 68 was already consumed by an older binary, so the candidate is correctly Build 69; current EAS history showed 69 unused at preflight.
- W5 web chat now keeps an exact visible element-plus-pixel anchor in both the main feed and reply rail; ordinary edits, reactions, reply/status projections, new messages, and HTTP/socket echoes use keyed narrow reconciliation while complex records retain an anchor-safe bounded fallback.
- Reaction taps paint optimistic state before the request, coalesce per message+emoji to the latest intent, retain server authority, and roll back visibly on failure. The focused Chromium journey proved one request for a rapid true-false-true tap burst against a 450 ms response, rollback after a 500, at most 1 px main-feed and reply-rail anchor drift, stable focus/drafts/selections, near-bottom follow, older-reader stability, own-comment tail pinning, and the existing 200-node long-channel cap.
- W5 focused gate: `NODE_PATH=/Users/ajhart/meetingassist/node_modules go test . -run '^(TestWebChatSportsCar.*|TestChatColdIndex.*|TestDesktopChatSendRenderIsOneScrollStableTransaction|TestDesktopChatInteractionTargetsAndComposerStates|TestDesktopReplyMutationsDoNotRebuildMainFeed|TestDesktopThreadRepliesStayDiscoverableAndAvatarLed|TestDesktopLongConversationStaysAMessageAndUsesTheThreadInspector)$' -count=1` passed; `git diff --check` passed.
- W3 source selection binds the exact provider-facing context bytes, reply topology, and attachment identity/revision/content; long-thread edit/delete/revoke, label/kind mutation, legacy upgrade, and capture/verify race tests fail closed before provider admission. Operational scorer/source failures are terminal non-judgments and cannot revise, force-accept, or surface a misleading proceed option.
- W3 drawer gate includes the five production artifact shapes plus a custom 121 KB, 102-row, 1,000-line hostile artifact at desktop and 390px mobile widths.
- W4 Content Studio uses the canonical `https://kino.grok.me`, background inerting, cross-origin focus sentinels, fallback/external escape, and a web-only rail item. W4 PiP hit-tests above Stage/KINO, remains operable as a truthful composite, docks away from actions, routes Return to `/video`, and disappears only on canonical hangup.
- Final independent critic verdict: PASS after critical normal/race, PiP/Studio/stage, 230-event chat stress, and canonical cold-index browser suites.
- Combined release tree `d96fc89bdbb3372bfa57e2cdbbcc0f5ea288f5c8` contains web commit `712f2dce` plus Mobile Build 69 commit `d96fc89b`; the exact combined focused Go/browser gate passed in 19.119s, all 587 mobile tests passed, TypeScript passed, and `git diff --check` was clean. A clean `npm ci` reproduced the known eight high transitive Expo/Metro advisories without changing the lockfile.

## Pending Dependencies

- W3-W5 and Mobile Build 69 are green in the exact combined release tree; GitHub, VPS, production deck, EAS, Apple, and cleanup proof remain pending.
- TestFlight completion depends on EAS/Apple processing and tester-group state; authenticated physical-device acceptance remains a separately observable gate.

## Operations And Authority Queue

- Authorized: commit/push the reviewed integrated source to `axx/main`; deploy the exact web release to the DigitalOcean VPS; rerun and edit the production deck; build and submit the exact Mobile Build 69 to TestFlight.
- Not yet executed: final ledger checkpoint commit, push/deploy, production rerun, EAS Build 69, Apple submission/group verification, repository cleanup.
- Rollback: retain production release `25bf7248` and its images until the replacement passes the retained verifier and live canaries.

## Risks And Decisions

- Native document editing is deliberately the next bridge, not part of this release, unless it can reuse the structured Deck Studio persistence contract without widening the current release gate.
- `www.kino.grok.me` has no DNS record; the working canonical embed target is `https://kino.grok.me` (HTTP 200, no current frame restriction). The UI label is exactly `Content Studio` with no punctuation.
- The baseline full Go suite has seven known frontend static failures reproduced on clean `5fe9e469`; new changes must not add failures and focused production-path/browser gates remain mandatory.
- Mobile has eight high transitive Expo/Metro `image-size` audit advisories with only a breaking forced downgrade offered; no unsafe forced downgrade is authorized.

## Resume Here

1. Re-read this ledger, then verify the active goal, `git status`, `git diff --check`, and `axx/main` before trusting any status above.
2. Commit this release checkpoint, fetch and compare `axx/main`, and push only if it remains the expected ancestor; verify local/remote exact parity.
3. Preserve the combined source receipt: web `712f2dce`, mobile `d96fc89b`, and the final ledger-only release commit.
4. Deploy and verify the exact VPS release, rerun the production deck workflow, and start the exact-SHA EAS Build 69 in parallel.
5. Verify the deck save/reopen/present/channel receipts and TestFlight processing/groups before cleaning preserved worktrees and stale refs.
