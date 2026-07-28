# The Table — Team Chat That Compounds

> Branch `design/voice-first-mobile` · 2026-07-28
> Continues: [`voice-first-mobile-design.md`](voice-first-mobile-design.md) (the shell this lands in)
> Distinct from: [`first-class-chat-design.md`](first-class-chat-design.md) (2026-06-21 — *personal*
> Scout threads, compaction, Codex runner. Same substrate, different problem. No conflict.)

## Executive Summary

Bonfire replaces the team's iPhone group message with **The Table** — one privileged public
thread that is permanently present in the shell, reaches you when the app is closed, and
keeps what the conversation deposited.

The argument is not a better chat UI. It is structural:

> **An everything-channel is a river, and rivers don't hold.**
> Every group thread that carries work talk, links, decisions, and banter at once becomes
> unsearchable by construction. You scroll back and you fail. That is not a defect of
> iMessage — it is what a flat append-only log *is*.

The Table is the same river with a floor under it. `@scout` answers questions *about* the
thread with citations; a thread-scoped catch-up compresses 80 unread into what happened;
and the files, links and decisions the conversation produced are pinned to the thread that
produced them. All three ride machinery this repo already has.

The shell change is one line of text. The canvas's live line stops *describing* activity
and starts *being* it: your teammate's actual words, on the home screen, in the space §9
already spends.

---

## Part I — Why This, Why Now

### 1. The job to be done

The team's iPhone thread is an **everything-channel**: work talk, links, decisions,
screenshots, and banter in one stream — and, critically, *people scroll back looking for
things*. That last fact is the whole design brief. A logistics channel ("running late")
has no memory requirement. An everything-channel is 90% memory requirement and 0% memory.

### 2. What we are actually competing with

Not Slack. **iMessage.** That is a harder competitor in one specific way and a much weaker
one in another:

| | iMessage | The Table |
|---|---|---|
| Reaches you | **Always.** Defining property. | Must be built (§6). Non-negotiable. |
| Zero friction | **Already on the phone.** | App is installed; cutover is a decision. |
| Finding what was said | Effectively impossible past a week | The entire product |
| Names spelled right | Generic dictation — "Dana" → "Donna" | Company-vocabulary lane (§10 of the shell doc) |
| The assistant | Absent | A participant in the thread |

We lose on ubiquity and win on memory. The design's job is to make sure we do not *also*
lose on delivery, because delivery is table stakes and a chat that misses messages is
uninstalled in a week regardless of how good its recall is.

### 3. The load-bearing gap inventory

Verified in-tree on 2026-07-28, not assumed:

| Capability | State | Evidence |
|---|---|---|
| `@`-mentions → targeted notification | ✅ built | `chat_mentions.go` |
| `@scout` routed to answer path, not notify | ✅ built | `chat_mentions.go` |
| Public threads *are* channels | ✅ built | `scout_chat_threads.go:1620` |
| Company-vocabulary dictation | ✅ built | `transcription_lane.go`, `domain_terms.go` |
| Realtime delivery **while app is open** | ✅ built | `OfficeEventsContext.tsx` websocket |
| Per-recipient notification fan-out | ✅ built | `notifications.go:223` `pushNotificationRecord` |
| Web push (VAPID) | ✅ built | `web_push.go` |
| **Native mobile push** | ❌ **absent** | no `expo-notifications` dep, no APNs code |
| **Reactions** | ❌ absent | no `reaction` symbol anywhere in Go |
| **Attachments in the mobile bubble** | ❌ absent | `ScoutMessage.files` exists; `MessageBubble.tsx` ignores it |
| Typing indicators / read receipts | ❌ absent | *and deliberately stay absent — §14* |
| Catch-up recap | ⚠️ **room**-scoped | `catch_up_recap.go:65` takes `roomID`, not `threadID` |
| Thread pinning / "default" thread | ❌ absent | no `pinned` concept in `scout_chat_threads.go` |
| **Per-thread read state** | ❌ **absent** | no `lastRead` / `unreadCount` anywhere — §15.5 |
| Attachments on the *server* message model | ✅ built | `scoutChatMessageRecord.Files` — client-only gap |
| `PostedOnBehalfOf` disclosure stamp | ⚠️ server-only | set unconditionally server-side; mobile bubble never renders it — §13 |

**The native push gap is the one that decides the project.** The shell design already
specifies an app-icon badge for direct mentions (§14.5) — it was designed and never built.
Without it the phone is silent when a teammate posts, and "replaces iMessage" is false on
day one.

---

## Part II — The Table in the Shell

### 4. The fifth noun

Canvas · Dock · Deck · Island · **Table**.

The Table is deliberately **not a new navigator**. It is *one thread*, flagged, that earns
permanent presence. The distinction matters: adding a fifth navigator would re-litigate the
tab-bar decision the shell design won. Adding a privileged instance of an existing noun does
not.

```
        ▁▃▅█▅▃▁                     THE CANVAS — unchanged

     Good morning.

  Dana · Pushed the pricing memo,   THE LIVE LINE — now renders the
       needs eyes before 2                          message itself

  [💬]                      [⊞]     rest: one tap to Table, one to all else
  [══════════ Dock ═════════════]
```

### 5. The live line becomes the thread

Today the line is a **signpost** — *"3 unread in #pricing. Dana mentioned you."* You tap it
to go find out what happened. §9 already spends this line; a signpost is the weakest
possible use of it.

> **Promote the line from *describing* activity to *being* activity.**

This costs zero new chrome, reuses the existing hook (`useLiveLine`), the existing tap
target, and the existing type ramp. It is strictly more informative, because a rendered
message can be *read and acted on* without navigating, which a count never can.

And it fixes a real emotional risk in the voice-first thesis. A minimalist home screen with
nothing on it reads as a **dead tool**. A teammate's actual words on it make the app feel
*inhabited*. This is the cheapest warmth available anywhere in the product.

**Arbitration ladder.** One line, strict priority, all inside `useLiveLine`:

| # | Condition | Renders | Tap routes to |
|---|---|---|---|
| 1 | Direct mention **in the Table** | the mentioning message, author in ember | Table, scrolled to that message |
| 2 | Direct mention elsewhere | the message + `in #channel` | that thread, scrolled to it |
| 3 | Table unread | `Dana · <last message>` | Table, at the unread boundary |
| 4 | Rooms live | `2 rooms are live.` | Deck → Rooms |
| 5 | Other threads unread | `5 unread in 3 threads.` | Deck → Threads |
| — | nothing | **absent** — §9 holds | — |

**Rendering rules.**
- Author in `text1` weight 500; body in `text2`. Two lines maximum, **ellipsized, never
  truncated mid-word**.
- **Never renders your own last message.** You know what you said; showing it back is noise
  that makes the line feel broken.
- Change animates as a cross-fade plus 4pt rise — `transform` and `opacity` only (motion
  canon §8.4), removed entirely under Reduce Motion.
- The existing 330pt `maxWidth` and centred measure are retained.

**Privacy.** A teammate's words now appear on the app's home screen. Settings gains
**"Show message previews"** (default **on**); off reverts rows 1–3 to counts
(`4 new in #team`). Default-on is correct — the inhabited canvas *is* the feature — but a
work app that can be screen-shared needs the switch to exist.

### 6. The chat circle

At rest the row above the Dock is `[💬] ......... [⊞]`.

The Dock cannot host this. It is full-width, and tap / hold / drag-up / trailing-keyboard
are all spent (`Dock.tsx`); a fifth affordance would collide with gestures the shell design
calls load-bearing.

**When the cluster opens, the chat circle cross-fades out.** Its function is covered —
Threads is one of the four items — and this keeps `NavCluster`'s existing right-to-left
geometry untouched. That is not cosmetic: four 58pt labelled items plus a permanent 44pt
circle plus the toggle does not fit an iPhone SE's 375pt without collision, and the cluster's
absolute positioning trap (shell §6.4) makes "just reflow it" expensive.

**Unlabelled at rest.** A label under a lone circle adds chrome to a canvas whose thesis is
emptiness, and the live line already teaches the destination — tapping a rendered message
lands in the same place. VoiceOver gets `accessibilityLabel="Team"`.

**The direct-mention ember dot moves here, off the Dock.** This changes shell canon §14.5,
deliberately and on the record: the Dock means *"talk to Scout,"* and hanging a message
badge on it conflates two unrelated things. A badge belongs on the control that takes you to
the messages. The Dock stays purely about voice, which is cleaner than what was ratified.

---

## Part III — Reaching You

### 7. Interrupt versus debt

The push gap forces a rule, and the rule is better than the gap:

> **Push is a transient interrupt — every Table message, mutable per thread.**
> **Badge is persistent debt — direct mentions only.**

This *preserves* shell canon §14.5 rather than breaking it. §14.5 governs **badges** — "ambient
volume never earns pixels outside the Deck." It says nothing about transient delivery. Push
tells you *now*; a badge is unresolved state that persists until you deal with it. Different
jobs, different thresholds.

Defaulting ambient Table messages to push matches the mental model the team is migrating
*from* — in iMessage everything buzzes and you mute what you don't want. A chat that only
notifies on `@` mentions feels broken to someone arriving from iMessage, and they will keep
the old thread open "just in case," which is how this fails.

### 8. The native lane

Server work is a **sibling to `web_push.go`, not a rebuild**, because per-recipient
filtering already exists and is transport-agnostic:

```
createNotification(...)
  └─ pushNotificationRecord(record)          notifications.go:223
       ├─ pushNotificationRecordLocal
       ├─ pushNotificationRecordWebsocket
       ├─ pushNotificationRecordOS           ← web push (VAPID)
       └─ pushNotificationRecordDevice       ← NEW: Expo Push / APNs
```

`pushRecipientMatches` and `resolvePushPrefs` are reused **unchanged**. The new lane needs:

- a device-token store, keyed `(tenantID, userEmail, token)`, mirroring
  `pushSubscriptionRecord`'s shape and its stale-token pruning (`prunePushSubscriptions`);
- an Expo Push send with receipt handling — `DeviceNotRegistered` prunes the token, exactly
  as a 404/410 prunes a VAPID endpoint;
- per-thread mute state, so "mute #team" is one tap in the thread header.

Client: `expo-notifications`, APNs entitlement, token registration on login, token teardown
on logout, and a deep link that opens **the thread at the mentioning message** — never the
canvas. A notification is a request to see one specific thing; landing home makes the user
navigate twice (shell §14.5).

> **Trap, from the shell build:** `app.config.ts` changes require a native rebuild, and
> CocoaPods on this Mac needs `LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8` or `pod install` throws
> `Unicode Normalization not appropriate for ASCII-8BIT`.

---

## Part IV — Inside the Table

### 9. Opening with 80 unread

The everything-channel's defining moment. Designed, not defaulted:

- **Scroll lands at the unread boundary, not the bottom.** iMessage lands at the bottom;
  that is correct for a 5-message thread and wrong for an 80-message one. Slack's boundary
  behaviour is right for volume.
- A divider: `80 new messages`.
- **On the divider: "Catch me up"** → an evidence-linked recap (§11).
- Below it, normal chronological messages. Nothing is hidden.

### 10. Ask the thread

Designed in shell §13.5, never built. It is the sharpest claim we have against every other
chat app, so it ships as a real control rather than a hidden gesture.

- **Invocation.** `@scout` in the composer (silent path), or tapping the Dock while the
  Table is open (voice path, already specified). No new gesture.
- **Rendering.** The answer is a real message *in the thread*, authored by Scout, in ember.
  Everyone sees it. It becomes part of the thread's memory. This is what makes Scout a
  participant rather than a private assistant with a shared address.
- **Evidence.** Every answer carries source chips beneath it — anchors to the messages it
  used. Tapping scrolls to the cited message. **An answer with no sources renders visibly as
  an answer with no sources** rather than borrowing unearned authority.
- **Private variant.** Holding the Dock dictates into the composer instead — "ask privately"
  is simply *don't send*. No separate mode to learn, and no risk of a private question
  appearing in a public channel, because posting requires the visible tap.

### 11. Thread catch-up

`catch_up_recap.go` is **room-scoped** (`exactCatchUpRecap(ctx, requesterEmail, roomID, focus)`).
The Table needs a thread-scoped sibling. What carries over unchanged is the part that
matters: `composeEvidenceLinkedCatchUp` is *deliberately extractive* — every bullet is copied
from one authorized primary body and carries its evidence id, and publication is
re-authorized before exposure.

That discipline is non-negotiable here. A recap that paraphrases a colleague inaccurately is
worse than no recap, because it will be quoted.

### 12. The deposit rail

The thread header carries a compact chip strip of what this conversation *produced* — files,
links, decisions — sourced from `files.go`, `decision_ledger.go`, and the message bodies.

- Present **only when non-empty**. An empty rail is chrome that narrates its own emptiness.
- Tap a chip → open it. Tap the rail → the full deposit sheet.
- It lives in the **thread**, not in a separate Work tab. That is the entire point: "I know
  we shared it here somewhere" is answered where the asking happens. A Work tab you have to
  remember to visit is a Work tab you don't visit.

### 13. Table stakes

**Reactions — iMessage tapbacks.** A fixed set of six (❤️ 👍 👎 😂 ‼️ ❓), long-press a
bubble to pick. Zero learning curve for a team arriving from iMessage, a tiny server model,
and no emoji picker, search, or skin-tone handling to build and maintain. Stored as
`Reactions map[string][]string` (emoji → author emails) on the message record, `omitempty`.

**Photos and screenshots.** `scoutChatMessageRecord.Files` already exists server-side and
`MessageBubble` ignores it — this is **client-only work**. Render image attachments inline;
add camera and photo-library controls to the composer. An everything-channel without
screenshots is not an everything-channel.

**The "via Scout" disclosure chip.** `scoutChatMessageRecord.PostedOnBehalfOf` is set
*unconditionally server-side* when Scout posts as a user (`start_chat_as_user`), specifically
so Scout can never silently impersonate someone — and its own comment states the client
renders a visible chip whenever it is present. **The mobile bubble does not.** That is a
disclosure gap, not a polish item, and it gets worse the moment a team's primary
conversation moves into this surface. Ships in Wave A with the bubble work, not Wave B.

**List performance.** `@shopify/flash-list` v2 is a dependency and unused (shell §15). The
Table will have thousands of messages. Stable `keyExtractor` on message id; mention parsing
stays memoized per message.

**Delivery.** Replace full-thread refetch on every `chat_thread` event with append-and-scroll
(shell §14). Refetching 2,000 messages because one arrived is the obvious scaling failure.

### 14. What we deliberately do not build

**No typing indicators. No read receipts.** Both are surveillance affordances that convert a
work chat into an availability monitor, and neither is load-bearing for the everything-channel
job. Their absence is a feature and should be stated as one, not apologised for.

---

## Part V — Server Model

### 15. Flagging the Table

`scout_chat_threads.go` already has the idiom for this. `scoutChatThreadRecord` grows
`omitempty` fields precisely so records on disk round-trip unchanged — see the `Intake` /
`IntakeStep` comment at `scout_chat_threads.go:131`, which spells out the rule.

```go
// Table marks the tenant's single permanent team thread — the one the canvas
// live line and the shell's chat control point at. omitempty so every
// pre-Table thread on disk round-trips unchanged, same rule as Intake above.
Table bool `json:"table,omitempty"`
```

- **Exactly one Table per tenant**, enforced at creation.
- **Auto-provisioned**: if a tenant has no Table when threads are first listed, create one —
  public, titled `#team`. It exists on day one without an admin step.
- Visibility is `public`, so it inherits channel semantics (`#`-prefix, broadcast notify,
  `@`-mention parsing) with no new code.
- No new store, no migration, no tenant settings table.

### 15.5 Read markers — the missing primitive

**Nothing in the system tracks what a user has read.** The canvas live line derives "unread"
from the *notifications* store, which is a different thing entirely: notifications are created
per mention and per event, not per message. There is no `lastRead`, no `unreadCount`, no
read marker of any kind.

Four things in this design silently depend on one:

- `80 new messages` and the unread boundary (§9)
- landing scroll position (§9)
- `unreadCount` on the threads list (§16)
- the Deck's per-channel unread dots (shell §14.5)

So it is built first, in Wave A, before anything that consumes it:

```go
// One row per (tenant, user, thread). The marker is the message id the user has
// seen through — not a count — so a count is always derivable and never drifts
// when messages are deleted (scout_chat_delete.go) or arrive out of order.
type threadReadMarker struct {
    TenantID          string `json:"tenantId"`
    UserEmail         string `json:"userEmail"`
    ThreadID          string `json:"threadId"`
    LastReadMessageID string `json:"lastReadMessageId"`
    ReadAt            string `json:"readAt"`
}
```

Persisted alongside the push store, using the same
`mutate*` / `snapshot*` / `writeJSONFileAtomically` pattern (`web_push.go:118-134`).

**Storing an id, not a count, is load-bearing.** A stored count drifts the moment a message
is deleted — and this repo *has* message deletion (`scout_chat_delete.go`). A marker plus the
message list yields a correct count every time, from data that cannot disagree with itself.

**The client advances the marker on genuine reads only** — scrolled to the bottom, or thread
closed while at the bottom. Not on open. Opening a thread with 80 unread and immediately
marking all read is how you lose messages you never saw.

### 16. API surface

| Endpoint | Change |
|---|---|
| `GET /assistant/threads` | Table sorts first; carries `table: true` and `unreadCount` |
| `POST /assistant/threads/:id/read` | **new** — advance the read marker (§15.5) |
| `POST /assistant/threads/:id/reactions` | **new** — toggle a tapback |
| `POST /assistant/threads/:id/catchup` | **new** — thread-scoped evidence-linked recap |
| `GET /assistant/threads/:id/deposits` | **new** — files, links, decisions from this thread |
| `POST /assistant/threads/:id/mute` | **new** — per-thread push preference |
| `POST /push/devices` · `DELETE /push/devices` | **new** — device-token register / teardown |

---

## Part VI — Waves

Each wave ships something usable on its own.

| Wave | Scope | Gate |
|---|---|---|
| **A — It exists and it reaches you** | Read markers (§15.5) · Table flag + auto-provision · native push lane · **per-thread mute** · live line rebuild · chat circle · unread boundary · "via Scout" chip · FlashList + append-not-refetch | Device test: locked phone receives a Table message |
| **B — Table stakes** | Tapbacks · photos in bubbles + composer · previews setting | Simulator + `/code-review` |
| **C — Why you never go back** | Ask-the-thread with source chips · thread catch-up · deposit rail | Evidence-citation correctness review |
| **D — End-to-end design audit** | Visual + interaction audit across the whole app, including these surfaces | Contrast + motion + `/code-review` |

**Wave A's device test is the real gate.** Everything else is polish on a chat that does not
yet reach anyone. If a locked iPhone does not buzz, the wave is not done regardless of how
the surfaces look.

---

## Part VII — Risks

| Risk | Mitigation |
|---|---|
| **Push doesn't land** — the whole project fails | Wave A gates on a locked-device test, not a simulator. Expo receipts prune dead tokens like VAPID 410s already do. |
| Notification fatigue drives the team back | Per-thread mute ships in **Wave A, alongside the push default that creates the need** — shipping push-everything without the valve is the version of this that gets the app muted at the OS level, which is unrecoverable. Badge stays mentions-only so persistent state never becomes wallpaper. |
| Unread surfaces built on a primitive that doesn't exist | Read markers are the **first** item in Wave A, before the boundary, the counts, or the dots that consume them (§15.5). |
| Live line feels noisy on a chatty day | It renders only *unread*, one line, two lines max, and reverts to absent once read. Previews setting is the escape hatch. |
| Catch-up paraphrases a colleague wrongly | Reuse `composeEvidenceLinkedCatchUp`'s extractive discipline verbatim — copied bullets with evidence ids, re-authorized before exposure. Never free-form summary. |
| Chat circle re-creates the tab bar | One circle, not five; cross-fades out when the cluster opens; no persistent label. The canvas at rest gains exactly 44pt of chrome. |
| Thread grows past client capacity | FlashList + append-and-scroll from Wave A, before volume exists — not retrofitted after it hurts. |
| Moving the mention dot breaks learned behaviour | It has not shipped to users yet; §14.5 is design canon, not deployed muscle memory. |

---

## Decisions Ratified (AJ, 2026-07-28)

1. **Job**: the iPhone thread is an **everything-channel** — work, links, decisions, banter,
   and people *do* scroll back. Memory is the product.
2. **Cutover**: everyone is already on Bonfire with the app installed. No roster or import
   work. *(Superseded in one respect: push was assumed present and is not — §3.)*
3. **Placement**: **live line becomes the thread**, plus a permanent chat circle. Accepts the
   two canon changes (live line's nature; mention dot off the Dock).
4. **Substance**: **all of it** — ask-the-thread, catch-up, deposit rail, *and* table stakes.
   "Best in class, better than iMessage."
5. **Reactions**: iMessage tapbacks, fixed set of six.
6. **Previews**: show by default, with a Settings toggle.
7. **Audit**: Wave D, after C, over the whole result.

## Open Questions

None blocking Wave A. Two judgment calls made rather than deferred:

1. **Expo Push over raw APNs.** Expo's service handles token lifecycle and receipts, and the
   app is already an Expo managed build. Raw APNs is a later optimization if delivery
   latency or vendor dependency becomes a real problem — not a day-one concern.
2. **Auto-provisioned `#team` rather than an admin setup step.** A chat that requires
   configuration before the first message does not get a first message.
