# BonfireOS iOS (Expo)

Native iOS client for [BonfireOS / STRIDE](https://thebonfire.xyz) that is fully interoperable with the web app:

| Surface | How it works |
|---|---|
| **Auth** | Same `/auth/login`, `/auth/me`, `/auth/logout` roster sessions as the browser |
| **Rooms** | `GET /rooms` — same room list the web lobby shows |
| **Scout** | `GET/POST /assistant/chat-threads` + `/assistant/query` |
| **Board** | `GET /assistant/board` — same kanban cards |
| **Full OS** | Authenticated WebView of the production SPA for any deeper surface |

Native clients send `X-Bonfire-Client: expo` so the server returns a `sessionToken` in the login JSON (HttpOnly cookies are not reliably visible to React Native). Subsequent API calls use `Authorization: Bearer <token>` / `X-Bonfire-Session`. That token is the same session id the web stores in `bonfire_session`.

## Prerequisites

- Node 20+
- Expo account (this project is owned by `axx_archive`)
- Apple Developer membership for TestFlight
- Backend with mobile session support deployed (`sessionToken` on native login)

## Local development

```bash
cd mobile
npm install
npm start
# press i for iOS simulator
```

Point at a local server:

```bash
EXPO_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 \
EXPO_PUBLIC_WEB_APP_URL=http://127.0.0.1:8080 \
npm start
```

## Typecheck

```bash
npx tsc --noEmit
```

## EAS / TestFlight

**Project:** [@axx_archive/bonfireos](https://expo.dev/accounts/axx_archive/projects/bonfireos)  
**EAS project id:** `0236310a-4904-4eaf-b1d4-a86e50e49d88`  
**Bundle id:** `xyz.thebonfire.app`

### One-time Apple setup (interactive — needs your Apple ID)

EAS cannot set distribution credentials non-interactively without an App Store Connect API key. On a machine where you can log into Apple:

```bash
cd mobile
npx eas-cli login          # axx_archive
npx eas-cli credentials   # iOS → production → let EAS create/reuse dist cert + provisioning
```

In [App Store Connect](https://appstoreconnect.apple.com):

1. Create app **BonfireOS** with bundle id `xyz.thebonfire.app` if missing.
2. (Recommended) Create an **App Store Connect API key** (Users and Access → Keys) and upload it to EAS so future builds/submits are non-interactive.

### Build + TestFlight

```bash
cd mobile
npx eas-cli build --platform ios --profile production
# when the build finishes:
npx eas-cli submit --platform ios --profile production --latest
```

Or one shot after credentials exist:

```bash
npx eas-cli build --platform ios --profile production --auto-submit
```

Credentials (certificates, provisioning, App Store Connect API key) are managed by EAS and never committed.

## Bundle identity

- **Name:** BonfireOS  
- **Slug:** bonfireos  
- **iOS bundle id:** `xyz.thebonfire.app`  
- **Default API host:** `https://thebonfire.xyz`

## Relationship to `apple/`

The Swift package under `../apple` is the protocol-first native room/WebRTC foundation. This Expo app is the product shell for day-to-day OS access and TestFlight distribution. Both speak the same backend contracts.
