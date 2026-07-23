# BonfireOS iOS (Expo)

Native iOS client for the **live** [BonfireOS](https://thebonfire.xyz) web OS. Design and product language are rooted in the deployed Glass & Ink system (`index.html` + `.superdesign/design-system.md`) — not the older Swift package under `../apple`, which targeted an earlier build.

| Surface | How it works |
|---|---|
| **Auth** | Same `/auth/login`, `/auth/me`, `/auth/logout` roster sessions as the browser; login gate matches live wordmark + “Enter your office” |
| **Rooms** | `GET /rooms` — same room list the web lobby shows |
| **Chat** | `GET/POST /assistant/chat-threads` + Scout `/assistant/query` |
| **Board** | `GET /assistant/board` — same kanban cards |
| **Full OS** | Authenticated WebView of the **production SPA** so any deeper tool is the live design, not a fork |

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

## Design source of truth

1. **Live web:** `https://thebonfire.xyz` / repo `index.html` (tokens, login gate, tool labels).
2. **Canon notes:** `.superdesign/design-system.md` (Glass & Ink).
3. **Not authoritative:** `../apple` Swift client — older room-focused work; do not port its chrome or palette forward.

## Relationship to `apple/`

The Swift tree is historical room/WebRTC scaffolding. This Expo app is the current product path for TestFlight and day-to-day OS access against the live design.
