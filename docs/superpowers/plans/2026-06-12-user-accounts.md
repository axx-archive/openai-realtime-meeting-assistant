# Real User Accounts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the shared room password with real per-user accounts for the six shareability.com users — bcrypt passwords (seeded `B0NFIRE!`), cookie sessions, WebAuthn passkeys, in-app password change, and Resend-backed password reset. No public signup.

**Architecture:** The Go server gains three new modules: `accounts.go` (JSON-file user store + bcrypt + reset tokens), `auth_http.go` (cookie session store + `/auth/*` handlers), and `passkeys.go` (WebAuthn ceremonies via go-webauthn). The WebSocket handler authenticates from the session cookie instead of a password message. The client login card posts to `/auth/login`, the passkey button does real WebAuthn, and the settings dialog gains an Account section (change password, manage passkeys, sign out).

**Tech Stack:** Go 1.24 stdlib HTTP, `golang.org/x/crypto/bcrypt`, `github.com/go-webauthn/webauthn`, Resend HTTP API, vanilla JS in `index.html`.

**Users (the only accounts; no signup):** aj@, tim@, e@ (Erick), joel@, tyler@, caitlyn@ — all `@shareability.com`.

---

### Task 1: User store (`accounts.go`)

**Files:** Create `accounts.go`, `accounts_test.go`.

- [x] Write failing tests: store seeds exactly 6 users on first load; `authenticateUser("aj@shareability.com", "B0NFIRE!")` succeeds and returns Name "AJ"; wrong password fails; unknown email fails (constant-time-ish: still runs bcrypt); `changeUserPassword` rejects wrong current password, accepts correct one and the new password then authenticates; store round-trips through its JSON file; emails are normalized (trim+lowercase); seeding is idempotent (second load doesn't reset a changed password).
- [x] Implement `accounts.go`:
  - `type userAccount struct { Email, Name string; PasswordHash []byte; WebAuthnIDBytes []byte; Credentials []webauthn.Credential; PasswordChangedAt time.Time }` (webauthn fields added in Task 4; start with a `Credentials json.RawMessage`-free struct and grow it).
  - `usersFilePath()` → sibling of meeting memory file: `filepath.Join(filepath.Dir(meetingMemoryPath()), "users.json")`, env override `BONFIRE_USERS_PATH`.
  - Global `userStore` with mutex; `loadUserStore()` lazily reads file, seeds missing seed users (bcrypt of `defaultMeetingRoomPassword`), writes file `0600`.
  - `authenticateUser(email, password) (*userAccount, bool)`, `changeUserPassword(email, current, new string) error` (min 8 chars), `setUserPassword(email, new string) error` (for resets), `findUser(email)`.
  - Reset tokens: `createPasswordResetToken(email) (string, error)` → 32 random bytes hex, stores SHA-256 of token in-memory with 30-min expiry, single use; `consumePasswordResetToken(token) (email string, ok bool)`.
- [x] `go test ./... -run 'Account|UserStore|Reset' -count=1` → PASS. Commit.

### Task 2: Sessions + `/auth/login|logout|me` (`auth_http.go`)

**Files:** Create `auth_http.go`, `auth_http_test.go`. Modify `main.go` (route registration).

- [x] Failing tests (httptest): login with good creds sets `bonfire_session` HttpOnly cookie and returns `{email,name}`; bad creds → 401; GET `/auth/me` with cookie → user JSON, without → 401; logout clears session (subsequent `/auth/me` 401); sessions persist across store reload (file round-trip); cross-origin POST (Origin header mismatching Host) → 403.
- [x] Implement: session token = 32 random bytes hex; server stores SHA-256(token) → `{Email, Expires}` in `data/sessions.json` (env `BONFIRE_SESSIONS_PATH`), 30-day expiry, pruned on save. Cookie: `Path=/; HttpOnly; SameSite=Lax`, `Secure` when `r.TLS != nil || X-Forwarded-Proto == https`. Helpers: `createUserSession`, `userFromRequest(r) *userAccount`, `clearUserSession`. `sameOriginRequired` middleware-style check for all POST `/auth/*`. Simple per-IP fixed-window limiter (12 attempts / 5 min) on login + reset endpoints.
- [x] Register `http.HandleFunc("/auth/", authHandler)` in `main.go` next to other routes; internal mux switch on path.
- [x] Tests pass; commit.

### Task 3: WebSocket joins via session cookie

**Files:** Modify `main.go` (websocketHandler + participant case), `participants.go` (roster derives from accounts), affected tests.

- [x] Failing test: `participant` message admits using the session identity (name from account) and ignores any password; a websocket upgrade with no valid session cookie is rejected with 401 before upgrade.
- [x] In `websocketHandler`: resolve `user := userFromRequest(r)` first; if nil → `http.Error(w, "unauthorized", 401)` and return (also closes the pre-auth resource-allocation gap). In the `participant` case: drop password validation; `name := user.Name` (payload name ignored), keep capacity/admission logic.
- [x] `participants.go`: keep `canonicalParticipantName` (display normalization) but participant emails now come from the user store (`findUserByName`); keep `validMeetingPassword` only if other tests need it, else delete it and its tests. Roster shrinks to the six account names for the login-page preview copy.
- [x] Run full `go test ./... -count=1`; fix fallout (participants_test, archives untouched). Commit.

### Task 4: Passkeys (WebAuthn) server side (`passkeys.go`)

**Files:** Create `passkeys.go`, `passkeys_test.go`. Modify `accounts.go` (credential storage), `auth_http.go` (routes). `go get github.com/go-webauthn/webauthn`.

- [x] Endpoints: `POST /auth/passkey/register/begin` + `/finish` (session required) — registers a resident-key-preferred credential, stores it on the user; `POST /auth/passkey/login/begin` + `/finish` — discoverable login (no email needed) that creates a session on success; `GET /auth/passkeys` (list: id suffix, createdAt, label) and `POST /auth/passkey/delete` (session required).
- [x] WebAuthn config built per request: RPID = hostname of request Host (strip port), RPOrigins = request origin; ceremony `SessionData` kept in an in-memory map keyed by a short-lived random cookie (`bonfire_webauthn`, 5 min).
- [x] Tests: begin-registration returns a challenge for an authed session and 401 otherwise; credential add/list/delete round-trips through the user store file. (Full ceremony crypto is exercised in the browser, not unit tests.)
- [x] `go build ./... && go test ./... -count=1`; commit.

### Task 5: Password reset via Resend (`resend.go`)

**Files:** Create `resend.go`. Modify `auth_http.go` (reset endpoints), `resend`/auth tests.

- [x] Endpoints: `POST /auth/reset/request {email}` → always 202 (no account enumeration); if user exists, mint token (Task 1) and send email with link `{origin}/?reset={token}`. `POST /auth/reset/confirm {token, newPassword}` → consumes token, sets password, destroys all of that user's sessions.
- [x] `resend.go`: `sendAccountEmail(to, subject, html string) error` → POST `https://api.resend.com/emails` with `Authorization: Bearer $RESEND_API_KEY`, from `$RESEND_FROM` (default `Bonfire <no-reply@thebonfire.xyz>`). No key configured → log the reset URL at Info level and return nil (dev mode). Sender injected via package var so tests stub it.
- [x] Tests: request for known email calls sender with a link containing a consumable token; unknown email → 202 and no send; confirm with used/expired token → 400; confirm rotates password and kills sessions.
- [x] Commit.

### Task 6: Client — login via `/auth/login`, real passkey sign-in, reset flow

**Files:** Modify `index.html` (login card ~6894-6931, validate/join JS ~8291-8412, joinRoom ~9299, signal handler ~11383).

- [x] `joinRoom()` becomes: `await fetch('/auth/login', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({email, password})})` when not already authed; 401 → inline error on the card; then open the WebSocket and send `{event:'participant', data: JSON.stringify({name})}` (no password). Track authed user from the login response / `GET /auth/me` on page load (if authed: show "continue as {name}" state, password field optional, plus a "not you? sign out" link).
- [x] `signInWithPasskey()` → real WebAuthn: `POST /auth/passkey/login/begin` → options (base64url decode challenge/ids) → `navigator.credentials.get` → `POST .../finish` → on 200 join. Button enabled whenever `window.PublicKeyCredential` exists (not gated on cached identity anymore).
- [x] Forgot password: small `forgot password?` link under the password field → posts `/auth/reset/request` with the typed email, hint text "if that address has an account, a reset link is on its way." On load, `?reset=` in URL swaps the card into reset mode (new password + confirm → `/auth/reset/confirm`, then back to sign-in).
- [x] Remove the sessionStorage password cache (`sessionPasswordKey`) — the cookie is the session now; keep localStorage identity for prefill only.
- [x] Commit.

### Task 7: Client — Account settings section

**Files:** Modify `index.html` (settings dialog `#audioSettingsRegion` ~7365-7435 + JS).

- [x] Add an "account" section to the settings dialog: signed-in identity line; change password (current / new / confirm → `POST /auth/change-password`, inline success/error); passkeys list (`GET /auth/passkeys`) with "add a passkey on this device" (register ceremony) and per-item remove; "sign out" button (`POST /auth/logout`, then leaveRoom + reload to login gate).
- [x] Commit.

### Task 8: Verify end-to-end + docs

- [x] `go build ./... && go vet ./... && go test ./... -count=1` all green.
- [x] Keyless smoke test on :3100 (per bonfire-smoke-test ops memory): drive login as aj@shareability.com / B0NFIRE!, join room, open settings, change password, sign out, sign back in with new password, request reset (logged URL), complete reset. Playwright via browser tools.
- [x] Update README/env docs with `RESEND_API_KEY`, `RESEND_FROM`, `BONFIRE_USERS_PATH`, `BONFIRE_SESSIONS_PATH`; note `MEETING_ROOM_PASSWORD` now only seeds first-run passwords.
- [x] Final commit.

## Self-Review
- Spec coverage: 6 fixed users ✔ (Task 1 seed list), shared starter password ✔, change password ✔ (Tasks 2/7), passkeys ✔ (Tasks 4/6/7), no public signup ✔ (store only seeds; no register endpoint), Resend resets ✔ (Task 5/6).
- Names consistent: `userAccount`, `authenticateUser`, `userFromRequest`, `createUserSession`, `sendAccountEmail` used consistently across tasks.
