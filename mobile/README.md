# Stride iOS (Expo)

Native iOS client for **Stride**. Until the coordinated domain cutover, the live authenticated app remains at [thebonfire.xyz](https://thebonfire.xyz). Design and product language are rooted in the deployed Glass & Ink system (`index.html` + `.superdesign/design-system.md`) — not the older Swift package under `../apple`, which targeted an earlier build.

| Surface | How it works |
|---|---|
| **Auth** | Same `/auth/login`, `/auth/me`, `/auth/logout` roster sessions as the browser; login gate matches live wordmark + “Enter your office” |
| **Rooms** | `GET /rooms` — same room list the web lobby shows |
| **Chat** | `GET/POST /assistant/chat-threads` + Scout `/assistant/query` |
| **Scout voice** | Native private Realtime 2.1 through `/assistant/realtime-offer`; the production Build 76 profile enables it, while tools, Brain context, thread receipts, usage, and ACLs stay server-authoritative |
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

Build 76 enables native private Realtime in the production EAS profile. Home
and the signed-in tab shell expose the same voice transport: one private Scout
thread stays bound across navigation, entering a room yields microphone focus,
and backgrounding ends private capture. Local/ad-hoc builds remain default-off
unless `EXPO_PUBLIC_NATIVE_REALTIME_VOICE_ENABLED=true` is supplied. That client
flag is only the first key: the signed-in `/client-config` response must also
project `privateRealtimeVoiceQualified: true`, which the server emits only when
`PRIVATE_REALTIME_VOICE_QUALIFIED=true`. Either key off hides/stops the launcher.
The server env value takes effect on a receipted container replacement; once
false, offers and tool effects fail closed, Renew terminalizes the exact lease
within the foreground 10-second cadence. Every Renew has a dynamic deadline of
at most five seconds, always before an exact-generation local watchdog at three
seconds before the server-provided lease expiry. That watchdog closes the peer
and microphone tracks synchronously even if Renew or native deactivation never
settles; the 30-second server TTL remains the final authority bound. Source
qualification does not claim live activation, provider acceptance, or physical
iPhone/iPad audio acceptance.

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
npx --yes eas-cli@21.4.0 login          # any user that is Owner/Admin on axxonlabs
npx --yes eas-cli@21.4.0 credentials   # iOS → production → let EAS create/reuse dist cert + provisioning
```

In [App Store Connect](https://appstoreconnect.apple.com):

1. Use the existing App Store Connect record with bundle id `xyz.thebonfire.app`; update its customer-facing name to **Stride** only as part of the coordinated release.
2. (Recommended) Create an **App Store Connect API key** (Users and Access → Keys) and upload it to EAS so future builds/submits are non-interactive.

### Build + TestFlight

```bash
cd mobile
npx --yes eas-cli@21.4.0 build \
  --platform ios \
  --profile production \
  --non-interactive \
  --wait
```

Record the exact build ID printed by EAS, inspect that artifact, and submit that
same ID only:

```bash
npx --yes eas-cli@21.4.0 submit \
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

- **Display name:** Stride
- **Slug:** bonfireos
- **iOS bundle id:** `xyz.thebonfire.app`
- **Default API host:** `https://thebonfire.xyz`

The slug, bundle id, API host, session headers, and EAS project remain compatibility identifiers during the brand transition. Changing them is a separate migration, not a visual rename.

## Design source of truth

1. **Live web:** `https://thebonfire.xyz` / repo `index.html` (tokens, login gate, tool labels).
2. **Canon notes:** `.superdesign/design-system.md` (Glass & Ink).
3. **Not authoritative:** `../apple` Swift client — older room-focused work; do not port its chrome or palette forward.

## Relationship to `apple/`

The Swift tree is historical room/WebRTC scaffolding. This Expo app is the current product path for TestFlight and day-to-day OS access against the live design.
