# BonfireOS Expo iOS client

## Purpose

Ship a native iOS app that is entirely interoperable with the web OS at
`https://thebonfire.xyz`: same accounts, same session model, same rooms / Scout
/ board data.

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

| Tab | Endpoint(s) |
|---|---|
| Login | `POST /auth/login`, `GET /auth/me`, `POST /auth/logout` |
| Rooms | `GET /rooms` |
| Scout | `GET/POST /assistant/chat-threads`, `POST /assistant/query` |
| Board | `GET /assistant/board` |
| Full OS | WebView of production SPA with session cookie injected |

## EAS / TestFlight

- Expo account: `axx_archive`
- Project: https://expo.dev/accounts/axx_archive/projects/bonfireos
- Project id: `0236310a-4904-4eaf-b1d4-a86e50e49d88`
- Bundle id: `xyz.thebonfire.app`

Apple distribution credentials must be set up interactively once:

```bash
cd mobile
npx eas-cli credentials
npx eas-cli build --platform ios --profile production
npx eas-cli submit --platform ios --profile production --latest
```

## Local checks

```bash
cd mobile && npm test && npx tsc --noEmit
go test ./... -count=1 -run 'TestNativeLogin|TestLoginSetsSession'
```
