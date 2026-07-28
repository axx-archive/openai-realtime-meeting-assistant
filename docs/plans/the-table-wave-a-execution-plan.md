# The Table — Wave A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the spine of The Table — a locked iPhone buzzes when a teammate posts, the canvas renders their words, and the thread knows what you have and haven't read.

**Architecture:** Server-side, three additions ride existing patterns rather than inventing stores: a read-marker file persisted with `web_push.go`'s atomic-write idiom, an `omitempty` `Table` flag on the thread record (the `Intake` rule at `scout_chat_threads.go:131`), and an Expo push lane added as a fourth sibling inside the existing `pushNotificationRecord` fan-out so per-recipient filtering is reused unchanged. Client-side, the live-line arbitration ladder is extracted as a **pure function** because mobile tests are plain `node:test` with no React renderer — a pure function is the only testable shape available.

**Tech Stack:** Go (server, `net/http`, no framework) · TypeScript / React Native 0.86 / Expo SDK ~57.0.8 · `@shopify/flash-list` 2.0.2 · `expo-notifications` (new) · tests: `go test` and `node --import tsx --test`

## Global Constraints

- **Design spec:** [`the-table-design.md`](the-table-design.md). Shell canon: [`voice-first-mobile-design.md`](voice-first-mobile-design.md).
- **Expo docs must be read at the pinned version** before writing client code: `https://docs.expo.dev/versions/v57.0.0/`. Per [`mobile/AGENTS.md`](../../mobile/AGENTS.md). Expo has changed; training data is not authoritative.
- **v57 notification handler uses `shouldShowBanner` / `shouldShowList`** — *not* the older `shouldShowAlert`. Verified against the v57 reference.
- **CocoaPods on this Mac dies without a UTF-8 locale.** Every `pod install` and `npx expo run:ios` must be prefixed `LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8` or `Pod::Config#installation_root` throws `Unicode Normalization not appropriate for ASCII-8BIT`.
- **iOS deployment target is 16.4** (`app.config.ts:116`). Anything iOS-26-only needs the three-way `<Glass>` fallback.
- **Never animate a `GlassView` to `opacity: 0`** — it kills the effect. Use `glassEffectStyle: { animate, animationDuration }`.
- **Never store an unread count.** `scout_chat_delete.go` deletes messages; a stored count drifts. Always derive.
- **Motion canon:** `transform` and `opacity` only. Never animate `width`/`height`. Honor Reduce Motion.
- **This tree is shared with concurrent sessions.** Never `checkout`, `reset`, or discard changes you did not make. Stage explicit paths — never `git add -A`.
- **Go tests take a long time under `-race`;** use a 35m timeout if you enable it.

---

### Task 1: Server — per-thread read markers

**Files:**
- Create: `thread_read_markers.go`
- Create: `thread_read_markers_test.go`
- Modify: `scout_chat_threads.go` (add `unreadCount` to the thread list payload near `:1679`)
- Modify: `main.go` (route registration)

**Interfaces:**
- Consumes: `scoutChatMessageRecord` (`scout_chat_threads.go:82`), `writeJSONFileAtomically`, `meetingMemoryPath()`
- Produces:
  - `threadUnreadCount(messages []scoutChatMessageRecord, readAt string, viewerEmail string) int`
  - `upsertThreadReadMarker(marker threadReadMarker) error`
  - `lookupThreadReadMarker(tenantID, userEmail, threadID string) threadReadMarker`
  - `POST /assistant/threads/{id}/read` accepting `{"lastReadMessageId": "..."}`

- [ ] **Step 1: Write the failing test for the count**

```go
package main

import "testing"

func msg(id, author, createdAt string) scoutChatMessageRecord {
	return scoutChatMessageRecord{ID: id, AuthorEmail: author, CreatedAt: createdAt}
}

func TestThreadUnreadCountExcludesOwnMessages(t *testing.T) {
	messages := []scoutChatMessageRecord{
		msg("1", "dana@x.com", "2026-07-28T10:00:00Z"),
		msg("2", "aj@x.com", "2026-07-28T10:01:00Z"),
		msg("3", "dana@x.com", "2026-07-28T10:02:00Z"),
	}
	// Read through 10:00. Message 2 is the viewer's own and never counts.
	if got := threadUnreadCount(messages, "2026-07-28T10:00:30Z", "aj@x.com"); got != 1 {
		t.Fatalf("unread = %d, want 1", got)
	}
}

func TestThreadUnreadCountWithNoMarkerCountsEverythingButOwn(t *testing.T) {
	messages := []scoutChatMessageRecord{
		msg("1", "dana@x.com", "2026-07-28T10:00:00Z"),
		msg("2", "aj@x.com", "2026-07-28T10:01:00Z"),
	}
	if got := threadUnreadCount(messages, "", "aj@x.com"); got != 1 {
		t.Fatalf("unread = %d, want 1", got)
	}
}

// The marker is a TIMESTAMP, so deleting the message the user last read
// cannot strand the count. This is the whole reason the design stores
// ReadAt rather than only an id (spec §15.5).
func TestThreadUnreadCountSurvivesDeletionOfTheMarkedMessage(t *testing.T) {
	messages := []scoutChatMessageRecord{
		// The 10:00 message the marker was set from has been deleted.
		msg("3", "dana@x.com", "2026-07-28T10:02:00Z"),
	}
	if got := threadUnreadCount(messages, "2026-07-28T10:00:30Z", "aj@x.com"); got != 1 {
		t.Fatalf("unread = %d, want 1", got)
	}
}

func TestThreadUnreadCountIgnoresUnparseableTimestamps(t *testing.T) {
	messages := []scoutChatMessageRecord{msg("1", "dana@x.com", "not-a-time")}
	if got := threadUnreadCount(messages, "2026-07-28T10:00:00Z", "aj@x.com"); got != 0 {
		t.Fatalf("unread = %d, want 0", got)
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test -run TestThreadUnread ./... 2>&1 | head -20`
Expected: FAIL — `undefined: threadUnreadCount`

- [ ] **Step 3: Implement the count**

```go
package main

import (
	"strings"
	"time"
)

// threadReadMarker is one row per (tenant, user, thread). It stores the moment
// the user last read AND the message id they read through: the timestamp drives
// the count (immune to deletion) and the id anchors the client's "new messages"
// divider.
type threadReadMarker struct {
	TenantID          string `json:"tenantId"`
	UserEmail         string `json:"userEmail"`
	ThreadID          string `json:"threadId"`
	LastReadMessageID string `json:"lastReadMessageId,omitempty"`
	ReadAt            string `json:"readAt"`
}

// threadUnreadCount counts messages newer than the marker, excluding the
// viewer's own.
//
// Counting from a TIMESTAMP rather than storing a count is load-bearing:
// scout_chat_delete.go removes messages, and a stored count drifts the moment
// one goes. Scanning for the marked message id would have the same problem in
// reverse — deleting the message the user last read would strand the scan and
// report the whole thread unread.
//
// An unparseable message timestamp counts as read. A message we cannot place in
// time cannot honestly be called new, and guessing "new" here would put a
// permanent unread badge on a thread the user has already seen.
func threadUnreadCount(messages []scoutChatMessageRecord, readAt string, viewerEmail string) int {
	viewer := strings.ToLower(strings.TrimSpace(viewerEmail))

	var since time.Time
	if trimmed := strings.TrimSpace(readAt); trimmed != "" {
		parsed, err := time.Parse(time.RFC3339, trimmed)
		if err != nil {
			// A corrupt marker must not hide messages. Treat it as "never read".
			since = time.Time{}
		} else {
			since = parsed
		}
	}

	count := 0
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.AuthorEmail), viewer) && viewer != "" {
			continue
		}
		created, err := time.Parse(time.RFC3339, strings.TrimSpace(message.CreatedAt))
		if err != nil {
			continue
		}
		if created.After(since) {
			count++
		}
	}
	return count
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test -run TestThreadUnread ./... 2>&1 | tail -5`
Expected: PASS

- [ ] **Step 5: Add the store, mirroring `web_push.go`'s idiom**

Follow `web_push.go:82-134` exactly — `threadReadMarkersPath()` next to `push-subscriptions.json`, `loadThreadReadStoreFile`, `mutateThreadReadStore`, `snapshotThreadReadStore`, all writing through `writeJSONFileAtomically`. Upsert keys on `(TenantID, UserEmail, ThreadID)` and **only ever moves the marker forward** — a late-arriving request from a stale client must not un-read a thread:

```go
// Markers only advance. A retry or an out-of-order request from a stale client
// must never move the marker backwards and resurrect messages the user read.
func upsertThreadReadMarker(marker threadReadMarker) error {
	return mutateThreadReadStore(func(state *threadReadStoreData) {
		for index, existing := range state.Markers {
			if !sameReadMarkerKey(existing, marker) {
				continue
			}
			if !readMarkerAdvances(existing.ReadAt, marker.ReadAt) {
				return
			}
			state.Markers[index] = marker
			return
		}
		state.Markers = append(state.Markers, marker)
	})
}
```

- [ ] **Step 6: Write the handler and wire the route**

`POST /assistant/threads/{id}/read` — authenticated, body `{"lastReadMessageId": "..."}`, stamps `ReadAt` **server-side** (never trust a client clock for a monotonicity guarantee). Add `unreadCount` to each thread in the list payload built near `scout_chat_threads.go:1679`.

- [ ] **Step 7: Full package test, then commit**

```bash
go build ./... && go test -run 'TestThreadUnread|TestThreadReadMarker' ./... 2>&1 | tail -5
git add thread_read_markers.go thread_read_markers_test.go scout_chat_threads.go main.go
git commit -m "feat: per-thread read markers"
```

---

### Task 2: Server — Table flag and auto-provision

**Files:**
- Modify: `scout_chat_threads.go` (record at `:121`, creation at `:351`, list payload at `:1679`)
- Create: `table_thread_test.go`

**Interfaces:**
- Consumes: `scoutChatThreadRecord`, `createScoutChatThread(ownerEmail, createdBy, title, visibility string)`
- Produces:
  - `Table bool` field on `scoutChatThreadRecord`
  - `ensureTenantTable(tenantID, ownerEmail string) (scoutChatThreadRecord, error)`
  - `table: true` and Table-first ordering in `GET /assistant/threads`

- [ ] **Step 1: Write the failing tests**

```go
package main

import "testing"

// The omitempty round-trip rule: scout_chat_threads.go:131 documents that every
// new thread field must be omitempty so pre-existing records on disk decode
// unchanged. A Table field that serialized as `"table":false` on every legacy
// thread would rewrite the entire store on first save.
func TestTableFieldOmitsWhenFalse(t *testing.T) {
	encoded, err := json.Marshal(scoutChatThreadRecord{ID: "t1", Title: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "table") {
		t.Fatalf("legacy thread serialized a table key: %s", encoded)
	}
}

// This is the main package's established isolation pattern — verified against
// scout_chat_threads_test.go. Tests swap the package-level `kanbanApp` global
// rather than passing an app around; there is no newTestBoardApp constructor.
func TestEnsureTenantTableIsIdempotent(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	first, err := kanbanApp.ensureTenantTable("tenant-a", "aj@shareability.com")
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	second, err := kanbanApp.ensureTenantTable("tenant-a", "aj@shareability.com")
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("second call created a new Table: %s != %s", first.ID, second.ID)
	}
}

func TestEnsureTenantTableCreatesAPublicChannel(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	table, err := kanbanApp.ensureTenantTable("tenant-a", "aj@shareability.com")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Public visibility is what makes it a CHANNEL — #-prefix, broadcast
	// notify, @-mention parsing all come free from that one value.
	if table.Visibility != scoutChatVisibilityPublic {
		t.Fatalf("visibility = %q, want public", table.Visibility)
	}
	if !table.Table {
		t.Fatal("the provisioned thread is not flagged as the Table")
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test -run 'TestTable|TestEnsureTenantTable' ./... 2>&1 | head -20`
Expected: FAIL — `unknown field Table`

- [ ] **Step 3: Add the field**

At `scout_chat_threads.go:138`, directly under `IntakeStep`, matching that block's documented rule:

```go
	// Table marks the tenant's single permanent team thread — the one the
	// canvas live line and the shell's chat control point at. omitempty for
	// exactly the reason Intake above is: every pre-Table thread on disk must
	// round-trip unchanged.
	Table bool `json:"table,omitempty"`
```

- [ ] **Step 4: Implement `ensureTenantTable`**

Returns the existing Table if one exists for the tenant; otherwise creates a `public` thread titled `#team` with `Table: true`. **Exactly one per tenant** — the existence check is the enforcement, and it must happen inside the same lock as creation or two concurrent first-loads race into two Tables.

- [ ] **Step 5: Call it from the list handler; sort Table first**

The Table is provisioned lazily on first list so it exists on day one without an admin step (spec §15).

- [ ] **Step 6: Run, then commit**

```bash
go build ./... && go test -run 'TestTable|TestEnsureTenantTable' ./... 2>&1 | tail -5
git add scout_chat_threads.go table_thread_test.go
git commit -m "feat: flag and auto-provision the tenant Table thread"
```

---

### Task 3: Server — native push lane (Expo)

**Files:**
- Create: `device_push.go`
- Create: `device_push_test.go`
- Modify: `notifications.go:223` (`pushNotificationRecord` fan-out)
- Modify: `main.go` (routes)

**Interfaces:**
- Consumes: `notificationRecord`, `pushRecipientMatches`, `resolvePushPrefs`, `prunePushSubscriptions` (as the pruning model)
- Produces:
  - `pushNotificationRecordDevice(record notificationRecord)`
  - `expoPushMessagesFor(record notificationRecord, tokens []deviceTokenRecord) []expoPushMessage`
  - `chunkExpoPushMessages(tokens []string, size int) [][]string`
  - `applyExpoPushTickets(tokens []string, tickets []expoPushTicket) []string` — returns tokens to prune
  - `expoPushTicket{ Status, ID string; Details expoPushTicketDetails }` and `expoPushTicketDetails{ Error string }`
  - `deviceTokenRecord` (shape in Step 3)
  - `POST /push/devices`, `DELETE /push/devices`

- [ ] **Step 1: Write the failing tests for the pure parts**

The HTTP send is a thin wrapper; the *testable* logic is batching and ticket handling.

```go
package main

import "testing"

// Expo rejects >100 messages per request with PUSH_TOO_MANY_NOTIFICATIONS.
func TestExpoPushBatchesAtOneHundred(t *testing.T) {
	tokens := make([]string, 250)
	for i := range tokens {
		tokens[i] = "ExponentPushToken[t]"
	}
	batches := chunkExpoPushMessages(tokens, 100)
	if len(batches) != 3 {
		t.Fatalf("batches = %d, want 3", len(batches))
	}
	if len(batches[0]) != 100 || len(batches[2]) != 50 {
		t.Fatalf("bad batch sizes: %d, %d", len(batches[0]), len(batches[2]))
	}
}

// DeviceNotRegistered is the APNs equivalent of a VAPID 410: the token is gone
// for good and must be pruned, exactly as prunePushSubscriptions treats a dead
// endpoint. Anything else is transient and must NOT prune.
func TestDeviceNotRegisteredTokensArePruned(t *testing.T) {
	tokens := []string{"good", "dead", "ratelimited"}
	tickets := []expoPushTicket{
		{Status: "ok", ID: "x"},
		{Status: "error", Details: expoPushTicketDetails{Error: "DeviceNotRegistered"}},
		{Status: "error", Details: expoPushTicketDetails{Error: "MessageRateExceeded"}},
	}
	prune := applyExpoPushTickets(tokens, tickets)
	if len(prune) != 1 || prune[0] != "dead" {
		t.Fatalf("prune = %v, want [dead]", prune)
	}
}

// A short ticket array must not panic or mis-attribute an error to the wrong
// token — Expo returns one ticket per message, but a malformed response is a
// network reality, not a hypothetical.
func TestApplyExpoPushTicketsToleratesShortResponses(t *testing.T) {
	prune := applyExpoPushTickets([]string{"a", "b"}, []expoPushTicket{{Status: "ok"}})
	if len(prune) != 0 {
		t.Fatalf("prune = %v, want empty", prune)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test -run 'TestExpoPush|TestDeviceNotRegistered|TestApplyExpoPush' ./... 2>&1 | head -20`
Expected: FAIL — undefined symbols

- [ ] **Step 3: Implement the store and the sender**

```go
// deviceTokenRecord is one Expo push token bound to an account, keyed by token
// the way pushSubscriptionRecord is keyed by endpoint.
type deviceTokenRecord struct {
	TenantID  string `json:"tenantId"`
	UserEmail string `json:"userEmail"`
	Token     string `json:"token"`
	Platform  string `json:"platform,omitempty"`
	CreatedAt string `json:"createdAt"`
}

const expoPushSendURL = "https://exp.host/--/api/v2/push/send"

// expoPushMaxBatch is Expo's documented per-request ceiling; exceeding it
// returns PUSH_TOO_MANY_NOTIFICATIONS for the whole request, not a partial send.
const expoPushMaxBatch = 100
```

`pushNotificationRecordDevice` becomes the fourth sibling in the `notifications.go:223` fan-out. **It reuses `pushRecipientMatches` and `resolvePushPrefs` unchanged** — per-recipient filtering is transport-agnostic and duplicating it is how the two lanes drift apart and start disagreeing about who gets what.

- [ ] **Step 4: Run tests, confirm pass**

Run: `go test -run 'TestExpoPush|TestDeviceNotRegistered|TestApplyExpoPush' ./... 2>&1 | tail -5`
Expected: PASS

- [ ] **Step 5: Wire the routes and the fan-out, then commit**

```bash
go build ./... && go test -run 'TestExpoPush|TestDevice' ./... 2>&1 | tail -5
git add device_push.go device_push_test.go notifications.go main.go
git commit -m "feat: native Expo push lane alongside web push"
```

---

### Task 4: Server — per-thread mute

**Blocked by Task 3.**

**Files:**
- Modify: `device_push.go` (mute check in the lane)
- Create: `thread_mute.go`, `thread_mute_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes: `deviceTokenRecord`, `pushNotificationRecordDevice` (Task 3)
- Produces:
  - `threadMuted(tenantID, userEmail, threadID string) bool`
  - `deviceLaneDelivers(record notificationRecord, viewerEmail string, muted bool) bool` — the pure predicate the lane calls, extracted so the mute rule is testable without a transport
  - `POST /assistant/threads/{id}/mute` accepting `{"muted": true}`

- [ ] **Step 1: Write the failing test**

```go
// Mute silences AMBIENT volume only. A direct mention still delivers — muting a
// channel means "stop buzzing me for every message", not "make me unreachable",
// and conflating those is how someone misses being paged.
func TestMuteSilencesAmbientButNotMentions(t *testing.T) {
	ambient := notificationRecord{ThreadID: "table-1", Kind: "chat"}
	mention := notificationRecord{ThreadID: "table-1", Kind: "chat", UserEmail: "aj@x.com"}

	if deviceLaneDelivers(ambient, "aj@x.com", true /* muted */) {
		t.Fatal("ambient message delivered to a muted thread")
	}
	if !deviceLaneDelivers(mention, "aj@x.com", true /* muted */) {
		t.Fatal("direct mention was swallowed by a thread mute")
	}
}
```

- [ ] **Step 2: Run, confirm failure, implement, confirm pass**

Run: `go test -run TestMute ./... 2>&1 | tail -5`

The distinction is already available for free: a targeted notification carries `UserEmail`; a broadcast channel post does not. Do **not** re-derive it from message text.

- [ ] **Step 3: Commit**

```bash
git add thread_mute.go thread_mute_test.go device_push.go main.go
git commit -m "feat: per-thread mute for the device push lane"
```

---

### Task 5: Client — live line arbitration as a pure function

**Files:**
- Create: `mobile/src/canvas/liveLine.ts`
- Create: `mobile/src/__tests__/liveLine.test.ts`

**Interfaces:**
- Produces:
```typescript
export type LiveLineInput = {
  viewerEmail: string;
  tableThreadId: string | null;
  tableUnreadCount: number;
  tableLastMessage: { authorName: string; authorEmail: string; text: string } | null;
  mentions: Array<{ threadId: string; threadName: string; text: string; authorName: string }>;
  liveRooms: number;
  otherUnreadCount: number;
  otherUnreadThreads: number;
  showPreviews: boolean;
};

export type LiveLineResult = {
  kind: 'mention-table' | 'mention-elsewhere' | 'table' | 'rooms' | 'other' | 'none';
  author: string | null;
  text: string | null;
  mentioned: boolean;
  threadId: string | null;
};

export function resolveLiveLine(input: LiveLineInput): LiveLineResult;
```

- [ ] **Step 1: Write the failing tests**

```typescript
import test from 'node:test';
import assert from 'node:assert/strict';
import { resolveLiveLine } from '../canvas/liveLine';

const base = {
  viewerEmail: 'aj@x.com',
  tableThreadId: 'table-1',
  tableUnreadCount: 0,
  tableLastMessage: null,
  mentions: [],
  liveRooms: 0,
  otherUnreadCount: 0,
  otherUnreadThreads: 0,
  showPreviews: true,
};

test('nothing live renders as absent, not as "Nothing live"', () => {
  const line = resolveLiveLine(base);
  assert.equal(line.kind, 'none');
  assert.equal(line.text, null);
});

test('a mention in the Table outranks everything else', () => {
  const line = resolveLiveLine({
    ...base,
    liveRooms: 3,
    tableUnreadCount: 9,
    mentions: [
      { threadId: 'table-1', threadName: '#team', text: 'can you look?', authorName: 'Dana' },
    ],
  });
  assert.equal(line.kind, 'mention-table');
  assert.equal(line.author, 'Dana');
  assert.equal(line.mentioned, true);
  assert.equal(line.threadId, 'table-1');
});

// Rung 3. The single most common state in daily use.
test('unread Table messages render the message itself, not a count', () => {
  const line = resolveLiveLine({
    ...base,
    tableUnreadCount: 3,
    tableLastMessage: {
      authorName: 'Dana',
      authorEmail: 'dana@x.com',
      text: 'Pushed the pricing memo',
    },
  });
  assert.equal(line.kind, 'table');
  assert.equal(line.author, 'Dana');
  assert.equal(line.text, 'Pushed the pricing memo');
});

// You know what you said. Showing it back reads as a broken feed.
test('the viewer\'s own last message never renders', () => {
  const line = resolveLiveLine({
    ...base,
    tableUnreadCount: 1,
    tableLastMessage: { authorName: 'AJ', authorEmail: 'AJ@X.com', text: 'shipping it' },
  });
  assert.notEqual(line.kind, 'table');
});

// The privacy switch (spec §5) degrades the line to a count — it must never
// silence it, or turning previews off would hide that anything happened.
test('previews off degrades to a count and keeps the signal', () => {
  const line = resolveLiveLine({
    ...base,
    showPreviews: false,
    tableUnreadCount: 4,
    tableLastMessage: { authorName: 'Dana', authorEmail: 'dana@x.com', text: 'secret' },
  });
  assert.equal(line.kind, 'table');
  assert.equal(line.author, null);
  assert.equal(line.text, '4 new in #team');
  assert.ok(!JSON.stringify(line).includes('secret'));
});

test('rooms outrank ambient unread from other threads', () => {
  const line = resolveLiveLine({ ...base, liveRooms: 2, otherUnreadCount: 5, otherUnreadThreads: 3 });
  assert.equal(line.kind, 'rooms');
  assert.equal(line.text, '2 rooms are live.');
});

test('one live room is singular', () => {
  const line = resolveLiveLine({ ...base, liveRooms: 1 });
  assert.equal(line.text, '1 room is live.');
});
```

- [ ] **Step 2: Run and confirm failure**

Run: `cd mobile && npm test 2>&1 | head -20`
Expected: FAIL — cannot resolve `../canvas/liveLine`

- [ ] **Step 3: Implement the ladder**

Strict priority, exactly the table in spec §5. Own-message comparison is **case-insensitive and trimmed** — the email in a message record and the email on the session are not guaranteed to match in case.

- [ ] **Step 4: Run and confirm pass**

Run: `cd mobile && npm test 2>&1 | tail -10`
Expected: all pass

- [ ] **Step 5: Commit**

```bash
git add mobile/src/canvas/liveLine.ts mobile/src/__tests__/liveLine.test.ts
git commit -m "feat: live line arbitration ladder as a pure function"
```

---

### Task 6: Client — live line renders the message

**Blocked by Tasks 1, 2, 5.**

**Files:**
- Modify: `mobile/src/canvas/useLiveLine.ts`
- Modify: `mobile/src/screens/CanvasScreen.tsx:186-200`
- Modify: `mobile/src/screens/SettingsScreen.tsx` (previews toggle)
- Modify: `mobile/src/api/client.ts`, `mobile/src/api/types.ts` (`table`, `unreadCount`)

**Interfaces:**
- Consumes: `resolveLiveLine` (Task 5), `unreadCount` + `table` from the thread list (Tasks 1, 2)
- Produces: a `LiveLine` whose `author` and `text` are rendered separately by the canvas

- [ ] **Step 1: Replace the hand-rolled arbitration in `useLiveLine`**

The hook keeps its fetching and its `useFocusEffect` / office-event refresh. Everything between "we have data" and "here is a sentence" moves to `resolveLiveLine`. The existing behaviour where **a failed poll leaves the previous line in place** (`useLiveLine.ts:106-109`) must be preserved — a network blip must not look like "all clear".

- [ ] **Step 2: Render author and body as separate spans**

```tsx
{live.text ? (
  <Pressable
    accessibilityRole="button"
    accessibilityLabel={live.author ? `${live.author}: ${live.text}` : live.text}
    onPress={openLiveTarget}
    style={({ pressed }) => [styles.liveLine, pressed && styles.pressed]}
  >
    <Animated.Text
      style={[styles.liveText, live.mentioned && styles.liveMention, { opacity, transform: [{ translateY }] }]}
      numberOfLines={2}
      ellipsizeMode="tail"
    >
      {live.author ? <Text style={styles.liveAuthor}>{live.author} · </Text> : null}
      {live.text}
    </Animated.Text>
  </Pressable>
) : null}
```

```ts
liveAuthor: {
  color: colors.text1,
  fontWeight: '500',
},
```

- [ ] **Step 3: Animate the change**

Cross-fade plus a 4pt rise on `live.text` change, driven by `Animated` with `useNativeDriver: true`. **`transform` and `opacity` only.** Under `useReduceMotion()` set the values directly with no animation — the content still updates, only the motion is removed.

- [ ] **Step 4: Add the previews toggle**

`Show message previews`, default **on**, persisted the way existing settings are. Threads through to `resolveLiveLine`'s `showPreviews`.

- [ ] **Step 5: Verify in the simulator**

```bash
cd mobile && LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 npx expo run:ios
```

Screenshot with `xcrun simctl io <udid> screenshot /tmp/canvas.png` — the Simulator MCP would not attach on this Mac (it demands `sudo xcode-select -s`, which needs AJ's password).

Confirm by eye: greeting → line spacing still reads as one thought; a two-line message ellipsizes rather than pushing the Dock; the line is **absent** when nothing is unread.

- [ ] **Step 6: Commit**

```bash
git add mobile/src/canvas/useLiveLine.ts mobile/src/screens/CanvasScreen.tsx mobile/src/screens/SettingsScreen.tsx mobile/src/api/
git commit -m "feat: canvas live line renders the message itself"
```

---

### Task 7: Client — the chat circle

**Blocked by Task 2.**

**Files:**
- Create: `mobile/src/components/ChatCircle.tsx`
- Modify: `mobile/src/screens/CanvasScreen.tsx:260-318` (the `dockRow`)
- Modify: `mobile/src/components/Dock.tsx` (confirm no badge is added there)
- Modify: `mobile/src/__tests__/brandAssets.test.ts` if it asserts on the dock row's shape

**Interfaces:**
- Consumes: `Glass`, `usingLiquidGlass`, `useReduceMotion`, live-line `mentioned`
- Produces: `<ChatCircle open={navOpen} mentioned={boolean} onPress={() => void} />`

- [ ] **Step 1: Build the circle**

44pt (`hitMin`) glass circle, `radius.full`, `interactive`, SF Symbol `bubble.left.and.bubble.right.fill` at 19pt in `colors.text1`. Left-aligned in the row above the Dock; the NavCluster toggle stays right-aligned.

**Unlabelled at rest** — a label under a lone circle adds chrome to a canvas whose thesis is emptiness, and the live line already teaches the destination. `accessibilityLabel="Team"`, `accessibilityHint="Opens your team's thread."`

- [ ] **Step 2: Cross-fade it out when the cluster opens**

```tsx
// Four 58pt labelled cluster items plus a 44pt circle plus the toggle does not
// fit an iPhone SE's 375pt. The circle's function is covered while the cluster
// is open — Threads is one of the four — so it yields rather than collides.
opacity: progress.interpolate({ inputRange: [0, 1], outputRange: [1, 0] }),
pointerEvents: open ? 'none' : 'auto',
```

**Do not animate the `GlassView`'s own `opacity` to 0** — that kills the effect outright. Animate a wrapping `Animated.View`, or use `glassEffectStyle: { animate: true, animationDuration }`.

- [ ] **Step 3: Move the direct-mention ember dot here**

6pt `colors.ember` dot, top-trailing, shown only when an unread **direct mention** exists — never for ambient volume, or it becomes wallpaper.

This removes the dot from the Dock, changing shell canon §14.5 deliberately: the Dock means *"talk to Scout"*, and a message badge there conflates two unrelated things. Leave a comment in `Dock.tsx` recording that the omission is intentional, so a later reader does not "restore" it.

- [ ] **Step 4: Verify layout at the narrow end**

Simulator on **iPhone SE (375pt)** *and* a Pro Max. Open and close the cluster. Confirm: no overlap at any point in the animation, the Dock does not shift, and the collapsed cluster still occupies **zero layout space** (the absolute-positioning rule at `NavCluster.tsx:186-196` — left in flow, hidden items reserve an invisible band that shoves the Dock to mid-screen).

- [ ] **Step 5: Run the mobile tests and commit**

```bash
cd mobile && npm test 2>&1 | tail -5
git add mobile/src/components/ChatCircle.tsx mobile/src/components/Dock.tsx mobile/src/screens/CanvasScreen.tsx
git commit -m "feat: permanent chat circle; mention dot moves off the Dock"
```

---

### Task 8: Client — push registration and deep link

**Blocked by Task 3.**

**Files:**
- Modify: `mobile/package.json`, `mobile/app.config.ts`
- Create: `mobile/src/push/usePushRegistration.ts`
- Create: `mobile/src/push/deepLink.ts`, `mobile/src/__tests__/pushDeepLink.test.ts`
- Modify: `mobile/App.tsx`, `mobile/src/auth/AuthContext.tsx`

**Interfaces:**
- Consumes: `POST/DELETE /push/devices` (Task 3)
- Produces: `usePushRegistration()`, `parsePushTarget(data: unknown): PushTarget | null`

- [ ] **Step 1: Read the pinned docs first**

`https://docs.expo.dev/versions/v57.0.0/sdk/notifications/`. Do not write this task from memory — the handler field names changed.

- [ ] **Step 2: Write the failing test for the pure part**

Registration and permissions cannot be unit-tested without a device. Deep-link target parsing can, and it is where a silent bug would actually hurt.

```typescript
import test from 'node:test';
import assert from 'node:assert/strict';
import { parsePushTarget } from '../push/deepLink';

test('a thread notification resolves to its thread and message', () => {
  const target = parsePushTarget({ threadId: 't1', messageId: 'm9', threadName: '#team' });
  assert.deepEqual(target, { threadId: 't1', messageId: 'm9', threadName: '#team' });
});

// A notification is a request to see ONE thing. Landing on the canvas would
// make the user navigate twice (shell §14.5).
test('a payload with no thread yields null rather than a canvas fallback', () => {
  assert.equal(parsePushTarget({ kind: 'digest' }), null);
  assert.equal(parsePushTarget(null), null);
  assert.equal(parsePushTarget('nonsense'), null);
});

test('a non-string threadId is rejected rather than coerced', () => {
  assert.equal(parsePushTarget({ threadId: 12 }), null);
});
```

- [ ] **Step 3: Run, confirm failure**

Run: `cd mobile && npm test 2>&1 | head -20`

- [ ] **Step 4: Install and configure**

```bash
cd mobile && npx expo install expo-notifications
```

Add `expo-notifications` to `plugins` in `app.config.ts`. This changes native config, so a rebuild is required:

```bash
cd mobile && LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 npx expo run:ios
```

- [ ] **Step 5: Implement registration**

```typescript
import * as Notifications from 'expo-notifications';
import Constants from 'expo-constants';

// v57 field names. `shouldShowAlert` is the OLD API and is silently ignored.
Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldPlaySound: true,
    shouldSetBadge: true,
    shouldShowBanner: true,
    shouldShowList: true,
  }),
});

const { status } = await Notifications.requestPermissionsAsync({
  ios: { allowAlert: true, allowBadge: true, allowSound: true },
});

const projectId = Constants.expoConfig?.extra?.eas?.projectId;
const token = await Notifications.getExpoPushTokenAsync({ projectId });
```

Register on login, `DELETE /push/devices` on logout. **Leaving a token registered after logout pushes one account's messages to whoever signs in next on that device.**

- [ ] **Step 6: Wire the deep link, including cold start**

```typescript
// Cold start: the app was launched BY the notification, so no listener has
// been attached yet. Without this the tap opens the canvas and the user has
// to navigate to the thing they were told about.
const initial = Notifications.getLastNotificationResponse();
if (initial?.notification) navigateToTarget(initial.notification);

const subscription = Notifications.addNotificationResponseReceivedListener((response) =>
  navigateToTarget(response.notification),
);
return () => subscription.remove();
```

- [ ] **Step 7: Device verification — this is Wave A's real gate**

On a **physical iPhone**, signed in as user A, with the app **backgrounded and the phone locked**: post a message to the Table from user B on the web client. The phone must buzz, the banner must show the author and the message, and tapping it must land in the Table at that message.

The simulator cannot verify this. A green simulator run is not this gate.

- [ ] **Step 8: Commit**

```bash
git add mobile/package.json mobile/app.config.ts mobile/src/push/ mobile/src/__tests__/pushDeepLink.test.ts mobile/App.tsx mobile/src/auth/AuthContext.tsx
git commit -m "feat: native push registration, badge, and thread deep links"
```

---

### Task 9: Client — the Table thread screen

**Blocked by Tasks 1, 2.**

**Files:**
- Modify: `mobile/src/screens/ThreadScreen.tsx`
- Modify: `mobile/src/messaging/MessageBubble.tsx`
- Create: `mobile/src/messaging/unreadBoundary.ts`, `mobile/src/__tests__/unreadBoundary.test.ts`

**Interfaces:**
- Consumes: `unreadCount` and read markers (Task 1), `POST /assistant/threads/{id}/read`
- Produces: `firstUnreadIndex(messages, readAt, viewerEmail): number` (−1 when all read)

- [ ] **Step 1: Write the failing test for the boundary**

```typescript
import test from 'node:test';
import assert from 'node:assert/strict';
import { firstUnreadIndex } from '../messaging/unreadBoundary';

const at = (iso: string, email = 'dana@x.com') => ({ id: iso, createdAt: iso, authorEmail: email });

test('the boundary sits at the first message the viewer has not read', () => {
  const messages = [at('2026-07-28T10:00:00Z'), at('2026-07-28T10:05:00Z'), at('2026-07-28T10:06:00Z')];
  assert.equal(firstUnreadIndex(messages, '2026-07-28T10:01:00Z', 'aj@x.com'), 1);
});

test('all read yields -1 so no divider renders', () => {
  const messages = [at('2026-07-28T10:00:00Z')];
  assert.equal(firstUnreadIndex(messages, '2026-07-28T10:01:00Z', 'aj@x.com'), -1);
});

// Your own message must not open an unread run — sending from another device
// would otherwise draw a "new messages" line above your own text.
test('the viewer\'s own message never starts the unread run', () => {
  const messages = [at('2026-07-28T10:05:00Z', 'aj@x.com'), at('2026-07-28T10:06:00Z')];
  assert.equal(firstUnreadIndex(messages, '2026-07-28T10:01:00Z', 'aj@x.com'), 1);
});
```

- [ ] **Step 2: Run, confirm failure, implement, confirm pass**

Run: `cd mobile && npm test 2>&1 | tail -10`

- [ ] **Step 3: Swap `ScrollView` for `FlashList`**

`@shopify/flash-list` 2.0.2 is already a dependency and unused. `keyExtractor` on message id. Mention parsing stays memoized per message (`MessageBubble` is already `React.memo` with a `useMemo` on `parseMentions` — preserve both).

- [ ] **Step 4: Land at the boundary, not the bottom**

When `firstUnreadIndex >= 0`, `initialScrollIndex` targets it and a divider renders above that row: `80 new messages`. Below the boundary everything is chronological; **nothing is hidden**.

- [ ] **Step 5: Replace refetch-on-event with append-and-scroll**

`ThreadScreen.tsx:77-79` currently calls `load()` on every `chat_thread` event, refetching the whole thread. At Table volume that is a full re-download because one message arrived. Append the new messages and scroll only if the user is already near the bottom — **yanking the viewport while someone is reading scrollback is worse than a missing message.**

- [ ] **Step 6: Render the "via Scout" chip**

`PostedOnBehalfOf` is set *unconditionally server-side* so Scout can never silently post as a user (`scout_chat_threads.go:93-98`), and the bubble ignores it today. Render a visible chip whenever it is present. This is a disclosure requirement, not decoration.

- [ ] **Step 7: Advance the read marker on genuine reads only**

`POST /assistant/threads/{id}/read` when the user reaches the bottom, or closes the thread while at the bottom. **Never on open.** Marking 80 messages read because the screen appeared is how people lose messages they never saw.

- [ ] **Step 8: Verify and commit**

Simulator: open a thread with unread messages, confirm the divider position, that scrollback is not yanked when a message arrives, and that the marker advances only on a real read.

```bash
cd mobile && npm test 2>&1 | tail -5
git add mobile/src/screens/ThreadScreen.tsx mobile/src/messaging/
git commit -m "feat: unread boundary, FlashList, append-not-refetch, via-Scout chip"
```

---

## Wave A Exit Gate

All of the following, in order. **The device test is the gate** — everything else is polish on a chat that does not yet reach anyone.

- [ ] `go build ./... && go test ./... 2>&1 | tail -20` — green
- [ ] `cd mobile && npm test` — green
- [ ] `cd mobile && npx tsc --noEmit` — clean
- [ ] Simulator: canvas renders a teammate's message; line **absent** when nothing unread
- [ ] Simulator: chat circle at rest, cross-fades cleanly on iPhone SE **and** Pro Max
- [ ] Simulator: unread divider lands correctly; scrollback is not yanked on a new message
- [ ] **Physical device, app backgrounded, phone locked: a teammate's Table message buzzes, and the tap lands in the Table at that message**
- [ ] `/code-review` on the full diff
- [ ] Previews toggle off → line degrades to a count and leaks no message text
