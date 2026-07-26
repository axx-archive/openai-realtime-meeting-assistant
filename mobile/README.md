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
- Expo account with access to the **axxonlabs** org (paid plan — faster EAS queues)
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

**Project:** [@axxonlabs/bonfireos](https://expo.dev/accounts/axxonlabs/projects/bonfireos)
**EAS project id:** `30cd10a4-275d-45e3-8084-a1d7617b42f8`
**Owner org:** `axxonlabs` (paid — do not use free-tier `axx_archive`)
**Bundle id:** `xyz.thebonfire.app`

### One-time Apple setup (interactive — needs your Apple ID)

EAS cannot set distribution credentials non-interactively without an App Store Connect API key. On a machine where you can log into Apple:

```bash
cd mobile
npx eas-cli login          # any user that is Owner/Admin on axxonlabs
npx eas-cli credentials   # iOS → production → let EAS create/reuse dist cert + provisioning
```

In [App Store Connect](https://appstoreconnect.apple.com):

1. Create app **BonfireOS** with bundle id `xyz.thebonfire.app` if missing.
2. (Recommended) Create an **App Store Connect API key** (Users and Access → Keys) and upload it to EAS so future builds/submits are non-interactive.

### Build + TestFlight

```bash
cd mobile
npx --yes eas-cli@20.1.0 build \
  --platform ios \
  --profile production \
  --non-interactive \
  --wait
```

Record the exact build ID printed by EAS, inspect that artifact, and submit that
same ID only:

```bash
npx --yes eas-cli@20.1.0 submit \
  --platform ios \
  --profile production \
  --id <exact-build-id> \
  --non-interactive \
  --wait
```

Do not use `--latest` or `--auto-submit`; both remove the exact-artifact gate
between build inspection and TestFlight submission.

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
