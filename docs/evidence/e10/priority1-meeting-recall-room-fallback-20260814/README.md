# Priority-1 meeting recall and room fallback — 2026-08-14

This is local, synthetic evidence for the dirty E10 candidate based on HEAD
`9cfb43ed0d9c8f08df27429479ee5a44987a1b5d`. It is not a production,
TestFlight, signed-IPA or physical-device receipt.

## Product result

- Typed Scout, a session-bound private Realtime thread and a named private
  agent resolve the same current authorized Meeting Record briefing, sources
  and coverage. The named response retains the named agent identity.
- Shared-room voice remains behind the trusted qualification gate.
- When neither Scout nor a specialist voice route is qualified, web and native
  hide the room `Agent team` entry. Passive transcription, rolling analysis,
  meeting chat and explicit `@Scout what did I miss?` remain available.
- A qualified route or an agent already present in the current room retains
  the management control. Native status is fenced to the exact account and
  room; stale status cannot reopen it.

## Executable gates

- Focused normal Go: PASS
  - meeting briefing parity across typed/private-Realtime/named-agent seams
  - default-off room voice qualification
  - exact late-join `@Scout` meeting-chat fallback
  - desktop specialist and meeting-control contracts
- Focused race Go: PASS (`go test -race`, no findings)
- Rendered desktop/responsive control journey: PASS (`2.097s`, refreshed after
  the final room-focus/media-generation repairs)
  - 1440px light, 768px light and 320px dark
  - unqualified menu contains Chat/recap/transcript and no `Agent team`
  - synthetic qualified state can still open and return focus from the agent
    management panel
- Native suite: PASS (`559/559`)
- Native TypeScript: PASS (`tsc --noEmit`)
- `git diff --check`: PASS

## Captures

- `desktop-1440-light.png`
- `desktop-1440-light-menu.png`
- `desktop-768-light.png`
- `desktop-320-dark.png`
- `desktop-320-dark-menu.png`

The captures are rendered from the local `index.html` by
`TestDesktopMeetingControlIslandRenderedResponsiveFocusAndGuestChat`. Their
source and image hashes are recorded in `CANDIDATE-MANIFEST.txt` and
`SHA256SUMS`.

## Remaining release gates

No in-room voice provider is claimed qualified. No source-only or simulator
result may enable it. Qualification still requires the frozen real-room corpus,
latency/usefulness/source-grounding thresholds, a ten-sitting physical-device
soak and exact signed-release identity. Production remains on its separately
verified release until an explicitly authorized release operation occurs.
