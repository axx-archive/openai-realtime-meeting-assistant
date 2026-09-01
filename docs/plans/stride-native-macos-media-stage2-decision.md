# STRIDE native macOS media stage 2 decision

Date: 2026-08-31
Status: accepted for a local-QA vertical slice; WebKit remains the default until the evidence gate passes

This document decides meeting-media ownership only. Keeping WebKit as the
default meeting path until physical evidence passes does not make WKWebView the
permanent Mac product architecture. The Mac app is now directed toward a
first-class native desktop composition, while the browser remains a separate
first-class network client; both retain shared service and protocol contracts.

## Decision

STRIDE will not attempt to inject native audio or video into a JavaScript `MediaStream` in `WKWebView`. The public macOS WebKit surface can grant or stop page capture and exchange property-list messages, but it has no native media-track insertion API. Passing media, SDP, ICE, credentials, or device identity through evaluated JavaScript would be brittle and would expand the trust boundary.

The first safe slice is therefore an explicit first-class native room surface inside the existing polished Mac shell. The already-present `LiveKitWebRTC` XCFramework owns capture, Apple Voice Processing/WebRTC APM, peer connection, device routing, stats, and teardown. Existing native room packages own the current room signaling protocol. The web workspace remains mounted for fresh product surfaces, authentication, and the proven WebKit meeting fallback, but it never captures while the native room is active.

## Ownership contract

- A user chooses Web meeting or Native media before joining. There is no automatic mid-call owner switch.
- Native mode owns microphone, camera, output route, processing, peer connection, recovery, and leave cleanup. WebKit capture must be `none` before native admission.
- Web mode remains unchanged and native media is not initialized.
- The native room uses the existing `participant`, `media_ready`, `offer`, `answer`, `candidate`, `participant_media_state`, `restart_ice`, and screen-share events. No second participant identity or signaling socket is created for one native join.
- Native mute, camera, screen share, device choice, processing truth, degradation, and recovery state are rendered by the native room surface. A future web-control bridge must be server-authored, versioned, origin-checked, allow-listed, and state-only; it must never transport raw media or negotiation secrets.
- If native initialization fails before `media_ready`, native capture and signaling are torn down and the user can explicitly return to the unchanged WebKit path. A failure after admission stays native and is reported; it never silently falls back.

## Dependency decision

Reuse `LiveKitWebRTC` `144.7559.10`, already pinned and used by the iPhone/iPad implementation. Its macOS XCFramework contains arm64 and x86_64 slices, exposes input/output selection plus Apple Voice Processing and WebRTC APM requested/resolved/active state, and is MIT-licensed. The downloaded XCFramework is about 141 MB across all platforms and its macOS slice about 28 MB before app packaging. Local QA may use ad-hoc signing; Developer ID, hardened-runtime verification, notarization, stapling, the signed Sparkle feed, and privacy review remain separate public-distribution gates.

## Evidence gate

Native mode is not better merely because it is native. Keep WebKit as the safer architecture unless a same-Mac A/B run and a real mixed-client room prove a material benefit in at least one user outcome (processing quality, device handling/recovery, reliability, or efficiency) without a material regression elsewhere. Evidence must be aggregate and sanitized: no raw media, SDP, ICE credentials, device names, account data, or secrets.

The local-QA slice must prove one active owner, audio/video interoperability, no phantom seat after leave, denied/unavailable-device recovery, truthful processing fallback, installed-DMG behavior, and unchanged WebKit fallback. Screen sharing may remain explicitly unavailable only if it is labeled truthfully; the existing native stack's public macOS screen-capture implementation should be retained if it passes the same gate.

## Local QA result

Build 21 preserves the current shell, exposes the native room as a truthful preview in the same `STRIDE.app` bundle, and makes the web canvas edge-to-edge in full screen so native and web borders do not compete. Identity-free discovery, authenticated login, the versioned room socket, server offer delivery, explicit microphone/camera authorization, fixed publisher MIDs, bounded WebRTC operations, device/runtime truth, single-owner gating, and teardown are integrated. Capture shutdown is a fail-closed, process-sticky gate, native desktop capture reports asynchronous start/stop/error state through the coordinator, and the A/B harness requires complete source and artifact identity before it can report material benefit. A loopback signaling test received a real server publisher offer, and an earlier installed candidate entered and then removed exactly one seat.

An earlier pre-review candidate was Apple Development signed with the hardened runtime and installed at `/Applications/STRIDE.app`; it retained the build 18 sidebar, WebKit workspace, and explicit return-to-Web control. That artifact is obsolete and must not be shared because later security and lifecycle corrections changed the source. A fresh local-QA artifact must be built only from the final reviewed commit and its receipt must bind that exact commit and source fingerprint. In an earlier physical permission run, macOS did not resolve the microphone authorization request; the bounded request timed out after 15 seconds, tore down the single admitted seat, reported the unresolved permission honestly, and returned only by explicit user action to the working WebKit surface. No exact final artifact has completed a physical native audio call. Therefore mixed browser/mobile interoperability, physical native camera and screen sharing, and controlled same-Mac A/B/BA quality remain unproven. There is no measured basis to call the native path materially better, so WebKit remains the default and this slice is not ready for public distribution.
