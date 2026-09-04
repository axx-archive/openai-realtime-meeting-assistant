# STRIDE 3 — one design language

Status: canonical palette accepted by root on 2026-09-04; remaining layout/navigation contract proposed for root integration. Source audit and design only; no application migration or new rendered acceptance is implied. Applies to the business-kernel worktree, the existing web/iOS application, and the separately versioned marketing site. The founder's current direction supersedes inherited Bonfire/Glass & Ink visual prescriptions: agents build and run businesses; people choose their participation and authority ceiling.

## Decision and visual idea

Keep STRIDE and its existing wordmark. Build one identity around cobalt, ink, generous neutral space, and precise typography. The memorable composition is **a company in motion, with its work in the foreground**: a clear mission, one consequential next move, and a substantial result canvas. People, agents, conversation and evidence support that canvas. Avoid a dashboard of identical rounded metrics, permanent glowing agent decorations, or a mandatory approval inbox.

Use light surfaces for focused daytime work and equally complete ink surfaces for dark appearance. Calls have a fixed dark stage. Marketing can use large ink/cobalt fields and expressive scale; the app uses the same grammar at working scale. The distinctive brand comes from composition, optical typography, restrained cobalt and an inspectable business story, not from making every panel blue or every screen black.

This chooses the current Business/marketing direction over the putty/orange application palette. Retain the useful existing type families, wordmark geometry, semantic media controls, adaptive native layout and accessibility behavior. Retire putty, ember-as-brand, ambient glass and duplicate navigation systems through explicit migration. Existing constraints that protect media continuity and authorization remain in force.

## What the audit actually found

| Surface/source | Current fact | Consequence |
| --- | --- | --- |
| `index.html` root tokens, dark overrides and `.pd1-primary-nav` | Google Sans Flex/Geist Mono; putty `--paper-50:#DDD4C6`; ember `#FF5A19`; deeply mixed local rules. Labels Home / Rooms / Conversations / Work / Drive. | Keep fonts and routing contracts; migrate theme and navigation deliberately. A root variable replacement alone cannot cover inline/local styles. |
| `public/stride-operating.css` | Editorial Home and Work evidence layout inherits legacy `--surface-*`, `--text-*`, `--ember`; 16px Work cards, 22px composer, 10–13px secondary labels. | Good narrative structure, incompatible palette and some undersized operational text. |
| `business.html`, `public/business/*` | Separate ink header/light canvas, cobalt `#2D4FE7`, own 6px controls and 8px document radius, Unicode navigation symbols, no dark theme; own Overview/Team/Work/Activity/Settings navigation and Workspace escape. | Best operational composition to develop, but currently a distinct product shell. Dark mode and common components are missing. |
| `mobile/src/theme/tokens.ts` | Same bundled fonts; native putty `#CFC5B7` still described as web-mirrored, while web changed to `#DDD4C6`; orange branding. | “Mirrored” comments do not establish parity. Replace manual duplication with generated values. |
| `mobile/src/navigation/RootNavigator.tsx` | Navigation theme hardcodes `#F5F5F7/#FFFFFF` independently of native putty; native shell uses Home / Meet / Chat / Work / Files. | Even native has multiple competing palettes; theme adapter must cover navigation and portals. |
| `mobile/src/theme/motion.ts`, `glass.tsx`, `components/Screen.tsx` | Existing reduced-motion/transparency support, 44px hit target token, scalable heading workaround; `Screen` Retry is a Text action and back visual is 42px plus hitSlop. | Preserve accessibility foundations; normalize actual operable bounds/roles, not just appearance. |
| Separate site `/Users/ajhart/meetingassist/stride-site/app/globals.css` and `layout.tsx` | Ink `#101116`, paper `#F5F5F2`, cobalt `#2850FF`; same type families via Next fonts; hero tracking `-.066em`, max146px. | Site has drifted cobalt and overly tight hero optics. Its display scale must not become app heading scale. |
| `.superdesign/design-system.md` | Calls itself BonfireOS; says deployed HTML is visual source and describes another neutral palette/orange. | Historical reference after this contract is accepted. Keep media invariants, replace its claim of visual authority in the migration commit. |

The marketing repo is absent from the business-kernel checkout. It remains a separate nested repo at the path above; do not create a replacement hosting identity or silently copy its deployment configuration. Current source review does not certify all listed surfaces usable or production-ready.

## Canonical token source and generation contract

Implementation target: `design/stride.tokens.json`, `schemaVersion:1`, as the only editable cross-platform values. Structure: `color.{light,dark,constant}`, `space`, `radius`, `size`, `typography.{family,role}`, `motion`, `layout`, `icon`. Use semantic keys in camelCase; colors are opaque sRGB hex, dimensions are logical px/pt numbers, line heights are ratios, motion times milliseconds. No CSS variables, DynamicColorIOS values, platform names, or JSX in the source.

Generate deterministically into `public/design/stride-tokens.css` (`--stride-color-canvas`, etc.), `mobile/src/theme/generatedTokens.ts` (plain values/role data), and an exported `stride-tokens.css` with schema version and source SHA for the separately versioned site. Site checks in that exact generated artifact under `app/generated/stride-tokens.css`; its release records the source SHA. Generator check mode rejects drift. Root owns implementation; these paths are proposed new files, not existing artifacts.

Native `tokens.ts` becomes a small platform adapter using generated roles, DynamicColorIOS/appearance preference and bundled font names. Native `RootNavigator` and native sheets must consume that adapter. Web theme uses one shared appearance preference/resolver across legacy and Business routes, default system, with light/dark/system choices. Appearance can persist as a preference; authority, drafts acknowledged as saved, and business data cannot be fabricated through local persistence. Marketing may deliberately remain fixed ink with an explicit theme root; it still consumes the same ink/brand roles.

Legacy names map through a temporary compatibility stylesheet/adapter: `--bg-app → canvas`, `--surface-1/2 → surface`, `--surface-3 → surfaceInset`, `--text-1/2/3 → text/textSecondary/textMuted`, `--ring → focus`. Do **not** alias every `ember` reference to cobalt: classify existing usages as action, agent activity, capture activity or decoration; rebind the semantic role and remove obsolete decoration. No new raw palette literals outside generated files, approved media constants and third-party content. Lock explicit exceptions instead of pretending a grep can prove visual parity.

### Color values

| Semantic role | Light | Dark |
| --- | --- | --- |
| canvas | `#F6F7FB` | `#10131B` |
| surface | `#FFFFFF` | `#191E2A` |
| surfaceInset | `#EDF0F7` | `#131824` |
| text | `#151823` | `#F5F7FC` |
| textSecondary | `#515C70` | `#B6C0D2` |
| textMuted | `#5E6A7F` | `#99A6BD` |
| border, decorative | `#D7DDEA` | `#333E53` |
| borderControl | `#78859E` | `#71809B` |
| action | `#2D4FE7` | `#A8B8FF` |
| onAction | `#FFFFFF` | `#10182F` |
| actionHover | `#2442C8` | `#BCC8FF` |
| actionPressed | `#1E36A8` | `#92A6FF` |
| selection | `#E5EBFF` | `#27365C` |
| focus | `#2D4FE7` | `#A8B8FF` |
| successText / successSurface | `#17623D` / `#E5F3EB` | `#8FE2B2` / `#183326` |
| warningText / warningSurface | `#795100` / `#FFF1D6` | `#FFD082` / `#382C16` |
| dangerText / dangerSurface | `#B42335` / `#FFE9ED` | `#FFACB6` / `#3B2029` |

Constants: `brandCobalt:#2D4FE7`, `onBrand:#FFFFFF`, `stage:#080B12`, `stageChrome:#151C2B`, `stageText:#F5F7FC`, `stageTextSecondary:#B6C0D2`, `stageControlBorder:#9AADCA`, `videoLetterbox:#000000`, `speaking:#67DAA0`, `leaveFill:#B42335`, `onLeave:#FFFFFF`. Fixed brand fields always use onBrand, never adaptive action text. Do not repurpose success to mean active connection, capturing audio, or business success. Label those states separately.

Disabled controls retain readable text, a clear disabled state and an adjacent reason when useful; avoid whole-component opacity that accidentally dims explanations. Focus: 3px ring, 3px offset; controls over images/video have solid chrome and a contrasting offset. Decorative separators do not identify operable control boundaries.

Local luminance calculations checked representative pairs: white on cobalt 6.21:1; light secondary on inset 5.91:1; dark muted on surface 6.78:1; status foregrounds on their fills exceed 5.6:1. These are token calculations, not complete rendered accessibility proof. Recompute all final combinations, hover/selected/focus states and composited overlays in implementation; ordinary text target 4.5:1, large text/control boundaries 3:1.

### Typography, geometry and motion

| Role | Web compact / wide | Native default | Weight / line height / tracking |
| --- | --- | --- | --- |
| Display, marketing only | 64–140px fluid, fit actual words | Not a product role | 550 / 1.02 / `-.035em`; per-word optical check, never tighter than `-.045em` |
| Page title | 32 / 40px | 32pt | 500 / 1.18 / `-.03em` |
| Section title | 22 / 24px | 22pt | 600 / 1.25 / `-.015em` |
| Item title | 17 / 18px | 17pt | 600 / 1.35 / `-.005em` |
| Body / composer | 16px | 17pt | 400 / 1.5 / 0 |
| Reading / result | 17px | 17pt | 400 / 1.65 / 0 |
| Secondary / button | 14 / 16px | 15 / 17pt | 400 / 500 / 1.4 / 0 |
| Label | 12px | 12pt | 500 / 1.4 / `.02em`; sentence case by default |

Sans: Google Sans Flex 400/500/600 (700 only for purposeful emphasis). Mono: Geist Mono 400/500 for exact identifiers, compact numeric evidence and occasional section labels, not all dates/body text. Use tabular numbers where values update; proportional body text. Native font roles use the already bundled files, scale with Dynamic Type and derive line boxes rather than clipping fixed line heights. Native system controls keep system typography where required. Web uses existing self-hosted faces, font-display swap and metric-compatible fallbacks; migrate font file format only with measured size/render benefit. Do not load duplicate font families for Business.

Spacing: `4,8,12,16,20,24,32,40,48,64,96`. Radius: `control:8`, `surface:12`, `sheet:20`, `pill:999`. Full-bleed media can have zero radius. Use borders/space to group ordinary content; shadows only for elevation: popover `0 8px 32px rgba(8,11,18,.14)` light / `.36` dark. Native approximates elevation visually; numerical shadow equality is not parity. Glass only for temporary overlays that benefit from scene context, with solid fallback for reduced transparency; no blur across large scrollable work areas.

Size: `hitMin:44`, `control:48`, `controlCompact:44`, `icon:20`, `iconNav:24`, `avatarSmall:32`, `avatar:40`. Dense desktop controls may be visually 36px only with nonoverlapping 44px targets. Do not use pills for every control: pills indicate status/filter membership, rectangular rounded controls indicate actions. Selected authority is labeled “Authority: Full autonomy”, never styled as an unlabeled launch button.

Motion: `feedback:120`, `transition:220`, `reveal:320`, ease cubic-bezier(`.2,0,0,1`). Only opacity/transform; no `transition:all`, animated layout measurement, or spring-bounce navigation. Maximum press scale `.98` on standalone buttons; rows use surface feedback without shrinking text. No idle shimmer or breathing agent avatars. Real audio amplitude is information, not invented keyframes. Reduced motion removes positional/decorative animations and smooth scrolling, retaining understandable state changes. Offscreen/tab-hidden marketing demos pause. Live status never depends on animation.

Icons: shared semantic registry with 24-unit grid, 1.75-unit rounded stroke. Reuse/normalize current custom shell SVG geometry; generate web SVG and React Native SVG from the same paths. SF Symbols remain appropriate for native system actions (share, camera rotation, device picker) through named semantic mapping. No Unicode/emoji for core navigation, evidence status or controls. A monochrome outline can become selected with surface/label emphasis; icons never provide the only accessible name.

## One shell, two scope levels

Canonical primary destinations, same names/order on web and native: **Home / Work / Chat / Meet / Files**. Retain route and API IDs behind adapters; do not rename `office`, `video`, `Conversations`, or `Drive` keys merely to change visible labels. Home holds the current scope's mission/next move; Work holds execution/results; Chat conversations; Meet rooms/records; Files reusable material. Search and notifications are utilities. Profiles, memory inspection and settings are secondary destinations, not more primary tabs.

A persistent scope selector identifies **Personal** or the exact business name, with organization visible in its expanded selector. Business overview is Home in business scope. Team, Activity and Settings are contextual business destinations, not a second global bottom bar. Keep the current Business navigation until scoped integration is real; the migration must eliminate the Workspace escape as the everyday mental model, not simply recolor it.

Critical boundary: the new SQL business scope does not currently establish tenant isolation for inherited Chat/Meet/Files. Never apply a business title around a legacy global conversation and imply it is private to that business. Until a surface supports exact business authority, show a clearly labeled Personal workspace destination or an honest contextual unavailable state. Scope switching cancels/invalidates old reads, clears private content and live announcements, resets titles, and preserves drafts only under their exact authorized identity. Appearance/theme sharing does not share data authority.

At widths below 744 logical units use five labeled bottom destinations with safe-area padding. Detail flows replace that bar with a clear Back path when space/focus requires it; active calls keep an accessible return-to-call indicator without creating a second competing toolbar. At 744–1023 use a collapsible 208px sidebar only if the content retains adequate width; otherwise compact navigation. At >=1024 use a 224px sidebar and contextual header. iPad split-view width, font size and usable content decide layout, not device model. At large accessibility sizes collapse navigation and stack all essential controls.

Content: 20px compact / 32px medium / 48px wide gutters; maximum ordinary canvas1440px, reading column72ch. Wide result view gives the document the majority of space and a 280–320px evidence column; phone uses one reading column and disclosure. Never shrink a desktop control wall onto an iPad. iPad keyboard/trackpad, phone keyboard/safe areas and portrait/landscape all have explicit layouts. Opening a detail must retain the list position and selection on return, including deep links and back/forward navigation.

## Shared component and state contract

Implement the first coherent kit around actual flows: scope header, navigation, page/section title, field/help/error, action, status, identity row, list/detail shell, composer, media card, result reader, evidence disclosure, sheet/dialog, empty/error state and call controls. Every component has light/dark/disabled/focus/pressed and large-text variants. Reusable styles without reusable state behavior are insufficient.

| State | Required behavior on web and native |
| --- | --- |
| Initial load | Stable page skeleton/structure and named loading status; no fake agent rows, metrics or results. Do not announce every skeleton node. |
| Empty | Name what is absent and one real next action, if available. “No agents hired” is distinct from “Team could not load.” |
| Feature unavailable | Concise capability reason in context. No active control that leads only to an apology; no setup implying execution exists. |
| Refresh/offline | Preserve authorized readable content with “Last updated…” or offline status when appropriate. Preserve unsent draft locally only under exact user/scope; do not claim saved to server. |
| Submit/save | Disable only the affected action; prevent duplicate submissions; communicate saved draft vs running operation. A lost acknowledgement permits idempotent retry, not a new action. |
| Failure | Specific understandable failure and supported recovery. Keep related user input, but never auto-apply it to a different result version. |
| Access/session changed | Clear private contents, titles, announcements and previews immediately; offer sign-in or scope selection. Do not keep a forbidden cached result on screen. |
| Conflict/stale result | Refresh exact identity and show retained draft separately; explicit user decision before applying it to changed work. |
| Agent execution | Distinguish queued, running, blocked, reconciling and saved result. Activity indicators reflect authoritative state, not timed theater. |
| Cost unknown | Say “Cost still being checked”; known recorded cost is not total cost. Never translate unknown into zero; pending does not prove a provider was called. |
| Outcome | Result saved, optional human review and observed impact are separate records. “Impact not recorded yet” is enough; no universal score or forced approval gate. |

Full autonomy means priorities/actions within granted resources without routine human approval. Authority presets are editable policy ceilings, never evidence that agents are hired, a provider is ready, or a business is executing. A no-agent business remains honestly empty. Public profiles, if developed, inherit this kit and emphasize selected source-linked work/outcomes; no private result becomes public through a visual refresh or profile default.

## First-class conversation and calls

Chat uses readable 16/17px text, a consistent sender/time hierarchy and a stable composer. Media cards give the content a generous image/video area and a useful linked title; metadata/caption remains readable when an image or embed fails. Provider brand does not override STRIDE navigation/state controls. Failed send/upload, retry, progress, canceled upload, unsupported embed, expired/private attachment and lost connectivity must be inspectable without losing the message draft. Phone attachment sheets respect keyboard and safe area; iPad can show conversation list/detail without narrowing the composer into a strip. Document/deck editing is a work surface, not a webview whose errors disappear behind chrome.

Calls use fixed stage tokens in both appearances. The participant/shared content is dominant; essential microphone, camera, audio device, sharing and Leave controls stay visible/reachable. Leave is isolated and red; mute/camera-off state has icon plus label, not red color alone. Persistent stop-sharing must be reachable while another panel is open. Native system share/capture UI remains native. Joining shows device preview and actual permissions; denied mic, unavailable camera or missing playback route gets a local remedy, not an indefinite spinner.

Speaker stage is stable through short pauses; explicit pin and active screen share have clear precedence and do not lose chosen state on rotation. Share content uses contain with readable text and intentional letterboxing; face tiles can crop appropriately. Preserve the canonical video element/native render identity and media ownership through layout changes. No cloned WebRTC session for a redesigned screen, no call teardown when opening chat, no beauty filter enabled by default. Self preview is mirrored only for camera; shared content is not mirrored.

Phone: focused speaker/share, reachable filmstrip/participants, no labels beneath the home indicator, controls clear of PiP and keyboard. iPad/desktop: gallery or stage+filmstrip, optional conversation/transcript panel that leaves sufficient shared-content area, keyboard navigation, controllable PiP/pinning. Large text may move secondary controls into a labeled sheet; mute and Leave remain direct.

Keep three dimensions separate: **connection/AV health**, **device state**, **capture/transcript coverage**. A green “Live” connection cannot mean every person's speech was captured. Capture states are `not_recording`, `excluded_consent`, `waiting_for_audio`, `capturing`, `catching_up`, `degraded`, `ended`. Use plain display labels such as “Transcript off”, “Waiting for audio”, “Capturing speech”, “Catching up”, “Transcript incomplete”. Preserve exact per-source state in details. Coverage dispositions `transcribed`, `no_text`, `excluded_gate`, `excluded_consent`, `revoked`, `expired`, `missing`, `unknown_after_crash` remain distinct; do not collapse them to silence or success. A healthy participant cannot clear another participant's issue. Reconnected media does not recover missing words.

Call UI/recovery companion: `docs/plans/stride-meeting-capture-recovery.md` (owner: native/media agent). Visual parity does not establish Zoom quality. The release gate needs real web↔iPhone↔iPad calls, measured join/audio/video/recovery behavior, route changes, screen sharing, background/foreground, network interruption and sustained multi-party performance. Record device/build/network and baseline/candidate results; investigate audio interruption, silent capture, frozen video and restart loss independently. Marketing must not claim Zoom parity from screenshots or fixture tests.

## Surface inventory and parity checklist

All paths below are relative to the business-kernel repo except the explicitly separate website. A row remains open until its complete lifecycle is rendered and exercised on both supported product platforms. Existing screen filenames do not prove route reachability. RootNavigator's current registrations are the routing inventory; compatibility-only/unreachable screens must be marked as such before retirement.

| Wave / surface | Actual files/anchors to migrate | Acceptance check |
| --- | --- | --- |
| A — token/source authority | `index.html` root/dark rules; `public/stride-operating.css`; `public/business/business.css`; `mobile/src/theme/{tokens,colors,motion,glass,installTypography}.ts*`; `appearancePreference.ts`, `mobileAppearanceStore.ts`; `.superdesign/design-system.md` | [ ] Generated values; no conflicting appearance; compatible adapters; contrast and typography checked. |
| A — shell/login/settings | `business.html`; `public/business/business.js`; `index.html` `#accessPanel`, `#appShell`, `.pd1-primary-nav`; `mobile/src/navigation/{RootNavigator,NativeUniversalShell,nativeShellModel,types}.ts*`; `components/Screen.tsx`; `screens/{LoginScreen,SettingsScreen,AlertsScreen}.tsx` | [ ] Same visible destinations; correct scope; auth errors, deep link/back, large text, keyboard, focus and safe areas. |
| B — Home and business setup | `index.html` `#officeTool`; `public/stride-operating.css`; `public/business/{business.js,api.js}`; `mobile/src/screens/CanvasScreen.tsx` and native Business screen still to build | [ ] Exact real setup/save/reload, no agents vs unavailable, authority ceiling, allowance semantics, scope isolation. Native absence remains explicit until implemented. |
| B — Work/results/team | `index.html` Work/project detail and `#artifactsTool`, `#researchTool`; `public/business/*`; `mobile/src/screens/{WorkHubScreen,AgentTeamScreen,DeckScreen}.tsx`; `mobile/src/work/{WorkProjectSheet,WorkEvidencePanel,useSelectedWorkDetail,studioProjectModel}.ts*` | [ ] Same current result/status/cost meaning; exact version, optional feedback, unknown outcome, revoked read, durable return-to-list. Existing native Studio Work is not SQL Business parity. |
| C — conversations/media | `index.html` `#chatTool`, rich-link/composer/thread surfaces; `public/composer-dictation.js`; `mobile/src/screens/{ChatScreen,ThreadScreen,NewConversationScreen}.tsx`; `mobile/src/messaging/{LinkPreviewCard,InlineArtifactPreview,AttachmentSourceSheet,MentionComposerInput}.tsx` | [ ] Long thread, rich media, send/upload recovery, permissions, image failure, text size, phone/tablet composer. ChannelRiff is exported by ThreadScreen and included. |
| D — call entry/stage | `index.html` room/lobby, `#videoStack`, `#roomChatPanel`, `#consentPanel`, transfer/room activity panels; `mobile/src/screens/{MeetScreen,RoomScreen,CreateRoomScreen}.tsx`; `components/Room{Participants,Conversation,Consent,Specialists}Sheet.tsx`; `realtime/{useNativeRoom,callPresentation,meetingIntelligence}.ts` | [ ] Device/join/leave, consent, capture vs connection, all recovery states, pin/share/rotation/PiP, real-media gate. Lifecycle files are read/verification targets, not authorization to change transport. |
| E — files/knowledge/records | `index.html` `#filesTool`, `#memoryTool`, `#memoryInspectorPanel`, `#memoryTimelinePanel`; `mobile/src/screens/{LibraryScreens,FilesScreen,MeetingsScreen,MemoryInspectorScreen,CollectionScreen}.tsx`; `components/FilePreviewModal.tsx` | [ ] Empty/loading/search/no match/forbidden/expired; exact audience/version; long names, large files, missing coverage. Resolve LibraryScreens reexports before duplicating implementation. |
| E — editors/embedded tools | `index.html` `#artifactsTool`/artifact editor/share panel; `mobile/src/screens/{OSWebScreen,DeckViewerScreen}.tsx`; artifact routes; `DeckScreen.tsx` compatibility sections | [ ] Editing/saving/error/conflict/history/share constraints, real native navigation and keyboard; preserve artifact authority. |
| F — secondary/public candidates | `index.html` profile/organization/network dialogs and notification panel; `mobile/src/screens/{StrideProductScreens,NativeShellScreens}.tsx`; `BoardScreen.tsx` compatibility file | [ ] Profile/WorkRecord/Organizations/People/Coworker/Requests/Recruiting/ContributionApprovals/NetworkDraft and Network/WorkSearch/You/ContactInbox aliases mapped; current availability honest. No social expansion prerequisite. Board route currently resolves to WorkHub, not BoardScreen. |
| A→F — website and brand packaging | Separate `stride-site/app/{globals.css,layout.tsx,page.tsx}`, `components/{LivingCompany,WorkLoopDemo,Wordmark,BrandMark,StrideSignal,MarketingCradle,SystemDemo}.tsx`; website public assets; product `public/*wordmark*`, icons/manifest; native brand/theme assets | [ ] Reachable components distinguished from retained unused demos; exported token SHA; hero optical spacing; truthful interactive story, pause/reduced motion, small-phone CTA, favicon/app icon consistency. Preserve maker/login/hosting identity. |

## Migration order, evidence and rollback

1. **A: prove the shared kit first.** Root implements source/generator/adapters and one isolated component/state gallery with explicit fixtures. Review matched web/native light/dark examples at default and enlarged text, plus site hero optical correction. Do not claim a generated theme has migrated the product. Freeze old visual guidance as historical; list remaining overrides.
2. **B: migrate one real business lifecycle on web and iOS.** Home/setup → team readiness → Work → exact result/reconciliation. Use the current shell's editorial result composition and first-run spec. SQL native parity requires actual API integration. Bring the legacy Work reader/Home into the same visible grammar in this wave; do not leave beige Work behind a cobalt Home. Shell integration must honor the current tenant boundary.
3. **C and D: conversation and calls as complete surfaces.** Shared composer/media card first, then call entry/stage and recovery. Run media regressions before and after each layout change. Tokens/components can migrate independently; transport behavior needs separate evidence. Never release a visual improvement that hides or worsens capture loss.
4. **E: knowledge, Files and editors.** Migrate list/detail, missing/expired/error and write-conflict states together; reduce legacy CSS by deleting only the superseded surface blocks. Keep old route IDs/deep links valid.
5. **F: complete the inventory and brand assets.** Secondary settings/profile/organization screens cannot remain an unrelated theme. Public work portfolios remain optional product work. Align website exports and application icons at the reviewed release boundary; website storytelling can stay expressive while using the same tokens.

Each wave has one writer per mutable resource and a narrow shared commit/release checkpoint. Keep a route-level rollback path until replacement has behavioral parity; token rollback restores the entire matching generated/adapted set, not just JSON. No mass regexp palette swap, duplicate shadow shell, simultaneous API rename or database mutation for a design change. Source checks should identify unmanaged hardcoded palette values, not rewrite user content or third-party media.

Evidence matrix for every migrated row: web 320/390/430 wide phones, 744/834/1024 tablets including split view, 1280/1440/2056 desktop; iPhone and iPad installed builds in portrait/landscape; light/dark/system; increased text through accessible sizes; reduced motion/transparency; keyboard-only web/VoiceOver native; long names/data, loading/empty/offline/error/revoked/conflict. Use real authorized test API flows for functionality and explicitly labeled synthetic fixtures for rare states. Record screenshot/build/device/path and behavioral outcome, including unresolved items. A static screenshot cannot certify loading/retry/auth/back-stack behavior.

Performance gates: preserve current Business baseline (~26KB gzipped HTML/JS/CSS excluding reused fonts from prior QA); shared token/component cost should be explained if initial route payload grows materially. Target no font-induced header/composer shift, immediate local press feedback, no long task over50ms from ordinary navigation/theme changes in measured representative fixtures, and no offscreen animation/polling introduced by styling. Virtualize long lists and isolate live call updates from full-page rerenders. Record actual measurements/device rather than claiming “60fps” from transform-only CSS.

Existing decisive suites to extend where behavior changes: `public/business/api.test.mjs`; `scripts/stride-work-evidence.test.mjs`; native `contrast.test.ts`, `nativeUniversalShell.test.ts`, `nativeRoomTerminal.test.ts`, `linkPreviewRecovery.test.ts`; `scripts/live-media-smoke*.mjs`; `frontend_latency_test.go`. Keep assertions about behavior/identity/geometry, not exact old hex values as a substitute for visual acceptance.

Root has accepted the corrected palette and taken ownership of the canonical generator. Remaining decision: accept the navigation/layout contract and authorize each ordered surface migration. No external dependency, paid design tool, new brand name or product authority change is needed. Full design acceptance remains open until the inventory has matched rendered and functional evidence; this document closes the design choice, not the migration.
