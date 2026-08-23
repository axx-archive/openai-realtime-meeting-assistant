# BonfireOS Expo iOS client

## Purpose

Ship a native iOS app that is entirely interoperable with the **live** web OS at
`https://thebonfire.xyz`: same accounts, same session model, same rooms / Chat /
board data, and the **Glass & Ink** visual system from the deployed `index.html`.

Older work under `apple/` targeted an earlier build and is **not** the design or
product source of truth for this client.

## Layout

| Path | Role |
|---|---|
| `mobile/` | Expo SDK 57 app (React Native) |
| `apple/` | Swift package + Xcode room/WebRTC foundation (separate track) |
| `auth_http.go` | Cookie sessions + native `sessionToken` for Expo |

## Auth contract (interop)

1. Mobile sends `X-Bonfire-Client: expo` on every request.
2. `POST /auth/login` returns identity JSON **plus** `sessionToken` for native
   clients only (web browsers keep HttpOnly `bonfire_session` only).
3. Subsequent calls send:
   - `Authorization: Bearer <sessionToken>`
   - `X-Bonfire-Session: <sessionToken>`
4. `userFromRequest` accepts cookie **or** bearer/header — same session store
   as the web.

## Surfaces

| Tab (live label) | Endpoint(s) |
|---|---|
| Login (`bonfireOS` / Enter your office) | `POST /auth/login`, `GET /auth/me`, `POST /auth/logout` |
| BonfireOS (office) | Home + deep link into full OS |
| Rooms | `GET /rooms` |
| Chat | `GET/POST /assistant/chat-threads`, `POST /assistant/query` |
| Board | `GET /assistant/board` |
| Full OS | WebView of **production** SPA (live design, no fork) |

Tokens live in `mobile/src/theme/tokens.ts`, mirrored from `:root` in `index.html`.

Build 74's production profile opts the native client into private Realtime with
`EXPO_PUBLIC_NATIVE_REALTIME_VOICE_ENABLED=true`. The server remains an
independent fail-closed key: `/client-config` qualifies the launcher only when
`PRIVATE_REALTIME_VOICE_QUALIFIED=true` is explicitly installed by an exact VPS
activation. Either value can keep the surface dark. A false server process
refuses offers/tools, terminalizes the exact lease at its next 10-second Renew,
and retains the 30-second server TTL as final authority. Each native Renew is
bounded to at most five seconds and strictly before an exact-generation local
watchdog at `leaseExpiresAt - 3s`; the watchdog closes the peer and microphone
tracks synchronously before waiting for native audio deactivation. The env value
is process-scoped, so a production change also requires a receipted exact
container replacement. This source contract does not claim that activation,
provider acceptance, or physical iPhone/iPad audio acceptance occurred.

## EAS / TestFlight

- Expo account: `axxonlabs` (paid organization; do not submit from `axx_archive`)
- Project: https://expo.dev/accounts/axxonlabs/projects/bonfireos
- Project id: `30cd10a4-275d-45e3-8084-a1d7617b42f8`
- Bundle id: `xyz.thebonfire.app`

Apple distribution credentials must be set up interactively once:

```bash
cd mobile
npx --yes eas-cli@21.4.0 credentials
npx --yes eas-cli@21.4.0 build \
  --platform ios \
  --profile production \
  --non-interactive \
  --wait
npx --yes eas-cli@21.4.0 submit \
  --platform ios \
  --profile production \
  --id <exact-build-id> \
  --non-interactive \
  --wait
```

The submit ID must be the build ID returned by the immediately preceding,
inspected build. Never substitute `--latest` or `--auto-submit`.

## Local checks

```bash
cd mobile && npm test && npx tsc --noEmit
go test ./... -count=1 -run 'TestNativeLogin|TestLoginSetsSession'
```
